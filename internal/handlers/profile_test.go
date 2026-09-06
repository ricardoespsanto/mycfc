package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestProfileAuthorizationBoundaries(t *testing.T) {
	adultID, dependentID, guardianID := uuid.New(), uuid.New(), uuid.New()
	adult := dbgen.GetMemberProfileRow{ID: adultID, IsActive: true}
	dependent := dbgen.GetMemberProfileRow{ID: dependentID, GuardianID: &guardianID, IsDependent: true, IsActive: true, DateOfBirth: pgtype.Date{Time: time.Now().AddDate(-10, 0, 0), Valid: true}}
	if !canViewProfile(adult, adultID, false) || !canEditProfile(adult, adultID, false) {
		t.Fatal("adult cannot manage own profile")
	}
	if !canViewProfile(dependent, dependentID, false) || canEditProfile(dependent, dependentID, false) {
		t.Fatal("credentialed dependent must read but not edit")
	}
	if !canViewProfile(dependent, guardianID, false) || !canEditProfile(dependent, guardianID, false) {
		t.Fatal("guardian cannot manage named dependent")
	}
	if canViewProfile(dependent, uuid.New(), false) || canEditProfile(dependent, uuid.New(), false) {
		t.Fatal("unrelated account can access dependent profile")
	}
	dependent.DateOfBirth = pgtype.Date{Time: time.Now().AddDate(-18, 0, -1), Valid: true}
	if canViewProfile(dependent, guardianID, false) || canEditProfile(dependent, guardianID, false) {
		t.Fatal("guardian retains access after the dependant reaches adulthood")
	}
	dependent.DateOfBirth = pgtype.Date{Time: time.Now().AddDate(-10, 0, 0), Valid: true}
	dependent.IsActive = false
	if canViewProfile(dependent, guardianID, false) || canEditProfile(dependent, guardianID, false) {
		t.Fatal("inactive dependent is exposed to non-admin")
	}
	if !canViewProfile(dependent, uuid.New(), true) || !canEditProfile(dependent, uuid.New(), true) {
		t.Fatal("administrator cannot manage inactive profile")
	}
}

func TestPostgresProfileStoreSavePhotoCreatesConsentAndAuditsReplacement(t *testing.T) {
	subjectID, consentID := uuid.New(), uuid.New()
	actorID := subjectID
	oldKey := "profiles/old.png"
	tx := &profileTransactionFake{subjectID: subjectID, consentID: consentID, oldPhotoKey: &oldKey}
	store := PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}
	old, err := store.SavePhoto(context.Background(), ProfilePhotoUpdate{ActorID: actorID, SubjectID: subjectID, ObjectKey: "profiles/new.png", ContentType: "image/png", Size: 42, ConsentVersion: "2026-09", ConsentSHA256: "digest", AcceptConsent: true, UserAgent: "MyCFCoimbra test"})

	if err != nil || old == nil || *old != oldKey || !tx.committed {
		t.Fatalf("old=%v error=%v committed=%t", old, err, tx.committed)
	}
	if len(tx.execCalls) != 1 || len(tx.queryCalls) != 4 {
		t.Fatalf("exec=%#v query=%#v", tx.execCalls, tx.queryCalls)
	}
	consentArgs := tx.argsFor("CreateConsentForm")
	if len(consentArgs) != 7 || consentArgs[0] != subjectID || consentArgs[2] != "Foto_Perfil" || consentArgs[3] != "2026-09" || consentArgs[4] != "digest" {
		t.Fatalf("consent args=%#v", consentArgs)
	}
	photoArgs := tx.argsFor("UpdateMemberProfilePhoto")
	if len(photoArgs) != 5 {
		t.Fatalf("photo args=%#v", photoArgs)
	}
	key, keyOK := photoArgs[0].(*string)
	photoConsent, consentOK := photoArgs[3].(*uuid.UUID)
	if !keyOK || *key != "profiles/new.png" || !consentOK || *photoConsent != consentID || photoArgs[4] != subjectID {
		t.Fatalf("photo args=%#v", photoArgs)
	}
	auditArgs := tx.argsFor("CreateMemberProfileAudit")
	if len(auditArgs) != 4 || auditArgs[0] != actorID || auditArgs[1] != subjectID || auditArgs[2] != "PHOTO_REPLACED" {
		t.Fatalf("audit args=%#v", auditArgs)
	}
}

func TestPostgresProfileStoreSavePhotoRequiresCurrentConsentBeforeWrite(t *testing.T) {
	subjectID := uuid.New()
	tx := &profileTransactionFake{subjectID: subjectID}
	store := PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}
	_, err := store.SavePhoto(context.Background(), ProfilePhotoUpdate{ActorID: subjectID, SubjectID: subjectID, ObjectKey: "profiles/new.png", ContentType: "image/png", Size: 42, ConsentVersion: "2026-09", ConsentSHA256: "digest"})
	if !errors.Is(err, ErrConsentRequired) || tx.committed || len(tx.queryCalls) != 1 {
		t.Fatalf("error=%v committed=%t query=%#v", err, tx.committed, tx.queryCalls)
	}
}

func TestPostgresProfileStorePropagatesReadAndPhotoWriteFailures(t *testing.T) {
	subjectID := uuid.New()
	readErr := errors.New("profile read unavailable")
	_, err := (PostgresProfileStore{DB: profileDatabaseFake{tx: &profileTransactionFake{subjectID: subjectID, queryErrs: map[string]error{"GetMemberProfile": readErr}}}}).View(context.Background(), subjectID, subjectID, false)
	if !errors.Is(err, readErr) {
		t.Fatalf("View error = %v, want %v", err, readErr)
	}

	writeErr := errors.New("photo update unavailable")
	tx := &profileTransactionFake{subjectID: subjectID, currentConsent: true, queryErrs: map[string]error{"UpdateMemberProfilePhoto": writeErr}}
	_, err = (PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}).SavePhoto(context.Background(), ProfilePhotoUpdate{ActorID: subjectID, SubjectID: subjectID, ObjectKey: "profiles/new.png", ContentType: "image/png", Size: 42, ConsentVersion: "2026-09", ConsentSHA256: "digest", AcceptConsent: true})
	if !errors.Is(err, writeErr) || tx.committed || tx.argsFor("CreateMemberProfileAudit") != nil {
		t.Fatalf("SavePhoto error=%v committed=%t calls=%#v", err, tx.committed, tx.queryCalls)
	}
}

func TestPostgresProfileStoreSavePhotoRequiresFreshConsentForEveryObject(t *testing.T) {
	subjectID, consentID := uuid.New(), uuid.New()
	tx := &profileTransactionFake{subjectID: subjectID, consentID: consentID, currentConsent: true}
	_, err := (PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}).SavePhoto(context.Background(), ProfilePhotoUpdate{ActorID: subjectID, SubjectID: subjectID, ObjectKey: "profiles/current-consent.png", ContentType: "image/png", Size: 42, ConsentVersion: "2026-09", ConsentSHA256: "digest", AcceptConsent: true})
	if err != nil || !tx.committed || tx.argsFor("CreateConsentForm") == nil {
		t.Fatalf("error=%v committed=%t calls=%#v", err, tx.committed, tx.queryCalls)
	}
	photoArgs := tx.argsFor("UpdateMemberProfilePhoto")
	if len(photoArgs) != 5 {
		t.Fatalf("photo args=%#v", photoArgs)
	}
	storedConsent, ok := photoArgs[3].(*uuid.UUID)
	if !ok || *storedConsent != consentID {
		t.Fatalf("photo args=%#v", photoArgs)
	}
}

