package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

var errTrainingPlacementPolicy = &trainingPlacementFieldFailure{Field: "effective_on", Message: "Há instruções já iniciadas ou sem hora definida neste dia. Reveja esta data antes de continuar."}
var errTrainingPlacementReturn = &trainingPlacementFieldFailure{Field: "temporary_until", Message: "Não foi possível confirmar o regresso ao grupo anterior nesta data. Reveja as datas e a elegibilidade."}

type trainingPlacementFieldFailure struct{ Field, Message string }

func (e *trainingPlacementFieldFailure) Error() string { return e.Message }

type trainingPlacementReadQueries interface {
	TrainingPlacementSubjectForReview(context.Context, dbgen.TrainingPlacementSubjectForReviewParams) (dbgen.TrainingPlacementSubjectForReviewRow, error)
	TrainingPlacementSourceForReview(context.Context, dbgen.TrainingPlacementSourceForReviewParams) (dbgen.TrainingPlacementSourceForReviewRow, error)
	TrainingPlacementGroupChoices(context.Context, dbgen.TrainingPlacementGroupChoicesParams) ([]dbgen.TrainingPlacementGroupChoicesRow, error)
	TrainingPlacementTimeline(context.Context, dbgen.TrainingPlacementTimelineParams) ([]dbgen.TrainingPlacementTimelineRow, error)
	TrainingPlacementInstructionReview(context.Context, dbgen.TrainingPlacementInstructionReviewParams) ([]dbgen.TrainingPlacementInstructionReviewRow, error)
	TrainingPlacementReturnForReview(context.Context, dbgen.TrainingPlacementReturnForReviewParams) ([]dbgen.TrainingPlacementReturnForReviewRow, error)
}

// Read facts come only from scoped Data queries. Runtime admission is the
// concrete store's prerequisite; a proof never authorizes an unverified reader.
type trainingPlacementRead struct {
	State        db.TrainingScheduleState                      `json:"state"`
	Subject      *dbgen.TrainingPlacementSubjectForReviewRow   `json:"subject"`
	Source       *dbgen.TrainingPlacementSourceForReviewRow    `json:"source"`
	Groups       []dbgen.TrainingPlacementGroupChoicesRow      `json:"groups"`
	Timeline     []dbgen.TrainingPlacementTimelineRow          `json:"timeline"`
	Instructions []dbgen.TrainingPlacementInstructionReviewRow `json:"instructions"`
	Returns      []dbgen.TrainingPlacementReturnForReviewRow   `json:"returns"`
	DecisionOn   string                                        `json:"decision_on"`
}

