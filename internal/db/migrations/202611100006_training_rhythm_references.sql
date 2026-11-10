-- Complete shared rhythm reference witnesses. Definitions only: no data seeding,
-- record conversion, range normalization, or stored prescription rewrite.
CREATE FUNCTION public.touch_training_rhythm_profile_plans() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE profile_ids uuid[];plan_ref uuid;plan_count bigint;
BEGIN
 profile_ids:=CASE WHEN TG_OP='DELETE' THEN ARRAY[OLD.id] WHEN TG_OP='INSERT' THEN ARRAY[NEW.id] ELSE ARRAY[OLD.id,NEW.id] END;
 SELECT count(*) INTO plan_count FROM public.training_plans plan WHERE
 EXISTS(SELECT 1 FROM public.training_sessions session JOIN public.training_session_segments segment ON segment.session_id=session.id JOIN public.training_segment_blocks block ON block.segment_id=segment.id JOIN public.water_block_prescriptions water ON water.block_id=block.id WHERE session.plan_id=plan.id AND water.intensity_profile_id=ANY(profile_ids))
 OR EXISTS(SELECT 1 FROM public.training_variations variation WHERE variation.plan_id=plan.id AND variation.is_active AND variation.subject_kind='BLOCK' AND variation.patch->>'intensity_profile_id'=ANY(SELECT id::text FROM unnest(profile_ids) id));
 IF plan_count>4096 THEN RAISE EXCEPTION USING ERRCODE='54000',MESSAGE='training_rhythm_affected_plan_limit'; END IF;
 FOR plan_ref IN SELECT plan.id FROM public.training_plans plan WHERE
 EXISTS(SELECT 1 FROM public.training_sessions session JOIN public.training_session_segments segment ON segment.session_id=session.id JOIN public.training_segment_blocks block ON block.segment_id=segment.id JOIN public.water_block_prescriptions water ON water.block_id=block.id WHERE session.plan_id=plan.id AND water.intensity_profile_id=ANY(profile_ids))
 OR EXISTS(SELECT 1 FROM public.training_variations variation WHERE variation.plan_id=plan.id AND variation.is_active AND variation.subject_kind='BLOCK' AND variation.patch->>'intensity_profile_id'=ANY(SELECT id::text FROM unnest(profile_ids) id)) ORDER BY plan.id LOOP
 UPDATE public.training_plans SET updated_at=clock_timestamp() WHERE id=plan_ref;
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;RETURN NEW;
END $$;
CREATE TRIGGER water_intensity_profiles_rhythm_plan_witness AFTER INSERT OR UPDATE OR DELETE ON public.water_intensity_profiles FOR EACH ROW EXECUTE FUNCTION public.touch_training_rhythm_profile_plans();

CREATE FUNCTION public.lock_training_rhythm_zone_parent() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE profile_ids uuid[];profile_ref uuid;
BEGIN
 profile_ids:=CASE WHEN TG_OP='DELETE' THEN ARRAY[OLD.profile_id] WHEN TG_OP='INSERT' THEN ARRAY[NEW.profile_id] ELSE ARRAY[OLD.profile_id,NEW.profile_id] END;
 FOR profile_ref IN SELECT DISTINCT id FROM unnest(profile_ids) id ORDER BY id LOOP
 -- Actual UPDATE protects first zone INSERT as well as edits/deletes. The
 -- profile AFTER witness physically touches all base and override plans.
 UPDATE ONLY public.water_intensity_profiles SET is_active=is_active WHERE id=profile_ref;
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;RETURN NEW;
END $$;
CREATE TRIGGER water_intensity_zones_rhythm_parent_witness BEFORE INSERT OR UPDATE OR DELETE ON public.water_intensity_zones FOR EACH ROW EXECUTE FUNCTION public.lock_training_rhythm_zone_parent();

CREATE FUNCTION public.guard_training_rhythm_profile_delete() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM public.training_variations variation WHERE variation.is_active AND variation.subject_kind='BLOCK' AND variation.patch->>'intensity_profile_id'=OLD.id::text) THEN
 RAISE EXCEPTION USING ERRCODE='23503',MESSAGE='training_rhythm_profile_referenced'; END IF;
 RETURN OLD;
END $$;
CREATE TRIGGER water_intensity_profiles_rhythm_delete_guard BEFORE DELETE ON public.water_intensity_profiles FOR EACH ROW EXECUTE FUNCTION public.guard_training_rhythm_profile_delete();

