package pages

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/validation"
)

func TestStructuredTrainingDialogFeedbackKeepsDistinctControlTargets(t *testing.T) {
	for _, active := range []string{"", "segment", "water", "gym"} {
		t.Run("validation target "+active, func(t *testing.T) {
			segmentFields := map[string]string{
				"segment-title-session": "title", "segment-location-session": "location",
				"segment-duration-session": "planned_duration_minutes", "segment-start-offset-session": "planned_start_offset_minutes",
				"segment-transition-session": "transition_duration_minutes", "segment-equipment-session": "equipment_notes",
				"segment-block-title-session": "block_title", "segment-instructions-session": "instructions",
			}
			page := StructuredTrainingPage{CSRFField: templ.Raw(""), SegmentForm: StructuredTrainingSegmentForm{Errors: validation.FieldErrors{}}, BlockForm: StructuredTrainingBlockForm{Errors: validation.FieldErrors{}}}
			page.SegmentForm.SessionID = "session"
			page.BlockForm.SegmentID = active
			if active == "segment" {
				for _, field := range segmentFields {
					page.SegmentForm.Errors[field] = "Reveja " + field + "."
				}
			}
			if active == "water" || active == "gym" {
				for _, field := range []string{"purpose", "title", "instructions"} {
					page.BlockForm.Errors[field] = "Reveja " + field + "."
				}
			}
			var output bytes.Buffer
			for _, form := range []templ.Component{structuredSegmentForm(page, "session"), structuredBlockForm(page, "water"), structuredBlockForm(page, "gym")} {
				if err := form.Render(context.Background(), &output); err != nil {
					t.Fatal(err)
				}
			}
			rendered := output.String()
			// Inspect actual rendered tags, including hidden feedback in unopened dialogs.
			tags := regexp.MustCompile(`<[^>]+>`).FindAllString(rendered, -1)
			attribute := func(tag, key string) string {
				match := regexp.MustCompile(`(?:^|\s)` + regexp.QuoteMeta(key) + `="([^"]*)"`).FindStringSubmatch(tag)
				if len(match) == 2 {
					return match[1]
				}
				return ""
			}
			byID := map[string]string{}
			labels := map[string]int{}
			var targets []string
			for _, tag := range tags {
				if id := attribute(tag, "id"); id != "" {
					if _, duplicate := byID[id]; duplicate {
						t.Fatalf("duplicate rendered ID %q", id)
					}
					byID[id] = tag
				}
				if strings.HasPrefix(tag, "<label ") {
					labels[attribute(tag, "for")]++
				}
				if href := attribute(tag, "href"); strings.HasPrefix(href, "#") {
					targets = append(targets, strings.TrimPrefix(href, "#"))
				}
			}
			for _, segment := range []string{"water", "gym"} {
				for _, field := range []string{"purpose", "title", "instructions"} {
					segmentFields["block-"+field+"-"+segment] = field
				}
			}
			expectedTargets := 0
			for id, field := range segmentFields {
				invalid := (active == "segment" && strings.HasPrefix(id, "segment-")) || (active != "" && active != "segment" && strings.HasSuffix(id, "-"+active))
				if invalid {
					expectedTargets++
				}
				control, feedback := byID[id], byID[id+"-error"]
				if control == "" || feedback == "" || labels[id] != 1 {
					t.Fatalf("control %s needs its own label and feedback: %q / %q", id, control, feedback)
				}
				if attribute(control, "name") != field || attribute(control, "aria-describedby") != id+"-error" {
					t.Fatalf("control %s lost submitted field or feedback association: %s", id, control)
				}
				if attribute(control, "aria-invalid") != map[bool]string{false: "false", true: "true"}[invalid] || strings.Contains(feedback, " hidden") == invalid {
					t.Fatalf("control %s has inconsistent validation state: %s / %s", id, control, feedback)
				}
				if invalid && !strings.Contains(rendered, `id="`+id+`-error" class="field-error">Reveja `+field+".</p>") {
					t.Fatalf("feedback %s does not retain its original validation message", id)
				}
			}
			if len(targets) != expectedTargets {
				t.Fatalf("expected %d field-summary targets, got %d", expectedTargets, len(targets))
			}
			for _, target := range targets {
				if _, ok := segmentFields[target]; !ok || byID[target] == "" {
					t.Fatalf("error-summary target %q is not its rendered control", target)
				}
			}
		})
	}
}
