package liverelay

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MaxRangeBytes = 8 * 1024 * 1024
	MaxRanges     = 8
	WriterWait    = 25 * time.Second
	RangeWait     = 30 * time.Second
)

type Stream struct {
	Name  string `json:"name"`
	Size  uint64 `json:"size"`
	State string `json:"state"`
}

type Server struct {
	mu      sync.Mutex
	token   string
	streams map[string]map[string]Stream
	queues  map[string]chan *ticket
	pending map[string]*ticket
}

type ticket struct {
	id     string
	name   string
	start  uint64
	length uint64
	result chan rangeResult
}

type rangeResult struct {
	content []byte
	err     string
}

type rangeJob struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Start  uint64 `json:"start"`
	Length uint64 `json:"length"`
}

func NewServer(token string) (*Server, error) {
	if token == "" {
		return nil, fmt.Errorf("live relay: token required")
	}
	return &Server{
		token:   token,
		streams: make(map[string]map[string]Stream),
		queues:  make(map[string]chan *ticket),
		pending: make(map[string]*ticket),
	}, nil
}

func (s *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if !s.authorized(request) {
		response.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "v1" {
		http.NotFound(response, request)
		return
	}
	switch parts[1] {
	case "streams":
		s.serveStreams(response, request, parts[2:])
	case "live":
		s.serveLive(response, request, parts[2:])
	case "writers":
		s.serveWriters(response, request, parts[2:])
	default:
		http.NotFound(response, request)
	}
}

func (s *Server) serveStreams(response http.ResponseWriter, request *http.Request, parts []string) {
	if len(parts) == 1 && request.Method == http.MethodGet {
		s.writeJSON(response, http.StatusOK, s.list(parts[0]))
		return
	}
	if len(parts) == 2 && request.Method == http.MethodPut {
		var stream Stream
		if err := json.NewDecoder(io.LimitReader(request.Body, 1024)).Decode(&stream); err != nil {
			http.Error(response, "invalid stream", http.StatusBadRequest)
			return
		}
		if stream.Name != parts[1] || !validName(stream.Name) || !validState(stream.State) {
			http.Error(response, "invalid stream", http.StatusBadRequest)
			return
		}
		s.publish(parts[0], stream)
		response.WriteHeader(http.StatusNoContent)
		return
	}
	http.NotFound(response, request)
}

func (s *Server) serveLive(response http.ResponseWriter, request *http.Request, parts []string) {
	if len(parts) != 2 || !validName(parts[1]) {
		http.NotFound(response, request)
		return
	}
	stream, ok := s.lookup(parts[0], parts[1])
	if !ok || stream.State == "durable" || stream.State == "aborted" {
		http.NotFound(response, request)
		return
	}
	if request.Method == http.MethodHead {
		response.Header().Set("Accept-Ranges", "bytes")
		response.Header().Set("Content-Length", strconv.FormatUint(stream.Size, 10))
		response.WriteHeader(http.StatusOK)
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET, HEAD")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	start, length, err := parseRange(request.Header.Get("Range"))
	if err != nil || length > MaxRangeBytes {
		response.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", stream.Size))
		http.Error(response, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	content, err := s.requestRange(request.Context(), parts[0], parts[1], start, length)
	if err != nil {
		http.Error(response, "range unavailable", http.StatusServiceUnavailable)
		return
	}
	end := start + uint64(len(content)) - 1
	response.Header().Set("Accept-Ranges", "bytes")
	response.Header().Set("Content-Length", strconv.Itoa(len(content)))
	response.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, stream.Size))
	response.WriteHeader(http.StatusPartialContent)
	_, _ = response.Write(content)
}

func (s *Server) serveWriters(response http.ResponseWriter, request *http.Request, parts []string) {
	if len(parts) == 2 && parts[1] == "next" && request.Method == http.MethodGet {
		job, ok := s.next(request.Context(), parts[0])
		if !ok {
			response.WriteHeader(http.StatusNoContent)
			return
		}
		s.writeJSON(response, http.StatusOK, job)
		return
	}
	if len(parts) == 3 && parts[1] == "ranges" && request.Method == http.MethodPut {
		s.complete(response, request, parts[2])
		return
	}
	http.NotFound(response, request)
}

func (s *Server) list(library string) []Stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	streams := s.streams[library]
	out := make([]Stream, 0, len(streams))
	for _, stream := range streams {
		if stream.State != "durable" && stream.State != "aborted" {
			out = append(out, stream)
		}
	}
	return out
}

func (s *Server) lookup(library, name string) (Stream, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stream, ok := s.streams[library][name]
	return stream, ok
}

func (s *Server) publish(library string, stream Stream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams[library] == nil {
		s.streams[library] = make(map[string]Stream)
	}
	previous, exists := s.streams[library][stream.Name]
	if exists && (previous.State == "durable" || previous.State == "aborted") {
		return
	}
	if exists && previous.Size > stream.Size {
		return
	}
	s.streams[library][stream.Name] = stream
}

func (s *Server) requestRange(ctx context.Context, library, name string, start, length uint64) ([]byte, error) {
	ticket, err := newTicket(name, start, length)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	queue := s.queue(library)
	if len(s.pending) >= MaxRanges {
		s.mu.Unlock()
		return nil, errors.New("live relay: range limit reached")
	}
	s.pending[ticket.id] = ticket
	s.mu.Unlock()
	defer s.remove(ticket.id)
	select {
	case queue <- ticket:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case result := <-ticket.result:
		if result.err != "" {
			return nil, errors.New(result.err)
		}
		if uint64(len(result.content)) != length {
			return nil, errors.New("live relay: writer returned wrong range length")
		}
		return result.content, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(RangeWait):
		return nil, errors.New("live relay: writer range deadline exceeded")
	}
}

func (s *Server) next(ctx context.Context, library string) (rangeJob, bool) {
	s.mu.Lock()
	queue := s.queue(library)
	s.mu.Unlock()
	select {
	case ticket := <-queue:
		return rangeJob{ID: ticket.id, Name: ticket.name, Start: ticket.start, Length: ticket.length}, true
	case <-ctx.Done():
		return rangeJob{}, false
	case <-time.After(WriterWait):
		return rangeJob{}, false
	}
}

func (s *Server) complete(response http.ResponseWriter, request *http.Request, id string) {
	s.mu.Lock()
	ticket, ok := s.pending[id]
	s.mu.Unlock()
	if !ok {
		http.Error(response, "unknown range", http.StatusNotFound)
		return
	}
	content, err := io.ReadAll(io.LimitReader(request.Body, int64(ticket.length)+1))
	if err != nil || uint64(len(content)) != ticket.length {
		http.Error(response, "invalid range response", http.StatusBadRequest)
		return
	}
	select {
	case ticket.result <- rangeResult{content: content}:
		response.WriteHeader(http.StatusNoContent)
	default:
		http.Error(response, "range no longer pending", http.StatusGone)
	}
}

func (s *Server) queue(library string) chan *ticket {
	queue := s.queues[library]
	if queue == nil {
		queue = make(chan *ticket, MaxRanges)
		s.queues[library] = queue
	}
	return queue
}

func (s *Server) remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, id)
}

