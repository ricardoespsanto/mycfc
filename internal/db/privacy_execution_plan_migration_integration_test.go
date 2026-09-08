//go:build integration

package db

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPrivacyExecutionPlanForwardMigrationAppliesToPriorSchema(t *testing.T) {
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
	schemaName := "privacy_plan_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	schema := pgx.Identifier{schemaName}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, dropErr := conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); dropErr != nil {
			t.Error(dropErr)
		}
	}()
	if _, err = conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	priorSchema := `
CREATE TABLE users(id uuid PRIMARY KEY,is_active boolean NOT NULL DEFAULT true,is_dependent boolean NOT NULL DEFAULT false,date_of_birth date);
CREATE TABLE platform_roles(id uuid PRIMARY KEY,code text NOT NULL);
CREATE TABLE user_platform_roles(user_id uuid NOT NULL,role_id uuid NOT NULL);
CREATE TABLE privacy_request_policies(
 version varchar(80) PRIMARY KEY,category_catalogue jsonb NOT NULL,account_closure_enabled boolean NOT NULL DEFAULT false,
 working_retention_days integer,response_months integer NOT NULL DEFAULT 1,extension_months integer NOT NULL DEFAULT 2,
 adopted_at timestamptz,adopted_by uuid,created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE data_erasure_requests(
 id uuid PRIMARY KEY,status text NOT NULL,evidence_expires_at timestamptz,working_expires_at timestamptz,working_erased_at timestamptz,
 decision_explanation text NOT NULL DEFAULT '',category_decisions jsonb NOT NULL DEFAULT '[]',categories text[] NOT NULL DEFAULT '{}',
 policy_snapshot jsonb,policy_version varchar(80),identity_verified_at timestamptz,identity_verified_by uuid,identity_method text,
 representation_verified_at timestamptz,representation_verified_by uuid,representation_method text,representation_guardian_id uuid,
 representation_relationship_updated_at timestamptz,representation_conflict boolean NOT NULL DEFAULT false,
 requester_user_id uuid,subject_user_id uuid,claimed_by uuid,decided_by uuid,closed_at timestamptz
);
CREATE TABLE email_outbox(privacy_request_id uuid);
CREATE TABLE privacy_request_dependant_resolutions(request_id uuid NOT NULL,related_request_id uuid);
CREATE TABLE data_erasure_request_events(request_id uuid NOT NULL);
CREATE TABLE privacy_request_auth_limits(bucket text PRIMARY KEY,window_start timestamptz NOT NULL);
CREATE TABLE privacy_request_maintenance_events(actor_ref uuid NOT NULL,occurred_at timestamptz NOT NULL,working_records integer NOT NULL,evidence_records integer NOT NULL);
CREATE FUNCTION prevent_privacy_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME = 'data_erasure_request_events' AND TG_OP = 'DELETE' AND EXISTS (
  SELECT 1 FROM data_erasure_requests WHERE id=OLD.request_id AND status IN ('CANCELLED','REFUSED') AND evidence_expires_at <= now()
 ) THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'privacy audit events are append-only';
END; $$;
CREATE TRIGGER privacy_case_events_immutable BEFORE UPDATE OR DELETE ON data_erasure_request_events FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();`
	if _, err = conn.Exec(ctx, priorSchema); err != nil {
		t.Fatal(err)
	}
	actor, expiredRequest, heldRequest := uuid.New(), uuid.New(), uuid.New()
	role := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,date_of_birth) VALUES($1,'1990-01-01')`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO platform_roles(id,code) VALUES($1,'ADMIN')`, role); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) VALUES($1,$2)`, actor, role); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,working_retention_days,adopted_at,adopted_by) VALUES('legacy-policy','[]',90,now(),$1)`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO data_erasure_requests(id,status,evidence_expires_at,working_expires_at,decision_explanation,category_decisions,categories,policy_snapshot,policy_version,requester_user_id,subject_user_id,closed_at)
 VALUES($2,'REFUSED',now()-interval '1 day',now()-interval '1 day','legacy working data','[]','{identity-core}','{}','legacy-policy',$1,$1,now()-interval '2 years'),
	       ($3,'REFUSED',now()-interval '1 day',now()-interval '1 day','held working data','[]','{identity-core}','{}','legacy-policy',$1,$1,now()-interval '2 years')`, actor, expiredRequest, heldRequest); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO data_erasure_request_events(request_id) VALUES($1),($2)`, expiredRequest, heldRequest); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFiles.ReadFile("migrations/202609080001_privacy_execution_plans.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply forward migration: %v", err)
	}
	var planTable, exceptionTable bool
	if err = conn.QueryRow(ctx, `SELECT to_regclass('privacy_request_execution_plans') IS NOT NULL,to_regclass('privacy_request_retention_exceptions') IS NOT NULL`).Scan(&planTable, &exceptionTable); err != nil {
		t.Fatal(err)
	}
	if !planTable || !exceptionTable {
		t.Fatalf("plan table=%v exception table=%v", planTable, exceptionTable)
	}
	var digestType string
	if err = conn.QueryRow(ctx, `SELECT data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='privacy_request_execution_plans' AND column_name='plan_sha256'`).Scan(&digestType); err != nil {
		t.Fatal(err)
	}
	if digestType != "bytea" {
		t.Fatalf("plan_sha256 type=%q", digestType)
	}
	var legacyPolicies, legacyRequests int
	if err = conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM privacy_request_policies WHERE version='legacy-policy'),(SELECT count(*) FROM data_erasure_requests WHERE id IN ($1,$2))`, expiredRequest, heldRequest).Scan(&legacyPolicies, &legacyRequests); err != nil {
		t.Fatal(err)
	}
	if legacyPolicies != 1 || legacyRequests != 2 {
		t.Fatalf("forward migration lost legacy data: policies=%d requests=%d", legacyPolicies, legacyRequests)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO privacy_request_execution_plans(request_id,policy_version,executor_version,schema_version,plan,plan_sha256,created_at)
VALUES($1,'legacy-policy','privacy-erasure-executor/v1','privacy-erasure-plan/v1',
	 '{"policy_version":"legacy-policy","executor_version":"privacy-erasure-executor/v1","schema_version":"privacy-erasure-plan/v1"}',decode(repeat('00',32),'hex'),now())`, heldRequest); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO privacy_request_retention_exceptions(request_id,owner_ref,actor_ref,category_key,purpose_code,retained_field_codes,evidence_ref,reason_code,review_at,expires_at,created_at)
