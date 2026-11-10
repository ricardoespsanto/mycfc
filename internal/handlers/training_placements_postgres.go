package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresTrainingPlacementStore struct{ Pool *pgxpool.Pool }

func placementRuntime(ctx context.Context, q db.TrainingScheduleQueryReader) (db.TrainingScheduleState, error) {
	state, e := db.VerifyTrainingPlacementRuntime(ctx, q, publicationRuntimeCapabilities())
	if e != nil {
		return state, errors.Join(errTrainingPlacementUnavailable, e)
	}
	return state, nil
}
func (s PostgresTrainingPlacementStore) PlacementContext(ctx context.Context, actor, group uuid.UUID) (trainingPlacementPageContext, error) {
	var out trainingPlacementPageContext
	if _, e := placementRuntime(ctx, s.Pool); e != nil {
		return out, e
	}
	q := dbgen.New(s.Pool)
	g, e := getTaskGroup(ctx, q, actor, group)
	if e != nil {
		return out, e
	}
	out.Group = g
	if e = s.Pool.QueryRow(ctx, `SELECT public.training_placement_group_programme($1)`, group).Scan(&out.ProgrammeID); e != nil {
		return out, e
	}
	if out.ProgrammeID == uuid.Nil {
		return out, pgx.ErrNoRows
	}
	out.Seasons, e = q.ListReuseTargetSeasons(ctx, dbgen.ListReuseTargetSeasonsParams{ActorID: actor, GroupID: group})
	if len(out.Seasons) > 100 {
		return trainingPlacementPageContext{}, errTrainingPlacementLimit
	}
	return out, e
}
func (s PostgresTrainingPlacementStore) PlacementView(ctx context.Context, actor, group, person, season uuid.UUID, on string) (trainingPlacementView, error) {
	var out trainingPlacementView
	c, e := s.PlacementContext(ctx, actor, group)
	if e != nil {
		return out, e
	}
	if !validWorkspaceDate(on) || person == uuid.Nil {
		return out, errTrainingPlacementInvalid
	}
	selected := false
	for _, s := range c.Seasons {
		if s.ID == season && on >= placementDateString(s.StartsOn) && on <= placementDateString(s.EndsOn) {
			selected = true
		}
	}
	if !selected {
		return out, errTrainingPlacementInvalid
	}
	q := dbgen.New(s.Pool)
	rows, e := q.TrainingPlacementTimeline(ctx, dbgen.TrainingPlacementTimelineParams{ActorID: actor, SubjectID: person, ProgrammeID: c.ProgrammeID, RowLimit: trainingPlacementMaxTimeline + 1})
	if e != nil {
		return out, e
	}
	if len(rows) > trainingPlacementMaxTimeline {
		return out, errTrainingPlacementLimit
	}
	subject, e := q.TrainingPlacementSubjectForReview(ctx, dbgen.TrainingPlacementSubjectForReviewParams{ActorID: actor, SubjectID: person, GroupID: group, SeasonID: season, EffectiveOn: placementDate(on)})
	if e == nil {
		out.PersonID = subject.SubjectID
		out.PersonName = subject.SubjectName
		out.Revision = subject.Revision
	} else if errors.Is(e, pgx.ErrNoRows) {
		// A retained source placement is the scoped directory anchor for LEAVE after
		// an episode gap. Never use a handtyped person UUID as an unrestricted lookup.
		var anchor *dbgen.TrainingPlacementSourceForReviewRow
		for _, row := range rows {
			if row.GroupID != group {
				continue
			}
			a, readErr := q.TrainingPlacementSourceForReview(ctx, dbgen.TrainingPlacementSourceForReviewParams{ActorID: actor, SourcePlacementID: row.ID})
			if readErr != nil {
				return out, readErr
			}
			if a.SubjectID != person || a.ProgrammeID != c.ProgrammeID {
				return out, pgx.ErrNoRows
			}
			anchor = &a
			break
		}
		if anchor == nil {
			return out, pgx.ErrNoRows
		}
		out.PersonID = anchor.SubjectID
		out.PersonName = anchor.SubjectName
		out.Revision = anchor.Revision
	} else {
		return out, e
	}
	if out.PersonID != person {
		return out, pgx.ErrNoRows
	}
	for _, row := range rows {
		if row.SubjectUserID == nil || *row.SubjectUserID != person || row.ProgrammeID != c.ProgrammeID || row.Revision != out.Revision {
			return trainingPlacementView{}, errTrainingPlacementChanged
		}
	}
	out.Timeline = rows
	out.Groups, e = q.TrainingPlacementGroupChoices(ctx, dbgen.TrainingPlacementGroupChoicesParams{ActorID: actor, ProgrammeID: c.ProgrammeID, SeasonID: season, RowLimit: trainingPlacementMaxTimeline + 1})
	if len(out.Groups) > trainingPlacementMaxTimeline {
		return trainingPlacementView{}, errTrainingPlacementLimit
	}
	return out, e
}
func (s PostgresTrainingPlacementStore) ReadPlacement(ctx context.Context, actor uuid.UUID, c trainingPlacementCommand, refresh bool) (trainingPlacementRead, error) {
	state, e := placementRuntime(ctx, s.Pool)
	if e != nil {
		return trainingPlacementRead{}, e
	}
	return readTrainingPlacement(ctx, dbgen.New(s.Pool), actor, c, state, refresh)
}
func (s PostgresTrainingPlacementStore) ReadPlacementCancellation(ctx context.Context, actor, group, person, change uuid.UUID) (trainingPlacementCancellationRead, error) {
	state, e := placementRuntime(ctx, s.Pool)
	if e != nil {
		return trainingPlacementCancellationRead{}, e
	}
	return readTrainingPlacementCancellation(ctx, dbgen.New(s.Pool), actor, group, person, change, state)
}

