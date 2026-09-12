package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
)

type guardianAgeHandoffStoreFake struct {
	item             GuardianAgeHandoff
	notices          []GuardianAgeHandoffNotice
	proposedEmail    string
	proposedVersion  int64
	verified         bool
	confirmed        bool
	recovered        bool
	recoveredVersion int64
	err              error
	confirmErr       error
	recoverErr       error
}

func (s *guardianAgeHandoffStoreFake) GetForSubject(context.Context, uuid.UUID) (GuardianAgeHandoff, error) {
	return s.item, s.err
}
func (s *guardianAgeHandoffStoreFake) ListNoticesForSubject(context.Context, uuid.UUID) ([]GuardianAgeHandoffNotice, error) {
	return s.notices, s.err
}
func (s *guardianAgeHandoffStoreFake) ProposeEmail(_ context.Context, _ uuid.UUID, version int64, email string, _ []byte, _ time.Time, _ []byte) (GuardianAgeHandoff, error) {
	s.proposedEmail = email
	s.proposedVersion = version
	return s.item, s.err
}
func (s *guardianAgeHandoffStoreFake) VerifyEmail(context.Context, []byte) error {
	s.verified = true
	return s.err
}
func (s *guardianAgeHandoffStoreFake) ListForAdmin(context.Context, uuid.UUID, int32, int32) ([]GuardianAgeHandoff, error) {
	return []GuardianAgeHandoff{s.item}, s.err
}
func (s *guardianAgeHandoffStoreFake) GetForAdmin(context.Context, uuid.UUID, uuid.UUID) (GuardianAgeHandoff, error) {
	return s.item, s.err
}
func (s *guardianAgeHandoffStoreFake) ConfirmIdentity(context.Context, uuid.UUID, uuid.UUID, int64) (GuardianAgeHandoff, error) {
	s.confirmed = true
	return s.item, s.confirmErr
}
func (s *guardianAgeHandoffStoreFake) RecoverEmail(_ context.Context, _ uuid.UUID, _ uuid.UUID, version int64, email string, _ []byte, _ time.Time, _ []byte) (GuardianAgeHandoff, error) {
	s.recovered = true
	s.recoveredVersion = version
	s.proposedEmail = email
	return s.item, s.recoverErr
}