func TestPostgresProfileStoreRemovePhotoClearsObjectReferenceAndAudits(t *testing.T) {
	subjectID := uuid.New()
	oldKey := "profiles/old.png"
	tx := &profileTransactionFake{subjectID: subjectID, oldPhotoKey: &oldKey}
	store := PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}
	removed, err := store.RemovePhoto(context.Background(), subjectID, subjectID, false)
	if err != nil || removed == nil || *removed != oldKey || !tx.committed || len(tx.queryCalls) != 3 {
		t.Fatalf("removed=%v error=%v committed=%t query=%#v", removed, err, tx.committed, tx.queryCalls)
	}
	auditArgs := tx.argsFor("CreateMemberProfileAudit")
	if len(auditArgs) != 4 || auditArgs[0] != subjectID || auditArgs[1] != subjectID || auditArgs[2] != "PHOTO_REMOVED" {
		t.Fatalf("audit args=%#v", auditArgs)
	}
}

func TestPostgresProfileStoreRefusesPhotoChangesWithoutEligibleState(t *testing.T) {
	t.Run("remove absent photo", func(t *testing.T) {
		subjectID := uuid.New()
		tx := &profileTransactionFake{subjectID: subjectID}
		removed, err := (PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}).RemovePhoto(context.Background(), subjectID, subjectID, false)
		if !errors.Is(err, pgx.ErrNoRows) || removed != nil || tx.committed || len(tx.argsFor("CreateMemberProfileAudit")) != 0 {
			t.Fatalf("removed=%v error=%v committed=%t calls=%#v", removed, err, tx.committed, tx.queryCalls)
		}
	})

	t.Run("administrator cannot grant another person's consent", func(t *testing.T) {
		subjectID := uuid.New()
		tx := &profileTransactionFake{subjectID: subjectID}
		_, err := (PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}).SavePhoto(context.Background(), ProfilePhotoUpdate{ActorID: uuid.New(), SubjectID: subjectID, IsAdmin: true, ObjectKey: "profiles/admin.png", AcceptConsent: true})
		if !errors.Is(err, ErrConsentRequired) || tx.committed || tx.argsFor("CreateConsentForm") != nil {
			t.Fatalf("error=%v committed=%t calls=%#v", err, tx.committed, tx.queryCalls)
		}
	})
}

func TestPostgresProfileStoreViewAuditsAuthorizedSensitiveRead(t *testing.T) {
	subjectID := uuid.New()
	tx := &profileTransactionFake{subjectID: subjectID}
	store := PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}
	profile, err := store.View(context.Background(), subjectID, subjectID, false)
	if err != nil || profile.ID != subjectID || !tx.committed || len(tx.execCalls) != 1 || len(tx.queryCalls) != 2 {
		t.Fatalf("profile=%#v error=%v committed=%t exec=%#v query=%#v", profile, err, tx.committed, tx.execCalls, tx.queryCalls)
	}
	auditArgs := tx.argsFor("CreateMemberProfileAudit")
	if len(auditArgs) != 4 || auditArgs[0] != subjectID || auditArgs[1] != subjectID || auditArgs[2] != "SENSITIVE_VIEW" {
		t.Fatalf("audit args=%#v", auditArgs)
	}
}

func TestPostgresProfileStoreUpdateProtectsNonAdminIdentityAndAuditsProfile(t *testing.T) {
	subjectID := uuid.New()
	tx := &profileTransactionFake{subjectID: subjectID}
	store := PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}
	input := ProfileUpdate{ActorID: subjectID, SubjectID: subjectID, Profile: dbgen.UpdateMemberProfileParams{Phone: "+351 912 000 000"}, Identity: &dbgen.UpdateMemberIdentityParams{Email: stringPtr("changed@example.test")}, ChangedFields: []string{"phone"}, IdentityFields: []string{"email"}}
	err := store.Update(context.Background(), input)
	if err != nil || !tx.committed || len(tx.execCalls) != 1 {
		t.Fatalf("error=%v committed=%t exec=%#v query=%#v", err, tx.committed, tx.execCalls, tx.queryCalls)
	}
	if tx.argsFor("UpdateMemberIdentity") != nil {
		t.Fatalf("non-admin identity update was attempted: %#v", tx.queryCalls)
	}
	updateArgs := tx.argsFor("UpdateMemberProfile")
	if len(updateArgs) == 0 || updateArgs[len(updateArgs)-2] != subjectID {
		t.Fatalf("profile update args=%#v", updateArgs)
	}
	auditArgs := tx.argsFor("CreateMemberProfileAudit")
	if len(auditArgs) != 4 || auditArgs[2] != "PROFILE_UPDATED" {
		t.Fatalf("audit args=%#v", auditArgs)
	}
}

func TestPostgresProfileStoreRequiresAndRecordsExplicitHealthConsent(t *testing.T) {
	subjectID := uuid.New()
	profile := dbgen.UpdateMemberProfileParams{MedicalDeclaration: "PROVIDED", Allergies: "Pólen"}
	input := ProfileUpdate{ActorID: subjectID, SubjectID: subjectID, Profile: profile, ChangedFields: []string{"medical_declaration", "allergies"}, HealthVersion: "2026-09-06", HealthSHA256: "privacy-digest"}

	tx := &profileTransactionFake{subjectID: subjectID}
	err := (PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}).Update(context.Background(), input)
	if !errors.Is(err, ErrHealthConsentRequired) || tx.committed || tx.argsFor("UpdateMemberProfile") != nil {
		t.Fatalf("without consent: error=%v committed=%t calls=%#v", err, tx.committed, tx.queryCalls)
	}

	tx = &profileTransactionFake{subjectID: subjectID, consentID: uuid.New()}
	input.AcceptHealthConsent = true
	err = (PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}).Update(context.Background(), input)
	if err != nil || !tx.committed {
		t.Fatalf("with consent: error=%v committed=%t", err, tx.committed)
	}
	args := tx.argsFor("CreateConsentForm")
	if len(args) != 7 || args[0] != subjectID || args[2] != "Dados_Saude" || args[3] != "2026-09-06" || args[4] != "privacy-digest" {
		t.Fatalf("health consent args=%#v", args)
	}
}

