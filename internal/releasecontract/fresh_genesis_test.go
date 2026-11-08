package releasecontract

import "testing"

func TestFreshGenesisCompatibilityIsPinnedAndForwardOnly(t *testing.T) {
	oldVersion, oldCandidate := Version, Candidate
	t.Cleanup(func() { Version, Candidate = oldVersion, oldCandidate })
	Candidate = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for _, tc := range []struct {
		version, genesisVersion, genesisCandidate string
		want                                      bool
	}{
		{"v2.0.3", FreshGenesisVersion, FreshGenesisCandidate, true},
		{"v2.1.0", FreshGenesisVersion, FreshGenesisCandidate, true},
		{"v2.0.2", FreshGenesisVersion, FreshGenesisCandidate, false},
		{"v2.0.1", FreshGenesisVersion, FreshGenesisCandidate, false},
		{"v2.0.3-rc.1", FreshGenesisVersion, FreshGenesisCandidate, false},
		{"v2.0.3", FreshGenesisVersion, "cccccccccccccccccccccccccccccccccccccccc", false},
		{"v2.0.3", "v2.0.1", FreshGenesisCandidate, false},
		{"v2.0.3", "v2.0.3", Candidate, true},
	} {
		Version = tc.version
		if got := CompatibleFreshGenesis(Database, Version, Candidate, tc.genesisVersion, tc.genesisCandidate); got != tc.want {
			t.Errorf("version=%s genesis=%s compatible=%t want=%t", tc.version, tc.genesisVersion, got, tc.want)
		}
	}
	Version = "v2.0.3"
	if CompatibleFreshGenesis("other", Version, Candidate, FreshGenesisVersion, FreshGenesisCandidate) ||
		CompatibleFreshGenesis(Database, Version, "cccccccccccccccccccccccccccccccccccccccc", FreshGenesisVersion, FreshGenesisCandidate) {
		t.Fatal("wrong database or binary/runtime identity accepted")
	}
}
