package main

import (
	"bytes"
	"strings"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

func TestLoadConfigFailsClosedAndBoundsBatch(t *testing.T) {
	worker := uuid.New().String()
	tests := []struct {
		name string
		env  map[string]string
		ok   bool
	}{
		{name: "disabled", env: map[string]string{}},
		{name: "wrong gate case", env: map[string]string{"PRIVACY_RETENTION_ENABLED": "TRUE"}},
		{name: "missing database", env: map[string]string{"PRIVACY_RETENTION_ENABLED": "true", "PRIVACY_RETENTION_WORKER_REF": worker}},
		{name: "nil worker", env: map[string]string{"PRIVACY_RETENTION_ENABLED": "true", "PRIVACY_RETENTION_DATABASE_URL": "postgres://private", "PRIVACY_RETENTION_WORKER_REF": uuid.Nil.String()}},
		{name: "zero batch", env: map[string]string{"PRIVACY_RETENTION_ENABLED": "true", "PRIVACY_RETENTION_DATABASE_URL": "postgres://private", "PRIVACY_RETENTION_WORKER_REF": worker, "PRIVACY_RETENTION_BATCH_LIMIT": "0"}},
		{name: "oversized batch", env: map[string]string{"PRIVACY_RETENTION_ENABLED": "true", "PRIVACY_RETENTION_DATABASE_URL": "postgres://private", "PRIVACY_RETENTION_WORKER_REF": worker, "PRIVACY_RETENTION_BATCH_LIMIT": "10001"}},
		{name: "valid defaults", env: map[string]string{"PRIVACY_RETENTION_ENABLED": "true", "PRIVACY_RETENTION_DATABASE_URL": "postgres://private", "PRIVACY_RETENTION_WORKER_REF": worker}, ok: true},
		{name: "valid maximum", env: map[string]string{"PRIVACY_RETENTION_ENABLED": "true", "PRIVACY_RETENTION_DATABASE_URL": "postgres://private", "PRIVACY_RETENTION_WORKER_REF": worker, "PRIVACY_RETENTION_BATCH_LIMIT": "10000"}, ok: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := loadConfig(func(key string) string { return test.env[key] })
			if (err == nil) != test.ok {
				t.Fatalf("loadConfig() error = %v", err)
			}
			if test.ok && (cfg.batchLimit < 1 || cfg.batchLimit > maximumBatchLimit) {
				t.Fatalf("batchLimit = %d", cfg.batchLimit)
			}
		})
	}
}

func TestWriteEvidenceIsAggregateOnlyAndFailsOnSLABreach(t *testing.T) {
	secret := uuid.New().String()
	var output bytes.Buffer
	err := writeEvidence(&output, dbgen.RunPrivacyRetentionRow{RunID: uuid.MustParse(secret), SessionsDeleted: 2}, dbgen.GetPrivacyRetentionStatusRow{RepairOverdueCount: 1})
	if err == nil || !strings.Contains(output.String(), "privacy_retention_succeeded sessions_deleted=2") ||
		!strings.Contains(output.String(), "privacy_retention_sla_breach") || strings.Contains(output.String(), secret) {
		t.Fatalf("output=%q error=%v", output.String(), err)
	}
}

func TestWriteEvidenceFailsOnOldBacklog(t *testing.T) {
	var output bytes.Buffer
	err := writeEvidence(&output, dbgen.RunPrivacyRetentionRow{}, dbgen.GetPrivacyRetentionStatusRow{DueCount: 1, OldestDueAgeSeconds: maximumBacklogAge + 1})
	if err == nil || !strings.Contains(output.String(), "privacy_retention_backlog_breach") {
		t.Fatalf("output=%q error=%v", output.String(), err)
	}
}
