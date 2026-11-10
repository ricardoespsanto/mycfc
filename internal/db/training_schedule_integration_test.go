//go:build integration

package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func isolatedTrainingScheduleURL(t *testing.T) string {
	t.Helper()
	if raw := os.Getenv("TEST_SCHEDULE_DATABASE_URL"); raw != "" {
		return raw
	}
	name := "mycfc-schedule-ci-" + uuid.NewString()[:8]
	password := strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-P", "-e", "POSTGRES_DB=mycfc", "-e", "POSTGRES_USER=acceptance", "-e", "POSTGRES_PASSWORD="+password, "postgres:16.9").Run(); err != nil {
		t.Fatal("isolated precision PostgreSQL could not start")
	}
	t.Setenv("TEST_SCHEDULE_CONTAINER", name)
	t.Cleanup(func() {
		if err := exec.Command("docker", "stop", name).Run(); err != nil {
			t.Error("isolated precision PostgreSQL cleanup failed")
		}
	})
	out, err := exec.Command("docker", "port", name, "5432/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	port := line[strings.LastIndex(line, ":")+1:]
	raw := "postgres://acceptance:" + password + "@127.0.0.1:" + port + "/mycfc?sslmode=disable"
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		c, err := pgx.Connect(t.Context(), raw)
		if err == nil {
			_ = c.Close(t.Context())
			return raw
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("isolated precision PostgreSQL not ready")
	return ""
}

func scheduleTestConnection(t *testing.T, role, password string) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig(os.Getenv("TEST_SCHEDULE_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if role != "" {
		cfg.User, cfg.Password = role, password
	}
	conn, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func TestTrainingScheduleCompatibilityForwardAndGate(t *testing.T) {
	t.Setenv("TEST_SCHEDULE_DATABASE_URL", isolatedTrainingScheduleURL(t))
	ctx := t.Context()
	root := scheduleTestConnection(t, "", "")
	var database string
	if err := root.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	if database != "mycfc" {
		t.Fatal("precision fixture requires dedicated isolated mycfc database")
	}
	var empty bool
	if err := root.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','f'))`).Scan(&empty); err != nil || !empty {
		t.Fatal("precision test requires its own empty database")
	}
	roles := RoleCredentials{AppUsername: "schedule_web", AppPassword: "synthetic-web", MigrationUsername: "schedule_migration", MigrationPassword: "synthetic-migration", MediaCleanupUsername: "schedule_media", MediaCleanupPassword: "synthetic-media", DataRetentionUsername: "schedule_retention", DataRetentionPassword: "synthetic-retention"}
	if err := BootstrapRoles(ctx, root, database, roles); err != nil {
		t.Fatal(err)
	}
	low := scheduleTestConnection(t, roles.MigrationUsername, roles.MigrationPassword)
	parent := strings.Split(baselineSchema, "\n-- Baseline through 202611100001_training_schedule_compatibility.")[0]
	if _, err := low.PgConn().Exec(ctx, parent).ReadAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := low.Exec(ctx, `CREATE SCHEMA mycfc_meta;`); err == nil {
		t.Fatal("BootstrapRoles metadata schema unexpectedly missing")
	}
	if _, err := low.Exec(ctx, `CREATE TABLE mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	for _, version := range EmbeddedMigrationInventory() {
		if version != TrainingScheduleCompatibilityMigration {
			if _, err := low.Exec(ctx, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES($1)`, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := HardenPrivacyExecutionRoles(ctx, root, database, roles); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := root.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	actor, subject, season, group, plan, session := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{actor, subject} {
		exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic schedule subject',$2,'hash','1980-01-01')`, id, id.String()+"@example.test")
	}
	exec(`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor)
	exec(`INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,'SCH_C','Synthetic season',CURRENT_DATE-30,CURRENT_DATE+400)`, season)
	var programme uuid.UUID
	if err := root.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	var membership uuid.UUID
	if err := root.QueryRow(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,CURRENT_DATE-10,CURRENT_DATE+300) RETURNING id`, subject, season, programme).Scan(&membership); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Synthetic timing group',$2,$3)`, group, programme, actor)
	exec(`INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, group, membership, actor)
	exec(`INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) VALUES($1,'Synthetic week',$2,$3,$4,date_trunc('week',CURRENT_DATE)::date+7,$5)`, plan, programme, group, season, actor)
	exec(`INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) SELECT $1,id,'Synthetic exact session',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours',$3 FROM training_plans WHERE id=$2`, session, plan, actor)
	segment, block, exercise := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO training_session_segments(id,session_id,position,modality) VALUES($1,$2,1,'GYM')`, segment, session)
	exec(`INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$2,1,'MAIN','Synthetic instructions')`, block, segment)
	exec(`INSERT INTO gym_block_prescriptions(block_id,structure,objective) VALUES($1,'STRAIGHT_SETS','CORE')`, block)
	exec(`INSERT INTO gym_exercises(id,block_id,position,name,repetitions) VALUES($1,$2,1,'Synthetic exercise',10)`, exercise, block)
	var oldSession, oldPlan, oldSnapshot string
	if err := root.QueryRow(ctx, `SELECT jsonb_build_array(id,plan_id,title,starts_at,ends_at,status,created_at,updated_at)::text FROM training_sessions WHERE id=$1`, session).Scan(&oldSession); err != nil {
		t.Fatal(err)
	}
	if err := root.QueryRow(ctx, `SELECT row_to_json(p)::text FROM training_plans p WHERE id=$1`, plan).Scan(&oldPlan); err != nil {
		t.Fatal(err)
	}
	if err := root.QueryRow(ctx, `SELECT training_session_snapshot($1)::text`, session).Scan(&oldSnapshot); err != nil {
		t.Fatal(err)
	}
	var publication uuid.UUID
	if err := root.QueryRow(ctx, `INSERT INTO training_plan_publications(plan_id,revision,source_updated_at,change_summary,published_by_id) SELECT id,1,updated_at,'Synthetic exact publication',$2 FROM training_plans WHERE id=$1 RETURNING id`, plan, actor).Scan(&publication); err != nil {
		t.Fatal(err)
	}
	snapshot := []byte(`{"schema_version":1,"session":{"ID":"` + session.String() + `","EntryKind":"TRAINING","When":"legacy exact display"}}`)
	hash := sha256.Sum256(snapshot)
	exec(`INSERT INTO training_prescriptions(publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256) VALUES($1,$2,$3,$4,$5,$6)`, publication, session, membership, subject, snapshot, hex.EncodeToString(hash[:]))
	// Trusted fixture receipt models the exact protected original lineage; the
	// command integration suite separately builds/rehearses the signed genesis.
	exec(`CREATE SCHEMA mycfc_fresh_init; REVOKE ALL ON SCHEMA mycfc_fresh_init FROM PUBLIC; CREATE TABLE mycfc_fresh_init.receipt(database_name text PRIMARY KEY,version text NOT NULL,candidate text NOT NULL,schema_digest text NOT NULL); REVOKE ALL ON mycfc_fresh_init.receipt FROM PUBLIC; GRANT USAGE ON SCHEMA mycfc_fresh_init TO schedule_migration; GRANT SELECT ON mycfc_fresh_init.receipt TO schedule_migration`)
	exec(`INSERT INTO mycfc_fresh_init.receipt VALUES($1,$2,$3,$4)`, database, releasecontract.FreshGenesisVersion, releasecontract.FreshGenesisCandidate, releasecontract.FreshGenesisDigest)
	if err := ProvisionGuardianReleaseBindRole(ctx, root, database, guardianReleaseBindRole, "synthetic-bind"); err != nil {
		t.Fatal(err)
	}
	binder := scheduleTestConnection(t, guardianReleaseBindRole, "synthetic-bind")
	if _, err := binder.Exec(ctx, `SELECT * FROM guardian_ops.release_disable_and_bind($1,$2,$3)`, "sha256:"+strings.Repeat("1", 64), releasecontract.NormalParentDigest, database); err != nil {
		t.Fatal(err)
	}
	guardStates := func() string {
		t.Helper()
		var value string
		if err := root.QueryRow(ctx, `SELECT jsonb_build_object('binding',(SELECT to_jsonb(b) FROM guardian_ops.runtime_release_binding b WHERE singleton),'privacy',(SELECT to_jsonb(p) FROM privacy_request_activation p WHERE singleton),'kill',(SELECT to_jsonb(k) FROM privacy_worker_kill_switch k WHERE singleton))::text`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	oldGates := guardStates()
	previousVersion, previousCandidate := releasecontract.Version, releasecontract.Candidate
	releasecontract.Version, releasecontract.Candidate = "v99.0.0", strings.Repeat("c", 40)
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = previousVersion, previousCandidate })
	check := func(want bool) {
		t.Helper()
		ok, err := FreshInitializationMatchesWithInspector(ctx, root, low, roles.MigrationUsername, database, releasecontract.Version, releasecontract.Candidate)
		if want && (err != nil || !ok) || !want && err == nil && ok {
			t.Fatalf("normal lineage matched=%t want=%t err=%v", ok, want, err)
		}
	}
	check(true)
	attachment78, err := migrationFiles.ReadFile("migrations/202610010003_privacy_attachment_trigger_runtime.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Both explicitly reviewed old inventories require their actual old shape;
	// a marker plus a same-name CHECK(true) or altered privacy function is not
	// permission to silently repair/adopt an unreviewed parent.
	for _, genesis77 := range []bool{false, true} {
		if genesis77 {
			exec(`DELETE FROM mycfc_meta.schema_migrations WHERE version='202610010003_privacy_attachment_trigger_runtime'`)
			if _, err := low.Exec(ctx, freshGenesisAttachmentSQL); err != nil {
				t.Fatal(err)
			}
		}
		check(true)
		for _, change := range []struct{ mutate, undo string }{
			{`ALTER TABLE training_sessions DROP CONSTRAINT training_sessions_times_valid; ALTER TABLE training_sessions ADD CONSTRAINT training_sessions_times_valid CHECK(true)`, `ALTER TABLE training_sessions DROP CONSTRAINT training_sessions_times_valid; ALTER TABLE training_sessions ADD CONSTRAINT training_sessions_times_valid CHECK(starts_at<ends_at)`},
			{`ALTER TABLE training_sessions ALTER COLUMN ends_at DROP NOT NULL`, `ALTER TABLE training_sessions ALTER COLUMN ends_at SET NOT NULL`},
		} {
			exec(change.mutate)
			check(false)
			exec(change.undo)
		}
		exec(`CREATE OR REPLACE FUNCTION require_active_privacy_attachment_subject() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$`)
		check(false)
		if genesis77 {
			if _, err := low.Exec(ctx, freshGenesisAttachmentSQL); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := low.Exec(ctx, string(attachment78)); err != nil {
				t.Fatal(err)
			}
		}
		check(true)
		if genesis77 {
			if _, err := low.Exec(ctx, string(attachment78)); err != nil {
				t.Fatal(err)
			}
			exec(`INSERT INTO mycfc_meta.schema_migrations(version) VALUES('202610010003_privacy_attachment_trigger_runtime')`)
		}
	}
	if err := ApplyBaselineAndHarden(ctx, low, database, roles); err != nil {
		t.Fatal(err)
	}
	check(true)
	exec(`ALTER FUNCTION training_session_schedule_valid(text,date,timestamptz,timestamptz,text,training_entry_kind) STRICT`)
	check(false)
	exec(`ALTER FUNCTION training_session_schedule_valid(text,date,timestamptz,timestamptz,text,training_entry_kind) CALLED ON NULL INPUT`)
	exec(`ALTER TABLE training_sessions ENABLE REPLICA TRIGGER training_sessions_schedule_guard`)
	check(false)
	exec(`ALTER TABLE training_sessions ENABLE TRIGGER training_sessions_schedule_guard`)
	exec(`ALTER TABLE training_sessions DROP CONSTRAINT training_sessions_schedule_shape_valid; ALTER TABLE training_sessions ADD CONSTRAINT training_sessions_schedule_shape_valid CHECK(true)`)
	check(false)
	exec(`ALTER TABLE training_sessions DROP CONSTRAINT training_sessions_schedule_shape_valid; ALTER TABLE training_sessions ADD CONSTRAINT training_sessions_schedule_shape_valid CHECK(public.training_session_schedule_valid(schedule_precision,scheduled_on,starts_at,ends_at,day_band,entry_kind))`)
	check(true)
	if guardStates() != oldGates {
		t.Fatal("C migration changed guardian binding or retired privacy controls")
	}
	newImage := "sha256:" + strings.Repeat("2", 64)
	if err := BindGuardianRuntimeRelease(ctx, binder, database, newImage); err != nil {
		t.Fatalf("normal guardian bind rejected new final digest: %v", err)
	}
	if enabled, err := GuardianReleaseIntakeEnabled(ctx, binder, database, newImage); err != nil || enabled {
		t.Fatalf("normal guardian bind changed disable-only contract: %t %v", enabled, err)
	}
	var privacyRetired bool
	if err := root.QueryRow(ctx, `SELECT (SELECT engaged FROM privacy_worker_kill_switch WHERE singleton) AND NOT EXISTS(SELECT 1 FROM privacy_request_activation WHERE enabled OR fulfilment_ready)`).Scan(&privacyRetired); err != nil || !privacyRetired {
		t.Fatalf("privacy automation reactivated: %v", err)
	}
	for _, tc := range []struct{ sql, want string }{{`SELECT jsonb_build_array(id,plan_id,title,starts_at,ends_at,status,created_at,updated_at)::text FROM training_sessions WHERE id=$1`, oldSession}, {`SELECT row_to_json(p)::text FROM training_plans p WHERE id=$1`, oldPlan}, {`SELECT training_session_snapshot($1)::text`, oldSnapshot}} {
		var actual string
		id := session
		if strings.Contains(tc.sql, "training_plans p") {
			id = plan
		}
		if err := root.QueryRow(ctx, tc.sql, id).Scan(&actual); err != nil || actual != tc.want {
			t.Fatalf("C expansion changed existing values: %v", err)
		}
	}
	var savedSnapshot []byte
	var savedHash string
	if err := root.QueryRow(ctx, `SELECT snapshot::text,snapshot_sha256 FROM training_prescriptions WHERE publication_id=$1`, publication).Scan(&savedSnapshot, &savedHash); err != nil || savedHash != hex.EncodeToString(hash[:]) || !strings.Contains(string(savedSnapshot), "legacy exact display") {
		t.Fatalf("old prescription changed: %v", err)
	}
	web := scheduleTestConnection(t, roles.AppUsername, roles.AppPassword)
	state, err := VerifyTrainingScheduleRuntime(ctx, web, TrainingScheduleCapabilities{ReaderVersion: 2, WriterVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaContract != TrainingScheduleContractC || state.PartialV2Enabled || state.RequiredReader != 1 {
		t.Fatalf("unexpected C state: %+v", state)
	}
	for _, sql := range []string{`SELECT * FROM training_schedule_control.state`, `UPDATE training_schedule_control.state SET partial_v2_enabled=true`, `SELECT training_schedule_control.set_enabled(gen_random_uuid(),0,true)`, `SELECT training_schedule_control.commit_floor(gen_random_uuid(),0,gen_random_uuid(),repeat('a',64),2)`, `CREATE TABLE training_schedule_control.forbidden(id int)`} {
		if _, err := web.Exec(ctx, sql); err == nil {
			t.Fatalf("runtime gate privilege allowed: %s", sql)
		}
	}
	q := dbgen.New(web)
	if supported, err := q.TrainingScheduleGuardPlanV1(ctx, dbgen.TrainingScheduleGuardPlanV1Params{ActorID: actor, PlanID: plan}); err != nil || !supported {
		t.Fatalf("exact C operation refused: %t %v", supported, err)
	}
	if _, err := q.TrainingScheduleGuardPlanV1(ctx, dbgen.TrainingScheduleGuardPlanV1Params{ActorID: subject, PlanID: plan}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unscoped guard allowed: %v", err)
	}
	for _, target := range []struct {
		kind string
		id   uuid.UUID
	}{{"PLAN", plan}, {"SESSION", session}, {"SEGMENT", segment}, {"BLOCK", block}, {"EXERCISE", exercise}} {
		lockedPlan, err := q.TrainingScheduleLockTargetV1(ctx, dbgen.TrainingScheduleLockTargetV1Params{ActorID: actor, TargetKind: target.kind, TargetID: target.id})
		if err != nil || lockedPlan != plan {
			t.Fatalf("scoped %s ancestor lock: %v", target.kind, err)
		}
		if _, err := q.TrainingScheduleLockTargetV1(ctx, dbgen.TrainingScheduleLockTargetV1Params{ActorID: subject, TargetKind: target.kind, TargetID: target.id}); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("denied %s lock disclosed target: %v", target.kind, err)
		}
	}
	verifyPreCTrainingReader(t, roles, actor)
	// Pure shape function must always return a definite boolean, including SQL
	// NULL inputs; the C-only constraint still refuses every partial row.
	for _, tc := range []struct {
		precision, date, start, end, band, kind string
		want                                    bool
	}{
		{"INTERVAL", "NULL", "'2026-11-10 10:00Z'", "'2026-11-10 11:00Z'", "NULL", "'TRAINING'", true},
		{"START_ONLY", "'2026-11-10'", "'2026-11-10 10:00Z'", "NULL", "NULL", "'TRAINING'", true},
		{"DAY", "'2026-11-10'", "NULL", "NULL", "NULL", "'REST'", true},
		{"BAND", "'2026-11-10'", "NULL", "NULL", "'MORNING'", "'LOGISTICS'", true},
		{"BAND", "'2026-11-10'", "NULL", "NULL", "NULL", "'REST'", false},
		{"DAY", "'2026-11-10'", "NULL", "NULL", "NULL", "'TRAINING'", false},
		{"START_ONLY", "NULL", "'2026-11-10 10:00Z'", "NULL", "NULL", "'TRAINING'", false},
		{"START_ONLY", "'2026-11-11'", "'2026-11-10 10:00Z'", "NULL", "NULL", "'TRAINING'", false},
		{"unknown", "NULL", "NULL", "NULL", "NULL", "'REST'", false},
	} {
		var value bool
		sql := fmt.Sprintf(`SELECT public.training_session_schedule_valid('%s',%s::date,%s::timestamptz,%s::timestamptz,%s,%s::training_entry_kind)`, tc.precision, tc.date, tc.start, tc.end, tc.band, tc.kind)
		if err := web.QueryRow(ctx, sql).Scan(&value); err != nil || value != tc.want {
			t.Fatalf("shape %s got=%t want=%t err=%v", tc.precision, value, tc.want, err)
		}
	}
	if _, err := web.Exec(ctx, `INSERT INTO training_sessions(plan_id,title,starts_at,ends_at,entry_kind,schedule_precision,scheduled_on,day_band,created_by_id) SELECT id,'Forbidden partial',NULL,NULL,'REST','BAND',week_start,'MORNING',$2 FROM training_plans WHERE id=$1`, plan, actor); !errors.Is(TrainingScheduleWriteError(err), ErrTrainingScheduleWriteUnsupported) {
		t.Fatalf("closed partial write: %v", err)
	}
	if err := BootstrapRoles(ctx, root, database, roles); err != nil {
		t.Fatal(err)
	}
	if err := HardenPrivacyExecutionRoles(ctx, root, database, roles); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTrainingScheduleRuntime(ctx, web, TrainingScheduleCapabilities{ReaderVersion: 2, WriterVersion: 1}); err != nil {
		t.Fatalf("repeated hardening changed gate privileges: %v", err)
	}
	verifyFreshTrainingScheduleParity(t, root)
	// Resolve-before-lock must not silently follow a child that moves to a
	// different parent while the scoped plan lock is waiting.
	otherPlan, otherSession := uuid.New(), uuid.New()
	exec(`INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) SELECT $1,'Synthetic second week',programme_id,training_group_id,season_id,week_start+7,created_by_id FROM training_plans WHERE id=$2`, otherPlan, plan)
	exec(`INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) SELECT $1,id,'Synthetic second exact',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours',$3 FROM training_plans WHERE id=$2`, otherSession, otherPlan, actor)
	mover := scheduleTestConnection(t, roles.AppUsername, roles.AppPassword)
	moveTx, err := mover.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := moveTx.Exec(ctx, `SELECT id FROM training_plans WHERE id=$1 FOR UPDATE`, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := moveTx.Exec(ctx, `UPDATE training_session_segments SET session_id=$1 WHERE id=$2`, otherSession, segment); err != nil {
		t.Fatal(err)
	}
	targetReader := scheduleTestConnection(t, roles.AppUsername, roles.AppPassword)
	targetDone := make(chan error, 1)
	go func() {
		_, err := dbgen.New(targetReader).TrainingScheduleLockTargetV1(ctx, dbgen.TrainingScheduleLockTargetV1Params{ActorID: actor, TargetKind: "SEGMENT", TargetID: segment})
		targetDone <- err
	}()
	awaitScheduleLock(t, root, targetReader.PgConn().PID())
	if err := moveTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-targetDone:
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "40001" {
			t.Fatalf("moved target silently followed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("moved target reader did not finish")
	}
	// Unknown/partial ledger and weakened installed schema fail normal admission.
	exec(`INSERT INTO mycfc_meta.schema_migrations(version) VALUES('unreviewed-extra')`)
	check(false)
	exec(`DELETE FROM mycfc_meta.schema_migrations WHERE version='unreviewed-extra'`)
	exec(`ALTER TABLE training_sessions DROP CONSTRAINT training_sessions_schedule_compatibility_only`)
	check(false)
	exec(`ALTER TABLE training_sessions ADD CONSTRAINT training_sessions_schedule_compatibility_only CHECK(schedule_precision='INTERVAL')`)
	// Retired destructive reset pins deliberately do not advance with C.
	retired := roles
	retired.AppUsername = releasecontract.OldWebRole
	if err := BootstrapDisposableRelease(ctx, root, database, retired); err == nil || err.Error() != "disposable release reviewed schema changed" {
		t.Fatalf("retired reset pin changed: %v", err)
	}
	// A queued exclusive state operator must not block old exact/v1 paths via
	// a newly added late shared-state request.
	operator := scheduleTestConnection(t, roles.MigrationUsername, roles.MigrationPassword)
	locked, err := operator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := locked.Exec(ctx, `SELECT 1 FROM training_schedule_control.state WHERE singleton FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	fastCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	_, err = web.Exec(fastCtx, `UPDATE training_sessions SET title=title WHERE id=$1`, session)
	cancel()
	if err != nil {
		t.Fatalf("legacy exact writer acquired late state lock: %v", err)
	}
	if err := locked.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// Test-only P schema is deliberately not a pending embedded migration.
	fixture, err := os.ReadFile("testdata/training_schedule_p_contract.sql")
	if err != nil {
		t.Fatal(err)
	}
	cutover, err := low.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cutover.Exec(ctx, string(fixture)); err != nil {
		_ = cutover.Rollback(ctx)
		t.Fatal(err)
	}
	if err := cutover.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	state, err = VerifyTrainingScheduleRuntime(ctx, web, TrainingScheduleCapabilities{ReaderVersion: 2, WriterVersion: 1})
	if err != nil || state.PartialV2Enabled {
		t.Fatalf("C cannot read closed final contract: %v", err)
	}
	var epoch int64
	floor := uuid.New()
	if err := low.QueryRow(ctx, `SELECT training_schedule_control.commit_floor($1,$2,$3,repeat('a',64),2)`, state.DatabaseIncarnation, state.StateEpoch, floor).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	if err := low.QueryRow(ctx, `SELECT training_schedule_control.set_enabled($1,$2,true)`, state.DatabaseIncarnation, epoch).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	state.StateEpoch = epoch
	partialIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, precision := range []string{"START_ONLY", "DAY", "BAND"} {
		var start any
		var band any
		kind := "REST"
		if precision == "START_ONLY" {
			var instant time.Time
			if err := root.QueryRow(ctx, `SELECT (week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '14 hours' FROM training_plans WHERE id=$1`, plan).Scan(&instant); err != nil {
				t.Fatal(err)
			}
			start = instant
			kind = "TRAINING"
		}
		if precision == "BAND" {
			band = "MORNING"
		}
		if _, err := web.Exec(ctx, `INSERT INTO training_sessions(id,plan_id,title,entry_kind,schedule_precision,scheduled_on,starts_at,ends_at,day_band,created_by_id) SELECT $1,id,'Typed partial',$3,$4,week_start,$5,NULL,$6,$7 FROM training_plans WHERE id=$2`, partialIDs[i], plan, kind, precision, start, band, actor); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range partialIDs {
		affected, err := q.SaveTrainingSessionOutcome(ctx, dbgen.SaveTrainingSessionOutcomeParams{SessionID: id, UserID: subject, Status: dbgen.TrainingOutcomeStatusCOMPLETED, ExpectedVersion: 0})
		if err != nil || i == 0 && affected != 1 || i > 0 && affected != 0 {
			t.Fatalf("C outcome precision%d rows=%d err=%v", i, affected, err)
		}
		if i > 0 {
			exec(`INSERT INTO training_session_outcomes(session_id,user_id,status) VALUES($1,$2,'COMPLETED')`, id, subject)
		}
		value := int32(30)
		affected, err = q.UpdateOwnCompletedSessionFeedback(ctx, dbgen.UpdateOwnCompletedSessionFeedbackParams{SessionID: id, UserID: subject, ExpectedVersion: 1, ActualDurationMinutes: &value})
		if err != nil || i == 0 && affected != 1 || i > 0 && affected != 0 {
			t.Fatalf("C completed feedback precision%d rows=%d err=%v", i, affected, err)
		}
	}
	rows, err := q.ListStructuredSessionSnapshotsForPlan(ctx, plan)
	if err != nil || len(rows) != 4 {
		t.Fatalf("partial whole-plan omission: rows=%d err=%v", len(rows), err)
	}
	// An independent exact draft target remains editable in a mixed week; it
	// does not consume or rewrite its partial/v2 siblings or immutable history.
	if p, err := q.TrainingScheduleLockTargetV1(ctx, dbgen.TrainingScheduleLockTargetV1Params{ActorID: actor, TargetKind: "SESSION", TargetID: session}); err != nil || p != plan {
		t.Fatalf("exact target in mixed plan refused: %v", err)
	}
	if _, err := q.TrainingScheduleLockTargetV1(ctx, dbgen.TrainingScheduleLockTargetV1Params{ActorID: actor, TargetKind: "SESSION", TargetID: partialIDs[0]}); !errors.Is(TrainingScheduleWriteError(err), ErrTrainingScheduleWriteUnsupported) {
		t.Fatalf("partial target admitted: %v", err)
	}
	for _, r := range rows {
		if !r.SessionOn.Valid || len(r.ScheduleSnapshot) == 0 {
			t.Fatal("typed read projection missing")
		}
		if r.SchedulePrecision != "INTERVAL" && len(r.Snapshot) != 0 {
			t.Fatal("partial converted into legacy snapshot")
		}
	}
	for _, call := range []func() error{
		func() error {
			_, err := q.TrainingScheduleGuardPlanV1(ctx, dbgen.TrainingScheduleGuardPlanV1Params{ActorID: actor, PlanID: plan})
			return err
		},
		func() error {
			_, err := q.TrainingScheduleGuardSessionV1(ctx, dbgen.TrainingScheduleGuardSessionV1Params{ActorID: actor, SessionID: partialIDs[0]})
			return err
		},
	} {
		if err := TrainingScheduleWriteError(call()); !errors.Is(err, ErrTrainingScheduleWriteUnsupported) {
			t.Fatalf("C mixed-source guard: %v", err)
		}
	}
	if _, err := q.TrainingScheduleGuardPlanV1(ctx, dbgen.TrainingScheduleGuardPlanV1Params{ActorID: subject, PlanID: plan}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unsupported guard disclosed denied plan: %v", err)
	}
	var routine uuid.UUID
	if err := web.QueryRow(ctx, `INSERT INTO training_routines(name,kind,owner_user_id,source_id,source_updated_at,snapshot) SELECT 'Typed routine','SESSION',$2,id,updated_at,training_session_snapshot_v2(id) FROM training_sessions WHERE id=$1 RETURNING id`, partialIDs[0], actor).Scan(&routine); err != nil {
		t.Fatal(err)
	}
	if _, err := q.TrainingScheduleGuardRoutineV1(ctx, dbgen.TrainingScheduleGuardRoutineV1Params{ActorID: actor, RoutineID: routine}); !errors.Is(TrainingScheduleWriteError(err), ErrTrainingScheduleWriteUnsupported) {
		t.Fatalf("v2 routine guard: %v", err)
	}
	var v2 []byte
	if err := web.QueryRow(ctx, `SELECT snapshot FROM training_routines WHERE id=$1`, routine).Scan(&v2); err != nil {
		t.Fatal(err)
	}
	if _, err := web.Exec(ctx, `SELECT public.restore_training_session($1,$2,clock_timestamp(),$3)`, v2, plan, actor); !errors.Is(TrainingScheduleWriteError(err), ErrTrainingScheduleWriteUnsupported) {
		t.Fatalf("v2 restored through old duration-only helper: %v", err)
	}
	// Prove restrictive classification history still sees a null-start session.
	secondSubject := uuid.New()
	exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic partial recipient',$2,'hash','1980-01-01')`, secondSubject, secondSubject.String()+"@example.test")
	var secondMembership uuid.UUID
	if err := root.QueryRow(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,CURRENT_DATE-10,CURRENT_DATE+300) RETURNING id`, secondSubject, season, programme).Scan(&secondMembership); err != nil {
		t.Fatal(err)
	}
	var secondPublication uuid.UUID
	if err := web.QueryRow(ctx, `INSERT INTO training_plan_publications(plan_id,revision,source_updated_at,change_summary,published_by_id) SELECT id,2,updated_at,'Typed partial publication',$2 FROM training_plans WHERE id=$1 RETURNING id`, plan, actor).Scan(&secondPublication); err != nil {
		t.Fatal(err)
	}
	if _, err := web.Exec(ctx, `INSERT INTO training_prescriptions(publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256) SELECT $1,$2,$3,$4,jsonb_build_object('schema_version',2,'session',training_session_snapshot_v2($2)),repeat('b',64)`, secondPublication, partialIDs[1], secondMembership, secondSubject); err != nil {
		t.Fatal(err)
	}
	_, err = root.Exec(ctx, `UPDATE user_memberships SET ends_on=(SELECT week_start-1 FROM training_plans WHERE id=$2) WHERE id=$1`, secondMembership, plan)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "user_memberships_recorded_history_end" {
		t.Fatalf("null-start history guard bypass: %v", err)
	}
	// Closure drains admitted writers and the supported first-state order avoids
	// the three-way late-admission waiter cycle from the agreed lock matrix.
	oldWriter := scheduleTestConnection(t, roles.AppUsername, roles.AppPassword)
	oldTx, err := oldWriter.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldTx.Exec(ctx, `SELECT id FROM training_plans WHERE id=$1 FOR UPDATE`, plan); err != nil {
		t.Fatal(err)
	}
	writer := scheduleTestConnection(t, roles.AppUsername, roles.AppPassword)
	writerTx, err := writer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writerTx.Exec(ctx, `SELECT public.training_schedule_admit_partial($1,$2)`, state.DatabaseIncarnation, epoch); err != nil {
		t.Fatal(err)
	}
	writerDone := make(chan error, 1)
	go func() {
		_, err := writerTx.Exec(ctx, `SELECT id FROM training_plans WHERE id=$1 FOR UPDATE`, plan)
		if err == nil {
			_, err = writerTx.Exec(ctx, `INSERT INTO training_sessions(plan_id,title,entry_kind,schedule_precision,scheduled_on,starts_at,ends_at,created_by_id) SELECT id,'Admitted partial','REST','DAY',week_start,NULL,NULL,$2 FROM training_plans WHERE id=$1`, plan, actor)
		}
		if err == nil {
			err = writerTx.Commit(ctx)
		} else {
			_ = writerTx.Rollback(ctx)
		}
		writerDone <- err
	}()
	awaitScheduleLock(t, root, writer.PgConn().PID())
	closeDone := make(chan error, 1)
	go func() {
		var next int64
		err := operator.QueryRow(ctx, `SELECT training_schedule_control.set_enabled($1,$2,false)`, state.DatabaseIncarnation, epoch).Scan(&next)
		closeDone <- err
	}()
	awaitScheduleLock(t, root, operator.PgConn().PID())
	if _, err := oldTx.Exec(ctx, `UPDATE training_sessions SET title=title WHERE id=$1`, session); err != nil {
		t.Fatal(err)
	}
	if err := oldTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first-state writer did not finish")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gate closer did not drain admitted writer")
	}
	state, err = VerifyTrainingScheduleRuntime(ctx, web, TrainingScheduleCapabilities{ReaderVersion: 2, WriterVersion: 1})
	if err != nil || state.PartialV2Enabled || !state.EverPartialV2Enabled || state.RequiredReader < 2 {
		t.Fatalf("closed P compatible reader/floor: %+v %v", state, err)
	}
	if _, err := web.Exec(ctx, `UPDATE training_sessions SET title=title WHERE id=$1`, partialIDs[0]); !errors.Is(TrainingScheduleWriteError(err), ErrTrainingScheduleWriteUnsupported) {
		t.Fatalf("closed gate allowed later partial mutation: %v", err)
	}
	if _, err := web.Exec(ctx, `INSERT INTO training_plan_publications(plan_id,revision,source_updated_at,change_summary,published_by_id) SELECT id,3,updated_at,'Must not omit partials',$2 FROM training_plans WHERE id=$1`, plan, actor); !errors.Is(TrainingScheduleWriteError(err), ErrTrainingScheduleWriteUnsupported) {
		t.Fatalf("closed publication subset bypass: %v", err)
	}
	if err := low.QueryRow(ctx, `SELECT training_schedule_control.commit_floor($1,$2,$3,repeat('c',64),3)`, state.DatabaseIncarnation, state.StateEpoch, uuid.New()).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTrainingScheduleRuntime(ctx, web, TrainingScheduleCapabilities{ReaderVersion: 2, WriterVersion: 1}); err == nil {
		t.Fatal("reader2 treated as safe for future reader3 generation")
	}
	if err := low.QueryRow(ctx, `SELECT training_schedule_control.commit_floor($1,$2,$3,repeat('d',64),2)`, state.DatabaseIncarnation, epoch, uuid.New()).Scan(&epoch); err == nil {
		t.Fatal("reader floor lowered")
	}
}

func awaitScheduleLock(t *testing.T, root *pgx.Conn, pid uint32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		if err := root.QueryRow(t.Context(), `SELECT COALESCE(wait_event_type='Lock',false) FROM pg_catalog.pg_stat_activity WHERE pid=$1`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expected PostgreSQL lock waiter not observed")
}

func verifyPreCTrainingReader(t *testing.T, roles RoleCredentials, actor uuid.UUID) {
	t.Helper()
	dir := t.TempDir()
	archive := exec.Command("git", "archive", "--format=tar", "7b1f66a888ba8086485b7b01ed838f8558546664")
	archive.Dir = filepath.Join("..", "..")
	pipe, err := archive.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	extract := exec.Command("tar", "-xf", "-", "-C", dir)
	extract.Stdin = pipe
	if err := archive.Start(); err != nil {
		t.Fatal(err)
	}
	if err := extract.Run(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Wait(); err != nil {
		t.Fatal(err)
	}
	probeDir := filepath.Join(dir, "cmd", "precision-compat-probe")
	if err := os.MkdirAll(probeDir, 0700); err != nil {
		t.Fatal(err)
	}
	const probe = `package main
import("context";"fmt";"os";"github.com/cfcoimbra/mycfc/internal/db";dbgen "github.com/cfcoimbra/mycfc/internal/db/generated";"github.com/google/uuid";"github.com/jackc/pgx/v5")
func main(){ctx:=context.Background();c,e:=pgx.Connect(ctx,os.Getenv("PRECISION_PROBE_URL"));if e!=nil{panic("probe connection failed")};defer c.Close(ctx);if e=db.VerifyDatedParticipationRuntime(ctx,c);e!=nil{panic("actual pre-C startup schema verifier refused C")};a,e:=uuid.Parse(os.Getenv("PRECISION_PROBE_ACTOR"));if e!=nil{panic(e)};rows,e:=dbgen.New(c).ListStructuredTrainingOverviewForManager(ctx,dbgen.ListStructuredTrainingOverviewForManagerParams{IsAdmin:true,UserID:a});if e!=nil||len(rows)==0{panic("actual pre-C query reader refused C")};for _,r:=range rows{if r.SessionID!=nil&&(!r.StartsAt.Valid||!r.EndsAt.Valid){panic("pre-C reader received unknown endpoint")}};fmt.Println("actual pre-C verifier and exact reader passed")}
`
	if err := os.WriteFile(filepath.Join(probeDir, "main.go"), []byte(probe), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "probe")
	build := exec.Command("go", "build", "-o", binary, "./cmd/precision-compat-probe")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build owned old-source probe: %v %s", err, out)
	}
	u, err := url.Parse(os.Getenv("TEST_SCHEDULE_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(roles.AppUsername, roles.AppPassword)
	run := exec.Command(binary)
	run.Env = append(os.Environ(), "PRECISION_PROBE_URL="+u.String(), "PRECISION_PROBE_ACTOR="+actor.String())
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("source7b1f66a compatibility probe failed: %v %s", err, out)
	} else {
		t.Log(strings.TrimSpace(string(out)))
	}
}

func verifyFreshTrainingScheduleParity(t *testing.T, parent *pgx.Conn) {
	t.Helper()
	ctx := t.Context()
	container := os.Getenv("TEST_SCHEDULE_CONTAINER")
	if container == "" {
		t.Fatal("schema dump parity requires its owned isolated container")
	}
	if _, err := parent.Exec(ctx, `CREATE DATABASE schedule_c_fresh`); err != nil {
		t.Fatal(err)
	}
	cfg := parent.Config().Copy()
	cfg.Database = "schedule_c_fresh"
	freshRoot, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer freshRoot.Close(ctx)
	roles := RoleCredentials{AppUsername: "schedule_fresh_web", AppPassword: "synthetic-web", MigrationUsername: "schedule_fresh_migration", MigrationPassword: "synthetic-migration", MediaCleanupUsername: "schedule_fresh_media", MediaCleanupPassword: "synthetic-media", DataRetentionUsername: "schedule_fresh_retention", DataRetentionPassword: "synthetic-retention"}
	if err := BootstrapRoles(ctx, freshRoot, cfg.Database, roles); err != nil {
		t.Fatal(err)
	}
	lowCfg := cfg.Copy()
	lowCfg.User, lowCfg.Password = roles.MigrationUsername, roles.MigrationPassword
	freshLow, err := pgx.ConnectConfig(ctx, lowCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer freshLow.Close(ctx)
	if err := ApplyBaselineAndHarden(ctx, freshLow, cfg.Database, roles); err != nil {
		t.Fatal(err)
	}
	webCfg := cfg.Copy()
	webCfg.User, webCfg.Password = roles.AppUsername, roles.AppPassword
	freshWeb, err := pgx.ConnectConfig(ctx, webCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer freshWeb.Close(ctx)
	if _, err := VerifyTrainingScheduleRuntime(ctx, freshWeb, TrainingScheduleCapabilities{ReaderVersion: 2, WriterVersion: 1}); err != nil {
		t.Fatalf("fresh C runtime/ACL contract: %v", err)
	}
	dump := func(database string) string {
		t.Helper()
		cmd := exec.Command("docker", "exec", container, "pg_dump", "-U", parent.Config().User, "-d", database, "--schema-only", "--no-owner", "--no-acl", "--exclude-schema=mycfc_fresh_init")
		out, err := cmd.Output()
		if err != nil {
			t.Fatal("owned schema dump failed")
		}
		var lines []string
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, `\restrict `) || strings.HasPrefix(line, `\unrestrict `) {
				continue
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n")
	}
	a, b := dump("mycfc"), dump("schedule_c_fresh")
	if a != b {
		index := 0
		for index < len(a) && index < len(b) && a[index] == b[index] {
			index++
		}
		t.Fatalf("fresh/forward schema parity mismatch at byte%d", index)
	}
}
