package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrDatedParticipationForbidden  = errors.New("dated participation requires an active adult administrator and eligible member")
	ErrDatedParticipationWindow     = errors.New("ordinary assignment starts today or tomorrow; historical changes require audited correction")
	ErrDatedParticipationTransition = errors.New("transition requires a previously started interval covering tomorrow; same-day reversal is unavailable")
	ErrDatedPoloTeamUnavailable     = errors.New("a current-season Kayak Polo team must exist before assigning participation")
)

// OrdinaryDatedAssignment is the only non-correction service input. Date is a
// Lisbon calendar day; no actor GUC, reason, backdated date or history rewrite.
type OrdinaryDatedAssignment struct {
	ActorID         uuid.UUID
	MemberID        uuid.UUID
	SeasonID        uuid.UUID
	ProgrammeID     uuid.UUID
	CategoryID      *uuid.UUID
	StartsOn        time.Time
	ExpectedVersion string
}

// AgeExceptionAssignment supplies private evidence for a category mismatch.
// ActorID must be sourced from the authenticated server session, never request data.
type AgeExceptionAssignment struct {
	ActorID, MemberID, SeasonID, ProgrammeID, CategoryID uuid.UUID
	StartsOn                                             time.Time
	Reason                                               string
	ExpectedVersion                                      string
}

var ErrAgeExceptionReason = errors.New("age exception reason must contain 2 to 500 characters")

func (s DatedParticipationService) CreateAgeException(ctx context.Context, in AgeExceptionAssignment) (dbgen.UserMembership, error) {
	return s.writeException(ctx, in, nil)
}

func (s DatedParticipationService) TransitionNextDayAgeException(ctx context.Context, oldID uuid.UUID, in AgeExceptionAssignment) (dbgen.UserMembership, error) {
	return s.writeException(ctx, in, &oldID)
}

func (s DatedParticipationService) writeException(ctx context.Context, in AgeExceptionAssignment, oldID *uuid.UUID) (dbgen.UserMembership, error) {
	reason := strings.TrimSpace(in.Reason)
	if len([]rune(reason)) < 2 || len([]rune(reason)) > 500 || in.CategoryID == uuid.Nil {
		return dbgen.UserMembership{}, ErrAgeExceptionReason
	}
	return s.writeWithReason(ctx, OrdinaryDatedAssignment{ActorID: in.ActorID, MemberID: in.MemberID, SeasonID: in.SeasonID, ProgrammeID: in.ProgrammeID, CategoryID: &in.CategoryID, StartsOn: in.StartsOn, ExpectedVersion: in.ExpectedVersion}, oldID, reason)
}

type DatedParticipationService struct{ Pool *pgxpool.Pool }

// CreateOrdinaryAssignment inserts one interval; overlapping dates fail in the DB.
// This is a server-side API, not a public route or an authorization-free sqlc call.
func (s DatedParticipationService) CreateOrdinaryAssignment(ctx context.Context, in OrdinaryDatedAssignment) (dbgen.UserMembership, error) {
	return s.write(ctx, in, nil)
}

// TransitionNextDay closes the old interval today and creates a new interval
// tomorrow atomically; it cannot correct the past or reverse a same-day start.
func (s DatedParticipationService) TransitionNextDay(ctx context.Context, oldMembershipID uuid.UUID, in OrdinaryDatedAssignment) (dbgen.UserMembership, error) {
	return s.write(ctx, in, &oldMembershipID)
}

func (s DatedParticipationService) write(ctx context.Context, in OrdinaryDatedAssignment, oldID *uuid.UUID) (dbgen.UserMembership, error) {
	return s.writeWithReason(ctx, in, oldID, "")
}

