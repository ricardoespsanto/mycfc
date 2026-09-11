package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	pr "github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type privacyHandlerStore struct {
	PrivacyRequestStore
	policy                               pr.AdoptedPolicy
	availableErr, subjectsErr, listErr   error
	subjects                             []dbgen.User
	list                                 []dbgen.DataErasureRequest
	listManagement                       bool
	listStatus, listDeadline, listOrder  string
	view                                 pr.View
	viewErr                              error
	submitResult                         dbgen.DataErasureRequest
	submitErr                            error
	submitInput                          pr.SubmitInput
	changeErr                            error
	changeInput                          pr.ReviewInput
	startResult                          dbgen.PrivacyErasureExecution
	startErr                             error
	startInput                           pr.StartInput
	completionResult                     pr.CompletionDetail
	completionErr                        error
	completionToken                      string
	completionValidateErr                error
	completionValidatedToken             string
	controlSnapshot                      pr.CompletionControlSnapshot
	controlErr                           error
	controlActor, controlReference       uuid.UUID
	requeueProposal                      pr.TerminalRequeueProposal
	requeueProposeErr, requeueApproveErr error
	requeueActor, requeueJob             uuid.UUID
	requeueApprovalActor                 uuid.UUID
	requeueApprovalProposal              uuid.UUID
	requeueApprovalDigest                []byte
	activationSnapshot                   pr.ActivationControlSnapshot
	activationErr                        error
	activationActor                      uuid.UUID
	activationProposal                   pr.ActivationProposal
	activationProposeErr                 error
	activationPolicy                     string
	activationEvidence                   []uuid.UUID
	activationApproveErr                 error
	activationApprovalActor              uuid.UUID
	activationApprovalProposal           uuid.UUID
	activationApprovalDigest             []byte
}

func (s *privacyHandlerStore) Available(context.Context) (pr.AdoptedPolicy, error) {
	return s.policy, s.availableErr
}
func (s *privacyHandlerStore) Subjects(context.Context, uuid.UUID) ([]dbgen.User, error) {
	return s.subjects, s.subjectsErr
}
func (s *privacyHandlerStore) List(_ context.Context, _ uuid.UUID, management bool, status, deadline, order string) ([]dbgen.DataErasureRequest, error) {
	s.listManagement, s.listStatus, s.listDeadline, s.listOrder = management, status, deadline, order
	return s.list, s.listErr
}
func (s *privacyHandlerStore) View(context.Context, uuid.UUID, uuid.UUID, bool) (pr.View, error) {
	return s.view, s.viewErr
}
func (s *privacyHandlerStore) Submit(_ context.Context, in pr.SubmitInput) (dbgen.DataErasureRequest, error) {
	s.submitInput = in
	return s.submitResult, s.submitErr
}
func (s *privacyHandlerStore) Change(_ context.Context, in pr.ReviewInput) (dbgen.DataErasureRequest, error) {
	s.changeInput = in
	return s.view.Record, s.changeErr
}
func (s *privacyHandlerStore) StartExecution(_ context.Context, in pr.StartInput) (dbgen.PrivacyErasureExecution, error) {
	s.startInput = in
	return s.startResult, s.startErr
}
func (s *privacyHandlerStore) ConsumeCompletionDetail(_ context.Context, token string) (pr.CompletionDetail, error) {
	s.completionToken = token
	return s.completionResult, s.completionErr
}
func (s *privacyHandlerStore) ValidateCompletionLink(_ context.Context, token string) error {
	s.completionValidatedToken = token
	return s.completionValidateErr
}
func (s *privacyHandlerStore) CompletionControlSnapshot(_ context.Context, actor, reference uuid.UUID) (pr.CompletionControlSnapshot, error) {
	s.controlActor, s.controlReference = actor, reference
	return s.controlSnapshot, s.controlErr
}
func (s *privacyHandlerStore) ProposeTerminalRequeue(_ context.Context, actor, job uuid.UUID) (pr.TerminalRequeueProposal, error) {
	s.requeueActor, s.requeueJob = actor, job
	return s.requeueProposal, s.requeueProposeErr
}
func (s *privacyHandlerStore) ApproveTerminalRequeue(_ context.Context, actor, proposal uuid.UUID, digest []byte) error {
	s.requeueApprovalActor, s.requeueApprovalProposal, s.requeueApprovalDigest = actor, proposal, append([]byte(nil), digest...)
	return s.requeueApproveErr
}
func (s *privacyHandlerStore) ActivationControlSnapshot(_ context.Context, actor uuid.UUID) (pr.ActivationControlSnapshot, error) {
	s.activationActor = actor
	return s.activationSnapshot, s.activationErr
}
func (s *privacyHandlerStore) ProposeActivation(_ context.Context, actor uuid.UUID, policy string, evidence []uuid.UUID) (pr.ActivationProposal, error) {
	s.activationActor, s.activationPolicy, s.activationEvidence = actor, policy, append([]uuid.UUID(nil), evidence...)
	return s.activationProposal, s.activationProposeErr
}
func (s *privacyHandlerStore) ApproveActivation(_ context.Context, actor, proposal uuid.UUID, digest []byte) error {
	s.activationApprovalActor, s.activationApprovalProposal, s.activationApprovalDigest = actor, proposal, append([]byte(nil), digest...)
	return s.activationApproveErr
}
func privacyHandlerRequest(method, path string, form url.Values) *http.Request {
	return privacyHandlerRequestFor(method, path, form, CurrentUser{ID: uuid.New(), Name: "Current person"})
}
func privacyHandlerRequestFor(method, path string, form url.Values, user CurrentUser) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("ref", "11000000-0000-0000-0000-000000000001")
	return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, user))
}
func privacyHandlerFixture(t *testing.T) *privacyHandlerStore {
	t.Helper()
	decisions, err := json.Marshal([]pr.CategoryDecision{{Category: "alpha", Outcome: "RETAIN", Ground: "hold"}})
	if err != nil {
		t.Fatal(err)
	}
	policy := pr.AdoptedPolicy{Version: "private-policy", AccountClosureEnabled: true, Categories: []pr.CatalogueEntry{{Key: "alpha", Label: "Protected category", Description: "Protected description", Grounds: []pr.Ground{{Code: "hold", Label: "Approved preservation ground"}}}}}
	return &privacyHandlerStore{policy: policy, subjects: []dbgen.User{{ID: uuid.New(), Name: "Current person"}}, view: pr.View{
		Record:  dbgen.DataErasureRequest{PublicRef: uuid.MustParse("11000000-0000-0000-0000-000000000001"), Version: 7, Status: "REFUSED", ScopeKind: "CATEGORIES", Categories: []string{"alpha"}, CategoryDecisions: decisions, DecisionExplanation: "Protected explanation", ReceivedAt: pgtype.Timestamptz{Time: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), Valid: true}},
		Subject: dbgen.User{ID: uuid.New(), Name: "Protected subject"}, Requester: dbgen.User{ID: uuid.New(), Name: "Protected requester"},
		Policy: policy, CanReview: true,
	}}
}

