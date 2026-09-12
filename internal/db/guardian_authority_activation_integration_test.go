//go:build integration

package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type guardianActivationApproval struct {
	Contract                   string          `json:"contract"`
	AuthorizedOperatorActorRef uuid.UUID       `json:"authorized_operator_actor_ref"`
	ExpectedDatabase           string          `json:"expected_database"`
	ControllerRole             string          `json:"controller_role"`
	ControllerApprovalRef      string          `json:"controller_approval_reference"`
	ControllerApprovedOn       string          `json:"controller_approved_on"`
	EffectiveOn                string          `json:"effective_on"`
	ReviewDueOn                string          `json:"review_due_on"`
	LegalReviewerRef           string          `json:"legal_reviewer_reference"`
	LegalReviewRef             string          `json:"legal_review_reference"`
	LegalReviewedOn            string          `json:"legal_reviewed_on"`
	LegalReviewConclusion      string          `json:"legal_review_conclusion"`
	Policy                     json.RawMessage `json:"policy"`
	PolicySHA256               string          `json:"policy_sha256"`
	PolicyVersion              string          `json:"policy_version"`
}

const guardianActivationPolicyJSON = `{"contract":"guardian-authority-v2","evidence_categories":["CLUB_REGISTRATION_RECORD","IN_PERSON_ID_AND_CIVIL_RECORD","COURT_OR_LEGAL_AUTHORITY"],"reason_codes":["RELATIONSHIP_CONFIRMED","EVIDENCE_INSUFFICIENT","AUTHORITY_NOT_ESTABLISHED","CONFLICT","AUTHORITY_CHANGED","UNCERTAINTY","AUTHORITY_ENDED","ELIGIBILITY_ENDED","REVIEW_EXPIRED","MAJORITY_REACHED"],"validity_days":365,"review_days":365,"fresh_authentication_minutes":60,"invitation_validity_days":30,"submission_account_limit_24h":10,"submission_network_limit_24h":100,"renewal_period_months":12,"adult_age_years":18,"guardians_per_minor":1}`

func guardianActivationFixture(t *testing.T, actor uuid.UUID, database string) ([]byte, []byte) {
	t.Helper()
	location, err := time.LoadLocation("Europe/Lisbon")
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(location)
	policy := []byte(guardianActivationPolicyJSON)
	policyDigest := sha256.Sum256(policy)
	approval := guardianActivationApproval{
		Contract: "mycfc/guardian-authority-policy-approval/v1", AuthorizedOperatorActorRef: actor, ExpectedDatabase: database,
		ControllerRole: "CLUB_DIRECTION", ControllerApprovalRef: "test/controller/approval",
		ControllerApprovedOn: today.Format(time.DateOnly), EffectiveOn: today.Format(time.DateOnly), ReviewDueOn: today.AddDate(1, 0, 0).Format(time.DateOnly),
		LegalReviewerRef: "test/legal/reviewer", LegalReviewRef: "test/legal/approval", LegalReviewedOn: today.Format(time.DateOnly),
		LegalReviewConclusion: "APPROVED", Policy: policy, PolicySHA256: hex.EncodeToString(policyDigest[:]), PolicyVersion: "guardian-v2-integration-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
	}
	payload, err := json.Marshal(approval)
	if err != nil {
		t.Fatal(err)
	}
	return payload, policy
}

