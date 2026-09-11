package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func legacyPurgeTestRunner(api versionedS3API) *LegacyMediaPurge {
	return &LegacyMediaPurge{client: api, bucket: "private-media", evidenceKeyID: "legacy-purge-test-v1", evidenceKey: bytes.Repeat([]byte{7}, 32), maxPages: 10, maxEntries: 10000, maxPasses: 6}
}

func legacyPurgeExecutionRequest(runner *LegacyMediaPurge, inventory map[string][]legacyMediaVersion) LegacyMediaPurgeRequest {
	prefixes := make([]LegacyMediaPrefixEvidence, 0, len(legacyMediaPrefixes))
	for _, prefix := range legacyMediaPrefixes {
		entries := append([]legacyMediaVersion(nil), inventory[prefix]...)
		sort.Slice(entries, func(i, j int) bool { return entries[i].identity() < entries[j].identity() })
		evidence := LegacyMediaPrefixEvidence{Prefix: prefix, InventoryDigest: runner.inventoryDigest(prefix, entries)}
		for _, entry := range entries {
			if entry.deleteMarker {
				evidence.DeleteMarkers++
			} else {
				evidence.Versions++
			}
		}
		prefixes = append(prefixes, evidence)
	}
	return LegacyMediaPurgeRequest{Execute: true, Enabled: true, Confirmation: LegacyMediaPurgeConfirmation, ExpectedInventoryDigest: runner.aggregateDigest(prefixes)}
}

func TestLegacyMediaPurgeConstructorDefaultsAndOpaqueFormatting(t *testing.T) {
	client := &s3.Client{}
	key := bytes.Repeat([]byte{0x71}, 32)
	runner, err := NewLegacyMediaPurge(client, "private-media", "legacy-purge-key/v1", key)
	if err != nil || runner.client == nil || runner.bucket != "private-media" || !bytes.Equal(runner.evidenceKey, key) {
		t.Fatalf("constructor=%+v err=%v", runner, err)
	}
	key[0] ^= 0xff
	if bytes.Equal(runner.evidenceKey, key) {
		t.Fatal("constructor retained caller-owned key storage")
	}
	if pages, entries, passes := runner.limits(); pages != legacyMediaDefaultMaxPages || entries != legacyMediaDefaultMaxEntries || passes != legacyMediaDefaultMaxPasses {
		t.Fatalf("default limits=%d/%d/%d", pages, entries, passes)
	}
	for _, invalid := range []struct {
		bucket, keyID string
		key           []byte
	}{{"", "key", bytes.Repeat([]byte{1}, 32)}, {"private", "-bad", bytes.Repeat([]byte{1}, 32)}, {"private", "key", bytes.Repeat([]byte{1}, 31)}} {
		if _, err = NewLegacyMediaPurge(client, invalid.bucket, invalid.keyID, invalid.key); !errors.Is(err, ErrLegacyMediaPurgeConfiguration) {
			t.Fatalf("invalid constructor bucket=%q key=%q err=%v", invalid.bucket, invalid.keyID, err)
		}
	}
	cause := errors.New("provider secret")
	opaque := legacyMediaOpaqueError{kind: ErrLegacyMediaPurgeInventory, cause: cause}
	if !errors.Is(opaque, ErrLegacyMediaPurgeInventory) || !errors.Is(opaque, cause) || fmt.Sprintf("%+v", opaque) != ErrLegacyMediaPurgeInventory.Error() {
		t.Fatalf("opaque error semantics=%v", opaque)
	}
	withoutCause := legacyMediaOpaqueError{kind: ErrLegacyMediaPurgeChanged}
	if !errors.Is(withoutCause, ErrLegacyMediaPurgeChanged) || len(withoutCause.Unwrap()) != 1 {
		t.Fatalf("cause-free opaque error=%v", withoutCause)
	}
}

