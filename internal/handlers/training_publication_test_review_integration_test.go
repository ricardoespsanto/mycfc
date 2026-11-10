//go:build integration

package handlers

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/cfcoimbra/mycfc/internal/db"
	"os"
	"sync"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Test setup for historical callers now goes through the same canonical source,
// real MAC verification and admitted confirmation as HTTP. Caller snapshots are
// not publication authority: requested tuple IDs select canonical source bytes.
// Substituted/missing tuple IDs still produce a mismatching reviewed set.
// The production binary contains neither this fixture type nor a raw publisher.
type publicationTestReview struct {
	PlanID          uuid.UUID
	SourceUpdatedAt pgtype.Timestamptz
	ChangeSummary   string
	PublishedByID   uuid.UUID
	Prescriptions   []StructuredPrescriptionInput
}

func confirmPublicationTestReview(ctx context.Context, store PostgresStructuredTrainingStore, input publicationTestReview) (dbgen.TrainingPlanPublication, error) {
	var zero dbgen.TrainingPlanPublication
	original, ok := store.Pool.(*pgxpool.Pool)
	if !ok {
		return zero, errPublicationSourceUnavailable
	}
	var super bool
	if err := original.QueryRow(ctx, `SELECT rolsuper FROM pg_catalog.pg_roles WHERE rolname=session_user`).Scan(&super); err != nil {
		return zero, err
	}
	if super {
		user, password := os.Getenv("APP_DB_USER"), os.Getenv("APP_DB_PASSWORD")
		if user == "" || password == "" {
			return zero, errors.New("publication integration fixture requires explicit application role credentials")
		}
		cfg := original.Config()
		cfg.ConnConfig.User, cfg.ConnConfig.Password = user, password
		web, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			return zero, err
		}
		defer web.Close()
		store.Pool = web
	}
	source, err := store.ReadPublicationSource(ctx, input.PublishedByID, input.PlanID)
	if err != nil {
		return zero, err
	}
	location, _ := time.LoadLocation("Europe/Lisbon")
	material, err := materializePublicationSource(source, location)
	if err != nil {
		return zero, err
	}
	selected := material.Prescriptions
	if input.Prescriptions != nil {
		selected = make([]StructuredPrescriptionInput, 0, len(input.Prescriptions))
		for _, requested := range input.Prescriptions {
			found := false
			for _, canonical := range material.Prescriptions {
				if canonical.SessionID == requested.SessionID && canonical.MembershipID == requested.MembershipID && canonical.AthleteUserID == requested.AthleteUserID {
					selected = append(selected, canonical)
					found = true
					break
				}
			}
			if !found {
				if len(material.Prescriptions) > 0 {
					requested.Snapshot = material.Prescriptions[0].Snapshot
				} else {
					requested.Snapshot = []byte(`{"schema_version":1,"session":{"EntryKind":"WORKOUT"}}`)
				}
				sum := sha256.Sum256(requested.Snapshot)
				requested.SnapshotSHA256 = fmt.Sprintf("%x", sum)
				selected = append(selected, requested)
			}
		}
	}
	metadata := trainingPublicationReviewMetadata{ActorID: input.PublishedByID, PlanID: input.PlanID, SourceUpdatedAt: input.SourceUpdatedAt.Time, ChangeSummary: input.ChangeSummary, LatestRevision: source.Header.LatestRevision}
	if source.Header.LatestPublicationID != nil {
		metadata.LatestPublicationID = *source.Header.LatestPublicationID
	}
	token, err := trainingPublicationReviews.sign(trainingPublicationReviewInput{metadata, selected}, time.Now())
	if err != nil {
		return zero, err
	}
	claims, err := trainingPublicationReviews.verify(token, metadata, time.Now())
	if err != nil {
		return zero, err
	}
	confirmed := publicationConfirmInput{ActorID: input.PublishedByID, PlanID: input.PlanID, Review: claims}
	var result publicationSaveResult
	if gate, ok := ctx.Value(publicationTestGateKey{}).(*publicationTestGate); ok {
		pool := store.Pool.(*pgxpool.Pool)
		outcome, bodyErr := db.WithRecipientPublicationTx(ctx, pool, db.RecipientTxOptions{AdmissionTimeout: 5 * time.Second, TransactionTimeout: 10 * time.Second, CleanupTimeout: 5 * time.Second}, func(txCtx context.Context, commands dbgen.DBTX) error {
			q := publicationTestPauseQueries{publicationTransactionQueries: publicationPostgresQueries{commands: commands, queries: dbgen.New(commands)}, gate: gate}
			var e error
			result, e = savePublicationInTransaction(txCtx, q, confirmed, location, time.Now)
			return e
		})
		err = publicationTransactionError(outcome, bodyErr)
	} else {
		result, err = store.ConfirmPublication(ctx, confirmed)
	}
	if err != nil {
		if errors.Is(err, errTrainingPublicationReviewChanged) {
			return zero, errStructuredTrainingPublicationConflict
		}
		return zero, err
	}
	var publication dbgen.TrainingPlanPublication
	err = original.QueryRow(ctx, `SELECT id,plan_id,revision,source_updated_at,change_summary,supersedes_id,published_by_id,published_at FROM training_plan_publications WHERE id=$1`, result.PublicationID).Scan(&publication.ID, &publication.PlanID, &publication.Revision, &publication.SourceUpdatedAt, &publication.ChangeSummary, &publication.SupersedesID, &publication.PublishedByID, &publication.PublishedAt)
	return publication, err
}

type publicationTestGateKey struct{}
type publicationTestGate struct {
	reached chan struct{}
	release chan struct{}
}
type publicationTestPauseQueries struct {
	publicationTransactionQueries
	gate *publicationTestGate
}

func (q publicationTestPauseQueries) InsertPublication(ctx context.Context, actor uuid.UUID, h publicationPlanHeader, summary string) (dbgen.TrainingPlanPublication, error) {
	close(q.gate.reached)
	select {
	case <-q.gate.release:
	case <-ctx.Done():
		return dbgen.TrainingPlanPublication{}, ctx.Err()
	}
	return q.publicationTransactionQueries.InsertPublication(ctx, actor, h, summary)
}
func withPublicationTestGate(ctx context.Context) (context.Context, <-chan struct{}, func()) {
	gate := &publicationTestGate{reached: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	return context.WithValue(ctx, publicationTestGateKey{}, gate), gate.reached, func() { once.Do(func() { close(gate.release) }) }
}
func awaitPublicationTestGate(t *testing.T, ctx context.Context, reached <-chan struct{}, done <-chan error) {
	t.Helper()
	select {
	case <-reached:
	case err := <-done:
		t.Fatalf("publication failed before admitted insert gate: %v", err)
	case <-ctx.Done():
		t.Fatal("publication did not reach admitted gate", ctx.Err())
	}
}