func TestPostgresProfileStoreProtectsHealthDataAcrossActorTypesAndConsentStates(t *testing.T) {
	subjectID, guardianID := uuid.New(), uuid.New()

	t.Run("guardian health values are preserved and excluded from audit fields", func(t *testing.T) {
		tx := &profileTransactionFake{subjectID: subjectID, guardianID: &guardianID, isDependent: true, dateOfBirth: pgtype.Date{Time: time.Now().AddDate(-10, 0, 0), Valid: true}, medicalDeclaration: "NONE_KNOWN"}
		input := ProfileUpdate{ActorID: guardianID, SubjectID: subjectID, Profile: dbgen.UpdateMemberProfileParams{MedicalDeclaration: "PROVIDED", Allergies: "Pólen"}, ChangedFields: []string{"phone", "medical_declaration", "allergies"}}
		if err := (PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}).Update(context.Background(), input); err != nil || !tx.committed {
			t.Fatalf("error=%v committed=%t", err, tx.committed)
		}
		updateArgs := tx.argsFor("UpdateMemberProfile")
		if len(updateArgs) < 21 || updateArgs[13] != "NONE_KNOWN" || updateArgs[14] != "" {
			t.Fatalf("health values were not preserved: %#v", updateArgs)
		}
		auditArgs := tx.argsFor("CreateMemberProfileAudit")
		fields, ok := auditArgs[3].([]string)
		if !ok || !reflect.DeepEqual(fields, []string{"phone"}) {
			t.Fatalf("audit fields=%#v", auditArgs)
		}
	})

	t.Run("administrator cannot expand health data", func(t *testing.T) {
		tx := &profileTransactionFake{subjectID: subjectID}
		err := (PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}).Update(context.Background(), ProfileUpdate{ActorID: uuid.New(), SubjectID: subjectID, IsAdmin: true, Profile: dbgen.UpdateMemberProfileParams{MedicalDeclaration: "PROVIDED", Allergies: "Pólen"}})
		if !errors.Is(err, ErrHealthConsentRequired) || tx.committed {
			t.Fatalf("error=%v committed=%t", err, tx.committed)
		}
	})

	t.Run("current consent permits an adult health update", func(t *testing.T) {
		tx := &profileTransactionFake{subjectID: subjectID, medicalDeclaration: "PROVIDED", allergies: "Pólen", healthConsent: true}
		err := (PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}).Update(context.Background(), ProfileUpdate{ActorID: subjectID, SubjectID: subjectID, Profile: dbgen.UpdateMemberProfileParams{MedicalDeclaration: "PROVIDED", Allergies: "Pólen e ácaros"}, HealthVersion: "v1", HealthSHA256: "digest"})
		if err != nil || !tx.committed || tx.argsFor("HasConsentVersion") == nil || tx.argsFor("CreateConsentForm") != nil {
			t.Fatalf("error=%v committed=%t calls=%#v", err, tx.committed, tx.queryCalls)
		}
	})

	t.Run("consent write failure rolls back", func(t *testing.T) {
		want := errors.New("consent unavailable")
		tx := &profileTransactionFake{subjectID: subjectID, queryErrs: map[string]error{"CreateConsentForm": want}}
		err := (PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}).Update(context.Background(), ProfileUpdate{ActorID: subjectID, SubjectID: subjectID, Profile: dbgen.UpdateMemberProfileParams{MedicalDeclaration: "NONE_KNOWN"}, AcceptHealthConsent: true})
		if !errors.Is(err, want) || tx.committed {
			t.Fatalf("error=%v committed=%t", err, tx.committed)
		}
	})
}

func TestHealthDataExpansionIncludesNegativeDeclarationAndAllowsMinimization(t *testing.T) {
	current := dbgen.GetMemberProfileRow{MedicalDeclaration: "UNKNOWN"}
	if !healthDataExpanded(current, dbgen.UpdateMemberProfileParams{MedicalDeclaration: "NONE_KNOWN"}) {
		t.Fatal("negative medical-status assertion bypassed explicit consent")
	}
	current = dbgen.GetMemberProfileRow{MedicalDeclaration: "PROVIDED", Allergies: "Pólen", MedicalNotes: "Nota antiga"}
	next := dbgen.UpdateMemberProfileParams{MedicalDeclaration: "PROVIDED", Allergies: "Pólen"}
	if healthDataExpanded(current, next) {
		t.Fatal("removing health information was treated as an expansion")
	}
}

func TestPostgresProfileStoreUpdateAuditsAdministratorIdentityChange(t *testing.T) {
	subjectID, actorID := uuid.New(), uuid.New()
	email := "member@example.test"
	tx := &profileTransactionFake{subjectID: subjectID, email: &email}
	store := PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}
	input := ProfileUpdate{ActorID: actorID, SubjectID: subjectID, IsAdmin: true, Profile: dbgen.UpdateMemberProfileParams{Phone: "+351 912 000 000"}, Identity: &dbgen.UpdateMemberIdentityParams{Email: &email}, ChangedFields: []string{"phone"}, IdentityFields: []string{"email"}}
	err := store.Update(context.Background(), input)
	if err != nil || !tx.committed || tx.argsFor("UpdateMemberIdentity") == nil {
		t.Fatalf("error=%v committed=%t query=%#v", err, tx.committed, tx.queryCalls)
	}
	identityAudit, profileAudit := 0, 0
	for _, call := range tx.queryCalls {
		if !strings.Contains(call.query, "CreateMemberProfileAudit") || len(call.args) < 3 {
			continue
		}
		if call.args[2] == "IDENTITY_UPDATED" {
			identityAudit++
		}
		if call.args[2] == "PROFILE_UPDATED" {
			profileAudit++
		}
	}
	if identityAudit != 1 || profileAudit != 1 {
		t.Fatalf("identity audits=%d profile audits=%d calls=%#v", identityAudit, profileAudit, tx.queryCalls)
	}
}

func TestPostgresProfileStoreAdministratorEmailChangeIssuesVerification(t *testing.T) {
	subjectID, actorID := uuid.New(), uuid.New()
	previousEmail, nextEmail := "member@example.test", "member.new@example.test"
	tx := &profileTransactionFake{subjectID: subjectID, email: &previousEmail}
	store := PostgresProfileStore{DB: profileDatabaseFake{tx: tx}, Now: func() time.Time { return time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC) }}
	err := store.Update(context.Background(), ProfileUpdate{ActorID: actorID, SubjectID: subjectID, IsAdmin: true, Profile: dbgen.UpdateMemberProfileParams{}, Identity: &dbgen.UpdateMemberIdentityParams{Email: &nextEmail}, IdentityFields: []string{"email"}})
	if err != nil || !tx.committed {
		t.Fatalf("error=%v committed=%t calls=%#v", err, tx.committed, tx.queryCalls)
	}
	issueArgs := tx.argsFor("CreateEmailVerification")
	if len(issueArgs) != 5 || issueArgs[0] != subjectID || issueArgs[1] != nextEmail || issueArgs[4] != false {
		t.Fatalf("verification issue args=%#v", issueArgs)
	}
}

func TestPostgresProfileStoreUpdateMapsStaleIdentityAndProfileWritesToConflict(t *testing.T) {
	for name, input := range map[string]ProfileUpdate{
		"identity": {IsAdmin: true, Identity: &dbgen.UpdateMemberIdentityParams{}},
		"profile":  {},
	} {
		t.Run(name, func(t *testing.T) {
			subjectID := uuid.New()
			query := "UpdateMemberProfile"
			if name == "identity" {
				query = "UpdateMemberIdentity"
			}
			tx := &profileTransactionFake{subjectID: subjectID, queryErrs: map[string]error{query: pgx.ErrNoRows}}
			input.ActorID, input.SubjectID = subjectID, subjectID
			err := (PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}).Update(context.Background(), input)
			if !errors.Is(err, ErrProfileConflict) || tx.committed {
				t.Fatalf("error=%v committed=%t calls=%#v", err, tx.committed, tx.queryCalls)
			}
		})
	}
}

func TestIdentityChangedFieldsReportsOnlyProtectedIdentityDifferences(t *testing.T) {
	birthday := time.Date(1990, 3, 15, 0, 0, 0, 0, time.UTC)
	email := "ana@example.test"
	record := dbgen.GetMemberProfileRow{Name: "Ana Silva", Email: &email, DateOfBirth: pgtype.Date{Time: birthday, Valid: true}}
	unchanged := dbgen.UpdateMemberIdentityParams{Name: "Ana Silva", Email: &email, DateOfBirth: pgtype.Date{Time: birthday, Valid: true}}
	if fields := identityChangedFields(record, unchanged); len(fields) != 0 {
		t.Fatalf("unchanged fields=%#v", fields)
	}
	otherEmail := "ana.nova@example.test"
	changed := unchanged
	changed.Name, changed.Email, changed.DateOfBirth = "Ana Costa", &otherEmail, pgtype.Date{Time: birthday.AddDate(1, 0, 0), Valid: true}
	if fields := identityChangedFields(record, changed); !reflect.DeepEqual(fields, []string{"name", "email", "date_of_birth"}) {
		t.Fatalf("changed fields=%#v", fields)
	}
}

