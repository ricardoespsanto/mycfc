\set ON_ERROR_STOP on
-- NEW owned fixture only. Dates/profiles/identities are explicitly synthetic,
-- not club season boundaries, rhythm approval or coach acceptance.
DO $$ BEGIN IF current_database()<>'mycfc' OR current_user<>'authoring_bootstrap' THEN RAISE EXCEPTION 'wrong owned workout EDIT fixture'; END IF; END $$;
BEGIN;
INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id)
SELECT ('39139900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,'Semana EDIT exclusivamente sintética '||n,p.id,'39139900-0000-0000-0000-000000000200','39139900-0000-0000-0000-000000000100',:'fixture_week'::date+(n-300)*7,'39139900-0000-0000-0000-000000000001'
FROM programmes p CROSS JOIN unnest(ARRAY[306,307,308])n WHERE code='Leisure';
INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id)
SELECT '39139900-0000-0000-0000-000000000309','Semana atual apenas para política sintética',id,'39139900-0000-0000-0000-000000000200','39139900-0000-0000-0000-000000000100',date_trunc('week',CURRENT_DATE)::date,'39139900-0000-0000-0000-000000000001' FROM programmes WHERE code='Leisure';
INSERT INTO water_intensity_profiles(id,name,craft,revision,notes,created_by_id)
VALUES('39139900-0000-0000-0000-000000000950','SYNTHETIC PIN ONLY - not club approved','KAYAK',1,'Revisão sintética inativa para preservar instruções existentes.','39139900-0000-0000-0000-000000000001');
INSERT INTO water_intensity_zones(id,profile_id,position,code,label,cadence_min,cadence_max,meaning)
VALUES('39139900-0000-0000-0000-000000000951','39139900-0000-0000-0000-000000000950',1,'SYN_PIN','Zona sintética fixada',72,82,'Sem aprovação do clube.');
UPDATE water_intensity_profiles SET is_active=false WHERE id='39139900-0000-0000-0000-000000000950';
INSERT INTO training_sessions(id,plan_id,title,description,starts_at,ends_at,entry_kind,created_by_id,schedule_precision,scheduled_on,modality_id)
SELECT '39139900-0000-0000-0000-000000001000',id,'Treino misto EDIT exclusivamente sintético','Notas originais sintéticas',((week_start::text||' 10:00')::timestamp AT TIME ZONE 'Europe/Lisbon'),((week_start::text||' 11:00')::timestamp AT TIME ZONE 'Europe/Lisbon'),'TRAINING','39139900-0000-0000-0000-000000000001','INTERVAL',week_start,(SELECT id FROM modalities ORDER BY id LIMIT 1)
FROM training_plans WHERE id='39139900-0000-0000-0000-000000000306';
INSERT INTO training_sessions(id,plan_id,title,description,starts_at,entry_kind,created_by_id,schedule_precision,scheduled_on)
SELECT ('39139900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,id,CASE n WHEN 1001 THEN 'Concorrente de plano sintético' ELSE 'Concorrente de fonte sintética' END,'Notas concorrentes originais',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '12 hours','TRAINING','39139900-0000-0000-0000-000000000001','START_ONLY',week_start
FROM training_plans CROSS JOIN unnest(ARRAY[1001,1002])n WHERE id=CASE n WHEN 1001 THEN '39139900-0000-0000-0000-000000000306'::uuid ELSE '39139900-0000-0000-0000-000000000307'::uuid END;
INSERT INTO training_session_segments(id,session_id,position,modality,title)
VALUES
('39139900-0000-0000-0000-000000001010','39139900-0000-0000-0000-000000001000',1,'WATER','Água sintética'),
('39139900-0000-0000-0000-000000001020','39139900-0000-0000-0000-000000001000',2,'GYM','Força sintética'),
('39139900-0000-0000-0000-000000001030','39139900-0000-0000-0000-000000001001',1,'RUN','Corrida concorrente sintética'),
('39139900-0000-0000-0000-000000001040','39139900-0000-0000-0000-000000001002',1,'RUN','Corrida de fonte sintética');
INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions)
VALUES
('39139900-0000-0000-0000-000000001100','39139900-0000-0000-0000-000000001010',1,'MAIN','Água original com revisão fixada sintética'),
('39139900-0000-0000-0000-000000001200','39139900-0000-0000-0000-000000001020',1,'MAIN','Força original sintética'),
('39139900-0000-0000-0000-000000001300','39139900-0000-0000-0000-000000001030',1,'MAIN','Corrida concorrente original'),
('39139900-0000-0000-0000-000000001400','39139900-0000-0000-0000-000000001040',1,'MAIN','Corrida de fonte original');
INSERT INTO water_block_prescriptions(block_id,method,intensity_profile_id,target_distance_metres,target_distance_certainty)
VALUES('39139900-0000-0000-0000-000000001100','INTERVALS','39139900-0000-0000-0000-000000000950',4000,'EXACT');
INSERT INTO water_work_steps(id,block_id,parent_step_id,position,kind,name,repeats,recovery_seconds)
VALUES('39139900-0000-0000-0000-000000001110','39139900-0000-0000-0000-000000001100',NULL,1,'REPEAT_GROUP','Grupo retido sintético',2,60),
('39139900-0000-0000-0000-000000001113','39139900-0000-0000-0000-000000001100',NULL,3,'REPEAT_GROUP','Grupo removido explicitamente',3,30);
INSERT INTO water_work_steps(id,block_id,parent_step_id,position,kind,name,duration_seconds,duration_certainty,intensity_code,instructions)
VALUES('39139900-0000-0000-0000-000000001111','39139900-0000-0000-0000-000000001100','39139900-0000-0000-0000-000000001110',1,'EFFORT','Esforço filho retido',180,'EXACT','SYN_PIN','Instruções filhas sintéticas'),
('39139900-0000-0000-0000-000000001112','39139900-0000-0000-0000-000000001100',NULL,2,'EFFORT','Esforço de raiz retido',120,'EXACT','SYN_PIN','Instruções de raiz sintéticas'),
('39139900-0000-0000-0000-000000001114','39139900-0000-0000-0000-000000001100','39139900-0000-0000-0000-000000001113',1,'EFFORT','Filho removido explicitamente',60,'EXACT','SYN_PIN','Remoção de subárvore sintética');
INSERT INTO gym_block_prescriptions(block_id,structure,objective,rounds,round_recovery_seconds)
VALUES('39139900-0000-0000-0000-000000001200','SUPERSET','STRENGTH_ENDURANCE',2,90);
INSERT INTO gym_exercises(id,block_id,position,name,sets,repetitions,resistance_kind,resistance_value,notes)
VALUES('39139900-0000-0000-0000-000000001201','39139900-0000-0000-0000-000000001200',1,'Supino retido sintético',3,10,'KILOGRAMS',40.5,'Carga explícita sintética'),
('39139900-0000-0000-0000-000000001202','39139900-0000-0000-0000-000000001200',2,'Tração retida sintética',3,8,'BODY_WEIGHT',NULL,'Sem carga inventada');
INSERT INTO training_sessions(id,plan_id,title,description,starts_at,entry_kind,created_by_id,schedule_precision,scheduled_on,day_band)
SELECT ('39139900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,id,CASE n WHEN 1003 THEN 'Descanso DAY sintético' WHEN 1004 THEN 'Logística BAND sintética' ELSE 'Sessão com segundos sintética' END,'Notas civis sintéticas',CASE WHEN n=1005 THEN ((week_start+2)::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours 17 seconds' ELSE NULL END,CASE n WHEN 1003 THEN 'REST'::training_entry_kind WHEN 1004 THEN 'LOGISTICS'::training_entry_kind ELSE 'TRAINING'::training_entry_kind END,'39139900-0000-0000-0000-000000000001',CASE n WHEN 1003 THEN 'DAY' WHEN 1004 THEN 'BAND' ELSE 'START_ONLY' END,week_start+(n-1003),CASE WHEN n=1004 THEN 'AFTERNOON' ELSE NULL END
FROM training_plans CROSS JOIN unnest(ARRAY[1003,1004,1005])n WHERE id='39139900-0000-0000-0000-000000000308';
INSERT INTO training_sessions(id,plan_id,title,description,entry_kind,created_by_id,schedule_precision,scheduled_on)
VALUES('39139900-0000-0000-0000-000000001006','39139900-0000-0000-0000-000000000309','Descanso de hoje apenas sintético','Política de hoje ainda não decidida','REST','39139900-0000-0000-0000-000000000001','DAY',CURRENT_DATE);
-- Nonempty detached history/outcome sentinels are setup, not a claim that the
-- browser published these seeded instructions or recreated a real coach plan.
INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at)
VALUES('39139900-0000-0000-0000-000000001500','Atleta histórico exclusivamente sintético','edit-history@example.test','synthetic-no-login','1985-01-01',now());
INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on)
SELECT '39139900-0000-0000-0000-000000001501','39139900-0000-0000-0000-000000001500',season_id,programme_id,CURRENT_DATE,:'fixture_end'::date FROM training_plans WHERE id='39139900-0000-0000-0000-000000000306';
INSERT INTO training_group_members(group_id,membership_id,added_by_id)
VALUES('39139900-0000-0000-0000-000000000200','39139900-0000-0000-0000-000000001501','39139900-0000-0000-0000-000000000001');
INSERT INTO training_plan_publications(id,plan_id,revision,source_updated_at,change_summary,published_by_id)
SELECT '39139900-0000-0000-0000-000000001600',id,1,updated_at,'Histórico sintético não publicado pelo ensaio','39139900-0000-0000-0000-000000000001' FROM training_plans WHERE id='39139900-0000-0000-0000-000000000306';
WITH source AS (
 SELECT s.*,jsonb_build_object('schema_version',1,'session',jsonb_build_object('ID',s.id,'Title',s.title,'Description',s.description,'EntryKind',s.entry_kind,'When','Horário sintético congelado')) AS frozen
 FROM training_sessions s WHERE s.id='39139900-0000-0000-0000-000000001000'
)
INSERT INTO training_prescriptions(id,publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256)
SELECT '39139900-0000-0000-0000-000000001601','39139900-0000-0000-0000-000000001600',id,'39139900-0000-0000-0000-000000001501','39139900-0000-0000-0000-000000001500',frozen,encode(digest(frozen::text,'sha256'),'hex') FROM source;
INSERT INTO training_session_outcomes(session_id,user_id,prescription_id,status,distance_metres,actual_duration_minutes)
VALUES('39139900-0000-0000-0000-000000001000','39139900-0000-0000-0000-000000001500','39139900-0000-0000-0000-000000001601','COMPLETED',3500,55);
INSERT INTO training_routines(id,name,kind,owner_user_id,source_id,source_updated_at,snapshot)
SELECT '39139900-0000-0000-0000-000000001602','Rotina histórica exclusivamente sintética','SESSION','39139900-0000-0000-0000-000000000001',id,updated_at,training_session_snapshot_v2(id) FROM training_sessions WHERE id='39139900-0000-0000-0000-000000001000';
INSERT INTO training_copy_events(id,source_kind,source_id,source_updated_at,destination_kind,destination_id,copied_by_id)
SELECT '39139900-0000-0000-0000-000000001603','SESSION',id,updated_at,'SESSION','39139900-0000-0000-0000-000000001001','39139900-0000-0000-0000-000000000001' FROM training_sessions WHERE id='39139900-0000-0000-0000-000000001000';
COMMIT;
