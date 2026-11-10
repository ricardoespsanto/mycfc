//go:build integration

package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func editOriginalParams(w focusedWaterFixture, actor uuid.UUID, r dbgen.LockFocusedWorkoutForEditRow) dbgen.CheckFocusedWorkoutOriginalEligibilityParams {
	return dbgen.CheckFocusedWorkoutOriginalEligibilityParams{ActorID: actor, PlanID: w.plan, SessionID: r.ID, OriginalStatus: r.Status, OriginalSchedulePrecision: r.SchedulePrecision, OriginalStartsAt: r.StartsAt, OriginalScheduledOn: r.ScheduledOn}
}
func editSessionParams(w focusedWaterFixture, actor uuid.UUID, r dbgen.LockFocusedWorkoutForEditRow) dbgen.UpdateFocusedWorkoutSessionParams {
	return dbgen.UpdateFocusedWorkoutSessionParams{ActorID: actor, PlanID: w.plan, SessionID: r.ID, ExpectedUpdatedAt: r.UpdatedAt, Title: r.Title, Description: r.Description, StartsAt: r.StartsAt, EndsAt: r.EndsAt, SchedulePrecision: r.SchedulePrecision, ScheduledOn: r.ScheduledOn, DayBand: r.DayBand}
}
func beginEdit(t *testing.T, w focusedWaterFixture, s TrainingScheduleState, actor, id uuid.UUID) (pgx.Tx, *dbgen.Queries, dbgen.LockFocusedWorkoutForEditRow) {
	t.Helper()
	tx, err := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	recipientSchemaExec(t, tx, `SELECT public.training_schedule_admit_partial($1,$2)`, s.DatabaseIncarnation, s.StateEpoch)
	if _, err = VerifyTrainingWorkoutGraphRuntime(t.Context(), tx, TrainingScheduleCapabilities{ReaderVersion: 5, WriterVersion: 2}); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, tx, `SELECT id FROM users WHERE id=$1 FOR SHARE`, actor)
	recipientSchemaExec(t, tx, `SELECT user_id FROM user_platform_roles WHERE user_id=$1 FOR SHARE`, actor)
	recipientSchemaExec(t, tx, `SELECT id FROM staff_grants WHERE user_id=$1 FOR SHARE`, actor)
	q := dbgen.New(tx)
	if _, err = q.LockFocusedWaterCreatePlan(t.Context(), dbgen.LockFocusedWaterCreatePlanParams{ActorID: actor, PlanID: w.plan}); err != nil {
		t.Fatal(err)
	}
	row, err := q.LockFocusedWorkoutForEdit(t.Context(), dbgen.LockFocusedWorkoutForEditParams{ActorID: actor, PlanID: w.plan, SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	return tx, q, row
}
func TestWorkoutEditLegacyMetadataAndOtherScopedEditor(t *testing.T) {
	w := newFocusedWaterFixture(t)
	s := w.prepare(t)
	id := uuid.New()
	var modality uuid.UUID
	if err := w.web.QueryRow(t.Context(), `SELECT id FROM modalities ORDER BY id LIMIT 1`).Scan(&modality); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO training_sessions(id,plan_id,title,description,starts_at,ends_at,modality_id,created_by_id) SELECT $1,id,'Synthetic exact legacy','Original notes',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours',$2,$3 FROM training_plans WHERE id=$4`, id, modality, w.actor, w.plan)
	editor := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic scoped editor',$2,'hash','1980-01-01')`, editor, editor.String()+"@example.test")
	recipientSchemaExec(t, w.web, `INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, editor, w.programme, w.actor)
	tx, q, original := beginEdit(t, w, s, editor, id)
	if original.ScheduledOn.Valid || original.ModalityID == nil || *original.ModalityID != modality || original.CreatedByID == editor {
		t.Fatal("fixture did not exercise raw legacy/other creator")
	}
	eligible, err := q.CheckFocusedWorkoutOriginalEligibility(t.Context(), editOriginalParams(w, editor, original))
	if err != nil || !eligible.Eligible {
		t.Fatal("future original not eligible", err)
	}
	input := editSessionParams(w, editor, original)
	input.Title = "Synthetic exact legacy edited"
	input.Description = "Edited own words"
	if _, err = q.UpdateFocusedWorkoutSession(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if eligible, err = q.CheckFocusedWorkoutOriginalEligibility(t.Context(), editOriginalParams(w, editor, original)); err != nil || !eligible.Eligible {
		t.Fatal("final old eligibility failed", err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := dbgen.New(w.web).GetFocusedWorkoutSession(t.Context(), dbgen.GetFocusedWorkoutSessionParams{ActorID: editor, PlanID: w.plan, SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != original.ID || current.PlanID != original.PlanID || current.EntryKind != original.EntryKind || current.CreatedByID != original.CreatedByID || !current.CreatedAt.Time.Equal(original.CreatedAt.Time) || current.ModalityID == nil || *current.ModalityID != modality || current.ScheduledOn.Valid || current.Title != input.Title {
		t.Fatal("edit converted/reassigned raw legacy metadata")
	}
	if _, err = dbgen.New(w.web).UpdateFocusedWorkoutSession(t.Context(), input); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("old session version silently refreshed", err)
	}
	recipientSchemaExec(t, w.web, `UPDATE staff_grants SET revoked_at=clock_timestamp(),revoked_by_id=$1 WHERE user_id=$2`, w.actor, editor)
	input.ExpectedUpdatedAt = current.UpdatedAt
	if _, err = dbgen.New(w.web).UpdateFocusedWorkoutSession(t.Context(), input); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("revoked editor wrote", err)
	}
}

func editGraphJSON(t *testing.T, w focusedWaterFixture, id uuid.UUID) string {
	t.Helper()
	var raw string
	err := w.web.QueryRow(t.Context(), `SELECT jsonb_build_array(
 (SELECT to_jsonb(x) FROM training_plans x WHERE id=$1),
 (SELECT to_jsonb(x) FROM training_sessions x WHERE id=$2),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM training_session_segments x WHERE session_id=$2),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM training_segment_blocks x JOIN training_session_segments s ON s.id=x.segment_id WHERE s.session_id=$2),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM water_work_steps x JOIN training_segment_blocks b ON b.id=x.block_id JOIN training_session_segments s ON s.id=b.segment_id WHERE s.session_id=$2),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM gym_exercises x JOIN training_segment_blocks b ON b.id=x.block_id JOIN training_session_segments s ON s.id=b.segment_id WHERE s.session_id=$2))::text`, w.plan, id).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func editComparable(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	delete(out, "position")
	delete(out, "updated_at")
	return out
}
func TestWorkoutEditStagingPreservesIDsAndRollsBack(t *testing.T) {
	w := newFocusedWaterFixture(t)
	s := w.prepare(t)
	id, err := graphBareSession(t, w, dbgen.TrainingEntryKindTRAINING, "START_ONLY")
	if err != nil {
		t.Fatal(err)
	}
	wa, gy, b1, b2, bg, st1, st2, ex1, ex2 := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_segments(id,session_id,position,modality,title) VALUES($1,$3,2147483647,'WATER','Legacy water'),($2,$3,1,'GYM','Legacy gym')`, wa, gy, id)
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$4,2147483647,'MAIN','Water original'),($2,$4,1,'COOL_DOWN','Cooldown original'),($3,$5,1,'MAIN','Gym original')`, b1, b2, bg, wa, gy)
	recipientSchemaExec(t, w.web, `INSERT INTO water_block_prescriptions(block_id,method) VALUES($1,'INTERVALS')`, b1)
	recipientSchemaExec(t, w.web, `INSERT INTO water_work_steps(id,block_id,position,kind,name,repeats,duration_seconds,duration_certainty,recovery_seconds,instructions) VALUES($1,$3,2147483647,'REPEAT_GROUP','Legacy group',2,NULL,NULL,120,'Group notes'),($2,$3,1,'EFFORT','Legacy effort',NULL,240,'EXACT',60,'Effort notes')`, st1, st2, b1)
	recipientSchemaExec(t, w.web, `INSERT INTO gym_block_prescriptions(block_id,structure,objective,rounds) VALUES($1,'CIRCUIT','CUSTOM',2)`, bg)
	recipientSchemaExec(t, w.web, `INSERT INTO gym_exercises(id,block_id,position,name,sets,repetitions,resistance_kind,resistance_value,execution_intent,tempo,notes) VALUES($1,$3,2147483647,'Legacy squat',3,8,'KILOGRAMS',12.5,'CONTROLLED','3-1-1','Squat notes'),($2,$3,1,'Legacy press',2,10,'BODY_WEIGHT',NULL,NULL,NULL,'Press notes')`, ex1, ex2, bg)
	before := editGraphJSON(t, w, id)
	apply := func(fault bool) {
		tx, q, original := beginEdit(t, w, s, w.actor, id)
		segments, err := q.LockFocusedWorkoutSegments(t.Context(), dbgen.LockFocusedWorkoutSegmentsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
		if err != nil {
			t.Fatal(err)
		}
		blocks, err := q.LockFocusedWorkoutBlocks(t.Context(), dbgen.LockFocusedWorkoutBlocksParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
		if err != nil {
			t.Fatal(err)
		}
		steps, err := q.LockFocusedWorkoutWaterSteps(t.Context(), dbgen.LockFocusedWorkoutWaterStepsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
		if err != nil {
			t.Fatal(err)
		}
		exercises, err := q.LockFocusedWorkoutGymExercises(t.Context(), dbgen.LockFocusedWorkoutGymExercisesParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
		if err != nil {
			t.Fatal(err)
		}
		stageSegments := map[uuid.UUID]dbgen.TrainingSessionSegment{}
		for i, row := range segments {
			staged, err := q.StageFocusedWorkoutSegmentPosition(t.Context(), dbgen.StageFocusedWorkoutSegmentPositionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: row.ID, ExpectedUpdatedAt: row.UpdatedAt, ExpectedPosition: row.Position, TemporaryPosition: int32(3 + i)})
			if err != nil {
				t.Fatal(err)
			}
			if !staged.UpdatedAt.Time.Equal(row.UpdatedAt.Time) {
				t.Fatal("stage touched child version")
			}
			stageSegments[row.ID] = staged
		}
		for _, row := range blocks {
			if row.SegmentID == wa {
				pos := int32(3)
				if row.ID == b2 {
					pos = 4
				}
				if _, err = q.StageFocusedWorkoutBlockPosition(t.Context(), dbgen.StageFocusedWorkoutBlockPositionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: row.SegmentID, BlockID: row.ID, ExpectedUpdatedAt: row.UpdatedAt, ExpectedPosition: row.Position, TemporaryPosition: pos}); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, row := range steps {
			pos := int32(3)
			if row.ID == st2 {
				pos = 4
			}
			staged, err := q.StageFocusedWorkoutWaterStepPosition(t.Context(), dbgen.StageFocusedWorkoutWaterStepPositionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: wa, BlockID: b1, StepID: row.ID, ParentStepID: row.ParentStepID, ExpectedUpdatedAt: row.UpdatedAt, ExpectedPosition: row.Position, TemporaryPosition: pos})
			if err != nil {
				t.Fatal(err)
			}
			if !staged.UpdatedAt.Time.Equal(row.UpdatedAt.Time) {
				t.Fatal("water stage version changed")
			}
		}
		for _, row := range exercises {
			pos := int32(3)
			if row.ID == ex2 {
				pos = 4
			}
			if _, err = q.StageFocusedWorkoutGymExercisePosition(t.Context(), dbgen.StageFocusedWorkoutGymExercisePositionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: gy, BlockID: bg, ExerciseID: row.ID, ExpectedUpdatedAt: row.UpdatedAt, ExpectedPosition: row.Position, TemporaryPosition: pos}); err != nil {
				t.Fatal(err)
			}
		}
		for _, row := range segments {
			pos := int32(1)
			if row.ID == gy {
				pos = 2
			}
			got, err := q.UpdateFocusedWorkoutSegment(t.Context(), dbgen.UpdateFocusedWorkoutSegmentParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: row.ID, ExpectedUpdatedAt: row.UpdatedAt, Position: pos, Modality: row.Modality, Title: row.Title, Location: row.Location, PlannedDurationMinutes: row.PlannedDurationMinutes, PlannedStartOffsetMinutes: row.PlannedStartOffsetMinutes, TransitionDurationMinutes: row.TransitionDurationMinutes, EquipmentNotes: row.EquipmentNotes})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(editComparable(t, row), editComparable(t, got)) {
				t.Fatal("segment identity/raw metadata changed")
			}
		}
		for _, row := range steps {
			pos := int32(1)
			if row.ID == st2 {
				pos = 2
			}
			got, err := q.UpdateFocusedWorkoutWaterStep(t.Context(), dbgen.UpdateFocusedWorkoutWaterStepParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: wa, BlockID: b1, StepID: row.ID, ParentStepID: row.ParentStepID, ExpectedUpdatedAt: row.UpdatedAt, Position: pos, Kind: row.Kind, Name: row.Name, Repeats: row.Repeats, DurationSeconds: row.DurationSeconds, DurationCertainty: row.DurationCertainty, DistanceMetres: row.DistanceMetres, DistanceCertainty: row.DistanceCertainty, RecoverySeconds: row.RecoverySeconds, IntensityCode: row.IntensityCode, CadenceSpm: row.CadenceSpm, DrillFocus: row.DrillFocus, DrillFormat: row.DrillFormat, RoleNotes: row.RoleNotes, Instructions: row.Instructions})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(editComparable(t, row), editComparable(t, got)) {
				t.Fatal("step identity/parent/raw payload changed")
			}
		}
		for _, row := range exercises {
			pos := int32(1)
			if row.ID == ex2 {
				pos = 2
			}
			got, err := q.UpdateFocusedWorkoutGymExercise(t.Context(), dbgen.UpdateFocusedWorkoutGymExerciseParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: gy, BlockID: bg, ExerciseID: row.ID, ExpectedUpdatedAt: row.UpdatedAt, Position: pos, Name: row.Name, Sets: row.Sets, Repetitions: row.Repetitions, DurationSeconds: row.DurationSeconds, DistanceMetres: row.DistanceMetres, RecoverySeconds: row.RecoverySeconds, ResistanceKind: row.ResistanceKind, ResistanceValue: row.ResistanceValue, ResistanceText: row.ResistanceText, ExecutionIntent: row.ExecutionIntent, Tempo: row.Tempo, Notes: row.Notes})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(editComparable(t, row), editComparable(t, got)) {
				t.Fatal("exercise identity/raw payload changed")
			}
		}
		for _, row := range blocks {
			pos := row.Position
			if row.SegmentID == wa {
				pos = 1
				if row.ID == b2 {
					pos = 2
				}
			}
			words := row.Instructions
			if fault && row.ID == bg {
				words = "x"
			}
			got, err := q.UpdateFocusedWorkoutBlock(t.Context(), dbgen.UpdateFocusedWorkoutBlockParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: row.SegmentID, BlockID: row.ID, ExpectedUpdatedAt: row.UpdatedAt, Position: pos, Purpose: row.Purpose, Title: row.Title, Instructions: words})
			if fault && row.ID == bg {
				if err == nil {
					t.Fatal("last fault accepted")
				}
				_ = tx.Rollback(t.Context())
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(editComparable(t, row), editComparable(t, got)) {
				t.Fatal("block metadata changed")
			}
		}
		eligible, err := q.CheckFocusedWorkoutOriginalEligibility(t.Context(), editOriginalParams(w, w.actor, original))
		if err != nil || !eligible.Eligible {
			t.Fatal("final eligibility", err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	apply(true)
	if got := editGraphJSON(t, w, id); got != before {
		t.Fatal("fault failed to restore complete graph/versions/positions")
	}
	apply(false)
	q := dbgen.New(w.web)
	rows, err := q.ListFocusedWorkoutSegments(t.Context(), dbgen.ListFocusedWorkoutSegmentsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil || len(rows) != 2 || rows[0].ID != wa || rows[0].Position != 1 || rows[1].ID != gy || rows[1].Position != 2 {
		t.Fatal("safe final segment positions", err)
	}
}

func editCurrentWeek(t *testing.T, w *focusedWaterFixture) {
	t.Helper()
	recipientSchemaExec(t, w.web, `UPDATE training_plans SET week_start=date_trunc('week',CURRENT_DATE)::date WHERE id=$1`, w.plan)
	if err := w.web.QueryRow(t.Context(), `SELECT week_start FROM training_plans WHERE id=$1`, w.plan).Scan(&w.day); err != nil {
		t.Fatal(err)
	}
	var enough bool
	if err := w.web.QueryRow(t.Context(), `SELECT ((week_start+7)::timestamp AT TIME ZONE 'Europe/Lisbon')-clock_timestamp()>interval '3 seconds' FROM training_plans WHERE id=$1`, w.plan).Scan(&enough); err != nil {
		t.Fatal(err)
	}
	if !enough {
		t.Skip("actual week boundary leaves no stable synthetic future window")
	}
}
func TestWorkoutEditOriginalClockAndProposedPast(t *testing.T) {
	w := newFocusedWaterFixture(t)
	s := w.prepare(t)
	editCurrentWeek(t, &w)
	id := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_sessions(id,plan_id,title,starts_at,entry_kind,created_by_id,schedule_precision,scheduled_on) SELECT $1,id,'Synthetic original future',((week_start+7)::timestamp AT TIME ZONE 'Europe/Lisbon')-interval '1 second','TRAINING',$2,'START_ONLY',week_start+6 FROM training_plans WHERE id=$3`, id, w.actor, w.plan)
	tx, q, original := beginEdit(t, w, s, w.actor, id)
	start, err := q.CheckFocusedWorkoutOriginalEligibility(t.Context(), editOriginalParams(w, w.actor, original))
	if err != nil || !start.Eligible {
		t.Fatal("original future clock", err)
	}
	var past pgtype.Timestamptz
	if err = tx.QueryRow(t.Context(), `SELECT week_start::timestamp AT TIME ZONE 'Europe/Lisbon' FROM training_plans WHERE id=$1`, w.plan).Scan(&past); err != nil {
		t.Fatal(err)
	}
	input := editSessionParams(w, w.actor, original)
	input.StartsAt = past
	input.ScheduledOn = w.day
	input.Description = "Explicit permitted proposed past"
	updated, err := q.UpdateFocusedWorkoutSession(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	final, err := q.CheckFocusedWorkoutOriginalEligibility(t.Context(), editOriginalParams(w, w.actor, original))
	if err != nil || !final.Eligible {
		t.Fatal("own proposed past self-rejected", err)
	}
	next, err := q.CheckFocusedWorkoutOriginalEligibility(t.Context(), dbgen.CheckFocusedWorkoutOriginalEligibilityParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, OriginalStatus: updated.Status, OriginalSchedulePrecision: updated.SchedulePrecision, OriginalStartsAt: updated.StartsAt, OriginalScheduledOn: updated.ScheduledOn})
	if err != nil || next.Eligible {
		t.Fatal("next edit of newly past source allowed", err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}
func seedImminentEdit(t *testing.T, w focusedWaterFixture) uuid.UUID {
	t.Helper()
	id := uuid.New()
	recipientSchemaExec(t, w.web, `WITH instant AS MATERIALIZED(SELECT clock_timestamp()+interval '400 milliseconds' AS start_at) INSERT INTO training_sessions(id,plan_id,title,description,starts_at,entry_kind,created_by_id,schedule_precision,scheduled_on) SELECT $1,$2,'Synthetic imminent','Original unchanged',start_at,'TRAINING',$3,'START_ONLY',(start_at AT TIME ZONE 'Europe/Lisbon')::date FROM instant`, id, w.plan, w.actor)
	return id
}
func TestWorkoutEditFinalClockRollsBackAndWaitClockIsFresh(t *testing.T) {
	w := newFocusedWaterFixture(t)
	s := w.prepare(t)
	editCurrentWeek(t, &w)
	id := seedImminentEdit(t, w)
	pool := w.pool(t)
	cutoff := errors.New("synthetic original cutoff elapsed")
	result, err := WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(ctx context.Context, commands dbgen.DBTX) error {
		if _, err := commands.Exec(ctx, `SELECT public.training_schedule_admit_partial($1,$2)`, s.DatabaseIncarnation, s.StateEpoch); err != nil {
			return err
		}
		q := dbgen.New(commands)
		if _, err := q.LockFocusedWaterCreatePlan(ctx, dbgen.LockFocusedWaterCreatePlanParams{ActorID: w.actor, PlanID: w.plan}); err != nil {
			return err
		}
		original, err := q.LockFocusedWorkoutForEdit(ctx, dbgen.LockFocusedWorkoutForEditParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
		if err != nil {
			return err
		}
		initial, err := q.CheckFocusedWorkoutOriginalEligibility(ctx, editOriginalParams(w, w.actor, original))
		if err != nil {
			return err
		}
		if !initial.Eligible {
			return errors.New("synthetic fixture missed initial window")
		}
		input := editSessionParams(w, w.actor, original)
		input.Description = "Must roll back at final clock"
		if _, err = q.UpdateFocusedWorkoutSession(ctx, input); err != nil {
			return err
		}
		if _, err = commands.Exec(ctx, `SELECT pg_sleep(0.6)`); err != nil {
			return err
		}
		final, err := q.CheckFocusedWorkoutOriginalEligibility(ctx, editOriginalParams(w, w.actor, original))
		if err != nil {
			return err
		}
		if final.Eligible {
			return errors.New("old cutoff not observed")
		}
		return cutoff
	})
	if !errors.Is(err, cutoff) || result.Outcome != SessionTxNotCommitted {
		t.Fatalf("final cutoff did not rollback %+v %v", result, err)
	}
	raw, err := dbgen.New(w.web).GetFocusedWorkoutSession(t.Context(), dbgen.GetFocusedWorkoutSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil || raw.Description != "Original unchanged" {
		t.Fatal("expired write survived", err)
	}
	// Both a waiting lock clock and cancellation decision must be post-wait.
	id = seedImminentEdit(t, w)
	raw, err = dbgen.New(w.web).GetFocusedWorkoutSession(t.Context(), dbgen.GetFocusedWorkoutSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := w.web.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(t.Context())
	recipientSchemaExec(t, blocker, `SELECT id FROM training_sessions WHERE id=$1 FOR UPDATE`, id)
	lockConn, cancelConn := w.f.web(t), w.f.web(t)
	type lockResult struct {
		r   dbgen.LockFocusedWorkoutForEditRow
		err error
	}
	locked := make(chan lockResult, 1)
	cancelled := make(chan error, 1)
	go func() {
		r, err := dbgen.New(lockConn).LockFocusedWorkoutForEdit(t.Context(), dbgen.LockFocusedWorkoutForEditParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
		locked <- lockResult{r, err}
	}()
	go func() {
		_, err := dbgen.New(cancelConn).CancelFocusedWorkoutSession(t.Context(), dbgen.CancelFocusedWorkoutSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, ExpectedUpdatedAt: raw.UpdatedAt, CancellationReason: "Synthetic post-wait reason"})
		cancelled <- err
	}()
	focusedWaterWaitRowLock(t, w.f.root, lockConn.PgConn().PID())
	focusedWaterWaitRowLock(t, w.f.root, cancelConn.PgConn().PID())
	recipientSchemaExec(t, blocker, `SELECT pg_sleep(0.6)`)
	_ = blocker.Rollback(t.Context())
	r := <-locked
	if r.err != nil || !r.r.DecisionAt.Valid || !r.r.DecisionAt.Time.After(raw.StartsAt.Time) {
		t.Fatalf("lock returned pre-wait decision clock %v %+v", r.err, r.r.DecisionAt)
	}
	if err = <-cancelled; !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("waiting cancellation used old clock %v", err)
	}
}
func TestWorkoutEditFutureCivilCancellationIsIrreversible(t *testing.T) {
	w := newFocusedWaterFixture(t)
	s := w.prepare(t)
	q := dbgen.New(w.web)
	for _, tc := range []struct {
		kind      dbgen.TrainingEntryKind
		precision string
	}{{dbgen.TrainingEntryKindREST, "DAY"}, {dbgen.TrainingEntryKindLOGISTICS, "BAND"}, {dbgen.TrainingEntryKindTRAINING, "START_ONLY"}} {
		id, err := graphBareSession(t, w, tc.kind, tc.precision)
		if err != nil {
			t.Fatal(err)
		}
		tx, tq, original := beginEdit(t, w, s, w.actor, id)
		state, err := tq.CheckFocusedWorkoutOriginalEligibility(t.Context(), editOriginalParams(w, w.actor, original))
		if err != nil || !state.Eligible {
			t.Fatal("future civil/known start not eligible", err)
		}
		cancelled, err := tq.CancelFocusedWorkoutSession(t.Context(), dbgen.CancelFocusedWorkoutSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, ExpectedUpdatedAt: original.UpdatedAt, CancellationReason: "Synthetic explicit cancellation"})
		if err != nil {
			t.Fatal(err)
		}
		if cancelled.Status != "CANCELLED" || cancelled.CancelledByID == nil || *cancelled.CancelledByID != w.actor || cancelled.CancelledAt.Time.Before(state.DecisionAt.Time) {
			t.Fatal("cancellation metadata invalid")
		}
		final, err := tq.CheckFocusedWorkoutOriginalEligibility(t.Context(), editOriginalParams(w, w.actor, original))
		if err != nil || !final.Eligible {
			t.Fatal("own cancellation self-rejected original", err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err = q.CancelFocusedWorkoutSession(t.Context(), dbgen.CancelFocusedWorkoutSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, ExpectedUpdatedAt: cancelled.UpdatedAt, CancellationReason: "Overwrite not allowed"}); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal("cancelled status overwritten", err)
		}
		input := editSessionParams(w, w.actor, original)
		input.ExpectedUpdatedAt = cancelled.UpdatedAt
		if _, err = q.UpdateFocusedWorkoutSession(t.Context(), input); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal("cancelled session re-edited", err)
		}
	}
	editCurrentWeek(t, &w)
	today := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_sessions(id,plan_id,title,entry_kind,created_by_id,schedule_precision,scheduled_on) VALUES($1,$2,'Synthetic today untimed','REST',$3,'DAY',(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date)`, today, w.plan, w.actor)
	tx, tq, original := beginEdit(t, w, s, w.actor, today)
	defer tx.Rollback(t.Context())
	state, err := tq.CheckFocusedWorkoutOriginalEligibility(t.Context(), editOriginalParams(w, w.actor, original))
	if err != nil || state.Eligible {
		t.Fatal("today untimed policy invented", err)
	}
	if _, err = tq.CancelFocusedWorkoutSession(t.Context(), dbgen.CancelFocusedWorkoutSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: today, ExpectedUpdatedAt: original.UpdatedAt, CancellationReason: "Not allowed today"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("today untimed cancelled", err)
	}
}

func TestWorkoutEditPinnedInactiveProfileIsSameBlockOnly(t *testing.T) {
	w := newFocusedWaterFixture(t)
	state := w.prepare(t)
	q := dbgen.New(w.web)
	id, err := graphBareSession(t, w, dbgen.TrainingEntryKindTRAINING, "START_ONLY")
	if err != nil {
		t.Fatal(err)
	}
	water, gym, a, b, c, g := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_segments(id,session_id,position,modality) VALUES($1,$3,1,'WATER'),($2,$3,2,'GYM')`, water, gym, id)
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$5,1,'MAIN','Original A'),($2,$5,2,'MAIN','Original B'),($3,$5,3,'MAIN','New details C'),($4,$6,1,'MAIN','Original gym')`, a, b, c, g, water, gym)
	profile, _ := graphProfile(t, w)
	old, err := q.InsertFocusedWorkoutWaterPrescription(t.Context(), dbgen.InsertFocusedWorkoutWaterPrescriptionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: water, BlockID: a, Method: dbgen.WaterWorkMethodINTERVALS, IntensityProfileID: &profile})
	if err != nil {
		t.Fatal(err)
	}
	next, err := q.CreateWaterIntensityProfile(t.Context(), dbgen.CreateWaterIntensityProfileParams{Name: "Synthetic explicit profile", Craft: dbgen.PaddlingCraftKAYAK, Notes: "Synthetic next revision", CreatedByID: w.actor})
	if err != nil {
		t.Fatal(err)
	}
	other, err := q.InsertFocusedWorkoutWaterPrescription(t.Context(), dbgen.InsertFocusedWorkoutWaterPrescriptionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: water, BlockID: b, Method: dbgen.WaterWorkMethodINTERVALS, IntensityProfileID: &next.ID})
	if err != nil {
		t.Fatal(err)
	}
	gp, err := q.InsertFocusedWorkoutGymPrescription(t.Context(), dbgen.InsertFocusedWorkoutGymPrescriptionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: gym, BlockID: g, Structure: dbgen.GymBlockStructureCIRCUIT, Objective: dbgen.TrainingObjectiveCUSTOM, Rounds: 2})
	if err != nil {
		t.Fatal(err)
	}
	tx, tq, original := beginEdit(t, w, state, w.actor, id)
	wpRows, err := tq.LockFocusedWorkoutWaterPrescriptions(t.Context(), dbgen.LockFocusedWorkoutWaterPrescriptionsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil || len(wpRows) != 2 {
		t.Fatal("locked exact water details", err)
	}
	gpRows, err := tq.LockFocusedWorkoutGymPrescriptions(t.Context(), dbgen.LockFocusedWorkoutGymPrescriptionsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil || len(gpRows) != 1 {
		t.Fatal("locked exact gym details", err)
	}
	pin, err := tq.GetFocusedWorkoutPinnedProfile(t.Context(), dbgen.GetFocusedWorkoutPinnedProfileParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, BlockID: a})
	if err != nil || pin.ID != profile || pin.IsActive {
		t.Fatalf("did not retain exact inactive source pin %+v %v", pin, err)
	}
	selected := dbgen.ListFocusedWorkoutEditProfilesParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, ProfileIds: []uuid.UUID{profile}, PinnedBlockIds: []uuid.UUID{a}}
	profiles, err := tq.ListFocusedWorkoutEditProfiles(t.Context(), selected)
	if err != nil || len(profiles) != 1 || profiles[0].IsActive {
		t.Fatal("source pin selection failed", err)
	}
	locked, err := tq.LockFocusedWorkoutEditProfiles(t.Context(), dbgen.LockFocusedWorkoutEditProfilesParams(selected))
	if err != nil || len(locked) != 1 {
		t.Fatal("source pin lock failed", err)
	}
	zones, err := tq.ListFocusedWorkoutEditZones(t.Context(), dbgen.ListFocusedWorkoutEditZonesParams(selected))
	if err != nil || len(zones) != 1 || zones[0].Code != "R2" {
		t.Fatal("pinned exact revision zones lost", err)
	}
	selected.PinnedBlockIds = []uuid.UUID{b}
	profiles, err = tq.ListFocusedWorkoutEditProfiles(t.Context(), selected)
	if err != nil || len(profiles) != 0 {
		t.Fatal("another block's active pin admitted inactive selection", err)
	}
	selected.PinnedBlockIds = []uuid.UUID{uuid.New()}
	profiles, err = tq.ListFocusedWorkoutEditProfiles(t.Context(), selected)
	if err != nil || len(profiles) != 0 {
		t.Fatal("foreign pin exposed selected facts", err)
	}
	selected.ActorID = uuid.New()
	selected.PinnedBlockIds = []uuid.UUID{a}
	profiles, err = tq.ListFocusedWorkoutEditProfiles(t.Context(), selected)
	if err != nil || len(profiles) != 0 {
		t.Fatal("unauthorized profile facts", err)
	}
	changed, err := tq.UpdateFocusedWorkoutWaterPrescription(t.Context(), dbgen.UpdateFocusedWorkoutWaterPrescriptionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: water, BlockID: a, ExpectedUpdatedAt: old.UpdatedAt, Method: dbgen.WaterWorkMethodCONTINUOUS, IntensityProfileID: &profile, TargetDistanceMetres: graphPtr(int32(8000)), TargetDistanceCertainty: graphPtr(dbgen.TrainingMeasureCertaintyEXACT)})
	if err != nil || changed.IntensityProfileID == nil || *changed.IntensityProfileID != profile || !changed.CreatedAt.Time.Equal(old.CreatedAt.Time) {
		t.Fatal("unchanged inactive pin rejected or identity changed", err)
	}
	_, err = tq.UpdateFocusedWorkoutWaterPrescription(t.Context(), dbgen.UpdateFocusedWorkoutWaterPrescriptionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: water, BlockID: b, ExpectedUpdatedAt: other.UpdatedAt, Method: dbgen.WaterWorkMethodINTERVALS, IntensityProfileID: &profile})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("inactive pin moved to other existing block", err)
	}
	_, err = tq.InsertFocusedWorkoutWaterPrescription(t.Context(), dbgen.InsertFocusedWorkoutWaterPrescriptionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: water, BlockID: c, Method: dbgen.WaterWorkMethodINTERVALS, IntensityProfileID: &profile})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("new block inherited inactive pin", err)
	}
	ug, err := tq.UpdateFocusedWorkoutGymPrescription(t.Context(), dbgen.UpdateFocusedWorkoutGymPrescriptionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: gym, BlockID: g, ExpectedUpdatedAt: gp.UpdatedAt, Structure: dbgen.GymBlockStructureSTRAIGHTSETS, Objective: dbgen.TrainingObjectiveCUSTOM, Rounds: 3, RoundRecoverySeconds: graphPtr(int32(90))})
	if err != nil || ug.BlockID != g || !ug.CreatedAt.Time.Equal(gp.CreatedAt.Time) || ug.Rounds != 3 {
		t.Fatal("gym prescription identity update failed", err)
	}
	final, err := tq.CheckFocusedWorkoutOriginalEligibility(t.Context(), editOriginalParams(w, w.actor, original))
	if err != nil || !final.Eligible {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func editExactSession(t *testing.T, w focusedWaterFixture) uuid.UUID {
	t.Helper()
	id := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) SELECT $1,id,'Synthetic editable exact',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours',$3 FROM training_plans WHERE id=$2`, id, w.plan, w.actor)
	return id
}
func editVariationTarget(t *testing.T, w focusedWaterFixture) uuid.UUID {
	t.Helper()
	id := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_variation_groups(id,training_group_id,name,kind,effective_from,created_by_id) VALUES($1,$2,'Synthetic selected subgroup','SUBGROUP',CURRENT_DATE,$3)`, id, w.group, w.actor)
	return id
}
func editVariationInput(w focusedWaterFixture, target, subject uuid.UUID, kind dbgen.TrainingVariationSubjectKind) dbgen.CreateTrainingVariationParams {
	return dbgen.CreateTrainingVariationParams{PlanID: w.plan, TargetGroupID: &target, SubjectKind: kind, SubjectID: subject, Operation: dbgen.TrainingVariationOperationOVERRIDE, ChangeSummary: "Synthetic explicit variation", Patch: []byte(`{"instructions":"Synthetic varied instruction"}`), CreatedByID: w.actor}
}
func TestWorkoutEditDeletionClosureRejectsRetiredReferencesAndPartialBatches(t *testing.T) {
	w := newFocusedWaterFixture(t)
	state := w.prepare(t)
	q := dbgen.New(w.web)
	id := editExactSession(t, w)
	segment, block, parent, child, free, dropSeg, dropBlock, dropParent, dropChild, gym, gymBlock, exercise := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_segments(id,session_id,position,modality) VALUES($1,$4,1,'WATER'),($2,$4,2,'WATER'),($3,$4,3,'GYM')`, segment, dropSeg, gym, id)
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$4,1,'MAIN','Referenced branch'),($2,$5,1,'MAIN','Removable branch'),($3,$6,1,'MAIN','Removable gym')`, block, dropBlock, gymBlock, segment, dropSeg, gym)
	recipientSchemaExec(t, w.web, `INSERT INTO water_block_prescriptions(block_id,method) VALUES($1,'INTERVALS'),($2,'INTERVALS')`, block, dropBlock)
	recipientSchemaExec(t, w.web, `INSERT INTO water_work_steps(id,block_id,parent_step_id,position,kind,name,repeats,duration_seconds,duration_certainty) VALUES($1,$6,NULL,1,'REPEAT_GROUP','Referenced parent',2,NULL,NULL),($2,$6,$1,1,'EFFORT','Referenced child',NULL,60,'EXACT'),($3,$6,NULL,2,'EFFORT','Free leaf',NULL,60,'EXACT'),($4,$7,NULL,1,'REPEAT_GROUP','Removable parent',2,NULL,NULL),($5,$7,$4,1,'EFFORT','Removable child',NULL,60,'EXACT')`, parent, child, free, dropParent, dropChild, block, dropBlock)
	recipientSchemaExec(t, w.web, `INSERT INTO gym_block_prescriptions(block_id,structure,objective,rounds) VALUES($1,'CIRCUIT','CUSTOM',2)`, gymBlock)
	recipientSchemaExec(t, w.web, `INSERT INTO gym_exercises(id,block_id,position,name,repetitions) VALUES($1,$2,1,'Synthetic removable squat',8)`, exercise, gymBlock)
	target := editVariationTarget(t, w)
	variation, err := q.CreateTrainingVariation(t.Context(), editVariationInput(w, target, child, dbgen.TrainingVariationSubjectKindWATERSTEP))
	if err != nil {
		t.Fatal(err)
	}
	n, err := q.RetireTrainingVariation(t.Context(), dbgen.RetireTrainingVariationParams{ID: variation.ID, Version: variation.Version, RetiredByID: &w.actor})
	if err != nil || n != 1 {
		t.Fatal("retire fixture", err)
	}
	// A separate ACTIVE ancestor reference must also veto its removal.
	if _, err = q.CreateTrainingVariation(t.Context(), editVariationInput(w, target, block, dbgen.TrainingVariationSubjectKindBLOCK)); err != nil {
		t.Fatal(err)
	}
	tx, tq, original := beginEdit(t, w, state, w.actor, id)
	lockedSteps, err := tq.LockFocusedWorkoutWaterSteps(t.Context(), dbgen.LockFocusedWorkoutWaterStepsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range lockedSteps {
		if row.ID == parent || row.ID == child {
			input := dbgen.UpdateFocusedWorkoutWaterStepParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: segment, BlockID: block, StepID: row.ID, ExpectedUpdatedAt: row.UpdatedAt, ParentStepID: row.ParentStepID, Position: row.Position, Kind: row.Kind, Name: row.Name, Repeats: row.Repeats, DurationSeconds: row.DurationSeconds, DurationCertainty: row.DurationCertainty, Instructions: row.Instructions}
			if row.ID == parent {
				input.Kind = dbgen.WaterStepKindEFFORT
				input.Repeats = nil
				input.DurationSeconds = graphPtr(int32(60))
				input.DurationCertainty = graphPtr(dbgen.TrainingMeasureCertaintyEXACT)
			} else {
				input.ParentStepID = nil
			}
			if _, err = tq.UpdateFocusedWorkoutWaterStep(t.Context(), input); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatal("retained parent reassigned or repeat converted with surviving children", err)
			}
		}
	}
	has, err := tq.HasFocusedWorkoutRemovalReferences(t.Context(), dbgen.HasFocusedWorkoutRemovalReferencesParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentIds: []uuid.UUID{segment}, BlockIds: []uuid.UUID{block}, StepIds: []uuid.UUID{parent, child, free}})
	if err != nil || !has {
		t.Fatal("retired/active reference omitted", err)
	}
	has, err = tq.HasFocusedWorkoutRemovalReferences(t.Context(), dbgen.HasFocusedWorkoutRemovalReferencesParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, StepIds: []uuid.UUID{child}})
	if err != nil || !has {
		t.Fatal("retired leaf reference ignored", err)
	}
	_, err = tq.HasFocusedWorkoutRemovalReferences(t.Context(), dbgen.HasFocusedWorkoutRemovalReferencesParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, StepIds: []uuid.UUID{uuid.New()}})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("foreign closure identifier accepted", err)
	}
	for _, ids := range [][]uuid.UUID{{child, free}, {free, free}, {parent}, {free, uuid.New()}} {
		deleted, err := tq.DeleteFocusedWorkoutWaterSteps(t.Context(), dbgen.DeleteFocusedWorkoutWaterStepsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, StepIds: ids})
		if err != nil || len(deleted) != 0 {
			t.Fatal("invalid batch partially deleted or implicit descendant cascade", err)
		}
	}
	before, err := tq.ListFocusedWorkoutWaterSteps(t.Context(), dbgen.ListFocusedWorkoutWaterStepsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil || len(before) != 5 {
		t.Fatal("refused batch changed source", err)
	}
	if removed, err := tq.DeleteFocusedWorkoutWaterPrescriptions(t.Context(), dbgen.DeleteFocusedWorkoutWaterPrescriptionsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, BlockIds: []uuid.UUID{dropBlock}}); err != nil || len(removed) != 0 {
		t.Fatal("prescription implicitly cascaded steps", err)
	}
	if removed, err := tq.DeleteFocusedWorkoutGymPrescriptions(t.Context(), dbgen.DeleteFocusedWorkoutGymPrescriptionsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, BlockIds: []uuid.UUID{gymBlock}}); err != nil || len(removed) != 0 {
		t.Fatal("prescription implicitly cascaded exercises", err)
	}
	for _, step := range []uuid.UUID{dropChild, dropParent} {
		removed, err := tq.DeleteFocusedWorkoutWaterSteps(t.Context(), dbgen.DeleteFocusedWorkoutWaterStepsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, StepIds: []uuid.UUID{step}})
		if err != nil || len(removed) != 1 || removed[0] != step {
			t.Fatal("explicit bottom-up step delete", err)
		}
	}
	if removed, err := tq.DeleteFocusedWorkoutGymExercises(t.Context(), dbgen.DeleteFocusedWorkoutGymExercisesParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, ExerciseIds: []uuid.UUID{exercise}}); err != nil || len(removed) != 1 || removed[0] != exercise {
		t.Fatal("explicit gym delete", err)
	}
	if removed, err := tq.DeleteFocusedWorkoutWaterPrescriptions(t.Context(), dbgen.DeleteFocusedWorkoutWaterPrescriptionsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, BlockIds: []uuid.UUID{dropBlock}}); err != nil || len(removed) != 1 {
		t.Fatal("explicit water prescription delete", err)
	}
	if removed, err := tq.DeleteFocusedWorkoutGymPrescriptions(t.Context(), dbgen.DeleteFocusedWorkoutGymPrescriptionsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, BlockIds: []uuid.UUID{gymBlock}}); err != nil || len(removed) != 1 {
		t.Fatal("explicit gym prescription delete", err)
	}
	if removed, err := tq.DeleteFocusedWorkoutBlocks(t.Context(), dbgen.DeleteFocusedWorkoutBlocksParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, BlockIds: []uuid.UUID{dropBlock, gymBlock}}); err != nil || len(removed) != 2 {
		t.Fatal("explicit blocks delete", err)
	}
	if removed, err := tq.DeleteFocusedWorkoutSegments(t.Context(), dbgen.DeleteFocusedWorkoutSegmentsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentIds: []uuid.UUID{dropSeg, gym}}); err != nil || len(removed) != 2 {
		t.Fatal("explicit segments delete", err)
	}
	final, err := tq.CheckFocusedWorkoutOriginalEligibility(t.Context(), editOriginalParams(w, w.actor, original))
	if err != nil || !final.Eligible {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	var remaining, retired int
	if err = w.web.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM water_work_steps WHERE block_id=$1),(SELECT count(*) FROM training_variations WHERE plan_id=$2 AND NOT is_active)`, block, w.plan).Scan(&remaining, &retired); err != nil || remaining != 3 || retired != 1 {
		t.Fatal("referenced history/source altered", err)
	}
}

