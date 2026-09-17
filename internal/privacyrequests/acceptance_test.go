package privacyrequests

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestAcceptanceEvidenceSignatureBindsExactPrivateSafeContract(t *testing.T) {
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	digest := strings.Repeat("a", 64)
	value := AcceptanceEvidence{Contract: AcceptanceContract, Mode: "run", Outcome: "COMPLETED", ImageDigest: "sha256:" + digest, SchemaDigest: digest, FixtureSHA256: digest, ManifestSHA256: digest, PolicySHA256: digest, StartedAt: now, ObservedAt: now, SimulatedNotices: 2, Checkpoints: 31, Conditions: []string{}}
	signed, err := SignAcceptanceEvidence(value, "test-key", key)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil {
		t.Fatal(err)
	}
	message := append([]byte(AcceptanceContract+"\x00test-key\x00"), signed.Payload...)
	if !ed25519.Verify(public, message, signature) {
		t.Fatal("invalid signature")
	}
	sha := sha256.Sum256(signed.Payload)
	if signed.PayloadSHA256 != hex.EncodeToString(sha[:]) {
		t.Fatal("digest mismatch")
	}
	message[len(message)-2] ^= 1
	if ed25519.Verify(public, message, signature) {
		t.Fatal("tampered payload verified")
	}
	encoded, _ := json.Marshal(signed)
	for _, private := range []string{"subject_ref", "request_id", "worker_ref", "password", "proof", "email", "database_url"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("evidence leaks field %s", private)
		}
	}
	for _, mutation := range []func(*AcceptanceEvidence){func(v *AcceptanceEvidence) { v.Conditions = []string{"person@example.test"} }, func(v *AcceptanceEvidence) { v.Outcome = "SUCCEEDED" }, func(v *AcceptanceEvidence) { v.ManifestSHA256 = "" }, func(v *AcceptanceEvidence) { v.Mode = "person-id" }, func(v *AcceptanceEvidence) { v.PolicySHA256 = "private text" }} {
		invalid := value
		mutation(&invalid)
		if _, err = SignAcceptanceEvidence(invalid, "test-key", key); err == nil {
			t.Fatal("invalid evidence signed")
		}
	}
	terminal := value
	terminal.Mode = "canary-failure"
	terminal.Outcome = "CANARY_VERIFIED"
	terminal.Conditions = []string{"failure"}
	terminal.ManifestSHA256 = ""
	terminal.SimulatedNotices = 0
	terminal.Checkpoints = 0
	if _, err = SignAcceptanceEvidence(terminal, "test-key", key); err != nil {
		t.Fatal(err)
	}
	terminal.Outcome = "COMPLETED"
	terminal.ManifestSHA256 = digest
	terminal.SimulatedNotices = 1
	terminal.Checkpoints = 1
	if _, err = SignAcceptanceEvidence(terminal, "test-key", key); err == nil {
		t.Fatal("terminal canary signed as completed acceptance")
	}
	malformed := append([]byte(nil), key...)
	malformed[63] ^= 1
	if _, err = SignAcceptanceEvidence(value, "test-key", malformed); err == nil {
		t.Fatal("inconsistent private key accepted")
	}
}
func TestAcceptanceFailClosedWithoutAnyIdentityInputs(t *testing.T) {
	for _, mode := range []string{"run", "canary-retry", "canary-failure", "canary-aged", "canary-heartbeat", "canary-recovery", "existing-person"} {
		if _, err := RunSyntheticAcceptance(context.Background(), AcceptanceOptions{Mode: mode}); err == nil {
			t.Fatal("unbound acceptance succeeded")
		}
	}
	if _, err := (acceptanceNoObjects{}).DeleteAllVersions(context.Background(), "real/member/key"); err == nil {
		t.Fatal("unexpected live object target allowed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := acceptanceWait(ctx, time.Hour); err == nil {
		t.Fatal("cancelled canary wait continued")
	}
	if acceptanceConditions("existing-person") != nil {
		t.Fatal("unsupported mode exposed acceptance conditions")
	}
	if _, err := acceptanceRuntime.providerCheckpoint(&acceptanceRun{}, t.Context(), ExecutionLease{}); !errors.Is(err, ErrProviderRegistryUnavailable) {
		t.Fatalf("default provider boundary error=%v", err)
	}
}

func TestAcceptanceCanaryFailClosedBranches(t *testing.T) {
	boundaryErr := errors.New("boundary unavailable")

	t.Run("binding", func(t *testing.T) {
		r, _ := acceptanceTestRun("canary-retry")
		ops := acceptanceTestOperations()
		ops.bound = func(*acceptanceRun, context.Context) error { return boundaryErr }
		installAcceptanceRuntime(t, ops)
		if err := r.canary(t.Context()); !errors.Is(err, boundaryErr) {
			t.Fatalf("error=%v", err)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*acceptanceRuntimeOperations)
	}{
		{"age query", func(ops *acceptanceRuntimeOperations) {
			ops.aged = func(*acceptanceRun, context.Context) (bool, error) { return false, boundaryErr }
		}},
		{"age rebinding", func(ops *acceptanceRuntimeOperations) {
			ops.aged = func(*acceptanceRun, context.Context) (bool, error) { return false, nil }
			ops.bound = sequenceAcceptanceErrors(nil, boundaryErr)
		}},
		{"age wait", func(ops *acceptanceRuntimeOperations) {
			ops.aged = func(*acceptanceRun, context.Context) (bool, error) { return false, nil }
			ops.wait = func(context.Context, time.Duration) error { return boundaryErr }
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, _ := acceptanceTestRun("canary-aged")
			ops := acceptanceTestOperations()
			test.mutate(&ops)
			installAcceptanceRuntime(t, ops)
			if err := r.canary(t.Context()); err == nil {
				t.Fatal("aged canary failure was ignored")
			}
		})
	}

	t.Run("aged observed", func(t *testing.T) {
		r, _ := acceptanceTestRun("canary-aged")
		ops := acceptanceTestOperations()
		installAcceptanceRuntime(t, ops)
		if err := r.canary(t.Context()); err != nil || !slicesEqualStrings(r.evidence.Conditions, []string{"aged"}) {
			t.Fatalf("conditions=%v error=%v", r.evidence.Conditions, err)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*acceptanceRuntimeOperations, *acceptanceRun, ExecutionLease)
	}{
		{"heartbeat claim", func(ops *acceptanceRuntimeOperations, _ *acceptanceRun, _ ExecutionLease) {
			ops.claim = func(*acceptanceRun, context.Context) (ExecutionLease, error) { return ExecutionLease{}, boundaryErr }
		}},
		{"heartbeat wait", func(ops *acceptanceRuntimeOperations, _ *acceptanceRun, _ ExecutionLease) {
			ops.wait = func(context.Context, time.Duration) error { return boundaryErr }
		}},
		{"heartbeat retained", func(ops *acceptanceRuntimeOperations, _ *acceptanceRun, _ ExecutionLease) {
			ops.heartbeat = func(*acceptanceRun, context.Context, ExecutionLease) (dbgen.PrivacyErasureJobLease, error) {
				return dbgen.PrivacyErasureJobLease{}, nil
			}
		}},
		{"recovery claim mismatch", func(ops *acceptanceRuntimeOperations, _ *acceptanceRun, lease ExecutionLease) {
			ops.claim = sequenceAcceptanceClaims(lease, ExecutionLease{Job: ExecutionJob{PrivacyErasureCategoryJob: dbgen.PrivacyErasureCategoryJob{ID: uuid.New(), LeaseEpoch: 2}}})
		}},
		{"recovery perform", func(ops *acceptanceRuntimeOperations, _ *acceptanceRun, lease ExecutionLease) {
			recovered := lease
			recovered.Job.LeaseEpoch++
			recovered.Job.ExecutionID = uuid.New()
			ops.claim = sequenceAcceptanceClaims(lease, recovered)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, lease := acceptanceTestRun("canary-heartbeat")
			ops := acceptanceTestOperations()
			ops.heartbeat = acceptanceLostHeartbeat
			test.mutate(&ops, r, lease)
			installAcceptanceRuntime(t, ops)
			if err := r.canary(t.Context()); err == nil {
				t.Fatal("heartbeat canary failure was ignored")
			}
		})
	}

	t.Run("heartbeat event", func(t *testing.T) {
		r, lease := acceptanceTestRun("canary-heartbeat")
		r.options.Event = func(context.Context, string) error { return boundaryErr }
		ops := acceptanceTestOperations()
		ops.heartbeat = acceptanceLostHeartbeat
		ops.claim = sequenceAcceptanceClaims(lease)
		installAcceptanceRuntime(t, ops)
		if err := r.canary(t.Context()); !errors.Is(err, boundaryErr) {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("heartbeat recovered", func(t *testing.T) {
		r, lease := acceptanceTestRun("canary-heartbeat")
		recovered := lease
		recovered.Job.LeaseEpoch++
		ops := acceptanceTestOperations()
		ops.heartbeat = acceptanceLostHeartbeat
		ops.claim = sequenceAcceptanceClaims(lease, recovered)
		installAcceptanceRuntime(t, ops)
		if err := r.canary(t.Context()); err != nil {
			t.Fatal(err)
		}
	})

	for _, test := range []struct {
		name   string
		mode   string
		mutate func(*acceptanceRuntimeOperations, *acceptanceRun, ExecutionLease)
	}{
		{"claim", "canary-retry", func(ops *acceptanceRuntimeOperations, _ *acceptanceRun, _ ExecutionLease) {
			ops.claim = func(*acceptanceRun, context.Context) (ExecutionLease, error) { return ExecutionLease{}, boundaryErr }
		}},
		{"fail", "canary-retry", func(ops *acceptanceRuntimeOperations, _ *acceptanceRun, lease ExecutionLease) {
			ops.claim = sequenceAcceptanceClaims(lease)
			ops.failJob = func(*acceptanceRun, context.Context, ExecutionLease, ExecutionFailure) (dbgen.PrivacyErasureExecution, error) {
				return dbgen.PrivacyErasureExecution{}, boundaryErr
			}
		}},
		{"terminal status", "canary-failure", func(ops *acceptanceRuntimeOperations, _ *acceptanceRun, lease ExecutionLease) {
			ops.claim = sequenceAcceptanceClaims(lease)
			ops.failJob = func(*acceptanceRun, context.Context, ExecutionLease, ExecutionFailure) (dbgen.PrivacyErasureExecution, error) {
				return dbgen.PrivacyErasureExecution{Status: "RETRY_WAIT"}, nil
			}
		}},
		{"retry status", "canary-retry", func(ops *acceptanceRuntimeOperations, _ *acceptanceRun, lease ExecutionLease) {
			ops.claim = sequenceAcceptanceClaims(lease)
			ops.retryStatus = func(*acceptanceRun, context.Context, ExecutionLease) (string, error) { return "PENDING", nil }
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, lease := acceptanceTestRun(test.mode)
			ops := acceptanceTestOperations()
			test.mutate(&ops, r, lease)
			installAcceptanceRuntime(t, ops)
			if err := r.canary(t.Context()); err == nil {
				t.Fatal("failure/retry canary boundary was ignored")
			}
		})
	}

	t.Run("terminal event", func(t *testing.T) {
		r, lease := acceptanceTestRun("canary-failure")
		r.options.Event = func(context.Context, string) error { return boundaryErr }
		ops := acceptanceTestOperations()
		ops.claim = sequenceAcceptanceClaims(lease)
		installAcceptanceRuntime(t, ops)
		if err := r.canary(t.Context()); !errors.Is(err, boundaryErr) {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("retry observed", func(t *testing.T) {
		r, lease := acceptanceTestRun("canary-retry")
		ops := acceptanceTestOperations()
		ops.claim = sequenceAcceptanceClaims(lease)
		installAcceptanceRuntime(t, ops)
		if err := r.canary(t.Context()); err != nil || !slicesEqualStrings(r.evidence.Conditions, []string{"retry"}) {
			t.Fatalf("conditions=%v error=%v", r.evidence.Conditions, err)
		}
	})
}

func TestAcceptancePerformDispatchesAndFailsClosed(t *testing.T) {
	boundaryErr := errors.New("operation unavailable")
	r, lease := acceptanceTestRun("run")
	bad := lease
	bad.Job.ExecutionID = uuid.New()
	if err := r.perform(t.Context(), bad); !errors.Is(err, ErrAcceptance) {
		t.Fatalf("mismatched lease error=%v", err)
	}

	operations := []string{"OBJECT_VERSION_DELETE", "PROVIDER_RECIPIENT_NOTIFY", "BACKUP_TOMBSTONE_REPLAY", "IDENTITY_CLEAR"}
	for _, operation := range operations {
		lease.Checkpoints = append(lease.Checkpoints, dbgen.PrivacyErasureJobCheckpoint{OperationCode: operation, ActionVersion: SupportedActionVersion})
	}
	ops := acceptanceTestOperations()
	ops.heartbeat = func(*acceptanceRun, context.Context, ExecutionLease) (dbgen.PrivacyErasureJobLease, error) {
		return dbgen.PrivacyErasureJobLease{}, nil
	}
	installAcceptanceRuntime(t, ops)
	if err := r.perform(t.Context(), lease); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		lease  ExecutionLease
		mutate func(*acceptanceRuntimeOperations)
	}{
		{"binding", leaseWithCheckpoint(lease, "IDENTITY_CLEAR", ""), func(ops *acceptanceRuntimeOperations) {
			ops.bound = func(*acceptanceRun, context.Context) error { return boundaryErr }
		}},
		{"heartbeat", leaseWithCheckpoint(lease, "IDENTITY_CLEAR", ""), func(ops *acceptanceRuntimeOperations) {
			ops.heartbeat = func(*acceptanceRun, context.Context, ExecutionLease) (dbgen.PrivacyErasureJobLease, error) {
				return dbgen.PrivacyErasureJobLease{}, boundaryErr
			}
		}},
		{"checkpoint", leaseWithCheckpoint(lease, "IDENTITY_CLEAR", ""), func(ops *acceptanceRuntimeOperations) {
			ops.ordinaryCheckpoint = func(*acceptanceRun, context.Context, ExecutionLease, dbgen.PrivacyErasureJobCheckpoint) (dbgen.PrivacyErasureJobCheckpoint, error) {
				return dbgen.PrivacyErasureJobCheckpoint{}, boundaryErr
			}
		}},
		{"final binding", leaseWithCheckpoint(lease, "IDENTITY_CLEAR", "SUCCEEDED"), func(ops *acceptanceRuntimeOperations) {
			ops.bound = func(*acceptanceRun, context.Context) error { return boundaryErr }
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caseOps := acceptanceTestOperations()
			test.mutate(&caseOps)
			installAcceptanceRuntime(t, caseOps)
			if err := r.perform(t.Context(), test.lease); err == nil {
				t.Fatal("perform boundary failure was ignored")
			}
		})
	}
}

func TestAcceptanceCompleteFailsClosedAtEveryBoundary(t *testing.T) {
	boundaryErr := errors.New("completion unavailable")
	tests := []struct {
		name   string
		mutate func(*acceptanceRuntimeOperations, ExecutionLease)
	}{
		{"binding", func(ops *acceptanceRuntimeOperations, _ ExecutionLease) {
			ops.bound = func(*acceptanceRun, context.Context) error { return boundaryErr }
		}},
		{"status query", func(ops *acceptanceRuntimeOperations, _ ExecutionLease) {
			ops.claim = func(*acceptanceRun, context.Context) (ExecutionLease, error) { return ExecutionLease{}, pgx.ErrNoRows }
			ops.executionStatus = func(*acceptanceRun, context.Context) (string, error) { return "", boundaryErr }
		}},
		{"terminal status", func(ops *acceptanceRuntimeOperations, _ ExecutionLease) {
			ops.claim = func(*acceptanceRun, context.Context) (ExecutionLease, error) { return ExecutionLease{}, pgx.ErrNoRows }
			ops.executionStatus = func(*acceptanceRun, context.Context) (string, error) { return "TERMINAL_FAILED", nil }
		}},
		{"wait", func(ops *acceptanceRuntimeOperations, _ ExecutionLease) {
			ops.claim = func(*acceptanceRun, context.Context) (ExecutionLease, error) { return ExecutionLease{}, pgx.ErrNoRows }
			ops.executionStatus = func(*acceptanceRun, context.Context) (string, error) { return "RUNNING", nil }
			ops.wait = func(context.Context, time.Duration) error { return boundaryErr }
		}},
		{"claim", func(ops *acceptanceRuntimeOperations, _ ExecutionLease) {
			ops.claim = func(*acceptanceRun, context.Context) (ExecutionLease, error) { return ExecutionLease{}, boundaryErr }
		}},
		{"perform", func(ops *acceptanceRuntimeOperations, lease ExecutionLease) {
			lease.Job.ExecutionID = uuid.New()
			ops.claim = sequenceAcceptanceClaims(lease)
		}},
		{"post-loop binding", func(ops *acceptanceRuntimeOperations, _ ExecutionLease) {
			ops.claim = func(*acceptanceRun, context.Context) (ExecutionLease, error) { return ExecutionLease{}, pgx.ErrNoRows }
			ops.bound = sequenceAcceptanceErrors(nil, boundaryErr)
		}},
		{"closure export", func(ops *acceptanceRuntimeOperations, _ ExecutionLease) {
			ops.claim = func(*acceptanceRun, context.Context) (ExecutionLease, error) { return ExecutionLease{}, pgx.ErrNoRows }
			ops.exportClosure = func(*acceptanceRun, context.Context) error { return boundaryErr }
		}},
		{"post-export binding", func(ops *acceptanceRuntimeOperations, _ ExecutionLease) {
			ops.claim = func(*acceptanceRun, context.Context) (ExecutionLease, error) { return ExecutionLease{}, pgx.ErrNoRows }
			ops.bound = sequenceAcceptanceErrors(nil, nil, boundaryErr)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r, lease := acceptanceTestRun("run")
			ops := acceptanceTestOperations()
			test.mutate(&ops, lease)
			installAcceptanceRuntime(t, ops)
			if err := r.complete(t.Context()); err == nil {
				t.Fatal("completion boundary failure was ignored")
			}
		})
	}

	t.Run("success", func(t *testing.T) {
		r, _ := acceptanceTestRun("run")
		ops := acceptanceTestOperations()
		ops.claim = func(*acceptanceRun, context.Context) (ExecutionLease, error) { return ExecutionLease{}, pgx.ErrNoRows }
		installAcceptanceRuntime(t, ops)
		if err := r.complete(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func acceptanceTestRun(mode string) (*acceptanceRun, ExecutionLease) {
	executionID, workerID, jobID := uuid.New(), uuid.New(), uuid.New()
	r := &acceptanceRun{options: AcceptanceOptions{Mode: mode}, execution: executionID, fixture: acceptanceFixture{worker: workerID}}
	lease := ExecutionLease{Job: ExecutionJob{PrivacyErasureCategoryJob: dbgen.PrivacyErasureCategoryJob{ID: jobID, ExecutionID: executionID, LeaseEpoch: 1}, WorkerRef: workerID}}
	return r, lease
}

func acceptanceTestOperations() acceptanceRuntimeOperations {
	return acceptanceRuntimeOperations{
		bound: func(*acceptanceRun, context.Context) error { return nil },
		wait:  func(context.Context, time.Duration) error { return nil },
		aged:  func(*acceptanceRun, context.Context) (bool, error) { return true, nil },
		claim: func(r *acceptanceRun, _ context.Context) (ExecutionLease, error) {
			_, lease := acceptanceTestRun(r.options.Mode)
			lease.Job.ExecutionID = r.execution
			lease.Job.WorkerRef = r.fixture.worker
			return lease, nil
		},
		heartbeat: func(*acceptanceRun, context.Context, ExecutionLease) (dbgen.PrivacyErasureJobLease, error) {
			return dbgen.PrivacyErasureJobLease{}, nil
		},
		failJob: func(*acceptanceRun, context.Context, ExecutionLease, ExecutionFailure) (dbgen.PrivacyErasureExecution, error) {
			return dbgen.PrivacyErasureExecution{Status: "TERMINAL_FAILED"}, nil
		},
		retryStatus:     func(*acceptanceRun, context.Context, ExecutionLease) (string, error) { return "RETRY_WAIT", nil },
		executionStatus: func(*acceptanceRun, context.Context) (string, error) { return "SUCCEEDED", nil },
		objectCheckpoint: func(*acceptanceRun, context.Context, ExecutionLease) (dbgen.PrivacyErasureJobCheckpoint, error) {
			return dbgen.PrivacyErasureJobCheckpoint{}, nil
		},
		providerCheckpoint: func(*acceptanceRun, context.Context, ExecutionLease) (dbgen.PrivacyErasureJobCheckpoint, error) {
			return dbgen.PrivacyErasureJobCheckpoint{}, nil
		},
		tombstoneCheckpoint: func(*acceptanceRun, context.Context, ExecutionLease) error { return nil },
		ordinaryCheckpoint: func(*acceptanceRun, context.Context, ExecutionLease, dbgen.PrivacyErasureJobCheckpoint) (dbgen.PrivacyErasureJobCheckpoint, error) {
			return dbgen.PrivacyErasureJobCheckpoint{}, nil
		},
		completeJob: func(*acceptanceRun, context.Context, ExecutionLease) (dbgen.PrivacyErasureExecution, error) {
			return dbgen.PrivacyErasureExecution{Status: "SUCCEEDED"}, nil
		},
		exportClosure:     func(*acceptanceRun, context.Context) error { return nil },
		completeExecution: func(*acceptanceRun, context.Context) (CompletionResult, error) { return CompletionResult{}, nil },
	}
}

func acceptanceLostHeartbeat(*acceptanceRun, context.Context, ExecutionLease) (dbgen.PrivacyErasureJobLease, error) {
	return dbgen.PrivacyErasureJobLease{}, ErrLeaseLost
}

func installAcceptanceRuntime(t *testing.T, operations acceptanceRuntimeOperations) {
	t.Helper()
	original := acceptanceRuntime
	acceptanceRuntime = operations
	t.Cleanup(func() { acceptanceRuntime = original })
}

func sequenceAcceptanceClaims(leases ...ExecutionLease) func(*acceptanceRun, context.Context) (ExecutionLease, error) {
	index := 0
	return func(*acceptanceRun, context.Context) (ExecutionLease, error) {
		if index >= len(leases) {
			return ExecutionLease{}, errors.New("unexpected claim")
		}
		lease := leases[index]
		index++
		return lease, nil
	}
}

func sequenceAcceptanceErrors(values ...error) func(*acceptanceRun, context.Context) error {
	index := 0
	return func(*acceptanceRun, context.Context) error {
		if index >= len(values) {
			return errors.New("unexpected binding")
		}
		value := values[index]
		index++
		return value
	}
}

func leaseWithCheckpoint(lease ExecutionLease, operation, status string) ExecutionLease {
	lease.Checkpoints = []dbgen.PrivacyErasureJobCheckpoint{{OperationCode: operation, ActionVersion: SupportedActionVersion, Status: status}}
	return lease
}

func slicesEqualStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
