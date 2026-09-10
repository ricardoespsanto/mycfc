//go:build integration

package db

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPrivacyRestoreLedgerFencesDestructionAndRecordsClosure(t *testing.T) {
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

	now := time.Now().UTC().Add(-time.Hour)
	subject, actor, requestID, executionID, worker := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for id, email := range map[uuid.UUID]string{subject: "ledger-subject-" + uuid.NewString() + "@example.test", actor: "ledger-actor-" + uuid.NewString() + "@example.test"} {
		if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Ledger fixture',$2,'hash','1990-01-01')`, id, email); err != nil {
			t.Fatal(err)
		}
	}
	policy := "ledger-" + uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,working_retention_days,adopted_at,adopted_by)
		VALUES($1,'[]','executor-v1','schema-v1',90,$2,$3)`, policy, now, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO data_erasure_requests(id,public_ref,idempotency_key,subject_user_id,requester_user_id,subject_kind,scope_kind,categories,status,version,
		received_at,due_at,decided_by,decided_at,decision_code,policy_version,policy_snapshot,updated_at)
		VALUES($1,$2,$3,$4,$4,'SELF','CATEGORIES',ARRAY['identity-core'],'PROCESSING',2,$5::timestamptz,$5::timestamptz+interval '1 month',$6,$5::timestamptz,'APPROVED',$7,'{}',$5::timestamptz)`, requestID, uuid.New(), uuid.New(), subject, now, actor, policy); err != nil {
		t.Fatal(err)
	}
	planDigest := bytes.Repeat([]byte{0x41}, 32)
	plan := `{"policy_version":"` + policy + `","executor_version":"executor-v1","schema_version":"schema-v1","entries":[` +
		`{"category":"backup-tombstones","fallback":"BLOCK","action_version":"v1","operations":["BACKUP_TOMBSTONE_REPLAY"]},` +
		`{"category":"identity-core","fallback":"BLOCK","action_version":"v1","operations":["AUTH_TOKEN_DELETE"]}]}`
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_request_execution_plans(request_id,policy_version,executor_version,schema_version,plan,plan_sha256,created_at)
		VALUES($1,$2,'executor-v1','schema-v1',$3,$4,$5)`, requestID, policy, plan, planDigest, now); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_erasure_executions(id,request_id,plan_sha256,executor_version,schema_version,request_version_at_start,status,started_by_ref,accepted_at,started_at,updated_at)
		VALUES($1,$2,$3,'executor-v1','schema-v1',2,'RUNNING',$4,$5,$5,$5)`, executionID, requestID, planDigest, actor, now); err != nil {
		t.Fatal(err)
	}

	type work struct{ job, lease, attempt, checkpoint uuid.UUID }
	seedWork := func(position int16, category, operation string) work {
		t.Helper()
		row := work{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
		if _, e := tx.Exec(ctx, `INSERT INTO privacy_erasure_category_jobs(id,execution_id,plan_entry_position,entry_sha256,category_key,purpose_code,status,next_attempt_at,lease_epoch,attempt_count,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,'test-purpose','LEASED',$6::timestamptz,1,1,$6::timestamptz,$6::timestamptz)`, row.job, executionID, position, bytes.Repeat([]byte{byte(position)}, 32), category, now); e != nil {
			t.Fatal(e)
		}
		if _, e := tx.Exec(ctx, `INSERT INTO privacy_erasure_job_leases(id,job_id,epoch,worker_ref,acquired_at,heartbeat_at,expires_at) VALUES($1,$2,1,$3,$4::timestamptz,$4::timestamptz,$4::timestamptz+interval '1 day')`, row.lease, row.job, worker, now); e != nil {
			t.Fatal(e)
		}
		if _, e := tx.Exec(ctx, `INSERT INTO privacy_erasure_job_attempts(id,job_id,lease_id,lease_epoch,attempt_number,started_at) VALUES($1,$2,$3,1,1,$4)`, row.attempt, row.job, row.lease, now); e != nil {
			t.Fatal(e)
		}
		if _, e := tx.Exec(ctx, `INSERT INTO privacy_erasure_job_checkpoints(id,job_id,operation_position,operation_code,action_version,created_at) VALUES($1,$2,1,$3,'v1',$4)`, row.checkpoint, row.job, operation, now); e != nil {
			t.Fatal(e)
		}
		return row
	}
	tombstone := seedWork(1, "backup-tombstones", "BACKUP_TOMBSTONE_REPLAY")
	destructive := seedWork(2, "identity-core", "AUTH_TOKEN_DELETE")

	if _, err = tx.Exec(ctx, `SAVEPOINT missing_tombstone`); err != nil {
		t.Fatal(err)
	}
	var ignored uuid.UUID
	err = tx.QueryRow(ctx, `SELECT privacy_worker_execute_checkpoint($1,$2,$3,1,$4,'AUTH_TOKEN_DELETE','v1')`, destructive.job, destructive.lease, destructive.attempt, worker).Scan(&ignored)
	if err == nil || !strings.Contains(err.Error(), "privacy_restore_tombstone_required") {
		t.Fatalf("destruction without independent receipt error=%v", err)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT missing_tombstone`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT legacy_tombstone`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.restore_tombstone_receipts(
		execution_id,request_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
		VALUES($1,$2,'restore-tombstone/v1','legacy-encrypt','legacy-locator',$3,'legacy-version',$4,512,$5,$5)`,
		executionID, requestID, bytes.Repeat([]byte{0x31}, 32), bytes.Repeat([]byte{0x32}, 32), now); err != nil {
		t.Fatal(err)
	}
	err = tx.QueryRow(ctx, `SELECT privacy_worker_execute_checkpoint($1,$2,$3,1,$4,'AUTH_TOKEN_DELETE','v1')`, destructive.job, destructive.lease, destructive.attempt, worker).Scan(&ignored)
	if err == nil || !strings.Contains(err.Error(), "privacy_restore_tombstone_required") {
		t.Fatalf("legacy non-replayable receipt authorized destruction error=%v", err)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT legacy_tombstone`); err != nil {
		t.Fatal(err)
	}

	var preparedExecution, preparedRequest, requestRef, preparedSubject uuid.UUID
	var preparedPlan, workset []byte
	var executionStarted time.Time
	var replayOperations []string
	if err = tx.QueryRow(ctx, `SELECT * FROM privacy_tombstone_prepare_v2($1,$2,$3,1,$4)`, tombstone.job, tombstone.lease, tombstone.attempt, worker).
		Scan(&preparedExecution, &preparedRequest, &requestRef, &preparedSubject, &preparedPlan, &workset, &executionStarted, &replayOperations); err != nil {
		t.Fatal(err)
	}
	if preparedExecution != executionID || preparedRequest != requestID || preparedSubject != subject || requestRef == uuid.Nil || !bytes.Equal(preparedPlan, planDigest) || len(workset) != 32 || len(replayOperations) != 1 || replayOperations[0] != "AUTH_TOKEN_DELETE" {
		t.Fatalf("unexpected tombstone context execution=%s request=%s subject=%s workset=%x", preparedExecution, preparedRequest, preparedSubject, workset)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT stale_lease`); err != nil {
		t.Fatal(err)
	}
	err = tx.QueryRow(ctx, `SELECT * FROM privacy_tombstone_prepare_v2($1,$2,$3,2,$4)`, tombstone.job, tombstone.lease, tombstone.attempt, worker).
		Scan(&preparedExecution, &preparedRequest, &requestRef, &preparedSubject, &preparedPlan, &workset, &executionStarted, &replayOperations)
	if err != pgx.ErrNoRows {
		t.Fatalf("stale lease error=%v", err)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT stale_lease`); err != nil {
		t.Fatal(err)
	}

	writtenAt, verifiedAt := now.Add(5*time.Minute), now.Add(6*time.Minute)
	locator, ciphertext := bytes.Repeat([]byte{0x55}, 32), bytes.Repeat([]byte{0x66}, 32)
	if err = tx.QueryRow(ctx, `SELECT privacy_tombstone_confirm_v2($1,$2,$3,1,$4,'restore-tombstone/v2','encrypt-v2','locator-v2',$5,'object-version-1',$6,512,$7,$8)`,
		tombstone.job, tombstone.lease, tombstone.attempt, worker, locator, ciphertext, writtenAt, verifiedAt).Scan(&ignored); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT immutable_receipt`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE privacy_protected.restore_tombstone_receipts SET object_version_id='replaced' WHERE execution_id=$1`, executionID); err == nil {
		t.Fatal("restore receipt was mutable")
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT immutable_receipt`); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT privacy_worker_complete_job($1,$2,$3,1,$4)`, tombstone.job, tombstone.lease, tombstone.attempt, worker).Scan(&ignored); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT privacy_worker_execute_checkpoint($1,$2,$3,1,$4,'AUTH_TOKEN_DELETE','v1')`, destructive.job, destructive.lease, destructive.attempt, worker).Scan(&ignored); err != nil {
		t.Fatalf("destruction remained blocked after receipt: %v", err)
	}
	if err = tx.QueryRow(ctx, `SELECT privacy_worker_complete_job($1,$2,$3,1,$4)`, destructive.job, destructive.lease, destructive.attempt, worker).Scan(&ignored); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE privacy_erasure_executions SET status='SUCCEEDED',finished_at=$2,updated_at=$2 WHERE id=$1`, executionID, verifiedAt); err != nil {
		t.Fatal(err)
	}

	var closedAt, expiresAt time.Time
	if err = tx.QueryRow(ctx, `SELECT closed_at,evidence_expires_at FROM privacy_tombstone_prepare_closure_v2($1,$2)`, executionID, worker).Scan(&closedAt, &expiresAt); err != nil {
		t.Fatal(err)
	}
	if !expiresAt.Equal(closedAt.AddDate(0, 24, 0)) {
		t.Fatalf("closure expiry=%s want=%s", expiresAt, closedAt.AddDate(0, 24, 0))
	}
	closureLocator := bytes.Repeat([]byte{0x77}, 32)
	if err = tx.QueryRow(ctx, `SELECT privacy_tombstone_confirm_closure_v2($1,$2,'restore-tombstone-closure/v2','encrypt-v2','locator-v2',$3,'closure-version-1',$4,768,$5,$6)`,
		executionID, worker, closureLocator, ciphertext, closedAt, closedAt.Add(time.Second)).Scan(&ignored); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT closure_conflict`); err != nil {
		t.Fatal(err)
	}
	err = tx.QueryRow(ctx, `SELECT privacy_tombstone_confirm_closure_v2($1,$2,'restore-tombstone-closure/v2','encrypt-v2','locator-v2',$3,'different-version',$4,768,$5,$6)`,
		executionID, worker, closureLocator, ciphertext, closedAt, closedAt.Add(time.Second)).Scan(&ignored)
	if err == nil || !strings.Contains(err.Error(), "privacy_tombstone_closure_receipt_conflict") {
		t.Fatalf("closure overwrite error=%v", err)
	}
}

func TestPrivacyRetentionRunIsBoundedAndPreservesUnresolvedRows(t *testing.T) {
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

	now := time.Now().UTC().Truncate(time.Microsecond)
	member, other, actor := uuid.New(), uuid.New(), uuid.New()
	for id, email := range map[uuid.UUID]string{
		member: "retention-member-" + uuid.NewString() + "@example.test",
		other:  "retention-other-" + uuid.NewString() + "@example.test",
		actor:  "retention-actor-" + uuid.NewString() + "@example.test",
	} {
		if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Retention fixture',$2,'hash','1990-01-01')`, id, email); err != nil {
			t.Fatal(err)
		}
	}
	expiredSession, activeSession := "expired-"+uuid.NewString(), "active-"+uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($1,'x',$3,$2,true),($4,'x',$5,$2,true)`,
		expiredSession, member, now.Add(-time.Hour), activeSession, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	oldReset, currentVerification := uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO password_reset_tokens(id,user_id,email,token_digest,created_at,expires_at,consumed_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, oldReset, member, "retention-member@example.test", bytes.Repeat([]byte{0x88}, 32), now.AddDate(0, 0, -100), now.AddDate(0, 0, -99), now.AddDate(0, 0, -98)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO email_verification_tokens(id,user_id,email,created_at,expires_at) VALUES($1,$2,$3,$4,$5)`,
		currentVerification, other, "retention-other@example.test", now.AddDate(0, 0, -8), now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	archivedOutbox, stoppedOutbox := uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO email_outbox(id,message_type,password_reset_token_id,sealed_payload,status,attempts,next_attempt_at,sent_at,created_at,updated_at)
		VALUES($1,'PASSWORD_RESET',$2,'encrypted','SENT',1,$3,$4,$5,$4)`, archivedOutbox, oldReset, now.AddDate(0, 0, -60), now.AddDate(0, 0, -59), now.AddDate(0, 0, -60)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO email_outbox(id,message_type,verification_token_id,status,next_attempt_at,created_at,updated_at)
		VALUES($1,'EMAIL_VERIFICATION',$2,'PENDING',$3,$3,$3)`, stoppedOutbox, currentVerification, now.AddDate(0, 0, -8)); err != nil {
		t.Fatal(err)
	}

	oldConsent, currentConsent := uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO consent_forms(id,user_id,consent_type,document_version,document_sha256,is_accepted,date_signed,ip_address,user_agent)
		VALUES($1,$3,'Termos_Gerais','v1',$4,true,$5,'192.0.2.10','old-agent'),($2,$3,'Uso_Imagem','v1',$4,true,$6,'192.0.2.11','new-agent')`,
		oldConsent, currentConsent, member, strings.Repeat("a", 64), now.AddDate(-1, 0, -1), now.AddDate(0, -11, 0)); err != nil {
		t.Fatal(err)
	}

	oldEvent, currentEvent := uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO events(id,title,starts_at,ends_at,created_by_id) VALUES($1,'Old event',$3,$4,$2),($5,'Current event',$6,$7,$2)`,
		oldEvent, actor, now.AddDate(0, 0, -101), now.AddDate(0, 0, -100), currentEvent, now.AddDate(0, 0, -2), now.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO event_responses(event_id,user_id,status,responded_by_id,responded_at) VALUES($1,$3,'Going',$3,$4),($2,$3,'Going',$3,$4)`,
		oldEvent, currentEvent, member, now.AddDate(0, 0, -102)); err != nil {
		t.Fatal(err)
	}

	announcement := uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO announcements(id,title,body,author_id) VALUES($1,'Retention news','Retention body',$2)`, announcement, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO announcement_deliveries(announcement_id,user_id,delivered_at) VALUES($1,$2,$4),($1,$3,$5)`,
		announcement, member, other, now.AddDate(0, 0, -91), now.AddDate(0, 0, -89)); err != nil {
		t.Fatal(err)
	}

	oldSuggestion, unresolvedSuggestion := uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO suggestions(id,requester_id,category,subject,description,status,staff_response,responded_by_id,responded_at,created_at,updated_at)
		VALUES($1,$3,'OTHER','Old terminal suggestion','Long enough description','DECLINED','Reviewed response',$4,$5,$5,$5),
		($2,$3,'OTHER','Unresolved suggestion','Long enough description','SUBMITTED',NULL,NULL,NULL,$5,$5)`,
		oldSuggestion, unresolvedSuggestion, member, actor, now.AddDate(-1, 0, -1)); err != nil {
		t.Fatal(err)
	}

	policy := "retention-" + uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,working_retention_days,adopted_at,adopted_by) VALUES($1,'[]',90,$2,$3)`, policy, now.AddDate(0, 0, -110), actor); err != nil {
		t.Fatal(err)
	}
	requestID, publicRef, evidenceExpiry := uuid.New(), uuid.New(), now.AddDate(1, 11, 0)
	closedAt := now.AddDate(0, 0, -100)
	if _, err = tx.Exec(ctx, `INSERT INTO data_erasure_requests(id,public_ref,idempotency_key,subject_user_id,requester_user_id,subject_kind,scope_kind,categories,status,version,
		received_at,due_at,decided_by,decided_at,decision_code,decision_explanation,category_decisions,policy_version,policy_snapshot,closed_at,evidence_expires_at,working_expires_at,updated_at)
		VALUES($1,$2,$3,$4,$4,'SELF','CATEGORIES',ARRAY['identity-core'],'REFUSED',2,$5,$6,$7,$8,'REFUSED','identifying explanation','[]',$9,'{}',$8,$10,$11,$8)`,
		requestID, publicRef, uuid.New(), member, closedAt.AddDate(0, 0, -5), closedAt.AddDate(0, 1, 0), actor, closedAt, policy, evidenceExpiry, now.AddDate(0, 0, -10)); err != nil {
		t.Fatal(err)
	}

	type result struct {
		runID                                                                                                                                      uuid.UUID
		sessions, tokens, stopped, payloads, evidence, consent, consentEvidence, audit, repairs, events, announcements, suggestions, working, auth int32
	}
	var got result
	if err = tx.QueryRow(ctx, `SELECT * FROM privacy_retention_run($1,100)`, uuid.New()).Scan(
		&got.runID, &got.sessions, &got.tokens, &got.stopped, &got.payloads, &got.evidence, &got.consent,
		&got.consentEvidence, &got.audit, &got.repairs, &got.events, &got.announcements, &got.suggestions, &got.working, &got.auth); err != nil {
		t.Fatal(err)
	}
	if got.runID == uuid.Nil || got.sessions != 1 || got.tokens != 1 || got.stopped != 1 || got.payloads != 1 || got.consent != 1 ||
		got.events != 1 || got.announcements != 1 || got.suggestions != 1 || got.working != 1 {
		t.Fatalf("unexpected retention counts: %+v", got)
	}

	var expiredExists, activeExists, archiveExists, stoppedFailed, unresolvedExists, requestExists bool
	var oldIP *string
	var oldAgent, currentAgent, explanation string
	var scrubbedAt *time.Time
	var persistedEvidenceExpiry time.Time
	if err = tx.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM sessions WHERE token=$1),EXISTS(SELECT 1 FROM sessions WHERE token=$2),
		EXISTS(SELECT 1 FROM privacy_outbox_delivery_evidence WHERE outbox_id=$3),
		EXISTS(SELECT 1 FROM email_outbox WHERE id=$4 AND status='FAILED'),
		EXISTS(SELECT 1 FROM suggestions WHERE id=$5),EXISTS(SELECT 1 FROM data_erasure_requests WHERE id=$6)`,
		expiredSession, activeSession, archivedOutbox, stoppedOutbox, unresolvedSuggestion, requestID).
		Scan(&expiredExists, &activeExists, &archiveExists, &stoppedFailed, &unresolvedExists, &requestExists); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT ip_address::text,user_agent FROM consent_forms WHERE id=$1`, oldConsent).Scan(&oldIP, &oldAgent); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT user_agent FROM consent_forms WHERE id=$1`, currentConsent).Scan(&currentAgent); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT decision_explanation,working_erased_at,evidence_expires_at FROM data_erasure_requests WHERE id=$1`, requestID).
		Scan(&explanation, &scrubbedAt, &persistedEvidenceExpiry); err != nil {
		t.Fatal(err)
	}
	if expiredExists || !activeExists || !archiveExists || !stoppedFailed || !unresolvedExists || !requestExists || oldIP != nil || oldAgent != "" || currentAgent != "new-agent" ||
		explanation != "" || scrubbedAt == nil || !persistedEvidenceExpiry.Equal(evidenceExpiry) {
		t.Fatalf("retention postcondition expired=%t active=%t archive=%t stopped=%t unresolved=%t request=%t oldIP=%v oldAgent=%q currentAgent=%q explanation=%q scrubbed=%v evidence=%s",
			expiredExists, activeExists, archiveExists, stoppedFailed, unresolvedExists, requestExists, oldIP, oldAgent, currentAgent, explanation, scrubbedAt, persistedEvidenceExpiry)
	}
}

func TestPrivacyRestoreAndRetentionForwardMigrationsAreAdditive(t *testing.T) {
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
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	schemaName, protectedName := "privacy_247_migration_"+suffix, "privacy_247_protected_"+suffix
	schema, protected := pgx.Identifier{schemaName}.Sanitize(), pgx.Identifier{protectedName}.Sanitize()
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
	hardeningMarker := strings.LastIndex(baselineSchema, "-- #247 replay hardening:")
	if hardeningMarker < 0 {
		t.Fatal("replay hardening marker missing from baseline")
	}
	if _, err = conn.PgConn().Exec(ctx, rewrite(baselineSchema[:hardeningMarker])).ReadAll(); err != nil {
		t.Fatalf("create isolated baseline: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		DROP FUNCTION privacy_restore_execute_checkpoint(uuid,uuid,smallint,text,text,bytea);
		DROP FUNCTION privacy_restore_apply_relational_operation(uuid,uuid,timestamptz,text);
		DROP FUNCTION privacy_restore_begin_replay(uuid,uuid);
		DROP FUNCTION privacy_restore_import_authenticated_v2(uuid,text,text,text,text,text,bytea,bytea,text,timestamptz,timestamptz,timestamptz,uuid,uuid,uuid,uuid,bytea,bytea,timestamptz,text,text,text[],bytea,bytea);
		DROP FUNCTION privacy_tombstone_confirm_closure_v2(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz);
		DROP FUNCTION privacy_tombstone_prepare_closure_v2(uuid,uuid);
		DROP FUNCTION privacy_tombstone_confirm_v2(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz);
		DROP FUNCTION privacy_tombstone_prepare_v2(uuid,uuid,uuid,bigint,uuid);
		DROP FUNCTION privacy_relational_replay_operation_supported(text);
		CREATE OR REPLACE FUNCTION audit_staff_grant_change() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
		 IF TG_OP='INSERT' THEN INSERT INTO staff_grant_audit_events(staff_grant_id,action,actor_user_id) VALUES(NEW.id,'GRANTED',NEW.granted_by_id); RETURN NEW; END IF;
		 IF OLD.user_id<>NEW.user_id OR OLD.capability<>NEW.capability OR OLD.programme_id IS DISTINCT FROM NEW.programme_id
		  OR OLD.team_id IS DISTINCT FROM NEW.team_id OR OLD.granted_by_id<>NEW.granted_by_id OR OLD.granted_at<>NEW.granted_at
		  OR OLD.revoked_at IS NOT NULL OR NEW.revoked_at IS NULL THEN RAISE EXCEPTION 'staff grants are immutable except for one revocation'; END IF;
		 INSERT INTO staff_grant_audit_events(staff_grant_id,action,actor_user_id,occurred_at,reason)
		 VALUES(NEW.id,'REVOKED',NEW.revoked_by_id,NEW.revoked_at,NEW.revoke_reason); RETURN NEW;
		END; $$;
		ALTER TABLE staff_grant_audit_events DROP CONSTRAINT staff_grant_audit_actor_principal_exactly_one,
		 DROP COLUMN actor_replay_run_id,
		 ADD CONSTRAINT staff_grant_audit_actor_principal_exactly_one CHECK(num_nonnulls(actor_user_id,actor_principal_id)=1);
		ALTER TABLE staff_grants DROP CONSTRAINT staff_grants_revocation_valid,
		 DROP COLUMN revoked_by_replay_run_id,
		 ADD CONSTRAINT staff_grants_revocation_valid CHECK(
		  (revoked_at IS NULL AND revoked_by_id IS NULL AND revoke_reason IS NULL)
		  OR (revoked_at IS NOT NULL AND revoked_by_id IS NOT NULL AND revoke_reason=btrim(revoke_reason) AND char_length(revoke_reason) BETWEEN 1 AND 500));
		ALTER TABLE privacy_reviewer_grants DROP CONSTRAINT privacy_reviewer_grants_revocation_actor_exactly_one,
		 DROP COLUMN revoked_by_replay_run_id,
		 ADD CONSTRAINT privacy_reviewer_grants_check CHECK((revoked_at IS NULL)=(revoked_by IS NULL));
		ALTER TABLE privacy_executor_grants DROP CONSTRAINT privacy_executor_grants_revocation_actor_exactly_one,
		 DROP COLUMN revoked_by_replay_run_id,
		 ADD CONSTRAINT privacy_executor_grants_check CHECK((revoked_at IS NULL)=(revoked_by IS NULL));
		ALTER TABLE users DROP CONSTRAINT users_identity_shape;
		ALTER TABLE users DROP COLUMN erasure_replay_run_id;
		ALTER TABLE users ADD CONSTRAINT users_identity_shape CHECK (
		 (erased_at IS NULL AND erasure_execution_id IS NULL AND (
		   (is_dependent AND guardian_id IS NOT NULL AND email IS NULL AND
		    ((minor_login_id IS NULL AND password_hash IS NULL) OR (minor_login_id IS NOT NULL AND password_hash IS NOT NULL)))
		   OR
		   (NOT is_dependent AND guardian_id IS NULL AND email IS NOT NULL AND password_hash IS NOT NULL AND minor_login_id IS NULL)
		 ))
		 OR
		 (erased_at IS NOT NULL AND erasure_execution_id IS NOT NULL AND NOT is_active
		  AND NOT leaderboard_visible AND NOT is_dependent AND guardian_id IS NULL
		  AND email IS NULL AND email_verified_at IS NULL AND minor_login_id IS NULL
		  AND password_hash IS NULL AND name='Conta eliminada' AND date_of_birth=DATE '1900-01-01')
		);
		DROP TABLE `+protected+`.restore_replay_checkpoints,`+protected+`.restore_replay_runs,`+protected+`.restore_ledger_imports;
		ALTER TABLE `+protected+`.restore_tombstone_receipts
		 DROP CONSTRAINT restore_tombstone_receipts_ledger_version_check,
		 ADD CONSTRAINT restore_tombstone_receipts_ledger_version_check CHECK(ledger_version='restore-tombstone/v1');
		ALTER TABLE `+protected+`.restore_tombstone_closure_receipts
		 DROP CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check,
		 ADD CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check CHECK(ledger_version='restore-tombstone-closure/v1');
		DROP FUNCTION privacy_retention_run(uuid,integer);
		DROP TABLE privacy_outbox_delivery_evidence,privacy_retention_runs;
		DROP INDEX email_verification_tokens_retention_idx,password_reset_tokens_retention_idx,event_responses_retention_idx,announcement_deliveries_retention_idx,suggestions_retention_idx;
		DROP FUNCTION privacy_tombstone_confirm_closure(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz);
		DROP FUNCTION privacy_tombstone_prepare_closure(uuid,uuid);
		DROP FUNCTION privacy_tombstone_confirm(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz);
		DROP FUNCTION privacy_tombstone_prepare(uuid,uuid,uuid,bigint,uuid);
		DROP FUNCTION privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text);
		ALTER FUNCTION privacy_worker_execute_checkpoint_without_tombstone_guard(uuid,uuid,uuid,bigint,uuid,text,text) RENAME TO privacy_worker_execute_checkpoint;
		DROP TABLE `+protected+`.restore_tombstone_closure_receipts,`+protected+`.restore_tombstone_closure_intents,`+protected+`.restore_tombstone_receipts`); err != nil {
		t.Fatalf("reconstruct pre-#247 schema: %v", err)
	}
	userID := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Pre-247 row',$2,'hash','1990-01-01')`, userID, "pre-247-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES('pre-247-session','x',clock_timestamp()+interval '1 day',$1,true)`, userID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"migrations/202609100006_privacy_restore_ledger.sql", "migrations/202609100007_retention_maintenance.sql", "migrations/202609100008_privacy_tombstone_replay.sql"} {
		migration, readErr := migrationFiles.ReadFile(name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = conn.Exec(ctx, rewrite(string(migration))); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	var userPreserved, sessionPreserved, receiptTable, retentionTable, importTable, replayColumn, replayAPI, guardedWorker, originalWorker bool
	if err = conn.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM users WHERE id=$1),EXISTS(SELECT 1 FROM sessions WHERE token='pre-247-session'),
		to_regclass($2) IS NOT NULL,to_regclass('privacy_retention_runs') IS NOT NULL,to_regclass($3) IS NOT NULL,
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='users' AND column_name='erasure_replay_run_id'),
		to_regprocedure('privacy_restore_import_authenticated_v2(uuid,text,text,text,text,text,bytea,bytea,text,timestamptz,timestamptz,timestamptz,uuid,uuid,uuid,uuid,bytea,bytea,timestamptz,text,text,text[],bytea,bytea)') IS NOT NULL,
		to_regprocedure('privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text)') IS NOT NULL,
		to_regprocedure('privacy_worker_execute_checkpoint_without_tombstone_guard(uuid,uuid,uuid,bigint,uuid,text,text)') IS NOT NULL`,
		userID, protectedName+".restore_tombstone_receipts", protectedName+".restore_ledger_imports").
		Scan(&userPreserved, &sessionPreserved, &receiptTable, &retentionTable, &importTable, &replayColumn, &replayAPI, &guardedWorker, &originalWorker); err != nil {
		t.Fatal(err)
	}
	if !userPreserved || !sessionPreserved || !receiptTable || !retentionTable || !importTable || !replayColumn || !replayAPI || !guardedWorker || !originalWorker {
		t.Fatalf("migration additive user=%t session=%t receipt=%t retention=%t imports=%t replay_column=%t replay_api=%t guard=%t original=%t",
			userPreserved, sessionPreserved, receiptTable, retentionTable, importTable, replayColumn, replayAPI, guardedWorker, originalWorker)
	}
}
