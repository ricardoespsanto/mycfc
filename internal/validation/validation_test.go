package validation

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeName(t *testing.T) {
	got, err := NormalizeName("  Ana   Maria  ")
	if err != nil {
		t.Fatalf("NormalizeName() error = %v", err)
	}
	if got != "Ana Maria" {
		t.Fatalf("NormalizeName() = %q", got)
	}
}

func TestNormalizeEmail(t *testing.T) {
	got, err := NormalizeEmail("  ANA.Example@EXAMPLE.COM ")
	if err != nil {
		t.Fatalf("NormalizeEmail() error = %v", err)
	}
	if got != "ana.example@example.com" {
		t.Fatalf("NormalizeEmail() = %q", got)
	}
}

func TestAdultBoundary(t *testing.T) {
	location, err := time.LoadLocation("Europe/Lisbon")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, location)
	cases := []struct {
		name    string
		birth   time.Time
		wantErr bool
	}{
		{"turns 18 today", time.Date(2008, 7, 22, 0, 0, 0, 0, location), false},
		{"turns 18 tomorrow", time.Date(2008, 7, 23, 0, 0, 0, 0, location), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAdultDateOfBirth(tc.birth, now, location)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestDependentBoundary(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Lisbon")
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, location)
	if err := ValidateDependentDateOfBirth(time.Date(2008, 7, 23, 0, 0, 0, 0, location), now, location); err != nil {
		t.Fatalf("17-year-old rejected: %v", err)
	}
	if err := ValidateDependentDateOfBirth(time.Date(2008, 7, 22, 0, 0, 0, 0, location), now, location); err == nil {
		t.Fatal("18-year-old accepted")
	}
	if err := ValidateDependentDateOfBirth(time.Date(2026, 7, 23, 0, 0, 0, 0, location), now, location); err == nil {
		t.Fatal("future date accepted")
	}
}

func TestValidatePassword(t *testing.T) {
	for _, valid := range []string{"uma palavra-passe 7", "abcdefghijk1"} {
		if err := ValidatePassword(valid); err != nil {
			t.Errorf("valid password %q rejected: %v", valid, err)
		}
	}
	for _, invalid := range []string{"short1", strings.Repeat("a", 12), strings.Repeat("a", 72) + "1"} {
		if err := ValidatePassword(invalid); err == nil {
			t.Errorf("invalid password %q accepted", invalid)
		}
	}
}

func TestSafeNext(t *testing.T) {
	for _, valid := range []string{"/dashboard", "/admin/fleet", "/admin/membros/criar"} {
		if got := SafeNext(valid); got != valid {
			t.Errorf("SafeNext(%q) = %q", valid, got)
		}
	}
	for _, invalid := range []string{"https://attacker.example", "//attacker.example", "/login", "/dashboard?x=1", "/%2f%2fattacker.example"} {
		if got := SafeNext(invalid); got != "" {
			t.Errorf("SafeNext(%q) = %q", invalid, got)
		}
	}
}
