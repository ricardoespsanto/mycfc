\set ON_ERROR_STOP on
-- NEW owned synthetic acceptance identities only; no historical preservation exercise.
-- No target group, season, classification, sport/craft, profile or week is seeded.
DO $$ BEGIN IF current_database()<>'mycfc' OR current_user<>'authoring_bootstrap' THEN RAISE EXCEPTION 'wrong owned real-plan fixture'; END IF; END $$;
BEGIN;
INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at)
VALUES('39139900-0000-0000-0000-000000000001','Coordenação de aceitação sintética','real-plan-admin@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1980-01-01',now());
INSERT INTO user_platform_roles(user_id,role_id) SELECT '39139900-0000-0000-0000-000000000001',id FROM platform_roles WHERE code='ADMIN';
INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id)
SELECT '39139900-0000-0000-0000-000000000001','COACH',id,'39139900-0000-0000-0000-000000000001' FROM programmes WHERE code='Competition';
INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at)
SELECT ('39140400-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,name,email,'$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1990-01-01',now()
FROM (VALUES
 (8101,'Referência um C sintética','real-plan-one-c@example.test'),(8102,'Referência um KC sintética','real-plan-one-both@example.test'),
 (8103,'Referência dois K sintética','real-plan-two-k@example.test'),(8104,'Referência dois C sintética','real-plan-two-c@example.test'),
 (8105,'Referência dois KC sintética','real-plan-two-both@example.test'),
 (8200,'Tutor atual sintético','real-plan-guardian@example.test'),(8201,'Tutor suspenso sintético','real-plan-suspended@example.test'),
 (8300,'Pessoa estranha sintética','real-plan-unrelated@example.test'),(8301,'Treinador revogado sintético','real-plan-revoked@example.test')) fixture(n,name,email);
INSERT INTO users(id,name,is_dependent,date_of_birth)
VALUES('39140400-0000-0000-0000-000000008100','Referência um K menor sintética',true,'2012-01-01'),
('39140400-0000-0000-0000-000000008106','Dependente suspenso separado sintético',true,'2012-01-01');
INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id)
SELECT '39140400-0000-0000-0000-000000008301','COACH',id,'39139900-0000-0000-0000-000000000001' FROM programmes WHERE code='Competition';
UPDATE staff_grants SET revoked_by_id='39139900-0000-0000-0000-000000000001',revoked_at=now(),revoke_reason='Synthetic initial revoked control' WHERE user_id='39140400-0000-0000-0000-000000008301';
UPDATE feature_flags SET mode='ENABLED',updated_by_id='39139900-0000-0000-0000-000000000001' WHERE feature_key='structured_training_planning';
INSERT INTO guardian_authority_policies(version,evidence_types,reason_codes,validity_days,review_days,adopted_at,adopted_by,enabled)
VALUES('real-plan-browser-test','{TEST_EVIDENCE}','{EVIDENCE_CONFIRMED,EVIDENCE_INSUFFICIENT,AUTHORITY_CHANGED,CONFLICT,VALIDITY_ENDED}',365,180,now(),'39139900-0000-0000-0000-000000000001',false);
INSERT INTO guardian_authority_policy_approvals(policy_version,policy_sha256,approval_sha256,approval_contract,approval_canonical,expected_database,authorized_operator_actor_ref,controller_role,controller_approval_reference,controller_approved_on,effective_on,review_due_on,legal_reviewer_reference,legal_review_reference,legal_reviewed_on,legal_review_conclusion,bound_image_digest,bound_schema_migration_digest,bound_by)
SELECT 'real-plan-browser-test',digest('real-plan-browser-test','sha256'),digest('synthetic publication guardian approval','sha256'),'mycfc/guardian-authority-policy-approval/v1',convert_to('{}','UTF8'),current_database(),'39139900-0000-0000-0000-000000000001','CLUB_DIRECTION','synthetic/controller',CURRENT_DATE,CURRENT_DATE,CURRENT_DATE+365,'synthetic/reviewer','synthetic/legal',CURRENT_DATE,'APPROVED','sha256:'||repeat('a',64),(SELECT encode(digest(string_agg(version,E'\n' ORDER BY version),'sha256'),'hex') FROM mycfc_meta.schema_migrations),'39139900-0000-0000-0000-000000000001';
UPDATE guardian_ops.runtime_release_binding SET database_name=current_database(),image_digest='sha256:'||repeat('a',64),schema_migration_digest=(SELECT bound_schema_migration_digest FROM guardian_authority_policy_approvals WHERE policy_version='real-plan-browser-test'),generation=generation+1,bound_at=now() WHERE singleton;
UPDATE guardian_authority_policies SET enabled=true,enabled_at=now(),enabled_by='39139900-0000-0000-0000-000000000001' WHERE version='real-plan-browser-test';
UPDATE guardian_application_intake_release gate SET enabled=true,policy_version=approval.policy_version,policy_sha256=approval.policy_sha256,approval_sha256=approval.approval_sha256,image_digest=approval.bound_image_digest,schema_migration_digest=approval.bound_schema_migration_digest,enabled_by=approval.authorized_operator_actor_ref,enabled_at=now()
FROM guardian_authority_policy_approvals approval WHERE gate.singleton AND approval.policy_version='real-plan-browser-test';

INSERT INTO guardian_authority_relationships(id,guardian_user_id,subject_user_id,submitted_label,state,version,policy_version,verified_at,verified_by,verified_until,review_due_at)
VALUES('39140400-0000-0000-0000-000000008210','39140400-0000-0000-0000-000000008200','39140400-0000-0000-0000-000000008100','Menor de aceitação sintética','VERIFIED',2,'real-plan-browser-test',now(),'39139900-0000-0000-0000-000000000001',now()+interval '365 days',now()+interval '180 days'),
('39140400-0000-0000-0000-000000008211','39140400-0000-0000-0000-000000008201','39140400-0000-0000-0000-000000008106','Menor com tutor suspenso sintético','SUSPENDED',3,'real-plan-browser-test',now()-interval '1 day','39139900-0000-0000-0000-000000000001',now()+interval '365 days',now()+interval '180 days');
COMMIT;
