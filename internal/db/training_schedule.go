package db

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const TrainingScheduleContractC = "schedule-c1"
const TrainingScheduleContractP = "schedule-p1"
const TrainingScheduleCompatibilityMigration = "202611100001_training_schedule_compatibility"

var ErrTrainingScheduleWriteUnsupported = errors.New("training schedule write unsupported")

//go:embed contracts/fresh_genesis_attachment.sql
var freshGenesisAttachmentSQL string

type TrainingScheduleState struct {
	DatabaseIncarnation                    uuid.UUID
	SchemaContract                         string
	StateEpoch                             int64
	RequiredReader                         int32
	FloorCommitID                          *uuid.UUID
	FloorManifestSHA256                    *string
	PartialV2Enabled, EverPartialV2Enabled bool
}
type TrainingScheduleCapabilities struct{ ReaderVersion, WriterVersion int32 }
type TrainingScheduleQueryReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// TrainingScheduleStatus contains database facts, not proof that an external
// restore/controller floor is trustworthy. A copied database copies these facts.
func TrainingScheduleStatus(ctx context.Context, q TrainingScheduleQueryReader) (TrainingScheduleState, error) {
	var s TrainingScheduleState
	err := q.QueryRow(ctx, `SELECT database_incarnation,schema_contract,state_epoch,required_reader,floor_commit_id,floor_manifest_sha256,partial_v2_enabled,ever_partial_v2_enabled FROM training_schedule_control.status()`).Scan(
		&s.DatabaseIncarnation, &s.SchemaContract, &s.StateEpoch, &s.RequiredReader, &s.FloorCommitID, &s.FloorManifestSHA256, &s.PartialV2Enabled, &s.EverPartialV2Enabled)
	if err != nil {
		return s, errors.New("training schedule status unavailable")
	}
	return s, nil
}

// VerifyTrainingScheduleRuntime verifies ONLY the low-privilege database
// contract. App/controller must independently verify the externally trusted
// restore/floor fence before readiness; matching copied DB fields is not proof.
func VerifyTrainingScheduleRuntime(ctx context.Context, q TrainingScheduleQueryReader, cap TrainingScheduleCapabilities) (TrainingScheduleState, error) {
	s, err := TrainingScheduleStatus(ctx, q)
	if err != nil {
		return s, err
	}
	if cap.ReaderVersion < 2 || cap.ReaderVersion < s.RequiredReader || cap.WriterVersion < 1 || cap.WriterVersion > 2 || s.DatabaseIncarnation == uuid.Nil || s.StateEpoch < 0 {
		return s, errors.New("training schedule capability mismatch")
	}
	if err = verifyTrainingScheduleSchema(ctx, q, s.SchemaContract, true); err != nil {
		return s, err
	}
	if s.RequiredReader < 1 || s.RequiredReader >= 2 && (s.FloorCommitID == nil || s.FloorManifestSHA256 == nil) || s.PartialV2Enabled && (s.SchemaContract != TrainingScheduleContractP || s.RequiredReader < 2 || !s.EverPartialV2Enabled) || s.EverPartialV2Enabled && s.RequiredReader < 2 {
		return s, errors.New("training schedule control invalid")
	}
	return s, nil
}

// VerifyTrainingScheduleMigrationFinal runs on the actual non-superuser
// migration transaction, never on the bootstrap connection.
func VerifyTrainingScheduleMigrationFinal(ctx context.Context, q TrainingScheduleQueryReader) error {
	var exact bool
	if err := q.QueryRow(ctx, `SELECT /* training schedule final inventory */
 (SELECT pg_catalog.array_agg(version ORDER BY version COLLATE "C") FROM mycfc_meta.schema_migrations)=$1::text[]`, EmbeddedMigrationInventory()).Scan(&exact); err != nil || !exact {
		return errors.New("training schedule final inventory mismatch")
	}
	return verifyTrainingScheduleSchema(ctx, q, TrainingScheduleContractP, false)
}

