package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
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
	if command != "bootstrap-db" && command != "migrate" && command != "harden-db" {
		return fmt.Errorf("unknown command %q", command)
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
		switch command {
		case "bootstrap-db":
			return db.BootstrapRoles(ctx, conn, databaseName, databaseRoleCredentialsFromEnvironment())
		case "migrate":
			return db.ApplyBaselineAndHarden(ctx, conn, databaseName, databaseRoleCredentialsFromEnvironment())
		case "harden-db":
			return db.HardenPrivacyExecutionRoles(ctx, conn, databaseName, databaseRoleCredentialsFromEnvironment())
		}
	}

	cfg, err := loadDatabaseCommandConfig(ctx)
	if err != nil {
		return err
	}
	var databaseURL string
	switch command {
	case "bootstrap-db", "harden-db":
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

	credentials := db.RoleCredentials{
		AppUsername:       cfg.DBUser,
		AppPassword:       cfg.DBPassword.Value(),
		MigrationUsername: cfg.MigrationDBUser,
		MigrationPassword: cfg.MigrationDBPassword.Value(),
	}
	if command == "bootstrap-db" {
		return db.BootstrapRoles(ctx, conn, cfg.DBName, credentials)
	}
	if command == "harden-db" {
		return db.HardenPrivacyExecutionRoles(ctx, conn, cfg.DBName, credentials)
	}
	return db.ApplyBaselineAndHarden(ctx, conn, cfg.DBName, credentials)
}

func databaseRoleCredentialsFromEnvironment() db.RoleCredentials {
	return db.RoleCredentials{
		AppUsername:             os.Getenv("APP_DB_USER"),
		AppPassword:             os.Getenv("APP_DB_PASSWORD"),
		MigrationUsername:       os.Getenv("MIGRATION_DB_USER"),
		MigrationPassword:       os.Getenv("MIGRATION_DB_PASSWORD"),
		PrivacyExecutorUsername: os.Getenv("PRIVACY_EXECUTOR_DB_USER"),
		PrivacyExecutorPassword: os.Getenv("PRIVACY_EXECUTOR_DB_PASSWORD"),
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
