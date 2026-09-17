-- Replace the file-staged v1 approval with a registered, release-bound,
-- 15-minute ceremony and two registry-pinned KMS P-256 approvals.
DO $$DECLARE definition text;rewritten text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass
  AND conname='privacy_activation_authenticated_artifacts_v20_check';
 IF definition IS NULL OR length(definition)-length(replace(definition,'202609170004_privacy_empty_provider_execution',''))<>length('202609170004_privacy_empty_provider_execution') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='dual_signer_privacy_constraint_predecessor_mismatch';
 END IF;
 rewritten:=replace(definition,
  '''202609170004_privacy_empty_provider_execution''',
  '''202609170004_privacy_empty_provider_execution'', ''202609170005_privacy_activation_dual_signer''');
 ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v20_check;
 EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v21_check '||rewritten||' NOT VALID';
 ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v21_check;
END$$;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609170004_privacy_empty_provider_execution''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609170005_privacy_activation_dual_signer''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='dual_signer_privacy_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609170004_privacy_empty_provider_execution''';
 new_clause:='schema_row.baseline_includes_through=''202609170005_privacy_activation_dual_signer''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='dual_signer_privacy_digest_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

CREATE TABLE privacy_protected.activation_ceremonies (
 ceremony_id uuid PRIMARY KEY,
 proposal_id uuid NOT NULL UNIQUE,
 source_sha varchar(40) NOT NULL CHECK(source_sha~'^[0-9a-f]{40}$'),
 policy_version varchar(120) NOT NULL,
 evidence_ids uuid[] NOT NULL CHECK(cardinality(evidence_ids)=4),
 evidence_set_sha256 bytea NOT NULL CHECK(octet_length(evidence_set_sha256)=32),
 activation_sha256 bytea NOT NULL CHECK(octet_length(activation_sha256)=32),
 executor_version varchar(80) NOT NULL,
 plan_schema_version varchar(80) NOT NULL,
 image_digest varchar(71) NOT NULL CHECK(image_digest~'^sha256:[0-9a-f]{64}$'),
 schema_migration_digest bytea NOT NULL CHECK(octet_length(schema_migration_digest)=32),
 signer_registry_sha256 bytea NOT NULL CHECK(octet_length(signer_registry_sha256)=32),
 material_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(material_sha256)=32),
 raw_material bytea NOT NULL CHECK(octet_length(raw_material) BETWEEN 1 AND 65536),
 parsed_material jsonb NOT NULL,
 prepared_at timestamptz NOT NULL,
 ceremony_expires_at timestamptz NOT NULL,
 executor_actor_ref uuid NOT NULL,
 executor_signing_key_id varchar(120) NOT NULL,
 executor_kms_key_arn text NOT NULL,
 executor_public_key_spki_sha256 bytea NOT NULL CHECK(octet_length(executor_public_key_spki_sha256)=32),
 executor_github_actor_id bigint NOT NULL CHECK(executor_github_actor_id>0),
 executor_github_environment text NOT NULL CHECK(executor_github_environment='privacy-activation-executor'),
 administrator_actor_ref uuid NOT NULL,
 administrator_signing_key_id varchar(120) NOT NULL,
 administrator_kms_key_arn text NOT NULL,
 administrator_public_key_spki_sha256 bytea NOT NULL CHECK(octet_length(administrator_public_key_spki_sha256)=32),
 administrator_github_actor_id bigint NOT NULL CHECK(administrator_github_actor_id>0),
 administrator_github_environment text NOT NULL CHECK(administrator_github_environment='privacy-activation-administrator'),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(ceremony_expires_at>prepared_at AND ceremony_expires_at<=prepared_at+interval '15 minutes'),
 CHECK(executor_actor_ref<>administrator_actor_ref),
 CHECK(executor_signing_key_id<>administrator_signing_key_id),
 CHECK(executor_kms_key_arn<>administrator_kms_key_arn),
 CHECK(executor_public_key_spki_sha256<>administrator_public_key_spki_sha256),
 CHECK(executor_github_actor_id<>administrator_github_actor_id)
);
CREATE TRIGGER privacy_activation_ceremonies_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.activation_ceremonies FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

