package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/featureflags"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/release"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const dashboardQueryTimeout = 5 * time.Second
const fleetPageSize = 6

type DashboardStore interface {
	ListRecentPerformanceMetrics(context.Context, dbgen.ListRecentPerformanceMetricsParams) ([]dbgen.PerformanceMetric, error)
	ListRecentTrainingLogs(context.Context, dbgen.ListRecentTrainingLogsParams) ([]dbgen.TrainingLog, error)
	ListPublishedNews(context.Context, int32) ([]dbgen.NewsItem, error)
	ListWhatsAppGroupsForUserProgramme(context.Context, dbgen.ListWhatsAppGroupsForUserProgrammeParams) ([]dbgen.WhatsappGroup, error)
	ListDependentsByGuardian(context.Context, dbgen.ListDependentsByGuardianParams) ([]dbgen.ListDependentsByGuardianRow, error)
	ListOperationalEquipment(context.Context, int32) ([]dbgen.Equipment, error)
	ListEventsForMember(context.Context, dbgen.ListEventsForMemberParams) ([]dbgen.ListEventsForMemberRow, error)
	ListEventsForToday(context.Context, dbgen.ListEventsForTodayParams) ([]dbgen.ListEventsForTodayRow, error)
	ListUpcomingTrainingSessionsForDashboard(context.Context, dbgen.ListUpcomingTrainingSessionsForDashboardParams) ([]dbgen.ListUpcomingTrainingSessionsForDashboardRow, error)
	ListDistanceLeaderboard(context.Context, dbgen.ListDistanceLeaderboardParams) ([]dbgen.ListDistanceLeaderboardRow, error)
	UpdateOwnLeaderboardVisibility(context.Context, dbgen.UpdateOwnLeaderboardVisibilityParams) (int64, error)
	UpdateDependentLeaderboardVisibility(context.Context, dbgen.UpdateDependentLeaderboardVisibilityParams) (int64, error)
}

type FleetStore interface {
	CountEquipmentByStatus(context.Context) ([]dbgen.CountEquipmentByStatusRow, error)
	ListEquipmentForAdmin(context.Context, dbgen.ListEquipmentForAdminParams) ([]dbgen.Equipment, error)
	ListPendingRepairRequests(context.Context, dbgen.ListPendingRepairRequestsParams) ([]dbgen.ListPendingRepairRequestsRow, error)
	ListUpcomingMaintenance(context.Context, dbgen.ListUpcomingMaintenanceParams) ([]dbgen.ListUpcomingMaintenanceRow, error)
	GetMaintenanceForAdmin(context.Context, uuid.UUID) (dbgen.GetMaintenanceForAdminRow, error)
	ScheduleMaintenanceTask(context.Context, dbgen.ScheduleMaintenanceTaskParams) (dbgen.ScheduleMaintenanceTaskRow, error)
	UpdateRepairStatus(context.Context, dbgen.UpdateRepairStatusParams) (dbgen.RepairRequest, error)
	CompleteMaintenanceTask(context.Context, uuid.UUID) (dbgen.MaintenanceTask, error)
}

type EquipmentStore interface {
	GetEquipmentByID(context.Context, uuid.UUID) (dbgen.Equipment, error)
	CreateEquipmentWithAudit(context.Context, dbgen.CreateEquipmentWithAuditParams) (dbgen.CreateEquipmentWithAuditRow, error)
	UpdateEquipmentWithAudit(context.Context, dbgen.UpdateEquipmentWithAuditParams) (dbgen.UpdateEquipmentWithAuditRow, error)
	RetireEquipmentWithAudit(context.Context, dbgen.RetireEquipmentWithAuditParams) (dbgen.RetireEquipmentWithAuditRow, error)
	ReactivateEquipmentWithAudit(context.Context, dbgen.ReactivateEquipmentWithAuditParams) (dbgen.ReactivateEquipmentWithAuditRow, error)
	ListEquipmentAuditEvents(context.Context, dbgen.ListEquipmentAuditEventsParams) ([]dbgen.ListEquipmentAuditEventsRow, error)
}

type ReleaseChecker interface {
	Snapshot(context.Context) release.Snapshot
}

type Dashboard struct {
	Store                 DashboardStore
	Fleet                 FleetStore
	Equipment             EquipmentStore
	Releases              ReleaseChecker
	Features              FeatureFlagStore
	System                System
	PageMeta              components.PageMeta
	Location              *time.Location
	Dependents            GuardianDependentStore
	Now                   func() time.Time
	ResponsibilityVersion string
	ResponsibilitySHA256  string
	ResponsibilityURL     string
	Sessions              *scs.SessionManager
	Objects               storage.ObjectStore
	MaxRequestBytes       int64
	MaxPhotoBytes         int64
}

func (h Dashboard) Competitor(w http.ResponseWriter, r *http.Request) {
	h.athlete(w, r, "Painel de atleta", "/dashboard/competitor", []string{"Competition", "Initiation", "Kayak_Polo"})
}

func (h Dashboard) Initiation(w http.ResponseWriter, r *http.Request) {
	h.athlete(w, r, "Iniciação", "/dashboard/initiation", []string{"Initiation"})
}

func (h Dashboard) Competition(w http.ResponseWriter, r *http.Request) {
	h.athlete(w, r, "Competição", "/dashboard/competition", []string{"Competition"})
}

func (h Dashboard) KayakPolo(w http.ResponseWriter, r *http.Request) {
	h.athlete(w, r, "Kayak polo", "/dashboard/kayak-polo", []string{"Kayak_Polo"})
}

