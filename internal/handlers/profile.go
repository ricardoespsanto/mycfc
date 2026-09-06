package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	profilePhonePattern     = regexp.MustCompile(`^[+]?[0-9][0-9 ().-]*[0-9]$`)
	fpcAthleteNumberPattern = regexp.MustCompile(`^[0-9]{1,20}$`)
)

const (
	fpcNationalHistoryBase      = "https://www.fpcanoagem.pt/resultados/verhistorico/"
	fpcInternationalHistoryBase = "https://www.fpcanoagem.pt/resultados/verhistoricointernational/"
)

type Profile struct {
	Store                  ProfileStore
	Objects                storage.ObjectStore
	System                 System
	PageMeta               components.PageMeta
	Sessions               *scs.SessionManager
	Location               *time.Location
	Now                    func() time.Time
	MaxRequestBytes        int64
	MaxPhotoBytes          int64
	ImageVersion           string
	ImageSHA256            string
	ImageURL               string
	HealthVersion          string
	HealthSHA256           string
	HealthURL              string
	HealthConsentStatement string
	HTTPClient             *http.Client
}

func (h Profile) Get(w http.ResponseWriter, r *http.Request) {
	actor, _ := CurrentUserFromContext(r.Context())
	subjectID, base, ok := h.subject(w, r, actor)
	if !ok {
		return
	}
	record, err := h.view(r.Context(), actor, subjectID)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrProfileForbidden) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.render(w, r, http.StatusOK, h.page(r, actor, base, record, pages.ProfileForm{}, ""))
}