type trainingPlacementPostgresQueries struct {
	*dbgen.Queries
	commands dbgen.DBTX
}

func (q trainingPlacementPostgresQueries) VerifyPlacementRuntime(ctx context.Context) (db.TrainingScheduleState, error) {
	before, e := db.TrainingScheduleStatus(ctx, q.commands)
	if e != nil {
		return before, errors.Join(errTrainingPlacementUnavailable, e)
	}
	if before.PartialV2Enabled {
		if _, e = q.commands.Exec(ctx, `SELECT public.training_schedule_admit_partial($1,$2)`, before.DatabaseIncarnation, before.StateEpoch); e != nil {
			return before, e
		}
	}
	state, e := placementRuntime(ctx, q.commands)
	if e != nil {
		return state, e
	}
	if state.DatabaseIncarnation != before.DatabaseIncarnation || state.StateEpoch != before.StateEpoch || state.SchemaContract != before.SchemaContract || state.PartialV2Enabled != before.PartialV2Enabled {
		return state, errTrainingPlacementChanged
	}
	return state, nil
}
func (q trainingPlacementPostgresQueries) PlacementDecisionAt(ctx context.Context) (time.Time, error) {
	var now time.Time
	e := q.commands.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now, e
}
func (s PostgresTrainingPlacementStore) ConfirmPlacement(ctx context.Context, r trainingPlacementReviewed) (trainingPlacementSaveResult, error) {
	var out trainingPlacementSaveResult
	outcome, e := db.WithRecipientPublicationTx(ctx, s.Pool, focusedEditTxOptions(), func(ctx context.Context, commands dbgen.DBTX) error {
		var e error
		out, e = saveTrainingPlacement(ctx, trainingPlacementPostgresQueries{dbgen.New(commands), commands}, r)
		return e
	})
	out.CleanupFailed = outcome.CleanupFailed
	if e = trainingPlacementTransactionError(outcome, e); e != nil {
		return trainingPlacementSaveResult{}, e
	}
	return out, nil
}
func (s PostgresTrainingPlacementStore) ConfirmPlacementCancellation(ctx context.Context, r trainingPlacementCancelReviewed) (trainingPlacementSaveResult, error) {
	var out trainingPlacementSaveResult
	outcome, e := db.WithRecipientPublicationTx(ctx, s.Pool, focusedEditTxOptions(), func(ctx context.Context, commands dbgen.DBTX) error {
		var e error
		out, e = saveTrainingPlacementCancellation(ctx, trainingPlacementPostgresQueries{dbgen.New(commands), commands}, r)
		return e
	})
	out.CleanupFailed = outcome.CleanupFailed
	if e = trainingPlacementTransactionError(outcome, e); e != nil {
		return trainingPlacementSaveResult{}, e
	}
	return out, nil
}
