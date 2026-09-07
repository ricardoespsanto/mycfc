package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/polar"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/jackc/pgx/v5"
)

const polarOAuthLifetime = 10 * time.Minute

type Integrations struct {
	Service  PolarIntegrationService
	Client   *polar.Client
	Sessions *scs.SessionManager
	PageMeta components.PageMeta
	System   System
	Now      func() time.Time
	Location *time.Location
}

func (h Integrations) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}
func polarEligible(user CurrentUser) bool { return !user.IsDependent && len(user.Programmes) > 0 }
func (h Integrations) enabled() bool      { return h.Client != nil && h.Service != nil }
func (h Integrations) meta(r *http.Request) components.PageMeta {
	meta := h.PageMeta
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return meta
}

func (h Integrations) Index(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	page := pages.IntegrationsPage{Meta: h.meta(r), Enabled: h.enabled(), Eligible: polarEligible(user), Success: h.Sessions.PopString(r.Context(), "polar_flash"), Error: h.Sessions.PopString(r.Context(), "polar_error")}
	if h.enabled() && !user.IsDependent {
		connection, err := h.Service.Connection(r.Context(), user.ID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			h.System.InternalError(w, r)
			return
		case connection.Status == "ACTIVE":
			if connection.CredentialExpiresAt != nil && !connection.CredentialExpiresAt.After(h.now()) {
				page.ReauthorizationRequired = true
				break
			}
			page.Connected, page.Status = true, "Ligada"
			if connection.LastSuccessfulSyncAt != nil {
				location := h.Location
				if location == nil {
					location = time.UTC
				}
				page.LastSync = connection.LastSuccessfulSyncAt.In(location).Format("02/01/2006 15:04")
			}
			if page.Error == "" && connection.LastErrorCode != "" {
				page.Error = "A última sincronização Polar não foi concluída. Pode tentar novamente mais tarde."
			}
		case connection.Status == "REAUTHORIZATION_REQUIRED":
			page.ReauthorizationRequired, page.Status = true, "Nova autorização necessária"
		}
	}
	if err := pages.Integrations(page).Render(r.Context(), w); err != nil {
		h.System.InternalError(w, r)
	}
}

