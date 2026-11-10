//go:build integration

package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type placementFixture struct {
	f                                                 recipientSchemaFixture
	web                                               *pgx.Conn
	actor, subject, programme, season, groupA, groupB uuid.UUID
}

func newPlacementFixture(t *testing.T) placementFixture {
	t.Helper()
	f := newRecipientSchemaFixture(t)
	if _, err := f.low.PgConn().Exec(t.Context(), baselineSchema).ReadAll(); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) {
			pos := int(pe.Position)
			if pos > 0 && pos < len(baselineSchema) {
				a := pos - 120
				if a < 0 {
					a = 0
				}
				b := pos + 120
				if b > len(baselineSchema) {
					b = len(baselineSchema)
				}
				t.Logf("schema position %d snippet %s", pos, baselineSchema[a:b])
			}
			t.Logf("SQL context: %s internal-position:%d internal-query:%s", pe.Where, pe.InternalPosition, pe.InternalQuery)
		}
		t.Fatal("fresh placement schema", err)
	}
	if err := HardenPrivacyExecutionRoles(t.Context(), f.root, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	w := placementFixture{f: f, web: f.web(t), actor: uuid.New(), subject: uuid.New(), season: uuid.New(), groupA: uuid.New(), groupB: uuid.New()}
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic placement coach',$2,'hash','1980-01-01'),($3,'Synthetic placement athlete',$4,'hash','1990-01-01')`, w.actor, w.actor.String()+"@example.test", w.subject, w.subject.String()+"@example.test")
	recipientSchemaExec(t, w.web, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, w.actor)
	recipientSchemaExec(t, w.web, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,'SYN_PLACEMENT','Synthetic placement season',CURRENT_DATE-365,CURRENT_DATE+400)`, w.season)
	if err := w.web.QueryRow(t.Context(), `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&w.programme); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Synthetic placement A',$3,$4),($2,'Synthetic placement B',$3,$4)`, w.groupA, w.groupB, w.programme, w.actor)
	recipientSchemaExec(t, w.web, `INSERT INTO user_memberships(user_id,programme_id,season_id,starts_on,ends_on) VALUES($1,$2,$3,CURRENT_DATE-20,CURRENT_DATE+300)`, w.subject, w.programme, w.season)
	s, err := TrainingScheduleStatus(t.Context(), f.low)
	if err != nil {
		t.Fatal(err)
	}
	var epoch int64
	if err = f.low.QueryRow(t.Context(), `SELECT training_schedule_control.commit_floor($1,$2,$3,$4,4)`, s.DatabaseIncarnation, s.StateEpoch, uuid.New(), strings.Repeat("a", 64)).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	return w
}
func (w placementFixture) revision(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := w.web.QueryRow(t.Context(), `SELECT COALESCE((SELECT revision FROM training_placement_heads WHERE subject_user_id=$1 AND programme_id=$2),0)`, w.subject, w.programme).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestTrainingPlacementFreshSchemaJoin(t *testing.T) {
	w := newPlacementFixture(t)
	tx, err := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var change, source, destination, ret *uuid.UUID
	var revision int64
	var replayed bool
	err = tx.QueryRow(t.Context(), `SELECT * FROM apply_training_placement_change($1,$2,$3,$4,$5,'JOIN',NULL,$6,CURRENT_DATE+1,NULL,$7,repeat('c',64))`, w.actor, w.subject, w.programme, w.season, uuid.New(), w.groupA, w.revision(t)).Scan(&change, &revision, &source, &destination, &ret, &replayed)
	if err != nil {
		t.Fatal("join", err)
	}
	if change == nil || destination == nil || source != nil || ret != nil || replayed {
		t.Fatal("invalid join result")
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal("commit join", err)
	}
	var count int
	if err = w.web.QueryRow(t.Context(), `SELECT count(*) FROM training_placement_resolve($1,$2,$3,CURRENT_DATE+1)`, w.subject, w.groupA, w.season).Scan(&count); err != nil || count != 1 {
		t.Fatalf("resolver count %d: %v", count, err)
	}
}

func (w placementFixture) day(t *testing.T, offset int) pgtype.Date {
	t.Helper()
	var d pgtype.Date
	if err := w.web.QueryRow(t.Context(), `SELECT (clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+$1::integer`, offset).Scan(&d); err != nil {
		t.Fatal(err)
	}
	return d
}
func (w placementFixture) apply(t *testing.T, in dbgen.ApplyTrainingPlacementChangeParams) (dbgen.ApplyTrainingPlacementChangeRow, error) {
	t.Helper()
	tx, err := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return dbgen.ApplyTrainingPlacementChangeRow{}, err
	}
	defer tx.Rollback(context.Background())
	out, err := dbgen.New(tx).ApplyTrainingPlacementChange(t.Context(), in)
	if err != nil {
		return out, err
	}
	err = tx.Commit(t.Context())
	return out, err
}
func (w placementFixture) input(t *testing.T, kind string, source, target *uuid.UUID, on, until int) dbgen.ApplyTrainingPlacementChangeParams {
	t.Helper()
	in := dbgen.ApplyTrainingPlacementChangeParams{ActorID: w.actor, SubjectID: w.subject, ProgrammeID: w.programme, SeasonID: w.season, CommandID: uuid.New(), Kind: kind, SourcePlacementID: source, TargetGroupID: target, EffectiveOn: w.day(t, on), ExpectedRevision: w.revision(t), ReviewSha256: strings.Repeat("c", 64)}
	if until != 0 {
		in.TemporaryUntil = w.day(t, until)
	}
	return in
}
func (w placementFixture) aggregate(t *testing.T) string {
	t.Helper()
	var raw string
	if err := w.web.QueryRow(t.Context(), `SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(h) ORDER BY subject_user_id,programme_id) FROM training_placement_heads h),
 (SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM training_placement_changes c),
 (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM training_group_placements p),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY prescription_id,change_id,leg) FROM training_prescription_withdrawals x))::text`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestTrainingPlacementTemporaryReplayAndSourceStartCancellation(t *testing.T) {
	w := newPlacementFixture(t)
	joinIn := w.input(t, "JOIN", nil, &w.groupA, 2, 0)
	joined, err := w.apply(t, joinIn)
	if err != nil {
		t.Fatal(err)
	}
	if joined.DestinationPlacementID == uuid.Nil || joined.SourcePlacementID != uuid.Nil || joined.ReturnPlacementID != uuid.Nil {
		t.Fatal("typed nullable IDs not represented by zero UUID")
	}
	moveIn := w.input(t, "TEMPORARY", &joined.DestinationPlacementID, &w.groupB, 2, 5)
	moved, err := w.apply(t, moveIn)
	if err != nil {
		t.Fatal("source-start temporary move", err)
	}
	if moved.SourcePlacementID != joined.DestinationPlacementID || moved.ReturnPlacementID == uuid.Nil || moved.DestinationPlacementID == uuid.Nil {
		t.Fatal("missing exact legs")
	}
	var valid bool
	err = w.web.QueryRow(t.Context(), `SELECT p.starts_on=p.ends_on OR p.ends_on IS NULL OR p.starts_on<p.ends_on FROM training_group_placements p WHERE p.id=$1`, joined.DestinationPlacementID).Scan(&valid)
	if err != nil || !valid {
		t.Fatal("superseded source range invalid", err)
	}
	var returned uuid.UUID
	err = w.web.QueryRow(t.Context(), `SELECT placement_id FROM training_placement_resolve($1,$2,$3,$4::date+1)`, w.subject, w.groupA, w.season, moveIn.TemporaryUntil).Scan(&returned)
	if err != nil || returned != moved.ReturnPlacementID {
		t.Fatal("exact previous-group return missing", err)
	}
	before := w.aggregate(t)
	replay, err := w.apply(t, moveIn)
	if err != nil || !replay.Replayed || replay.HeadRevision != moved.HeadRevision || replay.DestinationPlacementID != moved.DestinationPlacementID || replay.ReturnPlacementID != moved.ReturnPlacementID {
		t.Fatal("exact replay result mismatch", err)
	}
	if before != w.aggregate(t) {
		t.Fatal("replay changed head/audit/placements/tickets")
	}
	changed := moveIn
	changed.EffectiveOn = w.day(t, 3)
	if _, err = w.apply(t, changed); !sqlState(err, "40001") {
		t.Fatal("changed canonical replay not refused", err)
	}
	if before != w.aggregate(t) {
		t.Fatal("failed replay changed aggregate")
	}
	tx, err := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	cancellationID := uuid.New()
	cancelInput := dbgen.CancelTrainingPlacementChangeParams{ActorID: w.actor, CommandID: moved.ChangeID, CancellationID: cancellationID, ExpectedRevision: w.revision(t), ReviewSha256: strings.Repeat("c", 64)}
	cancelled, err := dbgen.New(tx).CancelTrainingPlacementChange(t.Context(), cancelInput)
	if err != nil {
		tx.Rollback(t.Context())
		t.Fatal("cancel", err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if cancelled.SourcePlacementID != joined.DestinationPlacementID {
		t.Fatal("cancel restored wrong source")
	}
	err = w.web.QueryRow(t.Context(), `SELECT placement_id FROM training_placement_resolve($1,$2,$3,$4)`, w.subject, w.groupA, w.season, w.day(t, 3)).Scan(&returned)
	if err != nil || returned != joined.DestinationPlacementID {
		t.Fatal("source-start cancellation failed restoration", err)
	}
	before = w.aggregate(t)
	cancelledReplay, err := dbgen.New(w.web).CancelTrainingPlacementChange(t.Context(), cancelInput)
	if err != nil || !cancelledReplay.Replayed || cancelledReplay.ChangeID != cancellationID {
		t.Fatal("cancel replay failed", err)
	}
	if before != w.aggregate(t) {
		t.Fatal("cancel replay changed aggregate")
	}
	var containsIdentity bool
	err = w.web.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM training_placement_changes WHERE canonical_command::text LIKE '%'||$1::text||'%' OR canonical_command::text LIKE '%'||$2::text||'%')`, w.subject, w.actor).Scan(&containsIdentity)
	if err != nil || containsIdentity {
		t.Fatal("canonical payload retained direct identity", err)
	}
}
func TestTrainingPlacementAuthorityScopeAndNonoverlap(t *testing.T) {
	w := newPlacementFixture(t)
	joined, err := w.apply(t, w.input(t, "JOIN", nil, &w.groupA, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	before := w.aggregate(t)
	if _, err = w.apply(t, w.input(t, "JOIN", nil, &w.groupB, 2, 0)); !sqlState(err, "23P01") {
		t.Fatal("second active group admitted", err)
	}
	if before != w.aggregate(t) {
		t.Fatal("overlap refusal changed aggregate")
	}
	stranger := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic unrelated coach',$2,'hash','1980-01-01')`, stranger, stranger.String()+"@example.test")
	in := w.input(t, "MOVE", &joined.DestinationPlacementID, &w.groupB, 3, 0)
	in.ActorID = stranger
	if _, err = w.apply(t, in); !sqlState(err, "42501") {
		t.Fatal("unrelated coach moved athlete", err)
	}
	if before != w.aggregate(t) {
		t.Fatal("authority refusal changed aggregate")
	}
	programmeOther := uuid.New()
	groupOther := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO programmes(id,code,name_pt) VALUES($1,$2,'Synthetic other scope')`, programmeOther, "SYN_"+programmeOther.String()[:8])
	recipientSchemaExec(t, w.web, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Synthetic wrong scope',$2,$3)`, groupOther, programmeOther, w.actor)
	in = w.input(t, "MOVE", &joined.DestinationPlacementID, &groupOther, 3, 0)
	if _, err = w.apply(t, in); !sqlState(err, "42501") {
		t.Fatal("mixed programme group admitted", err)
	}
	if before != w.aggregate(t) {
		t.Fatal("scope refusal changed aggregate")
	}
}

type placementInstruction struct{ plan, session, publication, prescription, membership uuid.UUID }

func (w placementFixture) instruction(t *testing.T, group, placement uuid.UUID, offset int) placementInstruction {
	t.Helper()
	r := placementInstruction{plan: uuid.New(), session: uuid.New(), publication: uuid.New(), prescription: uuid.New()}
	if err := w.web.QueryRow(t.Context(), `SELECT id FROM user_memberships WHERE user_id=$1 AND season_id=$2 ORDER BY starts_on LIMIT 1`, w.subject, w.season).Scan(&r.membership); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) VALUES($1,'Synthetic placement instruction week',$2,$3,$4,date_trunc('week',$5::date::timestamp)::date,$6) ON CONFLICT(training_group_id,week_start) DO NOTHING`, r.plan, w.programme, group, w.season, w.day(t, offset), w.actor)
	if err := w.web.QueryRow(t.Context(), `SELECT id FROM training_plans WHERE training_group_id=$1 AND week_start=date_trunc('week',$2::date::timestamp)::date`, group, w.day(t, offset)).Scan(&r.plan); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, w.web, `INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) VALUES($1,$2,'Synthetic placement instruction',($3::date::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours',($3::date::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours',$4)`, r.session, r.plan, w.day(t, offset), w.actor)
	recipientSchemaExec(t, w.web, `INSERT INTO training_plan_publications(id,plan_id,revision,source_updated_at,change_summary,published_by_id,supersedes_id) SELECT $1,plan.id,COALESCE(latest.revision,0)+1,plan.updated_at,'Synthetic explicit publication',$3,latest.id FROM training_plans plan LEFT JOIN LATERAL(SELECT p.id,p.revision FROM training_plan_publications p WHERE p.plan_id=plan.id ORDER BY p.revision DESC LIMIT 1) latest ON true WHERE plan.id=$2`, r.publication, r.plan, w.actor)
	raw := `{"schema_version":1,"session":{"EntryKind":"WORKOUT"}}`
	sum := sha256.Sum256([]byte(raw))
	recipientSchemaExec(t, w.web, `INSERT INTO training_prescriptions(id,publication_id,session_id,membership_id,athlete_user_id,placement_id,snapshot,snapshot_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, r.prescription, r.publication, r.session, r.membership, w.subject, placement, raw, hex.EncodeToString(sum[:]))
	return r
}
func (w placementFixture) delivery(t *testing.T, id uuid.UUID, want bool) {
	t.Helper()
	var allowed bool
	if err := w.web.QueryRow(t.Context(), `SELECT training_prescription_current_delivery($1)`, id).Scan(&allowed); err != nil || allowed != want {
		t.Fatalf("delivery %v want %v: %v", allowed, want, err)
	}
}
func TestTrainingPlacementWithdrawalDirectReadOutcomeAndExplicitReturn(t *testing.T) {
	w := newPlacementFixture(t)
	joined, err := w.apply(t, w.input(t, "JOIN", nil, &w.groupA, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	old := w.instruction(t, w.groupA, joined.DestinationPlacementID, 7)
	w.delivery(t, old.prescription, true)
	q := dbgen.New(w.web)
	if _, err = q.GetTrainingPrescriptionForViewer(t.Context(), dbgen.GetTrainingPrescriptionForViewerParams{ID: old.prescription, UserID: w.subject}); err != nil {
		t.Fatal("initial direct access", err)
	}
	var oldBytes string
	if err = w.web.QueryRow(t.Context(), `SELECT to_jsonb(p)::text FROM training_prescriptions p WHERE id=$1`, old.prescription).Scan(&oldBytes); err != nil {
		t.Fatal(err)
	}
	moved, err := w.apply(t, w.input(t, "TEMPORARY", &joined.DestinationPlacementID, &w.groupB, 0, 2))
	if err != nil {
		t.Fatal("effective-today future-only move", err)
	}
	w.delivery(t, old.prescription, false)
	if _, err = q.GetTrainingPrescriptionForViewer(t.Context(), dbgen.GetTrainingPrescriptionForViewerParams{ID: old.prescription, UserID: w.subject}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("withdrawn direct ID exposed", err)
	}
	if n, e := q.SaveTrainingSessionOutcome(t.Context(), dbgen.SaveTrainingSessionOutcomeParams{SessionID: old.session, UserID: w.subject, Status: dbgen.TrainingOutcomeStatusCOMPLETED}); e != nil || n != 0 {
		t.Fatal("withdrawn grouped outcome fallback", n, e)
	}
	returned := w.instruction(t, w.groupA, moved.ReturnPlacementID, 7)
	w.delivery(t, returned.prescription, true)
	w.delivery(t, old.prescription, false)
	var still string
	if err = w.web.QueryRow(t.Context(), `SELECT to_jsonb(p)::text FROM training_prescriptions p WHERE id=$1`, old.prescription).Scan(&still); err != nil || still != oldBytes {
		t.Fatal("old immutable instruction changed", err)
	}
	recipientSchemaExec(t, w.web, `UPDATE training_sessions SET starts_at=starts_at+interval '1 hour',ends_at=ends_at+interval '1 hour',updated_at=clock_timestamp() WHERE id=$1`, old.session)
	w.delivery(t, old.prescription, false)
	// Restore/live references may be detached only by the established privileged operation, never an app GUC.
	tx, err := w.web.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, tx, `SET LOCAL mycfc.privacy_erasure_operation='TRAINING_PRESCRIPTION_DELETE'`)
	_, err = tx.Exec(t.Context(), `UPDATE training_prescription_withdrawals SET live_prescription_id=NULL WHERE prescription_id=$1`, old.prescription)
	if !sqlState(err, "42501") {
		t.Fatal("ordinary marker detached denial", err)
	}
	_ = tx.Rollback(t.Context())
}
func TestTrainingPlacementPreEffectiveTicketsAndClassificationContinuation(t *testing.T) {
	w := newPlacementFixture(t)
	joined, err := w.apply(t, w.input(t, "JOIN", nil, &w.groupA, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	old := w.instruction(t, w.groupA, joined.DestinationPlacementID, 7)
	moved, err := w.apply(t, w.input(t, "TEMPORARY", &joined.DestinationPlacementID, &w.groupB, 2, 4))
	if err != nil {
		t.Fatal(err)
	}
	_ = moved
	w.delivery(t, old.prescription, true)
	// A next-day category-equivalent episode keeps person placement. The old FK stays pinned.
	tx, err := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, tx, `UPDATE user_memberships SET ends_on=(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date WHERE id=$1`, old.membership)
	recipientSchemaExec(t, tx, `INSERT INTO user_memberships(user_id,programme_id,season_id,starts_on,ends_on) VALUES($1,$2,$3,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+1,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+300)`, w.subject, w.programme, w.season)
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal("exact successor with canonical withdrawal", err)
	}
	w.delivery(t, old.prescription, true)
	var pinned uuid.UUID
	if err = w.web.QueryRow(t.Context(), `SELECT membership_id FROM training_prescriptions WHERE id=$1`, old.prescription).Scan(&pinned); err != nil || pinned != old.membership {
		t.Fatal("classification rewrote immutable membership", err)
	}
}

func TestTrainingPlacementReadQueryAPI(t *testing.T) {
	w := newPlacementFixture(t)
	q := dbgen.New(w.web)
	reviewed, err := q.TrainingPlacementSubjectForReview(t.Context(), dbgen.TrainingPlacementSubjectForReviewParams{ActorID: w.actor, SubjectID: w.subject, GroupID: w.groupA, SeasonID: w.season, EffectiveOn: w.day(t, 1)})
	if err != nil || reviewed.Revision != w.revision(t) || reviewed.SubjectID != w.subject || !reviewed.DecisionAt.Valid || reviewed.MembershipID == uuid.Nil {
		t.Fatal("actual scoped head/subject review", err)
	}
	groups, err := q.TrainingPlacementGroupChoices(t.Context(), dbgen.TrainingPlacementGroupChoicesParams{ActorID: w.actor, ProgrammeID: w.programme, SeasonID: w.season, RowLimit: 101})
	if err != nil || len(groups) != 2 {
		t.Fatal("actual scoped explicit-season choices", len(groups), err)
	}
	joined, err := w.apply(t, w.input(t, "JOIN", nil, &w.groupA, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	sourceReview, err := q.TrainingPlacementSourceForReview(t.Context(), dbgen.TrainingPlacementSourceForReviewParams{ActorID: w.actor, SourcePlacementID: joined.DestinationPlacementID})
	if err != nil || sourceReview.SubjectID != w.subject || sourceReview.ID != joined.DestinationPlacementID || sourceReview.Revision != w.revision(t) {
		t.Fatal("actual source-only review", err)
	}
	changeReview, err := q.TrainingPlacementChangeForReview(t.Context(), dbgen.TrainingPlacementChangeForReviewParams{ActorID: w.actor, ChangeID: joined.ChangeID})
	if err != nil || changeReview.ID != joined.ChangeID || changeReview.DestinationPlacementID == nil || *changeReview.DestinationPlacementID != joined.DestinationPlacementID || changeReview.Revision != w.revision(t) {
		t.Fatal("actual change review", err)
	}
	returnFacts, err := q.TrainingPlacementReturnForReview(t.Context(), dbgen.TrainingPlacementReturnForReviewParams{ActorID: w.actor, SubjectID: w.subject, GroupID: w.groupA, ReturnOn: w.day(t, 5)})
	if err != nil || len(returnFacts) != 1 || returnFacts[0].SeasonID != w.season {
		t.Fatal("actual return availability", err)
	}
	timeline, err := q.TrainingPlacementTimeline(t.Context(), dbgen.TrainingPlacementTimelineParams{ActorID: w.actor, SubjectID: w.subject, ProgrammeID: w.programme, RowLimit: 101})
	if err != nil || len(timeline) != 1 || timeline[0].ID != joined.DestinationPlacementID || timeline[0].Revision != w.revision(t) {
		t.Fatal("actual scoped timeline", err)
	}
	instruction := w.instruction(t, w.groupA, joined.DestinationPlacementID, 5)
	instructionReview, err := q.TrainingPlacementInstructionReview(t.Context(), dbgen.TrainingPlacementInstructionReviewParams{ActorID: w.actor, SourcePlacementID: joined.DestinationPlacementID, EffectiveOn: w.day(t, 1)})
	if err != nil || len(instructionReview) != 1 || instructionReview[0].PrescriptionID != instruction.prescription || !instructionReview[0].WillWithdraw || instructionReview[0].RequiresPolicy {
		t.Fatal("actual affected instruction review", err)
	}
	tx, err := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	recipientSchemaExec(t, tx, `SELECT id FROM training_plans WHERE id=$1 FOR UPDATE`, instruction.plan)
	heads, err := dbgen.New(tx).LockPublicationPlacementHeads(t.Context(), dbgen.LockPublicationPlacementHeadsParams{ActorID: w.actor, PlanID: instruction.plan})
	if err != nil || len(heads) != 1 || heads[0].SubjectUserID != w.subject || heads[0].ProgrammeID != w.programme {
		t.Fatal("actual structural head query", err)
	}
}

func (w placementFixture) privacyOperator(t *testing.T, database string, create bool) *pgx.Conn {
	t.Helper()
	if create {
		recipientSchemaExec(t, w.f.root, `CREATE ROLE placement_privacy_proof LOGIN PASSWORD 'synthetic-privacy-proof' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT`)
		recipientSchemaExec(t, w.f.root, `GRANT CONNECT ON DATABASE mycfc TO placement_privacy_proof`)
		recipientSchemaExec(t, w.f.low, `GRANT USAGE ON SCHEMA public TO placement_privacy_proof`)
		recipientSchemaExec(t, w.f.low, `GRANT EXECUTE ON FUNCTION public.privacy_restore_apply_relational_operation(uuid,uuid,timestamptz,text),public.training_placement_history_canonical(uuid[],uuid[],uuid) TO placement_privacy_proof`)
	}
	cfg, err := pgx.ParseConfig(w.f.url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.User = "placement_privacy_proof"
	cfg.Password = "synthetic-privacy-proof"
	if database != "" {
		cfg.Database = database
	}
	c, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	var super bool
	if err = c.QueryRow(t.Context(), `SELECT rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&super); err != nil || super {
		t.Fatal("privacy proof must use non-superuser delegate", err)
	}
	return c
}
func TestTrainingPlacementPrivateUnlinkDeletionAndPopulatedRestore(t *testing.T) {
	w := newPlacementFixture(t)
	joined, err := w.apply(t, w.input(t, "JOIN", nil, &w.groupA, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	old := w.instruction(t, w.groupA, joined.DestinationPlacementID, 7)
	if _, err = w.apply(t, w.input(t, "TEMPORARY", &joined.DestinationPlacementID, &w.groupB, 0, 2)); err != nil {
		t.Fatal(err)
	}
	var placementIDs, changeIDs []uuid.UUID
	if err = w.web.QueryRow(t.Context(), `SELECT array_agg(id ORDER BY id) FROM training_group_placements`).Scan(&placementIDs); err != nil {
		t.Fatal(err)
	}
	if err = w.web.QueryRow(t.Context(), `SELECT array_agg(id ORDER BY id) FROM training_placement_changes`).Scan(&changeIDs); err != nil {
		t.Fatal(err)
	}
	// Ordinary app SQL cannot forge a protected fence or call the private executor.
	if _, err = w.web.Exec(t.Context(), `INSERT INTO privacy_protected.training_placement_erasure_fence(transaction_id,subject_user_id,operation,effective_date) VALUES(txid_current(),$1,'MEMBERSHIP_ACTIVE_REVOKE',CURRENT_DATE)`, w.subject); !sqlState(err, "42501") {
		t.Fatal("ordinary fence forgery", err)
	}
	if _, err = w.web.Exec(t.Context(), `SELECT public.privacy_restore_apply_relational_operation($1,$2,clock_timestamp(),'MEMBERSHIP_HISTORY_ANONYMIZE')`, uuid.New(), w.subject); !sqlState(err, "42501") {
		t.Fatal("ordinary private executor invocation", err)
	}
	op := w.privacyOperator(t, "", true)
	// Populate a genuinely new owned restore database from this fixture's pre-erasure dump.
	dump, err := exec.CommandContext(t.Context(), "docker", "exec", w.f.container, "pg_dump", "-U", "acceptance", "-d", "mycfc", "-Fc").Output()
	if err != nil {
		t.Fatal("owned populated dump", err)
	}
	recipientSchemaExec(t, w.f.root, `CREATE DATABASE mycfc_placement_replay OWNER recipient_migration`)
	recipientSchemaExec(t, w.f.root, `GRANT CONNECT ON DATABASE mycfc_placement_replay TO placement_privacy_proof`)
	restore := exec.CommandContext(t.Context(), "docker", "exec", "-i", w.f.container, "pg_restore", "-U", "acceptance", "-d", "mycfc_placement_replay", "--exit-on-error")
	restore.Stdin = bytes.NewReader(dump)
	if raw, e := restore.CombinedOutput(); e != nil {
		t.Fatalf("owned populated restore: %v %s", e, raw)
	}
	replayOp := w.privacyOperator(t, "mycfc_placement_replay", false)
	// This is a focused private-relational primitive proof. The fixture grants only
	// this existing helper to a non-superuser; it does not claim lease/activation or
	// whole external tombstone-envelope acceptance.
	var originalCanonical []byte
	for i, c := range []*pgx.Conn{op, replayOp} {
		runID := uuid.New()
		for _, operation := range []string{"TRAINING_PRESCRIPTION_DELETE", "MEMBERSHIP_ACTIVE_REVOKE", "MEMBERSHIP_HISTORY_ANONYMIZE"} {
			if _, e := c.Exec(t.Context(), `SELECT public.privacy_restore_apply_relational_operation($1,$2,clock_timestamp(),$3)`, runID, w.subject, operation); e != nil {
				t.Fatal("actual private relational operation", operation, e)
			}
		}
		cfg, err := pgx.ParseConfig(w.f.url)
		if err != nil {
			t.Fatal(err)
		}
		cfg.User = w.f.roles.MigrationUsername
		cfg.Password = w.f.roles.MigrationPassword
		if i == 1 {
			cfg.Database = "mycfc_placement_replay"
		}
		inspector, e := pgx.ConnectConfig(t.Context(), cfg)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = inspector.Close(context.Background()) })
		var principal uuid.UUID
		var count int
		var live *uuid.UUID
		if e = inspector.QueryRow(t.Context(), `SELECT subject_principal_id FROM training_group_placements WHERE id=$1`, joined.DestinationPlacementID).Scan(&principal); e != nil {
			t.Fatal(e)
		}
		if e = inspector.QueryRow(t.Context(), `SELECT count(*) FROM training_placement_heads WHERE subject_user_id=$1`, w.subject).Scan(&count); e != nil || count != 0 {
			t.Fatal("operational head survived unlink", e)
		}
		if e = inspector.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM training_group_placements WHERE subject_user_id=$1)+(SELECT count(*) FROM training_placement_changes WHERE subject_user_id=$1)`, w.subject).Scan(&count); e != nil || count != 0 {
			t.Fatal("subject link survived unlink", e)
		}
		if e = inspector.QueryRow(t.Context(), `SELECT live_prescription_id FROM training_prescription_withdrawals WHERE prescription_id=$1`, old.prescription).Scan(&live); e != nil || live != nil {
			t.Fatal("approved delete did not detach live reference", e)
		}
		if e = inspector.QueryRow(t.Context(), `SELECT count(*) FROM privacy_protected.training_placement_erasure_fence`).Scan(&count); e != nil || count != 0 {
			t.Fatal("protected proof persisted after operation", e)
		}
		var retainedCommands string
		if e = inspector.QueryRow(t.Context(), `SELECT COALESCE(string_agg(canonical_command::text,','),'') FROM training_placement_changes WHERE id=ANY($1::uuid[])`, changeIDs).Scan(&retainedCommands); e != nil || strings.Contains(retainedCommands, w.subject.String()) || strings.Contains(retainedCommands, w.actor.String()) {
			t.Fatal("retained canonical command embeds direct person identity", e)
		}
		var canonical []byte
		if e = c.QueryRow(t.Context(), `SELECT public.training_placement_history_canonical($1,$2,$3)`, placementIDs, changeIDs, principal).Scan(&canonical); e != nil {
			t.Fatal("canonical placement proof", e)
		}
		if i == 0 {
			originalCanonical = canonical
		} else if !bytes.Equal(originalCanonical, canonical) {
			t.Fatal("private unlink/restore changed stable canonical denial history")
		}
		var missing bool
		if e = inspector.QueryRow(t.Context(), `SELECT NOT EXISTS(SELECT 1 FROM training_prescriptions WHERE id=$1)`, old.prescription).Scan(&missing); e != nil || !missing {
			t.Fatal("approved deletion blocked", e)
		}
		// Replaying an already-applied private relation operation must preserve facts.
		if _, e = c.Exec(t.Context(), `SELECT public.privacy_restore_apply_relational_operation($1,$2,clock_timestamp(),'MEMBERSHIP_HISTORY_ANONYMIZE')`, runID, w.subject); e != nil {
			t.Fatal("private replay idempotency", e)
		}
		if e = c.QueryRow(t.Context(), `SELECT public.training_placement_history_canonical($1,$2,$3)`, placementIDs[1:], changeIDs, principal).Scan(&canonical); !sqlState(e, "P0001") {
			t.Fatal("omitted closure admitted", e)
		}
	}
}

func TestTrainingPlacementFutureDateDiscoveryAndGuardianDenial(t *testing.T) {
	w := newPlacementFixture(t)
	q := dbgen.New(w.web)
	future, minor, stranger := uuid.New(), uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic future eligible athlete',$2,'hash','1990-01-01'),($3,'Synthetic unrelated reader',$4,'hash','1980-01-01')`, future, future.String()+"@example.test", stranger, stranger.String()+"@example.test")
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,is_dependent,date_of_birth) VALUES($1,'Synthetic future unverified minor',true,'2015-01-01')`, minor)
	recipientSchemaExec(t, w.web, `INSERT INTO user_memberships(user_id,programme_id,season_id,starts_on,ends_on) VALUES($1,$3,$4,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+5,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+6),($1,$3,$4,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+7,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+100),($2,$3,$4,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+5,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+100)`, future, minor, w.programme, w.season)
	in := dbgen.TrainingGroupAthleteChoicesForDateParams{ActorID: w.actor, GroupID: w.groupA, SeasonID: w.season, EffectiveOn: w.day(t, 0), NameQuery: "Synthetic future"}
	found, err := q.TrainingGroupAthleteChoicesForDate(t.Context(), in)
	if err != nil || len(found) != 0 {
		t.Fatal("future-only person leaked into today", len(found), err)
	}
	in.EffectiveOn = w.day(t, 7)
	found, err = q.TrainingGroupAthleteChoicesForDate(t.Context(), in)
	if err != nil || len(found) != 1 || found[0].UserID != future || found[0].AlreadyMember {
		t.Fatal("future actual episode/guardian filter", found, err)
	}
	reviewed, err := q.TrainingPlacementSubjectForReview(t.Context(), dbgen.TrainingPlacementSubjectForReviewParams{ActorID: w.actor, SubjectID: future, GroupID: w.groupA, SeasonID: w.season, EffectiveOn: in.EffectiveOn})
	if err != nil || reviewed.MembershipID != found[0].MembershipID {
		t.Fatal("selected future episode mismatch", err)
	}
	command := dbgen.ApplyTrainingPlacementChangeParams{ActorID: w.actor, SubjectID: future, ProgrammeID: w.programme, SeasonID: w.season, CommandID: uuid.New(), Kind: "JOIN", TargetGroupID: &w.groupA, EffectiveOn: in.EffectiveOn, ExpectedRevision: reviewed.Revision, ReviewSha256: strings.Repeat("c", 64)}
	if _, err = w.apply(t, command); err != nil {
		t.Fatal("known future-only eligible assignment", err)
	}
	found, err = q.TrainingGroupAthleteChoicesForDate(t.Context(), in)
	if err != nil || len(found) != 1 || !found[0].AlreadyMember {
		t.Fatal("future placement membership projection", err)
	}
	in.ActorID = stranger
	if found, err = q.TrainingGroupAthleteChoicesForDate(t.Context(), in); err != nil || len(found) != 0 {
		t.Fatal("unrelated reader discovered future person", err)
	}
	in.ActorID = w.actor
	in.EffectiveOn = w.day(t, 500)
	if found, err = q.TrainingGroupAthleteChoicesForDate(t.Context(), in); err != nil || len(found) != 0 {
		t.Fatal("outside configured season discovery", err)
	}
	in.EffectiveOn = w.day(t, 7)
	in.NameQuery = ""
	if found, err = q.TrainingGroupAthleteChoicesForDate(t.Context(), in); err != nil || len(found) != 0 {
		t.Fatal("blank future directory disclosure", err)
	}
}

func placementReplayInput(in dbgen.ApplyTrainingPlacementChangeParams) dbgen.TrainingPlacementApplyReplayParams {
	return dbgen.TrainingPlacementApplyReplayParams{ActorID: in.ActorID, SubjectID: in.SubjectID, ProgrammeID: in.ProgrammeID, SeasonID: in.SeasonID, CommandID: in.CommandID, Kind: in.Kind, SourcePlacementID: in.SourcePlacementID, TargetGroupID: in.TargetGroupID, EffectiveOn: in.EffectiveOn, TemporaryUntil: in.TemporaryUntil, ExpectedRevision: in.ExpectedRevision, ReviewSha256: in.ReviewSha256}
}
func TestTrainingPlacementReadOnlyExactProofReplay(t *testing.T) {
	w := newPlacementFixture(t)
	q := dbgen.New(w.web)
	in := w.input(t, "JOIN", nil, &w.groupA, 1, 0)
	probe := placementReplayInput(in)
	before := w.aggregate(t)
	if _, err := q.TrainingPlacementApplyReplay(t.Context(), probe); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("uncommitted replay probe", err)
	}
	if before != w.aggregate(t) {
		t.Fatal("readonly missing replay changed aggregate")
	}
	saved, err := w.apply(t, in)
	if err != nil {
		t.Fatal(err)
	}
	before = w.aggregate(t)
	result, err := q.TrainingPlacementApplyReplay(t.Context(), probe)
	if err != nil || !result.Replayed || result.ChangeID != saved.ChangeID || result.HeadRevision != saved.HeadRevision || result.DestinationPlacementID != saved.DestinationPlacementID {
		t.Fatal("exact readonly replay", err)
	}
	changedProof := probe
	changedProof.ReviewSha256 = strings.Repeat("b", 64)
	if _, err = q.TrainingPlacementApplyReplay(t.Context(), changedProof); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("different proof fingerprint disclosed result", err)
	}
	changed := in
	changed.ReviewSha256 = changedProof.ReviewSha256
	if _, err = w.apply(t, changed); !sqlState(err, "40001") {
		t.Fatal("different proof reused committed command", err)
	}
	changedSubject := probe
	changedSubject.SubjectID = uuid.New()
	if _, err = q.TrainingPlacementApplyReplay(t.Context(), changedSubject); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("foreign subject replay", err)
	}
	changedActor := probe
	changedActor.ActorID = uuid.New()
	if _, err = q.TrainingPlacementApplyReplay(t.Context(), changedActor); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("foreign actor replay", err)
	}
	if before != w.aggregate(t) {
		t.Fatal("readonly replay/negative changed aggregate")
	}
	cancelIn := dbgen.CancelTrainingPlacementChangeParams{ActorID: w.actor, CommandID: saved.ChangeID, CancellationID: uuid.New(), ExpectedRevision: w.revision(t), ReviewSha256: strings.Repeat("d", 64)}
	cancelProbe := dbgen.TrainingPlacementCancelReplayParams{ActorID: cancelIn.ActorID, CommandID: cancelIn.CommandID, CancellationID: cancelIn.CancellationID, ExpectedRevision: cancelIn.ExpectedRevision, ReviewSha256: cancelIn.ReviewSha256}
	if _, err = q.TrainingPlacementCancelReplay(t.Context(), cancelProbe); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("uncommitted cancel probe", err)
	}
	tx, err := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := dbgen.New(tx).CancelTrainingPlacementChange(t.Context(), cancelIn)
	if err != nil {
		_ = tx.Rollback(t.Context())
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	before = w.aggregate(t)
	cancelledResult, err := q.TrainingPlacementCancelReplay(t.Context(), cancelProbe)
	if err != nil || !cancelledResult.Replayed || cancelledResult.ChangeID != cancelled.ChangeID || cancelledResult.HeadRevision != cancelled.HeadRevision {
		t.Fatal("exact cancel replay", err)
	}
	cancelProbe.ReviewSha256 = strings.Repeat("e", 64)
	if _, err = q.TrainingPlacementCancelReplay(t.Context(), cancelProbe); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("different cancel proof disclosed result", err)
	}
	if before != w.aggregate(t) {
		t.Fatal("readonly cancel replay changed aggregate")
	}
}
func TestTrainingPlacementPrivacyGUCsCannotChangeCommandSemantics(t *testing.T) {
	w := newPlacementFixture(t)
	joined, err := w.apply(t, w.input(t, "JOIN", nil, &w.groupA, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	var applyDelta, cancelDelta int64
	for i, marker := range []string{"", "MEMBERSHIP_HISTORY_ANONYMIZE", "MEMBERSHIP_ACTIVE_REVOKE", "AUDIT_ACTOR_ANONYMIZE", "TRAINING_PRESCRIPTION_DELETE"} {
		in := w.input(t, "TEMPORARY", &joined.DestinationPlacementID, &w.groupB, 1, 3)
		prior := w.revision(t)
		tx, e := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(t.Context(), `SELECT set_config('mycfc.privacy_erasure_operation',$1,true)`, marker); e != nil {
			t.Fatal(e)
		}
		saved, e := dbgen.New(tx).ApplyTrainingPlacementChange(t.Context(), in)
		if e != nil {
			_ = tx.Rollback(t.Context())
			t.Fatal("ordinary marker affected valid apply", marker, e)
		}
		if e = tx.Commit(t.Context()); e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			applyDelta = saved.HeadRevision - prior
		}
		if saved.HeadRevision-prior != applyDelta || applyDelta <= 1 {
			t.Fatal("marker bypassed structural head witness", marker)
		}
		cancelIn := dbgen.CancelTrainingPlacementChangeParams{ActorID: w.actor, CommandID: saved.ChangeID, CancellationID: uuid.New(), ExpectedRevision: w.revision(t), ReviewSha256: strings.Repeat("f", 64)}
		prior = w.revision(t)
		tx, e = w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(t.Context(), `SELECT set_config('mycfc.privacy_erasure_operation',$1,true)`, marker); e != nil {
			t.Fatal(e)
		}
		cancelled, e := dbgen.New(tx).CancelTrainingPlacementChange(t.Context(), cancelIn)
		if e != nil {
			_ = tx.Rollback(t.Context())
			t.Fatal("ordinary marker affected valid cancel", marker, e)
		}
		if e = tx.Commit(t.Context()); e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			cancelDelta = cancelled.HeadRevision - prior
		}
		if cancelled.HeadRevision-prior != cancelDelta || cancelDelta <= 1 {
			t.Fatal("marker bypassed cancel head witness", marker)
		}
	}
	var writable bool
	for _, table := range []string{"training_placement_heads", "training_group_placements", "training_placement_changes", "training_prescription_withdrawals"} {
		if err = w.web.QueryRow(t.Context(), `SELECT has_table_privilege(current_user,$1,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')`, table).Scan(&writable); err != nil || writable {
			t.Fatal("web has aggregate DML", table, err)
		}
	}
	// A fresh mutation also requires the admitted SERIALIZABLE transaction.
	in := w.input(t, "MOVE", &joined.DestinationPlacementID, &w.groupB, 2, 0)
	if _, err = dbgen.New(w.web).ApplyTrainingPlacementChange(t.Context(), in); !sqlState(err, "55000") {
		t.Fatal("fresh nonserializable command admitted", err)
	}
}

func TestTrainingPlacementRosterPreservesGapAndScheduledPerson(t *testing.T) {
	w := newPlacementFixture(t)
	q := dbgen.New(w.web)
	gap, future, place, change := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic gapped roster person',$2,'hash','1990-01-01'),($3,'Synthetic scheduled roster person',$4,'hash','1990-01-01')`, gap, gap.String()+"@example.test", future, future.String()+"@example.test")
	recipientSchemaExec(t, w.web, `INSERT INTO user_memberships(user_id,programme_id,season_id,starts_on,ends_on) VALUES($1,$3,$4,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date-20,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date-1),($2,$3,$4,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+5,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+100)`, gap, future, w.programme, w.season)
	// A controlled historical fixture models an elapsed episode while retaining
	// the same current person placement; it is not a backdated business command.
	tx, err := w.f.low.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, tx, `INSERT INTO training_placement_changes(id,subject_user_id,programme_id,season_id,kind,destination_placement_id,effective_on,actor_user_id,canonical_command,head_revision,recorded_at)
 SELECT $1,$2,$3,$4,'JOIN',$5,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date-5,$6,jsonb_build_object('kind','JOIN','programme',$3::uuid,'season',$4::uuid,'source',NULL,'target',$7::uuid,'effective_on',(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date-5,'temporary_until',NULL,'expected_revision',head.revision,'review_sha256',repeat('a',64)),head.revision+1,clock_timestamp()-interval '5 days' FROM training_placement_heads head WHERE head.subject_user_id=$2 AND head.programme_id=$3`, change, gap, w.programme, w.season, place, w.actor, w.groupA)
	recipientSchemaExec(t, tx, `INSERT INTO training_group_placements(id,subject_user_id,programme_id,group_id,starts_on,change_id) VALUES($1,$2,$3,$4,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date-5,$5)`, place, gap, w.programme, w.groupA, change)
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	reviewed, err := q.TrainingPlacementSubjectForReview(t.Context(), dbgen.TrainingPlacementSubjectForReviewParams{ActorID: w.actor, SubjectID: future, GroupID: w.groupA, SeasonID: w.season, EffectiveOn: w.day(t, 7)})
	if err != nil {
		t.Fatal(err)
	}
	in := dbgen.ApplyTrainingPlacementChangeParams{ActorID: w.actor, SubjectID: future, ProgrammeID: w.programme, SeasonID: w.season, CommandID: uuid.New(), Kind: "JOIN", TargetGroupID: &w.groupA, EffectiveOn: w.day(t, 7), ExpectedRevision: reviewed.Revision, ReviewSha256: strings.Repeat("a", 64)}
	joined, err := w.apply(t, in)
	if err != nil {
		t.Fatal(err)
	}
	// Two future legs in the same group still display one person anchor.
	var revision int64
	if err = w.web.QueryRow(t.Context(), `SELECT revision FROM training_placement_heads WHERE subject_user_id=$1 AND programme_id=$2`, future, w.programme).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	in.CommandID = uuid.New()
	in.Kind = "TEMPORARY"
	in.SourcePlacementID = &joined.DestinationPlacementID
	in.TargetGroupID = &w.groupB
	in.EffectiveOn = w.day(t, 9)
	in.TemporaryUntil = w.day(t, 11)
	in.ExpectedRevision = revision
	if _, err = w.apply(t, in); err != nil {
		t.Fatal(err)
	}
	rows, err := q.TrainingGroupPlacementRosterForReview(t.Context(), dbgen.TrainingGroupPlacementRosterForReviewParams{ActorID: w.actor, GroupID: w.groupA})
	if err != nil || len(rows) != 2 {
		t.Fatal("current+scheduled person roster", len(rows), err)
	}
	var sawGap, sawFuture bool
	for _, row := range rows {
		switch row.UserID {
		case gap:
			sawGap = true
			if !row.IsCurrent || row.MembershipID != nil || row.Availability != "UNAVAILABLE" || row.PlacementID != place || row.DisplayOn != w.day(t, 0) {
				t.Fatal("gapped person disappeared or episode fabricated", row)
			}
		case future:
			sawFuture = true
			if row.IsCurrent || !row.HasScheduled || row.MembershipID == nil || row.Availability != "AVAILABLE" || row.PlacementID != joined.DestinationPlacementID || row.DisplayOn != w.day(t, 7) {
				t.Fatal("scheduled person anchor/episode incorrect", row)
			}
		default:
			t.Fatal("unlinked person disclosed", row.UserID)
		}
	}
	if !sawGap || !sawFuture {
		t.Fatal("roster omitted person")
	}
	stranger := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic stranger roster',$2,'hash','1980-01-01')`, stranger, stranger.String()+"@example.test")
	if rows, err = q.TrainingGroupPlacementRosterForReview(t.Context(), dbgen.TrainingGroupPlacementRosterForReviewParams{ActorID: stranger, GroupID: w.groupA}); err != nil || len(rows) != 0 {
		t.Fatal("unauthorized roster disclosure", err)
	}
}

