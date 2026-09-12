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

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type guardianSQLFake struct {
	rowErr   error
	queryErr error
	execErr  error
	rowsErr  error
	rows     int
}

func (f *guardianSQLFake) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("UPDATE 1"), f.execErr
}

func (f *guardianSQLFake) Query(context.Context, string, ...any) (pgx.Rows, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return &guardianRowsFake{remaining: f.rows, err: f.rowsErr}, nil
}

func (f *guardianSQLFake) QueryRow(context.Context, string, ...any) pgx.Row {
	return guardianRowFake{err: f.rowErr}
}

type guardianRowsFake struct {
	pgx.Rows
	remaining int
	err       error
}

func (*guardianRowsFake) Close()       {}
func (r *guardianRowsFake) Err() error { return r.err }
func (r *guardianRowsFake) Next() bool {
	if r.remaining == 0 {
		return false
	}
	r.remaining--
	return true
}
func (r *guardianRowsFake) Scan(dest ...any) error { return guardianScan(dest, nil) }

type guardianRowFake struct{ err error }

func (r guardianRowFake) Scan(dest ...any) error { return guardianScan(dest, r.err) }

func guardianScan(dest []any, err error) error {
	if err != nil {
		return err
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	for _, item := range dest {
		switch value := item.(type) {
		case *uuid.UUID:
			*value = uuid.MustParse("10000000-0000-0000-0000-000000000001")
		case **uuid.UUID:
			id := uuid.MustParse("10000000-0000-0000-0000-000000000002")
			*value = &id
		case *string:
			*value = "TEST_EVIDENCE"
		case **string:
			text := "value"
			*value = &text
		case *int64:
			*value = 1
		case *int32:
			*value = 1
		case *bool:
			*value = true
		case *any:
			*value = "member@example.test"
		case *pgtype.Date:
			*value = pgtype.Date{Time: now, Valid: true}
		case *pgtype.Timestamptz:
			*value = pgtype.Timestamptz{Time: now, Valid: true}
		case *pgtype.Text:
			*value = pgtype.Text{String: "value", Valid: true}
		}
	}
	return nil
}

func TestPostgresGuardianAgeHandoffStoreMapsEveryOperation(t *testing.T) {
	database := &guardianSQLFake{rows: 1}
	store := PostgresGuardianAgeHandoffStore{DB: database}
	actor, ref := uuid.New(), uuid.New()
	now := time.Now().UTC()

	if item, err := store.GetForSubject(t.Context(), actor); err != nil || item.Reference == uuid.Nil {
		t.Fatalf("subject item=%#v error=%v", item, err)
	}
	if notices, err := store.ListNoticesForSubject(t.Context(), actor); err != nil || len(notices) != 1 {
		t.Fatalf("notices=%#v error=%v", notices, err)
	}
	if item, err := store.ProposeEmail(t.Context(), actor, 1, "member@example.test", []byte("digest"), now, []byte("sealed")); err != nil || item.Reference == uuid.Nil {
		t.Fatalf("proposed item=%#v error=%v", item, err)
	}
	if err := store.VerifyEmail(t.Context(), []byte("digest")); err != nil {
		t.Fatal(err)
	}
	if items, err := store.ListForAdmin(t.Context(), actor, 100, 0); err != nil || len(items) != 1 {
		t.Fatalf("admin items=%#v error=%v", items, err)
	}
	if item, err := store.GetForAdmin(t.Context(), actor, ref); err != nil || item.Reference == uuid.Nil {
		t.Fatalf("admin item=%#v error=%v", item, err)
	}
	if item, err := store.ConfirmIdentity(t.Context(), actor, ref, 1); err != nil || item.Reference == uuid.Nil {
		t.Fatalf("confirmed item=%#v error=%v", item, err)
	}
	if item, err := store.RecoverEmail(t.Context(), actor, ref, 1, "member@example.test", []byte("digest"), now, []byte("sealed")); err != nil || item.Reference == uuid.Nil {
		t.Fatalf("recovered item=%#v error=%v", item, err)
	}
}

func TestGuardianAgeHandoffStoreMapsDatabaseErrorsAndRepresentations(t *testing.T) {
	for message, want := range map[string]error{
		"guardian_age_handoff_stale":                  ErrGuardianAgeHandoffConflict,
		"guardian_age_handoff_administrator_required": ErrGuardianAgeHandoffForbidden,
		"guardian_age_handoff_separation_required":    ErrGuardianAgeHandoffForbidden,
		"guardian_age_handoff_recovery_rejected":      ErrGuardianAgeHandoffForbidden,
		"guardian_age_handoff_email_collision":        ErrGuardianAgeHandoffCollision,
		"guardian_age_handoff_not_available":          ErrGuardianAgeHandoffInvalid,
		"guardian_age_handoff_token_invalid":          ErrGuardianAgeHandoffInvalid,
		"guardian_age_handoff_transition_rejected":    ErrGuardianAgeHandoffInvalid,
	} {
		if got := guardianAgeHandoffStoreError(&pgconn.PgError{Message: message}); !errors.Is(got, want) {
			t.Errorf("message=%s error=%v", message, got)
		}
	}
	want := errors.New("database unavailable")
	if got := guardianAgeHandoffStoreError(want); !errors.Is(got, want) {
		t.Fatalf("plain error=%v", got)
	}
	if guardianAgeHandoffStoreError(nil) != nil {
		t.Fatal("nil error changed")
	}
	if got := guardianAgeHandoffString("email@example.test"); got != "email@example.test" {
		t.Fatalf("string=%q", got)
	}
	if got := guardianAgeHandoffString([]byte("bytes@example.test")); got != "bytes@example.test" {
		t.Fatalf("bytes=%q", got)
	}
	if got := guardianAgeHandoffString(42); got != "" {
		t.Fatalf("unexpected representation=%q", got)
	}

	database := &guardianSQLFake{queryErr: want, rowErr: want}
	store := PostgresGuardianAgeHandoffStore{DB: database}
	if _, err := store.GetForSubject(t.Context(), uuid.New()); !errors.Is(err, want) {
		t.Fatalf("subject error=%v", err)
	}
	if _, err := store.ListForAdmin(t.Context(), uuid.New(), 1, 0); !errors.Is(err, want) {
		t.Fatalf("list error=%v", err)
	}
}

func TestPostgresGuardianAuthorityStoreMapsListsAndMutations(t *testing.T) {
	database := &guardianSQLFake{rows: 1}
	store := PostgresGuardianAuthorityStore{DB: database}
	actor, ref := uuid.New(), uuid.New()

	if available, err := store.PolicyAvailable(t.Context()); err != nil || !available {
		t.Fatalf("available=%v error=%v", available, err)
	}
	if values, err := store.EvidenceTypes(t.Context()); err != nil || len(values) != 1 || values[0].Label == "" {
		t.Fatalf("evidence=%#v error=%v", values, err)
	}
	if values, err := store.ReasonCodes(t.Context()); err != nil || len(values) != 1 || values[0].Label == "" {
		t.Fatalf("reasons=%#v error=%v", values, err)
	}
	if values, err := store.ListForGuardian(t.Context(), actor, 10); err != nil || len(values) != 1 {
		t.Fatalf("guardian relationships=%#v error=%v", values, err)
	}
	if values, err := store.ListPending(t.Context(), actor, 10, 0); err != nil || len(values) != 1 {
		t.Fatalf("pending relationships=%#v error=%v", values, err)
	}
	if value, err := store.GetForVerifier(t.Context(), ref, actor); err != nil || value.Reference == uuid.Nil {
		t.Fatalf("relationship=%#v error=%v", value, err)
	}
	if err := store.Transition(t.Context(), GuardianAuthorityTransitionInput{ActorID: actor, Reference: ref, ExpectedVersion: 1, Action: "APPROVE"}); err != nil {
		t.Fatal(err)
	}
	if value, err := store.IssueInvitation(t.Context(), actor, "member@example.test", []byte("digest")); err != nil || value.Reference == uuid.Nil {
		t.Fatalf("invitation=%#v error=%v", value, err)
	}
	if values, err := store.ListInvitations(t.Context(), actor, 10); err != nil || len(values) != 1 {
		t.Fatalf("invitations=%#v error=%v", values, err)
	}
	if err := store.RevokeInvitation(t.Context(), actor, ref); err != nil {
		t.Fatal(err)
	}
	if err := store.SubmitRenewal(t.Context(), actor, ref, 1, "NADA_MUDOU"); err != nil {
		t.Fatal(err)
	}
}

func TestGuardianStoresRejectMissingKeysAndInvitationDatabaseFailure(t *testing.T) {
	dependentStore := PostgresGuardianDependentStore{Key: []byte("short")}
	if err := dependentStore.ReserveAttempt(t.Context(), uuid.New(), nil, "SUBMISSION"); err == nil {
		t.Fatal("short application key accepted")
	}
	dependentStore.Pool = identityBeginnerFake{tx: &identityTransactionFake{}}
	if err := dependentStore.CreateDependent(t.Context(), GuardianDependentInput{GuardianID: uuid.New(), InvitationToken: "opaque"}); err == nil {
		t.Fatal("short invitation key accepted")
	}
	want := errors.New("database unavailable")
	if _, err := (PostgresGuardianAuthorityStore{DB: &guardianSQLFake{rowErr: want}}).IssueInvitation(t.Context(), uuid.New(), "member@example.test", []byte("digest")); !errors.Is(err, want) {
		t.Fatalf("issue invitation error=%v", err)
	}
}

func TestGuardianAuthorityStoreHelpersCoverNullsAndDatabaseCodes(t *testing.T) {
	now := time.Now().UTC()
	if optionalTime(pgtype.Timestamptz{}) != nil || optionalDate(pgtype.Date{}) != nil {
		t.Fatal("invalid nullable timestamps were materialized")
	}
	if got := optionalTime(pgtype.Timestamptz{Time: now, Valid: true}); got == nil || !got.Equal(now) {
		t.Fatalf("time=%v", got)
	}
	if got := optionalDate(pgtype.Date{Time: now, Valid: true}); got == nil || !got.Equal(now) {
		t.Fatalf("date=%v", got)
	}
	if got := guardianAuthorityCodeLabel("UNKNOWN_CODE"); got != "UNKNOWN CODE" {
		t.Fatalf("fallback label=%q", got)
	}
	for message, want := range map[string]error{
		"guardian_authority_limit_reached":             ErrMaximumDependents,
		"guardian_authority_applicant_ineligible":      ErrGuardianApplicantIneligible,
		"guardian_authority_invitation_invalid":        ErrGuardianInvitationInvalid,
		"guardian_application_rate_limited":            ErrGuardianApplicationLimited,
		"guardian_authority_policy_unavailable":        ErrGuardianAuthorityPolicyUnavailable,
		"guardian_authority_stale":                     ErrGuardianAuthorityConflict,
		"guardian_authority_verifier_required":         ErrGuardianAuthorityForbidden,
		"guardian_authority_transition_rejected":       ErrGuardianAuthorityInvalid,
		"guardian_authority_renewal_already_submitted": ErrGuardianAuthorityConflict,
		"guardian_authority_guardian_required":         ErrGuardianAuthorityForbidden,
	} {
		if got := guardianAuthorityStoreError(&pgconn.PgError{Message: message}); !errors.Is(got, want) {
			t.Errorf("message=%s error=%v", message, got)
		}
	}
	want := errors.New("database unavailable")
	if _, err := (PostgresGuardianAuthorityStore{DB: &guardianSQLFake{queryErr: want}}).ListInvitations(t.Context(), uuid.New(), 10); !errors.Is(err, want) {
		t.Fatalf("invitation list error=%v", err)
	}
	if err := (PostgresGuardianAuthorityStore{DB: &guardianSQLFake{rowErr: want}}).RevokeInvitation(t.Context(), uuid.New(), uuid.New()); !errors.Is(err, want) {
		t.Fatalf("invitation revoke error=%v", err)
	}
}

var _ dbgen.DBTX = (*guardianSQLFake)(nil)

type guardianHandoffBranchStore struct {
	item       GuardianAgeHandoff
	notices    []GuardianAgeHandoffNotice
	listErr    error
	getErr     error
	proposeErr error
	verifyErr  error
	confirmErr error
	recoverErr error
}

func (s *guardianHandoffBranchStore) GetForSubject(context.Context, uuid.UUID) (GuardianAgeHandoff, error) {
	return s.item, s.getErr
}
func (s *guardianHandoffBranchStore) ListNoticesForSubject(context.Context, uuid.UUID) ([]GuardianAgeHandoffNotice, error) {
	return s.notices, s.listErr
}
func (s *guardianHandoffBranchStore) ProposeEmail(context.Context, uuid.UUID, int64, string, []byte, time.Time, []byte) (GuardianAgeHandoff, error) {
	return s.item, s.proposeErr
}
func (s *guardianHandoffBranchStore) VerifyEmail(context.Context, []byte) error { return s.verifyErr }
func (s *guardianHandoffBranchStore) ListForAdmin(context.Context, uuid.UUID, int32, int32) ([]GuardianAgeHandoff, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return []GuardianAgeHandoff{s.item}, nil
}
func (s *guardianHandoffBranchStore) GetForAdmin(context.Context, uuid.UUID, uuid.UUID) (GuardianAgeHandoff, error) {
	return s.item, s.getErr
}
func (s *guardianHandoffBranchStore) ConfirmIdentity(context.Context, uuid.UUID, uuid.UUID, int64) (GuardianAgeHandoff, error) {
	return s.item, s.confirmErr
}
func (s *guardianHandoffBranchStore) RecoverEmail(context.Context, uuid.UUID, uuid.UUID, int64, string, []byte, time.Time, []byte) (GuardianAgeHandoff, error) {
	return s.item, s.recoverErr
}

func guardianHandoffBranchDashboard(store GuardianAgeHandoffStore) Dashboard {
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianHandoffs = store
	dashboard.GuardianHandoffKey = []byte("0123456789abcdef0123456789abcdef")
	dashboard.GuardianHandoffBaseURL = "https://mycfc.example"
	dashboard.Now = func() time.Time { return time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC) }
	return dashboard
}