func TestPostgresProfileStoreAvatarUsesConsentScopedLookup(t *testing.T) {
	subjectID := uuid.New()
	tx := &profileTransactionFake{subjectID: subjectID}
	store := PostgresProfileStore{DB: profileDatabaseFake{tx: tx}}
	avatar, err := store.Avatar(context.Background(), dbgen.GetMemberAvatarParams{UserID: subjectID, DocumentVersion: "2026-09", DocumentSha256: "digest", IsAdmin: false})
	if err != nil || avatar.Name != "Atleta" || avatar.PhotoObjectKey == nil || *avatar.PhotoObjectKey != "profiles/avatar.png" || !avatar.ConsentCurrent {
		t.Fatalf("avatar=%#v error=%v", avatar, err)
	}
	if len(tx.queryCalls) != 1 || !strings.Contains(tx.queryCalls[0].query, "GetMemberAvatar") || tx.queryCalls[0].args[2] != subjectID {
		t.Fatalf("avatar query=%#v", tx.queryCalls)
	}
}

func TestPostgresProfileStoreSelectsInjectedDatabaseAndFailsClosedForForeignActor(t *testing.T) {
	tx := &profileTransactionFake{subjectID: uuid.New()}
	injected := profileDatabaseFake{tx: tx}
	if got := (PostgresProfileStore{DB: injected}).database(); got != injected {
		t.Fatal("injected database was not selected")
	}
	if got := (PostgresProfileStore{}).database(); got != nil {
		t.Fatalf("empty store database=%#v", got)
	}
	store := PostgresProfileStore{DB: injected}
	foreignActor := uuid.New()
	if _, err := store.View(context.Background(), foreignActor, tx.subjectID, false); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign view error=%v", err)
	}
	if err := store.Update(context.Background(), ProfileUpdate{ActorID: foreignActor, SubjectID: tx.subjectID, Profile: dbgen.UpdateMemberProfileParams{}}); !errors.Is(err, ErrProfileForbidden) {
		t.Fatalf("foreign update error=%v", err)
	}
	if _, err := store.SavePhoto(context.Background(), ProfilePhotoUpdate{ActorID: foreignActor, SubjectID: tx.subjectID, ObjectKey: "profiles/foreign.png"}); !errors.Is(err, ErrProfileForbidden) {
		t.Fatalf("foreign upload error=%v", err)
	}
	if _, err := store.RemovePhoto(context.Background(), foreignActor, tx.subjectID, false); !errors.Is(err, ErrProfileForbidden) {
		t.Fatalf("foreign removal error=%v", err)
	}
}

func TestProfileDeepLinkMetadataNamesOwningArea(t *testing.T) {
	actorID, subjectID := uuid.New(), uuid.New()
	record := dbgen.GetMemberProfileRow{ID: subjectID, Name: "Leonor Rodrigues", IsDependent: true, IsActive: true}
	for _, tc := range []struct {
		name, base, area string
		actor            CurrentUser
		breadcrumbs      []string
	}{
		{name: "self", base: "/perfil", area: "Conta", actor: CurrentUser{ID: subjectID, Name: "Leonor"}},
		{name: "guardian", base: "/perfil/dependentes/" + subjectID.String(), area: "Família", actor: CurrentUser{ID: actorID, Name: "Marta"}, breadcrumbs: []string{"Menores a cargo"}},
		{name: "administrator", base: "/admin/membros/" + subjectID.String() + "/perfil", area: "Administração", actor: CurrentUser{ID: actorID, Name: "Beatriz", IsAdmin: true}, breadcrumbs: []string{"Membros", "Leonor Rodrigues"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := Profile{Store: profilePageStore{}}
			page := h.page(httptest.NewRequest(http.MethodGet, tc.base, nil), tc.actor, tc.base, record, pages.ProfileForm{}, "")
			if page.Meta.AreaLabel != tc.area || page.Meta.CurrentPath != tc.base || page.Meta.PageLabel != "Perfil" || len(page.Meta.Breadcrumbs) != len(tc.breadcrumbs) {
				t.Fatalf("meta = %#v", page.Meta)
			}
			for i, label := range tc.breadcrumbs {
				if page.Meta.Breadcrumbs[i].Label != label {
					t.Fatalf("breadcrumb %d = %#v, want %q", i, page.Meta.Breadcrumbs[i], label)
				}
			}
			if tc.actor.ID != subjectID && page.Meta.SubjectContext != record.Name {
				t.Fatalf("subject context = %q", page.Meta.SubjectContext)
			}
		})
	}
}

type profilePageStore struct{ ProfileStore }

type profileWorkflowStore struct {
	record    dbgen.GetMemberProfileRow
	update    ProfileUpdate
	photo     ProfilePhotoUpdate
	removed   *string
	avatar    dbgen.GetMemberAvatarRow
	viewErr   error
	updateErr error
	photoErr  error
	removeErr error
	avatarErr error
}

func (s *profileWorkflowStore) View(context.Context, uuid.UUID, uuid.UUID, bool) (dbgen.GetMemberProfileRow, error) {
	return s.record, s.viewErr
}

func (s *profileWorkflowStore) Update(_ context.Context, update ProfileUpdate) error {
	s.update = update
	return s.updateErr
}

func (s *profileWorkflowStore) SavePhoto(_ context.Context, photo ProfilePhotoUpdate) (*string, error) {
	s.photo = photo
	return s.removed, s.photoErr
}
func (s *profileWorkflowStore) RemovePhoto(context.Context, uuid.UUID, uuid.UUID, bool) (*string, error) {
	return s.removed, s.removeErr
}
func (s *profileWorkflowStore) Avatar(context.Context, dbgen.GetMemberAvatarParams) (dbgen.GetMemberAvatarRow, error) {
	return s.avatar, s.avatarErr
}

type profileObjectStoreFake struct {
	puts, deletes int
	presignedURL  string
	putErr        error
	deleteErr     error
	presignErr    error
}

type profileSQLCall struct {
	query string
	args  []any
}

type profileTransactionFake struct {
	pgx.Tx
	subjectID            uuid.UUID
	consentID            uuid.UUID
	oldPhotoKey          *string
	email                *string
	currentConsent       bool
	healthConsent        bool
	guardianID           *uuid.UUID
	isDependent          bool
	dateOfBirth          pgtype.Date
	medicalDeclaration   string
	allergies            string
	medicalConditions    string
	medication           string
	activityRestrictions string
	medicalNotes         string
	queryErrs            map[string]error
	execCalls            []profileSQLCall
	queryCalls           []profileSQLCall
	committed            bool
}

func (tx *profileTransactionFake) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	tx.queryCalls = append(tx.queryCalls, profileSQLCall{query: query, args: args})
	row := profileTransactionRow{query: query, subjectID: tx.subjectID, consentID: tx.consentID, oldPhotoKey: tx.oldPhotoKey, email: tx.email, currentConsent: tx.currentConsent, healthConsent: tx.healthConsent, guardianID: tx.guardianID, isDependent: tx.isDependent, dateOfBirth: tx.dateOfBirth, medicalDeclaration: tx.medicalDeclaration, allergies: tx.allergies, medicalConditions: tx.medicalConditions, medication: tx.medication, activityRestrictions: tx.activityRestrictions, medicalNotes: tx.medicalNotes}
	for name, err := range tx.queryErrs {
		if strings.Contains(query, name) {
			row.err = err
			break
		}
	}
	return row
}
func (tx *profileTransactionFake) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	tx.execCalls = append(tx.execCalls, profileSQLCall{query: query, args: args})
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (tx *profileTransactionFake) Commit(context.Context) error { tx.committed = true; return nil }
func (*profileTransactionFake) Rollback(context.Context) error  { return nil }
func (tx *profileTransactionFake) argsFor(name string) []any {
	for _, call := range tx.queryCalls {
		if strings.Contains(call.query, name) {
			return call.args
		}
	}
	return nil
}

