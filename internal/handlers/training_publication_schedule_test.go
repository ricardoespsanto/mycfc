package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func mixedPublicationSourceFixture() publicationSourceEnvelope {
	source := publicationSourceFixture()
	base := source.Overview[0]
	source.Overview = nil
	source.Candidates = nil
	source.EligibleRecipients = nil
	for i, precision := range []string{"INTERVAL", "START_ONLY", "DAY", "BAND"} {
		row := base
		id := uuid.New()
		kind := dbgen.TrainingEntryKindTRAINING
		if i >= 2 {
			kind = dbgen.TrainingEntryKindREST
		}
		row.SessionID, row.SchedulePrecision, row.EntryKind = &id, &precision, &kind
		row.ScheduledOn = source.Header.WeekStart
		row.ScheduledOn.Time = row.ScheduledOn.Time.AddDate(0, 0, i)
		row.SessionOn = row.ScheduledOn
		row.StartsAt.Time = row.StartsAt.Time.AddDate(0, 0, i).Add(123456 * time.Microsecond)
		row.EndsAt.Time = row.EndsAt.Time.AddDate(0, 0, i).Add(123456 * time.Microsecond)
		if i >= 1 {
			row.EndsAt = pgtype.Timestamptz{}
		}
		if i >= 2 {
			row.StartsAt = pgtype.Timestamptz{}
		}
		if i == 3 {
			band := "AFTERNOON"
			row.DayBand = &band
		}
		source.Overview = append(source.Overview, row)
		member, athlete := uuid.New(), uuid.New()
		source.Candidates = append(source.Candidates, publicationCandidate{SessionID: id, MembershipID: member, SubjectID: &athlete, StartsOn: source.Header.WeekStart, SessionOn: row.SessionOn})
		source.EligibleRecipients = append(source.EligibleRecipients, dbgen.ListStructuredTrainingPublicationMembersRow{MembershipID: member, AthleteUserID: &athlete, AthleteName: "Synthetic athlete", StartsOn: source.Header.WeekStart, SessionID: id})
	}
	return source
}

func TestPublicationMixedSchedulesPreserveNativePrecisionAndFrozenHeaders(t *testing.T) {
	source := mixedPublicationSourceFixture()
	material, err := materializePublicationSource(decodeSourceFixture(t, source), lisbonLocation())
	if err != nil || len(material.Prescriptions) != 4 {
		t.Fatalf("mixed materialization: %d %v", len(material.Prescriptions), err)
	}
	for i, prescription := range material.Prescriptions {
		row := source.Overview[i]
		decoded, err := decodeStructuredPrescription(prescription.Snapshot, lisbonLocation())
		if err != nil || decoded.PlanTitle != *row.PlanTitle || decoded.WeekDescription != *row.PlanDescription || decoded.Season != *row.SeasonName {
			t.Fatalf("frozen headers lost at %d: %+v %v", i, decoded, err)
		}
		if i == 0 {
			if decoded.SchemaVersion != 1 || bytes.Contains(prescription.Snapshot, []byte(`"schedule"`)) {
				t.Fatal("INTERVAL changed v1 representation")
			}
			continue
		}
		var typed structuredPrescriptionSnapshotV2
		if err = json.Unmarshal(prescription.Snapshot, &typed); err != nil || typed.SchemaVersion != 2 || typed.Session.Schedule.Date != row.SessionOn.Time.Format("2006-01-02") {
			t.Fatalf("wrong native v2 date %d: %v", i, err)
		}
		if bytes.Contains(prescription.Snapshot, []byte(`"entry_kind"`)) || typed.Session.Schedule.EndsAt != nil {
			t.Fatal("ambiguous kind or invented end")
		}
		if i == 1 {
			if typed.Session.Schedule.StartsAt == nil || !typed.Session.Schedule.StartsAt.Equal(row.StartsAt.Time) || typed.Session.Schedule.StartsAt.Nanosecond() != 123456000 {
				t.Fatal("native microseconds were replaced by the rendered minute")
			}
		} else if typed.Session.Schedule.StartsAt != nil {
			t.Fatal("untimed entry acquired an invented start")
		}
		if decoded.Session.When != decoded.Session.Schedule.Label || decoded.Session.Schedule.Precision != *row.SchedulePrecision {
			t.Fatal("recipient reader lost precision")
		}
	}
}

