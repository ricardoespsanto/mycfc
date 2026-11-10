package handlers

import (
	"encoding/json"
	"github.com/jackc/pgx/v5/pgtype"
	"sort"
	"strings"
	"time"

	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
)

func focusedClockOccurrences(date, clock string) []time.Time {
	parsed, err := time.Parse("2006-01-02 15:04", date+" "+clock)
	if err != nil || parsed.Format("2006-01-02 15:04") != date+" "+clock || parsed.Year() < 1 {
		return nil
	}
	loc := lisbonLocation()
	offsets := map[int]bool{}
	for h := -48; h <= 48; h++ {
		_, offset := parsed.Add(time.Duration(h) * time.Hour).In(loc).Zone()
		offsets[offset] = true
	}
	var result []time.Time
	for offset := range offsets {
		candidate := parsed.Add(-time.Duration(offset) * time.Second)
		if candidate.In(loc).Format("2006-01-02 15:04") == date+" "+clock {
			result = append(result, candidate.UTC())
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Before(result[j]) })
	return result
}
func focusedOccurrenceChoices(date, clock string) []pages.StructuredWorkoutChoice {
	candidates := focusedClockOccurrences(date, clock)
	if len(candidates) < 2 {
		return nil
	}
	choices := make([]pages.StructuredWorkoutChoice, 0, len(candidates))
	for _, t := range candidates {
		choices = append(choices, pages.StructuredWorkoutChoice{Value: t.Format(time.RFC3339Nano), Label: t.In(lisbonLocation()).Format("02/01/2006 15:04 MST -07:00")})
	}
	return choices
}
func focusedSelectedClock(v mapValues, dateKey, clockKey, occurrenceKey string, errs validation.FieldErrors) *time.Time {
	date, clock := v.Get(dateKey), v.Get(clockKey)
	candidates := focusedClockOccurrences(date, clock)
	if len(candidates) == 0 {
		errs.Add(focusedFieldKey(clockKey), "Indique uma hora existente nesta data em Portugal continental.")
		return nil
	}
	raw := v.Get(occurrenceKey)
	if len(candidates) > 1 && raw == "" {
		errs.Add(focusedFieldKey(occurrenceKey), "Esta hora ocorre duas vezes. Escolha a ocorrência pretendida.")
		return nil
	}
	for _, candidate := range candidates {
		if raw == "" || raw == candidate.Format(time.RFC3339Nano) {
			out := candidate
			return &out
		}
	}
	errs.Add(focusedFieldKey(occurrenceKey), "Reveja a ocorrência desta hora.")
	return nil
}

type mapValues interface{ Get(string) string }

