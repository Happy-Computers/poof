//go:build linux

// Package linux is the Linux FUSE volume backend for Infinity Storage.
package linux

import (
	"context"
	"errors"
	"sync"
	"syscall"
	"time"

	"github.com/amaan/infinity-storage/internal/cacheclient"
	"github.com/amaan/infinity-storage/internal/catalog"
	"github.com/amaan/infinity-storage/internal/ingest"
	"github.com/amaan/infinity-storage/internal/proxypool"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// ByteSource provides ranged reads for a single object (single-file mount).
type ByteSource interface {
	ReadAt(dest []byte, offset uint64) (int, error)
}

// CatalogLoader re-lists Infinity Storage entries (S3). Nil means static catalog (--dir).
type CatalogLoader func(ctx context.Context) ([]catalog.Entry, error)

// RootSingle is the mount root containing one read-only file (--uds mode).
type RootSingle struct {
	fs.Inode
	client   *cacheclient.Client
	fileName string
	size     uint64
}

// NewRootSingle builds a single-file Infinity Storage root.
func NewRootSingle(client *cacheclient.Client, fileName string, size uint64) *RootSingle {
	return &RootSingle{client: client, fileName: fileName, size: size}
}

// NewRoot is an alias for NewRootSingle (backward compatible).
func NewRoot(client *cacheclient.Client, fileName string, size uint64) *RootSingle {
	return NewRootSingle(client, fileName, size)
}

func (r *RootSingle) OnAdd(ctx context.Context) {
	ch := r.NewPersistentInode(ctx, &singleFile{
		client: r.client,
		size:   r.size,
	}, fs.StableAttr{Mode: syscall.S_IFREG, Ino: 2})
	r.AddChild(r.fileName, ch, false)
}

func (r *RootSingle) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0555
	return 0
}

var _ = (fs.NodeOnAdder)((*RootSingle)(nil))
var _ = (fs.NodeGetattrer)((*RootSingle)(nil))

type singleFile struct {
	fs.Inode
	client *cacheclient.Client
	size   uint64
}

func (f *singleFile) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0444
	out.Size = f.size
	return 0
}

func (f *singleFile) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (f *singleFile) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	return readFrom(f.client, f.size, dest, off)
}

var _ = (fs.NodeGetattrer)((*singleFile)(nil))
var _ = (fs.NodeOpener)((*singleFile)(nil))
var _ = (fs.NodeReader)((*singleFile)(nil))

// RootMulti is a flat multi-file Infinity Storage root.
type RootMulti struct {
	fs.Inode
	pool   *proxypool.Pool
	ingest *ingest.Manager

	mu           sync.Mutex
	entries      map[string]catalog.Entry
	load         CatalogLoader
	nextIno      uint64
	pollInterval time.Duration
	stopPoll     chan struct{}
	pollOnce     sync.Once
}

// NewRootMulti builds a static multi-file root (--dir harness).
func NewRootMulti(entries []catalog.Entry, pool *proxypool.Pool) *RootMulti {
	return newRootMulti(entries, pool, nil, nil)
}

func NewRootMultiLive(entries []catalog.Entry, pool *proxypool.Pool, load CatalogLoader) *RootMulti {
	return newRootMulti(entries, pool, load, nil)
}

func NewRootMultiWritable(entries []catalog.Entry, pool *proxypool.Pool, load CatalogLoader, manager *ingest.Manager) *RootMulti {
	root := newRootMulti(entries, pool, load, manager)
	root.pollInterval = 250 * time.Millisecond
	return root
}

func newRootMulti(entries []catalog.Entry, pool *proxypool.Pool, load CatalogLoader, manager *ingest.Manager) *RootMulti {
	m := make(map[string]catalog.Entry, len(entries))
	for _, e := range entries {
		m[e.Name] = e
	}
	r := &RootMulti{
		pool:         pool,
		ingest:       manager,
		entries:      m,
		load:         load,
		nextIno:      2,
		pollInterval: 2 * time.Second,
		stopPoll:     make(chan struct{}),
	}
	return r
}

func (r *RootMulti) Stop() {
	r.pollOnce.Do(func() {
		close(r.stopPoll)
	})
}