func installGuardianActivationInventory(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	if _, err := tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS mycfc_meta; CREATE TABLE IF NOT EXISTS mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	for _, version := range EmbeddedMigrationInventory() {
		if _, err := tx.Exec(ctx, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES($1) ON CONFLICT DO NOTHING`, version); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGuardianActivationIsAtomicIdempotentAndEmergencyDisableRevokesAccess(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	installGuardianActivationInventory(t, ctx, tx)
	// Keep this proof repeatable even when a developer reruns it against a
	// database containing fixtures from another integration test. The changes
	// are transaction-local and do not weaken preflight's stale-state checks.
	if _, err = tx.Exec(ctx, `UPDATE guardian_authority_relationships SET state='EXPIRED',conflict=false,conflict_actor_ref=NULL
		WHERE state IN('VERIFIED','SUSPENDED');
		DELETE FROM sessions session USING users subject
			WHERE session.user_id=subject.id AND session.subject_indexed AND subject.is_dependent;
		UPDATE users SET minor_login_id=NULL,password_hash=NULL
			WHERE is_dependent AND (minor_login_id IS NOT NULL OR password_hash IS NOT NULL);
		UPDATE guardian_application_intake_release SET enabled=false,policy_version=NULL,policy_sha256=NULL,approval_sha256=NULL,
		image_digest=NULL,schema_migration_digest=NULL,enabled_by=NULL,enabled_at=NULL WHERE singleton;
		UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled`); err != nil {
		t.Fatal(err)
	}

	actor, guardian, subject := uuid.New(), uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,email_verified_at,date_of_birth) VALUES
		($1,'Activation administrator',$2,'hash',clock_timestamp(),'1990-01-01'),
		($3,'Activation guardian',$4,'hash',clock_timestamp(),'1990-01-01')`, actor, "activation-admin-"+uuid.NewString()+"@example.test", guardian, "activation-guardian-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,minor_login_id,guardian_id,is_dependent,date_of_birth)
		VALUES($1,'Activation dependent',NULL,NULL,NULL,NULL,true,'2012-01-01')`, subject); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor); err != nil {
		t.Fatal(err)
	}
	approval, policy := guardianActivationFixture(t, actor, conn.Config().Database)
	image := "sha256:" + strings.Repeat("a", 64)
	database := conn.Config().Database
	if _, err = tx.Exec(ctx, `UPDATE guardian_ops.runtime_release_binding SET database_name=$1,image_digest=$2,schema_migration_digest=$3,generation=1,bound_at=clock_timestamp() WHERE singleton`, database, image, EmbeddedMigrationDigest()); err != nil {
		t.Fatal(err)
	}

	var ready, already bool
	var version string
	var policyHash, approvalHash []byte
	var stale, credentials, sessions int64
	if err = tx.QueryRow(ctx, `SELECT * FROM guardian_ops.preflight($1,$2,$3,$4,$5,$6,$7)`, actor, string(approval), approval, policy, image, EmbeddedMigrationDigest(), database).
		Scan(&ready, &already, &version, &policyHash, &approvalHash, &stale, &credentials, &sessions); err != nil {
		t.Fatal(err)
	}
	if !ready || already || stale != 0 || credentials != 0 || sessions != 0 {
		t.Fatalf("preflight ready=%v already=%v stale=%d credentials=%d sessions=%d", ready, already, stale, credentials, sessions)
	}
	var changed bool
	if err = tx.QueryRow(ctx, `SELECT * FROM guardian_ops.enable($1,$2,$3,$4,$5,$6,$7)`, actor, string(approval), approval, policy, image, EmbeddedMigrationDigest(), database).
		Scan(&changed, &version, &policyHash, &approvalHash); err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first enable did not change state")
	}
	if err = tx.QueryRow(ctx, `SELECT * FROM guardian_ops.enable($1,$2,$3,$4,$5,$6,$7)`, actor, string(approval), approval, policy, image, EmbeddedMigrationDigest(), database).
		Scan(&changed, &version, &policyHash, &approvalHash); err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("repeated enable was not idempotent")
	}

	if _, err = tx.Exec(ctx, `UPDATE users SET password_hash='minor-hash',minor_login_id=$2 WHERE id=$1`, subject, "activation-minor-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label,state,policy_version,verified_at,verified_by,verified_until,review_due_at)
		VALUES($1,$2,'Activation dependent','VERIFIED',$3,clock_timestamp(),$4,clock_timestamp()+interval '365 days',clock_timestamp()+interval '300 days')`, guardian, subject, version, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($1,'session',clock_timestamp()+interval '1 hour',$2,true)`, "activation-session-"+uuid.NewString(), subject); err != nil {
		t.Fatal(err)
	}
	var disabledVersion *string
	var relationships int64
	if err = tx.QueryRow(ctx, `SELECT * FROM guardian_ops.disable($1,$2)`, actor, database).Scan(&disabledVersion, &relationships, &credentials, &sessions); err != nil {
		t.Fatal(err)
	}
	if disabledVersion == nil || relationships != 1 || credentials != 1 || sessions != 1 {
		t.Fatalf("disable version=%v relationships=%d credentials=%d sessions=%d", disabledVersion, relationships, credentials, sessions)
	}
	var state string
	var credentialPresent, sessionPresent bool
	if err = tx.QueryRow(ctx, `SELECT relationship.state,subject.minor_login_id IS NOT NULL OR subject.password_hash IS NOT NULL,
		EXISTS(SELECT 1 FROM sessions WHERE user_id=$1) FROM guardian_authority_relationships relationship JOIN users subject ON subject.id=relationship.subject_user_id WHERE relationship.subject_user_id=$1`, subject).
		Scan(&state, &credentialPresent, &sessionPresent); err != nil {
		t.Fatal(err)
	}
	if state != "EXPIRED" || credentialPresent || sessionPresent {
		t.Fatalf("emergency postcondition state=%s credential=%v session=%v", state, credentialPresent, sessionPresent)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT wrong_disable_actor`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT * FROM guardian_ops.disable($1,$2)`, uuid.New(), database); err == nil {
		t.Fatal("unbound emergency-disable actor accepted")
	}
	if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT wrong_disable_actor`); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	if err = tx.QueryRow(ctx, `SELECT * FROM guardian_ops.disable($1,$2)`, actor, database).Scan(&disabledVersion, &relationships, &credentials, &sessions); err != nil {
		t.Fatal(err)
	}
	var disableEvents int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM guardian_application_intake_release_events WHERE action='DISABLED' AND actor_ref=$1`, actor).Scan(&disableEvents); err != nil {
		t.Fatal(err)
	}
	if disableEvents != 2 {
		t.Fatalf("repeatable disable events=%d want 2", disableEvents)
	}
}

func TestGuardianActivationPreflightBlocksStaleRelationshipAndWrongSchema(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	installGuardianActivationInventory(t, ctx, tx)
	actor := uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Blocked activation administrator',$2,'hash','1990-01-01')`, actor, "blocked-activation-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor); err != nil {
		t.Fatal(err)
	}
	approval, policy := guardianActivationFixture(t, actor, conn.Config().Database)
	var ready, already bool
	var version string
	var policyHash, approvalHash []byte
	var stale, credentials, sessions int64
	if err = tx.QueryRow(ctx, `SELECT * FROM guardian_ops.preflight($1,$2,$3,$4,$5,$6,$7)`, actor, string(approval), approval, policy, "sha256:"+strings.Repeat("b", 64), strings.Repeat("0", 64), conn.Config().Database).
		Scan(&ready, &already, &version, &policyHash, &approvalHash, &stale, &credentials, &sessions); err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("wrong embedded schema digest passed preflight")
	}
}

