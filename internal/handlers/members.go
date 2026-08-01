package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

const membersPageSize = 6

type MemberStore interface {
	ListMembersForAdmin(context.Context, dbgen.ListMembersForAdminParams) ([]dbgen.ListMembersForAdminRow, error)
	GetMemberForAdmin(context.Context, uuid.UUID) (dbgen.GetMemberForAdminRow, error)
	ListActiveAdultsForAdmin(context.Context, int32) ([]dbgen.ListActiveAdultsForAdminRow, error)
	CreateAdultUser(context.Context, dbgen.CreateAdultUserParams) (dbgen.CreateAdultUserRow, error)
	CreateDependentUser(context.Context, dbgen.CreateDependentUserParams) (dbgen.CreateDependentUserRow, error)
	DeactivateUser(context.Context, uuid.UUID) error
	GetCurrentSeason(context.Context) (dbgen.Season, error)
	CreateSeason(context.Context, dbgen.CreateSeasonParams) (dbgen.Season, error)
	ListMembershipProgrammes(context.Context) ([]dbgen.Programme, error)
	ListActiveMembershipsForUser(context.Context, uuid.UUID) ([]dbgen.ListActiveMembershipsForUserRow, error)
	UpsertCurrentSeasonMembership(context.Context, dbgen.UpsertCurrentSeasonMembershipParams) (dbgen.UserMembership, error)
	EndCurrentSeasonMembership(context.Context, dbgen.EndCurrentSeasonMembershipParams) (int64, error)
	IssueMinorCredential(context.Context, dbgen.IssueMinorCredentialParams) (uuid.UUID, error)
}

type Members struct {
	Store    MemberStore
	System   System
	PageMeta components.PageMeta
	Location *time.Location
	Now      func() time.Time
	Sessions *scs.SessionManager
}

type memberForm struct {
	Name, Email, DateOfBirth, GuardianID, Password, PasswordConfirmation string
	Dependent                                                            bool
	Errors                                                               validation.FieldErrors
}

func (h Members) Index(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	var filter *string
	if search != "" {
		filter = &search
	}
	pageNumber := membersPageNumber(r.URL.Query().Get("page"))
	members, err := h.Store.ListMembersForAdmin(ctx, dbgen.ListMembersForAdminParams{Search: filter, RowLimit: membersPageSize + 1, RowOffset: int32((pageNumber - 1) * membersPageSize)})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	adults, err := h.Store.ListActiveAdultsForAdmin(ctx, 500)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	page := pages.MembersPage{Search: search, Form: h.memberFormFromAdults(r, memberForm{Errors: validation.FieldErrors{}}, adults), Meta: h.meta(r, "Gestão de membros", "/admin/membros")}
	if h.Sessions != nil {
		page.Success = h.Sessions.PopString(r.Context(), "members_flash")
	}
	if pageNumber > 1 {
		page.PreviousURL = membersPageURL(search, pageNumber-1)
	}
	if len(members) > membersPageSize {
		page.NextURL = membersPageURL(search, pageNumber+1)
		members = members[:membersPageSize]
	}
	for _, member := range members {
		page.Members = append(page.Members, pages.MemberListItem{ID: member.ID.String(), Name: member.Name, Email: stringValue(member.Email), LoginID: stringValue(member.MinorLoginID), Dependent: member.IsDependent, Active: member.IsActive})
	}
	h.render(w, r, http.StatusOK, page)
}

func membersPageNumber(value string) int {
	page, err := strconv.Atoi(value)
	if err != nil || page < 1 || page > 10000 {
		return 1
	}
	return page
}

func membersPageURL(search string, page int) string {
	query := url.Values{"page": {strconv.Itoa(page)}}
	if search != "" {
		query.Set("q", search)
	}
	return "/admin/membros?" + query.Encode()
}

