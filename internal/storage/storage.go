package storage

import (
	"context"
	"io"
	"time"
)

type ObjectStore interface {
	PutObject(ctx context.Context, key, contentType string, size int64, body io.Reader) error
	DeleteObject(ctx context.Context, key string) error
	PresignGet(ctx context.Context, key string, lifetime time.Duration) (string, error)
}

type uploadMetadataKey struct{}
type presignContentTypeKey struct{}

type UploadMetadata struct {
	RequestID string
	UserID    string
}

func WithUploadMetadata(ctx context.Context, metadata UploadMetadata) context.Context {
	return context.WithValue(ctx, uploadMetadataKey{}, metadata)
}

func uploadMetadata(ctx context.Context) UploadMetadata {
	metadata, _ := ctx.Value(uploadMetadataKey{}).(UploadMetadata)
	return metadata
}

func WithPresignContentType(ctx context.Context, contentType string) context.Context {
	return context.WithValue(ctx, presignContentTypeKey{}, contentType)
}

func presignContentType(ctx context.Context) string {
	value, _ := ctx.Value(presignContentTypeKey{}).(string)
	return value
}
