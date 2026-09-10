package main

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ledgerInputContract  = "mycfc/privacy-restore-ledger-input/v2"
	replayResultContract = "mycfc/privacy-restore-replay-result/v2"
	maxLedgerInputBytes  = 32 << 20
	maxLedgerObjects     = 1024
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "privacy-restore-replay:", err)
		os.Exit(1)
	}
}

type commandRequest struct {
	ledgerInput           string
	privateKeyFile        string
	attestationOutput     string
	bootstrapSynthetic    bool
	syntheticLedgerOutput string
	policyVersion         string
	executorVersion       string
	planSchemaVersion     string
	imageDigest           string
}

func parseCommand(args []string) (commandRequest, error) {
	flags := flag.NewFlagSet("privacy-restore-replay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	isolated := flags.Bool("isolated-restore", false, "")
	ledgerInput := flags.String("ledger-input", "", "")
	privateKeyFile := flags.String("private-key-file", "", "")
	attestationOutput := flags.String("attestation-output", "", "")
	bootstrapSynthetic := flags.Bool("bootstrap-synthetic-fixture", false, "")
	syntheticLedgerOutput := flags.String("synthetic-ledger-output", "", "")
	policyVersion := flags.String("policy-version", "", "")
	executorVersion := flags.String("executor-version", "", "")
	planSchemaVersion := flags.String("plan-schema-version", "", "")
	imageDigest := flags.String("image-digest", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !*isolated || strings.TrimSpace(*privateKeyFile) == "" {
		return commandRequest{}, errors.New("invalid privacy restore replay invocation")
	}
	if *bootstrapSynthetic {
		if strings.TrimSpace(*syntheticLedgerOutput) == "" || strings.TrimSpace(*ledgerInput) != "" || strings.TrimSpace(*attestationOutput) != "" {
			return commandRequest{}, errors.New("invalid synthetic fixture invocation")
		}
	} else if strings.TrimSpace(*ledgerInput) == "" || strings.TrimSpace(*attestationOutput) == "" || strings.TrimSpace(*syntheticLedgerOutput) != "" ||
		!policyValue(*policyVersion) || !policyValue(*executorVersion) || !policyValue(*planSchemaVersion) || !imageDigestValue(*imageDigest) {
		return commandRequest{}, errors.New("invalid privacy restore replay invocation")
	}
	return commandRequest{ledgerInput: *ledgerInput, privateKeyFile: *privateKeyFile, attestationOutput: *attestationOutput,
		bootstrapSynthetic: *bootstrapSynthetic, syntheticLedgerOutput: *syntheticLedgerOutput, policyVersion: *policyVersion,
		executorVersion: *executorVersion, planSchemaVersion: *planSchemaVersion, imageDigest: *imageDigest}, nil
}

type ledgerInventory struct {
	Contract        string                  `json:"contract"`
	Source          string                  `json:"source"`
	InventorySHA256 string                  `json:"inventory_sha256"`
	Objects         []ledgerInventoryObject `json:"objects"`
}

type ledgerInventoryObject struct {
	KeySHA256        string     `json:"key_sha256"`
	ObjectVersion    string     `json:"object_version"`
	CiphertextSHA256 string     `json:"ciphertext_sha256"`
	SizeBytes        int64      `json:"size_bytes"`
	Payload          []byte     `json:"payload"`
	WrittenAt        time.Time  `json:"written_at"`
	VerifiedAt       time.Time  `json:"verified_at"`
	RetainUntil      *time.Time `json:"retain_until,omitempty"`
}

type replayAttestation struct {
	Contract                        string `json:"contract"`
	Result                          string `json:"result"`
	InputSource                     string `json:"input_source"`
	PolicyVersion                   string `json:"policy_version"`
	ExecutorVersion                 string `json:"executor_version"`
	PlanSchemaVersion               string `json:"plan_schema_version"`
	ImageDigest                     string `json:"image_digest"`
	SchemaMigrationDigest           string `json:"schema_migration_digest"`
	InventorySHA256                 string `json:"inventory_sha256"`
	ObjectCount                     int    `json:"object_count"`
	ImportedCount                   int    `json:"imported_count"`
	ReplayedCount                   int    `json:"replayed_count"`
	AlreadyAppliedCount             int    `json:"already_applied_count"`
	NonReplayableV1Count            int    `json:"non_replayable_v1_count"`
	AbsenceVerifiedCount            int    `json:"absence_verified_count"`
	SyntheticReplayedCount          int    `json:"synthetic_replayed_count"`
	ClosureV3Count                  int    `json:"closure_v3_count"`
	IntentOnlyCount                 int    `json:"intent_only_count"`
	LegacyClosureV2Count            int    `json:"legacy_closure_v2_count"`
	ErasureEffectiveAtVerifiedCount int    `json:"erasure_effective_at_verified_count"`
	FailedCount                     int    `json:"failed_count"`
}

func run(ctx context.Context, args []string) error {
	request, err := parseCommand(args)
	if err != nil {
		return err
	}
	privateKey, err := readPrivateKey(request.privateKeyFile)
	if err != nil {
		return err
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return errors.New("open isolated restore database")
	}
	defer pool.Close()
	if request.bootstrapSynthetic {
		inventory, fixtureErr := createSyntheticFixture(ctx, pool, privateKey)
		if fixtureErr != nil {
			return fixtureErr
		}
		return writeLedgerInventory(request.syntheticLedgerOutput, inventory)
	}
	inventory, err := readLedgerInventory(request.ledgerInput)
	if err != nil {
		return err
	}
	engine := databaseReplayEngine{pool: pool, worker: privacyrequests.TombstoneReplayWorker{Store: pool, WorkerRef: uuid.New()}}
	attestation, err := executeReplay(ctx, inventory, privateKey, engine, replayBindings{PolicyVersion: request.policyVersion,
		ExecutorVersion: request.executorVersion, PlanSchemaVersion: request.planSchemaVersion, ImageDigest: request.imageDigest})
	if err != nil {
		return err
	}
	return writeAttestation(request.attestationOutput, attestation)
}

func readLedgerInventory(path string) (ledgerInventory, error) {
	var zero ledgerInventory
	file, err := os.Open(path)
	if err != nil {
		return zero, errors.New("open ledger input")
	}
	defer file.Close()
	limited := io.LimitReader(file, maxLedgerInputBytes+1)
	encoded, err := io.ReadAll(limited)
	if err != nil || len(encoded) == 0 || len(encoded) > maxLedgerInputBytes {
		return zero, errors.New("read bounded ledger input")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var inventory ledgerInventory
	if err = decoder.Decode(&inventory); err != nil || ensureEOF(decoder) != nil || inventory.Contract != ledgerInputContract ||
		(inventory.Source != "LIVE_LEDGER" && inventory.Source != "SYNTHETIC_BOOTSTRAP") || len(inventory.Objects) == 0 || len(inventory.Objects) > maxLedgerObjects {
		return zero, errors.New("invalid ledger input")
	}
	digest, err := inventoryMetadataDigest(inventory.Objects)
	if err != nil || inventory.InventorySHA256 != digest {
		return zero, errors.New("ledger inventory digest mismatch")
	}
	for _, object := range inventory.Objects {
		checksum, decodeErr := hex.DecodeString(object.CiphertextSHA256)
		actualChecksum := sha256.Sum256(object.Payload)
		if !lowerHexDigest(object.KeySHA256) || !lowerHexDigest(object.CiphertextSHA256) || object.SizeBytes != int64(len(object.Payload)) ||
			object.SizeBytes < 1 || object.SizeBytes > 1<<20 || object.ObjectVersion == "" ||
			object.ObjectVersion != strings.TrimSpace(object.ObjectVersion) || len(object.ObjectVersion) > 1024 ||
			object.WrittenAt.IsZero() || object.VerifiedAt.IsZero() || object.VerifiedAt.Before(object.WrittenAt) || decodeErr != nil ||
			!bytes.Equal(checksum, actualChecksum[:]) {
			return zero, errors.New("invalid ledger object metadata")
		}
	}
	return inventory, nil
}

func inventoryMetadataDigest(objects []ledgerInventoryObject) (string, error) {
	ordered := append([]ledgerInventoryObject(nil), objects...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].KeySHA256 == ordered[j].KeySHA256 {
			return ordered[i].ObjectVersion < ordered[j].ObjectVersion
		}
		return ordered[i].KeySHA256 < ordered[j].KeySHA256
	})
	metadata := make([]map[string]any, 0, len(ordered))
	for index, object := range ordered {
		if index > 0 && object.KeySHA256 == ordered[index-1].KeySHA256 && object.ObjectVersion == ordered[index-1].ObjectVersion {
			return "", errors.New("duplicate ledger object")
		}
		var retainUntil any
		if object.RetainUntil != nil {
			retainUntil = object.RetainUntil.UTC().Format(time.RFC3339Nano)
		}
		metadata = append(metadata, map[string]any{
			"ciphertext_sha256": object.CiphertextSHA256,
			"key_sha256":        object.KeySHA256,
			"object_version":    object.ObjectVersion,
			"retain_until":      retainUntil,
			"size_bytes":        object.SizeBytes,
			"verified_at":       object.VerifiedAt.UTC().Format(time.RFC3339Nano),
			"written_at":        object.WrittenAt.UTC().Format(time.RFC3339Nano),
		})
	}
	canonical, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func readPrivateKey(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 4096 || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("private replay key file must be a bounded owner-only regular file")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read private replay key")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("private replay key must contain one base64-encoded X25519 key")
	}
	return key, nil
}

func lowerHexDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func ensureEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	}
	return errors.New("trailing JSON")
}

