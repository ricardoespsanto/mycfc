package handlers

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type definitionListFailure struct{ definitionFake }

func (*definitionListFailure) List(context.Context, uuid.UUID) ([]DefinitionRow, error) {
	return nil, errors.New("private-list-detail")
}

func TestDefinitionHTTPFailureAndValidationBoundaries(t *testing.T) {
	season, programme, actor := uuid.New(), uuid.New(), uuid.New()
	scope := season.String() + ":" + programme.String()
	scopes := []DefinitionScope{{SeasonID: season, ProgrammeID: programme, Season: "Época", Programme: "Competição"}}
	for _, tc := range []struct {
		name, method, body string
		store              DefinitionStore
		auth               bool
		want               int
	}{
		{"anonymous get", "GET", "", &definitionFake{}, false, 404},
		{"anonymous post", "POST", "", &definitionFake{}, false, 404},
		{"missing store get", "GET", "", nil, true, 503},
		{"missing store post", "POST", "", nil, true, 503},
		{"scope query get", "GET", "", &definitionFake{err: errors.New("private-detail")}, true, 503},
		{"scope query post", "POST", "scope=" + url.QueryEscape(scope), &definitionFake{err: errors.New("private-detail")}, true, 503},
		{"missing scope", "POST", "", &definitionFake{scopes: scopes}, true, 422},
		{"malformed scope", "POST", "scope=bad%3Abad", &definitionFake{scopes: scopes}, true, 404},
		{"invalid code", "POST", "scope=" + url.QueryEscape(scope) + "&code=BAD!&name=Nome", &definitionFake{scopes: scopes}, true, 422},
		{"invalid birthday", "POST", "scope=" + url.QueryEscape(scope) + "&code=ADULT&name=Nome&birth_date_from=invalid", &definitionFake{scopes: scopes}, true, 422},
		{"create scope revoked", "POST", "scope=" + url.QueryEscape(scope) + "&code=ADULT&name=Nome", &definitionFake{scopes: scopes, createErr: errDefinitionScope}, true, 404},
		{"create unavailable", "POST", "scope=" + url.QueryEscape(scope) + "&code=ADULT&name=Nome", &definitionFake{scopes: scopes, createErr: errors.New("private-detail")}, true, 503},
		{"list unavailable", "GET", "", &definitionListFailure{definitionFake{scopes: scopes}}, true, 503},
		{"bad encoding", "POST", "scope=%zz", &definitionFake{scopes: scopes}, true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/equipa/escaloes", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.auth {
				r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
			}
			w := httptest.NewRecorder()
			h := Definitions{Store: tc.store}
			if tc.method == "GET" {
				h.Get(w, r)
			} else {
				h.Post(w, r)
			}
			if w.Code != tc.want || strings.Contains(w.Body.String(), "private-") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestDefinitionAndClassificationStoresFailClosedWithoutDatabase(t *testing.T) {
	ctx := t.Context()
	actor, member := uuid.New(), uuid.New()
	pool, err := pgxpool.New(ctx, "postgres://synthetic@localhost/mycfc")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	for _, pool := range []*pgxpool.Pool{nil, pool} {
		d := PostgresDefinitionStore{Pool: pool}
		if _, err := d.Scopes(ctx, actor); err == nil {
			t.Fatal("scope query passed unavailable database")
		}
		if err := d.Create(ctx, actor, DefinitionInput{}); err == nil {
			t.Fatal("definition write passed unavailable database")
		}
		if pool != nil {
			if _, err := d.List(ctx, actor); err == nil {
				t.Fatal("list passed closed database")
			}
		}
		s := PostgresClassificationStore{Pool: pool}
		if _, _, err := s.View(ctx, actor, member); err == nil {
			t.Fatal("view passed unavailable database")
		}
		if _, err := s.Options(ctx, actor, member); err == nil {
			t.Fatal("options passed unavailable database")
		}
		if _, _, err := s.Taxonomy(ctx, member); err == nil {
			t.Fatal("taxonomy passed unavailable database")
		}
		if _, _, err := s.Selections(ctx, actor, member); err == nil {
			t.Fatal("selections passed unavailable database")
		}
		if _, err := s.CanCorrect(ctx, actor, member); err == nil {
			t.Fatal("correction passed unavailable database")
		}
		if _, _, err := s.Search(ctx, actor, "Ana", 1); err == nil {
			t.Fatal("search passed unavailable database")
		}
		if _, err := s.EligibilityOnWrite(ctx, actor, member, uuid.New(), uuid.New(), uuid.New()); err == nil {
			t.Fatal("eligibility passed unavailable database")
		}
	}
	if err := (PostgresClassificationStore{}).Write(ctx, ClassificationWrite{AgeExceptionReason: "Mismatch"}); err == nil {
		t.Fatal("exception without category accepted")
	}
}

func TestClassificationStylesheetMaintainsCSPAndFocusContract(t *testing.T) {
	w := httptest.NewRecorder()
	ClassificationStylesheet(w, httptest.NewRequest("GET", "/equipa/classificacao.css", nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/css; charset=utf-8" || w.Header().Get("Cache-Control") != "public, max-age=3600" || !strings.Contains(w.Body.String(), ":focus-visible") || !strings.Contains(w.Body.String(), "forced-colors") {
		t.Fatal("same-origin stylesheet contract missing")
	}
}

func TestClassificationSearchBoundsAndPreviousPage(t *testing.T) {
	for _, query := range []string{"Ana", strings.Repeat("A", 101)} {
		s := &classificationSearchFake{}
		r := httptest.NewRequest("GET", "/equipa/classificacao?q="+query+"&page=2", nil)
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New()}))
		w := httptest.NewRecorder()
		(Classification{Store: s}).Search(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		if len(query) > 100 && s.query != "" {
			t.Fatal("overlong query reached database")
		}
		if query == "Ana" && !strings.Contains(w.Body.String(), `href="/equipa/classificacao?q=Ana">Anterior</a>`) {
			t.Fatal("previous page missing")
		}
	}
	w := httptest.NewRecorder()
	(Classification{Store: &classificationSearchFake{}}).Search(w, httptest.NewRequest("GET", "/equipa/classificacao", nil))
	if w.Code != 404 {
		t.Fatal("anonymous search accepted")
	}
}
