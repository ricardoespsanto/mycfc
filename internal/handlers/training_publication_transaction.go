package handlers

import (
	"context"
	"errors"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type publicationRecipientTuple struct {
	SessionID     uuid.UUID `json:"session_id"`
	MembershipID  uuid.UUID `json:"membership_id"`
	AthleteUserID uuid.UUID `json:"athlete_user_id"`
}

type publicationFinalFacts struct {
	PlanID, GroupID                         uuid.UUID
	SourceUpdatedAt                         pgtype.Timestamptz
	LatestPublicationID                     uuid.UUID
	LatestRevision                          int32
	LatestSupersedesID                      *uuid.UUID
	TransactionLisbonDate, ActualLisbonDate pgtype.Date
	DecisionAt                              pgtype.Timestamptz
	RawTupleCount, PublicationTupleCount    int64
	Overflow                                bool
	RecipientTuples                         []publicationRecipientTuple
	PublicationTuples                       []publicationHashTuple
}

type publicationSaveResult struct {
	PublicationID       uuid.UUID
	Revision            int32
	NoOp, CleanupFailed bool
	PrescriptionCount   int
}

type publicationConfirmInput struct {
	ActorID, PlanID uuid.UUID
	Review          trainingPublicationReviewClaims
}

// The production adapter owns scoped SQL and receives only the admitted DBTX.
// This interface has no transaction/connection/pool lifecycle methods. It also
// keeps the canonical builder independent from the SQL transport encoding.
type publicationTransactionQueries interface {
	AdmitScheduleWrites(context.Context) (bool, error)
	VerifyRuntime(context.Context) error
	LockActor(context.Context, uuid.UUID) error
	GuardPlanV1(context.Context, uuid.UUID, uuid.UUID) error
	LockPlan(context.Context, uuid.UUID, uuid.UUID) (publicationPlanHeader, error)
	SourceBudget(context.Context, uuid.UUID, uuid.UUID) (publicationSourceBudget, error)
	StructuralCandidates(context.Context, uuid.UUID, uuid.UUID) ([]publicationCandidate, error)
	LockMemberships(context.Context, uuid.UUID, uuid.UUID) ([]uuid.UUID, error)
	LockEvents(context.Context, uuid.UUID, uuid.UUID) ([]publicationEvent, error)
	LockUsers(context.Context, uuid.UUID, uuid.UUID) ([]uuid.UUID, error)
	RawSource(context.Context, uuid.UUID, uuid.UUID) ([]byte, publicationSourceBudget, error)
	CheckArtifactSizes(context.Context, []StructuredPrescriptionInput) error
	InsertPublication(context.Context, uuid.UUID, publicationPlanHeader, string) (dbgen.TrainingPlanPublication, error)
	InsertPrescription(context.Context, uuid.UUID, uuid.UUID, StructuredPrescriptionInput) error
	FinalFacts(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (publicationFinalFacts, error)
}

func savePublicationInTransaction(ctx context.Context, q publicationTransactionQueries, input publicationConfirmInput, location *time.Location, now func() time.Time) (publicationSaveResult, error) {
	var result publicationSaveResult
	if !input.Review.verified || input.ActorID == uuid.Nil || input.PlanID == uuid.Nil || input.Review.metadata.ActorID != input.ActorID || input.Review.metadata.PlanID != input.PlanID {
		return result, errTrainingPublicationReviewInvalid
	}
	// The request may have queued before an exclusive migration/floor change.
	// This check is INSIDE the new post-admission transaction, before actor rows.
	partialEnabled, err := q.AdmitScheduleWrites(ctx)
	if err != nil {
		return result, err
	}
	if err := q.VerifyRuntime(ctx); err != nil {
		return result, err
	}
	if err := q.LockActor(ctx, input.ActorID); err != nil {
		return result, trainingScopeWriteError(err)
	}
	if !partialEnabled {
		if err := q.GuardPlanV1(ctx, input.ActorID, input.PlanID); err != nil {
			return result, err
		}
	}
	header, err := q.LockPlan(ctx, input.ActorID, input.PlanID)
	if err != nil {
		return result, err
	}
	if !publicationReviewedHeaderMatches(input.Review, header) {
		return result, errStructuredTrainingPublicationConflict
	}
	budget, err := q.SourceBudget(ctx, input.ActorID, input.PlanID)
	if err != nil {
		return result, err
	}
	if budget.Overflow || budget.SourceBytes > publicationSourceMaxBytes || budget.RawTupleCount > trainingPublicationReviewMaxTuples || budget.LatestTupleCount > trainingPublicationReviewMaxTuples {
		return result, errTrainingPublicationReviewLimit
	}
	candidates, err := q.StructuralCandidates(ctx, input.ActorID, input.PlanID)
	if err != nil {
		return result, err
	}
	if int64(len(candidates)) != budget.RawTupleCount {
		return result, errStructuredTrainingPublicationConflict
	}
	memberships, err := q.LockMemberships(ctx, input.ActorID, input.PlanID)
	if err != nil {
		return result, err
	}
	if !publicationSameIDs(memberships, publicationMembershipIDs(publicationSourceEnvelope{Candidates: candidates})) {
		return result, errStructuredTrainingPublicationConflict
	}
	events, err := q.LockEvents(ctx, input.ActorID, input.PlanID)
	if err != nil {
		return result, publicationNowaitError(err)
	}
	users, err := q.LockUsers(ctx, input.ActorID, input.PlanID)
	if err != nil {
		return result, publicationNowaitError(err)
	}
	if !publicationCandidateUsersLocked(candidates, users) {
		return result, errStructuredTrainingPublicationConflict
	}
	raw, materialBudget, err := q.RawSource(ctx, input.ActorID, input.PlanID)
	if err != nil {
		return result, err
	}
	if budget != materialBudget {
		return result, errStructuredTrainingPublicationConflict
	}
	source, err := decodePublicationSource(raw, materialBudget, input.PlanID)
	if err != nil {
		return result, err
	}
	if !publicationReviewedHeaderMatches(input.Review, source.Header) || source.Header.GroupID != header.GroupID || !publicationSameCandidates(candidates, source.Candidates) || !publicationSameEvents(events, source.ReferencedEvents) {
		return result, errStructuredTrainingPublicationConflict
	}
	if !publicationReturnMatchesSource(input.Review.metadata.Return, source) {
		return result, pgx.ErrNoRows
	}
	materialized, err := materializePublicationSource(source, location)
	if err != nil {
		return result, err
	}
	if err := q.CheckArtifactSizes(ctx, materialized.Prescriptions); err != nil {
		return result, err
	}
	current := publicationCurrentReviewInput(input, source.Header, materialized.Prescriptions)
	if err := input.Review.matches(current, now()); err != nil {
		return result, err
	}
	result = publicationSaveResult{Revision: header.LatestRevision, NoOp: materialized.NoChanges, PrescriptionCount: len(materialized.Prescriptions)}
	if result.NoOp {
		result.PublicationID = *header.LatestPublicationID
	} else {
		publication, err := q.InsertPublication(ctx, input.ActorID, header, input.Review.metadata.ChangeSummary)
		if err != nil {
			return result, err
		}
		if publication.ID == uuid.Nil || publication.PlanID != input.PlanID || publication.Revision != header.LatestRevision+1 || !publicationSameOptionalID(publication.SupersedesID, header.LatestPublicationID) {
			return result, errStructuredTrainingPublicationConflict
		}
		result.PublicationID, result.Revision = publication.ID, publication.Revision
		for _, p := range materialized.Prescriptions {
			if err := q.InsertPrescription(ctx, input.ActorID, publication.ID, p); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return result, errStructuredTrainingPublicationConflict
				}
				return result, err
			}
		}
	}
	facts, err := q.FinalFacts(ctx, input.ActorID, input.PlanID, result.PublicationID)
	if err != nil {
		return result, err
	}
	if err := checkPublicationFinalFacts(input, current, header, result, facts, budget.RawTupleCount); err != nil {
		return result, err
	}
	return result, nil // The sole session helper, never this callback, commits.
}