func TestWorkoutEditRemovalSerializesSupportedVariationWriter(t *testing.T) {
	w := newFocusedWaterFixture(t)
	state := w.prepare(t)
	id := editExactSession(t, w)
	target := editVariationTarget(t, w)
	seg, block, step := uuid.New(), uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_segments(id,session_id,position,modality) VALUES($1,$2,1,'WATER')`, seg, id)
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$2,1,'MAIN','Synthetic removable source')`, block, seg)
	recipientSchemaExec(t, w.web, `INSERT INTO water_block_prescriptions(block_id,method) VALUES($1,'INTERVALS')`, block)
	recipientSchemaExec(t, w.web, `INSERT INTO water_work_steps(id,block_id,position,kind,name,duration_seconds,duration_certainty) VALUES($1,$2,1,'EFFORT','Synthetic concurrent leaf',60,'EXACT')`, step, block)
	tx, tq, original := beginEdit(t, w, state, w.actor, id)
	other := w.f.web(t)
	result := make(chan error, 1)
	go func() {
		var outcome error
		defer func() { result <- outcome }()
		// Same current supported variation store protocol: fresh actor, whole-plan
		// guard, plan lock, whole-plan recheck, then the existing source INSERT.
		vtx, e := other.BeginTx(t.Context(), pgx.TxOptions{})
		if e != nil {
			outcome = e
			return
		}
		defer vtx.Rollback(context.Background())
		vq := dbgen.New(vtx)
		for _, sql := range []string{`SELECT id FROM users WHERE id=$1 FOR SHARE`, `SELECT user_id FROM user_platform_roles WHERE user_id=$1 FOR SHARE`, `SELECT id FROM staff_grants WHERE user_id=$1 FOR SHARE`} {
			if _, e = vtx.Exec(t.Context(), sql, w.actor); e != nil {
				outcome = e
				return
			}
		}
		guard := dbgen.TrainingScheduleGuardPlanV1Params{ActorID: w.actor, PlanID: w.plan}
		if _, e = vq.TrainingScheduleGuardPlanV1(t.Context(), guard); e != nil {
			outcome = e
			return
		}
		if _, e = vq.LockStructuredTrainingPlanForPublication(t.Context(), w.plan); e != nil {
			outcome = e
			return
		}
		if _, e = vq.TrainingScheduleGuardPlanV1(t.Context(), guard); e != nil {
			outcome = e
			return
		}
		_, e = vq.CreateTrainingVariation(t.Context(), editVariationInput(w, target, step, dbgen.TrainingVariationSubjectKindWATERSTEP))
		if e != nil {
			outcome = e
			return
		}
		outcome = vtx.Commit(t.Context())
	}()
	focusedWaterWaitRowLock(t, w.f.root, other.PgConn().PID())
	has, err := tq.HasFocusedWorkoutRemovalReferences(t.Context(), dbgen.HasFocusedWorkoutRemovalReferencesParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, StepIds: []uuid.UUID{step}})
	if err != nil || has {
		t.Fatal("unexpected reference before blocked writer", err)
	}
	removed, err := tq.DeleteFocusedWorkoutWaterSteps(t.Context(), dbgen.DeleteFocusedWorkoutWaterStepsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, StepIds: []uuid.UUID{step}})
	if err != nil || len(removed) != 1 {
		t.Fatal("remove under lock", err)
	}
	final, err := tq.CheckFocusedWorkoutOriginalEligibility(t.Context(), editOriginalParams(w, w.actor, original))
	if err != nil || !final.Eligible {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal("variation inserted dangling subject after plan wait", err)
		}
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	var count int
	if err = w.web.QueryRow(t.Context(), `SELECT count(*) FROM training_variations WHERE plan_id=$1`, w.plan).Scan(&count); err != nil || count != 0 {
		t.Fatal("dangling variation retained", err)
	}
}

