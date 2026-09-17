package privacyrequests

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type approvalTestFixture struct {
	now              time.Time
	registryRaw      []byte
	registryDigest   string
	registry         ActivationSignerRegistry
	materialRaw      []byte
	material         ActivationApprovalMaterial
	executorPrivate  *ecdsa.PrivateKey
	executorPublic   []byte
	administratorKey *ecdsa.PrivateKey
	administratorPub []byte
}

func newApprovalTestFixture(t *testing.T) approvalTestFixture {
	t.Helper()
	executorPrivate, executorPublic := newP256Key(t)
	administratorPrivate, administratorPublic := newP256Key(t)
	registry := ActivationSignerRegistry{Contract: ActivationSignerRegistryContract, Signers: ActivationRegistrySigners{
		Executor: ActivationSigner{ActorRef: uuid.New(), SigningKeyID: "executor-key-v1",
			KMSKeyARN:        "arn:aws:kms:eu-west-1:123456789012:key/11111111-1111-4111-8111-111111111111",
			PublicKeySPKI256: digestBytes(executorPublic), GitHubActorID: 101, GitHubEnvironment: ActivationExecutorEnvironment},
		Administrator: ActivationSigner{ActorRef: uuid.New(), SigningKeyID: "administrator-key-v1",
			KMSKeyARN:        "arn:aws:kms:eu-west-1:123456789012:key/22222222-2222-4222-8222-222222222222",
			PublicKeySPKI256: digestBytes(administratorPublic), GitHubActorID: 202, GitHubEnvironment: ActivationAdministratorEnvironment},
	}}
	registryRaw, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	registryDigest := digestBytes(registryRaw)
	registry, err = ParseActivationSignerRegistry(registryRaw, registryDigest)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	materialRaw, material, err := GenerateActivationApprovalMaterial(ActivationApprovalMaterial{
		SourceSHA:             "0123456789abcdef0123456789abcdef01234567",
		PolicyVersion:         "club-2026-09-15-v1",
		EvidenceIDs:           []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()},
		EvidenceSetSHA256:     digestByte(0x11),
		ActivationSHA256:      digestByte(0x22),
		ExecutorVersion:       SupportedExecutorVersion,
		PlanSchemaVersion:     SupportedPlanSchemaVersion,
		ImageDigest:           "sha256:" + digestByte(0x33),
		SchemaMigrationDigest: digestByte(0x44),
		SignerRegistrySHA256:  registryDigest,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	return approvalTestFixture{now, registryRaw, registryDigest, registry, materialRaw, material,
		executorPrivate, executorPublic, administratorPrivate, administratorPublic}
}

func TestActivationApprovalV2RoundTrip(t *testing.T) {
	fixture := newApprovalTestFixture(t)
	executorRaw, executorNonce := makeSignedApproval(t, fixture, ActivationExecutorRole, 101, 501, 1,
		fixture.executorPrivate, fixture.executorPublic, fixture.now.Add(time.Minute))
	administratorRaw, administratorNonce := makeSignedApproval(t, fixture, ActivationAdministratorRole, 202, 502, 1,
		fixture.administratorKey, fixture.administratorPub, fixture.now.Add(2*time.Minute))
	bundle, err := VerifyActivationApprovalBundle(fixture.materialRaw, fixture.material, fixture.registry,
		executorRaw, administratorRaw, fixture.executorPublic, fixture.administratorPub, fixture.now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Executor.ActorRef != fixture.registry.Signers.Executor.ActorRef ||
		bundle.Administrator.ActorRef != fixture.registry.Signers.Administrator.ActorRef ||
		!bytes.Equal(bundle.ExecutorNonce, executorNonce) || !bytes.Equal(bundle.AdminNonce, administratorNonce) {
		t.Fatalf("bundle not registry-bound: %+v", bundle)
	}
	expected := ActivationMaterialExpectation{CeremonyID: fixture.material.CeremonyID, SourceSHA: fixture.material.SourceSHA,
		PolicyVersion: fixture.material.PolicyVersion, ImageDigest: fixture.material.ImageDigest,
		SchemaMigrationDigest: fixture.material.SchemaMigrationDigest, SignerRegistrySHA256: fixture.registryDigest}
	if _, err = VerifyActivationApprovalMaterial(fixture.materialRaw, expected, fixture.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestActivationCanonicalJSONRejectsUnknownWhitespaceAndTrailingBytes(t *testing.T) {
	fixture := newApprovalTestFixture(t)
	expected := ActivationMaterialExpectation{CeremonyID: fixture.material.CeremonyID, SourceSHA: fixture.material.SourceSHA,
		PolicyVersion: fixture.material.PolicyVersion, ImageDigest: fixture.material.ImageDigest,
		SchemaMigrationDigest: fixture.material.SchemaMigrationDigest, SignerRegistrySHA256: fixture.registryDigest}
	var object map[string]any
	if err := json.Unmarshal(fixture.materialRaw, &object); err != nil {
		t.Fatal(err)
	}
	object["unexpected"] = true
	unknown, _ := json.Marshal(object)
	for name, raw := range map[string][]byte{
		"unknown":             unknown,
		"leading whitespace":  append([]byte(" "), fixture.materialRaw...),
		"trailing whitespace": append(append([]byte(nil), fixture.materialRaw...), '\n'),
		"trailing object":     append(append([]byte(nil), fixture.materialRaw...), []byte("{}")...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyActivationApprovalMaterial(raw, expected, fixture.now); err == nil {
				t.Fatal("non-canonical material accepted")
			}
		})
	}
	if _, err := ParseActivationSignerRegistry(append([]byte(" "), fixture.registryRaw...), fixture.registryDigest); err == nil {
		t.Fatal("non-canonical signer registry accepted")
	}
}

func TestActivationRegistryRequiresDistinctExactSigners(t *testing.T) {
	fixture := newApprovalTestFixture(t)
	cases := map[string]func(*ActivationSignerRegistry){
		"actor": func(r *ActivationSignerRegistry) { r.Signers.Administrator.ActorRef = r.Signers.Executor.ActorRef },
		"github actor": func(r *ActivationSignerRegistry) {
			r.Signers.Administrator.GitHubActorID = r.Signers.Executor.GitHubActorID
		},
		"key id": func(r *ActivationSignerRegistry) {
			r.Signers.Administrator.SigningKeyID = r.Signers.Executor.SigningKeyID
		},
		"kms arn": func(r *ActivationSignerRegistry) { r.Signers.Administrator.KMSKeyARN = r.Signers.Executor.KMSKeyARN },
		"public key": func(r *ActivationSignerRegistry) {
			r.Signers.Administrator.PublicKeySPKI256 = r.Signers.Executor.PublicKeySPKI256
		},
		"environment": func(r *ActivationSignerRegistry) {
			r.Signers.Administrator.GitHubEnvironment = ActivationExecutorEnvironment
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			registry := fixture.registry
			change(&registry)
			raw, _ := json.Marshal(registry)
			if _, err := ParseActivationSignerRegistry(raw, digestBytes(raw)); err == nil {
				t.Fatal("invalid signer registry accepted")
			}
		})
	}
}

func TestActivationApprovalRejectsWrongIdentityKeyAlgorithmAndDigest(t *testing.T) {
	fixture := newApprovalTestFixture(t)
	unsigned, unsignedRaw, _, err := NewActivationApprovalUnsigned(fixture.materialRaw, fixture.material, fixture.registry,
		ActivationExecutorRole, 101, 501, 1, fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(unsignedRaw)
	signature, err := ecdsa.SignASN1(rand.Reader, fixture.executorPrivate, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	validRaw, _, err := AssembleActivationApproval(unsignedRaw, signature, fixture.executorPublic, fixture.materialRaw,
		fixture.material, fixture.registry, ActivationExecutorRole, fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*ActivationApprovalEnvelope){
		"actor":         func(e *ActivationApprovalEnvelope) { e.ActorRef = uuid.New() },
		"key id":        func(e *ActivationApprovalEnvelope) { e.SigningKeyID = "other-key" },
		"kms arn":       func(e *ActivationApprovalEnvelope) { e.KMSKeyARN = fixture.registry.Signers.Administrator.KMSKeyARN },
		"public digest": func(e *ActivationApprovalEnvelope) { e.PublicKeySPKI256 = digestByte(0x91) },
		"github actor":  func(e *ActivationApprovalEnvelope) { e.GitHubActorID++ },
		"environment":   func(e *ActivationApprovalEnvelope) { e.GitHubEnvironment = ActivationAdministratorEnvironment },
		"algorithm":     func(e *ActivationApprovalEnvelope) { e.SignatureAlgorithm = "ECDSA_SHA_384" },
		"material":      func(e *ActivationApprovalEnvelope) { e.MaterialSHA256 = digestByte(0x92) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			var envelope ActivationApprovalEnvelope
			if err := json.Unmarshal(validRaw, &envelope); err != nil {
				t.Fatal(err)
			}
			change(&envelope)
			raw, _ := json.Marshal(envelope)
			if _, _, err := VerifyActivationApproval(raw, fixture.materialRaw, fixture.material, fixture.registry,
				ActivationExecutorRole, fixture.executorPublic, fixture.now.Add(2*time.Minute)); err == nil {
				t.Fatal("altered approval accepted")
			}
		})
	}
	if unsigned.ActorRef != fixture.registry.Signers.Executor.ActorRef {
		t.Fatal("unsigned approval was not registry-derived")
	}
	if _, _, err := VerifyActivationApproval(validRaw, fixture.materialRaw, fixture.material, fixture.registry,
		ActivationExecutorRole, fixture.administratorPub, fixture.now.Add(2*time.Minute)); err == nil {
		t.Fatal("wrong public key accepted")
	}
}

func TestActivationApprovalRejectsMalformedDERSignature(t *testing.T) {
	fixture := newApprovalTestFixture(t)
	_, unsignedRaw, _, err := NewActivationApprovalUnsigned(fixture.materialRaw, fixture.material, fixture.registry,
		ActivationExecutorRole, 101, 501, 1, fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for name, signature := range map[string][]byte{
		"empty":        nil,
		"not DER":      bytes.Repeat([]byte{0x42}, 64),
		"trailing":     append(validSignature(t, fixture.executorPrivate, unsignedRaw), 0),
		"wrong digest": validSignature(t, fixture.executorPrivate, []byte("different")),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := AssembleActivationApproval(unsignedRaw, signature, fixture.executorPublic, fixture.materialRaw,
				fixture.material, fixture.registry, ActivationExecutorRole, fixture.now.Add(time.Minute)); err == nil {
				t.Fatal("malformed signature accepted")
			}
		})
	}
}

func TestActivationApprovalIsBoundedByFifteenMinutesAndCeremonyWindow(t *testing.T) {
	fixture := newApprovalTestFixture(t)
	validRaw, _ := makeSignedApproval(t, fixture, ActivationExecutorRole, 101, 501, 1,
		fixture.executorPrivate, fixture.executorPublic, fixture.now.Add(time.Minute))
	if _, _, err := VerifyActivationApproval(validRaw, fixture.materialRaw, fixture.material, fixture.registry,
		ActivationExecutorRole, fixture.executorPublic, fixture.now); err == nil {
		t.Fatal("future approval accepted")
	}
	if _, _, err := VerifyActivationApproval(validRaw, fixture.materialRaw, fixture.material, fixture.registry,
		ActivationExecutorRole, fixture.executorPublic, fixture.now.Add(16*time.Minute)); err == nil {
		t.Fatal("expired approval accepted")
	}
	var envelope ActivationApprovalEnvelope
	if err := json.Unmarshal(validRaw, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.ExpiresAt = envelope.IssuedAt.Add(13 * time.Minute)
	shortRaw, _ := json.Marshal(envelope)
	if _, _, err := VerifyActivationApproval(shortRaw, fixture.materialRaw, fixture.material, fixture.registry,
		ActivationExecutorRole, fixture.executorPublic, fixture.now.Add(2*time.Minute)); err == nil {
		t.Fatal("altered approval lifetime accepted")
	}
	late, _, _, err := NewActivationApprovalUnsigned(fixture.materialRaw, fixture.material, fixture.registry,
		ActivationExecutorRole, 101, 501, 1, fixture.material.CeremonyExpiresAt.Add(-time.Minute))
	if err != nil || !late.ExpiresAt.Equal(fixture.material.CeremonyExpiresAt) {
		t.Fatalf("approval was not capped by ceremony: approval=%+v error=%v", late, err)
	}
	if _, _, _, err = NewActivationApprovalUnsigned(fixture.materialRaw, fixture.material, fixture.registry,
		ActivationExecutorRole, 101, 501, 1, fixture.material.CeremonyExpiresAt); err == nil {
		t.Fatal("approval at ceremony expiry accepted")
	}
	if _, err := ParseActivationApprovalMaterial(fixture.materialRaw, fixture.registryDigest, fixture.material.CeremonyExpiresAt); err == nil {
		t.Fatal("expired ceremony accepted")
	}
}

func TestActivationBundleRejectsRepeatedNonce(t *testing.T) {
	fixture := newApprovalTestFixture(t)
	executorRaw, _ := makeSignedApproval(t, fixture, ActivationExecutorRole, 101, 501, 1,
		fixture.executorPrivate, fixture.executorPublic, fixture.now.Add(time.Minute))
	var executor ActivationApprovalEnvelope
	if err := json.Unmarshal(executorRaw, &executor); err != nil {
		t.Fatal(err)
	}
	administratorUnsigned, _, _, err := NewActivationApprovalUnsigned(fixture.materialRaw, fixture.material, fixture.registry,
		ActivationAdministratorRole, 202, 502, 1, fixture.now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	administratorUnsigned.Nonce = executor.Nonce
	administratorRaw, err := json.Marshal(administratorUnsigned)
	if err != nil {
		t.Fatal(err)
	}
	signature := validSignature(t, fixture.administratorKey, administratorRaw)
	administratorApproval, _, err := AssembleActivationApproval(administratorRaw, signature, fixture.administratorPub,
		fixture.materialRaw, fixture.material, fixture.registry, ActivationAdministratorRole, fixture.now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyActivationApprovalBundle(fixture.materialRaw, fixture.material, fixture.registry, executorRaw,
		administratorApproval, fixture.executorPublic, fixture.administratorPub, fixture.now.Add(3*time.Minute)); err == nil {
		t.Fatal("same nonce accepted across roles")
	}
}

func TestActivationApprovalBoundaryHelpersFailClosed(t *testing.T) {
	fixture := newApprovalTestFixture(t)
	if _, ok := fixture.registry.Signer("AUDITOR"); ok {
		t.Fatal("unknown signer role accepted")
	}
	if parsed, err := ParseActivationApprovalMaterial(fixture.materialRaw, fixture.registryDigest, fixture.now.Add(time.Minute)); err != nil || parsed.CeremonyID != fixture.material.CeremonyID {
		t.Fatalf("material parse=%+v error=%v", parsed, err)
	}
	invalidMaterial := fixture.material
	invalidMaterial.EvidenceIDs = append([]uuid.UUID(nil), fixture.material.EvidenceIDs...)
	invalidMaterial.EvidenceIDs[1] = invalidMaterial.EvidenceIDs[0]
	if validActivationMaterial(invalidMaterial, fixture.now) {
		t.Fatal("material with duplicate evidence accepted")
	}
	if _, _, _, err := NewActivationApprovalUnsigned(fixture.materialRaw, fixture.material, fixture.registry,
		"AUDITOR", 101, 501, 1, fixture.now.Add(time.Minute)); err == nil {
		t.Fatal("unknown approval role accepted")
	}
	if _, _, err := AssembleActivationApproval([]byte("{}"), []byte("signature"), fixture.executorPublic,
		fixture.materialRaw, fixture.material, fixture.registry, ActivationExecutorRole, fixture.now.Add(time.Minute)); err == nil {
		t.Fatal("invalid unsigned approval assembled")
	}
	executorRaw, _ := makeSignedApproval(t, fixture, ActivationExecutorRole, 101, 501, 1,
		fixture.executorPrivate, fixture.executorPublic, fixture.now.Add(time.Minute))
	var envelope ActivationApprovalEnvelope
	if err := json.Unmarshal(executorRaw, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.SignatureDERBase64 = "%%%"
	invalidSignatureRaw, _ := json.Marshal(envelope)
	if _, _, err := VerifyActivationApproval(invalidSignatureRaw, fixture.materialRaw, fixture.material, fixture.registry,
		ActivationExecutorRole, fixture.executorPublic, fixture.now.Add(2*time.Minute)); err == nil {
		t.Fatal("invalid base64 signature accepted")
	}
	if _, err := VerifyActivationApprovalBundle(fixture.materialRaw, fixture.material, fixture.registry,
		[]byte("{}"), []byte("{}"), fixture.executorPublic, fixture.administratorPub, fixture.now.Add(time.Minute)); err == nil {
		t.Fatal("bundle with invalid executor approval accepted")
	}

	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p384DER, err := x509.MarshalPKIXPublicKey(&p384.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = verifyActivationECDSA(p384DER, digestBytes(p384DER), []byte("message"), []byte{0x30, 0}); err == nil {
		t.Fatal("non-P256 public key accepted")
	}
	if raw, material, err := GenerateActivationApprovalMaterial(ActivationApprovalMaterial{}, fixture.now); err == nil || raw != nil || material.CeremonyID != uuid.Nil {
		t.Fatalf("invalid generated material raw=%q material=%+v error=%v", raw, material, err)
	}
	if _, err = ParseActivationPublicKeySPKIBase64("%%% "); err == nil {
		t.Fatal("invalid public key encoding accepted")
	}
	if _, err = DecodeActivationSignatureBase64(""); err == nil {
		t.Fatal("empty signature accepted")
	}
	if err = decodeCanonicalActivationJSON(bytes.Repeat([]byte("x"), MaximumActivationApprovalBytes+1), &ActivationApprovalMaterial{}); err == nil {
		t.Fatal("oversized approval JSON accepted")
	}
	if distinctUUIDs([]uuid.UUID{uuid.Nil}) || distinctUUIDs([]uuid.UUID{fixture.material.EvidenceIDs[0], fixture.material.EvidenceIDs[0]}) {
		t.Fatal("non-distinct UUID list accepted")
	}
	if equalActivationUUIDs([]uuid.UUID{uuid.New()}, nil) || equalActivationUUIDs([]uuid.UUID{uuid.New()}, []uuid.UUID{uuid.New()}) {
		t.Fatal("different UUID lists treated as equal")
	}
	if got := fixture.material.String(); !strings.Contains(got, fixture.material.CeremonyID.String()) || !strings.Contains(got, fixture.material.ProposalID.String()) {
		t.Fatalf("material string=%q", got)
	}
}

func makeSignedApproval(t *testing.T, fixture approvalTestFixture, role string, actorID, runID, attempt uint64,
	privateKey *ecdsa.PrivateKey, publicKey []byte, issued time.Time) ([]byte, []byte) {
	t.Helper()
	_, unsignedRaw, _, err := NewActivationApprovalUnsigned(fixture.materialRaw, fixture.material, fixture.registry,
		role, actorID, runID, attempt, issued)
	if err != nil {
		t.Fatal(err)
	}
	signature := validSignature(t, privateKey, unsignedRaw)
	raw, envelope, err := AssembleActivationApproval(unsignedRaw, signature, publicKey, fixture.materialRaw,
		fixture.material, fixture.registry, role, issued)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := base64.StdEncoding.Strict().DecodeString(envelope.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	return raw, nonce
}

func validSignature(t *testing.T, privateKey *ecdsa.PrivateKey, unsigned []byte) []byte {
	t.Helper()
	digest := sha256.Sum256(unsigned)
	signature, err := ecdsa.SignASN1(rand.Reader, privateKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signature
}

func newP256Key(t *testing.T) (*ecdsa.PrivateKey, []byte) {
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

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func digestByte(value byte) string {
	return hex.EncodeToString(bytes.Repeat([]byte{value}, sha256.Size))
}
