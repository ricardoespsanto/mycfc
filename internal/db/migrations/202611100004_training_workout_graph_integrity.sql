-- Fresh graph integrity/current serialization definitions only. No application
-- records, stored routine/publication snapshots or outcomes are rewritten.
ALTER TABLE public.water_work_steps DROP CONSTRAINT water_work_steps_shape_valid;
ALTER TABLE public.water_work_steps ADD CONSTRAINT water_work_steps_shape_valid CHECK(COALESCE(
 (kind='REPEAT_GROUP' AND repeats BETWEEN 1 AND 100 AND duration_seconds IS NULL AND distance_metres IS NULL)
 OR (kind='EFFORT' AND repeats IS NULL AND (duration_seconds IS NOT NULL OR distance_metres IS NOT NULL OR char_length(instructions)>=2)),false));

CREATE OR REPLACE FUNCTION training_block_snapshot_water(p_block_id uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
SELECT jsonb_strip_nulls(jsonb_build_object(
 'purpose', block.purpose::text, 'title', block.title, 'instructions', block.instructions,
 'gym', CASE WHEN gym.block_id IS NULL THEN NULL ELSE jsonb_build_object(
   'structure', gym.structure::text, 'objective', gym.objective::text, 'rounds', gym.rounds,
   'round_recovery_seconds', gym.round_recovery_seconds,
   'exercises', COALESCE((SELECT jsonb_agg(to_jsonb(exercise) - 'id' - 'block_id' - 'created_at' - 'updated_at' ORDER BY exercise.position) FROM gym_exercises exercise WHERE exercise.block_id = gym.block_id), '[]'::jsonb)
 ) END,
 'water', CASE WHEN water.block_id IS NULL THEN NULL ELSE jsonb_build_object(
   'method', water.method::text, 'intensity_profile_id', water.intensity_profile_id,
   'target_distance_metres', water.target_distance_metres,
   'target_distance_certainty', water.target_distance_certainty::text,
   'steps', COALESCE((WITH RECURSIVE tree(id,order_path,id_path) AS (
 SELECT root.id,ARRAY[root.position]::integer[],ARRAY[root.id]::uuid[] FROM water_work_steps root WHERE root.block_id=water.block_id AND root.parent_step_id IS NULL
 UNION ALL
 SELECT child.id,tree.order_path||child.position,tree.id_path||child.id FROM water_work_steps child JOIN tree ON child.parent_step_id=tree.id WHERE child.block_id=water.block_id AND NOT child.id=ANY(tree.id_path)
) SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
      'source_id', step.id, 'source_parent_id', step.parent_step_id, 'position', step.position,
      'kind', step.kind::text, 'name', step.name, 'repeats', step.repeats,
      'duration_seconds', step.duration_seconds, 'duration_certainty', step.duration_certainty::text,
      'distance_metres', step.distance_metres, 'distance_certainty', step.distance_certainty::text,
      'recovery_seconds', step.recovery_seconds, 'intensity_code', step.intensity_code,
      'cadence_spm', step.cadence_spm, 'drill_focus', step.drill_focus,
      'drill_format', step.drill_format, 'role_notes', step.role_notes,
      'instructions', step.instructions)) ORDER BY tree.order_path NULLS LAST,tree.id_path NULLS LAST,step.parent_step_id NULLS FIRST,step.position,step.id)
    FROM water_work_steps step LEFT JOIN tree ON tree.id=step.id WHERE step.block_id = water.block_id), '[]'::jsonb)
 ) END
))
FROM training_segment_blocks block
LEFT JOIN gym_block_prescriptions gym ON gym.block_id = block.id
LEFT JOIN water_block_prescriptions water ON water.block_id = block.id
WHERE block.id = p_block_id
$$;
