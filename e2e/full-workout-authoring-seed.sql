\set ON_ERROR_STOP on
-- Extends the base fresh fixture with three empty weeks and one EXPLICITLY
-- SYNTHETIC profile. This is not a club rhythm table or season-date approval.
DO $$ BEGIN IF current_database()<>'mycfc' OR current_user<>'authoring_bootstrap' THEN RAISE EXCEPTION 'wrong owned full workout fixture'; END IF; END $$;
INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id)
SELECT ('39139900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,'Semana exclusivamente sintética completa '||n,p.id,'39139900-0000-0000-0000-000000000200','39139900-0000-0000-0000-000000000100',:'fixture_week'::date+(n-300)*7,'39139900-0000-0000-0000-000000000001'
FROM programmes p CROSS JOIN unnest(ARRAY[303,304,305])n WHERE code='Leisure';
INSERT INTO water_intensity_profiles(id,name,craft,revision,notes,created_by_id)
VALUES('39139900-0000-0000-0000-000000000900','SYNTHETIC ONLY - not club approved','KAYAK',1,'Exclusivamente para ensaio automatizado; não define ritmos do clube.','39139900-0000-0000-0000-000000000001');
INSERT INTO water_intensity_zones(id,profile_id,position,code,label,cadence_min,cadence_max,meaning)
VALUES('39139900-0000-0000-0000-000000000901','39139900-0000-0000-0000-000000000900',1,'SYN2','Zona exclusivamente sintética',70,80,'Valores sintéticos sem aprovação do treinador ou do clube.');
