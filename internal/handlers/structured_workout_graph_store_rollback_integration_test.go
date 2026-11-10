//go:build integration

package handlers

import (
	"errors"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestWorkoutGraphPGWriteFaultsRollbackAllParentsAndVersion(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	ctx := t.Context()
	store := PostgresFocusedWorkoutStore{Pool: f.web}
	_, raw := focusedGraphWaterExample()
	raw.Add("activity_order", "a_2")
	raw.Set("activities[a_2].modality", "GYM")
	raw.Add("activities[a_2].phase_order", "p_2")
	const phase = "activities[a_2].phases[p_2]"
	raw.Set(phase+".purpose", "MAIN")
	raw.Set(phase+".instructions", "Força e estabilidade para o ensaio.")
	raw.Set(phase+".detail_mode", "GYM")
	raw.Set(phase+".gym.structure", "SUPERSET")
	raw.Set(phase+".gym.objective", "CORE")
	raw.Set(phase+".gym.rounds", "3")
	raw.Add(phase+".gym.exercise_order", "e_1")
	raw.Set(phase+".gym.exercises[e_1].name", "Prancha lateral")
	raw.Set(phase+".gym.exercises[e_1].duration_seconds", "30")
	payload, review := graphRealReview(t, f, store, raw)
	assertRollback := func(t *testing.T) {
		t.Helper()
		var exists bool
		var version time.Time
		if err := f.web.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM training_sessions WHERE id=$1), updated_at FROM training_plans WHERE id=$2`, payload.Proposal.SessionID, f.plan).Scan(&exists, &version); err != nil || exists || !version.Equal(payload.Proposal.Context.PlanUpdatedAt) {
			t.Fatalf("failed graph write retained a session or changed its plan version: exists=%t version=%v err=%v", exists, version, err)
		}
	}
	if _, err := f.root.Exec(ctx, `CREATE FUNCTION public.qa_reject_workout_graph_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='qa_workout_graph_insert'; END $$`); err != nil {
		t.Fatal(err)
	}
	for _, relation := range []string{"training_session_segments", "training_segment_blocks", "water_work_steps", "gym_block_prescriptions"} {
		t.Run(relation, func(t *testing.T) {
			table := pgx.Identifier{"public", relation}.Sanitize()
			if _, err := f.root.Exec(t.Context(), `CREATE TRIGGER qa_reject_workout_graph_insert BEFORE INSERT ON `+table+` FOR EACH ROW EXECUTE FUNCTION public.qa_reject_workout_graph_insert()`); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := f.root.Exec(ctx, `DROP TRIGGER qa_reject_workout_graph_insert ON `+table); err != nil {
					t.Error(err)
				}
			})
			out, err := store.SaveFocusedGraph(t.Context(), review)
			var rejected *pgconn.PgError
			if !errors.As(err, &rejected) || rejected.ConstraintName != "qa_workout_graph_insert" || out != (FocusedWorkoutSaveResult{}) {
				t.Fatalf("fixture fault did not reject the intended SQL stage: out=%+v err=%v", out, err)
			}
			assertRollback(t)
		})
	}
	if _, err := f.root.Exec(ctx, `CREATE FUNCTION public.qa_drift_workout_graph_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.instructions='Synthetic persisted drift'; RETURN NEW; END $$; CREATE TRIGGER qa_drift_workout_graph_insert BEFORE INSERT ON training_segment_blocks FOR EACH ROW EXECUTE FUNCTION public.qa_drift_workout_graph_insert()`); err != nil {
		t.Fatal(err)
	}
	if out, err := store.SaveFocusedGraph(ctx, review); !errors.Is(err, errFocusedWorkoutConflict) || out != (FocusedWorkoutSaveResult{}) {
		t.Fatalf("read-back divergence produced a saved result: out=%+v err=%v", out, err)
	}
	assertRollback(t)
	if _, err := f.root.Exec(ctx, `DROP TRIGGER qa_drift_workout_graph_insert ON training_segment_blocks`); err != nil {
		t.Fatal(err)
	}
	out, err := store.SaveFocusedGraph(ctx, review)
	if err != nil || out.SessionID != payload.Proposal.SessionID || out.AlreadyCreated {
		t.Fatalf("valid review could not commit after fixture fault was removed: out=%+v err=%v", out, err)
	}
	graph, err := readFocusedGraph(ctx, dbgen.New(f.web), payload.Proposal)
	if err != nil || !focusedGraphJSONEqual(graph, payload.Proposal) {
		t.Fatalf("successful write did not retain the exact reviewed graph: %v", err)
	}
}
