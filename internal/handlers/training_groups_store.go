package handlers

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrTrainingGroupConflict = errors.New("training group changed; review required")
var ErrTrainingGroupEligibility = errors.New("training group selection is not eligible")

type TrainingGroupRecord struct {
	ID                                uuid.UUID
	Name, ScopeName                   string
	ProgrammeID, TeamID, LatestWeekID *uuid.UUID
	UpdatedAt                         time.Time
	MemberCount                       int64
}
type TrainingGroupScope struct {
	ProgrammeID, TeamID *uuid.UUID
	Name                string
}
type TrainingGroupAthlete struct {
	MembershipID, UserID uuid.UUID
	Name                 string
	AlreadyMember        bool
}
type TrainingGroupCreateInput struct {
	ActorID             uuid.UUID
	Name                string
	ProgrammeID, TeamID *uuid.UUID
}
type TrainingGroupRenameInput struct {
	ActorID, GroupID  uuid.UUID
	Name              string
	ExpectedUpdatedAt time.Time
}
type TrainingGroupRosterInput struct {
	ActorID, GroupID  uuid.UUID
	MembershipIDs     []uuid.UUID
	ExpectedUpdatedAt time.Time
}
type TrainingGroupRosterResult struct {
	Group TrainingGroupRecord
	Added int
}
type TrainingGroupStore interface {
	ListGroups(context.Context, uuid.UUID, int32) ([]TrainingGroupRecord, error)
	ListScopes(context.Context, uuid.UUID) ([]TrainingGroupScope, error)
	GetGroup(context.Context, uuid.UUID, uuid.UUID) (TrainingGroupRecord, error)
	ListRoster(context.Context, uuid.UUID, uuid.UUID, int32) ([]TrainingGroupAthlete, error)
	SearchAthletes(context.Context, uuid.UUID, uuid.UUID, string, int32) ([]TrainingGroupAthlete, error)
	CreateGroup(context.Context, TrainingGroupCreateInput) (TrainingGroupRecord, error)
	RenameGroup(context.Context, TrainingGroupRenameInput) (TrainingGroupRecord, error)
	AddAthletes(context.Context, TrainingGroupRosterInput) (TrainingGroupRosterResult, error)
}
type PostgresTrainingGroupStore struct{ Pool structuredTrainingDB }

func groupRecord(r dbgen.TrainingGroupRecordsRow) TrainingGroupRecord {
	return TrainingGroupRecord{ID: r.ID, Name: r.Name, ScopeName: r.ScopeName, ProgrammeID: r.ProgrammeID, TeamID: r.TeamID, LatestWeekID: taskGroupWeekID(r.LatestWeekID), UpdatedAt: r.UpdatedAt.Time, MemberCount: r.MemberCount}
}
func getTaskGroup(ctx context.Context, q *dbgen.Queries, actor, id uuid.UUID) (TrainingGroupRecord, error) {
	rows, err := q.TrainingGroupRecords(ctx, dbgen.TrainingGroupRecordsParams{ActorID: actor, GroupID: &id, RowLimit: 1})
	if err != nil {
		return TrainingGroupRecord{}, err
	}
	if len(rows) != 1 {
		return TrainingGroupRecord{}, pgx.ErrNoRows
	}
	return groupRecord(rows[0]), nil
}
func (s PostgresTrainingGroupStore) ListGroups(ctx context.Context, actor uuid.UUID, offset int32) ([]TrainingGroupRecord, error) {
	if offset < 0 || offset > 1980 {
		return nil, ErrTrainingGroupEligibility
	}
	rows, err := dbgen.New(s.Pool).TrainingGroupRecords(ctx, dbgen.TrainingGroupRecordsParams{ActorID: actor, RowLimit: 20, RowOffset: offset})
	if err != nil {
		return nil, err
	}
	out := []TrainingGroupRecord{}
	for _, r := range rows {
		out = append(out, groupRecord(r))
	}
	return out, nil
}
func (s PostgresTrainingGroupStore) ListScopes(ctx context.Context, actor uuid.UUID) ([]TrainingGroupScope, error) {
	rows, err := dbgen.New(s.Pool).TrainingGroupScopeOptions(ctx, actor)
	if err != nil {
		return nil, err
	}
	out := []TrainingGroupScope{}
	for _, r := range rows {
		out = append(out, TrainingGroupScope{ProgrammeID: r.ProgrammeID, TeamID: r.TeamID, Name: r.Name})
	}
	return out, nil
}
func (s PostgresTrainingGroupStore) GetGroup(ctx context.Context, actor, id uuid.UUID) (TrainingGroupRecord, error) {
	return getTaskGroup(ctx, dbgen.New(s.Pool), actor, id)
}
func (s PostgresTrainingGroupStore) athletes(ctx context.Context, actor, id uuid.UUID, q string, offset int32, only bool) ([]TrainingGroupAthlete, error) {
	if offset < 0 || offset > 1980 || utf8.RuneCountInString(q) > 100 {
		return nil, ErrTrainingGroupEligibility
	}
	if _, err := s.GetGroup(ctx, actor, id); err != nil {
		return nil, err
	}
	rows, err := dbgen.New(s.Pool).TrainingGroupAthleteChoices(ctx, dbgen.TrainingGroupAthleteChoicesParams{ActorID: actor, GroupID: id, OnlyMembers: only, NameQuery: q, RowOffset: offset})
	if err != nil {
		return nil, err
	}
	out := []TrainingGroupAthlete{}
	for _, r := range rows {
		out = append(out, TrainingGroupAthlete{MembershipID: r.MembershipID, UserID: r.UserID, Name: r.Name, AlreadyMember: r.AlreadyMember})
	}
	return out, nil
}
func (s PostgresTrainingGroupStore) ListRoster(ctx context.Context, actor, id uuid.UUID, offset int32) ([]TrainingGroupAthlete, error) {
	return s.athletes(ctx, actor, id, "", offset, true)
}
func (s PostgresTrainingGroupStore) SearchAthletes(ctx context.Context, actor, id uuid.UUID, q string, offset int32) ([]TrainingGroupAthlete, error) {
	return s.athletes(ctx, actor, id, strings.TrimSpace(q), offset, false)
}

