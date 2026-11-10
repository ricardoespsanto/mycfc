//go:build integration

package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type publicationDomainFixture struct {
	root, low                                        *pgx.Conn
	web                                              *pgxpool.Pool
	actor, subject, membership, group, plan, session uuid.UUID
	programme, season, placement                     uuid.UUID
	roles                                            db.RoleCredentials
	raw                                              string
}

func newPublicationDomainFixture(t *testing.T) *publicationDomainFixture {
	t.Helper()
	name := "mycfc-recipient-domain-" + uuid.NewString()[:8]
	password := strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-P", "-e", "POSTGRES_DB=mycfc", "-e", "POSTGRES_USER=acceptance", "-e", "POSTGRES_PASSWORD="+password, "postgres:16.9").Run(); err != nil {
		t.Fatal("owned domain PostgreSQL startup failed")
	}
	t.Cleanup(func() {
		if err := exec.Command("docker", "stop", name).Run(); err != nil {
			t.Error("owned domain PostgreSQL cleanup failed")
		}
	})
	out, err := exec.Command("docker", "port", name, "5432/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	port := line[strings.LastIndex(line, ":")+1:]
	raw := "postgres://acceptance:" + password + "@127.0.0.1:" + port + "/mycfc?sslmode=disable"
	f := &publicationDomainFixture{raw: raw, roles: db.RoleCredentials{AppUsername: "recipient_web", AppPassword: "synthetic-web", MigrationUsername: "recipient_migration", MigrationPassword: "synthetic-migration", MediaCleanupUsername: "recipient_media", MediaCleanupPassword: "synthetic-media", DataRetentionUsername: "recipient_retention", DataRetentionPassword: "synthetic-retention"}}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		f.root, err = pgx.Connect(t.Context(), raw)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("owned domain PostgreSQL unavailable")
	}
	t.Cleanup(func() { f.root.Close(context.Background()) })
	if err = db.BootstrapRoles(t.Context(), f.root, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgx.ParseConfig(raw)
	cfg.User, cfg.Password = f.roles.MigrationUsername, f.roles.MigrationPassword
	f.low, err = pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.low.Close(context.Background()) })
	if err = db.ApplyBaselineAndHarden(t.Context(), f.low, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	if err = db.HardenPrivacyExecutionRoles(t.Context(), f.root, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	state, err := db.TrainingScheduleStatus(t.Context(), f.low)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.low.Exec(t.Context(), `SELECT training_schedule_control.commit_floor($1,$2,$3,$4,4)`, state.DatabaseIncarnation, state.StateEpoch, uuid.New(), strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	u.User = url.UserPassword(f.roles.AppUsername, f.roles.AppPassword)
	f.web, err = pgxpool.New(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.web.Close)
	f.actor, f.subject, f.membership, f.group, f.plan, f.session = uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	season := uuid.New()
	f.season = season
	for _, id := range []uuid.UUID{f.actor, f.subject} {
		f.exec(t, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Recipient composed subject',$2,'hash','1980-01-01')`, id, id.String()+"@example.test")
	}
	f.exec(t, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, f.actor)
	f.exec(t, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,'COMPOSED','Composed season',CURRENT_DATE-365,CURRENT_DATE+400)`, season)
	var programme uuid.UUID
	if err = f.web.QueryRow(t.Context(), `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	f.programme = programme
	f.exec(t, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,CURRENT_DATE,CURRENT_DATE+300)`, f.membership, f.subject, season, programme)
	f.exec(t, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Composed group',$2,$3)`, f.group, programme, f.actor)
	f.placement = f.place(t, f.subject, f.group, season, f.day(t, 0))
	f.exec(t, `INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) VALUES($1,'Composed week',$2,$3,$4,date_trunc('week',CURRENT_DATE)::date+7,$5)`, f.plan, programme, f.group, season, f.actor)
	f.exec(t, `INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) SELECT $1,id,'Composed exact session',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours',$3 FROM training_plans WHERE id=$2`, f.session, f.plan, f.actor)
	return f
}
func (f *publicationDomainFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.web.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
func (f *publicationDomainFixture) review(t *testing.T, summary string) publicationConfirmInput {
	t.Helper()
	store := PostgresStructuredTrainingStore{Pool: f.web}
	source, err := store.ReadPublicationSource(t.Context(), f.actor, f.plan)
	if err != nil {
		t.Fatal(err)
	}
	location, _ := time.LoadLocation("Europe/Lisbon")
	preview, err := makePublicationPreview(source, f.actor, summary, trainingPublicationReturnContext{}, location, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claims, err := trainingPublicationReviews.verify(preview.Token, preview.Metadata, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return publicationConfirmInput{ActorID: f.actor, PlanID: f.plan, Review: claims}
}
func TestRecipientPublicationComposedReviewSaveNoopAndStale(t *testing.T) {
	f := newPublicationDomainFixture(t)
	store := PostgresStructuredTrainingStore{Pool: f.web}
	original := f.review(t, "Primeira linha\r\nSegunda\tlinha")
	start := time.Now()
	first, err := store.ConfirmPublication(t.Context(), original)
	t.Logf("composed single-recipient confirm %s", time.Since(start))
	if err != nil || first.NoOp || first.Revision != 1 || first.PrescriptionCount != 1 {
		t.Fatalf("first %+v %v", first, err)
	}
	if _, err = store.ConfirmPublication(t.Context(), original); !errors.Is(err, errStructuredTrainingPublicationConflict) {
		t.Fatal("stale baseline not rejected", err)
	}
	fresh := f.review(t, "Summary alone is no content revision")
	var beforeXmin string
	if err = f.web.QueryRow(t.Context(), `SELECT xmin::text FROM training_plans WHERE id=$1`, f.plan).Scan(&beforeXmin); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		result, err := store.ConfirmPublication(t.Context(), fresh)
		if err != nil || !result.NoOp || result.PublicationID != first.PublicationID || result.Revision != 1 {
			t.Fatalf("noop %+v %v", result, err)
		}
	}
	var afterXmin string
	var count int
	if err = f.web.QueryRow(t.Context(), `SELECT xmin::text,(SELECT count(*) FROM training_plan_publications WHERE plan_id=$1) FROM training_plans WHERE id=$1`, f.plan).Scan(&afterXmin, &count); err != nil || beforeXmin != afterXmin || count != 1 {
		t.Fatalf("noop changed tuple/revision %s %s %d %v", beforeXmin, afterXmin, count, err)
	}
	f.exec(t, `UPDATE training_sessions SET title='Changed then reverted' WHERE id=$1`, f.session)
	f.exec(t, `UPDATE training_sessions SET title='Composed exact session' WHERE id=$1`, f.session)
	reverted := f.review(t, "Reverted content remains unchanged")
	result, err := store.ConfirmPublication(t.Context(), reverted)
	if err != nil || !result.NoOp {
		t.Fatalf("edit/revert %+v %v", result, err)
	}
	// The historical source timestamp is retained, while currentness is canonical.
	source, err := store.ReadPublicationSource(t.Context(), f.actor, f.plan)
	if err != nil {
		t.Fatal(err)
	}
	material, err := materializePublicationSource(source, time.UTC)
	if err != nil || !material.NoChanges {
		t.Fatal("canonical currentness failed", err)
	}
	f.changePlacement(t, trainingPlacementCommand{Kind: placementLeave, SubjectID: f.subject, ContextGroupID: f.group, SourcePlacementID: f.placement, EffectiveOn: f.day(t, 0)})
	if _, err = store.ConfirmPublication(t.Context(), reverted); err == nil {
		t.Fatal("changed audience silently recomputed")
	}
	empty := f.review(t, "Withdraw future audience explicitly")
	removed, err := store.ConfirmPublication(t.Context(), empty)
	if err != nil || removed.NoOp || removed.Revision != 2 || removed.PrescriptionCount != 0 {
		t.Fatalf("empty successor %+v %v", removed, err)
	}
	repeated := f.review(t, "Same empty successor")
	noop, err := store.ConfirmPublication(t.Context(), repeated)
	if err != nil || !noop.NoOp {
		t.Fatal("empty latest not noop", err)
	}
}
func TestRecipientPublicationComposedNativeHTTP(t *testing.T) {
	f := newPublicationDomainFixture(t)
	h := StructuredTraining{Publications: PostgresStructuredTrainingStore{Pool: f.web}, HTTPWriteTimeout: 30 * time.Second, Location: time.UTC}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/treinos/estruturados/semanas/{id}/publicar", h.PublishPlan)
	wrapped := httpx.PublicationIngressMiddleware(30 * time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: f.actor, Name: "Coach"}))
		mux.ServeHTTP(w, r)
	}))
	path := "/admin/treinos/estruturados/semanas/" + f.plan.String() + "/publicar"
	preview := publicationPost(t, wrapped, path, url.Values{"publication_intent": {"preview"}, "change_summary": {"Aprovado\r\ncom\tlinhas"}})
	if preview.Code != 200 {
		t.Fatalf("preview %d %s", preview.Code, preview.Body.String())
	}
	values := publicationConfirmForm(t, preview.Body.String())
	values.Set("change_summary", nativePublicationValue(values.Get("change_summary")))
	confirmed := publicationPost(t, wrapped, path, values)
	if confirmed.Code != 303 {
		t.Fatalf("confirm %d %s", confirmed.Code, confirmed.Body.String())
	}
	var saved string
	if err := f.web.QueryRow(t.Context(), `SELECT change_summary FROM training_plan_publications WHERE plan_id=$1`, f.plan).Scan(&saved); err != nil || saved != "Aprovado\r\ncom\tlinhas" {
		t.Fatalf("native summary mismatch %q %v", saved, err)
	}
}

