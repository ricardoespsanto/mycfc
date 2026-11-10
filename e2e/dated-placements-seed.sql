\set ON_ERROR_STOP on
-- NEW owned synthetic fixture only. These dates do not establish club seasons.
-- Groups start empty: supported dated JOIN/move/publication actions happen in UI.
DO $$ BEGIN IF current_database()<>'mycfc' OR current_user<>'authoring_bootstrap' THEN RAISE EXCEPTION 'wrong owned placement browser fixture'; END IF; END $$;
INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at)
SELECT ('39139900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,name,email,
'$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1990-01-01',now()
FROM (VALUES
 (7000,'Colocação nativa sintética','placement-native@example.test'),
 (7001,'Colocação imediata sintética','placement-now@example.test'),
 (7002,'Pessoa estranha sintética','placement-unrelated@example.test'),
 (7003,'Treinador sem âmbito sintético','placement-unscoped@example.test'),
 (7004,'Treinador revogado sintético','placement-revoked@example.test'),
 (7005,'Colocação inativa sintética','placement-inactive@example.test'),
 (7006,'Colocação outra participação secreta','placement-foreign@example.test')
) fixture(n,name,email);
INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id,revoked_by_id,revoked_at,revoke_reason)
SELECT '39139900-0000-0000-0000-000000007004','COACH',id,'39139900-0000-0000-0000-000000000001','39139900-0000-0000-0000-000000000001',now(),'Synthetic already revoked control' FROM programmes WHERE code='Leisure';
INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on)
SELECT ('39139900-0000-0000-0000-'||lpad((n+100)::text,12,'0'))::uuid,
 ('39139900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,
 '39139900-0000-0000-0000-000000000100',p.id,:'fixture_start'
FROM programmes p CROSS JOIN unnest(ARRAY[7000,7001,7005,7006]) n
WHERE p.code=CASE WHEN n=7006 THEN 'Initiation' ELSE 'Leisure' END;
UPDATE users SET is_active=false WHERE id='39139900-0000-0000-0000-000000007005';
INSERT INTO training_groups(id,name,programme_id,created_by_id)
SELECT ('39139900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,name,p.id,'39139900-0000-0000-0000-000000000001'
FROM programmes p CROSS JOIN (VALUES(201,'Destino temporário sintético','Leisure'),(202,'Grupo de outra participação secreto','Initiation')) fixture(n,name,code)
WHERE p.code=fixture.code;
INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id)
SELECT '39139900-0000-0000-0000-000000000350','Semana do destino sintético',p.id,'39139900-0000-0000-0000-000000000201','39139900-0000-0000-0000-000000000100',:'fixture_week','39139900-0000-0000-0000-000000000001' FROM programmes p WHERE code='Leisure';