func (r *RootMulti) OnAdd(ctx context.Context) {
	r.mu.Lock()
	for _, e := range r.entries {
		r.addChildLocked(ctx, e)
	}
	r.mu.Unlock()

	if r.load != nil {
		go r.pollCatalog()
	}
}

func (r *RootMulti) addChildLocked(ctx context.Context, e catalog.Entry) {
	ino := r.nextIno
	r.nextIno++
	mode := uint32(syscall.S_IFREG | 0444)
	if e.Source != nil {
		mode = syscall.S_IFREG | 0644
	}
	ch := r.NewPersistentInode(ctx, &multiFile{
		root: r,
		name: e.Name,
		pool: r.pool,
	}, fs.StableAttr{Mode: mode, Ino: ino})
	r.AddChild(e.Name, ch, true)
}

func (r *RootMulti) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0755
	return 0
}

func (r *RootMulti) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	if r.ingest == nil {
		return nil, nil, 0, syscall.EROFS
	}
	r.mu.Lock()
	_, exists := r.entries[name]
	r.mu.Unlock()
	if exists {
		return nil, nil, 0, syscall.EEXIST
	}
	file, err := r.ingest.Reserve(name)
	if err != nil {
		return nil, nil, 0, ingestErrno(err)
	}
	r.mu.Lock()
	r.entries[name] = catalog.Entry{Name: name, Source: file}
	r.addChildLocked(ctx, r.entries[name])
	child := r.GetChild(name)
	r.mu.Unlock()
	if child == nil {
		return nil, nil, 0, syscall.EIO
	}
	return child, &writeHandle{file: file}, fuse.FOPEN_DIRECT_IO, 0
}

func (r *RootMulti) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if ch := r.GetChild(name); ch != nil {
		if mf, ok := ch.Operations().(*multiFile); ok {
			entry, exists := mf.entry()
			if exists {
				out.Mode = 0444
				out.Size = entry.Size
				if entry.Source != nil {
					out.Mode = 0644
					out.Size = entry.Source.Size()
				}
			}
		}
		return ch, 0
	}
	return nil, syscall.ENOENT
}

func (r *RootMulti) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := make([]fuse.DirEntry, 0, len(r.entries))
	for name := range r.entries {
		mode := uint32(syscall.S_IFREG | 0444)
		if r.entries[name].Source != nil {
			mode = syscall.S_IFREG | 0644
		}
		list = append(list, fuse.DirEntry{Name: name, Mode: mode})
	}
	return fs.NewListDirStream(list), 0
}

func (r *RootMulti) pollCatalog() {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopPoll:
			return
		case <-ticker.C:
			r.refresh(context.Background())
		}
	}
}

func (r *RootMulti) refresh(ctx context.Context) {
	if r.load == nil {
		return
	}
	entries, err := r.load(ctx)
	if err != nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	wanted := make(map[string]catalog.Entry, len(entries))
	for _, e := range entries {
		wanted[e.Name] = e
	}
	if r.ingest != nil {
		for _, e := range r.ingest.Entries() {
			wanted[e.Name] = e
		}
	}

	for name := range r.entries {
		if _, ok := wanted[name]; !ok {
			delete(r.entries, name)
			r.RmChild(name)
			_ = r.NotifyEntry(name)
		}
	}
	for name, e := range wanted {
		if _, ok := r.entries[name]; ok {
			r.entries[name] = e
			continue
		}
		r.entries[name] = e
		r.addChildLocked(ctx, e)
		_ = r.NotifyEntry(name)
	}
}

var _ = (fs.NodeOnAdder)((*RootMulti)(nil))
var _ = (fs.NodeGetattrer)((*RootMulti)(nil))
var _ = (fs.NodeLookuper)((*RootMulti)(nil))
var _ = (fs.NodeReaddirer)((*RootMulti)(nil))
var _ = (fs.NodeCreater)((*RootMulti)(nil))

type multiFile struct {
	fs.Inode
	root *RootMulti
	name string
	pool *proxypool.Pool
}

func (f *multiFile) entry() (catalog.Entry, bool) {
	f.root.mu.Lock()
	defer f.root.mu.Unlock()
	entry, ok := f.root.entries[f.name]
	return entry, ok
}