type privacyBrokenBody struct{}

func (privacyBrokenBody) Read([]byte) (int, error) { return 0, errors.New("broken request body") }

func TestPrivacyIndexStatesFiltersAndFailures(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	overdueRef, soonRef, terminalRef := uuid.New(), uuid.New(), uuid.New()
	rows := []dbgen.DataErasureRequest{
		{PublicRef: overdueRef, Status: "UNDER_REVIEW", ReceivedAt: privacyTestStamp(now.AddDate(0, 0, -10)), DueAt: privacyTestStamp(now.Add(-time.Hour))},
		{PublicRef: soonRef, Status: "RECEIVED", ReceivedAt: privacyTestStamp(now.AddDate(0, 0, -2)), DueAt: privacyTestStamp(now.AddDate(0, 0, 20)), ExtendedDueAt: privacyTestStamp(now.AddDate(0, 0, 2))},
		{PublicRef: terminalRef, Status: "REFUSED", ReceivedAt: privacyTestStamp(now.AddDate(0, 0, -20)), DueAt: privacyTestStamp(now.AddDate(0, 0, -10))},
	}
	t.Run("management deadline views use extended deadline and terminal safety", func(t *testing.T) {
		for _, tc := range []struct {
			name, deadline, wantRef, warning string
		}{
			{"overdue", "overdue", overdueRef.String(), "Prazo ultrapassado"},
			{"soon", "soon", soonRef.String(), "Prazo nos próximos 7 dias"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s := privacyHandlerFixture(t)
				s.list = rows
				w := httptest.NewRecorder()
				r := privacyHandlerRequest(http.MethodGet, "/admin/privacidade?status=UNDER_REVIEW&deadline="+tc.deadline+"&order=received", nil)
				PrivacyRequests{Service: s, Now: func() time.Time { return now }}.Index(w, r)
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.wantRef) || !strings.Contains(w.Body.String(), tc.warning) || strings.Contains(w.Body.String(), terminalRef.String()) {
					t.Fatalf("unexpected filtered queue status=%d body=%s", w.Code, w.Body.String())
				}
				if !s.listManagement || s.listStatus != "UNDER_REVIEW" || s.listDeadline != tc.deadline || s.listOrder != "received" {
					t.Fatalf("filters not forwarded: management=%v status=%q deadline=%q order=%q", s.listManagement, s.listStatus, s.listDeadline, s.listOrder)
				}
			})
		}
	})
	t.Run("member can start available request", func(t *testing.T) {
		s := privacyHandlerFixture(t)
		s.list = rows[2:]
		w := httptest.NewRecorder()
		PrivacyRequests{Service: s}.Index(w, privacyHandlerRequest(http.MethodGet, "/perfil/privacidade", nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Novo pedido") || !strings.Contains(w.Body.String(), "/perfil/privacidade/"+terminalRef.String()) || s.listManagement {
			t.Fatalf("member index status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("minor sees rights without loading case data", func(t *testing.T) {
		w := httptest.NewRecorder()
		u := CurrentUser{ID: uuid.New(), Name: "Young person", IsDependent: true}
		PrivacyRequests{Service: &privacyHandlerStore{}}.Index(w, privacyHandlerRequestFor(http.MethodGet, "/perfil/privacidade", nil, u))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Os teus direitos") {
			t.Fatalf("minor page status=%d body=%s", w.Code, w.Body.String())
		}
	})
	for _, tc := range []struct {
		name, query string
	}{
		{"bad status", "status=UNKNOWN"}, {"bad deadline", "deadline=later"}, {"bad order", "order=newest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			PrivacyRequests{Service: privacyHandlerFixture(t)}.Index(w, privacyHandlerRequest(http.MethodGet, "/admin/privacidade?"+tc.query, nil))
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Pedido recusado") {
				t.Fatalf("invalid filter status=%d", w.Code)
			}
		})
	}
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"hidden policy failure", pr.ErrPolicyUnresolved, http.StatusNotFound},
		{"internal list failure", errors.New("list unavailable"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := privacyHandlerFixture(t)
			s.listErr = tc.err
			w := httptest.NewRecorder()
			PrivacyRequests{Service: s}.Index(w, privacyHandlerRequest(http.MethodGet, "/perfil/privacidade", nil))
			if w.Code != tc.want {
				t.Fatalf("failure status=%d want=%d", w.Code, tc.want)
			}
		})
	}
}

