package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/activity"
	"github.com/cfcoimbra/mycfc/internal/activity/polar"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const polarSessionState = "polar_oauth_state"

type PolarIntegrationStore interface {
	GetActivityConnectionForUser(context.Context, dbgen.GetActivityConnectionForUserParams) (dbgen.ActivityConnection, error)
	UpsertActivityConnection(context.Context, dbgen.UpsertActivityConnectionParams) (dbgen.ActivityConnection, error)
	UpsertSyncedActivity(context.Context, dbgen.UpsertSyncedActivityParams) (dbgen.SyncedActivity, error)
	RecordActivityConnectionSyncSuccess(context.Context, dbgen.RecordActivityConnectionSyncSuccessParams) (dbgen.ActivityConnection, error)
	RecordActivityConnectionError(context.Context, dbgen.RecordActivityConnectionErrorParams) (dbgen.ActivityConnection, error)
	DisconnectActivityConnection(context.Context, dbgen.DisconnectActivityConnectionParams) (dbgen.DisconnectActivityConnectionRow, error)
}

type PolarIntegration struct {
	Store    PolarIntegrationStore
	Vault    activity.CredentialVault
	Client   polar.Client
	Sessions *scs.SessionManager
	System   System
	PageMeta components.PageMeta
	Now      func() time.Time
}

func (h PolarIntegration) Index(w http.ResponseWriter, r *http.Request) { h.render(w, r, "") }
func (h PolarIntegration) Begin(w http.ResponseWriter, r *http.Request) {
	if !h.Client.Enabled() {
		h.render(w, r, "A ligação Polar ainda não está disponível.")
		return
	}
	state := make([]byte, 32)
	if _, err := rand.Read(state); err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.Sessions.Put(r.Context(), polarSessionState, base64.RawURLEncoding.EncodeToString(state))
	http.Redirect(w, r, h.Client.AuthorizeURL(base64.RawURLEncoding.EncodeToString(state)), http.StatusSeeOther)
}
func (h PolarIntegration) Callback(w http.ResponseWriter, r *http.Request) {
	state := h.Sessions.PopString(r.Context(), polarSessionState)
	if !h.Client.Enabled() || state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(r.URL.Query().Get("state"))) != 1 || r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		h.flash(r, "Não foi possível concluir a ligação Polar. Tente novamente.")
		http.Redirect(w, r, "/perfil/integracoes/polar", http.StatusSeeOther)
		return
	}
	actor, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	token, err := h.Client.ExchangeCode(ctx, r.URL.Query().Get("code"))
	if err != nil {
		h.flash(r, "A Polar não confirmou a ligação. Tente novamente.")
		http.Redirect(w, r, "/perfil/integracoes/polar", http.StatusSeeOther)
		return
	}
	sealed, err := h.Vault.Seal(ctx, polar.Provider, actor.ID.String(), polar.Credential(token))
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	keyID := sealed.KeyID
	connection, err := h.Store.UpsertActivityConnection(ctx, dbgen.UpsertActivityConnectionParams{UserID: actor.ID, Provider: string(polar.Provider), ProviderUserID: token.UserID, CredentialsCiphertext: sealed.Ciphertext, CredentialKeyID: &keyID, Scopes: []string{"exercise:read"}})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.sync(ctx, actor.ID, connection)
	h.flash(r, "Polar Flow foi ligada. As atividades recentes serão apresentadas quando estiverem disponíveis.")
	http.Redirect(w, r, "/perfil/integracoes/polar", http.StatusSeeOther)
}
func (h PolarIntegration) StartRecentSync(ctx context.Context, userID uuid.UUID) {
	if !h.Client.Enabled() || h.Store == nil || h.Vault == nil {
		return
	}
	connection, err := h.Store.GetActivityConnectionForUser(ctx, dbgen.GetActivityConnectionForUserParams{UserID: userID, Provider: string(polar.Provider)})
	if err != nil || connection.Status != "ACTIVE" {
		return
	}
	if connection.LastSuccessfulSyncAt.Valid && connection.LastSuccessfulSyncAt.Time.After(h.now().Add(-15*time.Minute)) {
		return
	}
	syncCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	h.sync(syncCtx, userID, connection)
}

