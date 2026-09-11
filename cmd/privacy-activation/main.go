package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maximumArtifactBytes = 1 << 20

type activationInputs struct {
	databaseURL string
	actor       uuid.UUID
	restoreKey  []byte
	trustedKeys map[string]ed25519.PublicKey
	release     privacyrequests.ActivationReleaseBinding
	restore     []byte
	artifacts   [][]byte
}

type activationEvidenceStore interface {
	activationEvidenceWriter
	Close()
}

type activationEvidenceWriter interface {
	VerifyAndRecordRestoreActivationEvidence(context.Context, uuid.UUID, []byte, []byte, privacyrequests.ActivationReleaseBinding, time.Time) (privacyrequests.ActivationEvidence, error)
	VerifyAndRecordActivationArtifact(context.Context, uuid.UUID, []byte, map[string]ed25519.PublicKey, privacyrequests.ActivationReleaseBinding, time.Time) (privacyrequests.ActivationEvidence, error)
}

type postgresActivationEvidenceStore struct {
	close  func()
	writer activationEvidenceWriter
}

func (s postgresActivationEvidenceStore) VerifyAndRecordRestoreActivationEvidence(ctx context.Context, actor uuid.UUID, payload, key []byte,
	release privacyrequests.ActivationReleaseBinding, now time.Time) (privacyrequests.ActivationEvidence, error) {
	return s.writer.VerifyAndRecordRestoreActivationEvidence(ctx, actor, payload, key, release, now)
}

func (s postgresActivationEvidenceStore) VerifyAndRecordActivationArtifact(ctx context.Context, actor uuid.UUID, payload []byte,
	trustedKeys map[string]ed25519.PublicKey, release privacyrequests.ActivationReleaseBinding, now time.Time) (privacyrequests.ActivationEvidence, error) {
	return s.writer.VerifyAndRecordActivationArtifact(ctx, actor, payload, trustedKeys, release, now)
}

func (s postgresActivationEvidenceStore) Close() { s.close() }

var verifyRestoreActivationAttestation = privacyrequests.VerifyRestoreActivationAttestation
var verifyActivationArtifact = privacyrequests.VerifyActivationArtifact
var exitProcess = os.Exit
var openActivationEvidenceStore = func(ctx context.Context, databaseURL string) (activationEvidenceStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	return postgresActivationEvidenceStore{close: pool.Close, writer: privacyrequests.Service{Pool: pool}}, nil
}

func main() {
	mode := "record-evidence"
	if len(os.Args) == 2 {
		mode = os.Args[1]
	} else if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "privacy_activation_usage_rejected")
		exitProcess(2)
		return
	}
	var err error
	switch mode {
	case "record-evidence":
		err = run(context.Background(), os.Getenv, os.Stdout)
	case "prepare-approvals":
		err = runPrepare(context.Background(), os.Getenv, os.Stdout)
	case "sign-approval":
		err = runSign(os.Getenv)
	case "activate":
		err = runActivate(context.Background(), os.Getenv, os.Stdout)
	case "disable":
		err = runDisable(context.Background(), os.Getenv, os.Stdout)
	default:
		err = errors.New("privacy activation mode rejected")
	}
	if err != nil {
		if mode == "disable" {
			fmt.Fprintln(os.Stderr, "privacy_activation_disable_failed")
		} else {
			fmt.Fprintln(os.Stderr, "privacy_activation_evidence_failed")
		}
		exitProcess(1)
	}
}

func run(ctx context.Context, getenv func(string) string, output io.Writer) error {
	now := time.Now().UTC()
	inputs, err := loadActivationInputs(getenv)
	if err != nil {
		return err
	}
	// Use the package verifier as the sole authority and validate the complete
	// four-artifact set before the first evidence row can be recorded. The
	// service wrappers repeat the same verification at the write boundary.
	if _, err = verifyRestoreActivationAttestation(inputs.restore, inputs.restoreKey, inputs.release, now); err != nil {
		return errors.New("restore activation evidence rejected")
	}
	for _, payload := range inputs.artifacts {
		if _, err = verifyActivationArtifact(payload, inputs.trustedKeys, inputs.release, now); err != nil {
			return errors.New("signed activation evidence rejected")
		}
	}

	store, err := openActivationEvidenceStore(ctx, inputs.databaseURL)
	if err != nil {
		return errors.New("open privacy activation database")
	}
	defer store.Close()
	if _, err = store.VerifyAndRecordRestoreActivationEvidence(ctx, inputs.actor, inputs.restore, inputs.restoreKey, inputs.release, now); err != nil {
		return errors.New("record restore activation evidence")
	}
	seen := make(map[string]bool, len(inputs.artifacts))
	for _, payload := range inputs.artifacts {
		evidence, recordErr := store.VerifyAndRecordActivationArtifact(ctx, inputs.actor, payload, inputs.trustedKeys, inputs.release, now)
		if recordErr != nil {
			return errors.New("record signed activation evidence")
		}
		seen[evidence.Kind] = true
	}
	if len(seen) != 3 || !seen["INFRASTRUCTURE"] || !seen["PROVIDER"] || !seen["SCHEMA"] {
		return errors.New("signed activation evidence set rejected")
	}
	fmt.Fprintln(output, "privacy_activation_evidence_recorded restore=1 infrastructure=1 provider=1 schema=1")
	return nil
}

