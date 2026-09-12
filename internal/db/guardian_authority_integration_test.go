//go:build integration

package db

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type guardianFixtureDB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func activateGuardianPolicyFixture(t *testing.T, ctx context.Context, fixtureDB guardianFixtureDB, actorID uuid.UUID, version string) {
	t.Helper()
	if _, err := fixtureDB.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS mycfc_meta; CREATE TABLE IF NOT EXISTS mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range EmbeddedMigrationInventory() {
		if _, err := fixtureDB.Exec(ctx, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES($1) ON CONFLICT DO NOTHING`, migration); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixtureDB.Exec(ctx, `UPDATE guardian_application_intake_release SET enabled=false,policy_version=NULL,policy_sha256=NULL,approval_sha256=NULL,image_digest=NULL,schema_migration_digest=NULL,enabled_by=NULL,enabled_at=NULL WHERE singleton;
		UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureDB.Exec(ctx, `INSERT INTO guardian_authority_policy_approvals(policy_version,policy_sha256,approval_sha256,approval_contract,approval_canonical,
		expected_database,authorized_operator_actor_ref,controller_role,controller_approval_reference,controller_approved_on,effective_on,review_due_on,
		legal_reviewer_reference,legal_review_reference,legal_reviewed_on,legal_review_conclusion,bound_image_digest,bound_schema_migration_digest,bound_by)
		VALUES($1::text,digest(convert_to($1::text,'UTF8'),'sha256'),digest(convert_to('approval/'||$1::text,'UTF8'),'sha256'),'mycfc/guardian-authority-policy-approval/v1',convert_to('{}','UTF8'),
		current_database(),$2,'CLUB_DIRECTION','integration/controller',CURRENT_DATE,CURRENT_DATE,CURRENT_DATE+365,'integration/legal-reviewer','integration/legal-review',CURRENT_DATE,'APPROVED',
		'sha256:'||repeat('a',64),$3,$2)`, version, actorID, EmbeddedMigrationDigest()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureDB.Exec(ctx, `UPDATE guardian_authority_policies SET enabled=true,enabled_at=clock_timestamp(),enabled_by=$2 WHERE version=$1`, version, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureDB.Exec(ctx, `UPDATE guardian_ops.runtime_release_binding SET database_name=current_database(),image_digest='sha256:'||repeat('a',64),schema_migration_digest=$1,generation=generation+1,bound_at=clock_timestamp() WHERE singleton`, EmbeddedMigrationDigest()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureDB.Exec(ctx, `UPDATE guardian_application_intake_release gate SET enabled=true,policy_version=$1,policy_sha256=approval.policy_sha256,approval_sha256=approval.approval_sha256,
		image_digest=approval.bound_image_digest,schema_migration_digest=approval.bound_schema_migration_digest,enabled_by=$2,enabled_at=clock_timestamp()
		FROM guardian_authority_policy_approvals approval WHERE gate.singleton AND approval.policy_version=$1`, version, actorID); err != nil {
		t.Fatal(err)
	}
}

func insertVerifiedDependentFixture(t *testing.T, ctx context.Context, db guardianFixtureDB, guardianID, subjectID uuid.UUID, name string, born any) {
	t.Helper()
	policy := "guardian-test-" + uuid.NewString()
	if _, err := db.Exec(ctx, `UPDATE guardian_authority_policies
		SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO guardian_authority_policies
		(version,evidence_types,reason_codes,validity_days,review_days,adopted_at,adopted_by,enabled,enabled_at,enabled_by)
		VALUES($1,'{CIVIL_REGISTRY}','{CONFLICT,LOSS,CHANGE}',365,180,clock_timestamp(),$2,true,clock_timestamp(),$2)`, policy, guardianID); err != nil {
		t.Fatal(err)
	}
	activateGuardianPolicyFixture(t, ctx, db, guardianID, policy)
	if _, err := db.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,guardian_id,is_dependent,date_of_birth)
		VALUES($1,$2,NULL,NULL,NULL,true,$3)`, subjectID, name, born); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `WITH relationship AS (
		INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label,state,version,policy_version,
		 verified_at,verified_by,verified_until,review_due_at,created_at,updated_at)
		VALUES($1,$2,$3,'VERIFIED',2,$4,clock_timestamp(),$1,clock_timestamp()+interval '365 days',clock_timestamp()+interval '180 days',clock_timestamp(),clock_timestamp())
		RETURNING id,created_at)
		INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,policy_version,
		 evidence_type,evidence_reference,evidence_sha256,occurred_at,verified_until,review_due_at)
		SELECT id,2,$1,'VERIFIER','VERIFIED','PENDING','VERIFIED',$4,'CIVIL_REGISTRY','fixture/'||id::text,digest(id::text,'sha256'),created_at,
		 created_at+interval '365 days',created_at+interval '180 days' FROM relationship`, guardianID, subjectID, name, policy); err != nil {
		t.Fatal(err)
	}
}

func TestGuardianAuthorityPendingVerificationExpiryConflictAndConcurrency(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	q := dbgen.New(conn)
	if _, err = conn.Exec(ctx, `ALTER TABLE guardian_application_intake_release DROP CONSTRAINT IF EXISTS guardian_application_intake_release_disabled_check`); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `UPDATE guardian_authority_policies
		SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled`); err != nil {
		t.Fatal(err)
	}

	guardianID, verifierID, grantorID := uuid.New(), uuid.New(), uuid.New()
	for id, label := range map[uuid.UUID]string{guardianID: "Responsável em teste", verifierID: "Verificadora em teste", grantorID: "Concedente em teste"} {
		if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth) VALUES($1,$2,$3,clock_timestamp(),'hash','1980-01-01')`, id, label, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	eligibilitySeasonID := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Guardian authority test',CURRENT_DATE-1,CURRENT_DATE+1)`, eligibilitySeasonID, "guardian-"+eligibilitySeasonID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on) SELECT $1,$2,id,CURRENT_DATE FROM programmes WHERE code='Leisure'`, guardianID, eligibilitySeasonID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id)
		SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, grantorID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id)
		SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, verifierID); err != nil {
		t.Fatal(err)
	}
	if can, err := q.CanVerifyGuardianAuthority(ctx, verifierID); err != nil || can {
		t.Fatalf("verification capability before policy/grant = %v, %v", can, err)
	}
	noPolicyName := "Sem política " + uuid.NewString()
	if _, err = q.CreateDependentUser(ctx, dbgen.CreateDependentUserParams{Name: noPolicyName, GuardianID: guardianID,
		DateOfBirth: pgtype.Date{Time: time.Now().AddDate(-10, 0, 0), Valid: true}}); err == nil {
		t.Fatal("dependent creation succeeded without an adopted enabled policy")
	}
	var usersBefore int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM users WHERE name=$1`, noPolicyName).Scan(&usersBefore); err != nil || usersBefore != 0 {
		t.Fatalf("failed creation left users=%d err=%v", usersBefore, err)
	}

	policy := "guardian-authority-" + uuid.NewString()
	if _, err = q.AdoptGuardianAuthorityPolicy(ctx, dbgen.AdoptGuardianAuthorityPolicyParams{
		ActorID: grantorID, Version: policy + "-missing-conflict", EvidenceTypes: "{CIVIL_REGISTRY}",
		ReasonCodes: "{LOSS,CHANGE}", ValidityDays: 30, ReviewDays: 10,
	}); err == nil {
		t.Fatal("policy without the reserved CONFLICT reason was adopted")
	}
	if _, err = q.AdoptGuardianAuthorityPolicy(ctx, dbgen.AdoptGuardianAuthorityPolicyParams{
		ActorID: grantorID, Version: policy + "-oversized", EvidenceTypes: "{CIVIL_REGISTRY_CODE_THAT_IS_LONGER_THAN_FORTY_CHARACTERS}",
		ReasonCodes: "{CONFLICT}", ValidityDays: 30, ReviewDays: 10,
	}); err == nil {
		t.Fatal("policy with an oversized evidence code was adopted")
	}
	if _, err = q.AdoptGuardianAuthorityPolicy(ctx, dbgen.AdoptGuardianAuthorityPolicyParams{
		ActorID: grantorID, Version: policy, EvidenceTypes: "{CIVIL_REGISTRY}",
		ReasonCodes: "{CONFLICT,LOSS,CHANGE}", ValidityDays: 30, ReviewDays: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.SetGuardianAuthorityPolicyEnabled(ctx, dbgen.SetGuardianAuthorityPolicyEnabledParams{
		ActorID: grantorID, Version: policy, Enabled: true,
	}); err == nil {
		t.Fatal("legacy policy enablement remained available")
	} else {
		var databaseError *pgconn.PgError
		if !errors.As(err, &databaseError) || databaseError.Message != "guardian_authority_policy_replacement_required" {
			t.Fatalf("legacy policy enable error=%v", err)
		}
	}
	// This synthetic integration fixture bypasses the deliberately closed
	// operator routine so the existing verifier lifecycle remains testable.
	if _, err = conn.Exec(ctx, `WITH enabled AS (
		UPDATE guardian_authority_policies SET enabled=true,enabled_at=clock_timestamp(),enabled_by=$1
		WHERE version=$2 RETURNING id,version,enabled_at)
		INSERT INTO guardian_authority_policy_events(policy_id,policy_version,actor_ref,action,occurred_at)
		SELECT id,version,$1,'ENABLED',enabled_at FROM enabled`, grantorID, policy); err != nil {
		t.Fatal(err)
	}
	activateGuardianPolicyFixture(t, ctx, conn, grantorID, policy)
	if _, err = q.GrantGuardianVerifier(ctx, dbgen.GrantGuardianVerifierParams{ActorID: grantorID, UserID: verifierID}); err != nil {
		t.Fatal(err)
	}
	if can, err := q.CanVerifyGuardianAuthority(ctx, verifierID); err != nil || !can {
		t.Fatalf("verification capability = %v, %v", can, err)
	}

	dependent, err := q.CreateDependentUser(ctx, dbgen.CreateDependentUserParams{Name: "Menor pendente", GuardianID: guardianID,
		DateOfBirth: pgtype.Date{Time: time.Now().AddDate(-10, 0, 0), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	var storedGuardian *uuid.UUID
	var loginBefore *string
	if err = conn.QueryRow(ctx, `SELECT guardian_id,minor_login_id FROM users WHERE id=$1`, dependent.ID).Scan(&storedGuardian, &loginBefore); err != nil {
		t.Fatal(err)
	}
	if storedGuardian != nil || loginBefore != nil {
		t.Fatalf("legacy pointer/credential unexpectedly set guardian=%v login=%v", storedGuardian, loginBefore)
	}
	if _, err = conn.Exec(ctx, `UPDATE users SET guardian_id=$1 WHERE id=$2`, guardianID, dependent.ID); err == nil {
		t.Fatal("legacy guardian pointer write succeeded")
	}
	relationships, err := q.ListGuardianRelationshipsForGuardian(ctx, dbgen.ListGuardianRelationshipsForGuardianParams{GuardianID: guardianID, RowLimit: 10})
	if err != nil || len(relationships) != 1 {
		t.Fatalf("pending relationship scan rows=%d err=%v", len(relationships), err)
	}
	pending := relationships[0]
	if pending.State != "PENDING" || pending.SubjectName != "" || pending.MinorLoginID != "" || pending.DateOfBirth.Valid || pending.LeaderboardVisible || pending.ProfileComplete {
		t.Fatalf("pending disclosure was not redacted: %+v", pending)
	}
	if has, err := q.HasVerifiedGuardianAuthority(ctx, guardianID); err != nil || has {
		t.Fatalf("pending relationship treated as verified: %v, %v", has, err)
	}
	if _, err = q.GetMemberForAdmin(ctx, dependent.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("pending minor remained visible in ordinary administrator detail: %v", err)
	}
	if _, err = q.GetMemberAvatar(ctx, dbgen.GetMemberAvatarParams{UserID: dependent.ID, ActorID: grantorID, IsAdmin: true, DocumentVersion: "test", DocumentSha256: "test"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("pending minor avatar remained visible to ordinary administrator: %v", err)
	}
	var seasonID, programmeID uuid.UUID
	if err = conn.QueryRow(ctx, `INSERT INTO seasons(code,name,starts_on,ends_on) VALUES($1,'Época teste',CURRENT_DATE,CURRENT_DATE+365) RETURNING id`, "S-"+uuid.NewString()[:8]).Scan(&seasonID); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, `INSERT INTO programmes(code,name_pt) VALUES($1,'Programa teste') RETURNING id`, "P_"+strings.ReplaceAll(uuid.NewString(), "-", "")).Scan(&programmeID); err != nil {
		t.Fatal(err)
	}
	assertAdminMutationBlocked := func(subjectID uuid.UUID, state string) {
		t.Helper()
		_, mutationErr := q.UpsertCurrentSeasonMembership(ctx, dbgen.UpsertCurrentSeasonMembershipParams{UserID: subjectID, SeasonID: seasonID, ProgrammeID: programmeID, StartsOn: pgtype.Date{Time: time.Now(), Valid: true}})
		if !errors.Is(mutationErr, pgx.ErrNoRows) {
			t.Fatalf("%s minor membership mutation error=%v", state, mutationErr)
		}
		affected, mutationErr := q.DeactivateMemberForAdmin(ctx, subjectID)
		if mutationErr != nil {
			t.Fatalf("%s minor deactivation error=%v", state, mutationErr)
		}
		var active bool
		if mutationErr = conn.QueryRow(ctx, `SELECT is_active FROM users WHERE id=$1`, subjectID).Scan(&active); mutationErr != nil || !active || affected != 0 {
			t.Fatalf("%s minor deactivation bypass affected=%d active=%t error=%v", state, affected, active, mutationErr)
		}
	}
	assertAdminMutationBlocked(dependent.ID, "pending")

	evidenceType, evidenceRef, decisionReason := "CIVIL_REGISTRY", "registry/"+uuid.NewString(), "CHANGE"
	verified, err := q.TransitionGuardianAuthority(ctx, dbgen.TransitionGuardianAuthorityParams{ActorID: verifierID,
		RelationshipRef: pending.RelationshipRef, ExpectedVersion: 1, TargetState: "VERIFIED",
		EvidenceType: &evidenceType, EvidenceReference: &evidenceRef, EvidenceSha256: make([]byte, 32), ReasonCode: &decisionReason})
	if err != nil {
		t.Fatal(err)
	}
	if verified.Version != 2 || verified.State != "VERIFIED" {
		t.Fatalf("verified relationship = %+v", verified)
	}
	if current, err := q.HasVerifiedGuardianAuthority(ctx, guardianID); err != nil || !current {
		t.Fatalf("verified relationship unavailable: %v, %v", current, err)
	}
	if _, err = q.GetMemberForAdmin(ctx, dependent.ID); err != nil {
		t.Fatalf("verified minor unavailable in administrator detail: %v", err)
	}
	queue, err := q.ListPendingGuardianAuthorityRequests(ctx, dbgen.ListPendingGuardianAuthorityRequestsParams{ActorID: verifierID, RowLimit: 20})
	if err != nil {
		t.Fatal(err)
	}
	foundVerified := false
	for _, row := range queue {
		foundVerified = foundVerified || row.RelationshipRef == pending.RelationshipRef && row.State == "VERIFIED"
	}
	if !foundVerified {
		t.Fatal("verified relationship was not returned to verifier queue")
	}
	minorLogin, minorHash := "minor-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:12], "minor-password-hash"
	if _, err = q.IssueMinorCredential(ctx, dbgen.IssueMinorCredentialParams{
		MinorLoginID: &minorLogin, PasswordHash: &minorHash, MinorUserID: dependent.ID,
		GuardianUserID: guardianID, ActorUserID: grantorID, Action: "ISSUED",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.GetActiveDependentByLoginID(ctx, &minorLogin); err != nil {
		t.Fatalf("issued dependent credential unavailable before majority: %v", err)
	}
	var credentialVersionBefore int64
	if err = conn.QueryRow(ctx, `SELECT credential_version FROM users WHERE id=$1`, dependent.ID).Scan(&credentialVersionBefore); err != nil {
		t.Fatal(err)
	}
	sessionToken := "guardian-authority-" + uuid.NewString()
	if _, err = conn.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($1,'\\x',clock_timestamp()+interval '1 hour',$2,true)`, sessionToken, dependent.ID); err != nil {
		t.Fatal(err)
	}
	privacySubject, err := q.GetPrivacyAccountForUpdate(ctx, dependent.ID)
	if err != nil || privacySubject.GuardianID == nil || *privacySubject.GuardianID != guardianID || !privacySubject.UpdatedAt.Time.Equal(verified.UpdatedAt.Time) {
		t.Fatalf("privacy projection = %+v err=%v relationship=%+v", privacySubject, err, verified)
	}
	identityUpdatedAt, err := q.GetPrivacyIdentityUpdatedAt(ctx, dependent.ID)
	if err != nil || !identityUpdatedAt.Valid || identityUpdatedAt.Time.Equal(privacySubject.UpdatedAt.Time) {
		t.Fatalf("identity and relationship clocks were not kept independent: identity=%+v relationship=%+v err=%v", identityUpdatedAt, privacySubject.UpdatedAt, err)
	}

	// Exactly one optimistic transition wins even when two workers start with the same version.
	reason := "CONFLICT"
	inputs := []dbgen.TransitionGuardianAuthorityParams{
		{ActorID: verifierID, RelationshipRef: pending.RelationshipRef, ExpectedVersion: 2, TargetState: "SUSPENDED", ReasonCode: &reason},
		{ActorID: verifierID, RelationshipRef: pending.RelationshipRef, ExpectedVersion: 2, TargetState: "SUSPENDED", ReasonCode: &reason},
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, input := range inputs {
		wg.Add(1)
		go func(input dbgen.TransitionGuardianAuthorityParams) {
			defer wg.Done()
			parallel, openErr := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
			if openErr != nil {
				errs <- openErr
				return
			}
			defer parallel.Close(ctx)
			_, transitionErr := dbgen.New(parallel).TransitionGuardianAuthority(ctx, input)
			errs <- transitionErr
		}(input)
	}
	wg.Wait()
	close(errs)
	successes, failures := 0, 0
	for transitionErr := range errs {
		if transitionErr == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent transitions successes=%d failures=%d", successes, failures)
	}
	if current, err := q.HasVerifiedGuardianAuthority(ctx, guardianID); err != nil || current {
		t.Fatalf("suspended relationship remained current: %v, %v", current, err)
	}
	if _, err = q.GetActiveDependentByLoginID(ctx, &minorLogin); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("suspended minor credential remained usable: %v", err)
	}
	var clearedLogin, clearedHash *string
	var credentialVersionAfter int64
	var sessionCount int
	if err = conn.QueryRow(ctx, `SELECT minor_login_id,password_hash,credential_version FROM users WHERE id=$1`, dependent.ID).Scan(&clearedLogin, &clearedHash, &credentialVersionAfter); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE token=$1`, sessionToken).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if clearedLogin != nil || clearedHash != nil || credentialVersionAfter != credentialVersionBefore+1 || sessionCount != 0 {
		t.Fatalf("suspension credential cutoff login=%v hash=%v version=%d want=%d sessions=%d", clearedLogin, clearedHash, credentialVersionAfter, credentialVersionBefore+1, sessionCount)
	}
	assertAdminMutationBlocked(dependent.ID, "suspended")

	// A fresh verifier can resolve the conflict; the verifier who recorded it cannot self-clear it.
	if _, err = q.TransitionGuardianAuthority(ctx, dbgen.TransitionGuardianAuthorityParams{ActorID: verifierID,
		RelationshipRef: pending.RelationshipRef, ExpectedVersion: 3, TargetState: "VERIFIED",
		EvidenceType: &evidenceType, EvidenceReference: &evidenceRef, EvidenceSha256: make([]byte, 32), ReasonCode: &decisionReason}); err == nil {
		t.Fatal("conflict actor cleared their own conflict")
	}
	secondVerifier := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Segunda verificadora',$2,'hash','1980-01-01')`, secondVerifier, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, secondVerifier); err != nil {
		t.Fatal(err)
	}
	if _, err = q.GrantGuardianVerifier(ctx, dbgen.GrantGuardianVerifierParams{ActorID: grantorID, UserID: secondVerifier}); err != nil {
		t.Fatal(err)
	}
	revoker, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer revoker.Close(ctx)
	revokeTx, err := revoker.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dbgen.New(revokeTx).RevokeGuardianVerifier(ctx, dbgen.RevokeGuardianVerifierParams{ActorID: grantorID, UserID: secondVerifier}); err != nil {
		_ = revokeTx.Rollback(ctx)
		t.Fatal(err)
	}
	transitionResult := make(chan error, 1)
	go func() {
		parallel, openErr := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
		if openErr != nil {
			transitionResult <- openErr
			return
		}
		defer parallel.Close(ctx)
		_, transitionErr := dbgen.New(parallel).TransitionGuardianAuthority(ctx, dbgen.TransitionGuardianAuthorityParams{
			ActorID: secondVerifier, RelationshipRef: pending.RelationshipRef, ExpectedVersion: 3, TargetState: "VERIFIED",
			EvidenceType: &evidenceType, EvidenceReference: &evidenceRef, EvidenceSha256: make([]byte, 32), ReasonCode: &decisionReason,
		})
		transitionResult <- transitionErr
	}()
	select {
	case transitionErr := <-transitionResult:
		_ = revokeTx.Rollback(ctx)
		t.Fatalf("transition did not serialize behind verifier revocation: %v", transitionErr)
	case <-time.After(100 * time.Millisecond):
	}
	if err = revokeTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if transitionErr := <-transitionResult; transitionErr == nil {
		t.Fatal("transition succeeded after the verifier grant was concurrently revoked")
	}
	if _, err = q.GrantGuardianVerifier(ctx, dbgen.GrantGuardianVerifierParams{ActorID: grantorID, UserID: secondVerifier}); err != nil {
		t.Fatal(err)
	}
	verified, err = q.TransitionGuardianAuthority(ctx, dbgen.TransitionGuardianAuthorityParams{ActorID: secondVerifier,
		RelationshipRef: pending.RelationshipRef, ExpectedVersion: 3, TargetState: "VERIFIED",
		EvidenceType: &evidenceType, EvidenceReference: &evidenceRef, EvidenceSha256: make([]byte, 32), ReasonCode: &decisionReason})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `UPDATE guardian_authority_relationships SET
		verified_at=clock_timestamp()-interval '2 days',review_due_at=clock_timestamp()-interval '1 day'
		WHERE id=$1`, verified.ID); err != nil {
		t.Fatal(err)
	}
	expired, err := q.TransitionGuardianAuthority(ctx, dbgen.TransitionGuardianAuthorityParams{ActorID: secondVerifier,
		RelationshipRef: pending.RelationshipRef, ExpectedVersion: 4, TargetState: "EXPIRED", ReasonCode: &decisionReason})
	if err != nil || expired.State != "EXPIRED" || expired.Version != 5 {
		t.Fatalf("explicit expiry = %+v err=%v", expired, err)
	}
	assertAdminMutationBlocked(dependent.ID, "expired")
	queue, err = q.ListPendingGuardianAuthorityRequests(ctx, dbgen.ListPendingGuardianAuthorityRequestsParams{ActorID: secondVerifier, RowLimit: 20})
	if err != nil {
		t.Fatal(err)
	}
	foundExpired := false
	for _, row := range queue {
		foundExpired = foundExpired || row.RelationshipRef == pending.RelationshipRef && row.State == "EXPIRED"
	}
	if !foundExpired {
		t.Fatal("effective expiry was not returned to verifier queue")
	}
	if _, err = q.TransitionGuardianAuthority(ctx, dbgen.TransitionGuardianAuthorityParams{ActorID: secondVerifier,
		RelationshipRef: pending.RelationshipRef, ExpectedVersion: 5, TargetState: "VERIFIED",
		EvidenceType: &evidenceType, EvidenceReference: &evidenceRef, EvidenceSha256: make([]byte, 32), ReasonCode: &decisionReason}); err != nil {
		t.Fatalf("expired relationship was not revalidated: %v", err)
	}

	rejectedSubject, err := q.CreateDependentUser(ctx, dbgen.CreateDependentUserParams{Name: "Menor rejeitado", GuardianID: guardianID,
		DateOfBirth: pgtype.Date{Time: time.Now().AddDate(-12, 0, 0), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	var rejectedRef uuid.UUID
	if err = conn.QueryRow(ctx, `SELECT public_ref FROM guardian_authority_relationships WHERE subject_user_id=$1`, rejectedSubject.ID).Scan(&rejectedRef); err != nil {
		t.Fatal(err)
	}
	rejectReason := "LOSS"
	if _, err = q.TransitionGuardianAuthority(ctx, dbgen.TransitionGuardianAuthorityParams{ActorID: secondVerifier,
		RelationshipRef: rejectedRef, ExpectedVersion: 1, TargetState: "REJECTED", EvidenceType: &evidenceType,
		EvidenceReference: &evidenceRef, EvidenceSha256: make([]byte, 32), ReasonCode: &rejectReason}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.TransitionGuardianAuthority(ctx, dbgen.TransitionGuardianAuthorityParams{ActorID: secondVerifier,
		RelationshipRef: rejectedRef, ExpectedVersion: 2, TargetState: "VERIFIED", EvidenceType: &evidenceType,
		EvidenceReference: &evidenceRef, EvidenceSha256: make([]byte, 32), ReasonCode: &decisionReason}); err == nil {
		t.Fatal("rejected relationship was not terminal")
	}
	assertAdminMutationBlocked(rejectedSubject.ID, "rejected")

	if _, err = conn.Exec(ctx, `UPDATE users SET date_of_birth=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date WHERE id=$1`, dependent.ID); err != nil {
		t.Fatal(err)
	}
	if current, err := q.HasVerifiedGuardianAuthority(ctx, guardianID); err != nil || current {
		t.Fatalf("majority did not end guardian authority: %v, %v", current, err)
	}
	relationships, err = q.ListGuardianRelationshipsForGuardian(ctx, dbgen.ListGuardianRelationshipsForGuardianParams{GuardianID: guardianID, RowLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	majorityExpired := false
	for _, row := range relationships {
		if row.RelationshipRef == pending.RelationshipRef {
			majorityExpired = row.State == "EXPIRED" && row.SubjectName == "" && !row.DateOfBirth.Valid
		}
	}
	if !majorityExpired {
		t.Fatal("majority relationship was not safely expired and redacted")
	}
	if _, err = q.GetActiveAccountByID(ctx, dependent.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("dependent session lookup at majority error=%v", err)
	}
	if _, err = q.GetActiveDependentByLoginID(ctx, &minorLogin); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("dependent login at majority error=%v", err)
	}
	var retainedLogin, retainedHash *string
	if err = conn.QueryRow(ctx, `SELECT minor_login_id,password_hash FROM users WHERE id=$1`, dependent.ID).Scan(&retainedLogin, &retainedHash); err != nil {
		t.Fatal(err)
	}
	if retainedLogin != nil || retainedHash != nil {
		t.Fatalf("revoked dependent credential reappeared at majority: login=%v hash_set=%t", retainedLogin, retainedHash != nil)
	}
	if _, err = conn.Exec(ctx, `UPDATE guardian_authority_events SET reason_code='CHANGE' WHERE relationship_id=$1`, verified.ID); err == nil {
		t.Fatal("authority event update succeeded")
	}
	if _, err = q.RevokeGuardianVerifier(ctx, dbgen.RevokeGuardianVerifierParams{ActorID: grantorID, UserID: verifierID}); err != nil {
		t.Fatal(err)
	}
	if can, err := q.CanVerifyGuardianAuthority(ctx, verifierID); err != nil || can {
		t.Fatalf("revoked verifier capability = %v, %v", can, err)
	}
}

func TestGuardianAuthorityAdministratorReviewIsClosedAuditedAndSeparated(t *testing.T) {
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
	q := dbgen.New(tx)
	guardianID, subjectID := uuid.New(), uuid.New()
	adminOne, adminTwo, familyAdmin, ordinary := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for id, label := range map[uuid.UUID]string{guardianID: "Requerente V2", adminOne: "Admin um V2", adminTwo: "Admin dois V2", familyAdmin: "Admin familiar V2", ordinary: "Pessoa não administradora"} {
		if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth) VALUES($1,$2,$3,clock_timestamp(),'hash','1980-01-01')`, id, label, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{guardianID, adminOne, adminTwo, familyAdmin} {
		if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, id); err != nil {
			t.Fatal(err)
		}
	}
	policy := "guardian-review-v2-" + uuid.NewString()
	if _, err = tx.Exec(ctx, `UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO guardian_authority_policies(version,evidence_types,reason_codes,validity_days,review_days,adopted_at,adopted_by,enabled,enabled_at,enabled_by)
		VALUES($1,'{LEGACY_UNUSED}','{CONFLICT}',365,180,clock_timestamp(),$2,true,clock_timestamp(),$2)`, policy, adminOne); err != nil {
		t.Fatal(err)
	}
	activateGuardianPolicyFixture(t, ctx, tx, adminOne, policy)
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,is_dependent,date_of_birth) VALUES($1,'Menor V2',NULL,NULL,true,CURRENT_DATE-interval '10 years')`, subjectID); err != nil {
		t.Fatal(err)
	}
	var relationshipRef uuid.UUID
	if err = tx.QueryRow(ctx, `WITH relationship AS (
		INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label) VALUES($1,$2,'Menor V2') RETURNING id,public_ref,created_at)
		INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,to_state,occurred_at)
		SELECT id,1,$1,'GUARDIAN','DECLARED','PENDING',created_at FROM relationship RETURNING (SELECT public_ref FROM relationship)`, guardianID, subjectID).Scan(&relationshipRef); err != nil {
		t.Fatal(err)
	}
	// The relationship graph is the only stored family boundary. This fixture
	// links the family administrator to a request party without making them a
	// party to the relationship under review.
	if _, err = tx.Exec(ctx, `WITH relationship AS (
		INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label)
		VALUES($1,$2,'Ligação familiar para separação') RETURNING id,created_at)
		INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,to_state,occurred_at)
		SELECT id,1,$1,'GUARDIAN','DECLARED','PENDING',created_at FROM relationship`, familyAdmin, guardianID); err != nil {
		t.Fatal(err)
	}
	expectRejected := func(label string, input dbgen.AdminTransitionGuardianAuthorityParams) {
		t.Helper()
		if _, saveErr := tx.Exec(ctx, `SAVEPOINT guardian_review_rejected`); saveErr != nil {
			t.Fatal(saveErr)
		}
		_, decisionErr := q.AdminTransitionGuardianAuthority(ctx, input)
		if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT guardian_review_rejected`); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		if decisionErr == nil {
			t.Fatalf("%s succeeded", label)
		}
	}
	expectSeparated := func(label string, actorID uuid.UUID, expectedVersion int64, action, reason string) {
		t.Helper()
		var beforeState string
		var beforeVersion int64
		var beforeEvents int
		if snapshotErr := tx.QueryRow(ctx, `SELECT relationship.state,relationship.version,
			(SELECT count(*) FROM guardian_authority_events event WHERE event.relationship_id=relationship.id)
			FROM guardian_authority_relationships relationship WHERE relationship.public_ref=$1`, relationshipRef).
			Scan(&beforeState, &beforeVersion, &beforeEvents); snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		if _, saveErr := tx.Exec(ctx, `SAVEPOINT guardian_review_separated`); saveErr != nil {
			t.Fatal(saveErr)
		}
		_, decisionErr := q.AdminTransitionGuardianAuthority(ctx, dbgen.AdminTransitionGuardianAuthorityParams{
			ActorID: actorID, RelationshipRef: relationshipRef, ExpectedVersion: expectedVersion, Action: action,
			EvidenceCategory: "CLUB_REGISTRATION_RECORD", ReasonCode: reason,
		})
		var databaseError *pgconn.PgError
		if !errors.As(decisionErr, &databaseError) || databaseError.Message != "guardian_authority_separation_required" {
			t.Fatalf("%s error=%v", label, decisionErr)
		}
		if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT guardian_review_separated`); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		var afterState string
		var afterVersion int64
		var afterEvents int
		if snapshotErr := tx.QueryRow(ctx, `SELECT relationship.state,relationship.version,
			(SELECT count(*) FROM guardian_authority_events event WHERE event.relationship_id=relationship.id)
			FROM guardian_authority_relationships relationship WHERE relationship.public_ref=$1`, relationshipRef).
			Scan(&afterState, &afterVersion, &afterEvents); snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		if afterState != beforeState || afterVersion != beforeVersion || afterEvents != beforeEvents {
			t.Fatalf("%s mutated relationship state=%s/%s version=%d/%d events=%d/%d", label,
				beforeState, afterState, beforeVersion, afterVersion, beforeEvents, afterEvents)
		}
	}
	expectSeparated("administrator request party", guardianID, 1, "APPROVE", "RELATIONSHIP_CONFIRMED")
	expectSeparated("administrator with family relationship", familyAdmin, 1, "APPROVE", "RELATIONSHIP_CONFIRMED")
	expectRejected("non-administrator decision", dbgen.AdminTransitionGuardianAuthorityParams{ActorID: ordinary, RelationshipRef: relationshipRef, ExpectedVersion: 1, Action: "APPROVE", EvidenceCategory: "CLUB_REGISTRATION_RECORD", ReasonCode: "RELATIONSHIP_CONFIRMED"})
	expectRejected("forged action/reason combination", dbgen.AdminTransitionGuardianAuthorityParams{ActorID: adminOne, RelationshipRef: relationshipRef, ExpectedVersion: 1, Action: "APPROVE", EvidenceCategory: "CLUB_REGISTRATION_RECORD", ReasonCode: "AUTHORITY_ENDED"})
	if _, err = q.ListPendingGuardianAuthorityRequests(ctx, dbgen.ListPendingGuardianAuthorityRequestsParams{ActorID: adminOne, RowLimit: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.GetGuardianAuthorityRequestForVerifier(ctx, dbgen.GetGuardianAuthorityRequestForVerifierParams{ActorID: adminOne, RelationshipRef: relationshipRef}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.IssueGuardianAuthorityInvitation(ctx, dbgen.IssueGuardianAuthorityInvitationParams{ActorID: adminOne, InvitedEmail: "unrelated-" + uuid.NewString() + "@example.test", TokenDigest: make([]byte, 32)}); err != nil {
		t.Fatal(err)
	}
	var queueViews, detailViews int
	if err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE view_kind='QUEUE'),count(*) FILTER(WHERE view_kind='DETAIL') FROM guardian_authority_review_access_events WHERE actor_ref=$1`, adminOne).Scan(&queueViews, &detailViews); err != nil {
		t.Fatal(err)
	}
	if queueViews != 1 || detailViews != 1 {
		t.Fatalf("view audit queue=%d detail=%d", queueViews, detailViews)
	}
	suspended, err := q.AdminTransitionGuardianAuthority(ctx, dbgen.AdminTransitionGuardianAuthorityParams{ActorID: adminOne, RelationshipRef: relationshipRef, ExpectedVersion: 1, Action: "SUSPEND", EvidenceCategory: "IN_PERSON_ID_AND_CIVIL_RECORD", ReasonCode: "CONFLICT"})
	if err != nil {
		t.Fatal(err)
	}
	expectRejected("conflict recorder cleared own conflict", dbgen.AdminTransitionGuardianAuthorityParams{ActorID: adminOne, RelationshipRef: relationshipRef, ExpectedVersion: suspended.Version, Action: "APPROVE", EvidenceCategory: "IN_PERSON_ID_AND_CIVIL_RECORD", ReasonCode: "RELATIONSHIP_CONFIRMED"})
	approved, err := q.AdminTransitionGuardianAuthority(ctx, dbgen.AdminTransitionGuardianAuthorityParams{ActorID: adminTwo, RelationshipRef: relationshipRef, ExpectedVersion: suspended.Version, Action: "APPROVE", EvidenceCategory: "COURT_OR_LEGAL_AUTHORITY", ReasonCode: "RELATIONSHIP_CONFIRMED"})
	if err != nil || approved.State != "VERIFIED" {
		t.Fatalf("second admin approval=%+v error=%v", approved, err)
	}
	var category, reason string
	var reference *string
	var digest []byte
	if err = tx.QueryRow(ctx, `SELECT evidence_type,reason_code,evidence_reference,evidence_sha256 FROM guardian_authority_events WHERE relationship_id=$1 AND relationship_version=$2`, approved.ID, approved.Version).Scan(&category, &reason, &reference, &digest); err != nil {
		t.Fatal(err)
	}
	if category != "COURT_OR_LEGAL_AUTHORITY" || reason != "RELATIONSHIP_CONFIRMED" || reference != nil || digest != nil {
		t.Fatalf("decision audit category=%q reason=%q reference=%v digest=%x", category, reason, reference, digest)
	}
	expectSeparated("administrator request party ended own relationship", guardianID, approved.Version, "END", "AUTHORITY_ENDED")
}

func TestGuardianAuthorityAdminTransitionSerializesAndEndsDependentAccess(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	q := dbgen.New(conn)

	guardianID, subjectID, adminOne, adminTwo := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for id, label := range map[uuid.UUID]string{
		guardianID: "Responsável concorrência V2", adminOne: "Admin concorrência um V2", adminTwo: "Admin concorrência dois V2",
	} {
		if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth)
			VALUES($1,$2,$3,clock_timestamp(),'hash','1980-01-01')`, id, label, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{adminOne, adminTwo} {
		if _, err = conn.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, id); err != nil {
			t.Fatal(err)
		}
	}
	policy := "guardian-admin-concurrency-v2-" + uuid.NewString()
	if _, err = conn.Exec(ctx, `UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled`); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO guardian_authority_policies(version,evidence_types,reason_codes,validity_days,review_days,adopted_at,adopted_by,enabled,enabled_at,enabled_by)
		VALUES($1,'{LEGACY_UNUSED}','{CONFLICT}',365,180,clock_timestamp(),$2,true,clock_timestamp(),$2)`, policy, adminOne); err != nil {
		t.Fatal(err)
	}
	activateGuardianPolicyFixture(t, ctx, conn, adminOne, policy)
	defer func() {
		_, _ = conn.Exec(context.Background(), `UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE version=$1`, policy)
	}()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,is_dependent,date_of_birth)
		VALUES($1,'Menor concorrência V2',NULL,NULL,true,CURRENT_DATE-interval '10 years')`, subjectID); err != nil {
		t.Fatal(err)
	}
	var relationshipRef uuid.UUID
	if err = conn.QueryRow(ctx, `WITH relationship AS (
		INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label)
		VALUES($1,$2,'Menor concorrência V2') RETURNING id,public_ref,created_at)
		INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,to_state,occurred_at)
		SELECT id,1,$1,'GUARDIAN','DECLARED','PENDING',created_at FROM relationship
		RETURNING (SELECT public_ref FROM relationship)`, guardianID, subjectID).Scan(&relationshipRef); err != nil {
		t.Fatal(err)
	}
	approved, err := q.AdminTransitionGuardianAuthority(ctx, dbgen.AdminTransitionGuardianAuthorityParams{
		ActorID: adminOne, RelationshipRef: relationshipRef, ExpectedVersion: 1, Action: "APPROVE",
		EvidenceCategory: "CLUB_REGISTRATION_RECORD", ReasonCode: "RELATIONSHIP_CONFIRMED",
	})
	if err != nil || approved.State != "VERIFIED" || approved.Version != 2 {
		t.Fatalf("initial administrator approval=%+v error=%v", approved, err)
	}

	issueCredential := func(label string) (string, int64) {
		t.Helper()
		login, hash := "minor-"+strings.ReplaceAll(uuid.NewString(), "-", ""), "password-hash-"+label
		if _, issueErr := q.IssueMinorCredential(ctx, dbgen.IssueMinorCredentialParams{
			MinorLoginID: &login, PasswordHash: &hash, MinorUserID: subjectID,
			GuardianUserID: guardianID, ActorUserID: adminOne, Action: "ISSUED",
		}); issueErr != nil {
			t.Fatal(issueErr)
		}
		var version int64
		if versionErr := conn.QueryRow(ctx, `SELECT credential_version FROM users WHERE id=$1`, subjectID).Scan(&version); versionErr != nil {
			t.Fatal(versionErr)
		}
		token := "guardian-admin-transition-" + label + "-" + uuid.NewString()
		if _, sessionErr := conn.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed)
			VALUES($1,'\\x',clock_timestamp()+interval '1 hour',$2,true)`, token, subjectID); sessionErr != nil {
			t.Fatal(sessionErr)
		}
		return token, version
	}
	assertAccessEnded := func(label, token string, previousVersion int64) {
		t.Helper()
		var login, hash *string
		var version int64
		var sessions int
		if queryErr := conn.QueryRow(ctx, `SELECT minor_login_id,password_hash,credential_version FROM users WHERE id=$1`, subjectID).
			Scan(&login, &hash, &version); queryErr != nil {
			t.Fatal(queryErr)
		}
		if queryErr := conn.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE token=$1`, token).Scan(&sessions); queryErr != nil {
			t.Fatal(queryErr)
		}
		if login != nil || hash != nil || version != previousVersion+1 || sessions != 0 {
			t.Fatalf("%s cutoff login=%v hash=%v credential_version=%d want=%d sessions=%d", label,
				login, hash, version, previousVersion+1, sessions)
		}
	}

	suspendToken, suspendCredentialVersion := issueCredential("suspend")
	inputs := []dbgen.AdminTransitionGuardianAuthorityParams{
		{ActorID: adminOne, RelationshipRef: relationshipRef, ExpectedVersion: 2, Action: "SUSPEND", EvidenceCategory: "IN_PERSON_ID_AND_CIVIL_RECORD", ReasonCode: "UNCERTAINTY"},
		{ActorID: adminTwo, RelationshipRef: relationshipRef, ExpectedVersion: 2, Action: "SUSPEND", EvidenceCategory: "COURT_OR_LEGAL_AUTHORITY", ReasonCode: "UNCERTAINTY"},
	}
	errs := make(chan error, len(inputs))
	var wg sync.WaitGroup
	for _, input := range inputs {
		wg.Add(1)
		go func(input dbgen.AdminTransitionGuardianAuthorityParams) {
			defer wg.Done()
			parallel, openErr := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
			if openErr != nil {
				errs <- openErr
				return
			}
			defer parallel.Close(ctx)
			_, transitionErr := dbgen.New(parallel).AdminTransitionGuardianAuthority(ctx, input)
			errs <- transitionErr
		}(input)
	}
	wg.Wait()
	close(errs)
	successes, staleFailures := 0, 0
	for transitionErr := range errs {
		if transitionErr == nil {
			successes++
			continue
		}
		var databaseError *pgconn.PgError
		if errors.As(transitionErr, &databaseError) && databaseError.Message == "guardian_authority_stale" {
			staleFailures++
		} else {
			t.Fatalf("unexpected concurrent transition error=%v", transitionErr)
		}
	}
	var relationshipState string
	var relationshipVersion int64
	var versionThreeEvents int
	if err = conn.QueryRow(ctx, `SELECT relationship.state,relationship.version,
		(SELECT count(*) FROM guardian_authority_events event WHERE event.relationship_id=relationship.id AND event.relationship_version=3)
		FROM guardian_authority_relationships relationship WHERE relationship.public_ref=$1`, relationshipRef).
		Scan(&relationshipState, &relationshipVersion, &versionThreeEvents); err != nil {
		t.Fatal(err)
	}
	if successes != 1 || staleFailures != 1 || relationshipState != "SUSPENDED" || relationshipVersion != 3 || versionThreeEvents != 1 {
		t.Fatalf("concurrent admin transitions successes=%d stale=%d state=%s version=%d events=%d",
			successes, staleFailures, relationshipState, relationshipVersion, versionThreeEvents)
	}
	assertAccessEnded("suspend", suspendToken, suspendCredentialVersion)

	reapproved, err := q.AdminTransitionGuardianAuthority(ctx, dbgen.AdminTransitionGuardianAuthorityParams{
		ActorID: adminOne, RelationshipRef: relationshipRef, ExpectedVersion: 3, Action: "APPROVE",
		EvidenceCategory: "CLUB_REGISTRATION_RECORD", ReasonCode: "RELATIONSHIP_CONFIRMED",
	})
	if err != nil || reapproved.State != "VERIFIED" || reapproved.Version != 4 {
		t.Fatalf("administrator reapproval=%+v error=%v", reapproved, err)
	}
	endToken, endCredentialVersion := issueCredential("end")
	ended, err := q.AdminTransitionGuardianAuthority(ctx, dbgen.AdminTransitionGuardianAuthorityParams{
		ActorID: adminTwo, RelationshipRef: relationshipRef, ExpectedVersion: 4, Action: "END",
		EvidenceCategory: "COURT_OR_LEGAL_AUTHORITY", ReasonCode: "AUTHORITY_ENDED",
	})
	if err != nil || ended.State != "EXPIRED" || ended.Version != 5 {
		t.Fatalf("administrator end=%+v error=%v", ended, err)
	}
	assertAccessEnded("end", endToken, endCredentialVersion)
}

