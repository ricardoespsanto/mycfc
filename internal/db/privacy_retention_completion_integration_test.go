//go:build integration

package db

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPrivacyRetentionCompletesConsentAuditAndRepairClocks(t *testing.T) {
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

	// Audit tables remain append-only for the session role. The maintenance
	// routine owns the sole SECURITY DEFINER bypass; model that distinct owner
	// inside this rollback-only fixture.
	testOwner := "retention_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	owner := pgx.Identifier{testOwner}.Sanitize()
	for _, statement := range []string{
		"CREATE ROLE " + owner + " NOLOGIN",
		"GRANT USAGE ON SCHEMA public,privacy_protected TO " + owner,
		"GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public,privacy_protected TO " + owner,
		"GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public,privacy_protected TO " + owner,
		"GRANT ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public TO " + owner,
		"ALTER FUNCTION privacy_retention_pseudonymize_audit(integer) OWNER TO " + owner,
	} {
		if _, err = tx.Exec(ctx, statement); err != nil {
			t.Fatalf("prepare distinct maintenance owner: %v", err)
		}
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	userID, equipmentID := uuid.New(), uuid.New()
	userEmail := "retention-" + uuid.NewString() + "@example.test"
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Retention Canary',$2,'hash','1990-01-01')`, userID, userEmail); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO equipment(id,asset_tag,name,type) VALUES($1,$2,'Retention test boat','Boat')`, equipmentID, "RT-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	oldAuditID, newAuditID := uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO equipment_audit_events(id,equipment_id,actor_user_id,action,after_state,occurred_at) VALUES
		 ($1,$3,$4,'UPDATED',jsonb_build_object('note','Retention Canary '||$5::text),'2024-08-01'),
		 ($2,$3,$4,'UPDATED',jsonb_build_object('note','recent accountable event'),clock_timestamp())`, oldAuditID, newAuditID, equipmentID, userID, userEmail); err != nil {
		t.Fatal(err)
	}

	expiredConsentID, referencedConsentID := uuid.New(), uuid.New()
	ceased := now.AddDate(-3, 0, -1)
	if _, err = tx.Exec(ctx, `INSERT INTO consent_forms(id,user_id,granted_by_user_id,consent_type,document_version,document_sha256,is_accepted,date_signed,ceased_at,cessation_reason,evidence_expires_at)
	 VALUES($1,$2,$2,'Foto_Perfil','retention-v1',$3,true,$4,$5::timestamptz,'WITHDRAWN',$5::timestamptz+interval '3 years')`, expiredConsentID, userID, strings.Repeat("a", 64), ceased.Add(-time.Hour), ceased); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO consent_forms(id,user_id,granted_by_user_id,consent_type,document_version,document_sha256,is_accepted,date_signed)
	 VALUES($1,$2,$2,'Foto_Perfil','retention-v1',$3,true,$4)`, referencedConsentID, userID, strings.Repeat("b", 64), ceased.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE member_profiles DISABLE TRIGGER privacy_upload_profile_pointer_deferred`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO member_profiles(user_id,photo_object_key,photo_content_type,photo_size_bytes,photo_consent_form_id)
	 VALUES($1,'profiles/retained-by-fk.png','image/png',1,$2)`, userID, referencedConsentID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE member_profiles ENABLE TRIGGER privacy_upload_profile_pointer_deferred`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT privacy_consent_cease($1,'Foto_Perfil',NULL,'WITHDRAWN',$2)`, userID, ceased); err != nil {
		t.Fatal(err)
	}

	intentID, repairID := uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.object_upload_intents(
	 id,subject_user_id,provenance_actor_user_id,source_kind,source_ref,service_code,target_kind,provider_contract_version,
	 envelope_version,algorithm,encryption_key_id,encapsulation,nonce,ciphertext,digest_key_id,locator_digest,locator_commitment,
	 content_type,size_bytes,cleanup_after,created_at)
	 VALUES($1,$2,$2,'REPAIR_ATTACHMENT',$3,'private-media','OBJECT_KEY','s3-versioned/v1',
	 'x25519-aes256gcm-hkdfsha256/upload-intent-v1','X25519-HKDF-SHA256-AES-256-GCM','test-key',decode(repeat('11',32),'hex'),
		 decode(repeat('22',12),'hex'),decode(repeat('33',17),'hex'),'test-digest',decode(repeat('44',32),'hex'),digest(convert_to('repairs/retention.png','UTF8'),'sha256'),
		 'image/png',1,$4::timestamptz+interval '1 day',$4::timestamptz)`, intentID, userID, repairID, now.AddDate(0, 0, -31)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_protected.object_upload_intent_events(intent_id,sequence,status,reason_code,occurred_at) VALUES
	 ($1,1,'PREPARED','UPLOAD_RESERVED',$2),($1,2,'PUT_CONFIRMED','PUT_ACKNOWLEDGED',$2),($1,3,'ATTACHED','POINTER_ATTACHED',$2)`, intentID, now.AddDate(0, 0, -31)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO repair_requests(id,idempotency_key,equipment_id,reported_by_id,issue_description,image_object_key,image_content_type,image_size_bytes,image_upload_intent_id,date_reported)
	 VALUES($1,$2,$3,$4,'Repair retention integration fixture','repairs/retention.png','image/png',1,$5,$6)`, repairID, uuid.New(), equipmentID, userID, intentID, now.AddDate(0, 0, -31)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		t.Fatal(err)
	}

	result, err := dbgen.New(tx).RunPrivacyRetention(ctx, dbgen.RunPrivacyRetentionParams{WorkerRef: uuid.New(), BatchLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if result.ConsentEvidenceDeleted != 1 || result.AuditEventsPseudonymized < 1 || result.RepairAttachmentsQueued != 1 {
		t.Fatalf("retention result = %#v", result)
	}
	var expiredExists, referencedExists, oldActorPresent, newActorPresent, pointerPresent bool
	var oldState string
	if err = tx.QueryRow(ctx, `SELECT
	 EXISTS(SELECT 1 FROM consent_forms WHERE id=$1),
	 EXISTS(SELECT 1 FROM consent_forms WHERE id=$2),
	 EXISTS(SELECT 1 FROM equipment_audit_events WHERE id=$3 AND actor_user_id IS NOT NULL),
	 EXISTS(SELECT 1 FROM equipment_audit_events WHERE id=$4 AND actor_user_id IS NOT NULL),
	 (SELECT after_state::text FROM equipment_audit_events WHERE id=$3),
	 EXISTS(SELECT 1 FROM repair_requests WHERE id=$5 AND image_object_key IS NOT NULL)`, expiredConsentID, referencedConsentID, oldAuditID, newAuditID, repairID).
		Scan(&expiredExists, &referencedExists, &oldActorPresent, &newActorPresent, &oldState, &pointerPresent); err != nil {
		t.Fatal(err)
	}
	if expiredExists || !referencedExists || oldActorPresent || !newActorPresent || pointerPresent || strings.Contains(oldState, "Retention Canary") || strings.Contains(oldState, userEmail) {
		t.Fatalf("expired=%v referenced=%v old_actor=%v new_actor=%v pointer=%v old_state=%q", expiredExists, referencedExists, oldActorPresent, newActorPresent, pointerPresent, oldState)
	}
	status, err := dbgen.New(tx).GetPrivacyRetentionStatus(ctx)
	if err != nil || status.RepairOverdueCount != 1 {
		t.Fatalf("overdue status=%#v err=%v", status, err)
	}

	attemptID := uuid.New()
	statements := []struct {
		sql  string
		args []any
	}{
		{`UPDATE privacy_protected.object_upload_cleanup_jobs SET status='SUCCEEDED',lease_epoch=1,attempt_count=1,updated_at=clock_timestamp() WHERE intent_id=$1`, []any{intentID}},
		{`INSERT INTO privacy_protected.object_upload_cleanup_attempts(id,intent_id,lease_epoch,worker_ref,acquired_at,expires_at,released_at,outcome)
		 VALUES($1,$2,1,$3,clock_timestamp()-interval '1 minute',clock_timestamp()+interval '4 minutes',clock_timestamp(),'SUCCEEDED')`, []any{attemptID, intentID, uuid.New()}},
		{`INSERT INTO privacy_protected.object_upload_absence_evidence(intent_id,attempt_id,lease_epoch,evidence_version,deleted_version_count,deleted_marker_count,list_call_count,stable_empty_check_count,transcript_key_id,transcript_digest,occurred_at)
		 VALUES($1,$2,1,'s3-absence/v1',1,1,2,2,'test-transcript',decode(repeat('66',32),'hex'),clock_timestamp())`, []any{intentID, attemptID}},
		{`INSERT INTO privacy_protected.object_upload_intent_events(intent_id,sequence,status,reason_code,occurred_at)
		 VALUES($1,5,'ABSENCE_VERIFIED','CLEANUP_CONFIRMED',clock_timestamp())`, []any{intentID}},
	}
	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	status, err = dbgen.New(tx).GetPrivacyRetentionStatus(ctx)
	if err != nil || status.RepairOverdueCount != 0 {
		t.Fatalf("verified status=%#v err=%v", status, err)
	}
}

func TestPrivacyRetentionRejectsConcurrentRuns(t *testing.T) {
	ctx := context.Background()
	first, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close(ctx)
	second, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(ctx)
	if _, err = first.Exec(ctx, `SELECT pg_advisory_lock(247,247)`); err != nil {
		t.Fatal(err)
	}
	defer first.Exec(ctx, `SELECT pg_advisory_unlock(247,247)`)
	if _, err = second.Exec(ctx, `SELECT * FROM privacy_retention_run($1,1)`, uuid.New()); err == nil || !strings.Contains(err.Error(), "privacy retention already running") {
		t.Fatalf("concurrent retention error = %v", err)
	}
}
