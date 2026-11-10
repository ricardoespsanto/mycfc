package db

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const TrainingDatedPlacementsMigration = "202611100005_training_dated_placements"
const TrainingPlacementReaderGeneration int32 = 4

// VerifyTrainingPlacementRuntime verifies the current feature's actual floor
// and definition identity. It is not a fleet upgrade/controller protocol.
func VerifyTrainingPlacementRuntime(ctx context.Context, q TrainingScheduleQueryReader, cap TrainingScheduleCapabilities) (TrainingScheduleState, error) {
	state, err := VerifyTrainingWorkoutGraphRuntime(ctx, q, cap)
	if err != nil {
		return state, err
	}
	if cap.ReaderVersion < TrainingPlacementReaderGeneration || state.RequiredReader < TrainingPlacementReaderGeneration {
		return state, errors.New("training placement reader floor not committed")
	}
	if err = verifyTrainingPlacementPrivileges(ctx, q); err != nil {
		return state, err
	}
	return state, nil
}

//go:embed training_placements_catalog.json
var trainingPlacementCatalogJSON []byte

func verifyTrainingPlacementDefinition(ctx context.Context, q TrainingScheduleQueryReader) error {
	var relations map[string]json.RawMessage
	if err := json.Unmarshal(trainingPlacementCatalogJSON, &relations); err != nil {
		return errors.New("training placement compiled catalog invalid")
	}
	tables := make([]string, 0, len(relations))
	for table := range relations {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		var valid bool
		if err := q.QueryRow(ctx, placementRelationContractSQL, table, string(relations[table])).Scan(&valid); err != nil || !valid {
			return fmt.Errorf("training placement relation mismatch: %s", table)
		}
	}

	signatures := map[string]string{
		"public.training_placement_group_programme":                        "uuid",
		"public.training_placement_actor_scope":                            "uuid,uuid",
		"public.training_placement_resolve":                                "uuid,uuid,uuid,date",
		"public.training_placement_privileged_operation":                   "text,uuid,uuid",
		"public.guard_training_placement_audit":                            "",
		"public.guard_training_prescription_withdrawal_immutable":          "",
		"public.guard_training_group_placement":                            "",
		"public.guard_training_placement_group_scope":                      "",
		"public.training_placement_touch_heads":                            "uuid,uuid[]",
		"public.touch_training_placement_candidates":                       "",
		"public.training_placement_lock_actor_subject":                     "uuid,uuid",
		"public.training_placement_latch_withdrawals":                      "uuid,uuid,text,date",
		"public.apply_training_placement_change":                           "uuid,uuid,uuid,uuid,uuid,text,uuid,uuid,date,date,bigint,text",
		"public.cancel_training_placement_change":                          "uuid,uuid,uuid,bigint,text",
		"public.training_prescription_current_delivery":                    "uuid",
		"public.guard_training_prescription_placement":                     "",
		"public.latch_training_placement_session_departures":               "",
		"public.guard_training_placement_head":                             "",
		"public.guard_training_prescription_withdrawal":                    "",
		"public.prevent_dated_participation_rewrite":                       "",
		"public.check_training_placement_participation_successor":          "",
		"public.training_placement_history_canonical":                      "uuid[],uuid[],uuid",
		"public.privacy_membership_history_compute_source":                 "uuid,timestamptz",
		"public.privacy_membership_history_compute_replay":                 "uuid,timestamptz",
		"public.guard_sealed_training_placement_history":                   "",
		"public.privacy_membership_history_lock_source":                    "uuid",
		"public.require_active_privacy_attachment_subject":                 "",
		"public.privacy_worker_execute_checkpoint_without_tombstone_guard": "uuid,uuid,uuid,bigint,uuid,text,text",
		"public.privacy_worker_execute_checkpoint_inner_017":               "uuid,uuid,uuid,bigint,uuid,text,text",
		"public.privacy_restore_apply_relational_operation":                "uuid,uuid,timestamptz,text",
		"public.privacy_restore_begin_replay_hardened":                     "uuid,uuid",
		"public.privacy_retention_pseudonymize_audit":                      "integer",
		"public.detach_training_placement_withdrawal_live_reference":       "",
		"public.lock_training_publication_placement_heads":                 "uuid,uuid",
		"public.guard_training_placement_team_scope":                       "",
		"public.lock_training_placement_existing_heads_for_join":           "uuid,uuid,uuid[]",
	}
	names := make([]string, 0, len(signatures))
	for name := range signatures {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		args := signatures[name]
		header, err := trainingScheduleExtractFunction(baselineSchema, name)
		if err != nil {
			header, err = trainingScheduleExtractFunction(baselineSchema, strings.TrimPrefix(name, "public."))
		}
		if err != nil {
			return err
		}
		valid, err := trainingScheduleFunctionMatches(ctx, q, name+"("+args+")", header)
		if err != nil || !valid {
			return fmt.Errorf("training placement definition mismatch: %s", name)
		}
	}
	before, _, ok := strings.Cut(baselineSchema, "\n-- Baseline through 202611100005_training_dated_placements.")
	if !ok {
		return errors.New("training placement baseline definition missing")
	}
	for _, kind := range []string{"source", "replay"} {
		name := "public.privacy_membership_history_compute_" + kind
		header, err := trainingScheduleExtractFunction(before, name)
		if err != nil {
			return err
		}
		valid, err := trainingScheduleFunctionMatches(ctx, q, name+"_inner_083(uuid,timestamptz)", header)
		if err != nil || !valid {
			return errors.New("training placement inner canonical definition mismatch")
		}
	}
	return nil
}

