package handlers

import (
	"context"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestClassificationFieldErrorsIdentifyOnlyTheirTarget(t *testing.T) {
	for _, tc := range []struct {
		name, target, invalidField string
		invalid                    bool
	}{
		{"valid", "", "", false},
		{"date", "starts_on", "starts_on", true},
		{"scope", "scope", "scope", true},
		{"age reason", "age_exception_reason", "age_exception_reason", true},
		{"general dated error retains date fallback", "", "starts_on", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &classificationFake{allowed: true, name: "Ana"}
			member := uuid.New()
			r := httptest.NewRequest("GET", "/equipa/classificacao/"+member.String(), nil)
			r.SetPathValue("id", member.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New()}))
			w := httptest.NewRecorder()
			(Classification{Store: store}).render(w, r, 200, classificationForm{
				Invalid: tc.invalid, ErrorTarget: tc.target, Message: "Reveja o campo indicado.",
				StartsOn: "2026-11-10", AgeExceptionReason: "Motivo retido",
			})
			if w.Code != 200 {
				t.Fatalf("status %d", w.Code)
			}
			body := w.Body.String()
			for _, field := range []struct{ id, help string }{
				{"scope", "polo-team-help"}, {"starts_on", "date-help"},
				{"age_exception_reason", "age-reason-help"},
			} {
				opening := regexp.MustCompile(`<[^>]+\bid="` + field.id + `"[^>]*>`).FindString(body)
				if opening == "" {
					t.Fatalf("missing field %s", field.id)
				}
				wantInvalid := field.id == tc.invalidField
				if got := strings.Contains(opening, `aria-invalid="true"`); got != wantInvalid {
					t.Errorf("%s invalid=%t, want %t: %s", field.id, got, wantInvalid, opening)
				}
				wantDescription := field.help
				if wantInvalid {
					wantDescription += " error-summary"
				}
				if !strings.Contains(opening, `aria-describedby="`+wantDescription+`"`) {
					t.Errorf("%s description should be %q: %s", field.id, wantDescription, opening)
				}
			}
			if tc.invalid {
				if !strings.Contains(body, `id="error-summary" role="alert" tabindex="-1" autofocus`) ||
					!strings.Contains(body, `href="#`+tc.invalidField+`"`) {
					t.Error("error summary lost focus or target link")
				}
			}
			if !strings.Contains(body, `value="2026-11-10"`) || !strings.Contains(body, `>Motivo retido</textarea>`) || store.writes != 0 {
				t.Error("rendering must retain proposed values without writing")
			}
		})
	}
}
