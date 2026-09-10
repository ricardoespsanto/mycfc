//go:build integration

package main

import (
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

	"github.com/jackc/pgx/v5"
)

func TestSyntheticBootstrapUsesTheOrdinaryAuthenticatedV3ReplayAndAttestationPath(t *testing.T) {
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
		result.ReplayedCount != 1 || result.SyntheticReplayedCount != 1 || result.ClosureV3Count != 1 ||
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
	var providerAbsent, consentClockVerified, closureV3, effectiveVerified int
	var evidence []byte
	if err = conn.QueryRow(ctx, `SELECT * FROM privacy_restore_observe_inventory($1,$2,$3,$4,$5,$6,$7)`,
		result.InputSource, inventoryDigest, schemaDigest, result.PolicyVersion, result.ExecutorVersion,
		result.PlanSchemaVersion, result.ImageDigest).Scan(&replayCount, &sourceCount, &syntheticCount, &verifiedRuns,
		&expectedCheckpoints, &succeededCheckpoints, &providerAbsent, &consentClockVerified, &closureV3, &effectiveVerified, &evidence); err != nil {
		t.Fatal(err)
	}
	if replayCount != 1 || sourceCount != 0 || syntheticCount != 1 || verifiedRuns != replayCount ||
		expectedCheckpoints <= 0 || succeededCheckpoints != expectedCheckpoints || providerAbsent != replayCount ||
		consentClockVerified != replayCount || closureV3 != replayCount || effectiveVerified != replayCount || len(evidence) != 32 {
		t.Fatalf("observer replay=%d source=%d synthetic=%d verified=%d checkpoints=%d/%d provider=%d consent=%d closure=%d effective=%d evidence=%d",
			replayCount, sourceCount, syntheticCount, verifiedRuns, expectedCheckpoints, succeededCheckpoints,
			providerAbsent, consentClockVerified, closureV3, effectiveVerified, len(evidence))
	}
}
