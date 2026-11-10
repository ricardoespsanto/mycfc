package handlers

import (
	"context"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

type publicationRhythmQueries interface {
	ListPublicationRhythmProfiles(context.Context, dbgen.ListPublicationRhythmProfilesParams) ([]dbgen.WaterIntensityProfile, error)
	LockPublicationRhythmProfiles(context.Context, dbgen.LockPublicationRhythmProfilesParams) ([]dbgen.WaterIntensityProfile, error)
	ListPublicationRhythmZones(context.Context, dbgen.ListPublicationRhythmZonesParams) ([]dbgen.WaterIntensityZone, error)
	PublicationRecipientCrafts(context.Context, dbgen.PublicationRecipientCraftsParams) ([]dbgen.PublicationRecipientCraftsRow, error)
	LockPublicationRecipientCrafts(context.Context, dbgen.LockPublicationRecipientCraftsParams) ([]dbgen.LockPublicationRecipientCraftsRow, error)
}

func readPublicationRhythm(ctx context.Context, q publicationRhythmQueries, actor uuid.UUID, s publicationSourceEnvelope, locked bool) (*publicationRhythmFacts, error) {
	ids, e := publicationRhythmProfileIDs(s)
	if e != nil {
		return nil, e
	}
	facts := publicationRhythmFacts{Profiles: []dbgen.WaterIntensityProfile{}, Zones: []dbgen.WaterIntensityZone{}, Crafts: []publicationRhythmCraft{}}
	if len(ids) > 0 {
		if locked {
			facts.Profiles, e = q.LockPublicationRhythmProfiles(ctx, dbgen.LockPublicationRhythmProfilesParams{ActorID: actor, PlanID: s.Header.PlanID, ProfileIds: ids})
		} else {
			facts.Profiles, e = q.ListPublicationRhythmProfiles(ctx, dbgen.ListPublicationRhythmProfilesParams{ActorID: actor, PlanID: s.Header.PlanID, ProfileIds: ids})
		}
		if e != nil {
			return nil, publicationNowaitError(e)
		}
		if len(facts.Profiles) > 32 {
			return nil, errTrainingPublicationReviewLimit
		}
		for i := range facts.Profiles {
			p := &facts.Profiles[i]
			p.CreatedAt.Time = p.CreatedAt.Time.UTC().Truncate(time.Microsecond)
		}
		facts.Zones, e = q.ListPublicationRhythmZones(ctx, dbgen.ListPublicationRhythmZonesParams{ActorID: actor, PlanID: s.Header.PlanID, ProfileIds: ids})
		if e != nil {
			return nil, e
		}
	}
	var permissions []dbgen.LockPublicationRecipientCraftsRow
	if locked {
		permissions, e = q.LockPublicationRecipientCrafts(ctx, dbgen.LockPublicationRecipientCraftsParams{ActorID: actor, PlanID: s.Header.PlanID})
		if e != nil {
			return nil, publicationNowaitError(e)
		}
		if len(permissions) > 32768 {
			return nil, errTrainingPublicationReviewLimit
		}
	}
	rows, e := q.PublicationRecipientCrafts(ctx, dbgen.PublicationRecipientCraftsParams{ActorID: actor, PlanID: s.Header.PlanID})
	if e != nil {
		return nil, e
	}
	for _, r := range rows {
		facts.Crafts = append(facts.Crafts, publicationRhythmCraft{SubjectID: r.SubjectID, Kayak: r.Kayak, Canoe: r.Canoe})
	}
	if e = validatePublicationRhythmFacts(s, facts); e != nil {
		return nil, e
	}
	if locked && !publicationRhythmPermissionsMatch(facts, permissions) {
		return nil, errStructuredTrainingPublicationConflict
	}
	return &facts, nil
}
func publicationRhythmPermissionsMatch(f publicationRhythmFacts, rows []dbgen.LockPublicationRecipientCraftsRow) bool {
	type permission struct{ canoeing, polo, k, c bool }
	current := map[uuid.UUID]permission{}
	seen := map[string]bool{}
	for _, r := range f.Crafts {
		current[r.SubjectID] = permission{}
	}
	for _, r := range rows {
		p, exists := current[r.UserID]
		key := r.UserID.String() + ":" + r.PermissionKind + ":" + r.PermissionCode
		if !exists || seen[key] {
			return false
		}
		seen[key] = true
		switch r.PermissionKind {
		case "MODALITY":
			switch r.PermissionCode {
			case "CANOEING":
				p.canoeing = true
			case "KAYAK_POLO":
				p.polo = true
			default:
				return false
			}
		case "CRAFT":
			switch r.PermissionCode {
			case "K1", "K2", "K4":
				p.k = true
			case "C1", "C2", "C4":
				p.c = true
			default:
				return false
			}
		default:
			return false
		}
		current[r.UserID] = p
	}
	for _, r := range f.Crafts {
		p := current[r.SubjectID]
		if r.Kayak != (p.polo || p.canoeing && p.k) || r.Canoe != (p.canoeing && p.c) {
			return false
		}
	}
	return true
}
func samePublicationRhythmHeader(a, b publicationPlanHeader) bool {
	return a.PlanID == b.PlanID && a.GroupID == b.GroupID && publicationSameOptionalID(a.SeasonID, b.SeasonID) && a.WeekStart == b.WeekStart && a.SourceUpdatedAt.Valid && b.SourceUpdatedAt.Valid && a.SourceUpdatedAt.Time.Equal(b.SourceUpdatedAt.Time) && a.LatestRevision == b.LatestRevision && publicationSameOptionalID(a.LatestPublicationID, b.LatestPublicationID)
}
