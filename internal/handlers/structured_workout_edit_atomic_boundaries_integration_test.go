//go:build integration

package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
)

func reviewedWorkoutRemoval(t *testing.T, f workoutEditStoreFixture, before focusedEditSource) focusedEditReviewed {
	t.Helper()
	_, review := editRealReview(t, f.store, f.request, func(d *focusedGraphDraft) {
		d.Values.Set("title", "Treino revisto sem a série inicial")
		activity := d.Activities[0]
		phase := activity.Phases[0]
		for _, node := range phase.Nodes {
			if node.Parent == "" && d.Values.Get(focusedNodePrefix(activity.Key, phase.Key, node.Key)+".kind") == "REPEAT_GROUP" {
				changed, err := applyFocusedEditCommand(*d, "water_remove:"+activity.Key+":"+phase.Key+":"+node.Key, before.Graph)
				if err != nil {
					t.Fatal(err)
				}
				*d = changed
				return
			}
		}
		t.Fatal("fixture has no repeated set to remove")
	})
	return review
}

func assertWorkoutEditUnchanged(t *testing.T, f workoutEditStoreFixture, before focusedEditSource, baseline focusedEditBaseline) {
	t.Helper()
	after, err := f.store.LoadFocusedEdit(t.Context(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	if err = baseline.matches(after); err != nil || !after.Context.PlanUpdatedAt.Equal(before.Context.PlanUpdatedAt) ||
		!focusedGraphJSONEqual(after.Graph.References, before.Graph.References) {
		t.Fatalf("failed transaction changed the stored workout, plan version or selected rhythm: %v", err)
	}
}

func TestWorkoutEditAtomicInterruptedEditRollsBackWholeNativeGraph(t *testing.T) {
	f := newWorkoutEditStoreFixture(t)
	ctx := t.Context()
	before, err := f.store.LoadFocusedEdit(ctx, f.request)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := focusedEditBaselineFor(before)
	if err != nil {
		t.Fatal(err)
	}
	review := reviewedWorkoutRemoval(t, f, before)
	expired := review
	expired.expiresAt = time.Now().Add(-time.Second)
	if _, err = f.store.SaveFocusedEdit(ctx, expired); !errors.Is(err, errFocusedWorkoutInvalid) {
		t.Fatalf("expired edit error=%v", err)
	}
	for _, tc := range []struct {
		name, marker string
		occurrence   int32
		runtime      bool
	}{
		{"runtime contract verification", "/* workout graph profile version witness */", 1, true},
		{"actor admission", "SELECT role_id FROM user_platform_roles WHERE user_id=$1 FOR SHARE", 1, false},
		{"lock workout plan", "-- name: LockFocusedWaterCreatePlan ", 1, false},
		{"lock original workout", "-- name: LockFocusedWorkoutForEdit ", 1, false},
		{"lock original workout details", "-- name: LockFocusedWorkoutWaterSteps ", 1, false},
		{"load original workout context", "-- name: GetFocusedWaterCreateContext ", 1, false},
		{"lock selected rhythm", "-- name: LockFocusedWorkoutEditProfiles ", 1, false},
		{"verify locked rhythm zones", "-- name: ListFocusedWorkoutEditZones ", 2, false},
		{"check variation references", "-- name: HasFocusedWorkoutRemovalReferences ", 1, false},
		{"stage retained activity position", "-- name: StageFocusedWorkoutSegmentPosition ", 1, false},
		{"delete explicitly removed water step", "-- name: DeleteFocusedWorkoutWaterSteps ", 1, false},
		{"save workout header", "-- name: UpdateFocusedWorkoutSession ", 1, false},
		{"read saved workout header", "-- name: GetFocusedWorkoutSession ", 1, false},
		{"read saved workout children", "-- name: ListFocusedWorkoutSegments ", 1, false},
		{"measure saved artifact", "-- name: FocusedWaterArtifactSize ", 1, false},
		{"reread stored rhythm", "-- name: ListFocusedWorkoutEditProfiles ", 2, false},
		{"final original eligibility", "-- name: CheckFocusedWorkoutOriginalEligibility ", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, fault := workoutEditInterruptedStore(t, f, tc.marker, tc.occurrence)
			result, err := store.SaveFocusedEdit(t.Context(), review)
			matched := errors.Is(err, context.Canceled)
			if tc.runtime {
				matched = err != nil && strings.Contains(err.Error(), "workout profile version witness contract mismatch")
			}
			if fault.seen.Load() < tc.occurrence || !matched || result != (FocusedWorkoutSaveResult{}) {
				t.Fatalf("interrupted edit result=%+v matches=%d error=%v", result, fault.seen.Load(), err)
			}
			assertWorkoutEditUnchanged(t, f, before, baseline)
		})
	}
	result, err := f.store.SaveFocusedEdit(ctx, review)
	if err != nil || result.SessionID != f.request.SessionID {
		t.Fatalf("healthy edit could not use retained review: %+v, %v", result, err)
	}
	after, err := f.store.LoadFocusedEdit(ctx, f.request)
	if err != nil || len(after.Raw.Steps) != 1 || len(before.Raw.Steps) != 3 || after.Raw.Session.Title != review.Payload.Proposal.Title {
		t.Fatalf("healthy edit did not remove the reviewed subtree and save the title: %v", err)
	}
}

func TestWorkoutEditAtomicCancellationRollsBackUntilFinalEligibility(t *testing.T) {
	f := newWorkoutEditStoreFixture(t)
	ctx := t.Context()
	before, err := f.store.LoadFocusedEdit(ctx, f.request)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := focusedEditBaselineFor(before)
	if err != nil {
		t.Fatal(err)
	}
	const reason = "Cancelamento sintético verificado"
	for _, tc := range []struct {
		name, marker string
	}{
		{"load scoped cancellation request", "-- name: GetFocusedWorkoutSession "},
		{"admit cancellation", "SELECT public.training_schedule_admit_partial($1,$2)"},
		{"lock plan", "-- name: LockFocusedWaterCreatePlan "},
		{"lock original workout", "-- name: LockFocusedWorkoutForEdit "},
		{"save cancellation", "-- name: CancelFocusedWorkoutSession "},
		{"recheck original eligibility after writing", "-- name: CheckFocusedWorkoutOriginalEligibility "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, fault := workoutEditInterruptedStore(t, f, tc.marker, 1)
			result, err := store.CancelFocusedEdit(t.Context(), f.request, before.Raw.Session.UpdatedAt.Time, reason)
			if fault.seen.Load() < 1 || !errors.Is(err, context.Canceled) || result != (FocusedWorkoutSaveResult{}) {
				t.Fatalf("interrupted cancellation result=%+v matches=%d error=%v", result, fault.seen.Load(), err)
			}
			assertWorkoutEditUnchanged(t, f, before, baseline)
		})
	}
	q := dbgen.New(f.web)
	if err = focusedEditFinalEligibility(ctx, q, f.request, before.Raw.Session, time.Now().Add(-time.Second)); !errors.Is(err, errFocusedWorkoutInvalid) {
		t.Fatalf("expired final review error=%v", err)
	}
	result, err := f.store.CancelFocusedEdit(ctx, f.request, before.Raw.Session.UpdatedAt.Time, reason)
	if err != nil || result.SessionID != f.request.SessionID {
		t.Fatalf("healthy cancellation result=%+v error=%v", result, err)
	}
	after, err := f.store.LoadFocusedEditSession(ctx, f.request)
	if err != nil || after.Raw.Session.Status != "CANCELLED" || after.Raw.Session.CancellationReason == nil || *after.Raw.Session.CancellationReason != reason {
		t.Fatalf("cancellation metadata was not saved: %+v, %v", after.Raw.Session, err)
	}
	if _, err = f.store.CancelFocusedEdit(ctx, f.request, after.Raw.Session.UpdatedAt.Time, reason); !errors.Is(err, errFocusedEditReadOnly) {
		t.Fatalf("already cancelled workout became writable: %v", err)
	}
	if err = focusedEditFinalEligibility(ctx, q, f.request, after.Raw.Session, time.Time{}); !errors.Is(err, errFocusedEditReadOnly) {
		t.Fatalf("nonactive original final eligibility error=%v", err)
	}
}
