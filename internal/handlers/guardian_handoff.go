package handlers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/guardianauthority"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (h Dashboard) GuardianAgeHandoff(w http.ResponseWriter, r *http.Request) {
	h.renderGuardianAgeHandoff(w, r, http.StatusOK, validation.FieldErrors{}, "")
}

func (h Dashboard) ProposeGuardianAgeHandoffEmail(w http.ResponseWriter, r *http.Request) {
	if h.GuardianHandoffs == nil || r.ParseForm() != nil {
		h.System.InternalError(w, r)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	email, emailErr := validation.NormalizeEmail(r.PostForm.Get("email"))
	version, versionErr := strconv.ParseInt(r.PostForm.Get("version"), 10, 64)
	if emailErr != nil || versionErr != nil || version < 1 {
		errors := validation.FieldErrors{}
		if emailErr != nil {
			errors["email"] = emailErr.Error()
		} else {
			errors["email"] = "Atualize a página e volte a tentar."
		}
		h.renderGuardianAgeHandoff(w, r, http.StatusUnprocessableEntity, errors, "")
		return
	}
	_, digest, expiresAt, payload, err := h.newGuardianAgeHandoffEmail(email)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	_, err = h.GuardianHandoffs.ProposeEmail(r.Context(), user.ID, version, email, digest, expiresAt, payload)
	if errors.Is(err, ErrGuardianAgeHandoffConflict) {
		h.renderGuardianAgeHandoff(w, r, http.StatusConflict, validation.FieldErrors{"email": "A transição foi alterada. Reveja o estado atual antes de continuar."}, "")
		return
	}
	if errors.Is(err, ErrGuardianAgeHandoffCollision) {
		h.renderGuardianAgeHandoff(w, r, http.StatusUnprocessableEntity, validation.FieldErrors{"email": "Não foi possível utilizar este email. Contacte o clube para resolução presencial; não crie outra conta."}, "")
		return
	}
	if errors.Is(err, ErrGuardianAgeHandoffInvalid) || errors.Is(err, ErrGuardianAgeHandoffForbidden) {
		h.renderGuardianAgeHandoff(w, r, http.StatusUnprocessableEntity, validation.FieldErrors{"email": "Não foi possível iniciar esta confirmação."}, "")
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "guardian_handoff_flash", "Enviámos um link de confirmação válido durante 24 horas.")
	}
	if h.Logger != nil {
		h.Logger.Info("guardian age handoff email proposed", "outcome", "accepted")
	}
	httpx.Redirect(w, r, "/transicao-18", http.StatusSeeOther)
}

