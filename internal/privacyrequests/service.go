package privacyrequests

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"slices"
	"sort"
	"strings"
	"time"
)

var ErrRateLimited = errors.New("privacy authentication temporarily limited")
var ErrDuplicate = errors.New("an active privacy request already exists")

type Service struct {
	Pool                  *pgxpool.Pool
	Enabled               bool
	Key                   []byte
	ContactURL            string
	Now                   func() time.Time
	ExecutionCapabilities map[string]bool
	ObjectTargets         ObjectTargetProtector
	ProviderRegistry      *ProviderExecutionRegistry
	ProviderTargets       ProviderTargetProtector
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func stamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()} }
func scopeOf(r dbgen.DataErasureRequest) Scope {
	cs := make([]Category, len(r.Categories))
	for i, c := range r.Categories {
		cs[i] = Category(c)
	}
	return Scope{Kind: ScopeKind(r.ScopeKind), Categories: cs}
}
func (s Service) begin(ctx context.Context) (pgx.Tx, *dbgen.Queries, error) {
	if s.Pool == nil {
		return nil, nil, ErrInvalid
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return nil, nil, e
	}
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(110,110)"); e != nil {
		tx.Rollback(ctx)
		return nil, nil, e
	}
	return tx, dbgen.New(tx), nil
}
func lockAccounts(ctx context.Context, q *dbgen.Queries, ids ...uuid.UUID) (map[uuid.UUID]dbgen.User, error) {
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	out := map[uuid.UUID]dbgen.User{}
	for _, id := range ids {
		if _, ok := out[id]; ok {
			continue
		}
		u, e := q.GetPrivacyAccountForUpdate(ctx, id)
		if e != nil {
			return nil, ErrForbidden
		}
		out[id] = u
	}
	return out, nil
}
func adult(u dbgen.User, now time.Time) bool {
	return u.IsActive && !u.IsDependent && (!u.DateOfBirth.Valid || !u.DateOfBirth.Time.AddDate(18, 0, 0).After(now))
}
func currentRelationship(requester, subject dbgen.User, now time.Time) bool {
	if requester.ID == subject.ID {
		return requester.IsActive
	}
	return adult(requester, now) && subject.IsDependent && subject.IsActive && subject.GuardianID != nil && *subject.GuardianID == requester.ID && subject.DateOfBirth.Valid && subject.DateOfBirth.Time.AddDate(18, 0, 0).After(now)
}
func reviewer(ctx context.Context, q *dbgen.Queries, u dbgen.User, now time.Time) bool {
	if !adult(u, now) {
		return false
	}
	_, e := q.GetPrivacyReviewerGrantForShare(ctx, u.ID)
	return e == nil
}

func executor(ctx context.Context, q *dbgen.Queries, u dbgen.User, now time.Time) bool {
	if !adult(u, now) {
		return false
	}
	_, e := q.GetPrivacyExecutorGrantForShare(ctx, u.ID)
	return e == nil
}
func (s Service) Available(ctx context.Context) (AdoptedPolicy, error) {
	if !s.Enabled {
		return AdoptedPolicy{}, ErrPolicyUnresolved
	}
	return activePolicy(ctx, dbgen.New(s.Pool))
}
func activePolicy(ctx context.Context, q *dbgen.Queries) (AdoptedPolicy, error) {
	a, e := q.GetPrivacyActivation(ctx)
	if e != nil || !a.Enabled || !a.FulfilmentReady {
		return AdoptedPolicy{}, ErrPolicyUnresolved
	}
	ready, e := q.PrivacyActivationReady(ctx, a.PolicyVersion)
	if e != nil || !ready {
		return AdoptedPolicy{}, ErrPolicyUnresolved
	}
	r, e := q.GetPrivacyPolicy(ctx, a.PolicyVersion)
	if e != nil {
		return AdoptedPolicy{}, ErrPolicyUnresolved
	}
	return ReadPolicy(r)
}

// reconfirmAttempt holds the actor and keyed-IP buckets while a credential is
// checked. A failed check commits the reserved attempts; a successful check or
// any later validation error rolls them back.
type reconfirmAttempt struct {
	tx pgx.Tx
}

func (a *reconfirmAttempt) close(ctx context.Context, failed bool) error {
	if a == nil || a.tx == nil {
		return nil
	}
	tx := a.tx
	a.tx = nil
	if failed {
		return tx.Commit(ctx)
	}
	return tx.Rollback(ctx)
}

// beginReconfirmation serializes attempts sharing either bucket, reserves one
// failure in both buckets, and checks the limit before bcrypt is evaluated.
// The caller commits the reservation only when credential confirmation fails.
func (s Service) beginReconfirmation(ctx context.Context, actor uuid.UUID, ip string) (*reconfirmAttempt, error) {
	if len(s.Key) < 32 {
		return nil, ErrPolicyUnresolved
	}
	mac := hmac.New(sha256.New, s.Key)
	mac.Write([]byte("privacy-reauth-ip/v1:" + ip))
	keys := []string{"actor:" + actor.String(), "ip:" + hex.EncodeToString(mac.Sum(nil))}
	sort.Strings(keys)
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	attempt := &reconfirmAttempt{tx: tx}
	now := s.now()
	for _, key := range keys {
		var count int
		e = tx.QueryRow(ctx, `INSERT INTO privacy_request_auth_limits(bucket,window_start,attempts) VALUES($1,$2,1) ON CONFLICT(bucket) DO UPDATE SET attempts=CASE WHEN privacy_request_auth_limits.window_start <= $2 - interval '15 minutes' THEN 1 ELSE privacy_request_auth_limits.attempts+1 END, window_start=CASE WHEN privacy_request_auth_limits.window_start <= $2 - interval '15 minutes' THEN $2 ELSE privacy_request_auth_limits.window_start END RETURNING attempts`, key, now).Scan(&count)
		if e != nil {
			_ = attempt.close(ctx, false)
			return nil, e
		}
		if count > 10 {
			_ = attempt.close(ctx, false)
			return nil, ErrRateLimited
		}
	}
	return attempt, nil
}

