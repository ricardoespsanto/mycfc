\set ON_ERROR_STOP on
-- Initial NEW owned fixture only. Guardian approvals here are synthetic test
-- prerequisites, not a real club/legal approval or season-date decision.
DO $$ BEGIN IF current_database()<>'mycfc' OR current_user<>'authoring_bootstrap' THEN RAISE EXCEPTION 'wrong owned partial publication fixture'; END IF; END $$;
BEGIN;
INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at)
SELECT ('39139900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,name,email,'$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1980-01-01',now()
FROM (VALUES(6000,'Alvo publicação adulto sintético','publication-adult@example.test'),(6001,'Tutor atual de publicação sintético','publication-guardian@example.test'),(6003,'Leitor estranho de publicação sintético','publication-unrelated@example.test'),(6004,'Tutor suspenso de publicação sintético','publication-suspended@example.test')) f(n,name,email);
INSERT INTO users(id,name,is_dependent,date_of_birth)
VALUES('39139900-0000-0000-0000-000000006002','Alvo publicação menor sintético',true,:'fixture_date'::date-interval '14 years'),
('39139900-0000-0000-0000-000000006005','Dependente suspenso apenas sintético',true,:'fixture_date'::date-interval '13 years');
INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on)
SELECT ('39139900-0000-0000-0000-'||lpad(member::text,12,'0'))::uuid,('39139900-0000-0000-0000-'||lpad(person::text,12,'0'))::uuid,'39139900-0000-0000-0000-000000000101',p.id,:'fixture_start',:'fixture_end'
FROM programmes p CROSS JOIN(VALUES(6100,6000),(6102,6002))f(member,person) WHERE p.code='Leisure';
INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on)
SELECT '39139900-0000-0000-0000-000000006015','39139900-0000-0000-0000-000000006005','39139900-0000-0000-0000-000000000100',id,:'fixture_start',:'fixture_end' FROM programmes WHERE code='Leisure';
-- Target adult/minor remain UNASSIGNED here; actual roster UI adds them once.
INSERT INTO guardian_authority_policies(version,evidence_types,reason_codes,validity_days,review_days,adopted_at,adopted_by,enabled)
VALUES('pub-browser-test','{TEST_EVIDENCE}','{EVIDENCE_CONFIRMED,EVIDENCE_INSUFFICIENT,AUTHORITY_CHANGED,CONFLICT,VALIDITY_ENDED}',365,180,now(),'39139900-0000-0000-0000-000000000001',false);
INSERT INTO guardian_authority_policy_approvals(policy_version,policy_sha256,approval_sha256,approval_contract,approval_canonical,expected_database,authorized_operator_actor_ref,controller_role,controller_approval_reference,controller_approved_on,effective_on,review_due_on,legal_reviewer_reference,legal_review_reference,legal_reviewed_on,legal_review_conclusion,bound_image_digest,bound_schema_migration_digest,bound_by)
SELECT 'pub-browser-test',digest('pub-browser-test','sha256'),digest('synthetic publication guardian approval','sha256'),'mycfc/guardian-authority-policy-approval/v1',convert_to('{}','UTF8'),current_database(),'39139900-0000-0000-0000-000000000001','CLUB_DIRECTION','synthetic/controller',CURRENT_DATE,CURRENT_DATE,CURRENT_DATE+365,'synthetic/reviewer','synthetic/legal',CURRENT_DATE,'APPROVED','sha256:'||repeat('a',64),(SELECT encode(digest(string_agg(version,E'\n' ORDER BY version),'sha256'),'hex') FROM mycfc_meta.schema_migrations),'39139900-0000-0000-0000-000000000001';
UPDATE guardian_ops.runtime_release_binding SET database_name=current_database(),image_digest='sha256:'||repeat('a',64),schema_migration_digest=(SELECT bound_schema_migration_digest FROM guardian_authority_policy_approvals WHERE policy_version='pub-browser-test'),generation=generation+1,bound_at=now() WHERE singleton;
UPDATE guardian_authority_policies SET enabled=true,enabled_at=now(),enabled_by='39139900-0000-0000-0000-000000000001' WHERE version='pub-browser-test';
UPDATE guardian_application_intake_release gate SET enabled=true,policy_version=approval.policy_version,policy_sha256=approval.policy_sha256,approval_sha256=approval.approval_sha256,image_digest=approval.bound_image_digest,schema_migration_digest=approval.bound_schema_migration_digest,enabled_by=approval.authorized_operator_actor_ref,enabled_at=now()
FROM guardian_authority_policy_approvals approval WHERE gate.singleton AND approval.policy_version='pub-browser-test';
INSERT INTO guardian_authority_relationships(id,guardian_user_id,subject_user_id,submitted_label,state,version,policy_version,verified_at,verified_by,verified_until,review_due_at)
VALUES('39139900-0000-0000-0000-000000006201','39139900-0000-0000-0000-000000006001','39139900-0000-0000-0000-000000006002','Menor alvo sintético','VERIFIED',2,'pub-browser-test',now(),'39139900-0000-0000-0000-000000000001',now()+interval '365 days',now()+interval '180 days'),
('39139900-0000-0000-0000-000000006204','39139900-0000-0000-0000-000000006004','39139900-0000-0000-0000-000000006005','Menor com tutor suspenso sintético','SUSPENDED',3,'pub-browser-test',now()-interval '1 day','39139900-0000-0000-0000-000000000001',now()+interval '365 days',now()+interval '180 days');
-- A suspended guardian has an OWN distinct historical subject and stored
-- prescription, isolating current authority denial from unrelated-subject denial.
WITH frozen AS(SELECT jsonb_build_object('schema_version',1,'session',jsonb_build_object('ID','39139900-0000-0000-0000-000000001000','Title','Histórico privado do dependente suspenso','Description','Conteúdo histórico sintético que exige autoridade atual.','EntryKind','TRAINING','When','Horário histórico sintético')) AS snapshot)
INSERT INTO training_prescriptions(id,publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256)
SELECT '39139900-0000-0000-0000-000000006205','39139900-0000-0000-0000-000000001600','39139900-0000-0000-0000-000000001000','39139900-0000-0000-0000-000000006015','39139900-0000-0000-0000-000000006005',snapshot,encode(digest(snapshot::text,'sha256'),'hex') FROM frozen;
COMMIT;
