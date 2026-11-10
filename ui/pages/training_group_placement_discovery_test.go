package pages

import (
	"html"
	"regexp"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/validation"
)

func placementDiscoveryFixture() TrainingGroupsPage {
	return TrainingGroupsPage{Mode: "add", ActionURL: "/groups/g/atletas", ReturnURL: "/groups/g?week_id=w&day=2027-01-11", ReviewURL: "/groups/g/atletas?q=Ana&season_id=explicit-season&effective_on=2027-01-11", Group: TrainingGroupRow{Name: "Grupo autorizado", ScopeName: "Competição"}, WeekID: "w", ContextDay: "2027-01-11", SearchPage: 2, SearchPerformed: true, Seasons: []TrainingPlacementSeason{{ID: "explicit-season", Name: "Época sintética configurada"}}, Form: TrainingGroupForm{Query: "Ana", Version: "original-version", SeasonID: "explicit-season", EffectiveOn: "2027-01-11", CommandID: "original-command", SelectionProof: `opaque-original-proof<&"`, SelectedIDs: map[string]bool{"future-episode": true}}, Results: []TrainingGroupAthleteChoice{{MembershipID: "future-episode", PersonID: "actual-person", Name: "Ana elegível futura", PlacementURL: "/placements?group_id=g&person_id=actual-person&season_id=explicit-season&effective_on=2027-01-11"}, {MembershipID: "existing-episode", Name: "Ana já colocada", AlreadyMember: true}, {MembershipID: "unavailable-episode", Name: "Ana indisponível", Unavailable: true}}}
}
func checkDiscoveryIDsAndForms(t *testing.T, body string) {
	t.Helper()
	ids := map[string]bool{}
	for _, match := range regexp.MustCompile(`\sid="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		if ids[match[1]] {
			t.Fatalf("duplicate rendered ID %s", match[1])
		}
		ids[match[1]] = true
	}
	for _, tag := range publicationTags(body, "label") {
		if target, ok := publicationAttr(tag, "for"); ok && !ids[target] {
			t.Fatalf("missing label target %q", target)
		}
	}
	for _, tag := range append(publicationTags(body, "input"), publicationTags(body, "select")...) {
		if described, ok := publicationAttr(tag, "aria-describedby"); ok {
			for _, target := range strings.Fields(described) {
				if !ids[target] {
					t.Fatalf("missing feedback/help target %s", target)
				}
			}
		}
	}
	forms := publicationForms(body)
	if len(forms) != strings.Count(body, "<form ") || len(forms) != strings.Count(body, "</form>") {
		t.Fatal("forms nested or incomplete")
	}
	for _, form := range forms {
		if strings.Count(form, "<form ") != 1 {
			t.Fatal("nested form")
		}
	}
	for _, label := range regexp.MustCompile(`(?s)<label\b[^>]*>.*?</label>`).FindAllString(body, -1) {
		if strings.Contains(label, "<a ") {
			t.Fatal("placement navigation must be outside checkbox labels")
		}
	}
}
func discoveryForm(body, method string) string {
	for _, form := range publicationForms(body) {
		tag := publicationTags(form, "form")[0]
		if actual, _ := publicationAttr(tag, "method"); actual == method {
			return form
		}
	}
	return ""
}
func TestTrainingGroupFutureDiscoveryPreservesNativeSearchAndBulkContext(t *testing.T) {
	p := placementDiscoveryFixture()
	body := renderTrainingGroups(t, p)
	checkDiscoveryIDsAndForms(t, body)
	get, post := discoveryForm(body, "get"), discoveryForm(body, "post")
	if get == "" || post == "" {
		t.Fatal("missing separate GET search and POST multi-select")
	}
	for _, f := range []string{get, post} {
		for n, v := range map[string]string{"return_to": p.ReturnURL, "week_id": p.WeekID, "day": p.ContextDay, "page": "2"} {
			publicationHidden(t, f, n, v)
		}
	}
	for n, v := range map[string]string{"csrf_token": "fixture", "version": p.Form.Version, "q": p.Form.Query, "season_id": p.Form.SeasonID, "effective_on": p.Form.EffectiveOn, "command_id": p.Form.CommandID, "selection_proof": p.Form.SelectionProof} {
		publicationHidden(t, post, n, v)
	}
	if strings.Contains(get, "selection_proof") || strings.Contains(get, "opaque-original-proof") {
		t.Fatal("opaque bulk selection proof must never enter GET search")
	}
	for _, link := range publicationTags(body, "a") {
		if href, _ := publicationAttr(link, "href"); strings.Contains(href, "selection_proof") || strings.Contains(href, "opaque-original-proof") {
			t.Fatal("opaque bulk selection proof must never enter navigation URLs")
		}
	}
	if strings.Contains(get, `name="command_id"`) || !strings.Contains(get, `name="q" type="search"`) || !strings.Contains(get, `value="explicit-season" selected`) || !strings.Contains(get, `name="effective_on" type="date" value="2027-01-11"`) {
		t.Fatal("explicit search context missing or command moved into navigation")
	}
	if !strings.Contains(post, `value="future-episode" checked`) || !strings.Contains(post, `value="existing-episode" disabled`) || !strings.Contains(post, `value="unavailable-episode" disabled`) {
		t.Fatal("existing membership checkbox names/selection/protection changed")
	}
	if !strings.Contains(body, "Preparar colocação") || !strings.Contains(body, `person_id=actual-person&amp;season_id=explicit-season&amp;effective_on=2027-01-11`) || strings.Contains(body, `name="person_id"`) {
		t.Fatal("actual scoped person navigation missing or replaced by manual ID entry")
	}
	p.Form.Errors = validation.FieldErrors{"members": "Reveja a seleção."}
	body = renderTrainingGroups(t, p)
	checkDiscoveryIDsAndForms(t, body)
	post = discoveryForm(body, "post")
	if !strings.Contains(body, p.Results[0].Name) || !strings.Contains(post, `value="future-episode" checked`) {
		t.Fatal("bulk validation lost actual eligible results or selected member")
	}
	publicationHidden(t, post, "command_id", p.Form.CommandID)
	publicationHidden(t, post, "selection_proof", p.Form.SelectionProof)
}
func TestTrainingGroupDiscoveryDoesNotInventDateSeasonOrExposeNamesBeforeSelection(t *testing.T) {
	for _, kind := range []string{"blank-season", "blank-date", "invalid-date", "unknown-season", "blank-name", "not-searched", "context-error"} {
		t.Run(kind, func(t *testing.T) {
			p := placementDiscoveryFixture()
			p.Seasons[0].Selected = true
			switch kind {
			case "blank-season":
				p.Form.SeasonID = ""
			case "blank-date":
				p.Form.EffectiveOn = ""
			case "invalid-date":
				p.Form.EffectiveOn = "0000-01-01"
			case "unknown-season":
				p.Form.SeasonID = "own-unavailable-season"
			case "blank-name":
				p.Form.Query = "  "
			case "not-searched":
				p.SearchPerformed = false
			case "context-error":
				p.Form.Errors = validation.FieldErrors{"season_id": "Reveja a época."}
			}
			body := renderTrainingGroups(t, p)
			checkDiscoveryIDsAndForms(t, body)
			for _, name := range []string{p.Results[0].Name, "future-episode", "Preparar colocação"} {
				if strings.Contains(body, name) {
					t.Fatalf("incomplete discovery disclosed %q", name)
				}
			}
			if kind == "blank-season" && (!strings.Contains(body, `value="" selected>Selecione a época`) || strings.Contains(body, `value="explicit-season" selected`)) {
				t.Fatal("UI chose season from available/Selected metadata")
			}
			if kind == "blank-date" && !strings.Contains(body, `name="effective_on" type="date" value=""`) {
				t.Fatal("UI invented a date")
			}
		})
	}
}
func TestTrainingGroupDiscoveryRetainsInvalidOwnContextWithQualifiedErrors(t *testing.T) {
	p := placementDiscoveryFixture()
	p.Form.SeasonID = "own-unknown-season"
	p.Form.EffectiveOn = "not-a-date"
	p.Form.Errors = validation.FieldErrors{"season_id": "Reveja a época indicada.", "effective_on": "Reveja a data indicada."}
	body := renderTrainingGroups(t, p)
	checkDiscoveryIDsAndForms(t, body)
	for _, want := range []string{`value="own-unknown-season" selected`, `name="effective_on" type="text" value="not-a-date"`, `href="#training-group-search-season"`, `href="#training-group-search-date"`, `id="training-group-search-season-error"`, `id="training-group-search-date-error"`, `autofocus`} {
		if !strings.Contains(body, want) {
			t.Fatalf("raw/context validation lost %q", want)
		}
	}
	post := discoveryForm(body, "post")
	publicationHidden(t, post, "season_id", p.Form.SeasonID)
	publicationHidden(t, post, "effective_on", p.Form.EffectiveOn)
	publicationHidden(t, post, "command_id", p.Form.CommandID)
}
func TestTrainingGroupRosterOffersOnlyProvidedNativePlacementLink(t *testing.T) {
	p := placementDiscoveryFixture()
	p.Mode = "detail"
	p.Roster = []TrainingGroupAthleteChoice{{Name: "Pessoa já no grupo", PersonID: "actual-person", PlacementURL: "/placements?group_id=g&person_id=actual-person"}, {Name: "Sem ação permitida"}}
	body := renderTrainingGroups(t, p)
	checkDiscoveryIDsAndForms(t, body)
	if strings.Count(body, "Rever colocação") != 1 || !strings.Contains(body, `href="/placements?group_id=g&amp;person_id=actual-person"`) {
		t.Fatal("roster navigation must use only optional scoped backend URLs")
	}
	for _, status := range []string{"Em vigor", "Agendada para 2027-01-18", "Em vigor; tem colocações agendadas", "Intervalo sem participação <rever>"} {
		p.Roster[0].PlacementStatus = status
		body = renderTrainingGroups(t, p)
		checkDiscoveryIDsAndForms(t, body)
		if !strings.Contains(body, html.EscapeString(status)) {
			t.Fatalf("roster lost actual provided placement status %q", status)
		}
		if strings.Contains(body, "<rever>") {
			t.Fatal("provided placement metadata must be escaped")
		}
	}
}
