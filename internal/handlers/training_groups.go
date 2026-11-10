package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const trainingGroupsPath = "/admin/treinos/estruturados/grupos"

// TrainingGroups keeps group setup independent of the planner's routines,
// variations, profiles and publications. Every protected read goes through Store.
type TrainingGroups struct {
	Setup    TrainingSetupStore
	Store    TrainingGroupStore
	System   System
	PageMeta components.PageMeta
	Sessions *scs.SessionManager
}

func trainingGroupPageNumber(raw string) (int, bool) {
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	return n, err == nil && n >= 0 && n < 100
}
func trainingGroupScope(raw string) (programme, team *uuid.UUID, valid bool) {
	kind, value, ok := strings.Cut(raw, ":")
	if !ok {
		return nil, nil, false
	}
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return nil, nil, false
	}
	switch kind {
	case "programme":
		return &id, nil, true
	case "team":
		return nil, &id, true
	}
	return nil, nil, false
}
func trainingGroupVersion(raw string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	return t, err == nil && !t.IsZero()
}
func trainingGroupRow(group TrainingGroupRecord) pages.TrainingGroupRow {
	planner := "/admin/treinos/estruturados?group_id=" + group.ID.String()
	if group.LatestWeekID != nil {
		planner += "&week_id=" + group.LatestWeekID.String()
	}
	return pages.TrainingGroupRow{ID: group.ID.String(), Name: group.Name, ScopeName: group.ScopeName, Version: group.UpdatedAt.Format(time.RFC3339Nano), MemberCount: int(group.MemberCount), HasWeek: group.LatestWeekID != nil, URL: trainingGroupsPath + "/" + group.ID.String(), PlannerURL: planner}
}
func trainingGroupAthletes(rows []TrainingGroupAthlete) []pages.TrainingGroupAthleteChoice {
	out := make([]pages.TrainingGroupAthleteChoice, 0, len(rows))
	for _, row := range rows {
		out = append(out, pages.TrainingGroupAthleteChoice{MembershipID: row.MembershipID.String(), Name: row.Name, AlreadyMember: row.AlreadyMember})
	}
	return out
}
func (h TrainingGroups) actor(w http.ResponseWriter, r *http.Request) (CurrentUser, bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	actor, ok := CurrentUserFromContext(r.Context())
	if !ok || h.Store == nil {
		h.System.NotFound(w, r)
		return actor, false
	}
	return actor, true
}
func (h TrainingGroups) failure(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
	} else {
		h.System.InternalError(w, r)
	}
}
func (h TrainingGroups) group(ctx context.Context, w http.ResponseWriter, r *http.Request, actor uuid.UUID) (TrainingGroupRecord, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		h.System.NotFound(w, r)
		return TrainingGroupRecord{}, false
	}
	group, err := h.Store.GetGroup(ctx, actor, id)
	if err != nil {
		h.failure(w, r, err)
		return group, false
	}
	return group, true
}
func (h TrainingGroups) page(r *http.Request, actor CurrentUser, mode string) pages.TrainingGroupsPage {
	meta := h.PageMeta
	meta.Title = "Grupos de treino | MyCFCoimbra"
	meta.CurrentPath = trainingGroupsPath
	meta.CurrentUserName, meta.CurrentUserID = actor.Name, actor.ID.String()
	meta.EmailVerificationPending = !actor.IsDependent && !actor.EmailVerified
	meta.Navigation = dashboardNavigation(actor)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return pages.TrainingGroupsPage{Meta: meta, CSRFField: meta.CSRFField, Mode: mode, CollectionURL: trainingGroupsPath, CreateURL: trainingGroupsPath + "/criar", ReturnURL: trainingGroupsPath, Form: pages.TrainingGroupForm{Errors: validation.FieldErrors{}, SelectedIDs: map[string]bool{}}}
}
func (h TrainingGroups) render(w http.ResponseWriter, r *http.Request, status int, page pages.TrainingGroupsPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.TrainingGroups(page).Render(r.Context(), w)
}

// The optional resolver retains an authorised older week without adding broad
// planner data reads to group setup. Unverified context is simply discarded.
type trainingGroupWeekResolver interface {
	AllowsWeek(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error)
}

