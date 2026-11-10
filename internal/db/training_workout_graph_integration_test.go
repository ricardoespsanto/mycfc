//go:build integration

package db

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func graphPtr[T any](v T) *T { return &v }

func TestFullWorkoutFreshDefinition(t *testing.T) {
	f := newRecipientSchemaFixture(t)
	// Authenticated migration owner installs actual fresh definitions. Inspect the
	// exact catalog before full bootstrap verifier for useful mismatch evidence.
	if _, err := f.low.PgConn().Exec(t.Context(), baselineSchema).ReadAll(); err != nil {
		t.Fatal(err)
	}
	var shape string
	if err := f.low.QueryRow(t.Context(), `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='water_work_steps'::regclass AND conname='water_work_steps_shape_valid'`).Scan(&shape); err != nil {
		t.Fatal(err)
	}
	t.Log(shape)
	if err := verifyTrainingWorkoutGraphDefinition(t.Context(), f.low); err != nil {
		t.Fatal(err)
	}
}

type fullGraphIDs struct{ session, water, gym, run, main, cool, strength, running, warm, outer, inner, effort, coolEffort, squat, push uuid.UUID }

func newFullGraphIDs() fullGraphIDs {
	return fullGraphIDs{uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()}
}
func graphProfile(t *testing.T, w focusedWaterFixture) (uuid.UUID, dbgen.CreateWaterIntensityZoneParams) {
	t.Helper()
	q := dbgen.New(w.web)
	p, err := q.CreateWaterIntensityProfile(t.Context(), dbgen.CreateWaterIntensityProfileParams{Name: "Synthetic explicit profile", Craft: dbgen.PaddlingCraftKAYAK, Notes: "Synthetic coach values only", CreatedByID: w.actor})
	if err != nil {
		t.Fatal(err)
	}
	zone := dbgen.CreateWaterIntensityZoneParams{ProfileID: p.ID, Code: "R2", Label: "Synthetic R2", CadenceMin: graphPtr(int32(60)), CadenceMax: graphPtr(int32(70)), Meaning: "Coach supplied meaning"}
	if _, err = q.CreateWaterIntensityZone(t.Context(), zone); err != nil {
		t.Fatal(err)
	}
	return p.ID, zone
}
func TestFullWorkoutMixedAtomicRawAndOrder(t *testing.T) {
	w := newFocusedWaterFixture(t)
	s := w.prepare(t)
	profile, _ := graphProfile(t, w)
	ids := newFullGraphIDs()
	q := dbgen.New(w.web)
	p := w.pool(t)
	plain := w.sessionInput(t, ids.session)
	// One eligible synthetic subject makes current recipient raw source meaningful.
	subject, member := uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic athlete',$2,'hash','1980-01-01')`, subject, subject.String()+"@example.test")
	recipientSchemaExec(t, w.web, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) SELECT $1,$2,season_id,programme_id,CURRENT_DATE,CURRENT_DATE+300 FROM training_plans WHERE id=$3`, member, subject, w.plan)
	recipientSchemaExec(t, w.web, `INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, w.group, member, w.actor)
	plain = w.sessionInput(t, ids.session)
	save := func(fault bool) (SessionTxResult, error) {
		return WithRecipientPublicationTx(t.Context(), p, recipientTestOptions(), func(ctx context.Context, commands dbgen.DBTX) error {
			if _, err := commands.Exec(ctx, `SELECT public.training_schedule_admit_partial($1,$2)`, s.DatabaseIncarnation, s.StateEpoch); err != nil {
				return err
			}
			if _, err := VerifyTrainingWorkoutGraphRuntime(ctx, commands, TrainingScheduleCapabilities{ReaderVersion: 5, WriterVersion: 2}); err != nil {
				return err
			}
			txq := dbgen.New(commands)
			if _, err := txq.LockFocusedWaterCreatePlan(ctx, dbgen.LockFocusedWaterCreatePlanParams{ActorID: w.actor, PlanID: w.plan}); err != nil {
				return err
			}
			profiles, err := txq.LockFocusedWorkoutSelectedProfiles(ctx, dbgen.LockFocusedWorkoutSelectedProfilesParams{ActorID: w.actor, PlanID: w.plan, ProfileIds: []uuid.UUID{profile}})
			if err != nil {
				return err
			}
			if len(profiles) != 1 {
				return errors.New("incomplete selected profiles")
			}
			zones, err := txq.ListFocusedWorkoutSelectedZones(ctx, dbgen.ListFocusedWorkoutSelectedZonesParams{ActorID: w.actor, PlanID: w.plan, ProfileIds: []uuid.UUID{profile}})
			if err != nil {
				return err
			}
			if len(zones) != 1 || zones[0].Code != "R2" {
				return errors.New("selected reference facts changed")
			}
			if _, err = txq.InsertFocusedWorkoutSession(ctx, dbgen.InsertFocusedWorkoutSessionParams{SessionID: ids.session, ActorID: w.actor, PlanID: w.plan, Title: "Synthetic water gym run", Description: "Own words Força A and B", EntryKind: dbgen.TrainingEntryKindTRAINING, StartsAt: plain.StartsAt, EndsAt: plain.EndsAt, SchedulePrecision: plain.SchedulePrecision, ScheduledOn: plain.ScheduledOn, ExpectedPlanUpdatedAt: plain.ExpectedPlanUpdatedAt}); err != nil {
				return err
			}
			// Deliberately create sibling2 before sibling1; created_at must not win.
			for _, a := range []struct {
				id   uuid.UUID
				pos  int32
				mode dbgen.TrainingSegmentModality
			}{{ids.gym, 2, dbgen.TrainingSegmentModalityGYM}, {ids.water, 1, dbgen.TrainingSegmentModalityWATER}, {ids.run, 3, dbgen.TrainingSegmentModalityRUN}} {
				if _, err = txq.InsertFocusedWorkoutSegment(ctx, dbgen.InsertFocusedWorkoutSegmentParams{ActorID: w.actor, PlanID: w.plan, SessionID: ids.session, SegmentID: a.id, Position: a.pos, Modality: a.mode, Title: "Synthetic activity", Location: "Coach supplied location"}); err != nil {
					return err
				}
			}
			for _, b := range []struct {
				id, seg uuid.UUID
				pos     int32
				purpose dbgen.TrainingBlockPurpose
				words   string
			}{{ids.main, ids.water, 1, dbgen.TrainingBlockPurposeMAIN, "Synthetic10 groups of2 explicit efforts with nested recovery"}, {ids.cool, ids.water, 2, dbgen.TrainingBlockPurposeCOOLDOWN, "Continue until session reaches 8 km"}, {ids.strength, ids.gym, 1, dbgen.TrainingBlockPurposeMAIN, "Força A in coach own words"}, {ids.running, ids.run, 1, dbgen.TrainingBlockPurposeMAIN, "Corrida and Força B written guidance"}} {
				if _, err = txq.InsertFocusedWorkoutBlock(ctx, dbgen.InsertFocusedWorkoutBlockParams{ActorID: w.actor, PlanID: w.plan, SessionID: ids.session, SegmentID: b.seg, BlockID: b.id, Position: b.pos, Purpose: b.purpose, Instructions: b.words}); err != nil {
					return err
				}
			}
			if _, err = txq.InsertFocusedWorkoutWaterPrescription(ctx, dbgen.InsertFocusedWorkoutWaterPrescriptionParams{ActorID: w.actor, PlanID: w.plan, SessionID: ids.session, SegmentID: ids.water, BlockID: ids.main, Method: dbgen.WaterWorkMethodINTERVALS, IntensityProfileID: &profile}); err != nil {
				return err
			}
			if _, err = txq.InsertFocusedWorkoutWaterPrescription(ctx, dbgen.InsertFocusedWorkoutWaterPrescriptionParams{ActorID: w.actor, PlanID: w.plan, SessionID: ids.session, SegmentID: ids.water, BlockID: ids.cool, Method: dbgen.WaterWorkMethodCONTINUOUS, TargetDistanceMetres: graphPtr(int32(8000)), TargetDistanceCertainty: graphPtr(dbgen.TrainingMeasureCertaintyEXACT)}); err != nil {
				return err
			}
			step := func(id uuid.UUID, parent *uuid.UUID, pos int32, kind dbgen.WaterStepKind, repeat, duration, recovery *int32, block uuid.UUID) error {
				in := dbgen.InsertFocusedWorkoutWaterStepParams{ActorID: w.actor, PlanID: w.plan, SessionID: ids.session, SegmentID: ids.water, BlockID: block, StepID: id, ParentStepID: parent, Position: pos, Kind: kind, Name: "Synthetic explicit water node", Repeats: repeat, DurationSeconds: duration, RecoverySeconds: recovery, Instructions: "Coach instruction"}
				if duration != nil {
					in.DurationCertainty = graphPtr(dbgen.TrainingMeasureCertaintyEXACT)
					in.IntensityCode = graphPtr("R2")
					in.CadenceSpm = graphPtr(int32(64))
					in.DrillFocus = graphPtr("Explicit drill")
					in.DrillFormat = graphPtr("Explicit format")
					in.RoleNotes = graphPtr("Explicit crew role")
				}
				if id == ids.warm {
					in.DistanceMetres = graphPtr(int32(1000))
					in.DistanceCertainty = graphPtr(dbgen.TrainingMeasureCertaintyESTIMATED)
				}
				_, err := txq.InsertFocusedWorkoutWaterStep(ctx, in)
				return err
			}
			for _, err = range []error{step(ids.outer, nil, 2, dbgen.WaterStepKindREPEATGROUP, graphPtr(int32(10)), nil, graphPtr(int32(120)), ids.main), step(ids.inner, &ids.outer, 1, dbgen.WaterStepKindREPEATGROUP, graphPtr(int32(2)), nil, graphPtr(int32(30)), ids.main), step(ids.effort, &ids.inner, 1, dbgen.WaterStepKindEFFORT, nil, graphPtr(int32(240)), nil, ids.main), step(ids.warm, nil, 1, dbgen.WaterStepKindEFFORT, nil, graphPtr(int32(600)), nil, ids.main), step(ids.coolEffort, nil, 1, dbgen.WaterStepKindEFFORT, nil, nil, nil, ids.cool)} {
				if err != nil {
					return err
				}
			}
			if _, err = txq.InsertFocusedWorkoutGymPrescription(ctx, dbgen.InsertFocusedWorkoutGymPrescriptionParams{ActorID: w.actor, PlanID: w.plan, SessionID: ids.session, SegmentID: ids.gym, BlockID: ids.strength, Structure: dbgen.GymBlockStructureCIRCUIT, Objective: dbgen.TrainingObjectiveMAXSTRENGTHHYPERTROPHY, Rounds: 3, RoundRecoverySeconds: graphPtr(int32(60))}); err != nil {
				return err
			}
			if _, err = txq.InsertFocusedWorkoutGymExercise(ctx, dbgen.InsertFocusedWorkoutGymExerciseParams{ActorID: w.actor, PlanID: w.plan, SessionID: ids.session, SegmentID: ids.gym, BlockID: ids.strength, ExerciseID: ids.push, Position: 2, Name: "Synthetic controlled press", Sets: graphPtr(int32(3)), DurationSeconds: graphPtr(int32(45)), RecoverySeconds: graphPtr(int32(90)), ResistanceKind: graphPtr(dbgen.GymResistanceKindCOACHINSTRUCTION), ResistanceText: graphPtr("Coach-selected resistance"), ExecutionIntent: graphPtr(dbgen.GymExecutionIntentCONTROLLED), Tempo: graphPtr("3-1-1"), Notes: "Força A readable guidance"}); err != nil {
				return err
			}
			name := "Synthetic squat"
			if fault {
				name = "x"
			}
			_, err = txq.InsertFocusedWorkoutGymExercise(ctx, dbgen.InsertFocusedWorkoutGymExerciseParams{ActorID: w.actor, PlanID: w.plan, SessionID: ids.session, SegmentID: ids.gym, BlockID: ids.strength, ExerciseID: ids.squat, Position: 1, Name: name, Sets: graphPtr(int32(3)), Repetitions: graphPtr(int32(8)), ResistanceKind: graphPtr(dbgen.GymResistanceKindKILOGRAMS), ResistanceValue: graphPtr(12.5)})
			if err != nil {
				return err
			}
			steps, err := txq.ListFocusedWorkoutWaterSteps(ctx, dbgen.ListFocusedWorkoutWaterStepsParams{ActorID: w.actor, PlanID: w.plan, SessionID: ids.session})
			if err != nil {
				return err
			}
			if len(steps) != 5 {
				return errors.New("raw tree incomplete")
			}
			return nil
		})
	}
	run, err := save(true)
	if err == nil || run.Outcome != SessionTxNotCommitted {
		t.Fatalf("last detail failure partly committed %+v %v", run, err)
	}
	if _, err = q.GetFocusedWorkoutSession(t.Context(), dbgen.GetFocusedWorkoutSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: ids.session}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("failed graph retained")
	}
	run, err = save(false)
	if err != nil || run.Outcome != SessionTxCommitted {
		t.Fatalf("full graph save %+v %v", run, err)
	}
	raw, err := q.GetFocusedWorkoutSession(t.Context(), dbgen.GetFocusedWorkoutSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: ids.session})
	if err != nil || raw.EndsAt.Valid || raw.HasOutcomes || raw.ID != ids.session {
		t.Fatalf("raw appointment %v", err)
	}
	steps, err := q.ListFocusedWorkoutWaterSteps(t.Context(), dbgen.ListFocusedWorkoutWaterStepsParams{ActorID: w.actor, PlanID: w.plan, SessionID: ids.session})
	if err != nil {
		t.Fatal(err)
	}
	want := []uuid.UUID{ids.warm, ids.outer, ids.inner, ids.effort, ids.coolEffort}
	got := []uuid.UUID{}
	for _, st := range steps {
		got = append(got, st.ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("raw tree order %v", got)
	}
	if steps[0].DistanceMetres == nil || *steps[0].DistanceMetres != 1000 || steps[0].DistanceCertainty == nil || *steps[0].DistanceCertainty != dbgen.TrainingMeasureCertaintyESTIMATED {
		t.Fatal("explicit estimated distance lost")
	}
	if *steps[1].Repeats != 10 || *steps[1].RecoverySeconds != 120 || *steps[2].Repeats != 2 || *steps[2].RecoverySeconds != 30 || *steps[3].DurationSeconds != 240 || steps[4].DurationSeconds != nil {
		t.Fatal("nested raw quantities/nulls changed")
	}
	gym, err := q.ListFocusedWorkoutGymExercises(t.Context(), dbgen.ListFocusedWorkoutGymExercisesParams{ActorID: w.actor, PlanID: w.plan, SessionID: ids.session})
	if err != nil || len(gym) != 2 || gym[0].ID != ids.squat || *gym[0].ResistanceValue != 12.5 || gym[1].Repetitions != nil || *gym[1].ResistanceText != "Coach-selected resistance" {
		t.Fatalf("gym raw lost meaning %v", err)
	}
	water, err := q.ListFocusedWorkoutWaterPrescriptions(t.Context(), dbgen.ListFocusedWorkoutWaterPrescriptionsParams{ActorID: w.actor, PlanID: w.plan, SessionID: ids.session})
	if err != nil || len(water) != 2 || *water[1].TargetDistanceMetres != 8000 {
		t.Fatal("target continuation changed", err)
	}
	// All current sources and NEW routine snapshot serialize the same sibling tree.
	later := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) SELECT $1,'Synthetic later week',programme_id,training_group_id,season_id,week_start+7,created_by_id FROM training_plans WHERE id=$2`, later, w.plan)
	manager, err := q.ListStructuredTrainingOverviewForManager(t.Context(), dbgen.ListStructuredTrainingOverviewForManagerParams{IsAdmin: true, UserID: w.actor})
	if err != nil {
		t.Fatal(err)
	}
	planOrder := []uuid.UUID{}
	for _, r := range manager {
		if r.PlanID != nil && (len(planOrder) == 0 || planOrder[len(planOrder)-1] != *r.PlanID) {
			planOrder = append(planOrder, *r.PlanID)
		}
	}
	if !reflect.DeepEqual(planOrder, []uuid.UUID{later, w.plan}) {
		t.Fatalf("manager outer week order changed %v", planOrder)
	}
	got = nil
	for _, r := range manager {
		if r.BlockID != nil && *r.BlockID == ids.main && r.WaterStepID != nil {
			got = append(got, *r.WaterStepID)
		}
	}
	if !reflect.DeepEqual(got, want[:4]) {
		t.Fatalf("manager order %v", got)
	}
	subjectRows, err := q.ListStructuredTrainingOverviewForSubject(t.Context(), subject)
	if err != nil {
		t.Fatal(err)
	}
	planOrder = nil
	for _, r := range subjectRows {
		if len(planOrder) == 0 || planOrder[len(planOrder)-1] != r.PlanID {
			planOrder = append(planOrder, r.PlanID)
		}
	}
	if !reflect.DeepEqual(planOrder, []uuid.UUID{later, w.plan}) {
		t.Fatalf("subject outer week order changed %v", planOrder)
	}
	got = nil
	for _, r := range subjectRows {
		if r.BlockID != nil && *r.BlockID == ids.main && r.WaterStepID != nil {
			got = append(got, *r.WaterStepID)
		}
	}
	if !reflect.DeepEqual(got, want[:4]) {
		t.Fatalf("subject order %v", got)
	}
	source, err := q.PublicationRawSource(t.Context(), dbgen.PublicationRawSourceParams{ActorID: w.actor, PlanID: w.plan})
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Overview []struct {
			BlockID *uuid.UUID `json:"block_id"`
			StepID  *uuid.UUID `json:"water_step_id"`
		} `json:"overview"`
	}
	if err = json.Unmarshal(source.Envelope, &envelope); err != nil {
		t.Fatal(err)
	}
	got = nil
	for _, r := range envelope.Overview {
		if r.BlockID != nil && *r.BlockID == ids.main && r.StepID != nil {
			got = append(got, *r.StepID)
		}
	}
	if !reflect.DeepEqual(got, want[:4]) {
		t.Fatalf("recipient source order %v", got)
	}
	var snapshot []byte
	if err = w.web.QueryRow(t.Context(), `SELECT training_block_snapshot_water($1)`, ids.main).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	var block struct {
		Water struct {
			Steps []struct {
				ID uuid.UUID `json:"source_id"`
			} `json:"steps"`
		} `json:"water"`
	}
	if err = json.Unmarshal(snapshot, &block); err != nil {
		t.Fatal(err)
	}
	got = nil
	for _, r := range block.Water.Steps {
		got = append(got, r.ID)
	}
	if !reflect.DeepEqual(got, want[:4]) {
		t.Fatalf("new routine snapshot order %v", got)
	}
}

func TestFullWorkoutSelectedProfileWitnessRaces(t *testing.T) {
	w := newFocusedWaterFixture(t)
	s := w.prepare(t)
	profile, zone := graphProfile(t, w)
	q := dbgen.New(w.web)
	editor := w.f.web(t)
	eq := dbgen.New(editor)
	logical := func() string {
		t.Helper()
		var row string
		if err := w.web.QueryRow(t.Context(), `SELECT to_jsonb(profile)::text FROM water_intensity_profiles profile WHERE id=$1`, profile).Scan(&row); err != nil {
			t.Fatal(err)
		}
		return row
	}
	planVersion := func() string {
		t.Helper()
		var v string
		if err := w.web.QueryRow(t.Context(), `SELECT updated_at::text FROM training_plans WHERE id=$1`, w.plan).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	beforeProfile, beforePlan := logical(), planVersion()
	for _, mode := range []string{"insert", "update"} {
		t.Run(mode, func(t *testing.T) {
			tx, err := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			recipientSchemaExec(t, tx, `SELECT public.training_schedule_admit_partial($1,$2)`, s.DatabaseIncarnation, s.StateEpoch)
			if _, err = VerifyTrainingWorkoutGraphRuntime(t.Context(), tx, TrainingScheduleCapabilities{ReaderVersion: 5, WriterVersion: 2}); err != nil {
				t.Fatal(err)
			}
			tq := dbgen.New(tx)
			if _, err = tq.LockFocusedWaterCreatePlan(t.Context(), dbgen.LockFocusedWaterCreatePlanParams{ActorID: w.actor, PlanID: w.plan}); err != nil {
				t.Fatal(err)
			}
			changed := zone
			if mode == "insert" {
				changed.Code = "NEW_AFTER_SNAPSHOT"
				changed.Label = "Synthetic new zone"
			} else {
				changed.Label = "Synthetic revised R2"
			}
			if _, err = eq.CreateWaterIntensityZone(t.Context(), changed); err != nil {
				t.Fatal(err)
			}
			_, err = tq.LockFocusedWorkoutSelectedProfiles(t.Context(), dbgen.LockFocusedWorkoutSelectedProfilesParams{ActorID: w.actor, PlanID: w.plan, ProfileIds: []uuid.UUID{profile}})
			var pe *pgconn.PgError
			if !errors.As(err, &pe) || pe.Code != "40001" {
				t.Fatalf("completed post-snapshot profile %s escaped witness %v", mode, err)
			}
			_ = tx.Rollback(t.Context())
			if logical() != beforeProfile || planVersion() != beforePlan {
				t.Fatal("profile witness altered logical profile or unreferenced plan values")
			}
		})
	}
	edit, err := editor.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	pending := zone
	pending.Code = "LIVE_PENDING"
	if _, err = dbgen.New(edit).CreateWaterIntensityZone(t.Context(), pending); err != nil {
		t.Fatal(err)
	}
	_, err = q.LockFocusedWorkoutSelectedProfiles(t.Context(), dbgen.LockFocusedWorkoutSelectedProfilesParams{ActorID: w.actor, PlanID: w.plan, ProfileIds: []uuid.UUID{profile}})
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "55P03" {
		t.Fatalf("live profile writer did not NOWAIT conflict %v", err)
	}
	_ = edit.Rollback(t.Context())
	held, err := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(t.Context())
	recipientSchemaExec(t, held, `SELECT public.training_schedule_admit_partial($1,$2)`, s.DatabaseIncarnation, s.StateEpoch)
	if _, err = dbgen.New(held).LockFocusedWorkoutSelectedProfiles(t.Context(), dbgen.LockFocusedWorkoutSelectedProfilesParams{ActorID: w.actor, PlanID: w.plan, ProfileIds: []uuid.UUID{profile}}); err != nil {
		t.Fatal(err)
	}
	blocked := make(chan error, 1)
	go func() {
		next := zone
		next.Code = "AFTER_READER"
		_, err := eq.CreateWaterIntensityZone(t.Context(), next)
		blocked <- err
	}()
	focusedWaterWaitRowLock(t, w.f.root, editor.PgConn().PID())
	if err = held.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-blocked; err != nil {
		t.Fatal(err)
	}
	rows, err := q.ListFocusedWorkoutSelectedZones(t.Context(), dbgen.ListFocusedWorkoutSelectedZonesParams{ActorID: w.actor, PlanID: w.plan, ProfileIds: []uuid.UUID{profile}})
	if err != nil || len(rows) != 3 {
		t.Fatalf("completed reference set %d %v", len(rows), err)
	}
}

func graphBareSession(t *testing.T, w focusedWaterFixture, kind dbgen.TrainingEntryKind, precision string) (uuid.UUID, error) {
	t.Helper()
	in := w.sessionInput(t, uuid.New())
	out := dbgen.InsertFocusedWorkoutSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: in.SessionID, Title: "Synthetic generic appointment", Description: "Coach plain own words", EntryKind: kind, StartsAt: in.StartsAt, EndsAt: in.EndsAt, SchedulePrecision: precision, ScheduledOn: in.ScheduledOn, ExpectedPlanUpdatedAt: in.ExpectedPlanUpdatedAt}
	if precision == "DAY" || precision == "BAND" {
		out.StartsAt = pgtype.Timestamptz{}
	}
	if precision == "BAND" {
		out.DayBand = graphPtr("MORNING")
	}
	row, err := dbgen.New(w.web).InsertFocusedWorkoutSession(t.Context(), out)
	return row.ID, err
}
func TestFullWorkoutKindsNullRepeatsScopeAndProfileBounds(t *testing.T) {
	w := newFocusedWaterFixture(t)
	w.prepare(t)
	q := dbgen.New(w.web)
	for _, tc := range []struct {
		kind  dbgen.TrainingEntryKind
		shape string
	}{{dbgen.TrainingEntryKindREST, "DAY"}, {dbgen.TrainingEntryKindLOGISTICS, "BAND"}} {
		id, err := graphBareSession(t, w, tc.kind, tc.shape)
		if err != nil {
			t.Fatal(err)
		}
		_, err = q.InsertFocusedWorkoutSegment(t.Context(), dbgen.InsertFocusedWorkoutSegmentParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: uuid.New(), Position: 1, Modality: dbgen.TrainingSegmentModalityWATER})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("%s accepted content %v", tc.kind, err)
		}
	}
	id, err := graphBareSession(t, w, dbgen.TrainingEntryKindCOMPETITION, "START_ONLY")
	if err != nil {
		t.Fatal(err)
	}
	seg, block := uuid.New(), uuid.New()
	if _, err = q.InsertFocusedWorkoutSegment(t.Context(), dbgen.InsertFocusedWorkoutSegmentParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: seg, Position: 1, Modality: dbgen.TrainingSegmentModalityWATER}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.InsertFocusedWorkoutBlock(t.Context(), dbgen.InsertFocusedWorkoutBlockParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: seg, BlockID: block, Position: 1, Purpose: dbgen.TrainingBlockPurposeMAIN, Instructions: "Synthetic competition own words"}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.InsertFocusedWorkoutWaterPrescription(t.Context(), dbgen.InsertFocusedWorkoutWaterPrescriptionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: seg, BlockID: block, Method: dbgen.WaterWorkMethodINTERVALS}); err != nil {
		t.Fatal(err)
	}
	_, err = q.InsertFocusedWorkoutWaterStep(t.Context(), dbgen.InsertFocusedWorkoutWaterStepParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: seg, BlockID: block, StepID: uuid.New(), Position: 1, Kind: dbgen.WaterStepKindREPEATGROUP, Name: "Missing explicit repeats"})
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23514" {
		t.Fatalf("NULL repeat group admitted %v", err)
	}
	// Separate literal10x4min/2minR2 example, without inferring pace or end.
	repeat, effort := uuid.New(), uuid.New()
	for _, input := range []dbgen.InsertFocusedWorkoutWaterStepParams{
		{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: seg, BlockID: block, StepID: repeat, Position: 1, Kind: dbgen.WaterStepKindREPEATGROUP, Name: "Synthetic10repeats", Repeats: graphPtr(int32(10)), RecoverySeconds: graphPtr(int32(120))},
		{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: seg, BlockID: block, StepID: effort, ParentStepID: &repeat, Position: 1, Kind: dbgen.WaterStepKindEFFORT, Name: "Synthetic4minuteR2", DurationSeconds: graphPtr(int32(240)), DurationCertainty: graphPtr(dbgen.TrainingMeasureCertaintyEXACT), IntensityCode: graphPtr("R2")},
	} {
		if _, err = q.InsertFocusedWorkoutWaterStep(t.Context(), input); err != nil {
			t.Fatal(err)
		}
	}
	literal, err := q.ListFocusedWorkoutWaterSteps(t.Context(), dbgen.ListFocusedWorkoutWaterStepsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil || len(literal) != 2 || literal[0].ID != repeat || *literal[0].Repeats != 10 || *literal[0].RecoverySeconds != 120 || *literal[1].DurationSeconds != 240 || *literal[1].IntensityCode != "R2" {
		t.Fatal("literal interval raw roundtrip changed", err)
	}
	foreign := uuid.New()
	_, err = q.InsertFocusedWorkoutWaterStep(t.Context(), dbgen.InsertFocusedWorkoutWaterStepParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: seg, BlockID: block, StepID: uuid.New(), ParentStepID: &foreign, Position: 1, Kind: dbgen.WaterStepKindEFFORT, Name: "Foreign parent", Instructions: "Must not attach"})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("foreign parent accepted", err)
	}
	stranger := uuid.New()
	if _, err = q.GetFocusedWorkoutSession(t.Context(), dbgen.GetFocusedWorkoutSessionParams{ActorID: stranger, PlanID: w.plan, SessionID: id}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("raw session leaked", err)
	}
	steps, err := q.ListFocusedWorkoutWaterSteps(t.Context(), dbgen.ListFocusedWorkoutWaterStepsParams{ActorID: stranger, PlanID: w.plan, SessionID: id})
	if err != nil || len(steps) != 0 {
		t.Fatal("raw steps leaked")
	}
	profile, _ := graphProfile(t, w)
	for _, ids := range [][]uuid.UUID{{profile, profile}, {profile, uuid.New()}, append([]uuid.UUID{profile}, make([]uuid.UUID, 32)...)} {
		locked, err := q.LockFocusedWorkoutSelectedProfiles(t.Context(), dbgen.LockFocusedWorkoutSelectedProfilesParams{ActorID: w.actor, PlanID: w.plan, ProfileIds: ids})
		if err != nil || len(locked) != 0 {
			t.Fatal("partial/duplicate/oversize reference set returned")
		}
		zones, err := q.ListFocusedWorkoutSelectedZones(t.Context(), dbgen.ListFocusedWorkoutSelectedZonesParams{ActorID: w.actor, PlanID: w.plan, ProfileIds: ids})
		if err != nil || len(zones) != 0 {
			t.Fatal("partial zones returned")
		}
	}
	recipientSchemaExec(t, w.web, `INSERT INTO water_intensity_zones(profile_id,position,code,label,meaning) SELECT $1,n,'SYN_'||n,'Synthetic zone','Synthetic oversize catalog fixture' FROM generate_series(2,259) n`, profile)
	zones, err := q.ListFocusedWorkoutSelectedZones(t.Context(), dbgen.ListFocusedWorkoutSelectedZonesParams{ActorID: w.actor, PlanID: w.plan, ProfileIds: []uuid.UUID{profile}})
	if err != nil || len(zones) != 257 {
		t.Fatalf("zone count+1 lost %d %v", len(zones), err)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO water_intensity_profiles(name,craft,revision,created_by_id) SELECT 'Synthetic catalog '||n,'KAYAK',1,$1 FROM generate_series(1,101)n`, w.actor)
	choices, err := q.ListFocusedWorkoutActiveProfiles(t.Context(), dbgen.ListFocusedWorkoutActiveProfilesParams{ActorID: w.actor, PlanID: w.plan})
	if err != nil || len(choices) != 101 {
		t.Fatalf("catalog count+1 lost %d %v", len(choices), err)
	}
	choices, err = q.ListFocusedWorkoutActiveProfiles(t.Context(), dbgen.ListFocusedWorkoutActiveProfilesParams{ActorID: stranger, PlanID: w.plan})
	if err != nil || len(choices) != 0 {
		t.Fatal("catalog leaked outside task")
	}
	recipientSchemaExec(t, w.web, `UPDATE water_intensity_profiles SET is_active=false WHERE id=$1`, profile)
	if _, err = q.GetFocusedWorkoutSelectedProfile(t.Context(), dbgen.GetFocusedWorkoutSelectedProfileParams{ActorID: w.actor, PlanID: w.plan, ProfileID: profile}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("inactive profile fallback returned")
	}
}

