package handlers

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestLegacyTrainingRoutesRefuseWritesForPartiallyScheduledWorkout(t *testing.T) {
	day := time.Date(2026, time.November, 12, 0, 0, 0, 0, time.UTC)
	start := day.Add(10 * time.Hour)
	updated := day.Add(-time.Hour)
	session := dbgen.GetTrainingSessionForEditRow{
		ID: uuid.New(), PlanID: uuid.New(), Title: "Treino parcialmente marcado", Status: "ACTIVE",
		SchedulePrecision: string(TrainingScheduleStartOnly),
		ScheduledOn:       pgtype.Date{Time: day, Valid: true}, StartsAt: pgtype.Timestamptz{Time: start, Valid: true},
		UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true},
	}
	store := &trainingWorkflowStore{editSession: session}
	h := Training{Store: store, Location: time.UTC, Now: func() time.Time { return updated }}
	admin := CurrentUser{ID: uuid.New(), IsAdmin: true}
	edit := url.Values{"title": {"Alteração proposta"}, "plan_id": {session.PlanID.String()}, "starts_at": {"2026-11-12T10:00"}, "ends_at": {"2026-11-12T11:00"}, "expected_updated_at": {updated.Format(time.RFC3339Nano)}}
	cancel := url.Values{"cancellation_reason": {"Chuva forte"}, "confirm_cancellation": {"yes"}, "expected_updated_at": {updated.Format(time.RFC3339Nano)}}
	path := "/admin/treinos/sessoes/" + session.ID.String()
	for _, tc := range []struct {
		name, method, path string
		values             url.Values
		handler            http.HandlerFunc
	}{
		{"update", http.MethodPost, path, edit, h.UpdateSession},
		{"cancel", http.MethodPost, path + "/cancelar", cancel, h.CancelSession},
		{"cancel form", http.MethodGet, path + "/cancelar", nil, h.CancelSessionPage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := performTrainingRequest(t, admin, tc.method, tc.path, tc.values, "id", session.ID.String(), tc.handler)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "Nada foi guardado.") || response.Header().Get("Location") != "" {
				t.Fatalf("partially scheduled workout returned status %d with redirect %q", response.Code, response.Header().Get("Location"))
			}
			if store.updateCalls != 0 || store.cancelled.ID != uuid.Nil {
				t.Fatal("partially scheduled workout reached the exact-only writer")
			}
		})
	}
}

func TestLegacyTrainingCreateRejectsPrecisionFieldAndDisappearedPlan(t *testing.T) {
	plan := uuid.New()
	admin := CurrentUser{ID: uuid.New(), IsAdmin: true}
	store := &trainingWorkflowStore{createSessionErr: errTrainingScheduleUnavailable}
	h := Training{Store: store, Location: time.UTC}
	values := url.Values{"title": {"Treino proposto"}, "plan_id": {plan.String()}, "starts_at": {"2026-11-12T10:00"}, "ends_at": {"2026-11-12T11:00"}, "schedule_precision": {"START_ONLY"}}
	path := "/admin/treinos/sessoes"
	response := performTrainingRequest(t, admin, http.MethodPost, path, values, "", "", h.CreateSession)
	if response.Code != http.StatusUnprocessableEntity || store.createdSession.PlanID != uuid.Nil {
		t.Fatalf("precision field reached compatibility create, status %d", response.Code)
	}
	values.Del("schedule_precision")
	response = performTrainingRequest(t, admin, http.MethodPost, path, values, "", "", h.CreateSession)
	if response.Code != http.StatusNotFound || store.createdSession.PlanID != plan || response.Header().Get("Location") != "" {
		t.Fatalf("disappeared plan returned status %d and redirect %q", response.Code, response.Header().Get("Location"))
	}
}
