package liverelay

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amaan/infinity-storage/internal/catalog"
	"github.com/amaan/infinity-storage/internal/ingest"
)

type Config struct {
	URL      string
	Library  string
	Token    string
	WriterID string
}

type Client struct {
	base     *url.URL
	library  string
	token    string
	writerID string
	http     *http.Client

	mu      sync.Mutex
	sources map[string]*Source
}

type Source struct {
	client *Client
	name   string
	size   atomic.Uint64
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.URL == "" || cfg.Library == "" || cfg.Token == "" {
		return nil, fmt.Errorf("live relay: URL, library, and token required")
	}
	base, err := url.Parse(cfg.URL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("live relay: invalid URL")
	}
	if !validName(cfg.Library) {
		return nil, fmt.Errorf("live relay: invalid library")
	}
	writerID := cfg.WriterID
	if writerID == "" {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, fmt.Errorf("live relay: generate writer ID: %w", err)
		}
		writerID = hex.EncodeToString(random[:])
	}
	if !validName(writerID) {
		return nil, fmt.Errorf("live relay: invalid writer ID")
	}
	return &Client{
		base:     base,
		library:  cfg.Library,
		token:    cfg.Token,
		writerID: writerID,
		http:     &http.Client{Timeout: RangeWait + 5*time.Second},
		sources:  make(map[string]*Source),
	}, nil
}

func (c *Client) Publish(ctx context.Context, snapshot ingest.Snapshot) error {
	if snapshot.State == ingest.StateReserved {
		return nil
	}
	stream := Stream{Name: snapshot.Name, Size: snapshot.Size, State: string(snapshot.State), WriterID: c.writerID}
	body, err := json.Marshal(stream)
	if err != nil {
		return err
	}
	request, err := c.request(ctx, http.MethodPut, "/v1/streams/"+escaped(c.library)+"/"+escaped(snapshot.Name), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return responseError(response)
	}
	return nil
}

func (c *Client) Load(ctx context.Context) ([]catalog.Entry, error) {
	request, err := c.request(ctx, http.MethodGet, "/v1/streams/"+escaped(c.library), nil)
	if err != nil {
		return nil, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, responseError(response)
	}
	var streams []Stream
	if err := json.NewDecoder(io.LimitReader(response.Body, 128*1024)).Decode(&streams); err != nil {
		return nil, err
	}
	entries := make([]catalog.Entry, 0, len(streams))
	for _, stream := range streams {
		if !validName(stream.Name) || !validName(stream.WriterID) || stream.Size == 0 || !validState(stream.State) {
			return nil, fmt.Errorf("live relay: invalid stream catalog entry")
		}
		c.mu.Lock()
		source := c.sources[stream.Name]
		if source == nil {
			source = &Source{client: c, name: stream.Name}
			c.sources[stream.Name] = source
		}
		source.size.Store(stream.Size)
		c.mu.Unlock()
		entries = append(entries, catalog.Entry{Name: stream.Name, Source: source, Size: stream.Size})
	}
	return entries, nil
}

func (c *Client) Start(ctx context.Context, manager *ingest.Manager) {
	for index := 0; index < MaxRanges; index++ {
		go c.serveWriter(ctx, manager)
	}
}

func (c *Client) serveWriter(ctx context.Context, manager *ingest.Manager) {
	for ctx.Err() == nil {
		job, ok, err := c.next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if !ok {
			continue
		}
		file, found := manager.Lookup(job.Name)
		if !found {
			log.Printf("live relay writer missing name=%s start=%d length=%d", job.Name, job.Start, job.Length)
			_ = c.respondUnavailable(ctx, job.ID)
			continue
		}
		content := make([]byte, job.Length)
		if _, err := file.ReadAt(content, job.Start); err != nil {
			log.Printf("live relay writer unavailable name=%s start=%d length=%d error=%v", job.Name, job.Start, job.Length, err)
			_ = c.respondUnavailable(ctx, job.ID)
			continue
		}
		if err := c.respond(ctx, job.ID, content); err != nil {
			log.Printf("live relay writer response name=%s start=%d length=%d error=%v", job.Name, job.Start, job.Length, err)
		}
	}
}

func (c *Client) next(ctx context.Context) (rangeJob, bool, error) {
	request, err := c.request(ctx, http.MethodGet, "/v1/writers/"+escaped(c.library)+"/"+escaped(c.writerID)+"/next", nil)
	if err != nil {
		return rangeJob{}, false, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return rangeJob{}, false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return rangeJob{}, false, nil
	}
	if response.StatusCode != http.StatusOK {
		return rangeJob{}, false, responseError(response)
	}
	var job rangeJob
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&job); err != nil {
		return rangeJob{}, false, err
	}
	if job.ID == "" || !validName(job.Name) || job.Length == 0 || job.Length > MaxRangeBytes {
		return rangeJob{}, false, fmt.Errorf("live relay: invalid range job")
	}
	return job, true, nil
}

func (c *Client) respond(ctx context.Context, id string, content []byte) error {
	request, err := c.request(ctx, http.MethodPut, "/v1/writers/"+escaped(c.library)+"/ranges/"+escaped(id), bytes.NewReader(content))
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return responseError(response)
	}
	return nil
}

func (c *Client) respondUnavailable(ctx context.Context, id string) error {
	request, err := c.request(ctx, http.MethodPut, "/v1/writers/"+escaped(c.library)+"/ranges/"+escaped(id), nil)
	if err != nil {
		return err
	}
	request.Header.Set(rangeErrorHeader, rangeUnavailableValue)
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return responseError(response)
	}
	return nil
}

func (s *Source) ReadAt(dest []byte, offset uint64) (int, error) {
	if len(dest) == 0 || uint64(len(dest)) > MaxRangeBytes {
		return 0, fmt.Errorf("live relay: invalid range length")
	}
	if offset > ^uint64(0)-uint64(len(dest)) {
		return 0, fmt.Errorf("live relay: range overflow")
	}
	end := offset + uint64(len(dest)) - 1
	ctx, cancel := context.WithTimeout(context.Background(), RangeWait+5*time.Second)
	defer cancel()
	request, err := s.client.request(ctx, http.MethodGet, "/v1/live/"+escaped(s.client.library)+"/"+escaped(s.name), nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Range", "bytes="+strconv.FormatUint(offset, 10)+"-"+strconv.FormatUint(end, 10))
	response, err := s.client.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent {
		return 0, responseError(response)
	}
	n, err := io.ReadFull(io.LimitReader(response.Body, int64(len(dest))), dest)
	if err != nil {
		return n, err
	}
	return n, nil
}

func (s *Source) Size() uint64 {
	return s.size.Load()
}

func (c *Client) request(ctx context.Context, method, requestPath string, body io.Reader) (*http.Request, error) {
	url := *c.base
	url.Path = strings.TrimRight(c.base.Path, "/") + requestPath
	request, err := http.NewRequestWithContext(ctx, method, url.String(), body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	return request, nil
}

func responseError(response *http.Response) error {
	content, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	message := strings.TrimSpace(string(content))
	if message == "" {
		message = response.Status
	}
	return fmt.Errorf("live relay: %s", message)
}

var _ catalog.Source = (*Source)(nil)
