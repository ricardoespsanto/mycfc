\set ON_ERROR_STOP on

-- Synthetic browser fixture only. Never run against club or production data.
DO $$ BEGIN
  IF current_database() <> 'mycfc_test' THEN
    RAISE EXCEPTION 'guardian browser fixtures require mycfc_test';
  END IF;
END $$;

BEGIN;
INSERT INTO users (id, name, email, password_hash, date_of_birth, email_verified_at) VALUES
  ('11000000-0000-0000-0000-000000000001', 'Verificador de representação de teste', 'e2e-guardian-reviewer@example.test', '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK', '1990-01-01', now()),
  ('11000000-0000-0000-0000-000000000004', 'Verificador alternativo de representação', 'e2e-guardian-alternate@example.test', '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK', '1990-01-01', now()),
  ('11000000-0000-0000-0000-000000000006', 'Segundo administrador de representação', 'e2e-guardian-admin-two@example.test', '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK', '1990-01-01', now()),
  ('11000000-0000-0000-0000-000000000002', 'Tutor de representação de teste', 'e2e-guardian-fixture@example.test', '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK', '1990-01-01', now())
ON CONFLICT (id) DO NOTHING;
INSERT INTO user_platform_roles(user_id,role_id)
SELECT '11000000-0000-0000-0000-000000000006',id FROM platform_roles WHERE code='ADMIN'
ON CONFLICT DO NOTHING;
INSERT INTO users (id, name, is_dependent, date_of_birth, minor_login_id, password_hash)
VALUES ('11000000-0000-0000-0000-000000000003', 'Menor de representação de teste', true, '2015-01-01', 'CFC-EE110003', '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK')
ON CONFLICT (id) DO NOTHING;
INSERT INTO users (id, name, is_dependent, date_of_birth)
VALUES ('11000000-0000-0000-0000-000000000005', 'Menor com verificação expirada', true, '2014-03-03')
ON CONFLICT (id) DO NOTHING;

-- Synthetic guardian-authority policy for browser tests only. Production and
-- fresh databases remain closed until an administrator adopts policy and
-- separately grants a verifier.
SELECT guardian_authority_adopt_policy(
  actor.id,
  'e2e-guardian-v1',
  ARRAY['IN_PERSON_IDENTITY'],
  ARRAY['APPROVED','INSUFFICIENT_EVIDENCE','CONFLICT','NO_AUTHORITY'],
  365,
  180
)
FROM users actor
WHERE actor.email = 'e2e-admin@example.test';
INSERT INTO guardian_authority_policy_approvals(
  policy_version,policy_sha256,approval_sha256,approval_contract,approval_canonical,expected_database,authorized_operator_actor_ref,
  controller_role,controller_approval_reference,controller_approved_on,effective_on,review_due_on,
  legal_reviewer_reference,legal_review_reference,legal_reviewed_on,legal_review_conclusion,
  bound_image_digest,bound_schema_migration_digest,bound_by)
SELECT 'e2e-guardian-v1',digest(convert_to('e2e-guardian-v1','UTF8'),'sha256'),digest(convert_to('e2e/approval','UTF8'),'sha256'),
  'mycfc/guardian-authority-policy-approval/v1',convert_to('{}','UTF8'),current_database(),actor.id,'CLUB_DIRECTION','e2e/controller',
  CURRENT_DATE,CURRENT_DATE,CURRENT_DATE+365,'e2e/legal-reviewer','e2e/legal-review',CURRENT_DATE,'APPROVED',
  'sha256:'||repeat('a',64),
  (SELECT encode(digest(convert_to(string_agg(version,E'\n' ORDER BY version),'UTF8'),'sha256'),'hex') FROM mycfc_meta.schema_migrations),actor.id
FROM users actor WHERE actor.email='e2e-admin@example.test';
WITH enabled AS (
  UPDATE guardian_authority_policies policy SET enabled=true,enabled_at=clock_timestamp(),enabled_by=actor.id
  FROM users actor WHERE actor.email='e2e-admin@example.test' AND policy.version='e2e-guardian-v1'
  RETURNING policy.id,policy.version,policy.enabled_by,policy.enabled_at
)
INSERT INTO guardian_authority_policy_events(policy_id,policy_version,actor_ref,action,occurred_at)
SELECT id,version,enabled_by,'ENABLED',enabled_at FROM enabled;
UPDATE guardian_ops.runtime_release_binding SET database_name=current_database(),image_digest='sha256:'||repeat('a',64),
  schema_migration_digest=(SELECT encode(digest(convert_to(string_agg(version,E'\n' ORDER BY version),'UTF8'),'sha256'),'hex') FROM mycfc_meta.schema_migrations),
  generation=generation+1,bound_at=clock_timestamp() WHERE singleton;
UPDATE guardian_application_intake_release gate SET enabled=true,policy_version=approval.policy_version,
  policy_sha256=approval.policy_sha256,approval_sha256=approval.approval_sha256,image_digest=approval.bound_image_digest,
  schema_migration_digest=approval.bound_schema_migration_digest,enabled_by=approval.authorized_operator_actor_ref,enabled_at=clock_timestamp()
FROM guardian_authority_policy_approvals approval WHERE gate.singleton AND approval.policy_version='e2e-guardian-v1';
SELECT guardian_authority_grant_verifier(actor.id, verifier.id)
FROM users actor
CROSS JOIN users verifier
WHERE actor.email = 'e2e-admin@example.test'
  AND verifier.email IN ('e2e-guardian-reviewer@example.test', 'e2e-guardian-alternate@example.test');

INSERT INTO guardian_authority_relationships
  (id, public_ref, guardian_user_id, subject_user_id, submitted_label)
