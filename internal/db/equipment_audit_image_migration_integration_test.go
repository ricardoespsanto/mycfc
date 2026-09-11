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

func TestEquipmentAuditImageSanitizationMigrationPreservesMeaningAndImmutability(t *testing.T) {
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

	schemaName := "equipment_audit_sanitize_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	protectedSchemaName := "privacy_protected_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	disableSchemaName := "privacy_disable_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	schema := pgx.Identifier{schemaName}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		_, _ = conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{protectedSchemaName}.Sanitize()+" CASCADE")
		_, _ = conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{disableSchemaName}.Sanitize()+" CASCADE")
	}()
	if _, err = conn.Exec(ctx, "SET search_path TO "+schema+",public"); err != nil {
		t.Fatal(err)
	}
	isolatedBaseline := strings.ReplaceAll(baselineSchema, "public.", schemaName+".")
	isolatedBaseline = strings.ReplaceAll(isolatedBaseline, "pg_catalog, public", "pg_catalog, "+schemaName+", public")
	isolatedBaseline = strings.ReplaceAll(isolatedBaseline, "pg_catalog,public", "pg_catalog,"+schemaName+",public")
	isolatedBaseline = strings.ReplaceAll(isolatedBaseline, "privacy_protected", protectedSchemaName)
	isolatedBaseline = strings.ReplaceAll(isolatedBaseline, "privacy_disable", disableSchemaName)
	if _, err = conn.PgConn().Exec(ctx, isolatedBaseline).ReadAll(); err != nil {
		t.Fatalf("create isolated baseline: %v", err)
	}
	// Recreate the immediately preceding production schema so a legacy-shaped
	// row can exist before the forward migration is applied.
	if _, err = conn.Exec(ctx, `DROP TRIGGER equipment_audit_image_sanitization_trigger ON equipment_audit_events`); err != nil {
		t.Fatal(err)
	}

	actorID, equipmentID := uuid.New(), uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Fleet admin',$2,'hash','1990-01-01')`, actorID, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO equipment(id,asset_tag,name,type) VALUES($1,$2,'K1 migration','Boat')`, equipmentID, "M-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	before := `{"asset_tag":"M-1","notes":"unchanged","image_object_key":null,"custom":{"keep":1}}`
	after := `{"asset_tag":"M-1","notes":"unchanged","image_object_key":"equipment/private/key.png","custom":{"keep":1}}`
	var eventID uuid.UUID
	if err = conn.QueryRow(ctx, `INSERT INTO equipment_audit_events(equipment_id,actor_user_id,action,before_state,after_state) VALUES($1,$2,'UPDATED',$3::jsonb,$4::jsonb) RETURNING id`, equipmentID, actorID, before, after).Scan(&eventID); err != nil {
		t.Fatal(err)
	}

	migration, err := migrationFiles.ReadFile("migrations/202609100001_equipment_audit_image_sanitization.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply image audit migration: %v", err)
	}

	var beforeHasImage, afterHasImage, imageChanged, hasBeforeKey, hasAfterKey, unrelatedPreserved bool
	if err = conn.QueryRow(ctx, `SELECT
		(before_state->>'has_image')::boolean,(after_state->>'has_image')::boolean,(after_state->>'image_changed')::boolean,
		before_state ? 'image_object_key',after_state ? 'image_object_key',
		before_state->'custom'=jsonb_build_object('keep',1) AND after_state->>'notes'='unchanged'
		FROM equipment_audit_events WHERE id=$1`, eventID).Scan(&beforeHasImage, &afterHasImage, &imageChanged, &hasBeforeKey, &hasAfterKey, &unrelatedPreserved); err != nil {
		t.Fatal(err)
	}
	if beforeHasImage || !afterHasImage || !imageChanged || hasBeforeKey || hasAfterKey || !unrelatedPreserved {
		t.Fatalf("sanitized state before_image=%v after_image=%v image_changed=%v before_key=%v after_key=%v unrelated=%v", beforeHasImage, afterHasImage, imageChanged, hasBeforeKey, hasAfterKey, unrelatedPreserved)
	}
	if _, err = conn.Exec(ctx, `UPDATE equipment_audit_events SET after_state=jsonb_set(after_state,'{notes}','"changed"') WHERE id=$1`, eventID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("ordinary audit mutation error = %v", err)
	}

	// A still-running old application slot may insert the legacy JSON shape
	// after this migration commits. The permanent insert trigger must sanitize
	// that row without rejecting the old writer during a rolling deployment.
	var rollingEventID uuid.UUID
	if err = conn.QueryRow(ctx, `INSERT INTO equipment_audit_events(equipment_id,actor_user_id,action,before_state,after_state) VALUES($1,$2,'UPDATED',$3::jsonb,$4::jsonb) RETURNING id`, equipmentID, actorID, before, after).Scan(&rollingEventID); err != nil {
		t.Fatalf("legacy writer after migration: %v", err)
	}
	var rollingBeforeKey, rollingAfterKey, rollingBeforeImage, rollingAfterImage, rollingChanged bool
	if err = conn.QueryRow(ctx, `SELECT before_state?'image_object_key',after_state?'image_object_key',(before_state->>'has_image')::boolean,(after_state->>'has_image')::boolean,(after_state->>'image_changed')::boolean FROM equipment_audit_events WHERE id=$1`, rollingEventID).Scan(&rollingBeforeKey, &rollingAfterKey, &rollingBeforeImage, &rollingAfterImage, &rollingChanged); err != nil {
		t.Fatal(err)
	}
	if rollingBeforeKey || rollingAfterKey || rollingBeforeImage || !rollingAfterImage || !rollingChanged {
		t.Fatalf("rolling insert before_key=%v after_key=%v before_image=%v after_image=%v changed=%v", rollingBeforeKey, rollingAfterKey, rollingBeforeImage, rollingAfterImage, rollingChanged)
	}
}