ALTER TABLE privacy_protected.activation_signed_approvals
 ADD COLUMN ceremony_id uuid NULL REFERENCES privacy_protected.activation_ceremonies(ceremony_id) ON DELETE RESTRICT,
 ADD COLUMN material_sha256 bytea NULL CHECK(material_sha256 IS NULL OR octet_length(material_sha256)=32),
 ADD COLUMN signature_algorithm text NULL,
 ADD COLUMN kms_key_arn text NULL,
 ADD COLUMN public_key_spki_sha256 bytea NULL CHECK(public_key_spki_sha256 IS NULL OR octet_length(public_key_spki_sha256)=32),
 ADD COLUMN github_actor_id bigint NULL CHECK(github_actor_id IS NULL OR github_actor_id>0),
 ADD COLUMN github_environment text NULL,
 ADD COLUMN github_run_id bigint NULL CHECK(github_run_id IS NULL OR github_run_id>0),
 ADD COLUMN github_run_attempt bigint NULL CHECK(github_run_attempt IS NULL OR github_run_attempt>0),
 ADD COLUMN signature_der_sha256 bytea NULL CHECK(signature_der_sha256 IS NULL OR octet_length(signature_der_sha256)=32);

ALTER TABLE privacy_protected.activation_broker_receipts
 ADD COLUMN ceremony_id uuid NULL REFERENCES privacy_protected.activation_ceremonies(ceremony_id) ON DELETE RESTRICT;

REVOKE ALL ON FUNCTION public.privacy_activation_broker_activate(uuid,text,uuid[],bytea,bytea,uuid,uuid,text,text,bytea,bytea,bytea,bytea,jsonb,jsonb,timestamptz,timestamptz,timestamptz,timestamptz) FROM PUBLIC;
DROP FUNCTION public.privacy_activation_broker_activate(uuid,text,uuid[],bytea,bytea,uuid,uuid,text,text,bytea,bytea,bytea,bytea,jsonb,jsonb,timestamptz,timestamptz,timestamptz,timestamptz);

