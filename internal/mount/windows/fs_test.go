//go:build windows

package windows

import (
	"testing"

	"github.com/amaan/video-storage-engine/internal/spacecatalog"
)

func TestNewMultiWiring(t *testing.T) {
	entries := []spacecatalog.Entry{
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

func TestNewSingle(t *testing.T) {
	root := NewSingle(nil, "video.mp4", 42)
	if root.singleName != "video.mp4" || root.singleSize != 42 {
		t.Fatalf("%+v", root)
	}
	root.Stop()
}

func TestCleanPath(t *testing.T) {
	if cleanPath("") != "/" {
		t.Fatal(cleanPath(""))
	}
	if cleanPath("/a.mp4") != "/a.mp4" {
		t.Fatal(cleanPath("/a.mp4"))
	}
}
