package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var (
	ErrLegacyMediaPurgeConfiguration = errors.New("legacy media purge configuration invalid")
	ErrLegacyMediaPurgeDisabled      = errors.New("legacy media purge execution disabled")
	ErrLegacyMediaPurgeConfirmation  = errors.New("legacy media purge confirmation invalid")
	ErrLegacyMediaPurgeInventory     = errors.New("legacy media inventory failed")
	ErrLegacyMediaPurgeDeletion      = errors.New("legacy media deletion failed")
	ErrLegacyMediaPurgeChanged       = errors.New("legacy media changed during purge")
	ErrLegacyMediaPurgeBound         = errors.New("legacy media purge safety bound reached")
)

const (
	LegacyMediaPurgeConfirmation = "DELETE-ALL-LEGACY-MEDIA-VERSIONS"
	legacyMediaEvidenceVersion   = "mycfc/legacy-media-purge-evidence/v1"
	legacyMediaPageSize          = int32(1000)
	legacyMediaDefaultMaxPages   = 1000
	legacyMediaDefaultMaxEntries = 250000
	legacyMediaDefaultMaxPasses  = 10
	legacyMediaStableChecks      = 2
)

var legacyMediaPrefixes = [...]string{"profiles/", "repairs/", "equipment/"}

type LegacyMediaPurgeRequest struct {
	Execute                 bool
	Enabled                 bool
	Confirmation            string
	ExpectedInventoryDigest string
}

type LegacyMediaPrefixEvidence struct {
	Prefix           string `json:"prefix"`
	Versions         int    `json:"versions"`
	DeleteMarkers    int    `json:"delete_markers"`
	DeletedVersions  int    `json:"deleted_versions"`
	DeletedMarkers   int    `json:"deleted_markers"`
	ListCalls        int    `json:"list_calls"`
	StableEmptyScans int    `json:"stable_empty_scans"`
	InventoryDigest  string `json:"inventory_digest"`
}

// LegacyMediaPurgeEvidence is deliberately safe to retain in an operational
// record: it contains fixed prefixes, counts and keyed digests, never object
// keys or provider version identifiers.
type LegacyMediaPurgeEvidence struct {
	EvidenceVersion  string                      `json:"evidence_version"`
	Mode             string                      `json:"mode"`
	EvidenceKeyID    string                      `json:"evidence_key_id"`
	Prefixes         []LegacyMediaPrefixEvidence `json:"prefixes"`
	Versions         int                         `json:"versions"`
	DeleteMarkers    int                         `json:"delete_markers"`
	DeletedVersions  int                         `json:"deleted_versions"`
	DeletedMarkers   int                         `json:"deleted_markers"`
	ListCalls        int                         `json:"list_calls"`
	StableEmptyScans int                         `json:"stable_empty_scans"`
	InventoryDigest  string                      `json:"inventory_digest"`
}

type LegacyMediaPurge struct {
	client        versionedS3API
	bucket        string
	evidenceKey   []byte
	evidenceKeyID string
	maxPages      int
	maxEntries    int
	maxPasses     int
}

func NewLegacyMediaPurge(client *s3.Client, bucket, evidenceKeyID string, evidenceKey []byte) (*LegacyMediaPurge, error) {
	runner := &LegacyMediaPurge{client: client, bucket: bucket, evidenceKeyID: evidenceKeyID, evidenceKey: append([]byte(nil), evidenceKey...)}
	if !runner.valid() {
		return nil, ErrLegacyMediaPurgeConfiguration
	}
	return runner, nil
}

func (p *LegacyMediaPurge) valid() bool {
	return p != nil && p.client != nil && strings.TrimSpace(p.bucket) != "" && len(p.bucket) <= 63 &&
		validOperationalKeyID(p.evidenceKeyID) && len(p.evidenceKey) >= 32
}

func validOperationalKeyID(value string) bool {
	if value == "" || len(value) > 80 || value != strings.TrimSpace(value) {
		return false
	}
	for index, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			(index > 0 && strings.ContainsRune("_.:/-", character)) {
			continue
		}
		return false
	}
	return true
}

