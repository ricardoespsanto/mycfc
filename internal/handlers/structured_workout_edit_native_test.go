package handlers

import (
	"bytes"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
)

// Successful controls are extracted from the actual rendered editor, URL encoded
// and parsed by net/http, so omitted hidden identity fields cannot be bypassed by
// an in-memory draft shortcut.
func focusedRenderedEditor(t *testing.T, page pages.StructuredWorkoutTaskPage) url.Values {
	t.Helper()
	var out bytes.Buffer
	if err := pages.StructuredWorkoutTask(page).Render(t.Context(), &out); err != nil {
		t.Fatal(err)
	}
	form := ""
	for _, match := range regexp.MustCompile(`(?s)<form\b[^>]*>(.*?)</form>`).FindAllStringSubmatch(out.String(), -1) {
		if strings.Contains(match[1], `name="activity_order"`) {
			form = match[1]
			break
		}
	}
	if form == "" {
		t.Fatal("native editor absent")
	}
	attr := func(tag, name string) string {
		m := regexp.MustCompile(`\b` + name + `="([^"]*)"`).FindStringSubmatch(tag)
		if len(m) == 2 {
			return html.UnescapeString(m[1])
		}
		return ""
	}
	fields := url.Values{}
	for _, tag := range regexp.MustCompile(`<input\b[^>]*>`).FindAllString(form, -1) {
		if name := attr(tag, "name"); name != "" && !strings.Contains(tag, " disabled") {
			fields.Add(name, attr(tag, "value"))
		}
	}
	for _, m := range regexp.MustCompile(`(?s)<textarea\b([^>]*)>(.*?)</textarea>`).FindAllStringSubmatch(form, -1) {
		fields.Add(attr(m[1], "name"), html.UnescapeString(m[2]))
	}
	for _, m := range regexp.MustCompile(`(?s)<select\b([^>]*)>(.*?)</select>`).FindAllStringSubmatch(form, -1) {
		options := regexp.MustCompile(`<option\b([^>]*)>`).FindAllStringSubmatch(m[2], -1)
		if len(options) == 0 {
			continue
		}
		chosen := options[0][1]
		for _, o := range options {
			if strings.Contains(o[1], " selected") {
				chosen = o[1]
				break
			}
		}
		fields.Add(attr(m[1], "name"), attr(chosen, "value"))
	}
	request := httptest.NewRequest(http.MethodPost, page.DraftURL, strings.NewReader(fields.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	return request.PostForm
}
func TestFocusedEditNativeRemovedGroupIdentitySurvivesNextCommandAndRestore(t *testing.T) {
	source := focusedEditFixture(t)
	water := source.Graph.Activities[0].Phases[0].Water
	block := source.Raw.Blocks[0].ID
	// Two existing groups separated by an effort reproduce the native journey:
	// remove the last root+child, then move the retained effort before the first.
	oldRoot, oldChild := water.Nodes[0], water.Nodes[1]
	effort := focusedGraphWaterNodeInput{Key: "n_3", ID: uuid.New(), Kind: "EFFORT", Name: "Técnica suave", Instructions: "Manter a técnica."}
	root := oldRoot
	root.Key, root.ID = "n_4", uuid.New()
	child := oldChild
	child.Key, child.ID, child.ParentKey = "n_5", uuid.New(), "n_4"
	water.Nodes = append(water.Nodes, effort, root, child)
	stamp := source.Raw.Session.UpdatedAt
	source.Raw.Steps = append(source.Raw.Steps, dbgen.WaterWorkStep{ID: effort.ID, BlockID: block, Position: 2, Kind: effort.Kind, Name: effort.Name, Instructions: effort.Instructions, CreatedAt: stamp, UpdatedAt: stamp})
	rawRoot, rawChild := source.Raw.Steps[0], source.Raw.Steps[1]
	rawRoot.ID, rawRoot.Position = root.ID, 3
	rawChild.ID, rawChild.ParentStepID = child.ID, &root.ID
	source.Raw.Steps = append(source.Raw.Steps, rawRoot, rawChild)
	draft, err := focusedEditLosslessDraft(source)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := applyFocusedEditCommand(draft, "water_remove:a_1:p_1:n_4", source.Graph)
	if err != nil {
		t.Fatal(err)
	}
	h := StructuredWorkout{Store: &focusedRecoveryStore{source: source.Context}}
	request := httptest.NewRequest(http.MethodGet, "/edit", nil)
	fields := focusedRenderedEditor(t, h.editPage(request, CurrentUser{ID: source.Context.ActorID}, source, removed))
	tomb := fields.Get("removed_order")
	if fields.Get("removed["+tomb+"].id") != root.ID.String() {
		t.Fatal("rendered tombstone lost removed saved root identity")
	}
	if !strings.Contains(fields.Get("removed["+tomb+"].payload"), child.ID.String()) {
		t.Fatal("lost removed descendant identity")
	}
	parsed, err := parseFocusedEditGraph(fields, source.Graph)
	if err != nil {
		t.Fatal("rendered removal could not be posted", err)
	}
	moved, err := applyFocusedEditCommand(parsed, "water_up:a_1:p_1:n_3", source.Graph)
	if err != nil {
		t.Fatal("native next command", err)
	}
	fields = focusedRenderedEditor(t, h.editPage(request, CurrentUser{ID: source.Context.ActorID}, source, moved))
	parsed, err = parseFocusedEditGraph(fields, source.Graph)
	if err != nil {
		t.Fatal("second native post", err)
	}
	restored, err := applyFocusedEditCommand(parsed, "restore:"+tomb, source.Graph)
	if err != nil {
		t.Fatal("native restore", err)
	}
	fields = focusedRenderedEditor(t, h.editPage(request, CurrentUser{ID: source.Context.ActorID}, source, restored))
	final, err := parseFocusedEditGraph(fields, source.Graph)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Removed) != 0 || final.Values.Get(focusedNodePrefix("a_1", "p_1", "n_4")+".id") != root.ID.String() || final.Values.Get(focusedNodePrefix("a_1", "p_1", "n_5")+".id") != child.ID.String() {
		t.Fatal("restore changed root/child identity")
	}
	if _, err = parseFocusedGraph(fields); err == nil {
		t.Fatal("CREATE strictness weakened for saved identities")
	}
}
