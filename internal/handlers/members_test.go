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
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestDeactivationRequiresExplicitConfirmation(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{{"", false}, {"confirm_deactivation=no", false}, {"confirm_deactivation=yes", true}} {
		request := httptest.NewRequest(http.MethodPost, "/admin/membros/member/desativar", strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if got := deactivationConfirmed(request); got != tc.want {
			t.Errorf("deactivationConfirmed(%q) = %t, want %t", tc.body, got, tc.want)
		}
	}
}

func TestMemberCreateValidation(t *testing.T) {
	h := Members{Location: time.UTC, Now: func() time.Time { return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC) }}
	for _, tc := range []struct {
		name   string
		values url.Values
		field  string
	}{
		{"adult requires credentials", url.Values{"account_type": {"adult"}, "name": {"Ana Silva"}, "date_of_birth": {"1990-01-02"}, "email": {"ana@example.com"}}, "password"},
		{"dependent requires guardian", url.Values{"account_type": {"dependent"}, "name": {"João Silva"}, "date_of_birth": {"2014-01-02"}}, "guardian_id"},
		{"dependent accepts guardian without adult credentials", url.Values{"account_type": {"dependent"}, "name": {"João Silva"}, "date_of_birth": {"2014-01-02"}, "guardian_id": {"d4a3ea61-1176-4dc5-930e-b04ce610a1e7"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/admin/membros", strings.NewReader(tc.values.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			form := h.validateCreate(r)
			if tc.field == "" && !form.Errors.Empty() {
				t.Fatalf("errors = %#v", form.Errors)
			}
			if tc.field != "" && !form.Errors.Has(tc.field) {
				t.Fatalf("errors = %#v, want %q", form.Errors, tc.field)
			}
		})
	}
}

func TestMemberLoginID(t *testing.T) {
	id := uuid.MustParse("d4a3ea61-1176-4dc5-930e-b04ce610a1e7")
	if got, want := minorLoginID(id), "CFC-D4A3EA61"; got != want {
		t.Fatalf("login ID = %q, want %q", got, want)
	}
}

func TestCurrentSeasonUsesExistingCreatesMissingAndPropagatesFailures(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		lookupErr error
		createErr error
		wantErr   bool
		created   bool
	}{
		{name: "existing season"},
		{name: "missing season creates current year", lookupErr: pgx.ErrNoRows, created: true},
		{name: "lookup failure", lookupErr: errors.New("database unavailable"), wantErr: true},
		{name: "creation failure", lookupErr: pgx.ErrNoRows, createErr: errors.New("database unavailable"), wantErr: true, created: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &memberWorkflowStore{season: dbgen.Season{ID: uuid.New(), Name: "Existente"}, seasonErr: tc.lookupErr, createSeasonErr: tc.createErr}
			season, err := (Members{Store: store, Location: time.UTC, Now: func() time.Time { return now }}).currentSeason(t.Context())
			if (err != nil) != tc.wantErr {
				t.Fatalf("season=%#v error=%v", season, err)
			}
			if tc.created && (store.createdSeason.Code != "2026" || store.createdSeason.StartsOn.Time != time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
				t.Fatalf("created season=%#v", store.createdSeason)
			}
		})
	}
}

func TestMembersPageNumber(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{
		{"", 1},
		{"3", 3},
		{"0", 1},
		{"invalid", 1},
		{"10001", 1},
	} {
		if got := membersPageNumber(tc.value); got != tc.want {
			t.Errorf("membersPageNumber(%q) = %d, want %d", tc.value, got, tc.want)
		}
	}
}

func TestMembersPageURL(t *testing.T) {
	if got, want := membersPageURL("Ana & João", 2), "/admin/membros?page=2&q=Ana+%26+Jo%C3%A3o"; got != want {
		t.Errorf("membersPageURL() = %q, want %q", got, want)
	}
}

