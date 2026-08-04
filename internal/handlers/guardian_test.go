package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestGuardianDashboardRendersDependentsAndDeduplicatedCalendars(t *testing.T) {
	guardianID := uuid.New()
	store := &guardianDashboardStore{dependents: []dbgen.ListDependentsByGuardianRow{
		{Name: "Ana", DateOfBirth: pgtype.Date{Time: time.Date(2014, 7, 25, 0, 0, 0, 0, time.UTC), Valid: true}},
		{Name: "Bruno", DateOfBirth: pgtype.Date{Time: time.Date(2010, 7, 24, 0, 0, 0, 0, time.UTC), Valid: true}},
	}}
	dashboard := guardianDashboard(store, &guardianDependentStoreFake{})
	response := guardianResponse(t, dashboard.Guardian, guardianID, nil)
	body := response.Body.String()
	for _, want := range []string{"Ana", "11 anos", "Bruno", "16 anos", `href="https://example.test/responsabilidade"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAddDependentValidatesBeforeStore(t *testing.T) {
	store := &guardianDependentStoreFake{}
	dashboard := guardianDashboard(&guardianDashboardStore{}, store)
	form := validDependentForm()
	form.Set("date_of_birth", "2000-01-01")
	form.Del("accept_minor_responsibility")
	response := guardianResponse(t, dashboard.AddDependent, uuid.New(), form)
	if response.Code != http.StatusUnprocessableEntity || store.called {
		t.Fatalf("response = %d, called = %t", response.Code, store.called)
	}
	for _, want := range []string{"tem de ter menos de 18 anos", "Tem de aceitar a responsabilidade"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAddDependentPreservesAcceptedResponsibilityOnValidationError(t *testing.T) {
	store := &guardianDependentStoreFake{}
	dashboard := guardianDashboard(&guardianDashboardStore{}, store)
	form := validDependentForm()
	form.Set("name", "X")
	response := guardianResponse(t, dashboard.AddDependent, uuid.New(), form)
	if response.Code != http.StatusUnprocessableEntity || store.called {
		t.Fatalf("response = %d, called = %t", response.Code, store.called)
	}
	if !strings.Contains(response.Body.String(), `id="accept_minor_responsibility" name="accept_minor_responsibility" type="checkbox" required checked`) {
		t.Fatalf("accepted responsibility was not preserved: %q", response.Body.String())
	}
}

func TestAddDependentCreatesFromCurrentGuardianAndRedirects(t *testing.T) {
	guardianID := uuid.New()
	store := &guardianDependentStoreFake{}
	dashboard := guardianDashboard(&guardianDashboardStore{}, store)
	response := guardianResponse(t, dashboard.AddDependent, guardianID, validDependentForm())
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/dashboard/guardian" {
		t.Fatalf("response = %d %q", response.Code, response.Header().Get("Location"))
	}
	if !store.called || store.input.GuardianID != guardianID || store.input.Name != "Maria Silva" {
		t.Fatalf("input = %+v", store.input)
	}
	if store.input.ResponsibilityVersion != "1.0" || store.input.ResponsibilitySHA256 != strings.Repeat("c", 64) {
		t.Fatalf("consent input = %+v", store.input)
	}
}

func TestAddDependentReportsMaximumAndSupportsHTMX(t *testing.T) {
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{err: ErrMaximumDependents})
	response := guardianResponse(t, dashboard.AddDependent, uuid.New(), validDependentForm())
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), maximumDependentsMessage) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}

	dashboard = guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	response = guardianResponse(t, dashboard.AddDependent, uuid.New(), validDependentForm())
	if response.Code != http.StatusSeeOther {
		t.Fatalf("normal response = %d", response.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/guardian/add-dependent", strings.NewReader(validDependentForm().Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	ctx := context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Name: "Guardião"})
	response = httptest.NewRecorder()
	guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{}).AddDependent(response, request.WithContext(ctx))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="guardian-content"`) || !strings.Contains(response.Body.String(), "Menor a cargo adicionado.") {
		t.Fatalf("HTMX response = %d %q", response.Code, response.Body.String())
	}
}

func guardianDashboard(store DashboardStore, dependents GuardianDependentStore) Dashboard {
	return Dashboard{
		Store: store, Dependents: dependents, Location: time.UTC, Now: func() time.Time { return time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC) },
		PageMeta:      components.PageMeta{StylesheetURL: "/assets/app.css", ScriptURL: "/assets/app.js"},
		CompetitionID: "competition", TrainingID: "training", SocialID: "social", CleanupsID: "cleanups",
		ResponsibilityVersion: "1.0", ResponsibilitySHA256: strings.Repeat("c", 64), ResponsibilityURL: "https://example.test/responsabilidade",
	}
}

func validDependentForm() url.Values {
	return url.Values{"name": {"  Maria   Silva "}, "date_of_birth": {"2010-07-24"}, "accept_minor_responsibility": {"on"}}
}

func guardianResponse(t *testing.T, handler http.HandlerFunc, guardianID uuid.UUID, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	method, body := http.MethodGet, ""
	if form != nil {
		method, body = http.MethodPost, form.Encode()
	}
	request := httptest.NewRequest(method, "/dashboard/guardian", strings.NewReader(body))
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	ctx := context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: guardianID, Name: "Guardião"})
	response := httptest.NewRecorder()
	handler(response, request.WithContext(ctx))
	return response
}

type guardianDependentStoreFake struct {
	input  GuardianDependentInput
	err    error
	called bool
}

func (s *guardianDependentStoreFake) CreateDependent(_ context.Context, input GuardianDependentInput) error {
	s.called = true
	s.input = input
	return s.err
}

type guardianDashboardStore struct {
	dependents []dbgen.ListDependentsByGuardianRow
	err        error
}

func (s *guardianDashboardStore) ListRecentPerformanceMetrics(context.Context, dbgen.ListRecentPerformanceMetricsParams) ([]dbgen.PerformanceMetric, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListRecentTrainingLogs(context.Context, dbgen.ListRecentTrainingLogsParams) ([]dbgen.TrainingLog, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListPublishedNews(context.Context, int32) ([]dbgen.NewsItem, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListWhatsAppGroupsForUserProgramme(context.Context, dbgen.ListWhatsAppGroupsForUserProgrammeParams) ([]dbgen.WhatsappGroup, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListDependentsByGuardian(context.Context, dbgen.ListDependentsByGuardianParams) ([]dbgen.ListDependentsByGuardianRow, error) {
	return s.dependents, s.err
}
func (s *guardianDashboardStore) ListOperationalEquipment(context.Context, int32) ([]dbgen.Equipment, error) {
	return nil, nil
}
func (s *guardianDashboardStore) ListEventsForToday(context.Context, dbgen.ListEventsForTodayParams) ([]dbgen.ListEventsForTodayRow, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListVisibleAnnouncements(context.Context, dbgen.ListVisibleAnnouncementsParams) ([]dbgen.ListVisibleAnnouncementsRow, error) {
	return nil, errors.New("not used")
}
