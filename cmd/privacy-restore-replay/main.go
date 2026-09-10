package main

import (
	"bytes"
	"context"
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

	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ledgerInputContract  = "mycfc/privacy-restore-ledger-input/v1"
	replayResultContract = "mycfc/privacy-restore-replay-result/v1"
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
	ledgerInput       string
	privateKeyFile    string
	attestationOutput string
}

func parseCommand(args []string) (commandRequest, error) {
	flags := flag.NewFlagSet("privacy-restore-replay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	isolated := flags.Bool("isolated-restore", false, "")
	ledgerInput := flags.String("ledger-input", "", "")
	privateKeyFile := flags.String("private-key-file", "", "")
	attestationOutput := flags.String("attestation-output", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !*isolated || strings.TrimSpace(*ledgerInput) == "" ||
		strings.TrimSpace(*privateKeyFile) == "" || strings.TrimSpace(*attestationOutput) == "" {
		return commandRequest{}, errors.New("usage: privacy-restore-replay --isolated-restore --ledger-input FILE --private-key-file FILE --attestation-output FILE")
	}
	return commandRequest{ledgerInput: *ledgerInput, privateKeyFile: *privateKeyFile, attestationOutput: *attestationOutput}, nil
}

type ledgerInventory struct {
	Contract        string                  `json:"contract"`
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
	Contract              string `json:"contract"`
	Result                string `json:"result"`
	SchemaMigrationDigest string `json:"schema_migration_digest"`
	InventorySHA256       string `json:"inventory_sha256"`
	ObjectCount           int    `json:"object_count"`
	ImportedCount         int    `json:"imported_count"`
	ReplayedCount         int    `json:"replayed_count"`
	AlreadyAppliedCount   int    `json:"already_applied_count"`
	NonReplayableV1Count  int    `json:"non_replayable_v1_count"`
	AbsenceVerifiedCount  int    `json:"absence_verified_count"`
	FailedCount           int    `json:"failed_count"`
}

func run(ctx context.Context, args []string) error {
	request, err := parseCommand(args)
	if err != nil {
		return err
	}
	inventory, err := readLedgerInventory(request.ledgerInput)
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
	engine := databaseReplayEngine{pool: pool, worker: privacyrequests.TombstoneReplayWorker{Store: pool, WorkerRef: uuid.New()}}
	attestation, err := executeReplay(ctx, inventory, privateKey, engine)
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
		len(inventory.Objects) == 0 || len(inventory.Objects) > maxLedgerObjects {
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

func schemaMigrationDigest(orderedVersions []string) string {
	digest := sha256.Sum256([]byte(strings.Join(orderedVersions, "\n")))
	return hex.EncodeToString(digest[:])
}

func executeReplay(ctx context.Context, inventory ledgerInventory, privateKey []byte, engine replayEngine) (replayAttestation, error) {
	attestation := replayAttestation{Contract: replayResultContract, Result: "FAILED", InventorySHA256: inventory.InventorySHA256, ObjectCount: len(inventory.Objects)}
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
	for _, authenticated := range selected {
		result, err := engine.Replay(ctx, authenticated)
		if err != nil {
			return replayAttestation{}, errors.New("database replay failed")
		}
		attestation.ReplayedCount++
		attestation.AbsenceVerifiedCount++
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
		attestation.AbsenceVerifiedCount != attestation.ReplayedCount || attestation.FailedCount != 0 {
		return replayAttestation{}, errors.New("restore replay attestation invariant failed")
	}
	attestation.SchemaMigrationDigest = digest
	attestation.Result = "SUCCEEDED"
	return attestation, nil
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
