//go:build integration

package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

type rhythmDomainFixture struct {
	*publicationDomainFixture
	kayak, canoe, block, work, second, secondMember, polo uuid.UUID
}

func newRhythmDomainFixture(t *testing.T) rhythmDomainFixture {
	t.Helper()
	f := newPublicationDomainFixture(t)
	w := rhythmDomainFixture{publicationDomainFixture: f, block: uuid.New(), work: uuid.New(), second: uuid.New(), secondMember: uuid.New(), polo: uuid.New()}
	f.exec(t, `INSERT INTO person_sporting_modalities(user_id,modality_code) VALUES($1,'CANOEING')`, f.subject)
	f.exec(t, `INSERT INTO person_canoe_craft_classes(user_id,craft_code) VALUES($1,'K1'),($1,'C1')`, f.subject)
	for _, person := range []uuid.UUID{w.second, w.polo} {
		f.exec(t, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic mixed rhythm athlete',$2,'hash','1990-01-01')`, person, person.String()+"@example.test")
	}
	f.exec(t, `INSERT INTO person_sporting_modalities(user_id,modality_code) VALUES($1,'CANOEING'),($2,'KAYAK_POLO')`, w.second, w.polo)
	f.exec(t, `INSERT INTO person_canoe_craft_classes(user_id,craft_code) VALUES($1,'C4')`, w.second)
	f.exec(t, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,CURRENT_DATE,CURRENT_DATE+300)`, w.secondMember, w.second, f.season, f.programme)
	f.exec(t, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,CURRENT_DATE,CURRENT_DATE+300)`, w.polo, f.season, f.programme)
	f.place(t, w.second, f.group, f.season, f.day(t, 0))
	f.place(t, w.polo, f.group, f.season, f.day(t, 0))
	store := PostgresStructuredTrainingStore{Pool: f.web}
	k, e := store.CreateWaterIntensityProfile(t.Context(), dbgen.CreateWaterIntensityProfileParams{Name: "Synthetic approved kayak reference", Craft: dbgen.PaddlingCraftKAYAK, Notes: "Disposable reference exercise; actual season dates are configurable", CreatedByID: f.actor})
	if e != nil {
		t.Fatal(e)
	}
	w.kayak = k.ID
	c, e := store.CreateWaterIntensityProfile(t.Context(), dbgen.CreateWaterIntensityProfileParams{Name: "Synthetic approved canoe reference", Craft: dbgen.PaddlingCraftCANOE, Notes: "Explicit canoe reference, no age inference", CreatedByID: f.actor})
	if e != nil {
		t.Fatal(e)
	}
	w.canoe = c.ID
	for _, profile := range []uuid.UUID{k.ID, c.ID} {
		limits := [][2]int32{{0, 60}, {70, 80}, {100, 115}, {110, 130}}
		if profile == c.ID {
			limits = [][2]int32{{0, 34}, {40, 45}, {58, 66}, {66, 78}}
		}
		for i, code := range []string{"R0", "R2", "R4", "R5", "R6", "R7"} {
			zone := dbgen.CreateWaterIntensityZoneParams{ProfileID: profile, Code: code, Label: code + " literal guidance", Meaning: "RP"}
			if i < 4 {
				zone.CadenceMin = rhythmInt(limits[i][0])
				zone.CadenceMax = rhythmInt(limits[i][1])
				zone.Meaning = "Exact synthetic stored reference"
				if i == 0 {
					zone.CadenceMin = nil
					zone.Meaning = "<60"
					if profile == c.ID {
						zone.Meaning = "<34"
					}
				}
			}
			if code == "R6" {
				zone.Meaning = "Máx"
			}
			if _, e = store.CreateWaterIntensityZone(t.Context(), zone); e != nil {
				t.Fatal(e)
			}
		}
	}
	segment, parent, recovery := uuid.New(), uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO training_session_segments(id,session_id,position,modality,title) VALUES($1,$2,1,'WATER','Synthetic shared nested water work')`, segment, f.session)
	f.exec(t, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,title,instructions) VALUES($1,$2,1,'MAIN','Shared prescribed work','Identical common content for all crafts')`, w.block, segment)
	f.exec(t, `INSERT INTO water_block_prescriptions(block_id,method) VALUES($1,'INTERVALS')`, w.block)
	f.exec(t, `INSERT INTO water_work_steps(id,block_id,position,kind,name,repeats) VALUES($1,$2,1,'REPEAT_GROUP','Three common repetitions',3)`, parent, w.block)
	f.exec(t, `INSERT INTO water_work_steps(id,block_id,parent_step_id,position,kind,name,duration_seconds,duration_certainty,intensity_code,cadence_spm) VALUES($1,$2,$3,1,'EFFORT','Common work',120,'EXACT','R2',70),($4,$2,$3,2,'EFFORT','Common recovery',60,'EXACT','R0',NULL)`, w.work, w.block, parent, recovery)
	return w
}
func (w rhythmDomainFixture) selectProfiles(t *testing.T) {
	t.Helper()
	w.exec(t, `UPDATE water_block_prescriptions SET intensity_profile_id=$1 WHERE block_id=$2`, w.kayak, w.block)
	_, e := (PostgresStructuredTrainingStore{Pool: w.web}).CreateTrainingVariation(t.Context(), dbgen.CreateTrainingVariationParams{PlanID: w.plan, TargetMembershipID: &w.secondMember, SubjectKind: dbgen.TrainingVariationSubjectKindBLOCK, SubjectID: w.block, Operation: dbgen.TrainingVariationOperationOVERRIDE, ChangeSummary: "Explicit canoe choice keeps common work", Patch: []byte(`{"intensity_profile_id":"` + w.canoe.String() + `"}`), CreatedByID: w.actor})
	if e != nil {
		t.Fatal(e)
	}
}
func TestRhythmActualAdapterExplicitCraftChoiceAndCanonicalPublication(t *testing.T) {
	w := newRhythmDomainFixture(t)
	store := PostgresStructuredTrainingStore{Pool: w.web}
	if _, e := store.ReadPublicationSource(t.Context(), w.actor, w.plan); !errors.Is(e, errPublicationRhythmReference) {
		t.Fatal("typed R2 with zero profile IDs bypassed explicit choice", e)
	}
	w.exec(t, `UPDATE water_block_prescriptions SET intensity_profile_id=$1 WHERE block_id=$2`, w.kayak, w.block)
	if _, e := store.ReadPublicationSource(t.Context(), w.actor, w.plan); !errors.Is(e, errPublicationRhythmPermission) {
		t.Fatal("C4-only athlete silently received common kayak reference", e)
	}
	w.selectProfiles(t)
	source, e := store.ReadPublicationSource(t.Context(), w.actor, w.plan)
	if e != nil {
		t.Fatal(e)
	}
	material, e := materializePublicationSource(source, time.UTC)
	if e != nil || len(material.Prescriptions) != 3 {
		t.Fatal("mixed canonical preview", e)
	}
	var canonicalCommon []byte
	for _, p := range material.Prescriptions {
		session := rhythmSnapshot(t, p)
		block := session.Segments[0].Blocks[0]
		expected := "KAYAK"
		cadence := "70–80"
		if p.AthleteUserID == w.second {
			expected = "CANOE"
			cadence = "40–45"
		}
		if block.WaterReference == nil || block.WaterReference.CraftCode != expected || block.WaterReference.Revision != 1 || !strings.Contains(block.WaterSteps[1].Intensity, cadence) {
			t.Fatal("incorrect selected reference in per-athlete body", block)
		}
		if p.AthleteUserID == w.polo && block.WaterReference.CraftCode != "KAYAK" {
			t.Fatal("explicit Polo sport was forced into canoeing")
		}
		if block.WaterReference.Zones[0].Cadence != block.WaterReference.Zones[0].Meaning || strings.Contains(block.WaterSteps[2].Intensity, "até") {
			t.Fatal("literal one-sided reference turned inclusive")
		}
		// Strip only guidance/variation presentation; every prescribed work field stays identical.
		block.WaterReference = nil
		block.WaterProfile = ""
		for i := range block.WaterSteps {
			block.WaterSteps[i].Intensity = ""
		}
		current, _ := jsonMarshalRhythmWork(block)
		if canonicalCommon == nil {
			canonicalCommon = current
		} else if !bytes.Equal(canonicalCommon, current) {
			t.Fatal("per-craft guidance changed shared prescribed work")
		}
	}
	input := w.review(t, "Mixed explicit craft guidance reviewed")
	saved, e := store.ConfirmPublication(t.Context(), input)
	if e != nil || saved.PrescriptionCount != 3 {
		t.Fatal("actual mixed publication", e)
	}
	old := publicationRowsJSON(t, w.publicationDomainFixture, saved.PublicationID)
	next, e := store.CreateWaterIntensityProfile(t.Context(), dbgen.CreateWaterIntensityProfileParams{Name: "Synthetic approved kayak reference", Craft: dbgen.PaddlingCraftKAYAK, Notes: "Explicit new revision", CreatedByID: w.actor})
	if e != nil || next.Revision != 2 || next.SupersedesID == nil || *next.SupersedesID != w.kayak {
		t.Fatal("new profile revision", e)
	}
	if _, e = store.ReadPublicationSource(t.Context(), w.actor, w.plan); !errors.Is(e, errPublicationRhythmReference) {
		t.Fatal("inactive selected profile silently retargeted to latest", e)
	}
	if old != publicationRowsJSON(t, w.publicationDomainFixture, saved.PublicationID) {
		t.Fatal("profile revision rewrote existing canonical publication")
	}
}
func jsonMarshalRhythmWork(block pages.StructuredTrainingBlock) ([]byte, error) {
	return json.Marshal(block)
}

