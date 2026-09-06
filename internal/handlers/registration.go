package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
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
const registrationBotMessage = "Não foi possível confirmar que este pedido foi feito por uma pessoa. Tente novamente."
const registrationRenderTokenMaxAge = 30 * time.Minute
const registrationRenderTokenMinAge = 2 * time.Second
const turnstileVerifyEndpoint = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

type RegistrationInput struct {
	Name, Email, PasswordHash string
	DateOfBirth               time.Time
	TermsVersion, TermsSHA256 string
	IP                        *netip.Addr
	UserAgent                 string
}

type RegistrationResult struct {
	UserID            uuid.UUID
	CredentialVersion int64
}

type RegistrationStore interface {
	RegisterAdult(context.Context, RegistrationInput) (RegistrationResult, error)
}

type TurnstileVerifier interface {
	Verify(context.Context, string, *netip.Addr) error
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
	AntiBotKey   []byte

	TurnstileSiteKey  string
	TurnstileVerifier TurnstileVerifier
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
	h.validateBotChecks(r, &form)
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
		IP: ip, UserAgent: truncateRunes(r.UserAgent(), 512),
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
	h.Sessions.Put(r.Context(), "credential_version", result.CredentialVersion)
	h.Sessions.Put(r.Context(), "last_seen_at", h.now().UTC().Format(time.RFC3339Nano))
	httpx.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

type registrationForm struct {
	Name, Email, DateOfBirthInput string
	DateOfBirth                   time.Time
	TermsAccepted                 bool
	Errors                        validation.FieldErrors
}

func (h Registration) validate(r *http.Request) registrationForm {
	form := registrationForm{
		Name: strings.TrimSpace(r.PostForm.Get("name")), Email: strings.TrimSpace(r.PostForm.Get("email")),
		DateOfBirthInput: strings.TrimSpace(r.PostForm.Get("date_of_birth")),
		TermsAccepted:    r.PostForm.Get("accept_terms") == "on",
		Errors:           validation.FieldErrors{},
	}
	var err error
	if normalized, normalizeErr := validation.NormalizeName(form.Name); normalizeErr != nil {
		err = normalizeErr
		form.Errors.Add("name", err.Error())
	} else {
		form.Name = normalized
	}
	if normalized, normalizeErr := validation.NormalizeEmail(form.Email); normalizeErr != nil {
		err = normalizeErr
		form.Errors.Add("email", err.Error())
	} else {
		form.Email = normalized
	}
	if form.DateOfBirth, err = validation.ParseISODate(form.DateOfBirthInput); err != nil {
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
	if !form.TermsAccepted {
		form.Errors.Add("accept_terms", "Tem de aceitar os termos gerais.")
	}
	return form
}

func (h Registration) validateBotChecks(r *http.Request, form *registrationForm) {
	if strings.TrimSpace(r.PostForm.Get("company")) != "" {
		form.Errors.Add("registration_check", registrationBotMessage)
		return
	}
	if err := h.validateRenderToken(r.PostForm.Get("registration_token")); err != nil {
		form.Errors.Add("registration_check", registrationBotMessage)
		return
	}
	if h.TurnstileVerifier == nil {
		return
	}
	token := strings.TrimSpace(r.PostForm.Get("cf-turnstile-response"))
	var ip *netip.Addr
	if value, ok := httpx.RemoteIP(r.Context()); ok {
		ip = &value
	}
	if err := h.TurnstileVerifier.Verify(r.Context(), token, ip); err != nil {
		form.Errors.Add("registration_check", registrationBotMessage)
	}
}

func (h Registration) render(w http.ResponseWriter, r *http.Request, status int, form registrationForm) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	meta := h.PageMeta
	meta.Title = "Criar conta | MyCFCoimbra"
	dateOfBirth := form.DateOfBirthInput
	if dateOfBirth == "" && !form.DateOfBirth.IsZero() {
		dateOfBirth = form.DateOfBirth.Format("2006-01-02")
	}
	_ = pages.Registration(pages.RegistrationPage{
		Meta: meta, Name: form.Name, Email: form.Email, DateOfBirth: dateOfBirth,
		TermsURL: h.TermsURL, ImageURL: h.ImageURL, TermsAccepted: form.TermsAccepted,
		Errors: form.Errors, CSRFField: templ.Raw(string(csrf.TemplateField(r))), RegistrationToken: h.registrationRenderToken(h.now()),
		TurnstileSiteKey: h.TurnstileSiteKey,
	}).Render(r.Context(), w)
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

func (h Registration) registrationRenderToken(renderedAt time.Time) string {
	if len(h.AntiBotKey) == 0 {
		return ""
	}
	var payload [8]byte
	binary.BigEndian.PutUint64(payload[:], uint64(renderedAt.UTC().UnixNano()))
	signature := h.signRegistrationToken(payload[:])
	token := make([]byte, 0, len(payload)+len(signature))
	token = append(token, payload[:]...)
	token = append(token, signature...)
	return base64.RawURLEncoding.EncodeToString(token)
}

func (h Registration) validateRenderToken(raw string) error {
	if len(h.AntiBotKey) == 0 {
		return errors.New("registration anti-bot key is not configured")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil || len(decoded) != 8+sha256.Size {
		return errors.New("registration token is invalid")
	}
	payload, gotSignature := decoded[:8], decoded[8:]
	wantSignature := h.signRegistrationToken(payload)
	if !hmac.Equal(gotSignature, wantSignature) {
		return errors.New("registration token signature is invalid")
	}
	renderedAt := time.Unix(0, int64(binary.BigEndian.Uint64(payload))).UTC()
	age := h.now().UTC().Sub(renderedAt)
	if age < registrationRenderTokenMinAge {
		return errors.New("registration token is too new")
	}
	if age > registrationRenderTokenMaxAge {
		return errors.New("registration token expired")
	}
	return nil
}

func (h Registration) signRegistrationToken(payload []byte) []byte {
	mac := hmac.New(sha256.New, h.AntiBotKey)
	mac.Write([]byte("mycfc registration render token v1"))
	mac.Write(payload)
	return mac.Sum(nil)
}

type CloudflareTurnstileVerifier struct {
	Secret   string
	Endpoint string
	Client   *http.Client
}

func (v CloudflareTurnstileVerifier) Verify(ctx context.Context, token string, remoteIP *netip.Addr) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("turnstile response is empty")
	}
	endpoint := v.Endpoint
	if endpoint == "" {
		endpoint = turnstileVerifyEndpoint
	}
	client := v.Client
	if client == nil {
		client = http.DefaultClient
	}
	values := url.Values{}
	values.Set("secret", v.Secret)
	values.Set("response", token)
	if remoteIP != nil {
		values.Set("remoteip", remoteIP.String())
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return fmt.Errorf("build turnstile request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("verify turnstile: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read turnstile response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("turnstile status %d", response.StatusCode)
	}
	var result struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decode turnstile response: %w", err)
	}
	if !result.Success {
		return errors.New("turnstile verification failed")
	}
	return nil
}
