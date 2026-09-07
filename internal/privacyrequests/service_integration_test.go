//go:build integration

package privacyrequests

import (
	"context"
	"errors"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"os"
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
	if _, e = pool.Exec(ctx, string(baseline)); e != nil {
		t.Fatal(e)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("privacy-test-password"), bcrypt.MinCost)
	user := func(guardian *uuid.UUID) uuid.UUID {
		id := uuid.New()
		var email, ph *string
		dob := "2015-01-01"
		if guardian == nil {
			email = ptr(id.String() + "@example.test")
			ph = ptr(string(hash))
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
	s := Service{Pool: pool, Enabled: true, Key: []byte(strings.Repeat("k", 32)), ContactURL: "https://example.test/legal/direitos"}
	p := testPolicy()
	p.Version = "test-" + uuid.NewString()
	if e = s.ImportPolicy(ctx, owner, p); e != nil {
		t.Fatal(e)
	}
	for _, r := range []uuid.UUID{reviewerA, reviewerB} {
		if e = s.GrantReviewer(ctx, owner, r, false); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.Activate(ctx, owner, p.Version, true, true); e != nil {
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
	})
	submit := func(actor, subject uuid.UUID, kind ScopeKind) (dbgen.DataErasureRequest, error) {
		scope := Scope{Kind: kind}
		if kind == Categories {
			scope.Categories = []Category{"alpha", "beta"}
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
	decisions := map[string]CategoryDecision{"alpha": {Outcome: "APPROVE"}, "beta": {Outcome: "APPROVE"}}
	t.Run("receipt-auth-idempotency-atomic-outbox", func(t *testing.T) {
		actor := user(nil)
		in := SubmitInput{ActorID: actor, SubjectID: actor, RequestKey: uuid.New(), CredentialVersion: 1, Password: "wrong", IP: actor.String(), PolicyVersion: p.Version, Scope: Scope{Kind: Categories, Categories: []Category{"alpha"}}}
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
		in := SubmitInput{ActorID: actor, SubjectID: actor, RequestKey: uuid.New(), CredentialVersion: 0, Password: "privacy-test-password", IP: ip, PolicyVersion: p.Version, Scope: Scope{Kind: Categories, Categories: []Category{"alpha"}}}
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
			_, err := s.Submit(ctx, SubmitInput{ActorID: actor, SubjectID: actor, RequestKey: uuid.New(), CredentialVersion: 1, Password: "privacy-test-password", IP: sharedIP, PolicyVersion: p.Version, Scope: Scope{Kind: Categories, Categories: []Category{"alpha"}}})
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
		r = claimVerify(r, false)
		r, e = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "partial", PolicyVersion: p.Version, Explanation: "Uma categoria aguarda conservação aprovada.", Decisions: map[string]CategoryDecision{"alpha": {Outcome: "APPROVE"}, "beta": {Outcome: "RETAIN", Ground: "HOLD"}}})
		if e != nil || r.Status != "PARTIALLY_APPROVED" || r.ClosedAt.Valid {
			t.Fatal(r.Status, e)
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
		r, e := submit(guardian, guardian, AccountClosure)
		if e != nil {
			t.Fatal(e)
		}
		r = claimVerify(r, false)
		command := ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Decisão explicada.", Decisions: decisions}
		if _, e = s.Change(ctx, command); !errors.Is(e, ErrClosureSafeguards) {
			t.Fatal(e)
		}
		r, e = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "resolve-dependant", DependantID: minor, ResolutionCode: "FORMAL_RESOLUTION", ResolutionExplanation: "Resolução formal verificada para o caso."})
		if e != nil {
			t.Fatal(e)
		}
		command.Version = r.Version
		r, e = s.Change(ctx, command)
		if e != nil || r.Status != "AWAITING_EXECUTION" {
			t.Fatal(e)
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
		r, e = s.Change(ctx, ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "refuse", PolicyVersion: p.Version, Explanation: "Exceção aprovada para ambas as categorias.", Decisions: map[string]CategoryDecision{"alpha": {Outcome: "RETAIN", Ground: "HOLD"}, "beta": {Outcome: "RETAIN", Ground: "HOLD"}}})
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
		_, e = pool.Exec(ctx, "UPDATE data_erasure_requests SET closed_at=now()-interval '3 years',evidence_expires_at=now()-interval '1 day' WHERE id=$1", r.ID)
		if e != nil {
			t.Fatal(e)
		}
		result, e = s.Expire(ctx, owner)
		if e != nil || result.EvidenceRecords != 1 {
			t.Fatal(result, e)
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
}
