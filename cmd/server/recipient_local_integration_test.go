//go:build integration

package main

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/trainingcompat"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func setupLocalRecipientFixture(t *testing.T) (string, *url.URL, string) {
	t.Helper()
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
	t.Run("interrupted bootstrap only records captured identity", func(t *testing.T) {
		testInterruptedLocalBootstrapProvenance(t, raw)
	})
	if err := runServerCommand(t.Context(), []string{"bootstrap-db"}); err != nil {
		t.Fatal("constructive bootstrap", err)
	}
	migration := *parsed
	migration.User = url.UserPassword("local_recipient_migration", "synthetic-migration")
	t.Setenv("DATABASE_URL", migration.String())
	if err := runServerCommand(t.Context(), []string{"migrate"}); err != nil {
		t.Fatal("prepared exact migration", err)
	}
	return raw, parsed, migration.String()
}

func TestLocalRecipientConstructiveBootstrapFloorRealPostgres(t *testing.T) {
	raw, parsed, migration := setupLocalRecipientFixture(t)
	if err := runServerCommand(t.Context(), []string{"migrate"}); err == nil || !strings.Contains(err.Error(), "publisher NOLOGIN and proved drain") {
		t.Fatalf("pre-floor final ledger accepted a live publisher: %v", err)
	}
	// Role LOGIN is global: an unrelated-database backend must prevent fencing.
	root, err := pgx.Connect(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close(context.Background())
	t.Run("recorded preparation refusals", func(t *testing.T) {
		testLocalPreparationRefusals(t, root, migration)
	})
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
	low, err := pgx.Connect(t.Context(), migration)
	if err != nil {
		t.Fatal(err)
	}
	defer low.Close(context.Background())
	state, err := db.TrainingScheduleStatus(t.Context(), low)
	if err != nil || state.RequiredReader != trainingcompat.ReaderGeneration || state.FloorCommitID == nil || !state.PartialV2Enabled || state.SchemaContract != db.TrainingScheduleContractP {
		t.Fatal("floor not acknowledged", err)
	}
	t.Run("serving precision status", func(t *testing.T) {
		web := *parsed
		web.User = url.UserPassword("local_recipient_web", "synthetic-web")
		testPreparedPrecisionStatus(t, web.String(), state)
	})
	t.Run("publisher and floor transaction refusals", func(t *testing.T) {
		testLocalPublisherAndFloorRefusals(t, root, low, state)
	})
	t.Run("independent inspection refuses mismatched target or credentials", func(t *testing.T) {
		testIndependentRecipientInspectionRefusals(t, root, low)
	})
	t.Run("authoring requires its exact activation witness", func(t *testing.T) {
		testLocalAuthoringRejectsMissingActivationWitness(t, root, low, state)
	})
	t.Run("transaction disconnects require state inspection", func(t *testing.T) {
		testLocalRecipientTransactionDisconnect(t, root, low, state)
	})
	t.Run("only terminal journal may report state without floor intent", func(t *testing.T) {
		testLocalTerminalJournalWithoutFloorIntent(t, root, low, state)
	})
	t.Run("authoring restore requires current floor and publisher evidence", func(t *testing.T) {
		testLocalAuthoringRestoreNeedsFreshEvidence(t, root, low, state)
	})
	t.Run("local provenance rejects an unbound target", func(t *testing.T) {
		testLocalProvenanceRefusesUnboundTarget(t, low)
	})
	t.Run("bootstrap provenance preserves exact journal ownership", func(t *testing.T) {
		testLocalProvenanceJournalLifecycle(t, root, low)
	})
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
	// Model a process exit after the real enable committed but before its
	// acknowledgement was durably recorded. Resume must reconcile that exact
	// intent, not execute another enable or refresh its epoch.
	binding, err := localManifestBinding(t.Context(), root, db.RoleCredentials{AppUsername: "local_recipient_web", MigrationUsername: "local_recipient_migration"})
	if err != nil {
		t.Fatal(err)
	}
	record, err := openLocalRecipientManifest(binding, false)
	if err != nil {
		t.Fatal(err)
	}
	if record.Value.AuthoringExpectedEpoch == nil || record.Value.AuthoringPreparedEpoch == nil || *record.Value.AuthoringPreparedEpoch != state.StateEpoch {
		t.Fatal("activation acknowledgement absent")
	}
	record.Value.AuthoringPreparedEpoch = nil
	record.Value.Phase = "floor_committed"
	if err = record.Save(); err != nil {
		t.Fatal(err)
	}
	record.Close()
	if _, err = root.Exec(t.Context(), `ALTER ROLE local_recipient_web NOLOGIN`); err != nil {
		t.Fatal(err)
	}
	if err = runServerCommand(t.Context(), []string{"prepare-local-recipient-floor"}); err != nil {
		t.Fatal("recorded enable acknowledgement recovery", err)
	}
	state, err = db.TrainingScheduleStatus(t.Context(), low)
	if err != nil || state.StateEpoch != original.StateEpoch || !state.PartialV2Enabled {
		t.Fatal("resume replayed enable", err)
	}
	if err = low.QueryRow(t.Context(), `SELECT rolcanlogin FROM pg_catalog.pg_roles WHERE rolname='local_recipient_web'`).Scan(&login); err != nil || !login {
		t.Fatal("matching recovery did not restore original LOGIN", err)
	}
	// A separately closed gate is a different decision. The recorded command
	// must not reopen it or restore a publisher fenced by that later decision.
	if _, err = root.Exec(t.Context(), `ALTER ROLE local_recipient_web NOLOGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err = low.Exec(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,false)`, state.DatabaseIncarnation, state.StateEpoch); err != nil {
		t.Fatal(err)
	}
	if err = runServerCommand(t.Context(), []string{"prepare-local-recipient-floor"}); err == nil {
		t.Fatal("independently closed authoring gate reopened")
	}
	state, err = db.TrainingScheduleStatus(t.Context(), low)
	if err != nil || state.PartialV2Enabled {
		t.Fatal("closed gate changed", err)
	}
	if err = low.QueryRow(t.Context(), `SELECT rolcanlogin FROM pg_catalog.pg_roles WHERE rolname='local_recipient_web'`).Scan(&login); err != nil || login {
		t.Fatal("unexpected publisher LOGIN restored", err)
	}
	t.Run("recorded activation cannot reopen a separately closed gate", func(t *testing.T) {
		testClosedAuthoringRejectsRecordedActivation(t, root, low, state)
	})
	if _, err = low.Exec(t.Context(), `SELECT training_schedule_control.commit_floor($1,$2,$3,$4,$5)`, state.DatabaseIncarnation, state.StateEpoch, uuid.New(), strings.Repeat("b", 64), trainingcompat.ReaderGeneration+1); err != nil {
		t.Fatal(err)
	}
	if err = runServerCommand(t.Context(), []string{"prepare-local-recipient-floor"}); err == nil {
		t.Fatal("future reader floor adopted")
	}
	if err = runServerCommand(t.Context(), []string{"migrate"}); err == nil || !strings.Contains(err.Error(), "newer reader generation") {
		t.Fatalf("migration accepted the future reader floor: %v", err)
	}
	// A different project directory cannot retroactively adopt this populated DB.
	t.Chdir(t.TempDir())
	if err = runServerCommand(t.Context(), []string{"prepare-local-recipient-floor"}); err == nil {
		t.Fatal("unrecorded populated fixture adopted")
	}
}
