package handlers

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/gorilla/csrf"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const invalidLoginMessage = "O endereço de correio eletrónico ou a palavra-passe não estão corretos."

// dummyPasswordHash makes unknown-account comparisons follow the bcrypt path.
const dummyPasswordHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

type LoginUserLookup interface {
	GetActiveUserByEmail(ctx context.Context, email *string) (dbgen.User, error)
}

type Login struct {
	Users       LoginUserLookup
	Sessions    *scs.SessionManager
	PageMeta    components.PageMeta
	FailureWait func(context.Context)
}

func (h Login) Get(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, "", r.URL.Query().Get("next"))
}

func (h Login) Post(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render(w, r, http.StatusBadRequest, "", "")
		return
	}

	email, emailErr := validation.NormalizeEmail(r.PostForm.Get("email"))
	password := r.PostForm.Get("password")
	next := validation.SafeNext(r.PostForm.Get("next"))
	if emailErr != nil || password == "" {
		h.wait(r.Context())
		h.render(w, r, http.StatusUnprocessableEntity, r.PostForm.Get("email"), next)
		return
	}

	user, err := h.Users.GetActiveUserByEmail(r.Context(), &email)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		h.render(w, r, http.StatusInternalServerError, "", "")
		return
	}

	hash := dummyPasswordHash
	if err == nil && user.PasswordHash != nil {
		hash = *user.PasswordHash
	}
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		h.wait(r.Context())
		h.render(w, r, http.StatusUnprocessableEntity, email, next)
		return
	}

	if err := h.Sessions.RenewToken(r.Context()); err != nil {
		h.render(w, r, http.StatusInternalServerError, "", "")
		return
	}
	h.Sessions.Put(r.Context(), "user_id", user.ID.String())
	h.Sessions.Put(r.Context(), "role", user.Role)
	h.Sessions.Put(r.Context(), "last_seen_at", time.Now().UTC().Format(time.RFC3339Nano))
	if next == "" {
		next = "/dashboard"
	}
	httpx.Redirect(w, r, next, http.StatusSeeOther)
}

func (h Login) render(w http.ResponseWriter, r *http.Request, status int, email, next string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	meta := h.PageMeta
	meta.Title = "Iniciar sessão | MyCFC"
	_ = pages.Login(pages.LoginPage{
		Meta:      meta,
		Email:     email,
		Next:      validation.SafeNext(next),
		CSRFField: templ.Raw(string(csrf.TemplateField(r))),
		Error:     loginError(status),
	}).Render(r.Context(), w)
}

func (h Login) wait(ctx context.Context) {
	if h.FailureWait != nil {
		h.FailureWait(ctx)
		return
	}
	delay, err := rand.Int(rand.Reader, big.NewInt(151))
	if err != nil {
		delay = big.NewInt(75)
	}
	timer := time.NewTimer(time.Duration(100+delay.Int64()) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func loginError(status int) string {
	if status == http.StatusUnprocessableEntity {
		return invalidLoginMessage
	}
	return ""
}