func TestPrivacyCompletionDetailIsSessionIndependentOneUseAndPrivate(t *testing.T) {
	completedAt := time.Date(2026, 9, 10, 18, 30, 0, 0, time.UTC)
	manifest := strings.Repeat("a", 64)
	s := privacyHandlerFixture(t)
	s.completionResult = pr.CompletionDetail{
		RequestReference: uuid.MustParse("11000000-0000-0000-0000-000000000099"),
		CompletedAt:      completedAt, Status: "COMPLETED", ManifestSHA256: manifest,
		Categories: 3, Checkpoints: 8, ObjectTargets: 2, ProviderTargets: 1,
	}
	token := "never-render-or-log-this-token"
	r := httptest.NewRequest(http.MethodGet, "/privacidade/conclusao/"+token, nil)
	r.SetPathValue("token", token)
	w := httptest.NewRecorder()
	h := PrivacyRequests{Service: s, ContactURL: "/legal/direitos", CompletionLinkKey: bytes.Repeat([]byte{7}, 32), CompletionStateRandom: bytes.NewReader(bytes.Repeat([]byte{8}, 32)), SecureCookies: true, Now: func() time.Time { return completedAt }}
	h.CompletionDetail(w, r)
	if w.Code != http.StatusOK || s.completionValidatedToken != token || s.completionToken != "" {
		t.Fatalf("prefetch status=%d validated=%q consumed=%q", w.Code, s.completionValidatedToken, s.completionToken)
	}
	for _, want := range []string{"Consultar resultado", "Consultar resultado agora"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("prefetch page missing %q", want)
		}
	}
	for _, forbidden := range []string{token, "11000000-0000-0000-0000-000000000099", manifest} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatalf("prefetch page rendered protected value %q", forbidden)
		}
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("X-Robots-Tag") != "noindex, nofollow, noarchive" {
		t.Fatalf("completion privacy headers=%v", w.Header())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value == "" || strings.Contains(cookies[0].Value, token) || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("completion state cookie=%+v", cookies)
	}
	_, confirmationNonce, stateErr := h.openCompletionState(cookies[0].Value)
	if stateErr != nil || confirmationNonce == "" || !strings.Contains(w.Body.String(), `name="completion_state" value="`+confirmationNonce+`"`) {
		t.Fatalf("completion confirmation binding nonce=%q err=%v body=%s", confirmationNonce, stateErr, w.Body.String())
	}

	post := httptest.NewRequest(http.MethodPost, "/privacidade/conclusao/consultar", strings.NewReader(url.Values{"completion_state": {confirmationNonce}}.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	h.ConsumeCompletionDetail(w, post)
	if w.Code != http.StatusOK || s.completionToken != token {
		t.Fatalf("explicit consume status=%d token=%q", w.Code, s.completionToken)
	}
	for _, want := range []string{"Pedido concluído", "11000000-0000-0000-0000-000000000099", "3", "8", manifest} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("completion page missing %q", want)
		}
	}
	if strings.Contains(w.Body.String(), token) {
		t.Fatal("completion page rendered its bearer token")
	}

	// A second POST has no browser state and must not call the core consumer.
	s.completionToken = ""
	w = httptest.NewRecorder()
	h.ConsumeCompletionDetail(w, httptest.NewRequest(http.MethodPost, "/privacidade/conclusao/consultar", nil))
	if w.Code != http.StatusNotFound || s.completionToken != "" || !strings.Contains(w.Body.String(), "inválida, já foi utilizada ou expirou") {
		t.Fatalf("replay status=%d token=%q body=%s", w.Code, s.completionToken, w.Body.String())
	}

	post = httptest.NewRequest(http.MethodPost, "/privacidade/conclusao/consultar", strings.NewReader("completion_state=wrong-browser-state"))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	h.ConsumeCompletionDetail(w, post)
	if w.Code != http.StatusNotFound {
		t.Fatalf("mismatched completion state status=%d", w.Code)
	}

	for _, unavailable := range []error{pr.ErrCompletionLinkUnavailable, errors.Join(pr.ErrCompletionLinkUnavailable, errors.New("opaque"))} {
		s.completionValidateErr = unavailable
		w = httptest.NewRecorder()
		h.CompletionDetail(w, r)
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "inválida, já foi utilizada ou expirou") || strings.Contains(w.Body.String(), token) {
			t.Fatalf("unavailable completion status=%d body=%s", w.Code, w.Body.String())
		}
	}

	s.completionValidateErr = errors.New("database unavailable")
	w = httptest.NewRecorder()
	h.CompletionDetail(w, r)
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), token) {
		t.Fatalf("internal completion status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestPrivacyCompletionControlUsesSafeSnapshotAndServerBoundDualControl(t *testing.T) {
	actor, reference, jobID, proposalID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	digest := bytes.Repeat([]byte{9}, 32)
	s := privacyHandlerFixture(t)
	s.controlSnapshot = pr.CompletionControlSnapshot{
		RequestReference: reference, RequestStatus: "TERMINAL_FAILED", ExecutionID: uuid.New(), ExecutionStatus: "TERMINAL_FAILED",
		Jobs: []pr.CompletionControlJob{{JobID: jobID, CategoryCode: "identity-core", PurposeCode: "ACCOUNT_ERASURE", Status: "TERMINAL_FAILED", AttemptCount: 5, FailureStage: "VERIFY", FailureCode: "VERIFICATION_FAILED", CanProposeRequeue: true}},
	}
	h := PrivacyRequests{Service: s}
	r := privacyHandlerRequestFor(http.MethodGet, "/admin/privacidade/controlo/"+reference.String(), nil, CurrentUser{ID: actor, CanExecutePrivacy: true})
	r.SetPathValue("ref", reference.String())
	w := httptest.NewRecorder()
	h.CompletionControl(w, r)
	for _, want := range []string{"Estado técnico limitado", reference.String(), "identity-core", "VERIFICATION_FAILED", "Propor nova tentativa"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("control page missing %q", want)
		}
	}
	for _, forbidden := range []string{"protected subject", "recipient@example", "raw database error"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Errorf("control page exposed %q", forbidden)
		}
	}

	post := privacyHandlerRequestFor(http.MethodPost, "/admin/privacidade/controlo/"+reference.String()+"/reagendar", url.Values{"job_id": {jobID.String()}, "confirmed": {"yes"}}, CurrentUser{ID: actor, CanExecutePrivacy: true})
	post.SetPathValue("ref", reference.String())
	w = httptest.NewRecorder()
	h.ProposeTerminalRequeue(w, post)
	if w.Code != http.StatusSeeOther || s.requeueActor != actor || s.requeueJob != jobID || !strings.Contains(w.Header().Get("Location"), "resultado=proposta") {
		t.Fatalf("requeue proposal status=%d actor=%s job=%s location=%q", w.Code, s.requeueActor, s.requeueJob, w.Header().Get("Location"))
	}

	s.controlSnapshot.Jobs[0].CanProposeRequeue = false
	s.controlSnapshot.Jobs[0].CanApproveRequeue = true
	s.controlSnapshot.Jobs[0].PendingRequeueProposal = &pr.ControlProposal{ID: proposalID, Digest: digest, ProposedAt: time.Now()}
	post = privacyHandlerRequestFor(http.MethodPost, "/admin/privacidade/controlo/"+reference.String()+"/reagendar/aprovar", url.Values{"job_id": {jobID.String()}, "confirmed": {"yes"}}, CurrentUser{ID: actor, IsAdmin: true})
	post.SetPathValue("ref", reference.String())
	w = httptest.NewRecorder()
	h.ApproveTerminalRequeue(w, post)
	if w.Code != http.StatusSeeOther || s.requeueApprovalActor != actor || s.requeueApprovalProposal != proposalID || !bytes.Equal(s.requeueApprovalDigest, digest) || !strings.Contains(w.Header().Get("Location"), "resultado=aprovada") {
		t.Fatalf("requeue approval status=%d actor=%s proposal=%s digest=%x location=%q", w.Code, s.requeueApprovalActor, s.requeueApprovalProposal, s.requeueApprovalDigest, w.Header().Get("Location"))
	}

	post = privacyHandlerRequestFor(http.MethodPost, "/admin/privacidade/controlo/"+reference.String()+"/reagendar/aprovar", url.Values{"job_id": {jobID.String()}}, CurrentUser{ID: actor, IsAdmin: true})
	post.SetPathValue("ref", reference.String())
	w = httptest.NewRecorder()
	h.ApproveTerminalRequeue(w, post)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Confirme a revisão") {
		t.Fatalf("unconfirmed approval status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestPrivacyControlLookupAndActivationUseCurrentServerSnapshot(t *testing.T) {
	actor, reference := uuid.New(), uuid.New()
	s := privacyHandlerFixture(t)
	h := PrivacyRequests{Service: s}

	r := privacyHandlerRequestFor(http.MethodGet, "/admin/privacidade/controlo?ref="+reference.String(), nil, CurrentUser{ID: actor, IsAdmin: true})
	w := httptest.NewRecorder()
	h.ControlLookup(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/privacidade/controlo/"+reference.String() {
		t.Fatalf("control lookup status=%d location=%q", w.Code, w.Header().Get("Location"))
	}
	r = privacyHandlerRequestFor(http.MethodGet, "/admin/privacidade/controlo", nil, CurrentUser{ID: actor})
	w = httptest.NewRecorder()
	h.ControlLookup(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unauthorized lookup status=%d", w.Code)
	}

	evidence := []pr.ActivationEvidenceSummary{
		{ID: uuid.New(), Kind: "RESTORE", ObservedAt: time.Now()}, {ID: uuid.New(), Kind: "INFRASTRUCTURE", ObservedAt: time.Now()},
		{ID: uuid.New(), Kind: "PROVIDER", ObservedAt: time.Now()}, {ID: uuid.New(), Kind: "SCHEMA", ObservedAt: time.Now()},
	}
	s.activationSnapshot = pr.ActivationControlSnapshot{PolicyVersion: "policy-v2", Evidence: evidence, CanPropose: true,
		PendingProposal: &pr.ControlProposal{ID: uuid.New(), Digest: bytes.Repeat([]byte{3}, 32), ProposedAt: time.Now()}}
	r = privacyHandlerRequestFor(http.MethodGet, "/admin/privacidade/ativacao", nil, CurrentUser{ID: actor, CanExecutePrivacy: true})
	w = httptest.NewRecorder()
	h.ActivationControl(w, r)
	for _, want := range []string{"policy-v2", "Restauro isolado", "Infraestrutura", "Destinatários externos", "Esquema de dados"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("activation page missing %q", want)
		}
	}
	if strings.Contains(w.Body.String(), "auth_hmac") || strings.Contains(w.Body.String(), `name="evidence_id"`) {
		t.Fatal("activation page exposed trusted evidence input")
	}
	if strings.Contains(w.Body.String(), "Propor ativação") || strings.Contains(w.Body.String(), "Aprovar e ativar") || strings.Contains(w.Body.String(), "/admin/privacidade/ativacao/propor") || strings.Contains(w.Body.String(), "/admin/privacidade/ativacao/aprovar") {
		t.Fatal("read-only activation page exposed a mutation control")
	}
}

