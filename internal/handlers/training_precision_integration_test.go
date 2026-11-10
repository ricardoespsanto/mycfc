//go:build integration

package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	mycfcdb "github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func precisionHandlerFixture(t *testing.T) (context.Context, *pgx.Conn, *pgx.Conn, *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	name := "mycfc-domain-c-" + uuid.NewString()[:8]
	password := strings.ReplaceAll(uuid.NewString(), "-", "")
	if e := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-P", "-e", "POSTGRES_DB=mycfc", "-e", "POSTGRES_USER=acceptance", "-e", "POSTGRES_PASSWORD="+password, "postgres:16.9").Run(); e != nil {
		t.Fatal("own precision fixture start failed")
	}
	t.Cleanup(func() {
		if e := exec.Command("docker", "stop", name).Run(); e != nil {
			t.Error("own precision fixture cleanup failed")
		}
	})
	output, e := exec.Command("docker", "port", name, "5432/tcp").Output()
	if e != nil {
		t.Fatal(e)
	}
	line := strings.SplitN(strings.TrimSpace(string(output)), "\n", 2)[0]
	port := line[strings.LastIndex(line, ":")+1:]
	raw := fmt.Sprintf("postgres://acceptance:%s@127.0.0.1:%s/mycfc?sslmode=disable", password, port)
	var root *pgx.Conn
	for range 150 {
		root, e = pgx.Connect(ctx, raw)
		if e == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if e != nil {
		t.Fatal("own precision fixture readiness failed")
	}
	t.Cleanup(func() { _ = root.Close(context.Background()) })
	roles := mycfcdb.RoleCredentials{AppUsername: "domain_web", AppPassword: "synthetic-web", MigrationUsername: "domain_migration", MigrationPassword: "synthetic-migration", MediaCleanupUsername: "domain_media", MediaCleanupPassword: "synthetic-media", DataRetentionUsername: "domain_retention", DataRetentionPassword: "synthetic-retention"}
	if e = mycfcdb.BootstrapRoles(ctx, root, "mycfc", roles); e != nil {
		t.Fatal(e)
	}
	cfg := root.Config().Copy()
	cfg.User, cfg.Password = roles.MigrationUsername, roles.MigrationPassword
	migration, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = migration.Close(context.Background()) })
	if e = mycfcdb.ApplyBaselineAndHarden(ctx, migration, "mycfc", roles); e != nil {
		t.Fatal(e)
	}
	webConfig, parseErr := pgxpool.ParseConfig(raw)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	webConfig.ConnConfig.User, webConfig.ConnConfig.Password = roles.AppUsername, roles.AppPassword
	web, e := pgxpool.NewWithConfig(ctx, webConfig)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(web.Close)
	if _, e = mycfcdb.VerifyTrainingScheduleRuntime(ctx, web, mycfcdb.TrainingScheduleCapabilities{ReaderVersion: 2, WriterVersion: 1}); e != nil {
		t.Fatal(e)
	}
	return ctx, root, migration, web
}

