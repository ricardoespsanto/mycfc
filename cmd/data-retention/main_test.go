package main

import (
	"os"
	"testing"
)

func TestRunRequiresExplicitManualReviewAndEnablement(t *testing.T) {
	if err := run(t.Context(), nil, func(string) string { return "" }, os.Stdout); err == nil {
		t.Fatal("missing manual confirmation accepted")
	}
	if err := run(t.Context(), []string{"run", "--confirmed-manual-review"}, func(string) string { return "" }, os.Stdout); err == nil {
		t.Fatal("disabled manual retention accepted")
	}
}

func TestRunRejectsMissingMalformedAndUnavailableDatabase(t *testing.T) {
	values := map[string]string{"DATA_RETENTION_ENABLED": "true"}
	getenv := func(key string) string { return values[key] }
	args := []string{"run", "--confirmed-manual-review"}
	if err := run(t.Context(), args, getenv, os.Stdout); err == nil {
		t.Fatal("missing database URL accepted")
	}
	values["DATA_RETENTION_DATABASE_URL"] = "://bad-url"
	if err := run(t.Context(), args, getenv, os.Stdout); err == nil {
		t.Fatal("malformed database URL accepted")
	}
	values["DATA_RETENTION_DATABASE_URL"] = "postgres://retention@127.0.0.1:1/mycfc?connect_timeout=1"
	if err := run(t.Context(), args, getenv, os.Stdout); err == nil {
		t.Fatal("unavailable database accepted")
	}
}
