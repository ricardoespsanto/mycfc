package handlers

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
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
	Reference          uuid.UUID
	GuardianID         uuid.UUID
	GuardianName       string
	SubjectID          uuid.UUID
	SubmittedLabel     string
	SubjectName        string
	State              string
	StoredState        string
	Version            int64
	CreatedAt          time.Time
	DateOfBirth        time.Time
	VerifiedUntil      *time.Time
	ReviewDueAt        *time.Time
	Conflict           bool
	ConflictActorID    *uuid.UUID
	MinorLoginIssued   bool
	LeaderboardVisible bool
	ProfileComplete    bool
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
	Reference         uuid.UUID
	ActorID           uuid.UUID
	ExpectedVersion   int64
	Action            string
	EvidenceType      string
	EvidenceReference string
	EvidenceDigest    []byte
	ReasonCode        string
}

type GuardianAuthorityStore interface {
	PolicyAvailable(context.Context) (bool, error)
	EvidenceTypes(context.Context) ([]GuardianAuthorityEvidenceType, error)
	ReasonCodes(context.Context) ([]GuardianAuthorityReasonCode, error)
	ListForGuardian(context.Context, uuid.UUID, int32) ([]GuardianAuthorityRelationship, error)
	ListPending(context.Context, uuid.UUID, int32, int32) ([]GuardianAuthorityRelationship, error)
	GetForVerifier(context.Context, uuid.UUID, uuid.UUID) (GuardianAuthorityRelationship, error)
	Transition(context.Context, GuardianAuthorityTransitionInput) error
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
	page := pages.GuardianAuthorityQueuePage{Meta: h.guardianAuthorityMeta(r)}
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
	Action, EvidenceType, EvidenceReference, EvidenceDigest, ReasonCode string
	Errors                                                              validation.FieldErrors
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
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "guardian_authority_flash", "Decisão de representação registada.")
	}
	httpx.Redirect(w, r, "/admin/representacoes", http.StatusSeeOther)
}

func (h Dashboard) validateGuardianAuthorityDecision(r *http.Request) (guardianAuthorityDecisionForm, GuardianAuthorityTransitionInput) {
	form := guardianAuthorityDecisionForm{
		Action: strings.TrimSpace(r.PostForm.Get("action")), EvidenceType: strings.TrimSpace(r.PostForm.Get("evidence_type")),
		EvidenceReference: strings.TrimSpace(r.PostForm.Get("evidence_reference")), EvidenceDigest: strings.TrimSpace(r.PostForm.Get("evidence_digest")),
		ReasonCode: strings.TrimSpace(r.PostForm.Get("reason_code")), Errors: validation.FieldErrors{},
	}
	input := GuardianAuthorityTransitionInput{Action: form.Action, EvidenceType: form.EvidenceType, EvidenceReference: form.EvidenceReference, ReasonCode: form.ReasonCode}
	version, err := strconv.ParseInt(r.PostForm.Get("version"), 10, 64)
	if err != nil || version < 1 {
		form.Errors.Add("form", "A versão do pedido é inválida. Recarregue a página.")
	} else {
		input.ExpectedVersion = version
	}
	if _, err := uuid.Parse(r.PathValue("ref")); err != nil {
		form.Errors.Add("form", "A referência do pedido é inválida.")
	}
	allowedAction := map[string]bool{"VERIFY": true, "REJECT": true, "SUSPEND": true, "EXPIRE": true}
	if !allowedAction[form.Action] {
		form.Errors.Add("action", "Selecione uma decisão válida.")
	}
	if form.ReasonCode == "" {
		form.Errors.Add("reason_code", "Selecione um código de motivo válido.")
	}
	if r.PostForm.Get("confirmed") != "yes" {
		form.Errors.Add("form", "Confirme que aplicou a política aprovada.")
	}
	if form.Action == "VERIFY" || form.Action == "REJECT" {
		if form.EvidenceType == "" {
			form.Errors.Add("evidence_type", "Selecione o tipo de comprovativo.")
		}
		if len(form.EvidenceReference) < 8 || len(form.EvidenceReference) > 200 {
			form.Errors.Add("evidence_reference", "Introduza uma referência opaca válida.")
		}
		digest, digestErr := hex.DecodeString(form.EvidenceDigest)
		if digestErr != nil || len(digest) != 32 {
			form.Errors.Add("evidence_digest", "Introduza uma impressão digital SHA-256 válida.")
		} else {
			input.EvidenceDigest = digest
		}
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
	evidenceTypes, err := h.GuardianAuthority.EvidenceTypes(ctx)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	reasonCodes, err := h.GuardianAuthority.ReasonCodes(ctx)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	storedState := relationship.StoredState
	if storedState == "" {
		storedState = relationship.State
	}
	isMinor := relationship.DateOfBirth.After(h.now().AddDate(-18, 0, 0))
	isParty := relationship.GuardianID == user.ID || relationship.SubjectID == user.ID
	isConflictRecorder := relationship.ConflictActorID != nil && *relationship.ConflictActorID == user.ID
	canVerify := isMinor && relationship.State != "REJECTED"
	canReject := isMinor && storedState == "PENDING"
	canSuspend := isMinor && (relationship.State == "PENDING" || relationship.State == "VERIFIED")
	canExpire := isMinor && ((relationship.State == "EXPIRED" && storedState != "EXPIRED") || storedState == "SUSPENDED")
	canAct := canVerify || canReject || canSuspend || canExpire
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
		CanVerify: canVerify, CanReject: canReject, CanSuspend: canSuspend, CanExpire: canExpire,
		SubjectLabel: subjectLabel, ReadOnlyMessage: readOnlyMessage,
		Errors: form.Errors, Action: form.Action, EvidenceType: form.EvidenceType, EvidenceReference: form.EvidenceReference,
		EvidenceDigest: form.EvidenceDigest, ReasonCode: form.ReasonCode,
	}
	for _, evidenceType := range evidenceTypes {
		page.EvidenceTypes = append(page.EvidenceTypes, pages.GuardianAuthorityEvidenceType{Code: evidenceType.Code, Label: evidenceType.Label})
	}
	for _, reasonCode := range reasonCodes {
		page.ReasonCodes = append(page.ReasonCodes, pages.GuardianAuthorityReasonCode{Code: reasonCode.Code, Label: reasonCode.Label})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = pages.GuardianAuthorityDetail(page).Render(r.Context(), w)
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