func (h Dashboard) athlete(w http.ResponseWriter, r *http.Request, heading, path string, programmes []string) {
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	since := pgtype.Timestamptz{Time: time.Now().AddDate(0, 0, -90), Valid: true}
	metrics, err := h.Store.ListRecentPerformanceMetrics(ctx, dbgen.ListRecentPerformanceMetricsParams{UserID: user.ID, Since: since, RowLimit: 6})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	logs, err := h.Store.ListRecentTrainingLogs(ctx, dbgen.ListRecentTrainingLogsParams{UserID: user.ID, Since: since, RowLimit: 10})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	groups, err := h.groups(ctx, user.ID, programmes...)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	agenda, err := h.programmeAgenda(ctx, user.ID, true)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.render(w, r, heading, "Acompanhe os seus treinos, desempenho e agenda.", "Ainda não existem treinos ou métricas para apresentar.", path, agenda, []DashboardSectionVM{
		{Heading: "Desempenho", Empty: "Ainda não existem métricas recentes.", Items: performanceMetricItems(metrics)},
		{Heading: "Treinos recentes", Empty: "Ainda não existem treinos recentes.", Items: trainingLogItems(logs)},
		{Heading: "Grupos WhatsApp", Empty: "Ainda não existem grupos WhatsApp ativos.", Items: whatsappGroupItems(groups)},
	})
}
func (h Dashboard) Leisure(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	news, err := h.Store.ListPublishedNews(ctx, 10)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	groups, err := h.groups(ctx, user.ID, "Leisure")
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	agenda, err := h.programmeAgenda(ctx, user.ID, false)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.render(w, r, "Lazer", "Consulte as notícias e a agenda do clube.", "Ainda não existem notícias publicadas.", "/dashboard/leisure", agenda, []DashboardSectionVM{
		{Heading: "Notícias", Empty: "Ainda não existem notícias publicadas.", Items: newsItems(news)},
		{Heading: "Grupos WhatsApp", Empty: "Ainda não existem grupos WhatsApp ativos.", Items: whatsappGroupItems(groups)},
	})
}
func (h Dashboard) Member(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "Área de membro", "A sua conta está ativa. As atividades do clube serão apresentadas quando a sua inscrição de época for atribuída.", "Ainda não tem inscrições de época ativas.", "/dashboard/member", nil, nil)
}
func (h Dashboard) Coach(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "Área de treinador", "Consulte os eventos das suas atribuições ativas.", "Use Eventos para criar e gerir eventos no âmbito das suas atribuições.", "/dashboard/coach", nil, nil)
}
func (h Dashboard) Moderator(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "Área de moderador", "A sua atribuição de moderação está ativa.", "As ferramentas de moderação serão apresentadas aqui.", "/dashboard/moderator", nil, nil)
}
func (h Dashboard) Today(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	now := h.now().In(h.location())
	period := selectedLeaderboardPeriod(r.URL.Query().Get("leaderboard_period"))
	periodStart, periodEnd := leaderboardPeriodBounds(period, now, h.location())
	dayStartsAt := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, h.location())
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	events, err := h.Store.ListEventsForToday(ctx, dbgen.ListEventsForTodayParams{UserID: user.ID, IsAdmin: user.IsAdmin, DayStartsAt: pgtype.Timestamptz{Time: dayStartsAt, Valid: true}, DayEndsAt: pgtype.Timestamptz{Time: dayStartsAt.AddDate(0, 0, 1), Valid: true}})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if len(events) > 4 {
		events = events[:4]
	}
	leaders, err := h.Store.ListDistanceLeaderboard(ctx, dbgen.ListDistanceLeaderboardParams{
		CurrentUserID: user.ID,
		ActiveOn:      pgtype.Date{Time: dayStartsAt, Valid: true},
		AsOf:          pgtype.Timestamptz{Time: now, Valid: true},
		PeriodStart:   periodStart,
		PeriodEnd:     periodEnd,
	})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	eventBasePath := "/events"
	if user.IsAdmin {
		eventBasePath = "/admin/eventos"
	}
	page := pages.TodayPage{Name: user.Name, EventBasePath: eventBasePath, Events: make([]pages.TodayEvent, len(events)), Shortcuts: todayShortcuts(user), ShowShortcuts: true, ProfileIncomplete: !user.ProfileComplete}
	page.Leaderboard = pages.TodayLeaderboard{Period: string(period), IsAthlete: len(user.Programmes) > 0, Visible: user.LeaderboardVisible}
	for _, leader := range leaders {
		row := pages.TodayLeaderboardRow{Position: leader.Position, UserID: leader.UserID.String(), Name: leader.Name, Distance: formatKilometres(leader.TotalMetres)}
		if leader.UserID == user.ID && leader.Position > 10 {
			page.Leaderboard.Current = &row
			continue
		}
		if leader.Position <= 10 {
			page.Leaderboard.Rows = append(page.Leaderboard.Rows, row)
		}
	}
	for i, event := range events {
		page.Events[i] = pages.TodayEvent{ID: event.ID.String(), Title: event.Title, When: event.StartsAt.Time.In(h.location()).Format("15:04") + " - " + event.EndsAt.Time.In(h.location()).Format("15:04"), Cancelled: event.Status == "CANCELLED", CancellationReason: stringValue(event.CancellationReason)}
		if page.NextEvent == nil && event.Status != "CANCELLED" && event.EndsAt.Time.After(now) {
			page.NextEvent = &page.Events[i]
		}
	}
	if !user.IsDependent {
		dependents, err := h.Store.ListDependentsByGuardian(ctx, dbgen.ListDependentsByGuardianParams{GuardianID: &user.ID, RowLimit: 3})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		for _, dependent := range dependents {
			page.Dependents = append(page.Dependents, pages.TodayDependent{ID: dependent.ID.String(), Name: dependent.Name, HasAccess: dependent.MinorLoginID != nil})
		}
	}
	if user.IsAdmin && h.Fleet != nil {
		repairs, err := h.Fleet.ListPendingRepairRequests(ctx, dbgen.ListPendingRepairRequestsParams{RowLimit: 3})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		for _, repair := range repairs {
			page.Operations = append(page.Operations, pages.TodayOperation{Label: repair.AssetTag + " · " + repair.EquipmentName, Detail: todayRepairStatus(repair.Status)})
		}
	}
	if len(page.Dependents) > 0 && len(page.Operations) > 0 {
		page.ShowShortcuts = false
	}
	if h.Sessions != nil {
		page.Success = h.Sessions.PopString(r.Context(), "leaderboard_flash")
	}
	page.Meta = h.PageMeta
	page.Meta.Title = "Hoje | MyCFC"
	page.Meta.CurrentPath = "/today"
	page.Meta.CurrentUserName = user.Name
	page.Meta.CurrentUserID = user.ID.String()
	page.Meta.EmailVerificationPending = !user.IsDependent && !user.EmailVerified
	page.Meta.Navigation = dashboardNavigation(user)
	page.Meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pages.Today(page).Render(r.Context(), w)
}

type leaderboardPeriod string

const (
	leaderboardWeek  leaderboardPeriod = "week"
	leaderboardMonth leaderboardPeriod = "month"
	leaderboardYear  leaderboardPeriod = "year"
	leaderboardAll   leaderboardPeriod = "all"
)

func selectedLeaderboardPeriod(value string) leaderboardPeriod {
	switch leaderboardPeriod(value) {
	case leaderboardMonth, leaderboardYear, leaderboardAll:
		return leaderboardPeriod(value)
	default:
		return leaderboardWeek
	}
}

