package handlers

import (
	"net/url"
	"strings"

	"github.com/google/uuid"
)

type focusedEditIdentity struct {
	IDs     map[string]uuid.UUID
	Parents map[string]string
}

func focusedEditIdentityFor(source focusedWorkoutGraph) (focusedEditIdentity, error) {
	out := focusedEditIdentity{IDs: map[string]uuid.UUID{}, Parents: map[string]string{}}
	seen := map[uuid.UUID]bool{source.SessionID: true}
	add := func(path string, id uuid.UUID) bool {
		if id == uuid.Nil || seen[id] {
			return false
		}
		seen[id] = true
		out.IDs[path] = id
		return true
	}
	if source.SessionID == uuid.Nil {
		return out, errFocusedWorkoutInvalid
	}
	for _, a := range source.Activities {
		if !add(focusedActivityPrefix(a.Key), a.ID) {
			return out, errFocusedWorkoutInvalid
		}
		for _, p := range a.Phases {
			if !add(focusedGraphPhasePrefix(a.Key, p.Key), p.ID) {
				return out, errFocusedWorkoutInvalid
			}
			if p.Water != nil {
				for _, n := range p.Water.Nodes {
					path := focusedNodePrefix(a.Key, p.Key, n.Key)
					if !add(path, n.ID) {
						return out, errFocusedWorkoutInvalid
					}
					out.Parents[path] = n.ParentKey
				}
			}
			if p.Gym != nil {
				for _, x := range p.Gym.Exercises {
					if !add(focusedExercisePrefix(a.Key, p.Key, x.Key), x.ID) {
						return out, errFocusedWorkoutInvalid
					}
				}
			}
		}
	}
	return out, nil
}
func focusedEditTombPath(t focusedGraphTombstone) string {
	switch t.Kind {
	case "activity":
		return focusedActivityPrefix(t.Activity)
	case "phase":
		return focusedGraphPhasePrefix(t.Activity, t.Phase)
	case "water":
		return focusedNodePrefix(t.Activity, t.Phase, t.Item)
	case "gym":
		return focusedExercisePrefix(t.Activity, t.Phase, t.Item)
	}
	return ""
}
func focusedGraphRemovedID(r focusedGraphRemoval) string {
	var t focusedGraphTombstone
	if focusedStrictJSON([]byte(r.Payload), &t) != nil {
		return ""
	}
	return t.Values.Get(focusedEditTombPath(t) + ".id")
}

// The CREATE parser still accepts no caller identity. EDIT strips identities
// only for that same structural parser, then validates and restores every ID
// against a freshly authorized, unchanged saved source. No allow-ID flag or
// caller-supplied parent map grants authority.
func parseFocusedEditGraph(input url.Values, source focusedWorkoutGraph) (focusedGraphDraft, error) {
	var zero focusedGraphDraft
	if len(input.Encode()) > focusedWorkoutMaxRequestBytes {
		return zero, errFocusedWorkoutLimit
	}
	identity, err := focusedEditIdentityFor(source)
	if err != nil {
		return zero, err
	}
	stripped := cloneFocusedValues(input)
	for k, values := range stripped {
		if strings.HasSuffix(k, ".id") {
			if len(values) != 1 {
				return zero, errFocusedWorkoutInvalid
			}
			stripped.Set(k, "")
		}
	}
	originals := map[string]focusedGraphTombstone{}
	originalPayload := map[string]string{}
	for _, key := range input["removed_order"] {
		prefix := "removed[" + key + "]"
		var t focusedGraphTombstone
		if focusedStrictJSON([]byte(input.Get(prefix+".payload")), &t) != nil {
			return zero, errFocusedWorkoutInvalid
		}
		originals[key] = t
		originalPayload[key] = input.Get(prefix + ".payload")
		strippedTomb := t
		strippedTomb.Values = cloneFocusedValues(t.Values)
		for k, values := range strippedTomb.Values {
			if strings.HasSuffix(k, ".id") {
				if len(values) != 1 {
					return zero, errFocusedWorkoutInvalid
				}
				strippedTomb.Values.Set(k, "")
			}
		}
		stripped.Set(prefix+".payload", focusedGraphTombstoneJSON(strippedTomb))
	}
	parsed, err := parseFocusedGraph(stripped)
	if err != nil {
		return zero, err
	}
	validate := func(d focusedGraphDraft, values url.Values, detachedRoot, parent string) error {
		for path := range focusedGraphPaths(d) {
			expected, retained := identity.IDs[path]
			actual := values.Get(path + ".id")
			if retained {
				if actual != expected.String() {
					return errFocusedWorkoutInvalid
				}
			} else if actual != "" {
				return errFocusedWorkoutInvalid
			}
		}
		for _, a := range d.Activities {
			for _, p := range a.Phases {
				for _, n := range p.Nodes {
					path := focusedNodePrefix(a.Key, p.Key, n.Key)
					if expected, retained := identity.Parents[path]; retained {
						actual := n.Parent
						if path == detachedRoot {
							actual = parent
						}
						if actual != expected {
							return errFocusedWorkoutInvalid
						}
					}
				}
			}
		}
		return nil
	}
	if err = validate(parsed, input, "", ""); err != nil {
		return zero, err
	}
	for path := range focusedGraphPaths(parsed) {
		if values, ok := input[path+".id"]; ok {
			parsed.Values[path+".id"] = append([]string(nil), values...)
		}
	}
	for i, r := range parsed.Removed {
		original := originals[r.Key]
		path := focusedEditTombPath(original)
		expected := identity.IDs[path]
		required := ""
		if expected != uuid.Nil {
			required = expected.String()
		}
		if input.Get("removed["+r.Key+"].id") != required {
			return zero, errFocusedWorkoutInvalid
		}
		strippedTomb := original
		strippedTomb.Values = cloneFocusedValues(original.Values)
		for k := range strippedTomb.Values {
			if strings.HasSuffix(k, ".id") {
				strippedTomb.Values.Set(k, "")
			}
		}
		sub, _, _, err := focusedGraphTombstoneGraph(strippedTomb)
		if err != nil {
			return zero, err
		}
		// Supply trusted ancestor IDs only for structural scaffolding. They are not
		// part of the removed subtree and do not authorize another parent.
		complete := cloneFocusedValues(original.Values)
		for p := range focusedGraphPaths(sub) {
			ancestor := p == focusedActivityPrefix(original.Activity) && original.Kind != "activity" || p == focusedGraphPhasePrefix(original.Activity, original.Phase) && (original.Kind == "water" || original.Kind == "gym")
			if ancestor {
				if id := identity.IDs[p]; id != uuid.Nil {
					complete.Set(p+".id", id.String())
				}
			}
		}
		if err = validate(sub, complete, path, original.Parent); err != nil {
			return zero, err
		}
		parsed.Removed[i].Payload = originalPayload[r.Key]
	}
	return parsed, nil
}
func applyFocusedEditCommand(original focusedGraphDraft, command string, source focusedWorkoutGraph) (focusedGraphDraft, error) {
	return applyFocusedGraphCommandUsing(original, command, func(v url.Values) (focusedGraphDraft, error) { return parseFocusedEditGraph(v, source) })
}
