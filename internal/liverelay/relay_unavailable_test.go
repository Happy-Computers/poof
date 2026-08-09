package liverelay

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRelayReturnsUnavailableWriterRangeImmediately(t *testing.T) {
	server, err := NewServer("secret")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	client := &http.Client{Timeout: time.Second}

	body, err := json.Marshal(Stream{Name: "clip.mp4", Size: 6, State: "streaming", WriterID: "writer-a"})
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
		request, err = http.NewRequest(http.MethodPut, httpServer.URL+"/v1/writers/demo/ranges/"+job.ID, nil)
		if err != nil {
			t.Error(err)
			return
		}
		request.Header.Set("Authorization", "Bearer secret")
		request.Header.Set(rangeErrorHeader, rangeUnavailableValue)
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
	request.Header.Set("Range", "bytes=0-2")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: %d", response.StatusCode)
	}
}
