package s3origin

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// Handler serves HEAD and range GET for /object.
type Handler struct {
	origin *Origin
}

func NewHandler(origin *Origin) *Handler {
	return &Handler{origin: origin}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/object" {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodHead:
		h.handleHead(w, r)
	case http.MethodGet:
		h.handleGet(w, r)
	default:
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed\n", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleHead(w http.ResponseWriter, r *http.Request) {
	size, err := h.origin.Head(r.Context())
	if err != nil {
		http.Error(w, "origin error\n", http.StatusBadGateway)
		return
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	rangeHeader := r.Header.Get("Range")
	if rangeHeader == "" {
		w.Header().Set("Accept-Ranges", "bytes")
		http.Error(w, "Range header required\n", http.StatusBadRequest)
		return
	}

	br, err := ParseBytesRange(rangeHeader, h.origin.ObjectSize())
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", h.origin.ObjectSize()))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}

	if br.Length() > MaxRangeBytes {
		http.Error(w, "range too large\n", http.StatusRequestEntityTooLarge)
		return
	}

	body, length, err := h.origin.GetRange(r.Context(), br)
	if err != nil {
		http.Error(w, "origin error\n", http.StatusBadGateway)
		return
	}
	defer body.Close()

	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", br.Start, br.EndInclusive, h.origin.ObjectSize()))
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(http.StatusPartialContent)

	if _, err := io.Copy(w, body); err != nil {
		return
	}
}
