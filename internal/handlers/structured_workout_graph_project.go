package handlers

import (
	"encoding/json"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func focusedPointer[T any](v T) *T { return &v }
func allocateFocusedGraphIDs(p *focusedWorkoutGraph, prior *focusedWorkoutGraph) {
	p.SessionID = uuid.New()
	if prior != nil {
		p.SessionID = prior.SessionID
	}
	for ai := range p.Activities {
		a := &p.Activities[ai]
		a.ID = uuid.New()
		var oldA *focusedGraphActivityInput
		if prior != nil {
			for i := range prior.Activities {
				if prior.Activities[i].Key == a.Key {
					oldA = &prior.Activities[i]
					a.ID = oldA.ID
				}
			}
		}
		for pi := range a.Phases {
			b := &a.Phases[pi]
			b.ID = uuid.New()
			var oldB *focusedGraphPhaseInput
			if oldA != nil {
				for i := range oldA.Phases {
					if oldA.Phases[i].Key == b.Key {
						oldB = &oldA.Phases[i]
						b.ID = oldB.ID
					}
				}
			}
			if b.Water != nil {
				for ni := range b.Water.Nodes {
					n := &b.Water.Nodes[ni]
					n.ID = uuid.New()
					if oldB != nil && oldB.Water != nil {
						for _, old := range oldB.Water.Nodes {
							if old.Key == n.Key {
								n.ID = old.ID
							}
						}
					}
				}
			}
			if b.Gym != nil {
				for xi := range b.Gym.Exercises {
					x := &b.Gym.Exercises[xi]
					x.ID = uuid.New()
					if oldB != nil && oldB.Gym != nil {
						for _, old := range oldB.Gym.Exercises {
							if old.Key == x.Key {
								x.ID = old.ID
							}
						}
					}
				}
			}
		}
	}
}
func projectFocusedGraph(p focusedWorkoutGraph) (pages.StructuredTrainingSession, error) {
	if _, err := focusedGraphCheckedTotals(p); err != nil {
		return pages.StructuredTrainingSession{}, err
	}
	day, err := time.Parse("2006-01-02", p.Schedule.Date)
	if err != nil {
		return pages.StructuredTrainingSession{}, err
	}
	base := structuredTrainingRow{groupID: &p.Context.GroupID, groupName: p.Context.GroupName, planID: &p.Context.PlanID, planTitle: p.Context.PlanTitle, weekStart: p.Context.WeekStart, sessionID: &p.SessionID, sessionTitle: p.Title, sessionDescription: p.Description, entryKind: string(p.Kind), sessionStatus: "ACTIVE", schedulePrecision: string(p.Schedule.Precision), scheduledOn: pgtype.Date{Time: day, Valid: true}}
	if p.Schedule.Band != nil {
		base.dayBand = focusedPointer(string(*p.Schedule.Band))
	}
	if p.Schedule.StartsAt != nil {
		base.startsAt = *p.Schedule.StartsAt
		base.startsKnown = true
	}
	if p.Schedule.EndsAt != nil {
		base.endsAt = *p.Schedule.EndsAt
		base.endsKnown = true
	}
	profiles := map[uuid.UUID]dbgen.WaterIntensityProfile{}
	zones := map[uuid.UUID]map[string]dbgen.WaterIntensityZone{}
	for _, r := range p.References.Profiles {
		profiles[r.ID] = r
	}
	for _, z := range p.References.Zones {
		if zones[z.ProfileID] == nil {
			zones[z.ProfileID] = map[string]dbgen.WaterIntensityZone{}
		}
		zones[z.ProfileID][z.Code] = z
	}
	rows := []structuredTrainingRow{}
	for ai := range p.Activities {
		a := &p.Activities[ai]
		ar := base
		ar.segmentID = &a.ID
		ar.segmentPosition = ai + 1
		ar.modality = string(a.Modality)
		ar.segmentTitle = a.Title
		ar.segmentLocation = a.Location
		ar.equipmentNotes = a.Equipment
		ar.duration = intValue(a.DurationMinutes)
		ar.startOffset = intValue(a.StartOffsetMinutes)
		ar.plannedStartSet = a.StartOffsetMinutes != nil
		ar.transition = intValue(a.TransitionMinutes)
		if len(a.Phases) == 0 {
			rows = append(rows, ar)
			continue
		}
		for bi := range a.Phases {
			b := &a.Phases[bi]
			br := ar
			br.blockID = &b.ID
			br.blockPosition = bi + 1
			br.blockPurpose = string(b.Purpose)
			br.blockTitle = b.Title
			br.instructions = b.Instructions
			if w := b.Water; w != nil {
				var profileName *string
				var profileRevision *int32
				var profileCraft *dbgen.PaddlingCraft
				if w.ProfileID != nil {
					if r, ok := profiles[*w.ProfileID]; ok {
						profileName = &r.Name
						profileRevision = &r.Revision
						profileCraft = &r.Craft
					}
				}
				if len(w.Nodes) == 0 {
					rows = append(rows, structuredWaterRow(br, &w.Method, w.TargetDistance, w.TargetCertainty, profileName, profileRevision, profileCraft, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
					continue
				}
				byKey := map[string]uuid.UUID{}
				for _, n := range w.Nodes {
					byKey[n.Key] = n.ID
				}
				positions := map[string]int32{}
				for ni := range w.Nodes {
					n := &w.Nodes[ni]
					positions[n.ParentKey]++
					position := positions[n.ParentKey]
					var parentID *uuid.UUID
					if n.ParentKey != "" {
						parentID = focusedPointer(byKey[n.ParentKey])
					}
					var zoneLabel, zoneMeaning *string
					var zoneMin, zoneMax *int32
					if w.ProfileID != nil && n.Intensity != nil {
						if z, ok := zones[*w.ProfileID][*n.Intensity]; ok {
							zoneLabel = &z.Label
							zoneMeaning = &z.Meaning
							zoneMin = z.CadenceMin
							zoneMax = z.CadenceMax
						}
					}
					rows = append(rows, structuredWaterRow(br, &w.Method, w.TargetDistance, w.TargetCertainty, profileName, profileRevision, profileCraft, &n.ID, parentID, &position, &n.Kind, &n.Name, n.Repeats, n.Duration, n.DurationCertainty, n.Distance, n.DistanceCertainty, n.Recovery, n.Intensity, n.Cadence, zoneLabel, zoneMin, zoneMax, zoneMeaning, n.DrillFocus, n.DrillFormat, n.RoleNotes, &n.Instructions))
				}
				continue
			}
			if g := b.Gym; g != nil {
				for xi := range g.Exercises {
					x := &g.Exercises[xi]
					position := int32(xi + 1)
					rows = append(rows, structuredRowFromValues(br, &g.Structure, &g.Objective, &g.Rounds, g.RoundRecovery, &x.ID, &position, &x.Name, x.Sets, x.Repetitions, x.Duration, x.Distance, x.Recovery, x.ResistanceKind, x.ResistanceValue, x.ResistanceText, x.Intent, x.Tempo, &x.Notes))
				}
				continue
			}
			rows = append(rows, br)
		}
	}
	if len(rows) == 0 {
		rows = append(rows, base)
	}
	audiences := assembleStructuredTraining(rows, lisbonLocation())
	if len(audiences) != 1 || len(audiences[0].Weeks) != 1 || len(audiences[0].Weeks[0].Sessions) != 1 {
		return pages.StructuredTrainingSession{}, errFocusedWorkoutInvalid
	}
	return audiences[0].Weeks[0].Sessions[0], nil
}
func focusedGraphArtifact(p focusedWorkoutGraph) ([]byte, error) {
	session, err := projectFocusedGraph(p)
	if err != nil {
		return nil, err
	}
	return focusedWorkoutBaseArtifact(session, p.Context, p.Schedule)
}
func focusedWorkoutBaseArtifact(projected pages.StructuredTrainingSession, c FocusedWorkoutContext, schedule TrainingSessionSchedule) ([]byte, error) {
	raw, err := json.Marshal(projected)
	if err != nil {
		return nil, err
	}
	session := map[string]json.RawMessage{}
	if err = json.Unmarshal(raw, &session); err != nil {
		return nil, err
	}
	delete(session, "EntryKind")
	session["entry_kind"], err = json.Marshal(projected.EntryKind)
	if err != nil {
		return nil, err
	}
	session["schedule"], err = json.Marshal(schedule)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Version   int                        `json:"schema_version"`
		PlanID    string                     `json:"plan_id"`
		PlanTitle string                     `json:"plan_title"`
		GroupName string                     `json:"group_name"`
		Session   map[string]json.RawMessage `json:"session"`
	}{2, c.PlanID.String(), c.PlanTitle, c.GroupName, session})
}
