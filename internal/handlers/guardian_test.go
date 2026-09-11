package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestGuardianDashboardRendersDependentsAndDeduplicatedCalendars(t *testing.T) {
	guardianID := uuid.New()
	store := &guardianDashboardStore{dependents: []dbgen.ListDependentsByGuardianRow{
		{Name: "Ana", DateOfBirth: pgtype.Date{Time: time.Date(2014, 7, 25, 0, 0, 0, 0, time.UTC), Valid: true}},
		{Name: "Bruno", DateOfBirth: pgtype.Date{Time: time.Date(2010, 7, 24, 0, 0, 0, 0, time.UTC), Valid: true}},
	}}
	dashboard := guardianDashboard(store, &guardianDependentStoreFake{})
	response := guardianResponse(t, dashboard.Guardian, guardianID, nil)
	body := response.Body.String()
	for _, want := range []string{"Ana", "11 anos", "Bruno", "16 anos", `href="https://example.test/responsabilidade"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAddDependentValidatesBeforeStore(t *testing.T) {
	store := &guardianDependentStoreFake{}
	dashboard := guardianDashboard(&guardianDashboardStore{}, store)
	form := validDependentForm()
	form.Set("date_of_birth", "2000-01-01")
	form.Del("accept_minor_responsibility")
	response := guardianResponse(t, dashboard.AddDependent, uuid.New(), form)
	if response.Code != http.StatusUnprocessableEntity || store.called {
		t.Fatalf("response = %d, called = %t", response.Code, store.called)
	}
	for _, want := range []string{"tem de ter menos de 18 anos", "Tem de aceitar a responsabilidade"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAddDependentPreservesAcceptedResponsibilityOnValidationError(t *testing.T) {
	store := &guardianDependentStoreFake{}
	dashboard := guardianDashboard(&guardianDashboardStore{}, store)
	form := validDependentForm()
	form.Set("name", "X")
	response := guardianResponse(t, dashboard.AddDependent, uuid.New(), form)
	if response.Code != http.StatusUnprocessableEntity || store.called {
		t.Fatalf("response = %d, called = %t", response.Code, store.called)
	}
	if !strings.Contains(response.Body.String(), `id="accept_minor_responsibility" name="accept_minor_responsibility" type="checkbox" required checked`) {
		t.Fatalf("accepted responsibility was not preserved: %q", response.Body.String())
	}
}

func TestAddDependentCreatesFromCurrentGuardianAndRedirects(t *testing.T) {
	guardianID := uuid.New()
	store := &guardianDependentStoreFake{}
	dashboard := guardianDashboard(&guardianDashboardStore{}, store)
	response := guardianResponse(t, dashboard.AddDependent, guardianID, validDependentForm())
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/dashboard/guardian" {
		t.Fatalf("response = %d %q", response.Code, response.Header().Get("Location"))
	}
	if !store.called || store.input.GuardianID != guardianID || store.input.Name != "Maria Silva" {
		t.Fatalf("input = %+v", store.input)
	}
	if store.input.ResponsibilityVersion != "1.0" || store.input.ResponsibilitySHA256 != strings.Repeat("c", 64) {
		t.Fatalf("consent input = %+v", store.input)
	}
}

func TestAddDependentReportsMaximumAndSupportsHTMX(t *testing.T) {
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{err: ErrMaximumDependents})
	response := guardianResponse(t, dashboard.AddDependent, uuid.New(), validDependentForm())
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), maximumDependentsMessage) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}

	dashboard = guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	response = guardianResponse(t, dashboard.AddDependent, uuid.New(), validDependentForm())
	if response.Code != http.StatusSeeOther {
		t.Fatalf("normal response = %d", response.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/guardian/add-dependent", strings.NewReader(validDependentForm().Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	ctx := context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Name: "Guardião"})
	response = httptest.NewRecorder()
	guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{}).AddDependent(response, request.WithContext(ctx))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="guardian-content"`) || !strings.Contains(response.Body.String(), "Pedido recebido.") {
		t.Fatalf("HTMX response = %d %q", response.Code, response.Body.String())
	}
}

