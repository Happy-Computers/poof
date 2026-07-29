//go:build windows

// Package windows is the WinFsp (cgofuse) volume backend for Space.
package windows

import (
	"context"
	"errors"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/amaan/video-storage-engine/internal/cacheclient"
	"github.com/amaan/video-storage-engine/internal/proxypool"
	"github.com/amaan/video-storage-engine/internal/s3origin"
	"github.com/amaan/video-storage-engine/internal/spacecatalog"
	"github.com/winfsp/cgofuse/fuse"
)

// CatalogLoader re-lists Space entries. Nil means static catalog.
type CatalogLoader func(ctx context.Context) ([]spacecatalog.Entry, error)

// SpaceFS is a read-only flat Space volume for WinFsp via cgofuse.
type SpaceFS struct {
	fuse.FileSystemBase

	pool *proxypool.Pool

	mu          sync.Mutex
	entries     map[string]spacecatalog.Entry
	load        CatalogLoader
	lastRefresh time.Time
	stopPoll    chan struct{}
	pollOnce    sync.Once

	// Single-file mode.
	single     *cacheclient.Client
	singleName string
	singleSize uint64

	handles   map[uint64]*openHandle
	nextHandle uint64
}

type openHandle struct {
	client  *cacheclient.Client
	release func()
	size    uint64
}

// NewMulti builds a multi-file root (optional live loader).
func NewMulti(entries []spacecatalog.Entry, pool *proxypool.Pool, load CatalogLoader) *SpaceFS {
	m := make(map[string]spacecatalog.Entry, len(entries))
	for _, e := range entries {
		m[e.Name] = e
	}
	fs := &SpaceFS{
		pool:       pool,
		entries:    m,
		load:       load,
		stopPoll:   make(chan struct{}),
		handles:    make(map[uint64]*openHandle),
		nextHandle: 1,
	}
	if load != nil {
		go fs.pollCatalog()
	}
	return fs
}

// NewSingle builds a single-file Space root.
func NewSingle(client *cacheclient.Client, name string, size uint64) *SpaceFS {
	return &SpaceFS{
		single:     client,
		singleName: name,
		singleSize: size,
		handles:    make(map[uint64]*openHandle),
		nextHandle: 1,
		stopPoll:   make(chan struct{}),
	}
}

// Stop halts the background catalog poller.
func (f *SpaceFS) Stop() {
	f.pollOnce.Do(func() {
		close(f.stopPoll)
	})
}

func (f *SpaceFS) Init() {
	f.FileSystemBase.Init()
}

func (f *SpaceFS) Destroy() {
	f.Stop()
	f.FileSystemBase.Destroy()
}

func (f *SpaceFS) Getattr(p string, stat *fuse.Stat_t, fh uint64) (errc int) {
	p = cleanPath(p)
	if p == "/" {
		stat.Mode = fuse.S_IFDIR | 0555
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
	e, ok := f.entries[name]
	f.mu.Unlock()
	if !ok {
		return -fuse.ENOENT
	}
	stat.Mode = fuse.S_IFREG | 0444
	stat.Size = int64(e.Size)
	return 0
}

func (f *SpaceFS) Open(p string, flags int) (errc int, fh uint64) {
	_ = flags
	p = cleanPath(p)
	name := strings.TrimPrefix(p, "/")
	if name == "" || strings.Contains(name, "/") {
		return -fuse.ENOENT, ^uint64(0)
	}

	if f.single != nil {
		if name != f.singleName {
			return -fuse.ENOENT, ^uint64(0)
		}
		f.mu.Lock()
		fh = f.nextHandle
		f.nextHandle++
		f.handles[fh] = &openHandle{client: f.single, size: f.singleSize}
		f.mu.Unlock()
		return 0, fh
	}

	f.maybeRefresh()
	f.mu.Lock()
	e, ok := f.entries[name]
	f.mu.Unlock()
	if !ok {
		return -fuse.ENOENT, ^uint64(0)
	}

	client, release, err := f.pool.Acquire(e)
	if err != nil {
		if errors.Is(err, proxypool.ErrBusy) {
			return -fuse.EBUSY, ^uint64(0)
		}
		return -fuse.EIO, ^uint64(0)
	}

	f.mu.Lock()
	fh = f.nextHandle
	f.nextHandle++
	f.handles[fh] = &openHandle{client: client, release: release, size: e.Size}
	f.mu.Unlock()
	return 0, fh
}

func (f *SpaceFS) Release(p string, fh uint64) int {
	_ = p
	f.mu.Lock()
	h, ok := f.handles[fh]
	if ok {
		delete(f.handles, fh)
	}
	f.mu.Unlock()
	if ok && h.release != nil {
		h.release()
	}
	return 0
}

func (f *SpaceFS) Read(p string, buff []byte, ofst int64, fh uint64) (n int) {
	_ = p
	f.mu.Lock()
	h, ok := f.handles[fh]
	f.mu.Unlock()
	if !ok || h.client == nil {
		return 0
	}
	if ofst < 0 {
		return 0
	}
	if uint64(ofst) >= h.size {
		return 0
	}
	remaining := h.size - uint64(ofst)
	want := uint64(len(buff))
	if want > remaining {
		want = remaining
	}
	got, err := h.client.ReadAt(buff[:want], uint64(ofst))
	if err != nil {
		return 0
	}
	return got
}

func (f *SpaceFS) Readdir(p string,
	fill func(name string, stat *fuse.Stat_t, ofst int64) bool,
	ofst int64,
	fh uint64,
) (errc int) {
	_ = ofst
	_ = fh
	p = cleanPath(p)
	if p != "/" {
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

func (f *SpaceFS) maybeRefresh() {
	if f.load == nil {
		return
	}
	f.mu.Lock()
	due := time.Since(f.lastRefresh) >= time.Duration(s3origin.MinCatalogRefresh)*time.Second
	f.mu.Unlock()
	if !due {
		return
	}
	f.refresh(context.Background())
}

func (f *SpaceFS) pollCatalog() {
	ticker := time.NewTicker(2 * time.Second)
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

func (f *SpaceFS) refresh(ctx context.Context) {
	if f.load == nil {
		return
	}
	entries, err := f.load(ctx)
	if err != nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRefresh = time.Now()
	wanted := make(map[string]spacecatalog.Entry, len(entries))
	for _, e := range entries {
		wanted[e.Name] = e
	}
	f.entries = wanted
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