func TestPrivacyOperationalControlFailureAndStaleStateBoundaries(t *testing.T) {
	actor, reference, jobID := uuid.New(), uuid.New(), uuid.New()
	request := func(method, path string, form url.Values) *http.Request {
		r := privacyHandlerRequestFor(method, path, form, CurrentUser{ID: actor, IsAdmin: true, CanExecutePrivacy: true})
		r.SetPathValue("ref", reference.String())
		return r
	}
	t.Run("lookup empty and invalid", func(t *testing.T) {
		s := privacyHandlerFixture(t)
		h := PrivacyRequests{Service: s}
		for path, want := range map[string]int{"/admin/privacidade/controlo": http.StatusOK, "/admin/privacidade/controlo?ref=not-a-uuid": http.StatusUnprocessableEntity} {
			w := httptest.NewRecorder()
			h.ControlLookup(w, request(http.MethodGet, path, nil))
			if w.Code != want {
				t.Fatalf("lookup %s status=%d want=%d", path, w.Code, want)
			}
		}
	})
	t.Run("completion snapshot errors and result states", func(t *testing.T) {
		for _, tc := range []struct {
			name, result string
			err          error
			want         int
		}{
			{"forbidden", "", pr.ErrForbidden, http.StatusNotFound},
			{"unavailable", "", pr.ErrCompletionUnavailable, http.StatusNotFound},
			{"internal", "", errors.New("database unavailable"), http.StatusInternalServerError},
			{"invalid result", "unexpected", nil, http.StatusForbidden},
			{"proposal result", "proposta", nil, http.StatusOK},
			{"approval result", "aprovada", nil, http.StatusOK},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s := privacyHandlerFixture(t)
				s.controlErr = tc.err
				s.controlSnapshot = pr.CompletionControlSnapshot{RequestReference: reference, Jobs: []pr.CompletionControlJob{{JobID: jobID}}}
				w := httptest.NewRecorder()
				r := request(http.MethodGet, "/admin/privacidade/controlo/"+reference.String()+"?resultado="+tc.result, nil)
				PrivacyRequests{Service: s}.CompletionControl(w, r)
				if w.Code != tc.want {
					t.Fatalf("status=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
				}
			})
		}
		w := httptest.NewRecorder()
		r := request(http.MethodGet, "/admin/privacidade/controlo/not-a-uuid", nil)
		r.SetPathValue("ref", "not-a-uuid")
		PrivacyRequests{Service: privacyHandlerFixture(t)}.CompletionControl(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("invalid reference status=%d", w.Code)
		}
	})
	t.Run("terminal mutation stale and service failures", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			approve bool
			setup   func(*privacyHandlerStore)
			form    url.Values
			want    int
		}{
			{"invalid job", false, nil, url.Values{"job_id": {"bad"}, "confirmed": {"yes"}}, http.StatusForbidden},
			{"stale selection", false, nil, url.Values{"job_id": {jobID.String()}, "confirmed": {"yes"}}, http.StatusUnprocessableEntity},
			{"snapshot unavailable", false, func(s *privacyHandlerStore) { s.controlErr = pr.ErrCompletionUnavailable }, url.Values{"job_id": {jobID.String()}, "confirmed": {"yes"}}, http.StatusNotFound},
			{"proposal unavailable", false, func(s *privacyHandlerStore) {
				s.controlSnapshot.Jobs = []pr.CompletionControlJob{{JobID: jobID, CanProposeRequeue: true}}
				s.requeueProposeErr = pr.ErrRequeueUnavailable
			}, url.Values{"job_id": {jobID.String()}, "confirmed": {"yes"}}, http.StatusUnprocessableEntity},
			{"proposal internal", false, func(s *privacyHandlerStore) {
				s.controlSnapshot.Jobs = []pr.CompletionControlJob{{JobID: jobID, CanProposeRequeue: true}}
				s.requeueProposeErr = errors.New("database unavailable")
			}, url.Values{"job_id": {jobID.String()}, "confirmed": {"yes"}}, http.StatusInternalServerError},
			{"approval missing proposal", true, func(s *privacyHandlerStore) {
				s.controlSnapshot.Jobs = []pr.CompletionControlJob{{JobID: jobID, CanApproveRequeue: true}}
			}, url.Values{"job_id": {jobID.String()}, "confirmed": {"yes"}}, http.StatusUnprocessableEntity},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s := privacyHandlerFixture(t)
				if tc.setup != nil {
					tc.setup(s)
				}
				w := httptest.NewRecorder()
				r := request(http.MethodPost, "/admin/privacidade/controlo/"+reference.String(), tc.form)
				h := PrivacyRequests{Service: s}
				if tc.approve {
					h.ApproveTerminalRequeue(w, r)
				} else {
					h.ProposeTerminalRequeue(w, r)
				}
				if w.Code != tc.want {
					t.Fatalf("status=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
				}
			})
		}
	})
	t.Run("activation failure and result states", func(t *testing.T) {
		for _, tc := range []struct {
			result string
			err    error
			want   int
		}{{"", pr.ErrActivationUnavailable, http.StatusNotFound}, {"", errors.New("database unavailable"), http.StatusInternalServerError}, {"unexpected", nil, http.StatusForbidden}, {"proposta", nil, http.StatusOK}, {"aprovada", nil, http.StatusOK}} {
			s := privacyHandlerFixture(t)
			s.activationErr = tc.err
			s.activationSnapshot = pr.ActivationControlSnapshot{PolicyVersion: "policy-v2"}
			w := httptest.NewRecorder()
			r := request(http.MethodGet, "/admin/privacidade/ativacao?resultado="+tc.result, nil)
			PrivacyRequests{Service: s}.ActivationControl(w, r)
			if w.Code != tc.want {
				t.Fatalf("activation result=%q status=%d want=%d", tc.result, w.Code, tc.want)
			}
		}
	})
}

