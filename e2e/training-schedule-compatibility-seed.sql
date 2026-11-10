\set ON_ERROR_STOP on
-- Synthetic source observations only. C baseline + explicit test-only P shape fixture.
DO $$ BEGIN IF current_database()<>'mycfc_schedule_compatibility_test' THEN RAISE EXCEPTION 'wrong isolated database'; END IF; END $$;
INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at)
SELECT ('39107900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,name,'schedule-'||actor||'@example.test',
'$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1990-01-01',now()
FROM(VALUES(1,'Administrador horário sintético','admin'),(2,'Treinador horário sintético','coach'),(3,'Treinador horário revogável','revoked'),(11,'Atleta horário Alfa','alfa'),(16,'Tutor horário sintético','guardian')) f(n,name,actor);
INSERT INTO users(id,name,is_dependent,date_of_birth) VALUES('39107900-0000-0000-0000-000000000017','Dependente horário reservado',true,CURRENT_DATE-interval '14 years');
INSERT INTO user_platform_roles(user_id,role_id) SELECT '39107900-0000-0000-0000-000000000001',id FROM platform_roles WHERE code='ADMIN';
INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) SELECT ('39107900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,'COACH',p.id,'39107900-0000-0000-0000-000000000001' FROM programmes p CROSS JOIN unnest(ARRAY[2,3]) n WHERE code='Initiation';
INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current) VALUES('39107900-0000-0000-0000-000000000100','C_READER_TEST','Época sintética de horários','2026-01-01','2027-12-31',true);
INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) SELECT ('39107900-0000-0000-0000-'||lpad((100+n)::text,12,'0'))::uuid,('39107900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,'39107900-0000-0000-0000-000000000100',p.id,'2026-01-01','2027-12-31' FROM programmes p CROSS JOIN unnest(ARRAY[11,17]) n WHERE code='Initiation';
INSERT INTO training_groups(id,name,programme_id,created_by_id) SELECT '39107900-0000-0000-0000-000000000200','Grupo horários sintético',id,'39107900-0000-0000-0000-000000000001' FROM programmes WHERE code='Initiation';
INSERT INTO training_group_members(group_id,membership_id,added_by_id) SELECT '39107900-0000-0000-0000-000000000200',id,'39107900-0000-0000-0000-000000000001' FROM user_memberships;
UPDATE feature_flags SET mode='ENABLED',updated_by_id='39107900-0000-0000-0000-000000000001' WHERE feature_key='structured_training_planning';
INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) SELECT ('39107900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,title,p.id,'39107900-0000-0000-0000-000000000200','39107900-0000-0000-0000-000000000100',day,'39107900-0000-0000-0000-000000000001' FROM programmes p CROSS JOIN(VALUES(300,'Semana quatro horários',date_trunc('week',CURRENT_DATE+2)::date),(301,'Semana mudança de hora',DATE '2026-10-19')) f(n,title,day) WHERE code='Initiation';
INSERT INTO training_sessions(id,plan_id,title,description,entry_kind,schedule_precision,scheduled_on,starts_at,ends_at,day_band,created_by_id)
SELECT ('39107900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,'39107900-0000-0000-0000-000000000300',title,'Instrução sintética preservada',kind::training_entry_kind,precision,CURRENT_DATE+2,
CASE WHEN precision IN('INTERVAL','START_ONLY') THEN (CURRENT_DATE+2+time '10:00') AT TIME ZONE 'Europe/Lisbon' END,
CASE WHEN precision='INTERVAL' THEN (CURRENT_DATE+2+time '11:00') AT TIME ZONE 'Europe/Lisbon' END,band,'39107900-0000-0000-0000-000000000001'
FROM(VALUES(400,'Intervalo exato sintético','TRAINING','INTERVAL',NULL),(401,'Início conhecido sintético','TRAINING','START_ONLY',NULL),(402,'Dia de descanso sintético','REST','DAY',NULL),(403,'Tarde logística sintética','LOGISTICS','BAND','AFTERNOON')) f(n,title,kind,precision,band);
INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) VALUES('39107900-0000-0000-0000-000000000404','39107900-0000-0000-0000-000000000301','Intervalo repetido outono','2026-10-25T01:30:00+01:00','2026-10-25T01:30:00+00:00','39107900-0000-0000-0000-000000000001');
INSERT INTO training_session_segments(id,session_id,position,modality,title) VALUES('39107900-0000-0000-0000-000000000500','39107900-0000-0000-0000-000000000400',1,'WATER','Água exata');
INSERT INTO training_segment_blocks(segment_id,position,purpose,instructions) VALUES('39107900-0000-0000-0000-000000000500',1,'MAIN','Texto do treinador preservado');
INSERT INTO guardian_authority_policies(version,evidence_types,reason_codes,validity_days,review_days,adopted_at,adopted_by,enabled) VALUES ('schedule-c-test','{TEST_EVIDENCE}','{EVIDENCE_CONFIRMED,EVIDENCE_INSUFFICIENT,AUTHORITY_CHANGED,CONFLICT,VALIDITY_ENDED}',365,180,now(),'39107900-0000-0000-0000-000000000001',false);
INSERT INTO guardian_authority_policy_approvals(policy_version,policy_sha256,approval_sha256,approval_contract,approval_canonical,expected_database,authorized_operator_actor_ref,controller_role,controller_approval_reference,controller_approved_on,effective_on,review_due_on,legal_reviewer_reference,legal_review_reference,legal_reviewed_on,legal_review_conclusion,bound_image_digest,bound_schema_migration_digest,bound_by)
SELECT 'schedule-c-test',digest('schedule-c-test','sha256'),digest('synthetic approval','sha256'),'mycfc/guardian-authority-policy-approval/v1',convert_to('{}','UTF8'),current_database(),'39107900-0000-0000-0000-000000000001','CLUB_DIRECTION','synthetic/controller',CURRENT_DATE,CURRENT_DATE,CURRENT_DATE+365,'synthetic/reviewer','synthetic/legal',CURRENT_DATE,'APPROVED','sha256:'||repeat('a',64),(SELECT encode(digest(string_agg(version,E'\n' ORDER BY version),'sha256'),'hex') FROM mycfc_meta.schema_migrations),'39107900-0000-0000-0000-000000000001';
UPDATE guardian_ops.runtime_release_binding SET database_name=current_database(),image_digest='sha256:'||repeat('a',64),schema_migration_digest=(SELECT bound_schema_migration_digest FROM guardian_authority_policy_approvals WHERE policy_version='schedule-c-test'),generation=generation+1,bound_at=now() WHERE singleton;
UPDATE guardian_authority_policies SET enabled=true,enabled_at=now(),enabled_by='39107900-0000-0000-0000-000000000001' WHERE version='schedule-c-test';
UPDATE guardian_application_intake_release gate SET enabled=true,policy_version=approval.policy_version,policy_sha256=approval.policy_sha256,approval_sha256=approval.approval_sha256,image_digest=approval.bound_image_digest,schema_migration_digest=approval.bound_schema_migration_digest,enabled_by=approval.authorized_operator_actor_ref,enabled_at=now() FROM guardian_authority_policy_approvals approval WHERE gate.singleton AND approval.policy_version='schedule-c-test';
INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label,state,version,policy_version,verified_at,verified_by,verified_until,review_due_at) VALUES ('39107900-0000-0000-0000-000000000016','39107900-0000-0000-0000-000000000017','Dependente aceitação','VERIFIED',2,'schedule-c-test',now(),'39107900-0000-0000-0000-000000000001',now()+interval '365 days',now()+interval '180 days');

