package handlers

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

const focusedMaxActivities = 8
const focusedMaxWaterNodes = 256
const focusedMaxGymExercises = 256
const focusedMaxWaterDepth = 8

var focusedActivityFields = []string{"id", "modality", "title", "location", "duration_minutes", "start_offset_minutes", "transition_minutes", "equipment_notes"}
var focusedPhaseFields = []string{"id", "purpose", "title", "instructions", "detail_mode"}
var focusedWaterFields = []string{"method", "profile_id", "target_distance_metres", "target_distance_certainty"}
var focusedNodeFields = []string{"id", "parent_key", "kind", "name", "repeats", "duration_seconds", "duration_certainty", "distance_metres", "distance_certainty", "recovery_seconds", "intensity_code", "cadence_spm", "drill_focus", "drill_format", "role_notes", "instructions"}
var focusedGymFields = []string{"structure", "objective", "rounds", "round_recovery_seconds"}
var focusedExerciseFields = []string{"id", "name", "sets", "repetitions", "duration_seconds", "distance_metres", "recovery_seconds", "resistance_kind", "resistance_value", "resistance_text", "execution_intent", "tempo", "notes"}

type focusedGraphNode struct{ Key, Parent string }
type focusedGraphPhase struct {
	Key       string
	Nodes     []focusedGraphNode
	Exercises []string
}
type focusedGraphActivity struct {
	Key    string
	Phases []focusedGraphPhase
}
type focusedGraphRemoval struct{ Key, Kind, Payload string }
type focusedGraphDraft struct {
	Values     url.Values
	Activities []focusedGraphActivity
	Removed    []focusedGraphRemoval
}
type focusedGraphCounts struct{ Activities, Phases, Nodes, Exercises int }
type focusedGraphTombstone struct {
	Kind     string     `json:"kind"`
	Activity string     `json:"activity"`
	Phase    string     `json:"phase"`
	Item     string     `json:"item"`
	Parent   string     `json:"parent"`
	Index    int        `json:"index"`
	Values   url.Values `json:"values"`
}

func focusedActivityPrefix(a string) string { return "activities[" + a + "]" }
func focusedGraphPhasePrefix(a, p string) string {
	return focusedActivityPrefix(a) + ".phases[" + p + "]"
}
func focusedNodePrefix(a, p, n string) string {
	return focusedGraphPhasePrefix(a, p) + ".water.nodes[" + n + "]"
}
func focusedExercisePrefix(a, p, e string) string {
	return focusedGraphPhasePrefix(a, p) + ".gym.exercises[" + e + "]"
}
func cloneFocusedValues(v url.Values) url.Values {
	out := url.Values{}
	for k, x := range v {
		out[k] = append([]string(nil), x...)
	}
	return out
}
func focusedUniqueKeys(keys []string, max int) bool {
	if len(keys) > max {
		return false
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if !focusedWorkoutKey.MatchString(k) || seen[k] {
			return false
		}
		seen[k] = true
	}
	return true
}
func (c focusedGraphCounts) within() bool {
	return c.Activities <= focusedMaxActivities && c.Phases <= focusedWorkoutMaxPhases && c.Nodes <= focusedMaxWaterNodes && c.Exercises <= focusedMaxGymExercises
}
func (c *focusedGraphCounts) add(x focusedGraphCounts) {
	c.Activities += x.Activities
	c.Phases += x.Phases
	c.Nodes += x.Nodes
	c.Exercises += x.Exercises
}
func focusedGraphPaths(d focusedGraphDraft) map[string]bool {
	out := map[string]bool{}
	for _, a := range d.Activities {
		out[focusedActivityPrefix(a.Key)] = true
		for _, p := range a.Phases {
			out[focusedGraphPhasePrefix(a.Key, p.Key)] = true
			for _, n := range p.Nodes {
				out[focusedNodePrefix(a.Key, p.Key, n.Key)] = true
			}
			for _, e := range p.Exercises {
				out[focusedExercisePrefix(a.Key, p.Key, e)] = true
			}
		}
	}
	return out
}

