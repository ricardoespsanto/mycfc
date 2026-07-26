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
	"unicode/utf8"

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
	GetAccountByEmail(context.Context, *string) (dbgen.GetAccountByEmailRow, error)
	CreateAdultUser(context.Context, dbgen.CreateAdultUserParams) (dbgen.User, error)
	GrantPlatformRoleByCode(context.Context, dbgen.GrantPlatformRoleByCodeParams) error
	SetUserPasswordHash(context.Context, dbgen.SetUserPasswordHashParams) error
	DeactivateUser(context.Context, uuid.UUID) error
	GetProgrammeByCode(context.Context, string) (dbgen.Programme, error)
	GetTeamByID(context.Context, uuid.UUID) (dbgen.Team, error)
	GrantStaffCapability(context.Context, dbgen.GrantStaffCapabilityParams) (dbgen.GrantStaffCapabilityRow, error)
	RevokeStaffGrant(context.Context, dbgen.RevokeStaffGrantParams) (int64, error)
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
		return errors.New("usage: admin <create|set-password|deactivate|grant-staff|revoke-staff> [options]")
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
	case "grant-staff":
		return grantStaff(ctx, store, output, args[1:])
	case "revoke-staff":
		return revokeStaff(ctx, store, output, args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func grantStaff(ctx context.Context, store adminStore, output io.Writer, args []string) error {
	flags := flag.NewFlagSet("grant-staff", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	email := flags.String("email", "", "")
	actorEmail := flags.String("actor-email", "", "")
	capability := flags.String("capability", "", "")
	programmeCode := flags.String("programme", "", "")
	teamID := flags.String("team-id", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("usage: admin grant-staff --email ... --actor-email ... --capability coach --programme CODE|--team-id UUID; moderators have no scope")
	}
	target, err := activeAdultByEmail(ctx, store, *email, "staff account")
	if err != nil {
		return err
	}
	actor, err := activeAdminByEmail(ctx, store, *actorEmail)
	if err != nil {
		return err
	}
	params := dbgen.GrantStaffCapabilityParams{UserID: target.ID, GrantedByID: actor.ID}
	programme := strings.TrimSpace(*programmeCode)
	team := strings.TrimSpace(*teamID)
	switch strings.ToLower(strings.TrimSpace(*capability)) {
	case "coach":
		params.Capability = dbgen.StaffCapabilityCOACH
		if (programme == "") == (team == "") {
			return errors.New("coach grants require exactly one of --programme or --team-id")
		}
		if programme != "" {
			programme, err := store.GetProgrammeByCode(ctx, programme)
			if errors.Is(err, pgx.ErrNoRows) {
				return errors.New("programme not found")
			}
			if err != nil {
				return fmt.Errorf("look up programme: %w", err)
			}
			params.ProgrammeID = &programme.ID
		} else {
			id, err := uuid.Parse(team)
			if err != nil {
				return errors.New("team-id must be a UUID")
			}
			team, err := store.GetTeamByID(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return errors.New("team not found")
			}
			if err != nil {
				return fmt.Errorf("look up team: %w", err)
			}
			params.TeamID = &team.ID
		}
	case "moderator":
		params.Capability = dbgen.StaffCapabilityMODERATOR
		if programme != "" || team != "" {
			return errors.New("moderator grants cannot have a programme or team scope")
		}
	default:
		return errors.New("capability must be coach or moderator")
	}
	if _, err := store.GrantStaffCapability(ctx, params); err != nil {
		return fmt.Errorf("grant staff capability: %w", err)
	}
	fmt.Fprintln(output, "staff capability granted")
	return nil
}

func revokeStaff(ctx context.Context, store adminStore, output io.Writer, args []string) error {
	flags := flag.NewFlagSet("revoke-staff", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	grantID := flags.String("grant-id", "", "")
	actorEmail := flags.String("actor-email", "", "")
	reason := flags.String("reason", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("usage: admin revoke-staff --grant-id UUID --actor-email ... --reason ...")
	}
	id, err := uuid.Parse(*grantID)
	if err != nil {
		return errors.New("grant-id must be a UUID")
	}
	actor, err := activeAdminByEmail(ctx, store, *actorEmail)
	if err != nil {
		return err
	}
	trimmedReason := strings.TrimSpace(*reason)
	if n := utf8.RuneCountInString(trimmedReason); n == 0 || n > 500 {
		return errors.New("revocation reason must contain between 1 and 500 characters")
	}
	n, err := store.RevokeStaffGrant(ctx, dbgen.RevokeStaffGrantParams{ID: id, RevokedByID: &actor.ID, RevokeReason: &trimmedReason})
	if err != nil {
		return fmt.Errorf("revoke staff grant: %w", err)
	}
	if n != 1 {
		return errors.New("active staff grant not found")
	}
	fmt.Fprintln(output, "staff capability revoked")
	return nil
}

func activeAdultByEmail(ctx context.Context, store adminStore, rawEmail, label string) (dbgen.GetAccountByEmailRow, error) {
	email, err := validation.NormalizeEmail(rawEmail)
	if err != nil {
		return dbgen.GetAccountByEmailRow{}, err
	}
	account, err := store.GetAccountByEmail(ctx, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.GetAccountByEmailRow{}, fmt.Errorf("%s not found", label)
	}
	if err != nil {
		return dbgen.GetAccountByEmailRow{}, fmt.Errorf("look up %s: %w", label, err)
	}
	if !account.IsActive || account.IsDependent {
		return dbgen.GetAccountByEmailRow{}, fmt.Errorf("%s must be an active adult", label)
	}
	return account, nil
}

func activeAdminByEmail(ctx context.Context, store adminStore, rawEmail string) (dbgen.GetAccountByEmailRow, error) {
	account, err := activeAdultByEmail(ctx, store, rawEmail, "actor account")
	if err != nil {
		return dbgen.GetAccountByEmailRow{}, err
	}
	if !account.IsAdmin {
		return dbgen.GetAccountByEmailRow{}, errors.New("actor account must be an active administrator")
	}
	return account, nil
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
	user, err := store.GetAccountByEmail(ctx, &email)
	if err == nil {
		if user.IsAdmin && !user.IsDependent {
			fmt.Fprintln(output, "administrator already exists")
			return nil
		}
		if err := store.GrantPlatformRoleByCode(ctx, dbgen.GrantPlatformRoleByCodeParams{UserID: user.ID, RoleCode: "ADMIN"}); err != nil {
			return fmt.Errorf("grant administrator role: %w", err)
		}
		fmt.Fprintln(output, "administrator role granted")
		return nil
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
	created, err := store.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: name, Email: &email, PasswordHash: ptr(string(hash)), DateOfBirth: pgtype.Date{Time: birth, Valid: true}})
	if err != nil {
		return fmt.Errorf("create administrator: %w", err)
	}
	if err := store.GrantPlatformRoleByCode(ctx, dbgen.GrantPlatformRoleByCodeParams{UserID: created.ID, RoleCode: "ADMIN"}); err != nil {
		return fmt.Errorf("grant administrator role: %w", err)
	}
	fmt.Fprintln(output, "administrator created")
	return nil
}

func setPassword(ctx context.Context, store adminStore, stdin *os.File, passwordFile string, output io.Writer, rawEmail string) error {
	email, err := validation.NormalizeEmail(rawEmail)
	if err != nil {
		return err
	}
	user, err := store.GetAccountByEmail(ctx, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("administrator not found")
	}
	if err != nil {
		return fmt.Errorf("look up administrator: %w", err)
	}
	if !user.IsAdmin || user.IsDependent {
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
	user, err := store.GetAccountByEmail(ctx, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("administrator not found")
	}
	if err != nil {
		return fmt.Errorf("look up administrator: %w", err)
	}
	if !user.IsAdmin || user.IsDependent {
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
