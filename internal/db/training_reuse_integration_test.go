//go:build integration

package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func reuseHeader(t *testing.T, w focusedWaterFixture) dbgen.GetReusePlanRow {
	t.Helper()
	r, e := dbgen.New(w.web).GetReusePlan(t.Context(), dbgen.GetReusePlanParams{ActorID: w.actor, PlanID: w.plan})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func reuseWeek(t *testing.T, w focusedWaterFixture, season uuid.UUID, offset int) dbgen.TrainingPlan {
	t.Helper()
	q := dbgen.New(w.web)
	day := pgtype.Date{Time: w.day.Time.AddDate(0, 0, offset*7), Valid: true}
	target := dbgen.GetReuseTargetContextParams{ActorID: w.actor, GroupID: w.group, SeasonID: season, WeekStart: day}
	if r, e := q.GetReuseTargetContext(t.Context(), target); e != nil || r.ExistingPlanID != nil {
		t.Fatal("explicit new target", e)
	}
	r, e := q.CreateReuseWeek(t.Context(), dbgen.CreateReuseWeekParams{ID: uuid.New(), ActorID: w.actor, GroupID: w.group, SeasonID: season, WeekStart: day, Title: "Synthetic explicit reuse week", Description: "Synthetic fixture, not club dates"})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func reuseBegin(t *testing.T, w focusedWaterFixture, state TrainingScheduleState, target uuid.UUID) (pgx.Tx, *dbgen.Queries) {
	t.Helper()
	tx, e := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	recipientSchemaExec(t, tx, `SELECT public.training_schedule_admit_partial($1,$2)`, state.DatabaseIncarnation, state.StateEpoch)
	if _, e = VerifyTrainingWorkoutGraphRuntime(t.Context(), tx, TrainingScheduleCapabilities{ReaderVersion: 3, WriterVersion: 2}); e != nil {
		t.Fatal(e)
	}
	for _, sql := range []string{`SELECT id FROM users WHERE id=$1 FOR SHARE`, `SELECT user_id FROM user_platform_roles WHERE user_id=$1 FOR SHARE`, `SELECT id FROM staff_grants WHERE user_id=$1 FOR SHARE`} {
		recipientSchemaExec(t, tx, sql, w.actor)
	}
	q := dbgen.New(tx)
	if _, e = q.LockFocusedWaterCreatePlan(t.Context(), dbgen.LockFocusedWaterCreatePlanParams{ActorID: w.actor, PlanID: target}); e != nil {
		t.Fatal(e)
	}
	return tx, q
}
func reuseNativeInput(w focusedWaterFixture, target dbgen.TrainingPlan, src dbgen.GetFocusedWorkoutSessionRow) dbgen.InsertCopiedWorkoutSessionParams {
	shift := func(t pgtype.Timestamptz) pgtype.Timestamptz {
		if t.Valid {
			t.Time = t.Time.AddDate(0, 0, 7)
		}
		return t
	}
	date := src.ScheduledOn
	if date.Valid {
		date.Time = date.Time.AddDate(0, 0, 7)
	}
	return dbgen.InsertCopiedWorkoutSessionParams{ID: uuid.New(), ActorID: w.actor, TargetPlanID: target.ID, ExpectedPlanUpdatedAt: target.UpdatedAt, TargetDay: target.WeekStart, Title: src.Title, Description: src.Description, EntryKind: src.EntryKind, SchedulePrecision: src.SchedulePrecision, ScheduledOn: date, StartsAt: shift(src.StartsAt), EndsAt: shift(src.EndsAt), DayBand: src.DayBand, ModalityID: src.ModalityID}
}
func TestReuseExplicitSeasonNullableShapesAndEventReplay(t *testing.T) {
	w := newFocusedWaterFixture(t)
	w.prepare(t)
	q := dbgen.New(w.web)
	header := reuseHeader(t, w)
	explicit := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,'REUSE_EXPLICIT','Synthetic overlapping selection',CURRENT_DATE-30,CURRENT_DATE+400)`, explicit)
	target := reuseWeek(t, w, explicit, 1)
	if target.SeasonID == nil || *target.SeasonID != explicit || *target.SeasonID == *header.SeasonID {
		t.Fatal("selected season inferred instead of explicit")
	}
	lock, e := q.LockReuseTargetContext(t.Context(), dbgen.LockReuseTargetContextParams{ActorID: w.actor, GroupID: w.group, SeasonID: explicit, WeekStart: target.WeekStart})
	if e != nil || lock.ExistingPlanID == nil || *lock.ExistingPlanID != target.ID {
		t.Fatal("actual existing context", e)
	}
	_, e = q.GetReuseTargetContext(t.Context(), dbgen.GetReuseTargetContextParams{ActorID: w.actor, GroupID: w.group, SeasonID: *header.SeasonID, WeekStart: target.WeekStart})
	if !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("conflicting actual week season accepted", e)
	}
	_, e = q.CreateReuseWeek(t.Context(), dbgen.CreateReuseWeekParams{ID: uuid.New(), ActorID: w.actor, GroupID: w.group, SeasonID: explicit, WeekStart: target.WeekStart, Title: "Must not merge"})
	var unique *pgconn.PgError
	if !errors.As(e, &unique) || unique.Code != "23505" {
		t.Fatal("duplicate week silently merged", e)
	}
	exact := editExactSession(t, w)
	var modality uuid.UUID
	if e = w.web.QueryRow(t.Context(), `SELECT id FROM modalities ORDER BY id LIMIT 1`).Scan(&modality); e != nil {
		t.Fatal(e)
	}
	recipientSchemaExec(t, w.web, `UPDATE training_sessions SET modality_id=$2 WHERE id=$1`, exact, modality)
	ids := []uuid.UUID{exact}
	for _, tc := range []struct {
		kind      dbgen.TrainingEntryKind
		precision string
	}{{dbgen.TrainingEntryKindTRAINING, "START_ONLY"}, {dbgen.TrainingEntryKindREST, "DAY"}, {dbgen.TrainingEntryKindLOGISTICS, "BAND"}} {
		id, e := graphBareSession(t, w, tc.kind, tc.precision)
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, id)
	}
	var before string
	if e = w.web.QueryRow(t.Context(), `SELECT jsonb_agg(to_jsonb(s) ORDER BY id)::text FROM training_sessions s WHERE plan_id=$1`, w.plan).Scan(&before); e != nil {
		t.Fatal(e)
	}
	events := []uuid.UUID{}
	for _, id := range ids {
		src, e := q.GetFocusedWorkoutSession(t.Context(), dbgen.GetFocusedWorkoutSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
		if e != nil {
			t.Fatal(e)
		}
		ph, e := q.GetReusePlan(t.Context(), dbgen.GetReusePlanParams{ActorID: w.actor, PlanID: target.ID})
		if e != nil {
			t.Fatal(e)
		}
		target.UpdatedAt = ph.UpdatedAt
		input := reuseNativeInput(w, target, src)
		saved, e := q.InsertCopiedWorkoutSession(t.Context(), input)
		if e != nil {
			t.Fatal(e)
		}
		if saved.ID == src.ID || saved.SchedulePrecision != src.SchedulePrecision || saved.ScheduledOn.Valid != src.ScheduledOn.Valid || saved.StartsAt.Valid != src.StartsAt.Valid || saved.EndsAt.Valid != src.EndsAt.Valid || saved.CreatedByID != w.actor || saved.Status != "ACTIVE" {
			t.Fatal("copy changed precision/null/identity")
		}
		if id == exact && (saved.ModalityID == nil || *saved.ModalityID != modality) {
			t.Fatal("native modality lost")
		}
		stale := input
		stale.ID = uuid.New()
		if _, e = q.InsertCopiedWorkoutSession(t.Context(), stale); !errors.Is(e, pgx.ErrNoRows) {
			t.Fatal("old target version accepted", e)
		}
		eid := uuid.New()
		event, e := q.InsertReuseCopyEvent(t.Context(), dbgen.InsertReuseCopyEventParams{ID: eid, ActorID: w.actor, SourcePlanID: &w.plan, TargetPlanID: target.ID, SourceKind: "SESSION", SourceID: id, SourceUpdatedAt: src.UpdatedAt, DestinationKind: "SESSION", DestinationID: saved.ID})
		if e != nil || event.ID != eid {
			t.Fatal("provenance missing", e)
		}
		events = append(events, eid)
	}
	got, e := q.GetReuseCopyEventsByIDs(t.Context(), dbgen.GetReuseCopyEventsByIDsParams{ActorID: w.actor, EventIds: events, SourcePlanID: &w.plan, TargetPlanID: target.ID})
	if e != nil || len(got) != 4 {
		t.Fatal("retained replay IDs missing", e)
	}
	wrongHint := got[0].SourceUpdatedAt
	wrongHint.Time = wrongHint.Time.Add(-time.Second)
	if _, e = q.InsertReuseCopyEvent(t.Context(), dbgen.InsertReuseCopyEventParams{ID: uuid.New(), ActorID: w.actor, SourcePlanID: &w.plan, TargetPlanID: target.ID, SourceKind: got[0].SourceKind, SourceID: got[0].SourceID, SourceUpdatedAt: wrongHint, DestinationKind: got[0].DestinationKind, DestinationID: got[0].DestinationID}); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("wrong provenance version hint recorded", e)
	}
	wrongSource, e := q.GetReuseCopyEventsByIDs(t.Context(), dbgen.GetReuseCopyEventsByIDsParams{ActorID: w.actor, EventIds: events, SourcePlanID: &target.ID, TargetPlanID: target.ID})
	if e != nil || len(wrongSource) != 0 {
		t.Fatal("replay ignored actual source ancestry", e)
	}
	invalid := append(append([]uuid.UUID{}, events...), uuid.New())
	got, e = q.GetReuseCopyEventsByIDs(t.Context(), dbgen.GetReuseCopyEventsByIDsParams{ActorID: w.actor, EventIds: invalid, SourcePlanID: &w.plan, TargetPlanID: target.ID})
	if e != nil || len(got) != 0 {
		t.Fatal("partial event replay accepted", e)
	}
	var after string
	if e = w.web.QueryRow(t.Context(), `SELECT jsonb_agg(to_jsonb(s) ORDER BY id)::text FROM training_sessions s WHERE plan_id=$1`, w.plan).Scan(&after); e != nil || before != after {
		t.Fatal("copy changed source", e)
	}
	var sensitive int
	if e = w.web.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM training_plan_publications WHERE plan_id=$1)+(SELECT count(*) FROM training_session_outcomes o JOIN training_sessions s ON s.id=o.session_id WHERE s.plan_id=$1)`, target.ID).Scan(&sensitive); e != nil || sensitive != 0 {
		t.Fatal("copy cloned publication/outcome", e)
	}
}

