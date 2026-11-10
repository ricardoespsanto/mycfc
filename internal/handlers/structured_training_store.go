package handlers

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

var errStructuredTrainingMembershipScope = errors.New("membership is outside the training group scope")
var errStructuredVariationMemberScope = errors.New("variation member is outside the training group")
var errStructuredVariationCrewCapacity = errors.New("variation crew does not match the craft capacity")
var errStructuredTrainingPublicationConflict = errors.New("structured training plan changed before publication")
var errStructuredTrainingPublicationVariationConflict = errors.New("structured training variation conflict prevents publication")
var errStructuredTrainingCycleScope = errors.New("training cycle weeks must share one group and season")
var errStructuredTrainingCycleConflict = errors.New("training cycle changed before update")

type StructuredTrainingGroupInput struct {
	Params        dbgen.CreateStructuredTrainingGroupParams
	MembershipIDs []uuid.UUID
}

type StructuredVariationGroupInput struct {
	Params        dbgen.CreateTrainingVariationGroupParams
	MembershipIDs []uuid.UUID
}

type StructuredTrainingSegmentInput struct {
	Segment dbgen.CreateTrainingSessionSegmentParams
	Block   dbgen.CreateTrainingSegmentBlockParams
}

type StructuredGymBlockInput struct {
	Block        dbgen.CreateTrainingSegmentBlockParams
	Prescription dbgen.CreateGymBlockPrescriptionParams
	Exercise     dbgen.CreateGymExerciseParams
}

type StructuredWaterBlockInput struct {
	Block        dbgen.CreateTrainingSegmentBlockParams
	Prescription dbgen.CreateWaterBlockPrescriptionParams
	Step         dbgen.CreateWaterWorkStepParams
}

type StructuredRoutineSource struct {
	PlanID          uuid.UUID
	SourceUpdatedAt pgtype.Timestamptz
	ProgrammeID     *uuid.UUID
	TeamID          *uuid.UUID
	Modality        *dbgen.TrainingSegmentModality
	Objective       *dbgen.TrainingObjective
	Snapshot        []byte
}

type StructuredRoutineInsertInput struct {
	Routine  dbgen.TrainingRoutine
	TargetID uuid.UUID
	StartsAt pgtype.Timestamptz
	ActorID  uuid.UUID
}

type StructuredWeekCopyInput struct {
	SourcePlanID uuid.UUID
	WeekStart    time.Time
	Title        string
	Description  string
	ActorID      uuid.UUID
}

type StructuredTrainingCycleInput struct {
	CycleID         uuid.UUID
	TrainingGroupID uuid.UUID
	ExpectedVersion int32
	Name            string
	LevelLabel      string
	Goals           string
	PhaseFocusNotes string
	WeekIDs         []uuid.UUID
	ChildCycleIDs   []uuid.UUID
	TargetEventIDs  []uuid.UUID
	ActorID         uuid.UUID
	IsAdmin         bool
}

type StructuredTrainingCycleCopyInput struct {
	SourceCycleID uuid.UUID
	FirstMonday   time.Time
	Name          string
	ActorID       uuid.UUID
}

type StructuredDayCopyInput struct {
	SourcePlanID, TargetPlanID uuid.UUID
	SourceDate, TargetDate     time.Time
	ActorID                    uuid.UUID
}

type StructuredPrescriptionInput struct {
	PlacementID    uuid.UUID
	SessionID      uuid.UUID
	MembershipID   uuid.UUID
	AthleteUserID  uuid.UUID
	Snapshot       []byte
	SnapshotSHA256 string
}

