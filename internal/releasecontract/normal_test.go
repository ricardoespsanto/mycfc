package releasecontract

import "testing"

func TestCorrectedNormalContractLeavesRetiredResetPinsUntouched(t *testing.T) {
	if FinalDigest != "88bdc8ccf003c6a4994310c11e2d1c18ce5d4b212ccb6eff8113f8499b9152ec" || BaselineDigest != "fc8f9dbb96dcfd7f49aa81e562bf53684416a32282a26d4d9a9d329f20c14a1f" || PredecessorDigest != "8ad238f2a1e36976bebd8f6950c935c9fa5b72862fe7cb14d2ad1149d37d1a4e" || ReviewedParent != "e90033d" {
		t.Fatal("retired reset admission changed")
	}
	if NormalFinalDigest == FinalDigest || NormalRecipientParentDigest != FinalDigest || NormalParentDigest == NormalRecipientParentDigest {
		t.Fatal("normal reviewed parent/final lineage not separated")
	}
}
