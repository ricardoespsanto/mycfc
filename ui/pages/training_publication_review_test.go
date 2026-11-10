package pages

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/validation"
)

func publicationReviewFixture() TrainingPublicationReviewPage {
	return TrainingPublicationReviewPage{CSRFField: templ.Raw(`<input type="hidden" name="_csrf" value="synthetic">`), Mode: "preview", PlanTitle: "Semana M11", GroupName: "Cadetes", WeekLabel: "9–15 novembro", Summary: "Revisão das instruções\nLinha interior\tcom nota.", SourceUpdatedAt: "original-source-version", ReviewToken: "opaque-original-review", LatestPublicationID: "original-publication-id", ExistingRevision: 7, ReturnGroup: "group", ReturnWeek: "week", ReturnSession: "session", ReturnDay: "2026-11-09", ActionURL: "/publication/plan/publicar", ReturnURL: "/week?day=2026-11-09", ReviewURL: "/week?day=2026-11-09#training-publication", SetupURL: "/group/atletas", CanConfirm: true, Recipients: []TrainingPublicationRecipient{{Name: "Ana Silva", SessionCount: 2}, {Name: "Bruno Costa", SessionCount: 1}}, PrescriptionCount: 3, AddedCount: 1, ChangedCount: 1, RemovedCount: 1, UnchangedCount: 1}
}
func renderPublicationComponent(t *testing.T, c templ.Component) string {
	t.Helper()
	var output bytes.Buffer
	if err := c.Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	body := output.String()
	if dir := os.Getenv("PUBLICATION_REVIEW_COMPONENT_EVIDENCE"); dir != "" {
		if !filepath.IsAbs(dir) || !strings.HasPrefix(filepath.Clean(dir), filepath.Clean(os.TempDir())+string(os.PathSeparator)) {
			t.Fatal("component evidence requires absolute temporary directory")
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(body))
		name := strings.ReplaceAll(t.Name(), "/", "-") + fmt.Sprintf("-%x.html", sum[:6])
		doc := `<!doctype html><html lang="pt-PT"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Revisão de destinatários</title><link rel="stylesheet" href="/assets/app.css"><script defer src="/assets/app.js"></script></head><body class="app-body"><main id="conteudo-principal">` + body + `</main></body></html>`
		if err := os.WriteFile(filepath.Join(dir, name), []byte(doc), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return body
}
func renderPublicationReview(t *testing.T, p TrainingPublicationReviewPage) string {
	t.Helper()
	return renderPublicationComponent(t, trainingPublicationReviewContent(p))
}
func publicationTags(body, tag string) []string {
	return regexp.MustCompile(`(?s)<`+regexp.QuoteMeta(tag)+`\b[^>]*>`).FindAllString(body, -1)
}
func publicationAttr(tag, name string) (string, bool) {
	m := regexp.MustCompile(`(?:\s|<)` + regexp.QuoteMeta(name) + `(?:="([^"]*)")?(?:\s|/?>|$)`).FindStringSubmatch(tag)
	if m == nil {
		return "", false
	}
	return html.UnescapeString(m[1]), true
}
func publicationForms(body string) []string {
	return regexp.MustCompile(`(?s)<form\b[^>]*>.*?</form>`).FindAllString(body, -1)
}
func publicationFormFor(body, intent string) string {
	for _, form := range publicationForms(body) {
		for _, tag := range publicationTags(form, "input") {
			name, _ := publicationAttr(tag, "name")
			value, _ := publicationAttr(tag, "value")
			if name == "publication_intent" && value == intent {
				return form
			}
		}
	}
	return ""
}
func publicationHidden(t *testing.T, body, name, want string) {
	t.Helper()
	for _, tag := range publicationTags(body, "input") {
		if field, _ := publicationAttr(tag, "name"); field == name {
			value, _ := publicationAttr(tag, "value")
			kind, _ := publicationAttr(tag, "type")
			if value != want || kind != "hidden" {
				t.Fatalf("hidden %s type%q value%q want%q", name, kind, value, want)
			}
			return
		}
	}
	t.Fatalf("missing hidden %s", name)
}

func TestPublicationReviewPreviewBindsOriginalMetadataAndShowsAuthorizedRecipients(t *testing.T) {
	p := publicationReviewFixture()
	body := renderPublicationReview(t, p)
	confirm := publicationFormFor(body, "confirm")
	if confirm == "" {
		t.Fatal("valid preview has no confirmation")
	}
	for _, want := range []string{"Ana Silva", "Bruno Costa", "2 sessões", "1 sessão", "Prescrições previstas", "Resumo aprovado", "Última revisão publicada: 7", "Publicar revisão"} {
		if !strings.Contains(body, want) {
			t.Error("missing", want)
		}
	}
	for name, want := range map[string]string{"publication_intent": "confirm", "confirm": "yes", "source_updated_at": p.SourceUpdatedAt, "change_summary": p.Summary, "review_token": p.ReviewToken, "reviewed_latest_id": p.LatestPublicationID, "reviewed_latest_revision": "7", "return_group": "group", "return_week": "week", "return_session": "session", "return_day": "2026-11-09"} {
		publicationHidden(t, confirm, name, want)
	}
	if strings.Contains(confirm, "<textarea") || strings.Contains(confirm, `type="text"`) || strings.Contains(body, `name="return_to"`) {
		t.Fatal("confirm edits approved summary or transports arbitrary return URL")
	}
	for _, button := range publicationTags(confirm, "button") {
		if _, named := publicationAttr(button, "name"); named {
			t.Fatal("confirmation depends on disabled named submitter")
		}
	}
	for _, link := range publicationTags(body, "a") {
		if target, _ := publicationAttr(link, "href"); strings.Contains(target, p.ReviewToken) {
			t.Fatal("review token leaked in link")
		}
	}
	if strings.Contains(body, "athlete_user_id") || strings.Contains(body, "membership_id") || strings.Contains(body, "guardian_id") {
		t.Fatal("recipient identifiers exposed")
	}
}

func TestPublicationReviewNoOpAndReviewedEmptySuccessor(t *testing.T) {
	t.Run("no changes", func(t *testing.T) {
		p := publicationReviewFixture()
		p.NoChanges = true
		p.AddedCount = 0
		p.ChangedCount = 0
		p.RemovedCount = 0
		p.UnchangedCount = 3
		body := renderPublicationReview(t, p)
		if publicationFormFor(body, "confirm") == "" || !strings.Contains(body, "Concluir revisão") || !strings.Contains(body, "não cria outra publicação") || !strings.Contains(body, "resumo introduzido não será guardado") {
			t.Fatal("no-op invented new revision/save or lost fresh confirmation")
		}
		if strings.Contains(body, "revisão 8") || strings.Contains(body, "Publicado agora") {
			t.Fatal("invented revision or timestamp")
		}
	})
	t.Run("empty successor", func(t *testing.T) {
		p := publicationReviewFixture()
		p.Recipients = nil
		p.PrescriptionCount = 0
		p.AddedCount = 0
		p.ChangedCount = 0
		p.UnchangedCount = 0
		p.RemovedCount = 3
		body := renderPublicationReview(t, p)
		if publicationFormFor(body, "confirm") == "" || !strings.Contains(body, "Esta revisão ficará sem destinatários") || !strings.Contains(body, "histórico anterior mantém-se") {
			t.Fatal("reviewed empty successor lost explicit removal confirmation")
		}
	})
	t.Run("first empty", func(t *testing.T) {
		p := publicationReviewFixture()
		p.Recipients = nil
		p.PrescriptionCount = 0
		p.ExistingRevision = 0
		p.LatestPublicationID = ""
		body := renderPublicationReview(t, p)
		if publicationFormFor(body, "confirm") != "" || strings.Contains(body, "Revisão dos destinatários") {
			t.Fatal("first no-recipient preview allowed publication")
		}
		if !strings.Contains(body, "Rever configuração do grupo") || !strings.Contains(body, "Voltar à semana") {
			t.Fatal("empty first preview lost actionable recovery")
		}
	})
}

func TestPublicationReviewErrorsHideStaleAudienceAndRetainOwnOriginalProposal(t *testing.T) {
	p := publicationReviewFixture()
	p.Mode = "error"
	p.Error = "A fonte mudou. Reveja os destinatários."
	p.Errors = validation.FieldErrors{"summary": "Reveja o resumo.", "form": p.Error}
	p.Summary = "\nProposta própria\nSegunda linha\tcom tabulação."
	p.Recipients = []TrainingPublicationRecipient{{Name: "PRIVATE_STALE_ATHLETE", SessionCount: 99991}}
	p.PrescriptionCount = 99991
	p.AddedCount = 99992
	p.NoChanges = true
	body := renderPublicationReview(t, p)
	for _, forbidden := range []string{"PRIVATE_STALE_ATHLETE", "99991", "99992", "Revisão dos destinatários", "Última revisão publicada:", "Concluir revisão"} {
		if strings.Contains(body, forbidden) {
			t.Error("stale preview leaked", forbidden)
		}
	}
	if publicationFormFor(body, "confirm") != "" {
		t.Fatal("error offered confirmation")
	}
	preview := publicationFormFor(body, "preview")
	if preview == "" {
		t.Fatal("no explicit re-preview")
	}
	for name, want := range map[string]string{"source_updated_at": p.SourceUpdatedAt, "review_token": p.ReviewToken, "reviewed_latest_id": p.LatestPublicationID, "reviewed_latest_revision": "7", "return_day": p.ReturnDay} {
		publicationHidden(t, preview, name, want)
	}
	if !strings.Contains(body, `href="#publication-review-summary"`) || !strings.Contains(body, `aria-invalid="true"`) || !strings.Contains(body, "<textarea") {
		t.Fatal("summary error is not linked/retained")
	}
	if count := len(regexp.MustCompile(`\sautofocus(?:\s|>)`).FindAllString(body, -1)); count != 1 {
		t.Fatalf("autofocus count%d", count)
	}
	if !strings.Contains(body, "\n\nProposta própria") {
		t.Fatal("leading LF lacks native textarea compensation")
	}
	if strings.Contains(body, `type="text" name="change_summary"`) {
		t.Fatal("summary would sanitize line breaks")
	}
}

func TestPublicationReviewUnknownCommitOffersLinksOnly(t *testing.T) {
	p := publicationReviewFixture()
	p.Mode = "unknown"
	p.Error = "A confirmação do resultado ficou interrompida."
	p.Errors = validation.FieldErrors{"summary": "Não volte a submeter sem rever."}
	p.Recipients = []TrainingPublicationRecipient{{Name: "PRIVATE_UNKNOWN_ATHLETE", SessionCount: 99991}}
	p.PrescriptionCount = 99991
	body := renderPublicationReview(t, p)
	if len(publicationForms(body)) != 0 || strings.Contains(body, p.ReviewToken) || strings.Contains(body, "PRIVATE_UNKNOWN_ATHLETE") || strings.Contains(body, "99991") {
		t.Fatal("unknown commit exposed audience/token or resubmit form")
	}
	for _, forbidden := range []string{"Nada foi guardado", "nothing saved", `href="#publication-review-summary"`} {
		if strings.Contains(body, forbidden) {
			t.Error("unknown outcome made false recovery claim", forbidden)
		}
	}
	if !strings.Contains(body, "Consultar a semana e o histórico") || !strings.Contains(body, "Voltar à semana") {
		t.Fatal("unknown commit lost authorized history/context links")
	}
	if count := len(regexp.MustCompile(`\sautofocus(?:\s|>)`).FindAllString(body, -1)); count != 1 {
		t.Fatalf("autofocus count%d", count)
	}
}

func TestPublicationReviewDefensiveConfirmationAndEscaping(t *testing.T) {
	for _, tc := range []string{"false", "token", "source", "summary", "baseline", "errors", "audience", "wrong-mode"} {
		t.Run(tc, func(t *testing.T) {
			p := publicationReviewFixture()
			switch tc {
			case "false":
				p.CanConfirm = false
			case "token":
				p.ReviewToken = ""
			case "source":
				p.SourceUpdatedAt = ""
			case "summary":
				p.Summary = "x"
			case "baseline":
				p.LatestPublicationID = ""
			case "errors":
				p.Errors = validation.FieldErrors{"form": "Reveja a fonte."}
			case "audience":
				p.Recipients = nil
			case "wrong-mode":
				p.Mode = "other"
			}
			body := renderPublicationReview(t, p)
			if publicationFormFor(body, "confirm") != "" || strings.Contains(body, "Ana Silva") {
				t.Fatal("invalid preview exposed confirmation or audience")
			}
		})
	}
	p := publicationReviewFixture()
	attack := `"><img src=x onerror="alert(1)">`
	p.Recipients = []TrainingPublicationRecipient{{Name: attack, SessionCount: 1}, {Name: attack, SessionCount: 2}}
	p.Summary = "Resumo <script>alert(1)</script>\ncom tabulação\t."
	p.ReviewToken = `opaque"</input><script>`
	body := renderPublicationReview(t, p)
	if strings.Contains(body, "<img") || strings.Contains(body, "<script>") {
		t.Fatal("unescaped context/recipient/summary/token")
	}
	if strings.Count(body, "&lt;img") != 2 {
		t.Fatal("duplicate displayed names were deduplicated or lost")
	}
	publicationHidden(t, publicationFormFor(body, "confirm"), "review_token", p.ReviewToken)
}

func TestPublicationReviewSummaryPreservesMultilineAndARIAReferences(t *testing.T) {
	p := publicationReviewFixture()
	body := renderPublicationReview(t, p)
	preview := publicationFormFor(body, "preview")
	if !strings.Contains(preview, p.Summary) || !strings.Contains(preview, `minlength="2" maxlength="500"`) {
		t.Fatal("raw multiline summary or2..500 rule lost")
	}
	if publicationReviewTextareaValue("\nA\nB") != "\n\nA\nB" || publicationReviewTextareaValue("A\nB") != "A\nB" {
		t.Fatal("textarea compensation altered non-leading line breaks")
	}
	ids := map[string]bool{}
	for _, match := range regexp.MustCompile(`\sid="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		if ids[match[1]] {
			t.Error("duplicate id", match[1])
		}
		ids[match[1]] = true
	}
	for _, match := range regexp.MustCompile(`\s(?:for|aria-describedby|aria-labelledby)="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		for _, id := range strings.Fields(match[1]) {
			if !ids[id] {
				t.Error("orphan accessibility reference", id)
			}
		}
	}
	if strings.Contains(body, "task-surface--sheet") || !strings.Contains(body, `class="task-route-page"`) {
		t.Fatal("long route uses fixed-height sheet or loses established wrapper")
	}
}

func TestPublicationPlannerCurrentReviewEntryRemainsDiscoverable(t *testing.T) {
	p := StructuredTrainingPage{CSRFField: templ.Raw(`<input name="_csrf" value="synthetic" type="hidden">`), PlannerReturnURL: "/week?day=2026-11-09", Publications: []StructuredPublicationState{{PlanID: "plan", Plan: "M11", Current: true, Status: "Publicação atual", SourceUpdatedAt: "different-current-source-version", Revision: 7, PublishedAt: "2026-11-08 18:00", PublishedBy: "Treinador", AthleteCount: 2, PrescriptionCount: 3, UnchangedCount: 3}}}
	body := renderPublicationComponent(t, structuredPublicationManagement(p))
	if publicationFormFor(body, "preview") == "" || !strings.Contains(body, "Rever destinatários") || !strings.Contains(body, "Não há alterações materiais") {
		t.Fatal("current/no-op audience review entry hidden")
	}
	for _, want := range []string{"Publicação atual", "revisão 7", "2026-11-08 18:00", "Treinador", `value="different-current-source-version"`} {
		if !strings.Contains(body, want) {
			t.Error("current actual metadata changed", want)
		}
	}
	if strings.Contains(body, "Pré-visualizado · publicar revisão") || publicationFormFor(body, "confirm") != "" {
		t.Fatal("planner entry still directly confirms publication")
	}
	if !strings.Contains(body, "<textarea") || !strings.Contains(body, `minlength="2" maxlength="500"`) {
		t.Fatal("planner summary semantics changed")
	}
	p.Publications[0].PrescriptionCount = 0
	p.Publications[0].AthleteCount = 0
	p.Publications[0].UnchangedCount = 0
	if publicationFormFor(renderPublicationComponent(t, structuredPublicationManagement(p)), "preview") == "" {
		t.Fatal("published empty current revision lost no-op review entry")
	}
	p.Publications[0].Revision = 0
	if publicationFormFor(renderPublicationComponent(t, structuredPublicationManagement(p)), "preview") != "" {
		t.Fatal("empty first week offered publish entry")
	}
	p.Publications[0].PrescriptionCount = 3
	p.Publications[0].ConflictCount = 1
	if publicationFormFor(renderPublicationComponent(t, structuredPublicationManagement(p)), "preview") != "" {
		t.Fatal("conflicted week offered review as publishable")
	}
	p.Publications[0].ConflictCount = 0
	p.Publications[0].Unavailable = true
	if publicationFormFor(renderPublicationComponent(t, structuredPublicationManagement(p)), "preview") != "" {
		t.Fatal("unavailable week offered publication")
	}
}

func TestPublicationReviewLongNamesRemainDistinct(t *testing.T) {
	p := publicationReviewFixture()
	name := strings.Repeat("Nome", 30)
	p.Recipients = []TrainingPublicationRecipient{{Name: name, SessionCount: 1}, {Name: name, SessionCount: 2}}
	body := renderPublicationReview(t, p)
	if strings.Count(body, name) != 2 {
		t.Fatal("same displayed name for distinct subjects was collapsed")
	}
	if !strings.Contains(body, `<p class="record-item__copy"><strong>`) {
		t.Fatal("long recipient name lacks established wrapping container")
	}
}
