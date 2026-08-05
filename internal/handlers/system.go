package handlers

import (
	"net/http"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
)

type System struct{ PageMeta components.PageMeta }

func (h System) NotFound(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusNotFound, "Página indisponível", "Página não encontrada", "Verifique o endereço ou regresse ao início do MyCFC.", "")
}

func (h System) Forbidden(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusForbidden, "Acesso e permissões", "Acesso recusado", "A sua conta não tem a capacidade necessária para abrir esta página.", "")
}

func (h System) RequestRejected(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusForbidden, "Segurança do pedido", "Pedido recusado", "Atualize a página e tente novamente. Nenhuma alteração foi efetuada.", httpx.RequestID(r.Context()))
}

func (h System) InternalError(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusInternalServerError, "Estado do sistema", "Não foi possível concluir o pedido", "Tente novamente mais tarde. Os seus dados e permissões não foram alterados.", httpx.RequestID(r.Context()))
}

func (h System) NotImplemented(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusNotImplemented, "Estado do sistema", "Funcionalidade em implementação", "Esta parte da aplicação ainda não foi concluída.", httpx.RequestID(r.Context()))
}

func (h System) render(w http.ResponseWriter, r *http.Request, status int, eyebrow, title, message, requestID string) {
	meta := h.PageMeta
	meta.Title = title + " | MyCFC"
	// A failed or denied target must not activate route-derived navigation (for
	// example, the administration sub-navigation for a non-administrator).
	meta.CurrentPath = "/system"
	meta.AreaLabel = "MyCFC"
	meta.PageLabel = title
	if user, ok := CurrentUserFromContext(r.Context()); ok {
		meta.CurrentUserName = user.Name
		meta.CurrentUserID = user.ID.String()
		meta.Navigation = dashboardNavigation(user)
		meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	}
	action := components.PageAction{Label: "Ir para o início", Href: "/", Variant: "primary"}
	if len(meta.Navigation) > 0 {
		action.Label = "Voltar a Hoje"
		action.Href = "/today"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.SystemState(pages.SystemStatePage{Meta: meta, Eyebrow: eyebrow, Title: title, Message: message, RequestID: requestID, Action: action}).Render(r.Context(), w)
}
