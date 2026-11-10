package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"html"
	"math"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func focusedEditFixture(t *testing.T) focusedEditSource {
	t.Helper()
	c, v := focusedGraphWaterExample()
	p := normalizedGraphExample(t, c, v)
	allocateFocusedGraphIDs(&p, nil)
	stamp := pgtype.Timestamptz{Time: c.PlanUpdatedAt, Valid: true}
	day, _ := focusedDay(c.Day)
	raw := focusedEditRaw{Session: dbgen.GetFocusedWorkoutSessionRow{ID: p.SessionID, PlanID: c.PlanID, Title: p.Title, Description: p.Description, StartsAt: pgtype.Timestamptz{Time: *p.Schedule.StartsAt, Valid: true}, EntryKind: p.Kind, Status: "ACTIVE", CreatedByID: uuid.New(), CreatedAt: stamp, UpdatedAt: stamp, SchedulePrecision: string(p.Schedule.Precision), ScheduledOn: day}}
	for ai, a := range p.Activities {
		raw.Segments = append(raw.Segments, dbgen.TrainingSessionSegment{ID: a.ID, SessionID: p.SessionID, Position: int32(ai + 1), Modality: a.Modality, Title: a.Title, Location: a.Location, EquipmentNotes: a.Equipment, CreatedAt: stamp, UpdatedAt: stamp})
		for bi, b := range a.Phases {
			raw.Blocks = append(raw.Blocks, dbgen.TrainingSegmentBlock{ID: b.ID, SegmentID: a.ID, Position: int32(bi + 1), Purpose: b.Purpose, Title: b.Title, Instructions: b.Instructions, CreatedAt: stamp, UpdatedAt: stamp})
			if b.Water != nil {
				w := b.Water
				raw.Water = append(raw.Water, dbgen.WaterBlockPrescription{BlockID: b.ID, Method: w.Method, IntensityProfileID: w.ProfileID, TargetDistanceMetres: w.TargetDistance, TargetDistanceCertainty: w.TargetCertainty, CreatedAt: stamp, UpdatedAt: stamp})
				ids := map[string]uuid.UUID{}
				positions := map[string]int32{}
				for _, n := range w.Nodes {
					ids[n.Key] = n.ID
				}
				for _, n := range w.Nodes {
					positions[n.ParentKey]++
					var parent *uuid.UUID
					if n.ParentKey != "" {
						parent = focusedPointer(ids[n.ParentKey])
					}
					raw.Steps = append(raw.Steps, dbgen.WaterWorkStep{ID: n.ID, BlockID: b.ID, ParentStepID: parent, Position: positions[n.ParentKey], Kind: n.Kind, Name: n.Name, Repeats: n.Repeats, DurationSeconds: n.Duration, DurationCertainty: n.DurationCertainty, DistanceMetres: n.Distance, DistanceCertainty: n.DistanceCertainty, RecoverySeconds: n.Recovery, IntensityCode: n.Intensity, Instructions: n.Instructions, CreatedAt: stamp, UpdatedAt: stamp})
				}
			}
		}
	}
	return focusedEditSource{Context: c, Raw: raw, Graph: p, DecisionAt: c.WeekStart.Add(24 * time.Hour)}
}
func TestFocusedEditRawDigestSeparatesPlanReferencesAndOutcomes(t *testing.T) {
	source := focusedEditFixture(t)
	baseline, err := focusedEditBaselineFor(source)
	if err != nil {
		t.Fatal(err)
	}
	other := source
	other.Context.PlanUpdatedAt = other.Context.PlanUpdatedAt.Add(time.Hour)
	other.Raw.Session.HasOutcomes = true
	other.Graph.References.Profiles = append(other.Graph.References.Profiles, dbgen.WaterIntensityProfile{ID: uuid.New()})
	if err = baseline.matches(other); err != nil {
		t.Fatal("plan/reference/outcome drift misclassified as own graph change", err)
	}
	changed := source
	changed.Raw.Steps = append([]dbgen.WaterWorkStep(nil), source.Raw.Steps...)
	changed.Raw.Steps[1].Instructions = "Concurrent own change"
	if err = baseline.matches(changed); !errors.Is(err, errFocusedEditSourceChanged) {
		t.Fatal("child-only change not detected", err)
	}
	changed = source
	changed.Raw.Session.UpdatedAt.Time = changed.Raw.Session.UpdatedAt.Time.Add(time.Microsecond)
	if err = baseline.matches(changed); !errors.Is(err, errFocusedEditSourceChanged) {
		t.Fatal("session version drift accepted", err)
	}
	changed = source
	changed.Raw.Session.ModalityID = focusedPointer(uuid.New())
	if err = baseline.matches(changed); !errors.Is(err, errFocusedEditSourceChanged) {
		t.Fatal("unsupported opaque metadata omitted from source digest", err)
	}
}
func TestFocusedEditIDsParentsCommandsAndExplicitClosure(t *testing.T) {
	source := focusedEditFixture(t)
	d, err := focusedEditLosslessDraft(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = parseFocusedGraph(focusedGraphValues(d)); err == nil {
		t.Fatal("CREATE parser accepted saved identities")
	}
	removed, err := applyFocusedEditCommand(d, "water_remove:a_1:p_1:n_1", source.Graph)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Removed) != 1 || focusedGraphRemovedID(removed.Removed[0]) != source.Graph.Activities[0].Phases[0].Water.Nodes[0].ID.String() {
		t.Fatal("source-bound root identity not retained")
	}
	restored, err := applyFocusedEditCommand(removed, "restore:"+removed.Removed[0].Key, source.Graph)
	if err != nil {
		t.Fatal(err)
	}
	if !focusedGraphJSONEqual(focusedGraphValues(d), focusedGraphValues(restored)) {
		t.Fatal("restoring source subtree changed IDs/raw/order")
	}
	p, e := normalizeFocusedEdit(source, removed)
	if len(e) > 0 {
		t.Fatal(e)
	}
	if err = allocateFocusedEditIDs(&p, source.Graph, nil); err != nil {
		t.Fatal(err)
	}
	diff, err := buildFocusedEditDiff(source.Graph, p, removed)
	if err != nil || len(diff.RemovedSteps) != 2 || len(diff.RetainedBlocks) != 1 || len(diff.RetainedSegments) != 1 {
		t.Fatal("server removal closure wrong", diff, err)
	}
	withoutExplicit := removed
	withoutExplicit.Removed = nil
	if _, err = buildFocusedEditDiff(source.Graph, p, withoutExplicit); err == nil {
		t.Fatal("implicit deletion accepted")
	}
	for _, mutate := range []func(url.Values){
		func(v url.Values) { v.Set("activities[a_1].id", uuid.NewString()) },
		func(v url.Values) { v.Del("activities[a_1].id") },
		func(v url.Values) { v.Add("activities[a_1].id", v.Get("activities[a_1].id")) },
		func(v url.Values) { v.Set("activities[a_1].phases[p_1].water.nodes[n_2].parent_key", "") },
	} {
		raw := focusedGraphValues(d)
		mutate(raw)
		if _, err = parseFocusedEditGraph(raw, source.Graph); err == nil {
			t.Fatal("foreign/cleared/duplicate/reparented source identity accepted")
		}
	}
	// Even a descendant's forged ID hidden inside a removed subtree is rejected.
	raw := focusedGraphValues(removed)
	r := removed.Removed[0]
	var tomb focusedGraphTombstone
	if focusedStrictJSON([]byte(r.Payload), &tomb) != nil {
		t.Fatal("tomb decode")
	}
	tomb.Values.Set("activities[a_1].phases[p_1].water.nodes[n_2].id", uuid.NewString())
	raw.Set("removed["+r.Key+"].payload", focusedGraphTombstoneJSON(tomb))
	if _, err = parseFocusedEditGraph(raw, source.Graph); err == nil {
		t.Fatal("foreign descendant hidden in removal accepted")
	}
}
func TestFocusedEditStagingIgnoresLargeOldPositionsAndUsesFreePositiveSlots(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	current := map[uuid.UUID]int32{a: math.MaxInt32, b: 4, c: 5}
	slots, err := focusedEditStagePositions(current, []uuid.UUID{a, b}, 3, 8)
	if err != nil || len(slots) != 2 {
		t.Fatal(err)
	}
	seen := map[int32]bool{}
	for id, p := range slots {
		if p <= 3 || p > int32(3+len(current)+len(slots)) || p == current[a] || p == current[b] || p == current[c] || seen[p] || id == c {
			t.Fatal("unsafe temporary slot", id, p)
		}
		seen[p] = true
	}
	if _, err = focusedEditStagePositions(current, []uuid.UUID{a, a}, 3, 8); err == nil {
		t.Fatal("duplicate retained row accepted")
	}
}
func TestFocusedEditOriginalLifecycleAndLosslessUnsupportedPrecision(t *testing.T) {
	source := focusedEditFixture(t)
	if !focusedEditEligible(source.Raw.Session, source.DecisionAt) {
		t.Fatal("future source not eligible")
	}
	start := source.Raw.Session.StartsAt.Time
	if focusedEditEligible(source.Raw.Session, start) || focusedEditEligible(source.Raw.Session, start.Add(time.Nanosecond)) {
		t.Fatal("original cutoff not strict")
	}
	// Full original precision is used for cutoff, never rounded to the UI minute.
	precise := source.Raw.Session
	precise.StartsAt.Time = precise.StartsAt.Time.Add(500 * time.Millisecond)
	if !focusedEditEligible(precise, start.Add(100*time.Millisecond)) {
		t.Fatal("original subsecond precision lost in eligibility")
	}
	source.Raw.Session = precise
	source.Graph.Schedule.StartsAt = focusedPointer(precise.StartsAt.Time)
	if _, err := focusedEditLosslessDraft(source); !errors.Is(err, errFocusedEditReadOnly) {
		t.Fatal("minute form silently rounded valid source", err)
	}
	session := source.Raw.Session
	session.SchedulePrecision = "DAY"
	session.StartsAt = pgtype.Timestamptz{}
	session.EndsAt = pgtype.Timestamptz{}
	session.EntryKind = "REST"
	today := source.DecisionAt.In(lisbonLocation()).Format("2006-01-02")
	session.ScheduledOn, _ = focusedDay(today)
	if focusedEditEligible(session, source.DecisionAt) {
		t.Fatal("today untimed policy guessed")
	}
	session.ScheduledOn.Time = session.ScheduledOn.Time.AddDate(0, 0, 1)
	if !focusedEditEligible(session, source.DecisionAt) {
		t.Fatal("future civil date requires invented clock")
	}
}

