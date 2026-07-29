package proxypool

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/amaan/video-storage-engine/internal/spacecatalog"
)

func findProxyBin(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.Getenv("STREAM_PROXY_BIN"),
		filepath.Join("..", "..", "stream_proxy", "zig-out", "bin", "stream_proxy"),
		"stream_proxy",
	}
	// Also try from module root via cwd walk.
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "..", "..", "stream_proxy", "zig-out", "bin", "stream_proxy"),
			filepath.Join(wd, "stream_proxy", "zig-out", "bin", "stream_proxy"),
		)
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if path, err := exec.LookPath(c); err == nil {
			return path
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	t.Skip("stream_proxy binary not found; build with: cd stream_proxy && zig build")
	return ""
}

func TestAcquireSharesProcess(t *testing.T) {
	bin := findProxyBin(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(path, []byte("hello-world"), 0644); err != nil {
		t.Fatal(err)
	}
	entry := spacecatalog.Entry{Name: "a.bin", AbsPath: path, Size: 11}

	pool, err := New(Config{ProxyBin: bin, MaxActive: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	c1, rel1, err := pool.Acquire(entry)
	if err != nil {
		t.Fatal(err)
	}
	c2, rel2, err := pool.Acquire(entry)
	if err != nil {
		t.Fatal(err)
	}
	if pool.ActiveCount() != 1 {
		t.Fatalf("want 1 active, got %d", pool.ActiveCount())
	}
	sz, err := c1.Size()
	if err != nil || sz != 11 {
		t.Fatalf("size via c1: %d %v", sz, err)
	}
	sz, err = c2.Size()
	if err != nil || sz != 11 {
		t.Fatalf("size via c2: %d %v", sz, err)
	}
	rel1()
	rel2()
	if pool.ActiveCount() != 1 {
		t.Fatalf("idle proxy should stay warm, got %d", pool.ActiveCount())
	}
}

func TestBusyWhenNoIdle(t *testing.T) {
	bin := findProxyBin(t)
	dir := t.TempDir()
	var entries []spacecatalog.Entry
	for i, name := range []string{"a.bin", "b.bin"} {
		path := filepath.Join(dir, name)
		data := []byte{byte('a' + i)}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, spacecatalog.Entry{Name: name, AbsPath: path, Size: 1})
	}

	pool, err := New(Config{ProxyBin: bin, MaxActive: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, rel, err := pool.Acquire(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	defer rel()

	_, _, err = pool.Acquire(entries[1])
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
}

func TestEvictIdle(t *testing.T) {
	bin := findProxyBin(t)
	dir := t.TempDir()
	var entries []spacecatalog.Entry
	for i, name := range []string{"a.bin", "b.bin"} {
		path := filepath.Join(dir, name)
		data := []byte{byte('a' + i)}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, spacecatalog.Entry{Name: name, AbsPath: path, Size: 1})
	}

	pool, err := New(Config{ProxyBin: bin, MaxActive: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, rel, err := pool.Acquire(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	rel() // idle — can be evicted

	c2, rel2, err := pool.Acquire(entries[1])
	if err != nil {
		t.Fatal(err)
	}
	defer rel2()
	if pool.ActiveCount() != 1 {
		t.Fatalf("want 1 after eviction, got %d", pool.ActiveCount())
	}
	sz, err := c2.Size()
	if err != nil || sz != 1 {
		t.Fatalf("size: %d %v", sz, err)
	}
}

func TestReadBytes(t *testing.T) {
	bin := findProxyBin(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "x.bin")
	payload := []byte("abcdefghij")
	if err := os.WriteFile(path, payload, 0644); err != nil {
		t.Fatal(err)
	}
	entry := spacecatalog.Entry{Name: "x.bin", AbsPath: path, Size: uint64(len(payload))}

	pool, err := New(Config{ProxyBin: bin})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	client, rel, err := pool.Acquire(entry)
	if err != nil {
		t.Fatal(err)
	}
	defer rel()

	buf := make([]byte, 4)
	n, err := client.ReadAt(buf, 2)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 || string(buf) != "cdef" {
		t.Fatalf("got %q n=%d", buf, n)
	}
}

func TestMaxActiveHardCap(t *testing.T) {
	_, err := New(Config{ProxyBin: "/bin/true", MaxActive: MaxActiveProxies + 1})
	if err == nil {
		t.Fatal("want hard-cap error")
	}
}
