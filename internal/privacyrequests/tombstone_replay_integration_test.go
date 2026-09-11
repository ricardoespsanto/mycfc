//go:build integration

package privacyrequests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func TestAuthenticatedTombstoneReplayIsExactIdempotentAndSubjectScoped(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	effectiveAt := time.Date(2026, time.September, 10, 23, 30, 0, 0, time.UTC)
	if _, err = tx.Exec(ctx, "SET LOCAL TIME ZONE 'Europe/Lisbon'"); err != nil {
		t.Fatal(err)
	}

	subject, unrelated, admin := uuid.New(), uuid.New(), uuid.New()
	for id, email := range map[uuid.UUID]string{
		subject:   "restore-subject-" + uuid.NewString() + "@example.test",
		unrelated: "restore-unrelated-" + uuid.NewString() + "@example.test",
		admin:     "restore-admin-" + uuid.NewString() + "@example.test",
	} {
		if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Restore fixture',$2,'hash','1990-01-01')`, id, email); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO member_profiles(user_id,address_line1) VALUES($1,'Preserved address')`, id); err != nil {
			t.Fatal(err)
		}
		tokenDigest := sha256.Sum256([]byte(id.String()))
		if _, err = tx.Exec(ctx, `INSERT INTO password_reset_tokens(id,user_id,email,token_digest,created_at,expires_at)
			VALUES($1,$2,$3,$4,$5::timestamptz,$5::timestamptz+interval '1 hour')`, uuid.New(), id, email, tokenDigest[:], time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{subject, unrelated} {
		if _, err = tx.Exec(ctx, `INSERT INTO staff_grants(user_id,capability,granted_by_id,granted_at) VALUES($1,'MODERATOR',$2,$3)`, id, unrelated, effectiveAt.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_reviewer_grants(user_id,granted_by,granted_at) VALUES($1,$2,$3)`, id, unrelated, effectiveAt.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_executor_grants(user_id,granted_by,granted_at) VALUES($1,$2,$3)`, id, unrelated, effectiveAt.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	activationPolicy := "replay-activation-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,working_retention_days,adopted_at,adopted_by)
		VALUES($1,'[]','privacy-erasure-executor/v2','privacy-erasure-plan/v2',90,clock_timestamp(),$2)`, activationPolicy, admin); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin); err != nil {
		t.Fatal(err)
	}
	activatePrivacyIntegrationFixture(t, ctx, tx, admin, unrelated, activationPolicy)
	for _, id := range []uuid.UUID{subject, unrelated} {
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.provider_connections(
		 id,subject_user_id,service_code,provider_role,provider_contract_version,registry_evidence_key_id,registry_evidence_digest,
		 target_key_id,target_opaque,credential_key_id,credential_opaque,state,sync_enabled,webhook_enabled,reconnect_enabled,created_at,updated_at)
		 VALUES($1,$2,$3,'PROCESSOR','test-v1','test-key',$4,'test-key',$4,'test-key',$4,'ACTIVE',true,true,true,clock_timestamp(),clock_timestamp())`,
			uuid.New(), id, "replay-"+id.String(), bytes.Repeat([]byte{7}, 32)); err != nil {
			t.Fatal(err)
		}
	}
	programmeID := uuid.New()
	seasonSuffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	historySeason, activeSeason, futureSeason := uuid.New(), uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO programmes(id,code,name_pt) VALUES($1,$2,'Programa replay')`, programmeID, "Replay"+seasonSuffix); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES
		 ($1,$4||'H','Histórico',($5::timestamptz AT TIME ZONE 'UTC')::date-400,($5::timestamptz AT TIME ZONE 'UTC')::date-300),
		 ($2,$4||'A','Atual',($5::timestamptz AT TIME ZONE 'UTC')::date-30,($5::timestamptz AT TIME ZONE 'UTC')::date+30),
		 ($3,$4||'F','Futuro',($5::timestamptz AT TIME ZONE 'UTC')::date+1,($5::timestamptz AT TIME ZONE 'UTC')::date+365)`, historySeason, activeSeason, futureSeason, seasonSuffix, effectiveAt); err != nil {
		t.Fatal(err)
	}
	historyMembership, activeMembership, futureMembership := uuid.New(), uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES
		 ($1,$4,$5,$8,($9::timestamptz AT TIME ZONE 'UTC')::date-400,($9::timestamptz AT TIME ZONE 'UTC')::date-300),
		 ($2,$4,$6,$8,($9::timestamptz AT TIME ZONE 'UTC')::date-30,NULL),
		 ($3,$4,$7,$8,($9::timestamptz AT TIME ZONE 'UTC')::date+1,NULL)`, historyMembership, activeMembership, futureMembership, subject, historySeason, activeSeason, futureSeason, programmeID, effectiveAt); err != nil {
		t.Fatal(err)
	}

	protector, privateKey := tombstoneProtectorFixture(t)
	record := tombstoneFixture()
	record.SubjectUserID = subject
	record.ExecutionStart = effectiveAt
	record.Replay.Operations = []string{"AUTH_ACCESS_REVOKE", "AUTH_TOKEN_DELETE", "PROFILE_IDENTITY_DELETE", "PROVIDER_LOCAL_FENCE", "IDENTITY_CLEAR", "MEMBERSHIP_ACTIVE_REVOKE", "MEMBERSHIP_HISTORY_ANONYMIZE"}
	if _, err = tx.Exec(ctx, "SAVEPOINT expected_membership_postcondition"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE user_memberships SET ends_on=($2::timestamptz AT TIME ZONE 'UTC')::date-1 WHERE id=$1`, activeMembership, record.ExecutionStart); err != nil {
		t.Fatal(err)
	}
	record.Replay.MembershipHistoryPostcondition = databaseMembershipPostcondition(t, ctx, tx, []uuid.UUID{historyMembership, activeMembership}, record.ExecutionStart)
	if _, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT expected_membership_postcondition"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "RELEASE SAVEPOINT expected_membership_postcondition"); err != nil {
		t.Fatal(err)
	}
	closedAt := record.ExecutionStart.Add(time.Hour)
	closure := TombstoneClosure{Version: TombstoneClosureVersion, Tombstone: record, ClosedAt: closedAt,
		EvidenceExpiresAt: closedAt.AddDate(0, 24, 0), ErasureEffectiveAt: record.ExecutionStart}
	sealed, err := protector.SealClosure(closure)
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := AuthenticateReplayTombstone(privateKey, listedTombstoneFixture(sealed))
	if err != nil {
		t.Fatal(err)
	}
	assertV4ImportRejectsNullMembershipDigest(t, ctx, tx, uuid.New(), authenticated)
	worker := TombstoneReplayWorker{Store: tx, WorkerRef: uuid.New()}
	if _, err = tx.Exec(ctx, "SAVEPOINT corrupt_ordinary_postcondition"); err != nil {
		t.Fatal(err)
	}
	corruptSealed, sealErr := protector.SealClosure(corruptClosureMembershipDigest(closure))
	if sealErr != nil {
		t.Fatal(sealErr)
	}
	corruptAuthenticated, authenticateErr := AuthenticateReplayTombstone(privateKey, listedTombstoneFixture(corruptSealed))
	if authenticateErr != nil {
		t.Fatal(authenticateErr)
	}
	if _, replayErr := worker.Replay(ctx, corruptAuthenticated); !errors.Is(replayErr, ErrTombstoneReplayUnavailable) {
		t.Fatalf("corrupt ordinary membership postcondition error=%v", replayErr)
	}
	if _, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT corrupt_ordinary_postcondition"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "RELEASE SAVEPOINT corrupt_ordinary_postcondition"); err != nil {
		t.Fatal(err)
	}
	result, err := worker.Replay(ctx, authenticated)
	if err != nil {
		t.Fatal(err)
	}
	runID := result.RunID
	repeated, err := (TombstoneReplayWorker{Store: tx, WorkerRef: uuid.New()}).Replay(ctx, authenticated)
	if err != nil || repeated.RunID != runID || repeated.AlreadyApplied || result.AlreadyApplied {
		t.Fatalf("idempotent replay result=%+v initial=%+v err=%v", repeated, result, err)
	}

	var erased, subjectProfile, subjectToken, unrelatedProfile, unrelatedToken bool
	var subjectEmail *string
	var subjectName, runStatus string
	var checkpointCount, succeededCount, subjectActiveGrants, unrelatedActiveGrants, replayGrantEvents int
	var subjectProviders, unrelatedProviders, retainedMemberships, futureMemberships, subjectMemberships int
	if err = tx.QueryRow(ctx, `SELECT erased_at IS NOT NULL,email::text,name FROM users WHERE id=$1`, subject).Scan(&erased, &subjectEmail, &subjectName); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM member_profiles WHERE user_id=$1),EXISTS(SELECT 1 FROM password_reset_tokens WHERE user_id=$1),
		EXISTS(SELECT 1 FROM member_profiles WHERE user_id=$2),EXISTS(SELECT 1 FROM password_reset_tokens WHERE user_id=$2)`, subject, unrelated).
		Scan(&subjectProfile, &subjectToken, &unrelatedProfile, &unrelatedToken); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT run.status,count(checkpoint.*),count(checkpoint.*) FILTER(WHERE checkpoint.status='SUCCEEDED')
		FROM privacy_protected.restore_replay_runs run JOIN privacy_protected.restore_replay_checkpoints checkpoint ON checkpoint.run_id=run.id
		WHERE run.id=$1 GROUP BY run.status`, runID).Scan(&runStatus, &checkpointCount, &succeededCount); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM staff_grants WHERE user_id=$1 AND revoked_at IS NULL)
		 +(SELECT count(*) FROM privacy_reviewer_grants WHERE user_id=$1 AND revoked_at IS NULL)
		 +(SELECT count(*) FROM privacy_executor_grants WHERE user_id=$1 AND revoked_at IS NULL),
		(SELECT count(*) FROM staff_grants WHERE user_id=$2 AND revoked_at IS NULL)
		 +(SELECT count(*) FROM privacy_reviewer_grants WHERE user_id=$2 AND revoked_at IS NULL)
		 +(SELECT count(*) FROM privacy_executor_grants WHERE user_id=$2 AND revoked_at IS NULL),
		(SELECT count(*) FROM staff_grant_audit_events WHERE actor_replay_run_id=$3 AND action='REVOKED')
		 +(SELECT count(*) FROM privacy_reviewer_grant_events WHERE actor_ref=$3 AND action='REVOKED')
		 +(SELECT count(*) FROM privacy_executor_grant_events WHERE actor_ref=$3 AND action='REVOKED')`, subject, unrelated, runID).
		Scan(&subjectActiveGrants, &unrelatedActiveGrants, &replayGrantEvents); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE subject_user_id=$1),count(*) FILTER(WHERE subject_user_id=$2)
	 FROM privacy_protected.provider_connections`, subject, unrelated).Scan(&subjectProviders, &unrelatedProviders); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT
		count(*) FILTER(WHERE id IN($2,$3) AND user_id IS NULL AND principal_id IS NOT NULL AND ends_on<$5::date),
		count(*) FILTER(WHERE id=$4),count(*) FILTER(WHERE user_id=$1)
		FROM user_memberships`, subject, historyMembership, activeMembership, futureMembership, record.ExecutionStart).
		Scan(&retainedMemberships, &futureMemberships, &subjectMemberships); err != nil {
		t.Fatal(err)
	}
	if !erased || subjectEmail != nil || subjectName != "Conta eliminada" || subjectProfile || subjectToken || !unrelatedProfile || !unrelatedToken ||
		runStatus != "SUCCEEDED" || checkpointCount != 7 || succeededCount != 7 || subjectActiveGrants != 0 || unrelatedActiveGrants != 3 || replayGrantEvents != 3 ||
		subjectProviders != 0 || unrelatedProviders != 1 || retainedMemberships != 2 || futureMemberships != 0 || subjectMemberships != 0 {
		t.Fatalf("scope/result erased=%t email=%v name=%q profiles=%t/%t tokens=%t/%t run=%s checkpoints=%d/%d grants=%d/%d events=%d",
			erased, subjectEmail, subjectName, subjectProfile, unrelatedProfile, subjectToken, unrelatedToken, runStatus, checkpointCount, succeededCount,
			subjectActiveGrants, unrelatedActiveGrants, replayGrantEvents)
	}

	conflicting, err := protector.SealClosure(closure)
	if err != nil {
		t.Fatal(err)
	}
	conflictingAuth, err := AuthenticateReplayTombstone(privateKey, listedTombstoneFixture(conflicting))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = worker.Replay(ctx, conflictingAuth); !errors.Is(err, ErrTombstoneReplayUnavailable) {
		t.Fatalf("conflicting ciphertext replay error=%v", err)
	}
}

func TestReplayRecognizesPostErasureBackupVerifiesAndRecordsImmutableEvidence(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	subject, actor, executor := uuid.New(), uuid.New(), uuid.New()
	requestID, requestRef, executionID := uuid.New(), uuid.New(), uuid.New()
	effective := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	for _, item := range []struct {
		id    uuid.UUID
		email string
	}{{subject, "post-erasure-" + uuid.NewString() + "@example.test"}, {actor, "post-erasure-actor-" + uuid.NewString() + "@example.test"}, {executor, "post-erasure-executor-" + uuid.NewString() + "@example.test"}} {
		if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth)VALUES($1,'Post erasure fixture',$2,'hash','1990-01-01')`, item.id, item.email); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO consent_forms(user_id,consent_type,document_version,document_sha256,is_accepted,date_signed)
	 VALUES($1,'Termos_Gerais','v1',$2,true,$3)`, subject, strings.Repeat("a", 64), effective.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	planDigest := bytes.Repeat([]byte{8}, 32)
	plan := `{"policy_version":"replay-source-policy","executor_version":"privacy-erasure-executor/v2","schema_version":"privacy-erasure-plan/v2"}`
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,adopted_at,adopted_by)
		 VALUES('replay-source-policy','[]','privacy-erasure-executor/v2','privacy-erasure-plan/v2',$1,$2)`, []any{effective, actor}},
		{`INSERT INTO data_erasure_requests(id,public_ref,idempotency_key,subject_user_id,requester_user_id,subject_kind,scope_kind,status,version,
		 received_at,due_at,decided_by,decided_at,decision_code,policy_version,policy_snapshot,updated_at)
		 VALUES($1,$2,$3,$4,$4,'SELF','ACCOUNT_CLOSURE','PROCESSING',2,$5::timestamptz-interval '1 day',$5::timestamptz+interval '1 day',$6,$5::timestamptz,'APPROVED','replay-source-policy','{}',$5::timestamptz)`, []any{requestID, requestRef, uuid.New(), subject, effective, actor}},
		{`INSERT INTO privacy_request_execution_plans(request_id,policy_version,executor_version,schema_version,plan,plan_sha256,created_at)
		 VALUES($1,'replay-source-policy','privacy-erasure-executor/v2','privacy-erasure-plan/v2',$2,$3,$4)`, []any{requestID, plan, planDigest, effective}},
		{`INSERT INTO privacy_erasure_executions(id,request_id,plan_sha256,executor_version,schema_version,request_version_at_start,status,started_by_ref,
		 accepted_at,started_at,finished_at,updated_at) VALUES($1,$2,$3,'privacy-erasure-executor/v2','privacy-erasure-plan/v2',2,'SUCCEEDED',$4,$5,$5,$5,$5)`, []any{executionID, requestID, planDigest, actor, effective}},
	}
	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_executor_grants(user_id,granted_by,granted_at) VALUES($1,$2,clock_timestamp())`, executor, actor); err != nil {
		t.Fatal(err)
	}
	activatePrivacyIntegrationFixture(t, ctx, tx, actor, executor, "replay-source-policy")
	if _, err = tx.Exec(ctx, `UPDATE users SET name='Conta eliminada',email=NULL,email_verified_at=NULL,password_hash=NULL,
	 date_of_birth='1900-01-01',is_active=false,leaderboard_visible=false,credential_version=credential_version+1,
	 erased_at=$2,erasure_execution_id=$3,updated_at=$2 WHERE id=$1`, subject, effective, executionID); err != nil {
		t.Fatal(err)
	}

	protector, privateKey := tombstoneProtectorFixture(t)
	record := tombstoneFixture()
	record.ExecutionID, record.RequestID, record.RequestRef, record.SubjectUserID = executionID, requestID, requestRef, subject
	record.PlanSHA256, record.ExecutionStart = planDigest, effective.Add(-time.Minute)
	record.Replay.Operations = []string{"AUTH_TOKEN_DELETE", "PROFILE_IDENTITY_DELETE", "PROVIDER_LOCAL_FENCE", "IDENTITY_CLEAR"}
	record.Replay.MembershipHistoryPostcondition = databaseMembershipPostcondition(t, ctx, tx, nil, effective)
	closedAt := effective.Add(time.Hour)
	sealed, err := protector.SealClosure(TombstoneClosure{Version: TombstoneClosureVersion, Tombstone: record, ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0), ErasureEffectiveAt: effective})
	if err != nil {
		t.Fatal(err)
	}
	listed := listedTombstoneFixture(sealed)
	listed.RetainUntil = sealed.RetainUntil
	authenticated, err := AuthenticateReplayTombstone(privateKey, listed)
	if err != nil {
		t.Fatal(err)
	}
	assertV4ImportRejectsNullMembershipDigest(t, ctx, tx, uuid.New(), authenticated)
	worker := TombstoneReplayWorker{Store: tx, WorkerRef: uuid.New()}
	if _, err = tx.Exec(ctx, "SAVEPOINT corrupt_already_applied_postcondition"); err != nil {
		t.Fatal(err)
	}
	corruptSealed, sealErr := protector.SealClosure(corruptClosureMembershipDigest(TombstoneClosure{Version: TombstoneClosureVersion,
		Tombstone: record, ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0), ErasureEffectiveAt: effective}))
	if sealErr != nil {
		t.Fatal(sealErr)
	}
	corruptListed := listedTombstoneFixture(corruptSealed)
	corruptListed.RetainUntil = corruptSealed.RetainUntil
	corruptAuthenticated, authenticateErr := AuthenticateReplayTombstone(privateKey, corruptListed)
	if authenticateErr != nil {
		t.Fatal(authenticateErr)
	}
	if _, replayErr := worker.Replay(ctx, corruptAuthenticated); !errors.Is(replayErr, ErrTombstoneReplayUnavailable) {
		t.Fatalf("corrupt already-applied membership postcondition error=%v", replayErr)
	}
	if _, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT corrupt_already_applied_postcondition"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "RELEASE SAVEPOINT corrupt_already_applied_postcondition"); err != nil {
		t.Fatal(err)
	}
	result, err := worker.Replay(ctx, authenticated)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AlreadyApplied {
		t.Fatal("post-erasure source was mutated instead of recognized")
	}
	var outcome string
	var evidenceCount, affected int
	var erasedAt, ceasedAt, expiresAt time.Time
	if err = tx.QueryRow(ctx, `SELECT run.outcome_code,
	 (SELECT count(*) FROM privacy_protected.restore_replay_already_applied_evidence evidence WHERE evidence.run_id=run.id),
	 (SELECT count(*) FROM privacy_protected.restore_replay_checkpoints checkpoint WHERE checkpoint.run_id=run.id AND checkpoint.affected_rows=0)
	 FROM privacy_protected.restore_replay_runs run WHERE run.id=$1`, result.RunID).Scan(&outcome, &evidenceCount, &affected); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT users.erased_at,consent.ceased_at,consent.evidence_expires_at FROM users
	 JOIN consent_forms consent ON consent.user_id=users.id WHERE users.id=$1`, subject).Scan(&erasedAt, &ceasedAt, &expiresAt); err != nil {
		t.Fatal(err)
	}
	if outcome != "ALREADY_APPLIED_SOURCE" || evidenceCount != 1 || affected != 4 || !erasedAt.Equal(effective) ||
		!ceasedAt.Equal(effective) || !expiresAt.Equal(effective.AddDate(3, 0, 0)) {
		t.Fatalf("outcome=%s evidence=%d checkpoints=%d clocks=%s/%s/%s", outcome, evidenceCount, affected, erasedAt, ceasedAt, expiresAt)
	}
}