type legacyMediaVersion struct {
	key          string
	versionID    string
	deleteMarker bool
}

func (v legacyMediaVersion) identity() string {
	marker := byte(0)
	if v.deleteMarker {
		marker = 1
	}
	return v.key + "\x00" + v.versionID + string(marker)
}

func (v legacyMediaVersion) providerIdentity() string {
	return v.key + "\x00" + v.versionID
}

func (p *LegacyMediaPurge) limits() (maxPages, maxEntries, maxPasses int) {
	maxPages, maxEntries, maxPasses = p.maxPages, p.maxEntries, p.maxPasses
	if maxPages < 1 {
		maxPages = legacyMediaDefaultMaxPages
	}
	if maxEntries < 1 {
		maxEntries = legacyMediaDefaultMaxEntries
	}
	if maxPasses < legacyMediaStableChecks {
		maxPasses = legacyMediaDefaultMaxPasses
	}
	return
}

func (p *LegacyMediaPurge) Run(ctx context.Context, request LegacyMediaPurgeRequest) (LegacyMediaPurgeEvidence, error) {
	evidence := LegacyMediaPurgeEvidence{EvidenceVersion: legacyMediaEvidenceVersion, Mode: "DRY_RUN"}
	if p != nil {
		evidence.EvidenceKeyID = p.evidenceKeyID
	}
	if !p.valid() {
		return evidence, ErrLegacyMediaPurgeConfiguration
	}
	if request.Execute {
		evidence.Mode = "EXECUTE"
		if !request.Enabled {
			return evidence, ErrLegacyMediaPurgeDisabled
		}
		if request.Confirmation != LegacyMediaPurgeConfirmation {
			return evidence, ErrLegacyMediaPurgeConfirmation
		}
	}

	inventories := make([][]legacyMediaVersion, len(legacyMediaPrefixes))
	for index, prefix := range legacyMediaPrefixes {
		entries, calls, err := p.listPrefix(ctx, prefix)
		if err != nil {
			return evidence, err
		}
		inventories[index] = entries
		prefixEvidence := LegacyMediaPrefixEvidence{Prefix: prefix, ListCalls: calls, InventoryDigest: p.inventoryDigest(prefix, entries)}
		for _, entry := range entries {
			if entry.deleteMarker {
				prefixEvidence.DeleteMarkers++
			} else {
				prefixEvidence.Versions++
			}
		}
		evidence.Prefixes = append(evidence.Prefixes, prefixEvidence)
	}
	p.recalculate(&evidence)
	evidence.InventoryDigest = p.aggregateDigest(evidence.Prefixes)
	if !request.Execute {
		return evidence, nil
	}
	expectedDigest, expectedErr := hex.DecodeString(request.ExpectedInventoryDigest)
	actualDigest, actualErr := hex.DecodeString(evidence.InventoryDigest)
	if expectedErr != nil || actualErr != nil || len(expectedDigest) != sha256.Size || !hmac.Equal(expectedDigest, actualDigest) {
		return evidence, ErrLegacyMediaPurgeConfirmation
	}

	for index := range evidence.Prefixes {
		if err := p.purgePrefix(ctx, inventories[index], &evidence.Prefixes[index]); err != nil {
			p.recalculate(&evidence)
			return evidence, err
		}
	}
	if err := p.verifyFinalAbsence(ctx, evidence.Prefixes); err != nil {
		p.recalculate(&evidence)
		return evidence, err
	}
	p.recalculate(&evidence)
	return evidence, nil
}

