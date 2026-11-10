package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func reuseExample(t *testing.T) reuseReviewPayload {
	t.Helper()
	source := focusedEditFixture(t)
	week := source.Context.WeekStart.AddDate(0, 0, 7)
	target := source.Context
	target.PlanID, target.GroupID, target.SeasonID = uuid.New(), uuid.New(), uuid.New()
	target.Day = week.AddDate(0, 0, 2).Format("2006-01-02")
	target.WeekStart = week
	item := reuseSourceItem{Key: "m_1", Graph: source.Graph, Raw: &source.Raw, Origins: reuseNativeOrigins(source.Graph)}
	values := initialReuseMapping(item, target.Day)
	values.Set(reuseMappingPrefix(item.Key)+".start_time", "16:00")
	schedule, e := normalizeReuseSchedule(item, values, week)
	if len(e) > 0 {
		t.Fatal(e)
	}
	destination, err := independentReuseGraph(item, target, schedule, nil)
	if err != nil {
		t.Fatal(err)
	}
	selector := reuseTargetSelector{GroupID: target.GroupID, SeasonID: target.SeasonID, WeekStart: week.Format("2006-01-02"), Title: "Semana de destino"}
	config := dbgen.GetReuseTargetContextRow{GroupID: selector.GroupID, SeasonID: selector.SeasonID, WeekStart: pgtype.Date{Time: week, Valid: true}}
	return reuseReviewPayload{ActorID: target.ActorID, Incarnation: target.DatabaseIncarnation, Epoch: target.StateEpoch, Source: reuseSourceMaterial{Selector: reuseSourceSelector{Kind: "SESSION", PlanID: source.Context.PlanID, SessionID: source.Graph.SessionID}, Items: []reuseSourceItem{item}}, Target: reuseTargetMaterial{Selector: selector, Configuration: config}, References: source.Graph.References, Proposal: reuseProposal{TargetPlanID: target.PlanID, NewWeek: true, Destinations: []reuseDestination{destination}, Events: []reuseEvent{{ID: uuid.New(), SourceKind: "SESSION", SourceID: source.Graph.SessionID, SourceUpdatedAt: source.Raw.Session.UpdatedAt.Time, DestinationKind: "SESSION", DestinationID: destination.Graph.SessionID}}}, Values: values}
}