func corruptClosureMembershipDigest(closure TombstoneClosure) TombstoneClosure {
	record := closure.Tombstone
	replay := *record.Replay
	postcondition := *replay.MembershipHistoryPostcondition
	postcondition.SHA256 = bytes.Clone(postcondition.SHA256)
	postcondition.SHA256[len(postcondition.SHA256)-1] ^= 0xff
	replay.MembershipHistoryPostcondition = &postcondition
	record.Replay = &replay
	closure.Tombstone = record
	return closure
}

func databaseMembershipPostcondition(t *testing.T, ctx context.Context, tx pgx.Tx, membershipIDs []uuid.UUID, effectiveAt time.Time) *MembershipHistoryPostcondition {
	t.Helper()
	var contract string
	var digest []byte
	var membershipCount, variationCount int64
	if err := tx.QueryRow(ctx, `SELECT contract,postcondition_sha256,membership_count,variation_count
		FROM privacy_membership_history_compute($1::uuid[],$2,false)`, membershipIDs, effectiveAt).
		Scan(&contract, &digest, &membershipCount, &variationCount); err != nil {
		t.Fatal(err)
	}
	if membershipCount < 0 || variationCount < 0 {
		t.Fatal("negative membership postcondition count")
	}
	return &MembershipHistoryPostcondition{Contract: contract, SHA256: digest, MembershipCount: uint64(membershipCount), VariationCount: uint64(variationCount)}
}

