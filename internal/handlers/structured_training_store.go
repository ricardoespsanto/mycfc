package handlers

import (
	"context"
	"errors"
	"sort"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	SessionID      uuid.UUID
	MembershipID   uuid.UUID
	AthleteUserID  uuid.UUID
	Snapshot       []byte
	SnapshotSHA256 string
}

type StructuredPublicationInput struct {
	PlanID          uuid.UUID
	SourceUpdatedAt pgtype.Timestamptz
	ChangeSummary   string
	PublishedByID   uuid.UUID
	Prescriptions   []StructuredPrescriptionInput
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
	ListStructuredCrewModalities(context.Context) ([]dbgen.ListStructuredCrewModalitiesRow, error)
	ListManagedStructuredCompetitionEvents(context.Context, dbgen.ListManagedStructuredCompetitionEventsParams) ([]dbgen.ListManagedStructuredCompetitionEventsRow, error)
	CreateTrainingVariationGroup(context.Context, StructuredVariationGroupInput) (dbgen.TrainingVariationGroup, error)
	ListManagedTrainingVariationGroups(context.Context, dbgen.ListManagedTrainingVariationGroupsParams) ([]dbgen.ListManagedTrainingVariationGroupsRow, error)
	CreateTrainingVariation(context.Context, dbgen.CreateTrainingVariationParams) (dbgen.TrainingVariation, error)
	ListTrainingVariationMatchesForManager(context.Context, dbgen.ListTrainingVariationMatchesForManagerParams) ([]dbgen.ListTrainingVariationMatchesForManagerRow, error)
	GetTrainingVariationPlanID(context.Context, uuid.UUID) (uuid.UUID, error)
	RetireTrainingVariation(context.Context, dbgen.RetireTrainingVariationParams) (int64, error)
	ListManagedTrainingPublicationStates(context.Context, dbgen.ListManagedTrainingPublicationStatesParams) ([]dbgen.ListManagedTrainingPublicationStatesRow, error)
	ListStructuredTrainingPublicationMembers(context.Context, dbgen.ListStructuredTrainingPublicationMembersParams) ([]dbgen.ListStructuredTrainingPublicationMembersRow, error)
	PublishStructuredTrainingPlan(context.Context, StructuredPublicationInput) (dbgen.TrainingPlanPublication, error)
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

func (s PostgresStructuredTrainingStore) cycleTransaction(ctx context.Context) (structuredTrainingCycleTx, structuredTrainingCycleQueries, error) {
	if s.beginCycleTx != nil {
		return s.beginCycleTx(ctx)
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
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

func (s PostgresStructuredTrainingStore) ListStructuredTrainingPublicationMembers(ctx context.Context, params dbgen.ListStructuredTrainingPublicationMembersParams) ([]dbgen.ListStructuredTrainingPublicationMembersRow, error) {
	return s.queries().ListStructuredTrainingPublicationMembers(ctx, params)
}

func (s PostgresStructuredTrainingStore) ListTrainingPrescriptionsForViewer(ctx context.Context, params dbgen.ListTrainingPrescriptionsForViewerParams) ([]dbgen.ListTrainingPrescriptionsForViewerRow, error) {
	return s.queries().ListTrainingPrescriptionsForViewer(ctx, params)
}

func (s PostgresStructuredTrainingStore) ListLatestTrainingPrescriptionHashesForPlan(ctx context.Context, planID uuid.UUID) ([]dbgen.ListLatestTrainingPrescriptionHashesForPlanRow, error) {
	return s.queries().ListLatestTrainingPrescriptionHashesForPlan(ctx, planID)
}

func (s PostgresStructuredTrainingStore) GetTrainingPrescriptionForViewer(ctx context.Context, params dbgen.GetTrainingPrescriptionForViewerParams) (dbgen.GetTrainingPrescriptionForViewerRow, error) {
	return s.queries().GetTrainingPrescriptionForViewer(ctx, params)
}

func (s PostgresStructuredTrainingStore) ListTrainingPrescriptionLinksForSessionViewer(ctx context.Context, params dbgen.ListTrainingPrescriptionLinksForSessionViewerParams) ([]dbgen.ListTrainingPrescriptionLinksForSessionViewerRow, error) {
	return s.queries().ListTrainingPrescriptionLinksForSessionViewer(ctx, params)
}

func (s PostgresStructuredTrainingStore) PublishStructuredTrainingPlan(ctx context.Context, input StructuredPublicationInput) (publication dbgen.TrainingPlanPublication, err error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return publication, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := dbgen.New(tx)
	state, err := queries.LockStructuredTrainingPlanForPublication(ctx, input.PlanID)
	if err != nil {
		return publication, err
	}
	if !state.UpdatedAt.Valid || !input.SourceUpdatedAt.Valid || !state.UpdatedAt.Time.Equal(input.SourceUpdatedAt.Time) {
		return publication, errStructuredTrainingPublicationConflict
	}
	var supersedesID *uuid.UUID
	if state.LatestPublicationID != uuid.Nil {
		supersedesID = &state.LatestPublicationID
	}
	publication, err = queries.CreateTrainingPlanPublication(ctx, dbgen.CreateTrainingPlanPublicationParams{
		PlanID: input.PlanID, Revision: state.LatestRevision + 1, SourceUpdatedAt: state.UpdatedAt,
		ChangeSummary: input.ChangeSummary, SupersedesID: supersedesID, PublishedByID: input.PublishedByID,
	})
	if err != nil {
		return publication, err
	}
	for _, prescription := range input.Prescriptions {
		if _, err = queries.CreateTrainingPrescription(ctx, dbgen.CreateTrainingPrescriptionParams{
			PublicationID: publication.ID, SessionID: prescription.SessionID, MembershipID: prescription.MembershipID,
			AthleteUserID: prescription.AthleteUserID, Snapshot: prescription.Snapshot, SnapshotSha256: prescription.SnapshotSHA256,
		}); err != nil {
			return publication, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return publication, err
	}
	return publication, nil
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

func (s PostgresStructuredTrainingStore) UpdateStructuredTrainingWeekLoad(ctx context.Context, params dbgen.UpdateStructuredTrainingWeekLoadParams) (int64, error) {
	return s.queries().UpdateStructuredTrainingWeekLoad(ctx, params)
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
	if len(input.WeekIDs) == 0 && len(input.ChildCycleIDs) == 0 {
		return cycle, errStructuredTrainingCycleScope
	}
	tx, queries, err := s.cycleTransaction(ctx)
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
	tx, queries, err := s.cycleTransaction(ctx)
	if err != nil {
		return cycle, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	source, err := queries.GetTrainingCycleCopySource(ctx, input.SourceCycleID)
	if err != nil {
		return cycle, err
	}
	weeks, err := queries.ListTrainingCycleWeekCopySources(ctx, input.SourceCycleID)
	if err != nil || len(weeks) == 0 || !weeks[0].WeekStart.Valid || weeks[0].TrainingGroupID == nil {
		return cycle, errStructuredTrainingCycleScope
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
		snapshots, snapshotErr := queries.ListStructuredSessionSnapshotsForPlan(ctx, sourceWeek.ID)
		if snapshotErr != nil {
			return cycle, snapshotErr
		}
		for _, row := range snapshots {
			destinationID, restoreErr := queries.RestoreTrainingSession(ctx, dbgen.RestoreTrainingSessionParams{
				Snapshot: row.Snapshot, PlanID: created.ID,
				StartsAt:    pgtype.Timestamptz{Time: row.StartsAt.Time.AddDate(0, 0, shiftDays), Valid: true},
				CreatedByID: input.ActorID,
			})
			if restoreErr != nil {
				return cycle, restoreErr
			}
			if err = queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "CYCLE", SourceID: row.ID, SourceUpdatedAt: row.UpdatedAt, DestinationKind: "SESSION", DestinationID: destinationID, CopiedByID: input.ActorID}); err != nil {
				return cycle, err
			}
		}
		if err = queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "CYCLE", SourceID: sourceWeek.ID, SourceUpdatedAt: sourceWeek.UpdatedAt, DestinationKind: "WEEK", DestinationID: created.ID, CopiedByID: input.ActorID}); err != nil {
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
	if err = queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "CYCLE", SourceID: source.ID, SourceUpdatedAt: source.UpdatedAt, DestinationKind: "CYCLE", DestinationID: cycle.ID, CopiedByID: input.ActorID}); err != nil {
		return cycle, err
	}
	if err = tx.Commit(ctx); err != nil {
		return cycle, err
	}
	return cycle, nil
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

func (s PostgresStructuredTrainingStore) CreateGymBlock(ctx context.Context, input StructuredGymBlockInput) (blockID uuid.UUID, err error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := dbgen.New(tx)
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
	return s.queries().CreateGymExercise(ctx, params)
}

func (s PostgresStructuredTrainingStore) CreateWaterBlock(ctx context.Context, input StructuredWaterBlockInput) (blockID uuid.UUID, err error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := dbgen.New(tx)
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
	return s.queries().CreateWaterWorkStep(ctx, params)
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
	return s.queries().MoveTrainingSessionSegment(ctx, params)
}

func (s PostgresStructuredTrainingStore) MoveTrainingSegmentBlock(ctx context.Context, params dbgen.MoveTrainingSegmentBlockParams) (bool, error) {
	return s.queries().MoveTrainingSegmentBlock(ctx, params)
}

func (s PostgresStructuredTrainingStore) MoveGymExercise(ctx context.Context, params dbgen.MoveGymExerciseParams) (bool, error) {
	return s.queries().MoveGymExercise(ctx, params)
}

func (s PostgresStructuredTrainingStore) GetRoutineSource(ctx context.Context, kind dbgen.TrainingRoutineKind, sourceID uuid.UUID) (StructuredRoutineSource, error) {
	switch kind {
	case dbgen.TrainingRoutineKindBLOCK:
		row, err := s.queries().GetBlockRoutineSource(ctx, sourceID)
		modality := row.Modality
		return StructuredRoutineSource{PlanID: row.PlanID, SourceUpdatedAt: row.SourceUpdatedAt, ProgrammeID: row.ProgrammeID, TeamID: row.TeamID, Modality: &modality, Objective: row.Objective, Snapshot: row.Snapshot}, err
	case dbgen.TrainingRoutineKindSEGMENT:
		row, err := s.queries().GetSegmentRoutineSource(ctx, sourceID)
		modality := row.Modality
		return StructuredRoutineSource{PlanID: row.PlanID, SourceUpdatedAt: row.SourceUpdatedAt, ProgrammeID: row.ProgrammeID, TeamID: row.TeamID, Modality: &modality, Objective: row.Objective, Snapshot: row.Snapshot}, err
	case dbgen.TrainingRoutineKindSESSION:
		row, err := s.queries().GetSessionRoutineSource(ctx, sourceID)
		return StructuredRoutineSource{PlanID: row.PlanID, SourceUpdatedAt: row.SourceUpdatedAt, ProgrammeID: row.ProgrammeID, TeamID: row.TeamID, Modality: row.Modality, Objective: row.Objective, Snapshot: row.Snapshot}, err
	default:
		return StructuredRoutineSource{}, pgx.ErrNoRows
	}
}

func (s PostgresStructuredTrainingStore) CreateTrainingRoutine(ctx context.Context, params dbgen.CreateTrainingRoutineParams) (dbgen.TrainingRoutine, error) {
	return s.queries().CreateTrainingRoutine(ctx, params)
}

func (s PostgresStructuredTrainingStore) ListVisibleTrainingRoutines(ctx context.Context, params dbgen.ListVisibleTrainingRoutinesParams) ([]dbgen.ListVisibleTrainingRoutinesRow, error) {
	return s.queries().ListVisibleTrainingRoutines(ctx, params)
}

func (s PostgresStructuredTrainingStore) GetVisibleTrainingRoutine(ctx context.Context, params dbgen.GetVisibleTrainingRoutineParams) (dbgen.TrainingRoutine, error) {
	return s.queries().GetVisibleTrainingRoutine(ctx, params)
}

func (s PostgresStructuredTrainingStore) InsertTrainingRoutine(ctx context.Context, input StructuredRoutineInsertInput) (destinationID uuid.UUID, err error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := dbgen.New(tx)
	destinationKind := string(input.Routine.Kind)
	switch input.Routine.Kind {
	case dbgen.TrainingRoutineKindBLOCK:
		destinationID, err = queries.RestoreTrainingBlock(ctx, dbgen.RestoreTrainingBlockParams{Snapshot: input.Routine.Snapshot, SegmentID: input.TargetID})
	case dbgen.TrainingRoutineKindSEGMENT:
		destinationID, err = queries.RestoreTrainingSegment(ctx, dbgen.RestoreTrainingSegmentParams{Snapshot: input.Routine.Snapshot, SessionID: input.TargetID})
	case dbgen.TrainingRoutineKindSESSION:
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
	if err := queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "ROUTINE", SourceID: input.Routine.ID, SourceUpdatedAt: input.Routine.UpdatedAt, DestinationKind: destinationKind, DestinationID: destinationID, CopiedByID: input.ActorID}); err != nil {
		return uuid.Nil, err
	}
	return destinationID, tx.Commit(ctx)
}

func (s PostgresStructuredTrainingStore) CopyTrainingBlock(ctx context.Context, sourceID, targetSegmentID, actorID uuid.UUID) (destinationID uuid.UUID, err error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := dbgen.New(tx)
	source, err := queries.GetBlockRoutineSource(ctx, sourceID)
	if err != nil {
		return uuid.Nil, err
	}
	destinationID, err = queries.RestoreTrainingBlock(ctx, dbgen.RestoreTrainingBlockParams{Snapshot: source.Snapshot, SegmentID: targetSegmentID})
	if err != nil {
		return uuid.Nil, err
	}
	if err := queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "BLOCK", SourceID: sourceID, SourceUpdatedAt: source.SourceUpdatedAt, DestinationKind: "BLOCK", DestinationID: destinationID, CopiedByID: actorID}); err != nil {
		return uuid.Nil, err
	}
	return destinationID, tx.Commit(ctx)
}

