package handlers

import (
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cfcoimbra/mycfc/internal/validation"
)

var reuseOrdinaryFields = []string{"source_kind", "source_plan_id", "source_session_id", "source_day", "routine_id", "target_group_id", "season_id", "week_start", "target_session_id", "target_segment_id", "week_title", "week_description", "planned_load_percentage", "command", "prior_proposal", "prior_review_token", "gorilla.csrf.Token", "routine_query", "routine_modality", "routine_objective", "routine_tag", "routine_page", "group_page"}
var reuseMappingFields = []string{"date", "start_time", "start_occurrence", "end_date", "end_time", "end_occurrence"}

func reuseMappingPrefix(key string) string { return "mappings[" + key + "]" }
func parseReuseValues(input url.Values, keys []string) (url.Values, error) {
	if len(input) > 1000 || len(input.Encode()) > focusedWorkoutMaxRequestBytes || len(keys) > reuseMaxSessions {
		return nil, errFocusedWorkoutLimit
	}
	allowed := map[string]bool{"mapping_order": true}
	for _, name := range reuseOrdinaryFields {
		allowed[name] = true
	}
	for _, key := range keys {
		if !focusedWorkoutKey.MatchString(key) {
			return nil, errFocusedWorkoutInvalid
		}
		for _, name := range reuseMappingFields {
			allowed[reuseMappingPrefix(key)+"."+name] = true
		}
	}
	if len(input["mapping_order"]) != len(keys) {
		return nil, errFocusedWorkoutInvalid
	}
	for i, key := range keys {
		if input["mapping_order"][i] != key {
			return nil, errFocusedWorkoutInvalid
		}
	}
	out := url.Values{}
	for name, values := range input {
		if !allowed[name] || name != "mapping_order" && len(values) != 1 {
			return nil, errFocusedWorkoutInvalid
		}
		for _, value := range values {
			if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
				return nil, errFocusedWorkoutInvalid
			}
		}
		if name != "command" && name != "gorilla.csrf.Token" && name != "prior_proposal" && name != "prior_review_token" {
			out[name] = append([]string(nil), values...)
		}
	}
	return out, nil
}

