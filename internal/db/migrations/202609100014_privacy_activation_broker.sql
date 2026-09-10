-- #248 trusted activation boundary. The web and executor roles remain unable
-- to create evidence, propose, approve, enable, or clear the kill switch.

ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_check;
ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_check CHECK((
 (kind='RESTORE' AND signing_key_id IS NULL AND immutable_evidence_ref IS NOT NULL AND schema_migration_digest IS NOT NULL AND baseline_includes_through IS NULL
  AND restore_input_source IN ('LIVE_LEDGER','SYNTHETIC_BOOTSTRAP') AND restore_input_contract='mycfc/privacy-restore-ledger-input/v2'
  AND restore_replay_contract='relational-erasure-replay/v1' AND restore_closure_contract='restore-tombstone-closure/v3'
  AND restore_candidate_sha256 IS NOT NULL AND octet_length(restore_candidate_sha256)=32 AND restore_inventory_sha256 IS NOT NULL
  AND octet_length(restore_inventory_sha256)=32 AND restore_observer_sha256 IS NOT NULL AND octet_length(restore_observer_sha256)=32
  AND restore_object_count>=restore_replayed_count AND restore_replayed_count>0
  AND ((restore_input_source='LIVE_LEDGER' AND restore_synthetic_count=0) OR (restore_input_source='SYNTHETIC_BOOTSTRAP' AND restore_synthetic_count=restore_replayed_count)))
 OR (kind='INFRASTRUCTURE' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL AND production_state_serial>0 AND hetzner_state_serial>0
  AND octet_length(production_state_sha256)=32 AND octet_length(hetzner_state_sha256)=32 AND octet_length(production_plan_sha256)=32
  AND octet_length(hetzner_plan_sha256)=32 AND worker_identity_enabled AND s3_version_deletion_enabled AND ledger_broker_invoke_enabled
  AND worker_monitoring_enabled AND restore_infrastructure_enabled AND restore_ledger_write_enabled)
 OR (kind='PROVIDER' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL AND provider_registry_state='READY'
  AND provider_registration_count>0 AND octet_length(provider_registry_sha256)=32)
 OR (kind='SCHEMA' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL AND octet_length(schema_migration_digest)=32
  AND baseline_includes_through='202609100014_privacy_activation_broker')) IS TRUE);

CREATE TABLE privacy_protected.activation_signed_approvals (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 proposal_id uuid NOT NULL REFERENCES privacy_activation_proposals(id) ON DELETE RESTRICT,
 signer_role varchar(20) NOT NULL CHECK(signer_role IN ('EXECUTOR','ADMINISTRATOR')),
 actor_ref uuid NOT NULL,
 signing_key_id varchar(120) NOT NULL CHECK(signing_key_id=btrim(signing_key_id) AND signing_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'),
 nonce_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(nonce_sha256)=32),
 envelope_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(envelope_sha256)=32),
 raw_envelope bytea NOT NULL CHECK(octet_length(raw_envelope) BETWEEN 1 AND 16384),
 parsed_envelope jsonb NOT NULL,
 issued_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(proposal_id,signer_role),
 CHECK(expires_at>issued_at AND expires_at<=issued_at+interval '15 minutes')
);
CREATE TRIGGER privacy_activation_signed_approvals_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.activation_signed_approvals FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