func TestFullWorkoutProfileWitnessCatalogDrift(t *testing.T) {
	w := newFocusedWaterFixture(t)
	for _, sql := range []string{
		`ALTER TABLE water_intensity_profiles ENABLE ROW LEVEL SECURITY`,
		`CREATE FUNCTION profile_witness_drift() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW;END$$;CREATE TRIGGER profile_witness_drift BEFORE UPDATE ON water_intensity_profiles FOR EACH ROW EXECUTE FUNCTION profile_witness_drift()`,
		`CREATE RULE profile_witness_drift AS ON UPDATE TO water_intensity_profiles DO ALSO NOTIFY profile_witness_drift`,
		`CREATE TABLE profile_witness_child() INHERITS(water_intensity_profiles)`,
		`ALTER TABLE water_work_steps DROP CONSTRAINT water_work_steps_shape_valid;ALTER TABLE water_work_steps ADD CONSTRAINT water_work_steps_shape_valid CHECK(true)`,
	} {
		tx, err := w.f.low.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		recipientSchemaExec(t, tx, sql)
		if err = verifyTrainingWorkoutGraphDefinition(t.Context(), tx); err == nil {
			t.Fatal("witness/shape drift accepted", sql)
		}
		if err = tx.Rollback(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFullWorkoutWholeSessionCounters(t *testing.T) {
	w := newFocusedWaterFixture(t)
	w.prepare(t)
	q := dbgen.New(w.web)
	id, err := graphBareSession(t, w, dbgen.TrainingEntryKindTRAINING, "START_ONLY")
	if err != nil {
		t.Fatal(err)
	}
	water, gym, run := uuid.New(), uuid.New(), uuid.New()
	for _, v := range []struct {
		id       uuid.UUID
		position int32
		kind     dbgen.TrainingSegmentModality
	}{{water, 1, dbgen.TrainingSegmentModalityWATER}, {gym, 2, dbgen.TrainingSegmentModalityGYM}, {run, 3, dbgen.TrainingSegmentModalityRUN}} {
		if _, err = q.InsertFocusedWorkoutSegment(t.Context(), dbgen.InsertFocusedWorkoutSegmentParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: v.id, Position: v.position, Modality: v.kind}); err != nil {
			t.Fatal(err)
		}
	}
	wa, wb, ga, gb := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, v := range []struct {
		id, seg  uuid.UUID
		position int32
		isWater  bool
	}{{wa, water, 1, true}, {wb, water, 2, true}, {ga, gym, 1, false}, {gb, gym, 2, false}} {
		if _, err = q.InsertFocusedWorkoutBlock(t.Context(), dbgen.InsertFocusedWorkoutBlockParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: v.seg, BlockID: v.id, Position: v.position, Purpose: dbgen.TrainingBlockPurposeMAIN, Instructions: "Synthetic bounded instruction"}); err != nil {
			t.Fatal(err)
		}
		if v.isWater {
			_, err = q.InsertFocusedWorkoutWaterPrescription(t.Context(), dbgen.InsertFocusedWorkoutWaterPrescriptionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: v.seg, BlockID: v.id, Method: dbgen.WaterWorkMethodCONTINUOUS})
		} else {
			_, err = q.InsertFocusedWorkoutGymPrescription(t.Context(), dbgen.InsertFocusedWorkoutGymPrescriptionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: v.seg, BlockID: v.id, Structure: dbgen.GymBlockStructureSUPERSET, Objective: dbgen.TrainingObjectiveCUSTOM, Rounds: 1})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	// Seed255 instructions in one block; sibling block may add only the256th,
	// not another256. Fixtures are fresh and owned, no production adoption.
	recipientSchemaExec(t, w.web, `INSERT INTO water_work_steps(block_id,position,kind,name,instructions) SELECT $1,n,'EFFORT','Synthetic bounded effort','Coach own words only' FROM generate_series(1,255)n`, wa)
	addStep := func(pos int32) (dbgen.WaterWorkStep, error) {
		return q.InsertFocusedWorkoutWaterStep(t.Context(), dbgen.InsertFocusedWorkoutWaterStepParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: water, BlockID: wb, StepID: uuid.New(), Position: pos, Kind: dbgen.WaterStepKindEFFORT, Name: "Last bounded effort", Instructions: "No inferred measurements"})
	}
	if _, err = addStep(1); err != nil {
		t.Fatal(err)
	}
	if _, err = addStep(2); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("water ceiling was per-block", err)
	}
	steps, err := q.ListFocusedWorkoutWaterSteps(t.Context(), dbgen.ListFocusedWorkoutWaterStepsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil || len(steps) != 256 {
		t.Fatal("exact256 raw steps incomplete", err)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO water_work_steps(block_id,position,kind,name,instructions) SELECT $1,n,'EFFORT','Synthetic larger advanced source','Detect sentinel before recursive traversal' FROM generate_series(2,300)n`, wb)
	steps, err = q.ListFocusedWorkoutWaterSteps(t.Context(), dbgen.ListFocusedWorkoutWaterStepsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil || len(steps) != 257 {
		t.Fatal("water257 sentinel missing", err)
	}
	// Recursive ordering must consume only the257 sentinel input rows, not an
	// unbounded larger source before its final result LIMIT.
	sourceSQL, readErr := os.ReadFile("queries/training_workout_graph.sql")
	if readErr != nil {
		t.Fatal(readErr)
	}
	sqlText := string(sourceSQL)
	marker := "-- name: ListFocusedWorkoutWaterSteps :many"
	at := strings.Index(sqlText, marker)
	if at < 0 {
		t.Fatal("raw source query missing")
	}
	sqlText = sqlText[at+len(marker):]
	if end := strings.Index(sqlText, "-- name:"); end >= 0 {
		sqlText = sqlText[:end]
	}
	sqlText = strings.NewReplacer("sqlc.arg(actor_id)", "$1", "sqlc.arg(plan_id)", "$2", "sqlc.arg(session_id)", "$3").Replace(sqlText)
	var explain []byte
	if err = w.web.QueryRow(t.Context(), "EXPLAIN (ANALYZE,FORMAT JSON) "+sqlText, w.actor, w.plan, id).Scan(&explain); err != nil {
		t.Fatal(err)
	}
	var explained any
	if err = json.Unmarshal(explain, &explained); err != nil {
		t.Fatal(err)
	}
	foundInput, foundTree := false, false
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				walk(child)
			}
		case map[string]any:
			if node["Subplan Name"] == "CTE water_nodes" {
				rows, _ := node["Actual Rows"].(float64)
				if rows > 257 {
					t.Fatalf("recursive source exceeded cap %.0f", rows)
				}
				foundInput = true
			}
			if node["Node Type"] == "Recursive Union" {
				rows, _ := node["Actual Rows"].(float64)
				if rows > 257 {
					t.Fatalf("recursive traversal exceeded cap %.0f", rows)
				}
				foundTree = true
			}
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(explained)
	if !foundInput || !foundTree {
		t.Fatal("missing real executor cap evidence")
	}
	if err = os.WriteFile("/tmp/mycfc-391-full-workout-water-cap-explain.json", explain, 0600); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO gym_exercises(block_id,position,name,duration_seconds,notes) SELECT $1,n,'Synthetic bounded exercise',1,'Explicit fixture quantity' FROM generate_series(1,255)n`, ga)
	addExercise := func(pos int32) (dbgen.GymExercise, error) {
		return q.InsertFocusedWorkoutGymExercise(t.Context(), dbgen.InsertFocusedWorkoutGymExerciseParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: gym, BlockID: gb, ExerciseID: uuid.New(), Position: pos, Name: "Last bounded exercise", DurationSeconds: graphPtr(int32(1))})
	}
	if _, err = addExercise(1); err != nil {
		t.Fatal(err)
	}
	if _, err = addExercise(2); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("gym ceiling was per-block", err)
	}
	exercises, err := q.ListFocusedWorkoutGymExercises(t.Context(), dbgen.ListFocusedWorkoutGymExercisesParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil || len(exercises) != 256 {
		t.Fatal("exact256 gym rows incomplete", err)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO gym_exercises(block_id,position,name,duration_seconds) VALUES($1,2,'Synthetic larger advanced exercise',1)`, gb)
	exercises, err = q.ListFocusedWorkoutGymExercises(t.Context(), dbgen.ListFocusedWorkoutGymExercisesParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil || len(exercises) != 257 {
		t.Fatal("gym257 sentinel missing", err)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(segment_id,position,purpose,instructions) SELECT $1,n,'MAIN','Synthetic bounded phase' FROM generate_series(3,29)n`, water)
	//4 original blocks+27=31, so RUN can add exactlyone, not32more.
	if _, err = q.InsertFocusedWorkoutBlock(t.Context(), dbgen.InsertFocusedWorkoutBlockParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: run, BlockID: uuid.New(), Position: 1, Purpose: dbgen.TrainingBlockPurposeMAIN, Instructions: "Last32nd phase"}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.InsertFocusedWorkoutBlock(t.Context(), dbgen.InsertFocusedWorkoutBlockParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: run, BlockID: uuid.New(), Position: 2, Purpose: dbgen.TrainingBlockPurposeMAIN, Instructions: "Rejected33rd phase"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("block ceiling was per-segment", err)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(segment_id,position,purpose,instructions) VALUES($1,2,'MAIN','Synthetic advanced count sentinel')`, run)
	blocks, err := q.ListFocusedWorkoutBlocks(t.Context(), dbgen.ListFocusedWorkoutBlocksParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil || len(blocks) != 33 {
		t.Fatal("block33 sentinel missing", err)
	}
	for pos := int32(4); pos <= 8; pos++ {
		if _, err = q.InsertFocusedWorkoutSegment(t.Context(), dbgen.InsertFocusedWorkoutSegmentParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: uuid.New(), Position: pos, Modality: dbgen.TrainingSegmentModalityRUN}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = q.InsertFocusedWorkoutSegment(t.Context(), dbgen.InsertFocusedWorkoutSegmentParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: uuid.New(), Position: 9, Modality: dbgen.TrainingSegmentModalityRUN}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("segment9 admitted", err)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_segments(session_id,position,modality) VALUES($1,9,'RUN')`, id)
	segments, err := q.ListFocusedWorkoutSegments(t.Context(), dbgen.ListFocusedWorkoutSegmentsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil || len(segments) != 9 {
		t.Fatal("segment9 sentinel missing", err)
	}
}

func TestFullWorkoutDefinitionPreservesStoredArtifacts(t *testing.T) {
	f := newRecipientSchemaFixture(t)
	f.install79(t)
	h := recipientHistoryFixture(t, f)
	routine := uuid.New()
	snapshot := `{"purpose":"MAIN","instructions":"Synthetic saved routine in original order","water":{"steps":[{"source_id":"00000000-0000-0000-0000-000000000002","position":2,"kind":"EFFORT","name":"Original first"},{"source_id":"00000000-0000-0000-0000-000000000001","position":1,"kind":"EFFORT","name":"Original second"}]}}`
	web := f.web(t)
	recipientSchemaExec(t, web, `INSERT INTO training_routines(id,name,kind,visibility,owner_user_id,source_id,source_updated_at,snapshot) VALUES($1,'Synthetic stored block','BLOCK','PRIVATE',$2,$3,$4,$5::jsonb)`, routine, h.actor, uuid.New(), h.source, snapshot)
	var before, after string
	if err := web.QueryRow(t.Context(), `SELECT to_jsonb(r)::text FROM training_routines r WHERE id=$1`, routine).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := ApplyBaselineAndHarden(t.Context(), f.low, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	if recipientHistoryBytes(t, f.low, h) != h.before {
		t.Fatal("definition changed stored publication/hash/outcome/plan/session")
	}
	if err := web.QueryRow(t.Context(), `SELECT to_jsonb(r)::text FROM training_routines r WHERE id=$1`, routine).Scan(&after); err != nil || before != after {
		t.Fatal("stored routine bytes/metadata rewritten", err)
	}
}
