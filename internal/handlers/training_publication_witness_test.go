package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type publicationWitnessFault struct {
	*publicationTransactionFake
	missingHead, missingMembership, missingUser bool
	differentBudget, foreignSuccessor           bool
}

func (q publicationWitnessFault) LockPlacementHeads(ctx context.Context, actor, plan uuid.UUID) ([]dbgen.TrainingPlacementHead, error) {
	if q.missingHead {
		return nil, nil
	}
	return q.publicationTransactionFake.LockPlacementHeads(ctx, actor, plan)
}

func (q publicationWitnessFault) LockMemberships(ctx context.Context, actor, plan uuid.UUID) ([]uuid.UUID, error) {
	if q.missingMembership {
		return nil, nil
	}
	return q.publicationTransactionFake.LockMemberships(ctx, actor, plan)
}

func (q publicationWitnessFault) LockUsers(ctx context.Context, actor, plan uuid.UUID) ([]uuid.UUID, error) {
	if q.missingUser {
		return nil, nil
	}
	return q.publicationTransactionFake.LockUsers(ctx, actor, plan)
}

func (q publicationWitnessFault) RawSource(ctx context.Context, actor, plan uuid.UUID) ([]byte, publicationSourceBudget, error) {
	raw, budget, err := q.publicationTransactionFake.RawSource(ctx, actor, plan)
	if q.differentBudget {
		budget.SourceBytes++
	}
	return raw, budget, err
}

func (q publicationWitnessFault) InsertPublication(ctx context.Context, actor uuid.UUID, header publicationPlanHeader, summary string) (dbgen.TrainingPlanPublication, error) {
	row, err := q.publicationTransactionFake.InsertPublication(ctx, actor, header, summary)
	if q.foreignSuccessor {
		row.PlanID = uuid.New()
	}
	return row, err
}

func TestPublicationTransactionRefusesMissingOrChangedWitnessesBeforeInsert(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, *publicationWitnessFault, *publicationConfirmInput)
		want   error
	}{
		{"verified review belongs to another actor", func(_ *testing.T, _ *publicationWitnessFault, input *publicationConfirmInput) {
			input.ActorID = uuid.New()
		}, errTrainingPublicationReviewInvalid},
		{"admitted source exceeds the limit", func(_ *testing.T, q *publicationWitnessFault, _ *publicationConfirmInput) {
			q.budget.Overflow = true
		}, errTrainingPublicationReviewLimit},
		{"candidate count drifted before locking", func(_ *testing.T, q *publicationWitnessFault, _ *publicationConfirmInput) {
			q.budget.RawTupleCount++
		}, errStructuredTrainingPublicationConflict},
		{"placement head vanished", func(_ *testing.T, q *publicationWitnessFault, _ *publicationConfirmInput) {
			q.missingHead = true
		}, errStructuredTrainingPublicationConflict},
		{"membership witness vanished", func(_ *testing.T, q *publicationWitnessFault, _ *publicationConfirmInput) {
			q.missingMembership = true
		}, errStructuredTrainingPublicationConflict},
		{"subject user was not locked", func(_ *testing.T, q *publicationWitnessFault, _ *publicationConfirmInput) {
			q.missingUser = true
		}, errStructuredTrainingPublicationConflict},
		{"source was materialized under a different budget", func(_ *testing.T, q *publicationWitnessFault, _ *publicationConfirmInput) {
			q.differentBudget = true
		}, errStructuredTrainingPublicationConflict},
		{"source arrived truncated", func(_ *testing.T, q *publicationWitnessFault, _ *publicationConfirmInput) {
			q.raw = q.raw[:len(q.raw)-1]
			q.budget.SourceBytes--
		}, errPublicationSourceUnavailable},
		{"locked event was rescheduled", func(t *testing.T, q *publicationWitnessFault, _ *publicationConfirmInput) {
			before := publicationEvent{EventID: uuid.New(), StartsAt: pgtype.Timestamptz{Time: q.decision, Valid: true}}
			after := before
			after.StartsAt.Time = before.StartsAt.Time.Add(time.Hour)
			q.source.ReferencedEvents = []publicationEvent{before}
			material := q.source
			material.ReferencedEvents = []publicationEvent{after}
			var err error
			q.raw, err = json.Marshal(material)
			if err != nil {
				t.Fatal(err)
			}
			q.budget.SourceBytes = int64(len(q.raw))
		}, errStructuredTrainingPublicationConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, input, now := publicationTransactionFixture(t)
			q := publicationWitnessFault{publicationTransactionFake: base}
			tc.change(t, &q, &input)
			_, err := savePublicationInTransaction(t.Context(), q, input, time.UTC, func() time.Time { return now })
			if !errors.Is(err, tc.want) || slices.Contains(q.steps, "insert publication") {
				t.Fatalf("incomplete publication evidence reached writes: %v, %v", err, q.steps)
			}
		})
	}
}

func TestPublicationTransactionPropagatesWriteAndFinalWitnessFailures(t *testing.T) {
	storageErr := errors.New("synthetic storage unavailable")
	for _, tc := range []struct {
		name, stage      string
		err, want        error
		foreignSuccessor bool
	}{
		{"insert failed", "insert publication", storageErr, storageErr, false},
		{"successor for another plan", "insert publication", nil, errStructuredTrainingPublicationConflict, true},
		{"recipient disappeared during write", "insert prescription", pgx.ErrNoRows, errStructuredTrainingPublicationConflict, false},
		{"recipient write failed", "insert prescription", storageErr, storageErr, false},
		{"final witness could not be read", "final", storageErr, storageErr, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, input, now := publicationTransactionFixture(t)
			q := publicationWitnessFault{publicationTransactionFake: base, foreignSuccessor: tc.foreignSuccessor}
			if tc.err != nil {
				q.failStep, q.failErr = tc.stage, tc.err
			}
			_, err := savePublicationInTransaction(t.Context(), q, input, time.UTC, func() time.Time { return now })
			if !errors.Is(err, tc.want) || q.steps[len(q.steps)-1] != tc.stage {
				t.Fatalf("write or witness failure did not stop publication: %v, %v", err, q.steps)
			}
		})
	}
}

func TestPublicationFinalWitnessRejectsOverflow(t *testing.T) {
	q, input, now := publicationTransactionFixture(t)
	q.factsChange = func(f *publicationFinalFacts) { f.Overflow = true }
	_, err := savePublicationInTransaction(t.Context(), q, input, time.UTC, func() time.Time { return now })
	if !errors.Is(err, errTrainingPublicationReviewLimit) || q.steps[len(q.steps)-1] != "final" {
		t.Fatalf("final tuple limit did not refuse publication: %v, %v", err, q.steps)
	}
}
