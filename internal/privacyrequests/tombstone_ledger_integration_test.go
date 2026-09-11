//go:build integration

package privacyrequests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type capturingTombstoneLedger struct {
	sealed []SealedTombstone
	err    error
}

func (l *capturingTombstoneLedger) Write(_ context.Context, sealed SealedTombstone) (TombstoneLedgerReceipt, error) {
	if l.err != nil {
		return TombstoneLedgerReceipt{}, l.err
	}
	l.sealed = append(l.sealed, sealed)
	writtenAt := time.Now().UTC().Truncate(time.Microsecond)
	return TombstoneLedgerReceipt{
		LocatorKeyID: sealed.LocatorKeyID, LocatorDigest: bytes.Clone(sealed.Locator),
		ObjectVersion: "integration-version", CiphertextSHA: bytes.Clone(sealed.SHA256),
		SizeBytes: int64(len(sealed.Encoded)), WrittenAt: writtenAt, VerifiedAt: writtenAt,
	}, nil
}

func TestTombstoneExportWorkerPublicIntentBoundary(t *testing.T) {
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
		if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Tombstone worker',$2,'hash','1990-01-01')`, id, id.String()+"@example.test"); err != nil {
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
	policy := "tombstone-export-" + uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,working_retention_days,adopted_at,adopted_by)
		VALUES($1,'[]','privacy-erasure-executor/v2','privacy-erasure-plan/v2',90,$2,$3)`, policy, now, admin); err != nil {
		t.Fatal(err)
	}
	activatePrivacyIntegrationFixture(t, ctx, pool, approver, executor, policy)

	requestID, requestRef, executionID := uuid.New(), uuid.New(), uuid.New()
	identityJobID, tombstoneJobID, leaseID, attemptID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	planDigest := sha256.Sum256([]byte("intent-plan-" + executionID.String()))
	identityEntry := sha256.Sum256([]byte("identity-entry-" + executionID.String()))
	tombstoneEntry := sha256.Sum256([]byte("tombstone-entry-" + executionID.String()))
	plan := `{"policy_version":"` + policy + `","executor_version":"privacy-erasure-executor/v2","schema_version":"privacy-erasure-plan/v2","entries":[]}`
	if _, err = pool.Exec(ctx, `INSERT INTO data_erasure_requests(id,public_ref,idempotency_key,subject_user_id,requester_user_id,subject_kind,scope_kind,categories,status,version,received_at,due_at,decision_code,decided_by,decided_at,policy_version,policy_snapshot,updated_at)
		VALUES($1,$2,$3,$4,$4,'SELF','CATEGORIES',ARRAY['core-identity','backup-tombstones'],'PROCESSING',3,$5,$5::timestamptz+interval '30 days','APPROVED',$6,$5,$7,'{}',$5)`, requestID, requestRef, uuid.New(), subject, now, admin, policy); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_request_execution_plans VALUES($1,$2,'privacy-erasure-executor/v2','privacy-erasure-plan/v2',$3,$4,$5)`, requestID, policy, plan, planDigest[:], now); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_erasure_executions(id,request_id,plan_sha256,executor_version,schema_version,request_version_at_start,status,version,started_by_ref,accepted_at,started_at,updated_at)
		VALUES($1,$2,$3,'privacy-erasure-executor/v2','privacy-erasure-plan/v2',3,'RUNNING',3,$4,$5,$5,$5)`, executionID, requestID, planDigest[:], executor, now); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_erasure_category_jobs(id,execution_id,plan_entry_position,entry_sha256,category_key,purpose_code,status,next_attempt_at,lease_epoch,attempt_count,created_at,updated_at)
		VALUES($1,$2,1,$3,'core-identity','IDENTITY','PENDING',$4,0,0,$4,$4),($5,$2,2,$6,'backup-tombstones','RESTORE_SAFETY','LEASED',$4,1,1,$4,$4)`, identityJobID, executionID, identityEntry[:], now, tombstoneJobID, tombstoneEntry[:]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_erasure_job_checkpoints(job_id,operation_position,operation_code,action_version,status,created_at)
		VALUES($1,1,'IDENTITY_CLEAR','v1','PENDING',$3),($2,1,'BACKUP_TOMBSTONE_REPLAY','v1','PENDING',$3)`, identityJobID, tombstoneJobID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_erasure_job_leases(id,job_id,epoch,worker_ref,acquired_at,heartbeat_at,expires_at)
		VALUES($1,$2,1,$3,$4,$4,$4::timestamptz+interval '5 minutes')`, leaseID, tombstoneJobID, executor, now); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO privacy_erasure_job_attempts(id,job_id,lease_id,lease_epoch,attempt_number,started_at)
		VALUES($1,$2,$3,1,1,$4)`, attemptID, tombstoneJobID, leaseID, now); err != nil {
		t.Fatal(err)
	}

	protector, privateKey := tombstoneProtectorFixture(t)
	ledger := &capturingTombstoneLedger{}
	lease := ExecutionLease{
		Job: ExecutionJob{
			PrivacyErasureCategoryJob: dbgen.PrivacyErasureCategoryJob{
				ID: tombstoneJobID, ExecutionID: executionID, CategoryKey: "backup-tombstones", PurposeCode: "RESTORE_SAFETY",
				Status: "LEASED", EntrySha256: tombstoneEntry[:], LeaseEpoch: 1, AttemptCount: 1,
			},
			ActiveLeaseID: leaseID, ActiveAttemptID: attemptID, WorkerRef: executor,
			LeaseExpiresAt: pgtype.Timestamptz{Time: now.Add(5 * time.Minute), Valid: true},
		},
		Checkpoints: []dbgen.PrivacyErasureJobCheckpoint{{
			JobID: tombstoneJobID, OperationPosition: 1, OperationCode: "BACKUP_TOMBSTONE_REPLAY", ActionVersion: SupportedActionVersion, Status: "PENDING",
		}},
	}
	worker := TombstoneExportWorker{Store: pool, Ledger: ledger, Protector: protector, WorkerRef: executor}
	if err = worker.Export(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if len(ledger.sealed) != 1 || ledger.sealed[0].Kind != "intent" {
		t.Fatalf("ledger writes=%d kind=%q", len(ledger.sealed), ledger.sealed[0].Kind)
	}
	opened, err := OpenRestoreTombstoneV2(privateKey, ledger.sealed[0].LocatorKeyID, ledger.sealed[0].Locator, ledger.sealed[0].Envelope)
	if err != nil {
		t.Fatal(err)
	}
	if opened.ExecutionID != executionID || opened.RequestRef != requestRef || opened.Replay == nil ||
		!slices.Equal(opened.Replay.Operations, []string{"IDENTITY_CLEAR", "PROVIDER_LOCAL_FENCE"}) {
		t.Fatalf("unexpected exported intent: %+v", opened)
	}
	var checkpointStatus, ledgerVersion string
	if err = pool.QueryRow(ctx, `SELECT checkpoint.status,receipt.ledger_version FROM privacy_erasure_job_checkpoints checkpoint
		JOIN privacy_protected.restore_tombstone_receipts receipt ON receipt.execution_id=$1
		WHERE checkpoint.job_id=$2`, executionID, tombstoneJobID).Scan(&checkpointStatus, &ledgerVersion); err != nil {
		t.Fatal(err)
	}
	if checkpointStatus != "SUCCEEDED" || ledgerVersion != TombstoneRecordVersion {
		t.Fatalf("checkpoint=%s ledger=%s", checkpointStatus, ledgerVersion)
	}
	if err = worker.Export(ctx, lease); !errors.Is(err, ErrTombstoneUnavailable) {
		t.Fatalf("completed checkpoint replay error=%v", err)
	}

	bad := worker
	bad.WorkerRef = uuid.New()
	if err = bad.Export(ctx, lease); !errors.Is(err, ErrTombstoneInvalid) {
		t.Fatalf("worker mismatch error=%v", err)
	}
	bad = worker
	bad.Ledger = nil
	if err = bad.Export(ctx, lease); !errors.Is(err, ErrTombstoneInvalid) {
		t.Fatalf("missing ledger error=%v", err)
	}
	invalidCheckpoint := lease
	invalidCheckpoint.Checkpoints = nil
	if err = worker.Export(ctx, invalidCheckpoint); !errors.Is(err, ErrTombstoneInvalid) {
		t.Fatalf("checkpoint boundary error=%v", err)
	}
}
