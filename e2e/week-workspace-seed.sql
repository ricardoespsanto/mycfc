\set ON_ERROR_STOP on
-- Disposable synthetic identities and an empty group; intentionally no seasons.
DO $$ BEGIN IF current_database()<>'mycfc_week_workspace_test' THEN RAISE EXCEPTION 'wrong acceptance database'; END IF; END $$;
INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at)
SELECT ('39800000-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,name,'week-'||actor||'@example.test',
'$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1990-01-01',now()
FROM (VALUES(1,'Administrador semana sintético','admin'),(2,'Treinador semana sintético','coach'),(3,'Treinador revogável sintético','revoked'),(11,'Atleta semanal Alfa','alfa'),(12,'Atleta semanal Beta','beta')) f(n,name,actor);
INSERT INTO user_platform_roles(user_id,role_id) SELECT '39800000-0000-0000-0000-000000000001',id FROM platform_roles WHERE code='ADMIN';
INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) SELECT ('39800000-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,'COACH',p.id,'39800000-0000-0000-0000-000000000001' FROM programmes p CROSS JOIN unnest(ARRAY[2,3]) n WHERE code='Leisure';
INSERT INTO training_groups(id,name,programme_id,created_by_id) SELECT ('39800000-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,name,p.id,'39800000-0000-0000-0000-000000000001' FROM programmes p CROSS JOIN(VALUES(200,'Grupo semanal sintético','Leisure'),(201,'Grupo semanal reservado','Initiation'),(202,'Grupo sem destinatários','Leisure')) f(n,name,code2) WHERE code=code2;
UPDATE feature_flags SET mode='ENABLED',updated_by_id='39800000-0000-0000-0000-000000000001' WHERE feature_key='structured_training_planning';