func (p *LegacyMediaPurge) listPrefix(ctx context.Context, prefix string) ([]legacyMediaVersion, int, error) {
	maxPages, maxEntries, _ := p.limits()
	paginator := s3.NewListObjectVersionsPaginator(p.client, &s3.ListObjectVersionsInput{
		Bucket: aws.String(p.bucket), Prefix: aws.String(prefix), MaxKeys: aws.Int32(legacyMediaPageSize),
	}, func(options *s3.ListObjectVersionsPaginatorOptions) { options.StopOnDuplicateToken = true })
	entries := make([]legacyMediaVersion, 0)
	seen := make(map[string]struct{})
	listCalls := 0
	for paginator.HasMorePages() {
		if listCalls >= maxPages {
			return nil, listCalls, ErrLegacyMediaPurgeBound
		}
		page, err := paginator.NextPage(ctx)
		listCalls++
		if err != nil || page == nil {
			return nil, listCalls, legacyMediaOpaqueError{kind: ErrLegacyMediaPurgeInventory, cause: err}
		}
		appendEntry := func(key, versionID string, marker bool) error {
			if !strings.HasPrefix(key, prefix) || key == prefix || versionID == "" || strings.ContainsRune(key, 0) || strings.ContainsRune(versionID, 0) {
				return ErrLegacyMediaPurgeInventory
			}
			entry := legacyMediaVersion{key: key, versionID: versionID, deleteMarker: marker}
			identity := entry.providerIdentity()
			if _, duplicate := seen[identity]; duplicate {
				return ErrLegacyMediaPurgeInventory
			}
			seen[identity] = struct{}{}
			entries = append(entries, entry)
			if len(entries) > maxEntries {
				return ErrLegacyMediaPurgeBound
			}
			return nil
		}
		for _, version := range page.Versions {
			if err = appendEntry(aws.ToString(version.Key), aws.ToString(version.VersionId), false); err != nil {
				return nil, listCalls, err
			}
		}
		for _, marker := range page.DeleteMarkers {
			if err = appendEntry(aws.ToString(marker.Key), aws.ToString(marker.VersionId), true); err != nil {
				return nil, listCalls, err
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].identity() < entries[j].identity() })
	return entries, listCalls, nil
}

func (p *LegacyMediaPurge) purgePrefix(ctx context.Context, initial []legacyMediaVersion, evidence *LegacyMediaPrefixEvidence) error {
	known := make(map[string]struct{}, len(initial))
	confirmed := make(map[string]struct{}, len(initial))
	for _, entry := range initial {
		known[entry.identity()] = struct{}{}
	}
	if err := p.deleteEntries(ctx, initial, confirmed, evidence); err != nil {
		return err
	}
	_, _, maxPasses := p.limits()
	stable := 0
	for pass := 0; pass < maxPasses; pass++ {
		current, calls, err := p.listPrefix(ctx, evidence.Prefix)
		evidence.ListCalls += calls
		if err != nil {
			return err
		}
		if len(current) == 0 {
			stable++
			evidence.StableEmptyScans = stable
			if stable == legacyMediaStableChecks {
				return nil
			}
			continue
		}
		if stable > 0 {
			return ErrLegacyMediaPurgeChanged
		}
		for _, entry := range current {
			if _, ok := known[entry.identity()]; !ok {
				return ErrLegacyMediaPurgeChanged
			}
		}
		if err = p.deleteEntries(ctx, current, confirmed, evidence); err != nil {
			return err
		}
	}
	return ErrLegacyMediaPurgeBound
}

// verifyFinalAbsence repeats complete scans across the whole fixed inventory.
// Per-prefix success alone is insufficient because an earlier prefix could
// change while a later prefix is being processed.
func (p *LegacyMediaPurge) verifyFinalAbsence(ctx context.Context, evidence []LegacyMediaPrefixEvidence) error {
	for scan := 1; scan <= legacyMediaStableChecks; scan++ {
		for index := range evidence {
			entries, calls, err := p.listPrefix(ctx, evidence[index].Prefix)
			evidence[index].ListCalls += calls
			if err != nil {
				return err
			}
			if len(entries) != 0 {
				return ErrLegacyMediaPurgeChanged
			}
			evidence[index].StableEmptyScans = scan
		}
	}
	return nil
}

