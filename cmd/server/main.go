package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	_ "time/tzdata"

	"github.com/cfcoimbra/mycfc/internal/app"
	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type databaseCommandConnection interface {
	Close(context.Context) error
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Begin(context.Context) (pgx.Tx, error)
}

var (
	connectDatabaseCommand = func(ctx context.Context, databaseURL string) (databaseCommandConnection, error) {
		return pgx.Connect(ctx, databaseURL)
	}
	loadDatabaseCommandConfig = config.Load
	executePrivacyCommand     = runPrivacyCommand
)

const legacyProductionAppDatabaseRole = "mycfc_app"

func main() {
	ctx := context.Background()
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		if err := runServerCommand(ctx, os.Args[1:]); err != nil {
			slog.Error("database command failed", "error", err)
			os.Exit(1)
		}
		return
	}
	application, err := app.New(ctx)
	if err != nil {
		slog.Error("application startup failed", "error", err)
		os.Exit(1)
	}
	defer application.Close()

	if err := application.Run(ctx); err != nil {
		application.Logger.Error("application stopped with error", "error", err)
		os.Exit(1)
	}
}

func runServerCommand(ctx context.Context, args []string) error {
	if args[0] == "privacy" {
		if err := executePrivacyCommand(ctx, args[1:]); err != nil {
			return errors.New("privacy operator command failed")
		}
		return nil
	}
	return runDatabaseCommand(ctx, args[0])
}

