package ingest

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type RangeHandler struct {
	manager *Manager
	token   string
	reads   chan struct{}
}

func NewRangeHandler(manager *Manager, token string) (*RangeHandler, error) {
	if manager == nil {
		return nil, fmt.Errorf("ingest: manager required")
	}
	if token == "" {
		return nil, fmt.Errorf("ingest: range token required")
	}
	return &RangeHandler{manager: manager, token: token, reads: make(chan struct{}, MaxPeerRangeReads)}, nil
}

func (h *RangeHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(request) {
		response.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	name, err := h.name(request.URL.Path)
	if err != nil {
		http.Error(response, "not found", http.StatusNotFound)
		return
	}
	file, ok := h.manager.Lookup(name)
	if !ok {
		http.Error(response, "not found", http.StatusNotFound)
		return
	}
	start, end, err := parseRange(request.Header.Get("Range"))
	if err != nil {
		response.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", file.Size()))
		http.Error(response, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	length := end - start + 1
	if length > MaxPeerRangeBytes {
		http.Error(response, "range too large", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	select {
	case h.reads <- struct{}{}:
		defer func() { <-h.reads }()
	default:
		http.Error(response, "range origin busy", http.StatusServiceUnavailable)
		return
	}
	content := make([]byte, length)
	if _, err := file.ReadAt(content, start); err != nil {
		http.Error(response, "range unavailable", http.StatusServiceUnavailable)
		return
	}
	response.Header().Set("Accept-Ranges", "bytes")
	response.Header().Set("Content-Length", strconv.FormatUint(length, 10))
	response.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, file.Size()))
	response.WriteHeader(http.StatusPartialContent)
	_, _ = response.Write(content)
}

func (h *RangeHandler) authorized(request *http.Request) bool {
	value := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	if len(value) != len(h.token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(value), []byte(h.token)) == 1
}

func (h *RangeHandler) name(path string) (string, error) {
	prefix := "/object/"
	if !strings.HasPrefix(path, prefix) {
		return "", fmt.Errorf("invalid object path")
	}
	name, err := url.PathUnescape(strings.TrimPrefix(path, prefix))
	if err != nil {
		return "", err
	}
	if err := validateName(name); err != nil {
		return "", err
	}
	return name, nil
}

func parseRange(value string) (uint64, uint64, error) {
	if !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") {
		return 0, 0, fmt.Errorf("invalid range")
	}
	startText, endText, ok := strings.Cut(strings.TrimPrefix(value, "bytes="), "-")
	if !ok || startText == "" || endText == "" {
		return 0, 0, fmt.Errorf("invalid range")
	}
	start, err := strconv.ParseUint(startText, 10, 64)
	if err != nil {
		return 0, 0, err
	}
	end, err := strconv.ParseUint(endText, 10, 64)
	if err != nil || end < start {
		return 0, 0, fmt.Errorf("invalid range")
	}
	return start, end, nil
}
