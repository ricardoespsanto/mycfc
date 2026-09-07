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

-- These names and codes deliberately make no claim about adopted club policy.
INSERT INTO privacy_request_policies
  (version, category_catalogue, account_closure_enabled, working_retention_days, response_months, extension_months, adopted_at, adopted_by)
SELECT 'e2e-privacy-v1', '[
  {"key":"e2e-alpha","label":"Categoria sintética alfa","description":"Dados exclusivamente sintéticos para testes.","action":"e2e-erase","grounds":[{"code":"e2e-hold","label":"Fundamento sintético de conservação"}]},
  {"key":"e2e-beta","label":"Categoria sintética beta","description":"Outra categoria exclusivamente sintética.","action":"e2e-erase","grounds":[{"code":"e2e-hold","label":"Fundamento sintético de conservação"}]}
]'::jsonb, true, 30, 1, 2, now(), id
FROM users WHERE email = 'e2e-admin@example.test'
ON CONFLICT (version) DO NOTHING;
INSERT INTO privacy_request_activation (policy_version, enabled, fulfilment_ready, updated_by)
SELECT 'e2e-privacy-v1', true, true, id FROM users WHERE email = 'e2e-admin@example.test'
ON CONFLICT (singleton) DO UPDATE SET policy_version = EXCLUDED.policy_version,
  enabled = true, fulfilment_ready = true, updated_by = EXCLUDED.updated_by, updated_at = now();
INSERT INTO privacy_request_activation_events (policy_version, actor_ref, enabled, fulfilment_ready, occurred_at)
SELECT 'e2e-privacy-v1', id, true, true, now() FROM users
WHERE email = 'e2e-admin@example.test';
COMMIT;