type StructuredTrainingStore interface {
	ListEligibleTrainingGroupMemberships(context.Context, dbgen.ListEligibleTrainingGroupMembershipsParams) ([]dbgen.ListEligibleTrainingGroupMembershipsRow, error)
	ListStructuredTrainingOverviewForManager(context.Context, dbgen.ListStructuredTrainingOverviewForManagerParams) ([]dbgen.ListStructuredTrainingOverviewForManagerRow, error)
	ListStructuredTrainingOverviewForSubject(context.Context, uuid.UUID) ([]dbgen.ListStructuredTrainingOverviewForSubjectRow, error)
	CreateGroup(context.Context, StructuredTrainingGroupInput) (dbgen.TrainingGroup, error)
	CanManageStructuredTrainingGroup(context.Context, dbgen.CanManageStructuredTrainingGroupParams) (bool, error)
	CreateStructuredTrainingWeek(context.Context, dbgen.CreateStructuredTrainingWeekParams) (dbgen.TrainingPlan, error)
	CanManageStructuredTrainingWeek(context.Context, dbgen.CanManageStructuredTrainingWeekParams) (bool, error)
	UpdateStructuredTrainingWeekLoad(context.Context, dbgen.UpdateStructuredTrainingWeekLoadParams) (int64, error)
	ListManagedTrainingCycles(context.Context, dbgen.ListManagedTrainingCyclesParams) ([]dbgen.ListManagedTrainingCyclesRow, error)
	ListManagedTrainingCycleWeeks(context.Context, dbgen.ListManagedTrainingCycleWeeksParams) ([]dbgen.ListManagedTrainingCycleWeeksRow, error)
	ListManagedTrainingCycleTargets(context.Context, dbgen.ListManagedTrainingCycleTargetsParams) ([]dbgen.ListManagedTrainingCycleTargetsRow, error)
	CanManageTrainingCycle(context.Context, dbgen.CanManageTrainingCycleParams) (bool, error)
	SaveTrainingCycle(context.Context, StructuredTrainingCycleInput) (dbgen.TrainingCycle, error)
	CopyStructuredTrainingCycle(context.Context, StructuredTrainingCycleCopyInput) (dbgen.TrainingCycle, error)
	CreateStructuredTrainingSession(context.Context, dbgen.CreateStructuredTrainingSessionParams) (dbgen.TrainingSession, error)
	CreateSegment(context.Context, StructuredTrainingSegmentInput) (uuid.UUID, error)
	CreateTrainingSegmentBlock(context.Context, dbgen.CreateTrainingSegmentBlockParams) (uuid.UUID, error)
	CreateGymBlock(context.Context, StructuredGymBlockInput) (uuid.UUID, error)
	CreateGymExercise(context.Context, dbgen.CreateGymExerciseParams) (uuid.UUID, error)
	CreateWaterBlock(context.Context, StructuredWaterBlockInput) (uuid.UUID, error)
	CreateWaterWorkStep(context.Context, dbgen.CreateWaterWorkStepParams) (uuid.UUID, error)
	CreateWaterIntensityProfile(context.Context, dbgen.CreateWaterIntensityProfileParams) (dbgen.CreateWaterIntensityProfileRow, error)
	CreateWaterIntensityZone(context.Context, dbgen.CreateWaterIntensityZoneParams) (uuid.UUID, error)
	ListActiveWaterIntensityProfiles(context.Context) ([]dbgen.ListActiveWaterIntensityProfilesRow, error)
	GetStructuredSessionPlanID(context.Context, uuid.UUID) (uuid.UUID, error)
	GetStructuredSegmentPlanID(context.Context, uuid.UUID) (uuid.UUID, error)
	GetStructuredBlockPlanID(context.Context, uuid.UUID) (uuid.UUID, error)
	GetGymExercisePlanID(context.Context, uuid.UUID) (uuid.UUID, error)
	MoveTrainingSessionSegment(context.Context, dbgen.MoveTrainingSessionSegmentParams) (bool, error)
	MoveTrainingSegmentBlock(context.Context, dbgen.MoveTrainingSegmentBlockParams) (bool, error)
	MoveGymExercise(context.Context, dbgen.MoveGymExerciseParams) (bool, error)
	GetRoutineSource(context.Context, dbgen.TrainingRoutineKind, uuid.UUID) (StructuredRoutineSource, error)
	CreateTrainingRoutine(context.Context, dbgen.CreateTrainingRoutineParams) (dbgen.TrainingRoutine, error)
	ListVisibleTrainingRoutines(context.Context, dbgen.ListVisibleTrainingRoutinesParams) ([]dbgen.ListVisibleTrainingRoutinesRow, error)
	GetVisibleTrainingRoutine(context.Context, dbgen.GetVisibleTrainingRoutineParams) (dbgen.TrainingRoutine, error)
	InsertTrainingRoutine(context.Context, StructuredRoutineInsertInput) (uuid.UUID, error)
	CopyTrainingBlock(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (uuid.UUID, error)
	CopyTrainingSession(context.Context, uuid.UUID, uuid.UUID, pgtype.Timestamptz, uuid.UUID) (uuid.UUID, error)
	CopyStructuredTrainingDay(context.Context, StructuredDayCopyInput) (int, error)
	CopyStructuredTrainingWeek(context.Context, StructuredWeekCopyInput) (dbgen.TrainingPlan, error)
	GetStructuredPlanCopySource(context.Context, uuid.UUID) (dbgen.GetStructuredPlanCopySourceRow, error)
	ListManagedTrainingGroupMembers(context.Context, dbgen.ListManagedTrainingGroupMembersParams) ([]dbgen.ListManagedTrainingGroupMembersRow, error)
	ListStructuredCrewModalities(context.Context) ([]dbgen.CanoeCraftClass, error)
	ListManagedStructuredCompetitionEvents(context.Context, dbgen.ListManagedStructuredCompetitionEventsParams) ([]dbgen.ListManagedStructuredCompetitionEventsRow, error)
	CreateTrainingVariationGroup(context.Context, StructuredVariationGroupInput) (dbgen.TrainingVariationGroup, error)
	ListManagedTrainingVariationGroups(context.Context, dbgen.ListManagedTrainingVariationGroupsParams) ([]dbgen.ListManagedTrainingVariationGroupsRow, error)
	CreateTrainingVariation(context.Context, dbgen.CreateTrainingVariationParams) (dbgen.TrainingVariation, error)
	ListTrainingVariationMatchesForManager(context.Context, dbgen.ListTrainingVariationMatchesForManagerParams) ([]dbgen.ListTrainingVariationMatchesForManagerRow, error)
	GetTrainingVariationPlanID(context.Context, uuid.UUID) (uuid.UUID, error)
	RetireTrainingVariation(context.Context, dbgen.RetireTrainingVariationParams) (int64, error)
	ListManagedTrainingPublicationStates(context.Context, dbgen.ListManagedTrainingPublicationStatesParams) ([]dbgen.ListManagedTrainingPublicationStatesRow, error)
	ListStructuredTrainingPublicationMembers(context.Context, uuid.UUID) ([]dbgen.ListStructuredTrainingPublicationMembersRow, error)
	ListTrainingPrescriptionsForViewer(context.Context, dbgen.ListTrainingPrescriptionsForViewerParams) ([]dbgen.ListTrainingPrescriptionsForViewerRow, error)
	ListLatestTrainingPrescriptionHashesForPlan(context.Context, uuid.UUID) ([]dbgen.ListLatestTrainingPrescriptionHashesForPlanRow, error)
	GetTrainingPrescriptionForViewer(context.Context, dbgen.GetTrainingPrescriptionForViewerParams) (dbgen.GetTrainingPrescriptionForViewerRow, error)
	ListTrainingPrescriptionLinksForSessionViewer(context.Context, dbgen.ListTrainingPrescriptionLinksForSessionViewerParams) ([]dbgen.ListTrainingPrescriptionLinksForSessionViewerRow, error)
}

type structuredTrainingDB interface {
	dbgen.DBTX
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type structuredTrainingCycleTx interface {
	Commit(context.Context) error
	Rollback(context.Context) error
}

type structuredTrainingCycleQueries interface {
	trainingSchedulePlanQueries
	LockTrainingCycles(context.Context, []uuid.UUID) ([]dbgen.TrainingCycle, error)
	GetTrainingCycleWeekScope(context.Context, uuid.UUID) (dbgen.GetTrainingCycleWeekScopeRow, error)
	CreateTrainingCycle(context.Context, dbgen.CreateTrainingCycleParams) (dbgen.TrainingCycle, error)
	UpdateTrainingCycle(context.Context, dbgen.UpdateTrainingCycleParams) (dbgen.TrainingCycle, error)
	ClearTrainingCycleChildren(context.Context, uuid.UUID) error
	AssignTrainingCycleChild(context.Context, dbgen.AssignTrainingCycleChildParams) (int64, error)
	ClearTrainingCycleWeeks(context.Context, uuid.UUID) error
	AssignTrainingWeekToCycle(context.Context, dbgen.AssignTrainingWeekToCycleParams) (int64, error)
	ClearManageableTrainingCycleTargets(context.Context, dbgen.ClearManageableTrainingCycleTargetsParams) error
	AddTrainingCycleTarget(context.Context, dbgen.AddTrainingCycleTargetParams) (int64, error)
	GetTrainingCycleCopySource(context.Context, uuid.UUID) (dbgen.TrainingCycle, error)
	ListTrainingCycleWeekCopySources(context.Context, uuid.UUID) ([]dbgen.ListTrainingCycleWeekCopySourcesRow, error)
	CreateStructuredTrainingWeek(context.Context, dbgen.CreateStructuredTrainingWeekParams) (dbgen.TrainingPlan, error)
	ListStructuredSessionSnapshotsForPlan(context.Context, uuid.UUID) ([]dbgen.ListStructuredSessionSnapshotsForPlanRow, error)
	RestoreTrainingSession(context.Context, dbgen.RestoreTrainingSessionParams) (uuid.UUID, error)
	CreateTrainingCopyEvent(context.Context, dbgen.CreateTrainingCopyEventParams) error
}

type PostgresStructuredTrainingStore struct {
	Pool         structuredTrainingDB
	beginCycleTx func(context.Context) (structuredTrainingCycleTx, structuredTrainingCycleQueries, error)
}

func (s PostgresStructuredTrainingStore) cycleTransaction(ctx context.Context, actor uuid.UUID) (structuredTrainingCycleTx, structuredTrainingCycleQueries, error) {
	if s.beginCycleTx != nil {
		return s.beginCycleTx(ctx)
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}
	if err = lockTrainingGroupActor(ctx, tx, actor); err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, err
	}
	return tx, dbgen.New(tx), nil
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

func (s PostgresStructuredTrainingStore) ListManagedTrainingPublicationStates(ctx context.Context, params dbgen.ListManagedTrainingPublicationStatesParams) ([]dbgen.ListManagedTrainingPublicationStatesRow, error) {
	return s.queries().ListManagedTrainingPublicationStates(ctx, params)
}

func (s PostgresStructuredTrainingStore) ListStructuredTrainingPublicationMembers(ctx context.Context, plan uuid.UUID) ([]dbgen.ListStructuredTrainingPublicationMembersRow, error) {
	rows, err := s.queries().PublicationEligibleRecipients(ctx, dbgen.PublicationEligibleRecipientsParams{ActorID: trainingWriteActor(ctx), PlanID: plan})
	if err != nil {
		return nil, err
	}
	result := make([]dbgen.ListStructuredTrainingPublicationMembersRow, len(rows))
	for i, row := range rows {
		if row.MembershipID == nil || *row.MembershipID == uuid.Nil || row.PlacementID == uuid.Nil {
			return nil, errPublicationSourceUnavailable
		}
		result[i] = dbgen.ListStructuredTrainingPublicationMembersRow{PlacementID: row.PlacementID, MembershipID: *row.MembershipID, AthleteUserID: &row.AthleteUserID, AthleteName: row.AthleteName, StartsOn: row.StartsOn, EndsOn: row.EndsOn, SessionID: row.SessionID}
	}
	return result, nil
}

func (s PostgresStructuredTrainingStore) ListTrainingPrescriptionsForViewer(ctx context.Context, params dbgen.ListTrainingPrescriptionsForViewerParams) ([]dbgen.ListTrainingPrescriptionsForViewerRow, error) {
	return s.queries().ListTrainingPrescriptionsForViewer(ctx, params)
}

func (s PostgresStructuredTrainingStore) ListLatestTrainingPrescriptionHashesForPlan(ctx context.Context, planID uuid.UUID) ([]dbgen.ListLatestTrainingPrescriptionHashesForPlanRow, error) {
	actor := trainingWriteActor(ctx)
	q := s.queries()
	header, err := q.PublicationPlanForReview(ctx, dbgen.PublicationPlanForReviewParams{ActorID: actor, PlanID: planID})
	if err != nil {
		return nil, err
	}
	if header.LatestPublicationID == nil {
		return []dbgen.ListLatestTrainingPrescriptionHashesForPlanRow{}, nil
	}
	rows, err := q.PublicationLatestHashes(ctx, dbgen.PublicationLatestHashesParams{ActorID: actor, PlanID: planID, PublicationID: *header.LatestPublicationID})
	if err != nil {
		return nil, err
	}
	if len(rows) > trainingPublicationReviewMaxTuples {
		return nil, errTrainingPublicationReviewLimit
	}
	result := make([]dbgen.ListLatestTrainingPrescriptionHashesForPlanRow, len(rows))
	for i, row := range rows {
		result[i] = dbgen.ListLatestTrainingPrescriptionHashesForPlanRow{PlacementID: row.PlacementID, MembershipID: row.MembershipID, SessionID: row.SessionID, SnapshotSha256: row.SnapshotSha256}
	}
	return result, nil
}

func (s PostgresStructuredTrainingStore) GetTrainingPrescriptionForViewer(ctx context.Context, params dbgen.GetTrainingPrescriptionForViewerParams) (dbgen.GetTrainingPrescriptionForViewerRow, error) {
	return s.queries().GetTrainingPrescriptionForViewer(ctx, params)
}

func (s PostgresStructuredTrainingStore) ListTrainingPrescriptionLinksForSessionViewer(ctx context.Context, params dbgen.ListTrainingPrescriptionLinksForSessionViewerParams) ([]dbgen.ListTrainingPrescriptionLinksForSessionViewerRow, error) {
	return s.queries().ListTrainingPrescriptionLinksForSessionViewer(ctx, params)
}

func structuredPublicationConcurrencyError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "40P01" || pgErr.Code == "40001") {
		return errStructuredTrainingPublicationConflict
	}
	return err
}