type SubmitInput struct {
	ActorID, SubjectID, RequestKey uuid.UUID
	CredentialVersion              int64
	Password, IP, PolicyVersion    string
	Scope                          Scope
}

func (s Service) Submit(ctx context.Context, in SubmitInput) (dbgen.DataErasureRequest, error) {
	var zero dbgen.DataErasureRequest
	if !s.Enabled {
		return zero, ErrPolicyUnresolved
	}
	if in.RequestKey == uuid.Nil || in.ActorID == uuid.Nil || in.SubjectID == uuid.Nil || len(in.Password) > 1024 {
		return zero, ErrInvalid
	}
	attempt, e := s.beginReconfirmation(ctx, in.ActorID, in.IP)
	if e != nil {
		return zero, e
	}
	defer func() { _ = attempt.close(ctx, false) }()
	tx, q, e := s.begin(ctx)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	users, e := lockAccounts(ctx, q, in.ActorID)
	if e != nil {
		if commitErr := attempt.close(ctx, true); commitErr != nil {
			return zero, commitErr
		}
		return zero, e
	}
	actor := users[in.ActorID]
	now := s.now()
	if !adult(actor, now) || actor.CredentialVersion != in.CredentialVersion || actor.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*actor.PasswordHash), []byte(in.Password)) != nil {
		if commitErr := attempt.close(ctx, true); commitErr != nil {
			return zero, commitErr
		}
		return zero, ErrForbidden
	}
	if e = attempt.close(ctx, false); e != nil {
		return zero, e
	}
	users, e = lockAccounts(ctx, q, in.SubjectID)
	if e != nil {
		return zero, e
	}
	subject := users[in.SubjectID]
	if !subject.IsActive {
		return zero, ErrForbidden
	}
	if !currentRelationship(actor, subject, now) {
		return zero, ErrForbidden
	}
	p, e := activePolicy(ctx, q)
	if e != nil || p.Version != in.PolicyVersion {
		return zero, ErrPolicyUnresolved
	}
	if _, e = p.Snapshot(in.Scope); e != nil {
		return zero, e
	}
	if _, e = Receive(Actor{ID: actor.ID, Active: true}, true, true, subject.ID, in.Scope, true, now); e != nil {
		return zero, e
	}
	previous, e := q.GetPrivacyRequestByIdempotency(ctx, dbgen.GetPrivacyRequestByIdempotencyParams{RequesterUserID: &actor.ID, IdempotencyKey: in.RequestKey})
	if e == nil {
		if previous.SubjectUserID == nil || *previous.SubjectUserID != subject.ID || previous.ScopeKind != string(in.Scope.Kind) || !sameCategories(previous.Categories, in.Scope.Categories) {
			return zero, ErrInvalid
		}
		return previous, tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return zero, e
	}
	categories := make([]string, len(in.Scope.Categories))
	for i, c := range in.Scope.Categories {
		categories[i] = string(c)
	}
	kind := "SELF"
	if subject.ID != actor.ID {
		kind = "DEPENDANT"
	}
	r, e := q.CreatePrivacyRequest(ctx, dbgen.CreatePrivacyRequestParams{PublicRef: uuid.New(), IdempotencyKey: in.RequestKey, SubjectUserID: &subject.ID, RequesterUserID: &actor.ID, SubjectKind: kind, ScopeKind: string(in.Scope.Kind), Categories: categories, ReceivedAt: stamp(now), DueAt: stamp(CalendarDeadline(now, 1))})
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) && pe.Code == "23505" {
			return zero, ErrDuplicate
		}
		return zero, e
	}
	snapshot, _ := json.Marshal(p)
	_, e = tx.Exec(ctx, "UPDATE data_erasure_requests SET policy_version=$2,policy_snapshot=$3 WHERE id=$1", r.ID, p.Version, snapshot)
	if e != nil {
		return zero, e
	}
	r.PolicyVersion = &p.Version
	r.PolicySnapshot = snapshot
	event, e := q.AppendPrivacyRequestEvent(ctx, dbgen.AppendPrivacyRequestEventParams{RequestID: r.ID, ActorRole: "REQUESTER", ActorRef: s.auditActor(r.ID, actor.ID), Action: "RECEIVED", ReasonCode: "REQUEST_RECEIVED", ToStatus: r.Status, Version: r.Version, OccurredAt: stamp(now)})
	if e != nil {
		return zero, e
	}
	if e = s.enqueue(ctx, q, r, actor, event.ID, "PRIVACY_ACKNOWLEDGEMENT", now); e != nil {
		return zero, e
	}
	return r, tx.Commit(ctx)
}
func sameCategories(a []string, b []Category) bool {
	if len(a) != len(b) {
		return false
	}
	for _, v := range a {
		if !containsCategory(b, v) {
			return false
		}
	}
	return true
}
func (s Service) enqueue(ctx context.Context, q *dbgen.Queries, r dbgen.DataErasureRequest, u dbgen.User, event uuid.UUID, kind string, now time.Time) error {
	if u.Email == nil {
		return ErrInvalid
	}
	payload, e := SealDelivery(s.Key, Delivery{Recipient: *u.Email, ContactURL: s.ContactURL})
	if e != nil {
		return e
	}
	_, e = q.EnqueuePrivacyRequestEmail(ctx, dbgen.EnqueuePrivacyRequestEmailParams{MessageType: kind, PrivacyRequestID: &r.ID, PrivacyRequesterID: r.RequesterUserID, PrivacyEventKey: &event, SealedPayload: payload, CreatedAt: stamp(now)})
	return e
}

