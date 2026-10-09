\set ON_ERROR_STOP on
-- Additive, synthetic #335 fixture; apply after classification-338-seed.sql.
INSERT INTO users (id,name,email,password_hash,date_of_birth) VALUES
 ('33500000-0000-0000-0000-000000000001','Treinador competição','definitions-coach@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1987-01-01');
INSERT INTO staff_grants(id,user_id,capability,programme_id,granted_by_id)
SELECT '33500000-0000-0000-0000-000000000002','33500000-0000-0000-0000-000000000001','COACH',id,
 '33800000-0000-0000-0000-000000000001' FROM programmes WHERE code='Competition';
