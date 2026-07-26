package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type fakeAdminStore struct {
	user            dbgen.User
	lookupErr       error
	created         dbgen.CreateAdultUserParams
	granted         dbgen.GrantPlatformRoleByCodeParams
	password        dbgen.SetUserPasswordHashParams
	deactivated     uuid.UUID
	admin           bool
	accounts        map[string]dbgen.GetAccountByEmailRow
	programme       dbgen.Programme
	team            dbgen.Team
	staffGrant      dbgen.GrantStaffCapabilityParams
	revocation      dbgen.RevokeStaffGrantParams
	revoked         int64
	minorCredential dbgen.IssueMinorCredentialParams
}

func (s *fakeAdminStore) GetAccountByEmail(_ context.Context, email *string) (dbgen.GetAccountByEmailRow, error) {
	if account, ok := s.accounts[*email]; ok {
		return account, nil
	}
	return dbgen.GetAccountByEmailRow{ID: s.user.ID, IsActive: s.user.IsActive, IsAdmin: s.admin}, s.lookupErr
}
func (s *fakeAdminStore) GrantPlatformRoleByCode(_ context.Context, input dbgen.GrantPlatformRoleByCodeParams) error {
	s.granted = input
	return nil
}
func (s *fakeAdminStore) CreateAdultUser(_ context.Context, input dbgen.CreateAdultUserParams) (dbgen.CreateAdultUserRow, error) {
	s.created = input
	return dbgen.CreateAdultUserRow{}, nil
}
func (s *fakeAdminStore) SetUserPasswordHash(_ context.Context, input dbgen.SetUserPasswordHashParams) error {
	s.password = input
	return nil
}
func (s *fakeAdminStore) DeactivateUser(_ context.Context, id uuid.UUID) error {
	s.deactivated = id
	return nil
}
func (s *fakeAdminStore) GetProgrammeByCode(context.Context, string) (dbgen.Programme, error) {
	return s.programme, nil
}
func (s *fakeAdminStore) GetTeamByID(context.Context, uuid.UUID) (dbgen.Team, error) {
	return s.team, nil
}
func (s *fakeAdminStore) GrantStaffCapability(_ context.Context, input dbgen.GrantStaffCapabilityParams) (dbgen.GrantStaffCapabilityRow, error) {
	s.staffGrant = input
	return dbgen.GrantStaffCapabilityRow{ID: uuid.New()}, nil
}
func (s *fakeAdminStore) RevokeStaffGrant(_ context.Context, input dbgen.RevokeStaffGrantParams) (int64, error) {
	s.revocation = input
	return s.revoked, nil
}
func (s *fakeAdminStore) IssueMinorCredential(_ context.Context, input dbgen.IssueMinorCredentialParams) (uuid.UUID, error) {
	s.minorCredential = input
	return input.MinorUserID, nil
}

func TestCreateAdminUsesValidatedInputAndPasswordFile(t *testing.T) {
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("correct horse 7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &fakeAdminStore{lookupErr: pgx.ErrNoRows}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"create", "--email", " ADMIN@EXAMPLE.COM ", "--name", " Admin   User ", "--date-of-birth", "1990-01-01"}, store, os.Stdin, passwordFile, &output); err != nil {
		t.Fatal(err)
	}
	if store.created.Email == nil || *store.created.Email != "admin@example.com" || store.granted.RoleCode != "ADMIN" {
		t.Fatalf("create input = %+v", store.created)
	}
	if store.created.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*store.created.PasswordHash), []byte("correct horse 7")) != nil {
		t.Fatal("created password was not bcrypt hashed")
	}
	if !strings.Contains(output.String(), "administrator created") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestIssueMinorLoginRequiresGuardianAndAuditsActor(t *testing.T) {
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("correct horse 7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	guardianID, actorID, minorID := uuid.New(), uuid.New(), uuid.New()
	store := &fakeAdminStore{accounts: map[string]dbgen.GetAccountByEmailRow{
		"guardian@example.com": {ID: guardianID, IsActive: true},
		"admin@example.com":    {ID: actorID, IsActive: true, IsAdmin: true},
	}}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"issue-minor-login", "--minor-id", minorID.String(), "--guardian-email", "guardian@example.com", "--actor-email", "admin@example.com"}, store, os.Stdin, passwordFile, &output); err != nil {
		t.Fatal(err)
	}
	if store.minorCredential.MinorUserID != minorID || store.minorCredential.GuardianUserID != guardianID || store.minorCredential.ActorUserID != actorID || store.minorCredential.Action != "ISSUED" {
		t.Fatalf("credential input = %+v", store.minorCredential)
	}
	if !strings.HasPrefix(output.String(), "minor login CFC-") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestSetPasswordUsesPasswordFile(t *testing.T) {
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("another horse 8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	store := &fakeAdminStore{user: dbgen.User{ID: id, IsActive: true}, admin: true}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"set-password", "--email", "admin@example.com"}, store, os.Stdin, passwordFile, &output); err != nil {
		t.Fatal(err)
	}
	if store.password.ID != id || store.password.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*store.password.PasswordHash), []byte("another horse 8")) != nil {
		t.Fatalf("password input = %+v", store.password)
	}
}