type View struct {
	Record                            dbgen.DataErasureRequest
	Subject, Requester                dbgen.User
	Policy                            AdoptedPolicy
	Plan                              *ExecutionPlan
	Execution                         *dbgen.PrivacyErasureExecution
	Events                            []dbgen.DataErasureRequestEvent
	Dependants                        []dbgen.User
	Resolutions                       []dbgen.PrivacyRequestDependantResolution
	ExecutionBlockers                 []string
	SafeReceipt, CanCancel, CanReview bool
	CanViewExecution, CanExecute      bool
}

func verification(ctx context.Context, q *dbgen.Queries, r dbgen.DataErasureRequest, requester, subject dbgen.User, now time.Time) (Verification, error) {
	identityUpdatedAt, err := q.GetPrivacyIdentityUpdatedAt(ctx, subject.ID)
	if err != nil {
		return Verification{}, err
	}
	identityCurrent := r.IdentityVerifiedAt.Valid && identityUpdatedAt.Valid && !r.IdentityVerifiedAt.Time.Before(identityUpdatedAt.Time)
	return Verification{IdentityVerified: identityCurrent, CurrentRelationship: currentRelationship(requester, subject, now), RepresentationVerified: r.RepresentationVerifiedAt.Valid && r.RepresentationGuardianID != nil && subject.GuardianID != nil && *r.RepresentationGuardianID == *subject.GuardianID && r.RepresentationRelationshipUpdatedAt.Valid && r.RepresentationRelationshipUpdatedAt.Time.Equal(subject.UpdatedAt.Time), Conflict: r.RepresentationConflict}, nil
}
func (s Service) View(ctx context.Context, actor, ref uuid.UUID, management bool) (View, error) {
	var v View
	tx, q, e := s.begin(ctx)
	if e != nil {
		return v, e
	}
	defer tx.Rollback(ctx)
	r, e := q.GetPrivacyRequestForUpdate(ctx, ref)
	if e != nil || r.RequesterUserID == nil || r.SubjectUserID == nil {
		return v, ErrForbidden
	}
	us, e := lockAccounts(ctx, q, actor, *r.RequesterUserID, *r.SubjectUserID)
	if e != nil {
		return v, e
	}
	a, requester, subject := us[actor], us[*r.RequesterUserID], us[*r.SubjectUserID]
	now := s.now()
	executorEligible := false
	if !a.IsActive {
		return v, ErrForbidden
	}
	if management {
		isReviewer := reviewer(ctx, q, a, now)
		isExecutor := executor(ctx, q, a, now)
		if (!isReviewer && !isExecutor) || actor == requester.ID || actor == subject.ID {
			return v, ErrForbidden
		}
		v.CanReview = isReviewer
		executorEligible = isExecutor && (r.DecidedBy == nil || actor != *r.DecidedBy)
		v.CanViewExecution = executorEligible
		v.CanExecute = executorEligible
	} else {
		historicalReceipt := requester.ID != subject.ID && subject.GuardianID != nil && *subject.GuardianID == requester.ID && executionStatus(r.Status)
		if actor != requester.ID || (!currentRelationship(requester, subject, now) && !historicalReceipt) {
			return v, ErrForbidden
		}
	}
	v.Record = r
	checks, e := verification(ctx, q, r, requester, subject, now)
	if e != nil {
		return View{}, e
	}
	v.SafeReceipt = !management && requester.ID != subject.ID && (!checks.IdentityVerified || !checks.RepresentationVerified || checks.Conflict || !checks.CurrentRelationship)
	v.CanCancel = !management && (r.Status == "RECEIVED" || r.Status == "UNDER_REVIEW" || r.Status == "AWAITING_EXECUTION" || r.Status == "PARTIALLY_APPROVED") && !checks.Conflict && (!v.SafeReceipt || r.Status == "RECEIVED" || r.Status == "UNDER_REVIEW")
	if v.SafeReceipt {
		status := "RECEIVED"
		if executionStatus(r.Status) {
			status = r.Status
		}
		v.Record = dbgen.DataErasureRequest{PublicRef: r.PublicRef, ReceivedAt: r.ReceivedAt, Version: r.Version, Status: status}
		return v, tx.Commit(ctx)
	}
	v.Subject = subject
	v.Requester = requester
	if json.Unmarshal(r.PolicySnapshot, &v.Policy) != nil || v.Policy.validateCompatible() != nil {
		return View{}, ErrPolicyUnresolved
	}
	v.Events, e = q.ListPrivacyRequestEvents(ctx, r.ID)
	if e != nil {
		return View{}, e
	}
	if r.Status == "AWAITING_EXECUTION" || r.Status == "PARTIALLY_APPROVED" || executionStatus(r.Status) {
		planRow, planErr := q.GetPrivacyExecutionPlan(ctx, r.ID)
		if planErr != nil {
			return View{}, ErrPolicyUnresolved
		}
		plan, planErr := ReadExecutionPlan(planRow)
		if planErr != nil {
			return View{}, planErr
		}
		v.Plan = &plan
		v.CanExecute = v.CanExecute && s.ExecutionCapabilitiesReady(plan)
		executionRow, executionErr := q.GetPrivacyErasureExecutionByRequest(ctx, r.ID)
		if executionErr == nil {
			v.Execution = &executionRow
		} else if !errors.Is(executionErr, pgx.ErrNoRows) {
			return View{}, executionErr
		}
	}
	if management && r.ScopeKind == string(AccountClosure) {
		v.Dependants, e = q.ListPrivacyDependantsForUpdate(ctx, &subject.ID)
		if e != nil {
			return View{}, e
		}
		v.Resolutions, e = q.ListPrivacyDependantResolutions(ctx, r.ID)
		if e != nil {
			return View{}, e
		}
	}
	if management && v.Plan != nil && v.Execution == nil && (r.Status == string(AwaitingExecution) || r.Status == string(PartiallyApproved)) {
		v.ExecutionBlockers, e = s.executionViewBlockers(ctx, tx, q, r, requester, subject, checks, v, executorEligible)
		if e != nil {
			return View{}, e
		}
		v.CanExecute = len(v.ExecutionBlockers) == 0
	}
	return v, tx.Commit(ctx)
}

