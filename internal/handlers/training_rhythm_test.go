package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
)

func rhythmInt(v int32) *int32      { return &v }
func rhythmString(v string) *string { return &v }
func rhythmFixture() publicationSourceEnvelope {
	s := publicationSourceFixture()
	k, c := uuid.MustParse("40200000-0000-0000-0000-000000000001"), uuid.MustParse("40200000-0000-0000-0000-000000000002")
	secondMember, secondPerson, secondPlacement := uuid.New(), uuid.New(), uuid.New()
	first := s.EligibleRecipients[0]
	s.EligibleRecipients = append(s.EligibleRecipients, dbgen.ListStructuredTrainingPublicationMembersRow{MembershipID: secondMember, AthleteUserID: &secondPerson, PlacementID: secondPlacement, SessionID: first.SessionID, StartsOn: first.StartsOn, AthleteName: "Canoe athlete"})
	candidate := s.Candidates[0]
	candidate.MembershipID = &secondMember
	candidate.SubjectID = secondPerson
	candidate.PlacementID = secondPlacement
	s.Candidates = append(s.Candidates, candidate)
	block, group, work, recovery, variation := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	row := s.Overview[0]
	row.SegmentID = testUUIDPtr(uuid.New())
	row.SegmentModality = func() *dbgen.TrainingSegmentModality { v := dbgen.TrainingSegmentModalityWATER; return &v }()
	row.SegmentPosition = rhythmInt(1)
	row.SegmentTitle = rhythmString("Common water")
	row.BlockID = &block
	row.BlockPosition = rhythmInt(1)
	row.BlockTitle = rhythmString("Identical prescribed work")
	row.WaterMethod = func() *dbgen.WaterWorkMethod { v := dbgen.WaterWorkMethodINTERVALS; return &v }()
	row.WaterIntensityProfileID = &k
	row.WaterStepID = &group
	row.WaterStepKind = func() *dbgen.WaterStepKind { v := dbgen.WaterStepKindREPEATGROUP; return &v }()
	row.WaterStepPosition = rhythmInt(1)
	row.WaterStepRepeats = rhythmInt(2)
	row.WaterStepName = rhythmString("Two repetitions")
	s.Overview = []dbgen.ListStructuredTrainingOverviewForManagerRow{row}
	row.WaterStepID = &work
	row.WaterParentStepID = &group
	row.WaterStepKind = func() *dbgen.WaterStepKind { v := dbgen.WaterStepKindEFFORT; return &v }()
	row.WaterStepName = rhythmString("Common effort")
	row.WaterStepRepeats = nil
	row.WaterStepDurationSeconds = rhythmInt(60)
	row.WaterStepIntensityCode = rhythmString("R2")
	row.WaterStepCadenceSpm = rhythmInt(70)
	s.Overview = append(s.Overview, row)
	row.WaterStepID = &recovery
	row.WaterStepPosition = rhythmInt(2)
	row.WaterStepName = rhythmString("Common recovery")
	row.WaterStepKind = func() *dbgen.WaterStepKind { v := dbgen.WaterStepKindEFFORT; return &v }()
	row.WaterStepIntensityCode = rhythmString("R0")
	row.WaterStepCadenceSpm = nil
	s.Overview = append(s.Overview, row)
	patch, _ := json.Marshal(map[string]string{"intensity_profile_id": c.String()})
	s.VariationDefinitions = append(s.VariationDefinitions, publicationDefinition{VariationID: variation, PlanID: s.Header.PlanID, SubjectKind: dbgen.TrainingVariationSubjectKindBLOCK, SubjectID: block, Operation: dbgen.TrainingVariationOperationOVERRIDE, Patch: patch})
	s.VariationMatches = append(s.VariationMatches, publicationVariationTransport{ListTrainingVariationMatchesForManagerRow: dbgen.ListTrainingVariationMatchesForManagerRow{VariationID: variation, PlanID: s.Header.PlanID, SessionID: first.SessionID, MembershipID: &secondMember, SubjectKind: dbgen.TrainingVariationSubjectKindBLOCK, SubjectID: block, Operation: dbgen.TrainingVariationOperationOVERRIDE, Priority: 2}, Patch: patch})
	s.Rhythm = &publicationRhythmFacts{Profiles: []dbgen.WaterIntensityProfile{{ID: k, Name: "Synthetic Kayak reference", Craft: dbgen.PaddlingCraftKAYAK, Revision: 3, IsActive: true}, {ID: c, Name: "Synthetic Canoe reference", Craft: dbgen.PaddlingCraftCANOE, Revision: 4, IsActive: true}}, Crafts: []publicationRhythmCraft{{SubjectID: *first.AthleteUserID, Kayak: true}, {SubjectID: secondPerson, Canoe: true}}}
	for _, p := range s.Rhythm.Profiles {
		ranges := [][2]int32{{0, 60}, {70, 80}, {100, 115}, {110, 130}}
		if p.Craft == dbgen.PaddlingCraftCANOE {
			ranges = [][2]int32{{0, 34}, {40, 45}, {58, 66}, {66, 78}}
		}
		for i, code := range []string{"R0", "R2", "R4", "R5"} {
			z := dbgen.WaterIntensityZone{ID: uuid.New(), ProfileID: p.ID, Position: int32(i + 1), Code: code, Label: code + " reference", CadenceMin: rhythmInt(ranges[i][0]), CadenceMax: rhythmInt(ranges[i][1]), Meaning: "Confirmed synthetic guidance"}
			if i == 0 {
				z.CadenceMin = nil
				z.Meaning = "<" + fmt.Sprint(ranges[i][1])
			}
			s.Rhythm.Zones = append(s.Rhythm.Zones, z)
		}
	}
	sort.Slice(s.Rhythm.Crafts, func(i, j int) bool {
		return s.Rhythm.Crafts[i].SubjectID.String() < s.Rhythm.Crafts[j].SubjectID.String()
	})
	return s
}
func rhythmSnapshot(t *testing.T, p StructuredPrescriptionInput) pages.StructuredTrainingSession {
	t.Helper()
	var out structuredPrescriptionSnapshot
	if e := json.Unmarshal(p.Snapshot, &out); e != nil {
		t.Fatal(e)
	}
	return out.Session
}
func TestRhythmCommonNestedWorkoutUsesExplicitPerRecipientReference(t *testing.T) {
	s := rhythmFixture()
	before, _ := json.Marshal(s.Overview)
	material, e := materializePublicationSource(s, time.UTC)
	if e != nil {
		t.Fatal(e)
	}
	a, b := rhythmSnapshot(t, material.Prescriptions[0]), rhythmSnapshot(t, material.Prescriptions[1])
	ak, bc := a.Segments[0].Blocks[0], b.Segments[0].Blocks[0]
	if ak.WaterReference == nil || bc.WaterReference == nil || ak.WaterReference.CraftCode != "KAYAK" || bc.WaterReference.CraftCode != "CANOE" || ak.WaterReference.Revision != 3 || bc.WaterReference.Revision != 4 {
		t.Fatal("selected typed revision/craft reference missing")
	}
	if len(ak.WaterSteps) != 3 || len(bc.WaterSteps) != 3 {
		t.Fatal("nested work omitted")
	}
	for i, step := range ak.WaterSteps {
		other := bc.WaterSteps[i]
		if step.ID != other.ID || step.ParentID != other.ParentID || step.DurationSeconds != other.DurationSeconds || step.Repeats != other.Repeats || step.DistanceMetres != other.DistanceMetres || step.Instructions != other.Instructions {
			t.Fatal("rhythm reference changed common prescribed work")
		}
	}
	if !strings.Contains(ak.WaterSteps[1].Intensity, "70–80") || !strings.Contains(bc.WaterSteps[1].Intensity, "40–45") || !strings.Contains(bc.WaterSteps[1].Intensity, "70 remadas/min") || !strings.Contains(bc.WaterSteps[2].Intensity, "<34") {
		t.Fatal("nested guidance did not recompute or prescribed cadence was lost", ak.WaterSteps, bc.WaterSteps)
	}
	after, _ := json.Marshal(s.Overview)
	if !bytes.Equal(before, after) {
		t.Fatal("recipient resolution mutated common source")
	}
	if len(ak.WaterReference.Zones) != 4 || ak.WaterReference.Zones[2].CadenceMax == nil || *ak.WaterReference.Zones[2].CadenceMax != 115 || *ak.WaterReference.Zones[3].CadenceMin != 110 {
		t.Fatal("intentional overlapping reference ranges changed")
	}
}
func TestRhythmExplicitChoiceMissingPermissionAndMissingZoneRefuse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*publicationSourceEnvelope)
		want   error
	}{
		{"no canoe override", func(s *publicationSourceEnvelope) {
			s.VariationDefinitions = nil
			s.VariationMatches = nil
			s.Rhythm.Profiles = s.Rhythm.Profiles[:1]
			s.Rhythm.Zones = s.Rhythm.Zones[:4]
		}, errPublicationRhythmPermission},
		{"missing used code", func(s *publicationSourceEnvelope) { s.Rhythm.Zones = append(s.Rhythm.Zones[:5], s.Rhythm.Zones[6:]...) }, errPublicationRhythmZone},
		{"inactive chosen revision", func(s *publicationSourceEnvelope) { s.Rhythm.Profiles[1].IsActive = false }, errPublicationRhythmReference},
		{"both crafts no explicit profile", func(s *publicationSourceEnvelope) {
			for i := range s.Overview {
				s.Overview[i].WaterIntensityProfileID = nil
			}
			s.VariationDefinitions = nil
			s.VariationMatches = nil
			s.Rhythm.Profiles = nil
			s.Rhythm.Zones = nil
			for i := range s.Rhythm.Crafts {
				s.Rhythm.Crafts[i].Kayak = true
				s.Rhythm.Crafts[i].Canoe = true
			}
		}, errPublicationRhythmReference},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := rhythmFixture()
			tc.mutate(&s)
			_, e := materializePublicationSource(s, time.UTC)
			if !errors.Is(e, tc.want) {
				t.Fatalf("want %v got %v", tc.want, e)
			}
		})
	}
	s := rhythmFixture()
	for i := range s.Rhythm.Crafts {
		s.Rhythm.Crafts[i].Kayak = true
		s.Rhythm.Crafts[i].Canoe = true
	}
	s.VariationDefinitions = nil
	s.VariationMatches = nil
	s.Rhythm.Profiles = s.Rhythm.Profiles[:1]
	s.Rhythm.Zones = s.Rhythm.Zones[:4]
	material, e := materializePublicationSource(s, time.UTC)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range material.Prescriptions {
		if rhythmSnapshot(t, p).Segments[0].Blocks[0].WaterReference.CraftCode != "KAYAK" {
			t.Fatal("explicit common choice inferred another craft")
		}
	}
}
func TestRhythmCompleteReferenceFactsBindEmptyPublicationReview(t *testing.T) {
	s := rhythmFixture()
	id := uuid.New()
	s.Header.LatestPublicationID = &id
	s.Header.LatestRevision = 1
	s.EligibleRecipients = []dbgen.ListStructuredTrainingPublicationMembersRow{}
	s.VariationMatches = []publicationVariationTransport{}
	material, e := materializePublicationSource(s, time.UTC)
	if e != nil {
		t.Fatal(e)
	}
	now := s.Header.SourceUpdatedAt.Time
	meta := trainingPublicationReviewMetadata{ActorID: uuid.New(), PlanID: s.Header.PlanID, SourceUpdatedAt: now, LatestPublicationID: id, LatestRevision: 1, ChangeSummary: "Reviewed empty audience", referenceSHA256: material.referenceSHA256}
	signer, _ := newTrainingPublicationReviewSigner()
	token, e := signer.sign(trainingPublicationReviewInput{meta, nil}, now)
	if e != nil {
		t.Fatal(e)
	}
	// Native metadata transports no reference payload/digest; opaque token does.
	posted := meta
	posted.referenceSHA256 = [32]byte{}
	claims, e := signer.verify(token, posted, now)
	if e != nil {
		t.Fatal(e)
	}
	s.Rhythm.Profiles[1].Notes = "New unused reference fact"
	changed, e := materializePublicationSource(s, time.UTC)
	if e != nil {
		t.Fatal(e)
	}
	meta.referenceSHA256 = changed.referenceSHA256
	if e = claims.matches(trainingPublicationReviewInput{meta, nil}, now); !errors.Is(e, errTrainingPublicationReviewChanged) {
		t.Fatal("empty audience failed to bind complete reference facts", e)
	}
}

