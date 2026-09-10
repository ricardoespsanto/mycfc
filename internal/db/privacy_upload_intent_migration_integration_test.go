//go:build integration

package db

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPrivacyUploadIntentFoundationMigrationIsAdditiveProtectedAndImmutable(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	if _, err = tx.Exec(ctx, `
		DROP TRIGGER privacy_upload_profile_pointer_deferred ON member_profiles;
		DROP TRIGGER privacy_upload_repair_pointer_deferred ON repair_requests;
		DROP TRIGGER privacy_upload_equipment_pointer_deferred ON equipment`); err != nil {
		t.Fatal(err)
	}
	equipmentID := uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO equipment(id,asset_tag,name,type) VALUES($1,$2,'Migration fixture','Boat')`, equipmentID, "migration-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `
		ALTER TABLE member_profiles DROP CONSTRAINT member_profiles_photo_upload_intent_fk;
		ALTER TABLE repair_requests DROP CONSTRAINT repair_requests_image_upload_intent_fk;
		ALTER TABLE equipment DROP CONSTRAINT equipment_image_upload_intent_fk;
		ALTER TABLE member_profiles DROP COLUMN photo_upload_intent_id;
		ALTER TABLE repair_requests DROP COLUMN image_upload_intent_id;
		ALTER TABLE equipment DROP COLUMN image_upload_intent_id;
		DROP TABLE privacy_protected.object_upload_absence_evidence;
		DROP TABLE privacy_protected.object_upload_cleanup_attempts;
		DROP TABLE privacy_protected.object_upload_cleanup_jobs;
		DROP TABLE privacy_protected.object_upload_intent_holds;
		DROP TABLE privacy_protected.object_upload_intent_reservations;
		DROP TABLE privacy_protected.object_upload_intent_events;
		DROP TABLE privacy_protected.object_upload_intents`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFiles.ReadFile("migrations/202609100003_privacy_upload_intent_foundation.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}

	var intents, events, profileColumn, repairColumn, equipmentColumn, equipmentPreserved bool
	if err = tx.QueryRow(ctx, `SELECT
		to_regclass('privacy_protected.object_upload_intents') IS NOT NULL,
		to_regclass('privacy_protected.object_upload_intent_events') IS NOT NULL,
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='member_profiles' AND column_name='photo_upload_intent_id'),
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='repair_requests' AND column_name='image_upload_intent_id'),
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='equipment' AND column_name='image_upload_intent_id'),
		EXISTS(SELECT 1 FROM equipment WHERE id=$1)`, equipmentID).Scan(&intents, &events, &profileColumn, &repairColumn, &equipmentColumn, &equipmentPreserved); err != nil {
		t.Fatal(err)
	}
	if !intents || !events || !profileColumn || !repairColumn || !equipmentColumn || !equipmentPreserved {
		t.Fatalf("intents=%t events=%t columns=(%t,%t,%t) equipment_preserved=%t", intents, events, profileColumn, repairColumn, equipmentColumn, equipmentPreserved)
	}

	userID, profileIntentID, equipmentIntentID := uuid.New(), uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Upload intent test',$2,'hash','1990-01-01')`, userID, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	insertIntent := `INSERT INTO privacy_protected.object_upload_intents(
		id,subject_user_id,provenance_actor_user_id,source_kind,source_ref,service_code,target_kind,provider_contract_version,envelope_version,algorithm,encryption_key_id,encapsulation,nonce,ciphertext,digest_key_id,locator_digest,content_type,size_bytes,cleanup_after,created_at)
		VALUES($1,$2,$3,$4,$5,'private-media','OBJECT_KEY','s3-versioned/v1','x25519-aes256gcm-hkdfsha256/upload-intent-v1','X25519-HKDF-SHA256-AES-256-GCM',$6,$7,$8,$9,$10,$11,'image/jpeg',128,now()+interval '5 minutes',now())`
	if _, err = tx.Exec(ctx, insertIntent, profileIntentID, userID, userID, "MEMBER_PROFILE_PHOTO", userID, "upload-key-1", bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 12), bytes.Repeat([]byte{3}, 32), "digest-key-1", bytes.Repeat([]byte{4}, 32)); err != nil {
		t.Fatalf("valid profile intent rejected: %v", err)
	}
	if _, err = tx.Exec(ctx, insertIntent, equipmentIntentID, nil, userID, "EQUIPMENT_PHOTO", equipmentID, "upload-key-2", bytes.Repeat([]byte{5}, 32), bytes.Repeat([]byte{6}, 12), bytes.Repeat([]byte{7}, 32), "digest-key-2", bytes.Repeat([]byte{8}, 32)); err != nil {
		t.Fatalf("valid equipment intent rejected: %v", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE equipment SET image_upload_intent_id=$1 WHERE id=$2`, equipmentIntentID, equipmentID); err != nil {
		t.Fatalf("valid intent pointer rejected: %v", err)
	}

	if _, err = tx.Exec(ctx, `SAVEPOINT invalid_subject_classification`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, insertIntent, uuid.New(), userID, userID, "EQUIPMENT_PHOTO", uuid.New(), "upload-key-3", bytes.Repeat([]byte{9}, 32), bytes.Repeat([]byte{10}, 12), bytes.Repeat([]byte{11}, 32), "digest-key-3", bytes.Repeat([]byte{12}, 32)); err == nil {
		t.Fatal("equipment intent unexpectedly classified as its provenance actor's subject data")
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_subject_classification`); err != nil {
		t.Fatal(err)
	}

	if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.object_upload_intent_events(intent_id,sequence,status,reason_code,occurred_at) VALUES($1,1,'PREPARED','UPLOAD_RESERVED',now())`, profileIntentID); err != nil {
		t.Fatalf("valid intent event rejected: %v", err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT invalid_event_pair`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.object_upload_intent_events(intent_id,sequence,status,reason_code,occurred_at) VALUES($1,2,'ABSENCE_VERIFIED','PUT_FAILED',now())`, profileIntentID); err == nil {
		t.Fatal("invalid status/reason pairing unexpectedly accepted")
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_event_pair`); err != nil {
		t.Fatal(err)
	}
	assertMutationRejected := func(name, statement string, args ...any) {
		t.Helper()
		if _, err = tx.Exec(ctx, `SAVEPOINT protected_upload_mutation`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, statement, args...); err == nil {
			t.Fatalf("%s unexpectedly succeeded", name)
		}
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT protected_upload_mutation`); err != nil {
			t.Fatal(err)
		}
	}
	assertMutationRejected("intent update", `UPDATE privacy_protected.object_upload_intents SET cleanup_after=cleanup_after+interval '1 second' WHERE id=$1`, profileIntentID)
	assertMutationRejected("intent delete", `DELETE FROM privacy_protected.object_upload_intents WHERE id=$1`, profileIntentID)
	assertMutationRejected("event update", `UPDATE privacy_protected.object_upload_intent_events SET occurred_at=occurred_at+interval '1 second' WHERE intent_id=$1`, profileIntentID)
	assertMutationRejected("event delete", `DELETE FROM privacy_protected.object_upload_intent_events WHERE intent_id=$1`, profileIntentID)
}
