package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/jackc/pgx/v5"
)

const TrainingPartialAuthoringMigration = "202611100003_training_partial_authoring"

const TrainingRecipientRevisionMigration = "202611100002_training_publication_recipient_revision"
const TrainingRecipientReaderGeneration int32 = 3

type RecipientSchemaPhase string

const (
	RecipientReviewed77 RecipientSchemaPhase = "reviewed77"
	RecipientReviewed78 RecipientSchemaPhase = "reviewed78"
	RecipientReviewed79 RecipientSchemaPhase = "reviewed79"
	RecipientReviewed80 RecipientSchemaPhase = "reviewed80"
	RecipientFinal      RecipientSchemaPhase = "recipient-final"
)

type RecipientSchemaInspection struct {
	Phase           RecipientSchemaPhase
	MigrationDigest string
	ScheduleState   *TrainingScheduleState
}

// InspectTrainingRecipientParent verifies an explicitly enumerated admission
// phase, including an already upgraded final. actualDigest must come from the
// independently authenticated, validated and locked ordinary-ledger inspector,
// never request input. No application schema code may run as bootstrap.
func InspectTrainingRecipientParent(ctx context.Context, q TrainingScheduleQueryReader, actualDigest string) (RecipientSchemaInspection, error) {
	result := RecipientSchemaInspection{MigrationDigest: actualDigest}
	if err := verifyTrainingRecipientLowSession(ctx, q); err != nil {
		return result, err
	}
	inventory, phase := trainingRecipientInventory(actualDigest)
	if inventory == nil {
		return result, errors.New("training recipient inventory unsupported")
	}
	var exact bool
	if err := q.QueryRow(ctx, `SELECT /* training recipient exact inventory */ (SELECT pg_catalog.array_agg(version ORDER BY version COLLATE pg_catalog."C") FROM mycfc_meta.schema_migrations) OPERATOR(pg_catalog.=) $1::pg_catalog.text[]`, inventory).Scan(&exact); err != nil || !exact {
		return result, errors.New("training recipient inventory mismatch")
	}
	if err := verifyTrainingScheduleAdmissionSchema(ctx, q, actualDigest); err != nil {
		return result, err
	}
	if err := verifyTrainingRecipientCatalog(ctx, q, phase != RecipientFinal && phase != RecipientReviewed80, phase == RecipientReviewed79 || phase == RecipientReviewed80 || phase == RecipientFinal); err != nil {
		return result, err
	}
	result.Phase = phase
	if phase == RecipientReviewed79 || phase == RecipientReviewed80 || phase == RecipientFinal {
		state, err := TrainingScheduleStatus(ctx, q)
		if err != nil {
			return result, err
		}
		if phase == RecipientFinal {
			if state.SchemaContract != TrainingScheduleContractP {
				return result, errors.New("training recipient final schedule phase mismatch")
			}
		} else if state.SchemaContract != TrainingScheduleContractC || state.PartialV2Enabled || state.EverPartialV2Enabled {
			return result, errors.New("training recipient closed parent schedule phase mismatch")
		}
		result.ScheduleState = &state
	}
	return result, nil
}

func trainingRecipientInventory(digest string) ([]string, RecipientSchemaPhase) {
	excluded := map[string]bool{}
	var phase RecipientSchemaPhase
	switch digest {
	case releasecontract.NormalFinalDigest:
		phase = RecipientFinal
	case releasecontract.NormalAuthoringParentDigest:
		phase = RecipientReviewed80
		excluded[TrainingPartialAuthoringMigration] = true
	case releasecontract.NormalRecipientParentDigest:
		phase = RecipientReviewed79
		excluded[TrainingRecipientRevisionMigration] = true
	case releasecontract.NormalParentDigest:
		phase = RecipientReviewed78
		excluded[TrainingRecipientRevisionMigration] = true
		excluded[TrainingScheduleCompatibilityMigration] = true
	case releasecontract.FreshGenesisDigest:
		phase = RecipientReviewed77
		excluded[TrainingRecipientRevisionMigration] = true
		excluded[TrainingScheduleCompatibilityMigration] = true
		excluded["202610010003_privacy_attachment_trigger_runtime"] = true
	default:
		return nil, ""
	}
	if phase != RecipientFinal {
		excluded[TrainingPartialAuthoringMigration] = true
	}
	var inventory []string
	for _, version := range EmbeddedMigrationInventory() {
		if !excluded[version] {
			inventory = append(inventory, version)
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(inventory, "\n")))
	if hex.EncodeToString(sum[:]) != digest {
		return nil, ""
	}
	return inventory, phase
}

