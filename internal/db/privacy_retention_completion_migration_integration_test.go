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

func TestPrivacyRetentionCompletionForwardMigrationPreservesPriorRows(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	schemaName, protectedName, disableName, guardianOpsName := "retention_migration_"+suffix, "retention_protected_"+suffix, "retention_disable_"+suffix, "retention_guardian_ops_"+suffix
	schema, protected, disable, guardianOps := pgx.Identifier{schemaName}.Sanitize(), pgx.Identifier{protectedName}.Sanitize(), pgx.Identifier{disableName}.Sanitize(), pgx.Identifier{guardianOpsName}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	defer conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+protected+" CASCADE")
	defer conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+disable+" CASCADE")
	defer conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+guardianOps+" CASCADE")
	if _, err = conn.Exec(ctx, "SET search_path TO "+schema+",public"); err != nil {
		t.Fatal(err)
	}
	isolate := func(sql string) string {
		sql = strings.ReplaceAll(sql, "public.", schemaName+".")
		sql = strings.ReplaceAll(sql, "pg_catalog, public", "pg_catalog, "+schemaName+", public")
		sql = strings.ReplaceAll(sql, "pg_catalog,public", "pg_catalog,"+schemaName+",public")
		sql = strings.ReplaceAll(sql, "privacy_protected", protectedName)
		sql = strings.ReplaceAll(sql, "privacy_disable", disableName)
		return strings.ReplaceAll(sql, "guardian_ops", guardianOpsName)
	}
	if _, err = conn.PgConn().Exec(ctx, isolate(baselineSchema)).ReadAll(); err != nil {
		t.Fatalf("create isolated baseline: %v", err)
	}
	if _, err = conn.Exec(ctx, pre010RetentionRollback); err != nil {
		t.Fatalf("reconstruct exact pre-010 surface: %v", err)
	}
	userID, consentID := uuid.New(), uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Legacy consent',$2,'hash','1990-01-01')`, userID, "legacy-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO consent_forms(id,user_id,granted_by_user_id,consent_type,document_version,document_sha256,is_accepted,ip_address,user_agent)
	 VALUES($1,$2,$2,'Uso_Imagem','legacy',repeat('a',64),true,'192.0.2.1','legacy-agent')`, consentID, userID); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFiles.ReadFile("migrations/202609100010_privacy_retention_completion.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, isolate(string(migration))); err != nil {
		t.Fatalf("apply exact 009 to 010 migration: %v", err)
	}
	var legacyPreserved, hasColumns, hasRun, hasStatus bool
	if err = conn.QueryRow(ctx, `SELECT
	 EXISTS(SELECT 1 FROM consent_forms WHERE id=$1 AND ceased_at IS NULL AND ip_address='192.0.2.1' AND user_agent='legacy-agent'),
	 (SELECT count(*)=3 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='consent_forms' AND column_name IN('ceased_at','cessation_reason','evidence_expires_at')),
	 to_regprocedure(current_schema()||'.privacy_retention_run(uuid,integer)') IS NOT NULL,
	 to_regprocedure(current_schema()||'.privacy_retention_status()') IS NOT NULL`, consentID).Scan(&legacyPreserved, &hasColumns, &hasRun, &hasStatus); err != nil {
		t.Fatal(err)
	}
	if !legacyPreserved || !hasColumns || !hasRun || !hasStatus {
		t.Fatalf("legacy=%v columns=%v run=%v status=%v", legacyPreserved, hasColumns, hasRun, hasStatus)
	}
}

const pre010RetentionRollback = `
DROP FUNCTION privacy_retention_status();
DROP FUNCTION privacy_retention_run(uuid,integer);
CREATE FUNCTION privacy_retention_run(uuid,integer) RETURNS TABLE(
 run_id uuid,sessions_deleted integer,tokens_deleted integer,outbox_stopped integer,outbox_payloads_deleted integer,
 outbox_evidence_deleted integer,consent_network_scrubbed integer,event_responses_deleted integer,
 announcement_deliveries_deleted integer,suggestions_deleted integer,privacy_working_scrubbed integer,auth_limits_deleted integer
) LANGUAGE sql AS $$ SELECT gen_random_uuid(),0,0,0,0,0,0,0,0,0,0,0 $$;
ALTER TABLE privacy_retention_runs DROP COLUMN consent_evidence_deleted,DROP COLUMN audit_events_pseudonymized,DROP COLUMN repair_attachments_queued;
DROP FUNCTION privacy_retention_pseudonymize_audit(integer);
DROP FUNCTION privacy_retention_queue_repair_attachments(integer);
DROP TRIGGER member_profiles_photo_active_consent ON member_profiles;
DROP FUNCTION privacy_profile_photo_requires_active_consent();
DROP TRIGGER users_consent_cessation ON users;
DROP FUNCTION privacy_consent_cease_on_erasure();
DROP FUNCTION privacy_consent_cease(uuid,text,uuid,text,timestamptz);
DROP TRIGGER consent_forms_cessation_immutable ON consent_forms;
DROP FUNCTION privacy_consent_cessation_immutable();
DROP INDEX consent_forms_evidence_expiry_idx;
ALTER TABLE consent_forms DROP CONSTRAINT consent_cessation_complete,
 DROP COLUMN ceased_at,DROP COLUMN cessation_reason,DROP COLUMN evidence_expires_at;
ALTER TABLE privacy_protected.object_upload_intent_events DROP CONSTRAINT object_upload_intent_events_reason_code_check;
ALTER TABLE privacy_protected.object_upload_intent_events ADD CONSTRAINT object_upload_intent_events_reason_code_check CHECK(reason_code IN(
 'UPLOAD_RESERVED','PUT_ACKNOWLEDGED','POINTER_ATTACHED','PUT_AMBIGUOUS','PUT_FAILED','ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED','STALE_TIMEOUT','CLEANUP_CONFIRMED'));
ALTER TABLE privacy_protected.object_upload_intent_events DROP CONSTRAINT object_upload_intent_events_check;
ALTER TABLE privacy_protected.object_upload_intent_events ADD CONSTRAINT object_upload_intent_events_check CHECK(
 (status='PREPARED' AND reason_code='UPLOAD_RESERVED') OR(status='PUT_CONFIRMED' AND reason_code='PUT_ACKNOWLEDGED') OR(status='ATTACHED' AND reason_code='POINTER_ATTACHED')
 OR(status='CLEANUP_REQUIRED' AND reason_code IN('PUT_AMBIGUOUS','PUT_FAILED','ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED','STALE_TIMEOUT'))
 OR(status='ABSENCE_VERIFIED' AND reason_code='CLEANUP_CONFIRMED'));
`
