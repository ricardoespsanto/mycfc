package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type placementReadFake struct {
	subject                   dbgen.TrainingPlacementSubjectForReviewRow
	source                    dbgen.TrainingPlacementSourceForReviewRow
	groups                    []dbgen.TrainingPlacementGroupChoicesRow
	timeline                  []dbgen.TrainingPlacementTimelineRow
	instructions              []dbgen.TrainingPlacementInstructionReviewRow
	returns                   []dbgen.TrainingPlacementReturnForReviewRow
	subjectReads, sourceReads int
	driftSubject, driftSource bool
	readErrors                map[string]error
}

func (f *placementReadFake) TrainingPlacementSubjectForReview(context.Context, dbgen.TrainingPlacementSubjectForReviewParams) (dbgen.TrainingPlacementSubjectForReviewRow, error) {
	f.subjectReads++
	s := f.subject
	if f.driftSubject && f.subjectReads > 1 {
		s.Revision++
	}
	return s, f.readErrors["subject"]
}
func (f *placementReadFake) TrainingPlacementSourceForReview(context.Context, dbgen.TrainingPlacementSourceForReviewParams) (dbgen.TrainingPlacementSourceForReviewRow, error) {
	f.sourceReads++
	s := f.source
	if f.driftSource && f.sourceReads > 1 {
		s.Version++
	}
	return s, f.readErrors["source"]
}
func (f *placementReadFake) TrainingPlacementGroupChoices(context.Context, dbgen.TrainingPlacementGroupChoicesParams) ([]dbgen.TrainingPlacementGroupChoicesRow, error) {
	return f.groups, f.readErrors["groups"]
}
func (f *placementReadFake) TrainingPlacementTimeline(context.Context, dbgen.TrainingPlacementTimelineParams) ([]dbgen.TrainingPlacementTimelineRow, error) {
	return f.timeline, f.readErrors["timeline"]
}
func (f *placementReadFake) TrainingPlacementInstructionReview(context.Context, dbgen.TrainingPlacementInstructionReviewParams) ([]dbgen.TrainingPlacementInstructionReviewRow, error) {
	return f.instructions, f.readErrors["instructions"]
}
func (f *placementReadFake) TrainingPlacementReturnForReview(context.Context, dbgen.TrainingPlacementReturnForReviewParams) ([]dbgen.TrainingPlacementReturnForReviewRow, error) {
	return f.returns, f.readErrors["returns"]
}
func placementReadFixture() (*placementReadFake, trainingPlacementContext, trainingPlacementCommand, db.TrainingScheduleState) {
	x, c := placementTestCommand()
	clock := pgtype.Timestamptz{Time: time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC), Valid: true}
	sourceID := c.ContextGroupID
	f := &placementReadFake{subject: dbgen.TrainingPlacementSubjectForReviewRow{DecisionAt: clock, SubjectID: c.SubjectID, SubjectName: "Pessoa sintética", ProgrammeID: c.ProgrammeID, GroupID: c.TargetGroupID, GroupName: "Destino", SeasonID: c.SeasonID, SeasonStartsOn: placementDate("2027-01-01"), SeasonEndsOn: placementDate("2027-12-31"), Revision: c.ExpectedRevision, MembershipID: uuid.New()}, source: dbgen.TrainingPlacementSourceForReviewRow{DecisionAt: clock, ID: c.SourcePlacementID, SubjectID: c.SubjectID, SubjectName: "Pessoa sintética", ProgrammeID: c.ProgrammeID, GroupID: sourceID, GroupName: "Anterior", StartsOn: placementDate("2027-03-01"), Version: 3, Revision: c.ExpectedRevision}, returns: []dbgen.TrainingPlacementReturnForReviewRow{{MembershipID: uuid.New(), SeasonID: c.SeasonID, StartsOn: placementDate("2027-01-01"), SeasonStartsOn: placementDate("2027-01-01"), SeasonEndsOn: placementDate("2027-12-31")}}, instructions: []dbgen.TrainingPlacementInstructionReviewRow{{PrescriptionID: uuid.New(), SessionID: uuid.New(), DecisionAt: clock, SessionOn: placementDate("2027-04-01"), WillWithdraw: true}}}
	f.groups = []dbgen.TrainingPlacementGroupChoicesRow{{ID: sourceID, Name: "Anterior", ProgrammeID: c.ProgrammeID, SeasonID: c.SeasonID, SeasonStartsOn: placementDate("2027-01-01"), SeasonEndsOn: placementDate("2027-12-31")}, {ID: c.TargetGroupID, Name: "Destino", ProgrammeID: c.ProgrammeID, SeasonID: c.SeasonID, SeasonStartsOn: placementDate("2027-01-01"), SeasonEndsOn: placementDate("2027-12-31")}}
	f.timeline = []dbgen.TrainingPlacementTimelineRow{{ID: c.SourcePlacementID, SubjectUserID: &c.SubjectID, ProgrammeID: c.ProgrammeID, GroupID: sourceID, GroupName: "Anterior", StartsOn: placementDate("2027-03-01"), Version: 3, Revision: c.ExpectedRevision}}
	return f, x, c, db.TrainingScheduleState{DatabaseIncarnation: uuid.New(), StateEpoch: 2, RequiredReader: 4, SchemaContract: "schedule-p1"}
}
func TestTrainingPlacementReadUsesSourceForLeaveAndUniqueExactReturn(t *testing.T) {
	f, x, c, state := placementReadFixture()
	r, e := readTrainingPlacement(t.Context(), f, x.ActorID, c, state, false)
	if e != nil {
		t.Fatal(e)
	}
	p, e := r.proposal(x.ActorID, c)
	if e != nil || p.Projection.Return.GroupID != f.source.GroupID || p.Projection.Return.StartsOn != "2027-03-29" {
		t.Fatalf("return %+v %v", p, e)
	}
	f, x, c, state = placementReadFixture()
	c.Kind = placementLeave
	c.TargetGroupID = uuid.Nil
	c.TemporaryUntil = ""
	f.subject.MembershipID = uuid.Nil
	if _, e = readTrainingPlacement(t.Context(), f, x.ActorID, c, state, false); e != nil || f.subjectReads != 0 {
		t.Fatalf("leaving a protected gapped source must not demand a new episode: %v calls%d", e, f.subjectReads)
	}
	for _, n := range []int{0, 2} {
		f, x, c, state = placementReadFixture()
		f.returns = make([]dbgen.TrainingPlacementReturnForReviewRow, n)
		if n == 2 {
			for i := range f.returns {
				f.returns[i].MembershipID = uuid.New()
			}
		}
		if _, e = readTrainingPlacement(t.Context(), f, x.ActorID, c, state, false); !errors.Is(e, errTrainingPlacementReturn) {
			t.Fatalf("return count%d silently selected: %v", n, e)
		}
	}
}
func TestTrainingPlacementReadRefusesDriftAndOnlyAffectedPolicyBoundary(t *testing.T) {
	for _, kind := range []string{"subject", "source", "timeline"} {
		f, x, c, state := placementReadFixture()
		switch kind {
		case "subject":
			f.driftSubject = true
		case "source":
			f.driftSource = true
		case "timeline":
			f.timeline[0].Revision++
		}
		if _, e := readTrainingPlacement(t.Context(), f, x.ActorID, c, state, false); !errors.Is(e, errTrainingPlacementChanged) {
			t.Fatalf("%s drift %v", kind, e)
		}
	}
	f, x, c, state := placementReadFixture()
	f.instructions[0].RequiresPolicy = true
	if _, e := readTrainingPlacement(t.Context(), f, x.ActorID, c, state, false); !errors.Is(e, errTrainingPlacementPolicy) {
		t.Fatalf("missing affected boundary refusal: %v", e)
	}
	f, x, c, state = placementReadFixture()
	if _, e := readTrainingPlacement(t.Context(), f, x.ActorID, c, state, false); e != nil {
		t.Fatalf("pending same-day policy blocked known future move: %v", e)
	}
	for _, kind := range []string{"groups", "timeline", "instructions"} {
		f, x, c, state = placementReadFixture()
		switch kind {
		case "groups":
			f.groups = make([]dbgen.TrainingPlacementGroupChoicesRow, trainingPlacementMaxTimeline+1)
		case "timeline":
			f.timeline = make([]dbgen.TrainingPlacementTimelineRow, trainingPlacementMaxTimeline+1)
		case "instructions":
			f.instructions = make([]dbgen.TrainingPlacementInstructionReviewRow, trainingPublicationReviewMaxTuples+1)
		}
		if _, e := readTrainingPlacement(t.Context(), f, x.ActorID, c, state, false); !errors.Is(e, errTrainingPlacementLimit) {
			t.Fatalf("%s truncated/accepted overflow: %v", kind, e)
		}
	}
}
func TestTrainingPlacementReadDigestRetainsDecisionsWithoutVolatileClock(t *testing.T) {
	f, x, c, state := placementReadFixture()
	r, e := readTrainingPlacement(t.Context(), f, x.ActorID, c, state, false)
	if e != nil {
		t.Fatal(e)
	}
	before, e := r.digest()
	if e != nil {
		t.Fatal(e)
	}
	r.Subject.DecisionAt.Time = r.Subject.DecisionAt.Time.Add(time.Second)
	r.Source.DecisionAt.Time = r.Source.DecisionAt.Time.Add(time.Second)
	r.Instructions[0].DecisionAt.Time = r.Instructions[0].DecisionAt.Time.Add(time.Second)
	same, e := r.digest()
	if e != nil || same != before {
		t.Fatalf("read clock alone changed commitment: %v", e)
	}
	r.Instructions[0].WillWithdraw = false
	changed, e := r.digest()
	if e != nil || changed == before {
		t.Fatal("omitted actual withdrawal decision from commitment")
	}
}
