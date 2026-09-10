package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var (
	ErrVersionListing       = errors.New("versioned object listing failed")
	ErrVersionDeletion      = errors.New("versioned object deletion failed")
	ErrObjectVersionChanged = errors.New("object changed during absence verification")
	ErrUnstableAbsence      = errors.New("stable object absence was not established")
)

// VersionedObjectStore is intentionally separate from ObjectStore. Web
// handlers must not gain bucket-version listing or permanent-version deletion
// merely because the privacy executor needs those capabilities.
type VersionedObjectStore interface {
	DeleteAllVersions(ctx context.Context, key string) (VersionDeletionEvidence, error)
}

type versionedS3API interface {
	ListObjectVersions(context.Context, *s3.ListObjectVersionsInput, ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error)
	DeleteObjects(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}

type S3VersionedStore struct {
	client       versionedS3API
	bucket       string
	maxPasses    int
	stableChecks int
	beforeDelete func(context.Context) error
}

type VersionDeletionEvidence struct {
	DeletedVersions int
	DeletedMarkers  int
	ListCalls       int
	StableChecks    int
}

func NewS3VersionedStore(client *s3.Client, bucket string) *S3VersionedStore {
	return &S3VersionedStore{client: client, bucket: bucket, maxPasses: 10, stableChecks: 2}
}

// NewGuardedS3VersionedStore re-evaluates an external authorization boundary
// immediately before every destructive S3 request. The privacy worker uses it
// to ensure a database-clock activation revocation cannot be missed during a
// multi-pass, multi-batch exact-version cleanup.
func NewGuardedS3VersionedStore(client *s3.Client, bucket string, beforeDelete func(context.Context) error) *S3VersionedStore {
	return &S3VersionedStore{client: client, bucket: bucket, maxPasses: 10, stableChecks: 2, beforeDelete: beforeDelete}
}

type objectVersion struct {
	versionID    string
	deleteMarker bool
}

// DeleteAllVersions permanently removes every version and delete marker for
// one exact key. Prefix siblings are ignored. Success requires two complete,
// consecutive authoritative listings with no match; an object appearing after
// the first empty listing is treated as concurrent drift and blocks success.
func (s *S3VersionedStore) DeleteAllVersions(ctx context.Context, key string) (VersionDeletionEvidence, error) {
	var evidence VersionDeletionEvidence
	if s == nil || s.client == nil || strings.TrimSpace(s.bucket) == "" || key == "" || len(key) > 1024 || strings.ContainsRune(key, 0) {
		return evidence, ErrVersionListing
	}
	maxPasses := s.maxPasses
	if maxPasses < 1 {
		maxPasses = 10
	}
	stableChecks := s.stableChecks
	if stableChecks < 2 {
		stableChecks = 2
	}
	emptyChecks := 0
	var initialVersions map[string]struct{}
	for pass := 0; pass < maxPasses; pass++ {
		versions, calls, err := s.listExact(ctx, key)
		evidence.ListCalls += calls
		if err != nil {
			return evidence, err
		}
		if len(versions) == 0 {
			emptyChecks++
			evidence.StableChecks = emptyChecks
			if emptyChecks == stableChecks {
				return evidence, nil
			}
			continue
		}
		if emptyChecks > 0 {
			return evidence, ErrObjectVersionChanged
		}
		if initialVersions == nil {
			initialVersions = make(map[string]struct{}, len(versions))
			for _, version := range versions {
				initialVersions[version.versionID] = struct{}{}
			}
		} else {
			for _, version := range versions {
				if _, known := initialVersions[version.versionID]; !known {
					return evidence, ErrObjectVersionChanged
				}
			}
		}
		deletedVersions, deletedMarkers, deleteErr := s.deleteExact(ctx, key, versions)
		evidence.DeletedVersions += deletedVersions
		evidence.DeletedMarkers += deletedMarkers
		if deleteErr != nil {
			return evidence, deleteErr
		}
	}
	return evidence, ErrUnstableAbsence
}

func (s *S3VersionedStore) listExact(ctx context.Context, key string) ([]objectVersion, int, error) {
	paginator := s3.NewListObjectVersionsPaginator(s.client, &s3.ListObjectVersionsInput{
		Bucket: aws.String(s.bucket), Prefix: aws.String(key), MaxKeys: aws.Int32(1000),
	}, func(options *s3.ListObjectVersionsPaginatorOptions) { options.StopOnDuplicateToken = true })
	var out []objectVersion
	calls := 0
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		calls++
		if err != nil {
			return nil, calls, opaqueVersionError{kind: ErrVersionListing, cause: err}
		}
		for _, version := range page.Versions {
			if aws.ToString(version.Key) != key {
				continue
			}
			id := aws.ToString(version.VersionId)
			if id == "" {
				return nil, calls, ErrVersionListing
			}
			out = append(out, objectVersion{versionID: id})
		}
		for _, marker := range page.DeleteMarkers {
			if aws.ToString(marker.Key) != key {
				continue
			}
			id := aws.ToString(marker.VersionId)
			if id == "" {
				return nil, calls, ErrVersionListing
			}
			out = append(out, objectVersion{versionID: id, deleteMarker: true})
		}
	}
	return out, calls, nil
}

