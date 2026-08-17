package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/google/uuid"
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

func TestMemberCollectionReturnSurvivesDetailMutation(t *testing.T) {
	id := uuid.MustParse("d4a3ea61-1176-4dc5-930e-b04ce610a1e7")
	returnURL := "/admin/membros?page=2&q=Ana#member-" + id.String()
	request := httptest.NewRequest(http.MethodPost, memberDetailPath(id, returnURL), nil)
	if got := memberCollectionReturn(request); got != returnURL {
		t.Fatalf("memberCollectionReturn() = %q, want %q", got, returnURL)
	}
}

type memberStoreFake struct{}

type memberListStoreFake struct {
	memberStoreFake
	rows []dbgen.ListMembersForAdminRow
}

func (f memberListStoreFake) ListMembersForAdmin(context.Context, dbgen.ListMembersForAdminParams) ([]dbgen.ListMembersForAdminRow, error) {
	return f.rows, nil
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
