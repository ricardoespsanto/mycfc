package handlers

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"regexp"
	"strings"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errDefinitionScope = errors.New("definition scope unavailable")
var errDefinitionDuplicate = errors.New("definition code exists")
var errDefinitionInvalid = errors.New("definition range invalid")

type DefinitionScope struct {
	SeasonID, ProgrammeID uuid.UUID
	Season, Programme     string
}
type DefinitionRow struct{ Programme, Code, Name, From, To string }
type DefinitionInput struct {
	SeasonID, ProgrammeID uuid.UUID
	Code, Name            string
	From, To              *time.Time
}
type DefinitionStore interface {
	Scopes(context.Context, uuid.UUID) ([]DefinitionScope, error)
	Create(context.Context, uuid.UUID, DefinitionInput) error
}
type Definitions struct{ Store DefinitionStore }
type PostgresDefinitionStore struct{ Pool *pgxpool.Pool }

// Programme-level grants only: a team-only coach grant never authorises a
// programme-wide definition. Scope is checked again under a row lock on write.
const definitionScopesSQL = `SELECT s.id,p.id,s.name,p.name_pt FROM seasons s CROSS JOIN programmes p
 WHERE s.is_current AND p.code IN ('Competition','Initiation') AND
 (guardian_authority_is_administrator($1) OR EXISTS (
 SELECT 1 FROM staff_grants g JOIN users u ON u.id=g.user_id
 WHERE g.user_id=$1 AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND g.capability='COACH' AND g.revoked_at IS NULL AND g.programme_id=p.id)) ORDER BY p.name_pt`

