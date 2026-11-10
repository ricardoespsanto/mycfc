package handlers

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestReuseProposalContractRefusesIncompleteWholeCopies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*reuseRead, url.Values)
		fields []string
	}{
		{"destination required", func(r *reuseRead, _ url.Values) { r.Target = nil }, []string{"target_group_id"}},
		{"source required", func(r *reuseRead, _ url.Values) { r.Source.Items = nil }, []string{"form"}},
		{"session count exceeds cap", func(r *reuseRead, _ url.Values) { r.Source.Items = make([]reuseSourceItem, reuseMaxSessions+1) }, []string{"form"}},
		{"new week requires valid own text", func(r *reuseRead, _ url.Values) {
			r.Target.Selector.Title = " "
			r.Target.Selector.Description = strings.Repeat("x", 4001)
		}, []string{"week_title", "week_description"}},
		{"whole workout cannot append to existing session", func(r *reuseRead, _ url.Values) {
			r.Target.Selector.SessionID = uuid.New()
		}, []string{"target_session_id"}},
		{"mapping stays in destination week", func(_ *reuseRead, values url.Values) {
			values.Set("mappings[m_1].date", "1900-01-01")
		}, []string{"mappings[m_1].date"}},
		{"prescription origin required", func(r *reuseRead, _ url.Values) { r.Source.Items[0].Origins = nil }, []string{"form"}},
		{"review epoch required", func(r *reuseRead, _ url.Values) { r.State.StateEpoch = 0 }, []string{"form"}},
		{"reference closure budget", func(r *reuseRead, _ url.Values) {
			r.References.Profiles = make([]dbgen.WaterIntensityProfile, 33)
		}, []string{"form"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, read := reuseHTTPExample(t)
			tc.change(&read, base.Values)
			original := cloneFocusedValues(base.Values)
			proposal, fields := buildReuseProposal(base.ActorID, read, base.Values, nil)
			for _, field := range tc.fields {
				if fields[field] == "" {
					t.Fatalf("missing validation for %s: %v", field, fields)
				}
			}
			if !focusedGraphJSONEqual(proposal.Values, original) || !focusedGraphJSONEqual(base.Values, original) {
				t.Fatal("proposal validation changed the submitted values")
			}
		})
	}
}

func reuseFragmentProposalFixture(t *testing.T, kind dbgen.TrainingRoutineKind) (uuid.UUID, reuseRead) {
	t.Helper()
	target := focusedEditFixture(t)
	c := target.Context
	block := reuseRoutineWaterFixture()
	var snapshot any = block
	if kind == "SEGMENT" {
		snapshot = reuseRoutineSegment{Modality: "WATER", Title: "Rotina de água", Blocks: []reuseRoutineBlock{block}}
	}
	row := reuseRoutineSnapshot(t, kind, snapshot)
	row.Modality = focusedPointer(dbgen.TrainingSegmentModalityWATER)
	row.UpdatedAt = pgtype.Timestamptz{Time: c.PlanUpdatedAt, Valid: true}
	item, err := decodeReuseRoutine(row)
	if err != nil {
		t.Fatal(err)
	}
	selector := reuseTargetSelector{GroupID: c.GroupID, SeasonID: c.SeasonID, WeekStart: c.WeekStart.Format(time.DateOnly), SessionID: target.Graph.SessionID}
	if kind == "BLOCK" {
		selector.SegmentID = target.Graph.Activities[0].ID
	}
	return c.ActorID, reuseRead{
		State:  db.TrainingScheduleState{DatabaseIncarnation: c.DatabaseIncarnation, StateEpoch: c.StateEpoch, SchemaContract: c.SchemaContract, PartialV2Enabled: c.AuthoringEnabled},
		Source: reuseSourceMaterial{Selector: reuseSourceSelector{Kind: "ROUTINE", RoutineID: row.ID}, Routine: &row, Items: []reuseSourceItem{item}},
		Target: &reuseTargetMaterial{
			Selector:      selector,
			Configuration: dbgen.GetReuseTargetContextRow{GroupID: c.GroupID, SeasonID: c.SeasonID, ExistingPlanID: &c.PlanID, WeekStart: pgtype.Date{Time: c.WeekStart, Valid: true}},
			Existing:      &target.Raw,
		},
		References:  focusedGraphProfileFacts{Profiles: []dbgen.WaterIntensityProfile{{ID: *block.Water.ProfileID, Revision: 1}}},
		TargetGraph: &target.Graph,
		DecisionAt:  target.Graph.Schedule.StartsAt.Add(-time.Minute),
	}
}