func (s PostgresStructuredTrainingStore) CreateGroup(ctx context.Context, input StructuredTrainingGroupInput) (dbgen.TrainingGroup, error) {
	_, row, err := (PostgresTrainingGroupStore{Pool: s.Pool}).createGroupWithMembers(ctx, TrainingGroupCreateInput{ActorID: input.Params.CreatedByID, Name: input.Params.Name, ProgrammeID: input.Params.ProgrammeID, TeamID: input.Params.TeamID}, input.MembershipIDs)
	if errors.Is(err, ErrTrainingGroupEligibility) {
		err = errStructuredTrainingMembershipScope
	}
	return row, err
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

func (s PostgresStructuredTrainingStore) UpdateStructuredTrainingWeekLoad(ctx context.Context, params dbgen.UpdateStructuredTrainingWeekLoadParams) (int64, error) {
	return withTrainingSchedulePlan(ctx, s, params.UserID, params.PlanID, func(q *dbgen.Queries) (int64, error) { return q.UpdateStructuredTrainingWeekLoad(ctx, params) })
}

func (s PostgresStructuredTrainingStore) ListManagedTrainingCycles(ctx context.Context, params dbgen.ListManagedTrainingCyclesParams) ([]dbgen.ListManagedTrainingCyclesRow, error) {
	return s.queries().ListManagedTrainingCycles(ctx, params)
}

func (s PostgresStructuredTrainingStore) ListManagedTrainingCycleWeeks(ctx context.Context, params dbgen.ListManagedTrainingCycleWeeksParams) ([]dbgen.ListManagedTrainingCycleWeeksRow, error) {
	return s.queries().ListManagedTrainingCycleWeeks(ctx, params)
}

func (s PostgresStructuredTrainingStore) ListManagedTrainingCycleTargets(ctx context.Context, params dbgen.ListManagedTrainingCycleTargetsParams) ([]dbgen.ListManagedTrainingCycleTargetsRow, error) {
	return s.queries().ListManagedTrainingCycleTargets(ctx, params)
}

func (s PostgresStructuredTrainingStore) CanManageTrainingCycle(ctx context.Context, params dbgen.CanManageTrainingCycleParams) (bool, error) {
	return s.queries().CanManageTrainingCycle(ctx, params)
}

func (s PostgresStructuredTrainingStore) SaveTrainingCycle(ctx context.Context, input StructuredTrainingCycleInput) (cycle dbgen.TrainingCycle, err error) {
	defer func() { err = trainingScheduleWriteError(err) }()
	if len(input.WeekIDs) == 0 && len(input.ChildCycleIDs) == 0 {
		return cycle, errStructuredTrainingCycleScope
	}
	tx, queries, err := s.cycleTransaction(ctx, input.ActorID)
	if err != nil {
		return cycle, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	sort.Slice(input.ChildCycleIDs, func(left, right int) bool {
		return input.ChildCycleIDs[left].String() < input.ChildCycleIDs[right].String()
	})
	sort.Slice(input.WeekIDs, func(left, right int) bool { return input.WeekIDs[left].String() < input.WeekIDs[right].String() })
	cycleIDsToLock := append([]uuid.UUID{}, input.ChildCycleIDs...)
	if input.CycleID != uuid.Nil {
		cycleIDsToLock = append(cycleIDsToLock, input.CycleID)
	}
	sort.Slice(cycleIDsToLock, func(left, right int) bool { return cycleIDsToLock[left].String() < cycleIDsToLock[right].String() })
	lockedCycles, err := queries.LockTrainingCycles(ctx, cycleIDsToLock)
	if err != nil {
		return cycle, err
	}
	lockedByID := make(map[uuid.UUID]dbgen.TrainingCycle, len(lockedCycles))
	for _, locked := range lockedCycles {
		lockedByID[locked.ID] = locked
	}
	if len(lockedByID) != len(cycleIDsToLock) {
		return cycle, errStructuredTrainingCycleScope
	}
	if input.CycleID != uuid.Nil && lockedByID[input.CycleID].Version != input.ExpectedVersion {
		return cycle, errStructuredTrainingCycleConflict
	}
	var groupID, seasonID uuid.UUID
	seenChildren := make(map[uuid.UUID]bool, len(input.ChildCycleIDs))
	for _, childID := range input.ChildCycleIDs {
		if childID == input.CycleID || seenChildren[childID] {
			return cycle, errStructuredTrainingCycleScope
		}
		seenChildren[childID] = true
		child, found := lockedByID[childID]
		if !found || (child.ParentCycleID != nil && (input.CycleID == uuid.Nil || *child.ParentCycleID != input.CycleID)) {
			return cycle, errStructuredTrainingCycleScope
		}
		if groupID == uuid.Nil {
			groupID, seasonID = child.TrainingGroupID, child.SeasonID
		} else if child.TrainingGroupID != groupID || child.SeasonID != seasonID {
			return cycle, errStructuredTrainingCycleScope
		}
	}
	seenWeeks := make(map[uuid.UUID]bool, len(input.WeekIDs))
	for _, weekID := range input.WeekIDs {
		if seenWeeks[weekID] {
			return cycle, errStructuredTrainingCycleScope
		}
		seenWeeks[weekID] = true
		week, scopeErr := queries.GetTrainingCycleWeekScope(ctx, weekID)
		if scopeErr != nil || week.TrainingGroupID == nil || week.SeasonID == nil {
			return cycle, errStructuredTrainingCycleScope
		}
		if week.CycleID != nil && (input.CycleID == uuid.Nil || *week.CycleID != input.CycleID) {
			return cycle, errStructuredTrainingCycleScope
		}
		if groupID == uuid.Nil {
			groupID, seasonID = *week.TrainingGroupID, *week.SeasonID
		} else if *week.TrainingGroupID != groupID || *week.SeasonID != seasonID {
			return cycle, errStructuredTrainingCycleScope
		}
	}
	if input.CycleID == uuid.Nil {
		if input.TrainingGroupID == uuid.Nil || input.TrainingGroupID != groupID {
			return cycle, errStructuredTrainingCycleScope
		}
		cycle, err = queries.CreateTrainingCycle(ctx, dbgen.CreateTrainingCycleParams{
			TrainingGroupID: groupID, SeasonID: seasonID,
			Name: input.Name, LevelLabel: input.LevelLabel, Goals: input.Goals,
			PhaseFocusNotes: input.PhaseFocusNotes, CreatedByID: input.ActorID,
		})
	} else {
		cycle, err = queries.UpdateTrainingCycle(ctx, dbgen.UpdateTrainingCycleParams{
			Name: input.Name, LevelLabel: input.LevelLabel, Goals: input.Goals,
			PhaseFocusNotes: input.PhaseFocusNotes, UpdatedByID: input.ActorID,
			CycleID: input.CycleID, ExpectedVersion: input.ExpectedVersion,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return cycle, errStructuredTrainingCycleConflict
		}
	}
	if err != nil {
		return cycle, err
	}
	if cycle.TrainingGroupID != groupID || cycle.SeasonID != seasonID {
		return cycle, errStructuredTrainingCycleScope
	}
	if err = queries.ClearTrainingCycleChildren(ctx, cycle.ID); err != nil {
		return cycle, err
	}
	for _, childID := range input.ChildCycleIDs {
		rows, assignErr := queries.AssignTrainingCycleChild(ctx, dbgen.AssignTrainingCycleChildParams{ParentCycleID: cycle.ID, ChildCycleID: childID})
		if assignErr != nil {
			return cycle, assignErr
		}
		if rows != 1 {
			return cycle, errStructuredTrainingCycleScope
		}
	}
	if err = queries.ClearTrainingCycleWeeks(ctx, cycle.ID); err != nil {
		return cycle, err
	}
	for _, weekID := range input.WeekIDs {
		rows, assignErr := queries.AssignTrainingWeekToCycle(ctx, dbgen.AssignTrainingWeekToCycleParams{CycleID: cycle.ID, PlanID: weekID})
		if assignErr != nil {
			return cycle, assignErr
		}
		if rows != 1 {
			return cycle, errStructuredTrainingCycleScope
		}
	}
	if err = queries.ClearManageableTrainingCycleTargets(ctx, dbgen.ClearManageableTrainingCycleTargetsParams{CycleID: cycle.ID, IsAdmin: input.IsAdmin, UserID: input.ActorID}); err != nil {
		return cycle, err
	}
	seenTargets := make(map[uuid.UUID]bool, len(input.TargetEventIDs))
	for _, eventID := range input.TargetEventIDs {
		if seenTargets[eventID] {
			continue
		}
		seenTargets[eventID] = true
		rows, targetErr := queries.AddTrainingCycleTarget(ctx, dbgen.AddTrainingCycleTargetParams{
			AddedByID: input.ActorID, EventID: eventID, CycleID: cycle.ID,
			IsAdmin: input.IsAdmin, UserID: input.ActorID,
		})
		if targetErr != nil {
			return cycle, targetErr
		}
		if rows != 1 {
			return cycle, errStructuredTrainingCycleScope
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return cycle, err
	}
	return cycle, nil
}

func (s PostgresStructuredTrainingStore) CopyStructuredTrainingCycle(ctx context.Context, input StructuredTrainingCycleCopyInput) (cycle dbgen.TrainingCycle, err error) {
	defer func() { err = trainingScheduleWriteError(err) }()
	tx, queries, err := s.cycleTransaction(ctx, input.ActorID)
	if err != nil {
		return cycle, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = queries.LockTrainingCycles(ctx, []uuid.UUID{input.SourceCycleID}); err != nil {
		return cycle, err
	}
	source, err := queries.GetTrainingCycleCopySource(ctx, input.SourceCycleID)
	if err != nil {
		return cycle, err
	}
	weeks, err := queries.ListTrainingCycleWeekCopySources(ctx, input.SourceCycleID)
	if err != nil || len(weeks) == 0 || !weeks[0].WeekStart.Valid || weeks[0].TrainingGroupID == nil {
		return cycle, errStructuredTrainingCycleScope
	}
	planIDs := make([]uuid.UUID, 0, len(weeks))
	for _, w := range weeks {
		planIDs = append(planIDs, w.ID)
	}
	if err = guardTrainingSchedulePlans(ctx, queries, input.ActorID, planIDs...); err != nil {
		return cycle, err
	}
	allSnapshots := make(map[uuid.UUID][]dbgen.ListStructuredSessionSnapshotsForPlanRow, len(weeks))
	for _, w := range weeks {
		rows, readErr := queries.ListStructuredSessionSnapshotsForPlan(ctx, w.ID)
		if readErr != nil {
			return cycle, readErr
		}
		for _, row := range rows {
			if err = unsupportedTrainingArtifact(row.Snapshot); err != nil {
				return cycle, err
			}
		}
		allSnapshots[w.ID] = rows
	}
	sourceStart := weeks[0].WeekStart.Time
	sourceStartUTC := time.Date(sourceStart.Year(), sourceStart.Month(), sourceStart.Day(), 0, 0, 0, 0, time.UTC)
	targetStartUTC := time.Date(input.FirstMonday.Year(), input.FirstMonday.Month(), input.FirstMonday.Day(), 0, 0, 0, 0, time.UTC)
	shiftDays := int(targetStartUTC.Sub(sourceStartUTC).Hours() / 24)
	createdPlans := make([]dbgen.TrainingPlan, 0, len(weeks))
	for _, sourceWeek := range weeks {
		if !sourceWeek.WeekStart.Valid || sourceWeek.TrainingGroupID == nil {
			return cycle, errStructuredTrainingCycleScope
		}
		sourceWeekUTC := time.Date(sourceWeek.WeekStart.Time.Year(), sourceWeek.WeekStart.Time.Month(), sourceWeek.WeekStart.Time.Day(), 0, 0, 0, 0, time.UTC)
		days := int(sourceWeekUTC.Sub(sourceStartUTC).Hours() / 24)
		targetStart := input.FirstMonday.AddDate(0, 0, days)
		created, createErr := queries.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{
			Title: sourceWeek.Title, Description: sourceWeek.Description,
			WeekStart:             pgtype.Date{Time: targetStart, Valid: true},
			PlannedLoadPercentage: sourceWeek.PlannedLoadPercentage,
			CreatedByID:           input.ActorID, GroupID: *sourceWeek.TrainingGroupID,
		})
		if createErr != nil {
			return cycle, createErr
		}
		snapshots := allSnapshots[sourceWeek.ID]
		for _, row := range snapshots {
			localTarget := row.StartsAt.Time.In(lisbonLocation()).AddDate(0, 0, shiftDays)
			copiedStart, timeErr := trainingCopiedExactStart(row.StartsAt.Time, localTarget.Year(), localTarget.Month(), localTarget.Day())
			if timeErr != nil {
				return cycle, timeErr
			}
			if !row.StartsAt.Valid || !row.EndsAt.Valid {
				return cycle, ErrTrainingScheduleInvalid
			}
			if timeErr = trainingCopiedExactInterval(row.StartsAt.Time, row.EndsAt.Time, copiedStart); timeErr != nil {
				return cycle, timeErr
			}
			destinationID, restoreErr := queries.RestoreTrainingSession(ctx, dbgen.RestoreTrainingSessionParams{
				Snapshot: row.Snapshot, PlanID: created.ID,
				StartsAt:    pgtype.Timestamptz{Time: copiedStart, Valid: true},
				CreatedByID: input.ActorID,
			})
			if restoreErr != nil {
				return cycle, restoreErr
			}
			if err = queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "CYCLE", SourceID: row.ID, SourceUpdatedAt: row.UpdatedAt, DestinationKind: "SESSION", DestinationID: destinationID, CopiedByID: &input.ActorID}); err != nil {
				return cycle, err
			}
		}
		if err = queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "CYCLE", SourceID: sourceWeek.ID, SourceUpdatedAt: sourceWeek.UpdatedAt, DestinationKind: "WEEK", DestinationID: created.ID, CopiedByID: &input.ActorID}); err != nil {
			return cycle, err
		}
		createdPlans = append(createdPlans, created)
	}
	first := createdPlans[0]
	if first.TrainingGroupID == nil || first.SeasonID == nil {
		return cycle, errStructuredTrainingCycleScope
	}
	cycle, err = queries.CreateTrainingCycle(ctx, dbgen.CreateTrainingCycleParams{
		TrainingGroupID: *first.TrainingGroupID, SeasonID: *first.SeasonID, Name: input.Name,
		LevelLabel: source.LevelLabel, Goals: source.Goals, PhaseFocusNotes: source.PhaseFocusNotes,
		CreatedByID: input.ActorID,
	})
	if err != nil {
		return cycle, err
	}
	for _, plan := range createdPlans {
		rows, assignErr := queries.AssignTrainingWeekToCycle(ctx, dbgen.AssignTrainingWeekToCycleParams{CycleID: cycle.ID, PlanID: plan.ID})
		if assignErr != nil || rows != 1 {
			return cycle, errStructuredTrainingCycleScope
		}
	}
	if err = queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "CYCLE", SourceID: source.ID, SourceUpdatedAt: source.UpdatedAt, DestinationKind: "CYCLE", DestinationID: cycle.ID, CopiedByID: &input.ActorID}); err != nil {
		return cycle, err
	}
	if err = tx.Commit(ctx); err != nil {
		return cycle, err
	}
	return cycle, nil
}