func TestLegacyMediaPurgeDryRunInventoriesEveryFixedPrefixAcrossPagesWithoutRawKeys(t *testing.T) {
	secretKey := "profiles/member-identifying-name.png"
	api := &versionedAPIFake{list: func(input *s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) {
		prefix := aws.ToString(input.Prefix)
		switch prefix {
		case "equipment/":
			return &s3.ListObjectVersionsOutput{DeleteMarkers: []types.DeleteMarkerEntry{{Key: aws.String("equipment/old.png"), VersionId: aws.String("marker-equipment")}}}, nil
		case "profiles/":
			if input.KeyMarker == nil {
				return &s3.ListObjectVersionsOutput{Versions: []types.ObjectVersion{{Key: aws.String(secretKey), VersionId: aws.String("profile-v1")}}, IsTruncated: aws.Bool(true), NextKeyMarker: aws.String(secretKey), NextVersionIdMarker: aws.String("profile-v1")}, nil
			}
			return &s3.ListObjectVersionsOutput{Versions: []types.ObjectVersion{{Key: aws.String("profiles/second.png"), VersionId: aws.String("profile-v2")}}}, nil
		case "repairs/":
			return &s3.ListObjectVersionsOutput{}, nil
		default:
			t.Fatalf("unexpected prefix %q", prefix)
			return nil, nil
		}
	}}
	evidence, err := legacyPurgeTestRunner(api).Run(context.Background(), LegacyMediaPurgeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Mode != "DRY_RUN" || evidence.Versions != 2 || evidence.DeleteMarkers != 1 || evidence.DeletedVersions != 0 || evidence.DeletedMarkers != 0 || evidence.ListCalls != 4 || len(evidence.Prefixes) != 3 || len(evidence.InventoryDigest) != 64 {
		t.Fatalf("evidence=%+v", evidence)
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secretKey, "profile-v1", "marker-equipment"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatalf("privacy evidence disclosed %q: %s", forbidden, raw)
		}
	}
	if len(api.deleted) != 0 {
		t.Fatalf("dry run issued %d delete calls", len(api.deleted))
	}
}

func TestLegacyMediaPurgeExecutionRequiresBothInternalGateAndExactConfirmation(t *testing.T) {
	calls := 0
	api := &versionedAPIFake{list: func(*s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) {
		calls++
		return &s3.ListObjectVersionsOutput{}, nil
	}}
	runner := legacyPurgeTestRunner(api)
	if _, err := runner.Run(context.Background(), LegacyMediaPurgeRequest{Execute: true, Confirmation: LegacyMediaPurgeConfirmation}); !errors.Is(err, ErrLegacyMediaPurgeDisabled) {
		t.Fatalf("disabled execution error=%v", err)
	}
	if _, err := runner.Run(context.Background(), LegacyMediaPurgeRequest{Execute: true, Enabled: true, Confirmation: "yes"}); !errors.Is(err, ErrLegacyMediaPurgeConfirmation) {
		t.Fatalf("unconfirmed execution error=%v", err)
	}
	if calls != 0 || len(api.deleted) != 0 {
		t.Fatalf("blocked execution touched storage: lists=%d deletes=%d", calls, len(api.deleted))
	}
	if _, err := runner.Run(context.Background(), LegacyMediaPurgeRequest{Execute: true, Enabled: true, Confirmation: LegacyMediaPurgeConfirmation, ExpectedInventoryDigest: strings.Repeat("0", 64)}); !errors.Is(err, ErrLegacyMediaPurgeConfirmation) {
		t.Fatalf("unbound inventory execution error=%v", err)
	}
	if calls != 3 || len(api.deleted) != 0 {
		t.Fatalf("inventory digest mismatch touched deletion: lists=%d deletes=%d", calls, len(api.deleted))
	}
}