func reuseRoutine(t *testing.T, w focusedWaterFixture, kind dbgen.TrainingRoutineKind, source uuid.UUID, name string) dbgen.TrainingRoutine {
	t.Helper()
	var snapshot []byte
	var at pgtype.Timestamptz
	function, table := "public.training_session_snapshot_v2", "training_sessions"
	if kind == dbgen.TrainingRoutineKindBLOCK {
		function, table = "training_block_snapshot", "training_segment_blocks"
	}
	if kind == dbgen.TrainingRoutineKindSEGMENT {
		function, table = "training_segment_snapshot", "training_session_segments"
	}
	if e := w.web.QueryRow(t.Context(), `SELECT `+function+`(id),updated_at FROM `+table+` WHERE id=$1`, source).Scan(&snapshot, &at); e != nil {
		t.Fatal(e)
	}
	r, e := dbgen.New(w.web).CreateTrainingRoutine(t.Context(), dbgen.CreateTrainingRoutineParams{Name: name, Kind: kind, Visibility: dbgen.TrainingRoutineVisibilitySHARED, OwnerUserID: w.actor, ProgrammeID: &w.programme, SourceID: source, SourceUpdatedAt: at, Snapshot: snapshot, Tags: []string{"synthetic"}})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestReuseRoutinesAndSourceBoundInactivePins(t *testing.T) {
	w := newFocusedWaterFixture(t)
	w.prepare(t)
	q := dbgen.New(w.web)
	id, e := graphBareSession(t, w, dbgen.TrainingEntryKindTRAINING, "START_ONLY")
	if e != nil {
		t.Fatal(e)
	}
	seg, block := uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_segments(id,session_id,position,modality) VALUES($1,$2,1,'WATER')`, seg, id)
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$2,1,'MAIN','Synthetic represented template')`, block, seg)
	profile, _ := graphProfile(t, w)
	recipientSchemaExec(t, w.web, `INSERT INTO water_block_prescriptions(block_id,method,intensity_profile_id,target_distance_metres,target_distance_certainty) VALUES($1,'INTERVALS',$2,8000,'EXACT')`, block, profile)
	if _, e = q.CreateWaterIntensityProfile(t.Context(), dbgen.CreateWaterIntensityProfileParams{Name: "Synthetic explicit profile", Craft: dbgen.PaddlingCraftKAYAK, CreatedByID: w.actor}); e != nil {
		t.Fatal(e)
	}
	pins, e := q.ListReusePinnedProfiles(t.Context(), dbgen.ListReusePinnedProfilesParams{ActorID: w.actor, NativeSourcePlanID: &w.plan, NativeSourceBlockIds: []uuid.UUID{block}})
	if e != nil || len(pins) != 1 || pins[0].ID != profile || pins[0].IsActive {
		t.Fatal("native inactive source pin missing", e)
	}
	dest, e := graphBareSession(t, w, dbgen.TrainingEntryKindTRAINING, "START_ONLY")
	if e != nil {
		t.Fatal(e)
	}
	dseg := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_segments(id,session_id,position,modality) VALUES($1,$2,1,'WATER')`, dseg, dest)
	for i, kind := range []dbgen.TrainingRoutineKind{dbgen.TrainingRoutineKindBLOCK, dbgen.TrainingRoutineKindSEGMENT, dbgen.TrainingRoutineKindSESSION} {
		source := id
		if kind == dbgen.TrainingRoutineKindBLOCK {
			source = block
		}
		if kind == dbgen.TrainingRoutineKindSEGMENT {
			source = seg
		}
		name := "Synthetic configured routine " + string(kind)
		if i == 0 {
			name = "Synthetic %_\\ configured routine"
		}
		routine := reuseRoutine(t, w, kind, source, name)
		read, e := q.GetReuseRoutine(t.Context(), dbgen.GetReuseRoutineParams{ActorID: w.actor, RoutineID: routine.ID})
		if e != nil || read.Overflow || !json.Valid(read.Snapshot) {
			t.Fatal("represented routine read failed", e)
		}
		if _, e = q.LockReuseRoutine(t.Context(), dbgen.LockReuseRoutineParams{ActorID: w.actor, RoutineID: routine.ID}); e != nil {
			t.Fatal(e)
		}
		rp, e := q.LockReusePinnedProfiles(t.Context(), dbgen.LockReusePinnedProfilesParams{ActorID: w.actor, RoutineID: &routine.ID})
		if e != nil || len(rp) != 1 || rp[0].ID != profile || rp[0].IsActive {
			t.Fatal("routine inactive pin missing", e)
		}
		zones, e := q.ListReusePinnedZones(t.Context(), dbgen.ListReusePinnedZonesParams{ActorID: w.actor, RoutineID: &routine.ID})
		if e != nil || len(zones) != 1 || zones[0].Code != "R2" {
			t.Fatal("routine pin exact zones lost", e)
		}
		db := uuid.New()
		recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$2,$3,'MAIN','Independent copied template')`, db, dseg, i+1)
		path := []int32{}
		if kind == dbgen.TrainingRoutineKindSEGMENT {
			path = []int32{0}
		}
		if kind == dbgen.TrainingRoutineKindSESSION {
			path = []int32{0, 0}
		}
		input := dbgen.InsertCopiedWaterPrescriptionParams{ActorID: w.actor, TargetPlanID: w.plan, TargetSessionID: dest, TargetSegmentID: dseg, TargetBlockID: db, RoutineID: &routine.ID, RoutinePath: path}
		copied, e := q.InsertCopiedWaterPrescription(t.Context(), input)
		if e != nil || copied.IntensityProfileID == nil || *copied.IntensityProfileID != profile || copied.TargetDistanceMetres == nil || *copied.TargetDistanceMetres != 8000 {
			t.Fatal("routine source-bound prescription", e)
		}
		input.TargetBlockID = uuid.New()
		recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$2,$3,'MAIN','Synthetic valid destination for invalid path')`, input.TargetBlockID, dseg, i+6)
		input.RoutinePath = append([]int32(nil), path...)
		if len(input.RoutinePath) == 0 {
			input.RoutinePath = []int32{-1}
		} else {
			input.RoutinePath[len(input.RoutinePath)-1] = -1
		}
		if _, e = q.InsertCopiedWaterPrescription(t.Context(), input); !errors.Is(e, pgx.ErrNoRows) {
			t.Fatal("negative/arbitrary routine path accepted", e)
		}
	}
	for _, literal := range []string{"%", "_", "\\"} {
		r, e := q.ListReuseRoutines(t.Context(), dbgen.ListReuseRoutinesParams{ActorID: w.actor, Query: literal})
		if e != nil || len(r) != 1 {
			t.Fatal("routine literal filter treated as wildcard", literal, e)
		}
	}
	db := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$2,4,'MAIN','Native independent copy')`, db, dseg)
	copied, e := q.InsertCopiedWaterPrescription(t.Context(), dbgen.InsertCopiedWaterPrescriptionParams{ActorID: w.actor, TargetPlanID: w.plan, TargetSessionID: dest, TargetSegmentID: dseg, TargetBlockID: db, NativeSourcePlanID: &w.plan, NativeSourceBlockID: &block})
	if e != nil || *copied.IntensityProfileID != profile {
		t.Fatal("native source-bound inactive pin", e)
	}
	// Existing generic CREATE still refuses an arbitrary inactive selection.
	db = uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$2,5,'MAIN','No arbitrary inactive selection')`, db, dseg)
	if _, e = q.InsertFocusedWorkoutWaterPrescription(t.Context(), dbgen.InsertFocusedWorkoutWaterPrescriptionParams{ActorID: w.actor, PlanID: w.plan, SessionID: dest, SegmentID: dseg, BlockID: db, Method: dbgen.WaterWorkMethodINTERVALS, IntensityProfileID: &profile}); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("inactive selection broadened", e)
	}
}

func TestReuseActualAuthorityTeamScopeAndRevokedRoutineRead(t *testing.T) {
	w := newFocusedWaterFixture(t)
	w.prepare(t)
	q := dbgen.New(w.web)
	header := reuseHeader(t, w)
	coach, team, group := uuid.New(), uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic reuse coach',$2,'hash','1980-01-01')`, coach, coach.String()+"@example.test")
	recipientSchemaExec(t, w.web, `INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, coach, w.programme, w.actor)
	recipientSchemaExec(t, w.web, `INSERT INTO teams(id,season_id,programme_id,code,name) VALUES($1,$2,$3,'SyntheticReuse','Synthetic exact team')`, team, header.SeasonID, w.programme)
	recipientSchemaExec(t, w.web, `INSERT INTO training_groups(id,name,team_id,created_by_id) VALUES($1,'Synthetic team training group',$2,$3)`, group, team, w.actor)
	ctx := dbgen.GetReuseTargetContextParams{ActorID: coach, GroupID: group, SeasonID: *header.SeasonID, WeekStart: w.day}
	if _, e := q.GetReusePlan(t.Context(), dbgen.GetReusePlanParams{ActorID: coach, PlanID: w.plan}); e != nil {
		t.Fatal("exact programme grant did not admit own source", e)
	}
	if _, e := q.GetReuseTargetContext(t.Context(), ctx); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("programme grant broadened into team authority", e)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO staff_grants(user_id,capability,team_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, coach, team, w.actor)
	if _, e := q.GetReuseTargetContext(t.Context(), ctx); e != nil {
		t.Fatal("exact team scope denied", e)
	}
	wrongSeason := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,'WRONG_TEAM','Synthetic overlapping team mismatch',CURRENT_DATE-30,CURRENT_DATE+400)`, wrongSeason)
	wrongContext := ctx
	wrongContext.SeasonID = wrongSeason
	if _, e := q.GetReuseTargetContext(t.Context(), wrongContext); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("wrong team-season target accepted", e)
	}
	if _, e := q.CreateReuseWeek(t.Context(), dbgen.CreateReuseWeekParams{ID: uuid.New(), ActorID: coach, GroupID: group, SeasonID: wrongSeason, WeekStart: w.day, Title: "Wrong team season"}); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("wrong team-season week created", e)
	}
	if _, e := q.LockReuseTargetContext(t.Context(), dbgen.LockReuseTargetContextParams(ctx)); e != nil {
		t.Fatal("matching team-season lock refused", e)
	}
	seasons, e := q.ListReuseTargetSeasons(t.Context(), dbgen.ListReuseTargetSeasonsParams{ActorID: coach, GroupID: group})
	if e != nil || len(seasons) != 1 || seasons[0].ID != *header.SeasonID {
		t.Fatal("team catalogue did not bind actual season", e)
	}
	programmeSeasons, e := q.ListReuseTargetSeasons(t.Context(), dbgen.ListReuseTargetSeasonsParams{ActorID: coach, GroupID: w.group})
	if e != nil || len(programmeSeasons) != 2 {
		t.Fatal("programme catalogue hidden by ADMIN-only rule", e)
	}
	id := editExactSession(t, w)
	routine := reuseRoutine(t, w, dbgen.TrainingRoutineKindSESSION, id, "Synthetic authorized session routine")
	if _, e := q.GetReuseRoutine(t.Context(), dbgen.GetReuseRoutineParams{ActorID: coach, RoutineID: routine.ID}); e != nil {
		t.Fatal("exact shared routine scope", e)
	}
	// Ownership is not a request-supplied administrator flag: this other actor
	// cannot read the now-private routine or return another scope's source facts.
	recipientSchemaExec(t, w.web, `UPDATE training_routines SET visibility='PRIVATE',programme_id=NULL WHERE id=$1`, routine.ID)
	if _, e := q.GetReuseRoutine(t.Context(), dbgen.GetReuseRoutineParams{ActorID: coach, RoutineID: routine.ID}); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("private routine leaked", e)
	}
	recipientSchemaExec(t, w.web, `UPDATE training_routines SET visibility='SHARED',programme_id=$2 WHERE id=$1`, routine.ID, w.programme)
	recipientSchemaExec(t, w.web, `UPDATE staff_grants SET revoked_at=clock_timestamp(),revoked_by_id=$1,revoke_reason='Synthetic revoke' WHERE user_id=$2`, w.actor, coach)
	if _, e := q.GetReusePlan(t.Context(), dbgen.GetReusePlanParams{ActorID: coach, PlanID: w.plan}); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("revoked source grant trusted", e)
	}
	if _, e := q.GetReuseRoutine(t.Context(), dbgen.GetReuseRoutineParams{ActorID: coach, RoutineID: routine.ID}); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("revoked routine grant trusted", e)
	}
	// Actual ADMIN identity becomes unavailable after deactivation/erasure even
	// while session claims could still say administrator.
	recipientSchemaExec(t, w.web, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, coach)
	recipientSchemaExec(t, w.web, `UPDATE users SET is_active=false WHERE id=$1`, w.actor)
	if _, e := q.GetReuseRoutine(t.Context(), dbgen.GetReuseRoutineParams{ActorID: w.actor, RoutineID: routine.ID}); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("inactive administrator admitted", e)
	}
}