func (s PostgresStructuredTrainingStore) CreateStructuredTrainingSession(ctx context.Context, params dbgen.CreateStructuredTrainingSessionParams) (dbgen.TrainingSession, error) {
	return withTrainingScheduleTarget(ctx, s, params.CreatedByID, "PLAN", params.PlanID, func(q *dbgen.Queries) (dbgen.TrainingSession, error) {
		return q.CreateStructuredTrainingSession(ctx, params)
	})
}

func (s PostgresStructuredTrainingStore) CreateSegment(ctx context.Context, input StructuredTrainingSegmentInput) (segmentID uuid.UUID, err error) {
	defer func() { err = trainingScheduleWriteError(err) }()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockTrainingGroupActor(ctx, tx, trainingWriteActor(ctx)); err != nil {
		return uuid.Nil, trainingScopeWriteError(err)
	}
	queries := dbgen.New(tx)
	if _, err = lockTrainingScheduleTarget(ctx, queries, trainingWriteActor(ctx), "SESSION", input.Segment.SessionID); err != nil {
		return uuid.Nil, err
	}
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
	return withTrainingScheduleTarget(ctx, s, trainingWriteActor(ctx), "SEGMENT", params.SegmentID, func(q *dbgen.Queries) (uuid.UUID, error) { return q.CreateTrainingSegmentBlock(ctx, params) })
}

