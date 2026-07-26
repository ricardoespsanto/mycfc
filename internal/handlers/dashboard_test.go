package handlers

import (
	"context"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestDashboardRoleShellsRenderOnlyRelevantNavigation(t *testing.T) {
	store := &dashboardStoreFake{}
	dashboard := Dashboard{Store: store, Fleet: store, PageMeta: components.PageMeta{StylesheetURL: "/assets/app.css", ScriptURL: "/assets/app.js"}, CompetitionID: "competition", TrainingID: "training", SocialID: "social", CleanupsID: "cleanups"}
	for _, tc := range []struct {
		name    string
		present []string
		absent  []string
		user    CurrentUser
		handler http.HandlerFunc
	}{
		{"competition athlete", []string{"Hoje", "Atleta de competição"}, []string{"Atleta de iniciação", "Atleta de kayak polo", "Treinador", "Moderador", "Frota"}, CurrentUser{Programmes: map[string]bool{"Competition": true}}, dashboard.Competition},
		{"multiple memberships", []string{"Lazer", "Atleta de iniciação", "Atleta de competição", "Atleta de kayak polo"}, nil, CurrentUser{Programmes: map[string]bool{"Leisure": true, "Initiation": true, "Competition": true, "Kayak_Polo": true}}, dashboard.Competition},
		{"active staff grants", []string{"Treinador", "Moderador"}, []string{"Frota"}, CurrentUser{CanManageEvents: true, CanModerateContent: true}, dashboard.Coach},
		{"admin", []string{"Frota"}, []string{"Treinador", "Moderador"}, CurrentUser{IsAdmin: true}, dashboard.Admin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
			tc.user.ID, tc.user.Name = uuid.New(), "Maria Silva"
			ctx := context.WithValue(request.Context(), currentUserKey{}, tc.user)
			response := httptest.NewRecorder()
			tc.handler(response, request.WithContext(ctx))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d", response.Code)
			}
			body := response.Body.String()
			for _, label := range tc.present {
				if !strings.Contains(body, label) {
					t.Fatalf("dashboard does not contain %q: %q", label, body)
				}
			}
			for _, label := range tc.absent {
				if strings.Contains(body, label) {
					t.Fatalf("dashboard unexpectedly contains %q: %q", label, body)
				}
			}
		})
	}
}

func TestDashboardTodayRendersVisibleEventsAndLocalDayBounds(t *testing.T) {
	location := time.FixedZone("WEST", 3600)
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, location)
	store := &dashboardStoreFake{todayEvents: []dbgen.ListEventsForTodayRow{{ID: uuid.New(), Title: "Treino de manhã", StartsAt: pgtype.Timestamptz{Time: now, Valid: true}, EndsAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}}}}
	dashboard := Dashboard{Store: store, Location: location, Now: func() time.Time { return now }, PageMeta: components.PageMeta{StylesheetURL: "/assets/app.css", ScriptURL: "/assets/app.js"}}
	response := dashboardResponse(t, dashboard.Today, uuid.New())
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Treino de manhã") {
		t.Fatalf("unexpected Today response: %d %q", response.Code, response.Body.String())
	}
	if store.todayParams.DayStartsAt.Time != time.Date(2026, 7, 24, 0, 0, 0, 0, location) || store.todayParams.DayEndsAt.Time != time.Date(2026, 7, 25, 0, 0, 0, 0, location) {
		t.Fatalf("unexpected Today bounds: %+v", store.todayParams)
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
	response := dashboardResponse(t, dashboard.Competitor, memberID)
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
	response := dashboardResponse(t, dashboard.Leisure, uuid.New())
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
			response := dashboardResponse(t, handler, uuid.New())
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d", response.Code)
			}
		})
	}
}

func dashboardResponse(t *testing.T, handler http.HandlerFunc, userID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	ctx := context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: userID, Name: "Maria Silva"})
	response := httptest.NewRecorder()
	handler(response, request.WithContext(ctx))
	return response
}

