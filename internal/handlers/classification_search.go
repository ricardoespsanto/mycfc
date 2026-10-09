package handlers

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ClassificationPerson projects only task-approved data, never account/profile fields.
type ClassificationPerson struct {
	ID   uuid.UUID
	Name string
}

const classificationSearchSize = 20
const classificationMaxSearchPage = 100
const classificationMaxSearchRunes = 100

type classificationSearcher interface {
	Search(context.Context, uuid.UUID, string, int) ([]ClassificationPerson, bool, error)
}

func (s PostgresClassificationStore) Search(ctx context.Context, actor uuid.UUID, query string, page int) ([]ClassificationPerson, bool, error) {
	if s.Pool == nil {
		return nil, false, ErrClassificationDenied
	}
	var allowed bool
	if err := s.Pool.QueryRow(ctx, `SELECT `+classificationFirstScope, actor).Scan(&allowed); err != nil {
		return nil, false, err
	}
	if !allowed {
		return nil, false, ErrClassificationDenied
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, false, nil
	} // Never return a blank-query roster.
	if utf8.RuneCountInString(query) > classificationMaxSearchRunes || page < 1 || page > classificationMaxSearchPage {
		return nil, false, nil
	}
	// strpos treats %, _ and backslashes literally. Authorization and subject
	// eligibility run inside the SQL filter, before the bounded LIMIT/OFFSET.
	rows, err := s.Pool.Query(ctx, `SELECT member.id,member.name FROM users member WHERE `+classificationSubjectScope+` AND strpos(lower(member.name),lower($2))>0 ORDER BY lower(member.name),member.id LIMIT $3 OFFSET $4`, actor, query, classificationSearchSize+1, (page-1)*classificationSearchSize)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	people := []ClassificationPerson{}
	for rows.Next() {
		var p ClassificationPerson
		if err = rows.Scan(&p.ID, &p.Name); err != nil {
			return nil, false, err
		}
		people = append(people, p)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(people) > classificationSearchSize && page < classificationMaxSearchPage
	if len(people) > classificationSearchSize {
		people = people[:classificationSearchSize]
	}
	return people, more, nil
}

func classificationSearchURL(query string, page int) string {
	values := url.Values{}
	if query != "" {
		values.Set("q", query)
	}
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	if len(values) == 0 {
		return "/equipa/classificacao"
	}
	return "/equipa/classificacao?" + values.Encode()
}

func (h Classification) Search(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	actor, ok := currentUser(r.Context())
	store, hasSearch := h.Store.(classificationSearcher)
	if !ok || actor.ID == uuid.Nil || !hasSearch {
		http.NotFound(w, r)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	page := 1
	if raw := r.URL.Query().Get("page"); raw != "" {
		var err error
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 || page > classificationMaxSearchPage {
			page = 1
		}
	}
	message := ""
	submitted := query != ""
	searchQuery := query
	if utf8.RuneCountInString(query) > classificationMaxSearchRunes {
		message = "Indique um nome com até 100 caracteres."
		searchQuery = ""
	}
	people, more, err := store.Search(r.Context(), actor.ID, searchQuery, page)
	status := http.StatusOK
	if errors.Is(err, ErrClassificationDenied) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		message = "Não foi possível pesquisar. Tente novamente."
		status = http.StatusServiceUnavailable
	}
	previous, next := "", ""
	if page > 1 && searchQuery != "" {
		previous = classificationSearchURL(query, page-1)
	}
	if more {
		next = classificationSearchURL(query, page+1)
	}
	data := struct {
		Query, Message, Previous, Next string
		People                         []ClassificationPerson
		Submitted                      bool
	}{query, message, previous, next, people, submitted}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = classificationSearchTemplate.Execute(w, data)
}

var classificationSearchTemplate = template.Must(template.New("classification-search").Funcs(template.FuncMap{"selectURL": func(id uuid.UUID, q string) string {
	return "/equipa/classificacao/" + id.String() + "?" + url.Values{"q": {q}}.Encode()
}}).Parse(`<!doctype html><html lang="pt-PT"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Classificação | MyCFCoimbra</title><link rel="stylesheet" href="/equipa/classificacao.css"><main><h1>Classificação</h1><p>Pesquise pelo nome e selecione uma pessoa para classificar. Esta pesquisa não dá acesso ao perfil.</p><form method="get" action="/equipa/classificacao"><label for="person-name">Nome da pessoa</label><input id="person-name" name="q" type="search" value="{{.Query}}" maxlength="100" autofocus><button type="submit">Pesquisar</button></form>{{if .Message}}<p role="alert">{{.Message}}</p>{{else if .People}}<section aria-label="Pessoas disponíveis para classificação"><h2>Resultados</h2><ul>{{range .People}}<li><a href="{{selectURL .ID $.Query}}">Selecionar {{.Name}}</a></li>{{end}}</ul></section>{{else if .Submitted}}<p role="status">Não foram encontradas pessoas disponíveis para classificação com este nome.</p>{{end}}{{if or .Previous .Next}}<nav aria-label="Páginas de resultados">{{if .Previous}}<a href="{{.Previous}}">Anterior</a>{{end}} {{if .Next}}<a href="{{.Next}}">Seguinte</a>{{end}}</nav>{{end}}<p><a href="/today">Voltar à Coordenação</a></p></main></html>`))