func TestWorkoutEditAndCancellationPreserveStoredHistoryAndOutcome(t *testing.T) {
	w := newFocusedWaterFixture(t)
	state := w.prepare(t)
	id := editExactSession(t, w)
	subject, membership, publication, prescription, routine := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic historical subject',$2,'hash','1980-01-01')`, subject, subject.String()+"@example.test")
	recipientSchemaExec(t, w.web, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) SELECT $1,$2,season_id,programme_id,CURRENT_DATE,CURRENT_DATE+300 FROM training_plans WHERE id=$3`, membership, subject, w.plan)
	recipientSchemaExec(t, w.web, `INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, w.group, membership, w.actor)
	placement := recipientPlaceFixtureMember(t, w.f, w.actor, membership, w.group)
	recipientSchemaExec(t, w.web, `INSERT INTO training_plan_publications(id,plan_id,revision,source_updated_at,change_summary,published_by_id) SELECT $1,id,1,updated_at,'Synthetic immutable first publication',$3 FROM training_plans WHERE id=$2`, publication, w.plan, w.actor)
	raw := `{"schema_version":1,"session":{"EntryKind":"WORKOUT"}}`
	sum := sha256.Sum256([]byte(raw))
	recipientSchemaExec(t, w.web, `INSERT INTO training_prescriptions(id,publication_id,session_id,membership_id,athlete_user_id,placement_id,snapshot,snapshot_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, prescription, publication, id, membership, subject, placement, raw, hex.EncodeToString(sum[:]))
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_outcomes(session_id,user_id,prescription_id,status) VALUES($1,$2,$3,'COMPLETED')`, id, subject, prescription)
	recipientSchemaExec(t, w.web, `INSERT INTO training_routines(id,name,kind,owner_user_id,source_id,source_updated_at,snapshot) SELECT $1,'Synthetic immutable routine','SESSION',$2,id,updated_at,'{"schema_version":1,"entry_kind":"WORKOUT"}'::jsonb FROM training_sessions WHERE id=$3`, routine, w.actor, id)
	history := func() string {
		var out string
		if err := w.web.QueryRow(t.Context(), `SELECT jsonb_build_array((SELECT to_jsonb(x) FROM training_plan_publications x WHERE id=$1),(SELECT to_jsonb(x) FROM training_prescriptions x WHERE id=$2),(SELECT to_jsonb(x) FROM training_session_outcomes x WHERE session_id=$3 AND user_id=$4),(SELECT to_jsonb(x) FROM training_routines x WHERE id=$5))::text`, publication, prescription, id, subject, routine).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := history()
	tx, tq, original := beginEdit(t, w, state, w.actor, id)
	if !original.HasOutcomes {
		t.Fatal("fixture lacks completed outcome")
	}
	input := editSessionParams(w, w.actor, original)
	input.Title = "Synthetic edited next instructions"
	current, err := tq.UpdateFocusedWorkoutSession(t.Context(), input)
	if err != nil {
		t.Fatal("same-plan edit with outcome rejected", err)
	}
	cancelled, err := tq.CancelFocusedWorkoutSession(t.Context(), dbgen.CancelFocusedWorkoutSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, ExpectedUpdatedAt: current.UpdatedAt, CancellationReason: "Synthetic future exact cancellation"})
	if err != nil || cancelled.Status != "CANCELLED" {
		t.Fatal(err)
	}
	final, err := tq.CheckFocusedWorkoutOriginalEligibility(t.Context(), editOriginalParams(w, w.actor, original))
	if err != nil || !final.Eligible {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := history(); got != before {
		t.Fatal("edit/cancellation rewrote publication, snapshot/hash, completed outcome or stored routine")
	}
}