func TestGuardianAuthorityReconciliationAndRenewalDiscardDormantCredentials(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	q := dbgen.New(conn)
	guardianID, verifierID, subjectID := uuid.New(), uuid.New(), uuid.New()
	for id, label := range map[uuid.UUID]string{guardianID: "Responsável cutoff", verifierID: "Verificador cutoff"} {
		if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,$2,$3,'hash','1980-01-01')`, id, label, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	insertVerifiedDependentFixture(t, ctx, conn, guardianID, subjectID, "Menor cutoff", time.Now().AddDate(-12, 0, 0))
	if _, err = conn.Exec(ctx, `WITH grant_row AS (
		INSERT INTO guardian_verifier_grants(user_id,granted_by,granted_at) VALUES($1,$2,clock_timestamp()) RETURNING id,granted_at)
		INSERT INTO guardian_verifier_grant_events(grant_id,actor_ref,action,occurred_at)
		SELECT id,$2,'GRANTED',granted_at FROM grant_row`, verifierID, guardianID); err != nil {
		t.Fatal(err)
	}
	var relationshipRef uuid.UUID
	if err = conn.QueryRow(ctx, `SELECT public_ref FROM guardian_authority_relationships WHERE subject_user_id=$1`, subjectID).Scan(&relationshipRef); err != nil {
		t.Fatal(err)
	}
	setCredential := func(token string) int64 {
		t.Helper()
		if _, setErr := conn.Exec(ctx, `UPDATE users SET minor_login_id=$2,password_hash='dormant-hash' WHERE id=$1`, subjectID, "CFC-"+strings.ToUpper(uuid.NewString()[:8])); setErr != nil {
			t.Fatal(setErr)
		}
		if _, setErr := conn.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($1,'\\x',clock_timestamp()+interval '1 hour',$2,true)`, token, subjectID); setErr != nil {
			t.Fatal(setErr)
		}
		var version int64
		if setErr := conn.QueryRow(ctx, `SELECT credential_version FROM users WHERE id=$1`, subjectID).Scan(&version); setErr != nil {
			t.Fatal(setErr)
		}
		return version
	}
	assertCleared := func(token string, previousVersion int64) {
		t.Helper()
		var login, hash *string
		var version int64
		var sessions int
		if scanErr := conn.QueryRow(ctx, `SELECT minor_login_id,password_hash,credential_version FROM users WHERE id=$1`, subjectID).Scan(&login, &hash, &version); scanErr != nil {
			t.Fatal(scanErr)
		}
		if scanErr := conn.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE token=$1`, token).Scan(&sessions); scanErr != nil {
			t.Fatal(scanErr)
		}
		if login != nil || hash != nil || version != previousVersion+1 || sessions != 0 {
			t.Fatalf("credential cutoff login=%v hash=%v version=%d want=%d sessions=%d", login, hash, version, previousVersion+1, sessions)
		}
	}

	firstToken := "cutoff-renew-" + uuid.NewString()
	firstVersion := setCredential(firstToken)
	if _, err = conn.Exec(ctx, `UPDATE guardian_authority_relationships SET verified_at=clock_timestamp()-interval '2 days',review_due_at=clock_timestamp()-interval '1 day' WHERE subject_user_id=$1`, subjectID); err != nil {
		t.Fatal(err)
	}
	evidenceType, evidenceRef, reason := "CIVIL_REGISTRY", "registry/"+uuid.NewString(), "CHANGE"
	if _, err = q.TransitionGuardianAuthority(ctx, dbgen.TransitionGuardianAuthorityParams{ActorID: verifierID, RelationshipRef: relationshipRef, ExpectedVersion: 2, TargetState: "VERIFIED", EvidenceType: &evidenceType, EvidenceReference: &evidenceRef, EvidenceSha256: make([]byte, 32), ReasonCode: &reason}); err != nil {
		t.Fatal(err)
	}
	assertCleared(firstToken, firstVersion)

	secondToken := "cutoff-majority-" + uuid.NewString()
	secondVersion := setCredential(secondToken)
	if _, err = conn.Exec(ctx, `UPDATE users SET date_of_birth=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date WHERE id=$1`, subjectID); err != nil {
		t.Fatal(err)
	}
	if reconciled, reconcileErr := q.ReconcileGuardianAuthorityCutoffs(ctx); reconcileErr != nil || reconciled < 1 {
		t.Fatalf("reconciled=%d error=%v", reconciled, reconcileErr)
	}
	var preservedLogin, preservedHash *string
	var majorityVersion int64
	var majoritySessions int
	if err = conn.QueryRow(ctx, `SELECT minor_login_id,password_hash,credential_version FROM users WHERE id=$1`, subjectID).Scan(&preservedLogin, &preservedHash, &majorityVersion); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE token=$1`, secondToken).Scan(&majoritySessions); err != nil {
		t.Fatal(err)
	}
	if preservedLogin == nil || preservedHash == nil || majorityVersion != secondVersion+1 || majoritySessions != 0 {
		t.Fatalf("majority recovery lock login=%v hash=%v version=%d want=%d sessions=%d", preservedLogin, preservedHash, majorityVersion, secondVersion+1, majoritySessions)
	}
	if _, loginErr := q.GetActiveDependentByLoginID(ctx, preservedLogin); !errors.Is(loginErr, pgx.ErrNoRows) {
		t.Fatalf("preserved majority credential remained usable: %v", loginErr)
	}
	var handoffStatus string
	if err = conn.QueryRow(ctx, `SELECT status FROM guardian_age_handoffs WHERE subject_user_id=$1`, subjectID).Scan(&handoffStatus); err != nil || handoffStatus != "RECOVERY_REQUIRED" {
		t.Fatalf("majority handoff status=%q err=%v", handoffStatus, err)
	}
	var state, actorRole string
	var actorRef *uuid.UUID
	if err = conn.QueryRow(ctx, `SELECT relationship.state,event.actor_role,event.actor_ref
		FROM guardian_authority_relationships relationship JOIN guardian_authority_events event
		 ON event.relationship_id=relationship.id AND event.relationship_version=relationship.version
		WHERE relationship.subject_user_id=$1`, subjectID).Scan(&state, &actorRole, &actorRef); err != nil {
		t.Fatal(err)
	}
	if state != "EXPIRED" || actorRole != "SYSTEM" || actorRef != nil {
		t.Fatalf("reconciled state=%s actor_role=%s actor_ref=%v", state, actorRole, actorRef)
	}
}

