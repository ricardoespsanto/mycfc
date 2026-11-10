\set ON_ERROR_STOP on
-- After real HTTP selection of season A; stored synthetic history, not a publishing-UI test.
DO $$ BEGIN IF current_database()<>'mycfc_seasons_test' THEN RAISE EXCEPTION 'wrong acceptance database'; END IF; END $$;
INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on)
SELECT '39400000-0000-0000-0000-000000000111','39400000-0000-0000-0000-000000000011',s.id,p.id,s.starts_on FROM seasons s CROSS JOIN programmes p WHERE s.code='SYN_A' AND p.code='Leisure';
INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES ('39400000-0000-0000-0000-000000000200','39400000-0000-0000-0000-000000000111','39400000-0000-0000-0000-000000000001');
INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id)
SELECT '39400000-0000-0000-0000-000000000300','Semana histórica sintética',p.id,'39400000-0000-0000-0000-000000000200',s.id,date_trunc('week',CURRENT_DATE)::date,'39400000-0000-0000-0000-000000000001' FROM seasons s CROSS JOIN programmes p WHERE s.code='SYN_A' AND p.code='Leisure';
INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) VALUES ('39400000-0000-0000-0000-000000000400','39400000-0000-0000-0000-000000000300','Treino histórico sintético',(CURRENT_DATE-1+time '10:00') AT TIME ZONE 'Europe/Lisbon',(CURRENT_DATE-1+time '11:00') AT TIME ZONE 'Europe/Lisbon','39400000-0000-0000-0000-000000000001');
INSERT INTO training_plan_publications(id,plan_id,revision,source_updated_at,change_summary,published_by_id)
SELECT '39400000-0000-0000-0000-000000000500',id,1,updated_at,'Publicação histórica sintética','39400000-0000-0000-0000-000000000001' FROM training_plans WHERE id='39400000-0000-0000-0000-000000000300';
INSERT INTO training_prescriptions(id,publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256)
VALUES ('39400000-0000-0000-0000-000000000600','39400000-0000-0000-0000-000000000500','39400000-0000-0000-0000-000000000400','39400000-0000-0000-0000-000000000111','39400000-0000-0000-0000-000000000011','{"schema_version":1,"session":{"title":"Instrução histórica preservada"}}',encode(digest('{"schema_version":1,"session":{"title":"Instrução histórica preservada"}}','sha256'),'hex'));