// verifyTrainingScheduleAdmissionSchema accepts only explicitly reviewed old
// inventories before definition changes, and the current final P schema.
func verifyTrainingScheduleAdmissionSchema(ctx context.Context, q TrainingScheduleQueryReader, digest string) error {
	if digest == EmbeddedMigrationDigest() {
		return verifyTrainingScheduleSchema(ctx, q, TrainingScheduleContractP, false)
	}
	if digest == releasecontract.NormalAuthoringParentDigest || digest == releasecontract.NormalRecipientParentDigest {
		return verifyTrainingScheduleSchema(ctx, q, TrainingScheduleContractC, false)
	}
	if digest != releasecontract.NormalParentDigest && digest != releasecontract.FreshGenesisDigest {
		return errors.New("training schedule parent inventory mismatch")
	}
	var valid bool
	err := q.QueryRow(ctx, `SELECT /* training schedule reviewed parent */
 NOT EXISTS(SELECT 1 FROM pg_catalog.pg_attribute WHERE attrelid='public.training_sessions'::pg_catalog.regclass AND attname IN ('schedule_precision','scheduled_on','day_band') AND NOT attisdropped)
 AND pg_catalog.to_regnamespace('training_schedule_control') IS NULL
 AND (SELECT pg_catalog.count(*)=2 FROM pg_catalog.pg_attribute WHERE attrelid='public.training_sessions'::pg_catalog.regclass AND attname IN ('starts_at','ends_at') AND atttypid='pg_catalog.timestamptz'::pg_catalog.regtype AND attnotnull AND NOT attisdropped)
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_constraint WHERE conrelid='public.training_sessions'::pg_catalog.regclass AND conname='training_sessions_times_valid' AND convalidated AND pg_catalog.pg_get_constraintdef(oid)='CHECK ((starts_at < ends_at))')
 AND (SELECT prosecdef=$1 FROM pg_catalog.pg_proc WHERE oid='public.require_active_privacy_attachment_subject()'::pg_catalog.regprocedure)`, digest == releasecontract.NormalParentDigest).Scan(&valid)
	if err != nil || !valid {
		return errors.New("training schedule reviewed parent schema mismatch")
	}
	parent := strings.Split(baselineSchema, "\n-- Baseline through 202611100001_training_schedule_compatibility.")[0]
	for _, name := range []string{"public.training_session_snapshot", "public.restore_training_session"} {
		header, err := trainingScheduleExtractFunction(parent, strings.TrimPrefix(name, "public."))
		if err != nil {
			return err
		}
		start := strings.Index(header, "AS $$")
		body := header[start+5 : len(header)-3]
		if err = q.QueryRow(ctx, `SELECT /* training schedule parent function contract */ EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE p.oid=pg_catalog.to_regprocedure($1) AND p.prosrc=$2 AND p.proowner=n.nspowner AND NOT p.proisstrict AND NOT p.prosecdef)`, trainingScheduleFunctionSignature(name), body).Scan(&valid); err != nil || !valid {
			return errors.New("training schedule reviewed parent function mismatch")
		}
	}
	attachment := freshGenesisAttachmentSQL
	if digest == releasecontract.NormalParentDigest {
		sql, err := migrationFiles.ReadFile("migrations/202610010003_privacy_attachment_trigger_runtime.sql")
		if err != nil {
			return err
		}
		attachment = string(sql)
	}
	header, err := trainingScheduleExtractFunction(attachment, "require_active_privacy_attachment_subject")
	if err != nil {
		return err
	}
	if ok, err := trainingScheduleFunctionMatches(ctx, q, "public.require_active_privacy_attachment_subject()", header); err != nil || !ok {
		return errors.New("training schedule reviewed parent privacy attachment mismatch")
	}
	return nil
}

