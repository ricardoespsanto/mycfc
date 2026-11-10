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
