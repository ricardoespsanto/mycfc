//go:build integration

package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPrivacyUploadPointersAttachAtomicallyForEveryMediaFamily(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	q := dbgen.New(conn)
	actorID := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Upload pointer integration',$2,'hash','1990-01-01')`, actorID, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err = q.CreateEquipmentWithAudit(ctx, dbgen.CreateEquipmentWithAuditParams{ID: uuid.New(), AssetTag: "UG-" + uuid.NewString()[:8], Name: "Unguarded image", Type: "Boat", Status: "Operational", ImageObjectKey: stringAddress("equipment/untracked.png"), ImageContentType: stringAddress("image/png"), ImageSizeBytes: int64Address(5), ActorUserID: &actorID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("untracked equipment pointer error=%v", err)
	}

	equipmentID := uuid.New()
	firstIntent, firstToken := seedConfirmedUploadIntent(t, ctx, conn, actorID, nil, "EQUIPMENT_PHOTO", equipmentID, "equipment/first.png", 5)
	equipment, err := q.CreateEquipmentWithAudit(ctx, dbgen.CreateEquipmentWithAuditParams{
		ID: equipmentID, AssetTag: "UP-" + uuid.NewString()[:8], Name: "Upload provenance boat", Type: "Boat", Status: "Operational",
		ImageObjectKey: stringAddress("equipment/first.png"), ImageContentType: stringAddress("image/png"), ImageSizeBytes: int64Address(5),
		ImageUploadIntentID: &firstIntent, UploadHoldToken: firstToken, ActorUserID: &actorID,
	})
	if err != nil || equipment.ImageUploadIntentID == nil || *equipment.ImageUploadIntentID != firstIntent {
		t.Fatalf("equipment create=%#v err=%v", equipment, err)
	}
	if err = q.AttachPrivacyUploadIntent(ctx, dbgen.AttachPrivacyUploadIntentParams{IntentID: firstIntent, HoldToken: randomBytes(t, 32), SourceKind: "EQUIPMENT_PHOTO", SourceRef: equipmentID, ObjectKey: "equipment/first.png", ContentType: "image/png", SizeBytes: 5}); err == nil {
		t.Fatal("attached intent accepted a mismatched replay token")
	}
	if err = q.AttachPrivacyUploadIntent(ctx, dbgen.AttachPrivacyUploadIntentParams{IntentID: firstIntent, HoldToken: firstToken, SourceKind: "EQUIPMENT_PHOTO", SourceRef: equipmentID, ObjectKey: "equipment/first.png", ContentType: "image/png", SizeBytes: 5}); err != nil {
		t.Fatalf("exact attached replay failed: %v", err)
	}
	if err = q.AttachPrivacyUploadIntent(ctx, dbgen.AttachPrivacyUploadIntentParams{IntentID: firstIntent, SourceKind: "EQUIPMENT_PHOTO", SourceRef: equipmentID, ObjectKey: "equipment/first.png", ContentType: "image/png", SizeBytes: 5}); err == nil {
		t.Fatal("attached intent accepted a null replay token")
	}
	if err = q.RemovePrivacyUploadIntent(ctx, dbgen.RemovePrivacyUploadIntentParams{IntentID: firstIntent, ActorUserID: actorID, SourceKind: "MEMBER_PROFILE_PHOTO", SourceRef: equipmentID}); err == nil {
		t.Fatal("still-referenced intent was queued for permanent cleanup")
	}
	assertUploadStatus(t, ctx, conn, firstIntent, "ATTACHED")
	secondIntent, secondToken := seedConfirmedUploadIntent(t, ctx, conn, actorID, nil, "EQUIPMENT_PHOTO", equipmentID, "equipment/second.png", 6)
	updatedEquipment, err := q.UpdateEquipmentWithAudit(ctx, dbgen.UpdateEquipmentWithAuditParams{
		EquipmentID: equipmentID, ExpectedUpdatedAt: equipment.UpdatedAt, AssetTag: equipment.AssetTag, Name: equipment.Name,
		Type: equipment.Type, Status: equipment.Status, Notes: equipment.Notes, ImageObjectKey: stringAddress("equipment/second.png"),
		ImageContentType: stringAddress("image/png"), ImageSizeBytes: int64Address(6), ImageUploadIntentID: &secondIntent,
		UploadHoldToken: secondToken, ActorUserID: &actorID,
	})
	if err != nil || updatedEquipment.ImageUploadIntentID == nil || *updatedEquipment.ImageUploadIntentID != secondIntent {
		t.Fatalf("equipment replacement=%#v err=%v", updatedEquipment, err)
	}
	assertUploadStatus(t, ctx, conn, firstIntent, "CLEANUP_REQUIRED")
	assertUploadStatus(t, ctx, conn, secondIntent, "ATTACHED")
	if _, err = conn.Exec(ctx, `UPDATE equipment SET image_content_type='image/webp' WHERE id=$1`, equipmentID); err == nil {
		t.Fatal("direct equipment pointer metadata mutation bypassed provenance")
	}
	assertUploadStatus(t, ctx, conn, secondIntent, "ATTACHED")
	identityEquipmentID := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO equipment(id,asset_tag,name,type,status) VALUES($1,$2,'Identity-bound pointer','Boat','Operational')`, identityEquipmentID, "ID-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	identityIntent, identityToken := seedConfirmedUploadIntent(t, ctx, conn, actorID, nil, "EQUIPMENT_PHOTO", identityEquipmentID, "equipment/identity.png", 8)
	identityTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = identityTx.Exec(ctx, `SELECT privacy_upload_attach($1,$2,NULL,'EQUIPMENT_PHOTO',$3,'equipment/identity.png','image/png',8)`, identityIntent, identityToken, identityEquipmentID); err == nil {
		_, err = identityTx.Exec(ctx, `UPDATE equipment SET image_object_key='equipment/identity.png',image_content_type='image/png',image_size_bytes=8,image_upload_intent_id=$1 WHERE id=$2`, identityIntent, identityEquipmentID)
	}
	if err == nil {
		err = identityTx.Commit(ctx)
	} else {
		_ = identityTx.Rollback(ctx)
	}
	if err != nil {
		t.Fatalf("identity-bound equipment attachment failed: %v", err)
	}
	if _, err = conn.Exec(ctx, `UPDATE equipment SET id=$2 WHERE id=$1`, identityEquipmentID, uuid.New()); err == nil || !strings.Contains(err.Error(), "upload pointer invariant rejected") {
		t.Fatalf("equipment source identity rebinding error=%v", err)
	}

	repairID := uuid.New()
	repairIntent, repairToken := seedConfirmedUploadIntent(t, ctx, conn, actorID, &actorID, "REPAIR_ATTACHMENT", repairID, "repairs/evidence.png", 7)
	repair, err := q.CreateRepairRequest(ctx, dbgen.CreateRepairRequestParams{
		ID: repairID, IdempotencyKey: uuid.New(), EquipmentID: equipmentID, ReportedByID: &actorID,
		IssueDescription: "Repair upload provenance integration fixture.", ImageObjectKey: stringAddress("repairs/evidence.png"),
		ImageContentType: stringAddress("image/png"), ImageSizeBytes: int64Address(7), ImageUploadIntentID: &repairIntent, UploadHoldToken: repairToken,
	})
	if err != nil || repair.ImageUploadIntentID == nil || *repair.ImageUploadIntentID != repairIntent {
		t.Fatalf("repair=%#v err=%v", repair, err)
	}
	assertUploadStatus(t, ctx, conn, repairIntent, "ATTACHED")
	if _, err = conn.Exec(ctx, `UPDATE repair_requests SET image_size_bytes=image_size_bytes+1 WHERE id=$1`, repairID); err == nil {
		t.Fatal("direct repair pointer metadata mutation bypassed provenance")
	}
	assertUploadStatus(t, ctx, conn, repairIntent, "ATTACHED")
	if _, err = conn.Exec(ctx, `UPDATE repair_requests SET id=$2 WHERE id=$1`, repairID, uuid.New()); err == nil || !strings.Contains(err.Error(), "upload pointer invariant rejected") {
		t.Fatalf("repair source identity rebinding error=%v", err)
	}
	if _, err = q.CreateRepairRequest(ctx, dbgen.CreateRepairRequestParams{ID: uuid.New(), IdempotencyKey: uuid.New(), EquipmentID: equipmentID, ReportedByID: &actorID, IssueDescription: "Untracked repair image must be rejected.", ImageObjectKey: stringAddress("repairs/untracked.png"), ImageContentType: stringAddress("image/png"), ImageSizeBytes: int64Address(5)}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("untracked repair pointer error=%v", err)
	}

	profileIntent, profileToken := seedConfirmedUploadIntent(t, ctx, conn, actorID, &actorID, "MEMBER_PROFILE_PHOTO", actorID, "profiles/profile.png", 5)
	err = q.AttachPrivacyUploadIntent(ctx, dbgen.AttachPrivacyUploadIntentParams{IntentID: profileIntent, HoldToken: profileToken, SourceKind: "MEMBER_PROFILE_PHOTO", SourceRef: uuid.New(), ObjectKey: "profiles/profile.png", ContentType: "image/png", SizeBytes: 5})
	if err == nil {
		t.Fatal("mismatched profile source unexpectedly attached")
	}
	assertUploadStatus(t, ctx, conn, profileIntent, "PUT_CONFIRMED")
	if _, err = conn.Exec(ctx, `INSERT INTO member_profiles(user_id) VALUES($1) ON CONFLICT(user_id) DO NOTHING`, actorID); err != nil {
		t.Fatal(err)
	}
	consentID := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO consent_forms(id,user_id,granted_by_user_id,consent_type,document_version,document_sha256,is_accepted) VALUES($1,$2,$2,'Foto_Perfil','integration-v1',$3,true)`, consentID, actorID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT privacy_upload_attach($1,$2,NULL,'MEMBER_PROFILE_PHOTO',$3,'profiles/profile.png','image/png',5)`, profileIntent, profileToken, actorID); err == nil {
		_, err = tx.Exec(ctx, `UPDATE member_profiles SET photo_object_key='profiles/profile.png',photo_content_type='image/png',photo_size_bytes=5,photo_consent_form_id=$1,photo_upload_intent_id=$2 WHERE user_id=$3`, consentID, profileIntent, actorID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	} else {
		_ = tx.Rollback(ctx)
	}
	if err != nil {
		t.Fatalf("atomic profile attachment failed: %v", err)
	}
	if _, err = conn.Exec(ctx, `UPDATE member_profiles SET photo_object_key=NULL,photo_content_type=NULL,photo_size_bytes=NULL,photo_consent_form_id=NULL,photo_upload_intent_id=NULL WHERE user_id=$1`, actorID); err == nil {
		t.Fatal("direct profile pointer removal bypassed cleanup provenance")
	}
	assertUploadStatus(t, ctx, conn, profileIntent, "ATTACHED")
	reboundProfileID := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Rebound upload pointer',$2,'hash','1990-01-01')`, reboundProfileID, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `UPDATE member_profiles SET user_id=$2 WHERE user_id=$1`, actorID, reboundProfileID); err == nil || !strings.Contains(err.Error(), "upload pointer invariant rejected") {
		t.Fatalf("profile source identity rebinding error=%v", err)
	}
	if err = q.RemovePrivacyUploadIntent(ctx, dbgen.RemovePrivacyUploadIntentParams{IntentID: profileIntent, ActorUserID: actorID, SourceKind: "MEMBER_PROFILE_PHOTO", SourceRef: actorID}); err == nil {
		t.Fatal("still-referenced profile intent was queued for permanent cleanup")
	}
	assertUploadStatus(t, ctx, conn, profileIntent, "ATTACHED")
}