func TestReuseReferenceClosureRequiresExactPinsAndValidUniqueZones(t *testing.T) {
	profile := uuid.New()
	other := uuid.New()
	valid := focusedGraphProfileFacts{Profiles: []dbgen.WaterIntensityProfile{{ID: profile, Revision: 1}}, Zones: []dbgen.WaterIntensityZone{{ID: uuid.New(), ProfileID: profile, Position: 1, Code: "R1"}}}
	for _, name := range []string{"valid", "missing", "extra", "substituted", "duplicate-profile", "foreign-zone", "duplicate-zone", "duplicate-code", "duplicate-position"} {
		t.Run(name, func(t *testing.T) {
			facts := focusedGraphProfileFacts{Profiles: append([]dbgen.WaterIntensityProfile(nil), valid.Profiles...), Zones: append([]dbgen.WaterIntensityZone(nil), valid.Zones...)}
			switch name {
			case "missing":
				facts.Profiles = nil
			case "extra":
				facts.Profiles = append(facts.Profiles, dbgen.WaterIntensityProfile{ID: other, Revision: 1})
			case "substituted":
				facts.Profiles[0].ID = other
			case "duplicate-profile":
				facts.Profiles = append(facts.Profiles, facts.Profiles[0])
			case "foreign-zone":
				facts.Zones[0].ProfileID = other
			case "duplicate-zone":
				facts.Zones = append(facts.Zones, facts.Zones[0])
			case "duplicate-code":
				z := facts.Zones[0]
				z.ID, z.Position = uuid.New(), 2
				facts.Zones = append(facts.Zones, z)
			case "duplicate-position":
				z := facts.Zones[0]
				z.ID, z.Code = uuid.New(), "R2"
				facts.Zones = append(facts.Zones, z)
			}
			if err := validateReuseReferenceClosure([]uuid.UUID{profile}, facts); (err == nil) != (name == "valid") {
				t.Fatal("accepted incomplete or substituted guidance", err)
			}
		})
	}
	if err := validateReuseReferenceClosure(nil, focusedGraphProfileFacts{}); err != nil {
		t.Fatal("legitimate no-pin source", err)
	}
	p := reuseExample(t)
	p.Source.Items[0].Graph.Activities[0].Phases[0].Water.ProfileID = &profile
	if _, _, err := focusedWorkoutReviews.signReuse(p, time.Now()); err == nil {
		t.Fatal("signed graph with missing source references")
	}
}
func TestReuseIndependentGraphPreservesPrescriptionAndStableReviewIDs(t *testing.T) {
	p := reuseExample(t)
	source, dest := p.Source.Items[0], p.Proposal.Destinations[0]
	if source.Graph.SessionID == dest.Graph.SessionID {
		t.Fatal("reused source session ID")
	}
	old, _ := focusedEditEntities(source.Graph)
	next, _ := focusedEditEntities(dest.Graph)
	if len(old) != len(next) {
		t.Fatal("content omitted")
	}
	for id := range old {
		if _, exists := next[id]; exists {
			t.Fatal("source child identity cloned")
		}
	}
	if !dest.NativeNullDate && source.Raw.Session.ScheduledOn.Valid == false {
		t.Fatal("native NULL fact lost")
	}
	again, err := independentReuseGraph(source, dest.Graph.Context, dest.Graph.Schedule, &dest)
	if err != nil || !focusedGraphJSONEqual(again, dest) {
		t.Fatal("editback reallocated destination", err)
	}
	a, b := source.Graph.Activities[0].Phases[0].Water, dest.Graph.Activities[0].Phases[0].Water
	if a.Method != b.Method || !focusedGraphJSONEqual(a.Nodes[0].Repeats, b.Nodes[0].Repeats) || !focusedGraphJSONEqual(a.Nodes[0].Recovery, b.Nodes[0].Recovery) || a.Nodes[1].ParentKey != b.Nodes[1].ParentKey {
		t.Fatal("repeat/recovery/tree facts changed")
	}
	b.Nodes[1].Instructions = "Independente"
	if source.Graph.Activities[0].Phases[0].Water.Nodes[1].Instructions == "Independente" {
		t.Fatal("graph alias changed source")
	}
}
func TestReuseFourScheduleMappingsDoNotInventClocksAndRequireActualDSTChoice(t *testing.T) {
	item := reuseExample(t).Source.Items[0]
	week := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	key := reuseMappingPrefix(item.Key)
	for _, precision := range []TrainingSchedulePrecision{TrainingScheduleDay, TrainingScheduleBand, TrainingScheduleStartOnly, TrainingScheduleInterval} {
		item.Graph.Kind = "LOGISTICS"
		item.Graph.Schedule = TrainingSessionSchedule{Precision: precision, Date: "2026-10-20"}
		if precision == TrainingScheduleBand {
			item.Graph.Schedule.Band = focusedPointer(TrainingBandMorning)
		}
		values := url.Values{key + ".date": {"2026-10-25"}}
		if precision == TrainingScheduleStartOnly || precision == TrainingScheduleInterval {
			values.Set(key+".start_time", "01:30")
			if precision == TrainingScheduleInterval {
				values.Set(key+".end_date", "2026-10-25")
				values.Set(key+".end_time", "03:00")
			}
			_, e := normalizeReuseSchedule(item, values, week)
			if e[key+".start_occurrence"] == "" {
				t.Fatal("fold silently resolved")
			}
			values.Set(key+".start_occurrence", focusedClockOccurrences("2026-10-25", "01:30")[1].Format(time.RFC3339Nano))
		}
		schedule, e := normalizeReuseSchedule(item, values, week)
		if len(e) > 0 || schedule.Precision != precision {
			t.Fatal(precision, e)
		}
		if precision == TrainingScheduleDay || precision == TrainingScheduleBand {
			if schedule.StartsAt != nil || schedule.EndsAt != nil {
				t.Fatal("invented midnight")
			}
			values.Set(key+".start_time", "12:00")
			if _, e = normalizeReuseSchedule(item, values, week); len(e) == 0 {
				t.Fatal("contradictory clock dropped")
			}
		}
	}
	item.Graph.Schedule = TrainingSessionSchedule{Precision: TrainingScheduleStartOnly}
	values := url.Values{key + ".date": {"2026-03-29"}, key + ".start_time": {"01:30"}}
	if _, e := normalizeReuseSchedule(item, values, time.Date(2026, 3, 23, 0, 0, 0, 0, time.UTC)); e[key+".start_time"] == "" {
		t.Fatal("DST gap accepted")
	}
	exact := reuseClockOccurrences("2026-11-10", "16:00:00.123456")
	if len(exact) != 1 || exact[0].Nanosecond() != 123456000 {
		t.Fatal("source fractions rounded")
	}
}
func TestReuseReviewBindsSourceTargetRawAndOriginalDestinationIDs(t *testing.T) {
	p := reuseExample(t)
	now := time.Now()
	raw, token, err := focusedWorkoutReviews.signReuse(p, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = focusedWorkoutReviews.verifyReuse(raw, token, p.ActorID, p.Source.Selector, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err = focusedWorkoutReviews.verifyReuse(raw, token, uuid.New(), p.Source.Selector, now); err == nil {
		t.Fatal("cross actor")
	}
	substituted := p.Source.Selector
	substituted.SessionID = uuid.New()
	if _, _, err = focusedWorkoutReviews.verifyReuse(raw, token, p.ActorID, substituted, now); err == nil {
		t.Fatal("same-count substituted source")
	}
	if _, _, err = focusedWorkoutReviews.verifyReuse(raw+" ", token, p.ActorID, p.Source.Selector, now); err == nil {
		t.Fatal("raw bytes not bound")
	}
	if _, _, err = focusedWorkoutReviews.verifyReuse(raw, token, p.ActorID, p.Source.Selector, now.Add(focusedWorkoutReviewTTL)); err == nil {
		t.Fatal("expiry")
	}
	restarted := focusedWorkoutReviews
	restarted.key[0] ^= 1
	if _, _, err = restarted.verifyReuse(raw, token, p.ActorID, p.Source.Selector, now); err == nil {
		t.Fatal("restart")
	}
	duplicate := p
	duplicate.Proposal.Destinations = append(duplicate.Proposal.Destinations, duplicate.Proposal.Destinations[0])
	if _, _, err = focusedWorkoutReviews.signReuse(duplicate, now); err == nil {
		t.Fatal("duplicate destination")
	}
	p.Values.Set("prior_proposal", raw)
	if _, _, err = focusedWorkoutReviews.signReuse(p, now); err == nil {
		t.Fatal("recursive previous proof")
	}
}
func TestReuseStrictMappingAndActualEncodedTransportBounds(t *testing.T) {
	p := reuseExample(t)
	values, err := parseReuseValues(p.Values, []string{"m_1"})
	if err != nil {
		t.Fatal(err)
	}
	values.Add("mappings[m_1].date", "2026-11-11")
	if _, err = parseReuseValues(values, []string{"m_1"}); err == nil {
		t.Fatal("duplicate scalar")
	}
	p = reuseExample(t)
	p.Values.Set("week_description", strings.Repeat("<", 160000))
	encoded, _ := json.Marshal(p)
	if len(encoded) > focusedWorkoutMaxRequestBytes {
		t.Fatal("fixture should fit internal JSON while exceeding actual encoded transport")
	}
	if _, _, err = focusedWorkoutReviews.signReuse(p, time.Now()); !errors.Is(err, errFocusedWorkoutLimit) {
		t.Fatal("canonical/encoded oversize accepted", err)
	}
}
func TestReuseRoutineRepresentedV1DurationAndFragmentWithoutAppointment(t *testing.T) {
	row := dbgen.GetReuseRoutineRow{ID: uuid.New(), Name: "Força A", Kind: "SESSION", Snapshot: []byte(`{"title":"Rotina exata","description":"Texto","entry_kind":"TRAINING","duration_seconds":3600,"segments":[]}`)}
	item, err := decodeReuseRoutine(row)
	if err != nil || item.DurationSeconds == nil || *item.DurationSeconds != 3600 || item.Graph.Schedule.StartsAt != nil {
		t.Fatal("legacy routine fabricated source calendar", err)
	}
	values := url.Values{"mappings[m_1].date": {"2026-11-10"}, "mappings[m_1].start_time": {"16:00"}}
	schedule, e := normalizeReuseSchedule(item, values, time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC))
	if len(e) > 0 || schedule.EndsAt == nil || schedule.EndsAt.Sub(*schedule.StartsAt) != time.Hour {
		t.Fatal("represented duration not resolved", e)
	}
	row.Kind = "BLOCK"
	row.Modality = focusedPointer(dbgen.TrainingSegmentModalityGYM)
	row.Snapshot = []byte(`{"purpose":"MAIN","title":"Força","instructions":"Instruções configuradas."}`)
	block, err := decodeReuseRoutine(row)
	if err != nil || block.FragmentKind != "BLOCK" || block.Graph.Schedule.Date != "" || block.Graph.Schedule.StartsAt != nil || len(block.Graph.Activities[0].Phases) != 1 {
		t.Fatal("block source invented appointment", err)
	}
}

func TestReuseCanonicalEmptyGymFragmentKeepsStoredOwnInstructions(t *testing.T) {
	row := dbgen.GetReuseRoutineRow{ID: uuid.New(), Name: "Rotina configurada sem exercícios ainda", Kind: "SEGMENT", Snapshot: []byte(`{"modality":"GYM","title":"Mobilidade","blocks":[{"purpose":"MAIN","title":"Estrutura guardada","instructions":"Texto guardado que não pode desaparecer.","gym":{"structure":"CIRCUIT","objective":"MOBILITY","rounds":1,"exercises":[]}}]}`)}
	item, err := decodeReuseRoutine(row)
	if err != nil {
		t.Fatal(err)
	}
	segments, err := projectFocusedFragment(item.Graph)
	if err != nil || len(segments) != 1 || len(segments[0].Blocks) != 1 || segments[0].Blocks[0].Instructions != "Texto guardado que não pode desaparecer." || segments[0].PlannedStart != "" {
		t.Fatal("canonical fragment omitted a stored phase or invented a start", err, segments)
	}
}
func TestReuseTimestampPrecisionRefusesUnsaveableNanosecondsWithoutRounding(t *testing.T) {
	item := reuseExample(t).Source.Items[0]
	week := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	prefix := reuseMappingPrefix(item.Key)
	values := url.Values{prefix + ".date": {"2026-11-10"}, prefix + ".start_time": {"16:00:00.123456789"}}
	_, e := normalizeReuseSchedule(item, values, week)
	if e[prefix+".start_time"] == "" || values.Get(prefix+".start_time") != "16:00:00.123456789" {
		t.Fatal("unrepresentable timestamp was accepted or normalized away", e)
	}
	values.Set(prefix+".start_time", "16:00:00.123456")
	schedule, e := normalizeReuseSchedule(item, values, week)
	if len(e) > 0 || schedule.StartsAt.Nanosecond() != 123456000 {
		t.Fatal("actual database precision lost", e)
	}
}

func TestReuseLegacySessionSourcePreviewKeepsOwnTextWithoutCalendar(t *testing.T) {
	row := dbgen.GetReuseRoutineRow{ID: uuid.New(), Name: "Rotina configurada", Kind: "SESSION", Snapshot: []byte(`{"title":"Título guardado","description":"Texto próprio guardado.","entry_kind":"TRAINING","duration_seconds":3600,"segments":[{"modality":"WATER","planned_start_offset_minutes":10,"blocks":[]}]}`)}
	item, err := decodeReuseRoutine(row)
	if err != nil {
		t.Fatal(err)
	}
	component, err := reuseSourcePreview(reuseRead{Source: reuseSourceMaterial{Routine: &row, Items: []reuseSourceItem{item}}})
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	if err = component.Render(t.Context(), &body); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Título guardado", "Texto próprio guardado.", "10 min após o início"} {
		if !strings.Contains(body.String(), text) {
			t.Fatal("source instruction omitted", text)
		}
	}
	for _, fake := range []string{"0001-01-01", "01/01/0001", ">00:10<"} {
		if strings.Contains(body.String(), fake) {
			t.Fatal("source calendar invented", fake)
		}
	}
}

