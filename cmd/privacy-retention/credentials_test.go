package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func credentialTestInputs(t *testing.T) (map[string]string, map[string]string) {
	t.Helper()
	old := retentionEffectiveUID
	retentionEffectiveUID = func() int { return 0 }
	t.Cleanup(func() { retentionEffectiveUID = old })
	return map[string]string{"PRIVACY_RETENTION_EXPECTED_DATABASE": "mycfc_test", "PRIVACY_RETENTION_ADMIN_DATABASE_URL_FILE": "/admin", "PRIVACY_RETENTION_LOGIN_DATABASE_URL_FILE": "/login"}, map[string]string{"/admin": "postgres://postgres:private-admin@localhost:5432/mycfc_test?sslmode=disable", "/login": "postgres://" + retentionLogin + ":" + strings.Repeat("x", 32) + "@localhost:5432/mycfc_test?sslmode=disable"}
}
func TestRetentionCredentialInputsRejectUnsafeAuthority(t *testing.T) {
	env, files := credentialTestInputs(t)
	get := func(k string) string { return env[k] }
	read := func(k string) (string, error) { return files[k], nil }
	for _, mode := range []string{"provision", "rotate", "revoke"} {
		if _, err := loadCredentialInputs(mode, get, read); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{"postgres://mycfc_app:" + strings.Repeat("x", 32) + "@localhost:5432/mycfc_test?sslmode=disable", strings.Replace(files["/login"], "localhost", "remote", 1), strings.Replace(files["/login"], "mycfc_test", "wrong", 1), files["/login"] + "&user=postgres", files["/login"] + "&sslmode=disable", strings.Replace(files["/login"], strings.Repeat("x", 32), "short", 1)} {
		previous := files["/login"]
		files["/login"] = raw
		if _, err := loadCredentialInputs("provision", get, read); err == nil {
			t.Fatal("unsafe credential accepted")
		}
		files["/login"] = previous
	}
	oldAdmin := files["/admin"]
	files["/admin"] = strings.Replace(oldAdmin, "private-admin", strings.Repeat("x", 32), 1)
	if _, err := loadCredentialInputs("provision", get, read); err == nil {
		t.Fatal("administrator password reused")
	}
	files["/admin"] = oldAdmin
	if _, err := loadCredentialInputs("unknown", get, read); err == nil {
		t.Fatal("unknown credential mode accepted")
	}
	delete(files, "/login")
	if _, err := loadCredentialInputs("revoke", get, read); err != nil {
		t.Fatal("revocation needs missing login secret", err)
	}
	if _, err := loadCredentialInputs("rotate", get, read); err == nil {
		t.Fatal("rotation accepted no password")
	}
	retentionEffectiveUID = func() int { return 1000 }
	if _, err := loadCredentialInputs("revoke", get, read); err == nil {
		t.Fatal("non-root accepted")
	}
	var output bytes.Buffer
	if err := runCommand(t.Context(), []string{"unknown"}, get, &output); err == nil || output.Len() != 0 {
		t.Fatal("invalid mode produced success")
	}
}
func TestRetentionCredentialFilesRejectSymlinksAndUnsafeModes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credential")
	if err := os.WriteFile(path, []byte("private"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readRootCredential(path); err == nil {
		t.Fatal("public file accepted")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readRootCredential(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, err := readRootCredential(dir); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := readRootCredential("relative"); err == nil {
		t.Fatal("relative path accepted")
	}
}

type credentialRow struct {
	values []any
	err    error
}

func (r credentialRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, v := range r.values {
		switch d := dest[i].(type) {
		case *string:
			*d = v.(string)
		case *bool:
			*d = v.(bool)
		}
	}
	return nil
}

type credentialFake struct {
	pgx.Tx
	queries               []string
	args                  [][]any
	fail                  string
	queryFail             string
	committed, rolledBack bool
	closed                bool
	exists                bool
	databaseMismatch      bool
	notSuperuser          bool
	capabilityMissing     bool
	owned                 bool
	terminateFalse        bool
	active                bool
	beginErr              error
}

func (f *credentialFake) Begin(context.Context) (pgx.Tx, error) { return f, f.beginErr }
func (f *credentialFake) Exec(_ context.Context, q string, args ...any) (pgconn.CommandTag, error) {
	f.queries = append(f.queries, q)
	f.args = append(f.args, args)
	if strings.Contains(q, f.fail) && f.fail != "" {
		return pgconn.CommandTag{}, errors.New("raw-password-private-db-error")
	}
	return pgconn.CommandTag{}, nil
}
func (f *credentialFake) QueryRow(_ context.Context, q string, _ ...any) pgx.Row {
	f.queries = append(f.queries, q)
	if f.queryFail != "" && strings.Contains(q, f.queryFail) {
		return credentialRow{err: errors.New("raw-password-private-db-error")}
	}
	switch {
	case strings.Contains(q, "current_database()"):
		database := "mycfc_test"
		if f.databaseMismatch {
			database = "wrong"
		}
		return credentialRow{values: []any{database, !f.notSuperuser}}
	case strings.Contains(q, "AND NOT rolcanlogin"):
		return credentialRow{values: []any{f.exists, !f.capabilityMissing}}
	case strings.Contains(q, "pg_shdepend"):
		return credentialRow{values: []any{f.owned}}
	case strings.Contains(q, "bool_and"):
		return credentialRow{values: []any{!f.terminateFalse}}
	case strings.Contains(q, "pg_stat_activity"):
		return credentialRow{values: []any{f.active}}
	default:
		return credentialRow{values: []any{false}}
	}
}
func (f *credentialFake) Commit(context.Context) error {
	if f.fail == "commit" {
		return errors.New("raw-password-private-db-error")
	}
	f.committed = true
	return nil
}
func (f *credentialFake) Rollback(context.Context) error { f.rolledBack = true; return nil }
func (f *credentialFake) Close(context.Context) error    { f.closed = true; return nil }
func TestRetentionCredentialTransactionNeverEmbedsPasswordOrReportsFailedCommit(t *testing.T) {
	password := strings.Repeat("secret", 8)
	for _, failure := range []string{"", "set_config('mycfc", "GRANT mycfc", "commit"} {
		db := &credentialFake{fail: failure}
		err := changeRetentionCredential(t.Context(), db, "provision", credentialInputs{database: "mycfc_test", password: password})
		if failure == "" {
			if err != nil || !db.committed {
				t.Fatal(err)
			}
		} else if err == nil || db.committed || strings.Contains(err.Error(), "private-db-error") {
			t.Fatal("unsafe failure", err)
		}
		for _, q := range db.queries {
			if strings.Contains(q, password) {
				t.Fatal("password embedded in SQL")
			}
		}
		if !db.rolledBack {
			t.Fatal("transaction cleanup omitted")
		}
	}
}

func TestRetentionCredentialFileReadsExactProtectedContent(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "credential")
	if err := os.WriteFile(path, []byte("private-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	uid, gid := uint32(os.Geteuid()), uint32(os.Getegid())
	value, err := readCredential(path, uid, gid)
	if err != nil || value != "private-value" {
		t.Fatalf("credential = %q, %v", value, err)
	}
	if _, err = readCredential(path, uid+1, gid); err == nil {
		t.Fatal("wrong owner accepted")
	}
	if err = os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = readCredential(path, uid, gid); err == nil {
		t.Fatal("empty credential accepted")
	}
	if err = os.WriteFile(path, bytes.Repeat([]byte{'x'}, 8193), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = readCredential(path, uid, gid); err == nil {
		t.Fatal("oversized credential accepted")
	}
}

func TestCredentialURLAndInputBoundaries(t *testing.T) {
	for _, raw := range []string{
		"not a URL",
		"http://admin:secret@localhost/mycfc_test",
		"postgres://localhost/mycfc_test",
		"postgres://admin@localhost/mycfc_test",
		"postgres://admin:secret@localhost/wrong",
		"postgres://admin:secret@localhost/mycfc_test#fragment",
		"postgres://admin:secret@localhost/mycfc_test?sslmode=prefer",
		"postgres://admin:secret@localhost/mycfc_test?application_name=mycfc",
		"postgres://admin:secret@localhost/mycfc_test?sslmode=disable&sslmode=require",
		"postgres://admin:secret@localhost/mycfc_test?sslmode=%zz",
	} {
		if _, err := parseCredentialURL(raw, "mycfc_test"); err == nil {
			t.Fatalf("unsafe database URL accepted: %q", raw)
		}
	}

	env, files := credentialTestInputs(t)
	get := func(k string) string { return env[k] }
	read := func(path string) (string, error) {
		value, ok := files[path]
		if !ok {
			return "", errors.New("missing")
		}
		return value, nil
	}
	for _, database := range []string{"", "1database", "bad-name", strings.Repeat("a", 64)} {
		env["PRIVACY_RETENTION_EXPECTED_DATABASE"] = database
		if _, err := loadCredentialInputs("provision", get, read); err == nil {
			t.Fatalf("invalid database accepted: %q", database)
		}
	}
	env["PRIVACY_RETENTION_EXPECTED_DATABASE"] = "mycfc_test"
	delete(files, "/admin")
	if _, err := loadCredentialInputs("provision", get, read); err == nil {
		t.Fatal("missing administrator credential accepted")
	}
	files["/admin"] = "postgres://" + retentionLogin + ":admin-secret@localhost:5432/mycfc_test?sslmode=disable"
	if _, err := loadCredentialInputs("provision", get, read); err == nil {
		t.Fatal("retention login accepted as administrator")
	}
	files["/admin"] = "postgres://postgres:admin-secret@localhost:5432/mycfc_test?sslmode=disable"
	for name, password := range map[string]string{
		"too long": strings.Repeat("x", 1025),
		"newline":  strings.Repeat("x", 31) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			files["/login"] = "postgres://" + retentionLogin + ":" + password + "@localhost:5432/mycfc_test?sslmode=disable"
			if _, err := loadCredentialInputs("rotate", get, read); err == nil {
				t.Fatal("unsafe password accepted")
			}
		})
	}
}

func TestRunCredentialOperationUsesInjectedProtectedInputsAndConnection(t *testing.T) {
	env, files := credentialTestInputs(t)
	get := func(k string) string { return env[k] }
	read := func(path string) (string, error) { return files[path], nil }
	connectFailure := func(context.Context, string) (credentialConnection, error) {
		return nil, errors.New("private connection detail")
	}
	if err := runCredentialOperationWith(t.Context(), "provision", get, io.Discard, read, connectFailure); err == nil || strings.Contains(err.Error(), "private connection detail") {
		t.Fatalf("connection failure = %v", err)
	}

	db := &credentialFake{}
	connect := func(_ context.Context, databaseURL string) (credentialConnection, error) {
		if databaseURL != files["/admin"] {
			t.Fatalf("database URL = %q", databaseURL)
		}
		return db, nil
	}
	var output bytes.Buffer
	if err := runCredentialOperationWith(t.Context(), "provision", get, &output, read, connect); err != nil {
		t.Fatal(err)
	}
	if output.String() != "privacy_retention_credential_provision_succeeded\n" || !db.committed || !db.closed {
		t.Fatalf("output=%q committed=%t closed=%t", output.String(), db.committed, db.closed)
	}
}

func TestChangeRetentionCredentialRejectsEveryUnsafeDatabaseState(t *testing.T) {
	password := strings.Repeat("x", 32)
	inputs := credentialInputs{database: "mycfc_test", password: password}
	tests := []struct {
		name string
		mode string
		db   *credentialFake
	}{
		{name: "begin", mode: "provision", db: &credentialFake{beginErr: errors.New("private")}},
		{name: "identity query", mode: "provision", db: &credentialFake{queryFail: "current_database()"}},
		{name: "database mismatch", mode: "provision", db: &credentialFake{databaseMismatch: true}},
		{name: "not superuser", mode: "provision", db: &credentialFake{notSuperuser: true}},
		{name: "preparation", mode: "provision", db: &credentialFake{fail: "log_statement"}},
		{name: "role query", mode: "provision", db: &credentialFake{queryFail: "AND NOT rolcanlogin"}},
		{name: "provision existing", mode: "provision", db: &credentialFake{exists: true}},
		{name: "rotate absent", mode: "rotate", db: &credentialFake{}},
		{name: "capability missing", mode: "provision", db: &credentialFake{capabilityMissing: true}},
		{name: "ownership query", mode: "rotate", db: &credentialFake{exists: true, queryFail: "pg_shdepend"}},
		{name: "owns objects", mode: "rotate", db: &credentialFake{exists: true, owned: true}},
		{name: "creation", mode: "provision", db: &credentialFake{fail: "CREATE ROLE"}},
		{name: "hardening", mode: "provision", db: &credentialFake{fail: "ALTER ROLE"}},
		{name: "terminate query", mode: "rotate", db: &credentialFake{exists: true, queryFail: "bool_and"}},
		{name: "terminate false", mode: "rotate", db: &credentialFake{exists: true, terminateFalse: true}},
		{name: "active query", mode: "rotate", db: &credentialFake{exists: true, queryFail: "SELECT EXISTS(SELECT 1 FROM pg_stat_activity"}},
		{name: "sessions remain", mode: "rotate", db: &credentialFake{exists: true, active: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := changeRetentionCredential(t.Context(), test.db, test.mode, inputs)
			if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe database state accepted or leaked detail: %v", err)
			}
		})
	}
}

func TestChangeRetentionCredentialHandlesIdempotentRevokeAndExistingRotation(t *testing.T) {
	absent := &credentialFake{}
	if err := changeRetentionCredential(t.Context(), absent, "revoke", credentialInputs{database: "mycfc_test"}); err != nil || !absent.committed {
		t.Fatalf("idempotent revoke: committed=%t err=%v", absent.committed, err)
	}
	failedCommit := &credentialFake{fail: "commit"}
	if err := changeRetentionCredential(t.Context(), failedCommit, "revoke", credentialInputs{database: "mycfc_test"}); err == nil {
		t.Fatal("failed idempotent revoke commit accepted")
	}
	existing := &credentialFake{exists: true}
	if err := changeRetentionCredential(t.Context(), existing, "rotate", credentialInputs{database: "mycfc_test", password: strings.Repeat("x", 32)}); err != nil || !existing.committed {
		t.Fatalf("existing credential rotation: committed=%t err=%v", existing.committed, err)
	}
}
