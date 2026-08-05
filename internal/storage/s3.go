package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type S3Store struct {
	client    *s3.Client
	presigner *s3.PresignClient
	bucket    string
}

func NewS3Store(client *s3.Client, bucket string) *S3Store {
	return &S3Store{
		client:    client,
		presigner: s3.NewPresignClient(client),
		bucket:    bucket,
	}
}

func (s *S3Store) PutObject(ctx context.Context, key, contentType string, size int64, body io.Reader) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	metadata := uploadMetadata(ctx)
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:               aws.String(s.bucket),
		Key:                  aws.String(key),
		Body:                 body,
		ContentLength:        aws.Int64(size),
		ContentType:          aws.String(contentType),
		ServerSideEncryption: types.ServerSideEncryptionAes256,
		Metadata: map[string]string{
			"request-id":          metadata.RequestID,
			"uploaded-by-user-id": metadata.UserID,
		},
	})
	if err != nil {
		return fmt.Errorf("put repair photo: %w", err)
	}
	return nil
}

func (s *S3Store) DeleteObject(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

func (s *S3Store) PresignGet(ctx context.Context, key string, lifetime time.Duration) (string, error) {
	input := &s3.GetObjectInput{
		Bucket:                     aws.String(s.bucket),
		Key:                        aws.String(key),
		ResponseContentDisposition: aws.String("inline"),
	}
	if contentType := presignContentType(ctx); contentType != "" {
		input.ResponseContentType = aws.String(contentType)
	}
	result, err := s.presigner.PresignGetObject(ctx, input, s3.WithPresignExpires(lifetime))
	if err != nil {
		return "", fmt.Errorf("presign object: %w", err)
	}
	return result.URL, nil
}