func TestReuseSource64AndRoutineWireBounds(t *testing.T) {
	w := newFocusedWaterFixture(t)
	w.prepare(t)
	q := dbgen.New(w.web)
	recipientSchemaExec(t, w.web, `INSERT INTO training_sessions(plan_id,title,entry_kind,created_by_id,schedule_precision,scheduled_on) SELECT $1,'Synthetic bounded day '||n,'REST',$2,'DAY',$3::date+(n%7) FROM generate_series(1,64) n`, w.plan, w.actor, w.day)
	input := dbgen.ListReuseSourceSessionsParams{ActorID: w.actor, PlanID: w.plan}
	rows, e := q.ListReuseSourceSessions(t.Context(), input)
	if e != nil || len(rows) != 64 {
		t.Fatal("exact64 source boundary", e)
	}
	for i, row := range rows {
		if !row.SessionOn.Valid || (i > 0 && row.SessionOn.Time.Before(rows[i-1].SessionOn.Time)) {
			t.Fatal("civil date ordering invalid")
		}
	}
	extra, e := graphBareSession(t, w, dbgen.TrainingEntryKindLOGISTICS, "BAND")
	if e != nil {
		t.Fatal(e)
	}
	rows, e = q.ListReuseSourceSessions(t.Context(), input)
	if e != nil || len(rows) != 65 {
		t.Fatal("missing whole batch65 sentinel", e)
	}
	input.OwnDestinationSessionIds = []uuid.UUID{extra}
	rows, e = q.ListReuseSourceSessions(t.Context(), input)
	if e != nil || len(rows) != 64 {
		t.Fatal("own same-plan write not excluded before source sentinel", e)
	}
	input.OwnDestinationSessionIds = []uuid.UUID{extra, extra}
	rows, e = q.ListReuseSourceSessions(t.Context(), input)
	if e != nil || len(rows) != 0 {
		t.Fatal("duplicate own ID closure accepted", e)
	}
	input.OwnDestinationSessionIds = nil
	input.SourceDay = w.day
	rows, e = q.ListReuseSourceSessions(t.Context(), input)
	if e != nil || len(rows) == 0 {
		t.Fatal(e)
	}
	for _, row := range rows {
		if !row.SessionOn.Time.Equal(w.day.Time) {
			t.Fatal("day copy leaked another day")
		}
	}
	// Actual represented v1 JSON is allowed in the routine table. The new task
	// never emits an over-budget snapshot into its response/proposal.
	large := []byte(`{"instructions":"` + strings.Repeat("x", 1048577) + `"}`)
	routine, e := q.CreateTrainingRoutine(t.Context(), dbgen.CreateTrainingRoutineParams{Name: "Synthetic oversized fixture", Kind: dbgen.TrainingRoutineKindBLOCK, Visibility: dbgen.TrainingRoutineVisibilityPRIVATE, OwnerUserID: w.actor, SourceID: extra, SourceUpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}, Snapshot: large, Tags: []string{}})
	if e != nil {
		t.Fatal(e)
	}
	r, e := q.GetReuseRoutine(t.Context(), dbgen.GetReuseRoutineParams{ActorID: w.actor, RoutineID: routine.ID})
	if e != nil || !r.Overflow || r.SnapshotBytes <= 1048576 || len(r.Snapshot) != 0 {
		t.Fatal("oversized routine crossed wire guard", e)
	}
	// Metadata-only library pages remain bounded even with large snapshots.
	for n := 0; n < 22; n++ {
		if _, e = q.CreateTrainingRoutine(t.Context(), dbgen.CreateTrainingRoutineParams{Name: fmt.Sprintf("Synthetic configured Forca %02d", n), Kind: dbgen.TrainingRoutineKindBLOCK, Visibility: dbgen.TrainingRoutineVisibilityPRIVATE, OwnerUserID: w.actor, SourceID: extra, SourceUpdatedAt: routine.SourceUpdatedAt, Snapshot: []byte(`{"purpose":"MAIN","instructions":"Synthetic configured work"}`), Tags: []string{}}); e != nil {
			t.Fatal(e)
		}
	}
	page, e := q.ListReuseRoutines(t.Context(), dbgen.ListReuseRoutinesParams{ActorID: w.actor})
	if e != nil || len(page) != 21 {
		t.Fatal("routine metadata page21 sentinel", e)
	}
	// Explicit-season selector returns at most100+1 metadata rows, even for an
	// administrator with many configured seasons. It never sweeps graphs.
	recipientSchemaExec(t, w.web, `INSERT INTO seasons(code,name,starts_on,ends_on) SELECT 'SYN_CATALOG_'||n,'Synthetic catalogue season '||n,CURRENT_DATE-30,CURRENT_DATE+400 FROM generate_series(1,99) n`)
	seasonRows, e := q.ListReuseTargetSeasons(t.Context(), dbgen.ListReuseTargetSeasonsParams{ActorID: w.actor, GroupID: w.group})
	if e != nil || len(seasonRows) != 100 {
		t.Fatal("season catalogue exact100 boundary", e)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO seasons(code,name,starts_on,ends_on) VALUES('SYN_CATALOG_100','Synthetic catalogue overflow',CURRENT_DATE-30,CURRENT_DATE+400)`)
	seasonRows, e = q.ListReuseTargetSeasons(t.Context(), dbgen.ListReuseTargetSeasonsParams{ActorID: w.actor, GroupID: w.group})
	if e != nil || len(seasonRows) != 101 {
		t.Fatal("season catalogue101 sentinel missing", e)
	}

}

func TestReuseWholeOriginProfileAndZoneBounds(t *testing.T) {
	w := newFocusedWaterFixture(t)
	w.prepare(t)
	q := dbgen.New(w.web)
	segments := []uuid.UUID{}
	for i := 0; i < 2; i++ {
		id, e := graphBareSession(t, w, dbgen.TrainingEntryKindTRAINING, "START_ONLY")
		if e != nil {
			t.Fatal(e)
		}
		seg := uuid.New()
		recipientSchemaExec(t, w.web, `INSERT INTO training_session_segments(id,session_id,position,modality) VALUES($1,$2,1,'WATER')`, seg, id)
		segments = append(segments, seg)
	}
	blocks := []uuid.UUID{}
	profiles := []uuid.UUID{}
	for i := 0; i < 33; i++ {
		p, e := q.CreateWaterIntensityProfile(t.Context(), dbgen.CreateWaterIntensityProfileParams{Name: fmt.Sprintf("Synthetic explicit bound profile %02d", i), Craft: dbgen.PaddlingCraftKAYAK, CreatedByID: w.actor})
		if e != nil {
			t.Fatal(e)
		}
		profiles = append(profiles, p.ID)
		block := uuid.New()
		segment, position := segments[0], i+1
		if i == 32 {
			segment, position = segments[1], 1
		}
		recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$2,$3,'MAIN','Synthetic bounded source origin')`, block, segment, position)
		recipientSchemaExec(t, w.web, `INSERT INTO water_block_prescriptions(block_id,method,intensity_profile_id) VALUES($1,'INTERVALS',$2)`, block, p.ID)
		blocks = append(blocks, block)
	}
	origin := dbgen.ListReusePinnedProfilesParams{ActorID: w.actor, NativeSourcePlanID: &w.plan, NativeSourceBlockIds: blocks[:32]}
	found, e := q.ListReusePinnedProfiles(t.Context(), origin)
	if e != nil || len(found) != 32 {
		t.Fatal("exact32 profile union rejected", e)
	}
	origin.NativeSourceBlockIds = blocks
	found, e = q.ListReusePinnedProfiles(t.Context(), origin)
	if e != nil || len(found) != 0 {
		t.Fatal("33 profiles silently truncated", e)
	}
	origin.NativeSourceBlockIds = []uuid.UUID{blocks[0], blocks[0]}
	found, e = q.ListReusePinnedProfiles(t.Context(), origin)
	if e != nil || len(found) != 0 {
		t.Fatal("duplicate origin accepted", e)
	}
	origin.NativeSourceBlockIds = []uuid.UUID{blocks[0], uuid.New()}
	found, e = q.ListReusePinnedProfiles(t.Context(), origin)
	if e != nil || len(found) != 0 {
		t.Fatal("foreign origin subset returned", e)
	}
	for i := 0; i < 256; i++ {
		if _, e = q.CreateWaterIntensityZone(t.Context(), dbgen.CreateWaterIntensityZoneParams{ProfileID: profiles[0], Code: fmt.Sprintf("Z%03d", i), Label: "Synthetic bounded zone", Meaning: "Synthetic zone guidance"}); e != nil {
			t.Fatal(e)
		}
	}
	zonesInput := dbgen.ListReusePinnedZonesParams{ActorID: w.actor, NativeSourcePlanID: &w.plan, NativeSourceBlockIds: blocks[:32]}
	zones, e := q.ListReusePinnedZones(t.Context(), zonesInput)
	if e != nil || len(zones) != 256 {
		t.Fatal("exact256 zone closure rejected", e)
	}
	if _, e = q.CreateWaterIntensityZone(t.Context(), dbgen.CreateWaterIntensityZoneParams{ProfileID: profiles[0], Code: "Z256", Label: "Synthetic overflow zone", Meaning: "Synthetic zone guidance"}); e != nil {
		t.Fatal(e)
	}
	zones, e = q.ListReusePinnedZones(t.Context(), zonesInput)
	if e != nil || len(zones) != 257 {
		t.Fatal("missing zone257 sentinel", e)
	}
}

