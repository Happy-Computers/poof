package s3origin

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// Handler serves HEAD and range GET for /object and /object/<basename>.
type Handler struct {
	store *Store
}

func NewHandler(store *Store) *Handler {
	return &Handler{store: store}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.EscapedPath()
	if p == "" {
		p = r.URL.Path
	}
	name, ok := objectNameFromPath(p, h.store)
	if !ok {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodHead:
		h.handleHead(w, name)
	case http.MethodGet:
		h.handleGet(w, r, name)
	default:
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed\n", http.StatusMethodNotAllowed)
	}
}

// objectNameFromPath resolves /object (single-object stores) or /object/<name>.
func objectNameFromPath(urlPath string, store *Store) (string, bool) {
	urlPath = path.Clean("/" + strings.TrimPrefix(urlPath, "/"))
	if urlPath == "/object" {
		m, ok := store.Single()
		return m.Name, ok
	}
	const prefix = "/object/"
	if !strings.HasPrefix(urlPath, prefix) {
		return "", false
	}
	raw := strings.TrimPrefix(urlPath, prefix)
	if raw == "" || strings.Contains(raw, "/") {
		return "", false
	}
	name, err := url.PathUnescape(raw)
	if err != nil {
		return "", false
	}
	if _, ok := store.Lookup(name); !ok {
		return "", false
	}
	return name, true
}

func (h *Handler) handleHead(w http.ResponseWriter, name string) {
	size, ok := h.store.CachedSize(name)
	if !ok {
		http.Error(w, "not found\n", http.StatusNotFound)
		return
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", strconv.FormatUint(size, 10))
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, name string) {
	size, ok := h.store.CachedSize(name)
	if !ok {
		http.NotFound(w, r)
		return
	}

	rangeHeader := r.Header.Get("Range")
	if rangeHeader == "" {
		w.Header().Set("Accept-Ranges", "bytes")
		http.Error(w, "Range header required\n", http.StatusBadRequest)
		return
	}

	br, err := ParseBytesRange(rangeHeader, size)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}

	if br.Length() > MaxRangeBytes {
		http.Error(w, "range too large\n", http.StatusRequestEntityTooLarge)
		return
	}

	body, length, err := h.store.GetRange(r.Context(), name, br)
	if err != nil {
		http.Error(w, "origin error\n", http.StatusBadGateway)
		return
	}
	defer body.Close()

	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", br.Start, br.EndInclusive, size))
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(http.StatusPartialContent)

	_, _ = io.Copy(w, body)
}
