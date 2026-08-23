package liverelay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type libraryAuthorizer struct {
	library string
	token   string
}

func (a libraryAuthorizer) Authorize(ctx context.Context, token string, library string) error {
	if token == a.token && library == a.library {
		return nil
	}
	return fmt.Errorf("denied")
}

func TestHTTPAuthorizerPassesBearerAndLibrary(t *testing.T) {
	authority := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Fatalf("method=%s", request.Method)
		}
		if request.URL.Path != "/v1/libraries/demo/authorize" {
			t.Fatalf("path=%s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer session" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer authority.Close()
	authorizer, err := NewHTTPAuthorizer(authority.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := authorizer.Authorize(context.Background(), "session", "demo"); err != nil {
		t.Fatal(err)
	}
}

func TestRelayAuthorizesLibrary(t *testing.T) {
	server, err := NewServerWithAuthorizer(libraryAuthorizer{library: "demo", token: "session"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/streams/demo", nil)
	request.Header.Set("Authorization", "Bearer session")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized status=%d", response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/streams/other", nil)
	request.Header.Set("Authorization", "Bearer session")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", response.Code)
	}
}

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
