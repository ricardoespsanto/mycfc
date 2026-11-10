package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const trainingPlacementCancelPurpose = "mycfc/training-placement-cancel-review/v1"

type trainingPlacementCancellation struct {
	ID               uuid.UUID `json:"id"`
	ChangeID         uuid.UUID `json:"change_id"`
	SubjectID        uuid.UUID `json:"subject_id"`
	ProgrammeID      uuid.UUID `json:"programme_id"`
	SeasonID         uuid.UUID `json:"season_id"`
	ContextGroupID   uuid.UUID `json:"context_group_id"`
	ExpectedRevision int64     `json:"expected_revision"`
}

func (c trainingPlacementCancellation) values() url.Values {
	return url.Values{"group_id": {c.ContextGroupID.String()}, "person_id": {c.SubjectID.String()}, "command_id": {""}, "head_revision": {strconv.FormatInt(c.ExpectedRevision, 10)}, "kind": {"CANCEL"}, "season_id": {c.SeasonID.String()}, "source_placement_id": {""}, "target_group_id": {""}, "effective_on": {""}, "temporary_until": {""}, "change_id": {c.ChangeID.String()}, "cancellation_id": {c.ID.String()}}
}
func (c trainingPlacementCancellation) validate() error {
	if c.ID == uuid.Nil || c.ChangeID == uuid.Nil || c.ID == c.ChangeID || c.SubjectID == uuid.Nil || c.ProgrammeID == uuid.Nil || c.SeasonID == uuid.Nil || c.ContextGroupID == uuid.Nil || c.ExpectedRevision < 0 {
		return errTrainingPlacementInvalid
	}
	return nil
}

type trainingPlacementCancellationRead struct {
	State      db.TrainingScheduleState
	Change     dbgen.TrainingPlacementChangeForReviewRow
	PersonName string
	Timeline   []dbgen.TrainingPlacementTimelineRow
	CanCancel  bool // actual Data read, not a caller permission or posted value
}

