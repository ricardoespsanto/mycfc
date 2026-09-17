package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type acceptanceLedgerStub struct{}

func (acceptanceLedgerStub) Write(context.Context, privacyrequests.SealedTombstone) (privacyrequests.TombstoneLedgerReceipt, error) {
	return privacyrequests.TombstoneLedgerReceipt{}, nil
}

func TestProtectedCredentialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credential")
	if err := os.WriteFile(path, []byte("private-value"), 0600); err != nil {
		t.Fatal(err)
	}
	owner := uint32(os.Geteuid())
	got, err := readProtectedFile(path, owner)
	if err != nil || string(got) != "private-value" {
		t.Fatalf("valid protected file: %v", err)
	}
	if _, err = readProtectedFile(path, owner+1); err == nil {
		t.Fatal("wrong owner accepted")
	}
	link := filepath.Join(dir, "link")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err = readProtectedFile(link, owner); err == nil {
		t.Fatal("symlink accepted")
	}
	if err = os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err = readProtectedFile(path, owner); err == nil {
		t.Fatal("group readable accepted")
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"", strings.Repeat("x", maxCredentialFile+1)} {
		if err = os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = readProtectedFile(path, owner); err == nil {
			t.Fatal("invalid size accepted")
		}
	}
	if _, err = readProtectedFile(dir, owner); err == nil {
		t.Fatal("directory accepted")
	}
	fifo := filepath.Join(dir, "fifo")
	if err = syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readProtectedFile(fifo, owner); err == nil {
		t.Fatal("FIFO accepted")
	}
}

func TestOperatorRejectsNonRootUnknownModeAndExtraIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		uid  int
		args []string
	}{{1000, []string{"provision"}}, {0, []string{"unknown"}}, {0, []string{"provision", "person-id"}}, {0, nil}} {
		err := run(context.Background(), tc.args, func(string) string { t.Fatal("rejection read credentials"); return "" }, tc.uid, io.Discard)
		if err == nil {
			t.Fatal("unsafe operator invocation accepted")
		}
	}
}

func TestMainReportsRejectedInvocation(t *testing.T) {
	previousArgs := os.Args
	previousExit := exitAcceptanceProcess
	t.Cleanup(func() {
		os.Args = previousArgs
		exitAcceptanceProcess = previousExit
	})
	os.Args = []string{"privacy-acceptance", "unknown"}
	exitCode := 0
	exitAcceptanceProcess = func(code int) { exitCode = code }
	main()
	if exitCode != 1 {
		t.Fatalf("exit code = %d", exitCode)
	}
}

func TestAcceptanceModesRejectMissingReleaseBeforeCredentials(t *testing.T) {
	for _, mode := range []string{"run", "canary-retry", "canary-failure", "canary-aged", "canary-heartbeat", "canary-recovery"} {
		var output strings.Builder
		err := run(context.Background(), []string{mode}, func(name string) string {
			if strings.HasSuffix(name, "_FILE") {
				t.Fatal("unbound run reached credential files")
			}
			return ""
		}, 0, &output)
		if err == nil || output.Len() != 0 {
			t.Fatal("unbound mode emitted evidence")
		}
		if err = run(context.Background(), []string{mode, "existing-person"}, func(string) string { t.Fatal("identifier rejection read configuration"); return "" }, 0, &output); err == nil {
			t.Fatal("accepted a person identifier")
		}
	}
}

