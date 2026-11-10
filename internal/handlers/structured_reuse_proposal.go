package handlers

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/google/uuid"
)

func reuseTargetContext(actor uuid.UUID, state reuseRead, planID uuid.UUID, day string) FocusedWorkoutContext {
	t := state.Target
	c := FocusedWorkoutContext{ActorID: actor, PlanID: planID, GroupID: t.Selector.GroupID, SeasonID: t.Selector.SeasonID, Day: day, GroupName: t.Configuration.GroupName, PlanTitle: t.Selector.Title, WeekStart: t.Configuration.WeekStart.Time, DatabaseIncarnation: state.State.DatabaseIncarnation, StateEpoch: state.State.StateEpoch, SchemaContract: state.State.SchemaContract, AuthoringEnabled: state.State.PartialV2Enabled}
	if t.Plan != nil {
		c.PlanTitle = t.Plan.Title
		c.PlanUpdatedAt = t.Plan.UpdatedAt.Time.UTC()
	}
	return c
}
func reusePriorDestination(prior *reuseReviewPayload, key string) *reuseDestination {
	if prior != nil {
		for i := range prior.Proposal.Destinations {
			if prior.Proposal.Destinations[i].Key == key {
				return &prior.Proposal.Destinations[i]
			}
		}
	}
	return nil
}
func reuseEventID(prior *reuseReviewPayload, sourceKind string, sourceID uuid.UUID, destKind string, destID uuid.UUID) uuid.UUID {
	if prior != nil {
		for _, e := range prior.Proposal.Events {
			if e.SourceKind == sourceKind && e.SourceID == sourceID && e.DestinationKind == destKind && e.DestinationID == destID {
				return e.ID
			}
		}
	}
	return uuid.New()
}
func buildReuseProposal(actor uuid.UUID, read reuseRead, values url.Values, prior *reuseReviewPayload) (reuseReviewPayload, validation.FieldErrors) {
	e := validation.FieldErrors{}
	p := reuseReviewPayload{ActorID: actor, Incarnation: read.State.DatabaseIncarnation, Epoch: read.State.StateEpoch, Source: read.Source, References: read.References, Values: values}
	if read.Target == nil {
		e.Add("target_group_id", "Escolha o grupo, a época e a semana de destino.")
		return p, e
	}
	p.Target = *read.Target
	if len(read.Source.Items) == 0 {
		e.Add("form", "A origem não contém trabalho ativo para copiar.")
		return p, e
	}
	if len(read.Source.Items) > reuseMaxSessions {
		e.Add("form", "A origem excede o limite técnico de 64 sessões; nada foi copiado.")
		return p, e
	}
	target := read.Target
	newWeek := target.Configuration.ExistingPlanID == nil
	planID := uuid.Nil
	if newWeek {
		if prior != nil && prior.Proposal.NewWeek {
			planID = prior.Proposal.TargetPlanID
		} else {
			planID = uuid.New()
		}
		if !validTrainingText(strings.TrimSpace(target.Selector.Title), 2, 180) {
			e.Add("week_title", "Indique o título da nova semana, entre 2 e 180 caracteres.")
		}
		if !validTrainingText(strings.TrimSpace(target.Selector.Description), 0, 4000) {
			e.Add("week_description", "A descrição deve ter até 4000 caracteres.")
		}
	} else {
		planID = *target.Configuration.ExistingPlanID
	}
	p.Proposal = reuseProposal{TargetPlanID: planID, NewWeek: newWeek, Destinations: []reuseDestination{}, Events: []reuseEvent{}}
	if read.Source.Items[0].FragmentKind != "" {
		if newWeek || read.TargetGraph == nil || target.Existing == nil {
			e.Add("target_session_id", "Escolha uma sessão de destino existente.")
			return p, e
		}
		if target.Existing.Session.EntryKind != "TRAINING" && target.Existing.Session.EntryKind != "COMPETITION" {
			e.Add("target_session_id", "Escolha uma sessão de treino ou competição para acrescentar este conteúdo.")
			return p, e
		}
		if !focusedEditEligible(target.Existing.Session, read.DecisionAt) {
			e.Add("target_session_id", "A sessão de destino já não pode receber alterações.")
			return p, e
		}
		destination, event, err := appendReuseFragment(read.Source.Items[0], *read.TargetGraph, target.Selector.SegmentID, prior)
		if err != nil {
			if err == errFocusedWorkoutLimit {
				e.Add("form", "O destino ultrapassaria os limites técnicos do treino. Escolha outro destino ou reduza a origem.")
				return p, e
			}
			field := "target_segment_id"
			if read.Source.Items[0].FragmentKind == "SEGMENT" {
				field = "target_session_id"
			}
			e.Add(field, "Escolha um destino compatível que permita acrescentar este conteúdo.")
			return p, e
		}
		destination.NativeNullDate = !target.Existing.Session.ScheduledOn.Valid
		destination.ModalityID = target.Existing.Session.ModalityID
		p.Proposal.Destinations = append(p.Proposal.Destinations, destination)
		event.SourceID = read.Source.Routine.ID
		event.SourceUpdatedAt = read.Source.Routine.UpdatedAt.Time.UTC()
		event.ID = reuseEventID(prior, event.SourceKind, event.SourceID, event.DestinationKind, event.DestinationID)
		p.Proposal.Events = append(p.Proposal.Events, event)
	} else {
		if target.Selector.SessionID != uuid.Nil || target.Selector.SegmentID != uuid.Nil {
			e.Add("target_session_id", "Este conteúdo cria sessões na semana; remova a seleção de uma sessão ou segmento existente.")
		}
		for _, item := range read.Source.Items {
			schedule, fields := normalizeReuseSchedule(item, values, target.Configuration.WeekStart.Time)
			for name, message := range fields {
				e.Add(name, message)
			}
			if len(fields) > 0 {
				continue
			}
			context := reuseTargetContext(actor, read, planID, schedule.Date)
			destination, err := independentReuseGraph(item, context, schedule, reusePriorDestination(prior, item.Key))
			if err != nil {
				e.Add("form", "Não foi possível preparar uma cópia completa desta origem.")
				continue
			}
			p.Proposal.Destinations = append(p.Proposal.Destinations, destination)
			event := reuseEvent{SourceKind: read.Source.Selector.Kind, DestinationKind: "SESSION", DestinationID: destination.Graph.SessionID}
			if read.Source.Routine != nil {
				event.SourceKind = "ROUTINE"
				event.SourceID = read.Source.Routine.ID
				event.SourceUpdatedAt = read.Source.Routine.UpdatedAt.Time.UTC()
			} else {
				event.SourceID = item.Raw.Session.ID
				event.SourceUpdatedAt = item.Raw.Session.UpdatedAt.Time.UTC()
			}
			event.ID = reuseEventID(prior, event.SourceKind, event.SourceID, event.DestinationKind, event.DestinationID)
			p.Proposal.Events = append(p.Proposal.Events, event)
		}
	}
	if newWeek && read.Source.Plan != nil {
		event := reuseEvent{SourceKind: "WEEK", SourceID: read.Source.Plan.ID, SourceUpdatedAt: read.Source.Plan.UpdatedAt.Time.UTC(), DestinationKind: "WEEK", DestinationID: planID}
		event.ID = reuseEventID(prior, event.SourceKind, event.SourceID, event.DestinationKind, event.DestinationID)
		p.Proposal.Events = append(p.Proposal.Events, event)
	}
	if len(e) == 0 {
		if err := validateReuseEnvelope(p); err != nil {
			e.Add("form", fmt.Sprintf("Não foi possível preparar a operação completa: %s.", reuseErrorText(err)))
		}
	}
	return p, e
}
func reuseErrorText(err error) string {
	if err == errFocusedWorkoutLimit {
		return "limite técnico excedido"
	}
	return "reveja a origem e o destino"
}
func appendReuseFragment(item reuseSourceItem, target focusedWorkoutGraph, segmentID uuid.UUID, prior *reuseReviewPayload) (reuseDestination, reuseEvent, error) {
	out := reuseDestination{Key: item.Key, Origins: map[uuid.UUID]reusePinOrigin{}}
	event := reuseEvent{SourceKind: "ROUTINE", DestinationKind: string(item.FragmentKind)}
	graph, err := cloneReuseGraph(target)
	if err != nil {
		return out, event, err
	}
	fragment, err := cloneReuseGraph(item.Graph)
	if err != nil || len(fragment.Activities) != 1 {
		return out, event, errFocusedWorkoutInvalid
	}
	used := map[string]bool{}
	for _, a := range graph.Activities {
		used[a.Key] = true
		for _, b := range a.Phases {
			used[b.Key] = true
		}
	}
	var previous *focusedWorkoutGraph
	if d := reusePriorDestination(prior, item.Key); d != nil {
		previous = &d.Graph
	}
	originalActivity := fragment.Activities[0]
	originByBlock := map[uuid.UUID]reusePinOrigin{}
	if item.FragmentKind == "SEGMENT" {
		if segmentID != uuid.Nil || len(graph.Activities) >= focusedMaxActivities {
			return out, event, errFocusedWorkoutInvalid
		}
		fragment.Activities[0].Key = focusedGraphNewKey("a_", used)
		allocateFocusedGraphIDs(&fragment, previous)
		activity := fragment.Activities[0]
		for bi, b := range activity.Phases {
			if b.Water != nil {
				originByBlock[b.ID] = item.Origins[focusedGraphPhasePrefix(originalActivity.Key, originalActivity.Phases[bi].Key)]
			}
		}
		graph.Activities = append(graph.Activities, activity)
		event.DestinationID = activity.ID
	} else if item.FragmentKind == "BLOCK" {
		if len(originalActivity.Phases) != 1 {
			return out, event, errFocusedWorkoutInvalid
		}
		found := false
		for ai, a := range graph.Activities {
			if a.ID != segmentID {
				continue
			}
			found = true
			b := originalActivity.Phases[0]
			if b.Water != nil && a.Modality != "WATER" || b.Gym != nil && a.Modality != "GYM" {
				return out, event, errFocusedWorkoutInvalid
			}
			fragment.Activities[0].Key = a.Key
			fragment.Activities[0].Phases[0].Key = focusedGraphNewKey("p_", used)
			allocateFocusedGraphIDs(&fragment, previous)
			copied := fragment.Activities[0].Phases[0]
			graph.Activities[ai].Phases = append(graph.Activities[ai].Phases, copied)
			event.DestinationID = copied.ID
			if copied.Water != nil {
				originByBlock[copied.ID] = item.Origins[focusedGraphPhasePrefix(originalActivity.Key, b.Key)]
			}
		}
		if !found {
			return out, event, errFocusedWorkoutInvalid
		}
	} else {
		return out, event, errFocusedWorkoutInvalid
	}
	if err = validateReuseGraphLimits(graph); err != nil {
		return out, event, err
	}
	if _, err = buildFocusedEditDiff(target, graph, focusedGraphDraft{}); err != nil {
		return out, event, err
	}
	if _, err = focusedGraphCheckedTotals(graph); err != nil {
		return out, event, err
	}
	out.Graph, out.Origins = graph, originByBlock
	return out, event, nil
}
