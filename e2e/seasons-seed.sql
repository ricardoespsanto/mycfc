\set ON_ERROR_STOP on
-- Synthetic actors only; no season exists in this fixture.
DO $$ BEGIN IF current_database()<>'mycfc_seasons_test' THEN RAISE EXCEPTION 'wrong acceptance database'; END IF; END $$;
INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at)
SELECT ('39400000-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,name,'seasons-'||actor||'@example.test',
'$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1990-01-01',now()
FROM (VALUES(1,'Administrador épocas sintético','admin'),(2,'Treinador épocas sintético','coach'),(11,'Pessoa épocas sintética','member')) f(n,name,actor);
INSERT INTO user_platform_roles(user_id,role_id) SELECT '39400000-0000-0000-0000-000000000001',id FROM platform_roles WHERE code='ADMIN';
INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) SELECT '39400000-0000-0000-0000-000000000002','COACH',id,'39400000-0000-0000-0000-000000000001' FROM programmes WHERE code='Leisure';
INSERT INTO training_groups(id,name,programme_id,created_by_id) SELECT '39400000-0000-0000-0000-000000000200','Grupo épocas sintético',id,'39400000-0000-0000-0000-000000000001' FROM programmes WHERE code='Leisure';
UPDATE feature_flags SET mode='DISABLED',updated_by_id='39400000-0000-0000-0000-000000000001' WHERE feature_key='structured_training_planning';
