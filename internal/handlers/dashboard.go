package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/locale"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/gorilla/csrf"
	"github.com/jackc/pgx/v5/pgtype"
)

const dashboardQueryTimeout = 5 * time.Second

type DashboardStore interface {
	ListRecentPerformanceMetrics(context.Context, dbgen.ListRecentPerformanceMetricsParams) ([]dbgen.PerformanceMetric, error)
	ListRecentTrainingLogs(context.Context, dbgen.ListRecentTrainingLogsParams) ([]dbgen.TrainingLog, error)
	ListPublishedNews(context.Context, int32) ([]dbgen.NewsItem, error)
	ListWhatsAppGroupsForRole(context.Context, dbgen.ListWhatsAppGroupsForRoleParams) ([]dbgen.WhatsappGroup, error)
	ListDependentsByGuardian(context.Context, dbgen.ListDependentsByGuardianParams) ([]dbgen.ListDependentsByGuardianRow, error)
}

type Dashboard struct {
	Store                 DashboardStore
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
	h.render(w, r, "Painel de administração", "Consulte a frota, pedidos de reparação e manutenção.", "Ainda não existem dados de frota para apresentar.", "/admin/fleet", []CalendarVM{h.calendar("Treinos", h.TrainingID), h.calendar("Competições", h.CompetitionID), h.calendar("Eventos sociais", h.SocialID), h.calendar("Ações de limpeza", h.CleanupsID)}, nil)
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
	_ = pages.Dashboard(pages.DashboardPage{Meta: view.Meta, Heading: view.Heading, Intro: view.Intro, EmptyText: view.EmptyText, Calendars: links, Sections: pageSections}).Render(r.Context(), w)
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
	page := pages.GuardianPage{
		Meta: meta, Dependents: pageItems, Calendars: guardianCalendarLinks(h.guardianCalendars(dependents)),
		Name: form.Name, DateOfBirth: form.DateOfBirth, Role: form.Role, Squad: form.Squad, Errors: form.Errors, Success: success,
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
