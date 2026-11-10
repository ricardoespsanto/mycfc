//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/trainingcompat"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const startupPublisher = "local_recipient_web"

func testLocalPreparationRefusals(t *testing.T, root *pgx.Conn, migrationURL string) {
	t.Helper()
	for _, tc := range []struct{ name, password, message string }{
		{"migration login rejected", "wrong", "migration connection rejected"},
		{"bootstrap login rejected", "", "bootstrap connection rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.password != "" {
				parsed, err := url.Parse(migrationURL)
				if err != nil {
					t.Fatal(err)
				}
				parsed.User = url.UserPassword("local_recipient_migration", tc.password)
				t.Setenv("DATABASE_URL", parsed.String())
			} else {
				t.Setenv("POSTGRES_PASSWORD", "wrong")
			}
			if err := runServerCommand(t.Context(), []string{"prepare-local-recipient-floor"}); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("err = %v, want %q", err, tc.message)
			}
		})
	}
	binding, err := localManifestBinding(t.Context(), root, db.RoleCredentials{AppUsername: startupPublisher, MigrationUsername: "local_recipient_migration"})
	if err != nil {
		t.Fatal(err)
	}
	record, err := openLocalRecipientManifest(binding, false)
	if err != nil {
		t.Fatal(err)
	}
	original := record.Value
	record.Close()
	var state db.TrainingScheduleState
	low, err := pgx.Connect(t.Context(), migrationURL)
	if err != nil {
		t.Fatal(err)
	}
	defer low.Close(t.Context())
	state, err = db.TrainingScheduleStatus(t.Context(), low)
	if err != nil {
		t.Fatal(err)
	}
	originalIntent := localRecipientFloorIntent{
		Incarnation: state.DatabaseIncarnation.String(), Commit: uuid.NewString(), ManifestSHA256: strings.Repeat("a", 64),
		ExpectedEpoch: state.StateEpoch, MinimumReader: trainingcompat.ReaderGeneration,
	}
	writeRecord := func(value localRecipientManifest) {
		t.Helper()
		record, err := openLocalRecipientManifest(binding, false)
		if err != nil {
			t.Fatal(err)
		}
		defer record.Close()
		record.Value = value
		if err := record.Save(); err != nil {
			t.Fatal(err)
		}
	}
	defer writeRecord(original)
	for _, tc := range []struct {
		name, want string
		change     func(*localRecipientManifest, *localRecipientFloorIntent)
	}{
		{"interrupted bootstrap", "bootstrap provenance incomplete", func(m *localRecipientManifest, i *localRecipientFloorIntent) { m.Phase = "captured" }},
		{"invalid incarnation", "intent incarnation invalid", func(m *localRecipientManifest, i *localRecipientFloorIntent) { i.Incarnation = "incomplete" }},
		{"invalid commit", "intent commit invalid", func(m *localRecipientManifest, i *localRecipientFloorIntent) { i.Commit = "incomplete" }},
		{"different incarnation", "intent binding changed", func(m *localRecipientManifest, i *localRecipientFloorIntent) { i.Incarnation = uuid.NewString() }},
		{"stale intent epoch", "state changed", func(m *localRecipientManifest, i *localRecipientFloorIntent) { i.ExpectedEpoch-- }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, intent := original, originalIntent
			value.Intent = &intent
			tc.change(&value, &intent)
			writeRecord(value)
			if err := runServerCommand(t.Context(), []string{"prepare-local-recipient-floor"}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			after, err := db.TrainingScheduleStatus(t.Context(), low)
			if err != nil || after.StateEpoch != state.StateEpoch || after.FloorCommitID != nil {
				t.Fatal("refusal changed the real floor", err)
			}
			var login bool
			if err := root.QueryRow(t.Context(), "SELECT rolcanlogin FROM pg_catalog.pg_roles WHERE rolname=$1", startupPublisher).Scan(&login); err != nil || !login {
				t.Fatal("refusal fenced the publisher before validating its recorded intent", err)
			}
		})
	}
}