func trainingGroupContextURL(base, week string) string {
	if week == "" {
		return base
	}
	u, _ := url.Parse(base)
	q := u.Query()
	q.Set("week_id", week)
	u.RawQuery = q.Encode()
	return u.String()
}
func (h TrainingGroups) groupPage(r *http.Request, actor CurrentUser, mode string, g TrainingGroupRecord) pages.TrainingGroupsPage {
	p := h.page(r, actor, mode)
	p.Group = trainingGroupRow(g)
	base := p.Group.URL
	if requested, err := uuid.Parse(r.FormValue("week_id")); err == nil && requested != uuid.Nil {
		verified := g.LatestWeekID != nil && requested == *g.LatestWeekID
		if !verified {
			if resolver, ok := h.Store.(trainingGroupWeekResolver); ok {
				ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
				allowed, err := resolver.AllowsWeek(ctx, actor.ID, g.ID, requested)
				cancel()
				verified = err == nil && allowed
			}
		}
		if verified {
			p.WeekID = requested.String()
			u, _ := url.Parse(p.Group.PlannerURL)
			v := u.Query()
			v.Set("week_id", p.WeekID)
			u.RawQuery = v.Encode()
			p.Group.PlannerURL = u.String()
		}
	}
	p.GroupURL = trainingGroupContextURL(base, p.WeekID)
	p.Group.URL = p.GroupURL
	p.AddURL = trainingGroupContextURL(base+"/atletas", p.WeekID)
	p.RenameURL = trainingGroupContextURL(base+"/nome", p.WeekID)
	p.ReturnURL = p.GroupURL
	p.PlannerURL = p.Group.PlannerURL
	p.Form.Version = p.Group.Version
	return p
}

func trainingGroupURL(base, q string, page int) string {
	v := url.Values{}
	if q != "" {
		v.Set("q", q)
	}
	if page > 0 {
		v.Set("page", strconv.Itoa(page))
	}
	if len(v) == 0 {
		return base
	}
	return base + "?" + v.Encode()
}
func (h TrainingGroups) flash(r *http.Request, message string) {
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "training_groups_flash", message)
	}
}

