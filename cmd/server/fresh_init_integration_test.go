//go:build integration

package main

import (
	"context"
	"fmt"
	urlpkg "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The protected production genesis was created by this exact signed source,
// not by relabeling the current candidate's expanded baseline as v2.0.2.
func originalFreshInitBinary(t *testing.T) string {
	t.Helper()
	const source = "dbe7eb1771af741c3c41008bcf9e92986310ff3c"
	if source != releasecontract.FreshGenesisCandidate {
		t.Fatal("pinned fresh genesis source drift")
	}
	directory := t.TempDir()
	archive := exec.Command("git", "archive", "--format=tar", source)
	archive.Dir = filepath.Join("..", "..")
	stream, err := archive.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	extract := exec.Command("tar", "-xf", "-", "-C", directory)
	extract.Stdin = stream
	if err := archive.Start(); err != nil {
		t.Fatalf("original genesis source unavailable: %v", err)
	}
	extractErr := extract.Run()
	archiveErr := archive.Wait()
	if archiveErr != nil || extractErr != nil {
		t.Fatalf("archive original genesis source: git=%v tar=%v", archiveErr, extractErr)
	}
	binary := filepath.Join(directory, "original-fresh-init")
	build := exec.Command("go", "build", "-trimpath", "-ldflags=-X github.com/cfcoimbra/mycfc/internal/releasecontract.Version=v2.0.2 -X github.com/cfcoimbra/mycfc/internal/releasecontract.Candidate="+source, "-o", binary, "./cmd/server")
	build.Dir = directory
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build original genesis binary: %v: %s", err, output)
	}
	return binary
}

