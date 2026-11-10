\set ON_ERROR_STOP on
-- Disposable synthetic fixtures. No club identities, source screenshot or private data.
DO $$ BEGIN IF current_database()<>'mycfc_training_groups_test' THEN RAISE EXCEPTION 'wrong acceptance database'; END IF; END $$;
INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at)
SELECT ('39139300-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,name,'groups-'||actor||'@example.test',
'$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1990-01-01',now()
FROM (VALUES (1,'Administrador sintético','admin'),(2,'Treinador sintético','coach'),(3,'Sem âmbito sintético','unscoped'),(4,'Revogado sintético','revoked'),(5,'Treinador equipa sintético','team')) f(n,name,actor);
INSERT INTO user_platform_roles(user_id,role_id) SELECT '39139300-0000-0000-0000-000000000001',id FROM platform_roles WHERE code='ADMIN';
INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id)
SELECT ('39139300-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,'COACH',p.id,'39139300-0000-0000-0000-000000000001' FROM programmes p CROSS JOIN unnest(ARRAY[2,4]) n WHERE code='Leisure';
UPDATE staff_grants SET revoked_by_id='39139300-0000-0000-0000-000000000001',revoked_at=now(),revoke_reason='Synthetic revocation' WHERE user_id='39139300-0000-0000-0000-000000000004';
UPDATE feature_flags SET mode='ENABLED',updated_by_id='39139300-0000-0000-0000-000000000001' WHERE feature_key='structured_training_planning';
-- Intentionally no season, memberships or training groups in clean phase.