func TestPrecisionCompatibilityAdaptersRejectWholeSourcesAtomicallyAndKeepExactTargetIndependent(t *testing.T) {
	ctx, root, migration, web := precisionHandlerFixture(t)
	actor, coach, group, plan, session, partial := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	execute := func(sql string, args ...any) {
		t.Helper()
		if _, e := root.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range []uuid.UUID{actor, coach} {
		execute(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic compatibility actor',$2,'hash','1980-01-01')`, id, id.String()+"@example.test")
	}
	execute(`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor)
	var programme, season uuid.UUID
	if e := root.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); e != nil {
		t.Fatal(e)
	}
	if e := root.QueryRow(ctx, `INSERT INTO seasons(code,name,starts_on,ends_on) VALUES('DOMAIN_C','Synthetic',CURRENT_DATE-30,CURRENT_DATE+400) RETURNING id`).Scan(&season); e != nil {
		t.Fatal(e)
	}
	execute(`INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, coach, programme, actor)
	execute(`INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Synthetic group',$2,$3)`, group, programme, actor)
	execute(`INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) VALUES($1,'Synthetic week',$2,$3,$4,date_trunc('week',CURRENT_DATE)::date+7,$5)`, plan, programme, group, season, actor)
	execute(`INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) SELECT $1,id,'Exact interval',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours',$3 FROM training_plans WHERE id=$2`, session, plan, actor)

	userCtx := context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: coach})
	store := PostgresStructuredTrainingStore{Pool: web}
	var originalStart, originalEnd time.Time
	if e := root.QueryRow(ctx, `SELECT starts_at,ends_at FROM training_sessions WHERE id=$1`, session).Scan(&originalStart, &originalEnd); e != nil {
		t.Fatal(e)
	}
	proposedStart := originalStart.Add(2 * time.Hour)
	copiedID, copyErr := store.CopyTrainingSession(userCtx, session, plan, pgtype.Timestamptz{Time: proposedStart, Valid: true}, coach)
	if copyErr != nil || copiedID == uuid.Nil {
		t.Fatalf("ordinary exact copy refused: %v", copyErr)
	}
	var copiedStart, copiedEnd time.Time
	if e := root.QueryRow(ctx, `SELECT starts_at,ends_at FROM training_sessions WHERE id=$1`, copiedID).Scan(&copiedStart, &copiedEnd); e != nil {
		t.Fatal(e)
	}
	if !copiedStart.Equal(proposedStart) || copiedEnd.Sub(copiedStart) != originalEnd.Sub(originalStart) {
		t.Fatal("ordinary exact copy changed known duration/start")
	}
	// Explicit test-only floor/activation is confined to this disposable database.
	var e error
	var incarnation uuid.UUID
	var epoch int64
	if e = migration.QueryRow(ctx, `SELECT database_incarnation,state_epoch FROM training_schedule_control.state`).Scan(&incarnation, &epoch); e != nil {
		t.Fatal(e)
	}
	if _, e = migration.Exec(ctx, `SELECT training_schedule_control.commit_floor($1,$2,$3,$4,5)`, incarnation, epoch, uuid.New(), strings.Repeat("a", 64)); e != nil {
		t.Fatal(e)
	}
	if e = migration.QueryRow(ctx, `SELECT state_epoch FROM training_schedule_control.state`).Scan(&epoch); e != nil {
		t.Fatal(e)
	}
	if _, e = migration.Exec(ctx, `SELECT training_schedule_control.set_enabled($1,$2,true)`, incarnation, epoch); e != nil {
		t.Fatal(e)
	}
	execute(`INSERT INTO training_sessions(id,plan_id,title,entry_kind,schedule_precision,scheduled_on,starts_at,ends_at,created_by_id) SELECT $1,id,'Untimed rest','REST','DAY',week_start,NULL,NULL,$3 FROM training_plans WHERE id=$2`, partial, plan, actor)

	segment, e := store.CreateSegment(userCtx, StructuredTrainingSegmentInput{Segment: dbgen.CreateTrainingSessionSegmentParams{SessionID: session, Modality: dbgen.TrainingSegmentModalityGYM}, Block: dbgen.CreateTrainingSegmentBlockParams{Purpose: dbgen.TrainingBlockPurposeMAIN, Instructions: "Synthetic instructions"}})
	if e != nil || segment == uuid.Nil {
		t.Fatalf("exact target in mixed plan blocked: %v", e)
	}
	if _, e = store.CreateSegment(userCtx, StructuredTrainingSegmentInput{Segment: dbgen.CreateTrainingSessionSegmentParams{SessionID: partial, Modality: dbgen.TrainingSegmentModalityGYM}}); !errors.Is(e, ErrTrainingScheduleWriteUnsupported) {
		t.Fatalf("partial target error %v", e)
	}
	before := func() (plans, sessions, copies, pubs int) {
		t.Helper()
		for i, q := range []string{`SELECT count(*) FROM training_plans`, `SELECT count(*) FROM training_sessions`, `SELECT count(*) FROM training_copy_events`, `SELECT count(*) FROM training_plan_publications`} {
			target := []*int{&plans, &sessions, &copies, &pubs}[i]
			if e := root.QueryRow(ctx, q).Scan(target); e != nil {
				t.Fatal(e)
			}
		}
		return
	}
	a, b, c, d := before()
	var start time.Time
	if e = root.QueryRow(ctx, `SELECT week_start FROM training_plans WHERE id=$1`, plan).Scan(&start); e != nil {
		t.Fatal(e)
	}
	if _, e = store.CopyStructuredTrainingWeek(userCtx, StructuredWeekCopyInput{SourcePlanID: plan, WeekStart: start.AddDate(0, 0, 7), Title: "Attempt", ActorID: coach}); !errors.Is(e, ErrTrainingScheduleWriteUnsupported) {
		t.Fatalf("whole copy error %v", e)
	}
	if _, e = confirmPublicationTestReview(userCtx, store, publicationTestReview{PlanID: plan, PublishedByID: coach, SourceUpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}, ChangeSummary: "Unsupported mixed plan"}); !errors.Is(e, errPublicationFirstEmpty) {
		t.Fatalf("native mixed publication without recipients error %v", e)
	}
	aa, bb, cc, dd := before()
	if a != aa || b != bb || c != cc || d != dd {
		t.Fatalf("unsupported operation persisted rows: %d/%d %d/%d %d/%d %d/%d", a, aa, b, bb, c, cc, d, dd)
	}

	// Actual outcome writes reference a frozen v2 START_ONLY prescription without
	// filling the unknown planned end or creating a replacement schedule artifact.
	athlete, knownStart, publication, prescription := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	execute(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic athlete',$2,'hash','1980-01-01')`, athlete, athlete.String()+"@example.test")
	var membership uuid.UUID
	if e = root.QueryRow(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,CURRENT_DATE-10,CURRENT_DATE+300) RETURNING id`, athlete, season, programme).Scan(&membership); e != nil {
		t.Fatal(e)
	}
	execute(`INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, group, membership, actor)
	placementFixture := &publicationDomainFixture{root: root, low: migration, web: web, actor: actor, programme: programme, season: season}
	placement := currentTrainingPlacement(t, placementFixture, actor, athlete, programme, season, group, time.Now().UTC())
	execute(`INSERT INTO training_sessions(id,plan_id,title,entry_kind,schedule_precision,scheduled_on,starts_at,ends_at,created_by_id) SELECT $1,id,'Known start only','TRAINING','START_ONLY',week_start,(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours',NULL,$3 FROM training_plans WHERE id=$2`, knownStart, plan, actor)
	execute(`INSERT INTO training_plan_publications(id,plan_id,revision,source_updated_at,change_summary,published_by_id) SELECT $1,id,1,updated_at,'Test-only P fixture',$3 FROM training_plans WHERE id=$2`, publication, plan, actor)
	var snapshot []byte
	if e = root.QueryRow(ctx, `SELECT jsonb_build_object('schema_version',2,'plan_id',$2::text,'session',jsonb_build_object('ID',id::text,'EntryKind','TRAINING','schedule',training_session_snapshot_v2(id)->'schedule')) FROM training_sessions WHERE id=$1`, knownStart, plan).Scan(&snapshot); e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(snapshot)
	execute(`INSERT INTO training_prescriptions(id,publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256,placement_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, prescription, publication, knownStart, membership, athlete, snapshot, hex.EncodeToString(hash[:]), placement)
	actualStore := PostgresTrainingScheduleStore{Querier: dbgen.New(web), Pool: web}
	duration := int32(42)
	n, e := actualStore.SaveTrainingSessionOutcome(ctx, dbgen.SaveTrainingSessionOutcomeParams{SessionID: knownStart, UserID: athlete, Status: dbgen.TrainingOutcomeStatusCOMPLETED, ActualDurationMinutes: &duration})
	if e != nil || n != 1 {
		t.Fatalf("known-start v2 actual result %d %v", n, e)
	}
	duration = 45
	n, e = actualStore.UpdateOwnCompletedSessionFeedback(ctx, dbgen.UpdateOwnCompletedSessionFeedbackParams{SessionID: knownStart, UserID: athlete, ExpectedVersion: 1, ActualDurationMinutes: &duration})
	if e != nil || n != 1 {
		t.Fatalf("known-start v2 feedback %d %v", n, e)
	}
	var plannedEnd pgtype.Timestamptz
	var actual int32
	var frozenHash string
	var linked uuid.UUID
	if e = root.QueryRow(ctx, `SELECT session.ends_at,outcome.actual_duration_minutes,outcome.prescription_id,prescription.snapshot_sha256 FROM training_sessions session JOIN training_session_outcomes outcome ON outcome.session_id=session.id JOIN training_prescriptions prescription ON prescription.id=outcome.prescription_id WHERE session.id=$1`, knownStart).Scan(&plannedEnd, &actual, &linked, &frozenHash); e != nil {
		t.Fatal(e)
	}
	if plannedEnd.Valid || actual != 45 || linked != prescription || frozenHash != hex.EncodeToString(hash[:]) {
		t.Fatal("actual metrics changed planned schedule or frozen v2 history")
	}
	n, e = actualStore.SaveTrainingSessionOutcome(ctx, dbgen.SaveTrainingSessionOutcomeParams{SessionID: partial, UserID: athlete, Status: dbgen.TrainingOutcomeStatusCOMPLETED})
	if e != nil || n != 0 {
		t.Fatalf("DAY lifecycle unexpectedly supported %d %v", n, e)
	}

	execute(`INSERT INTO training_session_outcomes(session_id,user_id,status,actual_duration_minutes) VALUES($1,$2,'COMPLETED',12)`, partial, athlete)
	n, e = actualStore.UpdateOwnCompletedSessionFeedback(ctx, dbgen.UpdateOwnCompletedSessionFeedbackParams{SessionID: partial, UserID: athlete, ExpectedVersion: 1, ActualDurationMinutes: &duration})
	if e != nil || n != 0 {
		t.Fatalf("DAY correction unexpectedly supported %d %v", n, e)
	}
	// Fresh grant revocation cannot leave stale context authority effective.
	execute(`UPDATE staff_grants SET revoked_at=clock_timestamp(),revoked_by_id=$2,revoke_reason='Synthetic revoke' WHERE user_id=$1 AND revoked_at IS NULL`, coach, actor)
	if _, e = store.CreateTrainingSegmentBlock(userCtx, dbgen.CreateTrainingSegmentBlockParams{SegmentID: segment, Purpose: dbgen.TrainingBlockPurposeMAIN, Instructions: "Denied scoped write"}); !errors.Is(e, errTrainingScheduleUnavailable) {
		t.Fatalf("revoked error %v", e)
	}
}
