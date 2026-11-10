package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/trainingcompat"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (s PostgresStructuredTrainingStore) AuthorizePublication(ctx context.Context, actor, plan uuid.UUID) (publicationPlanHeader, error) {
	if _, err := db.VerifyTrainingRhythmRuntime(ctx, s.Pool, publicationRuntimeCapabilities()); err != nil {
		return publicationPlanHeader{}, err
	}
	row, err := s.queries().PublicationPlanForReview(ctx, dbgen.PublicationPlanForReviewParams{ActorID: actor, PlanID: plan})
	return publicationPlanHeader(row), err
}
func (s PostgresStructuredTrainingStore) ReadPublicationSource(ctx context.Context, actor, plan uuid.UUID) (publicationSourceEnvelope, error) {
	state, err := db.VerifyTrainingRhythmRuntime(ctx, s.Pool, publicationRuntimeCapabilities())
	if err != nil {
		return publicationSourceEnvelope{}, err
	}
	q := s.queries()
	if !state.PartialV2Enabled {
		if _, err = q.TrainingScheduleGuardPlanV1(ctx, dbgen.TrainingScheduleGuardPlanV1Params{ActorID: actor, PlanID: plan}); err != nil {
			return publicationSourceEnvelope{}, trainingScheduleWriteError(err)
		}
	}
	first, err := q.PublicationSourceBudget(ctx, dbgen.PublicationSourceBudgetParams{ActorID: actor, PlanID: plan})
	if err != nil {
		return publicationSourceEnvelope{}, err
	}
	if err := publicationBudgetAdmitted(publicationSourceBudget(first)); err != nil {
		return publicationSourceEnvelope{}, err
	}
	row, err := q.PublicationRawSource(ctx, dbgen.PublicationRawSourceParams{ActorID: actor, PlanID: plan})
	if err != nil {
		return publicationSourceEnvelope{}, err
	}
	// Preview has READ COMMITTED statements: only this independently gated
	// materialization's own header/source constitutes the new shown review.
	source, err := decodePublicationSource(row.Envelope, publicationSourceBudget{row.SourceBytes, row.RawTupleCount, row.LatestTupleCount, row.Overflow}, plan)
	if err != nil {
		return publicationSourceEnvelope{}, err
	}
	source.Rhythm, err = readPublicationRhythm(ctx, q, actor, source, false)
	if err != nil {
		return publicationSourceEnvelope{}, err
	}
	actual, err := q.PublicationPlanForReview(ctx, dbgen.PublicationPlanForReviewParams{ActorID: actor, PlanID: plan})
	if err != nil {
		return publicationSourceEnvelope{}, err
	}
	if !samePublicationRhythmHeader(source.Header, publicationPlanHeader(actual)) {
		return publicationSourceEnvelope{}, errStructuredTrainingPublicationConflict
	}
	if err = validatePublicationScheduleWriteState(source, state); err != nil {
		return publicationSourceEnvelope{}, err
	}
	material, materialErr := materializePublicationSource(source, lisbonLocation())
	if materialErr != nil && !errors.Is(materialErr, errPublicationFirstEmpty) && !errors.Is(materialErr, errStructuredTrainingPublicationVariationConflict) {
		return publicationSourceEnvelope{}, materialErr
	}
	if materialErr == nil {
		if err = checkPublicationArtifactSizes(ctx, s.Pool, material.Prescriptions); err != nil {
			return publicationSourceEnvelope{}, err
		}
	}
	return source, nil
}
func (s PostgresStructuredTrainingStore) ConfirmPublication(ctx context.Context, input publicationConfirmInput) (publicationSaveResult, error) {
	var result publicationSaveResult
	if !input.Review.verified {
		return result, errTrainingPublicationReviewInvalid
	}
	pool, ok := s.Pool.(*pgxpool.Pool)
	if !ok {
		return result, errPublicationSourceUnavailable
	}
	outcome, err := db.WithRecipientPublicationTx(ctx, pool, db.RecipientTxOptions{AdmissionTimeout: 5 * time.Second, TransactionTimeout: 10 * time.Second, CleanupTimeout: 5 * time.Second}, func(txCtx context.Context, commands dbgen.DBTX) error {
		adapter := publicationPostgresQueries{commands: commands, queries: dbgen.New(commands)}
		var bodyErr error
		location, _ := time.LoadLocation("Europe/Lisbon")
		if location == nil {
			return errPublicationSourceUnavailable
		}
		result, bodyErr = savePublicationInTransaction(txCtx, adapter, input, location, time.Now)
		return bodyErr
	})
	result.CleanupFailed = outcome.CleanupFailed
	if normalized := publicationTransactionError(outcome, err); normalized != nil {
		return publicationSaveResult{}, normalized
	}
	return result, nil
}
func publicationRuntimeCapabilities() db.TrainingScheduleCapabilities {
	return db.TrainingScheduleCapabilities{ReaderVersion: trainingcompat.ReaderGeneration, WriterVersion: trainingcompat.WriterGeneration}
}
func publicationBudgetAdmitted(b publicationSourceBudget) error {
	if b.Overflow || b.SourceBytes > publicationSourceMaxBytes || b.RawTupleCount > trainingPublicationReviewMaxTuples || b.LatestTupleCount > trainingPublicationReviewMaxTuples {
		return errTrainingPublicationReviewLimit
	}
	if b.SourceBytes <= 0 || b.RawTupleCount < 0 || b.LatestTupleCount < 0 {
		return errPublicationSourceUnavailable
	}
	return nil
}