func TestPrivacyNewLoadsApprovedSubjectsAndFailsClosed(t *testing.T) {
	t.Run("renders adopted catalogue", func(t *testing.T) {
		s := privacyHandlerFixture(t)
		s.subjects = []dbgen.User{{ID: uuid.New(), Name: "Adult member"}, {ID: uuid.New(), Name: "Dependent member"}}
		w := httptest.NewRecorder()
		PrivacyRequests{Service: s, ContactURL: "/legal/privacy-contact"}.New(w, privacyHandlerRequest(http.MethodGet, "/perfil/privacidade/novo", nil))
		for _, want := range []string{"Adult member", "Dependent member", "Protected category", "Protected description", "private-policy", "Encerramento da conta"} {
			if !strings.Contains(w.Body.String(), want) {
				t.Errorf("new request page missing %q", want)
			}
		}
		if w.Code != http.StatusOK {
			t.Fatalf("new page status=%d", w.Code)
		}
	})
	for _, tc := range []struct {
		name string
		set  func(*privacyHandlerStore)
		want int
	}{
		{"unavailable policy", func(s *privacyHandlerStore) { s.availableErr = pr.ErrPolicyUnresolved }, http.StatusNotFound},
		{"subject lookup failure", func(s *privacyHandlerStore) { s.subjectsErr = errors.New("subjects unavailable") }, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := privacyHandlerFixture(t)
			tc.set(s)
			w := httptest.NewRecorder()
			PrivacyRequests{Service: s}.New(w, privacyHandlerRequest(http.MethodGet, "/perfil/privacidade/novo", nil))
			if w.Code != tc.want {
				t.Fatalf("new failure status=%d want=%d", w.Code, tc.want)
			}
		})
	}
}

func TestPrivacySubmitValidationErrorsAndSuccess(t *testing.T) {
	actor, subject, requestKey, resultRef := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	valid := url.Values{"subject_id": {subject.String()}, "request_key": {requestKey.String()}, "scope_kind": {"CATEGORIES"}, "categories": {"alpha"}, "password": {"current password"}, "policy_version": {"private-policy"}}
	t.Run("malformed request body is rejected", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/perfil/privacidade/novo", privacyBrokenBody{})
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
		w := httptest.NewRecorder()
		PrivacyRequests{Service: privacyHandlerFixture(t)}.Submit(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("malformed body status=%d", w.Code)
		}
	})
	t.Run("invalid identifiers preserve selected values", func(t *testing.T) {
		form := clonePrivacyValues(valid)
		form.Set("subject_id", "not-a-uuid")
		w := httptest.NewRecorder()
		PrivacyRequests{Service: privacyHandlerFixture(t)}.Submit(w, privacyHandlerRequestFor(http.MethodPost, "/perfil/privacidade/novo", form, CurrentUser{ID: actor}))
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Selecione uma pessoa") || !strings.Contains(w.Body.String(), "checked") {
			t.Fatalf("invalid identifiers status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("unavailable policy fails closed", func(t *testing.T) {
		s := privacyHandlerFixture(t)
		s.availableErr = pr.ErrPolicyUnresolved
		w := httptest.NewRecorder()
		PrivacyRequests{Service: s}.Submit(w, privacyHandlerRequestFor(http.MethodPost, "/perfil/privacidade/novo", valid, CurrentUser{ID: actor}))
		if w.Code != http.StatusNotFound {
			t.Fatalf("unavailable policy status=%d", w.Code)
		}
	})
	for _, tc := range []struct {
		name, scope string
		err         error
		wantStatus  int
		wantMessage string
		categories  []string
	}{
		{"rate limited", "CATEGORIES", pr.ErrRateLimited, http.StatusTooManyRequests, "Aguarde 15 minutos", []string{"alpha"}},
		{"authentication denied", "CATEGORIES", pr.ErrForbidden, http.StatusUnprocessableEntity, "Não foi possível confirmar", []string{"alpha"}},
		{"duplicate", "CATEGORIES", pr.ErrDuplicate, http.StatusUnprocessableEntity, "Já existe um pedido ativo", []string{"alpha"}},
		{"missing categories", "CATEGORIES", pr.ErrInvalid, http.StatusUnprocessableEntity, "Selecione pelo menos uma categoria", nil},
		{"invalid scope", "UNKNOWN", pr.ErrInvalid, http.StatusUnprocessableEntity, "Reveja o âmbito", []string{"alpha"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := privacyHandlerFixture(t)
			s.submitErr = tc.err
			form := clonePrivacyValues(valid)
			form.Set("scope_kind", tc.scope)
			form.Del("categories")
			for _, category := range tc.categories {
				form.Add("categories", category)
			}
			w := httptest.NewRecorder()
			PrivacyRequests{Service: s}.Submit(w, privacyHandlerRequestFor(http.MethodPost, "/perfil/privacidade/novo", form, CurrentUser{ID: actor}))
			if w.Code != tc.wantStatus || !strings.Contains(w.Body.String(), tc.wantMessage) {
				t.Fatalf("submit error status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.err == pr.ErrRateLimited && w.Header().Get("Retry-After") != "900" {
				t.Fatal("rate limit omitted Retry-After")
			}
		})
	}
	t.Run("forwards authenticated actor session remote IP and scope", func(t *testing.T) {
		s := privacyHandlerFixture(t)
		s.submitResult.PublicRef = resultRef
		sessions := scs.New()
		setup := httptest.NewRecorder()
		sessions.LoadAndSave(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			sessions.Put(r.Context(), "credential_version", int64(9))
		})).ServeHTTP(setup, httptest.NewRequest(http.MethodGet, "/", nil))
		cookie := setup.Result().Cookies()[0]
		r := privacyHandlerRequestFor(http.MethodPost, "/perfil/privacidade/novo", valid, CurrentUser{ID: actor})
		r.AddCookie(cookie)
		r = r.WithContext(httpx.WithRemoteIP(r.Context(), netip.MustParseAddr("203.0.113.45")))
		w := httptest.NewRecorder()
		sessions.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			PrivacyRequests{Service: s, Sessions: sessions}.Submit(w, r)
		})).ServeHTTP(w, r)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/perfil/privacidade/"+resultRef.String() {
			t.Fatalf("success status=%d location=%q", w.Code, w.Header().Get("Location"))
		}
		if s.submitInput.ActorID != actor || s.submitInput.SubjectID != subject || s.submitInput.RequestKey != requestKey || s.submitInput.CredentialVersion != 9 || s.submitInput.IP != "203.0.113.45" || s.submitInput.PolicyVersion != "private-policy" || s.submitInput.Scope.Kind != pr.Categories || len(s.submitInput.Scope.Categories) != 1 || s.submitInput.Scope.Categories[0] != "alpha" {
			t.Fatalf("submission not forwarded safely: %+v", s.submitInput)
		}
	})
}

