\set ON_ERROR_STOP on

-- Deterministic, test-only identities. Password for every credentialed account:
-- "correct horse 7". This file is applied only to the isolated
-- mycfc_ui_review database by scripts/ui-review-reset.sh.

INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES
  ('10000000-0000-0000-0000-000000000001', 'Alexandre Sem Inscrição', 'review-member@example.test', '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK', '1991-04-12'),
  ('10000000-0000-0000-0000-000000000002', 'Marta Tutora da Família Rodrigues e Albuquerque', 'review-tutor@example.test', '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK', '1982-09-03'),
  ('10000000-0000-0000-0000-000000000003', 'Inês Atleta de Competição', 'review-athlete@example.test', '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK', '2004-02-18'),
  ('10000000-0000-0000-0000-000000000004', 'Tiago Treinador de Competição', 'review-coach@example.test', '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK', '1987-11-25'),
  ('10000000-0000-0000-0000-000000000005', 'Beatriz Administradora do Clube', 'review-admin@example.test', '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK', '1985-06-14'),
  ('10000000-0000-0000-0000-000000000006', 'Rui Atleta Tutor Treinador e Moderador', 'review-multi@example.test', '$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK', '1989-01-30');

INSERT INTO users (id, name, is_dependent, date_of_birth) VALUES
  ('10000000-0000-0000-0000-000000000011', 'Leonor Rodrigues e Albuquerque', true, '2013-05-19'),
  ('10000000-0000-0000-0000-000000000012', 'Gonçalo Rodrigues e Albuquerque', true, '2016-10-08'),
  ('10000000-0000-0000-0000-000000000013', 'Sofia Ferreira', true, '2015-03-21');

INSERT INTO user_platform_roles (user_id, role_id)
SELECT '10000000-0000-0000-0000-000000000005', id FROM platform_roles WHERE code = 'ADMIN';

-- Synthetic authority policy and evidence for this isolated visual-review
-- database. Fresh application databases deliberately seed neither.
SELECT guardian_authority_adopt_policy(
  '10000000-0000-0000-0000-000000000005',
  'ui-review-guardian-v1',
  ARRAY['IN_PERSON_IDENTITY'],
  ARRAY['APPROVED','INSUFFICIENT_EVIDENCE','CONFLICT','NO_AUTHORITY'],
  3650,
  1825
);
INSERT INTO guardian_authority_policy_approvals(
  policy_version,policy_sha256,approval_sha256,approval_contract,approval_canonical,expected_database,authorized_operator_actor_ref,
  controller_role,controller_approval_reference,controller_approved_on,effective_on,review_due_on,
  legal_reviewer_reference,legal_review_reference,legal_reviewed_on,legal_review_conclusion,
  bound_image_digest,bound_schema_migration_digest,bound_by)
VALUES ('ui-review-guardian-v1',digest(convert_to('ui-review-guardian-v1','UTF8'),'sha256'),digest(convert_to('ui-review/approval','UTF8'),'sha256'),
  'mycfc/guardian-authority-policy-approval/v1',convert_to('{}','UTF8'),current_database(),'10000000-0000-0000-0000-000000000005',
  'CLUB_DIRECTION','ui-review/controller',CURRENT_DATE,CURRENT_DATE,CURRENT_DATE+3650,'ui-review/legal-reviewer','ui-review/legal-review',
  CURRENT_DATE,'APPROVED','sha256:'||repeat('a',64),
  (SELECT encode(digest(convert_to(string_agg(version,E'\n' ORDER BY version),'UTF8'),'sha256'),'hex') FROM mycfc_meta.schema_migrations),
  '10000000-0000-0000-0000-000000000005');
