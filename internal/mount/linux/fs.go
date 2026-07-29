//go:build linux

// Package linux is the Linux FUSE volume backend for Space.
package linux

import (
	"context"
	"errors"
	"sync"
	"syscall"
	"time"

	"github.com/amaan/video-storage-engine/internal/cacheclient"
	"github.com/amaan/video-storage-engine/internal/proxypool"
	"github.com/amaan/video-storage-engine/internal/s3origin"
	"github.com/amaan/video-storage-engine/internal/spacecatalog"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// ByteSource provides ranged reads for a single object (single-file mount).
type ByteSource interface {
	ReadAt(dest []byte, offset uint64) (int, error)
}

// CatalogLoader re-lists Space entries (S3). Nil means static catalog (--dir).
type CatalogLoader func(ctx context.Context) ([]spacecatalog.Entry, error)

// RootSingle is the mount root containing one read-only file (--uds mode).
type RootSingle struct {
	fs.Inode
	client   *cacheclient.Client
	fileName string
	size     uint64
}

// NewRootSingle builds a single-file Space root.
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

// RootMulti is a flat multi-file Space root.
type RootMulti struct {
	fs.Inode
	pool *proxypool.Pool

	mu          sync.Mutex
	entries     map[string]spacecatalog.Entry
	load        CatalogLoader
	lastRefresh time.Time
	nextIno     uint64
	stopPoll    chan struct{}
	pollOnce    sync.Once
}

// NewRootMulti builds a static multi-file root (--dir harness).
func NewRootMulti(entries []spacecatalog.Entry, pool *proxypool.Pool) *RootMulti {
	return newRootMulti(entries, pool, nil)
}

// NewRootMultiLive builds a root that re-lists via load (S3).
func NewRootMultiLive(entries []spacecatalog.Entry, pool *proxypool.Pool, load CatalogLoader) *RootMulti {
	return newRootMulti(entries, pool, load)
}

func newRootMulti(entries []spacecatalog.Entry, pool *proxypool.Pool, load CatalogLoader) *RootMulti {
	m := make(map[string]spacecatalog.Entry, len(entries))
	for _, e := range entries {
		m[e.Name] = e
	}
	r := &RootMulti{
		pool:     pool,
		entries:  m,
		load:     load,
		nextIno:  2,
		stopPoll: make(chan struct{}),
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
	r.lastRefresh = time.Now()
	r.mu.Unlock()

	if r.load != nil {
		go r.pollCatalog()
	}
}

func (r *RootMulti) addChildLocked(ctx context.Context, e spacecatalog.Entry) {
	ino := r.nextIno
	r.nextIno++
	ch := r.NewPersistentInode(ctx, &multiFile{
		entry: e,
		pool:  r.pool,
	}, fs.StableAttr{Mode: syscall.S_IFREG, Ino: ino})
	r.AddChild(e.Name, ch, true)
}

func (r *RootMulti) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0555
	return 0
}

func (r *RootMulti) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	r.maybeRefresh(ctx)
	if ch := r.GetChild(name); ch != nil {
		if mf, ok := ch.Operations().(*multiFile); ok {
			out.Mode = 0444
			out.Size = mf.entry.Size
		}
		return ch, 0
	}
	return nil, syscall.ENOENT
}

func (r *RootMulti) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	r.maybeRefresh(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	list := make([]fuse.DirEntry, 0, len(r.entries))
	for name := range r.entries {
		list = append(list, fuse.DirEntry{
			Name: name,
			Mode: syscall.S_IFREG,
		})
	}
	return fs.NewListDirStream(list), 0
}

func (r *RootMulti) maybeRefresh(ctx context.Context) {
	if r.load == nil {
		return
	}
	r.mu.Lock()
	due := time.Since(r.lastRefresh) >= time.Duration(s3origin.MinCatalogRefresh)*time.Second
	r.mu.Unlock()
	if !due {
		return
	}
	r.refresh(ctx)
}

func (r *RootMulti) pollCatalog() {
	ticker := time.NewTicker(2 * time.Second)
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
	r.lastRefresh = time.Now()

	wanted := make(map[string]spacecatalog.Entry, len(entries))
	for _, e := range entries {
		wanted[e.Name] = e
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

type multiFile struct {
	fs.Inode
	entry spacecatalog.Entry
	pool  *proxypool.Pool
}

type pooledHandle struct {
	client  *cacheclient.Client
	release func()
}

func (f *multiFile) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0444
	out.Size = f.entry.Size
	return 0
}

func (f *multiFile) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	client, release, err := f.pool.Acquire(f.entry)
	if err != nil {
		if errors.Is(err, proxypool.ErrBusy) {
			return nil, 0, syscall.EBUSY
		}
		return nil, 0, syscall.EIO
	}
	return &pooledHandle{client: client, release: release}, fuse.FOPEN_DIRECT_IO, 0
}

func (f *multiFile) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	h, ok := fh.(*pooledHandle)
	if !ok || h.client == nil {
		return nil, syscall.EIO
	}
	return readFrom(h.client, f.entry.Size, dest, off)
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
