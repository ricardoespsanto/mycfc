package handlers

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	pr "github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type PrivacyRequestStore interface {
	Available(context.Context) (pr.AdoptedPolicy, error)
	Subjects(context.Context, uuid.UUID) ([]dbgen.User, error)
	List(context.Context, uuid.UUID, bool, string, string, string) ([]dbgen.DataErasureRequest, error)
	View(context.Context, uuid.UUID, uuid.UUID, bool) (pr.View, error)
	Submit(context.Context, pr.SubmitInput) (dbgen.DataErasureRequest, error)
	Change(context.Context, pr.ReviewInput) (dbgen.DataErasureRequest, error)
	StartExecution(context.Context, pr.StartInput) (dbgen.PrivacyErasureExecution, error)
	ValidateCompletionLink(context.Context, string) error
	ConsumeCompletionDetail(context.Context, string) (pr.CompletionDetail, error)
	CompletionControlSnapshot(context.Context, uuid.UUID, uuid.UUID) (pr.CompletionControlSnapshot, error)
	ProposeTerminalRequeue(context.Context, uuid.UUID, uuid.UUID) (pr.TerminalRequeueProposal, error)
	ApproveTerminalRequeue(context.Context, uuid.UUID, uuid.UUID, []byte) error
	ActivationControlSnapshot(context.Context, uuid.UUID) (pr.ActivationControlSnapshot, error)
	ProposeActivation(context.Context, uuid.UUID, string, []uuid.UUID) (pr.ActivationProposal, error)
	ApproveActivation(context.Context, uuid.UUID, uuid.UUID, []byte) error
}
type PrivacyRequests struct {
	Service               PrivacyRequestStore
	Sessions              *scs.SessionManager
	System                System
	PageMeta              components.PageMeta
	ContactURL            string
	CompletionLinkKey     []byte
	CompletionStateRandom io.Reader
	SecureCookies         bool
	Now                   func() time.Time
}

func (h PrivacyRequests) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}
func (h PrivacyRequests) meta(r *http.Request) components.PageMeta {
	u, _ := CurrentUserFromContext(r.Context())
	m := h.PageMeta
	m.Title = "Privacidade e dados pessoais | MyCFCoimbra"
	m.PageLabel = "Privacidade e dados pessoais"
	m.CurrentPath = r.URL.Path
	m.CurrentUserID = u.ID.String()
	m.CurrentUserName = u.Name
	m.Navigation = dashboardNavigation(u)
	m.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return m
}
func privacyHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "same-origin")
}

func completionDetailHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
}
func (h PrivacyRequests) render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	privacyHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.Render(r.Context(), w); err != nil {
		return
	}
}
func privacyManagement(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/admin/privacidade")
}
func privacyDate(t time.Time) string {
	l, _ := time.LoadLocation("Europe/Lisbon")
	return t.In(l).Format("02/01/2006 15:04")
}

func (h PrivacyRequests) CompletionDetail(w http.ResponseWriter, r *http.Request) {
	completionDetailHeaders(w)
	token := r.PathValue("token")
	if err := h.Service.ValidateCompletionLink(r.Context(), token); err != nil {
		h.clearCompletionState(w)
		if !errors.Is(err, pr.ErrCompletionLinkUnavailable) {
			h.System.InternalError(w, r)
			return
		}
		h.renderCompletionDetail(w, r, http.StatusNotFound, pages.PrivacyCompletionDetailPage{Meta: h.completionMeta(r), Unavailable: true, ContactURL: h.ContactURL})
		return
	}
	state, confirmationNonce, err := h.sealCompletionState(token)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.setCompletionState(w, state)
	h.renderCompletionDetail(w, r, http.StatusOK, pages.PrivacyCompletionDetailPage{Meta: h.completionMeta(r), Confirm: true, ConfirmationNonce: confirmationNonce, ContactURL: h.ContactURL})
}

func (h PrivacyRequests) ConsumeCompletionDetail(w http.ResponseWriter, r *http.Request) {
	completionDetailHeaders(w)
	if err := r.ParseForm(); err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	cookie, err := r.Cookie(privacyCompletionStateCookie)
	h.clearCompletionState(w)
	if err != nil {
		h.renderCompletionDetail(w, r, http.StatusNotFound, pages.PrivacyCompletionDetailPage{Meta: h.completionMeta(r), Unavailable: true, ContactURL: h.ContactURL})
		return
	}
	token, confirmationNonce, err := h.openCompletionState(cookie.Value)
	if err != nil || subtle.ConstantTimeCompare([]byte(confirmationNonce), []byte(r.PostForm.Get("completion_state"))) != 1 {
		h.renderCompletionDetail(w, r, http.StatusNotFound, pages.PrivacyCompletionDetailPage{Meta: h.completionMeta(r), Unavailable: true, ContactURL: h.ContactURL})
		return
	}
	detail, err := h.Service.ConsumeCompletionDetail(r.Context(), token)
	if err != nil {
		if !errors.Is(err, pr.ErrCompletionLinkUnavailable) {
			h.System.InternalError(w, r)
			return
		}
		h.renderCompletionDetail(w, r, http.StatusNotFound, pages.PrivacyCompletionDetailPage{Meta: h.completionMeta(r), Unavailable: true, ContactURL: h.ContactURL})
		return
	}
	h.renderCompletionDetail(w, r, http.StatusOK, pages.PrivacyCompletionDetailPage{
		Meta: h.completionMeta(r), Reference: detail.RequestReference.String(), CompletedAt: privacyDate(detail.CompletedAt),
		ManifestSHA256: detail.ManifestSHA256, Categories: detail.Categories, Checkpoints: detail.Checkpoints,
		ObjectTargets: detail.ObjectTargets, ProviderTargets: detail.ProviderTargets, ContactURL: h.ContactURL,
	})
}