WITH enabled AS (
  UPDATE guardian_authority_policies
  SET enabled=true,enabled_at=clock_timestamp(),enabled_by='10000000-0000-0000-0000-000000000005'
  WHERE version='ui-review-guardian-v1'
  RETURNING id,version,enabled_at
)
INSERT INTO guardian_authority_policy_events(policy_id,policy_version,actor_ref,action,occurred_at)
SELECT id,version,'10000000-0000-0000-0000-000000000005','ENABLED',enabled_at FROM enabled;
UPDATE guardian_ops.runtime_release_binding SET database_name=current_database(),image_digest='sha256:'||repeat('a',64),
  schema_migration_digest=(SELECT encode(digest(convert_to(string_agg(version,E'\n' ORDER BY version),'UTF8'),'sha256'),'hex') FROM mycfc_meta.schema_migrations),
  generation=generation+1,bound_at=clock_timestamp() WHERE singleton;
UPDATE guardian_application_intake_release gate SET enabled=true,policy_version=approval.policy_version,
  policy_sha256=approval.policy_sha256,approval_sha256=approval.approval_sha256,image_digest=approval.bound_image_digest,
  schema_migration_digest=approval.bound_schema_migration_digest,enabled_by=approval.authorized_operator_actor_ref,enabled_at=clock_timestamp()
FROM guardian_authority_policy_approvals approval WHERE gate.singleton AND approval.policy_version='ui-review-guardian-v1';
SELECT guardian_authority_grant_verifier(
  '10000000-0000-0000-0000-000000000005',
  '10000000-0000-0000-0000-000000000004'
);

INSERT INTO guardian_authority_relationships
  (id, public_ref, guardian_user_id, subject_user_id, submitted_label)
VALUES
  ('12000000-0000-0000-0000-000000000011', '12100000-0000-0000-0000-000000000011', '10000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000011', 'Leonor Rodrigues e Albuquerque'),
  ('12000000-0000-0000-0000-000000000012', '12100000-0000-0000-0000-000000000012', '10000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000012', 'Gonçalo Rodrigues e Albuquerque'),
  ('12000000-0000-0000-0000-000000000013', '12100000-0000-0000-0000-000000000013', '10000000-0000-0000-0000-000000000006', '10000000-0000-0000-0000-000000000013', 'Sofia Ferreira');
INSERT INTO guardian_authority_events
  (relationship_id, relationship_version, actor_ref, actor_role, action, from_state, to_state)
SELECT id, 1, guardian_user_id, 'GUARDIAN', 'DECLARED', NULL, 'PENDING'
FROM guardian_authority_relationships
WHERE id IN (
  '12000000-0000-0000-0000-000000000011',
  '12000000-0000-0000-0000-000000000012',
  '12000000-0000-0000-0000-000000000013'
);
SELECT guardian_authority_transition(
  '10000000-0000-0000-0000-000000000004',
  public_ref,
  1,
  'VERIFIED',
  'IN_PERSON_IDENTITY',
  'ui-review/' || subject_user_id::text,
  digest(convert_to('ui-review/' || subject_user_id::text, 'UTF8'), 'sha256'),
  'APPROVED'
)
FROM guardian_authority_relationships
WHERE id IN (
  '12000000-0000-0000-0000-000000000011',
  '12000000-0000-0000-0000-000000000012',
  '12000000-0000-0000-0000-000000000013'
);

UPDATE feature_flags
SET mode = 'ENABLED', updated_by_id = '10000000-0000-0000-0000-000000000005', updated_at = now()
WHERE feature_key = 'structured_training_planning';

INSERT INTO seasons (id, code, name, starts_on, ends_on, is_current)
VALUES ('20000000-0000-0000-0000-000000000001', 'UI_REVIEW', 'Época de revisão da experiência MyCFC', '2025-01-01', '2099-12-31', true);

INSERT INTO teams (id, season_id, programme_id, code, name)
SELECT '30000000-0000-0000-0000-000000000001', '20000000-0000-0000-0000-000000000001', id, 'COMP_LONG', 'Equipa de competição de velocidade e fundo — Mondego' FROM programmes WHERE code = 'Competition';