func TestReuseFragmentCompatibilityUsesExistingStructuredContentRules(t *testing.T) {
	target := reuseExample(t).Proposal.Destinations[0].Graph
	target.Activities[0].Modality = "GYM"
	target.Activities[0].Phases = nil
	// Plain own words have no typed water/gym prescription; existing restore
	// semantics allow them in another activity without relabelling that activity.
	row := dbgen.GetReuseRoutineRow{ID: uuid.New(), Name: "Notas configuradas", Kind: "BLOCK", Modality: focusedPointer(dbgen.TrainingSegmentModalityWATER), Snapshot: []byte(`{"purpose":"MAIN","title":"Instruções","instructions":"Texto próprio reutilizável."}`)}
	item, err := decodeReuseRoutine(row)
	if err != nil {
		t.Fatal(err)
	}
	copied, _, err := appendReuseFragment(item, target, target.Activities[0].ID, nil)
	if err != nil || copied.Graph.Activities[0].Modality != "GYM" {
		t.Fatal("invented plain-note modality restriction", err)
	}
	item.Graph.Activities[0].Phases[0].Water = &focusedGraphWaterInput{Method: "CUSTOM"}
	if _, _, err = appendReuseFragment(item, target, target.Activities[0].ID, nil); err == nil {
		t.Fatal("typed water inserted into gym segment")
	}
	p := reuseExample(t).Proposal.Destinations[0].Graph
	phase := p.Activities[0].Phases[0]
	for len(p.Activities[0].Phases) <= focusedWorkoutMaxPhases {
		copy := phase
		copy.ID = uuid.New()
		copy.Key = fmt.Sprintf("p_%d", len(p.Activities[0].Phases)+1)
		p.Activities[0].Phases = append(p.Activities[0].Phases, copy)
	}
	if err = validateReuseGraphLimits(p); !errors.Is(err, errFocusedWorkoutLimit) {
		t.Fatal("aggregate destination limit not enforced before preview", err)
	}
}