func TestRhythmActualReferenceAndSportingChangesInvalidateReviewWithoutWrites(t *testing.T) {
	w := newRhythmDomainFixture(t)
	w.selectProfiles(t)
	store := PostgresStructuredTrainingStore{Pool: w.web}
	reviewed := w.review(t, "Complete references before unused literal edit")
	w.exec(t, `UPDATE water_intensity_zones SET meaning='Synthetic changed unused RP reference' WHERE profile_id=$1 AND code='R7'`, w.kayak)
	if _, err := store.ConfirmPublication(t.Context(), reviewed); !errors.Is(err, errStructuredTrainingPublicationConflict) {
		t.Fatalf("unused reference edit did not stale review: %v", err)
	}
	assertRhythmNoPublications(t, w)
	reviewed = w.review(t, "Current complete sporting permission facts")
	// The selected K profile remains permitted. The unused C permission still belongs to the reviewed facts.
	w.exec(t, `DELETE FROM person_canoe_craft_classes WHERE user_id=$1 AND craft_code='C1'`, w.subject)
	if _, err := store.ConfirmPublication(t.Context(), reviewed); !errors.Is(err, errStructuredTrainingPublicationConflict) {
		t.Fatalf("sporting permission edit did not stale review: %v", err)
	}
	assertRhythmNoPublications(t, w)
	if result, err := store.ConfirmPublication(t.Context(), w.review(t, "Explicit fresh review after both changes")); err != nil || result.PrescriptionCount != 3 {
		t.Fatal("fresh review failed", err)
	}
}
func assertRhythmNoPublications(t *testing.T, w rhythmDomainFixture) {
	t.Helper()
	var count int
	if err := w.web.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM training_plan_publications WHERE plan_id=$1)+(SELECT count(*) FROM training_prescriptions WHERE publication_id IN(SELECT id FROM training_plan_publications WHERE plan_id=$1))`, w.plan).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected review wrote publication/prescription rows count=%d err=%v", count, err)
	}
}
func TestRhythmActualDatedCrewCreationAndWholePeriodRefusal(t *testing.T) {
	w := newRhythmDomainFixture(t)
	store := PostgresStructuredTrainingStore{Pool: w.web}
	code := "C2"
	in := StructuredVariationGroupInput{Params: dbgen.CreateTrainingVariationGroupParams{TrainingGroupID: w.group, Name: "Synthetic dated canoe crew", Kind: dbgen.TrainingVariationGroupKindCREW, CraftCode: &code, EffectiveFrom: placementDate(w.day(t, 0)), EffectiveUntil: placementDate(w.day(t, 2)), CreatedByID: w.actor}, MembershipIDs: []uuid.UUID{w.membership, w.secondMember}}
	created, err := store.CreateTrainingVariationGroup(t.Context(), in)
	if err != nil || created.ID == uuid.Nil {
		t.Fatal("actual dated crew creation", err)
	}
	var count int
	if err = w.web.QueryRow(t.Context(), `SELECT count(*) FROM training_variation_group_members WHERE variation_group_id=$1`, created.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("atomic crew members=%d error=%v", count, err)
	}
	var secondPlacement uuid.UUID
	if err = w.web.QueryRow(t.Context(), `SELECT placement_id FROM training_placement_resolve($1,$2,$3,$4)`, w.second, w.group, w.season, placementDate(w.day(t, 0))).Scan(&secondPlacement); err != nil {
		t.Fatal(err)
	}
	w.changePlacement(t, trainingPlacementCommand{SubjectID: w.second, ContextGroupID: w.group, Kind: placementLeave, SourcePlacementID: secondPlacement, EffectiveOn: w.day(t, 2)})
	in.Params.Name = "Synthetic crossing departure must not persist"
	in.Params.EffectiveUntil = placementDate(w.day(t, 3))
	before := rhythmCrewAggregate(t, w)
	if _, err = store.CreateTrainingVariationGroup(t.Context(), in); !errors.Is(err, errStructuredVariationMemberScope) {
		t.Fatal("crew spanning departure accepted", err)
	}
	if after := rhythmCrewAggregate(t, w); before != after {
		t.Fatal("rejected crew left partial group/member rows")
	}
	in.Params.Kind = dbgen.TrainingVariationGroupKindSUBGROUP
	in.Params.CraftCode = nil
	in.Params.Name = "Synthetic missing member must not persist"
	in.MembershipIDs = []uuid.UUID{w.membership, uuid.New()}
	if _, err = store.CreateTrainingVariationGroup(t.Context(), in); !errors.Is(err, errStructuredVariationMemberScope) {
		t.Fatal("missing member accepted", err)
	}
	if after := rhythmCrewAggregate(t, w); before != after {
		t.Fatal("invalid member left partial group/member rows")
	}
}
func rhythmCrewAggregate(t *testing.T, w rhythmDomainFixture) string {
	t.Helper()
	var value string
	if err := w.web.QueryRow(t.Context(), `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(g) ORDER BY g.id) FROM training_variation_groups g WHERE training_group_id=$1),(SELECT jsonb_agg(to_jsonb(m) ORDER BY m.variation_group_id,m.membership_id) FROM training_variation_group_members m JOIN training_variation_groups g ON g.id=m.variation_group_id WHERE g.training_group_id=$1))::text`, w.group).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