// VerifyTrainingRecipientMigrationFinal runs only on the actual non-superuser
// migration transaction. It deliberately permits pre-floor state: the externally
// authenticated initial floor CAS happens later while publishers remain fenced.
func VerifyTrainingRecipientMigrationFinal(ctx context.Context, q TrainingScheduleQueryReader) error {
	if EmbeddedMigrationDigest() != releasecontract.NormalFinalDigest {
		return errors.New("training recipient compiled inventory mismatch")
	}
	if err := VerifyTrainingScheduleMigrationFinal(ctx, q); err != nil {
		return err
	}
	return verifyTrainingRecipientCatalog(ctx, q, false)
}

// VerifyTrainingRecipientRuntime proves DB facts only. The external controller /
// restore fence remains independently mandatory; copied matching DB fields are
// not proof. Serving requires corrected schema and actual floor >=3.
func VerifyTrainingRecipientRuntime(ctx context.Context, q TrainingScheduleQueryReader, cap TrainingScheduleCapabilities) (TrainingScheduleState, error) {
	if err := verifyTrainingRecipientLowSession(ctx, q); err != nil {
		return TrainingScheduleState{}, err
	}
	if cap.ReaderVersion < TrainingRecipientReaderGeneration {
		return TrainingScheduleState{}, errors.New("training recipient capability mismatch")
	}
	if err := verifyTrainingRecipientCatalog(ctx, q, false); err != nil {
		return TrainingScheduleState{}, err
	}
	state, err := VerifyTrainingScheduleRuntime(ctx, q, cap)
	if err != nil {
		return state, err
	}
	if state.RequiredReader < TrainingRecipientReaderGeneration {
		return state, errors.New("training recipient reader floor not committed")
	}
	return state, nil
}

func verifyTrainingRecipientCatalog(ctx context.Context, q TrainingScheduleQueryReader, sourceUnique bool, hasSchedule ...bool) error {
	if err := verifyTrainingRecipientPlanWitness(ctx, q); err != nil {
		return err
	}
	schedule := true
	if len(hasSchedule) > 0 {
		schedule = hasSchedule[0]
	}
	for _, table := range []string{"training_plan_publications", "training_prescriptions"} {
		expected, err := json.Marshal(trainingRecipientRelationSpec(table, sourceUnique, schedule))
		if err != nil {
			return err
		}
		var valid bool
		if err = q.QueryRow(ctx, recipientRelationContractSQL, "public."+table, string(expected)).Scan(&valid); err != nil || !valid {
			return fmt.Errorf("training recipient catalog contract mismatch: %s", table)
		}
	}
	header, err := trainingScheduleExtractFunction(baselineSchema, "prevent_training_publication_mutation")
	if err != nil {
		return err
	}
	if valid, err := trainingScheduleFunctionMatches(ctx, q, "public.prevent_training_publication_mutation()", header); err != nil || !valid {
		return errors.New("training recipient immutable function mismatch")
	}
	return nil
}

