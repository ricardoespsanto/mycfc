//go:build integration

package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type recipientSchemaFixture struct {
	root, low      *pgx.Conn
	url, container string
	roles          RoleCredentials
}

func newRecipientSchemaFixture(t *testing.T, bare ...bool) recipientSchemaFixture {
	t.Helper()
	name := "mycfc-recipient-schema-" + uuid.NewString()[:8]
	password := strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-P", "-e", "POSTGRES_DB=mycfc", "-e", "POSTGRES_USER=acceptance", "-e", "POSTGRES_PASSWORD="+password, "postgres:16.9", "-c", "max_prepared_transactions=8").Run(); err != nil {
		t.Fatal("isolated recipient schema PostgreSQL failed")
	}
	t.Cleanup(func() {
		if err := exec.Command("docker", "stop", name).Run(); err != nil {
			t.Error("isolated schema PostgreSQL cleanup failed")
		}
	})
	out, err := exec.Command("docker", "port", name, "5432/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	port := line[strings.LastIndex(line, ":")+1:]
	raw := "postgres://acceptance:" + password + "@127.0.0.1:" + port + "/mycfc?sslmode=disable"
	var root *pgx.Conn
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		root, err = pgx.Connect(t.Context(), raw)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("isolated schema PostgreSQL not ready")
	}
	t.Cleanup(func() { _ = root.Close(context.Background()) })
	roles := RoleCredentials{AppUsername: "recipient_web", AppPassword: "synthetic-web", MigrationUsername: "recipient_migration", MigrationPassword: "synthetic-migration", MediaCleanupUsername: "recipient_media", MediaCleanupPassword: "synthetic-media", DataRetentionUsername: "recipient_retention", DataRetentionPassword: "synthetic-retention"}
	if len(bare) > 0 && bare[0] {
		return recipientSchemaFixture{root: root, url: raw, container: name, roles: roles}
	}
	if err = BootstrapRoles(t.Context(), root, "mycfc", roles); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.User = roles.MigrationUsername
	cfg.Password = roles.MigrationPassword
	low, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = low.Close(context.Background()) })
	return recipientSchemaFixture{root, low, raw, name, roles}
}
func recipientSchemaExec(t *testing.T, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, sql string, args ...any) {
	t.Helper()
	if _, err := q.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
func (f recipientSchemaFixture) install79(t *testing.T) {
	t.Helper()
	parent := strings.Split(baselineSchema, "\n-- Baseline through 202611100002_training_publication_recipient_revision.")[0]
	if _, err := f.low.PgConn().Exec(t.Context(), parent).ReadAll(); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, f.low, `CREATE TABLE mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`)
	versions, _ := trainingRecipientInventory(releasecontract.NormalRecipientParentDigest)
	for _, version := range versions {
		recipientSchemaExec(t, f.low, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES($1)`, version)
	}
	if err := HardenPrivacyExecutionRoles(t.Context(), f.root, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
}

func (f recipientSchemaFixture) web(t *testing.T) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig(f.url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.User = f.roles.AppUsername
	cfg.Password = f.roles.AppPassword
	conn, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}
func (f recipientSchemaFixture) inspectionInput(mode RecipientInspectionMode) RecipientDatabaseInspectionInput {
	return RecipientDatabaseInspectionInput{Database: "mycfc", MigrationRole: f.roles.MigrationUsername, PublisherRole: f.roles.AppUsername, Mode: mode, InspectionTimeout: 10 * time.Second, CleanupTimeout: 2 * time.Second}
}
func (f recipientSchemaFixture) receipt(t *testing.T) {
	t.Helper()
	recipientSchemaExec(t, f.root, `CREATE SCHEMA mycfc_fresh_init;REVOKE ALL ON SCHEMA mycfc_fresh_init FROM PUBLIC;CREATE TABLE mycfc_fresh_init.receipt(database_name text PRIMARY KEY,version text NOT NULL,candidate text NOT NULL,schema_digest text NOT NULL);REVOKE ALL ON mycfc_fresh_init.receipt FROM PUBLIC`)
	recipientSchemaExec(t, f.root, `INSERT INTO mycfc_fresh_init.receipt VALUES('mycfc',$1,$2,$3)`, releasecontract.FreshGenesisVersion, releasecontract.FreshGenesisCandidate, releasecontract.FreshGenesisDigest)
}
func recipientStampedFixture(t *testing.T) {
	t.Helper()
	v, c := releasecontract.Version, releasecontract.Candidate
	releasecontract.Version, releasecontract.Candidate = "v99.0.0", strings.Repeat("c", 40)
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = v, c })
}
func recipientReceiptMatches(t *testing.T, f recipientSchemaFixture, want bool) {
	t.Helper()
	got, err := FreshInitializationMatchesWithInspector(t.Context(), f.root, f.low, f.roles.MigrationUsername, "mycfc", releasecontract.Version, releasecontract.Candidate)
	if got != want || (want && err != nil) {
		t.Fatalf("receipt match=%t want=%t err=%v", got, want, err)
	}
}

type recipientHistory struct {
	actor, subject, membership, group, plan, session, publication, prescription uuid.UUID
	source                                                                      time.Time
	before                                                                      string
}

func recipientHistoryFixture(t *testing.T, f recipientSchemaFixture) recipientHistory {
	t.Helper()
	web := f.web(t)
	h := recipientHistory{actor: uuid.New(), subject: uuid.New(), membership: uuid.New(), group: uuid.New(), plan: uuid.New(), session: uuid.New(), publication: uuid.New(), prescription: uuid.New()}
	season := uuid.New()
	for _, id := range []uuid.UUID{h.actor, h.subject} {
		recipientSchemaExec(t, web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic recipient subject',$2,'hash','1980-01-01')`, id, id.String()+"@example.test")
	}
	recipientSchemaExec(t, web, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, h.actor)
	recipientSchemaExec(t, web, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,'RECIPIENT','Synthetic season',CURRENT_DATE-365,CURRENT_DATE+400)`, season)
	var programme uuid.UUID
	if err := web.QueryRow(t.Context(), `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, web, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,CURRENT_DATE,CURRENT_DATE+300)`, h.membership, h.subject, season, programme)
	recipientSchemaExec(t, web, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Synthetic recipient group',$2,$3)`, h.group, programme, h.actor)
	recipientSchemaExec(t, web, `INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, h.group, h.membership, h.actor)
	recipientSchemaExec(t, web, `INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) VALUES($1,'Synthetic recipient week',$2,$3,$4,date_trunc('week',CURRENT_DATE)::date+7,$5)`, h.plan, programme, h.group, season, h.actor)
	recipientSchemaExec(t, web, `INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) SELECT $1,id,'Synthetic exact session',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours',$3 FROM training_plans WHERE id=$2`, h.session, h.plan, h.actor)
	if err := web.QueryRow(t.Context(), `SELECT updated_at FROM training_plans WHERE id=$1`, h.plan).Scan(&h.source); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, web, `INSERT INTO training_plan_publications(id,plan_id,revision,source_updated_at,change_summary,published_by_id) VALUES($1,$2,1,$3,'Synthetic first publication',$4)`, h.publication, h.plan, h.source, h.actor)
	raw := `{"schema_version":1,"session":{"EntryKind":"WORKOUT"}}`
	sum := sha256.Sum256([]byte(raw))
	recipientSchemaExec(t, web, `INSERT INTO training_prescriptions(id,publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256) VALUES($1,$2,$3,$4,$5,$6,$7)`, h.prescription, h.publication, h.session, h.membership, h.subject, raw, hex.EncodeToString(sum[:]))
	recipientSchemaExec(t, web, `INSERT INTO training_session_outcomes(session_id,user_id,prescription_id,status) VALUES($1,$2,$3,'COMPLETED')`, h.session, h.subject, h.prescription)
	h.before = recipientHistoryBytes(t, f.low, h)
	if _, err := web.Exec(t.Context(), `INSERT INTO training_plan_publications(plan_id,revision,source_updated_at,change_summary,supersedes_id,published_by_id) VALUES($1,2,$2,'Same-source before migration',$3,$4)`, h.plan, h.source, h.publication, h.actor); err == nil {
		t.Fatal("old source constraint did not reject duplicate source")
	}
	return h
}
func recipientHistoryBytes(t *testing.T, q TrainingScheduleQueryReader, h recipientHistory) string {
	t.Helper()
	var data string
	if err := q.QueryRow(t.Context(), `SELECT jsonb_build_array(row_to_json(plan),row_to_json(publication),row_to_json(prescription),row_to_json(outcome))::text FROM training_plans plan JOIN training_plan_publications publication ON publication.id=$2 JOIN training_prescriptions prescription ON prescription.id=$3 JOIN training_session_outcomes outcome ON outcome.session_id=$4 AND outcome.user_id=$5 WHERE plan.id=$1`, h.plan, h.publication, h.prescription, h.session, h.subject).Scan(&data); err != nil {
		t.Fatal(err)
	}
	return data
}
func recipientRejectDrift(t *testing.T, f recipientSchemaFixture, digest, sql string) {
	t.Helper()
	tx, err := f.low.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	recipientSchemaExec(t, tx, sql)
	if _, err = InspectTrainingRecipientParent(t.Context(), tx, digest); err == nil {
		t.Fatalf("catalog admitted drift %s", sql)
	}
}

