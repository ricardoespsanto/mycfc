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
	"github.com/jackc/pgx/v5/pgtype"
)

const newsQueryTimeout = 5 * time.Second
const newsPageSize = 6

type NewsStore interface {
	ListNewsForAdmin(context.Context, dbgen.ListNewsForAdminParams) ([]dbgen.NewsItem, error)
	GetNewsForAdmin(context.Context, uuid.UUID) (dbgen.NewsItem, error)
	CreateNews(context.Context, dbgen.CreateNewsParams) (dbgen.NewsItem, error)
	PublishNews(context.Context, uuid.UUID) (int64, error)
	ExpireNews(context.Context, uuid.UUID) (int64, error)
}

type News struct {
	Store    NewsStore
	System   System
	PageMeta components.PageMeta
	Location *time.Location
	Sessions *scs.SessionManager
}

type newsForm struct {
	Title, Summary, URL, PublishedAt string
	Errors                           validation.FieldErrors
}

func (h News) Index(w http.ResponseWriter, r *http.Request) {
	h.renderIndex(w, r, http.StatusOK, newsForm{Errors: validation.FieldErrors{}})
}

func (h News) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderIndex(w, r, http.StatusBadRequest, newsForm{Errors: validation.FieldErrors{}})
		return
	}
	form := h.validate(r)
	if !form.Errors.Empty() {
		h.renderIndex(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	publishedAt, _ := time.ParseInLocation("2006-01-02T15:04", form.PublishedAt, h.location())
	var link *string
	if form.URL != "" {
		link = &form.URL
	}
	ctx, cancel := context.WithTimeout(r.Context(), newsQueryTimeout)
	defer cancel()
	if _, err := h.Store.CreateNews(ctx, dbgen.CreateNewsParams{TitlePt: form.Title, SummaryPt: form.Summary, Url: link, PublishedAt: pgtype.Timestamptz{Time: publishedAt, Valid: true}}); err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Rascunho guardado.")
	httpx.Redirect(w, r, "/admin/noticias", http.StatusSeeOther)
}

func (h News) Publish(w http.ResponseWriter, r *http.Request) { h.changeStatus(w, r, true) }
func (h News) Expire(w http.ResponseWriter, r *http.Request)  { h.changeStatus(w, r, false) }

func (h News) PublishPage(w http.ResponseWriter, r *http.Request) { h.renderStatusPage(w, r, true, "") }
func (h News) ExpirePage(w http.ResponseWriter, r *http.Request)  { h.renderStatusPage(w, r, false, "") }

func (h News) changeStatus(w http.ResponseWriter, r *http.Request, publish bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), newsQueryTimeout)
	defer cancel()
	var changed int64
	if publish {
		changed, err = h.Store.PublishNews(ctx, id)
	} else {
		changed, err = h.Store.ExpireNews(ctx, id)
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if changed == 0 {
		h.renderStatusPage(w, r, publish, "A notícia foi alterada entretanto. Reveja o estado atual antes de voltar a confirmar.")
		return
	}
	if publish {
		h.flash(r, "Notícia publicada.")
	} else {
		h.flash(r, "Notícia expirada.")
	}
	httpx.Redirect(w, r, h.collectionReturn(r), http.StatusSeeOther)
}

func (h News) renderStatusPage(w http.ResponseWriter, r *http.Request, publish bool, conflict string) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), newsQueryTimeout)
	defer cancel()
	item, err := h.Store.GetNewsForAdmin(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if conflict == "" && item.IsPublished == publish {
		if publish {
			conflict = "Esta notícia já está publicada. Reveja o estado atual antes de continuar."
		} else {
			conflict = "Esta notícia já está expirada. Reveja o estado atual antes de continuar."
		}
	}
	status := http.StatusOK
	if conflict != "" {
		status = http.StatusConflict
	}
	action := "expire"
	if publish {
		action = "publish"
	}
	page := pages.NewsStatusPage{Meta: h.meta(r), Item: h.item(item), CSRFField: templ.Raw(string(csrf.TemplateField(r))), ReturnURL: h.collectionReturn(r), Action: action, Conflict: conflict}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = pages.NewsStatus(page).Render(r.Context(), w)
}

