package handlers

import (
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/google/uuid"
)

const trainingPlacementsPath = "/admin/treinos/estruturados/colocacoes"
const trainingPlacementReviewPurpose = "mycfc/training-placement-review/v1"
const trainingPlacementMaxTimeline = 512
const trainingPlacementMaxFormBytes = 16 << 10

var errTrainingPlacementInvalid = errors.New("invalid training placement review")
var errTrainingPlacementChanged = errors.New("training placement changed; review required")
var errTrainingPlacementLimit = errors.New("training placement review limit exceeded")

type trainingPlacementKind string

const (
	placementJoin      trainingPlacementKind = "JOIN"
	placementLeave     trainingPlacementKind = "LEAVE"
	placementMove      trainingPlacementKind = "MOVE"
	placementTemporary trainingPlacementKind = "TEMPORARY"
)

// Context IDs are resolved by protected reads; these fields are not authority
// merely because a form or a signed review carries them.
type trainingPlacementCommand struct {
	ID                uuid.UUID             `json:"id"`
	SubjectID         uuid.UUID             `json:"subject_id"`
	ProgrammeID       uuid.UUID             `json:"programme_id"`
	ContextGroupID    uuid.UUID             `json:"context_group_id"`
	SeasonID          uuid.UUID             `json:"season_id"`
	Kind              trainingPlacementKind `json:"kind"`
	SourcePlacementID uuid.UUID             `json:"source_placement_id"`
	TargetGroupID     uuid.UUID             `json:"target_group_id"`
	EffectiveOn       string                `json:"effective_on"`
	TemporaryUntil    string                `json:"temporary_until"`
	ExpectedRevision  int64                 `json:"expected_revision"`
}

type trainingPlacementContext struct {
	ActorID, SubjectID, ProgrammeID, GroupID uuid.UUID
}

// Retain every submitted native value. The parser neither chooses a season nor
// invents a group/date when a selection is absent or invalid.
func parseTrainingPlacementForm(v url.Values, context trainingPlacementContext) (trainingPlacementCommand, url.Values, validation.FieldErrors) {
	raw := cloneFocusedValues(v)
	delete(raw, "gorilla.csrf.Token")
	errors := validation.FieldErrors{}
	out := trainingPlacementCommand{SubjectID: context.SubjectID, ProgrammeID: context.ProgrammeID, ContextGroupID: context.GroupID}
	allowed := map[string]bool{"command_id": true, "group_id": true, "person_id": true, "head_revision": true, "kind": true, "season_id": true, "source_placement_id": true, "target_group_id": true, "effective_on": true, "temporary_until": true}
	if len(raw.Encode()) > trainingPlacementMaxFormBytes {
		errors.Add("form", "Os dados excedem o limite deste formulário.")
		return out, raw, errors
	}
	for k, values := range raw {
		if !allowed[k] || len(values) != 1 {
			errors.Add("form", "O formulário contém campos repetidos ou não reconhecidos.")
		}
	}
	if context.ActorID == uuid.Nil || context.SubjectID == uuid.Nil || context.ProgrammeID == uuid.Nil || context.GroupID == uuid.Nil {
		errors.Add("form", "Não foi possível confirmar o contexto desta pessoa.")
	}
	id := func(name string, optional bool) uuid.UUID {
		s := raw.Get(name)
		if optional && s == "" {
			return uuid.Nil
		}
		parsed, e := uuid.Parse(s)
		if e != nil || parsed == uuid.Nil || parsed.String() != s {
			errors.Add(name, "Selecione uma opção válida.")
			return uuid.Nil
		}
		return parsed
	}
	out.ID = id("command_id", false)
	out.SeasonID = id("season_id", false)
	out.SourcePlacementID = id("source_placement_id", true)
	out.TargetGroupID = id("target_group_id", true)
	if id("person_id", false) != context.SubjectID || id("group_id", false) != context.GroupID {
		errors.Add("form", "O contexto mudou. Recarregue a pessoa antes de continuar.")
	}
	revision, e := strconv.ParseInt(raw.Get("head_revision"), 10, 64)
	if e != nil || revision < 0 || strconv.FormatInt(revision, 10) != raw.Get("head_revision") {
		errors.Add("head_revision", "Recarregue o estado atual antes de continuar.")
	} else {
		out.ExpectedRevision = revision
	}
	out.Kind = trainingPlacementKind(raw.Get("kind"))
	out.EffectiveOn = raw.Get("effective_on")
	out.TemporaryUntil = raw.Get("temporary_until")
	if !validWorkspaceDate(out.EffectiveOn) {
		errors.Add("effective_on", "Indique uma data de início válida.")
	}
	switch out.Kind {
	case placementJoin:
		if out.SourcePlacementID != uuid.Nil {
			errors.Add("source_placement_id", "Uma entrada nova não substitui outra colocação.")
		}
		if out.TargetGroupID == uuid.Nil {
			errors.Add("target_group_id", "Selecione o grupo de destino.")
		}
	case placementLeave:
		if out.SourcePlacementID == uuid.Nil {
			errors.Add("source_placement_id", "Selecione a colocação que termina.")
		}
		if out.TargetGroupID != uuid.Nil {
			errors.Add("target_group_id", "Uma saída não tem grupo de destino.")
		}
	case placementMove, placementTemporary:
		if out.SourcePlacementID == uuid.Nil {
			errors.Add("source_placement_id", "Selecione a colocação de origem.")
		}
		if out.TargetGroupID == uuid.Nil {
			errors.Add("target_group_id", "Selecione o grupo de destino.")
		}
	default:
		errors.Add("kind", "Selecione a alteração a realizar.")
	}
	if out.Kind == placementTemporary {
		if !validWorkspaceDate(out.TemporaryUntil) {
			errors.Add("temporary_until", "Indique o último dia no grupo temporário.")
		} else if validWorkspaceDate(out.EffectiveOn) && out.TemporaryUntil < out.EffectiveOn {
			errors.Add("temporary_until", "O último dia não pode ser anterior ao início.")
		}
	} else if out.TemporaryUntil != "" {
		errors.Add("temporary_until", "Indique uma data final apenas para uma mudança temporária.")
	}
	return out, raw, errors
}

