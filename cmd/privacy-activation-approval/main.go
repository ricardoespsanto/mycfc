// privacy-activation-approval performs only public-key verification and KMS
// signing preparation. It has no database mode and accepts no private key.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
)

const maximumProtectedInputBytes = privacyrequests.MaximumActivationApprovalBytes

type commonOptions struct {
	materialPath                  string
	registryPath                  string
	registrySHA256                string
	expectedCeremonyID            string
	expectedSourceSHA             string
	expectedPolicyVersion         string
	expectedImageDigest           string
	expectedSchemaMigrationDigest string
}

type verifiedInputs struct {
	materialRaw []byte
	material    privacyrequests.ActivationApprovalMaterial
	registry    privacyrequests.ActivationSignerRegistry
}

var currentTime = func() time.Time { return time.Now().UTC() }

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "privacy_activation_approval_failed")
		os.Exit(1)
	}
}

func run(arguments []string, output io.Writer) error {
	if len(arguments) == 0 {
		return errors.New("privacy activation approval mode required")
	}
	switch arguments[0] {
	case "material-verify":
		return runMaterialVerify(arguments[1:], output)
	case "approval-unsigned":
		return runApprovalUnsigned(arguments[1:], output)
	case "approval-assemble":
		return runApprovalAssemble(arguments[1:], output)
	case "approval-verify":
		return runApprovalVerify(arguments[1:], output)
	case "bundle-verify":
		return runBundleVerify(arguments[1:], output)
	default:
		return errors.New("privacy activation approval mode rejected")
	}
}

func runMaterialVerify(arguments []string, output io.Writer) error {
	set, common := newCommonFlags("material-verify")
	if err := parseFlags(set, arguments); err != nil {
		return err
	}
	if _, err := common.load(currentTime()); err != nil {
		return err
	}
	_, err := fmt.Fprintln(output, "privacy_activation_material_verified")
	return err
}

func runApprovalUnsigned(arguments []string, output io.Writer) error {
	set, common := newCommonFlags("approval-unsigned")
	role := set.String("role", "", "fixed signer role")
	githubActorID := set.Uint64("github-actor-id", 0, "numeric GitHub actor ID")
	githubRunID := set.Uint64("github-run-id", 0, "numeric GitHub run ID")
	githubRunAttempt := set.Uint64("github-run-attempt", 0, "numeric GitHub run attempt")
	outputPath := set.String("output", "", "exclusive canonical unsigned output")
	digestOutputPath := set.String("digest-output", "", "exclusive raw SHA-256 digest output")
	if err := parseFlags(set, arguments); err != nil {
		return err
	}
	inputs, err := common.load(currentTime())
	if err != nil {
		return err
	}
	_, raw, digest, err := privacyrequests.NewActivationApprovalUnsigned(inputs.materialRaw, inputs.material, inputs.registry,
		strings.TrimSpace(*role), *githubActorID, *githubRunID, *githubRunAttempt, currentTime())
	if err != nil {
		return err
	}
	if err = writeExclusive(*outputPath, raw); err != nil {
		return err
	}
	if err = writeExclusive(*digestOutputPath, digest[:]); err != nil {
		_ = os.Remove(strings.TrimSpace(*outputPath))
		return err
	}
	_, err = fmt.Fprintf(output, "privacy_activation_unsigned_sha256=%x\n", digest)
	return err
}