type pooledHandle struct {
	source  ByteSource
	release func()
}

type writableFile interface {
	WriteAt(content []byte, offset uint64) (int, error)
	Flush() error
	Close() error
}

type writeHandle struct {
	file writableFile
}

func (f *multiFile) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	entry, ok := f.entry()
	if !ok {
		return syscall.ENOENT
	}
	out.Mode = 0444
	out.Size = entry.Size
	if entry.Source != nil {
		out.Mode = 0644
		out.Size = entry.Source.Size()
	}
	return 0
}

func (f *multiFile) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	entry, ok := f.entry()
	if !ok {
		return nil, 0, syscall.ENOENT
	}
	if flags&(syscall.O_WRONLY|syscall.O_RDWR) != 0 {
		return nil, 0, syscall.EROFS
	}
	if entry.Source != nil {
		return &pooledHandle{source: entry.Source}, fuse.FOPEN_DIRECT_IO, 0
	}
	client, release, err := f.pool.Acquire(entry)
	if err != nil {
		if errors.Is(err, proxypool.ErrBusy) {
			return nil, 0, syscall.EBUSY
		}
		return nil, 0, syscall.EIO
	}
	return &pooledHandle{source: client, release: release}, fuse.FOPEN_DIRECT_IO, 0
}

func (f *multiFile) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	h, ok := fh.(*pooledHandle)
	if !ok || h.source == nil {
		return nil, syscall.EIO
	}
	entry, ok := f.entry()
	if !ok {
		return nil, syscall.ENOENT
	}
	size := entry.Size
	if entry.Source != nil {
		size = entry.Source.Size()
	}
	return readFrom(h.source, size, dest, off)
}

func (f *multiFile) Release(ctx context.Context, fh fs.FileHandle) syscall.Errno {
	if h, ok := fh.(*pooledHandle); ok && h.release != nil {
		h.release()
		h.release = nil
	}
	return 0
}

var _ = (fs.NodeGetattrer)((*multiFile)(nil))
var _ = (fs.NodeOpener)((*multiFile)(nil))
var _ = (fs.NodeReader)((*multiFile)(nil))
var _ = (fs.NodeReleaser)((*multiFile)(nil))

func (h *writeHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	if off < 0 {
		return 0, syscall.EINVAL
	}
	n, err := h.file.WriteAt(data, uint64(off))
	return uint32(n), ingestErrno(err)
}

func (h *writeHandle) Flush(ctx context.Context) syscall.Errno {
	return ingestErrno(h.file.Close())
}

func (h *writeHandle) Fsync(ctx context.Context, flags uint32) syscall.Errno {
	return ingestErrno(h.file.Flush())
}

func (h *writeHandle) Release(ctx context.Context) syscall.Errno {
	return ingestErrno(h.file.Close())
}

var _ = (fs.FileWriter)((*writeHandle)(nil))
var _ = (fs.FileFlusher)((*writeHandle)(nil))
var _ = (fs.FileFsyncer)((*writeHandle)(nil))
var _ = (fs.FileReleaser)((*writeHandle)(nil))

func ingestErrno(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	if errors.Is(err, ingest.ErrNonSequential) {
		return syscall.EINVAL
	}
	if errors.Is(err, ingest.ErrSpoolFull) || errors.Is(err, ingest.ErrFileTooLarge) {
		return syscall.ENOSPC
	}
	if errors.Is(err, ingest.ErrActiveWriteLimit) || errors.Is(err, ingest.ErrCatalogFull) {
		return syscall.EBUSY
	}
	if errors.Is(err, ingest.ErrNameExists) {
		return syscall.EEXIST
	}
	return syscall.EIO
}

func readFrom(client ByteSource, size uint64, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if off < 0 {
		return nil, syscall.EINVAL
	}
	if uint64(off) >= size {
		return fuse.ReadResultData(nil), 0
	}
	remaining := size - uint64(off)
	want := uint64(len(dest))
	if want > remaining {
		want = remaining
	}
	buf := dest[:want]
	n, err := client.ReadAt(buf, uint64(off))
	if err != nil {
		return nil, syscall.EIO
	}
	return fuse.ReadResultData(buf[:n]), 0
}
