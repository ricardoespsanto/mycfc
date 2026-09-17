package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
)

type activationEvidenceStoreFake struct {
	kinds       []string
	artifactErr error
	restoreErr  error
	artifactAt  int
	closed      bool
}

func (f *activationEvidenceStoreFake) VerifyAndRecordRestoreActivationEvidence(context.Context, uuid.UUID, []byte, []byte,
	privacyrequests.ActivationReleaseBinding, time.Time) (privacyrequests.ActivationEvidence, error) {
	return privacyrequests.ActivationEvidence{Kind: "RESTORE"}, f.restoreErr
}

func (f *activationEvidenceStoreFake) VerifyAndRecordActivationArtifact(context.Context, uuid.UUID, []byte, map[string]ed25519.PublicKey,
	privacyrequests.ActivationReleaseBinding, time.Time) (privacyrequests.ActivationEvidence, error) {
	index := f.artifactAt
	f.artifactAt++
	if f.artifactErr != nil {
		return privacyrequests.ActivationEvidence{}, f.artifactErr
	}
	return privacyrequests.ActivationEvidence{Kind: f.kinds[index]}, nil
}

func (f *activationEvidenceStoreFake) Close() { f.closed = true }

func TestLoadActivationInputsUsesTrustedCurrentReleaseBinding(t *testing.T) {
	directory := t.TempDir()
	public, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	restoreKey := bytes.Repeat([]byte{0x42}, sha256.Size)
	files := map[string]string{
		"PRIVACY_ACTIVATION_RESTORE_AUTH_KEY_FILE":    writeActivationFile(t, directory, "restore.key", []byte(hex.EncodeToString(restoreKey))),
		"PRIVACY_ACTIVATION_ARTIFACT_PUBLIC_KEY_FILE": writeActivationFile(t, directory, "artifact.pub", []byte(base64.StdEncoding.EncodeToString(public))),
		"PRIVACY_ACTIVATION_RESTORE_ATTESTATION_FILE": writeActivationFile(t, directory, "restore.json", []byte(`{"contract":"restore"}`)),
		"PRIVACY_ACTIVATION_INFRASTRUCTURE_FILE":      writeActivationFile(t, directory, "infrastructure.json", []byte(`{"contract":"mycfc/privacy-infrastructure-posture/v1"}`)),
		"PRIVACY_ACTIVATION_PROVIDER_FILE":            writeActivationFile(t, directory, "provider.json", []byte(`{"contract":"mycfc/privacy-provider-registry/v2"}`)),
		"PRIVACY_ACTIVATION_SCHEMA_FILE":              writeActivationFile(t, directory, "schema.json", []byte(`{"contract":"mycfc/schema-migration-inventory/v1"}`)),
	}
	actor := uuid.New()
	image := "sha256:" + strings.Repeat("a", 64)
	env := map[string]string{
		"PRIVACY_ACTIVATION_EVIDENCE_ENABLED":                "true",
		"PRIVACY_ACTIVATION_BROKER_DATABASE_URL":             "postgres://broker@postgres/mycfc",
		"PRIVACY_ACTIVATION_ACTOR_REF":                       actor.String(),
		"PRIVACY_ACTIVATION_ARTIFACT_SIGNING_KEY_ID":         "operations-v1",
		"PRIVACY_ACTIVATION_POLICY_VERSION":                  "privacy-policy-v1",
		"PRIVACY_ACTIVATION_CURRENT_IMAGE_DIGEST":            image,
		"PRIVACY_ACTIVATION_CURRENT_SCHEMA_MIGRATION_DIGEST": strings.Repeat("f", 64),
	}
	for name, value := range files {
		env[name] = value
	}
	inputs, err := loadActivationInputs(func(name string) string { return env[name] })
	if err != nil {
		t.Fatal(err)
	}
	if inputs.actor != actor || inputs.databaseURL != env["PRIVACY_ACTIVATION_BROKER_DATABASE_URL"] || !bytes.Equal(inputs.restoreKey, restoreKey) || len(inputs.artifacts) != 3 {
		t.Fatalf("unexpected activation inputs: actor=%s artifacts=%d", inputs.actor, len(inputs.artifacts))
	}
	if inputs.release != (privacyrequests.ActivationReleaseBinding{
		PolicyVersion: "privacy-policy-v1", ExecutorVersion: privacyrequests.SupportedExecutorVersion,
		PlanSchemaVersion: privacyrequests.SupportedPlanSchemaVersion, ImageDigest: image, SchemaMigrationDigest: db.EmbeddedMigrationDigest(),
	}) {
		t.Fatalf("release binding=%+v", inputs.release)
	}
	if inputs.release.SchemaMigrationDigest == env["PRIVACY_ACTIVATION_CURRENT_SCHEMA_MIGRATION_DIGEST"] {
		t.Fatal("untrusted environment schema digest replaced the embedded migration inventory")
	}
	if !bytes.Equal(inputs.trustedKeys["operations-v1"], public) {
		t.Fatal("trusted signing key was not bound to its configured key ID")
	}
}

