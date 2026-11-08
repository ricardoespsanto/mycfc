\set ON_ERROR_STOP on
-- Synthetic preview/correction fixtures, isolated from existing serial journeys.
INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES
 ('33800000-0000-0000-0000-000000000071','Atleta pré-visualização','preview-athlete@example.test','hash','1990-01-01'),
 ('33800000-0000-0000-0000-000000000072','Atleta conflito','conflict-athlete@example.test','hash','1990-01-01'),
 ('33800000-0000-0000-0000-000000000073','Atleta transição entre programas','cross-athlete@example.test','hash','1990-01-01'),
 ('33800000-0000-0000-0000-000000000074','Atleta mesmo programa','same-athlete@example.test','hash','1990-01-01'),
 ('33800000-0000-0000-0000-000000000075','Treinador dois programas','dual-coach@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1987-01-01');
INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on)
SELECT u.id,s.id,p.id,(now() AT TIME ZONE 'Europe/Lisbon')::date-5 FROM users u
CROSS JOIN seasons s CROSS JOIN programmes p
WHERE u.id IN ('33800000-0000-0000-0000-000000000071','33800000-0000-0000-0000-000000000072','33800000-0000-0000-0000-000000000073','33800000-0000-0000-0000-000000000074') AND s.is_current AND p.code='Leisure';
INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id)
SELECT '33800000-0000-0000-0000-000000000075','COACH',p.id,'33800000-0000-0000-0000-000000000001'
FROM programmes p WHERE p.code IN ('Leisure','Initiation');
