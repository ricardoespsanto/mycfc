\set ON_ERROR_STOP on
-- Disposable #338-only database. All identities and credentials are synthetic.
INSERT INTO users (id,name,email,password_hash,date_of_birth) VALUES
 ('33800000-0000-0000-0000-000000000001','Admin classificação','classification-admin@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1985-01-01'),
 ('33800000-0000-0000-0000-000000000002','Atleta classificação','classification-athlete@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1995-01-01'),
 ('33800000-0000-0000-0000-000000000003','Treinador classificação','classification-coach@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1987-01-01'),
 ('33800000-0000-0000-0000-000000000004','Treinador revogado','classification-revoked@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1987-01-01'),
 ('33800000-0000-0000-0000-000000000005','Membro sem acesso','classification-outsider@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1987-01-01'),
 ('33800000-0000-0000-0000-000000000006','Atleta exceção','classification-age@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1995-01-01');
INSERT INTO user_platform_roles(user_id,role_id)
SELECT '33800000-0000-0000-0000-000000000001',id FROM platform_roles WHERE code='ADMIN';
INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current)
VALUES ('33800000-0000-0000-0000-000000000010','E2E338','Época de classificação E2E',CURRENT_DATE-30,CURRENT_DATE+30,true);
INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on)
SELECT '33800000-0000-0000-0000-000000000020','33800000-0000-0000-0000-000000000002',
 '33800000-0000-0000-0000-000000000010',id,CURRENT_DATE-5 FROM programmes WHERE code='Leisure';
INSERT INTO staff_grants(id,user_id,capability,programme_id,granted_by_id,revoked_by_id,revoked_at,revoke_reason)
SELECT '33800000-0000-0000-0000-000000000030','33800000-0000-0000-0000-000000000004','COACH',id,
 '33800000-0000-0000-0000-000000000001','33800000-0000-0000-0000-000000000001',now(),'Fixture de revogação'
FROM programmes WHERE code='Leisure';
INSERT INTO staff_grants(id,user_id,capability,programme_id,granted_by_id)
SELECT '33800000-0000-0000-0000-000000000031','33800000-0000-0000-0000-000000000003','COACH',id,
 '33800000-0000-0000-0000-000000000001' FROM programmes WHERE code='Leisure';
INSERT INTO competition_categories(id,season_id,programme_id,code,name_pt,birth_date_from,birth_date_to,approved_by_user_id,approved_at)
SELECT '33800000-0000-0000-0000-000000000040','33800000-0000-0000-0000-000000000010',id,'AGE_TEST','Escalão fora da idade','2010-01-01','2010-12-31','33800000-0000-0000-0000-000000000001',now()
FROM programmes WHERE code='Competition';