func TestGuardianDashboardKeepsPendingRelationshipStatusOnly(t *testing.T) {
	guardianID, subjectID := uuid.New(), uuid.New()
	authority := &guardianAuthorityStoreFake{policyAvailable: true, relationships: []GuardianAuthorityRelationship{{Reference: uuid.New(), GuardianID: guardianID, SubjectID: subjectID, SubmittedLabel: "Pedido familiar", SubjectName: "Nome privado", State: "PENDING", DateOfBirth: time.Date(2012, 1, 2, 0, 0, 0, 0, time.UTC), MinorLoginIssued: true, ProfileComplete: true}}}
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = authority
	response := guardianResponse(t, dashboard.Guardian, guardianID, nil)
	body := response.Body.String()
	for _, want := range []string{"Pedido familiar", "A aguardar verificação", "não permitem consultar dados"} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	for _, forbidden := range []string{"Nome privado", subjectID.String(), "Perfil incompleto", "classificação", "Acesso individual"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("pending response exposes %q", forbidden)
		}
	}
	if response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache control = %q", response.Header().Get("Cache-Control"))
	}
}

func TestGuardianDashboardClosesNewRequestsWithoutAdoptedPolicy(t *testing.T) {
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = &guardianAuthorityStoreFake{}
	response := guardianResponse(t, dashboard.Guardian, uuid.New(), nil)
	body := response.Body.String()
	if !strings.Contains(body, "Novos pedidos temporariamente indisponíveis") || strings.Contains(body, "Pedir associação</a>") || strings.Contains(body, `action="/guardian/add-dependent"`) {
		t.Fatalf("closed policy response = %q", body)
	}
}

func TestAddDependentRejectsForgedPostWithoutAdoptedPolicy(t *testing.T) {
	dependents := &guardianDependentStoreFake{}
	dashboard := guardianDashboard(&guardianDashboardStore{}, dependents)
	dashboard.GuardianAuthority = &guardianAuthorityStoreFake{}
	response := guardianResponse(t, dashboard.AddDependent, uuid.New(), validDependentForm())
	if response.Code != http.StatusConflict || dependents.called || !strings.Contains(response.Body.String(), "política de verificação ainda não foi ativada") {
		t.Fatalf("response=%d called=%v body=%q", response.Code, dependents.called, response.Body.String())
	}
}

func TestGuardianAuthorityDecisionValidatesMetadataAndConcurrency(t *testing.T) {
	actor, ref := uuid.New(), uuid.New()
	store := &guardianAuthorityStoreFake{policyAvailable: true, detail: GuardianAuthorityRelationship{Reference: ref, GuardianID: uuid.New(), SubjectName: "Minor", SubmittedLabel: "Guardian", State: "PENDING", Version: 2, DateOfBirth: time.Date(2012, 1, 2, 0, 0, 0, 0, time.UTC)}, evidenceTypes: []GuardianAuthorityEvidenceType{{Code: "COURT_ORDER", Label: "Decisão judicial"}}, reasonCodes: []GuardianAuthorityReasonCode{{Code: "EVIDENCE_CONFIRMED", Label: "Comprovativo confirmado"}}}
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = store
	dashboard.Now = func() time.Time { return time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC) }

	invalid := guardianAuthorityRequest(ref, actor, url.Values{"version": {"2"}, "action": {"VERIFY"}, "reason_code": {"EVIDENCE_CONFIRMED"}, "confirmed": {"yes"}})
	w := httptest.NewRecorder()
	dashboard.GuardianAuthorityTransition(w, invalid)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "SHA-256 válida") {
		t.Fatalf("invalid response=%d %q", w.Code, w.Body.String())
	}

	form := url.Values{"version": {"2"}, "action": {"VERIFY"}, "evidence_type": {"COURT_ORDER"}, "evidence_reference": {"vault-ref-123"}, "evidence_digest": {strings.Repeat("ab", 32)}, "reason_code": {"EVIDENCE_CONFIRMED"}, "confirmed": {"yes"}}
	w = httptest.NewRecorder()
	dashboard.GuardianAuthorityTransition(w, guardianAuthorityRequest(ref, actor, form))
	if w.Code != http.StatusSeeOther || store.transition.Reference != ref || store.transition.ActorID != actor || store.transition.ExpectedVersion != 2 || len(store.transition.EvidenceDigest) != 32 {
		t.Fatalf("transition response=%d input=%+v", w.Code, store.transition)
	}

	store.transitionErr = ErrGuardianAuthorityConflict
	w = httptest.NewRecorder()
	dashboard.GuardianAuthorityTransition(w, guardianAuthorityRequest(ref, actor, form))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "alterado por outra pessoa") {
		t.Fatalf("conflict response=%d %q", w.Code, w.Body.String())
	}
}