func clonePrivacyValues(source url.Values) url.Values {
	out := make(url.Values, len(source))
	for key, values := range source {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func privacyTestStamp(at time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: at, Valid: true}
}

func TestPrivacyDetailReviewerStateHistoryAndFailures(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	actor := uuid.New()
	t.Run("claimed verified representative exposes only valid reviewer controls", func(t *testing.T) {
		s := privacyHandlerFixture(t)
		relationshipAt := privacyTestStamp(now.AddDate(0, 0, -1))
		s.view.Record.Status = "UNDER_REVIEW"
		s.view.Record.ScopeKind = string(pr.AccountClosure)
		s.view.Record.Categories = nil
		s.view.Record.ClaimedBy = &actor
		s.view.Record.DueAt = privacyTestStamp(now.AddDate(0, 0, 3))
		s.view.Record.IdentityVerifiedAt = privacyTestStamp(now)
		s.view.Record.IdentityMethod = stringPointer("IN_PERSON")
		s.view.Record.RepresentationVerifiedAt = privacyTestStamp(now)
		s.view.Record.RepresentationMethod = stringPointer("DOCUMENT_CHECK")
		s.view.Record.RepresentationRelationshipUpdatedAt = relationshipAt
		s.view.Subject.ID = uuid.New()
		s.view.Subject.UpdatedAt = relationshipAt
		s.view.Requester.ID = uuid.New()
		s.view.Policy.Categories = append(s.view.Policy.Categories, pr.CatalogueEntry{Key: "beta", Label: "Second category"})
		dependant := dbgen.User{ID: uuid.New(), Name: "Dependent child", UpdatedAt: relationshipAt}
		s.view.Dependants = []dbgen.User{dependant}
		s.view.Resolutions = []dbgen.PrivacyRequestDependantResolution{{DependantID: dependant.ID, RelationshipUpdatedAt: relationshipAt, VerifiedAt: privacyTestStamp(now)}}
		s.view.Events = []dbgen.DataErasureRequestEvent{{Action: "RECEIVED", OccurredAt: privacyTestStamp(now.Add(-time.Hour))}, {Action: "FUTURE_EVENT", OccurredAt: privacyTestStamp(now)}}
		w := httptest.NewRecorder()
		r := privacyHandlerRequestFor(http.MethodGet, "/admin/privacidade/11000000-0000-0000-0000-000000000001", nil, CurrentUser{ID: actor, Name: "Reviewer"})
		PrivacyRequests{Service: s, Now: func() time.Time { return now }}.Detail(w, r)
		for _, want := range []string{"Encerramento da conta", "Guardar verificação", "Aprovar apagamento", "Prorrogar o prazo", "Dependent child", "Resolução verificada", "Pedido recebido", "Pedido atualizado", "Second category"} {
			if !strings.Contains(w.Body.String(), want) {
				t.Errorf("review detail missing %q", want)
			}
		}
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "Assumir análise") {
			t.Fatalf("review detail status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("unclaimed case can be claimed and extended due date is displayed", func(t *testing.T) {
		s := privacyHandlerFixture(t)
		s.view.Record.Status = "RECEIVED"
		s.view.Record.ClaimedBy = nil
		s.view.Record.DueAt = privacyTestStamp(now.AddDate(0, 0, 2))
		s.view.Record.ExtendedDueAt = privacyTestStamp(now.AddDate(0, 1, 2))
		w := httptest.NewRecorder()
		PrivacyRequests{Service: s, Now: func() time.Time { return now }}.Detail(w, privacyHandlerRequestFor(http.MethodGet, "/admin/privacidade/11000000-0000-0000-0000-000000000001", nil, CurrentUser{ID: actor}))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Assumir análise") || !strings.Contains(w.Body.String(), privacyDate(s.view.Record.ExtendedDueAt.Time)) {
			t.Fatalf("claim detail status=%d body=%s", w.Code, w.Body.String())
		}
	})
	for _, tc := range []struct {
		name      string
		configure func(*privacyHandlerStore, *http.Request)
		want      int
	}{
		{"invalid reference", func(_ *privacyHandlerStore, r *http.Request) { r.SetPathValue("ref", "invalid") }, http.StatusNotFound},
		{"internal lookup failure", func(s *privacyHandlerStore, _ *http.Request) { s.viewErr = errors.New("view unavailable") }, http.StatusInternalServerError},
		{"invalid stored decisions", func(s *privacyHandlerStore, _ *http.Request) { s.view.Record.CategoryDecisions = []byte("{") }, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := privacyHandlerFixture(t)
			r := privacyHandlerRequest(http.MethodGet, "/admin/privacidade/11000000-0000-0000-0000-000000000001", nil)
			tc.configure(s, r)
			w := httptest.NewRecorder()
			PrivacyRequests{Service: s}.Detail(w, r)
			if w.Code != tc.want {
				t.Fatalf("detail failure status=%d want=%d", w.Code, tc.want)
			}
		})
	}
}

