package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const operatorRole = "mycfc_guardian_activation_operator"

var (
	errBlocked      = errors.New("guardian activation blocked")
	effectiveUserID = os.Geteuid
	openDatabase    = func(ctx context.Context, dsn string) (activationDatabase, error) { return pgxpool.New(ctx, dsn) }
	approvalReader  = readApproval
	runCommand      = run
)

type activationDatabase interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Close()
}

type policyDocument struct {
	Contract                   string   `json:"contract"`
	EvidenceCategories         []string `json:"evidence_categories"`
	ReasonCodes                []string `json:"reason_codes"`
	ValidityDays               int      `json:"validity_days"`
	ReviewDays                 int      `json:"review_days"`
	FreshAuthenticationMinutes int      `json:"fresh_authentication_minutes"`
	InvitationValidityDays     int      `json:"invitation_validity_days"`
	SubmissionAccountLimit24H  int      `json:"submission_account_limit_24h"`
	SubmissionNetworkLimit24H  int      `json:"submission_network_limit_24h"`
	RenewalPeriodMonths        int      `json:"renewal_period_months"`
	AdultAgeYears              int      `json:"adult_age_years"`
	GuardiansPerMinor          int      `json:"guardians_per_minor"`
}

type approvalDocument struct {
	Contract                   string         `json:"contract"`
	AuthorizedOperatorActorRef uuid.UUID      `json:"authorized_operator_actor_ref"`
	ExpectedDatabase           string         `json:"expected_database"`
	ControllerRole             string         `json:"controller_role"`
	ControllerApprovalRef      string         `json:"controller_approval_reference"`
	ControllerApprovedOn       string         `json:"controller_approved_on"`
	EffectiveOn                string         `json:"effective_on"`
	ReviewDueOn                string         `json:"review_due_on"`
	LegalReviewerRef           string         `json:"legal_reviewer_reference"`
	LegalReviewRef             string         `json:"legal_review_reference"`
	LegalReviewedOn            string         `json:"legal_reviewed_on"`
	LegalReviewConclusion      string         `json:"legal_review_conclusion"`
	Policy                     policyDocument `json:"policy"`
	PolicySHA256               string         `json:"policy_sha256"`
	PolicyVersion              string         `json:"policy_version"`
}

type config struct {
	databaseURL, expectedDatabase, imageDigest, schemaDigest string
	actor                                                    uuid.UUID
	approvalRaw, policyRaw                                   []byte
}