func (s PostgresDefinitionStore) Scopes(ctx context.Context, actor uuid.UUID) ([]DefinitionScope, error) {
	if s.Pool == nil {
		return nil, errDefinitionScope
	}
	rows, err := s.Pool.Query(ctx, definitionScopesSQL, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DefinitionScope
	for rows.Next() {
		var x DefinitionScope
		if err = rows.Scan(&x.SeasonID, &x.ProgrammeID, &x.Season, &x.Programme); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s PostgresDefinitionStore) Create(ctx context.Context, actor uuid.UUID, in DefinitionInput) error {
	if s.Pool == nil {
		return errDefinitionScope
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current bool
	var seasonStart time.Time
	err = tx.QueryRow(ctx, `SELECT s.is_current AND p.code IN ('Competition','Initiation'), s.starts_on FROM seasons s CROSS JOIN programmes p WHERE s.id=$1 AND p.id=$2 FOR SHARE OF s,p`, in.SeasonID, in.ProgrammeID).Scan(&current, &seasonStart)
	if errors.Is(err, pgx.ErrNoRows) || !current && err == nil {
		return errDefinitionScope
	}
	if err != nil {
		return err
	}
	var admin bool
	if in.From != nil && in.From.After(seasonStart) || in.To != nil && in.To.After(seasonStart) || in.From != nil && in.To != nil && in.From.After(*in.To) {
		return errDefinitionInvalid
	}
	if err = tx.QueryRow(ctx, `SELECT guardian_authority_is_administrator($1)`, actor).Scan(&admin); err != nil {
		return err
	}
	if !admin {
		// A concurrent revocation must wait until the definition is committed.
		var grant uuid.UUID
		err = tx.QueryRow(ctx, `SELECT g.id FROM staff_grants g JOIN users u ON u.id=g.user_id WHERE g.user_id=$1 AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent AND g.capability='COACH' AND g.programme_id=$2 AND g.revoked_at IS NULL LIMIT 1 FOR SHARE OF g,u`, actor, in.ProgrammeID).Scan(&grant)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDefinitionScope
		}
		if err != nil {
			return err
		}
	}
	var from, to any
	if in.From != nil {
		from = *in.From
	}
	if in.To != nil {
		to = *in.To
	}
	_, err = tx.Exec(ctx, `INSERT INTO competition_categories(season_id,programme_id,code,name_pt,birth_date_from,birth_date_to,approved_by_user_id,approved_at) VALUES($1,$2,$3,$4,$5,$6,$7,now())`, in.SeasonID, in.ProgrammeID, in.Code, in.Name, from, to, actor)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" {
		return errDefinitionDuplicate
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var definitionCode = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

func (h Definitions) Get(w http.ResponseWriter, r *http.Request) { h.render(w, r, 200, "", "", "") }
func (h Definitions) Post(w http.ResponseWriter, r *http.Request) {
	actor, ok := currentUser(r.Context())
	if !ok || actor.ID == uuid.Nil {
		http.NotFound(w, r)
		return
	}
	if h.Store == nil {
		http.Error(w, "Serviço indisponível", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido", 400)
		return
	}
	// Never accept provenance, membership, or a scope split across unrelated fields.
	for _, key := range []string{"actor_id", "approved_by_user_id", "approved_at", "member_id", "category_id", "age_exception_reason"} {
		if r.PostForm.Has(key) {
			h.render(w, r, 422, "Campos não permitidos.", "", "")
			return
		}
	}
	scope := r.PostForm.Get("scope")
	code := strings.TrimSpace(r.PostForm.Get("code"))
	name := strings.TrimSpace(r.PostForm.Get("name"))
	parts := strings.Split(scope, ":")
	if len(parts) != 2 {
		h.render(w, r, 422, "Escolha a época e o programa.", scope, code)
		return
	}
	season, e1 := uuid.Parse(parts[0])
	programme, e2 := uuid.Parse(parts[1])
	if e1 != nil || e2 != nil {
		http.NotFound(w, r)
		return
	}
	scopes, err := h.Store.Scopes(r.Context(), actor.ID)
	if err != nil {
		http.Error(w, "Serviço indisponível", http.StatusServiceUnavailable)
		return
	}
	allowed := false
	for _, v := range scopes {
		if v.SeasonID == season && v.ProgrammeID == programme {
			allowed = true
		}
	}
	if !allowed {
		http.NotFound(w, r)
		return
	}
	if len(code) == 0 || len(code) > 40 || !definitionCode.MatchString(code) || len([]rune(name)) < 2 || len([]rune(name)) > 120 || name != r.PostForm.Get("name") {
		h.render(w, r, 422, "Indique um código e nome válidos.", scope, code)
		return
	}
	var from, to *time.Time
	for _, item := range []struct {
		raw  string
		dest **time.Time
	}{{r.PostForm.Get("birth_date_from"), &from}, {r.PostForm.Get("birth_date_to"), &to}} {
		if item.raw != "" {
			v, e := time.Parse("2006-01-02", item.raw)
			if e != nil || v.Format("2006-01-02") != item.raw {
				h.render(w, r, 422, "Verifique as datas de nascimento.", scope, code)
				return
			}
			*item.dest = &v
		}
	}
	if from != nil && to != nil && from.After(*to) {
		h.render(w, r, 422, "O início do intervalo deve ser anterior ao fim.", scope, code)
		return
	}
	err = h.Store.Create(r.Context(), actor.ID, DefinitionInput{season, programme, code, name, from, to})
	if errors.Is(err, errDefinitionScope) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, errDefinitionDuplicate) {
		h.render(w, r, 409, "Já existe um escalão com este código nesta época e programa.", scope, code)
		return
	}
	if errors.Is(err, errDefinitionInvalid) {
		h.render(w, r, 422, "As datas de nascimento não podem ser posteriores ao início da época.", scope, code)
		return
	}
	if err != nil {
		http.Error(w, "Não foi possível guardar o escalão.", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, "/equipa/escaloes", http.StatusSeeOther)
}
func definitionErrorTarget(status int, message string) string {
	switch {
	case status == http.StatusConflict:
		return "code"
	case strings.Contains(message, "datas") || strings.Contains(message, "início do intervalo"):
		return "birth_date_from"
	case strings.Contains(message, "código e nome"):
		return "code"
	default:
		return "scope"
	}
}
func (h Definitions) render(w http.ResponseWriter, r *http.Request, status int, message, scope, code string) {
	actor, ok := currentUser(r.Context())
	if !ok || actor.ID == uuid.Nil {
		http.NotFound(w, r)
		return
	}
	if h.Store == nil {
		http.Error(w, "Serviço indisponível", http.StatusServiceUnavailable)
		return
	}
	scopes, err := h.Store.Scopes(r.Context(), actor.ID)
	if err != nil {
		http.Error(w, "Serviço indisponível", http.StatusServiceUnavailable)
		return
	}
	if len(scopes) == 0 {
		http.NotFound(w, r)
		return
	}
	// Read only definitions within scopes resolved from the authenticated actor.
	var definitions []DefinitionRow
	if store, ok := h.Store.(interface {
		List(context.Context, uuid.UUID) ([]DefinitionRow, error)
	}); ok {
		definitions, err = store.List(r.Context(), actor.ID)
		if err != nil {
			http.Error(w, "Serviço indisponível", http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = definitionTemplate.Execute(w, struct {
		Scopes                                                  []DefinitionScope
		Rows                                                    []DefinitionRow
		Message, ErrorTarget, Scope, Code, Name, From, To, CSRF string
	}{scopes, definitions, message, definitionErrorTarget(status, message), scope, code, r.PostForm.Get("name"), r.PostForm.Get("birth_date_from"), r.PostForm.Get("birth_date_to"), string(csrf.Token(r))})
}
func (s PostgresDefinitionStore) List(ctx context.Context, actor uuid.UUID) ([]DefinitionRow, error) {
	rows, err := s.Pool.Query(ctx, `SELECT p.name_pt,c.code,c.name_pt,coalesce(c.birth_date_from::text,''),coalesce(c.birth_date_to::text,'') FROM competition_categories c JOIN seasons s ON s.id=c.season_id JOIN programmes p ON p.id=c.programme_id WHERE s.is_current AND p.code IN ('Competition','Initiation') AND (guardian_authority_is_administrator($1) OR EXISTS(SELECT 1 FROM staff_grants g JOIN users u ON u.id=g.user_id WHERE g.user_id=$1 AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent AND g.capability='COACH' AND g.revoked_at IS NULL AND g.programme_id=c.programme_id)) ORDER BY p.name_pt,c.name_pt`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DefinitionRow
	for rows.Next() {
		var x DefinitionRow
		if err = rows.Scan(&x.Programme, &x.Code, &x.Name, &x.From, &x.To); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

var definitionTemplate = template.Must(template.New("definitions").Parse(`<!doctype html>
<html lang="pt-PT">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Escalões da época | MyCFCoimbra</title>
<style>
*{box-sizing:border-box}body{font:1rem/1.5 system-ui;max-width:48rem;margin:auto;padding:1rem;overflow-wrap:anywhere}
label,input,select{display:block;max-width:100%}input,select{width:100%;min-height:2.75rem;margin:.3rem 0 1rem;font:inherit}
button{min-height:2.75rem;font:inherit;padding:.4rem 1rem}a,button,input,select{touch-action:manipulation}
:focus-visible,[tabindex="-1"]:focus{outline:3px solid #205493;outline-offset:3px}
.error{border:2px solid currentColor;padding:1rem;margin:1rem 0}[aria-invalid="true"]{border:2px solid #a52820}
@media(forced-colors:active){.error,[aria-invalid="true"]{border-color:CanvasText}:focus-visible,[tabindex="-1"]:focus{outline-color:Highlight}}
</style></head><body><main><h1 id="heading" tabindex="-1" {{if not .Message}}autofocus{{end}}>Escalões da época</h1>
<p><a href="/today">Voltar a Hoje</a></p>
<p>Crie uma definição para a época atual antes de classificar participantes. As datas de nascimento são inclusivas e avaliadas no início da época; não há mudança automática no aniversário. Competição exige escalão; Iniciação pode ter um. Uma definição utilizada não pode ser alterada aqui.</p>
{{if .Message}}<div role="alert" class="error" id="errors" tabindex="-1" autofocus><strong>Não foi possível guardar</strong><p><a href="#{{.ErrorTarget}}">{{.Message}}</a></p></div>{{end}}
<form method="post"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><fieldset><legend>Criar escalão</legend>
<label for="scope">Época e programa</label><select name="scope" id="scope" {{if eq .ErrorTarget "scope"}}aria-invalid="true" aria-describedby="errors"{{end}} required><option value="">Escolha</option>{{range .Scopes}}<option value="{{.SeasonID}}:{{.ProgrammeID}}" {{if eq $.Scope (printf "%s:%s" .SeasonID .ProgrammeID)}}selected{{end}}>{{.Season}} — {{.Programme}}</option>{{end}}</select>
<label for="code">Código único nesta época e programa</label><input id="code" name="code" maxlength="40" pattern="[A-Za-z][A-Za-z0-9_]*" aria-describedby="code-help{{if eq .ErrorTarget "code"}} errors{{end}}" {{if eq .ErrorTarget "code"}}aria-invalid="true"{{end}} value="{{.Code}}" required><p id="code-help">Comece por uma letra; use apenas letras sem acentos, algarismos e sublinhados.</p>
<label for="name">Nome do escalão</label><input id="name" name="name" minlength="2" maxlength="120" value="{{.Name}}" {{if eq .ErrorTarget "name"}}aria-invalid="true" aria-describedby="errors"{{end}} required>
<label for="birth_date_from">Nascimento desde (opcional)</label><input id="birth_date_from" name="birth_date_from" type="date" value="{{.From}}" {{if eq .ErrorTarget "birth_date_from"}}aria-invalid="true" aria-describedby="errors"{{end}}>
<label for="birth_date_to">Nascimento até (opcional)</label><input id="birth_date_to" name="birth_date_to" type="date" value="{{.To}}" {{if eq .ErrorTarget "birth_date_to"}}aria-invalid="true" aria-describedby="errors"{{end}}>
<button type="submit">Criar escalão</button></fieldset></form>
<section aria-labelledby="definitions-title"><h2 id="definitions-title">Definições acessíveis</h2>{{if .Rows}}<ul>{{range .Rows}}<li>{{.Programme}} — {{.Name}} ({{.Code}}): {{if .From}}{{.From}}{{else}}sem limite{{end}} — {{if .To}}{{.To}}{{else}}sem limite{{end}}</li>{{end}}</ul>{{else}}<p>Sem definições para os seus programas nesta época.</p>{{end}}</section></main></body></html>`))
