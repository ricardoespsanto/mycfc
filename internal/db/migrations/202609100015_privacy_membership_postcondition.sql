-- #247 closure-v4: authenticate the complete retained membership-history
-- postcondition without retaining a subject or pseudonymous-principal link.
-- Existing v1-v3 ledger material remains readable but cannot authorize a new
-- replay or activation after this migration.

ALTER TABLE privacy_protected.restore_tombstone_closure_receipts
 DROP CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check;
ALTER TABLE privacy_protected.restore_tombstone_closure_receipts
 ADD CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check
 CHECK(ledger_version IN ('restore-tombstone-closure/v1','restore-tombstone-closure/v2','restore-tombstone-closure/v3','restore-tombstone-closure/v4')) NOT VALID;
ALTER TABLE privacy_protected.restore_tombstone_closure_receipts
 VALIDATE CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check;

ALTER TABLE privacy_protected.restore_ledger_imports
 DROP CONSTRAINT restore_ledger_imports_closure_version_check;
ALTER TABLE privacy_protected.restore_ledger_imports
 ADD COLUMN membership_postcondition_contract varchar(64) NULL,
 ADD COLUMN membership_postcondition_sha256 bytea NULL,
 ADD COLUMN membership_count bigint NULL,
 ADD COLUMN variation_count bigint NULL,
 ADD CONSTRAINT restore_ledger_imports_closure_version_check CHECK(
  (kind='intent' AND closure_version IS NULL)
  OR (kind='closure' AND closure_version IN ('restore-tombstone-closure/v2','restore-tombstone-closure/v3','restore-tombstone-closure/v4'))
 ) NOT VALID,
 ADD CONSTRAINT restore_ledger_imports_membership_postcondition_check CHECK((
  (closure_version='restore-tombstone-closure/v4'
   AND membership_postcondition_contract='mycfc/membership-history-postcondition/v1'
   AND octet_length(membership_postcondition_sha256)=32
   AND membership_count BETWEEN 0 AND 10000 AND variation_count BETWEEN 0 AND 100000)
 OR (closure_version IS DISTINCT FROM 'restore-tombstone-closure/v4'
   AND membership_postcondition_contract IS NULL AND membership_postcondition_sha256 IS NULL
   AND membership_count IS NULL AND variation_count IS NULL)
 ) IS TRUE) NOT VALID;
ALTER TABLE privacy_protected.restore_ledger_imports
 VALIDATE CONSTRAINT restore_ledger_imports_closure_version_check;
ALTER TABLE privacy_protected.restore_ledger_imports
 VALIDATE CONSTRAINT restore_ledger_imports_membership_postcondition_check;