func (h Members) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.Index(w, r)
		return
	}
	form := h.validateCreate(r)
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	if form.Dependent && !form.Errors.Has("guardian_id") {
		guardianID, _ := uuid.Parse(form.GuardianID)
		guardian, err := h.Store.GetMemberForAdmin(ctx, guardianID)
		if err != nil || guardian.IsDependent || !guardian.IsActive {
			form.Errors.Add("guardian_id", "Selecione um tutor ativo.")
		}
	}
	if !form.Errors.Empty() {
		h.renderCreateError(w, r, form)
		return
	}
	if form.Dependent {
		guardianID, _ := uuid.Parse(form.GuardianID)
		_, err := h.Store.CreateDependentUser(ctx, dbgen.CreateDependentUserParams{Name: form.Name, GuardianID: &guardianID, DateOfBirth: pgtype.Date{Time: parseMemberDate(form.DateOfBirth), Valid: true}})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
	} else {
		hash, err := bcrypt.GenerateFromPassword([]byte(form.Password), 12)
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		_, err = h.Store.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: form.Name, Email: &form.Email, PasswordHash: stringPtr(string(hash)), DateOfBirth: pgtype.Date{Time: parseMemberDate(form.DateOfBirth), Valid: true}})
		if isUniqueViolation(err) {
			form.Errors.Add("email", duplicateEmailMessage)
			h.renderCreateError(w, r, form)
			return
		}
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "members_flash", "Conta criada.")
	}
	httpx.Redirect(w, r, "/admin/membros", http.StatusSeeOther)
}

func (h Members) Detail(w http.ResponseWriter, r *http.Request) {
	id, ok := h.memberID(w, r)
	if !ok {
		return
	}
	h.renderDetail(w, r, id, http.StatusOK, validation.FieldErrors{}, "")
}

