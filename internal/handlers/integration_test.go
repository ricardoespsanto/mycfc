//go:build integration

package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/emailverification"
	"github.com/cfcoimbra/mycfc/internal/passwordreset"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func TestPostgresRegistrationStorePersistsConsentsAtomically(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	store := PostgresRegistrationStore{Pool: pool}
	email := "registration-" + uuid.NewString() + "@example.test"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE email = $1`, email)
	})

	input := RegistrationInput{
		Name: "Pessoa de integração", Email: email, PasswordHash: "hash",
		DateOfBirth:  time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
		TermsVersion: "test-v1", TermsSHA256: strings.Repeat("a", 64),
		ImageVersion: "test-v1", ImageSHA256: strings.Repeat("b", 64),
		IP: ptrAddr(netip.MustParseAddr("192.0.2.1")), UserAgent: "integration-test",
	}
	result, err := store.RegisterAdult(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	user, err := queries.GetUserByID(ctx, result.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if user.Email == nil || *user.Email != email || user.IsDependent {
		t.Fatalf("created user = %#v", user)
	}
	consents, err := queries.ListConsentFormsForUser(ctx, dbgen.ListConsentFormsForUserParams{UserID: user.ID, RowLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(consents) != 2 {
		t.Fatalf("consent count = %d, want 2", len(consents))
	}
	for _, consent := range consents {
		if consent.GrantedByUserID == nil || *consent.GrantedByUserID != user.ID || !consent.IsAccepted {
			t.Fatalf("consent = %#v", consent)
		}
	}
	var tokenID uuid.UUID
	var outboxStatus string
	var verifiedAt pgtype.Timestamptz
	if err := pool.QueryRow(ctx, `SELECT token.id, outbox.status, account.email_verified_at
		FROM email_verification_tokens token
		JOIN email_outbox outbox ON outbox.verification_token_id = token.id
		JOIN users account ON account.id = token.user_id
		WHERE token.user_id = $1`, user.ID).Scan(&tokenID, &outboxStatus, &verifiedAt); err != nil {
		t.Fatal(err)
	}
	if outboxStatus != "PENDING" || verifiedAt.Valid {
		t.Fatalf("verification state = status %q, verified %#v", outboxStatus, verifiedAt)
	}
	verification := emailverification.Service{Store: queries, BaseURL: "https://mycfc.example", Key: []byte("0123456789abcdef0123456789abcdef")}
	if verifiedID, err := verification.Verify(ctx, tokenID.String(), verification.Signature(tokenID)); err != nil || verifiedID != user.ID {
		t.Fatalf("verify = %s, %v", verifiedID, err)
	}
	if err := pool.QueryRow(ctx, `SELECT email_verified_at FROM users WHERE id = $1`, user.ID).Scan(&verifiedAt); err != nil || !verifiedAt.Valid {
		t.Fatalf("verified timestamp = %#v, %v", verifiedAt, err)
	}

	rollbackEmail := "registration-rollback-" + uuid.NewString() + "@example.test"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE email = $1`, rollbackEmail)
	})
	input.Email = rollbackEmail
	input.ImageVersion = strings.Repeat("x", 41)
	if _, err := store.RegisterAdult(ctx, input); err == nil {
		t.Fatal("RegisterAdult succeeded with an invalid consent version")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email = $1`, rollbackEmail).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rolled back user count = %d, want 0", count)
	}
}

func TestPostgresProfileStoreEmailChangeInvalidatesVerificationAtomically(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	email := "profile-verification-" + uuid.NewString() + "@example.test"
	account, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: "Pessoa verificada", Email: &email, PasswordHash: integrationStringPtr("hash"), DateOfBirth: pgtype.Date{Time: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, account.ID) })
	if _, err := pool.Exec(ctx, `UPDATE users SET email_verified_at = now() WHERE id = $1`, account.ID); err != nil {
		t.Fatal(err)
	}
	if err := queries.EnsureMemberProfile(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	profile, err := queries.GetMemberProfile(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	newEmail := "profile-verification-new-" + uuid.NewString() + "@example.test"
	store := PostgresProfileStore{Pool: pool, Now: func() time.Time { return time.Now().UTC().Add(time.Minute) }}
	err = store.Update(ctx, ProfileUpdate{
		ActorID: account.ID, SubjectID: account.ID, IsAdmin: true,
		Profile:        dbgen.UpdateMemberProfileParams{UserID: account.ID, MedicalDeclaration: "UNKNOWN", ExpectedUpdatedAt: profile.UpdatedAt},
		Identity:       &dbgen.UpdateMemberIdentityParams{Name: profile.Name, Email: &newEmail, DateOfBirth: profile.DateOfBirth, ExpectedUpdatedAt: profile.IdentityUpdatedAt},
		IdentityFields: []string{"email"},
		ChangedFields:  []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var activeEmail string
	var verifiedAt pgtype.Timestamptz
	var activeTokens, cancelledOutbox int
	if err := pool.QueryRow(ctx, `SELECT email, email_verified_at FROM users WHERE id = $1`, account.ID).Scan(&activeEmail, &verifiedAt); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_verification_tokens WHERE user_id = $1 AND consumed_at IS NULL`, account.ID).Scan(&activeTokens); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox outbox JOIN email_verification_tokens token ON token.id = outbox.verification_token_id WHERE token.user_id = $1 AND outbox.status = 'CANCELLED'`, account.ID).Scan(&cancelledOutbox); err != nil {
		t.Fatal(err)
	}
	if activeEmail != newEmail || verifiedAt.Valid || activeTokens != 1 || cancelledOutbox != 1 {
		t.Fatalf("email=%q verified=%v active_tokens=%d cancelled=%d", activeEmail, verifiedAt.Valid, activeTokens, cancelledOutbox)
	}
}

func TestEmailVerificationResendThrottleSerializesConcurrentRequests(t *testing.T) {
	ctx, pool := integrationPool(t)
	userID := uuid.New()
	email := "verification-throttle-" + uuid.NewString() + "@example.test"
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, email_verified_at, password_hash, date_of_birth) VALUES ($1, 'Pessoa concorrente', $2, now(), 'hash', '1990-01-01')`, userID, email); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) })
	service := emailverification.Service{Store: dbgen.New(pool)}
	start := make(chan struct{})
	errorsSeen := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := service.Issue(context.Background(), userID, email, true)
			errorsSeen <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsSeen)
	var succeeded, throttled int
	for err := range errorsSeen {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, emailverification.ErrTooSoon):
			throttled++
		default:
			t.Fatalf("unexpected issue error: %v", err)
		}
	}
	var active int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_verification_tokens WHERE user_id = $1 AND consumed_at IS NULL`, userID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if succeeded != 1 || throttled != 1 || active != 1 {
		t.Fatalf("succeeded=%d throttled=%d active=%d", succeeded, throttled, active)
	}
}

func TestPostgresGuardianDependentStorePersistsResponsibilityAndEnforcesLimit(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	guardianEmail := "guardian-" + uuid.NewString() + "@example.test"
	guardian, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{
		Name: "Guardião de integração", Email: &guardianEmail, PasswordHash: integrationStringPtr("hash"),
		DateOfBirth: pgtype.Date{Time: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE guardian_id = $1`, guardian.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, guardian.ID)
	})

	store := PostgresGuardianDependentStore{Pool: pool}
	input := GuardianDependentInput{
		GuardianID: guardian.ID, Name: "Menor de integração",
		DateOfBirth:           time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC),
		ResponsibilityVersion: "test-v1", ResponsibilitySHA256: strings.Repeat("c", 64),
		IP: ptrAddr(netip.MustParseAddr("192.0.2.2")), UserAgent: "integration-test",
	}
	if err := store.CreateDependent(ctx, input); err != nil {
		t.Fatal(err)
	}
	dependents, err := queries.ListDependentsByGuardian(ctx, dbgen.ListDependentsByGuardianParams{GuardianID: &guardian.ID, RowLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(dependents) != 1 || dependents[0].GuardianID == nil || *dependents[0].GuardianID != guardian.ID {
		t.Fatalf("dependents = %#v", dependents)
	}
	consents, err := queries.ListConsentFormsForUser(ctx, dbgen.ListConsentFormsForUserParams{UserID: dependents[0].ID, RowLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(consents) != 1 || consents[0].ConsentType != "Responsabilidade_Menor" || consents[0].GrantedByUserID == nil || *consents[0].GrantedByUserID != guardian.ID {
		t.Fatalf("responsibility consent = %#v", consents)
	}

	for i := 1; i < 10; i++ {
		input.Name = "Menor de integração " + string(rune('A'+i))
		if err := store.CreateDependent(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	input.Name = "Menor excedente"
	if err := store.CreateDependent(ctx, input); !errors.Is(err, ErrMaximumDependents) {
		t.Fatalf("limit error = %v, want %v", err, ErrMaximumDependents)
	}
	count, err := queries.CountDependentsByGuardian(ctx, &guardian.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 10 {
		t.Fatalf("active dependant count = %d, want 10", count)
	}
}

func TestMinorCredentialRequiresCurrentGuardianAndWritesAudit(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	guardianEmail := "credential-guardian-" + uuid.NewString() + "@example.test"
	actorEmail := "credential-admin-" + uuid.NewString() + "@example.test"
	guardian, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: "Guardião de credencial", Email: &guardianEmail, PasswordHash: integrationStringPtr("hash"), DateOfBirth: pgtype.Date{Time: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: "Administrador de credencial", Email: &actorEmail, PasswordHash: integrationStringPtr("hash"), DateOfBirth: pgtype.Date{Time: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM minor_credential_audit WHERE guardian_user_id = $1`, guardian.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE guardian_id = $1`, guardian.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id IN ($1, $2)`, guardian.ID, actor.ID)
	})
	if err := queries.GrantPlatformRoleByCode(ctx, dbgen.GrantPlatformRoleByCodeParams{UserID: actor.ID, RoleCode: "ADMIN"}); err != nil {
		t.Fatal(err)
	}
	minor, err := queries.CreateDependentUser(ctx, dbgen.CreateDependentUserParams{Name: "Menor com credencial", GuardianID: &guardian.ID, DateOfBirth: pgtype.Date{Time: time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	loginID, passwordHash := "CFC-TEST0001", "hash"
	if _, err := queries.IssueMinorCredential(ctx, dbgen.IssueMinorCredentialParams{MinorLoginID: &loginID, PasswordHash: &passwordHash, MinorUserID: minor.ID, GuardianUserID: uuid.New(), ActorUserID: actor.ID, Action: "ISSUED"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("wrong guardian error = %v", err)
	}
	if _, err := queries.IssueMinorCredential(ctx, dbgen.IssueMinorCredentialParams{MinorLoginID: &loginID, PasswordHash: &passwordHash, MinorUserID: minor.ID, GuardianUserID: guardian.ID, ActorUserID: actor.ID, Action: "ISSUED"}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.GetActiveDependentByLoginID(ctx, &loginID); err != nil {
		t.Fatalf("issued minor cannot log in: %v", err)
	}
	var credentialVersion int64
	if err := pool.QueryRow(ctx, `SELECT credential_version FROM users WHERE id = $1`, minor.ID).Scan(&credentialVersion); err != nil || credentialVersion != 2 {
		t.Fatalf("issued credential version = %d, err = %v", credentialVersion, err)
	}
	recoveredHash := "recovered-hash"
	if _, err := queries.IssueMinorCredential(ctx, dbgen.IssueMinorCredentialParams{MinorLoginID: &loginID, PasswordHash: &recoveredHash, MinorUserID: minor.ID, GuardianUserID: guardian.ID, ActorUserID: actor.ID, Action: "RECOVERED"}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT credential_version FROM users WHERE id = $1`, minor.ID).Scan(&credentialVersion); err != nil || credentialVersion != 3 {
		t.Fatalf("recovered credential version = %d, err = %v", credentialVersion, err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM minor_credential_audit WHERE minor_user_id = $1`, minor.ID).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("audit count = %d, err = %v", auditCount, err)
	}
}

func TestAdministratorPasswordReplacementRevokesOlderSessions(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	email := "credential-admin-reset-" + uuid.NewString() + "@example.test"
	account, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: "Administrador direto", Email: &email, PasswordHash: integrationStringPtr("old-hash"), DateOfBirth: pgtype.Date{Time: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, account.ID) })
	if err := queries.GrantPlatformRoleByCode(ctx, dbgen.GrantPlatformRoleByCodeParams{UserID: account.ID, RoleCode: "ADMIN"}); err != nil {
		t.Fatal(err)
	}
	newHash := "new-hash"
	if err := queries.SetUserPasswordHash(ctx, dbgen.SetUserPasswordHashParams{ID: account.ID, PasswordHash: &newHash}); err != nil {
		t.Fatal(err)
	}
	var storedHash string
	var credentialVersion int64
	if err := pool.QueryRow(ctx, `SELECT password_hash, credential_version FROM users WHERE id = $1`, account.ID).Scan(&storedHash, &credentialVersion); err != nil {
		t.Fatal(err)
	}
	if storedHash != newHash || credentialVersion != 2 {
		t.Fatalf("administrator credential = %q, version %d", storedHash, credentialVersion)
	}
}

func TestPasswordResetFlowRejectsPreResetSession(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	email := "session-reset-" + uuid.NewString() + "@example.test"
	oldPassword, newPassword := "old password 7", "new password 8"
	oldHash, err := bcrypt.GenerateFromPassword([]byte(oldPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	account, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: "Sessão anterior", Email: &email, PasswordHash: integrationStringPtr(string(oldHash)), DateOfBirth: pgtype.Date{Time: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, account.ID) })

	sessions := scs.New()
	auth := Auth{Users: queries, Sessions: sessions}
	protected := sessions.LoadAndSave(auth.Load(auth.RequireAuthenticated(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))))
	seed := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		sessions.Put(r.Context(), "user_id", account.ID.String())
		sessions.Put(r.Context(), "credential_version", account.CredentialVersion)
	})).ServeHTTP(seed, httptest.NewRequest(http.MethodGet, "/", nil))
	cookie := seed.Result().Cookies()[0]
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("pre-reset session status = %d", response.Code)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	rawToken := bytes.Repeat([]byte{0x42}, passwordreset.TokenBytes)
	service := passwordreset.Service{
		Store: queries, BaseURL: "https://mycfc.example", Key: []byte("0123456789abcdef0123456789abcdef"),
		Rand: bytes.NewReader(append(append([]byte{}, rawToken...), bytes.Repeat([]byte{0x24}, 12)...)), Now: func() time.Time { return now },
	}
	if _, err := service.Issue(ctx, email, false); err != nil {
		t.Fatal(err)
	}
	var outboxStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM email_outbox outbox JOIN password_reset_tokens token ON token.id = outbox.password_reset_token_id WHERE token.user_id = $1`, account.ID).Scan(&outboxStatus); err != nil || outboxStatus != "PENDING" {
		t.Fatalf("outbox status = %q, err = %v", outboxStatus, err)
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	if _, err := service.Consume(ctx, token, newPassword); err != nil {
		t.Fatal(err)
	}

	request = httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login?next=%2Fprotected" {
		t.Fatalf("post-reset session response = %d %q", response.Code, response.Header().Get("Location"))
	}
	var storedHash string
	var credentialVersion int64
	if err := pool.QueryRow(ctx, `SELECT password_hash, credential_version FROM users WHERE id = $1`, account.ID).Scan(&storedHash, &credentialVersion); err != nil {
		t.Fatal(err)
	}
	if credentialVersion != account.CredentialVersion+1 || bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(newPassword)) != nil || bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(oldPassword)) == nil {
		t.Fatalf("password reset did not replace credential at version %d", credentialVersion)
	}
}

func TestPostgresProfileStoreEnforcesGuardianConsentConflictAndAudit(t *testing.T) {
	ctx, pool := integrationPool(t)
	guardianID, dependentID, unrelatedID := uuid.New(), uuid.New(), uuid.New()
	for id, email := range map[uuid.UUID]string{guardianID: "guardian-" + uuid.NewString() + "@example.test", unrelatedID: "unrelated-" + uuid.NewString() + "@example.test"} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, 'Adulto perfil', $2, 'hash', '1990-01-01')`, id, email); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, guardian_id, is_dependent, date_of_birth) VALUES ($1, 'Menor perfil', $2, true, '2014-01-01')`, dependentID, guardianID); err != nil {
		t.Fatal(err)
	}
	store := PostgresProfileStore{Pool: pool}
	profile, err := store.View(ctx, guardianID, dependentID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.View(ctx, unrelatedID, dependentID, false); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unrelated view error = %v", err)
	}
	params := dbgen.UpdateMemberProfileParams{EmergencyContactName: "Responsável", EmergencyContactRelationship: "Tutor", EmergencyContactPhone: "+351 910 000 000", MedicalDeclaration: "NONE_KNOWN", ExpectedUpdatedAt: profile.UpdatedAt}
	if err := store.Update(ctx, ProfileUpdate{ActorID: guardianID, SubjectID: dependentID, Profile: params, ChangedFields: []string{"emergency_contact_name", "emergency_contact_relationship", "emergency_contact_phone", "medical_declaration"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, ProfileUpdate{ActorID: guardianID, SubjectID: dependentID, Profile: params}); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	consentVersion, consentSHA := "profile-v1", strings.Repeat("c", 64)
	key := "profiles/integration/photo.png"
	if _, err := store.SavePhoto(ctx, ProfilePhotoUpdate{ActorID: guardianID, SubjectID: dependentID, ObjectKey: key, ContentType: "image/png", Size: 128, ConsentVersion: consentVersion, ConsentSHA256: consentSHA, AcceptConsent: true, UserAgent: "integration-test"}); err != nil {
		t.Fatal(err)
	}
	avatar, err := store.Avatar(ctx, dbgen.GetMemberAvatarParams{UserID: dependentID, DocumentVersion: consentVersion, DocumentSha256: consentSHA})
	if err != nil || avatar.PhotoObjectKey == nil || *avatar.PhotoObjectKey != key || !avatar.ConsentCurrent {
		t.Fatalf("avatar = %#v, err = %v", avatar, err)
	}
	if _, err := store.RemovePhoto(ctx, guardianID, dependentID, false); err != nil {
		t.Fatal(err)
	}
	var actions []string
	rows, err := pool.Query(ctx, `SELECT action FROM member_profile_audit_events WHERE subject_user_id = $1 ORDER BY occurred_at, id`, dependentID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	joined := strings.Join(actions, ",")
	for _, action := range []string{"SENSITIVE_VIEW", "PROFILE_UPDATED", "PHOTO_UPLOADED", "PHOTO_REMOVED"} {
		if !strings.Contains(joined, action) {
			t.Fatalf("audit actions = %v, missing %s", actions, action)
		}
	}
}

func integrationPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return ctx, pool
}

func ptrAddr(value netip.Addr) *netip.Addr { return &value }

func integrationStringPtr(value string) *string { return &value }
