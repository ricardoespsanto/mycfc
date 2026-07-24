package handlers

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestDashboardRoleShellsRenderOnlyRelevantNavigation(t *testing.T) {
	dashboard := Dashboard{Store: &dashboardStoreFake{}, PageMeta: components.PageMeta{StylesheetURL: "/assets/app.css", ScriptURL: "/assets/app.js"}, CompetitionID: "competition", TrainingID: "training", SocialID: "social", CleanupsID: "cleanups"}
	for _, tc := range []struct {
		name, role, absent, present string
		handler                     http.HandlerFunc
	}{
		{"competitor", "Competitor", "Frota", "Competições", dashboard.Competitor},
		{"leisure", "Leisure", "Competidor", "Eventos sociais", dashboard.Leisure},
		{"guardian", "Guardian", "Frota", "Adicione o primeiro menor", dashboard.Guardian},
		{"admin", "Admin", "Menores a cargo", "Frota", dashboard.Admin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
			ctx := context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Name: "Maria Silva", Role: tc.role})
			response := httptest.NewRecorder()
			tc.handler(response, request.WithContext(ctx))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d", response.Code)
			}
			body := response.Body.String()
			if !strings.Contains(body, tc.present) || strings.Contains(body, tc.absent) {
				t.Fatalf("unexpected dashboard content: %q", body)
			}
		})
	}
}

func TestDashboardCompetitorRendersDatabaseContent(t *testing.T) {
	memberID := uuid.New()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	store := &dashboardStoreFake{
		metrics: []dbgen.PerformanceMetric{{LabelPt: "Peso", Value: pgtype.Numeric{Int: big.NewInt(725), Exp: -1, Valid: true}, UnitPt: "kg", MeasuredAt: pgtype.Timestamptz{Time: now, Valid: true}}},
		logs:    []dbgen.TrainingLog{{DurationSeconds: 5400, DistanceMetres: 12500, Notes: "Série longa", OccurredAt: pgtype.Timestamptz{Time: now, Valid: true}}},
		groups:  []dbgen.WhatsappGroup{{Name: "Seniores", Discipline: "Remo", Url: "https://chat.whatsapp.com/seniores"}},
	}
	dashboard := Dashboard{Store: store, PageMeta: components.PageMeta{StylesheetURL: "/assets/app.css", ScriptURL: "/assets/app.js"}}
	response := dashboardResponse(t, dashboard.Competitor, memberID, "Competitor")
	body := response.Body.String()
	for _, want := range []string{"Peso", "72.5 kg", "Treinos recentes", "Série longa", `href="https://chat.whatsapp.com/seniores"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %q", want, body)
		}
	}
	if !store.deadlineSeen || store.metricsParams.UserID != memberID || store.metricsParams.RowLimit != 6 || store.logsParams.RowLimit != 10 {
		t.Fatalf("unexpected competitor queries: %+v", store)
	}
}

func TestDashboardLeisureRendersDatabaseContent(t *testing.T) {
	url := "https://example.com/noticia"
	dashboard := Dashboard{Store: &dashboardStoreFake{
		news:   []dbgen.NewsItem{{TitlePt: "Regata", SummaryPt: "Inscrições abertas", Url: &url}},
		groups: []dbgen.WhatsappGroup{{Name: "Lazer", Discipline: "Canoagem", Url: "https://chat.whatsapp.com/lazer"}},
	}, PageMeta: components.PageMeta{StylesheetURL: "/assets/app.css", ScriptURL: "/assets/app.js"}}
	response := dashboardResponse(t, dashboard.Leisure, uuid.New(), "Leisure")
	body := response.Body.String()
	for _, want := range []string{`href="https://example.com/noticia"`, "Inscrições abertas", `href="https://chat.whatsapp.com/lazer"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %q", want, body)
		}
	}
}

func TestDashboardReturnsInternalErrorForRequiredQueryFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store dashboardStoreFake
		role  string
	}{
		{name: "competitor metrics", store: dashboardStoreFake{metricsErr: errors.New("down")}, role: "Competitor"},
		{name: "competitor training logs", store: dashboardStoreFake{logsErr: errors.New("down")}, role: "Competitor"},
		{name: "competitor groups", store: dashboardStoreFake{groupsErr: errors.New("down")}, role: "Competitor"},
		{name: "leisure news", store: dashboardStoreFake{newsErr: errors.New("down")}, role: "Leisure"},
		{name: "leisure groups", store: dashboardStoreFake{groupsErr: errors.New("down")}, role: "Leisure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dashboard := Dashboard{Store: &tc.store}
			var handler http.HandlerFunc
			if tc.role == "Competitor" {
				handler = dashboard.Competitor
			} else {
				handler = dashboard.Leisure
			}
			response := dashboardResponse(t, handler, uuid.New(), tc.role)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d", response.Code)
			}
		})
	}
}

func dashboardResponse(t *testing.T, handler http.HandlerFunc, userID uuid.UUID, role string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	ctx := context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: userID, Name: "Maria Silva", Role: role})
	response := httptest.NewRecorder()
	handler(response, request.WithContext(ctx))
	return response
}

type dashboardStoreFake struct {
	metrics       []dbgen.PerformanceMetric
	logs          []dbgen.TrainingLog
	news          []dbgen.NewsItem
	groups        []dbgen.WhatsappGroup
	dependents    []dbgen.ListDependentsByGuardianRow
	metricsErr    error
	logsErr       error
	newsErr       error
	groupsErr     error
	metricsParams dbgen.ListRecentPerformanceMetricsParams
	logsParams    dbgen.ListRecentTrainingLogsParams
	deadlineSeen  bool
}

func (f *dashboardStoreFake) ListOperationalEquipment(_ context.Context, _ int32) ([]dbgen.Equipment, error) {
	return nil, nil
}

func (f *dashboardStoreFake) ListRecentPerformanceMetrics(ctx context.Context, params dbgen.ListRecentPerformanceMetricsParams) ([]dbgen.PerformanceMetric, error) {
	_, f.deadlineSeen = ctx.Deadline()
	f.metricsParams = params
	return f.metrics, f.metricsErr
}

func (f *dashboardStoreFake) ListRecentTrainingLogs(_ context.Context, params dbgen.ListRecentTrainingLogsParams) ([]dbgen.TrainingLog, error) {
	f.logsParams = params
	return f.logs, f.logsErr
}

func (f *dashboardStoreFake) ListPublishedNews(_ context.Context, _ int32) ([]dbgen.NewsItem, error) {
	return f.news, f.newsErr
}

func (f *dashboardStoreFake) ListWhatsAppGroupsForRole(_ context.Context, _ dbgen.ListWhatsAppGroupsForRoleParams) ([]dbgen.WhatsappGroup, error) {
	return f.groups, f.groupsErr
}

func (f *dashboardStoreFake) ListDependentsByGuardian(_ context.Context, _ dbgen.ListDependentsByGuardianParams) ([]dbgen.ListDependentsByGuardianRow, error) {
	return f.dependents, nil
}
