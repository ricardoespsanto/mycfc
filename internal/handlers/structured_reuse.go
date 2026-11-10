package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

const reuseMaxSessions = 64
const reuseReviewPurpose = "mycfc/structured-reuse/v1"

var errReuseChanged = errors.New("reuse source or destination changed")
var errReuseDestinationChanged = errors.New("retained reuse destination has changed")
var errReuseNoWork = errors.New("reuse source contains no active work")

type reuseSourceSelector struct {
	Kind      string    `json:"kind"`
	PlanID    uuid.UUID `json:"plan_id"`
	SessionID uuid.UUID `json:"session_id"`
	RoutineID uuid.UUID `json:"routine_id"`
	Day       string    `json:"day"`
}

func (s reuseSourceSelector) validate() error {
	switch s.Kind {
	case "SESSION":
		if s.PlanID != uuid.Nil && s.SessionID != uuid.Nil && s.RoutineID == uuid.Nil && s.Day == "" {
			return nil
		}
	case "DAY":
		if s.PlanID != uuid.Nil && s.SessionID == uuid.Nil && s.RoutineID == uuid.Nil && validWorkspaceDate(s.Day) {
			return nil
		}
	case "WEEK":
		if s.PlanID != uuid.Nil && s.SessionID == uuid.Nil && s.RoutineID == uuid.Nil && s.Day == "" {
			return nil
		}
	case "ROUTINE":
		if s.RoutineID != uuid.Nil && s.PlanID == uuid.Nil && s.SessionID == uuid.Nil && s.Day == "" {
			return nil
		}
	}
	return errFocusedWorkoutInvalid
}

