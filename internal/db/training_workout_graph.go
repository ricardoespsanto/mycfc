package db

import (
	"context"
	"errors"
)

// VerifyTrainingWorkoutGraphRuntime adds the full CREATE writer's definition
// contract to corrected recipient/schedule checks. Caller admits the expected
// schedule incarnation/epoch FIRST in its transaction. It never enables gates.
func VerifyTrainingWorkoutGraphRuntime(ctx context.Context, q TrainingScheduleQueryReader, cap TrainingScheduleCapabilities) (TrainingScheduleState, error) {
	state, err := VerifyTrainingRecipientRuntime(ctx, q, cap)
	if err != nil {
		return state, err
	}
	if cap.WriterVersion != 2 || state.SchemaContract != TrainingScheduleContractP {
		return state, errors.New("workout graph writer capability mismatch")
	}
	if err = verifyTrainingWorkoutGraphDefinition(ctx, q); err != nil {
		return state, err
	}
	return state, nil
}

func verifyTrainingWorkoutGraphDefinition(ctx context.Context, q TrainingScheduleQueryReader) error {
	var valid bool
	err := q.QueryRow(ctx, `SELECT /* workout graph profile version witness */ EXISTS(
 SELECT 1 FROM pg_catalog.pg_class relation JOIN pg_catalog.pg_namespace namespace ON namespace.oid OPERATOR(pg_catalog.=) relation.relnamespace JOIN pg_catalog.pg_roles owner ON owner.oid OPERATOR(pg_catalog.=) relation.relowner
 WHERE relation.oid OPERATOR(pg_catalog.=) pg_catalog.to_regclass('public.water_intensity_profiles'::pg_catalog.text)
 AND relation.relkind OPERATOR(pg_catalog.=) 'r'::pg_catalog."char" AND relation.relpersistence OPERATOR(pg_catalog.=) 'p'::pg_catalog."char"
 AND NOT relation.relispartition AND NOT relation.relrowsecurity AND NOT relation.relforcerowsecurity AND NOT owner.rolsuper AND relation.relowner OPERATOR(pg_catalog.=) namespace.nspowner
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_inherits inheritance WHERE inheritance.inhrelid OPERATOR(pg_catalog.=) relation.oid OR inheritance.inhparent OPERATOR(pg_catalog.=) relation.oid)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_rewrite rewrite WHERE rewrite.ev_class OPERATOR(pg_catalog.=) relation.oid)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_trigger trigger_row WHERE trigger_row.tgrelid OPERATOR(pg_catalog.=) relation.oid AND NOT trigger_row.tgisinternal AND (trigger_row.tgtype OPERATOR(pg_catalog.&) 16::pg_catalog.int2) OPERATOR(pg_catalog.<>) 0::pg_catalog.int2 AND trigger_row.tgname OPERATOR(pg_catalog.<>) 'water_intensity_profiles_rhythm_plan_witness'::pg_catalog.name)
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_attribute attribute WHERE attribute.attrelid OPERATOR(pg_catalog.=) relation.oid AND attribute.attname OPERATOR(pg_catalog.=) 'is_active'::pg_catalog.name AND attribute.atttypid OPERATOR(pg_catalog.=) 'pg_catalog.bool'::pg_catalog.regtype AND attribute.attnotnull AND NOT attribute.attisdropped AND attribute.attgenerated OPERATOR(pg_catalog.=) ''::pg_catalog."char")
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_attribute attribute WHERE attribute.attrelid OPERATOR(pg_catalog.=) relation.oid AND attribute.attname OPERATOR(pg_catalog.=) 'id'::pg_catalog.name AND attribute.atttypid OPERATOR(pg_catalog.=) 'pg_catalog.uuid'::pg_catalog.regtype AND attribute.attnotnull AND NOT attribute.attisdropped)
 )`).Scan(&valid)
	if err != nil || !valid {
		return errors.New("workout profile version witness contract mismatch")
	}
	const shape = "CHECK (COALESCE((((kind = 'REPEAT_GROUP'::water_step_kind) AND ((repeats >= 1) AND (repeats <= 100)) AND (duration_seconds IS NULL) AND (distance_metres IS NULL)) OR ((kind = 'EFFORT'::water_step_kind) AND (repeats IS NULL) AND ((duration_seconds IS NOT NULL) OR (distance_metres IS NOT NULL) OR (char_length((instructions)::text) >= 2)))), false))"
	err = q.QueryRow(ctx, `SELECT /* workout graph exact repeat shape */ EXISTS(SELECT 1 FROM pg_catalog.pg_constraint WHERE conrelid OPERATOR(pg_catalog.=) 'public.water_work_steps'::pg_catalog.regclass AND conname OPERATOR(pg_catalog.=) 'water_work_steps_shape_valid'::pg_catalog.name AND convalidated AND pg_catalog.pg_get_constraintdef(oid) OPERATOR(pg_catalog.=) $1::pg_catalog.text)`, shape).Scan(&valid)
	if err != nil || !valid {
		return errors.New("workout repeat shape contract mismatch")
	}
	header, err := trainingScheduleExtractFunction(baselineSchema, "training_block_snapshot_water")
	if err != nil {
		return err
	}
	valid, err = trainingScheduleFunctionMatches(ctx, q, "public.training_block_snapshot_water(uuid)", header)
	if err != nil || !valid {
		return errors.New("workout current snapshot order contract mismatch")
	}
	return verifyTrainingRhythmDefinition(ctx, q)
}
