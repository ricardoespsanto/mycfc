package handlers

import (
	"errors"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/emailverification"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
)

type EmailVerification struct {
	Service  emailverification.Service
	Sessions *scs.SessionManager
	PageMeta components.PageMeta
	System   System
}

func (h EmailVerification) Confirm(w http.ResponseWriter, r *http.Request) {
	verifiedID, err := h.Service.Verify(r.Context(), r.URL.Query().Get("id"), r.URL.Query().Get("signature"))
	if err != nil {
		if errors.Is(err, emailverification.ErrInvalidToken) {
			h.render(w, r, http.StatusUnprocessableEntity, false)
			return
		}
		h.System.InternalError(w, r)
		return
	}
	if user, ok := currentUser(r.Context()); ok && user.ID == verifiedID {
		if h.Sessions != nil {
			h.Sessions.Put(r.Context(), "profile_flash", "Email confirmado.")
		}
		httpx.Redirect(w, r, "/perfil", http.StatusSeeOther)
		return
	}
	h.render(w, r, http.StatusOK, true)
}

func (h EmailVerification) Resend(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r.Context())
	if !ok {
		httpx.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	message := "O seu email já está confirmado."
	if !user.IsDependent && !user.EmailVerified {
		_, err := h.Service.Issue(r.Context(), user.ID, user.Email, true)
		switch {
		case err == nil:
			message = "Enviámos um novo link de confirmação."
		case errors.Is(err, emailverification.ErrTooSoon):
			message = "Já enviámos um link recentemente. Aguarde um minuto antes de tentar novamente."
		default:
			h.System.InternalError(w, r)
			return
		}
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "profile_flash", message)
	}
	httpx.Redirect(w, r, "/perfil", http.StatusSeeOther)
}

func (h EmailVerification) render(w http.ResponseWriter, r *http.Request, status int, success bool) {
	meta := h.PageMeta
	meta.Title = "Confirmar email | MyCFCoimbra"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.EmailVerification(pages.EmailVerificationPage{Meta: meta, Success: success}).Render(r.Context(), w)
}