func placementDate(value string) pgtype.Date {
	d, e := time.Parse("2006-01-02", value)
	return pgtype.Date{Time: d, Valid: e == nil && validWorkspaceDate(value)}
}
func placementDateString(d pgtype.Date) string {
	if !d.Valid || d.InfinityModifier != pgtype.Finite {
		return ""
	}
	return d.Time.Format("2006-01-02")
}
func placementClock(t pgtype.Timestamptz) (time.Time, error) {
	if !t.Valid || t.InfinityModifier != pgtype.Finite || t.Time.IsZero() {
		return time.Time{}, errTrainingPlacementInvalid
	}
	return t.Time, nil
}
func (r trainingPlacementRead) revision() int64 {
	if r.Subject != nil {
		return r.Subject.Revision
	}
	if r.Source != nil {
		return r.Source.Revision
	}
	return -1
}
func (r trainingPlacementRead) decisionAt() (time.Time, error) {
	if r.Subject != nil {
		return placementClock(r.Subject.DecisionAt)
	}
	if r.Source != nil {
		return placementClock(r.Source.DecisionAt)
	}
	return time.Time{}, errTrainingPlacementInvalid
}
func (r trainingPlacementRead) sourceSpan() *trainingPlacementSpan {
	if r.Source == nil {
		return nil
	}
	s := r.Source
	return &trainingPlacementSpan{ID: s.ID, GroupID: s.GroupID, GroupName: s.GroupName, StartsOn: placementDateString(s.StartsOn), EndsOn: placementDateString(s.EndsOn), Version: s.Version, Superseded: s.SupersededByChangeID != nil, Cancelled: s.CancelledByChangeID != nil}
}
func (r trainingPlacementRead) digest() (string, error) {
	// Clock reads advance; their actual date/decision flags are binding instead.
	// The save still uses the actual final DB clock, never this normalization.
	copy := r
	if r.Subject != nil {
		s := *r.Subject
		s.DecisionAt = pgtype.Timestamptz{}
		copy.Subject = &s
	}
	if r.Source != nil {
		s := *r.Source
		s.DecisionAt = pgtype.Timestamptz{}
		copy.Source = &s
	}
	copy.Instructions = append([]dbgen.TrainingPlacementInstructionReviewRow{}, r.Instructions...)
	for i := range copy.Instructions {
		copy.Instructions[i].DecisionAt = pgtype.Timestamptz{}
	}
	raw, e := json.Marshal(copy)
	if e != nil {
		return "", e
	}
	if len(raw) > focusedWorkoutMaxRequestBytes {
		return "", errTrainingPlacementLimit
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func readTrainingPlacement(ctx context.Context, q trainingPlacementReadQueries, actor uuid.UUID, c trainingPlacementCommand, state db.TrainingScheduleState, refreshRevision bool) (trainingPlacementRead, error) {
	out := trainingPlacementRead{State: state, Groups: []dbgen.TrainingPlacementGroupChoicesRow{}, Timeline: []dbgen.TrainingPlacementTimelineRow{}, Instructions: []dbgen.TrainingPlacementInstructionReviewRow{}, Returns: []dbgen.TrainingPlacementReturnForReviewRow{}}
	if c.validate() != nil || actor == uuid.Nil || state.DatabaseIncarnation == uuid.Nil || state.StateEpoch < 1 || state.RequiredReader < 4 {
		return out, errTrainingPlacementInvalid
	}
	if c.SourcePlacementID != uuid.Nil {
		s, e := q.TrainingPlacementSourceForReview(ctx, dbgen.TrainingPlacementSourceForReviewParams{ActorID: actor, SourcePlacementID: c.SourcePlacementID})
		if e != nil {
			return out, e
		}
		if s.SubjectID != c.SubjectID || s.ProgrammeID != c.ProgrammeID || s.ID != c.SourcePlacementID {
			return out, errTrainingPlacementInvalid
		}
		out.Source = &s
	}
	// A LEAVE needs actual protected source facts, not a destination episode.
	if c.Kind != placementLeave {
		s, e := q.TrainingPlacementSubjectForReview(ctx, dbgen.TrainingPlacementSubjectForReviewParams{ActorID: actor, SubjectID: c.SubjectID, GroupID: c.TargetGroupID, SeasonID: c.SeasonID, EffectiveOn: placementDate(c.EffectiveOn)})
		if e != nil {
			return out, e
		}
		if s.SubjectID != c.SubjectID || s.ProgrammeID != c.ProgrammeID || s.GroupID != c.TargetGroupID || s.SeasonID != c.SeasonID || s.MembershipID == uuid.Nil {
			return out, errTrainingPlacementInvalid
		}
		out.Subject = &s
	}
	decisionAt, e := out.decisionAt()
	if e != nil {
		return out, e
	}
	out.DecisionOn = decisionAt.In(lisbonLocation()).Format("2006-01-02")
	if out.Source != nil && out.Source.Revision != out.revision() || !refreshRevision && out.revision() != c.ExpectedRevision {
		return out, errTrainingPlacementChanged
	}
	c.ExpectedRevision = out.revision()
	groups, e := q.TrainingPlacementGroupChoices(ctx, dbgen.TrainingPlacementGroupChoicesParams{ActorID: actor, ProgrammeID: c.ProgrammeID, SeasonID: c.SeasonID, RowLimit: trainingPlacementMaxTimeline + 1})
	if e != nil {
		return out, e
	}
	if len(groups) > trainingPlacementMaxTimeline {
		return out, errTrainingPlacementLimit
	}
	out.Groups = groups
	seenGroups := map[uuid.UUID]bool{}
	target := ""
	contextAllowed := c.ContextGroupID == c.TargetGroupID || out.Source != nil && c.ContextGroupID == out.Source.GroupID
	for _, g := range groups {
		if g.ID == uuid.Nil || seenGroups[g.ID] || g.ProgrammeID != c.ProgrammeID || g.SeasonID != c.SeasonID || placementDateString(g.SeasonStartsOn) == "" || placementDateString(g.SeasonEndsOn) == "" {
			return out, errTrainingPlacementInvalid
		}
		seenGroups[g.ID] = true

		if c.TargetGroupID == g.ID {
			target = g.Name
		}
		if c.EffectiveOn < placementDateString(g.SeasonStartsOn) || c.EffectiveOn > placementDateString(g.SeasonEndsOn) {
			return out, &trainingPlacementFieldFailure{Field: "effective_on", Message: "Escolha uma data dentro da época selecionada."}
		}
	}
	if len(groups) == 0 || !contextAllowed || c.Kind != placementLeave && target == "" {
		return out, errTrainingPlacementChanged
	}
	timeline, e := q.TrainingPlacementTimeline(ctx, dbgen.TrainingPlacementTimelineParams{ActorID: actor, SubjectID: c.SubjectID, ProgrammeID: c.ProgrammeID, RowLimit: trainingPlacementMaxTimeline + 1})
	if e != nil {
		return out, e
	}
	if len(timeline) > trainingPlacementMaxTimeline {
		return out, errTrainingPlacementLimit
	}
	out.Timeline = timeline
	seen := map[uuid.UUID]bool{}
	for _, row := range timeline {
		if row.ID == uuid.Nil || seen[row.ID] || row.SubjectUserID == nil || *row.SubjectUserID != c.SubjectID || row.ProgrammeID != c.ProgrammeID || row.Revision != c.ExpectedRevision {
			return out, errTrainingPlacementChanged
		}
		seen[row.ID] = true
	}
	if c.SourcePlacementID != uuid.Nil && !seen[c.SourcePlacementID] {
		return out, errTrainingPlacementChanged
	}
	projection, e := projectTrainingPlacement(c, out.sourceSpan(), target)
	if e != nil {
		return out, e
	}
	if out.Source != nil {
		instructions, e := q.TrainingPlacementInstructionReview(ctx, dbgen.TrainingPlacementInstructionReviewParams{ActorID: actor, SourcePlacementID: c.SourcePlacementID, EffectiveOn: placementDate(c.EffectiveOn)})
		if e != nil {
			return out, e
		}
		if len(instructions) > trainingPublicationReviewMaxTuples {
			return out, errTrainingPlacementLimit
		}
		out.Instructions = instructions
		seenInstructions := map[uuid.UUID]bool{}
		for _, i := range instructions {
			if i.PrescriptionID == uuid.Nil || seenInstructions[i.PrescriptionID] {
				return out, errTrainingPlacementInvalid
			}
			seenInstructions[i.PrescriptionID] = true
			if i.RequiresPolicy {
				return out, errTrainingPlacementPolicy
			}
		}
	}
	if projection.Return != nil {
		returns, e := q.TrainingPlacementReturnForReview(ctx, dbgen.TrainingPlacementReturnForReviewParams{ActorID: actor, SubjectID: c.SubjectID, GroupID: projection.Return.GroupID, ReturnOn: placementDate(projection.Return.StartsOn)})
		if e != nil {
			return out, e
		}
		if len(returns) != 1 || returns[0].MembershipID == uuid.Nil || returns[0].SeasonID == uuid.Nil {
			return out, errTrainingPlacementReturn
		}
		returned := returns[0]
		day := projection.Return.StartsOn
		seasonStart, seasonEnd := placementDateString(returned.SeasonStartsOn), placementDateString(returned.SeasonEndsOn)
		episodeStart, episodeEnd := placementDateString(returned.StartsOn), placementDateString(returned.EndsOn)
		if seasonStart == "" || seasonEnd == "" || episodeStart == "" || day < seasonStart || day > seasonEnd || day < episodeStart || returned.EndsOn.Valid && (episodeEnd == "" || day > episodeEnd) {
			return out, errTrainingPlacementReturn
		}
		out.Returns = returns
	}
	// Reject a completed mutation between READ COMMITTED preview statements.
	if out.Source != nil {
		fresh, e := q.TrainingPlacementSourceForReview(ctx, dbgen.TrainingPlacementSourceForReviewParams{ActorID: actor, SourcePlacementID: c.SourcePlacementID})
		if e != nil {
			return out, e
		}
		prior := *out.Source
		prior.DecisionAt = pgtype.Timestamptz{}
		fresh.DecisionAt = pgtype.Timestamptz{}
		if !focusedGraphJSONEqual(prior, fresh) {
			return out, errTrainingPlacementChanged
		}
	}
	if out.Subject != nil {
		fresh, e := q.TrainingPlacementSubjectForReview(ctx, dbgen.TrainingPlacementSubjectForReviewParams{ActorID: actor, SubjectID: c.SubjectID, GroupID: c.TargetGroupID, SeasonID: c.SeasonID, EffectiveOn: placementDate(c.EffectiveOn)})
		if e != nil {
			return out, e
		}
		prior := *out.Subject
		prior.DecisionAt = pgtype.Timestamptz{}
		fresh.DecisionAt = pgtype.Timestamptz{}
		if !focusedGraphJSONEqual(prior, fresh) {
			return out, errTrainingPlacementChanged
		}
	}
	return out, nil
}
func (r trainingPlacementRead) proposal(actor uuid.UUID, c trainingPlacementCommand) (trainingPlacementReviewPayload, error) {
	if c.ExpectedRevision != r.revision() {
		return trainingPlacementReviewPayload{}, errTrainingPlacementChanged
	}
	digest, e := r.digest()
	if e != nil {
		return trainingPlacementReviewPayload{}, e
	}
	target := ""
	for _, g := range r.Groups {
		if g.ID == c.TargetGroupID {
			target = g.Name
		}
	}
	projection, e := projectTrainingPlacement(c, r.sourceSpan(), target)
	if e != nil {
		return trainingPlacementReviewPayload{}, e
	}
	p := trainingPlacementReviewPayload{ActorID: actor, Incarnation: r.State.DatabaseIncarnation, Epoch: r.State.StateEpoch, DecisionOn: r.DecisionOn, Command: c, Source: r.sourceSpan(), TargetName: target, FactsDigest: digest, Projection: projection, Values: c.values()}
	return p, validateTrainingPlacementReview(p)
}
