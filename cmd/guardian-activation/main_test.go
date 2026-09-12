package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type activationRowFake struct {
	values []any
	err    error
}

func (row activationRowFake) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != len(row.values) {
		return errors.New("unexpected scan shape")
	}
	for index, value := range row.values {
		destination := reflect.ValueOf(destinations[index]).Elem()
		if value == nil {
			destination.SetZero()
			continue
		}
		destination.Set(reflect.ValueOf(value))
	}
	return nil
}

type activationDatabaseFake struct {
	rows   []activationRowFake
	closed bool
}

func (database *activationDatabaseFake) QueryRow(context.Context, string, ...any) pgx.Row {
	row := database.rows[0]
	database.rows = database.rows[1:]
	return row
}

func (database *activationDatabaseFake) Close() { database.closed = true }

type activationErrorWriter struct{}

func (activationErrorWriter) Write([]byte) (int, error) { return 0, errors.New("write unavailable") }

func validApproval(t *testing.T, actor uuid.UUID) []byte {
	t.Helper()
	policy := fixedPolicy()
	policyRaw, err := marshalCanonical(policy)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(policyRaw)
	doc := approvalDocument{
		Contract: "mycfc/guardian-authority-policy-approval/v1", AuthorizedOperatorActorRef: actor, ExpectedDatabase: "mycfc",
		ControllerRole: "CLUB_DIRECTION", ControllerApprovalRef: "direction/minute/actual-reference",
		ControllerApprovedOn: "2026-09-12", EffectiveOn: "2026-09-12", ReviewDueOn: "2027-09-12",
		LegalReviewerRef: "legal/reviewer/reference", LegalReviewRef: "legal/review/evidence", LegalReviewedOn: "2026-09-12",
		LegalReviewConclusion: "APPROVED", Policy: policy, PolicySHA256: hex.EncodeToString(digest[:]), PolicyVersion: "guardian-v2-actual",
	}
	payload, err := marshalCanonical(doc)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestParseApprovalRequiresCanonicalActorBoundPolicyDigest(t *testing.T) {
	actor := uuid.New()
	payload := validApproval(t, actor)
	canonical, policy, err := parseApproval(payload, actor, "mycfc")
	if err != nil || string(canonical) != string(payload) || len(policy) == 0 {
		t.Fatalf("parse=%v policy=%q", err, policy)
	}
	if _, _, err = parseApproval(append(payload, '\n'), actor, "mycfc"); err == nil {
		t.Fatal("noncanonical approval accepted")
	}
	if _, _, err = parseApproval(payload, uuid.New(), "mycfc"); err == nil {
		t.Fatal("different actor accepted")
	}

	var doc approvalDocument
	if err = json.Unmarshal(payload, &doc); err != nil {
		t.Fatal(err)
	}
	doc.PolicySHA256 = strings.Repeat("0", 64)
	tampered, _ := json.Marshal(doc)
	if _, _, err = parseApproval(tampered, actor, "mycfc"); err == nil {
		t.Fatal("tampered policy digest accepted")
	}
}

func TestParseApprovalRejectsUnknownFieldsAndInvalidDates(t *testing.T) {
	actor := uuid.New()
	payload := validApproval(t, actor)
	withUnknown := append(payload[:len(payload)-1], []byte(`,"unexpected":true}`)...)
	if _, _, err := parseApproval(withUnknown, actor, "mycfc"); err == nil {
		t.Fatal("unknown field accepted")
	}
	var doc approvalDocument
	_ = json.Unmarshal(payload, &doc)
	doc.ReviewDueOn = doc.EffectiveOn
	invalid, _ := json.Marshal(doc)
	if _, _, err := parseApproval(invalid, actor, "mycfc"); err == nil {
		t.Fatal("nonfuture review accepted")
	}
}

func TestLoadConfigUsesFixedRoleImageAndEmbeddedSchema(t *testing.T) {
	actor := uuid.New()
	payload := validApproval(t, actor)
	oldUID, oldReader := effectiveUserID, approvalReader
	effectiveUserID = func() int { return 0 }
	approvalReader = func(string, uuid.UUID, string) ([]byte, []byte, error) { return parseApproval(payload, actor, "mycfc") }
	t.Cleanup(func() { effectiveUserID, approvalReader = oldUID, oldReader })
	env := map[string]string{
		"GUARDIAN_ACTIVATION_DATABASE_URL":      "postgres://mycfc_guardian_activation_operator:secret@postgres:5432/mycfc?sslmode=disable",
		"GUARDIAN_ACTIVATION_EXPECTED_DATABASE": "mycfc", "GUARDIAN_ACTIVATION_CURRENT_IMAGE_DIGEST": "sha256:" + strings.Repeat("a", 64),
		"GUARDIAN_ACTIVATION_ACTOR_REF": actor.String(), "GUARDIAN_ACTIVATION_APPROVAL_FILE": "/protected/approval.json",
	}
	cfg, err := loadConfig("enable", func(name string) string { return env[name] })
	if err != nil || cfg.actor != actor || len(cfg.schemaDigest) != 64 || len(cfg.approvalRaw) == 0 {
		t.Fatalf("config=%+v err=%v", cfg, err)
	}
	env["GUARDIAN_ACTIVATION_DATABASE_URL"] = strings.Replace(env["GUARDIAN_ACTIVATION_DATABASE_URL"], operatorRole, "mycfc_app", 1)
	if _, err = loadConfig("status", func(name string) string { return env[name] }); err == nil {
		t.Fatal("web role accepted")
	}
}

func TestApprovalDateValidationIsCalendarBased(t *testing.T) {
	approval := approvalDocument{ControllerApprovedOn: "2026-01-01", EffectiveOn: "2026-01-02", ReviewDueOn: "2027-01-02", LegalReviewedOn: "2026-01-01"}
	if !validApprovalDates(approval) {
		t.Fatal("valid dates rejected")
	}
	approval.LegalReviewedOn = time.Now().Format(time.RFC3339)
	if validApprovalDates(approval) {
		t.Fatal("timestamp accepted where a date is required")
	}
}

func activationExecutableEnvironment(t *testing.T, actor uuid.UUID) map[string]string {
	t.Helper()
	return map[string]string{
		"GUARDIAN_ACTIVATION_DATABASE_URL":         "postgres://mycfc_guardian_activation_operator:secret@postgres:5432/mycfc?sslmode=disable",
		"GUARDIAN_ACTIVATION_EXPECTED_DATABASE":    "mycfc",
		"GUARDIAN_ACTIVATION_CURRENT_IMAGE_DIGEST": "sha256:" + strings.Repeat("a", 64),
		"GUARDIAN_ACTIVATION_ACTOR_REF":            actor.String(), "GUARDIAN_ACTIVATION_APPROVAL_FILE": "/protected/approval.json",
	}
}

func installExecutableFakes(t *testing.T, database *activationDatabaseFake, approval []byte, actor uuid.UUID) {
	t.Helper()
	oldUID, oldReader, oldOpen := effectiveUserID, approvalReader, openDatabase
	effectiveUserID = func() int { return 0 }
	approvalReader = func(string, uuid.UUID, string) ([]byte, []byte, error) {
		return parseApproval(approval, actor, "mycfc")
	}
	openDatabase = func(context.Context, string) (activationDatabase, error) { return database, nil }
	t.Cleanup(func() { effectiveUserID, approvalReader, openDatabase = oldUID, oldReader, oldOpen })
}

func TestExecutableModesEmitOnlyAggregateOutcomes(t *testing.T) {
	actor := uuid.New()
	approval := validApproval(t, actor)
	env := activationExecutableEnvironment(t, actor)
	getenv := func(name string) string { return env[name] }
	hash := bytes.Repeat([]byte{0x42}, sha256.Size)
	policyVersion := "guardian-v2-test"
	tests := []struct {
		mode, contains string
		rows           []activationRowFake
	}{
		{"status", "state=DISABLED", []activationRowFake{{values: []any{"DISABLED", true, true, true, true, &policyVersion, hash, hash, int64(2), int64(0), int64(1), int64(3), int64(4), int64(5)}}}},
		{"preflight", "outcome=ready", []activationRowFake{{values: []any{true, false, policyVersion, hash, hash, int64(0), int64(0), int64(0)}}}},
		{"enable", "changed=true", []activationRowFake{{values: []any{true, policyVersion, hash, hash}}}},
		{"enable", "changed=false", []activationRowFake{{values: []any{false, policyVersion, hash, hash}}}},
		{"disable", "relationships_revoked=2", []activationRowFake{{values: []any{&policyVersion, int64(2), int64(3), int64(4)}}}},
	}
	for _, test := range tests {
		t.Run(test.mode+test.contains, func(t *testing.T) {
			database := &activationDatabaseFake{rows: test.rows}
			installExecutableFakes(t, database, approval, actor)
			var stdout, stderr bytes.Buffer
			if code := execute([]string{test.mode}, getenv, &stdout, &stderr); code != 0 {
				t.Fatalf("exit=%d stderr=%q", code, stderr.String())
			}
			if !database.closed || !strings.Contains(stdout.String(), test.contains) || strings.Contains(stdout.String(), actor.String()) {
				t.Fatalf("closed=%t output=%q", database.closed, stdout.String())
			}
		})
	}
}

func TestExecutableBlockedPreflightUsesExitThree(t *testing.T) {
	actor := uuid.New()
	approval := validApproval(t, actor)
	database := &activationDatabaseFake{rows: []activationRowFake{{values: []any{false, false, "guardian-v2-test", []byte{}, []byte{}, int64(1), int64(1), int64(1)}}}}
	installExecutableFakes(t, database, approval, actor)
	env := activationExecutableEnvironment(t, actor)
	var output bytes.Buffer
	if code := execute([]string{"preflight"}, func(name string) string { return env[name] }, &output, io.Discard); code != 3 || !strings.Contains(output.String(), "outcome=blocked") {
		t.Fatalf("exit=%d output=%q", code, output.String())
	}
}

func TestExecutableRedactsDatabaseErrorsAndPropagatesOutputErrors(t *testing.T) {
	actor := uuid.New()
	approval := validApproval(t, actor)
	env := activationExecutableEnvironment(t, actor)
	getenv := func(name string) string { return env[name] }
	database := &activationDatabaseFake{rows: []activationRowFake{{err: errors.New("database secret should never be logged")}}}
	installExecutableFakes(t, database, approval, actor)
	var stderr bytes.Buffer
	if code := execute([]string{"status"}, getenv, io.Discard, &stderr); code != 1 || strings.Contains(stderr.String(), "secret") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}

	hash := bytes.Repeat([]byte{1}, sha256.Size)
	for _, test := range []struct {
		mode string
		row  activationRowFake
	}{
		{"status", activationRowFake{values: []any{"DISABLED", true, true, true, true, (*string)(nil), []byte(nil), []byte(nil), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0)}}},
		{"preflight", activationRowFake{values: []any{true, false, "v", hash, hash, int64(0), int64(0), int64(0)}}},
		{"enable", activationRowFake{values: []any{true, "v", hash, hash}}},
		{"disable", activationRowFake{values: []any{(*string)(nil), int64(0), int64(0), int64(0)}}},
	} {
		database = &activationDatabaseFake{rows: []activationRowFake{test.row}}
		oldOpen := openDatabase
		openDatabase = func(context.Context, string) (activationDatabase, error) { return database, nil }
		if code := execute([]string{test.mode}, getenv, activationErrorWriter{}, io.Discard); code != 1 {
			t.Fatalf("mode=%s exit=%d", test.mode, code)
		}
		openDatabase = oldOpen
	}
}

func TestOfflineGeneratorProducesAcceptedCanonicalApproval(t *testing.T) {
	actor := uuid.New()
	today := time.Now()
	effective := today.AddDate(0, 0, -1).Format(time.DateOnly)
	review := today.AddDate(1, 0, 0).Format(time.DateOnly)
	args := []string{"generate", "--actor-ref", actor.String(), "--expected-database", "mycfc", "--policy-version", "guardian-v2-approved",
		"--controller-approval-reference", "direction/minute/reference", "--controller-approved-on", effective, "--effective-on", effective,
		"--review-due-on", review, "--legal-reviewer-reference", "legal/reviewer", "--legal-review-reference", "legal/evidence", "--legal-reviewed-on", effective}
	var output bytes.Buffer
	if code := execute(args, nil, &output, io.Discard); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if bytes.HasSuffix(output.Bytes(), []byte{'\n'}) {
		t.Fatal("generator added a trailing newline")
	}
	if _, _, err := parseApproval(output.Bytes(), actor, "mycfc"); err != nil {
		t.Fatalf("generated approval rejected: %v", err)
	}
}