func TestReuseMappingRejectsMalformedTransport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		keys   []string
		values url.Values
		want   error
	}{
		{"oversize transport", []string{"m_1"}, url.Values{"mapping_order": {"m_1"}, "week_title": {strings.Repeat("ç", focusedWorkoutMaxRequestBytes)}}, errFocusedWorkoutLimit},
		{"invalid key", []string{"m_1.extra"}, url.Values{"mapping_order": {"m_1.extra"}}, errFocusedWorkoutInvalid},
		{"missing order", []string{"m_1"}, url.Values{}, errFocusedWorkoutInvalid},
		{"reordered sources", []string{"m_1", "m_2"}, url.Values{"mapping_order": {"m_2", "m_1"}}, errFocusedWorkoutInvalid},
		{"binary text", []string{"m_1"}, url.Values{"mapping_order": {"m_1"}, "week_title": {"Título\x00"}}, errFocusedWorkoutInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values, err := parseReuseValues(tc.values, tc.keys)
			if !errors.Is(err, tc.want) || values != nil {
				t.Fatalf("invalid mapping reached the editor: %v, %v", values, err)
			}
		})
	}
}

func TestReuseScheduleRejectsInconsistentDestinationFacts(t *testing.T) {
	week := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                                string
		precision                           TrainingSchedulePrecision
		duration                            *int64
		day, start, occurrence, endDay, end string
		want                                string
	}{
		{name: "forged fold occurrence", precision: TrainingScheduleStartOnly, day: "2026-10-25", start: "01:30:00.123456", occurrence: "2026-10-25T05:30:00Z", want: "start_occurrence"},
		{name: "outside target week", precision: TrainingScheduleStartOnly, day: "2026-10-26", start: "10:00", want: "date"},
		{name: "interval crosses week boundary", precision: TrainingScheduleInterval, day: "2026-10-25", start: "22:00", endDay: "2026-10-26", end: "00:01", want: "end_time"},
		{name: "interval ends before start", precision: TrainingScheduleInterval, day: "2026-10-24", start: "14:00", endDay: "2026-10-24", end: "12:00", want: "date"},
		{name: "start-only origin cannot acquire an end", precision: TrainingScheduleStartOnly, day: "2026-10-24", start: "14:00", end: "16:00", want: "end_time"},
		{name: "stored routine cannot acquire a custom end", precision: TrainingScheduleInterval, duration: focusedPointer(int64(3600)), day: "2026-10-24", start: "14:00", end: "16:00", want: "end_time"},
		{name: "invalid stored duration", precision: TrainingScheduleInterval, duration: focusedPointer(int64(0)), day: "2026-10-24", start: "14:00", want: "start_time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := reuseExample(t).Source.Items[0]
			item.Graph.Kind = "LOGISTICS"
			item.Graph.Schedule = TrainingSessionSchedule{Precision: tc.precision}
			item.DurationSeconds = tc.duration
			p := reuseMappingPrefix(item.Key)
			_, errs := normalizeReuseSchedule(item, url.Values{
				p + ".date": {tc.day}, p + ".start_time": {tc.start}, p + ".start_occurrence": {tc.occurrence},
				p + ".end_date": {tc.endDay}, p + ".end_time": {tc.end},
			}, week)
			if errs[p+"."+tc.want] == "" {
				t.Fatalf("inconsistent destination did not surface its field error: %v", errs)
			}
		})
	}
}

