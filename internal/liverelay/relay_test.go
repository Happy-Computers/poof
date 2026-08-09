package liverelay

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRelayPublishesAndServesWriterRanges(t *testing.T) {
	server, err := NewServer("secret")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	client := &http.Client{Timeout: time.Second}

	publish := Stream{Name: "clip.mp4", Size: 6, State: "streaming", WriterID: "writer-a"}
	body, err := json.Marshal(publish)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPut, httpServer.URL+"/v1/streams/demo/clip.mp4", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("publish status: %d", response.StatusCode)
	}

	go func() {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, httpServer.URL+"/v1/writers/demo/writer-a/next", nil)
		if err != nil {
			t.Error(err)
			return
		}
		request.Header.Set("Authorization", "Bearer secret")
		response, err := client.Do(request)
		if err != nil {
			t.Error(err)
			return
		}
		defer response.Body.Close()
		var job rangeJob
		if err := json.NewDecoder(response.Body).Decode(&job); err != nil {
			t.Error(err)
			return
		}
		request, err = http.NewRequest(http.MethodPut, httpServer.URL+"/v1/writers/demo/ranges/"+job.ID, bytes.NewBufferString("cde"))
		if err != nil {
			t.Error(err)
			return
		}
		request.Header.Set("Authorization", "Bearer secret")
		response, err = client.Do(request)
		if err != nil {
			t.Error(err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Errorf("range completion status: %d", response.StatusCode)
		}
	}()

	request, err = http.NewRequest(http.MethodGet, httpServer.URL+"/v1/live/demo/clip.mp4", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Range", "bytes=2-4")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusPartialContent || string(content) != "cde" {
		t.Fatalf("status=%d content=%q", response.StatusCode, content)
	}
}

func TestRelayRejectsUnauthorizedRequests(t *testing.T) {
	server, err := NewServer("secret")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/streams/demo", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status: %d", response.Code)
	}
}