func (s Service) executionViewBlockers(ctx context.Context, tx pgx.Tx, q *dbgen.Queries, r dbgen.DataErasureRequest, requester, subject dbgen.User, checks Verification, v View, executorEligible bool) ([]string, error) {
	blockers := make([]string, 0, 8)
	add := func(code string) {
		if !slices.Contains(blockers, code) {
			blockers = append(blockers, code)
		}
	}
	if !executorEligible {
		add("EXECUTOR_AUTHORITY_OR_SEPARATION")
	}
	if !checks.IdentityVerified {
		add("IDENTITY_CHANGED")
	}
	if !checks.CurrentRelationship {
		add("RELATIONSHIP_CHANGED")
	}
	if requester.ID != subject.ID && (!checks.RepresentationVerified || checks.Conflict) {
		add("REPRESENTATION_CHANGED")
	}
	if r.DecidedBy == nil || !r.DecidedAt.Valid {
		add("DECISION_AUTHORITY")
	} else if ok, err := historicalReviewerAuthority(ctx, tx, *r.DecidedBy, r.DecidedAt.Time); err != nil {
		return nil, err
	} else if !ok {
		add("DECISION_AUTHORITY")
	}
	if v.Plan == nil || !s.ExecutionCapabilitiesReady(*v.Plan) {
		add("CAPABILITIES_UNAVAILABLE")
	}
	activation, err := q.GetPrivacyActivation(ctx)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		add("ACTIVATION_DISABLED")
	} else if ready, readyErr := q.PrivacyActivationReady(ctx, activation.PolicyVersion); !executionActivationReady(activation) || readyErr != nil || !ready {
		add("ACTIVATION_DISABLED")
	}
	if r.ScopeKind != string(AccountClosure) {
		return blockers, nil
	}
	admin, err := q.IsPrivacyAdministrator(ctx, subject.ID)
	if err != nil {
		return nil, err
	}
	if admin {
		count, countErr := q.CountPrivacyActiveAdministrators(ctx)
		if countErr != nil {
			return nil, countErr
		}
		if count <= 1 {
			add("ADMIN_CONTINUITY")
		}
	}
	legacy, err := q.CountActiveUnindexedPrivacySessions(ctx)
	if err != nil {
		return nil, err
	}
	if legacy > 0 {
		add("LEGACY_SESSIONS")
	}
	related, err := lockRelatedClosureExecutions(ctx, tx, v.Resolutions)
	if err != nil {
		return nil, err
	}
	for _, dependant := range v.Dependants {
		resolved := false
		for _, resolution := range v.Resolutions {
			if resolution.DependantID != dependant.ID || resolution.GuardianIDSnapshot != subject.ID ||
				!resolution.RelationshipUpdatedAt.Valid || !dependant.UpdatedAt.Valid ||
				!resolution.RelationshipUpdatedAt.Time.Equal(dependant.UpdatedAt.Time) ||
				resolution.ResolutionCode != "SEPARATE_APPROVED_REQUEST" || resolution.RelatedRequestID == nil {
				continue
			}
			child, ok := related[*resolution.RelatedRequestID]
			resolved = ok && child.GraphValid && child.SubjectID != nil && *child.SubjectID == dependant.ID &&
				child.ScopeKind == string(AccountClosure) && (child.Status == string(Processing) || child.Status == string(Completed))
		}
		if !resolved {
			add("DEPENDANTS_UNRESOLVED")
		}
	}
	return blockers, nil
}
func (s Service) List(ctx context.Context, actor uuid.UUID, management bool, status, deadline, order string) ([]dbgen.DataErasureRequest, error) {
	tx, q, e := s.begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	u, e := q.GetPrivacyAccountForUpdate(ctx, actor)
	if e != nil || !u.IsActive {
		return nil, ErrForbidden
	}
	var rows []dbgen.DataErasureRequest
	if management {
		if !reviewer(ctx, q, u, s.now()) && !executor(ctx, q, u, s.now()) {
			return nil, ErrForbidden
		}
		rows, e = q.ListPrivacyReviewQueue(ctx, dbgen.ListPrivacyReviewQueueParams{StatusFilter: status, ReviewerID: &actor, DeadlineFilter: deadline, OrderFilter: order, NowAt: stamp(s.now()), SoonAt: stamp(s.now().AddDate(0, 0, 7))})
	} else {
		rows, e = q.ListPrivacyRequestsForRequester(ctx, &actor)
	}
	if e != nil {
		return nil, e
	}
	out := make([]dbgen.DataErasureRequest, 0, len(rows))
	for _, r := range rows {
		if r.SubjectUserID == nil {
			continue
		}
		if !management && *r.SubjectUserID != actor {
			subject, e := q.GetPrivacyAccountForUpdate(ctx, *r.SubjectUserID)
			historicalReceipt := e == nil && subject.GuardianID != nil && *subject.GuardianID == actor && executionStatus(r.Status)
			if e != nil || (!currentRelationship(u, subject, s.now()) && !historicalReceipt) {
				continue
			}
			checks, verificationErr := verification(ctx, q, r, u, subject, s.now())
			if verificationErr != nil {
				return nil, verificationErr
			}
			if !checks.IdentityVerified || !checks.RepresentationVerified || checks.Conflict || !checks.CurrentRelationship {
				r.Status = "RECEIVED"
				r.DueAt = pgtype.Timestamptz{}
				r.ExtendedDueAt = pgtype.Timestamptz{}
			}
		}
		out = append(out, dbgen.DataErasureRequest{PublicRef: r.PublicRef, ReceivedAt: r.ReceivedAt, DueAt: r.DueAt, ExtendedDueAt: r.ExtendedDueAt, Status: r.Status})
	}
	return out, tx.Commit(ctx)
}

