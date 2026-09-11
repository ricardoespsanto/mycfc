//go:build integration

package db

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestGuardianAuthorityForwardMigrationFailsLegacyAuthorityClosed(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	migration, err := migrationFiles.ReadFile("migrations/202609110004_guardian_authority_verification.sql")
	if err != nil {
		t.Fatal(err)
	}
	const predecessorSHA256 = "4a48214c265b2dca6741fa5e363a1135211a4cde27ce3906892f7b9df98a1eed"
	if digest := fmt.Sprintf("%x", sha256.Sum256(migration)); digest != predecessorSHA256 {
		t.Fatalf("guardian-authority predecessor fixture changed: got %s", digest)
	}
	cutoffMigration, err := migrationFiles.ReadFile("migrations/202609110005_guardian_authority_cutoff_reconciliation.sql")
	if err != nil {
		t.Fatal(err)
	}
	const marker = "-- Guardian authority is an explicit, reviewed capability. Legacy users.guardian_id"
	markerIndex := strings.LastIndex(baselineSchema, marker)
	if markerIndex < 0 {
		t.Fatal("guardian-authority migration marker missing from baseline")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	schemaName := "guardian_277_forward_" + suffix
	protectedName := "guardian_277_protected_" + suffix
	disableName := "guardian_277_disable_" + suffix
	schema := pgx.Identifier{schemaName}.Sanitize()
	protected := pgx.Identifier{protectedName}.Sanitize()
	disable := pgx.Identifier{disableName}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+protected+" CASCADE")
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+disable+" CASCADE")
	}()
	if _, err = conn.Exec(ctx, "SET search_path TO "+schema+",public"); err != nil {
		t.Fatal(err)
	}
	rewrite := func(sql string) string {
		sql = strings.ReplaceAll(sql, "public.", schemaName+".")
		sql = strings.ReplaceAll(sql, "pg_catalog, public", "pg_catalog, "+schemaName+", public")
		sql = strings.ReplaceAll(sql, "pg_catalog,public", "pg_catalog,"+schemaName+",public")
		sql = strings.ReplaceAll(sql, "namespace.nspname='public'", "namespace.nspname='"+schemaName+"'")
		sql = strings.ReplaceAll(sql, "privacy_protected", protectedName)
		return strings.ReplaceAll(sql, "privacy_disable", disableName)
	}
	if _, err = conn.PgConn().Exec(ctx, rewrite(baselineSchema[:markerIndex])).ReadAll(); err != nil {
		t.Fatalf("create exact guardian-authority predecessor baseline: %v", err)
	}

	guardianID, subjectID, administratorID := uuid.New(), uuid.New(), uuid.New()
	for _, account := range []struct {
		id    uuid.UUID
		name  string
		email string
	}{{guardianID, "Legacy guardian", "guardian-" + suffix + "@example.test"}, {administratorID, "Migration administrator", "admin-" + suffix + "@example.test"}} {
		if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth)
			VALUES($1,$2,$3,'adult-password-hash','1980-01-01')`, account.id, account.name, account.email); err != nil {
			t.Fatal(err)
		}
	}
	legacyLogin := "legacy-" + suffix[:12]
	const initialCredentialVersion int64 = 7
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,minor_login_id,password_hash,credential_version,guardian_id,is_dependent,date_of_birth)
		VALUES($1,'Legacy dependent',NULL,$2,'minor-password-hash',$3,$4,true,(CURRENT_DATE-interval '10 years')::date)`,
		subjectID, legacyLogin, initialCredentialVersion, guardianID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO member_profiles(user_id,address_line1,postcode,locality,emergency_contact_name,
		emergency_contact_relationship,emergency_contact_phone,medical_declaration,medical_notes)
		VALUES($1,'Private street','3000-000','Coimbra','Private contact','Parent','912345678','PROVIDED','Private medical note')`, subjectID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed)
		VALUES($1,$2,clock_timestamp()+interval '1 day',$3,true)`, "legacy-session-"+suffix, []byte("legacy-session"), subjectID); err != nil {
		t.Fatal(err)
	}

	// Model an active predecessor release. The approval row satisfies the table
	// constraint; fixture-only trigger suspension avoids reproducing the broker's
	// signed-envelope protocol, which is independently covered by its own tests.
	policyVersion := "guardian-forward-" + suffix
	proposalID, approvalID := uuid.New(), uuid.New()
	evidenceIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	digest := make([]byte, 32)
	if _, err = conn.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,
		working_retention_days,adopted_at,adopted_by) VALUES($1,'[]','privacy-erasure-executor/v2','privacy-erasure-plan/v2',90,clock_timestamp(),$2)`,
		policyVersion, administratorID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO privacy_activation_proposals(id,policy_version,evidence_ids,evidence_set_sha256,activation_sha256,proposed_by_ref,proposed_at)
		VALUES($1,$2,$3,$4,$4,$5,clock_timestamp())`, proposalID, policyVersion, evidenceIDs, digest, guardianID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO privacy_activation_approvals(id,proposal_id,activation_sha256,approved_by_ref,approved_at)
		VALUES($1,$2,$3,$4,clock_timestamp())`, approvalID, proposalID, digest, administratorID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `ALTER TABLE privacy_request_activation DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by,approval_id)
		VALUES(true,$1,true,true,$2,$3)`, policyVersion, administratorID, approvalID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `ALTER TABLE privacy_request_activation ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `UPDATE privacy_worker_kill_switch
		SET engaged=false,version=version+1,activation_approval_id=$1,changed_at=clock_timestamp()
		WHERE singleton RETURNING version`, approvalID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO privacy_worker_kill_switch_events(version,engaged,activation_approval_id,occurred_at)
		SELECT version,false,activation_approval_id,changed_at FROM privacy_worker_kill_switch WHERE singleton`); err != nil {
		t.Fatal(err)
	}

	if _, err = conn.PgConn().Exec(ctx, rewrite(string(migration))).ReadAll(); err != nil {
		t.Fatalf("apply guardian-authority forward migration: %v", err)
	}
	if _, err = conn.PgConn().Exec(ctx, rewrite(string(cutoffMigration))).ReadAll(); err != nil {
		t.Fatalf("apply guardian-authority cutoff forward migration: %v", err)
	}

	var guardianPointer, loginID, passwordHash *string
	var credentialVersion int64
	if err = conn.QueryRow(ctx, `SELECT guardian_id::text,minor_login_id::text,password_hash,credential_version FROM users WHERE id=$1`, subjectID).
		Scan(&guardianPointer, &loginID, &passwordHash, &credentialVersion); err != nil {
		t.Fatal(err)
	}
	if guardianPointer != nil || loginID != nil || passwordHash != nil || credentialVersion != initialCredentialVersion+1 {
		t.Fatalf("legacy credentials not revoked guardian=%v login=%v password=%v credential_version=%d", guardianPointer, loginID, passwordHash, credentialVersion)
	}
	var sessionCount int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1`, subjectID).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 0 {
		t.Fatalf("legacy indexed sessions remaining = %d", sessionCount)
	}

	var relationshipID uuid.UUID
	var relationshipState, actorRole, action string
	var relationshipVersion int64
	var actorRef *uuid.UUID
	if err = conn.QueryRow(ctx, `SELECT relationship.id,relationship.state,relationship.version,event.actor_ref,event.actor_role,event.action
		FROM guardian_authority_relationships relationship
		JOIN guardian_authority_events event ON event.relationship_id=relationship.id AND event.relationship_version=relationship.version
		WHERE relationship.guardian_user_id=$1 AND relationship.subject_user_id=$2`, guardianID, subjectID).
		Scan(&relationshipID, &relationshipState, &relationshipVersion, &actorRef, &actorRole, &action); err != nil {
		t.Fatal(err)
	}
	if relationshipID == uuid.Nil || relationshipState != "PENDING" || relationshipVersion != 1 || actorRef != nil || actorRole != "SYSTEM" || action != "DECLARED" {
		t.Fatalf("legacy provenance relationship=%s state=%q version=%d actor=%v role=%q action=%q",
			relationshipID, relationshipState, relationshipVersion, actorRef, actorRole, action)
	}

	queries := dbgen.New(conn)
	if _, err = queries.GetActiveDependentByLoginID(ctx, &legacyLogin); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("legacy dependent login did not fail closed: %v", err)
	}
	if _, err = queries.GetActiveAccountByIDWithoutProfile(ctx, subjectID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("pending dependent remained session-authenticatable: %v", err)
	}
	dependents, err := queries.ListDependentsByGuardian(ctx, dbgen.ListDependentsByGuardianParams{GuardianID: &guardianID, RowLimit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(dependents) != 0 {
		t.Fatalf("pending relationship exposed private dependent rows: %+v", dependents)
	}
	disclosures, err := queries.ListGuardianRelationshipsForGuardian(ctx, dbgen.ListGuardianRelationshipsForGuardianParams{GuardianID: guardianID, RowLimit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(disclosures) != 1 || disclosures[0].State != "PENDING" || disclosures[0].SubjectName != "" ||
		disclosures[0].DateOfBirth.Valid || disclosures[0].MinorLoginID != "" || disclosures[0].LeaderboardVisible || disclosures[0].ProfileComplete {
		t.Fatalf("pending guardian disclosure was not redacted: %+v", disclosures)
	}

	var activationEnabled, fulfilmentReady, killSwitchEngaged, workerReady bool
	var activationApproval *uuid.UUID
	var switchApproval *uuid.UUID
	if err = conn.QueryRow(ctx, `SELECT activation.enabled,activation.fulfilment_ready,activation.approval_id,
		switch.engaged,switch.activation_approval_id,privacy_worker_activation_ready()
		FROM privacy_request_activation activation CROSS JOIN privacy_worker_kill_switch switch
		WHERE activation.singleton AND switch.singleton`).Scan(&activationEnabled, &fulfilmentReady, &activationApproval,
		&killSwitchEngaged, &switchApproval, &workerReady); err != nil {
		t.Fatal(err)
	}
	if activationEnabled || fulfilmentReady || activationApproval != nil || !killSwitchEngaged || switchApproval != nil || workerReady {
		t.Fatalf("privacy execution not fenced enabled=%t ready=%t approval=%v kill_switch=%t switch_approval=%v worker_ready=%t",
			activationEnabled, fulfilmentReady, activationApproval, killSwitchEngaged, switchApproval, workerReady)
	}

	var evidenceDefinition, digestDefinition string
	if err = conn.QueryRow(ctx, `SELECT pg_get_functiondef('guardian_authority_reconcile_cutoffs()'::regprocedure)`).Scan(&evidenceDefinition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(evidenceDefinition, "DELETE FROM sessions") || !strings.Contains(evidenceDefinition, "credential_version=credential_version+1") {
		t.Fatalf("cutoff reconciliation does not revoke dormant access: %s", evidenceDefinition)
	}
	if err = conn.QueryRow(ctx, `SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure)`).Scan(&evidenceDefinition); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, `SELECT pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure)`).Scan(&digestDefinition); err != nil {
		t.Fatal(err)
	}
	const cutoff = "202609110005_guardian_authority_cutoff_reconciliation"
	if !strings.Contains(evidenceDefinition, cutoff) || !strings.Contains(digestDefinition, cutoff) {
		t.Fatal("cutoff migration did not advance the authenticated schema boundary")
	}
}