func TestPublicationNativeScheduleRowsRefuseConflictsAndInvalidShapes(t *testing.T) {
	for _, change := range []func(*publicationSourceEnvelope){
		func(s *publicationSourceEnvelope) {
			r := s.Overview[1]
			r.StartsAt.Time = r.StartsAt.Time.Add(time.Microsecond)
			s.Overview = append(s.Overview, r)
		},
		func(s *publicationSourceEnvelope) {
			s.Overview[1].SchedulePrecision = publicationScheduleStringPtr("FUTURE_UNKNOWN")
		},
		func(s *publicationSourceEnvelope) { s.Overview[1].EndsAt = s.Overview[0].EndsAt },
		func(s *publicationSourceEnvelope) { s.Overview[2].EntryKind = s.Overview[0].EntryKind },
		func(s *publicationSourceEnvelope) {
			s.Overview[2].SessionOn.Time = s.Overview[2].SessionOn.Time.AddDate(0, 0, 1)
		},
	} {
		source := mixedPublicationSourceFixture()
		change(&source)
		if _, err := materializePublicationSource(source, lisbonLocation()); err == nil {
			t.Fatal("inconsistent/invalid native schedule accepted")
		}
	}
	source := mixedPublicationSourceFixture()
	if err := validatePublicationScheduleWriteState(source, db.TrainingScheduleState{}); !errors.Is(err, ErrTrainingScheduleWriteUnsupported) {
		t.Fatal("disabled partial source became confirmable", err)
	}
	if err := validatePublicationScheduleWriteState(source, db.TrainingScheduleState{PartialV2Enabled: true}); err != nil {
		t.Fatal(err)
	}
}

func publicationScheduleStringPtr(s string) *string { return &s }

func TestPublicationRecipientVariationDoesNotMutateNextRecipientsSource(t *testing.T) {
	source := publicationSourceFixture()
	segment, variation, secondMember, secondAthlete := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	position := int32(1)
	modality := dbgen.TrainingSegmentModalityWATER
	source.Overview[0].SegmentID, source.Overview[0].SegmentPosition = &segment, &position
	source.Overview[0].SegmentModality = &modality
	source.Overview[0].SegmentTitle = publicationScheduleStringPtr("Common instructions")
	first := source.EligibleRecipients[0]
	source.EligibleRecipients = append(source.EligibleRecipients, dbgen.ListStructuredTrainingPublicationMembersRow{MembershipID: secondMember, AthleteUserID: &secondAthlete, AthleteName: "Second athlete", StartsOn: source.Header.WeekStart, SessionID: first.SessionID})
	source.Candidates = append(source.Candidates, publicationCandidate{SessionID: first.SessionID, MembershipID: secondMember, SubjectID: &secondAthlete, StartsOn: source.Header.WeekStart, SessionOn: source.Header.WeekStart})
	patch := json.RawMessage(`{"title":"First athlete only"}`)
	source.VariationDefinitions = []publicationDefinition{{VariationID: variation, PlanID: source.Header.PlanID, SubjectID: segment, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, TargetMembershipID: &first.MembershipID, Patch: patch}}
	source.VariationMatches = []publicationVariationTransport{{ListTrainingVariationMatchesForManagerRow: dbgen.ListTrainingVariationMatchesForManagerRow{VariationID: variation, PlanID: source.Header.PlanID, SessionID: first.SessionID, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, SubjectID: segment, Operation: dbgen.TrainingVariationOperationOVERRIDE, MembershipID: &first.MembershipID, Priority: 2, ChangeSummary: "First athlete only"}, Patch: patch}}
	material, err := materializePublicationSource(source, lisbonLocation())
	if err != nil || len(material.Prescriptions) != 2 {
		t.Fatal(err)
	}
	for i, input := range material.Prescriptions {
		snapshot, err := decodeStructuredPrescription(input.Snapshot, lisbonLocation())
		want := "Common instructions"
		if i == 0 {
			want = "First athlete only"
		}
		if err != nil || len(snapshot.Session.Segments) != 1 || snapshot.Session.Segments[0].Title != want {
			t.Fatalf("recipient%d inherited another recipient's variation: %+v %v", i, snapshot.Session.Segments, err)
		}
	}
}

func TestPublicationEnabledAdmissionReplacesOnlyPublicationV1Guard(t *testing.T) {
	q, input, now := publicationTransactionFixture(t)
	q.partialEnabled = true
	if _, err := savePublicationInTransaction(t.Context(), q, input, time.UTC, func() time.Time { return now }); err != nil {
		t.Fatal(err)
	}
	if len(q.steps) < 3 || q.steps[0] != "admission" || q.steps[1] != "runtime" || q.steps[2] != "actor" {
		t.Fatal("admission did not precede business locks", q.steps)
	}
	for _, step := range q.steps {
		if step == "guard" {
			t.Fatal("enabled publisher retained v1-only guard")
		}
	}
}