type profileTransactionRow struct {
	query                string
	subjectID            uuid.UUID
	consentID            uuid.UUID
	oldPhotoKey          *string
	email                *string
	currentConsent       bool
	healthConsent        bool
	guardianID           *uuid.UUID
	isDependent          bool
	dateOfBirth          pgtype.Date
	medicalDeclaration   string
	allergies            string
	medicalConditions    string
	medication           string
	activityRestrictions string
	medicalNotes         string
	err                  error
}

func (row profileTransactionRow) Scan(dest ...any) error {
	if row.err != nil {
		return row.err
	}
	switch {
	case strings.Contains(row.query, "HasConsentVersion"):
		*dest[0].(*bool) = row.healthConsent
	case strings.Contains(row.query, "GetCurrentImageConsent"):
		if row.currentConsent {
			*dest[0].(*uuid.UUID) = row.consentID
			return nil
		}
		return pgx.ErrNoRows
	case strings.Contains(row.query, "GetMemberProfile"):
		*dest[0].(*uuid.UUID) = row.subjectID
		*dest[2].(**string) = row.email
		*dest[5].(**uuid.UUID) = row.guardianID
		*dest[6].(*bool) = row.isDependent
		*dest[7].(*pgtype.Date) = row.dateOfBirth
		*dest[8].(*bool) = true
		*dest[23].(*string) = row.medicalDeclaration
		*dest[24].(*string) = row.allergies
		*dest[25].(*string) = row.medicalConditions
		*dest[26].(*string) = row.medication
		*dest[27].(*string) = row.activityRestrictions
		*dest[28].(*string) = row.medicalNotes
		*dest[29].(**string) = row.oldPhotoKey
	case strings.Contains(row.query, "CreateConsentForm"):
		*dest[0].(*uuid.UUID) = row.consentID
	case strings.Contains(row.query, "UpdateMemberProfilePhoto"):
		*dest[0].(*pgtype.Timestamptz) = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	case strings.Contains(row.query, "ClearMemberProfilePhoto"):
		*dest[0].(*pgtype.Timestamptz) = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	case strings.Contains(row.query, "CreateMemberProfileAudit"):
		*dest[0].(*uuid.UUID) = uuid.New()
	case strings.Contains(row.query, "GetMemberAvatar"):
		*dest[0].(*string) = "Atleta"
		key, contentType := "profiles/avatar.png", "image/png"
		size := int64(42)
		*dest[1].(**string) = &key
		*dest[2].(**string) = &contentType
		*dest[3].(**int64) = &size
		*dest[4].(*bool) = true
	}
	return nil
}

type profileDatabaseFake struct {
	pgx.Tx
	tx *profileTransactionFake
}

func (db profileDatabaseFake) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return db.tx, nil
}
func (db profileDatabaseFake) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return db.tx.QueryRow(ctx, query, args...)
}
func (db profileDatabaseFake) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return db.tx.Exec(ctx, query, args...)
}

func (s *profileObjectStoreFake) PutObject(_ context.Context, _ string, _ string, _ int64, body io.Reader) error {
	_, _ = io.ReadAll(body)
	s.puts++
	return s.putErr
}
func (s *profileObjectStoreFake) DeleteObject(context.Context, string) error {
	s.deletes++
	return s.deleteErr
}
func (s *profileObjectStoreFake) PresignGet(context.Context, string, time.Duration) (string, error) {
	return s.presignedURL, s.presignErr
}

var _ storage.ObjectStore = (*profileObjectStoreFake)(nil)

func (profilePageStore) Avatar(context.Context, dbgen.GetMemberAvatarParams) (dbgen.GetMemberAvatarRow, error) {
	return dbgen.GetMemberAvatarRow{}, nil
}

