-- Closed vocabulary remains unchanged; only person selections are mutable.
CREATE TABLE person_sport_assignment_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 operation_id uuid NOT NULL,
 subject_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 actor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 coach_grant_id uuid REFERENCES staff_grants(id) ON DELETE RESTRICT,
 kind varchar(5) NOT NULL CHECK (kind IN ('SPORT','CRAFT')),
 action varchar(7) NOT NULL CHECK (action IN ('ADDED','REMOVED')),
 code varchar(20) NOT NULL,
 reason varchar(500) NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 500),
 occurred_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT person_sport_assignment_event_code CHECK (
  (kind='SPORT' AND code IN ('CANOEING','KAYAK_POLO','SUP')) OR
  (kind='CRAFT' AND code IN ('K1','K2','K4','C1','C2','C4')))
);
CREATE INDEX person_sport_assignment_events_subject_idx ON person_sport_assignment_events(subject_user_id,occurred_at DESC,id DESC);
CREATE FUNCTION prevent_person_sport_assignment_event_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'person sport assignment audit is append-only' USING ERRCODE='23514'; END $$;
CREATE TRIGGER person_sport_assignment_events_immutable BEFORE UPDATE OR DELETE ON person_sport_assignment_events
 FOR EACH ROW EXECUTE FUNCTION prevent_person_sport_assignment_event_mutation();
