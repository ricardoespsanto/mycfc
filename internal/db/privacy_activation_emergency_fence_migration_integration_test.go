//go:build integration

package db

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPrivacyActivationEmergencyFenceForwardMigrationAppliesToPreviousBoundary(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	migration, err := migrationFiles.ReadFile("migrations/202609110003_privacy_activation_emergency_fence.sql")
	if err != nil {
		t.Fatal(err)
	}
	const marker = "-- Serialize activation and emergency disable"
	index := strings.LastIndex(baselineSchema, marker)
	if index < 0 || strings.TrimSpace(baselineSchema[index:]) != strings.TrimSpace(string(migration)) {
		t.Fatal("privacy activation emergency fence migration is not the exact final baseline segment")
	}

	previous, err := migrationFiles.ReadFile("migrations/202609110002_privacy_empty_provider_registry_activation.sql")
	if err != nil {
		t.Fatal(err)
	}
	v5 := migrationFunctionSegment(t, string(previous),
		"ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v5_check",
		"ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v5_check;")
	if _, err = tx.Exec(ctx, `ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v6_check`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, v5); err != nil {
		t.Fatal(err)
	}

	var activateDefinition string
	if err = tx.QueryRow(ctx, `SELECT pg_get_functiondef('privacy_activation_broker_activate(uuid,text,uuid[],bytea,bytea,uuid,uuid,text,text,bytea,bytea,bytea,bytea,jsonb,jsonb,timestamptz,timestamptz,timestamptz,timestamptz)'::regprocedure)`).Scan(&activateDefinition); err != nil {
		t.Fatal(err)
	}
	activateDefinition = strings.Replace(activateDefinition,
		" PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/privacy-activation',0));\n", "", 1)
	activateDefinition = strings.Replace(activateDefinition,
		"  OR p_executor_issued_at<=(SELECT changed_at FROM privacy_worker_kill_switch WHERE singleton)\n  OR p_administrator_issued_at<=(SELECT changed_at FROM privacy_worker_kill_switch WHERE singleton)\n  OR EXISTS(SELECT 1 FROM privacy_activation_evidence evidence\n    WHERE evidence.id=ANY(p_evidence_ids) AND evidence.recorded_at<=(SELECT changed_at FROM privacy_worker_kill_switch WHERE singleton))\n", "", 1)
	if _, err = tx.Exec(ctx, activateDefinition); err != nil {
		t.Fatal(err)
	}

	if _, err = tx.Exec(ctx, `DO $$DECLARE definition text;
BEGIN
 SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 EXECUTE replace(definition,'202609110003_privacy_activation_emergency_fence','202609110002_privacy_empty_provider_registry_activation');
 SELECT pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 EXECUTE replace(definition,'202609110003_privacy_activation_emergency_fence','202609110002_privacy_empty_provider_registry_activation');
END$$`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `DROP FUNCTION privacy_activation_disable(uuid,text)`); err != nil {
		t.Fatal(err)
	}
	brokerMigration, err := migrationFiles.ReadFile("migrations/202609100014_privacy_activation_broker.sql")
	if err != nil {
		t.Fatal(err)
	}
	disablePredecessor := migrationFunctionSegment(t, string(brokerMigration),
		"CREATE FUNCTION privacy_activation_disable(p_actor uuid)", "REVOKE ALL ON TABLE privacy_protected.activation_signed_approvals")
	if _, err = tx.Exec(ctx, disablePredecessor); err != nil {
		t.Fatal(err)
	}

	if _, err = tx.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	var fencedDefinition string
	if err = tx.QueryRow(ctx, `SELECT pg_get_functiondef('privacy_activation_broker_activate(uuid,text,uuid[],bytea,bytea,uuid,uuid,text,text,bytea,bytea,bytea,bytea,jsonb,jsonb,timestamptz,timestamptz,timestamptz,timestamptz)'::regprocedure)`).Scan(&fencedDefinition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fencedDefinition, "pg_advisory_xact_lock") || !strings.Contains(fencedDefinition, "evidence.recorded_at<=") {
		t.Fatal("forward migration did not install activation serialization and generation fence")
	}
	var oldExists, checkedExists bool
	if err = tx.QueryRow(ctx, `SELECT to_regprocedure('privacy_activation_disable(uuid)') IS NOT NULL,to_regprocedure('privacy_activation_disable(uuid,text)') IS NOT NULL`).Scan(&oldExists, &checkedExists); err != nil {
		t.Fatal(err)
	}
	if oldExists || !checkedExists {
		t.Fatalf("disable API boundary old=%t checked=%t", oldExists, checkedExists)
	}
	var publicDefaultExecute bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM pg_default_acl defaults
		CROSS JOIN LATERAL aclexplode(defaults.defaclacl) acl
		WHERE defaults.defaclnamespace='public'::regnamespace AND defaults.defaclobjtype='f'
		 AND acl.grantee=0 AND acl.privilege_type='EXECUTE')`).Scan(&publicDefaultExecute); err != nil {
		t.Fatal(err)
	}
	if publicDefaultExecute {
		t.Fatal("future public functions retain default PUBLIC execution")
	}
}