func (h TrainingGroups) Index(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	n, valid := trainingGroupPageNumber(r.URL.Query().Get("page"))
	if !valid {
		h.System.NotFound(w, r)
		return
	}
	groups, err := h.Store.ListGroups(ctx, actor.ID, int32(n*20))
	if err != nil {
		h.failure(w, r, err)
		return
	}
	scopes, err := h.Store.ListScopes(ctx, actor.ID)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	p := h.page(r, actor, "list")
	p.CanCreate = len(scopes) > 0
	for _, g := range groups {
		p.Groups = append(p.Groups, trainingGroupRow(g))
	}
	if n > 0 {
		p.PreviousURL = trainingGroupURL(trainingGroupsPath, "", n-1)
	}
	if len(groups) == 20 && n < 99 {
		p.NextURL = trainingGroupURL(trainingGroupsPath, "", n+1)
	}
	h.render(w, r, http.StatusOK, p)
}
func (h TrainingGroups) createPage(ctx context.Context, w http.ResponseWriter, r *http.Request, actor CurrentUser, status int, form pages.TrainingGroupForm) {
	scopes, err := h.Store.ListScopes(ctx, actor.ID)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	p := h.page(r, actor, "create")
	p.Form = form
	p.CanCreate = len(scopes) > 0
	p.ActionURL = p.CreateURL
	for _, scope := range scopes {
		value := ""
		if scope.ProgrammeID != nil {
			value = "programme:" + scope.ProgrammeID.String()
		} else if scope.TeamID != nil {
			value = "team:" + scope.TeamID.String()
		}
		p.Scopes = append(p.Scopes, pages.TrainingGroupScopeChoice{Value: value, Name: scope.Name})
	}
	if len(scopes) == 0 && r.Method == http.MethodPost {
		h.System.NotFound(w, r)
		return
	}
	h.render(w, r, status, p)
}
func (h TrainingGroups) CreatePage(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	h.createPage(ctx, w, r, actor, http.StatusOK, pages.TrainingGroupForm{Errors: validation.FieldErrors{}})
}
func (h TrainingGroups) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	form := pages.TrainingGroupForm{Name: strings.TrimSpace(r.PostForm.Get("name")), Scope: r.PostForm.Get("scope"), Errors: validation.FieldErrors{}}
	name, err := validation.NormalizeName(form.Name)
	if err != nil {
		form.Errors.Add("name", err.Error())
	}
	programme, team, valid := trainingGroupScope(form.Scope)
	if !valid {
		form.Errors.Add("scope", "Escolha a participação deste grupo.")
	}
	if !form.Errors.Empty() {
		h.createPage(ctx, w, r, actor, http.StatusUnprocessableEntity, form)
		return
	}
	group, err := h.Store.CreateGroup(ctx, TrainingGroupCreateInput{ActorID: actor.ID, Name: name, ProgrammeID: programme, TeamID: team})
	if errors.Is(err, ErrTrainingGroupEligibility) {
		form.Errors.Add("scope", "A participação escolhida já não está disponível.")
		h.createPage(ctx, w, r, actor, http.StatusUnprocessableEntity, form)
		return
	}
	if err != nil {
		h.failure(w, r, err)
		return
	}
	h.flash(r, "Grupo de treino criado.")
	httpx.Redirect(w, r, trainingGroupsPath+"/"+group.ID.String(), http.StatusSeeOther)
}
func (h TrainingGroups) Detail(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	g, ok := h.group(ctx, w, r, actor.ID)
	if !ok {
		return
	}
	n, valid := trainingGroupPageNumber(r.URL.Query().Get("page"))
	if !valid {
		h.System.NotFound(w, r)
		return
	}
	roster, err := h.Store.ListRoster(ctx, actor.ID, g.ID, int32(n*20))
	if err != nil {
		h.failure(w, r, err)
		return
	}
	p := h.groupPage(r, actor, "detail", g)
	p.Roster = trainingGroupAthletes(roster)
	if h.Sessions != nil {
		p.Success = h.Sessions.PopString(r.Context(), "training_groups_flash")
	}
	if n > 0 {
		p.RosterPreviousURL = trainingGroupContextURL(trainingGroupURL(strings.Split(p.GroupURL, "?")[0], "", n-1), p.WeekID)
	}
	if len(roster) == 20 && n < 99 {
		p.RosterNextURL = trainingGroupContextURL(trainingGroupURL(strings.Split(p.GroupURL, "?")[0], "", n+1), p.WeekID)
	}
	if err := h.applySetup(ctx, r, actor, g, &p); err != nil {
		h.failure(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, p)
}
func (h TrainingGroups) task(ctx context.Context, w http.ResponseWriter, r *http.Request, actor CurrentUser, g TrainingGroupRecord, mode string, status int, form pages.TrainingGroupForm, conflict string) {
	// Recheck authority after mutation failures before retaining any task data.
	current, err := h.Store.GetGroup(ctx, actor.ID, g.ID)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	p := h.groupPage(r, actor, mode, current)
	p.Form = form
	p.Conflict = conflict
	if mode == "rename" {
		p.ActionURL = p.RenameURL
	} else {
		p.ActionURL = p.AddURL
		p.SearchPerformed = form.Query != ""
		n, valid := trainingGroupPageNumber(r.FormValue("page"))
		if !valid {
			n = 0
		}
		p.SearchPage = n
		if form.Query != "" && !form.Errors.Has("q") {
			rows, err := h.Store.SearchAthletes(ctx, actor.ID, g.ID, form.Query, int32(n*20))
			if err != nil {
				h.failure(w, r, err)
				return
			}
			p.Results = trainingGroupAthletes(rows)
			if n > 0 {
				p.PreviousURL = trainingGroupContextURL(trainingGroupURL(strings.Split(p.AddURL, "?")[0], form.Query, n-1), p.WeekID)
			}
			if len(rows) == 20 && n < 99 {
				p.NextURL = trainingGroupContextURL(trainingGroupURL(strings.Split(p.AddURL, "?")[0], form.Query, n+1), p.WeekID)
			}
		}
	}
	if err := h.applySetup(ctx, r, actor, current, &p); err != nil {
		h.failure(w, r, err)
		return
	}
	if mode == "rename" {
		p.ActionURL = p.RenameURL
	} else {
		p.ActionURL = p.AddURL
	}
	p.ReviewURL = groupContextDayURL(trainingGroupURL(strings.Split(p.ActionURL, "?")[0], form.Query, 0), p.WeekID, p.ContextDay)
	h.render(w, r, status, p)
}
func (h TrainingGroups) Athletes(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	g, ok := h.group(ctx, w, r, actor.ID)
	if !ok {
		return
	}
	form := pages.TrainingGroupForm{Version: g.UpdatedAt.Format(time.RFC3339Nano), Query: strings.TrimSpace(r.URL.Query().Get("q")), SelectedIDs: map[string]bool{}, Errors: validation.FieldErrors{}}
	if utf8.RuneCountInString(form.Query) > 100 {
		form.Errors.Add("q", "Use até 100 caracteres na pesquisa.")
	}
	if _, valid := trainingGroupPageNumber(r.URL.Query().Get("page")); !valid {
		h.System.NotFound(w, r)
		return
	}
	h.task(ctx, w, r, actor, g, "add", http.StatusOK, form, "")
}
func (h TrainingGroups) AddAthletes(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	g, ok := h.group(ctx, w, r, actor.ID)
	if !ok {
		return
	}
	if r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	form := pages.TrainingGroupForm{Version: r.PostForm.Get("version"), Query: strings.TrimSpace(r.PostForm.Get("q")), SelectedIDs: map[string]bool{}, Errors: validation.FieldErrors{}}
	if utf8.RuneCountInString(form.Query) > 100 {
		form.Errors.Add("q", "Use até 100 caracteres na pesquisa.")
	}
	expected, valid := trainingGroupVersion(form.Version)
	if !valid {
		form.Errors.Add("form", "Recarregue o grupo antes de adicionar atletas.")
	}
	var ids []uuid.UUID
	seen := map[uuid.UUID]bool{}
	rawIDs := r.PostForm["membership_id"]
	if len(rawIDs) == 0 || len(rawIDs) > 20 {
		form.Errors.Add("members", "Selecione entre um e vinte atletas desta página.")
	}
	for _, raw := range rawIDs {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil || seen[id] {
			form.Errors.Add("members", "Selecione apenas atletas válidos, sem repetições.")
			continue
		}
		seen[id] = true
		ids = append(ids, id)
		form.SelectedIDs[id.String()] = true
	}
	if !form.Errors.Empty() {
		h.task(ctx, w, r, actor, g, "add", http.StatusUnprocessableEntity, form, "")
		return
	}
	result, err := h.Store.AddAthletes(ctx, TrainingGroupRosterInput{ActorID: actor.ID, GroupID: g.ID, MembershipIDs: ids, ExpectedUpdatedAt: expected})
	if errors.Is(err, ErrTrainingGroupConflict) {
		h.task(ctx, w, r, actor, g, "add", http.StatusConflict, form, "O grupo foi alterado desde que abriu esta tarefa. Reveja os dados antes de guardar.")
		return
	}
	if errors.Is(err, ErrTrainingGroupEligibility) {
		form.Errors.Add("members", "Um ou mais atletas já não estão elegíveis para este grupo. Reveja a seleção.")
		h.task(ctx, w, r, actor, g, "add", http.StatusUnprocessableEntity, form, "")
		return
	}
	if err != nil {
		h.failure(w, r, err)
		return
	}
	message := fmt.Sprintf("%s: %d atletas adicionados. Reveja a publicação da semana; as adições não publicam instruções.", result.Group.Name, result.Added)
	if result.Added == 0 {
		message = "As pessoas selecionadas já pertencem ao grupo. Nenhuma adição foi necessária."
	}
	h.flash(r, message)
	httpx.Redirect(w, r, h.setupGroupDestination(ctx, r, actor, g), http.StatusSeeOther)
}
func (h TrainingGroups) RenamePage(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	g, ok := h.group(ctx, w, r, actor.ID)
	if !ok {
		return
	}
	h.task(ctx, w, r, actor, g, "rename", http.StatusOK, pages.TrainingGroupForm{Name: g.Name, Version: g.UpdatedAt.Format(time.RFC3339Nano), Errors: validation.FieldErrors{}}, "")
}
func (h TrainingGroups) Rename(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	g, ok := h.group(ctx, w, r, actor.ID)
	if !ok {
		return
	}
	if r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	form := pages.TrainingGroupForm{Name: strings.TrimSpace(r.PostForm.Get("name")), Version: r.PostForm.Get("version"), Errors: validation.FieldErrors{}}
	name, err := validation.NormalizeName(form.Name)
	if err != nil {
		form.Errors.Add("name", err.Error())
	}
	expected, valid := trainingGroupVersion(form.Version)
	if !valid {
		form.Errors.Add("form", "Recarregue o grupo antes de alterar o nome.")
	}
	if !form.Errors.Empty() {
		h.task(ctx, w, r, actor, g, "rename", http.StatusUnprocessableEntity, form, "")
		return
	}
	_, err = h.Store.RenameGroup(ctx, TrainingGroupRenameInput{ActorID: actor.ID, GroupID: g.ID, Name: name, ExpectedUpdatedAt: expected})
	if errors.Is(err, ErrTrainingGroupConflict) {
		h.task(ctx, w, r, actor, g, "rename", http.StatusConflict, form, "O grupo foi alterado desde que abriu esta tarefa. Reveja os dados antes de guardar.")
		return
	}
	if errors.Is(err, ErrTrainingGroupEligibility) {
		form.Errors.Add("name", "Introduza um nome válido para o grupo.")
		h.task(ctx, w, r, actor, g, "rename", http.StatusUnprocessableEntity, form, "")
		return
	}
	if err != nil {
		h.failure(w, r, err)
		return
	}
	h.flash(r, "Nome do grupo alterado; as semanas existentes foram preservadas.")
	httpx.Redirect(w, r, h.setupGroupDestination(ctx, r, actor, g), http.StatusSeeOther)
}