func runDatabaseCommand(ctx context.Context, command string) error {
	if command != "bootstrap-db" && command != "migrate" && command != "harden-db" && command != "bind-guardian-release" && command != "provision-privacy-activation-disable" && command != "provision-guardian-activation" && command != "provision-guardian-release-bind" {
		return fmt.Errorf("unknown command %q", command)
	}
	if command == "bind-guardian-release" {
		return bindGuardianRelease(ctx)
	}
	if databaseURL, ok, err := databaseURLFromEnvironment(); err != nil {
		return err
	} else if ok {
		connectionConfig, err := pgx.ParseConfig(databaseURL)
		if err != nil {
			return fmt.Errorf("parse database URL: %w", err)
		}
		conn, err := connectDatabaseCommand(ctx, databaseURL)
		if err != nil {
			return fmt.Errorf("connect to database: %w", err)
		}
		defer conn.Close(ctx)
		databaseName := connectionConfig.Database
		if command == "provision-privacy-activation-disable" {
			return provisionPrivacyActivationDisable(ctx, conn, databaseName)
		}
		if command == "provision-guardian-activation" {
			return provisionGuardianActivation(ctx, conn, databaseName)
		}
		if command == "provision-guardian-release-bind" {
			return provisionGuardianReleaseBind(ctx, conn, databaseName)
		}
		credentials := databaseRoleCredentialsFromEnvironment()
		configSource := "environment"
		if command == "migrate" && !databaseRoleIdentifiersComplete(credentials) {
			if strings.TrimSpace(credentials.AppUsername) == "" {
				credentials.AppUsername = legacyProductionAppDatabaseRole
			}
			if strings.TrimSpace(credentials.MigrationUsername) == "" {
				credentials.MigrationUsername = connectionConfig.User
			}
			configSource = "environment_connection+legacy_role_contract"
		} else if os.Getenv("APP_ENV") == "production" && !databaseRoleCredentialsComplete(credentials) {
			cfg, loadErr := loadDatabaseCommandConfig(ctx)
			if loadErr != nil {
				return fmt.Errorf("load production database role configuration: %w", loadErr)
			}
			credentials = databaseRoleCredentialsFromConfig(cfg)
			configSource = "environment_connection+aws_remote_roles"
		}
		logDatabaseCommandConfiguration(command, configSource, connectionConfig.Host, databaseName, connectionConfig.User, credentials)
		switch command {
		case "bootstrap-db":
			return db.BootstrapRoles(ctx, conn, databaseName, credentials)
		case "migrate":
			return db.ApplyBaselineAndHarden(ctx, conn, databaseName, credentials)
		case "harden-db":
			return db.HardenPrivacyExecutionRoles(ctx, conn, databaseName, credentials)
		}
	}

	cfg, err := loadDatabaseCommandConfig(ctx)
	if err != nil {
		return err
	}
	var databaseURL string
	switch command {
	case "bootstrap-db", "harden-db", "provision-privacy-activation-disable", "provision-guardian-activation", "provision-guardian-release-bind":
		databaseURL, err = cfg.BootstrapDatabaseURL()
	case "migrate":
		databaseURL, err = cfg.MigrationDatabaseURL()
	}
	if err != nil {
		return err
	}
	conn, err := connectDatabaseCommand(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer conn.Close(ctx)
	if command == "provision-privacy-activation-disable" {
		return provisionPrivacyActivationDisable(ctx, conn, cfg.DBName)
	}
	if command == "provision-guardian-activation" {
		return provisionGuardianActivation(ctx, conn, cfg.DBName)
	}
	if command == "provision-guardian-release-bind" {
		return provisionGuardianReleaseBind(ctx, conn, cfg.DBName)
	}

	credentials := databaseRoleCredentialsFromConfig(cfg)
	connectionRole := cfg.MigrationDBUser
	if command == "bootstrap-db" || command == "harden-db" {
		connectionRole = cfg.PostgresUser
	}
	logDatabaseCommandConfiguration(command, "aws_remote", cfg.DBHost, cfg.DBName, connectionRole, credentials)
	if command == "bootstrap-db" {
		return db.BootstrapRoles(ctx, conn, cfg.DBName, credentials)
	}
	if command == "harden-db" {
		return db.HardenPrivacyExecutionRoles(ctx, conn, cfg.DBName, credentials)
	}
	return db.ApplyBaselineAndHarden(ctx, conn, cfg.DBName, credentials)
}

func provisionPrivacyActivationDisable(ctx context.Context, conn databaseCommandConnection, databaseName string) error {
	disableURL := strings.TrimSpace(os.Getenv("PRIVACY_ACTIVATION_DISABLE_DATABASE_URL"))
	config, err := pgx.ParseConfig(disableURL)
	if err != nil || config.Database != databaseName || config.User != "mycfc_privacy_activation_disable" || strings.TrimSpace(config.Password) == "" {
		return errors.New("privacy activation disable provisioning credential rejected")
	}
	logDatabaseCommandConfiguration("provision-privacy-activation-disable", "dedicated_break_glass_file", config.Host, databaseName, "bootstrap administrator", db.RoleCredentials{})
	return db.ProvisionPrivacyActivationDisableRole(ctx, conn, databaseName, config.User, config.Password)
}

func provisionGuardianActivation(ctx context.Context, conn databaseCommandConnection, databaseName string) error {
	operatorURL := strings.TrimSpace(os.Getenv("GUARDIAN_ACTIVATION_DATABASE_URL"))
	config, err := pgx.ParseConfig(operatorURL)
	if err != nil || config.Database != databaseName || config.User != "mycfc_guardian_activation_operator" || strings.TrimSpace(config.Password) == "" {
		return errors.New("guardian activation provisioning credential rejected")
	}
	logDatabaseCommandConfiguration("provision-guardian-activation", "dedicated_root_file", config.Host, databaseName, "bootstrap administrator", db.RoleCredentials{})
	return db.ProvisionGuardianActivationRole(ctx, conn, databaseName, config.User, config.Password)
}

func provisionGuardianReleaseBind(ctx context.Context, conn databaseCommandConnection, databaseName string) error {
	releaseURL := strings.TrimSpace(os.Getenv("GUARDIAN_RELEASE_BIND_DATABASE_URL"))
	config, err := pgx.ParseConfig(releaseURL)
	if err != nil || config.Database != databaseName || config.User != "mycfc_guardian_release_bind" || strings.TrimSpace(config.Password) == "" {
		return errors.New("guardian release bind provisioning credential rejected")
	}
	logDatabaseCommandConfiguration("provision-guardian-release-bind", "dedicated_root_file", config.Host, databaseName, "bootstrap administrator", db.RoleCredentials{})
	if err := db.ProvisionGuardianReleaseBindRole(ctx, conn, databaseName, config.User, config.Password); err != nil {
		return err
	}
	slog.Info("guardian release bind role provisioned", "event", "guardian_release_bind_role_provisioned")
	return nil
}

func bindGuardianRelease(ctx context.Context) error {
	releaseURL := strings.TrimSpace(os.Getenv("GUARDIAN_RELEASE_BIND_DATABASE_URL"))
	expectedDatabase := strings.TrimSpace(os.Getenv("GUARDIAN_RELEASE_BIND_EXPECTED_DATABASE"))
	config, err := pgx.ParseConfig(releaseURL)
	if err != nil || config.Database != expectedDatabase || config.User != "mycfc_guardian_release_bind" || strings.TrimSpace(config.Password) == "" {
		return errors.New("guardian release bind credential rejected")
	}
	conn, err := connectDatabaseCommand(ctx, releaseURL)
	if err != nil {
		return errors.New("connect guardian release bind database")
	}
	defer conn.Close(ctx)
	logDatabaseCommandConfiguration("bind-guardian-release", "dedicated_release_bind_file", config.Host, expectedDatabase, config.User, db.RoleCredentials{})
	return db.BindGuardianRuntimeRelease(ctx, conn, expectedDatabase, strings.TrimSpace(os.Getenv("GUARDIAN_RUNTIME_IMAGE_DIGEST")))
}

func databaseRoleCredentialsFromConfig(cfg config.Config) db.RoleCredentials {
	return db.RoleCredentials{
		AppUsername:                     cfg.DBUser,
		AppPassword:                     cfg.DBPassword.Value(),
		MigrationUsername:               cfg.MigrationDBUser,
		MigrationPassword:               cfg.MigrationDBPassword.Value(),
		PrivacyExecutorUsername:         os.Getenv("PRIVACY_EXECUTOR_DB_USER"),
		PrivacyExecutorPassword:         os.Getenv("PRIVACY_EXECUTOR_DB_PASSWORD"),
		PrivacyActivationBrokerUsername: os.Getenv("PRIVACY_ACTIVATION_BROKER_DB_USER"),
		PrivacyActivationBrokerPassword: os.Getenv("PRIVACY_ACTIVATION_BROKER_DB_PASSWORD"),
		PrivacyRestoreObserverUsername:  os.Getenv("PRIVACY_RESTORE_OBSERVER_DB_USER"),
		PrivacyRestoreObserverPassword:  os.Getenv("PRIVACY_RESTORE_OBSERVER_DB_PASSWORD"),
	}
}

func databaseRoleCredentialsComplete(credentials db.RoleCredentials) bool {
	return strings.TrimSpace(credentials.AppUsername) != "" &&
		strings.TrimSpace(credentials.AppPassword) != "" &&
		strings.TrimSpace(credentials.MigrationUsername) != "" &&
		strings.TrimSpace(credentials.MigrationPassword) != ""
}

func databaseRoleIdentifiersComplete(credentials db.RoleCredentials) bool {
	return strings.TrimSpace(credentials.AppUsername) != "" &&
		strings.TrimSpace(credentials.MigrationUsername) != ""
}

func logDatabaseCommandConfiguration(command, source, host, databaseName, connectionRole string, credentials db.RoleCredentials) {
	slog.Info("database command configured",
		"command", command,
		"config_source", source,
		"database_host", host,
		"database_name", databaseName,
		"connection_role", connectionRole,
		"app_role", credentials.AppUsername,
		"migration_role", credentials.MigrationUsername,
		"privacy_executor_configured", credentials.PrivacyExecutorUsername != "",
		"privacy_activation_broker_configured", credentials.PrivacyActivationBrokerUsername != "",
		"privacy_restore_observer_configured", credentials.PrivacyRestoreObserverUsername != "",
	)
}

func databaseRoleCredentialsFromEnvironment() db.RoleCredentials {
	return db.RoleCredentials{
		AppUsername:                     os.Getenv("APP_DB_USER"),
		AppPassword:                     os.Getenv("APP_DB_PASSWORD"),
		MigrationUsername:               os.Getenv("MIGRATION_DB_USER"),
		MigrationPassword:               os.Getenv("MIGRATION_DB_PASSWORD"),
		PrivacyExecutorUsername:         os.Getenv("PRIVACY_EXECUTOR_DB_USER"),
		PrivacyExecutorPassword:         os.Getenv("PRIVACY_EXECUTOR_DB_PASSWORD"),
		PrivacyActivationBrokerUsername: os.Getenv("PRIVACY_ACTIVATION_BROKER_DB_USER"),
		PrivacyActivationBrokerPassword: os.Getenv("PRIVACY_ACTIVATION_BROKER_DB_PASSWORD"),
		PrivacyRestoreObserverUsername:  os.Getenv("PRIVACY_RESTORE_OBSERVER_DB_USER"),
		PrivacyRestoreObserverPassword:  os.Getenv("PRIVACY_RESTORE_OBSERVER_DB_PASSWORD"),
	}
}

func databaseURLFromEnvironment() (string, bool, error) {
	if raw := os.Getenv("DATABASE_URL"); raw != "" {
		return raw, true, nil
	}
	for _, name := range []string{"DB_HOST", "DB_PORT", "DB_NAME", "DB_USER", "DB_PASSWORD"} {
		if os.Getenv(name) == "" {
			return "", false, nil
		}
	}
	u := &url.URL{Scheme: "postgres", User: url.UserPassword(os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD")), Host: net.JoinHostPort(os.Getenv("DB_HOST"), os.Getenv("DB_PORT")), Path: os.Getenv("DB_NAME")}
	query := u.Query()
	sslmode := os.Getenv("DB_SSLMODE")
	if sslmode == "" {
		sslmode = "require"
	}
	query.Set("sslmode", sslmode)
	u.RawQuery = query.Encode()
	return u.String(), true, nil
}