func (h Integrations) Connect(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	if !h.enabled() {
		h.System.NotFound(w, r)
		return
	}
	if !polarEligible(user) {
		h.System.Forbidden(w, r)
		return
	}
	if previous, err := strconv.ParseInt(h.Sessions.GetString(r.Context(), "polar_connect_started"), 10, 64); err == nil {
		age := h.now().Sub(time.Unix(previous, 0))
		if age >= 0 && age < 15*time.Second {
			h.Sessions.Put(r.Context(), "polar_error", "Aguarde um momento antes de iniciar outra ligação à Polar.")
			http.Redirect(w, r, "/perfil/integracoes", http.StatusSeeOther)
			return
		}
	}
	state, err := randomURLToken(32)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	memberID, err := randomURLToken(24)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	digest := sha256.Sum256([]byte(state))
	connectionVersion := int64(0)
	connection, connectionErr := h.Service.Connection(r.Context(), user.ID)
	if connectionErr == nil {
		connectionVersion = connection.Version
	} else if !errors.Is(connectionErr, pgx.ErrNoRows) {
		h.System.InternalError(w, r)
		return
	}
	h.Sessions.Put(r.Context(), "polar_oauth_state", hex.EncodeToString(digest[:]))
	h.Sessions.Put(r.Context(), "polar_oauth_user", user.ID.String())
	h.Sessions.Put(r.Context(), "polar_oauth_issued", strconv.FormatInt(h.now().Unix(), 10))
	h.Sessions.Put(r.Context(), "polar_oauth_member", "mycfc-"+memberID)
	h.Sessions.Put(r.Context(), "polar_oauth_connection_version", strconv.FormatInt(connectionVersion, 10))
	h.Sessions.Put(r.Context(), "polar_connect_started", strconv.FormatInt(h.now().Unix(), 10))
	target, err := h.Client.AuthorizationURL(state)
	if err != nil {
		h.clearOAuth(r)
		h.System.InternalError(w, r)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (h Integrations) Callback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	user, _ := CurrentUserFromContext(r.Context())
	storedDigest := h.Sessions.PopString(r.Context(), "polar_oauth_state")
	storedUser := h.Sessions.PopString(r.Context(), "polar_oauth_user")
	issuedRaw := h.Sessions.PopString(r.Context(), "polar_oauth_issued")
	memberID := h.Sessions.PopString(r.Context(), "polar_oauth_member")
	connectionVersionRaw := h.Sessions.PopString(r.Context(), "polar_oauth_connection_version")
	digest := sha256.Sum256([]byte(r.URL.Query().Get("state")))
	issued, parseErr := strconv.ParseInt(issuedRaw, 10, 64)
	connectionVersion, versionErr := strconv.ParseInt(connectionVersionRaw, 10, 64)
	valid := h.enabled() && polarEligible(user) && storedUser == user.ID.String() && memberID != "" && parseErr == nil && versionErr == nil && h.now().Sub(time.Unix(issued, 0)) >= 0 && h.now().Sub(time.Unix(issued, 0)) <= polarOAuthLifetime
	decodedStored, decodeErr := hex.DecodeString(storedDigest)
	valid = valid && decodeErr == nil && len(decodedStored) == len(digest) && subtle.ConstantTimeCompare(decodedStored, digest[:]) == 1
	if valid {
		current, connectionErr := h.Service.Connection(r.Context(), user.ID)
		valid = (errors.Is(connectionErr, pgx.ErrNoRows) && connectionVersion == 0) || (connectionErr == nil && current.Version == connectionVersion)
	}
	if !valid || r.URL.Query().Get("error") != "" {
		h.failRedirect(w, r, "Não foi possível validar a autorização Polar. Tente novamente.")
		return
	}
	credentials, err := h.Client.ExchangeCode(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		h.failRedirect(w, r, "A Polar não concluiu a autorização. Tente novamente.")
		return
	}
	if err := h.Service.Connect(r.Context(), user.ID, credentials, memberID, connectionVersion); err != nil {
		if errors.Is(err, ErrPolarIdentityConflict) {
			h.failRedirect(w, r, "Esta conta Polar não pode ser ligada a este perfil.")
			return
		}
		h.failRedirect(w, r, "Não foi possível concluir a ligação à Polar. Tente novamente.")
		return
	}
	h.Sessions.Put(r.Context(), "polar_flash", "Conta Polar ligada. Pode iniciar a primeira sincronização.")
	http.Redirect(w, r, "/perfil/integracoes", http.StatusSeeOther)
}

func (h Integrations) Sync(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	if !h.enabled() {
		h.System.NotFound(w, r)
		return
	}
	if !polarEligible(user) {
		h.System.Forbidden(w, r)
		return
	}
	err := h.Service.Sync(r.Context(), user.ID)
	switch {
	case err == nil:
		h.Sessions.Put(r.Context(), "polar_flash", "Sincronização Polar concluída.")
	case errors.Is(err, ErrPolarSyncInProgress):
		h.Sessions.Put(r.Context(), "polar_error", "Já existe uma sincronização Polar em curso.")
	case errors.Is(err, ErrPolarSyncTooSoon):
		h.Sessions.Put(r.Context(), "polar_error", "Aguarde um momento antes de voltar a sincronizar.")
	case polar.ErrorKindOf(err) == polar.ErrorRateLimited:
		h.Sessions.Put(r.Context(), "polar_error", "A Polar limitou temporariamente os pedidos. Tente mais tarde.")
	case polar.ErrorKindOf(err) == polar.ErrorReauthorization || polar.ErrorKindOf(err) == polar.ErrorConsent:
		h.Sessions.Put(r.Context(), "polar_error", "A autorização Polar terminou. Volte a ligar a conta.")
	default:
		h.Sessions.Put(r.Context(), "polar_error", "A sincronização Polar não foi concluída. A restante aplicação continua disponível.")
	}
	http.Redirect(w, r, "/perfil/integracoes", http.StatusSeeOther)
}

func (h Integrations) DisconnectPage(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	if !h.enabled() {
		h.System.NotFound(w, r)
		return
	}
	if user.IsDependent {
		h.System.Forbidden(w, r)
		return
	}
	if err := pages.PolarDisconnect(pages.PolarDisconnectPage{Meta: h.meta(r)}).Render(r.Context(), w); err != nil {
		h.System.InternalError(w, r)
	}
}

func (h Integrations) Disconnect(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	if !h.enabled() {
		h.System.NotFound(w, r)
		return
	}
	if user.IsDependent {
		h.System.Forbidden(w, r)
		return
	}
	if r.FormValue("confirm_disconnect") != "yes" {
		h.System.RequestRejected(w, r)
		return
	}
	h.clearOAuth(r)
	err := h.Service.Disconnect(r.Context(), user.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.Sessions.Put(r.Context(), "polar_error", "Polar desligada no MyCFC. A confirmação remota não foi concluída; revogue o acesso também na Polar Flow.")
	} else {
		h.Sessions.Put(r.Context(), "polar_flash", "Polar desligada. Os dados já importados foram mantidos.")
	}
	http.Redirect(w, r, "/perfil/integracoes", http.StatusSeeOther)
}

func (h Integrations) failRedirect(w http.ResponseWriter, r *http.Request, message string) {
	h.Sessions.Put(r.Context(), "polar_error", message)
	http.Redirect(w, r, "/perfil/integracoes", http.StatusSeeOther)
}
func (h Integrations) clearOAuth(r *http.Request) {
	for _, key := range []string{"polar_oauth_state", "polar_oauth_user", "polar_oauth_issued", "polar_oauth_member", "polar_oauth_connection_version"} {
		h.Sessions.Remove(r.Context(), key)
	}
}
func randomURLToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