func (s PostgresStructuredTrainingStore) CreateGymBlock(ctx context.Context, input StructuredGymBlockInput) (blockID uuid.UUID, err error) {
	defer func() { err = trainingScheduleWriteError(err) }()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockTrainingGroupActor(ctx, tx, trainingWriteActor(ctx)); err != nil {
		return uuid.Nil, trainingScopeWriteError(err)
	}
	queries := dbgen.New(tx)
	if _, err = lockTrainingScheduleTarget(ctx, queries, trainingWriteActor(ctx), "SEGMENT", input.Block.SegmentID); err != nil {
		return uuid.Nil, err
	}
	blockID, err = queries.CreateTrainingSegmentBlock(ctx, input.Block)
	if err != nil {
		return uuid.Nil, err
	}
	input.Prescription.BlockID = blockID
	rows, err := queries.CreateGymBlockPrescription(ctx, input.Prescription)
	if err != nil {
		return uuid.Nil, err
	}
	if rows != 1 {
		return uuid.Nil, pgx.ErrNoRows
	}
	input.Exercise.BlockID = blockID
	if _, err := queries.CreateGymExercise(ctx, input.Exercise); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return blockID, nil
}

func (s PostgresStructuredTrainingStore) CreateGymExercise(ctx context.Context, params dbgen.CreateGymExerciseParams) (uuid.UUID, error) {
	return withTrainingScheduleTarget(ctx, s, trainingWriteActor(ctx), "BLOCK", params.BlockID, func(q *dbgen.Queries) (uuid.UUID, error) { return q.CreateGymExercise(ctx, params) })
}

