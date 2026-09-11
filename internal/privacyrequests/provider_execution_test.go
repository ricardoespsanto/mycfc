package privacyrequests

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

type fakeProviderAdapter struct {
	results []ProviderErasureResult
	errors  []error
	calls   int
}

type providerTargetProtectorFailure struct {
	sealTargetErr, digestTargetErr, sealCredentialErr, digestCredentialErr error
}

func (s providerTargetProtectorFailure) SealProviderTarget(ProviderTargetBinding, string, []byte) (ProviderTargetEnvelope, error) {
	return ProviderTargetEnvelope{Version: ProviderTargetEnvelopeVersion, Algorithm: providerTargetAlgorithm, KeyID: "provider-test-encryption",
		Encapsulation: bytes.Repeat([]byte{1}, 32), Nonce: bytes.Repeat([]byte{2}, 12), Ciphertext: bytes.Repeat([]byte{3}, 32)}, s.sealTargetErr
}
func (s providerTargetProtectorFailure) DigestProviderTarget(ProviderTargetBinding, string, []byte) (ProviderTargetDigest, error) {
	return ProviderTargetDigest{KeyID: "provider-test-digest", Digest: bytes.Repeat([]byte{4}, 32)}, s.digestTargetErr
}
func (s providerTargetProtectorFailure) SealProviderCredential(ProviderTargetBinding, string, []byte) (ProviderTargetEnvelope, error) {
	return ProviderTargetEnvelope{Version: ProviderTargetEnvelopeVersion, Algorithm: providerTargetAlgorithm, KeyID: "provider-test-encryption",
		Encapsulation: bytes.Repeat([]byte{5}, 32), Nonce: bytes.Repeat([]byte{6}, 12), Ciphertext: bytes.Repeat([]byte{7}, 32)}, s.sealCredentialErr
}
func (s providerTargetProtectorFailure) DigestProviderCredential(ProviderTargetBinding, string, []byte) (ProviderTargetDigest, error) {
	return ProviderTargetDigest{KeyID: "provider-test-credential-digest", Digest: bytes.Repeat([]byte{8}, 32)}, s.digestCredentialErr
}

func (f *fakeProviderAdapter) EraseOrNotify(_ context.Context, _ ProviderErasureRequest) (ProviderErasureResult, error) {
	index := f.calls
	f.calls++
	var result ProviderErasureResult
	if index < len(f.results) {
		result = f.results[index]
	}
	if index < len(f.errors) {
		return result, f.errors[index]
	}
	return result, nil
}

func providerRegistration(adapter ProviderErasureAdapter) ProviderExecutionRegistration {
	return ProviderExecutionRegistration{ServiceCode: "fake-provider", Role: ProviderRoleProcessor, ContractVersion: "fake-delete/v1",
		RegistryEvidenceKeyID: "registry-evidence-2026", RegistryEvidenceDigest: bytes.Repeat([]byte{7}, 32), Adapter: adapter}
}

func TestProviderRegistryIsClosedAndFailsWithoutFactualEvidence(t *testing.T) {
	empty, err := NewProviderExecutionRegistry()
	if err != nil || empty.Ready() {
		t.Fatalf("empty registry ready=%v err=%v", empty.Ready(), err)
	}
	invalid := providerRegistration(&fakeProviderAdapter{})
	invalid.RegistryEvidenceDigest = nil
	if _, err = NewProviderExecutionRegistry(invalid); !errors.Is(err, ErrProviderRegistryUnavailable) {
		t.Fatalf("invalid evidence error=%v", err)
	}
	valid := providerRegistration(&fakeProviderAdapter{})
	if registry, err := NewProviderExecutionRegistry(valid); err != nil || !registry.Ready() {
		t.Fatalf("valid registry ready=%v err=%v", registry != nil && registry.Ready(), err)
	}
	if _, err = NewProviderExecutionRegistry(valid, valid); !errors.Is(err, ErrProviderRegistryUnavailable) {
		t.Fatalf("duplicate registration error=%v", err)
	}
}

