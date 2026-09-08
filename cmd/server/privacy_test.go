package main

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestParsePrivacyHoldRequiresScopedPlanInputs(t *testing.T) {
	reference, owner := uuid.New(), uuid.New()
	hold, err := parsePrivacyHold(reference.String(), owner.String(), " identity-core ", " legal_hold ", " legal-file-001 ")
	if err != nil {
		t.Fatal(err)
	}
	if hold.reference != reference || hold.owner != owner || hold.category != "identity-core" || hold.reason != "LEGAL_HOLD" || hold.evidence != "legal-file-001" {
		t.Fatalf("hold=%+v", hold)
	}
	for _, test := range []struct {
		name, reference, owner, category, evidence, want string
	}{
		{"reference", "invalid", owner.String(), "identity-core", "evidence", "--reference"},
		{"owner", reference.String(), "invalid", "identity-core", "evidence", "--owner"},
		{"category", reference.String(), owner.String(), " ", "evidence", "--category"},
		{"evidence", reference.String(), owner.String(), "identity-core", " ", "--evidence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parsePrivacyHold(test.reference, test.owner, test.category, "LEGAL_HOLD", test.evidence)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}
}
