package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

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