func (h Profile) Post(w http.ResponseWriter, r *http.Request) {
	actor, _ := CurrentUserFromContext(r.Context())
	subjectID, base, ok := h.subject(w, r, actor)
	if !ok {
		return
	}
	record, err := h.view(r.Context(), actor, subjectID)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrProfileForbidden) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !canEditProfile(record, actor.ID, actor.IsAdmin) {
		h.System.Forbidden(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	form, profileParams, identityParams := h.validateForm(r, record, actor.IsAdmin)
	if !form.Errors.Empty() {
		h.render(w, r, http.StatusUnprocessableEntity, h.page(r, actor, base, record, form, ""))
		return
	}
	profileParams.UserID = subjectID
	var ip *netip.Addr
	if address, found := httpx.RemoteIP(r.Context()); found {
		ip = &address
	}
	input := ProfileUpdate{ActorID: actor.ID, SubjectID: subjectID, IsAdmin: actor.IsAdmin, Profile: profileParams, Identity: identityParams, ChangedFields: profileChangedFields(record, profileParams), HealthVersion: h.HealthVersion, HealthSHA256: h.HealthSHA256, AcceptHealthConsent: form.HealthConsentAccepted, IP: ip, UserAgent: truncateRunes(r.UserAgent(), 512)}
	if identityParams != nil {
		input.IdentityFields = identityChangedFields(record, *identityParams)
	}
	err = h.Store.Update(r.Context(), input)
	if errors.Is(err, ErrHealthConsentRequired) {
		form.Errors.Add("accept_health_data", "Para guardar informação médica nova ou alterada, é necessário prestar consentimento explícito.")
		h.render(w, r, http.StatusUnprocessableEntity, h.page(r, actor, base, record, form, ""))
		return
	}
	if errors.Is(err, ErrProfileConflict) {
		h.render(w, r, http.StatusConflict, h.page(r, actor, base, record, form, "Outra pessoa alterou este perfil. Recarregue a página antes de guardar novamente."))
		return
	}
	if isUniqueViolation(err) {
		form.Errors.Add("official_identifiers", "O email ou um identificador oficial já pertence a outro membro.")
		h.render(w, r, http.StatusUnprocessableEntity, h.page(r, actor, base, record, form, ""))
		return
	}
	if errors.Is(err, ErrProfileForbidden) {
		h.System.Forbidden(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Perfil atualizado.")
	httpx.Redirect(w, r, profileActionPath(base, profileCollectionReturn(r, actor)), http.StatusSeeOther)
}

func (h Profile) UploadPhoto(w http.ResponseWriter, r *http.Request) {
	actor, _ := CurrentUserFromContext(r.Context())
	subjectID, base, ok := h.subject(w, r, actor)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.requestLimit())
	if err := r.ParseMultipartForm(h.photoLimit() + (1 << 20)); err != nil {
		h.photoError(w, r, actor, subjectID, base, "A fotografia excede o tamanho permitido ou o pedido é inválido.")
		return
	}
	header, err := singleProfilePhoto(r.MultipartForm)
	if err != nil || header == nil {
		h.photoError(w, r, actor, subjectID, base, "Selecione uma única fotografia JPEG, PNG ou WebP.")
		return
	}
	file, err := header.Open()
	if err != nil {
		h.photoError(w, r, actor, subjectID, base, "Não foi possível ler a fotografia.")
		return
	}
	photo, err := storage.ValidateRepairPhoto(file, h.photoLimit())
	_ = file.Close()
	if err != nil {
		h.photoError(w, r, actor, subjectID, base, err.Error()+" Selecione a imagem novamente.")
		return
	}
	if h.Objects == nil {
		h.System.InternalError(w, r)
		return
	}
	key := fmt.Sprintf("profiles/%s/%s.%s", h.now().In(h.location()).Format("2006/01"), uuid.New(), photo.Extension)
	uploadCtx := storage.WithUploadMetadata(r.Context(), storage.UploadMetadata{RequestID: httpx.RequestID(r.Context()), UserID: actor.ID.String()})
	if err := h.Objects.PutObject(uploadCtx, key, photo.ContentType, photo.Size, bytes.NewReader(photo.Bytes)); err != nil {
		h.System.InternalError(w, r)
		return
	}
	var ip = (*netip.Addr)(nil)
	if address, found := httpx.RemoteIP(r.Context()); found {
		ip = &address
	}
	userAgent := r.UserAgent()
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}
	oldKey, err := h.Store.SavePhoto(r.Context(), ProfilePhotoUpdate{ActorID: actor.ID, SubjectID: subjectID, IsAdmin: actor.IsAdmin, ObjectKey: key, ContentType: photo.ContentType, Size: photo.Size, ConsentVersion: h.ImageVersion, ConsentSHA256: h.ImageSHA256, AcceptConsent: r.MultipartForm.Value["accept_image_use"] != nil && r.MultipartForm.Value["accept_image_use"][0] == "yes", IP: ip, UserAgent: userAgent})
	if err != nil {
		h.deleteObject(r, &key)
		if errors.Is(err, ErrConsentRequired) {
			h.photoError(w, r, actor, subjectID, base, "É necessário aceitar o consentimento de uso de imagem atual antes de guardar a fotografia.")
			return
		}
		if errors.Is(err, ErrProfileForbidden) {
			h.System.Forbidden(w, r)
			return
		}
		h.System.InternalError(w, r)
		return
	}
	h.deleteObject(r, oldKey)
	h.flash(r, "Fotografia atualizada.")
	httpx.Redirect(w, r, profileActionPath(base, profileCollectionReturn(r, actor)), http.StatusSeeOther)
}

func (h Profile) RemovePhoto(w http.ResponseWriter, r *http.Request) {
	actor, _ := CurrentUserFromContext(r.Context())
	subjectID, base, ok := h.subject(w, r, actor)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil || r.PostForm.Get("confirm_removal") != "yes" {
		h.System.RequestRejected(w, r)
		return
	}
	oldKey, err := h.Store.RemovePhoto(r.Context(), actor.ID, subjectID, actor.IsAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if errors.Is(err, ErrProfileForbidden) {
		h.System.Forbidden(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.deleteObject(r, oldKey)
	h.flash(r, "Fotografia removida.")
	httpx.Redirect(w, r, profileActionPath(base, profileCollectionReturn(r, actor)), http.StatusSeeOther)
}

func (h Profile) RemovePhotoPage(w http.ResponseWriter, r *http.Request) {
	actor, _ := CurrentUserFromContext(r.Context())
	subjectID, base, ok := h.subject(w, r, actor)
	if !ok {
		return
	}
	record, err := h.view(r.Context(), actor, subjectID)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrProfileForbidden) || record.PhotoObjectKey == nil {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !canEditProfile(record, actor.ID, actor.IsAdmin) {
		h.System.Forbidden(w, r)
		return
	}
	meta := h.page(r, actor, base, record, pages.ProfileForm{}, "").Meta
	meta.Title = "Remover fotografia | MyCFCoimbra"
	meta.PageLabel = "Remover fotografia"
	returnURL := profileActionPath(base, profileCollectionReturn(r, actor))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pages.ProfilePhotoRemoval(pages.ProfilePhotoRemovalPage{Meta: meta, Name: record.Name, ActionPath: strings.Replace(returnURL, base, base+"/fotografia/remover", 1), ReturnURL: returnURL}).Render(r.Context(), w)
}

func (h Profile) Avatar(w http.ResponseWriter, r *http.Request) {
	actor, _ := CurrentUserFromContext(r.Context())
	subjectID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	avatar, err := h.Store.Avatar(r.Context(), dbgen.GetMemberAvatarParams{UserID: subjectID, IsAdmin: actor.IsAdmin, DocumentVersion: h.ImageVersion, DocumentSha256: h.ImageSHA256})
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if avatar.PhotoObjectKey == nil || avatar.PhotoContentType == nil || !avatar.ConsentCurrent {
		initials := profileInitialsText(avatar.Name)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 96 96" role="img" aria-label="Sem fotografia"><rect width="96" height="96" rx="48" fill="#dcefea"/><text x="48" y="58" text-anchor="middle" font-family="system-ui,sans-serif" font-size="30" font-weight="700" fill="#174f49">%s</text></svg>`, html.EscapeString(initials))
		return
	}
	if h.Objects == nil {
		h.System.InternalError(w, r)
		return
	}
	url, err := h.Objects.PresignGet(storage.WithPresignContentType(r.Context(), *avatar.PhotoContentType), *avatar.PhotoObjectKey, 5*time.Minute)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	client := h.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	response, err := client.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		if response != nil {
			_ = response.Body.Close()
		}
		h.System.InternalError(w, r)
		return
	}
	defer response.Body.Close()
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", *avatar.PhotoContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, io.LimitReader(response.Body, h.photoLimit()+1))
}

func (h Profile) view(parent context.Context, actor CurrentUser, subjectID uuid.UUID) (dbgen.GetMemberProfileRow, error) {
	ctx, cancel := context.WithTimeout(parent, dashboardQueryTimeout)
	defer cancel()
	return h.Store.View(ctx, actor.ID, subjectID, actor.IsAdmin)
}

func (h Profile) subject(w http.ResponseWriter, r *http.Request, actor CurrentUser) (uuid.UUID, string, bool) {
	if r.PathValue("id") == "" {
		return actor.ID, "/perfil", true
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return uuid.Nil, "", false
	}
	if strings.HasPrefix(r.URL.Path, "/admin/") {
		return id, "/admin/membros/" + id.String() + "/perfil", true
	}
	return id, "/perfil/dependentes/" + id.String(), true
}

func (h Profile) page(r *http.Request, actor CurrentUser, base string, record dbgen.GetMemberProfileRow, submitted pages.ProfileForm, conflict string) pages.ProfilePage {
	form := submitted
	if form.ProfileUpdatedAt == "" {
		form = profileFormFromRecord(record)
	}
	meta := h.PageMeta
	meta.Title = "Perfil de " + record.Name + " | MyCFCoimbra"
	meta.CurrentPath = base
	meta.CurrentUserName = actor.Name
	meta.CurrentUserID = actor.ID.String()
	meta.EmailVerificationPending = !actor.IsDependent && !actor.EmailVerified
	meta.Navigation = dashboardNavigation(actor)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	meta.PageLabel = "Perfil"
	switch {
	case strings.HasPrefix(base, "/admin/"):
		meta.AreaLabel = "Administração"
		returnURL := profileCollectionReturn(r, actor)
		meta.Breadcrumbs = []components.NavigationItem{{Label: "Membros", Path: returnURL}, {Label: record.Name, Path: memberDetailPath(record.ID, returnURL)}}
	case strings.HasPrefix(base, "/perfil/dependentes/"):
		meta.AreaLabel = "Família"
		meta.Breadcrumbs = []components.NavigationItem{{Label: "Menores a cargo", Path: "/dashboard/guardian"}}
	default:
		meta.AreaLabel = "Conta"
	}
	if record.ID != actor.ID {
		meta.SubjectContext = record.Name
	}
	avatar, avatarErr := h.Store.Avatar(r.Context(), dbgen.GetMemberAvatarParams{UserID: record.ID, IsAdmin: actor.IsAdmin, DocumentVersion: h.ImageVersion, DocumentSha256: h.ImageSHA256})
	visible := avatarErr == nil && avatar.PhotoObjectKey != nil && avatar.ConsentCurrent
	nationalHistoryURL, internationalHistoryURL := fpcHistoryURLs(stringValue(record.FederationLicenceNumber))
	returnURL := profileCollectionReturn(r, actor)
	page := pages.ProfilePage{Meta: meta, SubjectID: record.ID.String(), Name: record.Name, Email: stringValue(record.Email), LoginID: stringValue(record.MinorLoginID), DateOfBirth: record.DateOfBirth.Time.Format("2006-01-02"), Dependent: record.IsDependent, Active: record.IsActive, Editable: canEditProfile(record, actor.ID, actor.IsAdmin), Admin: actor.IsAdmin, Self: record.ID == actor.ID, Complete: profileComplete(record), HasPhoto: record.PhotoObjectKey != nil, PhotoVisible: visible, EmailVerified: record.EmailVerifiedAt.Valid, PhotoURL: "/membros/" + record.ID.String() + "/foto", BasePath: base, ActionPath: profileActionPath(base, returnURL), ReturnURL: returnURL, ImageConsentURL: h.ImageURL, HealthConsentURL: h.HealthURL, HealthConsentStatement: h.HealthConsentStatement, FPCNationalHistoryURL: nationalHistoryURL, FPCInternationalHistoryURL: internationalHistoryURL, Form: form, Conflict: conflict}
	if h.Sessions != nil {
		page.Success = h.Sessions.PopString(r.Context(), "profile_flash")
	}
	return page
}

func profileCollectionReturn(r *http.Request, actor CurrentUser) string {
	if !actor.IsAdmin {
		return ""
	}
	return memberCollectionReturn(r)
}

func profileActionPath(base, returnURL string) string {
	if returnURL == "" || returnURL == "/admin/membros" {
		return base
	}
	return base + "?return_to=" + url.QueryEscape(returnURL)
}

func (h Profile) validateForm(r *http.Request, current dbgen.GetMemberProfileRow, isAdmin bool) (pages.ProfileForm, dbgen.UpdateMemberProfileParams, *dbgen.UpdateMemberIdentityParams) {
	value := func(key string) string { return strings.TrimSpace(r.PostForm.Get(key)) }
	form := pages.ProfileForm{Phone: value("phone"), AddressLine1: value("address_line1"), AddressLine2: value("address_line2"), Postcode: value("postcode"), Locality: value("locality"), CountryCode: strings.ToUpper(value("country_code")), NationalityCode: strings.ToUpper(value("nationality_code")), ClubMemberNumber: value("club_member_number"), FederationLicenceNumber: value("federation_licence_number"), EmergencyName: value("emergency_contact_name"), EmergencyRelationship: value("emergency_contact_relationship"), EmergencyPhone: value("emergency_contact_phone"), EmergencyAlternatePhone: value("emergency_contact_alternate_phone"), MedicalDeclaration: value("medical_declaration"), Allergies: value("allergies"), MedicalConditions: value("medical_conditions"), Medication: value("medication"), ActivityRestrictions: value("activity_restrictions"), MedicalNotes: value("medical_notes"), HealthConsentAccepted: r.PostForm.Get("accept_health_data") == "on", Name: value("name"), Email: value("email"), DateOfBirth: value("date_of_birth"), ProfileUpdatedAt: value("profile_updated_at"), IdentityUpdatedAt: value("identity_updated_at"), Errors: validation.FieldErrors{}}
	checkLength := func(key, label, input string, max int) {
		if !utf8.ValidString(input) || utf8.RuneCountInString(input) > max {
			form.Errors.Add(key, fmt.Sprintf("%s não pode exceder %d caracteres.", label, max))
		}
	}
	for _, field := range []struct {
		key, label, value string
		max               int
	}{{"phone", "O telefone", form.Phone, 32}, {"address_line1", "A morada", form.AddressLine1, 200}, {"address_line2", "A morada complementar", form.AddressLine2, 200}, {"postcode", "O código postal", form.Postcode, 20}, {"locality", "A localidade", form.Locality, 120}, {"emergency_contact_name", "O nome do contacto de emergência", form.EmergencyName, 120}, {"emergency_contact_relationship", "A relação do contacto de emergência", form.EmergencyRelationship, 80}, {"emergency_contact_phone", "O telefone de emergência", form.EmergencyPhone, 32}, {"emergency_contact_alternate_phone", "O telefone alternativo", form.EmergencyAlternatePhone, 32}, {"allergies", "As alergias", form.Allergies, 2000}, {"medical_conditions", "As condições médicas", form.MedicalConditions, 2000}, {"medication", "A medicação", form.Medication, 2000}, {"activity_restrictions", "As restrições", form.ActivityRestrictions, 2000}, {"medical_notes", "As notas médicas", form.MedicalNotes, 2000}} {
		checkLength(field.key, field.label, field.value, field.max)
	}
	for key, phone := range map[string]string{"phone": form.Phone, "emergency_contact_phone": form.EmergencyPhone, "emergency_contact_alternate_phone": form.EmergencyAlternatePhone} {
		if phone != "" && !validProfilePhone(phone) {
			form.Errors.Add(key, "Introduza um número de telefone válido.")
		}
	}
	emergencyValues := []string{form.EmergencyName, form.EmergencyRelationship, form.EmergencyPhone, form.EmergencyAlternatePhone}
	if slices.ContainsFunc(emergencyValues, func(v string) bool { return v != "" }) && (form.EmergencyName == "" || form.EmergencyRelationship == "" || form.EmergencyPhone == "") {
		form.Errors.Add("emergency_contact", "Preencha o nome, a relação e o telefone principal do contacto de emergência.")
	}
	if form.CountryCode != "" && !validCountryCode(form.CountryCode) {
		form.Errors.Add("country_code", "Selecione um país válido.")
	}
	if form.NationalityCode != "" && !validCountryCode(form.NationalityCode) {
		form.Errors.Add("nationality_code", "Selecione uma nacionalidade válida.")
	}
	medicalFields := []string{form.Allergies, form.MedicalConditions, form.Medication, form.ActivityRestrictions, form.MedicalNotes}
	if form.MedicalDeclaration != "UNKNOWN" && form.MedicalDeclaration != "NONE_KNOWN" && form.MedicalDeclaration != "PROVIDED" {
		form.Errors.Add("medical_declaration", "Selecione uma declaração médica válida.")
	} else if form.MedicalDeclaration == "PROVIDED" && !slices.ContainsFunc(medicalFields, func(v string) bool { return v != "" }) {
		form.Errors.Add("medical_declaration", "Indique pelo menos uma informação médica relevante.")
	}
	profileTime, err := time.Parse(time.RFC3339Nano, form.ProfileUpdatedAt)
	if err != nil {
		form.Errors.Add("form", "O formulário está desatualizado. Recarregue a página.")
	}
	params := dbgen.UpdateMemberProfileParams{Phone: form.Phone, AddressLine1: form.AddressLine1, AddressLine2: form.AddressLine2, Postcode: form.Postcode, Locality: form.Locality, CountryCode: form.CountryCode, NationalityCode: form.NationalityCode, ClubMemberNumber: profileOptionalString(form.ClubMemberNumber), FederationLicenceNumber: profileOptionalString(form.FederationLicenceNumber), EmergencyContactName: form.EmergencyName, EmergencyContactRelationship: form.EmergencyRelationship, EmergencyContactPhone: form.EmergencyPhone, EmergencyContactAlternatePhone: form.EmergencyAlternatePhone, MedicalDeclaration: form.MedicalDeclaration, Allergies: form.Allergies, MedicalConditions: form.MedicalConditions, Medication: form.Medication, ActivityRestrictions: form.ActivityRestrictions, MedicalNotes: form.MedicalNotes, ExpectedUpdatedAt: pgtype.Timestamptz{Time: profileTime, Valid: err == nil}}
	if !isAdmin {
		return form, params, nil
	}
	if form.FederationLicenceNumber != stringValue(current.FederationLicenceNumber) && form.FederationLicenceNumber != "" && !validFPCAthleteNumber(form.FederationLicenceNumber) {
		form.Errors.Add("federation_licence_number", "Introduza um número de atleta FPC com 1 a 20 algarismos.")
	}
	name, nameErr := validation.NormalizeName(form.Name)
	if nameErr != nil {
		form.Errors.Add("name", nameErr.Error())
	} else {
		form.Name = name
	}
	birth, birthErr := validation.ParseISODate(form.DateOfBirth)
	if birthErr != nil {
		form.Errors.Add("date_of_birth", birthErr.Error())
	} else if current.IsDependent {
		if err := validation.ValidateDependentDateOfBirth(birth, h.now(), h.location()); err != nil {
			form.Errors.Add("date_of_birth", err.Error())
		}
	} else if err := validation.ValidateAdultDateOfBirth(birth, h.now(), h.location()); err != nil {
		form.Errors.Add("date_of_birth", err.Error())
	}
	var email *string
	if current.IsDependent {
		if form.Email != "" {
			form.Errors.Add("email", "Uma conta dependente não pode ter email de acesso.")
		}
	} else if normalized, err := validation.NormalizeEmail(form.Email); err != nil {
		form.Errors.Add("email", err.Error())
	} else {
		form.Email = normalized
		email = &normalized
	}
	identityTime, timeErr := time.Parse(time.RFC3339Nano, form.IdentityUpdatedAt)
	if timeErr != nil {
		form.Errors.Add("form", "O formulário está desatualizado. Recarregue a página.")
	}
	identity := &dbgen.UpdateMemberIdentityParams{Name: form.Name, Email: email, DateOfBirth: pgtype.Date{Time: birth, Valid: birthErr == nil}, ExpectedUpdatedAt: pgtype.Timestamptz{Time: identityTime, Valid: timeErr == nil}}
	return form, params, identity
}

func (h Profile) photoError(w http.ResponseWriter, r *http.Request, actor CurrentUser, subjectID uuid.UUID, base, message string) {
	record, err := h.view(r.Context(), actor, subjectID)
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	page := h.page(r, actor, base, record, pages.ProfileForm{}, "")
	page.Form.Errors.Add("photo", message)
	h.render(w, r, http.StatusUnprocessableEntity, page)
}

func (h Profile) render(w http.ResponseWriter, r *http.Request, status int, page pages.ProfilePage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = pages.Profile(page).Render(r.Context(), w)
}
func (h Profile) flash(r *http.Request, message string) {
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "profile_flash", message)
	}
}
func (h Profile) deleteObject(r *http.Request, key *string) {
	if key == nil || h.Objects == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Objects.DeleteObject(ctx, *key); err != nil {
		slog.Error("delete profile photo", "object_key", *key, "request_id", httpx.RequestID(r.Context()), "error", err)
	}
}
func (h Profile) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}
func (h Profile) location() *time.Location {
	if h.Location != nil {
		return h.Location
	}
	return time.UTC
}
func (h Profile) requestLimit() int64 {
	if h.MaxRequestBytes > 0 {
		return h.MaxRequestBytes
	}
	return 12 << 20
}
func (h Profile) photoLimit() int64 {
	if h.MaxPhotoBytes > 0 && h.MaxPhotoBytes <= 10<<20 {
		return h.MaxPhotoBytes
	}
	return 10 << 20
}