VALUES (
  '11200000-0000-0000-0000-000000000003',
  '11300000-0000-0000-0000-000000000003',
  '11000000-0000-0000-0000-000000000002',
  '11000000-0000-0000-0000-000000000003',
  'Menor de representação de teste'
);
INSERT INTO guardian_authority_events
  (relationship_id, relationship_version, actor_ref, actor_role, action, from_state, to_state)
VALUES (
  '11200000-0000-0000-0000-000000000003',
  1,
  '11000000-0000-0000-0000-000000000002',
  'GUARDIAN',
  'DECLARED',
  NULL,
  'PENDING'
);
SELECT guardian_authority_transition(
  verifier.id,
  '11300000-0000-0000-0000-000000000003',
  1,
  'VERIFIED',
  'IN_PERSON_IDENTITY',
  'e2e/guardian-minor',
  digest(convert_to('e2e/guardian-minor', 'UTF8'), 'sha256'),
  'APPROVED'
)
FROM users verifier
WHERE verifier.email = 'e2e-guardian-reviewer@example.test';

-- Put this verified relationship inside the approved 30-day renewal window.
WITH cutoff AS (SELECT clock_timestamp()+interval '20 days' AS value)
UPDATE guardian_authority_relationships relationship
SET verified_until=cutoff.value,review_due_at=cutoff.value
FROM cutoff WHERE relationship.id='11200000-0000-0000-0000-000000000003';

-- Issue the synthetic minor credential only after authority is current, matching
-- the application workflow and recording the same append-only audit evidence.
WITH issued AS (
  UPDATE users minor
  SET minor_login_id='CFC-EE110003',
      password_hash='$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK',
      credential_version=minor.credential_version+1,
      updated_at=clock_timestamp()
  WHERE minor.id='11000000-0000-0000-0000-000000000003'
    AND guardian_authority_current('11000000-0000-0000-0000-000000000002',minor.id)
  RETURNING minor.id
)
INSERT INTO minor_credential_audit(minor_user_id,guardian_user_id,actor_user_id,action,issued_login_id)
SELECT issued.id,'11000000-0000-0000-0000-000000000002',admin.id,'ISSUED','CFC-EE110003'
FROM issued CROSS JOIN users admin WHERE admin.email='e2e-admin@example.test';

-- Same-identity age-18 handoff fixture: the birthday remains 20 Lisbon dates
-- ahead regardless of when the browser suite runs.
INSERT INTO users(id,name,is_dependent,date_of_birth,minor_login_id,password_hash)
VALUES(
  '11000000-0000-0000-0000-000000000007',
  'Jovem em transição de teste',
  true,
  (((clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+20)-interval '18 years')::date,
  'CFC-EE110007',
  '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK'
);
INSERT INTO guardian_authority_relationships(id,public_ref,guardian_user_id,subject_user_id,submitted_label)
VALUES(
  '11200000-0000-0000-0000-000000000007',
  '11300000-0000-0000-0000-000000000007',
  '11000000-0000-0000-0000-000000000002',
  '11000000-0000-0000-0000-000000000007',
  'Jovem em transição de teste'
);
INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state)
VALUES(
  '11200000-0000-0000-0000-000000000007',1,
  '11000000-0000-0000-0000-000000000002','GUARDIAN','DECLARED',NULL,'PENDING'
);
SELECT guardian_authority_admin_transition(
  admin.id,
  '11300000-0000-0000-0000-000000000007',
  1,
  'APPROVE',
  'CLUB_REGISTRATION_RECORD',
  'RELATIONSHIP_CONFIRMED'
)
FROM users admin WHERE admin.email='e2e-admin@example.test';
SELECT guardian_age_handoff_reconcile();

INSERT INTO guardian_authority_relationships
  (id, public_ref, guardian_user_id, subject_user_id, submitted_label, state, version, policy_version,
   verified_at, verified_by, verified_until, review_due_at)
VALUES (
  '11200000-0000-0000-0000-000000000005',
  '11300000-0000-0000-0000-000000000005',
  '11000000-0000-0000-0000-000000000002',
  '11000000-0000-0000-0000-000000000005',
  'Menor com verificação expirada',
  'VERIFIED', 2, 'e2e-guardian-v1',
  clock_timestamp()-interval '200 days',
  '11000000-0000-0000-0000-000000000001',
  clock_timestamp()+interval '165 days',
  clock_timestamp()-interval '20 days'
);
INSERT INTO guardian_authority_events
  (relationship_id, relationship_version, actor_ref, actor_role, action, from_state, to_state,
   policy_version, evidence_type, evidence_reference, evidence_sha256, reason_code, occurred_at, verified_until, review_due_at)
SELECT id,2,'11000000-0000-0000-0000-000000000001','VERIFIER','VERIFIED','PENDING','VERIFIED',
 'e2e-guardian-v1','IN_PERSON_IDENTITY','e2e/expired-minor',digest(convert_to('e2e/expired-minor','UTF8'),'sha256'),
 'APPROVED',verified_at,verified_until,review_due_at
FROM guardian_authority_relationships WHERE id='11200000-0000-0000-0000-000000000005';

-- Keep the transition credential setup as the final fixture mutation. The
-- application starts alongside this transaction and may run a cutoff pass
-- against the preceding snapshot; the final reconcile makes the committed
-- browser fixture deterministic without weakening production behavior.
UPDATE users
SET minor_login_id='CFC-EE110007',
    password_hash='$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK'
WHERE id='11000000-0000-0000-0000-000000000007';
SELECT guardian_age_handoff_reconcile();
COMMIT;
