package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/releasecontract"
)

func TestTrainingRecipientNormalPinsAndExplicitPhases(t *testing.T) {
	if EmbeddedMigrationDigest() != releasecontract.NormalFinalDigest || EmbeddedMigrationDigest() == releasecontract.FinalDigest {
		t.Fatal("normal final and retired reset pins not separated")
	}
	sum := sha256.Sum256([]byte(baselineSchema))
	if hex.EncodeToString(sum[:]) != releasecontract.NormalBaselineDigest {
		t.Fatal("normal baseline pin mismatch")
	}
	for digest, want := range map[string]RecipientSchemaPhase{releasecontract.FreshGenesisDigest: RecipientReviewed77, releasecontract.NormalParentDigest: RecipientReviewed78, releasecontract.NormalRecipientParentDigest: RecipientReviewed79, releasecontract.NormalAuthoringParentDigest: RecipientReviewed80, releasecontract.NormalFinalDigest: RecipientFinal} {
		versions, phase := trainingRecipientInventory(digest)
		if phase != want || versions == nil {
			t.Fatalf("reviewed inventory missing %s", want)
		}
		expected := map[RecipientSchemaPhase]int{RecipientReviewed77: 77, RecipientReviewed78: 78, RecipientReviewed79: 79, RecipientReviewed80: 80, RecipientFinal: 84}[want]
		if len(versions) != expected {
			t.Fatalf("phase %s count=%d", want, len(versions))
		}
	}
	if versions, _ := trainingRecipientInventory(strings.Repeat("0", 64)); versions != nil {
		t.Fatal("unreviewed inventory accepted")
	}
	if _, err := InspectTrainingRecipientDatabase(context.Background(), nil, RecipientDatabaseInspectionInput{}); err == nil {
		t.Fatal("unbounded/unauthenticated inspection accepted")
	}
}