func TestGuardianAuthorityConflictRecorderCannotSeeDecisionForm(t *testing.T) {
	actor, ref := uuid.New(), uuid.New()
	store := &guardianAuthorityStoreFake{
		detail: GuardianAuthorityRelationship{
			Reference: ref, GuardianID: uuid.New(), GuardianName: "Pessoa requerente", SubjectID: uuid.New(),
			SubjectName: "Menor", State: "SUSPENDED", Version: 3, Conflict: true, ConflictActorID: &actor,
			DateOfBirth: time.Date(2012, 1, 2, 0, 0, 0, 0, time.UTC),
		},
	}
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = store
	r := httptest.NewRequest(http.MethodGet, "/admin/representacoes/"+ref.String(), nil)
	r.SetPathValue("ref", ref.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Verifier", CanVerifyGuardianAuthority: true}))
	w := httptest.NewRecorder()
	dashboard.GuardianAuthorityDetail(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Outra pessoa verificadora autorizada") || strings.Contains(w.Body.String(), `id="guardian-authority-decision"`) {
		t.Fatalf("response=%d %q", w.Code, w.Body.String())
	}
}

func TestGuardianAuthorityExpiredDetailOffersRenewalAndMaterializedExpiry(t *testing.T) {
	actor, ref := uuid.New(), uuid.New()
	reviewDue := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	store := &guardianAuthorityStoreFake{detail: GuardianAuthorityRelationship{Reference: ref, GuardianID: uuid.New(), GuardianName: "Pessoa requerente", SubjectID: uuid.New(), SubjectName: "Menor", State: "EXPIRED", StoredState: "VERIFIED", Version: 2, ReviewDueAt: &reviewDue, DateOfBirth: time.Date(2012, 1, 2, 0, 0, 0, 0, time.UTC)}}
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = store
	dashboard.Now = func() time.Time { return time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC) }
	r := httptest.NewRequest(http.MethodGet, "/admin/representacoes/"+ref.String(), nil)
	r.SetPathValue("ref", ref.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Verifier", CanVerifyGuardianAuthority: true}))
	w := httptest.NewRecorder()
	dashboard.GuardianAuthorityDetail(w, r)
	body := w.Body.String()
	for _, want := range []string{`value="VERIFY"`, `value="EXPIRE"`, `data-task-form`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if strings.Contains(body, `value="REJECT"`) || strings.Contains(body, `value="SUSPEND"`) {
		t.Fatal("effectively expired relationship offered an invalid action")
	}
}

func TestGuardianAuthorityTerminalAndMajorityDetailsAreReadOnly(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, state, stored, want string
		birth                     time.Time
	}{
		{name: "rejected", state: "REJECTED", stored: "REJECTED", birth: time.Date(2012, 1, 2, 0, 0, 0, 0, time.UTC), want: "Este pedido terminou sem aprovação"},
		{name: "majority", state: "EXPIRED", stored: "VERIFIED", birth: time.Date(2008, 9, 11, 0, 0, 0, 0, time.UTC), want: "estabelecer ou recuperar o acesso à conta adulta correta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor, ref := uuid.New(), uuid.New()
			store := &guardianAuthorityStoreFake{detail: GuardianAuthorityRelationship{Reference: ref, GuardianID: uuid.New(), GuardianName: "Pessoa requerente", SubjectID: uuid.New(), SubjectName: "Pessoa", State: tc.state, StoredState: tc.stored, Version: 3, DateOfBirth: tc.birth}}
			dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
			dashboard.GuardianAuthority = store
			dashboard.Now = func() time.Time { return now }
			r := httptest.NewRequest(http.MethodGet, "/admin/representacoes/"+ref.String(), nil)
			r.SetPathValue("ref", ref.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Verifier", CanVerifyGuardianAuthority: true}))
			w := httptest.NewRecorder()
			dashboard.GuardianAuthorityDetail(w, r)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.want) || strings.Contains(w.Body.String(), `id="guardian-authority-decision"`) {
				t.Fatalf("response=%d %q", w.Code, w.Body.String())
			}
		})
	}
}