func executionStatus(status string) bool {
	return status == "PROCESSING" || status == "RETRYABLE_FAILED" || status == "TERMINAL_FAILED" || status == "COMPLETED"
}
func (s Service) Subjects(ctx context.Context, actor uuid.UUID) ([]dbgen.User, error) {
	tx, q, e := s.begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	u, e := q.GetPrivacyAccountForUpdate(ctx, actor)
	if e != nil || !adult(u, s.now()) {
		return nil, ErrForbidden
	}
	ds, e := q.ListPrivacyDependantsForUpdate(ctx, &actor)
	if e != nil {
		return nil, e
	}
	out := []dbgen.User{u}
	for _, d := range ds {
		if currentRelationship(u, d, s.now()) {
			out = append(out, d)
		}
	}
	return out, tx.Commit(ctx)
}

type ReviewInput struct {
	ActorID, Reference                                          uuid.UUID
	Version                                                     int64
	Action, PolicyVersion, IdentityMethod, RepresentationMethod string
	IdentityVerified, RepresentationVerified, Conflict          bool
	Explanation                                                 string
	Decisions                                                   map[string]CategoryDecision
	ExtensionMonths                                             int
	ExtensionReason                                             string
	DependantID, RelatedReference                               uuid.UUID
	ResolutionCode, ResolutionExplanation                       string
}

