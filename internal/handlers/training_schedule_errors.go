package handlers

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/jackc/pgx/v5"
)

func (h StructuredTraining) writeTrainingScheduleError(w http.ResponseWriter, r *http.Request, err error) bool {
	err = trainingScheduleWriteError(err)
	if errors.Is(err, errTrainingScheduleUnavailable) {
		h.System.NotFound(w, r)
		return true
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	message := ""
	if errors.Is(err, ErrTrainingScheduleWriteUnsupported) || errors.Is(err, ErrTrainingSnapshotUnsupported) || errors.Is(err, ErrTrainingScheduleInvalid) {
		message = trainingScheduleDisplayError(err)
	}
	if errors.Is(err, errStructuredTrainingPublicationConflict) {
		message = "O plano mudou durante esta operação. Nada foi guardado. Reveja a semana antes de tentar novamente."
	}
	if message == "" {
		return false
	}
	// Preserve bounded submitted content as escaped text, including when the
	// authoritative source is now read-only and its mutation controls are hidden.
	retained := []string{}
	for _, field := range []struct{ key, label string }{{"title", "Título"}, {"name", "Nome"}, {"description", "Descrição"}, {"instructions", "Instruções"}, {"starts_at", "Início pedido"}, {"ends_at", "Fim pedido"}, {"week_start", "Semana pedida"}, {"first_monday", "Primeira segunda-feira"}, {"source_date", "Dia de origem"}, {"target_date", "Dia de destino"}, {"change_summary", "Resumo"}} {
		value := strings.TrimSpace(r.PostForm.Get(field.key))
		if value == "" {
			continue
		}
		if utf8.RuneCountInString(value) > 1000 {
			value = string([]rune(value)[:1000]) + "…"
		}
		retained = append(retained, field.label+": "+value)
	}
	if len(retained) > 0 {
		message += " Valores enviados: " + strings.Join(retained, " · ")
	}
	h.renderIndex(w, r, http.StatusConflict, pages.StructuredTrainingPage{Error: message, PlannerReturnURL: structuredPlannerReturn(r.PostForm.Get("return_to"))})
	return true
}
