package handlers

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func agePost(f *classificationFake, actor, member uuid.UUID, v url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/equipa/classificacao/"+member.String(), strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", member.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
	w := httptest.NewRecorder()
	(Classification{Store: f}).Post(w, r)
	return w
}
func ageValues(cat uuid.UUID, reason string) url.Values {
	return url.Values{"scope": {"11111111-1111-1111-1111-111111111111:22222222-2222-2222-2222-222222222222"}, "category_id": {cat.String()}, "starts_on": {time.Now().In(lisbonLocation()).Format("2006-01-02")}, "age_exception_reason": {reason}}
}
func TestClassificationAgeExceptionAuthenticatedActorAndPrivateReason(t *testing.T) {
	f := &classificationFake{allowed: true, name: "Ana", code: "Competition"}
	actor, member, cat := uuid.New(), uuid.New(), uuid.New()
	f.categories = []ClassificationCategory{{ID: cat, Name: "Sub", Eligibility: "2010-01-01 — 2010-12-31", Eligible: false}}
	v := ageValues(cat, "Diferença etária verificada")
	preview := agePost(f, actor, member, v)
	v.Set("preview_token", extractPreviewToken(t, preview.Body.String()))
	v.Set("confirm", "yes")
	w := agePost(f, actor, member, v)
	if w.Code != 200 || f.writes != 1 || f.lastWrite.ActorID != actor || f.lastWrite.AgeExceptionReason != "Diferença etária verificada" || strings.Contains(w.Body.String(), "Diferença etária verificada") {
		t.Fatalf("status=%d writes=%d body=%s", w.Code, f.writes, w.Body.String())
	}
}
func TestClassificationAgeExceptionRejectsForgedActorAndInvalidReason(t *testing.T) {
	for _, tc := range []struct {
		reason string
		forged bool
	}{{" ", false}, {strings.Repeat("x", 501), false}, {"Justificação", true}} {
		f := &classificationFake{allowed: true, name: "Ana", code: "Competition"}
		cat := uuid.New()
		f.categories = []ClassificationCategory{{ID: cat, Eligible: false}}
		v := ageValues(cat, tc.reason)
		if tc.forged {
			v.Set("actor_id", uuid.NewString())
		}
		w := agePost(f, uuid.New(), uuid.New(), v)
		if w.Code != 422 || f.writes != 0 || !strings.Contains(w.Body.String(), `id="error-summary"`) || !strings.Contains(w.Body.String(), `href="#`+map[bool]string{true: "starts_on", false: "age_exception_reason"}[tc.forged]+`"`) {
			t.Fatalf("status=%d writes=%d body=%s", w.Code, f.writes, w.Body.String())
		}
	}
}
func TestClassificationAgeExceptionRejectsEligibleAndUnscoped(t *testing.T) {
	f := &classificationFake{allowed: true, name: "Ana", code: "Competition"}
	cat := uuid.New()
	f.categories = []ClassificationCategory{{ID: cat, Eligible: true}}
	v := ageValues(cat, "Motivo indevido")
	if w := agePost(f, uuid.New(), uuid.New(), v); w.Code != 422 || f.writes != 0 {
		t.Fatalf("eligible exception: %d", w.Code)
	}
	f.allowed = false
	if w := agePost(f, uuid.New(), uuid.New(), v); w.Code != 404 || strings.Contains(w.Body.String(), "Motivo indevido") {
		t.Fatalf("unscoped: %d %s", w.Code, w.Body.String())
	}
}