func guardianHandoffRequest(method, target string, form url.Values, ref uuid.UUID) *http.Request {
	var body *strings.Reader
	if form == nil {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(form.Encode())
	}
	request := httptest.NewRequest(method, target, body)
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if ref != uuid.Nil {
		request.SetPathValue("ref", ref.String())
	}
	user := CurrentUser{ID: uuid.New(), Name: "Admin", IsAdmin: true, IsDependent: true, HasAgeHandoff: true}
	return request.WithContext(context.WithValue(request.Context(), currentUserKey{}, user))
}

func TestGuardianAgeHandoffAdminViewsCoverQueueAndDetailStates(t *testing.T) {
	ref := uuid.New()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	store := &guardianHandoffBranchStore{item: GuardianAgeHandoff{Reference: ref, SubjectName: "Jovem", Status: "EMAIL_COLLISION", Version: 4, Birthday: now, ProposedEmail: "young@example.test", EmailVerifiedAt: &now, IdentityConfirmedAt: &now, ReadyAt: &now, RecoveryRequiredAt: &now, CompletedAt: &now, PersonalInvolvement: true}}
	dashboard := guardianHandoffBranchDashboard(store)

	queueResponse := httptest.NewRecorder()
	dashboard.GuardianAgeHandoffAdminQueue(queueResponse, guardianHandoffRequest(http.MethodGet, "/admin/transicoes-18", nil, uuid.Nil))
	if queueResponse.Code != http.StatusOK || !strings.Contains(queueResponse.Body.String(), "Jovem") || !strings.Contains(queueResponse.Body.String(), "Email em conflito") {
		t.Fatalf("queue status=%d body=%q", queueResponse.Code, queueResponse.Body.String())
	}

	for _, success := range []string{"identity", "recovery"} {
		response := httptest.NewRecorder()
		request := guardianHandoffRequest(http.MethodGet, "/admin/transicoes-18/"+ref.String()+"?success="+success, nil, ref)
		dashboard.GuardianAgeHandoffAdminDetail(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Jovem") {
			t.Fatalf("detail success=%s status=%d body=%q", success, response.Code, response.Body.String())
		}
	}
}