func verifyTrainingPlacementPrivileges(ctx context.Context, q TrainingScheduleQueryReader) error {
	var valid bool
	err := q.QueryRow(ctx, `SELECT /* training placement fixed API privileges */
 NOT pg_catalog.has_schema_privilege(current_user,'privacy_protected','USAGE')
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class relation JOIN pg_catalog.pg_namespace namespace ON namespace.oid=relation.relnamespace
 WHERE namespace.nspname='public' AND relation.relname IN('training_placement_heads','training_group_placements','training_placement_changes','training_prescription_withdrawals')
 AND (pg_catalog.has_table_privilege(current_user,relation.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') OR pg_catalog.has_any_column_privilege(current_user,relation.oid,'INSERT,UPDATE,REFERENCES')))
 AND NOT pg_catalog.has_function_privilege(current_user,'public.training_placement_privileged_operation(text,uuid,uuid)','EXECUTE')
 AND NOT pg_catalog.has_function_privilege(current_user,'public.training_placement_lock_actor_subject(uuid,uuid)','EXECUTE')
 AND NOT pg_catalog.has_function_privilege(current_user,'public.training_placement_latch_withdrawals(uuid,uuid,text,date)','EXECUTE')
 AND NOT pg_catalog.has_function_privilege(current_user,'public.training_placement_history_canonical(uuid[],uuid[],uuid)','EXECUTE')
 AND pg_catalog.has_function_privilege(current_user,'public.apply_training_placement_change(uuid,uuid,uuid,uuid,uuid,text,uuid,uuid,date,date,bigint,text)','EXECUTE')
 AND pg_catalog.has_function_privilege(current_user,'public.cancel_training_placement_change(uuid,uuid,uuid,bigint,text)','EXECUTE')
 AND pg_catalog.has_function_privilege(current_user,'public.lock_training_publication_placement_heads(uuid,uuid)','EXECUTE')
 AND pg_catalog.has_function_privilege(current_user,'public.training_placement_touch_heads(uuid,uuid[])','EXECUTE')
 AND pg_catalog.has_function_privilege(current_user,'public.lock_training_placement_existing_heads_for_join(uuid,uuid,uuid[])','EXECUTE')`).Scan(&valid)
	if err != nil || !valid {
		return errors.New("training placement fixed API privilege mismatch")
	}
	return nil
}

// Resolve only fixed catalog names without granting the web role USAGE or data
// access to privacy_protected. All observation remains in pg_catalog.
var placementRelationContractSQL = strings.Replace(recipientRelationContractSQL,
	"relation.oid OPERATOR(pg_catalog.=) pg_catalog.to_regclass($1::pg_catalog.text)",
	"pg_catalog.concat(namespace.nspname,'.',relation.relname) OPERATOR(pg_catalog.=) $1::pg_catalog.text", 1)
