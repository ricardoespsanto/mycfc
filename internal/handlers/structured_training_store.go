package handlers

import (
	"context"
	"errors"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errStructuredTrainingMembershipScope = errors.New("membership is outside the training group scope")

type StructuredTrainingGroupInput struct {
	Params        dbgen.CreateStructuredTrainingGroupParams
	MembershipIDs []uuid.UUID
}

type StructuredTrainingSegmentInput struct {
	Segment dbgen.CreateTrainingSessionSegmentParams
	Block   dbgen.CreateTrainingSegmentBlockParams
}

type StructuredTrainingStore interface {
	ListEligibleTrainingGroupMemberships(context.Context, dbgen.ListEligibleTrainingGroupMembershipsParams) ([]dbgen.ListEligibleTrainingGroupMembershipsRow, error)
	ListStructuredTrainingOverviewForManager(context.Context, dbgen.ListStructuredTrainingOverviewForManagerParams) ([]dbgen.ListStructuredTrainingOverviewForManagerRow, error)
	ListStructuredTrainingOverviewForSubject(context.Context, uuid.UUID) ([]dbgen.ListStructuredTrainingOverviewForSubjectRow, error)
	CreateGroup(context.Context, StructuredTrainingGroupInput) (dbgen.TrainingGroup, error)
	CanManageStructuredTrainingGroup(context.Context, dbgen.CanManageStructuredTrainingGroupParams) (bool, error)
	CreateStructuredTrainingWeek(context.Context, dbgen.CreateStructuredTrainingWeekParams) (dbgen.TrainingPlan, error)
	CanManageStructuredTrainingWeek(context.Context, dbgen.CanManageStructuredTrainingWeekParams) (bool, error)
	CreateStructuredTrainingSession(context.Context, dbgen.CreateStructuredTrainingSessionParams) (dbgen.TrainingSession, error)
	CreateSegment(context.Context, StructuredTrainingSegmentInput) (uuid.UUID, error)
	CreateTrainingSegmentBlock(context.Context, dbgen.CreateTrainingSegmentBlockParams) (uuid.UUID, error)
	GetStructuredSessionPlanID(context.Context, uuid.UUID) (uuid.UUID, error)
	GetStructuredSegmentPlanID(context.Context, uuid.UUID) (uuid.UUID, error)
	GetStructuredBlockPlanID(context.Context, uuid.UUID) (uuid.UUID, error)
	MoveTrainingSessionSegment(context.Context, dbgen.MoveTrainingSessionSegmentParams) (bool, error)
	MoveTrainingSegmentBlock(context.Context, dbgen.MoveTrainingSegmentBlockParams) (bool, error)
}

type PostgresStructuredTrainingStore struct {
	Pool *pgxpool.Pool
}

func (s PostgresStructuredTrainingStore) queries() *dbgen.Queries { return dbgen.New(s.Pool) }

func (s PostgresStructuredTrainingStore) ListEligibleTrainingGroupMemberships(ctx context.Context, params dbgen.ListEligibleTrainingGroupMembershipsParams) ([]dbgen.ListEligibleTrainingGroupMembershipsRow, error) {
	return s.queries().ListEligibleTrainingGroupMemberships(ctx, params)
}

func (s PostgresStructuredTrainingStore) ListStructuredTrainingOverviewForManager(ctx context.Context, params dbgen.ListStructuredTrainingOverviewForManagerParams) ([]dbgen.ListStructuredTrainingOverviewForManagerRow, error) {
	return s.queries().ListStructuredTrainingOverviewForManager(ctx, params)
}

func (s PostgresStructuredTrainingStore) ListStructuredTrainingOverviewForSubject(ctx context.Context, userID uuid.UUID) ([]dbgen.ListStructuredTrainingOverviewForSubjectRow, error) {
	return s.queries().ListStructuredTrainingOverviewForSubject(ctx, userID)
}

func (s PostgresStructuredTrainingStore) CreateGroup(ctx context.Context, input StructuredTrainingGroupInput) (group dbgen.TrainingGroup, err error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return group, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := dbgen.New(tx)
	group, err = queries.CreateStructuredTrainingGroup(ctx, input.Params)
	if err != nil {
		return group, err
	}
	for _, membershipID := range input.MembershipIDs {
		rows, addErr := queries.AddStructuredTrainingGroupMember(ctx, dbgen.AddStructuredTrainingGroupMemberParams{GroupID: group.ID, MembershipID: membershipID, AddedByID: input.Params.CreatedByID})
		if addErr != nil {
			return group, addErr
		}
		if rows != 1 {
			return group, errStructuredTrainingMembershipScope
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return group, err
	}
	return group, nil
}

func (s PostgresStructuredTrainingStore) CanManageStructuredTrainingGroup(ctx context.Context, params dbgen.CanManageStructuredTrainingGroupParams) (bool, error) {
	return s.queries().CanManageStructuredTrainingGroup(ctx, params)
}

func (s PostgresStructuredTrainingStore) CreateStructuredTrainingWeek(ctx context.Context, params dbgen.CreateStructuredTrainingWeekParams) (dbgen.TrainingPlan, error) {
	return s.queries().CreateStructuredTrainingWeek(ctx, params)
}

func (s PostgresStructuredTrainingStore) CanManageStructuredTrainingWeek(ctx context.Context, params dbgen.CanManageStructuredTrainingWeekParams) (bool, error) {
	return s.queries().CanManageStructuredTrainingWeek(ctx, params)
}

func (s PostgresStructuredTrainingStore) CreateStructuredTrainingSession(ctx context.Context, params dbgen.CreateStructuredTrainingSessionParams) (dbgen.TrainingSession, error) {
	return s.queries().CreateStructuredTrainingSession(ctx, params)
}

func (s PostgresStructuredTrainingStore) CreateSegment(ctx context.Context, input StructuredTrainingSegmentInput) (segmentID uuid.UUID, err error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := dbgen.New(tx)
	segmentID, err = queries.CreateTrainingSessionSegment(ctx, input.Segment)
	if err != nil {
		return uuid.Nil, err
	}
	input.Block.SegmentID = segmentID
	if _, err = queries.CreateTrainingSegmentBlock(ctx, input.Block); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return segmentID, nil
}

func (s PostgresStructuredTrainingStore) CreateTrainingSegmentBlock(ctx context.Context, params dbgen.CreateTrainingSegmentBlockParams) (uuid.UUID, error) {
	return s.queries().CreateTrainingSegmentBlock(ctx, params)
}

func (s PostgresStructuredTrainingStore) GetStructuredSessionPlanID(ctx context.Context, sessionID uuid.UUID) (uuid.UUID, error) {
	return s.queries().GetStructuredSessionPlanID(ctx, sessionID)
}

func (s PostgresStructuredTrainingStore) GetStructuredSegmentPlanID(ctx context.Context, segmentID uuid.UUID) (uuid.UUID, error) {
	return s.queries().GetStructuredSegmentPlanID(ctx, segmentID)
}

func (s PostgresStructuredTrainingStore) GetStructuredBlockPlanID(ctx context.Context, blockID uuid.UUID) (uuid.UUID, error) {
	return s.queries().GetStructuredBlockPlanID(ctx, blockID)
}

func (s PostgresStructuredTrainingStore) MoveTrainingSessionSegment(ctx context.Context, params dbgen.MoveTrainingSessionSegmentParams) (bool, error) {
	return s.queries().MoveTrainingSessionSegment(ctx, params)
}

func (s PostgresStructuredTrainingStore) MoveTrainingSegmentBlock(ctx context.Context, params dbgen.MoveTrainingSegmentBlockParams) (bool, error) {
	return s.queries().MoveTrainingSegmentBlock(ctx, params)
}
