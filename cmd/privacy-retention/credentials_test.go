package main

import (
	"bytes"
	"context"
	"errors"
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
	committed, rolledBack bool
	exists                bool
}

func (f *credentialFake) Begin(context.Context) (pgx.Tx, error) { return f, nil }
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
	switch {
	case strings.Contains(q, "current_database()"):
		return credentialRow{values: []any{"mycfc_test", true}}
	case strings.Contains(q, "AND NOT rolcanlogin"):
		return credentialRow{values: []any{f.exists, true}}
	case strings.Contains(q, "pg_shdepend"):
		return credentialRow{values: []any{false}}
	case strings.Contains(q, "bool_and"):
		return credentialRow{values: []any{true}}
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
