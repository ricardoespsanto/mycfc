//go:build integration

package privacyrequests

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type providerTargetProtectorFailure struct {
	sealTargetErr, digestTargetErr, sealCredentialErr, digestCredentialErr error
}

func (s providerTargetProtectorFailure) SealProviderTarget(ProviderTargetBinding, string, []byte) (ProviderTargetEnvelope, error) {
	return ProviderTargetEnvelope{Version: ProviderTargetEnvelopeVersion, Algorithm: providerTargetAlgorithm, KeyID: "provider-test-encryption",
		Encapsulation: bytes.Repeat([]byte{1}, 32), Nonce: bytes.Repeat([]byte{2}, 12), Ciphertext: bytes.Repeat([]byte{3}, 32)}, s.sealTargetErr
}

func (s providerTargetProtectorFailure) DigestProviderTarget(ProviderTargetBinding, string, []byte) (ProviderTargetDigest, error) {
	return ProviderTargetDigest{KeyID: "provider-test-digest", Digest: bytes.Repeat([]byte{4}, 32)}, s.digestTargetErr
}

func (s providerTargetProtectorFailure) SealProviderCredential(ProviderTargetBinding, string, []byte) (ProviderTargetEnvelope, error) {
	return ProviderTargetEnvelope{Version: ProviderTargetEnvelopeVersion, Algorithm: providerTargetAlgorithm, KeyID: "provider-test-encryption",
		Encapsulation: bytes.Repeat([]byte{5}, 32), Nonce: bytes.Repeat([]byte{6}, 12), Ciphertext: bytes.Repeat([]byte{7}, 32)}, s.sealCredentialErr
}

func (s providerTargetProtectorFailure) DigestProviderCredential(ProviderTargetBinding, string, []byte) (ProviderTargetDigest, error) {
	return ProviderTargetDigest{KeyID: "provider-test-credential-digest", Digest: bytes.Repeat([]byte{8}, 32)}, s.digestCredentialErr
}

