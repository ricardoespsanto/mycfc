//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestSyntheticBootstrapUsesTheOrdinaryAuthenticatedV4ReplayAndAttestationPath(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS mycfc_meta;
	 CREATE TABLE IF NOT EXISTS mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now());
	 INSERT INTO mycfc_meta.schema_migrations(version) VALUES('202609100012_privacy_restore_replay_hardening') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	conn.Close(ctx)

	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	keyPath, inventoryPath := filepath.Join(directory, "key"), filepath.Join(directory, "synthetic-ledger.json")
	resultPath, repeatedResultPath := filepath.Join(directory, "result.json"), filepath.Join(directory, "result-repeated.json")
	if err = os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(privateKey.Bytes())), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", dsn)
	if err = run(ctx, []string{"--isolated-restore", "--bootstrap-synthetic-fixture", "--private-key-file", keyPath, "--synthetic-ledger-output", inventoryPath}); err != nil {
		t.Fatal(err)
	}
	imageDigest := "sha256:" + strings.Repeat("a", 64)
	if err = run(ctx, []string{"--isolated-restore", "--ledger-input", inventoryPath, "--private-key-file", keyPath,
		"--attestation-output", resultPath, "--policy-version", "synthetic-policy-v1", "--executor-version", "privacy-erasure-executor/v2",
		"--plan-schema-version", "privacy-erasure-plan/v2", "--image-digest", imageDigest}); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	var result replayAttestation
	if err = json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	if result.Contract != replayResultContract || result.Result != "SUCCEEDED" || result.InputSource != "SYNTHETIC_BOOTSTRAP" ||
		result.ReplayedCount != 1 || result.SyntheticReplayedCount != 1 || result.ClosureV4Count != 1 ||
		result.ErasureEffectiveAtVerifiedCount != 1 || result.IntentOnlyCount != 0 || result.LegacyClosureV2Count != 0 || result.AbsenceVerifiedCount != 1 {
		t.Fatalf("synthetic replay result=%+v", result)
	}
	if err = run(ctx, []string{"--isolated-restore", "--ledger-input", inventoryPath, "--private-key-file", keyPath,
		"--attestation-output", repeatedResultPath, "--policy-version", "synthetic-policy-v1", "--executor-version", "privacy-erasure-executor/v2",
		"--plan-schema-version", "privacy-erasure-plan/v2", "--image-digest", imageDigest}); err != nil {
		t.Fatal(err)
	}
	repeatedEncoded, err := os.ReadFile(repeatedResultPath)
	if err != nil {
		t.Fatal(err)
	}
	var repeated replayAttestation
	if err = json.Unmarshal(repeatedEncoded, &repeated); err != nil {
		t.Fatal(err)
	}
	if repeated != result {
		t.Fatalf("idempotent replay changed result first=%+v repeated=%+v", result, repeated)
	}
	conn, err = pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	inventoryDigest, _ := hex.DecodeString(result.InventorySHA256)
	schemaDigest, _ := hex.DecodeString(result.SchemaMigrationDigest)
	var replayCount, sourceCount, syntheticCount, verifiedRuns, expectedCheckpoints, succeededCheckpoints int
	var providerAbsent, consentClockVerified, closureV4, effectiveVerified, membershipVerified int
	var membershipContract string
	var membershipDigest, evidence []byte
	var membershipCount, variationCount int64
	if err = conn.QueryRow(ctx, `SELECT * FROM privacy_restore_observe_inventory($1,$2,$3,$4,$5,$6,$7)`,
		result.InputSource, inventoryDigest, schemaDigest, result.PolicyVersion, result.ExecutorVersion,
		result.PlanSchemaVersion, result.ImageDigest).Scan(&replayCount, &sourceCount, &syntheticCount, &verifiedRuns,
		&expectedCheckpoints, &succeededCheckpoints, &providerAbsent, &consentClockVerified, &closureV4, &effectiveVerified,
		&membershipContract, &membershipDigest, &membershipVerified, &membershipCount, &variationCount, &evidence); err != nil {
		t.Fatal(err)
	}
	if replayCount != 1 || sourceCount != 0 || syntheticCount != 1 || verifiedRuns != replayCount ||
		expectedCheckpoints <= 0 || succeededCheckpoints != expectedCheckpoints || providerAbsent != replayCount ||
		consentClockVerified != replayCount || closureV4 != replayCount || effectiveVerified != replayCount ||
		membershipContract != "mycfc/membership-history-postcondition/v1" || len(membershipDigest) != 32 || membershipVerified != replayCount ||
		membershipCount != 1 || variationCount != 1 || len(evidence) != 32 {
		t.Fatalf("observer replay=%d source=%d synthetic=%d verified=%d checkpoints=%d/%d provider=%d consent=%d closure=%d effective=%d evidence=%d",
			replayCount, sourceCount, syntheticCount, verifiedRuns, expectedCheckpoints, succeededCheckpoints,
			providerAbsent, consentClockVerified, closureV4, effectiveVerified, len(evidence))
	}

	assertSyntheticMembershipPostconditionIsComplete(t, ctx, conn)
}