func loadActivationInputs(getenv func(string) string) (activationInputs, error) {
	var inputs activationInputs
	if getenv("PRIVACY_ACTIVATION_EVIDENCE_ENABLED") != "true" {
		return inputs, errors.New("privacy activation evidence command is disabled")
	}
	inputs.databaseURL = strings.TrimSpace(getenv("PRIVACY_ACTIVATION_BROKER_DATABASE_URL"))
	var err error
	inputs.actor, err = uuid.Parse(strings.TrimSpace(getenv("PRIVACY_ACTIVATION_ACTOR_REF")))
	if inputs.databaseURL == "" || err != nil || inputs.actor == uuid.Nil {
		return activationInputs{}, errors.New("privacy activation operator configuration rejected")
	}
	inputs.restoreKey, err = readHexKey(getenv("PRIVACY_ACTIVATION_RESTORE_AUTH_KEY_FILE"))
	if err != nil {
		return activationInputs{}, err
	}
	artifactPublicKey, err := readBase64Key(getenv("PRIVACY_ACTIVATION_ARTIFACT_PUBLIC_KEY_FILE"))
	keyID := strings.TrimSpace(getenv("PRIVACY_ACTIVATION_ARTIFACT_SIGNING_KEY_ID"))
	if err != nil || len(artifactPublicKey) != ed25519.PublicKeySize || keyID == "" {
		return activationInputs{}, errors.New("privacy activation artifact trust root rejected")
	}
	inputs.trustedKeys = map[string]ed25519.PublicKey{keyID: ed25519.PublicKey(artifactPublicKey)}
	inputs.release = privacyrequests.ActivationReleaseBinding{
		PolicyVersion:         strings.TrimSpace(getenv("PRIVACY_ACTIVATION_POLICY_VERSION")),
		ExecutorVersion:       privacyrequests.SupportedExecutorVersion,
		PlanSchemaVersion:     privacyrequests.SupportedPlanSchemaVersion,
		ImageDigest:           strings.TrimSpace(getenv("PRIVACY_ACTIVATION_CURRENT_IMAGE_DIGEST")),
		SchemaMigrationDigest: db.EmbeddedMigrationDigest(),
	}
	inputs.restore, err = readArtifact(getenv("PRIVACY_ACTIVATION_RESTORE_ATTESTATION_FILE"))
	if err != nil {
		return activationInputs{}, err
	}
	for _, artifact := range []struct{ env, contract string }{
		{"PRIVACY_ACTIVATION_INFRASTRUCTURE_FILE", "mycfc/privacy-infrastructure-posture/v1"},
		{"PRIVACY_ACTIVATION_PROVIDER_FILE", "mycfc/privacy-provider-registry/v2"},
		{"PRIVACY_ACTIVATION_SCHEMA_FILE", "mycfc/schema-migration-inventory/v1"},
	} {
		payload, readErr := readArtifact(getenv(artifact.env))
		if readErr != nil {
			return activationInputs{}, readErr
		}
		var selector struct {
			Contract string `json:"contract"`
		}
		if json.Unmarshal(payload, &selector) != nil || selector.Contract != artifact.contract {
			return activationInputs{}, errors.New("privacy activation artifact contract rejected")
		}
		inputs.artifacts = append(inputs.artifacts, payload)
	}
	return inputs, nil
}

func runPrepare(ctx context.Context, getenv func(string) string, output io.Writer) error {
	if getenv("PRIVACY_ACTIVATION_EVIDENCE_ENABLED") != "true" {
		return errors.New("privacy activation evidence command is disabled")
	}
	release, err := trustedRelease(getenv)
	if err != nil {
		return err
	}
	material, err := prepareApprovalMaterial(ctx, strings.TrimSpace(getenv("PRIVACY_ACTIVATION_BROKER_DATABASE_URL")), release)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(material)
	if err != nil {
		return errors.New("privacy activation approval material rejected")
	}
	if path := strings.TrimSpace(getenv("PRIVACY_ACTIVATION_APPROVAL_MATERIAL_OUTPUT")); path != "" {
		return writeExclusive(path, payload)
	}
	_, err = fmt.Fprintln(output, string(payload))
	return err
}

