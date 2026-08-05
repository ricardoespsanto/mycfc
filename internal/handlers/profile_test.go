package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestProfileAuthorizationBoundaries(t *testing.T) {
	adultID, dependentID, guardianID := uuid.New(), uuid.New(), uuid.New()
	adult := dbgen.GetMemberProfileRow{ID: adultID, IsActive: true}
	dependent := dbgen.GetMemberProfileRow{ID: dependentID, GuardianID: &guardianID, IsDependent: true, IsActive: true}
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
	dependent.IsActive = false
	if canViewProfile(dependent, guardianID, false) || canEditProfile(dependent, guardianID, false) {
		t.Fatal("inactive dependent is exposed to non-admin")
	}
	if !canViewProfile(dependent, uuid.New(), true) || !canEditProfile(dependent, uuid.New(), true) {
		t.Fatal("administrator cannot manage inactive profile")
	}
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

func cloneValues(values url.Values) url.Values {
	clone := url.Values{}
	for key, items := range values {
		clone[key] = append([]string(nil), items...)
	}
	return clone
}