func TestGuardianAuthorityAnnualRenewalAndReminderBoundaries(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	q := dbgen.New(conn)
	guardianID, adminID, subjectID := uuid.New(), uuid.New(), uuid.New()
	for id, label := range map[uuid.UUID]string{guardianID: "Responsável renovação", adminID: "Admin renovação"} {
		if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth)
			VALUES($1,$2,$3,clock_timestamp(),'hash','1980-01-01')`, id, label, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = conn.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, adminID); err != nil {
		t.Fatal(err)
	}
	insertVerifiedDependentFixture(t, ctx, conn, guardianID, subjectID, "Menor renovação", time.Now().AddDate(-12, 0, 0))
	var relationshipRef uuid.UUID
	var originalExpiry time.Time
	if err = conn.QueryRow(ctx, `UPDATE guardian_authority_relationships SET verified_until=cutoff.value,review_due_at=cutoff.value
		FROM (SELECT clock_timestamp()+interval '20 days' AS value) cutoff WHERE subject_user_id=$1 RETURNING public_ref,verified_until`, subjectID).Scan(&relationshipRef, &originalExpiry); err != nil {
		t.Fatal(err)
	}
	targets, err := q.ListDueGuardianRenewalReminders(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	var target *dbgen.ListDueGuardianRenewalRemindersRow
	for i := range targets {
		if targets[i].RelationshipRef == relationshipRef {
			target = &targets[i]
		}
	}
	if target == nil || target.ReminderKind != "GUARDIAN_RENEWAL_30_DAY" {
		t.Fatalf("30-day reminder target missing: %+v", targets)
	}
	params := dbgen.EnqueueGuardianRenewalReminderParams{RelationshipRef: relationshipRef, GuardianUserID: target.GuardianUserID,
		Recipient: target.Recipient, RecipientVerifiedAt: target.RecipientVerifiedAt, ExpiryAnchor: target.ExpiryAnchor,
		ReminderKind: target.ReminderKind, SealedPayload: make([]byte, 64)}
	if queued, queueErr := q.EnqueueGuardianRenewalReminder(ctx, params); queueErr != nil || !queued {
		t.Fatalf("first reminder queued=%v err=%v", queued, queueErr)
	}
	if queued, queueErr := q.EnqueueGuardianRenewalReminder(ctx, params); queueErr != nil || queued {
		t.Fatalf("duplicate reminder queued=%v err=%v", queued, queueErr)
	}
	var reminderCount, outboxCount int
	if err = conn.QueryRow(ctx, `SELECT count(*),(SELECT count(*) FROM email_outbox outbox JOIN guardian_authority_renewal_reminders joined ON joined.id=outbox.guardian_reminder_id WHERE joined.relationship_id=relationship.id)
		FROM guardian_authority_renewal_reminders reminder JOIN guardian_authority_relationships relationship ON relationship.id=reminder.relationship_id WHERE relationship.public_ref=$1 GROUP BY relationship.id`, relationshipRef).Scan(&reminderCount, &outboxCount); err != nil {
		t.Fatal(err)
	}
	if reminderCount != 1 || outboxCount != 1 {
		t.Fatalf("reminders=%d outbox=%d", reminderCount, outboxCount)
	}

	if _, err = q.SubmitGuardianAuthorityRenewal(ctx, dbgen.SubmitGuardianAuthorityRenewalParams{ActorID: guardianID, RelationshipRef: relationshipRef, ExpectedVersion: 2, ResponseCode: "NADA_MUDOU"}); err != nil {
		t.Fatal(err)
	}
	var state string
	var version int64
	if err = conn.QueryRow(ctx, `SELECT state,version FROM guardian_authority_relationships WHERE public_ref=$1`, relationshipRef).Scan(&state, &version); err != nil {
		t.Fatal(err)
	}
	if state != "VERIFIED" || version != 3 {
		t.Fatalf("NADA_MUDOU changed access: state=%s version=%d", state, version)
	}
	if _, err = q.SubmitGuardianAuthorityRenewal(ctx, dbgen.SubmitGuardianAuthorityRenewalParams{ActorID: guardianID, RelationshipRef: relationshipRef, ExpectedVersion: 3, ResponseCode: "NADA_MUDOU"}); err == nil {
		t.Fatal("duplicate renewal submission succeeded")
	}
	renewed, err := q.AdminTransitionGuardianAuthority(ctx, dbgen.AdminTransitionGuardianAuthorityParams{ActorID: adminID, RelationshipRef: relationshipRef, ExpectedVersion: 3, Action: "RENEW", EvidenceCategory: "CLUB_REGISTRATION_RECORD", ReasonCode: "RELATIONSHIP_CONFIRMED"})
	if err != nil {
		t.Fatal(err)
	}
	wantExpiry := originalExpiry.AddDate(1, 0, 0)
	if renewed.State != "VERIFIED" || !renewed.VerifiedUntil.Time.Equal(wantExpiry) || !renewed.ReviewDueAt.Time.Equal(wantExpiry) {
		t.Fatalf("renewal expiry=%v review=%v want=%v", renewed.VerifiedUntil.Time, renewed.ReviewDueAt.Time, wantExpiry)
	}

	changedSubject := uuid.New()
	insertVerifiedDependentFixture(t, ctx, conn, guardianID, changedSubject, "Menor alterado", time.Now().AddDate(-10, 0, 0))
	var changedRef uuid.UUID
	if err = conn.QueryRow(ctx, `UPDATE guardian_authority_relationships SET verified_until=cutoff.value,review_due_at=cutoff.value
		FROM (SELECT clock_timestamp()+interval '10 days' AS value) cutoff WHERE subject_user_id=$1 RETURNING public_ref`, changedSubject).Scan(&changedRef); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `UPDATE users SET minor_login_id=$2,password_hash='credential' WHERE id=$1`, changedSubject, "CFC-"+strings.ToUpper(uuid.NewString()[:8])); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($2,'\x',clock_timestamp()+interval '1 hour',$1,true)`, changedSubject, "renewal-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err = q.SubmitGuardianAuthorityRenewal(ctx, dbgen.SubmitGuardianAuthorityRenewalParams{ActorID: guardianID, RelationshipRef: changedRef, ExpectedVersion: 2, ResponseCode: "DADOS_MUDARAM"}); err != nil {
		t.Fatal(err)
	}
	var login, password *string
	var sessions int
	if err = conn.QueryRow(ctx, `SELECT relationship.state,subject.minor_login_id,subject.password_hash,
		(SELECT count(*) FROM sessions WHERE user_id=subject.id AND subject_indexed)
		FROM guardian_authority_relationships relationship JOIN users subject ON subject.id=relationship.subject_user_id WHERE relationship.public_ref=$1`, changedRef).Scan(&state, &login, &password, &sessions); err != nil {
		t.Fatal(err)
	}
	if state != "SUSPENDED" || login != nil || password != nil || sessions != 0 {
		t.Fatalf("changed-data cutoff state=%s login=%v password=%v sessions=%d", state, login, password, sessions)
	}

	sevenDaySubject := uuid.New()
	insertVerifiedDependentFixture(t, ctx, conn, guardianID, sevenDaySubject, "Menor lembrete sete dias", time.Now().AddDate(-11, 0, 0))
	var sevenDayRef, sevenDayRelationshipID uuid.UUID
	var sevenDayExpiry, guardianVerifiedAt time.Time
	var guardianEmail string
	if err = conn.QueryRow(ctx, `UPDATE guardian_authority_relationships SET verified_until=cutoff.value,review_due_at=cutoff.value
		FROM (SELECT clock_timestamp()+interval '6 days' AS value) cutoff WHERE subject_user_id=$1
		RETURNING id,public_ref,verified_until`, sevenDaySubject).Scan(&sevenDayRelationshipID, &sevenDayRef, &sevenDayExpiry); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, `SELECT email::text,email_verified_at FROM users WHERE id=$1`, guardianID).Scan(&guardianEmail, &guardianVerifiedAt); err != nil {
		t.Fatal(err)
	}
	var priorReminderID uuid.UUID
	if err = conn.QueryRow(ctx, `INSERT INTO guardian_authority_renewal_reminders
		(relationship_id,guardian_user_id,recipient_verified_at,expiry_anchor,reminder_kind)
		VALUES($1,$2,$3,$4,'GUARDIAN_RENEWAL_30_DAY') RETURNING id`, sevenDayRelationshipID, guardianID, guardianVerifiedAt, sevenDayExpiry).Scan(&priorReminderID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO email_outbox(message_type,sealed_payload,guardian_reminder_id,status,sent_at)
		VALUES('GUARDIAN_RENEWAL_30_DAY',$1,$2,'SENT',clock_timestamp())`, make([]byte, 64), priorReminderID); err != nil {
		t.Fatal(err)
	}
	targets, err = q.ListDueGuardianRenewalReminders(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	target = nil
	for i := range targets {
		if targets[i].RelationshipRef == sevenDayRef {
			target = &targets[i]
		}
	}
	if target == nil || target.ReminderKind != "GUARDIAN_RENEWAL_7_DAY" {
		t.Fatalf("seven-day reminder target missing after prior 30-day delivery: %+v", targets)
	}
	sevenDayParams := dbgen.EnqueueGuardianRenewalReminderParams{RelationshipRef: sevenDayRef, GuardianUserID: target.GuardianUserID,
		Recipient: target.Recipient, RecipientVerifiedAt: target.RecipientVerifiedAt, ExpiryAnchor: target.ExpiryAnchor,
		ReminderKind: target.ReminderKind, SealedPayload: make([]byte, 64)}
	if queued, queueErr := q.EnqueueGuardianRenewalReminder(ctx, sevenDayParams); queueErr != nil || !queued {
		t.Fatalf("seven-day reminder queued=%v err=%v", queued, queueErr)
	}
	if queued, queueErr := q.EnqueueGuardianRenewalReminder(ctx, sevenDayParams); queueErr != nil || queued {
		t.Fatalf("duplicate seven-day reminder queued=%v err=%v", queued, queueErr)
	}
	var thirtyCount, sevenCount int
	if err = conn.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE reminder_kind='GUARDIAN_RENEWAL_30_DAY'),
		count(*) FILTER(WHERE reminder_kind='GUARDIAN_RENEWAL_7_DAY'),
		(SELECT count(*) FROM email_outbox outbox JOIN guardian_authority_renewal_reminders joined ON joined.id=outbox.guardian_reminder_id WHERE joined.relationship_id=$1)
		FROM guardian_authority_renewal_reminders WHERE relationship_id=$1`, sevenDayRelationshipID).
		Scan(&reminderCount, &thirtyCount, &sevenCount, &outboxCount); err != nil {
		t.Fatal(err)
	}
	if reminderCount != 2 || thirtyCount != 1 || sevenCount != 1 || outboxCount != 2 {
		t.Fatalf("seven-day idempotency reminders=%d 30-day=%d 7-day=%d outbox=%d", reminderCount, thirtyCount, sevenCount, outboxCount)
	}
}

