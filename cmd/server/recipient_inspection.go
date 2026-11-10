package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/jackc/pgx/v5"
)

type recipientCommandTarget struct {
	Connection                                                         *pgx.ConnConfig
	Roles                                                              db.RoleCredentials
	Database, BootstrapRole, Version, Candidate, AppEnv, AdmissionFile string
}

func loadRecipientCommandTarget(ctx context.Context, identity string) (recipientCommandTarget, error) {
	var target recipientCommandTarget
	roles := databaseRoleCredentialsFromEnvironment()
	raw, explicit, err := databaseURLFromEnvironment()
	if err != nil {
		return target, errors.New("recipient database target rejected")
	}
	target.BootstrapRole = os.Getenv("POSTGRES_USER")
	target.Version = os.Getenv("APP_VERSION")
	target.Candidate = os.Getenv("GIT_SHA")
	target.AppEnv = os.Getenv("APP_ENV")
	target.AdmissionFile = os.Getenv("TRAINING_SCHEDULE_ADMISSION_FILE")
	if !explicit || !databaseRoleIdentifiersComplete(roles) || identity == "bootstrap" && target.BootstrapRole == "" {
		cfg, err := loadDatabaseCommandConfig(ctx)
		if err != nil {
			return target, errors.New("recipient database configuration unavailable")
		}
		roles = databaseRoleCredentialsFromConfig(cfg)
		target.BootstrapRole = cfg.PostgresUser
		target.Version = cfg.AppVersion
		target.Candidate = cfg.GITSHA
		target.AppEnv = cfg.AppEnv
		target.AdmissionFile = cfg.TrainingScheduleAdmissionFile
		if !explicit {
			if identity == "bootstrap" {
				raw, err = cfg.BootstrapDatabaseURL()
			} else {
				raw, err = cfg.MigrationDatabaseURL()
			}
			if err != nil {
				return target, errors.New("recipient database endpoint unavailable")
			}
		}
	}
	if !databaseRoleIdentifiersComplete(roles) {
		return target, errors.New("recipient configured role identities required")
	}
	roles.AppUsername = releasecontract.AppRole(roles.AppUsername)
	config, err := pgx.ParseConfig(raw)
	if err != nil || config.Database == "" {
		return target, errors.New("recipient database endpoint rejected")
	}
	if identity == "migration" && config.User != roles.MigrationUsername {
		return target, errors.New("recipient command requires configured migration authentication")
	}
	if identity == "bootstrap" && (target.BootstrapRole == "" || config.User != target.BootstrapRole) {
		return target, errors.New("recipient command requires configured bootstrap authentication")
	}
	if localRecipientPreparationMode() {
		if !strictUnstampedLocalConnection(config) {
			return target, errors.New("local database target rejected before authentication")
		}
		installLocalRecipientDialGuard(config)
	}
	target.Connection, target.Database, target.Roles = config, config.Database, roles
	return target, nil
}

func requireRecipientCompiledTarget(target recipientCommandTarget) error {
	if !releasecontract.Matches(target.Version, target.Candidate, target.Database) {
		return errors.New("recipient command compiled/runtime identity mismatch")
	}
	return nil
}

type recipientScheduleStatus struct {
	Contract             string  `json:"contract"`
	DatabaseIncarnation  string  `json:"database_incarnation"`
	SchemaContract       string  `json:"schema_contract"`
	StateEpoch           int64   `json:"state_epoch"`
	RequiredReader       int32   `json:"required_reader"`
	FloorCommitID        *string `json:"floor_commit_id"`
	FloorManifestSHA256  *string `json:"floor_manifest_sha256"`
	PartialV2Enabled     bool    `json:"partial_v2_enabled"`
	EverPartialV2Enabled bool    `json:"ever_partial_v2_enabled"`
}

func recipientStatusObject(state db.TrainingScheduleState) recipientScheduleStatus {
	return recipientScheduleStatus{"mycfc/training-schedule-status/v1", state.DatabaseIncarnation.String(), state.SchemaContract, state.StateEpoch, state.RequiredReader, trainingFloorID(state), state.FloorManifestSHA256, state.PartialV2Enabled, state.EverPartialV2Enabled}
}

