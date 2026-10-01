//go:build integration

package handlers

import (
	"context"
	"github.com/google/uuid"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func confirmDatedValues(t *testing.T, h Classification, actor, member uuid.UUID, v url.Values, preview *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	v.Set("preview_token", extractPreviewToken(t, preview.Body.String()))
	v.Set("confirm", "yes")
	r := httptest.NewRequest("POST", "/equipa/classificacao/"+member.String(), strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", member.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
	w := httptest.NewRecorder()
	h.Post(w, r)
	if w.Code != 200 {
		t.Fatalf("confirmation status=%d body=%s", w.Code, w.Body.String())
	}
	return w
}
