\set ON_ERROR_STOP on
-- Apply after classification-338-seed.sql, only on a disposable database.
INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES
 ('33800000-0000-0000-0000-000000000007','Pessoa sem participação','classification-first@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1995-01-01'),
 ('33800000-0000-0000-0000-000000000008','Pessoa Polo disponível','classification-polo@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1995-01-01');
INSERT INTO teams(id,season_id,programme_id,code,name)
 SELECT '33800000-0000-0000-0000-000000000050','33800000-0000-0000-0000-000000000010',id,'E2E338_POLO','Equipa Polo partilhada' FROM programmes WHERE code='Kayak_Polo';
INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id)
 SELECT '33800000-0000-0000-0000-000000000003','COACH',id,'33800000-0000-0000-0000-000000000001' FROM programmes WHERE code='Kayak_Polo';