func (s PostgresStructuredTrainingStore) CreateWaterBlock(ctx context.Context, input StructuredWaterBlockInput) (blockID uuid.UUID, err error) {
	defer func() { err = trainingScheduleWriteError(err) }()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockTrainingGroupActor(ctx, tx, trainingWriteActor(ctx)); err != nil {
		return uuid.Nil, trainingScopeWriteError(err)
	}
	queries := dbgen.New(tx)
	if _, err = lockTrainingScheduleTarget(ctx, queries, trainingWriteActor(ctx), "SEGMENT", input.Block.SegmentID); err != nil {
		return uuid.Nil, err
	}
	blockID, err = queries.CreateTrainingSegmentBlock(ctx, input.Block)
	if err != nil {
		return uuid.Nil, err
	}
	input.Prescription.BlockID = blockID
	rows, err := queries.CreateWaterBlockPrescription(ctx, input.Prescription)
	if err != nil {
		return uuid.Nil, err
	}
	if rows != 1 {
		return uuid.Nil, pgx.ErrNoRows
	}
	input.Step.BlockID = blockID
	if _, err := queries.CreateWaterWorkStep(ctx, input.Step); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return blockID, nil
}

func (s PostgresStructuredTrainingStore) CreateWaterWorkStep(ctx context.Context, params dbgen.CreateWaterWorkStepParams) (uuid.UUID, error) {
	return withTrainingScheduleTarget(ctx, s, trainingWriteActor(ctx), "BLOCK", params.BlockID, func(q *dbgen.Queries) (uuid.UUID, error) { return q.CreateWaterWorkStep(ctx, params) })
}

func (s PostgresStructuredTrainingStore) CreateWaterIntensityProfile(ctx context.Context, params dbgen.CreateWaterIntensityProfileParams) (dbgen.CreateWaterIntensityProfileRow, error) {
	return s.queries().CreateWaterIntensityProfile(ctx, params)
}

func (s PostgresStructuredTrainingStore) CreateWaterIntensityZone(ctx context.Context, params dbgen.CreateWaterIntensityZoneParams) (uuid.UUID, error) {
	return s.queries().CreateWaterIntensityZone(ctx, params)
}

func (s PostgresStructuredTrainingStore) ListActiveWaterIntensityProfiles(ctx context.Context) ([]dbgen.ListActiveWaterIntensityProfilesRow, error) {
	return s.queries().ListActiveWaterIntensityProfiles(ctx)
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

func (s PostgresStructuredTrainingStore) GetGymExercisePlanID(ctx context.Context, exerciseID uuid.UUID) (uuid.UUID, error) {
	return s.queries().GetGymExercisePlanID(ctx, exerciseID)
}

func (s PostgresStructuredTrainingStore) MoveTrainingSessionSegment(ctx context.Context, params dbgen.MoveTrainingSessionSegmentParams) (bool, error) {
	return withTrainingScheduleTarget(ctx, s, trainingWriteActor(ctx), "SEGMENT", params.SegmentID, func(q *dbgen.Queries) (bool, error) { return q.MoveTrainingSessionSegment(ctx, params) })
}

func (s PostgresStructuredTrainingStore) MoveTrainingSegmentBlock(ctx context.Context, params dbgen.MoveTrainingSegmentBlockParams) (bool, error) {
	return withTrainingScheduleTarget(ctx, s, trainingWriteActor(ctx), "BLOCK", params.BlockID, func(q *dbgen.Queries) (bool, error) { return q.MoveTrainingSegmentBlock(ctx, params) })
}

func (s PostgresStructuredTrainingStore) MoveGymExercise(ctx context.Context, params dbgen.MoveGymExerciseParams) (bool, error) {
	return withTrainingScheduleTarget(ctx, s, trainingWriteActor(ctx), "EXERCISE", params.ExerciseID, func(q *dbgen.Queries) (bool, error) { return q.MoveGymExercise(ctx, params) })
}

func (s PostgresStructuredTrainingStore) GetRoutineSource(ctx context.Context, kind dbgen.TrainingRoutineKind, sourceID uuid.UUID) (StructuredRoutineSource, error) {
	return getTrainingRoutineSource(ctx, s.queries(), kind, sourceID)
}
func getTrainingRoutineSource(ctx context.Context, q *dbgen.Queries, kind dbgen.TrainingRoutineKind, sourceID uuid.UUID) (StructuredRoutineSource, error) {
	switch kind {
	case dbgen.TrainingRoutineKindBLOCK:
		row, err := q.GetBlockRoutineSource(ctx, sourceID)
		modality := row.Modality
		return StructuredRoutineSource{PlanID: row.PlanID, SourceUpdatedAt: row.SourceUpdatedAt, ProgrammeID: row.ProgrammeID, TeamID: row.TeamID, Modality: &modality, Objective: row.Objective, Snapshot: row.Snapshot}, err
	case dbgen.TrainingRoutineKindSEGMENT:
		row, err := q.GetSegmentRoutineSource(ctx, sourceID)
		modality := row.Modality
		return StructuredRoutineSource{PlanID: row.PlanID, SourceUpdatedAt: row.SourceUpdatedAt, ProgrammeID: row.ProgrammeID, TeamID: row.TeamID, Modality: &modality, Objective: row.Objective, Snapshot: row.Snapshot}, err
	case dbgen.TrainingRoutineKindSESSION:
		row, err := q.GetSessionRoutineSource(ctx, sourceID)
		return StructuredRoutineSource{PlanID: row.PlanID, SourceUpdatedAt: row.SourceUpdatedAt, ProgrammeID: row.ProgrammeID, TeamID: row.TeamID, Modality: row.Modality, Objective: row.Objective, Snapshot: row.Snapshot}, err
	default:
		return StructuredRoutineSource{}, pgx.ErrNoRows
	}
}

func (s PostgresStructuredTrainingStore) CreateTrainingRoutine(ctx context.Context, params dbgen.CreateTrainingRoutineParams) (dbgen.TrainingRoutine, error) {
	return withTrainingScheduleTarget(ctx, s, params.OwnerUserID, string(params.Kind), params.SourceID, func(q *dbgen.Queries) (dbgen.TrainingRoutine, error) {
		if e := unsupportedTrainingArtifact(params.Snapshot); e != nil {
			return dbgen.TrainingRoutine{}, e
		}
		fresh, e := getTrainingRoutineSource(ctx, q, params.Kind, params.SourceID)
		if e != nil {
			return dbgen.TrainingRoutine{}, e
		}
		if e = guardTrainingSchedulePlans(ctx, q, params.OwnerUserID, fresh.PlanID); e != nil {
			return dbgen.TrainingRoutine{}, e
		}
		if !fresh.SourceUpdatedAt.Time.Equal(params.SourceUpdatedAt.Time) || !bytes.Equal(fresh.Snapshot, params.Snapshot) {
			return dbgen.TrainingRoutine{}, errStructuredTrainingPublicationConflict
		}
		return q.CreateTrainingRoutine(ctx, params)
	})
}

func (s PostgresStructuredTrainingStore) ListVisibleTrainingRoutines(ctx context.Context, params dbgen.ListVisibleTrainingRoutinesParams) ([]dbgen.ListVisibleTrainingRoutinesRow, error) {
	return s.queries().ListVisibleTrainingRoutines(ctx, params)
}

func (s PostgresStructuredTrainingStore) GetVisibleTrainingRoutine(ctx context.Context, params dbgen.GetVisibleTrainingRoutineParams) (dbgen.TrainingRoutine, error) {
	return s.queries().GetVisibleTrainingRoutine(ctx, params)
}

func (s PostgresStructuredTrainingStore) InsertTrainingRoutine(ctx context.Context, input StructuredRoutineInsertInput) (destinationID uuid.UUID, err error) {
	if !validTrainingRoutineKind(input.Routine.Kind) || (input.Routine.Kind == dbgen.TrainingRoutineKindSESSION && !input.StartsAt.Valid) {
		return uuid.Nil, pgx.ErrNoRows
	}
	defer func() { err = trainingScheduleWriteError(err) }()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockTrainingGroupActor(ctx, tx, input.ActorID); err != nil {
		return uuid.Nil, trainingScopeWriteError(err)
	}
	queries := dbgen.New(tx)
	current, readErr := currentTrainingRoutineV1(ctx, queries, input)
	if readErr != nil {
		return uuid.Nil, readErr
	}
	input.Routine = current
	destinationKind := string(input.Routine.Kind)
	switch input.Routine.Kind {
	case dbgen.TrainingRoutineKindBLOCK:
		if _, err = lockTrainingScheduleTarget(ctx, queries, input.ActorID, "SEGMENT", input.TargetID); err != nil {
			return uuid.Nil, err
		}
		destinationID, err = queries.RestoreTrainingBlock(ctx, dbgen.RestoreTrainingBlockParams{Snapshot: input.Routine.Snapshot, SegmentID: input.TargetID})
	case dbgen.TrainingRoutineKindSEGMENT:
		if _, err = lockTrainingScheduleTarget(ctx, queries, input.ActorID, "SESSION", input.TargetID); err != nil {
			return uuid.Nil, err
		}
		destinationID, err = queries.RestoreTrainingSegment(ctx, dbgen.RestoreTrainingSegmentParams{Snapshot: input.Routine.Snapshot, SessionID: input.TargetID})
	case dbgen.TrainingRoutineKindSESSION:
		if err = guardTrainingSchedulePlans(ctx, queries, input.ActorID, input.TargetID); err != nil {
			return uuid.Nil, err
		}
		if !input.StartsAt.Valid {
			return uuid.Nil, pgx.ErrNoRows
		}
		destinationID, err = queries.RestoreTrainingSession(ctx, dbgen.RestoreTrainingSessionParams{Snapshot: input.Routine.Snapshot, PlanID: input.TargetID, StartsAt: input.StartsAt, CreatedByID: input.ActorID})
	default:
		return uuid.Nil, pgx.ErrNoRows
	}
	if err != nil {
		return uuid.Nil, err
	}
	if err := queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "ROUTINE", SourceID: input.Routine.ID, SourceUpdatedAt: input.Routine.UpdatedAt, DestinationKind: destinationKind, DestinationID: destinationID, CopiedByID: &input.ActorID}); err != nil {
		return uuid.Nil, err
	}
	return destinationID, tx.Commit(ctx)
}