// All observations are fixed pg_catalog reads, never application-owned code.
// Exact maps reject renamed/equivalent extra uniqueness as well as absent guards.
const recipientRelationContractSQL = `SELECT /* training recipient relation contract */ pg_catalog.jsonb_build_object(
 'ordinary',relation.relkind OPERATOR(pg_catalog.=) 'r'::pg_catalog."char" AND relation.relpersistence OPERATOR(pg_catalog.=) 'p'::pg_catalog."char" AND NOT relation.relrowsecurity AND NOT relation.relforcerowsecurity AND relation.relowner OPERATOR(pg_catalog.=) namespace.nspowner AND namespace.nspowner OPERATOR(pg_catalog.=) (SELECT metadata.nspowner FROM pg_catalog.pg_namespace metadata WHERE metadata.nspname OPERATOR(pg_catalog.=) 'mycfc_meta'::pg_catalog.name) AND NOT owner.rolsuper AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_inherits inheritance WHERE inheritance.inhrelid OPERATOR(pg_catalog.=) relation.oid OR inheritance.inhparent OPERATOR(pg_catalog.=) relation.oid) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_rewrite rewrite WHERE rewrite.ev_class OPERATOR(pg_catalog.=) relation.oid),
 'columns',(SELECT pg_catalog.jsonb_object_agg(column_row.attname,pg_catalog.jsonb_build_array(type_namespace.nspname::pg_catalog.text,type_row.typname::pg_catalog.text,column_row.atttypmod,column_row.attnotnull,column_row.atthasdef,column_row.attidentity::pg_catalog.text,column_row.attgenerated::pg_catalog.text,column_row.attndims)) FROM pg_catalog.pg_attribute column_row JOIN pg_catalog.pg_type type_row ON type_row.oid OPERATOR(pg_catalog.=) column_row.atttypid JOIN pg_catalog.pg_namespace type_namespace ON type_namespace.oid OPERATOR(pg_catalog.=) type_row.typnamespace WHERE column_row.attrelid OPERATOR(pg_catalog.=) relation.oid AND column_row.attnum OPERATOR(pg_catalog.>) 0::pg_catalog.int2 AND NOT column_row.attisdropped),
 'constraints',(SELECT pg_catalog.jsonb_object_agg(constraint_row.conname,pg_catalog.jsonb_build_array(pg_catalog.replace(pg_catalog.replace(pg_catalog.pg_get_constraintdef(constraint_row.oid),'public.'::pg_catalog.text,''::pg_catalog.text),'pg_catalog.'::pg_catalog.text,''::pg_catalog.text),constraint_row.convalidated,constraint_row.condeferrable,constraint_row.condeferred,constraint_row.conislocal,constraint_row.coninhcount)) FROM pg_catalog.pg_constraint constraint_row WHERE constraint_row.conrelid OPERATOR(pg_catalog.=) relation.oid),
 'indexes',(SELECT pg_catalog.jsonb_object_agg(index_relation.relname,pg_catalog.jsonb_build_array(pg_catalog.replace(pg_catalog.pg_get_indexdef(index_row.indexrelid),'public.'::pg_catalog.text,''::pg_catalog.text),index_row.indisvalid,index_row.indisready,index_row.indislive,index_row.indpred IS NULL,index_row.indexprs IS NULL,index_relation.relowner OPERATOR(pg_catalog.=) relation.relowner,NOT EXISTS(SELECT 1 FROM pg_catalog.unnest(index_row.indclass::pg_catalog.oid[]) class_oid JOIN pg_catalog.pg_opclass operator_class ON operator_class.oid OPERATOR(pg_catalog.=) class_oid JOIN pg_catalog.pg_namespace operator_namespace ON operator_namespace.oid OPERATOR(pg_catalog.=) operator_class.opcnamespace WHERE operator_namespace.nspname OPERATOR(pg_catalog.<>) 'pg_catalog'::pg_catalog.name))) FROM pg_catalog.pg_index index_row JOIN pg_catalog.pg_class index_relation ON index_relation.oid OPERATOR(pg_catalog.=) index_row.indexrelid WHERE index_row.indrelid OPERATOR(pg_catalog.=) relation.oid),
 'triggers',(SELECT pg_catalog.jsonb_object_agg(trigger_row.tgname,pg_catalog.jsonb_build_array(pg_catalog.replace(trigger_row.tgfoid::pg_catalog.regprocedure::pg_catalog.text,'public.'::pg_catalog.text,''::pg_catalog.text),trigger_row.tgtype,(trigger_row.tgenabled OPERATOR(pg_catalog.=) 'O'::pg_catalog."char" OR trigger_row.tgenabled OPERATOR(pg_catalog.=) 'A'::pg_catalog."char"),pg_catalog.encode(trigger_row.tgargs,'hex'::pg_catalog.text),(SELECT COALESCE(pg_catalog.jsonb_agg(attribute.attname::pg_catalog.text ORDER BY ordinal),'[]'::pg_catalog.jsonb) FROM pg_catalog.unnest(trigger_row.tgattr::pg_catalog.int2[]) WITH ORDINALITY AS columns(attnum,ordinal) JOIN pg_catalog.pg_attribute attribute ON attribute.attrelid OPERATOR(pg_catalog.=) relation.oid AND attribute.attnum OPERATOR(pg_catalog.=) columns.attnum),trigger_row.tgqual IS NULL,NOT trigger_row.tgdeferrable,NOT trigger_row.tginitdeferred)) FROM pg_catalog.pg_trigger trigger_row WHERE trigger_row.tgrelid OPERATOR(pg_catalog.=) relation.oid AND NOT trigger_row.tgisinternal),
 'builtin_expressions',NOT EXISTS(SELECT 1 FROM pg_catalog.pg_depend dependency WHERE
 ((dependency.classid OPERATOR(pg_catalog.=) 'pg_catalog.pg_constraint'::pg_catalog.regclass AND EXISTS(SELECT 1 FROM pg_catalog.pg_constraint referenced_constraint WHERE referenced_constraint.conrelid OPERATOR(pg_catalog.=) relation.oid AND referenced_constraint.oid OPERATOR(pg_catalog.=) dependency.objid)) OR (dependency.classid OPERATOR(pg_catalog.=) 'pg_catalog.pg_attrdef'::pg_catalog.regclass AND EXISTS(SELECT 1 FROM pg_catalog.pg_attrdef referenced_default WHERE referenced_default.adrelid OPERATOR(pg_catalog.=) relation.oid AND referenced_default.oid OPERATOR(pg_catalog.=) dependency.objid))) AND
 ((dependency.refclassid OPERATOR(pg_catalog.=) 'pg_catalog.pg_proc'::pg_catalog.regclass AND EXISTS(SELECT 1 FROM pg_catalog.pg_proc function_row WHERE function_row.oid OPERATOR(pg_catalog.=) dependency.refobjid AND function_row.pronamespace OPERATOR(pg_catalog.<>) 'pg_catalog'::pg_catalog.regnamespace)) OR
 (dependency.refclassid OPERATOR(pg_catalog.=) 'pg_catalog.pg_operator'::pg_catalog.regclass AND EXISTS(SELECT 1 FROM pg_catalog.pg_operator operator_row WHERE operator_row.oid OPERATOR(pg_catalog.=) dependency.refobjid AND operator_row.oprnamespace OPERATOR(pg_catalog.<>) 'pg_catalog'::pg_catalog.regnamespace)) OR
 (dependency.refclassid OPERATOR(pg_catalog.=) 'pg_catalog.pg_type'::pg_catalog.regclass AND EXISTS(SELECT 1 FROM pg_catalog.pg_type type_row WHERE type_row.oid OPERATOR(pg_catalog.=) dependency.refobjid AND type_row.typnamespace OPERATOR(pg_catalog.<>) 'pg_catalog'::pg_catalog.regnamespace))))
 ) OPERATOR(pg_catalog.=) $2::pg_catalog.jsonb
 FROM pg_catalog.pg_class relation JOIN pg_catalog.pg_namespace namespace ON namespace.oid OPERATOR(pg_catalog.=) relation.relnamespace JOIN pg_catalog.pg_roles owner ON owner.oid OPERATOR(pg_catalog.=) relation.relowner WHERE relation.oid OPERATOR(pg_catalog.=) pg_catalog.to_regclass($1::pg_catalog.text)`

