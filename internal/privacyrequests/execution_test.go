package privacyrequests

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func executionPlanFixture(t *testing.T) ExecutionPlan {
	t.Helper()
	policy := testPolicy()
	at := time.Date(2026, time.September, 8, 10, 0, 0, 0, time.UTC)
	_, plan, err := policy.DecisionPlan(
		Scope{Kind: Categories, Categories: []Category{"identity-core", "membership-history"}},
		map[string]CategoryDecision{
			"identity-core":      {Outcome: "APPROVE"},
			"membership-history": {Outcome: "APPROVE"},
		},
		"approve",
		at,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan.RequestVersion = 7
	return plan
}

func TestExecutionWorkGraphExactlyMirrorsImmutablePlan(t *testing.T) {
	plan := executionPlanFixture(t)
	work, err := executionWorkGraph(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(work) != len(plan.Entries) {
		t.Fatalf("jobs=%d entries=%d", len(work), len(plan.Entries))
	}
	for entryIndex, entry := range plan.Entries {
		job := work[entryIndex]
		if job.Position != int16(entryIndex+1) || job.Category != entry.Category || job.Purpose != entry.Purpose {
			t.Fatalf("job %d did not preserve plan position and codes: %+v", entryIndex, job)
		}
		canonical, marshalErr := json.Marshal(entry)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		digest := sha256.Sum256(canonical)
		if !slices.Equal(job.EntryDigest, digest[:]) {
			t.Fatalf("job %d digest did not bind the complete entry", entryIndex)
		}
		if len(job.Operations) != len(entry.Operations) {
			t.Fatalf("job %d checkpoints=%d operations=%d", entryIndex, len(job.Operations), len(entry.Operations))
		}
		for operationIndex, operation := range entry.Operations {
			checkpoint := job.Operations[operationIndex]
			if checkpoint.Position != int16(operationIndex+1) || checkpoint.Code != operation || checkpoint.ActionVersion != entry.ActionVersion {
				t.Fatalf("checkpoint %d/%d did not preserve the allowlisted operation: %+v", entryIndex, operationIndex, checkpoint)
			}
		}
	}
}

func TestExecutionWorkGraphFailsClosed(t *testing.T) {
	base := executionPlanFixture(t)
	for _, test := range []struct {
		name   string
		mutate func(*ExecutionPlan)
	}{
		{name: "no entries", mutate: func(plan *ExecutionPlan) { plan.Entries = nil }},
		{name: "duplicate category", mutate: func(plan *ExecutionPlan) { plan.Entries[1].Category = plan.Entries[0].Category }},
		{name: "unknown operation", mutate: func(plan *ExecutionPlan) { plan.Entries[0].Operations[0] = "FUTURE_UNREGISTERED_ACTION" }},
		{name: "duplicate operation", mutate: func(plan *ExecutionPlan) {
			plan.Entries[0].Operations = append(plan.Entries[0].Operations, plan.Entries[0].Operations[0])
		}},
		{name: "wrong action version", mutate: func(plan *ExecutionPlan) { plan.Entries[0].ActionVersion = "v2" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.Entries = append([]ExecutionPlanEntry(nil), base.Entries...)
			for index := range candidate.Entries {
				candidate.Entries[index].Operations = slices.Clone(base.Entries[index].Operations)
			}
			test.mutate(&candidate)
			if _, err := executionWorkGraph(candidate); !errors.Is(err, ErrExecutorUnavailable) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestExecutionPersistenceHelpersRejectInvalidPlansBeforeDatabaseAccess(t *testing.T) {
	if _, _, err := createExecutionWorkGraph(context.Background(), nil, uuid.New(), ExecutionPlan{}, time.Now()); !errors.Is(err, ErrExecutorUnavailable) {
		t.Fatalf("invalid creation plan error=%v", err)
	}
	valid, err := exactPersistedExecutionGraph(context.Background(), nil, uuid.New(), ExecutionPlan{})
	if err != nil || valid {
		t.Fatalf("invalid persisted plan valid=%v err=%v", valid, err)
	}
}

func TestExecutionReplayRequiresExactAcceptedHandoff(t *testing.T) {
	requestID, actorRef := uuid.New(), uuid.New()
	digest := sha256.Sum256([]byte("plan"))
	plan := dbgen.PrivacyRequestExecutionPlan{
		RequestID: requestID, PlanSha256: digest[:], ExecutorVersion: SupportedExecutorVersion,
		SchemaVersion: SupportedPlanSchemaVersion,
	}
	in := StartInput{ActorID: uuid.New(), Reference: uuid.New(), Version: 4, Confirmed: true}
	existing := dbgen.PrivacyErasureExecution{
		RequestID: requestID, PlanSha256: slices.Clone(digest[:]), ExecutorVersion: SupportedExecutorVersion,
		SchemaVersion: SupportedPlanSchemaVersion, RequestVersionAtStart: in.Version, StartedByRef: actorRef,
	}
	if !exactExecutionReplay(existing, plan, in, actorRef) {
		t.Fatal("exact replay was not idempotent")
	}
	for _, mutate := range []func(*dbgen.PrivacyErasureExecution){
		func(row *dbgen.PrivacyErasureExecution) { row.RequestVersionAtStart++ },
		func(row *dbgen.PrivacyErasureExecution) { row.StartedByRef = uuid.New() },
		func(row *dbgen.PrivacyErasureExecution) { row.PlanSha256[0] ^= 0xff },
		func(row *dbgen.PrivacyErasureExecution) { row.ExecutorVersion = "future" },
		func(row *dbgen.PrivacyErasureExecution) { row.SchemaVersion = "future" },
	} {
		candidate := existing
		candidate.PlanSha256 = slices.Clone(existing.PlanSha256)
		mutate(&candidate)
		if exactExecutionReplay(candidate, plan, in, actorRef) {
			t.Fatalf("divergent replay accepted: %+v", candidate)
		}
	}
}

func TestExecutionActivationDoesNotStrandAnOlderImmutablePlan(t *testing.T) {
	if !executionActivationReady(dbgen.PrivacyRequestActivation{PolicyVersion: "new-policy", Enabled: true, FulfilmentReady: true}) {
		t.Fatal("ready activation rejected because its current policy differs from the stored request plan")
	}
	for _, activation := range []dbgen.PrivacyRequestActivation{
		{Enabled: false, FulfilmentReady: true},
		{Enabled: true, FulfilmentReady: false},
	} {
		if executionActivationReady(activation) {
			t.Fatalf("unready activation accepted: %+v", activation)
		}
	}
}

func TestExecutionCapabilitiesMustCoverEveryExactPlanOperation(t *testing.T) {
	plan := executionPlanFixture(t)
	if (Service{}).ExecutionCapabilitiesReady(plan) {
		t.Fatal("nil capability registry enabled execution")
	}

	all := make(map[string]bool)
	for _, entry := range plan.Entries {
		for _, operation := range entry.Operations {
			all[operation] = true
		}
	}
	partial := make(map[string]bool, len(all))
	for operation := range all {
		partial[operation] = true
		break
	}
	if len(all) > 1 && (Service{ExecutionCapabilities: partial}).ExecutionCapabilitiesReady(plan) {
		t.Fatal("partial capability registry enabled execution")
	}
	if !(Service{ExecutionCapabilities: all}).ExecutionCapabilitiesReady(plan) {
		t.Fatal("complete explicit capability registry did not enable execution")
	}

	disabled := make(map[string]bool, len(all))
	for operation := range all {
		disabled[operation] = true
	}
	for operation := range disabled {
		disabled[operation] = false
		break
	}
	if (Service{ExecutionCapabilities: disabled}).ExecutionCapabilitiesReady(plan) {
		t.Fatal("registered-but-disabled operation enabled execution")
	}

	unknown := plan
	unknown.Entries = append([]ExecutionPlanEntry(nil), plan.Entries...)
	unknown.Entries[0].Operations = []string{"FUTURE_UNREGISTERED_ACTION"}
	registryWithUnknown := map[string]bool{"FUTURE_UNREGISTERED_ACTION": true}
	if (Service{ExecutionCapabilities: registryWithUnknown}).ExecutionCapabilitiesReady(unknown) {
		t.Fatal("registry entry outside the closed operation vocabulary enabled execution")
	}

	missingOperations := plan
	missingOperations.Entries = append([]ExecutionPlanEntry(nil), plan.Entries...)
	missingOperations.Entries[0].Operations = nil
	if (Service{ExecutionCapabilities: all}).ExecutionCapabilitiesReady(missingOperations) {
		t.Fatal("entry without operations enabled execution")
	}
	wrongVersion := plan
	wrongVersion.Entries = append([]ExecutionPlanEntry(nil), plan.Entries...)
	wrongVersion.Entries[0].ActionVersion = "privacy-erasure-action/v999"
	if (Service{ExecutionCapabilities: all}).ExecutionCapabilitiesReady(wrongVersion) {
		t.Fatal("unsupported action version enabled execution")
	}
}

func TestClaimedWorkRequiresFencedAllowlistedCheckpoints(t *testing.T) {
	jobID := uuid.New()
	job := ExecutionJob{
		PrivacyErasureCategoryJob: dbgen.PrivacyErasureCategoryJob{
			ID: jobID, ExecutionID: uuid.New(), EntrySha256: make([]byte, sha256.Size),
			CategoryKey: "identity-core", PurposeCode: "ACCOUNT_IDENTITY", Status: "LEASED", LeaseEpoch: 3, AttemptCount: 2,
		},
		ActiveLeaseID: uuid.New(), ActiveAttemptID: uuid.New(),
	}
	checkpoints := []dbgen.PrivacyErasureJobCheckpoint{{
		JobID: jobID, OperationPosition: 1, OperationCode: "IDENTITY_CLEAR",
		ActionVersion: SupportedActionVersion, Status: "PENDING",
	}}
	if err := validateClaimedWork(job, checkpoints); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*ExecutionJob, []dbgen.PrivacyErasureJobCheckpoint)
	}{
		{name: "wrong epoch", mutate: func(job *ExecutionJob, _ []dbgen.PrivacyErasureJobCheckpoint) { job.LeaseEpoch = 0 }},
		{name: "wrong position", mutate: func(_ *ExecutionJob, rows []dbgen.PrivacyErasureJobCheckpoint) {
			rows[0].OperationPosition = 2
		}},
		{name: "unknown action", mutate: func(_ *ExecutionJob, rows []dbgen.PrivacyErasureJobCheckpoint) {
			rows[0].OperationCode = "UNKNOWN_ACTION"
		}},
		{name: "wrong action version", mutate: func(_ *ExecutionJob, rows []dbgen.PrivacyErasureJobCheckpoint) {
			rows[0].ActionVersion = "v9"
		}},
		{name: "unknown checkpoint state", mutate: func(_ *ExecutionJob, rows []dbgen.PrivacyErasureJobCheckpoint) {
			rows[0].Status = "FAILED"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidateJob := job
			candidateRows := slices.Clone(checkpoints)
			test.mutate(&candidateJob, candidateRows)
			if err := validateClaimedWork(candidateJob, candidateRows); !errors.Is(err, ErrExecutorUnavailable) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestClaimedWorkMustMatchItsBoundImmutablePlan(t *testing.T) {
	plan := executionPlanFixture(t)
	created, err := time.Parse(time.RFC3339Nano, plan.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	requestID, executionID, jobID := uuid.New(), uuid.New(), uuid.New()
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	planRow := dbgen.PrivacyRequestExecutionPlan{
		RequestID: requestID, PolicyVersion: plan.PolicyVersion, ExecutorVersion: plan.ExecutorVersion,
		SchemaVersion: plan.SchemaVersion, Plan: payload, CreatedAt: stamp(created),
	}
	planRow.PlanSha256 = executionPlanDigest(requestID, created, plan.PolicyVersion, plan.ExecutorVersion, plan.SchemaVersion, payload)
	execution := dbgen.PrivacyErasureExecution{
		ID: executionID, RequestID: requestID, PlanSha256: slices.Clone(planRow.PlanSha256),
		ExecutorVersion: plan.ExecutorVersion, SchemaVersion: plan.SchemaVersion, RequestVersionAtStart: plan.RequestVersion,
	}
	entryPayload, err := json.Marshal(plan.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	entryDigest := sha256.Sum256(entryPayload)
	job := ExecutionJob{
		PrivacyErasureCategoryJob: dbgen.PrivacyErasureCategoryJob{
			ID: jobID, ExecutionID: executionID, PlanEntryPosition: 1, EntrySha256: entryDigest[:],
			CategoryKey: plan.Entries[0].Category, PurposeCode: plan.Entries[0].Purpose, Status: "LEASED", LeaseEpoch: 1, AttemptCount: 1,
		},
		ActiveLeaseID: uuid.New(), ActiveAttemptID: uuid.New(),
	}
	checkpoints := make([]dbgen.PrivacyErasureJobCheckpoint, len(plan.Entries[0].Operations))
	for index, operation := range plan.Entries[0].Operations {
		checkpoints[index] = dbgen.PrivacyErasureJobCheckpoint{
			JobID: jobID, OperationPosition: int16(index + 1), OperationCode: operation,
			ActionVersion: plan.Entries[0].ActionVersion, Status: "PENDING",
		}
	}
	if err = validateClaimedWorkAgainstPlan(job, checkpoints, execution, planRow, plan); err != nil {
		t.Fatal(err)
	}

	badExecution := execution
	badExecution.PlanSha256 = slices.Clone(execution.PlanSha256)
	badExecution.PlanSha256[0] ^= 0xff
	if err = validateClaimedWorkAgainstPlan(job, checkpoints, badExecution, planRow, plan); !errors.Is(err, ErrExecutorUnavailable) {
		t.Fatalf("execution-plan digest mismatch: %v", err)
	}
	badJob := job
	badJob.EntrySha256 = slices.Clone(job.EntrySha256)
	badJob.EntrySha256[0] ^= 0xff
	if err = validateClaimedWorkAgainstPlan(badJob, checkpoints, execution, planRow, plan); !errors.Is(err, ErrExecutorUnavailable) {
		t.Fatalf("entry digest mismatch: %v", err)
	}
	if err = validateClaimedWorkAgainstPlan(job, checkpoints[:0], execution, planRow, plan); !errors.Is(err, ErrExecutorUnavailable) {
		t.Fatalf("missing checkpoints: %v", err)
	}
	badCheckpoints := slices.Clone(checkpoints)
	badCheckpoints[0].OperationCode = "AUTH_TOKEN_DELETE"
	if err = validateClaimedWorkAgainstPlan(job, badCheckpoints, execution, planRow, plan); !errors.Is(err, ErrExecutorUnavailable) {
		t.Fatalf("wrong entry operation: %v", err)
	}
}

func TestFailureVocabularyAndRetryScheduleAreBounded(t *testing.T) {
	digest := sha256.Sum256([]byte("redacted diagnostic"))
	valid := ExecutionFailure{
		Classification: FailureRetryable, Stage: FailureStageExecute,
		Code: FailureDependencyUnavailable, DiagnosticDigest: digest[:],
	}
	if !validExecutionFailure(valid) {
		t.Fatal("valid coded failure rejected")
	}
	invalid := []ExecutionFailure{
		{Classification: "MAYBE", Stage: FailureStageExecute, Code: FailureActionFailed},
		{Classification: FailureTerminal, Stage: "RAW_STACK", Code: FailureActionFailed},
		{Classification: FailureTerminal, Stage: FailureStageVerify, Code: "provider said user@example.test"},
		{Classification: FailureRetryable, Stage: FailureStageExecute, Code: FailureActionFailed, DiagnosticDigest: []byte("raw error")},
	}
	for _, failure := range invalid {
		if validExecutionFailure(failure) {
			t.Fatalf("unbounded failure accepted: %+v", failure)
		}
	}
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour}
	for index, expected := range want {
		if got := retryDelay(int32(index + 1)); got != expected {
			t.Fatalf("attempt %d delay=%s want=%s", index+1, got, expected)
		}
	}
}

func TestVerifiedDependantTransferRequiresCurrentSafeGuardianState(t *testing.T) {
	now := time.Date(2026, time.September, 8, 12, 0, 0, 0, time.UTC)
	formerGuardian, newGuardianID, dependantID := uuid.New(), uuid.New(), uuid.New()
	newGuardian := dbgen.User{ID: newGuardianID, IsActive: true, DateOfBirth: pgtype.Date{Time: now.AddDate(-30, 0, 0), Valid: true}}
	dependant := dbgen.User{
		ID: dependantID, IsActive: true, IsDependent: true, GuardianID: &newGuardianID,
		DateOfBirth: pgtype.Date{Time: now.AddDate(-12, 0, 0), Valid: true}, UpdatedAt: stamp(now.Add(time.Minute)),
	}
	resolution := dbgen.PrivacyRequestDependantResolution{
		DependantID: dependantID, GuardianIDSnapshot: formerGuardian, ResolutionCode: "VERIFIED_TRANSFER",
		RelationshipUpdatedAt: stamp(now),
	}
	locked := map[uuid.UUID]dbgen.User{dependantID: dependant, newGuardianID: newGuardian}
	if !verifiedDependantTransfer(resolution, dependant, formerGuardian, locked, now) {
		t.Fatal("verified transfer to a current active adult was rejected")
	}
	formalSnapshot := resolution
	formalSnapshot.ResolutionCode = "FORMAL_RESOLUTION"
	if !verifiedDependantTransfer(formalSnapshot, dependant, formerGuardian, locked, now) {
		t.Fatal("locked current transfer facts were ignored because of the older snapshot code")
	}

	adultDependant := dependant
	adultDependant.IsDependent = false
	adultDependant.GuardianID = nil
	if !verifiedDependantTransfer(resolution, adultDependant, formerGuardian, map[uuid.UUID]dbgen.User{dependantID: adultDependant}, now) {
		t.Fatal("former dependant who no longer requires a guardian was rejected")
	}

	for _, test := range []struct {
		name   string
		mutate func(*dbgen.PrivacyRequestDependantResolution, *dbgen.User, map[uuid.UUID]dbgen.User)
	}{
		{name: "wrong former guardian", mutate: func(r *dbgen.PrivacyRequestDependantResolution, _ *dbgen.User, _ map[uuid.UUID]dbgen.User) {
			r.GuardianIDSnapshot = uuid.New()
		}},
		{name: "relationship did not change after snapshot", mutate: func(_ *dbgen.PrivacyRequestDependantResolution, d *dbgen.User, _ map[uuid.UUID]dbgen.User) {
			d.UpdatedAt = stamp(now)
		}},
		{name: "still linked to closing guardian", mutate: func(_ *dbgen.PrivacyRequestDependantResolution, d *dbgen.User, _ map[uuid.UUID]dbgen.User) {
			d.GuardianID = &formerGuardian
		}},
		{name: "new guardian was not locked", mutate: func(_ *dbgen.PrivacyRequestDependantResolution, _ *dbgen.User, users map[uuid.UUID]dbgen.User) {
			delete(users, newGuardianID)
		}},
		{name: "new guardian inactive", mutate: func(_ *dbgen.PrivacyRequestDependantResolution, _ *dbgen.User, users map[uuid.UUID]dbgen.User) {
			g := users[newGuardianID]
			g.IsActive = false
			users[newGuardianID] = g
		}},
		{name: "new guardian is dependent", mutate: func(_ *dbgen.PrivacyRequestDependantResolution, _ *dbgen.User, users map[uuid.UUID]dbgen.User) {
			g := users[newGuardianID]
			g.IsDependent = true
			users[newGuardianID] = g
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidateResolution := resolution
			candidateDependant := dependant
			candidateUsers := map[uuid.UUID]dbgen.User{dependantID: candidateDependant, newGuardianID: newGuardian}
			test.mutate(&candidateResolution, &candidateDependant, candidateUsers)
			candidateUsers[dependantID] = candidateDependant
			if verifiedDependantTransfer(candidateResolution, candidateDependant, formerGuardian, candidateUsers, now) {
				t.Fatal("unsafe transfer accepted")
			}
		})
	}
}

func TestExecutionEntryPointsRejectUntrustedIdentifiersBeforeDatabaseAccess(t *testing.T) {
	service := Service{Enabled: true}
	if _, err := service.StartExecution(context.Background(), StartInput{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("start error=%v", err)
	}
	worker := ExecutionWorker{WorkerRef: uuid.New()}
	if _, err := worker.Claim(context.Background()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("claim error=%v", err)
	}
	if _, err := worker.Heartbeat(context.Background(), ExecutionLease{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("heartbeat error=%v", err)
	}
	if _, err := worker.CompleteCheckpoint(context.Background(), ExecutionLease{}, "UNKNOWN", SupportedActionVersion); !errors.Is(err, ErrInvalid) {
		t.Fatalf("checkpoint error=%v", err)
	}
	if _, err := worker.CompleteJob(context.Background(), ExecutionLease{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("complete error=%v", err)
	}
	if _, err := worker.FailJob(context.Background(), ExecutionLease{}, ExecutionFailure{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("failure error=%v", err)
	}
	if err := worker.failInTransaction(context.Background(), nil, nil, ExecutionLease{}, ExecutionFailure{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("transaction failure error=%v", err)
	}
	if err := service.GrantExecutor(context.Background(), uuid.Nil, uuid.New(), false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("grant error=%v", err)
	}
	validInput := StartInput{ActorID: uuid.New(), Reference: uuid.New(), Version: 2, Confirmed: true}
	if _, err := service.StartExecution(context.Background(), validInput); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing pool start error=%v", err)
	}
	closedPool, err := pgxpool.New(context.Background(), "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	closedPool.Close()
	if _, err = (Service{Pool: closedPool}).StartExecution(context.Background(), validInput); err == nil {
		t.Fatal("closed service pool error was ignored")
	}
}

func TestExecutionWorkerDefaultsBoundsAndClosedPoolErrors(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	worker := ExecutionWorker{Pool: pool, WorkerRef: uuid.New()}
	if worker.leaseDuration() != defaultExecutionLeaseDuration || worker.maxAttempts() != defaultExecutionMaxAttempts || !worker.valid() {
		t.Fatal("valid defaults rejected")
	}
	explicit := ExecutionWorker{Pool: pool, WorkerRef: uuid.New(), LeaseDuration: time.Minute, MaxAttempts: 2}
	if explicit.leaseDuration() != time.Minute || explicit.maxAttempts() != 2 || !explicit.valid() {
		t.Fatal("valid explicit bounds rejected")
	}
	for _, invalid := range []ExecutionWorker{
		{Pool: pool, WorkerRef: uuid.New(), LeaseDuration: time.Millisecond},
		{Pool: pool, WorkerRef: uuid.New(), LeaseDuration: 2 * time.Hour},
		{Pool: pool, WorkerRef: uuid.New(), MaxAttempts: 101},
	} {
		if invalid.valid() {
			t.Fatalf("invalid worker accepted: %+v", invalid)
		}
	}
	jobID, executionID := uuid.New(), uuid.New()
	lease := ExecutionLease{Job: ExecutionJob{PrivacyErasureCategoryJob: dbgen.PrivacyErasureCategoryJob{
		ID: jobID, ExecutionID: executionID, LeaseEpoch: 1, AttemptCount: 1,
	}, ActiveLeaseID: uuid.New(), ActiveAttemptID: uuid.New()}}
	failure := ExecutionFailure{Classification: FailureTerminal, Stage: FailureStageExecute, Code: FailureActionFailed}
	for name, call := range map[string]func() error{
		"claim":     func() error { _, err := worker.Claim(context.Background()); return err },
		"heartbeat": func() error { _, err := worker.Heartbeat(context.Background(), lease); return err },
		"checkpoint": func() error {
			_, err := worker.CompleteCheckpoint(context.Background(), lease, "IDENTITY_CLEAR", SupportedActionVersion)
			return err
		},
		"complete": func() error { _, err := worker.CompleteJob(context.Background(), lease); return err },
		"fail":     func() error { _, err := worker.FailJob(context.Background(), lease, failure); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("closed pool error was not propagated")
			}
		})
	}
	if retryDelay(0) != time.Minute {
		t.Fatal("non-positive attempts must use the first bounded delay")
	}
	if err := recordAccessRevocation(context.Background(), nil, uuid.New(), "STAFF_GRANT", "COACH", 0, uuid.New(), time.Now()); err != nil {
		t.Fatalf("zero revocation count error=%v", err)
	}
}
