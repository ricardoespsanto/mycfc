package privacyrequests

import "testing"

func TestMediaSourceInventoryIsClosedAndMatchesCryptoContract(t *testing.T) {
	seenKinds := map[string]bool{}
	seenPointers := map[string]bool{}
	for _, contract := range mediaSourceContracts {
		if seenKinds[contract.SourceKind] || seenPointers[contract.Table+"."+contract.ObjectKeyColumn] {
			t.Fatalf("duplicate media contract: %+v", contract)
		}
		seenKinds[contract.SourceKind] = true
		seenPointers[contract.Table+"."+contract.ObjectKeyColumn] = true
		if !validObjectTargetSource(contract.SourceKind) || (contract.Category != "profile-photo" && contract.Category != "object-storage") {
			t.Fatalf("unexecutable media contract: %+v", contract)
		}
		if found, ok := mediaSourceContractFor(contract.SourceKind); !ok || found != contract {
			t.Fatalf("media contract lookup failed: %+v", contract)
		}
	}
	if len(seenKinds) != 3 {
		t.Fatalf("media source count=%d", len(seenKinds))
	}
	if _, ok := mediaSourceContractFor("FUTURE_UNCLASSIFIED_MEDIA"); ok {
		t.Fatal("unknown media source was classified")
	}
}