func TestProfileValidationRequiresCompleteEmergencyAndMedicalDetails(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	h := Profile{Location: time.UTC, Now: func() time.Time { return now }}
	updated := now.Add(-time.Hour)
	record := dbgen.GetMemberProfileRow{ID: uuid.New(), Name: "Ana Silva", Email: stringPtr("ana@example.test"), DateOfBirth: pgtype.Date{Time: time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC), Valid: true}, IsActive: true, UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}, IdentityUpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}

	validBase := url.Values{
		"name": {"Ana Silva"}, "email": {"ana@example.test"}, "date_of_birth": {"1990-01-02"},
		"profile_updated_at": {updated.Format(time.RFC3339Nano)}, "identity_updated_at": {updated.Format(time.RFC3339Nano)},
		"medical_declaration": {"NONE_KNOWN"}, "country_code": {"PT"}, "nationality_code": {"PT"},
	}

	for _, tc := range []struct {
		name, field string
		mutate      func(url.Values)
	}{
		{"partial emergency contact", "emergency_contact", func(v url.Values) { v.Set("emergency_contact_name", "Rui Silva") }},
		{"provided medical status needs detail", "medical_declaration", func(v url.Values) { v.Set("medical_declaration", "PROVIDED") }},
		{"unknown country rejected", "country_code", func(v url.Values) { v.Set("country_code", "ZZ") }},
		{"malformed phone rejected", "phone", func(v url.Values) { v.Set("phone", "call me") }},
		{"punctuation-only phone rejected", "phone", func(v url.Values) { v.Set("phone", "+++") }},
		{"short phone rejected", "phone", func(v url.Values) { v.Set("phone", "123456") }},
		{"too many phone digits rejected", "phone", func(v url.Values) { v.Set("phone", "+1234567890123456") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := cloneValues(validBase)
			tc.mutate(values)
			request := httptest.NewRequest(http.MethodPost, "/perfil", strings.NewReader(values.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			_ = request.ParseForm()
			form, _, _ := h.validateForm(request, record, false)
			if !form.Errors.Has(tc.field) {
				t.Fatalf("errors = %#v, want %q", form.Errors, tc.field)
			}
		})
	}

	request := httptest.NewRequest(http.MethodPost, "/perfil", strings.NewReader(validBase.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = request.ParseForm()
	form, params, identity := h.validateForm(request, record, false)
	if !form.Errors.Empty() || identity != nil || params.MedicalDeclaration != "NONE_KNOWN" {
		t.Fatalf("valid form = %#v, params = %#v, identity = %#v", form.Errors, params, identity)
	}

	record.MedicalDeclaration = "PROVIDED"
	record.Allergies = "Pólen"
	withoutHealthFields := cloneValues(validBase)
	withoutHealthFields.Del("medical_declaration")
	request = httptest.NewRequest(http.MethodPost, "/perfil/dependentes/member", strings.NewReader(withoutHealthFields.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = request.ParseForm()
	form, params, _ = h.validateForm(request, record, false)
	if !form.Errors.Empty() || params.MedicalDeclaration != "PROVIDED" || params.Allergies != "Pólen" {
		t.Fatalf("hidden health values were not preserved: errors=%#v params=%#v", form.Errors, params)
	}
}

func TestValidProfilePhone(t *testing.T) {
	for _, phone := range []string{"912626410", "+351 912 626 410", "+44 (20) 7946-0958", "212.555.0100"} {
		if !validProfilePhone(phone) {
			t.Errorf("validProfilePhone(%q) = false", phone)
		}
	}
	for _, phone := range []string{"", "+++", "123456", "+1234567890123456", "351+912626410", "+44 ((20)) 79460958", "+44 (20 79460958"} {
		if validProfilePhone(phone) {
			t.Errorf("validProfilePhone(%q) = true", phone)
		}
	}
}

func TestFPCAthleteNumberAndHistoryURLs(t *testing.T) {
	for _, number := range []string{"1", "12142", "27044", "12345678901234567890"} {
		if !validFPCAthleteNumber(number) {
			t.Errorf("validFPCAthleteNumber(%q) = false", number)
		}
	}
	for _, number := range []string{"", " 27044 ", "Ricardo Santo", "27044/Ricardo", "123456789012345678901"} {
		if validFPCAthleteNumber(number) {
			t.Errorf("validFPCAthleteNumber(%q) = true", number)
		}
		if national, international := fpcHistoryURLs(number); national != "" || international != "" {
			t.Errorf("fpcHistoryURLs(%q) = %q, %q", number, national, international)
		}
	}

	national, international := fpcHistoryURLs("27044")
	if national != "https://www.fpcanoagem.pt/resultados/verhistorico/27044/" {
		t.Fatalf("national URL = %q", national)
	}
	if international != "https://www.fpcanoagem.pt/resultados/verhistoricointernational/27044/" {
		t.Fatalf("international URL = %q", international)
	}
}

func TestProfileValidationRejectsChangedMalformedFPCNumberButAllowsUnchangedLegacyValue(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	updated := now.Add(-time.Hour)
	legacy := "FPC legacy"
	record := dbgen.GetMemberProfileRow{ID: uuid.New(), Name: "Ana Silva", Email: stringPtr("ana@example.test"), DateOfBirth: pgtype.Date{Time: time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC), Valid: true}, IsActive: true, FederationLicenceNumber: &legacy, UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}, IdentityUpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}
	h := Profile{Location: time.UTC, Now: func() time.Time { return now }}
	base := url.Values{
		"name": {"Ana Silva"}, "email": {"ana@example.test"}, "date_of_birth": {"1990-01-02"},
		"profile_updated_at": {updated.Format(time.RFC3339Nano)}, "identity_updated_at": {updated.Format(time.RFC3339Nano)},
		"medical_declaration": {"NONE_KNOWN"}, "federation_licence_number": {legacy},
	}

	request := httptest.NewRequest(http.MethodPost, "/admin/membros/member/perfil", strings.NewReader(base.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = request.ParseForm()
	form, _, _ := h.validateForm(request, record, true)
	if form.Errors.Has("federation_licence_number") {
		t.Fatalf("unchanged legacy identifier rejected: %#v", form.Errors)
	}

	base.Set("federation_licence_number", "27044/Ricardo")
	request = httptest.NewRequest(http.MethodPost, "/admin/membros/member/perfil", strings.NewReader(base.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = request.ParseForm()
	form, _, _ = h.validateForm(request, record, true)
	if !form.Errors.Has("federation_licence_number") {
		t.Fatalf("changed malformed identifier accepted: %#v", form.Errors)
	}
}

func TestProfileAuditChangesContainFieldNamesOnly(t *testing.T) {
	record := dbgen.GetMemberProfileRow{Phone: "+351 910 000 000", MedicalNotes: "private before"}
	params := dbgen.UpdateMemberProfileParams{Phone: "+351 920 000 000", MedicalNotes: "private after"}
	fields := profileChangedFields(record, params)
	joined := strings.Join(fields, ",")
	if !strings.Contains(joined, "phone") || !strings.Contains(joined, "medical_notes") {
		t.Fatalf("changed fields = %v", fields)
	}
	if strings.Contains(joined, "910") || strings.Contains(joined, "private") {
		t.Fatalf("audit leaked values: %v", fields)
	}
}

func TestProfileInitials(t *testing.T) {
	for input, want := range map[string]string{"Ana Silva": "AS", "Rui": "R", "": "?"} {
		if got := profileInitialsText(input); got != want {
			t.Errorf("profileInitialsText(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestProfileGetAndPostRenderAndPersistOwnProfile(t *testing.T) {
	userID := uuid.New()
	updated := time.Date(2026, 9, 4, 8, 0, 0, 0, time.UTC)
	record := dbgen.GetMemberProfileRow{ID: userID, Name: "Ana Silva", DateOfBirth: pgtype.Date{Time: time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC), Valid: true}, IsActive: true, MedicalDeclaration: "NONE_KNOWN", UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}, IdentityUpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}
	store := &profileWorkflowStore{record: record}
	h := Profile{Store: store, Location: time.UTC}
	actor := CurrentUser{ID: userID, Name: "Ana Silva", EmailVerified: true}

	get := httptest.NewRequest(http.MethodGet, "/perfil", nil)
	get = get.WithContext(context.WithValue(get.Context(), currentUserKey{}, actor))
	getResponse := httptest.NewRecorder()
	h.Get(getResponse, get)
	if getResponse.Code != http.StatusOK || getResponse.Header().Get("Cache-Control") != "private, no-store" || getResponse.Header().Get("Pragma") != "no-cache" || !strings.Contains(getResponse.Body.String(), "Perfil de Ana Silva") {
		t.Fatalf("GET response = %d, body=%s", getResponse.Code, getResponse.Body.String())
	}

	values := url.Values{"profile_updated_at": {updated.Format(time.RFC3339Nano)}, "medical_declaration": {"NONE_KNOWN"}, "phone": {"+351 912 626 410"}}
	post := httptest.NewRequest(http.MethodPost, "/perfil", strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post = post.WithContext(context.WithValue(post.Context(), currentUserKey{}, actor))
	postResponse := httptest.NewRecorder()
	h.Post(postResponse, post)
	if postResponse.Code != http.StatusSeeOther || postResponse.Header().Get("Location") != "/perfil" || store.update.SubjectID != userID || store.update.Profile.Phone != "+351 912 626 410" {
		t.Fatalf("POST response=%d location=%q update=%+v", postResponse.Code, postResponse.Header().Get("Location"), store.update)
	}
}

func TestProfileCompletionDoesNotRequireOptionalHealthData(t *testing.T) {
	record := dbgen.GetMemberProfileRow{EmergencyContactName: "Rui", EmergencyContactRelationship: "Tutor", EmergencyContactPhone: "+351 912 000 000", MedicalDeclaration: "UNKNOWN"}
	if !profileComplete(record) {
		t.Fatal("optional health declaration made an emergency-complete profile incomplete")
	}
	record.EmergencyContactPhone = ""
	if profileComplete(record) {
		t.Fatal("profile without a complete emergency contact was marked complete")
	}
}

func TestProfileHandlersKeepPrivateDataBehindAuthorizationAndExposeUpdateOutcomes(t *testing.T) {
	userID := uuid.New()
	updated := time.Date(2026, 9, 4, 8, 0, 0, 0, time.UTC)
	record := dbgen.GetMemberProfileRow{ID: userID, Name: "Ana Silva", DateOfBirth: pgtype.Date{Time: time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC), Valid: true}, IsActive: true, MedicalDeclaration: "NONE_KNOWN", UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}, IdentityUpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}
	form := url.Values{"profile_updated_at": {updated.Format(time.RFC3339Nano)}, "medical_declaration": {"NONE_KNOWN"}}

	t.Run("forbidden and missing profiles do not render sensitive data", func(t *testing.T) {
		for _, err := range []error{ErrProfileForbidden, pgx.ErrNoRows} {
			h := Profile{Store: &profileWorkflowStore{record: record, viewErr: err}, Location: time.UTC}
			r := httptest.NewRequest(http.MethodGet, "/perfil/dependentes/"+userID.String(), nil)
			r.SetPathValue("id", userID.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New()}))
			w := httptest.NewRecorder()
			h.Get(w, r)
			if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "Ana Silva") {
				t.Fatalf("error=%v response=%d body=%s", err, w.Code, w.Body.String())
			}
		}
	})

	t.Run("non-editor cannot submit an update", func(t *testing.T) {
		h := Profile{Store: &profileWorkflowStore{record: record}, Location: time.UTC}
		r := httptest.NewRequest(http.MethodPost, "/perfil", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New()}))
		w := httptest.NewRecorder()
		h.Post(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("response=%d", w.Code)
		}
	})

	for name, updateErr := range map[string]error{"stale update": ErrProfileConflict, "duplicate identity": errors.New("duplicate")} {
		t.Run(name, func(t *testing.T) {
			store := &profileWorkflowStore{record: record, updateErr: updateErr}
			h := Profile{Store: store, Location: time.UTC}
			r := httptest.NewRequest(http.MethodPost, "/perfil", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID}))
			w := httptest.NewRecorder()
			h.Post(w, r)
			if updateErr == ErrProfileConflict {
				if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "Outra pessoa alterou") {
					t.Fatalf("response=%d body=%s", w.Code, w.Body.String())
				}
				return
			}
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("response=%d", w.Code)
			}
		})
	}

	t.Run("new health data requires explicit consent", func(t *testing.T) {
		store := &profileWorkflowStore{record: record, updateErr: ErrHealthConsentRequired}
		h := Profile{Store: store, Location: time.UTC, HealthVersion: "2026-09-06", HealthSHA256: "digest"}
		values := cloneValues(form)
		values.Set("medical_declaration", "PROVIDED")
		values.Set("allergies", "Pólen")
		values.Set("accept_health_data", "on")
		r := httptest.NewRequest(http.MethodPost, "/perfil", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("User-Agent", "MyCFCoimbra test agent")
		r = r.WithContext(context.WithValue(httpx.WithRemoteIP(r.Context(), netip.MustParseAddr("192.0.2.10")), currentUserKey{}, CurrentUser{ID: userID}))
		w := httptest.NewRecorder()
		h.Post(w, r)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "consentimento explícito") || store.update.IP == nil || store.update.UserAgent != "MyCFCoimbra test agent" || store.update.HealthVersion != "2026-09-06" || !store.update.AcceptHealthConsent {
			t.Fatalf("response=%d update=%#v body=%s", w.Code, store.update, w.Body.String())
		}
	})

	t.Run("database unique violation retains the submitted identity form", func(t *testing.T) {
		store := &profileWorkflowStore{record: record, updateErr: &pgconn.PgError{Code: "23505"}}
		h := Profile{Store: store, Location: time.UTC}
		values := cloneValues(form)
		values.Set("email", "ana.nova@example.test")
		values.Set("identity_updated_at", updated.Format(time.RFC3339Nano))
		r := httptest.NewRequest(http.MethodPost, "/perfil", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID}))
		w := httptest.NewRecorder()
		h.Post(w, r)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "já pertence") || !strings.Contains(w.Body.String(), "ana.nova@example.test") {
			t.Fatalf("response=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("profile read failure prevents mutation", func(t *testing.T) {
		store := &profileWorkflowStore{record: record, viewErr: errors.New("database unavailable")}
		r := httptest.NewRequest(http.MethodPost, "/perfil", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID}))
		w := httptest.NewRecorder()
		(Profile{Store: store, Location: time.UTC}).Post(w, r)
		if w.Code != http.StatusInternalServerError || store.update.SubjectID != uuid.Nil {
			t.Fatalf("response=%d update=%#v", w.Code, store.update)
		}
	})
}