func TestGuardianAgeHandoffHandlersCoverRejectedInputsAndStorageFailures(t *testing.T) {
	ref := uuid.New()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	baseItem := GuardianAgeHandoff{Reference: ref, SubjectName: "Jovem", Status: "PENDING", Version: 1, Birthday: now}

	t.Run("propose validation", func(t *testing.T) {
		store := &guardianHandoffBranchStore{item: baseItem}
		dashboard := guardianHandoffBranchDashboard(store)
		for _, form := range []url.Values{{"email": {"invalid"}, "version": {"1"}}, {"email": {"young@example.test"}, "version": {"bad"}}} {
			response := httptest.NewRecorder()
			dashboard.ProposeGuardianAgeHandoffEmail(response, guardianHandoffRequest(http.MethodPost, "/transicao-18/email", form, uuid.Nil))
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		}
	})

	for name, storeErr := range map[string]error{
		"conflict":  ErrGuardianAgeHandoffConflict,
		"collision": ErrGuardianAgeHandoffCollision,
		"invalid":   ErrGuardianAgeHandoffInvalid,
		"forbidden": ErrGuardianAgeHandoffForbidden,
		"database":  errors.New("database unavailable"),
	} {
		t.Run("propose "+name, func(t *testing.T) {
			store := &guardianHandoffBranchStore{item: baseItem, proposeErr: storeErr}
			dashboard := guardianHandoffBranchDashboard(store)
			response := httptest.NewRecorder()
			form := url.Values{"email": {"young@example.test"}, "version": {"1"}}
			dashboard.ProposeGuardianAgeHandoffEmail(response, guardianHandoffRequest(http.MethodPost, "/transicao-18/email", form, uuid.Nil))
			if response.Code < 400 {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}

	t.Run("verification dependencies and store errors", func(t *testing.T) {
		token := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		for _, tc := range []struct {
			key  []byte
			err  error
			want int
		}{{nil, nil, http.StatusInternalServerError}, {[]byte("0123456789abcdef0123456789abcdef"), pgx.ErrNoRows, http.StatusUnprocessableEntity}, {[]byte("0123456789abcdef0123456789abcdef"), errors.New("database unavailable"), http.StatusInternalServerError}} {
			store := &guardianHandoffBranchStore{item: baseItem, verifyErr: tc.err}
			dashboard := guardianHandoffBranchDashboard(store)
			dashboard.GuardianHandoffKey = tc.key
			response := httptest.NewRecorder()
			dashboard.VerifyGuardianAgeHandoffEmail(response, guardianHandoffRequest(http.MethodGet, "/transicao-18/verificar?token="+token, nil, uuid.Nil))
			if response.Code != tc.want {
				t.Fatalf("error=%v status=%d want=%d", tc.err, response.Code, tc.want)
			}
		}
	})
}

func TestGuardianAgeHandoffRenderingAndMutationErrorsFailClosed(t *testing.T) {
	ref := uuid.New()
	baseItem := GuardianAgeHandoff{Reference: ref, SubjectName: "Jovem", Status: "PENDING", Version: 1, Birthday: time.Now().UTC()}

	for name, store := range map[string]*guardianHandoffBranchStore{
		"queue list":      {item: baseItem, listErr: errors.New("database unavailable")},
		"detail missing":  {item: baseItem, getErr: pgx.ErrNoRows},
		"detail database": {item: baseItem, getErr: errors.New("database unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			dashboard := guardianHandoffBranchDashboard(store)
			response := httptest.NewRecorder()
			request := guardianHandoffRequest(http.MethodGet, "/admin/transicoes-18/"+ref.String(), nil, ref)
			if name == "queue list" {
				dashboard.GuardianAgeHandoffAdminQueue(response, request)
			} else {
				dashboard.GuardianAgeHandoffAdminDetail(response, request)
			}
			if response.Code < 400 {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}

	for name, mutationErr := range map[string]error{
		"conflict":  ErrGuardianAgeHandoffConflict,
		"forbidden": ErrGuardianAgeHandoffForbidden,
		"collision": ErrGuardianAgeHandoffCollision,
		"invalid":   ErrGuardianAgeHandoffInvalid,
		"database":  errors.New("database unavailable"),
	} {
		t.Run("mutation "+name, func(t *testing.T) {
			store := &guardianHandoffBranchStore{item: baseItem}
			dashboard := guardianHandoffBranchDashboard(store)
			response := httptest.NewRecorder()
			request := guardianHandoffRequest(http.MethodPost, "/admin/transicoes-18/"+ref.String(), nil, ref)
			if !dashboard.handleGuardianAgeHandoffMutationError(response, request, mutationErr, "email") || response.Code < 400 {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
	if guardianAgeHandoffNoticeLabel("GUARDIAN_AGE_18_7_DAY") != "Aviso de 7 dias" {
		t.Fatal("seven-day notice label changed")
	}
}

func TestGuardianAgeHandoffEntryPointsRejectMissingDependenciesAndMalformedRequests(t *testing.T) {
	ref := uuid.New()
	item := GuardianAgeHandoff{Reference: ref, SubjectName: "Jovem", Status: "PENDING", Version: 1, Birthday: time.Now().UTC()}
	validStore := &guardianHandoffBranchStore{item: item}

	for name, invoke := range map[string]func(Dashboard, *httptest.ResponseRecorder){
		"propose missing store": func(d Dashboard, w *httptest.ResponseRecorder) {
			d.GuardianHandoffs = nil
			d.ProposeGuardianAgeHandoffEmail(w, guardianHandoffRequest(http.MethodPost, "/transicao-18/email", url.Values{"email": {"young@example.test"}, "version": {"1"}}, uuid.Nil))
		},
		"admin queue missing store": func(d Dashboard, w *httptest.ResponseRecorder) {
			d.GuardianHandoffs = nil
			d.GuardianAgeHandoffAdminQueue(w, guardianHandoffRequest(http.MethodGet, "/admin/transicoes-18", nil, uuid.Nil))
		},
		"confirm missing store": func(d Dashboard, w *httptest.ResponseRecorder) {
			d.GuardianHandoffs = nil
			d.ConfirmGuardianAgeHandoffIdentity(w, guardianHandoffRequest(http.MethodPost, "/admin/transicoes-18/x/confirmar", url.Values{}, uuid.Nil))
		},
		"recover missing store": func(d Dashboard, w *httptest.ResponseRecorder) {
			d.GuardianHandoffs = nil
			d.RecoverGuardianAgeHandoffEmail(w, guardianHandoffRequest(http.MethodPost, "/admin/transicoes-18/x/recuperar-email", url.Values{}, uuid.Nil))
		},
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			invoke(guardianHandoffBranchDashboard(validStore), response)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}

	t.Run("confirm malformed reference", func(t *testing.T) {
		dashboard := guardianHandoffBranchDashboard(validStore)
		response := httptest.NewRecorder()
		request := guardianHandoffRequest(http.MethodPost, "/admin/transicoes-18/bad/confirmar", url.Values{"version": {"1"}, "confirmation": {"yes"}}, uuid.Nil)
		request.SetPathValue("ref", "bad")
		dashboard.ConfirmGuardianAgeHandoffIdentity(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("status=%d", response.Code)
		}
	})
	t.Run("confirm requires explicit consent", func(t *testing.T) {
		dashboard := guardianHandoffBranchDashboard(validStore)
		response := httptest.NewRecorder()
		request := guardianHandoffRequest(http.MethodPost, "/admin/transicoes-18/"+ref.String()+"/confirmar", url.Values{"version": {"bad"}}, ref)
		dashboard.ConfirmGuardianAgeHandoffIdentity(response, request)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status=%d", response.Code)
		}
	})
	t.Run("recover malformed reference and input", func(t *testing.T) {
		dashboard := guardianHandoffBranchDashboard(validStore)
		badRef := guardianHandoffRequest(http.MethodPost, "/admin/transicoes-18/bad/recuperar-email", url.Values{"version": {"1"}, "email": {"young@example.test"}}, uuid.Nil)
		badRef.SetPathValue("ref", "bad")
		response := httptest.NewRecorder()
		dashboard.RecoverGuardianAgeHandoffEmail(response, badRef)
		if response.Code != http.StatusNotFound {
			t.Fatalf("bad ref status=%d", response.Code)
		}
		response = httptest.NewRecorder()
		dashboard.RecoverGuardianAgeHandoffEmail(response, guardianHandoffRequest(http.MethodPost, "/admin/transicoes-18/"+ref.String()+"/recuperar-email", url.Values{"version": {"bad"}, "email": {"invalid"}}, ref))
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("bad input status=%d", response.Code)
		}
	})
}

func TestGuardianAgeHandoffSubjectRenderingMapsMissingAndFailedRecords(t *testing.T) {
	for name, store := range map[string]*guardianHandoffBranchStore{
		"missing":        {getErr: pgx.ErrNoRows},
		"get failure":    {getErr: errors.New("database unavailable")},
		"notice failure": {item: GuardianAgeHandoff{Reference: uuid.New(), Birthday: time.Now().UTC()}, listErr: errors.New("database unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			dashboard := guardianHandoffBranchDashboard(store)
			response := httptest.NewRecorder()
			dashboard.GuardianAgeHandoff(response, guardianHandoffRequest(http.MethodGet, "/transicao-18", nil, uuid.Nil))
			if response.Code < 400 {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
	dashboard := guardianHandoffBranchDashboard(&guardianHandoffBranchStore{})
	response := httptest.NewRecorder()
	request := guardianHandoffRequest(http.MethodGet, "/admin/transicoes-18/bad", nil, uuid.Nil)
	request.SetPathValue("ref", "bad")
	dashboard.GuardianAgeHandoffAdminDetail(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("invalid detail status=%d", response.Code)
	}
}