func TestGuardianRenewalReminderRevalidatesRecipientAndAuthority(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	q := dbgen.New(conn)

	type queuedReminder struct {
		guardianID      uuid.UUID
		relationshipRef uuid.UUID
	}
	setup := func(label string) queuedReminder {
		t.Helper()
		guardianID, subjectID := uuid.New(), uuid.New()
		if _, setupErr := conn.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth)
			VALUES($1,$2,$3,clock_timestamp(),'hash','1980-01-01')`, guardianID, "Responsável "+label, uuid.NewString()+"@example.test"); setupErr != nil {
			t.Fatal(setupErr)
		}
		insertVerifiedDependentFixture(t, ctx, conn, guardianID, subjectID, "Menor "+label, time.Now().AddDate(-12, 0, 0))
		var relationshipRef uuid.UUID
		if setupErr := conn.QueryRow(ctx, `UPDATE guardian_authority_relationships SET verified_until=cutoff.value,review_due_at=cutoff.value
			FROM (SELECT clock_timestamp()+interval '20 days' AS value) cutoff WHERE subject_user_id=$1 RETURNING public_ref`, subjectID).Scan(&relationshipRef); setupErr != nil {
			t.Fatal(setupErr)
		}
		targets, setupErr := q.ListDueGuardianRenewalReminders(ctx, 100)
		if setupErr != nil {
			t.Fatal(setupErr)
		}
		for _, target := range targets {
			if target.RelationshipRef != relationshipRef {
				continue
			}
			params := dbgen.EnqueueGuardianRenewalReminderParams{RelationshipRef: relationshipRef, GuardianUserID: target.GuardianUserID,
				Recipient: target.Recipient, RecipientVerifiedAt: target.RecipientVerifiedAt, ExpiryAnchor: target.ExpiryAnchor,
				ReminderKind: target.ReminderKind, SealedPayload: make([]byte, 64)}
			if queued, queueErr := q.EnqueueGuardianRenewalReminder(ctx, params); queueErr != nil || !queued {
				t.Fatalf("setup reminder queued=%v err=%v", queued, queueErr)
			}
			return queuedReminder{guardianID: guardianID, relationshipRef: relationshipRef}
		}
		t.Fatalf("reminder target missing for %s", label)
		return queuedReminder{}
	}
	assertCancelled := func(fixture queuedReminder) {
		t.Helper()
		if _, cancelErr := q.CancelUndeliverableEmailOutbox(ctx, resetTimestamp(time.Now().Add(time.Second))); cancelErr != nil {
			t.Fatal(cancelErr)
		}
		var status string
		if scanErr := conn.QueryRow(ctx, `SELECT outbox.status FROM email_outbox outbox
			JOIN guardian_authority_renewal_reminders reminder ON reminder.id=outbox.guardian_reminder_id
			JOIN guardian_authority_relationships relationship ON relationship.id=reminder.relationship_id
			WHERE relationship.public_ref=$1`, fixture.relationshipRef).Scan(&status); scanErr != nil {
			t.Fatal(scanErr)
		}
		if status != "CANCELLED" {
			t.Fatalf("outbox status=%s, want CANCELLED", status)
		}
	}

	t.Run("verified email change", func(t *testing.T) {
		fixture := setup("email alterado")
		if _, err = conn.Exec(ctx, `UPDATE users SET email=$2,email_verified_at=NULL WHERE id=$1`, fixture.guardianID, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
		if _, claimErr := q.ClaimEmailOutbox(ctx, dbgen.ClaimEmailOutboxParams{
			ClaimedAt: resetTimestamp(time.Now().Add(time.Second)), StaleBefore: resetTimestamp(time.Now().Add(-time.Hour)),
		}); !errors.Is(claimErr, pgx.ErrNoRows) {
			t.Fatalf("stale-address reminder claim error=%v, want no rows", claimErr)
		}
		assertCancelled(fixture)
	})
	t.Run("guardian deactivation", func(t *testing.T) {
		fixture := setup("inativo")
		if _, err = conn.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, fixture.guardianID); err != nil {
			t.Fatal(err)
		}
		assertCancelled(fixture)
	})
	t.Run("renewal submission", func(t *testing.T) {
		fixture := setup("submetido")
		if _, err = q.SubmitGuardianAuthorityRenewal(ctx, dbgen.SubmitGuardianAuthorityRenewalParams{ActorID: fixture.guardianID,
			RelationshipRef: fixture.relationshipRef, ExpectedVersion: 2, ResponseCode: "NADA_MUDOU"}); err != nil {
			t.Fatal(err)
		}
		assertCancelled(fixture)
	})
	t.Run("immediate suspension", func(t *testing.T) {
		fixture := setup("suspenso")
		if _, err = q.SubmitGuardianAuthorityRenewal(ctx, dbgen.SubmitGuardianAuthorityRenewalParams{ActorID: fixture.guardianID,
			RelationshipRef: fixture.relationshipRef, ExpectedVersion: 2, ResponseCode: "DADOS_MUDARAM"}); err != nil {
			t.Fatal(err)
		}
		assertCancelled(fixture)
	})
	t.Run("expiry", func(t *testing.T) {
		fixture := setup("expirado")
		if _, err = conn.Exec(ctx, `UPDATE guardian_authority_relationships SET verified_at=cutoff.value-interval '1 second',
			verified_until=cutoff.value,review_due_at=cutoff.value
			FROM (SELECT clock_timestamp()-interval '1 second' AS value) cutoff WHERE public_ref=$1`, fixture.relationshipRef); err != nil {
			t.Fatal(err)
		}
		if _, err = q.ReconcileGuardianAuthorityCutoffs(ctx); err != nil {
			t.Fatal(err)
		}
		assertCancelled(fixture)
	})
	t.Run("discovery enqueue race", func(t *testing.T) {
		guardianID, subjectID := uuid.New(), uuid.New()
		if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth)
			VALUES($1,'Responsável corrida real',$2,clock_timestamp(),'hash','1980-01-01')`, guardianID, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
		insertVerifiedDependentFixture(t, ctx, conn, guardianID, subjectID, "Menor corrida real", time.Now().AddDate(-12, 0, 0))
		var relationshipRef uuid.UUID
		if err = conn.QueryRow(ctx, `UPDATE guardian_authority_relationships SET verified_until=cutoff.value,review_due_at=cutoff.value
			FROM (SELECT clock_timestamp()+interval '20 days' AS value) cutoff WHERE subject_user_id=$1 RETURNING public_ref`, subjectID).Scan(&relationshipRef); err != nil {
			t.Fatal(err)
		}
		targets, listErr := q.ListDueGuardianRenewalReminders(ctx, 100)
		if listErr != nil {
			t.Fatal(listErr)
		}
		var stale dbgen.EnqueueGuardianRenewalReminderParams
		for _, target := range targets {
			if target.RelationshipRef == relationshipRef {
				stale = dbgen.EnqueueGuardianRenewalReminderParams{RelationshipRef: relationshipRef, GuardianUserID: target.GuardianUserID,
					Recipient: target.Recipient, RecipientVerifiedAt: target.RecipientVerifiedAt, ExpiryAnchor: target.ExpiryAnchor,
					ReminderKind: target.ReminderKind, SealedPayload: make([]byte, 64)}
			}
		}
		if stale.RelationshipRef == uuid.Nil {
			t.Fatal("race target missing")
		}
		if _, err = conn.Exec(ctx, `UPDATE users SET email=$2,email_verified_at=NULL WHERE id=$1`, guardianID, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
		if queued, queueErr := q.EnqueueGuardianRenewalReminder(ctx, stale); queueErr != nil || queued {
			t.Fatalf("stale discovery enqueue queued=%v err=%v", queued, queueErr)
		}
		var count int
		if err = conn.QueryRow(ctx, `SELECT count(*) FROM guardian_authority_renewal_reminders reminder
			JOIN guardian_authority_relationships relationship ON relationship.id=reminder.relationship_id WHERE relationship.public_ref=$1`, relationshipRef).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("stale discovery created %d reminders", count)
		}
	})
}

