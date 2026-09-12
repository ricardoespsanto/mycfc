//go:build integration

package privacyrequests

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCompletionRequeueAndActivationControls(t *testing.T) {
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
	expectDatabaseError := func(query string, args ...any) {
		t.Helper()
		savepoint, beginErr := tx.Begin(ctx)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if _, execErr := savepoint.Exec(ctx, query, args...); execErr == nil {
			_ = savepoint.Rollback(ctx)
			t.Fatal("database operation unexpectedly succeeded")
		}
		if rollbackErr := savepoint.Rollback(ctx); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	adminA, adminB, executor, subject := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{adminA, adminB, executor, subject} {
		email := id.String() + "@example.test"
		if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Completion fixture',$2,'hash','1990-01-01')`, id, email); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{adminA, adminB, executor} {
		if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_executor_grants(user_id,granted_by,granted_at) VALUES($1,$2,$3)`, executor, adminA, now); err != nil {
		t.Fatal(err)
	}
	policy := "completion-" + uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,working_retention_days,adopted_at,adopted_by)
		VALUES($1,'[]','privacy-erasure-executor/v2','privacy-erasure-plan/v2',90,$2,$3)`, policy, now, adminA); err != nil {
		t.Fatal(err)
	}
	activatePrivacyIntegrationFixture(t, ctx, tx, adminB, executor, policy)

	t.Run("finalization-is-atomic-and-link-is-one-use", func(t *testing.T) {
		requestID, requestRef, executionID, jobID, attemptID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
		executionAt := now.Add(-25 * time.Hour)
		planDigest, entryDigest, resultDigest := sha256.Sum256([]byte("plan"+executionID.String())), sha256.Sum256([]byte("entry")), sha256.Sum256([]byte("result"))
		plan := `{"policy_version":"` + policy + `","executor_version":"privacy-erasure-executor/v2","schema_version":"privacy-erasure-plan/v2","entries":[{"category":"backup-tombstones","purpose":"RESTORE_SAFETY","operations":["BACKUP_TOMBSTONE_REPLAY"],"action_version":"v1"},{"category":"core-identity","purpose":"IDENTITY","operations":["IDENTITY_CLEAR"],"action_version":"v1"}]}`
		if _, err = tx.Exec(ctx, `INSERT INTO data_erasure_requests(id,public_ref,idempotency_key,subject_user_id,requester_user_id,subject_kind,scope_kind,categories,status,version,received_at,due_at,
			decision_code,decided_by,decided_at,policy_version,policy_snapshot,updated_at)
			VALUES($1,$2,$3,$4,$4,'SELF','CATEGORIES',ARRAY['backup-tombstones','core-identity'],'PROCESSING',3,$5::timestamptz-interval '2 days',$5::timestamptz+interval '28 days','APPROVED',$6,$5,$7,'{}',$5)`,
			requestID, requestRef, uuid.New(), subject, now, adminA, policy); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_request_execution_plans VALUES($1,$2,'privacy-erasure-executor/v2','privacy-erasure-plan/v2',$3,$4,$5)`, requestID, policy, plan, planDigest[:], now); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_erasure_executions(id,request_id,plan_sha256,executor_version,schema_version,request_version_at_start,status,version,started_by_ref,accepted_at,started_at,finished_at,updated_at)
			VALUES($1,$2,$3,'privacy-erasure-executor/v2','privacy-erasure-plan/v2',3,'SUCCEEDED',4,$4,$5,$5,$5,$5)`, executionID, requestID, planDigest[:], executor, executionAt); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_erasure_category_jobs(id,execution_id,plan_entry_position,entry_sha256,category_key,purpose_code,status,next_attempt_at,lease_epoch,attempt_count,created_at,updated_at,completed_at)
			VALUES($1,$2,1,$3,'backup-tombstones','RESTORE_SAFETY','SUCCEEDED',$4,1,1,$4,$4,$4)`, jobID, executionID, entryDigest[:], executionAt); err != nil {
			t.Fatal(err)
		}
		leaseID := uuid.New()
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_erasure_job_leases(id,job_id,epoch,worker_ref,acquired_at,heartbeat_at,expires_at,released_at,outcome)
			VALUES($1,$2,1,$3,$4,$4,$4::timestamptz+interval '5 minutes',$4,'SUCCEEDED')`, leaseID, jobID, executor, executionAt); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_erasure_job_attempts(id,job_id,lease_id,lease_epoch,attempt_number,started_at,finished_at,outcome)
			VALUES($1,$2,$3,1,1,$4,$4,'SUCCEEDED')`, attemptID, jobID, leaseID, executionAt); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_erasure_job_checkpoints(job_id,operation_position,operation_code,action_version,status,completed_by_attempt_id,created_at,completed_at,affected_rows,result_sha256)
			VALUES($1,1,'BACKUP_TOMBSTONE_REPLAY','v1','SUCCEEDED',$2,$3,$3,1,$4)`, jobID, attemptID, executionAt, resultDigest[:]); err != nil {
			t.Fatal(err)
		}
		identityJobID, identityAttemptID := uuid.New(), uuid.New()
		identityEntryDigest := sha256.Sum256([]byte("identity-entry" + executionID.String()))
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_erasure_category_jobs(id,execution_id,plan_entry_position,entry_sha256,category_key,purpose_code,status,next_attempt_at,lease_epoch,attempt_count,created_at,updated_at,completed_at)
			VALUES($1,$2,2,$3,'core-identity','IDENTITY','SUCCEEDED',$4,1,1,$4,$4,$4)`, identityJobID, executionID, identityEntryDigest[:], executionAt); err != nil {
			t.Fatal(err)
		}
		identityLeaseID := uuid.New()
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_erasure_job_leases(id,job_id,epoch,worker_ref,acquired_at,heartbeat_at,expires_at,released_at,outcome)
			VALUES($1,$2,1,$3,$4,$4,$4::timestamptz+interval '5 minutes',$4,'SUCCEEDED')`, identityLeaseID, identityJobID, executor, executionAt); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_erasure_job_attempts(id,job_id,lease_id,lease_epoch,attempt_number,started_at,finished_at,outcome)
			VALUES($1,$2,$3,1,1,$4,$4,'SUCCEEDED')`, identityAttemptID, identityJobID, identityLeaseID, executionAt); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_erasure_job_checkpoints(job_id,operation_position,operation_code,action_version,status,completed_by_attempt_id,created_at,completed_at,affected_rows,result_sha256)
			VALUES($1,1,'IDENTITY_CLEAR','v1','SUCCEEDED',$2,$3,$3,1,$4)`, identityJobID, identityAttemptID, executionAt, resultDigest[:]); err != nil {
			t.Fatal(err)
		}
		locator, ciphertext := sha256.Sum256([]byte("locator"+executionID.String())), sha256.Sum256([]byte("ciphertext"+executionID.String()))
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.restore_tombstone_receipts(execution_id,request_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
			VALUES($1,$2,'restore-tombstone/v2','enc-v2','loc-v2',$3,'version-intent',$4,512,$5,$5)`, executionID, requestID, locator[:], ciphertext[:], now); err != nil {
			t.Fatal(err)
		}
		closedAt := now.Add(-24 * time.Hour)
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.restore_tombstone_closure_intents(execution_id,closed_at,evidence_expires_at) VALUES($1,$2,$2::timestamptz+interval '24 months')`, executionID, closedAt); err != nil {
			t.Fatal(err)
		}
		programmeID, seasonID, membershipID := uuid.New(), uuid.New(), uuid.New()
		codeSuffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
		if _, err = tx.Exec(ctx, `INSERT INTO programmes(id,code,name_pt) VALUES($1,$2,'Programa conclusão')`, programmeID, "Completion"+codeSuffix); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Época conclusão',$3::date-2,$3::date-1)`, seasonID, "C"+codeSuffix, now); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on)
			VALUES($1,$2,$3,$4,$5::date-2,$5::date-1)`, membershipID, subject, seasonID, programmeID, executionAt); err != nil {
			t.Fatal(err)
		}
		principalID := uuid.New()
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_pseudonymous_principals(id,purpose) VALUES($1,'MEMBERSHIP')`, principalID); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE user_memberships SET user_id=NULL,principal_id=$2 WHERE id=$1`, membershipID, principalID); err != nil {
			t.Fatal(err)
		}
		erasedAt := executionAt.Add(30 * time.Minute)
		if _, err = tx.Exec(ctx, `UPDATE users SET name='Conta eliminada',email=NULL,email_verified_at=NULL,password_hash=NULL,
			date_of_birth='1900-01-01',is_active=false,leaderboard_visible=false,credential_version=credential_version+1,
			erased_at=$2,erasure_execution_id=$3,updated_at=$2 WHERE id=$1`, subject, erasedAt, executionID); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.membership_history_source_captures(execution_id) VALUES($1)`, executionID); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.membership_history_source_rows(execution_id,membership_id) VALUES($1,$2)`, executionID, membershipID); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.membership_history_source_postconditions(
			 execution_id,effective_at,contract,postcondition_sha256,membership_count,variation_count,canonical_size)
			SELECT $1,$2,computed.contract,computed.postcondition_sha256,computed.membership_count,computed.variation_count,computed.canonical_size
			FROM privacy_membership_history_compute_source($1,$2) computed`, executionID, erasedAt); err != nil {
			t.Fatal(err)
		}
		expectDatabaseError(`UPDATE user_memberships SET ends_on=ends_on-1 WHERE id=$1`, membershipID)
		closureProtector, _ := tombstoneProtectorFixture(t)
		closureLedger := &capturingTombstoneLedger{}
		if err = (TombstoneExportWorker{Store: tx, Ledger: closureLedger, Protector: closureProtector, WorkerRef: executor}).ExportClosure(ctx, executionID); err != nil {
			t.Fatal(err)
		}
		if len(closureLedger.sealed) != 1 || closureLedger.sealed[0].Kind != "closure" || !closureLedger.sealed[0].RetainUntil.Equal(closedAt.AddDate(0, 24, 0)) {
			t.Fatalf("closure export=%+v", closureLedger.sealed)
		}
		expectDatabaseError(`UPDATE user_memberships SET updated_at=clock_timestamp() WHERE id=$1`, membershipID)
		key := bytes.Repeat([]byte{8}, 32)
		sealed, sealErr := SealDelivery(key, Delivery{Recipient: subject.String() + "@example.test", ContactURL: "https://mycfc.example/legal/direitos"})
		if sealErr != nil {
			t.Fatal(sealErr)
		}
		eventID := uuid.New()
		if _, err = tx.Exec(ctx, `INSERT INTO data_erasure_request_events(id,request_id,actor_role,actor_ref,action,reason_code,from_status,to_status,version,occurred_at)
			VALUES($1,$2,'SYSTEM',$3,'PROCESSING_STARTED','EXECUTION_ACCEPTED','AWAITING_EXECUTION','PROCESSING',3,$4)`, eventID, requestID, executor, now); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO email_outbox(message_type,privacy_request_id,privacy_requester_id,privacy_event_key,sealed_payload,next_attempt_at,created_at,updated_at)
			VALUES('PRIVACY_PROCESSING_STARTED',$1,$2,$3,$4,$5,$5,$5)`, requestID, subject, eventID, sealed, now); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `SELECT privacy_execution_capture_completion_notice($1,$2)`, executionID, eventID); err != nil {
			t.Fatal(err)
		}
		var pendingExecution bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM privacy_completion_list_pending($1,100) pending WHERE pending.execution_id=$2)`, executor, executionID).Scan(&pendingExecution); err != nil || !pendingExecution {
			t.Fatalf("pending completion execution present=%t err=%v", pendingExecution, err)
		}

		// The fixture is uncommitted in tx, so exercise the exact SQL finalizer on
		// this transaction and the token consumer directly.
		rawToken := bytes.Repeat([]byte{7}, completionTokenBytes)
		token := base64.RawURLEncoding.EncodeToString(rawToken)
		link, _ := completionLink("https://mycfc.example/privacy/completion", token)
		completionPayload, _ := SealDelivery(key, Delivery{Recipient: subject.String() + "@example.test", ContactURL: link})
		tokenDigest := sha256.Sum256(rawToken)
		var completedID uuid.UUID
		if err = tx.QueryRow(ctx, `SELECT privacy_completion_finalize($1,$2,$3,$4)`, executionID, executor, tokenDigest[:], completionPayload).Scan(&completedID); err != nil {
			t.Fatal(err)
		}
		var status, messageType string
		var manifests, notices int
		if err = tx.QueryRow(ctx, `SELECT request.status,(SELECT count(*) FROM privacy_erasure_completion_manifests WHERE execution_id=$1),
			(SELECT count(*) FROM email_outbox WHERE privacy_request_id=request.id AND message_type='PRIVACY_COMPLETED'),
			(SELECT message_type FROM email_outbox WHERE privacy_request_id=request.id AND message_type='PRIVACY_COMPLETED')
			FROM data_erasure_requests request WHERE request.id=$2`, executionID, requestID).Scan(&status, &manifests, &notices, &messageType); err != nil {
			t.Fatal(err)
		}
		if completedID != executionID || status != "COMPLETED" || manifests != 1 || notices != 1 || messageType != "PRIVACY_COMPLETED" {
			t.Fatalf("completion id=%s status=%s manifests=%d notices=%d type=%s", completedID, status, manifests, notices, messageType)
		}
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT privacy_completion_validate($1)`, tokenDigest[:]).Scan(&valid); err != nil || !valid {
			t.Fatalf("fresh completion token valid=%t err=%v", valid, err)
		}
		var linkCreatedAt, linkExpiresAt time.Time
		if err = tx.QueryRow(ctx, `SELECT created_at,expires_at FROM privacy_completion_access_links WHERE execution_id=$1`, executionID).Scan(&linkCreatedAt, &linkExpiresAt); err != nil {
			t.Fatal(err)
		}
		if !linkCreatedAt.After(closedAt.Add(23*time.Hour)) || linkExpiresAt.Sub(linkCreatedAt) != 24*time.Hour {
			t.Fatalf("delayed finalization shortened link: closed=%s created=%s expires=%s", closedAt, linkCreatedAt, linkExpiresAt)
		}
		var deliverable bool
		if err = tx.QueryRow(ctx, `SELECT privacy_completion_notice_deliverable($1,clock_timestamp())`, requestID).Scan(&deliverable); err != nil || !deliverable {
			t.Fatalf("fresh completion notice deliverable=%t err=%v", deliverable, err)
		}
		var consumedRef uuid.UUID
		if err = tx.QueryRow(ctx, `SELECT request_ref FROM privacy_completion_consume($1)`, tokenDigest[:]).Scan(&consumedRef); err != nil || consumedRef != requestRef {
			t.Fatalf("consume ref=%s err=%v", consumedRef, err)
		}
		if err = tx.QueryRow(ctx, `SELECT privacy_completion_notice_deliverable($1,clock_timestamp())`, requestID).Scan(&deliverable); err != nil || deliverable {
			t.Fatalf("used completion notice deliverable=%t err=%v", deliverable, err)
		}
		if err = tx.QueryRow(ctx, `SELECT privacy_completion_validate($1)`, tokenDigest[:]).Scan(&valid); err != nil || valid {
			t.Fatalf("used completion token valid=%t err=%v", valid, err)
		}
		if err = tx.QueryRow(ctx, `SELECT request_ref FROM privacy_completion_consume($1)`, tokenDigest[:]).Scan(&consumedRef); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("one-use token replay error=%v", err)
		}
	})

	t.Run("terminal-requeue-needs-distinct-digest-bound-approval", func(t *testing.T) {
		requestID, requestRef, executionID, jobID, failureID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
		planDigest, entryDigest := sha256.Sum256([]byte("requeue-plan")), sha256.Sum256([]byte("requeue-entry"))
		if _, err = tx.Exec(ctx, `INSERT INTO data_erasure_requests(id,public_ref,idempotency_key,subject_user_id,requester_user_id,subject_kind,scope_kind,categories,status,version,received_at,due_at,decision_code,decided_by,decided_at,policy_version,policy_snapshot,updated_at)
			VALUES($1,$2,$3,$4,$4,'SELF','CATEGORIES',ARRAY['core-identity'],'TERMINAL_FAILED',4,$5::timestamptz-interval '2 days',$5::timestamptz+interval '28 days','APPROVED',$6,$5,$7,'{}',$5)`, requestID, requestRef, uuid.New(), subject, now, adminA, policy); err != nil {
			t.Fatal(err)
		}
		plan := `{"policy_version":"` + policy + `","executor_version":"privacy-erasure-executor/v2","schema_version":"privacy-erasure-plan/v2","entries":[]}`
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_request_execution_plans VALUES($1,$2,'privacy-erasure-executor/v2','privacy-erasure-plan/v2',$3,$4,$5)`, requestID, policy, plan, planDigest[:], now); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_erasure_executions(id,request_id,plan_sha256,executor_version,schema_version,request_version_at_start,status,version,started_by_ref,accepted_at,started_at,finished_at,updated_at)
			VALUES($1,$2,$3,'privacy-erasure-executor/v2','privacy-erasure-plan/v2',3,'TERMINAL_FAILED',4,$4,$5,$5,$5,$5)`, executionID, requestID, planDigest[:], executor, now); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_erasure_category_jobs(id,execution_id,plan_entry_position,entry_sha256,category_key,purpose_code,status,next_attempt_at,lease_epoch,attempt_count,created_at,updated_at)
			VALUES($1,$2,1,$3,'core-identity','IDENTITY','TERMINAL_FAILED',$4,1,5,$4,$4)`, jobID, executionID, entryDigest[:], now); err != nil {
			t.Fatal(err)
		}
		leaseID, attemptID := uuid.New(), uuid.New()
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_erasure_job_leases(id,job_id,epoch,worker_ref,acquired_at,heartbeat_at,expires_at,released_at,outcome) VALUES($1,$2,1,$3,$4,$4,$4::timestamptz+interval '5 minutes',$4,'TERMINAL_FAILED')`, leaseID, jobID, executor, now); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_erasure_job_attempts(id,job_id,lease_id,lease_epoch,attempt_number,started_at,finished_at,outcome) VALUES($1,$2,$3,1,5,$4,$4,'TERMINAL_FAILED')`, attemptID, jobID, leaseID, now); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_erasure_failures(id,job_id,attempt_id,classification,stage_code,failure_code,occurred_at) VALUES($1,$2,$3,'TERMINAL','VERIFY','VERIFICATION_FAILED',$4)`, failureID, jobID, attemptID, now); err != nil {
			t.Fatal(err)
		}
		var snapshotJob uuid.UUID
		var canPropose, canApprove bool
		if err = tx.QueryRow(ctx, `SELECT job_id,can_propose,can_approve FROM privacy_completion_control_snapshot($1,$2)`, executor, requestRef).Scan(&snapshotJob, &canPropose, &canApprove); err != nil || snapshotJob != jobID || !canPropose || canApprove {
			t.Fatalf("executor snapshot job=%s propose=%t approve=%t err=%v", snapshotJob, canPropose, canApprove, err)
		}
		var proposalID uuid.UUID
		var digest []byte
		if err = tx.QueryRow(ctx, `SELECT proposal_id,proposal_sha256 FROM privacy_terminal_requeue_propose($1,$2)`, jobID, executor).Scan(&proposalID, &digest); err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(ctx, `SELECT can_propose,can_approve FROM privacy_completion_control_snapshot($1,$2)`, adminB, requestRef).Scan(&canPropose, &canApprove); err != nil || canPropose || !canApprove {
			t.Fatalf("admin snapshot propose=%t approve=%t err=%v", canPropose, canApprove, err)
		}
		expectDatabaseError(`SELECT privacy_terminal_requeue_approve($1,$2,$3)`, proposalID, digest, executor)
		if _, err = tx.Exec(ctx, `SELECT privacy_terminal_requeue_approve($1,$2,$3)`, proposalID, digest, adminB); err != nil {
			t.Fatal(err)
		}
		var jobStatus, requestStatus string
		var allowance int
		if err = tx.QueryRow(ctx, `SELECT job.status,job.manual_attempt_allowance,request.status FROM privacy_erasure_category_jobs job JOIN privacy_erasure_executions execution ON execution.id=job.execution_id JOIN data_erasure_requests request ON request.id=execution.request_id WHERE job.id=$1`, jobID).Scan(&jobStatus, &allowance, &requestStatus); err != nil {
			t.Fatal(err)
		}
		if jobStatus != "PENDING" || requestStatus != "PROCESSING" || allowance != 1 {
			t.Fatalf("requeue status=%s/%s allowance=%d", jobStatus, requestStatus, allowance)
		}
	})

	t.Run("activation-requires-four-current-contracts-and-distinct-approval", func(t *testing.T) {
		if _, err = tx.Exec(ctx, `UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,updated_by=$1,updated_at=$2 WHERE singleton`, adminA, now); err != nil {
			t.Fatal(err)
		}
		contracts := map[string]string{
			"RESTORE":        "mycfc/privacy-restore-drill-attestation/v2",
			"INFRASTRUCTURE": "mycfc/privacy-infrastructure-posture/v1",
			"PROVIDER":       "mycfc/privacy-provider-registry/v2",
			"SCHEMA":         "mycfc/schema-migration-inventory/v1",
		}
		ids := make([]uuid.UUID, 0, 4)
		initialObservedAt := now.Add(-time.Hour)
		for kind, contract := range contracts {
			digest := sha256.Sum256([]byte(kind + contract))
			id := recordActivationFixtureEvidence(t, ctx, tx, adminA, policy, kind, digest[:], initialObservedAt)
			repeatedID := recordActivationFixtureEvidence(t, ctx, tx, adminB, policy, kind, digest[:], initialObservedAt)
			if repeatedID != id {
				t.Fatalf("idempotent evidence %s repeated=%s original=%s", kind, repeatedID, id)
			}
			ids = append(ids, id)
		}
		var snapshotEvidence int
		var snapshotCanPropose bool
		rows, snapshotErr := tx.Query(ctx, `SELECT evidence_id,can_propose FROM privacy_activation_control_snapshot($1)`, executor)
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		for rows.Next() {
			var evidenceID *uuid.UUID
			if err = rows.Scan(&evidenceID, &snapshotCanPropose); err != nil {
				t.Fatal(err)
			}
			if evidenceID != nil {
				snapshotEvidence++
			}
		}
		rows.Close()
		if rows.Err() != nil || snapshotEvidence != 4 || !snapshotCanPropose {
			t.Fatalf("activation snapshot evidence=%d propose=%t err=%v", snapshotEvidence, snapshotCanPropose, rows.Err())
		}
		expectDatabaseError(`INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by,updated_at) VALUES(true,$1,true,true,$2,$3)`, policy, adminA, now)
		var proposalID uuid.UUID
		var digest []byte
		if err = tx.QueryRow(ctx, `SELECT proposal_id,activation_sha256 FROM privacy_activation_propose($1,$2,$3)`, executor, policy, ids).Scan(&proposalID, &digest); err != nil {
			t.Fatal(err)
		}
		expectDatabaseError(`SELECT privacy_activation_approve($1,$2,$3)`, executor, proposalID, digest)
		var approvalID uuid.UUID
		if err = tx.QueryRow(ctx, `SELECT privacy_activation_approve($1,$2,$3)`, adminB, proposalID, digest).Scan(&approvalID); err != nil {
			t.Fatal(err)
		}
		var enabled, ready bool
		var storedApproval *uuid.UUID
		if err = tx.QueryRow(ctx, `SELECT enabled,fulfilment_ready,approval_id FROM privacy_request_activation WHERE singleton`).Scan(&enabled, &ready, &storedApproval); err != nil {
			t.Fatal(err)
		}
		if !enabled || !ready || storedApproval == nil || *storedApproval != approvalID {
			t.Fatalf("activation enabled=%t ready=%t approval=%v", enabled, ready, storedApproval)
		}
		if err = tx.QueryRow(ctx, `SELECT privacy_activation_ready($1),privacy_worker_activation_ready()`, policy).Scan(&enabled, &ready); err != nil || !enabled || !ready {
			t.Fatalf("activation readiness web=%t worker=%t err=%v", enabled, ready, err)
		}
		var canRenew bool
		if err = tx.QueryRow(ctx, `SELECT ready,can_propose,can_renew FROM privacy_activation_control_snapshot($1) LIMIT 1`, executor).Scan(&ready, &snapshotCanPropose, &canRenew); err != nil || !ready || snapshotCanPropose || canRenew {
			t.Fatalf("activation renewal ready=%t propose=%t renew=%t err=%v", ready, snapshotCanPropose, canRenew, err)
		}
		expectDatabaseError(`SELECT proposal_id FROM privacy_activation_propose($1,$2,$3)`, executor, policy, ids)
		newSchemaDigest := sha256.Sum256([]byte("renewed-schema-evidence"))
		_ = recordActivationFixtureEvidence(t, ctx, tx, adminA, policy, "SCHEMA", newSchemaDigest[:], now)
		if err = tx.QueryRow(ctx, `SELECT can_renew FROM privacy_activation_control_snapshot($1) LIMIT 1`, executor).Scan(&canRenew); err != nil || !canRenew {
			t.Fatalf("activation newer evidence renew=%t err=%v", canRenew, err)
		}
		if _, err = tx.Exec(ctx, `ALTER TABLE privacy_activation_evidence DISABLE TRIGGER privacy_activation_evidence_immutable`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE privacy_activation_evidence SET observed_at=$2::timestamptz-interval '90 days 1 second',expires_at=$2::timestamptz-interval '1 second' WHERE id=$1`, ids[0], now); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `ALTER TABLE privacy_activation_evidence ENABLE TRIGGER privacy_activation_evidence_immutable`); err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(ctx, `SELECT privacy_activation_ready($1),privacy_worker_activation_ready()`, policy).Scan(&enabled, &ready); err != nil || enabled || ready {
			t.Fatalf("expired activation web=%t worker=%t err=%v", enabled, ready, err)
		}
	})
}

