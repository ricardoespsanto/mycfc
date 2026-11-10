package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const seasonsPath = "/admin/epocas"

type Seasons struct {
	Store    SeasonStore
	System   System
	PageMeta components.PageMeta
	Sessions *scs.SessionManager
	Now      func() time.Time
}

func strconvSeasonPage(n int) string { return strconv.Itoa(n) }
func seasonMemberCollectionQuery(page, q string) string {
	v := url.Values{}
	if page != "" {
		v.Set("page", page)
	}
	if q != "" {
		v.Set("q", q)
	}
	return v.Encode()
}
func seasonContext(r *http.Request) SeasonReturnContext {
	return SeasonReturnContext{Kind: r.FormValue("return_kind"), ID: r.FormValue("return_id"), WeekID: r.FormValue("week_id"), Page: r.FormValue("return_page"), Query: strings.TrimSpace(r.FormValue("return_q"))}
}
func seasonContextValues(c SeasonReturnContext) url.Values {
	v := url.Values{}
	if c.Kind != "" && c.Kind != "list" {
		v.Set("return_kind", c.Kind)
	}
	if c.ID != "" {
		v.Set("return_id", c.ID)
	}
	if c.WeekID != "" {
		v.Set("week_id", c.WeekID)
	}
	if c.Page != "" {
		v.Set("return_page", c.Page)
	}
	if c.Query != "" {
		v.Set("return_q", c.Query)
	}
	return v
}
func seasonContextURL(base string, c SeasonReturnContext) string {
	v := seasonContextValues(c)
	if len(v) == 0 {
		return base
	}
	return base + "?" + v.Encode()
}
func seasonSetupURL(c SeasonReturnContext) string { return seasonContextURL(seasonsPath, c) }
func seasonRow(s dbgen.Season, c SeasonReturnContext) pages.SeasonRow {
	return pages.SeasonRow{ID: s.ID.String(), Code: s.Code, Name: s.Name, StartsOn: s.StartsOn.Time.Format("2006-01-02"), EndsOn: s.EndsOn.Time.Format("2006-01-02"), Current: s.IsCurrent, SelectURL: seasonContextURL(seasonsPath+"/"+s.ID.String()+"/selecionar", c)}
}
func (h Seasons) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}
func (h Seasons) failure(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
	} else {
		h.System.InternalError(w, r)
	}
}
func (h Seasons) actor(ctx context.Context, w http.ResponseWriter, r *http.Request) (CurrentUser, bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	actor, ok := CurrentUserFromContext(r.Context())
	if !ok || h.Store == nil {
		h.System.NotFound(w, r)
		return actor, false
	}
	if err := h.Store.Authorize(ctx, actor.ID); err != nil {
		h.failure(w, r, err)
		return actor, false
	}
	return actor, true
}
func (h Seasons) page(ctx context.Context, w http.ResponseWriter, r *http.Request, actor CurrentUser, mode string) (pages.SeasonsPage, SeasonReturnContext, bool) {
	c, dest, err := h.Store.ResolveReturn(ctx, actor.ID, seasonContext(r))
	if err != nil {
		h.failure(w, r, err)
		return pages.SeasonsPage{}, c, false
	}
	meta := h.PageMeta
	meta.Title = "Épocas | MyCFCoimbra"
	meta.CurrentPath = seasonsPath
	meta.CurrentUserName = actor.Name
	meta.CurrentUserID = actor.ID.String()
	meta.Navigation = dashboardNavigation(actor)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return pages.SeasonsPage{Meta: meta, CSRFField: meta.CSRFField, Mode: mode, Form: pages.SeasonForm{Errors: validation.FieldErrors{}}, ContextKind: c.Kind, ContextID: c.ID, ContextWeekID: c.WeekID, ContextPage: c.Page, ContextQuery: c.Query, CollectionURL: seasonContextURL(seasonsPath, c), CreateURL: seasonContextURL(seasonsPath+"/criar", c), ReturnURL: dest}, c, true
}
func (h Seasons) render(w http.ResponseWriter, r *http.Request, status int, p pages.SeasonsPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.Seasons(p).Render(r.Context(), w)
}
func (h Seasons) Index(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	actor, ok := h.actor(ctx, w, r)
	if !ok {
		return
	}
	n, valid := trainingGroupPageNumber(r.URL.Query().Get("page"))
	if !valid {
		h.System.NotFound(w, r)
		return
	}
	rows, err := h.Store.ListSeasons(ctx, actor.ID, int32(n*20))
	if err != nil {
		h.failure(w, r, err)
		return
	}
	p, c, ok := h.page(ctx, w, r, actor, "list")
	if !ok {
		return
	}
	p.HasCurrent, err = h.Store.HasCurrentSeason(ctx, actor.ID)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	for _, row := range rows {
		p.Rows = append(p.Rows, seasonRow(row, c))
	}
	pageURL := func(page int) string {
		v := seasonContextValues(c)
		if page > 0 {
			v.Set("page", strconv.Itoa(page))
		}
		if len(v) == 0 {
			return seasonsPath
		}
		return seasonsPath + "?" + v.Encode()
	}
	if n > 0 {
		p.PreviousURL = pageURL(n - 1)
	}
	if len(rows) == 20 && n < 99 {
		p.NextURL = pageURL(n + 1)
	}
	if h.Sessions != nil {
		p.Success = h.Sessions.PopString(r.Context(), "seasons_flash")
	}
	h.render(w, r, 200, p)
}
func (h Seasons) CreatePage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	actor, ok := h.actor(ctx, w, r)
	if !ok {
		return
	}
	p, _, ok := h.page(ctx, w, r, actor, "create")
	if !ok {
		return
	}
	p.ActionURL = p.CreateURL
	h.render(w, r, 200, p)
}
func (h Seasons) Create(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	actor, ok := h.actor(ctx, w, r)
	if !ok {
		return
	}
	if r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	p, c, ok := h.page(ctx, w, r, actor, "create")
	if !ok {
		return
	}
	p.ActionURL = p.CreateURL
	p.Form = pages.SeasonForm{Code: strings.TrimSpace(r.PostForm.Get("code")), Name: strings.TrimSpace(r.PostForm.Get("name")), StartsOn: r.PostForm.Get("starts_on"), EndsOn: r.PostForm.Get("ends_on"), Errors: validation.FieldErrors{}}
	if n := utf8.RuneCountInString(p.Form.Code); n < 1 || n > 20 {
		p.Form.Errors.Add("code", "Indique um código de 1 a 20 caracteres.")
	}
	if n := utf8.RuneCountInString(p.Form.Name); n < 2 || n > 120 {
		p.Form.Errors.Add("name", "Indique um nome de 2 a 120 caracteres.")
	}
	start, e1 := validation.ParseISODate(p.Form.StartsOn)
	end, e2 := validation.ParseISODate(p.Form.EndsOn)
	if e1 != nil || start.Year() < 1 || start.Year() > 9999 {
		p.Form.Errors.Add("starts_on", "Introduza uma data de início válida.")
	}
	if e2 != nil || end.Year() < 1 || end.Year() > 9999 {
		p.Form.Errors.Add("ends_on", "Introduza uma data de fim válida.")
	}
	if e1 == nil && e2 == nil && start.After(end) {
		p.Form.Errors.Add("ends_on", "O fim deve ser igual ou posterior ao início.")
	}
	if !p.Form.Errors.Empty() {
		h.render(w, r, 422, p)
		return
	}
	created, err := h.Store.CreateSeason(ctx, actor.ID, SeasonInput{Code: p.Form.Code, Name: p.Form.Name, StartsOn: start, EndsOn: end})
	if errors.Is(err, ErrSeasonDuplicate) {
		p.Form.Errors.Add("code", "Já existe uma época com este código.")
		h.render(w, r, 422, p)
		return
	}
	if errors.Is(err, ErrSeasonInvalid) {
		p.Form.Errors.Add("form", "Reveja os dados da época.")
		h.render(w, r, 422, p)
		return
	}
	if err != nil {
		h.failure(w, r, err)
		return
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "seasons_flash", "Época criada. Selecione-a explicitamente para a tornar atual.")
	}
	httpx.Redirect(w, r, seasonContextURL(seasonsPath+"/"+created.ID.String()+"/selecionar", c), 303)
}
func seasonConfirmationBinding(actor, target uuid.UUID, c SeasonReturnContext) string {
	return confirmationBinding(actor, target, "SEASON_SELECTION", c)
}
func (h Seasons) selectionPage(ctx context.Context, w http.ResponseWriter, r *http.Request, actor CurrentUser) (pages.SeasonsPage, SeasonSelection, SeasonReturnContext, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		h.System.NotFound(w, r)
		return pages.SeasonsPage{}, SeasonSelection{}, SeasonReturnContext{}, false
	}
	state, err := h.Store.GetSelectionState(ctx, actor.ID, id)
	if err != nil {
		h.failure(w, r, err)
		return pages.SeasonsPage{}, state, SeasonReturnContext{}, false
	}
	p, c, ok := h.page(ctx, w, r, actor, "select")
	if !ok {
		return p, state, c, false
	}
	p.Target = seasonRow(state.Target, c)
	if state.Current != nil {
		p.Current = seasonRow(*state.Current, c)
		p.HasCurrent = true
	}
	p.ActionURL = p.Target.SelectURL
	p.ReviewURL = p.ActionURL
	return p, state, c, true
}
func (h Seasons) Selection(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	actor, ok := h.actor(ctx, w, r)
	if !ok {
		return
	}
	p, _, _, ok := h.selectionPage(ctx, w, r, actor)
	if !ok {
		return
	}
	if h.Sessions != nil {
		p.Success = h.Sessions.PopString(r.Context(), "seasons_flash")
	}
	h.render(w, r, 200, p)
}
func (h Seasons) Select(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	actor, ok := h.actor(ctx, w, r)
	if !ok {
		return
	}
	if r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	p, state, c, ok := h.selectionPage(ctx, w, r, actor)
	if !ok {
		return
	}
	binding := seasonConfirmationBinding(actor.ID, state.Target.ID, c)
	p.Mode = "preview"
	if r.PostForm.Get("confirm") != "yes" {
		p.Token = signClassificationConfirmation(binding, state.Version, h.now())
		h.render(w, r, 200, p)
		return
	}
	p.Token = r.PostForm.Get("preview_token")
	expected, valid := verifyClassificationConfirmation(p.Token, binding, h.now())
	if !valid {
		p.Form.Errors.Add("form", "A pré-visualização expirou ou mudou. Recarregue e reveja a época antes de confirmar.")
		h.render(w, r, 422, p)
		return
	}
	err := h.Store.SelectCurrentSeason(ctx, actor.ID, state.Target.ID, expected)
	if errors.Is(err, ErrSeasonConflict) {
		// Refresh the displayed selection and authority, retaining the original token
		// so that the user must explicitly review rather than silently rebasing it.
		fresh, _, _, ok := h.selectionPage(ctx, w, r, actor)
		if !ok {
			return
		}
		fresh.Mode = "preview"
		fresh.Token = p.Token
		fresh.Conflict = "A época atual mudou. Nada foi guardado; recarregue para rever antes de confirmar."
		h.render(w, r, 409, fresh)
		return
	}
	if err != nil {
		h.failure(w, r, err)
		return
	}
	// Revalidate the destination after committing rather than trusting a posted URL.
	_, dest, err := h.Store.ResolveReturn(ctx, actor.ID, c)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "seasons_flash", "Época atual selecionada. O histórico existente foi preservado.")
	}
	httpx.Redirect(w, r, dest, 303)
}