const (
	privacyCompletionStateCookie = "mycfc_privacy_completion"
	privacyCompletionStateTTL    = 5 * time.Minute
	privacyCompletionStateAAD    = "mycfc/privacy-completion-browser-state/v1"
	privacyCompletionNonceBytes  = 16
)

func (h PrivacyRequests) completionMeta(r *http.Request) components.PageMeta {
	meta := h.PageMeta
	meta.Title = "Conclusão do pedido de privacidade | MyCFCoimbra"
	meta.PageLabel = "Conclusão do pedido de privacidade"
	meta.CurrentPath = "/privacidade/conclusao/*"
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return meta
}

func (h PrivacyRequests) completionStateAEAD() (cipher.AEAD, error) {
	if len(h.CompletionLinkKey) < sha256.Size {
		return nil, errors.New("completion browser state key is unavailable")
	}
	mac := hmac.New(sha256.New, h.CompletionLinkKey)
	_, _ = mac.Write([]byte(privacyCompletionStateAAD))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (h PrivacyRequests) sealCompletionState(token string) (string, string, error) {
	if token == "" || len(token) > 128 {
		return "", "", errors.New("completion browser state is invalid")
	}
	aead, err := h.completionStateAEAD()
	if err != nil {
		return "", "", err
	}
	nonce := make([]byte, aead.NonceSize())
	confirmationNonce := make([]byte, privacyCompletionNonceBytes)
	random := h.CompletionStateRandom
	if random == nil {
		random = rand.Reader
	}
	if _, err = io.ReadFull(random, nonce); err != nil {
		return "", "", err
	}
	if _, err = io.ReadFull(random, confirmationNonce); err != nil {
		return "", "", err
	}
	plain := make([]byte, 8+len(confirmationNonce)+len(token))
	binary.BigEndian.PutUint64(plain[:8], uint64(h.now().Add(privacyCompletionStateTTL).Unix()))
	copy(plain[8:], confirmationNonce)
	copy(plain[8+len(confirmationNonce):], token)
	sealed := aead.Seal(nonce, nonce, plain, []byte(privacyCompletionStateAAD))
	return base64.RawURLEncoding.EncodeToString(sealed), base64.RawURLEncoding.EncodeToString(confirmationNonce), nil
}

func (h PrivacyRequests) openCompletionState(value string) (string, string, error) {
	aead, err := h.completionStateAEAD()
	if err != nil {
		return "", "", err
	}
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead()+9+privacyCompletionNonceBytes {
		return "", "", errors.New("completion browser state is invalid")
	}
	nonce := sealed[:aead.NonceSize()]
	plain, err := aead.Open(nil, nonce, sealed[aead.NonceSize():], []byte(privacyCompletionStateAAD))
	if err != nil || len(plain) < 9+privacyCompletionNonceBytes {
		return "", "", errors.New("completion browser state is invalid")
	}
	expiresAt := time.Unix(int64(binary.BigEndian.Uint64(plain[:8])), 0)
	if !expiresAt.After(h.now()) || expiresAt.After(h.now().Add(privacyCompletionStateTTL+time.Minute)) {
		return "", "", errors.New("completion browser state is expired")
	}
	confirmationNonce := base64.RawURLEncoding.EncodeToString(plain[8 : 8+privacyCompletionNonceBytes])
	return string(plain[8+privacyCompletionNonceBytes:]), confirmationNonce, nil
}

func (h PrivacyRequests) setCompletionState(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{Name: privacyCompletionStateCookie, Value: value, Path: "/privacidade/conclusao", MaxAge: int(privacyCompletionStateTTL.Seconds()), HttpOnly: true, Secure: h.SecureCookies, SameSite: http.SameSiteStrictMode})
}

func (h PrivacyRequests) clearCompletionState(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: privacyCompletionStateCookie, Path: "/privacidade/conclusao", MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, Secure: h.SecureCookies, SameSite: http.SameSiteStrictMode})
}

func (h PrivacyRequests) ControlLookup(w http.ResponseWriter, r *http.Request) {
	privacyHeaders(w)
	u, _ := CurrentUserFromContext(r.Context())
	if !u.IsAdmin && !u.CanExecutePrivacy {
		h.System.NotFound(w, r)
		return
	}
	page := pages.PrivacyControlLookupPage{Meta: h.meta(r), Reference: strings.TrimSpace(r.URL.Query().Get("ref"))}
	if page.Reference == "" {
		h.render(w, r, http.StatusOK, pages.PrivacyControlLookup(page))
		return
	}
	if _, err := uuid.Parse(page.Reference); err != nil {
		page.Error = "Introduza uma referência de pedido válida."
		h.render(w, r, http.StatusUnprocessableEntity, pages.PrivacyControlLookup(page))
		return
	}
	http.Redirect(w, r, "/admin/privacidade/controlo/"+page.Reference, http.StatusSeeOther)
}