func TestLoadActivationInputsRequiresExactEnablementAndTrustRoot(t *testing.T) {
	if _, err := loadActivationInputs(func(string) string { return "" }); err == nil {
		t.Fatal("disabled activation command was accepted")
	}
	env := map[string]string{"PRIVACY_ACTIVATION_EVIDENCE_ENABLED": "TRUE"}
	if _, err := loadActivationInputs(func(name string) string { return env[name] }); err == nil {
		t.Fatal("non-exact activation flag was accepted")
	}
}

func TestReadArtifactRejectsDirectoriesAndOversizedFiles(t *testing.T) {
	directory := t.TempDir()
	if _, err := readArtifact(directory); err == nil {
		t.Fatal("directory accepted as activation artifact")
	}
	path := filepath.Join(directory, "oversized")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, maximumArtifactBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readArtifact(path); err == nil {
		t.Fatal("oversized activation artifact accepted")
	}
}

func TestReadSecretRequiresOwnerRegularPrivateModeAndRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	secret := writeActivationFile(t, directory, "secret", []byte("secret"))
	if _, err := readSecret(secret); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(secret, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecret(secret); err == nil {
		t.Fatal("group-readable activation secret accepted")
	}
	if err := os.Chmod(secret, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecret(secret); err != nil {
		t.Fatalf("owner-read-only activation secret rejected: %v", err)
	}
	if err := os.Chmod(secret, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "secret-link")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecret(link); err == nil {
		t.Fatal("symlink activation secret accepted")
	}
	if _, err := readSecret(directory); err == nil {
		t.Fatal("non-regular activation secret accepted")
	}
}

func TestReadSecretRejectsHardLink(t *testing.T) {
	directory := t.TempDir()
	secret := writeActivationFile(t, directory, "secret", []byte("secret"))
	link := filepath.Join(directory, "hard-link")
	if err := os.Link(secret, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecret(secret); err == nil {
		t.Fatal("multiply linked activation secret accepted")
	}
}

func TestRunValidatesAndRecordsCompleteEvidenceSet(t *testing.T) {
	env := activationCommandEnvironment(t)
	originalRestoreVerifier := verifyRestoreActivationAttestation
	originalArtifactVerifier := verifyActivationArtifact
	originalOpenStore := openActivationEvidenceStore
	t.Cleanup(func() {
		verifyRestoreActivationAttestation = originalRestoreVerifier
		verifyActivationArtifact = originalArtifactVerifier
		openActivationEvidenceStore = originalOpenStore
	})
	verifyRestoreActivationAttestation = func([]byte, []byte, privacyrequests.ActivationReleaseBinding, time.Time) (privacyrequests.VerifiedActivationEvidence, error) {
		return privacyrequests.VerifiedActivationEvidence{}, nil
	}
	verifyActivationArtifact = func([]byte, map[string]ed25519.PublicKey, privacyrequests.ActivationReleaseBinding, time.Time) (privacyrequests.VerifiedActivationEvidence, error) {
		return privacyrequests.VerifiedActivationEvidence{}, nil
	}
	store := &activationEvidenceStoreFake{kinds: []string{"INFRASTRUCTURE", "PROVIDER", "SCHEMA"}}
	openActivationEvidenceStore = func(context.Context, string) (activationEvidenceStore, error) { return store, nil }
	var output bytes.Buffer
	if err := run(t.Context(), func(name string) string { return env[name] }, &output); err != nil {
		t.Fatal(err)
	}
	if !store.closed || strings.TrimSpace(output.String()) != "privacy_activation_evidence_recorded restore=1 infrastructure=1 provider=1 schema=1" {
		t.Fatalf("closed=%t output=%q", store.closed, output.String())
	}

	verifyRestoreActivationAttestation = func([]byte, []byte, privacyrequests.ActivationReleaseBinding, time.Time) (privacyrequests.VerifiedActivationEvidence, error) {
		return privacyrequests.VerifiedActivationEvidence{}, errors.New("invalid restore")
	}
	if err := run(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil {
		t.Fatal("invalid restore evidence accepted")
	}
	verifyRestoreActivationAttestation = func([]byte, []byte, privacyrequests.ActivationReleaseBinding, time.Time) (privacyrequests.VerifiedActivationEvidence, error) {
		return privacyrequests.VerifiedActivationEvidence{}, nil
	}
	verifyActivationArtifact = func([]byte, map[string]ed25519.PublicKey, privacyrequests.ActivationReleaseBinding, time.Time) (privacyrequests.VerifiedActivationEvidence, error) {
		return privacyrequests.VerifiedActivationEvidence{}, errors.New("invalid artifact")
	}
	if err := run(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil {
		t.Fatal("invalid signed artifact accepted")
	}
	verifyActivationArtifact = func([]byte, map[string]ed25519.PublicKey, privacyrequests.ActivationReleaseBinding, time.Time) (privacyrequests.VerifiedActivationEvidence, error) {
		return privacyrequests.VerifiedActivationEvidence{}, nil
	}
	openActivationEvidenceStore = func(context.Context, string) (activationEvidenceStore, error) {
		return nil, errors.New("database unavailable")
	}
	if err := run(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil {
		t.Fatal("database open failure accepted")
	}
	for name, failingStore := range map[string]*activationEvidenceStoreFake{
		"restore record":  {kinds: []string{"INFRASTRUCTURE", "PROVIDER", "SCHEMA"}, restoreErr: errors.New("restore rejected")},
		"artifact record": {kinds: []string{"INFRASTRUCTURE", "PROVIDER", "SCHEMA"}, artifactErr: errors.New("artifact rejected")},
		"incomplete set":  {kinds: []string{"SCHEMA", "SCHEMA", "SCHEMA"}},
	} {
		t.Run(name, func(t *testing.T) {
			openActivationEvidenceStore = func(context.Context, string) (activationEvidenceStore, error) { return failingStore, nil }
			if err := run(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil {
				t.Fatal("recording failure accepted")
			}
			if !failingStore.closed {
				t.Fatal("evidence store was not closed")
			}
		})
	}
	adapterClosed := false
	adapterWriter := &activationEvidenceStoreFake{kinds: []string{"INFRASTRUCTURE"}}
	adapter := postgresActivationEvidenceStore{close: func() { adapterClosed = true }, writer: adapterWriter}
	if _, err := adapter.VerifyAndRecordRestoreActivationEvidence(t.Context(), uuid.New(), nil, nil, privacyrequests.ActivationReleaseBinding{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.VerifyAndRecordActivationArtifact(t.Context(), uuid.New(), nil, nil, privacyrequests.ActivationReleaseBinding{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	adapter.Close()
	if !adapterClosed {
		t.Fatal("postgres adapter did not close")
	}
	if _, err := originalOpenStore(t.Context(), "://invalid"); err == nil {
		t.Fatal("invalid database URL accepted")
	}
}

func TestActivationInputAndKeyFailureBoundaries(t *testing.T) {
	env := activationCommandEnvironment(t)
	getenv := func(name string) string { return env[name] }
	for name, mutate := range map[string]func(){
		"operator": func() { env["PRIVACY_ACTIVATION_ACTOR_REF"] = "invalid" },
		"trust root": func() {
			env["PRIVACY_ACTIVATION_ARTIFACT_SIGNING_KEY_ID"] = ""
		},
		"restore key": func() {
			env["PRIVACY_ACTIVATION_RESTORE_AUTH_KEY_FILE"] = filepath.Join(t.TempDir(), "missing")
		},
		"artifact trust key": func() {
			env["PRIVACY_ACTIVATION_ARTIFACT_PUBLIC_KEY_FILE"] = filepath.Join(t.TempDir(), "missing")
		},
		"restore": func() {
			env["PRIVACY_ACTIVATION_RESTORE_ATTESTATION_FILE"] = filepath.Join(t.TempDir(), "missing")
		},
		"required signed artifact": func() {
			env["PRIVACY_ACTIVATION_PROVIDER_FILE"] = filepath.Join(t.TempDir(), "missing")
		},
		"contract": func() {
			env["PRIVACY_ACTIVATION_PROVIDER_FILE"] = writeActivationFile(t, t.TempDir(), "provider.json", []byte(`{"contract":"wrong"}`))
		},
	} {
		t.Run(name, func(t *testing.T) {
			original := make(map[string]string, len(env))
			for key, value := range env {
				original[key] = value
			}
			mutate()
			if _, err := loadActivationInputs(getenv); err == nil {
				t.Fatal("invalid activation input accepted")
			}
			env = original
		})
	}
	directory := t.TempDir()
	invalidHex := writeActivationFile(t, directory, "invalid-hex.key", []byte("short"))
	if _, err := readHexKey(invalidHex); err == nil {
		t.Fatal("invalid restore authentication key accepted")
	}
	invalidBase64 := writeActivationFile(t, directory, "invalid-base64.key", []byte("not-base64"))
	if _, err := readBase64Key(invalidBase64); err == nil {
		t.Fatal("invalid artifact public key accepted")
	}
	emptySecret := writeActivationFile(t, directory, "empty.secret", nil)
	if _, err := readSecret(emptySecret); err == nil {
		t.Fatal("empty secret accepted")
	}
}

func activationCommandEnvironment(t *testing.T) map[string]string {
	t.Helper()
	directory := t.TempDir()
	public, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	restoreKey := bytes.Repeat([]byte{0x42}, sha256.Size)
	actor := uuid.New()
	return map[string]string{
		"PRIVACY_ACTIVATION_EVIDENCE_ENABLED":         "true",
		"PRIVACY_ACTIVATION_BROKER_DATABASE_URL":      "postgres://broker.invalid/mycfc",
		"PRIVACY_ACTIVATION_ACTOR_REF":                actor.String(),
		"PRIVACY_ACTIVATION_ARTIFACT_SIGNING_KEY_ID":  "operations-v1",
		"PRIVACY_ACTIVATION_POLICY_VERSION":           "policy-v1",
		"PRIVACY_ACTIVATION_CURRENT_IMAGE_DIGEST":     "sha256:" + strings.Repeat("a", 64),
		"PRIVACY_ACTIVATION_RESTORE_AUTH_KEY_FILE":    writeActivationFile(t, directory, "restore.key", []byte(hex.EncodeToString(restoreKey))),
		"PRIVACY_ACTIVATION_ARTIFACT_PUBLIC_KEY_FILE": writeActivationFile(t, directory, "artifact.pub", []byte(base64.StdEncoding.EncodeToString(public))),
		"PRIVACY_ACTIVATION_RESTORE_ATTESTATION_FILE": writeActivationFile(t, directory, "restore.json", []byte(`{"contract":"restore"}`)),
		"PRIVACY_ACTIVATION_INFRASTRUCTURE_FILE":      writeActivationFile(t, directory, "infrastructure.json", []byte(`{"contract":"mycfc/privacy-infrastructure-posture/v1"}`)),
		"PRIVACY_ACTIVATION_PROVIDER_FILE":            writeActivationFile(t, directory, "provider.json", []byte(`{"contract":"mycfc/privacy-provider-registry/v2"}`)),
		"PRIVACY_ACTIVATION_SCHEMA_FILE":              writeActivationFile(t, directory, "schema.json", []byte(`{"contract":"mycfc/schema-migration-inventory/v1"}`)),
	}
}

func writeActivationFile(t *testing.T, directory, name string, payload []byte) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
