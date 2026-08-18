package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type StructuredTraining struct {
	Store    StructuredTrainingStore
	System   System
	PageMeta components.PageMeta
	Location *time.Location
	Sessions *scs.SessionManager
}

type structuredTrainingRow struct {
	athleteName, groupName, scope, planTitle, planDescription, seasonName, sessionTitle, sessionDescription string
	groupID, planID, sessionID, segmentID, blockID, exerciseID                                              *uuid.UUID
	memberCount, segmentPosition, blockPosition, exercisePosition                                           int
	plannedLoadPercentage                                                                                   *int16
	weekStart                                                                                               time.Time
	startsAt, endsAt                                                                                        time.Time
	entryKind, modality, segmentTitle, segmentLocation, blockPurpose, blockTitle, instructions              string
	duration, startOffset, transition                                                                       int
	plannedStartSet                                                                                         bool
	equipmentNotes                                                                                          string
	gymStructure, gymObjective                                                                              string
	gymRounds, gymRoundRecovery                                                                             int
	exerciseName, exercisePrescription, exerciseResistance, exerciseIntent, exerciseTempo, exerciseNotes    string
	exerciseSets, exerciseRepetitions, exerciseDuration, exerciseDistance, exerciseRecovery                 int
	waterMethod, waterTarget, waterProfile                                                                  string
	waterTargetDistance                                                                                     int
	waterTargetCertainty                                                                                    string
	waterStepID, waterParentStepID                                                                          *uuid.UUID
	waterStepPosition, waterStepRepeats, waterStepRecovery                                                  int
	waterStepKind, waterStepName, waterStepPrescription, waterStepIntensity, waterStepDrill, waterStepNotes string
	waterStepDuration, waterStepDistance                                                                    int
	waterStepDurationCertainty, waterStepDistanceCertainty                                                  string
}

const structuredPrescriptionSchemaVersion = 1

type structuredPrescriptionSnapshot struct {
	SchemaVersion   int                             `json:"schema_version"`
	PlanID          string                          `json:"plan_id"`
	PlanTitle       string                          `json:"plan_title"`
	GroupName       string                          `json:"group_name"`
	WeekTitle       string                          `json:"week_title"`
	WeekDescription string                          `json:"week_description"`
	Season          string                          `json:"season"`
	DateRange       string                          `json:"date_range"`
	PlannedLoad     string                          `json:"planned_load"`
	Session         pages.StructuredTrainingSession `json:"session"`
}

type structuredTrainingSubject struct {
	ID   uuid.UUID
	Name string
}

var errStructuredTrainingSubjectNotFound = errors.New("structured training subject not found")

func (h StructuredTraining) Index(w http.ResponseWriter, r *http.Request) {
	h.renderIndex(w, r, http.StatusOK, pages.StructuredTrainingPage{})
}

