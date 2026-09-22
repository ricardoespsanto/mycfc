package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultBatchLimit = int32(500)

type result struct {
	sessionsDeleted, tokensDeleted, outboxStopped, outboxPayloadsDeleted, outboxEvidenceDeleted                         int32
	consentNetworkScrubbed, consentEvidenceDeleted, auditEventsPseudonymized, repairAttachmentsQueued                   int32
	eventResponsesDeleted, announcementDeliveriesDeleted, suggestionsDeleted, privacyWorkingScrubbed, authLimitsDeleted int32
}

type status struct {
	dueCount, oldestDueAgeSeconds, repairDueCount, repairOverdueCount int64
	repairTerminalFailures, repairLegacyDueCount, lastRunAgeSeconds   int64
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "data_retention_failed")
		os.Exit(1)
	}
}

// run is deliberately one-shot and requires an explicit board-review
// acknowledgement. The signed production image contains the one-shot binary,
// but no service or timer invokes it.
func run(ctx context.Context, args []string, getenv func(string) string, output io.Writer) error {
	if len(args) != 2 || args[0] != "run" || args[1] != "--confirmed-manual-review" {
		return errors.New("data retention requires explicit manual review confirmation")
	}
	if getenv("DATA_RETENTION_ENABLED") != "true" {
		return errors.New("data retention is disabled")
	}
	databaseURL := strings.TrimSpace(getenv("DATA_RETENTION_DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("data retention database URL is missing")
	}
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return errors.New("open data retention database")
	}
	poolConfig.AfterConnect = activateCapabilityRole("mycfc_data_retention")
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return errors.New("open data retention database")
	}
	defer pool.Close()
	var activeRole string
	if err = pool.QueryRow(ctx, "SELECT current_user").Scan(&activeRole); err != nil || activeRole != "mycfc_data_retention" {
		return errors.New("data retention database identity rejected")
	}
	workerRef := uuid.New()
	var ignored uuid.UUID
	var r result
	err = pool.QueryRow(ctx, `SELECT run_id,sessions_deleted,tokens_deleted,outbox_stopped,outbox_payloads_deleted,
outbox_evidence_deleted,consent_network_scrubbed,consent_evidence_deleted,audit_events_pseudonymized,
repair_attachments_queued,event_responses_deleted,announcement_deliveries_deleted,suggestions_deleted,
privacy_working_scrubbed,auth_limits_deleted FROM data_retention_run($1,$2)`, workerRef, defaultBatchLimit).Scan(
		&ignored, &r.sessionsDeleted, &r.tokensDeleted, &r.outboxStopped, &r.outboxPayloadsDeleted,
		&r.outboxEvidenceDeleted, &r.consentNetworkScrubbed, &r.consentEvidenceDeleted, &r.auditEventsPseudonymized,
		&r.repairAttachmentsQueued, &r.eventResponsesDeleted, &r.announcementDeliveriesDeleted, &r.suggestionsDeleted,
		&r.privacyWorkingScrubbed, &r.authLimitsDeleted,
	)
	if err != nil {
		return errors.New("execute data retention")
	}
	var s status
	if err = pool.QueryRow(ctx, `SELECT due_count,oldest_due_age_seconds,repair_due_count,repair_overdue_count,
repair_terminal_failures,repair_legacy_due_count,last_run_age_seconds FROM data_retention_status()`).Scan(
		&s.dueCount, &s.oldestDueAgeSeconds, &s.repairDueCount, &s.repairOverdueCount,
		&s.repairTerminalFailures, &s.repairLegacyDueCount, &s.lastRunAgeSeconds,
	); err != nil {
		return errors.New("read data retention status")
	}
	fmt.Fprintf(output, "data_retention_succeeded sessions_deleted=%d tokens_deleted=%d outbox_stopped=%d outbox_payloads_deleted=%d outbox_evidence_deleted=%d consent_network_scrubbed=%d consent_evidence_deleted=%d audit_events_pseudonymized=%d repair_attachments_queued=%d event_responses_deleted=%d announcement_deliveries_deleted=%d suggestions_deleted=%d privacy_working_scrubbed=%d auth_limits_deleted=%d due_count=%d oldest_due_age_seconds=%d repair_due_count=%d repair_overdue_count=%d repair_terminal_failures=%d repair_legacy_due_count=%d last_run_age_seconds=%d\n",
		r.sessionsDeleted, r.tokensDeleted, r.outboxStopped, r.outboxPayloadsDeleted, r.outboxEvidenceDeleted,
		r.consentNetworkScrubbed, r.consentEvidenceDeleted, r.auditEventsPseudonymized, r.repairAttachmentsQueued,
		r.eventResponsesDeleted, r.announcementDeliveriesDeleted, r.suggestionsDeleted, r.privacyWorkingScrubbed, r.authLimitsDeleted,
		s.dueCount, s.oldestDueAgeSeconds, s.repairDueCount, s.repairOverdueCount, s.repairTerminalFailures, s.repairLegacyDueCount, s.lastRunAgeSeconds)
	return nil
}

func activateCapabilityRole(role string) func(context.Context, *pgx.Conn) error {
	return func(ctx context.Context, conn *pgx.Conn) error {
		var allowed bool
		if err := conn.QueryRow(ctx, "SELECT pg_has_role(session_user,$1,'MEMBER')", role).Scan(&allowed); err != nil || !allowed {
			return errors.New("database capability membership rejected")
		}
		if _, err := conn.Exec(ctx, "SET ROLE "+role); err != nil {
			return errors.New("activate database capability")
		}
		var active string
		if err := conn.QueryRow(ctx, "SELECT current_user").Scan(&active); err != nil || active != role {
			return errors.New("database capability activation rejected")
		}
		return nil
	}
}
