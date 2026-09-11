package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	retentionRole     = "mycfc_privacy_retention"
	defaultBatchLimit = 500
	maximumBatchLimit = 10000
	maximumBacklogAge = int64(24 * 60 * 60)
	maximumLastRunAge = int64(2 * 60 * 60)
)

type config struct {
	databaseURL string
	workerRef   uuid.UUID
	batchLimit  int32
}

func main() {
	if err := run(context.Background(), os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "privacy_retention_failed")
		os.Exit(1)
	}
}

func loadConfig(getenv func(string) string) (config, error) {
	if getenv("PRIVACY_RETENTION_ENABLED") != "true" {
		return config{}, errors.New("retention maintenance is disabled")
	}
	databaseURL := strings.TrimSpace(getenv("PRIVACY_RETENTION_DATABASE_URL"))
	workerRef, err := uuid.Parse(strings.TrimSpace(getenv("PRIVACY_RETENTION_WORKER_REF")))
	if databaseURL == "" || err != nil || workerRef == uuid.Nil {
		return config{}, errors.New("retention maintenance configuration is incomplete")
	}
	limit := defaultBatchLimit
	if raw := strings.TrimSpace(getenv("PRIVACY_RETENTION_BATCH_LIMIT")); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 1 || parsed > maximumBatchLimit {
			return config{}, errors.New("retention batch limit is invalid")
		}
		limit = parsed
	}
	return config{databaseURL: databaseURL, workerRef: workerRef, batchLimit: int32(limit)}, nil
}

func run(ctx context.Context, getenv func(string) string, output io.Writer) error {
	cfg, err := loadConfig(getenv)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, cfg.databaseURL)
	if err != nil {
		return errors.New("open retention database")
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return errors.New("begin retention transaction")
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+retentionRole); err != nil {
		return errors.New("assume retention capability")
	}
	var currentRole string
	if err = tx.QueryRow(ctx, "SELECT current_user").Scan(&currentRole); err != nil || currentRole != retentionRole {
		return errors.New("verify retention capability")
	}
	queries := dbgen.New(tx)
	result, err := queries.RunPrivacyRetention(ctx, dbgen.RunPrivacyRetentionParams{WorkerRef: cfg.workerRef, BatchLimit: cfg.batchLimit})
	if err != nil {
		return errors.New("execute retention maintenance")
	}
	status, err := queries.GetPrivacyRetentionStatus(ctx)
	if err != nil {
		return errors.New("read retention status")
	}
	if err = tx.Commit(ctx); err != nil {
		return errors.New("commit retention maintenance")
	}

	return writeEvidence(output, result, status)
}

func writeEvidence(output io.Writer, result dbgen.RunPrivacyRetentionRow, status dbgen.GetPrivacyRetentionStatusRow) error {
	fmt.Fprintf(output, "privacy_retention_succeeded sessions_deleted=%d tokens_deleted=%d outbox_stopped=%d outbox_payloads_deleted=%d outbox_evidence_deleted=%d consent_network_scrubbed=%d consent_evidence_deleted=%d audit_events_pseudonymized=%d repair_attachments_queued=%d event_responses_deleted=%d announcement_deliveries_deleted=%d suggestions_deleted=%d privacy_working_scrubbed=%d auth_limits_deleted=%d due_count=%d oldest_due_age_seconds=%d repair_due_count=%d repair_overdue_count=%d repair_terminal_failures=%d repair_legacy_due_count=%d last_run_age_seconds=%d\n",
		result.SessionsDeleted, result.TokensDeleted, result.OutboxStopped, result.OutboxPayloadsDeleted,
		result.OutboxEvidenceDeleted, result.ConsentNetworkScrubbed, result.ConsentEvidenceDeleted,
		result.AuditEventsPseudonymized, result.RepairAttachmentsQueued, result.EventResponsesDeleted,
		result.AnnouncementDeliveriesDeleted, result.SuggestionsDeleted, result.PrivacyWorkingScrubbed,
		result.AuthLimitsDeleted, status.DueCount, status.OldestDueAgeSeconds, status.RepairDueCount,
		status.RepairOverdueCount, status.RepairTerminalFailures, status.RepairLegacyDueCount, status.LastRunAgeSeconds)
	if status.RepairOverdueCount > 0 || status.RepairTerminalFailures > 0 || status.RepairLegacyDueCount > 0 {
		fmt.Fprintln(output, "privacy_retention_sla_breach")
		return errors.New("repair retention SLA breach")
	}
	if (status.DueCount > 0 && status.OldestDueAgeSeconds > maximumBacklogAge) || status.LastRunAgeSeconds > maximumLastRunAge {
		fmt.Fprintln(output, "privacy_retention_backlog_breach")
		return errors.New("retention backlog breach")
	}
	return nil
}
