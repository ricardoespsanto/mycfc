package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigRequiresExplicitNarrowConfiguration(t *testing.T) {
	if _, err := loadConfig(func(string) string { return "" }); err == nil {
		t.Fatal("disabled media cleanup configuration accepted")
	}
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "private.key")
	evidencePath := filepath.Join(dir, "evidence.key")
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	if err := os.WriteFile(privatePath, []byte(encoded), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evidencePath, []byte(encoded), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"MEDIA_CLEANUP_ENABLED": "true", "MEDIA_CLEANUP_DATABASE_URL": "postgres://media@localhost/mycfc",
		"AWS_REGION": "eu-west-1", "S3_BUCKET_NAME": "private-media", "S3_FORCE_PATH_STYLE": "false",
		"MEDIA_CLEANUP_EVIDENCE_KEY_ID": "media-cleanup-v1", "MEDIA_UPLOAD_PRIVATE_KEY_FILE": privatePath,
		"MEDIA_CLEANUP_EVIDENCE_KEY_FILE": evidencePath,
	}
	cfg, err := loadConfig(func(key string) string { return values[key] })
	if err != nil || cfg.databaseURL == "" || len(cfg.privateKey) != 32 || len(cfg.transcriptKey) != 32 {
		t.Fatalf("config=%#v error=%v", cfg, err)
	}
}

func TestRunRejectsEveryNonServeCommand(t *testing.T) {
	if err := run(t.Context(), nil, func(string) string { return "" }, os.Stdout); err == nil {
		t.Fatal("missing command accepted")
	}
	if err := run(t.Context(), []string{"readiness"}, func(string) string { return "" }, os.Stdout); err == nil {
		t.Fatal("retired readiness command accepted")
	}
}

func TestLoadConfigRejectsUnsafeAndOutOfBoundsValues(t *testing.T) {
	dir := t.TempDir()
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	privatePath := filepath.Join(dir, "private.key")
	evidencePath := filepath.Join(dir, "evidence.key")
	for _, path := range []string{privatePath, evidencePath} {
		if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base := map[string]string{
		"MEDIA_CLEANUP_ENABLED": "true", "MEDIA_CLEANUP_DATABASE_URL": "postgres://media@localhost/mycfc",
		"AWS_REGION": "eu-west-1", "S3_BUCKET_NAME": "private-media", "MEDIA_CLEANUP_EVIDENCE_KEY_ID": "cleanup-v1",
		"MEDIA_UPLOAD_PRIVATE_KEY_FILE": privatePath, "MEDIA_CLEANUP_EVIDENCE_KEY_FILE": evidencePath,
	}
	for _, tc := range []struct{ name, key, value string }{
		{"boolean", "S3_FORCE_PATH_STYLE", "yes"},
		{"poll duration", "MEDIA_CLEANUP_POLL_INTERVAL", "100ms"},
		{"lease duration", "MEDIA_CLEANUP_LEASE_DURATION", "16m"},
		{"retry delay", "MEDIA_CLEANUP_RETRY_DELAY", "invalid"},
		{"attempts", "MEDIA_CLEANUP_MAX_ATTEMPTS", "0"},
		{"missing database", "MEDIA_CLEANUP_DATABASE_URL", ""},
		{"missing bucket", "S3_BUCKET_NAME", ""},
		{"unsafe region", "AWS_REGION", "bad region"},
		{"unsafe evidence ID", "MEDIA_CLEANUP_EVIDENCE_KEY_ID", "bad id"},
		{"bad endpoint", "S3_ENDPOINT", "ftp://example.com"},
		{"endpoint credentials", "S3_ENDPOINT", "https://user@example.com"},
		{"missing private key", "MEDIA_UPLOAD_PRIVATE_KEY_FILE", "/not-present/mycfc.key"},
		{"missing evidence key", "MEDIA_CLEANUP_EVIDENCE_KEY_FILE", "/not-present/evidence.key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := make(map[string]string, len(base)+1)
			for key, value := range base {
				values[key] = value
			}
			values[tc.key] = tc.value
			if _, err := loadConfig(func(key string) string { return values[key] }); err == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
	if err := run(t.Context(), []string{"serve"}, func(key string) string { return base[key] }, os.Stdout); err == nil {
		t.Fatal("unreachable database accepted")
	}
	base["MEDIA_CLEANUP_ENABLED"] = "false"
	if err := run(t.Context(), []string{"serve"}, func(key string) string { return base[key] }, os.Stdout); err == nil {
		t.Fatal("disabled worker accepted")
	}
}

func TestKeyAndPrimitiveValidation(t *testing.T) {
	if _, err := readBase64Key("relative.key"); err == nil {
		t.Fatal("relative key path accepted")
	}
	bad := filepath.Join(t.TempDir(), "invalid.key")
	if err := os.WriteFile(bad, []byte("not-base64"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBase64Key(bad); err == nil {
		t.Fatal("invalid key accepted")
	}
	empty := filepath.Join(t.TempDir(), "empty.key")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBase64Key(empty); err == nil {
		t.Fatal("empty key accepted")
	}
	if safeID("") || safeID(strings.Repeat("a", 121)) || safeID("bad id") || !safeID("eu-west-1/cleanup:v1") {
		t.Fatal("identifier validation failed")
	}
	if _, err := boundedDuration("bad", time.Second, time.Second, time.Minute); err == nil {
		t.Fatal("invalid duration accepted")
	}
	if value, err := boundedInt32("7", 5, 1, 100); err != nil || value != 7 {
		t.Fatalf("bounded integer=%d error=%v", value, err)
	}
	if _, err := boundedInt32("101", 5, 1, 100); err == nil {
		t.Fatal("out-of-bounds integer accepted")
	}
	if value, err := exactBool("true"); err != nil || !value {
		t.Fatalf("boolean=%t error=%v", value, err)
	}
	if value := valueOr("", "fallback"); value != "fallback" {
		t.Fatalf("fallback=%q", value)
	}
}