func TestPrivacyUploadLifecycleRejectsMissingTokensAndUnboundPointers(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	q := dbgen.New(conn)
	actorID, sourceRef := uuid.New(), uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Upload invariant integration',$2,'hash','1990-01-01')`, actorID, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actorID); err != nil {
		t.Fatal(err)
	}

	intentID, token := seedPreparedUploadIntent(t, ctx, conn, actorID, nil, "EQUIPMENT_PHOTO", sourceRef, "equipment/invariant.png", 9)
	if _, err = conn.Exec(ctx, `SELECT privacy_upload_confirm_put($1,NULL)`, intentID); err == nil {
		t.Fatal("confirm accepted a null token")
	}
	if _, err = conn.Exec(ctx, `SELECT privacy_upload_mark_cleanup($1,NULL,'PUT_FAILED')`, intentID); err == nil {
		t.Fatal("cleanup transition accepted a null token")
	}
	if _, err = conn.Exec(ctx, `SELECT privacy_upload_confirm_put($1,$2)`, intentID, token); err != nil {
		t.Fatal(err)
	}
	base := dbgen.AttachPrivacyUploadIntentParams{IntentID: intentID, HoldToken: token, SourceKind: "EQUIPMENT_PHOTO", SourceRef: sourceRef, ObjectKey: "equipment/invariant.png", ContentType: "image/png", SizeBytes: 9}
	for name, mutate := range map[string]func(*dbgen.AttachPrivacyUploadIntentParams){
		"key":  func(input *dbgen.AttachPrivacyUploadIntentParams) { input.ObjectKey = "equipment/swapped.png" },
		"type": func(input *dbgen.AttachPrivacyUploadIntentParams) { input.ContentType = "image/webp" },
		"size": func(input *dbgen.AttachPrivacyUploadIntentParams) { input.SizeBytes++ },
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			mutate(&input)
			if err := q.AttachPrivacyUploadIntent(ctx, input); err == nil {
				t.Fatalf("attachment accepted mismatched %s", name)
			}
		})
	}
	missingToken := base
	missingToken.HoldToken = nil
	if err = q.AttachPrivacyUploadIntent(ctx, missingToken); err == nil {
		t.Fatal("attachment accepted a null token")
	}
	if err = q.AttachPrivacyUploadIntent(ctx, base); err == nil {
		t.Fatal("standalone attachment committed without its source pointer")
	}
	assertUploadStatus(t, ctx, conn, intentID, "PUT_CONFIRMED")

	legacyID := uuid.New()
	// Simulate a pointer that predates the migration; new untracked pointers are rejected by the trigger.
	if _, err = conn.Exec(ctx, `ALTER TABLE equipment DISABLE TRIGGER privacy_upload_equipment_pointer_deferred`); err != nil {
		t.Fatal(err)
	}
	_, insertLegacyErr := conn.Exec(ctx, `INSERT INTO equipment(id,asset_tag,name,type,status,image_object_key,image_content_type,image_size_bytes) VALUES($1,$2,'Legacy pointer','Boat','Operational','equipment/legacy.png','image/png',8)`, legacyID, "LG-"+uuid.NewString()[:8])
	_, enableTriggerErr := conn.Exec(ctx, `ALTER TABLE equipment ENABLE TRIGGER privacy_upload_equipment_pointer_deferred`)
	if insertLegacyErr != nil {
		t.Fatal(insertLegacyErr)
	}
	if enableTriggerErr != nil {
		t.Fatal(enableTriggerErr)
	}
	legacy, err := q.GetEquipmentByID(ctx, legacyID)
	if err != nil {
		t.Fatal(err)
	}
	legacyIntent, legacyToken := seedConfirmedUploadIntent(t, ctx, conn, actorID, nil, "EQUIPMENT_PHOTO", legacyID, "equipment/replacement.png", 9)
	if _, err = q.UpdateEquipmentWithAudit(ctx, dbgen.UpdateEquipmentWithAuditParams{
		EquipmentID: legacyID, ExpectedUpdatedAt: legacy.UpdatedAt, AssetTag: legacy.AssetTag, Name: legacy.Name, Type: legacy.Type,
		Status: legacy.Status, Notes: legacy.Notes, ImageObjectKey: stringAddress("equipment/replacement.png"), ImageContentType: stringAddress("image/png"),
		ImageSizeBytes: int64Address(9), ImageUploadIntentID: &legacyIntent, UploadHoldToken: legacyToken, ActorUserID: &actorID,
	}); err == nil {
		t.Fatal("legacy pointer was replaced before migration or purge")
	}
	legacy, err = q.GetEquipmentByID(ctx, legacyID)
	if err != nil || legacy.ImageObjectKey == nil || *legacy.ImageObjectKey != "equipment/legacy.png" {
		t.Fatalf("legacy pointer changed: %#v err=%v", legacy, err)
	}
	assertUploadStatus(t, ctx, conn, legacyIntent, "PUT_CONFIRMED")

	reservationID, reservationToken := uuid.New(), randomBytes(t, 32)
	if _, err = conn.Exec(ctx, `SELECT privacy_upload_begin($1,NULL,$2,'EQUIPMENT_PHOTO',$3,'private-media','image/png',9,$4)`, reservationID, actorID, uuid.New(), reservationToken); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `SELECT privacy_upload_finalize($1,NULL,'x25519-aes256gcm-hkdfsha256/upload-intent-v1','X25519-HKDF-SHA256-AES-256-GCM','upload-test-key',$2,$3,$4,'upload-test-digest',$5,$6)`, reservationID, randomBytes(t, 32), randomBytes(t, 12), randomBytes(t, 32), randomBytes(t, 32), randomBytes(t, 32)); err == nil {
		t.Fatal("finalize accepted a null token")
	}
}

