package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
