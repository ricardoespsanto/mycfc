package handlers

import (
	"bytes"
	"sort"

	"github.com/google/uuid"
)

type focusedEditEntity struct {
	ID, Owner, Parent uuid.UUID
	Kind, Path        string
	Position          int32
}
type focusedEditDiff struct {
	RetainedSegments, AddedSegments, RemovedSegments    []uuid.UUID
	RetainedBlocks, AddedBlocks, RemovedBlocks          []uuid.UUID
	RetainedSteps, AddedSteps, RemovedSteps             []uuid.UUID
	RetainedExercises, AddedExercises, RemovedExercises []uuid.UUID
	RemovedWater, RemovedGym                            []uuid.UUID
}

func focusedEditEntities(p focusedWorkoutGraph) (map[uuid.UUID]focusedEditEntity, error) {
	out := map[uuid.UUID]focusedEditEntity{}
	add := func(e focusedEditEntity) bool {
		if e.ID == uuid.Nil || e.ID == p.SessionID {
			return false
		}
		if _, dup := out[e.ID]; dup {
			return false
		}
		out[e.ID] = e
		return true
	}
	for ai, a := range p.Activities {
		if !add(focusedEditEntity{ID: a.ID, Owner: p.SessionID, Kind: "segment", Path: focusedActivityPrefix(a.Key), Position: int32(ai + 1)}) {
			return nil, errFocusedWorkoutInvalid
		}
		for bi, b := range a.Phases {
			if !add(focusedEditEntity{ID: b.ID, Owner: a.ID, Kind: "block", Path: focusedGraphPhasePrefix(a.Key, b.Key), Position: int32(bi + 1)}) {
				return nil, errFocusedWorkoutInvalid
			}
			if b.Water != nil {
				ids := map[string]uuid.UUID{}
				for _, n := range b.Water.Nodes {
					ids[n.Key] = n.ID
				}
				positions := map[uuid.UUID]int32{}
				for _, n := range b.Water.Nodes {
					parent := uuid.Nil
					if n.ParentKey != "" {
						var ok bool
						parent, ok = ids[n.ParentKey]
						if !ok {
							return nil, errFocusedWorkoutInvalid
						}
					}
					positions[parent]++
					if !add(focusedEditEntity{ID: n.ID, Owner: b.ID, Parent: parent, Kind: "step", Path: focusedNodePrefix(a.Key, b.Key, n.Key), Position: positions[parent]}) {
						return nil, errFocusedWorkoutInvalid
					}
				}
			}
			if b.Gym != nil {
				for xi, x := range b.Gym.Exercises {
					if !add(focusedEditEntity{ID: x.ID, Owner: b.ID, Kind: "exercise", Path: focusedExercisePrefix(a.Key, b.Key, x.Key), Position: int32(xi + 1)}) {
						return nil, errFocusedWorkoutInvalid
					}
				}
			}
		}
	}
	return out, nil
}
func focusedEditSortedIDs(ids []uuid.UUID) []uuid.UUID {
	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
	return ids
}
func focusedEditRemovedClosure(source focusedWorkoutGraph, roots []focusedGraphRemoval) (map[uuid.UUID]bool, error) {
	entities, err := focusedEditEntities(source)
	if err != nil {
		return nil, err
	}
	byPath := map[string]focusedEditEntity{}
	for _, e := range entities {
		byPath[e.Path] = e
	}
	removed := map[uuid.UUID]bool{}
	var visit func(focusedEditEntity)
	visit = func(root focusedEditEntity) {
		if removed[root.ID] {
			return
		}
		removed[root.ID] = true
		for _, child := range entities {
			include := child.Owner == root.ID
			if root.Kind == "step" {
				include = child.Kind == "step" && child.Owner == root.Owner && child.Parent == root.ID
			}
			if include {
				visit(child)
			}
		}
	}
	for _, r := range roots {
		var t focusedGraphTombstone
		if focusedStrictJSON([]byte(r.Payload), &t) != nil {
			return nil, errFocusedWorkoutInvalid
		}
		path := focusedEditTombPath(t)
		if root, ok := byPath[path]; ok {
			if t.Values.Get(path+".id") != root.ID.String() {
				return nil, errFocusedWorkoutInvalid
			}
			visit(root)
		} else if t.Values.Get(path+".id") != "" {
			return nil, errFocusedWorkoutInvalid
		}
	}
	return removed, nil
}
func buildFocusedEditDiff(source, proposed focusedWorkoutGraph, draft focusedGraphDraft) (focusedEditDiff, error) {
	var diff focusedEditDiff
	if proposed.SessionID != source.SessionID || proposed.Kind != source.Kind || !focusedSameCreateIdentity(source.Context, proposed.Context) {
		return diff, errFocusedWorkoutInvalid
	}
	old, err := focusedEditEntities(source)
	if err != nil {
		return diff, err
	}
	next, err := focusedEditEntities(proposed)
	if err != nil {
		return diff, err
	}
	closure, err := focusedEditRemovedClosure(source, draft.Removed)
	if err != nil {
		return diff, err
	}
	appendTo := func(kind string, retained, added, removed bool, id uuid.UUID) {
		switch kind {
		case "segment":
			if retained {
				diff.RetainedSegments = append(diff.RetainedSegments, id)
			}
			if added {
				diff.AddedSegments = append(diff.AddedSegments, id)
			}
			if removed {
				diff.RemovedSegments = append(diff.RemovedSegments, id)
			}
		case "block":
			if retained {
				diff.RetainedBlocks = append(diff.RetainedBlocks, id)
			}
			if added {
				diff.AddedBlocks = append(diff.AddedBlocks, id)
			}
			if removed {
				diff.RemovedBlocks = append(diff.RemovedBlocks, id)
			}
		case "step":
			if retained {
				diff.RetainedSteps = append(diff.RetainedSteps, id)
			}
			if added {
				diff.AddedSteps = append(diff.AddedSteps, id)
			}
			if removed {
				diff.RemovedSteps = append(diff.RemovedSteps, id)
			}
		case "exercise":
			if retained {
				diff.RetainedExercises = append(diff.RetainedExercises, id)
			}
			if added {
				diff.AddedExercises = append(diff.AddedExercises, id)
			}
			if removed {
				diff.RemovedExercises = append(diff.RemovedExercises, id)
			}
		}
	}
	for id, e := range next {
		if previous, ok := old[id]; ok {
			if e.Kind != previous.Kind || e.Owner != previous.Owner || e.Parent != previous.Parent || e.Path != previous.Path {
				return diff, errFocusedWorkoutInvalid
			}
			if closure[id] {
				return diff, errFocusedWorkoutInvalid
			}
			appendTo(e.Kind, true, false, false, id)
		} else {
			appendTo(e.Kind, false, true, false, id)
		}
	}
	for id, e := range old {
		if _, kept := next[id]; !kept {
			if !closure[id] {
				return diff, errFocusedWorkoutInvalid
			}
			appendTo(e.Kind, false, false, true, id)
		} else if closure[id] {
			return diff, errFocusedWorkoutInvalid
		}
	}
	oldWater, oldGym, newWater, newGym := map[uuid.UUID]bool{}, map[uuid.UUID]bool{}, map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
	for _, a := range source.Activities {
		for _, b := range a.Phases {
			oldWater[b.ID] = b.Water != nil
			oldGym[b.ID] = b.Gym != nil
		}
	}
	for _, a := range proposed.Activities {
		for _, b := range a.Phases {
			newWater[b.ID] = b.Water != nil
			newGym[b.ID] = b.Gym != nil
		}
	}
	for id, present := range oldWater {
		if present && !newWater[id] {
			diff.RemovedWater = append(diff.RemovedWater, id)
		}
	}
	for id, present := range oldGym {
		if present && !newGym[id] {
			diff.RemovedGym = append(diff.RemovedGym, id)
		}
	}
	for _, ids := range [][]uuid.UUID{diff.RetainedSegments, diff.AddedSegments, diff.RemovedSegments, diff.RetainedBlocks, diff.AddedBlocks, diff.RemovedBlocks, diff.RetainedSteps, diff.AddedSteps, diff.RemovedSteps, diff.RetainedExercises, diff.AddedExercises, diff.RemovedExercises, diff.RemovedWater, diff.RemovedGym} {
		focusedEditSortedIDs(ids)
	}
	return diff, nil
}