func TestCredentialLifecycleCommandsBindDatabaseAndPassword(t *testing.T) {
	previousRead := readAcceptanceProtectedFile
	previousConnect := connectAcceptanceDatabase
	previousClose := closeAcceptanceDatabase
	previousConfigure := configureAcceptanceRole
	t.Cleanup(func() {
		readAcceptanceProtectedFile = previousRead
		connectAcceptanceDatabase = previousConnect
		closeAcceptanceDatabase = previousClose
		configureAcceptanceRole = previousConfigure
	})

	files := map[string][]byte{
		"admin":    []byte("postgres://postgres:administrator@database:5432/mycfc"),
		"operator": []byte("postgres://" + db.PrivacyAcceptanceRole + ":" + strings.Repeat("p", 32) + "@database:5432/mycfc"),
	}
	readAcceptanceProtectedFile = func(path string, owner uint32) ([]byte, error) {
		if owner != 0 || files[path] == nil {
			return nil, errors.New("missing protected file")
		}
		return files[path], nil
	}
	connectAcceptanceDatabase = func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) { return nil, nil }
	closed := 0
	closeAcceptanceDatabase = func(*pgx.Conn, context.Context) { closed++ }
	type call struct {
		password string
		revoke   bool
	}
	var calls []call
	configureAcceptanceRole = func(_ context.Context, _ *pgx.Conn, expected, password string, revoke bool) error {
		if expected != "mycfc" {
			t.Fatalf("expected database = %q", expected)
		}
		calls = append(calls, call{password: password, revoke: revoke})
		return nil
	}
	getenv := func(name string) string {
		return map[string]string{
			"PRIVACY_ACCEPTANCE_EXPECTED_DATABASE":       "mycfc",
			"PRIVACY_ACCEPTANCE_ADMIN_DATABASE_URL_FILE": "admin",
			"PRIVACY_ACCEPTANCE_DATABASE_URL_FILE":       "operator",
		}[name]
	}
	var output bytes.Buffer
	for _, mode := range []string{"provision", "rotate", "revoke"} {
		if err := run(t.Context(), []string{mode}, getenv, 0, &output); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
	}
	if len(calls) != 3 || calls[0].password != strings.Repeat("p", 32) || calls[0].revoke ||
		calls[1].password != strings.Repeat("p", 32) || calls[1].revoke || calls[2].password != "" || !calls[2].revoke {
		t.Fatalf("unexpected lifecycle calls: %#v", calls)
	}
	if closed != 3 || strings.Count(output.String(), "outcome=complete") != 3 {
		t.Fatalf("closed=%d output=%q", closed, output.String())
	}
	configureAcceptanceRole = func(context.Context, *pgx.Conn, string, string, bool) error {
		return errors.New("role configuration failed")
	}
	if err := run(t.Context(), []string{"revoke"}, getenv, 0, io.Discard); err == nil {
		t.Fatal("role configuration failure accepted")
	}
}