INSERT INTO user_memberships (id, user_id, season_id, programme_id, team_id, starts_on)
SELECT '40000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000003', '20000000-0000-0000-0000-000000000001', p.id, '30000000-0000-0000-0000-000000000001', '2025-01-01' FROM programmes p WHERE p.code = 'Competition';
INSERT INTO user_memberships (id, user_id, season_id, programme_id, starts_on)
SELECT '40000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000006', '20000000-0000-0000-0000-000000000001', p.id, '2025-01-01' FROM programmes p WHERE p.code = 'Competition';
INSERT INTO user_memberships (id, user_id, season_id, programme_id, starts_on)
SELECT '40000000-0000-0000-0000-000000000003', '10000000-0000-0000-0000-000000000006', '20000000-0000-0000-0000-000000000001', p.id, '2025-01-01' FROM programmes p WHERE p.code = 'Leisure';
INSERT INTO user_memberships (id, user_id, season_id, programme_id, starts_on)
SELECT '40000000-0000-0000-0000-000000000004', '10000000-0000-0000-0000-000000000011', '20000000-0000-0000-0000-000000000001', p.id, '2025-01-01' FROM programmes p WHERE p.code = 'Initiation';
INSERT INTO user_memberships (id, user_id, season_id, programme_id, starts_on)
SELECT '40000000-0000-0000-0000-000000000005', '10000000-0000-0000-0000-000000000002', '20000000-0000-0000-0000-000000000001', p.id, '2025-01-01' FROM programmes p WHERE p.code = 'Initiation';

-- Minimal reader fixture for subject switching: Marta can see her own plan and
-- the plan assigned to the minor Leonor, without gaining management controls.
INSERT INTO training_groups (id, name, programme_id, created_by_id)
SELECT '74000000-0000-0000-0000-000000000001', 'Iniciação — família Rodrigues e Albuquerque', p.id, '10000000-0000-0000-0000-000000000005' FROM programmes p WHERE p.code = 'Initiation';
INSERT INTO training_group_members (group_id, membership_id, added_by_id) VALUES
  ('74000000-0000-0000-0000-000000000001', '40000000-0000-0000-0000-000000000004', '10000000-0000-0000-0000-000000000005'),
  ('74000000-0000-0000-0000-000000000001', '40000000-0000-0000-0000-000000000005', '10000000-0000-0000-0000-000000000005');
INSERT INTO training_plans (id, title, description, programme_id, training_group_id, season_id, week_start, created_by_id)
SELECT '74100000-0000-0000-0000-000000000001', 'Semana familiar de iniciação', 'Plano estruturado determinístico para rever a troca de pessoa.', p.id, '74000000-0000-0000-0000-000000000001', '20000000-0000-0000-0000-000000000001', date_trunc('week', CURRENT_DATE)::date, '10000000-0000-0000-0000-000000000005' FROM programmes p WHERE p.code = 'Initiation';
INSERT INTO training_sessions (id, plan_id, title, description, starts_at, ends_at, created_by_id)
VALUES ('74200000-0000-0000-0000-000000000001', '74100000-0000-0000-0000-000000000001', 'Técnica base em família', 'Sessão apenas de leitura para Marta e Leonor.', date_trunc('week', CURRENT_DATE) + interval '1 day 18 hours', date_trunc('week', CURRENT_DATE) + interval '1 day 19 hours', '10000000-0000-0000-0000-000000000005');

INSERT INTO membership_modalities (membership_id, modality_id)
SELECT '40000000-0000-0000-0000-000000000001', id FROM modalities WHERE code = 'K1';

INSERT INTO staff_grants (id, user_id, capability, programme_id, granted_by_id)
SELECT '50000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000004', 'COACH', id, '10000000-0000-0000-0000-000000000005' FROM programmes WHERE code = 'Competition';
INSERT INTO staff_grants (id, user_id, capability, programme_id, granted_by_id)
SELECT '50000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000006', 'COACH', id, '10000000-0000-0000-0000-000000000005' FROM programmes WHERE code = 'Competition';
INSERT INTO staff_grants (id, user_id, capability, granted_by_id)
VALUES ('50000000-0000-0000-0000-000000000003', '10000000-0000-0000-0000-000000000006', 'MODERATOR', '10000000-0000-0000-0000-000000000005');

