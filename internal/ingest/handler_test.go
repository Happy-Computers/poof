package ingest

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRangeHandlerServesOnlyAuthorizedAcceptedBytes(t *testing.T) {
	manager := newTestManager(t, &memoryStore{})
	file, err := manager.Reserve("clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("abcdef"), 0); err != nil {
		t.Fatal(err)
	}
	handler, err := NewRangeHandler(manager, "secret")
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := httptest.NewRequest(http.MethodGet, "/object/clip.mp4", nil)
	unauthorized.Header.Set("Range", "bytes=0-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, unauthorized)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status: %d", response.Code)
	}
	authorized := httptest.NewRequest(http.MethodGet, "/object/clip.mp4", nil)
	authorized.Header.Set("Authorization", "Bearer secret")
	authorized.Header.Set("Range", "bytes=2-4")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, authorized)
	if response.Code != http.StatusPartialContent || response.Body.String() != "cde" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}
