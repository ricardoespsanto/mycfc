package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const trainingQueryTimeout = 5 * time.Second
const managedTrainingPlansPageSize = 6

type Training struct {
	Store    dbgen.Querier
	System   System
	PageMeta components.PageMeta
	Location *time.Location
	Sessions *scs.SessionManager
}

func (h Training) Index(w http.ResponseWriter, r *http.Request) {
	h.renderIndex(w, r, http.StatusOK, pages.TrainingPage{})
}

func (h Training) renderIndex(w http.ResponseWriter, r *http.Request, status int, page pages.TrainingPage) {
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	page.CanManage = user.IsAdmin || user.CanManageEvents
	sessions, err := h.Store.ListTrainingSessionsForAthlete(ctx, dbgen.ListTrainingSessionsForAthleteParams{UserID: user.ID, RowLimit: 100})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	for _, session := range sessions {
		modality := ""
		if session.ModalityName != nil {
			modality = *session.ModalityName
		}
		outcome, _ := session.OutcomeStatus.(string)
		page.Sessions = append(page.Sessions, pages.TrainingSession{ID: session.ID.String(), Plan: session.PlanTitle, Title: session.Title, Detail: session.Description, When: session.StartsAt.Time.In(h.location()).Format("02/01/2006 15:04") + " - " + session.EndsAt.Time.In(h.location()).Format("15:04"), Modality: modality, Outcome: outcome})
	}
	documents, err := h.Store.ListCompetitionDocumentsForAthlete(ctx, dbgen.ListCompetitionDocumentsForAthleteParams{UserID: user.ID, RowLimit: 100})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	for _, document := range documents {
		context := ""
		if document.EventTitle != nil {
			context = *document.EventTitle
		}
		if document.ModalityName != nil {
			if context != "" {
				context += " · "
			}
			context += *document.ModalityName
		}
		page.Documents = append(page.Documents, pages.CompetitionDocument{Title: document.Title, URL: document.Url, Source: document.Source, ReviewedOn: document.ReviewedOn.Time.Format("02/01/2006"), Context: context})
	}
	if page.CanManage {
		h.authoring(ctx, user, &page, managedTrainingPlansPageNumber(r.URL.Query().Get("managed_page")))
	}
	page.Meta = h.meta(r, user)
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
		h.renderIndex(w, r, http.StatusBadRequest, pages.TrainingPage{Error: "Não foi possível ler o formulário.", OpenForm: "plan"})
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
		h.renderIndex(w, r, http.StatusUnprocessableEntity, pages.TrainingPage{OpenForm: "plan", PlanForm: form})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if _, err := h.Store.CreateTrainingPlan(ctx, dbgen.CreateTrainingPlanParams{Title: title, Description: description, ProgrammeID: programmeID, TeamID: teamID, CreatedByID: user.ID}); err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Plano criado.")
	httpx.Redirect(w, r, "/treinos", http.StatusSeeOther)
}