type publicationPostgresQueries struct {
	commands dbgen.DBTX
	queries  *dbgen.Queries
}

func (q publicationPostgresQueries) AdmitScheduleWrites(ctx context.Context) (bool, error) {
	state, err := db.TrainingScheduleStatus(ctx, q.commands)
	if err != nil {
		return false, err
	}
	if state.PartialV2Enabled {
		if _, err = q.commands.Exec(ctx, `SELECT public.training_schedule_admit_partial($1,$2)`, state.DatabaseIncarnation, state.StateEpoch); err != nil {
			return false, trainingScheduleWriteError(err)
		}
	}
	actual, err := db.VerifyTrainingRhythmRuntime(ctx, q.commands, publicationRuntimeCapabilities())
	if err != nil {
		return false, err
	}
	if actual.DatabaseIncarnation != state.DatabaseIncarnation || actual.StateEpoch != state.StateEpoch || actual.SchemaContract != state.SchemaContract || actual.PartialV2Enabled != state.PartialV2Enabled {
		return false, errStructuredTrainingPublicationConflict
	}
	return actual.PartialV2Enabled, nil
}
func (q publicationPostgresQueries) CheckArtifactSizes(ctx context.Context, inputs []StructuredPrescriptionInput) error {
	return checkPublicationArtifactSizes(ctx, q.commands, inputs)
}
func (q publicationPostgresQueries) VerifyRuntime(ctx context.Context) error {
	_, err := db.VerifyTrainingRhythmRuntime(ctx, q.commands, publicationRuntimeCapabilities())
	return err
}
func (q publicationPostgresQueries) LockActor(ctx context.Context, actor uuid.UUID) error {
	return lockTrainingGroupActor(ctx, q.commands, actor)
}
func (q publicationPostgresQueries) GuardPlanV1(ctx context.Context, actor, plan uuid.UUID) error {
	_, err := q.queries.TrainingScheduleGuardPlanV1(ctx, dbgen.TrainingScheduleGuardPlanV1Params{ActorID: actor, PlanID: plan})
	return trainingScheduleWriteError(err)
}
func (q publicationPostgresQueries) LockPlan(ctx context.Context, actor, plan uuid.UUID) (publicationPlanHeader, error) {
	row, err := q.queries.LockPublicationPlan(ctx, dbgen.LockPublicationPlanParams{ActorID: actor, PlanID: plan})
	return publicationPlanHeader(row), err
}
func (q publicationPostgresQueries) SourceBudget(ctx context.Context, actor, plan uuid.UUID) (publicationSourceBudget, error) {
	row, err := q.queries.PublicationSourceBudget(ctx, dbgen.PublicationSourceBudgetParams{ActorID: actor, PlanID: plan})
	return publicationSourceBudget(row), err
}
func (q publicationPostgresQueries) StructuralCandidates(ctx context.Context, actor, plan uuid.UUID) ([]publicationCandidate, error) {
	rows, err := q.queries.PublicationStructuralCandidates(ctx, dbgen.PublicationStructuralCandidatesParams{ActorID: actor, PlanID: plan})
	if err != nil {
		return nil, err
	}
	result := make([]publicationCandidate, len(rows))
	for i, row := range rows {
		result[i] = publicationCandidate(row)
	}
	return result, nil
}
func (q publicationPostgresQueries) LockPlacementHeads(ctx context.Context, actor, plan uuid.UUID) ([]dbgen.TrainingPlacementHead, error) {
	return q.queries.LockPublicationPlacementHeads(ctx, dbgen.LockPublicationPlacementHeadsParams{ActorID: actor, PlanID: plan})
}
func (q publicationPostgresQueries) LockMemberships(ctx context.Context, actor, plan uuid.UUID) ([]uuid.UUID, error) {
	return q.queries.LockPublicationCandidateMemberships(ctx, dbgen.LockPublicationCandidateMembershipsParams{ActorID: actor, PlanID: plan})
}
func (q publicationPostgresQueries) LockEvents(ctx context.Context, actor, plan uuid.UUID) ([]publicationEvent, error) {
	rows, err := q.queries.LockPublicationReferencedEvents(ctx, dbgen.LockPublicationReferencedEventsParams{ActorID: actor, PlanID: plan})
	if err != nil {
		return nil, err
	}
	result := make([]publicationEvent, len(rows))
	for i, row := range rows {
		result[i] = publicationEvent(row)
	}
	return result, nil
}
func (q publicationPostgresQueries) LockUsers(ctx context.Context, actor, plan uuid.UUID) ([]uuid.UUID, error) {
	return q.queries.LockPublicationCandidateUsers(ctx, dbgen.LockPublicationCandidateUsersParams{ActorID: actor, PlanID: plan})
}
func (q publicationPostgresQueries) RawSource(ctx context.Context, actor, plan uuid.UUID) ([]byte, publicationSourceBudget, error) {
	row, err := q.queries.PublicationRawSource(ctx, dbgen.PublicationRawSourceParams{ActorID: actor, PlanID: plan})
	return row.Envelope, publicationSourceBudget{row.SourceBytes, row.RawTupleCount, row.LatestTupleCount, row.Overflow}, err
}
func (q publicationPostgresQueries) InsertPublication(ctx context.Context, actor uuid.UUID, h publicationPlanHeader, summary string) (dbgen.TrainingPlanPublication, error) {
	// The approved SQL performs the real-INSERT-only same-value plan tuple witness
	// and makes INSERT depend on RETURNING. No-op never enters this method.
	return q.queries.InsertPublication(ctx, dbgen.InsertPublicationParams{ActorID: actor, PlanID: h.PlanID, SourceUpdatedAt: h.SourceUpdatedAt, ExpectedLatestPublicationID: h.LatestPublicationID, ExpectedLatestRevision: h.LatestRevision, ChangeSummary: summary})
}
func (q publicationPostgresQueries) InsertPrescription(ctx context.Context, actor, pub uuid.UUID, p StructuredPrescriptionInput) error {
	_, err := q.queries.InsertPrescription(ctx, dbgen.InsertPrescriptionParams{ActorID: actor, PublicationID: pub, SessionID: p.SessionID, MembershipID: p.MembershipID, PlacementID: p.PlacementID, AthleteUserID: p.AthleteUserID, Snapshot: p.Snapshot, SnapshotSha256: p.SnapshotSHA256})
	return err
}
func (q publicationPostgresQueries) FinalFacts(ctx context.Context, actor, plan, pub uuid.UUID) (publicationFinalFacts, error) {
	row, err := q.queries.PublicationFinalFacts(ctx, dbgen.PublicationFinalFactsParams{ActorID: actor, PlanID: plan, PublicationID: pub})
	if err != nil {
		return publicationFinalFacts{}, err
	}
	facts := publicationFinalFacts{PlanID: row.PlanID, GroupID: row.GroupID, SourceUpdatedAt: row.SourceUpdatedAt, LatestPublicationID: row.LatestPublicationID, LatestRevision: row.LatestRevision, LatestSupersedesID: row.LatestSupersedesID, TransactionLisbonDate: row.TransactionLisbonDate, ActualLisbonDate: row.ActualLisbonDate, DecisionAt: row.DecisionAt, RawTupleCount: row.RawTupleCount, PublicationTupleCount: row.PublicationTupleCount, Overflow: row.Overflow}
	if facts.Overflow {
		return facts, nil
	}
	if len(row.RecipientTuples) > publicationSourceMaxBytes || len(row.PublicationTuples) > publicationSourceMaxBytes {
		return facts, errTrainingPublicationReviewLimit
	}
	if err := decodePublicationTuples(row.RecipientTuples, &facts.RecipientTuples); err != nil {
		return facts, err
	}
	if err := decodePublicationTuples(row.PublicationTuples, &facts.PublicationTuples); err != nil {
		return facts, err
	}
	if facts.RecipientTuples == nil || facts.PublicationTuples == nil {
		return facts, errPublicationSourceUnavailable
	}
	return facts, nil
}
func decodePublicationTuples(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errPublicationSourceUnavailable
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errPublicationSourceUnavailable
	}
	return nil
}

func (q publicationPostgresQueries) ReadRhythm(ctx context.Context, actor uuid.UUID, source publicationSourceEnvelope) (*publicationRhythmFacts, error) {
	return readPublicationRhythm(ctx, q.queries, actor, source, true)
}
