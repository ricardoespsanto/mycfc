package handlers

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestClassificationNameSearchContract(t *testing.T) {
	if _, ok := any(PostgresClassificationStore{}).(classificationSearcher); !ok {
		t.Fatal("classification store has no name-only selection search")
	}
}

func TestClassificationNavigationAndChangePerson(t *testing.T) {
	for _, tc := range []struct {
		name string
		user CurrentUser
		want bool
	}{
		{"admin", CurrentUser{IsAdmin: true}, true},
		{"programme coach", CurrentUser{CoachProgrammeIDs: map[uuid.UUID]bool{uuid.New(): true}}, true},
		{"team coach", CurrentUser{CanManageEvents: true, CoachTeamIDs: map[uuid.UUID]bool{uuid.New(): true}}, false},
		{"member", CurrentUser{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found := false
			for _, group := range dashboardNavigation(tc.user) {
				for _, item := range group.Items {
					if item.Path == "/equipa/classificacao" {
						found = true
						if group.Label != "Coordenação" {
							t.Fatal(group.Label)
						}
					}
				}
			}
			if found != tc.want {
				t.Fatalf("classification navigation present=%v want=%v", found, tc.want)
			}
		})
	}
	r := httptest.NewRequest("GET", "/equipa/classificacao/"+uuid.NewString()+"?q=Ana", nil)
	r.SetPathValue("id", uuid.NewString())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New()}))
	w := httptest.NewRecorder()
	(Classification{Store: &classificationFake{allowed: true, name: "Ana"}}).Get(w, r)
	if !strings.Contains(w.Body.String(), `href="/equipa/classificacao?q=Ana">Alterar pessoa</a>`) {
		t.Fatal("missing query-retaining change person link")
	}
}

type classificationSearchFake struct {
	classificationFake
	people []ClassificationPerson
	err    error
	query  string
	page   int
	more   bool
}

func (f *classificationSearchFake) Search(_ context.Context, _ uuid.UUID, q string, page int) ([]ClassificationPerson, bool, error) {
	f.query = q
	f.page = page
	return f.people, f.more, f.err
}

func TestClassificationSearchRetainsQueryAndFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{"empty", nil, 200, "Não foram encontradas pessoas disponíveis para classificação com este nome."},
		{"failure", errors.New("database private error"), 503, "Não foi possível pesquisar. Tente novamente."},
		{"denied", ErrClassificationDenied, 404, "404 page not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &classificationSearchFake{err: tc.err}
			r := httptest.NewRequest("GET", "/equipa/classificacao?q=Ana&page=9999999999999999", nil)
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New()}))
			w := httptest.NewRecorder()
			(Classification{Store: store}).Search(w, r)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.message) || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if store.query != "Ana" || store.page != 1 {
				t.Fatalf("query/page %q %d", store.query, store.page)
			}
			if tc.status != 404 && !strings.Contains(w.Body.String(), `value="Ana"`) {
				t.Fatal("lost query")
			}
			if strings.Contains(w.Body.String(), "database private error") {
				t.Fatal("private failure disclosed")
			}
		})
	}
}

func TestClassificationSearchEscapesNamesAndDoesNotAutoSelect(t *testing.T) {
	id := uuid.New()
	store := &classificationSearchFake{people: []ClassificationPerson{{ID: id, Name: `Ana <script>unsafe</script>`}}, more: true}
	r := httptest.NewRequest("GET", "/equipa/classificacao?q=Ana", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New()}))
	w := httptest.NewRecorder()
	(Classification{Store: store}).Search(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "Selecionar Ana &lt;script&gt;") || w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), "page=2") {
		t.Fatalf("unsafe search %d %s", w.Code, w.Body.String())
	}
}
