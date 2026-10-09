package handlers

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
)

func TestClassificationPoloAutomaticTeamAndForgedTeam(t *testing.T) {
	f := &classificationFake{allowed: true, name: "Ana", code: "Kayak_Polo"}
	member, actor := uuid.New(), uuid.New()
	request := func(method string, v url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/equipa/classificacao/"+member.String(), strings.NewReader(v.Encode()))
		r.SetPathValue("id", member.String())
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
		w := httptest.NewRecorder()
		if method == "GET" {
			(Classification{Store: f}).Get(w, r)
		} else {
			(Classification{Store: f}).Post(w, r)
		}
		return w
	}
	get := request("GET", url.Values{})
	if get.Code != 200 || !strings.Contains(get.Body.String(), "equipa partilhada") {
		t.Fatalf("missing automatic-team explanation: %d", get.Code)
	}
	v := url.Values{"scope": {"11111111-1111-1111-1111-111111111111:22222222-2222-2222-2222-222222222222"}, "starts_on": {time.Now().In(lisbonLocation()).Format("2006-01-02")}}
	v.Set("team_id", uuid.NewString())
	bad := request("POST", v)
	if bad.Code != 422 || f.writes != 0 {
		t.Fatalf("forged team: status=%d writes=%d", bad.Code, f.writes)
	}
	v.Del("team_id")
	preview := request("POST", v)
	v.Set("preview_token", extractPreviewToken(t, preview.Body.String()))
	v.Set("confirm", "yes")
	good := request("POST", v)
	if good.Code != 200 || f.writes != 1 {
		t.Fatalf("automatic team: status=%d writes=%d body=%s", good.Code, f.writes, good.Body.String())
	}
	f.writeErr = db.ErrDatedPoloTeamUnavailable
	missing := request("POST", v)
	if missing.Code != 409 || !strings.Contains(missing.Body.String(), "Ainda não existe uma equipa partilhada") || !strings.Contains(missing.Body.String(), `href="#scope"`) || !strings.Contains(missing.Body.String(), `aria-invalid="true"`) {
		t.Fatalf("missing team: status=%d body=%s", missing.Code, missing.Body.String())
	}
}
