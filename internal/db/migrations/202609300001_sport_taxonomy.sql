-- New classification is intentionally independent of mutable participation rows.
-- Legacy modalities and membership_modalities remain untouched for historical readers.
CREATE TABLE sporting_modalities (
 code varchar(20) PRIMARY KEY CHECK (code IN ('CANOEING','KAYAK_POLO','SUP')),
 name_pt varchar(120) NOT NULL
);
INSERT INTO sporting_modalities(code,name_pt) VALUES
 ('CANOEING','Canoagem'),('KAYAK_POLO','Kayak Polo'),('SUP','Stand up paddle');
CREATE TABLE canoe_craft_classes (
 code varchar(2) PRIMARY KEY CHECK (code IN ('K1','K2','K4','C1','C2','C4')),
 name_pt varchar(120) NOT NULL
);
INSERT INTO canoe_craft_classes(code,name_pt) VALUES
 ('K1','Caiaque individual'),('K2','Caiaque duplo'),('K4','Caiaque quádruplo'),
 ('C1','Canoa individual'),('C2','Canoa dupla'),('C4','Canoa quádrupla');
CREATE FUNCTION prevent_sport_taxonomy_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'sport taxonomy is a closed catalog' USING ERRCODE='23514';
END $$;
CREATE TRIGGER sporting_modalities_fixed BEFORE UPDATE OR DELETE ON sporting_modalities
 FOR EACH ROW EXECUTE FUNCTION prevent_sport_taxonomy_change();
CREATE TRIGGER canoe_craft_classes_fixed BEFORE UPDATE OR DELETE ON canoe_craft_classes
 FOR EACH ROW EXECUTE FUNCTION prevent_sport_taxonomy_change();
CREATE TABLE person_sporting_modalities (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 modality_code varchar(20) NOT NULL REFERENCES sporting_modalities(code) ON UPDATE RESTRICT,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(user_id,modality_code)
);
CREATE TABLE person_canoe_craft_classes (
 user_id uuid NOT NULL,
 modality_code varchar(20) NOT NULL DEFAULT 'CANOEING' CHECK (modality_code='CANOEING'),
 craft_code varchar(2) NOT NULL REFERENCES canoe_craft_classes(code) ON UPDATE RESTRICT,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(user_id,craft_code),
 FOREIGN KEY(user_id,modality_code) REFERENCES person_sporting_modalities(user_id,modality_code) ON DELETE CASCADE
);
-- Lock the season row: two concurrent team creations cannot each see an empty set.
CREATE FUNCTION guard_shared_kayak_polo_team() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM programmes WHERE id=NEW.programme_id AND code='Kayak_Polo') THEN
  PERFORM 1 FROM seasons WHERE id=NEW.season_id FOR UPDATE;
  IF EXISTS (SELECT 1 FROM teams WHERE season_id=NEW.season_id AND programme_id=NEW.programme_id AND id<>NEW.id) THEN
   RAISE EXCEPTION 'only one shared Kayak Polo team per season' USING ERRCODE='23505', CONSTRAINT='teams_shared_kayak_polo';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER teams_shared_kayak_polo BEFORE INSERT OR UPDATE OF season_id,programme_id ON teams
 FOR EACH ROW EXECUTE FUNCTION guard_shared_kayak_polo_team();
-- Refuse activation when durable pre-existing teams conflict; never discard one.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM teams t JOIN programmes p ON p.id=t.programme_id
   WHERE p.code='Kayak_Polo' GROUP BY t.season_id HAVING count(*)>1) THEN
  RAISE EXCEPTION 'existing Kayak Polo teams require reviewed reconciliation' USING ERRCODE='23505';
 END IF;
END $$;
