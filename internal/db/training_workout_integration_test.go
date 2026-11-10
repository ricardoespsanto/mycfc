//go:build integration

package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type focusedWaterFixture struct {
	f                             recipientSchemaFixture
	web                           *pgx.Conn
	actor, plan, group, programme uuid.UUID
	day                           pgtype.Date
}

func newFocusedWaterFixture(t *testing.T) focusedWaterFixture {
	t.Helper()
	f := newRecipientSchemaFixture(t)
	if err := ApplyBaselineAndHarden(t.Context(), f.low, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	w := focusedWaterFixture{f: f, web: f.web(t), actor: uuid.New(), plan: uuid.New(), group: uuid.New()}
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic water coach',$2,'hash','1980-01-01')`, w.actor, w.actor.String()+"@example.test")
	recipientSchemaExec(t, w.web, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, w.actor)
	season := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,'SYN_WATER','Synthetic water season',CURRENT_DATE-365,CURRENT_DATE+400)`, season)
	if err := w.web.QueryRow(t.Context(), `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&w.programme); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Synthetic water group',$2,$3)`, w.group, w.programme, w.actor)
	recipientSchemaExec(t, w.web, `INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) VALUES($1,'Synthetic water week',$2,$3,$4,date_trunc('week',CURRENT_DATE)::date+7,$5)`, w.plan, w.programme, w.group, season, w.actor)
	if err := w.web.QueryRow(t.Context(), `SELECT week_start FROM training_plans WHERE id=$1`, w.plan).Scan(&w.day); err != nil {
		t.Fatal(err)
	}
	return w
}
func (w focusedWaterFixture) prepare(t *testing.T) TrainingScheduleState {
	t.Helper()
	s, err := TrainingScheduleStatus(t.Context(), w.f.low)
	if err != nil {
		t.Fatal(err)
	}
	if s.SchemaContract != TrainingScheduleContractP || s.PartialV2Enabled {
		t.Fatal("fresh P must start closed")
	}
	var epoch int64
	if err = w.f.low.QueryRow(t.Context(), `SELECT training_schedule_control.commit_floor($1,$2,$3,$4,3)`, s.DatabaseIncarnation, s.StateEpoch, uuid.New(), strings.Repeat("a", 64)).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	if err = w.f.low.QueryRow(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,true)`, s.DatabaseIncarnation, epoch).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	s, err = TrainingScheduleStatus(t.Context(), w.web)
	if err != nil || !s.PartialV2Enabled || s.RequiredReader != 3 {
		t.Fatalf("actual isolated owner preparation failed %v", err)
	}
	return s
}
func (w focusedWaterFixture) sessionInput(t *testing.T, id uuid.UUID) dbgen.InsertFocusedWaterSessionParams {
	t.Helper()
	header, err := dbgen.New(w.web).GetFocusedWaterCreateContext(t.Context(), dbgen.GetFocusedWaterCreateContextParams{ActorID: w.actor, PlanID: w.plan, Day: w.day})
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/Lisbon")
	d := w.day.Time
	start := time.Date(d.Year(), d.Month(), d.Day(), 10, 0, 0, 0, loc)
	return dbgen.InsertFocusedWaterSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, Title: "Synthetic own words", Description: "Coach general notes", StartsAt: pgtype.Timestamptz{Time: start, Valid: true}, SchedulePrecision: "START_ONLY", ScheduledOn: w.day, ExpectedPlanUpdatedAt: header.PlanUpdatedAt}
}
func (w focusedWaterFixture) pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(w.f.url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.User = w.f.roles.AppUsername
	cfg.ConnConfig.Password = w.f.roles.AppPassword
	cfg.MaxConns = 1
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}
func TestFocusedWaterFreshShapesAndGate(t *testing.T) {
	w := newFocusedWaterFixture(t)
	s, err := TrainingScheduleStatus(t.Context(), w.web)
	if err != nil || s.SchemaContract != TrainingScheduleContractP || s.PartialV2Enabled || s.RequiredReader != 1 {
		t.Fatalf("fresh metadata %v %+v", err, s)
	}
	p := w.pool(t)
	input := w.sessionInput(t, uuid.New())
	run, err := WithRecipientPublicationTx(t.Context(), p, recipientTestOptions(), func(ctx context.Context, q dbgen.DBTX) error {
		_, err := q.Exec(ctx, `SELECT public.training_schedule_admit_partial($1,$2)`, s.DatabaseIncarnation, s.StateEpoch)
		if err != nil {
			return err
		}
		_, err = dbgen.New(q).InsertFocusedWaterSession(ctx, input)
		return err
	})
	if err == nil || run.Outcome != SessionTxNotCommitted {
		t.Fatal("closed authoring gate accepted save")
	}
	for _, sql := range []string{`SELECT training_schedule_control.set_enabled(NULL,NULL,true)`, `UPDATE training_schedule_control.state SET partial_v2_enabled=true`} {
		if _, err = w.web.Exec(t.Context(), sql); err == nil {
			t.Fatal("web gained activation rights")
		}
	}
	// Even the owner activation API cannot open authoring at recipient-unsafe floor2.
	var floor2epoch int64
	if err = w.f.low.QueryRow(t.Context(), `SELECT training_schedule_control.commit_floor($1,$2,$3,$4,2)`, s.DatabaseIncarnation, s.StateEpoch, uuid.New(), strings.Repeat("b", 64)).Scan(&floor2epoch); err != nil {
		t.Fatal(err)
	}
	if _, err = w.f.low.Exec(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,true)`, s.DatabaseIncarnation, floor2epoch); err == nil {
		t.Fatal("owner opened authoring below corrected recipient floor3")
	}
	s = w.prepare(t)
	if _, err = VerifyTrainingRecipientRuntime(t.Context(), w.web, TrainingScheduleCapabilities{ReaderVersion: 3, WriterVersion: 2}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"TRAINING", "COMPETITION", "REST", "LOGISTICS"} {
		for _, shape := range []string{"INTERVAL", "START_ONLY", "DAY", "BAND"} {
			t.Run(kind+"/"+shape, func(t *testing.T) {
				tx, err := w.web.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(t.Context())
				recipientSchemaExec(t, tx, `SELECT public.training_schedule_admit_partial($1,$2)`, s.DatabaseIncarnation, s.StateEpoch)
				var start, end any
				var band any
				if shape == "INTERVAL" || shape == "START_ONLY" {
					start = input.StartsAt.Time
				}
				if shape == "INTERVAL" {
					end = input.StartsAt.Time.Add(time.Hour)
				}
				if shape == "BAND" {
					band = "MORNING"
				}
				_, err = tx.Exec(t.Context(), `INSERT INTO training_sessions(plan_id,title,starts_at,ends_at,entry_kind,created_by_id,schedule_precision,scheduled_on,day_band) VALUES($1,'Synthetic shape',$2,$3,$4,$5,$6,$7,$8)`, w.plan, start, end, kind, w.actor, shape, w.day, band)
				want := (shape == "INTERVAL" || shape == "START_ONLY") || (kind == "REST" || kind == "LOGISTICS")
				if (err == nil) != want {
					t.Fatalf("kind/shape accepted=%v expected=%v err=%v", err == nil, want, err)
				}
			})
		}
	}
	// NULL shape, missing actual start, end-only, band+clock and out-of-week must fail even through direct SQL.
	for _, tuple := range []string{`NULL,NULL,NULL,NULL`, `'START_ONLY',NULL,NULL,NULL`, `'INTERVAL',NULL,clock_timestamp(),NULL`, `'DAY',clock_timestamp(),NULL,NULL`, `'BAND',NULL,NULL,NULL`} {
		_, err = w.web.Exec(t.Context(), `INSERT INTO training_sessions(plan_id,title,schedule_precision,starts_at,ends_at,day_band,scheduled_on,entry_kind,created_by_id) VALUES($1,'Synthetic invalid',`+tuple+`,$2,'REST',$3)`, w.plan, w.day, w.actor)
		if err == nil {
			t.Fatal("invalid SQL NULL/shape accepted", tuple)
		}
	}
	for _, epoch := range []int64{s.StateEpoch - 1, s.StateEpoch + 1} {
		if _, err = w.web.Exec(t.Context(), `SELECT public.training_schedule_admit_partial($1,$2)`, s.DatabaseIncarnation, epoch); err == nil {
			t.Fatal("stale/ABA epoch admitted")
		}
	}
	if _, err = w.web.Exec(t.Context(), `SELECT public.training_schedule_admit_partial($1,$2)`, uuid.New(), s.StateEpoch); err == nil {
		t.Fatal("foreign incarnation admitted")
	}
}
func TestFocusedWaterAtomicPlainRoundtripAndBounds(t *testing.T) {
	w := newFocusedWaterFixture(t)
	s := w.prepare(t)
	p := w.pool(t)
	q := dbgen.New(w.web)
	id, segment := uuid.New(), uuid.New()
	input := w.sessionInput(t, id)
	save := func(fault bool) (SessionTxResult, error) {
		return WithRecipientPublicationTx(t.Context(), p, recipientTestOptions(), func(ctx context.Context, commands dbgen.DBTX) error {
			if _, err := commands.Exec(ctx, `SELECT public.training_schedule_admit_partial($1,$2)`, s.DatabaseIncarnation, s.StateEpoch); err != nil {
				return err
			}
			if _, err := VerifyTrainingRecipientRuntime(ctx, commands, TrainingScheduleCapabilities{ReaderVersion: 3, WriterVersion: 2}); err != nil {
				return err
			}
			queries := dbgen.New(commands)
			if _, err := queries.LockFocusedWaterCreatePlan(ctx, dbgen.LockFocusedWaterCreatePlanParams{ActorID: w.actor, PlanID: w.plan}); err != nil {
				return err
			}
			if _, err := queries.InsertFocusedWaterSession(ctx, input); err != nil {
				return err
			}
			if _, err := queries.InsertFocusedWaterSegment(ctx, dbgen.InsertFocusedWaterSegmentParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: segment, Location: "Synthetic river", EquipmentNotes: "Coach equipment note"}); err != nil {
				return err
			}
			for i, purpose := range []dbgen.TrainingBlockPurpose{dbgen.TrainingBlockPurposeWARMUP, dbgen.TrainingBlockPurposeMAIN, dbgen.TrainingBlockPurposeCOOLDOWN, dbgen.TrainingBlockPurposeCUSTOM} {
				instructions := "Coach own words " + string(purpose)
				if fault && i == 3 {
					instructions = "x"
				}
				if _, err := queries.InsertFocusedWaterPhase(ctx, dbgen.InsertFocusedWaterPhaseParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: segment, PhaseID: uuid.New(), Position: int32(i + 1), Purpose: purpose, Title: "Coach phase", Instructions: instructions}); err != nil {
					return err
				}
			}
			got, err := queries.GetFocusedWaterSession(ctx, dbgen.GetFocusedWaterSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
			if err != nil {
				return err
			}
			if got.EndsAt.Valid || got.HasExtraDetail || got.SchedulePrecision != "START_ONLY" {
				return errors.New("lossy raw timing/detail")
			}
			rows, err := queries.ListFocusedWaterPhases(ctx, dbgen.ListFocusedWaterPhasesParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
			if err != nil {
				return err
			}
			if len(rows) != 4 || rows[3].Purpose != dbgen.TrainingBlockPurposeCUSTOM {
				return errors.New("phase order mismatch")
			}
			return nil
		})
	}
	run, err := save(true)
	if err == nil || run.Outcome != SessionTxNotCommitted {
		t.Fatal("last phase fault partially succeeded")
	}
	if _, err = q.GetFocusedWaterSession(t.Context(), dbgen.GetFocusedWaterSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("rolled-back graph remained")
	}
	run, err = save(false)
	if err != nil || run.Outcome != SessionTxCommitted {
		t.Fatalf("atomic create failed %+v %v", run, err)
	}
	rows, err := q.ListFocusedWaterSegments(t.Context(), dbgen.ListFocusedWaterSegmentsParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil || len(rows) != 1 || rows[0].Modality != dbgen.TrainingSegmentModalityWATER || rows[0].PlannedDurationMinutes != nil {
		t.Fatal("raw activity invented duration/details")
	}
	run, err = save(false)
	if err == nil || run.Outcome != SessionTxNotCommitted {
		t.Fatal("stale same-ID confirmation duplicated session")
	}
	// Exact PG text byte count, including jsonb-added whitespace, is authoritative.
	for _, n := range []int{199991, 199992} {
		raw := []byte(`{"x":"` + strings.Repeat("a", n) + `"}`)
		got, err := q.FocusedWaterArtifactSize(t.Context(), dbgen.FocusedWaterArtifactSizeParams{ActorID: w.actor, PlanID: w.plan, Snapshot: raw})
		if err != nil || int(got) != n+9 {
			t.Fatalf("actual PostgreSQL artifact bytes %d %v", got, err)
		}
	}
	// Maximum admitted plain graph remains lossless at the approved phase/text limits.
	startMax := time.Now()
	for i := 5; i <= 32; i++ {
		if _, err = q.InsertFocusedWaterPhase(t.Context(), dbgen.InsertFocusedWaterPhaseParams{ActorID: w.actor, PlanID: w.plan, SessionID: id, SegmentID: segment, PhaseID: uuid.New(), Position: int32(i), Purpose: dbgen.TrainingBlockPurposeCUSTOM, Instructions: strings.Repeat("a", 4000)}); err != nil {
			t.Fatal(err)
		}
	}
	maximum, err := q.ListFocusedWaterPhases(t.Context(), dbgen.ListFocusedWaterPhasesParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil || len(maximum) != 32 || len(maximum[31].Instructions) != 4000 {
		t.Fatal("maximum plain graph truncated", err)
	}
	t.Logf("synthetic32phase read/write elapsed=%s; source field limits enforced, not a request SLA", time.Since(startMax))
	// Extra structure must be explicit/read-only, never omitted from claimed complete plain graph.
	phases, err := q.ListFocusedWaterPhases(t.Context(), dbgen.ListFocusedWaterPhasesParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO water_block_prescriptions(block_id,method) VALUES($1,'CONTINUOUS')`, phases[0].ID)
	got, err := q.GetFocusedWaterSession(t.Context(), dbgen.GetFocusedWaterSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: id})
	if err != nil || !got.HasExtraDetail {
		t.Fatal("structured source disguised as plain")
	}
}

func TestFocusedWaterScopeVersionAndReadLimits(t *testing.T) {
	w := newFocusedWaterFixture(t)
	s := w.prepare(t)
	q := dbgen.New(w.web)
	contextInput := dbgen.GetFocusedWaterCreateContextParams{ActorID: w.actor, PlanID: w.plan, Day: w.day}
	denied := func(actor, plan uuid.UUID) {
		t.Helper()
		_, err := q.GetFocusedWaterCreateContext(t.Context(), dbgen.GetFocusedWaterCreateContextParams{ActorID: actor, PlanID: plan, Day: w.day})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("scope leaked %v", err)
		}
	}
	denied(uuid.New(), w.plan)
	denied(w.actor, uuid.New())
	wrongDay := w.day
	wrongDay.Time = wrongDay.Time.AddDate(0, 0, 7)
	if _, err := q.GetFocusedWaterCreateContext(t.Context(), dbgen.GetFocusedWaterCreateContextParams{ActorID: w.actor, PlanID: w.plan, Day: wrongDay}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("outside-week task admitted")
	}
	// Actual role/grant predicates, with no cached IsAdmin input.
	backup := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic retained administrator',$2,'hash','1980-01-01')`, backup, backup.String()+"@example.test")
	recipientSchemaExec(t, w.web, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, backup)
	recipientSchemaExec(t, w.web, `DELETE FROM user_platform_roles WHERE user_id=$1`, w.actor)
	denied(w.actor, w.plan)
	recipientSchemaExec(t, w.web, `INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$1)`, w.actor, w.programme)
	if _, err := q.GetFocusedWaterCreateContext(t.Context(), contextInput); err != nil {
		t.Fatal(err)
	}
	team, teamGroup, teamPlan := uuid.New(), uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO teams(id,code,name,season_id,programme_id) SELECT $1,'SYN_WATER_TEAM','Synthetic water team',season_id,programme_id FROM training_plans WHERE id=$2`, team, w.plan)
	recipientSchemaExec(t, w.web, `INSERT INTO training_groups(id,name,team_id,created_by_id) VALUES($1,'Synthetic team group',$2,$3)`, teamGroup, team, w.actor)
	recipientSchemaExec(t, w.web, `INSERT INTO training_plans(id,title,team_id,training_group_id,season_id,week_start,created_by_id) SELECT $1,'Synthetic team week',$2,$3,season_id,week_start,$4 FROM training_plans WHERE id=$5`, teamPlan, team, teamGroup, w.actor, w.plan)
	denied(w.actor, teamPlan)
	recipientSchemaExec(t, w.web, `INSERT INTO staff_grants(user_id,capability,team_id,granted_by_id) VALUES($1,'COACH',$2,$1)`, w.actor, team)
	if _, err := q.GetFocusedWaterCreateContext(t.Context(), dbgen.GetFocusedWaterCreateContextParams{ActorID: w.actor, PlanID: teamPlan, Day: w.day}); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, w.web, `UPDATE staff_grants SET revoked_at=clock_timestamp(),revoked_by_id=$1 WHERE user_id=$1 AND team_id=$2`, w.actor, team)
	denied(w.actor, teamPlan)
	input := w.sessionInput(t, uuid.New())
	stale := input
	stale.ExpectedPlanUpdatedAt.Time = stale.ExpectedPlanUpdatedAt.Time.Add(-time.Second)
	if _, err := q.InsertFocusedWaterSession(t.Context(), stale); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("stale plan accepted")
	}
	recipientSchemaExec(t, w.web, `SELECT public.training_schedule_admit_partial($1,$2)`, s.DatabaseIncarnation, s.StateEpoch)
	if _, err := q.InsertFocusedWaterSession(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	segment := uuid.New()
	if _, err := q.InsertFocusedWaterSegment(t.Context(), dbgen.InsertFocusedWaterSegmentParams{ActorID: w.actor, PlanID: w.plan, SessionID: input.SessionID, SegmentID: segment}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.InsertFocusedWaterPhase(t.Context(), dbgen.InsertFocusedWaterPhaseParams{ActorID: w.actor, PlanID: w.plan, SessionID: input.SessionID, SegmentID: segment, PhaseID: uuid.New(), Position: 33, Purpose: dbgen.TrainingBlockPurposeMAIN, Instructions: "Not admitted"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("over-limit phase inserted")
	}
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(segment_id,position,purpose,instructions) SELECT $1,n,'MAIN','Synthetic existing advanced phase' FROM generate_series(1,34) n`, segment)
	phases, err := q.ListFocusedWaterPhases(t.Context(), dbgen.ListFocusedWaterPhasesParams{ActorID: w.actor, PlanID: w.plan, SessionID: input.SessionID})
	if err != nil || len(phases) != 33 {
		t.Fatalf("phase count+1 sentinel lost %d %v", len(phases), err)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_segments(session_id,position,modality) SELECT $1,n,'WATER' FROM generate_series(2,10) n`, input.SessionID)
	segments, err := q.ListFocusedWaterSegments(t.Context(), dbgen.ListFocusedWaterSegmentsParams{ActorID: w.actor, PlanID: w.plan, SessionID: input.SessionID})
	if err != nil || len(segments) != 9 {
		t.Fatalf("segment count+1 sentinel lost %d %v", len(segments), err)
	}
	recipientSchemaExec(t, w.web, `UPDATE staff_grants SET revoked_at=clock_timestamp(),revoked_by_id=$1 WHERE user_id=$1 AND programme_id=$2`, w.actor, w.programme)
	denied(w.actor, w.plan)
	if _, err = q.GetFocusedWaterSession(t.Context(), dbgen.GetFocusedWaterSessionParams{ActorID: w.actor, PlanID: w.plan, SessionID: input.SessionID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("revoked source readable")
	}
	phases, err = q.ListFocusedWaterPhases(t.Context(), dbgen.ListFocusedWaterPhasesParams{ActorID: w.actor, PlanID: w.plan, SessionID: input.SessionID})
	if err != nil || len(phases) != 0 {
		t.Fatal("revoked phase content readable")
	}
	if _, err = q.FocusedWaterArtifactSize(t.Context(), dbgen.FocusedWaterArtifactSizeParams{ActorID: w.actor, PlanID: w.plan, Snapshot: []byte(`{"private":"text"}`)}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("revoked preview read allowed")
	}
}

func TestFocusedWaterConcurrencyAndGateClose(t *testing.T) {
	w := newFocusedWaterFixture(t)
	s := w.prepare(t)
	input := w.sessionInput(t, uuid.New())
	a, err := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Rollback(t.Context())
	recipientSchemaExec(t, a, `SELECT public.training_schedule_admit_partial($1,$2)`, s.DatabaseIncarnation, s.StateEpoch)
	aq := dbgen.New(a)
	if _, err = aq.LockFocusedWaterCreatePlan(t.Context(), dbgen.LockFocusedWaterCreatePlanParams{ActorID: w.actor, PlanID: w.plan}); err != nil {
		t.Fatal(err)
	}
	other := w.f.web(t)
	b, err := other.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Rollback(t.Context())
	recipientSchemaExec(t, b, `SELECT public.training_schedule_admit_partial($1,$2)`, s.DatabaseIncarnation, s.StateEpoch)
	bq := dbgen.New(b)
	if _, err = bq.GetFocusedWaterCreateContext(t.Context(), dbgen.GetFocusedWaterCreateContextParams{ActorID: w.actor, PlanID: w.plan, Day: w.day}); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() {
		_, err := bq.LockFocusedWaterCreatePlan(t.Context(), dbgen.LockFocusedWaterCreatePlanParams{ActorID: w.actor, PlanID: w.plan})
		waited <- err
	}()
	focusedWaterWaitRowLock(t, w.f.root, other.PgConn().PID())
	if _, err = aq.InsertFocusedWaterSession(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if err = a.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	err = <-waited
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "40001" {
		t.Fatalf("waited stale plan was silently accepted %v", err)
	}
	_ = b.Rollback(t.Context())
	// Supported close is state-only and cannot pass a live admitted save.
	tx, err := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	recipientSchemaExec(t, tx, `SELECT public.training_schedule_admit_partial($1,$2)`, s.DatabaseIncarnation, s.StateEpoch)
	operator := w.f.connectLow(t)
	closed := make(chan error, 1)
	go func() {
		var epoch int64
		err := operator.QueryRow(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,false)`, s.DatabaseIncarnation, s.StateEpoch).Scan(&epoch)
		closed <- err
	}()
	focusedWaterWaitRowLock(t, w.f.root, operator.PgConn().PID())
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-closed; err != nil {
		t.Fatal(err)
	}
	if _, err = w.web.Exec(t.Context(), `SELECT public.training_schedule_admit_partial($1,$2)`, s.DatabaseIncarnation, s.StateEpoch); err == nil {
		t.Fatal("closed gate admitted old preview")
	}
	next, err := TrainingScheduleStatus(t.Context(), w.web)
	if err != nil {
		t.Fatal(err)
	}
	var epoch int64
	if err = operator.QueryRow(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,true)`, next.DatabaseIncarnation, next.StateEpoch).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	if _, err = w.web.Exec(t.Context(), `SELECT public.training_schedule_admit_partial($1,$2)`, s.DatabaseIncarnation, s.StateEpoch); err == nil {
		t.Fatal("gate ABA accepted stale preview")
	}
}

func TestFocusedWaterDefinitionAndDraftPreserveHistory(t *testing.T) {
	f := newRecipientSchemaFixture(t)
	f.install79(t)
	h := recipientHistoryFixture(t, f)
	if err := ApplyBaselineAndHarden(t.Context(), f.low, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	if recipientHistoryBytes(t, f.low, h) != h.before {
		t.Fatal("DDL changed session/plan/prescription/outcome records")
	}
	w := focusedWaterFixture{f: f, web: f.web(t), actor: h.actor, plan: h.plan, group: h.group}
	if err := w.web.QueryRow(t.Context(), `SELECT week_start FROM training_plans WHERE id=$1`, w.plan).Scan(&w.day); err != nil {
		t.Fatal(err)
	}
	w.prepare(t)
	preserved := func() string {
		t.Helper()
		var raw string
		if err := f.low.QueryRow(t.Context(), `SELECT jsonb_build_array((SELECT to_jsonb(x) FROM training_plan_publications x WHERE id=$1),(SELECT to_jsonb(x) FROM training_prescriptions x WHERE id=$2),(SELECT to_jsonb(x) FROM training_session_outcomes x WHERE prescription_id=$2))::text`, h.publication, h.prescription).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := preserved()
	input := w.sessionInput(t, uuid.New())
	q := dbgen.New(w.web)
	if _, err := q.InsertFocusedWaterSession(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if preserved() != before {
		t.Fatal("draft create rewrote immutable old prescription/publication/outcome")
	}
	if _, err := q.TrainingScheduleGuardPlanV1(t.Context(), dbgen.TrainingScheduleGuardPlanV1Params{ActorID: h.actor, PlanID: h.plan}); err == nil {
		t.Fatal("C publication guard accepted partial whole source")
	}
	var count int
	if err := w.web.QueryRow(t.Context(), `SELECT count(*) FROM training_plan_publications WHERE plan_id=$1`, h.plan).Scan(&count); err != nil || count != 1 {
		t.Fatal("draft create published unexpectedly")
	}
}

func focusedWaterWaitRowLock(t *testing.T, observer *pgx.Conn, pid uint32) {
	t.Helper()
	recipientWait(t, func() bool {
		var blocked bool
		err := observer.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE pid=$1 AND NOT granted AND locktype IN ('transactionid','tuple'))`, pid).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		return blocked
	})
}

