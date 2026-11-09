//go:build integration

package db

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exercise the editable person classification and retained legacy training
// consumers together. Fixture publications are persisted snapshots, not a claim
// that this test exercises the separate publication workflow.
func TestSportCorrectionsPreserveLegacyTrainingConsumersAndAuthority(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	admin, member, season, membership := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	plan, session, document, publication, prescription := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, user := range []uuid.UUID{admin, member} {
		exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Consumer test',$2,'hash','1990-01-01')`, user, user.String()+"@example.test")
	}
	exec(`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin)
	var programme, legacy uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM modalities WHERE code='K1'`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(mustLisbon(t))
	exec(`INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Consumer season',$3,$4)`, season, "SC_"+uuid.NewString()[:8], today.AddDate(0, 0, -10), today.AddDate(0, 0, 10))
	exec(`INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,$4,$5)`, membership, member, season, programme, today.AddDate(0, 0, -5))
	exec(`INSERT INTO membership_modalities(membership_id,modality_id) VALUES($1,$2)`, membership, legacy)
	exec(`INSERT INTO training_plans(id,title,programme_id,created_by_id) VALUES($1,'Retained plan',$2,$3)`, plan, programme, admin)
	exec(`INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,modality_id,created_by_id) VALUES($1,$2,'Retained K1 session',$3,$4,$5,$6)`, session, plan, today.Add(-48*time.Hour), today.Add(-47*time.Hour), legacy, admin)
	exec(`INSERT INTO competition_documents(id,title,url,source,reviewed_on,modality_id,programme_id,author_id) VALUES($1,'Retained K1 document','https://example.test/results','Synthetic fixture',CURRENT_DATE,$2,$3,$4)`, document, legacy, programme, admin)
	exec(`INSERT INTO training_plan_publications(id,plan_id,revision,source_updated_at,change_summary,published_by_id) SELECT $1,id,1,updated_at,'Synthetic retained snapshot',$3 FROM training_plans WHERE id=$2`, publication, plan, admin)
	exec(`INSERT INTO training_prescriptions(id,publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256) VALUES($1,$2,$3,$4,$5,jsonb_build_object('legacy_modality_id',$6::text,'craft','K1'),repeat('a',64))`, prescription, publication, session, membership, member, legacy.String())
	// Compare complete persisted rows, not merely counts or displayed names.
	fingerprint := func() string {
		t.Helper()
		var value string
		err := pool.QueryRow(ctx, `SELECT jsonb_build_object(
 'membership',(SELECT to_jsonb(r) FROM user_memberships r WHERE id=$1),
 'legacy_links',(SELECT jsonb_agg(to_jsonb(r) ORDER BY modality_id) FROM membership_modalities r WHERE membership_id=$1),
 'session',(SELECT to_jsonb(r) FROM training_sessions r WHERE id=$2),
 'document',(SELECT to_jsonb(r) FROM competition_documents r WHERE id=$3),
 'publication',(SELECT to_jsonb(r) FROM training_plan_publications r WHERE id=$4),
 'prescription',(SELECT to_jsonb(r) FROM training_prescriptions r WHERE id=$5),
 'roles',(SELECT jsonb_agg(to_jsonb(r) ORDER BY role_id) FROM user_platform_roles r WHERE user_id=$6),
 'grants',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM staff_grants r WHERE user_id=$6))::text`, membership, session, document, publication, prescription, member).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := fingerprint()
	queries := dbgen.New(pool)
	assertConsumer := func() {
		t.Helper()
		rows, err := queries.ListTrainingSessionsForAthlete(ctx, dbgen.ListTrainingSessionsForAthleteParams{UserID: member, RowLimit: 100})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if row.ID == session {
				found = row.ModalityName != nil && *row.ModalityName == "Caiaque individual" && row.PrescriptionAvailable
			}
		}
		if !found {
			t.Fatal("retained session lost its original legacy label or prescription link")
		}
		edit, err := queries.GetTrainingSessionForEdit(ctx, session)
		if err != nil || edit.ModalityID == nil || *edit.ModalityID != legacy {
			t.Fatalf("stored session reference changed: %#v, %v", edit, err)
		}
		crews, err := queries.ListStructuredCrewModalities(ctx)
		var codes []string
		for _, craft := range crews {
			codes = append(codes, craft.Code)
		}
		if err != nil || !reflect.DeepEqual(codes, []string{"C2", "K2", "K4"}) {
			t.Fatalf("code-based crew catalog=%v err=%v", codes, err)
		}
		if after := fingerprint(); after != before {
			t.Fatal("sport correction rewrote participation, legacy references, snapshots or authority")
		}
	}
	assertConsumer()
	svc := SportAssignmentService{Pool: pool}
	for _, change := range []SportAssignmentCorrection{
		{Kind: SportingModalityAssignment, Codes: []string{"CANOEING", "KAYAK_POLO", "SUP"}},
		{Kind: CanoeCraftAssignment, Codes: []string{"K2", "C2", "K4"}},
		{Kind: SportingModalityAssignment, Codes: []string{"SUP"}},
	} {
		change.ActorID, change.MemberID, change.Reason = admin, member, "Synthetic sporting correction"
		if err := svc.Replace(ctx, change); err != nil {
			t.Fatal(err)
		}
		if change.Kind == SportingModalityAssignment {
			selected, err := queries.ListPersonSportingModalities(ctx, member)
			var codes []string
			for _, sport := range selected {
				codes = append(codes, sport.Code)
			}
			if err != nil || !reflect.DeepEqual(codes, change.Codes) {
				t.Fatalf("person sport read-model=%v want=%v err=%v", codes, change.Codes, err)
			}
		}
		var crafts []string
		if err := pool.QueryRow(ctx, `SELECT COALESCE(array_agg(craft_code::text ORDER BY craft_code),ARRAY[]::text[]) FROM person_canoe_craft_classes WHERE user_id=$1`, member).Scan(&crafts); err != nil {
			t.Fatal(err)
		}
		wantCrafts := []string{}
		if change.Kind == CanoeCraftAssignment {
			wantCrafts = []string{"C2", "K2", "K4"}
		}
		if !reflect.DeepEqual(crafts, wantCrafts) {
			t.Fatalf("person craft read-model=%v want=%v", crafts, wantCrafts)
		}
		assertConsumer()
	}
	// Selecting Kayak Polo did not confer coach/admin power on this athlete.
	if err := svc.Replace(ctx, SportAssignmentCorrection{ActorID: member, MemberID: member, Kind: SportingModalityAssignment, Codes: []string{"CANOEING"}, Reason: "Synthetic unauthorized correction"}); !errors.Is(err, ErrSportAssignmentForbidden) {
		t.Fatalf("classification conferred correction authority: %v", err)
	}
	assertConsumer()
}