func (s *Server) authorized(request *http.Request) bool {
	value := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	if len(value) != len(s.token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(value), []byte(s.token)) == 1
}

func (s *Server) writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func newTicket(name string, start, length uint64) (*ticket, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	return &ticket{
		id:     hex.EncodeToString(random[:]),
		name:   name,
		start:  start,
		length: length,
		result: make(chan rangeResult, 1),
	}, nil
}

func parseRange(value string) (uint64, uint64, error) {
	if !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") {
		return 0, 0, errors.New("invalid range")
	}
	startText, endText, ok := strings.Cut(strings.TrimPrefix(value, "bytes="), "-")
	if !ok || startText == "" || endText == "" {
		return 0, 0, errors.New("invalid range")
	}
	start, err := strconv.ParseUint(startText, 10, 64)
	if err != nil {
		return 0, 0, err
	}
	end, err := strconv.ParseUint(endText, 10, 64)
	if err != nil || end < start || end == ^uint64(0) {
		return 0, 0, errors.New("invalid range")
	}
	return start, end - start + 1, nil
}

func validName(name string) bool {
	return name != "" && len(name) <= 255 && !strings.Contains(name, "/") && name != "." && name != ".."
}

func validState(state string) bool {
	switch state {
	case "streaming", "sealing", "interrupted", "durable", "aborted":
		return true
	default:
		return false
	}
}

func escaped(name string) string {
	return url.PathEscape(name)
}
