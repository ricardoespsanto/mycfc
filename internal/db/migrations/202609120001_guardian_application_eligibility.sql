-- Guardian application eligibility, invitation, and privacy-safe abuse controls.
-- Invalid credential and invitation probes are limited to 10 attempts per
-- pseudonymous account and network bucket in 15 minutes.

ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v8_check;
ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v9_check CHECK(
 ((kind='RESTORE' AND provider_inventory_contract IS NULL AND signing_key_id IS NULL AND immutable_evidence_ref IS NOT NULL AND schema_migration_digest IS NOT NULL
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
 OR (kind='INFRASTRUCTURE' AND provider_inventory_contract IS NULL AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL
   AND production_state_serial>0 AND hetzner_state_serial>0 AND octet_length(production_state_sha256)=32 AND octet_length(hetzner_state_sha256)=32
   AND octet_length(production_plan_sha256)=32 AND octet_length(hetzner_plan_sha256)=32 AND worker_identity_enabled AND s3_version_deletion_enabled
   AND ledger_broker_invoke_enabled AND worker_monitoring_enabled AND restore_infrastructure_enabled AND restore_ledger_write_enabled)
 OR (kind='PROVIDER' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL AND provider_registry_state='READY'
   AND octet_length(provider_registry_sha256)=32
   AND ((provider_inventory_contract IS NULL AND provider_registration_count>0)
     OR (provider_inventory_contract='mycfc/privacy-provider-registry-source/v2' AND provider_registration_count=0)))
 OR (kind='SCHEMA' AND provider_inventory_contract IS NULL AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL
   AND octet_length(schema_migration_digest)=32
   AND baseline_includes_through IN('202609100014_privacy_activation_broker','202609100015_privacy_membership_postcondition',
    '202609110001_privacy_upload_finalize_execution_fence','202609110002_privacy_empty_provider_registry_activation',
    '202609110003_privacy_activation_emergency_fence','202609110004_guardian_authority_verification',
    '202609110005_guardian_authority_cutoff_reconciliation','202609120001_guardian_application_eligibility'))) IS TRUE) NOT VALID;
ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v9_check;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609110005_guardian_authority_cutoff_reconciliation''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609120001_guardian_application_eligibility''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_application_activation_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609110005_guardian_authority_cutoff_reconciliation''';
 new_clause:='schema_row.baseline_includes_through=''202609120001_guardian_application_eligibility''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_application_activation_digest_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

ALTER TABLE guardian_authority_policy_events
 DROP CONSTRAINT guardian_authority_policy_events_action_check,
 ALTER COLUMN actor_ref DROP NOT NULL;
ALTER TABLE guardian_authority_policy_events
 ADD CONSTRAINT guardian_authority_policy_events_action_v2_check
  CHECK(action IN('ADOPTED','ENABLED','DISABLED','MIGRATION_DISABLED')) NOT VALID,
 ADD CONSTRAINT guardian_authority_policy_events_actor_v2_check
  CHECK((action='MIGRATION_DISABLED' AND actor_ref IS NULL) OR (action<>'MIGRATION_DISABLED' AND actor_ref IS NOT NULL)) NOT VALID;
ALTER TABLE guardian_authority_policy_events VALIDATE CONSTRAINT guardian_authority_policy_events_action_v2_check;
ALTER TABLE guardian_authority_policy_events VALIDATE CONSTRAINT guardian_authority_policy_events_actor_v2_check;

CREATE OR REPLACE FUNCTION guardian_authority_set_policy_enabled(p_actor_id uuid,p_version text,p_enabled boolean)
RETURNS SETOF guardian_authority_policies LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE policy guardian_authority_policies%ROWTYPE;now_at timestamptz:=clock_timestamp();
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority-policy',0));
 IF NOT guardian_authority_is_administrator(p_actor_id) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_authority_policy_administrator_required'; END IF;
 IF p_enabled THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_policy_replacement_required'; END IF;
 UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL
 WHERE version=p_version AND enabled RETURNING * INTO policy;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_policy_transition_rejected'; END IF;
 INSERT INTO guardian_authority_policy_events(policy_id,policy_version,actor_ref,action,occurred_at)
 VALUES(policy.id,policy.version,p_actor_id,'DISABLED',now_at);
 RETURN NEXT policy;
END;
$$;

DO $$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority-policy',0));
 WITH disabled AS (
  UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL
  WHERE enabled RETURNING id,version
 )
 INSERT INTO guardian_authority_policy_events(policy_id,policy_version,actor_ref,action,occurred_at)
 SELECT id,version,NULL,'MIGRATION_DISABLED',clock_timestamp() FROM disabled;
END$$;

CREATE TABLE guardian_application_intake_release (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 gate_version text NOT NULL CHECK(gate_version='guardian-intake-v2'),
 enabled boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CONSTRAINT guardian_application_intake_release_disabled_check CHECK(NOT enabled)
);
CREATE TABLE guardian_application_intake_release_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 gate_version text NOT NULL,
 action text NOT NULL CHECK(action='CREATED_DISABLED'),
 actor_ref uuid NULL CHECK(actor_ref IS NULL),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER guardian_application_intake_release_events_immutable BEFORE UPDATE OR DELETE ON guardian_application_intake_release_events
 FOR EACH ROW EXECUTE FUNCTION prevent_guardian_authority_event_mutation();
INSERT INTO guardian_application_intake_release(singleton,gate_version,enabled) VALUES(true,'guardian-intake-v2',false);
INSERT INTO guardian_application_intake_release_events(gate_version,action,actor_ref) VALUES('guardian-intake-v2','CREATED_DISABLED',NULL);

CREATE TABLE guardian_authority_invitations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 public_ref uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
 invited_email citext NULL,
 token_digest bytea NOT NULL UNIQUE CHECK(octet_length(token_digest)=32),
 issued_by uuid NOT NULL,
 issued_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL,
 revoked_by uuid NULL,
 revoked_at timestamptz NULL,
 consumed_by uuid NULL,
 consumed_relationship_id uuid NULL REFERENCES guardian_authority_relationships(id) ON DELETE RESTRICT,
 consumed_at timestamptz NULL,
 CHECK(invited_email IS NULL OR (invited_email=btrim(invited_email::text)::citext AND char_length(invited_email::text) BETWEEN 3 AND 254)),
 CHECK(expires_at>issued_at),
 CHECK((revoked_at IS NULL AND revoked_by IS NULL) OR (revoked_at IS NOT NULL AND revoked_by IS NOT NULL)),
 CHECK((consumed_at IS NULL AND consumed_by IS NULL AND consumed_relationship_id IS NULL)
    OR (consumed_at IS NOT NULL AND consumed_by IS NOT NULL AND consumed_relationship_id IS NOT NULL)),
 CHECK(NOT (revoked_at IS NOT NULL AND consumed_at IS NOT NULL)),
 CHECK((revoked_at IS NULL AND consumed_at IS NULL) OR invited_email IS NULL)
);
CREATE INDEX guardian_authority_invitations_email_idx ON guardian_authority_invitations(invited_email,expires_at)
 WHERE revoked_at IS NULL AND consumed_at IS NULL;

CREATE TABLE guardian_application_rate_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 bucket_kind varchar(40) NOT NULL CHECK(bucket_kind IN('SUBMISSION_ACCOUNT','SUBMISSION_NETWORK','AUTH_ACCOUNT','AUTH_NETWORK','INVITATION_ACCOUNT','INVITATION_NETWORK')),
 bucket_digest bytea NOT NULL CHECK(octet_length(bucket_digest)=32),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX guardian_application_rate_events_bucket_idx ON guardian_application_rate_events(bucket_kind,bucket_digest,occurred_at);
CREATE INDEX guardian_application_rate_events_expiry_idx ON guardian_application_rate_events(occurred_at);

CREATE FUNCTION guardian_authority_issue_invitation(p_actor_id uuid,p_email citext,p_token_digest bytea)
RETURNS SETOF guardian_authority_invitations LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE result guardian_authority_invitations%ROWTYPE;now_at timestamptz:=clock_timestamp();
BEGIN
 IF NOT guardian_authority_is_administrator(p_actor_id) THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_authority_administrator_required'; END IF;
 IF p_email IS NULL OR p_email::text<>lower(btrim(p_email::text)) OR char_length(p_email::text) NOT BETWEEN 3 AND 254
  OR octet_length(p_token_digest)<>32 THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_invitation_rejected'; END IF;
 INSERT INTO guardian_authority_invitations(invited_email,token_digest,issued_by,issued_at,expires_at)
 VALUES(p_email,p_token_digest,p_actor_id,now_at,now_at+interval '30 days') RETURNING * INTO result;
 RETURN NEXT result;
END;$$;

CREATE FUNCTION guardian_authority_revoke_invitation(p_actor_id uuid,p_public_ref uuid)
RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE changed bigint;
BEGIN
 IF NOT guardian_authority_is_administrator(p_actor_id) THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_authority_administrator_required'; END IF;
 UPDATE guardian_authority_invitations SET invited_email=NULL,revoked_by=p_actor_id,revoked_at=clock_timestamp()
 WHERE public_ref=p_public_ref AND revoked_at IS NULL AND consumed_at IS NULL AND expires_at>clock_timestamp();
 GET DIAGNOSTICS changed=ROW_COUNT; RETURN changed;
END;$$;

CREATE FUNCTION guardian_authority_list_invitations(p_actor_id uuid,p_row_limit integer)
RETURNS SETOF guardian_authority_invitations LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF NOT guardian_authority_is_administrator(p_actor_id) OR p_row_limit NOT BETWEEN 1 AND 100 THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_authority_administrator_required';END IF;
 UPDATE guardian_authority_invitations SET invited_email=NULL
 WHERE invited_email IS NOT NULL AND revoked_at IS NULL AND consumed_at IS NULL AND expires_at<=clock_timestamp();
 RETURN QUERY SELECT invitation.* FROM guardian_authority_invitations invitation ORDER BY invitation.issued_at DESC,invitation.id DESC LIMIT p_row_limit;
END;$$;

CREATE FUNCTION guardian_application_reserve(p_account_digest bytea,p_network_digest bytea,p_kind text)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE now_at timestamptz:=clock_timestamp();window_size interval;account_kind text;network_kind text;account_limit integer;network_limit integer;
BEGIN
 IF octet_length(p_account_digest)<>32 OR octet_length(p_network_digest)<>32 THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_application_bucket_rejected'; END IF;
 CASE p_kind
  WHEN 'SUBMISSION' THEN window_size:=interval '24 hours';account_kind:='SUBMISSION_ACCOUNT';network_kind:='SUBMISSION_NETWORK';account_limit:=10;network_limit:=100;
  WHEN 'AUTH_INVALID' THEN window_size:=interval '15 minutes';account_kind:='AUTH_ACCOUNT';network_kind:='AUTH_NETWORK';account_limit:=10;network_limit:=10;
  WHEN 'INVITATION_INVALID' THEN window_size:=interval '15 minutes';account_kind:='INVITATION_ACCOUNT';network_kind:='INVITATION_NETWORK';account_limit:=10;network_limit:=10;
  ELSE RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_application_kind_rejected';
 END CASE;
 PERFORM pg_advisory_xact_lock(hashtextextended(account_kind||encode(p_account_digest,'hex'),0));
 PERFORM pg_advisory_xact_lock(hashtextextended(network_kind||encode(p_network_digest,'hex'),0));
 DELETE FROM guardian_application_rate_events WHERE occurred_at<=now_at-interval '24 hours';
 IF (SELECT count(*) FROM guardian_application_rate_events WHERE bucket_kind=account_kind AND bucket_digest=p_account_digest AND occurred_at>now_at-window_size)>=account_limit
  OR (SELECT count(*) FROM guardian_application_rate_events WHERE bucket_kind=network_kind AND bucket_digest=p_network_digest AND occurred_at>now_at-window_size)>=network_limit THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_application_rate_limited'; END IF;
 INSERT INTO guardian_application_rate_events(bucket_kind,bucket_digest,occurred_at)
 VALUES(account_kind,p_account_digest,now_at),(network_kind,p_network_digest,now_at);
END;$$;

CREATE FUNCTION guardian_application_prune() RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE changed bigint;
BEGIN
 UPDATE guardian_authority_invitations SET invited_email=NULL
 WHERE invited_email IS NOT NULL AND revoked_at IS NULL AND consumed_at IS NULL AND expires_at<=clock_timestamp();
 DELETE FROM guardian_application_rate_events WHERE occurred_at<=clock_timestamp()-interval '24 hours';
 GET DIAGNOSTICS changed=ROW_COUNT;RETURN changed;
END;$$;

DROP FUNCTION guardian_authority_create_dependent(text,date,uuid);
CREATE FUNCTION guardian_authority_create_dependent(p_name text,p_date_of_birth date,p_guardian_user_id uuid,p_invitation_digest bytea)
RETURNS SETOF users LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE guardian_row users%ROWTYPE;subject_row users%ROWTYPE;relationship_id uuid;now_at timestamptz:=clock_timestamp();invitation guardian_authority_invitations%ROWTYPE;has_membership boolean;
BEGIN
 PERFORM pg_advisory_xact_lock(110,110);
 IF NOT EXISTS(SELECT 1 FROM guardian_application_intake_release WHERE singleton AND enabled AND gate_version='guardian-intake-v2') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_application_intake_unreleased'; END IF;
 IF (SELECT count(*) FROM guardian_authority_policies WHERE enabled AND adopted_at<=now_at)<>1 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_policy_unavailable'; END IF;
 SELECT * INTO guardian_row FROM users WHERE id=p_guardian_user_id FOR UPDATE;
 IF NOT FOUND OR NOT guardian_row.is_active OR guardian_row.erased_at IS NOT NULL OR guardian_row.is_dependent
  OR guardian_row.email IS NULL OR guardian_row.email_verified_at IS NULL
  OR guardian_row.date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date THEN
  RETURN; END IF;
 SELECT EXISTS(SELECT 1 FROM user_memberships membership WHERE membership.user_id=guardian_row.id
  AND membership.starts_on<=CURRENT_DATE AND (membership.ends_on IS NULL OR membership.ends_on>=CURRENT_DATE)) INTO has_membership;
 IF p_invitation_digest IS NOT NULL THEN
  IF octet_length(p_invitation_digest)<>32 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_invitation_invalid'; END IF;
  SELECT * INTO invitation FROM guardian_authority_invitations WHERE token_digest=p_invitation_digest FOR UPDATE;
  IF NOT FOUND OR invitation.invited_email IS NULL OR invitation.invited_email<>guardian_row.email OR invitation.revoked_at IS NOT NULL OR invitation.consumed_at IS NOT NULL OR invitation.expires_at<=now_at THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_invitation_invalid'; END IF;
 ELSIF NOT has_membership THEN
  RETURN;
 END IF;
 IF p_name IS NULL OR p_name<>btrim(p_name) OR char_length(p_name) NOT BETWEEN 2 AND 120
  OR p_date_of_birth IS NULL OR p_date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date)
  OR p_date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_subject_rejected'; END IF;
 IF (SELECT count(*) FROM guardian_authority_relationships r JOIN users u ON u.id=r.subject_user_id
     WHERE r.guardian_user_id=p_guardian_user_id AND r.state NOT IN('EXPIRED','REJECTED') AND u.is_active AND u.erased_at IS NULL)>=10 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_limit_reached'; END IF;
 INSERT INTO users(name,email,password_hash,guardian_id,is_dependent,date_of_birth)
 VALUES(p_name,NULL,NULL,NULL,true,p_date_of_birth) RETURNING * INTO subject_row;
 INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label,state,created_at,updated_at)
 VALUES(p_guardian_user_id,subject_row.id,p_name,'PENDING',now_at,now_at) RETURNING id INTO relationship_id;
 INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,occurred_at)
 VALUES(relationship_id,1,p_guardian_user_id,'GUARDIAN','DECLARED',NULL,'PENDING',now_at);
 IF invitation.id IS NOT NULL THEN
  UPDATE guardian_authority_invitations SET invited_email=NULL,consumed_by=p_guardian_user_id,consumed_relationship_id=relationship_id,consumed_at=now_at WHERE id=invitation.id;
 END IF;
 RETURN NEXT subject_row;
END;$$;



REVOKE ALL ON TABLE guardian_application_intake_release,guardian_application_intake_release_events,
 guardian_authority_invitations,guardian_application_rate_events FROM PUBLIC;
DO $$BEGIN
 IF to_regrole('mycfc_app') IS NOT NULL THEN
  EXECUTE 'REVOKE ALL PRIVILEGES ON TABLE guardian_authority_invitations,guardian_application_rate_events,guardian_application_intake_release,guardian_application_intake_release_events FROM mycfc_app';
 END IF;
END$$;
REVOKE ALL ON FUNCTION guardian_authority_issue_invitation(uuid,citext,bytea),guardian_authority_revoke_invitation(uuid,uuid),
 guardian_authority_list_invitations(uuid,integer),guardian_application_reserve(bytea,bytea,text),guardian_application_prune(),guardian_authority_create_dependent(text,date,uuid,bytea) FROM PUBLIC;

-- This schema revision invalidates every prior privacy activation artifact.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp() WHERE singleton;
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at)
 SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton;
