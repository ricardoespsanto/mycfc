\set ON_ERROR_STOP on
-- Synthetic preview/correction fixtures, isolated from existing serial journeys.
INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES
 ('33800000-0000-0000-0000-000000000071','Atleta pré-visualização','preview-athlete@example.test','hash','1990-01-01'),
 ('33800000-0000-0000-0000-000000000072','Atleta conflito','conflict-athlete@example.test','hash','1990-01-01');
INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on)
SELECT u.id,s.id,p.id,(now() AT TIME ZONE 'Europe/Lisbon')::date-5 FROM users u
CROSS JOIN seasons s CROSS JOIN programmes p
WHERE u.id IN ('33800000-0000-0000-0000-000000000071','33800000-0000-0000-0000-000000000072') AND s.is_current AND p.code='Leisure';
