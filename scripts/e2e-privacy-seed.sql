\set ON_ERROR_STOP on

-- Synthetic browser fixture only. Never run against club or production data.
DO $$ BEGIN
  IF current_database() <> 'mycfc_test' THEN
    RAISE EXCEPTION 'privacy browser fixtures require mycfc_test';
  END IF;
END $$;

BEGIN;
INSERT INTO users (id, name, email, password_hash, date_of_birth, email_verified_at) VALUES
  ('11000000-0000-0000-0000-000000000001', 'Revisor de privacidade de teste', 'e2e-privacy-reviewer@example.test', '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK', '1990-01-01', now()),
  ('11000000-0000-0000-0000-000000000004', 'Revisor alternativo de privacidade de teste', 'e2e-privacy-alternate@example.test', '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK', '1990-01-01', now()),
  ('11000000-0000-0000-0000-000000000002', 'Tutor de privacidade de teste', 'e2e-privacy-guardian@example.test', '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK', '1990-01-01', now())
ON CONFLICT (id) DO NOTHING;
INSERT INTO users (id, name, guardian_id, is_dependent, date_of_birth, minor_login_id, password_hash)
VALUES ('11000000-0000-0000-0000-000000000003', 'Menor de privacidade de teste', '11000000-0000-0000-0000-000000000002', true, '2015-01-01', 'CFC-EE110003', '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK')
ON CONFLICT (id) DO NOTHING;

-- The normal administrator is the audited grant actor, never a privacy reviewer.
WITH grant_row AS (
  INSERT INTO privacy_reviewer_grants (user_id, granted_by, granted_at)
  SELECT reviewer.id, actor.id, now() FROM users actor CROSS JOIN users reviewer
  WHERE actor.email = 'e2e-admin@example.test'
    AND reviewer.email IN ('e2e-privacy-reviewer@example.test', 'e2e-privacy-alternate@example.test')
  ON CONFLICT (user_id) WHERE revoked_at IS NULL DO NOTHING
  RETURNING id, granted_by, granted_at
)
INSERT INTO privacy_reviewer_grant_events (grant_id, actor_ref, action, occurred_at)
SELECT id, granted_by, 'GRANTED', granted_at FROM grant_row;

WITH grant_row AS (
  INSERT INTO privacy_executor_grants (user_id, granted_by, granted_at)
  SELECT executor.id, actor.id, now() FROM users actor CROSS JOIN users executor
  WHERE actor.email = 'e2e-admin@example.test'
    AND executor.email = 'e2e-privacy-alternate@example.test'
  ON CONFLICT (user_id) WHERE revoked_at IS NULL DO NOTHING
  RETURNING id, granted_by, granted_at
)
INSERT INTO privacy_executor_grant_events (grant_id, actor_ref, action, occurred_at)
SELECT id, granted_by, 'GRANTED', granted_at FROM grant_row;

-- These names and codes deliberately make no claim about adopted club policy.
INSERT INTO privacy_request_policies
  (version, category_catalogue, executor_version, plan_schema_version, account_closure_enabled, working_retention_days, response_months, extension_months, adopted_at, adopted_by)