func TestMemberCreateTaskRouteRendersAndPreservesSafeValidationValues(t *testing.T) {
	h := Members{Store: memberStoreFake{}, Location: time.UTC, Now: func() time.Time { return time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC) }}
	returnURL := "/admin/membros?page=2&q=Ana#member-d4a3ea61-1176-4dc5-930e-b04ce610a1e7"
	taskURL := "/admin/membros/criar?return_to=" + url.QueryEscape(returnURL)

	getResponse := httptest.NewRecorder()
	h.CreatePage(getResponse, httptest.NewRequest(http.MethodGet, taskURL, nil))
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d", getResponse.Code)
	}
	if got := getResponse.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("GET Cache-Control = %q", got)
	}
	for _, expected := range []string{`class="task-surface task-surface--sheet task-surface--route"`, `action="` + taskURL + `"`, `href="/admin/membros?page=2&amp;q=Ana#member-d4a3ea61-1176-4dc5-930e-b04ce610a1e7"`, `data-task-form`} {
		if !strings.Contains(getResponse.Body.String(), expected) {
			t.Errorf("GET task does not contain %q", expected)
		}
	}

	values := url.Values{
		"account_type":          {"adult"},
		"name":                  {"Ana Segura"},
		"date_of_birth":         {"1990-01-02"},
		"email":                 {"ana.segura@example.test"},
		"password":              {"segredo temporario 7"},
		"password_confirmation": {"não coincide 8"},
	}
	request := httptest.NewRequest(http.MethodPost, taskURL, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	h.Create(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("422 Cache-Control = %q", got)
	}
	body := response.Body.String()
	for _, expected := range []string{`value="Ana Segura"`, `value="ana.segura@example.test"`, `value="1990-01-02"`, `class="error-summary"`, `href="#member-password-confirmation"`} {
		if !strings.Contains(body, expected) {
			t.Errorf("422 task does not contain %q", expected)
		}
	}
	for _, secret := range []string{"segredo temporario 7", "não coincide 8"} {
		if strings.Contains(body, secret) {
			t.Errorf("422 task exposed submitted password %q", secret)
		}
	}
}

