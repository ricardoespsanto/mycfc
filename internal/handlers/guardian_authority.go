package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrGuardianAuthorityConflict          = errors.New("guardian authority changed concurrently")
	ErrGuardianAuthorityPolicyUnavailable = errors.New("guardian authority policy unavailable")
	ErrGuardianAuthorityInvalid           = errors.New("guardian authority decision invalid")
	ErrGuardianAuthorityForbidden         = errors.New("guardian authority decision forbidden")
)

type GuardianAuthorityRelationship struct {
	Reference           uuid.UUID
	GuardianID          uuid.UUID
	GuardianName        string
	SubjectID           uuid.UUID
	SubmittedLabel      string
	SubjectName         string
	State               string
	StoredState         string
	Version             int64
	CreatedAt           time.Time
	DateOfBirth         time.Time
	VerifiedUntil       *time.Time
	ReviewDueAt         *time.Time
	Conflict            bool
	ConflictActorID     *uuid.UUID
	PersonalInvolvement bool
	MinorLoginIssued    bool
	LeaderboardVisible  bool
	ProfileComplete     bool
	RenewalReference    *uuid.UUID
	RenewalResponse     string
	RenewalStatus       string
	RenewalExpiry       *time.Time
	AgeHandoffStatus    string
	AgeHandoffBirthday  *time.Time
}

type GuardianAuthorityEvidenceType struct {
	Code  string
	Label string
}

type GuardianAuthorityReasonCode struct {
	Code  string
	Label string
}

type GuardianAuthorityTransitionInput struct {
	Reference        uuid.UUID
	ActorID          uuid.UUID
	ExpectedVersion  int64
	Action           string
	EvidenceCategory string
	ReasonCode       string
}

type GuardianAuthorityStore interface {
	PolicyAvailable(context.Context) (bool, error)
	EvidenceTypes(context.Context) ([]GuardianAuthorityEvidenceType, error)
	ReasonCodes(context.Context) ([]GuardianAuthorityReasonCode, error)
	ListForGuardian(context.Context, uuid.UUID, int32) ([]GuardianAuthorityRelationship, error)
	ListPending(context.Context, uuid.UUID, int32, int32) ([]GuardianAuthorityRelationship, error)
	GetForVerifier(context.Context, uuid.UUID, uuid.UUID) (GuardianAuthorityRelationship, error)
	Transition(context.Context, GuardianAuthorityTransitionInput) error
	IssueInvitation(context.Context, uuid.UUID, string, []byte) (GuardianAuthorityInvitation, error)
	ListInvitations(context.Context, uuid.UUID, int32) ([]GuardianAuthorityInvitation, error)
	RevokeInvitation(context.Context, uuid.UUID, uuid.UUID) error
	SubmitRenewal(context.Context, uuid.UUID, uuid.UUID, int64, string) error
}

