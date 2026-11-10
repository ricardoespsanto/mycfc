package main

import (
	"context"
	"github.com/jackc/pgx/v5"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalRecipientTargetRejectsPGXOverrides(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	t.Setenv("APP_VERSION", "dev")
	t.Setenv("GIT_SHA", strings.Repeat("0", 40))
	t.Setenv("TRAINING_SCHEDULE_ADMISSION_FILE", "")
	raw := "postgres://migration:secret@127.0.0.1:55487/mycfc_ui_review?sslmode=disable"
	t.Setenv("DATABASE_URL", raw)
	cfg, err := pgx.ParseConfig(raw)
	if err != nil || !strictUnstampedLocalConnection(cfg) {
		t.Fatal("canonical local rejected", err)
	}
	for _, suffix := range []string{"&%68ost=remote", "&user=root", "&port=5432", "&dbname=production", "&service=remote", "&options=-c+role%3Droot", "&hostaddr=10.0.0.1", "&password=other"} {
		bad := raw + suffix
		t.Setenv("DATABASE_URL", bad)
		if safeLocalDatabaseURL(bad) || strictUnstampedLocalConnection(cfg) {
			t.Fatal("override accepted", suffix)
		}
	}
	t.Setenv("DATABASE_URL", raw)
	t.Setenv("APP_VERSION", "v9.1.0")
	if strictUnstampedLocalConnection(cfg) {
		t.Fatal("release identity accepted")
	}
}
func TestLocalRecipientManifestOwnsBindingAndNeverAdoptsPopulatedTarget(t *testing.T) {
	t.Chdir(t.TempDir())
	binding := localRecipientManifest{Contract: "mycfc/local-recipient-floor/v1", ProjectPath: mustLocalCwd(t), SystemID: "123", Database: "mycfc_test", DatabaseOID: 42, EndpointSHA256: strings.Repeat("a", 64), MigrationRole: "migration", PublisherRole: "publisher"}
	if _, err := openLocalRecipientManifest(binding, false); err == nil {
		t.Fatal("unrecorded target adopted")
	}
	f, err := openLocalRecipientManifest(binding, true)
	if err != nil {
		t.Fatal(err)
	}
	f.Value.Phase = "captured"
	if err = f.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := openLocalRecipientManifest(binding, false); err == nil {
		t.Fatal("concurrent manifest operation admitted")
	}
	path := f.path
	f.Close()
	f, err = openLocalRecipientManifest(binding, false)
	if err != nil {
		t.Fatal(err)
	}
	if f.Value.PublisherOID != nil || f.Value.OriginalLogin != nil {
		t.Fatal("absent publisher was fabricated")
	}
	f.Close()
	changed := binding
	changed.EndpointSHA256 = strings.Repeat("b", 64)
	if _, err = openLocalRecipientManifest(changed, false); err == nil {
		t.Fatal("changed endpoint accepted")
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = openLocalRecipientManifest(binding, false); err == nil {
		t.Fatal("foreign mode accepted")
	}
	os.Chmod(path, 0600)
	moved := path + ".saved"
	os.Rename(path, moved)
	os.Symlink(filepath.Base(moved), path)
	if _, err = openLocalRecipientManifest(binding, false); err == nil {
		t.Fatal("symlink manifest accepted")
	}
}
func mustLocalCwd(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return cwd
}

type localDialProbeConn struct {
	net.Conn
	peer   net.Addr
	closed bool
}

func (c *localDialProbeConn) RemoteAddr() net.Addr { return c.peer }
func (c *localDialProbeConn) Close() error         { c.closed = true; return nil }
func TestLocalRecipientDialChecksSocketBeforePGXStartup(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "203.0.113.7"} {
		conn := &localDialProbeConn{peer: &net.TCPAddr{IP: net.ParseIP(ip), Port: 5432}}
		cfg := &pgx.ConnConfig{}
		cfg.DialFunc = func(context.Context, string, string) (net.Conn, error) { return conn, nil }
		installLocalRecipientDialGuard(cfg)
		result, err := cfg.DialFunc(t.Context(), "tcp", "localhost:5432")
		if net.ParseIP(ip).IsLoopback() {
			if err != nil || result != conn || conn.closed {
				t.Fatal("loopback socket rejected")
			}
		} else if err == nil || result != nil || !conn.closed {
			t.Fatal("nonloopback socket reached pgx")
		}
	}
}

func TestLocalRecipientConnectionRejectsOverridesBeforeAuthentication(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	t.Setenv("APP_VERSION", "dev")
	t.Setenv("GIT_SHA", strings.Repeat("0", 40))
	t.Setenv("TRAINING_SCHEDULE_ADMISSION_FILE", "")
	raw := "postgres://bootstrap:synthetic@localhost:5432/mycfc?sslmode=disable&%68ost=remote.invalid"
	t.Setenv("DATABASE_URL", raw)
	if _, err := connectDatabaseCommand(t.Context(), raw); err == nil || !strings.Contains(err.Error(), "before authentication") {
		t.Fatal("override reached network/authentication", err)
	}
}

func TestLocalRecipientEndpointRequiresUnambiguousConnectionOptions(t *testing.T) {
	for name, raw := range map[string]string{
		"fragment":        "postgres://local:synthetic@localhost/mycfc#fragment",
		"remote":          "postgres://local:synthetic@db.example.invalid/mycfc",
		"bad query":       "postgres://local:synthetic@localhost/mycfc?sslmode=%ZZ",
		"repeated option": "postgres://local:synthetic@localhost/mycfc?sslmode=disable&sslmode=require",
	} {
		t.Run(name, func(t *testing.T) {
			if safeLocalDatabaseURL(raw) {
				t.Fatal("ambiguous endpoint accepted")
			}
		})
	}
	if !safeLocalDatabaseURL("postgresql://local:synthetic@[::1]:5432/mycfc?sslmode=disable&application_name=fixture") {
		t.Fatal("canonical IPv6 fixture endpoint refused")
	}
	t.Setenv("APP_ENV", "local")
	t.Setenv("APP_VERSION", "dev")
	t.Setenv("GIT_SHA", strings.Repeat("0", 40))
	t.Setenv("TRAINING_SCHEDULE_ADMISSION_FILE", "")
	t.Setenv("DATABASE_URL", "")
	cfg := &pgx.ConnConfig{}
	cfg.Host, cfg.Database = "127.0.0.1", "mycfc"
	cfg.RuntimeParams = map[string]string{"application_name": "fixture"}
	if !strictUnstampedLocalConnection(cfg) {
		t.Fatal("canonical fixture with application label refused")
	}
	cfg.RuntimeParams["role"] = "other"
	if strictUnstampedLocalConnection(cfg) {
		t.Fatal("runtime role override accepted")
	}
	delete(cfg.RuntimeParams, "role")
	t.Setenv("PGSERVICE", "synthetic")
	if strictUnstampedLocalConnection(cfg) {
		t.Fatal("PostgreSQL service override accepted")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	dial := &pgx.ConnConfig{}
	installLocalRecipientDialGuard(dial)
	if conn, err := dial.DialFunc(ctx, "tcp", "127.0.0.1:1"); err == nil || conn != nil {
		t.Fatal("a canceled loopback dial returned a connection")
	}
}

func TestLocalRecipientManifestRejectsInterruptedAndUnknownRecords(t *testing.T) {
	t.Chdir(t.TempDir())
	binding := localRecipientManifest{Contract: "mycfc/local-recipient-floor/v1", ProjectPath: mustLocalCwd(t), SystemID: "123", Database: "mycfc_test", DatabaseOID: 42, EndpointSHA256: strings.Repeat("a", 64), MigrationRole: "migration", PublisherRole: "publisher"}
	record, err := openLocalRecipientManifest(binding, true)
	if err != nil {
		t.Fatal(err)
	}
	record.Value.Phase = "captured"
	if err := record.Save(); err != nil {
		t.Fatal(err)
	}
	path := record.path
	record.Close()
	valid, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, data, message string }{
		{"oversized", strings.Repeat(" ", 8193), "exceeds bound"},
		{"incomplete", "{", "manifest rejected"},
		{"trailing", string(valid) + " {}", "trailing data"},
		{"unknown phase", strings.Replace(string(valid), `"phase":"captured"`, `"phase":"future"`, 1), "phase rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := openLocalRecipientManifest(binding, false)
			if got != nil {
				got.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("err = %v, want %q", err, tc.message)
			}
		})
	}
	if err := os.WriteFile(path, valid, 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := openLocalRecipientManifest(binding, false)
	if err != nil {
		t.Fatal("rejected input retained the local lock", err)
	}
	recovered.Close()
}

func TestLocalRecipientManifestRefusesUnsafeStorage(t *testing.T) {
	for _, name := range []string{"symlinked scratch", "writable journal directory", "symlinked lock", "invalid lock mode", "replacement cannot overwrite directory"} {
		t.Run(name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			binding := localRecipientManifest{Contract: "mycfc/local-recipient-floor/v1", ProjectPath: mustLocalCwd(t), SystemID: "123", Database: "mycfc_test", DatabaseOID: 42, EndpointSHA256: strings.Repeat("a", 64), MigrationRole: "migration", PublisherRole: "publisher"}
			if name == "symlinked scratch" {
				if err := os.Symlink(t.TempDir(), "tmp"); err != nil {
					t.Fatal(err)
				}
			} else {
				record, err := openLocalRecipientManifest(binding, true)
				if err != nil {
					t.Fatal(err)
				}
				path := record.path
				switch name {
				case "writable journal directory":
					record.Close()
					if err := os.Chmod(filepath.Dir(path), 0777); err != nil {
						t.Fatal(err)
					}
				case "symlinked lock":
					record.Close()
					if err := os.Remove(path + ".lock"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(filepath.Base(path), path+".lock"); err != nil {
						t.Fatal(err)
					}
				case "invalid lock mode":
					record.Close()
					if err := os.Chmod(path+".lock", 0644); err != nil {
						t.Fatal(err)
					}
				case "replacement cannot overwrite directory":
					defer record.Close()
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
					if err := record.Save(); err == nil {
						t.Fatal("journal save overwrote a directory")
					}
					return
				}
			}
			if record, err := openLocalRecipientManifest(binding, true); err == nil {
				record.Close()
				t.Fatal("unsafe journal storage accepted")
			}
		})
	}
}
