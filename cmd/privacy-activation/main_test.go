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

func TestPrepareExchangeWritesBoundCeremonyMaterial(t *testing.T) {
	registry := commandRegistryFixture(t)
	registryRaw, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	registryPath := writeActivationFile(t, directory, "registry.json", registryRaw)
	registryDigest := commandApprovalDigest(registryRaw)
	outputPath := filepath.Join(directory, "material.json")
	ceremonyID := uuid.New()
	expiresAt := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second)
	previous := prepareActivationApprovalMaterial
	t.Cleanup(func() { prepareActivationApprovalMaterial = previous })
	prepareActivationApprovalMaterial = func(_ context.Context, databaseURL string, release privacyrequests.ActivationReleaseBinding,
		sourceSHA, pinnedRegistry string, gotRegistry privacyrequests.ActivationSignerRegistry, _ time.Time,
	) ([]byte, privacyrequests.ActivationApprovalMaterial, error) {
		if databaseURL != "postgres://broker.invalid/mycfc" || sourceSHA != "0123456789abcdef0123456789abcdef01234567" ||
			pinnedRegistry != registryDigest || gotRegistry.Signers.Executor.ActorRef != registry.Signers.Executor.ActorRef ||
			release.PolicyVersion != "policy-v1" || release.SchemaMigrationDigest != db.EmbeddedMigrationDigest() {
			t.Fatalf("unexpected prepare binding: database=%q source=%q registry=%q release=%+v", databaseURL, sourceSHA, pinnedRegistry, release)
		}
		return []byte(`{"contract":"material"}`), privacyrequests.ActivationApprovalMaterial{
			CeremonyID: ceremonyID, CeremonyExpiresAt: expiresAt,
		}, nil
	}
	env := map[string]string{
		"PRIVACY_ACTIVATION_EVIDENCE_ENABLED":         "true",
		"PRIVACY_ACTIVATION_POLICY_VERSION":           "policy-v1",
		"PRIVACY_ACTIVATION_CURRENT_IMAGE_DIGEST":     "sha256:" + strings.Repeat("a", 64),
		"PRIVACY_ACTIVATION_SIGNER_REGISTRY_FILE":     registryPath,
		"PRIVACY_ACTIVATION_SIGNER_REGISTRY_SHA256":   registryDigest,
		"PRIVACY_ACTIVATION_BROKER_DATABASE_URL":      "postgres://broker.invalid/mycfc",
		"PRIVACY_ACTIVATION_SOURCE_SHA":               "0123456789abcdef0123456789abcdef01234567",
		"PRIVACY_ACTIVATION_APPROVAL_MATERIAL_OUTPUT": outputPath,
	}
	var output bytes.Buffer
	if err := runPrepare(t.Context(), func(name string) string { return env[name] }, &output); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != `{"contract":"material"}` || !strings.Contains(output.String(), ceremonyID.String()) ||
		!strings.Contains(output.String(), expiresAt.Format(time.RFC3339)) {
		t.Fatalf("material=%q output=%q", written, output.String())
	}
	if info, err := os.Stat(outputPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("material permissions: info=%v err=%v", info, err)
	}
	prepareActivationApprovalMaterial = func(context.Context, string, privacyrequests.ActivationReleaseBinding,
		string, string, privacyrequests.ActivationSignerRegistry, time.Time,
	) ([]byte, privacyrequests.ActivationApprovalMaterial, error) {
		return nil, privacyrequests.ActivationApprovalMaterial{}, errors.New("broker material unavailable")
	}
	if err := runPrepare(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil {
		t.Fatal("broker preparation failure accepted")
	}
}