func verifyTrainingScheduleSchema(ctx context.Context, q TrainingScheduleQueryReader, contract string, runtime bool) error {
	if contract != TrainingScheduleContractC && contract != TrainingScheduleContractP {
		return errors.New("training schedule schema unsupported")
	}
	var valid bool
	err := q.QueryRow(ctx, `SELECT /* training schedule schema contract */
 (SELECT pg_catalog.count(*)=3 FROM pg_catalog.pg_attribute WHERE attrelid='public.training_sessions'::pg_catalog.regclass AND NOT attisdropped AND
  ((attname='schedule_precision' AND atttypid='pg_catalog.text'::pg_catalog.regtype AND attnotnull)
   OR (attname='scheduled_on' AND atttypid='pg_catalog.date'::pg_catalog.regtype AND NOT attnotnull)
   OR (attname='day_band' AND atttypid='pg_catalog.text'::pg_catalog.regtype AND NOT attnotnull)))
 AND (SELECT pg_catalog.count(*)=2 FROM pg_catalog.pg_attribute WHERE attrelid='public.training_sessions'::pg_catalog.regclass AND NOT attisdropped AND attname IN ('starts_at','ends_at') AND attnotnull=$1)
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
  WHERE a.attrelid='public.training_sessions'::pg_catalog.regclass AND a.attname='schedule_precision' AND pg_catalog.pg_get_expr(d.adbin,d.adrelid)=$2)
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_constraint WHERE conrelid='public.training_sessions'::pg_catalog.regclass AND conname='training_sessions_schedule_shape_valid' AND convalidated
  AND pg_catalog.replace(pg_catalog.pg_get_constraintdef(oid),'public.','')='CHECK (training_session_schedule_valid(schedule_precision, scheduled_on, starts_at, ends_at, day_band, entry_kind))')
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_constraint WHERE conrelid='public.training_sessions'::pg_catalog.regclass AND conname='training_sessions_schedule_compatibility_only' AND convalidated
  AND pg_catalog.pg_get_constraintdef(oid)='CHECK ((schedule_precision = ''INTERVAL''::text))')=$1
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_roles r ON r.oid=c.relowner AND r.oid=n.nspowner
  WHERE n.nspname='training_schedule_control' AND c.relname='state' AND c.relkind='r' AND c.relpersistence='p' AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity AND NOT r.rolsuper)
 AND (SELECT pg_catalog.count(*)=4 FROM pg_catalog.pg_constraint WHERE conrelid='training_schedule_control.state'::pg_catalog.regclass
  AND conname IN ('training_schedule_floor_complete','training_schedule_reader_floor','training_schedule_enable_valid','training_schedule_ever_floor') AND convalidated)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace n CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(n.nspacl,pg_catalog.acldefault('n',n.nspowner))) acl WHERE n.nspname='training_schedule_control' AND acl.grantee=0)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(c.relacl,pg_catalog.acldefault('r',c.relowner))) acl WHERE n.nspname='training_schedule_control' AND acl.grantee=0)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(p.proacl,pg_catalog.acldefault('f',p.proowner))) acl WHERE n.nspname='training_schedule_control' AND acl.grantee=0)
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_trigger WHERE tgrelid='public.training_sessions'::pg_catalog.regclass AND tgname='training_sessions_schedule_guard' AND tgfoid='training_schedule_control.guard_session()'::pg_catalog.regprocedure AND NOT tgisinternal AND tgenabled IN ('O','A') AND tgtype=31)
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_trigger WHERE tgrelid='public.training_prescriptions'::pg_catalog.regclass AND tgname='training_prescriptions_schedule_guard' AND tgfoid='training_schedule_control.guard_artifact()'::pg_catalog.regprocedure AND NOT tgisinternal AND tgenabled IN ('O','A') AND tgtype=7)
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_trigger WHERE tgrelid='public.training_routines'::pg_catalog.regclass AND tgname='training_routines_schedule_guard' AND tgfoid='training_schedule_control.guard_artifact()'::pg_catalog.regprocedure AND NOT tgisinternal AND tgenabled IN ('O','A') AND tgtype=23)
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_trigger WHERE tgrelid='public.training_plan_publications'::pg_catalog.regclass AND tgname='training_plan_publications_schedule_guard' AND tgfoid='training_schedule_control.guard_publication()'::pg_catalog.regprocedure AND NOT tgisinternal AND tgenabled IN ('O','A') AND tgtype=7)`, contract == TrainingScheduleContractC, "'INTERVAL'::text").Scan(&valid)
	if err != nil || !valid {
		return errors.New("training schedule schema contract mismatch")
	}
	// Actual P authoring cannot enable partial writes below the corrected
	// recipient reader floor. C's known parent retains its original CHECK2.
	minimum := 2
	if contract == TrainingScheduleContractP {
		minimum = 3
	}
	expectedEnable := fmt.Sprintf("CHECK (((NOT partial_v2_enabled) OR ((schema_contract = 'schedule-p1'::text) AND (required_reader >= %d) AND (floor_commit_id IS NOT NULL) AND ever_partial_v2_enabled)))", minimum)
	expectedEver := fmt.Sprintf("CHECK (((NOT ever_partial_v2_enabled) OR (required_reader >= %d)))", minimum)
	if err = q.QueryRow(ctx, `SELECT /* training schedule exact floor checks */
     EXISTS(SELECT 1 FROM pg_catalog.pg_constraint WHERE conrelid='training_schedule_control.state'::pg_catalog.regclass AND conname='training_schedule_enable_valid' AND convalidated AND pg_catalog.pg_get_constraintdef(oid)=$1)
     AND EXISTS(SELECT 1 FROM pg_catalog.pg_constraint WHERE conrelid='training_schedule_control.state'::pg_catalog.regclass AND conname='training_schedule_ever_floor' AND convalidated AND pg_catalog.pg_get_constraintdef(oid)=$2)`, expectedEnable, expectedEver).Scan(&valid); err != nil || !valid {
		return errors.New("training schedule reader floor constraint mismatch")
	}
	// Compare installed function source, not a self-reported state label or a
	// substring that a weakened body could retain. No application function is
	// executed by this catalog verification.
	for _, name := range []string{
		"public.training_session_local_date", "public.training_session_schedule_valid", "public.training_schedule_artifact_version",
		"public.training_session_snapshot", "public.restore_training_session", "public.training_session_snapshot_v2",
		"public.prevent_dated_participation_rewrite", "public.training_schedule_admit_partial",
		"public.training_schedule_assert_plan_v1", "public.training_schedule_assert_session_v1", "public.training_schedule_assert_routine_v1", "public.training_schedule_lock_target_v1",
		"training_schedule_control.status", "training_schedule_control.admit_partial", "training_schedule_control.commit_floor",
		"training_schedule_control.set_enabled", "training_schedule_control.guard_session", "training_schedule_control.guard_artifact", "training_schedule_control.guard_publication",
	} {
		header, err := trainingScheduleFunctionHeader(name)
		if err != nil {
			return err
		}
		if valid, err = trainingScheduleFunctionMatches(ctx, q, trainingScheduleFunctionSignature(name), header); err != nil || !valid {
			return fmt.Errorf("training schedule function contract mismatch: %s", name)
		}
	}
	if runtime {
		if err = q.QueryRow(ctx, `SELECT /* training schedule runtime privileges */
 session_user=current_user
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace n WHERE n.nspname='training_schedule_control' AND pg_catalog.pg_has_role(session_user,n.nspowner,'MEMBER'))
 AND NOT pg_catalog.has_schema_privilege(current_user,'training_schedule_control','CREATE')
 AND NOT pg_catalog.has_table_privilege(current_user,'training_schedule_control.state','SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
 AND pg_catalog.has_function_privilege(current_user,'training_schedule_control.status()','EXECUTE')
 AND NOT pg_catalog.has_function_privilege(current_user,'training_schedule_control.commit_floor(uuid,bigint,uuid,text,integer)','EXECUTE')
 AND NOT pg_catalog.has_function_privilege(current_user,'training_schedule_control.set_enabled(uuid,bigint,boolean)','EXECUTE')`).Scan(&valid); err != nil || !valid {
			return errors.New("training schedule runtime privileges mismatch")
		}
	}
	return nil
}

