\set ON_ERROR_STOP on
-- NEW owned synthetic fixture only; no real coach transcription/season approval.
DO $$ BEGIN IF current_database()<>'mycfc' OR current_user<>'authoring_bootstrap' THEN RAISE EXCEPTION 'wrong owned reuse fixture'; END IF; END $$;
BEGIN;
INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current)
VALUES('39139900-0000-0000-0000-000000000101','SYN_REUSE_TARGET','Época de destino exclusivamente sintética',:'fixture_start',:'fixture_end',false);
INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at)
VALUES('39139900-0000-0000-0000-000000005000','Treinador de outro âmbito sintético','unrelated-coach@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1980-01-01',now());
INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id)
SELECT '39139900-0000-0000-0000-000000005000','COACH',id,'39139900-0000-0000-0000-000000000001' FROM programmes WHERE code='Competition';
INSERT INTO training_groups(id,name,programme_id,created_by_id)
SELECT '39139900-0000-0000-0000-000000000201','Destino de reutilização exclusivamente sintético',id,'39139900-0000-0000-0000-000000000001' FROM programmes WHERE code='Leisure';
INSERT INTO training_groups(id,name,programme_id,created_by_id)
SELECT '39139900-0000-0000-0000-000000000203','Outro âmbito exclusivamente sintético',id,'39139900-0000-0000-0000-000000005000' FROM programmes WHERE code='Competition';
INSERT INTO training_plans(id,title,description,programme_id,training_group_id,season_id,week_start,created_by_id)
SELECT '39139900-0000-0000-0000-000000000310','Origem de quatro horários exclusivamente sintética','Não é a transcrição de um plano real.',id,'39139900-0000-0000-0000-000000000200','39139900-0000-0000-0000-000000000100',:'fixture_week'::date+70,'39139900-0000-0000-0000-000000000001' FROM programmes WHERE code='Leisure';
INSERT INTO training_plans(id,title,description,programme_id,training_group_id,season_id,week_start,created_by_id)
SELECT '39139900-0000-0000-0000-000000000311','Destino existente para rotinas sintéticas','Esta semana existente não deve ser renomeada.',id,'39139900-0000-0000-0000-000000000201','39139900-0000-0000-0000-000000000101',:'fixture_week'::date+91,'39139900-0000-0000-0000-000000000001' FROM programmes WHERE code='Leisure';
INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id)
SELECT '39139900-0000-0000-0000-000000000312','Conteúdo privado de outro âmbito sintético',id,'39139900-0000-0000-0000-000000000203','39139900-0000-0000-0000-000000000100',:'fixture_week'::date+98,'39139900-0000-0000-0000-000000005000' FROM programmes WHERE code='Competition';
-- Clone the already configured complete seed graph under NEW source identities.
-- This is fixture setup before the app starts, not product copy acceptance.
INSERT INTO training_sessions
SELECT (jsonb_populate_record(NULL::training_sessions,to_jsonb(s)||jsonb_build_object('id','39139900-0000-0000-0000-000000002000','plan_id','39139900-0000-0000-0000-000000000310','title','Misto quatro horários exclusivamente sintético','starts_at',(p.week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours','ends_at',(p.week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours','scheduled_on',p.week_start,'created_at',clock_timestamp(),'updated_at',clock_timestamp()))).*
FROM training_sessions s CROSS JOIN training_plans p WHERE s.id='39139900-0000-0000-0000-000000001000' AND p.id='39139900-0000-0000-0000-000000000310';
INSERT INTO training_session_segments
SELECT (jsonb_populate_record(NULL::training_session_segments,to_jsonb(s)||jsonb_build_object('id',('39139900-0000-0000-0000-'||lpad((right(s.id::text,12)::bigint+1000)::text,12,'0')),'session_id','39139900-0000-0000-0000-000000002000','created_at',clock_timestamp(),'updated_at',clock_timestamp()))).*
FROM training_session_segments s WHERE s.session_id='39139900-0000-0000-0000-000000001000';
INSERT INTO training_segment_blocks
SELECT (jsonb_populate_record(NULL::training_segment_blocks,to_jsonb(b)||jsonb_build_object('id',('39139900-0000-0000-0000-'||lpad((right(b.id::text,12)::bigint+1000)::text,12,'0')),'segment_id',('39139900-0000-0000-0000-'||lpad((right(b.segment_id::text,12)::bigint+1000)::text,12,'0')),'created_at',clock_timestamp(),'updated_at',clock_timestamp()))).*
FROM training_segment_blocks b JOIN training_session_segments s ON s.id=b.segment_id WHERE s.session_id='39139900-0000-0000-0000-000000001000';
INSERT INTO water_block_prescriptions
SELECT (jsonb_populate_record(NULL::water_block_prescriptions,to_jsonb(w)||jsonb_build_object('block_id','39139900-0000-0000-0000-000000002100','created_at',clock_timestamp(),'updated_at',clock_timestamp()))).*
FROM water_block_prescriptions w WHERE w.block_id='39139900-0000-0000-0000-000000001100';
INSERT INTO water_work_steps
SELECT (jsonb_populate_record(NULL::water_work_steps,to_jsonb(w)||jsonb_build_object('id',('39139900-0000-0000-0000-'||lpad((right(w.id::text,12)::bigint+1000)::text,12,'0')),'block_id','39139900-0000-0000-0000-000000002100','parent_step_id',CASE WHEN w.parent_step_id IS NULL THEN NULL ELSE ('39139900-0000-0000-0000-'||lpad((right(w.parent_step_id::text,12)::bigint+1000)::text,12,'0')) END,'created_at',clock_timestamp(),'updated_at',clock_timestamp()))).*
FROM water_work_steps w WHERE w.block_id='39139900-0000-0000-0000-000000001100' ORDER BY w.parent_step_id NULLS FIRST,w.position;
INSERT INTO gym_block_prescriptions
SELECT (jsonb_populate_record(NULL::gym_block_prescriptions,to_jsonb(g)||jsonb_build_object('block_id','39139900-0000-0000-0000-000000002200','created_at',clock_timestamp(),'updated_at',clock_timestamp()))).*
FROM gym_block_prescriptions g WHERE g.block_id='39139900-0000-0000-0000-000000001200';
INSERT INTO gym_exercises
SELECT (jsonb_populate_record(NULL::gym_exercises,to_jsonb(g)||jsonb_build_object('id',('39139900-0000-0000-0000-'||lpad((right(g.id::text,12)::bigint+1000)::text,12,'0')),'block_id','39139900-0000-0000-0000-000000002200','created_at',clock_timestamp(),'updated_at',clock_timestamp()))).*
FROM gym_exercises g WHERE g.block_id='39139900-0000-0000-0000-000000001200';
INSERT INTO training_sessions(id,plan_id,title,description,starts_at,entry_kind,created_by_id,schedule_precision,scheduled_on,day_band)
SELECT ('39139900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,p.id,CASE n WHEN 2001 THEN 'Início sem fim exclusivamente sintético' WHEN 2002 THEN 'Descanso sem horas exclusivamente sintético' ELSE 'Logística à tarde exclusivamente sintética' END,'Notas originais de reutilização sintética',CASE WHEN n=2001 THEN ((p.week_start+1)::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '16 hours' ELSE NULL END,CASE n WHEN 2001 THEN 'TRAINING'::training_entry_kind WHEN 2002 THEN 'REST'::training_entry_kind ELSE 'LOGISTICS'::training_entry_kind END,'39139900-0000-0000-0000-000000000001',CASE n WHEN 2001 THEN 'START_ONLY' WHEN 2002 THEN 'DAY' ELSE 'BAND' END,p.week_start+(n-2000),CASE WHEN n=2003 THEN 'AFTERNOON' ELSE NULL END
FROM training_plans p CROSS JOIN unnest(ARRAY[2001,2002,2003])n WHERE p.id='39139900-0000-0000-0000-000000000310';
INSERT INTO training_session_segments(id,session_id,position,modality,title)
VALUES('39139900-0000-0000-0000-000000002030','39139900-0000-0000-0000-000000002001',1,'RUN','Corrida sem fim inventado');
INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions)
VALUES('39139900-0000-0000-0000-000000002300','39139900-0000-0000-0000-000000002030',1,'MAIN','Correr por instrução sintética, sem fabricar duração.');
INSERT INTO training_sessions(id,plan_id,title,description,starts_at,ends_at,entry_kind,created_by_id,schedule_precision,scheduled_on)
SELECT '39139900-0000-0000-0000-000000003000',id,'Sessão de destino para acrescentar rotinas','Instruções originais do destino.',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours','TRAINING','39139900-0000-0000-0000-000000000001','INTERVAL',week_start FROM training_plans WHERE id='39139900-0000-0000-0000-000000000311';
INSERT INTO training_session_segments(id,session_id,position,modality,title)
VALUES('39139900-0000-0000-0000-000000003010','39139900-0000-0000-0000-000000003000',1,'GYM','Atividade de destino original');
INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions)
VALUES('39139900-0000-0000-0000-000000003100','39139900-0000-0000-0000-000000003010',1,'MAIN','Fase original que deve manter a identidade.');
INSERT INTO training_routines(id,name,description,kind,visibility,owner_user_id,programme_id,modality,tags,source_id,source_updated_at,snapshot)
SELECT '39139900-0000-0000-0000-000000004000','Força A sessão exclusivamente sintética','Rotina configurada apenas neste ensaio.','SESSION','SHARED','39139900-0000-0000-0000-000000000001',p.programme_id,NULL,ARRAY['synthetic'],s.id,s.updated_at,training_session_snapshot_v2(s.id) FROM training_sessions s JOIN training_plans p ON p.id=s.plan_id WHERE s.id='39139900-0000-0000-0000-000000002000';
INSERT INTO training_routines(id,name,description,kind,visibility,owner_user_id,programme_id,modality,tags,source_id,source_updated_at,snapshot)
SELECT '39139900-0000-0000-0000-000000004001','Força B atividade exclusivamente sintética','Atividade configurada apenas neste ensaio.','SEGMENT','SHARED','39139900-0000-0000-0000-000000000001',p.programme_id,'GYM',ARRAY['synthetic'],g.id,g.updated_at,training_segment_snapshot(g.id) FROM training_session_segments g JOIN training_sessions s ON s.id=g.session_id JOIN training_plans p ON p.id=s.plan_id WHERE g.id='39139900-0000-0000-0000-000000002020';
INSERT INTO training_routines(id,name,description,kind,visibility,owner_user_id,programme_id,modality,tags,source_id,source_updated_at,snapshot)
SELECT '39139900-0000-0000-0000-000000004002','Força B bloco exclusivamente sintético','Bloco configurado apenas neste ensaio.','BLOCK','SHARED','39139900-0000-0000-0000-000000000001',p.programme_id,'GYM',ARRAY['synthetic'],b.id,b.updated_at,training_block_snapshot(b.id) FROM training_segment_blocks b JOIN training_session_segments g ON g.id=b.segment_id JOIN training_sessions s ON s.id=g.session_id JOIN training_plans p ON p.id=s.plan_id WHERE b.id='39139900-0000-0000-0000-000000002200';
-- Legal same-priority overlap uses TWO distinct variation subgroups. These are
-- source-only variation targets, not additional active training-group placements.
INSERT INTO training_variation_groups(id,training_group_id,name,kind,effective_from,effective_until,created_by_id)
SELECT ('39139900-0000-0000-0000-'||lpad(n::text,12,'0'))::uuid,'39139900-0000-0000-0000-000000000200','Subgrupo sintético sobreposto '||n,'SUBGROUP',week_start,week_start+6,'39139900-0000-0000-0000-000000000001'
FROM training_plans CROSS JOIN unnest(ARRAY[4202,4203])n WHERE id='39139900-0000-0000-0000-000000000310';
INSERT INTO training_variation_group_members(variation_group_id,membership_id,added_by_id)
VALUES('39139900-0000-0000-0000-000000004202','39139900-0000-0000-0000-000000001501','39139900-0000-0000-0000-000000000001'),
('39139900-0000-0000-0000-000000004203','39139900-0000-0000-0000-000000001501','39139900-0000-0000-0000-000000000001');
INSERT INTO training_variations(id,plan_id,target_membership_id,target_group_id,subject_kind,subject_id,operation,change_summary,patch,created_by_id)
VALUES('39139900-0000-0000-0000-000000004200','39139900-0000-0000-0000-000000000310',NULL,'39139900-0000-0000-0000-000000004202','BLOCK','39139900-0000-0000-0000-000000002100','OVERRIDE','Primeira alteração sintética com conflito','{"instructions":"Primeira instrução sintética"}','39139900-0000-0000-0000-000000000001'),
('39139900-0000-0000-0000-000000004201','39139900-0000-0000-0000-000000000310',NULL,'39139900-0000-0000-0000-000000004203','BLOCK','39139900-0000-0000-0000-000000002100','OVERRIDE','Segunda alteração sintética com conflito','{"instructions":"Segunda instrução sintética"}','39139900-0000-0000-0000-000000000001');
INSERT INTO training_plan_publications(id,plan_id,revision,source_updated_at,change_summary,published_by_id)
SELECT '39139900-0000-0000-0000-000000004100',id,1,updated_at,'Sentinela histórica sintética de reutilização','39139900-0000-0000-0000-000000000001' FROM training_plans WHERE id='39139900-0000-0000-0000-000000000310';
WITH source AS (SELECT s.*,jsonb_build_object('schema_version',1,'session',jsonb_build_object('ID',s.id,'Title',s.title,'Description',s.description,'EntryKind',s.entry_kind,'When','Horário sintético congelado')) AS frozen FROM training_sessions s WHERE s.id='39139900-0000-0000-0000-000000002000')
INSERT INTO training_prescriptions(id,publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256)
SELECT '39139900-0000-0000-0000-000000004101','39139900-0000-0000-0000-000000004100',id,'39139900-0000-0000-0000-000000001501','39139900-0000-0000-0000-000000001500',frozen,encode(digest(frozen::text,'sha256'),'hex') FROM source;
INSERT INTO training_session_outcomes(session_id,user_id,prescription_id,status,distance_metres,actual_duration_minutes)
VALUES('39139900-0000-0000-0000-000000002000','39139900-0000-0000-0000-000000001500','39139900-0000-0000-0000-000000004101','COMPLETED',3500,55);
-- A valid source owned by the Competition-only coach isolates target-scope
-- authorization from malformed/empty-source validation in the browser denial.
INSERT INTO training_sessions(id,plan_id,title,description,starts_at,entry_kind,created_by_id,schedule_precision,scheduled_on)
SELECT '39139900-0000-0000-0000-000000005001',id,'Treino do âmbito autorizado sintético','Fonte configurada apenas para o treinador deste âmbito.',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours','TRAINING','39139900-0000-0000-0000-000000005000','START_ONLY',week_start FROM training_plans WHERE id='39139900-0000-0000-0000-000000000312';
INSERT INTO training_session_segments(id,session_id,position,modality,title)
VALUES('39139900-0000-0000-0000-000000005010','39139900-0000-0000-0000-000000005001',1,'RUN','Corrida do âmbito autorizado');
INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions)
VALUES('39139900-0000-0000-0000-000000005100','39139900-0000-0000-0000-000000005010',1,'MAIN','Instruções sintéticas da fonte autorizada, sem fim ou duração inventados.');
COMMIT;
