package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type featureFlagStoreFake struct {
	rows       []dbgen.ListFeatureFlagsRow
	events     []dbgen.ListFeatureFlagEventsRow
	updated    dbgen.UpdateFeatureFlagParams
	updateRows int64
	err        error
}

func (f *featureFlagStoreFake) ListFeatureFlags(context.Context) ([]dbgen.ListFeatureFlagsRow, error) {
	return f.rows, f.err
}
func (f *featureFlagStoreFake) ListFeatureFlagEvents(context.Context, int32) ([]dbgen.ListFeatureFlagEventsRow, error) {
	return f.events, f.err
}
func (f *featureFlagStoreFake) UpdateFeatureFlag(_ context.Context, params dbgen.UpdateFeatureFlagParams) (int64, error) {
	f.updated = params
	return f.updateRows, f.err
}

func TestSystemPageRendersRegisteredFeatureControlsAndAudit(t *testing.T) {
	now := time.Date(2026, 8, 12, 16, 0, 0, 0, time.UTC)
	actor := uuid.New()
	actorName := "Beatriz"
	store := &featureFlagStoreFake{
		rows: []dbgen.ListFeatureFlagsRow{
			{FeatureKey: "suggestions", Mode: "ENABLED", UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}, UpdatedByID: &actor, UpdatedByName: &actorName},
			{FeatureKey: "photo_submissions", Mode: "DISABLED", UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}},
		},
		events: []dbgen.ListFeatureFlagEventsRow{{FeatureKey: "suggestions", PreviousMode: "ADMIN_ONLY", NewMode: "ENABLED", ActorName: "Beatriz", OccurredAt: pgtype.Timestamptz{Time: now, Valid: true}}},
	}
	h := Dashboard{Features: store, Location: time.UTC, PageMeta: components.PageMeta{StylesheetURL: "/assets/app.css", ScriptURL: "/assets/app.js"}}
	response := featureFlagResponse(t, h.ReleasesPage, http.MethodGet, "/admin/sistema", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	for _, want := range []string{"Funcionalidades", "Sugestões", "Envio de fotografias para álbuns", "Só administradores", `action="/admin/sistema/funcionalidades/suggestions"`, `name="expected_updated_at"`, "Beatriz", "Alterações recentes"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("system page does not contain %q", want)
		}
	}
}

func TestFeatureFlagUpdateValidatesAndUsesOptimisticConcurrency(t *testing.T) {
	now := time.Date(2026, 8, 12, 16, 0, 0, 0, time.UTC)
	store := &featureFlagStoreFake{rows: []dbgen.ListFeatureFlagsRow{{FeatureKey: "suggestions", Mode: "ENABLED", UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, {FeatureKey: "photo_submissions", Mode: "DISABLED", UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}}, updateRows: 1}
	h := Dashboard{Features: store, Location: time.UTC}
	values := url.Values{"mode": {"ADMIN_ONLY"}, "expected_updated_at": {now.Format(time.RFC3339Nano)}}
	response := featureFlagResponse(t, h.UpdateFeatureFlag, http.MethodPost, "/admin/sistema/funcionalidades/suggestions", values)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/sistema" {
		t.Fatalf("response = %d %q", response.Code, response.Header().Get("Location"))
	}
	if store.updated.FeatureKey != "suggestions" || store.updated.Mode != dbgen.FeatureAvailabilityModeADMINONLY || !store.updated.ExpectedUpdatedAt.Time.Equal(now) || store.updated.ActorUserID == nil {
		t.Fatalf("update params = %+v", store.updated)
	}

	values.Set("mode", "UNKNOWN")
	response = featureFlagResponse(t, h.UpdateFeatureFlag, http.MethodPost, "/admin/sistema/funcionalidades/suggestions", values)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "Selecione uma disponibilidade válida") {
		t.Fatalf("invalid response = %d %q", response.Code, response.Body.String())
	}

	store.updateRows = 0
	values.Set("mode", "DISABLED")
	response = featureFlagResponse(t, h.UpdateFeatureFlag, http.MethodPost, "/admin/sistema/funcionalidades/suggestions", values)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "alterada por outra pessoa") {
		t.Fatalf("conflict response = %d %q", response.Code, response.Body.String())
	}
}

func TestFeatureFlagUpdateRejectsUnknownRegistryKey(t *testing.T) {
	h := Dashboard{Features: &featureFlagStoreFake{}}
	response := featureFlagResponse(t, h.UpdateFeatureFlag, http.MethodPost, "/admin/sistema/funcionalidades/unknown", url.Values{})
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
}

func featureFlagResponse(t *testing.T, handler http.HandlerFunc, method, target string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if values == nil {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(values.Encode())
	}
	request := httptest.NewRequest(method, target, body)
	if values != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	request.SetPathValue("key", strings.TrimPrefix(target, "/admin/sistema/funcionalidades/"))
	user := CurrentUser{ID: uuid.New(), Name: "Beatriz", IsAdmin: true}
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, user))
	response := httptest.NewRecorder()
	handler(response, request)
	return response
}