func TestCompletionControlPublicAPIs(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	now := time.Now().UTC().Truncate(time.Microsecond)
	adminA, adminB, executor, subject := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{adminA, adminB, executor, subject} {
		if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Completion API',$2,'hash','1990-01-01')`, id, id.String()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{adminA, adminB, executor} {
		if _, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_executor_grants(user_id,granted_by,granted_at) VALUES($1,$2,$3)`, executor, adminA, now); err != nil {
		t.Fatal(err)
	}
	policy := "completion-api-" + uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,working_retention_days,adopted_at,adopted_by)
		VALUES($1,'[]','privacy-erasure-executor/v2','privacy-erasure-plan/v2',90,$2,$3)`, policy, now, adminA); err != nil {
		t.Fatal(err)
	}
	activatePrivacyIntegrationFixture(t, ctx, pool, adminB, executor, policy)

	requestID, requestRef, executionID, jobID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	planDigest, entryDigest := sha256.Sum256([]byte("public-requeue-plan"+executionID.String())), sha256.Sum256([]byte("public-requeue-entry"+executionID.String()))
	if _, err = pool.Exec(ctx, `INSERT INTO data_erasure_requests(id,public_ref,idempotency_key,subject_user_id,requester_user_id,subject_kind,scope_kind,categories,status,version,received_at,due_at,decision_code,decided_by,decided_at,policy_version,policy_snapshot,updated_at)
		VALUES($1,$2,$3,$4,$4,'SELF','CATEGORIES',ARRAY['core-identity'],'TERMINAL_FAILED',4,$5,$5::timestamptz+interval '30 days','APPROVED',$6,$5,$7,'{}',$5)`, requestID, requestRef, uuid.New(), subject, now, adminA, policy); err != nil {
		t.Fatal(err)
	}
	plan := `{"policy_version":"` + policy + `","executor_version":"privacy-erasure-executor/v2","schema_version":"privacy-erasure-plan/v2","entries":[]}`
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_request_execution_plans VALUES($1,$2,'privacy-erasure-executor/v2','privacy-erasure-plan/v2',$3,$4,$5)`, requestID, policy, plan, planDigest[:], now); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_erasure_executions(id,request_id,plan_sha256,executor_version,schema_version,request_version_at_start,status,version,started_by_ref,accepted_at,started_at,finished_at,updated_at)
		VALUES($1,$2,$3,'privacy-erasure-executor/v2','privacy-erasure-plan/v2',3,'TERMINAL_FAILED',4,$4,$5,$5,$5,$5)`, executionID, requestID, planDigest[:], executor, now); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_erasure_category_jobs(id,execution_id,plan_entry_position,entry_sha256,category_key,purpose_code,status,next_attempt_at,lease_epoch,attempt_count,created_at,updated_at)
		VALUES($1,$2,1,$3,'core-identity','IDENTITY','TERMINAL_FAILED',$4,1,5,$4,$4)`, jobID, executionID, entryDigest[:], now); err != nil {
		t.Fatal(err)
	}
	leaseID, attemptID := uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_erasure_job_leases(id,job_id,epoch,worker_ref,acquired_at,heartbeat_at,expires_at,released_at,outcome)
		VALUES($1,$2,1,$3,$4,$4,$4::timestamptz+interval '5 minutes',$4,'TERMINAL_FAILED')`, leaseID, jobID, executor, now); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_erasure_job_attempts(id,job_id,lease_id,lease_epoch,attempt_number,started_at,finished_at,outcome)
		VALUES($1,$2,$3,1,5,$4,$4,'TERMINAL_FAILED')`, attemptID, jobID, leaseID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_erasure_failures(id,job_id,attempt_id,classification,stage_code,failure_code,occurred_at)
		VALUES($1,$2,$3,'TERMINAL','VERIFY','VERIFICATION_FAILED',$4)`, uuid.New(), jobID, attemptID, now); err != nil {
		t.Fatal(err)
	}

	service := Service{Pool: pool}
	const brokerRole = "mycfc_privacy_activation_broker"
	brokerIdentifier := pgx.Identifier{brokerRole}.Sanitize()
	var brokerRoleExists bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, brokerRole).Scan(&brokerRoleExists); err != nil {
		t.Fatal(err)
	}
	if !brokerRoleExists {
		if _, err = pool.Exec(ctx, `CREATE ROLE `+brokerIdentifier+` NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanupCtx := context.Background()
			if _, cleanupErr := pool.Exec(cleanupCtx, `DROP OWNED BY `+brokerIdentifier); cleanupErr != nil {
				t.Errorf("drop completion broker privileges: %v", cleanupErr)
				return
			}
			if _, cleanupErr := pool.Exec(cleanupCtx, `DROP ROLE `+brokerIdentifier); cleanupErr != nil {
				t.Errorf("drop completion broker role: %v", cleanupErr)
			}
		})
	}
	var canUsePublicSchema bool
	if err = pool.QueryRow(ctx, `SELECT has_schema_privilege($1,'public','USAGE')`, brokerRole).Scan(&canUsePublicSchema); err != nil {
		t.Fatal(err)
	}
	if !canUsePublicSchema {
		if _, err = pool.Exec(ctx, `GRANT USAGE ON SCHEMA public TO `+brokerIdentifier); err != nil {
			t.Fatal(err)
		}
		if brokerRoleExists {
			t.Cleanup(func() {
				if _, cleanupErr := pool.Exec(context.Background(), `REVOKE USAGE ON SCHEMA public FROM `+brokerIdentifier); cleanupErr != nil {
					t.Errorf("restore completion broker schema privilege: %v", cleanupErr)
				}
			})
		}
	}
	const recordEvidenceRoutine = "privacy_activation_broker_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)"
	var canRecordEvidence bool
	if err = pool.QueryRow(ctx, `SELECT has_function_privilege($1,$2,'EXECUTE')`, brokerRole, recordEvidenceRoutine).Scan(&canRecordEvidence); err != nil {
		t.Fatal(err)
	}
	if !canRecordEvidence {
		if _, err = pool.Exec(ctx, `GRANT EXECUTE ON FUNCTION `+recordEvidenceRoutine+` TO `+brokerIdentifier); err != nil {
			t.Fatal(err)
		}
		if brokerRoleExists {
			t.Cleanup(func() {
				if _, cleanupErr := pool.Exec(context.Background(), `REVOKE EXECUTE ON FUNCTION `+recordEvidenceRoutine+` FROM `+brokerIdentifier); cleanupErr != nil {
					t.Errorf("restore completion broker privilege: %v", cleanupErr)
				}
			})
		}
	}
	brokerConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	brokerConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, setErr := conn.Exec(ctx, `SET SESSION AUTHORIZATION `+brokerIdentifier)
		return setErr
	}
	brokerPool, err := pgxpool.NewWithConfig(ctx, brokerConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer brokerPool.Close()
	evidenceDigest := sha256.Sum256([]byte("public-record-activation-evidence" + policy))
	evidenceSHA := sha256.Sum256([]byte("public-record-activation-artifact" + policy))
	recorded, err := (Service{Pool: brokerPool}).RecordActivationEvidence(ctx, adminA, VerifiedActivationEvidence{
		kind: "SCHEMA", digest: evidenceDigest[:], reference: "mycfc/schema-migration-inventory/v1",
		observedAt: now.Add(-30 * time.Minute), expiresAt: now.Add(90*24*time.Hour - 30*time.Minute),
		artifact: activationArtifactRecord{
			PolicyVersion: policy, ExecutorVersion: SupportedExecutorVersion, PlanSchemaVersion: SupportedPlanSchemaVersion,
			ImageDigest: "sha256:" + strings.Repeat("7", sha256.Size*2), EvidenceRef: "s3://fixture/public-schema?versionId=v1",
			EvidenceSHA256: evidenceSHA[:], SigningKeyID: "fixture-key", SchemaMigrationDigest: bytes.Repeat([]byte{7}, sha256.Size),
			BaselineIncludesThrough: "202609120006_guardian_schema_ready_owner",
		},
	})
	if err != nil || recorded.ID == uuid.Nil || recorded.Kind != "SCHEMA" || !bytes.Equal(recorded.Digest, evidenceDigest[:]) {
		t.Fatalf("recorded activation evidence=%+v err=%v", recorded, err)
	}
	providerPublicKey, providerPrivateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	providerDocument := map[string]any{
		"contract": "mycfc/privacy-provider-registry/v2", "result": "SUCCEEDED", "observed_at": now.Add(-30 * time.Minute).Format(time.RFC3339),
		"policy_version": policy, "executor_version": SupportedExecutorVersion, "plan_schema_version": SupportedPlanSchemaVersion,
		"image_digest": "sha256:" + strings.Repeat("7", sha256.Size*2), "evidence_ref": "s3://fixture/provider-v2.json?versionId=v1",
		"evidence_sha256": strings.Repeat("8", sha256.Size*2), "signing_key_id": "provider-v2-key",
		"registry_state": "READY", "registration_count": 0, "provider_registry_sha256": strings.Repeat("9", sha256.Size*2),
		"inventory_contract": "mycfc/privacy-provider-registry-source/v2",
	}
	providerCanonical, err := json.Marshal(providerDocument)
	if err != nil {
		t.Fatal(err)
	}
	providerDocument["signature_ed25519"] = base64.StdEncoding.EncodeToString(ed25519.Sign(providerPrivateKey, providerCanonical))
	providerPayload, err := json.Marshal(providerDocument)
	if err != nil {
		t.Fatal(err)
	}
	providerRecorded, err := (Service{Pool: brokerPool}).VerifyAndRecordActivationArtifact(ctx, adminA, providerPayload,
		map[string]ed25519.PublicKey{"provider-v2-key": providerPublicKey}, ActivationReleaseBinding{
			PolicyVersion: policy, ExecutorVersion: SupportedExecutorVersion, PlanSchemaVersion: SupportedPlanSchemaVersion,
			ImageDigest: "sha256:" + strings.Repeat("7", sha256.Size*2), SchemaMigrationDigest: strings.Repeat("7", sha256.Size*2),
		}, now)
	if err != nil || providerRecorded.ID == uuid.Nil || providerRecorded.Kind != "PROVIDER" {
		t.Fatalf("recorded empty provider evidence=%+v err=%v", providerRecorded, err)
	}
	snapshot, err := service.CompletionControlSnapshot(ctx, executor, requestRef)
	if err != nil || snapshot.RequestReference != requestRef || len(snapshot.Jobs) != 1 || !snapshot.Jobs[0].CanProposeRequeue {
		t.Fatalf("completion snapshot=%+v err=%v", snapshot, err)
	}
	proposal, err := service.ProposeTerminalRequeue(ctx, executor, jobID)
	if err != nil || proposal.ID == uuid.Nil || len(proposal.Digest) != sha256.Size {
		t.Fatalf("requeue proposal=%+v err=%v", proposal, err)
	}
	approvalSnapshot, err := service.CompletionControlSnapshot(ctx, adminB, requestRef)
	if err != nil || len(approvalSnapshot.Jobs) != 1 || !approvalSnapshot.Jobs[0].CanApproveRequeue || approvalSnapshot.Jobs[0].PendingRequeueProposal == nil {
		t.Fatalf("approval snapshot=%+v err=%v", approvalSnapshot, err)
	}
	if err = service.ApproveTerminalRequeue(ctx, executor, proposal.ID, proposal.Digest); !errors.Is(err, ErrRequeueUnavailable) {
		t.Fatalf("same-actor approval error=%v", err)
	}
	if err = service.ApproveTerminalRequeue(ctx, adminB, proposal.ID, proposal.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CompletionControlSnapshot(ctx, executor, uuid.New()); !errors.Is(err, ErrCompletionUnavailable) {
		t.Fatalf("unknown request snapshot error=%v", err)
	}
	if _, err = service.ProposeTerminalRequeue(ctx, uuid.Nil, jobID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid proposal error=%v", err)
	}
	if err = service.ApproveTerminalRequeue(ctx, adminB, proposal.ID, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid approval error=%v", err)
	}

	activation, err := service.ActivationControlSnapshot(ctx, executor)
	if err != nil || !activation.Ready || len(activation.Evidence) != 4 {
		t.Fatalf("activation snapshot=%+v err=%v", activation, err)
	}
	newEvidence := make([]uuid.UUID, 0, 4)
	for _, kind := range []string{"INFRASTRUCTURE", "PROVIDER", "RESTORE", "SCHEMA"} {
		digest := sha256.Sum256([]byte("renew-" + kind + uuid.NewString()))
		newEvidence = append(newEvidence, recordActivationFixtureEvidence(t, ctx, pool, adminA, policy, kind, digest[:], now))
	}
	activation, err = service.ActivationControlSnapshot(ctx, executor)
	if err != nil || !activation.CanRenew {
		t.Fatalf("renewal snapshot=%+v err=%v", activation, err)
	}
	activationProposal, err := service.ProposeActivation(ctx, executor, policy, newEvidence)
	if err != nil || activationProposal.ID == uuid.Nil || len(activationProposal.Digest) != sha256.Size {
		t.Fatalf("activation proposal=%+v err=%v", activationProposal, err)
	}
	activation, err = service.ActivationControlSnapshot(ctx, adminB)
	if err != nil || !activation.CanApprove || activation.PendingProposal == nil {
		t.Fatalf("activation approval snapshot=%+v err=%v", activation, err)
	}
	if err = service.ApproveActivation(ctx, executor, activationProposal.ID, activationProposal.Digest); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("same-actor activation approval error=%v", err)
	}
	if err = service.ApproveActivation(ctx, adminB, activationProposal.ID, activationProposal.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ProposeActivation(ctx, executor, policy, newEvidence[:3]); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid activation proposal error=%v", err)
	}
	if err = service.ApproveActivation(ctx, adminB, activationProposal.ID, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid activation approval error=%v", err)
	}
	if _, err = (Service{}).ActivationControlSnapshot(ctx, executor); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil-pool activation snapshot error=%v", err)
	}
}

func TestCompletionWorkerAndLinkPublicAPIs(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)
	admin, approver, executor, subject := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{admin, approver, executor, subject} {
		if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Completion worker',$2,'hash','1990-01-01')`, id, id.String()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{admin, approver, executor} {
		if _, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_executor_grants(user_id,granted_by,granted_at) VALUES($1,$2,$3)`, executor, admin, now); err != nil {
		t.Fatal(err)
	}
	policy := "completion-worker-" + uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,working_retention_days,adopted_at,adopted_by)
		VALUES($1,'[]','privacy-erasure-executor/v2','privacy-erasure-plan/v2',90,$2,$3)`, policy, now, admin); err != nil {
		t.Fatal(err)
	}
	activatePrivacyIntegrationFixture(t, ctx, pool, approver, executor, policy)

	requestID, requestRef, executionID := uuid.New(), uuid.New(), uuid.New()
	backupJob, identityJob := uuid.New(), uuid.New()
	executionAt, erasedAt, closedAt := now.Add(-25*time.Hour), now.Add(-24*time.Hour-30*time.Minute), now.Add(-24*time.Hour)
	planDigest := sha256.Sum256([]byte("completion-plan-" + executionID.String()))
	backupEntry := sha256.Sum256([]byte("completion-backup-" + executionID.String()))
	identityEntry := sha256.Sum256([]byte("completion-identity-" + executionID.String()))
	resultDigest := sha256.Sum256([]byte("completion-result-" + executionID.String()))
	plan := `{"policy_version":"` + policy + `","executor_version":"privacy-erasure-executor/v2","schema_version":"privacy-erasure-plan/v2","entries":[{"category":"backup-tombstones","purpose":"RESTORE_SAFETY","operations":["BACKUP_TOMBSTONE_REPLAY"],"action_version":"v1"},{"category":"core-identity","purpose":"IDENTITY","operations":["IDENTITY_CLEAR"],"action_version":"v1"}]}`
	if _, err = pool.Exec(ctx, `INSERT INTO data_erasure_requests(id,public_ref,idempotency_key,subject_user_id,requester_user_id,subject_kind,scope_kind,categories,status,version,received_at,due_at,decision_code,decided_by,decided_at,policy_version,policy_snapshot,updated_at)
		VALUES($1,$2,$3,$4,$4,'SELF','CATEGORIES',ARRAY['backup-tombstones','core-identity'],'PROCESSING',3,$5::timestamptz-interval '2 days',$5::timestamptz+interval '28 days','APPROVED',$6,$5,$7,'{}',$5)`, requestID, requestRef, uuid.New(), subject, now, admin, policy); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_request_execution_plans VALUES($1,$2,'privacy-erasure-executor/v2','privacy-erasure-plan/v2',$3,$4,$5)`, requestID, policy, plan, planDigest[:], now); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_erasure_executions(id,request_id,plan_sha256,executor_version,schema_version,request_version_at_start,status,version,started_by_ref,accepted_at,started_at,finished_at,updated_at)
		VALUES($1,$2,$3,'privacy-erasure-executor/v2','privacy-erasure-plan/v2',3,'SUCCEEDED',4,$4,$5,$5,$5,$5)`, executionID, requestID, planDigest[:], executor, executionAt); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		jobID     uuid.UUID
		position  int
		entry     []byte
		category  string
		purpose   string
		operation string
	}{
		{backupJob, 1, backupEntry[:], "backup-tombstones", "RESTORE_SAFETY", "BACKUP_TOMBSTONE_REPLAY"},
		{identityJob, 2, identityEntry[:], "core-identity", "IDENTITY", "IDENTITY_CLEAR"},
	} {
		leaseID, attemptID := uuid.New(), uuid.New()
		if _, err = pool.Exec(ctx, `INSERT INTO privacy_erasure_category_jobs(id,execution_id,plan_entry_position,entry_sha256,category_key,purpose_code,status,next_attempt_at,lease_epoch,attempt_count,created_at,updated_at,completed_at)
			VALUES($1,$2,$3,$4,$5,$6,'SUCCEEDED',$7,1,1,$7,$7,$7)`, fixture.jobID, executionID, fixture.position, fixture.entry, fixture.category, fixture.purpose, executionAt); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO privacy_erasure_job_leases(id,job_id,epoch,worker_ref,acquired_at,heartbeat_at,expires_at,released_at,outcome)
			VALUES($1,$2,1,$3,$4,$4,$4::timestamptz+interval '5 minutes',$4,'SUCCEEDED')`, leaseID, fixture.jobID, executor, executionAt); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO privacy_erasure_job_attempts(id,job_id,lease_id,lease_epoch,attempt_number,started_at,finished_at,outcome)
			VALUES($1,$2,$3,1,1,$4,$4,'SUCCEEDED')`, attemptID, fixture.jobID, leaseID, executionAt); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO privacy_erasure_job_checkpoints(job_id,operation_position,operation_code,action_version,status,completed_by_attempt_id,created_at,completed_at,affected_rows,result_sha256)
			VALUES($1,1,$2,'v1','SUCCEEDED',$3,$4,$4,1,$5)`, fixture.jobID, fixture.operation, attemptID, executionAt, resultDigest[:]); err != nil {
			t.Fatal(err)
		}
	}
	locator, ciphertext := sha256.Sum256([]byte("completion-locator"+executionID.String())), sha256.Sum256([]byte("completion-ciphertext"+executionID.String()))
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_protected.restore_tombstone_receipts(execution_id,request_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
		VALUES($1,$2,'restore-tombstone/v2','enc-v2','loc-v2',$3,'intent-version',$4,512,$5,$5)`, executionID, requestID, locator[:], ciphertext[:], executionAt); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_protected.restore_tombstone_closure_intents(execution_id,closed_at,evidence_expires_at) VALUES($1,$2,$2::timestamptz+interval '24 months')`, executionID, closedAt); err != nil {
		t.Fatal(err)
	}
	programmeID, seasonID, membershipID := uuid.New(), uuid.New(), uuid.New()
	code := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	if _, err = pool.Exec(ctx, `INSERT INTO programmes(id,code,name_pt) VALUES($1,$2,'Programa worker')`, programmeID, "Worker"+code); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Época worker',$3::date-3,$3::date-1)`, seasonID, "W"+code, executionAt); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,$5::date-3,$5::date-1)`, membershipID, subject, seasonID, programmeID, erasedAt); err != nil {
		t.Fatal(err)
	}
	principalID := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_pseudonymous_principals(id,purpose) VALUES($1,'MEMBERSHIP')`, principalID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE user_memberships SET user_id=NULL,principal_id=$2 WHERE id=$1`, membershipID, principalID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET name='Conta eliminada',email=NULL,email_verified_at=NULL,password_hash=NULL,date_of_birth='1900-01-01',is_active=false,
		leaderboard_visible=false,credential_version=credential_version+1,erased_at=$2,erasure_execution_id=$3,updated_at=$2 WHERE id=$1`, subject, erasedAt, executionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_protected.membership_history_source_captures(execution_id) VALUES($1)`, executionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_protected.membership_history_source_rows(execution_id,membership_id) VALUES($1,$2)`, executionID, membershipID); err != nil {
		t.Fatal(err)
	}
	protector, _ := tombstoneProtectorFixture(t)
	if err = (TombstoneExportWorker{Store: pool, Ledger: &capturingTombstoneLedger{}, Protector: protector, WorkerRef: executor}).ExportClosure(ctx, executionID); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{8}, 32)
	sealedTarget, err := SealDelivery(key, Delivery{Recipient: subject.String() + "@example.test", ContactURL: "https://mycfc.example/legal/direitos"})
	if err != nil {
		t.Fatal(err)
	}
	eventID := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO data_erasure_request_events(id,request_id,actor_role,actor_ref,action,reason_code,from_status,to_status,version,occurred_at)
		VALUES($1,$2,'SYSTEM',$3,'PROCESSING_STARTED','EXECUTION_ACCEPTED','AWAITING_EXECUTION','PROCESSING',3,$4)`, eventID, requestID, executor, now); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO email_outbox(message_type,privacy_request_id,privacy_requester_id,privacy_event_key,sealed_payload,next_attempt_at,created_at,updated_at)
		VALUES('PRIVACY_PROCESSING_STARTED',$1,$2,$3,$4,$5,$5,$5)`, requestID, subject, eventID, sealedTarget, now); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `SELECT privacy_execution_capture_completion_notice($1,$2)`, executionID, eventID); err != nil {
		t.Fatal(err)
	}

	uniqueToken := sha256.Sum256([]byte("completion-token-" + executionID.String()))
	rawToken := uniqueToken[:]
	worker := CompletionWorker{
		Pool: pool, WorkerRef: executor, Key: key, DetailBaseURL: "https://mycfc.example/privacy/completion",
		Random: func(target []byte) (int, error) { return copy(target, rawToken), nil },
	}
	ready, err := worker.ActivationReady(ctx)
	if err != nil || !ready {
		t.Fatalf("activation ready=%t err=%v", ready, err)
	}
	pending, err := worker.ListPending(ctx, 10)
	if err != nil || !slices.Contains(pending, executionID) {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	if _, err = worker.ListPending(ctx, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid pending limit error=%v", err)
	}
	completed, err := worker.Complete(ctx, executionID)
	if err != nil || completed.ExecutionID != executionID || completed.AlreadyComplete {
		t.Fatalf("completion=%+v err=%v", completed, err)
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	service := Service{Pool: pool}
	if err = service.ValidateCompletionLink(ctx, token); err != nil {
		t.Fatal(err)
	}
	detail, err := service.ConsumeCompletionDetail(ctx, token)
	if err != nil || detail.RequestReference != requestRef || detail.Status != "COMPLETED" || detail.Categories != 2 || detail.Checkpoints != 2 {
		t.Fatalf("completion detail=%+v err=%v", detail, err)
	}
	if err = service.ValidateCompletionLink(ctx, token); !errors.Is(err, ErrCompletionLinkUnavailable) {
		t.Fatalf("consumed link validation error=%v", err)
	}
	if _, err = service.ConsumeCompletionDetail(ctx, token); !errors.Is(err, ErrCompletionLinkUnavailable) {
		t.Fatalf("completion link replay error=%v", err)
	}
	completed, err = worker.Complete(ctx, executionID)
	if err != nil || !completed.AlreadyComplete {
		t.Fatalf("idempotent completion=%+v err=%v", completed, err)
	}
	if _, err = worker.Complete(ctx, uuid.Nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid completion error=%v", err)
	}
	if err = service.ValidateCompletionLink(ctx, "invalid"); !errors.Is(err, ErrCompletionLinkUnavailable) {
		t.Fatalf("invalid token validation error=%v", err)
	}
	if _, err = (Service{}).ConsumeCompletionDetail(ctx, token); !errors.Is(err, ErrCompletionLinkUnavailable) {
		t.Fatalf("nil-pool consume error=%v", err)
	}
}