// TEST_FRESH_INIT_URL may point to an explicitly dedicated empty mycfc DB.
// The CI path creates a separate disposable PostgreSQL container because a
// stamped release can only initialize the fixed database name, never mycfc_test.
func TestExplicitFreshInitRealPostgres(t *testing.T) {
	url := os.Getenv("TEST_FRESH_INIT_URL")
	ctx := context.Background()
	if url == "" {
		url = isolatedFreshInitURL(t)
	}
	parsed, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Database != releasecontract.Database {
		t.Fatal("fresh integration fixture must use a dedicated mycfc database")
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	role := func(kind string) string { return "fresh_" + kind + "_" + suffix }
	app, migration, media, retention := role("web"), role("migration"), role("media"), role("retention")
	t.Setenv("DATABASE_URL", url)
	t.Setenv("APP_DB_USER", app)
	t.Setenv("APP_DB_PASSWORD", "test-web-"+suffix)
	t.Setenv("MIGRATION_DB_USER", migration)
	t.Setenv("MIGRATION_DB_PASSWORD", "test-migration-"+suffix)
	t.Setenv("MEDIA_CLEANUP_DB_USER", media)
	t.Setenv("MEDIA_CLEANUP_DB_PASSWORD", "test-media-"+suffix)
	t.Setenv("DATA_RETENTION_DB_USER", retention)
	t.Setenv("DATA_RETENTION_DB_PASSWORD", "test-retention-"+suffix)
	if err := runServerCommand(ctx, []string{"fresh-init-db"}); err == nil || !strings.Contains(err.Error(), "--confirm-empty") {
		t.Fatalf("explicit confirmation missing: %v", err)
	}
	if _, err := conn.Exec(ctx, `CREATE FUNCTION public.unapproved_control() RETURNS integer LANGUAGE sql AS 'SELECT 1'`); err != nil {
		t.Fatal(err)
	}
	if err := runServerCommand(ctx, []string{"fresh-init-db", "--confirm-empty"}); err == nil || !strings.Contains(err.Error(), "requires the named empty database") {
		t.Fatalf("function-only nonempty database accepted: %v", err)
	}
	var functionRetained bool
	if err := conn.QueryRow(ctx, `SELECT to_regprocedure('public.unapproved_control()') IS NOT NULL`).Scan(&functionRetained); err != nil || !functionRetained {
		t.Fatalf("nonempty function negative control lost: %t %v", functionRetained, err)
	}
	if _, err := conn.Exec(ctx, `DROP FUNCTION public.unapproved_control()`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE public.unrelated_control(id integer PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO public.unrelated_control(id) VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	if err := runServerCommand(ctx, []string{"fresh-init-db", "--confirm-empty"}); err == nil || !strings.Contains(err.Error(), "requires the named empty database") {
		t.Fatalf("nonempty database accepted: %v", err)
	}
	var retained int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM public.unrelated_control`).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("nonempty negative control lost: %d %v", retained, err)
	}
	if _, err := conn.Exec(ctx, `DROP TABLE public.unrelated_control`); err != nil {
		t.Fatal(err)
	}
	genesisBinary := originalFreshInitBinary(t)
	genesisCandidate := releasecontract.FreshGenesisCandidate
	oldVersion, oldCandidate := releasecontract.Version, releasecontract.Candidate
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = oldVersion, oldCandidate })
	t.Setenv("APP_VERSION", "v2.0.2")
	t.Setenv("GIT_SHA", genesisCandidate)
	runGenesis := func() error {
		return exec.CommandContext(ctx, genesisBinary, "fresh-init-db", "--confirm-empty").Run()
	}
	t.Setenv("DATA_RETENTION_DB_PASSWORD", "")
	if err := runGenesis(); err == nil {
		t.Fatal("original genesis accepted missing maintenance password")
	}
	t.Setenv("DATA_RETENTION_DB_PASSWORD", "test-retention-"+suffix)
	if err := runGenesis(); err != nil {
		t.Fatalf("original signed v2.0.2 genesis failed: %v", err)
	}
	if err := db.VerifyDatedParticipationContract(ctx, conn); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM mycfc_meta.schema_migrations`).Scan(&count); err != nil || count != len(db.EmbeddedMigrationInventory())-1 {
		t.Fatalf("original genesis ledger count=%d expected previous inventory err=%v", count, err)
	}
	var receiptVersion, receiptCandidate, receiptDigest string
	if err := conn.QueryRow(ctx, `SELECT version,candidate,schema_digest FROM mycfc_fresh_init.receipt WHERE database_name=$1`, parsed.Database).Scan(&receiptVersion, &receiptCandidate, &receiptDigest); err != nil || receiptVersion != releasecontract.FreshGenesisVersion || receiptCandidate != genesisCandidate || receiptDigest != releasecontract.FreshGenesisDigest {
		t.Fatalf("original protected genesis identity invalid: %v", err)
	}
	// Preserve a real recorded interval across the 77 -> 78 forward upgrade.
	member, season := uuid.New(), uuid.New()
	if _, err := conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic historic adult',$2,'hash','1980-01-01')`, member, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current) VALUES($1,$2,'Synthetic historic season',(now() AT TIME ZONE 'Europe/Lisbon')::date-10,(now() AT TIME ZONE 'Europe/Lisbon')::date+60,true)`, season, "GE_"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	var membership uuid.UUID
	if err := conn.QueryRow(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on,ends_on) SELECT $1,$2,id,(now() AT TIME ZONE 'Europe/Lisbon')::date-5,(now() AT TIME ZONE 'Europe/Lisbon')::date+30 FROM programmes WHERE code='Leisure' RETURNING id`, member, season).Scan(&membership); err != nil {
		t.Fatal(err)
	}
	var originalInterval string
	if err := conn.QueryRow(ctx, `SELECT row_to_json(m)::text FROM user_memberships m WHERE id=$1`, membership).Scan(&originalInterval); err != nil {
		t.Fatal(err)
	}
	// Only the new stamped binary may accept this protected historical receipt.
	releasecontract.Version, releasecontract.Candidate = "v2.0.3", strings.Repeat("b", 40)
	matched, err := db.FreshInitializationMatches(ctx, conn, parsed.Database, releasecontract.Version, releasecontract.Candidate)
	if err != nil || !matched {
		t.Fatalf("original protected genesis rejected before migration: %t %v", matched, err)
	}
	// A later stamped release must accept the original protected genesis,
	// without rewriting it or loosening the actual ledger/schema checks.
	releasecontract.Version, releasecontract.Candidate = "v2.0.3", strings.Repeat("b", 40)
	t.Setenv("APP_VERSION", releasecontract.Version)
	t.Setenv("GIT_SHA", releasecontract.Candidate)
	checkUpgrade := func(want bool) {
		t.Helper()
		ok, err := db.FreshInitializationMatches(ctx, conn, parsed.Database, releasecontract.Version, releasecontract.Candidate)
		if want && (err != nil || !ok) || !want && err == nil && ok {
			t.Fatalf("upgrade receipt outcome matched=%t err=%v want=%t", ok, err, want)
		}
	}
	checkUpgrade(true)
	if err := requireDisposableCompletion(ctx, conn, parsed.Database, releasecontract.Version, releasecontract.Candidate); err != nil {
		t.Fatalf("stamped upgrade refused before migration: %v", err)
	}
	if err := runServerCommand(ctx, []string{"bootstrap-db"}); err != nil {
		t.Fatalf("stamped upgrade bootstrap rejected: %v", err)
	}
	migrationURL, err := urlpkg.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	migrationURL.User = urlpkg.UserPassword(migration, "test-migration-"+suffix)
	t.Setenv("DATABASE_URL", migrationURL.String())
	if err := runServerCommand(ctx, []string{"migrate"}); err != nil {
		t.Fatalf("stamped upgrade migration rejected: %v", err)
	}
	t.Setenv("DATABASE_URL", url)
	if err := runServerCommand(ctx, []string{"harden-db"}); err != nil {
		t.Fatalf("stamped upgrade hardening rejected: %v", err)
	}
	var genesisVersion, originalCandidate, originalDigest string
	if err := conn.QueryRow(ctx, `SELECT version,candidate,schema_digest FROM mycfc_fresh_init.receipt WHERE database_name=$1`, parsed.Database).Scan(&genesisVersion, &originalCandidate, &originalDigest); err != nil || genesisVersion != releasecontract.FreshGenesisVersion || originalCandidate != genesisCandidate || originalDigest != releasecontract.FreshGenesisDigest {
		t.Fatalf("genesis receipt was rewritten: %v", err)
	}
	var afterUpgrade int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM mycfc_meta.schema_migrations`).Scan(&afterUpgrade); err != nil || afterUpgrade != len(db.EmbeddedMigrationInventory()) {
		t.Fatalf("forward ledger count=%d err=%v", afterUpgrade, err)
	}
	var retainedInterval string
	if err := conn.QueryRow(ctx, `SELECT row_to_json(m)::text FROM user_memberships m WHERE id=$1`, membership).Scan(&retainedInterval); err != nil || retainedInterval != originalInterval {
		t.Fatalf("historical membership changed across forward migration: %v", err)
	}
	checkUpgrade(true)
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES('tampered-test-marker')`); err != nil {
		t.Fatal(err)
	}
	checkUpgrade(false)
	if _, err := conn.Exec(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `ALTER TABLE public.user_memberships DROP CONSTRAINT user_memberships_one_participation_per_day`); err != nil {
		t.Fatal(err)
	}
	checkUpgrade(false)
	if _, err := conn.Exec(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `UPDATE mycfc_fresh_init.receipt SET candidate=$1`, strings.Repeat("c", 40)); err != nil {
		t.Fatal(err)
	}
	checkUpgrade(false)
	if _, err := conn.Exec(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `UPDATE mycfc_fresh_init.receipt SET schema_digest=$1`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	checkUpgrade(false)
	if _, err := conn.Exec(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.FreshInitializationMatches(ctx, conn, "wrong_database", releasecontract.Version, releasecontract.Candidate); err == nil && ok {
		t.Fatal("wrong database accepted")
	}
	releasecontract.Version = "v2.0.1"
	if ok, err := db.FreshInitializationMatches(ctx, conn, parsed.Database, releasecontract.Version, releasecontract.Candidate); err == nil && ok {
		t.Fatal("older unsupported release accepted")
	}
	releasecontract.Version = "v2.0.3"
	checkUpgrade(true)
	for _, username := range []string{app, migration, media, retention} {
		var login bool
		if err := conn.QueryRow(ctx, `SELECT rolcanlogin FROM pg_roles WHERE rolname=$1`, username).Scan(&login); err != nil || !login {
			t.Fatalf("role %s not available: %v", username, err)
		}
	}
	if err := runServerCommand(ctx, []string{"fresh-init-db", "--confirm-empty"}); err == nil {
		t.Fatal("repeated fresh initialization accepted")
	}
	var after int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM mycfc_meta.schema_migrations`).Scan(&after); err != nil || after != afterUpgrade {
		t.Fatal(fmt.Errorf("negative retry changed ledger: %d %v", after, err))
	}
	// The migration role can reproduce the schema, ledger and matching row,
	// but cannot forge the bootstrap superuser's ownership of the receipt.
	if _, err := conn.Exec(ctx, `DROP SCHEMA mycfc_fresh_init CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{migration}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `CREATE SCHEMA mycfc_fresh_init`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE mycfc_fresh_init.receipt(database_name text PRIMARY KEY, version text NOT NULL, candidate text NOT NULL, schema_digest text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO mycfc_fresh_init.receipt VALUES ($1,$2,$3,$4)`, parsed.Database, genesisVersion, originalCandidate, db.EmbeddedMigrationDigest()); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatal(err)
	}
	if matched, err := db.FreshInitializationMatches(ctx, conn, parsed.Database, releasecontract.Version, releasecontract.Candidate); err == nil || matched || !strings.Contains(err.Error(), "bootstrap-superuser-owned") {
		t.Fatalf("migration-role forged receipt accepted: matched=%t err=%v", matched, err)
	}
}