type replayEngine interface {
	Replay(context.Context, privacyrequests.AuthenticatedReplayTombstone) (privacyrequests.TombstoneReplayResult, error)
	SchemaMigrationDigest(context.Context) (string, error)
	RecordAttestation(context.Context, replayAttestation, []uuid.UUID) error
}

type databaseReplayEngine struct {
	pool   *pgxpool.Pool
	worker privacyrequests.TombstoneReplayWorker
}

func (e databaseReplayEngine) Replay(ctx context.Context, authenticated privacyrequests.AuthenticatedReplayTombstone) (privacyrequests.TombstoneReplayResult, error) {
	return e.worker.Replay(ctx, authenticated)
}

func (e databaseReplayEngine) SchemaMigrationDigest(ctx context.Context) (string, error) {
	rows, err := e.pool.Query(ctx, `SELECT version FROM mycfc_meta.schema_migrations ORDER BY version`)
	if err != nil {
		return "", errors.New("read restore schema migrations")
	}
	defer rows.Close()
	versions := make([]string, 0, 16)
	for rows.Next() {
		var version string
		if err = rows.Scan(&version); err != nil || version == "" {
			return "", errors.New("read restore schema migrations")
		}
		versions = append(versions, version)
	}
	if rows.Err() != nil || len(versions) == 0 {
		return "", errors.New("read restore schema migrations")
	}
	return schemaMigrationDigest(versions), nil
}