func (r trainingPlacementCancellationRead) digest() (string, error) {
	copy := r
	copy.Change.DecisionAt = pgtype.Timestamptz{}
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
func (r trainingPlacementCancellationRead) projection() (before, after []trainingPlacementSpan, err error) {
	x := r.Change
	add := func(id, group *uuid.UUID, start, end pgtype.Date, version *int64, suppressed, cancelled *uuid.UUID) {
		if id == nil {
			return
		}
		v := int64(0)
		if version != nil {
			v = *version
		}
		before = append(before, trainingPlacementSpan{ID: *id, GroupID: optionalPlacementUUID(group), StartsOn: placementDateString(start), EndsOn: placementDateString(end), Version: v, Superseded: suppressed != nil, Cancelled: cancelled != nil})
	}
	add(x.SourcePlacementID, x.SourceGroupID, x.SourceStartsOn, x.SourceEndsOn, x.SourceVersion, x.SourceSupersededByChangeID, x.SourceCancelledByChangeID)
	add(x.DestinationPlacementID, x.DestinationGroupID, x.DestinationStartsOn, x.DestinationEndsOn, x.DestinationVersion, x.DestinationSupersededByChangeID, x.DestinationCancelledByChangeID)
	add(x.ReturnPlacementID, x.ReturnGroupID, x.ReturnStartsOn, x.ReturnEndsOn, x.ReturnVersion, x.ReturnSupersededByChangeID, x.ReturnCancelledByChangeID)
	names := map[uuid.UUID]string{}
	for _, row := range r.Timeline {
		names[row.GroupID] = row.GroupName
	}
	after = append([]trainingPlacementSpan{}, before...)
	for i := range before {
		before[i].GroupName = names[before[i].GroupID]
		after[i].GroupName = before[i].GroupName
		if x.SourcePlacementID != nil && after[i].ID == *x.SourcePlacementID {
			after[i].StartsOn = placementDateString(x.OriginalSourceStartsOn)
			after[i].EndsOn = placementDateString(x.OriginalSourceEndsOn)
			after[i].Superseded = false
			after[i].Cancelled = false
			if after[i].StartsOn == "" || after[i].EndsOn != "" && after[i].EndsOn < after[i].StartsOn {
				return nil, nil, errTrainingPlacementInvalid
			}
		} else {
			after[i].Cancelled = true
		}
	}
	return before, after, nil
}
func optionalPlacementUUID(p *uuid.UUID) uuid.UUID {
	if p == nil {
		return uuid.Nil
	}
	return *p
}

type trainingPlacementCancelPayload struct {
	ActorID       uuid.UUID                     `json:"actor_id"`
	Incarnation   uuid.UUID                     `json:"database_incarnation"`
	Epoch         int64                         `json:"state_epoch"`
	DecisionOn    string                        `json:"decision_on"`
	Command       trainingPlacementCancellation `json:"command"`
	EffectiveOn   string                        `json:"effective_on"`
	FactsDigest   string                        `json:"facts_digest"`
	Before, After []trainingPlacementSpan
	Values        url.Values `json:"values"`
}
type trainingPlacementCancelReviewed struct {
	Payload             trainingPlacementCancelPayload
	issuedAt, expiresAt time.Time
	reviewSHA256        string
	verified            bool
}

func (r trainingPlacementCancellationRead) proposal(actor uuid.UUID, c trainingPlacementCancellation) (trainingPlacementCancelPayload, error) {
	var p trainingPlacementCancelPayload
	now, e := placementClock(r.Change.DecisionAt)
	if e != nil {
		return p, e
	}
	if !r.CanCancel || r.Change.CancellationID != nil || r.Change.ID != c.ChangeID || r.Change.SubjectUserID == nil || *r.Change.SubjectUserID != c.SubjectID || r.Change.ProgrammeID != c.ProgrammeID || r.Change.SeasonID != c.SeasonID || r.Change.Revision != c.ExpectedRevision || r.State.RequiredReader < 4 {
		return p, errTrainingPlacementChanged
	}
	before, after, e := r.projection()
	if e != nil {
		return p, e
	}
	digest, e := r.digest()
	if e != nil {
		return p, e
	}
	p = trainingPlacementCancelPayload{ActorID: actor, Incarnation: r.State.DatabaseIncarnation, Epoch: r.State.StateEpoch, DecisionOn: now.In(lisbonLocation()).Format("2006-01-02"), Command: c, EffectiveOn: placementDateString(r.Change.EffectiveOn), FactsDigest: digest, Before: before, After: after, Values: c.values()}
	return p, validateTrainingPlacementCancel(p)
}
func validateTrainingPlacementCancel(p trainingPlacementCancelPayload) error {
	digest, e := hex.DecodeString(p.FactsDigest)
	if p.ActorID == uuid.Nil || p.Incarnation == uuid.Nil || p.Epoch < 1 || p.Command.validate() != nil || !validWorkspaceDate(p.DecisionOn) || !validWorkspaceDate(p.EffectiveOn) || p.DecisionOn >= p.EffectiveOn || e != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != p.FactsDigest || !focusedGraphJSONEqual(p.Values, p.Command.values()) || len(p.Before) < 1 || len(p.Before) > 3 || len(p.Before) != len(p.After) {
		return errTrainingPlacementInvalid
	}
	seen := map[uuid.UUID]bool{}
	for i, b := range p.Before {
		a := p.After[i]
		if b.ID == uuid.Nil || b.GroupID == uuid.Nil || seen[b.ID] || a.ID != b.ID || a.GroupID != b.GroupID || !validWorkspaceDate(b.StartsOn) || !validWorkspaceDate(a.StartsOn) || b.EndsOn != "" && (!validWorkspaceDate(b.EndsOn) || b.EndsOn < b.StartsOn) || a.EndsOn != "" && (!validWorkspaceDate(a.EndsOn) || a.EndsOn < a.StartsOn) {
			return errTrainingPlacementInvalid
		}
		seen[b.ID] = true
	}
	return nil
}
func (s focusedWorkoutSigner) signPlacementCancel(p trainingPlacementCancelPayload, now time.Time) (string, string, error) {
	if e := validateTrainingPlacementCancel(p); e != nil {
		return "", "", e
	}
	raw, e := json.Marshal(p)
	if e != nil {
		return "", "", e
	}
	return s.signNativeFields(raw, p.Values, nil, trainingPlacementCancelPurpose, now)
}
func (s focusedWorkoutSigner) verifyPlacementCancel(raw, token string, actor, group, person uuid.UUID, now time.Time) (trainingPlacementCancelReviewed, error) {
	var out trainingPlacementCancelReviewed
	expiry, e := s.verifyNative(raw, token, trainingPlacementCancelPurpose, now)
	if e != nil {
		return out, errTrainingPlacementInvalid
	}
	var p trainingPlacementCancelPayload
	if focusedStrictJSON([]byte(raw), &p) != nil || p.ActorID != actor || p.Command.ContextGroupID != group || p.Command.SubjectID != person || validateTrainingPlacementCancel(p) != nil {
		return out, errTrainingPlacementInvalid
	}
	encoded, e := json.Marshal(p)
	if e != nil || !bytes.Equal(encoded, []byte(raw)) {
		return out, errTrainingPlacementInvalid
	}
	sum := sha256.Sum256([]byte(token))
	return trainingPlacementCancelReviewed{Payload: p, issuedAt: expiry.Add(-focusedWorkoutReviewTTL), expiresAt: expiry, reviewSHA256: hex.EncodeToString(sum[:]), verified: true}, nil
}
func (r trainingPlacementCancelReviewed) matches(p trainingPlacementCancelPayload, now time.Time) error {
	if !r.verified || now.Before(r.issuedAt) || !now.Before(r.expiresAt) {
		return errTrainingPlacementInvalid
	}
	if p.DecisionOn != now.In(lisbonLocation()).Format("2006-01-02") || validateTrainingPlacementCancel(p) != nil || !focusedGraphJSONEqual(r.Payload, p) {
		return errTrainingPlacementChanged
	}
	return nil
}

type trainingPlacementCancellationQueries interface {
	TrainingPlacementChangeForReview(context.Context, dbgen.TrainingPlacementChangeForReviewParams) (dbgen.TrainingPlacementChangeForReviewRow, error)
	TrainingPlacementSourceForReview(context.Context, dbgen.TrainingPlacementSourceForReviewParams) (dbgen.TrainingPlacementSourceForReviewRow, error)
	TrainingPlacementTimeline(context.Context, dbgen.TrainingPlacementTimelineParams) ([]dbgen.TrainingPlacementTimelineRow, error)
}

func readTrainingPlacementCancellation(ctx context.Context, q trainingPlacementCancellationQueries, actor, group, person, change uuid.UUID, state db.TrainingScheduleState) (trainingPlacementCancellationRead, error) {
	var out trainingPlacementCancellationRead
	if actor == uuid.Nil || group == uuid.Nil || person == uuid.Nil || change == uuid.Nil || state.DatabaseIncarnation == uuid.Nil || state.StateEpoch < 1 || state.RequiredReader < 4 {
		return out, errTrainingPlacementInvalid
	}
	row, e := q.TrainingPlacementChangeForReview(ctx, dbgen.TrainingPlacementChangeForReviewParams{ActorID: actor, ChangeID: change})
	if e != nil {
		return out, e
	}
	if row.ID != change || row.SubjectUserID == nil || *row.SubjectUserID != person {
		return out, pgx.ErrNoRows
	}
	allowedGroup := false
	var ref *uuid.UUID
	for i, id := range []*uuid.UUID{row.SourceGroupID, row.DestinationGroupID, row.ReturnGroupID} {
		if id != nil && *id == group {
			allowedGroup = true
		}
		if ref == nil && id != nil {
			ref = []*uuid.UUID{row.SourcePlacementID, row.DestinationPlacementID, row.ReturnPlacementID}[i]
		}
	}
	if !allowedGroup || ref == nil {
		return out, pgx.ErrNoRows
	}
	source, e := q.TrainingPlacementSourceForReview(ctx, dbgen.TrainingPlacementSourceForReviewParams{ActorID: actor, SourcePlacementID: *ref})
	if e != nil {
		return out, e
	}
	if source.SubjectID != person || source.ProgrammeID != row.ProgrammeID || source.Revision != row.Revision {
		return out, errTrainingPlacementChanged
	}
	rows, e := q.TrainingPlacementTimeline(ctx, dbgen.TrainingPlacementTimelineParams{ActorID: actor, SubjectID: person, ProgrammeID: row.ProgrammeID, RowLimit: trainingPlacementMaxTimeline + 1})
	if e != nil {
		return out, e
	}
	if len(rows) > trainingPlacementMaxTimeline {
		return out, errTrainingPlacementLimit
	}
	seen := map[uuid.UUID]bool{}
	for _, p := range rows {
		if p.ID == uuid.Nil || seen[p.ID] || p.SubjectUserID == nil || *p.SubjectUserID != person || p.ProgrammeID != row.ProgrammeID || p.Revision != row.Revision {
			return out, errTrainingPlacementChanged
		}
		seen[p.ID] = true
	}
	fresh, e := q.TrainingPlacementChangeForReview(ctx, dbgen.TrainingPlacementChangeForReviewParams{ActorID: actor, ChangeID: change})
	if e != nil {
		return out, e
	}
	before := row
	before.DecisionAt = pgtype.Timestamptz{}
	fresh.DecisionAt = pgtype.Timestamptz{}
	if !focusedGraphJSONEqual(before, fresh) {
		return out, errTrainingPlacementChanged
	}
	out = trainingPlacementCancellationRead{State: state, Change: row, PersonName: source.SubjectName, Timeline: rows, CanCancel: row.CanCancel}
	return out, nil
}