func publicationReviewedHeaderMatches(review trainingPublicationReviewClaims, h publicationPlanHeader) bool {
	m := review.metadata
	latest := uuid.Nil
	if h.LatestPublicationID != nil {
		latest = *h.LatestPublicationID
	}
	return h.PlanID == m.PlanID && h.SourceUpdatedAt.Valid && h.SourceUpdatedAt.Time.Equal(m.SourceUpdatedAt) && latest == m.LatestPublicationID && h.LatestRevision == m.LatestRevision
}
func publicationCurrentReviewInput(input publicationConfirmInput, h publicationPlanHeader, prescriptions []StructuredPrescriptionInput) trainingPublicationReviewInput {
	m := input.Review.metadata
	m.ActorID = input.ActorID
	m.PlanID = h.PlanID
	m.SourceUpdatedAt = h.SourceUpdatedAt.Time
	m.LatestRevision = h.LatestRevision
	m.LatestPublicationID = uuid.Nil
	if h.LatestPublicationID != nil {
		m.LatestPublicationID = *h.LatestPublicationID
	}
	return trainingPublicationReviewInput{trainingPublicationReviewMetadata: m, Prescriptions: prescriptions}
}
func publicationSameOptionalID(a, b *uuid.UUID) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func publicationSameIDs(actual, expected []uuid.UUID) bool {
	if len(actual) != len(expected) {
		return false
	}
	set := map[uuid.UUID]bool{}
	for _, id := range actual {
		if id == uuid.Nil || set[id] {
			return false
		}
		set[id] = true
	}
	for _, id := range expected {
		if !set[id] {
			return false
		}
	}
	return true
}
func publicationCandidateUsersLocked(candidates []publicationCandidate, users []uuid.UUID) bool {
	if len(users) > 2*trainingPublicationReviewMaxTuples {
		return false
	}
	set := map[uuid.UUID]bool{}
	for _, id := range users {
		if id == uuid.Nil || set[id] {
			return false
		}
		set[id] = true
	}
	for _, c := range candidates {
		if c.SubjectID != nil && !set[*c.SubjectID] {
			return false
		}
	}
	return true
}
func publicationSameCandidates(a, b []publicationCandidate) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[[2]uuid.UUID]publicationCandidate{}
	for _, c := range a {
		key := [2]uuid.UUID{c.SessionID, c.MembershipID}
		if _, ok := set[key]; ok {
			return false
		}
		set[key] = c
	}
	for _, c := range b {
		previous, ok := set[[2]uuid.UUID{c.SessionID, c.MembershipID}]
		if !ok || !publicationSameOptionalID(c.SubjectID, previous.SubjectID) || c.StartsOn != previous.StartsOn || c.EndsOn != previous.EndsOn || c.SessionOn != previous.SessionOn {
			return false
		}
		delete(set, [2]uuid.UUID{c.SessionID, c.MembershipID})
	}
	return len(set) == 0
}
func publicationSameEvents(a, b []publicationEvent) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[uuid.UUID]pgtype.Timestamptz{}
	for _, e := range a {
		if _, ok := set[e.EventID]; ok {
			return false
		}
		set[e.EventID] = e.StartsAt
	}
	for _, e := range b {
		previous, ok := set[e.EventID]
		if !ok || !previous.Valid || !e.StartsAt.Valid || previous.InfinityModifier != e.StartsAt.InfinityModifier || !previous.Time.Equal(e.StartsAt.Time) {
			return false
		}
		delete(set, e.EventID)
	}
	return len(set) == 0
}
func publicationReturnMatchesSource(c trainingPublicationReturnContext, s publicationSourceEnvelope) bool {
	if c.GroupID == uuid.Nil {
		return c.WeekID == uuid.Nil && c.SessionID == uuid.Nil && c.Day == ""
	}
	if c.GroupID != s.Header.GroupID || c.WeekID != s.Header.PlanID {
		return false
	}
	if c.Day != "" {
		d, err := time.Parse("2006-01-02", c.Day)
		if err != nil || !s.Header.WeekStart.Valid || d.Before(s.Header.WeekStart.Time) || !d.Before(s.Header.WeekStart.Time.AddDate(0, 0, 7)) {
			return false
		}
	}
	if c.SessionID != uuid.Nil {
		for _, r := range s.Overview {
			if r.SessionID != nil && *r.SessionID == c.SessionID {
				return c.Day == "" || r.SessionOn.Valid && r.SessionOn.Time.Format("2006-01-02") == c.Day
			}
		}
		return false
	}
	return true
}
func checkPublicationFinalFacts(input publicationConfirmInput, current trainingPublicationReviewInput, baseline publicationPlanHeader, result publicationSaveResult, f publicationFinalFacts, rawTupleCount int64) error {
	if f.Overflow || f.RawTupleCount > trainingPublicationReviewMaxTuples || f.PublicationTupleCount > trainingPublicationReviewMaxTuples {
		return errTrainingPublicationReviewLimit
	}
	if f.RawTupleCount != rawTupleCount || f.PlanID != input.PlanID || f.GroupID != baseline.GroupID || !f.SourceUpdatedAt.Valid || !f.SourceUpdatedAt.Time.Equal(input.Review.metadata.SourceUpdatedAt) || f.LatestPublicationID != result.PublicationID || f.LatestRevision != result.Revision || !f.TransactionLisbonDate.Valid || !f.ActualLisbonDate.Valid || f.TransactionLisbonDate.Time.Format("2006-01-02") != f.ActualLisbonDate.Time.Format("2006-01-02") || !f.DecisionAt.Valid {
		return errStructuredTrainingPublicationConflict
	}
	supersedes := baseline.LatestPublicationID
	if result.NoOp {
		supersedes = baseline.LatestSupersedesID
	}
	if !publicationSameOptionalID(f.LatestSupersedesID, supersedes) || int64(len(f.PublicationTuples)) != f.PublicationTupleCount || len(f.RecipientTuples) != len(current.Prescriptions) || len(f.PublicationTuples) != len(current.Prescriptions) {
		return errStructuredTrainingPublicationConflict
	}
	desired := map[[2]uuid.UUID]StructuredPrescriptionInput{}
	for _, p := range current.Prescriptions {
		desired[[2]uuid.UUID{p.SessionID, p.MembershipID}] = p
	}
	seen := map[[2]uuid.UUID]bool{}
	for _, r := range f.RecipientTuples {
		key := [2]uuid.UUID{r.SessionID, r.MembershipID}
		p, ok := desired[key]
		if !ok || seen[key] || r.AthleteUserID != p.AthleteUserID {
			return errStructuredTrainingPublicationConflict
		}
		seen[key] = true
	}
	seen = map[[2]uuid.UUID]bool{}
	for _, r := range f.PublicationTuples {
		key := [2]uuid.UUID{r.SessionID, r.MembershipID}
		p, ok := desired[key]
		if !ok || seen[key] || r.AthleteUserID == nil || *r.AthleteUserID != p.AthleteUserID || r.SnapshotSHA256 != p.SnapshotSHA256 {
			return errStructuredTrainingPublicationConflict
		}
		seen[key] = true
	}
	return input.Review.matches(current, f.DecisionAt.Time)
}