func TestFreshCurrentCandidateReceiptRealPostgres(t *testing.T) {
	url := os.Getenv("TEST_FRESH_CURRENT_URL")
	if url == "" {
		url = isolatedFreshInitURL(t)
	}
	ctx := context.Background()
	parsed, err := pgx.ParseConfig(url)
	if err != nil || parsed.Database != releasecontract.Database {
		t.Fatalf("current candidate requires isolated fixed-name mycfc: %v", err)
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	oldVersion, oldCandidate := releasecontract.Version, releasecontract.Candidate
	releasecontract.Version, releasecontract.Candidate = "v2.0.7", strings.Repeat("d", 40)
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = oldVersion, oldCandidate })
	t.Setenv("DATABASE_URL", url)
	t.Setenv("APP_VERSION", releasecontract.Version)
	t.Setenv("GIT_SHA", releasecontract.Candidate)
	t.Setenv("APP_DB_USER", "current_web_"+suffix)
	t.Setenv("APP_DB_PASSWORD", "test-web-"+suffix)
	t.Setenv("MIGRATION_DB_USER", "current_migration_"+suffix)
	t.Setenv("MIGRATION_DB_PASSWORD", "test-migration-"+suffix)
	t.Setenv("MEDIA_CLEANUP_DB_USER", "current_media_"+suffix)
	t.Setenv("MEDIA_CLEANUP_DB_PASSWORD", "test-media-"+suffix)
	t.Setenv("DATA_RETENTION_DB_USER", "current_retention_"+suffix)
	t.Setenv("DATA_RETENTION_DB_PASSWORD", "test-retention-"+suffix)
	if err := runServerCommand(ctx, []string{"fresh-init-db", "--confirm-empty"}); err != nil {
		t.Fatalf("current candidate fresh initialization: %v", err)
	}
	var version, candidate, digest string
	if err := conn.QueryRow(ctx, `SELECT version,candidate,schema_digest FROM mycfc_fresh_init.receipt WHERE database_name=$1`, parsed.Database).Scan(&version, &candidate, &digest); err != nil || version != releasecontract.Version || candidate != releasecontract.Candidate || digest != releasecontract.FinalDigest || digest == releasecontract.FreshGenesisDigest {
		t.Fatalf("current candidate receipt identity invalid: %v", err)
	}
	var ledgerCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM mycfc_meta.schema_migrations`).Scan(&ledgerCount); err != nil || ledgerCount != len(db.EmbeddedMigrationInventory()) {
		t.Fatalf("current candidate ledger count=%d err=%v", ledgerCount, err)
	}
	matched, err := db.FreshInitializationMatches(ctx, conn, parsed.Database, releasecontract.Version, releasecontract.Candidate)
	if err != nil || !matched {
		t.Fatalf("current candidate receipt rejected: matched=%t err=%v", matched, err)
	}
}