func TestLegacyMediaPurgeDeletesExplicitVersionsAndMarkersThenRelistsToStableAbsenceIdempotently(t *testing.T) {
	objects := map[string][]legacyMediaVersion{
		"equipment/": {{key: "equipment/one.png", versionID: "equipment-v1"}},
		"profiles/":  {{key: "profiles/one.png", versionID: "profile-marker", deleteMarker: true}},
		"repairs/":   {{key: "repairs/one.png", versionID: "repair-v1"}},
	}
	api := &versionedAPIFake{}
	api.list = func(input *s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) {
		page := &s3.ListObjectVersionsOutput{}
		for _, entry := range objects[aws.ToString(input.Prefix)] {
			if entry.deleteMarker {
				page.DeleteMarkers = append(page.DeleteMarkers, types.DeleteMarkerEntry{Key: aws.String(entry.key), VersionId: aws.String(entry.versionID)})
			} else {
				page.Versions = append(page.Versions, types.ObjectVersion{Key: aws.String(entry.key), VersionId: aws.String(entry.versionID)})
			}
		}
		return page, nil
	}
	api.delete = func(input *s3.DeleteObjectsInput) (*s3.DeleteObjectsOutput, error) {
		deleted := make([]types.DeletedObject, len(input.Delete.Objects))
		for index, object := range input.Delete.Objects {
			key, version := aws.ToString(object.Key), aws.ToString(object.VersionId)
			prefix := key[:strings.IndexByte(key, '/')+1]
			remaining := objects[prefix][:0]
			for _, entry := range objects[prefix] {
				if entry.key != key || entry.versionID != version {
					remaining = append(remaining, entry)
				}
			}
			objects[prefix] = remaining
			deleted[index] = types.DeletedObject{Key: object.Key, VersionId: object.VersionId}
		}
		return &s3.DeleteObjectsOutput{Deleted: deleted}, nil
	}
	runner := legacyPurgeTestRunner(api)
	request := legacyPurgeExecutionRequest(runner, objects)
	evidence, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Mode != "EXECUTE" || evidence.DeletedVersions != 2 || evidence.DeletedMarkers != 1 || evidence.StableEmptyScans != 6 || evidence.ListCalls != 15 || len(api.deleted) != 3 {
		t.Fatalf("evidence=%+v delete_calls=%d", evidence, len(api.deleted))
	}
	for _, batch := range api.deleted {
		for _, object := range batch {
			if aws.ToString(object.VersionId) == "" {
				t.Fatal("delete omitted an explicit provider version identifier")
			}
		}
	}
	second, err := runner.Run(context.Background(), legacyPurgeExecutionRequest(runner, objects))
	if err != nil || second.Versions != 0 || second.DeleteMarkers != 0 || second.DeletedVersions != 0 || second.DeletedMarkers != 0 || second.StableEmptyScans != 6 || second.ListCalls != 15 || len(api.deleted) != 3 {
		t.Fatalf("idempotent evidence=%+v delete_calls=%d err=%v", second, len(api.deleted), err)
	}
}

func TestLegacyMediaPurgeRejectsConcurrentVersionsAndSafetyBounds(t *testing.T) {
	secret := "equipment/private-name.png"
	lists := 0
	api := &versionedAPIFake{list: func(input *s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) {
		if aws.ToString(input.Prefix) != "equipment/" {
			return &s3.ListObjectVersionsOutput{}, nil
		}
		lists++
		version := "initial"
		if lists > 1 {
			version = "concurrent"
		}
		return &s3.ListObjectVersionsOutput{Versions: []types.ObjectVersion{{Key: aws.String(secret), VersionId: aws.String(version)}}}, nil
	}}
	runner := legacyPurgeTestRunner(api)
	request := legacyPurgeExecutionRequest(runner, map[string][]legacyMediaVersion{"equipment/": {{key: secret, versionID: "initial"}}})
	_, err := runner.Run(context.Background(), request)
	if !errors.Is(err, ErrLegacyMediaPurgeChanged) || strings.Contains(err.Error(), secret) {
		t.Fatalf("concurrent change error=%q", err)
	}

	entries := make([]types.ObjectVersion, 3)
	for index := range entries {
		entries[index] = types.ObjectVersion{Key: aws.String(fmt.Sprintf("equipment/%d.png", index)), VersionId: aws.String(fmt.Sprintf("v%d", index))}
	}
	boundedAPI := &versionedAPIFake{list: func(input *s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) {
		prefix := aws.ToString(input.Prefix)
		for index := range entries {
			entries[index].Key = aws.String(fmt.Sprintf("%s%d.png", prefix, index))
		}
		return &s3.ListObjectVersionsOutput{Versions: entries}, nil
	}}
	bounded := legacyPurgeTestRunner(boundedAPI)
	bounded.maxEntries = 2
	if _, err = bounded.Run(context.Background(), LegacyMediaPurgeRequest{}); !errors.Is(err, ErrLegacyMediaPurgeBound) {
		t.Fatalf("entry bound error=%v", err)
	}
}