func (p *LegacyMediaPurge) deleteEntries(ctx context.Context, entries []legacyMediaVersion, confirmed map[string]struct{}, evidence *LegacyMediaPrefixEvidence) error {
	for offset := 0; offset < len(entries); offset += 1000 {
		end := min(offset+1000, len(entries))
		objects := make([]types.ObjectIdentifier, end-offset)
		expected := make(map[string]legacyMediaVersion, end-offset)
		for index, entry := range entries[offset:end] {
			objects[index] = types.ObjectIdentifier{Key: aws.String(entry.key), VersionId: aws.String(entry.versionID)}
			expected[entry.key+"\x00"+entry.versionID] = entry
		}
		result, err := p.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: aws.String(p.bucket), Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(false)}})
		if err != nil || result == nil {
			return legacyMediaOpaqueError{kind: ErrLegacyMediaPurgeDeletion, cause: err}
		}
		seen := make(map[string]struct{}, len(result.Deleted))
		for _, deleted := range result.Deleted {
			identity := aws.ToString(deleted.Key) + "\x00" + aws.ToString(deleted.VersionId)
			entry, ok := expected[identity]
			if !ok {
				return ErrLegacyMediaPurgeDeletion
			}
			if _, duplicate := seen[identity]; duplicate {
				return ErrLegacyMediaPurgeDeletion
			}
			seen[identity] = struct{}{}
			if _, already := confirmed[entry.identity()]; !already {
				confirmed[entry.identity()] = struct{}{}
				if entry.deleteMarker {
					evidence.DeletedMarkers++
				} else {
					evidence.DeletedVersions++
				}
			}
		}
		if len(result.Errors) != 0 || len(seen) != len(expected) {
			return ErrLegacyMediaPurgeDeletion
		}
	}
	return nil
}

func (p *LegacyMediaPurge) inventoryDigest(prefix string, entries []legacyMediaVersion) string {
	mac := hmac.New(sha256.New, p.evidenceKey)
	writeLegacyEvidenceFields(mac, legacyMediaEvidenceVersion, p.evidenceKeyID, prefix)
	for _, entry := range entries {
		kind := "VERSION"
		if entry.deleteMarker {
			kind = "DELETE_MARKER"
		}
		writeLegacyEvidenceFields(mac, kind, entry.key, entry.versionID)
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func (p *LegacyMediaPurge) aggregateDigest(prefixes []LegacyMediaPrefixEvidence) string {
	mac := hmac.New(sha256.New, p.evidenceKey)
	writeLegacyEvidenceFields(mac, legacyMediaEvidenceVersion, p.evidenceKeyID, "AGGREGATE")
	for _, prefix := range prefixes {
		writeLegacyEvidenceFields(mac, prefix.Prefix, fmt.Sprintf("%d", prefix.Versions), fmt.Sprintf("%d", prefix.DeleteMarkers), prefix.InventoryDigest)
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func writeLegacyEvidenceFields(mac interface{ Write([]byte) (int, error) }, fields ...string) {
	var size [4]byte
	for _, field := range fields {
		binary.BigEndian.PutUint32(size[:], uint32(len(field)))
		_, _ = mac.Write(size[:])
		_, _ = mac.Write([]byte(field))
	}
}

func (p *LegacyMediaPurge) recalculate(evidence *LegacyMediaPurgeEvidence) {
	evidence.Versions, evidence.DeleteMarkers, evidence.DeletedVersions, evidence.DeletedMarkers, evidence.ListCalls, evidence.StableEmptyScans = 0, 0, 0, 0, 0, 0
	for _, prefix := range evidence.Prefixes {
		evidence.Versions += prefix.Versions
		evidence.DeleteMarkers += prefix.DeleteMarkers
		evidence.DeletedVersions += prefix.DeletedVersions
		evidence.DeletedMarkers += prefix.DeletedMarkers
		evidence.ListCalls += prefix.ListCalls
		evidence.StableEmptyScans += prefix.StableEmptyScans
	}
}

type legacyMediaOpaqueError struct {
	kind  error
	cause error
}

func (e legacyMediaOpaqueError) Error() string { return e.kind.Error() }
func (e legacyMediaOpaqueError) Unwrap() []error {
	if e.cause == nil {
		return []error{e.kind}
	}
	return []error{e.kind, e.cause}
}
func (e legacyMediaOpaqueError) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte(e.Error()))
}
