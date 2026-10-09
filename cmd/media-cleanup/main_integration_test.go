//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestServeUsesNarrowCleanupRole(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	admin, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(t.Context())
	// Earlier migration tests replace these routines; restore the fixed test grant.
	if _, err = admin.Exec(t.Context(), `GRANT EXECUTE ON FUNCTION media_upload_cleanup_claim(bigint,uuid),
		media_upload_cleanup_complete(uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea),
		media_upload_cleanup_fail(uuid,uuid,bigint,uuid,boolean,bigint) TO mycfc_media_cleanup`); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	privatePath := filepath.Join(dir, "private.key")
	evidencePath := filepath.Join(dir, "evidence.key")
	for _, path := range []string{privatePath, evidencePath} {
		if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	values := map[string]string{
		"MEDIA_CLEANUP_ENABLED": "true", "MEDIA_CLEANUP_DATABASE_URL": dsn,
		"AWS_REGION": "eu-west-1", "S3_BUCKET_NAME": "private-media",
		"MEDIA_CLEANUP_EVIDENCE_KEY_ID": "cleanup-test", "MEDIA_UPLOAD_PRIVATE_KEY_FILE": privatePath,
		"MEDIA_CLEANUP_EVIDENCE_KEY_FILE": evidencePath,
		"MEDIA_CLEANUP_POLL_INTERVAL":     "1s",
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	var output bytes.Buffer
	if err = run(ctx, []string{"serve"}, func(key string) string { return values[key] }, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "event=media_cleanup_started") || !strings.Contains(output.String(), "event=media_cleanup_stopped") {
		t.Fatalf("worker lifecycle events missing: %s", output.String())
	}
}
