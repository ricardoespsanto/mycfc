CREATE TABLE training_plan_publications (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 plan_id uuid NOT NULL REFERENCES training_plans(id) ON DELETE RESTRICT,
 revision integer NOT NULL,
 source_updated_at timestamptz NOT NULL,
 change_summary varchar(500) NOT NULL,
 supersedes_id uuid NULL REFERENCES training_plan_publications(id) ON DELETE RESTRICT,
 published_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 published_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT training_plan_publications_revision_valid CHECK (revision > 0),
 CONSTRAINT training_plan_publications_summary_valid CHECK (change_summary = btrim(change_summary) AND char_length(change_summary) BETWEEN 2 AND 500),
 CONSTRAINT training_plan_publications_revision_unique UNIQUE (plan_id, revision),
 CONSTRAINT training_plan_publications_source_unique UNIQUE (plan_id, source_updated_at)
);
CREATE INDEX training_plan_publications_plan_idx ON training_plan_publications (plan_id, revision DESC);

CREATE TABLE training_prescriptions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 publication_id uuid NOT NULL REFERENCES training_plan_publications(id) ON DELETE RESTRICT,
 session_id uuid NOT NULL REFERENCES training_sessions(id) ON DELETE RESTRICT,
 membership_id uuid NOT NULL REFERENCES user_memberships(id) ON DELETE RESTRICT,
 athlete_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 snapshot jsonb NOT NULL,
 snapshot_sha256 varchar(64) NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT training_prescriptions_snapshot_valid CHECK (jsonb_typeof(snapshot) = 'object' AND octet_length(snapshot::text) <= 200000),
 CONSTRAINT training_prescriptions_hash_valid CHECK (snapshot_sha256 ~ '^[0-9a-f]{64}$'),
 CONSTRAINT training_prescriptions_publication_session_membership_unique UNIQUE (publication_id, session_id, membership_id)
);
CREATE INDEX training_prescriptions_athlete_idx ON training_prescriptions (athlete_user_id, created_at DESC);
CREATE INDEX training_prescriptions_session_idx ON training_prescriptions (session_id, created_at DESC);

ALTER TABLE training_session_outcomes
 ADD COLUMN prescription_id uuid NULL REFERENCES training_prescriptions(id) ON DELETE RESTRICT;
CREATE INDEX training_outcomes_prescription_idx ON training_session_outcomes (prescription_id) WHERE prescription_id IS NOT NULL;

CREATE FUNCTION prevent_training_publication_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'training publications and prescriptions are immutable';
END;
$$;
CREATE TRIGGER training_plan_publications_immutable_trigger BEFORE UPDATE OR DELETE ON training_plan_publications FOR EACH ROW EXECUTE FUNCTION prevent_training_publication_mutation();
CREATE TRIGGER training_prescriptions_immutable_trigger BEFORE UPDATE OR DELETE ON training_prescriptions FOR EACH ROW EXECUTE FUNCTION prevent_training_publication_mutation();