func (c trainingPlacementCommand) validate() error {
	context := trainingPlacementContext{ActorID: c.SubjectID, SubjectID: c.SubjectID, ProgrammeID: c.ProgrammeID, GroupID: c.ContextGroupID}
	parsed, _, errors := parseTrainingPlacementForm(c.values(), context)
	if !errors.Empty() || parsed != c {
		return errTrainingPlacementInvalid
	}
	return nil
}
func (c trainingPlacementCommand) values() url.Values {
	optional := func(id uuid.UUID) string {
		if id == uuid.Nil {
			return ""
		}
		return id.String()
	}
	return url.Values{"command_id": {c.ID.String()}, "person_id": {c.SubjectID.String()}, "group_id": {c.ContextGroupID.String()}, "head_revision": {strconv.FormatInt(c.ExpectedRevision, 10)}, "kind": {string(c.Kind)}, "season_id": {c.SeasonID.String()}, "source_placement_id": {optional(c.SourcePlacementID)}, "target_group_id": {optional(c.TargetGroupID)}, "effective_on": {c.EffectiveOn}, "temporary_until": {c.TemporaryUntil}}
}

type trainingPlacementSpan struct {
	ID         uuid.UUID `json:"id"`
	GroupID    uuid.UUID `json:"group_id"`
	GroupName  string    `json:"group_name"`
	StartsOn   string    `json:"starts_on"`
	EndsOn     string    `json:"ends_on"`
	Version    int64     `json:"version"`
	Superseded bool      `json:"superseded"`
	Cancelled  bool      `json:"cancelled"`
}

// Proposed rows describe dates, not allocated database IDs. The one command
// creates its actual destination/return IDs atomically and retains them on replay.
type trainingPlacementProjection struct {
	Source      *trainingPlacementSpan `json:"source"`
	Destination *trainingPlacementSpan `json:"destination"`
	Return      *trainingPlacementSpan `json:"return"`
}

func projectTrainingPlacement(c trainingPlacementCommand, source *trainingPlacementSpan, targetName string) (trainingPlacementProjection, error) {
	var out trainingPlacementProjection
	if c.validate() != nil {
		return out, errTrainingPlacementInvalid
	}
	if c.Kind == placementJoin {
		if source != nil {
			return out, errTrainingPlacementInvalid
		}
		out.Destination = &trainingPlacementSpan{GroupID: c.TargetGroupID, GroupName: targetName, StartsOn: c.EffectiveOn}
		return out, nil
	}
	if source == nil || source.ID != c.SourcePlacementID || source.ID == uuid.Nil || source.GroupID == uuid.Nil || source.Cancelled || source.Superseded || !validWorkspaceDate(source.StartsOn) || source.EndsOn != "" && (!validWorkspaceDate(source.EndsOn) || source.EndsOn < source.StartsOn) || c.EffectiveOn < source.StartsOn || source.EndsOn != "" && c.EffectiveOn > source.EndsOn {
		return out, errTrainingPlacementChanged
	}
	if c.Kind != placementLeave && source.GroupID == c.TargetGroupID {
		return out, errTrainingPlacementInvalid
	}
	retained := *source
	day, _ := time.Parse("2006-01-02", c.EffectiveOn)
	if c.EffectiveOn == source.StartsOn {
		retained.Superseded = true
	} else {
		retained.EndsOn = day.AddDate(0, 0, -1).Format("2006-01-02")
	}
	out.Source = &retained
	if c.Kind == placementLeave {
		return out, nil
	}
	out.Destination = &trainingPlacementSpan{GroupID: c.TargetGroupID, GroupName: targetName, StartsOn: c.EffectiveOn, EndsOn: source.EndsOn}
	if c.Kind == placementTemporary {
		end, _ := time.Parse("2006-01-02", c.TemporaryUntil)
		returnOn := end.AddDate(0, 0, 1).Format("2006-01-02")
		if !validWorkspaceDate(returnOn) || source.EndsOn != "" && returnOn > source.EndsOn {
			return trainingPlacementProjection{}, errTrainingPlacementChanged
		}
		out.Destination.EndsOn = c.TemporaryUntil
		out.Return = &trainingPlacementSpan{GroupID: source.GroupID, GroupName: source.GroupName, StartsOn: returnOn, EndsOn: source.EndsOn}
	}
	return out, nil
}
