package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/google/uuid"
)

func TestSystemStatesUseSharedPageContract(t *testing.T) {
	tests := []struct {
		name, title string
		status      int
		render      func(System, http.ResponseWriter, *http.Request)
	}{
		{"not found", "Página não encontrada", http.StatusNotFound, System.NotFound},
		{"forbidden", "Acesso recusado", http.StatusForbidden, System.Forbidden},
		{"request rejected", "Pedido recusado", http.StatusForbidden, System.RequestRejected},
		{"internal error", "Não foi possível concluir o pedido", http.StatusInternalServerError, System.InternalError},
		{"not implemented", "Funcionalidade em implementação", http.StatusNotImplemented, System.NotImplemented},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/estado", nil)
			request = request.WithContext(httpx.WithRequestID(request.Context(), "request-123"))
			response := httptest.NewRecorder()
			tc.render(System{PageMeta: components.PageMeta{StylesheetURL: "/assets/app.css", ScriptURL: "/assets/app.js"}}, response, request)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d", response.Code, tc.status)
			}
			body := response.Body.String()
			for _, expected := range []string{tc.title, `class="page-header"`, `class="module system-state"`, `href="/"`, `/assets/app.css`} {
				if !strings.Contains(body, expected) {
					t.Errorf("body does not contain %q", expected)
				}
			}
		})
	}
}

func TestSystemStateKeepsAuthenticatedContext(t *testing.T) {
	user := CurrentUser{ID: uuid.New(), Name: "Beatriz Administradora", IsAdmin: true, Programmes: map[string]bool{}, CoachProgrammeIDs: map[uuid.UUID]bool{}, CoachTeamIDs: map[uuid.UUID]bool{}}
	request := httptest.NewRequest(http.MethodGet, "/admin/fleet", nil)
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, user))
	response := httptest.NewRecorder()
	System{}.NotFound(response, request)
	body := response.Body.String()
	for _, expected := range []string{"Beatriz Administradora", `aria-label="Contexto e navegação MyCFCoimbra"`, `href="/today"`, "Voltar a Hoje"} {
		if !strings.Contains(body, expected) {
			t.Errorf("body does not contain %q", expected)
		}
	}
	if strings.Contains(body, `aria-label="Secções de administração"`) {
		t.Error("denied system state exposes administration sub-navigation")
	}
}
