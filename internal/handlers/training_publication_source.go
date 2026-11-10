package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const publicationSourceContract = "mycfc/training-publication-source/v1"
const publicationSourceMaxBytes = 32 << 20

var errPublicationSourceUnavailable = errors.New("training publication source unavailable")
var errPublicationFirstEmpty = errors.New("training publication has no eligible recipients")

type publicationPlanHeader struct {
	PlanID                uuid.UUID          `json:"plan_id"`
	GroupID               uuid.UUID          `json:"group_id"`
	SeasonID              *uuid.UUID         `json:"season_id"`
	WeekStart             pgtype.Date        `json:"week_start"`
	SourceUpdatedAt       pgtype.Timestamptz `json:"source_updated_at"`
	LatestPublicationID   *uuid.UUID         `json:"latest_publication_id"`
	LatestRevision        int32              `json:"latest_revision"`
	LatestSupersedesID    *uuid.UUID         `json:"latest_supersedes_id"`
	LatestSourceUpdatedAt pgtype.Timestamptz `json:"latest_source_updated_at"`
}

type publicationCandidate struct {
	SessionID    uuid.UUID   `json:"session_id"`
	PlacementID  uuid.UUID   `json:"placement_id"`
	MembershipID *uuid.UUID  `json:"membership_id"`
	SubjectID    uuid.UUID   `json:"subject_id"`
	StartsOn     pgtype.Date `json:"starts_on"`
	EndsOn       pgtype.Date `json:"ends_on"`
	SessionOn    pgtype.Date `json:"session_on"`
}

type publicationDefinition struct {
	VariationID        uuid.UUID                          `json:"variation_id"`
	PlanID             uuid.UUID                          `json:"plan_id"`
	SubjectKind        dbgen.TrainingVariationSubjectKind `json:"subject_kind"`
	SubjectID          uuid.UUID                          `json:"subject_id"`
	TargetMembershipID *uuid.UUID                         `json:"target_membership_id"`
	TargetGroupID      *uuid.UUID                         `json:"target_group_id"`
	Operation          dbgen.TrainingVariationOperation   `json:"operation"`
	Patch              json.RawMessage                    `json:"patch"`
	ChangeSummary      string                             `json:"change_summary"`
	Version            int32                              `json:"version"`
	CreatedAt          pgtype.Timestamptz                 `json:"created_at"`
}

type publicationTarget struct {
	VariationID        uuid.UUID   `json:"variation_id"`
	MembershipID       uuid.UUID   `json:"membership_id"`
	Priority           int32       `json:"priority"`
	TargetGroupID      *uuid.UUID  `json:"target_group_id"`
	TargetKind         string      `json:"target_kind"`
	TargetGroupName    *string     `json:"target_group_name"`
	EffectiveFrom      pgtype.Date `json:"effective_from"`
	EffectiveUntil     pgtype.Date `json:"effective_until"`
	CompetitionEventID *uuid.UUID  `json:"competition_event_id"`
}

type publicationVariationTransport struct {
	dbgen.ListTrainingVariationMatchesForManagerRow
	Patch json.RawMessage `json:"patch"`
}

type publicationEvent struct {
	EventID  uuid.UUID          `json:"event_id"`
	StartsAt pgtype.Timestamptz `json:"starts_at"`
}

type publicationHashTuple struct {
	PlacementID    *uuid.UUID `json:"placement_id"`
	SessionID      uuid.UUID  `json:"session_id"`
	MembershipID   uuid.UUID  `json:"membership_id"`
	AthleteUserID  *uuid.UUID `json:"athlete_user_id"`
	SnapshotSHA256 string     `json:"snapshot_sha256"`
}

type publicationSourceEnvelope struct {
	Rhythm               *publicationRhythmFacts                             `json:"-"`
	Contract             string                                              `json:"contract"`
	Header               publicationPlanHeader                               `json:"header"`
	Overview             []dbgen.ListStructuredTrainingOverviewForManagerRow `json:"overview"`
	Candidates           []publicationCandidate                              `json:"candidates"`
	EligibleRecipients   []dbgen.ListStructuredTrainingPublicationMembersRow `json:"eligible_recipients"`
	VariationDefinitions []publicationDefinition                             `json:"variation_definitions"`
	VariationTargets     []publicationTarget                                 `json:"variation_targets"`
	VariationMatches     []publicationVariationTransport                     `json:"variation_matches"`
	ReferencedEvents     []publicationEvent                                  `json:"referenced_events"`
	LatestPrescriptions  []publicationHashTuple                              `json:"latest_prescriptions"`
}

type publicationSourceBudget struct {
	SourceBytes, RawTupleCount, LatestTupleCount int64
	Overflow                                     bool
}

type publicationMaterialization struct {
	referenceSHA256                    [32]byte
	Source                             publicationSourceEnvelope
	Prescriptions                      []StructuredPrescriptionInput
	PlanTitle, GroupName, WeekLabel    string
	Added, Changed, Removed, Unchanged int
	NoChanges                          bool
}

