//go:build integration

package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestStructuredPlannerStoreProtectsReviewAndStructuralWrites(t *testing.T) {
	f := newPublicationDomainFixture(t)
	ctx := context.WithValue(t.Context(), currentUserKey{}, CurrentUser{ID: f.actor})
	other := context.WithValue(t.Context(), currentUserKey{}, CurrentUser{ID: f.subject})
	store := PostgresStructuredTrainingStore{Pool: f.web}
	if hashes, err := store.ListLatestTrainingPrescriptionHashesForPlan(ctx, f.plan); err != nil || len(hashes) != 0 {
		t.Fatalf("unpublished plan returned hashes: %+v, %v", hashes, err)
	}
	var start time.Time
	if err := f.web.QueryRow(ctx, `SELECT starts_at FROM training_sessions WHERE id=$1`, f.session).Scan(&start); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateStructuredTrainingSession(ctx, dbgen.CreateStructuredTrainingSessionParams{Title: "Second exact workout", PlanID: f.plan, EntryKind: dbgen.TrainingEntryKindTRAINING, CreatedByID: f.actor, StartsAt: pgtype.Timestamptz{Time: start.Add(2 * time.Hour), Valid: true}, EndsAt: pgtype.Timestamptz{Time: start.Add(3 * time.Hour), Valid: true}})
	if err != nil || created.ID == uuid.Nil || created.SchedulePrecision != "INTERVAL" {
		t.Fatalf("exact workout: %+v, %v", created, err)
	}
	base := dbgen.CreateTrainingSegmentBlockParams{Purpose: dbgen.TrainingBlockPurposeMAIN, Title: "Reviewed work", Instructions: "Review only"}
	gymID, err := store.CreateSegment(ctx, StructuredTrainingSegmentInput{Segment: dbgen.CreateTrainingSessionSegmentParams{SessionID: created.ID, Modality: dbgen.TrainingSegmentModalityGYM, Title: "Gym"}, Block: base})
	if err != nil {
		t.Fatal(err)
	}
	waterID, err := store.CreateSegment(ctx, StructuredTrainingSegmentInput{Segment: dbgen.CreateTrainingSessionSegmentParams{SessionID: created.ID, Modality: dbgen.TrainingSegmentModalityWATER, Title: "Water"}, Block: base})
	if err != nil {
		t.Fatal(err)
	}
	negative := int32(-5)
	gym, water := base, base
	gym.SegmentID, water.SegmentID = gymID, waterID
	for _, call := range []func() (uuid.UUID, error){
		func() (uuid.UUID, error) {
			return store.CreateGymBlock(ctx, StructuredGymBlockInput{Block: gym, Prescription: dbgen.CreateGymBlockPrescriptionParams{Structure: dbgen.GymBlockStructureSTRAIGHTSETS, Objective: dbgen.TrainingObjectiveMOBILITY, Rounds: 0}})
		},
		func() (uuid.UUID, error) {
			return store.CreateWaterBlock(ctx, StructuredWaterBlockInput{Block: water, Prescription: dbgen.CreateWaterBlockPrescriptionParams{Method: dbgen.WaterWorkMethodCONTINUOUS, TargetDistanceMetres: &negative}})
		},
	} {
		id, err := call()
		var dbErr *pgconn.PgError
		if id != uuid.Nil || !errors.As(err, &dbErr) || dbErr.Code != "23514" {
			t.Fatalf("invalid child did not abort structural write: %s, %v", id, err)
		}
	}
	var blockCount int
	if err := f.web.QueryRow(ctx, `SELECT count(*) FROM training_segment_blocks WHERE segment_id IN ($1,$2)`, gymID, waterID).Scan(&blockCount); err != nil || blockCount != 2 {
		t.Fatalf("failed prescription left parent blocks: %d, %v", blockCount, err)
	}
	if _, err := store.CopyTrainingBlock(ctx, uuid.New(), gymID, f.actor); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown source block: %v", err)
	}
	if _, err := store.CopyTrainingBlock(ctx, uuid.New(), uuid.New(), f.actor); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown target segment: %v", err)
	}
	if _, err := store.CopyTrainingSession(ctx, uuid.New(), f.plan, pgtype.Timestamptz{}, f.actor); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown source session: %v", err)
	}
	if _, err := store.CopyTrainingSession(ctx, created.ID, uuid.New(), pgtype.Timestamptz{}, f.actor); !errors.Is(err, errTrainingScheduleUnavailable) {
		t.Fatalf("unknown target plan: %v", err)
	}
	if _, err := store.CopyTrainingSession(ctx, created.ID, f.plan, pgtype.Timestamptz{}, f.actor); !errors.Is(err, ErrTrainingScheduleInvalid) {
		t.Fatalf("missing destination time: %v", err)
	}
	if _, err := store.CopyStructuredTrainingDay(ctx, StructuredDayCopyInput{SourcePlanID: f.plan, TargetPlanID: uuid.New(), ActorID: f.actor}); !errors.Is(err, errTrainingScheduleUnavailable) {
		t.Fatalf("unknown destination week: %v", err)
	}
	if _, err := store.CopyStructuredTrainingCycle(ctx, StructuredTrainingCycleCopyInput{SourceCycleID: uuid.New(), ActorID: uuid.New()}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unrecognized actor reached cycle copy: %v", err)
	}

	source, err := store.GetRoutineSource(ctx, dbgen.TrainingRoutineKindSESSION, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	routine := dbgen.CreateTrainingRoutineParams{Kind: dbgen.TrainingRoutineKindSESSION, Visibility: dbgen.TrainingRoutineVisibilityPRIVATE, Name: "Private workout routine", OwnerUserID: f.actor, SourceID: created.ID, SourceUpdatedAt: source.SourceUpdatedAt, Snapshot: source.Snapshot, Tags: []string{}}
	bad := routine
	bad.Snapshot = []byte(`{"schema_version":99}`)
	if _, err := store.CreateTrainingRoutine(ctx, bad); !errors.Is(err, ErrTrainingSnapshotUnsupported) {
		t.Fatalf("unknown routine artifact version accepted: %v", err)
	}
	bad = routine
	bad.SourceUpdatedAt.Time = bad.SourceUpdatedAt.Time.Add(-time.Second)
	if _, err := store.CreateTrainingRoutine(ctx, bad); !errors.Is(err, errStructuredTrainingPublicationConflict) {
		t.Fatalf("stale routine source accepted: %v", err)
	}
	if row, err := store.CreateTrainingRoutine(ctx, routine); err != nil || row.ID == uuid.Nil {
		t.Fatalf("exact reviewed routine was not stored: %+v, %v", row, err)
	}
	if got, err := store.ConfirmPublication(ctx, f.review(t, "Reviewed private instructions")); err != nil || got.Revision != 1 || got.PrescriptionCount != 2 {
		t.Fatalf("publication: %+v, %v", got, err)
	}
	hashes, err := store.ListLatestTrainingPrescriptionHashesForPlan(ctx, f.plan)
	if err != nil || len(hashes) != 2 {
		t.Fatalf("authorized hashes: %+v, %v", hashes, err)
	}
	for _, hash := range hashes {
		if hash.MembershipID != f.membership || hash.PlacementID == nil || *hash.PlacementID != f.placement || len(hash.SnapshotSha256) != 64 {
			t.Fatalf("invalid scoped hash witness: %+v", hash)
		}
	}
	if hashes, err := store.ListLatestTrainingPrescriptionHashesForPlan(other, f.plan); !errors.Is(err, pgx.ErrNoRows) || len(hashes) != 0 {
		t.Fatalf("unscoped actor observed private hashes: %+v, %v", hashes, err)
	}
}