func TestProviderBoundaryHelpersFailClosedAndDoNotLeak(t *testing.T) {
	if RetryableProviderError(nil) != nil {
		t.Fatal("nil provider error became retryable")
	}
	retryable := RetryableProviderError(errors.New("provider secret"))
	if retryable == nil || retryable.Error() != "provider dependency unavailable" || !isRetryableProviderError(retryable) {
		t.Fatalf("retryable provider error=%v", retryable)
	}
	registry, err := NewProviderExecutionRegistry(providerRegistration(&fakeProviderAdapter{}))
	if err != nil {
		t.Fatal(err)
	}
	registration := providerRegistration(&fakeProviderAdapter{})
	if got, ok := registry.registration(registration.ServiceCode, registration.Role, registration.ContractVersion,
		registration.RegistryEvidenceKeyID, registration.RegistryEvidenceDigest); !ok || got.ServiceCode != registration.ServiceCode {
		t.Fatalf("registration lookup=%+v ok=%t", got, ok)
	}
	wrongDigest := bytes.Clone(registration.RegistryEvidenceDigest)
	wrongDigest[0] ^= 0xff
	if _, ok := registry.registration(registration.ServiceCode, registration.Role, registration.ContractVersion, registration.RegistryEvidenceKeyID, wrongDigest); ok {
		t.Fatal("registration accepted the wrong evidence digest")
	}
	if _, ok := (*ProviderExecutionRegistry)(nil).registration("service", ProviderRoleProcessor, "contract", "key", bytes.Repeat([]byte{1}, 32)); ok {
		t.Fatal("nil registry returned a registration")
	}
	if _, err = (*X25519ProviderTargetProtector)(nil).DigestProviderTarget(providerTargetTestBinding(), "source", []byte("value")); !errors.Is(err, ErrProviderCaptureFailed) {
		t.Fatalf("nil target protector error=%v", err)
	}
	if _, err = (*X25519ProviderTargetProtector)(nil).DigestProviderCredential(providerTargetTestBinding(), "source", []byte("value")); !errors.Is(err, ErrProviderCaptureFailed) {
		t.Fatalf("nil credential protector error=%v", err)
	}
	if _, err = digestProviderSecret(ProviderTargetBinding{}, "source", []byte("value"), "digest", bytes.Repeat([]byte{1}, 32), "domain", true); !errors.Is(err, ErrProviderCaptureFailed) {
		t.Fatalf("invalid digest binding error=%v", err)
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewX25519ProviderTargetProtector("same-key", private.PublicKey().Bytes(), "same-key",
		bytes.Repeat([]byte{1}, 32), "credential-key", bytes.Repeat([]byte{2}, 32)); !errors.Is(err, ErrProviderCaptureFailed) {
		t.Fatalf("shared key identifier error=%v", err)
	}
	if _, err = OpenProviderTargetEnvelope(nil, providerTargetTestBinding(), providerTargetLocatorVersion, ProviderTargetEnvelope{}); !errors.Is(err, ErrProviderExecutionFailed) {
		t.Fatalf("invalid envelope error=%v", err)
	}
}

func TestProviderOperationIsV2Only(t *testing.T) {
	plan := ExecutionPlan{ExecutorVersion: LegacyExecutorVersion, SchemaVersion: LegacyPlanSchemaVersion,
		Entries: []ExecutionPlanEntry{{Category: "external-provider", Purpose: "EXTERNAL_ERASURE", ActionVersion: SupportedActionVersion, Operations: []string{"PROVIDER_RECIPIENT_NOTIFY"}}}}
	if _, err := executionWorkGraph(plan); !errors.Is(err, ErrExecutorUnavailable) {
		t.Fatalf("legacy provider graph error=%v", err)
	}
	plan.ExecutorVersion, plan.SchemaVersion = SupportedExecutorVersion, SupportedPlanSchemaVersion
	if _, err := executionWorkGraph(plan); err != nil {
		t.Fatalf("v2 provider graph error=%v", err)
	}
	service := Service{ExecutionCapabilities: map[string]bool{"PROVIDER_RECIPIENT_NOTIFY": true}}
	if service.ExecutionCapabilitiesReady(plan) {
		t.Fatal("provider capability was ready without registry and target protection")
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	service.ProviderTargets, err = NewX25519ProviderTargetProtector("provider-public-2026", private.PublicKey().Bytes(),
		"provider-target-digest-2026", bytes.Repeat([]byte{8}, 32), "provider-credential-digest-2026", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	service.ProviderRegistry, err = NewProviderExecutionRegistry(providerRegistration(&fakeProviderAdapter{}))
	if err != nil || !service.ExecutionCapabilitiesReady(plan) {
		t.Fatalf("evidenced v2 provider capability ready=%v err=%v", service.ExecutionCapabilitiesReady(plan), err)
	}
}

func TestProviderTargetProtectionIsRandomizedBoundAndSeparatelyKeyed(t *testing.T) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewX25519ProviderTargetProtector("provider-public-2026", private.PublicKey().Bytes(),
		"provider-target-digest-2026", bytes.Repeat([]byte{8}, 32), "provider-credential-digest-2026", bytes.Repeat([]byte{8}, 32)); !errors.Is(err, ErrProviderCaptureFailed) {
		t.Fatalf("shared digest key accepted: %v", err)
	}
	protector, err := NewX25519ProviderTargetProtector("provider-public-2026", private.PublicKey().Bytes(),
		"provider-target-digest-2026", bytes.Repeat([]byte{8}, 32), "provider-credential-digest-2026", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	binding := providerTargetTestBinding()
	value := []byte("opaque-vault-target")
	first, err := protector.SealProviderTarget(binding, "source-target-v1", value)
	if err != nil {
		t.Fatal(err)
	}
	second, err := protector.SealProviderTarget(binding, "source-target-v1", value)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Ciphertext, second.Ciphertext) && bytes.Equal(first.Encapsulation, second.Encapsulation) {
		t.Fatal("target sealing was deterministic")
	}
	opened, err := OpenProviderTargetEnvelope(private.Bytes(), binding, providerTargetLocatorVersion, first)
	if err != nil || opened.KeyID() != "source-target-v1" || !bytes.Equal(opened.Bytes(), value) {
		t.Fatalf("opened=%q err=%v", opened.Bytes(), err)
	}
	credential, err := protector.SealProviderCredential(binding, "source-credential-v1", []byte("minimum-revocation-token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenProviderTargetEnvelope(private.Bytes(), binding, providerTargetLocatorVersion, credential); !errors.Is(err, ErrProviderExecutionFailed) {
		t.Fatalf("credential accepted as target: %v", err)
	}
	digest, err := protector.DigestProviderTarget(binding, "source-target-v1", value)
	if err != nil || len(digest.Digest) != 32 || digest.KeyID == first.KeyID {
		t.Fatalf("digest=%+v err=%v", digest, err)
	}
	credentialCommitment, err := protector.DigestProviderCredential(binding, "source-credential-v1", []byte("minimum-revocation-token"))
	if err != nil || len(credentialCommitment.Digest) != 32 || credentialCommitment.KeyID == digest.KeyID ||
		bytes.Equal(credentialCommitment.Digest, digest.Digest) {
		t.Fatalf("credential commitment was not separately keyed: %+v target=%+v err=%v", credentialCommitment, digest, err)
	}
	credentialBinding := binding
	credentialBinding.TargetID = uuid.New()
	changedCommitment, err := protector.DigestProviderCredential(credentialBinding, "source-credential-v1", []byte("minimum-revocation-token"))
	if err != nil || bytes.Equal(changedCommitment.Digest, credentialCommitment.Digest) {
		t.Fatal("credential commitment did not bind the target identity")
	}
	aliasBinding := binding
	aliasBinding.TargetID = uuid.New()
	alias, err := protector.DigestProviderTarget(aliasBinding, "source-target-v1", value)
	if err != nil || !bytes.Equal(alias.Digest, digest.Digest) {
		t.Fatal("same execution/service target did not produce a stable dedupe alias")
	}
	aliasBinding.ProviderContractVersion = "fake-delete/v2"
	changedAlias, err := protector.DigestProviderTarget(aliasBinding, "source-target-v1", value)
	if err != nil || bytes.Equal(changedAlias.Digest, digest.Digest) {
		t.Fatal("contract version was not bound into the target alias")
	}
	aliasBinding = binding
	aliasBinding.Category = "different-category"
	changedAlias, err = protector.DigestProviderTarget(aliasBinding, "source-target-v1", value)
	if err != nil || bytes.Equal(changedAlias.Digest, digest.Digest) {
		t.Fatal("category was not bound into the target alias")
	}
	tampered := binding
	tampered.TargetVersion++
	if _, err = OpenProviderTargetEnvelope(private.Bytes(), tampered, providerTargetLocatorVersion, first); !errors.Is(err, ErrProviderExecutionFailed) {
		t.Fatalf("binding tamper error=%v", err)
	}
}

func TestProviderAdapterRetriesAreBoundedAndAlreadyDisconnectedIsIdempotent(t *testing.T) {
	adapter := &fakeProviderAdapter{errors: []error{RetryableProviderError(errors.New("one")), RetryableProviderError(errors.New("two")), nil},
		results: []ProviderErasureResult{{}, {}, {Outcome: ProviderOutcomeAlreadyDisconnected, EvidenceCode: "REMOTE_ALREADY_DISCONNECTED", EvidenceReference: []byte("receipt")}}}
	result, attempts, err := executeProviderAdapter(context.Background(), adapter, ProviderErasureRequest{AlreadyDisconnected: true}, 3, time.Second)
	if err != nil || attempts != 3 || result.Outcome != ProviderOutcomeAlreadyDisconnected || !validProviderResult(ProviderRoleProcessor, result) {
		t.Fatalf("result=%+v attempts=%d err=%v", result, attempts, err)
	}
	exhausted := &fakeProviderAdapter{errors: []error{RetryableProviderError(errors.New("one")), RetryableProviderError(errors.New("two"))}}
	if _, attempts, err = executeProviderAdapter(context.Background(), exhausted, ProviderErasureRequest{}, 2, time.Second); err == nil || attempts != 2 {
		t.Fatalf("exhausted attempts=%d err=%v", attempts, err)
	}
}

func TestNotControllableIsAllowlistedOnlyForAutonomousRecipients(t *testing.T) {
	result := ProviderErasureResult{Outcome: ProviderOutcomeNotControllable, EvidenceCode: "RECIPIENT_NOTIFICATION_CONFIRMED",
		EvidenceReference: []byte("opaque-notification-reference"), RecipientRole: "AUTONOMOUS_RECIPIENT", ChannelCode: "PORTAL",
		NotificationCode: "DELIVERED", ReasonCode: "REGISTRY_DECLARED_AUTONOMOUS", GuidanceCode: "CONTACT_RECIPIENT"}
	if !validProviderResult(ProviderRoleAutonomousRecipient, result) {
		t.Fatal("valid autonomous result rejected")
	}
	if validProviderResult(ProviderRoleProcessor, result) {
		t.Fatal("processor accepted NOT_CONTROLLABLE")
	}
	for _, mutate := range []func(*ProviderErasureResult){
		func(v *ProviderErasureResult) { v.ChannelCode = "PHONE" },
		func(v *ProviderErasureResult) { v.ReasonCode = "FREE_TEXT" },
		func(v *ProviderErasureResult) { v.GuidanceCode = "IGNORE" },
		func(v *ProviderErasureResult) { v.EvidenceReference = nil },
	} {
		candidate := result
		mutate(&candidate)
		if validProviderResult(ProviderRoleAutonomousRecipient, candidate) {
			t.Fatalf("invalid result accepted: %+v", candidate)
		}
	}
}

func TestProviderEvidenceTranscriptContainsNoPlaintextAndBindsFence(t *testing.T) {
	binding := providerTargetTestBinding()
	lease := ExecutionLease{Job: ExecutionJob{PrivacyErasureCategoryJob: dbgen.PrivacyErasureCategoryJob{LeaseEpoch: 2}, ActiveAttemptID: uuid.New()}}
	originalAttemptID := lease.Job.ActiveAttemptID
	result := ProviderErasureResult{Outcome: ProviderOutcomeDeletionVerified, EvidenceCode: "REMOTE_DELETION_RECEIPT", EvidenceReference: []byte("provider-secret-receipt")}
	digest := providerEvidenceDigest("evidence-transcript", bytes.Repeat([]byte{3}, 32), binding, lease, result, 1, "credential-digest", bytes.Repeat([]byte{9}, 32))
	if len(digest) != 32 || bytes.Contains(digest, result.EvidenceReference) {
		t.Fatalf("unsafe evidence digest %x", digest)
	}
	lease.Job.ActiveAttemptID = uuid.New()
	if bytes.Equal(digest, providerEvidenceDigest("evidence-transcript", bytes.Repeat([]byte{3}, 32), binding, lease, result, 1, "credential-digest", bytes.Repeat([]byte{9}, 32))) {
		t.Fatal("attempt fence not bound")
	}
	lease.Job.ActiveAttemptID = originalAttemptID
	tampered := binding
	tampered.ActionVersion = "v2"
	if bytes.Equal(digest, providerEvidenceDigest("evidence-transcript", bytes.Repeat([]byte{3}, 32), tampered, lease, result, 1, "credential-digest", bytes.Repeat([]byte{9}, 32))) {
		t.Fatal("action version not bound")
	}
	if got := fmt.Sprintf("%v %#v", ProviderSecret{value: []byte("plain")}, ProviderSecret{value: []byte("plain")}); bytes.Contains([]byte(got), []byte("plain")) {
		t.Fatalf("secret formatting leaked: %s", got)
	}
}

func providerTargetTestBinding() ProviderTargetBinding {
	return ProviderTargetBinding{ExecutionID: uuid.New(), JobID: uuid.New(), CheckpointID: uuid.New(), TargetID: uuid.New(), PlanEntrySHA256: bytes.Repeat([]byte{1}, 32),
		Category: "external-provider", Service: "fake-provider", TargetKind: "REMOTE_ACCOUNT", Role: ProviderRoleProcessor, TargetVersion: 3,
		OperationCode: "PROVIDER_RECIPIENT_NOTIFY", ActionVersion: SupportedActionVersion, ProviderContractVersion: "fake-delete/v1"}
}

type providerEntropyFailure struct{}

func (providerEntropyFailure) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

type providerNonceEntropyFailure struct{}

func (*providerNonceEntropyFailure) Read(target []byte) (int, error) {
	if len(target) == 12 {
		return 0, errors.New("nonce entropy unavailable")
	}
	for index := range target {
		target[index] = 4
	}
	return len(target), nil
}

func TestProviderTargetProtectionFailsClosedAtCryptographicBoundaries(t *testing.T) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewX25519ProviderTargetProtector("provider-public-2026", []byte("invalid"),
		"provider-target-digest-2026", bytes.Repeat([]byte{8}, 32), "provider-credential-digest-2026", bytes.Repeat([]byte{9}, 32)); !errors.Is(err, ErrProviderCaptureFailed) {
		t.Fatalf("invalid public key error=%v", err)
	}
	protector, err := NewX25519ProviderTargetProtector("provider-public-2026", private.PublicKey().Bytes(),
		"provider-target-digest-2026", bytes.Repeat([]byte{8}, 32), "provider-credential-digest-2026", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	binding := providerTargetTestBinding()
	if _, err = (*X25519ProviderTargetProtector)(nil).SealProviderTarget(binding, "source-target-v1", []byte("target")); !errors.Is(err, ErrProviderCaptureFailed) {
		t.Fatalf("nil protector error=%v", err)
	}
	protector.random = providerEntropyFailure{}
	if _, err = protector.SealProviderTarget(binding, "source-target-v1", []byte("target")); !errors.Is(err, ErrProviderCaptureFailed) {
		t.Fatalf("ephemeral entropy error=%v", err)
	}
	protector.random = &providerNonceEntropyFailure{}
	if _, err = protector.SealProviderTarget(binding, "source-target-v1", []byte("target")); !errors.Is(err, ErrProviderCaptureFailed) {
		t.Fatalf("nonce entropy error=%v", err)
	}
	protector.random = rand.Reader
	envelope, err := protector.SealProviderTarget(binding, "source-target-v1", []byte("target"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenProviderTargetEnvelope([]byte("invalid"), binding, providerTargetLocatorVersion, envelope); !errors.Is(err, ErrProviderExecutionFailed) {
		t.Fatalf("invalid private key error=%v", err)
	}
	invalidEncapsulation := envelope
	invalidEncapsulation.Encapsulation = bytes.Repeat([]byte{1}, 31)
	if _, err = OpenProviderTargetEnvelope(private.Bytes(), binding, providerTargetLocatorVersion, invalidEncapsulation); !errors.Is(err, ErrProviderExecutionFailed) {
		t.Fatalf("invalid encapsulation error=%v", err)
	}
	lowOrderEncapsulation := envelope
	lowOrderEncapsulation.Encapsulation = make([]byte, 32)
	if _, err = OpenProviderTargetEnvelope(private.Bytes(), binding, providerTargetLocatorVersion, lowOrderEncapsulation); !errors.Is(err, ErrProviderExecutionFailed) {
		t.Fatalf("low-order encapsulation error=%v", err)
	}
}

func TestProviderWorkerConfigurationAndQueryFailuresAreBounded(t *testing.T) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewProviderExecutionRegistry(providerRegistration(&fakeProviderAdapter{}))
	if err != nil {
		t.Fatal(err)
	}
	transcriptKey := bytes.Repeat([]byte{3}, 32)
	worker := ProviderExecutionWorker{
		Pool: activationReadinessStore{}, Registry: registry, WorkerRef: uuid.New(), PrivateKey: private.Bytes(),
		TranscriptKeyID: "provider-transcript-2026", TranscriptKey: transcriptKey,
		CredentialDigestKeys: map[string][]byte{"credential-digest-2026": bytes.Repeat([]byte{4}, 32)},
		AdapterAttempts:      2, AdapterTimeout: 2 * time.Second,
	}
	if worker.attempts() != 2 || worker.timeout() != 2*time.Second || !worker.valid() {
		t.Fatal("valid explicit provider worker configuration rejected")
	}
	if validProviderDigestKeyring(nil, worker.TranscriptKeyID, worker.TranscriptKey) {
		t.Fatal("empty provider digest keyring accepted")
	}
	if validProviderDigestKeyring(map[string][]byte{worker.TranscriptKeyID: bytes.Clone(transcriptKey)}, worker.TranscriptKeyID, transcriptKey) {
		t.Fatal("shared transcript credential key accepted")
	}
	if optionalBytes(nil) != nil {
		t.Fatal("empty optional bytes were not nil")
	}
	lease := ExecutionLease{Job: ExecutionJob{PrivacyErasureCategoryJob: dbgen.PrivacyErasureCategoryJob{
		ID: uuid.New(), ExecutionID: uuid.New(), LeaseEpoch: 1, AttemptCount: 1,
	}, ActiveLeaseID: uuid.New(), ActiveAttemptID: uuid.New()}}
	if _, err = worker.CompleteCheckpoint(context.Background(), lease); !errors.Is(err, ErrProviderExecutionFailed) {
		t.Fatalf("query failure error=%v", err)
	}
}