func decodePublicationSource(raw []byte, budget publicationSourceBudget, planID uuid.UUID) (publicationSourceEnvelope, error) {
	var source publicationSourceEnvelope
	if budget.Overflow || budget.SourceBytes > publicationSourceMaxBytes || budget.RawTupleCount > trainingPublicationReviewMaxTuples || budget.LatestTupleCount > trainingPublicationReviewMaxTuples {
		return source, errTrainingPublicationReviewLimit
	}
	if len(raw) == 0 || int64(len(raw)) != budget.SourceBytes || budget.RawTupleCount < 0 || budget.LatestTupleCount < 0 {
		return source, errPublicationSourceUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&source); err != nil {
		return publicationSourceEnvelope{}, errPublicationSourceUnavailable
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return publicationSourceEnvelope{}, errPublicationSourceUnavailable
	}
	h := source.Header
	if source.Contract != publicationSourceContract || h.PlanID != planID || h.GroupID == uuid.Nil || h.SeasonID == nil || *h.SeasonID == uuid.Nil || !h.WeekStart.Valid || !h.SourceUpdatedAt.Valid || h.SourceUpdatedAt.InfinityModifier != pgtype.Finite || h.LatestRevision < 0 || (h.LatestPublicationID == nil) != (h.LatestRevision == 0) || h.LatestPublicationID != nil && *h.LatestPublicationID == uuid.Nil {
		return publicationSourceEnvelope{}, errPublicationSourceUnavailable
	}
	if source.Overview == nil || source.Candidates == nil || source.EligibleRecipients == nil || source.VariationDefinitions == nil || source.VariationTargets == nil || source.VariationMatches == nil || source.ReferencedEvents == nil || source.LatestPrescriptions == nil {
		return publicationSourceEnvelope{}, errPublicationSourceUnavailable
	}
	if int64(len(source.Candidates)) != budget.RawTupleCount || int64(len(source.LatestPrescriptions)) != budget.LatestTupleCount {
		return publicationSourceEnvelope{}, errPublicationSourceUnavailable
	}
	if err := validatePublicationSource(source); err != nil {
		return publicationSourceEnvelope{}, err
	}
	return source, nil
}

func validatePublicationSource(source publicationSourceEnvelope) error {
	h := source.Header
	sessions := map[uuid.UUID]bool{}
	for _, r := range source.Overview {
		if r.GroupID != h.GroupID || r.PlanID == nil || *r.PlanID != h.PlanID || r.MemberCount != 0 {
			return errPublicationSourceUnavailable
		}
		if r.SessionID != nil {
			if *r.SessionID == uuid.Nil {
				return errPublicationSourceUnavailable
			}
			sessions[*r.SessionID] = true
		}
	}
	if len(source.Overview) == 0 {
		return errPublicationSourceUnavailable
	}
	type pair struct{ session, membership uuid.UUID }
	candidates := map[[3]uuid.UUID]publicationCandidate{}
	for _, c := range source.Candidates {
		key := publicationCandidateKey(c)
		if !sessions[c.SessionID] || c.PlacementID == uuid.Nil || c.SubjectID == uuid.Nil || !c.SessionOn.Valid || c.MembershipID != nil && (*c.MembershipID == uuid.Nil || !c.StartsOn.Valid) {
			return errPublicationSourceUnavailable
		}
		if _, exists := candidates[key]; exists {
			return errPublicationSourceUnavailable
		}
		candidates[key] = c
	}
	eligible := map[pair]bool{}
	for _, r := range source.EligibleRecipients {
		if r.PlacementID == uuid.Nil || r.AthleteUserID == nil || *r.AthleteUserID == uuid.Nil || r.MembershipID == uuid.Nil {
			return errPublicationSourceUnavailable
		}
		c, ok := candidates[[3]uuid.UUID{r.SessionID, r.PlacementID, *r.AthleteUserID}]
		key := pair{r.SessionID, r.MembershipID}
		if !ok || c.MembershipID == nil || *c.MembershipID != r.MembershipID || eligible[key] {
			return errPublicationSourceUnavailable
		}
		eligible[key] = true
	}
	definitions := map[uuid.UUID]bool{}
	for _, v := range source.VariationDefinitions {
		if v.PlanID != h.PlanID || v.VariationID == uuid.Nil || v.SubjectID == uuid.Nil || definitions[v.VariationID] || !json.Valid(v.Patch) {
			return errPublicationSourceUnavailable
		}
		definitions[v.VariationID] = true
	}
	for _, target := range source.VariationTargets {
		if !definitions[target.VariationID] || target.MembershipID == uuid.Nil {
			return errPublicationSourceUnavailable
		}
	}
	for _, v := range source.VariationMatches {
		if v.PlanID != h.PlanID || !definitions[v.VariationID] || !sessions[v.SessionID] || v.MembershipID == nil || !eligible[pair{v.SessionID, *v.MembershipID}] || !json.Valid(v.Patch) {
			return errPublicationSourceUnavailable
		}
	}
	events := map[uuid.UUID]bool{}
	for _, e := range source.ReferencedEvents {
		if e.EventID == uuid.Nil || !e.StartsAt.Valid || events[e.EventID] {
			return errPublicationSourceUnavailable
		}
		events[e.EventID] = true
	}
	for _, target := range source.VariationTargets {
		if target.CompetitionEventID != nil && !events[*target.CompetitionEventID] {
			return errPublicationSourceUnavailable
		}
	}
	latest := map[pair]bool{}
	for _, p := range source.LatestPrescriptions {
		key := pair{p.SessionID, p.MembershipID}
		if h.LatestPublicationID == nil || p.SessionID == uuid.Nil || p.MembershipID == uuid.Nil || latest[key] || len(p.SnapshotSHA256) != 64 {
			return errPublicationSourceUnavailable
		}
		latest[key] = true
	}
	return nil
}

