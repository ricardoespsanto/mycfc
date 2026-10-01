package handlers

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type definitionFake struct {
	scopes    []DefinitionScope
	created   int
	actor     uuid.UUID
	err       error
	createErr error
}

func (f *definitionFake) Scopes(_ context.Context, _ uuid.UUID) ([]DefinitionScope, error) {
	return f.scopes, f.err
}
func (f *definitionFake) Create(_ context.Context, actor uuid.UUID, in DefinitionInput) error {
	f.created++
	f.actor = actor
	if f.createErr != nil {
		return f.createErr
	}
	return f.err
}

func TestDefinitionsDuplicateRetainsFieldsAndTargetsCode(t *testing.T) {
	actor, season, programme := uuid.New(), uuid.New(), uuid.New()
	f := &definitionFake{scopes: []DefinitionScope{{SeasonID: season, ProgrammeID: programme, Season: "Época", Programme: "Competição"}}, err: errDefinitionDuplicate}
	// Allow the scope lookup, then return a duplicate from Create.
	f.err = nil
	f.createErr = errDefinitionDuplicate
	values := url.Values{"scope": {season.String() + ":" + programme.String()}, "code": {"JOVENS"}, "name": {"Jovens"}, "birth_date_from": {"2009-01-01"}, "birth_date_to": {"2010-12-31"}}
	r := httptest.NewRequest("POST", "/equipa/escaloes", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
	w := httptest.NewRecorder()
	(Definitions{Store: f}).Post(w, r)
	body := w.Body.String()
	for _, part := range []string{`href="#code"`, `id="code"`, `aria-invalid="true"`, `value="JOVENS"`, `value="Jovens"`, `value="2009-01-01"`, `value="2010-12-31"`} {
		if !strings.Contains(body, part) || w.Code != 409 {
			t.Fatalf("status=%d missing %s", w.Code, part)
		}
	}
}

func TestDefinitionsRejectForgedScopeAndActor(t *testing.T) {
	actor := uuid.New()
	season := uuid.New()
	programme := uuid.New()
	f := &definitionFake{scopes: []DefinitionScope{{SeasonID: season, ProgrammeID: programme, Season: "Época", Programme: "Competição"}}}
	h := Definitions{Store: f}
	for _, tc := range []struct {
		name   string
		values url.Values
		want   int
	}{
		{"forged actor", url.Values{"scope": {season.String() + ":" + programme.String()}, "code": {"SUB"}, "name": {"Sub"}, "actor_id": {uuid.NewString()}}, 422},
		{"wrong scope", url.Values{"scope": {uuid.NewString() + ":" + programme.String()}, "code": {"SUB"}, "name": {"Sub"}}, 404},
		{"reversed DOB", url.Values{"scope": {season.String() + ":" + programme.String()}, "code": {"SUB"}, "name": {"Sub"}, "birth_date_from": {"2012-01-01"}, "birth_date_to": {"2011-01-01"}}, 422},
		{"valid", url.Values{"scope": {season.String() + ":" + programme.String()}, "code": {"SUB"}, "name": {"Sub"}}, 303},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/equipa/escaloes", strings.NewReader(tc.values.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
			w := httptest.NewRecorder()
			h.Post(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
		})
	}
	if f.created != 1 || f.actor != actor {
		t.Fatalf("creation count=%d actor=%s", f.created, f.actor)
	}
}