// Run migrated historical callers with an owned fully hardened floor3 fixture
// and separately authenticated web publisher, rather than bootstrap execution.

func TestRecipientPublicationActualJSONBArtifactBoundary(t *testing.T) {
	f := newPublicationDomainFixture(t)
	store := PostgresStructuredTrainingStore{Pool: f.web}
	segment, block := uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO training_session_segments(id,session_id,position,modality) VALUES($1,$2,1,'GYM')`, segment, f.session)
	f.exec(t, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$2,1,'MAIN','Boundary prescription')`, block, segment)
	f.exec(t, `INSERT INTO gym_block_prescriptions(block_id,structure,objective) VALUES($1,'STRAIGHT_SETS','TECHNIQUE')`, block)
	f.exec(t, `INSERT INTO gym_exercises(block_id,position,name,repetitions,notes) VALUES($1,1,'Boundary exercise',10,repeat('N',1000))`, block)
	material := func() (publicationMaterialization, int) {
		t.Helper()
		source, err := store.ReadPublicationSource(t.Context(), f.actor, f.plan)
		if err != nil {
			t.Fatal(err)
		}
		built, err := materializePublicationSource(source, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		var bytes int
		if err = f.web.QueryRow(t.Context(), `SELECT octet_length($1::jsonb::text)`, built.Prescriptions[0].Snapshot).Scan(&bytes); err != nil {
			t.Fatal(err)
		}
		return built, bytes
	}
	_, first := material()
	f.exec(t, `INSERT INTO gym_exercises(block_id,position,name,repetitions,notes) VALUES($1,2,'Boundary exercise',10,repeat('N',1000))`, block)
	_, second := material()
	increment := second - first
	rows := 2 + (200000-second)/increment
	f.exec(t, `INSERT INTO gym_exercises(block_id,position,name,repetitions,notes) SELECT $1,n,'Boundary exercise',10,repeat('N',1000) FROM generate_series(3,$2::integer)n`, block, rows)
	_, bytes := material()
	for bytes > 200000 {
		f.exec(t, `DELETE FROM gym_exercises WHERE block_id=$1 AND position=$2`, block, rows)
		rows--
		_, bytes = material()
	}
	pad := 200000 - bytes
	if pad < 0 || pad >= 4000 {
		t.Fatalf("artifact boundary padding %d", pad)
	}
	f.exec(t, `UPDATE training_plans SET description=repeat('P',$2::integer) WHERE id=$1`, f.plan, pad)
	exact, bytes := material()
	if bytes != 200000 {
		t.Fatalf("actual JSONB boundary %d", bytes)
	}
	t.Logf("actual artifact bytes: compactGo=%d PostgreSQLjsonbText=%d", len(exact.Prescriptions[0].Snapshot), bytes)
	before := time.Now()
	accepted, err := store.ConfirmPublication(t.Context(), f.review(t, "Exact JSONB boundary"))
	t.Logf("exact artifact full canonical review+confirm %s", time.Since(before))
	if err != nil {
		t.Fatal("exact actual artifact rejected", err)
	}
	var storedHash string
	var storedBytes int
	if err = f.web.QueryRow(t.Context(), `SELECT snapshot_sha256,octet_length(snapshot::text) FROM training_prescriptions WHERE publication_id=$1`, accepted.PublicationID).Scan(&storedHash, &storedBytes); err != nil || storedBytes != 200000 || storedHash != exact.Prescriptions[0].SnapshotSHA256 {
		t.Fatal("canonical Go hash or actual DB bytes changed", err)
	}
	f.exec(t, `UPDATE training_plans SET description=repeat('P',$2::integer) WHERE id=$1`, f.plan, pad+1)
	_, bytes = material()
	if bytes != 200001 {
		t.Fatalf("actual over-boundary %d", bytes)
	}
	var xmin string
	if err = f.web.QueryRow(t.Context(), `SELECT xmin::text FROM training_plans WHERE id=$1`, f.plan).Scan(&xmin); err != nil {
		t.Fatal(err)
	}
	over := f.review(t, "One actual DB byte too large")
	if _, err = store.ConfirmPublication(t.Context(), over); !errors.Is(err, errTrainingPublicationReviewLimit) {
		t.Fatal("actual artifact overflow not safely refused", err)
	}
	var after string
	var count int
	if err = f.web.QueryRow(t.Context(), `SELECT xmin::text,(SELECT count(*) FROM training_plan_publications WHERE plan_id=$1) FROM training_plans WHERE id=$1`, f.plan).Scan(&after, &count); err != nil || after != xmin || count != 1 {
		t.Fatalf("artifact overflow partial write %s/%s revisions%d %v", xmin, after, count, err)
	}
}