func TestGuardianAgeHandoffYoungPersonRendersAndProposesNormalizedEmail(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	actor := uuid.New()
	store := &guardianAgeHandoffStoreFake{item: GuardianAgeHandoff{Reference: uuid.New(), Status: "PENDING", Version: 1, Birthday: now.AddDate(0, 0, 30)}, notices: []GuardianAgeHandoffNotice{{Kind: "GUARDIAN_AGE_18_30_DAY", Birthday: now.AddDate(0, 0, 30)}}}
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianHandoffs = store
	dashboard.GuardianHandoffKey = []byte("0123456789abcdef0123456789abcdef")
	dashboard.GuardianHandoffBaseURL = "https://mycfc.example"
	dashboard.Now = func() time.Time { return now }
	request := httptest.NewRequest(http.MethodGet, "/transicao-18", nil)
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Jovem", IsDependent: true, HasAgeHandoff: true}))
	response := httptest.NewRecorder()
	dashboard.GuardianAgeHandoff(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Aviso de 30 dias") || !strings.Contains(response.Body.String(), "mesma conta") {
		t.Fatalf("response=%d %q", response.Code, response.Body.String())
	}

	form := url.Values{"version": {"1"}, "email": {" YOUNG@EXAMPLE.TEST "}}
	request = httptest.NewRequest(http.MethodPost, "/transicao-18/email", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Jovem", IsDependent: true, HasAgeHandoff: true}))
	response = httptest.NewRecorder()
	dashboard.ProposeGuardianAgeHandoffEmail(response, request)
	if response.Code != http.StatusSeeOther || store.proposedEmail != "young@example.test" || store.proposedVersion != 1 {
		t.Fatalf("status=%d email=%q version=%d", response.Code, store.proposedEmail, store.proposedVersion)
	}
}

func TestGuardianAgeHandoffAdminConfirmationUsesFreshRotatedAuthentication(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	actor, ref := uuid.New(), uuid.New()
	store := &guardianAgeHandoffStoreFake{item: GuardianAgeHandoff{Reference: ref, SubjectName: "Jovem", Status: "EMAIL_VERIFIED", Version: 3, Birthday: now.AddDate(0, 0, 7)}}
	dependents := &guardianDependentStoreFake{}
	dashboard := guardianDashboard(&guardianDashboardStore{}, dependents)
	dashboard.GuardianHandoffs = store
	dashboard.Now = func() time.Time { return now }
	sessions := scs.New()
	ctx, err := sessions.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err = sessions.RenewToken(ctx); err != nil {
		t.Fatal(err)
	}
	sessions.Put(ctx, "authenticated_at", now.Add(-2*time.Hour).Format(time.RFC3339Nano))
	oldToken := sessions.Token(ctx)
	dashboard.Sessions = sessions
	dependents.reauthErr = ErrGuardianAuthentication
	wrongForm := url.Values{"version": {"3"}, "confirmation": {"yes"}, "password": {"wrong"}}
	wrongRequest := httptest.NewRequest(http.MethodPost, "/admin/transicoes-18/"+ref.String()+"/confirmar", strings.NewReader(wrongForm.Encode()))
	wrongRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wrongRequest.SetPathValue("ref", ref.String())
	wrongContext := context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: actor, Name: "Admin", IsAdmin: true})
	wrongResponse := httptest.NewRecorder()
	dashboard.ConfirmGuardianAgeHandoffIdentity(wrongResponse, wrongRequest.WithContext(wrongContext))
	if wrongResponse.Code != http.StatusUnprocessableEntity || store.confirmed || len(dependents.reserveKinds) != 1 || dependents.reserveKinds[0] != "AUTH_INVALID" {
		t.Fatalf("wrong password status=%d confirmed=%v reserves=%v", wrongResponse.Code, store.confirmed, dependents.reserveKinds)
	}
	dependents.reauthErr = nil
	form := url.Values{"version": {"3"}, "confirmation": {"yes"}, "password": {"correct horse 7"}}
	request := httptest.NewRequest(http.MethodPost, "/admin/transicoes-18/"+ref.String()+"/confirmar", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetPathValue("ref", ref.String())
	ctx = context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: actor, Name: "Admin", IsAdmin: true})
	response := httptest.NewRecorder()
	dashboard.ConfirmGuardianAgeHandoffIdentity(response, request.WithContext(ctx))
	if response.Code != http.StatusSeeOther || !store.confirmed || dependents.reauthPassword != "correct horse 7" || sessions.Token(ctx) == oldToken || sessions.GetString(ctx, "authenticated_at") != now.Format(time.RFC3339Nano) {
		t.Fatalf("status=%d confirmed=%v password=%q rotated=%v", response.Code, store.confirmed, dependents.reauthPassword, sessions.Token(ctx) != oldToken)
	}
}

