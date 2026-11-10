package handlers

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestTrainingScheduleReadsAllRecordedShapesWithoutInventingTimes(t *testing.T) {
	start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	afternoon := TrainingBandAfternoon
	for _, s := range []TrainingSessionSchedule{
		{Precision: TrainingScheduleInterval, Date: "2026-11-15", StartsAt: &start, EndsAt: &end},
		{Precision: TrainingScheduleStartOnly, Date: "2026-11-15", StartsAt: &start},
		{Precision: TrainingScheduleDay, Date: "2026-11-15"},
		{Precision: TrainingScheduleBand, Date: "2026-11-15", Band: &afternoon},
	} {
		t.Run(string(s.Precision), func(t *testing.T) {
			if e := s.Validate(); e != nil {
				t.Fatal(e)
			}
			v := trainingScheduleView(s, lisbonLocation())
			if !v.Known || v.Unavailable || v.Date != s.Date {
				t.Fatalf("view %#v", v)
			}
			if s.StartsAt == nil && v.StartsAt != "" {
				t.Fatalf("invented start: %#v", v)
			}
			if s.EndsAt == nil && (v.EndsAt != "" || v.EndsOn != "") {
				t.Fatalf("invented end: %#v", v)
			}
			if s.Precision != TrainingScheduleInterval && !errors.Is(trainingScheduleExact(s), ErrTrainingScheduleWriteUnsupported) {
				t.Fatal("partial source writable")
			}
		})
	}
}
func TestTrainingScheduleRejectsContradictoryShapesAndInvalidDates(t *testing.T) {
	start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	band := TrainingBandMorning
	for _, s := range []TrainingSessionSchedule{
		{Precision: TrainingScheduleDay, Date: "2026-02-30"},
		{Precision: TrainingScheduleDay, Date: "2026-11-15", StartsAt: &start},
		{Precision: TrainingScheduleBand, Date: "2026-11-15"},
		{Precision: TrainingScheduleInterval, Date: "2026-11-15", StartsAt: &start},
		{Precision: TrainingScheduleStartOnly, Date: "2026-11-15", StartsAt: &start, EndsAt: &end},
		{Precision: TrainingScheduleInterval, Date: "2026-11-16", StartsAt: &start, EndsAt: &end},
		{Precision: TrainingScheduleInterval, Date: "2026-11-15", StartsAt: &start, EndsAt: &end, Band: &band},
	} {
		if s.Validate() == nil {
			t.Fatalf("accepted invalid %#v", s)
		}
	}
	if (TrainingSessionSchedule{Precision: TrainingScheduleDay, Date: "2026-11-15"}).ValidateKind("TRAINING") == nil {
		t.Fatal("DAY training accepted")
	}
}
func TestTrainingScheduleNullableReaderDoesNotUseZeroTimestamp(t *testing.T) {
	date := pgtype.Date{Time: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), Valid: true}
	s, e := trainingScheduleFromRecord("DAY", date, pgtype.Timestamptz{}, pgtype.Timestamptz{}, nil)
	if e != nil || s.StartsAt != nil || s.EndsAt != nil || s.Date != "2026-11-15" {
		t.Fatalf("%#v %v", s, e)
	}
	if _, e = trainingScheduleFromRecord("INTERVAL", date, pgtype.Timestamptz{}, pgtype.Timestamptz{}, nil); e == nil {
		t.Fatal("NULL interval accepted")
	}
}
func TestTrainingScheduleLocalParserRejectsLisbonGapAndFold(t *testing.T) {
	for _, v := range []string{"2026-03-29T01:30", "2026-10-25T01:30", "2026-02-30T12:00"} {
		if _, e := parseTrainingLocalDateTime(v, lisbonLocation()); e == nil {
			t.Fatalf("accepted %s", v)
		}
	}
	if _, e := parseTrainingLocalDateTime("2026-11-15T10:30", lisbonLocation()); e != nil {
		t.Fatal(e)
	}
}
func TestTrainingPrescriptionLegacyV1RemainsFrozenWithoutTypedSchedule(t *testing.T) {
	old := structuredPrescriptionSnapshot{SchemaVersion: 1, PlanID: "plan", Session: pages.StructuredTrainingSession{ID: "session", Title: "Água", When: "Texto legado congelado 10h?"}}
	before, e := json.Marshal(old)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := decodeStructuredPrescription(before, lisbonLocation())
	if e != nil {
		t.Fatal(e)
	}
	if decoded.Session.Schedule.Known || decoded.Session.When != old.Session.When {
		t.Fatalf("legacy inferred %#v", decoded.Session)
	}
	decoded.Session.Schedule = pages.TrainingScheduleView{Known: true, Precision: "DAY", Date: "2030-01-01"}
	decoded.Session.WriteUnsupported = true
	decoded.Session.UnsupportedMessage = "private"
	after, _ := json.Marshal(decoded)
	if string(before) != string(after) {
		t.Fatalf("v1 bytes changed\nbefore %s\nafter %s", before, after)
	}
}
func TestTrainingPrescriptionV2UsesFrozenTypedScheduleAndFailsUnknownVersions(t *testing.T) {
	raw := []byte(`{"schema_version":2,"plan_id":"plan","session":{"ID":"session","Title":"Descanso","EntryKind":"REST","When":"obsolete 07h","schedule":{"precision":"DAY","date":"2026-11-15","starts_at":null,"ends_at":null,"band":null}}}`)
	decoded, e := decodeStructuredPrescription(raw, lisbonLocation())
	if e != nil {
		t.Fatal(e)
	}
	if !decoded.Session.Schedule.Known || decoded.Session.Schedule.Precision != "DAY" || decoded.Session.StartsAt != "" || strings.Contains(decoded.Session.When, "07h") {
		t.Fatalf("wrong v2 %#v", decoded.Session)
	}
	if !errors.Is(unsupportedTrainingArtifact(raw), ErrTrainingScheduleWriteUnsupported) {
		t.Fatal("C allowed v2 write")
	}
	for _, bad := range []string{`{"schema_version":99}`, strings.Replace(string(raw), `"schema_version":2`, `"schema_version":2.0`, 1), strings.Replace(string(raw), `"date":"2026-11-15"`, `"date":"2026-11-15","date":"2026-11-16"`, 1), strings.Replace(string(raw), `"band":null`, `"band":null,"guess":true`, 1), strings.Replace(string(raw), `"EntryKind":"REST"`, `"EntryKind":"TRAINING"`, 1)} {
		if _, e = decodeStructuredPrescription([]byte(bad), lisbonLocation()); e == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
func TestTrainingRoutineV2IsSeparateFromPrescriptionEnvelope(t *testing.T) {
	raw := []byte(`{"schema_version":2,"title":"Descanso","description":"","entry_kind":"REST","segments":[],"schedule":{"precision":"BAND","date":"2026-11-15","starts_at":null,"ends_at":null,"band":"AFTERNOON"}}`)
	v, e := decodeRoutineSchedule(raw, lisbonLocation())
	if e != nil || !v.Known || v.Band != "AFTERNOON" || v.StartsAt != "" {
		t.Fatalf("%#v %v", v, e)
	}
	if _, e = decodeStructuredPrescription(raw, lisbonLocation()); e == nil {
		t.Fatal("routine decoded as prescribed DTO")
	}
}
func TestTrainingScheduleConcurrencyErrorsAtEveryBoundary(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		if !errors.Is(trainingScheduleWriteError(&pgconn.PgError{Code: code}), errStructuredTrainingPublicationConflict) {
			t.Fatal(code)
		}
	}
	ordinary := errors.New("database offline")
	if trainingScheduleWriteError(ordinary) != ordinary {
		t.Fatal("ordinary error erased")
	}
}

func TestTrainingScheduleKnownFoldInstantsRemainDistinctInDisplay(t *testing.T) {
	start, _ := time.Parse(time.RFC3339, "2026-10-25T01:30:00+01:00")
	end, _ := time.Parse(time.RFC3339, "2026-10-25T01:30:00Z")
	s := TrainingSessionSchedule{Precision: TrainingScheduleInterval, Date: "2026-10-25", StartsAt: &start, EndsAt: &end}
	v := trainingScheduleView(s, lisbonLocation())
	if v.Unavailable || !strings.Contains(v.Label, "UTC+01:00") || !strings.Contains(v.Label, "UTC+00:00") {
		t.Fatalf("fold instants collapsed: %#v", v)
	}
}

func TestCompatibilityAuthoringDoesNotSilentlyIgnorePrecisionRequestFields(t *testing.T) {
	for _, key := range []string{"precision", "schedule_precision", "scheduled_on", "day_band", "schema_version", "schedule"} {
		if !trainingScheduleFormUnsupported(url.Values{key: {"DAY"}}) {
			t.Fatal(key)
		}
	}
	if trainingScheduleFormUnsupported(url.Values{"starts_at": {"2026-11-15T10:00"}, "ends_at": {"2026-11-15T11:00"}}) {
		t.Fatal("legacy exact form refused")
	}
}

func TestExactCopyChecksEndpointTransitionPatternWithoutChoosingNewMode(t *testing.T) {
	parse := func(raw string) time.Time {
		v, e := time.Parse(time.RFC3339, raw)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	start := parse("2026-10-24T23:00:00+01:00")
	end := parse("2026-10-25T02:00:00Z")
	target := parse("2026-10-17T23:00:00+01:00")
	if !errors.Is(trainingCopiedExactInterval(start, end, target), ErrTrainingScheduleWriteUnsupported) {
		t.Fatal("source end transition silently chose an elapsed/wall result")
	}
	if e := trainingCopiedExactInterval(parse("2026-11-09T10:00:00Z"), parse("2026-11-09T11:30:00Z"), parse("2026-11-16T10:00:00Z")); e != nil {
		t.Fatalf("ordinary exact copy refused: %v", e)
	}
	// Matching transition patterns are retained; C does not ban all DST intervals.
	if e := trainingCopiedExactInterval(start, end, parse("2027-10-30T23:00:00+01:00")); e != nil {
		t.Fatalf("matching known endpoint patterns refused: %v", e)
	}
}
