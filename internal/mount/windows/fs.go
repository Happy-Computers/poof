//go:build windows

package windows

import (
	"context"
	"errors"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/amaan/infinity-storage/internal/cacheclient"
	"github.com/amaan/infinity-storage/internal/catalog"
	"github.com/amaan/infinity-storage/internal/ingest"
	"github.com/amaan/infinity-storage/internal/proxypool"
	"github.com/amaan/infinity-storage/internal/s3origin"
	"github.com/winfsp/cgofuse/fuse"
)

type CatalogLoader func(ctx context.Context) ([]catalog.Entry, error)

type InfinityStorageFS struct {
	fuse.FileSystemBase

	pool   *proxypool.Pool
	ingest *ingest.Manager

	mu           sync.Mutex
	entries      map[string]catalog.Entry
	load         CatalogLoader
	lastRefresh  time.Time
	pollInterval time.Duration
	stopPoll     chan struct{}
	pollOnce     sync.Once

	single     *cacheclient.Client
	singleName string
	singleSize uint64

	handles    map[uint64]*openHandle
	nextHandle uint64
}

type ByteSource interface {
	ReadAt(dest []byte, offset uint64) (int, error)
}

type openHandle struct {
	source  ByteSource
	live    catalog.Source
	writer  *ingest.File
	release func()
	size    uint64
}

func NewMulti(entries []catalog.Entry, pool *proxypool.Pool, load CatalogLoader) *InfinityStorageFS {
	return newMulti(entries, pool, load, nil)
}

func NewMultiWritable(entries []catalog.Entry, pool *proxypool.Pool, load CatalogLoader, manager *ingest.Manager) *InfinityStorageFS {
	filesystem := newMulti(entries, pool, load, manager)
	filesystem.pollInterval = 250 * time.Millisecond
	return filesystem
}

func newMulti(entries []catalog.Entry, pool *proxypool.Pool, load CatalogLoader, manager *ingest.Manager) *InfinityStorageFS {
	mapped := make(map[string]catalog.Entry, len(entries))
	for _, entry := range entries {
		mapped[entry.Name] = entry
	}
	filesystem := &InfinityStorageFS{
		pool:         pool,
		ingest:       manager,
		entries:      mapped,
		load:         load,
		pollInterval: 2 * time.Second,
		stopPoll:     make(chan struct{}),
		handles:      make(map[uint64]*openHandle),
		nextHandle:   1,
	}
	if load != nil {
		go filesystem.pollCatalog()
	}
	return filesystem
}

func NewSingle(client *cacheclient.Client, name string, size uint64) *InfinityStorageFS {
	return &InfinityStorageFS{
		single:     client,
		singleName: name,
		singleSize: size,
		handles:    make(map[uint64]*openHandle),
		nextHandle: 1,
		stopPoll:   make(chan struct{}),
	}
}

func (f *InfinityStorageFS) Stop() {
	f.pollOnce.Do(func() {
		close(f.stopPoll)
	})
}

func (f *InfinityStorageFS) Init() {
	f.FileSystemBase.Init()
}

func (f *InfinityStorageFS) Destroy() {
	f.Stop()
	f.FileSystemBase.Destroy()
}

func (f *InfinityStorageFS) Getattr(p string, stat *fuse.Stat_t, fh uint64) int {
	p = cleanPath(p)
	if p == "/" {
		stat.Mode = fuse.S_IFDIR | 0755
		return 0
	}
	name := strings.TrimPrefix(p, "/")
	if strings.Contains(name, "/") {
		return -fuse.ENOENT
	}
	if f.single != nil {
		if name != f.singleName {
			return -fuse.ENOENT
		}
		stat.Mode = fuse.S_IFREG | 0444
		stat.Size = int64(f.singleSize)
		return 0
	}
	f.maybeRefresh()
	f.mu.Lock()
	entry, ok := f.entries[name]
	f.mu.Unlock()
	if !ok {
		return -fuse.ENOENT
	}
	stat.Mode = fuse.S_IFREG | 0444
	stat.Size = int64(entry.Size)
	if entry.Source != nil {
		stat.Mode = fuse.S_IFREG | 0644
		stat.Size = int64(entry.Source.Size())
	}
	return 0
}

func (f *InfinityStorageFS) Open(p string, flags int) (int, uint64) {
	if flags&3 != 0 {
		return -fuse.EROFS, ^uint64(0)
	}
	p = cleanPath(p)
	name := strings.TrimPrefix(p, "/")
	if name == "" || strings.Contains(name, "/") {
		return -fuse.ENOENT, ^uint64(0)
	}
	if f.single != nil {
		if name != f.singleName {
			return -fuse.ENOENT, ^uint64(0)
		}
		return f.addHandle(&openHandle{source: f.single, size: f.singleSize})
	}
	f.maybeRefresh()
	f.mu.Lock()
	entry, ok := f.entries[name]
	f.mu.Unlock()
	if !ok {
		return -fuse.ENOENT, ^uint64(0)
	}
	if entry.Source != nil {
		return f.addHandle(&openHandle{source: entry.Source, live: entry.Source})
	}
	client, release, err := f.pool.Acquire(entry)
	if err != nil {
		if errors.Is(err, proxypool.ErrBusy) {
			return -fuse.EBUSY, ^uint64(0)
		}
		return -fuse.EIO, ^uint64(0)
	}
	return f.addHandle(&openHandle{source: client, release: release, size: entry.Size})
}

func (f *InfinityStorageFS) addHandle(handle *openHandle) (int, uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	handleID := f.nextHandle
	f.nextHandle++
	f.handles[handleID] = handle
	return 0, handleID
}

