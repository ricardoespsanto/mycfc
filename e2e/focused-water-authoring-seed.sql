\set ON_ERROR_STOP on
-- Synthetic identities/weeks only, after real Go bootstrap/migrate/fresh preparation.
-- None of these dates establishes actual club season boundaries.
DO $$ BEGIN IF current_database()<>'mycfc' OR current_user<>'authoring_bootstrap' THEN RAISE EXCEPTION 'wrong owned browser fixture'; END IF; END $$;
INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at)
VALUES ('39139900-0000-0000-0000-000000000001','Treinador exclusivamente sintético','water-coach@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1980-01-01',now());
INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current)
VALUES('39139900-0000-0000-0000-000000000100','SYNTHETIC_WATER','Época exclusivamente sintética',:'fixture_start',:'fixture_end',true);
INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id)
SELECT '39139900-0000-0000-0000-000000000001','COACH',id,'39139900-0000-0000-0000-000000000001' FROM programmes WHERE code='Leisure';
INSERT INTO training_groups(id,name,programme_id,created_by_id)
SELECT '39139900-0000-0000-0000-000000000200','Grupo de água exclusivamente sintético',id,'39139900-0000-0000-0000-000000000001' FROM programmes WHERE code='Leisure';
UPDATE feature_flags SET mode='ENABLED',updated_by_id='39139900-0000-0000-0000-000000000001' WHERE feature_key='structured_training_planning';
INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id)
SELECT ('39139900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,'Semana sintética de água '||n,p.id,'39139900-0000-0000-0000-000000000200','39139900-0000-0000-0000-000000000100',:'fixture_week'::date+(n-300)*7,'39139900-0000-0000-0000-000000000001'
FROM programmes p CROSS JOIN unnest(ARRAY[300,301,302])n WHERE code='Leisure';
