\set ON_ERROR_STOP on
-- Explicit synthetic browser fixture, after actual Go bootstrap/migrate/local floor.
-- Dates are supplied by the runner; no club season boundaries are inferred.
DO $$ BEGIN IF current_database()<>'mycfc' THEN RAISE EXCEPTION 'wrong owned fixture database'; END IF; END $$;
INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at)
VALUES ('39139100-0000-0000-0000-000000000001','Treinador sintético de revisão','recipient-coach@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1980-01-01',now()),
('39139100-0000-0000-0000-000000000011','Destinatário sintético elegível','recipient-adult@example.test','synthetic-no-login','1980-01-01',now());
INSERT INTO users(id,name,is_dependent,date_of_birth) VALUES('39139100-0000-0000-0000-000000000017','DEPENDENTE_EXCLUIDO_SINTETICO',true,:'fixture_date'::date-interval '14 years');
INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current) VALUES('39139100-0000-0000-0000-000000000100','SYNTHETIC_RECIPIENT','Época exclusivamente sintética',:'fixture_start',:'fixture_end',true);
INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) SELECT '39139100-0000-0000-0000-000000000001','COACH',id,'39139100-0000-0000-0000-000000000001' FROM programmes WHERE code='Leisure';
INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) SELECT ('39139100-0000-0000-0000-'||lpad((100+n)::text,12,'0'))::uuid,('39139100-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,'39139100-0000-0000-0000-000000000100',id,:'fixture_start',:'fixture_end' FROM programmes CROSS JOIN unnest(ARRAY[11,17])n WHERE code='Leisure';
INSERT INTO training_groups(id,name,programme_id,created_by_id) SELECT '39139100-0000-0000-0000-000000000200','Grupo exclusivamente sintético',id,'39139100-0000-0000-0000-000000000001' FROM programmes WHERE code='Leisure';
INSERT INTO training_group_members(group_id,membership_id,added_by_id) SELECT '39139100-0000-0000-0000-000000000200',id,'39139100-0000-0000-0000-000000000001' FROM user_memberships;
UPDATE feature_flags SET mode='ENABLED',updated_by_id='39139100-0000-0000-0000-000000000001' WHERE feature_key='structured_training_planning';
INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id)
SELECT ('39139100-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,'Semana sintética '||n,p.id,'39139100-0000-0000-0000-000000000200','39139100-0000-0000-0000-000000000100',:'fixture_week'::date+(n-300)*7,'39139100-0000-0000-0000-000000000001' FROM programmes p CROSS JOIN unnest(ARRAY[300,301])n WHERE code='Leisure';
INSERT INTO training_sessions(id,plan_id,title,description,starts_at,ends_at,created_by_id)
SELECT ('39139100-0000-0000-0000-'||lpad((n+100)::text,12,'0'))::uuid,id,'Sessão sintética '||n,'Instruções sintéticas preservadas',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours','39139100-0000-0000-0000-000000000001' FROM training_plans CROSS JOIN LATERAL(SELECT right(id::text,3)::integer n) f;