func (f *InfinityStorageFS) Create(p string, flags int, mode uint32) (int, uint64) {
	_ = flags
	_ = mode
	if f.ingest == nil {
		return -fuse.EROFS, ^uint64(0)
	}
	name := strings.TrimPrefix(cleanPath(p), "/")
	if name == "" || strings.Contains(name, "/") {
		return -fuse.EINVAL, ^uint64(0)
	}
	f.mu.Lock()
	_, exists := f.entries[name]
	f.mu.Unlock()
	if exists {
		return -fuse.EEXIST, ^uint64(0)
	}
	file, err := f.ingest.Reserve(name)
	if err != nil {
		return windowsIngestErrno(err), ^uint64(0)
	}
	f.mu.Lock()
	f.entries[name] = catalog.Entry{Name: name, Source: file}
	handleID := f.nextHandle
	f.nextHandle++
	f.handles[handleID] = &openHandle{writer: file}
	f.mu.Unlock()
	return 0, handleID
}

func (f *InfinityStorageFS) Release(p string, handleID uint64) int {
	_ = p
	f.mu.Lock()
	handle, ok := f.handles[handleID]
	if ok {
		delete(f.handles, handleID)
	}
	f.mu.Unlock()
	if !ok {
		return -fuse.EBADF
	}
	if handle.release != nil {
		handle.release()
	}
	if handle.writer != nil {
		return windowsIngestErrno(handle.writer.Close())
	}
	return 0
}

func (f *InfinityStorageFS) Read(p string, buff []byte, offset int64, handleID uint64) int {
	_ = p
	if offset < 0 {
		return 0
	}
	f.mu.Lock()
	handle, ok := f.handles[handleID]
	f.mu.Unlock()
	if !ok || handle.source == nil {
		return 0
	}
	size := handle.size
	if handle.live != nil {
		size = handle.live.Size()
	}
	if uint64(offset) >= size {
		return 0
	}
	remaining := size - uint64(offset)
	want := uint64(len(buff))
	if want > remaining {
		want = remaining
	}
	got, err := handle.source.ReadAt(buff[:want], uint64(offset))
	if err != nil {
		return 0
	}
	return got
}

func (f *InfinityStorageFS) Write(p string, buff []byte, offset int64, handleID uint64) int {
	_ = p
	if offset < 0 {
		return -fuse.EINVAL
	}
	f.mu.Lock()
	handle, ok := f.handles[handleID]
	f.mu.Unlock()
	if !ok || handle.writer == nil {
		return -fuse.EBADF
	}
	written, err := handle.writer.WriteAt(buff, uint64(offset))
	if err != nil {
		return windowsIngestErrno(err)
	}
	return written
}

func (f *InfinityStorageFS) Flush(p string, handleID uint64) int {
	_ = p
	f.mu.Lock()
	handle, ok := f.handles[handleID]
	f.mu.Unlock()
	if !ok || handle.writer == nil {
		return 0
	}
	return windowsIngestErrno(handle.writer.Flush())
}

func (f *InfinityStorageFS) Fsync(p string, datasync bool, handleID uint64) int {
	_ = datasync
	return f.Flush(p, handleID)
}

func (f *InfinityStorageFS) Readdir(p string, fill func(name string, stat *fuse.Stat_t, offset int64) bool, offset int64, handleID uint64) int {
	_ = offset
	_ = handleID
	if cleanPath(p) != "/" {
		return -fuse.ENOENT
	}
	fill(".", nil, 0)
	fill("..", nil, 0)
	if f.single != nil {
		fill(f.singleName, nil, 0)
		return 0
	}
	f.maybeRefresh()
	f.mu.Lock()
	names := make([]string, 0, len(f.entries))
	for name := range f.entries {
		names = append(names, name)
	}
	f.mu.Unlock()
	for _, name := range names {
		if !fill(name, nil, 0) {
			break
		}
	}
	return 0
}

func (f *InfinityStorageFS) maybeRefresh() {
	if f.load == nil {
		return
	}
	f.mu.Lock()
	due := time.Since(f.lastRefresh) >= time.Duration(s3origin.MinCatalogRefresh)*time.Second
	f.mu.Unlock()
	if due {
		f.refresh(context.Background())
	}
}

func (f *InfinityStorageFS) pollCatalog() {
	ticker := time.NewTicker(f.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-f.stopPoll:
			return
		case <-ticker.C:
			f.refresh(context.Background())
		}
	}
}

func (f *InfinityStorageFS) refresh(ctx context.Context) {
	if f.load == nil {
		return
	}
	entries, err := f.load(ctx)
	if err != nil {
		return
	}
	wanted := make(map[string]catalog.Entry, len(entries))
	for _, entry := range entries {
		wanted[entry.Name] = entry
	}
	if f.ingest != nil {
		for _, entry := range f.ingest.Entries() {
			wanted[entry.Name] = entry
		}
	}
	f.mu.Lock()
	f.lastRefresh = time.Now()
	f.entries = wanted
	f.mu.Unlock()
}

func windowsIngestErrno(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, ingest.ErrNonSequential) {
		return -fuse.EINVAL
	}
	if errors.Is(err, ingest.ErrSpoolFull) || errors.Is(err, ingest.ErrFileTooLarge) {
		return -fuse.ENOSPC
	}
	if errors.Is(err, ingest.ErrActiveWriteLimit) || errors.Is(err, ingest.ErrCatalogFull) {
		return -fuse.EBUSY
	}
	if errors.Is(err, ingest.ErrNameExists) {
		return -fuse.EEXIST
	}
	return -fuse.EIO
}

func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	p = path.Clean("/" + strings.TrimPrefix(p, "/"))
	if p == "." {
		return "/"
	}
	return p
}