func trainingRecipientRelationSpec(table string, sourceUnique, schedule bool) map[string]any {
	columns := map[string]any{}
	column := func(name, typ string, modifier int, notnull, hasDefault bool) {
		columns[name] = []any{"pg_catalog", typ, modifier, notnull, hasDefault, "", "", 0}
	}
	constraint := func(def string) any { return []any{def, true, false, false, true, 0} }
	index := func(def string) any { return []any{def, true, true, true, true, true, true, true} }
	trigger := func(function string, typ int, args string, cols ...string) any {
		if cols == nil {
			cols = []string{}
		}
		return []any{function, typ, true, hex.EncodeToString([]byte(args)), cols, true, true, true}
	}
	constraints := map[string]any{}
	indexes := map[string]any{}
	triggers := map[string]any{}
	column("id", "uuid", -1, true, true)
	if table == "training_plan_publications" {
		column("plan_id", "uuid", -1, true, false)
		column("revision", "int4", -1, true, false)
		column("source_updated_at", "timestamptz", -1, true, false)
		column("change_summary", "varchar", 504, true, false)
		column("supersedes_id", "uuid", -1, false, false)
		column("published_by_id", "uuid", -1, true, false)
		column("published_at", "timestamptz", -1, true, true)
		constraints[table+"_plan_id_fkey"] = constraint("FOREIGN KEY (plan_id) REFERENCES training_plans(id) ON DELETE RESTRICT")
		constraints[table+"_published_by_id_fkey"] = constraint("FOREIGN KEY (published_by_id) REFERENCES users(id) ON DELETE RESTRICT")
		constraints[table+"_supersedes_id_fkey"] = constraint("FOREIGN KEY (supersedes_id) REFERENCES training_plan_publications(id) ON DELETE RESTRICT")
		constraints[table+"_revision_valid"] = constraint("CHECK ((revision > 0))")
		constraints[table+"_summary_valid"] = constraint("CHECK ((((change_summary)::text = btrim((change_summary)::text)) AND ((char_length((change_summary)::text) >= 2) AND (char_length((change_summary)::text) <= 500))))")
		constraints[table+"_revision_unique"] = constraint("UNIQUE (plan_id, revision)")
		indexes[table+"_revision_unique"] = index("CREATE UNIQUE INDEX training_plan_publications_revision_unique ON training_plan_publications USING btree (plan_id, revision)")
		indexes[table+"_plan_idx"] = index("CREATE INDEX training_plan_publications_plan_idx ON training_plan_publications USING btree (plan_id, revision DESC)")
		if sourceUnique {
			constraints[table+"_source_unique"] = constraint("UNIQUE (plan_id, source_updated_at)")
			indexes[table+"_source_unique"] = index("CREATE UNIQUE INDEX training_plan_publications_source_unique ON training_plan_publications USING btree (plan_id, source_updated_at)")
		}
		if schedule {
			triggers[table+"_schedule_guard"] = trigger("training_schedule_control.guard_publication()", 7, "")
		}
	} else {
		for _, name := range []string{"publication_id", "session_id", "membership_id", "athlete_user_id"} {
			column(name, "uuid", -1, true, false)
		}
		column("snapshot", "jsonb", -1, true, false)
		column("snapshot_sha256", "varchar", 68, true, false)
		column("created_at", "timestamptz", -1, true, true)
		for name, ref := range map[string]string{"publication_id": "training_plan_publications", "session_id": "training_sessions", "membership_id": "user_memberships", "athlete_user_id": "users"} {
			constraints[table+"_"+name+"_fkey"] = constraint("FOREIGN KEY (" + name + ") REFERENCES " + ref + "(id) ON DELETE RESTRICT")
		}
		constraints[table+"_hash_valid"] = constraint("CHECK (((snapshot_sha256)::text ~ '^[0-9a-f]{64}$'::text))")
		constraints[table+"_snapshot_valid"] = constraint("CHECK (((jsonb_typeof(snapshot) = 'object'::text) AND (octet_length((snapshot)::text) <= 200000)))")
		constraints[table+"_publication_session_membership_unique"] = constraint("UNIQUE (publication_id, session_id, membership_id)")
		indexes[table+"_publication_session_membership_unique"] = index("CREATE UNIQUE INDEX training_prescriptions_publication_session_membership_unique ON training_prescriptions USING btree (publication_id, session_id, membership_id)")
		indexes[table+"_athlete_idx"] = index("CREATE INDEX training_prescriptions_athlete_idx ON training_prescriptions USING btree (athlete_user_id, created_at DESC)")
		indexes[table+"_session_idx"] = index("CREATE INDEX training_prescriptions_session_idx ON training_prescriptions USING btree (session_id, created_at DESC)")
		triggers[table+"_active_subject_attachment"] = trigger("require_active_privacy_attachment_subject()", 23, "athlete_user_id\x00", "athlete_user_id")
		if schedule {
			triggers[table+"_schedule_guard"] = trigger("training_schedule_control.guard_artifact()", 7, "")
		}
	}
	constraints[table+"_pkey"] = constraint("PRIMARY KEY (id)")
	indexes[table+"_pkey"] = index("CREATE UNIQUE INDEX " + table + "_pkey ON " + table + " USING btree (id)")
	triggers[table+"_immutable_trigger"] = trigger("prevent_training_publication_mutation()", 27, "")
	return map[string]any{"ordinary": true, "columns": columns, "constraints": constraints, "indexes": indexes, "triggers": triggers, "builtin_expressions": true}
}