func singleProfilePhoto(form *multipart.Form) (*multipart.FileHeader, error) {
	for key, values := range form.Value {
		if (key != "accept_image_use" && key != "gorilla.csrf.Token") || len(values) != 1 {
			return nil, errors.New("invalid field")
		}
	}
	if len(form.File) != 1 || len(form.File["photo"]) != 1 || form.File["photo"][0].Filename == "" {
		return nil, errors.New("invalid photo")
	}
	return form.File["photo"][0], nil
}
func profileOptionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func profileComplete(record dbgen.GetMemberProfileRow) bool {
	return record.EmergencyContactName != "" && record.EmergencyContactRelationship != "" && record.EmergencyContactPhone != ""
}
func profileFormFromRecord(r dbgen.GetMemberProfileRow) pages.ProfileForm {
	return pages.ProfileForm{Name: r.Name, Email: stringValue(r.Email), DateOfBirth: r.DateOfBirth.Time.Format("2006-01-02"), Phone: r.Phone, AddressLine1: r.AddressLine1, AddressLine2: r.AddressLine2, Postcode: r.Postcode, Locality: r.Locality, CountryCode: r.CountryCode, NationalityCode: r.NationalityCode, ClubMemberNumber: stringValue(r.ClubMemberNumber), FederationLicenceNumber: stringValue(r.FederationLicenceNumber), EmergencyName: r.EmergencyContactName, EmergencyRelationship: r.EmergencyContactRelationship, EmergencyPhone: r.EmergencyContactPhone, EmergencyAlternatePhone: r.EmergencyContactAlternatePhone, MedicalDeclaration: r.MedicalDeclaration, Allergies: r.Allergies, MedicalConditions: r.MedicalConditions, Medication: r.Medication, ActivityRestrictions: r.ActivityRestrictions, MedicalNotes: r.MedicalNotes, ProfileUpdatedAt: r.UpdatedAt.Time.Format(time.RFC3339Nano), IdentityUpdatedAt: r.IdentityUpdatedAt.Time.Format(time.RFC3339Nano), Errors: validation.FieldErrors{}}
}
func profileChangedFields(r dbgen.GetMemberProfileRow, p dbgen.UpdateMemberProfileParams) []string {
	pairs := []struct{ name, before, after string }{{"phone", r.Phone, p.Phone}, {"address_line1", r.AddressLine1, p.AddressLine1}, {"address_line2", r.AddressLine2, p.AddressLine2}, {"postcode", r.Postcode, p.Postcode}, {"locality", r.Locality, p.Locality}, {"country_code", r.CountryCode, p.CountryCode}, {"nationality_code", r.NationalityCode, p.NationalityCode}, {"club_member_number", stringValue(r.ClubMemberNumber), stringValue(p.ClubMemberNumber)}, {"federation_licence_number", stringValue(r.FederationLicenceNumber), stringValue(p.FederationLicenceNumber)}, {"emergency_contact_name", r.EmergencyContactName, p.EmergencyContactName}, {"emergency_contact_relationship", r.EmergencyContactRelationship, p.EmergencyContactRelationship}, {"emergency_contact_phone", r.EmergencyContactPhone, p.EmergencyContactPhone}, {"emergency_contact_alternate_phone", r.EmergencyContactAlternatePhone, p.EmergencyContactAlternatePhone}, {"medical_declaration", r.MedicalDeclaration, p.MedicalDeclaration}, {"allergies", r.Allergies, p.Allergies}, {"medical_conditions", r.MedicalConditions, p.MedicalConditions}, {"medication", r.Medication, p.Medication}, {"activity_restrictions", r.ActivityRestrictions, p.ActivityRestrictions}, {"medical_notes", r.MedicalNotes, p.MedicalNotes}}
	out := []string{}
	for _, pair := range pairs {
		if pair.before != pair.after {
			out = append(out, pair.name)
		}
	}
	return out
}
func identityChangedFields(r dbgen.GetMemberProfileRow, p dbgen.UpdateMemberIdentityParams) []string {
	out := []string{}
	if r.Name != p.Name {
		out = append(out, "name")
	}
	if stringValue(r.Email) != stringValue(p.Email) {
		out = append(out, "email")
	}
	if !r.DateOfBirth.Time.Equal(p.DateOfBirth.Time) {
		out = append(out, "date_of_birth")
	}
	return out
}

