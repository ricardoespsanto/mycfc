package handlers

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cfcoimbra/mycfc/internal/validation"
)

var focusedWorkoutKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
var focusedWorkoutOrdinary = map[string]bool{"title": true, "description": true, "entry_kind": true, "schedule.mode": true, "schedule.date": true, "schedule.start_time": true, "schedule.end_date": true, "schedule.end_time": true, "schedule.band": true, "schedule.start_occurrence": true, "schedule.end_occurrence": true}
var focusedWorkoutContextKeys = map[string]bool{"group_id": true, "plan_id": true, "session_id": true, "day": true, "expected_plan_updated_at": true, "expected_session_updated_at": true, "source_digest": true, "database_incarnation": true, "state_epoch": true}

func focusedPhasePrefix(key string) string { return "activities[a_1].phases[" + key + "]" }
func focusedFieldKey(name string) string   { return strings.NewReplacer("[", ".", "]", "").Replace(name) }
func newFocusedWorkoutDraft(day string) focusedWorkoutDraft {
	v := url.Values{"entry_kind": {"TRAINING"}, "schedule.mode": {"START_ONLY"}, "schedule.date": {day}, "activity_order": {"a_1"}, "activities[a_1].modality": {"WATER"}, "activities[a_1].phase_order": {"p_1", "p_2", "p_3"}}
	for i, p := range []string{"WARM_UP", "MAIN", "COOL_DOWN"} {
		v.Set(focusedPhasePrefix("p_"+strconv.Itoa(i+1))+".purpose", p)
	}
	return focusedWorkoutDraft{Values: v, Keys: []string{"p_1", "p_2", "p_3"}}
}
func parseFocusedWorkoutDraft(input url.Values) (focusedWorkoutDraft, error) {
	d := focusedWorkoutDraft{Values: url.Values{}}
	if len(input) > 512 || len(input.Encode()) > focusedWorkoutMaxRequestBytes {
		return d, errFocusedWorkoutLimit
	}
	if order := input["activity_order"]; len(order) != 1 || order[0] != "a_1" {
		return d, errFocusedWorkoutInvalid
	}
	d.Keys = append([]string(nil), input["activities[a_1].phase_order"]...)
	if len(d.Keys) > focusedWorkoutMaxPhases {
		return d, errFocusedWorkoutLimit
	}
	allowed := map[string]bool{}
	for k := range focusedWorkoutOrdinary {
		allowed[k] = true
	}
	for k := range focusedWorkoutContextKeys {
		allowed[k] = true
	}
	allowed["command"], allowed["gorilla.csrf.Token"], allowed["activity_order"], allowed["activities[a_1].phase_order"], allowed["removed_order"] = true, true, true, true, true
	allowed["prior_proposal"], allowed["prior_review_token"] = true, true
	for _, f := range []string{"id", "modality", "title", "location", "duration_minutes", "start_offset_minutes", "transition_minutes", "equipment_notes"} {
		allowed["activities[a_1]."+f] = true
	}
	seen := map[string]bool{}
	for _, key := range d.Keys {
		if !focusedWorkoutKey.MatchString(key) || seen[key] {
			return d, errFocusedWorkoutInvalid
		}
		seen[key] = true
		for _, f := range []string{"id", "purpose", "title", "instructions", "detail_mode"} {
			allowed[focusedPhasePrefix(key)+"."+f] = true
		}
	}
	removed := input["removed_order"]
	if len(removed)+len(d.Keys) > focusedWorkoutMaxPhases {
		return d, errFocusedWorkoutLimit
	}
	for _, key := range removed {
		if !focusedWorkoutKey.MatchString(key) || seen[key] {
			return d, errFocusedWorkoutInvalid
		}
		seen[key] = true
		prefix := "removed[" + key + "]"
		for _, f := range []string{"kind", "id", "key", "payload"} {
			allowed[prefix+"."+f] = true
		}
		if input.Get(prefix+".kind") != "phase" || input.Get(prefix+".id") != "" {
			return d, errFocusedWorkoutInvalid
		}
		if input.Get(prefix+".key") != key {
			return d, errFocusedWorkoutInvalid
		}
		var tomb focusedPhaseTombstone
		if focusedStrictJSON([]byte(input.Get(prefix+".payload")), &tomb) != nil || !focusedWorkoutKey.MatchString(tomb.Key) || tomb.Index < 0 || tomb.Index >= focusedWorkoutMaxPhases {
			return d, errFocusedWorkoutInvalid
		}
		phase := tomb.Key
		if seen["phase:"+phase] {
			return d, errFocusedWorkoutInvalid
		}
		seen["phase:"+phase] = true
		for _, active := range d.Keys {
			if phase == active {
				return d, errFocusedWorkoutInvalid
			}
		}
		raw := tomb.Values
		for field, value := range raw {
			switch field {
			case "purpose", "title", "instructions", "detail_mode", "id":
			default:
				return d, errFocusedWorkoutInvalid
			}
			if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || field == "id" && value != "" {
				return d, errFocusedWorkoutInvalid
			}
		}
		d.Removed = append(d.Removed, focusedWorkoutRemoval{Key: key, PhaseKey: phase, Payload: input.Get(prefix + ".payload")})
	}
	for k, values := range input {
		if !allowed[k] {
			return d, errFocusedWorkoutInvalid
		}
		if k != "activity_order" && k != "activities[a_1].phase_order" && k != "removed_order" && len(values) != 1 {
			return d, errFocusedWorkoutInvalid
		}
		for _, value := range values {
			if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
				return d, errFocusedWorkoutInvalid
			}
		}
		if k != "command" && k != "gorilla.csrf.Token" && k != "prior_proposal" && k != "prior_review_token" && !focusedWorkoutContextKeys[k] && !strings.HasPrefix(k, "removed[") {
			d.Values[k] = append([]string(nil), values...)
		}
	}
	if d.Values.Get("activities[a_1].id") != "" {
		return d, errFocusedWorkoutInvalid
	}
	for _, key := range d.Keys {
		if d.Values.Get(focusedPhasePrefix(key)+".id") != "" {
			return d, errFocusedWorkoutInvalid
		}
	}
	return d, nil
}
func focusedDraftWithRemovalFields(d focusedWorkoutDraft) url.Values {
	out := url.Values{}
	for k, v := range d.Values {
		out[k] = append([]string(nil), v...)
	}
	out["activities[a_1].phase_order"] = append([]string(nil), d.Keys...)
	for _, r := range d.Removed {
		out.Add("removed_order", r.Key)
		prefix := "removed[" + r.Key + "]"
		out.Set(prefix+".kind", "phase")
		out.Set(prefix+".id", "")
		out.Set(prefix+".key", r.Key)
		out.Set(prefix+".payload", r.Payload)
	}
	return out
}
func applyFocusedWorkoutCommand(d focusedWorkoutDraft, command string) (focusedWorkoutDraft, error) {
	// A command changes only the already-validated bounded raw proposal.
	original := d
	d.Values = url.Values{}
	for k, v := range original.Values {
		d.Values[k] = append([]string(nil), v...)
	}
	d.Keys = append([]string(nil), original.Keys...)
	d.Removed = append([]focusedWorkoutRemoval(nil), original.Removed...)
	parts := strings.Split(command, ":")
	switch parts[0] {
	case "refresh", "edit":
		if len(parts) != 1 {
			return original, errFocusedWorkoutInvalid
		}
	case "phase_add":
		if len(parts) != 2 || parts[1] != "a_1" {
			return original, errFocusedWorkoutInvalid
		}
		if len(d.Keys)+len(d.Removed) >= focusedWorkoutMaxPhases {
			return original, errFocusedWorkoutLimit
		}
		next := 1
		used := map[string]bool{}
		for _, k := range d.Keys {
			used[k] = true
		}
		for _, r := range d.Removed {
			used[r.PhaseKey] = true
		}
		for used["p_"+strconv.Itoa(next)] {
			next++
		}
		key := "p_" + strconv.Itoa(next)
		d.Keys = append(d.Keys, key)
		d.Values.Set(focusedPhasePrefix(key)+".purpose", "CUSTOM")
	case "phase_up", "phase_down", "phase_remove":
		if len(parts) != 3 || parts[1] != "a_1" {
			return original, errFocusedWorkoutInvalid
		}
		i := -1
		for n, k := range d.Keys {
			if k == parts[2] {
				i = n
			}
		}
		if i < 0 {
			return original, errFocusedWorkoutInvalid
		}
		if parts[0] == "phase_remove" {
			raw := map[string]string{}
			prefix := focusedPhasePrefix(parts[2])
			for _, f := range []string{"purpose", "title", "instructions", "detail_mode", "id"} {
				raw[f] = d.Values.Get(prefix + "." + f)
			}
			b, _ := json.Marshal(focusedPhaseTombstone{Key: parts[2], Index: i, Values: raw})
			next := 1
			used := map[string]bool{}
			for _, r := range d.Removed {
				used[r.Key] = true
			}
			for _, k := range d.Keys {
				used[k] = true
			}
			for used["r_"+strconv.Itoa(next)] {
				next++
			}
			removedKey := "r_" + strconv.Itoa(next)
			d.Removed = append(d.Removed, focusedWorkoutRemoval{removedKey, parts[2], string(b)})
			d.Keys = append(d.Keys[:i:i], d.Keys[i+1:]...)
			for k := range d.Values {
				if strings.HasPrefix(k, prefix+".") {
					delete(d.Values, k)
				}
			}
		} else {
			j := i - 1
			if parts[0] == "phase_down" {
				j = i + 1
			}
			if j >= 0 && j < len(d.Keys) {
				d.Keys[i], d.Keys[j] = d.Keys[j], d.Keys[i]
			}
		}
	case "restore":
		if len(parts) != 2 {
			return original, errFocusedWorkoutInvalid
		}
		found := -1
		for i, r := range d.Removed {
			if r.Key == parts[1] {
				found = i
			}
		}
		if found < 0 {
			return original, errFocusedWorkoutInvalid
		}
		r := d.Removed[found]
		var tomb focusedPhaseTombstone
		if focusedStrictJSON([]byte(r.Payload), &tomb) != nil {
			return original, errFocusedWorkoutInvalid
		}
		index := min(tomb.Index, len(d.Keys))
		d.Keys = append(d.Keys, "")
		copy(d.Keys[index+1:], d.Keys[index:])
		d.Keys[index] = r.PhaseKey
		for k, v := range tomb.Values {
			d.Values.Set(focusedPhasePrefix(r.PhaseKey)+"."+k, v)
		}
		d.Removed = append(d.Removed[:found:found], d.Removed[found+1:]...)
	default:
		return original, errFocusedWorkoutInvalid
	}
	d.Values["activities[a_1].phase_order"] = append([]string(nil), d.Keys...)
	if len(focusedDraftWithRemovalFields(d).Encode()) > focusedWorkoutMaxRequestBytes {
		return original, errFocusedWorkoutLimit
	}
	return d, nil
}
func focusedText(v url.Values, name string, min, max int, errs validation.FieldErrors) string {
	raw := v.Get(name)
	trimmed := strings.TrimSpace(raw)
	if !validTrainingText(trimmed, min, max) {
		errs.Add(focusedFieldKey(name), "Reveja este texto.")
	}
	return trimmed
}
func focusedOptionalInt(v url.Values, name string, min, max int32, errs validation.FieldErrors) *int32 {
	raw := v.Get(name)
	if raw == "" {
		return nil
	}
	n, e := strconv.ParseInt(raw, 10, 32)
	if e != nil || n < int64(min) || n > int64(max) {
		errs.Add(focusedFieldKey(name), "Indique um número inteiro dentro do intervalo permitido.")
		return nil
	}
	out := int32(n)
	return &out
}

type focusedPhaseTombstone struct {
	Key    string            `json:"key"`
	Index  int               `json:"index"`
	Values map[string]string `json:"values"`
}