type reuseTargetSelector struct {
	GroupID     uuid.UUID `json:"group_id"`
	SeasonID    uuid.UUID `json:"season_id"`
	WeekStart   string    `json:"week_start"`
	SessionID   uuid.UUID `json:"session_id"`
	SegmentID   uuid.UUID `json:"segment_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Load        *int16    `json:"load"`
}
type reusePinOrigin struct {
	NativePlanID  *uuid.UUID `json:"native_plan_id"`
	NativeBlockID *uuid.UUID `json:"native_block_id"`
	RoutineID     *uuid.UUID `json:"routine_id"`
	RoutinePath   []int32    `json:"routine_path"`
}
type reuseSourceItem struct {
	Key             string              `json:"key"`
	Graph           focusedWorkoutGraph `json:"graph"`
	Raw             *focusedEditRaw     `json:"raw"`
	DurationSeconds *int64              `json:"duration_seconds"`
	// A fragment has no appointment. Its actual routine kind identifies the
	// destination level without inventing a source session date or clock.
	FragmentKind dbgen.TrainingRoutineKind `json:"fragment_kind"`
	Origins      map[string]reusePinOrigin `json:"origins"`
}
type reuseSourceMaterial struct {
	Selector reuseSourceSelector       `json:"selector"`
	Plan     *dbgen.GetReusePlanRow    `json:"plan"`
	Routine  *dbgen.GetReuseRoutineRow `json:"routine"`
	Items    []reuseSourceItem         `json:"items"`
}
type reuseTargetMaterial struct {
	Selector      reuseTargetSelector            `json:"selector"`
	Configuration dbgen.GetReuseTargetContextRow `json:"configuration"`
	Plan          *dbgen.GetReusePlanRow         `json:"plan"`
	Existing      *focusedEditRaw                `json:"existing"`
}
type reuseDestination struct {
	Key            string                       `json:"key"`
	Graph          focusedWorkoutGraph          `json:"graph"`
	NativeNullDate bool                         `json:"native_null_date"`
	ModalityID     *uuid.UUID                   `json:"modality_id"`
	Origins        map[uuid.UUID]reusePinOrigin `json:"origins"`
}
type reuseEvent struct {
	ID              uuid.UUID `json:"id"`
	SourceKind      string    `json:"source_kind"`
	SourceID        uuid.UUID `json:"source_id"`
	SourceUpdatedAt time.Time `json:"source_updated_at"`
	DestinationKind string    `json:"destination_kind"`
	DestinationID   uuid.UUID `json:"destination_id"`
}
type reuseProposal struct {
	TargetPlanID uuid.UUID          `json:"target_plan_id"`
	NewWeek      bool               `json:"new_week"`
	Destinations []reuseDestination `json:"destinations"`
	Events       []reuseEvent       `json:"events"`
}
type reuseReviewPayload struct {
	ActorID     uuid.UUID                `json:"actor_id"`
	Incarnation uuid.UUID                `json:"incarnation"`
	Epoch       int64                    `json:"epoch"`
	Source      reuseSourceMaterial      `json:"source"`
	Target      reuseTargetMaterial      `json:"target"`
	References  focusedGraphProfileFacts `json:"references"`
	Proposal    reuseProposal            `json:"proposal"`
	Values      url.Values               `json:"values"`
}
type reuseReviewed struct {
	Payload   reuseReviewPayload
	verified  bool
	expiresAt time.Time
}

func reuseBoundedJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(raw) > focusedWorkoutMaxRequestBytes {
		return nil, errFocusedWorkoutLimit
	}
	return raw, nil
}
func reuseDigest(v any) (string, error) {
	raw, err := reuseBoundedJSON(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func cloneReuseGraph(p focusedWorkoutGraph) (focusedWorkoutGraph, error) {
	raw, err := reuseBoundedJSON(p)
	if err != nil {
		return focusedWorkoutGraph{}, err
	}
	var copy focusedWorkoutGraph
	err = json.Unmarshal(raw, &copy)
	return copy, err
}
func reuseNativeOrigins(p focusedWorkoutGraph) map[string]reusePinOrigin {
	origins := map[string]reusePinOrigin{}
	for _, a := range p.Activities {
		for _, b := range a.Phases {
			if b.Water != nil {
				origins[focusedGraphPhasePrefix(a.Key, b.Key)] = reusePinOrigin{NativePlanID: focusedPointer(p.Context.PlanID), NativeBlockID: focusedPointer(b.ID)}
			}
		}
	}
	return origins
}
func independentReuseGraph(item reuseSourceItem, c FocusedWorkoutContext, schedule TrainingSessionSchedule, prior *reuseDestination) (reuseDestination, error) {
	out := reuseDestination{Key: item.Key, NativeNullDate: item.Raw != nil && !item.Raw.Session.ScheduledOn.Valid, Origins: map[uuid.UUID]reusePinOrigin{}}
	graph, err := cloneReuseGraph(item.Graph)
	if err != nil {
		return out, err
	}
	if err = schedule.ValidateKind(string(graph.Kind)); err != nil {
		return out, err
	}
	if schedule.Band != nil {
		schedule.Band = focusedPointer(*schedule.Band)
	}
	graph.Context, graph.Schedule = c, schedule
	graph.References = focusedGraphProfileFacts{}
	var previous *focusedWorkoutGraph
	if prior != nil {
		if prior.Key != item.Key {
			return out, errFocusedWorkoutInvalid
		}
		previous = &prior.Graph
	}
	allocateFocusedGraphIDs(&graph, previous)
	for _, a := range graph.Activities {
		for _, b := range a.Phases {
			if b.Water != nil {
				origin, ok := item.Origins[focusedGraphPhasePrefix(a.Key, b.Key)]
				if !ok {
					return out, errFocusedWorkoutInvalid
				}
				out.Origins[b.ID] = origin
			}
		}
	}
	if item.Raw != nil {
		if item.Raw.Session.ModalityID != nil {
			out.ModalityID = focusedPointer(*item.Raw.Session.ModalityID)
		}
	}
	if _, err = focusedEditEntities(graph); err != nil {
		return out, err
	}
	out.Graph = graph
	return out, nil
}
