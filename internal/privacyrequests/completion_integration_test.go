//go:build integration

package privacyrequests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

	t.Run("finalization-is-atomic-and-link-is-one-use", func(t *testing.T) {
		requestID, requestRef, executionID, jobID, attemptID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
		executionAt := now.Add(-25 * time.Hour)
		planDigest, entryDigest, resultDigest := sha256.Sum256([]byte("plan"+executionID.String())), sha256.Sum256([]byte("entry")), sha256.Sum256([]byte("result"))
		plan := `{"policy_version":"` + policy + `","executor_version":"privacy-erasure-executor/v2","schema_version":"privacy-erasure-plan/v2","entries":[{"category":"backup-tombstones","purpose":"RESTORE_SAFETY","operations":["BACKUP_TOMBSTONE_REPLAY"],"action_version":"v1"}]}`
		if _, err = tx.Exec(ctx, `INSERT INTO data_erasure_requests(id,public_ref,idempotency_key,subject_user_id,requester_user_id,subject_kind,scope_kind,categories,status,version,received_at,due_at,
			decision_code,decided_by,decided_at,policy_version,policy_snapshot,updated_at)
			VALUES($1,$2,$3,$4,$4,'SELF','CATEGORIES',ARRAY['backup-tombstones'],'PROCESSING',3,$5::timestamptz-interval '2 days',$5::timestamptz+interval '28 days','APPROVED',$6,$5,$7,'{}',$5)`,
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
		locator, ciphertext := sha256.Sum256([]byte("locator"+executionID.String())), sha256.Sum256([]byte("ciphertext"+executionID.String()))
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.restore_tombstone_receipts(execution_id,request_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
			VALUES($1,$2,'restore-tombstone/v2','enc-v2','loc-v2',$3,'version-intent',$4,512,$5,$5)`, executionID, requestID, locator[:], ciphertext[:], now); err != nil {
			t.Fatal(err)
		}
		closedAt := now.Add(-24 * time.Hour)
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.restore_tombstone_closure_intents(execution_id,closed_at,evidence_expires_at) VALUES($1,$2,$2::timestamptz+interval '24 months')`, executionID, closedAt); err != nil {
			t.Fatal(err)
		}
		closureLocator, closureCiphertext := sha256.Sum256([]byte("closure-locator"+executionID.String())), sha256.Sum256([]byte("closure-cipher"+executionID.String()))
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.restore_tombstone_closure_receipts(execution_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
			VALUES($1,'restore-tombstone-closure/v2','enc-v2','loc-v2',$2,'version-closure',$3,512,$4,$4)`, executionID, closureLocator[:], closureCiphertext[:], closedAt); err != nil {
			t.Fatal(err)
		}
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
		var pendingExecution uuid.UUID
		if err = tx.QueryRow(ctx, `SELECT execution_id FROM privacy_completion_list_pending($1,1)`, executor).Scan(&pendingExecution); err != nil || pendingExecution != executionID {
			t.Fatalf("pending completion execution=%s err=%v", pendingExecution, err)
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
		contracts := map[string]string{
			"RESTORE":        "mycfc/privacy-restore-drill-attestation/v1",
			"INFRASTRUCTURE": "mycfc/privacy-infrastructure-posture/v1",
			"PROVIDER":       "mycfc/privacy-provider-registry/v1",
			"SCHEMA":         "mycfc/schema-migration-inventory/v1",
		}
		ids := make([]uuid.UUID, 0, 4)
		initialObservedAt := now.Add(-time.Hour)
		for kind, contract := range contracts {
			digest := sha256.Sum256([]byte(kind + contract))
			var id uuid.UUID
			if err = tx.QueryRow(ctx, `SELECT privacy_activation_record_evidence($1,$2,$3,$4,$5)`, adminA, kind, digest[:], contract, initialObservedAt).Scan(&id); err != nil {
				t.Fatal(err)
			}
			var repeatedID uuid.UUID
			if err = tx.QueryRow(ctx, `SELECT privacy_activation_record_evidence($1,$2,$3,$4,$5)`, adminB, kind, digest[:], contract, initialObservedAt).Scan(&repeatedID); err != nil || repeatedID != id {
				t.Fatalf("idempotent evidence %s repeated=%s original=%s err=%v", kind, repeatedID, id, err)
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
		var renewedSchemaID uuid.UUID
		if err = tx.QueryRow(ctx, `SELECT privacy_activation_record_evidence($1,'SCHEMA',$2,'mycfc/schema-migration-inventory/v1',$3)`, adminA, newSchemaDigest[:], now).Scan(&renewedSchemaID); err != nil {
			t.Fatal(err)
		}
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