func TestRecipientPublicationComposedTupleCeilingDeadlineAndOverflow(t *testing.T) {
	f := newPublicationDomainFixture(t)
	store := PostgresStructuredTrainingStore{Pool: f.web}
	f.exec(t, `INSERT INTO training_sessions(plan_id,title,starts_at,ends_at,created_by_id) SELECT plan_id,'Bounded exact session',starts_at,ends_at,created_by_id FROM training_sessions CROSS JOIN generate_series(1,4095) WHERE id=$1`, f.session)
	var memoryBefore, memoryAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memoryBefore)
	started := time.Now()
	source, err := store.ReadPublicationSource(t.Context(), f.actor, f.plan)
	if err != nil {
		t.Fatal("exact tuple source refused", err)
	}
	if len(source.Candidates) != 4096 {
		t.Fatal("exact tuple set truncated")
	}
	preview, err := makePublicationPreview(source, f.actor, "Exact technical tuple ceiling", trainingPublicationReturnContext{}, time.UTC, time.Now())
	if err != nil {
		t.Fatal("exact preview refused", err)
	}
	claims, err := trainingPublicationReviews.verify(preview.Token, preview.Metadata, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&memoryAfter)
	t.Logf("4096-tuples source+canonical+sign: elapsed=%s GoTotalAllocDelta=%d GoHeapAllocBefore=%d after=%d snapshots=%d", time.Since(started), memoryAfter.TotalAlloc-memoryBefore.TotalAlloc, memoryBefore.HeapAlloc, memoryAfter.HeapAlloc, len(preview.Materialization.Prescriptions))
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	started = time.Now()
	result, saveErr := store.ConfirmPublication(ctx, publicationConfirmInput{ActorID: f.actor, PlanID: f.plan, Review: claims})
	elapsed := time.Since(started)
	t.Logf("4096-tuples full admitted confirm: elapsed=%s noOp=%t error=%v", elapsed, result.NoOp, saveErr)
	if elapsed > 16*time.Second {
		t.Fatal("transaction+cleanup exceeded reserved bounds")
	}
	var publications, prescriptions int
	if err = f.web.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM training_plan_publications WHERE plan_id=$1),(SELECT count(*) FROM training_prescriptions prescription JOIN training_plan_publications publication ON publication.id=prescription.publication_id WHERE publication.plan_id=$1)`, f.plan).Scan(&publications, &prescriptions); err != nil {
		t.Fatal(err)
	}
	if saveErr != nil {
		if publications != 0 || prescriptions != 0 {
			t.Fatal("bounded refusal partially published")
		}
	} else if publications != 1 || prescriptions != 4096 {
		t.Fatal("exact admitted save truncated")
	}
	f.exec(t, `INSERT INTO training_sessions(plan_id,title,starts_at,ends_at,created_by_id) SELECT plan_id,'Overflow exact session',starts_at,ends_at,created_by_id FROM training_sessions WHERE id=$1`, f.session)
	if _, err = store.ReadPublicationSource(t.Context(), f.actor, f.plan); !errors.Is(err, errTrainingPublicationReviewLimit) {
		t.Fatal("4097 unfiltered tuples not refused", err)
	}
	var after int
	if err = f.web.QueryRow(t.Context(), `SELECT count(*) FROM training_plan_publications WHERE plan_id=$1`, f.plan).Scan(&after); err != nil || after != publications {
		t.Fatal("overflow manufactured publication", err)
	}
}

func TestRecipientPublicationComposedSourceByteCeiling(t *testing.T) {
	f := newPublicationDomainFixture(t)
	store := PostgresStructuredTrainingStore{Pool: f.web}
	segment, block := uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO training_session_segments(id,session_id,position,modality) VALUES($1,$2,1,'GYM')`, segment, f.session)
	f.exec(t, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$2,1,'MAIN',repeat('B',1000))`, block, segment)
	f.exec(t, `INSERT INTO gym_block_prescriptions(block_id,structure,objective) VALUES($1,'STRAIGHT_SETS','TECHNIQUE')`, block)
	f.exec(t, `INSERT INTO gym_exercises(block_id,position,name,repetitions) VALUES($1,1,'Source boundary exercise',10)`, block)
	fixed := func() { f.exec(t, `UPDATE training_plans SET updated_at='2026-11-10T12:00:00Z' WHERE id=$1`, f.plan) }
	q := dbgen.New(f.web)
	budget := func() dbgen.PublicationSourceBudgetRow {
		t.Helper()
		fixed()
		b, err := q.PublicationSourceBudget(t.Context(), dbgen.PublicationSourceBudgetParams{ActorID: f.actor, PlanID: f.plan})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	first := budget()
	f.exec(t, `INSERT INTO gym_exercises(block_id,position,name,repetitions) VALUES($1,2,'Source boundary exercise',10)`, block)
	second := budget()
	step := second.SourceBytes - first.SourceBytes
	rows := 2 + int((publicationSourceMaxBytes-second.SourceBytes)/step) - 10
	f.exec(t, `INSERT INTO gym_exercises(block_id,position,name,repetitions) SELECT $1,n,'Source boundary exercise',10 FROM generate_series(3,$2::integer)n`, block, rows)
	b := budget()
	for b.Overflow {
		rows -= 10
		f.exec(t, `DELETE FROM gym_exercises WHERE block_id=$1 AND position>$2`, block, rows)
		b = budget()
	}
	padding := publicationSourceMaxBytes - b.SourceBytes
	if padding < 0 || padding > int64(rows)*1000 {
		t.Fatal("source padding invalid", padding)
	}
	f.exec(t, `UPDATE gym_exercises SET notes=repeat('N',LEAST(1000::bigint,GREATEST(0::bigint,$2::bigint-(position-1)::bigint*1000))::integer) WHERE block_id=$1 AND position<=($2::bigint+999)/1000`, block, padding)
	b = budget()
	if b.Overflow || b.SourceBytes != publicationSourceMaxBytes {
		t.Fatalf("source boundary not exact %+v", b)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	started := time.Now()
	source, err := store.ReadPublicationSource(t.Context(), f.actor, f.plan)
	if err != nil {
		t.Fatal("exact source gate refused", err)
	}
	preview, err := makePublicationPreview(source, f.actor, "Exact canonical source transport ceiling", trainingPublicationReturnContext{}, time.UTC, time.Now())
	if err != nil {
		t.Fatal("exact source canonical build", err)
	}
	claims, err := trainingPublicationReviews.verify(preview.Token, preview.Metadata, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	t.Logf("32MiB source+canonical+sign elapsed=%s GoTotalAllocDelta=%d GoHeapBefore=%d after=%d compactOutput=%d", time.Since(started), after.TotalAlloc-before.TotalAlloc, before.HeapAlloc, after.HeapAlloc, len(preview.Materialization.Prescriptions[0].Snapshot))
	started = time.Now()
	_, saveErr := store.ConfirmPublication(t.Context(), publicationConfirmInput{ActorID: f.actor, PlanID: f.plan, Review: claims})
	t.Logf("32MiB composed confirm elapsed=%s error=%v", time.Since(started), saveErr)
	if saveErr == nil {
		t.Fatal("per-artifact200000B overflow was accepted")
	}
	var count int
	if err = f.web.QueryRow(t.Context(), `SELECT count(*) FROM training_plan_publications WHERE plan_id=$1`, f.plan).Scan(&count); err != nil || count != 0 {
		t.Fatal("large-source refusal partially saved", err)
	}
	f.exec(t, `UPDATE gym_exercises SET notes=notes||'X' WHERE block_id=$1 AND position=$2`, block, rows)
	b = budget()
	if !b.Overflow || b.SourceBytes != publicationSourceMaxBytes+1 {
		t.Fatalf("source over-boundary not exact %+v", b)
	}
	if _, err = store.ReadPublicationSource(t.Context(), f.actor, f.plan); !errors.Is(err, errTrainingPublicationReviewLimit) {
		t.Fatal("source one-byte overflow not refused", err)
	}
}
