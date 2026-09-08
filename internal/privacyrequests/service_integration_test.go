//go:build integration

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
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPrivacyServiceTransactions(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	// Isolate all fixtures, including the singleton activation and ADMIN count,
	// from the shared integration database and from previous test invocations.
	admin, e := pgx.Connect(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close(ctx)
	schemaName := "privacy_service_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	schema := pgx.Identifier{schemaName}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schemaName + ",public"
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	baseline, e := os.ReadFile("../db/schema.sql")
	if e != nil {
		t.Fatal(e)
	}
	isolatedBaseline := strings.ReplaceAll(string(baseline), "public.", schemaName+".")
	isolatedBaseline = strings.ReplaceAll(isolatedBaseline, "SET search_path = pg_catalog, public", "SET search_path = pg_catalog, "+schemaName)
	if _, e = pool.Exec(ctx, isolatedBaseline); e != nil {
		t.Fatal(e)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("privacy-test-password"), bcrypt.MinCost)
	user := func(guardian *uuid.UUID) uuid.UUID {
		id := uuid.New()
		var email, ph *string
		dob := "2015-01-01"
		if guardian == nil {
			emailValue := id.String() + "@example.test"
			passwordHash := string(hash)
			email = &emailValue
			ph = &passwordHash
			dob = "1990-01-01"
		}
		_, e := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth,is_dependent,guardian_id)VALUES($1,'Pessoa teste',$2,$3,$4,$5,$6)`, id, email, ph, dob, guardian != nil, guardian)
		if e != nil {
			t.Fatal(e)
		}
		return id
	}
	owner, reviewerA, reviewerB := user(nil), user(nil), user(nil)
	_, e = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id)SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, owner)
	if e != nil {
		t.Fatal(e)
	}
	capabilities := map[string]bool{}
	for _, profile := range executionProfiles {
		for _, operation := range profile.Operations {
			capabilities[operation] = true
		}
	}
	s := Service{Pool: pool, Enabled: true, Key: []byte(strings.Repeat("k", 32)), ContactURL: "https://example.test/legal/direitos", ExecutionCapabilities: capabilities}
	p := testPolicy()
	p.Version = "test-" + uuid.NewString()
	p.WorkingRetentionDays = ApprovedWorkingRetentionDays
	unsupported := p
	unsupported.Version = "test-unsupported-" + uuid.NewString()
	unsupported.ExecutorVersion = "privacy-erasure-executor/v9"
	if e = s.ImportPolicy(ctx, owner, unsupported); !errors.Is(e, ErrPolicyUnresolved) {
		t.Fatalf("unsupported executor policy imported: %v", e)
	}
	if e = s.ImportPolicy(ctx, owner, p); e != nil {
		t.Fatal(e)
	}
	for _, r := range []uuid.UUID{reviewerA, reviewerB} {
		if e = s.GrantReviewer(ctx, owner, r, false); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.GrantExecutor(ctx, owner, reviewerB, false); e != nil {
		t.Fatal(e)
	}
	wrongRetention := p
	wrongRetention.Version = "test-wrong-retention-" + uuid.NewString()
	wrongRetention.WorkingRetentionDays = ApprovedWorkingRetentionDays - 1
	if e = s.ImportPolicy(ctx, owner, wrongRetention); e != nil {
		t.Fatal(e)
	}
	if e = s.Activate(ctx, owner, wrongRetention.Version, true); !errors.Is(e, ErrPolicyUnresolved) {
		t.Fatalf("activate wrong working retention: %v", e)
	}
	if e = s.Activate(ctx, owner, p.Version, true); !errors.Is(e, ErrExecutorUnavailable) {
		t.Fatalf("activation trusted operator assertion: %v", e)
	}
	// Isolated integration fixture: production enablement remains impossible
	// until #111 supplies validated live capabilities and evidence.
	_, e = pool.Exec(ctx, `INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by,updated_at) VALUES(true,$1,true,true,$2,now())`, p.Version, owner)
	if e != nil {
		t.Fatal(e)
	}
	t.Run("navigation-lookup-does-not-wait-for-workflow-lock", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(110,110)"); err != nil {
			t.Fatal(err)
		}
		lookupCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		allowed, err := s.CanReview(lookupCtx, reviewerA)
		if err != nil || !allowed {
			t.Fatalf("reviewer navigation blocked: allowed=%v err=%v", allowed, err)
		}
		allowed, err = s.CanReview(lookupCtx, owner)
		if err != nil || allowed {
			t.Fatalf("ordinary admin navigation: allowed=%v err=%v", allowed, err)
		}
		allowed, err = s.CanReview(lookupCtx, uuid.New())
		if err != nil || allowed {
			t.Fatalf("missing account navigation: allowed=%v err=%v", allowed, err)
		}
		allowed, err = s.CanExecute(lookupCtx, reviewerB)
		if err != nil || !allowed {
			t.Fatalf("executor navigation: allowed=%v err=%v", allowed, err)
		}
		allowed, err = s.CanExecute(lookupCtx, owner)
		if err != nil || allowed {
			t.Fatalf("ordinary admin executor navigation: allowed=%v err=%v", allowed, err)
		}
	})
	t.Run("execution-blockers-fail-closed-without-a-decision", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		account := dbgen.User{ID: uuid.New()}
		blockers, err := s.executionViewBlockers(ctx, tx, dbgen.New(tx), dbgen.DataErasureRequest{ScopeKind: string(Categories)}, account, account, Verification{}, View{}, false)
		if err != nil || !slices.Contains(blockers, "DECISION_AUTHORITY") {
			t.Fatalf("missing decision blockers=%v err=%v", blockers, err)
		}
	})
	t.Run("execution-helper-query-errors-fail-closed", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		q := dbgen.New(tx)
		requestID, dependantID := uuid.New(), uuid.New()
		request := dbgen.DataErasureRequest{ID: requestID, SubjectUserID: &dependantID, ScopeKind: string(AccountClosure)}
		if reviewer(ctx, q, dbgen.User{}, time.Now()) || executor(ctx, q, dbgen.User{}, time.Now()) {
			t.Fatal("ineligible account received privacy authority")
		}
		if _, err = executionClosureAccountIDs(cancelled, tx, q, request); err == nil {
			t.Fatal("closure account query error was ignored")
		}
		if _, err = lockRelatedClosureExecutions(cancelled, tx, []dbgen.PrivacyRequestDependantResolution{{RelatedRequestID: &requestID}}); err == nil {
			t.Fatal("related execution query error was ignored")
		}
		missingRelated, err := lockRelatedClosureExecutions(ctx, tx, []dbgen.PrivacyRequestDependantResolution{{RelatedRequestID: &requestID}})
		if err != nil || len(missingRelated) != 0 {
			t.Fatalf("missing related execution=%v err=%v", missingRelated, err)
		}
		if _, err = exactPersistedExecutionGraph(cancelled, tx, uuid.New(), executionPlanFixture(t)); err == nil {
			t.Fatal("persisted graph query error was ignored")
		}
		if _, err = strictExecutionClosureSafeguards(cancelled, tx, q, request, dbgen.User{ID: dependantID}, map[uuid.UUID]dbgen.User{}, time.Now()); err == nil {
			t.Fatal("closure safeguard query error was ignored")
		}
		if _, err = closureSafeguards(cancelled, q, request, dbgen.User{ID: dependantID}); err == nil {
			t.Fatal("review closure safeguard query error was ignored")
		}
		if _, _, err = createExecutionWorkGraph(cancelled, q, uuid.New(), executionPlanFixture(t), time.Now()); err == nil {
			t.Fatal("work graph query error was ignored")
		}
		if err = recordAccessRevocation(cancelled, q, uuid.New(), "STAFF_GRANT", "COACH", 1, uuid.New(), time.Now()); err == nil {
			t.Fatal("access revocation query error was ignored")
		}
		if err = s.cutOffPrivacyAccount(cancelled, q, dbgen.PrivacyErasureExecution{ID: uuid.New(), RequestID: requestID}, dbgen.User{ID: dependantID}, reviewerB, time.Now()); err == nil {
			t.Fatal("account cutoff query error was ignored")
		}
	})
	submit := func(actor, subject uuid.UUID, kind ScopeKind) (dbgen.DataErasureRequest, error) {
		scope := Scope{Kind: kind}
		if kind == Categories {
			scope.Categories = []Category{"identity-core", "profile-core"}
		}
		return s.Submit(ctx, SubmitInput{ActorID: actor, SubjectID: subject, RequestKey: uuid.New(), CredentialVersion: 1, Password: "privacy-test-password", IP: actor.String(), PolicyVersion: p.Version, Scope: scope})
	}
	claimVerify := func(r dbgen.DataErasureRequest, representative bool) dbgen.DataErasureRequest {
		r, e = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "claim"})
		if e != nil {
			t.Fatal(e)
		}
		r, e = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "verify", IdentityVerified: true, IdentityMethod: "IN_PERSON", RepresentationVerified: representative, RepresentationMethod: "IN_PERSON"})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	decisions := map[string]CategoryDecision{"identity-core": {Outcome: "APPROVE"}, "profile-core": {Outcome: "APPROVE"}}
	closureDecisions := make(map[string]CategoryDecision, len(p.Categories))
	for _, category := range p.Categories {
		closureDecisions[category.Key] = CategoryDecision{Outcome: "APPROVE"}
	}
	t.Run("execution-view-revalidates-visible-blockers", func(t *testing.T) {
		subject := user(nil)
		request, err := submit(subject, subject, Categories)
		if err != nil {
			t.Fatal(err)
		}
		request = claimVerify(request, false)
		request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Categorias aprovadas para a vista de execução.", Decisions: decisions})
		if err != nil {
			t.Fatal(err)
		}
		view, err := s.View(ctx, reviewerB, request.PublicRef, true)
		if err != nil || !view.CanViewExecution || !view.CanExecute || view.Plan == nil || len(view.ExecutionBlockers) != 0 {
			t.Fatalf("ready execution view=%+v err=%v", view, err)
		}

		withoutCapabilities := s
		withoutCapabilities.ExecutionCapabilities = nil
		view, err = withoutCapabilities.View(ctx, reviewerB, request.PublicRef, true)
		if err != nil || !slices.Contains(view.ExecutionBlockers, "CAPABILITIES_UNAVAILABLE") {
			t.Fatalf("capability blockers=%v err=%v", view.ExecutionBlockers, err)
		}
		if _, err = pool.Exec(ctx, `UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false WHERE singleton`); err != nil {
			t.Fatal(err)
		}
		view, err = s.View(ctx, reviewerB, request.PublicRef, true)
		if err != nil || !slices.Contains(view.ExecutionBlockers, "ACTIVATION_DISABLED") {
			t.Fatalf("activation blockers=%v err=%v", view.ExecutionBlockers, err)
		}
		if _, err = pool.Exec(ctx, `DELETE FROM privacy_request_activation WHERE singleton`); err != nil {
			t.Fatal(err)
		}
		view, err = s.View(ctx, reviewerB, request.PublicRef, true)
		if err != nil || !slices.Contains(view.ExecutionBlockers, "ACTIVATION_DISABLED") {
			t.Fatalf("missing activation blockers=%v err=%v", view.ExecutionBlockers, err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by,updated_at) VALUES(true,$1,true,true,$2,now())`, p.Version, owner); err != nil {
			t.Fatal(err)
		}
		var originalGrantedAt time.Time
		if err = pool.QueryRow(ctx, `SELECT granted_at FROM privacy_reviewer_grants WHERE user_id=$1 AND revoked_at IS NULL`, reviewerA).Scan(&originalGrantedAt); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE privacy_reviewer_grants SET granted_at=(SELECT decided_at+interval '1 second' FROM data_erasure_requests WHERE id=$1) WHERE user_id=$2 AND revoked_at IS NULL`, request.ID, reviewerA); err != nil {
			t.Fatal(err)
		}
		view, err = s.View(ctx, reviewerB, request.PublicRef, true)
		if err != nil || !slices.Contains(view.ExecutionBlockers, "DECISION_AUTHORITY") {
			t.Fatalf("decision authority blockers=%v err=%v", view.ExecutionBlockers, err)
		}
		if _, err = pool.Exec(ctx, `UPDATE privacy_reviewer_grants SET granted_at=$2 WHERE user_id=$1 AND revoked_at IS NULL`, reviewerA, originalGrantedAt); err != nil {
			t.Fatal(err)
		}
		view, err = s.View(ctx, reviewerA, request.PublicRef, true)
		if err != nil || !slices.Contains(view.ExecutionBlockers, "EXECUTOR_AUTHORITY_OR_SEPARATION") {
			t.Fatalf("authority blockers=%v err=%v", view.ExecutionBlockers, err)
		}
		if _, err = pool.Exec(ctx, `UPDATE users SET name='Identidade alterada',updated_at=updated_at+interval '1 second' WHERE id=$1`, subject); err != nil {
			t.Fatal(err)
		}
		view, err = s.View(ctx, reviewerB, request.PublicRef, true)
		if err != nil || !slices.Contains(view.ExecutionBlockers, "IDENTITY_CHANGED") {
			t.Fatalf("identity blockers=%v err=%v", view.ExecutionBlockers, err)
		}

		guardian, replacementGuardian := user(nil), user(nil)
		dependant := user(&guardian)
		represented, err := submit(guardian, dependant, Categories)
		if err != nil {
			t.Fatal(err)
		}
		represented = claimVerify(represented, true)
		represented, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: represented.PublicRef, Version: represented.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Representação aprovada antes da mudança.", Decisions: decisions})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE users SET guardian_id=$2,updated_at=updated_at+interval '1 second' WHERE id=$1`, dependant, replacementGuardian); err != nil {
			t.Fatal(err)
		}
		view, err = s.View(ctx, reviewerB, represented.PublicRef, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, blocker := range []string{"RELATIONSHIP_CHANGED", "REPRESENTATION_CHANGED"} {
			if !slices.Contains(view.ExecutionBlockers, blocker) {
				t.Errorf("representation blockers=%v missing=%s", view.ExecutionBlockers, blocker)
			}
		}

		closingAdmin := user(nil)
		if _, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, closingAdmin); err != nil {
			t.Fatal(err)
		}
		closure, err := submit(closingAdmin, closingAdmin, AccountClosure)
		if err != nil {
			t.Fatal(err)
		}
		closure = claimVerify(closure, false)
		closure, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: closure.PublicRef, Version: closure.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Encerramento aprovado para revalidar bloqueios.", Decisions: closureDecisions})
		if err != nil {
			t.Fatal(err)
		}
		_ = user(&closingAdmin)
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN' ON CONFLICT DO NOTHING`, owner)
			_, _ = pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=$1`, closingAdmin)
			_, _ = pool.Exec(ctx, `DELETE FROM sessions WHERE NOT subject_indexed`)
		})
		if _, err = pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=$1`, owner); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($1,'legacy',now()+interval '1 hour',NULL,false)`, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		view, err = s.View(ctx, reviewerB, closure.PublicRef, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, blocker := range []string{"ADMIN_CONTINUITY", "LEGACY_SESSIONS", "DEPENDANTS_UNRESOLVED"} {
			if !slices.Contains(view.ExecutionBlockers, blocker) {
				t.Errorf("closure blockers=%v missing=%s", view.ExecutionBlockers, blocker)
			}
		}
		if _, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN' ON CONFLICT DO NOTHING`, owner); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `DELETE FROM sessions WHERE NOT subject_indexed`); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=$1`, closingAdmin); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("availability-and-operator-deactivation", func(t *testing.T) {
		available, err := s.Available(ctx)
		if err != nil || available.Version != p.Version {
			t.Fatalf("available policy=%q err=%v", available.Version, err)
		}
		disabled := s
		disabled.Enabled = false
		if _, err = disabled.Available(ctx); !errors.Is(err, ErrPolicyUnresolved) {
			t.Fatalf("disabled workflow available: %v", err)
		}
		if err = s.Activate(ctx, reviewerA, p.Version, false); !errors.Is(err, ErrForbidden) {
			t.Fatalf("non-admin deactivated policy: %v", err)
		}
		if err = s.Activate(ctx, owner, "missing-policy", false); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("missing policy deactivation: %v", err)
		}
		if err = s.Activate(ctx, owner, p.Version, false); err != nil {
			t.Fatal(err)
		}
		var enabled, ready bool
		if err = pool.QueryRow(ctx, `SELECT enabled,fulfilment_ready FROM privacy_request_activation WHERE singleton`).Scan(&enabled, &ready); err != nil {
			t.Fatal(err)
		}
		if enabled || ready {
			t.Fatalf("deactivation persisted enabled=%v ready=%v", enabled, ready)
		}
		if _, err = s.Available(ctx); !errors.Is(err, ErrPolicyUnresolved) {
			t.Fatalf("deactivated policy available: %v", err)
		}
		var events int
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM privacy_request_activation_events WHERE policy_version=$1 AND NOT enabled`, p.Version).Scan(&events); err != nil || events != 1 {
			t.Fatalf("deactivation events=%d err=%v", events, err)
		}
		// Restore the deliberately isolated fixture without pretending that a
		// production activation can bypass the unavailable #111 executor.
		if _, err = pool.Exec(ctx, `UPDATE privacy_request_activation SET enabled=true,fulfilment_ready=true WHERE singleton`); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("executor-grants-require-adults-and-record-revocation", func(t *testing.T) {
		guardian := user(nil)
		minor := user(&guardian)
		if err := s.GrantExecutor(ctx, owner, minor, false); !errors.Is(err, ErrForbidden) {
			t.Fatalf("minor executor grant error=%v", err)
		}
		candidate := user(nil)
		if err := s.GrantExecutor(ctx, reviewerA, candidate, false); !errors.Is(err, ErrForbidden) {
			t.Fatalf("non-operator executor grant error=%v", err)
		}
		if err := s.GrantExecutor(ctx, owner, candidate, false); err != nil {
			t.Fatal(err)
		}
		if err := s.GrantExecutor(ctx, owner, candidate, true); err != nil {
			t.Fatal(err)
		}
		if err := s.GrantExecutor(ctx, owner, candidate, true); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("duplicate executor revocation error=%v", err)
		}
		var events int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM privacy_executor_grant_events event JOIN privacy_executor_grants executor_grant ON executor_grant.id=event.grant_id WHERE executor_grant.user_id=$1`, candidate).Scan(&events); err != nil || events != 2 {
			t.Fatalf("executor grant events=%d err=%v", events, err)
		}
		if err := s.GrantExecutor(ctx, owner, uuid.New(), true); !errors.Is(err, ErrForbidden) {
			t.Fatalf("missing executor revocation error=%v", err)
		}
	})
	t.Run("subjects-and-requester-list-recheck-relationships", func(t *testing.T) {
		guardian := user(nil)
		minor := user(&guardian)
		adultDependant := user(&guardian)
		inactiveDependant := user(&guardian)
		if _, err := pool.Exec(ctx, `UPDATE users SET date_of_birth='2000-01-01' WHERE id=$1`, adultDependant); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, inactiveDependant); err != nil {
			t.Fatal(err)
		}
		subjects, err := s.Subjects(ctx, guardian)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[uuid.UUID]bool{}
		for _, subject := range subjects {
			seen[subject.ID] = true
		}
		if len(subjects) != 2 || !seen[guardian] || !seen[minor] || seen[adultDependant] || seen[inactiveDependant] {
			t.Fatalf("subjects=%v", seen)
		}
		if _, err = s.Subjects(ctx, minor); !errors.Is(err, ErrForbidden) {
			t.Fatalf("minor listed subjects: %v", err)
		}
		if _, err = s.Subjects(ctx, uuid.New()); !errors.Is(err, ErrForbidden) {
			t.Fatalf("missing account listed subjects: %v", err)
		}

		r, err := submit(guardian, minor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := s.List(ctx, guardian, false, "ignored", "ignored", "ignored")
		if err != nil || len(rows) != 1 || rows[0].PublicRef != r.PublicRef || rows[0].Status != "RECEIVED" || rows[0].DueAt.Valid {
			t.Fatalf("redacted requester list=%+v err=%v", rows, err)
		}
		r = claimVerify(r, true)
		rows, err = s.List(ctx, guardian, false, "", "", "")
		if err != nil || len(rows) != 1 || rows[0].Status != "UNDER_REVIEW" || !rows[0].DueAt.Valid {
			t.Fatalf("verified requester list=%+v err=%v", rows, err)
		}
		newGuardian := user(nil)
		if _, err = pool.Exec(ctx, `UPDATE users SET guardian_id=$2,updated_at=now()+interval '1 second' WHERE id=$1`, minor, newGuardian); err != nil {
			t.Fatal(err)
		}
		rows, err = s.List(ctx, guardian, false, "", "", "")
		if err != nil || len(rows) != 0 {
			t.Fatalf("former guardian list=%+v err=%v", rows, err)
		}
		if _, err = s.List(ctx, uuid.New(), false, "", "", ""); !errors.Is(err, ErrForbidden) {
			t.Fatalf("missing requester listed cases: %v", err)
		}
	})
	t.Run("view-failures-and-closure-detail", func(t *testing.T) {
		actor := user(nil)
		r, err := submit(actor, actor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.View(ctx, actor, uuid.New(), false); !errors.Is(err, ErrForbidden) {
			t.Fatalf("missing case view: %v", err)
		}
		if _, err = s.View(ctx, uuid.New(), r.PublicRef, false); !errors.Is(err, ErrForbidden) {
			t.Fatalf("missing actor view: %v", err)
		}
		if _, err = s.View(ctx, actor, r.PublicRef, true); !errors.Is(err, ErrForbidden) {
			t.Fatalf("subject self-reviewed: %v", err)
		}
		if _, err = pool.Exec(ctx, `UPDATE data_erasure_requests SET policy_snapshot='{"version":"broken"}' WHERE id=$1`, r.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.View(ctx, actor, r.PublicRef, false); !errors.Is(err, ErrPolicyUnresolved) {
			t.Fatalf("corrupt snapshot view: %v", err)
		}

		guardian := user(nil)
		minor := user(&guardian)
		closure, err := submit(guardian, guardian, AccountClosure)
		if err != nil {
			t.Fatal(err)
		}
		view, err := s.View(ctx, reviewerA, closure.PublicRef, true)
		if err != nil || !view.CanReview || len(view.Dependants) != 1 || view.Dependants[0].ID != minor {
			t.Fatalf("closure view=%+v err=%v", view, err)
		}
	})
	t.Run("identity-recheck-is-durable-and-generic", func(t *testing.T) {
		actor := user(nil)
		r, err := submit(actor, actor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		r, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "claim"})
		if err != nil {
			t.Fatal(err)
		}
		r, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "identity-needed"})
		if err != nil || r.Status != "UNDER_REVIEW" || r.IdentityVerifiedAt.Valid || r.IdentityVerifiedBy != nil {
			t.Fatalf("identity recheck=%+v err=%v", r, err)
		}
		var events, notices int
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM data_erasure_request_events WHERE request_id=$1 AND action='IDENTITY_REQUESTED'`, r.ID).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE privacy_request_id=$1 AND message_type='PRIVACY_DECISION'`, r.ID).Scan(&notices); err != nil {
			t.Fatal(err)
		}
		if events != 1 || notices != 1 {
			t.Fatalf("identity recheck events=%d notices=%d", events, notices)
		}
	})
	t.Run("dependant-resolution-requires-a-current-approved-case", func(t *testing.T) {
		guardian := user(nil)
		minor := user(&guardian)
		childRequest, err := submit(guardian, minor, AccountClosure)
		if err != nil {
			t.Fatal(err)
		}
		childRequest = claimVerify(childRequest, true)
		childRequest, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: childRequest.PublicRef, Version: childRequest.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Pedido separado do dependente aprovado.", Decisions: closureDecisions})
		if err != nil || childRequest.Status != "AWAITING_EXECUTION" {
			t.Fatalf("child request=%+v err=%v", childRequest, err)
		}

		parentRequest, err := submit(guardian, guardian, AccountClosure)
		if err != nil {
			t.Fatal(err)
		}
		parentRequest = claimVerify(parentRequest, false)
		base := ReviewInput{ActorID: reviewerA, Reference: parentRequest.PublicRef, Version: parentRequest.Version, Action: "resolve-dependant", DependantID: minor, ResolutionExplanation: "Resolução verificada para o dependente."}
		invalid := base
		invalid.ResolutionCode = "INVENTED"
		if _, err = s.Change(ctx, invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invented resolution: %v", err)
		}
		invalid = base
		invalid.ResolutionCode = "FORMAL_RESOLUTION"
		invalid.DependantID = uuid.New()
		if _, err = s.Change(ctx, invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("obsolete formal resolution: %v", err)
		}
		invalid = base
		invalid.ResolutionCode = "VERIFIED_TRANSFER"
		if _, err = s.Change(ctx, invalid); !errors.Is(err, ErrClosureSafeguards) {
			t.Fatalf("untransferred relationship: %v", err)
		}
		invalid = base
		invalid.ResolutionCode = "SEPARATE_APPROVED_REQUEST"
		invalid.RelatedReference = uuid.New()
		if _, err = s.Change(ctx, invalid); !errors.Is(err, ErrClosureSafeguards) {
			t.Fatalf("missing related request: %v", err)
		}
		valid := base
		valid.ResolutionCode = "SEPARATE_APPROVED_REQUEST"
		valid.RelatedReference = childRequest.PublicRef
		if _, err = s.Change(ctx, valid); !errors.Is(err, ErrClosureSafeguards) {
			t.Fatalf("merely approved related request: %v", err)
		}
		if _, err = s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: childRequest.PublicRef, Version: childRequest.Version, Confirmed: true}); err != nil {
			t.Fatalf("start child closure: %v", err)
		}
		parentRequest, err = s.Change(ctx, valid)
		if err != nil {
			t.Fatal(err)
		}
		parentRequest, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: parentRequest.PublicRef, Version: parentRequest.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Encerramento aprovado após resolução separada.", Decisions: closureDecisions})
		if err != nil || parentRequest.Status != "AWAITING_EXECUTION" {
			t.Fatalf("parent request=%+v err=%v", parentRequest, err)
		}
	})
	t.Run("change-validation-rolls-back-every-partial-write", func(t *testing.T) {
		if _, err := s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: uuid.New(), Version: 1, Action: "claim"}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("missing request: %v", err)
		}

		actor := user(nil)
		r, err := submit(actor, actor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Change(ctx, ReviewInput{ActorID: uuid.New(), Reference: r.PublicRef, Version: r.Version, Action: "claim"}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("missing reviewer: %v", err)
		}
		if _, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "verify", IdentityVerified: true, IdentityMethod: "IN_PERSON"}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("unclaimed verification: %v", err)
		}
		r, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "claim"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "verify", IdentityVerified: true, IdentityMethod: "INVENTED"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invented identity method: %v", err)
		}
		if _, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Decisão incompleta.", Decisions: map[string]CategoryDecision{"identity-core": {Outcome: "APPROVE"}}}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("incomplete decision: %v", err)
		}
		if _, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "invented"}); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("invented transition: %v", err)
		}

		guardian := user(nil)
		minor := user(&guardian)
		represented, err := submit(guardian, minor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		represented, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: represented.PublicRef, Version: represented.Version, Action: "claim"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: represented.PublicRef, Version: represented.Version, Action: "verify", IdentityVerified: true, IdentityMethod: "IN_PERSON", RepresentationVerified: true, RepresentationMethod: "EXISTING_CHANNEL"}); !errors.Is(err, ErrVerification) {
			t.Fatalf("invalid representation method: %v", err)
		}
		represented, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: represented.PublicRef, Version: represented.Version, Action: "verify", IdentityVerified: true, IdentityMethod: "IN_PERSON", Conflict: true})
		if err != nil || !represented.RepresentationConflict {
			t.Fatalf("representation conflict=%+v err=%v", represented, err)
		}
		if _, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: represented.PublicRef, Version: represented.Version, Action: "resolve-dependant", DependantID: minor, ResolutionCode: "FORMAL_RESOLUTION", ResolutionExplanation: "Not an account closure."}); !errors.Is(err, ErrVerification) {
			t.Fatalf("category resolution: %v", err)
		}

		terminalActor := user(nil)
		terminal, err := submit(terminalActor, terminalActor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		terminal, err = s.Change(ctx, ReviewInput{ActorID: terminalActor, Reference: terminal.PublicRef, Version: terminal.Version, Action: "cancel"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Change(ctx, ReviewInput{ActorID: terminalActor, Reference: terminal.PublicRef, Version: terminal.Version, Action: "cancel"}); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("terminal request changed: %v", err)
		}

		planActor := user(nil)
		planned, err := submit(planActor, planActor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		planned = claimVerify(planned, false)
		if _, err = pool.Exec(ctx, `INSERT INTO privacy_request_execution_plans(request_id,policy_version,executor_version,schema_version,plan,plan_sha256,created_at) VALUES($1,$2,$3,$4,'{}',decode(repeat('00',32),'hex'),now())`, planned.ID, p.Version, SupportedExecutorVersion, SupportedPlanSchemaVersion); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: planned.PublicRef, Version: planned.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "A duplicação do plano deve reverter.", Decisions: decisions}); err == nil {
			t.Fatal("duplicate execution plan accepted")
		}
		stored, err := dbgen.New(pool).GetPrivacyRequestByRef(ctx, planned.PublicRef)
		if err != nil || stored.Status != "UNDER_REVIEW" || stored.Version != planned.Version {
			t.Fatalf("duplicate plan escaped rollback: %+v err=%v", stored, err)
		}

		noticeActor := user(nil)
		notice, err := submit(noticeActor, noticeActor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		notice = claimVerify(notice, false)
		badNotice := s
		badNotice.ContactURL = "not-a-url"
		if _, err = badNotice.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: notice.PublicRef, Version: notice.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "A notificação inválida deve reverter.", Decisions: decisions}); err == nil {
			t.Fatal("invalid decision notice accepted")
		}
		stored, err = dbgen.New(pool).GetPrivacyRequestByRef(ctx, notice.PublicRef)
		if err != nil || stored.Status != "UNDER_REVIEW" || stored.Version != notice.Version {
			t.Fatalf("notice failure escaped rollback: %+v err=%v", stored, err)
		}
	})
	t.Run("submission-validation-and-idempotency-mismatch", func(t *testing.T) {
		actor := user(nil)
		valid := SubmitInput{ActorID: actor, SubjectID: actor, RequestKey: uuid.New(), CredentialVersion: 1, Password: "privacy-test-password", IP: actor.String(), PolicyVersion: p.Version, Scope: Scope{Kind: Categories, Categories: []Category{"identity-core"}}}
		disabled := s
		disabled.Enabled = false
		if _, err := disabled.Submit(ctx, valid); !errors.Is(err, ErrPolicyUnresolved) {
			t.Fatalf("disabled submission: %v", err)
		}
		invalid := valid
		invalid.RequestKey = uuid.Nil
		if _, err := s.Submit(ctx, invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("nil idempotency key: %v", err)
		}
		invalid = valid
		invalid.Password = strings.Repeat("x", 1025)
		if _, err := s.Submit(ctx, invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("oversized password: %v", err)
		}
		missingKey := s
		missingKey.Key = nil
		if _, err := missingKey.Submit(ctx, valid); !errors.Is(err, ErrPolicyUnresolved) {
			t.Fatalf("missing keyed throttle secret: %v", err)
		}
		invalid = valid
		invalid.ActorID = uuid.New()
		invalid.IP = invalid.ActorID.String()
		if _, err := s.Submit(ctx, invalid); !errors.Is(err, ErrForbidden) {
			t.Fatalf("missing actor: %v", err)
		}
		mac := hmac.New(sha256.New, s.Key)
		mac.Write([]byte("privacy-reauth-ip/v1:" + invalid.IP))
		if _, err := pool.Exec(ctx, `DELETE FROM privacy_request_auth_limits WHERE bucket=$1 OR bucket=$2`, "actor:"+invalid.ActorID.String(), "ip:"+hex.EncodeToString(mac.Sum(nil))); err != nil {
			t.Fatal(err)
		}
		invalid = valid
		invalid.SubjectID = uuid.New()
		if _, err := s.Submit(ctx, invalid); !errors.Is(err, ErrForbidden) {
			t.Fatalf("missing subject: %v", err)
		}
		unrelated := user(nil)
		invalid = valid
		invalid.SubjectID = unrelated
		if _, err := s.Submit(ctx, invalid); !errors.Is(err, ErrForbidden) {
			t.Fatalf("unrelated subject: %v", err)
		}
		invalid = valid
		invalid.PolicyVersion = "stale-policy"
		if _, err := s.Submit(ctx, invalid); !errors.Is(err, ErrPolicyUnresolved) {
			t.Fatalf("stale policy: %v", err)
		}
		invalid = valid
		invalid.Scope.Categories = []Category{"unknown-category"}
		if _, err := s.Submit(ctx, invalid); !errors.Is(err, ErrPolicyUnresolved) {
			t.Fatalf("unknown category: %v", err)
		}
		first, err := s.Submit(ctx, valid)
		if err != nil {
			t.Fatal(err)
		}
		mismatched := valid
		mismatched.Scope.Categories = []Category{"identity-core", "profile-core"}
		if _, err = s.Submit(ctx, mismatched); !errors.Is(err, ErrInvalid) {
			t.Fatalf("idempotency key accepted different scope: first=%s err=%v", first.ID, err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err = s.enqueue(ctx, dbgen.New(tx), dbgen.DataErasureRequest{}, dbgen.User{}, uuid.New(), "PRIVACY_ACKNOWLEDGEMENT", time.Now()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("missing notification recipient: %v", err)
		}
	})
	t.Run("operator-authorization-and-validation", func(t *testing.T) {
		nonAdmin := user(nil)
		target := user(nil)
		if err := s.GrantReviewer(ctx, owner, uuid.New(), false); !errors.Is(err, ErrForbidden) {
			t.Fatalf("missing reviewer account: %v", err)
		}
		if err := s.GrantReviewer(ctx, nonAdmin, target, false); !errors.Is(err, ErrForbidden) {
			t.Fatalf("non-admin granted reviewer: %v", err)
		}
		minor := user(&nonAdmin)
		if err := s.GrantReviewer(ctx, owner, minor, false); !errors.Is(err, ErrForbidden) {
			t.Fatalf("minor reviewer granted: %v", err)
		}
		if err := s.GrantReviewer(ctx, owner, target, true); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("missing reviewer revoked: %v", err)
		}
		if err := s.GrantReviewer(ctx, owner, reviewerA, false); err == nil {
			t.Fatal("duplicate reviewer grant accepted")
		}
		candidate := p
		candidate.Version = "unauthorized-" + uuid.NewString()
		if err := s.ImportPolicy(ctx, nonAdmin, candidate); !errors.Is(err, ErrForbidden) {
			t.Fatalf("non-admin imported policy: %v", err)
		}
		if err := s.ImportPolicy(ctx, owner, p); err == nil {
			t.Fatal("duplicate policy imported")
		}
		inactiveAdmin := user(nil)
		if _, err := pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, inactiveAdmin); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, inactiveAdmin); err != nil {
			t.Fatal(err)
		}
		if err := s.Activate(ctx, inactiveAdmin, p.Version, false); !errors.Is(err, ErrForbidden) {
			t.Fatalf("inactive admin deactivated policy: %v", err)
		}
		malformedVersion := "malformed-" + uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,account_closure_enabled,working_retention_days,response_months,extension_months,adopted_at,adopted_by) VALUES($1,'[]',$2,$3,false,$4,1,2,now(),$5)`, malformedVersion, SupportedExecutorVersion, SupportedPlanSchemaVersion, ApprovedWorkingRetentionDays, owner); err != nil {
			t.Fatal(err)
		}
		if err := s.Activate(ctx, owner, malformedVersion, false); !errors.Is(err, ErrPolicyUnresolved) {
			t.Fatalf("malformed policy deactivated: %v", err)
		}
		if err := s.AddRetentionException(ctx, owner, uuid.New(), reviewerA, "bad category", "LEGAL_HOLD", "evidence"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid category accepted: %v", err)
		}
		if err := s.AddRetentionException(ctx, owner, uuid.New(), reviewerA, "identity-core", "LEGAL_HOLD", "bad evidence"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid evidence accepted: %v", err)
		}
		if err := s.AddRetentionException(ctx, owner, uuid.New(), reviewerA, "identity-core", "LEGAL_HOLD", "case-1"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("missing case hold: %v", err)
		}
		openActor := user(nil)
		openRequest, err := submit(openActor, openActor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.AddRetentionException(ctx, owner, openRequest.PublicRef, reviewerA, "identity-core", "LEGAL_HOLD", "case-2"); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("open case hold: %v", err)
		}

		retainedActor := user(nil)
		retained, err := submit(retainedActor, retainedActor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		retained = claimVerify(retained, false)
		retained, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: retained.PublicRef, Version: retained.Version, Action: "refuse", PolicyVersion: p.Version, Explanation: "Conservação temporária aprovada.", Decisions: map[string]CategoryDecision{"identity-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}, "profile-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}}})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.AddRetentionException(ctx, nonAdmin, retained.PublicRef, reviewerA, "identity-core", "LEGAL_HOLD", "case-non-admin"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("non-admin attached hold: %v", err)
		}
		if err = s.AddRetentionException(ctx, owner, retained.PublicRef, reviewerA, "sessions", "LEGAL_HOLD", "case-wrong-category"); !errors.Is(err, ErrPolicyUnresolved) {
			t.Fatalf("unplanned category hold: %v", err)
		}
		future := s
		future.Now = func() time.Time { return time.Now().AddDate(200, 0, 0) }
		if err = future.AddRetentionException(ctx, owner, retained.PublicRef, reviewerA, "identity-core", "LEGAL_HOLD", "case-expired-plan"); !errors.Is(err, ErrPolicyUnresolved) {
			t.Fatalf("expired plan hold: %v", err)
		}
		if err = s.AddRetentionException(ctx, owner, retained.PublicRef, reviewerA, "identity-core", "LEGAL_HOLD", "case-duplicate"); err != nil {
			t.Fatal(err)
		}
		if err = s.AddRetentionException(ctx, owner, retained.PublicRef, reviewerA, "identity-core", "LEGAL_HOLD", "case-duplicate"); err == nil {
			t.Fatal("duplicate hold accepted")
		}

		missingPlanActor := user(nil)
		missingPlan, err := submit(missingPlanActor, missingPlanActor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		missingPlan = claimVerify(missingPlan, false)
		missingPlan, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: missingPlan.PublicRef, Version: missingPlan.Version, Action: "refuse", PolicyVersion: p.Version, Explanation: "Plano temporariamente removido no ensaio isolado.", Decisions: map[string]CategoryDecision{"identity-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}, "profile-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE data_erasure_requests SET closed_at=now()-interval '3 years',evidence_expires_at=now()-interval '1 day' WHERE id=$1`, missingPlan.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `DELETE FROM privacy_request_execution_plans WHERE request_id=$1`, missingPlan.ID); err != nil {
			t.Fatal(err)
		}
		if err = s.AddRetentionException(ctx, owner, missingPlan.PublicRef, reviewerA, "identity-core", "LEGAL_HOLD", "case-missing-plan"); !errors.Is(err, ErrPolicyUnresolved) {
			t.Fatalf("missing plan hold: %v", err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO privacy_request_execution_plans(request_id,policy_version,executor_version,schema_version,plan,plan_sha256,created_at) VALUES($1,$2,$3,$4,'{}',decode(repeat('00',32),'hex'),now())`, missingPlan.ID, p.Version, SupportedExecutorVersion, SupportedPlanSchemaVersion); err != nil {
			t.Fatal(err)
		}
		if err = s.AddRetentionException(ctx, owner, missingPlan.PublicRef, reviewerA, "identity-core", "LEGAL_HOLD", "case-malformed-plan"); !errors.Is(err, ErrPolicyUnresolved) {
			t.Fatalf("malformed plan hold: %v", err)
		}
		if _, err = pool.Exec(ctx, `UPDATE data_erasure_requests SET closed_at=now(),evidence_expires_at=now()+interval '2 years' WHERE id=$1`, missingPlan.ID); err != nil {
			t.Fatal(err)
		}

		databaseMinorAdmin := user(nil)
		if _, err = pool.Exec(ctx, `UPDATE users SET date_of_birth='2010-01-01' WHERE id=$1`, databaseMinorAdmin); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, databaseMinorAdmin); err != nil {
			t.Fatal(err)
		}
		future = s
		future.Now = func() time.Time { return time.Date(2040, time.January, 1, 0, 0, 0, 0, time.UTC) }
		if _, err = future.Expire(ctx, databaseMinorAdmin); err == nil {
			t.Fatal("database-side operator authorization was bypassed")
		}
		if _, err = pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=$1`, databaseMinorAdmin); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("closed-pool-transaction-boundaries", func(t *testing.T) {
		closedPool, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
		if err != nil {
			t.Fatal(err)
		}
		closedPool.Close()
		closed := Service{Pool: closedPool, Enabled: true, Key: s.Key, ContactURL: s.ContactURL}
		actor := uuid.New()
		if _, err = closed.Available(ctx); !errors.Is(err, ErrPolicyUnresolved) {
			t.Fatalf("closed pool availability: %v", err)
		}
		if _, err = closed.Subjects(ctx, actor); err == nil {
			t.Fatal("closed pool subjects succeeded")
		}
		if _, err = closed.List(ctx, actor, false, "", "", ""); err == nil {
			t.Fatal("closed pool list succeeded")
		}
		if _, err = closed.View(ctx, actor, uuid.New(), false); err == nil {
			t.Fatal("closed pool view succeeded")
		}
		if _, err = closed.Submit(ctx, SubmitInput{ActorID: actor, SubjectID: actor, RequestKey: uuid.New(), Password: "password", IP: "127.0.0.1"}); err == nil {
			t.Fatal("closed pool submission succeeded")
		}
		if err = closed.GrantReviewer(ctx, actor, uuid.New(), false); err == nil {
			t.Fatal("closed pool grant succeeded")
		}
		candidate := p
		candidate.Version = "closed-" + uuid.NewString()
		if err = closed.ImportPolicy(ctx, actor, candidate); err == nil {
			t.Fatal("closed pool import succeeded")
		}
		if err = closed.Activate(ctx, actor, p.Version, false); err == nil {
			t.Fatal("closed pool activation succeeded")
		}
		if err = closed.AddRetentionException(ctx, actor, uuid.New(), uuid.New(), "identity-core", "LEGAL_HOLD", "case-closed"); err == nil {
			t.Fatal("closed pool hold succeeded")
		}
		if _, err = closed.Expire(ctx, actor); err == nil {
			t.Fatal("closed pool expiry succeeded")
		}
		if _, err = closed.CanReview(ctx, actor); err == nil {
			t.Fatal("closed pool navigation lookup succeeded")
		}
	})
	t.Run("worker-query-errors-fail-closed", func(t *testing.T) {
		wrongSchemaConfig, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		wrongSchemaConfig.ConnConfig.RuntimeParams["search_path"] = "pg_catalog"
		wrongSchemaPool, err := pgxpool.NewWithConfig(ctx, wrongSchemaConfig)
		if err != nil {
			t.Fatal(err)
		}
		defer wrongSchemaPool.Close()
		worker := ExecutionWorker{Pool: wrongSchemaPool, WorkerRef: uuid.New(), LeaseDuration: time.Minute, MaxAttempts: 3}
		lease := ExecutionLease{Job: ExecutionJob{PrivacyErasureCategoryJob: dbgen.PrivacyErasureCategoryJob{
			ID: uuid.New(), ExecutionID: uuid.New(), LeaseEpoch: 1, AttemptCount: 1,
		}, ActiveLeaseID: uuid.New(), ActiveAttemptID: uuid.New()}}
		failure := ExecutionFailure{Classification: FailureTerminal, Stage: FailureStageExecute, Code: FailureActionFailed}
		for name, call := range map[string]func() error{
			"claim": func() error {
				_, queryErr := worker.Claim(ctx)
				return queryErr
			},
			"heartbeat": func() error {
				_, queryErr := worker.Heartbeat(ctx, lease)
				return queryErr
			},
			"checkpoint": func() error {
				_, queryErr := worker.CompleteCheckpoint(ctx, lease, "IDENTITY_CLEAR", SupportedActionVersion)
				return queryErr
			},
			"complete": func() error {
				_, queryErr := worker.CompleteJob(ctx, lease)
				return queryErr
			},
			"fail": func() error {
				_, queryErr := worker.FailJob(ctx, lease, failure)
				return queryErr
			},
		} {
			t.Run(name, func(t *testing.T) {
				if err := call(); err == nil {
					t.Fatal("missing worker schema error was ignored")
				}
			})
		}
	})
	t.Run("receipt-auth-idempotency-atomic-outbox", func(t *testing.T) {
		actor := user(nil)
		in := SubmitInput{ActorID: actor, SubjectID: actor, RequestKey: uuid.New(), CredentialVersion: 1, Password: "wrong", IP: actor.String(), PolicyVersion: p.Version, Scope: Scope{Kind: Categories, Categories: []Category{"identity-core"}}}
		if _, e = s.Submit(ctx, in); !errors.Is(e, ErrForbidden) {
			t.Fatal(e)
		}
		in.Password = "privacy-test-password"
		r, e := s.Submit(ctx, in)
		if e != nil {
			t.Fatal(e)
		}
		same, e := s.Submit(ctx, in)
		if e != nil || same.ID != r.ID {
			t.Fatal("idempotency", e)
		}
		in.RequestKey = uuid.New()
		if _, e = s.Submit(ctx, in); !errors.Is(e, ErrDuplicate) {
			t.Fatal("active duplicate", e)
		}
		var count int
		pool.QueryRow(ctx, "SELECT count(*) FROM data_erasure_request_events WHERE request_id=$1", r.ID).Scan(&count)
		if count != 1 {
			t.Fatal("duplicate audit", count)
		}
		pool.QueryRow(ctx, "SELECT count(*) FROM email_outbox WHERE privacy_request_id=$1", r.ID).Scan(&count)
		if count != 1 {
			t.Fatal("duplicate outbox", count)
		}
		if r.PublicRef == r.ID || r.PolicyVersion == nil || *r.PolicyVersion != p.Version {
			t.Fatal("reference/snapshot")
		}
		if _, e = s.View(ctx, owner, r.PublicRef, true); !errors.Is(e, ErrForbidden) {
			t.Fatal("admin fallback", e)
		}
		bad := s
		bad.ContactURL = "invalid"
		other := user(nil)
		in.ActorID = other
		in.SubjectID = other
		in.IP = other.String()
		if _, e = bad.Submit(ctx, in); e == nil {
			t.Fatal("bad outbox accepted")
		}
		pool.QueryRow(ctx, "SELECT count(*) FROM data_erasure_requests WHERE requester_user_id=$1", other).Scan(&count)
		if count != 0 {
			t.Fatal("non atomic outbox")
		}
	})
	t.Run("stale-credential-and-reauth-throttle-boundary", func(t *testing.T) {
		actor := user(nil)
		ip := "203.0.113.110"
		clock := s
		now := time.Now().UTC()
		clock.Now = func() time.Time { return now }
		in := SubmitInput{ActorID: actor, SubjectID: actor, RequestKey: uuid.New(), CredentialVersion: 0, Password: "privacy-test-password", IP: ip, PolicyVersion: p.Version, Scope: Scope{Kind: Categories, Categories: []Category{"identity-core"}}}
		if _, err := clock.Submit(ctx, in); !errors.Is(err, ErrForbidden) {
			t.Fatalf("stale credential: %v", err)
		}
		in.CredentialVersion = 1
		in.Password = "wrong"
		// The stale credential above is the first failed confirmation.
		for i := 1; i < 10; i++ {
			if _, err := clock.Submit(ctx, in); !errors.Is(err, ErrForbidden) {
				t.Fatalf("failed confirmation %d: %v", i+1, err)
			}
		}
		if _, err := clock.Submit(ctx, in); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("eleventh attempt: %v", err)
		}
		in.Password = "privacy-test-password"
		if _, err := clock.Submit(ctx, in); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("correct password during limited window: %v", err)
		}
		now = now.Add(15*time.Minute - time.Microsecond)
		if _, err := clock.Submit(ctx, in); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("before boundary: %v", err)
		}
		now = now.Add(time.Microsecond)
		if _, err := clock.Submit(ctx, in); err != nil {
			t.Fatalf("at boundary: %v", err)
		}
	})
	t.Run("successful-submissions-do-not-spend-shared-ip-budget", func(t *testing.T) {
		const sharedIP = "198.51.100.110"
		for i := 0; i < 12; i++ {
			actor := user(nil)
			_, err := s.Submit(ctx, SubmitInput{ActorID: actor, SubjectID: actor, RequestKey: uuid.New(), CredentialVersion: 1, Password: "privacy-test-password", IP: sharedIP, PolicyVersion: p.Version, Scope: Scope{Kind: Categories, Categories: []Category{"identity-core"}}})
			if err != nil {
				t.Fatalf("valid shared-IP submission %d: %v", i+1, err)
			}
		}
		var buckets int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM privacy_request_auth_limits WHERE bucket LIKE 'ip:%'`).Scan(&buckets); err != nil {
			t.Fatal(err)
		}
		// Earlier failed-confirmation tests create IP buckets; successful
		// submissions must not add another one.
		if buckets != 2 {
			t.Fatalf("successful confirmations persisted a shared-IP bucket: %d", buckets)
		}
	})
	t.Run("revoked-reviewer-denied-at-use", func(t *testing.T) {
		actor, removed := user(nil), user(nil)
		if err := s.GrantReviewer(ctx, owner, removed, false); err != nil {
			t.Fatal(err)
		}
		r, err := submit(actor, actor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		r, err = s.Change(ctx, ReviewInput{ActorID: removed, Reference: r.PublicRef, Version: r.Version, Action: "claim"})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.GrantReviewer(ctx, owner, removed, true); err != nil {
			t.Fatal(err)
		}
		if _, err = s.View(ctx, removed, r.PublicRef, true); !errors.Is(err, ErrForbidden) {
			t.Fatalf("view after revocation: %v", err)
		}
		if _, err = s.List(ctx, removed, true, "", "", "due"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("list after revocation: %v", err)
		}
		if _, err = s.Change(ctx, ReviewInput{ActorID: removed, Reference: r.PublicRef, Version: r.Version, Action: "verify", IdentityVerified: true, IdentityMethod: "IN_PERSON"}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("mutation after revocation: %v", err)
		}
	})
	t.Run("review-queue-deadline-filters-and-order", func(t *testing.T) {
		now := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
		clock := s
		clock.Now = func() time.Time { return now }
		firstActor, secondActor := user(nil), user(nil)
		first, err := submit(firstActor, firstActor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		second, err := submit(secondActor, secondActor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE data_erasure_requests SET received_at=$2,due_at=$3,updated_at=$2 WHERE id=$1`, first.ID, now.AddDate(0, 0, -4), now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE data_erasure_requests SET received_at=$2,due_at=$3,updated_at=$2 WHERE id=$1`, second.ID, now.AddDate(0, 0, -3), now.AddDate(0, 0, 2)); err != nil {
			t.Fatal(err)
		}
		contains := func(rows []dbgen.DataErasureRequest, ref uuid.UUID) bool {
			for _, row := range rows {
				if row.PublicRef == ref {
					return true
				}
			}
			return false
		}
		overdue, err := clock.List(ctx, reviewerA, true, "RECEIVED", "overdue", "due")
		if err != nil || !contains(overdue, first.PublicRef) || contains(overdue, second.PublicRef) {
			t.Fatalf("overdue filter: first=%v second=%v err=%v", contains(overdue, first.PublicRef), contains(overdue, second.PublicRef), err)
		}
		soon, err := clock.List(ctx, reviewerA, true, "RECEIVED", "soon", "due")
		if err != nil || contains(soon, first.PublicRef) || !contains(soon, second.PublicRef) {
			t.Fatalf("soon filter: first=%v second=%v err=%v", contains(soon, first.PublicRef), contains(soon, second.PublicRef), err)
		}
		ordered, err := clock.List(ctx, reviewerA, true, "RECEIVED", "", "received")
		if err != nil {
			t.Fatal(err)
		}
		firstIndex, secondIndex := -1, -1
		for i, row := range ordered {
			switch row.PublicRef {
			case first.PublicRef:
				firstIndex = i
			case second.PublicRef:
				secondIndex = i
			}
		}
		if firstIndex < 0 || secondIndex < 0 || firstIndex >= secondIndex {
			t.Fatalf("received order: first=%d second=%d", firstIndex, secondIndex)
		}
	})
	t.Run("concurrent-decision-cancellation-atomic", func(t *testing.T) {
		actor := user(nil)
		r, err := submit(actor, actor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		r = claimVerify(r, false)
		inputs := []ReviewInput{{ActorID: actor, Reference: r.PublicRef, Version: r.Version, Action: "cancel"}, {ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Decisão explicada.", Decisions: decisions}}
		results := make(chan error, 2)
		for _, in := range inputs {
			go func(in ReviewInput) { _, err := s.Change(ctx, in); results <- err }(in)
		}
		successes, stale := 0, 0
		for range inputs {
			err := <-results
			if err == nil {
				successes++
			} else if errors.Is(err, ErrStaleVersion) {
				stale++
			} else {
				t.Fatal(err)
			}
		}
		if successes != 1 || stale != 1 {
			t.Fatal(successes, stale)
		}
		current, err := dbgen.New(pool).GetPrivacyRequestByRef(ctx, r.PublicRef)
		if err != nil {
			t.Fatal(err)
		}
		if current.Version != r.Version+1 {
			t.Fatal("multiple transitions", current.Version)
		}
		var count int
		if err = pool.QueryRow(ctx, "SELECT count(*) FROM data_erasure_request_events WHERE request_id=$1 AND version=$2", r.ID, current.Version).Scan(&count); err != nil || count != 1 {
			t.Fatal("transition history", count, err)
		}
		var active bool
		if err = pool.QueryRow(ctx, "SELECT is_active FROM users WHERE id=$1", actor).Scan(&active); err != nil || !active {
			t.Fatal("account access", active, err)
		}
	})
	t.Run("concurrent-claims-partial-cancel", func(t *testing.T) {
		actor := user(nil)
		r, e := submit(actor, actor, Categories)
		if e != nil {
			t.Fatal(e)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, reviewer := range []uuid.UUID{reviewerA, reviewerB} {
			wg.Add(1)
			go func(id uuid.UUID) {
				defer wg.Done()
				_, e := s.Change(ctx, ReviewInput{ActorID: id, Reference: r.PublicRef, Version: r.Version, Action: "claim"})
				errs <- e
			}(reviewer)
		}
		wg.Wait()
		close(errs)
		ok, stale := 0, 0
		for e := range errs {
			if e == nil {
				ok++
			} else if errors.Is(e, ErrStaleVersion) {
				stale++
			} else {
				t.Fatal(e)
			}
		}
		if ok != 1 || stale != 1 {
			t.Fatal(ok, stale)
		}
		r, e = dbgen.New(pool).GetPrivacyRequestByRef(ctx, r.PublicRef)
		if e != nil {
			t.Fatal(e)
		}
		if r.ClaimedBy == nil {
			t.Fatal("winning claim was not persisted")
		}
		winningReviewer := *r.ClaimedBy
		r, e = s.Change(ctx, ReviewInput{ActorID: winningReviewer, Reference: r.PublicRef, Version: r.Version, Action: "verify", IdentityVerified: true, IdentityMethod: "IN_PERSON"})
		if e != nil {
			t.Fatal(e)
		}
		r, e = s.Change(ctx, ReviewInput{ActorID: winningReviewer, Reference: r.PublicRef, Version: r.Version, Action: "partial", PolicyVersion: p.Version, Explanation: "Uma categoria aguarda conservação aprovada.", Decisions: map[string]CategoryDecision{"identity-core": {Outcome: "APPROVE"}, "profile-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}}})
		if e != nil || r.Status != "PARTIALLY_APPROVED" || r.ClosedAt.Valid {
			t.Fatal(r.Status, e)
		}
		planRow, e := dbgen.New(pool).GetPrivacyExecutionPlan(ctx, r.ID)
		if e != nil || len(planRow.PlanSha256) != 32 || planRow.ExecutorVersion != SupportedExecutorVersion || planRow.SchemaVersion != SupportedPlanSchemaVersion {
			t.Fatalf("missing executable plan: %+v %v", planRow, e)
		}
		var plan ExecutionPlan
		if e = json.Unmarshal(planRow.Plan, &plan); e != nil || len(plan.Entries) != 2 || plan.Entries[0].Disposition != "DELETE" || plan.Entries[1].Disposition != "RESTRICT" || plan.Entries[1].RetentionAnchor != caseClosureAnchor || plan.Entries[1].ExpireAfter == nil || plan.Entries[1].ExpireAt != "" {
			t.Fatalf("invalid executable plan: %+v %v", plan, e)
		}
		if _, e = pool.Exec(ctx, "UPDATE privacy_request_execution_plans SET plan=plan WHERE request_id=$1", r.ID); e == nil {
			t.Fatal("executable plan was mutable")
		}
		if _, e = pool.Exec(ctx, "DELETE FROM privacy_request_execution_plans WHERE request_id=$1", r.ID); e == nil {
			t.Fatal("executable plan was deleted before evidence expiry")
		}
		r, e = s.Change(ctx, ReviewInput{ActorID: actor, Reference: r.PublicRef, Version: r.Version, Action: "cancel"})
		if e != nil || r.Status != "CANCELLED" || !r.EvidenceExpiresAt.Valid {
			t.Fatal(r.Status, e)
		}
		var active bool
		pool.QueryRow(ctx, "SELECT is_active FROM users WHERE id=$1", actor).Scan(&active)
		if !active {
			t.Fatal("approval/cancel disabled account")
		}
		if _, e = pool.Exec(ctx, "DELETE FROM data_erasure_request_events WHERE request_id=$1", r.ID); e == nil {
			t.Fatal("premature audit purge")
		}
	})
	t.Run("decision-plan-is-atomic-with-case-and-event", func(t *testing.T) {
		actor := user(nil)
		r, err := submit(actor, actor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		r = claimVerify(r, false)
		_, err = pool.Exec(ctx, `INSERT INTO data_erasure_request_events(request_id,actor_role,actor_ref,action,reason_code,from_status,to_status,version,occurred_at) VALUES($1,'SYSTEM',$2,'IDENTITY_VERIFIED','VERIFICATION_RECORDED','UNDER_REVIEW','UNDER_REVIEW',$3,now())`, r.ID, uuid.New(), r.Version+1)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Decisão que deve reverter por conflito de auditoria.", Decisions: decisions})
		if err == nil {
			t.Fatal("decision unexpectedly committed")
		}
		stored, err := dbgen.New(pool).GetPrivacyRequestByRef(ctx, r.PublicRef)
		if err != nil || stored.Status != "UNDER_REVIEW" || stored.Version != r.Version {
			t.Fatalf("case escaped rollback: %+v %v", stored, err)
		}
		if _, err = dbgen.New(pool).GetPrivacyExecutionPlan(ctx, r.ID); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("plan escaped rollback: %v", err)
		}
	})
	t.Run("claimed-case-cannot-be-stolen-or-extended-after-decision", func(t *testing.T) {
		actor := user(nil)
		r, err := submit(actor, actor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		r, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "claim"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Change(ctx, ReviewInput{ActorID: reviewerB, Reference: r.PublicRef, Version: r.Version, Action: "claim"}); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("second reviewer reclaimed case: %v", err)
		}
		r, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "verify", IdentityVerified: true, IdentityMethod: "IN_PERSON"})
		if err != nil {
			t.Fatal(err)
		}
		r, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Categorias aprovadas para execução.", Decisions: decisions})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "extend", ExtensionMonths: 1, ExtensionReason: "COMPLEXITY"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("extended decided case: %v", err)
		}
	})
	t.Run("guardian-disclosure-and-current-authority", func(t *testing.T) {
		guardian := user(nil)
		minor := user(&guardian)
		r, e := submit(guardian, minor, Categories)
		if e != nil {
			t.Fatal(e)
		}
		v, e := s.View(ctx, guardian, r.PublicRef, false)
		if e != nil || !v.SafeReceipt || v.Record.DueAt.Valid || len(v.Policy.Categories) != 0 || v.Subject.ID != uuid.Nil {
			t.Fatal("receipt disclosed", e)
		}
		r = claimVerify(r, true)
		v, e = s.View(ctx, guardian, r.PublicRef, false)
		if e != nil || v.SafeReceipt {
			t.Fatal(e)
		}
		other := user(nil)
		_, e = pool.Exec(ctx, "UPDATE users SET guardian_id=$2,updated_at=now()+interval '1 second' WHERE id=$1", minor, other)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.View(ctx, guardian, r.PublicRef, false); !errors.Is(e, ErrForbidden) {
			t.Fatal("former guardian access", e)
		}
		if _, e = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Decisão explicada.", Decisions: decisions}); !errors.Is(e, ErrVerification) {
			t.Fatal("stale representation", e)
		}
	})
	t.Run("guardian-closure-and-final-admin", func(t *testing.T) {
		guardian := user(nil)
		minor := user(&guardian)
		child, e := submit(guardian, minor, AccountClosure)
		if e != nil {
			t.Fatal(e)
		}
		child = claimVerify(child, true)
		child, e = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: child.PublicRef, Version: child.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Pedido separado aprovado.", Decisions: closureDecisions})
		if e != nil {
			t.Fatal(e)
		}
		childExecution, e := s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: child.PublicRef, Version: child.Version, Confirmed: true})
		if e != nil {
			t.Fatalf("start child closure: %v", e)
		}
		r, e := submit(guardian, guardian, AccountClosure)
		if e != nil {
			t.Fatal(e)
		}
		r = claimVerify(r, false)
		command := ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Decisão explicada.", Decisions: closureDecisions}
		if _, e = s.Change(ctx, command); !errors.Is(e, ErrClosureSafeguards) {
			t.Fatal(e)
		}
		r, e = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "resolve-dependant", DependantID: minor, ResolutionCode: "SEPARATE_APPROVED_REQUEST", RelatedReference: child.PublicRef, ResolutionExplanation: "Pedido separado verificado para o dependente."})
		if e != nil {
			t.Fatal(e)
		}
		command.Version = r.Version
		r, e = s.Change(ctx, command)
		if e != nil || r.Status != "AWAITING_EXECUTION" {
			t.Fatal(e)
		}
		view, viewErr := s.View(ctx, reviewerB, r.PublicRef, true)
		if viewErr != nil || slices.Contains(view.ExecutionBlockers, "DEPENDANTS_UNRESOLVED") {
			t.Fatalf("resolved dependant view blockers=%v err=%v", view.ExecutionBlockers, viewErr)
		}
		if _, e = s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: r.PublicRef, Version: r.Version, Confirmed: true}); e != nil {
			t.Fatalf("guardian closure with durable dependant execution: %v", e)
		}
		if _, e = pool.Exec(ctx, `INSERT INTO privacy_erasure_category_jobs(execution_id,plan_entry_position,entry_sha256,category_key,purpose_code,next_attempt_at,created_at,updated_at) VALUES($1,50,digest('orphan-job','sha256'),'orphan-job','orphan-purpose',clock_timestamp(),clock_timestamp(),clock_timestamp())`, childExecution.ID); e != nil {
			t.Fatal(e)
		}
		graphTx, graphErr := pool.Begin(ctx)
		if graphErr != nil {
			t.Fatal(graphErr)
		}
		planRow, graphErr := dbgen.New(graphTx).GetPrivacyExecutionPlan(ctx, child.ID)
		if graphErr != nil {
			_ = graphTx.Rollback(ctx)
			t.Fatal(graphErr)
		}
		childPlan, graphErr := ReadExecutionPlan(planRow)
		if graphErr != nil {
			_ = graphTx.Rollback(ctx)
			t.Fatal(graphErr)
		}
		graphValid, graphErr := exactPersistedExecutionGraph(ctx, graphTx, childExecution.ID, childPlan)
		_ = graphTx.Rollback(ctx)
		if graphErr != nil || graphValid {
			t.Fatalf("extra zero-checkpoint job accepted: valid=%v err=%v", graphValid, graphErr)
		}
		r, e = submit(owner, owner, AccountClosure)
		if e != nil {
			t.Fatal(e)
		}
		r = claimVerify(r, false)
		command.Reference = r.PublicRef
		command.Version = r.Version
		if _, e = s.Change(ctx, command); !errors.Is(e, ErrClosureSafeguards) {
			t.Fatal("last admin", e)
		}
	})
	t.Run("administrator-revocation-and-closure-start-serialize", func(t *testing.T) {
		closingAdmin, remainingAdmin := user(nil), user(nil)
		for _, id := range []uuid.UUID{closingAdmin, remainingAdmin} {
			if _, err := pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, id); err != nil {
				t.Fatal(err)
			}
		}
		request, err := submit(closingAdmin, closingAdmin, AccountClosure)
		if err != nil {
			t.Fatal(err)
		}
		request = claimVerify(request, false)
		request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Decisão de encerramento aprovada.", Decisions: closureDecisions})
		if err != nil {
			t.Fatal(err)
		}
		// Leave exactly the two fixture administrators in the protected set. Both
		// closure start and role revocation must then serialize on the same DB lock.
		if _, err = pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=$1 AND role_id=(SELECT id FROM platform_roles WHERE code='ADMIN')`, owner); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		startResult := make(chan error, 1)
		revokeResult := make(chan error, 1)
		go func() {
			<-start
			_, startErr := s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: request.PublicRef, Version: request.Version, Confirmed: true})
			startResult <- startErr
		}()
		go func() {
			<-start
			_, revokeErr := pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=$1 AND role_id=(SELECT id FROM platform_roles WHERE code='ADMIN')`, remainingAdmin)
			revokeResult <- revokeErr
		}()
		close(start)
		startErr, revokeErr := <-startResult, <-revokeResult
		startSucceeded := startErr == nil
		revokeSucceeded := revokeErr == nil
		if startSucceeded == revokeSucceeded {
			t.Fatalf("exactly one concurrent operation must succeed: start=%v revoke=%v", startErr, revokeErr)
		}
		if startErr != nil && !errors.Is(startErr, ErrClosureSafeguards) {
			t.Fatalf("closure start failed outside last-admin safeguard: %v", startErr)
		}
		if revokeErr != nil {
			var postgresErr *pgconn.PgError
			if !errors.As(revokeErr, &postgresErr) || postgresErr.Code != "P0001" || postgresErr.Message != "last_active_administrator" {
				t.Fatalf("administrator revocation failed outside DB invariant: %v", revokeErr)
			}
		}
		var activeAdmins int
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM user_platform_roles assignment JOIN platform_roles role ON role.id=assignment.role_id JOIN users account ON account.id=assignment.user_id WHERE role.code='ADMIN' AND account.is_active`).Scan(&activeAdmins); err != nil {
			t.Fatal(err)
		}
		if activeAdmins != 1 {
			t.Fatalf("active administrators after race = %d, want 1", activeAdmins)
		}
		// Restore exactly the original shared administrator set for subsequent
		// subtests. The isolated schema drops the two race-only accounts at test end.
		if _, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN' ON CONFLICT DO NOTHING`, owner); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=ANY($1) AND role_id=(SELECT id FROM platform_roles WHERE code='ADMIN')`, []uuid.UUID{closingAdmin, remainingAdmin}); err != nil {
			t.Fatal(err)
		}
	})
	for _, mutation := range []string{"grant", "deactivate"} {
		t.Run("administrator-"+mutation+"-and-closure-start-serialize", func(t *testing.T) {
			closingAdmin, otherAdmin := user(nil), user(nil)
			for _, id := range []uuid.UUID{closingAdmin, otherAdmin} {
				if _, err := pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, id); err != nil {
					t.Fatal(err)
				}
			}
			request, err := submit(closingAdmin, closingAdmin, AccountClosure)
			if err != nil {
				t.Fatal(err)
			}
			request = claimVerify(request, false)
			request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Decisão concorrente aprovada.", Decisions: closureDecisions})
			if err != nil {
				t.Fatal(err)
			}
			if mutation == "grant" {
				if _, err = pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=ANY($1) AND role_id=(SELECT id FROM platform_roles WHERE code='ADMIN')`, []uuid.UUID{owner, otherAdmin}); err != nil {
					t.Fatal(err)
				}
			} else if _, err = pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=$1 AND role_id=(SELECT id FROM platform_roles WHERE code='ADMIN')`, owner); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			startResult, mutationResult := make(chan error, 1), make(chan error, 1)
			go func() {
				<-start
				_, startErr := s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: request.PublicRef, Version: request.Version, Confirmed: true})
				startResult <- startErr
			}()
			go func() {
				<-start
				var mutationErr error
				if mutation == "grant" {
					_, mutationErr = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, otherAdmin)
				} else {
					_, mutationErr = pool.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, otherAdmin)
				}
				mutationResult <- mutationErr
			}()
			close(start)
			startErr, mutationErr := <-startResult, <-mutationResult
			if startErr != nil && !errors.Is(startErr, ErrClosureSafeguards) {
				t.Fatalf("start failed outside safeguard: %v", startErr)
			}
			if mutation == "grant" && mutationErr != nil {
				t.Fatalf("concurrent administrator grant failed: %v", mutationErr)
			}
			if mutation == "deactivate" && (startErr == nil) == (mutationErr == nil) {
				t.Fatalf("exactly one deactivation/start operation must succeed: start=%v deactivate=%v", startErr, mutationErr)
			}
			var activeAdmins int
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM user_platform_roles assignment JOIN platform_roles role ON role.id=assignment.role_id JOIN users account ON account.id=assignment.user_id WHERE role.code='ADMIN' AND account.is_active`).Scan(&activeAdmins); err != nil || activeAdmins < 1 {
				t.Fatalf("active administrator invariant count=%d err=%v", activeAdmins, err)
			}
			if _, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN' ON CONFLICT DO NOTHING`, owner); err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=ANY($1) AND role_id=(SELECT id FROM platform_roles WHERE code='ADMIN')`, []uuid.UUID{closingAdmin, otherAdmin}); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("changed-dependant-relationship-blocks-closure-start", func(t *testing.T) {
		guardian, replacement := user(nil), user(nil)
		minor := user(&guardian)
		newMinor := user(&replacement)
		child, err := submit(guardian, minor, AccountClosure)
		if err != nil {
			t.Fatal(err)
		}
		child = claimVerify(child, true)
		child, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: child.PublicRef, Version: child.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Pedido dependente aprovado.", Decisions: closureDecisions})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: child.PublicRef, Version: child.Version, Confirmed: true}); err != nil {
			t.Fatal(err)
		}
		request, err := submit(guardian, guardian, AccountClosure)
		if err != nil {
			t.Fatal(err)
		}
		request = claimVerify(request, false)
		request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "resolve-dependant", DependantID: minor, ResolutionCode: "SEPARATE_APPROVED_REQUEST", RelatedReference: child.PublicRef, ResolutionExplanation: "Pedido separado verificado."})
		if err != nil {
			t.Fatal(err)
		}
		request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Encerramento do tutor aprovado.", Decisions: closureDecisions})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE users SET guardian_id=$2,updated_at=clock_timestamp() WHERE id=$1`, newMinor, guardian); err != nil {
			t.Fatal(err)
		}
		if _, err = s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: request.PublicRef, Version: request.Version, Confirmed: true}); !errors.Is(err, ErrClosureSafeguards) {
			t.Fatalf("changed dependant relationship start error=%v", err)
		}
	})
	t.Run("deadlines-refusal-and-retention", func(t *testing.T) {
		openActor := user(nil)
		openRequest, e := submit(openActor, openActor, Categories)
		if e != nil {
			t.Fatal(e)
		}
		openRequest = claimVerify(openRequest, false)
		openRequest, e = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: openRequest.PublicRef, Version: openRequest.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Decisão aprovada e a aguardar execução.", Decisions: decisions})
		if e != nil || openRequest.Status != "AWAITING_EXECUTION" {
			t.Fatal(openRequest.Status, e)
		}
		heldActor := user(nil)
		heldRequest, e := submit(heldActor, heldActor, Categories)
		if e != nil {
			t.Fatal(e)
		}
		heldRequest = claimVerify(heldRequest, false)
		heldRequest, e = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: heldRequest.PublicRef, Version: heldRequest.Version, Action: "refuse", PolicyVersion: p.Version, Explanation: "Decisão recusada com conservação temporária por reclamação.", Decisions: map[string]CategoryDecision{"identity-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}, "profile-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}}})
		if e != nil {
			t.Fatal(e)
		}
		if e = s.AddRetentionException(ctx, owner, heldRequest.PublicRef, reviewerB, "identity-core", "LEGAL_HOLD", "legal-file-001"); e != nil {
			t.Fatal(e)
		}
		if e = s.AddRetentionException(ctx, owner, heldRequest.PublicRef, reviewerB, "identity-core", "OTHER", "legal-file-002"); !errors.Is(e, ErrInvalid) {
			t.Fatalf("invalid retention exception reason: %v", e)
		}
		if e = s.AddRetentionException(ctx, owner, heldRequest.PublicRef, owner, "profile-core", "LEGAL_HOLD", "legal-file-003"); !errors.Is(e, ErrForbidden) {
			t.Fatalf("non-reviewer retention owner accepted: %v", e)
		}
		var heldCategory, heldPurpose, heldEvidence string
		var heldFields []string
		var heldReviewAt, heldExpiresAt time.Time
		if e = pool.QueryRow(ctx, `SELECT category_key,purpose_code,retained_field_codes,evidence_ref,review_at,expires_at FROM privacy_request_retention_exceptions WHERE request_id=$1`, heldRequest.ID).Scan(&heldCategory, &heldPurpose, &heldFields, &heldEvidence, &heldReviewAt, &heldExpiresAt); e != nil {
			t.Fatal(e)
		}
		if heldCategory != "identity-core" || heldPurpose != "ACCOUNT_IDENTITY" || !slices.Equal(heldFields, []string{"users.id"}) || heldEvidence != "legal-file-001" || !heldReviewAt.Before(heldExpiresAt) {
			t.Fatalf("retention exception is not plan-derived: category=%q purpose=%q fields=%v evidence=%q review=%v expiry=%v", heldCategory, heldPurpose, heldFields, heldEvidence, heldReviewAt, heldExpiresAt)
		}
		if _, e = pool.Exec(ctx, "UPDATE data_erasure_requests SET working_expires_at=now()-interval '1 second',evidence_expires_at=now()-interval '1 second',closed_at=now()-interval '3 years' WHERE id=$1", heldRequest.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, "DELETE FROM privacy_request_retention_exceptions WHERE request_id=$1", heldRequest.ID); e == nil {
			t.Fatal("active retention exception was deletable after case evidence expiry")
		}
		heldResult, e := s.Expire(ctx, owner)
		if e != nil || heldResult.WorkingRecords != 1 || heldResult.EvidenceRecords != 0 {
			t.Fatalf("retention exception did not permit working scrub and block evidence expiry: result=%+v err=%v", heldResult, e)
		}
		var heldWorking bool
		var heldExplanation string
		var heldSubject *uuid.UUID
		if e = pool.QueryRow(ctx, "SELECT working_erased_at IS NOT NULL,decision_explanation,subject_user_id FROM data_erasure_requests WHERE id=$1", heldRequest.ID).Scan(&heldWorking, &heldExplanation, &heldSubject); e != nil {
			t.Fatal(e)
		}
		if !heldWorking || heldExplanation != "" || heldSubject != nil {
			t.Fatalf("active category hold retained unrelated case working data: erased=%v explanation=%q subject=%v", heldWorking, heldExplanation, heldSubject)
		}
		cancelActor := user(nil)
		cancelled, cancelErr := submit(cancelActor, cancelActor, Categories)
		if cancelErr != nil {
			t.Fatal(cancelErr)
		}
		cancelled, cancelErr = s.Change(ctx, ReviewInput{ActorID: cancelActor, Reference: cancelled.PublicRef, Version: cancelled.Version, Action: "cancel"})
		if cancelErr != nil {
			t.Fatal(cancelErr)
		}
		if e = s.AddRetentionException(ctx, owner, cancelled.PublicRef, reviewerB, "identity-core", "LEGAL_HOLD", "legal-file-cancelled"); !errors.Is(e, ErrInvalidTransition) {
			t.Fatalf("cancelled case accepted category hold without a decision plan: %v", e)
		}
		var reviewerAuditBefore, activationAuditBefore int
		if e = pool.QueryRow(ctx, "SELECT count(*) FROM privacy_reviewer_grant_events").Scan(&reviewerAuditBefore); e != nil {
			t.Fatal(e)
		}
		if e = pool.QueryRow(ctx, "SELECT count(*) FROM privacy_request_activation_events").Scan(&activationAuditBefore); e != nil {
			t.Fatal(e)
		}
		actor := user(nil)
		r, e := submit(actor, actor, Categories)
		if e != nil {
			t.Fatal(e)
		}
		r = claimVerify(r, false)
		r, e = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "extend", ExtensionMonths: 2, ExtensionReason: "COMPLEXITY"})
		if e != nil || !r.ExtendedDueAt.Valid {
			t.Fatal(e)
		}
		if _, e = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "extend", ExtensionMonths: 1, ExtensionReason: "COMPLEXITY"}); e == nil {
			t.Fatal("double extension")
		}
		r, e = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "refuse", PolicyVersion: p.Version, Explanation: "Exceção aprovada para ambas as categorias.", Decisions: map[string]CategoryDecision{"identity-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}, "profile-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}}})
		if e != nil {
			t.Fatal(e)
		}
		_, e = pool.Exec(ctx, "UPDATE data_erasure_requests SET working_expires_at=now()-interval '1 second' WHERE id=$1", r.ID)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Expire(ctx, actor); !errors.Is(e, ErrForbidden) {
			t.Fatal("ordinary cleanup", e)
		}
		result, e := s.Expire(ctx, owner)
		if e != nil || result.WorkingRecords != 1 {
			t.Fatal(result, e)
		}
		var working bool
		var explanation string
		var subject *uuid.UUID
		pool.QueryRow(ctx, "SELECT working_erased_at IS NOT NULL,decision_explanation,subject_user_id FROM data_erasure_requests WHERE id=$1", r.ID).Scan(&working, &explanation, &subject)
		if !working || explanation != "" || subject != nil {
			t.Fatal("working data retained")
		}
		if _, e = dbgen.New(pool).GetPrivacyExecutionPlan(ctx, r.ID); e != nil {
			t.Fatalf("non-identifying plan did not survive working scrub: %v", e)
		}
		_, e = pool.Exec(ctx, "UPDATE data_erasure_requests SET closed_at=now()-interval '3 years',evidence_expires_at=now()-interval '1 day' WHERE id=$1", r.ID)
		if e != nil {
			t.Fatal(e)
		}
		result, e = s.Expire(ctx, owner)
		if e != nil || result.EvidenceRecords != 1 {
			t.Fatal(result, e)
		}
		if _, e = dbgen.New(pool).GetPrivacyExecutionPlan(ctx, r.ID); !errors.Is(e, pgx.ErrNoRows) {
			t.Fatalf("plan survived evidence expiry: %v", e)
		}
		preserved, e := dbgen.New(pool).GetPrivacyRequestByRef(ctx, openRequest.PublicRef)
		if e != nil || preserved.Status != "AWAITING_EXECUTION" || preserved.WorkingErasedAt.Valid {
			t.Fatalf("open approval changed by expiry: status=%q erased=%v err=%v", preserved.Status, preserved.WorkingErasedAt.Valid, e)
		}
		var reviewerAuditAfter, activationAuditAfter int
		if e = pool.QueryRow(ctx, "SELECT count(*) FROM privacy_reviewer_grant_events").Scan(&reviewerAuditAfter); e != nil {
			t.Fatal(e)
		}
		if e = pool.QueryRow(ctx, "SELECT count(*) FROM privacy_request_activation_events").Scan(&activationAuditAfter); e != nil {
			t.Fatal(e)
		}
		if reviewerAuditAfter != reviewerAuditBefore || activationAuditAfter != activationAuditBefore {
			t.Fatalf("operator audit changed by expiry: reviewer %d/%d activation %d/%d", reviewerAuditBefore, reviewerAuditAfter, activationAuditBefore, activationAuditAfter)
		}
	})
	t.Run("execution-handoff-is-atomic-idempotent-and-category-scoped", func(t *testing.T) {
		actor := user(nil)
		if err := s.GrantReviewer(ctx, owner, actor, false); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($1,decode('00','hex'),clock_timestamp()+interval '1 hour',$2,true)`, "category-"+actor.String(), actor); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO password_reset_tokens(user_id,email,token_digest,expires_at) VALUES($1,$2::text,digest($2::text,'sha256'),clock_timestamp()+interval '1 hour')`, actor, actor.String()+"@example.test"); err != nil {
			t.Fatal(err)
		}
		var beforeCredential int64
		if err := pool.QueryRow(ctx, `SELECT credential_version FROM users WHERE id=$1`, actor).Scan(&beforeCredential); err != nil {
			t.Fatal(err)
		}
		request, err := submit(actor, actor, Categories)
		if err != nil {
			t.Fatal(err)
		}
		request = claimVerify(request, false)
		request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Apagamento aprovado.", Decisions: decisions})
		if err != nil {
			t.Fatal(err)
		}
		execution, err := s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: request.PublicRef, Version: request.Version, Confirmed: true})
		if err != nil {
			t.Fatal(err)
		}
		executionView, err := s.View(ctx, reviewerB, request.PublicRef, true)
		if err != nil || executionView.Execution == nil || executionView.Execution.ID != execution.ID {
			t.Fatalf("post-start execution view=%+v err=%v", executionView, err)
		}
		stored, err := dbgen.New(pool).GetPrivacyRequestByRef(ctx, request.PublicRef)
		if err != nil || stored.Status != string(Processing) {
			t.Fatalf("request status=%q err=%v", stored.Status, err)
		}
		var active bool
		var credential int64
		var sessions, tokens, grants int
		if err = pool.QueryRow(ctx, "SELECT is_active,credential_version FROM users WHERE id=$1", actor).Scan(&active, &credential); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM sessions WHERE user_id=$1),
			(SELECT count(*) FROM password_reset_tokens WHERE user_id=$1 AND consumed_at IS NULL),
			(SELECT count(*) FROM privacy_reviewer_grants WHERE user_id=$1 AND revoked_at IS NULL)`, actor).Scan(&sessions, &tokens, &grants); err != nil {
			t.Fatal(err)
		}
		if !active || credential != beforeCredential || sessions != 1 || tokens != 1 || grants != 1 {
			t.Fatalf("category execution changed access: active=%v credential=%d/%d sessions=%d tokens=%d grants=%d", active, beforeCredential, credential, sessions, tokens, grants)
		}
		counts, err := dbgen.New(pool).GetPrivacyErasureWorkSetCounts(ctx, execution.ID)
		if err != nil || counts.JobCount != 2 || counts.CheckpointCount < 2 {
			t.Fatalf("work graph=%+v err=%v", counts, err)
		}
		replayed, err := s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: request.PublicRef, Version: request.Version, Confirmed: true})
		if err != nil || replayed.ID != execution.ID {
			t.Fatalf("idempotent replay=%+v err=%v", replayed, err)
		}
		capabilities := s.ExecutionCapabilities
		s.ExecutionCapabilities = nil
		if _, err = pool.Exec(ctx, `UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false WHERE singleton`); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE privacy_executor_grants SET revoked_by=$2,revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL`, reviewerB, owner); err != nil {
			t.Fatal(err)
		}
		replayed, err = s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: request.PublicRef, Version: request.Version, Confirmed: true})
		if err != nil || replayed.ID != execution.ID {
			t.Fatalf("committed replay after mutable readiness changed=%+v err=%v", replayed, err)
		}
		s.ExecutionCapabilities = capabilities
		if _, err = pool.Exec(ctx, `UPDATE privacy_request_activation SET enabled=true,fulfilment_ready=true WHERE singleton`); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE privacy_executor_grants SET revoked_by=NULL,revoked_at=NULL WHERE user_id=$1`, reviewerB); err != nil {
			t.Fatal(err)
		}
		var executions, notices int
		if err = pool.QueryRow(ctx, "SELECT count(*) FROM privacy_erasure_executions WHERE request_id=$1", request.ID).Scan(&executions); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, "SELECT count(*) FROM email_outbox WHERE privacy_request_id=$1 AND message_type='PRIVACY_PROCESSING_STARTED'", request.ID).Scan(&notices); err != nil {
			t.Fatal(err)
		}
		if executions != 1 || notices != 1 {
			t.Fatalf("executions=%d notices=%d", executions, notices)
		}
	})
	t.Run("execution-start-fails-closed-at-mutable-gates", func(t *testing.T) {
		approved := func() dbgen.DataErasureRequest {
			subject := user(nil)
			request, err := submit(subject, subject, Categories)
			if err != nil {
				t.Fatal(err)
			}
			request = claimVerify(request, false)
			request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Execução aprovada para validar o fecho seguro.", Decisions: decisions})
			if err != nil {
				t.Fatal(err)
			}
			return request
		}
		valid := func(request dbgen.DataErasureRequest, actor uuid.UUID) StartInput {
			return StartInput{ActorID: actor, Reference: request.PublicRef, Version: request.Version, Confirmed: true}
		}
		if _, err := s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: uuid.New(), Version: 2, Confirmed: true}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("missing request error=%v", err)
		}
		request := approved()
		disabled := s
		disabled.Enabled = false
		if _, err := disabled.StartExecution(ctx, valid(request, reviewerB)); !errors.Is(err, ErrExecutorUnavailable) {
			t.Fatalf("disabled service error=%v", err)
		}
		request = approved()
		withoutCapabilities := s
		withoutCapabilities.ExecutionCapabilities = nil
		if _, err := withoutCapabilities.StartExecution(ctx, valid(request, reviewerB)); !errors.Is(err, ErrExecutorUnavailable) {
			t.Fatalf("missing capabilities error=%v", err)
		}
		request = approved()
		if _, err := s.StartExecution(ctx, valid(request, *request.SubjectUserID)); !errors.Is(err, ErrForbidden) {
			t.Fatalf("subject executor separation error=%v", err)
		}
		request = approved()
		if _, err := s.StartExecution(ctx, valid(request, user(nil))); !errors.Is(err, ErrForbidden) {
			t.Fatalf("missing executor grant error=%v", err)
		}
		request = approved()
		if _, err := pool.Exec(ctx, `UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false WHERE singleton`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.StartExecution(ctx, valid(request, reviewerB)); !errors.Is(err, ErrExecutorUnavailable) {
			t.Fatalf("disabled activation error=%v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE privacy_request_activation SET enabled=true,fulfilment_ready=true WHERE singleton`); err != nil {
			t.Fatal(err)
		}
		request = approved()
		var grantedAt time.Time
		if err := pool.QueryRow(ctx, `SELECT granted_at FROM privacy_reviewer_grants WHERE user_id=$1 AND revoked_at IS NULL`, reviewerA).Scan(&grantedAt); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE privacy_reviewer_grants SET granted_at=(SELECT decided_at+interval '1 second' FROM data_erasure_requests WHERE id=$1) WHERE user_id=$2 AND revoked_at IS NULL`, request.ID, reviewerA); err != nil {
			t.Fatal(err)
		}
		if _, err := s.StartExecution(ctx, valid(request, reviewerB)); !errors.Is(err, ErrForbidden) {
			t.Fatalf("historical reviewer authority error=%v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE privacy_reviewer_grants SET granted_at=$2 WHERE user_id=$1 AND revoked_at IS NULL`, reviewerA, grantedAt); err != nil {
			t.Fatal(err)
		}
		request = approved()
		if _, err := s.StartExecution(ctx, valid(request, reviewerB)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.StartExecution(ctx, valid(request, user(nil))); !errors.Is(err, ErrExecutionConflict) {
			t.Fatalf("divergent replay error=%v", err)
		}
		request = approved()
		if _, err := pool.Exec(ctx, `UPDATE data_erasure_requests SET policy_snapshot='{}'::jsonb WHERE id=$1`, request.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.StartExecution(ctx, valid(request, reviewerB)); !errors.Is(err, ErrPolicyUnresolved) {
			t.Fatalf("malformed policy snapshot error=%v", err)
		}
		request = approved()
		if _, err := pool.Exec(ctx, `UPDATE data_erasure_requests SET status='RECEIVED' WHERE id=$1`, request.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.StartExecution(ctx, valid(request, reviewerB)); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("invalid execution status error=%v", err)
		}
	})
	t.Run("execution-start-rolls-back-persistence-failures", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `CREATE OR REPLACE FUNCTION privacy_test_fail_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected privacy write failure'; END $$`); err != nil {
			t.Fatal(err)
		}
		defer pool.Exec(ctx, `DROP FUNCTION IF EXISTS privacy_test_fail_write() CASCADE`)
		for _, table := range []string{"privacy_erasure_executions", "privacy_erasure_category_jobs", "privacy_erasure_job_checkpoints", "data_erasure_requests", "data_erasure_request_events", "email_outbox"} {
			t.Run(table, func(t *testing.T) {
				subject := user(nil)
				request, err := submit(subject, subject, Categories)
				if err != nil {
					t.Fatal(err)
				}
				request = claimVerify(request, false)
				request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Execução aprovada para validar rollback integral.", Decisions: decisions})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = pool.Exec(ctx, `CREATE TRIGGER privacy_test_injected_failure BEFORE INSERT OR UPDATE ON `+table+` FOR EACH ROW EXECUTE FUNCTION privacy_test_fail_write()`); err != nil {
					t.Fatal(err)
				}
				_, startErr := s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: request.PublicRef, Version: request.Version, Confirmed: true})
				if _, err = pool.Exec(ctx, `DROP TRIGGER privacy_test_injected_failure ON `+table); err != nil {
					t.Fatal(err)
				}
				if startErr == nil {
					t.Fatal("injected persistence failure was ignored")
				}
				var executions int
				if err = pool.QueryRow(ctx, `SELECT count(*) FROM privacy_erasure_executions WHERE request_id=$1`, request.ID).Scan(&executions); err != nil || executions != 0 {
					t.Fatalf("rollback executions=%d err=%v", executions, err)
				}
			})
		}
		for _, table := range []string{"users", "sessions", "password_reset_tokens", "email_verification_tokens", "user_platform_roles", "staff_grants", "privacy_reviewer_grants", "privacy_executor_grants", "privacy_erasure_access_revocations"} {
			t.Run("closure_"+table, func(t *testing.T) {
				subject := user(nil)
				if _, err := pool.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($1,decode('00','hex'),clock_timestamp()+interval '1 hour',$2,true)`, "fault-"+subject.String(), subject); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `INSERT INTO password_reset_tokens(user_id,email,token_digest,expires_at) VALUES($1,$2::text,digest($2::text,'sha256'),clock_timestamp()+interval '1 hour')`, subject, subject.String()+"@example.test"); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `INSERT INTO email_verification_tokens(user_id,email,expires_at) VALUES($1,$2::text,clock_timestamp()+interval '1 hour')`, subject, subject.String()+"@example.test"); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, subject); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `INSERT INTO privacy_reviewer_grants(user_id,granted_by,granted_at) VALUES($1,$2,clock_timestamp())`, subject, owner); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `INSERT INTO privacy_executor_grants(user_id,granted_by,granted_at) VALUES($1,$2,clock_timestamp())`, subject, owner); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `INSERT INTO staff_grants(user_id,capability,granted_by_id) VALUES($1,'MODERATOR',$2)`, subject, owner); err != nil {
					t.Fatal(err)
				}
				request, err := submit(subject, subject, AccountClosure)
				if err != nil {
					t.Fatal(err)
				}
				request = claimVerify(request, false)
				request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Encerramento aprovado para validar rollback integral.", Decisions: closureDecisions})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = pool.Exec(ctx, `CREATE TRIGGER privacy_test_injected_failure BEFORE INSERT OR UPDATE OR DELETE ON `+table+` FOR EACH ROW EXECUTE FUNCTION privacy_test_fail_write()`); err != nil {
					t.Fatal(err)
				}
				_, startErr := s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: request.PublicRef, Version: request.Version, Confirmed: true})
				if _, err = pool.Exec(ctx, `DROP TRIGGER privacy_test_injected_failure ON `+table); err != nil {
					t.Fatal(err)
				}
				if startErr == nil {
					t.Fatal("injected closure failure was ignored")
				}
				var executions int
				if err = pool.QueryRow(ctx, `SELECT count(*) FROM privacy_erasure_executions WHERE request_id=$1`, request.ID).Scan(&executions); err != nil || executions != 0 {
					t.Fatalf("closure rollback executions=%d err=%v", executions, err)
				}
				if _, err = pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=$1`, subject); err != nil {
					t.Fatal(err)
				}
			})
		}
	})
	t.Run("concurrent-account-closure-starts-cut-off-once", func(t *testing.T) {
		subject := user(nil)
		request, err := submit(subject, subject, AccountClosure)
		if err != nil {
			t.Fatal(err)
		}
		request = claimVerify(request, false)
		request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Encerramento concorrente aprovado.", Decisions: closureDecisions})
		if err != nil {
			t.Fatal(err)
		}
		var beforeCredential int64
		if err = pool.QueryRow(ctx, `SELECT credential_version FROM users WHERE id=$1`, subject).Scan(&beforeCredential); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($1,decode('00','hex'),clock_timestamp()+interval '1 hour',$2,true)`, "concurrent-"+subject.String(), subject); err != nil {
			t.Fatal(err)
		}
		input := StartInput{ActorID: reviewerB, Reference: request.PublicRef, Version: request.Version, Confirmed: true}
		type result struct {
			execution dbgen.PrivacyErasureExecution
			err       error
		}
		results := make(chan result, 2)
		start := make(chan struct{})
		for range 2 {
			go func() {
				<-start
				execution, startErr := s.StartExecution(ctx, input)
				results <- result{execution: execution, err: startErr}
			}()
		}
		close(start)
		first, second := <-results, <-results
		if first.err != nil || second.err != nil || first.execution.ID == uuid.Nil || first.execution.ID != second.execution.ID {
			t.Fatalf("concurrent starts first=%+v second=%+v", first, second)
		}
		var credential int64
		var active bool
		var executions, sessions int
		if err = pool.QueryRow(ctx, `SELECT credential_version,is_active FROM users WHERE id=$1`, subject).Scan(&credential, &active); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM privacy_erasure_executions WHERE request_id=$1),(SELECT count(*) FROM sessions WHERE user_id=$2)`, request.ID, subject).Scan(&executions, &sessions); err != nil {
			t.Fatal(err)
		}
		if credential != beforeCredential+1 || active || executions != 1 || sessions != 0 {
			t.Fatalf("concurrent cutoff credential=%d/%d active=%v executions=%d sessions=%d", beforeCredential, credential, active, executions, sessions)
		}
	})
	t.Run("late-handoff-failure-rolls-back-graph-and-account-cutoff", func(t *testing.T) {
		subject := user(nil)
		request, err := submit(subject, subject, AccountClosure)
		if err != nil {
			t.Fatal(err)
		}
		request = claimVerify(request, false)
		request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Encerramento para teste de rollback.", Decisions: closureDecisions})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($1,decode('00','hex'),clock_timestamp()+interval '1 hour',$2,true)`, "rollback-"+subject.String(), subject); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO password_reset_tokens(user_id,email,token_digest,expires_at) VALUES($1,$2::text,digest($2::text,'sha256'),clock_timestamp()+interval '1 hour')`, subject, subject.String()+"@example.test"); err != nil {
			t.Fatal(err)
		}
		var verificationToken uuid.UUID
		if err = pool.QueryRow(ctx, `INSERT INTO email_verification_tokens(user_id,email,expires_at) VALUES($1,$2::text,clock_timestamp()+interval '1 hour') RETURNING id`, subject, subject.String()+"@example.test").Scan(&verificationToken); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO email_outbox(verification_token_id) VALUES($1)`, verificationToken); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, subject); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO privacy_reviewer_grants(user_id,granted_by,granted_at) VALUES($1,$2,clock_timestamp())`, subject, owner); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO privacy_executor_grants(user_id,granted_by,granted_at) VALUES($1,$2,clock_timestamp())`, subject, owner); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO staff_grants(user_id,capability,granted_by_id) VALUES($1,'MODERATOR',$2)`, subject, owner); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `CREATE FUNCTION reject_processing_notice() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.message_type='PRIVACY_PROCESSING_STARTED' THEN RAISE EXCEPTION 'injected_processing_notice_failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_processing_notice BEFORE INSERT ON email_outbox FOR EACH ROW EXECUTE FUNCTION reject_processing_notice()`); err != nil {
			t.Fatal(err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, `DROP TRIGGER IF EXISTS reject_processing_notice ON email_outbox; DROP FUNCTION IF EXISTS reject_processing_notice()`)
		}()
		if _, err = s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: request.PublicRef, Version: request.Version, Confirmed: true}); err == nil {
			t.Fatal("fault-injected handoff unexpectedly committed")
		}
		var status string
		var active bool
		var credential int64
		var executions, jobs, sessions, resetTokens, verificationTokens, verificationNotices, roles, reviewerGrants, executorGrants, staffGrants, revocations, notices int
		if err = pool.QueryRow(ctx, `SELECT status FROM data_erasure_requests WHERE id=$1`, request.ID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, `SELECT is_active,credential_version FROM users WHERE id=$1`, subject).Scan(&active, &credential); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM privacy_erasure_executions WHERE request_id=$1),
			(SELECT count(*) FROM privacy_erasure_category_jobs job JOIN privacy_erasure_executions execution ON execution.id=job.execution_id WHERE execution.request_id=$1),
			(SELECT count(*) FROM sessions WHERE user_id=$2),
			(SELECT count(*) FROM password_reset_tokens WHERE user_id=$2 AND consumed_at IS NULL),
			(SELECT count(*) FROM email_verification_tokens WHERE user_id=$2 AND consumed_at IS NULL),
			(SELECT count(*) FROM email_outbox outbox JOIN email_verification_tokens token ON token.id=outbox.verification_token_id WHERE token.user_id=$2 AND outbox.status='PENDING'),
			(SELECT count(*) FROM user_platform_roles WHERE user_id=$2),
			(SELECT count(*) FROM privacy_reviewer_grants WHERE user_id=$2 AND revoked_at IS NULL),
			(SELECT count(*) FROM privacy_executor_grants WHERE user_id=$2 AND revoked_at IS NULL),
			(SELECT count(*) FROM staff_grants WHERE user_id=$2 AND revoked_at IS NULL),
			(SELECT count(*) FROM privacy_erasure_access_revocations revocation JOIN privacy_erasure_executions execution ON execution.id=revocation.execution_id WHERE execution.request_id=$1),
			(SELECT count(*) FROM email_outbox WHERE privacy_request_id=$1 AND message_type='PRIVACY_PROCESSING_STARTED')`, request.ID, subject).
			Scan(&executions, &jobs, &sessions, &resetTokens, &verificationTokens, &verificationNotices, &roles, &reviewerGrants, &executorGrants, &staffGrants, &revocations, &notices); err != nil {
			t.Fatal(err)
		}
		if status != string(AwaitingExecution) || !active || credential != 1 || executions != 0 || jobs != 0 || sessions != 1 || resetTokens != 1 || verificationTokens != 1 || verificationNotices != 1 || roles != 1 || reviewerGrants != 1 || executorGrants != 1 || staffGrants != 1 || revocations != 0 || notices != 0 {
			t.Fatalf("rollback status=%s active=%v credential=%d executions=%d jobs=%d sessions=%d reset=%d verification=%d verification_notices=%d roles=%d reviewers=%d executors=%d staff=%d revocations=%d notices=%d", status, active, credential, executions, jobs, sessions, resetTokens, verificationTokens, verificationNotices, roles, reviewerGrants, executorGrants, staffGrants, revocations, notices)
		}
		if _, err = pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=$1`, subject); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("worker-retry-is-fenced-and-bounded", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE privacy_erasure_category_jobs SET next_attempt_at=clock_timestamp()+interval '2 hours' WHERE status IN ('PENDING','RETRY_WAIT')`); err != nil {
			t.Fatal(err)
		}
		subject := user(nil)
		request, err := submit(subject, subject, Categories)
		if err != nil {
			t.Fatal(err)
		}
		request = claimVerify(request, false)
		request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Execução de repetição aprovada.", Decisions: decisions})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: request.PublicRef, Version: request.Version, Confirmed: true}); err != nil {
			t.Fatal(err)
		}
		worker := ExecutionWorker{Pool: pool, WorkerRef: uuid.New(), LeaseDuration: time.Minute, MaxAttempts: 2}
		first, err := worker.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(first.Checkpoints) == 0 || first.Job.LeaseEpoch < 1 || first.Job.AttemptCount != 1 {
			t.Fatalf("invalid first lease: %+v checkpoints=%d", first.Job, len(first.Checkpoints))
		}
		if _, err = worker.FailJob(ctx, first, ExecutionFailure{Classification: FailureRetryable, Stage: FailureStageExecute, Code: FailureDependencyUnavailable}); err != nil {
			t.Fatal(err)
		}
		if _, err = worker.Heartbeat(ctx, first); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("released lease heartbeat error=%v", err)
		}
		if _, err = pool.Exec(ctx, `UPDATE privacy_erasure_category_jobs SET next_attempt_at=clock_timestamp()+interval '1 hour' WHERE id<>$1 AND status='PENDING'`, first.Job.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE privacy_erasure_category_jobs SET next_attempt_at=clock_timestamp() WHERE id=$1`, first.Job.ID); err != nil {
			t.Fatal(err)
		}
		second, err := worker.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if second.Job.ID != first.Job.ID || second.Job.LeaseEpoch <= first.Job.LeaseEpoch || second.Job.AttemptCount != 2 {
			t.Fatalf("retry lease not fenced: first=%+v second=%+v", first.Job, second.Job)
		}
		if _, err = worker.Heartbeat(ctx, first); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("superseded lease heartbeat error=%v", err)
		}
		execution, err := worker.FailJob(ctx, second, ExecutionFailure{Classification: FailureRetryable, Stage: FailureStageExecute, Code: FailureDependencyUnavailable})
		if err != nil || execution.Status != "TERMINAL_FAILED" {
			t.Fatalf("retry exhaustion execution=%+v err=%v", execution, err)
		}
		lifecycle, err := dbgen.New(pool).GetPrivacyRequestExecutionLifecycle(ctx, execution.RequestID)
		if err != nil || lifecycle.Status != string(TerminalFailed) {
			t.Fatalf("retry exhaustion request=%+v err=%v", lifecycle, err)
		}
		var failures int
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM privacy_erasure_failures WHERE job_id=$1`, first.Job.ID).Scan(&failures); err != nil || failures != 2 {
			t.Fatalf("structured failures=%d err=%v", failures, err)
		}
	})
	t.Run("workers-claim-exclusively-heartbeat-recover-and-complete", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE privacy_erasure_category_jobs SET next_attempt_at=clock_timestamp()+interval '2 hours' WHERE status IN ('PENDING','RETRY_WAIT')`); err != nil {
			t.Fatal(err)
		}
		subject := user(nil)
		request, err := submit(subject, subject, Categories)
		if err != nil {
			t.Fatal(err)
		}
		request = claimVerify(request, false)
		request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Execução concorrente aprovada.", Decisions: decisions})
		if err != nil {
			t.Fatal(err)
		}
		execution, err := s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: request.PublicRef, Version: request.Version, Confirmed: true})
		if err != nil {
			t.Fatal(err)
		}
		workers := []ExecutionWorker{
			{Pool: pool, WorkerRef: uuid.New(), LeaseDuration: time.Minute, MaxAttempts: 3},
			{Pool: pool, WorkerRef: uuid.New(), LeaseDuration: time.Minute, MaxAttempts: 3},
		}
		claims := make(chan ExecutionLease, 2)
		errorsOut := make(chan error, 2)
		start := make(chan struct{})
		for index := range workers {
			go func(worker ExecutionWorker) {
				<-start
				lease, claimErr := worker.Claim(ctx)
				claims <- lease
				errorsOut <- claimErr
			}(workers[index])
		}
		close(start)
		first, second := <-claims, <-claims
		if err = <-errorsOut; err != nil {
			t.Fatal(err)
		}
		if err = <-errorsOut; err != nil {
			t.Fatal(err)
		}
		if first.Job.ID == uuid.Nil || second.Job.ID == uuid.Nil || first.Job.ID == second.Job.ID || first.Job.ExecutionID != execution.ID || second.Job.ExecutionID != execution.ID {
			t.Fatalf("claims were not exclusive: first=%+v second=%+v", first.Job, second.Job)
		}
		workerByRef := map[uuid.UUID]ExecutionWorker{workers[0].WorkerRef: workers[0], workers[1].WorkerRef: workers[1]}
		firstWorker := workerByRef[first.Job.WorkerRef]
		secondWorker := workerByRef[second.Job.WorkerRef]
		heartbeat, err := firstWorker.Heartbeat(ctx, first)
		if err != nil || heartbeat.Epoch != first.Job.LeaseEpoch || !heartbeat.ExpiresAt.Valid {
			t.Fatalf("heartbeat=%+v err=%v", heartbeat, err)
		}
		for _, checkpoint := range second.Checkpoints {
			if _, err = secondWorker.CompleteCheckpoint(ctx, second, checkpoint.OperationCode, checkpoint.ActionVersion); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = secondWorker.CompleteJob(ctx, second); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE privacy_erasure_job_leases SET heartbeat_at=acquired_at,expires_at=acquired_at+interval '1 millisecond' WHERE id=$1`, first.Job.ActiveLeaseID); err != nil {
			t.Fatal(err)
		}
		recoveryWorker := ExecutionWorker{Pool: pool, WorkerRef: uuid.New(), LeaseDuration: time.Minute, MaxAttempts: 3}
		recovered, err := recoveryWorker.Claim(ctx)
		if err != nil || recovered.Job.ID != first.Job.ID || recovered.Job.LeaseEpoch <= first.Job.LeaseEpoch {
			t.Fatalf("recovered=%+v err=%v", recovered.Job, err)
		}
		if _, err = firstWorker.CompleteCheckpoint(ctx, first, first.Checkpoints[0].OperationCode, first.Checkpoints[0].ActionVersion); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("stale checkpoint error=%v", err)
		}
		if _, err = firstWorker.CompleteJob(ctx, first); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("stale completion error=%v", err)
		}
		if _, err = firstWorker.FailJob(ctx, first, ExecutionFailure{Classification: FailureTerminal, Stage: FailureStageVerify, Code: FailureVerificationFailed}); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("stale failure error=%v", err)
		}
		missingOperation := ""
		for operation := range capabilities {
			if !slices.ContainsFunc(recovered.Checkpoints, func(checkpoint dbgen.PrivacyErasureJobCheckpoint) bool { return checkpoint.OperationCode == operation }) {
				missingOperation = operation
				break
			}
		}
		if missingOperation != "" {
			if _, err = recoveryWorker.CompleteCheckpoint(ctx, recovered, missingOperation, SupportedActionVersion); !errors.Is(err, ErrInvalid) {
				t.Fatalf("unbound checkpoint error=%v", err)
			}
		}
		for _, checkpoint := range recovered.Checkpoints {
			completedCheckpoint, checkpointErr := recoveryWorker.CompleteCheckpoint(ctx, recovered, checkpoint.OperationCode, checkpoint.ActionVersion)
			if checkpointErr != nil {
				t.Fatal(checkpointErr)
			}
			replayedCheckpoint, checkpointErr := recoveryWorker.CompleteCheckpoint(ctx, recovered, checkpoint.OperationCode, checkpoint.ActionVersion)
			if checkpointErr != nil || replayedCheckpoint.ID != completedCheckpoint.ID {
				t.Fatalf("checkpoint replay=%+v err=%v", replayedCheckpoint, checkpointErr)
			}
		}
		completed, err := recoveryWorker.CompleteJob(ctx, recovered)
		if err != nil || completed.Status != "SUCCEEDED" {
			t.Fatalf("completed execution=%+v err=%v", completed, err)
		}
		openRequest, err := dbgen.New(pool).GetPrivacyRequestExecutionLifecycle(ctx, request.ID)
		if err != nil || openRequest.Status != string(Processing) {
			t.Fatalf("#248 completion gate request=%+v err=%v", openRequest, err)
		}
	})
	t.Run("worker-claim-terminals-at-attempt-limit", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE privacy_erasure_category_jobs SET next_attempt_at=clock_timestamp()+interval '2 hours' WHERE status IN ('PENDING','RETRY_WAIT')`); err != nil {
			t.Fatal(err)
		}
		subject := user(nil)
		request, err := submit(subject, subject, Categories)
		if err != nil {
			t.Fatal(err)
		}
		request = claimVerify(request, false)
		request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Execução para validar o limite de tentativas.", Decisions: decisions})
		if err != nil {
			t.Fatal(err)
		}
		execution, err := s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: request.PublicRef, Version: request.Version, Confirmed: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE privacy_erasure_category_jobs SET attempt_count=2,next_attempt_at=clock_timestamp() WHERE execution_id=$1`, execution.ID); err != nil {
			t.Fatal(err)
		}
		worker := ExecutionWorker{Pool: pool, WorkerRef: uuid.New(), LeaseDuration: time.Minute, MaxAttempts: 2}
		if _, err = worker.Claim(ctx); !errors.Is(err, ErrRetryExhausted) {
			t.Fatalf("attempt limit claim error=%v", err)
		}
		stored, err := dbgen.New(pool).GetPrivacyErasureExecution(ctx, execution.ID)
		if err != nil || stored.Status != "TERMINAL_FAILED" {
			t.Fatalf("attempt limit execution=%+v err=%v", stored, err)
		}
	})
	t.Run("worker-claim-terminals-tampered-work-graph", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE privacy_erasure_category_jobs SET next_attempt_at=clock_timestamp()+interval '2 hours' WHERE status IN ('PENDING','RETRY_WAIT')`); err != nil {
			t.Fatal(err)
		}
		subject := user(nil)
		request, err := submit(subject, subject, Categories)
		if err != nil {
			t.Fatal(err)
		}
		request = claimVerify(request, false)
		request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Execução para validar adulteração do grafo.", Decisions: decisions})
		if err != nil {
			t.Fatal(err)
		}
		execution, err := s.StartExecution(ctx, StartInput{ActorID: reviewerB, Reference: request.PublicRef, Version: request.Version, Confirmed: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE privacy_erasure_category_jobs SET entry_sha256=decode(repeat('00',32),'hex'),next_attempt_at=clock_timestamp() WHERE execution_id=$1`, execution.ID); err != nil {
			t.Fatal(err)
		}
		worker := ExecutionWorker{Pool: pool, WorkerRef: uuid.New(), LeaseDuration: time.Minute, MaxAttempts: 3}
		if _, err = worker.Claim(ctx); !errors.Is(err, ErrExecutorUnavailable) {
			t.Fatalf("tampered graph claim error=%v", err)
		}
		stored, err := dbgen.New(pool).GetPrivacyErasureExecution(ctx, execution.ID)
		if err != nil || stored.Status != "TERMINAL_FAILED" {
			t.Fatalf("tampered graph execution=%+v err=%v", stored, err)
		}
	})
	t.Run("account-closure-revalidates-admin-and-legacy-session-before-cutoff", func(t *testing.T) {
		var priorRef uuid.UUID
		var priorVersion int64
		if err := pool.QueryRow(ctx, `SELECT public_ref,version FROM data_erasure_requests WHERE requester_user_id=$1 AND status IN ('RECEIVED','UNDER_REVIEW','AWAITING_EXECUTION','PARTIALLY_APPROVED')`, owner).Scan(&priorRef, &priorVersion); err == nil {
			if _, err = s.Change(ctx, ReviewInput{ActorID: owner, Reference: priorRef, Version: priorVersion, Action: "cancel"}); err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN' ON CONFLICT DO NOTHING`, reviewerB); err != nil {
			t.Fatal(err)
		}
		request, err := submit(owner, owner, AccountClosure)
		if err != nil {
			t.Fatal(err)
		}
		request = claimVerify(request, false)
		request, err = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: request.PublicRef, Version: request.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Encerramento aprovado.", Decisions: closureDecisions})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=$1 AND role_id=(SELECT id FROM platform_roles WHERE code='ADMIN')`, reviewerB); err != nil {
			t.Fatal(err)
		}
		input := StartInput{ActorID: reviewerB, Reference: request.PublicRef, Version: request.Version, Confirmed: true}
		if _, err = s.StartExecution(ctx, input); !errors.Is(err, ErrClosureSafeguards) {
			t.Fatalf("last usable admin start error=%v", err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, reviewerB); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO sessions(token,data,expiry,subject_indexed) VALUES('legacy-privacy-session',decode('00','hex'),now()+interval '1 hour',false)`); err != nil {
			t.Fatal(err)
		}
		if _, err = s.StartExecution(ctx, input); !errors.Is(err, ErrExecutorUnavailable) {
			t.Fatalf("legacy session start error=%v", err)
		}
		if _, err = pool.Exec(ctx, `DELETE FROM sessions WHERE token='legacy-privacy-session'`); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES('indexed-privacy-session',decode('00','hex'),now()+interval '1 hour',$1,true)`, owner); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO password_reset_tokens(user_id,email,token_digest,expires_at) VALUES($1,$2::text,digest($2::text,'sha256'),clock_timestamp()+interval '1 hour')`, owner, owner.String()+"@example.test"); err != nil {
			t.Fatal(err)
		}
		execution, err := s.StartExecution(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		var active bool
		var indexedSessions, ownerRoles, activeTokens int
		if err = pool.QueryRow(ctx, "SELECT is_active FROM users WHERE id=$1", owner).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id=$1", owner).Scan(&indexedSessions); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, "SELECT count(*) FROM user_platform_roles WHERE user_id=$1", owner).Scan(&ownerRoles); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, "SELECT count(*) FROM password_reset_tokens WHERE user_id=$1 AND consumed_at IS NULL", owner).Scan(&activeTokens); err != nil {
			t.Fatal(err)
		}
		if active || indexedSessions != 0 || ownerRoles != 0 || activeTokens != 0 || execution.ID == uuid.Nil {
			t.Fatalf("account cutoff incomplete: active=%v sessions=%d roles=%d active_tokens=%d execution=%s", active, indexedSessions, ownerRoles, activeTokens, execution.ID)
		}
	})
}