// Slots are bounded by counts, never old MAX(position). Positive temporary
// positions cannot collide with current rows or final positions1..N.
func focusedEditStagePositions(current map[uuid.UUID]int32, retained []uuid.UUID, finalCount, limit int) (map[uuid.UUID]int32, error) {
	out := map[uuid.UUID]int32{}
	if limit < 1 || len(current) > limit || finalCount < 0 || finalCount > limit || len(retained) > len(current) || len(retained) > finalCount {
		return nil, errFocusedWorkoutLimit
	}
	used := map[int32]bool{}
	for id, p := range current {
		if id == uuid.Nil || p <= 0 || used[p] {
			return nil, errFocusedWorkoutInvalid
		}
		used[p] = true
	}
	ids := append([]uuid.UUID(nil), retained...)
	focusedEditSortedIDs(ids)
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		if _, ok := current[id]; !ok || seen[id] {
			return nil, errFocusedWorkoutInvalid
		}
		seen[id] = true
	}
	next, end := int32(finalCount+1), int32(finalCount+len(current)+len(ids))
	for _, id := range ids {
		for next <= end && used[next] {
			next++
		}
		if next > end {
			return nil, errFocusedWorkoutInvalid
		}
		out[id] = next
		used[next] = true
		next++
	}
	return out, nil
}