func (e databaseReplayEngine) RecordAttestation(ctx context.Context, attestation replayAttestation, runIDs []uuid.UUID) error {
	digest, err := hex.DecodeString(attestation.InventorySHA256)
	if err != nil {
		return errors.New("record restore replay attestation")
	}
	_, err = dbgen.New(e.pool).RecordPrivacyRestoreReplayInventoryAttestation(ctx, dbgen.RecordPrivacyRestoreReplayInventoryAttestationParams{
		InputSource: attestation.InputSource, InventorySha256: digest, SchemaMigrationDigest: mustDecodeHex(attestation.SchemaMigrationDigest),
		PolicyVersion: attestation.PolicyVersion, ExecutorVersion: attestation.ExecutorVersion, PlanSchemaVersion: attestation.PlanSchemaVersion, ImageDigest: attestation.ImageDigest,
		RunIds: runIDs, ObjectCount: int32(attestation.ObjectCount),
		ImportedCount: int32(attestation.ImportedCount), ReplayedCount: int32(attestation.ReplayedCount), AlreadyAppliedCount: int32(attestation.AlreadyAppliedCount),
		AbsenceVerifiedCount: int32(attestation.AbsenceVerifiedCount), SyntheticReplayedCount: int32(attestation.SyntheticReplayedCount),
		ClosureV3Count: int32(attestation.ClosureV3Count), IntentOnlyCount: int32(attestation.IntentOnlyCount), LegacyClosureV2Count: int32(attestation.LegacyClosureV2Count),
		ErasureEffectiveAtVerifiedCount: int32(attestation.ErasureEffectiveAtVerifiedCount),
	})
	if err != nil {
		return errors.New("record restore replay attestation")
	}
	return nil
}

func schemaMigrationDigest(orderedVersions []string) string {
	digest := sha256.Sum256([]byte(strings.Join(orderedVersions, "\n")))
	return hex.EncodeToString(digest[:])
}

type replayBindings struct{ PolicyVersion, ExecutorVersion, PlanSchemaVersion, ImageDigest string }

