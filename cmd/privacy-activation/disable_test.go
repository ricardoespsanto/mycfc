package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type disableDatabaseFake struct {
	query     string
	arguments []any
	scan      func(...any) error
	closed    bool
}

func (f *disableDatabaseFake) QueryRow(_ context.Context, query string, arguments ...any) pgx.Row {
	f.query, f.arguments = query, arguments
	return disableRowFake{scan: f.scan}
}

func (f *disableDatabaseFake) Close() { f.closed = true }

type disableRowFake struct{ scan func(...any) error }

func (f disableRowFake) Scan(destinations ...any) error { return f.scan(destinations...) }

func TestRunDisableUsesOnlyNarrowCredentialAndEmitsFixedOutcome(t *testing.T) {
	actor := uuid.New()
	databaseURL := "postgres://mycfc_privacy_activation_disable:secret@postgres:5432/mycfc?sslmode=disable"
	env := map[string]string{
		"PRIVACY_ACTIVATION_DISABLE_DATABASE_URL":      databaseURL,
		"PRIVACY_ACTIVATION_DISABLE_EXPECTED_DATABASE": "mycfc",
		"PRIVACY_ACTIVATION_DISABLE_ACTOR_REF":         actor.String(),
		"PRIVACY_ACTIVATION_BROKER_DATABASE_URL":       "postgres://broker:must-not-be-read@postgres/mycfc",
		"PRIVACY_ACTIVATION_RESTORE_AUTH_KEY_FILE":     "/must/not/be/read",
		"PRIVACY_ACTIVATION_APPROVAL_PRIVATE_KEY_FILE": "/must/not/be/read",
	}
	read := map[string]bool{}
	getenv := func(name string) string {
		read[name] = true
		return env[name]
	}
	database := &disableDatabaseFake{scan: func(destinations ...any) error {
		*destinations[0].(*int64) = 7
		*destinations[1].(*string) = "mycfc"
		*destinations[2].(*bool) = true
		*destinations[3].(*bool) = false
		return nil
	}}
	withDisableRuntime(t, 0, func(context.Context, string) (activationDisableDatabase, error) { return database, nil })

	var output bytes.Buffer
	if err := runDisable(t.Context(), getenv, &output); err != nil {
		t.Fatal(err)
	}
	if !database.closed || strings.TrimSpace(database.query) != "SELECT switch_version,database_name,engaged,fulfilment_ready FROM privacy_disable.privacy_activation_disable($1,$2)" || len(database.arguments) != 2 || database.arguments[0] != actor || database.arguments[1] != "mycfc" {
		t.Fatalf("closed=%t query=%q arguments=%v", database.closed, database.query, database.arguments)
	}
	if output.String() != "privacy_activation_disabled kill_switch=engaged readiness=blocked\n" {
		t.Fatalf("output=%q", output.String())
	}
	for _, forbidden := range []string{"PRIVACY_ACTIVATION_BROKER_DATABASE_URL", "PRIVACY_ACTIVATION_RESTORE_AUTH_KEY_FILE", "PRIVACY_ACTIVATION_APPROVAL_PRIVATE_KEY_FILE"} {
		if read[forbidden] {
			t.Fatalf("disable command read forbidden environment %s", forbidden)
		}
	}
}

func TestMainDispatchesDisableAndReportsDedicatedFailures(t *testing.T) {
	actor := uuid.New()
	for name, value := range map[string]string{
		"PRIVACY_ACTIVATION_DISABLE_DATABASE_URL":      "postgres://mycfc_privacy_activation_disable:secret@postgres:5432/mycfc?sslmode=disable",
		"PRIVACY_ACTIVATION_DISABLE_EXPECTED_DATABASE": "mycfc",
		"PRIVACY_ACTIVATION_DISABLE_ACTOR_REF":         actor.String(),
	} {
		t.Setenv(name, value)
	}
	originalArgs, originalStdout, originalStderr, originalExit := os.Args, os.Stdout, os.Stderr, exitProcess
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = stdout, stderr
	t.Cleanup(func() {
		os.Args, os.Stdout, os.Stderr, exitProcess = originalArgs, originalStdout, originalStderr, originalExit
		_ = stdout.Close()
		_ = stderr.Close()
	})

	database := &disableDatabaseFake{scan: func(destinations ...any) error {
		*destinations[0].(*int64) = 1
		*destinations[1].(*string) = "mycfc"
		*destinations[2].(*bool) = true
		*destinations[3].(*bool) = false
		return nil
	}}
	withDisableRuntime(t, 0, func(context.Context, string) (activationDisableDatabase, error) { return database, nil })
	exitCode := 0
	exitProcess = func(code int) { exitCode = code }
	os.Args = []string{"privacy-activation", "disable"}
	main()
	if exitCode != 0 || !database.closed {
		t.Fatalf("successful disable exit=%d closed=%t", exitCode, database.closed)
	}

	openActivationDisableDatabase = func(context.Context, string) (activationDisableDatabase, error) {
		return nil, errors.New("unavailable")
	}
	main()
	if exitCode != 1 {
		t.Fatalf("failed disable exit=%d", exitCode)
	}
	if _, err = stderr.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	failure, err := io.ReadAll(stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(failure), "privacy_activation_disable_failed") {
		t.Fatalf("dedicated failure marker missing from %q", failure)
	}

	exitCode = 0
	os.Args = []string{"privacy-activation", "disable", "unexpected"}
	main()
	if exitCode != 2 {
		t.Fatalf("usage rejection exit=%d", exitCode)
	}

	exitCode = 0
	os.Args = []string{"privacy-activation", "unknown"}
	main()
	if exitCode != 1 {
		t.Fatalf("unknown mode exit=%d", exitCode)
	}
}

