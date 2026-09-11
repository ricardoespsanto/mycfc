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
	if _, err = conn.Exec(ctx, `UPDATE guardian_authority_policies
		SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled`); err != nil {
		t.Fatal(err)
	}

	guardianID, verifierID, grantorID := uuid.New(), uuid.New(), uuid.New()
	for id, label := range map[uuid.UUID]string{guardianID: "Responsável em teste", verifierID: "Verificadora em teste", grantorID: "Concedente em teste"} {
		if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,$2,$3,'hash','1980-01-01')`, id, label, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = conn.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id)
		SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, grantorID); err != nil {
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
	}); err != nil {
		t.Fatal(err)
	}
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
	assertCleared(secondToken, secondVersion)
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