func runSign(getenv func(string) string) error {
	material, err := loadMaterial(getenv("PRIVACY_ACTIVATION_APPROVAL_MATERIAL_FILE"))
	if err != nil {
		return err
	}
	actor, err := uuid.Parse(strings.TrimSpace(getenv("PRIVACY_ACTIVATION_APPROVAL_ACTOR_REF")))
	if err != nil {
		return errors.New("privacy activation signer rejected")
	}
	privateKey, err := readPrivateKey(getenv("PRIVACY_ACTIVATION_APPROVAL_PRIVATE_KEY_FILE"))
	if err != nil {
		return err
	}
	payload, err := makeApproval(material, strings.TrimSpace(getenv("PRIVACY_ACTIVATION_APPROVAL_ROLE")),
		strings.TrimSpace(getenv("PRIVACY_ACTIVATION_APPROVAL_SIGNING_KEY_ID")), actor, privateKey, time.Now().UTC())
	if err != nil {
		return err
	}
	return writeExclusive(getenv("PRIVACY_ACTIVATION_APPROVAL_OUTPUT"), payload)
}

func runActivate(ctx context.Context, getenv func(string) string, output io.Writer) error {
	if getenv("PRIVACY_ACTIVATION_EVIDENCE_ENABLED") != "true" {
		return errors.New("privacy activation evidence command is disabled")
	}
	material, err := loadMaterial(getenv("PRIVACY_ACTIVATION_APPROVAL_MATERIAL_FILE"))
	if err != nil {
		return err
	}
	release, err := trustedRelease(getenv)
	if err != nil || material.PolicyVersion != release.PolicyVersion || material.ExecutorVersion != release.ExecutorVersion ||
		material.PlanSchemaVersion != release.PlanSchemaVersion || material.ImageDigest != release.ImageDigest || material.SchemaMigrationDigest != release.SchemaMigrationDigest {
		return errors.New("privacy activation approval release rejected")
	}
	executorRaw, err := readArtifact(getenv("PRIVACY_ACTIVATION_EXECUTOR_APPROVAL_FILE"))
	if err != nil {
		return err
	}
	administratorRaw, err := readArtifact(getenv("PRIVACY_ACTIVATION_ADMIN_APPROVAL_FILE"))
	if err != nil {
		return err
	}
	executorPublic, err := readBase64Key(getenv("PRIVACY_ACTIVATION_EXECUTOR_APPROVAL_PUBLIC_KEY_FILE"))
	if err != nil {
		return err
	}
	administratorPublic, err := readBase64Key(getenv("PRIVACY_ACTIVATION_ADMIN_APPROVAL_PUBLIC_KEY_FILE"))
	if err != nil {
		return err
	}
	if err = activateApprovedMaterial(ctx, strings.TrimSpace(getenv("PRIVACY_ACTIVATION_BROKER_DATABASE_URL")), material,
		executorRaw, administratorRaw, strings.TrimSpace(getenv("PRIVACY_ACTIVATION_EXECUTOR_APPROVAL_SIGNING_KEY_ID")),
		strings.TrimSpace(getenv("PRIVACY_ACTIVATION_ADMIN_APPROVAL_SIGNING_KEY_ID")), ed25519.PublicKey(executorPublic), ed25519.PublicKey(administratorPublic), time.Now().UTC()); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "privacy_activation_approved independent_signatures=2")
	return err
}

func readArtifact(path string) ([]byte, error) {
	file, err := os.Open(strings.TrimSpace(path))
	if err != nil {
		return nil, errors.New("privacy activation artifact unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("privacy activation artifact rejected")
	}
	payload, err := io.ReadAll(io.LimitReader(file, maximumArtifactBytes+1))
	if err != nil || len(payload) == 0 || len(payload) > maximumArtifactBytes {
		return nil, errors.New("privacy activation artifact rejected")
	}
	return payload, nil
}

func readHexKey(path string) ([]byte, error) {
	payload, err := readSecret(path)
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(payload)))
	if err != nil || len(key) != sha256.Size {
		return nil, errors.New("privacy restore authentication key rejected")
	}
	return key, nil
}

func readSecret(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	pathInfo, err := os.Lstat(path)
	if err != nil || !pathInfo.Mode().IsRegular() {
		return nil, errors.New("privacy activation secret rejected")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("privacy activation secret unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(pathInfo, info) || (info.Mode().Perm() != 0o600 && info.Mode().Perm() != 0o400) {
		return nil, errors.New("privacy activation secret rejected")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return nil, errors.New("privacy activation secret rejected")
	}
	payload, err := io.ReadAll(io.LimitReader(file, maximumArtifactBytes+1))
	if err != nil || len(payload) == 0 || len(payload) > maximumArtifactBytes {
		return nil, errors.New("privacy activation secret rejected")
	}
	return payload, nil
}

func readBase64Key(path string) ([]byte, error) {
	payload, err := readArtifact(path)
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(payload)))
	if err != nil {
		return nil, errors.New("privacy activation artifact key rejected")
	}
	return key, nil
}
