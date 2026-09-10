//go:build integration

package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
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
	if err := store.PutObject(ctx, key, "image/png", int64(len(body)), bytes.NewReader(body)); err != nil {
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

func TestS3VersionedStoreDeletesEveryExactVersionMarkerAndNullVersion(t *testing.T) {
	ctx := context.Background()
	config, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(os.Getenv("AWS_REGION")))
	if err != nil {
		t.Fatal(err)
	}
	client := s3.NewFromConfig(config, func(options *s3.Options) {
		options.UsePathStyle = true
		options.BaseEndpoint = aws.String(os.Getenv("S3_ENDPOINT"))
	})
	bucket := os.Getenv("S3_BUCKET_NAME")
	if _, err = client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{Bucket: aws.String(bucket), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = client.PutBucketVersioning(context.Background(), &s3.PutBucketVersioningInput{Bucket: aws.String(bucket), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}})
	})

	key := "repairs/versioned-integration/" + uuid.NewString() + ".png"
	sibling := key + ".sibling"
	for _, body := range []string{"version-one", "version-two"} {
		if _, err = client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: strings.NewReader(body)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(sibling), Body: strings.NewReader("must remain")}); err != nil {
		t.Fatal(err)
	}

	// A suspended versioning bucket represents unversioned writes as the
	// explicit "null" version, which must be permanently deleted as well.
	if _, err = client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{Bucket: aws.String(bucket), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusSuspended}}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: strings.NewReader("null-version")}); err != nil {
		t.Fatal(err)
	}

	store := NewS3VersionedStore(client, bucket)
	evidence, err := store.DeleteAllVersions(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.DeletedVersions < 3 || evidence.DeletedMarkers < 1 || evidence.StableChecks != 2 {
		t.Fatalf("version deletion evidence = %+v", evidence)
	}
	listed, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(bucket), Prefix: aws.String(key)})
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range listed.Versions {
		if aws.ToString(version.Key) == key {
			t.Fatalf("exact object version remains: %q", aws.ToString(version.VersionId))
		}
	}
	for _, marker := range listed.DeleteMarkers {
		if aws.ToString(marker.Key) == key {
			t.Fatalf("exact delete marker remains: %q", aws.ToString(marker.VersionId))
		}
	}
	if _, err = client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(sibling)}); err != nil {
		t.Fatalf("prefix sibling was deleted: %v", err)
	}
	if _, err = store.DeleteAllVersions(ctx, sibling); err != nil {
		t.Fatalf("cleanup sibling: %v", err)
	}
}
