package handlers

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func placementTestCommand() (trainingPlacementContext, trainingPlacementCommand) {
	x := trainingPlacementContext{ActorID: uuid.New(), SubjectID: uuid.New(), ProgrammeID: uuid.New(), GroupID: uuid.New()}
	c := trainingPlacementCommand{ID: uuid.New(), SubjectID: x.SubjectID, ProgrammeID: x.ProgrammeID, ContextGroupID: x.GroupID, SeasonID: uuid.New(), Kind: placementTemporary, SourcePlacementID: uuid.New(), TargetGroupID: uuid.New(), EffectiveOn: "2027-03-27", TemporaryUntil: "2027-03-28", ExpectedRevision: 7}
	return x, c
}
func TestTrainingPlacementParserPreservesRawAndRequiresExplicitContext(t *testing.T) {
	x, c := placementTestCommand()
	raw := c.values()
	raw.Set("gorilla.csrf.Token", "transport-only")
	parsed, retained, errs := parseTrainingPlacementForm(raw, x)
	if !errs.Empty() || parsed != c || retained.Get("gorilla.csrf.Token") != "" {
		t.Fatalf("valid command %+v %v", parsed, errs)
	}
	retained.Set("effective_on", "local change")
	if raw.Get("effective_on") != "2027-03-27" {
		t.Fatal("retained values alias input")
	}
	cases := []struct {
		name, field string
		mutate      func(url.Values)
	}{
		{"missing explicit season", "season_id", func(v url.Values) { v.Set("season_id", "") }},
		{"bad calendar day", "effective_on", func(v url.Values) { v.Set("effective_on", "2027-02-29") }},
		{"raw whitespace is not silently normalized", "effective_on", func(v url.Values) { v.Set("effective_on", "\n2027-03-27 ") }},
		{"duplicate intent", "form", func(v url.Values) { v.Add("kind", "MOVE") }},
		{"posted administrator is not authority", "form", func(v url.Values) { v.Set("is_admin", "true") }},
		{"posted programme is not authority", "form", func(v url.Values) { v.Set("programme_id", uuid.NewString()) }},
		{"person context changed", "form", func(v url.Values) { v.Set("person_id", uuid.NewString()) }},
		{"group context changed", "form", func(v url.Values) { v.Set("group_id", uuid.NewString()) }},
		{"noncanonical id", "command_id", func(v url.Values) { v.Set("command_id", strings.ToUpper("abcdefab-cdef-abcd-efab-cdefabcdefab")) }},
		{"head negative", "head_revision", func(v url.Values) { v.Set("head_revision", "-1") }},
		{"head ambiguous spelling", "head_revision", func(v url.Values) { v.Set("head_revision", "007") }},
		{"head overflow", "head_revision", func(v url.Values) { v.Set("head_revision", "999999999999999999999999") }},
		{"temporary end missing", "temporary_until", func(v url.Values) { v.Set("temporary_until", "") }},
		{"temporary end before start", "temporary_until", func(v url.Values) { v.Set("temporary_until", "2027-03-26") }},
		{"ordinary move cannot carry temporary end", "temporary_until", func(v url.Values) { v.Set("kind", "MOVE") }},
		{"source required", "source_placement_id", func(v url.Values) { v.Set("source_placement_id", "") }},
		{"destination required", "target_group_id", func(v url.Values) { v.Set("target_group_id", "") }},
		{"unknown kind", "kind", func(v url.Values) { v.Set("kind", "AUTOMATIC") }},
		{"bounded operation", "form", func(v url.Values) { v.Set("effective_on", strings.Repeat("x", trainingPlacementMaxFormBytes+1)) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := c.values()
			tc.mutate(v)
			before := cloneFocusedValues(v)
			_, kept, errs := parseTrainingPlacementForm(v, x)
			if !errs.Has(tc.field) {
				t.Fatalf("expected %s: %v", tc.field, errs)
			}
			if !reflect.DeepEqual(v, before) || !reflect.DeepEqual(kept, before) {
				t.Fatal("validation lost or changed own raw values")
			}
		})
	}
}
func TestTrainingPlacementProjectionReturnsExactPreviousGroupOnCalendarDay(t *testing.T) {
	_, c := placementTestCommand()
	source := trainingPlacementSpan{ID: c.SourcePlacementID, GroupID: uuid.New(), GroupName: "Grupo anterior", StartsOn: "2027-03-01", EndsOn: "2027-07-01", Version: 3}
	unchanged := source
	p, err := projectTrainingPlacement(c, &source, "Grupo temporário")
	if err != nil {
		t.Fatal(err)
	}
	if p.Source.EndsOn != "2027-03-26" || p.Source.ID != source.ID || p.Source.Superseded || p.Destination.StartsOn != "2027-03-27" || p.Destination.EndsOn != "2027-03-28" || p.Destination.GroupID != c.TargetGroupID || p.Return.StartsOn != "2027-03-29" || p.Return.EndsOn != source.EndsOn || p.Return.GroupID != source.GroupID || p.Return.GroupName != source.GroupName {
		t.Fatalf("incorrect inclusive timeline: %+v", p)
	}
	if source != unchanged || p.Destination.ID != uuid.Nil || p.Return.ID != uuid.Nil {
		t.Fatal("projection rewrote history or invented stored identities")
	}
	source.StartsOn = c.EffectiveOn
	unchanged = source
	p, err = projectTrainingPlacement(c, &source, "Grupo temporário")
	if err != nil || !p.Source.Superseded || p.Source.StartsOn != source.StartsOn || p.Source.EndsOn != source.EndsOn || source != unchanged {
		t.Fatalf("source-start must retain valid original A1: %+v %v", p, err)
	}
}
func TestTrainingPlacementProjectionDoesNotInventReturnOrSuppressOtherSource(t *testing.T) {
	_, c := placementTestCommand()
	s := trainingPlacementSpan{ID: c.SourcePlacementID, GroupID: uuid.New(), StartsOn: "2027-03-01", EndsOn: "2027-03-29", Version: 2}
	p, err := projectTrainingPlacement(c, &s, "B")
	if err != nil || p.Return.StartsOn != s.EndsOn || p.Return.EndsOn != s.EndsOn {
		t.Fatalf("one-day return must remain possible: %+v %v", p, err)
	}
	bad := s
	bad.EndsOn = c.TemporaryUntil
	if _, err = projectTrainingPlacement(c, &bad, "B"); err == nil {
		t.Fatal("accepted impossible finite return")
	}
	bad = s
	bad.ID = uuid.New()
	if _, err = projectTrainingPlacement(c, &bad, "B"); err == nil {
		t.Fatal("suppressed an unrelated placement")
	}
	bad = s
	bad.Cancelled = true
	if _, err = projectTrainingPlacement(c, &bad, "B"); err == nil {
		t.Fatal("reused cancelled source")
	}
	bad = s
	bad.Superseded = true
	if _, err = projectTrainingPlacement(c, &bad, "B"); err == nil {
		t.Fatal("reused superseded source")
	}
	c.TargetGroupID = s.GroupID
	if _, err = projectTrainingPlacement(c, &s, "B"); err == nil {
		t.Fatal("accepted same-group move")
	}
	c.TargetGroupID = uuid.New()
	c.Kind = placementMove
	c.TemporaryUntil = ""
	p, err = projectTrainingPlacement(c, &s, "B")
	if err != nil || p.Return != nil || p.Destination.EndsOn != s.EndsOn {
		t.Fatalf("ordinary move invented a return: %+v %v", p, err)
	}
	c.Kind = placementLeave
	c.TargetGroupID = uuid.Nil
	p, err = projectTrainingPlacement(c, &s, "")
	if err != nil || p.Destination != nil || p.Return != nil {
		t.Fatalf("leave invented placement: %+v %v", p, err)
	}
	c.Kind = placementJoin
	c.SourcePlacementID = uuid.Nil
	c.TargetGroupID = uuid.New()
	p, err = projectTrainingPlacement(c, nil, "B")
	if err != nil || p.Source != nil || p.Return != nil || p.Destination.GroupID != c.TargetGroupID {
		t.Fatalf("join: %+v %v", p, err)
	}
}
