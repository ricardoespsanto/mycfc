package handlers

import (
	"errors"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
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
