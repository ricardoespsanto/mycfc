package handlers

import (
	"errors"
	"strings"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPlacementReadRejectsInvalidOrIncompleteAuthorityFacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*placementReadFake, *trainingPlacementCommand)
		want   error
		field  string
	}{
		{name: "unknown source subject", change: func(f *placementReadFake, _ *trainingPlacementCommand) { f.source.SubjectID = uuid.New() }, want: errTrainingPlacementInvalid},
		{name: "missing destination membership", change: func(f *placementReadFake, _ *trainingPlacementCommand) { f.subject.MembershipID = uuid.Nil }, want: errTrainingPlacementInvalid},
		{name: "duplicate destination group", change: func(f *placementReadFake, _ *trainingPlacementCommand) { f.groups = append(f.groups, f.groups[0]) }, want: errTrainingPlacementInvalid},
		{name: "date outside configured season", change: func(f *placementReadFake, _ *trainingPlacementCommand) {
			f.groups[0].SeasonEndsOn = placementDate("2027-03-02")
		}, field: "effective_on"},
		{name: "revoked group choices", change: func(f *placementReadFake, _ *trainingPlacementCommand) { f.groups = nil }, want: errTrainingPlacementChanged},
		{name: "source missing from timeline", change: func(f *placementReadFake, _ *trainingPlacementCommand) { f.timeline = nil }, want: errTrainingPlacementChanged},
		{name: "duplicate affected instruction", change: func(f *placementReadFake, _ *trainingPlacementCommand) {
			f.instructions = append(f.instructions, f.instructions[0])
		}, want: errTrainingPlacementInvalid},
		{name: "previous placement expires before return", change: func(f *placementReadFake, _ *trainingPlacementCommand) {
			f.returns[0].EndsOn = placementDate("2027-03-02")
		}, want: errTrainingPlacementReturn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, x, c, state := placementReadFixture()
			tc.change(f, &c)
			_, err := readTrainingPlacement(t.Context(), f, x.ActorID, c, state, false)
			var failure *trainingPlacementFieldFailure
			if tc.field != "" {
				if !errors.As(err, &failure) || failure.Field != tc.field {
					t.Fatalf("field failure=%v", err)
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
		})
	}
	for _, source := range []string{"source", "subject", "groups", "timeline", "instructions", "returns"} {
		t.Run(source+" lookup revoked", func(t *testing.T) {
			f, x, c, state := placementReadFixture()
			f.readErrors = map[string]error{source: pgx.ErrNoRows}
			if _, err := readTrainingPlacement(t.Context(), f, x.ActorID, c, state, false); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("incomplete scoped lookup admitted: %v", err)
			}
		})
	}
}

func TestCancellationReadRefusesPartialOrChangedAuthority(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*placementCancelTxFake, *trainingPlacementCancellation)
		want   error
	}{
		{"missing reader floor", func(q *placementCancelTxFake, _ *trainingPlacementCancellation) { q.state.RequiredReader = 3 }, errTrainingPlacementInvalid},
		{"revoked change", func(q *placementCancelTxFake, _ *trainingPlacementCancellation) { q.changeErr = pgx.ErrNoRows }, pgx.ErrNoRows},
		{"foreign subject", func(_ *placementCancelTxFake, c *trainingPlacementCancellation) { c.SubjectID = uuid.New() }, pgx.ErrNoRows},
		{"foreign group", func(_ *placementCancelTxFake, c *trainingPlacementCancellation) { c.ContextGroupID = uuid.New() }, pgx.ErrNoRows},
		{"revoked source", func(q *placementCancelTxFake, _ *trainingPlacementCancellation) {
			q.readErrors = map[string]error{"source": pgx.ErrNoRows}
		}, pgx.ErrNoRows},
		{"stale source", func(q *placementCancelTxFake, _ *trainingPlacementCancellation) { q.source.Revision++ }, errTrainingPlacementChanged},
		{"revoked timeline", func(q *placementCancelTxFake, _ *trainingPlacementCancellation) {
			q.readErrors = map[string]error{"timeline": pgx.ErrNoRows}
		}, pgx.ErrNoRows},
		{"oversize timeline", func(q *placementCancelTxFake, _ *trainingPlacementCancellation) {
			q.timeline = make([]dbgen.TrainingPlacementTimelineRow, trainingPlacementMaxTimeline+1)
		}, errTrainingPlacementLimit},
		{"duplicate timeline", func(q *placementCancelTxFake, _ *trainingPlacementCancellation) {
			q.timeline = append(q.timeline, q.timeline[0])
		}, errTrainingPlacementChanged},
		{"changed during reread", func(q *placementCancelTxFake, _ *trainingPlacementCancellation) { q.driftChangeAt = q.changeReads + 2 }, errTrainingPlacementChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, r := placementCancelTxFixture(t)
			c := r.Payload.Command
			tc.change(q, &c)
			got, err := readTrainingPlacementCancellation(t.Context(), q, r.Payload.ActorID, c.ContextGroupID, c.SubjectID, c.ChangeID, q.state)
			if !errors.Is(err, tc.want) || got.PersonName != "" || got.CanCancel || len(got.Timeline) != 0 {
				t.Fatalf("partial cancellation read=%+v err=%v want=%v", got, err, tc.want)
			}
		})
	}
}

func TestCancellationReviewRequiresFutureActionAndExactIdentity(t *testing.T) {
	q, r := placementCancelTxFixture(t)
	c := r.Payload.Command
	read, err := readTrainingPlacementCancellation(t.Context(), q, r.Payload.ActorID, c.ContextGroupID, c.SubjectID, c.ChangeID, q.state)
	if err != nil {
		t.Fatal(err)
	}
	s := focusedWorkoutSigner{key: [32]byte{12}}
	for _, tc := range []struct {
		name   string
		change func(*trainingPlacementCancelPayload)
	}{
		{"already effective", func(p *trainingPlacementCancelPayload) { p.EffectiveOn = p.DecisionOn }},
		{"substituted placement", func(p *trainingPlacementCancelPayload) { p.After[0].ID = uuid.New() }},
		{"same change and cancel ID", func(p *trainingPlacementCancelPayload) { p.Command.ID = p.Command.ChangeID }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fresh, err := read.proposal(r.Payload.ActorID, c)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(&fresh)
			if _, _, err = s.signPlacementCancel(fresh, q.clock); !errors.Is(err, errTrainingPlacementInvalid) {
				t.Fatalf("invalid review signed: %v", err)
			}
		})
	}
	if _, err := s.verifyPlacementCancel("altered", "proof", r.Payload.ActorID, c.ContextGroupID, c.SubjectID, q.clock); !errors.Is(err, errTrainingPlacementInvalid) {
		t.Fatal("invalid transport verified")
	}
	read.PersonName = strings.Repeat("x", focusedWorkoutMaxRequestBytes)
	if _, err := read.proposal(r.Payload.ActorID, c); !errors.Is(err, errTrainingPlacementLimit) {
		t.Fatalf("oversize review signed: %v", err)
	}
}