func TestPrivacyChangeParsesActionsRedirectsAndHandlesFailures(t *testing.T) {
	actor, dependant, related := uuid.New(), uuid.New(), uuid.New()
	fullForm := url.Values{
		"version":                    {"7"},
		"action":                     {"partial"},
		"policy_version":             {"private-policy"},
		"identity_method":            {"IN_PERSON"},
		"representation_method":      {"DOCUMENT_CHECK"},
		"identity_verified":          {"yes"},
		"representation_verified":    {"yes"},
		"conflict":                   {"yes"},
		"explanation":                {"Reviewed category decisions."},
		"extension_months":           {"2"},
		"extension_reason":           {"COMPLEXITY"},
		"dependant_id":               {dependant.String()},
		"related_ref":                {related.String()},
		"resolution_code":            {"FORMAL_RESOLUTION"},
		"resolution_explanation":     {"Verified independently."},
		"outcome_alpha":              {"RETAIN"},
		"ground_alpha":               {"hold"},
		"outcome_ignored_duplicates": {"APPROVE", "RETAIN"},
	}
	t.Run("management action forwards every reviewed field", func(t *testing.T) {
		s := privacyHandlerFixture(t)
		w := httptest.NewRecorder()
		PrivacyRequests{Service: s}.Change(w, privacyHandlerRequestFor(http.MethodPost, "/admin/privacidade/11000000-0000-0000-0000-000000000001", fullForm, CurrentUser{ID: actor}))
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/privacidade/11000000-0000-0000-0000-000000000001" {
			t.Fatalf("management redirect status=%d location=%q", w.Code, w.Header().Get("Location"))
		}
		in := s.changeInput
		if in.ActorID != actor || in.Version != 7 || in.Action != "partial" || in.PolicyVersion != "private-policy" || in.IdentityMethod != "IN_PERSON" || in.RepresentationMethod != "DOCUMENT_CHECK" || !in.IdentityVerified || !in.RepresentationVerified || !in.Conflict || in.Explanation != "Reviewed category decisions." || in.ExtensionMonths != 2 || in.ExtensionReason != "COMPLEXITY" || in.DependantID != dependant || in.RelatedReference != related || in.ResolutionCode != "FORMAL_RESOLUTION" || in.ResolutionExplanation != "Verified independently." || in.Decisions["alpha"].Ground != "hold" {
			t.Fatalf("review input not forwarded: %+v", in)
		}
		if _, exists := in.Decisions["ignored_duplicates"]; exists {
			t.Fatal("ambiguous duplicate outcome was accepted")
		}
	})
	t.Run("member route forces cancellation", func(t *testing.T) {
		s := privacyHandlerFixture(t)
		form := url.Values{"version": {"7"}, "action": {"approve"}}
		w := httptest.NewRecorder()
		PrivacyRequests{Service: s}.Change(w, privacyHandlerRequestFor(http.MethodPost, "/perfil/privacidade/11000000-0000-0000-0000-000000000001/cancelar", form, CurrentUser{ID: actor}))
		if w.Code != http.StatusSeeOther || s.changeInput.Action != "cancel" || w.Header().Get("Location") != "/perfil/privacidade/11000000-0000-0000-0000-000000000001" {
			t.Fatalf("member cancellation status=%d input=%+v", w.Code, s.changeInput)
		}
	})
	t.Run("malformed form is rejected before mutation", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/admin/privacidade/ref", privacyBrokenBody{})
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
		w := httptest.NewRecorder()
		PrivacyRequests{Service: privacyHandlerFixture(t)}.Change(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("malformed change status=%d", w.Code)
		}
	})
	for _, tc := range []struct {
		name, ref, version string
	}{
		{"invalid reference", "not-a-uuid", "7"},
		{"invalid version", uuid.NewString(), "seven"},
		{"non-positive version", uuid.NewString(), "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := privacyHandlerRequest(http.MethodPost, "/admin/privacidade/invalid", url.Values{"version": {tc.version}})
			r.SetPathValue("ref", tc.ref)
			w := httptest.NewRecorder()
			PrivacyRequests{Service: privacyHandlerFixture(t)}.Change(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("invalid change status=%d", w.Code)
			}
		})
	}
	for _, tc := range []struct {
		name, message string
		err           error
		want          int
	}{
		{"forbidden mutation is hidden", "Página não encontrada", pr.ErrForbidden, http.StatusNotFound},
		{"closure safeguard explains correction", "Resolva os dependentes", pr.ErrClosureSafeguards, http.StatusUnprocessableEntity},
		{"validation failure redisplays case", "Não foi possível guardar", pr.ErrInvalid, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := privacyHandlerFixture(t)
			s.changeErr = tc.err
			w := httptest.NewRecorder()
			PrivacyRequests{Service: s}.Change(w, privacyHandlerRequestFor(http.MethodPost, "/admin/privacidade/11000000-0000-0000-0000-000000000001", fullForm, CurrentUser{ID: actor}))
			if w.Code != tc.want || !strings.Contains(w.Body.String(), tc.message) {
				t.Fatalf("change failure status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestPrivacyExecutionUsesSeparateConfirmedExecutorAction(t *testing.T) {
	actor := uuid.New()
	s := privacyHandlerFixture(t)
	s.view.CanReview = false
	s.view.CanViewExecution = true
	s.view.CanExecute = true
	s.view.Record.Status = "AWAITING_EXECUTION"
	s.view.Plan = &pr.ExecutionPlan{Entries: []pr.ExecutionPlanEntry{{Category: "alpha", Disposition: "DELETE", Owner: "PRIVACY", Operations: []string{"PROFILE_IDENTITY_DELETE"}}}}
	w := httptest.NewRecorder()
	PrivacyRequests{Service: s}.Detail(w, privacyHandlerRequestFor(http.MethodGet, "/admin/privacidade/11000000-0000-0000-0000-000000000001", nil, CurrentUser{ID: actor}))
	for _, want := range []string{"Plano de execução aprovado", "Iniciar processamento", `action="/admin/privacidade/11000000-0000-0000-0000-000000000001/executar"`, `name="execution_confirmed"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("executor detail missing %q", want)
		}
	}
	if strings.Contains(w.Body.String(), `value="claim"`) || strings.Contains(w.Body.String(), `value="approve"`) {
		t.Fatal("executor-only view exposed reviewer controls")
	}

	form := url.Values{"version": {"7"}, "execution_confirmed": {"yes"}}
	w = httptest.NewRecorder()
	PrivacyRequests{Service: s}.StartExecution(w, privacyHandlerRequestFor(http.MethodPost, "/admin/privacidade/11000000-0000-0000-0000-000000000001/executar", form, CurrentUser{ID: actor}))
	if w.Code != http.StatusSeeOther || s.startInput.ActorID != actor || s.startInput.Version != 7 || !s.startInput.Confirmed {
		t.Fatalf("start result status=%d input=%+v", w.Code, s.startInput)
	}

	s.startErr = pr.ErrStaleVersion
	w = httptest.NewRecorder()
	PrivacyRequests{Service: s}.StartExecution(w, privacyHandlerRequestFor(http.MethodPost, "/admin/privacidade/11000000-0000-0000-0000-000000000001/executar", form, CurrentUser{ID: actor}))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "atualizado por outra pessoa") {
		t.Fatalf("stale start status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestPrivacyExecutionBlockersUseBoundedPublicCopy(t *testing.T) {
	for code, want := range map[string]string{
		"EXECUTOR_AUTHORITY_OR_SEPARATION": "pessoa executora autorizada",
		"IDENTITY_CHANGED":                 "verificação de identidade",
		"RELATIONSHIP_CHANGED":             "relação atual",
		"REPRESENTATION_CHANGED":           "representação está incompleta",
		"DECISION_AUTHORITY":               "autoridade histórica",
		"CAPABILITIES_UNAVAILABLE":         "operações exigidas",
		"ACTIVATION_DISABLED":              "execução permanece desativada",
		"ADMIN_CONTINUITY":                 "última pessoa administradora",
		"LEGACY_SESSIONS":                  "sessões antigas",
		"DEPENDANTS_UNRESOLVED":            "dependentes sem transferência",
		"FUTURE_PRIVATE_DETAIL":            "bloqueio de segurança",
	} {
		if got := privacyExecutionBlocker(code); !strings.Contains(got, want) {
			t.Errorf("blocker %s=%q want substring %q", code, got, want)
		}
	}
}

func TestPrivacyExecutionRejectsMalformedInputsAndMapsFailures(t *testing.T) {
	actor := uuid.New()
	t.Run("malformed form", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/admin/privacidade/ref/executar", privacyBrokenBody{})
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
		w := httptest.NewRecorder()
		PrivacyRequests{Service: privacyHandlerFixture(t)}.StartExecution(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status=%d", w.Code)
		}
	})
	for _, tc := range []struct{ name, ref, version string }{
		{name: "invalid reference", ref: "invalid", version: "7"},
		{name: "invalid version", ref: uuid.NewString(), version: "seven"},
		{name: "non-positive version", ref: uuid.NewString(), version: "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := privacyHandlerRequestFor(http.MethodPost, "/admin/privacidade/ref/executar", url.Values{"version": {tc.version}}, CurrentUser{ID: actor})
			r.SetPathValue("ref", tc.ref)
			w := httptest.NewRecorder()
			PrivacyRequests{Service: privacyHandlerFixture(t)}.StartExecution(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
	for _, tc := range []struct {
		name string
		err  error
		want int
		text string
	}{
		{name: "forbidden", err: pr.ErrForbidden, want: http.StatusNotFound, text: "Página não encontrada"},
		{name: "executor unavailable", err: pr.ErrExecutorUnavailable, want: http.StatusUnprocessableEntity, text: "execução não pode começar"},
		{name: "closure safeguard", err: pr.ErrClosureSafeguards, want: http.StatusUnprocessableEntity, text: "Resolva os dependentes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := privacyHandlerFixture(t)
			s.startErr = tc.err
			w := httptest.NewRecorder()
			PrivacyRequests{Service: s}.StartExecution(w, privacyHandlerRequestFor(http.MethodPost, "/admin/privacidade/ref/executar", url.Values{"version": {"7"}, "execution_confirmed": {"yes"}}, CurrentUser{ID: actor}))
			if w.Code != tc.want || !strings.Contains(w.Body.String(), tc.text) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestPrivacyReviewerCannotSeeExecutorPlanOrBlockers(t *testing.T) {
	s := privacyHandlerFixture(t)
	s.view.CanReview = true
	s.view.CanViewExecution = false
	s.view.Record.Status = "AWAITING_EXECUTION"
	s.view.Plan = &pr.ExecutionPlan{Entries: []pr.ExecutionPlanEntry{{Category: "alpha", Disposition: "DELETE", Owner: "PRIVACY", Operations: []string{"PROFILE_IDENTITY_DELETE"}}}}
	s.view.ExecutionBlockers = []string{"IDENTITY_CHANGED"}
	w := httptest.NewRecorder()
	PrivacyRequests{Service: s}.Detail(w, privacyHandlerRequestFor(http.MethodGet, "/admin/privacidade/11000000-0000-0000-0000-000000000001", nil, CurrentUser{ID: uuid.New()}))
	for _, forbidden := range []string{"Plano de execução aprovado", "PROFILE_IDENTITY_DELETE", "Bloqueios atuais", "Iniciar processamento"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Errorf("reviewer detail exposed %q", forbidden)
		}
	}
}

func TestPrivacyEventLabelsUseStablePublicCopy(t *testing.T) {
	if got := privacyEventLabel("APPROVED"); got != "Aprovado — a aguardar execução" {
		t.Fatalf("approved label=%q", got)
	}
	if got := privacyEventLabel("FUTURE_EVENT"); got != "Pedido atualizado" {
		t.Fatalf("unknown event label=%q", got)
	}
}

func stringPointer(value string) *string { return &value }
func TestPrivacyDetailRendersSavedDecisionAndApprovedGround(t *testing.T) {
	s := privacyHandlerFixture(t)
	r := privacyHandlerRequest(http.MethodGet, "/perfil/privacidade/11000000-0000-0000-0000-000000000001", nil)
	w := httptest.NewRecorder()
	PrivacyRequests{Service: s}.Detail(w, r)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	for _, want := range []string{"Protected category", "Approved preservation ground", "Protected explanation"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatal("missing privacy headers")
	}
}
func TestPrivacyDetailSafeReceiptOmitsProtectedDecision(t *testing.T) {
	s := privacyHandlerFixture(t)
	s.view.SafeReceipt = true
	w := httptest.NewRecorder()
	PrivacyRequests{Service: s}.Detail(w, privacyHandlerRequest(http.MethodGet, "/perfil/privacidade/11000000-0000-0000-0000-000000000001", nil))
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	for _, secret := range []string{"Protected subject", "Protected requester", "Protected category", "Approved preservation ground", "Protected explanation", "private-policy"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("safe receipt exposed %q", secret)
		}
	}
}
func TestPrivacyChangeStaleVersionShowsCurrentCase(t *testing.T) {
	s := privacyHandlerFixture(t)
	s.changeErr = pr.ErrStaleVersion
	w := httptest.NewRecorder()
	PrivacyRequests{Service: s}.Change(w, privacyHandlerRequest(http.MethodPost, "/admin/privacidade/11000000-0000-0000-0000-000000000001", url.Values{"version": {"6"}, "action": {"refuse"}, "outcome_alpha": {"RETAIN"}, "ground_alpha": {"hold"}}))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "O pedido foi atualizado") {
		t.Fatalf("stale response %d", w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatal("privacy POST response did not preserve same-origin form policy")
	}
	if s.changeInput.Version != 6 || s.changeInput.Decisions["alpha"].Ground != "hold" {
		t.Fatalf("lost submitted CAS/decision: %+v", s.changeInput)
	}
}
func TestPrivacyDetailDeniedDoesNotExposeCase(t *testing.T) {
	s := privacyHandlerFixture(t)
	s.viewErr = pr.ErrForbidden
	w := httptest.NewRecorder()
	PrivacyRequests{Service: s}.Detail(w, privacyHandlerRequest(http.MethodGet, "/admin/privacidade/11000000-0000-0000-0000-000000000001", nil))
	if w.Code != 404 || strings.Contains(w.Body.String(), "Protected explanation") {
		t.Fatalf("unauthorized response %d", w.Code)
	}
}

func TestPrivacyDetailDoesNotOfferAnotherReviewersCaseActions(t *testing.T) {
	s := privacyHandlerFixture(t)
	s.view.Record.Status = "UNDER_REVIEW"
	s.view.Record.DueAt = pgtype.Timestamptz{Time: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Valid: true}
	otherReviewer := uuid.New()
	s.view.Record.ClaimedBy = &otherReviewer
	r := privacyHandlerRequest(http.MethodGet, "/admin/privacidade/11000000-0000-0000-0000-000000000001", nil)
	w := httptest.NewRecorder()
	PrivacyRequests{Service: s, Now: func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }}.Detail(w, r)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	for _, action := range []string{"Assumir análise", "Guardar verificação", "Prorrogar prazo"} {
		if strings.Contains(w.Body.String(), action) {
			t.Errorf("offered %q for another reviewer's case", action)
		}
	}
}
