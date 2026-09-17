package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
)

const retentionLogin = "mycfc_privacy_retention_login"

var retentionEffectiveUID = os.Geteuid
var retentionDatabaseName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)

type credentialInputs struct{ adminURL, password, database string }

func readRootCredential(path string) (string, error) {
	return readCredential(path, 0, 0)
}

func readCredential(path string, ownerUID, ownerGID uint32) (string, error) {
	if !strings.HasPrefix(path, "/") {
		return "", errors.New("credential file rejected")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", errors.New("credential file unavailable")
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode() != 0600 {
		return "", errors.New("credential file rejected")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != ownerUID || stat.Gid != ownerGID {
		return "", errors.New("credential file rejected")
	}
	b, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(b) == 0 || len(b) > 8192 {
		return "", errors.New("credential file rejected")
	}
	return strings.TrimSuffix(string(b), "\n"), nil
}

func parseCredentialURL(raw, database string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.User.Username() == "" || u.Hostname() == "" || u.Path != "/"+database || u.Fragment != "" || u.Opaque != "" || u.String() != raw {
		return nil, errors.New("database credential rejected")
	}
	password, ok := u.User.Password()
	if !ok || password == "" {
		return nil, errors.New("database credential rejected")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, errors.New("database credential rejected")
	}
	for key, values := range q {
		if key != "sslmode" || len(values) != 1 {
			return nil, errors.New("database credential parameters rejected")
		}
		switch values[0] {
		case "disable", "require", "verify-ca", "verify-full":
		default:
			return nil, errors.New("database credential parameters rejected")
		}
	}
	return u, nil
}

func loadCredentialInputs(mode string, getenv func(string) string, read func(string) (string, error)) (credentialInputs, error) {
	if retentionEffectiveUID() != 0 {
		return credentialInputs{}, errors.New("retention credential operation requires root")
	}
	if mode != "provision" && mode != "rotate" && mode != "revoke" {
		return credentialInputs{}, errors.New("retention credential operation rejected")
	}
	database := getenv("PRIVACY_RETENTION_EXPECTED_DATABASE")
	if !retentionDatabaseName.MatchString(database) {
		return credentialInputs{}, errors.New("retention database identity rejected")
	}
	raw, err := read(getenv("PRIVACY_RETENTION_ADMIN_DATABASE_URL_FILE"))
	if err != nil {
		return credentialInputs{}, errors.New("retention administrator credential unavailable")
	}
	admin, err := parseCredentialURL(raw, database)
	if err != nil || admin.User.Username() == retentionLogin || admin.User.Username() == retentionRole {
		return credentialInputs{}, errors.New("retention administrator credential rejected")
	}
	inputs := credentialInputs{adminURL: raw, database: database}
	if mode == "revoke" {
		return inputs, nil
	}
	raw, err = read(getenv("PRIVACY_RETENTION_LOGIN_DATABASE_URL_FILE"))
	if err != nil {
		return credentialInputs{}, errors.New("retention login credential unavailable")
	}
	login, err := parseCredentialURL(raw, database)
	if err != nil || login.User.Username() != retentionLogin || login.Host != admin.Host || login.RawQuery != admin.RawQuery {
		return credentialInputs{}, errors.New("retention login credential rejected")
	}
	inputs.password, _ = login.User.Password()
	adminPassword, _ := admin.User.Password()
	if inputs.password == adminPassword || len(inputs.password) < 32 || len(inputs.password) > 1024 || strings.ContainsAny(inputs.password, "\x00\r\n") {
		return credentialInputs{}, errors.New("retention login password rejected")
	}
	return inputs, nil
}

func runCredentialOperation(ctx context.Context, mode string, getenv func(string) string, output io.Writer) error {
	return runCredentialOperationWith(ctx, mode, getenv, output, readRootCredential, connectCredentialDatabase)
}

type credentialConnection interface {
	credentialDatabase
	Close(context.Context) error
}

func connectCredentialDatabase(ctx context.Context, databaseURL string) (credentialConnection, error) {
	return pgx.Connect(ctx, databaseURL)
}

func runCredentialOperationWith(ctx context.Context, mode string, getenv func(string) string, output io.Writer, read func(string) (string, error), connect func(context.Context, string) (credentialConnection, error)) error {
	inputs, err := loadCredentialInputs(mode, getenv, read)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := connect(ctx, inputs.adminURL)
	if err != nil {
		return errors.New("retention credential connection failed")
	}
	defer conn.Close(context.WithoutCancel(ctx))
	if err = changeRetentionCredential(ctx, conn, mode, inputs); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "privacy_retention_credential_%s_succeeded\n", mode)
	return err
}

type credentialDatabase interface {
	Begin(context.Context) (pgx.Tx, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func changeRetentionCredential(ctx context.Context, conn credentialDatabase, mode string, inputs credentialInputs) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("retention credential transaction failed")
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var database string
	var superuser bool
	if err = tx.QueryRow(ctx, `SELECT current_database(),current_setting('is_superuser')='on'`).Scan(&database, &superuser); err != nil || database != inputs.database || !superuser {
		return errors.New("retention credential database rejected")
	}
	// Suppress statement/bind logging before any password enters PostgreSQL. SQL
	// and driver errors are deliberately never wrapped into the returned error.
	for _, sql := range []string{
		`SET LOCAL log_statement='none'`, `SET LOCAL log_min_error_statement='panic'`,
		`SET LOCAL log_parameter_max_length=0`, `SET LOCAL log_parameter_max_length_on_error=0`,
		`SET LOCAL password_encryption='scram-sha-256'`,
		`SELECT pg_advisory_xact_lock(274,247)`,
	} {
		if _, err = tx.Exec(ctx, sql); err != nil {
			return errors.New("retention credential preparation failed")
		}
	}
	var exists, capability bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='mycfc_privacy_retention_login'), EXISTS(SELECT 1 FROM pg_roles WHERE rolname='mycfc_privacy_retention' AND NOT rolcanlogin AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT rolbypassrls AND NOT EXISTS(SELECT 1 FROM pg_auth_members WHERE member=pg_roles.oid) AND has_function_privilege(oid,'privacy_retention_run(uuid,integer)','EXECUTE') AND has_function_privilege(oid,'privacy_retention_status()','EXECUTE'))`).Scan(&exists, &capability); err != nil {
		return errors.New("retention credential role verification failed")
	}
	if mode == "provision" && exists || mode == "rotate" && !exists || mode != "revoke" && !capability {
		return errors.New("retention credential role state rejected")
	}
	if mode == "revoke" && !exists {
		if err = tx.Commit(ctx); err != nil {
			return errors.New("retention credential commit failed")
		}
		return nil
	}
	if exists && mode != "revoke" {
		var owned bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_shdepend WHERE refclassid='pg_authid'::regclass AND refobjid=(SELECT oid FROM pg_roles WHERE rolname='mycfc_privacy_retention_login') AND deptype='o')`).Scan(&owned); err != nil || owned {
			return errors.New("retention login owns database objects")
		}
	}
	if !exists {
		if _, err = tx.Exec(ctx, `CREATE ROLE mycfc_privacy_retention_login NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS`); err != nil {
			return errors.New("retention credential creation failed")
		}
	}
	statements := []string{
		`ALTER ROLE mycfc_privacy_retention_login NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1 VALID UNTIL 'infinity' PASSWORD NULL`,
		`ALTER ROLE mycfc_privacy_retention_login RESET ALL`,
		`DO $$DECLARE r record; BEGIN FOR r IN SELECT child.rolname FROM pg_auth_members m JOIN pg_roles parent ON parent.oid=m.roleid JOIN pg_roles child ON child.oid=m.member WHERE parent.rolname='mycfc_privacy_retention_login' LOOP EXECUTE format('REVOKE mycfc_privacy_retention_login FROM %I',r.rolname); END LOOP; END$$`,
		`DO $$DECLARE r record; BEGIN FOR r IN SELECT parent.rolname FROM pg_auth_members m JOIN pg_roles parent ON parent.oid=m.roleid JOIN pg_roles child ON child.oid=m.member WHERE child.rolname='mycfc_privacy_retention_login' LOOP EXECUTE format('REVOKE %I FROM mycfc_privacy_retention_login',r.rolname); END LOOP; END$$`,
		`REVOKE ALL ON DATABASE ` + pgx.Identifier{inputs.database}.Sanitize() + ` FROM mycfc_privacy_retention_login`,
		`DO $$DECLARE s record; BEGIN FOR s IN SELECT nspname FROM pg_namespace WHERE nspname NOT LIKE 'pg_%' AND nspname<>'information_schema' LOOP
 EXECUTE format('REVOKE ALL ON SCHEMA %I FROM mycfc_privacy_retention_login',s.nspname);
 EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA %I FROM mycfc_privacy_retention_login',s.nspname);
 EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA %I FROM mycfc_privacy_retention_login',s.nspname);
 EXECUTE format('REVOKE ALL ON ALL FUNCTIONS IN SCHEMA %I FROM mycfc_privacy_retention_login',s.nspname);
 END LOOP; END$$`,
	}
	if mode != "revoke" {
		// Values are bound, never interpolated into the statement sent by the CLI.
		if _, err = tx.Exec(ctx, `SELECT set_config('mycfc.retention_password',$1,true)`, inputs.password); err != nil {
			return errors.New("retention credential password setup failed")
		}
		statements = append(statements,
			`GRANT CONNECT ON DATABASE `+pgx.Identifier{inputs.database}.Sanitize()+` TO mycfc_privacy_retention_login`,
			`GRANT mycfc_privacy_retention TO mycfc_privacy_retention_login WITH INHERIT FALSE, SET TRUE`,
			`DO $$BEGIN EXECUTE format('ALTER ROLE mycfc_privacy_retention_login LOGIN PASSWORD %L',current_setting('mycfc.retention_password')); END$$`,
		)
	}
	for _, sql := range statements {
		if _, err = tx.Exec(ctx, sql); err != nil {
			return errors.New("retention credential hardening failed")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return errors.New("retention credential commit failed")
	}
	// Enforce the new password/NOLOGIN before terminating old sessions, closing
	// the pre-commit reconnect race. The host must keep the timer stopped. A
	// failure here reports no success; the committed role remains restricted.
	if exists {
		var terminated bool
		if err = conn.QueryRow(ctx, `SELECT COALESCE(bool_and(pg_terminate_backend(pid,5000)),true) FROM pg_stat_activity WHERE usename='mycfc_privacy_retention_login' AND pid<>pg_backend_pid()`).Scan(&terminated); err != nil || !terminated {
			return errors.New("retention credential session revocation failed")
		}
		var active bool
		if err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE usename='mycfc_privacy_retention_login')`).Scan(&active); err != nil || active {
			return errors.New("retention credential sessions remain")
		}
	}
	return nil
}
