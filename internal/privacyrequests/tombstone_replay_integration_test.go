//go:build integration

package privacyrequests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

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

	subject, unrelated := uuid.New(), uuid.New()
	for id, email := range map[uuid.UUID]string{
		subject:   "restore-subject-" + uuid.NewString() + "@example.test",
		unrelated: "restore-unrelated-" + uuid.NewString() + "@example.test",
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
		if _, err = tx.Exec(ctx, `INSERT INTO staff_grants(user_id,capability,granted_by_id) VALUES($1,'MODERATOR',$2)`, id, unrelated); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_reviewer_grants(user_id,granted_by,granted_at) VALUES($1,$2,clock_timestamp())`, id, unrelated); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO privacy_executor_grants(user_id,granted_by,granted_at) VALUES($1,$2,clock_timestamp())`, id, unrelated); err != nil {
			t.Fatal(err)
		}
	}

	protector, privateKey := tombstoneProtectorFixture(t)
	record := tombstoneFixture()
	record.SubjectUserID = subject
	record.ExecutionStart = time.Now().UTC()
	record.Replay.Operations = []string{"AUTH_ACCESS_REVOKE", "AUTH_TOKEN_DELETE", "PROFILE_IDENTITY_DELETE", "IDENTITY_CLEAR"}
	sealed, err := protector.Seal(record)
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := AuthenticateReplayTombstone(privateKey, listedTombstoneFixture(sealed))
	if err != nil {
		t.Fatal(err)
	}
	worker := TombstoneReplayWorker{Store: tx, WorkerRef: uuid.New()}
	result, err := worker.Replay(ctx, authenticated)
	if err != nil {
		t.Fatal(err)
	}
	runID := result.RunID
	repeated, err := (TombstoneReplayWorker{Store: tx, WorkerRef: uuid.New()}).Replay(ctx, authenticated)
	if err != nil || repeated.RunID != runID || !repeated.AlreadyApplied || result.AlreadyApplied {
		t.Fatalf("idempotent replay result=%+v initial=%+v err=%v", repeated, result, err)
	}

	var erased, subjectProfile, subjectToken, unrelatedProfile, unrelatedToken bool
	var subjectEmail *string
	var subjectName, runStatus string
	var checkpointCount, succeededCount, subjectActiveGrants, unrelatedActiveGrants, replayGrantEvents int
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
	if !erased || subjectEmail != nil || subjectName != "Conta eliminada" || subjectProfile || subjectToken || !unrelatedProfile || !unrelatedToken ||
		runStatus != "SUCCEEDED" || checkpointCount != 4 || succeededCount != 4 || subjectActiveGrants != 0 || unrelatedActiveGrants != 3 || replayGrantEvents != 3 {
		t.Fatalf("scope/result erased=%t email=%v name=%q profiles=%t/%t tokens=%t/%t run=%s checkpoints=%d/%d grants=%d/%d events=%d",
			erased, subjectEmail, subjectName, subjectProfile, unrelatedProfile, subjectToken, unrelatedToken, runStatus, checkpointCount, succeededCount,
			subjectActiveGrants, unrelatedActiveGrants, replayGrantEvents)
	}

	conflicting, err := protector.Seal(record)
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
