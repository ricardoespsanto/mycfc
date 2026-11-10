package main

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/cfcoimbra/mycfc/internal/trainingcompat"
	"github.com/jackc/pgx/v5"
)

// Called only after the original session owns P exclusive. Empty targets have
// their own exact catalog proof; inspection failure never implies emptiness.
func requireRecipientMutationAdmission(ctx context.Context, original databaseCommandConnection) error {
	conn, ok := original.(*pgx.Conn)
	if !ok {
		return errors.New("recipient admission requires original session")
	}
	roles := databaseRoleCredentialsFromEnvironment()
	if !databaseRoleIdentifiersComplete(roles) {
		cfg, err := loadDatabaseCommandConfig(ctx)
		if err != nil {
			return errors.New("recipient admission role configuration unavailable")
		}
		roles = databaseRoleCredentialsFromConfig(cfg)
	}
	roles.AppUsername = releasecontract.AppRole(roles.AppUsername)
	empty, err := db.IsTrainingRecipientEmptyTarget(ctx, conn, conn.Config().Database)
	if err != nil {
		return err
	}
	if empty {
		if strictUnstampedLocalConnection(conn.Config()) {
			return captureLocalRecipientProvenance(ctx, conn, roles)
		}
		return nil
	}
	// A prepared local fixture is an explicitly bounded structural exception,
	// never a general missing-ledger or failed-inspection fallback.
	var ledger bool
	if err := conn.QueryRow(ctx, `SELECT pg_catalog.to_regclass('mycfc_meta.schema_migrations') IS NOT NULL`).Scan(&ledger); err != nil {
		return errors.New("recipient ledger catalog inspection failed")
	}
	if !ledger && strictUnstampedLocalConnection(conn.Config()) {
		if _, err := db.InspectTrainingRecipientLocalBootstrapTarget(ctx, conn, conn.Config().Database, roles.MigrationUsername, roles.AppUsername); err != nil {
			return err
		}
		return requireLocalRecipientProvenance(ctx, conn, roles)
	}
	inspected, err := inspectRecipientFromOriginal(ctx, conn, conn.Config().Database, roles, db.RecipientInspectAdmission)
	if err != nil {
		return err
	}
	if inspected.ScheduleState != nil && inspected.ScheduleState.RequiredReader > trainingcompat.ReaderGeneration {
		return errors.New("recipient migration requires newer reader generation")
	}
	if inspected.Phase != db.RecipientFinal || inspected.ScheduleState == nil || inspected.ScheduleState.RequiredReader < 3 {
		if inspected.Publisher.CanLogin || inspected.Publisher.BackendCount != 0 || inspected.Publisher.PreparedCount != 0 || inspected.Publisher.UnexpectedPublisherCount != 0 {
			return errors.New("recipient initial transition requires publisher NOLOGIN and proved drain before mutation")
		}
	}
	if strictUnstampedLocalConnection(conn.Config()) {
		return requireLocalRecipientProvenance(ctx, conn, roles)
	}
	return nil
}

func localRecipientPreparationMode() bool {
	return (os.Getenv("APP_ENV") == "local" || os.Getenv("APP_ENV") == "test") && releasecontract.Version == "" && releasecontract.Candidate == "" && localRecipientRuntimeIdentity() && os.Getenv("TRAINING_SCHEDULE_ADMISSION_FILE") == ""
}
func strictUnstampedLocalConnection(c *pgx.ConnConfig) bool {
	if !localRecipientPreparationMode() {
		return false
	}
	ip := net.ParseIP(c.Host)
	if (c.Host != "localhost" && (ip == nil || !ip.IsLoopback())) || len(c.Fallbacks) != 0 || localRecipientPGEnvironmentOverride() {
		return false
	}
	for k := range c.RuntimeParams {
		if k != "application_name" {
			return false
		}
	}
	// Reject URI options even when pgx would silently consume them before runtime
	// parameters. The explicit URL is independently checked, not trusted to a wrapper.
	raw := os.Getenv("DATABASE_URL")
	if raw != "" {
		if !safeLocalDatabaseURL(raw) {
			return false
		}
	}
	return c.Database != "" && !strings.Contains(c.Host, ",")
}

func localRecipientRuntimeIdentity() bool {
	version, candidate := os.Getenv("APP_VERSION"), os.Getenv("GIT_SHA")
	return version == "" && candidate == "" || version == "dev" && candidate == strings.Repeat("0", 40)
}

func localRecipientPGEnvironmentOverride() bool {
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "PGHOST", "PGHOSTADDR", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGSERVICE", "PGSERVICEFILE", "PGOPTIONS", "PGPASSFILE", "PGSSLMODE", "PGSSLROOTCERT", "PGSSLCERT", "PGSSLKEY", "PGSSLCRL", "PGSSLPASSWORD", "PGTARGETSESSIONATTRS", "PGCONNECT_TIMEOUT", "PGCHANNELBINDING":
			return true
		}
	}
	return false
}