func guardianAuthorityRequest(ref, actor uuid.UUID, form url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/admin/representacoes/"+ref.String(), strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("ref", ref.String())
	return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Verifier", CanVerifyGuardianAuthority: true}))
}

func guardianDashboard(store DashboardStore, dependents GuardianDependentStore) Dashboard {
	authority := &guardianAuthorityStoreFake{policyAvailable: true}
	if source, ok := store.(*guardianDashboardStore); ok {
		for _, dependent := range source.dependents {
			authority.relationships = append(authority.relationships, GuardianAuthorityRelationship{Reference: uuid.New(), SubjectID: dependent.ID, SubmittedLabel: dependent.Name, SubjectName: dependent.Name, State: "VERIFIED", Version: 1, DateOfBirth: dependent.DateOfBirth.Time, VerifiedUntil: timePointer(time.Date(2027, 7, 24, 0, 0, 0, 0, time.UTC)), LeaderboardVisible: dependent.LeaderboardVisible, ProfileComplete: dependent.ProfileComplete})
		}
	}
	return Dashboard{
		Store: store, Dependents: dependents, GuardianAuthority: authority, Location: time.UTC, Now: func() time.Time { return time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC) },
		PageMeta:              components.PageMeta{StylesheetURL: "/assets/app.css", ScriptURL: "/assets/app.js"},
		ResponsibilityVersion: "1.0", ResponsibilitySHA256: strings.Repeat("c", 64), ResponsibilityURL: "https://example.test/responsabilidade",
	}
}

func timePointer(value time.Time) *time.Time { return &value }

type guardianAuthorityStoreFake struct {
	policyAvailable bool
	relationships   []GuardianAuthorityRelationship
	pending         []GuardianAuthorityRelationship
	detail          GuardianAuthorityRelationship
	evidenceTypes   []GuardianAuthorityEvidenceType
	reasonCodes     []GuardianAuthorityReasonCode
	transition      GuardianAuthorityTransitionInput
	err             error
	transitionErr   error
}