func TestAdminCommandsAreIdempotentWhereSafe(t *testing.T) {
	id := uuid.New()
	store := &fakeAdminStore{user: dbgen.User{ID: id, IsActive: false}, admin: true}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"create", "--email", "admin@example.com", "--name", "Admin User", "--date-of-birth", "1990-01-01"}, store, os.Stdin, "", &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "already exists") {
		t.Fatalf("output = %q", output.String())
	}
	output.Reset()
	if err := run(context.Background(), []string{"deactivate", "--email", "admin@example.com"}, store, os.Stdin, "", &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "already inactive") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestReadPasswordRejectsNonTerminalWithoutFile(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if _, err := readPassword(reader, ""); err == nil || !strings.Contains(err.Error(), "non-terminal") {
		t.Fatalf("error = %v", err)
	}
}

func TestGrantStaffValidatesScopesAndActor(t *testing.T) {
	targetID, actorID, programmeID, teamID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	store := &fakeAdminStore{accounts: map[string]dbgen.GetAccountByEmailRow{
		"coach@example.com": {ID: targetID, IsActive: true},
		"admin@example.com": {ID: actorID, IsActive: true, IsAdmin: true},
	}, programme: dbgen.Programme{ID: programmeID}, team: dbgen.Team{ID: teamID}}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"grant-staff", "--email", "coach@example.com", "--actor-email", "admin@example.com", "--capability", "coach", "--programme", "Competition"}, store, os.Stdin, "", &output); err != nil {
		t.Fatal(err)
	}
	if store.staffGrant.UserID != targetID || store.staffGrant.GrantedByID != actorID || store.staffGrant.Capability != dbgen.StaffCapabilityCOACH || store.staffGrant.ProgrammeID == nil || *store.staffGrant.ProgrammeID != programmeID || store.staffGrant.TeamID != nil {
		t.Fatalf("grant = %+v", store.staffGrant)
	}
	if err := run(context.Background(), []string{"grant-staff", "--email", "coach@example.com", "--actor-email", "admin@example.com", "--capability", "coach", "--programme", "Competition", "--team-id", teamID.String()}, store, os.Stdin, "", &output); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("mixed coach scope error = %v", err)
	}
	if err := run(context.Background(), []string{"grant-staff", "--email", "coach@example.com", "--actor-email", "admin@example.com", "--capability", "moderator", "--programme", "Competition"}, store, os.Stdin, "", &output); err == nil || !strings.Contains(err.Error(), "cannot have") {
		t.Fatalf("moderator scope error = %v", err)
	}
	store.accounts["admin@example.com"] = dbgen.GetAccountByEmailRow{ID: actorID, IsActive: true}
	if err := run(context.Background(), []string{"grant-staff", "--email", "coach@example.com", "--actor-email", "admin@example.com", "--capability", "moderator"}, store, os.Stdin, "", &output); err == nil || !strings.Contains(err.Error(), "active administrator") {
		t.Fatalf("non-admin actor error = %v", err)
	}
}

func TestGrantStaffSupportsTeamAndModerator(t *testing.T) {
	targetID, actorID, teamID := uuid.New(), uuid.New(), uuid.New()
	store := &fakeAdminStore{accounts: map[string]dbgen.GetAccountByEmailRow{
		"coach@example.com": {ID: targetID, IsActive: true},
		"admin@example.com": {ID: actorID, IsActive: true, IsAdmin: true},
	}, team: dbgen.Team{ID: teamID}}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"grant-staff", "--email", "coach@example.com", "--actor-email", "admin@example.com", "--capability", "coach", "--team-id", teamID.String()}, store, os.Stdin, "", &output); err != nil {
		t.Fatal(err)
	}
	if store.staffGrant.TeamID == nil || *store.staffGrant.TeamID != teamID || store.staffGrant.ProgrammeID != nil {
		t.Fatalf("team grant = %+v", store.staffGrant)
	}
	if err := run(context.Background(), []string{"grant-staff", "--email", "coach@example.com", "--actor-email", "admin@example.com", "--capability", "moderator"}, store, os.Stdin, "", &output); err != nil {
		t.Fatal(err)
	}
	if store.staffGrant.Capability != dbgen.StaffCapabilityMODERATOR || store.staffGrant.TeamID != nil || store.staffGrant.ProgrammeID != nil {
		t.Fatalf("moderator grant = %+v", store.staffGrant)
	}
}

func TestRevokeStaffRequiresActiveGrantAndReason(t *testing.T) {
	actorID, grantID := uuid.New(), uuid.New()
	store := &fakeAdminStore{accounts: map[string]dbgen.GetAccountByEmailRow{"admin@example.com": {ID: actorID, IsActive: true, IsAdmin: true}}, revoked: 1}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"revoke-staff", "--grant-id", grantID.String(), "--actor-email", "admin@example.com", "--reason", "  Alteração de equipa  "}, store, os.Stdin, "", &output); err != nil {
		t.Fatal(err)
	}
	if store.revocation.ID != grantID || store.revocation.RevokedByID == nil || *store.revocation.RevokedByID != actorID || store.revocation.RevokeReason == nil || *store.revocation.RevokeReason != "Alteração de equipa" {
		t.Fatalf("revocation = %+v", store.revocation)
	}
	if err := run(context.Background(), []string{"revoke-staff", "--grant-id", grantID.String(), "--actor-email", "admin@example.com", "--reason", " "}, store, os.Stdin, "", &output); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("empty reason error = %v", err)
	}
	store.revoked = 0
	if err := run(context.Background(), []string{"revoke-staff", "--grant-id", grantID.String(), "--actor-email", "admin@example.com", "--reason", "No longer needed"}, store, os.Stdin, "", &output); err == nil || !strings.Contains(err.Error(), "active staff grant") {
		t.Fatalf("inactive grant error = %v", err)
	}
}