func testPreparedPrecisionStatus(t *testing.T, servingURL string, state db.TrainingScheduleState) {
	t.Helper()
	old := loadDatabaseCommandConfig
	t.Cleanup(func() { loadDatabaseCommandConfig = old })
	loadDatabaseCommandConfig = func(context.Context) (config.Config, error) {
		return config.Config{DatabaseURL: config.Secret(servingURL)}, nil
	}
	output, err := os.CreateTemp(t.TempDir(), "precision-status")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	previous := os.Stdout
	os.Stdout = output
	t.Cleanup(func() { os.Stdout = previous })
	if err := runServerCommand(t.Context(), []string{"precision-status"}); err != nil {
		t.Fatal("serving identity could not read the admitted state", err)
	}
	os.Stdout = previous
	if _, err := output.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var result recipientScheduleStatus
	if err := json.NewDecoder(output).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Contract != "mycfc/training-schedule-status/v1" || result.DatabaseIncarnation != state.DatabaseIncarnation.String() ||
		result.StateEpoch != state.StateEpoch || result.RequiredReader != trainingcompat.ReaderGeneration ||
		result.FloorCommitID == nil || *result.FloorCommitID != state.FloorCommitID.String() ||
		result.FloorManifestSHA256 == nil || *result.FloorManifestSHA256 != *state.FloorManifestSHA256 ||
		!result.PartialV2Enabled || !result.EverPartialV2Enabled {
		t.Fatalf("unexpected status: %+v", result)
	}
}

func testLocalPublisherAndFloorRefusals(t *testing.T, root, low *pgx.Conn, state db.TrainingScheduleState) {
	t.Helper()
	ctx := t.Context()
	if err := requireRecipientBootstrapIdentity(ctx, low, low.Config().User); err == nil {
		t.Fatal("low migration role passed bootstrap identity")
	}
	publisher, err := db.InspectTrainingRecipientPublisher(ctx, root, startupPublisher)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, role, message string
		conn                *pgx.Conn
		oid                 uint32
	}{
		{"unknown role", "local_absent_publisher", "publisher", root, 0},
		{"different OID", startupPublisher, "publisher role identity changed", root, publisher.OID + 1},
		{"role mutation requires bootstrap", startupPublisher, "publisher LOGIN change failed", low, publisher.OID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := pgx.ConnectConfig(t.Context(), tc.conn.Config())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close(t.Context())
			err = withRecipientPolicySession(t.Context(), conn, func(ctx context.Context) error {
				_, err := setRecipientPublisherLogin(ctx, conn, tc.role, tc.oid, false)
				return err
			})
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("err = %v, want %q", err, tc.message)
			}
			var login bool
			if err := root.QueryRow(t.Context(), "SELECT rolcanlogin FROM pg_catalog.pg_roles WHERE oid=$1", publisher.OID).Scan(&login); err != nil || !login {
				t.Fatal("failed fence changed the real publisher", err)
			}
		})
	}

	inspected, err := inspectRecipientConnection(ctx, low, low.Config().Database, db.RoleCredentials{AppUsername: startupPublisher, MigrationUsername: low.Config().User}, db.RecipientInspectFinal)
	if err != nil {
		t.Fatal(err)
	}
	input := recipientFloorInput{Incarnation: state.DatabaseIncarnation, Commit: uuid.New(), Epoch: state.StateEpoch, Manifest: strings.Repeat("c", 64), Reader: state.RequiredReader}
	changed := input
	changed.Incarnation = uuid.New()
	if _, err := commitRecipientFloor(ctx, low, inspected, changed); err == nil || !strings.Contains(err.Error(), "inspected state changed") {
		t.Fatal("foreign floor incarnation accepted", err)
	}
	changed = input
	changed.Reader++
	if _, err := commitRecipientFloor(ctx, low, inspected, changed); err == nil || !strings.Contains(err.Error(), "requires publisher NOLOGIN") {
		t.Fatal("higher floor accepted a login-enabled publisher", err)
	}
	changed = input
	changed.Epoch--
	stale := state
	stale.StateEpoch = changed.Epoch
	staleInspection := inspected
	staleInspection.ScheduleState = &stale
	failed, err := pgx.ConnectConfig(ctx, low.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer failed.Close(ctx)
	err = withRecipientPolicySession(ctx, failed, func(ctx context.Context) error {
		_, err := commitRecipientFloor(ctx, failed, staleInspection, changed)
		return err
	})
	if err == nil {
		t.Fatal("database accepted stale floor CAS")
	}
	after, err := db.TrainingScheduleStatus(ctx, low)
	if err != nil || after.StateEpoch != state.StateEpoch || *after.FloorCommitID != *state.FloorCommitID {
		t.Fatal("stale CAS changed actual floor", err)
	}
}