func (s PostgresStructuredTrainingStore) CopyTrainingBlock(ctx context.Context, sourceID, targetSegmentID, actorID uuid.UUID) (destinationID uuid.UUID, err error) {
	defer func() { err = trainingScheduleWriteError(err) }()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockTrainingGroupActor(ctx, tx, actorID); err != nil {
		return uuid.Nil, trainingScopeWriteError(err)
	}
	queries := dbgen.New(tx)
	targetPlan, readErr := queries.GetStructuredSegmentPlanID(ctx, targetSegmentID)
	if readErr != nil {
		return uuid.Nil, readErr
	}
	sourcePlan, readErr := queries.GetStructuredBlockPlanID(ctx, sourceID)
	if readErr != nil {
		return uuid.Nil, readErr
	}
	if err = guardTrainingSchedulePlans(ctx, queries, actorID, sourcePlan, targetPlan); err != nil {
		return uuid.Nil, err
	}
	if _, err = lockTrainingScheduleTarget(ctx, queries, actorID, "SEGMENT", targetSegmentID); err != nil {
		return uuid.Nil, err
	}
	if _, err = lockTrainingScheduleTarget(ctx, queries, actorID, "BLOCK", sourceID); err != nil {
		return uuid.Nil, err
	}
	source, err := queries.GetBlockRoutineSource(ctx, sourceID)
	if err != nil {
		return uuid.Nil, err
	}
	if err = unsupportedTrainingArtifact(source.Snapshot); err != nil {
		return uuid.Nil, err
	}
	destinationID, err = queries.RestoreTrainingBlock(ctx, dbgen.RestoreTrainingBlockParams{Snapshot: source.Snapshot, SegmentID: targetSegmentID})
	if err != nil {
		return uuid.Nil, err
	}
	if err := queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "BLOCK", SourceID: sourceID, SourceUpdatedAt: source.SourceUpdatedAt, DestinationKind: "BLOCK", DestinationID: destinationID, CopiedByID: &actorID}); err != nil {
		return uuid.Nil, err
	}
	return destinationID, tx.Commit(ctx)
}

func (s PostgresStructuredTrainingStore) CopyTrainingSession(ctx context.Context, sourceID, targetPlanID uuid.UUID, startsAt pgtype.Timestamptz, actorID uuid.UUID) (destinationID uuid.UUID, err error) {
	defer func() { err = trainingScheduleWriteError(err) }()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockTrainingGroupActor(ctx, tx, actorID); err != nil {
		return uuid.Nil, trainingScopeWriteError(err)
	}
	queries := dbgen.New(tx)
	sourcePlan, readErr := queries.GetStructuredSessionPlanID(ctx, sourceID)
	if readErr != nil {
		return uuid.Nil, readErr
	}
	if err = guardTrainingSchedulePlans(ctx, queries, actorID, sourcePlan, targetPlanID); err != nil {
		return uuid.Nil, err
	}
	if _, err = lockTrainingScheduleTarget(ctx, queries, actorID, "SESSION", sourceID); err != nil {
		return uuid.Nil, err
	}
	source, err := queries.GetSessionRoutineSource(ctx, sourceID)
	if err != nil {
		return uuid.Nil, err
	}
	if err = unsupportedTrainingArtifact(source.Snapshot); err != nil {
		return uuid.Nil, err
	}
	if !source.StartsAt.Valid || !source.EndsAt.Valid || !startsAt.Valid {
		return uuid.Nil, ErrTrainingScheduleInvalid
	}
	if err = trainingCopiedExactInterval(source.StartsAt.Time, source.EndsAt.Time, startsAt.Time); err != nil {
		return uuid.Nil, err
	}
	destinationID, err = queries.RestoreTrainingSession(ctx, dbgen.RestoreTrainingSessionParams{Snapshot: source.Snapshot, PlanID: targetPlanID, StartsAt: startsAt, CreatedByID: actorID})
	if err != nil {
		return uuid.Nil, err
	}
	if err := queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "SESSION", SourceID: sourceID, SourceUpdatedAt: source.SourceUpdatedAt, DestinationKind: "SESSION", DestinationID: destinationID, CopiedByID: &actorID}); err != nil {
		return uuid.Nil, err
	}
	return destinationID, tx.Commit(ctx)
}

