package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
)

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

func TestReadSecretRequiresOwnerRegularMode0600AndRejectsSymlink(t *testing.T) {
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

func writeActivationFile(t *testing.T, directory, name string, payload []byte) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