func TestProfilePhotoUploadRemovalAndAvatarFallbackArePrivate(t *testing.T) {
	userID := uuid.New()
	oldKey := "profiles/old.png"
	store := &profileWorkflowStore{record: dbgen.GetMemberProfileRow{ID: userID, Name: "Ana Silva", IsActive: true}, removed: &oldKey, avatar: dbgen.GetMemberAvatarRow{Name: "Ana Silva"}}
	objects := &profileObjectStoreFake{}
	h := Profile{Store: store, Objects: objects, Location: time.UTC, Now: func() time.Time { return time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC) }, ImageVersion: "v1", ImageSHA256: "hash"}
	actor := CurrentUser{ID: userID, Name: "Ana Silva", EmailVerified: true}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("accept_image_use", "yes")
	part, err := writer.CreateFormFile("photo", "avatar.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(pngPhoto(t))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	upload := httptest.NewRequest(http.MethodPost, "/perfil/foto", &body)
	upload.Header.Set("Content-Type", writer.FormDataContentType())
	upload = upload.WithContext(context.WithValue(upload.Context(), currentUserKey{}, actor))
	uploadResponse := httptest.NewRecorder()
	h.UploadPhoto(uploadResponse, upload)
	if uploadResponse.Code != http.StatusSeeOther || objects.puts != 1 || objects.deletes != 1 || store.photo.SubjectID != userID || !store.photo.AcceptConsent || store.photo.ObjectKey == "" {
		t.Fatalf("upload=%d puts=%d deletes=%d photo=%+v", uploadResponse.Code, objects.puts, objects.deletes, store.photo)
	}

	avatar := httptest.NewRequest(http.MethodGet, "/membros/"+userID.String()+"/foto", nil)
	avatar.SetPathValue("id", userID.String())
	avatar = avatar.WithContext(context.WithValue(avatar.Context(), currentUserKey{}, actor))
	avatarResponse := httptest.NewRecorder()
	h.Avatar(avatarResponse, avatar)
	if avatarResponse.Code != http.StatusOK || avatarResponse.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(avatarResponse.Body.String(), ">AS<") {
		t.Fatalf("avatar=%d headers=%v body=%s", avatarResponse.Code, avatarResponse.Header(), avatarResponse.Body.String())
	}

	t.Run("current consent photo is proxied privately", func(t *testing.T) {
		photoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("private-image"))
		}))
		defer photoServer.Close()
		key, contentType := "profiles/current.png", "image/png"
		photoStore := &profileWorkflowStore{avatar: dbgen.GetMemberAvatarRow{Name: "Ana Silva", PhotoObjectKey: &key, PhotoContentType: &contentType, ConsentCurrent: true}}
		photoHandler := Profile{Store: photoStore, Objects: &profileObjectStoreFake{presignedURL: photoServer.URL}, ImageVersion: "v1", ImageSHA256: "hash"}
		r := httptest.NewRequest(http.MethodGet, "/membros/"+userID.String()+"/foto", nil)
		r.SetPathValue("id", userID.String())
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, actor))
		w := httptest.NewRecorder()
		photoHandler.Avatar(w, r)
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != contentType || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Body.String() != "private-image" {
			t.Fatalf("response=%d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
		}
	})

	remove := httptest.NewRequest(http.MethodPost, "/perfil/foto/remover", strings.NewReader("confirm_removal=yes"))
	remove.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	remove = remove.WithContext(context.WithValue(remove.Context(), currentUserKey{}, actor))
	removeResponse := httptest.NewRecorder()
	h.RemovePhoto(removeResponse, remove)
	if removeResponse.Code != http.StatusSeeOther || objects.deletes != 2 {
		t.Fatalf("remove=%d deletes=%d", removeResponse.Code, objects.deletes)
	}
}

func TestProfilePhotoUploadCompensatesObjectAndRendersConsentFailure(t *testing.T) {
	userID := uuid.New()
	store := &profileWorkflowStore{record: dbgen.GetMemberProfileRow{ID: userID, Name: "Ana Silva", IsActive: true}, photoErr: ErrConsentRequired}
	objects := &profileObjectStoreFake{}
	h := Profile{Store: store, Objects: objects, Location: time.UTC, ImageVersion: "v1", ImageSHA256: "hash"}
	actor := CurrentUser{ID: userID}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("photo", "avatar.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(pngPhoto(t))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/perfil/foto", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, actor))
	w := httptest.NewRecorder()
	h.UploadPhoto(w, r)
	if w.Code != http.StatusUnprocessableEntity || objects.puts != 1 || objects.deletes != 1 || !strings.Contains(w.Body.String(), "necessário aceitar") {
		t.Fatalf("response=%d puts=%d deletes=%d body=%s", w.Code, objects.puts, objects.deletes, w.Body.String())
	}
}