func (s Service) Change(ctx context.Context, in ReviewInput) (dbgen.DataErasureRequest, error) {
	var zero dbgen.DataErasureRequest
	tx, q, e := s.begin(ctx)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	r, e := q.GetPrivacyRequestForUpdate(ctx, in.Reference)
	if e != nil || r.RequesterUserID == nil || r.SubjectUserID == nil {
		return zero, ErrForbidden
	}
	us, e := lockAccounts(ctx, q, in.ActorID, *r.RequesterUserID, *r.SubjectUserID)
	if e != nil {
		return zero, e
	}
	a, requester, subject := us[in.ActorID], us[*r.RequesterUserID], us[*r.SubjectUserID]
	now := s.now().UTC().Truncate(time.Microsecond)
	checks, e := verification(ctx, q, r, requester, subject, now)
	if e != nil {
		return zero, e
	}
	isReviewer := reviewer(ctx, q, a, now)
	if in.Action == "cancel" {
		if a.ID != requester.ID || !a.IsActive || !checks.CurrentRelationship {
			return zero, ErrForbidden
		}
	} else if !isReviewer || a.ID == requester.ID || a.ID == subject.ID {
		return zero, ErrForbidden
	}
	if in.Version != r.Version {
		return zero, ErrStaleVersion
	}
	if r.Status == "REFUSED" || r.Status == "CANCELLED" {
		return zero, ErrInvalidTransition
	}
	var p AdoptedPolicy
	if json.Unmarshal(r.PolicySnapshot, &p) != nil || p.validateCompatible() != nil {
		return zero, ErrPolicyUnresolved
	}
	scope := scopeOf(r)
	policy, e := p.Snapshot(scope)
	if e != nil {
		return zero, e
	}
	before := r.Status
	eventAction, reason := "", ""
	notify := false
	var executionPlan *ExecutionPlan
	if in.Action != "cancel" && in.Action != "extend" && r.Status != "RECEIVED" && r.Status != "UNDER_REVIEW" {
		return zero, ErrInvalidTransition
	}
	if in.Action != "claim" && in.Action != "cancel" && (r.ClaimedBy == nil || *r.ClaimedBy != a.ID) {
		return zero, ErrForbidden
	}
	switch in.Action {
	case "claim":
		if r.ClaimedBy != nil {
			return zero, ErrInvalidTransition
		}
		r.Status = "UNDER_REVIEW"
		r.ClaimedBy = &a.ID
		r.ReviewedAt = stamp(now)
		eventAction = "CLAIMED"
		reason = "REVIEW_CLAIMED"
	case "identity-needed":
		r.IdentityVerifiedAt = pgtype.Timestamptz{}
		r.IdentityVerifiedBy = nil
		r.IdentityMethod = nil
		clearRepresentation(&r)
		eventAction = "IDENTITY_REQUESTED"
		reason = "VERIFICATION_REQUIRED"
		notify = true
	case "verify":
		if !in.IdentityVerified || !slices.Contains([]string{"IN_PERSON", "EXISTING_CHANNEL", "DOCUMENT_CHECK"}, in.IdentityMethod) {
			return zero, ErrInvalid
		}
		r.IdentityVerifiedAt = stamp(now)
		r.IdentityVerifiedBy = &a.ID
		r.IdentityMethod = &in.IdentityMethod
		clearRepresentation(&r)
		r.RepresentationConflict = in.Conflict
		if requester.ID != subject.ID && in.RepresentationVerified {
			if !checks.CurrentRelationship || !slices.Contains([]string{"IN_PERSON", "DOCUMENT_CHECK"}, in.RepresentationMethod) || in.Conflict {
				return zero, ErrVerification
			}
			r.RepresentationVerifiedAt = stamp(now)
			r.RepresentationVerifiedBy = &a.ID
			r.RepresentationMethod = &in.RepresentationMethod
			r.RepresentationGuardianID = subject.GuardianID
			r.RepresentationRelationshipUpdatedAt = subject.UpdatedAt
		}
		eventAction = "IDENTITY_VERIFIED"
		reason = "VERIFICATION_RECORDED"
		if in.Conflict {
			eventAction = "REPRESENTATION_CONFLICT"
			reason = "REPRESENTATION_CONFLICT"
		}
	case "extend":
		if (r.Status != "RECEIVED" && r.Status != "UNDER_REVIEW") || r.ExtendedDueAt.Valid || now.After(r.DueAt.Time) || in.ExtensionMonths < 1 || in.ExtensionMonths > 2 || !slices.Contains([]string{"COMPLEXITY", "REQUEST_VOLUME"}, in.ExtensionReason) {
			return zero, ErrInvalid
		}
		r.ExtendedDueAt = stamp(CalendarDeadline(r.ReceivedAt.Time, 1+in.ExtensionMonths))
		r.ExtensionReasonCode = &in.ExtensionReason
		eventAction = "DEADLINE_EXTENDED"
		reason = in.ExtensionReason
		notify = true
	case "resolve-dependant":
		if r.ScopeKind != string(AccountClosure) || !checks.IdentityVerified || checks.Conflict {
			return zero, ErrVerification
		}
		if e = resolveDependant(ctx, q, r, a, subject, in, now); e != nil {
			return zero, e
		}
		eventAction = "DEPENDANT_RESOLVED"
		reason = "DEPENDANT_RESOLVED"
	case "approve", "partial", "refuse", "cancel":
		c := Case{RequesterID: requester.ID, SubjectID: subject.ID, Scope: scope, Status: Status(r.Status), Version: r.Version, ReceivedAt: r.ReceivedAt.Time, UpdatedAt: r.UpdatedAt.Time}
		if c.Status == AwaitingExecution || c.Status == PartiallyApproved {
			c.DecisionPolicy = &policy
			c.DecidedBy = r.DecidedBy
		}
		cmd := Command{ExpectedVersion: r.Version, Actor: Actor{ID: a.ID, Active: a.IsActive, PrivacyReviewer: isReviewer}, Verification: checks, Policy: policy, At: now}
		if in.Action == "cancel" {
			cmd.Action = Cancel
		} else {
			if in.PolicyVersion != p.Version || !bounded(in.Explanation, 2000) {
				return zero, ErrInvalid
			}
			decisions, plan, e := p.DecisionPlan(scope, in.Decisions, in.Action, now)
			if e != nil {
				return zero, e
			}
			plan.RequestVersion = r.Version + 1
			executionPlan = &plan
			r.CategoryDecisions, _ = json.Marshal(decisions)
			r.DecisionExplanation = strings.TrimSpace(in.Explanation)
			cmd.Action = Approve
			if in.Action == "partial" {
				cmd.Action = PartialApprove
			}
			if in.Action == "refuse" {
				cmd.Action = Refuse
			}
			if scope.Kind == AccountClosure && in.Action != "refuse" {
				cmd.Safeguards, e = closureSafeguards(ctx, q, r, subject)
				if e != nil {
					return zero, e
				}
			}
		}
		result, e := Transition(c, cmd)
		if e != nil {
			return zero, e
		}
		r.Status = string(result.Case.Status)
		r.ClosedAt = stamp(result.Case.ClosedAt)
		r.EvidenceExpiresAt = stamp(result.Case.EvidenceExpiresAt)
		if in.Action == "cancel" {
			r.CancelledAt = stamp(now)
			eventAction = "CANCELLED"
			reason = "REQUESTER_CANCELLED"
		} else {
			code := "APPROVED"
			if in.Action == "partial" {
				code = "PARTIALLY_APPROVED"
			}
			if in.Action == "refuse" {
				code = "REFUSED"
			}
			r.DecisionCode = &code
			r.DecidedBy = &a.ID
			r.DecidedAt = stamp(now)
			eventAction = code
			reason = "POLICY_DECISION"
		}
		if r.ClosedAt.Valid {
			r.WorkingExpiresAt = stamp(CalendarDeadline(now, 0).AddDate(0, 0, int(p.WorkingRetentionDays)))
		}
		notify = true
	default:
		return zero, ErrInvalidTransition
	}
	r.UpdatedAt = stamp(now)
	r, e = saveCase(ctx, q, r, in.Version)
	if e != nil {
		return zero, e
	}
	if executionPlan != nil {
		planJSON, marshalErr := json.Marshal(executionPlan)
		if marshalErr != nil {
			return zero, ErrPolicyUnresolved
		}
		digest := executionPlanDigest(r.ID, now, executionPlan.PolicyVersion, executionPlan.ExecutorVersion, executionPlan.SchemaVersion, planJSON)
		storedPlan, createErr := q.CreatePrivacyExecutionPlan(ctx, dbgen.CreatePrivacyExecutionPlanParams{RequestID: r.ID, PolicyVersion: executionPlan.PolicyVersion, ExecutorVersion: executionPlan.ExecutorVersion, SchemaVersion: executionPlan.SchemaVersion, Plan: planJSON, PlanSha256: digest, CreatedAt: stamp(now)})
		if createErr != nil {
			return zero, createErr
		}
		if _, e = ReadExecutionPlan(storedPlan); e != nil {
			return zero, e
		}
	}
	role := "REVIEWER"
	if in.Action == "cancel" {
		role = "REQUESTER"
	}
	ev, e := q.AppendPrivacyRequestEvent(ctx, dbgen.AppendPrivacyRequestEventParams{RequestID: r.ID, ActorRole: role, ActorRef: s.auditActor(r.ID, a.ID), Action: eventAction, ReasonCode: reason, FromStatus: &before, ToStatus: r.Status, Version: r.Version, OccurredAt: stamp(now)})
	if e != nil {
		return zero, e
	}
	if notify {
		if e = s.enqueue(ctx, q, r, requester, ev.ID, "PRIVACY_DECISION", now); e != nil {
			return zero, e
		}
	}
	return r, tx.Commit(ctx)
}
func clearRepresentation(r *dbgen.DataErasureRequest) {
	r.RepresentationVerifiedAt = pgtype.Timestamptz{}
	r.RepresentationVerifiedBy = nil
	r.RepresentationMethod = nil
	r.RepresentationGuardianID = nil
	r.RepresentationRelationshipUpdatedAt = pgtype.Timestamptz{}
}
func saveCase(ctx context.Context, q *dbgen.Queries, r dbgen.DataErasureRequest, version int64) (dbgen.DataErasureRequest, error) {
	return q.UpdatePrivacyRequest(ctx, dbgen.UpdatePrivacyRequestParams{ID: r.ID, ExpectedVersion: version, Status: r.Status, ExtendedDueAt: r.ExtendedDueAt, ExtensionReasonCode: r.ExtensionReasonCode, ClaimedBy: r.ClaimedBy, ReviewedAt: r.ReviewedAt, IdentityVerifiedAt: r.IdentityVerifiedAt, IdentityMethod: r.IdentityMethod, IdentityVerifiedBy: r.IdentityVerifiedBy, RepresentationRelationshipUpdatedAt: r.RepresentationRelationshipUpdatedAt, RepresentationVerifiedAt: r.RepresentationVerifiedAt, RepresentationMethod: r.RepresentationMethod, RepresentationVerifiedBy: r.RepresentationVerifiedBy, RepresentationGuardianID: r.RepresentationGuardianID, RepresentationConflict: r.RepresentationConflict, DecisionCode: r.DecisionCode, DecisionExplanation: r.DecisionExplanation, CategoryDecisions: r.CategoryDecisions, DecidedBy: r.DecidedBy, DecidedAt: r.DecidedAt, PolicyVersion: r.PolicyVersion, PolicySnapshot: r.PolicySnapshot, ClosedAt: r.ClosedAt, CancelledAt: r.CancelledAt, EvidenceExpiresAt: r.EvidenceExpiresAt, WorkingExpiresAt: r.WorkingExpiresAt, UpdatedAt: r.UpdatedAt})
}
func resolveDependant(ctx context.Context, q *dbgen.Queries, r dbgen.DataErasureRequest, actor, subject dbgen.User, in ReviewInput, now time.Time) error {
	if !bounded(in.ResolutionExplanation, 2000) || !slices.Contains([]string{"VERIFIED_TRANSFER", "SEPARATE_APPROVED_REQUEST"}, in.ResolutionCode) {
		return ErrInvalid
	}
	d, e := q.GetPrivacyAccountForUpdate(ctx, in.DependantID)
	if e != nil || d.ID == actor.ID || d.GuardianID == nil || *d.GuardianID != subject.ID {
		return ErrForbidden
	}
	// A verified transfer must already have occurred through the guardian workflow;
	// currently linked dependants require a separate approved request or formal resolution.
	if in.ResolutionCode == "VERIFIED_TRANSFER" {
		return ErrClosureSafeguards
	}
	var related *uuid.UUID
	if in.ResolutionCode == "SEPARATE_APPROVED_REQUEST" {
		rr, e := q.GetPrivacyRequestByRef(ctx, in.RelatedReference)
		if e != nil || rr.SubjectUserID == nil || *rr.SubjectUserID != d.ID || !slices.Contains([]string{"PROCESSING", "COMPLETED"}, rr.Status) || rr.ScopeKind != string(AccountClosure) || rr.DecidedBy == nil || *rr.DecidedBy == d.ID {
			return ErrClosureSafeguards
		}
		related = &rr.ID
	}
	_, e = q.UpsertPrivacyDependantResolution(ctx, dbgen.UpsertPrivacyDependantResolutionParams{RequestID: r.ID, DependantID: d.ID, GuardianIDSnapshot: subject.ID, RelationshipUpdatedAt: d.UpdatedAt, ResolutionCode: in.ResolutionCode, RelatedRequestID: related, VerifiedBy: actor.ID, VerifiedAt: stamp(now), Explanation: strings.TrimSpace(in.ResolutionExplanation)})
	return e
}
func closureSafeguards(ctx context.Context, q *dbgen.Queries, r dbgen.DataErasureRequest, subject dbgen.User) (ClosureSafeguards, error) {
	out := ClosureSafeguards{DependantsResolved: true, PreservesAdministrator: true}
	admin, e := q.IsPrivacyAdministrator(ctx, subject.ID)
	if e != nil {
		return out, e
	}
	if admin {
		n, e := q.CountPrivacyActiveAdministrators(ctx)
		if e != nil {
			return out, e
		}
		out.PreservesAdministrator = n > 1
	}
	ds, e := q.ListPrivacyDependantsForUpdate(ctx, &subject.ID)
	if e != nil {
		return out, e
	}
	rs, e := q.ListPrivacyDependantResolutions(ctx, r.ID)
	if e != nil {
		return out, e
	}
	for _, d := range ds {
		resolved := false
		for _, x := range rs {
			if x.DependantID != d.ID || x.GuardianIDSnapshot != subject.ID || !x.RelationshipUpdatedAt.Time.Equal(d.UpdatedAt.Time) {
				continue
			}
			if x.ResolutionCode == "SEPARATE_APPROVED_REQUEST" && x.RelatedRequestID != nil {
				rr, e := q.GetPrivacyRequest(ctx, *x.RelatedRequestID)
				if e != nil {
					return out, e
				}
				resolved = slices.Contains([]string{"PROCESSING", "COMPLETED"}, rr.Status) && rr.SubjectUserID != nil && *rr.SubjectUserID == d.ID && rr.ScopeKind == string(AccountClosure)
			}
		}
		if !resolved {
			out.DependantsResolved = false
		}
	}
	return out, nil
}

