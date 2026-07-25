package handlers

import (
	"net/http"

	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
)

type Landing struct {
	PageMeta components.PageMeta
	HeroURL  string
}

func (h Landing) Get(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pages.Landing(pages.LandingPage{Meta: h.PageMeta, HeroURL: h.HeroURL}).Render(r.Context(), w)
}
