package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/a-h/templ"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/gorilla/csrf"
	"github.com/jackc/pgx/v5/pgtype"
)

const trainingQueryTimeout = 5 * time.Second

type Training struct {
	Store    dbgen.Querier
	System   System
	PageMeta components.PageMeta
	Location *time.Location
}

func (h Training) Index(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	page := pages.TrainingPage{CanManage: user.IsAdmin || user.CanManageEvents}
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
		page.Sessions = append(page.Sessions, pages.TrainingSession{ID: session.ID.String(), Plan: session.PlanTitle, Title: session.Title, Detail: session.Description, When: session.StartsAt.Time.In(h.location()).Format("02/01/2006 15:04") + " - " + session.EndsAt.Time.In(h.location()).Format("15:04"), Modality: modality, Completed: session.CompletedAt.Valid})
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
		h.authoring(ctx, user, &page)
	}
	page.Meta = h.meta(r, user)
	page.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pages.Training(page).Render(r.Context(), w)
}

func (h Training) CreatePlan(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	title, description := strings.TrimSpace(r.PostForm.Get("title")), strings.TrimSpace(r.PostForm.Get("description"))
	programmeID, teamID, err := trainingScope(r)
	if err != nil || !validTrainingText(title, 2, 180) || utf8.RuneCountInString(description) > 4000 || !h.canUseScope(user, programmeID, teamID) {
		http.Error(w, "Dados ou âmbito inválidos.", http.StatusUnprocessableEntity)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if _, err := h.Store.CreateTrainingPlan(ctx, dbgen.CreateTrainingPlanParams{Title: title, Description: description, ProgrammeID: programmeID, TeamID: teamID, CreatedByID: user.ID}); err != nil {
		h.System.InternalError(w, r)
		return
	}
	httpx.Redirect(w, r, "/treinos", http.StatusSeeOther)
}

func (h Training) CreateSession(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	planID, err := uuid.Parse(r.PostForm.Get("plan_id"))
	if err != nil {
		http.Error(w, "Plano inválido.", http.StatusUnprocessableEntity)
		return
	}
	starts, startErr := time.ParseInLocation("2006-01-02T15:04", r.PostForm.Get("starts_at"), h.location())
	ends, endErr := time.ParseInLocation("2006-01-02T15:04", r.PostForm.Get("ends_at"), h.location())
	title, description := strings.TrimSpace(r.PostForm.Get("title")), strings.TrimSpace(r.PostForm.Get("description"))
	modalityID, err := optionalUUID(r.PostForm.Get("modality_id"))
	if err != nil || startErr != nil || endErr != nil || !ends.After(starts) || !validTrainingText(title, 2, 180) || utf8.RuneCountInString(description) > 4000 {
		http.Error(w, "Dados da sessão inválidos.", http.StatusUnprocessableEntity)
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
	httpx.Redirect(w, r, "/treinos", http.StatusSeeOther)
}

func (h Training) Assign(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	sessionID, e1 := uuid.Parse(r.PostForm.Get("session_id"))
	athleteID, e2 := uuid.Parse(r.PostForm.Get("athlete_id"))
	if e1 != nil || e2 != nil {
		http.Error(w, "Atribuição inválida.", http.StatusUnprocessableEntity)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if !user.IsAdmin {
		allowed, err := h.Store.CanCoachManageTrainingSession(ctx, dbgen.CanCoachManageTrainingSessionParams{SessionID: sessionID, UserID: user.ID})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		if !allowed {
			h.System.Forbidden(w, r)
			return
		}
	}
	n, err := h.Store.AssignTrainingSession(ctx, dbgen.AssignTrainingSessionParams{SessionID: sessionID, UserID: athleteID, AssignedByID: user.ID})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if n != 1 {
		http.Error(w, "O atleta não pertence ao âmbito ativo da sessão ou já está atribuído.", http.StatusConflict)
		return
	}
	httpx.Redirect(w, r, "/treinos", http.StatusSeeOther)
}

func (h Training) Complete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	sessionID, err := uuid.Parse(r.PostForm.Get("session_id"))
	if err != nil {
		http.Error(w, "Sessão inválida.", http.StatusUnprocessableEntity)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	n, err := h.Store.CompleteTrainingSession(ctx, dbgen.CompleteTrainingSessionParams{UserID: &user.ID, SessionID: sessionID})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if n != 1 {
		http.Error(w, "A sessão não está disponível para conclusão.", http.StatusConflict)
		return
	}
	httpx.Redirect(w, r, "/treinos", http.StatusSeeOther)
}

func (h Training) CreateDocument(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	title, rawURL, source := strings.TrimSpace(r.PostForm.Get("title")), strings.TrimSpace(r.PostForm.Get("url")), strings.TrimSpace(r.PostForm.Get("source"))
	eventID, e1 := optionalUUID(r.PostForm.Get("event_id"))
	modalityID, e2 := optionalUUID(r.PostForm.Get("modality_id"))
	programmeID, teamID, e3 := trainingScope(r)
	reviewed, e4 := time.Parse("2006-01-02", r.PostForm.Get("reviewed_on"))
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || !validCompetitionDocumentInput(title, rawURL, source, reviewed, eventID, modalityID, programmeID, teamID) {
		http.Error(w, "Dados do documento inválidos.", http.StatusUnprocessableEntity)
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
	httpx.Redirect(w, r, "/treinos", http.StatusSeeOther)
}

func (h Training) authoring(ctx context.Context, user CurrentUser, page *pages.TrainingPage) {
	programmes, _ := h.Store.ListProgrammes(ctx)
	teams, _ := h.Store.ListTeamsForEventAuthoring(ctx)
	modalities, _ := h.Store.ListAnnouncementModalities(ctx)
	events, _ := h.Store.ListAnnouncementEvents(ctx)
	athletes, _ := h.Store.ListTrainingAthletesForCoach(ctx, dbgen.ListTrainingAthletesForCoachParams{UserID: user.ID, RowLimit: 200})
	plans, _ := h.Store.ListTrainingPlansForCoach(ctx, dbgen.ListTrainingPlansForCoachParams{UserID: user.ID, RowLimit: 100})
	if user.IsAdmin {
		adminAthletes, _ := h.Store.ListTrainingAthletesForAdmin(ctx, 200)
		athletes = make([]dbgen.ListTrainingAthletesForCoachRow, len(adminAthletes))
		for i, athlete := range adminAthletes {
			athletes[i] = dbgen.ListTrainingAthletesForCoachRow{ID: athlete.ID, Name: athlete.Name}
		}
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
	for _, x := range athletes {
		page.Athletes = append(page.Athletes, pages.TrainingChoice{ID: x.ID.String(), Name: x.Name})
	}
	for _, x := range plans {
		page.Plans = append(page.Plans, pages.TrainingChoice{ID: x.ID.String(), Name: x.Title})
	}
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
