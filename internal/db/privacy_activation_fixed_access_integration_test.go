//go:build integration

package db

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPrivacyActivationFixedAccessSurface(t *testing.T) {
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

	var snapshotDefinition, lockDefinition, evidenceDefinition string
	var v20, publicSnapshot, publicLock bool
	if err = conn.QueryRow(ctx, `SELECT
		pg_get_functiondef('privacy_activation_snapshot()'::regprocedure),
		pg_get_functiondef('privacy_activation_lock()'::regprocedure),
		pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure),
		EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v20_check'),
		EXISTS(SELECT 1 FROM pg_proc routine CROSS JOIN LATERAL aclexplode(COALESCE(routine.proacl,acldefault('f',routine.proowner))) acl
		 WHERE routine.oid='privacy_activation_snapshot()'::regprocedure AND acl.grantee=0 AND acl.privilege_type='EXECUTE'),
		EXISTS(SELECT 1 FROM pg_proc routine CROSS JOIN LATERAL aclexplode(COALESCE(routine.proacl,acldefault('f',routine.proowner))) acl
		 WHERE routine.oid='privacy_activation_lock()'::regprocedure AND acl.grantee=0 AND acl.privilege_type='EXECUTE')`).
		Scan(&snapshotDefinition, &lockDefinition, &evidenceDefinition, &v20, &publicSnapshot, &publicLock); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshotDefinition, "SECURITY DEFINER") ||
		!strings.Contains(snapshotDefinition, "SET search_path TO 'pg_catalog', 'public'") ||
		!strings.Contains(lockDefinition, "SECURITY DEFINER") ||
		!strings.Contains(lockDefinition, "FOR UPDATE OF activation") ||
		!strings.Contains(evidenceDefinition, baselineIncludesThrough) ||
		!v20 || publicSnapshot || publicLock {
		t.Fatalf("fixed activation surface snapshot=%q lock=%q v20=%t public_snapshot=%t public_lock=%t", snapshotDefinition, lockDefinition, v20, publicSnapshot, publicLock)
	}
}

func TestPrivacyActivationLockHoldsUntilTransactionEnd(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	locker, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close(ctx)
	contender, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close(ctx)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	actorID := uuid.New()
	policyVersion := "activation-lock-" + suffix
	if _, err = locker.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Activation lock fixture',$2,'unused','1990-01-01')`, actorID, "activation-lock-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = locker.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue) VALUES($1,'[]'::jsonb)`, policyVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = locker.Exec(ctx, `INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by)
		VALUES(true,$1,false,false,$2) ON CONFLICT(singleton) DO UPDATE SET policy_version=EXCLUDED.policy_version,enabled=false,fulfilment_ready=false,approval_id=NULL,updated_by=EXCLUDED.updated_by,updated_at=clock_timestamp()`, policyVersion, actorID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cleanupErr := pgx.Connect(context.Background(), dsn)
		if cleanupErr != nil {
			return
		}
		defer cleanup.Close(context.Background())
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM privacy_request_activation WHERE singleton AND policy_version=$1`, policyVersion)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM privacy_request_policies WHERE version=$1`, policyVersion)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actorID)
	})

	lockTx, err := locker.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback(ctx)
	var lockedPolicyVersion string
	var enabled, fulfilmentReady bool
	var approvalID uuid.UUID
	if err = lockTx.QueryRow(ctx, `SELECT policy_version,enabled,fulfilment_ready,approval_id FROM privacy_activation_lock()`).
		Scan(&lockedPolicyVersion, &enabled, &fulfilmentReady, &approvalID); err != nil {
		t.Fatal(err)
	}

	updateTx, err := contender.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer updateTx.Rollback(ctx)
	if _, err = updateTx.Exec(ctx, `SET LOCAL lock_timeout='150ms'`); err != nil {
		t.Fatal(err)
	}
	_, err = updateTx.Exec(ctx, `UPDATE privacy_request_activation SET updated_at=updated_at WHERE singleton`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("contending activation update error=%v, want lock timeout", err)
	}
}
