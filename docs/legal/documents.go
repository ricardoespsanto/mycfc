// Package legal provides the approved, immutable source texts served by MyCFCoimbra.
package legal

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
)

const CurrentVersion = "2026-09-06"
const HealthConsentStatement = "Consinto explicitamente o tratamento da informação médica nova ou alterada que introduzo, para segurança e adaptação da participação."

//go:embed versions/*/*.md
var sources embed.FS

type Document struct {
	Slug, Filename, Title, Version, SHA256 string
	Markdown                               []byte
}

var definitions = []struct{ slug, filename, title, version string }{
	{"privacidade", "politica-privacidade.md", "Política de privacidade", "2026-09-06"},
	{"termos-gerais", "termos-gerais.md", "Termos gerais", "2026-09-06"},
	{"cookies", "politica-cookies.md", "Política de cookies", "2026-09-06"},
	{"uso-imagem", "autorizacao-imagem.md", "Autorização de imagem e voz", "2026-09-06"},
	{"responsabilidade-menor", "responsabilidade-menor.md", "Responsabilidade por menor", "2026-09-06"},
	{"direitos", "exercicio-direitos.md", "Exercer direitos", "2026-09-06"},
	{"privacidade-menores", "privacidade-menores.md", "Privacidade para menores", "2026-09-06"},
}

func Documents() map[string]Document {
	documents := make(map[string]Document, len(definitions))
	for _, definition := range definitions {
		if definition.version != CurrentVersion {
			continue
		}
		source, err := sources.ReadFile("versions/" + definition.version + "/" + definition.filename)
		if err != nil {
			panic(err)
		}
		digest := sha256.Sum256(source)
		if definition.slug == "privacidade" && !bytes.Contains(source, []byte(HealthConsentStatement)) {
			panic("privacy notice does not contain the exact health-consent statement")
		}
		documents[definition.slug] = Document{Slug: definition.slug, Filename: definition.filename, Title: definition.title, Version: definition.version, SHA256: hex.EncodeToString(digest[:]), Markdown: source}
	}
	return documents
}

func Versions() map[string]map[string]Document {
	versions := make(map[string]map[string]Document)
	for _, definition := range definitions {
		source, err := sources.ReadFile("versions/" + definition.version + "/" + definition.filename)
		if err != nil {
			panic(err)
		}
		digest := sha256.Sum256(source)
		if definition.slug == "privacidade" && !bytes.Contains(source, []byte(HealthConsentStatement)) {
			panic("privacy notice does not contain the exact health-consent statement")
		}
		if versions[definition.slug] == nil {
			versions[definition.slug] = make(map[string]Document)
		}
		versions[definition.slug][definition.version] = Document{Slug: definition.slug, Filename: definition.filename, Title: definition.title, Version: definition.version, SHA256: hex.EncodeToString(digest[:]), Markdown: source}
	}
	return versions
}
