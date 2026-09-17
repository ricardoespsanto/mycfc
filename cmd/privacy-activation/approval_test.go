package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type brokerRowFake struct{ scan func(...any) error }

func (r brokerRowFake) Scan(destinations ...any) error { return r.scan(destinations...) }

type brokerDatabaseFake struct {
	rows      []pgx.Row
	closed    bool
	calls     int
	arguments [][]any
}

func (f *brokerDatabaseFake) QueryRow(_ context.Context, _ string, arguments ...any) pgx.Row {
	f.arguments = append(f.arguments, arguments)
	row := f.rows[f.calls]
	f.calls++
	return row
}

func (f *brokerDatabaseFake) Close() { f.closed = true }

func TestPrepareApprovalMaterialRegistersExactCeremony(t *testing.T) {
	release := testReleaseBinding()
	registry := commandRegistryFixture(t)
	registryRaw, _ := json.Marshal(registry)
	registryDigest := commandApprovalDigest(registryRaw)
	evidenceIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	setDigest := bytes.Repeat([]byte{0xa1}, 32)
	activationDigest := bytes.Repeat([]byte{0xb2}, 32)
	var registeredID uuid.UUID
	database := &brokerDatabaseFake{rows: []pgx.Row{
		brokerRowFake{scan: func(destinations ...any) error {
			*destinations[0].(*[]uuid.UUID) = evidenceIDs
			*destinations[1].(*[]byte) = setDigest
			*destinations[2].(*[]byte) = activationDigest
			*destinations[3].(*string) = release.ExecutorVersion
			*destinations[4].(*string) = release.PlanSchemaVersion
			*destinations[5].(*string) = release.ImageDigest
			schema, _ := hex.DecodeString(release.SchemaMigrationDigest)
			*destinations[6].(*[]byte) = schema
			return nil
		}},
		brokerRowFake{scan: func(destinations ...any) error {
			*destinations[0].(*uuid.UUID) = registeredID
			return nil
		}},
	}}
	previous := openActivationBrokerDatabase
	openActivationBrokerDatabase = func(context.Context, string) (activationBrokerDatabase, error) { return database, nil }
	t.Cleanup(func() { openActivationBrokerDatabase = previous })
	now := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	// The registration fake must echo the generated ceremony ID. Read it from
	// the arguments before Scan is invoked.
	database.rows[1] = brokerRowFake{scan: func(destinations ...any) error {
		registeredID = database.arguments[1][0].(uuid.UUID)
		*destinations[0].(*uuid.UUID) = registeredID
		return nil
	}}
	raw, material, err := prepareApprovalMaterial(t.Context(), "postgres://broker.invalid/database", release,
		"0123456789abcdef0123456789abcdef01234567", registryDigest, registry, now)
	if err != nil {
		t.Fatal(err)
	}
	if !database.closed || database.calls != 2 || material.CeremonyID != registeredID ||
		material.CeremonyExpiresAt.Sub(material.PreparedAt) != 15*time.Minute || !bytes.Equal(raw, database.arguments[1][13].([]byte)) {
		t.Fatalf("ceremony not exactly registered: material=%+v calls=%d closed=%t", material, database.calls, database.closed)
	}
	if got := database.arguments[1][17].(uuid.UUID); got != registry.Signers.Executor.ActorRef {
		t.Fatalf("executor registry actor = %s", got)
	}
	if got := database.arguments[1][23].(uuid.UUID); got != registry.Signers.Administrator.ActorRef {
		t.Fatalf("administrator registry actor = %s", got)
	}
}

