package handlers

import (
	"context"
	"errors"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
)

func (h StructuredTraining) publicationCanonicalStatus(ctx context.Context, store recipientPublicationStore, row dbgen.ListManagedTrainingPublicationStatesRow) (pages.StructuredPublicationState, error) {
	source, err := store.ReadPublicationSource(ctx, trainingWriteActor(ctx), row.ID)
	if err != nil {
		return pages.StructuredPublicationState{}, err
	}
	material, err := materializePublicationSource(source, h.location())
	conflict := errors.Is(err, errStructuredTrainingPublicationVariationConflict)
	if err != nil && !errors.Is(err, errPublicationFirstEmpty) && !conflict {
		return pages.StructuredPublicationState{}, err
	}
	if source.Header.LatestRevision != row.PublishedRevision {
		return pages.StructuredPublicationState{}, errPublicationSourceUnavailable
	}
	athletes := map[uuid.UUID]bool{}
	for _, r := range source.EligibleRecipients {
		if r.AthleteUserID == nil {
			return pages.StructuredPublicationState{}, errPublicationSourceUnavailable
		}
		athletes[*r.AthleteUserID] = true
	}
	publishedAt := ""
	if row.PublishedAt.Valid {
		publishedAt = row.PublishedAt.Time.In(h.location()).Format("02/01/2006 15:04")
	}
	if conflict {
		matches := make([]dbgen.ListTrainingVariationMatchesForManagerRow, len(source.VariationMatches))
		for i, row := range source.VariationMatches {
			matches[i] = row.ListTrainingVariationMatchesForManagerRow
			matches[i].Patch = row.Patch
		}
		return pages.StructuredPublicationState{PlanID: row.ID.String(), Plan: stringValue(source.Overview[0].PlanTitle), SourceUpdatedAt: source.Header.SourceUpdatedAt.Time.Format(time.RFC3339Nano), Status: "Variações em conflito", Revision: int(source.Header.LatestRevision), PublishedAt: publishedAt, PublishedBy: stringValue(row.PublishedByName), AthleteCount: len(athletes), PrescriptionCount: len(source.EligibleRecipients), ConflictCount: structuredPublicationConflictCount(matches, row.ID)}, nil
	}
	status := "Rascunho nunca publicado"
	current := source.Header.LatestRevision > 0 && material.NoChanges
	if current {
		status = "Publicado · sem alterações pendentes"
	} else if source.Header.LatestRevision > 0 {
		status = "Rascunho alterado desde a última publicação"
	}
	return pages.StructuredPublicationState{PlanID: source.Header.PlanID.String(), Plan: material.PlanTitle, SourceUpdatedAt: source.Header.SourceUpdatedAt.Time.Format(time.RFC3339Nano), Status: status, Current: current, Revision: int(source.Header.LatestRevision), PublishedAt: publishedAt, PublishedBy: stringValue(row.PublishedByName), AthleteCount: len(athletes), PrescriptionCount: len(material.Prescriptions), AddedCount: material.Added, ChangedCount: material.Changed, RemovedCount: material.Removed, UnchangedCount: material.Unchanged}, nil
}
