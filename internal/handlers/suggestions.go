package handlers

import (
	"context"
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
	"github.com/jackc/pgx/v5/pgtype"
)

const suggestionQueryTimeout = 5 * time.Second
const suggestionPageSize = 12

var suggestionCategories = []string{"FACILITIES", "EQUIPMENT", "TRAINING", "EVENTS", "COMMUNICATION", "OTHER"}
var suggestionStatuses = []string{"SUBMITTED", "UNDER_REVIEW", "PLANNED", "DECLINED", "COMPLETED"}

type SuggestionsStore interface {
	CreateSuggestion(context.Context, dbgen.CreateSuggestionParams) (dbgen.CreateSuggestionRow, error)
	ListSuggestionsForRequester(context.Context, dbgen.ListSuggestionsForRequesterParams) ([]dbgen.ListSuggestionsForRequesterRow, error)
	ListSuggestionsForTriage(context.Context, dbgen.ListSuggestionsForTriageParams) ([]dbgen.ListSuggestionsForTriageRow, error)
	UpdateSuggestionTriage(context.Context, dbgen.UpdateSuggestionTriageParams) (int64, error)
}

type Suggestions struct {
	Store    SuggestionsStore
	System   System
	PageMeta components.PageMeta
	Location *time.Location
	Sessions *scs.SessionManager
}

type suggestionForm struct {
	Category, Subject, Description string
	Errors                         validation.FieldErrors
}

type suggestionTriageForm struct {
	Status, Response, UpdatedAt string
	Errors                      validation.FieldErrors
}

func (h Suggestions) Index(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, suggestionForm{Errors: validation.FieldErrors{}}, suggestionTriageForm{Errors: validation.FieldErrors{}})
}

func (h Suggestions) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render(w, r, http.StatusBadRequest, suggestionForm{Errors: validation.FieldErrors{}}, suggestionTriageForm{Errors: validation.FieldErrors{}})
		return
	}
	form := validateSuggestion(r)
	if !form.Errors.Empty() {
		h.render(w, r, http.StatusUnprocessableEntity, form, suggestionTriageForm{Errors: validation.FieldErrors{}})
		return
	}
	user, _ := currentUser(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), suggestionQueryTimeout)
	defer cancel()
	if _, err := h.Store.CreateSuggestion(ctx, dbgen.CreateSuggestionParams{RequesterID: user.ID, Category: dbgen.SuggestionCategory(form.Category), Subject: form.Subject, Description: form.Description}); err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Sugestão enviada. Pode acompanhar o estado nesta página.")
	httpx.Redirect(w, r, "/sugestoes", http.StatusSeeOther)
}

func (h Suggestions) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.render(w, r, http.StatusBadRequest, suggestionForm{Errors: validation.FieldErrors{}}, suggestionTriageForm{Errors: validation.FieldErrors{}})
		return
	}
	form := validateSuggestionTriage(r)
	if !form.Errors.Empty() {
		h.render(w, r, http.StatusUnprocessableEntity, suggestionForm{Errors: validation.FieldErrors{}}, form)
		return
	}
	expected, _ := time.Parse(time.RFC3339Nano, form.UpdatedAt)
	var response *string
	if form.Response != "" {
		response = &form.Response
	}
	user, _ := currentUser(r.Context())
	actor := user.ID
	ctx, cancel := context.WithTimeout(r.Context(), suggestionQueryTimeout)
	defer cancel()
	changed, err := h.Store.UpdateSuggestionTriage(ctx, dbgen.UpdateSuggestionTriageParams{Status: dbgen.SuggestionStatus(form.Status), StaffResponse: response, ActorUserID: actor, ID: id, ExpectedUpdatedAt: pgtype.Timestamptz{Time: expected, Valid: true}})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if changed == 0 {
		h.render(w, r, http.StatusConflict, suggestionForm{Errors: validation.FieldErrors{}}, form)
		return
	}
	h.flash(r, "Sugestão atualizada.")
	httpx.Redirect(w, r, adminSuggestionsURL(r.URL.Query(), 1), http.StatusSeeOther)
}

