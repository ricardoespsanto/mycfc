package pages

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/ui/components"
)

func TestProfilePhotoRemovalRendersAnExplicitDestructiveConfirmation(t *testing.T) {
	page := ProfilePhotoRemovalPage{
		Meta:       components.PageMeta{Title: "Remover fotografia | MyCFC", CSRFField: templ.Raw("")},
		Name:       "Ana Silva",
		ActionPath: "/perfil/fotografia/remover",
		ReturnURL:  "/perfil",
	}

	var output bytes.Buffer
	if err := ProfilePhotoRemoval(page).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{
		"<h1 id=\"remove-profile-photo-title\">Remover fotografia",
		"Ana Silva",
		"removida definitivamente",
		"voltará às iniciais",
		"name=\"confirm_removal\" value=\"yes\"",
		"Remover fotografia definitivamente",
		"href=\"/perfil\"",
	} {
		if !strings.Contains(html, expected) {
			t.Fatalf("photo-removal confirmation missing %q: %s", expected, html)
		}
	}
}
