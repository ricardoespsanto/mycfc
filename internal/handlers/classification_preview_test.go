package handlers

import (
	"context"
	"github.com/google/uuid"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func extractPreviewToken(t *testing.T, body string) string {
	t.Helper()
	prefix := `name="preview_token" value="`
	_, rest, ok := strings.Cut(body, prefix)
	if !ok {
		t.Fatalf("missing preview token: %s", body)
	}
	token, _, _ := strings.Cut(rest, `"`)
	return token
}

func TestClassificationRequiresExplicitDatedPreview(t *testing.T) {
	f := &classificationFake{allowed: true, name: "Ana", code: "Leisure"}
	member, actor := uuid.New(), uuid.New()
	v := url.Values{"scope": {"11111111-1111-1111-1111-111111111111:22222222-2222-2222-2222-222222222222"}, "starts_on": {time.Now().In(lisbonLocation()).Format("2006-01-02")}}
	r := httptest.NewRequest("POST", "/equipa/classificacao/"+member.String(), strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", member.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
	w := httptest.NewRecorder()
	Classification{Store: f}.Post(w, r)
	if w.Code != 200 || f.writes != 0 || !strings.Contains(w.Body.String(), "Confirmar participação") || !strings.Contains(w.Body.String(), "Intervalo novo") {
		t.Fatalf("status=%d writes=%d body=%s", w.Code, f.writes, w.Body.String())
	}
}

func TestClassificationConfirmationRejectsForgedExpiredAndStale(t *testing.T) {
	for _, kind := range []string{"changed date", "changed actor", "changed member", "forged", "expired", "stale"} {
		t.Run(kind, func(t *testing.T) {
			f := &classificationFake{allowed: true, name: "Ana", code: "Leisure"}
			actor, member := uuid.New(), uuid.New()
			date := time.Now().In(lisbonLocation()).Format("2006-01-02")
			v := url.Values{"scope": {"11111111-1111-1111-1111-111111111111:22222222-2222-2222-2222-222222222222"}, "starts_on": {date}}
			post := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/equipa/classificacao/"+member.String(), strings.NewReader(v.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.SetPathValue("id", member.String())
				r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
				w := httptest.NewRecorder()
				Classification{Store: f}.Post(w, r)
				return w
			}
			preview := post()
			token := extractPreviewToken(t, preview.Body.String())
			switch kind {
			case "changed date":
				v.Set("starts_on", time.Now().In(lisbonLocation()).AddDate(0, 0, 1).Format("2006-01-02"))
			case "changed actor":
				actor = uuid.New()
			case "changed member":
				member = uuid.New()
			case "forged":
				token += "x"
			case "expired":
				start, _ := time.Parse("2006-01-02", date)
				in := ClassificationWrite{ActorID: actor, MemberID: member, SeasonID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), ProgrammeID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), StartsOn: start}
				token = signClassificationConfirmation(confirmationBinding(actor, member, "DATED", in), "dated-version", time.Now().Add(-16*time.Minute))
			case "stale":
				f.datedVersion = "new-version"
			}
			v.Set("preview_token", token)
			v.Set("confirm", "yes")
			response := post()
			if response.Code != 409 || f.writes != 0 || !strings.Contains(response.Body.String(), `id="error-summary"`) || !strings.Contains(response.Body.String(), v.Get("starts_on")) {
				t.Fatalf("status=%d writes=%d", response.Code, f.writes)
			}
		})
	}
}

func TestClassificationSportRejectsMissingOriginalToken(t *testing.T) {
	f := &classificationFake{allowed: true, name: "Ana"}
	member, actor := uuid.New(), uuid.New()
	v := url.Values{"kind": {"SPORT"}, "codes": {"SUP"}, "reason": {"Correção"}}
	r := httptest.NewRequest("POST", "/equipa/classificacao/"+member.String()+"/selecoes", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", member.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
	w := httptest.NewRecorder()
	Classification{Store: f}.ReplaceSelections(w, r)
	if w.Code != 409 || f.writes != 0 {
		t.Fatalf("status=%d writes=%d", w.Code, f.writes)
	}
}