func (h Members) Membership(w http.ResponseWriter, r *http.Request) {
	id, ok := h.memberID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	programmeID, err := uuid.Parse(r.PostForm.Get("programme_id"))
	if err != nil {
		h.renderDetail(w, r, id, http.StatusUnprocessableEntity, validation.FieldErrors{"membership": "Selecione um programa válido."}, "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	season, err := h.currentSeason(ctx)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	programmes, err := h.Store.ListMembershipProgrammes(ctx)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	valid := false
	for _, programme := range programmes {
		if programme.ID == programmeID {
			valid = true
		}
	}
	if !valid {
		h.renderDetail(w, r, id, http.StatusUnprocessableEntity, validation.FieldErrors{"membership": "Selecione um programa válido."}, "")
		return
	}
	if r.PostForm.Get("active") == "on" {
		_, err = h.Store.UpsertCurrentSeasonMembership(ctx, dbgen.UpsertCurrentSeasonMembershipParams{UserID: id, SeasonID: season.ID, ProgrammeID: programmeID, StartsOn: pgtype.Date{Time: h.today(), Valid: true}})
	} else {
		_, err = h.Store.EndCurrentSeasonMembership(ctx, dbgen.EndCurrentSeasonMembershipParams{UserID: id, SeasonID: season.ID, ProgrammeID: programmeID})
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	httpx.Redirect(w, r, "/admin/membros/"+id.String(), http.StatusSeeOther)
}

func (h Members) Deactivate(w http.ResponseWriter, r *http.Request) {
	id, ok := h.memberID(w, r)
	if !ok {
		return
	}
	actor, _ := CurrentUserFromContext(r.Context())
	if id == actor.ID {
		h.System.Forbidden(w, r)
		return
	}
	if !deactivationConfirmed(r) {
		h.renderDetail(w, r, id, http.StatusUnprocessableEntity, validation.FieldErrors{"deactivation": "Confirme que pretende desativar esta conta."}, "")
		return
	}
	if err := h.Store.DeactivateUser(r.Context(), id); err != nil {
		h.System.InternalError(w, r)
		return
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "member_detail_flash", "Conta desativada.")
	}
	httpx.Redirect(w, r, "/admin/membros/"+id.String(), http.StatusSeeOther)
}

func deactivationConfirmed(r *http.Request) bool {
	return r.ParseForm() == nil && r.PostForm.Get("confirm_deactivation") == "yes"
}

func (h Members) IssueMinorCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := h.memberID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	password := r.PostForm.Get("password")
	fieldErrors := validation.FieldErrors{}
	if err := validation.ValidatePassword(password); err != nil {
		fieldErrors.Add("password", err.Error())
	}
	if password != r.PostForm.Get("password_confirmation") {
		fieldErrors.Add("password_confirmation", "As palavras-passe não coincidem.")
	}
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	member, err := h.Store.GetMemberForAdmin(ctx, id)
	if err != nil || !member.IsDependent || !member.IsActive || member.GuardianID == nil {
		h.System.NotFound(w, r)
		return
	}
	if !fieldErrors.Empty() {
		h.renderDetail(w, r, id, http.StatusUnprocessableEntity, fieldErrors, "")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	loginID := minorLoginID(id)
	action := "ISSUED"
	if member.MinorLoginID != nil {
		action = "RECOVERED"
	}
	actor, _ := CurrentUserFromContext(r.Context())
	_, err = h.Store.IssueMinorCredential(ctx, dbgen.IssueMinorCredentialParams{MinorLoginID: &loginID, PasswordHash: stringPtr(string(hash)), MinorUserID: id, GuardianUserID: *member.GuardianID, ActorUserID: actor.ID, Action: action})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	httpx.Redirect(w, r, "/admin/membros/"+id.String(), http.StatusSeeOther)
}

func (h Members) validateCreate(r *http.Request) memberForm {
	f := memberForm{Dependent: r.PostForm.Get("account_type") == "dependent", Name: strings.TrimSpace(r.PostForm.Get("name")), Email: strings.TrimSpace(r.PostForm.Get("email")), DateOfBirth: strings.TrimSpace(r.PostForm.Get("date_of_birth")), GuardianID: strings.TrimSpace(r.PostForm.Get("guardian_id")), Password: r.PostForm.Get("password"), PasswordConfirmation: r.PostForm.Get("password_confirmation"), Errors: validation.FieldErrors{}}
	name, err := validation.NormalizeName(f.Name)
	if err != nil {
		f.Errors.Add("name", err.Error())
	} else {
		f.Name = name
	}
	birth, err := validation.ParseISODate(f.DateOfBirth)
	if err != nil {
		f.Errors.Add("date_of_birth", err.Error())
	} else if f.Dependent {
		if err := validation.ValidateDependentDateOfBirth(birth, h.now(), h.location()); err != nil {
			f.Errors.Add("date_of_birth", err.Error())
		}
	} else if err := validation.ValidateAdultDateOfBirth(birth, h.now(), h.location()); err != nil {
		f.Errors.Add("date_of_birth", err.Error())
	}
	if f.Dependent {
		if _, err := uuid.Parse(f.GuardianID); err != nil {
			f.Errors.Add("guardian_id", "Selecione um tutor válido.")
		}
		return f
	}
	email, err := validation.NormalizeEmail(f.Email)
	if err != nil {
		f.Errors.Add("email", err.Error())
	} else {
		f.Email = email
	}
	if err := validation.ValidatePassword(f.Password); err != nil {
		f.Errors.Add("password", err.Error())
	}
	if f.Password != f.PasswordConfirmation {
		f.Errors.Add("password_confirmation", "As palavras-passe não coincidem.")
	}
	return f
}

func (h Members) renderCreateError(w http.ResponseWriter, r *http.Request, form memberForm) {
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	adults, err := h.Store.ListActiveAdultsForAdmin(ctx, 500)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	page := pages.MembersPage{Form: h.memberFormFromAdults(r, form, adults), Meta: h.meta(r, "Gestão de membros", "/admin/membros")}
	h.render(w, r, http.StatusUnprocessableEntity, page)
}

func (h Members) renderDetail(w http.ResponseWriter, r *http.Request, id uuid.UUID, status int, fieldErrors validation.FieldErrors, _ string) {
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	member, err := h.Store.GetMemberForAdmin(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	season, err := h.currentSeason(ctx)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	programmes, err := h.Store.ListMembershipProgrammes(ctx)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	active, err := h.Store.ListActiveMembershipsForUser(ctx, id)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	activeIDs := map[uuid.UUID]bool{}
	for _, membership := range active {
		if membership.SeasonID == season.ID {
			activeIDs[membership.ProgrammeID] = true
		}
	}
	meta := h.meta(r, "Membro", "/admin/membros")
	meta.PageLabel = "Detalhe do membro"
	meta.SubjectContext = member.Name
	page := pages.MemberDetailPage{Meta: meta, Member: pages.MemberDetail{ID: member.ID.String(), Name: member.Name, Email: stringValue(member.Email), LoginID: stringValue(member.MinorLoginID), Guardian: stringValue(member.GuardianName), Dependent: member.IsDependent, Active: member.IsActive}, Season: season.Name, Errors: fieldErrors}
	if h.Sessions != nil {
		page.Success = h.Sessions.PopString(r.Context(), "member_detail_flash")
	}
	for _, programme := range programmes {
		page.Programmes = append(page.Programmes, pages.MemberProgramme{ID: programme.ID.String(), Name: programme.NamePt, Active: activeIDs[programme.ID]})
	}
	h.renderDetailPage(w, r, status, page)
}

func (h Members) currentSeason(ctx context.Context) (dbgen.Season, error) {
	season, err := h.Store.GetCurrentSeason(ctx)
	if err == nil {
		return season, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Season{}, err
	}
	year := h.today().Year()
	season, err = h.Store.CreateSeason(ctx, dbgen.CreateSeasonParams{Code: fmt.Sprintf("%d", year), Name: fmt.Sprintf("Época %d", year), StartsOn: pgtype.Date{Time: time.Date(year, 1, 1, 0, 0, 0, 0, h.location()), Valid: true}, EndsOn: pgtype.Date{Time: time.Date(year, 12, 31, 0, 0, 0, 0, h.location()), Valid: true}, IsCurrent: true})
	if isUniqueViolation(err) {
		return h.Store.GetCurrentSeason(ctx)
	}
	return season, err
}
func (h Members) memberID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return uuid.Nil, false
	}
	return id, true
}
func (h Members) memberForm(r *http.Request, form memberForm) pages.MemberCreateForm {
	return h.memberFormFromAdults(r, form, nil)
}
func (h Members) memberFormFromAdults(r *http.Request, form memberForm, adults []dbgen.ListActiveAdultsForAdminRow) pages.MemberCreateForm {
	f := pages.MemberCreateForm{Name: form.Name, Email: form.Email, DateOfBirth: form.DateOfBirth, GuardianID: form.GuardianID, Dependent: form.Dependent, Errors: form.Errors, CSRFField: templ.Raw(string(csrf.TemplateField(r)))}
	for _, adult := range adults {
		f.Guardians = append(f.Guardians, pages.MemberGuardian{ID: adult.ID.String(), Name: adult.Name, Email: stringValue(adult.Email)})
	}
	return f
}
func (h Members) render(w http.ResponseWriter, r *http.Request, status int, page pages.MembersPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.Members(page).Render(r.Context(), w)
}
func (h Members) renderDetailPage(w http.ResponseWriter, r *http.Request, status int, page pages.MemberDetailPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.MemberDetailView(page).Render(r.Context(), w)
}
func (h Members) meta(r *http.Request, title, path string) components.PageMeta {
	user, _ := CurrentUserFromContext(r.Context())
	meta := h.PageMeta
	meta.Title = title + " | MyCFC"
	meta.CurrentPath = path
	meta.CurrentUserName = user.Name
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return meta
}
func (h Members) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}
func (h Members) today() time.Time {
	now := h.now().In(h.location())
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, h.location())
}
func (h Members) location() *time.Location {
	if h.Location != nil {
		return h.Location
	}
	return time.UTC
}
func parseMemberDate(value string) time.Time { date, _ := time.Parse("2006-01-02", value); return date }
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func stringPtr(value string) *string { return &value }
func minorLoginID(id uuid.UUID) string {
	return "CFC-" + strings.ToUpper(strings.ReplaceAll(id.String()[:8], "-", ""))
}