func TestTrainingPlacementActualRuntimeFloorAndDefinition(t *testing.T) {
	w := newPlacementFixture(t)
	state, err := VerifyTrainingPlacementRuntime(t.Context(), w.web, TrainingScheduleCapabilities{ReaderVersion: 4, WriterVersion: 2})
	if err != nil || state.RequiredReader != 4 {
		t.Fatal("actual placement runtime", err)
	}
	if _, err = VerifyTrainingPlacementRuntime(t.Context(), w.web, TrainingScheduleCapabilities{ReaderVersion: 3, WriterVersion: 2}); err == nil {
		t.Fatal("reader3 admitted against actual floor4")
	}
	recipientSchemaExec(t, w.f.low, `GRANT UPDATE(revision) ON public.training_placement_heads TO `+quoteIdentifier(w.f.roles.AppUsername))
	if _, err = VerifyTrainingPlacementRuntime(t.Context(), w.web, TrainingScheduleCapabilities{ReaderVersion: 4, WriterVersion: 2}); err == nil {
		t.Fatal("column-only web write privilege admitted")
	}
	recipientSchemaExec(t, w.f.low, `REVOKE UPDATE(revision) ON public.training_placement_heads FROM `+quoteIdentifier(w.f.roles.AppUsername))
	if _, err = VerifyTrainingPlacementRuntime(t.Context(), w.web, TrainingScheduleCapabilities{ReaderVersion: 4, WriterVersion: 2}); err != nil {
		t.Fatal("runtime after removing column-only privilege", err)
	}
	var constraint string
	if err = w.f.low.QueryRow(t.Context(), `SELECT conname FROM pg_constraint WHERE conrelid='public.training_group_placements'::regclass AND contype='x'`).Scan(&constraint); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, w.f.low, `ALTER TABLE public.training_group_placements DROP CONSTRAINT `+quoteIdentifier(constraint))
	if _, err = VerifyTrainingPlacementRuntime(t.Context(), w.web, TrainingScheduleCapabilities{ReaderVersion: 4, WriterVersion: 2}); err == nil {
		t.Fatal("missing one-group exclusion admitted")
	}
}