func TestTrainingRecipientForwardSchemaAndRuntime(t *testing.T) {
	recipientStampedFixture(t)
	f := newRecipientSchemaFixture(t)
	f.install79(t)
	f.receipt(t)
	recipientReceiptMatches(t, f, true)
	inspection, err := InspectTrainingRecipientDatabase(t.Context(), f.low, f.inspectionInput(RecipientInspectParent))
	if err != nil || inspection.Phase != RecipientReviewed79 || inspection.ScheduleState == nil || inspection.ScheduleState.RequiredReader != 1 {
		t.Fatalf("parent79 phase %+v %v", inspection, err)
	}
	for _, sql := range []string{
		`ALTER TABLE training_plan_publications DROP CONSTRAINT training_plan_publications_source_unique`,
		`ALTER TABLE training_plan_publications DROP CONSTRAINT training_plan_publications_source_unique;ALTER TABLE training_plan_publications ADD CONSTRAINT training_plan_publications_source_unique UNIQUE(plan_id,revision,source_updated_at)`,
		`ALTER TABLE training_plan_publications DROP CONSTRAINT training_plan_publications_revision_valid;ALTER TABLE training_plan_publications ADD CONSTRAINT training_plan_publications_revision_valid CHECK(true)`,
		`ALTER TABLE training_plan_publications ALTER COLUMN source_updated_at DROP NOT NULL`,
		`ALTER TABLE training_prescriptions DISABLE TRIGGER training_prescriptions_immutable_trigger`,
		`CREATE OR REPLACE FUNCTION prevent_training_publication_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$`,
		`INSERT INTO mycfc_meta.schema_migrations(version) VALUES('unreviewed-marker')`,
	} {
		recipientRejectDrift(t, f, releasecontract.NormalRecipientParentDigest, sql)
	}
	h := recipientHistoryFixture(t, f)
	if err = ApplyBaselineAndHarden(t.Context(), f.low, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	if got := recipientHistoryBytes(t, f.low, h); got != h.before {
		t.Fatal("constraint migration changed stored history or plan")
	}
	recipientReceiptMatches(t, f, true)
	if _, err = InspectTrainingRecipientDatabase(t.Context(), f.connectLow(t), f.inspectionInput(RecipientInspectParent)); err == nil {
		t.Fatal("parent-only inspector accepted final")
	}
	final, err := InspectTrainingRecipientDatabase(t.Context(), f.low, f.inspectionInput(RecipientInspectFinal))
	if err != nil || final.Phase != RecipientFinal || final.ScheduleState == nil {
		t.Fatalf("pre-floor final inspection failed %+v %v", final, err)
	}
	web := f.web(t)
	if _, err = VerifyTrainingRecipientRuntime(t.Context(), web, TrainingScheduleCapabilities{ReaderVersion: 3, WriterVersion: 1}); err == nil {
		t.Fatal("serving accepted uncommitted floor3")
	}
	for _, sql := range []string{
		`ALTER TABLE training_plans ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE training_plans FORCE ROW LEVEL SECURITY`,
		`CREATE FUNCTION witness_drift() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$;CREATE TRIGGER witness_drift BEFORE UPDATE ON training_plans FOR EACH ROW EXECUTE FUNCTION witness_drift()`,
		`CREATE RULE witness_drift AS ON UPDATE TO training_plans DO ALSO NOTIFY witness_drift`,
		`CREATE TABLE witness_child() INHERITS(training_plans)`,
		`ALTER TABLE training_plans DROP CONSTRAINT training_plans_week_valid;ALTER TABLE training_plans ADD CONSTRAINT training_plans_week_valid CHECK(true)`,
		`CREATE UNIQUE INDEX renamed_source_unique ON training_plan_publications(plan_id,source_updated_at)`,
		`CREATE UNIQUE INDEX partial_source_unique ON training_plan_publications(source_updated_at,plan_id) WHERE revision>0`,
		`ALTER TABLE training_plan_publications DROP CONSTRAINT training_plan_publications_revision_unique`,
		`ALTER TABLE training_prescriptions DROP CONSTRAINT training_prescriptions_publication_session_membership_unique`,
		`ALTER TABLE training_prescriptions DROP CONSTRAINT training_prescriptions_hash_valid;ALTER TABLE training_prescriptions ADD CONSTRAINT training_prescriptions_hash_valid CHECK(true)`,
		`ALTER TABLE training_plan_publications DROP CONSTRAINT training_plan_publications_supersedes_id_fkey`,
	} {
		recipientRejectDrift(t, f, releasecontract.NormalFinalDigest, sql)
	}
	state, err := TrainingScheduleStatus(t.Context(), f.low)
	if err != nil {
		t.Fatal(err)
	}
	floorID := uuid.New()
	manifest := strings.Repeat("a", 64)
	_, err = WithGuardianPolicyWriter(t.Context(), f.low, PolicyWriterOptions{AdmissionTimeout: 5 * time.Second, CleanupTimeout: 2 * time.Second}, func(ctx context.Context, c *pgx.Conn) error {
		var epoch int64
		return c.QueryRow(ctx, `SELECT training_schedule_control.commit_floor($1,$2,$3,$4,3)`, state.DatabaseIncarnation, state.StateEpoch, floorID, manifest).Scan(&epoch)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyTrainingRecipientRuntime(t.Context(), web, TrainingScheduleCapabilities{ReaderVersion: 3, WriterVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyTrainingRecipientRuntime(t.Context(), web, TrainingScheduleCapabilities{ReaderVersion: 2, WriterVersion: 1}); err == nil {
		t.Fatal("C2 accepted corrected recipient runtime")
	}
	wrongRoot, connectErr := pgx.Connect(t.Context(), f.url)
	if connectErr != nil {
		t.Fatal(connectErr)
	}
	if _, err = InspectTrainingRecipientDatabase(t.Context(), wrongRoot, f.inspectionInput(RecipientInspectFinal)); err == nil {
		t.Fatal("bootstrap accepted as low inspector")
	}
	wrongRoot, connectErr = pgx.Connect(t.Context(), f.url)
	if connectErr != nil {
		t.Fatal(connectErr)
	}
	recipientSchemaExec(t, wrongRoot, `SET ROLE recipient_migration`)
	if _, err = InspectTrainingRecipientDatabase(t.Context(), wrongRoot, f.inspectionInput(RecipientInspectFinal)); err == nil {
		t.Fatal("bootstrap SET ROLE accepted as independent inspector")
	}
	_ = wrongRoot.Close(t.Context())
	successor := uuid.New()
	recipientSchemaExec(t, web, `INSERT INTO training_plan_publications(id,plan_id,revision,source_updated_at,change_summary,supersedes_id,published_by_id) VALUES($1,$2,2,$3,'Same-source reviewed successor',$4,$5)`, successor, h.plan, h.source, h.publication, h.actor)
	recipientSchemaExec(t, web, `INSERT INTO training_prescriptions(publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256) SELECT $1,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256 FROM training_prescriptions WHERE id=$2`, successor, h.prescription)
	empty := uuid.New()
	recipientSchemaExec(t, web, `INSERT INTO training_plan_publications(id,plan_id,revision,source_updated_at,change_summary,supersedes_id,published_by_id) VALUES($1,$2,3,$3,'Explicit reviewed empty successor',$4,$5)`, empty, h.plan, h.source, successor, h.actor)
	var currentCount int
	if err = web.QueryRow(t.Context(), `SELECT count(*) FROM training_prescriptions WHERE publication_id=(SELECT id FROM training_plan_publications WHERE plan_id=$1 ORDER BY revision DESC LIMIT 1)`, h.plan).Scan(&currentCount); err != nil || currentCount != 0 {
		t.Fatal("empty current publication exposed earlier prescriptions as current")
	}
	if got := recipientHistoryBytes(t, f.low, h); got != h.before {
		t.Fatal("new revisions rewrote historic outcome or old snapshot")
	}
	if _, err = web.Exec(t.Context(), `UPDATE training_plan_publications SET change_summary='rewritten history' WHERE id=$1`, h.publication); err == nil {
		t.Fatal("immutable publication history update accepted")
	}
	if _, err = web.Exec(t.Context(), `DELETE FROM training_prescriptions WHERE id=$1`, h.prescription); err == nil {
		t.Fatal("immutable prescription history delete accepted")
	}
}

func (f recipientSchemaFixture) installHistorical(t *testing.T, phase RecipientSchemaPhase) {
	t.Helper()
	parent := strings.Split(baselineSchema, "\n-- Baseline through 202611100001_training_schedule_compatibility.")[0]
	if _, err := f.low.PgConn().Exec(t.Context(), parent).ReadAll(); err != nil {
		t.Fatal(err)
	}
	digest := releasecontract.NormalParentDigest
	if phase == RecipientReviewed77 {
		digest = releasecontract.FreshGenesisDigest
		if _, err := f.low.Exec(t.Context(), freshGenesisAttachmentSQL); err != nil {
			t.Fatal(err)
		}
	}
	recipientSchemaExec(t, f.low, `CREATE TABLE mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`)
	versions, _ := trainingRecipientInventory(digest)
	for _, version := range versions {
		recipientSchemaExec(t, f.low, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES($1)`, version)
	}
	if err := HardenPrivacyExecutionRoles(t.Context(), f.root, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
}
func TestTrainingRecipientHistoricalParentsAndReceipt(t *testing.T) {
	recipientStampedFixture(t)
	for _, phase := range []RecipientSchemaPhase{RecipientReviewed77, RecipientReviewed78} {
		t.Run(string(phase), func(t *testing.T) {
			f := newRecipientSchemaFixture(t)
			f.installHistorical(t, phase)
			f.receipt(t)
			recipientReceiptMatches(t, f, true)
			inspected, err := InspectTrainingRecipientDatabase(t.Context(), f.low, f.inspectionInput(RecipientInspectParent))
			if err != nil || inspected.Phase != phase || inspected.ScheduleState != nil {
				t.Fatalf("historical phase fabricated state %+v %v", inspected, err)
			}
			recipientRejectDrift(t, f, inspected.MigrationDigest, `ALTER TABLE training_plan_publications DROP CONSTRAINT training_plan_publications_revision_valid;ALTER TABLE training_plan_publications ADD CONSTRAINT training_plan_publications_revision_valid CHECK(true)`)
			if err = ApplyBaselineAndHarden(t.Context(), f.low, "mycfc", f.roles); err != nil {
				t.Fatal(err)
			}
			final, err := InspectTrainingRecipientDatabase(t.Context(), f.low, f.inspectionInput(RecipientInspectFinal))
			if err != nil || final.Phase != RecipientFinal || final.ScheduleState == nil {
				t.Fatalf("historical forward failed %+v %v", final, err)
			}
			recipientReceiptMatches(t, f, true)
			var receiptDigest string
			if err = f.root.QueryRow(t.Context(), `SELECT schema_digest FROM mycfc_fresh_init.receipt`).Scan(&receiptDigest); err != nil || receiptDigest != releasecontract.FreshGenesisDigest {
				t.Fatal("forward upgrade rewrote genesis receipt")
			}
			recipientSchemaExec(t, f.root, `UPDATE mycfc_fresh_init.receipt SET version='v98.0.0',candidate=$1,schema_digest=$2`, strings.Repeat("e", 40), releasecontract.NormalRecipientParentDigest)
			recipientReceiptMatches(t, f, false)
		})
	}
}

func recipientSchemaDump(t *testing.T, f recipientSchemaFixture) string {
	t.Helper()
	out, err := exec.Command("docker", "exec", f.container, "pg_dump", "-U", "acceptance", "--schema-only", "--no-owner", "--exclude-schema=mycfc_fresh_init", "mycfc").CombinedOutput()
	if err != nil {
		t.Fatal("isolated schema dump failed")
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "--") || strings.HasPrefix(line, "\\restrict") || strings.HasPrefix(line, "\\unrestrict") || strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
func TestTrainingRecipientFreshAndForwardParity(t *testing.T) {
	recipientStampedFixture(t)
	forward := newRecipientSchemaFixture(t)
	forward.install79(t)
	if err := ApplyBaselineAndHarden(t.Context(), forward.low, "mycfc", forward.roles); err != nil {
		t.Fatal(err)
	}
	fresh := newRecipientSchemaFixture(t, true)
	if empty, err := IsTrainingRecipientEmptyTarget(t.Context(), fresh.root, "mycfc"); err != nil || !empty {
		t.Fatalf("bareempty proof failed %t %v", empty, err)
	}
	if err := FreshInitializeDatabase(t.Context(), fresh.root, "mycfc", fresh.roles); err != nil {
		t.Fatal(err)
	}
	fresh.low = fresh.connectLow(t)
	recipientReceiptMatches(t, fresh, true)
	var digest string
	if err := fresh.root.QueryRow(t.Context(), `SELECT schema_digest FROM mycfc_fresh_init.receipt`).Scan(&digest); err != nil || digest != releasecontract.NormalFinalDigest {
		t.Fatal("corrected fresh receipt not actual final")
	}
	if err := VerifyTrainingRecipientMigrationFinal(t.Context(), fresh.low); err != nil {
		t.Fatal(err)
	}
	if recipientSchemaDump(t, forward) != recipientSchemaDump(t, fresh) {
		t.Fatal("fresh and forward full schema/ACL definitions differ")
	}
}

func TestTrainingRecipientPublisherFence(t *testing.T) {
	f := newRecipientSchemaFixture(t)
	f.install79(t)
	before, err := InspectTrainingRecipientPublisher(t.Context(), f.low, f.roles.AppUsername)
	if err != nil || !before.CanLogin || before.OID == 0 || before.UnexpectedPublisherCount != 0 {
		t.Fatalf("new publisher incorrectly initialized %+v %v", before, err)
	}
	if _, err = RequireTrainingRecipientPublisherFence(t.Context(), f.low, f.roles.AppUsername); err == nil {
		t.Fatal("LOGIN publisher admitted mutation")
	}
	web := f.web(t)
	recipientSchemaExec(t, f.root, `CREATE TABLE public.recipient_fence_probe(id integer PRIMARY KEY,value integer);INSERT INTO public.recipient_fence_probe VALUES(1,0);GRANT SELECT,UPDATE ON public.recipient_fence_probe TO recipient_web`)
	recipientSchemaExec(t, web, `BEGIN;UPDATE public.recipient_fence_probe SET value=1 WHERE id=1;PREPARE TRANSACTION 'recipient-schema-owned-prepared'`)
	recipientSchemaExec(t, f.root, `ALTER ROLE recipient_web NOLOGIN`)
	open, err := InspectTrainingRecipientPublisher(t.Context(), f.low, f.roles.AppUsername)
	if err != nil || open.BackendCount != 1 || open.PreparedCount != 1 || open.CanLogin {
		t.Fatalf("publisher backend/prepared inventory wrong %+v %v", open, err)
	}
	if _, err = RequireTrainingRecipientPublisherFence(t.Context(), f.low, f.roles.AppUsername); err == nil {
		t.Fatal("active publisher/prepared transaction admitted")
	}
	if err = web.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = RequireTrainingRecipientPublisherFence(t.Context(), f.low, f.roles.AppUsername); err == nil {
		t.Fatal("prepared transaction admitted after backend closed")
	}
	recipientSchemaExec(t, f.root, `ROLLBACK PREPARED 'recipient-schema-owned-prepared'`)
	clean, err := RequireTrainingRecipientPublisherFence(t.Context(), f.low, f.roles.AppUsername)
	if err != nil || clean.OID != before.OID {
		t.Fatalf("clean publisher not recognized %+v %v", clean, err)
	}
	if err = BootstrapRoles(t.Context(), f.root, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	preserved, err := RequireTrainingRecipientPublisherFence(t.Context(), f.low, f.roles.AppUsername)
	if err != nil || preserved.CanLogin || preserved.OID != before.OID {
		t.Fatalf("bootstrap reopened publisher authentication %+v %v", preserved, err)
	}
	recipientSchemaExec(t, f.root, `CREATE ROLE recipient_rogue LOGIN NOSUPERUSER;GRANT INSERT ON public.training_plan_publications TO recipient_rogue`)
	rogue, err := InspectTrainingRecipientPublisher(t.Context(), f.low, f.roles.AppUsername)
	if err != nil || rogue.UnexpectedPublisherCount != 1 {
		t.Fatalf("unexpected insert login not found %+v %v", rogue, err)
	}
	if _, err = RequireTrainingRecipientPublisherFence(t.Context(), f.low, f.roles.AppUsername); err == nil {
		t.Fatal("unexpected publisher admitted")
	}
	recipientSchemaExec(t, f.root, `REVOKE INSERT ON public.training_plan_publications FROM recipient_rogue;GRANT recipient_web TO recipient_rogue`)
	rogue, err = InspectTrainingRecipientPublisher(t.Context(), f.low, f.roles.AppUsername)
	if err != nil || rogue.UnexpectedPublisherCount != 1 {
		t.Fatalf("unexpected SET ROLE login not found %+v %v", rogue, err)
	}
	recipientSchemaExec(t, f.root, `REVOKE recipient_web FROM recipient_rogue;DROP ROLE recipient_rogue`)
	if _, err = RequireTrainingRecipientPublisherFence(t.Context(), f.low, f.roles.AppUsername); err != nil {
		t.Fatal(err)
	}
	if _, err = InspectTrainingRecipientPublisher(t.Context(), f.low, "role'; SELECT 1"); err == nil {
		t.Fatal("invalid configured role accepted")
	}
}

func (f recipientSchemaFixture) connectLow(t *testing.T) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig(f.url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.User = f.roles.MigrationUsername
	cfg.Password = f.roles.MigrationPassword
	low, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = low.Close(context.Background()) })
	return low
}

func TestTrainingRecipientLocalEmptyPhases(t *testing.T) {
	version, candidate := releasecontract.Version, releasecontract.Candidate
	releasecontract.Version, releasecontract.Candidate = "", ""
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = version, candidate })
	f := newRecipientSchemaFixture(t, true)
	classify := func(q TrainingScheduleQueryReader) (RecipientBootstrapPhase, error) {
		return InspectTrainingRecipientLocalBootstrapTarget(t.Context(), q, "mycfc", f.roles.MigrationUsername, f.roles.AppUsername)
	}
	if phase, err := classify(f.root); err != nil || phase != RecipientBareEmpty {
		t.Fatalf("bare-empty phase %s %v", phase, err)
	}
	if err := BootstrapRoles(t.Context(), f.root, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	f.low = f.connectLow(t)
	for _, q := range []TrainingScheduleQueryReader{f.root, f.low} {
		if phase, err := classify(q); err != nil || phase != RecipientPreparedEmpty {
			t.Fatalf("prepared-empty phase %s %v", phase, err)
		}
	}
	if err := BootstrapRoles(t.Context(), f.root, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	if phase, err := classify(f.low); err != nil || phase != RecipientPreparedEmpty {
		t.Fatalf("prepared retry phase %s %v", phase, err)
	}
	for _, sql := range []string{
		`CREATE TABLE public.application_probe(id integer)`,
		`CREATE TYPE public.application_enum AS ENUM('unsafe')`,
		`CREATE FUNCTION public.application_function() RETURNS boolean LANGUAGE sql AS $$ SELECT true $$`,
		`CREATE SCHEMA foreign_application`,
		`CREATE TABLE mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`,
		`ALTER SCHEMA mycfc_meta OWNER TO recipient_web`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE recipient_migration IN SCHEMA public GRANT SELECT ON TABLES TO PUBLIC`,
	} {
		tx, err := f.root.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		recipientSchemaExec(t, tx, sql)
		if _, err = classify(tx); err == nil {
			t.Fatalf("prepared phase admitted drift %s", sql)
		}
		if err = tx.Rollback(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := InspectTrainingRecipientLocalBootstrapTarget(t.Context(), f.low, "other_target", f.roles.MigrationUsername, f.roles.AppUsername); err == nil {
		t.Fatal("wrong target classified prepared")
	}
	for _, stamp := range [][2]string{{"v99.0.0", ""}, {"", strings.Repeat("c", 40)}, {"v99.0.0", strings.Repeat("c", 40)}} {
		releasecontract.Version, releasecontract.Candidate = stamp[0], stamp[1]
		if _, err := classify(f.root); err == nil {
			t.Fatal("stamped/partially stamped prepared bypass")
		}
	}
}
