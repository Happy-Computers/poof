package s3origin

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"

	"github.com/amaan/infinity-storage/internal/catalog"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ObjectMeta is one flat Infinity Storage file backed by an S3 key.
type ObjectMeta struct {
	Name string // basename shown in /tmp/infinity-storage
	Key  string // full S3 object key
	Size uint64
}

// Store serves ranged reads for one or more S3 objects (flat Infinity Storage).
// Catalog can be refreshed via Refresh (ListObjectsV2 again).
type Store struct {
	mu     sync.RWMutex
	client *s3.Client
	bucket string
	prefix string
	byName map[string]ObjectMeta
	order  []string
	sem    chan struct{}
}

// NewStoreFromKey heads one object (single-file / legacy CLI).
func NewStoreFromKey(ctx context.Context, client *s3.Client, bucket, key string) (*Store, error) {
	if bucket == "" || key == "" {
		return nil, fmt.Errorf("bucket and key required")
	}
	head, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("head object s3://%s/%s: %w", bucket, key, err)
	}
	if head.ContentLength == nil || *head.ContentLength <= 0 {
		return nil, fmt.Errorf("head object s3://%s/%s: empty or unknown size", bucket, key)
	}
	name := path.Base(key)
	if name == "." || name == "/" || name == "" {
		name = "object"
	}
	meta := ObjectMeta{Name: name, Key: key, Size: uint64(*head.ContentLength)}
	return newStore(client, bucket, "", []ObjectMeta{meta})
}

// NewStoreFromList lists flat objects under prefix (Delimiter=/) and builds a store.
func NewStoreFromList(ctx context.Context, client *s3.Client, bucket, prefix string) (*Store, error) {
	metas, err := ListFlat(ctx, client, bucket, prefix)
	if err != nil {
		return nil, err
	}
	return newStore(client, bucket, prefix, metas)
}

func newStore(client *s3.Client, bucket, prefix string, metas []ObjectMeta) (*Store, error) {
	if bucket == "" {
		return nil, fmt.Errorf("bucket required")
	}
	byName, order, err := indexMetas(metas)
	if err != nil {
		return nil, err
	}
	return &Store{
		client: client,
		bucket: bucket,
		prefix: prefix,
		byName: byName,
		order:  order,
		sem:    make(chan struct{}, MaxConcurrentS3Fetches),
	}, nil
}

func indexMetas(metas []ObjectMeta) (map[string]ObjectMeta, []string, error) {
	byName := make(map[string]ObjectMeta, len(metas))
	order := make([]string, 0, len(metas))
	for _, m := range metas {
		if err := validateMeta(m); err != nil {
			return nil, nil, err
		}
		if _, ok := byName[m.Name]; ok {
			return nil, nil, fmt.Errorf("duplicate basename: %q", m.Name)
		}
		byName[m.Name] = m
		order = append(order, m.Name)
	}
	if len(byName) > catalog.MaxFiles {
		return nil, nil, fmt.Errorf("too many files (>%d)", catalog.MaxFiles)
	}
	return byName, order, nil
}

func validateMeta(m ObjectMeta) error {
	if m.Name == "" || m.Key == "" {
		return fmt.Errorf("object meta missing name/key")
	}
	if len(m.Name) > catalog.MaxNameBytes {
		return fmt.Errorf("name too long (%d > %d): %q", len(m.Name), catalog.MaxNameBytes, m.Name)
	}
	if strings.Contains(m.Name, "/") {
		return fmt.Errorf("name must be basename: %q", m.Name)
	}
	if m.Size == 0 {
		return fmt.Errorf("empty object not allowed: %q", m.Name)
	}
	return nil
}

// Refresh re-lists the bucket and replaces the in-memory catalog.
func (s *Store) Refresh(ctx context.Context) error {
	if s.client == nil {
		return fmt.Errorf("s3 client not configured")
	}
	metas, err := ListFlat(ctx, s.client, s.bucket, s.prefix)
	if err != nil {
		return err
	}
	byName, order, err := indexMetas(metas)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.byName = byName
	s.order = order
	s.mu.Unlock()
	return nil
}

// Objects returns catalog entries in list order.
func (s *Store) Objects() []ObjectMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ObjectMeta, 0, len(s.order))
	for _, name := range s.order {
		out = append(out, s.byName[name])
	}
	return out
}

// Bucket returns the S3 bucket name.
func (s *Store) Bucket() string { return s.bucket }

// Prefix returns the list prefix.
func (s *Store) Prefix() string { return s.prefix }

// Len returns object count.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byName)
}

// Lookup returns metadata for a Infinity Storage basename.
func (s *Store) Lookup(name string) (ObjectMeta, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.byName[name]
	return m, ok
}

// Single returns the sole object when the store has exactly one entry.
func (s *Store) Single() (ObjectMeta, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.order) != 1 {
		return ObjectMeta{}, false
	}
	return s.byName[s.order[0]], true
}

func (s *Store) acquire(ctx context.Context) error {
	select {
	case s.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store) release() {
	<-s.sem
}

// CachedSize returns the size known at catalog time (no S3 round-trip).
func (s *Store) CachedSize(name string) (uint64, bool) {
	m, ok := s.Lookup(name)
	if !ok {
		return 0, false
	}
	return m.Size, true
}

// GetRange fetches an inclusive byte range for name from S3.
func (s *Store) GetRange(ctx context.Context, name string, br ByteRange) (io.ReadCloser, int64, error) {
	s.mu.RLock()
	m, ok := s.byName[name]
	bucket := s.bucket
	client := s.client
	s.mu.RUnlock()
	if !ok {
		return nil, 0, fmt.Errorf("unknown object %q", name)
	}
	if err := s.acquire(ctx); err != nil {
		return nil, 0, err
	}
	if client == nil {
		s.release()
		return nil, 0, fmt.Errorf("s3 client not configured")
	}

	rangeHeader := fmt.Sprintf("bytes=%d-%d", br.Start, br.EndInclusive)
	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(m.Key),
		Range:  aws.String(rangeHeader),
	})
	if err != nil {
		s.release()
		return nil, 0, err
	}

	length := int64(br.Length())
	if out.ContentLength != nil && *out.ContentLength > 0 {
		length = *out.ContentLength
	}
	return &releaseOnClose{ReadCloser: out.Body, release: s.release}, length, nil
}

type releaseOnClose struct {
	io.ReadCloser
	release func()
}

func (r *releaseOnClose) Close() error {
	err := r.ReadCloser.Close()
	r.release()
	return err
}

// NewOrigin is kept for callers that still expect a single-object Origin.
func NewOrigin(ctx context.Context, client *s3.Client, bucket, key string) (*Store, error) {
	return NewStoreFromKey(ctx, client, bucket, key)
}
