package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"time"
	"unicode/utf8"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

const duplicateEmailMessage = "Já existe uma conta com este endereço de correio eletrónico."

type RegistrationInput struct {
	Name, Email, PasswordHash string
	DateOfBirth               time.Time
	TermsVersion, TermsSHA256 string
	ImageVersion, ImageSHA256 string
	IP                        *netip.Addr
	UserAgent                 string
}

type RegistrationResult struct {
	UserID uuid.UUID
}

type RegistrationStore interface {
	RegisterAdult(context.Context, RegistrationInput) (RegistrationResult, error)
}

type Registration struct {
	Store        RegistrationStore
	Sessions     *scs.SessionManager
	PageMeta     components.PageMeta
	Location     *time.Location
	Now          func() time.Time
	TermsVersion string
	TermsSHA256  string
	TermsURL     string
	ImageVersion string
	ImageSHA256  string
	ImageURL     string
}

func (h Registration) Get(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, registrationForm{})
}

func (h Registration) Post(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render(w, r, http.StatusBadRequest, registrationForm{})
		return
	}
	form := h.validate(r)
	if !form.Errors.Empty() {
		h.render(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(r.PostForm.Get("password")), 12)
	if err != nil {
		h.render(w, r, http.StatusInternalServerError, registrationForm{})
		return
	}
	var ip *netip.Addr
	if value, ok := httpx.RemoteIP(r.Context()); ok {
		ip = &value
	}
	result, err := h.Store.RegisterAdult(r.Context(), RegistrationInput{
		Name: form.Name, Email: form.Email, PasswordHash: string(hash),
		DateOfBirth: form.DateOfBirth, TermsVersion: h.TermsVersion, TermsSHA256: h.TermsSHA256,
		ImageVersion: h.ImageVersion, ImageSHA256: h.ImageSHA256, IP: ip, UserAgent: truncateRunes(r.UserAgent(), 512),
	})
	if err != nil {
		if isUniqueViolation(err) {
			form.Errors.Add("email", duplicateEmailMessage)
			h.render(w, r, http.StatusUnprocessableEntity, form)
			return
		}
		h.render(w, r, http.StatusInternalServerError, registrationForm{})
		return
	}
	if err := h.Sessions.RenewToken(r.Context()); err != nil {
		h.render(w, r, http.StatusInternalServerError, registrationForm{})
		return
	}
	h.Sessions.Put(r.Context(), "user_id", result.UserID.String())
	h.Sessions.Put(r.Context(), "last_seen_at", h.now().UTC().Format(time.RFC3339Nano))
	httpx.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

type registrationForm struct {
	Name, Email string
	DateOfBirth time.Time
	Errors      validation.FieldErrors
}

func (h Registration) validate(r *http.Request) registrationForm {
	form := registrationForm{Errors: validation.FieldErrors{}}
	var err error
	if form.Name, err = validation.NormalizeName(r.PostForm.Get("name")); err != nil {
		form.Errors.Add("name", err.Error())
	}
	if form.Email, err = validation.NormalizeEmail(r.PostForm.Get("email")); err != nil {
		form.Errors.Add("email", err.Error())
	}
	if form.DateOfBirth, err = validation.ParseISODate(r.PostForm.Get("date_of_birth")); err != nil {
		form.Errors.Add("date_of_birth", err.Error())
	} else if err := validation.ValidateAdultDateOfBirth(form.DateOfBirth, h.now(), h.Location); err != nil {
		form.Errors.Add("date_of_birth", err.Error())
	}
	password := r.PostForm.Get("password")
	if err := validation.ValidatePassword(password); err != nil {
		form.Errors.Add("password", err.Error())
	}
	if password != r.PostForm.Get("password_confirmation") {
		form.Errors.Add("password_confirmation", "As palavras-passe não coincidem.")
	}
	if r.PostForm.Get("accept_terms") != "on" {
		form.Errors.Add("accept_terms", "Tem de aceitar os termos gerais.")
	}
	if r.PostForm.Get("accept_image_use") != "on" {
		form.Errors.Add("accept_image_use", "Tem de aceitar a autorização de uso de imagem.")
	}
	return form
}

func (h Registration) render(w http.ResponseWriter, r *http.Request, status int, form registrationForm) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	meta := h.PageMeta
	meta.Title = "Criar conta | MyCFC"
	dateOfBirth := ""
	if !form.DateOfBirth.IsZero() {
		dateOfBirth = form.DateOfBirth.Format("2006-01-02")
	}
	_ = pages.Registration(pages.RegistrationPage{Meta: meta, Name: form.Name, Email: form.Email, DateOfBirth: dateOfBirth, TermsURL: h.TermsURL, ImageURL: h.ImageURL, Errors: form.Errors, CSRFField: templ.Raw(string(csrf.TemplateField(r)))}).Render(r.Context(), w)
}

func (h Registration) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func isUniqueViolation(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && databaseError.Code == "23505"
}

func truncateRunes(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	return string([]rune(value)[:max])
}