func runApprovalAssemble(arguments []string, output io.Writer) error {
	set, common := newCommonFlags("approval-assemble")
	role := set.String("role", "", "fixed signer role")
	unsignedPath := set.String("unsigned", "", "canonical unsigned approval")
	signaturePath := set.String("signature", "", "base64 KMS DER signature")
	publicKeyPath := set.String("public-key-spki", "", "DER SubjectPublicKeyInfo")
	outputPath := set.String("output", "", "exclusive canonical approval output")
	if err := parseFlags(set, arguments); err != nil {
		return err
	}
	inputs, err := common.load(currentTime())
	if err != nil {
		return err
	}
	unsigned, err := readProtected(*unsignedPath, maximumProtectedInputBytes)
	if err != nil {
		return err
	}
	signatureEncoded, err := readProtected(*signaturePath, 1024)
	if err != nil {
		return err
	}
	signature, err := privacyrequests.DecodeActivationSignatureBase64(string(signatureEncoded))
	if err != nil {
		return err
	}
	publicKey, err := readProtected(*publicKeyPath, 1024)
	if err != nil {
		return err
	}
	raw, _, err := privacyrequests.AssembleActivationApproval(unsigned, signature, publicKey, inputs.materialRaw,
		inputs.material, inputs.registry, strings.TrimSpace(*role), currentTime())
	if err != nil {
		return err
	}
	if err = writeExclusive(*outputPath, raw); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "privacy_activation_approval_assembled")
	return err
}

func runApprovalVerify(arguments []string, output io.Writer) error {
	set, common := newCommonFlags("approval-verify")
	role := set.String("role", "", "fixed signer role")
	approvalPath := set.String("approval", "", "canonical signed approval")
	publicKeyPath := set.String("public-key-spki", "", "DER SubjectPublicKeyInfo")
	if err := parseFlags(set, arguments); err != nil {
		return err
	}
	inputs, err := common.load(currentTime())
	if err != nil {
		return err
	}
	approval, err := readProtected(*approvalPath, maximumProtectedInputBytes)
	if err != nil {
		return err
	}
	publicKey, err := readProtected(*publicKeyPath, 1024)
	if err != nil {
		return err
	}
	if _, _, err = privacyrequests.VerifyActivationApproval(approval, inputs.materialRaw, inputs.material, inputs.registry,
		strings.TrimSpace(*role), publicKey, currentTime()); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "privacy_activation_approval_verified")
	return err
}

func runBundleVerify(arguments []string, output io.Writer) error {
	set, common := newCommonFlags("bundle-verify")
	executorApprovalPath := set.String("executor-approval", "", "canonical executor approval")
	administratorApprovalPath := set.String("administrator-approval", "", "canonical administrator approval")
	executorPublicKeyPath := set.String("executor-public-key-spki", "", "executor DER SubjectPublicKeyInfo")
	administratorPublicKeyPath := set.String("administrator-public-key-spki", "", "administrator DER SubjectPublicKeyInfo")
	if err := parseFlags(set, arguments); err != nil {
		return err
	}
	inputs, err := common.load(currentTime())
	if err != nil {
		return err
	}
	executorApproval, err := readProtected(*executorApprovalPath, maximumProtectedInputBytes)
	if err != nil {
		return err
	}
	administratorApproval, err := readProtected(*administratorApprovalPath, maximumProtectedInputBytes)
	if err != nil {
		return err
	}
	executorPublicKey, err := readProtected(*executorPublicKeyPath, 1024)
	if err != nil {
		return err
	}
	administratorPublicKey, err := readProtected(*administratorPublicKeyPath, 1024)
	if err != nil {
		return err
	}
	if _, err = privacyrequests.VerifyActivationApprovalBundle(inputs.materialRaw, inputs.material, inputs.registry,
		executorApproval, administratorApproval, executorPublicKey, administratorPublicKey, currentTime()); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "privacy_activation_approval_bundle_verified")
	return err
}

func newCommonFlags(name string) (*flag.FlagSet, *commonOptions) {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	options := &commonOptions{}
	set.StringVar(&options.materialPath, "material", "", "canonical approval material")
	set.StringVar(&options.registryPath, "registry", "", "canonical signer registry")
	set.StringVar(&options.registrySHA256, "registry-sha256", "", "pinned signer registry SHA-256")
	set.StringVar(&options.expectedCeremonyID, "expected-ceremony-id", "", "expected ceremony UUID")
	set.StringVar(&options.expectedSourceSHA, "expected-source-sha", "", "expected release source SHA")
	set.StringVar(&options.expectedPolicyVersion, "expected-policy-version", "", "expected policy version")
	set.StringVar(&options.expectedImageDigest, "expected-image-digest", "", "expected immutable image digest")
	set.StringVar(&options.expectedSchemaMigrationDigest, "expected-schema-migration-digest", "", "expected schema migration digest")
	return set, options
}