func validateSuggestion(r *http.Request) suggestionForm {
	form := suggestionForm{Category: strings.TrimSpace(r.PostForm.Get("category")), Subject: strings.TrimSpace(r.PostForm.Get("subject")), Description: strings.TrimSpace(r.PostForm.Get("description")), Errors: validation.FieldErrors{}}
	if !containsSuggestionValue(suggestionCategories, form.Category) {
		form.Errors.Add("category", "Selecione uma categoria válida.")
	}
	if n := utf8.RuneCountInString(form.Subject); n < 3 || n > 160 {
		form.Errors.Add("subject", "O assunto deve ter entre 3 e 160 caracteres.")
	}
	if n := utf8.RuneCountInString(form.Description); n < 10 || n > 3000 {
		form.Errors.Add("description", "A sugestão deve ter entre 10 e 3000 caracteres.")
	}
	return form
}

func validateSuggestionTriage(r *http.Request) suggestionTriageForm {
	form := suggestionTriageForm{Status: strings.TrimSpace(r.PostForm.Get("status")), Response: strings.TrimSpace(r.PostForm.Get("staff_response")), UpdatedAt: strings.TrimSpace(r.PostForm.Get("updated_at")), Errors: validation.FieldErrors{}}
	if !containsSuggestionValue(suggestionStatuses, form.Status) {
		form.Errors.Add("status", "Selecione um estado válido.")
	}
	if n := utf8.RuneCountInString(form.Response); n == 1 || n > 2000 {
		form.Errors.Add("staff_response", "A resposta deve ter entre 2 e 2000 caracteres, ou ficar vazia.")
	}
	if (form.Status == "DECLINED" || form.Status == "COMPLETED") && form.Response == "" {
		form.Errors.Add("staff_response", "Explique a decisão antes de concluir ou não avançar com a sugestão.")
	}
	if parsed, err := time.Parse(time.RFC3339Nano, form.UpdatedAt); err != nil || parsed.IsZero() {
		form.Errors.Add("updated_at", "A sugestão foi alterada. Recarregue a página e tente novamente.")
	}
	return form
}