func TestRhythmStepOverrideUsesChosenProfileAndPreservesLiteralSymbols(t *testing.T) {
	s := rhythmFixture()
	recipient := s.EligibleRecipients[1]
	step := *s.Overview[1].WaterStepID
	change := uuid.New()
	patch := []byte(`{"intensity_code":"R5"}`)
	s.VariationDefinitions = append(s.VariationDefinitions, publicationDefinition{VariationID: change, PlanID: s.Header.PlanID, SubjectKind: dbgen.TrainingVariationSubjectKindWATERSTEP, SubjectID: step, Operation: dbgen.TrainingVariationOperationOVERRIDE, Patch: patch})
	s.VariationMatches = append(s.VariationMatches, publicationVariationTransport{ListTrainingVariationMatchesForManagerRow: dbgen.ListTrainingVariationMatchesForManagerRow{VariationID: change, PlanID: s.Header.PlanID, SessionID: recipient.SessionID, MembershipID: &recipient.MembershipID, SubjectKind: dbgen.TrainingVariationSubjectKindWATERSTEP, SubjectID: step, Operation: dbgen.TrainingVariationOperationOVERRIDE, Priority: 2}, Patch: patch})
	material, err := materializePublicationSource(s, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range material.Prescriptions {
		work := rhythmSnapshot(t, p).Segments[0].Blocks[0].WaterSteps[1]
		if p.MembershipID == recipient.MembershipID {
			if work.IntensityCode != "R5" || !strings.Contains(work.Intensity, "66–78") || strings.Contains(work.Intensity, "40–45") || work.CadenceSPM == nil || *work.CadenceSPM != 70 {
				t.Fatal("step override retained wrong base guidance", work)
			}
		} else if work.IntensityCode != "R2" || !strings.Contains(work.Intensity, "70–80") {
			t.Fatal("step override leaked to common recipient", work)
		}
	}
	for _, literal := range []string{"<60", "Máx", "RP"} {
		zone := dbgen.WaterIntensityZone{CadenceMax: rhythmInt(60), Meaning: literal}
		if got := rhythmZoneCadence(zone); got != literal {
			t.Fatalf("literal %q became %q", literal, got)
		}
	}
}

func TestPublicationRhythmReferencesRequireCanonicalBlockOverride(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*publicationSourceEnvelope)
		want   error
	}{
		{"empty profile", func(s *publicationSourceEnvelope) { s.Overview[0].WaterIntensityProfileID = testUUIDPtr(uuid.Nil) }, errPublicationRhythmReference},
		{"invalid variation JSON", func(s *publicationSourceEnvelope) { s.VariationDefinitions[0].Patch = json.RawMessage(`[1]`) }, errPublicationSourceUnavailable},
		{"profile on a segment", func(s *publicationSourceEnvelope) {
			s.VariationDefinitions[0].SubjectKind = dbgen.TrainingVariationSubjectKindSEGMENT
		}, errPublicationRhythmReference},
		{"numeric profile", func(s *publicationSourceEnvelope) {
			s.VariationDefinitions[0].Patch = json.RawMessage(`{"intensity_profile_id":123}`)
		}, errPublicationRhythmReference},
		{"noncanonical profile", func(s *publicationSourceEnvelope) {
			s.VariationDefinitions[0].Patch = json.RawMessage(`{"intensity_profile_id":"40200000000000000000000000000001"}`)
		}, errPublicationRhythmReference},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := rhythmFixture()
			tc.change(&source)
			if ids, err := publicationRhythmProfileIDs(source); !errors.Is(err, tc.want) || ids != nil {
				t.Fatalf("invalid rhythm reference entered the review: %v, %v", ids, err)
			}
		})
	}
}
