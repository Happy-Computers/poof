package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3UploadCompleteIncludesPartChecksum(t *testing.T) {
	content := []byte("abcdef")
	partChecksum := sha256.Sum256(content)
	objectChecksum := sha256.Sum256(content)
	checksumText := base64.StdEncoding.EncodeToString(objectChecksum[:])
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Query().Get("uploadId") == "upload":
			if request.Header.Get("x-amz-checksum-sha256") != "" {
				t.Fatalf("complete request unexpectedly has an object checksum")
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			partChecksumText := base64.StdEncoding.EncodeToString(partChecksum[:])
			if !strings.Contains(string(body), "<ChecksumSHA256>"+partChecksumText+"</ChecksumSHA256>") {
				t.Fatalf("complete request missing part checksum: %s", body)
			}
			response.Header().Set("Content-Type", "application/xml")
			_, _ = response.Write([]byte("<CompleteMultipartUploadResult/>"))
		case request.Method == http.MethodHead:
			response.Header().Set("Content-Length", "6")
			response.Header().Set("x-amz-checksum-sha256", checksumText)
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL)
		}
	}))
	defer server.Close()
	client := s3.NewFromConfig(aws.Config{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
		BaseEndpoint: aws.String(server.URL),
	}, func(options *s3.Options) {
		options.UsePathStyle = true
	})
	upload := &s3Upload{client: client, bucket: "bucket", key: "clip.mp4", uploadID: "upload"}
	if err := upload.Complete(context.Background(), []Part{{
		Number:   1,
		ETag:     "etag",
		Checksum: partChecksum,
	}}, uint64(len(content)), objectChecksum); err != nil {
		t.Fatal(err)
	}
}
