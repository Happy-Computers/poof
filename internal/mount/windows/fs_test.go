//go:build windows

package windows

import (
	"context"
	"testing"
	"time"

	"github.com/amaan/infinity-storage/internal/catalog"
	"github.com/amaan/infinity-storage/internal/ingest"
	"github.com/winfsp/cgofuse/fuse"
)

func TestNewMultiWiring(t *testing.T) {
	entries := []catalog.Entry{
		{Name: "a.mp4", AbsPath: `C:\a.mp4`, Size: 100},
		{Name: "b.bin", AbsPath: `C:\b.bin`, Size: 200},
	}
	root := NewMulti(entries, nil, nil)
	if root == nil {
		t.Fatal("nil root")
	}
	if len(root.entries) != 2 {
		t.Fatalf("entries: %d", len(root.entries))
	}
	root.Stop()
}

func TestReaddirDoesNotRefreshCatalog(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	filesystem := NewMulti(nil, nil, func(ctx context.Context) ([]catalog.Entry, error) {
		<-block
		return nil, nil
	})
	defer filesystem.Stop()
	done := make(chan struct{})
	go func() {
		status := filesystem.Readdir("/", func(name string, stat *fuse.Stat_t, offset int64) bool {
			return true
		}, 0, 0)
		if status != 0 {
			t.Errorf("readdir: %d", status)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("readdir waited for catalog refresh")
	}
}

func TestStatfsReportsWritableCapacity(t *testing.T) {
	manager, err := ingest.NewManager(ingest.Config{
		SpoolDir: t.TempDir(),
		Store:    &discardStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	filesystem := NewMultiWritable(nil, nil, nil, manager)
	defer filesystem.Stop()
	stat := &fuse.Statfs_t{}
	if status := filesystem.Statfs("/", stat); status != 0 {
		t.Fatalf("statfs status=%d", status)
	}
	if stat.Bavail == 0 || stat.Bfree == 0 || stat.Blocks == 0 {
		t.Fatalf("capacity=%+v", stat)
	}
	if stat.Bavail*stat.Frsize != ingest.MaxSpoolBytes {
		t.Fatalf("available bytes=%d", stat.Bavail*stat.Frsize)
	}
}

func TestNewSingle(t *testing.T) {
	root := NewSingle(nil, "video.mp4", 42)
	if root.singleName != "video.mp4" || root.singleSize != 42 {
		t.Fatalf("%+v", root)
	}
	root.Stop()
}

func TestReleaseKeepsSharedEmptyWriter(t *testing.T) {
	manager, err := ingest.NewManager(ingest.Config{
		SpoolDir:        t.TempDir(),
		Store:           &discardStore{},
		MaxActiveWrites: 2,
		MaxSpoolBytes:   1024,
		MaxFileBytes:    1024,
		RangeWait:       time.Millisecond,
		UploadAttempts:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	filesystem := NewMultiWritable(nil, nil, nil, manager)
	defer filesystem.Stop()

	status, first := filesystem.Create("/clip.mp4", 0, 0)
	if status != 0 {
		t.Fatalf("create: %d", status)
	}
	status, second := filesystem.openForWrite("clip.mp4")
	if status != 0 {
		t.Fatalf("second open: %d", status)
	}
	if status := filesystem.Release("/clip.mp4", first); status != 0 {
		t.Fatalf("partial release: %d", status)
	}
	filesystem.mu.Lock()
	_, exists := filesystem.entries["clip.mp4"]
	filesystem.mu.Unlock()
	if !exists {
		t.Fatal("entry removed after partial release of empty writer")
	}
	status, third := filesystem.Open("/clip.mp4", fuse.O_WRONLY)
	if status != 0 {
		t.Fatalf("reopen write after partial release: %d", status)
	}
	written := filesystem.Write("/clip.mp4", []byte("abc"), 0, second)
	if written != 3 {
		t.Fatalf("write through remaining handle: %d", written)
	}
	if status := filesystem.Release("/clip.mp4", second); status != 0 {
		t.Fatalf("release second: %d", status)
	}
	if status := filesystem.Release("/clip.mp4", third); status != 0 {
		t.Fatalf("release third: %d", status)
	}
}

type discardStore struct{}

func (discardStore) Begin(ctx context.Context, name string) (ingest.Upload, error) {
	return discardUpload{}, nil
}

type discardUpload struct{}

func (discardUpload) PutPart(ctx context.Context, number int32, content []byte, checksum [32]byte) (ingest.Part, error) {
	return ingest.Part{Number: number, Checksum: checksum}, nil
}

func (discardUpload) Complete(ctx context.Context, parts []ingest.Part, size uint64, checksum [32]byte) error {
	return nil
}

func (discardUpload) Abort(ctx context.Context) error { return nil }

func TestCleanPath(t *testing.T) {
	if cleanPath("") != "/" {
		t.Fatal(cleanPath(""))
	}
	if cleanPath("/a.mp4") != "/a.mp4" {
		t.Fatal(cleanPath("/a.mp4"))
	}
}