func lockTrainingGroupActor(ctx context.Context, tx dbgen.DBTX, actor uuid.UUID) error {
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND is_active AND erased_at IS NULL AND NOT is_dependent AND date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date FOR SHARE`, actor).Scan(&id); err != nil {
		return err
	}
	// Lock existing authority rows against concurrent revocation; subsequent queries
	// derive administrator status and exact grants from the database, never POST.
	for _, query := range []string{`SELECT role_id FROM user_platform_roles WHERE user_id=$1 FOR SHARE`, `SELECT id FROM staff_grants WHERE user_id=$1 AND capability='COACH' AND revoked_at IS NULL ORDER BY id FOR SHARE`} {
		rows, err := tx.Query(ctx, query, actor)
		if err != nil {
			return err
		}
		for rows.Next() {
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
func (s PostgresTrainingGroupStore) transaction(ctx context.Context, actor uuid.UUID) (pgx.Tx, *dbgen.Queries, error) {
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
func (s PostgresTrainingGroupStore) CreateGroup(ctx context.Context, in TrainingGroupCreateInput) (TrainingGroupRecord, error) {
	out, _, err := s.createGroupWithMembers(ctx, in, nil)
	return out, err
}
func (s PostgresTrainingGroupStore) createGroupWithMembers(ctx context.Context, in TrainingGroupCreateInput, members []uuid.UUID) (TrainingGroupRecord, dbgen.TrainingGroup, error) {
	fail := func(err error) (TrainingGroupRecord, dbgen.TrainingGroup, error) {
		return TrainingGroupRecord{}, dbgen.TrainingGroup{}, err
	}
	if in.Name != strings.TrimSpace(in.Name) || utf8.RuneCountInString(in.Name) < 2 || utf8.RuneCountInString(in.Name) > 120 || (in.ProgrammeID == nil) == (in.TeamID == nil) {
		return fail(ErrTrainingGroupEligibility)
	}
	tx, q, err := s.transaction(ctx, in.ActorID)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	scopes, err := q.TrainingGroupScopeOptions(ctx, in.ActorID)
	if err != nil {
		return fail(err)
	}
	allowed := false
	for _, r := range scopes {
		if sameGroupScope(r.ProgrammeID, in.ProgrammeID) && sameGroupScope(r.TeamID, in.TeamID) {
			allowed = true
			break
		}
	}
	if !allowed {
		return fail(pgx.ErrNoRows)
	}
	row, err := q.InsertTaskTrainingGroup(ctx, dbgen.InsertTaskTrainingGroupParams{ActorID: in.ActorID, Name: in.Name, ProgrammeID: in.ProgrammeID, TeamID: in.TeamID})
	if err != nil {
		return fail(err)
	}
	if len(members) > 0 {
		if _, err = addTaskGroupMembers(ctx, tx, q, TrainingGroupRosterInput{ActorID: in.ActorID, GroupID: row.ID, MembershipIDs: members}); err != nil {
			return fail(err)
		}
	}
	out, err := getTaskGroup(ctx, q, in.ActorID, row.ID)
	if err != nil {
		return fail(err)
	}
	row.UpdatedAt.Time = out.UpdatedAt
	if err = tx.Commit(ctx); err != nil {
		return fail(err)
	}
	return out, row, nil
}
func sameGroupScope(a, b *uuid.UUID) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func lockTaskGroup(ctx context.Context, tx pgx.Tx, q *dbgen.Queries, actor, id uuid.UUID, expected time.Time) (TrainingGroupRecord, error) {
	if _, err := getTaskGroup(ctx, q, actor, id); err != nil {
		return TrainingGroupRecord{}, err
	}
	var ignored uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM training_groups WHERE id=$1 FOR UPDATE`, id).Scan(&ignored); err != nil {
		return TrainingGroupRecord{}, err
	}
	r, err := getTaskGroup(ctx, q, actor, id)
	if err != nil {
		return r, err
	}
	if !r.UpdatedAt.Equal(expected) {
		return r, ErrTrainingGroupConflict
	}
	return r, nil
}
func (s PostgresTrainingGroupStore) RenameGroup(ctx context.Context, in TrainingGroupRenameInput) (TrainingGroupRecord, error) {
	if in.Name != strings.TrimSpace(in.Name) || utf8.RuneCountInString(in.Name) < 2 || utf8.RuneCountInString(in.Name) > 120 {
		return TrainingGroupRecord{}, ErrTrainingGroupEligibility
	}
	tx, q, err := s.transaction(ctx, in.ActorID)
	if err != nil {
		return TrainingGroupRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = lockTaskGroup(ctx, tx, q, in.ActorID, in.GroupID, in.ExpectedUpdatedAt); err != nil {
		return TrainingGroupRecord{}, err
	}
	if err = q.RenameTaskTrainingGroup(ctx, dbgen.RenameTaskTrainingGroupParams{Name: in.Name, GroupID: in.GroupID}); err != nil {
		return TrainingGroupRecord{}, err
	}
	out, err := getTaskGroup(ctx, q, in.ActorID, in.GroupID)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s PostgresTrainingGroupStore) AddAthletes(ctx context.Context, in TrainingGroupRosterInput) (TrainingGroupRosterResult, error) {
	if len(in.MembershipIDs) == 0 || len(in.MembershipIDs) > 20 {
		return TrainingGroupRosterResult{}, ErrTrainingGroupEligibility
	}
	tx, q, err := s.transaction(ctx, in.ActorID)
	if err != nil {
		return TrainingGroupRosterResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = lockTaskGroup(ctx, tx, q, in.ActorID, in.GroupID, in.ExpectedUpdatedAt); err != nil {
		return TrainingGroupRosterResult{}, err
	}
	added, err := addTaskGroupMembers(ctx, tx, q, in)
	if err != nil {
		return TrainingGroupRosterResult{}, err
	}
	group, err := getTaskGroup(ctx, q, in.ActorID, in.GroupID)
	if err != nil {
		return TrainingGroupRosterResult{}, err
	}
	out := TrainingGroupRosterResult{Group: group, Added: added}
	return out, tx.Commit(ctx)
}

func taskGroupWeekID(value string) *uuid.UUID {
	id, err := uuid.Parse(value)
	if err != nil {
		return nil
	}
	return &id
}

func addTaskGroupMembers(ctx context.Context, tx pgx.Tx, q *dbgen.Queries, in TrainingGroupRosterInput) (int, error) {
	ids := append([]uuid.UUID{}, in.MembershipIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	for i, id := range ids {
		if id == uuid.Nil || i > 0 && id == ids[i-1] {
			return 0, ErrTrainingGroupEligibility
		}
	}
	// Every placement writer locks people in deterministic order. This serializes
	// same-person additions across different groups without ending old placements.
	rows, err := tx.Query(ctx, `SELECT u.id FROM users u WHERE u.id IN (SELECT user_id FROM user_memberships WHERE id=ANY($1::uuid[])) ORDER BY u.id FOR UPDATE`, ids)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err = q.ValidateTaskTrainingGroupAthlete(ctx, dbgen.ValidateTaskTrainingGroupAthleteParams{ActorID: in.ActorID, GroupID: in.GroupID, MembershipID: id}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				err = ErrTrainingGroupEligibility
			}
			return 0, err
		}
	}
	added := 0
	for _, id := range ids {
		n, e := q.AddTaskTrainingGroupAthlete(ctx, dbgen.AddTaskTrainingGroupAthleteParams{ActorID: in.ActorID, GroupID: in.GroupID, MembershipID: id})
		if e != nil {
			return 0, e
		}
		added += int(n)
	}
	if added > 0 {
		if err = q.TouchTaskTrainingGroup(ctx, in.GroupID); err != nil {
			return 0, err
		}
	}
	return added, nil
}

// AllowsWeek validates an older planner return context without loading any unrelated planner data.
func (s PostgresTrainingGroupStore) AllowsWeek(ctx context.Context, actor, group, week uuid.UUID) (bool, error) {
	if _, err := s.GetGroup(ctx, actor, group); err != nil {
		return false, err
	}
	return dbgen.New(s.Pool).TrainingGroupAllowsWeek(ctx, dbgen.TrainingGroupAllowsWeekParams{ActorID: actor, GroupID: group, WeekID: week})
}