VALUES($1,$2,$2,'identity-core','ACCOUNT_IDENTITY','{users.id}','legal-file-001','LEGAL_HOLD',now()+interval '1 day',now()+interval '2 days',now())`, heldRequest, actor); err != nil {
		t.Fatal(err)
	}
	var workingRecords, evidenceRecords int
	if err = conn.QueryRow(ctx, `SELECT working_records,evidence_records FROM expire_privacy_request_records($1)`, actor).Scan(&workingRecords, &evidenceRecords); err != nil {
		t.Fatalf("execute migrated expiry function: %v", err)
	}
	if workingRecords != 2 || evidenceRecords != 1 {
		t.Fatalf("expiry counts working=%d evidence=%d", workingRecords, evidenceRecords)
	}
	var expiredExists, heldExists, heldScrubbed, heldPlan, heldException bool
	if err = conn.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM data_erasure_requests WHERE id=$1),
 EXISTS(SELECT 1 FROM data_erasure_requests WHERE id=$2),
 EXISTS(SELECT 1 FROM data_erasure_requests WHERE id=$2 AND working_erased_at IS NOT NULL AND decision_explanation='' AND subject_user_id IS NULL),
 EXISTS(SELECT 1 FROM privacy_request_execution_plans WHERE request_id=$2),
 EXISTS(SELECT 1 FROM privacy_request_retention_exceptions WHERE request_id=$2)`, expiredRequest, heldRequest).Scan(&expiredExists, &heldExists, &heldScrubbed, &heldPlan, &heldException); err != nil {
		t.Fatal(err)
	}
	if expiredExists || !heldExists || !heldScrubbed || !heldPlan || !heldException {
		t.Fatalf("migrated expiry result expired=%v held=%v scrubbed=%v plan=%v exception=%v", expiredExists, heldExists, heldScrubbed, heldPlan, heldException)
	}
}