// RecipientPublisherState contains bounded fixed-catalog facts, no query text,
// session content, credentials or private guardian-control state.
type RecipientPublisherState struct {
	OID                      uint32
	Name                     string
	CanLogin                 bool
	BackendCount             int64
	PreparedCount            int64
	UnexpectedPublisherCount int64
}

// InspectTrainingRecipientPublisher performs fixed catalog observation. The
// caller supplies the actual configured/compiled runtime login, never a request-
// selected role. Low phase inspection must authenticate independently first.
func InspectTrainingRecipientPublisher(ctx context.Context, q TrainingScheduleQueryReader, expectedRole string) (RecipientPublisherState, error) {
	var state RecipientPublisherState
	if !postgresIdentifier.MatchString(expectedRole) {
		return state, errors.New("training recipient publisher identity invalid")
	}
	err := q.QueryRow(ctx, `SELECT /* training recipient publisher fence */ publisher.oid,publisher.rolname::pg_catalog.text,publisher.rolcanlogin,
 (SELECT pg_catalog.count(*) FROM pg_catalog.pg_stat_activity activity WHERE activity.datid OPERATOR(pg_catalog.=) database.oid AND activity.usesysid OPERATOR(pg_catalog.=) publisher.oid),
 (SELECT pg_catalog.count(*) FROM pg_catalog.pg_prepared_xacts prepared WHERE prepared.database OPERATOR(pg_catalog.=) database.datname AND prepared.owner OPERATOR(pg_catalog.=) publisher.rolname),
 (SELECT pg_catalog.count(*) FROM pg_catalog.pg_roles candidate WHERE candidate.rolcanlogin AND NOT candidate.rolsuper AND candidate.oid OPERATOR(pg_catalog.<>) publisher.oid AND candidate.oid OPERATOR(pg_catalog.<>) relation.relowner
 AND (pg_catalog.has_table_privilege(candidate.oid,relation.oid,'INSERT'::pg_catalog.text) OR pg_catalog.pg_has_role(candidate.oid,publisher.oid,'MEMBER'::pg_catalog.text) OR pg_catalog.pg_has_role(candidate.oid,relation.relowner,'MEMBER'::pg_catalog.text)))
 FROM pg_catalog.pg_roles publisher CROSS JOIN pg_catalog.pg_database database CROSS JOIN pg_catalog.pg_class relation
 WHERE publisher.rolname OPERATOR(pg_catalog.=) $1::pg_catalog.name AND database.datname OPERATOR(pg_catalog.=) pg_catalog.current_database() AND relation.oid OPERATOR(pg_catalog.=) pg_catalog.to_regclass('public.training_plan_publications'::pg_catalog.text) AND relation.relkind OPERATOR(pg_catalog.=) 'r'::pg_catalog."char"`, expectedRole).Scan(&state.OID, &state.Name, &state.CanLogin, &state.BackendCount, &state.PreparedCount, &state.UnexpectedPublisherCount)
	if err != nil || state.OID == 0 || state.Name != expectedRole || state.BackendCount < 0 || state.PreparedCount < 0 || state.UnexpectedPublisherCount < 0 {
		return state, errors.New("training recipient publisher inspection rejected")
	}
	return state, nil
}

