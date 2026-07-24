//go:build integration

package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3StoreStoresAndDeletesRepairPhoto(t *testing.T) {
	ctx := context.Background()
	config, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(os.Getenv("AWS_REGION")))
	if err != nil {
		t.Fatal(err)
	}
	client := s3.NewFromConfig(config, func(options *s3.Options) {
		options.UsePathStyle = true
		options.BaseEndpoint = aws.String(os.Getenv("S3_ENDPOINT"))
	})
	store := NewS3Store(client, os.Getenv("S3_BUCKET_NAME"))
	key := "repairs/integration/" + t.Name() + ".png"
	body := []byte("a validated image fixture")
	if err := store.PutRepairPhoto(WithUploadMetadata(ctx, UploadMetadata{RequestID: "integration", UserID: "user"}), key, "image/png", int64(len(body)), bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DeleteObject(context.Background(), key) })

	object, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(os.Getenv("S3_BUCKET_NAME")), Key: aws.String(key)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(object.Body)
	_ = object.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) || aws.ToString(object.ContentType) != "image/png" {
		t.Fatalf("stored object = %q with content type %q", got, aws.ToString(object.ContentType))
	}

	if err := store.DeleteObject(ctx, key); err != nil {
		t.Fatal(err)
	}
	_, err = client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(os.Getenv("S3_BUCKET_NAME")), Key: aws.String(key)})
	if err == nil {
		t.Fatal("object still exists after deletion")
	}
}
