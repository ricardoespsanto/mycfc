package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type versionedAPIFake struct {
	list    func(*s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error)
	delete  func(*s3.DeleteObjectsInput) (*s3.DeleteObjectsOutput, error)
	deleted [][]types.ObjectIdentifier
}

func (f *versionedAPIFake) ListObjectVersions(_ context.Context, input *s3.ListObjectVersionsInput, _ ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error) {
	return f.list(input)
}

func (f *versionedAPIFake) DeleteObjects(_ context.Context, input *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	f.deleted = append(f.deleted, input.Delete.Objects)
	if f.delete != nil {
		return f.delete(input)
	}
	deleted := make([]types.DeletedObject, len(input.Delete.Objects))
	for index, object := range input.Delete.Objects {
		deleted[index] = types.DeletedObject{Key: object.Key, VersionId: object.VersionId}
	}
	return &s3.DeleteObjectsOutput{Deleted: deleted}, nil
}

func TestVersionedStoreDeletesOnlyExactKeyAcrossPagesAndProvesStableAbsence(t *testing.T) {
	key := "profiles/2026/09/photo.png"
	page := 0
	api := &versionedAPIFake{}
	api.list = func(input *s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) {
		page++
		switch page {
		case 1:
			if aws.ToString(input.Prefix) != key || input.KeyMarker != nil {
				t.Fatalf("first list input = %+v", input)
			}
			return &s3.ListObjectVersionsOutput{
				Versions: []types.ObjectVersion{
					{Key: aws.String(key), VersionId: aws.String("v1")},
					{Key: aws.String(key + ".sibling"), VersionId: aws.String("sibling")},
				},
				IsTruncated: aws.Bool(true), NextKeyMarker: aws.String(key), NextVersionIdMarker: aws.String("v1"),
			}, nil
		case 2:
			if aws.ToString(input.KeyMarker) != key || aws.ToString(input.VersionIdMarker) != "v1" {
				t.Fatalf("second list markers = %q/%q", aws.ToString(input.KeyMarker), aws.ToString(input.VersionIdMarker))
			}
			return &s3.ListObjectVersionsOutput{
				Versions:      []types.ObjectVersion{{Key: aws.String(key), VersionId: aws.String("null")}},
				DeleteMarkers: []types.DeleteMarkerEntry{{Key: aws.String(key), VersionId: aws.String("m1")}},
			}, nil
		case 3, 4:
			return &s3.ListObjectVersionsOutput{}, nil
		default:
			t.Fatalf("unexpected list call %d", page)
			return nil, nil
		}
	}
	store := &S3VersionedStore{client: api, bucket: "private", maxPasses: 6, stableChecks: 2}
	evidence, err := store.DeleteAllVersions(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.DeletedVersions != 2 || evidence.DeletedMarkers != 1 || evidence.ListCalls != 4 || evidence.StableChecks != 2 {
		t.Fatalf("evidence = %+v", evidence)
	}
	if len(api.deleted) != 1 || len(api.deleted[0]) != 3 {
		t.Fatalf("deleted batches = %#v", api.deleted)
	}
	for _, object := range api.deleted[0] {
		if aws.ToString(object.Key) != key || aws.ToString(object.VersionId) == "sibling" {
			t.Fatalf("deleted non-exact object = %+v", object)
		}
	}
}

func TestVersionedStoreAlreadyAbsentStillRequiresTwoAuthoritativeListings(t *testing.T) {
	calls := 0
	api := &versionedAPIFake{list: func(*s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) {
		calls++
		return &s3.ListObjectVersionsOutput{}, nil
	}}
	evidence, err := (&S3VersionedStore{client: api, bucket: "private"}).DeleteAllVersions(context.Background(), "repairs/absent.png")
	if err != nil || calls != 2 || evidence.StableChecks != 2 || len(api.deleted) != 0 {
		t.Fatalf("evidence=%+v calls=%d deletes=%d err=%v", evidence, calls, len(api.deleted), err)
	}
}

func TestVersionedStoreRejectsAConcurrentVersionAfterFirstEmptyListing(t *testing.T) {
	key := "equipment/photo.png"
	calls := 0
	api := &versionedAPIFake{list: func(*s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) {
		calls++
		if calls == 1 {
			return &s3.ListObjectVersionsOutput{}, nil
		}
		return &s3.ListObjectVersionsOutput{Versions: []types.ObjectVersion{{Key: aws.String(key), VersionId: aws.String("new")}}}, nil
	}}
	_, err := (&S3VersionedStore{client: api, bucket: "private"}).DeleteAllVersions(context.Background(), key)
	if !errors.Is(err, ErrObjectVersionChanged) || len(api.deleted) != 0 {
		t.Fatalf("err=%v deletes=%d", err, len(api.deleted))
	}
}

func TestVersionedStoreRejectsANewVersionAfterDeletingInitialVersions(t *testing.T) {
	key := "equipment/photo.png"
	listCalls := 0
	api := &versionedAPIFake{list: func(*s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) {
		listCalls++
		id := "v1"
		if listCalls > 1 {
			id = "v2"
		}
		return &s3.ListObjectVersionsOutput{Versions: []types.ObjectVersion{{Key: aws.String(key), VersionId: aws.String(id)}}}, nil
	}}
	evidence, err := (&S3VersionedStore{client: api, bucket: "private"}).DeleteAllVersions(context.Background(), key)
	if !errors.Is(err, ErrObjectVersionChanged) || len(api.deleted) != 1 || evidence.DeletedVersions != 1 {
		t.Fatalf("evidence=%+v deletes=%d err=%v", evidence, len(api.deleted), err)
	}
}

func TestVersionedStoreFailsClosedOnListAndPartialDeleteFailuresWithoutLeakingKey(t *testing.T) {
	secretKey := "profiles/secret-subject.png"
	listFailure := errors.New("endpoint included " + secretKey)
	api := &versionedAPIFake{list: func(*s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) { return nil, listFailure }}
	_, err := (&S3VersionedStore{client: api, bucket: "private"}).DeleteAllVersions(context.Background(), secretKey)
	if !errors.Is(err, ErrVersionListing) || !errors.Is(err, listFailure) || err.Error() != ErrVersionListing.Error() {
		t.Fatalf("opaque list error = %q", err)
	}

	api = &versionedAPIFake{
		list: func(*s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) {
			return &s3.ListObjectVersionsOutput{Versions: []types.ObjectVersion{{Key: aws.String(secretKey), VersionId: aws.String("v1")}}}, nil
		},
		delete: func(*s3.DeleteObjectsInput) (*s3.DeleteObjectsOutput, error) {
			return &s3.DeleteObjectsOutput{Errors: []types.Error{{Code: aws.String("AccessDenied")}}}, nil
		},
	}
	evidence, err := (&S3VersionedStore{client: api, bucket: "private"}).DeleteAllVersions(context.Background(), secretKey)
	if !errors.Is(err, ErrVersionDeletion) || err.Error() != ErrVersionDeletion.Error() {
		t.Fatalf("partial delete error = %q", err)
	}
	if evidence.DeletedVersions != 0 || evidence.DeletedMarkers != 0 {
		t.Fatalf("unconfirmed deletion was counted: %+v", evidence)
	}
}

func TestVersionedStoreBatchesDeletesAndReportsConfirmedProgressBeforeFailure(t *testing.T) {
	key := "profiles/many-versions.png"
	versions := make([]types.ObjectVersion, 1001)
	for index := range versions {
		versions[index] = types.ObjectVersion{Key: aws.String(key), VersionId: aws.String(fmt.Sprintf("v-%04d", index))}
	}
	deleteCalls := 0
	api := &versionedAPIFake{
		list: func(*s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) {
			return &s3.ListObjectVersionsOutput{Versions: versions}, nil
		},
		delete: func(input *s3.DeleteObjectsInput) (*s3.DeleteObjectsOutput, error) {
			deleteCalls++
			if deleteCalls == 2 {
				return &s3.DeleteObjectsOutput{Errors: []types.Error{{Code: aws.String("AccessDenied")}}}, nil
			}
			deleted := make([]types.DeletedObject, len(input.Delete.Objects))
			for index, object := range input.Delete.Objects {
				deleted[index] = types.DeletedObject{Key: object.Key, VersionId: object.VersionId}
			}
			return &s3.DeleteObjectsOutput{Deleted: deleted}, nil
		},
	}
	evidence, err := (&S3VersionedStore{client: api, bucket: "private"}).DeleteAllVersions(context.Background(), key)
	if !errors.Is(err, ErrVersionDeletion) || deleteCalls != 2 || evidence.DeletedVersions != 1000 {
		t.Fatalf("evidence=%+v delete_calls=%d err=%v", evidence, deleteCalls, err)
	}
}

func TestVersionedStoreFailsClosedWhenAbsenceCannotStabilise(t *testing.T) {
	key := "repairs/photo.png"
	api := &versionedAPIFake{list: func(*s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) {
		return &s3.ListObjectVersionsOutput{Versions: []types.ObjectVersion{{Key: aws.String(key), VersionId: aws.String("persistent")}}}, nil
	}}
	store := &S3VersionedStore{client: api, bucket: "private", maxPasses: 2, stableChecks: 2}
	if _, err := store.DeleteAllVersions(context.Background(), key); !errors.Is(err, ErrUnstableAbsence) {
		t.Fatalf("err=%v", err)
	}
	if len(api.deleted) != 2 {
		t.Fatalf("delete attempts = %d", len(api.deleted))
	}
}
