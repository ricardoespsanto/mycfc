//go:build integration

package main

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestReadinessUsesRestrictedExecutorAndReportsInactiveActivation(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	if admin.Config().Database != "mycfc_test" {
		t.Fatalf("privacy worker integration test requires mycfc_test, got %q", admin.Config().Database)
	}
	// Reproduce migration 015's release invalidation. Other integration fixtures
	// may have activated the worker earlier in the shared test suite, so the
	// readiness contract must be tested after the real state transition rather
	// than relying on package order.
	if _, err = admin.Exec(ctx, `
		UPDATE privacy_request_activation
		SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp()
		WHERE singleton;
		UPDATE privacy_worker_kill_switch
		SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp()
		WHERE singleton;
		INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at)
		SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton`); err != nil {
		t.Fatal(err)
	}

	const executorRole = "mycfc_privacy_executor"
	var roleExists bool
	if err = admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, executorRole).Scan(&roleExists); err != nil {
		t.Fatal(err)
	}
	if roleExists {
		t.Skip("disposable privacy executor role already exists")
	}
	roleIdentifier := pgx.Identifier{executorRole}.Sanitize()
	databaseIdentifier := pgx.Identifier{admin.Config().Database}.Sanitize()
	password := strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, `CREATE ROLE `+roleIdentifier+` LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD '`+password+`'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, cleanupErr := admin.Exec(cleanupCtx, `DROP OWNED BY `+roleIdentifier); cleanupErr != nil {
			t.Errorf("revoke disposable executor privileges: %v", cleanupErr)
			return
		}
		if _, cleanupErr := admin.Exec(cleanupCtx, `DROP ROLE `+roleIdentifier); cleanupErr != nil {
			t.Errorf("drop disposable executor role: %v", cleanupErr)
		}
	})
	for _, statement := range []string{
		`GRANT CONNECT ON DATABASE ` + databaseIdentifier + ` TO ` + roleIdentifier,
		`GRANT USAGE ON SCHEMA public TO ` + roleIdentifier,
	} {
		if _, err = admin.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	var ready bool
	if err = admin.QueryRow(ctx, `SELECT privacy_worker_activation_ready()`).Scan(&ready); err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("fresh migration baseline unexpectedly activated the privacy worker")
	}

	executorURL, err := executorDatabaseURL(databaseURL, executorRole, password)
	if err != nil {
		t.Fatal(err)
	}
	env := workerTestEnvironment(t, executorURL)
	getenv := func(name string) string { return env[name] }
	if err = run(ctx, []string{"readiness"}, getenv, &bytes.Buffer{}); err == nil || errors.Is(err, errActivationRequired) {
		t.Fatalf("unprivileged readiness error=%v", err)
	}
	if _, err = admin.Exec(ctx, `GRANT EXECUTE ON FUNCTION privacy_worker_activation_ready() TO `+roleIdentifier); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err = run(ctx, []string{"readiness"}, getenv, &output)
	if err != errActivationRequired {
		t.Fatalf("readiness error=%v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("inactive readiness wrote success output %q", output.String())
	}
	message, status := failureStatus(err)
	if message != "privacy_worker_readiness_activation_required" || status != activationRequiredExit {
		t.Fatalf("inactive status=(%q,%d)", message, status)
	}
	if err = run(ctx, []string{"serve"}, getenv, &bytes.Buffer{}); err == nil || errors.Is(err, errActivationRequired) {
		t.Fatalf("inactive serve error=%v", err)
	}

	// Exercise the active bootstrap without allowing any work cycle or external
	// mutation. The shared integration suite has authenticated activation
	// fixtures; bind one to the test activation row, then force the CloudWatch
	// endpoint to a closed loopback port and require a safe bootstrap failure.
	var approvalID, actorID uuid.UUID
	var policyVersion string
	err = admin.QueryRow(ctx, `
		SELECT approval.id,proposal.policy_version,approval.approved_by_ref
		FROM privacy_activation_approvals approval
		JOIN privacy_activation_proposals proposal ON proposal.id=approval.proposal_id
		WHERE privacy_activation_authenticated_set_digest(proposal.policy_version,proposal.evidence_ids)=proposal.evidence_set_sha256
		  AND (SELECT count(DISTINCT evidence.kind) FROM privacy_activation_evidence evidence
		       WHERE evidence.id=ANY(proposal.evidence_ids) AND evidence.expires_at>clock_timestamp())=4
		ORDER BY approval.approved_at DESC LIMIT 1`).Scan(&approvalID, &policyVersion, &actorID)
	if !errors.Is(err, pgx.ErrNoRows) {
		if err != nil {
			t.Fatal(err)
		}
		if _, err = admin.Exec(ctx, `UPDATE privacy_request_activation
			SET policy_version=$1,enabled=true,fulfilment_ready=true,approval_id=$2,updated_by=$3,updated_at=clock_timestamp()
			WHERE singleton`,
			policyVersion, approvalID, actorID); err != nil {
			t.Fatal(err)
		}
		if _, err = admin.Exec(ctx, `UPDATE privacy_worker_kill_switch
			SET engaged=false,activation_approval_id=$1,version=version+1,changed_at=clock_timestamp() WHERE singleton`, approvalID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, cleanupErr := admin.Exec(context.Background(), `UPDATE privacy_request_activation
				SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
				UPDATE privacy_worker_kill_switch SET engaged=true,activation_approval_id=NULL,version=version+1,changed_at=clock_timestamp() WHERE singleton`)
			if cleanupErr != nil {
				t.Errorf("restore inactive worker state: %v", cleanupErr)
			}
		})
		for name, value := range map[string]string{
			"AWS_ACCESS_KEY_ID":         "integration-access-key",
			"AWS_SECRET_ACCESS_KEY":     "integration-secret-key",
			"AWS_ENDPOINT_URL":          "http://127.0.0.1:1",
			"AWS_EC2_METADATA_DISABLED": "true",
		} {
			t.Setenv(name, value)
		}
		bootstrapCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err = run(bootstrapCtx, []string{"serve"}, getenv, &bytes.Buffer{}); err == nil || errors.Is(err, errActivationRequired) {
			t.Fatalf("active bootstrap failure=%v", err)
		}
	}
}

func executorDatabaseURL(databaseURL, username, password string) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" || parsed.Host == "" {
		return "", errors.New("test database URL rejected")
	}
	parsed.User = url.UserPassword(username, password)
	return parsed.String(), nil
}