func executeReplay(ctx context.Context, inventory ledgerInventory, privateKey []byte, engine replayEngine, bindings replayBindings) (replayAttestation, error) {
	if !policyValue(bindings.PolicyVersion) || !policyValue(bindings.ExecutorVersion) || !policyValue(bindings.PlanSchemaVersion) || !imageDigestValue(bindings.ImageDigest) {
		return replayAttestation{}, errors.New("invalid restore replay bindings")
	}
	attestation := replayAttestation{Contract: replayResultContract, Result: "FAILED", InputSource: inventory.Source, InventorySHA256: inventory.InventorySHA256, ObjectCount: len(inventory.Objects),
		PolicyVersion: bindings.PolicyVersion, ExecutorVersion: bindings.ExecutorVersion, PlanSchemaVersion: bindings.PlanSchemaVersion, ImageDigest: bindings.ImageDigest}
	selected := make(map[string]privacyrequests.AuthenticatedReplayTombstone)
	for _, object := range inventory.Objects {
		checksum, _ := hex.DecodeString(object.CiphertextSHA256)
		var retainUntil time.Time
		if object.RetainUntil != nil {
			retainUntil = object.RetainUntil.UTC()
		}
		// V2 kind and locator fields are deliberately derived from the envelope:
		// openTombstoneEnvelopeV2 binds and cross-checks all three in AEAD AAD,
		// avoiding an unauthenticated duplicate in the inventory contract.
		listed := privacyrequests.ListedTombstoneObject{
			Payload: object.Payload, Checksum: checksum, ObjectVersion: object.ObjectVersion,
			WrittenAt: object.WrittenAt, VerifiedAt: object.VerifiedAt, RetainUntil: retainUntil,
		}
		authenticated, err := privacyrequests.AuthenticateReplayTombstone(privateKey, listed)
		if err != nil {
			if privacyrequests.IsLegacyTombstoneEnvelope(object.Payload) {
				attestation.NonReplayableV1Count++
				continue
			}
			return replayAttestation{}, errors.New("ledger object authentication failed")
		}
		if (inventory.Source == "SYNTHETIC_BOOTSTRAP") != authenticated.IsSynthetic() {
			return replayAttestation{}, errors.New("ledger source binding failed")
		}
		key := authenticated.OpaqueReplayID()
		if existing, ok := selected[key]; ok {
			if !existing.SameReplay(authenticated) {
				return replayAttestation{}, errors.New("conflicting ledger objects")
			}
			if existing.IsClosure() && !authenticated.IsClosure() {
				continue
			}
			if existing.IsClosure() == authenticated.IsClosure() {
				return replayAttestation{}, errors.New("conflicting ledger objects")
			}
		}
		selected[key] = authenticated
	}
	if len(selected) == 0 {
		return replayAttestation{}, errors.New("no replayable v2 ledger objects")
	}
	runIDs := make([]uuid.UUID, 0, len(selected))
	for _, authenticated := range selected {
		result, err := engine.Replay(ctx, authenticated)
		if err != nil {
			return replayAttestation{}, errors.New("database replay failed")
		}
		attestation.ReplayedCount++
		attestation.AbsenceVerifiedCount++
		runIDs = append(runIDs, result.RunID)
		if result.Synthetic {
			attestation.SyntheticReplayedCount++
		}
		switch result.ClosureVersion {
		case privacyrequests.TombstoneClosureVersion:
			attestation.ClosureV3Count++
		case privacyrequests.TombstoneClosureVersionV2:
			attestation.LegacyClosureV2Count++
		default:
			attestation.IntentOnlyCount++
		}
		attestation.ErasureEffectiveAtVerifiedCount++
		if result.AlreadyApplied {
			attestation.AlreadyAppliedCount++
		} else {
			attestation.ImportedCount++
		}
	}
	digest, err := engine.SchemaMigrationDigest(ctx)
	if err != nil || !lowerHexDigest(digest) {
		return replayAttestation{}, errors.New("restore schema attestation failed")
	}
	if attestation.ReplayedCount != attestation.ImportedCount+attestation.AlreadyAppliedCount ||
		attestation.AbsenceVerifiedCount != attestation.ReplayedCount || attestation.FailedCount != 0 ||
		(inventory.Source == "LIVE_LEDGER" && attestation.SyntheticReplayedCount != 0) ||
		(inventory.Source == "SYNTHETIC_BOOTSTRAP" && attestation.SyntheticReplayedCount != attestation.ReplayedCount) ||
		attestation.ClosureV3Count != attestation.ReplayedCount || attestation.IntentOnlyCount != 0 || attestation.LegacyClosureV2Count != 0 ||
		attestation.ErasureEffectiveAtVerifiedCount != attestation.ReplayedCount {
		return replayAttestation{}, errors.New("restore replay attestation invariant failed")
	}
	attestation.SchemaMigrationDigest = digest
	if err = engine.RecordAttestation(ctx, attestation, runIDs); err != nil {
		return replayAttestation{}, errors.New("restore replay attestation persistence failed")
	}
	attestation.Result = "SUCCEEDED"
	return attestation, nil
}

