package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
)

type classificationFake struct {
	allowed                      bool
	name                         string
	rows                         []ClassificationRow
	writes                       int
	lastWrite                    ClassificationWrite
	err                          error
	writeErr                     error
	selectedMods, selectedCrafts []ClassificationTaxon
	lastCorrection               db.SportAssignmentCorrection
	correctionErr                error
	categories                   []ClassificationCategory
	code                         string
	datedVersion                 string
}

func (f *classificationFake) Versions(context.Context, uuid.UUID, uuid.UUID) (string, string, error) {
	if f.datedVersion != "" {
		return "sport-version", f.datedVersion, nil
	}
	return "sport-version", "dated-version", nil
}

func (f *classificationFake) View(_ context.Context, actor, member uuid.UUID) (string, []ClassificationRow, error) {
	if !f.allowed {
		return "", nil, ErrClassificationDenied
	}
	return f.name, f.rows, f.err
}
func (f *classificationFake) Options(_ context.Context, actor, member uuid.UUID) ([]ClassificationOption, error) {
	if !f.allowed {
		return nil, ErrClassificationDenied
	}
	return []ClassificationOption{{SeasonID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), ProgrammeID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Season: "2026", Programme: "Lazer", Code: f.code, TeamName: "Polo partilhada", SeasonEndsOn: "2026-12-31", Categories: f.categories}}, nil
}
func (f *classificationFake) Selections(context.Context, uuid.UUID, uuid.UUID) ([]ClassificationTaxon, []ClassificationTaxon, error) {
	if !f.allowed {
		return nil, nil, ErrClassificationDenied
	}
	return f.selectedMods, f.selectedCrafts, nil
}
func (f *classificationFake) Taxonomy(context.Context, uuid.UUID) ([]ClassificationTaxon, []ClassificationTaxon, error) {
	return []ClassificationTaxon{{"CANOEING", "Canoagem"}, {"SUP", "SUP"}}, []ClassificationTaxon{{"K1", "Caiaque individual"}}, nil
}
func (f *classificationFake) ReplaceSelections(_ context.Context, in db.SportAssignmentCorrection) error {
	f.writes++
	f.lastCorrection = in
	if f.correctionErr != nil {
		return f.correctionErr
	}
	return f.err
}
func (f *classificationFake) Write(_ context.Context, in ClassificationWrite) error {
	f.lastWrite = in
	f.writes++
	if f.writeErr != nil {
		return f.writeErr
	}
	return f.err
}
func TestClassificationRouteDeniesUnscopedMemberWithoutDisclosure(t *testing.T) {
	f := &classificationFake{name: "Segredo"}
	h := Classification{Store: f}
	id := uuid.New()
	r := httptest.NewRequest("GET", "/equipa/classificacao/"+id.String(), nil)
	r.SetPathValue("id", id.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New()}))
	w := httptest.NewRecorder()
	h.Get(w, r)
	if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "Segredo") {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}
}
func TestClassificationInvalidSubmissionRetainsValuesWithoutWriting(t *testing.T) {
	f := &classificationFake{allowed: true, name: "Ana"}
	h := Classification{Store: f}
	id := uuid.New()
	v := url.Values{"season_id": {"11111111-1111-1111-1111-111111111111"}, "programme_id": {"22222222-2222-2222-2222-222222222222"}, "starts_on": {"2020-01-01"}}
	r := httptest.NewRequest("POST", "/equipa/classificacao/"+id.String(), strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", id.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New()}))
	w := httptest.NewRecorder()
	h.Post(w, r)
	if path := os.Getenv("CLASSIFICATION_ERROR_HTML_OUT"); path != "" {
		if err := os.WriteFile(path, w.Body.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "2020-01-01") || !strings.Contains(w.Body.String(), "error-summary") || f.writes != 0 {
		t.Fatalf("status %d writes %d body %s", w.Code, f.writes, w.Body.String())
	}
}
func TestClassificationRejectsForgedActorWithoutWriting(t *testing.T) {
	f := &classificationFake{allowed: true, name: "Ana"}
	h := Classification{Store: f}
	member, actor, forged := uuid.New(), uuid.New(), uuid.New()
	v := url.Values{"scope": {"11111111-1111-1111-1111-111111111111:22222222-2222-2222-2222-222222222222"}, "starts_on": {time.Now().In(lisbonLocation()).Format("2006-01-02")}, "actor_id": {forged.String()}}
	r := httptest.NewRequest("POST", "/equipa/classificacao/"+member.String(), strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", member.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
	w := httptest.NewRecorder()
	h.Post(w, r)
	if w.Code != 422 || f.writes != 0 || f.lastWrite.ActorID != uuid.Nil || strings.Contains(w.Body.String(), "Ana — Guardada:") {
		t.Fatalf("status %d writes %d actor %s", w.Code, f.writes, f.lastWrite.ActorID)
	}
}
func TestClassificationGetRendersAccessibleTask(t *testing.T) {
	f := &classificationFake{allowed: true, name: "Ana", rows: []ClassificationRow{{ID: uuid.New().String(), Season: "2026", Programme: "Lazer", StartsOn: "2026-09-01"}}}
	h := Classification{Store: f}
	id := uuid.New()
	r := httptest.NewRequest("GET", "/equipa/classificacao/"+id.String(), nil)
	r.SetPathValue("id", id.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New()}))
	w := httptest.NewRecorder()
	h.Get(w, r)
	if path := os.Getenv("CLASSIFICATION_HTML_OUT"); path != "" {
		if err := os.WriteFile(path, w.Body.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Alterar participação: Ana") || !strings.Contains(w.Body.String(), "fieldset") || !strings.Contains(w.Body.String(), "2026-09-01") || !strings.Contains(w.Body.String(), "Guardar participação") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestClassificationDefinitionLinkRequiresOwnScope(t *testing.T) {
	member, actor := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name   string
		scopes []DefinitionScope
		err    error
		want   bool
	}{
		{"eligible", []DefinitionScope{{SeasonID: uuid.New(), ProgrammeID: uuid.New()}}, nil, true},
		{"no competition scope", nil, nil, false},
		{"lookup fails closed", nil, errors.New("db unavailable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &classificationFake{allowed: true, name: "Ana"}
			d := &definitionFake{scopes: tc.scopes, err: tc.err}
			r := httptest.NewRequest("GET", "/equipa/classificacao/"+member.String(), nil)
			r.SetPathValue("id", member.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
			w := httptest.NewRecorder()
			(Classification{Store: f, Definitions: d}).Get(w, r)
			if got := strings.Contains(w.Body.String(), `href="/equipa/escaloes"`); got != tc.want || w.Code != 200 {
				t.Fatalf("status %d link=%t want=%t", w.Code, got, tc.want)
			}
		})
	}
}

func TestClassificationState(t *testing.T) {
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		start, end string
		want       string
	}{{"2026-10-01", "", "Agendada"}, {"2026-09-01", "", "Em vigor"}, {"2026-08-01", "2026-08-31", "Terminada"}} {
		row := ClassificationRow{StartsOn: tc.start, EndsOn: tc.end}
		if got := classificationState([]ClassificationRow{row}, now); got != tc.want {
			t.Errorf("%+v: %s", tc, got)
		}
	}
	if got := classificationState(nil, now); got != "Sem participação ativa" {
		t.Fatal(got)
	}
}

func TestClassificationReplaceFullSetAndRemoval(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		codes []string
		want  db.SportAssignmentKind
	}{
		{"SPORT", []string{"CANOEING", "SUP"}, db.SportingModalityAssignment},
		{"CRAFT", nil, db.CanoeCraftAssignment},
	} {
		f := &classificationFake{allowed: true, name: "Ana"}
		id, actor := uuid.New(), uuid.New()
		v := url.Values{"kind": {tc.kind}, "codes": tc.codes, "reason": {"Correção solicitada pela equipa"}, "actor_id": {uuid.NewString()}, "original_token": {signClassificationConfirmation(confirmationBinding(actor, id, tc.kind, nil), "sport-version", time.Now())}}
		r := httptest.NewRequest("POST", "/equipa/classificacao/"+id.String()+"/selecoes", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", id.String())
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
		w := httptest.NewRecorder()
		Classification{Store: f}.ReplaceSelections(w, r)
		if w.Code != 200 || f.writes != 1 || f.lastCorrection.ActorID != actor || f.lastCorrection.MemberID != id || f.lastCorrection.Kind != tc.want || strings.Join(f.lastCorrection.Codes, ",") != strings.Join(tc.codes, ",") || f.lastCorrection.Reason != "Correção solicitada pela equipa" || !strings.Contains(w.Body.String(), "Ana") {
			t.Fatalf("%s: status=%d correction=%+v body=%s", tc.kind, w.Code, f.lastCorrection, w.Body.String())
		}
	}
}

func TestClassificationReplaceRejectsInvalidAndPreservesSubmission(t *testing.T) {
	for _, tc := range []struct {
		allowed      bool
		kind, reason string
		codes        []string
		status       int
	}{
		{false, "SPORT", "reason", []string{"SUP"}, 404},
		{true, "SPORT", "", []string{"SUP"}, 422},
		{true, "CRAFT", "reason", []string{"BAD"}, 422},
		{true, "SPORT", "reason", []string{"SUP", "SUP"}, 422},
		{true, "OTHER", "reason", nil, 422},
		{true, "CRAFT", "reason", []string{"K1"}, 422},
	} {
		f := &classificationFake{allowed: tc.allowed, name: "Ana"}
		v := url.Values{"kind": {tc.kind}, "reason": {tc.reason}, "codes": tc.codes}
		id, actor := uuid.New(), uuid.New()
		v.Set("original_token", signClassificationConfirmation(confirmationBinding(actor, id, tc.kind, nil), "sport-version", time.Now()))
		r := httptest.NewRequest("POST", "/equipa/classificacao/"+id.String()+"/selecoes", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", id.String())
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
		w := httptest.NewRecorder()
		Classification{Store: f}.ReplaceSelections(w, r)
		if w.Code != tc.status || f.writes != 0 {
			t.Fatalf("%+v: %d writes=%d", tc, w.Code, f.writes)
		}
		if tc.allowed && (!strings.Contains(w.Body.String(), "error-summary") || !strings.Contains(w.Body.String(), tc.reason)) {
			t.Fatalf("missing retained error: %s", w.Body.String())
		}
	}
}

func TestClassificationCorrectionFormPreselectsCurrentAndRetainsConflict(t *testing.T) {
	f := &classificationFake{allowed: true, name: "Ana", selectedMods: []ClassificationTaxon{{Code: "CANOEING", Name: "Canoagem"}}, selectedCrafts: []ClassificationTaxon{{Code: "K1", Name: "K1"}}}
	id, actor := uuid.New(), uuid.New()
	request := func(method string, values url.Values) *httptest.ResponseRecorder {
		if method == "POST" {
			values.Set("original_token", signClassificationConfirmation(confirmationBinding(actor, id, values.Get("kind"), nil), "sport-version", time.Now()))
		}
		r := httptest.NewRequest(method, "/equipa/classificacao/"+id.String()+"/selecoes", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", id.String())
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
		w := httptest.NewRecorder()
		if method == "GET" {
			(Classification{Store: f}).Get(w, r)
		} else {
			(Classification{Store: f}).ReplaceSelections(w, r)
		}
		return w
	}
	get := request("GET", nil)
	if get.Code != 200 || !strings.Contains(get.Body.String(), `value="CANOEING" checked`) || !strings.Contains(get.Body.String(), `value="K1" checked`) || !strings.Contains(get.Body.String(), "Remover Canoagem remove também todas") {
		t.Fatalf("current rendered status=%d body=%s", get.Code, get.Body.String())
	}
	f.correctionErr = errors.New("concurrent correction")
	w := request("POST", url.Values{"kind": {"SPORT"}, "codes": {"SUP"}, "reason": {"Motivo <revisto>"}})
	if w.Code != 409 || !strings.Contains(w.Body.String(), `value="SUP" checked`) || strings.Contains(w.Body.String(), `value="CANOEING" checked`) || !strings.Contains(w.Body.String(), "Motivo &lt;revisto&gt;") || !strings.Contains(w.Body.String(), `aria-invalid="true"`) {
		t.Fatalf("conflict rendered status=%d body=%s", w.Code, w.Body.String())
	}
}