func TestMemberCreateSuccessPreservesValidatedCollectionReturn(t *testing.T) {
	h := Members{Store: memberStoreFake{}, Location: time.UTC, Now: func() time.Time { return time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC) }}
	returnURL := "/admin/membros?page=2&q=Ana#member-d4a3ea61-1176-4dc5-930e-b04ce610a1e7"
	values := url.Values{"account_type": {"adult"}, "name": {"Ana Segura"}, "date_of_birth": {"1990-01-02"}, "email": {"ana.segura@example.test"}, "password": {"segredo temporario 7"}, "password_confirmation": {"segredo temporario 7"}}
	request := httptest.NewRequest(http.MethodPost, "/admin/membros/criar?return_to="+url.QueryEscape(returnURL), strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	h.Create(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != returnURL {
		t.Fatalf("response = %d %q", response.Code, response.Header().Get("Location"))
	}
}

func TestMemberWritesMapDuplicateAndUnexpectedPersistenceFailures(t *testing.T) {
	memberID, guardianID, actorID := uuid.New(), uuid.New(), uuid.New()
	adultValues := url.Values{"account_type": {"adult"}, "name": {"Ana Segura"}, "date_of_birth": {"1990-01-02"}, "email": {"ana.segura@example.test"}, "password": {"segredo temporario 7"}, "password_confirmation": {"segredo temporario 7"}}
	for _, tc := range []struct {
		name string
		err  error
		want int
		body string
	}{
		{name: "duplicate adult email", err: &pgconn.PgError{Code: "23505"}, want: http.StatusUnprocessableEntity, body: duplicateEmailMessage},
		{name: "adult write failure", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &memberWorkflowStore{createAdultErr: tc.err}
			request := httptest.NewRequest(http.MethodPost, "/admin/membros/criar", strings.NewReader(adultValues.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			(Members{Store: store, Location: time.UTC}).Create(response, request)
			if response.Code != tc.want || (tc.body != "" && !strings.Contains(response.Body.String(), tc.body)) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	member := dbgen.GetMemberForAdminRow{ID: memberID, IsDependent: true, IsActive: true, GuardianID: &guardianID}
	for _, tc := range []struct {
		name      string
		configure func(*memberWorkflowStore, error)
		path      string
		body      string
		want      int
	}{
		{name: "deactivation failure", configure: func(s *memberWorkflowStore, err error) { s.deactivateErr = err }, path: "/desativar", body: "confirm_deactivation=yes", want: http.StatusInternalServerError},
		{name: "credential issue failure", configure: func(s *memberWorkflowStore, err error) { s.credentialErr = err }, path: "/credencial-menor", body: "password=segredo+temporario+7&password_confirmation=segredo+temporario+7", want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &memberWorkflowStore{member: member}
			tc.configure(store, errors.New("database unavailable"))
			request := httptest.NewRequest(http.MethodPost, "/admin/membros/"+memberID.String()+tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.SetPathValue("id", memberID.String())
			request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
			response := httptest.NewRecorder()
			h := Members{Store: store, Location: time.UTC}
			if tc.path == "/desativar" {
				h.Deactivate(response, request)
			} else {
				h.IssueMinorCredential(response, request)
			}
			if response.Code != tc.want {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
}

func TestLegacyMemberCreatePostReturnsVisibleTaskResponse(t *testing.T) {
	h := Members{Store: memberStoreFake{}, Location: time.UTC, Now: func() time.Time { return time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC) }}
	request := httptest.NewRequest(http.MethodPost, "/admin/membros", strings.NewReader("account_type=adult"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	h.Create(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `task-surface--route`) || !strings.Contains(response.Body.String(), `<h1 id="member-create-title">Criar conta</h1>`) || strings.Contains(response.Body.String(), `class="collection-page"`) {
		t.Fatalf("legacy response = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("legacy 422 Cache-Control = %q", got)
	}
}

func TestMemberCreateTaskConflictContract(t *testing.T) {
	h := Members{Store: memberStoreFake{}, Location: time.UTC}
	request := httptest.NewRequest(http.MethodGet, "/admin/membros/criar", nil)
	response := httptest.NewRecorder()
	h.renderCreateTask(response, request, http.StatusConflict, memberForm{Errors: validation.FieldErrors{}}, "A informação usada por esta tarefa foi alterada. Atualize a lista e reveja os dados antes de tentar novamente.")
	if response.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d", response.Code)
	}
	for _, expected := range []string{`class="task-conflict"`, `role="alert"`, `tabindex="-1"`, `Atualizar a lista`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("conflict task does not contain %q", expected)
		}
	}
}

func TestMembersCollectionRendersClearSearchAndStableReturnContext(t *testing.T) {
	id := uuid.MustParse("d4a3ea61-1176-4dc5-930e-b04ce610a1e7")
	store := memberListStoreFake{rows: []dbgen.ListMembersForAdminRow{{ID: id, Name: "Leonor Rodrigues e Albuquerque", IsActive: true}}}
	h := Members{Store: store, Location: time.UTC}
	request := httptest.NewRequest(http.MethodGet, "/admin/membros?q=Leonor&page=2", nil)
	response := httptest.NewRecorder()
	h.Index(response, request)
	body := response.Body.String()
	for _, expected := range []string{`href="/admin/membros">Limpar pesquisa</a>`, `id="member-` + id.String() + `"`, `aria-describedby="members-comparison-help"`, `return_to=%2Fadmin%2Fmembros%3Fpage%3D2%26q%3DLeonor%23member-` + id.String()} {
		if !strings.Contains(body, expected) {
			t.Errorf("members response does not contain %q: %s", expected, body)
		}
	}
}

func TestMembersDirectoryFailsClosedWhenRequiredListsCannotLoad(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store memberListStoreFake
	}{
		{"member list", memberListStoreFake{listErr: errors.New("database unavailable")}},
		{"guardian selector", memberListStoreFake{adultsErr: errors.New("database unavailable")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := Members{Store: tc.store, Location: time.UTC}
			r := httptest.NewRequest(http.MethodGet, "/admin/membros?q=ana", nil)
			w := httptest.NewRecorder()
			h.Index(w, r)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("response=%d", w.Code)
			}
		})
	}
}

func TestMemberCollectionReturnSurvivesDetailMutation(t *testing.T) {
	id := uuid.MustParse("d4a3ea61-1176-4dc5-930e-b04ce610a1e7")
	returnURL := "/admin/membros?page=2&q=Ana#member-" + id.String()
	request := httptest.NewRequest(http.MethodPost, memberDetailPath(id, returnURL), nil)
	if got := memberCollectionReturn(request); got != returnURL {
		t.Fatalf("memberCollectionReturn() = %q, want %q", got, returnURL)
	}
}

func TestMemberMembershipDeactivationAndMinorCredentialPersistAuthorizedActions(t *testing.T) {
	memberID, programmeID, guardianID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	store := &memberWorkflowStore{season: dbgen.Season{ID: uuid.New()}, programmes: []dbgen.Programme{{ID: programmeID}}, member: dbgen.GetMemberForAdminRow{ID: memberID, IsDependent: true, IsActive: true, GuardianID: &guardianID}}
	h := Members{Store: store, Location: time.UTC, Now: func() time.Time { return time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC) }}
	actor := CurrentUser{ID: actorID, IsAdmin: true}

	membership := httptest.NewRequest(http.MethodPost, "/admin/membros/"+memberID.String()+"/inscricao", strings.NewReader("programme_id="+programmeID.String()+"&active=on"))
	membership.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	membership.SetPathValue("id", memberID.String())
	membershipResponse := httptest.NewRecorder()
	h.Membership(membershipResponse, membership)
	if membershipResponse.Code != http.StatusSeeOther || store.membership.UserID != memberID || store.membership.ProgrammeID != programmeID {
		t.Fatalf("membership=%d params=%+v", membershipResponse.Code, store.membership)
	}

	deactivate := httptest.NewRequest(http.MethodPost, "/admin/membros/"+memberID.String()+"/desativar", strings.NewReader("confirm_deactivation=yes"))
	deactivate.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	deactivate.SetPathValue("id", memberID.String())
	deactivate = deactivate.WithContext(context.WithValue(deactivate.Context(), currentUserKey{}, actor))
	deactivateResponse := httptest.NewRecorder()
	h.Deactivate(deactivateResponse, deactivate)
	if deactivateResponse.Code != http.StatusSeeOther || store.deactivated != memberID {
		t.Fatalf("deactivate=%d id=%s", deactivateResponse.Code, store.deactivated)
	}

	credential := httptest.NewRequest(http.MethodPost, "/admin/membros/"+memberID.String()+"/credencial-menor", strings.NewReader("password=segredo+temporario+7&password_confirmation=segredo+temporario+7"))
	credential.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	credential.SetPathValue("id", memberID.String())
	credential = credential.WithContext(context.WithValue(credential.Context(), currentUserKey{}, actor))
	credentialResponse := httptest.NewRecorder()
	h.IssueMinorCredential(credentialResponse, credential)
	if credentialResponse.Code != http.StatusSeeOther || store.credential.MinorUserID != memberID || store.credential.GuardianUserID != guardianID || store.credential.ActorUserID != actorID || store.credential.Action != "ISSUED" {
		t.Fatalf("credential=%d params=%+v", credentialResponse.Code, store.credential)
	}
}

func TestMemberDetailRendersAdministratorManagementView(t *testing.T) {
	memberID := uuid.New()
	h := Members{Store: memberStoreFake{}, Location: time.UTC}
	r := httptest.NewRequest(http.MethodGet, "/admin/membros/"+memberID.String(), nil)
	r.SetPathValue("id", memberID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Name: "Admin", IsAdmin: true, EmailVerified: true}))
	w := httptest.NewRecorder()
	h.Detail(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Detalhe do membro") {
		t.Fatalf("response=%d body=%s", w.Code, w.Body.String())
	}
}

func TestMemberMutationsRejectInvalidTargetsAndSelfDeactivation(t *testing.T) {
	memberID, actorID := uuid.New(), uuid.New()
	actor := CurrentUser{ID: actorID, IsAdmin: true}

	t.Run("membership rejects malformed and unavailable programmes", func(t *testing.T) {
		store := &memberWorkflowStore{season: dbgen.Season{ID: uuid.New()}, member: dbgen.GetMemberForAdminRow{ID: memberID, Name: "Ana", IsActive: true}}
		h := Members{Store: store, Location: time.UTC}
		for _, body := range []string{"programme_id=not-a-uuid", "programme_id=" + uuid.NewString()} {
			r := httptest.NewRequest(http.MethodPost, "/admin/membros/"+memberID.String()+"/inscricao", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.SetPathValue("id", memberID.String())
			w := httptest.NewRecorder()
			h.Membership(w, r)
			if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Selecione um programa válido") || store.membership.UserID != uuid.Nil {
				t.Fatalf("body=%q response=%d membership=%+v", body, w.Code, store.membership)
			}
		}
	})

	t.Run("administrator cannot deactivate themselves", func(t *testing.T) {
		h := Members{Store: &memberWorkflowStore{}, Location: time.UTC}
		r := httptest.NewRequest(http.MethodPost, "/admin/membros/"+actorID.String()+"/desativar", strings.NewReader("confirm_deactivation=yes"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", actorID.String())
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, actor))
		w := httptest.NewRecorder()
		h.Deactivate(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("response=%d", w.Code)
		}
	})

	t.Run("credentials are unavailable for adults and inactive dependants", func(t *testing.T) {
		for _, member := range []dbgen.GetMemberForAdminRow{{ID: memberID, IsActive: true}, {ID: memberID, IsDependent: true, IsActive: false, GuardianID: ptr(uuid.New())}} {
			h := Members{Store: &memberWorkflowStore{member: member}, Location: time.UTC}
			r := httptest.NewRequest(http.MethodPost, "/admin/membros/"+memberID.String()+"/credencial-menor", strings.NewReader("password=segredo+temporario+7&password_confirmation=segredo+temporario+7"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.SetPathValue("id", memberID.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, actor))
			w := httptest.NewRecorder()
			h.IssueMinorCredential(w, r)
			if w.Code != http.StatusNotFound {
				t.Fatalf("member=%+v response=%d", member, w.Code)
			}
		}
	})
}

func TestMemberMembershipMapsSeasonProgrammeAndWriteFailures(t *testing.T) {
	memberID, programmeID := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name          string
		seasonErr     error
		programmesErr error
		membershipErr error
	}{
		{name: "season lookup failure", seasonErr: errors.New("database unavailable")},
		{name: "programme lookup failure", programmesErr: errors.New("database unavailable")},
		{name: "membership write failure", membershipErr: errors.New("database unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &memberWorkflowStore{season: dbgen.Season{ID: uuid.New()}, programmes: []dbgen.Programme{{ID: programmeID}}, seasonErr: tc.seasonErr, programmesErr: tc.programmesErr, membershipErr: tc.membershipErr}
			request := httptest.NewRequest(http.MethodPost, "/admin/membros/"+memberID.String()+"/inscricao", strings.NewReader("programme_id="+programmeID.String()+"&active=on"))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.SetPathValue("id", memberID.String())
			response := httptest.NewRecorder()
			(Members{Store: store, Location: time.UTC}).Membership(response, request)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
}

type memberStoreFake struct{}

type memberWorkflowStore struct {
	memberStoreFake
	season             dbgen.Season
	programmes         []dbgen.Programme
	member             dbgen.GetMemberForAdminRow
	membership         dbgen.UpsertCurrentSeasonMembershipParams
	deactivated        uuid.UUID
	credential         dbgen.IssueMinorCredentialParams
	createAdultErr     error
	createDependentErr error
	deactivateErr      error
	credentialErr      error
	seasonErr          error
	createSeasonErr    error
	createdSeason      dbgen.CreateSeasonParams
	programmesErr      error
	membershipErr      error
}

func (s *memberWorkflowStore) GetCurrentSeason(context.Context) (dbgen.Season, error) {
	return s.season, s.seasonErr
}
func (s *memberWorkflowStore) CreateSeason(_ context.Context, params dbgen.CreateSeasonParams) (dbgen.Season, error) {
	s.createdSeason = params
	return dbgen.Season{ID: uuid.New(), Code: params.Code, Name: params.Name}, s.createSeasonErr
}
func (s *memberWorkflowStore) CreateAdultUser(context.Context, dbgen.CreateAdultUserParams) (dbgen.CreateAdultUserRow, error) {
	return dbgen.CreateAdultUserRow{}, s.createAdultErr
}
func (s *memberWorkflowStore) CreateDependentUser(context.Context, dbgen.CreateDependentUserParams) (dbgen.CreateDependentUserRow, error) {
	return dbgen.CreateDependentUserRow{}, s.createDependentErr
}
func (s *memberWorkflowStore) ListMembershipProgrammes(context.Context) ([]dbgen.Programme, error) {
	return s.programmes, s.programmesErr
}
func (s *memberWorkflowStore) UpsertCurrentSeasonMembership(_ context.Context, params dbgen.UpsertCurrentSeasonMembershipParams) (dbgen.UserMembership, error) {
	s.membership = params
	return dbgen.UserMembership{}, s.membershipErr
}
func (s *memberWorkflowStore) DeactivateUser(_ context.Context, id uuid.UUID) error {
	s.deactivated = id
	return s.deactivateErr
}
func (s *memberWorkflowStore) GetMemberForAdmin(context.Context, uuid.UUID) (dbgen.GetMemberForAdminRow, error) {
	return s.member, nil
}
func (s *memberWorkflowStore) IssueMinorCredential(_ context.Context, params dbgen.IssueMinorCredentialParams) (uuid.UUID, error) {
	s.credential = params
	return uuid.New(), s.credentialErr
}

type memberListStoreFake struct {
	memberStoreFake
	rows      []dbgen.ListMembersForAdminRow
	listErr   error
	adultsErr error
}

func (f memberListStoreFake) ListMembersForAdmin(context.Context, dbgen.ListMembersForAdminParams) ([]dbgen.ListMembersForAdminRow, error) {
	return f.rows, f.listErr
}

func (f memberListStoreFake) ListActiveAdultsForAdmin(context.Context, int32) ([]dbgen.ListActiveAdultsForAdminRow, error) {
	return nil, f.adultsErr
}

func (memberStoreFake) ListMembersForAdmin(context.Context, dbgen.ListMembersForAdminParams) ([]dbgen.ListMembersForAdminRow, error) {
	return nil, nil
}
func (memberStoreFake) GetMemberForAdmin(context.Context, uuid.UUID) (dbgen.GetMemberForAdminRow, error) {
	return dbgen.GetMemberForAdminRow{}, nil
}
func (memberStoreFake) ListActiveAdultsForAdmin(context.Context, int32) ([]dbgen.ListActiveAdultsForAdminRow, error) {
	return nil, nil
}
func (memberStoreFake) CreateAdultUser(context.Context, dbgen.CreateAdultUserParams) (dbgen.CreateAdultUserRow, error) {
	return dbgen.CreateAdultUserRow{}, nil
}
func (memberStoreFake) CreateDependentUser(context.Context, dbgen.CreateDependentUserParams) (dbgen.CreateDependentUserRow, error) {
	return dbgen.CreateDependentUserRow{}, nil
}
func (memberStoreFake) DeactivateUser(context.Context, uuid.UUID) error { return nil }
func (memberStoreFake) GetCurrentSeason(context.Context) (dbgen.Season, error) {
	return dbgen.Season{}, nil
}
func (memberStoreFake) CreateSeason(context.Context, dbgen.CreateSeasonParams) (dbgen.Season, error) {
	return dbgen.Season{}, nil
}
func (memberStoreFake) ListMembershipProgrammes(context.Context) ([]dbgen.Programme, error) {
	return nil, nil
}
func (memberStoreFake) ListActiveMembershipsForUser(context.Context, uuid.UUID) ([]dbgen.ListActiveMembershipsForUserRow, error) {
	return nil, nil
}
func (memberStoreFake) UpsertCurrentSeasonMembership(context.Context, dbgen.UpsertCurrentSeasonMembershipParams) (dbgen.UserMembership, error) {
	return dbgen.UserMembership{}, nil
}
func (memberStoreFake) EndCurrentSeasonMembership(context.Context, dbgen.EndCurrentSeasonMembershipParams) (int64, error) {
	return 0, nil
}
func (memberStoreFake) IssueMinorCredential(context.Context, dbgen.IssueMinorCredentialParams) (uuid.UUID, error) {
	return uuid.Nil, nil
}
