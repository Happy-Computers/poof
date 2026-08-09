package s3origin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func testStore(t *testing.T, metas ...ObjectMeta) *Store {
	t.Helper()
	s, err := newStore(nil, "test-bucket", "", metas)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestObjectNameFromPath(t *testing.T) {
	store := testStore(t,
		ObjectMeta{Name: "a.mp4", Key: "a.mp4", Size: 100},
		ObjectMeta{Name: "b.bin", Key: "b.bin", Size: 200},
	)

	if _, ok := objectNameFromPath("/object", store); ok {
		t.Fatal("multi-object store must not resolve bare /object")
	}
	name, ok := objectNameFromPath("/object/a.mp4", store)
	if !ok || name != "a.mp4" {
		t.Fatalf("got %q ok=%v", name, ok)
	}
	if _, ok := objectNameFromPath("/object/missing", store); ok {
		t.Fatal("expected miss")
	}
	if _, ok := objectNameFromPath("/other", store); ok {
		t.Fatal("expected miss")
	}

	single := testStore(t, ObjectMeta{Name: "only.mp4", Key: "only.mp4", Size: 10})
	name, ok = objectNameFromPath("/object", single)
	if !ok || name != "only.mp4" {
		t.Fatalf("single /object: %q ok=%v", name, ok)
	}
}

func TestObjectNameFromPathEncoded(t *testing.T) {
	name := "Screencast from 2026-07-11 21-58-43.webm"
	store := testStore(t, ObjectMeta{Name: name, Key: name, Size: 100})
	got, ok := objectNameFromPath("/object/"+url.PathEscape(name), store)
	if !ok || got != name {
		t.Fatalf("got %q ok=%v", got, ok)
	}
}

func TestHandlerHeadAndRangeRequired(t *testing.T) {
	store := testStore(t, ObjectMeta{Name: "clip.mp4", Key: "clip.mp4", Size: 1024})
	h := NewHandler(store)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/object/clip.mp4", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("head status %d", rr.Code)
	}
	if rr.Header().Get("Content-Length") != "1024" {
		t.Fatalf("content-length %q", rr.Header().Get("Content-Length"))
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/object/clip.mp4", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("get without range: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandlerGetRangeNoClient(t *testing.T) {
	store := testStore(t, ObjectMeta{Name: "clip.mp4", Key: "clip.mp4", Size: 1024})
	h := NewHandler(store)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/object/clip.mp4", nil)
	req.Header.Set("Range", "bytes=0-15")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("want 502 without S3, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandlerHeadEncodedName(t *testing.T) {
	name := "Screencast from 2026.webm"
	store := testStore(t, ObjectMeta{Name: name, Key: name, Size: 7247866})
	h := NewHandler(store)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/object/"+url.PathEscape(name), nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("head status %d", rr.Code)
	}
	if rr.Header().Get("Content-Length") != "7247866" {
		t.Fatalf("content-length %q", rr.Header().Get("Content-Length"))
	}
}