func TestGuardianRenewalSurvivesExpiryAndSerializesAdministratorApproval(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	q := dbgen.New(conn)
	guardianID, subjectID, adminOne, adminTwo := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for id, label := range map[uuid.UUID]string{
		guardianID: "Responsável renovação expirada", adminOne: "Admin renovação expirada um", adminTwo: "Admin renovação expirada dois",
	} {
		if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth)
			VALUES($1,$2,$3,clock_timestamp(),'hash','1980-01-01')`, id, label, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	for _, adminID := range []uuid.UUID{adminOne, adminTwo} {
		if _, err = conn.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, adminID); err != nil {
			t.Fatal(err)
		}
	}
	insertVerifiedDependentFixture(t, ctx, conn, guardianID, subjectID, "Menor renovação expirada", time.Now().AddDate(-12, 0, 0))
	var relationshipRef uuid.UUID
	var originalExpiry time.Time
	if err = conn.QueryRow(ctx, `UPDATE guardian_authority_relationships SET verified_until=cutoff.value,review_due_at=cutoff.value
		FROM (SELECT clock_timestamp()+interval '1 second' AS value) cutoff WHERE subject_user_id=$1
		RETURNING public_ref,verified_until`, subjectID).Scan(&relationshipRef, &originalExpiry); err != nil {
		t.Fatal(err)
	}
	if _, err = q.SubmitGuardianAuthorityRenewal(ctx, dbgen.SubmitGuardianAuthorityRenewalParams{ActorID: guardianID,
		RelationshipRef: relationshipRef, ExpectedVersion: 2, ResponseCode: "NADA_MUDOU"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if reconciled, reconcileErr := q.ReconcileGuardianAuthorityCutoffs(ctx); reconcileErr != nil || reconciled < 1 {
		t.Fatalf("reconciled=%d err=%v", reconciled, reconcileErr)
	}
	var state string
	var version int64
	if err = conn.QueryRow(ctx, `SELECT state,version FROM guardian_authority_relationships WHERE public_ref=$1`, relationshipRef).Scan(&state, &version); err != nil {
		t.Fatal(err)
	}
	if state != "EXPIRED" || version != 4 {
		t.Fatalf("expired relationship state=%s version=%d", state, version)
	}

	inputs := []dbgen.AdminTransitionGuardianAuthorityParams{
		{ActorID: adminOne, RelationshipRef: relationshipRef, ExpectedVersion: 4, Action: "RENEW", EvidenceCategory: "CLUB_REGISTRATION_RECORD", ReasonCode: "RELATIONSHIP_CONFIRMED"},
		{ActorID: adminTwo, RelationshipRef: relationshipRef, ExpectedVersion: 4, Action: "RENEW", EvidenceCategory: "COURT_OR_LEGAL_AUTHORITY", ReasonCode: "RELATIONSHIP_CONFIRMED"},
	}
	errs := make(chan error, len(inputs))
	var wg sync.WaitGroup
	for _, input := range inputs {
		wg.Add(1)
		go func(input dbgen.AdminTransitionGuardianAuthorityParams) {
			defer wg.Done()
			parallel, openErr := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
			if openErr != nil {
				errs <- openErr
				return
			}
			defer parallel.Close(ctx)
			_, transitionErr := dbgen.New(parallel).AdminTransitionGuardianAuthority(ctx, input)
			errs <- transitionErr
		}(input)
	}
	wg.Wait()
	close(errs)
	successes, staleFailures := 0, 0
	for transitionErr := range errs {
		if transitionErr == nil {
			successes++
			continue
		}
		var databaseError *pgconn.PgError
		if errors.As(transitionErr, &databaseError) && databaseError.Message == "guardian_authority_stale" {
			staleFailures++
		} else {
			t.Fatalf("unexpected concurrent renewal error=%v", transitionErr)
		}
	}
	if successes != 1 || staleFailures != 1 {
		t.Fatalf("concurrent renewal successes=%d stale=%d", successes, staleFailures)
	}
	var renewedExpiry time.Time
	if err = conn.QueryRow(ctx, `SELECT state,version,verified_until FROM guardian_authority_relationships WHERE public_ref=$1`, relationshipRef).
		Scan(&state, &version, &renewedExpiry); err != nil {
		t.Fatal(err)
	}
	if state != "VERIFIED" || version != 5 || !renewedExpiry.Equal(originalExpiry.AddDate(1, 0, 0)) {
		t.Fatalf("renewed relationship state=%s version=%d expiry=%v want=%v", state, version, renewedExpiry, originalExpiry.AddDate(1, 0, 0))
	}

	rows, err := conn.Query(ctx, `SELECT event.action,event.actor_role,event.actor_ref,event.relationship_version,request.public_ref
		FROM guardian_authority_renewal_events event JOIN guardian_authority_renewal_requests request ON request.id=event.renewal_id
		JOIN guardian_authority_relationships relationship ON relationship.id=request.relationship_id
		WHERE relationship.public_ref=$1 ORDER BY event.id`, relationshipRef)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type renewalEvent struct {
		action, role string
		actor        *uuid.UUID
		version      int64
		requestRef   uuid.UUID
	}
	var events []renewalEvent
	for rows.Next() {
		var event renewalEvent
		if err = rows.Scan(&event.action, &event.role, &event.actor, &event.version, &event.requestRef); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].action != "SUBMITTED" || events[0].role != "GUARDIAN" || events[0].actor == nil || *events[0].actor != guardianID || events[0].version != 3 ||
		events[1].action != "APPROVED" || events[1].role != "ADMIN" || events[1].actor == nil || (*events[1].actor != adminOne && *events[1].actor != adminTwo) || events[1].version != 5 ||
		events[0].requestRef == uuid.Nil || events[0].requestRef != events[1].requestRef {
		t.Fatalf("renewal audit events=%+v", events)
	}
	if _, err = conn.Exec(ctx, `UPDATE guardian_authority_renewal_events SET actor_role='SYSTEM' WHERE renewal_id=(SELECT id FROM guardian_authority_renewal_requests WHERE public_ref=$1)`, events[0].requestRef); err == nil {
		t.Fatal("renewal event audit allowed mutation")
	}
}
