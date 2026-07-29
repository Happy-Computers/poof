package proxypool

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"testing"

	"github.com/amaan/video-storage-engine/internal/spacecatalog"
)

// tinyRangeOrigin serves one object at /object/name with HEAD + Range GET.
func startTinyOrigin(t *testing.T, name string, payload []byte) string {
	t.Helper()
	mux := http.NewServeMux()
	path := "/object/" + name
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			rng := r.Header.Get("Range")
			if rng == "" {
				http.Error(w, "range required", 400)
				return
			}
			// bytes=START-END
			var start, end int
			if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); err != nil {
				http.Error(w, "bad range", 416)
				return
			}
			if start < 0 || end >= len(payload) || end < start {
				http.Error(w, "bad range", 416)
				return
			}
			chunk := payload[start : end+1]
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
			w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(chunk)
		default:
			http.Error(w, "method", 405)
		}
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String()
}

func TestAcquireOriginURL(t *testing.T) {
	bin := findProxyBin(t)
	payload := []byte("abcdefghij0123456789")
	base := startTinyOrigin(t, "x.bin", payload)
	entry := spacecatalog.Entry{
		Name:      "x.bin",
		OriginURL: base + "/object/x.bin",
		Size:      uint64(len(payload)),
	}

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

	sz, err := client.Size()
	if err != nil || sz != uint64(len(payload)) {
		t.Fatalf("size %d %v", sz, err)
	}
	buf := make([]byte, 4)
	n, err := client.ReadAt(buf, 2)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 || string(buf) != "cdef" {
		t.Fatalf("got %q n=%d", buf[:n], n)
	}
}

func TestSlotKeyPrefersOriginURL(t *testing.T) {
	e := spacecatalog.Entry{Name: "a", AbsPath: "/tmp/a", OriginURL: "http://x/object/a"}
	if e.SlotKey() != e.OriginURL {
		t.Fatal(e.SlotKey())
	}
}
