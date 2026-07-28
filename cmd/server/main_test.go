package main

import (
	"net/url"
	"testing"
)

func TestDatabaseURLFromEnvironmentEscapesCredentials(t *testing.T) {
	t.Setenv("DB_HOST", "database.example.internal")
	t.Setenv("DB_PORT", "5432")
	t.Setenv("DB_NAME", "mycfc")
	t.Setenv("DB_USER", "master")
	t.Setenv("DB_PASSWORD", "password:/?#")
	t.Setenv("DATABASE_URL", "")

	raw, err := databaseURLFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := parsed.User.Password()
	if parsed.User.Username() != "master" || password != "password:/?#" || parsed.Query().Get("sslmode") != "require" {
		t.Fatalf("database URL = %q", raw)
	}
}