// Local timestamps may contain the exact seconds/fractions of an existing
// source; copying those represented facts does not round them to edit controls.
func reuseClockOccurrences(day, clock string) []time.Time {
	if len(clock) == 5 {
		return focusedClockOccurrences(day, clock)
	}
	parsed, err := time.Parse("2006-01-02 15:04:05.999999999", day+" "+clock)
	if err != nil || parsed.Year() < 1 || !validWorkspaceDate(day) {
		return nil
	}
	offsets := map[int]bool{}
	for h := -48; h <= 48; h++ {
		_, offset := parsed.Add(time.Duration(h) * time.Hour).In(lisbonLocation()).Zone()
		offsets[offset] = true
	}
	out := []time.Time{}
	for offset := range offsets {
		candidate := parsed.Add(-time.Duration(offset) * time.Second)
		if candidate.In(lisbonLocation()).Format("2006-01-02 15:04:05.999999999") == day+" "+clock {
			out = append(out, candidate.UTC())
		}
	}
	if len(out) == 2 && out[1].Before(out[0]) {
		out[0], out[1] = out[1], out[0]
	}
	return out
}
func reuseSelectedClock(values url.Values, prefix, dateField, timeField, occurrenceField string, e validation.FieldErrors) *time.Time {
	candidates := reuseClockOccurrences(values.Get(prefix+"."+dateField), values.Get(prefix+"."+timeField))
	key := prefix + "." + timeField
	if len(candidates) == 0 {
		e.Add(key, "Indique uma hora existente nesta data em Portugal continental.")
		return nil
	}
	raw := values.Get(prefix + "." + occurrenceField)
	if len(candidates) > 1 && raw == "" {
		e.Add(prefix+"."+occurrenceField, "Esta hora ocorre duas vezes. Escolha a ocorrência pretendida.")
		return nil
	}
	for _, candidate := range candidates {
		if candidate.Nanosecond()%1000 != 0 {
			e.Add(key, "Indique uma hora com precisão até ao microssegundo.")
			return nil
		}
		if raw == "" || raw == candidate.Format(time.RFC3339Nano) {
			return focusedPointer(candidate)
		}
	}
	e.Add(prefix+"."+occurrenceField, "Reveja a ocorrência desta hora.")
	return nil
}
func normalizeReuseSchedule(item reuseSourceItem, values url.Values, week time.Time) (TrainingSessionSchedule, validation.FieldErrors) {
	e := validation.FieldErrors{}
	prefix := reuseMappingPrefix(item.Key)
	day := values.Get(prefix + ".date")
	date, err := time.Parse("2006-01-02", day)
	if err != nil || !validWorkspaceDate(day) || date.Before(week) || !date.Before(week.AddDate(0, 0, 7)) {
		e.Add(prefix+".date", "Escolha uma data da semana de destino.")
	}
	precision := item.Graph.Schedule.Precision
	if item.DurationSeconds != nil {
		precision = TrainingScheduleInterval
	}
	out := TrainingSessionSchedule{Precision: precision, Date: day, Band: func() *TrainingDayBand {
		if item.Graph.Schedule.Band == nil {
			return nil
		}
		return focusedPointer(*item.Graph.Schedule.Band)
	}()}
	if precision == TrainingScheduleDay || precision == TrainingScheduleBand {
		for _, field := range []string{"start_time", "start_occurrence", "end_date", "end_time", "end_occurrence"} {
			if values.Get(prefix+"."+field) != "" {
				e.Add(prefix+"."+field, "Este horário não contém horas. Corrija os valores introduzidos.")
			}
		}
	} else {
		out.StartsAt = reuseSelectedClock(values, prefix, "date", "start_time", "start_occurrence", e)
		if item.DurationSeconds != nil {
			if *item.DurationSeconds < 1 || *item.DurationSeconds > 7*24*3600 {
				e.Add(prefix+".start_time", "A duração guardada nesta rotina não pode ser aplicada.")
			} else if out.StartsAt != nil {
				out.EndsAt = focusedPointer(out.StartsAt.Add(time.Duration(*item.DurationSeconds) * time.Second))
			}
			for _, field := range []string{"end_date", "end_time", "end_occurrence"} {
				if values.Get(prefix+"."+field) != "" {
					e.Add(prefix+"."+field, "Esta rotina conserva a duração guardada; reveja o início e o fim apresentado.")
				}
			}
		} else if precision == TrainingScheduleInterval {
			out.EndsAt = reuseSelectedClock(values, prefix, "end_date", "end_time", "end_occurrence", e)
		} else {
			for _, field := range []string{"end_date", "end_time", "end_occurrence"} {
				if values.Get(prefix+"."+field) != "" {
					e.Add(prefix+"."+field, "O treino de origem não tem uma hora de fim. Os valores introduzidos foram mantidos.")
				}
			}
		}
	}
	if out.StartsAt != nil {
		boundary := time.Date(week.Year(), week.Month(), week.Day(), 0, 0, 0, 0, lisbonLocation())
		if out.StartsAt.Before(boundary) || !out.StartsAt.Before(boundary.AddDate(0, 0, 7)) {
			e.Add(prefix+".start_time", "O início deve ficar nesta semana.")
		}
		if out.EndsAt != nil && out.EndsAt.After(boundary.AddDate(0, 0, 7)) {
			e.Add(prefix+".end_time", "O fim deve ficar nesta semana ou no seu limite exato.")
		}
	}
	if len(e) == 0 && out.ValidateKind(string(item.Graph.Kind)) != nil {
		e.Add(prefix+".date", "Reveja o horário completo de destino.")
	}
	return out, e
}
func reuseClockText(t time.Time) string { return t.In(lisbonLocation()).Format("15:04:05.999999999") }
func initialReuseMapping(item reuseSourceItem, targetDay string) url.Values {
	p := reuseMappingPrefix(item.Key)
	v := url.Values{"mapping_order": {item.Key}, p + ".date": {targetDay}}
	if item.Graph.Schedule.StartsAt != nil {
		v.Set(p+".start_time", reuseClockText(*item.Graph.Schedule.StartsAt))
	}
	if item.Graph.Schedule.EndsAt != nil && targetDay != "" {
		start, _ := time.Parse("2006-01-02", item.Graph.Schedule.Date)
		end, _ := time.Parse("2006-01-02", item.Graph.Schedule.EndsAt.In(lisbonLocation()).Format("2006-01-02"))
		target, err := time.Parse("2006-01-02", targetDay)
		if err == nil {
			v.Set(p+".end_date", target.AddDate(0, 0, int(end.Sub(start)/(24*time.Hour))).Format("2006-01-02"))
		}
		v.Set(p+".end_time", reuseClockText(*item.Graph.Schedule.EndsAt))
	}
	return v
}
func reuseMappingKey(index int) string { return fmt.Sprintf("m_%d", index+1) }