CREATE FUNCTION public.privacy_activation_broker_register_ceremony(
 p_ceremony_id uuid,p_proposal_id uuid,p_source_sha text,p_policy_version text,p_evidence_ids uuid[],
 p_evidence_set_sha256 bytea,p_activation_sha256 bytea,p_executor_version text,p_plan_schema_version text,p_image_digest text,
 p_schema_migration_digest bytea,p_signer_registry_sha256 bytea,p_material_sha256 bytea,p_material_raw bytea,p_material jsonb,
 p_prepared_at timestamptz,p_ceremony_expires_at timestamptz,
 p_executor_actor uuid,p_executor_key_id text,p_executor_kms_key_arn text,p_executor_public_key_sha256 bytea,
 p_executor_github_actor_id bigint,p_executor_github_environment text,
 p_administrator_actor uuid,p_administrator_key_id text,p_administrator_kms_key_arn text,p_administrator_public_key_sha256 bytea,
 p_administrator_github_actor_id bigint,p_administrator_github_environment text
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE material record;now_at timestamptz;
BEGIN
 IF session_user<>'mycfc_privacy_activation_broker' THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_broker_required'; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/privacy-activation',0));
 now_at:=clock_timestamp();
 SELECT * INTO material FROM privacy_activation_broker_material(p_policy_version);
 IF p_ceremony_id IS NULL OR p_proposal_id IS NULL OR p_ceremony_id=p_proposal_id
  OR p_source_sha!~'^[0-9a-f]{40}$' OR material.evidence_ids IS DISTINCT FROM p_evidence_ids
  OR material.evidence_set_sha256 IS DISTINCT FROM p_evidence_set_sha256 OR material.activation_sha256 IS DISTINCT FROM p_activation_sha256
  OR material.executor_version IS DISTINCT FROM p_executor_version OR material.plan_schema_version IS DISTINCT FROM p_plan_schema_version
  OR material.image_digest IS DISTINCT FROM p_image_digest OR material.schema_migration_digest IS DISTINCT FROM p_schema_migration_digest
  OR octet_length(p_signer_registry_sha256)<>32 OR octet_length(p_material_sha256)<>32
  OR digest(p_material_raw,'sha256') IS DISTINCT FROM p_material_sha256 OR convert_from(p_material_raw,'UTF8')::jsonb IS DISTINCT FROM p_material
  OR jsonb_typeof(p_material)<>'object'
  OR (SELECT array_agg(key ORDER BY key) FROM jsonb_object_keys(p_material) key)<>ARRAY[
   'activation_sha256','ceremony_expires_at','ceremony_id','contract','evidence_ids','evidence_set_sha256','executor_version',
   'image_digest','plan_schema_version','policy_version','prepared_at','proposal_id','schema_migration_digest','signer_registry_sha256','source_sha']
  OR p_material->>'contract'<>'mycfc/privacy-activation-approval-material/v2'
  OR p_material->>'ceremony_id'<>p_ceremony_id::text OR p_material->>'proposal_id'<>p_proposal_id::text
  OR p_material->>'source_sha'<>p_source_sha OR p_material->>'policy_version'<>p_policy_version
  OR p_material->'evidence_ids'<>to_jsonb(p_evidence_ids)
  OR p_material->>'evidence_set_sha256'<>encode(p_evidence_set_sha256,'hex')
  OR p_material->>'activation_sha256'<>encode(p_activation_sha256,'hex')
  OR p_material->>'executor_version'<>p_executor_version OR p_material->>'plan_schema_version'<>p_plan_schema_version
  OR p_material->>'image_digest'<>p_image_digest OR p_material->>'schema_migration_digest'<>encode(p_schema_migration_digest,'hex')
  OR p_material->>'signer_registry_sha256'<>encode(p_signer_registry_sha256,'hex')
  OR (p_material->>'prepared_at')::timestamptz IS DISTINCT FROM p_prepared_at
  OR (p_material->>'ceremony_expires_at')::timestamptz IS DISTINCT FROM p_ceremony_expires_at
  OR p_prepared_at>now_at OR p_ceremony_expires_at<=now_at OR p_ceremony_expires_at<=p_prepared_at
  OR p_ceremony_expires_at>p_prepared_at+interval '15 minutes'
  OR p_prepared_at<=(SELECT changed_at FROM privacy_worker_kill_switch WHERE singleton)
  OR EXISTS(SELECT 1 FROM privacy_activation_evidence evidence WHERE evidence.id=ANY(p_evidence_ids)
    AND evidence.recorded_at<=(SELECT changed_at FROM privacy_worker_kill_switch WHERE singleton))
  OR p_executor_actor IS NULL OR p_administrator_actor IS NULL OR p_executor_actor=p_administrator_actor
  OR p_executor_key_id IS NULL OR p_executor_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'
  OR p_administrator_key_id IS NULL OR p_administrator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'
  OR p_executor_key_id=p_administrator_key_id
  OR p_executor_kms_key_arn IS NULL OR p_executor_kms_key_arn!~'^arn:aws[a-zA-Z-]*:kms:[a-z0-9-]+:[0-9]{12}:key/[0-9a-fA-F-]{36}$'
  OR p_administrator_kms_key_arn IS NULL OR p_administrator_kms_key_arn!~'^arn:aws[a-zA-Z-]*:kms:[a-z0-9-]+:[0-9]{12}:key/[0-9a-fA-F-]{36}$'
  OR p_executor_kms_key_arn=p_administrator_kms_key_arn
  OR octet_length(p_executor_public_key_sha256)<>32 OR octet_length(p_administrator_public_key_sha256)<>32
  OR p_executor_public_key_sha256=p_administrator_public_key_sha256
  OR p_executor_github_actor_id<=0 OR p_administrator_github_actor_id<=0 OR p_executor_github_actor_id=p_administrator_github_actor_id
  OR p_executor_github_environment<>'privacy-activation-executor'
  OR p_administrator_github_environment<>'privacy-activation-administrator'
  OR NOT EXISTS(SELECT 1 FROM users u JOIN privacy_executor_grants g ON g.user_id=u.id AND g.revoked_at IS NULL
    WHERE u.id=p_executor_actor AND u.is_active AND NOT u.is_dependent)
  OR NOT EXISTS(SELECT 1 FROM users u JOIN user_platform_roles a ON a.user_id=u.id JOIN platform_roles r ON r.id=a.role_id
    WHERE u.id=p_administrator_actor AND u.is_active AND NOT u.is_dependent AND r.code='ADMIN')
 THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_ceremony_rejected'; END IF;

 INSERT INTO privacy_protected.activation_ceremonies(
  ceremony_id,proposal_id,source_sha,policy_version,evidence_ids,evidence_set_sha256,activation_sha256,executor_version,
  plan_schema_version,image_digest,schema_migration_digest,signer_registry_sha256,material_sha256,raw_material,parsed_material,
  prepared_at,ceremony_expires_at,executor_actor_ref,executor_signing_key_id,executor_kms_key_arn,
  executor_public_key_spki_sha256,executor_github_actor_id,executor_github_environment,administrator_actor_ref,
  administrator_signing_key_id,administrator_kms_key_arn,administrator_public_key_spki_sha256,
  administrator_github_actor_id,administrator_github_environment)
 VALUES(p_ceremony_id,p_proposal_id,p_source_sha,p_policy_version,p_evidence_ids,p_evidence_set_sha256,p_activation_sha256,
  p_executor_version,p_plan_schema_version,p_image_digest,p_schema_migration_digest,p_signer_registry_sha256,p_material_sha256,
  p_material_raw,p_material,p_prepared_at,p_ceremony_expires_at,p_executor_actor,p_executor_key_id,p_executor_kms_key_arn,
  p_executor_public_key_sha256,p_executor_github_actor_id,p_executor_github_environment,p_administrator_actor,
  p_administrator_key_id,p_administrator_kms_key_arn,p_administrator_public_key_sha256,p_administrator_github_actor_id,
  p_administrator_github_environment);
 RETURN p_ceremony_id;
