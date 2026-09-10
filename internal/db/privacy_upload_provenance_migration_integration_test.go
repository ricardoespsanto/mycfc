//go:build integration

package db

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPrivacyUploadProvenanceForwardMigrationAppliesToFoundationSchema(t *testing.T) {
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
	rollback := `
DROP FUNCTION privacy_upload_cleanup_fail(uuid,uuid,bigint,uuid,boolean,bigint);
DROP FUNCTION privacy_upload_cleanup_complete(uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea);
DROP FUNCTION privacy_upload_cleanup_claim(bigint,uuid);
DROP FUNCTION privacy_upload_remove(uuid,uuid,text,uuid);
DROP FUNCTION privacy_upload_attach(uuid,bytea,uuid,text,uuid,text,text,bigint);
DROP FUNCTION privacy_upload_mark_cleanup(uuid,bytea,text);
DROP FUNCTION privacy_upload_confirm_put(uuid,bytea);
DROP FUNCTION privacy_upload_finalize(uuid,bytea,text,text,text,bytea,bytea,bytea,text,bytea,bytea);
DROP FUNCTION privacy_upload_begin(uuid,uuid,uuid,text,uuid,text,text,bigint,bytea);
DROP TRIGGER privacy_upload_pointer_state_deferred ON privacy_protected.object_upload_intent_events;
DROP FUNCTION privacy_upload_enforce_pointer_state();
DROP TRIGGER privacy_upload_profile_pointer_deferred ON member_profiles;
DROP TRIGGER privacy_upload_repair_pointer_deferred ON repair_requests;
DROP TRIGGER privacy_upload_equipment_pointer_deferred ON equipment;
DROP FUNCTION privacy_upload_enforce_pointer_mutation();
DROP FUNCTION privacy_upload_pointer_matches(uuid,text,uuid,bytea,text,bigint);
DROP FUNCTION privacy_upload_pointer_references(uuid,text,uuid);
DROP FUNCTION privacy_upload_source_lock(text,uuid);
DROP TABLE privacy_protected.object_upload_absence_evidence;
DROP TABLE privacy_protected.object_upload_cleanup_attempts;
DROP TABLE privacy_protected.object_upload_cleanup_jobs;
DROP TABLE privacy_protected.object_upload_intent_holds;
DROP TABLE privacy_protected.object_upload_intent_reservations;
DROP INDEX privacy_protected.privacy_upload_intent_prepared_uidx;
DROP INDEX privacy_protected.privacy_upload_intent_put_uidx;
DROP INDEX privacy_protected.privacy_upload_intent_attached_uidx;
DROP INDEX privacy_protected.privacy_upload_intent_cleanup_uidx;
DROP INDEX privacy_protected.privacy_upload_intent_absent_uidx;
ALTER TABLE privacy_protected.object_upload_intent_events DROP CONSTRAINT object_upload_intent_events_reason_code_check;
ALTER TABLE privacy_protected.object_upload_intent_events ADD CONSTRAINT object_upload_intent_events_reason_code_check CHECK (reason_code IN ('UPLOAD_RESERVED','PUT_ACKNOWLEDGED','POINTER_ATTACHED','PUT_AMBIGUOUS','PUT_FAILED','ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED','CLEANUP_CONFIRMED')) NOT VALID;
ALTER TABLE privacy_protected.object_upload_intent_events DROP CONSTRAINT object_upload_intent_events_check;
ALTER TABLE privacy_protected.object_upload_intent_events ADD CONSTRAINT object_upload_intent_events_check CHECK ((status='PREPARED' AND reason_code='UPLOAD_RESERVED') OR (status='PUT_CONFIRMED' AND reason_code='PUT_ACKNOWLEDGED') OR (status='ATTACHED' AND reason_code='POINTER_ATTACHED') OR (status='CLEANUP_REQUIRED' AND reason_code IN ('PUT_AMBIGUOUS','PUT_FAILED','ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED')) OR (status='ABSENCE_VERIFIED' AND reason_code='CLEANUP_CONFIRMED')) NOT VALID;
ALTER TABLE privacy_protected.object_upload_intents DROP COLUMN locator_commitment;`

	if _, err = tx.Exec(ctx, rollback); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFiles.ReadFile("migrations/202609100004_privacy_upload_provenance.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	var reservations, cleanupJobs, claimRoutine, attachRoutine, staleAllowed bool
	if err = tx.QueryRow(ctx, `SELECT
 to_regclass('privacy_protected.object_upload_intent_reservations') IS NOT NULL,
 to_regclass('privacy_protected.object_upload_cleanup_jobs') IS NOT NULL,
 to_regprocedure('privacy_upload_cleanup_claim(bigint,uuid)') IS NOT NULL,
 to_regprocedure('privacy_upload_attach(uuid,bytea,uuid,text,uuid,text,text,bigint)') IS NOT NULL,
 pg_get_constraintdef((SELECT oid FROM pg_constraint WHERE conrelid='privacy_protected.object_upload_intent_events'::regclass AND conname='object_upload_intent_events_reason_code_check')) LIKE '%STALE_TIMEOUT%'`).Scan(&reservations, &cleanupJobs, &claimRoutine, &attachRoutine, &staleAllowed); err != nil {
		t.Fatal(err)
	}
	if !reservations || !cleanupJobs || !claimRoutine || !attachRoutine || !staleAllowed {
		t.Fatalf("reservations=%t cleanup=%t claim=%t attach=%t stale=%t", reservations, cleanupJobs, claimRoutine, attachRoutine, staleAllowed)
	}
}