func assertSyntheticMembershipPostconditionIsComplete(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var runID, subjectID, membershipID, variationID uuid.UUID
	var effectiveAt time.Time
	var expectedDigest []byte
	if err = tx.QueryRow(ctx, `SELECT run.id,imported.subject_user_id,imported.erasure_effective_at,row.membership_id,variation.id,
		imported.membership_postcondition_sha256
		FROM privacy_protected.restore_replay_runs run
		JOIN privacy_protected.restore_ledger_imports imported ON imported.id=run.import_id
		JOIN privacy_protected.membership_history_replay_rows row ON row.run_id=run.id
		JOIN training_variations variation ON variation.target_membership_id=row.membership_id
		WHERE imported.synthetic_fixture='mycfc/privacy-restore-synthetic-fixture/v1'
		ORDER BY run.started_at DESC LIMIT 1`).Scan(&runID, &subjectID, &effectiveAt, &membershipID, &variationID, &expectedDigest); err != nil {
		t.Fatal(err)
	}
	var retainedText string
	if err = tx.QueryRow(ctx, `SELECT change_summary||' '||patch::text FROM training_variations WHERE id=$1`, variationID).Scan(&retainedText); err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"Synthetic restore fixture", subjectID.String(), "synthetic-" + subjectID.String() + "@invalid.invalid"} {
		if strings.Contains(retainedText, canary) {
			t.Fatalf("retained variation still contains erased identity canary")
		}
	}

	computeReplay := func() ([]byte, int64, int64, error) {
		var digest []byte
		var membershipCount, variationCount int64
		err := tx.QueryRow(ctx, `SELECT postcondition_sha256,membership_count,variation_count
			FROM privacy_membership_history_compute_replay($1,$2)`, runID, effectiveAt).
			Scan(&digest, &membershipCount, &variationCount)
		return digest, membershipCount, variationCount, err
	}
	assertCurrent := func(label string, wantEqual bool) {
		t.Helper()
		digest, membershipCount, variationCount, computeErr := computeReplay()
		if computeErr != nil {
			t.Fatalf("%s compute: %v", label, computeErr)
		}
		equal := bytes.Equal(digest, expectedDigest)
		if equal != wantEqual || membershipCount != 1 || variationCount != 1 {
			t.Fatalf("%s equal=%t memberships=%d variations=%d", label, equal, membershipCount, variationCount)
		}
	}
	withSavepoint := func(name string, probe func()) {
		t.Helper()
		if _, err = tx.Exec(ctx, "SAVEPOINT "+name); err != nil {
			t.Fatal(err)
		}
		probe()
		if _, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "RELEASE SAVEPOINT "+name); err != nil {
			t.Fatal(err)
		}
	}

	assertCurrent("unchanged", true)
	withSavepoint("principal_excluded", func() {
		var principalID uuid.UUID
		if err = tx.QueryRow(ctx, `INSERT INTO privacy_pseudonymous_principals(purpose) VALUES('MEMBERSHIP') RETURNING id`).Scan(&principalID); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE user_memberships SET principal_id=$2 WHERE id=$1`, membershipID, principalID); err != nil {
			t.Fatal(err)
		}
		assertCurrent("principal exclusion", true)
	})
	withSavepoint("date_corruption", func() {
		if _, err = tx.Exec(ctx, `UPDATE user_memberships SET ends_on=ends_on-1 WHERE id=$1`, membershipID); err != nil {
			t.Fatal(err)
		}
		assertCurrent("membership date corruption", false)
	})
	withSavepoint("identity_corruption", func() {
		if _, err = tx.Exec(ctx, `UPDATE training_variations SET change_summary=$2,
			patch=jsonb_set(patch,'{nested,old_email}',to_jsonb($3::text),true) WHERE id=$1`, variationID,
			"Synthetic restore fixture "+subjectID.String(), "synthetic-"+subjectID.String()+"@invalid.invalid"); err != nil {
			t.Fatal(err)
		}
		assertCurrent("old identity corruption", false)
	})
	withSavepoint("json_corruption", func() {
		if _, err = tx.Exec(ctx, `UPDATE training_variations SET patch=jsonb_set(patch,'{nested,unexpected}','"old-login"'::jsonb,true) WHERE id=$1`, variationID); err != nil {
			t.Fatal(err)
		}
		assertCurrent("nested JSON corruption", false)
	})
	withSavepoint("json_reorder", func() {
		if _, err = tx.Exec(ctx, `UPDATE training_variations variation SET patch=(SELECT jsonb_object_agg(entry.key,entry.value ORDER BY entry.key DESC)
			FROM jsonb_each(variation.patch) entry) WHERE variation.id=$1`, variationID); err != nil {
			t.Fatal(err)
		}
		assertCurrent("canonical JSON reorder", true)
	})
	withSavepoint("missing_row", func() {
		var missingDigest []byte
		if err = tx.QueryRow(ctx, `SELECT postcondition_sha256 FROM privacy_membership_history_compute(ARRAY[]::uuid[],$1,false)`, effectiveAt).Scan(&missingDigest); err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(missingDigest, expectedDigest) {
			t.Fatal("missing membership row preserved digest")
		}
	})
	withSavepoint("extra_row", func() {
		var ignored []byte
		if err = tx.QueryRow(ctx, `SELECT postcondition_sha256 FROM privacy_membership_history_compute(ARRAY[$1,$2]::uuid[],$3,false)`, membershipID, uuid.New(), effectiveAt).Scan(&ignored); err == nil {
			t.Fatal("extra nonexistent membership row was accepted")
		}
	})
	withSavepoint("row_reorder", func() {
		var seasonID, secondMembership uuid.UUID
		var programmeID, principalID uuid.UUID
		if err = tx.QueryRow(ctx, `SELECT programme_id,principal_id FROM user_memberships WHERE id=$1`, membershipID).Scan(&programmeID, &principalID); err != nil {
			t.Fatal(err)
		}
		seasonID, secondMembership = uuid.New(), uuid.New()
		if _, err = tx.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current)
			VALUES($1,$2,'Postcondition order fixture',$3::date-interval '4 years',$3::date-interval '3 years',false)`,
			seasonID, "ORD-"+seasonID.String()[:8], effectiveAt); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO user_memberships(id,user_id,principal_id,season_id,programme_id,starts_on,ends_on)
			VALUES($1,NULL,$2,$3,$4,$5::date-interval '4 years',$5::date-interval '3 years')`,
			secondMembership, principalID, seasonID, programmeID, effectiveAt); err != nil {
			t.Fatal(err)
		}
		var forward, reverse []byte
		if err = tx.QueryRow(ctx, `SELECT postcondition_sha256 FROM privacy_membership_history_compute(ARRAY[$1,$2]::uuid[],$3,true)`, membershipID, secondMembership, effectiveAt).Scan(&forward); err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(ctx, `SELECT postcondition_sha256 FROM privacy_membership_history_compute(ARRAY[$2,$1]::uuid[],$3,true)`, membershipID, secondMembership, effectiveAt).Scan(&reverse); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(forward, reverse) {
			t.Fatal("membership row order changed canonical digest")
		}
	})
}
