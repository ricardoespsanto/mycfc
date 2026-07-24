package handlers

import (
	"context"
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
	"github.com/cfcoimbra/mycfc/internal/locale"
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
	ListWhatsAppGroupsForRole(context.Context, dbgen.ListWhatsAppGroupsForRoleParams) ([]dbgen.WhatsappGroup, error)
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
	Location              *time.Location
	Dependents            GuardianDependentStore
	Now                   func() time.Time
	ResponsibilityVersion string
	ResponsibilitySHA256  string
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
	groups, err := h.groups(ctx, "Competitor")
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
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	news, err := h.Store.ListPublishedNews(ctx, 10)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	groups, err := h.groups(ctx, "Leisure")
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.render(w, r, "Painel de lazer", "Consulte as notícias e a agenda do clube.", "Ainda não existem notícias publicadas.", "/dashboard/leisure", []CalendarVM{h.calendar("Eventos sociais", h.SocialID), h.calendar("Ações de limpeza", h.CleanupsID)}, []DashboardSectionVM{
		{Heading: "Notícias", Empty: "Ainda não existem notícias publicadas.", Items: newsItems(news)},
		{Heading: "Grupos WhatsApp", Empty: "Ainda não existem grupos WhatsApp ativos.", Items: whatsappGroupItems(groups)},
	})
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
	page.Meta.Navigation = dashboardNavigation(user.Role)
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
	meta.Navigation = dashboardNavigation(user.Role)
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
	page := pages.FleetPage{Counts: fleetStatusCounts(counts), EquipmentCapped: len(equipment) > 500, MaintenanceForm: pages.FleetMaintenanceForm{EquipmentID: form.EquipmentID, ScheduledFor: form.ScheduledFor, Description: form.Description, Errors: form.Errors, Success: form.Success}}
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
	meta.Navigation = dashboardNavigation(user.Role)
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
		links[i] = pages.CalendarLink{Label: calendar.Label, URL: calendar.URL}
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
	_ = pages.Dashboard(pages.DashboardPage{Meta: view.Meta, Heading: view.Heading, Intro: view.Intro, EmptyText: view.EmptyText, Calendars: links, Sections: pageSections, RepairForm: components.RepairFormData{CSRFField: meta.CSRFField, IdempotencyKey: uuid.NewString(), Equipment: choices, Success: repairSuccess}}).Render(r.Context(), w)
}

func (h Dashboard) renderGuardian(w http.ResponseWriter, r *http.Request, status int, dependents []dbgen.ListDependentsByGuardianRow, form guardianDependentForm) {
	user, _ := CurrentUserFromContext(r.Context())
	meta := h.PageMeta
	meta.Title = "Painel de encarregado de educação | MyCFC"
	meta.CurrentPath = "/dashboard/guardian"
	meta.CurrentUserName = user.Name
	meta.Navigation = dashboardNavigation(user.Role)
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
		Meta: meta, Dependents: pageItems, Calendars: guardianCalendarLinks(h.guardianCalendars(dependents)),
		Name: form.Name, DateOfBirth: form.DateOfBirth, Role: form.Role, Squad: form.Squad, Errors: form.Errors, Success: success, RepairForm: components.RepairFormData{CSRFField: meta.CSRFField, IdempotencyKey: uuid.NewString(), Equipment: choices, Success: repairSuccess},
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
		links[i] = pages.CalendarLink{Label: calendar.Label, URL: calendar.URL}
	}
	return links
}

func (h Dashboard) groups(ctx context.Context, role string) ([]dbgen.WhatsappGroup, error) {
	return h.Store.ListWhatsAppGroupsForRole(ctx, dbgen.ListWhatsAppGroupsForRoleParams{TargetRole: role, SquadCategory: nil, RowLimit: 50})
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
	return CalendarVM{Label: label, URL: "https://calendar.google.com/calendar/u/0?cid=" + url.QueryEscape(id)}
}

func (h Dashboard) guardianCalendars(dependents []dbgen.ListDependentsByGuardianRow) []CalendarVM {
	calendars := make([]CalendarVM, 0, 4)
	seen := map[string]bool{}
	add := func(label, id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			calendars = append(calendars, h.calendar(label, id))
		}
	}
	for _, dependent := range dependents {
		switch dependent.Role {
		case "Competitor":
			add("Treinos", h.TrainingID)
			add("Competições", h.CompetitionID)
		case "Leisure":
			add("Eventos sociais", h.SocialID)
			add("Ações de limpeza", h.CleanupsID)
		}
	}
	return calendars
}

func guardianDependentItems(dependents []dbgen.ListDependentsByGuardianRow, now time.Time, location *time.Location) []DashboardItemVM {
	items := make([]DashboardItemVM, len(dependents))
	for i, dependent := range dependents {
		role := dependent.Role
		squad := dependent.SquadCategory
		age := validation.AgeOn(dependent.DateOfBirth.Time, now, location)
		sources := "Eventos sociais e ações de limpeza"
		if role == "Competitor" {
			sources = "Treinos e competições"
		}
		items[i] = DashboardItemVM{Title: dependent.Name, Detail: fmt.Sprintf("%s · %s · %d anos · %s", locale.Role(role), locale.Squad(squad), age, sources)}
	}
	return items
}

func dashboardNavigation(role string) []components.NavigationItem {
	items := []components.NavigationItem{{Label: "Painel", Path: "/dashboard"}}
	switch role {
	case "Admin":
		return append(items, components.NavigationItem{Label: "Frota", Path: "/admin/fleet"})
	case "Competitor":
		return append(items, components.NavigationItem{Label: "Competidor", Path: "/dashboard/competitor"})
	case "Leisure":
		return append(items, components.NavigationItem{Label: "Lazer", Path: "/dashboard/leisure"})
	case "Guardian":
		return append(items, components.NavigationItem{Label: "Menores a cargo", Path: "/dashboard/guardian"})
	}
	return items
}