func parseFlags(set *flag.FlagSet, arguments []string) error {
	if err := set.Parse(arguments); err != nil || set.NArg() != 0 {
		return errors.New("privacy activation approval arguments rejected")
	}
	return nil
}

func (o commonOptions) load(now time.Time) (verifiedInputs, error) {
	ceremonyID, err := uuid.Parse(strings.TrimSpace(o.expectedCeremonyID))
	if err != nil || ceremonyID == uuid.Nil {
		return verifiedInputs{}, errors.New("privacy activation expected ceremony rejected")
	}
	registryRaw, err := readProtected(o.registryPath, maximumProtectedInputBytes)
	if err != nil {
		return verifiedInputs{}, err
	}
	registry, err := privacyrequests.ParseActivationSignerRegistry(registryRaw, strings.TrimSpace(o.registrySHA256))
	if err != nil {
		return verifiedInputs{}, err
	}
	materialRaw, err := readProtected(o.materialPath, maximumProtectedInputBytes)
	if err != nil {
		return verifiedInputs{}, err
	}
	material, err := privacyrequests.VerifyActivationApprovalMaterial(materialRaw, privacyrequests.ActivationMaterialExpectation{
		CeremonyID: ceremonyID, SourceSHA: strings.TrimSpace(o.expectedSourceSHA),
		PolicyVersion: strings.TrimSpace(o.expectedPolicyVersion), ImageDigest: strings.TrimSpace(o.expectedImageDigest),
		SchemaMigrationDigest: strings.TrimSpace(o.expectedSchemaMigrationDigest),
		SignerRegistrySHA256:  strings.TrimSpace(o.registrySHA256),
	}, now)
	if err != nil {
		return verifiedInputs{}, err
	}
	return verifiedInputs{materialRaw: materialRaw, material: material, registry: registry}, nil
}

func readProtected(path string, maximum int64) ([]byte, error) {
	path = strings.TrimSpace(path)
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || (before.Mode().Perm() != 0o600 && before.Mode().Perm() != 0o400) {
		return nil, errors.New("privacy activation protected input rejected")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("privacy activation protected input unavailable")
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) ||
		(after.Mode().Perm() != 0o600 && after.Mode().Perm() != 0o400) {
		return nil, errors.New("privacy activation protected input rejected")
	}
	stat, ok := after.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() || stat.Nlink != 1 {
		return nil, errors.New("privacy activation protected input rejected")
	}
	payload, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || len(payload) == 0 || int64(len(payload)) > maximum {
		return nil, errors.New("privacy activation protected input rejected")
	}
	final, err := file.Stat()
	if err != nil {
		return nil, errors.New("privacy activation protected input changed while reading")
	}
	finalStat, finalOK := final.Sys().(*syscall.Stat_t)
	if !finalOK || !os.SameFile(after, final) || after.Size() != final.Size() ||
		after.Mode() != final.Mode() || !after.ModTime().Equal(final.ModTime()) || stat.Ctim != finalStat.Ctim {
		return nil, errors.New("privacy activation protected input changed while reading")
	}
	return payload, nil
}

func writeExclusive(path string, payload []byte) error {
	path = strings.TrimSpace(path)
	if path == "" || len(payload) == 0 {
		return errors.New("privacy activation output rejected")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return errors.New("privacy activation output unavailable")
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err = file.Write(payload); err != nil || file.Sync() != nil {
		return errors.New("privacy activation output unavailable")
	}
	remove = false
	return nil
}