func TestReplayDatabaseAPIsRejectUnauthenticatedVersionsUnsupportedOperationsAndOutOfOrderCheckpoints(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	subject := uuid.New()
	email := "restore-api-subject-" + uuid.NewString() + "@example.test"
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Replay API fixture',$2,'hash','1990-01-01')`, subject, email); err != nil {
		t.Fatal(err)
	}
	tokenDigest := sha256.Sum256([]byte(subject.String()))
	if _, err = tx.Exec(ctx, `INSERT INTO password_reset_tokens(id,user_id,email,token_digest,created_at,expires_at)
		VALUES($1,$2,$3,$4,clock_timestamp(),clock_timestamp()+interval '1 hour')`, uuid.New(), subject, email, tokenDigest[:]); err != nil {
		t.Fatal(err)
	}

	protector, privateKey := tombstoneProtectorFixture(t)
	record := tombstoneFixture()
	record.SubjectUserID = subject
	record.Replay.Operations = []string{"AUTH_TOKEN_DELETE", "IDENTITY_CLEAR"}
	sealed, err := protector.Seal(record)
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := AuthenticateReplayTombstone(privateKey, listedTombstoneFixture(sealed))
	if err != nil {
		t.Fatal(err)
	}
	workerRef := uuid.New()
	params := replayImportParams(workerRef, authenticated)
	q := dbgen.New(tx)
	assertRejected := func(name string, mutate func(*dbgen.ImportAuthenticatedPrivacyRestoreTombstoneV2Params)) {
		t.Helper()
		if _, err = tx.Exec(ctx, "SAVEPOINT "+name); err != nil {
			t.Fatal(err)
		}
		candidate := params
		candidate.Operations = append([]string(nil), params.Operations...)
		mutate(&candidate)
		if _, err = q.ImportAuthenticatedPrivacyRestoreTombstoneV2(ctx, candidate); err == nil {
			t.Fatalf("%s input accepted", name)
		}
		if _, rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		if _, releaseErr := tx.Exec(ctx, "RELEASE SAVEPOINT "+name); releaseErr != nil {
			t.Fatal(releaseErr)
		}
	}
	assertRejected("reject_v1", func(candidate *dbgen.ImportAuthenticatedPrivacyRestoreTombstoneV2Params) {
		candidate.RecordVersion = TombstoneRecordVersionV1
	})
	assertRejected("reject_external", func(candidate *dbgen.ImportAuthenticatedPrivacyRestoreTombstoneV2Params) {
		candidate.Operations = []string{"OBJECT_VERSION_DELETE"}
	})

	importID, err := q.ImportAuthenticatedPrivacyRestoreTombstoneV2(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := q.BeginPrivacyRestoreReplay(ctx, dbgen.BeginPrivacyRestoreReplayParams{ImportID: importID, WorkerRef: workerRef})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "SAVEPOINT reject_order"); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ExecutePrivacyRestoreReplayCheckpoint(ctx, dbgen.ExecutePrivacyRestoreReplayCheckpointParams{
		RunID: runID, WorkerRef: workerRef, OperationPosition: 2, OperationCode: "IDENTITY_CLEAR",
		ActionVersion: SupportedActionVersion, PrescriptionSha256: authenticated.prescriptionSHA256,
	}); err == nil {
		t.Fatal("out-of-order replay checkpoint accepted")
	}
	if _, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT reject_order"); err != nil {
		t.Fatal(err)
	}
	var erased, tokenExists bool
	var pending int
	if err = tx.QueryRow(ctx, `SELECT erased_at IS NOT NULL FROM users WHERE id=$1`, subject).Scan(&erased); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM password_reset_tokens WHERE user_id=$1),
		count(*) FILTER(WHERE status='PENDING') FROM privacy_protected.restore_replay_checkpoints WHERE run_id=$2`, subject, runID).Scan(&tokenExists, &pending); err != nil {
		t.Fatal(err)
	}
	if erased || !tokenExists || pending != 2 {
		t.Fatalf("rejected checkpoint mutated state: erased=%t token=%t pending=%d", erased, tokenExists, pending)
	}
}

func replayImportParams(workerRef uuid.UUID, authenticated AuthenticatedReplayTombstone) dbgen.ImportAuthenticatedPrivacyRestoreTombstoneV2Params {
	return dbgen.ImportAuthenticatedPrivacyRestoreTombstoneV2Params{
		WorkerRef: workerRef, Kind: authenticated.kind, RecordVersion: authenticated.record.Version,
		EnvelopeVersion: authenticated.envelopeVersion, EncryptionKeyID: authenticated.encryptionKeyID,
		LocatorKeyID: authenticated.locatorKeyID, LocatorDigest: bytes.Clone(authenticated.locatorDigest),
		CiphertextSha256: bytes.Clone(authenticated.ciphertextSHA256), ObjectVersionID: authenticated.objectVersion,
		WrittenAt: stamp(authenticated.writtenAt), VerifiedAt: stamp(authenticated.verifiedAt), RetainUntil: pgtype.Timestamptz{},
		SourceExecutionID: authenticated.record.ExecutionID, SourceRequestID: authenticated.record.RequestID,
		SourceRequestRef: authenticated.record.RequestRef, SubjectUserID: authenticated.record.SubjectUserID,
		PlanSha256: bytes.Clone(authenticated.record.PlanSHA256), WorksetSha256: bytes.Clone(authenticated.record.WorksetSHA256),
		ExecutionStartedAt: stamp(authenticated.record.ExecutionStart), ReplayVersion: authenticated.record.Replay.Version,
		ActionVersion: authenticated.record.Replay.ActionVersion, Operations: append([]string(nil), authenticated.record.Replay.Operations...),
		PrescriptionSha256: bytes.Clone(authenticated.prescriptionSHA256), RecordSha256: bytes.Clone(authenticated.recordSHA256),
	}
}

func assertV4ImportRejectsNullMembershipDigest(t *testing.T, ctx context.Context, tx pgx.Tx, workerRef uuid.UUID, authenticated AuthenticatedReplayTombstone) {
	t.Helper()
	postcondition := authenticated.record.Replay.MembershipHistoryPostcondition
	params := dbgen.ImportAuthenticatedPrivacyRestoreTombstoneV4HardenedParams{
		WorkerRef: workerRef, Kind: authenticated.kind, RecordVersion: authenticated.record.Version,
		EnvelopeVersion: authenticated.envelopeVersion, EncryptionKeyID: authenticated.encryptionKeyID,
		LocatorKeyID: authenticated.locatorKeyID, LocatorDigest: bytes.Clone(authenticated.locatorDigest),
		CiphertextSha256: bytes.Clone(authenticated.ciphertextSHA256), ObjectVersionID: authenticated.objectVersion,
		WrittenAt: stamp(authenticated.writtenAt), VerifiedAt: stamp(authenticated.verifiedAt), RetainUntil: stamp(authenticated.retainUntil),
		SourceExecutionID: authenticated.record.ExecutionID, SourceRequestID: authenticated.record.RequestID,
		SourceRequestRef: authenticated.record.RequestRef, SubjectUserID: authenticated.record.SubjectUserID,
		PlanSha256: bytes.Clone(authenticated.record.PlanSHA256), WorksetSha256: bytes.Clone(authenticated.record.WorksetSHA256),
		ExecutionStartedAt: stamp(authenticated.record.ExecutionStart), ErasureEffectiveAt: stamp(authenticated.effectiveAt),
		ClosureVersion: authenticated.closureVersion, SyntheticFixture: nullableString(authenticated.record.SyntheticFixture),
		ReplayVersion: authenticated.record.Replay.Version, ActionVersion: authenticated.record.Replay.ActionVersion,
		Operations: append([]string(nil), authenticated.record.Replay.Operations...), PrescriptionSha256: bytes.Clone(authenticated.prescriptionSHA256),
		RecordSha256: bytes.Clone(authenticated.recordSHA256), MembershipPostconditionContract: postcondition.Contract,
		MembershipPostconditionSha256: nil, MembershipCount: int64(postcondition.MembershipCount), VariationCount: int64(postcondition.VariationCount),
	}
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dbgen.New(savepoint).ImportAuthenticatedPrivacyRestoreTombstoneV4Hardened(ctx, params); err == nil {
		_ = savepoint.Rollback(ctx)
		t.Fatal("NULL membership postcondition digest accepted by authenticated v4 import")
	}
	if err = savepoint.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}