func TestReuseLateNowaitAndRoutineProfileWitness(t *testing.T) {
	w := newFocusedWaterFixture(t)
	state := w.prepare(t)
	q := dbgen.New(w.web)
	header := reuseHeader(t, w)
	target := reuseWeek(t, w, *header.SeasonID, 1)
	id, e := graphBareSession(t, w, dbgen.TrainingEntryKindTRAINING, "START_ONLY")
	if e != nil {
		t.Fatal(e)
	}
	seg, block := uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_segments(id,session_id,position,modality) VALUES($1,$2,1,'WATER')`, seg, id)
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$2,1,'MAIN','Synthetic mutable reference source')`, block, seg)
	profile, zone := graphProfile(t, w)
	recipientSchemaExec(t, w.web, `INSERT INTO water_block_prescriptions(block_id,method,intensity_profile_id) VALUES($1,'INTERVALS',$2)`, block, profile)
	routine := reuseRoutine(t, w, dbgen.TrainingRoutineKindSESSION, id, "Synthetic independently scoped routine source")
	other := w.f.web(t)
	for _, mode := range []string{"completed", "inflight"} {
		t.Run(mode, func(t *testing.T) {
			tx, tq := reuseBegin(t, w, state, target.ID)
			if _, e := tq.LockReuseRoutine(t.Context(), dbgen.LockReuseRoutineParams{ActorID: w.actor, RoutineID: routine.ID}); e != nil {
				t.Fatal(e)
			}
			origin := dbgen.LockReusePinnedProfilesParams{ActorID: w.actor, RoutineID: &routine.ID}
			if rows, e := tq.ListReusePinnedProfiles(t.Context(), dbgen.ListReusePinnedProfilesParams(origin)); e != nil || len(rows) != 1 {
				t.Fatal(e)
			}
			writer, e := other.Begin(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			defer writer.Rollback(context.Background())
			zone.Code = "R3"
			if mode == "inflight" {
				zone.Code = "R4"
			}
			if _, e = dbgen.New(writer).CreateWaterIntensityZone(t.Context(), zone); e != nil {
				t.Fatal(e)
			}
			code := "55P03"
			if mode == "completed" {
				if e = writer.Commit(t.Context()); e != nil {
					t.Fatal(e)
				}
				code = "40001"
			}
			_, e = tq.LockReusePinnedProfiles(t.Context(), origin)
			var pgErr *pgconn.PgError
			if !errors.As(e, &pgErr) || pgErr.Code != code {
				t.Fatalf("profile zone witness want%s got%v", code, e)
			}
			if e = tx.Rollback(t.Context()); e != nil {
				t.Fatal(e)
			}
		})
	}
	// Existing routine and config row changes are not silently waited past the
	// signed source/config snapshot. Both new late lock APIs refuse immediately.
	for _, kind := range []string{"routine", "configuration"} {
		t.Run(kind, func(t *testing.T) {
			holder, e := other.Begin(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			defer holder.Rollback(context.Background())
			if kind == "routine" {
				recipientSchemaExec(t, holder, `SELECT id FROM training_routines WHERE id=$1 FOR UPDATE`, routine.ID)
			} else {
				recipientSchemaExec(t, holder, `SELECT id FROM training_groups WHERE id=$1 FOR UPDATE`, w.group)
			}
			var lockErr error
			if kind == "routine" {
				_, lockErr = q.LockReuseRoutine(t.Context(), dbgen.LockReuseRoutineParams{ActorID: w.actor, RoutineID: routine.ID})
			} else {
				_, lockErr = q.LockReuseTargetContext(t.Context(), dbgen.LockReuseTargetContextParams{ActorID: w.actor, GroupID: w.group, SeasonID: *header.SeasonID, WeekStart: target.WeekStart})
			}
			var pgErr *pgconn.PgError
			if !errors.As(lockErr, &pgErr) || pgErr.Code != "55P03" {
				t.Fatal("late lock did not refuse conflict", lockErr)
			}
		})
	}
}

func reuseFields(t *testing.T, source, destination any) {
	t.Helper()
	raw, e := json.Marshal(source)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, destination); e != nil {
		t.Fatal(e)
	}
}
func TestReuseNativeGraphAtomicRollbackAndImmutableSource(t *testing.T) {
	w := newFocusedWaterFixture(t)
	state := w.prepare(t)
	q := dbgen.New(w.web)
	header := reuseHeader(t, w)
	target := reuseWeek(t, w, *header.SeasonID, 1)
	source := editExactSession(t, w)
	water, gym, block, strength, parent, child, warm, exercise := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_segments(id,session_id,position,modality,title) VALUES($1,$3,1,'WATER','Synthetic water'),($2,$3,2,'GYM','Synthetic strength')`, water, gym, source)
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$3,1,'MAIN','Synthetic nested work'),($2,$4,1,'MAIN','Synthetic own strength words')`, block, strength, water, gym)
	profile, _ := graphProfile(t, w)
	recipientSchemaExec(t, w.web, `INSERT INTO water_block_prescriptions(block_id,method,intensity_profile_id) VALUES($1,'INTERVALS',$2)`, block, profile)
	recipientSchemaExec(t, w.web, `INSERT INTO water_work_steps(id,block_id,parent_step_id,position,kind,name,repeats,duration_seconds,duration_certainty,recovery_seconds,instructions) VALUES($1,$4,NULL,2,'REPEAT_GROUP','Synthetic nested group',10,NULL,NULL,120,'Group own words'),($2,$4,$1,1,'EFFORT','Synthetic explicit effort',NULL,240,'EXACT',60,'Effort own words'),($3,$4,NULL,1,'EFFORT','Synthetic warmup',NULL,600,'ESTIMATED',NULL,'Warmup own words')`, parent, child, warm, block)
	recipientSchemaExec(t, w.web, `INSERT INTO gym_block_prescriptions(block_id,structure,objective,rounds,round_recovery_seconds) VALUES($1,'CIRCUIT','CUSTOM',3,90)`, strength)
	recipientSchemaExec(t, w.web, `INSERT INTO gym_exercises(id,block_id,position,name,sets,repetitions,resistance_kind,resistance_value,execution_intent,tempo,notes) VALUES($1,$2,1,'Synthetic squat',3,8,'KILOGRAMS',12.5,'CONTROLLED','3-1-1','Synthetic loaded exercise')`, exercise, strength)
	routine := reuseRoutine(t, w, dbgen.TrainingRoutineKindSESSION, source, "Synthetic detached stored source routine")
	// Real stored publication/hash and completed outcome, never copied.
	subject, membership, pub, presc := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic stored outcome subject',$2,'hash','1980-01-01')`, subject, subject.String()+"@example.test")
	recipientSchemaExec(t, w.web, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) SELECT $1,$2,season_id,programme_id,CURRENT_DATE,CURRENT_DATE+300 FROM training_plans WHERE id=$3`, membership, subject, w.plan)
	recipientSchemaExec(t, w.web, `INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, w.group, membership, w.actor)
	recipientSchemaExec(t, w.web, `INSERT INTO training_plan_publications(id,plan_id,revision,source_updated_at,change_summary,published_by_id) SELECT $1,id,1,updated_at,'Synthetic source published once',$3 FROM training_plans WHERE id=$2`, pub, w.plan, w.actor)
	raw := `{"schema_version":1,"session":{"EntryKind":"WORKOUT"}}`
	sum := sha256.Sum256([]byte(raw))
	recipientSchemaExec(t, w.web, `INSERT INTO training_prescriptions(id,publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256) VALUES($1,$2,$3,$4,$5,$6,$7)`, presc, pub, source, membership, subject, raw, hex.EncodeToString(sum[:]))
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_outcomes(session_id,user_id,prescription_id,status) VALUES($1,$2,$3,'COMPLETED')`, source, subject, presc)
	history := func() string {
		var b string
		if e := w.web.QueryRow(t.Context(), `SELECT jsonb_build_array((SELECT to_jsonb(p) FROM training_plans p WHERE id=$1),(SELECT to_jsonb(r) FROM training_routines r WHERE id=$2),(SELECT to_jsonb(p) FROM training_plan_publications p WHERE id=$3),(SELECT to_jsonb(p) FROM training_prescriptions p WHERE id=$4),(SELECT to_jsonb(o) FROM training_session_outcomes o WHERE session_id=$5 AND user_id=$6),(SELECT jsonb_agg(to_jsonb(m) ORDER BY membership_id) FROM training_group_members m WHERE group_id=$7))::text`, w.plan, routine.ID, pub, presc, source, subject, w.group).Scan(&b); e != nil {
			t.Fatal(e)
		}
		return b
	}
	currentTarget, e := q.GetReusePlan(t.Context(), dbgen.GetReusePlanParams{ActorID: w.actor, PlanID: target.ID})
	if e != nil {
		t.Fatal(e)
	}
	target.UpdatedAt = currentTarget.UpdatedAt
	beforeHistory, beforeGraph := history(), editGraphJSON(t, w, source)
	sourceRow, e := q.GetFocusedWorkoutSession(t.Context(), dbgen.GetFocusedWorkoutSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: source})
	if e != nil {
		t.Fatal(e)
	}
	segments, e := q.ListFocusedWorkoutSegments(t.Context(), dbgen.ListFocusedWorkoutSegmentsParams{ActorID: w.actor, PlanID: w.plan, SessionID: source})
	if e != nil {
		t.Fatal(e)
	}
	blocks, e := q.ListFocusedWorkoutBlocks(t.Context(), dbgen.ListFocusedWorkoutBlocksParams{ActorID: w.actor, PlanID: w.plan, SessionID: source})
	if e != nil {
		t.Fatal(e)
	}
	steps, e := q.ListFocusedWorkoutWaterSteps(t.Context(), dbgen.ListFocusedWorkoutWaterStepsParams{ActorID: w.actor, PlanID: w.plan, SessionID: source})
	if e != nil {
		t.Fatal(e)
	}
	gps, e := q.ListFocusedWorkoutGymPrescriptions(t.Context(), dbgen.ListFocusedWorkoutGymPrescriptionsParams{ActorID: w.actor, PlanID: w.plan, SessionID: source})
	if e != nil {
		t.Fatal(e)
	}
	exs, e := q.ListFocusedWorkoutGymExercises(t.Context(), dbgen.ListFocusedWorkoutGymExercisesParams{ActorID: w.actor, PlanID: w.plan, SessionID: source})
	if e != nil {
		t.Fatal(e)
	}
	destination, eventID := uuid.New(), uuid.New()
	mapping := map[uuid.UUID]uuid.UUID{}
	for _, s := range segments {
		mapping[s.ID] = uuid.New()
	}
	for _, b := range blocks {
		mapping[b.ID] = uuid.New()
	}
	for _, s := range steps {
		mapping[s.ID] = uuid.New()
	}
	for _, x := range exs {
		mapping[x.ID] = uuid.New()
	}
	pool := w.pool(t)
	save := func(fault bool) (SessionTxResult, error) {
		return WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(ctx context.Context, commands dbgen.DBTX) error {
			if _, e := commands.Exec(ctx, `SELECT public.training_schedule_admit_partial($1,$2)`, state.DatabaseIncarnation, state.StateEpoch); e != nil {
				return e
			}
			if _, e := VerifyTrainingWorkoutGraphRuntime(ctx, commands, TrainingScheduleCapabilities{ReaderVersion: 3, WriterVersion: 2}); e != nil {
				return e
			}
			for _, sql := range []string{`SELECT id FROM users WHERE id=$1 FOR SHARE`, `SELECT user_id FROM user_platform_roles WHERE user_id=$1 FOR SHARE`, `SELECT id FROM staff_grants WHERE user_id=$1 FOR SHARE`} {
				if _, e := commands.Exec(ctx, sql, w.actor); e != nil {
					return e
				}
			}
			tq := dbgen.New(commands)
			if _, e := tq.LockReuseTargetContext(ctx, dbgen.LockReuseTargetContextParams{ActorID: w.actor, GroupID: w.group, SeasonID: *header.SeasonID, WeekStart: target.WeekStart}); e != nil {
				return e
			}
			plans := []uuid.UUID{w.plan, target.ID}
			sort.Slice(plans, func(i, j int) bool { return plans[i].String() < plans[j].String() })
			for _, id := range plans {
				if _, e := tq.LockFocusedWaterCreatePlan(ctx, dbgen.LockFocusedWaterCreatePlanParams{ActorID: w.actor, PlanID: id}); e != nil {
					return e
				}
			}
			if pins, e := tq.LockReusePinnedProfiles(ctx, dbgen.LockReusePinnedProfilesParams{ActorID: w.actor, NativeSourcePlanID: &w.plan, NativeSourceBlockIds: []uuid.UUID{block}}); e != nil || len(pins) != 1 {
				return fmt.Errorf("source reference closure: %w", e)
			}
			input := reuseNativeInput(w, target, sourceRow)
			input.ID = destination
			if _, e := tq.InsertCopiedWorkoutSession(ctx, input); e != nil {
				return e
			}
			for _, row := range segments {
				var p dbgen.InsertFocusedWorkoutSegmentParams
				reuseFields(t, row, &p)
				p.ActorID = w.actor
				p.PlanID = target.ID
				p.SessionID = destination
				p.SegmentID = mapping[row.ID]
				if _, e := tq.InsertFocusedWorkoutSegment(ctx, p); e != nil {
					return e
				}
			}
			for _, row := range blocks {
				var p dbgen.InsertFocusedWorkoutBlockParams
				reuseFields(t, row, &p)
				p.ActorID = w.actor
				p.PlanID = target.ID
				p.SessionID = destination
				p.SegmentID = mapping[row.SegmentID]
				p.BlockID = mapping[row.ID]
				if _, e := tq.InsertFocusedWorkoutBlock(ctx, p); e != nil {
					return e
				}
			}
			if _, e := tq.InsertCopiedWaterPrescription(ctx, dbgen.InsertCopiedWaterPrescriptionParams{ActorID: w.actor, TargetPlanID: target.ID, TargetSessionID: destination, TargetSegmentID: mapping[water], TargetBlockID: mapping[block], NativeSourcePlanID: &w.plan, NativeSourceBlockID: &block}); e != nil {
				return e
			}
			for _, row := range steps {
				var p dbgen.InsertFocusedWorkoutWaterStepParams
				reuseFields(t, row, &p)
				p.ActorID = w.actor
				p.PlanID = target.ID
				p.SessionID = destination
				p.SegmentID = mapping[water]
				p.BlockID = mapping[row.BlockID]
				p.StepID = mapping[row.ID]
				if row.ParentStepID != nil {
					parent := mapping[*row.ParentStepID]
					p.ParentStepID = &parent
				}
				if _, e := tq.InsertFocusedWorkoutWaterStep(ctx, p); e != nil {
					return e
				}
			}
			for _, row := range gps {
				var p dbgen.InsertFocusedWorkoutGymPrescriptionParams
				reuseFields(t, row, &p)
				p.ActorID = w.actor
				p.PlanID = target.ID
				p.SessionID = destination
				p.SegmentID = mapping[gym]
				p.BlockID = mapping[row.BlockID]
				if _, e := tq.InsertFocusedWorkoutGymPrescription(ctx, p); e != nil {
					return e
				}
			}
			for _, row := range exs {
				var p dbgen.InsertFocusedWorkoutGymExerciseParams
				reuseFields(t, row, &p)
				p.ActorID = w.actor
				p.PlanID = target.ID
				p.SessionID = destination
				p.SegmentID = mapping[gym]
				p.BlockID = mapping[row.BlockID]
				p.ExerciseID = mapping[row.ID]
				if _, e := tq.InsertFocusedWorkoutGymExercise(ctx, p); e != nil {
					return e
				}
			}
			provenance := dbgen.InsertReuseCopyEventParams{ID: eventID, ActorID: w.actor, TargetPlanID: target.ID, SourcePlanID: &w.plan, SourceKind: "SESSION", SourceID: source, SourceUpdatedAt: sourceRow.UpdatedAt, DestinationKind: "SESSION", DestinationID: destination}
			if _, e := tq.InsertReuseCopyEvent(ctx, provenance); e != nil {
				return e
			}
			if fault {
				_, e := tq.InsertReuseCopyEvent(ctx, provenance)
				return e
			}
			return nil
		})
	}
	result, e := save(true)
	if e == nil || result.Outcome != SessionTxNotCommitted {
		t.Fatal("last provenance failure partly committed", e)
	}
	if _, e = q.GetFocusedWorkoutSession(t.Context(), dbgen.GetFocusedWorkoutSessionParams{ActorID: w.actor, PlanID: target.ID, SessionID: destination}); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("failed copy retained destination", e)
	}
	ph, e := q.GetReusePlan(t.Context(), dbgen.GetReusePlanParams{ActorID: w.actor, PlanID: target.ID})
	if e != nil || !ph.UpdatedAt.Time.Equal(target.UpdatedAt.Time) {
		t.Fatal("rollback changed target version", e)
	}
	result, e = save(false)
	if e != nil || result.Outcome != SessionTxCommitted {
		t.Fatal("atomic mixed copy failed", e)
	}
	copiedSteps, e := q.ListFocusedWorkoutWaterSteps(t.Context(), dbgen.ListFocusedWorkoutWaterStepsParams{ActorID: w.actor, PlanID: target.ID, SessionID: destination})
	if e != nil || len(copiedSteps) != 3 || copiedSteps[0].ID != mapping[warm] || copiedSteps[1].ID != mapping[parent] || copiedSteps[2].ParentStepID == nil || *copiedSteps[2].ParentStepID != mapping[parent] || *copiedSteps[1].RecoverySeconds != 120 || *copiedSteps[2].DurationSeconds != 240 {
		t.Fatal("nested copy ordering/quantity/parent remap changed", e)
	}
	copiedEx, e := q.ListFocusedWorkoutGymExercises(t.Context(), dbgen.ListFocusedWorkoutGymExercisesParams{ActorID: w.actor, PlanID: target.ID, SessionID: destination})
	if e != nil || len(copiedEx) != 1 || copiedEx[0].ID != mapping[exercise] || *copiedEx[0].ResistanceValue != 12.5 || *copiedEx[0].Sets != 3 {
		t.Fatal("gym copy load lost", e)
	}
	if history() != beforeHistory || editGraphJSON(t, w, source) != beforeGraph {
		t.Fatal("copy rewrote source/currenthistory/publication/hash/outcome/routine/roster")
	}
	var clones int
	if e = w.web.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM training_plan_publications WHERE plan_id=$1)+(SELECT count(*) FROM training_session_outcomes WHERE session_id=$2)`, target.ID, destination).Scan(&clones); e != nil || clones != 0 {
		t.Fatal("sensitive source rows cloned", e)
	}
}