EXCEPTION
 WHEN unique_violation THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_ceremony_replay';
 WHEN invalid_text_representation OR character_not_in_repertoire OR datetime_field_overflow THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_ceremony_rejected';
END;$$;

CREATE FUNCTION public.privacy_activation_broker_activate(
 p_ceremony_id uuid,p_executor_envelope_raw bytea,p_executor_envelope jsonb,
 p_administrator_envelope_raw bytea,p_administrator_envelope jsonb,
 p_executor_nonce_sha256 bytea,p_administrator_nonce_sha256 bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE ceremony privacy_protected.activation_ceremonies%ROWTYPE;material record;now_at timestamptz;
 executor_envelope_id uuid;administrator_envelope_id uuid;approval_id uuid;switch_version bigint;
 executor_issued_at timestamptz;executor_expires_at timestamptz;administrator_issued_at timestamptz;administrator_expires_at timestamptz;
BEGIN
 IF session_user<>'mycfc_privacy_activation_broker' THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_broker_required'; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/privacy-activation',0));
 now_at:=clock_timestamp();
 SELECT * INTO ceremony FROM privacy_protected.activation_ceremonies row WHERE row.ceremony_id=p_ceremony_id FOR SHARE;
 IF ceremony.ceremony_id IS NULL THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_signed_approval_rejected'; END IF;
 SELECT * INTO material FROM privacy_activation_broker_material(ceremony.policy_version);
 executor_issued_at:=(p_executor_envelope->>'issued_at')::timestamptz;
 executor_expires_at:=(p_executor_envelope->>'expires_at')::timestamptz;
 administrator_issued_at:=(p_administrator_envelope->>'issued_at')::timestamptz;
 administrator_expires_at:=(p_administrator_envelope->>'expires_at')::timestamptz;
 IF ceremony.ceremony_expires_at<=now_at OR ceremony.prepared_at<=(SELECT changed_at FROM privacy_worker_kill_switch WHERE singleton)
  OR material.evidence_ids IS DISTINCT FROM ceremony.evidence_ids
  OR material.evidence_set_sha256 IS DISTINCT FROM ceremony.evidence_set_sha256
  OR material.activation_sha256 IS DISTINCT FROM ceremony.activation_sha256
  OR material.executor_version IS DISTINCT FROM ceremony.executor_version
  OR material.plan_schema_version IS DISTINCT FROM ceremony.plan_schema_version
  OR material.image_digest IS DISTINCT FROM ceremony.image_digest
  OR material.schema_migration_digest IS DISTINCT FROM ceremony.schema_migration_digest
  OR EXISTS(SELECT 1 FROM privacy_activation_evidence evidence WHERE evidence.id=ANY(ceremony.evidence_ids)
    AND evidence.recorded_at<=(SELECT changed_at FROM privacy_worker_kill_switch WHERE singleton))
  OR octet_length(p_executor_nonce_sha256)<>32 OR octet_length(p_administrator_nonce_sha256)<>32
  OR p_executor_nonce_sha256=p_administrator_nonce_sha256
  OR jsonb_typeof(p_executor_envelope)<>'object' OR jsonb_typeof(p_administrator_envelope)<>'object'
  OR convert_from(p_executor_envelope_raw,'UTF8')::jsonb IS DISTINCT FROM p_executor_envelope
  OR convert_from(p_administrator_envelope_raw,'UTF8')::jsonb IS DISTINCT FROM p_administrator_envelope
  OR (SELECT array_agg(key ORDER BY key) FROM jsonb_object_keys(p_executor_envelope) key)<>ARRAY[
   'activation_sha256','actor_ref','ceremony_expires_at','ceremony_id','contract','evidence_ids','evidence_set_sha256',
   'executor_version','expires_at','github_actor_id','github_environment','github_run_attempt','github_run_id','image_digest',
   'issued_at','kms_key_arn','material_sha256','nonce','plan_schema_version','policy_version','prepared_at','proposal_id',
   'public_key_spki_sha256','role','schema_migration_digest','signature_algorithm','signature_der_base64',
   'signer_registry_sha256','signing_key_id','source_sha']
  OR (SELECT array_agg(key ORDER BY key) FROM jsonb_object_keys(p_administrator_envelope) key)<>ARRAY[
   'activation_sha256','actor_ref','ceremony_expires_at','ceremony_id','contract','evidence_ids','evidence_set_sha256',
   'executor_version','expires_at','github_actor_id','github_environment','github_run_attempt','github_run_id','image_digest',
   'issued_at','kms_key_arn','material_sha256','nonce','plan_schema_version','policy_version','prepared_at','proposal_id',
   'public_key_spki_sha256','role','schema_migration_digest','signature_algorithm','signature_der_base64',
   'signer_registry_sha256','signing_key_id','source_sha']
  OR p_executor_envelope->>'contract'<>'mycfc/privacy-activation-approval/v2'
  OR p_administrator_envelope->>'contract'<>'mycfc/privacy-activation-approval/v2'
  OR p_executor_envelope->>'role'<>'EXECUTOR' OR p_administrator_envelope->>'role'<>'ADMINISTRATOR'
  OR p_executor_envelope->>'ceremony_id'<>ceremony.ceremony_id::text
  OR p_administrator_envelope->>'ceremony_id'<>ceremony.ceremony_id::text
  OR p_executor_envelope->>'proposal_id'<>ceremony.proposal_id::text
  OR p_administrator_envelope->>'proposal_id'<>ceremony.proposal_id::text
  OR p_executor_envelope->>'source_sha'<>ceremony.source_sha OR p_administrator_envelope->>'source_sha'<>ceremony.source_sha
  OR p_executor_envelope->>'policy_version'<>ceremony.policy_version OR p_administrator_envelope->>'policy_version'<>ceremony.policy_version
  OR p_executor_envelope->'evidence_ids'<>to_jsonb(ceremony.evidence_ids)
  OR p_administrator_envelope->'evidence_ids'<>to_jsonb(ceremony.evidence_ids)
  OR p_executor_envelope->>'evidence_set_sha256'<>encode(ceremony.evidence_set_sha256,'hex')
  OR p_administrator_envelope->>'evidence_set_sha256'<>encode(ceremony.evidence_set_sha256,'hex')
  OR p_executor_envelope->>'activation_sha256'<>encode(ceremony.activation_sha256,'hex')
  OR p_administrator_envelope->>'activation_sha256'<>encode(ceremony.activation_sha256,'hex')
  OR p_executor_envelope->>'executor_version'<>ceremony.executor_version OR p_administrator_envelope->>'executor_version'<>ceremony.executor_version
  OR p_executor_envelope->>'plan_schema_version'<>ceremony.plan_schema_version OR p_administrator_envelope->>'plan_schema_version'<>ceremony.plan_schema_version
  OR p_executor_envelope->>'image_digest'<>ceremony.image_digest OR p_administrator_envelope->>'image_digest'<>ceremony.image_digest
  OR p_executor_envelope->>'schema_migration_digest'<>encode(ceremony.schema_migration_digest,'hex')
  OR p_administrator_envelope->>'schema_migration_digest'<>encode(ceremony.schema_migration_digest,'hex')
  OR p_executor_envelope->>'signer_registry_sha256'<>encode(ceremony.signer_registry_sha256,'hex')
  OR p_administrator_envelope->>'signer_registry_sha256'<>encode(ceremony.signer_registry_sha256,'hex')
  OR p_executor_envelope->>'material_sha256'<>encode(ceremony.material_sha256,'hex')
  OR p_administrator_envelope->>'material_sha256'<>encode(ceremony.material_sha256,'hex')
  OR (p_executor_envelope->>'prepared_at')::timestamptz IS DISTINCT FROM ceremony.prepared_at
  OR (p_administrator_envelope->>'prepared_at')::timestamptz IS DISTINCT FROM ceremony.prepared_at
  OR (p_executor_envelope->>'ceremony_expires_at')::timestamptz IS DISTINCT FROM ceremony.ceremony_expires_at
  OR (p_administrator_envelope->>'ceremony_expires_at')::timestamptz IS DISTINCT FROM ceremony.ceremony_expires_at
  OR p_executor_envelope->>'actor_ref'<>ceremony.executor_actor_ref::text
  OR p_administrator_envelope->>'actor_ref'<>ceremony.administrator_actor_ref::text
  OR p_executor_envelope->>'signing_key_id'<>ceremony.executor_signing_key_id
  OR p_administrator_envelope->>'signing_key_id'<>ceremony.administrator_signing_key_id
  OR p_executor_envelope->>'kms_key_arn'<>ceremony.executor_kms_key_arn
  OR p_administrator_envelope->>'kms_key_arn'<>ceremony.administrator_kms_key_arn
  OR p_executor_envelope->>'public_key_spki_sha256'<>encode(ceremony.executor_public_key_spki_sha256,'hex')
  OR p_administrator_envelope->>'public_key_spki_sha256'<>encode(ceremony.administrator_public_key_spki_sha256,'hex')
  OR p_executor_envelope->>'signature_algorithm'<>'ECDSA_SHA_256'
  OR p_administrator_envelope->>'signature_algorithm'<>'ECDSA_SHA_256'
  OR (p_executor_envelope->>'github_actor_id')::bigint<>ceremony.executor_github_actor_id
  OR (p_administrator_envelope->>'github_actor_id')::bigint<>ceremony.administrator_github_actor_id
  OR p_executor_envelope->>'github_environment'<>ceremony.executor_github_environment
  OR p_administrator_envelope->>'github_environment'<>ceremony.administrator_github_environment
  OR (p_executor_envelope->>'github_run_id')::bigint<=0 OR (p_administrator_envelope->>'github_run_id')::bigint<=0
  OR (p_executor_envelope->>'github_run_attempt')::bigint<=0 OR (p_administrator_envelope->>'github_run_attempt')::bigint<=0
  OR executor_issued_at<ceremony.prepared_at OR administrator_issued_at<ceremony.prepared_at
  OR executor_issued_at>now_at OR administrator_issued_at>now_at
  OR executor_expires_at<=now_at OR administrator_expires_at<=now_at
  OR executor_expires_at<=executor_issued_at OR administrator_expires_at<=administrator_issued_at
  OR executor_expires_at>executor_issued_at+interval '15 minutes'
  OR administrator_expires_at>administrator_issued_at+interval '15 minutes'
  OR executor_expires_at>ceremony.ceremony_expires_at OR administrator_expires_at>ceremony.ceremony_expires_at
  OR digest(decode(p_executor_envelope->>'nonce','base64'),'sha256') IS DISTINCT FROM p_executor_nonce_sha256
  OR digest(decode(p_administrator_envelope->>'nonce','base64'),'sha256') IS DISTINCT FROM p_administrator_nonce_sha256
  OR octet_length(decode(p_executor_envelope->>'nonce','base64'))<>32
  OR octet_length(decode(p_administrator_envelope->>'nonce','base64'))<>32
  OR octet_length(decode(p_executor_envelope->>'signature_der_base64','base64')) NOT BETWEEN 8 AND 80
  OR octet_length(decode(p_administrator_envelope->>'signature_der_base64','base64')) NOT BETWEEN 8 AND 80
  OR NOT EXISTS(SELECT 1 FROM users u JOIN privacy_executor_grants g ON g.user_id=u.id AND g.revoked_at IS NULL
    WHERE u.id=ceremony.executor_actor_ref AND u.is_active AND NOT u.is_dependent)
  OR NOT EXISTS(SELECT 1 FROM users u JOIN user_platform_roles a ON a.user_id=u.id JOIN platform_roles r ON r.id=a.role_id
    WHERE u.id=ceremony.administrator_actor_ref AND u.is_active AND NOT u.is_dependent AND r.code='ADMIN')
 THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_signed_approval_rejected'; END IF;

 INSERT INTO privacy_activation_proposals(id,policy_version,evidence_ids,evidence_set_sha256,activation_sha256,proposed_by_ref,proposed_at)
 VALUES(ceremony.proposal_id,ceremony.policy_version,ceremony.evidence_ids,ceremony.evidence_set_sha256,ceremony.activation_sha256,
  ceremony.executor_actor_ref,now_at);
 INSERT INTO privacy_protected.activation_signed_approvals(
  proposal_id,signer_role,actor_ref,signing_key_id,nonce_sha256,envelope_sha256,raw_envelope,parsed_envelope,issued_at,expires_at,
  ceremony_id,material_sha256,signature_algorithm,kms_key_arn,public_key_spki_sha256,github_actor_id,github_environment,
  github_run_id,github_run_attempt,signature_der_sha256)
 VALUES(ceremony.proposal_id,'EXECUTOR',ceremony.executor_actor_ref,ceremony.executor_signing_key_id,p_executor_nonce_sha256,
  digest(p_executor_envelope_raw,'sha256'),p_executor_envelope_raw,p_executor_envelope,executor_issued_at,executor_expires_at,
  ceremony.ceremony_id,ceremony.material_sha256,'ECDSA_SHA_256',ceremony.executor_kms_key_arn,ceremony.executor_public_key_spki_sha256,
  ceremony.executor_github_actor_id,ceremony.executor_github_environment,(p_executor_envelope->>'github_run_id')::bigint,
  (p_executor_envelope->>'github_run_attempt')::bigint,digest(decode(p_executor_envelope->>'signature_der_base64','base64'),'sha256'))
 RETURNING id INTO executor_envelope_id;
 INSERT INTO privacy_protected.activation_signed_approvals(
  proposal_id,signer_role,actor_ref,signing_key_id,nonce_sha256,envelope_sha256,raw_envelope,parsed_envelope,issued_at,expires_at,
  ceremony_id,material_sha256,signature_algorithm,kms_key_arn,public_key_spki_sha256,github_actor_id,github_environment,
  github_run_id,github_run_attempt,signature_der_sha256)
 VALUES(ceremony.proposal_id,'ADMINISTRATOR',ceremony.administrator_actor_ref,ceremony.administrator_signing_key_id,p_administrator_nonce_sha256,
  digest(p_administrator_envelope_raw,'sha256'),p_administrator_envelope_raw,p_administrator_envelope,administrator_issued_at,administrator_expires_at,
  ceremony.ceremony_id,ceremony.material_sha256,'ECDSA_SHA_256',ceremony.administrator_kms_key_arn,ceremony.administrator_public_key_spki_sha256,
  ceremony.administrator_github_actor_id,ceremony.administrator_github_environment,(p_administrator_envelope->>'github_run_id')::bigint,
  (p_administrator_envelope->>'github_run_attempt')::bigint,digest(decode(p_administrator_envelope->>'signature_der_base64','base64'),'sha256'))
 RETURNING id INTO administrator_envelope_id;
 INSERT INTO privacy_activation_approvals(proposal_id,activation_sha256,approved_by_ref,approved_at)
 VALUES(ceremony.proposal_id,ceremony.activation_sha256,ceremony.administrator_actor_ref,now_at) RETURNING id INTO approval_id;
 PERFORM set_config('mycfc.privacy_activation_approval','approved',true);
 INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by,updated_at,approval_id)
 VALUES(true,ceremony.policy_version,true,true,ceremony.administrator_actor_ref,now_at,approval_id)
 ON CONFLICT(singleton) DO UPDATE SET policy_version=EXCLUDED.policy_version,enabled=true,fulfilment_ready=true,
  updated_by=EXCLUDED.updated_by,updated_at=EXCLUDED.updated_at,approval_id=EXCLUDED.approval_id;
 INSERT INTO privacy_request_activation_events(policy_version,actor_ref,enabled,fulfilment_ready,occurred_at)
 VALUES(ceremony.policy_version,ceremony.administrator_actor_ref,true,true,now_at);
 UPDATE privacy_worker_kill_switch SET engaged=false,version=version+1,activation_approval_id=approval_id,changed_at=now_at
  WHERE singleton RETURNING version INTO switch_version;
 INSERT INTO privacy_worker_kill_switch_events(version,engaged,activation_approval_id,occurred_at)
 VALUES(switch_version,false,approval_id,now_at);
 INSERT INTO privacy_protected.activation_broker_receipts(
  proposal_id,approval_id,executor_envelope_id,administrator_envelope_id,evidence_set_sha256,activation_sha256,ceremony_id)
 VALUES(ceremony.proposal_id,approval_id,executor_envelope_id,administrator_envelope_id,ceremony.evidence_set_sha256,
  ceremony.activation_sha256,ceremony.ceremony_id);
 RETURN approval_id;