func containsSuggestionValue(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func (h Suggestions) render(w http.ResponseWriter, r *http.Request, status int, createForm suggestionForm, triageForm suggestionTriageForm) {
	user, _ := currentUser(r.Context())
	management := strings.HasPrefix(r.URL.Path, "/admin/")
	pageNumber := suggestionPageNumber(r.URL.Query().Get("page"))
	page := pages.SuggestionsPage{Management: management, CanTriage: user.IsAdmin || user.CanModerateContent, Meta: h.meta(r, user, management), Form: pages.SuggestionForm{Category: createForm.Category, Subject: createForm.Subject, Description: createForm.Description, Errors: createForm.Errors, CSRFField: templ.Raw(string(csrf.TemplateField(r)))}, TriageErrors: triageForm.Errors}
	if h.Sessions != nil {
		page.Success = h.Sessions.PopString(r.Context(), "suggestion_flash")
	}
	if status == http.StatusConflict {
		page.Conflict = "Esta sugestão foi atualizada por outra pessoa. Reveja os dados atuais antes de tentar novamente."
	}
	ctx, cancel := context.WithTimeout(r.Context(), suggestionQueryTimeout)
	defer cancel()
	if management {
		statusFilter := validSuggestionFilter(r.URL.Query().Get("status"), suggestionStatuses)
		categoryFilter := validSuggestionFilter(r.URL.Query().Get("category"), suggestionCategories)
		page.StatusFilter = stringValue(statusFilter)
		page.CategoryFilter = stringValue(categoryFilter)
		items, err := h.Store.ListSuggestionsForTriage(ctx, dbgen.ListSuggestionsForTriageParams{StatusFilter: statusFilter, CategoryFilter: categoryFilter, RowLimit: suggestionPageSize + 1, RowOffset: int32((pageNumber - 1) * suggestionPageSize)})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		if len(items) > suggestionPageSize {
			page.NextURL = adminSuggestionsURL(r.URL.Query(), pageNumber+1)
			items = items[:suggestionPageSize]
		}
		if pageNumber > 1 {
			page.PreviousURL = adminSuggestionsURL(r.URL.Query(), pageNumber-1)
		}
		for _, item := range items {
			page.Items = append(page.Items, pages.SuggestionItem{ID: item.ID.String(), RequesterName: item.RequesterName, Category: item.Category, Subject: item.Subject, Description: item.Description, Status: item.Status, StaffResponse: stringValue(item.StaffResponse), ResponderName: stringValue(item.ResponderName), CreatedAt: h.formatTime(item.CreatedAt.Time), RespondedAt: h.formatOptionalTime(item.RespondedAt), UpdatedAt: item.UpdatedAt.Time.Format(time.RFC3339Nano)})
		}
	} else {
		items, err := h.Store.ListSuggestionsForRequester(ctx, dbgen.ListSuggestionsForRequesterParams{RequesterID: user.ID, RowLimit: suggestionPageSize + 1, RowOffset: int32((pageNumber - 1) * suggestionPageSize)})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		if len(items) > suggestionPageSize {
			page.NextURL = suggestionsPageURL(pageNumber + 1)
			items = items[:suggestionPageSize]
		}
		if pageNumber > 1 {
			page.PreviousURL = suggestionsPageURL(pageNumber - 1)
		}
		for _, item := range items {
			page.Items = append(page.Items, pages.SuggestionItem{ID: item.ID.String(), Category: item.Category, Subject: item.Subject, Description: item.Description, Status: item.Status, StaffResponse: stringValue(item.StaffResponse), CreatedAt: h.formatTime(item.CreatedAt.Time), RespondedAt: h.formatOptionalTime(item.RespondedAt), UpdatedAt: item.UpdatedAt.Time.Format(time.RFC3339Nano)})
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.Suggestions(page).Render(r.Context(), w)
}

func validSuggestionFilter(value string, allowed []string) *string {
	if containsSuggestionValue(allowed, value) {
		return &value
	}
	return nil
}

func suggestionPageNumber(value string) int {
	page, err := strconv.Atoi(value)
	if err != nil || page < 1 || page > 10000 {
		return 1
	}
	return page
}

func suggestionsPageURL(page int) string { return "/sugestoes?page=" + strconv.Itoa(page) }

func adminSuggestionsURL(values url.Values, page int) string {
	query := url.Values{}
	if filter := validSuggestionFilter(values.Get("status"), suggestionStatuses); filter != nil {
		query.Set("status", *filter)
	}
	if filter := validSuggestionFilter(values.Get("category"), suggestionCategories); filter != nil {
		query.Set("category", *filter)
	}
	query.Set("page", strconv.Itoa(page))
	return "/admin/sugestoes?" + query.Encode()
}

func (h Suggestions) flash(r *http.Request, message string) {
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "suggestion_flash", message)
	}
}

func (h Suggestions) meta(r *http.Request, user CurrentUser, management bool) components.PageMeta {
	meta := h.PageMeta
	meta.Title = "Sugestões | MyCFCoimbra"
	meta.CurrentPath = "/sugestoes"
	meta.AreaLabel = "Atividade"
	if management {
		meta.Title = "Triar sugestões | MyCFCoimbra"
		meta.CurrentPath = "/admin/sugestoes"
		meta.AreaLabel = "Moderação"
		meta.PageLabel = "Triar sugestões"
	}
	if !management {
		meta.PageLabel = "Sugestões"
	}
	meta.CurrentUserName = user.Name
	meta.CurrentUserID = user.ID.String()
	meta.EmailVerificationPending = !user.IsDependent && !user.EmailVerified
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return meta
}

func (h Suggestions) formatTime(value time.Time) string {
	return value.In(h.location()).Format("02/01/2006 15:04")
}
func (h Suggestions) formatOptionalTime(value pgtype.Timestamptz) string {
	if !value.Valid {
		return ""
	}
	return h.formatTime(value.Time)
}
func (h Suggestions) location() *time.Location {
	if h.Location != nil {
		return h.Location
	}
	return time.UTC
}