func TestAcceptanceCommandWiresBoundDependenciesAndSignsEvidence(t *testing.T) {
	previousRead := readAcceptanceProtectedFile
	previousConnect := connectAcceptanceDatabase
	previousCloseDatabase := closeAcceptanceDatabase
	previousProtector := newAcceptanceProtector
	previousAWS := loadAcceptanceAWSConfig
	previousLedger := newAcceptanceLedger
	previousPool := newAcceptancePool
	previousClosePool := closeAcceptancePool
	previousRun := runSyntheticAcceptance
	previousSign := signAcceptanceEvidence
	t.Cleanup(func() {
		readAcceptanceProtectedFile = previousRead
		connectAcceptanceDatabase = previousConnect
		closeAcceptanceDatabase = previousCloseDatabase
		newAcceptanceProtector = previousProtector
		loadAcceptanceAWSConfig = previousAWS
		newAcceptanceLedger = previousLedger
		newAcceptancePool = previousPool
		closeAcceptancePool = previousClosePool
		runSyntheticAcceptance = previousRun
		signAcceptanceEvidence = previousSign
	})

	seed := bytes.Repeat([]byte{0x41}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	encode := func(value []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(value)) }
	files := map[string][]byte{
		"operator": []byte("postgres://" + db.PrivacyAcceptanceRole + ":" + strings.Repeat("o", 32) + "@database:5432/mycfc"),
		"app":      []byte("postgres://mycfc_app:" + strings.Repeat("a", 32) + "@database:5432/mycfc"),
		"worker":   []byte("postgres://mycfc_privacy_executor:" + strings.Repeat("w", 32) + "@database:5432/mycfc"),
		"signing":  encode(seed),
		"public":   encode(bytes.Repeat([]byte{0x51}, 32)),
		"locator":  encode(bytes.Repeat([]byte{0x61}, 32)),
	}
	readAcceptanceProtectedFile = func(path string, _ uint32) ([]byte, error) {
		value, ok := files[path]
		if !ok {
			return nil, errors.New("missing protected file")
		}
		return value, nil
	}
	connectAcceptanceDatabase = func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) { return nil, nil }
	closedDatabase := false
	closeAcceptanceDatabase = func(*pgx.Conn, context.Context) { closedDatabase = true }
	newAcceptanceProtector = func(string, []byte, string, []byte) (*privacyrequests.TombstoneProtector, error) {
		return &privacyrequests.TombstoneProtector{}, nil
	}
	loadAcceptanceAWSConfig = func(_ context.Context, region string) (aws.Config, error) {
		if region != "eu-west-1" {
			t.Fatalf("region = %q", region)
		}
		return aws.Config{Region: region}, nil
	}
	newAcceptanceLedger = func(config aws.Config, functionName string) (privacyrequests.TombstoneLedger, error) {
		if config.Region != "eu-west-1" || functionName != "privacy-tombstones" {
			t.Fatalf("ledger binding region=%q function=%q", config.Region, functionName)
		}
		return acceptanceLedgerStub{}, nil
	}
	newAcceptancePool = func(context.Context, *pgxpool.Config) (*pgxpool.Pool, error) { return nil, nil }
	closedPool := false
	closeAcceptancePool = func(*pgxpool.Pool) { closedPool = true }
	runSyntheticAcceptance = func(_ context.Context, options privacyrequests.AcceptanceOptions) (privacyrequests.AcceptanceEvidence, error) {
		if options.Mode != "run" || options.ExpectedDatabase != "mycfc" || options.AppRole != "mycfc_app" ||
			options.ImageDigest != "sha256:"+strings.Repeat("b", 64) || options.SchemaDigest != db.EmbeddedMigrationDigest() {
			t.Fatalf("unexpected acceptance options: %+v", options)
		}
		if err := options.Event(t.Context(), "privacy_fixture_created"); err != nil {
			t.Fatal(err)
		}
		return privacyrequests.AcceptanceEvidence{Contract: privacyrequests.AcceptanceContract, Mode: "run"}, nil
	}
	signAcceptanceEvidence = func(value privacyrequests.AcceptanceEvidence, keyID string, key ed25519.PrivateKey) (privacyrequests.SignedAcceptanceEvidence, error) {
		if value.Mode != "run" || keyID != "acceptance-v1" || !bytes.Equal(key, privateKey) {
			t.Fatal("acceptance evidence was not signed with the bound key")
		}
		return privacyrequests.SignedAcceptanceEvidence{Contract: privacyrequests.AcceptanceContract, KeyID: keyID}, nil
	}
	env := map[string]string{
		"PRIVACY_ACCEPTANCE_EXPECTED_DATABASE":          "mycfc",
		"PRIVACY_ACCEPTANCE_APP_ROLE":                   "mycfc_app",
		"PRIVACY_ACCEPTANCE_SCHEMA_DIGEST":              db.EmbeddedMigrationDigest(),
		"PRIVACY_ACCEPTANCE_DATABASE_URL_FILE":          "operator",
		"PRIVACY_ACCEPTANCE_APP_DATABASE_URL_FILE":      "app",
		"PRIVACY_ACCEPTANCE_EXECUTOR_DATABASE_URL_FILE": "worker",
		"PRIVACY_ACCEPTANCE_SIGNING_KEY_FILE":           "signing",
		"PRIVACY_ACCEPTANCE_SIGNING_KEY_ID":             "acceptance-v1",
		"PRIVACY_TOMBSTONE_PUBLIC_KEY_FILE":             "public",
		"PRIVACY_TOMBSTONE_LOCATOR_KEY_FILE":            "locator",
		"PRIVACY_TOMBSTONE_ENCRYPTION_KEY_ID":           "encryption-v1",
		"PRIVACY_TOMBSTONE_LOCATOR_KEY_ID":              "locator-v1",
		"PRIVACY_TOMBSTONE_BROKER_FUNCTION_NAME":        "privacy-tombstones",
		"PRIVACY_ACCEPTANCE_IMAGE_DIGEST":               "sha256:" + strings.Repeat("b", 64),
		"AWS_REGION":                                    "eu-west-1",
	}
	var output bytes.Buffer
	if err := runAcceptance(t.Context(), "run", func(name string) string { return env[name] }, &output); err != nil {
		t.Fatal(err)
	}
	if !closedDatabase || !closedPool || !strings.Contains(output.String(), "event=privacy_fixture_created count=1") ||
		!strings.Contains(output.String(), `"key_id":"acceptance-v1"`) {
		t.Fatalf("database_closed=%t pool_closed=%t output=%q", closedDatabase, closedPool, output.String())
	}

	assertRejected := func(name string, mutate func() func()) {
		t.Helper()
		restore := mutate()
		t.Cleanup(restore)
		if err := runAcceptance(t.Context(), "run", func(name string) string { return env[name] }, io.Discard); err == nil {
			t.Fatalf("%s failure was accepted", name)
		}
		restore()
	}
	missingFile := func(variable string) func() func() {
		return func() func() {
			previous := env[variable]
			env[variable] = "missing"
			return func() { env[variable] = previous }
		}
	}
	assertRejected("application credential", missingFile("PRIVACY_ACCEPTANCE_APP_DATABASE_URL_FILE"))
	assertRejected("executor credential", missingFile("PRIVACY_ACCEPTANCE_EXECUTOR_DATABASE_URL_FILE"))
	assertRejected("operator role", func() func() {
		previous := files["operator"]
		files["operator"] = []byte("postgres://wrong_role:" + strings.Repeat("o", 32) + "@database:5432/mycfc")
		return func() { files["operator"] = previous }
	})
	assertRejected("database binding", func() func() {
		previous := files["app"]
		files["app"] = []byte("postgres://mycfc_app:" + strings.Repeat("a", 32) + "@database:5432/other")
		return func() { files["app"] = previous }
	})
	assertRejected("signing key read", missingFile("PRIVACY_ACCEPTANCE_SIGNING_KEY_FILE"))
	assertRejected("signing key", func() func() {
		previous := files["signing"]
		files["signing"] = encode([]byte("short"))
		return func() { files["signing"] = previous }
	})
	assertRejected("public key read", missingFile("PRIVACY_TOMBSTONE_PUBLIC_KEY_FILE"))
	assertRejected("locator key read", missingFile("PRIVACY_TOMBSTONE_LOCATOR_KEY_FILE"))
	assertRejected("protector", func() func() {
		previous := newAcceptanceProtector
		newAcceptanceProtector = func(string, []byte, string, []byte) (*privacyrequests.TombstoneProtector, error) {
			return nil, errors.New("protector unavailable")
		}
		return func() { newAcceptanceProtector = previous }
	})
	assertRejected("region", func() func() {
		previous := env["AWS_REGION"]
		env["AWS_REGION"] = "INVALID"
		return func() { env["AWS_REGION"] = previous }
	})
	assertRejected("AWS configuration", func() func() {
		previous := loadAcceptanceAWSConfig
		loadAcceptanceAWSConfig = func(context.Context, string) (aws.Config, error) {
			return aws.Config{}, errors.New("AWS unavailable")
		}
		return func() { loadAcceptanceAWSConfig = previous }
	})
	assertRejected("ledger", func() func() {
		previous := newAcceptanceLedger
		newAcceptanceLedger = func(aws.Config, string) (privacyrequests.TombstoneLedger, error) {
			return nil, errors.New("ledger unavailable")
		}
		return func() { newAcceptanceLedger = previous }
	})
	assertRejected("operator connection", func() func() {
		previous := connectAcceptanceDatabase
		connectAcceptanceDatabase = func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
			return nil, errors.New("database unavailable")
		}
		return func() { connectAcceptanceDatabase = previous }
	})
	assertRejected("executor pool", func() func() {
		previous := newAcceptancePool
		newAcceptancePool = func(context.Context, *pgxpool.Config) (*pgxpool.Pool, error) {
			return nil, errors.New("pool unavailable")
		}
		return func() { newAcceptancePool = previous }
	})
	assertRejected("synthetic execution", func() func() {
		previous := runSyntheticAcceptance
		runSyntheticAcceptance = func(context.Context, privacyrequests.AcceptanceOptions) (privacyrequests.AcceptanceEvidence, error) {
			return privacyrequests.AcceptanceEvidence{}, errors.New("acceptance failed")
		}
		return func() { runSyntheticAcceptance = previous }
	})
	assertRejected("evidence signing", func() func() {
		previous := signAcceptanceEvidence
		signAcceptanceEvidence = func(privacyrequests.AcceptanceEvidence, string, ed25519.PrivateKey) (privacyrequests.SignedAcceptanceEvidence, error) {
			return privacyrequests.SignedAcceptanceEvidence{}, errors.New("signing failed")
		}
		return func() { signAcceptanceEvidence = previous }
	})
}

