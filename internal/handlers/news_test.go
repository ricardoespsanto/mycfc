package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestNewsValidation(t *testing.T) {
	h := News{Location: time.UTC}
	for _, tc := range []struct {
		name   string
		values url.Values
		field  string
	}{
		{"accepts a scheduled HTTPS news item", url.Values{"title": {"Regata do Mondego"}, "summary": {"Inscrições abertas para a regata."}, "url": {"https://example.com/regata"}, "published_at": {"2026-07-30T10:00"}}, ""},
		{"rejects non HTTPS link", url.Values{"title": {"Regata do Mondego"}, "summary": {"Inscrições abertas para a regata."}, "url": {"http://example.com/regata"}, "published_at": {"2026-07-30T10:00"}}, "url"},
		{"requires a publication time", url.Values{"title": {"Regata do Mondego"}, "summary": {"Inscrições abertas para a regata."}}, "published_at"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/admin/noticias", strings.NewReader(tc.values.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			form := h.validate(r)
			if tc.field == "" && !form.Errors.Empty() {
				t.Fatalf("errors = %#v", form.Errors)
			}
			if tc.field != "" && !form.Errors.Has(tc.field) {
				t.Fatalf("errors = %#v, want %q", form.Errors, tc.field)
			}
		})
	}
}

func TestNewsStatusTransitions(t *testing.T) {
	id := uuid.New()
	store := &newsStoreFake{}
	h := News{Store: store}
	for _, tc := range []struct {
		name    string
		publish bool
	}{
		{"publishes a draft", true},
		{"expires a published item", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store.changed = 1
			r := httptest.NewRequest(http.MethodPost, "/admin/noticias/"+id.String(), nil)
			r.SetPathValue("id", id.String())
			w := httptest.NewRecorder()
			h.changeStatus(w, r, tc.publish)
			if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/noticias" {
				t.Fatalf("response = %d %q", w.Code, w.Header().Get("Location"))
			}
			if store.id != id || store.published != tc.publish {
				t.Fatalf("store = %#v", store)
			}
		})
	}
}

func TestNewsCreatePersistsScheduledDraft(t *testing.T) {
	store := &newsStoreFake{}
	h := News{Store: store, Location: time.UTC}
	values := url.Values{
		"title":        {"  Regata do Mondego  "},
		"summary":      {"  Inscrições abertas para a regata.  "},
		"url":          {"https://example.com/regata"},
		"published_at": {"2026-07-30T10:00"},
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/noticias", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/noticias" {
		t.Fatalf("response = %d %q", w.Code, w.Header().Get("Location"))
	}
	p := store.created
	if p.TitlePt != "Regata do Mondego" || p.SummaryPt != "Inscrições abertas para a regata." || p.Url == nil || *p.Url != "https://example.com/regata" {
		t.Fatalf("created = %#v", p)
	}
	if !p.PublishedAt.Valid || !p.PublishedAt.Time.Equal(time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("published_at = %#v", p.PublishedAt)
	}
}

func TestNewsCreateRejectsInvalidDraftAndHandlesStoreFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		values    url.Values
		createErr error
		want      int
	}{
		{"validation error", url.Values{"title": {"x"}, "summary": {"x"}, "published_at": {"invalid"}}, nil, http.StatusUnprocessableEntity},
		{"store error", url.Values{"title": {"Notícia"}, "summary": {"Resumo válido"}, "published_at": {"2026-07-30T10:00"}}, errors.New("database unavailable"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &newsStoreFake{createErr: tc.createErr}
			h := News{Store: store, Location: time.UTC}
			r := httptest.NewRequest(http.MethodPost, "/admin/noticias", strings.NewReader(tc.values.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			h.Create(w, r)

			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
			if tc.createErr == nil && store.created.TitlePt != "" {
				t.Fatalf("invalid draft was created: %#v", store.created)
			}
		})
	}
}

func TestNewsStatusRejectsInvalidTransition(t *testing.T) {
	store := &newsStoreFake{}
	h := News{Store: store}
	id := uuid.New()
	r := httptest.NewRequest(http.MethodPost, "/admin/noticias/"+id.String(), nil)
	r.SetPathValue("id", id.String())
	w := httptest.NewRecorder()
	h.Publish(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestNewsStatusPagesNameTheNewsAndRejectInvalidLifecycle(t *testing.T) {
	id := uuid.New()
	store := &newsStoreFake{item: dbgen.NewsItem{ID: id, TitlePt: "Regata do Mondego", PublishedAt: pgtype.Timestamptz{Time: time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC), Valid: true}}}
	h := News{Store: store, Location: time.UTC}
	request := httptest.NewRequest(http.MethodGet, "/admin/noticias/"+id.String()+"/publicar?return_to=%2Fadmin%2Fnoticias%3Fpage%3D2", nil)
	request.SetPathValue("id", id.String())
	response := httptest.NewRecorder()

	h.PublishPage(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Regata do Mondego") || !strings.Contains(response.Body.String(), `action="/admin/noticias/`+id.String()+`/publicar"`) || !strings.Contains(response.Body.String(), `href="/admin/noticias?page=2"`) {
		t.Fatalf("response = %d, body = %s", response.Code, response.Body.String())
	}

	store.item.IsPublished = true
	request = httptest.NewRequest(http.MethodGet, "/admin/noticias/"+id.String()+"/publicar", nil)
	request.SetPathValue("id", id.String())
	response = httptest.NewRecorder()
	h.PublishPage(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "já está publicada") {
		t.Fatalf("invalid lifecycle = %d, body = %s", response.Code, response.Body.String())
	}

	store.itemErr = pgx.ErrNoRows
	request = httptest.NewRequest(http.MethodGet, "/admin/noticias/"+id.String()+"/expirar", nil)
	request.SetPathValue("id", id.String())
	response = httptest.NewRecorder()
	h.ExpirePage(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing news = %d", response.Code)
	}
}

func TestNewsExpireChangesStatusAndMapsWriteFailure(t *testing.T) {
	id := uuid.New()
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/admin/noticias/"+id.String()+"/expirar", nil)
		r.SetPathValue("id", id.String())
		return r
	}
	t.Run("expires", func(t *testing.T) {
		store := &newsStoreFake{changed: 1}
		w := httptest.NewRecorder()
		(News{Store: store}).Expire(w, request())
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/noticias" || store.id != id || store.published {
			t.Fatalf("response=%d location=%q store=%#v", w.Code, w.Header().Get("Location"), store)
		}
	})
	t.Run("write failure", func(t *testing.T) {
		store := &newsStoreFake{changed: 1, expireErr: errors.New("database unavailable")}
		w := httptest.NewRecorder()
		(News{Store: store}).Expire(w, request())
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("response=%d", w.Code)
		}
	})
}

