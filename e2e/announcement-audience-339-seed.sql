\set ON_ERROR_STOP on
-- Synthetic isolated fixture only.
DO $$ BEGIN IF current_database() <> 'mycfc_announcement_339_test' THEN RAISE EXCEPTION 'wrong database'; END IF; END $$;
INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at)
SELECT ('33933900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,name,'announcement-'||actor||'@example.test',
'$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1990-01-01',now()
FROM (VALUES(1,'Admin sintético','admin'),(10,'Atleta sintético','athlete'),(12,'Tutor sintético','guardian'),(14,'Sem participação','unanchored')) f(n,name,actor);
INSERT INTO users(id,name,is_dependent,date_of_birth,is_active) VALUES
('33933900-0000-0000-0000-000000000011','Dependente K2',true,CURRENT_DATE-interval '14 years',true),
('33933900-0000-0000-0000-000000000013','Dependente inativo SUP',true,CURRENT_DATE-interval '14 years',true);
INSERT INTO user_platform_roles(user_id,role_id) SELECT '33933900-0000-0000-0000-000000000001',id FROM platform_roles WHERE code='ADMIN';
INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current) VALUES('33933900-0000-0000-0000-000000000100','ANN339','Época sintética',CURRENT_DATE-30,CURRENT_DATE+30,true);
INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on)
SELECT ('33933900-0000-0000-0000-'||lpad((n+100)::text,12,'0'))::uuid,('33933900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,'33933900-0000-0000-0000-000000000100',p.id,CURRENT_DATE-10 FROM programmes p CROSS JOIN unnest(ARRAY[10,11,12,13]) n WHERE p.code='Leisure';
-- Deliberately stale legacy selection: must not grant current K1 visibility.
INSERT INTO membership_modalities(membership_id,modality_id) SELECT '33933900-0000-0000-0000-000000000110',id FROM modalities WHERE code='K1';
INSERT INTO person_sporting_modalities(user_id,modality_code) VALUES
('33933900-0000-0000-0000-000000000011','CANOEING'),('33933900-0000-0000-0000-000000000012','SUP'),('33933900-0000-0000-0000-000000000013','SUP'),('33933900-0000-0000-0000-000000000014','SUP');
INSERT INTO person_canoe_craft_classes(user_id,craft_code) VALUES('33933900-0000-0000-0000-000000000011','K2');
INSERT INTO announcements(id,title,body,status,author_id,published_by_id,published_at)
SELECT ('33933900-0000-0000-0000-'||lpad((n+200)::text,12,'0'))::uuid,'Aviso '||lpad(n::text,2,'0')||' '||label,'Comunicação sintética de aceitação','PUBLISHED','33933900-0000-0000-0000-000000000001','33933900-0000-0000-0000-000000000001',CURRENT_DATE+time '00:00'-n*interval '1 minute'
FROM (VALUES(1,'K1'),(2,'K2'),(3,'K4'),(4,'C1'),(5,'C2'),(6,'C4'),(7,'SUP'),(8,'misto OR'),(9,'tutores'),(10,'tutores SUP'),(11,'tutores K2')) f(n,label);
INSERT INTO announcement_targets(announcement_id,target_type,target_id)
SELECT ('33933900-0000-0000-0000-'||lpad((n+200)::text,12,'0'))::uuid,'MODALITY',m.id FROM (VALUES(1,'K1'),(2,'K2'),(3,'K4'),(4,'C1'),(5,'C2'),(6,'C4'),(7,'SUP'),(8,'SUP'),(10,'SUP'),(11,'K2')) f(n,code) JOIN modalities m USING(code);
INSERT INTO announcement_targets(announcement_id,target_type,target_id) SELECT '33933900-0000-0000-0000-000000000208','PROGRAMME',id FROM programmes WHERE code='Leisure';
INSERT INTO announcement_targets(announcement_id,target_type) SELECT ('33933900-0000-0000-0000-'||lpad((n+200)::text,12,'0'))::uuid,'GUARDIAN' FROM unnest(ARRAY[9,10,11]) n;
-- Policy/relationship state seed, not UI verification or a real policy approval.
INSERT INTO guardian_authority_policies(version,evidence_types,reason_codes,validity_days,review_days,adopted_at,adopted_by,enabled) VALUES ('announcement339-test','{TEST_EVIDENCE}','{EVIDENCE_CONFIRMED,EVIDENCE_INSUFFICIENT,AUTHORITY_CHANGED,CONFLICT,VALIDITY_ENDED}',365,180,now(),'33933900-0000-0000-0000-000000000001',false);
INSERT INTO guardian_authority_policy_approvals(policy_version,policy_sha256,approval_sha256,approval_contract,approval_canonical,expected_database,authorized_operator_actor_ref,controller_role,controller_approval_reference,controller_approved_on,effective_on,review_due_on,legal_reviewer_reference,legal_review_reference,legal_reviewed_on,legal_review_conclusion,bound_image_digest,bound_schema_migration_digest,bound_by)
SELECT 'announcement339-test',digest('announcement339-test','sha256'),digest('synthetic approval','sha256'),'mycfc/guardian-authority-policy-approval/v1',convert_to('{}','UTF8'),current_database(),'33933900-0000-0000-0000-000000000001','CLUB_DIRECTION','synthetic/controller',CURRENT_DATE,CURRENT_DATE,CURRENT_DATE+365,'synthetic/reviewer','synthetic/legal',CURRENT_DATE,'APPROVED','sha256:'||repeat('a',64),(SELECT encode(digest(string_agg(version,E'\n' ORDER BY version),'sha256'),'hex') FROM mycfc_meta.schema_migrations),'33933900-0000-0000-0000-000000000001';
UPDATE guardian_ops.runtime_release_binding SET database_name=current_database(),image_digest='sha256:'||repeat('a',64),schema_migration_digest=(SELECT bound_schema_migration_digest FROM guardian_authority_policy_approvals WHERE policy_version='announcement339-test'),generation=generation+1,bound_at=now() WHERE singleton;
UPDATE guardian_authority_policies SET enabled=true,enabled_at=now(),enabled_by='33933900-0000-0000-0000-000000000001' WHERE version='announcement339-test';
UPDATE guardian_application_intake_release gate SET enabled=true,policy_version=approval.policy_version,policy_sha256=approval.policy_sha256,approval_sha256=approval.approval_sha256,image_digest=approval.bound_image_digest,schema_migration_digest=approval.bound_schema_migration_digest,enabled_by=approval.authorized_operator_actor_ref,enabled_at=now() FROM guardian_authority_policy_approvals approval WHERE gate.singleton AND approval.policy_version='announcement339-test';
INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label,state,version,policy_version,verified_at,verified_by,verified_until,review_due_at) VALUES ('33933900-0000-0000-0000-000000000012','33933900-0000-0000-0000-000000000011','Dependente aceitação','VERIFIED',2,'announcement339-test',now(),'33933900-0000-0000-0000-000000000001',now()+interval '365 days',now()+interval '180 days');

INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label,state,version,policy_version,verified_at,verified_by,verified_until,review_due_at) SELECT guardian_user_id,'33933900-0000-0000-0000-000000000013','Dependente inativo','VERIFIED',2,policy_version,verified_at,verified_by,verified_until,review_due_at FROM guardian_authority_relationships WHERE subject_user_id='33933900-0000-0000-0000-000000000011';

UPDATE users SET is_active=false WHERE id='33933900-0000-0000-0000-000000000013';
-- A real historical receipt must survive initial invisibility, re-add and cascade byte-for-byte.
INSERT INTO announcement_deliveries(announcement_id,user_id,delivered_at,read_at) VALUES('33933900-0000-0000-0000-000000000201','33933900-0000-0000-0000-000000000010',CURRENT_DATE-interval '2 days',CURRENT_DATE-interval '1 day');