var isoCountryCodes = func() map[string]bool {
	result := map[string]bool{}
	for _, code := range strings.Fields("AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW") {
		result[code] = true
	}
	return result
}()

func validCountryCode(code string) bool { return isoCountryCodes[code] }

func validFPCAthleteNumber(number string) bool {
	return fpcAthleteNumberPattern.MatchString(number)
}

func fpcHistoryURLs(number string) (string, string) {
	if !validFPCAthleteNumber(number) {
		return "", ""
	}
	return fpcNationalHistoryBase + number + "/", fpcInternationalHistoryBase + number + "/"
}

func validProfilePhone(phone string) bool {
	if !profilePhonePattern.MatchString(phone) {
		return false
	}
	digits := 0
	parentheses := 0
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			digits++
			continue
		}
		switch r {
		case '(':
			if parentheses != 0 {
				return false
			}
			parentheses = 1
		case ')':
			if parentheses != 1 {
				return false
			}
			parentheses = 0
		}
	}
	return parentheses == 0 && digits >= 7 && digits <= 15
}

func profileInitialsText(name string) string {
	words := strings.Fields(name)
	if len(words) == 0 {
		return "?"
	}
	first := []rune(words[0])
	if len(words) == 1 {
		return strings.ToUpper(string(first[0]))
	}
	last := []rune(words[len(words)-1])
	return strings.ToUpper(string(first[0]) + string(last[0]))
}
