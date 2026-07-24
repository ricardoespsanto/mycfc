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
	user        dbgen.User
	lookupErr   error
	created     dbgen.CreateAdultUserParams
	password    dbgen.SetUserPasswordHashParams
	deactivated uuid.UUID
}

func (s *fakeAdminStore) GetUserByEmail(context.Context, *string) (dbgen.User, error) {
	return s.user, s.lookupErr
}
func (s *fakeAdminStore) CreateAdultUser(_ context.Context, input dbgen.CreateAdultUserParams) (dbgen.User, error) {
	s.created = input
	return dbgen.User{}, nil
}
func (s *fakeAdminStore) SetUserPasswordHash(_ context.Context, input dbgen.SetUserPasswordHashParams) error {
	s.password = input
	return nil
}
func (s *fakeAdminStore) DeactivateUser(_ context.Context, id uuid.UUID) error {
	s.deactivated = id
	return nil
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
	if store.created.Email == nil || *store.created.Email != "admin@example.com" || store.created.Role != "Admin" || store.created.SquadCategory != "None" {
		t.Fatalf("create input = %+v", store.created)
	}
	if store.created.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*store.created.PasswordHash), []byte("correct horse 7")) != nil {
		t.Fatal("created password was not bcrypt hashed")
	}
	if !strings.Contains(output.String(), "administrator created") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestSetPasswordUsesPasswordFile(t *testing.T) {
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("another horse 8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	store := &fakeAdminStore{user: dbgen.User{ID: id, Role: "Admin", IsActive: true}}
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
	store := &fakeAdminStore{user: dbgen.User{ID: id, Role: "Admin", IsActive: false}}
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