func main() {
	os.Exit(execute(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func execute(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	mode := "status"
	if len(args) > 0 {
		mode = args[0]
	}
	if mode == "generate" {
		if err := generateApproval(args[1:], stdout); err != nil {
			fmt.Fprintln(stderr, "event=guardian_activation_failed mode=generate error_class=configuration")
			return 2
		}
		return 0
	}
	if len(args) > 1 {
		fmt.Fprintln(stderr, "event=guardian_activation_failed mode=usage error_class=configuration")
		return 2
	}
	err := runCommand(context.Background(), mode, getenv, stdout)
	if err == nil {
		return 0
	}
	if errors.Is(err, errBlocked) {
		return 3
	}
	fmt.Fprintf(stderr, "event=guardian_activation_failed mode=%s error_class=configuration_or_database\n", safeMode(mode))
	return 1
}

func fixedPolicy() policyDocument {
	return policyDocument{
		Contract:           "guardian-authority-v2",
		EvidenceCategories: []string{"CLUB_REGISTRATION_RECORD", "IN_PERSON_ID_AND_CIVIL_RECORD", "COURT_OR_LEGAL_AUTHORITY"},
		ReasonCodes:        []string{"RELATIONSHIP_CONFIRMED", "EVIDENCE_INSUFFICIENT", "AUTHORITY_NOT_ESTABLISHED", "CONFLICT", "AUTHORITY_CHANGED", "UNCERTAINTY", "AUTHORITY_ENDED", "ELIGIBILITY_ENDED", "REVIEW_EXPIRED", "MAJORITY_REACHED"},
		ValidityDays:       365, ReviewDays: 365, FreshAuthenticationMinutes: 60, InvitationValidityDays: 30,
		SubmissionAccountLimit24H: 10, SubmissionNetworkLimit24H: 100, RenewalPeriodMonths: 12, AdultAgeYears: 18, GuardiansPerMinor: 1,
	}
}

func generateApproval(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("guardian-activation generate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	actorText := flags.String("actor-ref", "", "authorized administrator UUID")
	database := flags.String("expected-database", "", "exact PostgreSQL database")
	version := flags.String("policy-version", "", "approved policy version")
	controllerRef := flags.String("controller-approval-reference", "", "opaque controller approval reference")
	controllerOn := flags.String("controller-approved-on", "", "controller approval date")
	effectiveOn := flags.String("effective-on", "", "policy effective date")
	reviewOn := flags.String("review-due-on", "", "policy review due date")
	legalReviewer := flags.String("legal-reviewer-reference", "", "opaque legal reviewer reference")
	legalRef := flags.String("legal-review-reference", "", "opaque legal evidence reference")
	legalOn := flags.String("legal-reviewed-on", "", "legal review date")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !validIdentifier(*database) {
		return errors.New("approval generator arguments rejected")
	}
	actor, err := uuid.Parse(*actorText)
	if err != nil || actor == uuid.Nil || actor.String() != *actorText {
		return errors.New("approval generator actor rejected")
	}
	policy := fixedPolicy()
	policyRaw, err := marshalCanonical(policy)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(policyRaw)
	document := approvalDocument{
		Contract: "mycfc/guardian-authority-policy-approval/v1", AuthorizedOperatorActorRef: actor, ExpectedDatabase: *database,
		ControllerRole: "CLUB_DIRECTION", ControllerApprovalRef: *controllerRef, ControllerApprovedOn: *controllerOn,
		EffectiveOn: *effectiveOn, ReviewDueOn: *reviewOn, LegalReviewerRef: *legalReviewer, LegalReviewRef: *legalRef,
		LegalReviewedOn: *legalOn, LegalReviewConclusion: "APPROVED", Policy: policy,
		PolicySHA256: hex.EncodeToString(digest[:]), PolicyVersion: *version,
	}
	if !validApprovalDates(document) || !approvalDatesCurrent(document) || !validPolicyVersion(document.PolicyVersion) ||
		!validReference(document.ControllerApprovalRef) || !validReference(document.LegalReviewerRef) || !validReference(document.LegalReviewRef) {
		return errors.New("approval generator evidence rejected")
	}
	canonical, err := marshalCanonical(document)
	if err != nil {
		return err
	}
	if _, _, err = parseApproval(canonical, actor, *database); err != nil {
		return err
	}
	if _, err = output.Write(canonical); err != nil {
		return errors.New("write approval template")
	}
	return nil
}

func marshalCanonical(value any) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(output.Bytes(), []byte{'\n'}), nil
}

func run(ctx context.Context, mode string, getenv func(string) string, output io.Writer) error {
	if mode != "status" && mode != "preflight" && mode != "enable" && mode != "disable" {
		return errors.New("unsupported guardian activation mode")
	}
	cfg, err := loadConfig(mode, getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	database, err := openDatabase(ctx, cfg.databaseURL)
	if err != nil {
		return errors.New("open guardian activation database")
	}
	defer database.Close()
	switch mode {
	case "status":
		return runStatus(ctx, database, cfg, output)
	case "preflight":
		return runPreflight(ctx, database, cfg, output)
	case "enable":
		return runEnable(ctx, database, cfg, output)
	default:
		return runDisable(ctx, database, cfg, output)
	}
}

func loadConfig(mode string, getenv func(string) string) (config, error) {
	var result config
	if effectiveUserID() != 0 {
		return result, errors.New("guardian activation requires root")
	}
	result.databaseURL = strings.TrimSpace(getenv("GUARDIAN_ACTIVATION_DATABASE_URL"))
	result.expectedDatabase = strings.TrimSpace(getenv("GUARDIAN_ACTIVATION_EXPECTED_DATABASE"))
	result.imageDigest = strings.TrimSpace(getenv("GUARDIAN_ACTIVATION_CURRENT_IMAGE_DIGEST"))
	result.schemaDigest = db.EmbeddedMigrationDigest()
	parsed, err := url.Parse(result.databaseURL)
	password, passwordPresent := "", false
	if parsed != nil && parsed.User != nil {
		password, passwordPresent = parsed.User.Password()
	}
	if err != nil || parsed == nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" || parsed.User == nil ||
		parsed.User.Username() != operatorRole || !passwordPresent || password == "" || parsed.Path != "/"+result.expectedDatabase ||
		result.expectedDatabase == "" || parsed.Fragment != "" || result.databaseURL != parsed.String() || !validIdentifier(result.expectedDatabase) || !validImageDigest(result.imageDigest) {
		return config{}, errors.New("guardian activation database or image rejected")
	}
	if mode != "status" {
		result.actor, err = uuid.Parse(strings.TrimSpace(getenv("GUARDIAN_ACTIVATION_ACTOR_REF")))
		if err != nil || result.actor == uuid.Nil || result.actor.String() != strings.TrimSpace(getenv("GUARDIAN_ACTIVATION_ACTOR_REF")) {
			return config{}, errors.New("guardian activation actor rejected")
		}
	}
	if mode == "preflight" || mode == "enable" {
		result.approvalRaw, result.policyRaw, err = approvalReader(getenv("GUARDIAN_ACTIVATION_APPROVAL_FILE"), result.actor, result.expectedDatabase)
		if err != nil {
			return config{}, err
		}
	}
	return result, nil
}

func readApproval(path string, actor uuid.UUID, expectedDatabase string) ([]byte, []byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, nil, errors.New("guardian activation approval file rejected")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return nil, nil, errors.New("guardian activation approval ownership rejected")
	}
	payload, err := os.ReadFile(path)
	if err != nil || len(payload) < 2 || len(payload) > 1<<20 {
		return nil, nil, errors.New("guardian activation approval read rejected")
	}
	return parseApproval(payload, actor, expectedDatabase)
}

func parseApproval(payload []byte, actor uuid.UUID, expectedDatabase string) ([]byte, []byte, error) {
	var approval approvalDocument
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&approval) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, nil, errors.New("guardian activation approval JSON rejected")
	}
	canonical, err := marshalCanonical(approval)
	policyRaw, policyErr := marshalCanonical(approval.Policy)
	if err != nil || policyErr != nil || !bytes.Equal(payload, canonical) || approval.AuthorizedOperatorActorRef != actor || approval.ExpectedDatabase != expectedDatabase {
		return nil, nil, errors.New("guardian activation approval must be canonical and actor-bound")
	}
	policyDigest := sha256.Sum256(policyRaw)
	if approval.PolicySHA256 != hex.EncodeToString(policyDigest[:]) || !validApprovalDates(approval) {
		return nil, nil, errors.New("guardian activation approval binding rejected")
	}
	return canonical, policyRaw, nil
}

func validApprovalDates(approval approvalDocument) bool {
	approved, err1 := time.Parse(time.DateOnly, approval.ControllerApprovedOn)
	effective, err2 := time.Parse(time.DateOnly, approval.EffectiveOn)
	review, err3 := time.Parse(time.DateOnly, approval.ReviewDueOn)
	legal, err4 := time.Parse(time.DateOnly, approval.LegalReviewedOn)
	return err1 == nil && err2 == nil && err3 == nil && err4 == nil && !approved.After(effective) && !legal.After(effective) && review.After(effective)
}

func approvalDatesCurrent(approval approvalDocument) bool {
	location, err := time.LoadLocation("Europe/Lisbon")
	if err != nil {
		return false
	}
	effective, effectiveErr := time.Parse(time.DateOnly, approval.EffectiveOn)
	review, reviewErr := time.Parse(time.DateOnly, approval.ReviewDueOn)
	todayText := time.Now().In(location).Format(time.DateOnly)
	today, todayErr := time.Parse(time.DateOnly, todayText)
	return effectiveErr == nil && reviewErr == nil && todayErr == nil && !effective.After(today) && review.After(today)
}

type statusRow struct {
	state                                                             string
	databaseCurrent, migrationsCurrent, imageCurrent, contractCurrent bool
	policyVersion                                                     *string
	policySHA256, approvalSHA256                                      []byte
	pending, current, stale, credentials, sessions, activeInvitations int64
}

func runStatus(ctx context.Context, database activationDatabase, cfg config, output io.Writer) error {
	var row statusRow
	err := database.QueryRow(ctx, `SELECT * FROM guardian_ops.status($1,$2,$3)`, cfg.expectedDatabase, cfg.imageDigest, cfg.schemaDigest).Scan(
		&row.state, &row.databaseCurrent, &row.migrationsCurrent, &row.imageCurrent, &row.contractCurrent, &row.policyVersion,
		&row.policySHA256, &row.approvalSHA256, &row.pending, &row.current, &row.stale, &row.credentials, &row.sessions, &row.activeInvitations)
	if err != nil {
		return errors.New("guardian activation status rejected")
	}
	_, err = fmt.Fprintf(output, "event=guardian_activation_status state=%s database_current=%t migrations_current=%t image_current=%t contract_current=%t pending=%d current_relationships=%d stale_relationships=%d credentials_at_risk=%d sessions_at_risk=%d active_invitations=%d\n",
		row.state, row.databaseCurrent, row.migrationsCurrent, row.imageCurrent, row.contractCurrent, row.pending, row.current, row.stale, row.credentials, row.sessions, row.activeInvitations)
	return err
}

func preflight(ctx context.Context, database activationDatabase, cfg config) (ready, already bool, version string, policy, approval []byte, stale, credentials, sessions int64, err error) {
	err = database.QueryRow(ctx, `SELECT * FROM guardian_ops.preflight($1,$2,$3,$4,$5,$6,$7)`, cfg.actor, string(cfg.approvalRaw), cfg.approvalRaw,
		cfg.policyRaw, cfg.imageDigest, cfg.schemaDigest, cfg.expectedDatabase).Scan(&ready, &already, &version, &policy, &approval, &stale, &credentials, &sessions)
	if err != nil {
		err = errors.New("guardian activation preflight rejected")
	}
	return
}

func runPreflight(ctx context.Context, database activationDatabase, cfg config, output io.Writer) error {
	ready, already, _, _, _, stale, credentials, sessions, err := preflight(ctx, database, cfg)
	if err != nil {
		return err
	}
	outcome := "ready"
	if !ready {
		outcome = "blocked"
	}
	if _, err = fmt.Fprintf(output, "event=guardian_activation_preflight outcome=%s already_enabled=%t stale_relationships=%d credentials_at_risk=%d sessions_at_risk=%d\n", outcome, already, stale, credentials, sessions); err != nil {
		return errors.New("write guardian activation preflight")
	}
	if !ready {
		return errBlocked
	}
	return nil
}

func runEnable(ctx context.Context, database activationDatabase, cfg config, output io.Writer) error {
	var changed bool
	var version string
	var policy, approval []byte
	if err := database.QueryRow(ctx, `SELECT * FROM guardian_ops.enable($1,$2,$3,$4,$5,$6,$7)`, cfg.actor, string(cfg.approvalRaw), cfg.approvalRaw,
		cfg.policyRaw, cfg.imageDigest, cfg.schemaDigest, cfg.expectedDatabase).Scan(&changed, &version, &policy, &approval); err != nil {
		return errors.New("guardian activation enable rejected")
	}
	if _, err := fmt.Fprintf(output, "event=guardian_activation_enabled changed=%t policy_sha256=%s approval_sha256=%s image_digest=%s schema_migration_digest=%s\n",
		changed, hex.EncodeToString(policy), hex.EncodeToString(approval), cfg.imageDigest, cfg.schemaDigest); err != nil {
		return errors.New("write guardian activation enable")
	}
	return nil
}

func runDisable(ctx context.Context, database activationDatabase, cfg config, output io.Writer) error {
	var version *string
	var relationships, credentials, sessions int64
	if err := database.QueryRow(ctx, `SELECT * FROM guardian_ops.disable($1,$2)`, cfg.actor, cfg.expectedDatabase).Scan(&version, &relationships, &credentials, &sessions); err != nil {
		return errors.New("guardian activation disable rejected")
	}
	if _, err := fmt.Fprintf(output, "event=guardian_activation_disabled intake=blocked relationships_revoked=%d credentials_revoked=%d sessions_revoked=%d\n", relationships, credentials, sessions); err != nil {
		return errors.New("write guardian activation disable")
	}
	return nil
}

func validIdentifier(value string) bool {
	if len(value) < 1 || len(value) > 63 || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) {
		return false
	}
	for _, character := range value[1:] {
		if !((character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_') {
			return false
		}
	}
	return true
}

func validReference(value string) bool {
	return value == strings.TrimSpace(value) && len(value) >= 1 && len(value) <= 200
}

func validPolicyVersion(value string) bool {
	if len(value) < 1 || len(value) > 80 || !isASCIIAlphaNumeric(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !isASCIIAlphaNumeric(value[index]) && !strings.ContainsRune("._/-", rune(value[index])) {
			return false
		}
	}
	return true
}

func isASCIIAlphaNumeric(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func validImageDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func safeMode(mode string) string {
	switch mode {
	case "status", "preflight", "enable", "disable":
		return mode
	default:
		return "usage"
	}
}
