package handlers

import (
	"context"
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
}
type PrivacyRequests struct {
	Service    PrivacyRequestStore
	Sessions   *scs.SessionManager
	System     System
	PageMeta   components.PageMeta
	ContactURL string
	Now        func() time.Time
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
	if !oneOf(status, "", "RECEIVED", "UNDER_REVIEW", "AWAITING_EXECUTION", "PARTIALLY_APPROVED", "REFUSED", "CANCELLED") || !oneOf(deadline, "", "overdue", "soon") || !oneOf(order, "", "due", "received") {
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
	page.StatusOptions = []pages.PrivacyOption{{Value: "", Label: "Todos"}, {Value: "RECEIVED", Label: "Recebido"}, {Value: "UNDER_REVIEW", Label: "Em análise"}, {Value: "AWAITING_EXECUTION", Label: "Aprovado — a aguardar execução"}, {Value: "PARTIALLY_APPROVED", Label: "Parcialmente aprovado"}, {Value: "REFUSED", Label: "Recusado"}, {Value: "CANCELLED", Label: "Cancelado"}}
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
		terminal := row.Status == "REFUSED" || row.Status == "CANCELLED"
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
		if errors.Is(actionErr, pr.ErrStaleVersion) {
			p.Conflict = "O pedido foi atualizado por outra pessoa. Reveja a versão atual antes de tentar novamente."
		}
		if errors.Is(actionErr, pr.ErrClosureSafeguards) {
			p.Errors["explanation"] = "Resolva os dependentes e confirme a continuidade da administração antes de aprovar."
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
		p.CanClaim = management && open && rcr.ClaimedBy == nil
		p.CanVerify = management && open && claimedByCurrentReviewer
		p.CanDecide = p.CanVerify && p.IdentityVerified && !p.ConflictFlag && (!p.Representative || p.RepresentationVerified)
		p.CanExtend = management && open && claimedByCurrentReviewer && !rcr.ExtendedDueAt.Valid && !h.now().After(rcr.DueAt.Time)
		p.IdentityMethods = []pages.PrivacyOption{{Value: "IN_PERSON", Label: "Presencial"}, {Value: "EXISTING_CHANNEL", Label: "Canal já verificado"}, {Value: "DOCUMENT_CHECK", Label: "Verificação documental"}}
		p.RepresentationMethods = []pages.PrivacyOption{{Value: "IN_PERSON", Label: "Presencial"}, {Value: "DOCUMENT_CHECK", Label: "Verificação documental"}}
		p.ResolutionOptions = []pages.PrivacyOption{{Value: "SEPARATE_APPROVED_REQUEST", Label: "Pedido separado aprovado"}, {Value: "FORMAL_RESOLUTION", Label: "Resolução formal verificada"}}
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
	}
	h.render(w, r, status, pages.PrivacyRequestDetail(p))
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
	m := map[string]string{"RECEIVED": "Pedido recebido", "CLAIMED": "Análise atribuída", "IDENTITY_REQUESTED": "Verificação solicitada", "IDENTITY_VERIFIED": "Verificação registada", "REPRESENTATION_VERIFIED": "Representação verificada", "REPRESENTATION_CONFLICT": "Conflito de representação", "DEADLINE_EXTENDED": "Prazo prorrogado", "DEPENDANT_RESOLVED": "Resolução de dependente registada", "APPROVED": "Aprovado — a aguardar execução", "PARTIALLY_APPROVED": "Parcialmente aprovado — a aguardar execução", "REFUSED": "Pedido recusado", "CANCELLED": "Pedido cancelado"}
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