func (s Service) auditActor(caseID, actorID uuid.UUID) uuid.UUID {
	mac := hmac.New(sha256.New, s.Key)
	mac.Write([]byte("privacy-case-actor/v1:" + caseID.String() + ":" + actorID.String()))
	var id uuid.UUID
	copy(id[:], mac.Sum(nil))
	return id
}

// CanReview is a navigation hint. Mutations independently recheck the grant
// under transaction locks; rendering ordinary pages must never acquire those locks.
func (s Service) CanReview(ctx context.Context, actor uuid.UUID) (bool, error) {
	var allowed bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM users u JOIN privacy_reviewer_grants g ON g.user_id = u.id
		WHERE u.id = $1 AND u.is_active AND NOT u.is_dependent
		AND (u.date_of_birth IS NULL OR u.date_of_birth <= $2)
		AND g.revoked_at IS NULL
	)`, actor, s.now().AddDate(-18, 0, 0).Format("2006-01-02")).Scan(&allowed)
	return allowed, err
}

// CanExecute is a navigation hint only. StartExecution rechecks the explicit
// grant, active-adult status, four-eyes separation, and every case invariant in
// its transaction.
func (s Service) CanExecute(ctx context.Context, actor uuid.UUID) (bool, error) {
	var allowed bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM users u JOIN privacy_executor_grants g ON g.user_id = u.id
		WHERE u.id = $1 AND u.is_active AND NOT u.is_dependent
		AND (u.date_of_birth IS NULL OR u.date_of_birth <= $2)
		AND g.revoked_at IS NULL
	)`, actor, s.now().AddDate(-18, 0, 0).Format("2006-01-02")).Scan(&allowed)
	return allowed, err
}
