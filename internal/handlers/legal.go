package handlers

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	legalcontent "github.com/cfcoimbra/mycfc/docs/legal"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/yuin/goldmark/v2/extension"
	"github.com/yuin/goldmark/v2/parser"
	markdownhtml "github.com/yuin/goldmark/v2/renderer/html"
)

type Legal struct {
	PageMeta  components.PageMeta
	Documents map[string]legalcontent.Document
	Versions  map[string]map[string]legalcontent.Document
}

func NewLegal(meta components.PageMeta) Legal {
	return Legal{PageMeta: meta, Documents: legalcontent.Documents(), Versions: legalcontent.Versions()}
}

func (h Legal) Get(w http.ResponseWriter, r *http.Request) {
	document, ok := h.Documents[r.PathValue("slug")]
	if version := strings.TrimSpace(r.PathValue("version")); version != "" {
		document, ok = h.Versions[r.PathValue("slug")][version]
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	p := parser.New(parser.WithExtensions(extension.GFMParser))
	renderer := markdownhtml.New(markdownhtml.WithExtensions(extension.GFMHTMLRenderer))
	var rendered bytes.Buffer
	if err := renderer.Render(&rendered, document.Markdown, p.Parse(document.Markdown)); err != nil {
		http.Error(w, "Não foi possível apresentar o documento.", http.StatusInternalServerError)
		return
	}
	meta := h.PageMeta
	meta.Title = document.Title + " | MyCFCoimbra"
	meta.CurrentPath = r.URL.Path
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("ETag", `"`+document.SHA256+`"`)
	if strings.TrimSpace(r.PathValue("version")) != "" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	_ = pages.Legal(pages.LegalPage{Meta: meta, Title: document.Title, Version: document.Version, SHA256: document.SHA256, Content: templ.Raw(rendered.String())}).Render(r.Context(), w)
}