func (s *guardianAuthorityStoreFake) PolicyAvailable(context.Context) (bool, error) {
	return s.policyAvailable, s.err
}
func (s *guardianAuthorityStoreFake) EvidenceTypes(context.Context) ([]GuardianAuthorityEvidenceType, error) {
	return s.evidenceTypes, s.err
}
func (s *guardianAuthorityStoreFake) ReasonCodes(context.Context) ([]GuardianAuthorityReasonCode, error) {
	return s.reasonCodes, s.err
}
func (s *guardianAuthorityStoreFake) ListForGuardian(context.Context, uuid.UUID, int32) ([]GuardianAuthorityRelationship, error) {
	return s.relationships, s.err
}
func (s *guardianAuthorityStoreFake) ListPending(context.Context, uuid.UUID, int32, int32) ([]GuardianAuthorityRelationship, error) {
	return s.pending, s.err
}
func (s *guardianAuthorityStoreFake) GetForVerifier(context.Context, uuid.UUID, uuid.UUID) (GuardianAuthorityRelationship, error) {
	return s.detail, s.err
}
func (s *guardianAuthorityStoreFake) Transition(_ context.Context, input GuardianAuthorityTransitionInput) error {
	s.transition = input
	return s.transitionErr
}

func validDependentForm() url.Values {
	return url.Values{"name": {"  Maria   Silva "}, "date_of_birth": {"2010-07-24"}, "accept_minor_responsibility": {"on"}}
}

func guardianResponse(t *testing.T, handler http.HandlerFunc, guardianID uuid.UUID, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	method, body := http.MethodGet, ""
	if form != nil {
		method, body = http.MethodPost, form.Encode()
	}
	request := httptest.NewRequest(method, "/dashboard/guardian", strings.NewReader(body))
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	ctx := context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: guardianID, Name: "Guardião"})
	response := httptest.NewRecorder()
	handler(response, request.WithContext(ctx))
	return response
}

type guardianDependentStoreFake struct {
	input  GuardianDependentInput
	err    error
	called bool
}

func (s *guardianDependentStoreFake) CreateDependent(_ context.Context, input GuardianDependentInput) error {
	s.called = true
	s.input = input
	return s.err
}

type guardianDashboardStore struct {
	dependents []dbgen.ListDependentsByGuardianRow
	err        error
}

func (s *guardianDashboardStore) ListRecentPerformanceMetrics(context.Context, dbgen.ListRecentPerformanceMetricsParams) ([]dbgen.PerformanceMetric, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListRecentTrainingLogs(context.Context, dbgen.ListRecentTrainingLogsParams) ([]dbgen.TrainingLog, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListPublishedNews(context.Context, int32) ([]dbgen.NewsItem, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListWhatsAppGroupsForUserProgramme(context.Context, dbgen.ListWhatsAppGroupsForUserProgrammeParams) ([]dbgen.WhatsappGroup, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListDependentsByGuardian(context.Context, dbgen.ListDependentsByGuardianParams) ([]dbgen.ListDependentsByGuardianRow, error) {
	return s.dependents, s.err
}
func (s *guardianDashboardStore) ListOperationalEquipment(context.Context, int32) ([]dbgen.Equipment, error) {
	return nil, nil
}
func (s *guardianDashboardStore) ListEventsForMember(context.Context, dbgen.ListEventsForMemberParams) ([]dbgen.ListEventsForMemberRow, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListUpcomingTrainingSessionsForDashboard(context.Context, dbgen.ListUpcomingTrainingSessionsForDashboardParams) ([]dbgen.ListUpcomingTrainingSessionsForDashboardRow, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListEventsForToday(context.Context, dbgen.ListEventsForTodayParams) ([]dbgen.ListEventsForTodayRow, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListVisibleAnnouncements(context.Context, dbgen.ListVisibleAnnouncementsParams) ([]dbgen.ListVisibleAnnouncementsRow, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListDistanceLeaderboard(context.Context, dbgen.ListDistanceLeaderboardParams) ([]dbgen.ListDistanceLeaderboardRow, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) UpdateOwnLeaderboardVisibility(context.Context, dbgen.UpdateOwnLeaderboardVisibilityParams) (int64, error) {
	return 0, errors.New("not used")
}
func (s *guardianDashboardStore) UpdateDependentLeaderboardVisibility(context.Context, dbgen.UpdateDependentLeaderboardVisibilityParams) (int64, error) {
	return 0, errors.New("not used")
}