func (s DatedParticipationService) writeWithReason(ctx context.Context, in OrdinaryDatedAssignment, oldID *uuid.UUID, reason string) (dbgen.UserMembership, error) {
	var empty dbgen.UserMembership
	if s.Pool == nil || in.ActorID == uuid.Nil || in.MemberID == uuid.Nil || in.SeasonID == uuid.Nil || in.ProgrammeID == uuid.Nil || in.StartsOn.IsZero() {
		return empty, ErrDatedParticipationWindow
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	var today time.Time
	if err = tx.QueryRow(ctx, `SELECT (now() AT TIME ZONE 'Europe/Lisbon')::date`).Scan(&today); err != nil {
		return empty, err
	}
	tomorrow := today.AddDate(0, 0, 1)
	start := in.StartsOn.Format("2006-01-02")
	if (oldID == nil && start != today.Format("2006-01-02") && start != tomorrow.Format("2006-01-02")) || (oldID != nil && start != tomorrow.Format("2006-01-02")) {
		return empty, ErrDatedParticipationWindow
	}
	var authorized bool
	if err = tx.QueryRow(ctx, `SELECT guardian_authority_is_administrator($1)`, in.ActorID).Scan(&authorized); err != nil {
		return empty, err
	}
	if !authorized {
		// A current-season programme grant permits the initial assignment too.
		// Lock the grant against concurrent revocation through commit; a team-only
		// grant and an inactive coach never authorize this programme writer.
		var grantID uuid.UUID
		err = tx.QueryRow(ctx, `SELECT g.id FROM staff_grants g
  JOIN users actor ON actor.id=g.user_id AND actor.is_active AND actor.erased_at IS NULL
  JOIN seasons season ON season.id=$2 AND season.is_current
  WHERE g.user_id=$1 AND g.capability='COACH' AND g.revoked_at IS NULL
    AND g.programme_id=$3 AND g.programme_id IS NOT NULL
  LIMIT 1`, in.ActorID, in.SeasonID, in.ProgrammeID).Scan(&grantID)
		if errors.Is(err, pgx.ErrNoRows) {
			return empty, ErrDatedParticipationForbidden
		}
		if err != nil {
			return empty, err
		}
		var active bool
		if err = tx.QueryRow(ctx, `SELECT revoked_at IS NULL FROM staff_grants WHERE id=$1 FOR SHARE`, grantID).Scan(&active); err != nil {
			return empty, err
		}
		if !active {
			return empty, ErrDatedParticipationForbidden
		}
	}
	var eligible bool
	if err = tx.QueryRow(ctx, `SELECT `+ClassificationSubjectEligibilitySQL+`
  FROM users member WHERE member.id=$1 FOR UPDATE`, in.MemberID).Scan(&eligible); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return empty, ErrDatedParticipationForbidden
		}
		return empty, err
	}
	if !eligible {
		return empty, ErrDatedParticipationForbidden
	}
	if in.ExpectedVersion != "" {
		version, e := DatedClassificationVersion(ctx, tx, in.MemberID)
		if e != nil {
			return empty, e
		}
		if version != in.ExpectedVersion {
			return empty, ErrClassificationStale
		}
	}
	// Resolve the shared Polo team inside the interval transaction, never from form data.
	var teamID *uuid.UUID
	var polo bool
	if err = tx.QueryRow(ctx, `SELECT p.code='Kayak_Polo' FROM programmes p WHERE p.id=$1`, in.ProgrammeID).Scan(&polo); err != nil {
		return empty, err
	}
	if polo {
		var team uuid.UUID
		err = tx.QueryRow(ctx, `SELECT t.id FROM teams t JOIN seasons s ON s.id=t.season_id AND s.is_current
		 WHERE t.season_id=$1 AND t.programme_id=$2 FOR SHARE OF t`, in.SeasonID, in.ProgrammeID).Scan(&team)
		if errors.Is(err, pgx.ErrNoRows) {
			return empty, ErrDatedPoloTeamUnavailable
		}
		if err != nil {
			return empty, err
		}
		teamID = &team
	}
	if oldID != nil {
		result, e := tx.Exec(ctx, `UPDATE user_memberships SET ends_on=$1::date,updated_at=now()
   WHERE id=$2 AND user_id=$3 AND season_id=$4 AND starts_on<$1::date
    AND (ends_on IS NULL OR ends_on >= $5::date) AND ($6::boolean OR programme_id=$7)`, today, *oldID, in.MemberID, in.SeasonID, tomorrow, authorized, in.ProgrammeID)
		if e != nil {
			return empty, fmt.Errorf("close prior participation: %w", e)
		}
		if result.RowsAffected() != 1 {
			return empty, ErrDatedParticipationTransition
		}
	}
	var row dbgen.UserMembership
	if reason != "" {
		err = tx.QueryRow(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,team_id,competition_category_id,starts_on,age_exception_reason,age_exception_by_id,age_exception_at)
		 VALUES($1,$2,$3,$4,$5,$6::date,$7,$8,now()) RETURNING id,user_id,season_id,programme_id,team_id,competition_category_id,starts_on,ends_on,created_at,updated_at,age_exception_reason,age_exception_by_id,age_exception_at,principal_id`,
			in.MemberID, in.SeasonID, in.ProgrammeID, teamID, in.CategoryID, in.StartsOn, reason, in.ActorID).Scan(
			&row.ID, &row.UserID, &row.SeasonID, &row.ProgrammeID, &row.TeamID, &row.CompetitionCategoryID, &row.StartsOn, &row.EndsOn, &row.CreatedAt, &row.UpdatedAt, &row.AgeExceptionReason, &row.AgeExceptionByID, &row.AgeExceptionAt, &row.PrincipalID)
	} else if polo {
		err = tx.QueryRow(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,team_id,competition_category_id,starts_on)
		 VALUES($1,$2,$3,$4,$5,$6::date) RETURNING id,user_id,season_id,programme_id,team_id,competition_category_id,starts_on,ends_on,created_at,updated_at,age_exception_reason,age_exception_by_id,age_exception_at,principal_id`,
			in.MemberID, in.SeasonID, in.ProgrammeID, teamID, in.CategoryID, in.StartsOn).Scan(
			&row.ID, &row.UserID, &row.SeasonID, &row.ProgrammeID, &row.TeamID, &row.CompetitionCategoryID, &row.StartsOn, &row.EndsOn, &row.CreatedAt, &row.UpdatedAt, &row.AgeExceptionReason, &row.AgeExceptionByID, &row.AgeExceptionAt, &row.PrincipalID)
	} else {
		row, err = dbgen.New(tx).CreateDatedParticipation(ctx, dbgen.CreateDatedParticipationParams{
			UserID: &in.MemberID, SeasonID: in.SeasonID, ProgrammeID: in.ProgrammeID,
			CompetitionCategoryID: in.CategoryID, StartsOn: pgtype.Date{Time: in.StartsOn, Valid: true},
		})
	}
	if err != nil {
		return empty, fmt.Errorf("create participation: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return row, nil
}
