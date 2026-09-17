package main

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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
)

type commandFixture struct {
	directory               string
	now                     time.Time
	registryPath            string
	registryDigest          string
	materialPath            string
	material                privacyrequests.ActivationApprovalMaterial
	executorPrivate         *ecdsa.PrivateKey
	executorPublicPath      string
	administratorPrivate    *ecdsa.PrivateKey
	administratorPublicPath string
}

func newCommandFixture(t *testing.T) commandFixture {
	t.Helper()
	directory := t.TempDir()
	executorPrivate, executorPublic := commandP256Key(t)
	administratorPrivate, administratorPublic := commandP256Key(t)
	registry := privacyrequests.ActivationSignerRegistry{Contract: privacyrequests.ActivationSignerRegistryContract,
		Signers: privacyrequests.ActivationRegistrySigners{
			Executor: privacyrequests.ActivationSigner{ActorRef: uuid.New(), SigningKeyID: "executor-v1",
				KMSKeyARN:        "arn:aws:kms:eu-west-1:123456789012:key/11111111-1111-4111-8111-111111111111",
				PublicKeySPKI256: commandDigest(executorPublic), GitHubActorID: 101,
				GitHubEnvironment: privacyrequests.ActivationExecutorEnvironment},
			Administrator: privacyrequests.ActivationSigner{ActorRef: uuid.New(), SigningKeyID: "administrator-v1",
				KMSKeyARN:        "arn:aws:kms:eu-west-1:123456789012:key/22222222-2222-4222-8222-222222222222",
				PublicKeySPKI256: commandDigest(administratorPublic), GitHubActorID: 202,
				GitHubEnvironment: privacyrequests.ActivationAdministratorEnvironment},
		}}
	registryRaw, _ := json.Marshal(registry)
	registryPath := commandWrite(t, directory, "registry.json", registryRaw)
	now := time.Date(2026, 9, 17, 13, 0, 0, 0, time.UTC)
	materialRaw, material, err := privacyrequests.GenerateActivationApprovalMaterial(privacyrequests.ActivationApprovalMaterial{
		SourceSHA: "0123456789abcdef0123456789abcdef01234567", PolicyVersion: "club-2026-09-15-v1",
		EvidenceIDs:       []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()},
		EvidenceSetSHA256: commandRepeatedDigest(0x11), ActivationSHA256: commandRepeatedDigest(0x22),
		ExecutorVersion: privacyrequests.SupportedExecutorVersion, PlanSchemaVersion: privacyrequests.SupportedPlanSchemaVersion,
		ImageDigest: "sha256:" + commandRepeatedDigest(0x33), SchemaMigrationDigest: commandRepeatedDigest(0x44),
		SignerRegistrySHA256: commandDigest(registryRaw),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	return commandFixture{directory: directory, now: now, registryPath: registryPath, registryDigest: commandDigest(registryRaw),
		materialPath: commandWrite(t, directory, "material.json", materialRaw), material: material,
		executorPrivate: executorPrivate, executorPublicPath: commandWrite(t, directory, "executor.der", executorPublic),
		administratorPrivate: administratorPrivate, administratorPublicPath: commandWrite(t, directory, "administrator.der", administratorPublic)}
}

func TestSigningOnlyCommandRoundTrip(t *testing.T) {
	fixture := newCommandFixture(t)
	previousTime := currentTime
	currentTime = func() time.Time { return fixture.now.Add(time.Minute) }
	t.Cleanup(func() { currentTime = previousTime })

	common := fixture.commonArguments()
	if err := run(append([]string{"material-verify"}, common...), ioDiscard{}); err != nil {
		t.Fatal(err)
	}
	executorApproval := fixture.makeApproval(t, common, privacyrequests.ActivationExecutorRole, "101", "501", fixture.executorPrivate, fixture.executorPublicPath)
	administratorApproval := fixture.makeApproval(t, common, privacyrequests.ActivationAdministratorRole, "202", "502", fixture.administratorPrivate, fixture.administratorPublicPath)
	var verificationOutput bytes.Buffer
	verificationArguments := append([]string{"approval-verify"}, common...)
	verificationArguments = append(verificationArguments, "--role", privacyrequests.ActivationExecutorRole,
		"--approval", executorApproval, "--public-key-spki", fixture.executorPublicPath)
	if err := run(verificationArguments, &verificationOutput); err != nil {
		t.Fatal(err)
	}
	if verificationOutput.String() != "privacy_activation_approval_verified\n" {
		t.Fatalf("unexpected verification output %q", verificationOutput.String())
	}
	arguments := append([]string{"bundle-verify"}, common...)
	arguments = append(arguments, "--executor-approval", executorApproval, "--administrator-approval", administratorApproval,
		"--executor-public-key-spki", fixture.executorPublicPath, "--administrator-public-key-spki", fixture.administratorPublicPath)
	var output bytes.Buffer
	if err := run(arguments, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "privacy_activation_approval_bundle_verified\n" {
		t.Fatalf("unexpected output %q", output.String())
	}
}

func TestSigningOnlyCommandHasNoPrivateKeyInput(t *testing.T) {
	fixture := newCommandFixture(t)
	arguments := append([]string{"approval-unsigned"}, fixture.commonArguments()...)
	arguments = append(arguments, "--role", privacyrequests.ActivationExecutorRole, "--github-actor-id", "101",
		"--github-run-id", "501", "--github-run-attempt", "1", "--output", filepath.Join(fixture.directory, "unsigned.json"),
		"--digest-output", filepath.Join(fixture.directory, "digest.bin"), "--private-key", filepath.Join(fixture.directory, "key"))
	if err := run(arguments, ioDiscard{}); err == nil {
		t.Fatal("private-key option was accepted")
	}
}

func TestProtectedReadRejectsSymlinkAndLooseMode(t *testing.T) {
	directory := t.TempDir()
	path := commandWrite(t, directory, "input", []byte("content"))
	symlink := filepath.Join(directory, "link")
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := readProtected(symlink, 100); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readProtected(path, 100); err == nil {
		t.Fatal("loosely permissioned input accepted")
	}
}

func TestProtectedReadRejectsHardLink(t *testing.T) {
	directory := t.TempDir()
	path := commandWrite(t, directory, "input", []byte("content"))
	link := filepath.Join(directory, "hard-link")
	if err := os.Link(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readProtected(path, 100); err == nil {
		t.Fatal("multiply linked protected input accepted")
	}
}

func TestExclusiveOutputRejectsExistingAndSymlink(t *testing.T) {
	directory := t.TempDir()
	existing := commandWrite(t, directory, "existing", []byte("old"))
	if err := writeExclusive(existing, []byte("new")); err == nil {
		t.Fatal("existing output replaced")
	}
	target := filepath.Join(directory, "target")
	if err := os.Symlink(existing, target); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusive(target, []byte("new")); err == nil {
		t.Fatal("symlink output followed")
	}
}

func TestCommandRejectsMissingAndUnknownModes(t *testing.T) {
	for _, arguments := range [][]string{nil, {"unknown"}} {
		if err := run(arguments, ioDiscard{}); err == nil {
			t.Fatalf("arguments %q were accepted", arguments)
		}
	}
}

func TestProtectedReadAcceptsReadOnlyAndRejectsSizeBounds(t *testing.T) {
	directory := t.TempDir()
	readOnly := commandWrite(t, directory, "read-only", []byte("content"))
	if err := os.Chmod(readOnly, 0o400); err != nil {
		t.Fatal(err)
	}
	payload, err := readProtected(readOnly, 100)
	if err != nil || string(payload) != "content" {
		t.Fatalf("read-only input: payload=%q err=%v", payload, err)
	}
	for name, payload := range map[string][]byte{
		"empty":     nil,
		"oversized": bytes.Repeat([]byte{'x'}, 101),
	} {
		path := commandWrite(t, directory, name, payload)
		if _, err := readProtected(path, 100); err == nil {
			t.Fatalf("%s protected input accepted", name)
		}
	}
	if _, err := readProtected(filepath.Join(directory, "missing"), 100); err == nil {
		t.Fatal("missing protected input accepted")
	}
}

func TestExclusiveOutputRejectsEmptyInputs(t *testing.T) {
	if err := writeExclusive("", []byte("content")); err == nil {
		t.Fatal("empty output path accepted")
	}
	if err := writeExclusive(filepath.Join(t.TempDir(), "output"), nil); err == nil {
		t.Fatal("empty output accepted")
	}
}

func TestCommandFailureBoundaries(t *testing.T) {
	fixture := newCommandFixture(t)
	previousTime := currentTime
	currentTime = func() time.Time { return fixture.now.Add(time.Minute) }
	t.Cleanup(func() { currentTime = previousTime })
	common := fixture.commonArguments()
	executorApproval := fixture.makeApproval(t, common, privacyrequests.ActivationExecutorRole, "101", "501",
		fixture.executorPrivate, fixture.executorPublicPath)
	administratorApproval := fixture.makeApproval(t, common, privacyrequests.ActivationAdministratorRole, "202", "502",
		fixture.administratorPrivate, fixture.administratorPublicPath)
	executorUnsigned := filepath.Join(fixture.directory, privacyrequests.ActivationExecutorRole+"-unsigned.json")
	executorSignature := filepath.Join(fixture.directory, privacyrequests.ActivationExecutorRole+"-signature.txt")
	missing := filepath.Join(fixture.directory, "missing")

	expectError := func(name string, arguments []string) {
		t.Helper()
		if err := run(arguments, ioDiscard{}); err == nil {
			t.Fatalf("%s failure was accepted", name)
		}
	}
	badCommon := append([]string(nil), common...)
	for index := range badCommon {
		if badCommon[index] == "--expected-ceremony-id" {
			badCommon[index+1] = "not-a-uuid"
		}
	}

	expectError("material flags", []string{"material-verify", "--unknown"})
	expectError("material binding", append([]string{"material-verify"}, badCommon...))

	unsignedArguments := func(bound []string, role, output, digest string) []string {
		arguments := append([]string{"approval-unsigned"}, bound...)
		return append(arguments, "--role", role, "--github-actor-id", "101", "--github-run-id", "700",
			"--github-run-attempt", "1", "--output", output, "--digest-output", digest)
	}
	expectError("unsigned material binding", unsignedArguments(badCommon, privacyrequests.ActivationExecutorRole,
		filepath.Join(fixture.directory, "bad-unsigned.json"), filepath.Join(fixture.directory, "bad-digest.bin")))
	expectError("unsigned role", unsignedArguments(common, "UNKNOWN",
		filepath.Join(fixture.directory, "unknown-unsigned.json"), filepath.Join(fixture.directory, "unknown-digest.bin")))
	expectError("unsigned existing output", unsignedArguments(common, privacyrequests.ActivationExecutorRole,
		executorUnsigned, filepath.Join(fixture.directory, "unused-digest.bin")))
	cleanupOutput := filepath.Join(fixture.directory, "cleanup-unsigned.json")
	expectError("unsigned existing digest", unsignedArguments(common, privacyrequests.ActivationExecutorRole,
		cleanupOutput, filepath.Join(fixture.directory, privacyrequests.ActivationExecutorRole+"-digest.bin")))
	if _, err := os.Stat(cleanupOutput); !os.IsNotExist(err) {
		t.Fatalf("partial unsigned output was retained: %v", err)
	}

	assembleArguments := func(bound []string, role, unsigned, signature, publicKey, output string) []string {
		arguments := append([]string{"approval-assemble"}, bound...)
		return append(arguments, "--role", role, "--unsigned", unsigned, "--signature", signature,
			"--public-key-spki", publicKey, "--output", output)
	}
	expectError("assemble flags", []string{"approval-assemble", "--unknown"})
	expectError("assemble material binding", assembleArguments(badCommon, privacyrequests.ActivationExecutorRole,
		executorUnsigned, executorSignature, fixture.executorPublicPath, filepath.Join(fixture.directory, "bad-approval.json")))
	expectError("assemble unsigned read", assembleArguments(common, privacyrequests.ActivationExecutorRole,
		missing, executorSignature, fixture.executorPublicPath, filepath.Join(fixture.directory, "missing-unsigned-approval.json")))
	expectError("assemble signature read", assembleArguments(common, privacyrequests.ActivationExecutorRole,
		executorUnsigned, missing, fixture.executorPublicPath, filepath.Join(fixture.directory, "missing-signature-approval.json")))
	invalidSignature := commandWrite(t, fixture.directory, "invalid-signature.txt", []byte("not-base64"))
	expectError("assemble signature", assembleArguments(common, privacyrequests.ActivationExecutorRole,
		executorUnsigned, invalidSignature, fixture.executorPublicPath, filepath.Join(fixture.directory, "invalid-signature-approval.json")))
	expectError("assemble public key read", assembleArguments(common, privacyrequests.ActivationExecutorRole,
		executorUnsigned, executorSignature, missing, filepath.Join(fixture.directory, "missing-key-approval.json")))
	expectError("assemble role binding", assembleArguments(common, privacyrequests.ActivationAdministratorRole,
		executorUnsigned, executorSignature, fixture.executorPublicPath, filepath.Join(fixture.directory, "wrong-role-approval.json")))
	expectError("assemble existing output", assembleArguments(common, privacyrequests.ActivationExecutorRole,
		executorUnsigned, executorSignature, fixture.executorPublicPath, executorApproval))

	verifyArguments := func(bound []string, role, approval, publicKey string) []string {
		arguments := append([]string{"approval-verify"}, bound...)
		return append(arguments, "--role", role, "--approval", approval, "--public-key-spki", publicKey)
	}
	expectError("verify flags", []string{"approval-verify", "--unknown"})
	expectError("verify material binding", verifyArguments(badCommon, privacyrequests.ActivationExecutorRole,
		executorApproval, fixture.executorPublicPath))
	expectError("verify approval read", verifyArguments(common, privacyrequests.ActivationExecutorRole, missing, fixture.executorPublicPath))
	expectError("verify public key read", verifyArguments(common, privacyrequests.ActivationExecutorRole, executorApproval, missing))
	expectError("verify role binding", verifyArguments(common, privacyrequests.ActivationAdministratorRole,
		executorApproval, fixture.executorPublicPath))

	bundleArguments := func(bound []string, executor, administrator, executorPublic, administratorPublic string) []string {
		arguments := append([]string{"bundle-verify"}, bound...)
		return append(arguments, "--executor-approval", executor, "--administrator-approval", administrator,
			"--executor-public-key-spki", executorPublic, "--administrator-public-key-spki", administratorPublic)
	}
	expectError("bundle flags", []string{"bundle-verify", "--unknown"})
	expectError("bundle material binding", bundleArguments(badCommon, executorApproval, administratorApproval,
		fixture.executorPublicPath, fixture.administratorPublicPath))
	expectError("bundle executor approval read", bundleArguments(common, missing, administratorApproval,
		fixture.executorPublicPath, fixture.administratorPublicPath))
	expectError("bundle administrator approval read", bundleArguments(common, executorApproval, missing,
		fixture.executorPublicPath, fixture.administratorPublicPath))
	expectError("bundle executor public key read", bundleArguments(common, executorApproval, administratorApproval,
		missing, fixture.administratorPublicPath))
	expectError("bundle administrator public key read", bundleArguments(common, executorApproval, administratorApproval,
		fixture.executorPublicPath, missing))
	expectError("bundle key binding", bundleArguments(common, executorApproval, administratorApproval,
		fixture.administratorPublicPath, fixture.executorPublicPath))
}

func TestCommonInputFailureBoundaries(t *testing.T) {
	fixture := newCommandFixture(t)
	valid := commonOptions{
		materialPath: fixture.materialPath, registryPath: fixture.registryPath, registrySHA256: fixture.registryDigest,
		expectedCeremonyID: fixture.material.CeremonyID.String(), expectedSourceSHA: fixture.material.SourceSHA,
		expectedPolicyVersion: fixture.material.PolicyVersion, expectedImageDigest: fixture.material.ImageDigest,
		expectedSchemaMigrationDigest: fixture.material.SchemaMigrationDigest,
	}
	assertRejected := func(name string, options commonOptions) {
		t.Helper()
		if _, err := options.load(fixture.now.Add(time.Minute)); err == nil {
			t.Fatalf("%s input was accepted", name)
		}
	}
	options := valid
	options.expectedCeremonyID = "not-a-uuid"
	assertRejected("ceremony", options)
	options = valid
	options.registryPath = filepath.Join(fixture.directory, "missing-registry")
	assertRejected("registry read", options)
	options = valid
	options.registryPath = commandWrite(t, fixture.directory, "invalid-registry.json", []byte(`{"contract":"wrong"}`))
	assertRejected("registry", options)
	options = valid
	options.materialPath = filepath.Join(fixture.directory, "missing-material")
	assertRejected("material read", options)
	options = valid
	options.expectedSourceSHA = "ffffffffffffffffffffffffffffffffffffffff"
	assertRejected("material binding", options)
}

func (f commandFixture) commonArguments() []string {
	return []string{"--material", f.materialPath, "--registry", f.registryPath, "--registry-sha256", f.registryDigest,
		"--expected-ceremony-id", f.material.CeremonyID.String(), "--expected-source-sha", f.material.SourceSHA,
		"--expected-policy-version", f.material.PolicyVersion, "--expected-image-digest", f.material.ImageDigest,
		"--expected-schema-migration-digest", f.material.SchemaMigrationDigest}
}

func (f commandFixture) makeApproval(t *testing.T, common []string, role, githubActorID, githubRunID string,
	privateKey *ecdsa.PrivateKey, publicKeyPath string) string {
	t.Helper()
	stem := role
	unsignedPath := filepath.Join(f.directory, stem+"-unsigned.json")
	digestPath := filepath.Join(f.directory, stem+"-digest.bin")
	arguments := append([]string{"approval-unsigned"}, common...)
	arguments = append(arguments, "--role", role, "--github-actor-id", githubActorID, "--github-run-id", githubRunID,
		"--github-run-attempt", "1", "--output", unsignedPath, "--digest-output", digestPath)
	if err := run(arguments, ioDiscard{}); err != nil {
		t.Fatal(err)
	}
	digest, err := os.ReadFile(digestPath)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := ecdsa.SignASN1(rand.Reader, privateKey, digest)
	if err != nil {
		t.Fatal(err)
	}
	signaturePath := commandWrite(t, f.directory, stem+"-signature.txt", []byte(base64.StdEncoding.EncodeToString(signature)))
	approvalPath := filepath.Join(f.directory, stem+"-approval.json")
	arguments = append([]string{"approval-assemble"}, common...)
	arguments = append(arguments, "--role", role, "--unsigned", unsignedPath, "--signature", signaturePath,
		"--public-key-spki", publicKeyPath, "--output", approvalPath)
	if err := run(arguments, ioDiscard{}); err != nil {
		t.Fatal(err)
	}
	return approvalPath
}

type ioDiscard struct{}

func (ioDiscard) Write(value []byte) (int, error) { return len(value), nil }

func commandP256Key(t *testing.T) (*ecdsa.PrivateKey, []byte) {
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

func commandWrite(t *testing.T, directory, name string, value []byte) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, value, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func commandDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func commandRepeatedDigest(value byte) string {
	return hex.EncodeToString(bytes.Repeat([]byte{value}, sha256.Size))
}
