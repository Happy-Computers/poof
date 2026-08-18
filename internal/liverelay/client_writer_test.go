package liverelay

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/amaan/infinity-storage/internal/ingest"
)

func TestClientRoutesRangeToPublishingWriter(t *testing.T) {
	server, err := NewServer("secret")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	writer, err := NewClient(Config{
		URL:      httpServer.URL,
		Library:  "demo",
		Token:    "secret",
		WriterID: "writer-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	observer, err := NewClient(Config{
		URL:      httpServer.URL,
		Library:  "demo",
		Token:    "secret",
		WriterID: "writer-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	writerManager, err := ingest.NewManager(ingest.Config{
		SpoolDir:  t.TempDir(),
		Store:     testStore{},
		RangeWait: time.Second,
		Publish: func(snapshot ingest.Snapshot) {
			_ = writer.Publish(context.Background(), snapshot)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	observerManager, err := ingest.NewManager(ingest.Config{
		SpoolDir:  t.TempDir(),
		Store:     testStore{},
		RangeWait: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer.Start(ctx, writerManager)
	observer.Start(ctx, observerManager)

	file, err := writerManager.Reserve("clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("abcdef"), 0); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		entries, err := observer.Load(context.Background())
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
