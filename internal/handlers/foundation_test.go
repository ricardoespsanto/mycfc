package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFoundationRendersComponentGalleryForCurrentAdministrator(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/admin/componentes", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Name: "Ana", IsAdmin: true, EmailVerified: true}))
	w := httptest.NewRecorder()

	(Foundation{}).Get(w, r)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Componentes") {
		t.Fatalf("response = %d, body = %s", w.Code, w.Body.String())
	}
}
