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
	if _, e = pool.Exec(ctx, string(baseline)); e != nil {
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
	s := Service{Pool: pool, Enabled: true, Key: []byte(strings.Repeat("k", 32)), ContactURL: "https://example.test/legal/direitos"}
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
		if _, err = s.Change(ctx, invalid); !errors.Is(err, ErrForbidden) {
			t.Fatalf("missing dependant: %v", err)
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
		r, e := submit(guardian, guardian, AccountClosure)
		if e != nil {
			t.Fatal(e)
		}
		r = claimVerify(r, false)
		command := ReviewInput{ActorID: reviewerA, Reference: r.PublicRef, Version: r.Version, Action: "approve", PolicyVersion: p.Version, Explanation: "Decisão explicada.", Decisions: closureDecisions}
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
}
