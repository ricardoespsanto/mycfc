package handlers

import (
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/google/uuid"
)

func focusedGraphHasValues(v url.Values, prefix string) bool {
	for k, values := range v {
		if strings.HasPrefix(k, prefix+".") {
			for _, x := range values {
				if x != "" {
					return true
				}
			}
		}
	}
	return false
}
func focusedGraphOptionalText(v url.Values, name string, max int, e validation.FieldErrors) *string {
	value := focusedText(v, name, 0, max, e)
	if value == "" {
		return nil
	}
	return &value
}
func focusedGraphCertainty(v url.Values, name string, present bool, e validation.FieldErrors) *dbgen.TrainingMeasureCertainty {
	c, err := waterMeasureCertainty(v.Get(name), present)
	if err != nil {
		focusedGraphError(e, name, "Indique a certeza da medida preenchida; deixe em branco quando não há medida.")
	}
	return c
}

// Empty order/default-choice scaffolding carries no instructions to discard.
func focusedGraphActivityHasContent(v url.Values, a focusedGraphActivity) bool {
	prefix := focusedActivityPrefix(a.Key) + "."
	for k, values := range v {
		if !strings.HasPrefix(k, prefix) || strings.HasSuffix(k, ".id") || strings.HasSuffix(k, "_order") || strings.HasSuffix(k, ".purpose") || k == prefix+"modality" {
			continue
		}
		for _, value := range values {
			if value != "" {
				return true
			}
		}
	}
	return false
}