func TestIndependentReuseRequiresStoredOriginsAndTheSameReviewedItem(t *testing.T) {
	p := reuseExample(t)
	item := p.Source.Items[0]
	target := item.Graph.Context
	if _, err := independentReuseGraph(item, target, item.Graph.Schedule, &reuseDestination{Key: "m_other"}); !errors.Is(err, errFocusedWorkoutInvalid) {
		t.Fatalf("another reviewed item supplied the destination identities: %v", err)
	}
	item.Origins = nil
	if _, err := independentReuseGraph(item, target, item.Graph.Schedule, nil); !errors.Is(err, errFocusedWorkoutInvalid) {
		t.Fatalf("water work copied without its original profile pin: %v", err)
	}
}

func TestIndependentReuseRetainsExplicitDayBandWithoutAliasing(t *testing.T) {
	p := reuseExample(t)
	item := p.Source.Items[0]
	item.Graph.Kind = "LOGISTICS"
	band := TrainingBandAfternoon
	date := item.Graph.Context.WeekStart.Format("2006-01-02")
	schedule := TrainingSessionSchedule{Date: date, Precision: TrainingScheduleBand, Band: &band}
	out, err := independentReuseGraph(item, item.Graph.Context, schedule, nil)
	if err != nil {
		t.Fatal(err)
	}
	band = TrainingBandMorning
	if out.Graph.Schedule.Band == nil || *out.Graph.Schedule.Band != TrainingBandAfternoon || out.Graph.Schedule.StartsAt != nil {
		t.Fatalf("copy altered the selected band or invented an exact time: %+v", out.Graph.Schedule)
	}
}