func TestProviderCaptureAndEvidenceAreAtomicFencedAndPrivate(t *testing.T) {
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
	defer tx.Rollback(ctx)

	fixture := seedProviderExecutionFixture(t, ctx, tx, "fake-provider", ProviderRoleProcessor, "fake-delete/v1", "ACTIVE")
	// The provider-neutral wearable foundation is deliberately not part of the
	// active external registry or capture inventory.
	_, err = tx.Exec(ctx, `INSERT INTO activity_connections(user_id,provider,provider_user_id,credentials_ciphertext,credential_key_id)
	 VALUES($1,'testwearable',$2,'wearable-secret','wearable-key')`, fixture.subjectID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}

	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := NewX25519ProviderTargetProtector("provider-test-encryption", private.PublicKey().Bytes(),
		"provider-test-digest", bytes.Repeat([]byte{4}, 32), "provider-test-credential-digest", bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeProviderAdapter{results: []ProviderErasureResult{{Outcome: ProviderOutcomeDeletionVerified, EvidenceCode: "REMOTE_DELETION_RECEIPT", EvidenceReference: []byte("opaque-receipt")}}}
	registration := providerRegistration(adapter)
	registry, err := NewProviderExecutionRegistry(registration)
	if err != nil {
		t.Fatal(err)
	}
	service := Service{ProviderRegistry: registry, ProviderTargets: protector}
	plan := providerExecutionPlanFixture(fixture.category, fixture.entryDigest)
	if err = service.materializeProviderTargets(ctx, tx, dbgen.New(tx), fixture.execution, plan, fixture.subjectID); err != nil {
		t.Fatal(err)
	}

	var state string
	var credentials []byte
	var syncEnabled, webhookEnabled, reconnectEnabled bool
	if err = tx.QueryRow(ctx, `SELECT state,credential_opaque,sync_enabled,webhook_enabled,reconnect_enabled FROM privacy_protected.provider_connections WHERE id=$1`, fixture.connectionID).
		Scan(&state, &credentials, &syncEnabled, &webhookEnabled, &reconnectEnabled); err != nil {
		t.Fatal(err)
	}
	if state != "QUARANTINED" || credentials != nil || syncEnabled || webhookEnabled || reconnectEnabled {
		t.Fatalf("connection was not fenced: state=%s credential=%x flags=%v/%v/%v", state, credentials, syncEnabled, webhookEnabled, reconnectEnabled)
	}
	var targetCount, quarantineCount int
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM privacy_protected.provider_targets WHERE execution_id=$1),(SELECT count(*) FROM privacy_protected.provider_credential_quarantine)`, fixture.execution.ID).Scan(&targetCount, &quarantineCount); err != nil {
		t.Fatal(err)
	}
	if targetCount != 1 || quarantineCount != 1 {
		t.Fatalf("target=%d quarantine=%d", targetCount, quarantineCount)
	}
	var commitmentKeyID string
	var credentialCommitment []byte
	if err = tx.QueryRow(ctx, `SELECT source_commitment_key_id,source_commitment FROM privacy_protected.provider_credential_quarantine LIMIT 1`).
		Scan(&commitmentKeyID, &credentialCommitment); err != nil {
		t.Fatal(err)
	}
	unkeyed := sha256.Sum256([]byte("minimum-provider-credential"))
	if commitmentKeyID != "provider-test-credential-digest" || len(credentialCommitment) != sha256.Size || bytes.Equal(credentialCommitment, unkeyed[:]) {
		t.Fatalf("credential commitment key=%q digest=%x", commitmentKeyID, credentialCommitment)
	}
	var wearableState string
	if err = tx.QueryRow(ctx, `SELECT status FROM activity_connections WHERE user_id=$1 AND provider='testwearable'`, fixture.subjectID).Scan(&wearableState); err != nil || wearableState != "ACTIVE" {
		t.Fatalf("inactive wearable was captured: %s %v", wearableState, err)
	}

	lease := fixture.lease
	worker := ProviderExecutionWorker{Pool: tx, Registry: registry, WorkerRef: fixture.workerRef, PrivateKey: private.Bytes(),
		TranscriptKeyID: "provider-transcript-test", TranscriptKey: bytes.Repeat([]byte{6}, 32),
		CredentialDigestKeys: map[string][]byte{"provider-test-credential-digest": bytes.Repeat([]byte{5}, 32)}}
	if _, err = tx.Exec(ctx, "SAVEPOINT stale_provider_worker"); err != nil {
		t.Fatal(err)
	}
	stale := lease
	stale.Job.LeaseEpoch++
	if _, staleErr := worker.CompleteCheckpoint(ctx, stale); !errors.Is(staleErr, ErrLeaseLost) {
		t.Fatalf("stale provider worker error=%v", staleErr)
	}
	if _, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT stale_provider_worker"); err != nil {
		t.Fatal(err)
	}
	if adapter.calls != 0 {
		t.Fatalf("stale provider worker invoked adapter %d times", adapter.calls)
	}
	checkpoint, err := worker.CompleteCheckpoint(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Status != "SUCCEEDED" || adapter.calls != 1 {
		t.Fatalf("checkpoint=%s calls=%d", checkpoint.Status, adapter.calls)
	}
	var connectionCount, evidenceCount int
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM privacy_protected.provider_credential_quarantine),(SELECT count(*) FROM privacy_protected.provider_connections WHERE id=$1),(SELECT count(*) FROM privacy_protected.provider_evidence WHERE job_id=$2)`, fixture.connectionID, fixture.jobID).Scan(&quarantineCount, &connectionCount, &evidenceCount); err != nil {
		t.Fatal(err)
	}
	if quarantineCount != 0 || connectionCount != 0 || evidenceCount != 1 {
		t.Fatalf("quarantine=%d connection=%d evidence=%d", quarantineCount, connectionCount, evidenceCount)
	}
	var evidenceText string
	if err = tx.QueryRow(ctx, `SELECT row_to_json(e)::text FROM privacy_protected.provider_evidence e WHERE job_id=$1`, fixture.jobID).Scan(&evidenceText); err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range []string{"opaque-receipt", "remote-account-secret", "minimum-provider-credential", fixture.subjectID.String()} {
		if bytes.Contains([]byte(evidenceText), []byte(prohibited)) {
			t.Fatalf("evidence leaked prohibited value %q: %s", prohibited, evidenceText)
		}
	}
	var publicTableRead, publicCaptureExecute, publicWorkerExecute bool
	ordinaryRole := "provider_ordinary_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = tx.Exec(ctx, "CREATE ROLE "+pgx.Identifier{ordinaryRole}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT has_table_privilege($1,'privacy_protected.provider_targets','SELECT'),
	 has_function_privilege($1,'privacy_execution_capture_provider_connections(uuid,uuid,text)','EXECUTE'),
	 has_function_privilege($1,'privacy_worker_list_provider_targets(uuid,uuid,uuid,bigint,uuid)','EXECUTE')`, ordinaryRole).
		Scan(&publicTableRead, &publicCaptureExecute, &publicWorkerExecute); err != nil {
		t.Fatal(err)
	}
	if publicTableRead || publicCaptureExecute || publicWorkerExecute {
		t.Fatalf("PUBLIC privileges table=%v capture=%v worker=%v", publicTableRead, publicCaptureExecute, publicWorkerExecute)
	}
}

func TestUnknownProviderRollsBackCaptureAndQuarantine(t *testing.T) {
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
	defer tx.Rollback(ctx)
	fixture := seedProviderExecutionFixture(t, ctx, tx, "unknown-provider", ProviderRoleProcessor, "unknown/v1", "ACTIVE")
	if _, err = tx.Exec(ctx, "SAVEPOINT before_provider_capture"); err != nil {
		t.Fatal(err)
	}

	private, _ := ecdh.X25519().GenerateKey(rand.Reader)
	protector, _ := NewX25519ProviderTargetProtector("provider-test-encryption", private.PublicKey().Bytes(),
		"provider-test-digest", bytes.Repeat([]byte{4}, 32), "provider-test-credential-digest", bytes.Repeat([]byte{5}, 32))
	registry, _ := NewProviderExecutionRegistry(providerRegistration(&fakeProviderAdapter{}))
	service := Service{ProviderRegistry: registry, ProviderTargets: protector}
	err = service.materializeProviderTargets(ctx, tx, dbgen.New(tx), fixture.execution, providerExecutionPlanFixture(fixture.category, fixture.entryDigest), fixture.subjectID)
	if !errors.Is(err, ErrProviderRegistryUnavailable) {
		t.Fatalf("unknown provider error=%v", err)
	}
	if _, rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT before_provider_capture"); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	var state string
	var credential []byte
	if err = tx.QueryRow(ctx, `SELECT state,credential_opaque FROM privacy_protected.provider_connections WHERE id=$1`, fixture.connectionID).Scan(&state, &credential); err != nil {
		t.Fatal(err)
	}
	if state != "ACTIVE" || !bytes.Equal(credential, []byte("minimum-provider-credential")) {
		t.Fatalf("rollback state=%s credential=%q", state, credential)
	}
}

func TestProviderMaterializationFailsClosedAtEveryProtectionBoundary(t *testing.T) {
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
	defer tx.Rollback(ctx)
	fixture := seedProviderExecutionFixture(t, ctx, tx, "fake-provider", ProviderRoleProcessor, "fake-delete/v1", "ACTIVE")
	registry, err := NewProviderExecutionRegistry(providerRegistration(&fakeProviderAdapter{}))
	if err != nil {
		t.Fatal(err)
	}
	plan := providerExecutionPlanFixture(fixture.category, fixture.entryDigest)
	if err = (Service{}).materializeProviderTargets(ctx, tx, dbgen.New(tx), fixture.execution, plan, fixture.subjectID); !errors.Is(err, ErrExecutorUnavailable) {
		t.Fatalf("missing registry error=%v", err)
	}
	for name, protector := range map[string]providerTargetProtectorFailure{
		"target-seal":       {sealTargetErr: errors.New("target seal failed")},
		"target-digest":     {digestTargetErr: errors.New("target digest failed")},
		"credential-seal":   {sealCredentialErr: errors.New("credential seal failed")},
		"credential-digest": {digestCredentialErr: errors.New("credential digest failed")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, savepointErr := tx.Exec(ctx, "SAVEPOINT provider_protection_boundary"); savepointErr != nil {
				t.Fatal(savepointErr)
			}
			service := Service{ProviderRegistry: registry, ProviderTargets: protector}
			materializeErr := service.materializeProviderTargets(ctx, tx, dbgen.New(tx), fixture.execution, plan, fixture.subjectID)
			if !errors.Is(materializeErr, ErrProviderCaptureFailed) {
				t.Fatalf("materialization error=%v", materializeErr)
			}
			if _, rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT provider_protection_boundary"); rollbackErr != nil {
				t.Fatal(rollbackErr)
			}
		})
	}
}

type providerFixture struct {
	subjectID, connectionID, jobID, workerRef uuid.UUID
	execution                                 dbgen.PrivacyErasureExecution
	lease                                     ExecutionLease
	category                                  string
	entryDigest                               []byte
}

func seedProviderExecutionFixture(t *testing.T, ctx context.Context, tx pgx.Tx, service string, role ProviderRole, contract, localState string) providerFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	subjectID, actorID, executorID, requestID, executionID, jobID, checkpointID, leaseID, attemptID, connectionID, workerRef := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	policyVersion := "provider-test-" + uuid.NewString()
	entryDigest := bytes.Repeat([]byte{2}, 32)
	planDigest := bytes.Repeat([]byte{3}, 32)
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Provider test',$2,'hash','1990-01-01'),($3,'Provider actor',$4,'hash','1990-01-01'),($5,'Provider executor',$6,'hash','1990-01-01')`, []any{subjectID, subjectID.String() + "@example.test", actorID, actorID.String() + "@example.test", executorID, executorID.String() + "@example.test"}},
		{`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, []any{actorID}},
		{`INSERT INTO privacy_executor_grants(user_id,granted_by,granted_at) VALUES($1,$2,$3)`, []any{executorID, actorID, now.Add(-time.Hour)}},
		{`INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,working_retention_days,adopted_at,adopted_by) VALUES($1,'[]','privacy-erasure-executor/v2','privacy-erasure-plan/v2',90,$2,$3)`, []any{policyVersion, now.Add(-time.Hour), actorID}},
		{`INSERT INTO data_erasure_requests(id,public_ref,idempotency_key,subject_user_id,requester_user_id,subject_kind,scope_kind,categories,status,version,received_at,due_at,decision_code,decided_by,decided_at,policy_version,policy_snapshot,updated_at)
		 VALUES($1,$2,$3,$4,$4,'SELF','CATEGORIES',ARRAY['external-provider'],'AWAITING_EXECUTION',2,$5::timestamptz,$5::timestamptz+interval '30 days','APPROVED',$6,$5::timestamptz,$7,'{}',$5::timestamptz)`, []any{requestID, uuid.New(), uuid.New(), subjectID, now, actorID, policyVersion}},
		{`INSERT INTO privacy_request_execution_plans(request_id,policy_version,executor_version,schema_version,plan,plan_sha256,created_at)
		 VALUES($1,$2::text,'privacy-erasure-executor/v2','privacy-erasure-plan/v2',jsonb_build_object('policy_version',$2::text,'executor_version','privacy-erasure-executor/v2','schema_version','privacy-erasure-plan/v2'),$3,$4)`, []any{requestID, policyVersion, planDigest, now}},
		{`INSERT INTO privacy_erasure_executions(id,request_id,plan_sha256,executor_version,schema_version,request_version_at_start,status,started_by_ref,accepted_at,started_at,updated_at)
		 VALUES($1,$2,$3,'privacy-erasure-executor/v2','privacy-erasure-plan/v2',2,'RUNNING',$4,$5,$5,$5)`, []any{executionID, requestID, planDigest, actorID, now}},
		{`INSERT INTO privacy_erasure_category_jobs(id,execution_id,plan_entry_position,entry_sha256,category_key,purpose_code,status,next_attempt_at,lease_epoch,attempt_count,created_at,updated_at)
		 VALUES($1,$2,1,$3,'external-provider','EXTERNAL_ERASURE','LEASED',$4,1,1,$4,$4)`, []any{jobID, executionID, entryDigest, now}},
		{`INSERT INTO privacy_erasure_job_checkpoints(id,job_id,operation_position,operation_code,action_version,created_at) VALUES($1,$2,1,'PROVIDER_RECIPIENT_NOTIFY','v1',$3)`, []any{checkpointID, jobID, now}},
		{`INSERT INTO privacy_erasure_job_leases(id,job_id,epoch,worker_ref,acquired_at,heartbeat_at,expires_at) VALUES($1,$2,1,$3,$4::timestamptz,$4::timestamptz,$4::timestamptz+interval '10 minutes')`, []any{leaseID, jobID, workerRef, now}},
		{`INSERT INTO privacy_erasure_job_attempts(id,job_id,lease_id,lease_epoch,attempt_number,started_at) VALUES($1,$2,$3,1,1,$4)`, []any{attemptID, jobID, leaseID, now}},
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("fixture statement failed: %v: %s", err, statement.sql)
		}
	}
	activatePrivacyIntegrationFixture(t, ctx, tx, actorID, executorID, policyVersion)
	registryDigest := bytes.Repeat([]byte{7}, 32)
	credentialKey, credential := any(nil), any(nil)
	syncEnabled := false
	if localState == "ACTIVE" {
		credentialKey, credential, syncEnabled = "source-credential-key", []byte("minimum-provider-credential"), true
	}
	_, err := tx.Exec(ctx, `INSERT INTO privacy_protected.provider_connections(id,subject_user_id,service_code,provider_role,provider_contract_version,registry_evidence_key_id,registry_evidence_digest,target_key_id,target_opaque,credential_key_id,credential_opaque,state,sync_enabled,webhook_enabled,reconnect_enabled,created_at,updated_at)
	 VALUES($1,$2,$3,$4,$5,'registry-evidence-2026',$6,'source-target-key',$7,$8,$9,$10,$11,$11,$11,$12,$12)`, connectionID, subjectID, service, string(role), contract, registryDigest, []byte("remote-account-secret"), credentialKey, credential, localState, syncEnabled, now)
	if err != nil {
		t.Fatal(err)
	}
	execution := dbgen.PrivacyErasureExecution{ID: executionID, RequestID: requestID, PlanSha256: planDigest, ExecutorVersion: SupportedExecutorVersion, SchemaVersion: SupportedPlanSchemaVersion, RequestVersionAtStart: 2}
	jobRow := dbgen.PrivacyErasureCategoryJob{ID: jobID, ExecutionID: executionID, PlanEntryPosition: 1, EntrySha256: entryDigest, CategoryKey: "external-provider", PurposeCode: "EXTERNAL_ERASURE", Status: "LEASED", LeaseEpoch: 1, AttemptCount: 1}
	return providerFixture{subjectID: subjectID, connectionID: connectionID, jobID: jobID, workerRef: workerRef, execution: execution,
		lease: ExecutionLease{Job: ExecutionJob{PrivacyErasureCategoryJob: jobRow, ActiveLeaseID: leaseID, ActiveAttemptID: attemptID, WorkerRef: workerRef}}, category: "external-provider", entryDigest: entryDigest}
}

func providerExecutionPlanFixture(category string, entryDigest []byte) ExecutionPlan {
	_ = entryDigest
	return ExecutionPlan{ExecutorVersion: SupportedExecutorVersion, SchemaVersion: SupportedPlanSchemaVersion,
		Entries: []ExecutionPlanEntry{{Category: category, Purpose: "EXTERNAL_ERASURE", ActionVersion: SupportedActionVersion, Operations: []string{"PROVIDER_RECIPIENT_NOTIFY"}}}}
}