SELECT 'e2e-privacy-v1', '[
  {"key":"announcement-deliveries","label":"Entregas de anúncios","description":"Dados sintéticos.","rule":{"profile":"ANNOUNCEMENT_DELIVERY_CLEAR_V1","legal_ground":"APPROVED_POLICY","owner":"CONTENT","complete_within_days":0,"fallback":"BLOCK"},"grounds":[]},
  {"key":"audit-evidence","label":"Evidência de auditoria","description":"Dados sintéticos.","rule":{"profile":"AUDIT_ACTOR_ANONYMIZE_V1","legal_ground":"APPROVED_POLICY","owner":"SECURITY","complete_within_days":0,"fallback":"BLOCK"},"grounds":[]},
  {"key":"backup-tombstones","label":"Marcadores de cópia de segurança","description":"Dados sintéticos.","rule":{"profile":"BACKUP_TOMBSTONE_RESTRICT_V1","retained_fields":["users.id"],"legal_ground":"APPROVED_POLICY","owner":"OPERATIONS","complete_within_days":0,"retention_unit":"CALENDAR_MONTH","review_after":12,"expire_after":24,"fallback":"BLOCK"},"grounds":[]},
  {"key":"consent-evidence","label":"Evidência de consentimento","description":"Dados sintéticos.","rule":{"profile":"CONSENT_EVIDENCE_RESTRICT_V1","retained_fields":["consent.document_hash"],"legal_ground":"APPROVED_POLICY","owner":"PRIVACY","complete_within_days":0,"retention_unit":"CALENDAR_YEAR","review_after":1,"expire_after":3,"fallback":"BLOCK"},"grounds":[]},
  {"key":"consent-network","label":"Rede de consentimento","description":"Dados sintéticos.","rule":{"profile":"CONSENT_NETWORK_EXPIRE_V1","retained_fields":["consent.decided_at"],"legal_ground":"APPROVED_POLICY","owner":"PRIVACY","complete_within_days":0,"retention_unit":"CALENDAR_MONTH","review_after":6,"expire_after":12,"fallback":"BLOCK"},"grounds":[]},
  {"key":"dependant-relations","label":"Relações de dependência","description":"Dados sintéticos.","rule":{"profile":"DEPENDANT_RELATION_CLEAR_V1","legal_ground":"APPROVED_POLICY","owner":"PRIVACY","complete_within_days":0,"fallback":"BLOCK"},"grounds":[]},
  {"key":"event-responses","label":"Respostas a eventos","description":"Dados sintéticos.","rule":{"profile":"EVENT_RESPONSE_CLEAR_V1","legal_ground":"APPROVED_POLICY","owner":"OPERATIONS","complete_within_days":0,"fallback":"BLOCK"},"grounds":[]},
  {"key":"health-emergency","label":"Saúde e emergência","description":"Dados sintéticos.","rule":{"profile":"PROFILE_HEALTH_CLEAR_V1","legal_ground":"APPROVED_POLICY","owner":"PRIVACY","complete_within_days":0,"fallback":"BLOCK"},"grounds":[]},
  {"key":"identity-core","label":"Identidade da conta","description":"Dados sintéticos.","rule":{"profile":"IDENTITY_CLEAR_V1","legal_ground":"APPROVED_POLICY","owner":"PRIVACY","complete_within_days":0,"fallback":"BLOCK"},"grounds":[{"code":"LEGAL_HOLD","label":"Conservação legal sintética","rule":{"profile":"IDENTITY_RESTRICT_V1","retained_fields":["users.id"],"legal_ground":"LEGAL_HOLD","owner":"PRIVACY","complete_within_days":0,"retention_unit":"CALENDAR_DAY","review_after":30,"expire_after":90,"fallback":"BLOCK"}}]},
  {"key":"membership-history","label":"Histórico de filiação","description":"Dados sintéticos.","rule":{"profile":"MEMBERSHIP_HISTORY_ANONYMIZE_V1","legal_ground":"APPROVED_POLICY","owner":"SECRETARIAT","complete_within_days":0,"fallback":"BLOCK"},"grounds":[]},
  {"key":"object-storage","label":"Objetos privados","description":"Dados sintéticos.","rule":{"profile":"OBJECT_VERSIONS_CLEAR_V1","legal_ground":"APPROVED_POLICY","owner":"IT","complete_within_days":0,"fallback":"BLOCK"},"grounds":[]},
  {"key":"operational-logs","label":"Registos operacionais","description":"Dados sintéticos.","rule":{"profile":"LOG_EXPIRE_V1","retained_fields":["audit.occurred_at"],"legal_ground":"APPROVED_POLICY","owner":"SECURITY","complete_within_days":0,"retention_unit":"CALENDAR_DAY","review_after":30,"expire_after":90,"fallback":"BLOCK"},"grounds":[]},
  {"key":"outbox-email","label":"Fila de correio","description":"Dados sintéticos.","rule":{"profile":"OUTBOX_EXPIRE_V1","retained_fields":["audit.occurred_at"],"legal_ground":"APPROVED_POLICY","owner":"IT","complete_within_days":0,"retention_unit":"CALENDAR_DAY","review_after":30,"expire_after":90,"fallback":"BLOCK"},"grounds":[]},
  {"key":"privacy-cases","label":"Casos de privacidade","description":"Dados sintéticos.","rule":{"profile":"PRIVACY_CASE_RESTRICT_V1","retained_fields":["privacy_request.public_ref"],"legal_ground":"APPROVED_POLICY","owner":"PRIVACY","complete_within_days":0,"retention_unit":"CALENDAR_MONTH","review_after":12,"expire_after":24,"fallback":"BLOCK"},"grounds":[]},
  {"key":"profile-core","label":"Perfil do membro","description":"Dados sintéticos.","rule":{"profile":"PROFILE_CLEAR_V1","legal_ground":"APPROVED_POLICY","owner":"SECRETARIAT","complete_within_days":0,"fallback":"BLOCK"},"grounds":[{"code":"LEGAL_HOLD","label":"Conservação legal sintética","rule":{"profile":"PROFILE_RESTRICT_V1","retained_fields":["member_profile.federation_id"],"legal_ground":"LEGAL_HOLD","owner":"PRIVACY","complete_within_days":0,"retention_unit":"CALENDAR_DAY","review_after":30,"expire_after":90,"fallback":"BLOCK"}}]},
  {"key":"profile-photo","label":"Fotografia de perfil","description":"Dados sintéticos.","rule":{"profile":"OBJECT_VERSIONS_CLEAR_V1","legal_ground":"APPROVED_POLICY","owner":"IT","complete_within_days":0,"fallback":"BLOCK"},"grounds":[]},
  {"key":"repair-history","label":"Histórico de reparações","description":"Dados sintéticos.","rule":{"profile":"REPAIR_REPORTER_ANONYMIZE_V1","legal_ground":"APPROVED_POLICY","owner":"OPERATIONS","complete_within_days":0,"fallback":"BLOCK"},"grounds":[]},
  {"key":"sessions","label":"Sessões","description":"Dados sintéticos.","rule":{"profile":"AUTH_SESSION_EXPIRE_V1","retained_fields":["users.id"],"legal_ground":"APPROVED_POLICY","owner":"IT","complete_within_days":0,"retention_unit":"HOUR","review_after":6,"expire_after":12,"fallback":"BLOCK"},"grounds":[]},
  {"key":"suggestions","label":"Sugestões","description":"Dados sintéticos.","rule":{"profile":"SUGGESTION_CLEAR_V1","legal_ground":"APPROVED_POLICY","owner":"MODERATION","complete_within_days":0,"fallback":"BLOCK"},"grounds":[]},
  {"key":"training-prescriptions","label":"Prescrições de treino","description":"Dados sintéticos.","rule":{"profile":"TRAINING_PRESCRIPTION_CLEAR_V1","legal_ground":"APPROVED_POLICY","owner":"SPORT","complete_within_days":0,"fallback":"BLOCK"},"grounds":[]},
  {"key":"training-results","label":"Resultados de treino","description":"Dados sintéticos.","rule":{"profile":"TRAINING_RESULT_CLEAR_V1","legal_ground":"APPROVED_POLICY","owner":"SPORT","complete_within_days":0,"fallback":"BLOCK"},"grounds":[]},
  {"key":"verification-reset","label":"Verificação e reposição","description":"Dados sintéticos.","rule":{"profile":"AUTH_TOKEN_CLEAR_V1","legal_ground":"APPROVED_POLICY","owner":"IT","complete_within_days":0,"fallback":"BLOCK"},"grounds":[]}
]'::jsonb, 'privacy-erasure-executor/v2', 'privacy-erasure-plan/v2', true, 90, 1, 2, now(), id
FROM users WHERE email = 'e2e-admin@example.test'
ON CONFLICT (version) DO UPDATE SET category_catalogue=EXCLUDED.category_catalogue,
  executor_version=EXCLUDED.executor_version, plan_schema_version=EXCLUDED.plan_schema_version,
  account_closure_enabled=EXCLUDED.account_closure_enabled, working_retention_days=EXCLUDED.working_retention_days,
  response_months=EXCLUDED.response_months, extension_months=EXCLUDED.extension_months,
  adopted_at=EXCLUDED.adopted_at, adopted_by=EXCLUDED.adopted_by;
