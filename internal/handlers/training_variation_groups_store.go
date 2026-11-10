package handlers

import (
	"context"
	"errors"
	"sort"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (s PostgresStructuredTrainingStore) createDatedVariationGroup(ctx context.Context, in StructuredVariationGroupInput) (dbgen.TrainingVariationGroup, error) {
	var result dbgen.TrainingVariationGroup
	pool, ok := s.Pool.(*pgxpool.Pool)
	if !ok {
		return result, errPublicationSourceUnavailable
	}
	outcome, e := db.WithRecipientPublicationTx(ctx, pool, focusedEditTxOptions(), func(ctx context.Context, commands dbgen.DBTX) error {
		q := dbgen.New(commands)
		adapter := publicationPostgresQueries{commands: commands, queries: q}
		if _, e := adapter.AdmitScheduleWrites(ctx); e != nil {
			return e
		}
		var e error
		result, e = writeDatedVariationGroup(ctx, commands, q, in)
		return e
	})
	e = trainingPlacementTransactionError(outcome, e)
	if errors.Is(e, errTrainingPlacementChanged) {
		e = errStructuredTrainingPublicationConflict
	}
	if errors.Is(e, pgx.ErrNoRows) {
		e = errStructuredVariationMemberScope
	}
	return result, e
}
func writeDatedVariationGroup(ctx context.Context, commands dbgen.DBTX, q *dbgen.Queries, in StructuredVariationGroupInput) (dbgen.TrainingVariationGroup, error) {
	var empty dbgen.TrainingVariationGroup
	if len(in.MembershipIDs) == 0 || len(in.MembershipIDs) > trainingPublicationReviewMaxTuples || in.Params.CreatedByID == uuid.Nil || in.Params.TrainingGroupID == uuid.Nil || !in.Params.EffectiveFrom.Valid {
		return empty, errStructuredVariationMemberScope
	}
	if _, e := getTaskGroup(ctx, q, in.Params.CreatedByID, in.Params.TrainingGroupID); e != nil {
		return empty, e
	}
	members, people := map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
	subjects := []uuid.UUID{}
	for _, member := range in.MembershipIDs {
		if member == uuid.Nil || members[member] {
			return empty, errStructuredVariationMemberScope
		}
		members[member] = true
		var person uuid.UUID
		if e := commands.QueryRow(ctx, `SELECT user_id FROM public.user_memberships WHERE id=$1 AND user_id IS NOT NULL`, member).Scan(&person); e != nil {
			return empty, e
		}
		if people[person] {
			return empty, errStructuredVariationMemberScope
		}
		people[person] = true
		subjects = append(subjects, person)
	}
	sort.Slice(subjects, func(i, j int) bool { return subjects[i].String() < subjects[j].String() })
	actorMode := "SHARE"
	if people[in.Params.CreatedByID] {
		actorMode = "UPDATE"
	}
	var id uuid.UUID
	if e := commands.QueryRow(ctx, `SELECT id FROM public.users WHERE id=$1 AND is_active AND erased_at IS NULL AND NOT is_dependent AND date_of_birth<=((clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date FOR `+actorMode+` NOWAIT`, in.Params.CreatedByID).Scan(&id); e != nil {
		return empty, e
	}
	for _, sql := range []string{`SELECT role_id FROM public.user_platform_roles WHERE user_id=$1 ORDER BY role_id FOR SHARE NOWAIT`, `SELECT id FROM public.staff_grants WHERE user_id=$1 AND capability='COACH' AND revoked_at IS NULL ORDER BY id FOR SHARE NOWAIT`} {
		rows, e := commands.Query(ctx, sql, in.Params.CreatedByID)
		if e != nil {
			return empty, e
		}
		for rows.Next() {
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return empty, e
		}
	}
	for _, person := range subjects {
		if person == in.Params.CreatedByID {
			continue
		}
		if e := commands.QueryRow(ctx, `SELECT id FROM public.users WHERE id=$1 AND is_active AND erased_at IS NULL FOR UPDATE NOWAIT`, person).Scan(&id); e != nil {
			return empty, e
		}
	}
	// All subject fences precede all existing heads; the fixed API changes no head.
	for start := 0; start < len(subjects); start += 20 {
		end := min(start+20, len(subjects))
		if _, e := q.LockTrainingPlacementExistingHeadsForJoin(ctx, dbgen.LockTrainingPlacementExistingHeadsForJoinParams{ActorID: in.Params.CreatedByID, GroupID: in.Params.TrainingGroupID, SubjectIds: subjects[start:end]}); e != nil {
			return empty, e
		}
	}
	if e := commands.QueryRow(ctx, `SELECT id FROM public.training_groups WHERE id=$1 FOR SHARE NOWAIT`, in.Params.TrainingGroupID).Scan(&id); e != nil {
		return empty, e
	}
	if _, e := getTaskGroup(ctx, q, in.Params.CreatedByID, in.Params.TrainingGroupID); e != nil {
		return empty, e
	}
	periodEnd := in.Params.EffectiveUntil
	if !periodEnd.Valid {
		periodEnd = in.Params.EffectiveFrom
		if in.Params.CompetitionEventID != nil {
			var admin bool
			if e := commands.QueryRow(ctx, `SELECT public.guardian_authority_is_administrator($1)`, in.Params.CreatedByID).Scan(&admin); e != nil {
				return empty, e
			}
			events, e := q.ListManagedStructuredCompetitionEvents(ctx, dbgen.ListManagedStructuredCompetitionEventsParams{IsAdmin: admin, UserID: in.Params.CreatedByID})
			if e != nil {
				return empty, e
			}
			found := false
			for _, event := range events {
				if event.ID == *in.Params.CompetitionEventID && event.StartsAt.Valid {
					periodEnd = placementDate(event.StartsAt.Time.In(lisbonLocation()).Format("2006-01-02"))
					found = true
					break
				}
			}
			if !found {
				return empty, errStructuredVariationMemberScope
			}
		}
	}
	if periodEnd.Time.Before(in.Params.EffectiveFrom.Time) {
		return empty, errStructuredVariationMemberScope
	}
	for _, member := range in.MembershipIDs {
		var person uuid.UUID
		e := commands.QueryRow(ctx, `SELECT membership.user_id FROM public.user_memberships membership JOIN public.users subject ON subject.id=membership.user_id
 JOIN LATERAL public.training_placement_resolve(subject.id,$1,membership.season_id,$3) placement ON placement.membership_id=membership.id
 JOIN LATERAL public.training_placement_resolve(subject.id,$1,membership.season_id,$4) ending ON ending.placement_id=placement.placement_id AND ending.membership_id=membership.id
 WHERE membership.id=$2 AND subject.is_active AND subject.erased_at IS NULL
 AND(NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships relationship WHERE relationship.subject_user_id=subject.id AND public.guardian_authority_current(relationship.guardian_user_id,subject.id)))
 AND membership.starts_on<=$3 AND(membership.ends_on IS NULL OR membership.ends_on>=$4)
 FOR SHARE OF membership NOWAIT`, in.Params.TrainingGroupID, member, in.Params.EffectiveFrom, periodEnd).Scan(&person)
		if e != nil {
			return empty, e
		}
	}
	if in.Params.Kind == dbgen.TrainingVariationGroupKindCREW {
		types, e := q.ListStructuredCrewModalities(ctx)
		if e != nil {
			return empty, e
		}
		size, ok := structuredCrewSize(types, in.Params.CraftCode)
		if !ok || size != len(in.MembershipIDs) {
			return empty, errStructuredVariationCrewCapacity
		}
	}
	group, e := q.CreateTrainingVariationGroup(ctx, in.Params)
	if e != nil {
		return empty, e
	}
	for _, member := range in.MembershipIDs {
		n, e := q.AddTrainingVariationGroupMember(ctx, dbgen.AddTrainingVariationGroupMemberParams{VariationGroupID: group.ID, MembershipID: member, AddedByID: in.Params.CreatedByID})
		if e != nil {
			return empty, e
		}
		if n != 1 {
			return empty, errStructuredVariationMemberScope
		}
	}
	return group, nil
}
