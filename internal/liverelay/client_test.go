package liverelay

import (
	"context"
	"crypto/sha256"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/amaan/infinity-storage/internal/ingest"
)

type testStore struct{}

type testUpload struct{}

func (testStore) Begin(ctx context.Context, name string) (ingest.Upload, error) {
	return testUpload{}, nil
}

func (testUpload) PutPart(ctx context.Context, number int32, content []byte, checksum [sha256.Size]byte) (ingest.Part, error) {
	return ingest.Part{Number: number, ETag: "part"}, nil
}

func (testUpload) Complete(ctx context.Context, parts []ingest.Part, size uint64, checksum [sha256.Size]byte) error {
	return nil
}

func (testUpload) Abort(ctx context.Context) error { return nil }

func TestClientPublishesStreamingFileAndServesRange(t *testing.T) {
	server, err := NewServer("secret")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	client, err := NewClient(Config{URL: httpServer.URL, Library: "demo", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := ingest.NewManager(ingest.Config{
		SpoolDir:  t.TempDir(),
		Store:     testStore{},
		RangeWait: time.Second,
		Publish: func(snapshot ingest.Snapshot) {
			_ = client.Publish(context.Background(), snapshot)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.Start(ctx, manager)
	file, err := manager.Reserve("clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("abcdef"), 0); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		entries, err := client.Load(context.Background())
		if err == nil && len(entries) == 1 {
			content := make([]byte, 3)
			if _, err := entries[0].Source.ReadAt(content, 2); err != nil {
				t.Fatal(err)
			}
			if string(content) != "cde" {
				t.Fatalf("content=%q", content)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stream catalog unavailable: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}