func (h News) validate(r *http.Request) newsForm {
	f := newsForm{Title: strings.TrimSpace(r.PostForm.Get("title")), Summary: strings.TrimSpace(r.PostForm.Get("summary")), URL: strings.TrimSpace(r.PostForm.Get("url")), PublishedAt: strings.TrimSpace(r.PostForm.Get("published_at")), Errors: validation.FieldErrors{}}
	if n := utf8.RuneCountInString(f.Title); n < 2 || n > 180 {
		f.Errors.Add("title", "O título deve ter entre 2 e 180 caracteres.")
	}
	if n := utf8.RuneCountInString(f.Summary); n < 2 || n > 1000 {
		f.Errors.Add("summary", "O resumo deve ter entre 2 e 1000 caracteres.")
	}
	if f.URL != "" {
		u, err := url.ParseRequestURI(f.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			f.Errors.Add("url", "Introduza uma ligação HTTPS válida.")
		}
	}
	if _, err := time.ParseInLocation("2006-01-02T15:04", f.PublishedAt, h.location()); err != nil {
		f.Errors.Add("published_at", "Introduza uma data e hora de publicação válidas.")
	}
	return f
}

func (h News) renderIndex(w http.ResponseWriter, r *http.Request, status int, form newsForm) {
	ctx, cancel := context.WithTimeout(r.Context(), newsQueryTimeout)
	defer cancel()
	pageNumber := newsPageNumber(r.URL.Query().Get("page"))
	items, err := h.Store.ListNewsForAdmin(ctx, dbgen.ListNewsForAdminParams{RowLimit: newsPageSize + 1, RowOffset: int32((pageNumber - 1) * newsPageSize)})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	page := pages.NewsPage{Meta: h.meta(r), Form: pages.NewsForm{Title: form.Title, Summary: form.Summary, URL: form.URL, PublishedAt: form.PublishedAt, Errors: form.Errors, CSRFField: templ.Raw(string(csrf.TemplateField(r)))}}
	if h.Sessions != nil {
		page.Success = h.Sessions.PopString(r.Context(), "news_flash")
	}
	if pageNumber > 1 {
		page.PreviousURL = newsPageURL(pageNumber - 1)
	}
	if len(items) > newsPageSize {
		page.NextURL = newsPageURL(pageNumber + 1)
		items = items[:newsPageSize]
	}
	for _, item := range items {
		page.Items = append(page.Items, h.item(item))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.News(page).Render(r.Context(), w)
}

func (h News) flash(r *http.Request, message string) {
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "news_flash", message)
	}
}

func (h News) item(item dbgen.NewsItem) pages.NewsItem {
	return pages.NewsItem{ID: item.ID.String(), Title: item.TitlePt, PublishedAt: item.PublishedAt.Time.In(h.location()).Format("02/01/2006 15:04"), Published: item.IsPublished}
}

func (h News) collectionReturn(r *http.Request) string {
	raw := r.URL.Query().Get("return_to")
	if raw == "" && r.PostForm != nil {
		raw = r.PostForm.Get("return_to")
	}
	if safe := adminCollectionReturn(raw, "/admin/noticias"); safe != "" {
		return safe
	}
	return "/admin/noticias"
}

func newsPageNumber(value string) int {
	page, err := strconv.Atoi(value)
	if err != nil || page < 1 || page > 10000 {
		return 1
	}
	return page
}

func newsPageURL(page int) string { return "/admin/noticias?page=" + strconv.Itoa(page) }

func (h News) meta(r *http.Request) components.PageMeta {
	user, _ := CurrentUserFromContext(r.Context())
	meta := h.PageMeta
	meta.Title = "Notícias | MyCFCoimbra"
	meta.CurrentPath = "/admin/noticias"
	meta.CurrentUserName = user.Name
	meta.CurrentUserID = user.ID.String()
	meta.EmailVerificationPending = !user.IsDependent && !user.EmailVerified
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return meta
}

func (h News) location() *time.Location {
	if h.Location != nil {
		return h.Location
	}
	return time.UTC
}
