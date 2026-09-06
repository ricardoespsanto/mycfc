package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/featureflags"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const trainingQueryTimeout = 5 * time.Second
const managedTrainingPlansPageSize = 6

var errInvalidKilometres = errors.New("invalid kilometres")

type Training struct {
	Store    dbgen.Querier
	System   System
	PageMeta components.PageMeta
	Location *time.Location
	Sessions *scs.SessionManager
	Now      func() time.Time
}

func (h Training) Index(w http.ResponseWriter, r *http.Request) {
	h.renderIndex(w, r, http.StatusOK, pages.TrainingPage{})
}

// CreatePlanPage renders the no-JavaScript fallback for the plan authoring task.
func (h Training) CreatePlanPage(w http.ResponseWriter, r *http.Request) {
	h.renderPlanCreate(w, r, http.StatusOK, pages.TrainingPage{})
}

// CreateSessionPage renders the no-JavaScript fallback for the session authoring task.
func (h Training) CreateSessionPage(w http.ResponseWriter, r *http.Request) {
	h.renderSessionCreate(w, r, http.StatusOK, pages.TrainingPage{})
}

func (h Training) renderPlanCreate(w http.ResponseWriter, r *http.Request, status int, page pages.TrainingPage) {
	h.prepareTrainingCreatePage(r, &page, "Criar plano")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.TrainingPlanCreateView(page).Render(r.Context(), w)
}

func (h Training) renderSessionCreate(w http.ResponseWriter, r *http.Request, status int, page pages.TrainingPage) {
	h.prepareTrainingCreatePage(r, &page, "Criar sessão")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.TrainingSessionCreateView(page).Render(r.Context(), w)
}

func (h Training) prepareTrainingCreatePage(r *http.Request, page *pages.TrainingPage, title string) {
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	page.Management = true
	page.CanManage = user.IsAdmin || user.CanManageEvents
	page.StructuredAvailable = featureflags.Available(user.FeatureModes, featureflags.StructuredTrainingPlanning, user.IsAdmin)
	h.authoring(ctx, user, page, 1)
	page.Meta = h.meta(r, user, true)
	page.Meta.Title = title + " | MyCFCoimbra"
	page.Meta.PageLabel = title
	page.Meta.CurrentPath = r.URL.Path
	page.Meta.Breadcrumbs = []components.NavigationItem{{Label: "Planear treinos", Path: "/admin/treinos"}}
	page.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
}