func normalizeFocusedWater(c FocusedWorkoutContext, d focusedWorkoutDraft) (focusedWaterProposal, validation.FieldErrors) {
	e := focusedWorkoutErrors()
	v := d.Values
	p := focusedWaterProposal{Context: c, Phases: []focusedWaterPhase{}}
	p.Title = focusedText(v, "title", 2, 180, e)
	p.Description = focusedText(v, "description", 0, 4000, e)
	if v.Get("entry_kind") != "TRAINING" {
		e.Add("entry_kind", "Este formulário cria apenas um treino de água.")
	}
	if v.Get("activities[a_1].modality") != "WATER" {
		e.Add("activities.a_1.modality", "Escolha Água neste formulário.")
	}
	if v.Get("schedule.date") != c.Day {
		e.Add("schedule.date", "Mantenha a data selecionada na semana.")
	}
	mode := v.Get("schedule.mode")
	if mode != "START_ONLY" && mode != "INTERVAL" {
		e.Add("schedule.mode", "Indique início, com ou sem hora de fim.")
	}
	precision := TrainingScheduleStartOnly
	if v.Get("schedule.end_date") != "" || v.Get("schedule.end_time") != "" || v.Get("schedule.end_occurrence") != "" {
		precision = TrainingScheduleInterval
	}
	p.Schedule = TrainingSessionSchedule{Date: c.Day, Precision: precision}
	p.Schedule.StartsAt = focusedSelectedClock(v, "schedule.date", "schedule.start_time", "schedule.start_occurrence", e)
	if precision == TrainingScheduleInterval {
		if !validWorkspaceDate(v.Get("schedule.end_date")) {
			e.Add("schedule.end_date", "Indique a data de fim.")
		} else {
			p.Schedule.EndsAt = focusedSelectedClock(v, "schedule.end_date", "schedule.end_time", "schedule.end_occurrence", e)
		}
	}
	if v.Get("schedule.band") != "" {
		e.Add("schedule.band", "Este treino precisa de uma hora de início, sem período aproximado.")
	}
	weekEndDay := c.WeekStart.AddDate(0, 0, 7)
	weekEnd := time.Date(weekEndDay.Year(), weekEndDay.Month(), weekEndDay.Day(), 0, 0, 0, 0, lisbonLocation())
	if p.Schedule.StartsAt != nil {
		date := p.Schedule.StartsAt.In(lisbonLocation()).Format("2006-01-02")
		if date != c.Day {
			e.Add("schedule.start_time", "Reveja a data e hora de início.")
		}
	}
	if p.Schedule.EndsAt != nil {
		if p.Schedule.StartsAt != nil && !p.Schedule.EndsAt.After(*p.Schedule.StartsAt) {
			e.Add("schedule.end_time", "O fim deve ser posterior ao início.")
		}
		if p.Schedule.EndsAt.After(weekEnd) {
			e.Add("schedule.end_date", "O fim deve permanecer nesta semana.")
		}
	}
	a := "activities[a_1]."
	p.Activity = focusedWaterActivity{Title: focusedText(v, a+"title", 0, 120, e), Location: focusedText(v, a+"location", 0, 180, e), Equipment: focusedText(v, a+"equipment_notes", 0, 1000, e), DurationMinutes: focusedOptionalInt(v, a+"duration_minutes", 1, 1440, e), StartOffsetMinutes: focusedOptionalInt(v, a+"start_offset_minutes", 0, 1440, e), TransitionMinutes: focusedOptionalInt(v, a+"transition_minutes", 1, 1440, e)}
	for _, key := range d.Keys {
		prefix := focusedPhasePrefix(key)
		title, instructions, detail := v.Get(prefix+".title"), v.Get(prefix+".instructions"), v.Get(prefix+".detail_mode")
		if title == "" && instructions == "" && detail == "" {
			continue
		}
		purpose := v.Get(prefix + ".purpose")
		switch purpose {
		case "WARM_UP", "MAIN", "COOL_DOWN", "CUSTOM":
		default:
			e.Add(focusedFieldKey(prefix+".purpose"), "Escolha uma fase disponível.")
		}
		if detail != "" {
			e.Add(focusedFieldKey(prefix+".detail_mode"), "Use instruções escritas neste formulário. Os detalhes estruturados continuam nas opções avançadas.")
		}
		title = focusedText(v, prefix+".title", 0, 120, e)
		instructions = focusedText(v, prefix+".instructions", 2, 4000, e)
		if purpose == "CUSTOM" && title == "" {
			e.Add(focusedFieldKey(prefix+".title"), "Dê um título à fase personalizada.")
		}
		p.Phases = append(p.Phases, focusedWaterPhase{Key: key, Purpose: purpose, Title: title, Instructions: instructions})
	}
	if len(p.Phases) == 0 && strings.TrimSpace(p.Description) == "" {
		e.Add("description", "Escreva as instruções gerais ou preencha pelo menos uma fase.")
	}
	if len(e) == 0 {
		if err := p.Schedule.ValidateKind("TRAINING"); err != nil {
			e.Add("schedule.start_time", "Reveja o horário indicado.")
		}
	}
	return p, e
}
func allocateFocusedWaterIDs(p *focusedWaterProposal) {
	p.SessionID = uuid.New()
	p.Activity.ID = uuid.New()
	for i := range p.Phases {
		p.Phases[i].ID = uuid.New()
	}
}
func projectFocusedWater(p focusedWaterProposal) pages.StructuredTrainingSession {
	day, _ := time.Parse("2006-01-02", p.Schedule.Date)
	base := structuredTrainingRow{groupID: &p.Context.GroupID, groupName: p.Context.GroupName, planID: &p.Context.PlanID, planTitle: p.Context.PlanTitle, weekStart: p.Context.WeekStart, sessionID: &p.SessionID, sessionTitle: p.Title, sessionDescription: p.Description, entryKind: "TRAINING", sessionStatus: "ACTIVE", schedulePrecision: string(p.Schedule.Precision), scheduledOn: pgtype.Date{Time: day, Valid: true}, segmentID: &p.Activity.ID, segmentPosition: 1, modality: "WATER", segmentTitle: p.Activity.Title, segmentLocation: p.Activity.Location, equipmentNotes: p.Activity.Equipment}
	if p.Schedule.StartsAt != nil {
		base.startsAt = *p.Schedule.StartsAt
		base.startsKnown = true
	}
	if p.Schedule.EndsAt != nil {
		base.endsAt = *p.Schedule.EndsAt
		base.endsKnown = true
	}
	if p.Activity.DurationMinutes != nil {
		base.duration = int(*p.Activity.DurationMinutes)
	}
	if p.Activity.StartOffsetMinutes != nil {
		base.startOffset = int(*p.Activity.StartOffsetMinutes)
		base.plannedStartSet = true
	}
	if p.Activity.TransitionMinutes != nil {
		base.transition = int(*p.Activity.TransitionMinutes)
	}
	rows := []structuredTrainingRow{}
	for i, phase := range p.Phases {
		row := base
		row.blockID = &p.Phases[i].ID
		row.blockPosition = i + 1
		row.blockPurpose = phase.Purpose
		row.blockTitle = phase.Title
		row.instructions = phase.Instructions
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		rows = append(rows, base)
	}
	audiences := assembleStructuredTraining(rows, lisbonLocation())
	if len(audiences) != 1 || len(audiences[0].Weeks) != 1 || len(audiences[0].Weeks[0].Sessions) != 1 {
		return pages.StructuredTrainingSession{}
	}
	return audiences[0].Weeks[0].Sessions[0]
}
func focusedWaterArtifact(p focusedWaterProposal) ([]byte, error) {
	projected := projectFocusedWater(p)
	encoded, err := json.Marshal(projected)
	if err != nil {
		return nil, err
	}
	session := map[string]json.RawMessage{}
	if err = json.Unmarshal(encoded, &session); err != nil {
		return nil, err
	}
	delete(session, "EntryKind")
	session["entry_kind"] = json.RawMessage(`"TRAINING"`)
	session["schedule"], err = json.Marshal(p.Schedule)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Version   int                        `json:"schema_version"`
		PlanID    string                     `json:"plan_id"`
		PlanTitle string                     `json:"plan_title"`
		GroupName string                     `json:"group_name"`
		Session   map[string]json.RawMessage `json:"session"`
	}{2, p.Context.PlanID.String(), p.Context.PlanTitle, p.Context.GroupName, session})
}