func TestConcurrentEquipmentPhotoReplacementAttachesOnlyTheCommittedWinner(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	q := dbgen.New(pool)
	actorID, equipmentID := uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Concurrent upload integration',$2,'hash','1990-01-01')`, actorID, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actorID); err != nil {
		t.Fatal(err)
	}
	equipment, err := q.CreateEquipmentWithAudit(ctx, dbgen.CreateEquipmentWithAuditParams{ID: equipmentID, AssetTag: "CR-" + uuid.NewString()[:8], Name: "Concurrent replacement", Type: "Boat", Status: "Operational", ActorUserID: &actorID})
	if err != nil {
		t.Fatal(err)
	}
	type candidate struct {
		intent uuid.UUID
		token  []byte
		key    string
		size   int64
	}
	candidates := []candidate{{key: "equipment/concurrent-a.png", size: 10}, {key: "equipment/concurrent-b.png", size: 11}}
	for index := range candidates {
		candidates[index].intent, candidates[index].token = seedConfirmedUploadIntent(t, ctx, pool, actorID, nil, "EQUIPMENT_PHOTO", equipmentID, candidates[index].key, candidates[index].size)
	}
	type result struct {
		index int
		err   error
	}
	results := make(chan result, len(candidates))
	var group sync.WaitGroup
	for index, upload := range candidates {
		group.Add(1)
		go func() {
			defer group.Done()
			_, updateErr := q.UpdateEquipmentWithAudit(ctx, dbgen.UpdateEquipmentWithAuditParams{
				EquipmentID: equipmentID, ExpectedUpdatedAt: equipment.UpdatedAt, AssetTag: equipment.AssetTag, Name: equipment.Name,
				Type: equipment.Type, Status: equipment.Status, Notes: equipment.Notes, ImageObjectKey: &upload.key,
				ImageContentType: stringAddress("image/png"), ImageSizeBytes: &upload.size, ImageUploadIntentID: &upload.intent,
				UploadHoldToken: upload.token, ActorUserID: &actorID,
			})
			results <- result{index: index, err: updateErr}
		}()
	}
	group.Wait()
	close(results)
	winner := -1
	for result := range results {
		if result.err == nil {
			if winner >= 0 {
				t.Fatal("both concurrent replacements committed")
			}
			winner = result.index
		}
	}
	if winner < 0 {
		t.Fatal("neither concurrent replacement committed")
	}
	for index, upload := range candidates {
		want := "PUT_CONFIRMED"
		if index == winner {
			want = "ATTACHED"
		}
		assertUploadStatus(t, ctx, pool, upload.intent, want)
	}
}