func TestNewsIndexPaginates(t *testing.T) {
	store := &newsStoreFake{}
	for i := 0; i < newsPageSize+1; i++ {
		store.items = append(store.items, dbgen.NewsItem{ID: uuid.New(), TitlePt: "Notícia"})
	}
	h := News{Store: store, Location: time.UTC}
	r := httptest.NewRequest(http.MethodGet, "/admin/noticias?page=2", nil)
	w := httptest.NewRecorder()

	h.Index(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if store.listParams != (dbgen.ListNewsForAdminParams{RowLimit: newsPageSize + 1, RowOffset: newsPageSize}) {
		t.Fatalf("params = %#v", store.listParams)
	}
	if !strings.Contains(w.Body.String(), "/admin/noticias?page=1") || !strings.Contains(w.Body.String(), "/admin/noticias?page=3") {
		t.Fatalf("body = %q", w.Body.String())
	}
}

type newsStoreFake struct {
	changed    int64
	id         uuid.UUID
	published  bool
	items      []dbgen.NewsItem
	item       dbgen.NewsItem
	itemErr    error
	listParams dbgen.ListNewsForAdminParams
	created    dbgen.CreateNewsParams
	createErr  error
	expireErr  error
}

func (s *newsStoreFake) ListNewsForAdmin(_ context.Context, params dbgen.ListNewsForAdminParams) ([]dbgen.NewsItem, error) {
	s.listParams = params
	return s.items, nil
}
func (s *newsStoreFake) GetNewsForAdmin(context.Context, uuid.UUID) (dbgen.NewsItem, error) {
	return s.item, s.itemErr
}
func (s *newsStoreFake) CreateNews(_ context.Context, params dbgen.CreateNewsParams) (dbgen.NewsItem, error) {
	s.created = params
	return dbgen.NewsItem{}, s.createErr
}
func (s *newsStoreFake) PublishNews(_ context.Context, id uuid.UUID) (int64, error) {
	s.id, s.published = id, true
	return s.changed, nil
}
func (s *newsStoreFake) ExpireNews(_ context.Context, id uuid.UUID) (int64, error) {
	s.id, s.published = id, false
	return s.changed, s.expireErr
}