func policyValue(value string) bool {
	if len(value) < 1 || len(value) > 80 {
		return false
	}
	for index, r := range value {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || index > 0 && strings.ContainsRune("_.:/-", r)) {
			return false
		}
	}
	return true
}
func imageDigestValue(value string) bool {
	return strings.HasPrefix(value, "sha256:") && lowerHexDigest(strings.TrimPrefix(value, "sha256:"))
}
func mustDecodeHex(value string) []byte { decoded, _ := hex.DecodeString(value); return decoded }

func createSyntheticFixture(ctx context.Context, pool *pgxpool.Pool, privateKey []byte) (ledgerInventory, error) {
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return ledgerInventory{}, errors.New("open synthetic fixture transaction")
	}
	defer connection.Release()
	if _, err = connection.Exec(ctx, `SELECT set_config('mycfc.privacy_restore_isolated','on',false)`); err != nil {
		return ledgerInventory{}, errors.New("enable isolated fixture mode")
	}
	workerRef := uuid.New()
	fixture, err := dbgen.New(connection).CreatePrivacyRestoreSyntheticFixture(ctx, workerRef)
	if err != nil {
		return ledgerInventory{}, errors.New("create synthetic restore fixture")
	}
	key, err := ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil {
		return ledgerInventory{}, errors.New("open synthetic replay key")
	}
	locatorKey := make([]byte, sha256.Size)
	if _, err = io.ReadFull(rand.Reader, locatorKey); err != nil {
		return ledgerInventory{}, errors.New("create synthetic locator")
	}
	protector, err := privacyrequests.NewTombstoneProtector("synthetic-replay-key-v1", key.PublicKey().Bytes(), "synthetic-locator-key-v1", locatorKey)
	if err != nil {
		return ledgerInventory{}, errors.New("create synthetic protector")
	}
	record := privacyrequests.RestoreTombstone{Version: privacyrequests.TombstoneRecordVersion, ExecutionID: fixture.FixtureSourceExecutionID,
		RequestID: fixture.FixtureSourceRequestID, RequestRef: fixture.FixtureSourceRequestRef, SubjectUserID: fixture.FixtureSubjectUserID,
		PlanSHA256: fixture.FixturePlanSha256, WorksetSHA256: fixture.FixtureWorksetSha256, ExecutionStart: fixture.FixtureErasureEffectiveAt.Time,
		SyntheticFixture: privacyrequests.SyntheticRestoreFixtureV1, Replay: &privacyrequests.RelationalReplayPrescription{Version: privacyrequests.TombstoneReplayVersion, ActionVersion: privacyrequests.SupportedActionVersion, Operations: fixture.FixtureOperations}}
	closedAt := time.Now().UTC()
	sealed, err := protector.SealClosure(privacyrequests.TombstoneClosure{Version: privacyrequests.TombstoneClosureVersion, Tombstone: record,
		ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0), ErasureEffectiveAt: fixture.FixtureErasureEffectiveAt.Time})
	if err != nil {
		return ledgerInventory{}, errors.New("seal synthetic restore fixture")
	}
	writtenAt := time.Now().UTC()
	keyDigest := sha256.Sum256(append([]byte("mycfc/synthetic-ledger-key/v1\x00"), sealed.Locator...))
	retainUntil := sealed.RetainUntil.UTC()
	object := ledgerInventoryObject{KeySHA256: hex.EncodeToString(keyDigest[:]), ObjectVersion: "synthetic-local-v1", CiphertextSHA256: hex.EncodeToString(sealed.SHA256), SizeBytes: int64(len(sealed.Encoded)), Payload: sealed.Encoded, WrittenAt: writtenAt, VerifiedAt: writtenAt, RetainUntil: &retainUntil}
	digest, err := inventoryMetadataDigest([]ledgerInventoryObject{object})
	if err != nil {
		return ledgerInventory{}, errors.New("digest synthetic restore fixture")
	}
	return ledgerInventory{Contract: ledgerInputContract, Source: "SYNTHETIC_BOOTSTRAP", InventorySHA256: digest, Objects: []ledgerInventoryObject{object}}, nil
}

func writeLedgerInventory(path string, inventory ledgerInventory) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("create synthetic ledger output")
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(inventory); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return errors.New("write synthetic ledger output")
	}
	if err = file.Close(); err != nil {
		return errors.New("close synthetic ledger output")
	}
	return nil
}

func writeAttestation(path string, attestation replayAttestation) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("create replay attestation")
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(attestation); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return errors.New("write replay attestation")
	}
	if err = file.Close(); err != nil {
		return errors.New("close replay attestation")
	}
	return nil
}
