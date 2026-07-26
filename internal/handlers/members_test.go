package handlers

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

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
