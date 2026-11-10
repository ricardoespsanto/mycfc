//go:build integration

package handlers

import (
	"context"
	"errors"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestModernVariationStoreAdaptsAndRetiresStartOnlyWithBandPlan(t *testing.T) {
	w := newRhythmDomainFixture(t)
	setPublicationFixtureEnabled(t, w.publicationDomainFixture, true)
	sessions := addPublicationMixedSchedules(t, w.publicationDomainFixture)
	// This fixture graph belongs to the actual START_ONLY workout; BAND and DAY
	// siblings reproduce the real weekly plan. No precision is repaired by the store.
	w.exec(t, `UPDATE training_session_segments SET session_id=$1 WHERE id=(SELECT segment_id FROM training_segment_blocks WHERE id=$2)`, sessions[1], w.block)
	before := modernVariationScheduleJSON(t, w)
	store := PostgresStructuredTrainingStore{Pool: w.web}
	in := dbgen.CreateTrainingVariationParams{PlanID: w.plan, TargetMembershipID: &w.secondMember, SubjectKind: dbgen.TrainingVariationSubjectKindBLOCK, SubjectID: w.block, Operation: dbgen.TrainingVariationOperationOVERRIDE, ChangeSummary: "Explicit canoe reference on start-only workout", Patch: []byte(`{"intensity_profile_id":"` + w.canoe.String() + `"}`), CreatedByID: w.actor}
	outsider := uuid.New()
	w.exec(t, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic unrelated adult',$2,'hash','1990-01-01')`, outsider, outsider.String()+"@example.test")
	unauthorized := in
	unauthorized.CreatedByID = outsider
	if _, err := store.CreateTrainingVariation(t.Context(), unauthorized); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("unrelated actor adapted plan", err)
	}
	created, err := store.CreateTrainingVariation(t.Context(), in)
	if err != nil || created.ID == uuid.Nil {
		t.Fatal("actual store still invokes v1 whole-plan guard", err)
	}
	actorCtx := context.WithValue(t.Context(), currentUserKey{}, CurrentUser{ID: w.actor})
	retire := dbgen.RetireTrainingVariationParams{ID: created.ID, Version: created.Version, RetiredByID: &w.actor}
	outsiderCtx := context.WithValue(t.Context(), currentUserKey{}, CurrentUser{ID: outsider, IsAdmin: true})
	forged := retire
	forged.RetiredByID = &outsider
	if _, err = store.RetireTrainingVariation(outsiderCtx, forged); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("forged admin retired another plan's adaptation", err)
	}
	stale := retire
	stale.Version++
	if count, err := store.RetireTrainingVariation(actorCtx, stale); err != nil || count != 0 {
		t.Fatal("stale version retired adaptation", count, err)
	}
	if count, err := store.RetireTrainingVariation(actorCtx, retire); err != nil || count != 1 {
		t.Fatal("actual store could not retire start-only adaptation", count, err)
	}
	if count, err := store.RetireTrainingVariation(actorCtx, retire); err != nil || count != 0 {
		t.Fatal("retired adaptation replay changed state", count, err)
	}
	in.ChangeSummary = "Explicit reviewed replacement after retirement"
	replacement, err := store.CreateTrainingVariation(t.Context(), in)
	if err != nil || replacement.ID == created.ID || replacement.ID == uuid.Nil {
		t.Fatal("explicit replacement rejected", err)
	}
	var active, all int
	if err = w.web.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE is_active),count(*) FROM training_variations WHERE plan_id=$1`, w.plan).Scan(&active, &all); err != nil || active != 1 || all != 2 {
		t.Fatalf("unexpected writes active=%d all=%d error=%v", active, all, err)
	}
	if after := modernVariationScheduleJSON(t, w); after != before {
		t.Fatal("instruction adaptation changed native schedule precision or times")
	}
}
func modernVariationScheduleJSON(t *testing.T, w rhythmDomainFixture) string {
	t.Helper()
	var value string
	if err := w.web.QueryRow(t.Context(), `SELECT jsonb_agg(jsonb_build_array(id,schedule_precision,scheduled_on,starts_at,ends_at,day_band) ORDER BY id)::text FROM training_sessions WHERE plan_id=$1`, w.plan).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
