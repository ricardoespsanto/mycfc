//go:build integration

package privacyrequests

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	mycfcdb "github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSyntheticAcceptanceRealRolesAndHandlers(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	database := "runner_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{database}.Sanitize()+" WITH (FORCE)")
		for _, role := range []string{"acceptance_app", "acceptance_migration", "mycfc_privacy_executor", "mycfc_privacy_acceptance", "mycfc_privacy_retention", "mycfc_privacy_activation_broker"} {
			_, _ = admin.Exec(context.Background(), "DROP ROLE "+pgx.Identifier{role}.Sanitize())
		}
	}()
	adminConfig := admin.Config().Copy()
	adminConfig.Database = database
	databaseAdmin, err := pgx.ConnectConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer databaseAdmin.Close(ctx)
	password := strings.Repeat("test-only-", 5)
	credentials := mycfcdb.RoleCredentials{AppUsername: "acceptance_app", AppPassword: password, MigrationUsername: "acceptance_migration", MigrationPassword: password, PrivacyExecutorUsername: "mycfc_privacy_executor", PrivacyExecutorPassword: password, PrivacyActivationBrokerUsername: "mycfc_privacy_activation_broker", PrivacyActivationBrokerPassword: password}
	if err = mycfcdb.BootstrapRoles(ctx, databaseAdmin, database, credentials); err != nil {
		t.Fatal(err)
	}
	migrationConfig := adminConfig.Copy()
	migrationConfig.User = credentials.MigrationUsername
	migrationConfig.Password = password
	migrationConnection, e := pgx.ConnectConfig(ctx, migrationConfig)
	if e != nil {
		t.Fatal(e)
	}
	defer migrationConnection.Close(ctx)
	if err = mycfcdb.ApplyBaselineAndHarden(ctx, migrationConnection, database, credentials); err != nil {
		t.Fatal(err)
	}
	adminPoolConfig, _ := pgxpool.ParseConfig(dsn)
	adminPoolConfig.ConnConfig.Database = database
	pool, err := pgxpool.NewWithConfig(ctx, adminPoolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	owner, executor, real := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{owner, executor, real} {
		if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Real isolated test fixture',$2,'hash','1980-01-01')`, id, id.String()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, owner); err != nil {
		t.Fatal(err)
	}
	service := Service{Pool: pool, Enabled: true, Key: []byte(strings.Repeat("k", 32))}
	policy := testPolicy()
	policy.Version = "acceptance-fixture"
	policy.WorkingRetentionDays = ApprovedWorkingRetentionDays
	if err = service.ImportPolicy(ctx, owner, policy); err != nil {
		t.Fatal(err)
	}
	if err = service.GrantExecutor(ctx, owner, executor, false); err != nil {
		t.Fatal(err)
	}
	activatePrivacyIntegrationFixture(t, ctx, pool, owner, executor, policy.Version)
	if err = mycfcdb.ConfigurePrivacyAcceptanceRole(ctx, databaseAdmin, database, password, false); err != nil {
		t.Fatal(err)
	}
	operatorConfig := adminConfig.Copy()
	operatorConfig.User = "mycfc_privacy_acceptance"
	operatorConfig.Password = password
	operator, err := pgx.ConnectConfig(ctx, operatorConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer operator.Close(ctx)
	workerConfig := adminPoolConfig.Copy()
	workerConfig.ConnConfig.User = "mycfc_privacy_executor"
	workerConfig.ConnConfig.Password = password
	worker, err := pgxpool.NewWithConfig(ctx, workerConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	appConfig := adminPoolConfig.Copy()
	appConfig.ConnConfig.User = "acceptance_app"
	appConfig.ConnConfig.Password = password
	appCheck, e := pgxpool.NewWithConfig(ctx, appConfig)
	if e != nil {
		t.Fatal(e)
	}
	defer appCheck.Close()
	policyRow, e := dbgen.New(appCheck).GetPrivacyPolicy(ctx, policy.Version)
	if e != nil {
		t.Fatalf("app policy read: %v", e)
	}
	if _, e = ReadPolicy(policyRow); e != nil {
		t.Fatalf("app policy decode: %v", e)
	}
	ready, e := dbgen.New(appCheck).PrivacyActivationReady(ctx, policy.Version)
	if e != nil || !ready {
		t.Fatalf("app policy readiness=%v error=%v", ready, e)
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := NewTombstoneProtector("test-tombstone", private.PublicKey().Bytes(), "test-locator", []byte(strings.Repeat("l", 32)))
	if err != nil {
		t.Fatal(err)
	}
	_, signingKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	validOptions := func() AcceptanceOptions {
		return AcceptanceOptions{Mode: "run", ExpectedDatabase: database, AppRole: "acceptance_app",
			ImageDigest: "sha256:" + strings.Repeat("7", 64), SchemaDigest: hex.EncodeToString([]byte(strings.Repeat("\x07", 32))),
			Operator: operator, AppConfig: appConfig, Worker: worker, Ledger: &capturingTombstoneLedger{}, Protector: protector}
	}
	assertRejected := func(t *testing.T, options AcceptanceOptions) {
		t.Helper()
		if result, runErr := RunSyntheticAcceptance(ctx, options); runErr == nil || !reflect.DeepEqual(result, AcceptanceEvidence{}) {
			t.Fatalf("unsafe acceptance result=%+v error=%v", result, runErr)
		}
	}
	t.Run("rejects operator database mismatch", func(t *testing.T) {
		options := validOptions()
		options.ExpectedDatabase = "other_database"
		assertRejected(t, options)
	})
	t.Run("rejects worker role mismatch", func(t *testing.T) {
		options := validOptions()
		options.Worker = appCheck
		assertRejected(t, options)
	})
	t.Run("rejects app connection mismatch", func(t *testing.T) {
		options := validOptions()
		missingRole := appConfig.Copy()
		missingRole.ConnConfig.User = "acceptance_missing_role"
		options.AppConfig = missingRole
		options.AppRole = "acceptance_missing_role"
		assertRejected(t, options)
	})
	t.Run("rejects elevated app connection", func(t *testing.T) {
		options := validOptions()
		options.AppConfig = adminPoolConfig
		options.AppRole = adminPoolConfig.ConnConfig.User
		assertRejected(t, options)
	})
	t.Run("rejects app activation mutation", func(t *testing.T) {
		options := validOptions()
		migrationPoolConfig := adminPoolConfig.Copy()
		migrationPoolConfig.ConnConfig.User = credentials.MigrationUsername
		migrationPoolConfig.ConnConfig.Password = password
		options.AppConfig = migrationPoolConfig
		options.AppRole = credentials.MigrationUsername
		assertRejected(t, options)
	})
	t.Run("rejects worker people mutation", func(t *testing.T) {
		if _, grantErr := databaseAdmin.Exec(ctx, `GRANT UPDATE ON TABLE users TO mycfc_privacy_executor`); grantErr != nil {
			t.Fatal(grantErr)
		}
		t.Cleanup(func() {
			_, _ = databaseAdmin.Exec(context.Background(), `REVOKE UPDATE ON TABLE users FROM mycfc_privacy_executor`)
		})
		assertRejected(t, validOptions())
	})
	t.Run("rejects denied fixture creation", func(t *testing.T) {
		if _, revokeErr := databaseAdmin.Exec(ctx, `REVOKE EXECUTE ON FUNCTION privacy_protected.acceptance_create(text,text,text) FROM mycfc_privacy_acceptance`); revokeErr != nil {
			t.Fatal(revokeErr)
		}
		t.Cleanup(func() {
			_, _ = databaseAdmin.Exec(context.Background(), `GRANT EXECUTE ON FUNCTION privacy_protected.acceptance_create(text,text,text) TO mycfc_privacy_acceptance`)
		})
		assertRejected(t, validOptions())
	})
	modes := []string{"run", "canary-heartbeat", "canary-failure", "canary-retry", "canary-recovery", "canary-aged"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			runContext, stopRun := context.WithCancel(ctx)
			defer stopRun()
			ageResult := make(chan error, 1)
			if mode == "canary-aged" {
				var cutoff time.Time
				if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&cutoff); err != nil {
					t.Fatal(err)
				}
				// Test-only DBA setup ages the newly registered fixture. The real
				// runner has neither a DBA credential nor a clock override.
				go func() {
					ageErr := ageAcceptanceTestFixture(runContext, pool, cutoff)
					ageResult <- ageErr
					if ageErr != nil {
						stopRun()
					}
				}()
			}
			ledger := &capturingTombstoneLedger{}
			var events []string
			result, err := RunSyntheticAcceptance(runContext, AcceptanceOptions{Mode: mode, ExpectedDatabase: database, AppRole: "acceptance_app", ImageDigest: "sha256:" + strings.Repeat("7", 64), SchemaDigest: hex.EncodeToString([]byte(strings.Repeat("\x07", 32))), Operator: operator, AppConfig: appConfig, Worker: worker, Ledger: ledger, Protector: protector, Event: func(_ context.Context, event string) error { events = append(events, event); return nil }})
			if mode == "canary-aged" {
				if ageErr := <-ageResult; ageErr != nil {
					t.Fatal(ageErr)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := map[string][]string{"run": nil, "canary-heartbeat": {"privacy_acceptance_canary_heartbeat_missing_observed", "privacy_acceptance_canary_recovery_observed"}, "canary-failure": {"privacy_acceptance_canary_failure_observed"}, "canary-retry": {"privacy_acceptance_canary_retry_observed", "privacy_acceptance_canary_recovery_observed"}, "canary-recovery": {"privacy_acceptance_canary_heartbeat_missing_observed", "privacy_acceptance_canary_retry_observed", "privacy_acceptance_canary_recovery_observed"}, "canary-aged": {"privacy_acceptance_canary_aged_observed", "privacy_acceptance_canary_recovery_observed"}}[mode]
			if !reflect.DeepEqual(events, expected) {
				t.Fatalf("events=%v expected=%v", events, expected)
			}
			if mode == "canary-failure" {
				if result.Outcome != "CANARY_VERIFIED" || result.ManifestSHA256 != "" || len(ledger.sealed) != 0 {
					t.Fatal("failure falsely claimed completed acceptance")
				}
			} else {
				if result.Outcome != "COMPLETED" || result.Checkpoints < 1 || result.SimulatedNotices < 1 || len(ledger.sealed) != 2 {
					t.Fatalf("incomplete real path: %+v ledger writes=%d", result, len(ledger.sealed))
				}
			}
			if _, err := SignAcceptanceEvidence(result, "real-role-test", signingKey); err != nil {
				t.Fatalf("observed evidence could not be signed: %v", err)
			}
			var unfinished int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM privacy_protected.acceptance_fixtures fixture
			 WHERE NOT EXISTS(SELECT 1 FROM privacy_protected.acceptance_finished WHERE fixture_id=fixture.id)
			 OR EXISTS(SELECT 1 FROM users WHERE id IN(fixture.subject_ref,fixture.reviewer_ref,fixture.executor_ref) AND is_active)`).Scan(&unfinished); err != nil || unfinished != 0 {
				t.Fatalf("synthetic cleanup incomplete: %d %v", unfinished, err)
			}
			var active bool
			var mail int
			if err = pool.QueryRow(ctx, `SELECT is_active FROM users WHERE id=$1`, real).Scan(&active); err != nil || !active {
				t.Fatalf("ordinary person changed: %v", err)
			}
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE sent_at IS NOT NULL OR status IN('PENDING','SENDING')`).Scan(&mail); err != nil || mail != 0 {
				t.Fatalf("synthetic notice escaped: %d %v", mail, err)
			}
		})
	}
}

// This helper is compiled only for integration tests and operates inside the
// test-created database. Require exactly one newly registered, unfinished
// synthetic execution before changing that fixture's pending-job timestamps.
func ageAcceptanceTestFixture(parent context.Context, pool *pgxpool.Pool, cutoff time.Time) error {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	for {
		var executions []uuid.UUID
		err := pool.QueryRow(ctx, `SELECT coalesce(array_agg(execution.id), ARRAY[]::uuid[])
		 FROM privacy_erasure_executions execution
		 JOIN privacy_protected.acceptance_requests request ON request.request_id=execution.request_id
		 JOIN privacy_protected.acceptance_fixtures fixture ON fixture.id=request.fixture_id
		 WHERE fixture.created_at >= $1 AND octet_length(fixture.marker_sha256)=32
		 AND NOT EXISTS(SELECT 1 FROM privacy_protected.acceptance_finished WHERE fixture_id=fixture.id)
		 AND EXISTS(SELECT 1 FROM privacy_erasure_category_jobs WHERE execution_id=execution.id AND status='PENDING')`, cutoff).Scan(&executions)
		if err != nil {
			return err
		}
		if len(executions) > 1 {
			return fmt.Errorf("ambiguous synthetic age fixture")
		}
		if len(executions) == 1 {
			tag, err := pool.Exec(ctx, `UPDATE privacy_erasure_category_jobs
			 SET created_at=clock_timestamp()-interval '17 minutes', updated_at=clock_timestamp()-interval '16 minutes'
			 WHERE execution_id=$1 AND status='PENDING'`, executions[0])
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return fmt.Errorf("synthetic age fixture had no pending jobs")
			}
			return nil
		}
		if err := acceptanceWait(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
}