func leaderboardPeriodBounds(period leaderboardPeriod, now time.Time, location *time.Location) (pgtype.Timestamptz, pgtype.Timestamptz) {
	local := now.In(location)
	var start, end time.Time
	switch period {
	case leaderboardMonth:
		start = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, location)
		end = start.AddDate(0, 1, 0)
	case leaderboardYear:
		start = time.Date(local.Year(), time.January, 1, 0, 0, 0, 0, location)
		end = start.AddDate(1, 0, 0)
	case leaderboardAll:
		return pgtype.Timestamptz{}, pgtype.Timestamptz{}
	default:
		daysSinceMonday := (int(local.Weekday()) + 6) % 7
		start = time.Date(local.Year(), local.Month(), local.Day()-daysSinceMonday, 0, 0, 0, 0, location)
		end = start.AddDate(0, 0, 7)
	}
	return pgtype.Timestamptz{Time: start, Valid: true}, pgtype.Timestamptz{Time: end, Valid: true}
}

func (h Dashboard) LeaderboardPrivacy(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	if len(user.Programmes) == 0 {
		h.System.Forbidden(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	visible := r.PostForm.Get("leaderboard_visible") == "on"
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	n, err := h.Store.UpdateOwnLeaderboardVisibility(ctx, dbgen.UpdateOwnLeaderboardVisibilityParams{UserID: user.ID, LeaderboardVisible: visible})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if n != 1 {
		http.Error(w, "A preferência não está disponível.", http.StatusConflict)
		return
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "leaderboard_flash", "Privacidade da classificação atualizada.")
	}
	period := selectedLeaderboardPeriod(r.PostForm.Get("leaderboard_period"))
	http.Redirect(w, r, "/today?leaderboard_period="+string(period)+"#leaderboard", http.StatusSeeOther)
}

func (h Dashboard) DependentLeaderboardPrivacy(w http.ResponseWriter, r *http.Request) {
	dependentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Menor inválido.", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	guardian, _ := CurrentUserFromContext(r.Context())
	visible := r.PostForm.Get("leaderboard_visible") == "on"
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	n, err := h.Store.UpdateDependentLeaderboardVisibility(ctx, dbgen.UpdateDependentLeaderboardVisibilityParams{DependentUserID: dependentID, GuardianUserID: &guardian.ID, LeaderboardVisible: visible})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if n != 1 {
		h.System.Forbidden(w, r)
		return
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "guardian_flash", "Privacidade da classificação atualizada.")
	}
	http.Redirect(w, r, "/dashboard/guardian", http.StatusSeeOther)
}

func todayRepairStatus(status string) string {
	if status == "Em_Analise" {
		return "Em análise"
	}
	return status
}

func todayShortcuts(user CurrentUser) []pages.TodayShortcut {
	items := []pages.TodayShortcut{{Label: "Eventos", Detail: "Agenda e respostas", Path: "/events"}, {Label: "Treinos", Detail: "Planos e sessões", Path: "/treinos"}}

programmeGroups:
	for _, group := range dashboardNavigation(user) {
		for _, item := range group.Items {
			if strings.HasPrefix(item.Path, "/dashboard/") && item.Path != "/dashboard/guardian" {
				items = append(items, pages.TodayShortcut{Label: item.Label, Detail: "Abrir o meu espaço", Path: item.Path})
				break programmeGroups
			}
		}
	}
	if user.IsAdmin {
		items = append(items, pages.TodayShortcut{Label: "Membros", Detail: "Diretório e inscrições", Path: "/admin/membros"})
	}
	if len(items) > 5 {
		items = items[:5]
	}
	return items
}
func (h Dashboard) Guardian(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	dependents, err := h.Store.ListDependentsByGuardian(ctx, dbgen.ListDependentsByGuardianParams{GuardianID: &user.ID, RowLimit: 10})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.renderGuardian(w, r, http.StatusOK, dependents, guardianDependentForm{})
}
func (h Dashboard) Admin(w http.ResponseWriter, r *http.Request) {
	h.renderFleet(w, r, http.StatusOK, fleetMaintenanceForm{})
}

func (h Dashboard) ReleasesPage(w http.ResponseWriter, r *http.Request) {
	h.renderSystem(w, r, http.StatusOK, featureFlagForm{})
}

type fleetMaintenanceForm struct {
	EquipmentID, ScheduledFor, Description, Success, ReturnURL string
	Errors                                                     validation.FieldErrors
	ActionErrorID, ActionError                                 string
}