func (s PostgresStructuredTrainingStore) CopyTrainingSession(ctx context.Context, sourceID, targetPlanID uuid.UUID, startsAt pgtype.Timestamptz, actorID uuid.UUID) (destinationID uuid.UUID, err error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := dbgen.New(tx)
	source, err := queries.GetSessionRoutineSource(ctx, sourceID)
	if err != nil {
		return uuid.Nil, err
	}
	destinationID, err = queries.RestoreTrainingSession(ctx, dbgen.RestoreTrainingSessionParams{Snapshot: source.Snapshot, PlanID: targetPlanID, StartsAt: startsAt, CreatedByID: actorID})
	if err != nil {
		return uuid.Nil, err
	}
	if err := queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "SESSION", SourceID: sourceID, SourceUpdatedAt: source.SourceUpdatedAt, DestinationKind: "SESSION", DestinationID: destinationID, CopiedByID: actorID}); err != nil {
		return uuid.Nil, err
	}
	return destinationID, tx.Commit(ctx)
}

func (s PostgresStructuredTrainingStore) CopyStructuredTrainingDay(ctx context.Context, input StructuredDayCopyInput) (copied int, err error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := dbgen.New(tx)
	rows, err := queries.ListStructuredSessionSnapshotsForDay(ctx, dbgen.ListStructuredSessionSnapshotsForDayParams{PlanID: input.SourcePlanID, SourceDate: pgtype.Date{Time: input.SourceDate, Valid: true}})
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		local := row.StartsAt.Time.In(input.TargetDate.Location())
		startsAt := time.Date(input.TargetDate.Year(), input.TargetDate.Month(), input.TargetDate.Day(), local.Hour(), local.Minute(), local.Second(), 0, input.TargetDate.Location())
		destinationID, err := queries.RestoreTrainingSession(ctx, dbgen.RestoreTrainingSessionParams{Snapshot: row.Snapshot, PlanID: input.TargetPlanID, StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, CreatedByID: input.ActorID})
		if err != nil {
			return copied, err
		}
		if err := queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "DAY", SourceID: row.ID, SourceUpdatedAt: row.UpdatedAt, DestinationKind: "SESSION", DestinationID: destinationID, CopiedByID: input.ActorID}); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, tx.Commit(ctx)
}