func inspectRecipientConnection(ctx context.Context, conn *pgx.Conn, database string, roles db.RoleCredentials, mode db.RecipientInspectionMode) (db.RecipientDatabaseInspection, error) {
	result, err := db.InspectTrainingRecipientDatabase(ctx, conn, db.RecipientDatabaseInspectionInput{Database: database, MigrationRole: roles.MigrationUsername, PublisherRole: roles.AppUsername, Mode: mode, InspectionTimeout: recipientCLIInspectionTimeout, CleanupTimeout: recipientCLICleanupTimeout})
	if err != nil {
		return db.RecipientDatabaseInspection{}, err
	}
	if conn.IsClosed() || conn.PgConn().IsBusy() || conn.PgConn().TxStatus() != 'I' || result.InspectionRole != roles.MigrationUsername || result.Publisher.UnexpectedPublisherCount != 0 {
		return db.RecipientDatabaseInspection{}, errors.New("recipient inspection did not complete cleanly")
	}
	return result, nil
}

// Bootstrap never evaluates migration-owned tables/functions. The separate
// authenticated low handle clones the actual endpoint/TLS/database and is
// attested against original fixed catalogs before running phase inspection.
func inspectRecipientFromOriginal(ctx context.Context, original databaseCommandConnection, database string, roles db.RoleCredentials, mode db.RecipientInspectionMode) (db.RecipientDatabaseInspection, error) {
	raw, ok := original.(*pgx.Conn)
	if !ok {
		return db.RecipientDatabaseInspection{}, errors.New("recipient inspection requires original physical session")
	}
	configured := raw.Config().Copy()
	if configured.Database != database || strings.TrimSpace(roles.MigrationUsername) == "" {
		return db.RecipientDatabaseInspection{}, errors.New("recipient inspection target identity rejected")
	}
	roles.AppUsername = releasecontract.AppRole(roles.AppUsername)
	if configured.User == roles.MigrationUsername {
		return inspectRecipientConnection(ctx, raw, database, roles, mode)
	}
	if roles.MigrationPassword == "" {
		return db.RecipientDatabaseInspection{}, errors.New("recipient independent migration credentials required")
	}
	configured.User, configured.Password = roles.MigrationUsername, roles.MigrationPassword
	low, err := pgx.ConnectConfig(ctx, configured)
	if err != nil {
		return db.RecipientDatabaseInspection{}, errors.New("recipient independent migration connection rejected")
	}
	defer low.Close(ctx)
	result, inspectionErr := func() (db.RecipientDatabaseInspection, error) {
		if err := verifyRecipientInspectorBackend(ctx, raw, low, database, roles.MigrationUsername); err != nil {
			return db.RecipientDatabaseInspection{}, err
		}
		return inspectRecipientConnection(ctx, low, database, roles, mode)
	}()
	closeErr := low.Close(ctx)
	if inspectionErr != nil {
		return db.RecipientDatabaseInspection{}, inspectionErr
	}
	if closeErr != nil {
		return db.RecipientDatabaseInspection{}, errors.New("recipient independent inspector cleanup failed")
	}
	return result, nil
}
func verifyRecipientInspectorBackend(ctx context.Context, original, low *pgx.Conn, database, migrationRole string) error {
	var pid uint32
	var started time.Time
	var databaseOID uint32
	var authenticated, current string
	if err := low.QueryRow(ctx, `SELECT activity.pid,activity.backend_start,activity.datid,session_user::pg_catalog.text,current_user::pg_catalog.text FROM pg_catalog.pg_stat_activity activity WHERE activity.pid OPERATOR(pg_catalog.=) pg_catalog.pg_backend_pid()`).Scan(&pid, &started, &databaseOID, &authenticated, &current); err != nil || authenticated != migrationRole || current != migrationRole {
		return errors.New("recipient migration backend identity rejected")
	}
	var same bool
	if err := original.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity activity JOIN pg_catalog.pg_database database ON database.oid OPERATOR(pg_catalog.=) activity.datid JOIN pg_catalog.pg_roles role ON role.oid OPERATOR(pg_catalog.=) activity.usesysid WHERE activity.pid OPERATOR(pg_catalog.=) $1::pg_catalog.int4 AND activity.backend_start OPERATOR(pg_catalog.=) $2::pg_catalog.timestamptz AND activity.datid OPERATOR(pg_catalog.=) $3::pg_catalog.oid AND database.datname OPERATOR(pg_catalog.=) $4::pg_catalog.name AND database.datname OPERATOR(pg_catalog.=) pg_catalog.current_database() AND role.rolname OPERATOR(pg_catalog.=) $5::pg_catalog.name AND NOT role.rolsuper)`, pid, started, databaseOID, database, migrationRole).Scan(&same); err != nil || !same {
		return errors.New("recipient inspector backend does not match original target")
	}
	return nil
}
