package handlers

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/passwordreset"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
)

const (
	recoveryClientLimit  = 5
	recoveryClientWindow = 10 * time.Minute
	recoveryMinimumWait  = 300 * time.Millisecond
	recoveryWaitJitter   = 151 * time.Millisecond
)

type PasswordRecoveryService interface {
	Issue(context.Context, string, bool) (uuid.UUID, error)
	Resolve(context.Context, string) (uuid.UUID, error)
	Consume(context.Context, string, string) (uuid.UUID, error)
}

type RecoveryClientLimiter interface {
	Allow(netip.Addr, time.Time) bool
}

type PasswordRecovery struct {
	Service      PasswordRecoveryService
	Sessions     *scs.SessionManager
	PageMeta     components.PageMeta
	System       System
	Limiter      RecoveryClientLimiter
	Now          func() time.Time
	ResponseWait func(context.Context, time.Time)
}

func (h PasswordRecovery) RequestGet(w http.ResponseWriter, r *http.Request) {
	h.renderRequest(w, r, http.StatusOK)
}

func (h PasswordRecovery) RequestPost(w http.ResponseWriter, r *http.Request) {
	started := h.now()
	if err := r.ParseForm(); err != nil {
		h.wait(r.Context(), started)
		h.renderRequest(w, r, http.StatusBadRequest)
		return
	}
	address, _ := httpx.RemoteIP(r.Context())
	allowed := h.Limiter == nil || h.Limiter.Allow(address, started)
	email, normalizeErr := validation.NormalizeEmail(r.PostForm.Get("identifier"))
	if allowed && normalizeErr == nil && h.Service != nil {
		// Account eligibility and per-account limits are deliberately not exposed
		// through the response. Unexpected delivery errors are also hidden here;
		// operational diagnosis is handled out of band without identifiers.
		_, _ = h.Service.Issue(r.Context(), email, true)
	}
	h.wait(r.Context(), started)
	h.renderConfirmation(w, r)
}

func (h PasswordRecovery) ResetGet(w http.ResponseWriter, r *http.Request) {
	h.recoveryHeaders(w, true)
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if !h.tokenIsActive(r.Context(), token, w, r) {
		return
	}
	h.renderReset(w, r, http.StatusOK, token, validation.FieldErrors{})
}

func (h PasswordRecovery) ResetPost(w http.ResponseWriter, r *http.Request) {
	h.recoveryHeaders(w, true)
	if err := r.ParseForm(); err != nil {
		h.renderInvalidLink(w, r)
		return
	}
	token := strings.TrimSpace(r.PostForm.Get("token"))
	if !h.tokenIsActive(r.Context(), token, w, r) {
		return
	}
	password := r.PostForm.Get("password")
	errorsByField := validation.FieldErrors{}
	if err := validation.ValidatePassword(password); err != nil {
		errorsByField.Add("password", err.Error())
	}
	if password != r.PostForm.Get("password_confirmation") {
		errorsByField.Add("password_confirmation", "As palavras-passe não coincidem.")
	}
	if !errorsByField.Empty() {
		h.renderReset(w, r, http.StatusUnprocessableEntity, token, errorsByField)
		return
	}
	_, err := h.Service.Consume(r.Context(), token, password)
	if errors.Is(err, passwordreset.ErrInvalidToken) {
		h.renderInvalidLink(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "login_flash", "A palavra-passe foi alterada. Já pode iniciar sessão.")
	}
	httpx.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h PasswordRecovery) tokenIsActive(ctx context.Context, token string, w http.ResponseWriter, r *http.Request) bool {
	if token == "" || h.Service == nil {
		h.renderInvalidLink(w, r)
		return false
	}
	_, err := h.Service.Resolve(ctx, token)
	if errors.Is(err, passwordreset.ErrInvalidToken) {
		h.renderInvalidLink(w, r)
		return false
	}
	if err != nil {
		h.System.InternalError(w, r)
		return false
	}
	return true
}

func (h PasswordRecovery) renderRequest(w http.ResponseWriter, r *http.Request, status int) {
	h.recoveryHeaders(w, false)
	meta := h.PageMeta
	meta.Title = "Recuperar palavra-passe | MyCFC"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.PasswordRecoveryRequest(pages.PasswordRecoveryRequestPage{Meta: meta, CSRFField: templ.Raw(string(csrf.TemplateField(r)))}).Render(r.Context(), w)
}

func (h PasswordRecovery) renderConfirmation(w http.ResponseWriter, r *http.Request) {
	h.recoveryHeaders(w, false)
	meta := h.PageMeta
	meta.Title = "Pedido recebido | MyCFC"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = pages.PasswordRecoveryConfirmation(pages.PasswordRecoveryConfirmationPage{Meta: meta}).Render(r.Context(), w)
}

func (h PasswordRecovery) renderReset(w http.ResponseWriter, r *http.Request, status int, token string, errorsByField validation.FieldErrors) {
	h.recoveryHeaders(w, true)
	meta := h.PageMeta
	meta.Title = "Definir nova palavra-passe | MyCFC"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.PasswordRecoveryReset(pages.PasswordRecoveryResetPage{Meta: meta, Token: token, Errors: errorsByField, CSRFField: templ.Raw(string(csrf.TemplateField(r)))}).Render(r.Context(), w)
}

func (h PasswordRecovery) renderInvalidLink(w http.ResponseWriter, r *http.Request) {
	h.recoveryHeaders(w, true)
	meta := h.PageMeta
	meta.Title = "Link de recuperação indisponível | MyCFC"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = pages.PasswordRecoveryInvalid(pages.PasswordRecoveryInvalidPage{Meta: meta}).Render(r.Context(), w)
}

func (h PasswordRecovery) recoveryHeaders(w http.ResponseWriter, receivesToken bool) {
	w.Header().Set("Cache-Control", "no-store")
	if receivesToken {
		w.Header().Set("Referrer-Policy", "no-referrer")
	}
}

func (h PasswordRecovery) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

func (h PasswordRecovery) wait(ctx context.Context, started time.Time) {
	if h.ResponseWait != nil {
		h.ResponseWait(ctx, started)
		return
	}
	jitter, err := rand.Int(rand.Reader, big.NewInt(int64(recoveryWaitJitter)))
	if err != nil {
		jitter = big.NewInt(int64(recoveryWaitJitter / 2))
	}
	remaining := started.Add(recoveryMinimumWait + time.Duration(jitter.Int64())).Sub(h.now())
	if remaining <= 0 {
		return
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

type recoveryLimitEntry struct {
	started time.Time
	count   int
}

type PasswordRecoveryLimiter struct {
	mu      sync.Mutex
	entries map[string]recoveryLimitEntry
	limit   int
	window  time.Duration
}

func NewPasswordRecoveryLimiter() *PasswordRecoveryLimiter {
	return &PasswordRecoveryLimiter{entries: map[string]recoveryLimitEntry{}, limit: recoveryClientLimit, window: recoveryClientWindow}
}

func (l *PasswordRecoveryLimiter) Allow(address netip.Addr, now time.Time) bool {
	if l == nil {
		return true
	}
	key := "unknown"
	if address.IsValid() {
		key = address.Unmap().String()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, found := l.entries[key]
	if !found || now.Sub(entry.started) >= l.window {
		if len(l.entries) >= 4096 {
			for candidate, existing := range l.entries {
				if now.Sub(existing.started) >= l.window {
					delete(l.entries, candidate)
				}
			}
			if len(l.entries) >= 4096 {
				return false
			}
		}
		l.entries[key] = recoveryLimitEntry{started: now, count: 1}
		return true
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	l.entries[key] = entry
	return true
}
