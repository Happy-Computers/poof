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
	"github.com/winfsp/cgofuse/fuse"
)

type CatalogLoader func(ctx context.Context) ([]catalog.Entry, error)

const filesystemBlockBytes = 4 * 1024

type InfinityStorageFS struct {
	fuse.FileSystemBase

	pool   *proxypool.Pool
	ingest *ingest.Manager

	mu           sync.Mutex
	entries      map[string]catalog.Entry
	load         CatalogLoader
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

func (f *InfinityStorageFS) Statfs(path string, stat *fuse.Statfs_t) int {
	_ = path
	stat.Bsize = filesystemBlockBytes
	stat.Frsize = filesystemBlockBytes
	stat.Blocks = ingest.MaxSpoolBytes / filesystemBlockBytes
	stat.Files = ingest.MaxCatalogFiles
	stat.Namemax = ingest.MaxBasenameBytes
	if f.ingest == nil {
		return 0
	}
	f.mu.Lock()
	fileCount := len(f.entries)
	f.mu.Unlock()
	stat.Bfree = stat.Blocks
	stat.Bavail = stat.Blocks
	if fileCount < ingest.MaxCatalogFiles {
		stat.Ffree = uint64(ingest.MaxCatalogFiles - fileCount)
		stat.Favail = stat.Ffree
	}
	return 0
}

func fillOwner(stat *fuse.Stat_t) {
	uid, gid, _ := fuse.Getcontext()
	stat.Uid = uid
	stat.Gid = gid
}

func (f *InfinityStorageFS) Access(p string, mask uint32) int {
	_ = p
	_ = mask
	return 0
}

func (f *InfinityStorageFS) Utimens(p string, tmsp []fuse.Timespec) int {
	_ = p
	_ = tmsp
	return 0
}

func (f *InfinityStorageFS) Getattr(p string, stat *fuse.Stat_t, fh uint64) int {
	_ = fh
	p = cleanPath(p)
	if p == "/" {
		stat.Mode = fuse.S_IFDIR | 0777
		fillOwner(stat)
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
		fillOwner(stat)
		return 0
	}
	f.mu.Lock()
	entry, ok := f.entries[name]
	f.mu.Unlock()
	if !ok {
		return -fuse.ENOENT
	}
	stat.Mode = fuse.S_IFREG | 0444
	stat.Size = int64(entry.Size)
	if entry.Source != nil {
		stat.Mode = fuse.S_IFREG | 0666
		stat.Size = int64(entry.Source.Size())
	}
	fillOwner(stat)
	return 0
}

func (f *InfinityStorageFS) Open(p string, flags int) (int, uint64) {
	wantWrite := flags&3 != 0
	p = cleanPath(p)
	name := strings.TrimPrefix(p, "/")
	if name == "" || strings.Contains(name, "/") {
		if wantWrite {
			return -fuse.EINVAL, ^uint64(0)
		}
		return -fuse.ENOENT, ^uint64(0)
	}
	if f.single != nil {
		if wantWrite {
			return -fuse.EROFS, ^uint64(0)
		}
		if name != f.singleName {
			return -fuse.ENOENT, ^uint64(0)
		}
		return f.addHandle(&openHandle{source: f.single, size: f.singleSize})
	}
	if wantWrite {
		return f.openForWrite(name)
	}
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

func (f *InfinityStorageFS) openForWrite(name string) (int, uint64) {
	if f.ingest == nil {
		return -fuse.EROFS, ^uint64(0)
	}
	f.mu.Lock()
	entry, exists := f.entries[name]
	f.mu.Unlock()
	if exists {
		if writer, ok := entry.Source.(*ingest.File); ok {
			if err := writer.AddWriter(); err != nil {
				return windowsIngestErrno(err), ^uint64(0)
			}
			return f.addHandle(&openHandle{writer: writer, source: writer, live: writer})
		}
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
	f.handles[handleID] = &openHandle{writer: file, source: file, live: file}
	f.mu.Unlock()
	return 0, handleID
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
	entry, exists := f.entries[name]
	f.mu.Unlock()
	if exists {
		if writer, ok := entry.Source.(*ingest.File); ok {
			snap := writer.Snapshot()
			if snap.Size == 0 && (snap.State == ingest.StateInterrupted || snap.State == ingest.StateAborted) {
				_ = writer.Abort(context.Background())
				f.mu.Lock()
				delete(f.entries, name)
				f.mu.Unlock()
			} else {
				return -fuse.EEXIST, ^uint64(0)
			}
		} else {
			return -fuse.EEXIST, ^uint64(0)
		}
	}
	file, err := f.ingest.Reserve(name)
	if err != nil {
		return windowsIngestErrno(err), ^uint64(0)
	}
	f.mu.Lock()
	f.entries[name] = catalog.Entry{Name: name, Source: file}
	handleID := f.nextHandle
	f.nextHandle++
	f.handles[handleID] = &openHandle{writer: file, source: file, live: file}
	f.mu.Unlock()
	return 0, handleID
}

func (f *InfinityStorageFS) Unlink(p string) int {
	if f.ingest == nil {
		return -fuse.EROFS
	}
	name := strings.TrimPrefix(cleanPath(p), "/")
	if name == "" || strings.Contains(name, "/") {
		return -fuse.EINVAL
	}
	f.mu.Lock()
	entry, exists := f.entries[name]
	f.mu.Unlock()
	if !exists {
		if file, ok := f.ingest.Lookup(name); ok {
			if err := file.Abort(context.Background()); err != nil {
				return windowsIngestErrno(err)
			}
			return 0
		}
		return -fuse.ENOENT
	}
	if writer, ok := entry.Source.(*ingest.File); ok {
		if err := writer.Abort(context.Background()); err != nil {
			return windowsIngestErrno(err)
		}
		f.mu.Lock()
		delete(f.entries, name)
		f.mu.Unlock()
		return 0
	}
	return -fuse.EROFS
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
		name := handle.writer.Snapshot().Name
		err := handle.writer.Close()
		snap := handle.writer.Snapshot()
		if snap.State == ingest.StateAborted {
			f.mu.Lock()
			if entry, exists := f.entries[name]; exists {
				if src, ok := entry.Source.(*ingest.File); ok && src == handle.writer {
					delete(f.entries, name)
				}
			}
			f.mu.Unlock()
		}
		return windowsIngestErrno(err)
	}
	return 0
}

func (f *InfinityStorageFS) Read(p string, buff []byte, offset int64, handleID uint64) int {
	_ = p
	if offset < 0 {
		return -fuse.EINVAL
	}
	f.mu.Lock()
	handle, ok := f.handles[handleID]
	f.mu.Unlock()
	if !ok || handle.source == nil {
		return -fuse.EBADF
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
		return -fuse.EIO
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

func (f *InfinityStorageFS) Truncate(p string, size int64, handleID uint64) int {
	if size < 0 {
		return -fuse.EINVAL
	}
	var writer *ingest.File
	if handleID != 0 && handleID != ^uint64(0) {
		f.mu.Lock()
		handle, ok := f.handles[handleID]
		f.mu.Unlock()
		if !ok || handle.writer == nil {
			return -fuse.EBADF
		}
		writer = handle.writer
	} else {
		name := strings.TrimPrefix(cleanPath(p), "/")
		if name == "" || strings.Contains(name, "/") {
			return -fuse.EINVAL
		}
		f.mu.Lock()
		entry, ok := f.entries[name]
		f.mu.Unlock()
		if !ok {
			return -fuse.ENOENT
		}
		writer, ok = entry.Source.(*ingest.File)
		if !ok {
			return -fuse.EROFS
		}
	}
	accepted := writer.Size()
	if uint64(size) == accepted {
		return 0
	}
	if accepted == 0 && size == 0 {
		return 0
	}
	return -fuse.EINVAL
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
