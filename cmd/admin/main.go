package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
)

type adminStore interface {
	GetUserByEmail(context.Context, *string) (dbgen.User, error)
	CreateAdultUser(context.Context, dbgen.CreateAdultUserParams) (dbgen.User, error)
	SetUserPasswordHash(context.Context, dbgen.SetUserPasswordHashParams) error
	DeactivateUser(context.Context, uuid.UUID) error
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		fail(err)
	}
	databaseURL, err := cfg.ResolvedDatabaseURL()
	if err != nil {
		fail(err)
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		fail(fmt.Errorf("open database pool: %w", err))
	}
	defer pool.Close()
	if err := run(context.Background(), os.Args[1:], dbgen.New(pool), os.Stdin, os.Getenv("MYCFC_ADMIN_PASSWORD_FILE"), os.Stdout); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "admin:", err)
	os.Exit(1)
}

func run(ctx context.Context, args []string, store adminStore, stdin *os.File, passwordFile string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: admin <create|set-password|deactivate> [options]")
	}
	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		email := flags.String("email", "", "")
		name := flags.String("name", "", "")
		dateOfBirth := flags.String("date-of-birth", "", "")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return errors.New("usage: admin create --email ... --name ... --date-of-birth YYYY-MM-DD")
		}
		return createAdmin(ctx, store, stdin, passwordFile, output, *email, *name, *dateOfBirth)
	case "set-password":
		email, err := emailFlag("set-password", args[1:])
		if err != nil {
			return err
		}
		return setPassword(ctx, store, stdin, passwordFile, output, email)
	case "deactivate":
		email, err := emailFlag("deactivate", args[1:])
		if err != nil {
			return err
		}
		return deactivate(ctx, store, output, email)
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func emailFlag(command string, args []string) (string, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	email := flags.String("email", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return "", fmt.Errorf("usage: admin %s --email ...", command)
	}
	return validation.NormalizeEmail(*email)
}

func createAdmin(ctx context.Context, store adminStore, stdin *os.File, passwordFile string, output io.Writer, rawEmail, rawName, rawDateOfBirth string) error {
	email, err := validation.NormalizeEmail(rawEmail)
	if err != nil {
		return err
	}
	name, err := validation.NormalizeName(rawName)
	if err != nil {
		return err
	}
	birth, err := validation.ParseISODate(rawDateOfBirth)
	if err != nil {
		return err
	}
	location, err := time.LoadLocation("Europe/Lisbon")
	if err != nil {
		return err
	}
	if err := validation.ValidateAdultDateOfBirth(birth, time.Now(), location); err != nil {
		return err
	}
	user, err := store.GetUserByEmail(ctx, &email)
	if err == nil {
		if user.Role == "Admin" && !user.IsDependent {
			fmt.Fprintln(output, "administrator already exists")
			return nil
		}
		return errors.New("email already belongs to a non-administrator account")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("look up administrator: %w", err)
	}
	password, err := readPassword(stdin, passwordFile)
	if err != nil {
		return err
	}
	if err := validation.ValidatePassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if _, err := store.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: name, Email: &email, PasswordHash: ptr(string(hash)), Role: "Admin", SquadCategory: "None", DateOfBirth: pgtype.Date{Time: birth, Valid: true}}); err != nil {
		return fmt.Errorf("create administrator: %w", err)
	}
	fmt.Fprintln(output, "administrator created")
	return nil
}

func setPassword(ctx context.Context, store adminStore, stdin *os.File, passwordFile string, output io.Writer, rawEmail string) error {
	email, err := validation.NormalizeEmail(rawEmail)
	if err != nil {
		return err
	}
	user, err := store.GetUserByEmail(ctx, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("administrator not found")
	}
	if err != nil {
		return fmt.Errorf("look up administrator: %w", err)
	}
	if user.Role != "Admin" || user.IsDependent {
		return errors.New("email does not belong to an administrator")
	}
	password, err := readPassword(stdin, passwordFile)
	if err != nil {
		return err
	}
	if err := validation.ValidatePassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := store.SetUserPasswordHash(ctx, dbgen.SetUserPasswordHashParams{ID: user.ID, PasswordHash: ptr(string(hash))}); err != nil {
		return fmt.Errorf("set administrator password: %w", err)
	}
	fmt.Fprintln(output, "administrator password updated")
	return nil
}

func deactivate(ctx context.Context, store adminStore, output io.Writer, rawEmail string) error {
	email, err := validation.NormalizeEmail(rawEmail)
	if err != nil {
		return err
	}
	user, err := store.GetUserByEmail(ctx, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("administrator not found")
	}
	if err != nil {
		return fmt.Errorf("look up administrator: %w", err)
	}
	if user.Role != "Admin" || user.IsDependent {
		return errors.New("email does not belong to an administrator")
	}
	if !user.IsActive {
		fmt.Fprintln(output, "administrator already inactive")
		return nil
	}
	if err := store.DeactivateUser(ctx, user.ID); err != nil {
		return fmt.Errorf("deactivate administrator: %w", err)
	}
	fmt.Fprintln(output, "administrator deactivated")
	return nil
}

func readPassword(stdin *os.File, passwordFile string) (string, error) {
	if passwordFile != "" {
		contents, err := os.ReadFile(passwordFile)
		if err != nil {
			return "", fmt.Errorf("read password file: %w", err)
		}
		return strings.TrimSuffix(strings.TrimSuffix(string(contents), "\n"), "\r"), nil
	}
	if !term.IsTerminal(int(stdin.Fd())) {
		return "", errors.New("refusing password input from a non-terminal; set MYCFC_ADMIN_PASSWORD_FILE")
	}
	fmt.Fprint(os.Stderr, "Password: ")
	first, err := term.ReadPassword(int(stdin.Fd()))
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "\nConfirm password: ")
	second, err := term.ReadPassword(int(stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("passwords do not match")
	}
	return string(first), nil
}

func ptr(value string) *string { return &value }