func (h Dashboard) VerifyGuardianAgeHandoffEmail(w http.ResponseWriter, r *http.Request) {
	if h.GuardianHandoffs == nil || len(h.GuardianHandoffKey) < 32 {
		h.System.InternalError(w, r)
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(r.URL.Query().Get("token"))
	if err != nil || len(raw) != 32 {
		h.renderGuardianAgeHandoffVerification(w, r, http.StatusUnprocessableEntity, false)
		return
	}
	err = h.GuardianHandoffs.VerifyEmail(r.Context(), guardianAgeHandoffDigest(h.GuardianHandoffKey, raw))
	if errors.Is(err, ErrGuardianAgeHandoffInvalid) || errors.Is(err, pgx.ErrNoRows) {
		h.renderGuardianAgeHandoffVerification(w, r, http.StatusUnprocessableEntity, false)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if h.Logger != nil {
		h.Logger.Info("guardian age handoff email verified", "outcome", "accepted")
	}
	h.renderGuardianAgeHandoffVerification(w, r, http.StatusOK, true)
}

func (h Dashboard) GuardianAgeHandoffAdminQueue(w http.ResponseWriter, r *http.Request) {
	if h.GuardianHandoffs == nil {
		h.System.InternalError(w, r)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	items, err := h.GuardianHandoffs.ListForAdmin(r.Context(), user.ID, 100, 0)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	pageItems := make([]pages.GuardianAgeHandoffQueueItem, len(items))
	for i, item := range items {
		pageItems[i] = pages.GuardianAgeHandoffQueueItem{Reference: item.Reference.String(), SubjectName: item.SubjectName,
			Status: guardianAgeHandoffStatusLabel(item.Status), Birthday: item.Birthday.Format("02/01/2006"), URL: "/admin/transicoes-18/" + item.Reference.String()}
	}
	h.renderGuardianAgeHandoffAdminQueue(w, r, pages.GuardianAgeHandoffQueuePage{Meta: h.guardianAgeHandoffMeta(r, "Transições aos 18 anos"), Items: pageItems})
}

func (h Dashboard) GuardianAgeHandoffAdminDetail(w http.ResponseWriter, r *http.Request) {
	h.renderGuardianAgeHandoffAdminDetail(w, r, http.StatusOK, validation.FieldErrors{}, "")
}

func (h Dashboard) ConfirmGuardianAgeHandoffIdentity(w http.ResponseWriter, r *http.Request) {
	if h.GuardianHandoffs == nil || r.ParseForm() != nil {
		h.System.InternalError(w, r)
		return
	}
	ref, refErr := uuid.Parse(r.PathValue("ref"))
	version, versionErr := strconv.ParseInt(r.PostForm.Get("version"), 10, 64)
	if refErr != nil {
		h.System.NotFound(w, r)
		return
	}
	if versionErr != nil || version < 1 || r.PostForm.Get("confirmation") != "yes" {
		h.renderGuardianAgeHandoffAdminDetail(w, r, http.StatusUnprocessableEntity, validation.FieldErrors{"confirmation": "Confirme explicitamente a verificação presencial."}, "")
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	if !h.guardianAgeHandoffFreshAuth(w, r, user.ID) {
		return
	}
	_, err := h.GuardianHandoffs.ConfirmIdentity(r.Context(), user.ID, ref, version)
	if h.handleGuardianAgeHandoffMutationError(w, r, err, "confirmation") {
		return
	}
	if h.Logger != nil {
		h.Logger.Info("guardian age handoff identity confirmed", "outcome", "accepted")
	}
	httpx.Redirect(w, r, "/admin/transicoes-18/"+ref.String()+"?success=identity", http.StatusSeeOther)
}

func (h Dashboard) RecoverGuardianAgeHandoffEmail(w http.ResponseWriter, r *http.Request) {
	if h.GuardianHandoffs == nil || r.ParseForm() != nil {
		h.System.InternalError(w, r)
		return
	}
	ref, refErr := uuid.Parse(r.PathValue("ref"))
	version, versionErr := strconv.ParseInt(r.PostForm.Get("version"), 10, 64)
	email, emailErr := validation.NormalizeEmail(r.PostForm.Get("email"))
	if refErr != nil {
		h.System.NotFound(w, r)
		return
	}
	if versionErr != nil || version < 1 || emailErr != nil {
		message := "Atualize a página e volte a tentar."
		if emailErr != nil {
			message = emailErr.Error()
		}
		h.renderGuardianAgeHandoffAdminDetail(w, r, http.StatusUnprocessableEntity, validation.FieldErrors{"email": message}, "")
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	if !h.guardianAgeHandoffFreshAuth(w, r, user.ID) {
		return
	}
	_, digest, expiresAt, payload, err := h.newGuardianAgeHandoffEmail(email)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	_, err = h.GuardianHandoffs.RecoverEmail(r.Context(), user.ID, ref, version, email, digest, expiresAt, payload)
	if h.handleGuardianAgeHandoffMutationError(w, r, err, "email") {
		return
	}
	if h.Logger != nil {
		h.Logger.Info("guardian age handoff recovery email proposed", "outcome", "accepted")
	}
	httpx.Redirect(w, r, "/admin/transicoes-18/"+ref.String()+"?success=recovery", http.StatusSeeOther)
}

func (h Dashboard) guardianAgeHandoffFreshAuth(w http.ResponseWriter, r *http.Request, actorID uuid.UUID) bool {
	if err := h.confirmGuardianAuthorityMutation(r, actorID); err != nil {
		if errors.Is(err, ErrGuardianAuthentication) {
			if limitErr := h.reserveGuardianAuthenticationFailure(r, actorID); errors.Is(limitErr, ErrGuardianApplicationLimited) {
				w.Header().Set("Retry-After", "900")
				h.renderGuardianAgeHandoffAdminDetail(w, r, http.StatusTooManyRequests, validation.FieldErrors{"password": "Aguarde 15 minutos antes de voltar a confirmar a palavra-passe."}, "")
				return false
			} else if limitErr != nil {
				h.System.InternalError(w, r)
				return false
			}
			h.renderGuardianAgeHandoffAdminDetail(w, r, http.StatusUnprocessableEntity, validation.FieldErrors{"password": "Não foi possível confirmar a palavra-passe atual."}, "")
			return false
		}
		h.System.InternalError(w, r)
		return false
	}
	return true
}

func (h Dashboard) handleGuardianAgeHandoffMutationError(w http.ResponseWriter, r *http.Request, err error, actionField string) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, ErrGuardianAgeHandoffConflict):
		h.renderGuardianAgeHandoffAdminDetail(w, r, http.StatusConflict, validation.FieldErrors{actionField: "A transição foi alterada. Reveja o estado atual."}, "")
	case errors.Is(err, ErrGuardianAgeHandoffForbidden):
		h.System.Forbidden(w, r)
	case errors.Is(err, ErrGuardianAgeHandoffCollision):
		h.renderGuardianAgeHandoffAdminDetail(w, r, http.StatusUnprocessableEntity, validation.FieldErrors{"email": "Este email já pertence a outra conta. A resolução tem de ser presencial, sem criar, juntar ou transferir contas."}, "")
	case errors.Is(err, ErrGuardianAgeHandoffInvalid):
		h.renderGuardianAgeHandoffAdminDetail(w, r, http.StatusUnprocessableEntity, validation.FieldErrors{actionField: "Esta transição já não permite a ação pedida."}, "")
	default:
		h.System.InternalError(w, r)
	}
	return true
}

func (h Dashboard) newGuardianAgeHandoffEmail(recipient string) (string, []byte, time.Time, []byte, error) {
	if len(h.GuardianHandoffKey) < 32 || strings.TrimSpace(h.GuardianHandoffBaseURL) == "" {
		return "", nil, time.Time{}, nil, errors.New("guardian age handoff key unavailable")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, time.Time{}, nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expiresAt := h.now().UTC().Add(24 * time.Hour)
	actionURL := strings.TrimRight(h.GuardianHandoffBaseURL, "/") + "/transicao-18/verificar?token=" + url.QueryEscape(token)
	payload, err := guardianauthority.SealHandoffDelivery(h.GuardianHandoffKey, guardianauthority.HandoffDelivery{Recipient: recipient, ActionURL: actionURL, EffectiveAt: expiresAt})
	if err != nil {
		return "", nil, time.Time{}, nil, err
	}
	return token, guardianAgeHandoffDigest(h.GuardianHandoffKey, raw), expiresAt, payload, nil
}

func guardianAgeHandoffDigest(key, raw []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("guardian-age-handoff-token/v1:"))
	_, _ = mac.Write(raw)
	return mac.Sum(nil)
}

func (h Dashboard) renderGuardianAgeHandoff(w http.ResponseWriter, r *http.Request, status int, fieldErrors validation.FieldErrors, success string) {
	if h.GuardianHandoffs == nil {
		h.System.InternalError(w, r)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	item, err := h.GuardianHandoffs.GetForSubject(r.Context(), user.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	notices, err := h.GuardianHandoffs.ListNoticesForSubject(r.Context(), user.ID)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	pageNotices := make([]pages.GuardianAgeHandoffNoticeItem, len(notices))
	for i, notice := range notices {
		pageNotices[i] = pages.GuardianAgeHandoffNoticeItem{Kind: guardianAgeHandoffNoticeLabel(notice.Kind), Message: "O acesso de representação termina em " + notice.Birthday.Format("02/01/2006") + "."}
	}
	if success == "" && h.Sessions != nil {
		success = h.Sessions.PopString(r.Context(), "guardian_handoff_flash")
	}
	meta := h.guardianAgeHandoffMeta(r, "Transição aos 18 anos")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = pages.GuardianAgeHandoff(pages.GuardianAgeHandoffPage{Meta: meta, Status: item.Status, Birthday: item.Birthday.Format("02/01/2006"), ProposedEmail: item.ProposedEmail, Version: item.Version, Notices: pageNotices, Errors: fieldErrors, Success: success}).Render(r.Context(), w)
}

func (h Dashboard) renderGuardianAgeHandoffVerification(w http.ResponseWriter, r *http.Request, status int, success bool) {
	meta := h.PageMeta
	meta.Title = "Confirmar email da transição | MyCFCoimbra"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = pages.GuardianAgeHandoffVerification(pages.GuardianAgeHandoffVerificationPage{Meta: meta, Success: success}).Render(r.Context(), w)
}

func (h Dashboard) renderGuardianAgeHandoffAdminQueue(w http.ResponseWriter, r *http.Request, page pages.GuardianAgeHandoffQueuePage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = pages.GuardianAgeHandoffQueue(page).Render(r.Context(), w)
}

func (h Dashboard) renderGuardianAgeHandoffAdminDetail(w http.ResponseWriter, r *http.Request, status int, fieldErrors validation.FieldErrors, success string) {
	if h.GuardianHandoffs == nil {
		h.System.InternalError(w, r)
		return
	}
	ref, err := uuid.Parse(r.PathValue("ref"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	item, err := h.GuardianHandoffs.GetForAdmin(r.Context(), user.ID, ref)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if success == "" {
		switch r.URL.Query().Get("success") {
		case "identity":
			success = "Identidade confirmada contra o registo existente do clube."
		case "recovery":
			success = "Enviámos um novo link de confirmação para o email indicado."
		}
	}
	page := pages.GuardianAgeHandoffDetailPage{Meta: h.guardianAgeHandoffMeta(r, "Transição aos 18 anos"), Reference: item.Reference.String(), SubjectName: item.SubjectName,
		Status: item.Status, Birthday: item.Birthday.Format("02/01/2006"), ProposedEmail: item.ProposedEmail, Version: item.Version,
		EmailVerified: item.EmailVerifiedAt != nil, IdentityConfirmed: item.IdentityConfirmedAt != nil, Ready: item.ReadyAt != nil,
		RecoveryRequired: item.RecoveryRequiredAt != nil || item.Status == "EMAIL_COLLISION", Completed: item.CompletedAt != nil,
		PersonalInvolvement: item.PersonalInvolvement, RequirePassword: !h.authenticationFresh(r.Context()), Errors: fieldErrors, Success: success}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = pages.GuardianAgeHandoffDetail(page).Render(r.Context(), w)
}

func (h Dashboard) guardianAgeHandoffMeta(r *http.Request, title string) components.PageMeta {
	user, _ := CurrentUserFromContext(r.Context())
	meta := h.PageMeta
	meta.Title = title + " | MyCFCoimbra"
	meta.PageLabel = title
	meta.CurrentPath = r.URL.Path
	meta.CurrentUserName = user.Name
	meta.CurrentUserID = user.ID.String()
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return meta
}

func guardianAgeHandoffStatusLabel(status string) string {
	switch status {
	case "PENDING":
		return "A preparar"
	case "EMAIL_PENDING":
		return "Email por confirmar"
	case "EMAIL_VERIFIED":
		return "Email confirmado"
	case "IDENTITY_CONFIRMED":
		return "Identidade confirmada"
	case "READY":
		return "Pronta para a data de transição"
	case "RECOVERY_REQUIRED":
		return "Recuperação presencial necessária"
	case "EMAIL_COLLISION":
		return "Email em conflito — resolução administrativa"
	case "COMPLETED":
		return "Concluída"
	default:
		return "Estado indisponível"
	}
}
func guardianAgeHandoffNoticeLabel(kind string) string {
	if strings.Contains(kind, "30_DAY") {
		return "Aviso de 30 dias"
	}
	return "Aviso de 7 dias"
}