func RequireTrainingRecipientPublisherFence(ctx context.Context, q TrainingScheduleQueryReader, expectedRole string) (RecipientPublisherState, error) {
	state, err := InspectTrainingRecipientPublisher(ctx, q, expectedRole)
	if err != nil {
		return state, err
	}
	if state.CanLogin || state.BackendCount != 0 || state.PreparedCount != 0 || state.UnexpectedPublisherCount != 0 {
		return state, errors.New("training recipient publisher not quiescent")
	}
	return state, nil
}

type RecipientDatabaseInspector interface {
	FreshInitializationInspector
	QueryRow(context.Context, string, ...any) pgx.Row
}
type RecipientInspectionMode string

const (
	RecipientInspectParent    RecipientInspectionMode = "parent"
	RecipientInspectFinal     RecipientInspectionMode = "final"
	RecipientInspectAdmission RecipientInspectionMode = "admission" // internal mutation wrapper only; never a CLI flag
)

type RecipientDatabaseInspectionInput struct {
	Database, MigrationRole, PublisherRole string
	Mode                                   RecipientInspectionMode
	InspectionTimeout                      time.Duration
	CleanupTimeout                         time.Duration
}
type RecipientDatabaseInspection struct {
	RecipientSchemaInspection
	DatabaseOID    uint32
	InspectionRole string
	Publisher      RecipientPublisherState
}