func (s PostgresStructuredTrainingStore) CopyStructuredTrainingDay(ctx context.Context, input StructuredDayCopyInput) (copied int, err error) {
	defer func() {
		if err != nil {
			copied = 0
		}
	}()
	defer func() { err = trainingScheduleWriteError(err) }()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockTrainingGroupActor(ctx, tx, input.ActorID); err != nil {
		return 0, trainingScopeWriteError(err)
	}
	queries := dbgen.New(tx)
	if err = guardTrainingSchedulePlans(ctx, queries, input.ActorID, input.SourcePlanID, input.TargetPlanID); err != nil {
		return 0, err
	}
	rows, err := queries.ListStructuredSessionSnapshotsForDay(ctx, dbgen.ListStructuredSessionSnapshotsForDayParams{PlanID: input.SourcePlanID, SourceDate: pgtype.Date{Time: input.SourceDate, Valid: true}})
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if err = unsupportedTrainingArtifact(row.Snapshot); err != nil {
			return 0, err
		}
	}
	for _, row := range rows {
		startsAt, timeErr := trainingCopiedExactStart(row.StartsAt.Time, input.TargetDate.Year(), input.TargetDate.Month(), input.TargetDate.Day())
		if timeErr != nil {
			return 0, timeErr
		}
		if !row.StartsAt.Valid || !row.EndsAt.Valid {
			return 0, ErrTrainingScheduleInvalid
		}
		if timeErr = trainingCopiedExactInterval(row.StartsAt.Time, row.EndsAt.Time, startsAt); timeErr != nil {
			return 0, timeErr
		}
		destinationID, err := queries.RestoreTrainingSession(ctx, dbgen.RestoreTrainingSessionParams{Snapshot: row.Snapshot, PlanID: input.TargetPlanID, StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, CreatedByID: input.ActorID})
		if err != nil {
			return copied, err
		}
		if err := queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "DAY", SourceID: row.ID, SourceUpdatedAt: row.UpdatedAt, DestinationKind: "SESSION", DestinationID: destinationID, CopiedByID: &input.ActorID}); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, tx.Commit(ctx)
}

func (s PostgresStructuredTrainingStore) CopyStructuredTrainingWeek(ctx context.Context, input StructuredWeekCopyInput) (plan dbgen.TrainingPlan, err error) {
	defer func() { err = trainingScheduleWriteError(err) }()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return plan, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockTrainingGroupActor(ctx, tx, input.ActorID); err != nil {
		return plan, trainingScopeWriteError(err)
	}
	queries := dbgen.New(tx)
	if err = guardTrainingSchedulePlans(ctx, queries, input.ActorID, input.SourcePlanID); err != nil {
		return plan, err
	}
	rows, readErr := queries.ListStructuredSessionSnapshotsForPlan(ctx, input.SourcePlanID)
	if readErr != nil {
		return plan, readErr
	}
	for _, row := range rows {
		if err = unsupportedTrainingArtifact(row.Snapshot); err != nil {
			return plan, err
		}
	}
	source, err := queries.GetStructuredPlanCopySource(ctx, input.SourcePlanID)
	if err != nil || source.TrainingGroupID == nil || !source.WeekStart.Valid {
		return plan, pgx.ErrNoRows
	}
	description := input.Description
	if description == "" {
		description = source.Description
	}
	plan, err = queries.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{Title: input.Title, Description: description, WeekStart: pgtype.Date{Time: input.WeekStart, Valid: true}, PlannedLoadPercentage: source.PlannedLoadPercentage, CreatedByID: input.ActorID, GroupID: *source.TrainingGroupID})
	if err != nil {
		return plan, err
	}
	sourceDate := time.Date(source.WeekStart.Time.Year(), source.WeekStart.Time.Month(), source.WeekStart.Time.Day(), 0, 0, 0, 0, time.UTC)
	targetDate := time.Date(input.WeekStart.Year(), input.WeekStart.Month(), input.WeekStart.Day(), 0, 0, 0, 0, time.UTC)
	days := int(targetDate.Sub(sourceDate).Hours() / 24)
	for _, row := range rows {
		localTarget := row.StartsAt.Time.In(lisbonLocation()).AddDate(0, 0, days)
		copiedStart, timeErr := trainingCopiedExactStart(row.StartsAt.Time, localTarget.Year(), localTarget.Month(), localTarget.Day())
		if timeErr != nil {
			return plan, timeErr
		}
		if !row.StartsAt.Valid || !row.EndsAt.Valid {
			return plan, ErrTrainingScheduleInvalid
		}
		if timeErr = trainingCopiedExactInterval(row.StartsAt.Time, row.EndsAt.Time, copiedStart); timeErr != nil {
			return plan, timeErr
		}
		destinationID, err := queries.RestoreTrainingSession(ctx, dbgen.RestoreTrainingSessionParams{Snapshot: row.Snapshot, PlanID: plan.ID, StartsAt: pgtype.Timestamptz{Time: copiedStart, Valid: true}, CreatedByID: input.ActorID})
		if err != nil {
			return plan, err
		}
		if err := queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "WEEK", SourceID: row.ID, SourceUpdatedAt: row.UpdatedAt, DestinationKind: "SESSION", DestinationID: destinationID, CopiedByID: &input.ActorID}); err != nil {
			return plan, err
		}
	}
	if err := queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "WEEK", SourceID: input.SourcePlanID, SourceUpdatedAt: source.UpdatedAt, DestinationKind: "WEEK", DestinationID: plan.ID, CopiedByID: &input.ActorID}); err != nil {
		return plan, err
	}
	return plan, tx.Commit(ctx)
}

func (s PostgresStructuredTrainingStore) GetStructuredPlanCopySource(ctx context.Context, planID uuid.UUID) (dbgen.GetStructuredPlanCopySourceRow, error) {
	return s.queries().GetStructuredPlanCopySource(ctx, planID)
}

func (s PostgresStructuredTrainingStore) ListManagedTrainingGroupMembers(ctx context.Context, params dbgen.ListManagedTrainingGroupMembersParams) ([]dbgen.ListManagedTrainingGroupMembersRow, error) {
	return s.queries().ListManagedTrainingGroupMembers(ctx, params)
}

func (s PostgresStructuredTrainingStore) ListStructuredCrewModalities(ctx context.Context) ([]dbgen.CanoeCraftClass, error) {
	return s.queries().ListStructuredCrewModalities(ctx)
}

func (s PostgresStructuredTrainingStore) ListManagedStructuredCompetitionEvents(ctx context.Context, params dbgen.ListManagedStructuredCompetitionEventsParams) ([]dbgen.ListManagedStructuredCompetitionEventsRow, error) {
	return s.queries().ListManagedStructuredCompetitionEvents(ctx, params)
}

func (s PostgresStructuredTrainingStore) CreateTrainingVariationGroup(ctx context.Context, input StructuredVariationGroupInput) (dbgen.TrainingVariationGroup, error) {
	return s.createDatedVariationGroup(ctx, input)
}

func (s PostgresStructuredTrainingStore) ListManagedTrainingVariationGroups(ctx context.Context, params dbgen.ListManagedTrainingVariationGroupsParams) ([]dbgen.ListManagedTrainingVariationGroupsRow, error) {
	return s.queries().ListManagedTrainingVariationGroups(ctx, params)
}

func (s PostgresStructuredTrainingStore) CreateTrainingVariation(ctx context.Context, params dbgen.CreateTrainingVariationParams) (dbgen.TrainingVariation, error) {
	return s.createModernTrainingVariation(ctx, params)
}

func (s PostgresStructuredTrainingStore) ListTrainingVariationMatchesForManager(ctx context.Context, params dbgen.ListTrainingVariationMatchesForManagerParams) ([]dbgen.ListTrainingVariationMatchesForManagerRow, error) {
	return s.queries().ListTrainingVariationMatchesForManager(ctx, params)
}

func (s PostgresStructuredTrainingStore) GetTrainingVariationPlanID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.queries().GetTrainingVariationPlanID(ctx, id)
}

func (s PostgresStructuredTrainingStore) RetireTrainingVariation(ctx context.Context, params dbgen.RetireTrainingVariationParams) (int64, error) {
	return s.retireModernTrainingVariation(ctx, params)
}
