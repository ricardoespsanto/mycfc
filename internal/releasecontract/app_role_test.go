package releasecontract

import "testing"

func TestAppRoleOnlyHandsOffStampedOldWebPrincipal(t *testing.T) {
	oldVersion, oldCandidate := Version, Candidate
	t.Cleanup(func() { Version, Candidate = oldVersion, oldCandidate })
	for _, tc := range []struct{ version, candidate, role, want string }{
		{"v0.0.0-ci", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", OldWebRole, WebRole},
		{"v0.0.0-ci", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", WebRole, WebRole},
		{"v0.0.0-ci", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "postgres", "postgres"},
		{"", "", OldWebRole, OldWebRole},
		{"v01.2.3", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", OldWebRole, OldWebRole},
		{"v1.2.3", "not-a-sha", OldWebRole, OldWebRole},
	} {
		Version, Candidate = tc.version, tc.candidate
		if got := AppRole(tc.role); got != tc.want {
			t.Fatalf("%+v role=%s", tc, got)
		}
	}
}