func testIndependentRecipientInspectionRefusals(t *testing.T, root, low *pgx.Conn) {
	t.Helper()
	ctx := t.Context()
	roles := db.RoleCredentials{
		AppUsername:       startupPublisher,
		MigrationUsername: low.Config().User,
		MigrationPassword: low.Config().Password,
	}
	for _, tc := range []struct {
		name, database, password, want string
	}{
		{"different database", "postgres", roles.MigrationPassword, "target identity rejected"},
		{"missing migration secret", root.Config().Database, "", "credentials required"},
		{"wrong migration secret", root.Config().Database, "synthetic-wrong", "connection rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configured := roles
			configured.MigrationPassword = tc.password
			if _, err := inspectRecipientFromOriginal(ctx, root, tc.database, configured, db.RecipientInspectFinal); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	t.Run("same server but wrong database", func(t *testing.T) {
		cfg := root.Config().Copy()
		cfg.Database = "postgres"
		other, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close(ctx)
		if err := verifyRecipientInspectorBackend(ctx, other, low, root.Config().Database, roles.MigrationUsername); err == nil || !strings.Contains(err.Error(), "does not match original target") {
			t.Fatalf("inspector accepted a backend from another database: %v", err)
		}
	})
	t.Run("mutation refuses unavailable role configuration", func(t *testing.T) {
		t.Setenv("APP_DB_USER", "")
		old := loadDatabaseCommandConfig
		t.Cleanup(func() { loadDatabaseCommandConfig = old })
		loadDatabaseCommandConfig = func(context.Context) (config.Config, error) {
			return config.Config{}, errors.New("fixture configuration unavailable")
		}
		original, err := pgx.ConnectConfig(t.Context(), low.Config())
		if err != nil {
			t.Fatal(err)
		}
		defer original.Close(t.Context())
		err = withRecipientPolicySession(t.Context(), original, func(ctx context.Context) error {
			return requireRecipientMutationAdmission(ctx, original)
		})
		if !original.IsClosed() {
			t.Fatal("a rejected mutation left its policy session available for reuse")
		}
		if err == nil || !strings.Contains(err.Error(), "role configuration unavailable") {
			t.Fatalf("mutation accepted missing role configuration: %v", err)
		}
	})
	t.Run("mutation refuses invalid migration identity", func(t *testing.T) {
		t.Setenv("MIGRATION_DB_USER", "nonexistent_fixture_role")
		original, err := pgx.ConnectConfig(t.Context(), low.Config())
		if err != nil {
			t.Fatal(err)
		}
		defer original.Close(t.Context())
		err = withRecipientPolicySession(t.Context(), original, func(ctx context.Context) error {
			return requireRecipientMutationAdmission(ctx, original)
		})
		if !original.IsClosed() {
			t.Fatal("a rejected mutation left its policy session available for reuse")
		}
		if err == nil || !strings.Contains(err.Error(), "independent migration connection rejected") {
			t.Fatalf("mutation continued without an authenticated inspector: %v", err)
		}
	})
}

func testLocalAuthoringRejectsMissingActivationWitness(t *testing.T, root, low *pgx.Conn, state db.TrainingScheduleState) {
	t.Helper()
	ctx := t.Context()
	roles := db.RoleCredentials{AppUsername: startupPublisher, MigrationUsername: low.Config().User}
	binding, err := localManifestBinding(ctx, root, roles)
	if err != nil {
		t.Fatal(err)
	}
	record, err := openLocalRecipientManifest(binding, false)
	if err != nil {
		t.Fatal(err)
	}
	defer record.Close()
	original := record.Value
	target := recipientCommandTarget{Database: root.Config().Database, Roles: roles}
	for _, tc := range []struct {
		name, want string
		change     func(*localRecipientManifest, *db.TrainingScheduleState)
	}{
		{"missing floor witness", "floor/role binding rejected", func(_ *localRecipientManifest, s *db.TrainingScheduleState) { s.FloorCommitID = nil }},
		{"active gate without recorded intent", "no matching recorded activation intent", func(m *localRecipientManifest, _ *db.TrainingScheduleState) { m.AuthoringExpectedEpoch = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, candidate := original, state
			tc.change(&value, &candidate)
			record.Value = value
			if err := completeLocalAuthoringPreparation(ctx, low, root, target, record, candidate); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			after, err := db.TrainingScheduleStatus(ctx, low)
			if err != nil || after.StateEpoch != state.StateEpoch || !after.PartialV2Enabled {
				t.Fatal("rejected witness changed the authoring gate", err)
			}
			var login bool
			if err := root.QueryRow(ctx, "SELECT rolcanlogin FROM pg_catalog.pg_roles WHERE rolname=$1", startupPublisher).Scan(&login); err != nil || !login {
				t.Fatal("rejected witness changed publisher LOGIN", err)
			}
		})
	}
}

// Close the real fixture connection before its COMMIT is sent. A caller
// must still treat the failed acknowledgement as unknown and inspect state.
type localCommitDisconnect struct{ interrupted bool }

func (f *localCommitDisconnect) TraceQueryStart(ctx context.Context, conn *pgx.Conn, query pgx.TraceQueryStartData) context.Context {
	if strings.EqualFold(query.SQL, "commit") && !f.interrupted {
		f.interrupted = true
		_ = conn.Close(ctx)
	}
	return ctx
}

func (*localCommitDisconnect) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func testLocalRecipientTransactionDisconnect(t *testing.T, root, low *pgx.Conn, state db.TrainingScheduleState) {
	t.Helper()
	ctx := t.Context()
	roles := db.RoleCredentials{AppUsername: startupPublisher, MigrationUsername: low.Config().User}
	inspected, err := inspectRecipientConnection(ctx, low, low.Config().Database, roles, db.RecipientInspectFinal)
	if err != nil {
		t.Fatal(err)
	}
	floor := recipientFloorInput{Incarnation: state.DatabaseIncarnation, Commit: uuid.New(), Epoch: state.StateEpoch, Manifest: strings.Repeat("d", 64), Reader: state.RequiredReader}
	for _, tc := range []struct {
		name     string
		original *pgx.Conn
		mutate   func(context.Context, *pgx.Conn) error
	}{
		{"publisher", root, func(ctx context.Context, conn *pgx.Conn) error {
			got, err := setRecipientPublisherLogin(ctx, conn, startupPublisher, inspected.Publisher.OID, false)
			if got.OID != 0 {
				t.Error("unconfirmed publisher change returned an acknowledgement")
			}
			return err
		}},
		{"floor", low, func(ctx context.Context, conn *pgx.Conn) error {
			got, err := commitRecipientFloor(ctx, conn, inspected, floor)
			if got.FloorCommitID != nil {
				t.Error("unconfirmed floor change returned an acknowledgement")
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.original.Config().Copy()
			closed, err := pgx.ConnectConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := closed.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := tc.mutate(t.Context(), closed); err == nil {
				t.Fatal("a closed session acknowledged a transaction")
			}
			fault := &localCommitDisconnect{}
			cfg.Tracer = fault
			connected, err := pgx.ConnectConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer connected.Close(t.Context())
			err = withRecipientPolicySession(t.Context(), connected, func(ctx context.Context) error {
				return tc.mutate(ctx, connected)
			})
			if !fault.interrupted || err == nil || !strings.Contains(err.Error(), "commit not acknowledged; inspect current state before retry") {
				t.Fatalf("commit interruption did not require inspection: %v", err)
			}
		})
	}
	after, err := db.TrainingScheduleStatus(ctx, low)
	if err != nil || after.StateEpoch != state.StateEpoch || after.FloorCommitID == nil || *after.FloorCommitID != *state.FloorCommitID {
		t.Fatal("interrupted transaction changed the fixture floor", err)
	}
	publisher, err := db.InspectTrainingRecipientPublisher(ctx, root, startupPublisher)
	if err != nil || publisher.OID != inspected.Publisher.OID || !publisher.CanLogin {
		t.Fatal("interrupted transaction changed the fixture publisher", err)
	}
}

func testLocalTerminalJournalWithoutFloorIntent(t *testing.T, root, low *pgx.Conn, state db.TrainingScheduleState) {
	t.Helper()
	ctx := t.Context()
	binding, err := localManifestBinding(ctx, root, db.RoleCredentials{AppUsername: startupPublisher, MigrationUsername: low.Config().User})
	if err != nil {
		t.Fatal(err)
	}
	record, err := openLocalRecipientManifest(binding, false)
	if err != nil {
		t.Fatal(err)
	}
	original := record.Value
	record.Close()
	writeRecord := func(value localRecipientManifest) {
		t.Helper()
		file, err := openLocalRecipientManifest(binding, false)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		file.Value = value
		if err := file.Save(); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { writeRecord(original) })
	withoutIntent := original
	withoutIntent.Intent = nil
	withoutIntent.Phase = "floor_committed"
	writeRecord(withoutIntent)
	if err := runServerCommand(ctx, []string{"prepare-local-recipient-floor"}); err == nil || !strings.Contains(err.Error(), "requires recorded intent inspection") {
		t.Fatalf("unfinished journal resumed without its floor intent: %v", err)
	}
	withoutIntent.Phase = "restored"
	writeRecord(withoutIntent)
	if err := runServerCommand(ctx, []string{"prepare-local-recipient-floor"}); err != nil {
		t.Fatal("terminal journal could not report its unchanged acknowledged state", err)
	}
	after, err := db.TrainingScheduleStatus(ctx, low)
	if err != nil || after.StateEpoch != state.StateEpoch || after.FloorCommitID == nil || *after.FloorCommitID != *state.FloorCommitID {
		t.Fatal("reporting the terminal journal changed its floor", err)
	}
}
