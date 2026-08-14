CREATE OR REPLACE FUNCTION training_block_snapshot(p_block_id uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
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
   'steps', COALESCE((SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
      'source_id', step.id, 'source_parent_id', step.parent_step_id, 'position', step.position,
      'kind', step.kind::text, 'name', step.name, 'repeats', step.repeats,
      'duration_seconds', step.duration_seconds, 'duration_certainty', step.duration_certainty::text,
      'distance_metres', step.distance_metres, 'distance_certainty', step.distance_certainty::text,
      'recovery_seconds', step.recovery_seconds, 'intensity_code', step.intensity_code,
      'cadence_spm', step.cadence_spm, 'drill_focus', step.drill_focus,
      'drill_format', step.drill_format, 'role_notes', step.role_notes,
      'instructions', step.instructions)) ORDER BY step.created_at, step.position)
    FROM water_work_steps step WHERE step.block_id = water.block_id), '[]'::jsonb)
 ) END
))
FROM training_segment_blocks block
LEFT JOIN gym_block_prescriptions gym ON gym.block_id = block.id
LEFT JOIN water_block_prescriptions water ON water.block_id = block.id
WHERE block.id = p_block_id
$$;

CREATE OR REPLACE FUNCTION restore_training_block(p_snapshot jsonb, p_segment_id uuid) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE
 new_block_id uuid;
 gym jsonb;
 water jsonb;
 exercise jsonb;
 step jsonb;
 new_step_id uuid;
 parent_id uuid;
 step_ids jsonb := '{}'::jsonb;
BEGIN
 gym := p_snapshot->'gym';
 water := p_snapshot->'water';
 IF NOT EXISTS (SELECT 1 FROM training_session_segments segment JOIN training_sessions session ON session.id = segment.session_id WHERE segment.id = p_segment_id AND session.status = 'ACTIVE') THEN RAISE NO_DATA_FOUND; END IF;
 IF gym IS NOT NULL AND jsonb_typeof(gym) = 'object' AND NOT EXISTS (SELECT 1 FROM training_session_segments WHERE id = p_segment_id AND modality = 'GYM') THEN RAISE CHECK_VIOLATION USING MESSAGE = 'structured gym blocks require a gym segment'; END IF;
 IF water IS NOT NULL AND jsonb_typeof(water) = 'object' AND NOT EXISTS (SELECT 1 FROM training_session_segments WHERE id = p_segment_id AND modality = 'WATER') THEN RAISE CHECK_VIOLATION USING MESSAGE = 'structured water blocks require a water segment'; END IF;
 INSERT INTO training_segment_blocks (segment_id, position, purpose, title, instructions)
 SELECT p_segment_id, COALESCE(max(position), 0) + 1, (p_snapshot->>'purpose')::training_block_purpose, COALESCE(p_snapshot->>'title', ''), p_snapshot->>'instructions'
 FROM training_segment_blocks WHERE segment_id = p_segment_id RETURNING id INTO new_block_id;
 IF gym IS NOT NULL AND jsonb_typeof(gym) = 'object' THEN
  INSERT INTO gym_block_prescriptions (block_id, structure, objective, rounds, round_recovery_seconds) VALUES (new_block_id, (gym->>'structure')::gym_block_structure, (gym->>'objective')::training_objective, (gym->>'rounds')::integer, NULLIF(gym->>'round_recovery_seconds', '')::integer);
  FOR exercise IN SELECT value FROM jsonb_array_elements(COALESCE(gym->'exercises', '[]'::jsonb)) LOOP
   INSERT INTO gym_exercises (block_id, position, name, sets, repetitions, duration_seconds, distance_metres, recovery_seconds, resistance_kind, resistance_value, resistance_text, execution_intent, tempo, notes)
   VALUES (new_block_id, (exercise->>'position')::integer, exercise->>'name', NULLIF(exercise->>'sets', '')::integer, NULLIF(exercise->>'repetitions', '')::integer, NULLIF(exercise->>'duration_seconds', '')::integer, NULLIF(exercise->>'distance_metres', '')::integer, NULLIF(exercise->>'recovery_seconds', '')::integer, NULLIF(exercise->>'resistance_kind', '')::gym_resistance_kind, NULLIF(exercise->>'resistance_value', '')::double precision, NULLIF(exercise->>'resistance_text', ''), NULLIF(exercise->>'execution_intent', '')::gym_execution_intent, NULLIF(exercise->>'tempo', ''), COALESCE(exercise->>'notes', ''));
  END LOOP;
 END IF;
 IF water IS NOT NULL AND jsonb_typeof(water) = 'object' THEN
  INSERT INTO water_block_prescriptions (block_id, method, intensity_profile_id, target_distance_metres, target_distance_certainty)
  VALUES (new_block_id, (water->>'method')::water_work_method, NULLIF(water->>'intensity_profile_id', '')::uuid, NULLIF(water->>'target_distance_metres', '')::integer, NULLIF(water->>'target_distance_certainty', '')::training_measure_certainty);
  FOR step IN SELECT value FROM jsonb_array_elements(COALESCE(water->'steps', '[]'::jsonb)) LOOP
   parent_id := NULL;
   IF step ? 'source_parent_id' THEN parent_id := NULLIF(step_ids->>(step->>'source_parent_id'), '')::uuid; END IF;
   IF step ? 'source_parent_id' AND parent_id IS NULL THEN RAISE CHECK_VIOLATION USING MESSAGE = 'water routine contains an invalid parent order'; END IF;
   INSERT INTO water_work_steps (block_id, parent_step_id, position, kind, name, repeats, duration_seconds, duration_certainty, distance_metres, distance_certainty, recovery_seconds, intensity_code, cadence_spm, drill_focus, drill_format, role_notes, instructions)
   VALUES (new_block_id, parent_id, (step->>'position')::integer, (step->>'kind')::water_step_kind, step->>'name', NULLIF(step->>'repeats', '')::integer, NULLIF(step->>'duration_seconds', '')::integer, NULLIF(step->>'duration_certainty', '')::training_measure_certainty, NULLIF(step->>'distance_metres', '')::integer, NULLIF(step->>'distance_certainty', '')::training_measure_certainty, NULLIF(step->>'recovery_seconds', '')::integer, NULLIF(step->>'intensity_code', ''), NULLIF(step->>'cadence_spm', '')::integer, NULLIF(step->>'drill_focus', ''), NULLIF(step->>'drill_format', ''), NULLIF(step->>'role_notes', ''), COALESCE(step->>'instructions', ''))
   RETURNING id INTO new_step_id;
   step_ids := step_ids || jsonb_build_object(step->>'source_id', new_step_id);
  END LOOP;
 END IF;
 RETURN new_block_id;
END
$$;