func seedConfirmedUploadIntent(t *testing.T, ctx context.Context, conn dbgen.DBTX, actorID uuid.UUID, subjectID *uuid.UUID, sourceKind string, sourceRef uuid.UUID, objectKey string, sizeBytes int64) (uuid.UUID, []byte) {
	t.Helper()
	intentID, token := seedPreparedUploadIntent(t, ctx, conn, actorID, subjectID, sourceKind, sourceRef, objectKey, sizeBytes)
	if _, err := conn.Exec(ctx, `SELECT privacy_upload_confirm_put($1,$2)`, intentID, token); err != nil {
		t.Fatal(err)
	}
	return intentID, token
}

func seedPreparedUploadIntent(t *testing.T, ctx context.Context, conn dbgen.DBTX, actorID uuid.UUID, subjectID *uuid.UUID, sourceKind string, sourceRef uuid.UUID, objectKey string, sizeBytes int64) (uuid.UUID, []byte) {
	t.Helper()
	intentID := uuid.New()
	token, encapsulation, nonce, ciphertext, locatorDigest := randomBytes(t, 32), randomBytes(t, 32), randomBytes(t, 12), randomBytes(t, 32), randomBytes(t, 32)
	if _, err := conn.Exec(ctx, `SELECT privacy_upload_begin($1,$2,$3,$4,$5,'private-media','image/png',$6,$7)`, intentID, subjectID, actorID, sourceKind, sourceRef, sizeBytes, token); err != nil {
		t.Fatal(err)
	}
	commitment := sha256.Sum256([]byte(objectKey))
	if _, err := conn.Exec(ctx, `SELECT privacy_upload_finalize($1,$2,'x25519-aes256gcm-hkdfsha256/upload-intent-v1','X25519-HKDF-SHA256-AES-256-GCM','upload-test-key',$3,$4,$5,'upload-test-digest',$6,$7)`, intentID, token, encapsulation, nonce, ciphertext, locatorDigest, commitment[:]); err != nil {
		t.Fatal(err)
	}
	return intentID, token
}

func assertUploadStatus(t *testing.T, ctx context.Context, conn dbgen.DBTX, intentID uuid.UUID, want string) {
	t.Helper()
	var got string
	if err := conn.QueryRow(ctx, `SELECT status FROM privacy_protected.object_upload_intent_events WHERE intent_id=$1 ORDER BY sequence DESC LIMIT 1`, intentID).Scan(&got); err != nil || got != want {
		t.Fatalf("intent=%s status=%q want=%q err=%v", intentID, got, want, err)
	}
}

func randomBytes(t *testing.T, size int) []byte {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return value
}

func stringAddress(value string) *string { return &value }
func int64Address(value int64) *int64    { return &value }
