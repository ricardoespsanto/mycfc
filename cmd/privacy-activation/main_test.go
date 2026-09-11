package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
		"PRIVACY_ACTIVATION_PROVIDER_FILE":            writeActivationFile(t, directory, "provider.json", []byte(`{"contract":"mycfc/privacy-provider-registry/v1"}`)),
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

func TestRunSignDispatchesCanonicalApprovalToExclusiveFile(t *testing.T) {
	directory := t.TempDir()
	material := testApprovalMaterial()
	materialPayload, err := json.Marshal(material)
	if err != nil {
		t.Fatal(err)
	}
	materialPath := writeActivationFile(t, directory, "approval-material.json", materialPayload)
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := writeActivationFile(t, directory, "approval-private.key", []byte(base64.StdEncoding.EncodeToString(private)))
	outputPath := filepath.Join(directory, "approval.json")
	actor := uuid.New()
	env := map[string]string{
		"PRIVACY_ACTIVATION_APPROVAL_MATERIAL_FILE":    materialPath,
		"PRIVACY_ACTIVATION_APPROVAL_ACTOR_REF":        actor.String(),
		"PRIVACY_ACTIVATION_APPROVAL_PRIVATE_KEY_FILE": privatePath,
		"PRIVACY_ACTIVATION_APPROVAL_ROLE":             "EXECUTOR",
		"PRIVACY_ACTIVATION_APPROVAL_SIGNING_KEY_ID":   "executor-v1",
		"PRIVACY_ACTIVATION_APPROVAL_OUTPUT":           outputPath,
	}
	if err = runSign(func(name string) string { return env[name] }); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.TrimSuffix(raw, []byte{'\n'})
	if _, _, err = verifyApproval(raw, material, "EXECUTOR", "executor-v1", public, time.Now().UTC()); err != nil {
		t.Fatalf("signed approval did not verify: %v", err)
	}
	env["PRIVACY_ACTIVATION_APPROVAL_ACTOR_REF"] = "invalid"
	if err = runSign(func(name string) string { return env[name] }); err == nil {
		t.Fatal("invalid signer accepted")
	}
	if err = runSign(func(string) string { return "" }); err == nil {
		t.Fatal("missing approval material accepted")
	}
	env["PRIVACY_ACTIVATION_APPROVAL_ACTOR_REF"] = actor.String()
	env["PRIVACY_ACTIVATION_APPROVAL_PRIVATE_KEY_FILE"] = filepath.Join(directory, "missing.key")
	if err = runSign(func(name string) string { return env[name] }); err == nil {
		t.Fatal("missing private key accepted")
	}
	env["PRIVACY_ACTIVATION_APPROVAL_PRIVATE_KEY_FILE"] = privatePath
	env["PRIVACY_ACTIVATION_APPROVAL_ROLE"] = "REVIEWER"
	if err = runSign(func(name string) string { return env[name] }); err == nil {
		t.Fatal("invalid approval role accepted")
	}
}

