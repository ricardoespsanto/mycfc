package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
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

type newsStoreFake struct {
	changed   int64
	id        uuid.UUID
	published bool
}

func (s *newsStoreFake) ListNewsForAdmin(context.Context, int32) ([]dbgen.NewsItem, error) {
	return nil, nil
}
func (s *newsStoreFake) CreateNews(context.Context, dbgen.CreateNewsParams) (dbgen.NewsItem, error) {
	return dbgen.NewsItem{}, nil
}
func (s *newsStoreFake) PublishNews(_ context.Context, id uuid.UUID) (int64, error) {
	s.id, s.published = id, true
	return s.changed, nil
}
func (s *newsStoreFake) ExpireNews(_ context.Context, id uuid.UUID) (int64, error) {
	s.id, s.published = id, false
	return s.changed, nil
}