INSERT INTO equipment (id, asset_tag, name, type, status, notes)
SELECT ('60000000-0000-0000-0000-' || lpad(n::text, 12, '0'))::uuid,
       'REV-' || lpad(n::text, 3, '0'),
       CASE WHEN n % 3 = 0 THEN 'Caiaque de competição K1 com denominação extensa ' || n WHEN n % 3 = 1 THEN 'Pagaia de carbono ' || n ELSE 'Atrelado de transporte ' || n END,
       CASE WHEN n % 3 = 0 THEN 'Boat'::equipment_type WHEN n % 3 = 1 THEN 'Paddle'::equipment_type ELSE 'Vehicle'::equipment_type END,
       CASE WHEN n IN (4, 9) THEN 'Maintenance'::equipment_status ELSE 'Operational'::equipment_status END,
       'Equipamento de demonstração para revisão visual e estados densos.'
FROM generate_series(1, 14) AS n;

INSERT INTO repair_requests (id, idempotency_key, equipment_id, reported_by_id, issue_description, status, date_reported, resolved_at)
SELECT ('61000000-0000-0000-0000-' || lpad(n::text, 12, '0'))::uuid,
       ('62000000-0000-0000-0000-' || lpad(n::text, 12, '0'))::uuid,
       ('60000000-0000-0000-0000-' || lpad((((n - 1) % 14) + 1)::text, 12, '0'))::uuid,
       '10000000-0000-0000-0000-000000000003',
       'Descrição detalhada da avaria número ' || n || ' observada durante o treino no rio Mondego.',
       CASE WHEN n % 3 = 0 THEN 'Resolvido'::repair_status WHEN n % 3 = 1 THEN 'Pendente'::repair_status ELSE 'Em_Analise'::repair_status END,
       now() - (n || ' days')::interval,
       CASE WHEN n % 3 = 0 THEN now() - ((n - 1) || ' days')::interval ELSE NULL END
FROM generate_series(1, 12) AS n;

INSERT INTO maintenance_tasks (id, equipment_id, scheduled_for, description, status, created_by_id, completed_at)
SELECT ('63000000-0000-0000-0000-' || lpad(n::text, 12, '0'))::uuid,
       ('60000000-0000-0000-0000-' || lpad(n::text, 12, '0'))::uuid,
       now() + (n || ' days')::interval,
       'Revisão programada de casco, finca-pés, leme e elementos de segurança ' || n || '.',
       'Scheduled', '10000000-0000-0000-0000-000000000005', NULL
FROM generate_series(1, 8) AS n;

INSERT INTO events (id, title, description, starts_at, ends_at, response_deadline, capacity, created_by_id) VALUES
  ('70000000-0000-0000-0000-000000000001', 'Treino conjunto de velocidade, fundo e preparação física no Mondego', 'Sessão conjunta com briefing de segurança e distribuição de embarcações.', date_trunc('day', now()) + interval '18 hours', date_trunc('day', now()) + interval '20 hours', date_trunc('day', now()) + interval '12 hours', 24, '10000000-0000-0000-0000-000000000004'),
  ('70000000-0000-0000-0000-000000000002', 'Convívio de início de época para atletas, tutores e equipa técnica', 'Encontro aberto à comunidade do clube.', date_trunc('day', now()) + interval '3 days 10 hours', date_trunc('day', now()) + interval '3 days 14 hours', NULL, 80, '10000000-0000-0000-0000-000000000005');
INSERT INTO event_audiences (event_id, programme_id)
SELECT '70000000-0000-0000-0000-000000000001', id FROM programmes WHERE code = 'Competition';

INSERT INTO event_responses (event_id, user_id, status, responded_by_id)
VALUES ('70000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000003', 'Going', '10000000-0000-0000-0000-000000000003');

