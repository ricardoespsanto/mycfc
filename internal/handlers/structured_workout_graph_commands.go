package handlers

import (
	"net/url"
	"strings"
)

func focusedGraphActivityAt(d focusedGraphDraft, key string) (focusedGraphActivity, bool) {
	for _, a := range d.Activities {
		if a.Key == key {
			return a, true
		}
	}
	return focusedGraphActivity{}, false
}
func focusedGraphPhaseAt(d focusedGraphDraft, a, p string) (focusedGraphPhase, bool) {
	activity, ok := focusedGraphActivityAt(d, a)
	if !ok {
		return focusedGraphPhase{}, false
	}
	for _, phase := range activity.Phases {
		if phase.Key == p {
			return phase, true
		}
	}
	return focusedGraphPhase{}, false
}
func focusedGraphIndex(v []string, k string) int {
	for i, x := range v {
		if x == k {
			return i
		}
	}
	return -1
}
func focusedGraphWithout(v []string, k string) []string {
	out := []string{}
	for _, x := range v {
		if x != k {
			out = append(out, x)
		}
	}
	return out
}
func focusedGraphInsert(v, add []string, index int) []string {
	if index > len(v) {
		index = len(v)
	}
	out := append([]string(nil), v[:index]...)
	out = append(out, add...)
	return append(out, v[index:]...)
}
func focusedGraphMove(v []string, k string, direction int) ([]string, error) {
	out := append([]string(nil), v...)
	i := focusedGraphIndex(out, k)
	if i < 0 || i+direction < 0 || i+direction >= len(out) {
		return nil, errFocusedWorkoutInvalid
	}
	out[i], out[i+direction] = out[i+direction], out[i]
	return out, nil
}
func focusedGraphNodeKeys(p focusedGraphPhase) []string {
	out := []string{}
	for _, n := range p.Nodes {
		out = append(out, n.Key)
	}
	return out
}
func focusedGraphChildren(p focusedGraphPhase, parent string) []string {
	out := []string{}
	for _, n := range p.Nodes {
		if n.Parent == parent {
			out = append(out, n.Key)
		}
	}
	return out
}
func focusedGraphSubtree(p focusedGraphPhase, root string) []string {
	out := []string{root}
	for _, k := range focusedGraphChildren(p, root) {
		out = append(out, focusedGraphSubtree(p, k)...)
	}
	return out
}
func focusedGraphPreorder(p focusedGraphPhase) []string {
	out := []string{}
	for _, k := range focusedGraphChildren(p, "") {
		out = append(out, focusedGraphSubtree(p, k)...)
	}
	return out
}
func focusedGraphUsedKeys(d focusedGraphDraft) map[string]bool {
	used := map[string]bool{}
	remember := func(path string) {
		for _, part := range strings.Split(path, "[")[1:] {
			if i := strings.Index(part, "]"); i >= 0 {
				used[part[:i]] = true
			}
		}
	}
	for path := range focusedGraphPaths(d) {
		remember(path)
	}
	for _, r := range d.Removed {
		used[r.Key] = true
		var t focusedGraphTombstone
		if focusedStrictJSON([]byte(r.Payload), &t) == nil {
			for _, key := range []string{t.Activity, t.Phase, t.Item, t.Parent} {
				if key != "" {
					used[key] = true
				}
			}
			for path := range t.Values {
				remember(path)
			}
		}
	}
	return used
}