func TestReuseProposalContractAppendsWaterWithStableOriginAndExistingIdentity(t *testing.T) {
	for _, kind := range []dbgen.TrainingRoutineKind{"BLOCK", "SEGMENT"} {
		t.Run(string(kind), func(t *testing.T) {
			actor, read := reuseFragmentProposalFixture(t, kind)
			// A block may target the second activity without disturbing the first.
			if kind == "BLOCK" {
				first := focusedGraphActivityInput{ID: uuid.New(), Key: "a_2", Modality: "GYM", Title: "Outra atividade"}
				read.TargetGraph.Activities = append([]focusedGraphActivityInput{first}, read.TargetGraph.Activities...)
			}
			proposal, fields := buildReuseProposal(actor, read, nil, nil)
			if len(fields) != 0 || len(proposal.Proposal.Destinations) != 1 || len(proposal.Proposal.Events) != 1 {
				t.Fatalf("fragment review fields = %v", fields)
			}
			dest, event := proposal.Proposal.Destinations[0], proposal.Proposal.Events[0]
			if dest.Graph.SessionID != read.TargetGraph.SessionID || !focusedGraphJSONEqual(dest.Graph.Schedule, read.TargetGraph.Schedule) ||
				event.SourceID != read.Source.Routine.ID || event.DestinationKind != string(kind) || event.DestinationID == uuid.Nil {
				t.Fatalf("append lost existing session or routine identity: %+v", event)
			}
			var copied focusedGraphPhaseInput
			if kind == "BLOCK" {
				copied = dest.Graph.Activities[1].Phases[1]
				if dest.Graph.Activities[0].ID != read.TargetGraph.Activities[0].ID || copied.ID != event.DestinationID {
					t.Fatal("block appended to the wrong activity")
				}
			} else {
				added := dest.Graph.Activities[1]
				copied = added.Phases[0]
				if added.ID != event.DestinationID {
					t.Fatal("segment event did not identify the appended segment")
				}
			}
			origin := dest.Origins[copied.ID]
			if copied.Water == nil || origin.RoutineID == nil || *origin.RoutineID != read.Source.Routine.ID || origin.NativeBlockID != nil {
				t.Fatalf("copied prescription lost its explicit routine origin: %+v", origin)
			}
			again, fields := buildReuseProposal(actor, read, nil, &proposal)
			if len(fields) != 0 || !focusedGraphJSONEqual(again.Proposal, proposal.Proposal) {
				t.Fatalf("regenerating the review changed copied or audit identities: %v", fields)
			}
		})
	}
}

func TestReuseProposalContractRefusesIneligibleOrOverfullFragmentDestination(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   dbgen.TrainingRoutineKind
		change func(*reuseRead)
		field  string
	}{
		{"existing workout required", "BLOCK", func(r *reuseRead) { r.Target.Existing = nil }, "target_session_id"},
		{"rest cannot receive training", "BLOCK", func(r *reuseRead) { r.Target.Existing.Session.EntryKind = "REST" }, "target_session_id"},
		{"workout has started", "BLOCK", func(r *reuseRead) { r.DecisionAt = *r.TargetGraph.Schedule.StartsAt }, "target_session_id"},
		{"block requires selected activity", "BLOCK", func(r *reuseRead) { r.Target.Selector.SegmentID = uuid.New() }, "target_segment_id"},
		{"segment cannot target another segment", "SEGMENT", func(r *reuseRead) { r.Target.Selector.SegmentID = r.TargetGraph.Activities[0].ID }, "target_session_id"},
		{"whole prescription must fit phase budget", "BLOCK", func(r *reuseRead) {
			a := &r.TargetGraph.Activities[0]
			for len(a.Phases) < focusedWorkoutMaxPhases {
				a.Phases = append(a.Phases, focusedGraphPhaseInput{Key: fmt.Sprintf("p_%d", len(a.Phases)+1), ID: uuid.New(), Purpose: "MAIN", Instructions: "Instruções da fase existente."})
			}
		}, "form"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor, read := reuseFragmentProposalFixture(t, tc.kind)
			tc.change(&read)
			p, fields := buildReuseProposal(actor, read, nil, nil)
			if fields[tc.field] == "" || len(p.Proposal.Destinations) != 0 || len(p.Proposal.Events) != 0 {
				t.Fatalf("ineligible fragment result fields=%v destinations=%d events=%d", fields, len(p.Proposal.Destinations), len(p.Proposal.Events))
			}
		})
	}
}

func TestReuseProposalContractWholeRoutineUsesStoredSourceVersion(t *testing.T) {
	base, read := reuseHTTPExample(t)
	row := dbgen.GetReuseRoutineRow{
		ID: uuid.New(), Kind: "SESSION", Name: "Treino reutilizável",
		UpdatedAt: pgtype.Timestamptz{Time: time.Date(2026, 10, 19, 9, 30, 0, 0, time.UTC), Valid: true},
		Snapshot:  []byte(`{"schema_version":1,"title":"Treino guardado","entry_kind":"TRAINING","duration_seconds":3600,"segments":[]}`),
	}
	item, err := decodeReuseRoutine(row)
	if err != nil {
		t.Fatal(err)
	}
	read.Source = reuseSourceMaterial{Selector: reuseSourceSelector{Kind: "ROUTINE", RoutineID: row.ID}, Routine: &row, Items: []reuseSourceItem{item}}
	p, fields := buildReuseProposal(base.ActorID, read, base.Values, nil)
	if len(fields) != 0 || len(p.Proposal.Events) != 1 || len(p.Proposal.Destinations) != 1 {
		t.Fatalf("whole routine review fields = %v", fields)
	}
	event, dest := p.Proposal.Events[0], p.Proposal.Destinations[0]
	if event.SourceKind != "ROUTINE" || event.SourceID != row.ID || !event.SourceUpdatedAt.Equal(row.UpdatedAt.Time) ||
		event.DestinationID != dest.Graph.SessionID || dest.Graph.Schedule.EndsAt.Sub(*dest.Graph.Schedule.StartsAt) != time.Hour {
		t.Fatalf("proposal lost stored routine version, duration or audit destination: %+v", event)
	}
}