INSERT INTO training_logs (user_id, occurred_at, duration_seconds, distance_metres, notes)
SELECT '10000000-0000-0000-0000-000000000003', now() - (n || ' days')::interval, 3600 + n * 60, 8000 + n * 250, 'Treino técnico com séries progressivas e retorno à calma.' FROM generate_series(1, 10) AS n;
INSERT INTO performance_metrics (user_id, metric_type, label_pt, value, unit_pt, measured_at)
SELECT '10000000-0000-0000-0000-000000000003', 'Custom', 'Tempo nos 500 metros', 120 - n, 'segundos', now() - (n || ' days')::interval FROM generate_series(1, 6) AS n;

INSERT INTO training_plans (id, title, description, programme_id, created_by_id)
SELECT '71000000-0000-0000-0000-000000000001', 'Preparação de velocidade — bloco de carga progressiva', 'Plano representativo com uma designação longa para validar cartões, formulários e tabelas.', id, '10000000-0000-0000-0000-000000000004' FROM programmes WHERE code = 'Competition';
INSERT INTO training_sessions (id, plan_id, title, description, starts_at, ends_at, modality_id, created_by_id)
SELECT ('72000000-0000-0000-0000-' || lpad(n::text, 12, '0'))::uuid, '71000000-0000-0000-0000-000000000001', 'Sessão técnica de velocidade ' || n, 'Aquecimento, séries, recuperação ativa e retorno à calma.', date_trunc('day', now()) + ((n - 4) || ' days 18 hours')::interval, date_trunc('day', now()) + ((n - 4) || ' days 20 hours')::interval, m.id, '10000000-0000-0000-0000-000000000004' FROM generate_series(1, 8) AS n CROSS JOIN modalities m WHERE m.code = 'K1';
INSERT INTO training_sessions (id, plan_id, title, description, starts_at, ends_at, modality_id, created_by_id)
SELECT '72000000-0000-0000-0000-000000000009', '71000000-0000-0000-0000-000000000001', 'Sessão concluída para classificação', 'Sessão determinística com distância para rever a classificação.', now() - interval '2 hours', now() - interval '1 hour', m.id, '10000000-0000-0000-0000-000000000004' FROM modalities m WHERE m.code = 'K1';

INSERT INTO training_session_outcomes (session_id, user_id, status, distance_metres)
VALUES
  ('72000000-0000-0000-0000-000000000009', '10000000-0000-0000-0000-000000000003', 'COMPLETED', 12500),
  ('72000000-0000-0000-0000-000000000009', '10000000-0000-0000-0000-000000000006', 'COMPLETED', 9750);

INSERT INTO news_items (title_pt, summary_pt, url, published_at, is_published)
SELECT 'Notícia de revisão visual ' || n || ': atividade do Clube Fluvial de Coimbra', 'Resumo realista e suficientemente longo sobre treinos, provas, voluntariado e vida do clube para testar conteúdo denso e quebras de linha.', 'https://cfcoimbra.com/noticias/', now() - (n || ' days')::interval, n <= 9 FROM generate_series(1, 12) AS n;

INSERT INTO announcements (id, title, body, status, author_id, published_by_id, published_at, expires_at)
SELECT ('73000000-0000-0000-0000-' || lpad(n::text, 12, '0'))::uuid,
       'Aviso ' || n || ': alteração importante ao funcionamento dos treinos e instalações',
       'Informação detalhada para atletas, tutores e equipa técnica. Consulte os horários e confirme a receção deste aviso.',
       'PUBLISHED', '10000000-0000-0000-0000-000000000004', '10000000-0000-0000-0000-000000000004', now() - (n || ' hours')::interval, now() + interval '30 days'
FROM generate_series(1, 10) AS n;

INSERT INTO whatsapp_groups (name, discipline, programme_id, url)
SELECT 'Competição — informações, deslocações e convocatórias', 'Velocidade e fundo', id, 'https://chat.whatsapp.com/UIReviewCompetitionGroup' FROM programmes WHERE code = 'Competition';
