package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/gorilla/csrf"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const dashboardQueryTimeout = 5 * time.Second

type DashboardStore interface {
	ListRecentPerformanceMetrics(context.Context, dbgen.ListRecentPerformanceMetricsParams) ([]dbgen.PerformanceMetric, error)
	ListRecentTrainingLogs(context.Context, dbgen.ListRecentTrainingLogsParams) ([]dbgen.TrainingLog, error)
	ListPublishedNews(context.Context, int32) ([]dbgen.NewsItem, error)
	ListWhatsAppGroupsForUserProgramme(context.Context, dbgen.ListWhatsAppGroupsForUserProgrammeParams) ([]dbgen.WhatsappGroup, error)
	ListDependentsByGuardian(context.Context, dbgen.ListDependentsByGuardianParams) ([]dbgen.ListDependentsByGuardianRow, error)
	ListOperationalEquipment(context.Context, int32) ([]dbgen.Equipment, error)
}

type FleetStore interface {
	CountEquipmentByStatus(context.Context) ([]dbgen.CountEquipmentByStatusRow, error)
	ListEquipmentForAdmin(context.Context, int32) ([]dbgen.Equipment, error)
	ListPendingRepairRequests(context.Context, int32) ([]dbgen.ListPendingRepairRequestsRow, error)
	ListUpcomingMaintenance(context.Context, dbgen.ListUpcomingMaintenanceParams) ([]dbgen.ListUpcomingMaintenanceRow, error)
	ScheduleMaintenanceTask(context.Context, dbgen.ScheduleMaintenanceTaskParams) (dbgen.ScheduleMaintenanceTaskRow, error)
}

type Dashboard struct {
	Store                 DashboardStore
	Fleet                 FleetStore
	System                System
	PageMeta              components.PageMeta
	CompetitionID         string
	TrainingID            string
	SocialID              string
	CleanupsID            string
	CalendarAPIKey        string
	Location              *time.Location
	Dependents            GuardianDependentStore
	Now                   func() time.Time
	ResponsibilityVersion string
	ResponsibilitySHA256  string
	ResponsibilityURL     string
	Sessions              *scs.SessionManager
	Objects               storage.ObjectStore
}