func TestTrainingPlacementPublicationLineageAndBulkHeadAPI(t *testing.T) {
	w := newPlacementFixture(t)
	joined, err := w.apply(t, w.input(t, "JOIN", nil, &w.groupA, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	instruction := w.instruction(t, w.groupA, joined.DestinationPlacementID, 3)
	q := dbgen.New(w.web)
	structural, err := q.PublicationStructuralCandidates(t.Context(), dbgen.PublicationStructuralCandidatesParams{ActorID: w.actor, PlanID: instruction.plan})
	if err != nil || len(structural) != 1 || structural[0].SubjectID != w.subject || structural[0].PlacementID != joined.DestinationPlacementID || structural[0].MembershipID == nil || *structural[0].MembershipID != instruction.membership {
		t.Fatalf("structural actual lineage: %+v %v", structural, err)
	}
	members, err := q.ListStructuredTrainingPublicationMembers(t.Context(), instruction.plan)
	if err != nil || len(members) != 1 || members[0].PlacementID != joined.DestinationPlacementID || members[0].MembershipID != instruction.membership {
		t.Fatalf("eligible actual lineage: %+v %v", members, err)
	}
	raw, err := q.PublicationRawSource(t.Context(), dbgen.PublicationRawSourceParams{ActorID: w.actor, PlanID: instruction.plan})
	if err != nil || !json.Valid(raw.Envelope) || !strings.Contains(string(raw.Envelope), joined.DestinationPlacementID.String()) {
		t.Fatalf("raw lineage: %s %v", raw.Envelope, err)
	}
	choices, err := q.TrainingGroupAthleteChoicesForDate(t.Context(), dbgen.TrainingGroupAthleteChoicesForDateParams{ActorID: w.actor, GroupID: w.groupB, SeasonID: w.season, EffectiveOn: w.day(t, 2), NameQuery: "Synthetic placement athlete"})
	if err != nil || len(choices) != 1 || choices[0].AlreadyMember || !choices[0].Unavailable {
		t.Fatalf("overlapping JOIN must be unavailable without other-group disclosure: %+v %v", choices, err)
	}
	before := w.aggregate(t)
	tx, err := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), `SELECT id FROM users WHERE id=$1 FOR UPDATE NOWAIT`, w.subject); err != nil {
		t.Fatal(err)
	}
	heads, err := dbgen.New(tx).LockTrainingPlacementExistingHeadsForJoin(t.Context(), dbgen.LockTrainingPlacementExistingHeadsForJoinParams{ActorID: w.actor, GroupID: w.groupB, SubjectIds: []uuid.UUID{w.subject}})
	if err != nil || len(heads) != 1 || heads[0].SubjectUserID != w.subject || heads[0].ProgrammeID != w.programme {
		t.Fatalf("fixed existing head API: %+v %v", heads, err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if after := w.aggregate(t); after != before {
		t.Fatal("read-only existing-head admission changed retained facts or revision")
	}
	// Exact date gap must retain structural placement/person while membership is NULL,
	// while the previously published day remains covered (no classifier guard bypass).
	gapSession := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) VALUES($1,$2,'Synthetic gapped unpublished session',($3::date::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours',($3::date::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours',$4)`, gapSession, instruction.plan, w.day(t, 4), w.actor)
	recipientSchemaExec(t, w.web, `UPDATE user_memberships SET ends_on=(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+3 WHERE id=$1`, instruction.membership)
	structural, err = q.PublicationStructuralCandidates(t.Context(), dbgen.PublicationStructuralCandidatesParams{ActorID: w.actor, PlanID: instruction.plan})
	if err != nil || len(structural) != 2 {
		t.Fatalf("gapped structural identity retained: %+v %v", structural, err)
	}
	foundGap := false
	for _, candidate := range structural {
		if candidate.SessionID == gapSession {
			foundGap = true
			if candidate.SubjectID != w.subject || candidate.PlacementID != joined.DestinationPlacementID || candidate.MembershipID != nil {
				t.Fatalf("gapped structural identity lost: %+v", candidate)
			}
		}
	}
	if !foundGap {
		t.Fatal("gap session omitted")
	}
	members, err = q.ListStructuredTrainingPublicationMembers(t.Context(), instruction.plan)
	if err != nil || len(members) != 1 || members[0].SessionID != instruction.session {
		t.Fatalf("gap leaked into eligible publication: %+v %v", members, err)
	}
}

func TestTrainingPlacementFreshProductionBootstrap(t *testing.T) {
	recipientStampedFixture(t)
	f := newRecipientSchemaFixture(t, true)
	if err := FreshInitializeDatabase(t.Context(), f.root, "mycfc", f.roles); err != nil {
		t.Fatal("fresh83 production initialization", err)
	}
	f.low = f.connectLow(t)
	if err := ApplyBaselineAndHarden(t.Context(), f.low, "mycfc", f.roles); err != nil {
		t.Fatal("fresh83 admission/hardening", err)
	}
	recipientReceiptMatches(t, f, true)
	if err := VerifyTrainingRecipientMigrationFinal(t.Context(), f.low); err != nil {
		t.Fatal("fresh83 final schema definitions", err)
	}
	var count int
	if err := f.low.QueryRow(t.Context(), `SELECT count(*) FROM mycfc_meta.schema_migrations`).Scan(&count); err != nil || count != 83 {
		t.Fatalf("fresh83 ledger count %d: %v", count, err)
	}
}

func TestTrainingPlacementPublicationLockOrdersAndFirstWitness(t *testing.T) {
	w := newPlacementFixture(t)
	joined, err := w.apply(t, w.input(t, "JOIN", nil, &w.groupA, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	instruction := w.instruction(t, w.groupA, joined.DestinationPlacementID, 5)
	other := w.f.web(t)
	pub, err := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Rollback(context.Background())
	pq := dbgen.New(pub)
	if _, err = pq.LockPublicationPlan(t.Context(), dbgen.LockPublicationPlanParams{ActorID: w.actor, PlanID: instruction.plan}); err != nil {
		t.Fatal(err)
	}
	if _, err = pq.LockPublicationPlacementHeads(t.Context(), dbgen.LockPublicationPlacementHeadsParams{ActorID: w.actor, PlanID: instruction.plan}); err != nil {
		t.Fatal(err)
	}
	move, err := other.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer move.Rollback(context.Background())
	if _, err = move.Exec(t.Context(), `SELECT id FROM users WHERE id=$1 FOR UPDATE NOWAIT`, w.subject); err != nil {
		t.Fatal(err)
	}
	// Supported publisher's late current-person lock must refuse this order,
	// releasing its plan/head locks before the move requests the head UPDATE.
	_, err = pub.Exec(t.Context(), `SELECT id FROM users WHERE id=$1 FOR SHARE NOWAIT`, w.subject)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "55P03" {
		t.Fatalf("publisher late person NOWAIT: %v", err)
	}
	if err = pub.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	in := w.input(t, "MOVE", &joined.DestinationPlacementID, &w.groupB, 2, 0)
	if _, err = dbgen.New(move).ApplyTrainingPlacementChange(t.Context(), in); err != nil {
		t.Fatal("move after bounded publisher abort", err)
	}
	// Reverse entry order: a publisher cannot acquire a plan already physically
	// touched by the admitted move. A bounded transaction can abort safely.
	pub, err = w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Rollback(context.Background())
	if _, err = pub.Exec(t.Context(), `SET LOCAL lock_timeout='100ms'`); err != nil {
		t.Fatal(err)
	}
	_, err = dbgen.New(pub).LockPublicationPlan(t.Context(), dbgen.LockPublicationPlanParams{ActorID: w.actor, PlanID: instruction.plan})
	if !errors.As(err, &pe) || pe.Code != "55P03" {
		t.Fatalf("move-first plan conflict: %v", err)
	}
	if err = pub.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = move.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	// First eligible-person INSERT is a physical head and affected-plan witness,
	// including plans whose group has no placement for this person yet.
	var before, after pgtype.Timestamptz
	if err = w.web.QueryRow(t.Context(), `SELECT updated_at FROM training_plans WHERE id=$1`, instruction.plan).Scan(&before); err != nil {
		t.Fatal(err)
	}
	newcomer := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic first witness',$2,'hash','1990-01-01')`, newcomer, newcomer.String()+"@example.test")
	recipientSchemaExec(t, w.web, `INSERT INTO user_memberships(user_id,programme_id,season_id,starts_on) VALUES($1,$2,$3,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+1)`, newcomer, w.programme, w.season)
	var revision int64
	if err = w.web.QueryRow(t.Context(), `SELECT revision FROM training_placement_heads WHERE subject_user_id=$1 AND programme_id=$2`, newcomer, w.programme).Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("first actual head witness %d: %v", revision, err)
	}
	if err = w.web.QueryRow(t.Context(), `SELECT updated_at FROM training_plans WHERE id=$1`, instruction.plan).Scan(&after); err != nil || !after.Time.After(before.Time) {
		t.Fatalf("first person did not physically touch affected plan: %v", err)
	}
}

func TestTrainingPlacementStartedTodayIsHeldFutureRemainsAvailable(t *testing.T) {
	w := newPlacementFixture(t)
	joined, err := w.apply(t, w.input(t, "JOIN", nil, &w.groupA, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	instruction := w.instruction(t, w.groupA, joined.DestinationPlacementID, 0)
	recipientSchemaExec(t, w.web, `UPDATE training_sessions SET starts_at=((clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date::timestamp AT TIME ZONE 'Europe/Lisbon'),ends_at=((clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '1 microsecond' WHERE id=$1`, instruction.session)
	q := dbgen.New(w.web)
	rows, err := q.TrainingPlacementInstructionReview(t.Context(), dbgen.TrainingPlacementInstructionReviewParams{ActorID: w.actor, SourcePlacementID: joined.DestinationPlacementID, EffectiveOn: w.day(t, 0)})
	if err != nil || len(rows) != 1 || !rows[0].RequiresPolicy {
		t.Fatalf("started/finished today must remain owner-policy held: %+v %v", rows, err)
	}
	before := w.aggregate(t)
	if _, err = w.apply(t, w.input(t, "MOVE", &joined.DestinationPlacementID, &w.groupB, 0, 0)); err == nil {
		t.Fatal("started today move accepted without owner policy")
	}
	if before != w.aggregate(t) {
		t.Fatal("held move rewrote retained history")
	}
	if _, err = w.apply(t, w.input(t, "MOVE", &joined.DestinationPlacementID, &w.groupB, 1, 0)); err != nil {
		t.Fatal("independent known future move blocked", err)
	}
}

func TestTrainingPlacementGuardianWithdrawalCurrentConsumers(t *testing.T) {
	w := newPlacementFixture(t)
	minor := uuid.New()
	recipientSchemaExec(t, w.f.low, `CREATE SCHEMA IF NOT EXISTS mycfc_meta; CREATE TABLE IF NOT EXISTS mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`)
	insertVerifiedDependentFixture(t, t.Context(), w.f.root, w.actor, minor, "Synthetic placement dependent", "2015-01-01")
	w.subject = minor
	recipientSchemaExec(t, w.web, `INSERT INTO user_memberships(user_id,programme_id,season_id,starts_on,ends_on) VALUES($1,$2,$3,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date-20,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+300)`, w.subject, w.programme, w.season)
	joined, err := w.apply(t, w.input(t, "JOIN", nil, &w.groupA, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	old := w.instruction(t, w.groupA, joined.DestinationPlacementID, 7)
	q := dbgen.New(w.web)
	if n, e := q.SaveTrainingSessionOutcome(t.Context(), dbgen.SaveTrainingSessionOutcomeParams{SessionID: old.session, UserID: minor, Status: dbgen.TrainingOutcomeStatusCOMPLETED}); e != nil || n != 1 {
		t.Fatalf("pre-departure future completion setup %d: %v", n, e)
	}
	var completedBefore string
	if err = w.web.QueryRow(t.Context(), `SELECT to_jsonb(outcome)::text FROM training_session_outcomes outcome WHERE session_id=$1 AND user_id=$2`, old.session, minor).Scan(&completedBefore); err != nil {
		t.Fatal(err)
	}
	if _, err = q.GetTrainingPrescriptionForViewer(t.Context(), dbgen.GetTrainingPrescriptionForViewerParams{ID: old.prescription, UserID: w.actor}); err != nil {
		t.Fatal("current guardian direct instruction", err)
	}
	var now pgtype.Timestamptz
	if err = w.web.QueryRow(t.Context(), `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	dashboard, err := q.ListUpcomingTrainingSessionsForDashboard(t.Context(), dbgen.ListUpcomingTrainingSessionsForDashboardParams{UserID: w.actor, FromTime: now, RowLimit: 100})
	if err != nil || len(dashboard) != 1 {
		t.Fatalf("current guardian Hoje instruction: %+v %v", dashboard, err)
	}
	moved, err := w.apply(t, w.input(t, "TEMPORARY", &joined.DestinationPlacementID, &w.groupB, 0, 2))
	if err != nil {
		t.Fatal(err)
	}
	w.delivery(t, old.prescription, false)
	var completedAfter string
	if err = w.web.QueryRow(t.Context(), `SELECT to_jsonb(outcome)::text FROM training_session_outcomes outcome WHERE session_id=$1 AND user_id=$2`, old.session, minor).Scan(&completedAfter); err != nil || completedAfter != completedBefore {
		t.Fatal("withdrawal rewrote stored future-completed result", err)
	}
	for _, viewer := range []uuid.UUID{minor, w.actor} {
		if _, err = q.GetTrainingPrescriptionForViewer(t.Context(), dbgen.GetTrainingPrescriptionForViewerParams{ID: old.prescription, UserID: viewer}); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("withdrawn guardian/subject direct read %s: %v", viewer, err)
		}
		rows, e := q.ListTrainingPrescriptionsForViewer(t.Context(), dbgen.ListTrainingPrescriptionsForViewerParams{UserID: viewer})
		if e != nil || len(rows) != 0 {
			t.Fatalf("withdrawn prescription collection %s: %+v %v", viewer, rows, e)
		}
		links, e := q.ListTrainingPrescriptionLinksForSessionViewer(t.Context(), dbgen.ListTrainingPrescriptionLinksForSessionViewerParams{SessionID: old.session, UserID: viewer})
		if e != nil || len(links) != 0 {
			t.Fatalf("withdrawn session link %s: %+v %v", viewer, links, e)
		}
	}
	calendar, err := q.ListTrainingSessionsForAthlete(t.Context(), dbgen.ListTrainingSessionsForAthleteParams{UserID: minor, RowLimit: 100})
	if err != nil || len(calendar) != 0 {
		t.Fatalf("withdrawn grouped calendar: %+v %v", calendar, err)
	}
	dashboard, err = q.ListUpcomingTrainingSessionsForDashboard(t.Context(), dbgen.ListUpcomingTrainingSessionsForDashboardParams{UserID: w.actor, FromTime: now, RowLimit: 100})
	if err != nil || len(dashboard) != 0 {
		t.Fatalf("withdrawn guardian Hoje: %+v %v", dashboard, err)
	}
	returned := w.instruction(t, w.groupA, moved.ReturnPlacementID, 8)
	reason := "Synthetic replacement check"
	if n, e := q.SaveTrainingSessionOutcome(t.Context(), dbgen.SaveTrainingSessionOutcomeParams{SessionID: returned.session, UserID: minor, Status: dbgen.TrainingOutcomeStatusREPLACED, ReplacementSessionID: &old.session, ReplacementReason: &reason}); e != nil || n != 0 {
		t.Fatalf("withdrawn replacement target accepted %d %v", n, e)
	}
	if n, e := q.SaveTrainingSessionOutcome(t.Context(), dbgen.SaveTrainingSessionOutcomeParams{SessionID: old.session, UserID: minor, Status: dbgen.TrainingOutcomeStatusCOMPLETED}); e != nil || n != 0 {
		t.Fatalf("withdrawn own outcome accepted %d %v", n, e)
	}
	// Revocation between private read queries cannot leak timeline or instruction metadata.
	recipientSchemaExec(t, w.f.root, `UPDATE guardian_authority_relationships SET state='SUSPENDED',version=version+1,updated_at=clock_timestamp() WHERE guardian_user_id=$1 AND subject_user_id=$2`, w.actor, minor)
	timeline, err := q.TrainingPlacementTimeline(t.Context(), dbgen.TrainingPlacementTimelineParams{ActorID: w.actor, SubjectID: minor, ProgrammeID: w.programme, RowLimit: 101})
	if err != nil || len(timeline) != 0 {
		t.Fatalf("private timeline after guardian suspension: %+v %v", timeline, err)
	}
	instructions, err := q.TrainingPlacementInstructionReview(t.Context(), dbgen.TrainingPlacementInstructionReviewParams{ActorID: w.actor, SourcePlacementID: moved.ReturnPlacementID, EffectiveOn: w.day(t, 3)})
	if err != nil || len(instructions) != 0 {
		t.Fatalf("private instruction metadata after guardian suspension: %+v %v", instructions, err)
	}
	w.delivery(t, returned.prescription, false)
	if _, err = q.GetTrainingPrescriptionForViewer(t.Context(), dbgen.GetTrainingPrescriptionForViewerParams{ID: returned.prescription, UserID: w.actor}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("suspended guardian return instruction", err)
	}
}

func TestTrainingPlacementSyntheticPriorDayCompletedHistoryRetained(t *testing.T) {
	w := newPlacementFixture(t)
	// Explicitly synthetic past source fixture, never a backdated web command:
	// migration-owner inserts coherent immutable change+placement in one deferred-FK transaction.
	change, placement := uuid.New(), uuid.New()
	tx, err := w.f.low.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	recipientSchemaExec(t, tx, `INSERT INTO training_group_placements(id,subject_user_id,programme_id,group_id,starts_on,change_id) VALUES($1,$2,$3,$4,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date-10,$5)`, placement, w.subject, w.programme, w.groupA, change)
	recipientSchemaExec(t, tx, `INSERT INTO training_placement_changes(id,subject_user_id,programme_id,season_id,kind,destination_placement_id,effective_on,actor_user_id,canonical_command,head_revision) SELECT $1,$2,$3,$4,'JOIN',$5,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date-10,$6,jsonb_build_object('synthetic_fixture',true),head.revision FROM training_placement_heads head WHERE head.subject_user_id=$2 AND head.programme_id=$3`, change, w.subject, w.programme, w.season, placement, w.actor)
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	old := w.instruction(t, w.groupA, placement, -1)
	q := dbgen.New(w.web)
	if n, e := q.SaveTrainingSessionOutcome(t.Context(), dbgen.SaveTrainingSessionOutcomeParams{SessionID: old.session, UserID: w.subject, Status: dbgen.TrainingOutcomeStatusCOMPLETED}); e != nil || n != 1 {
		t.Fatalf("prior-day completion %d %v", n, e)
	}
	if _, err = w.apply(t, w.input(t, "MOVE", &placement, &w.groupB, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err = q.GetTrainingPrescriptionForViewer(t.Context(), dbgen.GetTrainingPrescriptionForViewerParams{ID: old.prescription, UserID: w.subject}); err != nil {
		t.Fatal("prior-day completed body lost", err)
	}
	links, err := q.ListTrainingPrescriptionLinksForSessionViewer(t.Context(), dbgen.ListTrainingPrescriptionLinksForSessionViewerParams{SessionID: old.session, UserID: w.subject})
	if err != nil || len(links) != 1 {
		t.Fatalf("prior-day completed link %+v %v", links, err)
	}
	rows, err := q.ListTrainingPrescriptionsForViewer(t.Context(), dbgen.ListTrainingPrescriptionsForViewerParams{UserID: w.subject})
	if err != nil || len(rows) != 1 {
		t.Fatalf("prior-day completed collection %+v %v", rows, err)
	}
	if n, e := q.UpdateOwnCompletedSessionFeedback(t.Context(), dbgen.UpdateOwnCompletedSessionFeedbackParams{SessionID: old.session, UserID: w.subject, ExpectedVersion: 1}); e != nil || n != 1 {
		t.Fatalf("prior-day feedback lost %d %v", n, e)
	}
}