EXCEPTION
 WHEN unique_violation THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_signed_approval_replay';
 WHEN invalid_text_representation OR character_not_in_repertoire OR datetime_field_overflow OR numeric_value_out_of_range THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_signed_approval_rejected';
END;$$;

REVOKE ALL ON TABLE privacy_protected.activation_ceremonies FROM PUBLIC;
REVOKE ALL ON FUNCTION
 public.privacy_activation_broker_register_ceremony(uuid,uuid,text,text,uuid[],bytea,bytea,text,text,text,bytea,bytea,bytea,bytea,jsonb,timestamptz,timestamptz,uuid,text,text,bytea,bigint,text,uuid,text,text,bytea,bigint,text),
 public.privacy_activation_broker_activate(uuid,bytea,jsonb,bytea,jsonb,bytea,bytea)
FROM PUBLIC;
DO $$BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='mycfc_privacy_activation_broker') THEN
  GRANT EXECUTE ON FUNCTION
   public.privacy_activation_broker_register_ceremony(uuid,uuid,text,text,uuid[],bytea,bytea,text,text,text,bytea,bytea,bytea,bytea,jsonb,timestamptz,timestamptz,uuid,text,text,bytea,bigint,text,uuid,text,text,bytea,bigint,text),
   public.privacy_activation_broker_activate(uuid,bytea,jsonb,bytea,jsonb,bytea,bytea)
  TO mycfc_privacy_activation_broker;
 END IF;
END$$;

-- Every schema revision invalidates all prior activation evidence and keeps
-- both activation switches engaged until a fresh v2 ceremony completes.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp() WHERE singleton;
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at) SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton;
UPDATE guardian_application_intake_release SET enabled=false,policy_version=NULL,policy_sha256=NULL,approval_sha256=NULL,image_digest=NULL,schema_migration_digest=NULL,enabled_by=NULL,enabled_at=NULL WHERE singleton;
WITH disabled AS (UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled RETURNING id,version)
INSERT INTO guardian_authority_policy_events(policy_id,policy_version,actor_ref,action) SELECT id,version,NULL,'MIGRATION_DISABLED' FROM disabled;
SELECT guardian_authority_reconcile_cutoffs();
DELETE FROM sessions session USING users subject WHERE session.user_id=subject.id AND session.subject_indexed AND subject.is_dependent;
UPDATE users SET minor_login_id=NULL,password_hash=NULL,credential_version=credential_version+1,updated_at=clock_timestamp()
 WHERE is_dependent AND (minor_login_id IS NOT NULL OR password_hash IS NOT NULL);
