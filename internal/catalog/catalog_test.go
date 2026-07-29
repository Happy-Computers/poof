package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithOriginBaseEscapesSpaces(t *testing.T) {
	ents := WithOriginBase([]Entry{{Name: "a b.webm", Size: 1}}, "http://127.0.0.1:9")
	want := "http://127.0.0.1:9/object/a%20b.webm"
	if ents[0].OriginURL != want {
		t.Fatalf("got %q", ents[0].OriginURL)
	}
}

func TestLoadDirEmpty(t *testing.T) {
	dir := t.TempDir()
	ents, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Fatalf("want 0, got %d", len(ents))
	}
}

func TestLoadDirRegularFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.mp4"), []byte("hello"))
	mustWrite(t, filepath.Join(dir, "b.bin"), []byte("world!"))
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}

	ents, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Fatalf("want 2 files (subdir skipped), got %d", len(ents))
	}
	byName := map[string]Entry{}
	for _, e := range ents {
		byName[e.Name] = e
	}
	if byName["a.mp4"].Size != 5 {
		t.Fatalf("a.mp4 size: %d", byName["a.mp4"].Size)
	}
	if byName["b.bin"].AbsPath != filepath.Join(dir, "b.bin") {
		t.Fatalf("abs path: %s", byName["b.bin"].AbsPath)
	}
}

func TestLoadDirEmptyFileRejected(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "empty"), nil)
	_, err := LoadDir(dir)
	if err == nil || !strings.Contains(err.Error(), "empty file") {
		t.Fatalf("want empty-file error, got %v", err)
	}
}

func TestLoadDirMaxNameOK(t *testing.T) {
	// Linux NAME_MAX is typically 255; MaxNameBytes matches that ceiling.
	dir := t.TempDir()
	name := strings.Repeat("x", MaxNameBytes)
	mustWrite(t, filepath.Join(dir, name), []byte("x"))
	ents, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name != name {
		t.Fatalf("got %+v", ents)
	}
}

func TestLoadDirTooManyFiles(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxFiles+1; i++ {
		mustWrite(t, filepath.Join(dir, fmt.Sprintf("%d.dat", i)), []byte("x"))
	}
	_, err := LoadDir(dir)
	if err == nil || !strings.Contains(err.Error(), "too many files") {
		t.Fatalf("want too-many error, got %v", err)
	}
}

func TestLoadDirNotDirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	mustWrite(t, f, []byte("x"))
	_, err := LoadDir(f)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("want not-a-directory, got %v", err)
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}