func TestWorkoutEditSelectedProfileWitnessRejectsPostSnapshotZoneChanges(t *testing.T) {
	w := newFocusedWaterFixture(t)
	state := w.prepare(t)
	profile, zone := graphProfile(t, w)
	id, err := graphBareSession(t, w, dbgen.TrainingEntryKindTRAINING, "START_ONLY")
	if err != nil {
		t.Fatal(err)
	}
	seg, block := uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_segments(id,session_id,position,modality) VALUES($1,$2,1,'WATER')`, seg, id)
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$2,1,'MAIN','Synthetic selected profile')`, block, seg)
	other := w.f.web(t)
	for _, mode := range []string{"completed", "inflight"} {
		t.Run(mode, func(t *testing.T) {
			tx, tq, _ := beginEdit(t, w, state, w.actor, id)
			arg := dbgen.LockFocusedWorkoutEditProfilesParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, ProfileIds: []uuid.UUID{profile}, PinnedBlockIds: nil}
			before, err := tq.ListFocusedWorkoutEditProfiles(t.Context(), dbgen.ListFocusedWorkoutEditProfilesParams(arg))
			if err != nil || len(before) != 1 {
				t.Fatal("exact reference read", err)
			}
			writer, err := other.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Rollback(context.Background())
			zone.Code = "R3"
			zone.Label = "Synthetic post-snapshot zone"
			if mode == "inflight" {
				zone.Code = "R4"
			}
			if _, err = dbgen.New(writer).CreateWaterIntensityZone(t.Context(), zone); err != nil {
				t.Fatal(err)
			}
			expected := "55P03"
			if mode == "completed" {
				if err = writer.Commit(t.Context()); err != nil {
					t.Fatal(err)
				}
				expected = "40001"
			}
			_, err = tq.LockFocusedWorkoutEditProfiles(t.Context(), arg)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != expected {
				t.Fatalf("selected profile did not witness zone change: want%s got%v", expected, err)
			}
			if err = tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