// All commands operate on an already structurally validated draft, clone before
// mutation, and reparse the complete active+removed transport before returning.
func applyFocusedGraphCommand(original focusedGraphDraft, command string) (focusedGraphDraft, error) {
	return applyFocusedGraphCommandUsing(original, command, parseFocusedGraph)
}
func applyFocusedGraphCommandUsing(original focusedGraphDraft, command string, parse func(url.Values) (focusedGraphDraft, error)) (focusedGraphDraft, error) {
	d := original
	d.Values = cloneFocusedValues(original.Values)
	d.Removed = append([]focusedGraphRemoval(nil), original.Removed...)
	parts := strings.Split(command, ":")
	used := focusedGraphUsedKeys(original)
	fail := func() (focusedGraphDraft, error) { return original, errFocusedWorkoutInvalid }
	phase := focusedGraphPhase{}
	activity := focusedGraphActivity{}
	if len(parts) > 1 && parts[0] != "restore" {
		var ok bool
		activity, ok = focusedGraphActivityAt(original, parts[1])
		if !ok && parts[0] != "activity_add" {
			return fail()
		}
	}
	if len(parts) > 2 {
		var ok bool
		phase, ok = focusedGraphPhaseAt(original, parts[1], parts[2])
		if !ok {
			return fail()
		}
	}
	var tomb *focusedGraphTombstone
	switch parts[0] {
	case "refresh", "edit":
		if len(parts) != 1 {
			return fail()
		}
	case "activity_add":
		if len(parts) != 1 {
			return fail()
		}
		key := focusedGraphNewKey("a_", used)
		d.Values.Add("activity_order", key)
	case "phase_add":
		if len(parts) != 2 {
			return fail()
		}
		key := focusedGraphNewKey("p_", used)
		d.Values.Add(focusedActivityPrefix(activity.Key)+".phase_order", key)
	case "water_effort_add", "water_group_add":
		if len(parts) != 4 {
			return fail()
		}
		parent := parts[3]
		if parent == "_root" {
			parent = ""
		} else {
			found := false
			for _, n := range phase.Nodes {
				if n.Key == parent && d.Values.Get(focusedNodePrefix(activity.Key, phase.Key, parent)+".kind") == "REPEAT_GROUP" {
					found = true
				}
			}
			if !found {
				return fail()
			}
		}
		key := focusedGraphNewKey("n_", used)
		prefix := focusedNodePrefix(activity.Key, phase.Key, key)
		kind := "EFFORT"
		if parts[0] == "water_group_add" {
			kind = "REPEAT_GROUP"
		}
		d.Values.Set(prefix+".kind", kind)
		d.Values.Set(prefix+".parent_key", parent)
		phase.Nodes = append(append([]focusedGraphNode(nil), phase.Nodes...), focusedGraphNode{key, parent})
		d.Values[focusedGraphPhasePrefix(activity.Key, phase.Key)+".water.node_order"] = focusedGraphPreorder(phase)
	case "gym_add":
		if len(parts) != 3 {
			return fail()
		}
		d.Values.Add(focusedGraphPhasePrefix(activity.Key, phase.Key)+".gym.exercise_order", focusedGraphNewKey("e_", used))
	case "activity_up", "activity_down":
		if len(parts) != 2 {
			return fail()
		}
		direction := -1
		if strings.HasSuffix(parts[0], "down") {
			direction = 1
		}
		order, e := focusedGraphMove(d.Values["activity_order"], activity.Key, direction)
		if e != nil {
			return fail()
		}
		d.Values["activity_order"] = order
	case "phase_up", "phase_down":
		if len(parts) != 3 {
			return fail()
		}
		direction := -1
		if strings.HasSuffix(parts[0], "down") {
			direction = 1
		}
		field := focusedActivityPrefix(activity.Key) + ".phase_order"
		order, e := focusedGraphMove(d.Values[field], phase.Key, direction)
		if e != nil {
			return fail()
		}
		d.Values[field] = order
	case "water_up", "water_down":
		if len(parts) != 4 {
			return fail()
		}
		key := parts[3]
		parent := ""
		found := false
		for _, n := range phase.Nodes {
			if n.Key == key {
				parent = n.Parent
				found = true
			}
		}
		if !found {
			return fail()
		}
		direction := -1
		if strings.HasSuffix(parts[0], "down") {
			direction = 1
		}
		siblings, e := focusedGraphMove(focusedGraphChildren(phase, parent), key, direction)
		if e != nil {
			return fail()
		}
		// Reorder complete sibling subtrees; keep every child's parent fixed.
		byParent := map[string][]string{}
		for _, n := range phase.Nodes {
			byParent[n.Parent] = append(byParent[n.Parent], n.Key)
		}
		byParent[parent] = siblings
		order := []string{}
		var visit func(string)
		visit = func(par string) {
			for _, k := range byParent[par] {
				order = append(order, k)
				visit(k)
			}
		}
		visit("")
		d.Values[focusedGraphPhasePrefix(activity.Key, phase.Key)+".water.node_order"] = order
	case "gym_up", "gym_down":
		if len(parts) != 4 {
			return fail()
		}
		direction := -1
		if strings.HasSuffix(parts[0], "down") {
			direction = 1
		}
		field := focusedGraphPhasePrefix(activity.Key, phase.Key) + ".gym.exercise_order"
		order, e := focusedGraphMove(d.Values[field], parts[3], direction)
		if e != nil {
			return fail()
		}
		d.Values[field] = order
	case "activity_remove":
		if len(parts) != 2 {
			return fail()
		}
		prefix := focusedActivityPrefix(activity.Key)
		tomb = &focusedGraphTombstone{Kind: "activity", Activity: activity.Key, Index: focusedGraphIndex(d.Values["activity_order"], activity.Key), Values: url.Values{}}
		for k, v := range d.Values {
			if strings.HasPrefix(k, prefix+".") {
				tomb.Values[k] = append([]string(nil), v...)
				delete(d.Values, k)
			}
		}
		d.Values["activity_order"] = focusedGraphWithout(d.Values["activity_order"], activity.Key)
	case "phase_remove":
		if len(parts) != 3 {
			return fail()
		}
		prefix := focusedGraphPhasePrefix(activity.Key, phase.Key)
		order := focusedActivityPrefix(activity.Key) + ".phase_order"
		tomb = &focusedGraphTombstone{Kind: "phase", Activity: activity.Key, Phase: phase.Key, Index: focusedGraphIndex(d.Values[order], phase.Key), Values: url.Values{}}
		for k, v := range d.Values {
			if strings.HasPrefix(k, prefix+".") {
				tomb.Values[k] = append([]string(nil), v...)
				delete(d.Values, k)
			}
		}
		d.Values[order] = focusedGraphWithout(d.Values[order], phase.Key)
	case "water_remove":
		if len(parts) != 4 || focusedGraphIndex(focusedGraphNodeKeys(phase), parts[3]) < 0 {
			return fail()
		}
		key := parts[3]
		parent := ""
		for _, n := range phase.Nodes {
			if n.Key == key {
				parent = n.Parent
			}
		}
		order := focusedGraphPhasePrefix(activity.Key, phase.Key) + ".water.node_order"
		keys := focusedGraphSubtree(phase, key)
		tomb = &focusedGraphTombstone{Kind: "water", Activity: activity.Key, Phase: phase.Key, Item: key, Parent: parent, Index: focusedGraphIndex(focusedGraphChildren(phase, parent), key), Values: url.Values{order: keys}}
		for _, nk := range keys {
			prefix := focusedNodePrefix(activity.Key, phase.Key, nk)
			for k, v := range d.Values {
				if strings.HasPrefix(k, prefix+".") {
					tomb.Values[k] = append([]string(nil), v...)
					delete(d.Values, k)
				}
			}
			d.Values[order] = focusedGraphWithout(d.Values[order], nk)
		}
	case "gym_remove":
		if len(parts) != 4 || focusedGraphIndex(phase.Exercises, parts[3]) < 0 {
			return fail()
		}
		key := parts[3]
		prefix := focusedExercisePrefix(activity.Key, phase.Key, key)
		order := focusedGraphPhasePrefix(activity.Key, phase.Key) + ".gym.exercise_order"
		tomb = &focusedGraphTombstone{Kind: "gym", Activity: activity.Key, Phase: phase.Key, Item: key, Index: focusedGraphIndex(d.Values[order], key), Values: url.Values{order: {key}}}
		for k, v := range d.Values {
			if strings.HasPrefix(k, prefix+".") {
				tomb.Values[k] = append([]string(nil), v...)
				delete(d.Values, k)
			}
		}
		d.Values[order] = focusedGraphWithout(d.Values[order], key)
	case "restore":
		if len(parts) != 2 {
			return fail()
		}
		index := -1
		var t focusedGraphTombstone
		for i, r := range d.Removed {
			if r.Key == parts[1] {
				index = i
				if focusedStrictJSON([]byte(r.Payload), &t) != nil {
					return fail()
				}
				break
			}
		}
		if index < 0 {
			return fail()
		}
		field := "activity_order"
		items := []string{t.Activity}
		if t.Kind != "activity" {
			if _, ok := focusedGraphActivityAt(original, t.Activity); !ok {
				return fail()
			}
			field = focusedActivityPrefix(t.Activity) + ".phase_order"
			items = []string{t.Phase}
		}
		if t.Kind == "water" || t.Kind == "gym" {
			if _, ok := focusedGraphPhaseAt(original, t.Activity, t.Phase); !ok {
				return fail()
			}
			field = focusedGraphPhasePrefix(t.Activity, t.Phase)
			if t.Kind == "water" {
				field += ".water.node_order"
				items = t.Values[field]
			} else {
				field += ".gym.exercise_order"
				items = []string{t.Item}
			}
		}
		merged := focusedGraphInsert(d.Values[field], items, t.Index)
		if t.Kind == "water" {
			current, _ := focusedGraphPhaseAt(original, t.Activity, t.Phase)
			if t.Parent != "" && focusedGraphIndex(focusedGraphNodeKeys(current), t.Parent) < 0 {
				return fail()
			}
			byParent := map[string][]string{}
			for _, n := range current.Nodes {
				byParent[n.Parent] = append(byParent[n.Parent], n.Key)
			}
			byParent[t.Parent] = focusedGraphInsert(byParent[t.Parent], []string{t.Item}, t.Index)
			for _, key := range items {
				if key != t.Item {
					parent := t.Values.Get(focusedNodePrefix(t.Activity, t.Phase, key) + ".parent_key")
					byParent[parent] = append(byParent[parent], key)
				}
			}
			merged = nil
			var visit func(string)
			visit = func(parent string) {
				for _, key := range byParent[parent] {
					merged = append(merged, key)
					visit(key)
				}
			}
			visit("")
		}
		for k, v := range t.Values {
			if k == field {
				continue
			}
			if _, exists := d.Values[k]; exists {
				return fail()
			}
			d.Values[k] = append([]string(nil), v...)
		}
		d.Values[field] = merged
		d.Removed = append(d.Removed[:index:index], d.Removed[index+1:]...)
	default:
		return fail()
	}
	if tomb != nil {
		key := focusedGraphNewKey("r_", used)
		d.Removed = append(d.Removed, focusedGraphRemoval{key, tomb.Kind, focusedGraphTombstoneJSON(*tomb)})
	}
	reparsed, err := parse(focusedGraphValues(d))
	if err != nil {
		return original, err
	}
	return reparsed, nil
}