CREATE FUNCTION public.guard_training_rhythm_variation() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
DECLARE required integer;profile_ref uuid;
BEGIN
 IF NOT NEW.is_active OR NEW.subject_kind<>'BLOCK' OR NOT(NEW.patch?'intensity_profile_id') THEN RETURN NEW;END IF;
 SELECT state.required_reader INTO required FROM training_schedule_control.status() state;
 IF required<5 THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='training_rhythm_reader_floor_required'; END IF;
 IF NEW.operation<>'OVERRIDE' OR jsonb_typeof(NEW.patch->'intensity_profile_id')<>'string'
 OR (NEW.patch->>'intensity_profile_id')!~'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN
 RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_rhythm_profile_patch_invalid'; END IF;
 profile_ref:=(NEW.patch->>'intensity_profile_id')::uuid;
 PERFORM 1 FROM public.water_block_prescriptions water JOIN public.training_segment_blocks block ON block.id=water.block_id JOIN public.training_session_segments segment ON segment.id=block.segment_id JOIN public.training_sessions session ON session.id=segment.session_id WHERE water.block_id=NEW.subject_id AND session.plan_id=NEW.plan_id;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_rhythm_water_block_required'; END IF;
 PERFORM 1 FROM public.water_intensity_profiles profile WHERE profile.id=profile_ref AND profile.is_active FOR SHARE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_rhythm_active_profile_required'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER training_variations_rhythm_profile_guard BEFORE INSERT OR UPDATE ON public.training_variations FOR EACH ROW EXECUTE FUNCTION public.guard_training_rhythm_variation();

CREATE FUNCTION public.touch_training_rhythm_sporting_permissions() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE subjects uuid[];subject_ref uuid;programmes uuid[];plan_ref uuid;plan_count bigint;
BEGIN
 subjects:=CASE WHEN TG_OP='DELETE' THEN ARRAY[OLD.user_id] WHEN TG_OP='INSERT' THEN ARRAY[NEW.user_id] ELSE ARRAY[OLD.user_id,NEW.user_id] END;
 FOR subject_ref IN SELECT DISTINCT id FROM unnest(subjects) id ORDER BY id LOOP
 -- Supported sporting correction callers already take this lock first.
 -- It also fences an absent permission against the publisher's late user lock.
 PERFORM 1 FROM public.users WHERE id=subject_ref FOR UPDATE NOWAIT;
 SELECT array_agg(head.programme_id ORDER BY head.programme_id) INTO programmes FROM public.training_placement_heads head WHERE head.subject_user_id=subject_ref;
 PERFORM public.training_placement_touch_heads(subject_ref,programmes);
 SELECT count(DISTINCT plan.id) INTO plan_count FROM public.training_plans plan JOIN public.training_group_placements placement ON placement.group_id=plan.training_group_id WHERE placement.subject_user_id=subject_ref;
 IF plan_count>4096 THEN RAISE EXCEPTION USING ERRCODE='54000',MESSAGE='training_rhythm_affected_plan_limit'; END IF;
 FOR plan_ref IN SELECT DISTINCT plan.id FROM public.training_plans plan JOIN public.training_group_placements placement ON placement.group_id=plan.training_group_id WHERE placement.subject_user_id=subject_ref ORDER BY plan.id LOOP
 UPDATE public.training_plans SET updated_at=clock_timestamp() WHERE id=plan_ref;
 END LOOP;
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD;END IF;RETURN NEW;
END $$;
CREATE TRIGGER person_canoe_craft_classes_rhythm_witness BEFORE INSERT OR UPDATE OR DELETE ON public.person_canoe_craft_classes FOR EACH ROW EXECUTE FUNCTION public.touch_training_rhythm_sporting_permissions();
CREATE TRIGGER person_sporting_modalities_rhythm_witness BEFORE INSERT OR UPDATE OR DELETE ON public.person_sporting_modalities FOR EACH ROW EXECUTE FUNCTION public.touch_training_rhythm_sporting_permissions();
REVOKE ALL ON FUNCTION public.touch_training_rhythm_profile_plans(),public.lock_training_rhythm_zone_parent(),public.guard_training_rhythm_profile_delete(),public.guard_training_rhythm_variation(),public.touch_training_rhythm_sporting_permissions() FROM PUBLIC;