func TestFocusedEditReviewBindingsRefreshBoundariesAndInactivePin(t *testing.T) {
	source := focusedEditFixture(t)
	d, err := focusedEditLosslessDraft(source)
	if err != nil {
		t.Fatal(err)
	}
	d.Values.Set("title", "Proposta própria conservada")
	proposed, e := normalizeFocusedEdit(source, d)
	if len(e) > 0 {
		t.Fatal(e)
	}
	if err = allocateFocusedEditIDs(&proposed, source.Graph, nil); err != nil {
		t.Fatal(err)
	}
	proposed.References = source.Graph.References
	baseline, err := focusedEditBaselineFor(source)
	if err != nil {
		t.Fatal(err)
	}
	payload := focusedEditReviewPayload{Original: baseline, Proposal: proposed, Draft: focusedGraphValues(d)}
	now := source.DecisionAt
	raw, token, err := focusedWorkoutReviews.signEdit(payload, source, now)
	if err != nil {
		t.Fatal(err)
	}
	got, reviewed, err := focusedWorkoutReviews.verifyEdit(raw, token, source.Context.ActorID, source.Context.PlanID, source.Graph.SessionID, now.Add(time.Minute))
	if err != nil || !reviewed.verified || got.Original.OwnRawDigest != baseline.OwnRawDigest {
		t.Fatal("own edit proof rejected", err)
	}
	for _, badToken := range []string{token + "\n", token + "!"} {
		if _, _, err = focusedWorkoutReviews.verifyEdit(raw, badToken, source.Context.ActorID, source.Context.PlanID, source.Graph.SessionID, now); err == nil {
			t.Fatal("noncanonical proof accepted")
		}
	}
	if _, _, err = focusedWorkoutReviews.verifyEdit(raw, token, source.Context.ActorID, source.Context.PlanID, uuid.New(), now); err == nil {
		t.Fatal("cross-session edit proof")
	}
	if _, _, err = focusedWorkoutReviews.verifyGraph(raw, token, source.Context.ActorID, source.Context.PlanID, now); err == nil {
		t.Fatal("edit token became create proof")
	}
	if _, _, err = focusedWorkoutReviews.verifyEdit(raw, token, source.Context.ActorID, source.Context.PlanID, source.Graph.SessionID, now.Add(focusedWorkoutReviewTTL)); err == nil {
		t.Fatal("expired edit token accepted")
	}
	restarted := focusedWorkoutReviews
	restarted.key[0] ^= 1
	if _, _, err = restarted.verifyEdit(raw, token, source.Context.ActorID, source.Context.PlanID, source.Graph.SessionID, now); err == nil {
		t.Fatal("restarted-process edit token accepted")
	}

	otherPlan := source
	otherPlan.Context.PlanUpdatedAt = otherPlan.Context.PlanUpdatedAt.Add(time.Second)
	if err = validateFocusedEditPayload(payload, otherPlan); !errors.Is(err, errFocusedWorkoutConflict) {
		t.Fatal("confirm quietly refreshed plan version", err)
	}
	// Deliberate re-preview may update only the plan context if own source stayed.
	if err = baseline.matches(otherPlan); err != nil {
		t.Fatal(err)
	}
	payload.Proposal.Context = otherPlan.Context
	if _, _, err = focusedWorkoutReviews.signEdit(payload, otherPlan, now); err != nil {
		t.Fatal("unchanged-own-source re-preview refused", err)
	}
	changed := source
	changed.Raw.Blocks = append([]dbgen.TrainingSegmentBlock(nil), source.Raw.Blocks...)
	changed.Raw.Blocks[0].UpdatedAt.Time = changed.Raw.Blocks[0].UpdatedAt.Time.Add(time.Microsecond)
	if err = validateFocusedEditPayload(payload, changed); !errors.Is(err, errFocusedEditSourceChanged) {
		t.Fatal("own graph silently rebased", err)
	}
	profile := dbgen.WaterIntensityProfile{ID: uuid.New(), Name: "Perfil fixado", Craft: "KAYAK", Revision: 1, IsActive: false}
	pinnedSource := source.Graph
	originalWater := *pinnedSource.Activities[0].Phases[0].Water
	originalWater.ProfileID = &profile.ID
	// Clone before changing source fixture children.
	encoded, _ := json.Marshal(pinnedSource)
	if json.Unmarshal(encoded, &pinnedSource) != nil {
		t.Fatal("clone")
	}
	pinnedSource.Activities[0].Phases[0].Water = &originalWater
	pinned := pinnedSource
	pinned.References.Profiles = []dbgen.WaterIntensityProfile{profile}
	pinned.References.Zones = []dbgen.WaterIntensityZone{}
	if err = validateFocusedEditReferences(pinnedSource, pinned); err != nil {
		t.Fatal("same source block lost pinned inactive profile", err)
	}
	encoded, _ = json.Marshal(pinned)
	var newBlock focusedWorkoutGraph
	_ = json.Unmarshal(encoded, &newBlock)
	newBlock.Activities[0].Phases[0].ID = uuid.New()
	if err = validateFocusedEditReferences(pinnedSource, newBlock); err == nil {
		t.Fatal("inactive pin became permission for a new block")
	}
}
func TestFocusedEditRecoveryKeepsRawProofAndReloadWithoutPreview(t *testing.T) {
	source := focusedEditFixture(t)
	d, err := focusedEditLosslessDraft(source)
	if err != nil {
		t.Fatal(err)
	}
	own := "Texto próprio não aplicado\n\tpara copiar."
	d.Values.Set("description", own)
	for _, failure := range []error{errFocusedEditSourceChanged, errFocusedEditReadOnly, errFocusedWorkoutInvalid} {
		page := pages.StructuredWorkoutTaskPage{CSRFField: templ.Raw(""), Editing: true, Context: pages.StructuredWorkoutContext{SessionID: source.Graph.SessionID.String(), PlanID: source.Context.PlanID.String(), GroupID: source.Context.GroupID.String(), Day: source.Context.Day}, Values: d.Values, DraftURL: "/edit/draft", PreviewURL: "/edit/preview", ConfirmURL: "/edit/confirm", ReloadURL: "/edit/current", ReturnURL: "/week", ProposalJSON: "original proposal", ReviewToken: "original proof"}
		for _, a := range d.Activities {
			activity := pages.StructuredWorkoutActivity{Key: a.Key, ID: d.Values.Get(focusedActivityPrefix(a.Key) + ".id")}
			for _, p := range a.Phases {
				phase := pages.StructuredWorkoutPhase{Key: p.Key, ID: d.Values.Get(focusedGraphPhasePrefix(a.Key, p.Key) + ".id")}
				for _, n := range p.Nodes {
					phase.WaterNodes = append(phase.WaterNodes, pages.StructuredWorkoutWaterNode{Key: n.Key, ID: d.Values.Get(focusedNodePrefix(a.Key, p.Key, n.Key) + ".id"), ParentKey: n.Parent})
				}
				activity.Phases = append(activity.Phases, phase)
			}
			page.Activities = append(page.Activities, activity)
		}
		focusedEditRecoveryPage(&page, failure)
		var body bytes.Buffer
		if err = pages.StructuredWorkoutTask(page).Render(t.Context(), &body); err != nil {
			t.Fatal(err)
		}
		rendered := body.String()
		if page.ReadOnly || page.PreviewURL != "" || !strings.Contains(rendered, `<textarea id="workout-description"`) || !strings.Contains(html.UnescapeString(rendered), own) || !strings.Contains(rendered, `href="/edit/current"`) {
			t.Fatal("authorized POST recovery hides own raw/reload")
		}
		if regexp.MustCompile(`<button\b[^>]*value="preview"`).MatchString(rendered) || !strings.Contains(rendered, `name="prior_review_token" value="original proof"`) {
			t.Fatal("recovery loses proof or offers futile preview")
		}
	}
}