func TestProfilePhotoMutationsMapStorageAndPersistenceFailures(t *testing.T) {
	userID := uuid.New()
	actor := CurrentUser{ID: userID, EmailVerified: true}
	newUpload := func(t *testing.T) *http.Request {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("accept_image_use", "yes")
		part, err := writer.CreateFormFile("photo", "avatar.png")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(pngPhoto(t))
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/perfil/foto", &body)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, actor))
	}

	t.Run("upload object failure is internal", func(t *testing.T) {
		objects := &profileObjectStoreFake{putErr: errors.New("minio unavailable")}
		h := Profile{Store: &profileWorkflowStore{record: dbgen.GetMemberProfileRow{ID: userID, IsActive: true}}, Objects: objects, Location: time.UTC}
		w := httptest.NewRecorder()
		h.UploadPhoto(w, newUpload(t))
		if w.Code != http.StatusInternalServerError || objects.puts != 1 || objects.deletes != 0 {
			t.Fatalf("response=%d puts=%d deletes=%d", w.Code, objects.puts, objects.deletes)
		}
	})

	t.Run("persistence failure compensates uploaded object", func(t *testing.T) {
		objects := &profileObjectStoreFake{}
		h := Profile{Store: &profileWorkflowStore{record: dbgen.GetMemberProfileRow{ID: userID, IsActive: true}, photoErr: errors.New("database unavailable")}, Objects: objects, Location: time.UTC}
		w := httptest.NewRecorder()
		h.UploadPhoto(w, newUpload(t))
		if w.Code != http.StatusInternalServerError || objects.puts != 1 || objects.deletes != 1 {
			t.Fatalf("response=%d puts=%d deletes=%d", w.Code, objects.puts, objects.deletes)
		}
	})

	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"missing photo", pgx.ErrNoRows, http.StatusNotFound},
		{"forbidden subject", ErrProfileForbidden, http.StatusForbidden},
		{"database failure", errors.New("database unavailable"), http.StatusInternalServerError},
	} {
		t.Run("remove "+tc.name, func(t *testing.T) {
			h := Profile{Store: &profileWorkflowStore{removeErr: tc.err}, Objects: &profileObjectStoreFake{}}
			r := httptest.NewRequest(http.MethodPost, "/perfil/foto/remover", strings.NewReader("confirm_removal=yes"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, actor))
			w := httptest.NewRecorder()
			h.RemovePhoto(w, r)
			if w.Code != tc.want {
				t.Fatalf("response=%d want=%d", w.Code, tc.want)
			}
		})
	}
}

func TestProfileRemovePhotoPageRendersConfirmedDestructiveTask(t *testing.T) {
	userID := uuid.New()
	key := "profiles/current.png"
	h := Profile{Store: &profileWorkflowStore{record: dbgen.GetMemberProfileRow{ID: userID, Name: "Ana Silva", IsActive: true, PhotoObjectKey: &key}}, Location: time.UTC}
	r := httptest.NewRequest(http.MethodGet, "/perfil/fotografia/remover", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, Name: "Ana Silva", EmailVerified: true}))
	w := httptest.NewRecorder()

	h.RemovePhotoPage(w, r)

	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(w.Body.String(), "Remover fotografia") || !strings.Contains(w.Body.String(), "confirm_removal") {
		t.Fatalf("response=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
	}
}

func TestProfileRemovePhotoPageFailsClosedForMissingAndUnauthorizedPhotos(t *testing.T) {
	actorID, subjectID := uuid.New(), uuid.New()
	key := "profiles/current.png"
	for _, tc := range []struct {
		name   string
		record dbgen.GetMemberProfileRow
		err    error
		want   int
	}{
		{"missing photo", dbgen.GetMemberProfileRow{ID: actorID, IsActive: true}, nil, http.StatusNotFound},
		{"unavailable profile", dbgen.GetMemberProfileRow{ID: actorID, IsActive: true, PhotoObjectKey: &key}, errors.New("database unavailable"), http.StatusInternalServerError},
		{"unrelated actor", dbgen.GetMemberProfileRow{ID: subjectID, IsActive: true, PhotoObjectKey: &key}, nil, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/perfil/dependentes/"+subjectID.String()+"/fotografia/remover", nil)
			r.SetPathValue("id", subjectID.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID}))
			w := httptest.NewRecorder()
			(Profile{Store: &profileWorkflowStore{record: tc.record, viewErr: tc.err}}).RemovePhotoPage(w, r)
			if w.Code != tc.want {
				t.Fatalf("response=%d want=%d", w.Code, tc.want)
			}
		})
	}
}

func TestProfileAvatarFallsBackToPrivateInitialsWithoutAnEligiblePhoto(t *testing.T) {
	userID := uuid.New()
	h := Profile{Store: &profileWorkflowStore{avatar: dbgen.GetMemberAvatarRow{Name: "Ana Silva"}}}
	r := httptest.NewRequest(http.MethodGet, "/membros/"+userID.String()+"/foto", nil)
	r.SetPathValue("id", userID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID}))
	w := httptest.NewRecorder()

	h.Avatar(w, r)

	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/svg+xml; charset=utf-8" || w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Body.String(), "AS") {
		t.Fatalf("response=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
	}
}

func TestProfileAvatarMapsLookupPresignAndUpstreamFailures(t *testing.T) {
	userID := uuid.New()
	actor := CurrentUser{ID: userID}
	newRequest := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/membros/"+userID.String()+"/foto", nil)
		r.SetPathValue("id", userID.String())
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, actor))
	}
	key, contentType := "profiles/current.png", "image/png"
	current := dbgen.GetMemberAvatarRow{Name: "Ana Silva", PhotoObjectKey: &key, PhotoContentType: &contentType, ConsentCurrent: true}

	for _, tc := range []struct {
		name    string
		store   *profileWorkflowStore
		objects *profileObjectStoreFake
		want    int
	}{
		{"missing member", &profileWorkflowStore{avatarErr: pgx.ErrNoRows}, nil, http.StatusNotFound},
		{"lookup failure", &profileWorkflowStore{avatarErr: errors.New("database unavailable")}, nil, http.StatusInternalServerError},
		{"missing object store", &profileWorkflowStore{avatar: current}, nil, http.StatusInternalServerError},
		{"presign failure", &profileWorkflowStore{avatar: current}, &profileObjectStoreFake{presignErr: errors.New("minio unavailable")}, http.StatusInternalServerError},
		{"invalid presigned URL", &profileWorkflowStore{avatar: current}, &profileObjectStoreFake{presignedURL: "://invalid"}, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := Profile{Store: tc.store}
			if tc.objects != nil {
				h.Objects = tc.objects
			}
			w := httptest.NewRecorder()
			h.Avatar(w, newRequest())
			if w.Code != tc.want {
				t.Fatalf("response=%d want=%d", w.Code, tc.want)
			}
		})
	}

	t.Run("upstream non-success is not exposed", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
		defer server.Close()
		h := Profile{Store: &profileWorkflowStore{avatar: current}, Objects: &profileObjectStoreFake{presignedURL: server.URL}}
		w := httptest.NewRecorder()
		h.Avatar(w, newRequest())
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("response=%d", w.Code)
		}
	})
}

func cloneValues(values url.Values) url.Values {
	clone := url.Values{}
	for key, items := range values {
		clone[key] = append([]string(nil), items...)
	}
	return clone
}
