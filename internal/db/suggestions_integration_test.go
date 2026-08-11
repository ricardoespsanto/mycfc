//go:build integration

package db

import (
	"context"
	"os"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestSuggestionsRemainRequesterScopedAndUseOptimisticTriage(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	queries := dbgen.New(tx)

	requesterID, otherID, moderatorID := uuid.New(), uuid.New(), uuid.New()
	for id, name := range map[uuid.UUID]string{requesterID: "Membro", otherID: "Outro membro", moderatorID: "Moderadora"} {
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, $2, $3, 'hash', '1990-01-01')`, id, name, "suggestion-"+uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}

	created, err := queries.CreateSuggestion(ctx, dbgen.CreateSuggestionParams{RequesterID: requesterID, Category: "FACILITIES", Subject: "Mais cacifos", Description: "Adicionar cacifos junto ao balneário principal."})
	if err != nil {
		t.Fatal(err)
	}
	requesterItems, err := queries.ListSuggestionsForRequester(ctx, dbgen.ListSuggestionsForRequesterParams{RequesterID: requesterID, RowLimit: 10})
	if err != nil || len(requesterItems) != 1 || requesterItems[0].ID != created.ID {
		t.Fatalf("requester items = %#v, err = %v", requesterItems, err)
	}
	otherItems, err := queries.ListSuggestionsForRequester(ctx, dbgen.ListSuggestionsForRequesterParams{RequesterID: otherID, RowLimit: 10})
	if err != nil || len(otherItems) != 0 {
		t.Fatalf("other items = %#v, err = %v", otherItems, err)
	}

	response := "Incluída na próxima revisão das instalações."
	changed, err := queries.UpdateSuggestionTriage(ctx, dbgen.UpdateSuggestionTriageParams{Status: "PLANNED", StaffResponse: &response, ActorUserID: moderatorID, ID: created.ID, ExpectedUpdatedAt: created.UpdatedAt})
	if err != nil || changed != 1 {
		t.Fatalf("changed = %d, err = %v", changed, err)
	}
	stale, err := queries.UpdateSuggestionTriage(ctx, dbgen.UpdateSuggestionTriageParams{Status: "UNDER_REVIEW", ActorUserID: moderatorID, ID: created.ID, ExpectedUpdatedAt: created.UpdatedAt})
	if err != nil || stale != 0 {
		t.Fatalf("stale = %d, err = %v", stale, err)
	}

	statusFilter := "PLANNED"
	triage, err := queries.ListSuggestionsForTriage(ctx, dbgen.ListSuggestionsForTriageParams{StatusFilter: &statusFilter, RowLimit: 10})
	if err != nil || len(triage) != 1 || triage[0].RequesterName != "Membro" || triage[0].StaffResponse == nil || *triage[0].StaffResponse != response {
		t.Fatalf("triage = %#v, err = %v", triage, err)
	}

	currentUpdatedAt := triage[0].UpdatedAt
	if _, err := queries.UpdateSuggestionTriage(ctx, dbgen.UpdateSuggestionTriageParams{Status: "COMPLETED", ActorUserID: moderatorID, ID: created.ID, ExpectedUpdatedAt: pgtype.Timestamptz{Time: currentUpdatedAt.Time, Valid: true}}); err == nil {
		t.Fatal("completed suggestion without response unexpectedly succeeded")
	}
	if !currentUpdatedAt.Valid || !currentUpdatedAt.Time.After(time.Time{}) {
		t.Fatalf("updated at = %#v", currentUpdatedAt)
	}
}
