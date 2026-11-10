package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func publicationSourceFixture() publicationSourceEnvelope {
	plan, group, season, session, member, athlete, placement := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	week := pgtype.Date{Time: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), Valid: true}
	title, description, seasonName, precision, status := "Semana de treino", "Plano confirmado", "2026/2027", "INTERVAL", "ACTIVE"
	kind := dbgen.TrainingEntryKindTRAINING
	starts := pgtype.Timestamptz{Time: week.Time.Add(10 * time.Hour), Valid: true}
	ends := pgtype.Timestamptz{Time: week.Time.Add(11 * time.Hour), Valid: true}
	return publicationSourceEnvelope{
		Contract:             publicationSourceContract,
		Header:               publicationPlanHeader{PlanID: plan, GroupID: group, SeasonID: &season, WeekStart: week, SourceUpdatedAt: starts},
		Overview:             []dbgen.ListStructuredTrainingOverviewForManagerRow{{GroupID: group, GroupName: "Cadetes", ProgrammeName: "Competição", PlanID: &plan, PlanTitle: &title, PlanDescription: &description, SeasonName: &seasonName, WeekStart: week, SessionID: &session, SessionTitle: &title, SessionDescription: &description, StartsAt: starts, EndsAt: ends, EntryKind: &kind, SchedulePrecision: &precision, SessionOn: week, SessionStatus: &status}},
		Candidates:           []publicationCandidate{{SessionID: session, PlacementID: placement, MembershipID: &member, SubjectID: athlete, StartsOn: week, SessionOn: week}},
		EligibleRecipients:   []dbgen.ListStructuredTrainingPublicationMembersRow{{PlacementID: placement, MembershipID: member, AthleteUserID: &athlete, AthleteName: "Ana", StartsOn: week, SessionID: session}},
		VariationDefinitions: []publicationDefinition{}, VariationTargets: []publicationTarget{}, VariationMatches: []publicationVariationTransport{}, ReferencedEvents: []publicationEvent{}, LatestPrescriptions: []publicationHashTuple{},
	}
}
func decodeSourceFixture(t *testing.T, source publicationSourceEnvelope) publicationSourceEnvelope {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodePublicationSource(raw, publicationSourceBudget{SourceBytes: int64(len(raw)), RawTupleCount: int64(len(source.Candidates)), LatestTupleCount: int64(len(source.LatestPrescriptions))}, source.Header.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestPublicationTransportPreservesCanonicalV1AndMaterialNoop(t *testing.T) {
	source := publicationSourceFixture()
	decoded := decodeSourceFixture(t, source)
	actual, err := materializePublicationSource(decoded, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	audience, week, ok := structuredPublicationWeek(assembleStructuredTraining(managerStructuredRows(source.Overview), time.UTC), source.Header.PlanID)
	if !ok {
		t.Fatal("missing canonical week")
	}
	expected, err := buildStructuredPrescriptionInputs(source.Header.PlanID, audience, week, source.EligibleRecipients, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expected[0].Snapshot, actual.Prescriptions[0].Snapshot) || expected[0].SnapshotSHA256 != actual.Prescriptions[0].SnapshotSHA256 {
		t.Fatal("transport changed canonical frozen bytes")
	}
	if actual.NoChanges || actual.Added != 1 {
		t.Fatal("first publication is not new")
	}
	previous := uuid.New()
	source.Header.LatestPublicationID = &previous
	source.Header.LatestRevision = 1
	p := expected[0]
	source.LatestPrescriptions = []publicationHashTuple{{PlacementID: &p.PlacementID, SessionID: p.SessionID, MembershipID: p.MembershipID, AthleteUserID: &p.AthleteUserID, SnapshotSHA256: p.SnapshotSHA256}}
	// An edit followed by revert may have a new source timestamp. Currentness is
	// established from exact content/subject equality, not the timestamp hint.
	source.Header.LatestSourceUpdatedAt = source.Header.SourceUpdatedAt
	source.Header.SourceUpdatedAt.Time = source.Header.SourceUpdatedAt.Time.Add(time.Minute)
	unchanged, err := materializePublicationSource(decodeSourceFixture(t, source), time.UTC)
	if err != nil || !unchanged.NoChanges || unchanged.Unchanged != 1 {
		t.Fatal("identical material source not a no-op", err)
	}
	source.LatestPrescriptions[0].AthleteUserID = testUUIDPtr(uuid.New())
	replaced, err := materializePublicationSource(decodeSourceFixture(t, source), time.UTC)
	if err != nil || replaced.NoChanges || replaced.Changed != 1 {
		t.Fatal("same-count subject substitution treated as unchanged", err)
	}
	source.LatestPrescriptions[0].AthleteUserID = nil
	detached, err := materializePublicationSource(decodeSourceFixture(t, source), time.UTC)
	if err != nil || detached.NoChanges || detached.Changed != 1 {
		t.Fatal("privacy-detached historical subject became current", err)
	}
}

func TestPublicationTransportPatchIsRawObjectAndReusesResolver(t *testing.T) {
	source := publicationSourceFixture()
	segment, variation := uuid.New(), uuid.New()
	position := int32(1)
	modality := dbgen.TrainingSegmentModalityWATER
	title := "Base"
	source.Overview[0].SegmentID = &segment
	source.Overview[0].SegmentPosition = &position
	source.Overview[0].SegmentModality = &modality
	source.Overview[0].SegmentTitle = &title
	patch := json.RawMessage(`{"title":"Alterado"}`)
	source.VariationDefinitions = []publicationDefinition{{VariationID: variation, PlanID: source.Header.PlanID, SubjectID: segment, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, Patch: patch}}
	member := source.EligibleRecipients[0].MembershipID
	source.VariationMatches = []publicationVariationTransport{{ListTrainingVariationMatchesForManagerRow: dbgen.ListTrainingVariationMatchesForManagerRow{VariationID: variation, PlanID: source.Header.PlanID, SessionID: *source.Overview[0].SessionID, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, SubjectID: segment, Operation: dbgen.TrainingVariationOperationOVERRIDE, MembershipID: &member, Priority: 2, ChangeSummary: "Ajuste"}, Patch: patch}}
	decoded := decodeSourceFixture(t, source)
	if !bytes.Equal(decoded.VariationMatches[0].Patch, patch) {
		t.Fatal("patch encoded as base64 instead of raw JSON")
	}
	result, err := materializePublicationSource(decoded, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot structuredPrescriptionSnapshot
	if err := json.Unmarshal(result.Prescriptions[0].Snapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Session.Segments) != 1 || snapshot.Session.Segments[0].Title != "Alterado" {
		t.Fatal("existing resolver was not used")
	}
}

func TestPublicationTransportRejectsIncompleteSourceAndBudgets(t *testing.T) {
	original := publicationSourceFixture()
	cases := []struct {
		name   string
		change func(*publicationSourceEnvelope)
	}{
		{"foreign plan", func(s *publicationSourceEnvelope) { s.Header.PlanID = uuid.New() }},
		{"missing group", func(s *publicationSourceEnvelope) { s.Header.GroupID = uuid.Nil }},
		{"missing season", func(s *publicationSourceEnvelope) { s.Header.SeasonID = nil }},
		{"missing week", func(s *publicationSourceEnvelope) { s.Header.WeekStart = pgtype.Date{} }},
		{"missing overview", func(s *publicationSourceEnvelope) { s.Overview = nil }},
		{"hidden group count", func(s *publicationSourceEnvelope) { s.Overview[0].MemberCount = 1 }},
		{"unknown recipient session", func(s *publicationSourceEnvelope) { s.EligibleRecipients[0].SessionID = uuid.New() }},
		{"missing recipient subject", func(s *publicationSourceEnvelope) { s.EligibleRecipients[0].AthleteUserID = nil }},
		{"substituted recipient subject", func(s *publicationSourceEnvelope) { s.EligibleRecipients[0].AthleteUserID = testUUIDPtr(uuid.New()) }},
		{"duplicate candidate", func(s *publicationSourceEnvelope) { s.Candidates = append(s.Candidates, s.Candidates[0]) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := publicationSourceFixture()
			expectedPlan := s.Header.PlanID
			tc.change(&s)
			raw, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodePublicationSource(raw, publicationSourceBudget{SourceBytes: int64(len(raw)), RawTupleCount: int64(len(s.Candidates))}, expectedPlan); err == nil {
				t.Fatal("incomplete source accepted")
			}
		})
	}
	raw, _ := json.Marshal(original)
	good := publicationSourceBudget{SourceBytes: int64(len(raw)), RawTupleCount: 1}
	for _, budget := range []publicationSourceBudget{{SourceBytes: good.SourceBytes, RawTupleCount: 1, Overflow: true}, {SourceBytes: publicationSourceMaxBytes + 1}, {SourceBytes: good.SourceBytes, RawTupleCount: 4097}, {SourceBytes: good.SourceBytes, RawTupleCount: 1, LatestTupleCount: 4097}, {SourceBytes: good.SourceBytes + 1, RawTupleCount: 1}, {SourceBytes: good.SourceBytes, RawTupleCount: 2}} {
		if _, err := decodePublicationSource(raw, budget, original.Header.PlanID); err == nil {
			t.Fatal("overflow or mismatched budget accepted")
		}
	}
	unknown := bytes.Replace(raw, []byte(`"contract":`), []byte(`"unexpected":true,"contract":`), 1)
	if _, err := decodePublicationSource(unknown, publicationSourceBudget{SourceBytes: int64(len(unknown)), RawTupleCount: 1}, original.Header.PlanID); err == nil {
		t.Fatal("unknown projection accepted")
	}
}

func TestPublicationTransportEmptyInitialAndEmptySuccessor(t *testing.T) {
	source := publicationSourceFixture()
	source.Candidates = []publicationCandidate{}
	source.EligibleRecipients = []dbgen.ListStructuredTrainingPublicationMembersRow{}
	if _, err := materializePublicationSource(decodeSourceFixture(t, source), time.UTC); !errors.Is(err, errPublicationFirstEmpty) {
		t.Fatal("empty initial publish accepted", err)
	}
	latest := uuid.New()
	source.Header.LatestPublicationID = &latest
	source.Header.LatestRevision = 1
	source.LatestPrescriptions = []publicationHashTuple{{PlacementID: testUUIDPtr(uuid.New()), SessionID: uuid.New(), MembershipID: uuid.New(), AthleteUserID: testUUIDPtr(uuid.New()), SnapshotSHA256: stringsOfZeroHash()}}
	result, err := materializePublicationSource(decodeSourceFixture(t, source), time.UTC)
	if err != nil || result.NoChanges || result.Removed != 1 {
		t.Fatal("reviewed empty successor omitted removal", err)
	}
	source.LatestPrescriptions = []publicationHashTuple{}
	result, err = materializePublicationSource(decodeSourceFixture(t, source), time.UTC)
	if err != nil || !result.NoChanges {
		t.Fatal("repeated empty latest not no-op", err)
	}
}
func stringsOfZeroHash() string {
	return "0000000000000000000000000000000000000000000000000000000000000000"
}

func TestPublicationReturnPlacementWithIdenticalBodyRequiresNewPublication(t *testing.T) {
	source := publicationSourceFixture()
	first, e := materializePublicationSource(source, time.UTC)
	if e != nil {
		t.Fatal(e)
	}
	old := first.Prescriptions[0]
	publication := uuid.New()
	source.Header.LatestPublicationID = &publication
	source.Header.LatestRevision = 1
	source.LatestPrescriptions = []publicationHashTuple{{PlacementID: &old.PlacementID, SessionID: old.SessionID, MembershipID: old.MembershipID, AthleteUserID: &old.AthleteUserID, SnapshotSHA256: old.SnapshotSHA256}}
	returnPlacement := uuid.New()
	source.Candidates[0].PlacementID = returnPlacement
	source.EligibleRecipients[0].PlacementID = returnPlacement
	next, e := materializePublicationSource(source, time.UTC)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(old.Snapshot, next.Prescriptions[0].Snapshot) || old.SnapshotSHA256 != next.Prescriptions[0].SnapshotSHA256 {
		t.Fatal("relational return lineage changed immutable snapshot content")
	}
	if next.NoChanges || next.Changed != 1 || next.Unchanged != 0 || next.Prescriptions[0].PlacementID != returnPlacement {
		t.Fatal("A2 return was incorrectly reused from withdrawn A1")
	}
}
func TestPublicationStructuralCandidatesPreserveDistinctMissingEpisodes(t *testing.T) {
	source := publicationSourceFixture()
	session := source.Candidates[0].SessionID
	a := publicationCandidate{SessionID: session, PlacementID: uuid.New(), SubjectID: uuid.New(), SessionOn: source.Header.WeekStart}
	b := a
	b.PlacementID = uuid.New()
	b.SubjectID = uuid.New()
	source.Candidates = append(source.Candidates, a, b)
	if e := validatePublicationSource(source); e != nil {
		t.Fatal("distinct ineligible placements folded by absent membership", e)
	}
	if len(publicationMembershipIDs(source)) != 1 {
		t.Fatal("NULL episode was replaced by zero membership lock")
	}
	other := append([]publicationCandidate(nil), source.Candidates...)
	other[1], other[2] = other[2], other[1]
	if !publicationSameCandidates(source.Candidates, other) {
		t.Fatal("structural order affected complete candidate comparison")
	}
	other[2].PlacementID = uuid.New()
	if publicationSameCandidates(source.Candidates, other) {
		t.Fatal("placement lineage substitution accepted")
	}
	source.Candidates = append(source.Candidates, a)
	if e := validatePublicationSource(source); e == nil {
		t.Fatal("duplicate structural placement accepted")
	}
}
