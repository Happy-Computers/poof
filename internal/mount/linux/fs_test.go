//go:build linux

package linux

import (
	"context"
	"testing"

	"github.com/amaan/video-storage-engine/internal/spacecatalog"
)

func TestNewRootMultiWiring(t *testing.T) {
	entries := []spacecatalog.Entry{
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
	root := NewRootMultiLive(nil, nil, func(ctx context.Context) ([]spacecatalog.Entry, error) {
		return []spacecatalog.Entry{{Name: "x", Size: 1}}, nil
	})
	if root.load == nil {
		t.Fatal("expected loader")
	}
	root.Stop()
}