func TestFocusedWaterFreshInitializationAndKnownParentFacts(t *testing.T) {
	t.Run("fresh original initializes actual closed P", func(t *testing.T) {
		recipientStampedFixture(t)
		f := newRecipientSchemaFixture(t, true)
		if err := FreshInitializeDatabase(t.Context(), f.root, "mycfc", f.roles); err != nil {
			t.Fatal(err)
		}
		low := f.connectLow(t)
		facts, err := InspectTrainingRecipientDatabase(t.Context(), low, f.inspectionInput(RecipientInspectFinal))
		if err != nil || facts.Phase != RecipientFinal || facts.ScheduleState == nil || facts.ScheduleState.SchemaContract != TrainingScheduleContractP || facts.ScheduleState.PartialV2Enabled || facts.ScheduleState.RequiredReader != 1 {
			t.Fatalf("fresh P facts %+v %v", facts, err)
		}
	})
	t.Run("known80 facts remain C with original CHECK2", func(t *testing.T) {
		f := newRecipientSchemaFixture(t)
		parent := strings.Split(baselineSchema, "\n-- Baseline through 202611100003_training_partial_authoring.")[0]
		if _, err := f.low.PgConn().Exec(t.Context(), parent).ReadAll(); err != nil {
			t.Fatal(err)
		}
		recipientSchemaExec(t, f.low, `CREATE TABLE mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`)
		versions, _ := trainingRecipientInventory(releasecontract.NormalAuthoringParentDigest)
		for _, v := range versions {
			recipientSchemaExec(t, f.low, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES($1)`, v)
		}
		if err := HardenPrivacyExecutionRoles(t.Context(), f.root, "mycfc", f.roles); err != nil {
			t.Fatal(err)
		}
		facts, err := InspectTrainingRecipientDatabase(t.Context(), f.low, f.inspectionInput(RecipientInspectParent))
		if err != nil || facts.Phase != RecipientReviewed80 || facts.ScheduleState == nil || facts.ScheduleState.SchemaContract != TrainingScheduleContractC {
			t.Fatalf("unsupported80 facts %+v %v", facts, err)
		}
		// Malformed enabled metadata is not repaired/disabled into an admissible parent.
		tx, err := f.low.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(t.Context())
		recipientSchemaExec(t, tx, `UPDATE training_schedule_control.state SET schema_contract='schedule-p1',required_reader=3,floor_commit_id=$1,floor_manifest_sha256=$2,partial_v2_enabled=true,ever_partial_v2_enabled=true`, uuid.New(), strings.Repeat("a", 64))
		if _, err = InspectTrainingRecipientParent(t.Context(), tx, releasecontract.NormalAuthoringParentDigest); err == nil {
			t.Fatal("known80 accepted malformed enabled P metadata as closed C parent")
		}
		ddl, err := migrationFiles.ReadFile("migrations/202611100003_training_partial_authoring.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Conn().PgConn().Exec(t.Context(), string(ddl)).ReadAll(); err == nil {
			t.Fatal("enabled parent was silently disabled/adopted")
		}
	})
}