// Topology is parsed before semantic validation. Contradictory kind/detail
// choices and incomplete scalar strings stay editable; foreign/cyclic/order
// topology and caller-supplied persistent identities never become authority.
func parseFocusedGraph(input url.Values) (focusedGraphDraft, error) {
	return parseFocusedGraphInternal(input, true)
}
func parseFocusedGraphInternal(input url.Values, removals bool) (focusedGraphDraft, error) {
	d := focusedGraphDraft{Values: url.Values{}}
	if len(input) > 20000 || len(input.Encode()) > focusedWorkoutMaxRequestBytes {
		return d, errFocusedWorkoutLimit
	}
	allowed := map[string]bool{}
	repeated := map[string]bool{"activity_order": true}
	for k := range focusedWorkoutOrdinary {
		allowed[k] = true
	}
	for k := range focusedWorkoutContextKeys {
		allowed[k] = true
	}
	for _, k := range []string{"activity_order", "command", "gorilla.csrf.Token", "prior_proposal", "prior_review_token"} {
		allowed[k] = true
	}
	addFields := func(prefix string, fields []string) {
		for _, f := range fields {
			allowed[prefix+"."+f] = true
		}
	}
	addOrder := func(name string) { allowed[name] = true; repeated[name] = true }
	if len(input["activity_order"]) > focusedMaxActivities {
		return d, errFocusedWorkoutLimit
	}
	if !focusedUniqueKeys(input["activity_order"], focusedMaxActivities) {
		return d, errFocusedWorkoutInvalid
	}
	counts := focusedGraphCounts{Activities: len(input["activity_order"])}
	for _, ak := range input["activity_order"] {
		a := focusedGraphActivity{Key: ak}
		ap := focusedActivityPrefix(ak)
		addFields(ap, focusedActivityFields)
		addOrder(ap + ".phase_order")
		if len(input[ap+".phase_order"]) > focusedWorkoutMaxPhases {
			return d, errFocusedWorkoutLimit
		}
		if !focusedUniqueKeys(input[ap+".phase_order"], focusedWorkoutMaxPhases) {
			return d, errFocusedWorkoutInvalid
		}
		for _, pk := range input[ap+".phase_order"] {
			counts.Phases++
			p := focusedGraphPhase{Key: pk}
			pp := focusedGraphPhasePrefix(ak, pk)
			addFields(pp, focusedPhaseFields)
			addFields(pp+".water", focusedWaterFields)
			addFields(pp+".gym", focusedGymFields)
			addOrder(pp + ".water.node_order")
			addOrder(pp + ".gym.exercise_order")
			nodes := input[pp+".water.node_order"]
			exercises := input[pp+".gym.exercise_order"]
			if len(nodes) > focusedMaxWaterNodes || len(exercises) > focusedMaxGymExercises {
				return d, errFocusedWorkoutLimit
			}
			if !focusedUniqueKeys(nodes, focusedMaxWaterNodes) || !focusedUniqueKeys(exercises, focusedMaxGymExercises) {
				return d, errFocusedWorkoutInvalid
			}
			parent := map[string]string{}
			for _, nk := range nodes {
				np := focusedNodePrefix(ak, pk, nk)
				addFields(np, focusedNodeFields)
				parent[nk] = input.Get(np + ".parent_key")
				p.Nodes = append(p.Nodes, focusedGraphNode{nk, parent[nk]})
			}
			for _, n := range p.Nodes {
				current := n.Key
				seen := map[string]bool{}
				for depth := 1; current != ""; depth++ {
					par, ok := parent[current]
					if !ok || seen[current] || depth > focusedMaxWaterDepth {
						return d, errFocusedWorkoutInvalid
					}
					seen[current] = true
					current = par
				}
			}
			// Require one explicit parent-first DFS order, matching the actual native
			// nested controls; never silently repair or filter an untrusted graph.
			expected := []string{}
			var walk func(string)
			walk = func(par string) {
				for _, n := range p.Nodes {
					if n.Parent == par {
						expected = append(expected, n.Key)
						walk(n.Key)
					}
				}
			}
			walk("")
			if len(expected) != len(nodes) {
				return d, errFocusedWorkoutInvalid
			}
			for i, k := range nodes {
				if expected[i] != k {
					return d, errFocusedWorkoutInvalid
				}
			}
			for _, ek := range exercises {
				addFields(focusedExercisePrefix(ak, pk, ek), focusedExerciseFields)
				p.Exercises = append(p.Exercises, ek)
			}
			counts.Nodes += len(nodes)
			counts.Exercises += len(exercises)
			a.Phases = append(a.Phases, p)
		}
		d.Activities = append(d.Activities, a)
	}
	if !counts.within() {
		return d, errFocusedWorkoutLimit
	}
	if removals {
		addOrder("removed_order")
		if !focusedUniqueKeys(input["removed_order"], focusedMaxActivities+focusedWorkoutMaxPhases+focusedMaxWaterNodes+focusedMaxGymExercises) {
			return d, errFocusedWorkoutInvalid
		}
		paths := focusedGraphPaths(d)
		for _, rk := range input["removed_order"] {
			rp := "removed[" + rk + "]"
			addFields(rp, []string{"kind", "id", "key", "payload"})
			if input.Get(rp+".key") != rk || input.Get(rp+".id") != "" {
				return d, errFocusedWorkoutInvalid
			}
			var tomb focusedGraphTombstone
			if focusedStrictJSON([]byte(input.Get(rp+".payload")), &tomb) != nil || tomb.Kind != input.Get(rp+".kind") {
				return d, errFocusedWorkoutInvalid
			}
			sub, n, path, err := focusedGraphTombstoneGraph(tomb)
			if err != nil {
				return d, err
			}
			for p := range focusedGraphRemovedPaths(sub, tomb, path) {
				if paths[p] {
					return d, errFocusedWorkoutInvalid
				}
				paths[p] = true
			}
			counts.add(n)
			if !counts.within() {
				return d, errFocusedWorkoutLimit
			}
			d.Removed = append(d.Removed, focusedGraphRemoval{rk, tomb.Kind, input.Get(rp + ".payload")})
		}
	}
	for k, v := range input {
		if !allowed[k] || (!repeated[k] && len(v) != 1) {
			return d, errFocusedWorkoutInvalid
		}
		for _, raw := range v {
			if !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) {
				return d, errFocusedWorkoutInvalid
			}
		}
		if strings.HasSuffix(k, ".id") && len(v) > 0 && v[0] != "" {
			return d, errFocusedWorkoutInvalid
		}
		if k != "command" && k != "gorilla.csrf.Token" && k != "prior_proposal" && k != "prior_review_token" && !focusedWorkoutContextKeys[k] && !strings.HasPrefix(k, "removed[") && k != "removed_order" {
			d.Values[k] = append([]string(nil), v...)
		}
	}
	return d, nil
}
func focusedGraphTombstoneGraph(t focusedGraphTombstone) (focusedGraphDraft, focusedGraphCounts, string, error) {
	var zero focusedGraphDraft
	var counts focusedGraphCounts
	if !focusedWorkoutKey.MatchString(t.Activity) || t.Index < 0 || t.Index >= focusedMaxWaterNodes {
		return zero, counts, "", errFocusedWorkoutInvalid
	}
	raw := cloneFocusedValues(t.Values)
	ap := focusedActivityPrefix(t.Activity)
	path := ap
	raw["activity_order"] = []string{t.Activity}
	if t.Kind != "activity" {
		if !focusedWorkoutKey.MatchString(t.Phase) {
			return zero, counts, "", errFocusedWorkoutInvalid
		}
		raw[ap+".phase_order"] = []string{t.Phase}
		path = focusedGraphPhasePrefix(t.Activity, t.Phase)
	}
	switch t.Kind {
	case "activity":
		if t.Phase != "" || t.Item != "" || t.Parent != "" || t.Index >= focusedMaxActivities {
			return zero, counts, "", errFocusedWorkoutInvalid
		}
	case "phase":
		if t.Item != "" || t.Parent != "" || t.Index >= focusedWorkoutMaxPhases {
			return zero, counts, "", errFocusedWorkoutInvalid
		}
	case "water":
		if !focusedWorkoutKey.MatchString(t.Item) || (t.Parent != "" && !focusedWorkoutKey.MatchString(t.Parent)) {
			return zero, counts, "", errFocusedWorkoutInvalid
		}
		path = focusedNodePrefix(t.Activity, t.Phase, t.Item)
		raw[path+".parent_key"] = []string{""}
	case "gym":
		if !focusedWorkoutKey.MatchString(t.Item) || t.Parent != "" {
			return zero, counts, "", errFocusedWorkoutInvalid
		}
		path = focusedExercisePrefix(t.Activity, t.Phase, t.Item)
	default:
		return zero, counts, "", errFocusedWorkoutInvalid
	}
	// Tombstones carry only the removed subtree; order scaffolding is explicit.
	allowed := map[string]bool{}
	switch t.Kind {
	case "activity", "phase":
		for k := range t.Values {
			if !strings.HasPrefix(k, path+".") {
				return zero, counts, "", errFocusedWorkoutInvalid
			}
		}
	case "water":
		order := focusedGraphPhasePrefix(t.Activity, t.Phase) + ".water.node_order"
		keys := t.Values[order]
		if len(keys) == 0 || keys[0] != t.Item {
			return zero, counts, "", errFocusedWorkoutInvalid
		}
		allowed[order] = true
		for _, key := range keys {
			for _, f := range focusedNodeFields {
				allowed[focusedNodePrefix(t.Activity, t.Phase, key)+"."+f] = true
			}
		}
		if t.Values.Get(path+".parent_key") != t.Parent {
			return zero, counts, "", errFocusedWorkoutInvalid
		}
		for k := range t.Values {
			if !allowed[k] {
				return zero, counts, "", errFocusedWorkoutInvalid
			}
		}
	case "gym":
		order := focusedGraphPhasePrefix(t.Activity, t.Phase) + ".gym.exercise_order"
		if len(t.Values[order]) != 1 || t.Values.Get(order) != t.Item {
			return zero, counts, "", errFocusedWorkoutInvalid
		}
		for k := range t.Values {
			if k != order && !strings.HasPrefix(k, path+".") {
				return zero, counts, "", errFocusedWorkoutInvalid
			}
		}
	}
	sub, err := parseFocusedGraphInternal(raw, false)
	if err != nil {
		return zero, counts, "", err
	}
	if t.Kind == "water" {
		nodes := sub.Activities[0].Phases[0].Nodes
		for _, n := range nodes {
			if n.Key != t.Item && n.Parent == "" {
				return zero, counts, "", errFocusedWorkoutInvalid
			}
		}
	}
	for _, a := range sub.Activities {
		counts.Activities++
		for _, p := range a.Phases {
			counts.Phases++
			counts.Nodes += len(p.Nodes)
			counts.Exercises += len(p.Exercises)
		}
	}
	if t.Kind != "activity" {
		counts.Activities--
	}
	if t.Kind == "water" || t.Kind == "gym" {
		counts.Phases--
	}
	return sub, counts, path, nil
}
func focusedGraphRemovedPaths(sub focusedGraphDraft, t focusedGraphTombstone, path string) map[string]bool {
	out := map[string]bool{}
	if t.Kind == "water" {
		for _, n := range sub.Activities[0].Phases[0].Nodes {
			out[focusedNodePrefix(t.Activity, t.Phase, n.Key)] = true
		}
		return out
	}
	for p := range focusedGraphPaths(sub) {
		if p == path || strings.HasPrefix(p, path+".") {
			out[p] = true
		}
	}
	return out
}

func focusedGraphValues(d focusedGraphDraft) url.Values {
	out := cloneFocusedValues(d.Values)
	for _, r := range d.Removed {
		out.Add("removed_order", r.Key)
		p := "removed[" + r.Key + "]"
		out.Set(p+".kind", r.Kind)
		out.Set(p+".id", focusedGraphRemovedID(r))
		out.Set(p+".key", r.Key)
		out.Set(p+".payload", r.Payload)
	}
	return out
}
func focusedGraphNewKey(prefix string, used map[string]bool) string {
	for i := 1; ; i++ {
		k := prefix + strconv.Itoa(i)
		if !used[k] {
			return k
		}
	}
}
func focusedGraphTombstoneJSON(t focusedGraphTombstone) string {
	raw, _ := json.Marshal(t)
	return string(raw)
}