func materializePublicationSource(source publicationSourceEnvelope, location *time.Location) (publicationMaterialization, error) {
	var result publicationMaterialization
	if err := validatePublicationSource(source); err != nil {
		return result, err
	}
	audience, week, found := structuredPublicationWeek(assembleStructuredTraining(managerStructuredRows(source.Overview), location), source.Header.PlanID)
	if !found {
		return result, errPublicationSourceUnavailable
	}
	matches := make([]dbgen.ListTrainingVariationMatchesForManagerRow, len(source.VariationMatches))
	for i, row := range source.VariationMatches {
		matches[i] = row.ListTrainingVariationMatchesForManagerRow
		matches[i].Patch = append([]byte(nil), row.Patch...)
	}
	if structuredPublicationConflictCount(matches, source.Header.PlanID) > 0 {
		return result, errStructuredTrainingPublicationVariationConflict
	}
	schedules, err := publicationSourceSchedules(source.Overview)
	if err != nil {
		return result, err
	}
	referenceDigest := [32]byte{}
	if source.Rhythm != nil {
		if err := validatePublicationRhythmFacts(source, *source.Rhythm); err != nil {
			return result, err
		}
	}
	if source.Rhythm != nil {
		referenceDigest, err = publicationRhythmDigest(*source.Rhythm)
		if err != nil {
			return result, err
		}
	}
	prescriptions, err := buildStructuredPrescriptionInputsResolved(source.Header.PlanID, audience, week, source.EligibleRecipients, matches, schedules, func(session *pages.StructuredTrainingSession, r dbgen.ListStructuredTrainingPublicationMembersRow) error {
		return applyPublicationRhythm(session, r, source)
	})
	if err != nil {
		return result, err
	}
	if len(prescriptions) != len(source.EligibleRecipients) {
		return result, errPublicationSourceUnavailable
	}
	if len(prescriptions) > trainingPublicationReviewMaxTuples {
		return result, errTrainingPublicationReviewLimit
	}
	totalBytes := 0
	for _, p := range prescriptions {
		if len(p.Snapshot) > trainingPublicationReviewMaxSnapshotBytes-totalBytes {
			return result, errTrainingPublicationReviewLimit
		}
		totalBytes += len(p.Snapshot)
	}
	result = publicationMaterialization{referenceSHA256: referenceDigest, Source: source, Prescriptions: prescriptions, PlanTitle: week.Title, GroupName: audience.GroupName, WeekLabel: week.DateRange}
	previous := map[[2]uuid.UUID]publicationHashTuple{}
	for _, p := range source.LatestPrescriptions {
		previous[[2]uuid.UUID{p.SessionID, p.MembershipID}] = p
	}
	for _, p := range prescriptions {
		key := [2]uuid.UUID{p.SessionID, p.MembershipID}
		old, exists := previous[key]
		if !exists {
			result.Added++
		} else if old.PlacementID != nil && *old.PlacementID == p.PlacementID && old.AthleteUserID != nil && *old.AthleteUserID == p.AthleteUserID && old.SnapshotSHA256 == p.SnapshotSHA256 {
			result.Unchanged++
		} else {
			result.Changed++
		}
		delete(previous, key)
	}
	result.Removed = len(previous)
	result.NoChanges = source.Header.LatestPublicationID != nil && result.Added+result.Changed+result.Removed == 0
	if len(prescriptions) == 0 && source.Header.LatestPublicationID == nil {
		return result, errPublicationFirstEmpty
	}
	return result, nil
}

func publicationMembershipIDs(source publicationSourceEnvelope) []uuid.UUID {
	set := map[uuid.UUID]bool{}
	for _, c := range source.Candidates {
		if c.MembershipID != nil && *c.MembershipID != uuid.Nil {
			set[*c.MembershipID] = true
		}
	}
	ids := make([]uuid.UUID, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
	return ids
}

func publicationCandidateKey(c publicationCandidate) [3]uuid.UUID {
	return [3]uuid.UUID{c.SessionID, c.PlacementID, c.SubjectID}
}
