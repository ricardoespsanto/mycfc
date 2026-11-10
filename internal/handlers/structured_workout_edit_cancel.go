package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/jackc/pgx/v5"
)

func (h StructuredWorkout) CancelEdit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	user, ok := CurrentUserFromContext(r.Context())
	plan, pe := publicationRequestID(r.PathValue("plan"))
	session, se := publicationRequestID(r.PathValue("id"))
	store := h.editStore()
	if !ok || pe != nil || se != nil || store == nil {
		h.System.NotFound(w, r)
		return
	}
	ctx := r.Context()
	var cancel context.CancelFunc
	if r.Method == http.MethodGet {
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
	} else {
		deadline, valid := httpx.PublicationDeadline(ctx, h.HTTPWriteTimeout)
		if !valid {
			h.System.RequestRejected(w, r)
			return
		}
		ctx, cancel = context.WithDeadline(ctx, deadline)
	}
	defer cancel()
	r = r.WithContext(ctx)
	request := FocusedWorkoutEditRequest{user.ID, plan, session}
	source, err := store.LoadFocusedEditSession(ctx, request)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			h.System.NotFound(w, r)
		} else {
			h.System.InternalError(w, r)
		}
		return
	}
	schedule, _ := trainingScheduleFromRecord(source.Raw.Session.SchedulePrecision, source.Raw.Session.ScheduledOn, source.Raw.Session.StartsAt, source.Raw.Session.EndsAt, source.Raw.Session.DayBand)
	base := focusedWorkoutCancelURL(plan.String(), session.String())
	meta := h.page(r, user, source.Context, newFocusedWorkoutDraft(source.Context.Day)).Meta
	meta.Title = "Cancelar treino | MyCFCoimbra"
	meta.PageLabel = "Cancelar treino"
	page := pages.TrainingSessionCancelPage{Meta: meta, CSRFField: templ.Raw(string(csrf.TemplateField(r))), SessionID: session.String(), Title: source.Raw.Session.Title, When: trainingScheduleView(schedule, lisbonLocation()).Label, ExpectedUpdatedAt: source.Raw.Session.UpdatedAt.Time.Format(time.RFC3339Nano), ActionURL: base, ReloadURL: base, ReturnURL: structuredPlannerURL(source.Context.GroupID.String(), plan.String(), session.String(), source.Context.Day), CancellationErrors: validation.FieldErrors{}}
	render := func(status int) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_ = pages.TrainingSessionCancel(page).Render(r.Context(), w)
	}
	if r.Method == http.MethodGet {
		if !focusedEditEligible(source.Raw.Session, source.DecisionAt) {
			page.WriteUnsupported = true
			page.UnsupportedMessage = "Este treino está disponível para consulta. Não pode ser cancelado neste momento."
		}
		render(200)
		return
	}
	if err = r.ParseForm(); err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	page.CancellationReason = strings.TrimSpace(r.PostForm.Get("cancellation_reason"))
	page.ExpectedUpdatedAt = r.PostForm.Get("expected_updated_at")
	for key, values := range r.PostForm {
		if (key != "expected_updated_at" && key != "cancellation_reason" && key != "confirm_cancellation" && key != "gorilla.csrf.Token") || len(values) != 1 {
			h.System.RequestRejected(w, r)
			return
		}
	}
	if !validTrainingText(page.CancellationReason, 2, 500) {
		page.CancellationErrors.Add("cancellation_reason", "O motivo deve ter entre 2 e 500 caracteres.")
	}
	if r.PostForm.Get("confirm_cancellation") != "yes" {
		page.CancellationErrors.Add("confirm_cancellation", "Confirme que pretende cancelar a sessão.")
	}
	expected, parseErr := time.Parse(time.RFC3339Nano, page.ExpectedUpdatedAt)
	if parseErr != nil {
		page.Conflict = "Este formulário deixou de ser válido. Recarregue o treino atual."
		render(422)
		return
	}
	if len(page.CancellationErrors) > 0 {
		render(422)
		return
	}
	_, err = store.CancelFocusedEdit(ctx, request, expected, page.CancellationReason)
	if err != nil {
		if errors.Is(err, db.ErrCommitOutcomeUnknown) {
			page.WriteUnsupported = true
			page.UnsupportedMessage = "Não foi possível confirmar o resultado. Consulte o treino atual antes de tentar outro cancelamento."
			render(409)
			return
		}
		if _, accessErr := store.LoadFocusedEditSession(ctx, request); errors.Is(accessErr, pgx.ErrNoRows) {
			h.System.NotFound(w, r)
			return
		} else if accessErr != nil {
			h.System.InternalError(w, r)
			return
		}
		page.Conflict = "O treino mudou, já começou ou já foi cancelado. O motivo introduzido foi mantido. Recarregue o treino atual."
		render(409)
		return
	}
	httpx.Redirect(w, r, page.ReturnURL, http.StatusSeeOther)
}
