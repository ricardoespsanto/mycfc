package privacyrequests

import (
	"context"
	"encoding/json"
	"errors"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

// Operator actions require an explicitly supplied active adult administrator.
// They do not grant ordinary administrators any case-review capability.
func operator(ctx context.Context, q *dbgen.Queries, actor uuid.UUID, s Service) error {
	u, e := q.GetPrivacyAccountForUpdate(ctx, actor)
	if e != nil || !adult(u, s.now()) {
		return ErrForbidden
	}
	ok, e := q.IsPrivacyAdministrator(ctx, actor)
	if e != nil {
		return e
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}
func (s Service) GrantReviewer(ctx context.Context, actor, target uuid.UUID, revoke bool) error {
	tx, q, e := s.begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	us, e := lockAccounts(ctx, q, actor, target)
	if e != nil {
		return e
	}
	if e = operator(ctx, q, actor, s); e != nil {
		return e
	}
	if !adult(us[target], s.now()) && !revoke {
		return ErrForbidden
	}
	now := stamp(s.now())
	var grant dbgen.PrivacyReviewerGrant
	action := "GRANTED"
	if revoke {
		action = "REVOKED"
		grant, e = q.GetPrivacyReviewerGrantForShare(ctx, target)
		if e != nil {
			return e
		}
		grant, e = q.RevokePrivacyReviewer(ctx, dbgen.RevokePrivacyReviewerParams{ID: grant.ID, RevokedBy: &actor, RevokedAt: now})
	} else {
		grant, e = q.GrantPrivacyReviewer(ctx, dbgen.GrantPrivacyReviewerParams{UserID: target, GrantedBy: actor, GrantedAt: now})
	}
	if e != nil {
		return e
	}
	if e = q.AppendPrivacyReviewerGrantEvent(ctx, dbgen.AppendPrivacyReviewerGrantEventParams{GrantID: grant.ID, ActorRef: actor, Action: action, OccurredAt: now}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s Service) ImportPolicy(ctx context.Context, actor uuid.UUID, p AdoptedPolicy) error {
	if e := p.Validate(); e != nil {
		return e
	}
	tx, q, e := s.begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = operator(ctx, q, actor, s); e != nil {
		return e
	}
	catalogue, _ := json.Marshal(p.Categories)
	_, e = q.CreatePrivacyPolicy(ctx, dbgen.CreatePrivacyPolicyParams{Version: p.Version, CategoryCatalogue: catalogue, ExecutorVersion: &p.ExecutorVersion, PlanSchemaVersion: &p.PlanSchemaVersion, AccountClosureEnabled: p.AccountClosureEnabled, WorkingRetentionDays: &p.WorkingRetentionDays, ResponseMonths: p.ResponseMonths, ExtensionMonths: p.ExtensionMonths, AdoptedAt: stamp(s.now()), AdoptedBy: &actor, CreatedAt: stamp(s.now())})
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s Service) Activate(ctx context.Context, actor uuid.UUID, version string, enabled bool) error {
	tx, q, e := s.begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = operator(ctx, q, actor, s); e != nil {
		return e
	}
	row, e := q.GetPrivacyPolicy(ctx, version)
	if e != nil {
		return e
	}
	policy, e := ReadPolicy(row)
	if e != nil {
		return e
	}
	if enabled && policy.WorkingRetentionDays != ApprovedWorkingRetentionDays {
		return ErrPolicyUnresolved
	}
	// #243 defines and freezes an executable contract, but it does not install
	// the #111 executor or its evidence verifier. No operator assertion can
	// substitute for those capabilities.
	if enabled {
		return ErrExecutorUnavailable
	}
	_, e = q.SetPrivacyActivation(ctx, dbgen.SetPrivacyActivationParams{PolicyVersion: version, Enabled: false, FulfilmentReady: false, UpdatedBy: actor, UpdatedAt: stamp(s.now())})
	if e != nil {
		return e
	}
	if e = q.AppendPrivacyActivationEvent(ctx, dbgen.AppendPrivacyActivationEventParams{PolicyVersion: version, ActorRef: actor, Enabled: false, FulfilmentReady: false, OccurredAt: stamp(s.now())}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func (s Service) AddRetentionException(ctx context.Context, actor, reference, owner uuid.UUID, category, reason, evidence string) error {
	if (reason != "COMPLAINT" && reason != "LEGAL_HOLD") || !policyKey.MatchString(category) || !policyKey.MatchString(evidence) {
		return ErrInvalid
	}
	now := s.now()
	tx, q, e := s.begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = operator(ctx, q, actor, s); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(110,110)"); e != nil {
		return e
	}
	var requestID uuid.UUID
	var status string
	var workingErasedAt *time.Time
	e = tx.QueryRow(ctx, "SELECT id,status,working_erased_at FROM data_erasure_requests WHERE public_ref=$1 FOR UPDATE", reference).Scan(&requestID, &status, &workingErasedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrInvalid
	}
	if e != nil {
		return e
	}
	// Category retention exceptions only attach to a reviewer's executable
	// refusal plan. Cancellation has no category decision plan and therefore
	// cannot be reinterpreted as one by an operator after the fact.
	if status != "REFUSED" || workingErasedAt != nil {
		return ErrInvalidTransition
	}
	planRow, e := q.GetPrivacyExecutionPlan(ctx, requestID)
	if e != nil {
		return ErrPolicyUnresolved
	}
	plan, e := ReadExecutionPlan(planRow)
	if e != nil {
		return e
	}
	var selected *ExecutionPlanEntry
	for i := range plan.Entries {
		if plan.Entries[i].Category == category {
			selected = &plan.Entries[i]
			break
		}
	}
	if selected == nil || selected.Outcome != "RETAIN" || selected.Ground != reason || selected.LegalGround != reason || selected.Owner != "PRIVACY" || len(selected.RetainedFields) == 0 {
		return ErrPolicyUnresolved
	}
	reviewAt, reviewErr := time.Parse(time.RFC3339Nano, selected.ReviewAt)
	expiresAt, expiryErr := time.Parse(time.RFC3339Nano, selected.ExpireAt)
	if reviewErr != nil || expiryErr != nil || !reviewAt.After(now) || expiresAt.Before(reviewAt) {
		return ErrPolicyUnresolved
	}
	var ownerAllowed bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN privacy_reviewer_grants g ON g.user_id=u.id AND g.revoked_at IS NULL WHERE u.id=$1 AND u.is_active AND NOT u.is_dependent AND (u.date_of_birth IS NULL OR u.date_of_birth <= ($2 AT TIME ZONE 'Europe/Lisbon')::date - interval '18 years'))`, owner, now).Scan(&ownerAllowed)
	if e != nil {
		return e
	}
	if !ownerAllowed {
		return ErrForbidden
	}
	_, e = tx.Exec(ctx, `INSERT INTO privacy_request_retention_exceptions(request_id,owner_ref,actor_ref,category_key,purpose_code,retained_field_codes,evidence_ref,reason_code,review_at,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, requestID, owner, actor, selected.Category, selected.Purpose, selected.RetainedFields, evidence, reason, reviewAt, expiresAt, now)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}

type ExpiryResult struct{ WorkingRecords, EvidenceRecords int }

func (s Service) Expire(ctx context.Context, actor uuid.UUID) (ExpiryResult, error) {
	var out ExpiryResult
	tx, q, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	if e = operator(ctx, q, actor, s); e != nil {
		return out, e
	}
	e = tx.QueryRow(ctx, "SELECT working_records,evidence_records FROM expire_privacy_request_records($1)", actor).Scan(&out.WorkingRecords, &out.EvidenceRecords)
	if e != nil {
		return out, e
	}
	return out, tx.Commit(ctx)
}