func TestFocusedEditSourceRejectsIdentityAliasesAcrossActivities(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*focusedWorkoutGraph)
	}{
		{"missing workout identity", func(g *focusedWorkoutGraph) { g.SessionID = uuid.Nil }},
		{"activity aliases workout", func(g *focusedWorkoutGraph) { g.Activities[0].ID = g.SessionID }},
		{"phase aliases activity", func(g *focusedWorkoutGraph) { g.Activities[0].Phases[0].ID = g.Activities[0].ID }},
		{"water step aliases phase", func(g *focusedWorkoutGraph) {
			g.Activities[0].Phases[0].Water.Nodes[0].ID = g.Activities[0].Phases[0].ID
		}},
		{"gym exercise aliases a water step", func(g *focusedWorkoutGraph) {
			id := g.Activities[0].Phases[0].Water.Nodes[0].ID
			g.Activities = append(g.Activities, focusedGraphActivityInput{
				Key: "a_2", ID: uuid.New(), Modality: dbgen.TrainingSegmentModalityGYM,
				Phases: []focusedGraphPhaseInput{{
					Key: "p_1", ID: uuid.New(),
					Gym: &focusedGraphGymInput{Exercises: []focusedGraphExerciseInput{{Key: "x_1", ID: id}}},
				}},
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := focusedEditFixture(t)
			draft, err := focusedEditLosslessDraft(source)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(&source.Graph)
			parsed, err := parseFocusedEditGraph(focusedGraphValues(draft), source.Graph)
			if !errors.Is(err, errFocusedWorkoutInvalid) || len(parsed.Activities) != 0 {
				t.Fatalf("ambiguous source graph was admitted: %v", err)
			}
		})
	}
}

func TestFocusedEditRefusesMalformedRemovalPayload(t *testing.T) {
	source := focusedEditFixture(t)
	draft, err := focusedEditLosslessDraft(source)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := applyFocusedEditCommand(draft, "water_remove:a_1:p_1:n_1", source.Graph)
	if err != nil || len(removed.Removed) != 1 {
		t.Fatalf("could not construct reviewed removal: %v", err)
	}
	key := "removed[" + removed.Removed[0].Key + "].payload"
	for _, malformed := range []string{`{"kind":`, `{"kind":"water","kind":"gym"}`} {
		raw := focusedGraphValues(removed)
		raw.Set(key, malformed)
		got, err := parseFocusedEditGraph(raw, source.Graph)
		if !errors.Is(err, errFocusedWorkoutInvalid) || len(got.Removed) != 0 {
			t.Fatalf("invalid removal survived parsing: %v", err)
		}
	}
}
