//go:build integration

package db

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPrivacyWorkerReleaseGuardForwardMigrationPreservesMembershipHistoryAndRejectsCorruptSource(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	const marker = "-- #248 release guard:"
	migration, err := migrationFiles.ReadFile("migrations/202609100013_privacy_worker_release_guard.sql")
	if err != nil {
		t.Fatal(err)
	}
	markerIndex := strings.LastIndex(baselineSchema, marker)
	if markerIndex < 0 {
		t.Fatal("release-guard migration marker missing from baseline")
	}
	nextMarker := strings.Index(baselineSchema[markerIndex:], "-- #248 trusted activation boundary")
	if nextMarker < 0 || baselineSchema[markerIndex:markerIndex+nextMarker] != string(migration) {
		t.Fatal("release-guard migration is not the exact baseline segment before activation broker hardening")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	schemaName := "privacy_248_guard_" + suffix
	protectedName := "privacy_248_guard_protected_" + suffix
	schema := pgx.Identifier{schemaName}.Sanitize()
	protected := pgx.Identifier{protectedName}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		_, _ = conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+protected+" CASCADE")
	}()
	if _, err = conn.Exec(ctx, "SET search_path TO "+schema+",public"); err != nil {
		t.Fatal(err)
	}
	rewrite := func(sql string) string {
		sql = strings.ReplaceAll(sql, "public.", schemaName+".")
		sql = strings.ReplaceAll(sql, "pg_catalog, public", "pg_catalog, "+schemaName+", public")
		sql = strings.ReplaceAll(sql, "pg_catalog,public", "pg_catalog,"+schemaName+",public")
		return strings.ReplaceAll(sql, "privacy_protected", protectedName)
	}
	if _, err = conn.PgConn().Exec(ctx, rewrite(baselineSchema[:markerIndex])).ReadAll(); err != nil {
		t.Fatalf("create exact 012 baseline: %v", err)
	}

	effective := time.Date(2026, time.June, 15, 12, 0, 0, 0, time.UTC)
	subject, actor := uuid.New(), uuid.New()
	for _, item := range []struct {
		id    uuid.UUID
		email string
	}{{subject, "guard-subject-" + suffix + "@example.test"}, {actor, "guard-actor-" + suffix + "@example.test"}} {
		if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth)
			VALUES($1,'Release guard fixture',$2,'hash','1990-01-01')`, item.id, item.email); err != nil {
			t.Fatal(err)
		}
	}
	programme := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO programmes(id,code,name_pt) VALUES($1,$2,'Programa teste')`, programme, "Guard_"+suffix[:12]); err != nil {
		t.Fatal(err)
	}
	seasonIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	for position, seasonID := range seasonIDs {
		if _, err = conn.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on)
			VALUES($1,$2,$3,$4,$5)`, seasonID, "G"+suffix[:8]+string(rune('A'+position)), "Guard season "+string(rune('A'+position)),
			effective.AddDate(position-3, 0, 0), effective.AddDate(position-2, 0, -1)); err != nil {
			t.Fatal(err)
		}
	}
	historicID, activeID, futureID := uuid.New(), uuid.New(), uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES
		($1,$4,$5,$8,$9::date-interval '2 years',$9::date-interval '1 year'),
		($2,$4,$6,$8,$9::date-interval '1 month',NULL),
		($3,$4,$7,$8,$9::date+interval '1 month',NULL)`, historicID, activeID, futureID, subject,
		seasonIDs[0], seasonIDs[1], seasonIDs[2], programme, effective); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.PgConn().Exec(ctx, rewrite(string(migration))).ReadAll(); err != nil {
		t.Fatalf("apply release guard forward migration: %v", err)
	}

	digest := bytes.Repeat([]byte{7}, 32)
	worker, importID := uuid.New(), uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO `+protected+`.restore_ledger_imports(
		id,kind,record_version,envelope_version,encryption_key_id,locator_key_id,locator_digest,ciphertext_sha256,object_version_id,
		written_at,verified_at,retain_until,source_execution_id,source_request_id,source_request_ref,subject_user_id,plan_sha256,workset_sha256,
		execution_started_at,replay_version,action_version,operations,prescription_sha256,record_sha256,imported_by_ref,
		erasure_effective_at,closure_version)
		VALUES($1,'closure','restore-tombstone/v2','x25519-aes256gcm-hkdfsha256/v2','key','locator',$2,$2,'version',
		$3::timestamptz,$3::timestamptz,$3::timestamptz+interval '1 year',$4,$5,$6,$7,$2,$2,$3::timestamptz-interval '1 day','relational-erasure-replay/v1','v1',
		ARRAY['MEMBERSHIP_ACTIVE_REVOKE','MEMBERSHIP_HISTORY_ANONYMIZE'],$2,$2,$8,$3,'restore-tombstone-closure/v3')`,
		importID, digest, effective, uuid.New(), uuid.New(), uuid.New(), subject, worker); err != nil {
		t.Fatal(err)
	}
	var runID uuid.UUID
	var outcome *string
	if err = conn.QueryRow(ctx, `SELECT run_id,outcome_code FROM privacy_restore_begin_replay_hardened($1,$2)`, importID, worker).Scan(&runID, &outcome); err != nil {
		t.Fatal(err)
	}
	if outcome != nil {
		t.Fatalf("new replay outcome=%v", *outcome)
	}
	for position, operation := range []string{"MEMBERSHIP_ACTIVE_REVOKE", "MEMBERSHIP_HISTORY_ANONYMIZE"} {
		if _, err = conn.Exec(ctx, `SELECT privacy_restore_execute_checkpoint($1,$2,$3,$4,'v1',$5)`, runID, worker, position+1, operation, digest); err != nil {
			t.Fatalf("execute %s: %v", operation, err)
		}
	}
	var preserved, future, linked, activeAfter int
	if err = conn.QueryRow(ctx, `SELECT
		count(*) FILTER(WHERE id IN($1,$2) AND user_id IS NULL AND principal_id IS NOT NULL),
		count(*) FILTER(WHERE id=$3),
		count(*) FILTER(WHERE user_id=$4),
		count(*) FILTER(WHERE id IN($1,$2) AND (starts_on>=$5::date OR ends_on IS NULL OR ends_on>=$5::date))
		FROM user_memberships`, historicID, activeID, futureID, subject, effective).Scan(&preserved, &future, &linked, &activeAfter); err != nil {
		t.Fatal(err)
	}
	if preserved != 2 || future != 0 || linked != 0 || activeAfter != 0 {
		t.Fatalf("membership replay preserved=%d future=%d linked=%d active_after=%d", preserved, future, linked, activeAfter)
	}

	requestID, requestRef, executionID := uuid.New(), uuid.New(), uuid.New()
	planDigest := bytes.Repeat([]byte{8}, 32)
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,adopted_at,adopted_by)
		 VALUES('guard-source-policy','[]','privacy-erasure-executor/v2','privacy-erasure-plan/v2',$1,$2)`, []any{effective, actor}},
		{`INSERT INTO data_erasure_requests(id,public_ref,idempotency_key,subject_user_id,requester_user_id,subject_kind,scope_kind,status,version,
		 received_at,due_at,decided_by,decided_at,decision_code,policy_version,policy_snapshot,updated_at)
		 VALUES($1,$2,$3,$4,$4,'SELF','ACCOUNT_CLOSURE','PROCESSING',2,$5::timestamptz-interval '1 day',$5::timestamptz+interval '1 day',$6,$5,'APPROVED','guard-source-policy','{}',$5)`, []any{requestID, requestRef, uuid.New(), subject, effective, actor}},
		{`INSERT INTO privacy_request_execution_plans(request_id,policy_version,executor_version,schema_version,plan,plan_sha256,created_at)
		 VALUES($1,'guard-source-policy','privacy-erasure-executor/v2','privacy-erasure-plan/v2',$2,$3,$4)`, []any{requestID, `{"policy_version":"guard-source-policy","executor_version":"privacy-erasure-executor/v2","schema_version":"privacy-erasure-plan/v2"}`, planDigest, effective}},
		{`INSERT INTO privacy_erasure_executions(id,request_id,plan_sha256,executor_version,schema_version,request_version_at_start,status,started_by_ref,
		 accepted_at,started_at,finished_at,updated_at) VALUES($1,$2,$3,'privacy-erasure-executor/v2','privacy-erasure-plan/v2',2,'SUCCEEDED',$4,$5,$5,$5,$5)`, []any{executionID, requestID, planDigest, actor, effective}},
	}
	for _, statement := range statements {
		if _, err = conn.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = conn.Exec(ctx, `UPDATE users SET name='Conta eliminada',email=NULL,email_verified_at=NULL,password_hash=NULL,
		date_of_birth='1900-01-01',is_active=false,leaderboard_visible=false,credential_version=credential_version+1,
		erased_at=$2,erasure_execution_id=$3,updated_at=$2 WHERE id=$1`, subject, effective, executionID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `ALTER TABLE user_memberships DISABLE TRIGGER user_memberships_active_subject_attachment`); err != nil {
		t.Fatal(err)
	}
	corruptID := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on)
		VALUES($1,$2,$3,$4,$5::date+interval '1 day')`, corruptID, subject, seasonIDs[3], programme, effective); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `ALTER TABLE user_memberships ENABLE TRIGGER user_memberships_active_subject_attachment`); err != nil {
		t.Fatal(err)
	}
	alreadyImport := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO `+protected+`.restore_ledger_imports(
		id,kind,record_version,envelope_version,encryption_key_id,locator_key_id,locator_digest,ciphertext_sha256,object_version_id,
		written_at,verified_at,retain_until,source_execution_id,source_request_id,source_request_ref,subject_user_id,plan_sha256,workset_sha256,
		execution_started_at,replay_version,action_version,operations,prescription_sha256,record_sha256,imported_by_ref,
		erasure_effective_at,closure_version)
		VALUES($1,'closure','restore-tombstone/v2','x25519-aes256gcm-hkdfsha256/v2','key','locator-two',$2,$2,'version-two',
		$3::timestamptz,$3::timestamptz,$3::timestamptz+interval '1 year',$4,$5,$6,$7,$2,$2,$3::timestamptz-interval '1 day','relational-erasure-replay/v1','v1',
		ARRAY['MEMBERSHIP_ACTIVE_REVOKE','MEMBERSHIP_HISTORY_ANONYMIZE'],$2,$2,$8,$3,'restore-tombstone-closure/v3')`,
		alreadyImport, digest, effective, executionID, requestID, requestRef, subject, worker); err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `SELECT * FROM privacy_restore_begin_replay_hardened($1,$2)`, alreadyImport, worker)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Message != "privacy_restore_verification_failed" {
		t.Fatalf("corrupted already-applied source accepted: %v", err)
	}
	var replayRows int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM `+protected+`.restore_replay_runs WHERE import_id=$1`, alreadyImport).Scan(&replayRows); err != nil {
		t.Fatal(err)
	}
	if replayRows != 0 {
		t.Fatalf("rejected already-applied source persisted %d replay rows", replayRows)
	}
}