func TestLoadDisableInputsFailsClosed(t *testing.T) {
	actor := "7f40fdc4-1653-4aa6-8cd5-e44f25c2fd85"
	validURL := "postgres://mycfc_privacy_activation_disable:secret@postgres:5432/mycfc?sslmode=disable"
	for _, test := range []struct {
		name     string
		euid     int
		actor    string
		url      string
		database string
	}{
		{name: "non root", euid: 1000, actor: actor, url: validURL, database: "mycfc"},
		{name: "missing actor", euid: 0, url: validURL, database: "mycfc"},
		{name: "nil actor", euid: 0, actor: uuid.Nil.String(), url: validURL, database: "mycfc"},
		{name: "non canonical actor", euid: 0, actor: strings.ToUpper(actor), url: validURL, database: "mycfc"},
		{name: "actor whitespace", euid: 0, actor: " " + actor, url: validURL, database: "mycfc"},
		{name: "missing url", euid: 0, actor: actor, database: "mycfc"},
		{name: "missing expected database", euid: 0, actor: actor, url: validURL},
		{name: "database mismatch", euid: 0, actor: actor, url: validURL, database: "other"},
		{name: "invalid database", euid: 0, actor: actor, url: validURL, database: "mycfc.test"},
		{name: "wrong role", euid: 0, actor: actor, url: "postgres://broker:secret@postgres:5432/mycfc", database: "mycfc"},
		{name: "missing password", euid: 0, actor: actor, url: "postgres://mycfc_privacy_activation_disable@postgres:5432/mycfc", database: "mycfc"},
		{name: "wrong scheme", euid: 0, actor: actor, url: "https://mycfc_privacy_activation_disable:secret@postgres/mycfc", database: "mycfc"},
		{name: "missing database", euid: 0, actor: actor, url: "postgres://mycfc_privacy_activation_disable:secret@postgres/", database: "mycfc"},
		{name: "url whitespace", euid: 0, actor: actor, url: " " + validURL, database: "mycfc"},
	} {
		t.Run(test.name, func(t *testing.T) {
			withDisableRuntime(t, test.euid, nil)
			env := map[string]string{"PRIVACY_ACTIVATION_DISABLE_ACTOR_REF": test.actor, "PRIVACY_ACTIVATION_DISABLE_DATABASE_URL": test.url, "PRIVACY_ACTIVATION_DISABLE_EXPECTED_DATABASE": test.database}
			if _, _, _, err := loadDisableInputs(func(name string) string { return env[name] }); err == nil {
				t.Fatal("invalid disable input accepted")
			}
		})
	}
}

func TestRunDisableFailsClosedWithoutSuccessOutput(t *testing.T) {
	actor := uuid.New().String()
	env := map[string]string{
		"PRIVACY_ACTIVATION_DISABLE_DATABASE_URL":      "postgres://mycfc_privacy_activation_disable:secret@postgres:5432/mycfc",
		"PRIVACY_ACTIVATION_DISABLE_EXPECTED_DATABASE": "mycfc",
		"PRIVACY_ACTIVATION_DISABLE_ACTOR_REF":         actor,
	}
	for _, test := range []struct {
		name string
		open func(context.Context, string) (activationDisableDatabase, error)
	}{
		{name: "open", open: func(context.Context, string) (activationDisableDatabase, error) {
			return nil, errors.New("unavailable")
		}},
		{name: "call", open: func(context.Context, string) (activationDisableDatabase, error) {
			return &disableDatabaseFake{scan: func(...any) error { return errors.New("rejected") }}, nil
		}},
		{name: "invalid version", open: func(context.Context, string) (activationDisableDatabase, error) {
			return &disableDatabaseFake{scan: func(destinations ...any) error { *destinations[0].(*int64) = 0; return nil }}, nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			withDisableRuntime(t, 0, test.open)
			var output bytes.Buffer
			if err := runDisable(t.Context(), func(name string) string { return env[name] }, &output); err == nil {
				t.Fatal("disable failure accepted")
			}
			if output.Len() != 0 {
				t.Fatalf("failure emitted success output %q", output.String())
			}
		})
	}
	withDisableRuntime(t, 0, func(context.Context, string) (activationDisableDatabase, error) {
		return &disableDatabaseFake{scan: func(destinations ...any) error {
			*destinations[0].(*int64) = 1
			*destinations[1].(*string) = "mycfc"
			*destinations[2].(*bool) = true
			*destinations[3].(*bool) = false
			return nil
		}}, nil
	})
	if err := runDisable(t.Context(), func(name string) string { return env[name] }, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func withDisableRuntime(t *testing.T, euid int, open func(context.Context, string) (activationDisableDatabase, error)) {
	t.Helper()
	originalEUID, originalOpen := effectiveUserID, openActivationDisableDatabase
	effectiveUserID = func() int { return euid }
	if open != nil {
		openActivationDisableDatabase = open
	}
	t.Cleanup(func() {
		effectiveUserID, openActivationDisableDatabase = originalEUID, originalOpen
	})
}
