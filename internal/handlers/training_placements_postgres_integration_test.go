//go:build integration

package handlers

import (
	"bytes"
	"errors"
	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/jackc/pgx/v5"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
)

func (f *publicationDomainFixture) day(t *testing.T, offset int) string {
	t.Helper()
	var day string
	if e := f.web.QueryRow(t.Context(), `SELECT ((clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+$1::integer)::text`, offset).Scan(&day); e != nil {
		t.Fatal(e)
	}
	return day
}
func (f *publicationDomainFixture) placementReview(t *testing.T, c trainingPlacementCommand) trainingPlacementReviewed {
	t.Helper()
	store := PostgresTrainingPlacementStore{Pool: f.web}
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	c.ProgrammeID = f.programme
	c.SeasonID = f.season
	read, e := store.ReadPlacement(t.Context(), f.actor, c, true)
	if e != nil {
		t.Fatal("read actual placement review", e)
	}
	c.ExpectedRevision = read.revision()
	payload, e := read.proposal(f.actor, c)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	raw, token, e := focusedWorkoutReviews.signPlacement(payload, now)
	if e != nil {
		t.Fatal(e)
	}
	reviewed, e := focusedWorkoutReviews.verifyPlacement(raw, token, f.actor, c.ContextGroupID, c.SubjectID, now)
	if e != nil {
		t.Fatal(e)
	}
	return reviewed
}
func (f *publicationDomainFixture) changePlacement(t *testing.T, c trainingPlacementCommand) trainingPlacementSaveResult {
	t.Helper()
	review := f.placementReview(t, c)
	out, e := (PostgresTrainingPlacementStore{Pool: f.web}).ConfirmPlacement(t.Context(), review)
	if e != nil {
		t.Fatal("save actual placement", e)
	}
	return out
}
func (f *publicationDomainFixture) place(t *testing.T, person, group, season uuid.UUID, on string) uuid.UUID {
	t.Helper()
	if season != f.season {
		t.Fatal("fixture requires explicitly configured fixture season")
	}
	return f.changePlacement(t, trainingPlacementCommand{SubjectID: person, ContextGroupID: group, Kind: placementJoin, TargetGroupID: group, EffectiveOn: on}).DestinationPlacementID
}
func (f *publicationDomainFixture) placementAggregate(t *testing.T) string {
	t.Helper()
	var raw string
	e := f.web.QueryRow(t.Context(), `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(h) ORDER BY subject_user_id,programme_id) FROM training_placement_heads h),(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM training_placement_changes c),(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM training_group_placements p),(SELECT jsonb_agg(to_jsonb(w) ORDER BY prescription_id,change_id,leg) FROM training_prescription_withdrawals w))::text`).Scan(&raw)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func TestTrainingPlacementNativeStoreTemporaryExactReplayAndCancellation(t *testing.T) {
	f := newPublicationDomainFixture(t)
	groupB := uuid.New()
	f.exec(t, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Temporary destination',$2,$3)`, groupB, f.programme, f.actor)
	store := PostgresTrainingPlacementStore{Pool: f.web}
	review := f.placementReview(t, trainingPlacementCommand{Kind: placementTemporary, SubjectID: f.subject, ContextGroupID: f.group, SourcePlacementID: f.placement, TargetGroupID: groupB, EffectiveOn: f.day(t, 2), TemporaryUntil: f.day(t, 4)})
	if review.Payload.Projection.Return == nil || review.Payload.Projection.Return.GroupID != f.group || review.Payload.Projection.Return.StartsOn != f.day(t, 5) {
		t.Fatal("review did not return to exact previous group on E+1")
	}
	saved, e := store.ConfirmPlacement(t.Context(), review)
	if e != nil {
		t.Fatal(e)
	}
	before := f.placementAggregate(t)
	again, e := store.ConfirmPlacement(t.Context(), review)
	if e != nil || !again.Replayed || again.ChangeID != saved.ChangeID || again.ReturnPlacementID != saved.ReturnPlacementID || f.placementAggregate(t) != before {
		t.Fatal("exact proof replay changed aggregate", e)
	}
	read, e := store.ReadPlacementCancellation(t.Context(), f.actor, f.group, f.subject, saved.ChangeID)
	if e != nil || !read.CanCancel {
		t.Fatal("readonly cancellation review", e)
	}
	command := trainingPlacementCancellation{ID: uuid.New(), ChangeID: saved.ChangeID, SubjectID: f.subject, ProgrammeID: f.programme, SeasonID: f.season, ContextGroupID: f.group, ExpectedRevision: read.Change.Revision}
	payload, e := read.proposal(f.actor, command)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	raw, token, e := focusedWorkoutReviews.signPlacementCancel(payload, now)
	if e != nil {
		t.Fatal(e)
	}
	cancel, e := focusedWorkoutReviews.verifyPlacementCancel(raw, token, f.actor, f.group, f.subject, now)
	if e != nil {
		t.Fatal(e)
	}
	result, e := store.ConfirmPlacementCancellation(t.Context(), cancel)
	if e != nil {
		t.Fatal(e)
	}
	before = f.placementAggregate(t)
	replay, e := store.ConfirmPlacementCancellation(t.Context(), cancel)
	if e != nil || !replay.Replayed || replay.ChangeID != result.ChangeID || replay.ReturnPlacementID != uuid.Nil || f.placementAggregate(t) != before {
		t.Fatal("cancel exact replay changed aggregate", e)
	}
	var active int
	if e = f.web.QueryRow(t.Context(), `SELECT count(*) FROM training_group_placements WHERE subject_user_id=$1 AND group_id=$2 AND superseded_by_change_id IS NULL AND cancelled_by_change_id IS NULL`, f.subject, f.group).Scan(&active); e != nil || active != 1 {
		t.Fatal("cancellation did not restore exact original source", e, active)
	}
}
func TestTrainingPlacementReturnRequiresExplicitNewPublicationWithSameBody(t *testing.T) {
	f := newPublicationDomainFixture(t)
	publications := PostgresStructuredTrainingStore{Pool: f.web}
	first, e := publications.ConfirmPublication(t.Context(), f.review(t, "Initial A1 instructions"))
	if e != nil {
		t.Fatal(e)
	}
	var oldID uuid.UUID
	var oldBody []byte
	var oldHash string
	if e = f.web.QueryRow(t.Context(), `SELECT id,snapshot,snapshot_sha256 FROM training_prescriptions WHERE publication_id=$1`, first.PublicationID).Scan(&oldID, &oldBody, &oldHash); e != nil {
		t.Fatal(e)
	}
	groupB := uuid.New()
	f.exec(t, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Other group',$2,$3)`, groupB, f.programme, f.actor)
	moved := f.changePlacement(t, trainingPlacementCommand{Kind: placementMove, SubjectID: f.subject, ContextGroupID: f.group, SourcePlacementID: f.placement, TargetGroupID: groupB, EffectiveOn: f.day(t, 0)})
	var delivery bool
	if e = f.web.QueryRow(t.Context(), `SELECT public.training_prescription_current_delivery($1)`, oldID).Scan(&delivery); e != nil || delivery {
		t.Fatal("departure kept future A1 access", e)
	}
	returned := f.changePlacement(t, trainingPlacementCommand{Kind: placementMove, SubjectID: f.subject, ContextGroupID: groupB, SourcePlacementID: moved.DestinationPlacementID, TargetGroupID: f.group, EffectiveOn: f.day(t, 0)})
	if e = f.web.QueryRow(t.Context(), `SELECT public.training_prescription_current_delivery($1)`, oldID).Scan(&delivery); e != nil || delivery {
		t.Fatal("A2 return revived old A1 instructions", e)
	}
	second, e := publications.ConfirmPublication(t.Context(), f.review(t, "Explicit A2 publication"))
	if e != nil || second.NoOp || second.Revision != 2 {
		t.Fatal("samebody A2 publication became no-op", e, second)
	}
	var newID, placement uuid.UUID
	var newBody []byte
	var newHash string
	if e = f.web.QueryRow(t.Context(), `SELECT id,placement_id,snapshot,snapshot_sha256 FROM training_prescriptions WHERE publication_id=$1`, second.PublicationID).Scan(&newID, &placement, &newBody, &newHash); e != nil {
		t.Fatal(e)
	}
	if newID == oldID || placement != returned.DestinationPlacementID || !bytes.Equal(newBody, oldBody) || newHash != oldHash {
		t.Fatal("return did not create new relational lineage with identical immutable content")
	}
	if e = f.web.QueryRow(t.Context(), `SELECT public.training_prescription_current_delivery($1)`, newID).Scan(&delivery); e != nil || !delivery {
		t.Fatal("explicit A2 instructions unavailable", e)
	}
	if _, e = f.web.Exec(t.Context(), `UPDATE training_prescriptions SET snapshot=snapshot WHERE id=$1`, oldID); e == nil {
		t.Fatal("immutable historical instruction became editable")
	}
}

func TestTrainingPlacementBulkTwentyJoinsAndExactSelectedSetReplay(t *testing.T) {
	f := newPublicationDomainFixture(t)
	group := uuid.New()
	f.exec(t, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Bulk target',$2,$3)`, group, f.programme, f.actor)
	for i := 0; i < 20; i++ {
		person, member := uuid.New(), uuid.New()
		f.exec(t, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Bulk synthetic athlete '||$2::text,$3,'hash','1990-01-01')`, person, strconv.Itoa(i), person.String()+"@example.test")
		f.exec(t, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,CURRENT_DATE,CURRENT_DATE+300)`, member, person, f.season, f.programme)
	}
	store := PostgresTrainingGroupStore{Pool: f.web}
	g, e := store.GetGroup(t.Context(), f.actor, group)
	if e != nil {
		t.Fatal(e)
	}
	on, _ := time.Parse("2006-01-02", f.day(t, 0))
	rows, e := store.SearchAthletesForDate(t.Context(), f.actor, group, f.season, on, "Bulk synthetic athlete", 0)
	if e != nil || len(rows) != 20 {
		t.Fatal("bounded candidates", len(rows), e)
	}
	in := TrainingGroupRosterInput{ActorID: f.actor, GroupID: group, SeasonID: f.season, CommandID: uuid.New(), EffectiveOn: on, ExpectedUpdatedAt: g.UpdatedAt}
	in.SelectionProof, e = store.ReviewAthleteSelection(t.Context(), in, rows)
	if e != nil {
		t.Fatal("actual signed candidate review", e)
	}
	for _, row := range rows {
		in.MembershipIDs = append(in.MembershipIDs, row.MembershipID)
	}
	started := time.Now()
	first, e := store.AddAthletes(t.Context(), in)
	t.Logf("twenty reviewed JOIN callback %s", time.Since(started))
	if e != nil || first.Added != 20 || first.Group.MemberCount != 20 {
		t.Fatalf("first=%+v err=%v", first, e)
	}
	before := f.placementAggregate(t)
	again, e := store.AddAthletes(t.Context(), in)
	if e != nil || again.Added != 20 || !again.Group.UpdatedAt.Equal(first.Group.UpdatedAt) || f.placementAggregate(t) != before {
		t.Fatal("exact selected-set proof replay changed cohort/head/group", e)
	}
	changed := in
	changed.MembershipIDs = changed.MembershipIDs[:19]
	if _, e = store.AddAthletes(t.Context(), changed); e == nil || f.placementAggregate(t) != before {
		t.Fatal("different selected set repaired/replayed a partial batch", e)
	}
}

func TestTrainingPlacementCompletedFutureInstructionStillWithdraws(t *testing.T) {
	f := newPublicationDomainFixture(t)
	store := PostgresStructuredTrainingStore{Pool: f.web}
	first, e := store.ConfirmPublication(t.Context(), f.review(t, "Future A1 instructions"))
	if e != nil {
		t.Fatal(e)
	}
	var old uuid.UUID
	var body []byte
	if e = f.web.QueryRow(t.Context(), `SELECT id,snapshot FROM training_prescriptions WHERE publication_id=$1`, first.PublicationID).Scan(&old, &body); e != nil {
		t.Fatal(e)
	}
	f.exec(t, `INSERT INTO training_session_outcomes(session_id,user_id,prescription_id,status) VALUES($1,$2,$3,'COMPLETED')`, f.session, f.subject, old)
	var outcomeBefore string
	if e = f.web.QueryRow(t.Context(), `SELECT to_jsonb(o)::text FROM training_session_outcomes o WHERE session_id=$1 AND user_id=$2`, f.session, f.subject).Scan(&outcomeBefore); e != nil {
		t.Fatal(e)
	}
	groupB := uuid.New()
	f.exec(t, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Other destination',$2,$3)`, groupB, f.programme, f.actor)
	moved := f.changePlacement(t, trainingPlacementCommand{Kind: placementMove, SubjectID: f.subject, ContextGroupID: f.group, SourcePlacementID: f.placement, TargetGroupID: groupB, EffectiveOn: f.day(t, 0)})
	assertWithdrawn := func() {
		t.Helper()
		var current bool
		if e := f.web.QueryRow(t.Context(), `SELECT public.training_prescription_current_delivery($1)`, old).Scan(&current); e != nil || current {
			t.Fatal("precompleted future prescription bypassed withdrawal", e)
		}
		if _, e := dbgen.New(f.web).GetTrainingPrescriptionForViewer(t.Context(), dbgen.GetTrainingPrescriptionForViewerParams{ID: old, UserID: f.subject}); !errors.Is(e, pgx.ErrNoRows) {
			t.Fatal("COMPLETED future detail bypassed placement withdrawal", e)
		}
	}
	assertWithdrawn()
	f.changePlacement(t, trainingPlacementCommand{Kind: placementMove, SubjectID: f.subject, ContextGroupID: groupB, SourcePlacementID: moved.DestinationPlacementID, TargetGroupID: f.group, EffectiveOn: f.day(t, 0)})
	assertWithdrawn()
	var outcomeAfter string
	var retained []byte
	if e = f.web.QueryRow(t.Context(), `SELECT to_jsonb(o)::text,p.snapshot FROM training_session_outcomes o JOIN training_prescriptions p ON p.id=o.prescription_id WHERE o.session_id=$1 AND o.user_id=$2`, f.session, f.subject).Scan(&outcomeAfter, &retained); e != nil || outcomeAfter != outcomeBefore || !bytes.Equal(body, retained) {
		t.Fatal("withdrawal rewrote stored feedback or historical snapshot", e)
	}
}

func TestTrainingPlacementClassificationVersionsHeadBeforeNewEpisode(t *testing.T) {
	f := newPublicationDomainFixture(t)
	person, old, groupB := uuid.New(), uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Classification subject',$2,'hash','1990-01-01')`, person, person.String()+"@example.test")
	f.exec(t, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,CURRENT_DATE-1,CURRENT_DATE+300)`, old, person, f.season, f.programme)
	f.exec(t, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Classification target',$2,$3)`, groupB, f.programme, f.actor)
	source := f.place(t, person, f.group, f.season, f.day(t, 0))
	review := f.placementReview(t, trainingPlacementCommand{Kind: placementTemporary, SubjectID: person, ContextGroupID: f.group, SourcePlacementID: source, TargetGroupID: groupB, EffectiveOn: f.day(t, 2), TemporaryUntil: f.day(t, 4)})
	version, e := db.DatedClassificationVersion(t.Context(), f.web, person)
	if e != nil {
		t.Fatal(e)
	}
	tomorrow, _ := time.Parse("2006-01-02", f.day(t, 1))
	successor, e := (db.DatedParticipationService{Pool: f.web}).TransitionNextDay(t.Context(), old, db.OrdinaryDatedAssignment{ActorID: f.actor, MemberID: person, SeasonID: f.season, ProgrammeID: f.programme, StartsOn: tomorrow, ExpectedVersion: version})
	if e != nil || successor.ID == uuid.Nil {
		t.Fatal("actual supported classification transition", e)
	}
	var revision int64
	if e = f.web.QueryRow(t.Context(), `SELECT revision FROM training_placement_heads WHERE subject_user_id=$1 AND programme_id=$2`, person, f.programme).Scan(&revision); e != nil || revision <= review.Payload.Command.ExpectedRevision {
		t.Fatal("classification did not version physical placement head", e)
	}
	before := f.placementAggregate(t)
	if _, e = (PostgresTrainingPlacementStore{Pool: f.web}).ConfirmPlacement(t.Context(), review); !errors.Is(e, errTrainingPlacementChanged) || f.placementAggregate(t) != before {
		t.Fatal("classification metadata silently rebased reviewed placement", e)
	}
	refreshed := f.placementReview(t, review.Payload.Command)
	result, e := (PostgresTrainingPlacementStore{Pool: f.web}).ConfirmPlacement(t.Context(), refreshed)
	if e != nil || result.ReturnPlacementID == uuid.Nil {
		t.Fatal("explicit new review failed actual successor episode", e)
	}
}