func trainingScheduleFunctionMatches(ctx context.Context, q TrainingScheduleQueryReader, signature, header string) (bool, error) {
	index := strings.Index(header, "AS $$")
	if index < 0 {
		return false, errors.New("training schedule function body missing")
	}
	body := header[index+5 : len(header)-3]
	prefix := header[:index]
	var config []string
	if parts := regexp.MustCompile(`SET search_path=([^\s]+)`).FindStringSubmatch(prefix); len(parts) == 2 {
		config = []string{"search_path=" + strings.Join(strings.Split(parts[1], ","), ", ")}
	}
	volatility := "v"
	if strings.Contains(prefix, " IMMUTABLE ") {
		volatility = "i"
	} else if strings.Contains(prefix, " STABLE ") {
		volatility = "s"
	}
	var valid bool
	err := q.QueryRow(ctx, `SELECT /* training schedule function contract */ EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE p.oid=pg_catalog.to_regprocedure($1)
 AND p.prosrc=$2 AND p.proowner=n.nspowner AND p.prosecdef=$3 AND p.proconfig IS NOT DISTINCT FROM $4::text[]
 AND p.provolatile=$5::pg_catalog."char" AND NOT p.proisstrict AND NOT p.proleakproof)`, signature, body, strings.Contains(prefix, "SECURITY DEFINER"), config, volatility).Scan(&valid)
	return valid, err
}

