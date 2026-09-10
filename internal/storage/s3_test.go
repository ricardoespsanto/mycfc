package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3StorePutObjectOmitsIdentifyingUploadMetadata(t *testing.T) {
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
	if err := store.PutObject(context.Background(), "repairs/one.png", "image/png", 4, strings.NewReader("data")); err != nil {
		t.Fatalf("PutObject() error = %v", err)
	}

	got := <-request
	if got.method != http.MethodPut || got.path != "/private-photos/repairs/one.png" {
		t.Fatalf("request = %s %s", got.method, got.path)
	}
	if got.header.Get("Content-Type") != "image/png" || got.header.Get("X-Amz-Server-Side-Encryption") != "AES256" {
		t.Fatalf("upload headers = %#v", got.header)
	}
	for name := range got.header {
		if strings.HasPrefix(strings.ToLower(name), "x-amz-meta-") {
			t.Fatalf("identifying metadata header %q was sent", name)
		}
	}
	if got.body != "data" {
		t.Fatalf("body = %q", got.body)
	}
}

func TestS3StoreWrapsServiceFailures(t *testing.T) {
	secretKey := "repairs/private-do-not-log.png"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable for "+secretKey, http.StatusServiceUnavailable)
	}))
	defer server.Close()

	store := NewS3Store(testS3Client(t, server.URL), "private-photos")
	if err := store.PutObject(context.Background(), secretKey, "image/png", 4, strings.NewReader("data")); !errors.Is(err, ErrObjectUpload) || strings.Contains(err.Error(), secretKey) {
		t.Fatalf("PutObject() error = %v", err)
	}
	if err := store.DeleteObject(context.Background(), secretKey); !errors.Is(err, ErrObjectDeletion) || strings.Contains(err.Error(), secretKey) {
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

func TestS3StorePresignsTheConfiguredRegionalBucketOrigin(t *testing.T) {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("eu-west-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatal(err)
	}
	store := NewS3Store(s3.NewFromConfig(cfg), "mycfc-production-repairs")
	signed, err := store.PresignGet(context.Background(), "equipment/one.png", time.Minute)
	if err != nil {
		t.Fatalf("PresignGet() error = %v", err)
	}
	u, err := url.Parse(signed)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}
	if got := u.Scheme + "://" + u.Host; got != "https://mycfc-production-repairs.s3.eu-west-1.amazonaws.com" {
		t.Fatalf("presigned origin = %q", got)
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
