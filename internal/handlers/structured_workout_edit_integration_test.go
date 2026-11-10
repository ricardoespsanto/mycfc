//go:build integration

package handlers

import (
	"errors"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
)

func editRealReview(t *testing.T, store PostgresFocusedWorkoutStore, request FocusedWorkoutEditRequest, change func(*focusedGraphDraft)) (focusedEditReviewPayload, focusedEditReviewed) {
	t.Helper()
	source, err := store.LoadFocusedEdit(t.Context(), request)
	if err != nil {
		t.Fatal("load", err)
	}
	draft, err := focusedEditLosslessDraft(source)
	if err != nil {
		t.Fatal("lossless", err)
	}
	if change != nil {
		change(&draft)
	}
	p, e := normalizeFocusedEdit(source, draft)
	if len(e) > 0 {
		t.Fatal(e)
	}
	fresh, err := store.ReadFocusedEditPreview(t.Context(), request, focusedGraphSelectedProfileIDs(p))
	if err != nil {
		t.Fatal(err)
	}
	p.Context, p.References = fresh.Context, fresh.Graph.References
	if err = allocateFocusedEditIDs(&p, fresh.Graph, nil); err != nil {
		t.Fatal(err)
	}
	baseline, err := focusedEditBaselineFor(fresh)
	if err != nil {
		t.Fatal(err)
	}
	payload := focusedEditReviewPayload{baseline, p, focusedGraphValues(draft)}
	raw, token, err := focusedWorkoutReviews.signEdit(payload, fresh, time.Now())
	if err != nil {
		t.Fatal("sign", err)
	}
	_, review, err := focusedWorkoutReviews.verifyEdit(raw, token, request.ActorID, request.PlanID, request.SessionID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return payload, review
}
func TestFocusedEditPostgresPreservesIDsAtomicReorderRemovalNoopAndStale(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	store := PostgresFocusedWorkoutStore{Pool: f.web}
	_, raw := focusedGraphWaterExample()
	raw.Add("activities[a_1].phases[p_1].water.node_order", "n_3")
	raw.Set("activities[a_1].phases[p_1].water.nodes[n_3].kind", "EFFORT")
	raw.Set("activities[a_1].phases[p_1].water.nodes[n_3].name", "Técnica final")
	raw.Set("activities[a_1].phases[p_1].water.nodes[n_3].instructions", "Remar suavemente mantendo a técnica.")
	created, review := graphRealReview(t, f, store, raw)
	if _, err := store.SaveFocusedGraph(t.Context(), review); err != nil {
		t.Fatal(err)
	}
	request := FocusedWorkoutEditRequest{f.actor, f.plan, created.Proposal.SessionID}
	first, err := store.LoadFocusedEdit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := focusedEditBaselineFor(first)
	_, noop := editRealReview(t, store, request, nil)
	if _, err = store.SaveFocusedEdit(t.Context(), noop); err != nil {
		t.Fatal("noop", err)
	}
	unchanged, err := store.LoadFocusedEdit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err = before.matches(unchanged); err != nil {
		t.Fatal("noop changed raw state", err)
	}
	proposal, edit := editRealReview(t, store, request, func(d *focusedGraphDraft) {
		d.Values.Set("title", "Treino alterado com os mesmos identificadores")
		a, b := d.Activities[0].Key, d.Activities[0].Phases[0].Key
		nodes := d.Activities[0].Phases[0].Nodes
		// Move the last top-level effort before the repeat group, retaining its ID.
		changed, commandErr := applyFocusedEditCommand(*d, "water_up:"+a+":"+b+":"+nodes[len(nodes)-1].Key, first.Graph)
		if commandErr != nil {
			t.Fatal(commandErr)
		}
		*d = changed
	})
	if _, err = store.SaveFocusedEdit(t.Context(), edit); err != nil {
		t.Fatal("edit", err)
	}
	saved, err := store.LoadFocusedEdit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Raw.Session.ID != first.Raw.Session.ID || saved.Raw.Session.Title != proposal.Proposal.Title || saved.Raw.Session.CreatedByID != first.Raw.Session.CreatedByID {
		t.Fatal("identity/title")
	}
	oldIDs, _ := focusedEditEntities(first.Graph)
	newIDs, _ := focusedEditEntities(saved.Graph)
	if len(oldIDs) != len(newIDs) {
		t.Fatal("changed count")
	}
	for id := range oldIDs {
		if _, ok := newIDs[id]; !ok {
			t.Fatal("lost retained ID", id)
		}
	}
	if _, err = store.SaveFocusedEdit(t.Context(), edit); !errors.Is(err, errFocusedEditSourceChanged) {
		t.Fatal("own-source stale edit was not distinguished from plan-only drift", err)
	}
	_, removal := editRealReview(t, store, request, func(d *focusedGraphDraft) {
		a, b := d.Activities[0].Key, d.Activities[0].Phases[0].Key
		root := ""
		for _, n := range d.Activities[0].Phases[0].Nodes {
			if n.Parent == "" && d.Values.Get(focusedNodePrefix(a, b, n.Key)+".kind") == "REPEAT_GROUP" {
				root = n.Key
			}
		}
		changed, commandErr := applyFocusedEditCommand(*d, "water_remove:"+a+":"+b+":"+root, saved.Graph)
		if commandErr != nil {
			t.Fatal(commandErr)
		}
		*d = changed
	})
	if _, err = store.SaveFocusedEdit(t.Context(), removal); err != nil {
		t.Fatal("remove subtree", err)
	}
	var count int
	if err = f.web.QueryRow(t.Context(), `SELECT count(*) FROM water_work_steps WHERE block_id=$1`, saved.Raw.Blocks[0].ID).Scan(&count); err != nil || count >= len(saved.Raw.Steps) {
		t.Fatal("removal was not complete", count, err)
	}
}
func TestFocusedEditPostgresPlanOnlyReviewAndLosslessNativeMetadata(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	store := PostgresFocusedWorkoutStore{Pool: f.web}
	request := FocusedWorkoutEditRequest{f.actor, f.plan, f.session}
	// Another authorized editor may edit an older exact draft; raw NULL date and
	// opaque modality/creator are not rewritten by text-only editing.
	f.exec(t, `UPDATE training_sessions SET created_by_id=$2,description='Descrição original' WHERE id=$1`, f.session, f.subject)
	before, err := store.LoadFocusedEdit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	_, review := editRealReview(t, store, request, func(d *focusedGraphDraft) { d.Values.Set("description", "Descrição alterada") })
	f.exec(t, `UPDATE training_plans SET title='Changed plan only' WHERE id=$1`, f.plan)
	if _, err = store.SaveFocusedEdit(t.Context(), review); !errors.Is(err, errFocusedWorkoutConflict) {
		t.Fatal("plan-only change misclassified", err)
	}
	_, review = editRealReview(t, store, request, func(d *focusedGraphDraft) { d.Values.Set("description", "Descrição alterada") })
	f.exec(t, `UPDATE training_plans SET title=title WHERE id=$1`, f.plan)
	if _, err = store.SaveFocusedEdit(t.Context(), review); err != nil {
		t.Fatal("same timestamp plan no-op should not invalidate", err)
	}
	after, err := store.LoadFocusedEdit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if after.Raw.Session.ScheduledOn.Valid != before.Raw.Session.ScheduledOn.Valid || after.Raw.Session.CreatedByID != f.subject || !after.Raw.Session.CreatedAt.Time.Equal(before.Raw.Session.CreatedAt.Time) {
		t.Fatal("native metadata rewritten")
	}
	// The status-only cancel path still works when editor controls cannot exactly
	// express seconds/fractions; it does not require a graph/artifact rebuild.
	f.exec(t, `UPDATE training_sessions SET starts_at=starts_at+interval '0.123 seconds' WHERE id=$1`, f.session)
	source, err := store.LoadFocusedEditSession(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CancelFocusedEdit(t.Context(), request, source.Raw.Session.UpdatedAt.Time, "Cancelamento explícito"); err != nil {
		t.Fatal("cancel", err)
	}
	var row dbgen.GetFocusedWorkoutSessionRow
	row, err = dbgen.New(f.web).GetFocusedWorkoutSession(t.Context(), dbgen.GetFocusedWorkoutSessionParams{ActorID: f.actor, PlanID: f.plan, SessionID: f.session})
	if err != nil || row.Status != "CANCELLED" || row.CancellationReason == nil || !strings.Contains(*row.CancellationReason, "explícito") {
		t.Fatal(row.Status, err)
	}
	if _, err = store.CancelFocusedEdit(t.Context(), request, source.Raw.Session.UpdatedAt.Time, "Repetição inválida"); !errors.Is(err, errFocusedEditSourceChanged) && !errors.Is(err, errFocusedEditReadOnly) {
		t.Fatal("cancel reversible/replay", err)
	}
}
