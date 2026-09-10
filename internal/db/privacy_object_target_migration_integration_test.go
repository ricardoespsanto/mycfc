//go:build integration

package db

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPrivacyObjectTargetFoundationMigrationIsAdditiveProtectedAndImmutable(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	if _, err = tx.Exec(ctx, `DROP SCHEMA IF EXISTS privacy_protected CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `
		ALTER TABLE privacy_erasure_category_jobs DROP CONSTRAINT privacy_erasure_category_jobs_target_binding_unique;
		ALTER TABLE privacy_erasure_job_checkpoints DROP CONSTRAINT privacy_erasure_job_checkpoints_target_binding_unique`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFiles.ReadFile("migrations/202609100002_privacy_object_target_foundation.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}

	var targets, digests, evidence, publicUsage bool
	if err = tx.QueryRow(ctx, `SELECT
		to_regclass('privacy_protected.object_targets') IS NOT NULL,
		to_regclass('privacy_protected.object_target_digests') IS NOT NULL,
		to_regclass('privacy_protected.object_evidence') IS NOT NULL,
		has_schema_privilege('public','privacy_protected','USAGE')`).Scan(&targets, &digests, &evidence, &publicUsage); err != nil {
		t.Fatal(err)
	}
	if !targets || !digests || !evidence || publicUsage {
		t.Fatalf("targets=%t digests=%t evidence=%t public_usage=%t", targets, digests, evidence, publicUsage)
	}

	var immutableTriggers int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM pg_trigger trigger
		JOIN pg_class relation ON relation.oid=trigger.tgrelid
		JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
		WHERE namespace.nspname='privacy_protected' AND NOT trigger.tgisinternal
		AND trigger.tgname IN ('privacy_object_targets_immutable','privacy_object_target_digests_immutable','privacy_object_evidence_immutable')`).Scan(&immutableTriggers); err != nil {
		t.Fatal(err)
	}
	if immutableTriggers != 3 {
		t.Fatalf("immutable trigger count=%d", immutableTriggers)
	}

	var publicTableAccess bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM information_schema.role_table_grants
		WHERE grantee='PUBLIC' AND table_schema='privacy_protected'
	)`).Scan(&publicTableAccess); err != nil {
		t.Fatal(err)
	}
	if publicTableAccess {
		t.Fatal("PUBLIC unexpectedly has protected table access")
	}

	userID, requestID, executionID := uuid.New(), uuid.New(), uuid.New()
	jobOne, jobTwo, checkpointOne, checkpointTwo, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	leaseID, attemptID := uuid.New(), uuid.New()
	planDigest, entryOneDigest, entryTwoDigest := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32)
	policyVersion := "protected-target-test-" + uuid.NewString()
	fixtureStatements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Protected target test',$2,'hash','1990-01-01')`, []any{userID, uuid.NewString() + "@example.test"}},
		{`INSERT INTO privacy_request_policies(version,category_catalogue) VALUES($1,'[]')`, []any{policyVersion}},
		{`INSERT INTO data_erasure_requests(id,idempotency_key,subject_user_id,requester_user_id,subject_kind,scope_kind,categories,status,version,received_at,due_at,updated_at) VALUES($1,$2,$3,$3,'SELF','CATEGORIES',ARRAY['profile-photo'],'RECEIVED',2,now(),now()+interval '1 month',now())`, []any{requestID, uuid.New(), userID}},
		{`INSERT INTO privacy_request_execution_plans(request_id,policy_version,executor_version,schema_version,plan,plan_sha256,created_at) VALUES($1,$2::varchar,'privacy-erasure-executor/v1','privacy-erasure-plan/v1',jsonb_build_object('policy_version',$2::varchar,'executor_version','privacy-erasure-executor/v1','schema_version','privacy-erasure-plan/v1'),$3,now())`, []any{requestID, policyVersion, planDigest}},
		{`INSERT INTO privacy_erasure_executions(id,request_id,plan_sha256,executor_version,schema_version,request_version_at_start,started_by_ref,accepted_at,updated_at) VALUES($1,$2,$3,'privacy-erasure-executor/v1','privacy-erasure-plan/v1',2,$4,now(),now())`, []any{executionID, requestID, planDigest, uuid.New()}},
		{`INSERT INTO privacy_erasure_category_jobs(id,execution_id,plan_entry_position,entry_sha256,category_key,purpose_code,next_attempt_at,created_at,updated_at) VALUES($1,$2,1,$3,'profile-photo','OPTIONAL_IDENTIFICATION',now(),now(),now()),($4,$2,2,$5,'object-storage','PRIVATE_MEDIA',now(),now(),now())`, []any{jobOne, executionID, entryOneDigest, jobTwo, entryTwoDigest}},
		{`INSERT INTO privacy_erasure_job_leases(id,job_id,epoch,worker_ref,acquired_at,heartbeat_at,expires_at) VALUES($1,$2,1,$3,now(),now(),now()+interval '5 minutes')`, []any{leaseID, jobOne, uuid.New()}},
		{`INSERT INTO privacy_erasure_job_attempts(id,job_id,lease_id,lease_epoch,attempt_number,started_at) VALUES($1,$2,$3,1,1,now())`, []any{attemptID, jobOne, leaseID}},
		{`INSERT INTO privacy_erasure_job_checkpoints(id,job_id,operation_position,operation_code,action_version,created_at) VALUES($1,$2,1,'OBJECT_VERSION_DELETE','v1',now()),($3,$4,1,'OBJECT_VERSION_DELETE','v1',now())`, []any{checkpointOne, jobOne, checkpointTwo, jobTwo}},
	}
	for _, statement := range fixtureStatements {
		if _, err = tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	insertTarget := `INSERT INTO privacy_protected.object_targets(id,execution_id,job_id,checkpoint_id,plan_entry_sha256,category_key,service_code,target_kind,source_kind,source_ref,operation_code,action_version,provider_contract_version,envelope_version,algorithm,encryption_key_id,encapsulation,nonce,ciphertext,created_at)
		VALUES($1,$2,$3,$4,$5,$6,'private-media','OBJECT_KEY','MEMBER_PROFILE_PHOTO',$7,'OBJECT_VERSION_DELETE','v1','s3-versioned/v1','x25519-aes256gcm-hkdfsha256/v1','X25519-HKDF-SHA256-AES-256-GCM','target-key-v1',$8,$9,$10,now())`
	if _, err = tx.Exec(ctx, `SAVEPOINT invalid_target_binding`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, insertTarget, uuid.New(), executionID, jobOne, checkpointTwo, entryOneDigest, "profile-photo", uuid.New(), bytes.Repeat([]byte{4}, 32), bytes.Repeat([]byte{5}, 12), bytes.Repeat([]byte{6}, 32)); err == nil {
		t.Fatal("cross-job checkpoint binding unexpectedly accepted")
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_target_binding`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, insertTarget, targetID, executionID, jobOne, checkpointOne, entryOneDigest, "profile-photo", uuid.New(), bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 12), bytes.Repeat([]byte{9}, 32)); err != nil {
		t.Fatalf("valid protected target rejected: %v", err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT invalid_target_digest`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.object_target_digests(target_id,execution_id,service_code,target_kind,digest_key_id,locator_digest,created_at) VALUES($1,$2,'other-service','OBJECT_KEY','digest-key-v1',$3,now())`, targetID, executionID, bytes.Repeat([]byte{10}, 32)); err == nil {
		t.Fatal("cross-service target digest unexpectedly accepted")
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_target_digest`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.object_target_digests(target_id,execution_id,service_code,target_kind,digest_key_id,locator_digest,created_at) VALUES($1,$2,'private-media','OBJECT_KEY','digest-key-v1',$3,now())`, targetID, executionID, bytes.Repeat([]byte{11}, 32)); err != nil {
		t.Fatalf("valid target digest rejected: %v", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.object_evidence(target_id,job_id,attempt_id,evidence_version,outcome_code,deleted_version_count,deleted_marker_count,list_call_count,stable_empty_check_count,transcript_key_id,transcript_digest,occurred_at) VALUES($1,$2,$3,'s3-absence/v1','ABSENCE_VERIFIED',1,1,2,2,'transcript-key-v1',$4,now())`, targetID, jobOne, attemptID, bytes.Repeat([]byte{12}, 32)); err != nil {
		t.Fatalf("valid object evidence rejected: %v", err)
	}

	assertMutationRejected := func(name, statement string, args ...any) {
		t.Helper()
		if _, err = tx.Exec(ctx, `SAVEPOINT protected_mutation`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, statement, args...); err == nil {
			t.Fatalf("%s unexpectedly succeeded", name)
		}
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT protected_mutation`); err != nil {
			t.Fatal(err)
		}
	}
	assertMutationRejected("target update", `UPDATE privacy_protected.object_targets SET created_at=created_at+interval '1 second' WHERE id=$1`, targetID)
	assertMutationRejected("target delete", `DELETE FROM privacy_protected.object_targets WHERE id=$1`, targetID)
	assertMutationRejected("digest update", `UPDATE privacy_protected.object_target_digests SET created_at=created_at+interval '1 second' WHERE target_id=$1`, targetID)
	assertMutationRejected("digest delete", `DELETE FROM privacy_protected.object_target_digests WHERE target_id=$1`, targetID)
	assertMutationRejected("evidence update", `UPDATE privacy_protected.object_evidence SET occurred_at=occurred_at+interval '1 second' WHERE target_id=$1`, targetID)
	assertMutationRejected("evidence delete", `DELETE FROM privacy_protected.object_evidence WHERE target_id=$1`, targetID)
}
