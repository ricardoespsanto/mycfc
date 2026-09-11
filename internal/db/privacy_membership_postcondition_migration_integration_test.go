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

func TestMembershipPostconditionSealingSerializesInflightMutation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	first, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close(ctx)
	second, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(ctx)

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	subject, actor, requestID, requestRef, executionID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	programmeID, seasonID, membershipID, principalID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	effective := time.Date(2026, time.September, 10, 23, 30, 0, 0, time.UTC)
	digest := bytes.Repeat([]byte{9}, 32)
	for _, account := range []struct {
		id    uuid.UUID
		email string
	}{{subject, "seal-subject-" + suffix + "@example.test"}, {actor, "seal-actor-" + suffix + "@example.test"}} {
		if _, err = first.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Seal race',$2,'hash','1990-01-01')`, account.id, account.email); err != nil {
			t.Fatal(err)
		}
	}
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,adopted_at,adopted_by) VALUES($1,'[]','privacy-erasure-executor/v2','privacy-erasure-plan/v2',$2,$3)`, []any{"seal-" + suffix, effective.Add(-24 * time.Hour), actor}},
		{`INSERT INTO data_erasure_requests(id,public_ref,idempotency_key,subject_user_id,requester_user_id,subject_kind,scope_kind,status,version,received_at,due_at,decided_by,decided_at,decision_code,policy_version,policy_snapshot,updated_at) VALUES($1,$2,$3,$4,$4,'SELF','ACCOUNT_CLOSURE','PROCESSING',2,$5,$5::timestamptz+interval '1 day',$6,$5,'APPROVED',$7,'{}',$5)`, []any{requestID, requestRef, uuid.New(), subject, effective, actor, "seal-" + suffix}},
		{`INSERT INTO privacy_request_execution_plans(request_id,policy_version,executor_version,schema_version,plan,plan_sha256,created_at) VALUES($1,$2,'privacy-erasure-executor/v2','privacy-erasure-plan/v2','{}',$3,$4)`, []any{requestID, "seal-" + suffix, digest, effective}},
		{`INSERT INTO privacy_erasure_executions(id,request_id,plan_sha256,executor_version,schema_version,request_version_at_start,status,started_by_ref,accepted_at,started_at,finished_at,updated_at) VALUES($1,$2,$3,'privacy-erasure-executor/v2','privacy-erasure-plan/v2',2,'SUCCEEDED',$4,$5,$5,$5,$5)`, []any{executionID, requestID, digest, actor, effective}},
		{`INSERT INTO programmes(id,code,name_pt) VALUES($1,$2,'Programa seal')`, []any{programmeID, "Seal" + suffix[:10]}},
		{`INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Época seal',$3::date-2,$3::date-1)`, []any{seasonID, "S" + suffix[:10], effective}},
		{`INSERT INTO privacy_pseudonymous_principals(id,purpose) VALUES($1,'MEMBERSHIP')`, []any{principalID}},
		{`INSERT INTO user_memberships(id,user_id,principal_id,season_id,programme_id,starts_on,ends_on) VALUES($1,NULL,$2,$3,$4,$5::date-2,$5::date-1)`, []any{membershipID, principalID, seasonID, programmeID, effective}},
		{`INSERT INTO privacy_protected.membership_history_source_captures(execution_id) VALUES($1)`, []any{executionID}},
		{`INSERT INTO privacy_protected.membership_history_source_rows(execution_id,membership_id) VALUES($1,$2)`, []any{executionID, membershipID}},
	}
	for _, statement := range statements {
		if _, err = first.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = first.Exec(ctx, `UPDATE users SET name='Conta eliminada',email=NULL,email_verified_at=NULL,minor_login_id=NULL,password_hash=NULL,
		guardian_id=NULL,is_dependent=false,date_of_birth='1900-01-01',is_active=false,leaderboard_visible=false,
		erased_at=$2,erasure_execution_id=$3 WHERE id=$1`, subject, effective, executionID); err != nil {
		t.Fatal(err)
	}
	if _, err = first.Exec(ctx, "SET TIME ZONE 'Europe/Lisbon'"); err != nil {
		t.Fatal(err)
	}
	defer first.Exec(context.Background(), "SET TIME ZONE DEFAULT")
	var effectiveDate time.Time
	if err = first.QueryRow(ctx, `SELECT privacy_membership_history_effective_date($1,$2)`, executionID, subject).Scan(&effectiveDate); err != nil {
		t.Fatal(err)
	}
	if effectiveDate.Format(time.DateOnly) != "2026-09-10" {
		t.Fatalf("persisted UTC membership effective date=%s", effectiveDate.Format(time.DateOnly))
	}

	mutation, err := first.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = mutation.Exec(ctx, `UPDATE user_memberships SET ends_on=ends_on-1 WHERE id=$1`, membershipID); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	sealed := make(chan error, 1)
	go func() {
		sealTx, sealErr := second.Begin(ctx)
		if sealErr != nil {
			sealed <- sealErr
			return
		}
		defer sealTx.Rollback(ctx)
		close(started)
		_, sealErr = sealTx.Exec(ctx, `SELECT privacy_membership_history_lock_source($1)`, executionID)
		if sealErr == nil {
			_, sealErr = sealTx.Exec(ctx, `INSERT INTO privacy_protected.membership_history_source_postconditions(execution_id,effective_at,contract,postcondition_sha256,membership_count,variation_count,canonical_size)
			 SELECT $1,$2,computed.contract,computed.postcondition_sha256,computed.membership_count,computed.variation_count,computed.canonical_size FROM privacy_membership_history_compute_source($1,$2) computed`, executionID, effective)
		}
		if sealErr == nil {
			sealErr = sealTx.Commit(ctx)
		}
		sealed <- sealErr
	}()
	<-started
	select {
	case sealErr := <-sealed:
		t.Fatalf("sealing did not wait for in-flight mutation: %v", sealErr)
	case <-time.After(150 * time.Millisecond):
	}
	if err = mutation.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case sealErr := <-sealed:
		if sealErr != nil {
			t.Fatal(sealErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sealing remained blocked after mutation committed")
	}
	var ready bool
	if err = first.QueryRow(ctx, `SELECT privacy_membership_history_source_postcondition_ready($1)`, executionID).Scan(&ready); err != nil || !ready {
		t.Fatalf("sealed postcondition ready=%t err=%v", ready, err)
	}
	if _, err = first.Exec(ctx, `UPDATE user_memberships SET ends_on=ends_on-1 WHERE id=$1`, membershipID); err == nil {
		t.Fatal("mutation after sealing unexpectedly succeeded")
	}
}

func TestPrivacyMembershipPostconditionForwardMigrationFrom014(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	const marker = "-- #247 closure-v4:"
	migration, err := migrationFiles.ReadFile("migrations/202609100015_privacy_membership_postcondition.sql")
	if err != nil {
		t.Fatal(err)
	}
	markerIndex := strings.LastIndex(baselineSchema, marker)
	if markerIndex < 0 {
		t.Fatal("membership-postcondition migration marker missing from baseline")
	}
	const currentCutoff = "public.privacy_membership_history_effective_date(execution_ref,subject_ref)"
	baselineSegment := baselineSchema[markerIndex:]
	// The baseline uses OR REPLACE for the four wrappers renamed in this
	// migration because sqlc parses the monolithic schema without modelling
	// ALTER FUNCTION ... RENAME. PostgreSQL receives the exact forward file.
	for _, function := range []string{
		"privacy_worker_execute_checkpoint", "privacy_restore_verify_operation",
		"privacy_restore_begin_replay_hardened", "privacy_restore_execute_checkpoint",
	} {
		baselineSegment = strings.Replace(baselineSegment, "CREATE OR REPLACE FUNCTION public."+function, "CREATE FUNCTION public."+function, 1)
	}
	if baselineSegment != string(migration) {
		t.Fatal("membership-postcondition migration is not the exact final baseline segment")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	schemaName := "privacy_015_forward_" + suffix
	protectedName := "privacy_015_protected_" + suffix
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
		sql = strings.ReplaceAll(sql, "namespace.nspname='public'", "namespace.nspname='"+schemaName+"'")
		return strings.ReplaceAll(sql, "privacy_protected", protectedName)
	}
	legacyBaseline := strings.ReplaceAll(baselineSchema[:markerIndex], currentCutoff, "CURRENT_DATE")
	if strings.Count(legacyBaseline, "CURRENT_DATE") < 12 || strings.Count(baselineSchema[:markerIndex], currentCutoff) != 12 {
		t.Fatal("membership cutoff predecessor fixture is not exact")
	}
	if _, err = conn.PgConn().Exec(ctx, rewrite(legacyBaseline)).ReadAll(); err != nil {
		t.Fatalf("create exact 014 baseline: %v", err)
	}
	userID := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth)
		VALUES($1,'Migration preservation',$2,'hash','1990-01-01')`, userID, userID.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "GRANT EXECUTE ON FUNCTION "+schema+".privacy_tombstone_prepare_closure_v3(uuid,uuid) TO PUBLIC"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.PgConn().Exec(ctx, rewrite(string(migration))).ReadAll(); err != nil {
		t.Fatalf("apply 014 to 015 forward migration: %v", err)
	}

	var userPreserved, sourceTable, postconditionAPI, mutationTrigger, completionRequiresV4, completionRequiresPostcondition, legacyPublicGrantRemoved, stableCutoffInstalled bool
	if err = conn.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM users WHERE id=$1),
		to_regclass($2) IS NOT NULL,
		to_regprocedure('privacy_membership_history_source_postcondition_ready(uuid)') IS NOT NULL,
		EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='user_memberships'::regclass AND tgname='user_memberships_sealed_history' AND NOT tgisinternal),
		strpos(pg_get_functiondef('privacy_completion_prepare_inner_013(uuid,uuid)'::regprocedure),'restore-tombstone-closure/v4')>0,
		strpos(pg_get_functiondef('privacy_completion_prepare_inner_013(uuid,uuid)'::regprocedure),'privacy_completion_membership_postcondition_ready')>0,
		strpos(pg_get_functiondef('privacy_worker_execute_checkpoint_without_tombstone_guard(uuid,uuid,uuid,bigint,uuid,text,text)'::regprocedure),'CURRENT_DATE')=0
		 AND regexp_count(pg_get_functiondef('privacy_worker_execute_checkpoint_without_tombstone_guard(uuid,uuid,uuid,bigint,uuid,text,text)'::regprocedure),
		  'privacy_membership_history_effective_date')=12,
		NOT EXISTS(SELECT 1 FROM pg_proc proc JOIN pg_namespace namespace ON namespace.oid=proc.pronamespace
		 CROSS JOIN LATERAL aclexplode(COALESCE(proc.proacl,acldefault('f',proc.proowner))) acl
		 WHERE namespace.nspname=$3 AND proc.proname='privacy_tombstone_prepare_closure_v3'
		  AND acl.privilege_type='EXECUTE' AND acl.grantee=0)`,
		userID, protectedName+".membership_history_source_postconditions", schemaName).Scan(
		&userPreserved, &sourceTable, &postconditionAPI, &mutationTrigger, &completionRequiresV4, &completionRequiresPostcondition, &stableCutoffInstalled, &legacyPublicGrantRemoved); err != nil {
		t.Fatal(err)
	}
	if !userPreserved || !sourceTable || !postconditionAPI || !mutationTrigger || !completionRequiresV4 || !completionRequiresPostcondition || !stableCutoffInstalled || !legacyPublicGrantRemoved {
		t.Fatalf("forward state user=%t source_table=%t postcondition_api=%t mutation_trigger=%t completion_v4=%t completion_postcondition=%t stable_cutoff=%t legacy_public_grant_removed=%t",
			userPreserved, sourceTable, postconditionAPI, mutationTrigger, completionRequiresV4, completionRequiresPostcondition, stableCutoffInstalled, legacyPublicGrantRemoved)
	}
}