func (h Training) CreateSession(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderIndex(w, r, http.StatusBadRequest, pages.TrainingPage{Error: "Não foi possível ler o formulário.", OpenForm: "session"})
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
		h.renderIndex(w, r, http.StatusUnprocessableEntity, pages.TrainingPage{OpenForm: "session", SessionForm: form})
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
	httpx.Redirect(w, r, "/treinos", http.StatusSeeOther)
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
	if err != nil || replacementErr != nil || !validTrainingOutcome(status, replacementSessionID, replacementReason) {
		http.Error(w, "Resultado da sessão inválido.", http.StatusUnprocessableEntity)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	n, err := h.Store.SaveTrainingSessionOutcome(ctx, dbgen.SaveTrainingSessionOutcomeParams{SessionID: sessionID, UserID: user.ID, Status: dbgen.TrainingOutcomeStatus(status), ReplacementSessionID: replacementSessionID, ReplacementReason: optionalString(replacementReason)})
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

func (h Training) CreateDocument(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderIndex(w, r, http.StatusBadRequest, pages.TrainingPage{Error: "Não foi possível ler o formulário.", OpenForm: "document"})
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	title, rawURL, source := strings.TrimSpace(r.PostForm.Get("title")), strings.TrimSpace(r.PostForm.Get("url")), strings.TrimSpace(r.PostForm.Get("source"))
	form := pages.TrainingDocumentForm{Title: title, URL: rawURL, Source: source, ReviewedOn: r.PostForm.Get("reviewed_on"), EventID: r.PostForm.Get("event_id"), ModalityID: r.PostForm.Get("modality_id"), ProgrammeID: r.PostForm.Get("programme_id"), TeamID: r.PostForm.Get("team_id"), Errors: validation.FieldErrors{}}
	eventID, e1 := optionalUUID(r.PostForm.Get("event_id"))
	modalityID, e2 := optionalUUID(r.PostForm.Get("modality_id"))
	programmeID, teamID, e3 := trainingScope(r)
	reviewed, e4 := time.Parse("2006-01-02", r.PostForm.Get("reviewed_on"))
	if !validTrainingText(title, 2, 180) {
		form.Errors.Add("title", "O título deve ter entre 2 e 180 caracteres.")
	}
	if !validDocumentURL(rawURL) {
		form.Errors.Add("url", "Indique uma ligação HTTPS válida.")
	}
	if !validTrainingText(source, 2, 180) {
		form.Errors.Add("source", "A fonte deve ter entre 2 e 180 caracteres.")
	}
	if e4 != nil || reviewed.After(time.Now().UTC()) {
		form.Errors.Add("reviewed_on", "Indique uma data válida, não futura.")
	}
	if e1 != nil || e2 != nil || e3 != nil || !validCompetitionDocumentInput(title, rawURL, source, reviewed, eventID, modalityID, programmeID, teamID) {
		form.Errors.Add("scope", "Selecione um contexto válido para o documento.")
	}
	if !form.Errors.Empty() {
		h.renderIndex(w, r, http.StatusUnprocessableEntity, pages.TrainingPage{OpenForm: "document", DocumentForm: form})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if !user.IsAdmin {
		if eventID != nil {
			allowed, err := h.Store.CanCoachManageEvent(ctx, dbgen.CanCoachManageEventParams{EventID: *eventID, UserID: user.ID})
			if err != nil {
				h.System.InternalError(w, r)
				return
			}
			if !allowed {
				h.System.Forbidden(w, r)
				return
			}
		}
		if (eventID == nil || programmeID != nil || teamID != nil) && !h.canUseScope(user, programmeID, teamID) {
			h.System.Forbidden(w, r)
			return
		}
	}
	if _, err := h.Store.CreateCompetitionDocument(ctx, dbgen.CreateCompetitionDocumentParams{Title: title, Url: rawURL, Source: source, ReviewedOn: pgtype.Date{Time: reviewed, Valid: true}, EventID: eventID, ModalityID: modalityID, ProgrammeID: programmeID, TeamID: teamID, AuthorID: user.ID}); err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Documento publicado.")
	httpx.Redirect(w, r, "/treinos", http.StatusSeeOther)
}

func (h Training) flash(r *http.Request, message string) {
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "training_flash", message)
	}
}

func (h Training) authoring(ctx context.Context, user CurrentUser, page *pages.TrainingPage, pageNumber int) {
	programmes, _ := h.Store.ListProgrammes(ctx)
	teams, _ := h.Store.ListTeamsForEventAuthoring(ctx)
	modalities, _ := h.Store.ListAnnouncementModalities(ctx)
	events, _ := h.Store.ListAnnouncementEvents(ctx)
	plans, _ := h.Store.ListTrainingPlansForCoach(ctx, dbgen.ListTrainingPlansForCoachParams{UserID: user.ID, RowLimit: 100})
	managedPlans, _ := h.Store.ListTrainingPlansForAuthoring(ctx, dbgen.ListTrainingPlansForAuthoringParams{UserID: user.ID, IsAdmin: user.IsAdmin, PlanLimit: managedTrainingPlansPageSize + 1, PlanOffset: int32((pageNumber - 1) * managedTrainingPlansPageSize)})
	page.ManagedPlans = managedTrainingPlans(managedPlans, h.location())
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
	for _, x := range events {
		if user.IsAdmin {
			page.Events = append(page.Events, pages.TrainingChoice{ID: x.ID.String(), Name: x.Title})
			continue
		}
		allowed, _ := h.Store.CanCoachManageEvent(ctx, dbgen.CanCoachManageEventParams{EventID: x.ID, UserID: user.ID})
		if allowed {
			page.Events = append(page.Events, pages.TrainingChoice{ID: x.ID.String(), Name: x.Title})
		}
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
	return "/treinos?managed_page=" + strconv.Itoa(page)
}

func managedTrainingPlans(rows []dbgen.ListTrainingPlansForAuthoringRow, location *time.Location) []pages.ManagedTrainingPlan {
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
			Title:       *row.SessionTitle,
			Description: *row.SessionDescription,
			When:        row.StartsAt.Time.In(location).Format("02/01/2006 15:04") + " - " + row.EndsAt.Time.In(location).Format("15:04"),
			Modality:    modality,
		})
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
func (h Training) meta(r *http.Request, user CurrentUser) components.PageMeta {
	meta := h.PageMeta
	meta.Title = "Treinos e competição | MyCFC"
	meta.CurrentPath = "/treinos"
	meta.CurrentUserName = user.Name
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return meta
}