CREATE FUNCTION touch_structured_training_plan() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_id uuid;
BEGIN
 CASE TG_TABLE_NAME
  WHEN 'training_sessions' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.plan_id ELSE NEW.plan_id END; UPDATE training_plans SET updated_at = clock_timestamp() WHERE id = source_id;
  WHEN 'training_session_segments' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.session_id ELSE NEW.session_id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM training_sessions session WHERE session.id = source_id AND plan.id = session.plan_id;
  WHEN 'training_segment_blocks' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.segment_id ELSE NEW.segment_id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM training_session_segments segment JOIN training_sessions session ON session.id = segment.session_id WHERE segment.id = source_id AND plan.id = session.plan_id;
  WHEN 'gym_block_prescriptions', 'water_block_prescriptions' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.block_id ELSE NEW.block_id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id = block.segment_id JOIN training_sessions session ON session.id = segment.session_id WHERE block.id = source_id AND plan.id = session.plan_id;
  WHEN 'gym_exercises', 'water_work_steps' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.block_id ELSE NEW.block_id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id = block.segment_id JOIN training_sessions session ON session.id = segment.session_id WHERE block.id = source_id AND plan.id = session.plan_id;
  WHEN 'training_variations' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.plan_id ELSE NEW.plan_id END; UPDATE training_plans SET updated_at = clock_timestamp() WHERE id = source_id;
  WHEN 'training_group_members' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.group_id ELSE NEW.group_id END; UPDATE training_plans SET updated_at = clock_timestamp() WHERE training_group_id = source_id;
  WHEN 'training_variation_groups' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.training_group_id ELSE NEW.training_group_id END; UPDATE training_plans SET updated_at = clock_timestamp() WHERE training_group_id = source_id;
  WHEN 'training_variation_group_members' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.variation_group_id ELSE NEW.variation_group_id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM training_variation_groups variation_group WHERE variation_group.id = source_id AND plan.training_group_id = variation_group.training_group_id;
  WHEN 'training_groups' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END; UPDATE training_plans SET updated_at = clock_timestamp() WHERE training_group_id = source_id;
  WHEN 'user_memberships' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM training_group_members group_member WHERE group_member.membership_id = source_id AND plan.training_group_id = group_member.group_id;
  WHEN 'users' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM user_memberships membership JOIN training_group_members group_member ON group_member.membership_id = membership.id WHERE membership.user_id = source_id AND plan.training_group_id = group_member.group_id;
  WHEN 'water_intensity_zones' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.profile_id ELSE NEW.profile_id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM water_block_prescriptions water JOIN training_segment_blocks block ON block.id = water.block_id JOIN training_session_segments segment ON segment.id = block.segment_id JOIN training_sessions session ON session.id = segment.session_id WHERE water.intensity_profile_id = source_id AND plan.id = session.plan_id;
 END CASE;
 IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;

CREATE TRIGGER training_sessions_touch_plan AFTER INSERT OR UPDATE OR DELETE ON training_sessions FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
CREATE TRIGGER training_session_segments_touch_plan AFTER INSERT OR UPDATE OR DELETE ON training_session_segments FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
CREATE TRIGGER training_segment_blocks_touch_plan AFTER INSERT OR UPDATE OR DELETE ON training_segment_blocks FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
CREATE TRIGGER gym_block_prescriptions_touch_plan AFTER INSERT OR UPDATE OR DELETE ON gym_block_prescriptions FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
CREATE TRIGGER gym_exercises_touch_plan AFTER INSERT OR UPDATE OR DELETE ON gym_exercises FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
CREATE TRIGGER water_block_prescriptions_touch_plan AFTER INSERT OR UPDATE OR DELETE ON water_block_prescriptions FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
CREATE TRIGGER water_work_steps_touch_plan AFTER INSERT OR UPDATE OR DELETE ON water_work_steps FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
CREATE TRIGGER training_variations_touch_plan AFTER INSERT OR UPDATE OR DELETE ON training_variations FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
CREATE TRIGGER training_group_members_touch_plan AFTER INSERT OR UPDATE OR DELETE ON training_group_members FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
CREATE TRIGGER training_variation_groups_touch_plan AFTER INSERT OR UPDATE OR DELETE ON training_variation_groups FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
CREATE TRIGGER training_variation_group_members_touch_plan AFTER INSERT OR UPDATE OR DELETE ON training_variation_group_members FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
CREATE TRIGGER training_groups_touch_plan AFTER UPDATE OF name, programme_id, team_id ON training_groups FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
CREATE TRIGGER user_memberships_touch_training_plan AFTER UPDATE OF user_id, starts_on, ends_on, programme_id, team_id ON user_memberships FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
CREATE TRIGGER users_touch_training_plan AFTER UPDATE OF is_active ON users FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
CREATE TRIGGER water_intensity_zones_touch_plan AFTER INSERT OR UPDATE OR DELETE ON water_intensity_zones FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
