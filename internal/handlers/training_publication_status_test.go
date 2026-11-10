package handlers

import (
	"errors"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPublicationStatusRejectsRevokedOrChangedWeek(t *testing.T) {
	for _, tc := range []struct {
		name     string
		readErr  error
		revision int32
		want     error
	}{
		{"revoked scope", pgx.ErrNoRows, 0, pgx.ErrNoRows},
		{"listing was superseded", nil, 2, errPublicationSourceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := publicationSourceFixture()
			store := &publicationHTTPStoreFake{source: source, readErr: tc.readErr}
			row := dbgen.ListManagedTrainingPublicationStatesRow{ID: source.Header.PlanID, PublishedRevision: tc.revision}
			state, err := (StructuredTraining{Location: time.UTC}).publicationCanonicalStatus(t.Context(), store, row)
			if !errors.Is(err, tc.want) || state.PlanID != "" || state.AthleteCount != 0 || store.readCalls != 1 {
				t.Fatalf("stale or revoked status was exposed: %+v, %v", state, err)
			}
		})
	}
}

func TestPublicationStatusReportsPeerVariationConflictWithoutPreview(t *testing.T) {
	source := instructionReviewFixture()
	peer := source.VariationMatches[0]
	peer.VariationID = uuid.New()
	definition := source.VariationDefinitions[0]
	definition.VariationID = peer.VariationID
	source.VariationDefinitions = append(source.VariationDefinitions, definition)
	source.VariationMatches = append(source.VariationMatches, peer)
	store := &publicationHTTPStoreFake{source: source}
	row := dbgen.ListManagedTrainingPublicationStatesRow{
		ID: source.Header.PlanID, PublishedAt: source.Header.SourceUpdatedAt,
	}
	state, err := (StructuredTraining{Location: time.UTC}).publicationCanonicalStatus(t.Context(), store, row)
	if err != nil || state.Status != "Variações em conflito" || state.Current || state.ConflictCount != 1 || state.PublishedAt == "" {
		t.Fatalf("conflicting sources appeared publishable: %+v, %v", state, err)
	}
}

func TestPublicationStatusKeepsChangedDraftSeparateFromSavedRevision(t *testing.T) {
	source := publicationSourceFixture()
	prior, err := materializePublicationSource(source, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	saved := prior.Prescriptions[0]
	source.Header.LatestRevision = 1
	source.Header.LatestPublicationID = testUUIDPtr(uuid.New())
	source.LatestPrescriptions = []publicationHashTuple{{
		PlacementID: &saved.PlacementID, SessionID: saved.SessionID, MembershipID: saved.MembershipID,
		AthleteUserID: &saved.AthleteUserID, SnapshotSHA256: saved.SnapshotSHA256,
	}}
	changedTitle := "Treino alterado no rascunho"
	source.Overview[0].SessionTitle = &changedTitle
	store := &publicationHTTPStoreFake{source: source}
	row := dbgen.ListManagedTrainingPublicationStatesRow{ID: source.Header.PlanID, PublishedRevision: 1}
	state, err := (StructuredTraining{Location: time.UTC}).publicationCanonicalStatus(t.Context(), store, row)
	if err != nil || state.Current || state.Revision != 1 || state.ChangedCount != 1 || state.Status != "Rascunho alterado desde a última publicação" {
		t.Fatalf("pending draft obscured the saved revision: %+v, %v", state, err)
	}
}
