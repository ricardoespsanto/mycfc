package privacyrequests

import (
	"context"
	"encoding/json"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
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
	_, e = q.CreatePrivacyPolicy(ctx, dbgen.CreatePrivacyPolicyParams{Version: p.Version, CategoryCatalogue: catalogue, AccountClosureEnabled: p.AccountClosureEnabled, WorkingRetentionDays: &p.WorkingRetentionDays, ResponseMonths: p.ResponseMonths, ExtensionMonths: p.ExtensionMonths, AdoptedAt: stamp(s.now()), AdoptedBy: &actor, CreatedAt: stamp(s.now())})
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s Service) Activate(ctx context.Context, actor uuid.UUID, version string, enabled, fulfilmentReady bool) error {
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
	if _, e = ReadPolicy(row); e != nil {
		return e
	}
	if enabled && !fulfilmentReady {
		return ErrExecutorUnavailable
	}
	if enabled {
		var reviewers int
		e = tx.QueryRow(ctx, `SELECT count(*) FROM privacy_reviewer_grants g JOIN users u ON u.id=g.user_id WHERE g.revoked_at IS NULL AND u.is_active AND NOT u.is_dependent AND (u.date_of_birth IS NULL OR u.date_of_birth <= ($1 AT TIME ZONE 'Europe/Lisbon')::date - interval '18 years')`, s.now()).Scan(&reviewers)
		if e != nil {
			return e
		}
		if reviewers < 2 {
			return ErrForbidden
		}
	}
	_, e = q.SetPrivacyActivation(ctx, dbgen.SetPrivacyActivationParams{PolicyVersion: version, Enabled: enabled, FulfilmentReady: fulfilmentReady, UpdatedBy: actor, UpdatedAt: stamp(s.now())})
	if e != nil {
		return e
	}
	if e = q.AppendPrivacyActivationEvent(ctx, dbgen.AppendPrivacyActivationEventParams{PolicyVersion: version, ActorRef: actor, Enabled: enabled, FulfilmentReady: fulfilmentReady, OccurredAt: stamp(s.now())}); e != nil {
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