func TestActivateApprovedMaterialVerifiesBundleBeforeBroker(t *testing.T) {
	registry, materialRaw, material, executorPrivate, executorPublic, administratorPrivate, administratorPublic, now := activationCommandFixture(t)
	executorRaw := commandSignedEnvelope(t, materialRaw, material, registry, privacyrequests.ActivationExecutorRole,
		registry.Signers.Executor.GitHubActorID, executorPrivate, executorPublic, now.Add(time.Minute))
	administratorRaw := commandSignedEnvelope(t, materialRaw, material, registry, privacyrequests.ActivationAdministratorRole,
		registry.Signers.Administrator.GitHubActorID, administratorPrivate, administratorPublic, now.Add(2*time.Minute))
	approvalID := uuid.New()
	database := &brokerDatabaseFake{rows: []pgx.Row{brokerRowFake{scan: func(destinations ...any) error {
		*destinations[0].(*uuid.UUID) = approvalID
		return nil
	}}}}
	previous := openActivationBrokerDatabase
	openActivationBrokerDatabase = func(context.Context, string) (activationBrokerDatabase, error) { return database, nil }
	t.Cleanup(func() { openActivationBrokerDatabase = previous })
	if err := activateApprovedMaterial(t.Context(), "postgres://broker.invalid/database", materialRaw, material, registry,
		executorRaw, administratorRaw, executorPublic, administratorPublic, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if database.calls != 1 || !database.closed || database.arguments[0][0] != material.CeremonyID {
		t.Fatalf("broker dispatch calls=%d closed=%t args=%#v", database.calls, database.closed, database.arguments)
	}

	tampered := append([]byte(nil), executorRaw...)
	tampered[len(tampered)-1] ^= 1
	database.calls = 0
	database.arguments = nil
	if err := activateApprovedMaterial(t.Context(), "postgres://broker.invalid/database", materialRaw, material, registry,
		tampered, administratorRaw, executorPublic, administratorPublic, now.Add(3*time.Minute)); err == nil {
		t.Fatal("tampered bundle accepted")
	}
	if database.calls != 0 {
		t.Fatal("broker was called before bundle verification")
	}
}

func TestSignerRegistryReadRejectsSymlinkAndLooseMode(t *testing.T) {
	registry := commandRegistryFixture(t)
	raw, _ := json.Marshal(registry)
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := commandApprovalDigest(raw)
	if _, err := loadSignerRegistry(path, digest); err != nil {
		t.Fatal(err)
	}
	symlink := path + ".link"
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSignerRegistry(symlink, digest); err == nil {
		t.Fatal("symlink registry accepted")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSignerRegistry(path, digest); err == nil {
		t.Fatal("loosely permissioned registry accepted")
	}
}

func TestExclusiveApprovalOutputRejectsEmptyInputs(t *testing.T) {
	if err := writeExclusive("", []byte("material")); err == nil {
		t.Fatal("empty output path accepted")
	}
	if err := writeExclusive(filepath.Join(t.TempDir(), "material.json"), nil); err == nil {
		t.Fatal("empty approval material accepted")
	}
}

func activationCommandFixture(t *testing.T) (privacyrequests.ActivationSignerRegistry, []byte, privacyrequests.ActivationApprovalMaterial,
	*ecdsa.PrivateKey, []byte, *ecdsa.PrivateKey, []byte, time.Time) {
	t.Helper()
	executorPrivate, executorPublic := approvalP256Key(t)
	administratorPrivate, administratorPublic := approvalP256Key(t)
	registry := commandRegistryFixtureWithKeys(executorPublic, administratorPublic)
	registryRaw, _ := json.Marshal(registry)
	now := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	materialRaw, material, err := privacyrequests.GenerateActivationApprovalMaterial(privacyrequests.ActivationApprovalMaterial{
		SourceSHA: "0123456789abcdef0123456789abcdef01234567", PolicyVersion: "club-2026-09-15-v1",
		EvidenceIDs: []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}, EvidenceSetSHA256: commandDigestByte(0x11),
		ActivationSHA256: commandDigestByte(0x22), ExecutorVersion: privacyrequests.SupportedExecutorVersion,
		PlanSchemaVersion: privacyrequests.SupportedPlanSchemaVersion, ImageDigest: "sha256:" + commandDigestByte(0x33),
		SchemaMigrationDigest: commandDigestByte(0x44), SignerRegistrySHA256: commandApprovalDigest(registryRaw),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	return registry, materialRaw, material, executorPrivate, executorPublic, administratorPrivate, administratorPublic, now
}

func commandSignedEnvelope(t *testing.T, materialRaw []byte, material privacyrequests.ActivationApprovalMaterial,
	registry privacyrequests.ActivationSignerRegistry, role string, githubActorID uint64, privateKey *ecdsa.PrivateKey,
	publicKey []byte, now time.Time) []byte {
	t.Helper()
	_, unsignedRaw, digest, err := privacyrequests.NewActivationApprovalUnsigned(materialRaw, material, registry, role,
		githubActorID, 501, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := ecdsa.SignASN1(rand.Reader, privateKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := privacyrequests.AssembleActivationApproval(unsignedRaw, signature, publicKey, materialRaw, material, registry, role, now)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func commandRegistryFixture(t *testing.T) privacyrequests.ActivationSignerRegistry {
	t.Helper()
	_, executorPublic := approvalP256Key(t)
	_, administratorPublic := approvalP256Key(t)
	return commandRegistryFixtureWithKeys(executorPublic, administratorPublic)
}

func commandRegistryFixtureWithKeys(executorPublic, administratorPublic []byte) privacyrequests.ActivationSignerRegistry {
	return privacyrequests.ActivationSignerRegistry{Contract: privacyrequests.ActivationSignerRegistryContract,
		Signers: privacyrequests.ActivationRegistrySigners{
			Executor: privacyrequests.ActivationSigner{ActorRef: uuid.New(), SigningKeyID: "executor-v1",
				KMSKeyARN:        "arn:aws:kms:eu-west-1:123456789012:key/11111111-1111-4111-8111-111111111111",
				PublicKeySPKI256: commandApprovalDigest(executorPublic), GitHubActorID: 101,
				GitHubEnvironment: privacyrequests.ActivationExecutorEnvironment},
			Administrator: privacyrequests.ActivationSigner{ActorRef: uuid.New(), SigningKeyID: "administrator-v1",
				KMSKeyARN:        "arn:aws:kms:eu-west-1:123456789012:key/22222222-2222-4222-8222-222222222222",
				PublicKeySPKI256: commandApprovalDigest(administratorPublic), GitHubActorID: 202,
				GitHubEnvironment: privacyrequests.ActivationAdministratorEnvironment},
		}}
}

func approvalP256Key(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey, publicKey
}

func commandApprovalDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func commandDigestByte(value byte) string {
	return hex.EncodeToString(bytes.Repeat([]byte{value}, sha256.Size))
}

func testReleaseBinding() privacyrequests.ActivationReleaseBinding {
	return privacyrequests.ActivationReleaseBinding{
		PolicyVersion: "club-2026-09-15-v1", ExecutorVersion: privacyrequests.SupportedExecutorVersion,
		PlanSchemaVersion: privacyrequests.SupportedPlanSchemaVersion, ImageDigest: "sha256:" + commandDigestByte(0x71),
		SchemaMigrationDigest: commandDigestByte(0x72),
	}
}