func (h PolarIntegration) Sync(w http.ResponseWriter, r *http.Request) {
	actor, _ := CurrentUserFromContext(r.Context())
	connection, err := h.Store.GetActivityConnectionForUser(r.Context(), dbgen.GetActivityConnectionForUserParams{UserID: actor.ID, Provider: string(polar.Provider)})
	if errors.Is(err, pgx.ErrNoRows) {
		h.flash(r, "Ligue primeiro a Polar Flow.")
	} else if err != nil {
		h.System.InternalError(w, r)
		return
	} else {
		h.sync(r.Context(), actor.ID, connection)
		h.flash(r, "A sincronização Polar terminou. Consulte o estado abaixo.")
	}
	http.Redirect(w, r, "/perfil/integracoes/polar", http.StatusSeeOther)
}
func (h PolarIntegration) Disconnect(w http.ResponseWriter, r *http.Request) {
	actor, _ := CurrentUserFromContext(r.Context())
	connection, err := h.Store.GetActivityConnectionForUser(r.Context(), dbgen.GetActivityConnectionForUserParams{UserID: actor.ID, Provider: string(polar.Provider)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		h.System.InternalError(w, r)
		return
	}
	if err == nil {
		_, err = h.Store.DisconnectActivityConnection(r.Context(), dbgen.DisconnectActivityConnectionParams{ID: connection.ID, UserID: actor.ID, DisconnectedAt: pgtype.Timestamptz{Time: h.now(), Valid: true}})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
	}
	h.flash(r, "Polar Flow foi desligada e as credenciais foram removidas.")
	http.Redirect(w, r, "/perfil/integracoes/polar", http.StatusSeeOther)
}
func (h PolarIntegration) sync(ctx context.Context, userID uuid.UUID, connection dbgen.ActivityConnection) {
	if connection.CredentialKeyID == nil || len(connection.CredentialsCiphertext) == 0 {
		return
	}
	secret, err := h.Vault.Open(ctx, polar.Provider, userID.String(), activity.SealedCredentials{Ciphertext: connection.CredentialsCiphertext, KeyID: *connection.CredentialKeyID, Scopes: connection.Scopes})
	if err != nil {
		h.recordError(ctx, connection, "CREDENTIALS", true)
		return
	}
	now := h.now()
	page, err := h.Client.SyncRecent(ctx, secret, activity.SyncRequest{Since: now.AddDate(0, 0, -7), Until: now, Limit: 100})
	if err != nil {
		h.recordError(ctx, connection, polarErrorCode(err), errors.Is(err, polar.ErrUnauthorized))
		return
	}
	for _, item := range page.Activities {
		if err = item.Validate(); err != nil {
			h.recordError(ctx, connection, "NORMALIZATION", false)
			return
		}
		_, err = h.Store.UpsertSyncedActivity(ctx, activityParams(connection, userID, item))
		if err != nil {
			h.recordError(ctx, connection, "PERSISTENCE", false)
			return
		}
	}
	_, _ = h.Store.RecordActivityConnectionSyncSuccess(ctx, dbgen.RecordActivityConnectionSyncSuccessParams{ID: connection.ID, SucceededAt: pgtype.Timestamptz{Time: now, Valid: true}})
}
func activityParams(connection dbgen.ActivityConnection, userID uuid.UUID, item activity.NormalizedActivity) dbgen.UpsertSyncedActivityParams {
	return dbgen.UpsertSyncedActivityParams{ConnectionID: connection.ID, UserID: userID, Provider: string(polar.Provider), ProviderActivityID: item.ProviderActivityID, ProviderUpdatedAt: timeValue(item.ProviderUpdatedAt), StartsAt: pgtype.Timestamptz{Time: item.StartsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: item.EndsAt, Valid: true}, Sport: item.Sport, NormalizedSport: item.NormalizedSport, DurationSeconds: int32(item.DurationSeconds), DistanceMetres: item.DistanceMetres, AverageHeartRate: item.AverageHeartRate, MaximumHeartRate: item.MaximumHeartRate, ProviderMetrics: item.ProviderMetricsJSON, RawSummary: item.RawSummaryJSON, PayloadSha256: item.PayloadSHA256[:], NormalizationVersion: int32(item.NormalizationVersion)}
}
func timeValue(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}
func (h PolarIntegration) recordError(ctx context.Context, connection dbgen.ActivityConnection, code string, reauthorize bool) {
	message := "Não foi possível sincronizar com a Polar. Tente novamente mais tarde."
	_, _ = h.Store.RecordActivityConnectionError(ctx, dbgen.RecordActivityConnectionErrorParams{ID: connection.ID, RequiresReauthorization: reauthorize, ErrorCode: &code, ErrorMessage: &message, FailedAt: pgtype.Timestamptz{Time: h.now(), Valid: true}})
}
func polarErrorCode(err error) string {
	if errors.Is(err, polar.ErrUnauthorized) {
		return "UNAUTHORIZED"
	}
	if errors.Is(err, polar.ErrRateLimited) {
		return "RATE_LIMITED"
	}
	return "REMOTE_ERROR"
}
func (h PolarIntegration) render(w http.ResponseWriter, r *http.Request, additionalError string) {
	actor, _ := CurrentUserFromContext(r.Context())
	page := pages.PolarIntegrationPage{Meta: h.meta(r, actor), Available: h.Client.Enabled(), Error: additionalError}
	if h.Sessions != nil {
		page.Status = h.Sessions.PopString(r.Context(), "polar_flash")
	}
	if page.Available {
		connection, err := h.Store.GetActivityConnectionForUser(r.Context(), dbgen.GetActivityConnectionForUserParams{UserID: actor.ID, Provider: string(polar.Provider)})
		if err == nil && connection.Status != "DISCONNECTED" {
			page.Connected = true
			page.NeedsReauthorization = connection.Status == "REAUTHORIZATION_REQUIRED"
			if connection.LastErrorMessage != nil {
				page.Error = *connection.LastErrorMessage
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = pages.PolarIntegration(page).Render(r.Context(), w)
}
func (h PolarIntegration) meta(r *http.Request, actor CurrentUser) components.PageMeta {
	meta := h.PageMeta
	meta.Title = "Polar Flow | MyCFCoimbra"
	meta.CurrentPath = "/perfil/integracoes/polar"
	meta.CurrentUserName = actor.Name
	meta.CurrentUserID = actor.ID.String()
	meta.Navigation = dashboardNavigation(actor)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	meta.PageLabel = "Polar Flow"
	meta.AreaLabel = "Conta"
	return meta
}
func (h PolarIntegration) flash(r *http.Request, message string) {
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "polar_flash", message)
	}
}
func (h PolarIntegration) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}