func (h Dashboard) SubmitGuardianAuthorityRenewal(w http.ResponseWriter, r *http.Request) {
	if h.GuardianAuthority == nil || r.ParseForm() != nil {
		h.System.InternalError(w, r)
		return
	}
	reference, err := uuid.Parse(r.PathValue("ref"))
	version, versionErr := strconv.ParseInt(r.PostForm.Get("version"), 10, 64)
	response := strings.TrimSpace(r.PostForm.Get("response_code"))
	if err != nil || versionErr != nil || version < 1 || (response != "NADA_MUDOU" && response != "DADOS_MUDARAM") {
		h.renderGuardianRenewalError(w, r, http.StatusUnprocessableEntity, "Selecione uma resposta válida e volte a tentar.")
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	err = h.GuardianAuthority.SubmitRenewal(r.Context(), user.ID, reference, version, response)
	if errors.Is(err, ErrGuardianAuthorityConflict) {
		h.renderGuardianRenewalError(w, r, http.StatusConflict, "A representação foi alterada. Reveja o estado atual antes de responder.")
		return
	}
	if errors.Is(err, ErrGuardianAuthorityInvalid) {
		h.renderGuardianRenewalError(w, r, http.StatusUnprocessableEntity, "A renovação ainda não está disponível ou já foi enviada.")
		return
	}
	if errors.Is(err, ErrGuardianAuthorityForbidden) {
		h.System.Forbidden(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "guardian_flash", "Informação de renovação enviada para revisão pelo clube.")
	}
	httpx.Redirect(w, r, "/dashboard/guardian", http.StatusSeeOther)
}

func (h Dashboard) renderGuardianRenewalError(w http.ResponseWriter, r *http.Request, status int, message string) {
	user, _ := CurrentUserFromContext(r.Context())
	items, err := h.GuardianAuthority.ListForGuardian(r.Context(), user.ID, 10)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.renderGuardian(w, r, status, items, guardianDependentForm{Errors: validation.FieldErrors{"renewal": message}})
}

type GuardianAuthorityInvitation struct {
	Reference             uuid.UUID
	Email                 string
	IssuedAt, ExpiresAt   time.Time
	RevokedAt, ConsumedAt *time.Time
}

func (h Dashboard) GuardianAuthorityInvitations(w http.ResponseWriter, r *http.Request) {
	h.renderGuardianAuthorityInvitations(w, r, http.StatusOK, "", validation.FieldErrors{})
}

func (h Dashboard) IssueGuardianAuthorityInvitation(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderGuardianAuthorityInvitations(w, r, http.StatusBadRequest, "", validation.FieldErrors{})
		return
	}
	email, err := validation.NormalizeEmail(r.PostForm.Get("email"))
	if err != nil {
		h.renderGuardianAuthorityInvitations(w, r, http.StatusUnprocessableEntity, "", validation.FieldErrors{"email": err.Error()})
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	if err := h.confirmGuardianAuthorityMutation(r, user.ID); err != nil {
		if errors.Is(err, ErrGuardianAuthentication) {
			limitErr := h.reserveGuardianAuthenticationFailure(r, user.ID)
			if errors.Is(limitErr, ErrGuardianApplicationLimited) {
				w.Header().Set("Retry-After", "900")
				h.renderGuardianAuthorityInvitations(w, r, http.StatusTooManyRequests, "", validation.FieldErrors{"password": "Aguarde 15 minutos antes de voltar a confirmar a palavra-passe."})
				return
			}
			if limitErr != nil {
				h.System.InternalError(w, r)
				return
			}
			h.renderGuardianAuthorityInvitations(w, r, http.StatusUnprocessableEntity, "", validation.FieldErrors{"password": "Não foi possível confirmar a palavra-passe atual."})
			return
		}
		h.System.InternalError(w, r)
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil || len(h.GuardianInvitationKey) < 32 {
		h.System.InternalError(w, r)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if _, err := h.GuardianAuthority.IssueInvitation(r.Context(), user.ID, email, guardianInvitationDigest(h.GuardianInvitationKey, token)); err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.renderGuardianAuthorityInvitations(w, r, http.StatusCreated, token, validation.FieldErrors{})
}

func (h Dashboard) RevokeGuardianAuthorityInvitation(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderGuardianAuthorityInvitations(w, r, http.StatusBadRequest, "", validation.FieldErrors{})
		return
	}
	reference, err := uuid.Parse(r.PathValue("ref"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	if err := h.confirmGuardianAuthorityMutation(r, user.ID); err != nil {
		if errors.Is(err, ErrGuardianAuthentication) {
			limitErr := h.reserveGuardianAuthenticationFailure(r, user.ID)
			if errors.Is(limitErr, ErrGuardianApplicationLimited) {
				w.Header().Set("Retry-After", "900")
				h.renderGuardianAuthorityInvitations(w, r, http.StatusTooManyRequests, "", validation.FieldErrors{"password": "Aguarde 15 minutos antes de voltar a confirmar a palavra-passe."})
				return
			}
			if limitErr != nil {
				h.System.InternalError(w, r)
				return
			}
			h.renderGuardianAuthorityInvitations(w, r, http.StatusUnprocessableEntity, "", validation.FieldErrors{"password": "Não foi possível confirmar a palavra-passe atual."})
			return
		}
		h.System.InternalError(w, r)
		return
	}
	if err := h.GuardianAuthority.RevokeInvitation(r.Context(), user.ID, reference); err != nil {
		h.System.InternalError(w, r)
		return
	}
	httpx.Redirect(w, r, "/admin/representacoes/convites", http.StatusSeeOther)
}

func (h Dashboard) confirmGuardianAuthorityMutation(r *http.Request, actorID uuid.UUID) error {
	if h.authenticationFresh(r.Context()) {
		return nil
	}
	password := r.PostForm.Get("password")
	if password == "" || len(password) > 1024 || h.Dependents == nil {
		return ErrGuardianAuthentication
	}
	if err := h.Dependents.Reauthenticate(r.Context(), actorID, password); err != nil {
		return err
	}
	if h.Sessions == nil || h.Sessions.RenewToken(r.Context()) != nil {
		return errors.New("guardian authority session rotation failed")
	}
	h.Sessions.Put(r.Context(), "authenticated_at", h.now().UTC().Format(time.RFC3339Nano))
	return nil
}

func (h Dashboard) reserveGuardianAuthenticationFailure(r *http.Request, actorID uuid.UUID) error {
	if h.Dependents == nil {
		return errors.New("guardian authority authentication limiter unavailable")
	}
	var ip *netip.Addr
	if value, ok := httpx.RemoteIP(r.Context()); ok {
		ip = &value
	}
	return h.Dependents.ReserveAttempt(r.Context(), actorID, ip, "AUTH_INVALID")
}

func (h Dashboard) renderGuardianAuthorityInvitations(w http.ResponseWriter, r *http.Request, status int, token string, fieldErrors validation.FieldErrors) {
	user, _ := CurrentUserFromContext(r.Context())
	items, err := h.GuardianAuthority.ListInvitations(r.Context(), user.ID, 100)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	page := pages.GuardianAuthorityInvitationsPage{Meta: h.guardianAuthorityMeta(r), Token: token, Errors: fieldErrors, RequirePassword: !h.authenticationFresh(r.Context())}
	for _, item := range items {
		state := "Ativo"
		if item.ConsumedAt != nil {
			state = "Utilizado"
		} else if item.RevokedAt != nil {
			state = "Revogado"
		} else if !item.ExpiresAt.After(h.now()) {
			state = "Expirado"
		}
		email := item.Email
		if email == "" {
			email = "Email removido"
		}
		page.Items = append(page.Items, pages.GuardianAuthorityInvitationItem{Reference: item.Reference.String(), Email: email, ExpiresAt: item.ExpiresAt.In(h.location()).Format("02/01/2006 15:04"), State: state, CanRevoke: state == "Ativo"})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = pages.GuardianAuthorityInvitations(page).Render(r.Context(), w)
}

func (h Dashboard) GuardianAuthorityQueue(w http.ResponseWriter, r *http.Request) {
	if h.GuardianAuthority == nil {
		h.System.InternalError(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	user, _ := CurrentUserFromContext(r.Context())
	items, err := h.GuardianAuthority.ListPending(ctx, user.ID, 100, 0)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	page := pages.GuardianAuthorityQueuePage{Meta: h.guardianAuthorityMeta(r), CanManageInvitations: user.IsAdmin}
	for _, item := range items {
		page.Items = append(page.Items, pages.GuardianAuthorityQueueItem{
			Reference: item.Reference.String(), SubmittedLabel: item.SubmittedLabel,
			Status:     guardianAuthorityStatus(item.State, item.Conflict),
			ReceivedAt: item.CreatedAt.In(h.location()).Format("02/01/2006 15:04"),
			URL:        "/admin/representacoes/" + item.Reference.String(),
		})
	}
	if h.Sessions != nil {
		page.Success = h.Sessions.PopString(r.Context(), "guardian_authority_flash")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = pages.GuardianAuthorityQueue(page).Render(r.Context(), w)
}

func (h Dashboard) GuardianAuthorityDetail(w http.ResponseWriter, r *http.Request) {
	h.renderGuardianAuthorityDetail(w, r, http.StatusOK, guardianAuthorityDecisionForm{})
}

type guardianAuthorityDecisionForm struct {
	Action, EvidenceCategory, ReasonCode string
	Errors                               validation.FieldErrors
}

func (h Dashboard) GuardianAuthorityTransition(w http.ResponseWriter, r *http.Request) {
	if h.GuardianAuthority == nil {
		h.System.InternalError(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderGuardianAuthorityDetail(w, r, http.StatusBadRequest, guardianAuthorityDecisionForm{})
		return
	}
	form, input := h.validateGuardianAuthorityDecision(r)
	if !form.Errors.Empty() {
		h.renderGuardianAuthorityDetail(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	input.ActorID = user.ID
	input.Reference, _ = uuid.Parse(r.PathValue("ref"))
	if err := h.confirmGuardianAuthorityMutation(r, user.ID); err != nil {
		if errors.Is(err, ErrGuardianAuthentication) {
			limitErr := h.reserveGuardianAuthenticationFailure(r, user.ID)
			if errors.Is(limitErr, ErrGuardianApplicationLimited) {
				w.Header().Set("Retry-After", "900")
				form.Errors.Add("password", "Aguarde 15 minutos antes de voltar a confirmar a palavra-passe.")
				h.renderGuardianAuthorityDetail(w, r, http.StatusTooManyRequests, form)
				return
			}
			if limitErr != nil {
				h.System.InternalError(w, r)
				return
			}
			form.Errors.Add("password", "Não foi possível confirmar a palavra-passe atual.")
			h.renderGuardianAuthorityDetail(w, r, http.StatusUnprocessableEntity, form)
			return
		}
		h.System.InternalError(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	err := h.GuardianAuthority.Transition(ctx, input)
	if errors.Is(err, ErrGuardianAuthorityConflict) {
		form.Errors.Add("form", "O pedido foi alterado por outra pessoa. Reveja o estado atual antes de decidir.")
		h.renderGuardianAuthorityDetail(w, r, http.StatusConflict, form)
		return
	}
	if errors.Is(err, ErrGuardianAuthorityPolicyUnavailable) {
		form.Errors.Add("form", "A política de verificação já não está disponível. Nenhuma decisão foi registada.")
		h.renderGuardianAuthorityDetail(w, r, http.StatusConflict, form)
		return
	}
	if errors.Is(err, ErrGuardianAuthorityInvalid) {
		form.Errors.Add("form", "A decisão não corresponde ao estado atual ou à política aprovada.")
		h.renderGuardianAuthorityDetail(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	if errors.Is(err, ErrGuardianAuthorityForbidden) {
		h.System.Forbidden(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if h.Logger != nil {
		h.Logger.Info("guardian authority review event", "event", "guardian_authority_review", "action", input.Action, "outcome", "recorded")
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "guardian_authority_flash", "Decisão de representação registada.")
	}
	httpx.Redirect(w, r, "/admin/representacoes", http.StatusSeeOther)
}

func (h Dashboard) validateGuardianAuthorityDecision(r *http.Request) (guardianAuthorityDecisionForm, GuardianAuthorityTransitionInput) {
	form := guardianAuthorityDecisionForm{
		Action: strings.TrimSpace(r.PostForm.Get("action")), EvidenceCategory: strings.TrimSpace(r.PostForm.Get("evidence_category")),
		ReasonCode: strings.TrimSpace(r.PostForm.Get("reason_code")), Errors: validation.FieldErrors{},
	}
	input := GuardianAuthorityTransitionInput{Action: form.Action, EvidenceCategory: form.EvidenceCategory, ReasonCode: form.ReasonCode}
	version, err := strconv.ParseInt(r.PostForm.Get("version"), 10, 64)
	if err != nil || version < 1 {
		form.Errors.Add("form", "A versão do pedido é inválida. Recarregue a página.")
	} else {
		input.ExpectedVersion = version
	}
	if _, err := uuid.Parse(r.PathValue("ref")); err != nil {
		form.Errors.Add("form", "A referência do pedido é inválida.")
	}
	allowedAction := map[string]bool{"APPROVE": true, "RENEW": true, "REJECT": true, "SUSPEND": true, "END": true}
	if !allowedAction[form.Action] {
		form.Errors.Add("action", "Selecione uma decisão válida.")
	}
	evidence := map[string]bool{"CLUB_REGISTRATION_RECORD": true, "IN_PERSON_ID_AND_CIVIL_RECORD": true, "COURT_OR_LEGAL_AUTHORITY": true}
	if !evidence[form.EvidenceCategory] {
		form.Errors.Add("evidence_category", "Selecione uma categoria de verificação válida.")
	}
	reasons := map[string]map[string]bool{
		"APPROVE": {"RELATIONSHIP_CONFIRMED": true}, "RENEW": {"RELATIONSHIP_CONFIRMED": true},
		"REJECT":  {"EVIDENCE_INSUFFICIENT": true, "AUTHORITY_NOT_ESTABLISHED": true},
		"SUSPEND": {"CONFLICT": true, "AUTHORITY_CHANGED": true, "UNCERTAINTY": true},
		"END":     {"AUTHORITY_ENDED": true},
	}
	if !reasons[form.Action][form.ReasonCode] {
		form.Errors.Add("reason_code", "Selecione um motivo válido para esta decisão.")
	}
	if r.PostForm.Get("attested") != "yes" {
		form.Errors.Add("form", "Confirme a verificação efetuada e a categoria selecionada.")
	}
	if (form.Action == "SUSPEND" || form.Action == "END") && r.PostForm.Get("access_end_confirmed") != "yes" {
		form.Errors.Add("confirmation", "Confirme explicitamente que pretende terminar o acesso.")
	}
	return form, input
}

func (h Dashboard) renderGuardianAuthorityDetail(w http.ResponseWriter, r *http.Request, status int, form guardianAuthorityDecisionForm) {
	ref, err := uuid.Parse(r.PathValue("ref"))
	if err != nil || h.GuardianAuthority == nil {
		h.System.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	user, _ := CurrentUserFromContext(r.Context())
	relationship, err := h.GuardianAuthority.GetForVerifier(ctx, ref, user.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	storedState := relationship.StoredState
	if storedState == "" {
		storedState = relationship.State
	}
	isMinor := relationship.DateOfBirth.After(h.now().AddDate(-18, 0, 0))
	isParty := relationship.PersonalInvolvement || relationship.GuardianID == user.ID || relationship.SubjectID == user.ID
	isConflictRecorder := relationship.ConflictActorID != nil && *relationship.ConflictActorID == user.ID
	renewalPending := relationship.RenewalStatus == "SUBMITTED"
	canApprove := isMinor && !renewalPending && (storedState == "PENDING" || storedState == "SUSPENDED")
	canRenew := isMinor && renewalPending && (storedState == "VERIFIED" || storedState == "SUSPENDED" || relationship.State == "EXPIRED")
	canReject := isMinor && (storedState == "PENDING" || storedState == "SUSPENDED")
	canSuspend := isMinor && (relationship.State == "PENDING" || relationship.State == "VERIFIED")
	canEnd := isMinor && (relationship.State == "VERIFIED" || relationship.State == "SUSPENDED")
	canAct := canApprove || canRenew || canReject || canSuspend || canEnd
	readOnlyMessage := ""
	subjectLabel := "Menor indicado"
	if !isMinor {
		subjectLabel = "Pessoa indicada"
		readOnlyMessage = "A pessoa indicada atingiu a maioridade. Esta representação terminou; contacte o clube para estabelecer ou recuperar o acesso à conta adulta correta."
	} else if relationship.State == "REJECTED" {
		readOnlyMessage = "Este pedido terminou sem aprovação. Uma nova associação exige um novo pedido da pessoa adulta responsável."
	}
	page := pages.GuardianAuthorityDetailPage{
		Meta: h.guardianAuthorityMeta(r), Reference: relationship.Reference.String(), GuardianName: relationship.GuardianName,
		SubjectName: relationship.SubjectName, DateOfBirth: relationship.DateOfBirth.Format("02/01/2006"),
		Status: guardianAuthorityStatus(relationship.State, relationship.Conflict), Version: relationship.Version,
		CanDecide: !isParty && !isConflictRecorder && canAct, SelfConflict: isParty, ConflictRecorder: isConflictRecorder,
		CanApprove: canApprove, CanRenew: canRenew, CanReject: canReject, CanSuspend: canSuspend, CanEnd: canEnd,
		SubjectLabel: subjectLabel, ReadOnlyMessage: readOnlyMessage,
		Expiry: formatOptionalDate(relationship.VerifiedUntil, h.location()), RenewalStatus: relationship.RenewalStatus, RenewalResponse: relationship.RenewalResponse,
		RequirePassword: !h.authenticationFresh(r.Context()),
		Errors:          form.Errors, Action: form.Action, EvidenceCategory: form.EvidenceCategory, ReasonCode: form.ReasonCode,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = pages.GuardianAuthorityDetail(page).Render(r.Context(), w)
}

func formatOptionalDate(value *time.Time, location *time.Location) string {
	if value == nil {
		return ""
	}
	return value.In(location).Format("02/01/2006")
}

func (h Dashboard) guardianAuthorityMeta(r *http.Request) components.PageMeta {
	user, _ := CurrentUserFromContext(r.Context())
	meta := h.PageMeta
	meta.Title = "Verificação de representação | MyCFCoimbra"
	meta.PageLabel = "Verificação de representação"
	meta.CurrentPath = r.URL.Path
	meta.CurrentUserName = user.Name
	meta.CurrentUserID = user.ID.String()
	meta.EmailVerificationPending = !user.IsDependent && !user.EmailVerified
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	meta.Breadcrumbs = []components.NavigationItem{{Label: "Verificação de representação", Path: "/admin/representacoes"}}
	return meta
}

func guardianAuthorityStatus(state string, conflict bool) string {
	if conflict {
		return "Em revisão pelo clube"
	}
	switch state {
	case "PENDING":
		return "A aguardar verificação"
	case "VERIFIED":
		return "Representação verificada"
	case "SUSPENDED":
		return "Acesso suspenso"
	case "EXPIRED":
		return "Verificação expirada"
	case "REJECTED":
		return "Pedido não aprovado"
	default:
		return "Estado indisponível"
	}
}
