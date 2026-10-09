package releasecontract

import "testing"

func TestDisposableBindingRejectsMismatchedRuntime(t *testing.T) {
	oldVersion, oldCandidate := Version, Candidate
	t.Cleanup(func() { Version, Candidate = oldVersion, oldCandidate })
	Version, Candidate = "v0.0.0-rehearsal", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if !Matches(Version, Candidate, Database) {
		t.Fatal("exact candidate binding rejected")
	}
	for _, v := range []struct{ version, candidate, database string }{
		{"v1.25.9", Candidate, Database}, {Version, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Database}, {Version, Candidate, "other"},
	} {
		if Matches(v.version, v.candidate, v.database) {
			t.Fatal("mismatched runtime accepted")
		}
	}
	Version = ""
	if Matches("", Candidate, Database) {
		t.Fatal("unstamped build accepted")
	}
}