func (h Training) renderIndex(w http.ResponseWriter, r *http.Request, status int, page pages.TrainingPage) {
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	page.Management = strings.HasPrefix(r.URL.Path, "/admin/")
	page.CanManage = user.IsAdmin || user.CanManageEvents
	page.StructuredAvailable = featureflags.Available(user.FeatureModes, featureflags.StructuredTrainingPlanning, user.IsAdmin)
	if !page.Management {
		sessions, err := h.Store.ListTrainingSessionsForAthlete(ctx, dbgen.ListTrainingSessionsForAthleteParams{UserID: user.ID, RowLimit: 100})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		calendarEntries := make([]calendarEntry, 0, len(sessions))
		for _, session := range sessions {
			modality := ""
			if session.ModalityName != nil {
				modality = *session.ModalityName
			}
			outcome, _ := session.OutcomeStatus.(string)
			distance, distanceInput := "", ""
			if session.DistanceMetres != nil {
				distance = formatKilometres(int64(*session.DistanceMetres))
				distanceInput = kilometreInput(*session.DistanceMetres)
			}
			cancelled := session.Status == "CANCELLED"
			actualDuration, durationInput := "", ""
			if session.ActualDurationMinutes != nil {
				actualDuration = fmt.Sprintf("%d min", *session.ActualDurationMinutes)
				durationInput = strconv.Itoa(int(*session.ActualDurationMinutes))
			}
			feedbackUpdatedAt := ""
			if session.OutcomeUpdatedAt.Valid {
				feedbackUpdatedAt = session.OutcomeUpdatedAt.Time.In(h.location()).Format("02/01/2006 15:04")
			}
			page.Sessions = append(page.Sessions, pages.TrainingSession{
				ID: session.ID.String(), Plan: session.PlanTitle, Title: session.Title, Detail: session.Description,
				When:     session.StartsAt.Time.In(h.location()).Format("02/01/2006 15:04") + " - " + session.EndsAt.Time.In(h.location()).Format("15:04"),
				Modality: modality, Outcome: outcome, Distance: distance, DistanceKM: distanceInput,
				ActualDuration: actualDuration, DurationMinutes: durationInput,
				PerceivedEffort: optionalInt16Input(session.PerceivedExertion), RecoveryFeeling: optionalInt16Input(session.RecoveryFeeling),
				PerceptionNote: stringValue(session.PerceptionNote), FeedbackUpdatedAt: feedbackUpdatedAt, OutcomeVersion: int(session.OutcomeVersion),
				Cancelled: cancelled, CancellationReason: stringValue(session.CancellationReason), PrescriptionAvailable: session.PrescriptionAvailable,
			})
			calendarTitle, calendarKind := session.Title, "Treino"
			if cancelled {
				calendarTitle, calendarKind = "Cancelada: "+session.Title, "Cancelada"
			}
			calendarURL := "/treinos"
			if session.PrescriptionAvailable {
				calendarURL = "/treinos/prescricoes/sessoes/" + session.ID.String()
			}
			calendarEntries = append(calendarEntries, calendarEntry{Title: calendarTitle, URL: calendarURL, Kind: calendarKind, StartsAt: session.StartsAt.Time})
		}
		page.Calendar = basicCalendarMonth(calendarEntries, h.now(), h.location())
	}
	if page.Management {
		h.authoring(ctx, user, &page, managedTrainingPlansPageNumber(r.URL.Query().Get("managed_page")))
	}
	page.Meta = h.meta(r, user, page.Management)
	page.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	if h.Sessions != nil && page.Success == "" {
		page.Success = h.Sessions.PopString(r.Context(), "training_flash")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.Training(page).Render(r.Context(), w)
}

func (h Training) CreatePlan(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderPlanCreate(w, r, http.StatusBadRequest, pages.TrainingPage{Error: "Não foi possível ler o formulário."})
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	title, description := strings.TrimSpace(r.PostForm.Get("title")), strings.TrimSpace(r.PostForm.Get("description"))
	form := pages.TrainingPlanForm{Title: title, Description: description, ProgrammeID: r.PostForm.Get("programme_id"), TeamID: r.PostForm.Get("team_id"), Errors: validation.FieldErrors{}}
	programmeID, teamID, err := trainingScope(r)
	if !validTrainingText(title, 2, 180) {
		form.Errors.Add("title", "O título deve ter entre 2 e 180 caracteres.")
	}
	if utf8.RuneCountInString(description) > 4000 {
		form.Errors.Add("description", "A descrição não pode exceder 4000 caracteres.")
	}
	if err != nil || !h.canUseScope(user, programmeID, teamID) {
		form.Errors.Add("scope", "Selecione um âmbito que possa gerir.")
	}
	if !form.Errors.Empty() {
		h.renderPlanCreate(w, r, http.StatusUnprocessableEntity, pages.TrainingPage{PlanForm: form})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if _, err := h.Store.CreateTrainingPlan(ctx, dbgen.CreateTrainingPlanParams{Title: title, Description: description, ProgrammeID: programmeID, TeamID: teamID, CreatedByID: user.ID}); err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Plano criado.")
	httpx.Redirect(w, r, "/admin/treinos", http.StatusSeeOther)
}

func (h Training) CreateSession(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderSessionCreate(w, r, http.StatusBadRequest, pages.TrainingPage{Error: "Não foi possível ler o formulário."})
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	form := pages.TrainingSessionForm{PlanID: r.PostForm.Get("plan_id"), Title: strings.TrimSpace(r.PostForm.Get("title")), Description: strings.TrimSpace(r.PostForm.Get("description")), StartsAt: r.PostForm.Get("starts_at"), EndsAt: r.PostForm.Get("ends_at"), ModalityID: r.PostForm.Get("modality_id"), Errors: validation.FieldErrors{}}
	planID, planErr := uuid.Parse(form.PlanID)
	starts, startErr := time.ParseInLocation("2006-01-02T15:04", r.PostForm.Get("starts_at"), h.location())
	ends, endErr := time.ParseInLocation("2006-01-02T15:04", r.PostForm.Get("ends_at"), h.location())
	title, description := form.Title, form.Description
	modalityID, err := optionalUUID(r.PostForm.Get("modality_id"))
	if planErr != nil {
		form.Errors.Add("plan_id", "Selecione um plano válido.")
	}
	if err != nil {
		form.Errors.Add("modality_id", "Selecione uma modalidade válida.")
	}
	if !validTrainingText(title, 2, 180) {
		form.Errors.Add("title", "O título deve ter entre 2 e 180 caracteres.")
	}
	if utf8.RuneCountInString(description) > 4000 {
		form.Errors.Add("description", "A descrição não pode exceder 4000 caracteres.")
	}
	if startErr != nil {
		form.Errors.Add("starts_at", "Introduza uma data e hora de início válidas.")
	}
	if endErr != nil {
		form.Errors.Add("ends_at", "Introduza uma data e hora de fim válidas.")
	} else if startErr == nil && !ends.After(starts) {
		form.Errors.Add("ends_at", "O fim tem de ser posterior ao início.")
	}
	if !form.Errors.Empty() {
		h.renderSessionCreate(w, r, http.StatusUnprocessableEntity, pages.TrainingPage{SessionForm: form})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if !user.IsAdmin {
		allowed, err := h.Store.CanCoachManageTrainingPlan(ctx, dbgen.CanCoachManageTrainingPlanParams{PlanID: planID, UserID: user.ID})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		if !allowed {
			h.System.Forbidden(w, r)
			return
		}
	}
	if _, err := h.Store.CreateTrainingSession(ctx, dbgen.CreateTrainingSessionParams{PlanID: planID, Title: title, Description: description, StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: ends, Valid: true}, ModalityID: modalityID, CreatedByID: user.ID}); err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Sessão criada.")
	httpx.Redirect(w, r, "/admin/treinos", http.StatusSeeOther)
}

func (h Training) EditSession(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := h.trainingSessionID(w, r)
	if !ok {
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	session, err := h.Store.GetTrainingSessionForEdit(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageTrainingPlan(ctx, user, session.PlanID, w, r) {
		return
	}
	form := h.trainingSessionFormFromRecord(session)
	if session.Status != "ACTIVE" || !session.StartsAt.Time.After(h.now()) {
		form.Errors.Add("state", "Apenas sessões ativas que ainda não começaram podem ser alteradas.")
		h.renderSessionEdit(w, r, http.StatusConflict, sessionID, form, "A sessão já não pode ser alterada.", "", validation.FieldErrors{})
		return
	}
	h.renderSessionEdit(w, r, http.StatusOK, sessionID, form, "", "", validation.FieldErrors{})
}

func (h Training) UpdateSession(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := h.trainingSessionID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	current, err := h.Store.GetTrainingSessionForEdit(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageTrainingPlan(ctx, user, current.PlanID, w, r) {
		return
	}
	form, starts, ends, modalityID, expected := h.validateTrainingSessionEdit(r)
	form.ID, form.Editing, form.PlanLocked = sessionID.String(), true, current.HasOutcomes
	planID, planErr := uuid.Parse(form.PlanID)
	if planErr == nil {
		exists, getErr := h.Store.TrainingPlanExists(ctx, planID)
		if getErr != nil {
			h.System.InternalError(w, r)
			return
		}
		if !exists {
			form.Errors.Add("plan_id", "Selecione um plano válido.")
		}
		if exists && !user.IsAdmin {
			allowed, getErr := h.Store.CanCoachManageTrainingPlan(ctx, dbgen.CanCoachManageTrainingPlanParams{PlanID: planID, UserID: user.ID})
			if getErr != nil {
				h.System.InternalError(w, r)
				return
			}
			if !allowed {
				h.System.Forbidden(w, r)
				return
			}
		}
		if current.HasOutcomes && planID != current.PlanID {
			form.Errors.Add("plan_id", "O plano não pode ser alterado depois do primeiro resultado.")
		}
	}
	if !form.Errors.Empty() {
		h.renderSessionEdit(w, r, http.StatusUnprocessableEntity, sessionID, form, "", "", validation.FieldErrors{})
		return
	}
	_, err = h.Store.UpdateTrainingSession(ctx, dbgen.UpdateTrainingSessionParams{PlanID: planID, Title: form.Title, Description: form.Description, StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: ends, Valid: true}, ModalityID: modalityID, ID: sessionID, AsOf: pgtype.Timestamptz{Time: h.now(), Valid: true}, ExpectedUpdatedAt: pgtype.Timestamptz{Time: expected, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		latest, getErr := h.Store.GetTrainingSessionForEdit(ctx, sessionID)
		if getErr != nil {
			h.System.InternalError(w, r)
			return
		}
		h.renderSessionEdit(w, r, http.StatusConflict, sessionID, h.trainingSessionFormFromRecord(latest), "A sessão foi alterada entretanto, já começou ou já foi cancelada.", "", validation.FieldErrors{})
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Sessão atualizada.")
	httpx.Redirect(w, r, "/admin/treinos", http.StatusSeeOther)
}

func (h Training) CancelSession(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := h.trainingSessionID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	current, err := h.Store.GetTrainingSessionForEdit(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageTrainingPlan(ctx, user, current.PlanID, w, r) {
		return
	}
	reason := strings.TrimSpace(r.PostForm.Get("cancellation_reason"))
	errs := validation.FieldErrors{}
	if !validTrainingText(reason, 2, 500) {
		errs.Add("cancellation_reason", "O motivo deve ter entre 2 e 500 caracteres.")
	}
	if r.PostForm.Get("confirm_cancellation") != "yes" {
		errs.Add("confirm_cancellation", "Confirme que pretende cancelar a sessão.")
	}
	expected, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(r.PostForm.Get("expected_updated_at")))
	if parseErr != nil {
		errs.Add("state", "O formulário deixou de ser válido. Atualize a página.")
	}
	if !errs.Empty() {
		h.renderSessionCancel(w, r, http.StatusUnprocessableEntity, sessionID, current, "", reason, errs)
		return
	}
	now := h.now()
	_, err = h.Store.CancelTrainingSession(ctx, dbgen.CancelTrainingSessionParams{CancelledAt: pgtype.Timestamptz{Time: now, Valid: true}, CancelledByID: &user.ID, CancellationReason: &reason, ID: sessionID, ExpectedUpdatedAt: pgtype.Timestamptz{Time: expected, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		latest, getErr := h.Store.GetTrainingSessionForEdit(ctx, sessionID)
		if getErr != nil {
			h.System.InternalError(w, r)
			return
		}
		h.renderSessionCancel(w, r, http.StatusConflict, sessionID, latest, "A sessão foi alterada entretanto, já começou ou já foi cancelada.", reason, validation.FieldErrors{})
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Sessão cancelada.")
	httpx.Redirect(w, r, "/admin/treinos", http.StatusSeeOther)
}

func (h Training) CancelSessionPage(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := h.trainingSessionID(w, r)
	if !ok {
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	session, err := h.Store.GetTrainingSessionForEdit(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageTrainingPlan(ctx, user, session.PlanID, w, r) {
		return
	}
	conflict := ""
	status := http.StatusOK
	if session.Status != "ACTIVE" || !session.StartsAt.Time.After(h.now()) {
		status = http.StatusConflict
		conflict = "A sessão já não pode ser cancelada. As sessões canceladas ou já iniciadas continuam disponíveis para consulta."
	}
	h.renderSessionCancel(w, r, status, sessionID, session, conflict, "", validation.FieldErrors{})
}

func (h Training) ReportOutcome(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	sessionID, err := uuid.Parse(r.PostForm.Get("session_id"))
	status := r.PostForm.Get("status")
	replacementSessionID, replacementErr := optionalUUID(r.PostForm.Get("replacement_session_id"))
	replacementReason := strings.TrimSpace(r.PostForm.Get("replacement_reason"))
	distanceMetres, distanceErr := parseKilometres(r.PostForm.Get("distance_km"))
	actualDuration, durationErr := optionalBoundedInt32(r.PostForm.Get("actual_duration_minutes"), 1, 1440)
	perceivedExertion, exertionErr := optionalBoundedInt16(r.PostForm.Get("perceived_exertion"), 0, 10)
	recoveryFeeling, recoveryErr := optionalBoundedInt16(r.PostForm.Get("recovery_feeling"), 1, 5)
	perceptionNote := strings.TrimSpace(r.PostForm.Get("perception_note"))
	hasCompletedFeedback := distanceMetres != nil || actualDuration != nil || perceivedExertion != nil || recoveryFeeling != nil || perceptionNote != ""
	if err != nil || replacementErr != nil || distanceErr != nil || durationErr != nil || exertionErr != nil || recoveryErr != nil || utf8.RuneCountInString(perceptionNote) > 500 || !validTrainingOutcome(status, replacementSessionID, replacementReason) || (status != "COMPLETED" && hasCompletedFeedback) {
		http.Error(w, "Resultado da sessão inválido.", http.StatusUnprocessableEntity)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	n, err := h.Store.SaveTrainingSessionOutcome(ctx, dbgen.SaveTrainingSessionOutcomeParams{
		SessionID: sessionID, UserID: user.ID, Status: dbgen.TrainingOutcomeStatus(status),
		ExpectedVersion:      0,
		ReplacementSessionID: replacementSessionID, ReplacementReason: optionalString(replacementReason),
		DistanceMetres: distanceMetres, ActualDurationMinutes: actualDuration, PerceivedExertion: perceivedExertion,
		RecoveryFeeling: recoveryFeeling, PerceptionNote: optionalString(perceptionNote),
	})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if n != 1 {
		http.Error(w, "A sessão ou substituição não está disponível no seu âmbito ativo.", http.StatusConflict)
		return
	}
	h.flash(r, "Resultado registado.")
	httpx.Redirect(w, r, "/treinos", http.StatusSeeOther)
}

func (h Training) UpdateFeedback(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	sessionID, sessionErr := uuid.Parse(r.PostForm.Get("session_id"))
	distanceMetres, distanceErr := parseKilometres(r.PostForm.Get("distance_km"))
	actualDuration, durationErr := optionalBoundedInt32(r.PostForm.Get("actual_duration_minutes"), 1, 1440)
	perceivedExertion, exertionErr := optionalBoundedInt16(r.PostForm.Get("perceived_exertion"), 0, 10)
	recoveryFeeling, recoveryErr := optionalBoundedInt16(r.PostForm.Get("recovery_feeling"), 1, 5)
	perceptionNote := strings.TrimSpace(r.PostForm.Get("perception_note"))
	expectedVersion, versionErr := strconv.ParseInt(r.PostForm.Get("expected_version"), 10, 32)
	if sessionErr != nil || distanceErr != nil || durationErr != nil || exertionErr != nil || recoveryErr != nil || versionErr != nil || expectedVersion < 1 || utf8.RuneCountInString(perceptionNote) > 500 {
		http.Error(w, "Dados reais ou perceção inválidos.", http.StatusUnprocessableEntity)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	n, err := h.Store.UpdateOwnCompletedSessionFeedback(ctx, dbgen.UpdateOwnCompletedSessionFeedbackParams{
		SessionID: sessionID, UserID: user.ID, ExpectedVersion: int32(expectedVersion), DistanceMetres: distanceMetres,
		ActualDurationMinutes: actualDuration, PerceivedExertion: perceivedExertion, RecoveryFeeling: recoveryFeeling,
		PerceptionNote: optionalString(perceptionNote),
	})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if n != 1 {
		http.Error(w, "A sessão foi alterada entretanto ou já não está disponível. Atualize a página antes de tentar novamente.", http.StatusConflict)
		return
	}
	h.flash(r, "Dados reais e perceção atualizados.")
	httpx.Redirect(w, r, "/treinos", http.StatusSeeOther)
}

func (h Training) flash(r *http.Request, message string) {
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "training_flash", message)
	}
}

func (h Training) trainingSessionID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return uuid.Nil, false
	}
	return id, true
}

func (h Training) canManageTrainingPlan(ctx context.Context, user CurrentUser, planID uuid.UUID, w http.ResponseWriter, r *http.Request) bool {
	if user.IsAdmin {
		return true
	}
	allowed, err := h.Store.CanCoachManageTrainingPlan(ctx, dbgen.CanCoachManageTrainingPlanParams{PlanID: planID, UserID: user.ID})
	if err != nil {
		h.System.InternalError(w, r)
		return false
	}
	if !allowed {
		h.System.Forbidden(w, r)
		return false
	}
	return true
}

func (h Training) validateTrainingSessionEdit(r *http.Request) (pages.TrainingSessionForm, time.Time, time.Time, *uuid.UUID, time.Time) {
	form := pages.TrainingSessionForm{PlanID: r.PostForm.Get("plan_id"), Title: strings.TrimSpace(r.PostForm.Get("title")), Description: strings.TrimSpace(r.PostForm.Get("description")), StartsAt: r.PostForm.Get("starts_at"), EndsAt: r.PostForm.Get("ends_at"), ModalityID: r.PostForm.Get("modality_id"), ExpectedUpdatedAt: strings.TrimSpace(r.PostForm.Get("expected_updated_at")), Editing: true, Errors: validation.FieldErrors{}}
	_, planErr := uuid.Parse(form.PlanID)
	starts, startErr := time.ParseInLocation("2006-01-02T15:04", form.StartsAt, h.location())
	ends, endErr := time.ParseInLocation("2006-01-02T15:04", form.EndsAt, h.location())
	modalityID, modalityErr := optionalUUID(form.ModalityID)
	expected, expectedErr := time.Parse(time.RFC3339Nano, form.ExpectedUpdatedAt)
	if planErr != nil {
		form.Errors.Add("plan_id", "Selecione um plano válido.")
	}
	if modalityErr != nil {
		form.Errors.Add("modality_id", "Selecione uma modalidade válida.")
	}
	if !validTrainingText(form.Title, 2, 180) {
		form.Errors.Add("title", "O título deve ter entre 2 e 180 caracteres.")
	}
	if utf8.RuneCountInString(form.Description) > 4000 {
		form.Errors.Add("description", "A descrição não pode exceder 4000 caracteres.")
	}
	if startErr != nil {
		form.Errors.Add("starts_at", "Introduza uma data e hora de início válidas.")
	}
	if endErr != nil {
		form.Errors.Add("ends_at", "Introduza uma data e hora de fim válidas.")
	} else if startErr == nil && !ends.After(starts) {
		form.Errors.Add("ends_at", "O fim tem de ser posterior ao início.")
	}
	if expectedErr != nil {
		form.Errors.Add("state", "O formulário deixou de ser válido. Atualize a página.")
	}
	return form, starts, ends, modalityID, expected
}

func (h Training) trainingSessionFormFromRecord(session dbgen.GetTrainingSessionForEditRow) pages.TrainingSessionForm {
	modalityID := ""
	if session.ModalityID != nil {
		modalityID = session.ModalityID.String()
	}
	return pages.TrainingSessionForm{ID: session.ID.String(), PlanID: session.PlanID.String(), Title: session.Title, Description: session.Description, StartsAt: session.StartsAt.Time.In(h.location()).Format("2006-01-02T15:04"), EndsAt: session.EndsAt.Time.In(h.location()).Format("2006-01-02T15:04"), ModalityID: modalityID, ExpectedUpdatedAt: session.UpdatedAt.Time.Format(time.RFC3339Nano), Editing: true, PlanLocked: session.HasOutcomes, Errors: validation.FieldErrors{}}
}

func (h Training) renderSessionEdit(w http.ResponseWriter, r *http.Request, status int, sessionID uuid.UUID, form pages.TrainingSessionForm, conflict, cancellationReason string, cancellationErrors validation.FieldErrors) {
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	plans, err := h.Store.ListTrainingPlansForCoach(ctx, dbgen.ListTrainingPlansForCoachParams{UserID: user.ID, RowLimit: 100})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if user.IsAdmin {
		adminPlans, getErr := h.Store.ListTrainingPlansForAdmin(ctx, 100)
		if getErr != nil {
			h.System.InternalError(w, r)
			return
		}
		plans = make([]dbgen.ListTrainingPlansForCoachRow, len(adminPlans))
		for i, plan := range adminPlans {
			plans[i] = dbgen.ListTrainingPlansForCoachRow{ID: plan.ID, Title: plan.Title}
		}
	}
	modalities, err := h.Store.ListAnnouncementModalities(ctx)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	page := pages.TrainingSessionEditPage{SessionID: sessionID.String(), Form: form, Conflict: conflict, CancellationReason: cancellationReason, CancellationErrors: cancellationErrors, CSRFField: templ.Raw(string(csrf.TemplateField(r)))}
	for _, plan := range plans {
		page.Plans = append(page.Plans, pages.TrainingChoice{ID: plan.ID.String(), Name: plan.Title})
	}
	for _, modality := range modalities {
		page.Modalities = append(page.Modalities, pages.TrainingChoice{ID: modality.ID.String(), Name: modality.NamePt})
	}
	page.Meta = h.meta(r, user, true)
	page.Meta.Title = "Editar sessão | MyCFCoimbra"
	page.Meta.CurrentPath = r.URL.Path
	page.Meta.PageLabel = "Editar sessão"
	page.Meta.Breadcrumbs = []components.NavigationItem{{Label: "Planear treinos", Path: "/admin/treinos"}}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.TrainingSessionEdit(page).Render(r.Context(), w)
}

func (h Training) renderSessionCancel(w http.ResponseWriter, r *http.Request, status int, sessionID uuid.UUID, session dbgen.GetTrainingSessionForEditRow, conflict, cancellationReason string, cancellationErrors validation.FieldErrors) {
	user, _ := CurrentUserFromContext(r.Context())
	page := pages.TrainingSessionCancelPage{
		Meta: h.meta(r, user, true), SessionID: sessionID.String(), Title: session.Title,
		When: session.StartsAt.Time.In(h.location()).Format("02/01/2006 15:04") + " - " + session.EndsAt.Time.In(h.location()).Format("15:04"), ExpectedUpdatedAt: session.UpdatedAt.Time.Format(time.RFC3339Nano),
		Conflict: conflict, CancellationReason: cancellationReason, CancellationErrors: cancellationErrors,
		CSRFField: templ.Raw(string(csrf.TemplateField(r))),
	}
	page.Meta.Title = "Cancelar sessão | MyCFCoimbra"
	page.Meta.CurrentPath = r.URL.Path
	page.Meta.PageLabel = "Cancelar sessão"
	page.Meta.Breadcrumbs = []components.NavigationItem{{Label: "Planear treinos", Path: "/admin/treinos"}, {Label: session.Title, Path: "/admin/treinos/sessoes/" + sessionID.String() + "/editar"}}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.TrainingSessionCancel(page).Render(r.Context(), w)
}

func (h Training) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h Training) authoring(ctx context.Context, user CurrentUser, page *pages.TrainingPage, pageNumber int) {
	programmes, _ := h.Store.ListProgrammes(ctx)
	teams, _ := h.Store.ListTeamsForEventAuthoring(ctx)
	modalities, _ := h.Store.ListAnnouncementModalities(ctx)
	plans, _ := h.Store.ListTrainingPlansForCoach(ctx, dbgen.ListTrainingPlansForCoachParams{UserID: user.ID, RowLimit: 100})
	managedPlans, _ := h.Store.ListTrainingPlansForAuthoring(ctx, dbgen.ListTrainingPlansForAuthoringParams{UserID: user.ID, IsAdmin: user.IsAdmin, PlanLimit: managedTrainingPlansPageSize + 1, PlanOffset: int32((pageNumber - 1) * managedTrainingPlansPageSize)})
	page.ManagedPlans = managedTrainingPlansAt(managedPlans, h.location(), h.now())
	if len(page.ManagedPlans) > managedTrainingPlansPageSize {
		page.ManagedPlans = page.ManagedPlans[:managedTrainingPlansPageSize]
		page.ManagedPlansNextURL = managedTrainingPlansPageURL(pageNumber + 1)
	}
	if pageNumber > 1 {
		page.ManagedPlansPreviousURL = managedTrainingPlansPageURL(pageNumber - 1)
	}
	if user.IsAdmin {
		adminPlans, _ := h.Store.ListTrainingPlansForAdmin(ctx, 100)
		plans = make([]dbgen.ListTrainingPlansForCoachRow, len(adminPlans))
		for i, plan := range adminPlans {
			plans[i] = dbgen.ListTrainingPlansForCoachRow{ID: plan.ID, Title: plan.Title}
		}
	}
	for _, x := range programmes {
		if user.IsAdmin || user.CoachProgrammeIDs[x.ID] {
			page.Programmes = append(page.Programmes, pages.TrainingChoice{ID: x.ID.String(), Name: x.NamePt})
		}
	}
	for _, x := range teams {
		if user.IsAdmin || user.CoachTeamIDs[x.ID] || user.CoachProgrammeIDs[x.ProgrammeID] {
			page.Teams = append(page.Teams, pages.TrainingChoice{ID: x.ID.String(), Name: x.Name})
		}
	}
	for _, x := range modalities {
		page.Modalities = append(page.Modalities, pages.TrainingChoice{ID: x.ID.String(), Name: x.NamePt})
	}
	for _, x := range plans {
		page.Plans = append(page.Plans, pages.TrainingChoice{ID: x.ID.String(), Name: x.Title})
	}
}

func managedTrainingPlansPageNumber(value string) int {
	page, err := strconv.Atoi(value)
	if err != nil || page < 1 || page > 10000 {
		return 1
	}
	return page
}

func managedTrainingPlansPageURL(page int) string {
	return "/admin/treinos?managed_page=" + strconv.Itoa(page)
}

func managedTrainingPlans(rows []dbgen.ListTrainingPlansForAuthoringRow, location *time.Location) []pages.ManagedTrainingPlan {
	return managedTrainingPlansAt(rows, location, time.Now())
}

func managedTrainingPlansAt(rows []dbgen.ListTrainingPlansForAuthoringRow, location *time.Location, now time.Time) []pages.ManagedTrainingPlan {
	plans := make([]pages.ManagedTrainingPlan, 0, len(rows))
	for _, row := range rows {
		if len(plans) == 0 || plans[len(plans)-1].ID != row.PlanID.String() {
			plans = append(plans, pages.ManagedTrainingPlan{ID: row.PlanID.String(), Title: row.PlanTitle, Description: row.PlanDescription})
		}
		if row.SessionID == nil {
			continue
		}
		modality := ""
		if row.ModalityName != nil {
			modality = *row.ModalityName
		}
		plans[len(plans)-1].Sessions = append(plans[len(plans)-1].Sessions, pages.ManagedTrainingSession{
			ID:                 row.SessionID.String(),
			Title:              *row.SessionTitle,
			Description:        *row.SessionDescription,
			When:               row.StartsAt.Time.In(location).Format("02/01/2006 15:04") + " - " + row.EndsAt.Time.In(location).Format("15:04"),
			Modality:           modality,
			Cancelled:          row.Status != nil && *row.Status == "CANCELLED",
			CancellationReason: stringValue(row.CancellationReason),
			Editable:           row.Status != nil && *row.Status == "ACTIVE" && row.StartsAt.Time.After(now),
		})
		if row.CancelledAt.Valid {
			plans[len(plans)-1].Sessions[len(plans[len(plans)-1].Sessions)-1].CancelledAt = row.CancelledAt.Time.In(location).Format("02/01/2006 15:04")
		}
		plans[len(plans)-1].Sessions[len(plans[len(plans)-1].Sessions)-1].CancelledBy = stringValue(row.CancelledByName)
	}
	return plans
}

func (h Training) canUseScope(user CurrentUser, programmeID, teamID *uuid.UUID) bool {
	if user.IsAdmin {
		return programmeID != nil || teamID != nil
	}
	if programmeID == nil && teamID == nil {
		return false
	}
	if programmeID != nil && !user.CoachProgrammeIDs[*programmeID] {
		return false
	}
	return teamID == nil || user.CoachTeamIDs[*teamID]
}
func trainingScope(r *http.Request) (*uuid.UUID, *uuid.UUID, error) {
	p, err := optionalUUID(r.PostForm.Get("programme_id"))
	if err != nil {
		return nil, nil, err
	}
	t, err := optionalUUID(r.PostForm.Get("team_id"))
	return p, t, err
}
func optionalUUID(raw string) (*uuid.UUID, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, err
	}
	return &id, nil
}
func validTrainingText(value string, min, max int) bool {
	n := utf8.RuneCountInString(value)
	return n >= min && n <= max
}
func validTrainingOutcome(status string, replacementSessionID *uuid.UUID, replacementReason string) bool {
	if status == "REPLACED" {
		return replacementSessionID != nil && validTrainingText(replacementReason, 2, 300)
	}
	return (status == "COMPLETED" || status == "MISSED") && replacementSessionID == nil && replacementReason == ""
}

func parseKilometres(value string) (*int32, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if strings.Contains(value, ",") {
		if strings.Contains(value, ".") {
			return nil, errInvalidKilometres
		}
		value = strings.Replace(value, ",", ".", 1)
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts) == 2 && (parts[1] == "" || len(parts[1]) > 2) {
		return nil, errInvalidKilometres
	}
	for _, part := range parts {
		for _, r := range part {
			if r < '0' || r > '9' {
				return nil, errInvalidKilometres
			}
		}
	}
	whole, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, errInvalidKilometres
	}
	fraction := 0
	if len(parts) == 2 {
		fraction, err = strconv.Atoi(parts[1])
		if err != nil {
			return nil, errInvalidKilometres
		}
		if len(parts[1]) == 1 {
			fraction *= 10
		}
	}
	metres := whole*1000 + fraction*10
	if metres < 1 || metres > 200000 {
		return nil, errInvalidKilometres
	}
	result := int32(metres)
	return &result, nil
}

func optionalBoundedInt32(value string, minimum, maximum int32) (*int32, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed < int64(minimum) || parsed > int64(maximum) {
		return nil, errors.New("value outside allowed range")
	}
	result := int32(parsed)
	return &result, nil
}

func optionalBoundedInt16(value string, minimum, maximum int16) (*int16, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 16)
	if err != nil || parsed < int64(minimum) || parsed > int64(maximum) {
		return nil, errors.New("value outside allowed range")
	}
	result := int16(parsed)
	return &result, nil
}

func optionalInt16Input(value *int16) string {
	if value == nil {
		return ""
	}
	return strconv.Itoa(int(*value))
}

func trainingFeelingText(value int16) string {
	switch value {
	case 1:
		return "muito mal"
	case 2:
		return "mal"
	case 3:
		return "razoavelmente"
	case 4:
		return "bem"
	case 5:
		return "muito bem"
	default:
		return ""
	}
}

func kilometreInput(metres int32) string {
	whole, remainder := metres/1000, metres%1000
	if remainder == 0 {
		return strconv.FormatInt(int64(whole), 10)
	}
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(float64(metres)/1000, 'f', 2, 64), "0"), ".")
}

func formatKilometres(metres int64) string {
	whole, remainder := metres/1000, metres%1000
	value := strconv.FormatInt(whole, 10)
	if remainder != 0 {
		fraction := fmt.Sprintf("%03d", remainder)
		value += "," + strings.TrimRight(fraction, "0")
	}
	return value + " km"
}
func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func validCompetitionDocumentInput(title, rawURL, source string, reviewed time.Time, eventID, modalityID, programmeID, teamID *uuid.UUID) bool {
	return !reviewed.After(time.Now().UTC()) && validTrainingText(title, 2, 180) && validTrainingText(source, 2, 180) && validDocumentURL(rawURL) && (eventID != nil || modalityID != nil) && (eventID != nil || programmeID != nil || teamID != nil) && (modalityID == nil || programmeID != nil || teamID != nil)
}
func (h Training) location() *time.Location {
	if h.Location != nil {
		return h.Location
	}
	return time.UTC
}
func (h Training) meta(r *http.Request, user CurrentUser, management bool) components.PageMeta {
	meta := h.PageMeta
	meta.Title = "Treinos e competição | MyCFCoimbra"
	meta.CurrentPath = "/treinos"
	meta.AreaLabel = "Atividade"
	meta.PageLabel = "Treinos"
	if management {
		meta.Title = "Gestão de treinos | MyCFCoimbra"
		meta.CurrentPath = "/admin/treinos"
		meta.AreaLabel = "Coordenação"
		meta.PageLabel = "Planear treinos"
	}
	meta.CurrentUserName = user.Name
	meta.CurrentUserID = user.ID.String()
	meta.EmailVerificationPending = !user.IsDependent && !user.EmailVerified
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return meta
}
