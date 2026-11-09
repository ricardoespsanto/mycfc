\set ON_ERROR_STOP on
-- Phase provisioning, not evidence for #404's real-world first-setup journey.
DO $$ BEGIN IF current_database()<>'mycfc_training_groups_test' THEN RAISE EXCEPTION 'wrong acceptance database'; END IF; END $$;
INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current) VALUES ('39139300-0000-0000-0000-000000000100','GROUPS391','Época sintética',CURRENT_DATE-30,CURRENT_DATE+60,true);
INSERT INTO teams(id,season_id,programme_id,code,name) SELECT ('39139300-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,'39139300-0000-0000-0000-000000000100',p.id,code2,name FROM programmes p CROSS JOIN (VALUES(201,'SyntheticA','Equipa sintética A'),(202,'SyntheticB','Equipa sintética B')) f(n,code2,name) WHERE p.code='Leisure';
INSERT INTO staff_grants(user_id,capability,team_id,granted_by_id) VALUES ('39139300-0000-0000-0000-000000000005','COACH','39139300-0000-0000-0000-000000000201','39139300-0000-0000-0000-000000000001');
INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at,is_active)
SELECT ('39139300-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,name,'athlete-'||n||'@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1990-01-01',now(),true
FROM (VALUES(11,'Pesquisa Alfa'),(12,'Pesquisa Beta'),(13,'Pesquisa Outra participação'),(14,'Pessoa não procurada'),(15,'Pesquisa Inativa'),(16,'Pesquisa Equipa A'),(17,'Pesquisa Equipa B'),(18,'Pesquisa Grupo ocupado'),(19,'Pesquisa Lote válido')) f(n,name);
INSERT INTO user_memberships(id,user_id,season_id,programme_id,team_id,starts_on)
SELECT ('39139300-0000-0000-0000-'||lpad((n+1000)::text,12,'0'))::uuid,('39139300-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,'39139300-0000-0000-0000-000000000100',p.id,CASE n WHEN 16 THEN '39139300-0000-0000-0000-000000000201'::uuid WHEN 17 THEN '39139300-0000-0000-0000-000000000202'::uuid ELSE NULL END,CURRENT_DATE-10 FROM programmes p CROSS JOIN generate_series(11,19) n WHERE p.code=CASE WHEN n=13 THEN 'Initiation' ELSE 'Leisure' END;
INSERT INTO training_groups(id,name,programme_id,created_by_id) SELECT ('39139300-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,name,p.id,'39139300-0000-0000-0000-000000000001' FROM programmes p CROSS JOIN (VALUES(301,'Outro grupo autorizado','Leisure'),(302,'Grupo outra participação','Initiation')) f(n,name,code2) WHERE p.code=code2;
INSERT INTO training_groups(id,name,team_id,created_by_id) VALUES ('39139300-0000-0000-0000-000000000303','Grupo equipa A','39139300-0000-0000-0000-000000000201','39139300-0000-0000-0000-000000000001'),('39139300-0000-0000-0000-000000000304','Grupo equipa B','39139300-0000-0000-0000-000000000202','39139300-0000-0000-0000-000000000001');
INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES ('39139300-0000-0000-0000-000000000301','39139300-0000-0000-0000-000000001018','39139300-0000-0000-0000-000000000001');

UPDATE users SET is_active=false WHERE id='39139300-0000-0000-0000-000000000015';
