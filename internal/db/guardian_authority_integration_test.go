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

	evidenceType, evidenceRef := "CIVIL_REGISTRY", "registry/"+uuid.NewString()
	verified, err := q.TransitionGuardianAuthority(ctx, dbgen.TransitionGuardianAuthorityParams{ActorID: verifierID,
		RelationshipRef: pending.RelationshipRef, ExpectedVersion: 1, TargetState: "VERIFIED",
		EvidenceType: &evidenceType, EvidenceReference: &evidenceRef, EvidenceSha256: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if verified.Version != 2 || verified.State != "VERIFIED" {
		t.Fatalf("verified relationship = %+v", verified)
	}
	if current, err := q.HasVerifiedGuardianAuthority(ctx, guardianID); err != nil || !current {
		t.Fatalf("verified relationship unavailable: %v, %v", current, err)
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
	privacySubject, err := q.GetPrivacyAccountForUpdate(ctx, dependent.ID)
	if err != nil || privacySubject.GuardianID == nil || *privacySubject.GuardianID != guardianID || !privacySubject.UpdatedAt.Time.Equal(verified.UpdatedAt.Time) {
		t.Fatalf("privacy projection = %+v err=%v relationship=%+v", privacySubject, err, verified)
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

	// A fresh verifier can resolve the conflict; the verifier who recorded it cannot self-clear it.
	if _, err = q.TransitionGuardianAuthority(ctx, dbgen.TransitionGuardianAuthorityParams{ActorID: verifierID,
		RelationshipRef: pending.RelationshipRef, ExpectedVersion: 3, TargetState: "VERIFIED",
		EvidenceType: &evidenceType, EvidenceReference: &evidenceRef, EvidenceSha256: make([]byte, 32)}); err == nil {
		t.Fatal("conflict actor cleared their own conflict")
	}
	secondVerifier := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Segunda verificadora',$2,'hash','1980-01-01')`, secondVerifier, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = q.GrantGuardianVerifier(ctx, dbgen.GrantGuardianVerifierParams{ActorID: grantorID, UserID: secondVerifier}); err != nil {
		t.Fatal(err)
	}
	verified, err = q.TransitionGuardianAuthority(ctx, dbgen.TransitionGuardianAuthorityParams{ActorID: secondVerifier,
		RelationshipRef: pending.RelationshipRef, ExpectedVersion: 3, TargetState: "VERIFIED",
		EvidenceType: &evidenceType, EvidenceReference: &evidenceRef, EvidenceSha256: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `UPDATE guardian_authority_relationships SET
		verified_at=clock_timestamp()-interval '2 days',review_due_at=clock_timestamp()-interval '1 day'
		WHERE id=$1`, verified.ID); err != nil {
		t.Fatal(err)
	}
	expired, err := q.TransitionGuardianAuthority(ctx, dbgen.TransitionGuardianAuthorityParams{ActorID: secondVerifier,
		RelationshipRef: pending.RelationshipRef, ExpectedVersion: 4, TargetState: "EXPIRED"})
	if err != nil || expired.State != "EXPIRED" || expired.Version != 5 {
		t.Fatalf("explicit expiry = %+v err=%v", expired, err)
	}
	queue, err := q.ListPendingGuardianAuthorityRequests(ctx, dbgen.ListPendingGuardianAuthorityRequestsParams{ActorID: secondVerifier, RowLimit: 20})
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
		EvidenceType: &evidenceType, EvidenceReference: &evidenceRef, EvidenceSha256: make([]byte, 32)}); err != nil {
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
		EvidenceReference: &evidenceRef, EvidenceSha256: make([]byte, 32)}); err == nil {
		t.Fatal("rejected relationship was not terminal")
	}

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
	if retainedLogin == nil || retainedHash == nil || *retainedLogin != minorLogin || *retainedHash != minorHash {
		t.Fatalf("dependent credential was not preserved at majority: login=%v hash_set=%t", retainedLogin, retainedHash != nil)
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
