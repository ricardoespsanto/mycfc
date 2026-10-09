package handlers

import (
	"context"
	"errors"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type classificationReadFault struct {
	classificationFake
	stage                   string
	versionCalls, viewCalls int
}

func (f *classificationReadFault) Versions(ctx context.Context, a, m uuid.UUID) (string, string, error) {
	f.versionCalls++
	if f.stage == "versions" || f.stage == "after versions" && f.versionCalls == 2 {
		return "", "", errors.New("private-read-detail")
	}
	if f.stage == "stale sport" && f.versionCalls == 2 {
		return "new-version", "dated-version", nil
	}
	if f.stage == "stale dated" && f.versionCalls == 2 {
		return "sport-version", "new-version", nil
	}
	return f.classificationFake.Versions(ctx, a, m)
}
func (f *classificationReadFault) View(ctx context.Context, a, m uuid.UUID) (string, []ClassificationRow, error) {
	f.viewCalls++
	if f.stage == "view" || f.stage == "preview view" && f.viewCalls == 2 {
		return "", nil, errors.New("private-read-detail")
	}
	return f.classificationFake.View(ctx, a, m)
}
func (f *classificationReadFault) Options(ctx context.Context, a, m uuid.UUID) ([]ClassificationOption, error) {
	if f.stage == "options" {
		return nil, errors.New("private-read-detail")
	}
	return f.classificationFake.Options(ctx, a, m)
}
func (f *classificationReadFault) Taxonomy(ctx context.Context, m uuid.UUID) ([]ClassificationTaxon, []ClassificationTaxon, error) {
	if f.stage == "taxonomy" {
		return nil, nil, errors.New("private-read-detail")
	}
	return f.classificationFake.Taxonomy(ctx, m)
}
func (f *classificationReadFault) Selections(ctx context.Context, a, m uuid.UUID) ([]ClassificationTaxon, []ClassificationTaxon, error) {
	if f.stage == "selections" {
		return nil, nil, errors.New("private-read-detail")
	}
	return f.classificationFake.Selections(ctx, a, m)
}
func (f *classificationReadFault) CanCorrect(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	if f.stage == "can correct" {
		return false, errors.New("private-read-detail")
	}
	return true, nil
}
func (f *classificationReadFault) EligibilityOnWrite(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error) {
	if f.stage == "eligibility" {
		return false, errors.New("private-read-detail")
	}
	return true, nil
}

func classificationBoundaryRequest(method string, actor, member uuid.UUID, values url.Values) *http.Request {
	r := httptest.NewRequest(method, "/equipa/classificacao/"+member.String(), strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", member.String())
	return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
}

func TestClassificationReadFailuresNeverDiscloseOrWrite(t *testing.T) {
	for _, stage := range []string{"versions", "view", "options", "taxonomy", "selections", "after versions", "can correct", "stale sport"} {
		t.Run(stage, func(t *testing.T) {
			f := &classificationReadFault{classificationFake: classificationFake{allowed: true, name: "Ana", code: "Leisure"}, stage: stage}
			w := httptest.NewRecorder()
			(Classification{Store: f}).Get(w, classificationBoundaryRequest("GET", uuid.New(), uuid.New(), nil))
			want := 503
			if stage == "stale sport" {
				want = 409
			}
			if w.Code != want || strings.Contains(w.Body.String(), "private-read-detail") || f.writes != 0 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	for _, stage := range []string{"versions", "options", "eligibility", "preview view", "after versions", "stale dated"} {
		t.Run("post "+stage, func(t *testing.T) {
			category := uuid.New()
			f := &classificationReadFault{classificationFake: classificationFake{allowed: true, name: "Ana", code: "Competition", categories: []ClassificationCategory{{ID: category, Name: "Adultos", Eligible: true}}}, stage: stage}
			values := url.Values{"scope": {"11111111-1111-1111-1111-111111111111:22222222-2222-2222-2222-222222222222"}, "category_id": {category.String()}, "starts_on": {time.Now().In(lisbonLocation()).Format("2006-01-02")}}
			w := httptest.NewRecorder()
			(Classification{Store: f}).Post(w, classificationBoundaryRequest("POST", uuid.New(), uuid.New(), values))
			want := 503
			if stage == "preview view" || stage == "stale dated" {
				want = 409
			}
			if w.Code != want || f.writes != 0 || strings.Contains(w.Body.String(), "private-read-detail") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestClassificationBoundaryRejectsAnonymousMalformedAndInvalidCategory(t *testing.T) {
	actor, member := uuid.New(), uuid.New()
	for _, method := range []string{"GET", "POST", "SPORT"} {
		w := httptest.NewRecorder()
		h := Classification{Store: &classificationFake{allowed: true}}
		r := httptest.NewRequest("POST", "/equipa/classificacao/invalid", nil)
		if method == "GET" {
			h.Get(w, r)
		} else if method == "POST" {
			h.Post(w, r)
		} else {
			h.ReplaceSelections(w, r)
		}
		if w.Code != 404 {
			t.Fatal("anonymous classification accepted")
		}
	}
	for _, sport := range []bool{false, true} {
		w := httptest.NewRecorder()
		r := classificationBoundaryRequest("POST", actor, member, nil)
		r.Body = io.NopCloser(strings.NewReader("scope=%zz"))
		f := &classificationFake{allowed: true}
		h := Classification{Store: f}
		if sport {
			h.ReplaceSelections(w, r)
		} else {
			h.Post(w, r)
		}
		if w.Code != 400 || f.writes != 0 {
			t.Fatal("malformed form accepted")
		}
	}
	for _, code := range []string{"Competition", "Leisure", "Kayak_Polo"} {
		t.Run(code, func(t *testing.T) {
			f := &classificationFake{allowed: true, name: "Ana", code: code}
			values := url.Values{"scope": {"11111111-1111-1111-1111-111111111111:22222222-2222-2222-2222-222222222222"}, "starts_on": {time.Now().In(lisbonLocation()).Format("2006-01-02")}}
			if code != "Competition" {
				values.Set("category_id", uuid.NewString())
			}
			w := httptest.NewRecorder()
			(Classification{Store: f}).Post(w, classificationBoundaryRequest("POST", actor, member, values))
			if w.Code != 422 || f.writes != 0 {
				t.Fatal("invalid programme category accepted")
			}
		})
	}
}

func TestClassificationConfirmationHandlesRevokedWriter(t *testing.T) {
	actor, member := uuid.New(), uuid.New()
	f := &classificationFake{allowed: true, name: "Ana", code: "Leisure", writeErr: db.ErrDatedParticipationForbidden}
	h := Classification{Store: f}
	values := url.Values{"scope": {"11111111-1111-1111-1111-111111111111:22222222-2222-2222-2222-222222222222"}, "starts_on": {time.Now().In(lisbonLocation()).Format("2006-01-02")}}
	preview := httptest.NewRecorder()
	h.Post(preview, classificationBoundaryRequest("POST", actor, member, values))
	values.Set("preview_token", extractPreviewToken(t, preview.Body.String()))
	values.Set("confirm", "yes")
	w := httptest.NewRecorder()
	h.Post(w, classificationBoundaryRequest("POST", actor, member, values))
	if w.Code != 404 {
		t.Fatal("revoked writer not masked")
	}
}