func (h PrivacyRequests) CompletionControl(w http.ResponseWriter, r *http.Request) {
	privacyHeaders(w)
	h.renderCompletionControl(w, r, http.StatusOK, "")
}

func (h PrivacyRequests) renderCompletionControl(w http.ResponseWriter, r *http.Request, status int, actionError string) {
	u, _ := CurrentUserFromContext(r.Context())
	ref, err := uuid.Parse(r.PathValue("ref"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	snapshot, err := h.Service.CompletionControlSnapshot(r.Context(), u.ID, ref)
	if err != nil {
		if errors.Is(err, pr.ErrForbidden) || errors.Is(err, pr.ErrCompletionUnavailable) || errors.Is(err, pr.ErrInvalid) {
			h.System.NotFound(w, r)
			return
		}
		h.System.InternalError(w, r)
		return
	}
	page := pages.PrivacyCompletionControlPage{
		Meta: h.meta(r), Reference: snapshot.RequestReference.String(), RequestStatus: snapshot.RequestStatus,
		ExecutionStatus: snapshot.ExecutionStatus, Error: actionError, ContactURL: h.ContactURL,
	}
	switch r.URL.Query().Get("resultado") {
	case "":
	case "proposta":
		page.Success = "A nova tentativa foi proposta e aguarda aprovação independente."
	case "aprovada":
		page.Success = "A nova tentativa foi aprovada e ficou disponível para processamento."
	default:
		h.System.RequestRejected(w, r)
		return
	}
	for _, job := range snapshot.Jobs {
		item := pages.PrivacyControlJob{
			ID: job.JobID.String(), CategoryCode: job.CategoryCode, PurposeCode: job.PurposeCode,
			Status: job.Status, AttemptCount: strconv.FormatInt(int64(job.AttemptCount), 10), FailureStage: job.FailureStage,
			FailureCode: job.FailureCode, CanPropose: job.CanProposeRequeue, CanApprove: job.CanApproveRequeue,
		}
		if job.PendingRequeueProposal != nil {
			item.ProposedAt = privacyDate(job.PendingRequeueProposal.ProposedAt)
		}
		page.Jobs = append(page.Jobs, item)
	}
	h.render(w, r, status, pages.PrivacyCompletionControl(page))
}

func (h PrivacyRequests) ProposeTerminalRequeue(w http.ResponseWriter, r *http.Request) {
	h.terminalRequeue(w, r, false)
}

func (h PrivacyRequests) ApproveTerminalRequeue(w http.ResponseWriter, r *http.Request) {
	h.terminalRequeue(w, r, true)
}

func (h PrivacyRequests) terminalRequeue(w http.ResponseWriter, r *http.Request, approve bool) {
	privacyHeaders(w)
	if err := r.ParseForm(); err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	u, _ := CurrentUserFromContext(r.Context())
	ref, refErr := uuid.Parse(r.PathValue("ref"))
	jobID, jobErr := uuid.Parse(r.PostForm.Get("job_id"))
	if refErr != nil || jobErr != nil {
		h.System.RequestRejected(w, r)
		return
	}
	if r.PostForm.Get("confirmed") != "yes" {
		h.renderCompletionControl(w, r, http.StatusUnprocessableEntity, "Confirme a revisão antes de continuar.")
		return
	}
	snapshot, err := h.Service.CompletionControlSnapshot(r.Context(), u.ID, ref)
	if err != nil {
		h.controlFailure(w, r, err)
		return
	}
	var selected *pr.CompletionControlJob
	for i := range snapshot.Jobs {
		if snapshot.Jobs[i].JobID == jobID {
			selected = &snapshot.Jobs[i]
			break
		}
	}
	if selected == nil || (!approve && !selected.CanProposeRequeue) || (approve && (!selected.CanApproveRequeue || selected.PendingRequeueProposal == nil)) {
		h.renderCompletionControl(w, r, http.StatusUnprocessableEntity, "A ação já não está disponível. Atualize o estado e volte a rever.")
		return
	}
	if approve {
		err = h.Service.ApproveTerminalRequeue(r.Context(), u.ID, selected.PendingRequeueProposal.ID, selected.PendingRequeueProposal.Digest)
	} else {
		_, err = h.Service.ProposeTerminalRequeue(r.Context(), u.ID, jobID)
	}
	if err != nil {
		if errors.Is(err, pr.ErrRequeueUnavailable) || errors.Is(err, pr.ErrInvalid) {
			h.renderCompletionControl(w, r, http.StatusUnprocessableEntity, "A ação já não está disponível. Atualize o estado e volte a rever.")
			return
		}
		h.controlFailure(w, r, err)
		return
	}
	result := "proposta"
	if approve {
		result = "aprovada"
	}
	http.Redirect(w, r, "/admin/privacidade/controlo/"+ref.String()+"?resultado="+result, http.StatusSeeOther)
}

func (h PrivacyRequests) ActivationControl(w http.ResponseWriter, r *http.Request) {
	privacyHeaders(w)
	h.renderActivationControl(w, r, http.StatusOK, "")
}

func (h PrivacyRequests) renderActivationControl(w http.ResponseWriter, r *http.Request, status int, actionError string) {
	u, _ := CurrentUserFromContext(r.Context())
	snapshot, err := h.Service.ActivationControlSnapshot(r.Context(), u.ID)
	if err != nil {
		h.controlFailure(w, r, err)
		return
	}
	page := pages.PrivacyActivationControlPage{
		Meta: h.meta(r), PolicyVersion: snapshot.PolicyVersion, Ready: snapshot.Ready, CanPropose: snapshot.CanPropose,
		CanRenew: snapshot.CanRenew, CanApprove: snapshot.CanApprove, Error: actionError,
	}
	switch r.URL.Query().Get("resultado") {
	case "":
	case "proposta":
		page.Success = "A ativação foi proposta e aguarda aprovação independente."
	case "aprovada":
		page.Success = "A ativação foi aprovada com os quatro comprovativos atuais."
	default:
		h.System.RequestRejected(w, r)
		return
	}
	for _, evidence := range snapshot.Evidence {
		page.Evidence = append(page.Evidence, pages.PrivacyActivationEvidence{ID: evidence.ID.String(), Kind: evidence.Kind, ObservedAt: privacyDate(evidence.ObservedAt)})
	}
	if snapshot.PendingProposal != nil {
		page.ProposedAt = privacyDate(snapshot.PendingProposal.ProposedAt)
	}
	h.render(w, r, status, pages.PrivacyActivationControl(page))
}

func (h PrivacyRequests) ProposeActivation(w http.ResponseWriter, r *http.Request) {
	h.activationAction(w, r, false)
}

func (h PrivacyRequests) ApproveActivation(w http.ResponseWriter, r *http.Request) {
	h.activationAction(w, r, true)
}

func (h PrivacyRequests) activationAction(w http.ResponseWriter, r *http.Request, approve bool) {
	privacyHeaders(w)
	if err := r.ParseForm(); err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	if r.PostForm.Get("confirmed") != "yes" {
		h.renderActivationControl(w, r, http.StatusUnprocessableEntity, "Confirme a revisão antes de continuar.")
		return
	}
	u, _ := CurrentUserFromContext(r.Context())
	snapshot, err := h.Service.ActivationControlSnapshot(r.Context(), u.ID)
	if err != nil {
		h.controlFailure(w, r, err)
		return
	}
	if approve {
		if !snapshot.CanApprove || snapshot.PendingProposal == nil {
			h.renderActivationControl(w, r, http.StatusUnprocessableEntity, "A ação já não está disponível. Atualize os comprovativos e volte a rever.")
			return
		}
		err = h.Service.ApproveActivation(r.Context(), u.ID, snapshot.PendingProposal.ID, snapshot.PendingProposal.Digest)
	} else {
		if (!snapshot.CanPropose && !snapshot.CanRenew) || snapshot.PendingProposal != nil || len(snapshot.Evidence) != 4 {
			h.renderActivationControl(w, r, http.StatusUnprocessableEntity, "A ação já não está disponível. Atualize os comprovativos e volte a rever.")
			return
		}
		evidenceIDs := make([]uuid.UUID, 0, len(snapshot.Evidence))
		for _, evidence := range snapshot.Evidence {
			evidenceIDs = append(evidenceIDs, evidence.ID)
		}
		_, err = h.Service.ProposeActivation(r.Context(), u.ID, snapshot.PolicyVersion, evidenceIDs)
	}
	if err != nil {
		if errors.Is(err, pr.ErrActivationUnavailable) || errors.Is(err, pr.ErrInvalid) {
			h.renderActivationControl(w, r, http.StatusUnprocessableEntity, "A ação já não está disponível. Atualize os comprovativos e volte a rever.")
			return
		}
		h.controlFailure(w, r, err)
		return
	}
	result := "proposta"
	if approve {
		result = "aprovada"
	}
	http.Redirect(w, r, "/admin/privacidade/ativacao?resultado="+result, http.StatusSeeOther)
}

func (h PrivacyRequests) controlFailure(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pr.ErrForbidden) || errors.Is(err, pr.ErrCompletionUnavailable) || errors.Is(err, pr.ErrActivationUnavailable) || errors.Is(err, pr.ErrInvalid) {
		h.System.NotFound(w, r)
		return
	}
	h.System.InternalError(w, r)
}

func (h PrivacyRequests) renderCompletionDetail(w http.ResponseWriter, r *http.Request, status int, page pages.PrivacyCompletionDetailPage) {
	completionDetailHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.PrivacyCompletionDetail(page).Render(r.Context(), w)
}
func (h PrivacyRequests) Index(w http.ResponseWriter, r *http.Request) {
	privacyHeaders(w)
	u, _ := CurrentUserFromContext(r.Context())
	management := privacyManagement(r)
	page := pages.PrivacyRequestsPage{Meta: h.meta(r), Management: management, MinorRights: u.IsDependent, ContactURL: h.ContactURL}
	if u.IsDependent && !management {
		h.render(w, r, 200, pages.PrivacyMinorRights(page))
		return
	}
	status, deadline, order := r.URL.Query().Get("status"), r.URL.Query().Get("deadline"), r.URL.Query().Get("order")
	if !oneOf(status, "", "RECEIVED", "UNDER_REVIEW", "AWAITING_EXECUTION", "PARTIALLY_APPROVED", "PROCESSING", "RETRYABLE_FAILED", "TERMINAL_FAILED", "COMPLETED", "REFUSED", "CANCELLED") || !oneOf(deadline, "", "overdue", "soon") || !oneOf(order, "", "due", "received") {
		h.System.RequestRejected(w, r)
		return
	}
	rows, e := h.Service.List(r.Context(), u.ID, management, status, deadline, order)
	if e != nil {
		h.failure(w, r, e)
		return
	}
	_, available := h.Service.Available(r.Context())
	page.CanSubmit = !management && available == nil
	page.StatusFilter = status
	page.DeadlineFilter = deadline
	page.Order = order
	page.StatusOptions = []pages.PrivacyOption{{Value: "", Label: "Todos"}, {Value: "RECEIVED", Label: "Recebido"}, {Value: "UNDER_REVIEW", Label: "Em análise"}, {Value: "AWAITING_EXECUTION", Label: "Aprovado — a aguardar execução"}, {Value: "PARTIALLY_APPROVED", Label: "Parcialmente aprovado"}, {Value: "PROCESSING", Label: "Em processamento"}, {Value: "RETRYABLE_FAILED", Label: "Nova tentativa pendente"}, {Value: "TERMINAL_FAILED", Label: "Intervenção necessária"}, {Value: "COMPLETED", Label: "Concluído"}, {Value: "REFUSED", Label: "Recusado"}, {Value: "CANCELLED", Label: "Cancelado"}}
	page.DeadlineOptions = []pages.PrivacyOption{{Value: "", Label: "Todos os prazos"}, {Value: "overdue", Label: "Prazo ultrapassado"}, {Value: "soon", Label: "Próximos 7 dias"}}
	page.OrderOptions = []pages.PrivacyOption{{Value: "due", Label: "Prazo de resposta"}, {Value: "received", Label: "Data de receção"}}
	base := "/perfil/privacidade/"
	if management {
		base = "/admin/privacidade/"
	}
	now := h.now()
	for _, row := range rows {
		due := row.DueAt.Time
		if row.ExtendedDueAt.Valid {
			due = row.ExtendedDueAt.Time
		}
		terminal := row.Status == "REFUSED" || row.Status == "CANCELLED" || row.Status == "COMPLETED"
		overdue := row.DueAt.Valid && !terminal && due.Before(now)
		soon := row.DueAt.Valid && !terminal && !overdue && due.Before(now.AddDate(0, 0, 7))
		if deadline == "overdue" && !overdue || deadline == "soon" && !soon {
			continue
		}
		warning := ""
		if overdue {
			warning = "Prazo ultrapassado"
		} else if soon {
			warning = "Prazo nos próximos 7 dias"
		}
		page.Items = append(page.Items, pages.PrivacyRequestItem{Reference: row.PublicRef.String(), ReceivedAt: privacyDate(row.ReceivedAt.Time), Status: row.Status, URL: base + row.PublicRef.String(), DueAt: optionalPrivacyDate(due), DeadlineWarning: warning})
	}
	h.render(w, r, 200, pages.PrivacyRequests(page))
}
func (h PrivacyRequests) newPage(r *http.Request) (pages.PrivacyRequestNewPage, error) {
	u, _ := CurrentUserFromContext(r.Context())
	page := pages.PrivacyRequestNewPage{Meta: h.meta(r), SubjectID: u.ID.String(), RequestKey: uuid.NewString(), ContactURL: h.ContactURL, Errors: validation.FieldErrors{}}
	p, e := h.Service.Available(r.Context())
	if e != nil {
		return page, e
	}
	subjects, e := h.Service.Subjects(r.Context(), u.ID)
	if e != nil {
		return page, e
	}
	page.PolicyVersion = p.Version
	page.AccountClosureEnabled = p.AccountClosureEnabled
	for _, s := range subjects {
		page.Subjects = append(page.Subjects, pages.PrivacyOption{Value: s.ID.String(), Label: s.Name})
	}
	for _, c := range p.Categories {
		page.Categories = append(page.Categories, pages.PrivacyCategory{Key: c.Key, Label: c.Label, Description: c.Description})
	}
	return page, nil
}
func (h PrivacyRequests) New(w http.ResponseWriter, r *http.Request) {
	privacyHeaders(w)
	page, e := h.newPage(r)
	if e != nil {
		h.failure(w, r, e)
		return
	}
	h.render(w, r, 200, pages.PrivacyRequestNew(page))
}
func (h PrivacyRequests) Submit(w http.ResponseWriter, r *http.Request) {
	privacyHeaders(w)
	if e := r.ParseForm(); e != nil {
		h.System.RequestRejected(w, r)
		return
	}
	page, e := h.newPage(r)
	if e != nil {
		h.failure(w, r, e)
		return
	}
	u, _ := CurrentUserFromContext(r.Context())
	subject, e1 := uuid.Parse(r.PostForm.Get("subject_id"))
	key, e2 := uuid.Parse(r.PostForm.Get("request_key"))
	page.SubjectID = r.PostForm.Get("subject_id")
	page.ScopeKind = r.PostForm.Get("scope_kind")
	page.RequestKey = r.PostForm.Get("request_key")
	scope := pr.Scope{Kind: pr.ScopeKind(page.ScopeKind)}
	for _, c := range r.PostForm["categories"] {
		scope.Categories = append(scope.Categories, pr.Category(c))
		for i := range page.Categories {
			if page.Categories[i].Key == c {
				page.Categories[i].Selected = true
			}
		}
	}
	if e1 != nil || e2 != nil {
		page.Errors["subject_id"] = "Selecione uma pessoa e volte a tentar."
		h.render(w, r, 422, pages.PrivacyRequestNew(page))
		return
	}
	ip := "unknown"
	if a, ok := httpx.RemoteIP(r.Context()); ok {
		ip = a.String()
	}
	version := int64(0)
	if h.Sessions != nil {
		version = h.Sessions.GetInt64(r.Context(), "credential_version")
	}
	record, e := h.Service.Submit(r.Context(), pr.SubmitInput{ActorID: u.ID, SubjectID: subject, RequestKey: key, CredentialVersion: version, Password: r.PostForm.Get("password"), IP: ip, PolicyVersion: r.PostForm.Get("policy_version"), Scope: scope})
	if e != nil {
		if errors.Is(e, pr.ErrRateLimited) {
			w.Header().Set("Retry-After", "900")
			page.Errors["password"] = "Aguarde 15 minutos antes de voltar a confirmar a palavra-passe."
			h.render(w, r, 429, pages.PrivacyRequestNew(page))
			return
		}
		field := "scope_kind"
		message := "Reveja o âmbito selecionado e tente novamente."
		if scope.Kind == pr.Categories && len(scope.Categories) == 0 {
			field = "categories"
			message = "Selecione pelo menos uma categoria."
		}
		if errors.Is(e, pr.ErrForbidden) {
			field = "password"
			message = "Não foi possível confirmar a autenticação ou a pessoa selecionada."
		}
		if errors.Is(e, pr.ErrDuplicate) {
			message = "Já existe um pedido ativo. Consulte os seus pedidos."
		}
		page.Errors[field] = message
		h.render(w, r, 422, pages.PrivacyRequestNew(page))
		return
	}
	http.Redirect(w, r, "/perfil/privacidade/"+record.PublicRef.String(), http.StatusSeeOther)
}
func (h PrivacyRequests) Detail(w http.ResponseWriter, r *http.Request) {
	privacyHeaders(w)
	h.detail(w, r, 200, nil)
}
func (h PrivacyRequests) detail(w http.ResponseWriter, r *http.Request, status int, actionErr error) {
	u, _ := CurrentUserFromContext(r.Context())
	ref, e := uuid.Parse(r.PathValue("ref"))
	if e != nil {
		h.System.NotFound(w, r)
		return
	}
	management := privacyManagement(r)
	v, e := h.Service.View(r.Context(), u.ID, ref, management)
	if e != nil {
		h.failure(w, r, e)
		return
	}
	rcr := v.Record
	due := rcr.DueAt.Time
	if rcr.ExtendedDueAt.Valid {
		due = rcr.ExtendedDueAt.Time
	}
	p := pages.PrivacyRequestDetailPage{Meta: h.meta(r), Reference: ref.String(), ReceivedAt: privacyDate(rcr.ReceivedAt.Time), Status: rcr.Status, Version: strconv.FormatInt(rcr.Version, 10), DueAt: optionalPrivacyDate(due), Management: management, SafeReceipt: v.SafeReceipt, CanCancel: v.CanCancel, ContactURL: h.ContactURL, Errors: validation.FieldErrors{}}
	if actionErr != nil {
		p.Errors["explanation"] = "Não foi possível guardar. Reveja os campos, a verificação e o estado atual do pedido."
		if errors.Is(actionErr, pr.ErrExecutorUnavailable) || errors.Is(actionErr, pr.ErrVerification) {
			delete(p.Errors, "explanation")
			p.Errors["execution_confirmed"] = "A execução não pode começar: confirme o plano e resolva todos os bloqueios apresentados."
		}
		if errors.Is(actionErr, pr.ErrStaleVersion) {
			p.Conflict = "O pedido foi atualizado por outra pessoa. Reveja a versão atual antes de tentar novamente."
		}
		if errors.Is(actionErr, pr.ErrClosureSafeguards) {
			p.Errors["explanation"] = "Resolva os dependentes e confirme a continuidade da administração antes de continuar."
		}
	}
	if !v.SafeReceipt {
		p.SubjectName = v.Subject.Name
		p.RequesterName = v.Requester.Name
		p.Representative = v.Subject.ID != v.Requester.ID
		p.PolicyVersion = v.Policy.Version
		p.ScopeLabel = "Categorias de dados"
		if rcr.ScopeKind == string(pr.AccountClosure) {
			p.ScopeLabel = "Encerramento da conta"
		}
		p.Explanation = rcr.DecisionExplanation
		p.IdentityVerified = rcr.IdentityVerifiedAt.Valid
		p.RepresentationVerified = rcr.RepresentationVerifiedAt.Valid && rcr.RepresentationRelationshipUpdatedAt.Time.Equal(v.Subject.UpdatedAt.Time)
		p.ConflictFlag = rcr.RepresentationConflict
		p.IdentityMethod = stringValue(rcr.IdentityMethod)
		p.RepresentationMethod = stringValue(rcr.RepresentationMethod)
		open := rcr.Status == "RECEIVED" || rcr.Status == "UNDER_REVIEW"
		claimedByCurrentReviewer := rcr.ClaimedBy != nil && *rcr.ClaimedBy == u.ID
		p.CanClaim = management && v.CanReview && open && rcr.ClaimedBy == nil
		p.CanVerify = management && v.CanReview && open && claimedByCurrentReviewer
		p.CanDecide = p.CanVerify && p.IdentityVerified && !p.ConflictFlag && (!p.Representative || p.RepresentationVerified)
		p.CanExtend = management && v.CanReview && open && claimedByCurrentReviewer && !rcr.ExtendedDueAt.Valid && !h.now().After(rcr.DueAt.Time)
		p.IdentityMethods = []pages.PrivacyOption{{Value: "IN_PERSON", Label: "Presencial"}, {Value: "EXISTING_CHANNEL", Label: "Canal já verificado"}, {Value: "DOCUMENT_CHECK", Label: "Verificação documental"}}
		p.RepresentationMethods = []pages.PrivacyOption{{Value: "IN_PERSON", Label: "Presencial"}, {Value: "DOCUMENT_CHECK", Label: "Verificação documental"}}
		p.ResolutionOptions = []pages.PrivacyOption{{Value: "SEPARATE_APPROVED_REQUEST", Label: "Pedido separado aprovado (deve estar em processamento antes da execução)"}}
		var savedDecisions []pr.CategoryDecision
		if len(rcr.CategoryDecisions) > 0 {
			if err := json.Unmarshal(rcr.CategoryDecisions, &savedDecisions); err != nil {
				h.failure(w, r, err)
				return
			}
		}
		for _, c := range v.Policy.Categories {
			selected := rcr.ScopeKind == string(pr.AccountClosure)
			for _, k := range rcr.Categories {
				selected = selected || k == c.Key
			}
			if !selected {
				continue
			}
			pc := pages.PrivacyCategory{Key: c.Key, Label: c.Label, Description: c.Description}
			for _, decision := range savedDecisions {
				if decision.Category == c.Key {
					pc.Outcome, pc.Ground = decision.Outcome, decision.Ground
					break
				}
			}
			for _, g := range c.Grounds {
				pc.Grounds = append(pc.Grounds, pages.PrivacyOption{Value: g.Code, Label: g.Label})
			}
			p.Categories = append(p.Categories, pc)
		}
		for _, d := range v.Dependants {
			pd := pages.PrivacyDependant{ID: d.ID.String(), Name: d.Name, CanResolve: p.CanVerify}
			for _, res := range v.Resolutions {
				if res.DependantID == d.ID && res.RelationshipUpdatedAt.Time.Equal(d.UpdatedAt.Time) {
					pd.Resolution = "Resolução verificada"
					pd.ResolvedAt = privacyDate(res.VerifiedAt.Time)
				}
			}
			p.Dependants = append(p.Dependants, pd)
		}
		for _, event := range v.Events {
			label := privacyEventLabel(event.Action)
			p.History = append(p.History, pages.PrivacyHistoryItem{At: privacyDate(event.OccurredAt.Time), Label: label})
		}
		p.CanViewExecution = management && v.CanViewExecution
		if v.Plan != nil && p.CanViewExecution {
			labels := map[string]string{}
			for _, category := range v.Policy.Categories {
				labels[category.Key] = category.Label
			}
			for _, entry := range v.Plan.Entries {
				label := labels[entry.Category]
				if label == "" {
					label = entry.Category
				}
				p.ExecutionPlan = append(p.ExecutionPlan, pages.PrivacyExecutionPlanItem{Category: entry.Category, CategoryLabel: label, Disposition: entry.Disposition, Owner: entry.Owner, DueAt: entry.DueAt, Operations: entry.Operations})
			}
		}
		if v.Execution != nil && p.CanViewExecution {
			p.ExecutionStatus = v.Execution.Status
		}
		for _, blocker := range v.ExecutionBlockers {
			if !p.CanViewExecution {
				break
			}
			p.ExecutionBlockers = append(p.ExecutionBlockers, privacyExecutionBlocker(blocker))
		}
		p.CanExecute = management && v.CanExecute && v.Execution == nil && (rcr.Status == "AWAITING_EXECUTION" || rcr.Status == "PARTIALLY_APPROVED")
	}
	h.render(w, r, status, pages.PrivacyRequestDetail(p))
}

func privacyExecutionBlocker(code string) string {
	switch code {
	case "EXECUTOR_AUTHORITY_OR_SEPARATION":
		return "É necessária uma pessoa executora autorizada e diferente do requerente, da pessoa afetada e de quem decidiu."
	case "IDENTITY_CHANGED":
		return "A verificação de identidade deixou de corresponder aos dados atuais."
	case "RELATIONSHIP_CHANGED":
		return "A relação atual entre requerente e pessoa afetada precisa de nova validação."
	case "REPRESENTATION_CHANGED":
		return "A representação está incompleta, em conflito ou deixou de corresponder à relação atual."
	case "DECISION_AUTHORITY":
		return "A autoridade histórica de quem decidiu não pôde ser confirmada."
	case "CAPABILITIES_UNAVAILABLE":
		return "Ainda não estão instaladas todas as operações exigidas pelo plano imutável."
	case "ACTIVATION_DISABLED":
		return "A execução permanece desativada até à validação operacional final."
	case "ADMIN_CONTINUITY":
		return "O encerramento removeria a última pessoa administradora adulta e utilizável."
	case "LEGACY_SESSIONS":
		return "Ainda existem sessões antigas sem índice de pessoa; têm de expirar ou ser migradas."
	case "DEPENDANTS_UNRESOLVED":
		return "Há dependentes sem transferência verificada ou sem encerramento separado já em processamento."
	default:
		return "Existe um bloqueio de segurança que tem de ser resolvido antes do processamento."
	}
}

func (h PrivacyRequests) StartExecution(w http.ResponseWriter, r *http.Request) {
	privacyHeaders(w)
	if e := r.ParseForm(); e != nil {
		h.System.RequestRejected(w, r)
		return
	}
	u, _ := CurrentUserFromContext(r.Context())
	ref, refErr := uuid.Parse(r.PathValue("ref"))
	version, versionErr := strconv.ParseInt(r.PostForm.Get("version"), 10, 64)
	if refErr != nil || versionErr != nil || version < 1 {
		h.System.RequestRejected(w, r)
		return
	}
	_, e := h.Service.StartExecution(r.Context(), pr.StartInput{ActorID: u.ID, Reference: ref, Version: version, Confirmed: r.PostForm.Get("execution_confirmed") == "yes"})
	if e != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(e, pr.ErrStaleVersion) {
			status = http.StatusConflict
		}
		if errors.Is(e, pr.ErrForbidden) {
			h.failure(w, r, e)
			return
		}
		h.detail(w, r, status, errors.Join(e, pr.ErrExecutorUnavailable))
		return
	}
	http.Redirect(w, r, "/admin/privacidade/"+ref.String(), http.StatusSeeOther)
}
func (h PrivacyRequests) Change(w http.ResponseWriter, r *http.Request) {
	privacyHeaders(w)
	if e := r.ParseForm(); e != nil {
		h.System.RequestRejected(w, r)
		return
	}
	u, _ := CurrentUserFromContext(r.Context())
	ref, e := uuid.Parse(r.PathValue("ref"))
	version, ev := strconv.ParseInt(r.PostForm.Get("version"), 10, 64)
	if e != nil || ev != nil || version < 1 {
		h.System.RequestRejected(w, r)
		return
	}
	action := r.PostForm.Get("action")
	if !privacyManagement(r) {
		action = "cancel"
	}
	in := pr.ReviewInput{ActorID: u.ID, Reference: ref, Version: version, Action: action, PolicyVersion: r.PostForm.Get("policy_version"), IdentityMethod: r.PostForm.Get("identity_method"), RepresentationMethod: r.PostForm.Get("representation_method"), IdentityVerified: r.PostForm.Get("identity_verified") == "yes", RepresentationVerified: r.PostForm.Get("representation_verified") == "yes", Conflict: r.PostForm.Get("conflict") == "yes", Explanation: r.PostForm.Get("explanation"), ExtensionReason: r.PostForm.Get("extension_reason"), ResolutionCode: r.PostForm.Get("resolution_code"), ResolutionExplanation: r.PostForm.Get("resolution_explanation"), Decisions: map[string]pr.CategoryDecision{}}
	in.ExtensionMonths, _ = strconv.Atoi(r.PostForm.Get("extension_months"))
	in.DependantID, _ = uuid.Parse(r.PostForm.Get("dependant_id"))
	in.RelatedReference, _ = uuid.Parse(r.PostForm.Get("related_ref"))
	for key, values := range r.PostForm {
		if strings.HasPrefix(key, "outcome_") && len(values) == 1 {
			category := strings.TrimPrefix(key, "outcome_")
			in.Decisions[category] = pr.CategoryDecision{Category: category, Outcome: values[0], Ground: r.PostForm.Get("ground_" + category)}
		}
	}
	_, e = h.Service.Change(r.Context(), in)
	if e != nil {
		status := 422
		if errors.Is(e, pr.ErrStaleVersion) {
			status = 409
		}
		if errors.Is(e, pr.ErrForbidden) {
			h.failure(w, r, e)
			return
		}
		h.detail(w, r, status, e)
		return
	}
	base := "/perfil/privacidade/"
	if privacyManagement(r) {
		base = "/admin/privacidade/"
	}
	http.Redirect(w, r, base+ref.String(), http.StatusSeeOther)
}
func (h PrivacyRequests) failure(w http.ResponseWriter, r *http.Request, e error) {
	privacyHeaders(w)
	if errors.Is(e, pr.ErrForbidden) || errors.Is(e, pr.ErrPolicyUnresolved) {
		h.System.NotFound(w, r)
		return
	}
	h.System.InternalError(w, r)
}
func oneOf(v string, allowed ...string) bool {
	for _, x := range allowed {
		if v == x {
			return true
		}
	}
	return false
}
func privacyEventLabel(action string) string {
	m := map[string]string{"RECEIVED": "Pedido recebido", "CLAIMED": "Análise atribuída", "IDENTITY_REQUESTED": "Verificação solicitada", "IDENTITY_VERIFIED": "Verificação registada", "REPRESENTATION_VERIFIED": "Representação verificada", "REPRESENTATION_CONFLICT": "Conflito de representação", "DEADLINE_EXTENDED": "Prazo prorrogado", "DEPENDANT_RESOLVED": "Resolução de dependente registada", "APPROVED": "Aprovado — a aguardar execução", "PARTIALLY_APPROVED": "Parcialmente aprovado — a aguardar execução", "PROCESSING_STARTED": "Processamento iniciado", "EXECUTION_RETRY_STARTED": "Nova tentativa iniciada", "EXECUTION_RETRYABLE_FAILED": "Execução interrompida; nova tentativa pendente", "EXECUTION_TERMINAL_FAILED": "Execução bloqueada; intervenção necessária", "REFUSED": "Pedido recusado", "CANCELLED": "Pedido cancelado"}
	if s := m[action]; s != "" {
		return s
	}
	return "Pedido atualizado"
}

func optionalPrivacyDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return privacyDate(t)
}