func trainingScheduleFunctionBody(name string) (string, error) {
	header, err := trainingScheduleFunctionHeader(name)
	if err != nil {
		return "", err
	}
	index := strings.Index(header, "AS $$")
	return header[index+5 : len(header)-3], nil
}

func trainingScheduleFunctionHeader(name string) (string, error) {
	sql, err := migrationFiles.ReadFile("migrations/" + TrainingScheduleCompatibilityMigration + ".sql")
	if err != nil {
		return "", err
	}
	return trainingScheduleExtractFunction(string(sql), name)
}

func trainingScheduleExtractFunction(s, name string) (string, error) {
	index := strings.LastIndex(s, "CREATE FUNCTION "+name+"(")
	if replacement := strings.LastIndex(s, "CREATE OR REPLACE FUNCTION "+name+"("); replacement > index {
		index = replacement
	}
	if index < 0 {
		return "", fmt.Errorf("training schedule function definition missing")
	}
	s = s[index:]
	index = strings.Index(s, "$$;")
	if index < 0 || !strings.Contains(s[:index], "AS $$") {
		return "", errors.New("training schedule function body missing")
	}
	return s[:index+3], nil
}

func trainingScheduleFunctionSignature(name string) string {
	args := map[string]string{
		"public.training_session_local_date":        "pg_catalog.date,pg_catalog.timestamptz",
		"public.training_session_schedule_valid":    "pg_catalog.text,pg_catalog.date,pg_catalog.timestamptz,pg_catalog.timestamptz,pg_catalog.text,public.training_entry_kind",
		"public.training_schedule_artifact_version": "pg_catalog.jsonb,pg_catalog.bool",
		"public.training_session_snapshot":          "pg_catalog.uuid", "public.restore_training_session": "pg_catalog.jsonb,pg_catalog.uuid,pg_catalog.timestamptz,pg_catalog.uuid",
		"public.training_session_snapshot_v2": "pg_catalog.uuid", "public.training_schedule_admit_partial": "pg_catalog.uuid,pg_catalog.int8",
		"public.training_schedule_assert_plan_v1": "pg_catalog.uuid", "public.training_schedule_assert_session_v1": "pg_catalog.uuid", "public.training_schedule_assert_routine_v1": "pg_catalog.uuid",
		"public.training_schedule_lock_target_v1": "pg_catalog.uuid,pg_catalog.text,pg_catalog.uuid",
		"training_schedule_control.admit_partial": "pg_catalog.uuid,pg_catalog.int8",
		"training_schedule_control.commit_floor":  "pg_catalog.uuid,pg_catalog.int8,pg_catalog.uuid,pg_catalog.text,pg_catalog.int4",
		"training_schedule_control.set_enabled":   "pg_catalog.uuid,pg_catalog.int8,pg_catalog.bool",
	}
	return name + "(" + args[name] + ")"
}

