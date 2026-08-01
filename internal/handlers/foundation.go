package handlers

import (
	"net/http"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
)

type Foundation struct{ PageMeta components.PageMeta }

func (h Foundation) Get(w http.ResponseWriter, r *http.Request) {
	meta := h.PageMeta
	user, _ := CurrentUserFromContext(r.Context())
	meta.Title = "Componentes | MyCFC"
	meta.CurrentPath = "/admin/componentes"
	meta.CurrentUserName = user.Name
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	if err := pages.FoundationGallery(meta).Render(r.Context(), w); err != nil {
		http.Error(w, "Não foi possível apresentar os componentes.", http.StatusInternalServerError)
	}
}
