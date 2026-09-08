package privacyrequests

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrExecutionConflict = errors.New("privacy erasure execution conflicts with the accepted handoff")
	ErrLeaseLost         = errors.New("privacy erasure worker lease is no longer authoritative")
	ErrRetryExhausted    = errors.New("privacy erasure retry limit is exhausted")
)

const (
	defaultExecutionLeaseDuration = 5 * time.Minute
	defaultExecutionMaxAttempts   = int32(5)
	maximumRetryDelay             = time.Hour
)

// StartInput contains only the confirmation-bound identifiers rendered by the
// restricted case page. All authority, case, plan, and safety facts are loaded
// again while the request and affected accounts are locked.
type StartInput struct {
	ActorID   uuid.UUID
	Reference uuid.UUID
	Version   int64
	Confirmed bool
}

// StartExecution atomically accepts an immutable reviewed plan, creates its
// complete work graph, and moves the request into durable processing. Account
// closure cuts off access in this same transaction after the graph exists.
func (s Service) StartExecution(ctx context.Context, in StartInput) (dbgen.PrivacyErasureExecution, error) {
	var zero dbgen.PrivacyErasureExecution
	if in.ActorID == uuid.Nil || in.Reference == uuid.Nil || in.Version < 2 || !in.Confirmed {
		return zero, ErrInvalid
	}

	tx, q, err := s.begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)

	r, err := q.GetPrivacyRequestForUpdate(ctx, in.Reference)
	if err != nil || r.RequesterUserID == nil || r.SubjectUserID == nil || r.DecidedBy == nil || !r.DecidedAt.Valid {
		return zero, ErrForbidden
	}
	planRow, err := q.GetPrivacyExecutionPlan(ctx, r.ID)
	if err != nil {
		return zero, ErrPolicyUnresolved
	}
	plan, err := ReadExecutionPlan(planRow)
	if err != nil || plan.RequestVersion != in.Version || plan.DecisionAction == "refuse" ||
		(plan.DecisionAction == "approve" && !sameOptionalString(r.DecisionCode, "APPROVED")) ||
		(plan.DecisionAction == "partial" && !sameOptionalString(r.DecisionCode, "PARTIALLY_APPROVED")) ||
		r.PolicyVersion == nil || *r.PolicyVersion != plan.PolicyVersion {
		return zero, ErrPolicyUnresolved
	}
	// A replay is idempotent only when it is the exact confirmed handoff. A
	// different actor, request version, or plan binding is a divergent duplicate.
	// Replay deliberately precedes mutable grant, activation, capability, and
	// relationship checks: those gates controlled the original commit and cannot
	// make that already-durable handoff disappear.
	existing, existingErr := q.GetPrivacyErasureExecutionByRequest(ctx, r.ID)
	if existingErr == nil {
		if exactExecutionReplay(existing, planRow, in, s.auditActor(r.ID, in.ActorID)) {
			return existing, tx.Commit(ctx)
		}
		return zero, ErrExecutionConflict
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return zero, existingErr
	}
	if !s.Enabled {
		return zero, ErrExecutorUnavailable
	}

	accountIDs := []uuid.UUID{in.ActorID, *r.RequesterUserID, *r.SubjectUserID, *r.DecidedBy}
	if r.ScopeKind == string(AccountClosure) {
		relatedAccountIDs, lockErr := executionClosureAccountIDs(ctx, tx, q, r)
		if lockErr != nil {
			return zero, lockErr
		}
		accountIDs = append(accountIDs, relatedAccountIDs...)
	}
	users, err := lockAccounts(ctx, q, accountIDs...)
	if err != nil {
		return zero, err
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	actor, requester, subject := users[in.ActorID], users[*r.RequesterUserID], users[*r.SubjectUserID]
	if !adult(actor, now) || actor.ID == requester.ID || actor.ID == subject.ID || actor.ID == *r.DecidedBy {
		return zero, ErrForbidden
	}
	if _, err = q.GetPrivacyExecutorGrantForShare(ctx, actor.ID); err != nil {
		return zero, ErrForbidden
	}
	if ok, checkErr := historicalReviewerAuthority(ctx, tx, *r.DecidedBy, r.DecidedAt.Time); checkErr != nil {
		return zero, checkErr
	} else if !ok {
		return zero, ErrForbidden
	}
	if !s.ExecutionCapabilitiesReady(plan) {
		return zero, ErrExecutorUnavailable
	}
	activation, err := q.GetPrivacyActivationForUpdate(ctx)
	if err != nil || !executionActivationReady(activation) {
		return zero, ErrExecutorUnavailable
	}
	if r.Version != in.Version {
		return zero, ErrStaleVersion
	}
	if r.Status != string(AwaitingExecution) && r.Status != string(PartiallyApproved) {
		return zero, ErrInvalidTransition
	}

	var adopted AdoptedPolicy
	if json.Unmarshal(r.PolicySnapshot, &adopted) != nil || adopted.Validate() != nil || adopted.Version != plan.PolicyVersion || adopted.ExecutorVersion != plan.ExecutorVersion || adopted.PlanSchemaVersion != plan.SchemaVersion {
		return zero, ErrPolicyUnresolved
	}
	policy, err := adopted.Snapshot(scopeOf(r))
	if err != nil {
		return zero, err
	}
	checks := verification(r, requester, subject, now)
	safeguards := ClosureSafeguards{DependantsResolved: true, PreservesAdministrator: true}
	if r.ScopeKind == string(AccountClosure) {
		if err = q.LockPrivacyActiveAdministratorSet(ctx); err != nil {
			return zero, err
		}
		safeguards, err = strictExecutionClosureSafeguards(ctx, tx, q, r, subject, users, now)
		if err != nil {
			return zero, err
		}
	}
	caseAtStart := Case{
		RequesterID: requester.ID, SubjectID: subject.ID, Scope: scopeOf(r),
		Status: Status(r.Status), Version: r.Version, ReceivedAt: r.ReceivedAt.Time,
		UpdatedAt: r.UpdatedAt.Time, DecisionPolicy: &policy, DecidedBy: r.DecidedBy,
	}
	transition, err := Transition(caseAtStart, Command{
		Action: StartProcessing, ExpectedVersion: in.Version,
		Actor:        Actor{ID: actor.ID, Active: actor.IsActive, PrivacyExecutor: true},
		Verification: checks, Safeguards: safeguards, ExecutionReady: true, At: now,
	})
	if err != nil {
		return zero, err
	}

	execution, err := q.CreatePrivacyErasureExecution(ctx, dbgen.CreatePrivacyErasureExecutionParams{
		RequestID: r.ID, PlanSha256: slices.Clone(planRow.PlanSha256),
		ExecutorVersion: plan.ExecutorVersion, SchemaVersion: plan.SchemaVersion,
		RequestVersionAtStart: in.Version, StartedByRef: s.auditActor(r.ID, actor.ID), AcceptedAt: stamp(now),
	})
	if err != nil {
		return zero, err
	}
	expectedJobs, expectedCheckpoints, err := createExecutionWorkGraph(ctx, q, execution.ID, plan, now)
	if err != nil {
		return zero, err
	}
	counts, err := q.GetPrivacyErasureWorkSetCounts(ctx, execution.ID)
	if err != nil {
		return zero, err
	}
	if counts.JobCount != int64(expectedJobs) || counts.CheckpointCount != int64(expectedCheckpoints) {
		return zero, ErrExecutorUnavailable
	}

	updated, err := q.TransitionPrivacyRequestExecutionStatus(ctx, dbgen.TransitionPrivacyRequestExecutionStatusParams{
		ToStatus: string(transition.Case.Status), UpdatedAt: stamp(now), ID: r.ID,
		ExpectedVersion: in.Version, FromStatuses: []string{r.Status},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, ErrStaleVersion
	}
	if err != nil {
		return zero, err
	}

	if r.ScopeKind == string(AccountClosure) {
		if err = s.cutOffPrivacyAccount(ctx, q, execution, subject, actor.ID, now); err != nil {
			return zero, err
		}
	}
	from := r.Status
	event, err := q.AppendPrivacyRequestEvent(ctx, dbgen.AppendPrivacyRequestEventParams{
		RequestID: r.ID, ActorRole: "EXECUTOR", ActorRef: execution.StartedByRef,
		Action: "PROCESSING_STARTED", ReasonCode: "EXECUTION_ACCEPTED", FromStatus: &from,
		ToStatus: updated.Status, Version: updated.Version, OccurredAt: stamp(now),
	})
	if err != nil {
		return zero, err
	}
	if err = s.enqueue(ctx, q, updated, requester, event.ID, "PRIVACY_PROCESSING_STARTED", now); err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return execution, nil
}

func sameOptionalString(value *string, want string) bool { return value != nil && *value == want }

func executionActivationReady(activation dbgen.PrivacyRequestActivation) bool {
	return activation.Enabled && activation.FulfilmentReady
}

// ExecutionCapabilitiesReady is a read-only rendering hint. It lets a case
// page show the immutable plan while withholding the irreversible Start control
// until every exact operation has an explicitly installed implementation.
// StartExecution always repeats this check inside its transaction.
func (s Service) ExecutionCapabilitiesReady(plan ExecutionPlan) bool {
	if len(s.ExecutionCapabilities) == 0 || len(plan.Entries) == 0 {
		return false
	}
	for _, entry := range plan.Entries {
		if entry.ActionVersion != SupportedActionVersion || len(entry.Operations) == 0 {
			return false
		}
		for _, operation := range entry.Operations {
			if !supportedOperation(operation) || !s.ExecutionCapabilities[operation] {
				return false
			}
		}
	}
	return true
}

// GrantExecutor manages only the explicit executor capability. Administrator
// status authorizes this operator action but never substitutes for the grant at
// execution time.
func (s Service) GrantExecutor(ctx context.Context, actorID, userID uuid.UUID, revoke bool) error {
	if actorID == uuid.Nil || userID == uuid.Nil {
		return ErrInvalid
	}
	tx, q, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	users, err := lockAccounts(ctx, q, actorID, userID)
	if err != nil {
		return err
	}
	if err = operator(ctx, q, actorID, s); err != nil {
		return err
	}
	if !revoke && !adult(users[userID], s.now()) {
		return ErrForbidden
	}
	now := stamp(s.now())
	action := "GRANTED"
	var grant dbgen.PrivacyExecutorGrant
	if revoke {
		action = "REVOKED"
		grant, err = q.GetPrivacyExecutorGrantForShare(ctx, userID)
		if err == nil {
			grant, err = q.RevokePrivacyExecutor(ctx, dbgen.RevokePrivacyExecutorParams{ID: grant.ID, RevokedBy: &actorID, RevokedAt: now})
		}
	} else {
		grant, err = q.GrantPrivacyExecutor(ctx, dbgen.GrantPrivacyExecutorParams{UserID: userID, GrantedBy: actorID, GrantedAt: now})
	}
	if err != nil {
		return err
	}
	if err = q.AppendPrivacyExecutorGrantEvent(ctx, dbgen.AppendPrivacyExecutorGrantEventParams{
		GrantID: grant.ID, ActorRef: actorID, Action: action, OccurredAt: now,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func exactExecutionReplay(existing dbgen.PrivacyErasureExecution, plan dbgen.PrivacyRequestExecutionPlan, in StartInput, actorRef uuid.UUID) bool {
	return existing.RequestID == plan.RequestID && existing.RequestVersionAtStart == in.Version &&
		existing.StartedByRef == actorRef && existing.ExecutorVersion == plan.ExecutorVersion &&
		existing.SchemaVersion == plan.SchemaVersion && slices.Equal(existing.PlanSha256, plan.PlanSha256)
}

func historicalReviewerAuthority(ctx context.Context, tx pgx.Tx, reviewerID uuid.UUID, decidedAt time.Time) (bool, error) {
	var grantID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM privacy_reviewer_grants
		WHERE user_id=$1 AND granted_at<=$2 AND (revoked_at IS NULL OR revoked_at>$2)
		ORDER BY granted_at DESC,id LIMIT 1 FOR SHARE`, reviewerID, decidedAt).Scan(&grantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil && grantID != uuid.Nil, err
}

func executionClosureAccountIDs(ctx context.Context, tx pgx.Tx, q *dbgen.Queries, r dbgen.DataErasureRequest) ([]uuid.UUID, error) {
	resolutions, err := q.ListPrivacyDependantResolutions(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	resolvedIDs := make([]uuid.UUID, 0, len(resolutions))
	for _, resolution := range resolutions {
		resolvedIDs = append(resolvedIDs, resolution.DependantID)
	}
	rows, err := tx.Query(ctx, `SELECT id,guardian_id FROM users
		WHERE guardian_id=$1 OR id=ANY($2::uuid[]) ORDER BY id`, r.SubjectUserID, resolvedIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]uuid.UUID, 0, len(resolvedIDs)*2)
	for rows.Next() {
		var id uuid.UUID
		var guardianID *uuid.UUID
		if err = rows.Scan(&id, &guardianID); err != nil {
			return nil, err
		}
		ids = append(ids, id)
		if guardianID != nil {
			ids = append(ids, *guardianID)
		}
	}
	return ids, rows.Err()
}

type relatedClosureExecution struct {
	ID          uuid.UUID
	SubjectID   *uuid.UUID
	ScopeKind   string
	Status      string
	ExecutionID uuid.UUID
	Plan        dbgen.PrivacyRequestExecutionPlan
	GraphValid  bool
}

func lockRelatedClosureExecutions(ctx context.Context, tx pgx.Tx, resolutions []dbgen.PrivacyRequestDependantResolution) (map[uuid.UUID]relatedClosureExecution, error) {
	ids := make([]uuid.UUID, 0, len(resolutions))
	seen := make(map[uuid.UUID]bool, len(resolutions))
	for _, resolution := range resolutions {
		if resolution.RelatedRequestID != nil && !seen[*resolution.RelatedRequestID] {
			seen[*resolution.RelatedRequestID] = true
			ids = append(ids, *resolution.RelatedRequestID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	locked := make(map[uuid.UUID]relatedClosureExecution, len(ids))
	for _, id := range ids {
		var row relatedClosureExecution
		err := tx.QueryRow(ctx, `SELECT request.id,request.subject_user_id,request.scope_kind,request.status,
				execution.id,plan.request_id,plan.policy_version,plan.executor_version,plan.schema_version,plan.plan,plan.plan_sha256,plan.created_at
			FROM data_erasure_requests request
			JOIN privacy_erasure_executions execution ON execution.request_id=request.id
			JOIN privacy_request_execution_plans plan ON plan.request_id=request.id
			WHERE request.id=$1 FOR SHARE OF request,execution,plan`, id).Scan(
			&row.ID, &row.SubjectID, &row.ScopeKind, &row.Status, &row.ExecutionID,
			&row.Plan.RequestID, &row.Plan.PolicyVersion, &row.Plan.ExecutorVersion, &row.Plan.SchemaVersion,
			&row.Plan.Plan, &row.Plan.PlanSha256, &row.Plan.CreatedAt,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		plan, planErr := ReadExecutionPlan(row.Plan)
		if planErr == nil {
			row.GraphValid, err = exactPersistedExecutionGraph(ctx, tx, row.ExecutionID, plan)
			if err != nil {
				return nil, err
			}
		}
		locked[id] = row
	}
	return locked, nil
}

func exactPersistedExecutionGraph(ctx context.Context, tx pgx.Tx, executionID uuid.UUID, plan ExecutionPlan) (bool, error) {
	expected, err := executionWorkGraph(plan)
	if err != nil {
		return false, nil
	}
	rows, err := tx.Query(ctx, `SELECT job.plan_entry_position,job.entry_sha256,job.category_key,job.purpose_code,
		checkpoint.operation_position,checkpoint.operation_code,checkpoint.action_version
		FROM privacy_erasure_category_jobs job
		LEFT JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id
		WHERE job.execution_id=$1
		ORDER BY job.plan_entry_position,checkpoint.operation_position
		FOR SHARE OF job`, executionID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	jobIndex, operationIndex := 0, 0
	for rows.Next() {
		if jobIndex >= len(expected) || operationIndex >= len(expected[jobIndex].Operations) {
			return false, nil
		}
		var jobPosition int16
		var operationPosition pgtype.Int2
		var entryDigest []byte
		var category, purpose string
		var operationCode, actionVersion pgtype.Text
		if err = rows.Scan(&jobPosition, &entryDigest, &category, &purpose, &operationPosition, &operationCode, &actionVersion); err != nil {
			return false, err
		}
		job, operation := expected[jobIndex], expected[jobIndex].Operations[operationIndex]
		if !operationPosition.Valid || !operationCode.Valid || !actionVersion.Valid || jobPosition != job.Position || !slices.Equal(entryDigest, job.EntryDigest) || category != job.Category || purpose != job.Purpose ||
			operationPosition.Int16 != operation.Position || operationCode.String != operation.Code || actionVersion.String != operation.ActionVersion {
			return false, nil
		}
		operationIndex++
		if operationIndex == len(job.Operations) {
			jobIndex++
			operationIndex = 0
		}
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	return jobIndex == len(expected) && operationIndex == 0, nil
}

func strictExecutionClosureSafeguards(ctx context.Context, tx pgx.Tx, q *dbgen.Queries, r dbgen.DataErasureRequest, subject dbgen.User, lockedUsers map[uuid.UUID]dbgen.User, now time.Time) (ClosureSafeguards, error) {
	out := ClosureSafeguards{DependantsResolved: true, PreservesAdministrator: true}
	admin, err := q.IsPrivacyAdministrator(ctx, subject.ID)
	if err != nil {
		return out, err
	}
	if admin {
		count, countErr := q.CountPrivacyActiveAdministrators(ctx)
		if countErr != nil {
			return out, countErr
		}
		out.PreservesAdministrator = count > 1
	}
	dependants, err := q.ListPrivacyDependantsForUpdate(ctx, &subject.ID)
	if err != nil {
		return out, err
	}
	resolutions, err := q.ListPrivacyDependantResolutions(ctx, r.ID)
	if err != nil {
		return out, err
	}
	relatedRequests, err := lockRelatedClosureExecutions(ctx, tx, resolutions)
	if err != nil {
		return out, err
	}
	byDependant := make(map[uuid.UUID]dbgen.PrivacyRequestDependantResolution, len(resolutions))
	for _, resolution := range resolutions {
		byDependant[resolution.DependantID] = resolution
	}
	currentDependants := make(map[uuid.UUID]bool, len(dependants))
	for _, dependant := range dependants {
		currentDependants[dependant.ID] = true
		lockedUsers[dependant.ID] = dependant
		resolution, ok := byDependant[dependant.ID]
		if !ok || resolution.GuardianIDSnapshot != subject.ID || !resolution.RelationshipUpdatedAt.Valid ||
			!dependant.UpdatedAt.Valid || !resolution.RelationshipUpdatedAt.Time.Equal(dependant.UpdatedAt.Time) ||
			resolution.ResolutionCode != "SEPARATE_APPROVED_REQUEST" || resolution.RelatedRequestID == nil {
			out.DependantsResolved = false
			continue
		}
		related, ok := relatedRequests[*resolution.RelatedRequestID]
		if !ok || !related.GraphValid || related.SubjectID == nil || *related.SubjectID != dependant.ID || related.ScopeKind != string(AccountClosure) ||
			(related.Status != string(Processing) && related.Status != string(Completed)) {
			out.DependantsResolved = false
		}
	}
	for _, resolution := range resolutions {
		if currentDependants[resolution.DependantID] {
			continue
		}
		dependant, ok := lockedUsers[resolution.DependantID]
		if !ok || !verifiedDependantTransfer(resolution, dependant, subject.ID, lockedUsers, now) {
			out.DependantsResolved = false
		}
	}
	return out, nil
}

func verifiedDependantTransfer(resolution dbgen.PrivacyRequestDependantResolution, dependant dbgen.User, formerGuardian uuid.UUID, lockedUsers map[uuid.UUID]dbgen.User, now time.Time) bool {
	if resolution.GuardianIDSnapshot != formerGuardian ||
		!resolution.RelationshipUpdatedAt.Valid || !dependant.UpdatedAt.Valid || !dependant.UpdatedAt.Time.After(resolution.RelationshipUpdatedAt.Time) {
		return false
	}
	if !dependantRequiresGuardian(dependant, now) {
		return true
	}
	if dependant.GuardianID == nil || *dependant.GuardianID == formerGuardian {
		return false
	}
	guardian, ok := lockedUsers[*dependant.GuardianID]
	return ok && adult(guardian, now)
}

func dependantRequiresGuardian(dependant dbgen.User, now time.Time) bool {
	return dependant.IsDependent && (!dependant.DateOfBirth.Valid || dependant.DateOfBirth.Time.AddDate(18, 0, 0).After(now))
}

func createExecutionWorkGraph(ctx context.Context, q *dbgen.Queries, executionID uuid.UUID, plan ExecutionPlan, now time.Time) (int, int, error) {
	work, err := executionWorkGraph(plan)
	if err != nil {
		return 0, 0, err
	}
	checkpoints := 0
	for _, spec := range work {
		job, err := q.CreatePrivacyErasureCategoryJob(ctx, dbgen.CreatePrivacyErasureCategoryJobParams{
			ExecutionID: executionID, PlanEntryPosition: spec.Position, EntrySha256: slices.Clone(spec.EntryDigest),
			CategoryKey: spec.Category, PurposeCode: spec.Purpose, NextAttemptAt: stamp(now), CreatedAt: stamp(now),
		})
		if err != nil {
			return 0, 0, err
		}
		for _, operation := range spec.Operations {
			if _, err = q.CreatePrivacyErasureJobCheckpoint(ctx, dbgen.CreatePrivacyErasureJobCheckpointParams{
				JobID: job.ID, OperationPosition: operation.Position, OperationCode: operation.Code,
				ActionVersion: operation.ActionVersion, CreatedAt: stamp(now),
			}); err != nil {
				return 0, 0, err
			}
			checkpoints++
		}
	}
	return len(work), checkpoints, nil
}

type executionWorkJob struct {
	Position    int16
	EntryDigest []byte
	Category    string
	Purpose     string
	Operations  []executionWorkOperation
}

type executionWorkOperation struct {
	Position      int16
	Code          string
	ActionVersion string
}

func executionWorkGraph(plan ExecutionPlan) ([]executionWorkJob, error) {
	if len(plan.Entries) == 0 || len(plan.Entries) > 50 {
		return nil, ErrExecutorUnavailable
	}
	work := make([]executionWorkJob, 0, len(plan.Entries))
	seenCategories := make(map[string]bool, len(plan.Entries))
	for entryIndex, entry := range plan.Entries {
		if seenCategories[entry.Category] || !policyKey.MatchString(entry.Category) || !policyKey.MatchString(entry.Purpose) ||
			entry.ActionVersion != SupportedActionVersion || len(entry.Operations) == 0 || len(entry.Operations) > 100 {
			return nil, ErrExecutorUnavailable
		}
		seenCategories[entry.Category] = true
		canonical, err := json.Marshal(entry)
		if err != nil {
			return nil, ErrPolicyUnresolved
		}
		digest := sha256.Sum256(canonical)
		spec := executionWorkJob{Position: int16(entryIndex + 1), EntryDigest: slices.Clone(digest[:]), Category: entry.Category, Purpose: entry.Purpose}
		seenOperations := make(map[string]bool, len(entry.Operations))
		for operationIndex, operation := range entry.Operations {
			if !supportedOperation(operation) || seenOperations[operation] {
				return nil, ErrExecutorUnavailable
			}
			seenOperations[operation] = true
			spec.Operations = append(spec.Operations, executionWorkOperation{Position: int16(operationIndex + 1), Code: operation, ActionVersion: entry.ActionVersion})
		}
		work = append(work, spec)
	}
	return work, nil
}

func supportedOperation(operation string) bool {
	for _, profile := range executionProfiles {
		if slices.Contains(profile.Operations, operation) {
			return true
		}
	}
	return false
}

func (s Service) cutOffPrivacyAccount(ctx context.Context, q *dbgen.Queries, execution dbgen.PrivacyErasureExecution, subject dbgen.User, executorID uuid.UUID, now time.Time) error {
	legacy, err := q.CountActiveUnindexedPrivacySessions(ctx)
	if err != nil {
		return err
	}
	if legacy > 0 {
		return ErrExecutorUnavailable
	}
	if _, err = q.DisablePrivacyAccountForExecution(ctx, dbgen.DisablePrivacyAccountForExecutionParams{DisabledAt: stamp(now), UserID: subject.ID}); err != nil {
		return err
	}
	if _, err = q.DeletePrivacySessionsByUser(ctx, &subject.ID); err != nil {
		return err
	}
	if _, err = q.InvalidatePrivacyAccountTokens(ctx, dbgen.InvalidatePrivacyAccountTokensParams{InvalidatedAt: stamp(now), SubjectUserID: subject.ID}); err != nil {
		return err
	}

	actorRef := s.auditActor(execution.RequestID, executorID)
	roles, err := q.RevokePrivacyPlatformRolesForExecution(ctx, subject.ID)
	if err != nil {
		return err
	}
	for _, role := range roles {
		if err = recordAccessRevocation(ctx, q, execution.ID, "PLATFORM_ROLE", role, 1, actorRef, now); err != nil {
			return err
		}
	}
	staff, err := q.RevokePrivacyStaffGrantsForExecution(ctx, dbgen.RevokePrivacyStaffGrantsForExecutionParams{RevokedByID: &executorID, RevokedAt: stamp(now), UserID: subject.ID})
	if err != nil {
		return err
	}
	staffCounts := make(map[string]int32)
	for _, grant := range staff {
		staffCounts[string(grant.Capability)]++
	}
	capabilities := make([]string, 0, len(staffCounts))
	for capability := range staffCounts {
		capabilities = append(capabilities, capability)
	}
	sort.Strings(capabilities)
	for _, capability := range capabilities {
		if err = recordAccessRevocation(ctx, q, execution.ID, "STAFF_GRANT", capability, staffCounts[capability], actorRef, now); err != nil {
			return err
		}
	}
	reviewers, err := q.RevokePrivacyReviewerGrantsForExecution(ctx, dbgen.RevokePrivacyReviewerGrantsForExecutionParams{RevokedBy: &executorID, RevokedAt: stamp(now), UserID: subject.ID})
	if err != nil {
		return err
	}
	if len(reviewers) > 0 {
		if err = recordAccessRevocation(ctx, q, execution.ID, "PRIVACY_REVIEWER", "PRIVACY_REVIEWER", int32(len(reviewers)), actorRef, now); err != nil {
			return err
		}
	}
	executors, err := q.RevokePrivacyExecutorGrantsForExecution(ctx, dbgen.RevokePrivacyExecutorGrantsForExecutionParams{RevokedBy: &executorID, RevokedAt: stamp(now), UserID: subject.ID})
	if err != nil {
		return err
	}
	if len(executors) > 0 {
		if err = recordAccessRevocation(ctx, q, execution.ID, "PRIVACY_EXECUTOR", "PRIVACY_EXECUTOR", int32(len(executors)), actorRef, now); err != nil {
			return err
		}
	}
	return nil
}

func recordAccessRevocation(ctx context.Context, q *dbgen.Queries, executionID uuid.UUID, kind, capability string, count int32, actorRef uuid.UUID, now time.Time) error {
	if count < 1 {
		return nil
	}
	_, err := q.CreatePrivacyErasureAccessRevocation(ctx, dbgen.CreatePrivacyErasureAccessRevocationParams{
		ExecutionID: executionID, GrantKind: kind, CapabilityCode: capability,
		RevokedCount: count, ActorRef: actorRef, OccurredAt: stamp(now),
	})
	return err
}

// ExecutionWorker is deliberately separate from Service so a process can use
// the least-privilege worker database role without inheriting web mutations.
type ExecutionWorker struct {
	Pool          *pgxpool.Pool
	WorkerRef     uuid.UUID
	LeaseDuration time.Duration
	MaxAttempts   int32
}

type ExecutionLease struct {
	Job         ExecutionJob
	Checkpoints []dbgen.PrivacyErasureJobCheckpoint
}

type ExecutionJob struct {
	dbgen.PrivacyErasureCategoryJob
	ActiveLeaseID   uuid.UUID
	WorkerRef       uuid.UUID
	LeaseExpiresAt  pgtype.Timestamptz
	ActiveAttemptID uuid.UUID
}

type FailureClassification string
type FailureStage string
type FailureCode string

const (
	FailureRetryable FailureClassification = "RETRYABLE"
	FailureTerminal  FailureClassification = "TERMINAL"

	FailureStageExecute FailureStage = "EXECUTE"
	FailureStageVerify  FailureStage = "VERIFY"

	FailureActionFailed          FailureCode = "ACTION_FAILED"
	FailureDependencyUnavailable FailureCode = "DEPENDENCY_UNAVAILABLE"
	FailureUnsupportedOperation  FailureCode = "UNSUPPORTED_OPERATION"
	FailureVerificationFailed    FailureCode = "VERIFICATION_FAILED"
	FailureRetryLimitReached     FailureCode = "RETRY_LIMIT_REACHED"
)

type ExecutionFailure struct {
	Classification FailureClassification
	Stage          FailureStage
	Code           FailureCode
	// DiagnosticDigest may contain only a one-way 32-byte digest of a bounded,
	// redacted diagnostic. Raw errors and subject data are never persisted.
	DiagnosticDigest []byte
}

func (w ExecutionWorker) leaseDuration() time.Duration {
	if w.LeaseDuration > 0 {
		return w.LeaseDuration
	}
	return defaultExecutionLeaseDuration
}

func (w ExecutionWorker) maxAttempts() int32 {
	if w.MaxAttempts > 0 {
		return w.MaxAttempts
	}
	return defaultExecutionMaxAttempts
}

func (w ExecutionWorker) valid() bool {
	return w.Pool != nil && w.WorkerRef != uuid.Nil && w.leaseDuration() >= time.Second && w.leaseDuration() <= time.Hour && w.maxAttempts() >= 1 && w.maxAttempts() <= 100
}

func (w ExecutionWorker) Claim(ctx context.Context) (ExecutionLease, error) {
	var zero ExecutionLease
	if !w.valid() {
		return zero, ErrInvalid
	}
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	var jobID, leaseID, attemptID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT job_id,lease_id,attempt_id FROM privacy_worker_claim($1,$2)`, w.leaseDuration().Milliseconds(), w.WorkerRef).Scan(&jobID, &leaseID, &attemptID)
	if err != nil {
		return zero, err
	}
	jobRow, err := q.GetPrivacyErasureCategoryJob(ctx, jobID)
	if err != nil {
		return zero, err
	}
	leaseRow, err := q.GetPrivacyErasureJobLease(ctx, leaseID)
	if err != nil {
		return zero, err
	}
	job := ExecutionJob{PrivacyErasureCategoryJob: jobRow, ActiveLeaseID: leaseID, WorkerRef: leaseRow.WorkerRef, LeaseExpiresAt: leaseRow.ExpiresAt, ActiveAttemptID: attemptID}
	lease := ExecutionLease{Job: job}
	if job.AttemptCount > w.maxAttempts() {
		failure := ExecutionFailure{Classification: FailureTerminal, Stage: FailureStageExecute, Code: FailureRetryLimitReached}
		if err = w.failInTransaction(ctx, tx, q, lease, failure); err != nil {
			return zero, err
		}
		if err = tx.Commit(ctx); err != nil {
			return zero, err
		}
		return zero, ErrRetryExhausted
	}
	checkpoints, err := q.ListPrivacyErasureJobCheckpoints(ctx, job.ID)
	if err != nil {
		return zero, err
	}
	execution, err := q.GetPrivacyErasureExecution(ctx, job.ExecutionID)
	if err != nil {
		return zero, err
	}
	planRow, err := q.GetPrivacyExecutionPlan(ctx, execution.RequestID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	plan, planErr := ReadExecutionPlan(planRow)
	if errors.Is(err, pgx.ErrNoRows) || planErr != nil || validateClaimedWorkAgainstPlan(job, checkpoints, execution, planRow, plan) != nil {
		failure := ExecutionFailure{Classification: FailureTerminal, Stage: FailureStageVerify, Code: FailureUnsupportedOperation}
		if err = w.failInTransaction(ctx, tx, q, lease, failure); err != nil {
			return zero, err
		}
		if err = tx.Commit(ctx); err != nil {
			return zero, err
		}
		return zero, ErrExecutorUnavailable
	}
	lease.Checkpoints = checkpoints
	if _, err = syncExecutionAndRequest(ctx, q, lease, w.WorkerRef); err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return lease, nil
}

func (w ExecutionWorker) Heartbeat(ctx context.Context, lease ExecutionLease) (dbgen.PrivacyErasureJobLease, error) {
	var zero dbgen.PrivacyErasureJobLease
	if !w.valid() || !validLease(lease) {
		return zero, ErrInvalid
	}
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	var leaseID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM (SELECT privacy_worker_heartbeat($1,$2,$3,$4,$5,$6) AS id) result WHERE id IS NOT NULL`,
		lease.Job.ID, lease.Job.ActiveLeaseID, lease.Job.ActiveAttemptID, lease.Job.LeaseEpoch, w.WorkerRef, w.leaseDuration().Milliseconds()).Scan(&leaseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, ErrLeaseLost
	}
	if err != nil {
		return zero, err
	}
	row, err := q.GetPrivacyErasureJobLease(ctx, leaseID)
	if err != nil {
		return zero, err
	}
	if row.Epoch != lease.Job.LeaseEpoch || row.WorkerRef != w.WorkerRef {
		return zero, ErrLeaseLost
	}
	return row, tx.Commit(ctx)
}

func (w ExecutionWorker) CompleteCheckpoint(ctx context.Context, lease ExecutionLease, operationCode, actionVersion string) (dbgen.PrivacyErasureJobCheckpoint, error) {
	var zero dbgen.PrivacyErasureJobCheckpoint
	if !w.valid() || !validLease(lease) || !supportedOperation(operationCode) || actionVersion != SupportedActionVersion {
		return zero, ErrInvalid
	}
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	checkpoints, err := q.ListPrivacyErasureJobCheckpoints(ctx, lease.Job.ID)
	if err != nil {
		return zero, err
	}
	var selected *dbgen.PrivacyErasureJobCheckpoint
	for index := range checkpoints {
		if checkpoints[index].OperationCode == operationCode && checkpoints[index].ActionVersion == actionVersion {
			selected = &checkpoints[index]
			break
		}
	}
	if selected == nil {
		return zero, ErrInvalid
	}
	if selected.Status == "SUCCEEDED" {
		return *selected, tx.Commit(ctx)
	}
	var checkpointID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM (SELECT privacy_worker_complete_checkpoint($1,$2,$3,$4,$5,$6,$7) AS id) result WHERE id IS NOT NULL`,
		lease.Job.ID, lease.Job.ActiveLeaseID, lease.Job.ActiveAttemptID, lease.Job.LeaseEpoch, w.WorkerRef, operationCode, actionVersion).Scan(&checkpointID)
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, ErrLeaseLost
	}
	if err != nil {
		return zero, err
	}
	row, err := q.GetPrivacyErasureJobCheckpoint(ctx, checkpointID)
	if err != nil {
		return zero, err
	}
	return row, tx.Commit(ctx)
}

func (w ExecutionWorker) CompleteJob(ctx context.Context, lease ExecutionLease) (dbgen.PrivacyErasureExecution, error) {
	var zero dbgen.PrivacyErasureExecution
	if !w.valid() || !validLease(lease) {
		return zero, ErrInvalid
	}
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	var jobID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM (SELECT privacy_worker_complete_job($1,$2,$3,$4,$5) AS id) result WHERE id IS NOT NULL`,
		lease.Job.ID, lease.Job.ActiveLeaseID, lease.Job.ActiveAttemptID, lease.Job.LeaseEpoch, w.WorkerRef).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, ErrLeaseLost
	}
	if err != nil {
		return zero, err
	}
	job, err := q.GetPrivacyErasureCategoryJob(ctx, jobID)
	if err != nil {
		return zero, err
	}
	if job.LeaseEpoch != lease.Job.LeaseEpoch {
		return zero, ErrLeaseLost
	}
	execution, err := syncExecutionAndRequest(ctx, q, lease, w.WorkerRef)
	if err != nil {
		return zero, err
	}
	// #248 owns evidence verification and the only transition to COMPLETED.
	// Even an execution whose category jobs all succeeded leaves the request open.
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return execution, nil
}

func (w ExecutionWorker) FailJob(ctx context.Context, lease ExecutionLease, failure ExecutionFailure) (dbgen.PrivacyErasureExecution, error) {
	var zero dbgen.PrivacyErasureExecution
	if !w.valid() || !validLease(lease) || !validExecutionFailure(failure) {
		return zero, ErrInvalid
	}
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	if err = w.failInTransaction(ctx, tx, q, lease, failure); err != nil {
		return zero, err
	}
	execution, err := q.GetPrivacyErasureExecution(ctx, lease.Job.ExecutionID)
	if err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return execution, nil
}

func (w ExecutionWorker) failInTransaction(ctx context.Context, tx pgx.Tx, q *dbgen.Queries, lease ExecutionLease, failure ExecutionFailure) error {
	if !validExecutionFailure(failure) {
		return ErrInvalid
	}
	classification := string(failure.Classification)
	if lease.Job.AttemptCount >= w.maxAttempts() {
		classification = string(FailureTerminal)
	}
	retryMilliseconds := int64(0)
	if classification == string(FailureRetryable) {
		retryMilliseconds = retryDelay(lease.Job.AttemptCount).Milliseconds()
	}
	var jobID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM (SELECT privacy_worker_fail_job($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) AS id) result WHERE id IS NOT NULL`,
		lease.Job.ID, lease.Job.ActiveLeaseID, lease.Job.ActiveAttemptID, lease.Job.LeaseEpoch, w.WorkerRef,
		classification, retryMilliseconds, string(failure.Stage), string(failure.Code), slices.Clone(failure.DiagnosticDigest)).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	job, err := q.GetPrivacyErasureCategoryJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job.LeaseEpoch != lease.Job.LeaseEpoch {
		return ErrLeaseLost
	}
	_, err = syncExecutionAndRequest(ctx, q, lease, w.WorkerRef)
	return err
}

func validLease(lease ExecutionLease) bool {
	return lease.Job.ID != uuid.Nil && lease.Job.ExecutionID != uuid.Nil && lease.Job.ActiveLeaseID != uuid.Nil &&
		lease.Job.ActiveAttemptID != uuid.Nil && lease.Job.LeaseEpoch > 0 && lease.Job.AttemptCount > 0
}

func validateClaimedWork(job ExecutionJob, checkpoints []dbgen.PrivacyErasureJobCheckpoint) error {
	if job.Status != "LEASED" || !validLease(ExecutionLease{Job: job}) || len(job.EntrySha256) != sha256.Size ||
		!policyKey.MatchString(job.CategoryKey) || !policyKey.MatchString(job.PurposeCode) || len(checkpoints) == 0 || len(checkpoints) > 100 {
		return ErrExecutorUnavailable
	}
	seen := make(map[string]bool, len(checkpoints))
	for index, checkpoint := range checkpoints {
		if checkpoint.JobID != job.ID || checkpoint.OperationPosition != int16(index+1) || checkpoint.ActionVersion != SupportedActionVersion ||
			!supportedOperation(checkpoint.OperationCode) || seen[checkpoint.OperationCode] ||
			(checkpoint.Status != "PENDING" && checkpoint.Status != "SUCCEEDED") {
			return ErrExecutorUnavailable
		}
		seen[checkpoint.OperationCode] = true
	}
	return nil
}

func validateClaimedWorkAgainstPlan(job ExecutionJob, checkpoints []dbgen.PrivacyErasureJobCheckpoint, execution dbgen.PrivacyErasureExecution, planRow dbgen.PrivacyRequestExecutionPlan, plan ExecutionPlan) error {
	if validateClaimedWork(job, checkpoints) != nil || execution.ID != job.ExecutionID || execution.RequestID != planRow.RequestID ||
		!slices.Equal(execution.PlanSha256, planRow.PlanSha256) || execution.ExecutorVersion != planRow.ExecutorVersion ||
		execution.SchemaVersion != planRow.SchemaVersion || execution.RequestVersionAtStart != plan.RequestVersion ||
		job.PlanEntryPosition < 1 || int(job.PlanEntryPosition) > len(plan.Entries) {
		return ErrExecutorUnavailable
	}
	entry := plan.Entries[int(job.PlanEntryPosition)-1]
	canonical, err := json.Marshal(entry)
	if err != nil {
		return ErrExecutorUnavailable
	}
	digest := sha256.Sum256(canonical)
	if !slices.Equal(job.EntrySha256, digest[:]) || job.CategoryKey != entry.Category || job.PurposeCode != entry.Purpose || len(checkpoints) != len(entry.Operations) {
		return ErrExecutorUnavailable
	}
	for index, operation := range entry.Operations {
		checkpoint := checkpoints[index]
		if checkpoint.OperationPosition != int16(index+1) || checkpoint.OperationCode != operation || checkpoint.ActionVersion != entry.ActionVersion {
			return ErrExecutorUnavailable
		}
	}
	return nil
}

func validExecutionFailure(failure ExecutionFailure) bool {
	if failure.Classification != FailureRetryable && failure.Classification != FailureTerminal {
		return false
	}
	if failure.Stage != FailureStageExecute && failure.Stage != FailureStageVerify {
		return false
	}
	if !slices.Contains([]FailureCode{FailureActionFailed, FailureDependencyUnavailable, FailureUnsupportedOperation, FailureVerificationFailed, FailureRetryLimitReached}, failure.Code) {
		return false
	}
	return len(failure.DiagnosticDigest) == 0 || len(failure.DiagnosticDigest) == sha256.Size
}

func retryDelay(attempt int32) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Minute
	for n := int32(1); n < attempt && delay < maximumRetryDelay; n++ {
		delay *= 2
		if delay > maximumRetryDelay {
			delay = maximumRetryDelay
		}
	}
	return delay
}

func syncExecutionAndRequest(ctx context.Context, q *dbgen.Queries, lease ExecutionLease, workerRef uuid.UUID) (dbgen.PrivacyErasureExecution, error) {
	// The SECURITY DEFINER routine serializes on the execution, recomputes the
	// aggregate using database time, performs the only allowed request transition,
	// and appends its metadata-only event. The worker role has no direct DML.
	syncedID, err := q.CallPrivacyWorkerSync(ctx, dbgen.CallPrivacyWorkerSyncParams{JobID: lease.Job.ID, LeaseID: lease.Job.ActiveLeaseID, AttemptID: lease.Job.ActiveAttemptID, LeaseEpoch: lease.Job.LeaseEpoch, WorkerRef: workerRef})
	if err != nil {
		return dbgen.PrivacyErasureExecution{}, err
	}
	if syncedID == uuid.Nil {
		return dbgen.PrivacyErasureExecution{}, ErrExecutorUnavailable
	}
	return q.GetPrivacyErasureExecution(ctx, syncedID)
}