func TestPrepareAndActivateDispatchThroughBrokerBoundary(t *testing.T) {
	t.Run("prepare", func(t *testing.T) {
		env := activationCommandEnvironment(t)
		release, err := trustedRelease(func(name string) string { return env[name] })
		if err != nil {
			t.Fatal(err)
		}
		evidenceIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
		database := &brokerDatabaseFake{row: brokerRowFake{scan: func(destinations ...any) error {
			*destinations[0].(*[]uuid.UUID) = evidenceIDs
			*destinations[1].(*[]byte) = bytes.Repeat([]byte{0x11}, 32)
			*destinations[2].(*[]byte) = bytes.Repeat([]byte{0x22}, 32)
			*destinations[3].(*string) = release.ExecutorVersion
			*destinations[4].(*string) = release.PlanSchemaVersion
			*destinations[5].(*string) = release.ImageDigest
			schema, _ := hex.DecodeString(release.SchemaMigrationDigest)
			*destinations[6].(*[]byte) = schema
			return nil
		}}}
		withBrokerDatabase(t, database)
		var output bytes.Buffer
		if err = runPrepare(t.Context(), func(name string) string { return env[name] }, &output); err != nil {
			t.Fatal(err)
		}
		var material approvalMaterial
		if err = json.Unmarshal(output.Bytes(), &material); err != nil || material.Contract != approvalMaterialContract {
			t.Fatalf("material=%+v error=%v", material, err)
		}
		outputPath := filepath.Join(t.TempDir(), "material.json")
		env["PRIVACY_ACTIVATION_APPROVAL_MATERIAL_OUTPUT"] = outputPath
		database.closed = false
		if err = runPrepare(t.Context(), func(name string) string { return env[name] }, io.Discard); err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(outputPath); err != nil {
			t.Fatal(err)
		}
		env["PRIVACY_ACTIVATION_CURRENT_IMAGE_DIGEST"] = "invalid"
		if err = runPrepare(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil {
			t.Fatal("invalid release accepted for preparation")
		}
		env["PRIVACY_ACTIVATION_CURRENT_IMAGE_DIGEST"] = release.ImageDigest
		database.row = brokerRowFake{scan: func(...any) error { return errors.New("broker unavailable") }}
		if err = runPrepare(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil {
			t.Fatal("broker preparation failure accepted")
		}
	})

	t.Run("activate", func(t *testing.T) {
		directory := t.TempDir()
		env := activationCommandEnvironment(t)
		release, err := trustedRelease(func(name string) string { return env[name] })
		if err != nil {
			t.Fatal(err)
		}
		material := approvalMaterial{Contract: approvalMaterialContract, ProposalID: uuid.New(), PolicyVersion: release.PolicyVersion,
			EvidenceIDs: []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}, EvidenceSetSHA256: digestHex('a'), ActivationSHA256: digestHex('b'),
			ExecutorVersion: release.ExecutorVersion, PlanSchemaVersion: release.PlanSchemaVersion, ImageDigest: release.ImageDigest, SchemaMigrationDigest: release.SchemaMigrationDigest}
		materialPayload, err := json.Marshal(material)
		if err != nil {
			t.Fatal(err)
		}
		env["PRIVACY_ACTIVATION_APPROVAL_MATERIAL_FILE"] = writeActivationFile(t, directory, "material.json", materialPayload)
		executorPublic, executorPrivate, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatal(err)
		}
		administratorPublic, administratorPrivate, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		executorRaw, err := makeApproval(material, "EXECUTOR", "executor-v1", uuid.New(), executorPrivate, now)
		if err != nil {
			t.Fatal(err)
		}
		administratorRaw, err := makeApproval(material, "ADMINISTRATOR", "administrator-v1", uuid.New(), administratorPrivate, now)
		if err != nil {
			t.Fatal(err)
		}
		env["PRIVACY_ACTIVATION_EXECUTOR_APPROVAL_FILE"] = writeActivationFile(t, directory, "executor.json", executorRaw)
		env["PRIVACY_ACTIVATION_ADMIN_APPROVAL_FILE"] = writeActivationFile(t, directory, "administrator.json", administratorRaw)
		env["PRIVACY_ACTIVATION_EXECUTOR_APPROVAL_PUBLIC_KEY_FILE"] = writeActivationFile(t, directory, "executor.pub", []byte(base64.StdEncoding.EncodeToString(executorPublic)))
		env["PRIVACY_ACTIVATION_ADMIN_APPROVAL_PUBLIC_KEY_FILE"] = writeActivationFile(t, directory, "administrator.pub", []byte(base64.StdEncoding.EncodeToString(administratorPublic)))
		env["PRIVACY_ACTIVATION_EXECUTOR_APPROVAL_SIGNING_KEY_ID"] = "executor-v1"
		env["PRIVACY_ACTIVATION_ADMIN_APPROVAL_SIGNING_KEY_ID"] = "administrator-v1"
		database := &brokerDatabaseFake{row: brokerRowFake{scan: func(destinations ...any) error {
			*destinations[0].(*uuid.UUID) = uuid.New()
			return nil
		}}}
		withBrokerDatabase(t, database)
		var output bytes.Buffer
		if err = runActivate(t.Context(), func(name string) string { return env[name] }, &output); err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(output.String()) != "privacy_activation_approved independent_signatures=2" {
			t.Fatalf("activate output=%q", output.String())
		}
		env["PRIVACY_ACTIVATION_POLICY_VERSION"] = "different-policy"
		if err = runActivate(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil {
			t.Fatal("mismatched release accepted")
		}
		env["PRIVACY_ACTIVATION_POLICY_VERSION"] = release.PolicyVersion
		executorPath := env["PRIVACY_ACTIVATION_EXECUTOR_APPROVAL_FILE"]
		env["PRIVACY_ACTIVATION_EXECUTOR_APPROVAL_FILE"] = filepath.Join(directory, "missing-approval")
		if err = runActivate(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil {
			t.Fatal("missing executor approval accepted")
		}
		env["PRIVACY_ACTIVATION_EXECUTOR_APPROVAL_FILE"] = executorPath
		executorPublicPath := env["PRIVACY_ACTIVATION_EXECUTOR_APPROVAL_PUBLIC_KEY_FILE"]
		env["PRIVACY_ACTIVATION_EXECUTOR_APPROVAL_PUBLIC_KEY_FILE"] = writeActivationFile(t, directory, "bad-executor.pub", []byte("not-base64"))
		if err = runActivate(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil {
			t.Fatal("invalid executor public key accepted")
		}
		env["PRIVACY_ACTIVATION_EXECUTOR_APPROVAL_PUBLIC_KEY_FILE"] = executorPublicPath
		administratorPath := env["PRIVACY_ACTIVATION_ADMIN_APPROVAL_FILE"]
		env["PRIVACY_ACTIVATION_ADMIN_APPROVAL_FILE"] = filepath.Join(directory, "missing-administrator")
		if err = runActivate(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil {
			t.Fatal("missing administrator approval accepted")
		}
		env["PRIVACY_ACTIVATION_ADMIN_APPROVAL_FILE"] = administratorPath
		administratorPublicPath := env["PRIVACY_ACTIVATION_ADMIN_APPROVAL_PUBLIC_KEY_FILE"]
		env["PRIVACY_ACTIVATION_ADMIN_APPROVAL_PUBLIC_KEY_FILE"] = writeActivationFile(t, directory, "bad-administrator.pub", []byte("not-base64"))
		if err = runActivate(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil {
			t.Fatal("invalid administrator public key accepted")
		}
		env["PRIVACY_ACTIVATION_ADMIN_APPROVAL_PUBLIC_KEY_FILE"] = administratorPublicPath
		materialPath := env["PRIVACY_ACTIVATION_APPROVAL_MATERIAL_FILE"]
		env["PRIVACY_ACTIVATION_APPROVAL_MATERIAL_FILE"] = filepath.Join(directory, "missing-material")
		if err = runActivate(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil {
			t.Fatal("missing approval material accepted")
		}
		env["PRIVACY_ACTIVATION_APPROVAL_MATERIAL_FILE"] = materialPath
		database.row = brokerRowFake{scan: func(...any) error { return errors.New("broker rejected") }}
		if err = runActivate(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil {
			t.Fatal("broker activation rejection accepted")
		}
	})

	if err := runPrepare(t.Context(), func(string) string { return "" }, io.Discard); err == nil {
		t.Fatal("disabled prepare accepted")
	}
	if err := runActivate(t.Context(), func(string) string { return "" }, io.Discard); err == nil {
		t.Fatal("disabled activation accepted")
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
		"PRIVACY_ACTIVATION_PROVIDER_FILE":            writeActivationFile(t, directory, "provider.json", []byte(`{"contract":"mycfc/privacy-provider-registry/v1"}`)),
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
