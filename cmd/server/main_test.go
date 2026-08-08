package main

import (
	"net/url"
	"os"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/config"
)

func TestConfigDatabaseURLEscapesCredentials(t *testing.T) {
	cfg := config.Config{
		DBHost:     "database.example.internal",
		DBPort:     5432,
		DBName:     "mycfc",
		DBUser:     "master",
		DBPassword: config.Secret("password:/?#"),
		DBSSLMode:  "disable",
	}

	raw, err := cfg.ResolvedDatabaseURL()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := parsed.User.Password()
	if parsed.User.Username() != "master" || password != "password:/?#" || parsed.Query().Get("sslmode") != "disable" {
		t.Fatalf("database URL = %q", raw)
	}
}

func TestConfigDatabaseURLAcceptsDatabaseURL(t *testing.T) {
	cfg := config.Config{DatabaseURL: config.Secret("postgres://user:password@localhost:5432/mycfc?sslmode=disable")}

	raw, err := cfg.ResolvedDatabaseURL()
	if err != nil {
		t.Fatal(err)
	}
	if raw != "postgres://user:password@localhost:5432/mycfc?sslmode=disable" {
		t.Fatalf("database URL = %q", raw)
	}
}

func TestDatabaseURLFromEnvironmentEscapesComponents(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "postgres")
	t.Setenv("DB_PORT", "5432")
	t.Setenv("DB_NAME", "mycfc")
	t.Setenv("DB_USER", "mycfc_app")
	t.Setenv("DB_PASSWORD", "password:/?#")
	t.Setenv("DB_SSLMODE", "disable")

	raw, ok, err := databaseURLFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("databaseURLFromEnvironment() ok = false")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := parsed.User.Password()
	if parsed.User.Username() != "mycfc_app" || password != "password:/?#" || parsed.Query().Get("sslmode") != "disable" {
		t.Fatalf("database URL = %q", raw)
	}
}

func TestDatabaseURLFromEnvironmentUsesRawURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://raw:secret@localhost:5432/mycfc?sslmode=disable")
	t.Setenv("DB_HOST", "")

	raw, ok, err := databaseURLFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || raw != os.Getenv("DATABASE_URL") {
		t.Fatalf("databaseURLFromEnvironment() = %q, %t", raw, ok)
	}
}

func TestDatabaseURLFromEnvironmentMissingComponents(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "postgres")
	t.Setenv("DB_PORT", "")

	raw, ok, err := databaseURLFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if ok || raw != "" {
		t.Fatalf("databaseURLFromEnvironment() = %q, %t", raw, ok)
	}
}
