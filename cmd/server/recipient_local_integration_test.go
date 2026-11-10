//go:build integration

package main

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/jackc/pgx/v5"
)

func TestLocalRecipientConstructiveBootstrapFloorRealPostgres(t *testing.T) {
	raw := strings.Replace(isolatedFreshInitURL(t), "127.0.0.1:", "localhost:", 1)
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := parsed.User.Password()
	t.Chdir(t.TempDir())
	t.Setenv("APP_ENV", "local")
	t.Setenv("APP_VERSION", "dev")
	t.Setenv("GIT_SHA", strings.Repeat("0", 40))
	t.Setenv("TRAINING_SCHEDULE_ADMISSION_FILE", "")
	t.Setenv("POSTGRES_USER", parsed.User.Username())
	t.Setenv("POSTGRES_PASSWORD", password)
	for key, value := range map[string]string{"APP_DB_USER": "local_recipient_web", "APP_DB_PASSWORD": "synthetic-web", "MIGRATION_DB_USER": "local_recipient_migration", "MIGRATION_DB_PASSWORD": "synthetic-migration", "MEDIA_CLEANUP_DB_USER": "local_recipient_media", "MEDIA_CLEANUP_DB_PASSWORD": "synthetic-media", "DATA_RETENTION_DB_USER": "local_recipient_retention", "DATA_RETENTION_DB_PASSWORD": "synthetic-retention"} {
		t.Setenv(key, value)
	}
	t.Setenv("DATABASE_URL", raw)
	if err := runServerCommand(t.Context(), []string{"bootstrap-db"}); err != nil {
		t.Fatal("constructive bootstrap", err)
	}
	migration := *parsed
	migration.User = url.UserPassword("local_recipient_migration", "synthetic-migration")
	t.Setenv("DATABASE_URL", migration.String())
	if err := runServerCommand(t.Context(), []string{"migrate"}); err != nil {
		t.Fatal("prepared exact migration", err)
	}
	// Role LOGIN is global: an unrelated-database backend must prevent fencing.
	root, err := pgx.Connect(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close(context.Background())
	other := *parsed
	other.Path = "/postgres"
	other.User = url.UserPassword("local_recipient_web", "synthetic-web")
	active, err := pgx.Connect(t.Context(), other.String())
	if err != nil {
		t.Fatal("other database publisher connection", err)
	}
	if err = runServerCommand(t.Context(), []string{"prepare-local-recipient-floor"}); err == nil {
		t.Fatal("global publisher backend was ignored")
	}
	active.Close(t.Context())
	if err := runServerCommand(t.Context(), []string{"prepare-local-recipient-floor"}); err != nil {
		t.Fatal("local floor", err)
	}
	low, err := pgx.Connect(t.Context(), migration.String())
	if err != nil {
		t.Fatal(err)
	}
	defer low.Close(context.Background())
	state, err := db.TrainingScheduleStatus(t.Context(), low)
	if err != nil || state.RequiredReader != 3 || state.FloorCommitID == nil {
		t.Fatal("floor not acknowledged", err)
	}
	original := state
	if err = runServerCommand(t.Context(), []string{"prepare-local-recipient-floor"}); err != nil {
		t.Fatal("idempotent local floor", err)
	}
	state, err = db.TrainingScheduleStatus(t.Context(), low)
	if err != nil || state.StateEpoch != original.StateEpoch || *state.FloorCommitID != *original.FloorCommitID {
		t.Fatal("repeated floor changed state", err)
	}
	var login bool
	if err = low.QueryRow(t.Context(), `SELECT rolcanlogin FROM pg_catalog.pg_roles WHERE rolname='local_recipient_web'`).Scan(&login); err != nil || !login {
		t.Fatal("original local LOGIN not restored", err)
	}
	// A different project directory cannot retroactively adopt this populated DB.
	t.Chdir(t.TempDir())
	if err = runServerCommand(t.Context(), []string{"prepare-local-recipient-floor"}); err == nil {
		t.Fatal("unrecorded populated fixture adopted")
	}
}
