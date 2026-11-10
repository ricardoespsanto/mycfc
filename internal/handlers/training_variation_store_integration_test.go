//go:build integration

package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func holdTrainingFixtureRow(t *testing.T, f *publicationDomainFixture, sql string, id uuid.UUID, action func()) {
	t.Helper()
	tx, err := f.root.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), sql, id); err != nil {
		t.Fatal(err)
	}
	action()
}

func TestModernVariationStoreFencesSubjectAndReferenceChanges(t *testing.T) {
	w := newRhythmDomainFixture(t)
	ctx := t.Context()
	store := PostgresStructuredTrainingStore{Pool: w.web}
	segment, gymSegment, gymBlock, exercise := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := w.web.QueryRow(ctx, `SELECT segment_id FROM training_segment_blocks WHERE id=$1`, w.block).Scan(&segment); err != nil {
		t.Fatal(err)
	}
	w.exec(t, `INSERT INTO training_session_segments(id,session_id,position,modality,title) VALUES($1,$2,2,'GYM','Synthetic gym prescription')`, gymSegment, w.session)
	w.exec(t, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,title,instructions) VALUES($1,$2,1,'MAIN','Gym work','Synthetic instructions')`, gymBlock, gymSegment)
	w.exec(t, `INSERT INTO gym_block_prescriptions(block_id,structure,objective) VALUES($1,'STRAIGHT_SETS','MOBILITY')`, gymBlock)
	w.exec(t, `INSERT INTO gym_exercises(id,block_id,position,name,sets,repetitions) VALUES($1,$2,1,'Synthetic lift',2,12)`, exercise, gymBlock)
	in := dbgen.CreateTrainingVariationParams{PlanID: w.plan, TargetMembershipID: &w.membership, SubjectKind: dbgen.TrainingVariationSubjectKindBLOCK, SubjectID: w.block, Operation: dbgen.TrainingVariationOperationOMIT, ChangeSummary: "Reviewed individual variation", Patch: []byte("{}"), CreatedByID: w.actor}
	reject := func(t *testing.T, in dbgen.CreateTrainingVariationParams, want error) {
		t.Helper()
		got, err := store.CreateTrainingVariation(t.Context(), in)
		if !errors.Is(err, want) || got.ID != uuid.Nil {
			t.Fatalf("variation result=%s error=%v, want %v", got.ID, err, want)
		}
	}
	profilePatch := func(id string) []byte { return []byte(`{"intensity_profile_id":"` + id + `"}`) }
	for _, patch := range [][]byte{
		[]byte("["),
		[]byte(`{"intensity_profile_id":42}`),
		profilePatch(strings.ToUpper(w.kayak.String())),
		profilePatch(uuid.NewString()),
	} {
		bad := in
		bad.Operation, bad.Patch = dbgen.TrainingVariationOperationOVERRIDE, patch
		reject(t, bad, errPublicationRhythmReference)
	}
	unknown := in
	unknown.SubjectKind = "UNKNOWN"
	reject(t, unknown, pgx.ErrNoRows)
	missingPlan := in
	missingPlan.PlanID = uuid.New()
	reject(t, missingPlan, pgx.ErrNoRows)
	for _, tc := range []struct {
		name, sql string
		id        uuid.UUID
		patch     []byte
	}{
		{"placement head", `SELECT revision FROM training_placement_heads WHERE subject_user_id=$1 FOR UPDATE`, w.subject, nil},
		{"athlete identity", `SELECT id FROM users WHERE id=$1 FOR UPDATE`, w.subject, nil},
		{"exercise subject", `SELECT id FROM gym_exercises WHERE id=$1 FOR UPDATE`, exercise, nil},
		{"rhythm profile", `SELECT id FROM water_intensity_profiles WHERE id=$1 FOR UPDATE`, w.kayak, profilePatch(w.kayak.String())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blocked := in
			if tc.id == exercise {
				blocked.SubjectKind, blocked.SubjectID = dbgen.TrainingVariationSubjectKindGYMEXERCISE, exercise
			}
			if tc.patch != nil {
				blocked.Operation, blocked.Patch = dbgen.TrainingVariationOperationOVERRIDE, tc.patch
			}
			holdTrainingFixtureRow(t, w.publicationDomainFixture, tc.sql, tc.id, func() {
				reject(t, blocked, errStructuredTrainingPublicationConflict)
			})
		})
	}
	var created dbgen.TrainingVariation
	for _, subject := range []struct {
		kind dbgen.TrainingVariationSubjectKind
		id   uuid.UUID
	}{
		{dbgen.TrainingVariationSubjectKindSEGMENT, segment},
		{dbgen.TrainingVariationSubjectKindWATERSTEP, w.work},
		{dbgen.TrainingVariationSubjectKindGYMEXERCISE, exercise},
	} {
		mutation := in
		mutation.SubjectKind, mutation.SubjectID = subject.kind, subject.id
		var err error
		created, err = store.CreateTrainingVariation(ctx, mutation)
		if err != nil || created.SubjectID != subject.id || created.SubjectKind != subject.kind {
			t.Fatalf("persisted variation %+v, %v", created, err)
		}
	}
	actor := context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: w.actor})
	retire := dbgen.RetireTrainingVariationParams{ID: created.ID, Version: created.Version, RetiredByID: &w.actor}
	holdTrainingFixtureRow(t, w.publicationDomainFixture, `SELECT id FROM training_variations WHERE id=$1 FOR UPDATE`, created.ID, func() {
		if count, err := store.RetireTrainingVariation(actor, retire); count != 0 || !errors.Is(err, errStructuredTrainingPublicationConflict) {
			t.Fatalf("contended retirement count=%d, %v", count, err)
		}
	})
	retire.ID = uuid.New()
	if count, err := store.RetireTrainingVariation(actor, retire); count != 0 || !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing retirement count=%d, %v", count, err)
	}
	var total, active int
	if err := w.web.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE is_active) FROM training_variations WHERE plan_id=$1`, w.plan).Scan(&total, &active); err != nil || total != 3 || active != 3 {
		t.Fatalf("unsuccessful variation or retirement changed rows: total=%d active=%d error=%v", total, active, err)
	}
	setPublicationFixtureEnabled(t, w.publicationDomainFixture, true)
	addPublicationMixedSchedules(t, w.publicationDomainFixture)
	setPublicationFixtureEnabled(t, w.publicationDomainFixture, false)
	reject(t, in, ErrTrainingScheduleWriteUnsupported)
}

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