// InspectTrainingRecipientDatabase owns a bounded read-only low inspection. It
// never accepts bootstrap+SET ROLE, an alternate URL, fabricated pre-state, or an
// unknown phase. Observation honestly reports LOGIN/nonzero counts; mutation
// callers additionally require the publisher fence before changing anything.
func InspectTrainingRecipientDatabase(ctx context.Context, conn RecipientDatabaseInspector, input RecipientDatabaseInspectionInput) (RecipientDatabaseInspection, error) {
	var result RecipientDatabaseInspection
	if conn == nil || input.InspectionTimeout <= 0 || input.CleanupTimeout <= 0 || !postgresIdentifier.MatchString(input.Database) || !postgresIdentifier.MatchString(input.MigrationRole) || !postgresIdentifier.MatchString(input.PublisherRole) || (input.Mode != RecipientInspectParent && input.Mode != RecipientInspectFinal && input.Mode != RecipientInspectAdmission) {
		return result, errors.New("training recipient inspection input invalid")
	}
	physical, ok := conn.(*pgx.Conn)
	if !ok || physical == nil {
		return result, errors.New("training recipient inspector requires original one-shot connection")
	}
	inspectionCtx, cancel := context.WithTimeout(ctx, input.InspectionTimeout)
	defer cancel()
	ctx = inspectionCtx
	clean := false
	defer func() {
		if !clean {
			_ = physical.Close(ctx)
		}
	}()
	tx, digest, err := inspectFreshInitializationLedger(ctx, conn, conn, input.MigrationRole, input.Database)
	if err != nil {
		return result, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(ctx, input.CleanupTimeout)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	phase, err := InspectTrainingRecipientParent(ctx, tx, digest)
	if err != nil {
		return result, err
	}
	if input.Mode != RecipientInspectAdmission && (input.Mode == RecipientInspectFinal) != (phase.Phase == RecipientFinal) {
		return result, errors.New("training recipient inspection phase mismatch")
	}
	result.RecipientSchemaInspection = phase
	if err = tx.QueryRow(ctx, `SELECT oid,session_user::pg_catalog.text FROM pg_catalog.pg_database WHERE datname OPERATOR(pg_catalog.=) pg_catalog.current_database()`).Scan(&result.DatabaseOID, &result.InspectionRole); err != nil || result.DatabaseOID == 0 {
		return result, errors.New("training recipient database identity unavailable")
	}
	result.Publisher, err = InspectTrainingRecipientPublisher(ctx, tx, input.PublisherRole)
	if err != nil {
		return result, err
	}
	cleanup, cleanupCancel := context.WithTimeout(ctx, input.CleanupTimeout)
	rollbackErr := tx.Rollback(cleanup)
	cleanupCancel()
	if rollbackErr != nil || physical.IsClosed() || physical.PgConn().IsBusy() || physical.PgConn().TxStatus() != 'I' {
		return RecipientDatabaseInspection{}, errors.New("training recipient inspection cleanup failed")
	}
	clean = true
	return result, nil
}

// This guard runs only fixed catalogs, BEFORE any migration-owned status call.
// The separate empty-init final verifier executes catalogs only and retains its
// explicitly reviewed bootstrap SET ROLE exception; normal inspection does not.
func verifyTrainingRecipientLowSession(ctx context.Context, q TrainingScheduleQueryReader) error {
	var allowed bool
	err := q.QueryRow(ctx, `SELECT /* training recipient low identity */ session_user OPERATOR(pg_catalog.=) current_user AND NOT role.rolsuper AND NOT role.rolcreaterole AND NOT role.rolreplication AND NOT role.rolbypassrls
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_roles privileged WHERE (privileged.rolsuper OR privileged.rolcreaterole OR privileged.rolreplication OR privileged.rolbypassrls OR privileged.rolname OPERATOR(pg_catalog.=) 'pg_execute_server_program'::pg_catalog.name OR privileged.rolname OPERATOR(pg_catalog.=) 'pg_read_server_files'::pg_catalog.name OR privileged.rolname OPERATOR(pg_catalog.=) 'pg_write_server_files'::pg_catalog.name) AND pg_catalog.pg_has_role(session_user,privileged.oid,'MEMBER'::pg_catalog.text))
 FROM pg_catalog.pg_roles role WHERE role.rolname OPERATOR(pg_catalog.=) session_user`).Scan(&allowed)
	if err != nil || !allowed {
		return errors.New("training recipient requires independently authenticated unprivileged session")
	}
	return nil
}

// IsTrainingRecipientEmptyTarget is the existing explicit fresh-init proof,
// using only fixed catalogs on the original bootstrap connection. Missing app
// tables/ledger alone is never sufficient. Post-bootstrap extension-only state
// is deliberately not classified as this bare empty phase.
func IsTrainingRecipientEmptyTarget(ctx context.Context, q TrainingScheduleQueryReader, expectedDatabase string) (bool, error) {
	if !postgresIdentifier.MatchString(expectedDatabase) {
		return false, errors.New("training recipient empty target database invalid")
	}
	var empty bool
	err := q.QueryRow(ctx, `SELECT /* training recipient empty target */ pg_catalog.current_database() OPERATOR(pg_catalog.=) $1::pg_catalog.name AND session_user OPERATOR(pg_catalog.=) current_user
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_roles role WHERE role.rolname OPERATOR(pg_catalog.=) current_user AND role.rolsuper)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_depend dependency WHERE dependency.refclassid OPERATOR(pg_catalog.=) 'pg_catalog.pg_namespace'::pg_catalog.regclass AND dependency.refobjid OPERATOR(pg_catalog.=) 'public'::pg_catalog.regnamespace)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace namespace WHERE namespace.nspname OPERATOR(pg_catalog.<>) 'public'::pg_catalog.name AND namespace.nspname OPERATOR(pg_catalog.<>) 'pg_catalog'::pg_catalog.name AND namespace.nspname OPERATOR(pg_catalog.<>) 'information_schema'::pg_catalog.name AND namespace.nspname::pg_catalog.text OPERATOR(pg_catalog.!~~) 'pg_toast%'::pg_catalog.text AND namespace.nspname::pg_catalog.text OPERATOR(pg_catalog.!~~) 'pg_temp%'::pg_catalog.text)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_extension extension WHERE extension.extnamespace OPERATOR(pg_catalog.=) 'public'::pg_catalog.regnamespace)`, expectedDatabase).Scan(&empty)
	if err != nil {
		return false, errors.New("training recipient empty target inspection failed")
	}
	return empty, nil
}

// Same-value publication INSERT witnesses rely on an ordinary, unfiltered plan
// tuple with no user UPDATE trigger/rule that changes, audits or suppresses the update.
// Grouped nullability is established by the actual validated CHECK, not columns.
func verifyTrainingRecipientPlanWitness(ctx context.Context, q TrainingScheduleQueryReader) error {
	const expectedCheck = "CHECK ((((training_group_id IS NULL) AND (week_start IS NULL) AND (season_id IS NULL) AND (cycle_id IS NULL)) OR ((training_group_id IS NOT NULL) AND (week_start IS NOT NULL) AND (season_id IS NOT NULL) AND (EXTRACT(isodow FROM week_start) = (1)::numeric))))"
	var valid bool
	err := q.QueryRow(ctx, `SELECT /* training recipient plan witness contract */ EXISTS(
 SELECT 1 FROM pg_catalog.pg_class relation JOIN pg_catalog.pg_namespace namespace ON namespace.oid OPERATOR(pg_catalog.=) relation.relnamespace JOIN pg_catalog.pg_roles owner ON owner.oid OPERATOR(pg_catalog.=) relation.relowner
 WHERE relation.oid OPERATOR(pg_catalog.=) pg_catalog.to_regclass('public.training_plans'::pg_catalog.text) AND relation.relkind OPERATOR(pg_catalog.=) 'r'::pg_catalog."char" AND relation.relpersistence OPERATOR(pg_catalog.=) 'p'::pg_catalog."char" AND NOT relation.relrowsecurity AND NOT relation.relforcerowsecurity AND relation.relowner OPERATOR(pg_catalog.=) namespace.nspowner AND NOT owner.rolsuper
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_inherits inheritance WHERE inheritance.inhrelid OPERATOR(pg_catalog.=) relation.oid OR inheritance.inhparent OPERATOR(pg_catalog.=) relation.oid)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_rewrite rewrite WHERE rewrite.ev_class OPERATOR(pg_catalog.=) relation.oid)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_trigger trigger_row WHERE trigger_row.tgrelid OPERATOR(pg_catalog.=) relation.oid AND NOT trigger_row.tgisinternal AND (trigger_row.tgtype OPERATOR(pg_catalog.&) 16::pg_catalog.int2) OPERATOR(pg_catalog.<>) 0::pg_catalog.int2)
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_attribute attribute WHERE attribute.attrelid OPERATOR(pg_catalog.=) relation.oid AND attribute.attname OPERATOR(pg_catalog.=) 'updated_at'::pg_catalog.name AND attribute.atttypid OPERATOR(pg_catalog.=) 'pg_catalog.timestamptz'::pg_catalog.regtype AND attribute.attnotnull AND NOT attribute.attisdropped AND attribute.attgenerated OPERATOR(pg_catalog.=) ''::pg_catalog."char")
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_constraint constraint_row WHERE constraint_row.conrelid OPERATOR(pg_catalog.=) relation.oid AND constraint_row.conname OPERATOR(pg_catalog.=) 'training_plans_week_valid'::pg_catalog.name AND constraint_row.convalidated AND pg_catalog.pg_get_constraintdef(constraint_row.oid) OPERATOR(pg_catalog.=) $1::pg_catalog.text)
 )`, expectedCheck).Scan(&valid)
	if err != nil || !valid {
		return errors.New("training recipient plan witness contract mismatch")
	}
	return nil
}
