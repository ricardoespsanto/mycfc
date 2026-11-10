package handlers

import (
	"net/url"
	"strconv"
	"time"

	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/google/uuid"
)

func normalizeFocusedEdit(source focusedEditSource, d focusedGraphDraft) (focusedWorkoutGraph, validation.FieldErrors) {
	values := cloneFocusedValues(d.Values)
	kindChanged := values.Get("entry_kind") != string(source.Graph.Kind)
	values.Set("entry_kind", string(source.Graph.Kind))
	d.Values = values
	context := source.Context
	proposedDay := values.Get("schedule.date")
	context.Day = proposedDay
	p, e := normalizeFocusedGraphWithSource(context, d, &source.Graph)
	p.Context = source.Context
	p.SessionID = source.Raw.Session.ID
	day, err := time.Parse("2006-01-02", proposedDay)
	if err != nil || day.Before(source.Context.WeekStart) || !day.Before(source.Context.WeekStart.AddDate(0, 0, 7)) {
		focusedGraphError(e, "schedule.date", "A data proposta deve permanecer na semana deste treino.")
	}
	if kindChanged {
		focusedGraphError(e, "entry_kind", "O tipo deste treino mantém-se. Os valores introduzidos foram conservados.")
	}
	return p, e
}
func allocateFocusedEditIDs(p *focusedWorkoutGraph, source focusedWorkoutGraph, prior *focusedWorkoutGraph) error {
	identity, err := focusedEditIdentityFor(source)
	if err != nil {
		return err
	}
	allocateFocusedGraphIDs(p, prior)
	p.SessionID = source.SessionID
	for ai := range p.Activities {
		a := &p.Activities[ai]
		if id := identity.IDs[focusedActivityPrefix(a.Key)]; id != uuid.Nil {
			a.ID = id
		}
		for bi := range a.Phases {
			b := &a.Phases[bi]
			if id := identity.IDs[focusedGraphPhasePrefix(a.Key, b.Key)]; id != uuid.Nil {
				b.ID = id
			}
			if b.Water != nil {
				for ni := range b.Water.Nodes {
					n := &b.Water.Nodes[ni]
					if id := identity.IDs[focusedNodePrefix(a.Key, b.Key, n.Key)]; id != uuid.Nil {
						n.ID = id
					}
				}
			}
			if b.Gym != nil {
				for xi := range b.Gym.Exercises {
					x := &b.Gym.Exercises[xi]
					if id := identity.IDs[focusedExercisePrefix(a.Key, b.Key, x.Key)]; id != uuid.Nil {
						x.ID = id
					}
				}
			}
		}
	}
	return nil
}
func focusedEditDraftForGraph(p focusedWorkoutGraph) (focusedGraphDraft, error) {
	for _, t := range []*time.Time{p.Schedule.StartsAt, p.Schedule.EndsAt} {
		if t != nil && (t.Second() != 0 || t.Nanosecond() != 0) {
			return focusedGraphDraft{}, errFocusedEditReadOnly
		}
	}
	v := url.Values{"title": {p.Title}, "description": {p.Description}, "entry_kind": {string(p.Kind)}, "schedule.mode": {string(p.Schedule.Precision)}, "schedule.date": {p.Schedule.Date}}
	setText := func(prefix, field, value string) { v.Set(prefix+"."+field, value) }
	setInt := func(prefix, field string, n *int32) {
		if n != nil {
			setText(prefix, field, strconv.FormatInt(int64(*n), 10))
		}
	}
	setOptional := func(prefix, field string, value *string) {
		if value != nil {
			setText(prefix, field, *value)
		}
	}
	if p.Schedule.StartsAt != nil {
		t := p.Schedule.StartsAt.In(lisbonLocation())
		v.Set("schedule.start_time", t.Format("15:04"))
		if len(focusedClockOccurrences(p.Schedule.Date, t.Format("15:04"))) > 1 {
			v.Set("schedule.start_occurrence", p.Schedule.StartsAt.UTC().Format(time.RFC3339Nano))
		}
	}
	if p.Schedule.EndsAt != nil {
		t := p.Schedule.EndsAt.In(lisbonLocation())
		v.Set("schedule.end_date", t.Format("2006-01-02"))
		v.Set("schedule.end_time", t.Format("15:04"))
		if len(focusedClockOccurrences(t.Format("2006-01-02"), t.Format("15:04"))) > 1 {
			v.Set("schedule.end_occurrence", p.Schedule.EndsAt.UTC().Format(time.RFC3339Nano))
		}
	}
	if p.Schedule.Band != nil {
		v.Set("schedule.band", string(*p.Schedule.Band))
	}
	for _, a := range p.Activities {
		ap := focusedActivityPrefix(a.Key)
		v.Add("activity_order", a.Key)
		for name, value := range map[string]string{"id": a.ID.String(), "modality": string(a.Modality), "title": a.Title, "location": a.Location, "equipment_notes": a.Equipment} {
			setText(ap, name, value)
		}
		setInt(ap, "duration_minutes", a.DurationMinutes)
		setInt(ap, "start_offset_minutes", a.StartOffsetMinutes)
		setInt(ap, "transition_minutes", a.TransitionMinutes)
		for _, b := range a.Phases {
			pp := focusedGraphPhasePrefix(a.Key, b.Key)
			v.Add(ap+".phase_order", b.Key)
			for name, value := range map[string]string{"id": b.ID.String(), "purpose": string(b.Purpose), "title": b.Title, "instructions": b.Instructions} {
				setText(pp, name, value)
			}
			if w := b.Water; w != nil {
				setText(pp, "detail_mode", "WATER")
				wp := pp + ".water"
				setText(wp, "method", string(w.Method))
				if w.ProfileID != nil {
					setText(wp, "profile_id", w.ProfileID.String())
				}
				setInt(wp, "target_distance_metres", w.TargetDistance)
				if w.TargetCertainty != nil {
					setText(wp, "target_distance_certainty", string(*w.TargetCertainty))
				}
				for _, n := range w.Nodes {
					v.Add(wp+".node_order", n.Key)
					np := focusedNodePrefix(a.Key, b.Key, n.Key)
					for field, value := range map[string]string{"id": n.ID.String(), "parent_key": n.ParentKey, "kind": string(n.Kind), "name": n.Name, "instructions": n.Instructions} {
						setText(np, field, value)
					}
					setInt(np, "repeats", n.Repeats)
					setInt(np, "duration_seconds", n.Duration)
					setInt(np, "distance_metres", n.Distance)
					setInt(np, "recovery_seconds", n.Recovery)
					setInt(np, "cadence_spm", n.Cadence)
					if n.DurationCertainty != nil {
						setText(np, "duration_certainty", string(*n.DurationCertainty))
					}
					if n.DistanceCertainty != nil {
						setText(np, "distance_certainty", string(*n.DistanceCertainty))
					}
					setOptional(np, "intensity_code", n.Intensity)
					setOptional(np, "drill_focus", n.DrillFocus)
					setOptional(np, "drill_format", n.DrillFormat)
					setOptional(np, "role_notes", n.RoleNotes)
				}
			}
			if g := b.Gym; g != nil {
				setText(pp, "detail_mode", "GYM")
				gp := pp + ".gym"
				setText(gp, "structure", string(g.Structure))
				setText(gp, "objective", string(g.Objective))
				setText(gp, "rounds", strconv.FormatInt(int64(g.Rounds), 10))
				setInt(gp, "round_recovery_seconds", g.RoundRecovery)
				for _, x := range g.Exercises {
					v.Add(gp+".exercise_order", x.Key)
					ep := focusedExercisePrefix(a.Key, b.Key, x.Key)
					setText(ep, "id", x.ID.String())
					setText(ep, "name", x.Name)
					setText(ep, "notes", x.Notes)
					setInt(ep, "sets", x.Sets)
					setInt(ep, "repetitions", x.Repetitions)
					setInt(ep, "duration_seconds", x.Duration)
					setInt(ep, "distance_metres", x.Distance)
					setInt(ep, "recovery_seconds", x.Recovery)
					if x.ResistanceKind != nil {
						setText(ep, "resistance_kind", string(*x.ResistanceKind))
					}
					if x.ResistanceValue != nil {
						setText(ep, "resistance_value", strconv.FormatFloat(*x.ResistanceValue, 'f', -1, 64))
					}
					setOptional(ep, "resistance_text", x.ResistanceText)
					if x.Intent != nil {
						setText(ep, "execution_intent", string(*x.Intent))
					}
					setOptional(ep, "tempo", x.Tempo)
				}
			}
		}
	}
	return parseFocusedEditGraph(v, p)
}
func focusedEditLosslessDraft(source focusedEditSource) (focusedGraphDraft, error) {
	d, err := focusedEditDraftForGraph(source.Graph)
	if err != nil {
		return d, err
	}
	p, e := normalizeFocusedEdit(source, d)
	if len(e) > 0 {
		return d, errFocusedEditReadOnly
	}
	if err = allocateFocusedEditIDs(&p, source.Graph, nil); err != nil {
		return d, err
	}
	p.References = source.Graph.References
	if !focusedGraphJSONEqual(p, source.Graph) {
		return d, errFocusedEditReadOnly
	}
	return d, nil
}
