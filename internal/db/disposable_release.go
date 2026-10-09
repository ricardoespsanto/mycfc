package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type disposableConnection interface {
	Begin(context.Context) (pgx.Tx, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// BootstrapDisposableRelease is a temporary, owner-authorised replacement of
// the exact tagged disposable predecessor. All destructive DDL, administrator
// recovery, permissions and the completion record commit together. Fencing is
// deliberately durable before that transaction: old-poller rollback can cause
// maintenance downtime, never unsafe predecessor writes to a new schema.
// Errors intentionally exclude SQL/driver detail (retained authentication
// material must not enter command logs, including constraint error DETAIL).
func BootstrapDisposableRelease(ctx context.Context, conn disposableConnection, database string, credentials RoleCredentials) error {
	if !releasecontract.Matches(releasecontract.Version, releasecontract.Candidate, database) || credentials.AppUsername != releasecontract.OldWebRole {
		return errors.New("disposable release binding rejected")
	}
	if err := validateBootstrapInput(database, credentials); err != nil {
		return errors.New("disposable release credentials incomplete")
	}
	candidateCredentials := credentials
	candidateCredentials.AppUsername = releasecontract.WebRole
	if err := validateBootstrapInput(database, candidateCredentials); err != nil {
		return errors.New("disposable release candidate role configuration rejected")
	}
	for _, secret := range []string{credentials.AppPassword, credentials.MigrationPassword, credentials.MediaCleanupPassword, credentials.DataRetentionPassword} {
		if strings.Contains(secret, "$$") || strings.ContainsRune(secret, 0) {
			return errors.New("disposable credential incompatible with existing bootstrap SQL; no replacement performed")
		}
	}
	baselineHash := sha256.Sum256([]byte(baselineSchema))
	if hex.EncodeToString(baselineHash[:]) != releasecontract.BaselineDigest || EmbeddedMigrationDigest() != releasecontract.FinalDigest {
		return errors.New("disposable release reviewed schema changed")
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext('mycfc-disposable-release-20261001'))"); err != nil {
		return errors.New("disposable release lock failed")
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock(hashtext('mycfc-disposable-release-20261001'))")
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("disposable preflight begin failed")
	}
	complete, err := disposablePreflight(ctx, tx, database, credentials)
	_ = tx.Rollback(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	// NOLOGIN is committed before terminating sessions, rather than setting it in
	// an uncommitted reset transaction where new connections could still enter.
	if _, err = conn.Exec(ctx, "ALTER ROLE "+quoteIdentifier(releasecontract.OldWebRole)+" NOLOGIN"); err != nil {
		return errors.New("disposable predecessor fence failed")
	}
	if _, err = conn.Exec(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename=$1 AND pid<>pg_backend_pid()", releasecontract.OldWebRole); err != nil {
		return errors.New("disposable predecessor session termination failed")
	}
	if complete {
		// Completion permanently retired destruction before the candidate could
		// accept public writes. Retry only reinforces the old-role fence; lifecycle
		// metadata is not an operator-dependent prerequisite for data safety.
		return nil
	}
	tx, err = conn.Begin(ctx)
	if err != nil {
		return errors.New("disposable reset begin failed")
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	complete, err = disposablePreflight(ctx, tx, database, credentials)
	if err != nil {
		return err
	}
	if complete {
		return errors.New("disposable release state changed")
	}
	exec := func(stage, sql string, args ...any) error {
		if _, e := tx.Exec(ctx, sql, args...); e != nil {
			return fmt.Errorf("disposable reset %s failed; transaction rolled back", stage)
		}
		return nil
	}
	if err = exec("identity lock", "LOCK TABLE public.users, public.user_platform_roles, public.platform_roles IN ACCESS EXCLUSIVE MODE"); err != nil {
		return err
	}
	if err = exec("credential capture", `CREATE TEMP TABLE retained_disposable_admin ON COMMIT DROP AS
 SELECT u.id,u.name,u.email::text AS email,u.email_verified_at,u.password_hash,u.credential_version,u.date_of_birth,u.created_at,u.updated_at
 FROM public.users u JOIN public.user_platform_roles a ON a.user_id=u.id JOIN public.platform_roles r ON r.id=a.role_id
 WHERE r.code='ADMIN' AND u.is_active AND NOT u.is_dependent AND u.email IS NOT NULL
 AND u.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
 AND u.password_hash ~ '^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$'`); err != nil {
		return err
	}
	var admins bool
	if err = tx.QueryRow(ctx, `SELECT count(*)>0 AND bool_or(email_verified_at IS NOT NULL) AND count(*)=(
 SELECT count(*) FROM public.users u JOIN public.user_platform_roles a ON a.user_id=u.id JOIN public.platform_roles r ON r.id=a.role_id WHERE r.code='ADMIN' AND u.is_active AND NOT u.is_dependent
 AND u.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date) FROM retained_disposable_admin`).Scan(&admins); err != nil || !admins {
		return errors.New("disposable release has no recoverable verified adult administrator")
	}
	// Only reviewed application namespaces; never enumerate/drop other tenants.
	for _, schema := range []string{"guardian_ops", "privacy_disable", "privacy_protected", "mycfc_meta", "public"} {
		if err = exec("schema replacement", "DROP SCHEMA "+quoteIdentifier(schema)+" CASCADE"); err != nil {
			return err
		}
	}
	if err = exec("public schema", "CREATE SCHEMA public"); err != nil {
		return err
	}
	credentials.AppUsername = releasecontract.WebRole
	if err = BootstrapRoles(ctx, tx, database, credentials); err != nil {
		return errors.New("disposable reset role bootstrap failed; transaction rolled back")
	}
	if err = exec("migration owner", "SET LOCAL ROLE "+quoteIdentifier(credentials.MigrationUsername)); err != nil {
		return err
	}
	if _, err = tx.Conn().PgConn().Exec(ctx, baselineSchema).ReadAll(); err != nil {
		return errors.New("disposable reset baseline failed; transaction rolled back")
	}
	if err = exec("ledger", "CREATE TABLE mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		return err
	}
	for _, version := range EmbeddedMigrationInventory() {
		if err = exec("ledger marker", "INSERT INTO mycfc_meta.schema_migrations(version) VALUES ($1)", version); err != nil {
			return err
		}
	}
	if err = exec("bootstrap owner", "RESET ROLE"); err != nil {
		return err
	}
	if err = exec("administrator recovery", `INSERT INTO public.users(id,name,email,email_verified_at,password_hash,credential_version,date_of_birth,created_at,updated_at)
 SELECT id,name,email::public.citext,email_verified_at,password_hash,credential_version,date_of_birth,created_at,updated_at FROM retained_disposable_admin`); err != nil {
		return err
	}
	if err = exec("administrator role", `INSERT INTO public.user_platform_roles(user_id,role_id) SELECT a.id,r.id FROM retained_disposable_admin a CROSS JOIN public.platform_roles r WHERE r.code='ADMIN'`); err != nil {
		return err
	}
	if err = HardenPrivacyExecutionRoles(ctx, tx, database, credentials); err != nil {
		return errors.New("disposable reset web ACL hardening failed; transaction rolled back")
	}
	if err = VerifyDatedParticipationContract(ctx, tx); err != nil {
		return errors.New("disposable reset final schema validation failed; transaction rolled back")
	}
	if err = exec("predecessor database ACL", "REVOKE ALL ON DATABASE "+quoteIdentifier(database)+" FROM "+quoteIdentifier(releasecontract.OldWebRole)); err != nil {
		return err
	}
	if err = exec("predecessor schema ACL", "REVOKE ALL ON SCHEMA public FROM "+quoteIdentifier(releasecontract.OldWebRole)); err != nil {
		return err
	}
	if err = exec("predecessor tables ACL", "REVOKE ALL ON ALL TABLES IN SCHEMA public FROM "+quoteIdentifier(releasecontract.OldWebRole)); err != nil {
		return err
	}
	if err = exec("completion namespace", `CREATE SCHEMA mycfc_disposable_release; REVOKE ALL ON SCHEMA mycfc_disposable_release FROM PUBLIC;
 CREATE TABLE mycfc_disposable_release.completion(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),database_name text NOT NULL,version text NOT NULL,candidate text NOT NULL,predecessor text NOT NULL,final_digest text NOT NULL,real_data_started_at timestamptz,reset_retired_at timestamptz NOT NULL DEFAULT now(),completed_at timestamptz NOT NULL DEFAULT now(),CHECK(reset_retired_at<=completed_at));
 REVOKE ALL ON ALL TABLES IN SCHEMA mycfc_disposable_release FROM PUBLIC;
 CREATE FUNCTION mycfc_disposable_release.protect_retirement() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
 BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'disposable reset retirement is permanent'; END IF;
 IF NEW.reset_retired_at IS DISTINCT FROM OLD.reset_retired_at THEN RAISE EXCEPTION 'disposable reset retirement is permanent'; END IF;
 RETURN NEW;
 END $$;
 REVOKE ALL ON FUNCTION mycfc_disposable_release.protect_retirement() FROM PUBLIC;
 CREATE TRIGGER protect_retirement BEFORE UPDATE OR DELETE ON mycfc_disposable_release.completion FOR EACH ROW EXECUTE FUNCTION mycfc_disposable_release.protect_retirement();
 CREATE TRIGGER protect_retirement_truncate BEFORE TRUNCATE ON mycfc_disposable_release.completion FOR EACH STATEMENT EXECUTE FUNCTION mycfc_disposable_release.protect_retirement()`); err != nil {
		return err
	}
	if err = exec("durable completion", `INSERT INTO mycfc_disposable_release.completion(database_name,version,candidate,predecessor,final_digest) VALUES ($1,$2,$3,$4,$5)`, database, releasecontract.Version, releasecontract.Candidate, releasecontract.PredecessorDigest, releasecontract.FinalDigest); err != nil {
		return err
	}
	if err = exec("completion read API", `CREATE FUNCTION mycfc_disposable_release.matches(p_database text,p_version text,p_candidate text,p_final text) RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT count(*)=1 AND bool_and(database_name=p_database AND version=p_version AND candidate=p_candidate AND final_digest=p_final AND reset_retired_at IS NOT NULL AND reset_retired_at<=completed_at AND predecessor='`+releasecontract.PredecessorDigest+`')
 AND current_database()=p_database AND (SELECT encode(public.digest(string_agg(version,E'\n' ORDER BY version),'sha256'),'hex') FROM mycfc_meta.schema_migrations)=p_final
 FROM mycfc_disposable_release.completion $$;
 REVOKE ALL ON FUNCTION mycfc_disposable_release.matches(text,text,text,text) FROM PUBLIC;
 GRANT USAGE ON SCHEMA mycfc_disposable_release TO `+quoteIdentifier(credentials.MigrationUsername)+`;
 GRANT EXECUTE ON FUNCTION mycfc_disposable_release.matches(text,text,text,text) TO `+quoteIdentifier(credentials.MigrationUsername)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return errors.New("disposable release commit failed; inspect completion before retry")
	}
	return nil
}

func disposablePreflight(ctx context.Context, tx pgx.Tx, database string, credentials RoleCredentials) (bool, error) {
	var privileged bool
	if err := tx.QueryRow(ctx, "SELECT rolsuper FROM pg_roles WHERE rolname=current_user").Scan(&privileged); err != nil || !privileged {
		return false, errors.New("disposable release requires bootstrap superuser; migration role cannot replace database or fence sessions")
	}
	var roleConfiguration bool
	if err := tx.QueryRow(ctx, `SELECT current_user<>ALL(ARRAY[$1,$2,$3,$4]::text[])
 AND EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1 AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls)
 AND (SELECT count(*) FROM pg_namespace WHERE nspname IN ('public','mycfc_meta') AND pg_get_userbyid(nspowner)=$1)=2
 AND NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname IN ($3,$4) AND (rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls))`, credentials.MigrationUsername, releasecontract.WebRole, credentials.MediaCleanupUsername, credentials.DataRetentionUsername).Scan(&roleConfiguration); err != nil || !roleConfiguration {
		return false, errors.New("disposable migration role ownership/capability preflight rejected; no replacement performed")
	}
	var receipt bool
	if err := tx.QueryRow(ctx, "SELECT to_regnamespace('mycfc_disposable_release') IS NOT NULL").Scan(&receipt); err != nil {
		return false, errors.New("disposable release completion inspection failed")
	}
	var digest string
	if err := tx.QueryRow(ctx, "SELECT encode(public.digest(string_agg(version,E'\\n' ORDER BY version),'sha256'),'hex') FROM mycfc_meta.schema_migrations").Scan(&digest); err != nil {
		return false, errors.New("disposable release ledger missing or partial")
	}
	if receipt {
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT count(*)=1 AND bool_and(database_name=$1 AND version=$2 AND candidate=$3 AND predecessor=$4 AND final_digest=$5 AND reset_retired_at IS NOT NULL AND reset_retired_at<=completed_at) FROM mycfc_disposable_release.completion`, database, releasecontract.Version, releasecontract.Candidate, releasecontract.PredecessorDigest, releasecontract.FinalDigest).Scan(&valid); err != nil || !valid || digest != releasecontract.FinalDigest {
			return false, errors.New("disposable completion mismatched, partial, or reset retirement missing; refusing replacement")
		}
		return true, nil
	}
	if digest != releasecontract.PredecessorDigest {
		return false, errors.New("disposable release requires exact v1.25.9 predecessor; no completion permits no reset")
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT
 (SELECT array_agg(nspname::text ORDER BY nspname) FROM pg_namespace WHERE nspname !~ '^pg_' AND nspname<>'information_schema')=ARRAY['guardian_ops','mycfc_meta','privacy_disable','privacy_protected','public']::text[]
 AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='public.user_memberships'::regclass AND conname='user_memberships_user_season_programme_unique' AND contype='u')
 AND NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)
 AND EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$2 AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls)
 AND NOT EXISTS(SELECT 1 FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.member WHERE r.rolname=$2)
 AND EXISTS(SELECT 1 FROM public.users u JOIN public.user_platform_roles a ON a.user_id=u.id JOIN public.platform_roles r ON r.id=a.role_id WHERE r.code='ADMIN' AND u.is_active AND NOT u.is_dependent AND u.email_verified_at IS NOT NULL AND u.password_hash ~ '^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$')`, releasecontract.WebRole, releasecontract.OldWebRole).Scan(&valid); err != nil || !valid {
		return false, errors.New("disposable predecessor schema, role boundary or administrator recovery preflight rejected")
	}
	return false, nil
}