func TestGuardianRuntimeImageMismatchImmediatelyBlocksAuthorityAndReleaseDisablesBeforeSwitch(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	installGuardianActivationInventory(t, ctx, tx)
	if _, err = tx.Exec(ctx, `UPDATE guardian_authority_relationships SET state='EXPIRED',conflict=false,conflict_actor_ref=NULL WHERE state IN('VERIFIED','SUSPENDED');
		UPDATE guardian_application_intake_release SET enabled=false,policy_version=NULL,policy_sha256=NULL,approval_sha256=NULL,image_digest=NULL,schema_migration_digest=NULL,enabled_by=NULL,enabled_at=NULL WHERE singleton;
		UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled;
		DELETE FROM sessions WHERE subject_indexed;
		UPDATE users SET minor_login_id=NULL,password_hash=NULL WHERE is_dependent`); err != nil {
		t.Fatal(err)
	}
	actor, guardian, subject := uuid.New(), uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,email_verified_at,date_of_birth) VALUES
		($1,'Image administrator',$2,'hash',clock_timestamp(),'1990-01-01'),
		($3,'Image guardian',$4,'hash',clock_timestamp(),'1990-01-01')`, actor, "image-admin-"+uuid.NewString()+"@example.test", guardian, "image-guardian-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,is_dependent,date_of_birth) VALUES($1,'Image dependent',true,'2012-01-01')`, subject); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor); err != nil {
		t.Fatal(err)
	}
	database := conn.Config().Database
	imageA := "sha256:" + strings.Repeat("a", 64)
	imageB := "sha256:" + strings.Repeat("b", 64)
	if _, err = tx.Exec(ctx, `UPDATE guardian_ops.runtime_release_binding SET database_name=$1,image_digest=$2,schema_migration_digest=$3,generation=1,bound_at=clock_timestamp() WHERE singleton`, database, imageA, EmbeddedMigrationDigest()); err != nil {
		t.Fatal(err)
	}
	approval, policy := guardianActivationFixture(t, actor, database)
	var changed bool
	var version string
	var policyHash, approvalHash []byte
	if err = tx.QueryRow(ctx, `SELECT * FROM guardian_ops.enable($1,$2,$3,$4,$5,$6,$7)`, actor, string(approval), approval, policy, imageA, EmbeddedMigrationDigest(), database).Scan(&changed, &version, &policyHash, &approvalHash); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label,state,policy_version,verified_at,verified_by,verified_until,review_due_at)
		VALUES($1,$2,'Image dependent','VERIFIED',$3,clock_timestamp(),$4,clock_timestamp()+interval '365 days',clock_timestamp()+interval '300 days')`, guardian, subject, version, actor); err != nil {
		t.Fatal(err)
	}
	var current bool
	if err = tx.QueryRow(ctx, `SELECT guardian_authority_current($1,$2)`, guardian, subject).Scan(&current); err != nil || !current {
		t.Fatalf("image A authority current=%t err=%v", current, err)
	}
	if _, err = tx.Exec(ctx, `UPDATE guardian_ops.runtime_release_binding SET image_digest=$1,generation=generation+1,bound_at=clock_timestamp() WHERE singleton`, imageB); err != nil {
		t.Fatal(err)
	}
	var state string
	var databaseCurrent, migrationsCurrent, imageCurrent, contractCurrent bool
	var statusVersion *string
	var statusPolicy, statusApproval []byte
	var pending, currentCount, stale, credentials, sessions, invitations int64
	if err = tx.QueryRow(ctx, `SELECT * FROM guardian_ops.status($1,$2,$3)`, database, imageB, EmbeddedMigrationDigest()).Scan(
		&state, &databaseCurrent, &migrationsCurrent, &imageCurrent, &contractCurrent, &statusVersion, &statusPolicy, &statusApproval,
		&pending, &currentCount, &stale, &credentials, &sessions, &invitations); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT guardian_authority_current($1,$2)`, guardian, subject).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if state != "BLOCKED" || current || currentCount != 0 || imageCurrent {
		t.Fatalf("mismatch state=%s current=%t current_count=%d image_current=%t", state, current, currentCount, imageCurrent)
	}
	if _, err = tx.Exec(ctx, `UPDATE guardian_ops.runtime_release_binding SET image_digest=$1 WHERE singleton`, imageA); err != nil {
		t.Fatal(err)
	}
	var generation, relationships int64
	if err = tx.QueryRow(ctx, `SELECT * FROM guardian_ops.release_disable_and_bind($1,$2,$3)`, imageB, EmbeddedMigrationDigest(), database).Scan(&generation, &relationships, &credentials, &sessions); err != nil {
		t.Fatal(err)
	}
	var gate, policyEnabled bool
	if err = tx.QueryRow(ctx, `SELECT enabled,(SELECT EXISTS(SELECT 1 FROM guardian_authority_policies WHERE enabled)) FROM guardian_application_intake_release WHERE singleton`).Scan(&gate, &policyEnabled); err != nil {
		t.Fatal(err)
	}
	if gate || policyEnabled || relationships != 1 || generation < 2 {
		t.Fatalf("release binding generation=%d gate=%t policy=%t relationships=%d", generation, gate, policyEnabled, relationships)
	}
}