func (h Dashboard) Maintenance(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderFleet(w, r, http.StatusBadRequest, fleetMaintenanceForm{})
		return
	}
	form := h.validateMaintenance(r)
	if !form.Errors.Empty() {
		h.renderFleet(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	scheduledFor, _ := time.ParseInLocation("2006-01-02T15:04", form.ScheduledFor, h.location())
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	if h.Fleet == nil {
		h.System.InternalError(w, r)
		return
	}
	_, err := h.Fleet.ScheduleMaintenanceTask(ctx, dbgen.ScheduleMaintenanceTaskParams{
		EquipmentID: mustParseUUID(form.EquipmentID), ScheduledFor: pgtype.Timestamptz{Time: scheduledFor, Valid: true},
		Description: form.Description, CreatedByID: &user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		form.Errors.Add("equipment_id", "Selecione um equipamento disponível.")
		h.renderFleet(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		h.renderMaintenanceForm(w, r, http.StatusOK, fleetMaintenanceForm{Success: "Manutenção agendada.", ReturnURL: fleetCollectionReturn(r, "maintenance-schedule")})
		return
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "fleet_flash", "Manutenção agendada.")
	}
	httpx.Redirect(w, r, fleetCollectionReturn(r, "maintenance-schedule"), http.StatusSeeOther)
}

func (h Dashboard) RepairStatus(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderFleetActionError(w, r, http.StatusBadRequest, "repair", r.PostForm.Get("repair_id"), "Não foi possível processar o pedido.")
		return
	}
	repairID, err := uuid.Parse(r.PostForm.Get("repair_id"))
	if err != nil {
		h.renderFleetActionError(w, r, http.StatusUnprocessableEntity, "repair", r.PostForm.Get("repair_id"), "Pedido de reparação inválido.")
		return
	}
	expectedStatus, status := r.PostForm.Get("expected_status"), r.PostForm.Get("status")
	if !validRepairTransition(expectedStatus, status) {
		h.renderFleetActionError(w, r, http.StatusUnprocessableEntity, "repair", repairID.String(), "A alteração de estado não é válida.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	if h.Fleet == nil {
		h.System.InternalError(w, r)
		return
	}
	_, err = h.Fleet.UpdateRepairStatus(ctx, dbgen.UpdateRepairStatusParams{ID: repairID, Status: status, ExpectedStatus: expectedStatus})
	if errors.Is(err, pgx.ErrNoRows) {
		h.renderFleetActionError(w, r, http.StatusConflict, "repair", repairID.String(), "O pedido de reparação já foi atualizado. Atualize a página.")
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	message := "Pedido de reparação em análise."
	if status == "Resolvido" {
		message = "Pedido de reparação resolvido."
	}
	h.fleetActionSuccess(w, r, "repair", repairID.String(), status, message)
}

func (h Dashboard) CompleteMaintenance(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderMaintenanceCompletionError(w, r, http.StatusBadRequest, "Não foi possível processar o pedido. Reveja os dados atuais e volte a confirmar.")
		return
	}
	taskID, err := uuid.Parse(r.PostForm.Get("maintenance_id"))
	if err != nil {
		h.renderMaintenanceCompletionError(w, r, http.StatusUnprocessableEntity, "A confirmação não corresponde à tarefa de manutenção. Reveja os dados atuais e volte a confirmar.")
		return
	}
	if pathID := r.PathValue("id"); pathID != "" && pathID != taskID.String() {
		h.renderMaintenanceCompletionError(w, r, http.StatusUnprocessableEntity, "A confirmação não corresponde à tarefa de manutenção. Reveja os dados atuais e volte a confirmar.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	if h.Fleet == nil {
		h.System.InternalError(w, r)
		return
	}
	_, err = h.Fleet.CompleteMaintenanceTask(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.renderMaintenanceCompletionError(w, r, http.StatusConflict, "A tarefa já foi concluída ou cancelada. Reveja o estado atual antes de voltar a confirmar.")
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.fleetActionSuccess(w, r, "maintenance", taskID.String(), "Completed", "Manutenção concluída.")
}

func (h Dashboard) CompleteMaintenancePage(w http.ResponseWriter, r *http.Request) {
	h.renderMaintenanceCompletionPage(w, r, http.StatusOK, "")
}

func (h Dashboard) renderMaintenanceCompletionError(w http.ResponseWriter, r *http.Request, status int, conflict string) {
	if r.Header.Get("HX-Request") == "true" {
		h.renderFleetActionError(w, r, status, "maintenance", r.PathValue("id"), conflict)
		return
	}
	h.renderMaintenanceCompletionPage(w, r, status, conflict)
}

func (h Dashboard) renderMaintenanceCompletionPage(w http.ResponseWriter, r *http.Request, status int, conflict string) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	if h.Fleet == nil {
		h.System.InternalError(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	task, err := h.Fleet.GetMaintenanceForAdmin(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if conflict == "" && task.Status != "Scheduled" && task.Status != "In_Progress" {
		conflict = "A tarefa já foi concluída ou cancelada. Reveja o estado atual antes de continuar."
		status = http.StatusConflict
	}
	user, _ := CurrentUserFromContext(r.Context())
	meta := h.PageMeta
	meta.Title = "Concluir manutenção | MyCFC"
	meta.CurrentPath = r.URL.Path
	meta.PageLabel = "Concluir manutenção"
	meta.CurrentUserName = user.Name
	meta.CurrentUserID = user.ID.String()
	meta.EmailVerificationPending = !user.IsDependent && !user.EmailVerified
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	returnURL := fleetCollectionReturn(r, "maintenance-"+id.String())
	meta.Breadcrumbs = []components.NavigationItem{{Label: "Frota", Path: returnURL}}
	page := pages.MaintenanceCompletionPage{Meta: meta, Task: pages.FleetMaintenance{ID: task.ID.String(), Equipment: task.AssetTag + " - " + task.EquipmentName, Description: task.Description, Status: task.Status, ScheduledFor: task.ScheduledFor.Time.In(h.location()).Format("02/01/2006 15:04")}, CSRFField: meta.CSRFField, ReturnURL: returnURL, Conflict: conflict}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = pages.MaintenanceCompletion(page).Render(r.Context(), w)
}

func validRepairTransition(from, to string) bool {
	return (from == "Pendente" && to == "Em_Analise") || (from == "Em_Analise" && to == "Resolvido")
}

func (h Dashboard) fleetActionSuccess(w http.ResponseWriter, r *http.Request, kind, id, status, message string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pages.FleetMutationFeedback(kind, id, status, message, "", "").Render(r.Context(), w)
		return
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "fleet_flash", message)
	}
	httpx.Redirect(w, r, fleetCollectionReturn(r, kind+"-"+id), http.StatusSeeOther)
}

func (h Dashboard) renderFleetActionError(w http.ResponseWriter, r *http.Request, status int, kind, id, message string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_ = pages.FleetMutationFeedback(kind, id, "", "", message, fleetCollectionReturn(r, kind+"-"+id)).Render(r.Context(), w)
		return
	}
	h.renderFleet(w, r, status, fleetMaintenanceForm{ActionErrorID: id, ActionError: message})
}

func (h Dashboard) validateMaintenance(r *http.Request) fleetMaintenanceForm {
	form := fleetMaintenanceForm{EquipmentID: strings.TrimSpace(r.PostForm.Get("equipment_id")), ScheduledFor: strings.TrimSpace(r.PostForm.Get("scheduled_for")), Description: strings.TrimSpace(r.PostForm.Get("description")), Errors: validation.FieldErrors{}}
	if _, err := uuid.Parse(form.EquipmentID); err != nil {
		form.Errors.Add("equipment_id", "Selecione um equipamento válido.")
	}
	if _, err := time.ParseInLocation("2006-01-02T15:04", form.ScheduledFor, h.location()); err != nil {
		form.Errors.Add("scheduled_for", "Introduza uma data e hora válidas.")
	}
	length := utf8.RuneCountInString(form.Description)
	if length < 10 || length > 2000 {
		form.Errors.Add("description", "A descrição deve ter entre 10 e 2000 caracteres.")
	}
	return form
}

func mustParseUUID(value string) uuid.UUID {
	id, _ := uuid.Parse(value)
	return id
}

func (h Dashboard) renderFleet(w http.ResponseWriter, r *http.Request, status int, form fleetMaintenanceForm) {
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	page, err := h.fleetPage(ctx, r, form)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	page.Meta = h.PageMeta
	page.Meta.Title = "Frota | MyCFC"
	page.Meta.CurrentPath = "/admin/fleet"
	page.Meta.CurrentUserName = user.Name
	page.Meta.CurrentUserID = user.ID.String()
	page.Meta.EmailVerificationPending = !user.IsDependent && !user.EmailVerified
	page.Meta.Navigation = dashboardNavigation(user)
	page.Meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	page.MaintenanceForm.CSRFField = page.Meta.CSRFField
	page.EquipmentForm.CSRFField = page.Meta.CSRFField
	page.RepairForm.CSRFField = page.Meta.CSRFField
	if h.Sessions != nil {
		page.MaintenanceForm.Success = h.Sessions.PopString(r.Context(), "fleet_flash")
		page.Success = h.Sessions.PopString(r.Context(), "equipment_flash")
		page.RepairForm.Success = h.Sessions.PopString(r.Context(), "repair_flash")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if r.Header.Get("HX-Request") == "true" {
		_ = pages.MaintenanceForm(page.MaintenanceForm).Render(r.Context(), w)
		return
	}
	_ = pages.Fleet(page).Render(r.Context(), w)
}

func (h Dashboard) renderMaintenanceForm(w http.ResponseWriter, r *http.Request, status int, form fleetMaintenanceForm) {
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	equipment, err := h.Store.ListOperationalEquipment(ctx, 500)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	meta := h.PageMeta
	meta.CurrentUserName = user.Name
	meta.CurrentUserID = user.ID.String()
	meta.EmailVerificationPending = !user.IsDependent && !user.EmailVerified
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.MaintenanceForm(pages.FleetMaintenanceForm{CSRFField: meta.CSRFField, Equipment: repairEquipment(equipment), Success: form.Success, ReturnURL: collectionReturnOr(form.ReturnURL, "/admin/fleet#maintenance-schedule")}).Render(r.Context(), w)
}

func (h Dashboard) fleetPage(ctx context.Context, r *http.Request, form fleetMaintenanceForm) (pages.FleetPage, error) {
	if h.Fleet == nil {
		return pages.FleetPage{}, errors.New("fleet store is not configured")
	}
	counts, err := h.Fleet.CountEquipmentByStatus(ctx)
	if err != nil {
		return pages.FleetPage{}, err
	}
	query := fleetCollectionQuery(r)
	equipmentPage := fleetPageNumber(query.Get("equipment_page"))
	equipment, err := h.Fleet.ListEquipmentForAdmin(ctx, dbgen.ListEquipmentForAdminParams{RowLimit: fleetPageSize + 1, RowOffset: int32((equipmentPage - 1) * fleetPageSize)})
	if err != nil {
		return pages.FleetPage{}, err
	}
	repairsPage := fleetPageNumber(query.Get("repairs_page"))
	repairs, err := h.Fleet.ListPendingRepairRequests(ctx, dbgen.ListPendingRepairRequestsParams{RowLimit: fleetPageSize + 1, RowOffset: int32((repairsPage - 1) * fleetPageSize)})
	if err != nil {
		return pages.FleetPage{}, err
	}
	now := h.now()
	maintenancePage := fleetPageNumber(query.Get("maintenance_page"))
	maintenance, err := h.Fleet.ListUpcomingMaintenance(ctx, dbgen.ListUpcomingMaintenanceParams{FromTime: pgtype.Timestamptz{Time: now, Valid: true}, ToTime: pgtype.Timestamptz{Time: now.AddDate(0, 0, 90), Valid: true}, RowLimit: fleetPageSize + 1, RowOffset: int32((maintenancePage - 1) * fleetPageSize)})
	if err != nil {
		return pages.FleetPage{}, err
	}
	page := pages.FleetPage{ReturnURL: fleetCollectionURL(query), Counts: fleetStatusCounts(counts), EquipmentForm: pages.EquipmentForm{Type: "Boat", Status: "Operational", Errors: validation.FieldErrors{}}, MaintenanceForm: pages.FleetMaintenanceForm{EquipmentID: form.EquipmentID, ScheduledFor: form.ScheduledFor, Description: form.Description, Errors: form.Errors, Success: form.Success}}
	page.EquipmentForm.ReturnURL = collectionURLWithFragment(page.ReturnURL, "equipment-inventory")
	page.MaintenanceForm.ReturnURL = collectionURLWithFragment(page.ReturnURL, "maintenance-schedule")
	page.EquipmentPreviousURL, page.EquipmentNextURL, equipment = fleetPaginationURLs(query, "equipment_page", equipmentPage, "equipment-inventory", equipment)
	page.RepairsPreviousURL, page.RepairsNextURL, repairs = fleetPaginationURLs(query, "repairs_page", repairsPage, "repair-requests", repairs)
	page.MaintenancePreviousURL, page.MaintenanceNextURL, maintenance = fleetPaginationURLs(query, "maintenance_page", maintenancePage, "maintenance-schedule", maintenance)
	page.Equipment = make([]pages.FleetEquipment, len(equipment))
	for i, item := range equipment {
		photoURL, photoUnavailable := h.equipmentPhotoURL(ctx, r, item.ImageObjectKey, item.ImageContentType, item.ID)
		page.Equipment[i] = pages.FleetEquipment{ID: item.ID.String(), AssetTag: item.AssetTag, Name: item.Name, Type: item.Type, Status: item.Status, Notes: item.Notes, PhotoURL: photoURL, PhotoUnavailable: photoUnavailable}
	}
	page.Repairs = h.fleetRepairs(ctx, r, repairs)
	for i := range page.Repairs {
		if page.Repairs[i].ID == form.ActionErrorID {
			page.Repairs[i].ActionError = form.ActionError
		}
	}
	page.Maintenance = make([]pages.FleetMaintenance, len(maintenance))
	for i, task := range maintenance {
		page.Maintenance[i] = pages.FleetMaintenance{ID: task.ID.String(), Equipment: task.AssetTag + " - " + task.EquipmentName, Description: task.Description, Status: task.Status, ScheduledFor: task.ScheduledFor.Time.In(h.location()).Format("02/01/2006 15:04"), ActionError: actionError(task.ID.String(), form)}
	}
	nonRetired, err := h.Store.ListOperationalEquipment(ctx, 500)
	if err != nil {
		return pages.FleetPage{}, err
	}
	page.MaintenanceForm.Equipment = repairEquipment(nonRetired)
	page.RepairForm = components.RepairFormData{IdempotencyKey: uuid.NewString(), ReturnTo: collectionURLWithFragment(page.ReturnURL, "repair-requests"), Equipment: repairChoices(nonRetired)}
	return page, nil
}

func (h Dashboard) releaseStatus(ctx context.Context) pages.ReleaseStatus {
	if h.Releases == nil {
		return pages.ReleaseStatus{}
	}
	snapshot := h.Releases.Snapshot(ctx)
	return pages.ReleaseStatus{
		CurrentLabel:      snapshot.Current.Label,
		CurrentReleasedAt: h.formatReleaseTime(snapshot.Current.PublishedAt),
		LatestLabel:       snapshot.Latest.Label,
		LatestReleasedAt:  h.formatReleaseTime(snapshot.Latest.PublishedAt),
		LatestURL:         snapshot.Latest.URL,
		CheckedAt:         h.formatReleaseTime(snapshot.CheckedAt),
		Status:            string(snapshot.Status),
		CheckUnavailable:  snapshot.CheckUnavailable,
	}
}

func (h Dashboard) formatReleaseTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(h.location()).Format("02/01/2006 15:04")
}

func fleetPageNumber(value string) int {
	page, err := strconv.Atoi(value)
	if err != nil || page < 1 || page > 10000 {
		return 1
	}
	return page
}

func fleetPaginationURLs[T any](query url.Values, parameter string, page int, fragment string, items []T) (string, string, []T) {
	var previous, next string
	if page > 1 {
		previous = fleetPageURL(query, parameter, page-1) + "#" + fragment
	}
	if len(items) > fleetPageSize {
		next = fleetPageURL(query, parameter, page+1) + "#" + fragment
		items = items[:fleetPageSize]
	}
	return previous, next, items
}

func fleetCollectionQuery(r *http.Request) url.Values {
	raw := ""
	if r.URL.Path == "/admin/fleet" {
		raw = r.URL.RequestURI()
	} else {
		raw = r.URL.Query().Get("return_to")
		if raw == "" && r.PostForm != nil {
			raw = r.PostForm.Get("return_to")
		}
	}
	safe := safeCollectionReturn(raw)
	if !strings.HasPrefix(safe, "/admin/fleet") {
		return url.Values{}
	}
	parsed, _ := url.Parse(safe)
	return parsed.Query()
}

func fleetCollectionURL(query url.Values) string {
	if encoded := query.Encode(); encoded != "" {
		return "/admin/fleet?" + encoded
	}
	return "/admin/fleet"
}

func fleetCollectionReturn(r *http.Request, fallbackFragment string) string {
	raw := r.URL.Query().Get("return_to")
	if raw == "" && r.PostForm != nil {
		raw = r.PostForm.Get("return_to")
	}
	safe := safeCollectionReturn(raw)
	if strings.HasPrefix(safe, "/admin/fleet") {
		return safe
	}
	return "/admin/fleet#" + fallbackFragment
}

func fleetPageURL(query url.Values, parameter string, page int) string {
	values := make(url.Values, len(query))
	for key, items := range query {
		values[key] = append([]string(nil), items...)
	}
	if page == 1 {
		values.Del(parameter)
	} else {
		values.Set(parameter, strconv.Itoa(page))
	}
	if encoded := values.Encode(); encoded != "" {
		return "/admin/fleet?" + encoded
	}
	return "/admin/fleet"
}

func actionError(id string, form fleetMaintenanceForm) string {
	if id == form.ActionErrorID {
		return form.ActionError
	}
	return ""
}

func fleetStatusCounts(counts []dbgen.CountEquipmentByStatusRow) []pages.FleetStatusCount {
	totals := map[string]int64{"Operational": 0, "Maintenance": 0, "Retired": 0}
	for _, count := range counts {
		totals[count.Status] = count.Total
	}
	return []pages.FleetStatusCount{{Status: "Operational", Total: totals["Operational"]}, {Status: "Maintenance", Total: totals["Maintenance"]}, {Status: "Retired", Total: totals["Retired"]}}
}

func repairEquipment(equipment []dbgen.Equipment) []components.RepairEquipment {
	choices := make([]components.RepairEquipment, len(equipment))
	for i, item := range equipment {
		choices[i] = components.RepairEquipment{ID: item.ID.String(), Label: item.AssetTag + " - " + item.Name}
	}
	return choices
}

func (h Dashboard) fleetRepairs(ctx context.Context, r *http.Request, repairs []dbgen.ListPendingRepairRequestsRow) []pages.FleetRepair {
	items := make([]pages.FleetRepair, len(repairs))
	for i, repair := range repairs {
		items[i] = pages.FleetRepair{ID: repair.ID.String(), Equipment: repair.AssetTag + " - " + repair.EquipmentName, Description: repair.IssueDescription, Status: repair.Status, ReportedAt: repair.DateReported.Time.In(h.location()).Format("02/01/2006 15:04")}
		if repair.ImageObjectKey == nil {
			continue
		}
		if repair.ImageContentType == nil || !validRepairImageContentType(*repair.ImageContentType) || h.Objects == nil {
			items[i].PhotoUnavailable = "Imagem temporariamente indisponível"
			continue
		}
		url, err := h.Objects.PresignGet(storage.WithPresignContentType(ctx, *repair.ImageContentType), *repair.ImageObjectKey, 10*time.Minute)
		if err != nil {
			slog.Warn("presign repair photo", "repair_id", repair.ID, "request_id", httpx.RequestID(r.Context()), "error", err)
			items[i].PhotoUnavailable = "Imagem temporariamente indisponível"
			continue
		}
		items[i].PhotoURL = url
	}
	return items
}

func validRepairImageContentType(contentType string) bool {
	return contentType == "image/jpeg" || contentType == "image/png" || contentType == "image/webp"
}

func (h Dashboard) render(w http.ResponseWriter, r *http.Request, heading, intro, emptyText, path string, agenda []DashboardAgendaItemVM, sections []DashboardSectionVM) {
	user, _ := CurrentUserFromContext(r.Context())
	meta := h.PageMeta
	meta.Title = heading + " | MyCFC"
	meta.CurrentPath = path
	meta.CurrentUserName = user.Name
	meta.CurrentUserID = user.ID.String()
	meta.EmailVerificationPending = !user.IsDependent && !user.EmailVerified
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	view := DashboardVM{Meta: meta, Heading: heading, Intro: intro, EmptyText: emptyText, Agenda: agenda, Sections: sections}
	pageAgenda := make([]pages.CalendarItem, len(view.Agenda))
	for i, item := range view.Agenda {
		pageAgenda[i] = pages.CalendarItem{Title: item.Title, Detail: item.Detail, URL: item.URL, Kind: item.Kind}
	}
	pageSections := make([]pages.DashboardSection, len(view.Sections))
	for i, section := range view.Sections {
		items := make([]pages.DashboardItem, len(section.Items))
		for j, item := range section.Items {
			items[j] = pages.DashboardItem{Title: item.Title, Detail: item.Detail, URL: item.URL}
		}
		pageSections[i] = pages.DashboardSection{Heading: section.Heading, Empty: section.Empty, Items: items}
	}
	_ = pages.Dashboard(pages.DashboardPage{Meta: view.Meta, Heading: view.Heading, Intro: view.Intro, EmptyText: view.EmptyText, Agenda: pageAgenda, ShowAgenda: agenda != nil, Sections: pageSections, Actions: dashboardPageActions(path)}).Render(r.Context(), w)
}

func (h Dashboard) renderGuardian(w http.ResponseWriter, r *http.Request, status int, dependents []dbgen.ListDependentsByGuardianRow, form guardianDependentForm) {
	user, _ := CurrentUserFromContext(r.Context())
	meta := h.PageMeta
	meta.Title = "Menores a cargo | MyCFC"
	meta.PageLabel = "Menores a cargo"
	meta.CurrentPath = "/dashboard/guardian"
	meta.CurrentUserName = user.Name
	meta.CurrentUserID = user.ID.String()
	meta.EmailVerificationPending = !user.IsDependent && !user.EmailVerified
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	items := guardianDependentItems(dependents, h.now(), h.location())
	pageItems := make([]pages.GuardianDependent, len(items))
	for i, item := range items {
		pageItems[i] = pages.GuardianDependent{ID: dependents[i].ID.String(), Name: item.Title, Detail: item.Detail, LeaderboardVisible: dependents[i].LeaderboardVisible, ProfileIncomplete: !dependents[i].ProfileComplete}
	}
	success := form.Success
	if success == "" && h.Sessions != nil {
		success = h.Sessions.PopString(r.Context(), "guardian_flash")
	}
	page := pages.GuardianPage{
		Meta: meta, Dependents: pageItems,
		ResponsibilityURL: h.ResponsibilityURL, Name: form.Name, DateOfBirth: form.DateOfBirth, ResponsibilityAccepted: form.ResponsibilityAccepted, Errors: form.Errors, Success: success,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if r.Header.Get("HX-Request") == "true" {
		_ = pages.GuardianContent(page).Render(r.Context(), w)
		return
	}
	_ = pages.Guardian(page).Render(r.Context(), w)
}

func dashboardPageActions(path string) []components.PageAction {
	if path == "/dashboard/leisure" {
		return []components.PageAction{{Label: "Ver eventos", Href: "/events", Variant: "primary"}}
	}
	if path == "/dashboard/member" || path == "/dashboard/coach" || path == "/dashboard/moderator" {
		return nil
	}
	return []components.PageAction{{Label: "Ver treinos", Href: "/treinos", Variant: "primary"}, {Label: "Ver eventos", Href: "/events", Variant: "secondary"}}
}

func (h Dashboard) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h Dashboard) location() *time.Location {
	if h.Location != nil {
		return h.Location
	}
	return time.UTC
}

func (h Dashboard) groups(ctx context.Context, userID uuid.UUID, programmes ...string) ([]dbgen.WhatsappGroup, error) {
	groups := make([]dbgen.WhatsappGroup, 0, 50)
	seen := map[uuid.UUID]bool{}
	for _, programme := range programmes {
		items, err := h.Store.ListWhatsAppGroupsForUserProgramme(ctx, dbgen.ListWhatsAppGroupsForUserProgrammeParams{UserID: userID, ProgrammeCode: programme, RowLimit: 50})
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if !seen[item.ID] {
				seen[item.ID] = true
				groups = append(groups, item)
			}
		}
	}
	return groups, nil
}

func performanceMetricItems(metrics []dbgen.PerformanceMetric) []DashboardItemVM {
	items := make([]DashboardItemVM, len(metrics))
	for i, metric := range metrics {
		items[i] = DashboardItemVM{Title: metric.LabelPt, Detail: fmt.Sprintf("%s %s · %s", numericString(metric.Value), metric.UnitPt, metric.MeasuredAt.Time.Format("02/01/2006"))}
	}
	return items
}

func trainingLogItems(logs []dbgen.TrainingLog) []DashboardItemVM {
	items := make([]DashboardItemVM, len(logs))
	for i, log := range logs {
		detail := fmt.Sprintf("%d min · %.1f km · %s", log.DurationSeconds/60, float64(log.DistanceMetres)/1000, log.OccurredAt.Time.Format("02/01/2006"))
		if log.Notes != "" {
			detail += " · " + log.Notes
		}
		items[i] = DashboardItemVM{Title: "Treino", Detail: detail}
	}
	return items
}

func newsItems(news []dbgen.NewsItem) []DashboardItemVM {
	items := make([]DashboardItemVM, len(news))
	for i, item := range news {
		url := ""
		if item.Url != nil {
			url = *item.Url
		}
		items[i] = DashboardItemVM{Title: item.TitlePt, Detail: item.SummaryPt, URL: url}
	}
	return items
}

func whatsappGroupItems(groups []dbgen.WhatsappGroup) []DashboardItemVM {
	items := make([]DashboardItemVM, len(groups))
	for i, group := range groups {
		items[i] = DashboardItemVM{Title: group.Name, Detail: group.Discipline, URL: group.Url}
	}
	return items
}

func numericString(value pgtype.Numeric) string {
	v, err := value.Value()
	if err != nil || v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func (h Dashboard) programmeAgenda(ctx context.Context, userID uuid.UUID, includeTraining bool) ([]DashboardAgendaItemVM, error) {
	type agendaItem struct {
		vm       DashboardAgendaItemVM
		startsAt time.Time
	}
	events, err := h.Store.ListEventsForMember(ctx, dbgen.ListEventsForMemberParams{UserID: userID, RowLimit: 6})
	if err != nil {
		return nil, err
	}
	items := make([]agendaItem, 0, len(events)+6)
	for _, event := range events {
		detail := h.formatAgendaRange(event.StartsAt.Time, event.EndsAt.Time)
		kind := eventTypeLabel(event.EventType)
		if event.Status == "CANCELLED" {
			kind = "Cancelado"
			if event.CancellationReason != nil {
				detail += " · " + *event.CancellationReason
			}
		}
		items = append(items, agendaItem{
			startsAt: event.StartsAt.Time,
			vm: DashboardAgendaItemVM{
				Title:  event.Title,
				Detail: detail,
				URL:    "/events/" + event.ID.String(),
				Kind:   kind,
			},
		})
	}
	if includeTraining {
		sessions, err := h.Store.ListUpcomingTrainingSessionsForDashboard(ctx, dbgen.ListUpcomingTrainingSessionsForDashboardParams{UserID: userID, FromTime: pgtype.Timestamptz{Time: h.now(), Valid: true}, RowLimit: 6})
		if err != nil {
			return nil, err
		}
		for _, session := range sessions {
			detail := h.formatAgendaRange(session.StartsAt.Time, session.EndsAt.Time)
			if session.ModalityName != nil && *session.ModalityName != "" {
				detail += " · " + *session.ModalityName
			}
			kind := "Treino"
			if session.Status == "CANCELLED" {
				kind = "Cancelado"
				if session.CancellationReason != nil {
					detail += " · " + *session.CancellationReason
				}
			}
			trainingURL := "/treinos"
			if session.PrescriptionAvailable {
				trainingURL = "/treinos/prescricoes/sessoes/" + session.ID.String()
			}
			items = append(items, agendaItem{
				startsAt: session.StartsAt.Time,
				vm:       DashboardAgendaItemVM{Title: session.Title, Detail: detail, URL: trainingURL, Kind: kind},
			})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].startsAt.Before(items[j].startsAt) })
	if len(items) > 8 {
		items = items[:8]
	}
	agenda := make([]DashboardAgendaItemVM, len(items))
	for i, item := range items {
		agenda[i] = item.vm
	}
	return agenda, nil
}

func (h Dashboard) formatAgendaRange(startsAt, endsAt time.Time) string {
	start := startsAt.In(h.location())
	end := endsAt.In(h.location())
	return start.Format("02/01/2006 15:04") + " - " + end.Format("15:04")
}

func guardianDependentItems(dependents []dbgen.ListDependentsByGuardianRow, now time.Time, location *time.Location) []DashboardItemVM {
	items := make([]DashboardItemVM, len(dependents))
	for i, dependent := range dependents {
		age := validation.AgeOn(dependent.DateOfBirth.Time, now, location)
		credential := "Sem acesso individual"
		if dependent.MinorLoginID != nil {
			credential = "Acesso individual emitido: " + *dependent.MinorLoginID
		}
		items[i] = DashboardItemVM{Title: dependent.Name, Detail: fmt.Sprintf("%d anos · %s", age, credential)}
	}
	return items
}

// dashboardNavigation keeps everyday destinations first and clusters every
// additional responsibility into labelled, simultaneously visible groups.
func dashboardNavigation(user CurrentUser) []components.NavigationGroup {
	today := []components.NavigationItem{{Label: "Hoje", Path: "/today"}}
	activity := []components.NavigationItem{{Label: "Eventos", Path: "/events"}, {Label: "Treinos", Path: "/treinos"}, {Label: "Álbuns", Path: "/albuns"}}
	if featureflags.Available(user.FeatureModes, featureflags.Suggestions, user.IsAdmin) {
		activity = append(activity, components.NavigationItem{Label: "Sugestões", Path: "/sugestoes"})
	}
	if !user.IsAdmin {
		activity = append(activity, components.NavigationItem{Label: "Frota", Path: "/fleet"})
	}

	var family []components.NavigationItem
	if !user.IsDependent {
		family = append(family, components.NavigationItem{Label: "Menores a cargo", Path: "/dashboard/guardian"})
	}
	var memberships []components.NavigationItem
	if user.Programmes["Leisure"] {
		memberships = append(memberships, components.NavigationItem{Label: "Lazer", Path: "/dashboard/leisure"})
	}
	if user.Programmes["Initiation"] {
		memberships = append(memberships, components.NavigationItem{Label: "Iniciação", Path: "/dashboard/initiation"})
	}
	if user.Programmes["Competition"] {
		memberships = append(memberships, components.NavigationItem{Label: "Competição", Path: "/dashboard/competition"})
	}
	if user.Programmes["Kayak_Polo"] {
		memberships = append(memberships, components.NavigationItem{Label: "Kayak polo", Path: "/dashboard/kayak-polo"})
	}

	var coordination []components.NavigationItem
	if user.IsAdmin || user.CanManageEvents {
		coordination = append(coordination, components.NavigationItem{Label: "Gerir eventos", Path: "/admin/eventos"}, components.NavigationItem{Label: "Planear treinos", Path: "/admin/treinos"}, components.NavigationItem{Label: "Gerir avisos", Path: "/admin/avisos"})
	}
	var moderation []components.NavigationItem
	if user.IsAdmin || user.CanModerateContent {
		moderation = append(moderation, components.NavigationItem{Label: "Gerir álbuns", Path: "/admin/albuns"})
		if featureflags.Available(user.FeatureModes, featureflags.Suggestions, user.IsAdmin) {
			moderation = append(moderation, components.NavigationItem{Label: "Triar sugestões", Path: "/admin/sugestoes"})
		}
	}
	var admin []components.NavigationItem
	if user.IsAdmin {
		admin = append(admin, components.NavigationItem{Label: "Membros", Path: "/admin/membros"}, components.NavigationItem{Label: "Notícias", Path: "/admin/noticias"}, components.NavigationItem{Label: "Gerir frota", Path: "/admin/fleet"}, components.NavigationItem{Label: "Sistema", Path: "/admin/sistema"})
	}

	groups := []components.NavigationGroup{{Items: today, Capabilities: dashboardCapabilities(user), Memberships: dashboardMemberships(user)}, {Label: "Atividade", Items: activity}}
	if len(family) > 0 {
		groups = append(groups, components.NavigationGroup{Label: "Família", Items: family})
	}
	if len(memberships) > 0 {
		groups = append(groups, components.NavigationGroup{Label: "Inscrições", Items: memberships})
	}
	if len(coordination) > 0 {
		groups = append(groups, components.NavigationGroup{Label: "Coordenação", Items: coordination})
	}
	if len(moderation) > 0 {
		groups = append(groups, components.NavigationGroup{Label: "Moderação", Items: moderation})
	}
	if len(admin) > 0 {
		groups = append(groups, components.NavigationGroup{Label: "Administração", Items: admin})
	}
	return groups
}

func dashboardCapabilities(user CurrentUser) []string {
	labels := []string{}
	if !user.IsDependent {
		labels = append(labels, "Tutor")
	}
	if user.CanManageEvents {
		labels = append(labels, "Treinador")
	}
	if user.CanModerateContent {
		labels = append(labels, "Moderador")
	}
	if user.IsAdmin {
		labels = append(labels, "Administrador")
	}
	return labels
}

func dashboardMemberships(user CurrentUser) []string {
	labels := []string{}
	for _, programme := range []struct{ key, label string }{{"Leisure", "Lazer"}, {"Initiation", "Iniciação"}, {"Competition", "Competição"}, {"Kayak_Polo", "Kayak polo"}} {
		if user.Programmes[programme.key] {
			labels = append(labels, programme.label)
		}
	}
	return labels
}