func TestActivateExchangeVerifiesReleaseAndDispatchesBoundApprovals(t *testing.T) {
	registry := commandRegistryFixture(t)
	registryRaw, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	registryDigest := commandApprovalDigest(registryRaw)
	directory := t.TempDir()
	now := time.Now().UTC()
	sourceSHA := "0123456789abcdef0123456789abcdef01234567"
	imageDigest := "sha256:" + commandDigestByte(0x31)
	materialRaw, material, err := privacyrequests.GenerateActivationApprovalMaterial(privacyrequests.ActivationApprovalMaterial{
		SourceSHA: sourceSHA, PolicyVersion: "policy-v1", EvidenceIDs: []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()},
		EvidenceSetSHA256: commandDigestByte(0x32), ActivationSHA256: commandDigestByte(0x33),
		ExecutorVersion: privacyrequests.SupportedExecutorVersion, PlanSchemaVersion: privacyrequests.SupportedPlanSchemaVersion,
		ImageDigest: imageDigest, SchemaMigrationDigest: db.EmbeddedMigrationDigest(), SignerRegistrySHA256: registryDigest,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"PRIVACY_ACTIVATION_APPROVAL_MATERIAL_FILE":             writeActivationFile(t, directory, "material.json", materialRaw),
		"PRIVACY_ACTIVATION_SIGNER_REGISTRY_FILE":               writeActivationFile(t, directory, "registry.json", registryRaw),
		"PRIVACY_ACTIVATION_EXECUTOR_APPROVAL_FILE":             writeActivationFile(t, directory, "executor.json", []byte("executor-approval")),
		"PRIVACY_ACTIVATION_ADMIN_APPROVAL_FILE":                writeActivationFile(t, directory, "administrator.json", []byte("administrator-approval")),
		"PRIVACY_ACTIVATION_EXECUTOR_PUBLIC_KEY_SPKI_FILE":      writeActivationFile(t, directory, "executor.der", []byte("executor-public")),
		"PRIVACY_ACTIVATION_ADMINISTRATOR_PUBLIC_KEY_SPKI_FILE": writeActivationFile(t, directory, "administrator.der", []byte("administrator-public")),
	}
	previous := activateApprovalMaterial
	t.Cleanup(func() { activateApprovalMaterial = previous })
	dispatched := false
	activateApprovalMaterial = func(_ context.Context, databaseURL string, gotMaterialRaw []byte,
		gotMaterial privacyrequests.ActivationApprovalMaterial, gotRegistry privacyrequests.ActivationSignerRegistry,
		executorRaw, administratorRaw, executorPublic, administratorPublic []byte, _ time.Time,
	) error {
		dispatched = true
		if databaseURL != "postgres://broker.invalid/mycfc" || !bytes.Equal(gotMaterialRaw, materialRaw) ||
			gotMaterial.CeremonyID != material.CeremonyID || gotRegistry.Signers.Administrator.ActorRef != registry.Signers.Administrator.ActorRef ||
			string(executorRaw) != "executor-approval" || string(administratorRaw) != "administrator-approval" ||
			string(executorPublic) != "executor-public" || string(administratorPublic) != "administrator-public" {
			t.Fatal("activation exchange lost a verified binding")
		}
		return nil
	}
	env := map[string]string{
		"PRIVACY_ACTIVATION_EVIDENCE_ENABLED":       "true",
		"PRIVACY_ACTIVATION_POLICY_VERSION":         material.PolicyVersion,
		"PRIVACY_ACTIVATION_CURRENT_IMAGE_DIGEST":   material.ImageDigest,
		"PRIVACY_ACTIVATION_SIGNER_REGISTRY_SHA256": registryDigest,
		"PRIVACY_ACTIVATION_CEREMONY_ID":            material.CeremonyID.String(),
		"PRIVACY_ACTIVATION_SOURCE_SHA":             sourceSHA,
		"PRIVACY_ACTIVATION_BROKER_DATABASE_URL":    "postgres://broker.invalid/mycfc",
	}
	for name, value := range files {
		env[name] = value
	}
	var output bytes.Buffer
	if err := runActivate(t.Context(), func(name string) string { return env[name] }, &output); err != nil {
		t.Fatal(err)
	}
	if !dispatched || output.String() != "privacy_activation_approved independent_signatures=2\n" {
		t.Fatalf("dispatched=%t output=%q", dispatched, output.String())
	}

	env["PRIVACY_ACTIVATION_CEREMONY_ID"] = "not-a-uuid"
	dispatched = false
	if err := runActivate(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil || dispatched {
		t.Fatal("invalid ceremony reached activation broker")
	}
	env["PRIVACY_ACTIVATION_CEREMONY_ID"] = material.CeremonyID.String()
	for _, variable := range []string{
		"PRIVACY_ACTIVATION_EXECUTOR_PUBLIC_KEY_SPKI_FILE",
		"PRIVACY_ACTIVATION_ADMINISTRATOR_PUBLIC_KEY_SPKI_FILE",
	} {
		previousPath := env[variable]
		env[variable] = filepath.Join(directory, "missing")
		dispatched = false
		if err := runActivate(t.Context(), func(name string) string { return env[name] }, io.Discard); err == nil || dispatched {
			t.Fatalf("missing %s reached activation broker", variable)
		}
		env[variable] = previousPath
	}
}

func TestExchangeModesFailClosedWhenDisabled(t *testing.T) {
	getenv := func(string) string { return "" }
	if err := runPrepare(t.Context(), getenv, io.Discard); err == nil {
		t.Fatal("disabled prepare exchange accepted")
	}
	if err := runActivate(t.Context(), getenv, io.Discard); err == nil {
		t.Fatal("disabled activate exchange accepted")
	}
}

func TestMainDispatchesExchangeModes(t *testing.T) {
	previousArgs := os.Args
	previousExit := exitProcess
	t.Setenv("PRIVACY_ACTIVATION_EVIDENCE_ENABLED", "")
	t.Cleanup(func() {
		os.Args = previousArgs
		exitProcess = previousExit
	})
	for _, mode := range []string{"prepare-exchange", "activate-exchange"} {
		t.Run(mode, func(t *testing.T) {
			os.Args = []string{"privacy-activation", mode}
			exitCode := 0
			exitProcess = func(code int) { exitCode = code }
			main()
			if exitCode != 1 {
				t.Fatalf("exit code = %d", exitCode)
			}
		})
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