INSERT INTO training_plan_publications(id,plan_id,revision,source_updated_at,change_summary,published_by_id) SELECT '39107900-0000-0000-0000-000000000600',id,1,updated_at,'Fixture sintética de leitura v1/v2','39107900-0000-0000-0000-000000000001' FROM training_plans WHERE id='39107900-0000-0000-0000-000000000300';
WITH snapshots AS (
 SELECT s.id AS session_id,m.id AS membership_id,m.user_id,
 jsonb_build_object('schema_version',CASE WHEN s.schedule_precision='INTERVAL' THEN 1 ELSE 2 END,'plan_id',s.plan_id::text,'plan_title','Semana quatro horários','group_name','Grupo horários sintético','week_title','Semana quatro horários','season','Época sintética de horários',
 'session',CASE WHEN s.schedule_precision='INTERVAL' THEN jsonb_build_object('ID',s.id::text,'Title',s.title,'Description','Texto legado imutável','When','Horário legado congelado 08:20','EntryKind',s.entry_kind::text)
 ELSE jsonb_build_object('ID',s.id::text,'Title',s.title,'Description','Texto tipado imutável','EntryKind',s.entry_kind::text,'schedule',training_session_snapshot_v2(s.id)->'schedule') END) AS snapshot
 FROM training_sessions s CROSS JOIN user_memberships m WHERE s.plan_id='39107900-0000-0000-0000-000000000300'
)
INSERT INTO training_prescriptions(id,publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256)
SELECT ('39107900-0000-0000-0000-'||lpad((700+row_number() OVER(ORDER BY session_id,user_id))::text,12,'0'))::uuid,'39107900-0000-0000-0000-000000000600',session_id,membership_id,user_id,snapshot,encode(digest(snapshot::text,'sha256'),'hex') FROM snapshots;
INSERT INTO training_routines(id,name,kind,owner_user_id,source_id,source_updated_at,snapshot)
SELECT '39107900-0000-0000-0000-000000000800','Rotina início conhecido','SESSION','39107900-0000-0000-0000-000000000002',id,updated_at,training_session_snapshot_v2(id) FROM training_sessions WHERE id='39107900-0000-0000-0000-000000000401';

INSERT INTO training_session_outcomes(session_id,user_id,status) VALUES('39107900-0000-0000-0000-000000000404','39107900-0000-0000-0000-000000000011','MISSED');