-- Exercise the same four-evidence and independent-approval gate as production.
-- The fixture hashes are synthetic and authenticate no real operational claim.
DO $$
DECLARE
  admin_id uuid;
  executor_id uuid;
  evidence_ids uuid[];
  proposal_id uuid;
  proposal_sha256 bytea;
BEGIN
  SELECT id INTO STRICT admin_id FROM users WHERE email = 'e2e-admin@example.test';
  SELECT id INTO STRICT executor_id FROM users WHERE email = 'e2e-privacy-alternate@example.test';
  SELECT array_agg(recorded.id ORDER BY recorded.kind) INTO evidence_ids
  FROM (
    SELECT fixture.kind, privacy_activation_record_evidence(
      admin_id,
      fixture.kind,
      digest(convert_to('e2e-privacy-activation:' || fixture.kind, 'UTF8'), 'sha256'),
      fixture.contract,
      clock_timestamp() - interval '1 minute'
    ) AS id
    FROM (VALUES
      ('RESTORE', 'mycfc/privacy-restore-drill-attestation/v1'),
      ('INFRASTRUCTURE', 'mycfc/privacy-infrastructure-posture/v1'),
      ('PROVIDER', 'mycfc/privacy-provider-registry/v1'),
      ('SCHEMA', 'mycfc/schema-migration-inventory/v1')
    ) AS fixture(kind, contract)
  ) recorded;
  SELECT proposed.proposal_id, proposed.activation_sha256
  INTO STRICT proposal_id, proposal_sha256
  FROM privacy_activation_propose(executor_id, 'e2e-privacy-v1', evidence_ids) proposed;
  PERFORM privacy_activation_approve(admin_id, proposal_id, proposal_sha256);
END $$;
COMMIT;
