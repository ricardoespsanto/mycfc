package main

import (
	"context"
	"strings"
	"testing"
)

// Explicit unit-only lifecycle injection: existing command tests observe
// resolution/dispatch and SQL, not pooled/session PostgreSQL lock behavior.
// The real default is tested separately and must reject fake physical sessions.
func allowDatabaseCommandFakeLifecycle(t *testing.T) {
	t.Helper()
	previous := runOriginalRecipientWriter
	t.Cleanup(func() { runOriginalRecipientWriter = previous })
	runOriginalRecipientWriter = func(ctx context.Context, _ databaseCommandConnection, body func(context.Context) error) error {
		return body(ctx)
	}
}

func TestOriginalRecipientWriterRejectsNonPhysicalSession(t *testing.T) {
	called := false
	err := withOriginalRecipientWriter(t.Context(), &databaseCommandConnectionFake{}, func(context.Context) error { called = true; return nil })
	if err == nil || !strings.Contains(err.Error(), "original physical connection") || called {
		t.Fatal("nonphysical session bypassed writer admission", err)
	}
}
