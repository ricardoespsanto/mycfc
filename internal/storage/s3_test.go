package storage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3StorePutObjectSendsPrivateUploadMetadata(t *testing.T) {
	type receivedRequest struct {
		method string
		path   string
		header http.Header
		body   string
	}
	request := make(chan receivedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		request <- receivedRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: string(body)}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := NewS3Store(testS3Client(t, server.URL), "private-photos")
	ctx := WithUploadMetadata(context.Background(), UploadMetadata{RequestID: "request-123", UserID: "user-456"})
	if err := store.PutObject(ctx, "repairs/one.png", "image/png", 4, strings.NewReader("data")); err != nil {
		t.Fatalf("PutObject() error = %v", err)
	}

	got := <-request
	if got.method != http.MethodPut || got.path != "/private-photos/repairs/one.png" {
		t.Fatalf("request = %s %s", got.method, got.path)
	}
	if got.header.Get("Content-Type") != "image/png" || got.header.Get("X-Amz-Server-Side-Encryption") != "AES256" {
		t.Fatalf("upload headers = %#v", got.header)
	}
	if got.header.Get("X-Amz-Meta-Request-Id") != "request-123" || got.header.Get("X-Amz-Meta-Uploaded-By-User-Id") != "user-456" {
		t.Fatalf("metadata headers = %#v", got.header)
	}
	if got.body != "data" {
		t.Fatalf("body = %q", got.body)
	}
}

func TestS3StoreWrapsServiceFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	store := NewS3Store(testS3Client(t, server.URL), "private-photos")
	if err := store.PutObject(context.Background(), "repairs/one.png", "image/png", 4, strings.NewReader("data")); err == nil || !strings.Contains(err.Error(), "put repair photo") {
		t.Fatalf("PutObject() error = %v", err)
	}
	if err := store.DeleteObject(context.Background(), "repairs/one.png"); err == nil || !strings.Contains(err.Error(), "delete object") {
		t.Fatalf("DeleteObject() error = %v", err)
	}
}

func TestS3StorePresignsInlineContentWithOptionalType(t *testing.T) {
	store := NewS3Store(testS3Client(t, "https://objects.example.test"), "private-photos")
	url, err := store.PresignGet(WithPresignContentType(context.Background(), "image/png"), "avatars/one.png", time.Minute)
	if err != nil {
		t.Fatalf("PresignGet() error = %v", err)
	}
	if !strings.Contains(url, "/private-photos/avatars/one.png") || !strings.Contains(url, "response-content-disposition=inline") || !strings.Contains(url, "response-content-type=image%2Fpng") {
		t.Fatalf("presigned URL = %q", url)
	}
}

func testS3Client(t *testing.T, endpoint string) *s3.Client {
	t.Helper()
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("eu-west-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatal(err)
	}
	return s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	})
}