func TestGuardianActivationDatabaseRejectsNonCanonicalBytesAndWrongDatabaseBinding(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	installGuardianActivationInventory(t, ctx, tx)
	if _, err = tx.Exec(ctx, `UPDATE guardian_authority_relationships SET state='EXPIRED',conflict=false,conflict_actor_ref=NULL WHERE state IN('VERIFIED','SUSPENDED');
		UPDATE guardian_application_intake_release SET enabled=false,policy_version=NULL,policy_sha256=NULL,approval_sha256=NULL,image_digest=NULL,schema_migration_digest=NULL,enabled_by=NULL,enabled_at=NULL WHERE singleton;
		UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled;
		DELETE FROM sessions WHERE subject_indexed;
		UPDATE users SET minor_login_id=NULL,password_hash=NULL WHERE is_dependent`); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Canonical administrator',$2,'hash','1990-01-01')`, actor, "canonical-admin-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor); err != nil {
		t.Fatal(err)
	}
	database := conn.Config().Database
	image := "sha256:" + strings.Repeat("c", 64)
	if _, err = tx.Exec(ctx, `UPDATE guardian_ops.runtime_release_binding SET database_name=$1,image_digest=$2,schema_migration_digest=$3,generation=1,bound_at=clock_timestamp() WHERE singleton`, database, image, EmbeddedMigrationDigest()); err != nil {
		t.Fatal(err)
	}
	approval, policy := guardianActivationFixture(t, actor, database)
	call := func(approvalText string, approvalRaw, policyRaw []byte) error {
		var ready, already bool
		var version string
		var policyHash, approvalHash []byte
		var relationships, credentials, sessions int64
		return tx.QueryRow(ctx, `SELECT * FROM guardian_ops.preflight($1,$2,$3,$4,$5,$6,$7)`, actor, approvalText, approvalRaw, policyRaw, image, EmbeddedMigrationDigest(), database).
			Scan(&ready, &already, &version, &policyHash, &approvalHash, &relationships, &credentials, &sessions)
	}
	if err = call(string(approval), approval, policy); err != nil {
		t.Fatalf("canonical approval rejected: %v", err)
	}
	var object map[string]any
	if err = json.Unmarshal(approval, &object); err != nil {
		t.Fatal(err)
	}
	reordered, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	var wrongDatabase guardianActivationApproval
	if err = json.Unmarshal(approval, &wrongDatabase); err != nil {
		t.Fatal(err)
	}
	wrongDatabase.ExpectedDatabase = "another_database"
	wrongDatabaseRaw, err := json.Marshal(wrongDatabase)
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]struct {
		text   string
		raw    []byte
		policy []byte
	}{
		"approval whitespace": {string(approval) + " ", append(append([]byte{}, approval...), ' '), policy},
		"approval order":      {string(reordered), reordered, policy},
		"policy whitespace":   {string(approval), approval, append(append([]byte{}, policy...), ' ')},
		"database identity":   {string(wrongDatabaseRaw), wrongDatabaseRaw, policy},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tx.Exec(ctx, `SAVEPOINT canonical_rejection`); err != nil {
				t.Fatal(err)
			}
			callErr := call(input.text, input.raw, input.policy)
			var databaseError *pgconn.PgError
			if !errors.As(callErr, &databaseError) || databaseError.Code != "22023" || databaseError.Message != "guardian_activation_approval_rejected" {
				t.Fatalf("unsafe/non-exact rejection: %v", callErr)
			}
			if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT canonical_rejection`); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGuardianPreflightBlocksAllExistingAuthorityCredentialsAndSessionsButAllowsPending(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	installGuardianActivationInventory(t, ctx, tx)
	if _, err = tx.Exec(ctx, `UPDATE guardian_authority_relationships SET state='EXPIRED',conflict=false,conflict_actor_ref=NULL WHERE state IN('VERIFIED','SUSPENDED');
		UPDATE guardian_application_intake_release SET enabled=false,policy_version=NULL,policy_sha256=NULL,approval_sha256=NULL,image_digest=NULL,schema_migration_digest=NULL,enabled_by=NULL,enabled_at=NULL WHERE singleton;
		UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled;
		DELETE FROM sessions WHERE subject_indexed;
		UPDATE users SET minor_login_id=NULL,password_hash=NULL WHERE is_dependent`); err != nil {
		t.Fatal(err)
	}
	actor, guardian, subject, dangling := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES
		($1,'Inventory administrator',$2,'hash','1990-01-01'),($3,'Inventory guardian',$4,'hash','1990-01-01')`, actor, "inventory-admin-"+uuid.NewString()+"@example.test", guardian, "inventory-guardian-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,is_dependent,date_of_birth) VALUES($1,'Pending dependent',true,'2012-01-01'),($2,'Dangling dependent',true,'2013-01-01')`, subject, dangling); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label,state) VALUES($1,$2,'Pending dependent','PENDING')`, guardian, subject); err != nil {
		t.Fatal(err)
	}
	database := conn.Config().Database
	image := "sha256:" + strings.Repeat("d", 64)
	if _, err = tx.Exec(ctx, `UPDATE guardian_ops.runtime_release_binding SET database_name=$1,image_digest=$2,schema_migration_digest=$3,generation=1,bound_at=clock_timestamp() WHERE singleton`, database, image, EmbeddedMigrationDigest()); err != nil {
		t.Fatal(err)
	}
	approval, policy := guardianActivationFixture(t, actor, database)
	var approvalDocument guardianActivationApproval
	if err = json.Unmarshal(approval, &approvalDocument); err != nil {
		t.Fatal(err)
	}
	version := approvalDocument.PolicyVersion
	if _, err = tx.Exec(ctx, `INSERT INTO guardian_authority_policies(version,evidence_types,reason_codes,validity_days,review_days,adopted_at,adopted_by,enabled)
		VALUES($1,ARRAY['CLUB_REGISTRATION_RECORD','IN_PERSON_ID_AND_CIVIL_RECORD','COURT_OR_LEGAL_AUTHORITY'],
		ARRAY['RELATIONSHIP_CONFIRMED','EVIDENCE_INSUFFICIENT','AUTHORITY_NOT_ESTABLISHED','CONFLICT','AUTHORITY_CHANGED','UNCERTAINTY','AUTHORITY_ENDED','ELIGIBILITY_ENDED','REVIEW_EXPIRED','MAJORITY_REACHED'],365,365,clock_timestamp(),$2,false)`, version, actor); err != nil {
		t.Fatal(err)
	}
	check := func() (bool, int64, int64, int64) {
		var ready, already bool
		var returnedVersion string
		var policyHash, approvalHash []byte
		var relationships, credentials, sessions int64
		if err := tx.QueryRow(ctx, `SELECT * FROM guardian_ops.preflight($1,$2,$3,$4,$5,$6,$7)`, actor, string(approval), approval, policy, image, EmbeddedMigrationDigest(), database).
			Scan(&ready, &already, &returnedVersion, &policyHash, &approvalHash, &relationships, &credentials, &sessions); err != nil {
			t.Fatal(err)
		}
		return ready, relationships, credentials, sessions
	}
	if ready, relationships, credentials, sessions := check(); !ready || relationships != 0 || credentials != 0 || sessions != 0 {
		t.Fatalf("pending blocked ready=%t relationships=%d credentials=%d sessions=%d", ready, relationships, credentials, sessions)
	}
	if _, err = tx.Exec(ctx, `UPDATE guardian_authority_relationships SET state='VERIFIED',policy_version=$1,verified_at=clock_timestamp(),verified_by=$2,
		verified_until=clock_timestamp()+interval '365 days',review_due_at=clock_timestamp()+interval '300 days' WHERE subject_user_id=$3`, version, actor, subject); err != nil {
		t.Fatal(err)
	}
	if ready, relationships, _, _ := check(); ready || relationships != 1 {
		t.Fatalf("same-version authority accepted ready=%t relationships=%d", ready, relationships)
	}
	otherVersion := "guardian-v2-other-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err = tx.Exec(ctx, `INSERT INTO guardian_authority_policies(version,evidence_types,reason_codes,validity_days,review_days,adopted_at,adopted_by,enabled)
		SELECT $1,evidence_types,reason_codes,validity_days,review_days,clock_timestamp(),$2,false FROM guardian_authority_policies WHERE version=$3`, otherVersion, actor, version); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE guardian_authority_relationships SET policy_version=$1 WHERE subject_user_id=$2`, otherVersion, subject); err != nil {
		t.Fatal(err)
	}
	if ready, relationships, _, _ := check(); ready || relationships != 1 {
		t.Fatalf("different-version authority accepted ready=%t relationships=%d", ready, relationships)
	}
	if _, err = tx.Exec(ctx, `UPDATE guardian_authority_relationships SET state='PENDING',policy_version=NULL,verified_at=NULL,verified_by=NULL,verified_until=NULL,review_due_at=NULL WHERE subject_user_id=$1`, subject); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET minor_login_id='dangling-login',password_hash='dangling-hash' WHERE id=$1`, dangling); err != nil {
		t.Fatal(err)
	}
	if ready, relationships, credentials, _ := check(); ready || relationships != 0 || credentials != 1 {
		t.Fatalf("dangling credential accepted ready=%t relationships=%d credentials=%d", ready, relationships, credentials)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET minor_login_id=NULL,password_hash=NULL WHERE id=$1`, dangling); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($1,'dangling',clock_timestamp()+interval '1 hour',$2,true)`, "dangling-session-"+uuid.NewString(), dangling); err != nil {
		t.Fatal(err)
	}
	if ready, relationships, credentials, sessions := check(); ready || relationships != 0 || credentials != 0 || sessions != 1 {
		t.Fatalf("dangling session accepted ready=%t relationships=%d credentials=%d sessions=%d", ready, relationships, credentials, sessions)
	}
}

func TestGuardianActivationConcurrentEnableAndDisableSerializeAcrossConnections(t *testing.T) {
	ctx := context.Background()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	control, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close(ctx)
	config1, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config1.RuntimeParams["application_name"] = "guardian-concurrency-first-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	config2 := config1.Copy()
	config2.RuntimeParams["application_name"] = "guardian-concurrency-second-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	first, err := pgx.ConnectConfig(ctx, config1)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close(ctx)
	second, err := pgx.ConnectConfig(ctx, config2)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(ctx)
	setupTx, err := control.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	installGuardianActivationInventory(t, ctx, setupTx)
	if err = setupTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = control.Exec(ctx, `UPDATE guardian_authority_relationships SET state='EXPIRED',conflict=false,conflict_actor_ref=NULL WHERE state IN('VERIFIED','SUSPENDED');
		UPDATE guardian_application_intake_release SET enabled=false,policy_version=NULL,policy_sha256=NULL,approval_sha256=NULL,image_digest=NULL,schema_migration_digest=NULL,enabled_by=NULL,enabled_at=NULL WHERE singleton;
		UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled;
		DELETE FROM sessions WHERE subject_indexed;
		UPDATE users SET minor_login_id=NULL,password_hash=NULL WHERE is_dependent`); err != nil {
		t.Fatal(err)
	}
	actor, guardian, subject := uuid.New(), uuid.New(), uuid.New()
	if _, err = control.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES
		($1,'Concurrent administrator',$2,'hash','1990-01-01'),($3,'Concurrent guardian',$4,'hash','1990-01-01')`, actor, "concurrent-admin-"+uuid.NewString()+"@example.test", guardian, "concurrent-guardian-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = control.Exec(ctx, `INSERT INTO users(id,name,is_dependent,date_of_birth) VALUES($1,'Concurrent dependent',true,'2012-01-01')`, subject); err != nil {
		t.Fatal(err)
	}
	if _, err = control.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor); err != nil {
		t.Fatal(err)
	}
	database := control.Config().Database
	image := "sha256:" + strings.Repeat("e", 64)
	if _, err = control.Exec(ctx, `UPDATE guardian_ops.runtime_release_binding SET database_name=$1,image_digest=$2,schema_migration_digest=$3,generation=generation+1,bound_at=clock_timestamp() WHERE singleton`, database, image, EmbeddedMigrationDigest()); err != nil {
		t.Fatal(err)
	}
	approval, policy := guardianActivationFixture(t, actor, database)
	type enableResult struct {
		changed bool
		version string
		err     error
	}
	enable := func(connection interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	}) enableResult {
		var result enableResult
		var policyHash, approvalHash []byte
		result.err = connection.QueryRow(ctx, `SELECT * FROM guardian_ops.enable($1,$2,$3,$4,$5,$6,$7)`, actor, string(approval), approval, policy, image, EmbeddedMigrationDigest(), database).
			Scan(&result.changed, &result.version, &policyHash, &approvalHash)
		return result
	}
	waitForAdvisory := func(applicationName string) {
		deadline := time.Now().Add(3 * time.Second)
		for {
			var waiting bool
			if err := control.QueryRow(ctx, `SELECT COALESCE(bool_or(wait_event='advisory'),false) FROM pg_stat_activity WHERE application_name=$1`, applicationName).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("connection %s did not wait on advisory lock", applicationName)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	tx, err := first.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority-policy',0))`); err != nil {
		t.Fatal(err)
	}
	secondEnable := make(chan enableResult, 1)
	go func() { secondEnable <- enable(second) }()
	waitForAdvisory(config2.RuntimeParams["application_name"])
	firstResult := enable(tx)
	if firstResult.err != nil {
		t.Fatal(firstResult.err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	secondResult := <-secondEnable
	if secondResult.err != nil || !firstResult.changed || secondResult.changed || firstResult.version != secondResult.version {
		t.Fatalf("concurrent enable first=%+v second=%+v", firstResult, secondResult)
	}
	var enabledEvents int
	if err = control.QueryRow(ctx, `SELECT count(*) FROM guardian_application_intake_release_events WHERE action='ENABLED' AND policy_version=$1`, firstResult.version).Scan(&enabledEvents); err != nil || enabledEvents != 1 {
		t.Fatalf("enable events=%d err=%v", enabledEvents, err)
	}
	if _, err = control.Exec(ctx, `UPDATE users SET minor_login_id=$2,password_hash='minor-hash' WHERE id=$1`, subject, "concurrent-minor-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err = control.Exec(ctx, `INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label,state,policy_version,verified_at,verified_by,verified_until,review_due_at)
		VALUES($1,$2,'Concurrent dependent','VERIFIED',$3,clock_timestamp(),$4,clock_timestamp()+interval '365 days',clock_timestamp()+interval '300 days')`, guardian, subject, firstResult.version, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = control.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($1,'concurrent',clock_timestamp()+interval '1 hour',$2,true)`, "concurrent-session-"+uuid.NewString(), subject); err != nil {
		t.Fatal(err)
	}
	tx, err = first.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority-policy',0))`); err != nil {
		t.Fatal(err)
	}
	type disableResult struct {
		version                              *string
		relationships, credentials, sessions int64
		err                                  error
	}
	secondDisable := make(chan disableResult, 1)
	go func() {
		var result disableResult
		result.err = second.QueryRow(ctx, `SELECT * FROM guardian_ops.disable($1,$2)`, actor, database).Scan(&result.version, &result.relationships, &result.credentials, &result.sessions)
		secondDisable <- result
	}()
	waitForAdvisory(config2.RuntimeParams["application_name"])
	firstResult = enable(tx)
	if firstResult.err != nil || firstResult.changed {
		t.Fatalf("concurrent idempotent enable=%+v", firstResult)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	disabled := <-secondDisable
	if disabled.err != nil || disabled.relationships != 1 || disabled.credentials != 1 || disabled.sessions != 1 {
		t.Fatalf("concurrent disable=%+v", disabled)
	}
	var state string
	var hasCredential, hasSession, gateEnabled bool
	if err = control.QueryRow(ctx, `SELECT relationship.state,(subject.minor_login_id IS NOT NULL OR subject.password_hash IS NOT NULL),
		EXISTS(SELECT 1 FROM sessions WHERE user_id=$1), (SELECT enabled FROM guardian_application_intake_release WHERE singleton)
		FROM guardian_authority_relationships relationship JOIN users subject ON subject.id=relationship.subject_user_id WHERE relationship.subject_user_id=$1`, subject).
		Scan(&state, &hasCredential, &hasSession, &gateEnabled); err != nil {
		t.Fatal(err)
	}
	if state != "EXPIRED" || hasCredential || hasSession || gateEnabled {
		t.Fatalf("post-disable state=%s credential=%t session=%t gate=%t", state, hasCredential, hasSession, gateEnabled)
	}
}