func TestProtectedAcceptanceParsersRejectBadMaterial(t *testing.T) {
	previousRead := readAcceptanceProtectedFile
	t.Cleanup(func() { readAcceptanceProtectedFile = previousRead })
	readAcceptanceProtectedFile = func(path string, _ uint32) ([]byte, error) {
		switch path {
		case "missing":
			return nil, errors.New("missing")
		case "key":
			return []byte("not-base64"), nil
		default:
			return []byte("not-a-database-url"), nil
		}
	}
	if _, err := protectedKey("missing"); err == nil {
		t.Fatal("missing key accepted")
	}
	if _, err := protectedKey("key"); err == nil {
		t.Fatal("malformed key accepted")
	}
	if _, err := protectedDatabase("missing"); err == nil {
		t.Fatal("missing database file accepted")
	}
	if _, err := protectedDatabase("database"); err == nil {
		t.Fatal("malformed database URL accepted")
	}
}

func TestAcceptanceDependencyFailuresRemainClosed(t *testing.T) {
	previousRead := readAcceptanceProtectedFile
	previousConnect := connectAcceptanceDatabase
	t.Cleanup(func() {
		readAcceptanceProtectedFile = previousRead
		connectAcceptanceDatabase = previousConnect
	})
	readAcceptanceProtectedFile = func(path string, _ uint32) ([]byte, error) {
		if path == "admin" {
			return []byte("postgres://postgres:administrator@database:5432/mycfc"), nil
		}
		return nil, errors.New("unavailable")
	}
	connectAcceptanceDatabase = func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
		return nil, errors.New("database unavailable")
	}
	getenv := func(name string) string {
		return map[string]string{
			"PRIVACY_ACCEPTANCE_EXPECTED_DATABASE":       "mycfc",
			"PRIVACY_ACCEPTANCE_ADMIN_DATABASE_URL_FILE": "admin",
		}[name]
	}
	if err := run(t.Context(), []string{"revoke"}, getenv, 0, io.Discard); err == nil {
		t.Fatal("administrator connection failure accepted")
	}
	readAcceptanceProtectedFile = func(path string, _ uint32) ([]byte, error) {
		if path == "admin" {
			return []byte("not-a-database-url"), nil
		}
		return nil, errors.New("unavailable")
	}
	if err := run(t.Context(), []string{"revoke"}, getenv, 0, io.Discard); err == nil {
		t.Fatal("invalid administrator binding accepted")
	}

	readAcceptanceProtectedFile = func(string, uint32) ([]byte, error) { return nil, errors.New("unavailable") }
	if err := runAcceptance(t.Context(), "run", func(name string) string {
		return map[string]string{
			"PRIVACY_ACCEPTANCE_EXPECTED_DATABASE": "mycfc",
			"PRIVACY_ACCEPTANCE_APP_ROLE":          "mycfc_app",
			"PRIVACY_ACCEPTANCE_SCHEMA_DIGEST":     db.EmbeddedMigrationDigest(),
		}[name]
	}, io.Discard); err == nil {
		t.Fatal("missing acceptance operator credential accepted")
	}
}