func TestRecoverGuardianAgeHandoffEmailRequiresFreshAuthenticationBeforeMutation(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	actor, ref := uuid.New(), uuid.New()

	tests := []struct {
		name       string
		password   string
		reauthErr  error
		reserveErr error
		wantStatus int
		wantCalled bool
	}{
		{name: "missing password", wantStatus: http.StatusUnprocessableEntity},
		{name: "wrong password", password: "wrong", reauthErr: ErrGuardianAuthentication, wantStatus: http.StatusUnprocessableEntity},
		{name: "invalid authentication limited", password: "wrong", reauthErr: ErrGuardianAuthentication, reserveErr: ErrGuardianApplicationLimited, wantStatus: http.StatusTooManyRequests},
		{name: "successful reauthentication", password: "correct horse 7", wantStatus: http.StatusSeeOther, wantCalled: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &guardianAgeHandoffStoreFake{item: GuardianAgeHandoff{Reference: ref, SubjectName: "Jovem", Status: "RECOVERY_REQUIRED", Version: 4, Birthday: now, IdentityConfirmedAt: &now, RecoveryRequiredAt: &now}}
			dependents := &guardianDependentStoreFake{reauthErr: tc.reauthErr, reserveErr: map[string]error{"AUTH_INVALID": tc.reserveErr}}
			dashboard := guardianDashboard(&guardianDashboardStore{}, dependents)
			dashboard.GuardianHandoffs = store
			dashboard.GuardianHandoffKey = []byte("0123456789abcdef0123456789abcdef")
			dashboard.GuardianHandoffBaseURL = "https://mycfc.example"
			dashboard.Now = func() time.Time { return now }
			sessions := scs.New()
			ctx, err := sessions.Load(context.Background(), "")
			if err != nil {
				t.Fatal(err)
			}
			if err = sessions.RenewToken(ctx); err != nil {
				t.Fatal(err)
			}
			sessions.Put(ctx, "authenticated_at", now.Add(-2*time.Hour).Format(time.RFC3339Nano))
			oldToken := sessions.Token(ctx)
			dashboard.Sessions = sessions

			form := url.Values{"version": {"4"}, "email": {" recovered@example.test "}}
			if tc.password != "" {
				form.Set("password", tc.password)
			}
			request := httptest.NewRequest(http.MethodPost, "/admin/transicoes-18/"+ref.String()+"/recuperar-email", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.SetPathValue("ref", ref.String())
			ctx = context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: actor, Name: "Admin", IsAdmin: true})
			response := httptest.NewRecorder()
			dashboard.RecoverGuardianAgeHandoffEmail(response, request.WithContext(ctx))
			if response.Code != tc.wantStatus || store.recovered != tc.wantCalled {
				t.Fatalf("status=%d recovered=%v body=%q", response.Code, store.recovered, response.Body.String())
			}
			if !tc.wantCalled {
				if len(dependents.reserveKinds) != 1 || dependents.reserveKinds[0] != "AUTH_INVALID" {
					t.Fatalf("authentication failures=%v", dependents.reserveKinds)
				}
				if store.proposedEmail != "" {
					t.Fatalf("store mutated before authentication: %q", store.proposedEmail)
				}
				return
			}
			if store.proposedEmail != "recovered@example.test" || store.recoveredVersion != 4 || dependents.reauthPassword != tc.password {
				t.Fatalf("email=%q version=%d password=%q", store.proposedEmail, store.recoveredVersion, dependents.reauthPassword)
			}
			if sessions.Token(ctx) == oldToken || sessions.GetString(ctx, "authenticated_at") != now.Format(time.RFC3339Nano) {
				t.Fatal("successful recovery reauthentication did not rotate and refresh the session")
			}
		})

	}
}

func TestRecoverGuardianAgeHandoffEmailRejectsStaleMutationSafely(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	actor, ref := uuid.New(), uuid.New()
	store := &guardianAgeHandoffStoreFake{item: GuardianAgeHandoff{Reference: ref, SubjectName: "Jovem", Status: "RECOVERY_REQUIRED", Version: 5, Birthday: now, IdentityConfirmedAt: &now, RecoveryRequiredAt: &now}, recoverErr: ErrGuardianAgeHandoffConflict}
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianHandoffs = store
	dashboard.GuardianHandoffKey = []byte("0123456789abcdef0123456789abcdef")
	dashboard.GuardianHandoffBaseURL = "https://mycfc.example"
	dashboard.Now = func() time.Time { return now }
	sessions := scs.New()
	ctx, err := sessions.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	sessions.Put(ctx, "authenticated_at", now.Format(time.RFC3339Nano))
	dashboard.Sessions = sessions
	form := url.Values{"version": {"4"}, "email": {"recovered@example.test"}}
	request := httptest.NewRequest(http.MethodPost, "/admin/transicoes-18/"+ref.String()+"/recuperar-email", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetPathValue("ref", ref.String())
	ctx = context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: actor, Name: "Admin", IsAdmin: true})
	response := httptest.NewRecorder()
	dashboard.RecoverGuardianAgeHandoffEmail(response, request.WithContext(ctx))
	if response.Code != http.StatusConflict || !store.recovered || !strings.Contains(response.Body.String(), `href="#email"`) || !strings.Contains(response.Body.String(), `id="email"`) || !strings.Contains(response.Body.String(), `aria-describedby="email-error"`) {
		t.Fatalf("status=%d recovered=%v body=%q", response.Code, store.recovered, response.Body.String())
	}
}

