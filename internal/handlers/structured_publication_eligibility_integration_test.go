//go:build integration

package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPublicationEligibilityAtBothDatesAndAtomicRecheck(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := PostgresStructuredTrainingStore{Pool: pool}
	actor, current, future, expired := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{actor, current, future, expired} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Publication test',$2,'hash','1990-01-01')`, id, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	var today time.Time
	if err := pool.QueryRow(ctx, `SELECT (clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	season, group, plan, session := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	var programme uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	start := today.AddDate(0, 0, -14)
	end := today.AddDate(0, 0, 30)
	if _, err := pool.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Publication test',$3,$4)`, season, "PE_"+uuid.NewString()[:8], start, end); err != nil {
		t.Fatal(err)
	}
	memberships := map[uuid.UUID]uuid.UUID{}
	for user, dates := range map[uuid.UUID][2]time.Time{current: {start, end}, future: {today.AddDate(0, 0, 1), end}, expired: {start, today}} {
		id := uuid.New()
		memberships[user] = id
		if _, err := pool.Exec(ctx, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,$5,$6)`, id, user, season, programme, dates[0], dates[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Publication group',$2,$3)`, group, programme, actor); err != nil {
		t.Fatal(err)
	}
	for _, id := range memberships {
		if _, err := pool.Exec(ctx, `INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, group, id, actor); err != nil {
			t.Fatal(err)
		}
	}
	monday := today.AddDate(0, 0, -(int(today.Weekday())+6)%7)
	if _, err := pool.Exec(ctx, `INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) VALUES($1,'Publication plan',$2,$3,$4,$5,$6)`, plan, programme, group, season, monday, actor); err != nil {
		t.Fatal(err)
	}
	// Session calendar dates are evaluated in Lisbon even when UTC is still
	// the preceding day (during daylight-saving time).
	lisbon, err := time.LoadLocation("Europe/Lisbon")
	if err != nil {
		t.Fatal(err)
	}
	tomorrow := today.AddDate(0, 0, 1)
	sessionAt := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 0, 30, 0, 0, lisbon)
	if _, err := pool.Exec(ctx, `INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) VALUES($1,$2,'Future training',$3,$4,$5)`, session, plan, sessionAt, sessionAt.Add(time.Hour), actor); err != nil {
		t.Fatal(err)
	}
	recipients, err := store.ListStructuredTrainingPublicationMembers(ctx, dbgen.ListStructuredTrainingPublicationMembersParams{PlanID: plan, TimeZone: "Europe/Lisbon"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 1 || recipients[0].MembershipID != memberships[current] {
		t.Fatalf("preview should contain only both-date eligible recipient: %+v", recipients)
	}
	var source time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM training_plans WHERE id=$1`, plan).Scan(&source); err != nil {
		t.Fatal(err)
	}
	snapshot := []byte(`{"schema_version":1,"session":{"title":"Original"}}`)
	digest := sha256.Sum256(snapshot)
	hash := hex.EncodeToString(digest[:])
	prescription := func(user uuid.UUID) StructuredPrescriptionInput {
		return StructuredPrescriptionInput{SessionID: session, MembershipID: memberships[user], AthleteUserID: user, Snapshot: snapshot, SnapshotSHA256: hash}
	}
	publish := func(items ...StructuredPrescriptionInput) error {
		_, err := store.PublishStructuredTrainingPlan(ctx, StructuredPublicationInput{PlanID: plan, SourceUpdatedAt: pgtype.Timestamptz{Time: source, Valid: true}, ChangeSummary: "Publication test", PublishedByID: actor, Prescriptions: items})
		return err
	}
	for _, user := range []uuid.UUID{future, expired} {
		if err := publish(prescription(user)); !errors.Is(err, errStructuredTrainingPublicationConflict) {
			t.Fatalf("ineligible %s publish err=%v", user, err)
		}
	}
	var publications int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_plan_publications WHERE plan_id=$1`, plan).Scan(&publications); err != nil || publications != 0 {
		t.Fatalf("partial publication %d %v", publications, err)
	}
	missing := prescription(current)
	missing.MembershipID = uuid.New()
	if err := publish(missing); !errors.Is(err, errStructuredTrainingPublicationConflict) {
		t.Fatalf("missing membership accepted: %v", err)
	}
	if _, err := store.PublishStructuredTrainingPlan(ctx, StructuredPublicationInput{PlanID: uuid.New()}); err == nil {
		t.Fatal("missing plan accepted")
	}
	// A database insert fault must be distinct from stale-eligibility conflicts
	// and must leave no publication header behind.
	fn := "fault_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := pool.Exec(ctx, `CREATE FUNCTION `+fn+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic publication fault'; END $$; CREATE TRIGGER `+fn+` BEFORE INSERT ON training_prescriptions FOR EACH ROW EXECUTE FUNCTION `+fn+`() `); err != nil {
		t.Fatal(err)
	}
	faultErr := publish(prescription(current))
	if _, err := pool.Exec(ctx, `DROP TRIGGER `+fn+` ON training_prescriptions; DROP FUNCTION `+fn+`() `); err != nil {
		t.Fatal(err)
	}
	if faultErr == nil || errors.Is(faultErr, errStructuredTrainingPublicationConflict) {
		t.Fatalf("insert failure not propagated: %v", faultErr)
	}
	if _, err := store.PublishStructuredTrainingPlan(ctx, StructuredPublicationInput{PlanID: plan, SourceUpdatedAt: pgtype.Timestamptz{Time: source, Valid: true}, PublishedByID: uuid.New()}); err == nil {
		t.Fatal("missing publisher accepted")
	}
	closed, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	if _, err := (PostgresStructuredTrainingStore{Pool: closed}).PublishStructuredTrainingPlan(ctx, StructuredPublicationInput{}); err == nil {
		t.Fatal("closed database accepted publication")
	}
	// A row lock held by another writer must fail without a partial header.
	limitedConfig, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	limitedConfig.ConnConfig.RuntimeParams["lock_timeout"] = "50ms"
	limited, err := pgxpool.NewWithConfig(ctx, limitedConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer limited.Close()
	held, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := held.Exec(ctx, `SELECT id FROM user_memberships WHERE id=$1 FOR UPDATE`, memberships[current]); err != nil {
		t.Fatal(err)
	}
	_, lockErr := (PostgresStructuredTrainingStore{Pool: limited}).PublishStructuredTrainingPlan(ctx, StructuredPublicationInput{PlanID: plan, SourceUpdatedAt: pgtype.Timestamptz{Time: source, Valid: true}, ChangeSummary: "Lock test", PublishedByID: actor, Prescriptions: []StructuredPrescriptionInput{prescription(current)}})
	if err := held.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if lockErr == nil {
		t.Fatal("locked membership publication accepted")
	}
	// A deferred database failure at COMMIT rolls back the complete header/body.
	commitFn := "fault_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := pool.Exec(ctx, `CREATE FUNCTION `+commitFn+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic commit failure'; END $$; CREATE CONSTRAINT TRIGGER `+commitFn+` AFTER INSERT ON training_plan_publications DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION `+commitFn+`() `); err != nil {
		t.Fatal(err)
	}
	commitErr := publish(prescription(current))
	if _, err := pool.Exec(ctx, `DROP TRIGGER `+commitFn+` ON training_plan_publications; DROP FUNCTION `+commitFn+`() `); err != nil {
		t.Fatal(err)
	}
	if commitErr == nil {
		t.Fatal("failed commit accepted")
	}
	if err := publish(prescription(current)); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	var originalID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT prescription.id FROM training_prescriptions prescription JOIN training_plan_publications publication ON publication.id=prescription.publication_id WHERE publication.plan_id=$1`, plan).Scan(&originalID); err != nil {
		t.Fatal(err)
	}
	// Change group membership between preview and final insert. Failed second insert
	// must roll back even a first valid insertion and the publication header.
	if _, err := pool.Exec(ctx, `DELETE FROM training_group_members WHERE group_id=$1 AND membership_id=$2`, group, memberships[current]); err != nil {
		t.Fatal(err)
	}
	// A fresh valid recipient is inserted first; the stale preview recipient
	// fails second and must roll back the entire new publication.
	added := uuid.New()
	memberships[added] = uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'New recipient',$2,'hash','1990-01-01')`, added, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,$5,$6)`, memberships[added], added, season, programme, today, end); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, group, memberships[added], actor); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM training_plans WHERE id=$1`, plan).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if err := publish(prescription(added), prescription(current)); !errors.Is(err, errStructuredTrainingPublicationConflict) {
		t.Fatalf("stale group recipient err=%v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_plan_publications WHERE plan_id=$1`, plan).Scan(&publications); err != nil || publications != 1 {
		t.Fatalf("partial second publication %d %v", publications, err)
	}
	var persistedID uuid.UUID
	var persistedHash string
	var sameSnapshot bool
	if err := pool.QueryRow(ctx, `SELECT id,snapshot_sha256,snapshot=$2::jsonb FROM training_prescriptions WHERE id=$1`, originalID, string(snapshot)).Scan(&persistedID, &persistedHash, &sameSnapshot); err != nil || persistedID != originalID || persistedHash != hash || !sameSnapshot {
		t.Fatalf("historical snapshot/hash changed: %s %s %v %v", persistedID, persistedHash, sameSnapshot, err)
	}
}