CREATE TABLE privacy_protected.activation_broker_receipts (
 proposal_id uuid PRIMARY KEY REFERENCES privacy_activation_proposals(id) ON DELETE RESTRICT,
 approval_id uuid NOT NULL UNIQUE REFERENCES privacy_activation_approvals(id) ON DELETE RESTRICT,
 executor_envelope_id uuid NOT NULL UNIQUE REFERENCES privacy_protected.activation_signed_approvals(id) ON DELETE RESTRICT,
 administrator_envelope_id uuid NOT NULL UNIQUE REFERENCES privacy_protected.activation_signed_approvals(id) ON DELETE RESTRICT,
 evidence_set_sha256 bytea NOT NULL CHECK(octet_length(evidence_set_sha256)=32),
 activation_sha256 bytea NOT NULL CHECK(octet_length(activation_sha256)=32),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER privacy_activation_broker_receipts_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.activation_broker_receipts FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

-- Renaming SECURITY DEFINER functions preserves their ACL. Remove every
-- inherited EXECUTE capability from every non-owner, including PUBLIC, and
-- assert that the cleanup really took effect before this migration commits.
DO $$ DECLARE capability record; grantee_name text;
BEGIN
 FOR capability IN
  SELECT proc.oid,proc.proowner,acl.grantee
  FROM pg_proc proc JOIN pg_namespace namespace ON namespace.oid=proc.pronamespace
  CROSS JOIN LATERAL aclexplode(COALESCE(proc.proacl,acldefault('f',proc.proowner))) acl
  WHERE namespace.nspname='public' AND proc.proname LIKE '%\_inner\_013' ESCAPE '\'
   AND acl.privilege_type='EXECUTE' AND acl.grantee<>proc.proowner
 LOOP
  grantee_name:=CASE WHEN capability.grantee=0 THEN 'PUBLIC' ELSE quote_ident((SELECT rolname FROM pg_roles WHERE oid=capability.grantee)) END;
  IF grantee_name IS NOT NULL THEN
   EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM %s',capability.oid::regprocedure,grantee_name);
  END IF;
 END LOOP;
 IF EXISTS(
  SELECT 1 FROM pg_proc proc JOIN pg_namespace namespace ON namespace.oid=proc.pronamespace
  CROSS JOIN LATERAL aclexplode(COALESCE(proc.proacl,acldefault('f',proc.proowner))) acl
  WHERE namespace.nspname='public' AND proc.proname LIKE '%\_inner\_013' ESCAPE '\'
   AND acl.privilege_type='EXECUTE' AND acl.grantee<>proc.proowner
 ) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_inner_capability_revoke_failed'; END IF;
END $$;

-- No ordinary application identity may call the raw mutation primitives.
DO $$ DECLARE capability record; grantee_name text;
BEGIN
 FOR capability IN
  SELECT proc.oid,proc.proowner,acl.grantee
  FROM pg_proc proc JOIN pg_namespace namespace ON namespace.oid=proc.pronamespace
  CROSS JOIN LATERAL aclexplode(COALESCE(proc.proacl,acldefault('f',proc.proowner))) acl
  WHERE namespace.nspname='public' AND proc.proname IN (
   'privacy_activation_record_evidence','privacy_activation_record_authenticated_evidence',
   'privacy_activation_propose','privacy_activation_approve','privacy_activation_authenticated_set_digest')
   AND acl.privilege_type='EXECUTE' AND acl.grantee<>proc.proowner
 LOOP
  grantee_name:=CASE WHEN capability.grantee=0 THEN 'PUBLIC' ELSE quote_ident((SELECT rolname FROM pg_roles WHERE oid=capability.grantee)) END;
  IF grantee_name IS NOT NULL THEN
   EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM %s',capability.oid::regprocedure,grantee_name);
  END IF;
 END LOOP;
END $$;

CREATE FUNCTION privacy_activation_broker_record_authenticated_evidence(
 p_actor uuid,p_kind text,p_evidence_sha256 bytea,p_reference_code text,p_observed_at timestamptz,p_expires_at timestamptz,p_artifact jsonb
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF session_user<>'mycfc_privacy_activation_broker' THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_broker_required';
 END IF;
 RETURN privacy_activation_record_authenticated_evidence(p_actor,p_kind,p_evidence_sha256,p_reference_code,p_observed_at,p_expires_at,p_artifact);
END;$$;

CREATE FUNCTION privacy_activation_broker_material(p_policy_version text)
RETURNS TABLE(evidence_ids uuid[],evidence_set_sha256 bytea,activation_sha256 bytea,executor_version text,plan_schema_version text,image_digest text,schema_migration_digest bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE policy privacy_request_policies%ROWTYPE;selected_ids uuid[];set_digest bytea;
BEGIN
 IF session_user<>'mycfc_privacy_activation_broker' THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_broker_required';
 END IF;
 SELECT * INTO policy FROM privacy_request_policies WHERE version=p_policy_version AND adopted_at IS NOT NULL FOR SHARE;
 SELECT array_agg(latest.id ORDER BY latest.kind) INTO selected_ids FROM (
  SELECT DISTINCT ON(e.kind) e.id,e.kind FROM privacy_activation_evidence e
  JOIN privacy_activation_authenticated_artifacts a ON a.evidence_id=e.id AND a.kind=e.kind
  WHERE a.policy_version=p_policy_version AND e.expires_at>clock_timestamp()
  ORDER BY e.kind,e.observed_at DESC,e.id DESC
 ) latest;
 set_digest:=privacy_activation_authenticated_set_digest(p_policy_version,selected_ids);
 IF policy.version IS NULL OR set_digest IS NULL THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_evidence_incomplete';
 END IF;
 RETURN QUERY SELECT selected_ids,set_digest,
  digest(convert_to('privacy-activation/v2:'||policy.version||':'||policy.executor_version||':'||policy.plan_schema_version||':'||
   encode(digest(convert_to(policy.category_catalogue::text,'UTF8'),'sha256'),'hex')||':'||encode(set_digest,'hex'),'UTF8'),'sha256'),
  policy.executor_version::text,policy.plan_schema_version::text,
  (SELECT a.image_digest FROM privacy_activation_authenticated_artifacts a WHERE a.evidence_id=selected_ids[1]),
  (SELECT a.schema_migration_digest FROM privacy_activation_authenticated_artifacts a WHERE a.evidence_id=ANY(selected_ids) AND a.kind='SCHEMA');
END;$$;

CREATE FUNCTION privacy_activation_broker_activate(
 p_proposal_id uuid,p_policy_version text,p_evidence_ids uuid[],p_evidence_set_sha256 bytea,p_activation_sha256 bytea,
 p_executor_actor uuid,p_administrator_actor uuid,p_executor_key_id text,p_administrator_key_id text,
 p_executor_nonce_sha256 bytea,p_administrator_nonce_sha256 bytea,p_executor_envelope_raw bytea,p_administrator_envelope_raw bytea,
 p_executor_envelope jsonb,p_administrator_envelope jsonb,
 p_executor_issued_at timestamptz,p_executor_expires_at timestamptz,p_administrator_issued_at timestamptz,p_administrator_expires_at timestamptz
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE material record;now_at timestamptz:=clock_timestamp();executor_envelope_id uuid;administrator_envelope_id uuid;approval_id uuid;switch_version bigint;
BEGIN
 IF session_user<>'mycfc_privacy_activation_broker' THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_broker_required'; END IF;
 SELECT * INTO material FROM privacy_activation_broker_material(p_policy_version);
 IF p_proposal_id IS NULL OR p_executor_actor IS NULL OR p_administrator_actor IS NULL OR p_executor_actor=p_administrator_actor
  OR material.evidence_ids IS DISTINCT FROM p_evidence_ids OR material.evidence_set_sha256 IS DISTINCT FROM p_evidence_set_sha256
  OR material.activation_sha256 IS DISTINCT FROM p_activation_sha256 OR octet_length(p_executor_nonce_sha256)<>32 OR octet_length(p_administrator_nonce_sha256)<>32
  OR p_executor_nonce_sha256=p_administrator_nonce_sha256 OR p_executor_issued_at>now_at OR p_administrator_issued_at>now_at
  OR p_executor_expires_at<=now_at OR p_administrator_expires_at<=now_at
  OR p_executor_expires_at>p_executor_issued_at+interval '15 minutes' OR p_administrator_expires_at>p_administrator_issued_at+interval '15 minutes'
  OR jsonb_typeof(p_executor_envelope)<>'object' OR jsonb_typeof(p_administrator_envelope)<>'object'
  OR convert_from(p_executor_envelope_raw,'UTF8')::jsonb IS DISTINCT FROM p_executor_envelope
  OR convert_from(p_administrator_envelope_raw,'UTF8')::jsonb IS DISTINCT FROM p_administrator_envelope
  OR (SELECT array_agg(key ORDER BY key) FROM jsonb_object_keys(p_executor_envelope) key)<>ARRAY['activation_sha256','actor_ref','contract','evidence_ids','evidence_set_sha256','executor_version','expires_at','image_digest','issued_at','nonce','plan_schema_version','policy_version','proposal_id','role','schema_migration_digest','signature_ed25519','signing_key_id']
  OR (SELECT array_agg(key ORDER BY key) FROM jsonb_object_keys(p_administrator_envelope) key)<>ARRAY['activation_sha256','actor_ref','contract','evidence_ids','evidence_set_sha256','executor_version','expires_at','image_digest','issued_at','nonce','plan_schema_version','policy_version','proposal_id','role','schema_migration_digest','signature_ed25519','signing_key_id']
  OR p_executor_envelope->>'contract'<>'mycfc/privacy-activation-approval/v1' OR p_administrator_envelope->>'contract'<>'mycfc/privacy-activation-approval/v1'
  OR p_executor_envelope->>'role'<>'EXECUTOR' OR p_administrator_envelope->>'role'<>'ADMINISTRATOR'
  OR p_executor_envelope->>'proposal_id'<>p_proposal_id::text OR p_administrator_envelope->>'proposal_id'<>p_proposal_id::text
  OR p_executor_envelope->>'actor_ref'<>p_executor_actor::text OR p_administrator_envelope->>'actor_ref'<>p_administrator_actor::text
  OR p_executor_envelope->>'signing_key_id'<>p_executor_key_id OR p_administrator_envelope->>'signing_key_id'<>p_administrator_key_id
  OR (p_executor_envelope->>'issued_at')::timestamptz IS DISTINCT FROM p_executor_issued_at OR (p_administrator_envelope->>'issued_at')::timestamptz IS DISTINCT FROM p_administrator_issued_at
  OR (p_executor_envelope->>'expires_at')::timestamptz IS DISTINCT FROM p_executor_expires_at OR (p_administrator_envelope->>'expires_at')::timestamptz IS DISTINCT FROM p_administrator_expires_at
  OR digest(decode(p_executor_envelope->>'nonce','base64'),'sha256') IS DISTINCT FROM p_executor_nonce_sha256
  OR digest(decode(p_administrator_envelope->>'nonce','base64'),'sha256') IS DISTINCT FROM p_administrator_nonce_sha256
  OR octet_length(decode(p_executor_envelope->>'signature_ed25519','base64'))<>64 OR octet_length(decode(p_administrator_envelope->>'signature_ed25519','base64'))<>64
  OR p_executor_envelope->>'activation_sha256'<>encode(p_activation_sha256,'hex') OR p_administrator_envelope->>'activation_sha256'<>encode(p_activation_sha256,'hex')
  OR p_executor_envelope->>'evidence_set_sha256'<>encode(p_evidence_set_sha256,'hex') OR p_administrator_envelope->>'evidence_set_sha256'<>encode(p_evidence_set_sha256,'hex')
  OR p_executor_envelope->'evidence_ids'<>to_jsonb(p_evidence_ids) OR p_administrator_envelope->'evidence_ids'<>to_jsonb(p_evidence_ids)
  OR p_executor_envelope->>'policy_version'<>p_policy_version OR p_administrator_envelope->>'policy_version'<>p_policy_version
  OR p_executor_envelope->>'executor_version'<>material.executor_version OR p_administrator_envelope->>'executor_version'<>material.executor_version
  OR p_executor_envelope->>'plan_schema_version'<>material.plan_schema_version OR p_administrator_envelope->>'plan_schema_version'<>material.plan_schema_version
  OR p_executor_envelope->>'image_digest'<>material.image_digest OR p_administrator_envelope->>'image_digest'<>material.image_digest
  OR p_executor_envelope->>'schema_migration_digest'<>encode(material.schema_migration_digest,'hex') OR p_administrator_envelope->>'schema_migration_digest'<>encode(material.schema_migration_digest,'hex')
  OR NOT EXISTS(SELECT 1 FROM users u JOIN privacy_executor_grants g ON g.user_id=u.id AND g.revoked_at IS NULL WHERE u.id=p_executor_actor AND u.is_active AND NOT u.is_dependent)
  OR NOT EXISTS(SELECT 1 FROM users u JOIN user_platform_roles a ON a.user_id=u.id JOIN platform_roles r ON r.id=a.role_id WHERE u.id=p_administrator_actor AND u.is_active AND NOT u.is_dependent AND r.code='ADMIN')
 THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_signed_approval_rejected'; END IF;

 INSERT INTO privacy_activation_proposals(id,policy_version,evidence_ids,evidence_set_sha256,activation_sha256,proposed_by_ref,proposed_at)
 VALUES(p_proposal_id,p_policy_version,p_evidence_ids,p_evidence_set_sha256,p_activation_sha256,p_executor_actor,now_at);
 INSERT INTO privacy_protected.activation_signed_approvals(proposal_id,signer_role,actor_ref,signing_key_id,nonce_sha256,envelope_sha256,raw_envelope,parsed_envelope,issued_at,expires_at)
 VALUES(p_proposal_id,'EXECUTOR',p_executor_actor,p_executor_key_id,p_executor_nonce_sha256,digest(p_executor_envelope_raw,'sha256'),p_executor_envelope_raw,p_executor_envelope,p_executor_issued_at,p_executor_expires_at)
 RETURNING id INTO executor_envelope_id;
 INSERT INTO privacy_protected.activation_signed_approvals(proposal_id,signer_role,actor_ref,signing_key_id,nonce_sha256,envelope_sha256,raw_envelope,parsed_envelope,issued_at,expires_at)
 VALUES(p_proposal_id,'ADMINISTRATOR',p_administrator_actor,p_administrator_key_id,p_administrator_nonce_sha256,digest(p_administrator_envelope_raw,'sha256'),p_administrator_envelope_raw,p_administrator_envelope,p_administrator_issued_at,p_administrator_expires_at)
 RETURNING id INTO administrator_envelope_id;
 INSERT INTO privacy_activation_approvals(proposal_id,activation_sha256,approved_by_ref,approved_at)
 VALUES(p_proposal_id,p_activation_sha256,p_administrator_actor,now_at) RETURNING id INTO approval_id;
 PERFORM set_config('mycfc.privacy_activation_approval','approved',true);
 INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by,updated_at,approval_id)
 VALUES(true,p_policy_version,true,true,p_administrator_actor,now_at,approval_id)
 ON CONFLICT(singleton) DO UPDATE SET policy_version=EXCLUDED.policy_version,enabled=true,fulfilment_ready=true,updated_by=EXCLUDED.updated_by,updated_at=EXCLUDED.updated_at,approval_id=EXCLUDED.approval_id;
 INSERT INTO privacy_request_activation_events(policy_version,actor_ref,enabled,fulfilment_ready,occurred_at)
 VALUES(p_policy_version,p_administrator_actor,true,true,now_at);
 UPDATE privacy_worker_kill_switch SET engaged=false,version=version+1,activation_approval_id=approval_id,changed_at=now_at WHERE singleton RETURNING version INTO switch_version;
 INSERT INTO privacy_worker_kill_switch_events(version,engaged,activation_approval_id,occurred_at) VALUES(switch_version,false,approval_id,now_at);
 INSERT INTO privacy_protected.activation_broker_receipts(proposal_id,approval_id,executor_envelope_id,administrator_envelope_id,evidence_set_sha256,activation_sha256)
 VALUES(p_proposal_id,approval_id,executor_envelope_id,administrator_envelope_id,p_evidence_set_sha256,p_activation_sha256);
 RETURN approval_id;
EXCEPTION
 WHEN unique_violation THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_signed_approval_replay';
 WHEN invalid_text_representation OR character_not_in_repertoire OR datetime_field_overflow THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_signed_approval_rejected';
END;$$;

CREATE FUNCTION privacy_activation_disable(p_actor uuid) RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE now_at timestamptz:=clock_timestamp();switch_version bigint;
BEGIN
 IF session_user<>'mycfc_privacy_activation_disable' THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_disable_role_required'; END IF;
 UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=now_at
 WHERE singleton AND NOT engaged RETURNING version INTO switch_version;
 IF switch_version IS NOT NULL THEN
  INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at) VALUES(switch_version,true,now_at);
 ELSE
  SELECT version INTO switch_version FROM privacy_worker_kill_switch WHERE singleton;
 END IF;
 UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_by=p_actor,updated_at=now_at WHERE singleton;
 INSERT INTO privacy_request_activation_events(policy_version,actor_ref,enabled,fulfilment_ready,occurred_at)
 SELECT policy_version,p_actor,false,false,now_at FROM privacy_request_activation WHERE singleton;
 RETURN switch_version;
END;$$;

REVOKE ALL ON TABLE privacy_protected.activation_signed_approvals,privacy_protected.activation_broker_receipts FROM PUBLIC;
REVOKE ALL ON FUNCTION
 privacy_activation_broker_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb),
 privacy_activation_broker_material(text),
 privacy_activation_broker_activate(uuid,text,uuid[],bytea,bytea,uuid,uuid,text,text,bytea,bytea,bytea,bytea,jsonb,jsonb,timestamptz,timestamptz,timestamptz,timestamptz),
 privacy_activation_disable(uuid)
FROM PUBLIC;

-- Every schema upgrade re-engages the switch. Activation must be renewed
-- against the new embedded migration digest by the independent broker path.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp() WHERE singleton;
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at)
 SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton;
