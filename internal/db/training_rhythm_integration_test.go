//go:build integration

package db

import (
	"context"
	"strings"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func rhythmFloor(t *testing.T, w placementFixture) {
	t.Helper()
	state, err := TrainingScheduleStatus(t.Context(), w.f.low)
	if err != nil {
		t.Fatal(err)
	}
	var epoch int64
	if err = w.f.low.QueryRow(t.Context(), `SELECT training_schedule_control.commit_floor($1,$2,$3,$4,5)`, state.DatabaseIncarnation, state.StateEpoch, uuid.New(), strings.Repeat("e", 64)).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
}
func TestTrainingRhythmActualRuntimeAndFloor(t *testing.T) {
	w := newPlacementFixture(t)
	if _, err := VerifyTrainingRhythmRuntime(t.Context(), w.web, TrainingScheduleCapabilities{ReaderVersion: 5, WriterVersion: 2}); err == nil {
		t.Fatal("floor4 admitted new rhythm semantic")
	}
	rhythmFloor(t, w)
	if _, err := VerifyTrainingRhythmRuntime(t.Context(), w.web, TrainingScheduleCapabilities{ReaderVersion: 5, WriterVersion: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTrainingRhythmRuntime(t.Context(), w.web, TrainingScheduleCapabilities{ReaderVersion: 4, WriterVersion: 2}); err == nil {
		t.Fatal("Reader4 admitted new rhythm semantic")
	}
	t.Run("function definition drift", func(t *testing.T) {
		tx, err := w.f.low.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(t.Context())
		recipientSchemaExec(t, tx, `ALTER FUNCTION public.guard_training_rhythm_variation() SET search_path=pg_catalog`)
		if err = verifyTrainingRhythmDefinition(t.Context(), tx); err == nil || !strings.Contains(err.Error(), "training rhythm function contract mismatch: guard_training_rhythm_variation") {
			t.Fatalf("drifted guard function accepted: %v", err)
		}
	})
	recipientSchemaExec(t, w.f.low, `DROP TRIGGER water_intensity_zones_rhythm_parent_witness ON public.water_intensity_zones`)
	if _, err := VerifyTrainingRhythmRuntime(t.Context(), w.web, TrainingScheduleCapabilities{ReaderVersion: 5, WriterVersion: 2}); err == nil {
		t.Fatal("missing actual first-zone witness admitted")
	}
}
func TestTrainingRhythmCompleteFactsAndPlanWitnesses(t *testing.T) {
	w := newPlacementFixture(t)
	rhythmFloor(t, w)
	joined, err := w.apply(t, w.input(t, "JOIN", nil, &w.groupA, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	instruction := w.instruction(t, w.groupA, joined.DestinationPlacementID, 3)
	q := dbgen.New(w.web)
	k, err := q.CreateWaterIntensityProfile(t.Context(), dbgen.CreateWaterIntensityProfileParams{Name: "Synthetic exact kayak reference", Craft: dbgen.PaddlingCraftKAYAK, Notes: "Synthetic supplied observation, not a production seed", CreatedByID: w.actor})
	if err != nil {
		t.Fatal(err)
	}
	c, err := q.CreateWaterIntensityProfile(t.Context(), dbgen.CreateWaterIntensityProfileParams{Name: "Synthetic exact canoe reference", Craft: dbgen.PaddlingCraftCANOE, Notes: "Distinct synthetic reference", CreatedByID: w.actor})
	if err != nil {
		t.Fatal(err)
	}
	for _, zone := range []dbgen.CreateWaterIntensityZoneParams{
		{ProfileID: k.ID, Code: "R0", Label: "Ritmo zero", CadenceMax: graphPtr(int32(60)), Meaning: "<60"},
		{ProfileID: k.ID, Code: "R4", Label: "Ritmo quatro", CadenceMin: graphPtr(int32(100)), CadenceMax: graphPtr(int32(115)), Meaning: "100–115"},
		{ProfileID: k.ID, Code: "R5", Label: "Ritmo cinco", CadenceMin: graphPtr(int32(110)), CadenceMax: graphPtr(int32(130)), Meaning: "110–130"},
		{ProfileID: k.ID, Code: "R6", Label: "Ritmo seis", Meaning: "Máx"},
		{ProfileID: k.ID, Code: "R7", Label: "Ritmo sete", Meaning: "RP"},
		{ProfileID: c.ID, Code: "R4", Label: "Ritmo quatro", CadenceMin: graphPtr(int32(58)), CadenceMax: graphPtr(int32(66)), Meaning: "58–66"},
	} {
		if _, err = q.CreateWaterIntensityZone(t.Context(), zone); err != nil {
			t.Fatal(err)
		}
	}
	segment, block := uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_segments(id,session_id,position,modality,title) VALUES($1,$2,1,'WATER','Synthetic shared work')`, segment, instruction.session)
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,title,instructions) VALUES($1,$2,1,'MAIN','Synthetic common targets','Synthetic shared work instructions')`, block, segment)
	recipientSchemaExec(t, w.web, `INSERT INTO water_block_prescriptions(block_id,method,intensity_profile_id,target_distance_metres,target_distance_certainty) VALUES($1,'INTERVALS',$2,8000,'EXACT')`, block, k.ID)
	recipientSchemaExec(t, w.web, `INSERT INTO water_work_steps(block_id,position,kind,name,duration_seconds,duration_certainty,intensity_code) VALUES($1,1,'EFFORT','Synthetic shared effort',300,'EXACT','R4')`, block)
	if _, err = q.CreateTrainingVariation(t.Context(), dbgen.CreateTrainingVariationParams{PlanID: instruction.plan, TargetMembershipID: &instruction.membership, SubjectKind: dbgen.TrainingVariationSubjectKindBLOCK, SubjectID: block, Operation: dbgen.TrainingVariationOperationOVERRIDE, ChangeSummary: "Synthetic explicit canoe reference", Patch: []byte(`{"intensity_profile_id":"` + c.ID.String() + `"}`), CreatedByID: w.actor}); err != nil {
		t.Fatal("supported dated athlete variation", err)
	}
	profiles, err := q.ListPublicationRhythmProfiles(t.Context(), dbgen.ListPublicationRhythmProfilesParams{ActorID: w.actor, PlanID: instruction.plan, ProfileIds: []uuid.UUID{k.ID, c.ID}})
	if err != nil || len(profiles) != 2 {
		t.Fatalf("complete explicit profiles %+v %v", profiles, err)
	}
	zones, err := q.ListPublicationRhythmZones(t.Context(), dbgen.ListPublicationRhythmZonesParams{ActorID: w.actor, PlanID: instruction.plan, ProfileIds: []uuid.UUID{k.ID, c.ID}})
	if err != nil || len(zones) != 6 {
		t.Fatalf("complete unused/used reference zones %+v %v", zones, err)
	}
	var strict, maximum, race bool
	for _, zone := range zones {
		if zone.ProfileID == k.ID && zone.Code == "R0" {
			strict = zone.CadenceMin == nil && zone.CadenceMax != nil && *zone.CadenceMax == 60 && zone.Meaning == "<60"
		}
		if zone.Code == "R6" {
			maximum = zone.CadenceMin == nil && zone.CadenceMax == nil && zone.Meaning == "Máx"
		}
		if zone.Code == "R7" {
			race = zone.Meaning == "RP"
		}
	}
	if !strict || !maximum || !race {
		t.Fatal("literal open/maximum/race guidance normalized")
	}
	version := func() pgtype.Timestamptz {
		var v pgtype.Timestamptz
		if e := w.web.QueryRow(t.Context(), `SELECT updated_at FROM training_plans WHERE id=$1`, instruction.plan).Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	old := version()
	// This profile is selected only by BLOCK override, not the common water row.
	if _, err = q.CreateWaterIntensityZone(t.Context(), dbgen.CreateWaterIntensityZoneParams{ProfileID: c.ID, Code: "R7", Label: "Ritmo sete", Meaning: "RP"}); err != nil {
		t.Fatal(err)
	}
	if !version().Time.After(old.Time) {
		t.Fatal("first/unused override zone did not stale plan")
	}
	old = version()
	recipientSchemaExec(t, w.web, `UPDATE water_intensity_profiles SET notes='Synthetic revised reference note' WHERE id=$1`, c.ID)
	if !version().Time.After(old.Time) {
		t.Fatal("selected override profile change did not stale plan")
	}
	recipientSchemaExec(t, w.web, `INSERT INTO person_sporting_modalities(user_id,modality_code) VALUES($1,'CANOEING')`, w.subject)
	facts, err := q.PublicationRecipientCrafts(t.Context(), dbgen.PublicationRecipientCraftsParams{ActorID: w.actor, PlanID: instruction.plan})
	if err != nil || len(facts) != 1 || facts[0].Kayak || facts[0].Canoe {
		t.Fatalf("missing craft inferred from group/age/default %+v %v", facts, err)
	}
	old = version()
	head := w.revision(t)
	recipientSchemaExec(t, w.web, `INSERT INTO person_canoe_craft_classes(user_id,craft_code) VALUES($1,'K1'),($1,'C1')`, w.subject)
	facts, err = q.PublicationRecipientCrafts(t.Context(), dbgen.PublicationRecipientCraftsParams{ActorID: w.actor, PlanID: instruction.plan})
	if err != nil || len(facts) != 1 || !facts[0].Kayak || !facts[0].Canoe {
		t.Fatalf("both craft flags %+v %v", facts, err)
	}
	if !version().Time.After(old.Time) || w.revision(t) <= head {
		t.Fatal("sporting permission failed physical plan/head witness")
	}
	recipientSchemaExec(t, w.web, `DELETE FROM person_canoe_craft_classes WHERE user_id=$1 AND craft_code='K1'`, w.subject)
	facts, err = q.PublicationRecipientCrafts(t.Context(), dbgen.PublicationRecipientCraftsParams{ActorID: w.actor, PlanID: instruction.plan})
	if err != nil || len(facts) != 1 || facts[0].Kayak || !facts[0].Canoe {
		t.Fatalf("canonical canoe-only flags %+v %v", facts, err)
	}
	recipientSchemaExec(t, w.web, `DELETE FROM person_canoe_craft_classes WHERE user_id=$1 AND craft_code='C1'`, w.subject)
	recipientSchemaExec(t, w.web, `INSERT INTO person_canoe_craft_classes(user_id,craft_code) VALUES($1,'C4')`, w.subject)
	facts, err = q.PublicationRecipientCrafts(t.Context(), dbgen.PublicationRecipientCraftsParams{ActorID: w.actor, PlanID: instruction.plan})
	if err != nil || len(facts) != 1 || facts[0].Kayak || !facts[0].Canoe {
		t.Fatalf("C4 only must permit CANOE and refuse KAYAK family %+v %v", facts, err)
	}
	// Actual Polo modality is an explicit KAYAK permission with no Canoeing class.
	recipientSchemaExec(t, w.web, `DELETE FROM person_sporting_modalities WHERE user_id=$1 AND modality_code='CANOEING'`, w.subject)
	recipientSchemaExec(t, w.web, `INSERT INTO person_sporting_modalities(user_id,modality_code) VALUES($1,'KAYAK_POLO')`, w.subject)
	facts, err = q.PublicationRecipientCrafts(t.Context(), dbgen.PublicationRecipientCraftsParams{ActorID: w.actor, PlanID: instruction.plan})
	if err != nil || len(facts) != 1 || !facts[0].Kayak || facts[0].Canoe {
		t.Fatalf("Polo-only actual modality must permit KAYAK without Canoeing %+v %v", facts, err)
	}
	permissionReader := w.f.web(t)
	permissionTx, e := permissionReader.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = permissionTx.Exec(t.Context(), `SELECT id FROM users WHERE id=$1 FOR SHARE NOWAIT`, w.subject); e != nil {
		t.Fatal(e)
	}
	permissions, e := dbgen.New(permissionTx).LockPublicationRecipientCrafts(t.Context(), dbgen.LockPublicationRecipientCraftsParams{ActorID: w.actor, PlanID: instruction.plan})
	if e != nil || len(permissions) != 1 || permissions[0].PermissionKind != "MODALITY" || permissions[0].PermissionCode != "KAYAK_POLO" {
		t.Fatalf("actual typed permission lock %+v %v", permissions, e)
	}
	permissionWriter := w.f.web(t)
	recipientSchemaExec(t, permissionWriter, `SET lock_timeout='100ms'`)
	if _, e = permissionWriter.Exec(t.Context(), `INSERT INTO person_sporting_modalities(user_id,modality_code) VALUES($1,'CANOEING')`, w.subject); !sqlState(e, "55P03") {
		t.Fatalf("first modality absence was not fenced by subject lock: %v", e)
	}
	if _, e = permissionWriter.Exec(t.Context(), `DELETE FROM person_sporting_modalities WHERE user_id=$1 AND modality_code='KAYAK_POLO'`, w.subject); !sqlState(e, "55P03") {
		t.Fatalf("Polo revocation not fenced by actual late locks: %v", e)
	}
	if e = permissionTx.Rollback(t.Context()); e != nil {
		t.Fatal(e)
	}
	// Selected profile SHARE admits no concurrent zone edit, including first INSERT.
	tx, err := w.web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = dbgen.New(tx).LockPublicationRhythmProfiles(t.Context(), dbgen.LockPublicationRhythmProfilesParams{ActorID: w.actor, PlanID: instruction.plan, ProfileIds: []uuid.UUID{c.ID, k.ID}}); err != nil {
		t.Fatal(err)
	}
	writer := w.f.web(t)
	recipientSchemaExec(t, writer, `SET lock_timeout='100ms'`)
	_, err = writer.Exec(t.Context(), `INSERT INTO water_intensity_zones(profile_id,position,code,label,meaning) VALUES($1,99,'R9','Synthetic first new zone','Synthetic new zone')`, c.ID)
	if !sqlState(err, "55P03") {
		t.Fatalf("first-zone missing parent witness: %v", err)
	}
}

func TestTrainingRhythmPatchFloorScopeRevisionAndFrozenBody(t *testing.T) {
	w := newPlacementFixture(t)
	joined, err := w.apply(t, w.input(t, "JOIN", nil, &w.groupA, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	instruction := w.instruction(t, w.groupA, joined.DestinationPlacementID, 3)
	q := dbgen.New(w.web)
	profile, err := q.CreateWaterIntensityProfile(t.Context(), dbgen.CreateWaterIntensityProfileParams{Name: "Synthetic fixed reference revision", Craft: dbgen.PaddlingCraftKAYAK, Notes: "Synthetic revision one", CreatedByID: w.actor})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.CreateWaterIntensityZone(t.Context(), dbgen.CreateWaterIntensityZoneParams{ProfileID: profile.ID, Code: "R2", Label: "Synthetic R2", CadenceMin: graphPtr(int32(70)), CadenceMax: graphPtr(int32(80)), Meaning: "70–80"}); err != nil {
		t.Fatal(err)
	}
	segment, block := uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_segments(id,session_id,position,modality,title) VALUES($1,$2,1,'WATER','Synthetic common water')`, segment, instruction.session)
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,title,instructions) VALUES($1,$2,1,'MAIN','Synthetic shared targets','Synthetic common targets unchanged')`, block, segment)
	recipientSchemaExec(t, w.web, `INSERT INTO water_block_prescriptions(block_id,method,intensity_profile_id,target_distance_metres,target_distance_certainty) VALUES($1,'INTERVALS',$2,8000,'EXACT')`, block, profile.ID)
	patch := func(subject uuid.UUID, raw string) error {
		_, e := w.web.Exec(t.Context(), `INSERT INTO training_variations(plan_id,target_membership_id,subject_kind,subject_id,operation,change_summary,patch,created_by_id) VALUES($1,$2,'BLOCK',$3,'OVERRIDE','Synthetic explicit reference choice',$4::jsonb,$5)`, instruction.plan, instruction.membership, subject, raw, w.actor)
		return e
	}
	valid := `{"intensity_profile_id":"` + profile.ID.String() + `"}`
	if err = patch(block, valid); !sqlState(err, "55000") {
		t.Fatalf("new patch admitted at actualfloor4: %v", err)
	}
	rhythmFloor(t, w)
	for _, bad := range []string{`{"intensity_profile_id":null}`, `{"intensity_profile_id":23}`, `{"intensity_profile_id":"not-a-uuid"}`} {
		if err = patch(block, bad); !sqlState(err, "23514") {
			t.Fatalf("malformed typedprofile patch accepted %s: %v", bad, err)
		}
	}
	if err = patch(uuid.New(), valid); !sqlState(err, "23514") {
		t.Fatalf("nonwater/outofplan block accepted: %v", err)
	}
	if err = patch(block, valid); err != nil {
		t.Fatal(err)
	}
	var oldBody string
	if err = w.web.QueryRow(t.Context(), `SELECT snapshot::text FROM training_prescriptions WHERE id=$1`, instruction.prescription).Scan(&oldBody); err != nil {
		t.Fatal(err)
	}
	revised, err := q.CreateWaterIntensityProfile(t.Context(), dbgen.CreateWaterIntensityProfileParams{Name: profile.Name, Craft: profile.Craft, Notes: "Synthetic revision two", CreatedByID: w.actor})
	if err != nil || revised.Revision != 2 || revised.SupersedesID == nil || *revised.SupersedesID != profile.ID {
		t.Fatalf("exact explicit revision %+v %v", revised, err)
	}
	oldFacts, err := q.ListPublicationRhythmProfiles(t.Context(), dbgen.ListPublicationRhythmProfilesParams{ActorID: w.actor, PlanID: instruction.plan, ProfileIds: []uuid.UUID{profile.ID}})
	if err != nil || len(oldFacts) != 1 || oldFacts[0].IsActive {
		t.Fatalf("inactive selected reference silently defaulted %+v %v", oldFacts, err)
	}
	var selected uuid.UUID
	if err = w.web.QueryRow(t.Context(), `SELECT intensity_profile_id FROM water_block_prescriptions WHERE block_id=$1`, block).Scan(&selected); err != nil || selected != profile.ID {
		t.Fatal("draft oldreference silently retargeted", err)
	}
	var still string
	if err = w.web.QueryRow(t.Context(), `SELECT snapshot::text FROM training_prescriptions WHERE id=$1`, instruction.prescription).Scan(&still); err != nil || still != oldBody {
		t.Fatal("profile revision rewrote frozen old instruction", err)
	}
	if _, err = w.web.Exec(t.Context(), `DELETE FROM water_intensity_profiles WHERE id=$1`, profile.ID); !sqlState(err, "23503") {
		t.Fatalf("referenced profile deletion accepted: %v", err)
	}
	// An unrelated actor never obtains a reference directory through plan scope.
	missingActor := uuid.New()
	if rows, e := q.ListPublicationRhythmProfiles(t.Context(), dbgen.ListPublicationRhythmProfilesParams{ActorID: missingActor, PlanID: instruction.plan, ProfileIds: []uuid.UUID{profile.ID, revised.ID}}); e != nil || len(rows) != 0 {
		t.Fatalf("unscoped reference read %+v %v", rows, e)
	}
}

func TestTrainingRhythmDatedNativeVariationChoicesAndCrew(t *testing.T) {
	w := newPlacementFixture(t)
	rhythmFloor(t, w)
	joined, err := w.apply(t, w.input(t, "JOIN", nil, &w.groupA, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	instruction := w.instruction(t, w.groupA, joined.DestinationPlacementID, 3)
	segment, block := uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO training_session_segments(id,session_id,position,modality,title) VALUES($1,$2,1,'WATER','Synthetic common water')`, segment, instruction.session)
	recipientSchemaExec(t, w.web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,title,instructions) VALUES($1,$2,1,'MAIN','Synthetic shared targets','Synthetic unchanged shared work')`, block, segment)
	recipientSchemaExec(t, w.web, `INSERT INTO water_block_prescriptions(block_id,method) VALUES($1,'INTERVALS')`, block)
	future, member := uuid.New(), uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic future-only variation athlete',$2,'hash','1990-01-01')`, future, future.String()+"@example.test")
	recipientSchemaExec(t, w.web, `INSERT INTO user_memberships(id,user_id,programme_id,season_id,starts_on,ends_on) VALUES($1,$2,$3,$4,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+2,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date+100)`, member, future, w.programme, w.season)
	fw := w
	fw.subject = future
	futureJoin, err := fw.apply(t, fw.input(t, "JOIN", nil, &w.groupA, 2, 0))
	if err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(w.web)
	unrelated := uuid.New()
	recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic unrelated weekly load editor',$2,'hash','1990-01-01')`, unrelated, unrelated.String()+"@example.test")
	if n, e := q.UpdateStructuredTrainingWeekLoad(t.Context(), dbgen.UpdateStructuredTrainingWeekLoadParams{PlanID: instruction.plan, UserID: w.actor, IsAdmin: true, PlannedLoadPercentage: graphPtr(int16(80))}); e != nil || n != 1 {
		t.Fatalf("authorized weeklyload using original UserID API %d: %v", n, e)
	}
	if n, e := q.UpdateStructuredTrainingWeekLoad(t.Context(), dbgen.UpdateStructuredTrainingWeekLoadParams{PlanID: instruction.plan, UserID: unrelated, IsAdmin: true, PlannedLoadPercentage: graphPtr(int16(90))}); e != nil || n != 0 {
		t.Fatalf("unrelated forgedadmin weeklyload authority %d: %v", n, e)
	}
	var load int16
	if e := w.web.QueryRow(t.Context(), `SELECT planned_load_percentage FROM training_plans WHERE id=$1`, instruction.plan).Scan(&load); e != nil || load != 80 {
		t.Fatalf("refused weeklyload changed facts %d: %v", load, e)
	}
	choices, err := q.ListManagedTrainingGroupMembers(t.Context(), dbgen.ListManagedTrainingGroupMembersParams{UserID: w.actor, IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, choice := range choices {
		if choice.MembershipID == member && choice.TrainingGroupID == w.groupA {
			found = true
		}
	}
	if !found {
		t.Fatal("actual future-only plan/session episode absent from native variation choices")
	}
	individual := dbgen.CreateTrainingVariationParams{PlanID: instruction.plan, TargetMembershipID: &member, SubjectKind: dbgen.TrainingVariationSubjectKindBLOCK, SubjectID: block, Operation: dbgen.TrainingVariationOperationOVERRIDE, ChangeSummary: "Synthetic date-specific athlete variation", Patch: []byte(`{"instructions":"Synthetic explicit personal work"}`), CreatedByID: w.actor}
	unauthorized := individual
	unauthorized.CreatedByID = unrelated
	if _, e := q.CreateTrainingVariation(t.Context(), unauthorized); e != pgx.ErrNoRows {
		t.Fatalf("unrelated actor created private dated variation: %v", e)
	}
	if _, err = q.CreateTrainingVariation(t.Context(), individual); err != nil {
		t.Fatal("supported actual future-session variation", err)
	}
	group, err := q.CreateTrainingVariationGroup(t.Context(), dbgen.CreateTrainingVariationGroupParams{TrainingGroupID: w.groupA, Name: "Synthetic dated subgroup", Kind: dbgen.TrainingVariationGroupKindSUBGROUP, EffectiveFrom: w.day(t, 2), EffectiveUntil: w.day(t, 4), CreatedByID: w.actor})
	if err != nil {
		t.Fatal(err)
	}
	if n, e := q.AddTrainingVariationGroupMember(t.Context(), dbgen.AddTrainingVariationGroupMemberParams{VariationGroupID: group.ID, MembershipID: member, AddedByID: w.actor}); e != nil || n != 1 {
		t.Fatalf("dated crew/subgroup membership %d %v", n, e)
	}
	groupInput := individual
	groupInput.TargetMembershipID = nil
	groupInput.TargetGroupID = &group.ID
	groupInput.ChangeSummary = "Synthetic dated subgroup variation"
	if _, err = q.CreateTrainingVariation(t.Context(), groupInput); err != nil {
		t.Fatal(err)
	}
	matches, err := q.ListTrainingVariationMatchesForManager(t.Context(), dbgen.ListTrainingVariationMatchesForManagerParams{UserID: w.actor, IsAdmin: true, TimeZone: "Europe/Lisbon"})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, match := range matches {
		if match.PlanID == instruction.plan && match.MembershipID != nil && *match.MembershipID == member {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("dated individual+group native matches %d", count)
	}
	// A real later move truncates the source; start+end group identity cannot
	// smuggle a crew interval across an intervening departure.
	if _, err = fw.apply(t, fw.input(t, "MOVE", &futureJoin.DestinationPlacementID, &w.groupB, 5, 0)); err != nil {
		t.Fatal(err)
	}
	tooLong, err := q.CreateTrainingVariationGroup(t.Context(), dbgen.CreateTrainingVariationGroupParams{TrainingGroupID: w.groupA, Name: "Synthetic invalid crossing subgroup", Kind: dbgen.TrainingVariationGroupKindSUBGROUP, EffectiveFrom: w.day(t, 2), EffectiveUntil: w.day(t, 6), CreatedByID: w.actor})
	if err != nil {
		t.Fatal(err)
	}
	if n, e := q.AddTrainingVariationGroupMember(t.Context(), dbgen.AddTrainingVariationGroupMemberParams{VariationGroupID: tooLong.ID, MembershipID: member, AddedByID: w.actor}); e != nil || n != 0 {
		t.Fatalf("crew period crossed actual departure %d %v", n, e)
	}
	recipientSchemaExec(t, w.web, `UPDATE users SET is_active=false WHERE id=$1`, future)
	choices, err = q.ListManagedTrainingGroupMembers(t.Context(), dbgen.ListManagedTrainingGroupMembersParams{UserID: w.actor, IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range choices {
		if choice.MembershipID == member {
			t.Fatal("inactive subject leaked native choice")
		}
	}
	matches, err = q.ListTrainingVariationMatchesForManager(t.Context(), dbgen.ListTrainingVariationMatchesForManagerParams{UserID: w.actor, IsAdmin: true, TimeZone: "Europe/Lisbon"})
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range matches {
		if match.MembershipID != nil && *match.MembershipID == member {
			t.Fatal("inactive subject leaked native variation match")
		}
	}
	crewRows, e := q.ListManagedTrainingVariationGroups(t.Context(), dbgen.ListManagedTrainingVariationGroupsParams{UserID: w.actor, IsAdmin: true})
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range crewRows {
		if row.MembershipID == member {
			t.Fatal("inactive subject leaked native crew group label")
		}
	}
	// IsAdmin is a nonauthorizing compatibility parameter, never scope authority.
	if choices, err = q.ListManagedTrainingGroupMembers(t.Context(), dbgen.ListManagedTrainingGroupMembersParams{UserID: uuid.New(), IsAdmin: true}); err != nil || len(choices) != 0 {
		t.Fatalf("forgedadmin selector leaked choices %+v %v", choices, err)
	}
}
