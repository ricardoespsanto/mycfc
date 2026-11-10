package pages

import (
	"regexp"
	"strings"
	"testing"
)

var regexpInstructionIDs = regexp.MustCompile(`\bid="([^"]+)"`)

func publicationInstructionFixture() TrainingPublicationReviewPage {
	p := publicationReviewFixture()
	p.Instructions = &TrainingPublicationInstructionReview{Choices: []TrainingPublicationInstructionChoice{{Key: "athlete/first", Label: "Ana · Treino A"}, {Key: "athlete/second", Label: "Ana · Treino B"}}, SelectedKey: "athlete/second", Athlete: "Ana revista", Session: StructuredTrainingSession{ID: "resolved-session", Title: "Treino B resolvido", EntryKind: "TRAINING", Segments: []StructuredTrainingSegment{{ID: "resolved-segment", Modality: "GYM", Title: "Ginásio revisto", Blocks: []StructuredTrainingBlock{{ID: "resolved-block", Title: "Bloco revisto", Instructions: "Instrução individual revista"}}}}}}
	return p
}
func TestPublicationInstructionReviewUsesOriginalProofAndOneCanonicalBody(t *testing.T) {
	p := publicationInstructionFixture()
	body := renderPublicationReview(t, p)
	form := publicationFormFor(body, "inspect")
	if form == "" {
		t.Fatal("missing native inspect form")
	}
	for name, value := range map[string]string{"_csrf": "synthetic", "source_updated_at": p.SourceUpdatedAt, "review_token": p.ReviewToken, "reviewed_latest_id": p.LatestPublicationID, "reviewed_latest_revision": "7", "return_group": p.ReturnGroup, "return_week": p.ReturnWeek, "return_session": p.ReturnSession, "return_day": p.ReturnDay, "change_summary": p.Summary} {
		publicationHidden(t, form, name, value)
	}
	if !strings.Contains(form, `action="`+p.ActionURL+`"`) || !strings.Contains(form, `method="post"`) || !strings.Contains(form, `name="instruction_key"`) || !strings.Contains(form, `<label for="publication-instruction-key">`) || !strings.Contains(form, "Atleta e treino") || !strings.Contains(form, "Ver instruções") {
		t.Fatal("inspect must use original native POST and labelled selection")
	}
	if strings.Count(body, `id="structured-session-resolved-session"`) != 1 || strings.Count(body, "Instrução individual revista") != 1 || !strings.Contains(body, "Ana revista") || !strings.Contains(body, "Instruções revistas") {
		t.Fatal("expected one selected canonical instruction")
	}
	if strings.Index(body, "Instruções revistas") > strings.Index(body, `name="publication_intent" value="confirm"`) {
		t.Fatal("instructions must precede confirmation")
	}
	if publicationFormFor(body, "confirm") == "" || strings.Count(form, "<form") != 1 {
		t.Fatal("confirmation remains separate from inspection")
	}
	ids := map[string]bool{}
	for _, tag := range regexpInstructionIDs.FindAllStringSubmatch(body, -1) {
		if ids[tag[1]] {
			t.Fatalf("duplicate ID %s", tag[1])
		}
		ids[tag[1]] = true
	}
}
func TestPublicationInstructionReviewSuppressesStaleUnknownAndUnselectedBody(t *testing.T) {
	for _, mode := range []string{"error", "unknown", "other"} {
		t.Run(mode, func(t *testing.T) {
			p := publicationInstructionFixture()
			p.Mode = mode
			body := renderPublicationReview(t, p)
			if strings.Contains(body, "Ana revista") || strings.Contains(body, "Instrução individual revista") || publicationFormFor(body, "inspect") != "" {
				t.Fatal("non-preview must not expose instructions or inspect")
			}
		})
	}
	p := publicationInstructionFixture()
	p.Error = "A fonte mudou."
	if body := renderPublicationReview(t, p); strings.Contains(body, "Instrução individual revista") || publicationFormFor(body, "inspect") != "" {
		t.Fatal("stale preview must hide retained body")
	}
	p = publicationInstructionFixture()
	p.Instructions.SelectedKey = "unrecognised"
	if body := renderPublicationReview(t, p); strings.Contains(body, "Instrução individual revista") {
		t.Fatal("unknown choice must not render body")
	}
}