func normalizeFocusedGraph(c FocusedWorkoutContext, d focusedGraphDraft) (focusedWorkoutGraph, validation.FieldErrors) {
	return normalizeFocusedGraphWithSource(c, d, nil)
}
func normalizeFocusedGraphWithSource(c FocusedWorkoutContext, d focusedGraphDraft, saved *focusedWorkoutGraph) (focusedWorkoutGraph, validation.FieldErrors) {
	e := focusedWorkoutErrors()
	v := d.Values
	p := focusedWorkoutGraph{Context: c, Kind: dbgen.TrainingEntryKind(v.Get("entry_kind")), Title: focusedText(v, "title", 2, 180, e), Description: focusedText(v, "description", 0, 4000, e), Activities: []focusedGraphActivityInput{}, References: focusedGraphProfileFacts{Profiles: []dbgen.WaterIntensityProfile{}, Zones: []dbgen.WaterIntensityZone{}}}
	switch p.Kind {
	case "TRAINING", "COMPETITION", "REST", "LOGISTICS":
	default:
		focusedGraphError(e, "entry_kind", "Escolha um tipo disponível.")
	}
	p.Schedule = normalizeFocusedGraphSchedule(c, v, p.Kind, e)
	for _, a := range d.Activities {
		meaningful := focusedGraphActivityHasContent(v, a)
		if p.Kind == "REST" || p.Kind == "LOGISTICS" {
			if !meaningful {
				continue
			}
			focusedGraphError(e, "entry_kind", "Este tipo não contém atividades de esforço. Remova-as explicitamente ou volte ao tipo anterior; os valores foram mantidos.")
		} else if !meaningful && v.Get(focusedActivityPrefix(a.Key)+".modality") == "" {
			continue
		}
		ap := focusedActivityPrefix(a.Key)
		input := focusedGraphActivityInput{Key: a.Key, Modality: dbgen.TrainingSegmentModality(v.Get(ap + ".modality")), Title: focusedText(v, ap+".title", 0, 120, e), Location: focusedText(v, ap+".location", 0, 180, e), Equipment: focusedText(v, ap+".equipment_notes", 0, 1000, e), DurationMinutes: focusedOptionalInt(v, ap+".duration_minutes", 1, 1440, e), StartOffsetMinutes: focusedOptionalInt(v, ap+".start_offset_minutes", 0, 1440, e), TransitionMinutes: focusedOptionalInt(v, ap+".transition_minutes", 1, 1440, e), Phases: []focusedGraphPhaseInput{}}
		if !validTrainingSegmentModality(input.Modality) {
			focusedGraphError(e, ap+".modality", "Escolha a atividade.")
		}
		for _, phase := range a.Phases {
			pp := focusedGraphPhasePrefix(a.Key, phase.Key)
			mode := v.Get(pp + ".detail_mode")
			hasWater := len(phase.Nodes) > 0 || focusedGraphHasValues(v, pp+".water")
			hasGym := len(phase.Exercises) > 0 || focusedGraphHasValues(v, pp+".gym")
			retained := false
			if saved != nil {
				for _, oldA := range saved.Activities {
					if oldA.Key == a.Key {
						for _, oldB := range oldA.Phases {
							if oldB.Key == phase.Key {
								retained = true
							}
						}
					}
				}
			}
			if v.Get(pp+".title") == "" && v.Get(pp+".instructions") == "" && mode == "" && !hasWater && !hasGym && !retained {
				continue
			}
			minimumInstructions := 2
			block := focusedGraphPhaseInput{Key: phase.Key, Purpose: dbgen.TrainingBlockPurpose(v.Get(pp + ".purpose")), Title: focusedText(v, pp+".title", 0, 120, e), Instructions: focusedText(v, pp+".instructions", minimumInstructions, 4000, e)}
			if !validTrainingBlockPurpose(block.Purpose) {
				focusedGraphError(e, pp+".purpose", "Escolha a fase.")
			}
			if block.Purpose == "CUSTOM" && block.Title == "" {
				focusedGraphError(e, pp+".title", "Dê um título à fase personalizada.")
			}
			switch mode {
			case "":
				if hasWater || hasGym {
					focusedGraphError(e, pp+".detail_mode", "Há detalhes preenchidos. Escolha o detalhe correspondente ou remova-os explicitamente.")
				}
			case "WATER":
				if input.Modality != "WATER" || hasGym {
					focusedGraphError(e, pp+".detail_mode", "Os detalhes de água pertencem a uma atividade de água; reveja os restantes valores preenchidos.")
				}
				block.Water = normalizeFocusedGraphWater(v, a.Key, phase, e)
			case "GYM":
				if input.Modality != "GYM" || hasWater {
					focusedGraphError(e, pp+".detail_mode", "Os detalhes de ginásio pertencem a uma atividade de ginásio; reveja os restantes valores preenchidos.")
				}
				block.Gym = normalizeFocusedGraphGym(v, a.Key, phase, e)
			default:
				focusedGraphError(e, pp+".detail_mode", "Escolha um detalhe disponível ou apenas instruções escritas.")
			}
			input.Phases = append(input.Phases, block)
		}
		p.Activities = append(p.Activities, input)
	}
	if saved == nil && (p.Kind == "TRAINING" || p.Kind == "COMPETITION") && p.Description == "" {
		meaningful := false
		for _, a := range p.Activities {
			meaningful = meaningful || len(a.Phases) > 0
		}
		if !meaningful {
			focusedGraphError(e, "description", "Indique instruções gerais ou uma fase do treino.")
		}
	}
	if _, err := focusedGraphCheckedTotals(p); len(e) == 0 && err != nil {
		focusedGraphError(e, "form", "Os totais excedem os limites técnicos. Reduza as repetições ou o conteúdo antes de rever.")
	}
	return p, e
}
func normalizeFocusedGraphSchedule(c FocusedWorkoutContext, v url.Values, kind dbgen.TrainingEntryKind, e validation.FieldErrors) TrainingSessionSchedule {
	s := TrainingSessionSchedule{Date: v.Get("schedule.date"), Precision: TrainingSchedulePrecision(v.Get("schedule.mode"))}
	if s.Date != c.Day || !validWorkspaceDate(s.Date) {
		focusedGraphError(e, "schedule.date", "Mantenha o dia selecionado nesta semana.")
	}
	reject := func(keys ...string) {
		for _, k := range keys {
			if v.Get(k) != "" {
				focusedGraphError(e, k, "Este valor não corresponde ao horário escolhido. Reveja-o sem perder os dados introduzidos.")
			}
		}
	}
	// CREATE: actual supplied endpoint fields determine an interval; mode is
	// retained raw but cannot manufacture an end or suppress explicit values.
	if s.Precision == TrainingScheduleStartOnly && (v.Get("schedule.end_date") != "" || v.Get("schedule.end_time") != "" || v.Get("schedule.end_occurrence") != "") {
		s.Precision = TrainingScheduleInterval
	}
	timed := s.Precision == TrainingScheduleInterval || s.Precision == TrainingScheduleStartOnly
	if (kind == "TRAINING" || kind == "COMPETITION") && !timed {
		focusedGraphError(e, "schedule.mode", "Este compromisso requer uma hora de início real.")
	}
	switch s.Precision {
	case TrainingScheduleDay:
		reject("schedule.start_time", "schedule.end_date", "schedule.end_time", "schedule.start_occurrence", "schedule.end_occurrence", "schedule.band")
	case TrainingScheduleBand:
		reject("schedule.start_time", "schedule.end_date", "schedule.end_time", "schedule.start_occurrence", "schedule.end_occurrence")
		band := TrainingDayBand(v.Get("schedule.band"))
		if band != TrainingBandMorning && band != TrainingBandAfternoon {
			focusedGraphError(e, "schedule.band", "Escolha manhã ou tarde sem inferir uma hora.")
		} else {
			s.Band = &band
		}
	case TrainingScheduleStartOnly, TrainingScheduleInterval:
		reject("schedule.band")
		s.StartsAt = focusedSelectedClock(v, "schedule.date", "schedule.start_time", "schedule.start_occurrence", e)
		if s.Precision == TrainingScheduleInterval {
			s.EndsAt = focusedSelectedClock(v, "schedule.end_date", "schedule.end_time", "schedule.end_occurrence", e)
		} else {
			reject("schedule.end_date", "schedule.end_time", "schedule.end_occurrence")
		}
		if s.EndsAt != nil {
			if s.StartsAt != nil && !s.EndsAt.After(*s.StartsAt) {
				focusedGraphError(e, "schedule.end_time", "O fim deve ser posterior ao início.")
			}
			day := c.WeekStart.AddDate(0, 0, 7)
			limit := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, lisbonLocation())
			if s.EndsAt.After(limit) {
				focusedGraphError(e, "schedule.end_date", "O fim deve permanecer nesta semana.")
			}
		}
	default:
		focusedGraphError(e, "schedule.mode", "Escolha o horário sem inventar horas.")
	}
	return s
}
func normalizeFocusedGraphWater(v url.Values, a string, p focusedGraphPhase, e validation.FieldErrors) *focusedGraphWaterInput {
	pp := focusedGraphPhasePrefix(a, p.Key)
	wp := pp + ".water"
	w := &focusedGraphWaterInput{Method: dbgen.WaterWorkMethod(v.Get(wp + ".method")), TargetDistance: focusedOptionalInt(v, wp+".target_distance_metres", 1, 200000, e), Nodes: []focusedGraphWaterNodeInput{}}
	if !validWaterWorkMethod(w.Method) {
		focusedGraphError(e, wp+".method", "Escolha o método de água.")
	}
	if raw := v.Get(wp + ".profile_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil || id.String() != raw {
			focusedGraphError(e, wp+".profile_id", "Escolha um perfil disponível.")
		} else {
			w.ProfileID = &id
		}
	}
	w.TargetCertainty = focusedGraphCertainty(v, wp+".target_distance_certainty", w.TargetDistance != nil, e)
	byKey := map[string]focusedGraphNode{}
	for _, n := range p.Nodes {
		byKey[n.Key] = n
	}
	for _, n := range p.Nodes {
		np := focusedNodePrefix(a, p.Key, n.Key)
		node := focusedGraphWaterNodeInput{Key: n.Key, ParentKey: n.Parent, Kind: dbgen.WaterStepKind(v.Get(np + ".kind")), Name: focusedText(v, np+".name", 2, 180, e), Repeats: focusedOptionalInt(v, np+".repeats", 1, 100, e), Duration: focusedOptionalInt(v, np+".duration_seconds", 1, 86400, e), Distance: focusedOptionalInt(v, np+".distance_metres", 1, 200000, e), Recovery: focusedOptionalInt(v, np+".recovery_seconds", 1, 86400, e), Cadence: focusedOptionalInt(v, np+".cadence_spm", 1, 300, e), Intensity: focusedGraphOptionalText(v, np+".intensity_code", 20, e), DrillFocus: focusedGraphOptionalText(v, np+".drill_focus", 180, e), DrillFormat: focusedGraphOptionalText(v, np+".drill_format", 180, e), RoleNotes: focusedGraphOptionalText(v, np+".role_notes", 500, e), Instructions: focusedText(v, np+".instructions", 0, 1000, e)}
		node.DurationCertainty = focusedGraphCertainty(v, np+".duration_certainty", node.Duration != nil, e)
		node.DistanceCertainty = focusedGraphCertainty(v, np+".distance_certainty", node.Distance != nil, e)
		switch node.Kind {
		case "REPEAT_GROUP":
			if node.Repeats == nil {
				focusedGraphError(e, np+".repeats", "Indique o número de repetições do grupo.")
			}
			if node.Duration != nil || node.Distance != nil {
				focusedGraphError(e, np+".kind", "As medidas do grupo pertencem aos esforços que contém.")
			}
			children := 0
			for _, child := range p.Nodes {
				if child.Parent == n.Key {
					children++
				}
			}
			if children == 0 {
				focusedGraphError(e, np+".kind", "Acrescente um esforço ao grupo ou use instruções escritas.")
			}
		case "EFFORT":
			if node.Repeats != nil {
				focusedGraphError(e, np+".repeats", "Use um grupo para repetir esforços.")
			}
			if node.Duration == nil && node.Distance == nil && len([]rune(node.Instructions)) < 2 {
				focusedGraphError(e, np+".instructions", "Indique uma medida ou instruções para este esforço.")
			}
		default:
			focusedGraphError(e, np+".kind", "Escolha esforço ou grupo de repetições.")
		}
		if n.Parent != "" {
			if _, ok := byKey[n.Parent]; !ok || v.Get(focusedNodePrefix(a, p.Key, n.Parent)+".kind") != "REPEAT_GROUP" {
				focusedGraphError(e, np+".kind", "O elemento anterior deve ser um grupo desta fase.")
			}
		}
		w.Nodes = append(w.Nodes, node)
	}
	return w
}
func normalizeFocusedGraphGym(v url.Values, a string, p focusedGraphPhase, e validation.FieldErrors) *focusedGraphGymInput {
	gp := focusedGraphPhasePrefix(a, p.Key) + ".gym"
	g := &focusedGraphGymInput{Structure: dbgen.GymBlockStructure(v.Get(gp + ".structure")), Objective: dbgen.TrainingObjective(v.Get(gp + ".objective")), Exercises: []focusedGraphExerciseInput{}}
	if !validGymBlockStructure(g.Structure) {
		focusedGraphError(e, gp+".structure", "Escolha a organização do bloco.")
	}
	if !validTrainingObjective(g.Objective) {
		focusedGraphError(e, gp+".objective", "Escolha o objetivo.")
	}
	rounds := focusedOptionalInt(v, gp+".rounds", 1, 100, e)
	if rounds == nil {
		focusedGraphError(e, gp+".rounds", "Indique as rondas.")
	} else {
		g.Rounds = *rounds
	}
	g.RoundRecovery = focusedOptionalInt(v, gp+".round_recovery_seconds", 1, 86400, e)
	for _, key := range p.Exercises {
		ep := focusedExercisePrefix(a, p.Key, key)
		x := focusedGraphExerciseInput{Key: key, Name: focusedText(v, ep+".name", 2, 180, e), Sets: focusedOptionalInt(v, ep+".sets", 1, 100, e), Repetitions: focusedOptionalInt(v, ep+".repetitions", 1, 10000, e), Duration: focusedOptionalInt(v, ep+".duration_seconds", 1, 86400, e), Distance: focusedOptionalInt(v, ep+".distance_metres", 1, 100000, e), Recovery: focusedOptionalInt(v, ep+".recovery_seconds", 1, 86400, e), Notes: focusedText(v, ep+".notes", 0, 1000, e), Tempo: focusedGraphOptionalText(v, ep+".tempo", 30, e)}
		if x.Repetitions == nil && x.Duration == nil && x.Distance == nil {
			focusedGraphError(e, ep+".repetitions", "Indique repetições, tempo ou distância.")
		}
		if raw := v.Get(ep + ".execution_intent"); raw != "" {
			intent := dbgen.GymExecutionIntent(raw)
			if !validGymExecutionIntent(intent) {
				focusedGraphError(e, ep+".execution_intent", "Escolha a execução.")
			} else {
				x.Intent = &intent
			}
		}
		kind := dbgen.GymResistanceKind(v.Get(ep + ".resistance_kind"))
		value, text := v.Get(ep+".resistance_value"), v.Get(ep+".resistance_text")
		if kind == "" {
			if value != "" || text != "" {
				focusedGraphError(e, ep+".resistance_kind", "Escolha como indicar a resistência.")
			}
		} else if !validGymResistanceKind(kind) {
			focusedGraphError(e, ep+".resistance_kind", "Escolha uma resistência disponível.")
		} else {
			x.ResistanceKind = &kind
			switch kind {
			case "KILOGRAMS", "PERCENT_1RM", "RPE", "RIR":
				f, err := strconv.ParseFloat(value, 64)
				if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || !validGymResistanceValue(kind, f) {
					focusedGraphError(e, ep+".resistance_value", "Indique uma carga dentro do intervalo permitido.")
				} else {
					x.ResistanceValue = &f
				}
				if text != "" {
					focusedGraphError(e, ep+".resistance_text", "A resistência escolhida usa um valor numérico.")
				}
			case "BODY_WEIGHT":
				if value != "" || text != "" {
					focusedGraphError(e, ep+".resistance_value", "Peso corporal não usa outra carga.")
				}
			case "BAND", "COACH_INSTRUCTION":
				x.ResistanceText = focusedGraphOptionalText(v, ep+".resistance_text", 180, e)
				if x.ResistanceText == nil {
					focusedGraphError(e, ep+".resistance_text", "Indique a orientação do treinador.")
				}
				if value != "" {
					focusedGraphError(e, ep+".resistance_value", "Esta resistência usa texto.")
				}
			}
		}
		g.Exercises = append(g.Exercises, x)
	}
	if len(g.Exercises) == 0 {
		focusedGraphError(e, gp+".structure", "Acrescente um exercício ou use instruções escritas.")
	}
	return g
}
