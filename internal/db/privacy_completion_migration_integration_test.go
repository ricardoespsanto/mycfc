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

func TestPrivacyCompletionForwardMigrationPreservesDisabledActivation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	const marker = "-- #248 completion, exceptional requeue and evidence-bound activation control."
	migration, err := migrationFiles.ReadFile("migrations/202609100011_privacy_completion_control.sql")
	if err != nil {
		t.Fatal(err)
	}
	markerIndex := strings.LastIndex(baselineSchema, marker)
	nextMarkerIndex := strings.LastIndex(baselineSchema, "-- #247 replay hardening:")
	if markerIndex < 0 || nextMarkerIndex <= markerIndex || baselineSchema[markerIndex:nextMarkerIndex] != string(migration) {
		t.Fatal("completion migration is not the exact baseline segment before replay hardening")
	}
	priorBaseline := baselineSchema[:markerIndex]
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	schemaName := "privacy_completion_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	protectedSchemaName := "privacy_completion_protected_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	schema := pgx.Identifier{schemaName}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		_, _ = conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{protectedSchemaName}.Sanitize()+" CASCADE")
	}()
	if _, err = conn.Exec(ctx, "SET search_path TO "+schema+",public"); err != nil {
		t.Fatal(err)
	}
	isolate := func(sql string) string {
		sql = strings.ReplaceAll(sql, "public.", schemaName+".")
		sql = strings.ReplaceAll(sql, "pg_catalog, public", "pg_catalog, "+schemaName+", public")
		sql = strings.ReplaceAll(sql, "pg_catalog,public", "pg_catalog,"+schemaName+",public")
		return strings.ReplaceAll(sql, "privacy_protected", protectedSchemaName)
	}
	if _, err = conn.PgConn().Exec(ctx, isolate(priorBaseline)).ReadAll(); err != nil {
		t.Fatalf("create exact 010 baseline: %v", err)
	}
	adminID := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Completion migration admin',$2,'hash','1990-01-01')`, adminID, adminID.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,working_retention_days,adopted_at,adopted_by)
		VALUES('completion-forward','[]','privacy-erasure-executor/v2','privacy-erasure-plan/v2',90,clock_timestamp(),$1)`, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by) VALUES(true,'completion-forward',true,true,$1)`, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.PgConn().Exec(ctx, isolate(string(migration))).ReadAll(); err != nil {
		t.Fatalf("apply completion forward migration: %v", err)
	}
	var enabled, ready bool
	var approvalID *uuid.UUID
	var userCount, tableCount, deactivationEvents int
	if err = conn.QueryRow(ctx, `SELECT enabled,fulfilment_ready,approval_id FROM privacy_request_activation WHERE singleton`).Scan(&enabled, &ready, &approvalID); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM users WHERE id=$1`, adminID).Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name IN ('privacy_erasure_completion_manifests','privacy_activation_evidence')`, schemaName).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM privacy_request_activation_events WHERE policy_version='completion-forward' AND NOT enabled AND NOT fulfilment_ready`).Scan(&deactivationEvents); err != nil {
		t.Fatal(err)
	}
	if enabled || ready || approvalID != nil || userCount != 1 || tableCount != 2 || deactivationEvents != 1 {
		t.Fatalf("forward state enabled=%t ready=%t approval=%v users=%d tables=%d deactivations=%d", enabled, ready, approvalID, userCount, tableCount, deactivationEvents)
	}
}
