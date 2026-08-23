//go:build linux

package linux

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/amaan/infinity-storage/internal/catalog"
	"github.com/hanwen/go-fuse/v2/fuse"
)

func TestNewRootMultiWiring(t *testing.T) {
	entries := []catalog.Entry{
		{Name: "a.mp4", AbsPath: "/tmp/a.mp4", Size: 100},
		{Name: "b.bin", AbsPath: "/tmp/b.bin", Size: 200},
	}
	root := NewRootMulti(entries, nil)
	if root == nil {
		t.Fatal("nil root")
	}
	if len(root.entries) != 2 {
		t.Fatalf("entries: %d", len(root.entries))
	}
	if root.entries["a.mp4"].Name != "a.mp4" {
		t.Fatal(root.entries["a.mp4"].Name)
	}
}

func TestNewRootSingle(t *testing.T) {
	root := NewRootSingle(nil, "video.mp4", 42)
	if root.fileName != "video.mp4" || root.size != 42 {
		t.Fatalf("%+v", root)
	}
	alias := NewRoot(nil, "x", 1)
	if alias.fileName != "x" {
		t.Fatal(alias.fileName)
	}
}

func TestNewRootMultiLive(t *testing.T) {
	root := NewRootMultiLive(nil, nil, func(ctx context.Context) ([]catalog.Entry, error) {
		return []catalog.Entry{{Name: "x", Size: 1}}, nil
	})
	if root.load == nil {
		t.Fatal("expected loader")
	}
	root.Stop()
}

func TestReaddirDoesNotRefreshCatalog(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	root := NewRootMultiLive(nil, nil, func(ctx context.Context) ([]catalog.Entry, error) {
		<-block
		return nil, nil
	})
	done := make(chan struct{})
	go func() {
		_, errno := root.Readdir(context.Background())
		if errno != 0 {
			t.Errorf("readdir: %v", errno)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("readdir waited for catalog refresh")
	}
}

type recordingWritableFile struct {
	closed  int
	flushed int
}

func (f *recordingWritableFile) WriteAt(content []byte, offset uint64) (int, error) {
	return len(content), nil
}

func (f *recordingWritableFile) Flush() error {
	f.flushed++
	return nil
}

func (f *recordingWritableFile) Close() error {
	f.closed++
	return nil
}

func TestSetattrAcceptsMetadataAndRejectsResize(t *testing.T) {
	root := NewRootMulti([]catalog.Entry{{Name: "clip.mp4", Size: 42}}, nil)
	file := &multiFile{root: root, name: "clip.mp4"}
	out := &fuse.AttrOut{}
	if errno := file.Setattr(context.Background(), nil, &fuse.SetAttrIn{}, out); errno != 0 {
		t.Fatalf("metadata setattr: %v", errno)
	}
	if out.Size != 42 {
		t.Fatalf("size=%d", out.Size)
	}
	resize := &fuse.SetAttrIn{SetAttrInCommon: fuse.SetAttrInCommon{Valid: fuse.FATTR_SIZE, Size: 41}}
	if errno := file.Setattr(context.Background(), nil, resize, out); errno != syscall.EOPNOTSUPP {
		t.Fatalf("resize errno=%v", errno)
	}
}

func TestWriteHandleSealsOnFlush(t *testing.T) {
	file := &recordingWritableFile{}
	handle := &writeHandle{file: file}

	if errno := handle.Flush(context.Background()); errno != 0 {
		t.Fatalf("flush errno: %v", errno)
	}
	if file.closed != 1 || file.flushed != 0 {
		t.Fatalf("flush lifecycle: closed=%d flushed=%d", file.closed, file.flushed)
	}
	if errno := handle.Fsync(context.Background(), 0); errno != 0 {
		t.Fatalf("fsync errno: %v", errno)
	}
	if file.closed != 1 || file.flushed != 1 {
		t.Fatalf("fsync lifecycle: closed=%d flushed=%d", file.closed, file.flushed)
	}
}