func (h StructuredTraining) renderIndex(w http.ResponseWriter, r *http.Request, status int, page pages.StructuredTrainingPage) {
	user, _ := CurrentUserFromContext(r.Context())
	page.Management = strings.HasPrefix(r.URL.Path, "/admin/")
	page.CanManageWaterProfiles = page.Management && user.IsAdmin
	var selectedSubject *structuredTrainingSubject
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if page.Management {
		rows, err := h.Store.ListStructuredTrainingOverviewForManager(ctx, dbgen.ListStructuredTrainingOverviewForManagerParams{IsAdmin: user.IsAdmin, UserID: user.ID})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page.Audiences = assembleStructuredTraining(managerStructuredRows(rows), h.location())
		page.RoutineQuery = strings.TrimSpace(r.URL.Query().Get("routine_query"))
		page.RoutineModality = r.URL.Query().Get("routine_modality")
		page.RoutineObjective = r.URL.Query().Get("routine_objective")
		page.RoutineTag = strings.TrimSpace(r.URL.Query().Get("routine_tag"))
		routines, err := h.Store.ListVisibleTrainingRoutines(ctx, dbgen.ListVisibleTrainingRoutinesParams{UserID: user.ID, IsAdmin: user.IsAdmin, Query: page.RoutineQuery, Modality: page.RoutineModality, Objective: page.RoutineObjective, Tag: page.RoutineTag})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page.Routines = structuredRoutineRows(routines, h.location())
		profiles, err := h.Store.ListActiveWaterIntensityProfiles(ctx)
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page.WaterProfiles = structuredWaterProfiles(profiles)
		memberships, err := h.Store.ListEligibleTrainingGroupMemberships(ctx, dbgen.ListEligibleTrainingGroupMembershipsParams{IsAdmin: user.IsAdmin, UserID: user.ID})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page.Memberships, page.Programmes, page.Teams = structuredChoices(memberships)
		page.Groups, page.Weeks, page.Sessions, page.Segments = structuredPlanChoices(page.Audiences)
		variationMembers, err := h.Store.ListManagedTrainingGroupMembers(ctx, dbgen.ListManagedTrainingGroupMembersParams{IsAdmin: user.IsAdmin, UserID: user.ID})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page.VariationMembers = structuredVariationMembers(variationMembers)
		modalities, err := h.Store.ListStructuredCrewModalities(ctx)
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page.CrewModalities = structuredCrewModalities(modalities)
		competitionEvents, err := h.Store.ListManagedStructuredCompetitionEvents(ctx, dbgen.ListManagedStructuredCompetitionEventsParams{IsAdmin: user.IsAdmin, UserID: user.ID})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page.CompetitionEvents = structuredCompetitionEvents(competitionEvents, h.location())
		variationGroups, err := h.Store.ListManagedTrainingVariationGroups(ctx, dbgen.ListManagedTrainingVariationGroupsParams{IsAdmin: user.IsAdmin, UserID: user.ID})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page.VariationGroups = structuredVariationGroups(variationGroups, h.location())
		page.VariationTargets = structuredVariationTargets(variationMembers, variationGroups)
		page.VariationSubjects = structuredVariationSubjects(page.Audiences)
		variationMatches, err := h.Store.ListTrainingVariationMatchesForManager(ctx, dbgen.ListTrainingVariationMatchesForManagerParams{TimeZone: h.location().String(), IsAdmin: user.IsAdmin, UserID: user.ID})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page.VariationPreviews = structuredVariationPreviews(variationMatches, h.location())
		publicationRows, err := h.Store.ListManagedTrainingPublicationStates(ctx, dbgen.ListManagedTrainingPublicationStatesParams{IsAdmin: user.IsAdmin, UserID: user.ID})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page.Publications = h.structuredPublicationStates(ctx, page.Audiences, publicationRows, variationMatches)
	} else {
		rows, err := h.Store.ListTrainingPrescriptionsForViewer(ctx, dbgen.ListTrainingPrescriptionsForViewerParams{UserID: user.ID, IsAdmin: user.IsAdmin})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		var subjects []structuredTrainingSubject
		rows, subjects, selectedSubject, err = filterStructuredTrainingSubjectRows(rows, r.URL.Query().Get("subject_user_id"))
		if errors.Is(err, errStructuredTrainingSubjectNotFound) {
			h.System.NotFound(w, r)
			return
		}
		page.Audiences = structuredPublishedTraining(rows, h.location())
		page.AllSubjectsSelected = selectedSubject == nil
		for _, subject := range subjects {
			page.Subjects = append(page.Subjects, pages.StructuredTrainingSubjectChoice{ID: subject.ID.String(), Name: subject.Name, Selected: selectedSubject != nil && selectedSubject.ID == subject.ID, Self: subject.ID == user.ID})
		}
	}
	page.Meta = h.meta(r, user, page.Management)
	if page.Management {
		requestedGroupID, requestedWeekID, requestedSessionID := r.URL.Query().Get("group_id"), r.URL.Query().Get("week_id"), r.URL.Query().Get("session_id")
		if returnGroupID, returnWeekID, returnSessionID := structuredPlannerContextFromReturn(page.PlannerReturnURL); returnGroupID != "" {
			requestedGroupID, requestedWeekID, requestedSessionID = returnGroupID, returnWeekID, returnSessionID
		}
		page.SelectedGroupID, page.SelectedWeekID, page.SelectedSessionID = structuredPlannerSelection(
			page.Audiences,
			requestedGroupID,
			requestedWeekID,
			requestedSessionID,
		)
		page.PlannerReturnURL = structuredPlannerURL(page.SelectedGroupID, page.SelectedWeekID, page.SelectedSessionID)
	}
	page.Meta.PageLabel = "Planeamento semanal"
	if page.Management {
		page.Meta.Breadcrumbs = []components.NavigationItem{{Label: "Planear treinos", Path: "/admin/treinos"}}
	} else {
		page.Meta.Breadcrumbs = []components.NavigationItem{{Label: "Treinos", Path: "/treinos"}}
		if selectedSubject != nil && selectedSubject.ID != user.ID {
			page.Meta.SubjectContext = selectedSubject.Name
		}
	}
	page.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	if h.Sessions != nil && page.Success == "" {
		page.Success = h.Sessions.PopString(r.Context(), "structured_training_flash")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.StructuredTraining(page).Render(r.Context(), w)
}

const structuredPlannerPath = "/admin/treinos/estruturados"

func structuredPlannerURL(groupID, weekID, sessionID string) string {
	values := url.Values{}
	if groupID != "" {
		values.Set("group_id", groupID)
	}
	if weekID != "" {
		values.Set("week_id", weekID)
	}
	if sessionID != "" {
		values.Set("session_id", sessionID)
	}
	if len(values) == 0 {
		return structuredPlannerPath + "#training-plan"
	}
	return structuredPlannerPath + "?" + values.Encode() + "#training-plan"
}

func structuredPlannerReturn(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Path != structuredPlannerPath || parsed.Fragment != "training-plan" {
		return ""
	}
	values := parsed.Query()
	for key, entries := range values {
		if (key != "group_id" && key != "week_id" && key != "session_id") || len(entries) != 1 {
			return ""
		}
	}
	for _, key := range []string{"group_id", "week_id", "session_id"} {
		if value := values.Get(key); value != "" {
			id, parseErr := uuid.Parse(value)
			if parseErr != nil || id.String() != value {
				return ""
			}
		}
	}
	if values.Get("group_id") == "" {
		return ""
	}
	return structuredPlannerURL(values.Get("group_id"), values.Get("week_id"), values.Get("session_id"))
}

func structuredPlannerContextFromReturn(raw string) (groupID, weekID, sessionID string) {
	normalized := structuredPlannerReturn(raw)
	if normalized == "" {
		return "", "", ""
	}
	parsed, _ := url.Parse(normalized)
	return parsed.Query().Get("group_id"), parsed.Query().Get("week_id"), parsed.Query().Get("session_id")
}

func structuredPlannerSessionReturn(raw, weekID, sessionID string) string {
	groupID, selectedWeekID, _ := structuredPlannerContextFromReturn(raw)
	if selectedWeekID != weekID {
		return ""
	}
	return structuredPlannerURL(groupID, weekID, sessionID)
}

func structuredPlannerWeekReturn(raw, weekID string) string {
	groupID, selectedWeekID, sessionID := structuredPlannerContextFromReturn(raw)
	if selectedWeekID != weekID {
		return ""
	}
	return structuredPlannerURL(groupID, weekID, sessionID)
}

func structuredPlannerSelection(audiences []pages.StructuredTrainingAudience, requestedGroupID, requestedWeekID, requestedSessionID string) (groupID, weekID, sessionID string) {
	if len(audiences) == 0 {
		return "", "", ""
	}
	selectedAudience := audiences[0]
	for _, audience := range audiences {
		if audience.GroupID == requestedGroupID {
			selectedAudience = audience
			break
		}
	}
	groupID = selectedAudience.GroupID
	if len(selectedAudience.Weeks) == 0 {
		return groupID, "", ""
	}
	selectedWeek := selectedAudience.Weeks[0]
	for _, week := range selectedAudience.Weeks {
		if week.ID == requestedWeekID {
			selectedWeek = week
			break
		}
	}
	weekID = selectedWeek.ID
	if len(selectedWeek.Sessions) == 0 {
		return groupID, weekID, ""
	}
	sessionID = selectedWeek.Sessions[0].ID
	for _, session := range selectedWeek.Sessions {
		if session.ID == requestedSessionID {
			sessionID = session.ID
			break
		}
	}
	return groupID, weekID, sessionID
}

func (h StructuredTraining) PublishPlan(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	planID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	sourceUpdatedAt, err := time.Parse(time.RFC3339Nano, r.PostForm.Get("source_updated_at"))
	changeSummary := strings.TrimSpace(r.PostForm.Get("change_summary"))
	if err != nil || !validTrainingText(changeSummary, 2, 500) {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*trainingQueryTimeout)
	defer cancel()
	if !h.canManageWeek(ctx, user, planID, w, r) {
		return
	}
	input, err := h.buildStructuredPublication(ctx, user, planID, sourceUpdatedAt, changeSummary)
	if errors.Is(err, errStructuredTrainingPublicationConflict) {
		h.renderIndex(w, r, http.StatusConflict, pages.StructuredTrainingPage{Error: "O plano mudou desde a pré-visualização. Reveja os destinatários e tente novamente."})
		return
	}
	if errors.Is(err, errStructuredTrainingPublicationVariationConflict) {
		h.renderIndex(w, r, http.StatusConflict, pages.StructuredTrainingPage{Error: "Existem variações do mesmo nível em conflito. Resolva-as antes de publicar."})
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	publication, err := h.Store.PublishStructuredTrainingPlan(ctx, input)
	if errors.Is(err, errStructuredTrainingPublicationConflict) || isUniqueViolation(err) {
		h.renderIndex(w, r, http.StatusConflict, pages.StructuredTrainingPage{Error: "O plano já foi alterado ou publicado. Atualize a pré-visualização antes de confirmar."})
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, fmt.Sprintf("Revisão %d publicada para %d prescrições privadas.", publication.Revision, len(input.Prescriptions)))
	httpx.Redirect(w, r, "/admin/treinos/estruturados#training-publication", http.StatusSeeOther)
}

func (h StructuredTraining) PrescriptionDetail(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	row, err := h.Store.GetTrainingPrescriptionForViewer(ctx, dbgen.GetTrainingPrescriptionForViewerParams{ID: id, UserID: user.ID, IsAdmin: user.IsAdmin})
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	page := pages.StructuredTrainingPage{Audiences: structuredPublishedTraining([]dbgen.ListTrainingPrescriptionsForViewerRow{{
		ID: row.ID, SessionID: row.SessionID, AthleteUserID: row.AthleteUserID, AthleteName: row.AthleteName,
		PlanID: row.PlanID, PlanTitle: row.PlanTitle, WeekStart: row.WeekStart, SeasonName: row.SeasonName,
		Revision: row.Revision, ChangeSummary: row.ChangeSummary, PublishedAt: row.PublishedAt,
		PublishedByName: row.PublishedByName, Snapshot: row.Snapshot, IsCurrent: row.IsCurrent,
		OutcomeStatus: row.OutcomeStatus, DistanceMetres: row.DistanceMetres, ActualDurationMinutes: row.ActualDurationMinutes,
		PerceivedExertion: row.PerceivedExertion, RecoveryFeeling: row.RecoveryFeeling, PerceptionNote: row.PerceptionNote,
		OutcomeUpdatedAt: row.OutcomeUpdatedAt, OutcomeVersion: row.OutcomeVersion,
	}}, h.location())}
	page.Meta = h.meta(r, user, false)
	page.Meta.Title = "Prescrição de treino"
	page.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pages.StructuredTraining(page).Render(r.Context(), w)
}

func (h StructuredTraining) PrescriptionForSession(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	sessionID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	rows, err := h.Store.ListTrainingPrescriptionLinksForSessionViewer(ctx, dbgen.ListTrainingPrescriptionLinksForSessionViewerParams{SessionID: sessionID, UserID: user.ID, IsAdmin: user.IsAdmin})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if len(rows) == 0 {
		h.System.NotFound(w, r)
		return
	}
	if len(rows) == 1 {
		httpx.Redirect(w, r, "/treinos/prescricoes/"+rows[0].ID.String(), http.StatusSeeOther)
		return
	}
	page := pages.TrainingPrescriptionChoicesPage{Meta: h.meta(r, user, false), Choices: make([]pages.TrainingPrescriptionChoice, 0, len(rows))}
	page.Meta.Title = "Escolher prescrição de treino"
	for _, row := range rows {
		page.Choices = append(page.Choices, pages.TrainingPrescriptionChoice{ID: row.ID.String(), Athlete: row.AthleteName, Revision: int(row.Revision), PublishedAt: row.PublishedAt.Time.In(h.location()).Format("02/01/2006 15:04")})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pages.TrainingPrescriptionChoices(page).Render(r.Context(), w)
}

func (h StructuredTraining) CreateGroup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderIndex(w, r, http.StatusBadRequest, pages.StructuredTrainingPage{Error: "Não foi possível ler o formulário.", OpenForm: "group"})
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	form := pages.StructuredTrainingGroupForm{Name: strings.TrimSpace(r.PostForm.Get("name")), ProgrammeID: r.PostForm.Get("programme_id"), TeamID: r.PostForm.Get("team_id"), MembershipIDs: map[string]bool{}, Errors: validation.FieldErrors{}}
	programmeID, programmeErr := optionalUUID(form.ProgrammeID)
	teamID, teamErr := optionalUUID(form.TeamID)
	if !validTrainingText(form.Name, 2, 120) {
		form.Errors.Add("name", "O nome deve ter entre 2 e 120 caracteres.")
	}
	if programmeErr != nil || teamErr != nil || (programmeID == nil) == (teamID == nil) || !structuredScopeAllowed(user, programmeID, teamID) {
		form.Errors.Add("scope", "Selecione exatamente um âmbito que possa gerir.")
	}
	membershipIDs := make([]uuid.UUID, 0, len(r.PostForm["membership_id"]))
	for _, rawID := range r.PostForm["membership_id"] {
		form.MembershipIDs[rawID] = true
		id, err := uuid.Parse(rawID)
		if err != nil {
			form.Errors.Add("members", "Selecione apenas atletas válidos.")
			continue
		}
		membershipIDs = append(membershipIDs, id)
	}
	if len(membershipIDs) == 0 {
		form.Errors.Add("members", "Selecione pelo menos um atleta.")
	}
	if !form.Errors.Empty() {
		h.renderIndex(w, r, http.StatusUnprocessableEntity, pages.StructuredTrainingPage{OpenForm: "group", GroupForm: form})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	_, err := h.Store.CreateGroup(ctx, StructuredTrainingGroupInput{Params: dbgen.CreateStructuredTrainingGroupParams{Name: form.Name, ProgrammeID: programmeID, TeamID: teamID, CreatedByID: user.ID}, MembershipIDs: membershipIDs})
	if errors.Is(err, errStructuredTrainingMembershipScope) {
		form.Errors.Add("members", "Um ou mais atletas já não pertencem ao âmbito selecionado.")
		h.renderIndex(w, r, http.StatusUnprocessableEntity, pages.StructuredTrainingPage{OpenForm: "group", GroupForm: form})
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Grupo de treino criado.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) CreateWeek(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderIndex(w, r, http.StatusBadRequest, pages.StructuredTrainingPage{Error: "Não foi possível ler o formulário.", OpenForm: "week"})
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	returnTo := structuredPlannerReturn(r.PostForm.Get("return_to"))
	form := pages.StructuredTrainingWeekForm{GroupID: r.PostForm.Get("group_id"), Title: strings.TrimSpace(r.PostForm.Get("title")), Description: strings.TrimSpace(r.PostForm.Get("description")), WeekStart: r.PostForm.Get("week_start"), PlannedLoad: strings.TrimSpace(r.PostForm.Get("planned_load_percentage")), Errors: validation.FieldErrors{}}
	groupID, groupErr := uuid.Parse(form.GroupID)
	weekStart, dateErr := time.ParseInLocation("2006-01-02", form.WeekStart, h.location())
	plannedLoad, loadErr := optionalBoundedInt16(form.PlannedLoad, 0, 100)
	if groupErr != nil {
		form.Errors.Add("group_id", "Selecione um grupo válido.")
	}
	if !validTrainingText(form.Title, 2, 180) {
		form.Errors.Add("title", "O título deve ter entre 2 e 180 caracteres.")
	}
	if utf8.RuneCountInString(form.Description) > 4000 {
		form.Errors.Add("description", "As notas não podem exceder 4000 caracteres.")
	}
	if dateErr != nil || weekStart.Weekday() != time.Monday {
		form.Errors.Add("week_start", "Escolha a segunda-feira desta semana.")
	}
	if loadErr != nil {
		form.Errors.Add("planned_load_percentage", "Indique uma percentagem entre 0 e 100 ou deixe em branco.")
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if groupErr == nil && !h.canManageGroup(ctx, user, groupID, w, r) {
		return
	}
	if !form.Errors.Empty() {
		h.renderIndex(w, r, http.StatusUnprocessableEntity, pages.StructuredTrainingPage{OpenForm: "week", WeekForm: form, PlannerReturnURL: returnTo})
		return
	}
	created, err := h.Store.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{Title: form.Title, Description: form.Description, WeekStart: pgtype.Date{Time: weekStart, Valid: true}, PlannedLoadPercentage: plannedLoad, CreatedByID: user.ID, GroupID: groupID})
	if errors.Is(err, pgx.ErrNoRows) {
		form.Errors.Add("week_start", "A semana tem de pertencer a uma época registada.")
		h.renderIndex(w, r, http.StatusUnprocessableEntity, pages.StructuredTrainingPage{OpenForm: "week", WeekForm: form, PlannerReturnURL: returnTo})
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Semana de treino criada.")
	httpx.Redirect(w, r, structuredPlannerURL(form.GroupID, created.ID.String(), ""), http.StatusSeeOther)
}

func (h StructuredTraining) UpdateWeekLoad(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderIndex(w, r, http.StatusBadRequest, pages.StructuredTrainingPage{Error: "Não foi possível ler o formulário."})
		return
	}
	returnTo := structuredPlannerReturn(r.PostForm.Get("return_to"))
	weekID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	load, err := optionalBoundedInt16(strings.TrimSpace(r.PostForm.Get("planned_load_percentage")), 0, 100)
	if err != nil {
		h.renderIndex(w, r, http.StatusUnprocessableEntity, pages.StructuredTrainingPage{Error: "Indique uma percentagem entre 0 e 100 ou deixe em branco.", PlannerReturnURL: returnTo})
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if !h.canManageWeek(ctx, user, weekID, w, r) {
		return
	}
	updated, err := h.Store.UpdateStructuredTrainingWeekLoad(ctx, dbgen.UpdateStructuredTrainingWeekLoadParams{PlanID: weekID, PlannedLoadPercentage: load, IsAdmin: user.IsAdmin, UserID: user.ID})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if updated == 0 {
		http.NotFound(w, r)
		return
	}
	h.flash(r, "Carga planeada da semana atualizada.")
	if destination := structuredPlannerWeekReturn(returnTo, weekID.String()); destination != "" {
		httpx.Redirect(w, r, destination, http.StatusSeeOther)
		return
	}
	httpx.Redirect(w, r, structuredPlannerPath+"#training-plan", http.StatusSeeOther)
}

func (h StructuredTraining) CreateSession(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderIndex(w, r, http.StatusBadRequest, pages.StructuredTrainingPage{Error: "Não foi possível ler o formulário.", OpenForm: "session"})
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	returnTo := structuredPlannerReturn(r.PostForm.Get("return_to"))
	form := pages.StructuredTrainingSessionForm{PlanID: r.PostForm.Get("plan_id"), Title: strings.TrimSpace(r.PostForm.Get("title")), Description: strings.TrimSpace(r.PostForm.Get("description")), StartsAt: r.PostForm.Get("starts_at"), EndsAt: r.PostForm.Get("ends_at"), EntryKind: r.PostForm.Get("entry_kind"), Errors: validation.FieldErrors{}}
	planID, planErr := uuid.Parse(form.PlanID)
	startsAt, startErr := time.ParseInLocation("2006-01-02T15:04", form.StartsAt, h.location())
	endsAt, endErr := time.ParseInLocation("2006-01-02T15:04", form.EndsAt, h.location())
	entryKind := dbgen.TrainingEntryKind(form.EntryKind)
	if planErr != nil {
		form.Errors.Add("plan_id", "Selecione uma semana válida.")
	}
	if !validTrainingText(form.Title, 2, 180) {
		form.Errors.Add("title", "O título deve ter entre 2 e 180 caracteres.")
	}
	if utf8.RuneCountInString(form.Description) > 4000 {
		form.Errors.Add("description", "As instruções não podem exceder 4000 caracteres.")
	}
	if !validTrainingEntryKind(entryKind) {
		form.Errors.Add("entry_kind", "Selecione um tipo válido.")
	}
	if startErr != nil {
		form.Errors.Add("starts_at", "Introduza uma data e hora válidas.")
	}
	if endErr != nil {
		form.Errors.Add("ends_at", "Introduza uma data e hora válidas.")
	} else if startErr == nil && !endsAt.After(startsAt) {
		form.Errors.Add("ends_at", "O fim tem de ser posterior ao início.")
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if planErr == nil && !h.canManageWeek(ctx, user, planID, w, r) {
		return
	}
	if !form.Errors.Empty() {
		h.renderIndex(w, r, http.StatusUnprocessableEntity, pages.StructuredTrainingPage{OpenForm: "session", SessionForm: form, PlannerReturnURL: returnTo})
		return
	}
	created, err := h.Store.CreateStructuredTrainingSession(ctx, dbgen.CreateStructuredTrainingSessionParams{Title: form.Title, Description: form.Description, StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: endsAt, Valid: true}, EntryKind: entryKind, CreatedByID: user.ID, PlanID: planID})
	if errors.Is(err, pgx.ErrNoRows) {
		form.Errors.Add("starts_at", "A sessão tem de ficar inteiramente dentro da semana selecionada.")
		h.renderIndex(w, r, http.StatusUnprocessableEntity, pages.StructuredTrainingPage{OpenForm: "session", SessionForm: form, PlannerReturnURL: returnTo})
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Sessão estruturada criada.")
	if destination := structuredPlannerSessionReturn(returnTo, form.PlanID, created.ID.String()); destination != "" {
		httpx.Redirect(w, r, destination, http.StatusSeeOther)
		return
	}
	httpx.Redirect(w, r, structuredPlannerPath+"#training-plan", http.StatusSeeOther)
}

func (h StructuredTraining) CreateSegment(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	sessionID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	modality := dbgen.TrainingSegmentModality(r.PostForm.Get("modality"))
	purpose := dbgen.TrainingBlockPurpose(r.PostForm.Get("purpose"))
	title, location := strings.TrimSpace(r.PostForm.Get("title")), strings.TrimSpace(r.PostForm.Get("location"))
	blockTitle := strings.TrimSpace(r.PostForm.Get("block_title"))
	instructions := strings.TrimSpace(r.PostForm.Get("instructions"))
	duration, durationErr := optionalPositiveInt32(r.PostForm.Get("planned_duration_minutes"), 1440)
	startOffset, startOffsetErr := optionalNonNegativeInt32(r.PostForm.Get("planned_start_offset_minutes"), 1440)
	transition, transitionErr := optionalPositiveInt32(r.PostForm.Get("transition_duration_minutes"), 1440)
	equipmentNotes := strings.TrimSpace(r.PostForm.Get("equipment_notes"))
	if !validTrainingSegmentModality(modality) || !validTrainingBlockPurpose(purpose) || utf8.RuneCountInString(title) > 120 || utf8.RuneCountInString(location) > 180 || utf8.RuneCountInString(blockTitle) > 120 || !validTrainingText(instructions, 2, 4000) || utf8.RuneCountInString(equipmentNotes) > 1000 || durationErr != nil || startOffsetErr != nil || transitionErr != nil {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	planID, err := h.Store.GetStructuredSessionPlanID(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageWeek(ctx, user, planID, w, r) {
		return
	}
	_, err = h.Store.CreateSegment(ctx, StructuredTrainingSegmentInput{Segment: dbgen.CreateTrainingSessionSegmentParams{SessionID: sessionID, Modality: modality, Title: title, Location: location, PlannedDurationMinutes: duration, PlannedStartOffsetMinutes: startOffset, TransitionDurationMinutes: transition, EquipmentNotes: equipmentNotes}, Block: dbgen.CreateTrainingSegmentBlockParams{Purpose: purpose, Title: blockTitle, Instructions: instructions}})
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.RequestRejected(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Segmento adicionado.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) CreateBlock(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	segmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	purpose := dbgen.TrainingBlockPurpose(r.PostForm.Get("purpose"))
	title := strings.TrimSpace(r.PostForm.Get("title"))
	instructions := strings.TrimSpace(r.PostForm.Get("instructions"))
	if !validTrainingBlockPurpose(purpose) || utf8.RuneCountInString(title) > 120 || !validTrainingText(instructions, 2, 4000) {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	planID, err := h.Store.GetStructuredSegmentPlanID(ctx, segmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageWeek(ctx, user, planID, w, r) {
		return
	}
	_, err = h.Store.CreateTrainingSegmentBlock(ctx, dbgen.CreateTrainingSegmentBlockParams{SegmentID: segmentID, Purpose: purpose, Title: title, Instructions: instructions})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Bloco adicionado.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) CreateGymBlock(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	segmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	block, prescription, exercise, err := parseStructuredGymForm(r, segmentID, true)
	if err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	planID, err := h.Store.GetStructuredSegmentPlanID(ctx, segmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageWeek(ctx, user, planID, w, r) {
		return
	}
	if _, err := h.Store.CreateGymBlock(ctx, StructuredGymBlockInput{Block: block, Prescription: prescription, Exercise: exercise}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			h.System.RequestRejected(w, r)
			return
		}
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Bloco de ginásio adicionado.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) CreateGymExercise(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	blockID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	_, _, exercise, err := parseStructuredGymForm(r, blockID, false)
	if err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	planID, err := h.Store.GetStructuredBlockPlanID(ctx, blockID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageWeek(ctx, user, planID, w, r) {
		return
	}
	exercise.BlockID = blockID
	if _, err := h.Store.CreateGymExercise(ctx, exercise); err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Exercício adicionado.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) CreateWaterBlock(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	segmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	block, prescription, step, err := parseStructuredWaterForm(r, segmentID, true)
	if err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	planID, err := h.Store.GetStructuredSegmentPlanID(ctx, segmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageWeek(ctx, user, planID, w, r) {
		return
	}
	if _, err := h.Store.CreateWaterBlock(ctx, StructuredWaterBlockInput{Block: block, Prescription: prescription, Step: step}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isStructuredCopyRejection(err) {
			h.System.RequestRejected(w, r)
			return
		}
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Bloco de água adicionado.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

var (
	errStructuredWaterTaskNotFound  = errors.New("structured water task not found")
	errStructuredWaterTaskForbidden = errors.New("structured water task forbidden")
)

type structuredWaterTaskContext struct {
	group   pages.StructuredTrainingAudience
	week    pages.StructuredTrainingWeek
	session pages.StructuredTrainingSession
	segment pages.StructuredTrainingSegment
}

func (h StructuredTraining) WaterBlockTask(w http.ResponseWriter, r *http.Request) {
	h.renderWaterBlockTask(w, r, http.StatusOK, structuredWaterBlockTaskForm(r.PostForm, nil))
}

func (h StructuredTraining) CreateWaterBlockTask(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	form := structuredWaterBlockTaskForm(r.PostForm, nil)
	sessionID, segmentID, err := structuredWaterTaskIDs(r)
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	task, err := h.structuredWaterTaskContext(ctx, user, sessionID, segmentID)
	if h.writeStructuredWaterTaskContextError(w, r, err) {
		return
	}
	block, prescription, step, err := parseStructuredWaterForm(r, segmentID, true)
	if err != nil {
		form.Errors = structuredWaterTaskErrors(r, err)
		h.renderWaterBlockTask(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	if _, err := h.Store.CreateWaterBlock(ctx, StructuredWaterBlockInput{Block: block, Prescription: prescription, Step: step}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isStructuredCopyRejection(err) {
			form.Errors = validation.FieldErrors{"form": "Este segmento foi alterado. Reveja o plano antes de tentar novamente."}
			h.renderWaterBlockTask(w, r, http.StatusConflict, form)
			return
		}
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Bloco de água adicionado.")
	returnTo := structuredPlannerReturn(r.PostForm.Get("return_to"))
	if returnTo == "" {
		returnTo = structuredPlannerURL(task.group.GroupID, task.week.ID, task.session.ID)
	}
	httpx.Redirect(w, r, returnTo, http.StatusSeeOther)
}

func (h StructuredTraining) renderWaterBlockTask(w http.ResponseWriter, r *http.Request, status int, form pages.StructuredWaterBlockTaskForm) {
	sessionID, segmentID, err := structuredWaterTaskIDs(r)
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	task, err := h.structuredWaterTaskContext(ctx, user, sessionID, segmentID)
	if h.writeStructuredWaterTaskContextError(w, r, err) {
		return
	}
	profiles, err := h.Store.ListActiveWaterIntensityProfiles(ctx)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	returnTo := structuredPlannerReturn(r.Form.Get("return_to"))
	if returnTo == "" {
		returnTo = structuredPlannerURL(task.group.GroupID, task.week.ID, task.session.ID)
	}
	meta := h.meta(r, user, true)
	meta.Title = "Adicionar bloco de água | MyCFC"
	meta.PageLabel = "Adicionar bloco de água"
	meta.Breadcrumbs = []components.NavigationItem{{Label: "Planear treinos", Path: returnTo}}
	page := pages.StructuredWaterBlockTaskPage{
		Meta: meta, CSRFField: templ.Raw(string(csrf.TemplateField(r))),
		ActionURL: structuredWaterTaskPath(sessionID, segmentID), ReturnURL: returnTo,
		GroupName: task.group.GroupName, WeekTitle: task.week.Title, SessionTitle: task.session.Title,
		SegmentTitle: structuredSegmentTaskTitle(task.segment), SegmentModality: task.segment.Modality,
		WaterProfiles: structuredWaterProfiles(profiles), Form: form,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.StructuredWaterBlockTask(page).Render(r.Context(), w)
}

func structuredWaterTaskIDs(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	sessionID, err := uuid.Parse(r.PathValue("session_id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	segmentID, err := uuid.Parse(r.PathValue("segment_id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return sessionID, segmentID, nil
}

func structuredWaterTaskPath(sessionID, segmentID uuid.UUID) string {
	return "/admin/treinos/estruturados/sessoes/" + sessionID.String() + "/segmentos/" + segmentID.String() + "/agua"
}

func (h StructuredTraining) structuredWaterTaskContext(ctx context.Context, user CurrentUser, sessionID, segmentID uuid.UUID) (structuredWaterTaskContext, error) {
	rows, err := h.Store.ListStructuredTrainingOverviewForManager(ctx, dbgen.ListStructuredTrainingOverviewForManagerParams{IsAdmin: user.IsAdmin, UserID: user.ID})
	if err != nil {
		return structuredWaterTaskContext{}, err
	}
	for _, group := range assembleStructuredTraining(managerStructuredRows(rows), h.location()) {
		for _, week := range group.Weeks {
			for _, session := range week.Sessions {
				if session.ID != sessionID.String() {
					continue
				}
				for _, segment := range session.Segments {
					if segment.ID == segmentID.String() {
						planID, err := h.Store.GetStructuredSegmentPlanID(ctx, segmentID)
						if errors.Is(err, pgx.ErrNoRows) {
							return structuredWaterTaskContext{}, errStructuredWaterTaskNotFound
						}
						if err != nil {
							return structuredWaterTaskContext{}, err
						}
						allowed, err := h.Store.CanManageStructuredTrainingWeek(ctx, dbgen.CanManageStructuredTrainingWeekParams{PlanID: planID, IsAdmin: user.IsAdmin, UserID: user.ID})
						if err != nil {
							return structuredWaterTaskContext{}, err
						}
						if !allowed {
							return structuredWaterTaskContext{}, errStructuredWaterTaskForbidden
						}
						return structuredWaterTaskContext{group: group, week: week, session: session, segment: segment}, nil
					}
				}
			}
		}
	}
	return structuredWaterTaskContext{}, errStructuredWaterTaskNotFound
}

func (h StructuredTraining) writeStructuredWaterTaskContextError(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errStructuredWaterTaskNotFound) || errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return true
	}
	if errors.Is(err, errStructuredWaterTaskForbidden) {
		h.System.Forbidden(w, r)
		return true
	}
	h.System.InternalError(w, r)
	return true
}

func structuredSegmentTaskTitle(segment pages.StructuredTrainingSegment) string {
	if segment.Title != "" {
		return segment.Title
	}
	return segment.Modality
}

func structuredWaterBlockTaskForm(values url.Values, fieldErrors validation.FieldErrors) pages.StructuredWaterBlockTaskForm {
	form := pages.StructuredWaterBlockTaskForm{Errors: fieldErrors}
	if form.Errors == nil {
		form.Errors = validation.FieldErrors{}
	}
	form.Purpose, form.Title, form.Instructions, form.Method = values.Get("purpose"), values.Get("title"), values.Get("instructions"), values.Get("method")
	form.IntensityProfileID, form.TargetDistanceMetres, form.TargetDistanceCertainty = values.Get("intensity_profile_id"), values.Get("target_distance_metres"), values.Get("target_distance_certainty")
	form.StepKind, form.StepName, form.Repeats, form.DurationSeconds = values.Get("step_kind"), values.Get("step_name"), values.Get("repeats"), values.Get("duration_seconds")
	form.DurationCertainty, form.DistanceMetres, form.DistanceCertainty = values.Get("duration_certainty"), values.Get("distance_metres"), values.Get("distance_certainty")
	form.RecoverySeconds, form.IntensityCode, form.CadenceSPM = values.Get("recovery_seconds"), values.Get("intensity_code"), values.Get("cadence_spm")
	form.DrillFocus, form.DrillFormat, form.RoleNotes, form.StepInstructions = values.Get("drill_focus"), values.Get("drill_format"), values.Get("role_notes"), values.Get("step_instructions")
	return form
}

func structuredWaterTaskErrors(r *http.Request, err error) validation.FieldErrors {
	message := "Revise os campos obrigatórios e as medidas do primeiro esforço."
	if strings.Contains(err.Error(), "step") {
		return validation.FieldErrors{"step_name": message}
	}
	if strings.Contains(err.Error(), "water block") {
		return validation.FieldErrors{"title": message}
	}
	return validation.FieldErrors{"form": message}
}

func (h StructuredTraining) CreateWaterWorkStep(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	blockID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	_, _, step, err := parseStructuredWaterForm(r, blockID, false)
	if err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	planID, err := h.Store.GetStructuredBlockPlanID(ctx, blockID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageWeek(ctx, user, planID, w, r) {
		return
	}
	step.BlockID = blockID
	if _, err := h.Store.CreateWaterWorkStep(ctx, step); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isStructuredCopyRejection(err) {
			h.System.RequestRejected(w, r)
			return
		}
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Passo de água adicionado.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) CreateWaterIntensityProfile(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	if r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	name, notes := strings.TrimSpace(r.PostForm.Get("name")), strings.TrimSpace(r.PostForm.Get("notes"))
	craft := dbgen.PaddlingCraft(r.PostForm.Get("craft"))
	if !validTrainingText(name, 2, 120) || utf8.RuneCountInString(notes) > 1000 || !validPaddlingCraft(craft) {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if _, err := h.Store.CreateWaterIntensityProfile(ctx, dbgen.CreateWaterIntensityProfileParams{Name: name, Craft: craft, Notes: notes, CreatedByID: user.ID}); err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Nova revisão do perfil de intensidade criada.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) CreateWaterIntensityZone(w http.ResponseWriter, r *http.Request) {
	profileID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	code, label, meaning := strings.TrimSpace(r.PostForm.Get("code")), strings.TrimSpace(r.PostForm.Get("label")), strings.TrimSpace(r.PostForm.Get("meaning"))
	minimum, minErr := optionalNonNegativeInt32(r.PostForm.Get("cadence_min"), 300)
	maximum, maxErr := optionalNonNegativeInt32(r.PostForm.Get("cadence_max"), 300)
	if !validTrainingText(code, 1, 20) || !validTrainingText(label, 2, 120) || !validTrainingText(meaning, 2, 500) || minErr != nil || maxErr != nil || (minimum != nil && maximum != nil && *minimum > *maximum) {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if _, err := h.Store.CreateWaterIntensityZone(ctx, dbgen.CreateWaterIntensityZoneParams{ProfileID: profileID, Code: code, Label: label, CadenceMin: minimum, CadenceMax: maximum, Meaning: meaning}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isStructuredCopyRejection(err) {
			h.System.RequestRejected(w, r)
			return
		}
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Zona de intensidade adicionada.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) CreateRoutine(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	kind := dbgen.TrainingRoutineKind(r.PostForm.Get("source_kind"))
	sourceID, idErr := uuid.Parse(r.PostForm.Get("source_id"))
	name := strings.TrimSpace(r.PostForm.Get("name"))
	description := strings.TrimSpace(r.PostForm.Get("description"))
	method := strings.TrimSpace(r.PostForm.Get("method"))
	visibility := dbgen.TrainingRoutineVisibility(r.PostForm.Get("visibility"))
	tags, tagsErr := parseTrainingRoutineTags(r.PostForm.Get("tags"))
	if idErr != nil || !validTrainingRoutineKind(kind) || !validTrainingRoutineVisibility(visibility) || !validTrainingText(name, 2, 180) || utf8.RuneCountInString(description) > 1000 || utf8.RuneCountInString(method) > 80 || tagsErr != nil {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	source, err := h.Store.GetRoutineSource(ctx, kind, sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageWeek(ctx, user, source.PlanID, w, r) {
		return
	}
	var programmeID, teamID *uuid.UUID
	if visibility == dbgen.TrainingRoutineVisibilitySHARED {
		if source.TeamID != nil {
			teamID = source.TeamID
		} else if source.ProgrammeID != nil {
			programmeID = source.ProgrammeID
		} else {
			h.System.RequestRejected(w, r)
			return
		}
	}
	_, err = h.Store.CreateTrainingRoutine(ctx, dbgen.CreateTrainingRoutineParams{Name: name, Description: description, Kind: kind, Visibility: visibility, OwnerUserID: user.ID, ProgrammeID: programmeID, TeamID: teamID, Modality: source.Modality, Objective: source.Objective, Method: method, Tags: tags, SourceID: sourceID, SourceUpdatedAt: source.SourceUpdatedAt, Snapshot: source.Snapshot})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Rotina guardada como cópia independente.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) InsertRoutine(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	routineID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	targetID, err := uuid.Parse(r.PostForm.Get("target_id"))
	if err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	routine, err := h.Store.GetVisibleTrainingRoutine(ctx, dbgen.GetVisibleTrainingRoutineParams{RoutineID: routineID, UserID: user.ID, IsAdmin: user.IsAdmin})
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	var planID uuid.UUID
	startsAt := pgtype.Timestamptz{}
	switch routine.Kind {
	case dbgen.TrainingRoutineKindBLOCK:
		planID, err = h.Store.GetStructuredSegmentPlanID(ctx, targetID)
	case dbgen.TrainingRoutineKindSEGMENT:
		planID, err = h.Store.GetStructuredSessionPlanID(ctx, targetID)
	case dbgen.TrainingRoutineKindSESSION:
		planID = targetID
		parsed, parseErr := time.ParseInLocation("2006-01-02T15:04", r.PostForm.Get("starts_at"), h.location())
		if parseErr != nil {
			h.System.RequestRejected(w, r)
			return
		}
		startsAt = pgtype.Timestamptz{Time: parsed, Valid: true}
	default:
		err = pgx.ErrNoRows
	}
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageWeek(ctx, user, planID, w, r) {
		return
	}
	if _, err := h.Store.InsertTrainingRoutine(ctx, StructuredRoutineInsertInput{Routine: routine, TargetID: targetID, StartsAt: startsAt, ActorID: user.ID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isStructuredCopyRejection(err) {
			h.System.RequestRejected(w, r)
			return
		}
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Rotina inserida como cópia independente.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) CopyBlock(w http.ResponseWriter, r *http.Request) {
	h.copyBlockOrSession(w, r, true)
}

func (h StructuredTraining) CopySession(w http.ResponseWriter, r *http.Request) {
	h.copyBlockOrSession(w, r, false)
}

func (h StructuredTraining) copyBlockOrSession(w http.ResponseWriter, r *http.Request, block bool) {
	user, _ := CurrentUserFromContext(r.Context())
	sourceID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	targetID, err := uuid.Parse(r.PostForm.Get("target_id"))
	if err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	var sourcePlanID, targetPlanID uuid.UUID
	if block {
		sourcePlanID, err = h.Store.GetStructuredBlockPlanID(ctx, sourceID)
		if err == nil {
			targetPlanID, err = h.Store.GetStructuredSegmentPlanID(ctx, targetID)
		}
	} else {
		sourcePlanID, err = h.Store.GetStructuredSessionPlanID(ctx, sourceID)
		targetPlanID = targetID
	}
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageWeek(ctx, user, sourcePlanID, w, r) || !h.canManageWeek(ctx, user, targetPlanID, w, r) {
		return
	}
	if block {
		_, err = h.Store.CopyTrainingBlock(ctx, sourceID, targetID, user.ID)
	} else {
		startsAt, parseErr := time.ParseInLocation("2006-01-02T15:04", r.PostForm.Get("starts_at"), h.location())
		if parseErr != nil {
			h.System.RequestRejected(w, r)
			return
		}
		_, err = h.Store.CopyTrainingSession(ctx, sourceID, targetID, pgtype.Timestamptz{Time: startsAt, Valid: true}, user.ID)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isStructuredCopyRejection(err) {
			h.System.RequestRejected(w, r)
			return
		}
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Conteúdo copiado de forma independente.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) CopyDay(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	if r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	sourcePlanID, sourcePlanErr := uuid.Parse(r.PostForm.Get("source_plan_id"))
	targetPlanID, targetPlanErr := uuid.Parse(r.PostForm.Get("target_plan_id"))
	sourceDate, sourceDateErr := time.ParseInLocation("2006-01-02", r.PostForm.Get("source_date"), h.location())
	targetDate, targetDateErr := time.ParseInLocation("2006-01-02", r.PostForm.Get("target_date"), h.location())
	if sourcePlanErr != nil || targetPlanErr != nil || sourceDateErr != nil || targetDateErr != nil {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if !h.canManageWeek(ctx, user, sourcePlanID, w, r) || !h.canManageWeek(ctx, user, targetPlanID, w, r) {
		return
	}
	count, err := h.Store.CopyStructuredTrainingDay(ctx, StructuredDayCopyInput{SourcePlanID: sourcePlanID, TargetPlanID: targetPlanID, SourceDate: sourceDate, TargetDate: targetDate, ActorID: user.ID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isStructuredCopyRejection(err) {
			h.System.RequestRejected(w, r)
			return
		}
		h.System.InternalError(w, r)
		return
	}
	if count == 0 {
		h.System.RequestRejected(w, r)
		return
	}
	h.flash(r, fmt.Sprintf("Dia copiado: %d sessões independentes.", count))
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) CopyWeek(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	sourcePlanID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	weekStart, dateErr := time.ParseInLocation("2006-01-02", r.PostForm.Get("week_start"), h.location())
	title := strings.TrimSpace(r.PostForm.Get("title"))
	if dateErr != nil || weekStart.Weekday() != time.Monday || !validTrainingText(title, 2, 180) {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if !h.canManageWeek(ctx, user, sourcePlanID, w, r) {
		return
	}
	if _, err := h.Store.CopyStructuredTrainingWeek(ctx, StructuredWeekCopyInput{SourcePlanID: sourcePlanID, WeekStart: weekStart, Title: title, ActorID: user.ID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isStructuredCopyRejection(err) {
			h.System.RequestRejected(w, r)
			return
		}
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Semana copiada como novo rascunho independente.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) CreateVariationGroup(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	trainingGroupID, groupErr := uuid.Parse(r.PostForm.Get("training_group_id"))
	kind := dbgen.TrainingVariationGroupKind(r.PostForm.Get("kind"))
	name := strings.TrimSpace(r.PostForm.Get("name"))
	effectiveFrom, fromErr := time.ParseInLocation("2006-01-02", r.PostForm.Get("effective_from"), h.location())
	var effectiveUntil pgtype.Date
	if raw := r.PostForm.Get("effective_until"); raw != "" {
		parsed, err := time.ParseInLocation("2006-01-02", raw, h.location())
		if err != nil {
			h.System.RequestRejected(w, r)
			return
		}
		effectiveUntil = pgtype.Date{Time: parsed, Valid: true}
	}
	craftID, craftErr := optionalUUID(r.PostForm.Get("craft_modality_id"))
	competitionID, competitionErr := optionalUUID(r.PostForm.Get("competition_event_id"))
	openEnded := r.PostForm.Get("open_ended_exception") == "true"
	if groupErr != nil || fromErr != nil || craftErr != nil || competitionErr != nil || !validTrainingText(name, 2, 120) || (effectiveUntil.Valid && effectiveUntil.Time.Before(effectiveFrom)) {
		h.System.RequestRejected(w, r)
		return
	}
	membershipIDs := make([]uuid.UUID, 0, len(r.PostForm["membership_id"]))
	seenMembers := map[uuid.UUID]bool{}
	for _, raw := range r.PostForm["membership_id"] {
		membershipID, err := uuid.Parse(raw)
		if err != nil || seenMembers[membershipID] {
			h.System.RequestRejected(w, r)
			return
		}
		seenMembers[membershipID] = true
		membershipIDs = append(membershipIDs, membershipID)
	}
	if len(membershipIDs) == 0 {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if !h.canManageGroup(ctx, user, trainingGroupID, w, r) {
		return
	}
	modalities, err := h.Store.ListStructuredCrewModalities(ctx)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	crewSize, craftAllowed := structuredCrewSize(modalities, craftID)
	switch kind {
	case dbgen.TrainingVariationGroupKindSUBGROUP:
		if craftID != nil || competitionID != nil || openEnded {
			h.System.RequestRejected(w, r)
			return
		}
	case dbgen.TrainingVariationGroupKindCREW:
		if !craftAllowed || crewSize != len(membershipIDs) || (!effectiveUntil.Valid && competitionID == nil && !openEnded) {
			h.System.RequestRejected(w, r)
			return
		}
	default:
		h.System.RequestRejected(w, r)
		return
	}
	if competitionID != nil {
		events, err := h.Store.ListManagedStructuredCompetitionEvents(ctx, dbgen.ListManagedStructuredCompetitionEventsParams{IsAdmin: user.IsAdmin, UserID: user.ID})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		allowed := false
		for _, event := range events {
			if event.ID == *competitionID {
				eventDate := event.StartsAt.Time.In(h.location())
				allowed = !time.Date(eventDate.Year(), eventDate.Month(), eventDate.Day(), 0, 0, 0, 0, h.location()).Before(effectiveFrom)
			}
		}
		if !allowed {
			h.System.Forbidden(w, r)
			return
		}
	}
	_, err = h.Store.CreateTrainingVariationGroup(ctx, StructuredVariationGroupInput{Params: dbgen.CreateTrainingVariationGroupParams{
		TrainingGroupID: trainingGroupID, Name: name, Kind: kind, CraftModalityID: craftID,
		EffectiveFrom: pgtype.Date{Time: effectiveFrom, Valid: true}, EffectiveUntil: effectiveUntil,
		CompetitionEventID: competitionID, OpenEndedException: openEnded, CreatedByID: user.ID,
	}, MembershipIDs: membershipIDs})
	if errors.Is(err, errStructuredVariationMemberScope) || errors.Is(err, errStructuredVariationCrewCapacity) || isUniqueViolation(err) {
		h.System.RequestRejected(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Subgrupo ou tripulação criado.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados#training-variations", http.StatusSeeOther)
}

func (h StructuredTraining) CreateVariation(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	planID, planErr := uuid.Parse(r.PostForm.Get("plan_id"))
	targetKind, targetRaw, targetOK := strings.Cut(r.PostForm.Get("target"), ":")
	targetID, targetErr := uuid.Parse(targetRaw)
	subjectRawKind, subjectRawID, subjectOK := strings.Cut(r.PostForm.Get("subject"), ":")
	subjectID, subjectErr := uuid.Parse(subjectRawID)
	subjectKind := dbgen.TrainingVariationSubjectKind(subjectRawKind)
	operation := dbgen.TrainingVariationOperation(r.PostForm.Get("operation"))
	summary := strings.TrimSpace(r.PostForm.Get("change_summary"))
	if planErr != nil || !targetOK || targetErr != nil || !subjectOK || subjectErr != nil || !validTrainingVariationSubject(subjectKind) || !validTrainingVariationOperation(operation) || !validTrainingText(summary, 2, 500) {
		h.System.RequestRejected(w, r)
		return
	}
	patch, err := parseTrainingVariationPatch(r, subjectKind)
	if err != nil || (operation == dbgen.TrainingVariationOperationOMIT && len(patch) != 0) || (operation != dbgen.TrainingVariationOperationOMIT && len(patch) == 0) {
		h.System.RequestRejected(w, r)
		return
	}
	encodedPatch, err := json.Marshal(patch)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	params := dbgen.CreateTrainingVariationParams{PlanID: planID, SubjectKind: subjectKind, SubjectID: subjectID, Operation: operation, ChangeSummary: summary, Patch: encodedPatch, CreatedByID: user.ID}
	switch targetKind {
	case "ATHLETE":
		params.TargetMembershipID = &targetID
	case "GROUP":
		params.TargetGroupID = &targetID
	default:
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if !h.canManageWeek(ctx, user, planID, w, r) {
		return
	}
	if _, err := h.Store.CreateTrainingVariation(ctx, params); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isUniqueViolation(err) {
			h.System.RequestRejected(w, r)
			return
		}
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Variação adicionada; confirme a pré-visualização por atleta.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados#training-variations", http.StatusSeeOther)
}

func (h StructuredTraining) RetireVariation(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	version64, err := strconv.ParseInt(r.PostForm.Get("version"), 10, 32)
	if err != nil || version64 < 1 {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	planID, err := h.Store.GetTrainingVariationPlanID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageWeek(ctx, user, planID, w, r) {
		return
	}
	rows, err := h.Store.RetireTrainingVariation(ctx, dbgen.RetireTrainingVariationParams{RetiredByID: &user.ID, ID: id, Version: int32(version64)})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if rows != 1 {
		h.System.RequestRejected(w, r)
		return
	}
	h.flash(r, "Variação retirada.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados#training-variations", http.StatusSeeOther)
}

func (h StructuredTraining) MoveSegment(w http.ResponseWriter, r *http.Request) { h.move(w, r, true) }
func (h StructuredTraining) MoveBlock(w http.ResponseWriter, r *http.Request)   { h.move(w, r, false) }
func (h StructuredTraining) MoveGymExercise(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || r.ParseForm() != nil {
		h.System.RequestRejected(w, r)
		return
	}
	direction := int32(1)
	if r.PostForm.Get("direction") == "up" {
		direction = -1
	} else if r.PostForm.Get("direction") != "down" {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	planID, err := h.Store.GetGymExercisePlanID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageWeek(ctx, user, planID, w, r) {
		return
	}
	if _, err := h.Store.MoveGymExercise(ctx, dbgen.MoveGymExerciseParams{ExerciseID: id, Direction: direction}); err != nil {
		h.System.InternalError(w, r)
		return
	}
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) move(w http.ResponseWriter, r *http.Request, segment bool) {
	user, _ := CurrentUserFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	direction := int32(1)
	if r.PostForm.Get("direction") == "up" {
		direction = -1
	} else if r.PostForm.Get("direction") != "down" {
		h.System.RequestRejected(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	var planID uuid.UUID
	if segment {
		planID, err = h.Store.GetStructuredSegmentPlanID(ctx, id)
	} else {
		planID, err = h.Store.GetStructuredBlockPlanID(ctx, id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageWeek(ctx, user, planID, w, r) {
		return
	}
	if segment {
		_, err = h.Store.MoveTrainingSessionSegment(ctx, dbgen.MoveTrainingSessionSegmentParams{SegmentID: id, Direction: direction})
	} else {
		_, err = h.Store.MoveTrainingSegmentBlock(ctx, dbgen.MoveTrainingSegmentBlockParams{BlockID: id, Direction: direction})
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) canManageGroup(ctx context.Context, user CurrentUser, id uuid.UUID, w http.ResponseWriter, r *http.Request) bool {
	allowed, err := h.Store.CanManageStructuredTrainingGroup(ctx, dbgen.CanManageStructuredTrainingGroupParams{GroupID: id, IsAdmin: user.IsAdmin, UserID: user.ID})
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

func (h StructuredTraining) canManageWeek(ctx context.Context, user CurrentUser, id uuid.UUID, w http.ResponseWriter, r *http.Request) bool {
	allowed, err := h.Store.CanManageStructuredTrainingWeek(ctx, dbgen.CanManageStructuredTrainingWeekParams{PlanID: id, IsAdmin: user.IsAdmin, UserID: user.ID})
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

func (h StructuredTraining) flash(r *http.Request, message string) {
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "structured_training_flash", message)
	}
}

func (h StructuredTraining) location() *time.Location {
	if h.Location != nil {
		return h.Location
	}
	return time.UTC
}

func (h StructuredTraining) meta(r *http.Request, user CurrentUser, management bool) components.PageMeta {
	meta := h.PageMeta
	meta.Title, meta.CurrentPath = "Planeamento semanal | MyCFC", "/treinos/estruturados"
	if management {
		meta.Title, meta.CurrentPath = "Gestão do planeamento semanal | MyCFC", "/admin/treinos/estruturados"
	}
	meta.CurrentUserName, meta.CurrentUserID = user.Name, user.ID.String()
	meta.EmailVerificationPending = !user.IsDependent && !user.EmailVerified
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return meta
}

func structuredScopeAllowed(user CurrentUser, programmeID, teamID *uuid.UUID) bool {
	if (programmeID == nil) == (teamID == nil) {
		return false
	}
	if user.IsAdmin {
		return true
	}
	if programmeID != nil {
		return user.CoachProgrammeIDs[*programmeID]
	}
	return teamID != nil && user.CoachTeamIDs[*teamID]
}

func validTrainingEntryKind(value dbgen.TrainingEntryKind) bool {
	return value == dbgen.TrainingEntryKindTRAINING || value == dbgen.TrainingEntryKindREST || value == dbgen.TrainingEntryKindCOMPETITION || value == dbgen.TrainingEntryKindLOGISTICS
}

func validTrainingVariationSubject(value dbgen.TrainingVariationSubjectKind) bool {
	switch value {
	case dbgen.TrainingVariationSubjectKindSEGMENT, dbgen.TrainingVariationSubjectKindBLOCK, dbgen.TrainingVariationSubjectKindWATERSTEP, dbgen.TrainingVariationSubjectKindGYMEXERCISE:
		return true
	default:
		return false
	}
}

func validTrainingVariationOperation(value dbgen.TrainingVariationOperation) bool {
	switch value {
	case dbgen.TrainingVariationOperationOMIT, dbgen.TrainingVariationOperationREPLACE, dbgen.TrainingVariationOperationADD, dbgen.TrainingVariationOperationOVERRIDE:
		return true
	default:
		return false
	}
}

func parseTrainingVariationPatch(r *http.Request, subject dbgen.TrainingVariationSubjectKind) (map[string]any, error) {
	patch := map[string]any{}
	texts := []struct {
		form, key string
		maximum   int
	}{
		{"title", "title", 180}, {"instructions", "instructions", 4000}, {"intensity_code", "intensity_code", 20}, {"resistance", "resistance", 180},
	}
	for _, field := range texts {
		value := strings.TrimSpace(r.PostForm.Get(field.form))
		if value != "" {
			if utf8.RuneCountInString(value) > field.maximum {
				return nil, errors.New("variation text too long")
			}
			patch[field.key] = value
		}
	}
	if raw := r.PostForm.Get("modality"); raw != "" {
		modality := dbgen.TrainingSegmentModality(raw)
		if !validTrainingSegmentModality(modality) {
			return nil, errors.New("invalid variation modality")
		}
		patch["modality"] = string(modality)
	}
	numbers := []struct {
		form, key string
		maximum   int
	}{
		{"repeats", "repeats", 10000}, {"duration_seconds", "duration_seconds", 86400}, {"distance_metres", "distance_metres", 200000}, {"recovery_seconds", "recovery_seconds", 86400}, {"sets", "sets", 100}, {"exercise_repetitions", "repetitions", 10000},
	}
	for _, field := range numbers {
		value, err := optionalPositiveInt32(r.PostForm.Get(field.form), field.maximum)
		if err != nil {
			return nil, err
		}
		if value != nil {
			patch[field.key] = *value
		}
	}
	allowed := map[dbgen.TrainingVariationSubjectKind]map[string]bool{
		dbgen.TrainingVariationSubjectKindSEGMENT:     {"title": true, "modality": true, "instructions": true},
		dbgen.TrainingVariationSubjectKindBLOCK:       {"title": true, "instructions": true},
		dbgen.TrainingVariationSubjectKindWATERSTEP:   {"title": true, "instructions": true, "repeats": true, "duration_seconds": true, "distance_metres": true, "recovery_seconds": true, "intensity_code": true},
		dbgen.TrainingVariationSubjectKindGYMEXERCISE: {"title": true, "instructions": true, "sets": true, "repetitions": true, "duration_seconds": true, "distance_metres": true, "recovery_seconds": true, "resistance": true},
	}[subject]
	for key := range patch {
		if !allowed[key] {
			return nil, errors.New("field is not valid for variation subject")
		}
	}
	return patch, nil
}

func validTrainingSegmentModality(value dbgen.TrainingSegmentModality) bool {
	switch value {
	case dbgen.TrainingSegmentModalityWATER, dbgen.TrainingSegmentModalityGYM, dbgen.TrainingSegmentModalityRUN, dbgen.TrainingSegmentModalityBIKE, dbgen.TrainingSegmentModalityERGOMETER, dbgen.TrainingSegmentModalityFLEXIBILITY, dbgen.TrainingSegmentModalitySPORTSGAMES, dbgen.TrainingSegmentModalityOTHER:
		return true
	default:
		return false
	}
}

func validTrainingBlockPurpose(value dbgen.TrainingBlockPurpose) bool {
	return value == dbgen.TrainingBlockPurposeWARMUP || value == dbgen.TrainingBlockPurposeMAIN || value == dbgen.TrainingBlockPurposeCOOLDOWN || value == dbgen.TrainingBlockPurposeTECHNIQUE || value == dbgen.TrainingBlockPurposeCUSTOM
}

func optionalPositiveInt32(value string, maximum int) (*int32, error) {
	if value == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil || n < 1 || n > int64(maximum) {
		return nil, errors.New("invalid positive integer")
	}
	result := int32(n)
	return &result, nil
}

func optionalNonNegativeInt32(value string, maximum int) (*int32, error) {
	if value == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil || n < 0 || n > int64(maximum) {
		return nil, errors.New("invalid non-negative integer")
	}
	result := int32(n)
	return &result, nil
}

func parseStructuredGymForm(r *http.Request, parentID uuid.UUID, includeBlock bool) (dbgen.CreateTrainingSegmentBlockParams, dbgen.CreateGymBlockPrescriptionParams, dbgen.CreateGymExerciseParams, error) {
	block := dbgen.CreateTrainingSegmentBlockParams{SegmentID: parentID}
	prescription := dbgen.CreateGymBlockPrescriptionParams{}
	if includeBlock {
		block.Purpose = dbgen.TrainingBlockPurpose(r.PostForm.Get("purpose"))
		block.Title = strings.TrimSpace(r.PostForm.Get("title"))
		block.Instructions = strings.TrimSpace(r.PostForm.Get("instructions"))
		prescription.Structure = dbgen.GymBlockStructure(r.PostForm.Get("structure"))
		prescription.Objective = dbgen.TrainingObjective(r.PostForm.Get("objective"))
		rounds, err := optionalPositiveInt32(r.PostForm.Get("rounds"), 100)
		if err != nil || rounds == nil || !validTrainingBlockPurpose(block.Purpose) || !validGymBlockStructure(prescription.Structure) || !validTrainingObjective(prescription.Objective) || utf8.RuneCountInString(block.Title) > 120 || !validTrainingText(block.Instructions, 2, 4000) {
			return block, prescription, dbgen.CreateGymExerciseParams{}, errors.New("invalid gym block")
		}
		prescription.Rounds = *rounds
		prescription.RoundRecoverySeconds, err = optionalPositiveInt32(r.PostForm.Get("round_recovery_seconds"), 86400)
		if err != nil {
			return block, prescription, dbgen.CreateGymExerciseParams{}, err
		}
	}
	exercise, err := parseGymExercise(r)
	return block, prescription, exercise, err
}

func parseGymExercise(r *http.Request) (dbgen.CreateGymExerciseParams, error) {
	exercise := dbgen.CreateGymExerciseParams{Name: strings.TrimSpace(r.PostForm.Get("exercise_name")), Notes: strings.TrimSpace(r.PostForm.Get("exercise_notes"))}
	var err error
	if exercise.Sets, err = optionalPositiveInt32(r.PostForm.Get("sets"), 100); err != nil {
		return exercise, err
	}
	if exercise.Repetitions, err = optionalPositiveInt32(r.PostForm.Get("repetitions"), 10000); err != nil {
		return exercise, err
	}
	if exercise.DurationSeconds, err = optionalPositiveInt32(r.PostForm.Get("duration_seconds"), 86400); err != nil {
		return exercise, err
	}
	if exercise.DistanceMetres, err = optionalPositiveInt32(r.PostForm.Get("distance_metres"), 100000); err != nil {
		return exercise, err
	}
	if exercise.RecoverySeconds, err = optionalPositiveInt32(r.PostForm.Get("recovery_seconds"), 86400); err != nil {
		return exercise, err
	}
	if !validTrainingText(exercise.Name, 2, 180) || utf8.RuneCountInString(exercise.Notes) > 1000 || (exercise.Repetitions == nil && exercise.DurationSeconds == nil && exercise.DistanceMetres == nil) {
		return exercise, errors.New("invalid gym exercise")
	}
	if tempo := strings.TrimSpace(r.PostForm.Get("tempo")); tempo != "" {
		if utf8.RuneCountInString(tempo) > 30 {
			return exercise, errors.New("invalid tempo")
		}
		exercise.Tempo = &tempo
	}
	if raw := r.PostForm.Get("execution_intent"); raw != "" {
		intent := dbgen.GymExecutionIntent(raw)
		if !validGymExecutionIntent(intent) {
			return exercise, errors.New("invalid execution intent")
		}
		exercise.ExecutionIntent = &intent
	}
	if raw := r.PostForm.Get("resistance_kind"); raw != "" {
		kind := dbgen.GymResistanceKind(raw)
		if !validGymResistanceKind(kind) {
			return exercise, errors.New("invalid resistance kind")
		}
		exercise.ResistanceKind = &kind
		valueText := strings.TrimSpace(r.PostForm.Get("resistance_value"))
		instruction := strings.TrimSpace(r.PostForm.Get("resistance_text"))
		switch kind {
		case dbgen.GymResistanceKindKILOGRAMS, dbgen.GymResistanceKindPERCENT1RM, dbgen.GymResistanceKindRPE, dbgen.GymResistanceKindRIR:
			value, parseErr := strconv.ParseFloat(valueText, 64)
			if parseErr != nil || !validGymResistanceValue(kind, value) || instruction != "" {
				return exercise, errors.New("invalid resistance value")
			}
			exercise.ResistanceValue = &value
		case dbgen.GymResistanceKindBODYWEIGHT:
			if valueText != "" || instruction != "" {
				return exercise, errors.New("invalid body-weight resistance")
			}
		case dbgen.GymResistanceKindBAND, dbgen.GymResistanceKindCOACHINSTRUCTION:
			if valueText != "" || !validTrainingText(instruction, 1, 180) {
				return exercise, errors.New("invalid resistance instruction")
			}
			exercise.ResistanceText = &instruction
		}
	} else if strings.TrimSpace(r.PostForm.Get("resistance_value")) != "" || strings.TrimSpace(r.PostForm.Get("resistance_text")) != "" {
		return exercise, errors.New("resistance type required")
	}
	return exercise, nil
}

func validGymBlockStructure(value dbgen.GymBlockStructure) bool {
	return value == dbgen.GymBlockStructureSTRAIGHTSETS || value == dbgen.GymBlockStructureCIRCUIT || value == dbgen.GymBlockStructureSUPERSET
}

func validTrainingObjective(value dbgen.TrainingObjective) bool {
	switch value {
	case dbgen.TrainingObjectiveMOBILITY, dbgen.TrainingObjectiveACTIVATION, dbgen.TrainingObjectiveMAXSTRENGTHHYPERTROPHY, dbgen.TrainingObjectiveMAXSTRENGTHNEURAL, dbgen.TrainingObjectiveEXPLOSIVESTRENGTH, dbgen.TrainingObjectiveSTRENGTHENDURANCE, dbgen.TrainingObjectiveTECHNIQUE, dbgen.TrainingObjectiveCORE, dbgen.TrainingObjectiveCUSTOM:
		return true
	default:
		return false
	}
}

func validGymResistanceKind(value dbgen.GymResistanceKind) bool {
	switch value {
	case dbgen.GymResistanceKindKILOGRAMS, dbgen.GymResistanceKindPERCENT1RM, dbgen.GymResistanceKindBODYWEIGHT, dbgen.GymResistanceKindBAND, dbgen.GymResistanceKindRPE, dbgen.GymResistanceKindRIR, dbgen.GymResistanceKindCOACHINSTRUCTION:
		return true
	default:
		return false
	}
}

func validGymExecutionIntent(value dbgen.GymExecutionIntent) bool {
	return value == dbgen.GymExecutionIntentCONTROLLED || value == dbgen.GymExecutionIntentEXPLOSIVE || value == dbgen.GymExecutionIntentMAXIMUMVELOCITY || value == dbgen.GymExecutionIntentISOMETRIC || value == dbgen.GymExecutionIntentCUSTOM
}

func validGymResistanceValue(kind dbgen.GymResistanceKind, value float64) bool {
	switch kind {
	case dbgen.GymResistanceKindKILOGRAMS:
		return value >= 0.01 && value <= 10000
	case dbgen.GymResistanceKindPERCENT1RM:
		return value >= 0.01 && value <= 200
	case dbgen.GymResistanceKindRPE:
		return value >= 1 && value <= 10
	case dbgen.GymResistanceKindRIR:
		return value >= 0 && value <= 20
	default:
		return false
	}
}

func parseStructuredWaterForm(r *http.Request, parentID uuid.UUID, includeBlock bool) (dbgen.CreateTrainingSegmentBlockParams, dbgen.CreateWaterBlockPrescriptionParams, dbgen.CreateWaterWorkStepParams, error) {
	block := dbgen.CreateTrainingSegmentBlockParams{SegmentID: parentID}
	prescription := dbgen.CreateWaterBlockPrescriptionParams{}
	if includeBlock {
		block.Purpose = dbgen.TrainingBlockPurpose(r.PostForm.Get("purpose"))
		block.Title = strings.TrimSpace(r.PostForm.Get("title"))
		block.Instructions = strings.TrimSpace(r.PostForm.Get("instructions"))
		prescription.Method = dbgen.WaterWorkMethod(r.PostForm.Get("method"))
		profileID, err := optionalUUID(r.PostForm.Get("intensity_profile_id"))
		if err != nil {
			return block, prescription, dbgen.CreateWaterWorkStepParams{}, err
		}
		prescription.IntensityProfileID = profileID
		prescription.TargetDistanceMetres, err = optionalPositiveInt32(r.PostForm.Get("target_distance_metres"), 200000)
		if err != nil {
			return block, prescription, dbgen.CreateWaterWorkStepParams{}, err
		}
		if prescription.TargetDistanceMetres != nil {
			certainty := dbgen.TrainingMeasureCertainty(r.PostForm.Get("target_distance_certainty"))
			if !validTrainingMeasureCertainty(certainty) {
				return block, prescription, dbgen.CreateWaterWorkStepParams{}, errors.New("target distance certainty required")
			}
			prescription.TargetDistanceCertainty = &certainty
		} else if r.PostForm.Get("target_distance_certainty") != "" {
			return block, prescription, dbgen.CreateWaterWorkStepParams{}, errors.New("target distance required")
		}
		if !validTrainingBlockPurpose(block.Purpose) || !validWaterWorkMethod(prescription.Method) || utf8.RuneCountInString(block.Title) > 120 || !validTrainingText(block.Instructions, 2, 4000) {
			return block, prescription, dbgen.CreateWaterWorkStepParams{}, errors.New("invalid water block")
		}
	}
	step, err := parseWaterWorkStep(r)
	return block, prescription, step, err
}

func parseWaterWorkStep(r *http.Request) (dbgen.CreateWaterWorkStepParams, error) {
	step := dbgen.CreateWaterWorkStepParams{
		Kind:         dbgen.WaterStepKind(r.PostForm.Get("step_kind")),
		Name:         strings.TrimSpace(r.PostForm.Get("step_name")),
		Instructions: strings.TrimSpace(r.PostForm.Get("step_instructions")),
	}
	var err error
	if step.ParentStepID, err = optionalUUID(r.PostForm.Get("parent_step_id")); err != nil {
		return step, err
	}
	if step.Repeats, err = optionalPositiveInt32(r.PostForm.Get("repeats"), 100); err != nil {
		return step, err
	}
	if step.DurationSeconds, err = optionalPositiveInt32(r.PostForm.Get("duration_seconds"), 86400); err != nil {
		return step, err
	}
	if step.DistanceMetres, err = optionalPositiveInt32(r.PostForm.Get("distance_metres"), 200000); err != nil {
		return step, err
	}
	if step.RecoverySeconds, err = optionalPositiveInt32(r.PostForm.Get("recovery_seconds"), 86400); err != nil {
		return step, err
	}
	if step.CadenceSpm, err = optionalPositiveInt32(r.PostForm.Get("cadence_spm"), 300); err != nil {
		return step, err
	}
	if step.DurationCertainty, err = waterMeasureCertainty(r.PostForm.Get("duration_certainty"), step.DurationSeconds != nil); err != nil {
		return step, err
	}
	if step.DistanceCertainty, err = waterMeasureCertainty(r.PostForm.Get("distance_certainty"), step.DistanceMetres != nil); err != nil {
		return step, err
	}
	for raw, target := range map[string]**string{
		"intensity_code": &step.IntensityCode, "drill_focus": &step.DrillFocus,
		"drill_format": &step.DrillFormat, "role_notes": &step.RoleNotes,
	} {
		value := strings.TrimSpace(r.PostForm.Get(raw))
		if value != "" {
			*target = &value
		}
	}
	if !validTrainingText(step.Name, 2, 180) || utf8.RuneCountInString(step.Instructions) > 1000 || !optionalTextWithin(step.IntensityCode, 20) || !optionalTextWithin(step.DrillFocus, 180) || !optionalTextWithin(step.DrillFormat, 180) || !optionalTextWithin(step.RoleNotes, 500) {
		return step, errors.New("invalid water step text")
	}
	switch step.Kind {
	case dbgen.WaterStepKindREPEATGROUP:
		if step.Repeats == nil || step.DurationSeconds != nil || step.DistanceMetres != nil {
			return step, errors.New("invalid repeat group")
		}
	case dbgen.WaterStepKindEFFORT:
		if step.Repeats != nil || (step.DurationSeconds == nil && step.DistanceMetres == nil && !validTrainingText(step.Instructions, 2, 1000)) {
			return step, errors.New("invalid effort")
		}
	default:
		return step, errors.New("invalid water step kind")
	}
	return step, nil
}

func waterMeasureCertainty(raw string, present bool) (*dbgen.TrainingMeasureCertainty, error) {
	if !present {
		if raw != "" {
			return nil, errors.New("measure required")
		}
		return nil, nil
	}
	certainty := dbgen.TrainingMeasureCertainty(raw)
	if !validTrainingMeasureCertainty(certainty) {
		return nil, errors.New("measure certainty required")
	}
	return &certainty, nil
}

func optionalTextWithin(value *string, maximum int) bool {
	return value == nil || validTrainingText(*value, 1, maximum)
}

func validWaterWorkMethod(value dbgen.WaterWorkMethod) bool {
	switch value {
	case dbgen.WaterWorkMethodCONTINUOUS, dbgen.WaterWorkMethodINTERVALS, dbgen.WaterWorkMethodFARTLEK, dbgen.WaterWorkMethodTECHNIQUE, dbgen.WaterWorkMethodSTARTS, dbgen.WaterWorkMethodRACESIMULATION, dbgen.WaterWorkMethodTACTICALDRILL, dbgen.WaterWorkMethodCUSTOM:
		return true
	default:
		return false
	}
}

func validTrainingMeasureCertainty(value dbgen.TrainingMeasureCertainty) bool {
	return value == dbgen.TrainingMeasureCertaintyEXACT || value == dbgen.TrainingMeasureCertaintyESTIMATED
}

func validPaddlingCraft(value dbgen.PaddlingCraft) bool {
	return value == dbgen.PaddlingCraftKAYAK || value == dbgen.PaddlingCraftCANOE
}

func structuredChoices(rows []dbgen.ListEligibleTrainingGroupMembershipsRow) ([]pages.StructuredTrainingMembershipChoice, []pages.StructuredTrainingChoice, []pages.StructuredTrainingChoice) {
	members := make([]pages.StructuredTrainingMembershipChoice, 0, len(rows))
	programmes, teams := []pages.StructuredTrainingChoice{}, []pages.StructuredTrainingChoice{}
	seenProgrammes, seenTeams := map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
	for _, row := range rows {
		scope := row.ProgrammeName
		if row.TeamName != nil {
			scope += " · " + *row.TeamName
		}
		members = append(members, pages.StructuredTrainingMembershipChoice{ID: row.ID.String(), Athlete: row.AthleteName, Scope: scope})
		if !seenProgrammes[row.ProgrammeID] {
			programmes = append(programmes, pages.StructuredTrainingChoice{ID: row.ProgrammeID.String(), Name: row.ProgrammeName})
			seenProgrammes[row.ProgrammeID] = true
		}
		if row.TeamID != nil && !seenTeams[*row.TeamID] {
			teams = append(teams, pages.StructuredTrainingChoice{ID: row.TeamID.String(), Name: *row.TeamName})
			seenTeams[*row.TeamID] = true
		}
	}
	return members, programmes, teams
}

func structuredPlanChoices(audiences []pages.StructuredTrainingAudience) ([]pages.StructuredTrainingChoice, []pages.StructuredTrainingChoice, []pages.StructuredTrainingChoice, []pages.StructuredTrainingChoice) {
	groups, weeks, sessions, segments := []pages.StructuredTrainingChoice{}, []pages.StructuredTrainingChoice{}, []pages.StructuredTrainingChoice{}, []pages.StructuredTrainingChoice{}
	for _, audience := range audiences {
		groups = append(groups, pages.StructuredTrainingChoice{ID: audience.GroupID, Name: audience.GroupName})
		for _, week := range audience.Weeks {
			weeks = append(weeks, pages.StructuredTrainingChoice{ID: week.ID, Name: audience.GroupName + " · " + week.Title + " · " + week.DateRange, GroupID: audience.GroupID})
			for _, session := range week.Sessions {
				sessions = append(sessions, pages.StructuredTrainingChoice{ID: session.ID, Name: week.Title + " · " + session.When + " · " + session.Title})
				for _, segment := range session.Segments {
					name := structuredModalityName(segment.Modality)
					if segment.Title != "" {
						name += " · " + segment.Title
					}
					segments = append(segments, pages.StructuredTrainingChoice{ID: segment.ID, Name: week.Title + " · " + session.Title + " · " + name})
				}
			}
		}
	}
	return groups, weeks, sessions, segments
}

func structuredVariationMembers(rows []dbgen.ListManagedTrainingGroupMembersRow) []pages.StructuredVariationMemberChoice {
	result := make([]pages.StructuredVariationMemberChoice, 0, len(rows))
	for _, row := range rows {
		result = append(result, pages.StructuredVariationMemberChoice{ID: row.MembershipID.String(), Athlete: row.AthleteName, GroupID: row.TrainingGroupID.String(), GroupName: row.TrainingGroupName})
	}
	return result
}

func structuredCrewModalities(rows []dbgen.ListStructuredCrewModalitiesRow) []pages.StructuredTrainingChoice {
	result := make([]pages.StructuredTrainingChoice, 0, len(rows))
	for _, row := range rows {
		result = append(result, pages.StructuredTrainingChoice{ID: row.ID.String(), Name: row.Code + " · " + row.NamePt})
	}
	return result
}

func structuredCrewSize(rows []dbgen.ListStructuredCrewModalitiesRow, craftID *uuid.UUID) (int, bool) {
	if craftID == nil {
		return 0, false
	}
	for _, row := range rows {
		if row.ID != *craftID {
			continue
		}
		index := len(row.Code)
		for index > 0 && row.Code[index-1] >= '0' && row.Code[index-1] <= '9' {
			index--
		}
		size, err := strconv.Atoi(row.Code[index:])
		return size, err == nil && size >= 2
	}
	return 0, false
}

func structuredCompetitionEvents(rows []dbgen.ListManagedStructuredCompetitionEventsRow, location *time.Location) []pages.StructuredTrainingChoice {
	result := make([]pages.StructuredTrainingChoice, 0, len(rows))
	for _, row := range rows {
		result = append(result, pages.StructuredTrainingChoice{ID: row.ID.String(), Name: row.StartsAt.Time.In(location).Format("02/01/2006") + " · " + row.Title})
	}
	return result
}

func structuredVariationGroups(rows []dbgen.ListManagedTrainingVariationGroupsRow, _ *time.Location) []pages.StructuredVariationGroup {
	result := []pages.StructuredVariationGroup{}
	for _, row := range rows {
		if len(result) == 0 || result[len(result)-1].ID != row.ID.String() {
			period := "desde " + row.EffectiveFrom.Time.Format("02/01/2006")
			if row.EffectiveUntil.Valid {
				period += " até " + row.EffectiveUntil.Time.Format("02/01/2006")
			} else if row.CompetitionEventID != nil {
				period += " até à competição"
			} else if row.OpenEndedException {
				period += " · sem data final (exceção)"
			}
			kind := "Subgrupo"
			if row.Kind == dbgen.TrainingVariationGroupKindCREW {
				kind = "Tripulação"
			}
			result = append(result, pages.StructuredVariationGroup{ID: row.ID.String(), GroupID: row.TrainingGroupID.String(), GroupName: row.TrainingGroupName, Name: row.Name, Kind: kind, Craft: stringValue(row.CraftCode), Period: period, Competition: stringValue(row.CompetitionEventTitle)})
		}
		result[len(result)-1].Members = append(result[len(result)-1].Members, row.AthleteName)
	}
	return result
}

func structuredVariationTargets(members []dbgen.ListManagedTrainingGroupMembersRow, groups []dbgen.ListManagedTrainingVariationGroupsRow) []pages.StructuredVariationTargetChoice {
	result := make([]pages.StructuredVariationTargetChoice, 0, len(members)+len(groups))
	for _, row := range members {
		result = append(result, pages.StructuredVariationTargetChoice{Value: "ATHLETE:" + row.MembershipID.String(), Name: row.AthleteName + " · " + row.TrainingGroupName, GroupID: row.TrainingGroupID.String()})
	}
	seen := map[uuid.UUID]bool{}
	for _, row := range groups {
		if seen[row.ID] {
			continue
		}
		seen[row.ID] = true
		kind := "Subgrupo"
		if row.Kind == dbgen.TrainingVariationGroupKindCREW {
			kind = "Tripulação"
		}
		result = append(result, pages.StructuredVariationTargetChoice{Value: "GROUP:" + row.ID.String(), Name: kind + " · " + row.Name + " · " + row.TrainingGroupName, GroupID: row.TrainingGroupID.String()})
	}
	return result
}

func structuredVariationSubjects(audiences []pages.StructuredTrainingAudience) []pages.StructuredVariationSubjectChoice {
	result := []pages.StructuredVariationSubjectChoice{}
	seen := map[string]bool{}
	for _, audience := range audiences {
		for _, week := range audience.Weeks {
			for _, session := range week.Sessions {
				prefix := week.Title + " · " + session.When + " · " + session.Title
				for _, segment := range session.Segments {
					segmentName := structuredModalityName(segment.Modality)
					if segment.Title != "" {
						segmentName += " · " + segment.Title
					}
					appendVariationSubject(&result, seen, "SEGMENT:"+segment.ID, prefix+" · Segmento "+strconv.Itoa(segment.Position)+" · "+segmentName, week.ID)
					for _, block := range segment.Blocks {
						blockName := block.Title
						if blockName == "" {
							blockName = block.Purpose
						}
						appendVariationSubject(&result, seen, "BLOCK:"+block.ID, prefix+" · "+segmentName+" · Bloco "+strconv.Itoa(block.Position)+" · "+blockName, week.ID)
						for _, step := range block.WaterSteps {
							appendVariationSubject(&result, seen, "WATER_STEP:"+step.ID, prefix+" · Passo de água "+strconv.Itoa(step.Position)+" · "+step.Name, week.ID)
						}
						for _, exercise := range block.Exercises {
							appendVariationSubject(&result, seen, "GYM_EXERCISE:"+exercise.ID, prefix+" · Exercício "+strconv.Itoa(exercise.Position)+" · "+exercise.Name, week.ID)
						}
					}
				}
			}
		}
	}
	return result
}

func appendVariationSubject(result *[]pages.StructuredVariationSubjectChoice, seen map[string]bool, value, name, planID string) {
	if value == "" || seen[value] {
		return
	}
	seen[value] = true
	*result = append(*result, pages.StructuredVariationSubjectChoice{Value: value, Name: name, PlanID: planID})
}

func structuredVariationPreviews(rows []dbgen.ListTrainingVariationMatchesForManagerRow, location *time.Location) []pages.StructuredVariationPreview {
	result := []pages.StructuredVariationPreview{}
	for index := 0; index < len(rows); {
		row := rows[index]
		if row.MembershipID == nil {
			index++
			continue
		}
		end := index + 1
		for end < len(rows) && rows[end].MembershipID != nil && *rows[end].MembershipID == *row.MembershipID && rows[end].PlanID == row.PlanID && rows[end].SubjectKind == row.SubjectKind && rows[end].SubjectID == row.SubjectID {
			end++
		}
		highest := row.Priority
		preview := pages.StructuredVariationPreview{Athlete: row.AthleteName, Plan: row.PlanTitle, Session: row.SessionTitle, When: row.StartsAt.Time.In(location).Format("02/01/2006 15:04"), Subject: row.SubjectLabel}
		for _, candidate := range rows[index:end] {
			applied := candidate.Priority == highest
			preview.Rules = append(preview.Rules, pages.StructuredVariationRule{ID: candidate.VariationID.String(), Target: candidate.TargetKind + " · " + candidate.TargetName, Operation: trainingVariationOperationLabel(candidate.Operation), Summary: candidate.ChangeSummary, Patch: trainingVariationPatchLabel(candidate.Patch), Version: int(candidate.Version), Applied: applied})
		}
		appliedCount := 0
		for _, rule := range preview.Rules {
			if rule.Applied {
				appliedCount++
			}
		}
		preview.Conflict = appliedCount > 1
		if preview.Conflict {
			preview.Status = "Conflito: resolva as variações do mesmo nível antes de publicar."
		} else {
			preview.Status = "Resolvida com precedência explícita."
		}
		result = append(result, preview)
		index = end
	}
	return result
}

func trainingVariationOperationLabel(value dbgen.TrainingVariationOperation) string {
	switch value {
	case dbgen.TrainingVariationOperationOMIT:
		return "Omitir"
	case dbgen.TrainingVariationOperationREPLACE:
		return "Substituir"
	case dbgen.TrainingVariationOperationADD:
		return "Adicionar alternativa"
	default:
		return "Alterar campos"
	}
}

func trainingVariationPatchLabel(encoded []byte) string {
	values := map[string]any{}
	if len(encoded) == 0 || json.Unmarshal(encoded, &values) != nil {
		return ""
	}
	labels := map[string]string{"title": "título", "modality": "modalidade", "instructions": "instruções", "repeats": "repetições", "duration_seconds": "duração (s)", "distance_metres": "distância (m)", "recovery_seconds": "recuperação (s)", "intensity_code": "intensidade", "sets": "séries", "repetitions": "repetições do exercício", "resistance": "carga"}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s: %v", labels[key], values[key]))
	}
	return strings.Join(parts, " · ")
}

func (h StructuredTraining) structuredPublicationStates(ctx context.Context, audiences []pages.StructuredTrainingAudience, rows []dbgen.ListManagedTrainingPublicationStatesRow, matches []dbgen.ListTrainingVariationMatchesForManagerRow) []pages.StructuredPublicationState {
	result := make([]pages.StructuredPublicationState, 0, len(rows))
	for _, row := range rows {
		recipients, err := h.Store.ListStructuredTrainingPublicationMembers(ctx, dbgen.ListStructuredTrainingPublicationMembersParams{PlanID: row.ID, TimeZone: h.location().String()})
		if err != nil {
			continue
		}
		athletes := map[uuid.UUID]bool{}
		for _, recipient := range recipients {
			athletes[recipient.AthleteUserID] = true
		}
		status := "Rascunho nunca publicado"
		if row.PublishedRevision > 0 {
			status = "Rascunho alterado desde a última publicação"
			if row.PublicationCurrent != nil && *row.PublicationCurrent {
				status = "Publicado · sem alterações pendentes"
			}
		}
		publishedAt := ""
		if row.PublishedAt.Valid {
			publishedAt = row.PublishedAt.Time.In(h.location()).Format("02/01/2006 15:04")
		}
		added, changed, removed, unchanged := 0, 0, 0, 0
		if audience, week, found := structuredPublicationWeek(audiences, row.ID); found && structuredPublicationConflictCount(matches, row.ID) == 0 {
			candidates, candidateErr := buildStructuredPrescriptionInputs(row.ID, audience, week, recipients, matches)
			previous, previousErr := h.Store.ListLatestTrainingPrescriptionHashesForPlan(ctx, row.ID)
			if candidateErr == nil && previousErr == nil {
				previousHashes := map[string]string{}
				for _, prescription := range previous {
					previousHashes[prescription.MembershipID.String()+":"+prescription.SessionID.String()] = prescription.SnapshotSha256
				}
				for _, prescription := range candidates {
					key := prescription.MembershipID.String() + ":" + prescription.SessionID.String()
					if previousHash, exists := previousHashes[key]; !exists {
						added++
					} else if previousHash == prescription.SnapshotSHA256 {
						unchanged++
						delete(previousHashes, key)
					} else {
						changed++
						delete(previousHashes, key)
					}
				}
				removed = len(previousHashes)
			}
		}
		result = append(result, pages.StructuredPublicationState{
			PlanID: row.ID.String(), Plan: row.Title, SourceUpdatedAt: row.SourceUpdatedAt.Time.Format(time.RFC3339Nano),
			Status: status, PublishedAt: publishedAt, PublishedBy: stringValue(row.PublishedByName), Revision: int(row.PublishedRevision),
			AthleteCount: len(athletes), PrescriptionCount: len(recipients), ConflictCount: structuredPublicationConflictCount(matches, row.ID),
			AddedCount: added, ChangedCount: changed, RemovedCount: removed, UnchangedCount: unchanged,
		})
	}
	return result
}

func structuredPublicationConflictCount(rows []dbgen.ListTrainingVariationMatchesForManagerRow, planID uuid.UUID) int {
	counts := map[string]int{}
	highest := map[string]int32{}
	for _, row := range rows {
		if row.PlanID != planID || row.MembershipID == nil {
			continue
		}
		key := row.MembershipID.String() + ":" + string(row.SubjectKind) + ":" + row.SubjectID.String()
		if priority, ok := highest[key]; !ok || row.Priority > priority {
			highest[key], counts[key] = row.Priority, 1
		} else if row.Priority == priority {
			counts[key]++
		}
	}
	conflicts := 0
	for _, count := range counts {
		if count > 1 {
			conflicts++
		}
	}
	return conflicts
}

func structuredPublicationWeek(audiences []pages.StructuredTrainingAudience, planID uuid.UUID) (pages.StructuredTrainingAudience, pages.StructuredTrainingWeek, bool) {
	for _, audience := range audiences {
		for _, week := range audience.Weeks {
			if week.ID == planID.String() {
				return audience, week, true
			}
		}
	}
	return pages.StructuredTrainingAudience{}, pages.StructuredTrainingWeek{}, false
}

func buildStructuredPrescriptionInputs(planID uuid.UUID, audience pages.StructuredTrainingAudience, week pages.StructuredTrainingWeek, recipients []dbgen.ListStructuredTrainingPublicationMembersRow, matches []dbgen.ListTrainingVariationMatchesForManagerRow) ([]StructuredPrescriptionInput, error) {
	sessions := map[uuid.UUID]pages.StructuredTrainingSession{}
	for _, session := range week.Sessions {
		id, err := uuid.Parse(session.ID)
		if err == nil {
			sessions[id] = session
		}
	}
	result := make([]StructuredPrescriptionInput, 0, len(recipients))
	for _, recipient := range recipients {
		session, ok := sessions[recipient.SessionID]
		if !ok {
			continue
		}
		encoded, err := json.Marshal(session)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(encoded, &session); err != nil {
			return nil, err
		}
		if err = resolveStructuredPrescription(&session, matches, planID, recipient.SessionID, recipient.MembershipID); err != nil {
			return nil, err
		}
		snapshot := structuredPrescriptionSnapshot{SchemaVersion: structuredPrescriptionSchemaVersion, PlanID: planID.String(), PlanTitle: week.Title, GroupName: audience.GroupName, WeekTitle: week.Title, WeekDescription: week.Description, Season: week.Season, DateRange: week.DateRange, PlannedLoad: week.PlannedLoad, Session: session}
		encoded, err = json.Marshal(snapshot)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(encoded)
		result = append(result, StructuredPrescriptionInput{SessionID: recipient.SessionID, MembershipID: recipient.MembershipID, AthleteUserID: recipient.AthleteUserID, Snapshot: encoded, SnapshotSHA256: fmt.Sprintf("%x", digest)})
	}
	return result, nil
}

func (h StructuredTraining) buildStructuredPublication(ctx context.Context, user CurrentUser, planID uuid.UUID, expected time.Time, summary string) (StructuredPublicationInput, error) {
	states, err := h.Store.ListManagedTrainingPublicationStates(ctx, dbgen.ListManagedTrainingPublicationStatesParams{IsAdmin: user.IsAdmin, UserID: user.ID})
	if err != nil {
		return StructuredPublicationInput{}, err
	}
	var sourceUpdatedAt pgtype.Timestamptz
	for _, state := range states {
		if state.ID == planID {
			sourceUpdatedAt = state.SourceUpdatedAt
			break
		}
	}
	if !sourceUpdatedAt.Valid {
		return StructuredPublicationInput{}, pgx.ErrNoRows
	}
	if !sourceUpdatedAt.Time.Equal(expected) {
		return StructuredPublicationInput{}, errStructuredTrainingPublicationConflict
	}
	rows, err := h.Store.ListStructuredTrainingOverviewForManager(ctx, dbgen.ListStructuredTrainingOverviewForManagerParams{IsAdmin: user.IsAdmin, UserID: user.ID})
	if err != nil {
		return StructuredPublicationInput{}, err
	}
	audience, week, found := structuredPublicationWeek(assembleStructuredTraining(managerStructuredRows(rows), h.location()), planID)
	if !found {
		return StructuredPublicationInput{}, pgx.ErrNoRows
	}
	recipients, err := h.Store.ListStructuredTrainingPublicationMembers(ctx, dbgen.ListStructuredTrainingPublicationMembersParams{PlanID: planID, TimeZone: h.location().String()})
	if err != nil {
		return StructuredPublicationInput{}, err
	}
	matches, err := h.Store.ListTrainingVariationMatchesForManager(ctx, dbgen.ListTrainingVariationMatchesForManagerParams{TimeZone: h.location().String(), IsAdmin: user.IsAdmin, UserID: user.ID})
	if err != nil {
		return StructuredPublicationInput{}, err
	}
	if structuredPublicationConflictCount(matches, planID) > 0 {
		return StructuredPublicationInput{}, errStructuredTrainingPublicationVariationConflict
	}
	prescriptions, err := buildStructuredPrescriptionInputs(planID, audience, week, recipients, matches)
	if err != nil {
		return StructuredPublicationInput{}, err
	}
	input := StructuredPublicationInput{PlanID: planID, SourceUpdatedAt: sourceUpdatedAt, ChangeSummary: summary, PublishedByID: user.ID, Prescriptions: prescriptions}
	if len(input.Prescriptions) == 0 {
		previous, previousErr := h.Store.ListLatestTrainingPrescriptionHashesForPlan(ctx, planID)
		if previousErr != nil {
			return StructuredPublicationInput{}, previousErr
		}
		if len(previous) == 0 {
			return StructuredPublicationInput{}, pgx.ErrNoRows
		}
	}
	return input, nil
}

func resolveStructuredPrescription(session *pages.StructuredTrainingSession, rows []dbgen.ListTrainingVariationMatchesForManagerRow, planID, sessionID, membershipID uuid.UUID) error {
	selected := map[string]dbgen.ListTrainingVariationMatchesForManagerRow{}
	for _, row := range rows {
		if row.PlanID != planID || row.SessionID != sessionID || row.MembershipID == nil || *row.MembershipID != membershipID {
			continue
		}
		key := string(row.SubjectKind) + ":" + row.SubjectID.String()
		current, ok := selected[key]
		if !ok || row.Priority > current.Priority {
			selected[key] = row
		} else if row.Priority == current.Priority {
			return errStructuredTrainingPublicationVariationConflict
		}
	}
	keys := make([]string, 0, len(selected))
	for key := range selected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := applyStructuredPrescriptionVariation(session, selected[key]); err != nil {
			return err
		}
	}
	return nil
}

func applyStructuredPrescriptionVariation(session *pages.StructuredTrainingSession, row dbgen.ListTrainingVariationMatchesForManagerRow) error {
	change := pages.StructuredPrescriptionChange{Subject: row.SubjectLabel, Operation: trainingVariationOperationLabel(row.Operation), Summary: row.ChangeSummary, Fields: trainingVariationPatchLabel(row.Patch)}
	patch := map[string]any{}
	if len(row.Patch) > 0 && json.Unmarshal(row.Patch, &patch) != nil {
		return errors.New("invalid stored training variation patch")
	}
	subjectID := row.SubjectID.String()
	switch row.SubjectKind {
	case dbgen.TrainingVariationSubjectKindSEGMENT:
		for index := range session.Segments {
			if session.Segments[index].ID != subjectID {
				continue
			}
			if row.Operation == dbgen.TrainingVariationOperationOMIT {
				session.Segments = append(session.Segments[:index], session.Segments[index+1:]...)
			} else if row.Operation != dbgen.TrainingVariationOperationADD {
				applyPrescriptionTextPatch(patch, &session.Segments[index].Title, &session.Segments[index].Modality, nil)
			}
			break
		}
	case dbgen.TrainingVariationSubjectKindBLOCK:
		for segmentIndex := range session.Segments {
			for blockIndex := range session.Segments[segmentIndex].Blocks {
				block := &session.Segments[segmentIndex].Blocks[blockIndex]
				if block.ID != subjectID {
					continue
				}
				if row.Operation == dbgen.TrainingVariationOperationOMIT {
					session.Segments[segmentIndex].Blocks = append(session.Segments[segmentIndex].Blocks[:blockIndex], session.Segments[segmentIndex].Blocks[blockIndex+1:]...)
				} else if row.Operation != dbgen.TrainingVariationOperationADD {
					applyPrescriptionTextPatch(patch, &block.Title, nil, &block.Instructions)
				}
				break
			}
		}
	case dbgen.TrainingVariationSubjectKindWATERSTEP:
		for segmentIndex := range session.Segments {
			for blockIndex := range session.Segments[segmentIndex].Blocks {
				steps := &session.Segments[segmentIndex].Blocks[blockIndex].WaterSteps
				for stepIndex := range *steps {
					step := &(*steps)[stepIndex]
					if step.ID != subjectID {
						continue
					}
					if row.Operation == dbgen.TrainingVariationOperationOMIT {
						*steps = append((*steps)[:stepIndex], (*steps)[stepIndex+1:]...)
					} else if row.Operation != dbgen.TrainingVariationOperationADD {
						applyPrescriptionTextPatch(patch, &step.Name, nil, &step.Instructions)
						applyPrescriptionIntPatch(patch, "repeats", &step.Repeats)
						applyPrescriptionIntPatch(patch, "duration_seconds", &step.DurationSeconds)
						applyPrescriptionIntPatch(patch, "distance_metres", &step.DistanceMetres)
						applyPrescriptionIntPatch(patch, "recovery_seconds", &step.Recovery)
						if value, ok := patch["intensity_code"].(string); ok {
							step.Intensity = value
						}
						step.Prescription = structuredPrescriptionWaterStep(*step)
					}
					break
				}
			}
		}
	case dbgen.TrainingVariationSubjectKindGYMEXERCISE:
		for segmentIndex := range session.Segments {
			for blockIndex := range session.Segments[segmentIndex].Blocks {
				exercises := &session.Segments[segmentIndex].Blocks[blockIndex].Exercises
				for exerciseIndex := range *exercises {
					exercise := &(*exercises)[exerciseIndex]
					if exercise.ID != subjectID {
						continue
					}
					if row.Operation == dbgen.TrainingVariationOperationOMIT {
						*exercises = append((*exercises)[:exerciseIndex], (*exercises)[exerciseIndex+1:]...)
					} else if row.Operation != dbgen.TrainingVariationOperationADD {
						applyPrescriptionTextPatch(patch, &exercise.Name, nil, &exercise.Notes)
						if value, ok := patch["resistance"].(string); ok {
							exercise.Resistance = value
						}
						applyPrescriptionIntPatch(patch, "sets", &exercise.Sets)
						applyPrescriptionIntPatch(patch, "repetitions", &exercise.Repetitions)
						applyPrescriptionIntPatch(patch, "duration_seconds", &exercise.DurationSeconds)
						applyPrescriptionIntPatch(patch, "distance_metres", &exercise.DistanceMetres)
						applyPrescriptionIntPatch(patch, "recovery_seconds", &exercise.RecoverySeconds)
						exercise.Prescription = gymExercisePrescription(exercise.Sets, exercise.Repetitions, exercise.DurationSeconds, exercise.DistanceMetres, exercise.RecoverySeconds)
					}
					break
				}
			}
		}
	}
	session.Changes = append(session.Changes, change)
	session.Modalities = nil
	for _, segment := range session.Segments {
		if !slicesContains(session.Modalities, segment.Modality) {
			session.Modalities = append(session.Modalities, segment.Modality)
		}
	}
	return nil
}

func applyPrescriptionTextPatch(patch map[string]any, title, modality, instructions *string) {
	if title != nil {
		if value, ok := patch["title"].(string); ok {
			*title = value
		}
	}
	if modality != nil {
		if value, ok := patch["modality"].(string); ok {
			*modality = value
		}
	}
	if instructions != nil {
		if value, ok := patch["instructions"].(string); ok {
			*instructions = value
		}
	}
}

func applyPrescriptionIntPatch(patch map[string]any, key string, target *int) {
	if value, ok := patch[key].(float64); ok {
		*target = int(value)
	}
}

func structuredPrescriptionWaterStep(step pages.StructuredWaterStep) string {
	parts := []string{}
	if step.DurationSeconds > 0 {
		parts = append(parts, fmt.Sprintf("%d s (%s)", step.DurationSeconds, trainingMeasureCertaintyLabel(step.DurationCertainty)))
	}
	if step.DistanceMetres > 0 {
		parts = append(parts, fmt.Sprintf("%s (%s)", formatKilometres(int64(step.DistanceMetres)), trainingMeasureCertaintyLabel(step.DistanceCertainty)))
	}
	return strings.Join(parts, " · ")
}

func structuredPublishedTraining(rows []dbgen.ListTrainingPrescriptionsForViewerRow, location *time.Location) []pages.StructuredTrainingAudience {
	result := []pages.StructuredTrainingAudience{}
	for _, row := range rows {
		var snapshot structuredPrescriptionSnapshot
		if json.Unmarshal(row.Snapshot, &snapshot) != nil || snapshot.SchemaVersion != structuredPrescriptionSchemaVersion {
			continue
		}
		snapshot.Session.PrescriptionID = row.ID.String()
		snapshot.Session.Outcome, _ = row.OutcomeStatus.(string)
		if row.DistanceMetres != nil {
			snapshot.Session.ActualDistanceMetres = int(*row.DistanceMetres)
			snapshot.Session.ActualDistance = formatKilometres(int64(*row.DistanceMetres))
		}
		if row.ActualDurationMinutes != nil {
			snapshot.Session.ActualDurationMinutes = int(*row.ActualDurationMinutes)
			snapshot.Session.ActualDuration = fmt.Sprintf("%d min", *row.ActualDurationMinutes)
		}
		if row.PerceivedExertion != nil {
			snapshot.Session.PerceivedEffort = fmt.Sprintf("%d/10", *row.PerceivedExertion)
		}
		if row.RecoveryFeeling != nil {
			snapshot.Session.RecoveryFeeling = trainingFeelingText(*row.RecoveryFeeling)
		}
		snapshot.Session.PerceptionNote = stringValue(row.PerceptionNote)
		if row.OutcomeUpdatedAt.Valid {
			snapshot.Session.FeedbackUpdatedAt = row.OutcomeUpdatedAt.Time.In(location).Format("02/01/2006 15:04")
		}
		scope := fmt.Sprintf("Prescrição publicada · revisão %d · %s por %s", row.Revision, row.PublishedAt.Time.In(location).Format("02/01/2006 15:04"), row.PublishedByName)
		if !row.IsCurrent {
			scope = "Histórico · " + scope
		}
		audienceIndex := -1
		for index := range result {
			if result[index].AthleteName == row.AthleteName && result[index].Scope == scope {
				audienceIndex = index
				break
			}
		}
		if audienceIndex < 0 {
			result = append(result, pages.StructuredTrainingAudience{AthleteName: row.AthleteName, GroupName: snapshot.GroupName, Scope: scope})
			audienceIndex = len(result) - 1
		}
		weekIndex := -1
		for index := range result[audienceIndex].Weeks {
			if result[audienceIndex].Weeks[index].ID == snapshot.PlanID {
				weekIndex = index
				break
			}
		}
		if weekIndex < 0 {
			result[audienceIndex].Weeks = append(result[audienceIndex].Weeks, pages.StructuredTrainingWeek{ID: snapshot.PlanID, Title: snapshot.WeekTitle, Description: snapshot.WeekDescription, Season: snapshot.Season, DateRange: snapshot.DateRange, PlannedLoad: snapshot.PlannedLoad})
			weekIndex = len(result[audienceIndex].Weeks) - 1
		}
		result[audienceIndex].Weeks[weekIndex].Sessions = append(result[audienceIndex].Weeks[weekIndex].Sessions, snapshot.Session)
	}
	for audienceIndex := range result {
		for weekIndex := range result[audienceIndex].Weeks {
			result[audienceIndex].Weeks[weekIndex].Summary = calculateStructuredWeekSummary(result[audienceIndex].Weeks[weekIndex].Sessions)
		}
	}
	return result
}

func slicesContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func assembleStructuredTraining(rows []structuredTrainingRow, location *time.Location) []pages.StructuredTrainingAudience {
	audiences := []pages.StructuredTrainingAudience{}
	for _, row := range rows {
		if len(audiences) == 0 || audiences[len(audiences)-1].GroupID != row.groupID.String() || audiences[len(audiences)-1].AthleteName != row.athleteName {
			audiences = append(audiences, pages.StructuredTrainingAudience{AthleteName: row.athleteName, GroupID: row.groupID.String(), GroupName: row.groupName, Scope: row.scope, MemberCount: row.memberCount})
		}
		audience := &audiences[len(audiences)-1]
		if row.planID == nil {
			continue
		}
		if len(audience.Weeks) == 0 || audience.Weeks[len(audience.Weeks)-1].ID != row.planID.String() {
			plannedLoad := ""
			if row.plannedLoadPercentage != nil {
				plannedLoad = fmt.Sprintf("%d%%", *row.plannedLoadPercentage)
			}
			audience.Weeks = append(audience.Weeks, pages.StructuredTrainingWeek{ID: row.planID.String(), Title: row.planTitle, Description: row.planDescription, Season: row.seasonName, DateRange: fmt.Sprintf("%s–%s", row.weekStart.In(location).Format("02/01/2006"), row.weekStart.AddDate(0, 0, 6).In(location).Format("02/01/2006")), PlannedLoad: plannedLoad})
		}
		week := &audience.Weeks[len(audience.Weeks)-1]
		if row.sessionID == nil {
			continue
		}
		if len(week.Sessions) == 0 || week.Sessions[len(week.Sessions)-1].ID != row.sessionID.String() {
			when := row.startsAt.In(location).Format("02/01/2006 15:04") + "–" + row.endsAt.In(location).Format("15:04")
			week.Sessions = append(week.Sessions, pages.StructuredTrainingSession{ID: row.sessionID.String(), Title: row.sessionTitle, Description: row.sessionDescription, When: when, EntryKind: row.entryKind})
		}
		session := &week.Sessions[len(week.Sessions)-1]
		if row.segmentID == nil {
			continue
		}
		if len(session.Segments) == 0 || session.Segments[len(session.Segments)-1].ID != row.segmentID.String() {
			duration := ""
			if row.duration > 0 {
				duration = fmt.Sprintf("%d min", row.duration)
			}
			plannedStart := ""
			if row.startOffset > 0 {
				plannedStart = row.startsAt.Add(time.Duration(row.startOffset) * time.Minute).In(location).Format("15:04")
			} else if row.plannedStartSet {
				plannedStart = row.startsAt.In(location).Format("15:04")
			}
			session.Segments = append(session.Segments, pages.StructuredTrainingSegment{ID: row.segmentID.String(), Modality: row.modality, Title: row.segmentTitle, Location: row.segmentLocation, Duration: duration, PlannedStart: plannedStart, Transition: row.transition, EquipmentNotes: row.equipmentNotes, Position: row.segmentPosition})
			session.Modalities = appendStructuredModality(session.Modalities, row.modality)
		}
		segment := &session.Segments[len(session.Segments)-1]
		if row.blockID != nil {
			if len(segment.Blocks) == 0 || segment.Blocks[len(segment.Blocks)-1].ID != row.blockID.String() {
				segment.Blocks = append(segment.Blocks, pages.StructuredTrainingBlock{ID: row.blockID.String(), Purpose: row.blockPurpose, Title: row.blockTitle, Instructions: row.instructions, Position: row.blockPosition, GymStructure: row.gymStructure, GymObjective: row.gymObjective, GymRounds: row.gymRounds, GymRoundRecovery: row.gymRoundRecovery, WaterMethod: row.waterMethod, WaterTarget: row.waterTarget, WaterTargetDistanceMetres: row.waterTargetDistance, WaterTargetCertainty: row.waterTargetCertainty, WaterProfile: row.waterProfile})
			}
			block := &segment.Blocks[len(segment.Blocks)-1]
			if row.exerciseID != nil {
				block.Exercises = append(block.Exercises, pages.StructuredGymExercise{ID: row.exerciseID.String(), Name: row.exerciseName, Prescription: row.exercisePrescription, Resistance: row.exerciseResistance, Intent: row.exerciseIntent, Tempo: row.exerciseTempo, Notes: row.exerciseNotes, Position: row.exercisePosition, Sets: row.exerciseSets, Repetitions: row.exerciseRepetitions, DurationSeconds: row.exerciseDuration, DistanceMetres: row.exerciseDistance, RecoverySeconds: row.exerciseRecovery})
			}
			if row.waterStepID != nil {
				parentID := ""
				if row.waterParentStepID != nil {
					parentID = row.waterParentStepID.String()
				}
				block.WaterSteps = append(block.WaterSteps, pages.StructuredWaterStep{ID: row.waterStepID.String(), ParentID: parentID, Kind: row.waterStepKind, Name: row.waterStepName, Prescription: row.waterStepPrescription, Intensity: row.waterStepIntensity, Drill: row.waterStepDrill, Instructions: row.waterStepNotes, Position: row.waterStepPosition, Repeats: row.waterStepRepeats, Recovery: row.waterStepRecovery, DurationSeconds: row.waterStepDuration, DistanceMetres: row.waterStepDistance, DurationCertainty: row.waterStepDurationCertainty, DistanceCertainty: row.waterStepDistanceCertainty})
			}
		}
	}
	for audienceIndex := range audiences {
		for weekIndex := range audiences[audienceIndex].Weeks {
			for sessionIndex := range audiences[audienceIndex].Weeks[weekIndex].Sessions {
				for segmentIndex := range audiences[audienceIndex].Weeks[weekIndex].Sessions[sessionIndex].Segments {
					for blockIndex := range audiences[audienceIndex].Weeks[weekIndex].Sessions[sessionIndex].Segments[segmentIndex].Blocks {
						block := &audiences[audienceIndex].Weeks[weekIndex].Sessions[sessionIndex].Segments[segmentIndex].Blocks[blockIndex]
						block.WaterTotals = calculateWaterTotals(block.WaterSteps)
					}
				}
			}
			audiences[audienceIndex].Weeks[weekIndex].Summary = calculateStructuredWeekSummary(audiences[audienceIndex].Weeks[weekIndex].Sessions)
		}
	}
	return audiences
}

func appendStructuredModality(modalities []string, modality string) []string {
	for _, existing := range modalities {
		if existing == modality {
			return modalities
		}
	}
	return append(modalities, modality)
}

type waterMeasureTotal struct {
	value     int64
	hasValue  bool
	estimated bool
	unknown   bool
}

func calculateWaterTotals(steps []pages.StructuredWaterStep) []pages.StructuredWaterTotal {
	if len(steps) == 0 {
		return nil
	}
	children := map[string][]pages.StructuredWaterStep{}
	for _, step := range steps {
		children[step.ParentID] = append(children[step.ParentID], step)
	}
	duration, distance, recovery := waterMeasureTotal{}, waterMeasureTotal{}, waterMeasureTotal{hasValue: true}
	byIntensity := map[string]*waterMeasureTotal{}
	var walk func(string, int64, map[string]bool)
	walk = func(parentID string, multiplier int64, ancestors map[string]bool) {
		for _, step := range children[parentID] {
			if ancestors[step.ID] {
				duration.unknown, distance.unknown, recovery.unknown = true, true, true
				continue
			}
			if step.Kind == "REPEAT_GROUP" {
				repeats := int64(step.Repeats)
				if repeats < 1 || multiplier > (1<<63-1)/repeats {
					duration.unknown, distance.unknown, recovery.unknown = true, true, true
					continue
				}
				next := make(map[string]bool, len(ancestors)+1)
				for id := range ancestors {
					next[id] = true
				}
				next[step.ID] = true
				walk(step.ID, multiplier*repeats, next)
				if step.Recovery > 0 && repeats > 1 {
					recovery.value += int64(step.Recovery) * (repeats - 1) * multiplier
				}
				continue
			}
			if step.DurationSeconds > 0 {
				duration.value += int64(step.DurationSeconds) * multiplier
				duration.hasValue = true
				duration.estimated = duration.estimated || step.DurationCertainty == "ESTIMATED"
			} else {
				duration.unknown = true
			}
			if step.DistanceMetres > 0 {
				distance.value += int64(step.DistanceMetres) * multiplier
				distance.hasValue = true
				distance.estimated = distance.estimated || step.DistanceCertainty == "ESTIMATED"
			} else {
				distance.unknown = true
			}
			if step.Recovery > 0 {
				recovery.value += int64(step.Recovery) * multiplier
			}
			if step.Intensity != "" {
				code := strings.TrimSpace(strings.Split(step.Intensity, " · ")[0])
				total := byIntensity[code]
				if total == nil {
					total = &waterMeasureTotal{}
					byIntensity[code] = total
				}
				if step.DurationSeconds > 0 {
					total.value += int64(step.DurationSeconds) * multiplier
					total.hasValue = true
					total.estimated = total.estimated || step.DurationCertainty == "ESTIMATED"
				} else {
					total.unknown = true
				}
			}
		}
	}
	walk("", 1, map[string]bool{})
	totals := []pages.StructuredWaterTotal{
		waterTotalView("Esforço planeado", duration, formatTrainingDuration),
		waterTotalView("Recuperação planeada", recovery, formatTrainingDuration),
		waterTotalView("Distância planeada", distance, func(value int64) string { return formatKilometres(value) }),
	}
	codes := make([]string, 0, len(byIntensity))
	for code := range byIntensity {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		totals = append(totals, waterTotalView("Tempo em "+code, *byIntensity[code], formatTrainingDuration))
	}
	return totals
}

func waterTotalView(label string, total waterMeasureTotal, format func(int64) string) pages.StructuredWaterTotal {
	if total.unknown || !total.hasValue {
		return pages.StructuredWaterTotal{Label: label, Value: "Desconhecido", Certainty: "faltam dados; não foi inferido"}
	}
	certainty := "exato"
	if total.estimated {
		certainty = "estimado"
	}
	return pages.StructuredWaterTotal{Label: label, Value: format(total.value), Certainty: certainty}
}

func calculateStructuredWeekSummary(sessions []pages.StructuredTrainingSession) pages.StructuredTrainingWeekSummary {
	waterSteps := []pages.StructuredWaterStep{}
	supporting := map[string]int{}
	waterMethods := map[string]int{}
	waterTargets := []pages.StructuredTrainingSummaryItem{}
	actualDuration, actualDistance := int64(0), int64(0)
	durationCount, distanceCount := 0, 0
	for _, session := range sessions {
		if session.EntryKind == "REST" || session.EntryKind == "LOGISTICS" {
			continue
		}
		if session.ActualDurationMinutes > 0 {
			actualDuration += int64(session.ActualDurationMinutes)
			durationCount++
		}
		if session.ActualDistanceMetres > 0 {
			actualDistance += int64(session.ActualDistanceMetres)
			distanceCount++
		}
		for _, segment := range session.Segments {
			if segment.Modality != "WATER" {
				if segment.Modality == "GYM" {
					foundObjective := false
					for _, block := range segment.Blocks {
						if block.GymObjective != "" {
							supporting["Ginásio · "+structuredTrainingObjectiveName(block.GymObjective)]++
							foundObjective = true
						}
					}
					if !foundObjective {
						supporting["Ginásio"]++
					}
				} else {
					supporting[structuredModalityName(segment.Modality)]++
				}
				continue
			}
			for _, block := range segment.Blocks {
				waterSteps = append(waterSteps, block.WaterSteps...)
				if block.WaterMethod != "" {
					waterMethods[block.WaterMethod]++
				}
				if block.WaterTargetDistanceMetres > 0 {
					certainty := "exata"
					if block.WaterTargetCertainty == "ESTIMATED" {
						certainty = "estimada"
					}
					waterTargets = append(waterTargets, pages.StructuredTrainingSummaryItem{Label: "Meta de distância", Value: formatKilometres(int64(block.WaterTargetDistanceMetres)), Certainty: certainty + "; total da sessão, não somada aos blocos"})
				}
			}
		}
	}
	summary := pages.StructuredTrainingWeekSummary{}
	for _, total := range calculateWaterTotals(waterSteps) {
		summary.PlannedWater = append(summary.PlannedWater, pages.StructuredTrainingSummaryItem(total))
	}
	summary.PlannedWater = append(summary.PlannedWater, waterTargets...)
	methodLabels := make([]string, 0, len(waterMethods))
	for label := range waterMethods {
		methodLabels = append(methodLabels, label)
	}
	sort.Strings(methodLabels)
	for _, label := range methodLabels {
		summary.PlannedWater = append(summary.PlannedWater, pages.StructuredTrainingSummaryItem{Label: "Método · " + label, Value: pluralTrainingCount(waterMethods[label], "bloco", "blocos"), Certainty: "planeados"})
	}
	labels := make([]string, 0, len(supporting))
	for label := range supporting {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		summary.SupportingWork = append(summary.SupportingWork, pages.StructuredTrainingSummaryItem{Label: label, Value: pluralTrainingCount(supporting[label], "segmento", "segmentos"), Certainty: "planeados"})
	}
	if durationCount > 0 {
		summary.Actual = append(summary.Actual, pages.StructuredTrainingSummaryItem{Label: "Duração real", Value: formatTrainingDuration(actualDuration * 60), Certainty: "registada em " + pluralTrainingCount(durationCount, "sessão", "sessões")})
	}
	if distanceCount > 0 {
		summary.Actual = append(summary.Actual, pages.StructuredTrainingSummaryItem{Label: "Distância real", Value: formatKilometres(actualDistance), Certainty: "registada em " + pluralTrainingCount(distanceCount, "sessão", "sessões")})
	}
	return summary
}

func pluralTrainingCount(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func formatTrainingDuration(seconds int64) string {
	if seconds%3600 == 0 && seconds >= 3600 {
		return fmt.Sprintf("%d h", seconds/3600)
	}
	if seconds%60 == 0 {
		return fmt.Sprintf("%d min", seconds/60)
	}
	if seconds >= 60 {
		return fmt.Sprintf("%d min %d s", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%d s", seconds)
}

func structuredRoutineRows(rows []dbgen.ListVisibleTrainingRoutinesRow, location *time.Location) []pages.StructuredTrainingRoutine {
	result := make([]pages.StructuredTrainingRoutine, 0, len(rows))
	for _, row := range rows {
		scope := ""
		if row.TeamName != nil {
			scope = *row.TeamName
		} else if row.ProgrammeName != nil {
			scope = *row.ProgrammeName
		}
		visibility := "Só para mim"
		if row.Visibility == dbgen.TrainingRoutineVisibilitySHARED {
			visibility = "Partilhada"
		}
		result = append(result, pages.StructuredTrainingRoutine{ID: row.ID.String(), Name: row.Name, Description: row.Description, Kind: string(row.Kind), Visibility: visibility, Owner: row.OwnerName, Scope: scope, Modality: enumString(row.Modality), Objective: enumString(row.Objective), Method: row.Method, Tags: strings.Join(row.Tags, ", "), Preview: trainingRoutinePreview(row.Snapshot, row.Kind), Provenance: "Guardada de " + trainingRoutineKindName(row.Kind) + " em " + row.SourceUpdatedAt.Time.In(location).Format("02/01/2006 15:04")})
	}
	return result
}

func trainingRoutinePreview(snapshot []byte, kind dbgen.TrainingRoutineKind) string {
	var value struct {
		Title        string            `json:"title"`
		Instructions string            `json:"instructions"`
		Blocks       []json.RawMessage `json:"blocks"`
		Segments     []json.RawMessage `json:"segments"`
	}
	if json.Unmarshal(snapshot, &value) != nil {
		return "Conteúdo estruturado"
	}
	switch kind {
	case dbgen.TrainingRoutineKindBLOCK:
		if value.Title != "" {
			return value.Title + " · " + value.Instructions
		}
		return value.Instructions
	case dbgen.TrainingRoutineKindSEGMENT:
		label := value.Title
		if label == "" {
			label = "Segmento"
		}
		return fmt.Sprintf("%s · %d blocos", label, len(value.Blocks))
	default:
		label := value.Title
		if label == "" {
			label = "Sessão"
		}
		return fmt.Sprintf("%s · %d segmentos", label, len(value.Segments))
	}
}

func trainingRoutineKindName(kind dbgen.TrainingRoutineKind) string {
	switch kind {
	case dbgen.TrainingRoutineKindBLOCK:
		return "um bloco"
	case dbgen.TrainingRoutineKindSEGMENT:
		return "um segmento"
	default:
		return "uma sessão"
	}
}

func structuredModalityName(value string) string {
	switch value {
	case "WATER":
		return "Água"
	case "GYM":
		return "Ginásio"
	case "RUN":
		return "Corrida"
	case "BIKE":
		return "Bicicleta"
	case "ERGOMETER":
		return "Ergómetro"
	case "FLEXIBILITY":
		return "Flexibilidade e mobilidade"
	case "SPORTS_GAMES":
		return "Jogos desportivos"
	default:
		return "Outra"
	}
}

func structuredTrainingObjectiveName(value string) string {
	switch value {
	case "MOBILITY":
		return "Mobilidade"
	case "ACTIVATION":
		return "Ativação"
	case "MAX_STRENGTH_HYPERTROPHY":
		return "Força máxima e hipertrofia"
	case "MAX_STRENGTH_NEURAL":
		return "Força máxima neural"
	case "EXPLOSIVE_STRENGTH":
		return "Força explosiva"
	case "STRENGTH_ENDURANCE":
		return "Força-resistência"
	case "TECHNIQUE":
		return "Técnica"
	case "CORE":
		return "Core"
	default:
		return "Personalizado"
	}
}

func parseTrainingRoutineTags(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{}, nil
	}
	tags := make([]string, 0, 20)
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		tag := strings.TrimSpace(part)
		key := strings.ToLower(tag)
		if tag == "" || utf8.RuneCountInString(tag) > 40 {
			return nil, errors.New("invalid routine tag")
		}
		if !seen[key] {
			tags = append(tags, tag)
			seen[key] = true
		}
		if len(tags) > 20 {
			return nil, errors.New("too many routine tags")
		}
	}
	return tags, nil
}

func validTrainingRoutineKind(value dbgen.TrainingRoutineKind) bool {
	return value == dbgen.TrainingRoutineKindBLOCK || value == dbgen.TrainingRoutineKindSEGMENT || value == dbgen.TrainingRoutineKindSESSION
}

func validTrainingRoutineVisibility(value dbgen.TrainingRoutineVisibility) bool {
	return value == dbgen.TrainingRoutineVisibilityPRIVATE || value == dbgen.TrainingRoutineVisibilitySHARED
}

func isStructuredCopyRejection(err error) bool {
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) {
		return false
	}
	switch databaseError.Code {
	case "P0002", "23503", "23505", "23514":
		return true
	default:
		return false
	}
}

func managerStructuredRows(rows []dbgen.ListStructuredTrainingOverviewForManagerRow) []structuredTrainingRow {
	result := make([]structuredTrainingRow, 0, len(rows))
	for _, row := range rows {
		scope := row.ProgrammeName
		if row.TeamName != nil {
			scope += " · " + *row.TeamName
		}
		assembled := structuredRowFromValues(structuredTrainingRow{groupID: &row.GroupID, groupName: row.GroupName, scope: scope, memberCount: int(row.MemberCount), planID: row.PlanID, planTitle: stringValue(row.PlanTitle), planDescription: stringValue(row.PlanDescription), seasonName: stringValue(row.SeasonName), weekStart: row.WeekStart.Time, plannedLoadPercentage: row.PlannedLoadPercentage, sessionID: row.SessionID, sessionTitle: stringValue(row.SessionTitle), sessionDescription: stringValue(row.SessionDescription), startsAt: row.StartsAt.Time, endsAt: row.EndsAt.Time, entryKind: enumString(row.EntryKind), segmentID: row.SegmentID, segmentPosition: intValue(row.SegmentPosition), modality: enumString(row.SegmentModality), segmentTitle: stringValue(row.SegmentTitle), segmentLocation: stringValue(row.SegmentLocation), duration: intValue(row.PlannedDurationMinutes), startOffset: intValue(row.PlannedStartOffsetMinutes), plannedStartSet: row.PlannedStartOffsetMinutes != nil, transition: intValue(row.TransitionDurationMinutes), equipmentNotes: stringValue(row.EquipmentNotes), blockID: row.BlockID, blockPosition: intValue(row.BlockPosition), blockPurpose: enumString(row.BlockPurpose), blockTitle: stringValue(row.BlockTitle), instructions: stringValue(row.BlockInstructions)}, row.GymStructure, row.GymObjective, row.GymRounds, row.RoundRecoverySeconds, row.ExerciseID, row.ExercisePosition, row.ExerciseName, row.ExerciseSets, row.ExerciseRepetitions, row.ExerciseDurationSeconds, row.ExerciseDistanceMetres, row.ExerciseRecoverySeconds, row.ResistanceKind, row.ResistanceValue, row.ResistanceText, row.ExecutionIntent, row.Tempo, row.ExerciseNotes)
		result = append(result, structuredWaterRow(assembled, row.WaterMethod, row.WaterTargetDistanceMetres, row.WaterTargetDistanceCertainty, row.WaterProfileName, row.WaterProfileRevision, row.WaterProfileCraft, row.WaterStepID, row.WaterParentStepID, row.WaterStepPosition, row.WaterStepKind, row.WaterStepName, row.WaterStepRepeats, row.WaterStepDurationSeconds, row.WaterStepDurationCertainty, row.WaterStepDistanceMetres, row.WaterStepDistanceCertainty, row.WaterStepRecoverySeconds, row.WaterStepIntensityCode, row.WaterStepCadenceSpm, row.WaterZoneLabel, row.WaterZoneCadenceMin, row.WaterZoneCadenceMax, row.WaterZoneMeaning, row.WaterStepDrillFocus, row.WaterStepDrillFormat, row.WaterStepRoleNotes, row.WaterStepInstructions))
	}
	return result
}

func filterStructuredTrainingSubjectRows(rows []dbgen.ListTrainingPrescriptionsForViewerRow, requested string) ([]dbgen.ListTrainingPrescriptionsForViewerRow, []structuredTrainingSubject, *structuredTrainingSubject, error) {
	subjects := make([]structuredTrainingSubject, 0)
	seen := make(map[uuid.UUID]bool)
	for _, row := range rows {
		if seen[row.AthleteUserID] {
			continue
		}
		seen[row.AthleteUserID] = true
		subjects = append(subjects, structuredTrainingSubject{ID: row.AthleteUserID, Name: row.AthleteName})
	}
	if requested == "" {
		return rows, subjects, nil, nil
	}
	requestedID, err := uuid.Parse(requested)
	if err != nil {
		return nil, subjects, nil, errStructuredTrainingSubjectNotFound
	}
	var selected *structuredTrainingSubject
	for i := range subjects {
		if subjects[i].ID == requestedID {
			selected = &subjects[i]
			break
		}
	}
	if selected == nil {
		return nil, subjects, nil, errStructuredTrainingSubjectNotFound
	}
	filtered := make([]dbgen.ListTrainingPrescriptionsForViewerRow, 0, len(rows))
	for _, row := range rows {
		if row.AthleteUserID == requestedID {
			filtered = append(filtered, row)
		}
	}
	return filtered, subjects, selected, nil
}
func structuredRowFromValues(row structuredTrainingRow, structure *dbgen.GymBlockStructure, objective *dbgen.TrainingObjective, rounds, roundRecovery *int32, exerciseID *uuid.UUID, exercisePosition *int32, name *string, sets, repetitions, duration, distance, recovery *int32, resistanceKind *dbgen.GymResistanceKind, resistanceValue *float64, resistanceText *string, intent *dbgen.GymExecutionIntent, tempo, notes *string) structuredTrainingRow {
	row.gymStructure, row.gymObjective = enumString(structure), enumString(objective)
	row.gymRounds, row.gymRoundRecovery = intValue(rounds), intValue(roundRecovery)
	row.exerciseID, row.exercisePosition, row.exerciseName = exerciseID, intValue(exercisePosition), stringValue(name)
	row.exerciseSets, row.exerciseRepetitions, row.exerciseDuration = intValue(sets), intValue(repetitions), intValue(duration)
	row.exerciseDistance, row.exerciseRecovery = intValue(distance), intValue(recovery)
	row.exercisePrescription = gymExercisePrescription(row.exerciseSets, row.exerciseRepetitions, row.exerciseDuration, row.exerciseDistance, row.exerciseRecovery)
	row.exerciseResistance = gymResistanceLabel(enumString(resistanceKind), resistanceValue, stringValue(resistanceText))
	row.exerciseIntent, row.exerciseTempo, row.exerciseNotes = enumString(intent), stringValue(tempo), stringValue(notes)
	return row
}

func structuredWaterRow(row structuredTrainingRow, method *dbgen.WaterWorkMethod, targetDistance *int32, targetCertainty *dbgen.TrainingMeasureCertainty, profileName *string, profileRevision *int32, profileCraft *dbgen.PaddlingCraft, stepID, parentID *uuid.UUID, position *int32, kind *dbgen.WaterStepKind, name *string, repeats, duration *int32, durationCertainty *dbgen.TrainingMeasureCertainty, distance *int32, distanceCertainty *dbgen.TrainingMeasureCertainty, recovery *int32, intensity *string, cadence *int32, zoneLabel *string, zoneCadenceMin, zoneCadenceMax *int32, zoneMeaning *string, focus, format, roles, notes *string) structuredTrainingRow {
	row.waterMethod = waterMethodLabel(enumString(method))
	if targetDistance != nil {
		row.waterTargetDistance, row.waterTargetCertainty = int(*targetDistance), enumString(targetCertainty)
		row.waterTarget = fmt.Sprintf("Continuar até a sessão atingir %s (%s)", formatKilometres(int64(*targetDistance)), trainingMeasureCertaintyLabel(enumString(targetCertainty)))
	}
	if profileName != nil && profileRevision != nil {
		row.waterProfile = fmt.Sprintf("%s · %s · revisão %d", *profileName, paddlingCraftLabel(enumString(profileCraft)), *profileRevision)
	}
	row.waterStepID, row.waterParentStepID = stepID, parentID
	row.waterStepPosition, row.waterStepRepeats, row.waterStepRecovery = intValue(position), intValue(repeats), intValue(recovery)
	row.waterStepKind, row.waterStepName, row.waterStepNotes = enumString(kind), stringValue(name), stringValue(notes)
	row.waterStepDuration, row.waterStepDistance = intValue(duration), intValue(distance)
	row.waterStepDurationCertainty, row.waterStepDistanceCertainty = enumString(durationCertainty), enumString(distanceCertainty)
	parts := []string{}
	if duration != nil {
		parts = append(parts, fmt.Sprintf("%d s (%s)", *duration, trainingMeasureCertaintyLabel(enumString(durationCertainty))))
	}
	if distance != nil {
		parts = append(parts, fmt.Sprintf("%s (%s)", formatKilometres(int64(*distance)), trainingMeasureCertaintyLabel(enumString(distanceCertainty))))
	}
	row.waterStepPrescription = strings.Join(parts, " · ")
	if intensity != nil {
		row.waterStepIntensity = *intensity
	}
	if cadence != nil {
		row.waterStepIntensity += fmt.Sprintf(" · %d remadas/min", *cadence)
	}
	if zoneLabel != nil {
		row.waterStepIntensity += " · " + *zoneLabel
	}
	if zoneCadenceMin != nil || zoneCadenceMax != nil {
		row.waterStepIntensity += fmt.Sprintf(" · orientação %s–%s remadas/min", optionalIntLabel(zoneCadenceMin), optionalIntLabel(zoneCadenceMax))
	}
	if zoneMeaning != nil {
		row.waterStepIntensity += " · " + *zoneMeaning
	}
	drill := []string{}
	for _, value := range []*string{focus, format, roles} {
		if value != nil {
			drill = append(drill, *value)
		}
	}
	row.waterStepDrill = strings.Join(drill, " · ")
	return row
}

func structuredWaterProfiles(rows []dbgen.ListActiveWaterIntensityProfilesRow) []pages.StructuredWaterProfile {
	profiles := []pages.StructuredWaterProfile{}
	for _, row := range rows {
		if len(profiles) == 0 || profiles[len(profiles)-1].ID != row.ID.String() {
			profiles = append(profiles, pages.StructuredWaterProfile{ID: row.ID.String(), Name: row.Name, Craft: paddlingCraftLabel(string(row.Craft)), Notes: row.Notes, Revision: int(row.Revision)})
		}
		if row.ZoneID != nil {
			cadence := "Sem cadência fixa"
			if row.CadenceMin != nil || row.CadenceMax != nil {
				cadence = fmt.Sprintf("%s–%s remadas/min", optionalIntLabel(row.CadenceMin), optionalIntLabel(row.CadenceMax))
			}
			profiles[len(profiles)-1].Zones = append(profiles[len(profiles)-1].Zones, pages.StructuredWaterZone{Code: stringValue(row.Code), Label: stringValue(row.Label), Cadence: cadence, Meaning: stringValue(row.Meaning)})
		}
	}
	return profiles
}

func optionalIntLabel(value *int32) string {
	if value == nil {
		return "—"
	}
	return strconv.Itoa(int(*value))
}

func waterMethodLabel(value string) string {
	labels := map[string]string{"CONTINUOUS": "Contínuo", "INTERVALS": "Intervalos", "FARTLEK": "Fartlek", "TECHNIQUE": "Técnica", "STARTS": "Partidas", "RACE_SIMULATION": "Simulação de prova", "TACTICAL_DRILL": "Exercício técnico-tático", "CUSTOM": "Outro método"}
	return labels[value]
}

func trainingMeasureCertaintyLabel(value string) string {
	if value == "ESTIMATED" {
		return "estimado"
	}
	return "exato"
}

func paddlingCraftLabel(value string) string {
	if value == "CANOE" {
		return "Canoa"
	}
	return "Kayak"
}

func gymExercisePrescription(sets, repetitions, duration, distance, recovery int) string {
	parts := []string{}
	if sets > 0 {
		parts = append(parts, fmt.Sprintf("%d séries", sets))
	}
	if repetitions > 0 {
		parts = append(parts, fmt.Sprintf("%d repetições", repetitions))
	}
	if duration > 0 {
		parts = append(parts, fmt.Sprintf("%d s", duration))
	}
	if distance > 0 {
		parts = append(parts, fmt.Sprintf("%d m", distance))
	}
	if recovery > 0 {
		parts = append(parts, fmt.Sprintf("recuperação %d s", recovery))
	}
	return strings.Join(parts, " · ")
}

func gymResistanceLabel(kind string, value *float64, text string) string {
	if value != nil {
		formatted := strconv.FormatFloat(*value, 'f', -1, 64)
		switch kind {
		case "KILOGRAMS":
			return formatted + " kg"
		case "PERCENT_1RM":
			return formatted + "% de 1RM"
		case "RPE":
			return "RPE " + formatted
		case "RIR":
			return "RIR " + formatted
		}
	}
	switch kind {
	case "BODY_WEIGHT":
		return "Peso corporal"
	case "BAND":
		return "Banda · " + text
	case "COACH_INSTRUCTION":
		return text
	}
	return ""
}

func enumString[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}
func intValue(value *int32) int {
	if value == nil {
		return 0
	}
	return int(*value)
}