type dashboardStoreFake struct {
	metrics           []dbgen.PerformanceMetric
	logs              []dbgen.TrainingLog
	news              []dbgen.NewsItem
	groups            []dbgen.WhatsappGroup
	dependents        []dbgen.ListDependentsByGuardianRow
	metricsErr        error
	logsErr           error
	newsErr           error
	groupsErr         error
	metricsParams     dbgen.ListRecentPerformanceMetricsParams
	logsParams        dbgen.ListRecentTrainingLogsParams
	deadlineSeen      bool
	scheduleParams    dbgen.ScheduleMaintenanceTaskParams
	scheduleErr       error
	repairParams      dbgen.UpdateRepairStatusParams
	repairErr         error
	completeID        uuid.UUID
	completeErr       error
	counts            []dbgen.CountEquipmentByStatusRow
	adminEquipment    []dbgen.Equipment
	repairs           []dbgen.ListPendingRepairRequestsRow
	maintenance       []dbgen.ListUpcomingMaintenanceRow
	adminLimit        int32
	repairLimit       int32
	maintenanceParams dbgen.ListUpcomingMaintenanceParams
	todayEvents       []dbgen.ListEventsForTodayRow
	todayParams       dbgen.ListEventsForTodayParams
}

func (f *dashboardStoreFake) CountEquipmentByStatus(context.Context) ([]dbgen.CountEquipmentByStatusRow, error) {
	return f.counts, nil
}

func (f *dashboardStoreFake) ListEquipmentForAdmin(_ context.Context, limit int32) ([]dbgen.Equipment, error) {
	f.adminLimit = limit
	return f.adminEquipment, nil
}

func (f *dashboardStoreFake) ListPendingRepairRequests(_ context.Context, limit int32) ([]dbgen.ListPendingRepairRequestsRow, error) {
	f.repairLimit = limit
	return f.repairs, nil
}

func (f *dashboardStoreFake) ListUpcomingMaintenance(_ context.Context, params dbgen.ListUpcomingMaintenanceParams) ([]dbgen.ListUpcomingMaintenanceRow, error) {
	f.maintenanceParams = params
	return f.maintenance, nil
}

func (f *dashboardStoreFake) ScheduleMaintenanceTask(_ context.Context, params dbgen.ScheduleMaintenanceTaskParams) (dbgen.ScheduleMaintenanceTaskRow, error) {
	f.scheduleParams = params
	return dbgen.ScheduleMaintenanceTaskRow{}, f.scheduleErr
}

func (f *dashboardStoreFake) UpdateRepairStatus(_ context.Context, params dbgen.UpdateRepairStatusParams) (dbgen.RepairRequest, error) {
	f.repairParams = params
	return dbgen.RepairRequest{}, f.repairErr
}

func (f *dashboardStoreFake) CompleteMaintenanceTask(_ context.Context, id uuid.UUID) (dbgen.MaintenanceTask, error) {
	f.completeID = id
	return dbgen.MaintenanceTask{}, f.completeErr
}

func TestMaintenanceHTMXValidationAndSuccess(t *testing.T) {
	location := time.FixedZone("WEST", 3600)
	store := &dashboardStoreFake{}
	dashboard := Dashboard{Store: store, Fleet: store, Location: location}
	userID := uuid.New()
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"invalid", "equipment_id=bad&scheduled_for=wrong&description=curta", http.StatusUnprocessableEntity},
		{"success", "equipment_id=" + uuid.NewString() + "&scheduled_for=2026-07-24T14%3A30&description=Substituir%20a%20pe%C3%A7a%20danificada", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/admin/maintenance", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("HX-Request", "true")
			request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: userID, IsAdmin: true}))
			response := httptest.NewRecorder()
			dashboard.Maintenance(response, request)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d", response.Code, tc.status)
			}
		})
	}
	if store.scheduleParams.CreatedByID == nil || *store.scheduleParams.CreatedByID != userID || store.scheduleParams.Description != "Substituir a peça danificada" {
		t.Fatalf("unexpected maintenance parameters: %+v", store.scheduleParams)
	}
	if got := store.scheduleParams.ScheduledFor.Time; got.Location() != location || got.Hour() != 14 || got.Minute() != 30 {
		t.Fatalf("scheduled time = %s", got)
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/maintenance", strings.NewReader("equipment_id="+uuid.NewString()+"&scheduled_for=2026-07-24T14%3A30&description=Substituir%20a%20pe%C3%A7a%20danificada"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: userID, IsAdmin: true}))
	response := httptest.NewRecorder()
	dashboard.Maintenance(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/fleet" {
		t.Fatalf("normal response = %d %q", response.Code, response.Header().Get("Location"))
	}
}