CREATE TABLE privacy_protected.membership_history_source_captures (
 execution_id uuid PRIMARY KEY REFERENCES public.privacy_erasure_executions(id) ON DELETE RESTRICT,
 captured_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE privacy_protected.membership_history_source_rows (
 execution_id uuid NOT NULL REFERENCES privacy_protected.membership_history_source_captures(execution_id) ON DELETE RESTRICT,
 membership_id uuid NOT NULL,
 PRIMARY KEY(execution_id,membership_id)
);
CREATE TABLE privacy_protected.membership_history_source_postconditions (
 execution_id uuid PRIMARY KEY REFERENCES privacy_protected.membership_history_source_captures(execution_id) ON DELETE RESTRICT,
 effective_at timestamptz NOT NULL,
 contract varchar(64) NOT NULL CHECK(contract='mycfc/membership-history-postcondition/v1'),
 postcondition_sha256 bytea NOT NULL CHECK(octet_length(postcondition_sha256)=32),
 membership_count bigint NOT NULL CHECK(membership_count BETWEEN 0 AND 10000),
 variation_count bigint NOT NULL CHECK(variation_count BETWEEN 0 AND 100000),
 canonical_size bigint NOT NULL CHECK(canonical_size BETWEEN 1 AND 16777216),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE privacy_protected.membership_history_replay_captures (
 run_id uuid PRIMARY KEY REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 captured_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE privacy_protected.membership_history_replay_rows (
 run_id uuid NOT NULL REFERENCES privacy_protected.membership_history_replay_captures(run_id) ON DELETE RESTRICT,
 membership_id uuid NOT NULL,
 PRIMARY KEY(run_id,membership_id)
);
CREATE TABLE privacy_protected.membership_history_replay_postconditions (
 run_id uuid PRIMARY KEY REFERENCES privacy_protected.membership_history_replay_captures(run_id) ON DELETE RESTRICT,
 contract varchar(64) NOT NULL CHECK(contract='mycfc/membership-history-postcondition/v1'),
 postcondition_sha256 bytea NOT NULL CHECK(octet_length(postcondition_sha256)=32),
 membership_count bigint NOT NULL CHECK(membership_count BETWEEN 0 AND 10000),
 variation_count bigint NOT NULL CHECK(variation_count BETWEEN 0 AND 100000),
 canonical_size bigint NOT NULL CHECK(canonical_size BETWEEN 1 AND 16777216),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TRIGGER privacy_membership_history_source_captures_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.membership_history_source_captures FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_membership_history_source_rows_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.membership_history_source_rows FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_membership_history_source_postconditions_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.membership_history_source_postconditions FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_membership_history_replay_captures_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.membership_history_replay_captures FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_membership_history_replay_rows_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.membership_history_replay_rows FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_membership_history_replay_postconditions_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.membership_history_replay_postconditions FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

-- Every value is uint32-big-endian length-prefixed UTF-8. NULL uses the
-- reserved 0xffffffff length, so NULL and the empty string never collide.
CREATE FUNCTION public.privacy_membership_history_frame(p_value text)
RETURNS bytea LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT CASE WHEN p_value IS NULL THEN decode('ffffffff','hex')
  ELSE int4send(octet_length(convert_to(p_value,'UTF8')))||convert_to(p_value,'UTF8') END;
$$;
CREATE FUNCTION public.privacy_membership_history_frame(p_value bytea)
RETURNS bytea LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT CASE WHEN p_value IS NULL THEN decode('ffffffff','hex')
  ELSE int4send(octet_length(p_value))||p_value END;
$$;

-- Both arguments are contractually 32 bytes. The loop always examines all
-- 32 positions and is the database-side companion to Go's constant-time
-- comparison before a replay receipt is returned.
CREATE FUNCTION public.privacy_membership_history_digest_equal(p_left bytea,p_right bytea)
RETURNS boolean LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE difference integer:=0;position integer;
BEGIN
 IF p_left IS NULL OR p_right IS NULL OR octet_length(p_left)<>32 OR octet_length(p_right)<>32 THEN RETURN false; END IF;
 FOR position IN 0..31 LOOP
  difference:=difference | (get_byte(p_left,position) # get_byte(p_right,position));
 END LOOP;
 RETURN difference=0;
END;$$;

CREATE FUNCTION public.privacy_membership_history_compute(
 p_membership_ids uuid[],p_effective_at timestamptz,p_require_anonymized boolean
) RETURNS TABLE(contract text,postcondition_sha256 bytea,membership_count bigint,variation_count bigint,canonical_size bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE canonical bytea;membership_row record;child record;ids uuid[];actual_count bigint;principal_count bigint;principal_ref uuid;
 membership_total bigint;variation_total bigint;modality_total bigint;group_total bigint;variation_group_total bigint;
BEGIN
 ids:=COALESCE((SELECT array_agg(item.id ORDER BY item.id) FROM unnest(COALESCE(p_membership_ids,ARRAY[]::uuid[])) item(id)),ARRAY[]::uuid[]);
 membership_total:=cardinality(ids);
 IF p_effective_at IS NULL OR membership_total>10000 OR membership_total<>(SELECT count(DISTINCT id) FROM unnest(ids) item(id)) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
 SELECT count(*),count(DISTINCT membership.principal_id),min(membership.principal_id::text)::uuid
 INTO actual_count,principal_count,principal_ref FROM user_memberships membership WHERE membership.id=ANY(ids);
 IF actual_count<>membership_total OR EXISTS(SELECT 1 FROM user_memberships membership WHERE membership.id=ANY(ids)
   AND (membership.ends_on IS NULL OR membership.ends_on>=(p_effective_at AT TIME ZONE 'UTC')::date))
  OR (p_require_anonymized AND membership_total>0 AND (principal_count<>1 OR EXISTS(
    SELECT 1 FROM user_memberships membership WHERE membership.id=ANY(ids) AND (membership.user_id IS NOT NULL OR membership.principal_id IS NULL))))
  OR (p_require_anonymized AND membership_total>0 AND EXISTS(
    SELECT 1 FROM user_memberships membership WHERE membership.principal_id=principal_ref AND NOT(membership.id=ANY(ids)))) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
 SELECT count(*) INTO variation_total FROM training_variations variation WHERE variation.target_membership_id=ANY(ids);
 IF variation_total>100000 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
 canonical:=privacy_membership_history_frame('mycfc/membership-history-postcondition/v1')||
  privacy_membership_history_frame((p_effective_at AT TIME ZONE 'UTC')::date::text)||
  privacy_membership_history_frame(membership_total::text);
 FOR membership_row IN SELECT membership.* FROM user_memberships membership WHERE membership.id=ANY(ids) ORDER BY membership.id LOOP
  SELECT count(*) INTO modality_total FROM membership_modalities modality WHERE modality.membership_id=membership_row.id;
  SELECT count(*) INTO group_total FROM training_group_members linked WHERE linked.membership_id=membership_row.id;
  SELECT count(*) INTO variation_group_total FROM training_variation_group_members linked WHERE linked.membership_id=membership_row.id;
  canonical:=canonical||privacy_membership_history_frame('membership')||privacy_membership_history_frame(membership_row.id::text)||
   privacy_membership_history_frame(membership_row.season_id::text)||privacy_membership_history_frame(membership_row.programme_id::text)||
   privacy_membership_history_frame(membership_row.team_id::text)||privacy_membership_history_frame(membership_row.competition_category_id::text)||
   privacy_membership_history_frame(membership_row.starts_on::text)||privacy_membership_history_frame(membership_row.ends_on::text)||
   privacy_membership_history_frame('HISTORICAL')||privacy_membership_history_frame(modality_total::text);
  FOR child IN SELECT modality.modality_id FROM membership_modalities modality WHERE modality.membership_id=membership_row.id ORDER BY modality.modality_id LOOP
   canonical:=canonical||privacy_membership_history_frame('modality')||privacy_membership_history_frame(child.modality_id::text);
   IF octet_length(canonical)>16777216 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
  END LOOP;
  canonical:=canonical||privacy_membership_history_frame(group_total::text);
  FOR child IN SELECT linked.group_id FROM training_group_members linked WHERE linked.membership_id=membership_row.id ORDER BY linked.group_id LOOP
   canonical:=canonical||privacy_membership_history_frame('training-group')||privacy_membership_history_frame(child.group_id::text);
   IF octet_length(canonical)>16777216 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
  END LOOP;
  canonical:=canonical||privacy_membership_history_frame(variation_group_total::text);
  FOR child IN SELECT linked.variation_group_id FROM training_variation_group_members linked WHERE linked.membership_id=membership_row.id ORDER BY linked.variation_group_id LOOP
   canonical:=canonical||privacy_membership_history_frame('variation-group')||privacy_membership_history_frame(child.variation_group_id::text);
   IF octet_length(canonical)>16777216 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
  END LOOP;
  SELECT count(*) INTO actual_count FROM training_variations variation WHERE variation.target_membership_id=membership_row.id;
  canonical:=canonical||privacy_membership_history_frame(actual_count::text);
  FOR child IN SELECT variation.* FROM training_variations variation WHERE variation.target_membership_id=membership_row.id ORDER BY variation.id LOOP
   canonical:=canonical||privacy_membership_history_frame('variation')||privacy_membership_history_frame(child.id::text)||
    privacy_membership_history_frame(child.plan_id::text)||privacy_membership_history_frame(child.subject_kind::text)||
    privacy_membership_history_frame(child.subject_id::text)||privacy_membership_history_frame(child.operation::text)||
    privacy_membership_history_frame(child.change_summary::text)||privacy_membership_history_frame(child.patch::text)||
    privacy_membership_history_frame(child.version::text)||privacy_membership_history_frame(CASE WHEN child.is_active THEN 'true' ELSE 'false' END)||
    privacy_membership_history_frame(CASE WHEN child.retired_at IS NULL THEN NULL ELSE to_char(child.retired_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END);
   IF octet_length(canonical)>16777216 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
  END LOOP;
  IF octet_length(canonical)>16777216 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
 END LOOP;
 RETURN QUERY SELECT 'mycfc/membership-history-postcondition/v1'::text,digest(canonical,'sha256'),membership_total,variation_total,octet_length(canonical)::bigint;
END;$$;

CREATE FUNCTION public.privacy_membership_history_compute_source(p_execution_id uuid,p_effective_at timestamptz)
RETURNS TABLE(contract text,postcondition_sha256 bytea,membership_count bigint,variation_count bigint,canonical_size bigint)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT computed.* FROM privacy_protected.membership_history_source_captures capture
 CROSS JOIN LATERAL public.privacy_membership_history_compute(
  COALESCE((SELECT array_agg(row.membership_id ORDER BY row.membership_id) FROM privacy_protected.membership_history_source_rows row WHERE row.execution_id=capture.execution_id),ARRAY[]::uuid[]),
  p_effective_at,true) computed WHERE capture.execution_id=p_execution_id;
$$;
CREATE FUNCTION public.privacy_membership_history_compute_replay(p_run_id uuid,p_effective_at timestamptz)
RETURNS TABLE(contract text,postcondition_sha256 bytea,membership_count bigint,variation_count bigint,canonical_size bigint)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT computed.* FROM privacy_protected.membership_history_replay_captures capture
 CROSS JOIN LATERAL public.privacy_membership_history_compute(
  COALESCE((SELECT array_agg(row.membership_id ORDER BY row.membership_id) FROM privacy_protected.membership_history_replay_rows row WHERE row.run_id=capture.run_id),ARRAY[]::uuid[]),
  p_effective_at,true) computed WHERE capture.run_id=p_run_id;
$$;

-- Membership date boundaries are tied to the persisted erasure instant (or
-- execution start before identity clearing), never to a retry's wall clock.
CREATE FUNCTION public.privacy_membership_history_effective_date(p_execution_id uuid,p_subject_user_id uuid)
RETURNS date LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT (CASE WHEN subject.erasure_execution_id=execution.id AND subject.erased_at IS NOT NULL
              THEN subject.erased_at ELSE execution.started_at END AT TIME ZONE 'UTC')::date
 FROM privacy_erasure_executions execution JOIN users subject ON subject.id=p_subject_user_id
 WHERE execution.id=p_execution_id;
$$;

-- Upgrade the pre-v4 executor in place. Assert the exact number of legacy or
-- upgraded cutoff references so an unexpected predecessor cannot migrate.
DO $$DECLARE definition text;legacy_count integer;current_count integer;
 current_call text:='public.privacy_membership_history_effective_date(execution_ref,subject_ref)';
BEGIN
 SELECT pg_get_functiondef('public.privacy_worker_execute_checkpoint_without_tombstone_guard(uuid,uuid,uuid,bigint,uuid,text,text)'::regprocedure) INTO definition;
 legacy_count:=(length(definition)-length(replace(definition,'CURRENT_DATE','')))/length('CURRENT_DATE');
 current_count:=(length(definition)-length(replace(definition,current_call,'')))/length(current_call);
 IF legacy_count=12 AND current_count=0 THEN EXECUTE replace(definition,'CURRENT_DATE',current_call);
 ELSIF legacy_count<>0 OR current_count<>12 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_effective_date_predecessor_mismatch';
 END IF;
END$$;

-- Serialize sealing and every digest-covered mutation by membership ID.
CREATE FUNCTION public.privacy_membership_history_lock_source(p_execution_id uuid)
RETURNS void LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE membership_ref uuid;
BEGIN
 FOR membership_ref IN
  SELECT DISTINCT source_row.membership_id FROM privacy_protected.membership_history_source_rows source_row
  WHERE source_row.execution_id=p_execution_id ORDER BY source_row.membership_id
 LOOP
  PERFORM pg_advisory_xact_lock(hashtextextended('mycfc:privacy-membership-history:'||membership_ref::text,0));
 END LOOP;
END;$$;

CREATE FUNCTION public.privacy_membership_history_source_postcondition_ready(p_execution_id uuid)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 PERFORM public.privacy_membership_history_lock_source(p_execution_id);
 RETURN EXISTS(
  SELECT 1
  FROM privacy_protected.membership_history_source_postconditions postcondition
  CROSS JOIN LATERAL public.privacy_membership_history_compute_source(postcondition.execution_id,postcondition.effective_at) computed
  WHERE postcondition.execution_id=p_execution_id
   AND postcondition.contract='mycfc/membership-history-postcondition/v1'
   AND computed.contract=postcondition.contract
   AND public.privacy_membership_history_digest_equal(computed.postcondition_sha256,postcondition.postcondition_sha256)
   AND computed.membership_count=postcondition.membership_count
   AND computed.variation_count=postcondition.variation_count
 );
END;
$$;

-- Once closure preparation seals a source postcondition, every digest-covered
-- membership row and relationship becomes immutable. Erasure itself runs
-- before that postcondition exists, so its required anonymisation is not
-- impeded.
CREATE FUNCTION public.prevent_sealed_membership_history_mutation()
RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE old_membership uuid;new_membership uuid;column_name text;
BEGIN
 column_name:=CASE TG_TABLE_NAME WHEN 'user_memberships' THEN 'id' WHEN 'training_variations' THEN 'target_membership_id' ELSE 'membership_id' END;
 IF TG_OP<>'INSERT' THEN old_membership:=NULLIF(to_jsonb(OLD)->>column_name,'')::uuid; END IF;
 IF TG_OP<>'DELETE' THEN new_membership:=NULLIF(to_jsonb(NEW)->>column_name,'')::uuid; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc:privacy-membership-history:'||locked.membership_ref::text,0))
 FROM (SELECT DISTINCT membership_ref FROM unnest(ARRAY[old_membership,new_membership]) membership_ref
       WHERE membership_ref IS NOT NULL ORDER BY membership_ref) locked;
 IF EXISTS(
  SELECT 1 FROM privacy_protected.membership_history_source_rows source_row
  JOIN privacy_protected.membership_history_source_postconditions postcondition ON postcondition.execution_id=source_row.execution_id
  WHERE source_row.membership_id=old_membership OR source_row.membership_id=new_membership
 ) THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='sealed_membership_history_immutable'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER user_memberships_sealed_history BEFORE INSERT OR UPDATE OR DELETE ON user_memberships
 FOR EACH ROW EXECUTE FUNCTION public.prevent_sealed_membership_history_mutation();
CREATE TRIGGER membership_modalities_sealed_history BEFORE INSERT OR UPDATE OR DELETE ON membership_modalities
 FOR EACH ROW EXECUTE FUNCTION public.prevent_sealed_membership_history_mutation();
CREATE TRIGGER training_group_members_sealed_history BEFORE INSERT OR UPDATE OR DELETE ON training_group_members
 FOR EACH ROW EXECUTE FUNCTION public.prevent_sealed_membership_history_mutation();
CREATE TRIGGER training_variation_group_members_sealed_history BEFORE INSERT OR UPDATE OR DELETE ON training_variation_group_members
 FOR EACH ROW EXECUTE FUNCTION public.prevent_sealed_membership_history_mutation();
CREATE TRIGGER training_variations_sealed_history BEFORE INSERT OR UPDATE OR DELETE ON training_variations
 FOR EACH ROW EXECUTE FUNCTION public.prevent_sealed_membership_history_mutation();

-- Capture the final retained membership row set immediately before the
-- history operation. Identity clearing runs earlier, so its wrapper scrubs
-- variation canaries while the original name/email/login still exist.
ALTER FUNCTION public.privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text)
 RENAME TO privacy_worker_execute_checkpoint_inner_015;
CREATE FUNCTION public.privacy_worker_execute_checkpoint(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_operation_code text,p_action_version text
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE execution_ref uuid;subject_ref uuid;checkpoint_status text;subject_name text;subject_email text;subject_login text;capture_inserted bigint;
 previous_timezone text;checkpoint_ref uuid;
BEGIN
 IF p_operation_code='MEMBERSHIP_ACTIVE_REVOKE' THEN
  previous_timezone:=current_setting('TimeZone');
  PERFORM set_config('TimeZone','UTC',true);
 END IF;
 PERFORM public.privacy_worker_require_activation();
 SELECT execution.id,request.subject_user_id,checkpoint.status INTO execution_ref,subject_ref,checkpoint_status
 FROM privacy_erasure_category_jobs job
 JOIN privacy_erasure_job_leases lease ON lease.id=p_lease_id AND lease.job_id=job.id AND lease.epoch=p_lease_epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.id=p_attempt_id AND attempt.job_id=job.id AND attempt.lease_id=lease.id AND attempt.lease_epoch=p_lease_epoch
 JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id AND checkpoint.operation_code=p_operation_code AND checkpoint.action_version=p_action_version
 JOIN privacy_erasure_executions execution ON execution.id=job.execution_id
 JOIN data_erasure_requests request ON request.id=execution.request_id
 WHERE job.id=p_job_id AND job.status='LEASED' AND lease.worker_ref=p_worker_ref AND lease.released_at IS NULL
  AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL
  AND NOT EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints prior WHERE prior.job_id=job.id AND prior.operation_position<checkpoint.operation_position AND prior.status<>'SUCCEEDED')
  AND NOT EXISTS(SELECT 1 FROM privacy_erasure_category_jobs prior WHERE prior.execution_id=execution.id AND prior.plan_entry_position<job.plan_entry_position AND prior.status<>'SUCCEEDED')
 FOR UPDATE OF job,lease,attempt,checkpoint;
 IF execution_ref IS NULL OR subject_ref IS NULL THEN
  checkpoint_ref:=public.privacy_worker_execute_checkpoint_inner_015(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version);
  IF previous_timezone IS NOT NULL THEN PERFORM set_config('TimeZone',previous_timezone,true); END IF;
  RETURN checkpoint_ref;
 END IF;
 IF checkpoint_status='PENDING' AND p_operation_code='IDENTITY_CLEAR' AND EXISTS(
  SELECT 1 FROM privacy_erasure_category_jobs membership_job JOIN privacy_erasure_job_checkpoints membership_checkpoint ON membership_checkpoint.job_id=membership_job.id
  WHERE membership_job.execution_id=execution_ref AND membership_checkpoint.operation_code='MEMBERSHIP_HISTORY_ANONYMIZE') THEN
  SELECT name,email::text,minor_login_id::text INTO subject_name,subject_email,subject_login FROM users WHERE id=subject_ref FOR UPDATE;
  PERFORM set_config('mycfc.privacy_erasure_operation','MEMBERSHIP_HISTORY_ANONYMIZE',true);
  UPDATE training_variations SET
   change_summary=privacy_scrub_audit_text(change_summary,subject_ref,subject_name,subject_email,subject_login),
   patch=privacy_scrub_audit_json(patch,subject_ref,subject_name,subject_email,subject_login),updated_at=clock_timestamp()
  WHERE target_membership_id IN(SELECT id FROM user_memberships WHERE user_id=subject_ref);
 END IF;
 IF checkpoint_status='PENDING' AND p_operation_code='MEMBERSHIP_HISTORY_ANONYMIZE' THEN
  INSERT INTO privacy_protected.membership_history_source_captures(execution_id) VALUES(execution_ref) ON CONFLICT DO NOTHING;
  GET DIAGNOSTICS capture_inserted=ROW_COUNT;
  IF capture_inserted<>1 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_capture_conflict'; END IF;
  INSERT INTO privacy_protected.membership_history_source_rows(execution_id,membership_id)
   SELECT execution_ref,membership.id FROM user_memberships membership WHERE membership.user_id=subject_ref ORDER BY membership.id;
 END IF;
 checkpoint_ref:=public.privacy_worker_execute_checkpoint_inner_015(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version);
 IF previous_timezone IS NOT NULL THEN PERFORM set_config('TimeZone',previous_timezone,true); END IF;
 RETURN checkpoint_ref;
END;$$;

CREATE FUNCTION public.privacy_tombstone_prepare_closure_v4(p_execution_id uuid,p_worker_ref uuid)
RETURNS TABLE(execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,
 execution_started_at timestamptz,closed_at timestamptz,evidence_expires_at timestamptz,erasure_effective_at timestamptz,replay_operations text[],
 membership_postcondition_contract text,membership_postcondition_sha256 bytea,membership_count bigint,variation_count bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE prepared record;computed record;existing privacy_protected.membership_history_source_postconditions%ROWTYPE;
BEGIN
 PERFORM public.privacy_worker_require_activation();
 SELECT * INTO prepared FROM public.privacy_tombstone_prepare_closure_v3(p_execution_id,p_worker_ref);
 IF prepared.execution_id IS NULL OR EXISTS(SELECT 1 FROM privacy_protected.restore_tombstone_closure_receipts receipt
   WHERE receipt.execution_id=p_execution_id AND receipt.ledger_version<>'restore-tombstone-closure/v4') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_unavailable'; END IF;
 IF 'MEMBERSHIP_HISTORY_ANONYMIZE'=ANY(prepared.replay_operations) THEN
  IF NOT EXISTS(SELECT 1 FROM privacy_protected.membership_history_source_captures capture WHERE capture.execution_id=p_execution_id) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_unavailable'; END IF;
 ELSE INSERT INTO privacy_protected.membership_history_source_captures(execution_id) VALUES(p_execution_id) ON CONFLICT DO NOTHING;
 END IF;
 PERFORM public.privacy_membership_history_lock_source(p_execution_id);
 SELECT * INTO computed FROM public.privacy_membership_history_compute_source(p_execution_id,prepared.erasure_effective_at);
 IF computed.contract IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_unavailable'; END IF;
 SELECT * INTO existing FROM privacy_protected.membership_history_source_postconditions WHERE membership_history_source_postconditions.execution_id=p_execution_id;
 IF existing.execution_id IS NULL THEN
  INSERT INTO privacy_protected.membership_history_source_postconditions(execution_id,effective_at,contract,postcondition_sha256,membership_count,variation_count,canonical_size)
  VALUES(p_execution_id,prepared.erasure_effective_at,computed.contract,computed.postcondition_sha256,computed.membership_count,computed.variation_count,computed.canonical_size);
 ELSIF existing.effective_at IS DISTINCT FROM prepared.erasure_effective_at OR existing.contract<>computed.contract
  OR NOT public.privacy_membership_history_digest_equal(existing.postcondition_sha256,computed.postcondition_sha256)
  OR existing.membership_count<>computed.membership_count OR existing.variation_count<>computed.variation_count OR existing.canonical_size<>computed.canonical_size THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_conflict'; END IF;
 RETURN QUERY SELECT prepared.execution_id,prepared.request_id,prepared.request_ref,prepared.subject_user_id,prepared.plan_sha256,prepared.workset_sha256,
  prepared.execution_started_at,prepared.closed_at,prepared.evidence_expires_at,prepared.erasure_effective_at,prepared.replay_operations,
  computed.contract,computed.postcondition_sha256,computed.membership_count,computed.variation_count;
END;$$;

CREATE FUNCTION public.privacy_tombstone_confirm_closure_v4(
 p_execution_id uuid,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,
 p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE intent privacy_protected.restore_tombstone_closure_intents%ROWTYPE;existing privacy_protected.restore_tombstone_closure_receipts%ROWTYPE;
BEGIN
 PERFORM public.privacy_worker_require_activation();
 SELECT * INTO intent FROM privacy_protected.restore_tombstone_closure_intents WHERE execution_id=p_execution_id;
 IF intent.execution_id IS NULL OR p_worker_ref IS NULL OR p_ledger_version<>'restore-tombstone-closure/v4'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR p_object_version_id IS NULL
  OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_size_bytes NOT BETWEEN 1 AND 1048576 OR p_written_at IS NULL OR p_verified_at<p_written_at OR p_verified_at>intent.evidence_expires_at
  OR NOT public.privacy_membership_history_source_postcondition_ready(p_execution_id)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_tombstone_closure_receipts WHERE execution_id=p_execution_id;
 IF existing.execution_id IS NULL THEN
  INSERT INTO privacy_protected.restore_tombstone_closure_receipts(execution_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
  VALUES(p_execution_id,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at);
 ELSIF ROW(existing.ledger_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,existing.object_version_id,existing.ciphertext_sha256,existing.size_bytes,existing.written_at,existing.verified_at)
  IS DISTINCT FROM ROW(p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_conflict'; END IF;
 RETURN p_execution_id;
END;$$;

ALTER TABLE privacy_protected.restore_synthetic_fixtures
 ADD COLUMN membership_postcondition_contract varchar(64) NULL,
 ADD COLUMN membership_postcondition_sha256 bytea NULL,
 ADD COLUMN membership_count bigint NULL,
 ADD COLUMN variation_count bigint NULL,
 ADD CONSTRAINT restore_synthetic_fixtures_membership_postcondition_check CHECK((
  (membership_postcondition_contract IS NULL AND membership_postcondition_sha256 IS NULL AND membership_count IS NULL AND variation_count IS NULL)
  OR (membership_postcondition_contract='mycfc/membership-history-postcondition/v1'
   AND octet_length(membership_postcondition_sha256)=32
   AND membership_count BETWEEN 0 AND 10000 AND variation_count BETWEEN 0 AND 100000)) IS TRUE) NOT VALID;
ALTER TABLE privacy_protected.restore_synthetic_fixtures VALIDATE CONSTRAINT restore_synthetic_fixtures_membership_postcondition_check;

DROP FUNCTION public.privacy_restore_create_synthetic_fixture(uuid);
CREATE FUNCTION public.privacy_restore_create_synthetic_fixture(p_worker_ref uuid)
RETURNS TABLE(source_execution_id uuid,source_request_id uuid,source_request_ref uuid,subject_user_id uuid,
 plan_sha256 bytea,workset_sha256 bytea,erasure_effective_at timestamptz,operations text[],
 membership_postcondition_contract text,membership_postcondition_sha256 bytea,membership_count bigint,variation_count bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_subject uuid:=gen_random_uuid();v_execution uuid:=gen_random_uuid();v_request uuid:=gen_random_uuid();v_ref uuid:=gen_random_uuid();
 v_plan bytea:=gen_random_bytes(32);v_workset bytea:=gen_random_bytes(32);v_effective timestamptz:=date_trunc('microseconds',clock_timestamp());
 v_operations text[]:=ARRAY['AUTH_ACCESS_REVOKE','AUTH_TOKEN_DELETE','PROFILE_IDENTITY_DELETE','PROVIDER_LOCAL_FENCE','IDENTITY_CLEAR','MEMBERSHIP_ACTIVE_REVOKE','MEMBERSHIP_HISTORY_ANONYMIZE'];
 v_season uuid:=gen_random_uuid();v_membership uuid:=gen_random_uuid();v_group uuid:=gen_random_uuid();v_plan_id uuid:=gen_random_uuid();v_variation_group uuid:=gen_random_uuid();
 v_programme uuid;v_modality uuid;computed record;original_summary text;original_patch jsonb;
BEGIN
 IF p_worker_ref IS NULL OR current_setting('mycfc.privacy_restore_isolated',true)<>'on' THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_synthetic_fixture_rejected'; END IF;
 SELECT id INTO v_programme FROM programmes ORDER BY id LIMIT 1;
 SELECT id INTO v_modality FROM modalities ORDER BY id LIMIT 1;
 IF v_programme IS NULL OR v_modality IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_synthetic_fixture_unavailable'; END IF;
 INSERT INTO users(id,name,email,password_hash,date_of_birth,created_at,updated_at)
 VALUES(v_subject,'Synthetic restore fixture',('synthetic-'||v_subject::text||'@invalid.invalid')::citext,'synthetic-disabled','1900-01-01',v_effective,v_effective);
 INSERT INTO member_profiles(user_id,address_line1,created_at,updated_at) VALUES(v_subject,'Synthetic fixture only',v_effective,v_effective);
 INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current,created_at)
 VALUES(v_season,'SYN-'||substr(v_season::text,1,8),'Synthetic restore fixture',v_effective::date-interval '2 years',v_effective::date-interval '1 year',false,v_effective);
 INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on,created_at,updated_at)
 VALUES(v_membership,v_subject,v_season,v_programme,v_effective::date-interval '2 years',v_effective::date-interval '1 year',v_effective,v_effective);
 INSERT INTO membership_modalities(membership_id,modality_id,created_at) VALUES(v_membership,v_modality,v_effective);
 INSERT INTO training_groups(id,name,programme_id,created_by_id,created_at,updated_at)
 VALUES(v_group,'Synthetic restore fixture',v_programme,v_subject,v_effective,v_effective);
 INSERT INTO training_group_members(group_id,membership_id,added_by_id,added_at) VALUES(v_group,v_membership,v_subject,v_effective);
 INSERT INTO training_plans(id,title,programme_id,created_by_id,created_at,updated_at)
 VALUES(v_plan_id,'Synthetic restore fixture',v_programme,v_subject,v_effective,v_effective);
 INSERT INTO training_variation_groups(id,training_group_id,name,kind,effective_from,effective_until,created_by_id,created_at,updated_at)
 VALUES(v_variation_group,v_group,'Synthetic restore fixture','SUBGROUP',v_effective::date-interval '2 years',v_effective::date-interval '1 year',v_subject,v_effective,v_effective);
 INSERT INTO training_variation_group_members(variation_group_id,membership_id,added_by_id,added_at)
 VALUES(v_variation_group,v_membership,v_subject,v_effective);
 original_summary:='Fixture '||v_subject::text||' synthetic-'||v_subject::text||'@invalid.invalid';
 original_patch:=jsonb_build_object('old_name','Synthetic restore fixture','old_login',v_subject::text,'nested',jsonb_build_object('old_email','synthetic-'||v_subject::text||'@invalid.invalid'));
 INSERT INTO training_variations(plan_id,target_membership_id,subject_kind,subject_id,operation,change_summary,patch,created_by_id,created_at,updated_at)
 VALUES(v_plan_id,v_membership,'SEGMENT',gen_random_uuid(),'OVERRIDE',original_summary,original_patch,v_subject,v_effective,v_effective);
 UPDATE training_variations SET change_summary=privacy_scrub_audit_text(change_summary,v_subject,'Synthetic restore fixture','synthetic-'||v_subject::text||'@invalid.invalid',v_subject::text),
  patch=privacy_scrub_audit_json(patch,v_subject,'Synthetic restore fixture','synthetic-'||v_subject::text||'@invalid.invalid',v_subject::text)
 WHERE target_membership_id=v_membership;
 SELECT * INTO computed FROM public.privacy_membership_history_compute(ARRAY[v_membership],v_effective,false);
 UPDATE training_variations SET change_summary=original_summary,patch=original_patch WHERE target_membership_id=v_membership;
 INSERT INTO privacy_protected.provider_connections(id,subject_user_id,service_code,provider_role,provider_contract_version,
  registry_evidence_key_id,registry_evidence_digest,target_key_id,target_opaque,credential_key_id,credential_opaque,state,
  sync_enabled,webhook_enabled,reconnect_enabled,created_at,updated_at)
 VALUES(gen_random_uuid(),v_subject,'synthetic.restore.fixture','PROCESSOR','synthetic-v1','synthetic-key',gen_random_bytes(32),
  'synthetic-key',gen_random_bytes(32),'synthetic-key',gen_random_bytes(32),'ACTIVE',true,true,true,v_effective,v_effective);
 INSERT INTO privacy_protected.restore_synthetic_fixtures(subject_user_id,source_execution_id,source_request_id,source_request_ref,
  plan_sha256,workset_sha256,erasure_effective_at,operations,fixture_marker,created_by_ref,created_at,
  membership_postcondition_contract,membership_postcondition_sha256,membership_count,variation_count)
 VALUES(v_subject,v_execution,v_request,v_ref,v_plan,v_workset,v_effective,v_operations,'mycfc/privacy-restore-synthetic-fixture/v1',p_worker_ref,v_effective,
  computed.contract,computed.postcondition_sha256,computed.membership_count,computed.variation_count);
 RETURN QUERY SELECT v_execution,v_request,v_ref,v_subject,v_plan,v_workset,v_effective,v_operations,
  computed.contract,computed.postcondition_sha256,computed.membership_count,computed.variation_count;
END;$$;

CREATE FUNCTION public.privacy_restore_import_authenticated_v4_hardened(
 p_worker_ref uuid,p_kind text,p_record_version text,p_envelope_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,
 p_ciphertext_sha256 bytea,p_object_version_id text,p_written_at timestamptz,p_verified_at timestamptz,p_retain_until timestamptz,
 p_source_execution_id uuid,p_source_request_id uuid,p_source_request_ref uuid,p_subject_user_id uuid,p_plan_sha256 bytea,p_workset_sha256 bytea,
 p_execution_started_at timestamptz,p_erasure_effective_at timestamptz,p_closure_version text,p_synthetic_fixture text,p_replay_version text,p_action_version text,
 p_operations text[],p_prescription_sha256 bytea,p_record_sha256 bytea,p_membership_postcondition_contract text,p_membership_postcondition_sha256 bytea,
 p_membership_count bigint,p_variation_count bigint
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE import_ref uuid;existing privacy_protected.restore_ledger_imports%ROWTYPE;synthetic_match boolean;
BEGIN
 SELECT EXISTS(SELECT 1 FROM privacy_protected.restore_synthetic_fixtures fixture
  WHERE fixture.subject_user_id=p_subject_user_id AND fixture.source_execution_id=p_source_execution_id
   AND fixture.source_request_id=p_source_request_id AND fixture.source_request_ref=p_source_request_ref
   AND fixture.plan_sha256=p_plan_sha256 AND fixture.workset_sha256=p_workset_sha256
   AND fixture.erasure_effective_at=p_erasure_effective_at AND fixture.operations=p_operations AND fixture.fixture_marker=p_synthetic_fixture
   AND fixture.membership_postcondition_contract=p_membership_postcondition_contract
   AND public.privacy_membership_history_digest_equal(fixture.membership_postcondition_sha256,p_membership_postcondition_sha256)
   AND fixture.membership_count=p_membership_count AND fixture.variation_count=p_variation_count) INTO synthetic_match;
 IF p_worker_ref IS NULL OR p_kind<>'closure' OR p_record_version<>'restore-tombstone/v2'
  OR p_envelope_version<>'x25519-aes256gcm-hkdfsha256/v2' OR p_closure_version<>'restore-tombstone-closure/v4'
  OR p_replay_version<>'relational-erasure-replay/v1' OR p_action_version<>'v1'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR octet_length(p_plan_sha256)<>32
  OR octet_length(p_workset_sha256)<>32 OR octet_length(p_prescription_sha256)<>32 OR octet_length(p_record_sha256)<>32
  OR p_membership_postcondition_contract<>'mycfc/membership-history-postcondition/v1' OR octet_length(p_membership_postcondition_sha256) IS DISTINCT FROM 32
  OR p_membership_count NOT BETWEEN 0 AND 10000 OR p_variation_count NOT BETWEEN 0 AND 100000
  OR p_object_version_id IS NULL OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_written_at IS NULL OR p_verified_at<p_written_at OR p_retain_until IS NULL OR p_verified_at>p_retain_until
  OR p_source_execution_id IS NULL OR p_source_request_id IS NULL OR p_source_request_ref IS NULL OR p_subject_user_id IS NULL
  OR p_execution_started_at IS NULL OR p_erasure_effective_at IS NULL OR p_erasure_effective_at<p_execution_started_at
  OR cardinality(p_operations) NOT BETWEEN 1 AND 17 OR cardinality(p_operations)<>(SELECT count(DISTINCT operation) FROM unnest(p_operations) operation)
  OR EXISTS(SELECT 1 FROM unnest(p_operations) operation WHERE NOT(public.privacy_relational_replay_operation_supported(operation) OR operation='PROVIDER_LOCAL_FENCE'))
  OR (p_synthetic_fixture IS NULL AND EXISTS(SELECT 1 FROM privacy_protected.restore_synthetic_fixtures WHERE subject_user_id=p_subject_user_id))
  OR (p_synthetic_fixture IS NOT NULL AND (p_synthetic_fixture<>'mycfc/privacy-restore-synthetic-fixture/v1' OR NOT synthetic_match))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_import_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_ledger_imports
  WHERE (locator_key_id=p_locator_key_id AND locator_digest=p_locator_digest) OR source_execution_id=p_source_execution_id FOR UPDATE;
 IF existing.id IS NULL THEN
  INSERT INTO privacy_protected.restore_ledger_imports(kind,record_version,envelope_version,encryption_key_id,locator_key_id,locator_digest,
   ciphertext_sha256,object_version_id,written_at,verified_at,retain_until,source_execution_id,source_request_id,source_request_ref,subject_user_id,
   plan_sha256,workset_sha256,execution_started_at,erasure_effective_at,closure_version,synthetic_fixture,replay_version,action_version,operations,
   prescription_sha256,record_sha256,imported_by_ref,membership_postcondition_contract,membership_postcondition_sha256,membership_count,variation_count)
  VALUES(p_kind,p_record_version,p_envelope_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_ciphertext_sha256,p_object_version_id,
   p_written_at,p_verified_at,p_retain_until,p_source_execution_id,p_source_request_id,p_source_request_ref,p_subject_user_id,p_plan_sha256,p_workset_sha256,
   p_execution_started_at,p_erasure_effective_at,p_closure_version,p_synthetic_fixture,p_replay_version,p_action_version,p_operations,p_prescription_sha256,p_record_sha256,p_worker_ref,
   p_membership_postcondition_contract,p_membership_postcondition_sha256,p_membership_count,p_variation_count) RETURNING id INTO import_ref;
 ELSIF ROW(existing.kind,existing.record_version,existing.envelope_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,
   existing.ciphertext_sha256,existing.object_version_id,existing.written_at,existing.verified_at,existing.retain_until,existing.source_execution_id,
   existing.source_request_id,existing.source_request_ref,existing.subject_user_id,existing.plan_sha256,existing.workset_sha256,existing.execution_started_at,
   existing.erasure_effective_at,existing.closure_version,existing.synthetic_fixture,existing.replay_version,existing.action_version,existing.operations,
   existing.prescription_sha256,existing.record_sha256,existing.membership_postcondition_contract,existing.membership_postcondition_sha256,existing.membership_count,existing.variation_count)
  IS DISTINCT FROM ROW(p_kind,p_record_version,p_envelope_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_ciphertext_sha256,
   p_object_version_id,p_written_at,p_verified_at,p_retain_until,p_source_execution_id,p_source_request_id,p_source_request_ref,p_subject_user_id,
   p_plan_sha256,p_workset_sha256,p_execution_started_at,p_erasure_effective_at,p_closure_version,p_synthetic_fixture,p_replay_version,p_action_version,p_operations,
   p_prescription_sha256,p_record_sha256,p_membership_postcondition_contract,p_membership_postcondition_sha256,p_membership_count,p_variation_count)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_import_conflict'; ELSE import_ref:=existing.id; END IF;
 RETURN import_ref;
END;$$;

CREATE FUNCTION public.privacy_restore_finalize_membership_postcondition(p_run_id uuid)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE;computed record;existing privacy_protected.membership_history_replay_postconditions%ROWTYPE;
BEGIN
 SELECT imported_row.* INTO imported FROM privacy_protected.restore_replay_runs run
 JOIN privacy_protected.restore_ledger_imports imported_row ON imported_row.id=run.import_id WHERE run.id=p_run_id;
 IF imported.id IS NULL OR imported.closure_version<>'restore-tombstone-closure/v4' THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
 SELECT * INTO computed FROM public.privacy_membership_history_compute_replay(p_run_id,imported.erasure_effective_at);
 IF computed.contract IS NULL OR computed.contract<>imported.membership_postcondition_contract
  OR NOT public.privacy_membership_history_digest_equal(computed.postcondition_sha256,imported.membership_postcondition_sha256)
  OR computed.membership_count<>imported.membership_count OR computed.variation_count<>imported.variation_count THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_mismatch'; END IF;
 SELECT * INTO existing FROM privacy_protected.membership_history_replay_postconditions WHERE run_id=p_run_id;
 IF existing.run_id IS NULL THEN
  INSERT INTO privacy_protected.membership_history_replay_postconditions(run_id,contract,postcondition_sha256,membership_count,variation_count,canonical_size)
  VALUES(p_run_id,computed.contract,computed.postcondition_sha256,computed.membership_count,computed.variation_count,computed.canonical_size);
 ELSIF existing.contract<>computed.contract OR NOT public.privacy_membership_history_digest_equal(existing.postcondition_sha256,computed.postcondition_sha256)
  OR existing.membership_count<>computed.membership_count OR existing.variation_count<>computed.variation_count OR existing.canonical_size<>computed.canonical_size THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_conflict'; END IF;
END;$$;

ALTER FUNCTION public.privacy_restore_verify_operation(uuid,text,boolean) RENAME TO privacy_restore_verify_operation_inner_015;
CREATE FUNCTION public.privacy_restore_verify_operation(p_run_id uuid,p_operation_code text,p_source_already_applied boolean)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE;subject_email text;subject_login text;
BEGIN
 SELECT imported_row.* INTO imported FROM privacy_protected.restore_replay_runs run JOIN privacy_protected.restore_ledger_imports imported_row ON imported_row.id=run.import_id
 WHERE run.id=p_run_id;
 IF imported.id IS NULL OR imported.kind<>'closure' OR imported.closure_version<>'restore-tombstone-closure/v4' OR NOT(p_operation_code=ANY(imported.operations)) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 IF p_operation_code NOT IN ('MEMBERSHIP_ACTIVE_REVOKE','MEMBERSHIP_HISTORY_ANONYMIZE') THEN
  PERFORM public.privacy_restore_verify_operation_inner_013(p_run_id,p_operation_code,p_source_already_applied); RETURN; END IF;
 IF p_source_already_applied AND NOT EXISTS(SELECT 1 FROM users WHERE id=imported.subject_user_id
  AND erasure_execution_id=imported.source_execution_id AND erased_at=imported.erasure_effective_at) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 SELECT email::text,minor_login_id INTO subject_email,subject_login FROM users WHERE id=imported.subject_user_id;
 IF p_operation_code='MEMBERSHIP_ACTIVE_REVOKE' THEN
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=imported.subject_user_id
   AND (starts_on>=(imported.erasure_effective_at AT TIME ZONE 'UTC')::date OR ends_on IS NULL OR ends_on>=(imported.erasure_effective_at AT TIME ZONE 'UTC')::date)) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=imported.subject_user_id)
   OR EXISTS(SELECT 1 FROM training_variations variation
    WHERE variation.target_membership_id IN(SELECT row.membership_id FROM privacy_protected.membership_history_replay_rows row WHERE row.run_id=p_run_id)
     AND (variation.change_summary IS DISTINCT FROM privacy_scrub_audit_text(variation.change_summary,imported.subject_user_id,NULL,subject_email,subject_login)
      OR variation.patch IS DISTINCT FROM privacy_scrub_audit_json(variation.patch,imported.subject_user_id,NULL,subject_email,subject_login))) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
  PERFORM public.privacy_restore_finalize_membership_postcondition(p_run_id);
 END IF;
END;$$;

ALTER FUNCTION public.privacy_restore_begin_replay_hardened(uuid,uuid) RENAME TO privacy_restore_begin_replay_hardened_inner_015;
CREATE FUNCTION public.privacy_restore_begin_replay_hardened(p_import_id uuid,p_worker_ref uuid)
RETURNS TABLE(run_id uuid,outcome_code text) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE;run_ref uuid;outcome text;has_erasure boolean;
BEGIN
 SELECT * INTO imported FROM privacy_protected.restore_ledger_imports WHERE id=p_import_id;
 IF imported.id IS NULL OR imported.kind<>'closure' OR imported.closure_version<>'restore-tombstone-closure/v4' THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_replay_rejected'; END IF;
 run_ref:=public.privacy_restore_begin_replay(p_import_id,p_worker_ref);
 INSERT INTO privacy_protected.membership_history_replay_captures(run_id) VALUES(run_ref) ON CONFLICT DO NOTHING;
 SELECT EXISTS(SELECT 1 FROM users WHERE id=imported.subject_user_id AND erased_at IS NOT NULL) INTO has_erasure;
 IF has_erasure THEN
  INSERT INTO privacy_protected.membership_history_replay_rows(run_id,membership_id)
   SELECT run_ref,row.membership_id FROM privacy_protected.membership_history_source_rows row WHERE row.execution_id=imported.source_execution_id
   ON CONFLICT DO NOTHING;
 ELSE
  INSERT INTO privacy_protected.membership_history_replay_rows(run_id,membership_id)
   SELECT run_ref,membership.id FROM user_memberships membership
   WHERE membership.user_id=imported.subject_user_id AND membership.starts_on<(imported.erasure_effective_at AT TIME ZONE 'UTC')::date
   ON CONFLICT DO NOTHING;
 END IF;
 SELECT begun.run_id,begun.outcome_code INTO run_ref,outcome FROM public.privacy_restore_begin_replay_hardened_inner_013(p_import_id,p_worker_ref) begun;
 IF outcome='ALREADY_APPLIED_SOURCE' AND NOT EXISTS(SELECT 1 FROM privacy_protected.membership_history_replay_postconditions postcondition WHERE postcondition.run_id=run_ref) THEN
  -- Empty prescriptions do not pass through the history checkpoint.
  IF NOT('MEMBERSHIP_HISTORY_ANONYMIZE'=ANY(imported.operations)) THEN PERFORM public.privacy_restore_finalize_membership_postcondition(run_ref); END IF;
 END IF;
 RETURN QUERY SELECT run_ref,outcome;
END;$$;

ALTER FUNCTION public.privacy_restore_execute_checkpoint(uuid,uuid,smallint,text,text,bytea) RENAME TO privacy_restore_execute_checkpoint_inner_015;
CREATE FUNCTION public.privacy_restore_execute_checkpoint(p_run_id uuid,p_worker_ref uuid,p_operation_position smallint,p_operation_code text,p_action_version text,p_prescription_sha256 bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE;subject_name text;subject_email text;subject_login text;checkpoint_ref uuid;
 previous_timezone text;
BEGIN
 SELECT imported_row.* INTO imported FROM privacy_protected.restore_replay_runs run
 JOIN privacy_protected.restore_ledger_imports imported_row ON imported_row.id=run.import_id WHERE run.id=p_run_id;
 IF imported.id IS NULL OR imported.closure_version<>'restore-tombstone-closure/v4' THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_checkpoint_rejected'; END IF;
 IF p_operation_code='IDENTITY_CLEAR' AND 'MEMBERSHIP_HISTORY_ANONYMIZE'=ANY(imported.operations) THEN
  SELECT name,email::text,minor_login_id::text INTO subject_name,subject_email,subject_login FROM users WHERE id=imported.subject_user_id FOR UPDATE;
  UPDATE training_variations SET
   change_summary=privacy_scrub_audit_text(change_summary,imported.subject_user_id,subject_name,subject_email,subject_login),
   patch=privacy_scrub_audit_json(patch,imported.subject_user_id,subject_name,subject_email,subject_login),updated_at=clock_timestamp()
  WHERE target_membership_id IN(SELECT row.membership_id FROM privacy_protected.membership_history_replay_rows row WHERE row.run_id=p_run_id);
 END IF;
 IF p_operation_code='MEMBERSHIP_ACTIVE_REVOKE' THEN
  previous_timezone:=current_setting('TimeZone');
  PERFORM set_config('TimeZone','UTC',true);
 END IF;
 checkpoint_ref:=public.privacy_restore_execute_checkpoint_inner_013(p_run_id,p_worker_ref,p_operation_position,p_operation_code,p_action_version,p_prescription_sha256);
 IF previous_timezone IS NOT NULL THEN PERFORM set_config('TimeZone',previous_timezone,true); END IF;
 IF NOT('MEMBERSHIP_HISTORY_ANONYMIZE'=ANY(imported.operations)) AND NOT EXISTS(
  SELECT 1 FROM privacy_protected.restore_replay_checkpoints checkpoint WHERE checkpoint.run_id=p_run_id AND checkpoint.status<>'SUCCEEDED') THEN
  PERFORM public.privacy_restore_finalize_membership_postcondition(p_run_id);
 END IF;
 RETURN checkpoint_ref;
END;$$;

CREATE FUNCTION public.privacy_restore_membership_postcondition(p_run_id uuid)
RETURNS TABLE(membership_postcondition_contract text,membership_postcondition_sha256 bytea,membership_count bigint,variation_count bigint)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT postcondition.contract::text,postcondition.postcondition_sha256,postcondition.membership_count,postcondition.variation_count
 FROM privacy_protected.membership_history_replay_postconditions postcondition
 JOIN privacy_protected.restore_replay_runs run ON run.id=postcondition.run_id AND run.status='SUCCEEDED'
 WHERE postcondition.run_id=p_run_id;
$$;

ALTER TABLE privacy_protected.restore_replay_inventory_attestations
 ADD COLUMN closure_v4_count integer NULL CHECK(closure_v4_count IS NULL OR closure_v4_count>=0),
 ADD COLUMN membership_postcondition_contract varchar(64) NULL,
 ADD COLUMN membership_postcondition_sha256 bytea NULL,
 ADD COLUMN membership_postcondition_verified_count integer NULL CHECK(membership_postcondition_verified_count IS NULL OR membership_postcondition_verified_count>=0),
 ADD COLUMN membership_count bigint NULL CHECK(membership_count IS NULL OR membership_count BETWEEN 0 AND 10240000),
 ADD COLUMN variation_count bigint NULL CHECK(variation_count IS NULL OR variation_count BETWEEN 0 AND 102400000);
DO $$DECLARE constraint_name text;
BEGIN
 SELECT constraint_row.conname INTO constraint_name FROM pg_constraint constraint_row
 WHERE constraint_row.conrelid='privacy_protected.restore_replay_inventory_attestations'::regclass AND constraint_row.contype='c'
  AND pg_get_constraintdef(constraint_row.oid) LIKE '%closure_v3_count = replayed_count%' LIMIT 1;
 IF constraint_name IS NULL THEN RAISE EXCEPTION 'privacy restore attestation compatibility constraint missing'; END IF;
 EXECUTE format('ALTER TABLE privacy_protected.restore_replay_inventory_attestations DROP CONSTRAINT %I',constraint_name);
END$$;
ALTER TABLE privacy_protected.restore_replay_inventory_attestations
 ADD CONSTRAINT restore_replay_inventory_attestations_closure_compatibility CHECK((
  (closure_v4_count IS NULL AND membership_postcondition_contract IS NULL AND membership_postcondition_sha256 IS NULL
   AND membership_postcondition_verified_count IS NULL AND membership_count IS NULL AND variation_count IS NULL
   AND closure_v3_count=replayed_count AND intent_only_count=0 AND legacy_closure_v2_count=0 AND erasure_effective_at_verified_count=replayed_count)
  OR (closure_v4_count=replayed_count AND closure_v3_count=0 AND intent_only_count=0 AND legacy_closure_v2_count=0
   AND erasure_effective_at_verified_count=replayed_count
   AND membership_postcondition_contract='mycfc/membership-history-postcondition/v1'
   AND octet_length(membership_postcondition_sha256)=32 AND membership_postcondition_verified_count=replayed_count
   AND membership_count>=0 AND variation_count>=0)) IS TRUE) NOT VALID;
ALTER TABLE privacy_protected.restore_replay_inventory_attestations
 VALIDATE CONSTRAINT restore_replay_inventory_attestations_closure_compatibility;

CREATE FUNCTION public.privacy_restore_record_inventory_attestation_v4(p_input_source text,p_inventory_sha256 bytea,p_schema_migration_digest bytea,
 p_policy_version text,p_executor_version text,p_plan_schema_version text,p_image_digest text,p_run_ids uuid[],
 p_object_count integer,p_imported_count integer,p_replayed_count integer,p_already_applied_count integer,
 p_absence_verified_count integer,p_synthetic_replayed_count integer,p_closure_v4_count integer,p_intent_only_count integer,
 p_legacy_closure_v2_count integer,p_erasure_effective_at_verified_count integer,p_membership_postcondition_contract text,
 p_membership_postcondition_sha256 bytea,p_membership_postcondition_verified_count integer,p_membership_count bigint,p_variation_count bigint)
RETURNS bytea LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE attestation_ref uuid;evidence bytea;computed_membership_digest bytea;computed_membership_count bigint;computed_variation_count bigint;
 existing privacy_protected.restore_replay_inventory_attestations%ROWTYPE;
BEGIN
 IF p_input_source NOT IN ('LIVE_LEDGER','SYNTHETIC_BOOTSTRAP') OR octet_length(p_inventory_sha256)<>32 OR octet_length(p_schema_migration_digest)<>32
  OR p_policy_version!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' OR p_executor_version!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_plan_schema_version!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' OR p_image_digest!~'^sha256:[0-9a-f]{64}$'
  OR p_object_count<1 OR p_replayed_count<1 OR cardinality(p_run_ids)<>p_replayed_count
  OR cardinality(p_run_ids)<>(SELECT count(DISTINCT listed.run_id) FROM unnest(p_run_ids) listed(run_id))
  OR p_replayed_count<>p_imported_count+p_already_applied_count OR p_absence_verified_count<>p_replayed_count
  OR (p_input_source='LIVE_LEDGER' AND p_synthetic_replayed_count<>0) OR (p_input_source='SYNTHETIC_BOOTSTRAP' AND p_synthetic_replayed_count<>p_replayed_count)
  OR p_closure_v4_count<>p_replayed_count OR p_intent_only_count<>0 OR p_legacy_closure_v2_count<>0
  OR p_erasure_effective_at_verified_count<>p_replayed_count OR p_membership_postcondition_contract<>'mycfc/membership-history-postcondition/v1'
  OR octet_length(p_membership_postcondition_sha256) IS DISTINCT FROM 32 OR p_membership_postcondition_verified_count<>p_replayed_count
  OR p_membership_count<0 OR p_variation_count<0
  OR EXISTS(SELECT 1 FROM unnest(p_run_ids) listed(run_id)
   LEFT JOIN privacy_protected.restore_replay_runs run ON run.id=listed.run_id
   LEFT JOIN privacy_protected.restore_ledger_imports imported ON imported.id=run.import_id
   LEFT JOIN privacy_protected.membership_history_replay_postconditions postcondition ON postcondition.run_id=run.id
   WHERE run.status<>'SUCCEEDED' OR imported.closure_version<>'restore-tombstone-closure/v4'
    OR postcondition.contract IS DISTINCT FROM imported.membership_postcondition_contract
    OR NOT public.privacy_membership_history_digest_equal(postcondition.postcondition_sha256,imported.membership_postcondition_sha256)
    OR postcondition.membership_count IS DISTINCT FROM imported.membership_count OR postcondition.variation_count IS DISTINCT FROM imported.variation_count
    OR (p_input_source='LIVE_LEDGER')<>(imported.synthetic_fixture IS NULL))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_attestation_rejected'; END IF;
 SELECT digest(privacy_membership_history_frame('mycfc/membership-history-postcondition-set/v1')||
  string_agg(privacy_membership_history_frame(postcondition.postcondition_sha256),''::bytea ORDER BY postcondition.postcondition_sha256), 'sha256'),
  sum(postcondition.membership_count),sum(postcondition.variation_count)
 INTO computed_membership_digest,computed_membership_count,computed_variation_count
 FROM privacy_protected.membership_history_replay_postconditions postcondition WHERE postcondition.run_id=ANY(p_run_ids);
 IF NOT public.privacy_membership_history_digest_equal(computed_membership_digest,p_membership_postcondition_sha256)
  OR p_membership_count IS DISTINCT FROM computed_membership_count OR p_variation_count IS DISTINCT FROM computed_variation_count
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_attestation_rejected'; END IF;
 SELECT digest(privacy_membership_history_frame('mycfc/privacy-restore-attestation-evidence/v4')||
   privacy_membership_history_frame(p_inventory_sha256)||privacy_membership_history_frame(computed_membership_digest)||
   privacy_membership_history_frame(COALESCE(string_agg(checkpoint.result_sha256,''::bytea ORDER BY checkpoint.result_sha256),''::bytea)),'sha256') INTO evidence
 FROM privacy_protected.restore_replay_checkpoints checkpoint WHERE checkpoint.run_id=ANY(p_run_ids);
 PERFORM pg_advisory_xact_lock(hashtextextended(p_input_source||':'||encode(p_inventory_sha256,'hex'),0));
 SELECT * INTO existing FROM privacy_protected.restore_replay_inventory_attestations WHERE input_source=p_input_source AND inventory_sha256=p_inventory_sha256;
 IF existing.id IS NOT NULL THEN
  IF existing.closure_v4_count IS DISTINCT FROM p_closure_v4_count OR existing.membership_postcondition_contract IS DISTINCT FROM p_membership_postcondition_contract
   OR NOT public.privacy_membership_history_digest_equal(existing.membership_postcondition_sha256,p_membership_postcondition_sha256)
   OR existing.membership_postcondition_verified_count IS DISTINCT FROM p_membership_postcondition_verified_count OR existing.evidence_sha256<>evidence
  THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_attestation_conflict'; END IF;
  RETURN existing.evidence_sha256;
 END IF;
 INSERT INTO privacy_protected.restore_replay_inventory_attestations(input_source,inventory_sha256,schema_migration_digest,policy_version,executor_version,plan_schema_version,image_digest,
  object_count,imported_count,replayed_count,already_applied_count,absence_verified_count,synthetic_replayed_count,closure_v3_count,intent_only_count,
  legacy_closure_v2_count,erasure_effective_at_verified_count,evidence_sha256,closure_v4_count,membership_postcondition_contract,
  membership_postcondition_sha256,membership_postcondition_verified_count,membership_count,variation_count)
 VALUES(p_input_source,p_inventory_sha256,p_schema_migration_digest,p_policy_version,p_executor_version,p_plan_schema_version,p_image_digest,
  p_object_count,p_imported_count,p_replayed_count,p_already_applied_count,p_absence_verified_count,p_synthetic_replayed_count,0,p_intent_only_count,
  p_legacy_closure_v2_count,p_erasure_effective_at_verified_count,evidence,p_closure_v4_count,p_membership_postcondition_contract,
  p_membership_postcondition_sha256,p_membership_postcondition_verified_count,p_membership_count,p_variation_count) RETURNING id INTO attestation_ref;
 INSERT INTO privacy_protected.restore_replay_inventory_attestation_runs(attestation_id,run_id)
 SELECT attestation_ref,listed.run_id FROM unnest(p_run_ids) listed(run_id);
 RETURN evidence;
END;$$;

DROP FUNCTION public.privacy_restore_observe_inventory(text,bytea,bytea,text,text,text,text);
CREATE FUNCTION public.privacy_restore_observe_inventory(p_input_source text,p_inventory_sha256 bytea,p_schema_migration_digest bytea,
 p_policy_version text,p_executor_version text,p_plan_schema_version text,p_image_digest text)
RETURNS TABLE(replay_count integer,source_already_applied_count integer,synthetic_count integer,verified_run_count integer,
 expected_checkpoint_count integer,succeeded_checkpoint_count integer,provider_absent_count integer,consent_clock_verified_count integer,
 closure_v4_count integer,erasure_effective_at_verified_count integer,membership_postcondition_contract text,
 membership_postcondition_sha256 bytea,membership_postcondition_verified_count integer,membership_count bigint,variation_count bigint,evidence_sha256 bytea)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT attestation.replayed_count,
  count(DISTINCT run.id) FILTER(WHERE run.outcome_code='ALREADY_APPLIED_SOURCE')::integer,
  count(DISTINCT run.id) FILTER(WHERE imported.synthetic_fixture='mycfc/privacy-restore-synthetic-fixture/v1')::integer,
  count(DISTINCT run.id) FILTER(WHERE cardinality(imported.operations)=(SELECT count(*) FROM privacy_protected.restore_replay_checkpoints exact_checkpoint WHERE exact_checkpoint.run_id=run.id)
   AND NOT EXISTS(SELECT 1 FROM privacy_protected.restore_replay_checkpoints failed_checkpoint WHERE failed_checkpoint.run_id=run.id AND failed_checkpoint.status<>'SUCCEEDED'))::integer,
  count(checkpoint.*)::integer,count(checkpoint.*) FILTER(WHERE checkpoint.status='SUCCEEDED')::integer,
  count(DISTINCT run.id) FILTER(WHERE 'PROVIDER_LOCAL_FENCE'=ANY(imported.operations)
   AND NOT EXISTS(SELECT 1 FROM privacy_protected.provider_connections connection WHERE connection.subject_user_id=imported.subject_user_id)
   AND NOT EXISTS(SELECT 1 FROM privacy_protected.provider_credential_quarantine quarantine JOIN privacy_protected.provider_targets target ON target.id=quarantine.target_id
    JOIN privacy_protected.provider_capture_sets capture ON capture.execution_id=target.execution_id WHERE capture.subject_user_id=imported.subject_user_id))::integer,
  count(DISTINCT run.id) FILTER(WHERE 'IDENTITY_CLEAR'=ANY(imported.operations) AND EXISTS(SELECT 1 FROM users WHERE users.id=imported.subject_user_id AND users.erased_at=imported.erasure_effective_at)
   AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=imported.subject_user_id AND consent.ceased_at IS NULL)
   AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=imported.subject_user_id AND consent.cessation_reason='ACCOUNT_ERASURE'
    AND (consent.ceased_at<>imported.erasure_effective_at OR consent.evidence_expires_at<>imported.erasure_effective_at+interval '3 years')))::integer,
  count(DISTINCT run.id) FILTER(WHERE imported.closure_version='restore-tombstone-closure/v4')::integer,
  count(DISTINCT run.id) FILTER(WHERE EXISTS(SELECT 1 FROM users WHERE users.id=imported.subject_user_id AND users.erased_at=imported.erasure_effective_at))::integer,
  attestation.membership_postcondition_contract::text,attestation.membership_postcondition_sha256,
  count(DISTINCT run.id) FILTER(WHERE postcondition.contract=imported.membership_postcondition_contract
   AND public.privacy_membership_history_digest_equal(postcondition.postcondition_sha256,imported.membership_postcondition_sha256)
   AND postcondition.membership_count=imported.membership_count AND postcondition.variation_count=imported.variation_count)::integer,
  attestation.membership_count,attestation.variation_count,attestation.evidence_sha256
 FROM privacy_protected.restore_replay_inventory_attestations attestation
 JOIN privacy_protected.restore_replay_inventory_attestation_runs link ON link.attestation_id=attestation.id
 JOIN privacy_protected.restore_replay_runs run ON run.id=link.run_id
 JOIN privacy_protected.restore_ledger_imports imported ON imported.id=run.import_id
 JOIN privacy_protected.restore_replay_checkpoints checkpoint ON checkpoint.run_id=run.id
 JOIN privacy_protected.membership_history_replay_postconditions postcondition ON postcondition.run_id=run.id
 WHERE attestation.input_source=p_input_source AND attestation.inventory_sha256=p_inventory_sha256
  AND attestation.schema_migration_digest=p_schema_migration_digest AND attestation.policy_version=p_policy_version
  AND attestation.executor_version=p_executor_version AND attestation.plan_schema_version=p_plan_schema_version AND attestation.image_digest=p_image_digest
 GROUP BY attestation.id,attestation.replayed_count,attestation.membership_postcondition_contract,attestation.membership_postcondition_sha256,
  attestation.membership_count,attestation.variation_count,attestation.evidence_sha256 HAVING count(DISTINCT run.id)=attestation.replayed_count;
$$;

ALTER TABLE privacy_activation_authenticated_artifacts
 ADD COLUMN restore_membership_postcondition_contract varchar(64) NULL,
 ADD COLUMN restore_membership_postcondition_sha256 bytea NULL,
 ADD COLUMN restore_membership_postcondition_verified_count bigint NULL,
 ADD COLUMN restore_membership_count bigint NULL,
 ADD COLUMN restore_variation_count bigint NULL;
DO $$DECLARE constraint_name text;
BEGIN
 SELECT constraint_row.conname INTO constraint_name FROM pg_constraint constraint_row
 WHERE constraint_row.conrelid='public.privacy_activation_authenticated_artifacts'::regclass AND constraint_row.contype='c'
  AND pg_get_constraintdef(constraint_row.oid) LIKE '%restore_closure_contract%' LIMIT 1;
 IF constraint_name IS NULL THEN RAISE EXCEPTION 'privacy activation artifact compatibility constraint missing'; END IF;
 EXECUTE format('ALTER TABLE public.privacy_activation_authenticated_artifacts DROP CONSTRAINT %I',constraint_name);
END$$;
ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v4_check CHECK(
 ((kind='RESTORE' AND signing_key_id IS NULL AND immutable_evidence_ref IS NOT NULL AND schema_migration_digest IS NOT NULL
   AND baseline_includes_through IS NULL AND restore_input_source IN('LIVE_LEDGER','SYNTHETIC_BOOTSTRAP')
   AND restore_input_contract='mycfc/privacy-restore-ledger-input/v2' AND restore_replay_contract='relational-erasure-replay/v1'
   AND restore_closure_contract IN('restore-tombstone-closure/v3','restore-tombstone-closure/v4')
   AND octet_length(restore_candidate_sha256)=32 AND octet_length(restore_inventory_sha256)=32 AND octet_length(restore_observer_sha256)=32
   AND restore_object_count>=restore_replayed_count AND restore_replayed_count>0
   AND ((restore_input_source='LIVE_LEDGER' AND restore_synthetic_count=0) OR (restore_input_source='SYNTHETIC_BOOTSTRAP' AND restore_synthetic_count=restore_replayed_count))
   AND ((restore_closure_contract='restore-tombstone-closure/v3' AND restore_membership_postcondition_contract IS NULL
     AND restore_membership_postcondition_sha256 IS NULL AND restore_membership_postcondition_verified_count IS NULL
     AND restore_membership_count IS NULL AND restore_variation_count IS NULL)
    OR (restore_closure_contract='restore-tombstone-closure/v4'
     AND restore_membership_postcondition_contract='mycfc/membership-history-postcondition/v1'
     AND octet_length(restore_membership_postcondition_sha256)=32
     AND restore_membership_postcondition_verified_count=restore_replayed_count
     AND restore_membership_count>=0 AND restore_variation_count>=0)))
 OR (kind='INFRASTRUCTURE' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL
   AND production_state_serial>0 AND hetzner_state_serial>0 AND octet_length(production_state_sha256)=32 AND octet_length(hetzner_state_sha256)=32
   AND octet_length(production_plan_sha256)=32 AND octet_length(hetzner_plan_sha256)=32 AND worker_identity_enabled AND s3_version_deletion_enabled
   AND ledger_broker_invoke_enabled AND worker_monitoring_enabled AND restore_infrastructure_enabled AND restore_ledger_write_enabled)
 OR (kind='PROVIDER' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL AND provider_registry_state='READY'
   AND provider_registration_count>0 AND octet_length(provider_registry_sha256)=32)
 OR (kind='SCHEMA' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL AND octet_length(schema_migration_digest)=32
   AND baseline_includes_through IN('202609100014_privacy_activation_broker','202609100015_privacy_membership_postcondition'))) IS TRUE) NOT VALID;
ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v4_check;
CREATE OR REPLACE FUNCTION privacy_activation_record_authenticated_evidence(
 p_actor uuid,p_kind text,p_evidence_sha256 bytea,p_reference_code text,p_observed_at timestamptz,p_expires_at timestamptz,p_artifact jsonb
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_evidence_id uuid;now_at timestamptz:=clock_timestamp();expected_keys text[];
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id
   JOIN platform_roles role ON role.id=assignment.role_id WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN')
  OR p_kind NOT IN ('RESTORE','INFRASTRUCTURE','PROVIDER','SCHEMA') OR octet_length(p_evidence_sha256)<>32
  OR p_observed_at>now_at OR p_observed_at<=now_at-interval '2160 hours' OR p_expires_at<=now_at OR p_expires_at>p_observed_at+interval '2160 hours'
  OR jsonb_typeof(p_artifact)<>'object' THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_evidence_rejected'; END IF;
 expected_keys:=CASE p_kind
  WHEN 'RESTORE' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','schema_migration_digest','restore_input_source','restore_input_contract','restore_replay_contract','restore_closure_contract','restore_candidate_sha256','restore_inventory_sha256','restore_object_count','restore_replayed_count','restore_synthetic_count','restore_observer_sha256','restore_membership_postcondition_contract','restore_membership_postcondition_sha256','restore_membership_postcondition_verified_count','restore_membership_count','restore_variation_count']
  WHEN 'INFRASTRUCTURE' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','signing_key_id','production_state_serial','hetzner_state_serial','production_state_sha256','hetzner_state_sha256','production_plan_sha256','hetzner_plan_sha256','worker_identity_enabled','s3_version_deletion_enabled','ledger_broker_invoke_enabled','worker_monitoring_enabled','restore_infrastructure_enabled','restore_ledger_write_enabled']
  WHEN 'PROVIDER' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','signing_key_id','provider_registry_state','provider_registration_count','provider_registry_sha256']
  WHEN 'SCHEMA' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','signing_key_id','schema_migration_digest','baseline_includes_through'] END;
 IF NOT p_artifact ?& expected_keys OR (SELECT count(*) FROM jsonb_object_keys(p_artifact))<>cardinality(expected_keys)
  OR p_artifact->>'policy_version' IS NULL OR p_artifact->>'executor_version'<>'privacy-erasure-executor/v2'
  OR p_artifact->>'plan_schema_version'<>'privacy-erasure-plan/v2' OR p_artifact->>'image_digest'!~'^sha256:[0-9a-f]{64}$' THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_artifact_rejected'; END IF;
 IF (p_kind='RESTORE' AND p_reference_code<>'mycfc/privacy-restore-drill-attestation/v2')
  OR (p_kind='INFRASTRUCTURE' AND p_reference_code<>'mycfc/privacy-infrastructure-posture/v1')
  OR (p_kind='PROVIDER' AND p_reference_code<>'mycfc/privacy-provider-registry/v1')
  OR (p_kind='SCHEMA' AND p_reference_code<>'mycfc/schema-migration-inventory/v1') THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_evidence_contract_rejected'; END IF;
 IF (p_kind='RESTORE' AND (p_artifact->>'restore_closure_contract'<>'restore-tombstone-closure/v4'
    OR p_artifact->>'restore_membership_postcondition_contract'<>'mycfc/membership-history-postcondition/v1'
    OR (p_artifact->>'restore_membership_postcondition_verified_count')::bigint<>(p_artifact->>'restore_replayed_count')::bigint))
  OR (p_kind='SCHEMA' AND p_artifact->>'baseline_includes_through'<>'202609100015_privacy_membership_postcondition') THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_artifact_rejected'; END IF;
 INSERT INTO privacy_activation_evidence(kind,evidence_sha256,reference_code,observed_at,expires_at,recorded_by_ref,recorded_at)
 VALUES(p_kind,p_evidence_sha256,p_reference_code,p_observed_at,p_expires_at,p_actor,now_at)
 ON CONFLICT(kind,evidence_sha256) DO NOTHING RETURNING id INTO v_evidence_id;
 IF v_evidence_id IS NULL THEN
  SELECT evidence.id INTO v_evidence_id FROM privacy_activation_evidence evidence WHERE evidence.kind=p_kind AND evidence.evidence_sha256=p_evidence_sha256
   AND evidence.reference_code=p_reference_code AND evidence.observed_at=p_observed_at AND evidence.expires_at=p_expires_at;
  IF v_evidence_id IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_evidence_conflict'; END IF;
 END IF;
 INSERT INTO privacy_activation_authenticated_artifacts(
  evidence_id,kind,policy_version,executor_version,plan_schema_version,image_digest,immutable_evidence_ref,immutable_evidence_sha256,signing_key_id,
  schema_migration_digest,baseline_includes_through,production_state_serial,hetzner_state_serial,production_state_sha256,hetzner_state_sha256,
  production_plan_sha256,hetzner_plan_sha256,worker_identity_enabled,s3_version_deletion_enabled,ledger_broker_invoke_enabled,worker_monitoring_enabled,
  restore_infrastructure_enabled,restore_ledger_write_enabled,provider_registry_state,provider_registration_count,provider_registry_sha256,restore_input_source,
  restore_input_contract,restore_replay_contract,restore_closure_contract,restore_candidate_sha256,restore_inventory_sha256,restore_object_count,restore_replayed_count,
  restore_synthetic_count,restore_observer_sha256,restore_membership_postcondition_contract,restore_membership_postcondition_sha256,restore_membership_postcondition_verified_count,restore_membership_count,restore_variation_count,authenticated_at)
 VALUES(v_evidence_id,p_kind,p_artifact->>'policy_version',p_artifact->>'executor_version',p_artifact->>'plan_schema_version',p_artifact->>'image_digest',
  p_artifact->>'evidence_ref',decode(p_artifact->>'evidence_sha256','base64'),p_artifact->>'signing_key_id',decode(p_artifact->>'schema_migration_digest','base64'),
  p_artifact->>'baseline_includes_through',(p_artifact->>'production_state_serial')::bigint,(p_artifact->>'hetzner_state_serial')::bigint,
  decode(p_artifact->>'production_state_sha256','base64'),decode(p_artifact->>'hetzner_state_sha256','base64'),decode(p_artifact->>'production_plan_sha256','base64'),
  decode(p_artifact->>'hetzner_plan_sha256','base64'),(p_artifact->>'worker_identity_enabled')::boolean,(p_artifact->>'s3_version_deletion_enabled')::boolean,
  (p_artifact->>'ledger_broker_invoke_enabled')::boolean,(p_artifact->>'worker_monitoring_enabled')::boolean,(p_artifact->>'restore_infrastructure_enabled')::boolean,
  (p_artifact->>'restore_ledger_write_enabled')::boolean,p_artifact->>'provider_registry_state',(p_artifact->>'provider_registration_count')::bigint,
  decode(p_artifact->>'provider_registry_sha256','base64'),p_artifact->>'restore_input_source',p_artifact->>'restore_input_contract',p_artifact->>'restore_replay_contract',
  p_artifact->>'restore_closure_contract',decode(p_artifact->>'restore_candidate_sha256','base64'),
  decode(p_artifact->>'restore_inventory_sha256','base64'),(p_artifact->>'restore_object_count')::bigint,(p_artifact->>'restore_replayed_count')::bigint,
  (p_artifact->>'restore_synthetic_count')::bigint,decode(p_artifact->>'restore_observer_sha256','base64'),p_artifact->>'restore_membership_postcondition_contract',decode(p_artifact->>'restore_membership_postcondition_sha256','base64'),(p_artifact->>'restore_membership_postcondition_verified_count')::bigint,(p_artifact->>'restore_membership_count')::bigint,(p_artifact->>'restore_variation_count')::bigint,now_at)
 ON CONFLICT ON CONSTRAINT privacy_activation_authenticated_artifacts_pkey DO NOTHING;
 IF NOT EXISTS(SELECT 1 FROM privacy_activation_authenticated_artifacts authenticated WHERE authenticated.evidence_id=v_evidence_id
   AND authenticated.kind=p_kind AND authenticated.policy_version=p_artifact->>'policy_version' AND authenticated.image_digest=p_artifact->>'image_digest') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_artifact_conflict'; END IF;
 RETURN v_evidence_id;
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range OR check_violation THEN
 RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_artifact_rejected';
END;$$;

-- Completion may proceed only from the current authenticated closure and an
-- immutable membership-history postcondition that still recomputes exactly.
CREATE FUNCTION public.privacy_completion_membership_postcondition_ready(p_execution_id uuid)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT public.privacy_membership_history_source_postcondition_ready(p_execution_id);
$$;

-- The completion implementation predates closure v4 and was wrapped by the
-- release guard in 013. Replace only the two exact, asserted version clauses;
-- any unexpected predecessor definition aborts the migration.
DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_completion_prepare_inner_013(uuid,uuid)'::regprocedure) INTO definition;
 old_clause:='receipt.ledger_version=''restore-tombstone-closure/v2''';
 new_clause:='receipt.ledger_version=''restore-tombstone-closure/v4'' AND public.privacy_completion_membership_postcondition_ready(execution.id)';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_completion_prepare_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_completion_finalize_inner_013(uuid,uuid,bytea,bytea)'::regprocedure) INTO definition;
 old_clause:='receipt.ledger_version<>''restore-tombstone-closure/v2''';
 new_clause:='receipt.ledger_version<>''restore-tombstone-closure/v4'' OR NOT public.privacy_completion_membership_postcondition_ready(execution.id)';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_completion_finalize_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

REVOKE ALL ON TABLE privacy_protected.membership_history_source_captures,privacy_protected.membership_history_source_rows,
 privacy_protected.membership_history_source_postconditions,privacy_protected.membership_history_replay_captures,
 privacy_protected.membership_history_replay_rows,privacy_protected.membership_history_replay_postconditions FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_membership_history_frame(text),public.privacy_membership_history_frame(bytea),
 public.privacy_membership_history_digest_equal(bytea,bytea),public.privacy_membership_history_compute(uuid[],timestamptz,boolean),
 public.privacy_membership_history_compute_source(uuid,timestamptz),public.privacy_membership_history_compute_replay(uuid,timestamptz),
 public.privacy_membership_history_lock_source(uuid),public.privacy_membership_history_source_postcondition_ready(uuid),public.prevent_sealed_membership_history_mutation(),
 public.privacy_completion_membership_postcondition_ready(uuid),
 public.privacy_worker_execute_checkpoint_inner_015(uuid,uuid,uuid,bigint,uuid,text,text),
 public.privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text),
 public.privacy_tombstone_prepare_closure_v4(uuid,uuid),public.privacy_tombstone_confirm_closure_v4(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),
 public.privacy_restore_create_synthetic_fixture(uuid),
 public.privacy_restore_import_authenticated_v4_hardened(uuid,text,text,text,text,text,bytea,bytea,text,timestamptz,timestamptz,timestamptz,uuid,uuid,uuid,uuid,bytea,bytea,timestamptz,timestamptz,text,text,text,text,text[],bytea,bytea,text,bytea,bigint,bigint),
 public.privacy_restore_finalize_membership_postcondition(uuid),public.privacy_restore_verify_operation_inner_015(uuid,text,boolean),
 public.privacy_restore_verify_operation(uuid,text,boolean),public.privacy_restore_begin_replay_hardened_inner_015(uuid,uuid),
 public.privacy_restore_begin_replay_hardened(uuid,uuid),public.privacy_restore_execute_checkpoint_inner_015(uuid,uuid,smallint,text,text,bytea),
 public.privacy_restore_execute_checkpoint(uuid,uuid,smallint,text,text,bytea),public.privacy_restore_membership_postcondition(uuid),
 public.privacy_restore_record_inventory_attestation_v4(text,bytea,bytea,text,text,text,text,uuid[],integer,integer,integer,integer,integer,integer,integer,integer,integer,integer,text,bytea,integer,bigint,bigint),
 public.privacy_restore_observe_inventory(text,bytea,bytea,text,text,text,text) FROM PUBLIC;

-- Renaming a previously granted function preserves its ACL. Prove that every
-- newly-created inner/helper capability is owner-only before the migration
-- can commit; bootstrap later grants only the reviewed public wrappers.
DO $$DECLARE capability record;grantee_name text;
BEGIN
 FOR capability IN
  SELECT proc.oid,proc.proowner,acl.grantee FROM pg_proc proc JOIN pg_namespace namespace ON namespace.oid=proc.pronamespace
  CROSS JOIN LATERAL aclexplode(COALESCE(proc.proacl,acldefault('f',proc.proowner))) acl
  WHERE namespace.nspname='public' AND (proc.proname LIKE '%\_inner\_015' ESCAPE '\' OR proc.proname LIKE 'privacy\_membership\_history\_%' ESCAPE '\'
   OR proc.proname IN('privacy_worker_execute_checkpoint','privacy_restore_finalize_membership_postcondition','privacy_restore_membership_postcondition','privacy_restore_import_authenticated_v4_hardened','privacy_restore_record_inventory_attestation_v4','privacy_membership_history_source_postcondition_ready','prevent_sealed_membership_history_mutation','privacy_completion_membership_postcondition_ready','privacy_tombstone_prepare_closure_v2','privacy_tombstone_confirm_closure_v2','privacy_tombstone_prepare_closure_v3','privacy_tombstone_confirm_closure_v3'))
   AND acl.privilege_type='EXECUTE' AND acl.grantee<>proc.proowner
 LOOP
  grantee_name:=CASE WHEN capability.grantee=0 THEN 'PUBLIC' ELSE quote_ident((SELECT rolname FROM pg_roles WHERE oid=capability.grantee)) END;
  IF grantee_name IS NOT NULL THEN EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM %s',capability.oid::regprocedure,grantee_name); END IF;
 END LOOP;
 IF EXISTS(SELECT 1 FROM pg_proc proc JOIN pg_namespace namespace ON namespace.oid=proc.pronamespace
  CROSS JOIN LATERAL aclexplode(COALESCE(proc.proacl,acldefault('f',proc.proowner))) acl
  WHERE namespace.nspname='public' AND (proc.proname LIKE '%\_inner\_015' ESCAPE '\' OR proc.proname LIKE 'privacy\_membership\_history\_%' ESCAPE '\'
   OR proc.proname IN('privacy_worker_execute_checkpoint','privacy_restore_finalize_membership_postcondition','privacy_restore_membership_postcondition','privacy_restore_import_authenticated_v4_hardened','privacy_restore_record_inventory_attestation_v4','privacy_membership_history_source_postcondition_ready','prevent_sealed_membership_history_mutation','privacy_completion_membership_postcondition_ready','privacy_tombstone_prepare_closure_v2','privacy_tombstone_confirm_closure_v2','privacy_tombstone_prepare_closure_v3','privacy_tombstone_confirm_closure_v3'))
   AND acl.privilege_type='EXECUTE' AND acl.grantee<>proc.proowner) THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_inner_capability_revoke_failed'; END IF;
END$$;

-- A schema upgrade invalidates all prior release-specific evidence.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp() WHERE singleton;
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at)
 SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton;
CREATE OR REPLACE FUNCTION privacy_activation_authenticated_set_digest(p_policy_version text,p_evidence_ids uuid[])
RETURNS bytea LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE policy privacy_request_policies%ROWTYPE;now_at timestamptz:=clock_timestamp();result bytea;
BEGIN
 SELECT * INTO policy FROM privacy_request_policies WHERE version=p_policy_version;
 IF policy.version IS NULL OR policy.adopted_at IS NULL OR policy.working_retention_days<>90 OR cardinality(p_evidence_ids)<>4
  OR policy.executor_version<>'privacy-erasure-executor/v2' OR policy.plan_schema_version<>'privacy-erasure-plan/v2'
  OR (SELECT count(*) FROM privacy_activation_evidence evidence JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id
      WHERE evidence.id=ANY(p_evidence_ids) AND evidence.expires_at>now_at AND artifact.policy_version=policy.version
       AND artifact.executor_version=policy.executor_version AND artifact.plan_schema_version=policy.plan_schema_version)<>4
  OR (SELECT count(DISTINCT evidence.kind) FROM privacy_activation_evidence evidence JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id
      WHERE evidence.id=ANY(p_evidence_ids) AND evidence.expires_at>now_at)<>4
  OR (SELECT count(DISTINCT artifact.image_digest) FROM privacy_activation_authenticated_artifacts artifact WHERE artifact.evidence_id=ANY(p_evidence_ids))<>1
  OR (SELECT restore.schema_migration_digest IS DISTINCT FROM schema_row.schema_migration_digest
      FROM privacy_activation_authenticated_artifacts restore CROSS JOIN privacy_activation_authenticated_artifacts schema_row
      WHERE restore.evidence_id=ANY(p_evidence_ids) AND restore.kind='RESTORE' AND schema_row.evidence_id=ANY(p_evidence_ids) AND schema_row.kind='SCHEMA')

  OR NOT EXISTS(SELECT 1 FROM privacy_activation_authenticated_artifacts restore
   WHERE restore.evidence_id=ANY(p_evidence_ids) AND restore.kind='RESTORE'
    AND restore.restore_closure_contract='restore-tombstone-closure/v4'
    AND restore.restore_membership_postcondition_contract='mycfc/membership-history-postcondition/v1'
    AND octet_length(restore.restore_membership_postcondition_sha256)=32
    AND restore.restore_membership_postcondition_verified_count=restore.restore_replayed_count)
  OR NOT EXISTS(SELECT 1 FROM privacy_activation_authenticated_artifacts schema_row
   WHERE schema_row.evidence_id=ANY(p_evidence_ids) AND schema_row.kind='SCHEMA'
    AND schema_row.baseline_includes_through='202609100015_privacy_membership_postcondition')
 THEN RETURN NULL; END IF;
 SELECT digest(convert_to(string_agg(evidence.kind||':'||encode(evidence.evidence_sha256,'hex')||':'||
   encode(digest(convert_to((to_jsonb(artifact)-'authenticated_at')::text,'UTF8'),'sha256'),'hex')||':'||
   to_char(evidence.observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')||':'||
   to_char(evidence.expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'|' ORDER BY evidence.kind),'UTF8'),'sha256')
 INTO result FROM privacy_activation_evidence evidence JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id
 WHERE evidence.id=ANY(p_evidence_ids);
 RETURN result;
END;$$;
