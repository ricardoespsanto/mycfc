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

func placementFixture() TrainingPlacementsPage {
	return TrainingPlacementsPage{CSRFField: templ.Raw(`<input type="hidden" name="_csrf" value="synthetic">`), Mode: "editor", Title: "Rever grupo de treino", Person: TrainingPlacementPerson{ID: "person", Name: "Pessoa autorizada", Status: "Participação elegível"}, ContextGroup: TrainingPlacementGroup{ID: "old", Name: "Grupo anterior"}, Form: TrainingPlacementForm{GroupID: "old", PersonID: "person", CommandID: "original-command", HeadRevision: "7", Kind: "TEMPORARY", SeasonID: "explicit-season", SourcePlacementID: "source-placement", TargetGroupID: "destination", EffectiveOn: "2027-01-11", TemporaryUntil: "2027-01-17"}, Seasons: []TrainingPlacementSeason{{ID: "explicit-season", Name: "Época configurada", StartsOn: "2026-09-01", EndsOn: "2027-08-31"}}, Sources: []TrainingPlacementOption{{ID: "source-placement", Name: "Grupo anterior", Help: "Origem autorizada"}}, Destinations: []TrainingPlacementOption{{ID: "destination", Name: "Grupo de destino", Help: "Destino autorizado"}}, Timeline: []TrainingPlacementTimelineRow{{ID: "source-placement", Group: "Grupo anterior", StartsOn: "2026-09-01", Status: "Atual", Eligibility: "Elegível", CancelURL: "/placements?change_id=cancel-original"}}, EditorURL: "/placements", PreviewURL: "/placements/prever", ConfirmURL: "/placements/confirmar", CancelPreviewURL: "/placements/cancelar/prever", CancelConfirmURL: "/placements/cancelar/confirmar", ReturnURL: "/group/old", NewTaskURL: "/placements/new"}
}
func placementReviewedFixture() TrainingPlacementsPage {
	p := placementFixture()
	p.Mode = "review"
	p.Review = &TrainingPlacementReview{Kind: "TEMPORARY", Headline: "Rever a mudança temporária", EffectiveOn: p.Form.EffectiveOn, TemporaryUntil: p.Form.TemporaryUntil, ReturnOn: "2027-01-18", ReturnGroup: "Grupo anterior", Before: []TrainingPlacementTimelineRow{{Group: "Grupo anterior", StartsOn: "2026-09-01"}}, After: []TrainingPlacementTimelineRow{{Group: "Grupo anterior", StartsOn: "2026-09-01", EndsOn: "2027-01-10"}, {Group: "Grupo de destino", StartsOn: "2027-01-11", EndsOn: "2027-01-17"}, {Group: "Grupo anterior", StartsOn: "2027-01-18"}}, Instructions: []TrainingPlacementInstructionRow{{Title: "Treino publicado", When: "2027-01-12", Status: "Acesso retirado quando a mudança entrar em vigor"}}, Proposal: `{"original":"proposal"}`, ReviewToken: "original-token", CanConfirm: true}
	return p
}
func renderPlacements(t *testing.T, p TrainingPlacementsPage) string {
	t.Helper()
	var out bytes.Buffer
	if err := trainingPlacementsContent(p).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
func checkPlacementDOM(t *testing.T, body string) {
	t.Helper()
	ids := map[string]bool{}
	for _, match := range regexp.MustCompile(`\sid="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		if ids[match[1]] {
			t.Fatalf("duplicate rendered ID %s", match[1])
		}
		ids[match[1]] = true
	}
	for _, tag := range publicationTags(body, "label") {
		target, _ := publicationAttr(tag, "for")
		if !ids[target] {
			t.Fatalf("label points at missing control %q", target)
		}
	}
	for _, tag := range publicationTags(body, "input") {
		if value, ok := publicationAttr(tag, "aria-describedby"); ok {
			for _, target := range strings.Fields(value) {
				if !ids[target] {
					t.Fatalf("missing feedback %s", target)
				}
			}
		}
	}
	forms := publicationForms(body)
	if len(forms) != strings.Count(body, "<form ") || len(forms) != strings.Count(body, "</form>") {
		t.Fatal("forms nested or incomplete")
	}
	for _, f := range forms {
		if strings.Count(f, "<form ") != 1 {
			t.Fatal("nested forms")
		}
	}
}
func formWithAction(body, action string) string {
	for _, f := range publicationForms(body) {
		tag := publicationTags(f, "form")[0]
		if actual, _ := publicationAttr(tag, "action"); actual == action {
			return f
		}
	}
	return ""
}
func TestPlacementEditorUsesExplicitSeasonAndNativeRawFields(t *testing.T) {
	p := placementFixture()
	body := renderPlacements(t, p)
	checkPlacementDOM(t, body)
	f := formWithAction(body, p.PreviewURL)
	if f == "" {
		t.Fatal("missing native preview form")
	}
	for n, v := range map[string]string{"_csrf": "synthetic", "group_id": p.Form.GroupID, "person_id": p.Form.PersonID, "command_id": p.Form.CommandID, "head_revision": p.Form.HeadRevision} {
		publicationHidden(t, f, n, v)
	}
	for _, name := range []string{"kind", "season_id", "source_placement_id", "target_group_id", "effective_on", "temporary_until"} {
		if !strings.Contains(f, `name="`+name+`"`) {
			t.Fatalf("missing editable field %s", name)
		}
	}
	for _, name := range []string{"source_placement_id", "target_group_id", "temporary_until"} {
		for _, tag := range append(publicationTags(f, "select"), publicationTags(f, "input")...) {
			if n, _ := publicationAttr(tag, "name"); n == name {
				if _, required := publicationAttr(tag, "required"); required {
					t.Fatalf("no-JS operation changes must not be trapped by %s's previous kind", name)
				}
			}
		}
	}
	if strings.Contains(f, `name="reason"`) || strings.Contains(f, `name="proposal"`) {
		t.Fatal("editor invents reason or authority proof")
	}
	p.Form.SeasonID = ""
	p.Seasons[0].Selected = true
	body = renderPlacements(t, p)
	if !strings.Contains(body, `value="" selected>Selecione a época`) || strings.Contains(body, `value="explicit-season" selected`) {
		t.Fatal("UI inferred a season from available/selected metadata")
	}
}
func TestPlacementSelectDateDoesNotExposePersonBeforeScopedRead(t *testing.T) {
	p := placementFixture()
	p.Mode = "select-date"
	p.Form.SeasonID = ""
	p.Form.EffectiveOn = ""
	p.Review = placementReviewedFixture().Review
	body := renderPlacements(t, p)
	checkPlacementDOM(t, body)
	if strings.Contains(body, p.Person.Name) || strings.Contains(body, p.Person.Status) || strings.Contains(body, "Treino publicado") || strings.Contains(body, "Colocações atuais e agendadas") {
		t.Fatal("select-date disclosed later person/timeline/instruction details")
	}
	f := formWithAction(body, p.EditorURL)
	if f == "" || !strings.Contains(f, `method="get"`) {
		t.Fatal("season/date must be selected through native GET before person lookup")
	}
	for _, n := range []string{"season_id", "effective_on"} {
		if !strings.Contains(f, `name="`+n+`"`) {
			t.Fatalf("missing explicit %s", n)
		}
	}
	if strings.Contains(f, "original-token") || strings.Contains(f, "original-command") {
		t.Fatal("proof/command must not enter selection URL")
	}
}
func TestPlacementReviewPreservesExactReturnProofAndRawBack(t *testing.T) {
	p := placementReviewedFixture()
	body := renderPlacements(t, p)
	checkPlacementDOM(t, body)
	for _, text := range []string{"Antes", "Depois", "Regresso ao grupo anterior", "2027-01-18", "Grupo anterior", "nova publicação explícita", "não reaparecem automaticamente", "Treino publicado"} {
		if !strings.Contains(body, text) {
			t.Fatalf("missing reviewed fact %q", text)
		}
	}
	confirm := formWithAction(body, p.ConfirmURL)
	if confirm == "" {
		t.Fatal("missing explicit confirmation")
	}
	publicationHidden(t, confirm, "proposal", p.Review.Proposal)
	publicationHidden(t, confirm, "review_token", p.Review.ReviewToken)
	publicationHidden(t, confirm, "_csrf", "synthetic")
	if len(publicationTags(confirm, "input")) != 3 {
		t.Fatal("confirmation must carry only reviewed proof and CSRF")
	}
	back := formWithAction(body, p.EditorURL)
	if back == "" {
		t.Fatal("missing raw back POST")
	}
	for _, field := range placementRawFields(p.Form) {
		publicationHidden(t, back, field.Name, field.Value)
	}
	publicationHidden(t, back, "proposal", p.Review.Proposal)
	publicationHidden(t, back, "review_token", p.Review.ReviewToken)
	p.Review.Instructions[0].RequiresPolicy = true
	body = renderPlacements(t, p)
	if formWithAction(body, p.ConfirmURL) != "" {
		t.Fatal("affected unresolved instruction boundary must not be confirmable")
	}
}
func TestPlacementRecoveryRetainsOwnInvalidValuesAndNeverRetriesUnknownCommit(t *testing.T) {
	p := placementReviewedFixture()
	p.Mode = "recovery"
	p.Recovery = TrainingPlacementRecovery{Active: true, Message: "O estado mudou.", ReloadURL: "/placements/reload"}
	p.Form.EffectiveOn = "not-a-date"
	p.Form.SeasonID = "own-unavailable-season"
	p.Form.TargetGroupID = "own-unavailable-group"
	p.Form.Errors = validation.FieldErrors{"effective_on": "Reveja a data indicada.", "season_id": "Reveja a época indicada."}
	body := renderPlacements(t, p)
	checkPlacementDOM(t, body)
	if !strings.Contains(body, `type="text" value="not-a-date"`) || !strings.Contains(body, `value="own-unavailable-season" selected`) || !strings.Contains(body, `value="own-unavailable-group" selected`) {
		t.Fatal("invalid raw values disappeared from editable controls")
	}
	if !strings.Contains(body, `href="#placement-effective_on"`) || !strings.Contains(body, `class="error-summary"`) || !strings.Contains(body, `autofocus`) {
		t.Fatal("validation needs focused field-linked summary")
	}
	if formWithAction(body, p.ConfirmURL) != "" || formWithAction(body, p.PreviewURL) == "" || !strings.Contains(body, "original-token") {
		t.Fatal("known conflict must retain own proof, expose explicit re-preview and refuse confirm")
	}
	p.Recovery.UnknownCommit = true
	body = renderPlacements(t, p)
	checkPlacementDOM(t, body)
	if len(publicationForms(body)) != 0 || strings.Contains(body, "Confirmar alteração") || strings.Contains(body, "Iniciar outra alteração") {
		t.Fatal("unknown commit must not offer resubmission/fresh task")
	}
	for _, text := range []string{"not-a-date", "original-token", "Recarregar estado atual"} {
		if !strings.Contains(body, text) {
			t.Fatalf("unknown result lost own recovery data %s", text)
		}
	}
}
func TestPlacementCancellationUsesSeparateReviewAndConfirmActions(t *testing.T) {
	p := placementFixture()
	p.Form.Kind = "CANCEL"
	p.Form.ChangeID = "original-change"
	p.Form.CancellationID = "stable-cancellation"
	body := renderPlacements(t, p)
	checkPlacementDOM(t, body)
	f := formWithAction(body, p.CancelPreviewURL)
	if f == "" || formWithAction(body, p.PreviewURL) != "" {
		t.Fatal("cancellation must use dedicated preview")
	}
	publicationHidden(t, f, "change_id", "original-change")
	publicationHidden(t, f, "cancellation_id", "stable-cancellation")
	p.Mode = "review"
	p.Review = placementReviewedFixture().Review
	p.Review.Kind = "CANCEL"
	body = renderPlacements(t, p)
	checkPlacementDOM(t, body)
	if formWithAction(body, p.CancelConfirmURL) == "" || formWithAction(body, p.ConfirmURL) != "" {
		t.Fatal("cancellation must use dedicated proof confirmation")
	}
}