func TestRepairStatusRequiresSequentialTransitionsAndSupportsHTMXAndNormalForms(t *testing.T) {
	repairID := uuid.New()
	store := &dashboardStoreFake{}
	dashboard := Dashboard{Store: store, Fleet: store}
	for _, tc := range []struct {
		name, body, method string
		status             int
		wantStatus         string
		wantLocation       string
		wantBody           string
	}{
		{"invalid transition", "repair_id=" + repairID.String() + "&expected_status=Pendente&status=Resolvido", "HX", http.StatusUnprocessableEntity, "", "", "A alteração de estado não é válida."},
		{"start analysis", "repair_id=" + repairID.String() + "&expected_status=Pendente&status=Em_Analise", "HX", http.StatusOK, "Em_Analise", "", "Pedido de reparação em análise."},
		{"resolve normally", "repair_id=" + repairID.String() + "&expected_status=Em_Analise&status=Resolvido", "normal", http.StatusSeeOther, "Resolvido", "/admin/fleet", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/admin/repairs/status", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.method == "HX" {
				request.Header.Set("HX-Request", "true")
			}
			response := httptest.NewRecorder()
			dashboard.RepairStatus(response, request)
			if response.Code != tc.status || response.Header().Get("Location") != tc.wantLocation || !strings.Contains(response.Body.String(), tc.wantBody) {
				t.Fatalf("response = %d %q %q", response.Code, response.Header().Get("Location"), response.Body.String())
			}
			if tc.wantStatus != "" && (store.repairParams.ID != repairID || store.repairParams.Status != tc.wantStatus) {
				t.Fatalf("repair parameters = %+v", store.repairParams)
			}
		})
	}
	store.repairErr = pgx.ErrNoRows
	request := httptest.NewRequest(http.MethodPost, "/admin/repairs/status", strings.NewReader("repair_id="+repairID.String()+"&expected_status=Pendente&status=Em_Analise"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	dashboard.RepairStatus(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "já foi atualizado") {
		t.Fatalf("conflict response = %d %q", response.Code, response.Body.String())
	}
}

func TestCompleteMaintenanceSupportsHTMXAndNormalForms(t *testing.T) {
	taskID := uuid.New()
	store := &dashboardStoreFake{}
	dashboard := Dashboard{Store: store, Fleet: store}
	for _, htmx := range []bool{true, false} {
		request := httptest.NewRequest(http.MethodPost, "/admin/maintenance/"+taskID.String()+"/complete", strings.NewReader("maintenance_id="+taskID.String()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if htmx {
			request.Header.Set("HX-Request", "true")
		}
		response := httptest.NewRecorder()
		dashboard.CompleteMaintenance(response, request)
		wantStatus := http.StatusSeeOther
		if htmx {
			wantStatus = http.StatusOK
		}
		if response.Code != wantStatus || store.completeID != taskID {
			t.Fatalf("response = %d, complete id = %s", response.Code, store.completeID)
		}
		if htmx && !strings.Contains(response.Body.String(), "Manutenção concluída.") {
			t.Fatalf("success body = %q", response.Body.String())
		}
	}
	store.completeErr = pgx.ErrNoRows
	request := httptest.NewRequest(http.MethodPost, "/admin/maintenance/"+taskID.String()+"/complete", strings.NewReader("maintenance_id="+taskID.String()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	dashboard.CompleteMaintenance(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "já foi concluída") {
		t.Fatalf("conflict response = %d %q", response.Code, response.Body.String())
	}
}

func TestAdminFleetCapsEquipmentAndPresignsDisplayedRepairPhotos(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	key, contentType := "repairs/2026/07/photo.jpg", "image/jpeg"
	equipment := make([]dbgen.Equipment, 501)
	for i := range equipment {
		equipment[i] = dbgen.Equipment{ID: uuid.New(), AssetTag: "EQ", Name: "Barco", Type: "Boat", Status: "Operational"}
	}
	store := &dashboardStoreFake{counts: []dbgen.CountEquipmentByStatusRow{{Status: "Operational", Total: 501}}, adminEquipment: equipment, repairs: []dbgen.ListPendingRepairRequestsRow{{ID: uuid.New(), AssetTag: "EQ", EquipmentName: "Barco", IssueDescription: "Casco danificado", Status: "Pendente", ImageObjectKey: &key, ImageContentType: &contentType, DateReported: pgtype.Timestamptz{Time: now, Valid: true}}}}
	objects := &presignStoreFake{}
	dashboard := Dashboard{Store: store, Fleet: store, Objects: objects, Now: func() time.Time { return now }, PageMeta: components.PageMeta{StylesheetURL: "/assets/app.css", ScriptURL: "/assets/app.js"}}
	response := dashboardResponse(t, dashboard.Admin, uuid.New())
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "A lista está limitada a 500 equipamentos.") || !strings.Contains(response.Body.String(), `hx-target-422="#maintenance-form"`) {
		t.Fatalf("unexpected fleet response: %d %q", response.Code, response.Body.String())
	}
	if store.adminLimit != 501 || store.repairLimit != 50 || store.maintenanceParams.RowLimit != 50 || !store.maintenanceParams.ToTime.Time.Equal(now.AddDate(0, 0, 90)) {
		t.Fatalf("unexpected fleet limits: %+v", store)
	}
	if objects.calls != 1 || objects.lifetime != 10*time.Minute {
		t.Fatalf("unexpected presign request: %+v", objects)
	}
	objects.err = errors.New("storage unavailable")
	response = dashboardResponse(t, dashboard.Admin, uuid.New())
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Imagem temporariamente indisponível") {
		t.Fatalf("presign failure should not fail the fleet page: %d %q", response.Code, response.Body.String())
	}
}

func TestCalendarSourceIDsPreservesPublicSourceIDs(t *testing.T) {
	sources := calendarSourceIDs([]pages.CalendarLink{{ID: "training@example.test"}, {ID: "calendar with spaces@example.test"}})
	if sources != `["training@example.test","calendar with spaces@example.test"]` {
		t.Fatalf("calendar sources = %q", sources)
	}
}

type presignStoreFake struct {
	calls    int
	lifetime time.Duration
	err      error
}

func (f *presignStoreFake) PutRepairPhoto(context.Context, string, string, int64, io.Reader) error {
	return nil
}
func (f *presignStoreFake) DeleteObject(context.Context, string) error { return nil }
func (f *presignStoreFake) PresignGet(_ context.Context, _ string, lifetime time.Duration) (string, error) {
	f.calls++
	f.lifetime = lifetime
	return "https://storage.example/photo", f.err
}

func (f *dashboardStoreFake) ListOperationalEquipment(_ context.Context, _ int32) ([]dbgen.Equipment, error) {
	return nil, nil
}

func (f *dashboardStoreFake) ListEventsForToday(_ context.Context, params dbgen.ListEventsForTodayParams) ([]dbgen.ListEventsForTodayRow, error) {
	f.todayParams = params
	return f.todayEvents, nil
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

func (f *dashboardStoreFake) ListWhatsAppGroupsForUserProgramme(_ context.Context, _ dbgen.ListWhatsAppGroupsForUserProgrammeParams) ([]dbgen.WhatsappGroup, error) {
	return f.groups, f.groupsErr
}

func (f *dashboardStoreFake) ListDependentsByGuardian(_ context.Context, _ dbgen.ListDependentsByGuardianParams) ([]dbgen.ListDependentsByGuardianRow, error) {
	return f.dependents, nil
}