func (h Dashboard) Competitor(w http.ResponseWriter, r *http.Request) {
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
	groups, err := h.groups(ctx, user.ID, "Competition", "Initiation", "Kayak_Polo")
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.render(w, r, "Painel de competidor", "Acompanhe os seus treinos, desempenho e agenda.", "Ainda não existem treinos ou métricas para apresentar.", "/dashboard/competitor", []CalendarVM{h.calendar("Treinos", h.TrainingID), h.calendar("Competições", h.CompetitionID)}, []DashboardSectionVM{
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
	h.render(w, r, "Painel de lazer", "Consulte as notícias e a agenda do clube.", "Ainda não existem notícias publicadas.", "/dashboard/leisure", []CalendarVM{h.calendar("Eventos sociais", h.SocialID), h.calendar("Ações de limpeza", h.CleanupsID)}, []DashboardSectionVM{
		{Heading: "Notícias", Empty: "Ainda não existem notícias publicadas.", Items: newsItems(news)},
		{Heading: "Grupos WhatsApp", Empty: "Ainda não existem grupos WhatsApp ativos.", Items: whatsappGroupItems(groups)},
	})
}
func (h Dashboard) Member(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "Área de membro", "A sua conta está ativa. As atividades do clube serão apresentadas quando a sua inscrição de época for atribuída.", "Ainda não tem inscrições de época ativas.", "/dashboard/member", nil, nil)
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

type fleetMaintenanceForm struct {
	EquipmentID, ScheduledFor, Description, Success string
	Errors                                          validation.FieldErrors
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
		h.renderMaintenanceForm(w, r, http.StatusOK, fleetMaintenanceForm{Success: "Manutenção agendada."})
		return
	}
	httpx.Redirect(w, r, "/admin/fleet", http.StatusSeeOther)
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
	page.Meta.Navigation = dashboardNavigation(user)
	page.Meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	page.MaintenanceForm.CSRFField = page.Meta.CSRFField
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
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.MaintenanceForm(pages.FleetMaintenanceForm{CSRFField: meta.CSRFField, Equipment: repairEquipment(equipment), Success: form.Success}).Render(r.Context(), w)
}

func (h Dashboard) fleetPage(ctx context.Context, r *http.Request, form fleetMaintenanceForm) (pages.FleetPage, error) {
	if h.Fleet == nil {
		return pages.FleetPage{}, errors.New("fleet store is not configured")
	}
	counts, err := h.Fleet.CountEquipmentByStatus(ctx)
	if err != nil {
		return pages.FleetPage{}, err
	}
	equipment, err := h.Fleet.ListEquipmentForAdmin(ctx, 501)
	if err != nil {
		return pages.FleetPage{}, err
	}
	repairs, err := h.Fleet.ListPendingRepairRequests(ctx, 50)
	if err != nil {
		return pages.FleetPage{}, err
	}
	now := h.now()
	maintenance, err := h.Fleet.ListUpcomingMaintenance(ctx, dbgen.ListUpcomingMaintenanceParams{FromTime: pgtype.Timestamptz{Time: now, Valid: true}, ToTime: pgtype.Timestamptz{Time: now.AddDate(0, 0, 90), Valid: true}, RowLimit: 50})
	if err != nil {
		return pages.FleetPage{}, err
	}
	calendars := guardianCalendarLinks([]CalendarVM{
		h.calendar("Treinos", h.TrainingID), h.calendar("Competições", h.CompetitionID),
		h.calendar("Eventos sociais", h.SocialID), h.calendar("Ações de limpeza", h.CleanupsID),
	})
	page := pages.FleetPage{Counts: fleetStatusCounts(counts), EquipmentCapped: len(equipment) > 500, CalendarAPIKey: h.CalendarAPIKey, CalendarSources: calendarSourceIDs(calendars), Calendars: calendars, MaintenanceForm: pages.FleetMaintenanceForm{EquipmentID: form.EquipmentID, ScheduledFor: form.ScheduledFor, Description: form.Description, Errors: form.Errors, Success: form.Success}}
	if page.EquipmentCapped {
		equipment = equipment[:500]
	}
	page.Equipment = make([]pages.FleetEquipment, len(equipment))
	for i, item := range equipment {
		page.Equipment[i] = pages.FleetEquipment{AssetTag: item.AssetTag, Name: item.Name, Type: item.Type, Status: item.Status}
	}
	page.Repairs = h.fleetRepairs(ctx, r, repairs)
	page.Maintenance = make([]pages.FleetMaintenance, len(maintenance))
	for i, task := range maintenance {
		page.Maintenance[i] = pages.FleetMaintenance{Equipment: task.AssetTag + " - " + task.EquipmentName, Description: task.Description, Status: task.Status, ScheduledFor: task.ScheduledFor.Time.In(h.location()).Format("02/01/2006 15:04")}
	}
	nonRetired, err := h.Store.ListOperationalEquipment(ctx, 500)
	if err != nil {
		return pages.FleetPage{}, err
	}
	page.MaintenanceForm.Equipment = repairEquipment(nonRetired)
	return page, nil
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
		items[i] = pages.FleetRepair{Equipment: repair.AssetTag + " - " + repair.EquipmentName, Description: repair.IssueDescription, Status: repair.Status, ReportedAt: repair.DateReported.Time.In(h.location()).Format("02/01/2006 15:04")}
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

func (h Dashboard) render(w http.ResponseWriter, r *http.Request, heading, intro, emptyText, path string, calendars []CalendarVM, sections []DashboardSectionVM) {
	user, _ := CurrentUserFromContext(r.Context())
	meta := h.PageMeta
	meta.Title = heading + " | MyCFC"
	meta.CurrentPath = path
	meta.CurrentUserName = user.Name
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	equipment, err := h.Store.ListOperationalEquipment(r.Context(), 500)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	choices := make([]components.RepairEquipment, len(equipment))
	for i, item := range equipment {
		choices[i] = components.RepairEquipment{ID: item.ID.String(), Label: item.AssetTag + " - " + item.Name}
	}
	view := DashboardVM{Meta: meta, Heading: heading, Intro: intro, EmptyText: emptyText, Calendars: calendars, Sections: sections}
	links := make([]pages.CalendarLink, len(view.Calendars))
	for i, calendar := range view.Calendars {
		links[i] = pages.CalendarLink{Label: calendar.Label, URL: calendar.URL, ID: calendar.ID}
	}
	pageSections := make([]pages.DashboardSection, len(view.Sections))
	for i, section := range view.Sections {
		items := make([]pages.DashboardItem, len(section.Items))
		for j, item := range section.Items {
			items[j] = pages.DashboardItem{Title: item.Title, Detail: item.Detail, URL: item.URL}
		}
		pageSections[i] = pages.DashboardSection{Heading: section.Heading, Empty: section.Empty, Items: items}
	}
	repairSuccess := ""
	if h.Sessions != nil {
		repairSuccess = h.Sessions.PopString(r.Context(), "repair_flash")
	}
	_ = pages.Dashboard(pages.DashboardPage{Meta: view.Meta, Heading: view.Heading, Intro: view.Intro, EmptyText: view.EmptyText, CalendarAPIKey: h.CalendarAPIKey, CalendarSources: calendarSourceIDs(links), Calendars: links, Sections: pageSections, RepairForm: components.RepairFormData{CSRFField: meta.CSRFField, IdempotencyKey: uuid.NewString(), Equipment: choices, Success: repairSuccess}}).Render(r.Context(), w)
}

func (h Dashboard) renderGuardian(w http.ResponseWriter, r *http.Request, status int, dependents []dbgen.ListDependentsByGuardianRow, form guardianDependentForm) {
	user, _ := CurrentUserFromContext(r.Context())
	meta := h.PageMeta
	meta.Title = "Painel de encarregado de educação | MyCFC"
	meta.CurrentPath = "/dashboard/guardian"
	meta.CurrentUserName = user.Name
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	items := guardianDependentItems(dependents, h.now(), h.location())
	pageItems := make([]pages.DashboardItem, len(items))
	for i, item := range items {
		pageItems[i] = pages.DashboardItem{Title: item.Title, Detail: item.Detail, URL: item.URL}
	}
	success := form.Success
	if success == "" && h.Sessions != nil {
		success = h.Sessions.PopString(r.Context(), "guardian_flash")
	}
	repairSuccess := ""
	if h.Sessions != nil {
		repairSuccess = h.Sessions.PopString(r.Context(), "repair_flash")
	}
	equipment, err := h.Store.ListOperationalEquipment(r.Context(), 500)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	choices := make([]components.RepairEquipment, len(equipment))
	for i, item := range equipment {
		choices[i] = components.RepairEquipment{ID: item.ID.String(), Label: item.AssetTag + " - " + item.Name}
	}
	page := pages.GuardianPage{
		Meta: meta, Dependents: pageItems,
		ResponsibilityURL: h.ResponsibilityURL, Name: form.Name, DateOfBirth: form.DateOfBirth, Errors: form.Errors, Success: success, RepairForm: components.RepairFormData{CSRFField: meta.CSRFField, IdempotencyKey: uuid.NewString(), Equipment: choices, Success: repairSuccess},
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if r.Header.Get("HX-Request") == "true" {
		_ = pages.GuardianContent(page).Render(r.Context(), w)
		return
	}
	_ = pages.Guardian(page).Render(r.Context(), w)
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

func guardianCalendarLinks(calendars []CalendarVM) []pages.CalendarLink {
	links := make([]pages.CalendarLink, len(calendars))
	for i, calendar := range calendars {
		links[i] = pages.CalendarLink{Label: calendar.Label, URL: calendar.URL, ID: calendar.ID}
	}
	return links
}

func calendarSourceIDs(calendars []pages.CalendarLink) string {
	ids := make([]string, len(calendars))
	for i, calendar := range calendars {
		ids[i] = calendar.ID
	}
	encoded, _ := json.Marshal(ids)
	return string(encoded)
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

func (h Dashboard) calendar(label, id string) CalendarVM {
	return CalendarVM{Label: label, URL: "https://calendar.google.com/calendar/u/0?cid=" + url.QueryEscape(id), ID: id}
}

func guardianDependentItems(dependents []dbgen.ListDependentsByGuardianRow, now time.Time, location *time.Location) []DashboardItemVM {
	items := make([]DashboardItemVM, len(dependents))
	for i, dependent := range dependents {
		age := validation.AgeOn(dependent.DateOfBirth.Time, now, location)
		items[i] = DashboardItemVM{Title: dependent.Name, Detail: fmt.Sprintf("%d anos", age)}
	}
	return items
}

func dashboardNavigation(user CurrentUser) []components.NavigationItem {
	items := []components.NavigationItem{{Label: "Painel", Path: "/dashboard"}, {Label: "Eventos", Path: "/events"}, {Label: "Menores a cargo", Path: "/dashboard/guardian"}}
	if user.Programmes["Leisure"] {
		items = append(items, components.NavigationItem{Label: "Lazer", Path: "/dashboard/leisure"})
	}
	if user.Programmes["Competition"] || user.Programmes["Initiation"] || user.Programmes["Kayak_Polo"] {
		items = append(items, components.NavigationItem{Label: "Competição", Path: "/dashboard/competitor"})
	}
	if user.IsAdmin {
		items = append(items, components.NavigationItem{Label: "Frota", Path: "/admin/fleet"})
	}
	return items
}
