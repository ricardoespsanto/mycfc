package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

func (s PostgresTrainingGroupStore) ReviewAthleteSelection(ctx context.Context, in TrainingGroupRosterInput, rows []TrainingGroupAthlete) (string, error) {
	if len(rows) == 0 || len(rows) > 20 || in.CommandID == uuid.Nil || in.SeasonID == uuid.Nil || in.EffectiveOn.IsZero() {
		return "", errTrainingPlacementInvalid
	}
	state, e := placementRuntime(ctx, s.Pool)
	if e != nil {
		return "", e
	}
	q := dbgen.New(s.Pool)
	group, e := getTaskGroup(ctx, q, in.ActorID, in.GroupID)
	if e != nil {
		return "", e
	}
	if !group.UpdatedAt.Equal(in.ExpectedUpdatedAt) {
		return "", ErrTrainingGroupConflict
	}
	var programme uuid.UUID
	if e = s.Pool.QueryRow(ctx, `SELECT public.training_placement_group_programme($1)`, in.GroupID).Scan(&programme); e != nil {
		return "", e
	}
	offer := trainingPlacementSelection{ActorID: in.ActorID, GroupID: in.GroupID, SeasonID: in.SeasonID, CommandID: in.CommandID, EffectiveOn: in.EffectiveOn.Format("2006-01-02"), GroupVersion: in.ExpectedUpdatedAt.Format(time.RFC3339Nano)}
	for i, row := range rows {
		if row.AlreadyMember || row.Unavailable {
			continue
		}
		if row.UserID == uuid.Nil || row.MembershipID == uuid.Nil || row.GroupID != in.GroupID {
			return "", errTrainingPlacementInvalid
		}
		c := trainingPlacementCommand{ID: placementSelectionCommand(in.CommandID, row.UserID), SubjectID: row.UserID, ProgrammeID: programme, ContextGroupID: in.GroupID, SeasonID: in.SeasonID, Kind: placementJoin, TargetGroupID: in.GroupID, EffectiveOn: offer.EffectiveOn}
		read, e := readTrainingPlacement(ctx, q, in.ActorID, c, state, true)
		if e != nil {
			return "", e
		}
		if read.Subject == nil || read.Subject.MembershipID != row.MembershipID {
			return "", errTrainingPlacementChanged
		}
		unavailable := false
		for _, placement := range read.Timeline {
			if placement.CancelledByChangeID == nil && placement.SupersededByChangeID == nil && (!placement.EndsOn.Valid || placementDateString(placement.EndsOn) >= c.EffectiveOn) {
				unavailable = true
			}
		}
		if unavailable {
			rows[i].Unavailable = true
			continue
		}
		c.ExpectedRevision = read.revision()
		payload, e := read.proposal(in.ActorID, c)
		if e != nil {
			return "", e
		}
		offer.Entries = append(offer.Entries, trainingPlacementSelectionEntry{MembershipID: row.MembershipID, Review: payload})
	}
	if len(offer.Entries) == 0 {
		return "", nil
	}
	return focusedWorkoutReviews.signPlacementSelection(offer, time.Now())
}

// All subject rows precede all head locks. This helper takes no membership or
// group lock until the caller has established those physical tuple witnesses.
func (q trainingPlacementPostgresQueries) PreparePlacementBatch(ctx context.Context, in TrainingGroupRosterInput, reviews []trainingPlacementReviewed) error {
	self := false
	subjects := make([]uuid.UUID, 0, len(reviews))
	for _, r := range reviews {
		subjects = append(subjects, r.Payload.Command.SubjectID)
		if r.Payload.Command.SubjectID == in.ActorID {
			self = true
		}
	}
	actorMode := "SHARE"
	if self {
		actorMode = "UPDATE"
	}
	var id uuid.UUID
	if e := q.commands.QueryRow(ctx, `SELECT id FROM public.users WHERE id=$1 AND is_active AND erased_at IS NULL AND NOT is_dependent AND date_of_birth<=((clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date FOR `+actorMode+` NOWAIT`, in.ActorID).Scan(&id); e != nil {
		return e
	}
	for _, sql := range []string{`SELECT role_id FROM public.user_platform_roles WHERE user_id=$1 ORDER BY role_id FOR SHARE NOWAIT`, `SELECT id FROM public.staff_grants WHERE user_id=$1 AND capability='COACH' AND revoked_at IS NULL ORDER BY id FOR SHARE NOWAIT`} {
		rows, e := q.commands.Query(ctx, sql, in.ActorID)
		if e != nil {
			return e
		}
		for rows.Next() {
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	for _, subject := range subjects {
		if subject == in.ActorID {
			continue
		}
		if e := q.commands.QueryRow(ctx, `SELECT id FROM public.users WHERE id=$1 AND is_active AND erased_at IS NULL FOR UPDATE NOWAIT`, subject).Scan(&id); e != nil {
			return e
		}
	}
	// The Data-owned fixed scoped function locks EXISTING heads only. Missing
	// heads are safe under held subject UPDATE; actual Apply owns their insertion.
	if e := q.lockPlacementBatchHeads(ctx, in, subjects); e != nil {
		return e
	}
	if _, e := getTaskGroup(ctx, q.Queries, in.ActorID, in.GroupID); e != nil {
		return e
	}
	if e := q.commands.QueryRow(ctx, `SELECT id FROM public.training_groups WHERE id=$1 FOR UPDATE NOWAIT`, in.GroupID).Scan(&id); e != nil {
		return e
	}
	actual, e := getTaskGroup(ctx, q.Queries, in.ActorID, in.GroupID)
	if e != nil {
		return e
	}
	if !actual.UpdatedAt.Equal(in.ExpectedUpdatedAt) {
		return ErrTrainingGroupConflict
	}
	return nil
}
func (q trainingPlacementPostgresQueries) PlacementBatchGroup(ctx context.Context, actor, group uuid.UUID, touch bool) (TrainingGroupRecord, error) {
	if touch {
		if e := q.TouchTaskTrainingGroup(ctx, group); e != nil {
			return TrainingGroupRecord{}, e
		}
	}
	return getTaskGroup(ctx, q.Queries, actor, group)
}
func placementBatchError(outcome db.SessionTxResult, e error) error {
	e = trainingPlacementTransactionError(outcome, e)
	if errors.Is(e, errTrainingPlacementChanged) {
		return ErrTrainingGroupConflict
	}
	var field *trainingPlacementFieldFailure
	if errors.Is(e, errTrainingPlacementInvalid) || errors.As(e, &field) {
		return ErrTrainingGroupEligibility
	}
	return e
}

func (q trainingPlacementPostgresQueries) lockPlacementBatchHeads(ctx context.Context, in TrainingGroupRosterInput, subjects []uuid.UUID) error {
	heads, e := q.LockTrainingPlacementExistingHeadsForJoin(ctx, dbgen.LockTrainingPlacementExistingHeadsForJoinParams{ActorID: in.ActorID, GroupID: in.GroupID, SubjectIds: subjects})
	if e != nil {
		return e
	}
	wanted := map[uuid.UUID]bool{}
	seen := map[uuid.UUID]bool{}
	for _, id := range subjects {
		wanted[id] = true
	}
	for _, head := range heads {
		if !wanted[head.SubjectUserID] || seen[head.SubjectUserID] || head.ProgrammeID == uuid.Nil || head.Revision < 1 {
			return errTrainingPlacementChanged
		}
		seen[head.SubjectUserID] = true
	}
	return nil
}