func (s *S3VersionedStore) deleteExact(ctx context.Context, key string, versions []objectVersion) (int, int, error) {
	deletedVersions, deletedMarkers := 0, 0
	for offset := 0; offset < len(versions); offset += 1000 {
		if s.beforeDelete != nil {
			if err := s.beforeDelete(ctx); err != nil {
				return deletedVersions, deletedMarkers, ErrVersionDeletion
			}
		}
		end := min(offset+1000, len(versions))
		objects := make([]types.ObjectIdentifier, end-offset)
		for index, version := range versions[offset:end] {
			objects[index] = types.ObjectIdentifier{Key: aws.String(key), VersionId: aws.String(version.versionID)}
		}
		result, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(s.bucket), Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(false)},
		})
		if err != nil {
			return deletedVersions, deletedMarkers, opaqueVersionError{kind: ErrVersionDeletion, cause: err}
		}
		if result == nil {
			return deletedVersions, deletedMarkers, ErrVersionDeletion
		}
		expected := make(map[string]bool, len(objects))
		for _, version := range versions[offset:end] {
			expected[version.versionID] = version.deleteMarker
		}
		seen := make(map[string]struct{}, len(result.Deleted))
		for _, deleted := range result.Deleted {
			id := aws.ToString(deleted.VersionId)
			deleteMarker, ok := expected[id]
			if aws.ToString(deleted.Key) != key || !ok {
				return deletedVersions, deletedMarkers, ErrVersionDeletion
			}
			if _, duplicate := seen[id]; duplicate {
				return deletedVersions, deletedMarkers, ErrVersionDeletion
			}
			seen[id] = struct{}{}
			if deleteMarker {
				deletedMarkers++
			} else {
				deletedVersions++
			}
		}
		if len(result.Errors) != 0 || len(seen) != len(expected) {
			return deletedVersions, deletedMarkers, ErrVersionDeletion
		}
	}
	return deletedVersions, deletedMarkers, nil
}

// opaqueVersionError keeps SDK details available to errors.Is/errors.As while
// ensuring an ordinary log of the returned error cannot disclose a bucket,
// object key, version ID, endpoint, or provider response.
type opaqueVersionError struct {
	kind  error
	cause error
}

func (e opaqueVersionError) Error() string                  { return e.kind.Error() }
func (e opaqueVersionError) Unwrap() []error                { return []error{e.kind, e.cause} }
func (e opaqueVersionError) Format(state fmt.State, _ rune) { _, _ = state.Write([]byte(e.Error())) }
