//go:build integration

package db

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
)

// recordedExpandFixture installs the exact released predecessor baseline in its
// own database, then applies/records the immutable migrations through expand.
// It neither depends on nor rewrites the runner's final-baseline database.
func recordedExpandFixture(t *testing.T) *pgx.Conn {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL required for recorded-expand fixture")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "dated_expand_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.Exec(context.Background(), "DROP DATABASE "+quoted+" WITH (FORCE)")
		if err != nil {
			t.Errorf("cleanup recorded-expand database: %v", err)
		}
		admin.Close(context.Background())
	})
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	exec := func(sql string) {
		t.Helper()
		if _, err := conn.PgConn().Exec(ctx, sql).ReadAll(); err != nil {
			t.Fatalf("install recorded-expand fixture: %v", err)
		}
	}
	compressed, err := os.ReadFile("testdata/dated-participation-predecessor-68ba5f1.sql.gz")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	predecessor, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	const predecessorDigest = "5721695f5da09b3704d0be9e17957bcf25c51c366a6eb3a27c8f204ed78861b2"
	if got := fmt.Sprintf("%x", sha256.Sum256(predecessor)); got != predecessorDigest {
		t.Fatalf("immutable predecessor baseline changed: %s", got)
	}
	exec(string(predecessor))
	exec(`CREATE SCHEMA mycfc_meta;
CREATE TABLE mycfc_meta.schema_migrations(version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
INSERT INTO mycfc_meta.schema_migrations(version) VALUES ('reset-baseline-v1');
INSERT INTO news_items(title_pt,summary_pt,published_at) VALUES ('Notícia retida','Conteúdo retido',now());`)
	const predecessorCutoff = "202609220001_privacy_automation_retirement"
	const expand = "202609290001_dated_participation"
	entries, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	foundExpand := false
	for _, path := range entries {
		version := migrationVersion(path)
		if version > expand {
			break
		}
		if version > predecessorCutoff {
			sql, err := migrationFiles.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			exec(string(sql))
		}
		if _, err := conn.Exec(ctx, "INSERT INTO mycfc_meta.schema_migrations(version) VALUES($1)", version); err != nil {
			t.Fatal(err)
		}
		foundExpand = foundExpand || version == expand
	}
	if !foundExpand {
		t.Fatal("expand migration absent from fixture inventory")
	}
	return conn
}

// This test uses a disposable predecessor schema with the expand-only version
// already in its ledger. The runner must not mistake that marker for contract.
func TestDatedParticipationApplyBaselineFromRecordedExpand(t *testing.T) {
	ctx := context.Background()
	conn := recordedExpandFixture(t)
	defer conn.Close(ctx)
	var oldArbiter, contractMarker bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='user_memberships_user_season_programme_unique')`).Scan(&oldArbiter); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM mycfc_meta.schema_migrations WHERE version='202609290002_dated_participation_contract')`).Scan(&contractMarker); err != nil {
		t.Fatal(err)
	}
	if !oldArbiter || contractMarker {
		t.Fatalf("invalid recorded-expand fixture (old_arbiter=%v final_marker=%v)", oldArbiter, contractMarker)
	}
	if err := VerifyDatedParticipationContract(ctx, conn); err == nil {
		t.Fatal("expand ledger passed final postcondition")
	}
	if _, err := conn.Exec(ctx, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES('202609290002_dated_participation_contract')`); err != nil {
		t.Fatal(err)
	}
	if err := ApplyBaseline(ctx, conn); err == nil {
		t.Fatal("forged final marker allowed migration commit without final schema")
	}
	if _, err := conn.Exec(ctx, `DELETE FROM mycfc_meta.schema_migrations WHERE version='202609290002_dated_participation_contract'`); err != nil {
		t.Fatal(err)
	}
	if err := ApplyBaseline(ctx, conn); err != nil {
		t.Fatalf("apply recorded-expand contract: %v", err)
	}
	if err := VerifyDatedParticipationContract(ctx, conn); err != nil {
		t.Fatalf("final postcondition: %v", err)
	}
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='user_memberships_user_season_programme_unique')`).Scan(&oldArbiter); err != nil {
		t.Fatal(err)
	}
	if oldArbiter {
		t.Fatal("legacy arbiter survived final contract")
	}
	// An exact retry must preserve durable data, not reinstall the baseline.
	if err := ApplyBaseline(ctx, conn); err != nil {
		t.Fatalf("retry final contract: %v", err)
	}
	var news int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM news_items WHERE title_pt='Notícia retida'`).Scan(&news); err != nil || news != 1 {
		t.Fatalf("non-member durable row lost: %d %v", news, err)
	}
}