func hardenTrainingScheduleRoles(ctx context.Context, q bootstrapConnection, credentials RoleCredentials) error {
	roles := []string{credentials.AppUsername, mediaCleanupRole, dataRetentionRole, privacyRetentionRole, credentials.MediaCleanupUsername, credentials.DataRetentionUsername}
	var roleSQL []string
	for _, role := range roles {
		if role != "" {
			roleSQL = append(roleSQL, quoteLiteral(role))
		}
	}
	sql := `DO $$ DECLARE role_name pg_catalog.text; BEGIN
 IF pg_catalog.to_regnamespace('training_schedule_control') IS NULL THEN RETURN; END IF;
 REVOKE ALL ON SCHEMA training_schedule_control FROM PUBLIC;
 REVOKE ALL ON ALL TABLES IN SCHEMA training_schedule_control FROM PUBLIC;
 REVOKE ALL ON ALL FUNCTIONS IN SCHEMA training_schedule_control FROM PUBLIC;
 FOREACH role_name IN ARRAY ARRAY[` + strings.Join(roleSQL, ",") + `] LOOP
  IF EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname OPERATOR(pg_catalog.=) role_name::pg_catalog.name) THEN
   EXECUTE pg_catalog.format('REVOKE ALL ON SCHEMA training_schedule_control FROM %I',role_name);
   EXECUTE pg_catalog.format('REVOKE ALL ON ALL TABLES IN SCHEMA training_schedule_control FROM %I',role_name);
   EXECUTE pg_catalog.format('REVOKE ALL ON ALL FUNCTIONS IN SCHEMA training_schedule_control FROM %I',role_name);
  END IF;
 END LOOP;
 GRANT USAGE ON SCHEMA training_schedule_control TO ` + quoteIdentifier(credentials.AppUsername) + `;
 GRANT EXECUTE ON FUNCTION training_schedule_control.status() TO ` + quoteIdentifier(credentials.AppUsername) + `;
 GRANT EXECUTE ON FUNCTION public.training_schedule_artifact_version(pg_catalog.jsonb,pg_catalog.bool),
  public.training_session_snapshot_v2(pg_catalog.uuid),public.training_schedule_assert_plan_v1(pg_catalog.uuid),
  public.training_schedule_assert_session_v1(pg_catalog.uuid),public.training_schedule_assert_routine_v1(pg_catalog.uuid),
  public.training_schedule_lock_target_v1(pg_catalog.uuid,pg_catalog.text,pg_catalog.uuid),
  public.training_schedule_admit_partial(pg_catalog.uuid,pg_catalog.int8) TO ` + quoteIdentifier(credentials.AppUsername) + `;
 END $$`
	if _, err := q.Exec(ctx, sql); err != nil {
		return errors.New("harden training schedule privileges failed")
	}
	return nil
}

// TrainingScheduleWriteError preserves denial/stale errors and normalizes only
// the stable DB capability/unsupported sentinels for C callers.
func TrainingScheduleWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "P0001" && (pgErr.Message == "training_schedule_write_unsupported" || pgErr.Message == "training_schedule_writes_disabled") {
		return ErrTrainingScheduleWriteUnsupported
	}
	return err
}
