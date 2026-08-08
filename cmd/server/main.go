package main

import (
	"context"
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
)

func main() {
	ctx := context.Background()
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		if err := runDatabaseCommand(ctx, os.Args[1]); err != nil {
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

func runDatabaseCommand(ctx context.Context, command string) error {
	if databaseURL, ok, err := databaseURLFromEnvironment(); err != nil {
		return err
	} else if ok {
		conn, err := pgx.Connect(ctx, databaseURL)
		if err != nil {
			return fmt.Errorf("connect to database: %w", err)
		}
		defer conn.Close(ctx)
		switch command {
		case "bootstrap-db":
			return db.BootstrapRoles(ctx, conn, os.Getenv("DB_NAME"), db.RoleCredentials{
				AppUsername:       os.Getenv("APP_DB_USER"),
				AppPassword:       os.Getenv("APP_DB_PASSWORD"),
				MigrationUsername: os.Getenv("MIGRATION_DB_USER"),
				MigrationPassword: os.Getenv("MIGRATION_DB_PASSWORD"),
			})
		case "migrate":
			return db.ApplyBaseline(ctx, conn)
		default:
			return fmt.Errorf("unknown command %q", command)
		}
	}

	cfg, err := config.Load(ctx)
	if err != nil {
		return err
	}
	var databaseURL string
	switch command {
	case "bootstrap-db":
		databaseURL, err = cfg.BootstrapDatabaseURL()
	case "migrate":
		databaseURL, err = cfg.MigrationDatabaseURL()
	default:
		return fmt.Errorf("unknown command %q", command)
	}
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer conn.Close(ctx)

	if command == "bootstrap-db" {
		return db.BootstrapRoles(ctx, conn, cfg.DBName, db.RoleCredentials{
			AppUsername:       cfg.DBUser,
			AppPassword:       cfg.DBPassword.Value(),
			MigrationUsername: cfg.MigrationDBUser,
			MigrationPassword: cfg.MigrationDBPassword.Value(),
		})
	}
	return db.ApplyBaseline(ctx, conn)
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
