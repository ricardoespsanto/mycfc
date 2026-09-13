package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/jackc/pgx/v5"
)

var eventResultsURLPattern = regexp.MustCompile(`^https://(www\.)?fpcanoagem\.pt(/[A-Za-z0-9._~!$&'()*+,;=:@%/-]*)?$`)
var eventResultsUnsafeEscape = regexp.MustCompile(`(?i)%(0[0-9a-f]|1[0-9a-f]|7f|5c|3f|23|2f|25)`)

// This is an outbound link only. Never fetch, preview or follow the URL here.
// Queries/fragments and encoded delimiters are excluded to avoid token links.
func validEventResultsURL(value string) bool {
	if len(value) > 2048 || !eventResultsURLPattern.MatchString(value) || eventResultsUnsafeEscape.MatchString(value) {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || !utf8.ValidString(parsed.Path) || strings.ContainsFunc(parsed.Path, unicode.IsControl) {
		return false
	}
	return err == nil && parsed.Scheme == "https" && parsed.User == nil && parsed.Port() == "" &&
		(parsed.Host == "fpcanoagem.pt" || parsed.Host == "www.fpcanoagem.pt") && parsed.RawQuery == "" && !parsed.ForceQuery && parsed.Fragment == ""
}

func validatedEventResultsURL(value string) string {
	if validEventResultsURL(value) {
		return value
	}
	return ""
}

func (h Events) ResultsLink(w http.ResponseWriter, r *http.Request) {
	h.resultsLink(w, r, false)
}

func (h Events) UpdateResultsLink(w http.ResponseWriter, r *http.Request) {
	h.resultsLink(w, r, true)
}

func (h Events) resultsLink(w http.ResponseWriter, r *http.Request, update bool) {
	user, ok := CurrentUserFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.System.Forbidden(w, r)
		return
	}
	eventID, ok := h.eventID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), eventQueryTimeout)
	defer cancel()
	event, err := h.Store.GetEventResultsLink(ctx, eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if event.EventType != "COMPETITION" {
		http.Error(w, "Só as competições podem ter uma ligação de resultados.", http.StatusConflict)
		return
	}
	page := pages.EventResultsPage{
		Meta: h.meta(r, user, "/admin/eventos", "Gerir ligação de resultados"),
		ID:   event.ID.String(), Title: event.Title, URL: stringValue(event.OfficialResultsUrl),
		Version: strconv.FormatInt(event.ResultsVersion, 10), Errors: validation.FieldErrors{},
		CSRFField: templ.Raw(string(csrf.TemplateField(r))),
	}
	status := http.StatusOK
	if update {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Pedido inválido.", http.StatusBadRequest)
			return
		}
		page.URL = r.PostForm.Get("results_url") // Keep the exact draft on invalid/conflicting submissions.
		page.Version = r.PostForm.Get("expected_version")
		value := strings.Trim(page.URL, " ")
		if value != "" && !validEventResultsURL(value) {
			page.Errors.Add("results_url", "Introduza uma ligação HTTPS válida de fpcanoagem.pt ou www.fpcanoagem.pt, sem parâmetros nem fragmentos.")
		}
		expected, parseErr := strconv.ParseInt(page.Version, 10, 64)
		if parseErr != nil || expected < 0 {
			page.Errors.Add("state", "O formulário deixou de ser válido. Carregue novamente a página.")
		}
		status = http.StatusUnprocessableEntity
		if page.Errors.Empty() {
			var link *string
			if value != "" {
				link = &value
			}
			count, err := h.Store.UpdateEventResultsLink(ctx, dbgen.UpdateEventResultsLinkParams{
				ID: eventID, OfficialResultsUrl: link, ActorUserID: &user.ID, ExpectedVersion: expected,
			})
			if err != nil {
				h.System.InternalError(w, r)
				return
			}
			if count == 0 {
				status = http.StatusConflict
				page.Conflict = "A ligação ou o evento foram alterados entretanto, ou a sua autorização mudou. Reveja a versão atual antes de guardar novamente."
			} else {
				message := "Ligação de resultados guardada."
				if value == "" {
					message = "Ligação de resultados removida."
				}
				h.flash(r, message)
				httpx.Redirect(w, r, "/admin/eventos/"+eventID.String(), http.StatusSeeOther)
				return
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.EventResults(page).Render(r.Context(), w)
}
