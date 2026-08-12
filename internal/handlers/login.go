package handlers

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const invalidLoginMessage = "O endereço de correio eletrónico ou a palavra-passe não estão corretos."

// dummyPasswordHash makes unknown-account comparisons follow the bcrypt path.
const dummyPasswordHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

type LoginUserLookup interface {
	GetActiveUserByEmail(ctx context.Context, email *string) (dbgen.GetActiveUserByEmailRow, error)
	GetActiveDependentByLoginID(ctx context.Context, minorLoginID *string) (dbgen.GetActiveDependentByLoginIDRow, error)
}

type Login struct {
	Users       LoginUserLookup
	Sessions    *scs.SessionManager
	PageMeta    components.PageMeta
	FailureWait func(context.Context)
}

func (h Login) Get(w http.ResponseWriter, r *http.Request) {
	success := ""
	if h.Sessions != nil {
		success = h.Sessions.PopString(r.Context(), "login_flash")
	}
	h.render(w, r, http.StatusOK, "", r.URL.Query().Get("next"), success)
}

func (h Login) Post(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render(w, r, http.StatusBadRequest, "", "", "")
		return
	}

	identifier := strings.TrimSpace(r.PostForm.Get("identifier"))
	password := r.PostForm.Get("password")
	next := validation.SafeNext(r.PostForm.Get("next"))
	if identifier == "" || password == "" {
		h.wait(r.Context())
		h.render(w, r, http.StatusUnprocessableEntity, identifier, next, "")
		return
	}

	var userID uuid.UUID
	credentialVersion := int64(0)
	var passwordHash *string
	var err error
	if strings.HasPrefix(strings.ToUpper(identifier), "CFC-") {
		loginID := strings.ToUpper(identifier)
		user, lookupErr := h.Users.GetActiveDependentByLoginID(r.Context(), &loginID)
		err, userID, passwordHash, credentialVersion = lookupErr, user.ID, user.PasswordHash, user.CredentialVersion
	} else {
		email, emailErr := validation.NormalizeEmail(identifier)
		if emailErr != nil {
			h.wait(r.Context())
			h.render(w, r, http.StatusUnprocessableEntity, identifier, next, "")
			return
		}
		user, lookupErr := h.Users.GetActiveUserByEmail(r.Context(), &email)
		err, userID, passwordHash, credentialVersion = lookupErr, user.ID, user.PasswordHash, user.CredentialVersion
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		h.render(w, r, http.StatusInternalServerError, "", "", "")
		return
	}

	hash := dummyPasswordHash
	if err == nil && passwordHash != nil {
		hash = *passwordHash
	}
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		h.wait(r.Context())
		h.render(w, r, http.StatusUnprocessableEntity, identifier, next, "")
		return
	}

	if err := h.Sessions.RenewToken(r.Context()); err != nil {
		h.render(w, r, http.StatusInternalServerError, "", "", "")
		return
	}
	h.Sessions.Put(r.Context(), "user_id", userID.String())
	h.Sessions.Put(r.Context(), "credential_version", credentialVersion)
	h.Sessions.Put(r.Context(), "last_seen_at", time.Now().UTC().Format(time.RFC3339Nano))
	if next == "" {
		next = "/dashboard"
	}
	httpx.Redirect(w, r, next, http.StatusSeeOther)
}

func (h Login) render(w http.ResponseWriter, r *http.Request, status int, email, next, success string) {
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
		Success:   success,
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