func TestRecoverGuardianAgeHandoffEmailForgedPreBirthdayRequestFailsClosed(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	actor, ref := uuid.New(), uuid.New()
	store := &guardianAgeHandoffStoreFake{item: GuardianAgeHandoff{Reference: ref, SubjectName: "Jovem", Status: "IDENTITY_CONFIRMED", Version: 2, Birthday: now.AddDate(0, 0, 20), IdentityConfirmedAt: &now}, recoverErr: ErrGuardianAgeHandoffForbidden}
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianHandoffs = store
	dashboard.GuardianHandoffKey = []byte("0123456789abcdef0123456789abcdef")
	dashboard.GuardianHandoffBaseURL = "https://mycfc.example"
	dashboard.Now = func() time.Time { return now }
	sessions := scs.New()
	ctx, err := sessions.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	sessions.Put(ctx, "authenticated_at", now.Format(time.RFC3339Nano))
	dashboard.Sessions = sessions
	form := url.Values{"version": {"2"}, "email": {"attacker@example.test"}}
	request := httptest.NewRequest(http.MethodPost, "/admin/transicoes-18/"+ref.String()+"/recuperar-email", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetPathValue("ref", ref.String())
	ctx = context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: actor, Name: "Admin", IsAdmin: true})
	response := httptest.NewRecorder()
	dashboard.RecoverGuardianAgeHandoffEmail(response, request.WithContext(ctx))
	if response.Code != http.StatusForbidden || !store.recovered {
		t.Fatalf("status=%d database-called=%v", response.Code, store.recovered)
	}
}

func TestGuardianAgeHandoffStatusLabelUsesExplicitPortugueseForEveryState(t *testing.T) {
	for state, want := range map[string]string{
		"PENDING": "A preparar", "EMAIL_PENDING": "Email por confirmar", "EMAIL_VERIFIED": "Email confirmado",
		"IDENTITY_CONFIRMED": "Identidade confirmada", "READY": "Pronta para a data de transição",
		"RECOVERY_REQUIRED": "Recuperação presencial necessária", "EMAIL_COLLISION": "Email em conflito — resolução administrativa",
		"COMPLETED": "Concluída", "FORGED": "Estado indisponível",
	} {
		if got := guardianAgeHandoffStatusLabel(state); got != want {
			t.Errorf("%s label=%q want=%q", state, got, want)
		}
	}
}

func TestGuardianAgeHandoffVerificationIsGenericAndOneUse(t *testing.T) {
	store := &guardianAgeHandoffStoreFake{}
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianHandoffs = store
	dashboard.GuardianHandoffKey = []byte("0123456789abcdef0123456789abcdef")
	token := make([]byte, 32)
	encoded := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" // 32 zero bytes in raw URL encoding.
	if len(token) != 32 {
		t.Fatal("invalid fixture")
	}
	response := httptest.NewRecorder()
	dashboard.VerifyGuardianAgeHandoffEmail(response, httptest.NewRequest(http.MethodGet, "/transicao-18/verificar?token="+encoded, nil))
	if response.Code != http.StatusOK || !store.verified || !strings.Contains(response.Body.String(), "email foi confirmado") {
		t.Fatalf("status=%d verified=%v body=%q", response.Code, store.verified, response.Body.String())
	}
	response = httptest.NewRecorder()
	dashboard.VerifyGuardianAgeHandoffEmail(response, httptest.NewRequest(http.MethodGet, "/transicao-18/verificar?token=invalid", nil))
	if response.Code != http.StatusUnprocessableEntity || strings.Contains(response.Body.String(), "young@example") {
		t.Fatalf("invalid response=%d %q", response.Code, response.Body.String())
	}
}