func TestLegacyMediaPurgeFinalWholeInventoryRelistCatchesLateChange(t *testing.T) {
	equipmentLists := 0
	api := &versionedAPIFake{list: func(input *s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) {
		if aws.ToString(input.Prefix) != "equipment/" {
			return &s3.ListObjectVersionsOutput{}, nil
		}
		equipmentLists++
		switch equipmentLists {
		case 1:
			return &s3.ListObjectVersionsOutput{Versions: []types.ObjectVersion{{Key: aws.String("equipment/initial.png"), VersionId: aws.String("initial")}}}, nil
		case 2, 3:
			return &s3.ListObjectVersionsOutput{}, nil
		default:
			return &s3.ListObjectVersionsOutput{Versions: []types.ObjectVersion{{Key: aws.String("equipment/late.png"), VersionId: aws.String("late")}}}, nil
		}
	}}
	runner := legacyPurgeTestRunner(api)
	request := legacyPurgeExecutionRequest(runner, map[string][]legacyMediaVersion{"equipment/": {{key: "equipment/initial.png", versionID: "initial"}}})
	_, err := runner.Run(context.Background(), request)
	if !errors.Is(err, ErrLegacyMediaPurgeChanged) || equipmentLists != 4 {
		t.Fatalf("final inventory error=%v equipment_lists=%d", err, equipmentLists)
	}
}

func TestLegacyMediaPurgeBatchesAtProviderLimitWithExplicitVersionIDs(t *testing.T) {
	entries := make([]legacyMediaVersion, 1001)
	for index := range entries {
		entries[index] = legacyMediaVersion{key: fmt.Sprintf("profiles/%04d.png", index), versionID: fmt.Sprintf("version-%04d", index), deleteMarker: index%2 == 0}
	}
	api := &versionedAPIFake{list: func(*s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) {
		return &s3.ListObjectVersionsOutput{}, nil
	}}
	runner := legacyPurgeTestRunner(api)
	evidence := LegacyMediaPrefixEvidence{Prefix: "profiles/"}
	if err := runner.deleteEntries(context.Background(), entries, map[string]struct{}{}, &evidence); err != nil {
		t.Fatal(err)
	}
	if len(api.deleted) != 2 || len(api.deleted[0]) != 1000 || len(api.deleted[1]) != 1 || evidence.DeletedVersions != 500 || evidence.DeletedMarkers != 501 {
		t.Fatalf("batches=%d sizes=%d/%d evidence=%+v", len(api.deleted), len(api.deleted[0]), len(api.deleted[1]), evidence)
	}
	for _, batch := range api.deleted {
		for _, object := range batch {
			if aws.ToString(object.Key) == "" || aws.ToString(object.VersionId) == "" {
				t.Fatal("provider deletion omitted an exact key/version pair")
			}
		}
	}
}

func TestLegacyMediaPurgeErrorsAreOpaqueAndConfigurationIsValidated(t *testing.T) {
	if _, err := (*LegacyMediaPurge)(nil).Run(context.Background(), LegacyMediaPurgeRequest{}); !errors.Is(err, ErrLegacyMediaPurgeConfiguration) {
		t.Fatalf("nil runner error=%v", err)
	}
	secret := "repairs/private-key.png"
	cause := errors.New("provider failed for " + secret)
	api := &versionedAPIFake{list: func(*s3.ListObjectVersionsInput) (*s3.ListObjectVersionsOutput, error) { return nil, cause }}
	_, err := legacyPurgeTestRunner(api).Run(context.Background(), LegacyMediaPurgeRequest{})
	if !errors.Is(err, ErrLegacyMediaPurgeInventory) || !errors.Is(err, cause) || strings.Contains(err.Error(), secret) {
		t.Fatalf("opaque inventory error=%q", err)
	}
}