func (s PostgresStructuredTrainingStore) CopyStructuredTrainingWeek(ctx context.Context, input StructuredWeekCopyInput) (plan dbgen.TrainingPlan, err error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return plan, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := dbgen.New(tx)
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
	rows, err := queries.ListStructuredSessionSnapshotsForPlan(ctx, input.SourcePlanID)
	if err != nil {
		return plan, err
	}
	sourceDate := time.Date(source.WeekStart.Time.Year(), source.WeekStart.Time.Month(), source.WeekStart.Time.Day(), 0, 0, 0, 0, time.UTC)
	targetDate := time.Date(input.WeekStart.Year(), input.WeekStart.Month(), input.WeekStart.Day(), 0, 0, 0, 0, time.UTC)
	days := int(targetDate.Sub(sourceDate).Hours() / 24)
	for _, row := range rows {
		destinationID, err := queries.RestoreTrainingSession(ctx, dbgen.RestoreTrainingSessionParams{Snapshot: row.Snapshot, PlanID: plan.ID, StartsAt: pgtype.Timestamptz{Time: row.StartsAt.Time.AddDate(0, 0, days), Valid: true}, CreatedByID: input.ActorID})
		if err != nil {
			return plan, err
		}
		if err := queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "WEEK", SourceID: row.ID, SourceUpdatedAt: row.UpdatedAt, DestinationKind: "SESSION", DestinationID: destinationID, CopiedByID: input.ActorID}); err != nil {
			return plan, err
		}
	}
	if err := queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "WEEK", SourceID: input.SourcePlanID, SourceUpdatedAt: source.UpdatedAt, DestinationKind: "WEEK", DestinationID: plan.ID, CopiedByID: input.ActorID}); err != nil {
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

func (s PostgresStructuredTrainingStore) ListStructuredCrewModalities(ctx context.Context) ([]dbgen.ListStructuredCrewModalitiesRow, error) {
	return s.queries().ListStructuredCrewModalities(ctx)
}

func (s PostgresStructuredTrainingStore) ListManagedStructuredCompetitionEvents(ctx context.Context, params dbgen.ListManagedStructuredCompetitionEventsParams) ([]dbgen.ListManagedStructuredCompetitionEventsRow, error) {
	return s.queries().ListManagedStructuredCompetitionEvents(ctx, params)
}

func (s PostgresStructuredTrainingStore) CreateTrainingVariationGroup(ctx context.Context, input StructuredVariationGroupInput) (group dbgen.TrainingVariationGroup, err error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return group, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := dbgen.New(tx)
	if input.Params.Kind == dbgen.TrainingVariationGroupKindCREW {
		modalities, listErr := queries.ListStructuredCrewModalities(ctx)
		if listErr != nil {
			return group, listErr
		}
		capacity, valid := structuredCrewSize(modalities, input.Params.CraftModalityID)
		if !valid || capacity != len(input.MembershipIDs) {
			return group, errStructuredVariationCrewCapacity
		}
	}
	group, err = queries.CreateTrainingVariationGroup(ctx, input.Params)
	if err != nil {
		return group, err
	}
	for _, membershipID := range input.MembershipIDs {
		rows, addErr := queries.AddTrainingVariationGroupMember(ctx, dbgen.AddTrainingVariationGroupMemberParams{VariationGroupID: group.ID, MembershipID: membershipID, AddedByID: input.Params.CreatedByID})
		if addErr != nil {
			return group, addErr
		}
		if rows != 1 {
			return group, errStructuredVariationMemberScope
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return group, err
	}
	return group, nil
}

func (s PostgresStructuredTrainingStore) ListManagedTrainingVariationGroups(ctx context.Context, params dbgen.ListManagedTrainingVariationGroupsParams) ([]dbgen.ListManagedTrainingVariationGroupsRow, error) {
	return s.queries().ListManagedTrainingVariationGroups(ctx, params)
}

func (s PostgresStructuredTrainingStore) CreateTrainingVariation(ctx context.Context, params dbgen.CreateTrainingVariationParams) (dbgen.TrainingVariation, error) {
	return s.queries().CreateTrainingVariation(ctx, params)
}

func (s PostgresStructuredTrainingStore) ListTrainingVariationMatchesForManager(ctx context.Context, params dbgen.ListTrainingVariationMatchesForManagerParams) ([]dbgen.ListTrainingVariationMatchesForManagerRow, error) {
	return s.queries().ListTrainingVariationMatchesForManager(ctx, params)
}

func (s PostgresStructuredTrainingStore) GetTrainingVariationPlanID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	return s.queries().GetTrainingVariationPlanID(ctx, id)
}

func (s PostgresStructuredTrainingStore) RetireTrainingVariation(ctx context.Context, params dbgen.RetireTrainingVariationParams) (int64, error) {
	return s.queries().RetireTrainingVariation(ctx, params)
}
