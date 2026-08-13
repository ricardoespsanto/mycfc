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
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	weekStart                                                                                               time.Time
	startsAt, endsAt                                                                                        time.Time
	entryKind, modality, segmentTitle, segmentLocation, blockPurpose, blockTitle, instructions              string
	duration, startOffset, transition                                                                       int
	plannedStartSet                                                                                         bool
	equipmentNotes                                                                                          string
	gymStructure, gymObjective                                                                              string
	gymRounds, gymRoundRecovery                                                                             int
	exerciseName, exercisePrescription, exerciseResistance, exerciseIntent, exerciseTempo, exerciseNotes    string
}

func (h StructuredTraining) Index(w http.ResponseWriter, r *http.Request) {
	h.renderIndex(w, r, http.StatusOK, pages.StructuredTrainingPage{})
}

func (h StructuredTraining) renderIndex(w http.ResponseWriter, r *http.Request, status int, page pages.StructuredTrainingPage) {
	user, _ := CurrentUserFromContext(r.Context())
	page.Management = strings.HasPrefix(r.URL.Path, "/admin/")
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if page.Management {
		rows, err := h.Store.ListStructuredTrainingOverviewForManager(ctx, dbgen.ListStructuredTrainingOverviewForManagerParams{IsAdmin: user.IsAdmin, UserID: user.ID})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page.Audiences = assembleStructuredTraining(managerStructuredRows(rows), h.location())
		memberships, err := h.Store.ListEligibleTrainingGroupMemberships(ctx, dbgen.ListEligibleTrainingGroupMembershipsParams{IsAdmin: user.IsAdmin, UserID: user.ID})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page.Memberships, page.Programmes, page.Teams = structuredChoices(memberships)
		page.Groups, page.Weeks = structuredPlanChoices(page.Audiences)
	} else {
		rows, err := h.Store.ListStructuredTrainingOverviewForSubject(ctx, user.ID)
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page.Audiences = assembleStructuredTraining(subjectStructuredRows(rows), h.location())
	}
	page.Meta = h.meta(r, user, page.Management)
	page.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	if h.Sessions != nil && page.Success == "" {
		page.Success = h.Sessions.PopString(r.Context(), "structured_training_flash")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.StructuredTraining(page).Render(r.Context(), w)
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
	form := pages.StructuredTrainingWeekForm{GroupID: r.PostForm.Get("group_id"), Title: strings.TrimSpace(r.PostForm.Get("title")), Description: strings.TrimSpace(r.PostForm.Get("description")), WeekStart: r.PostForm.Get("week_start"), Errors: validation.FieldErrors{}}
	groupID, groupErr := uuid.Parse(form.GroupID)
	weekStart, dateErr := time.ParseInLocation("2006-01-02", form.WeekStart, h.location())
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
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	if groupErr == nil && !h.canManageGroup(ctx, user, groupID, w, r) {
		return
	}
	if !form.Errors.Empty() {
		h.renderIndex(w, r, http.StatusUnprocessableEntity, pages.StructuredTrainingPage{OpenForm: "week", WeekForm: form})
		return
	}
	_, err := h.Store.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{Title: form.Title, Description: form.Description, WeekStart: pgtype.Date{Time: weekStart, Valid: true}, CreatedByID: user.ID, GroupID: groupID})
	if errors.Is(err, pgx.ErrNoRows) {
		form.Errors.Add("week_start", "A semana tem de pertencer a uma época registada.")
		h.renderIndex(w, r, http.StatusUnprocessableEntity, pages.StructuredTrainingPage{OpenForm: "week", WeekForm: form})
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Semana de treino criada.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
}

func (h StructuredTraining) CreateSession(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderIndex(w, r, http.StatusBadRequest, pages.StructuredTrainingPage{Error: "Não foi possível ler o formulário.", OpenForm: "session"})
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
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
		h.renderIndex(w, r, http.StatusUnprocessableEntity, pages.StructuredTrainingPage{OpenForm: "session", SessionForm: form})
		return
	}
	_, err := h.Store.CreateStructuredTrainingSession(ctx, dbgen.CreateStructuredTrainingSessionParams{Title: form.Title, Description: form.Description, StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: endsAt, Valid: true}, EntryKind: entryKind, CreatedByID: user.ID, PlanID: planID})
	if errors.Is(err, pgx.ErrNoRows) {
		form.Errors.Add("starts_at", "A sessão tem de ficar inteiramente dentro da semana selecionada.")
		h.renderIndex(w, r, http.StatusUnprocessableEntity, pages.StructuredTrainingPage{OpenForm: "session", SessionForm: form})
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Sessão estruturada criada.")
	httpx.Redirect(w, r, "/admin/treinos/estruturados", http.StatusSeeOther)
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

func structuredPlanChoices(audiences []pages.StructuredTrainingAudience) ([]pages.StructuredTrainingChoice, []pages.StructuredTrainingChoice) {
	groups, weeks := []pages.StructuredTrainingChoice{}, []pages.StructuredTrainingChoice{}
	for _, audience := range audiences {
		groups = append(groups, pages.StructuredTrainingChoice{ID: audience.GroupID, Name: audience.GroupName})
		for _, week := range audience.Weeks {
			weeks = append(weeks, pages.StructuredTrainingChoice{ID: week.ID, Name: audience.GroupName + " · " + week.Title + " · " + week.DateRange})
		}
	}
	return groups, weeks
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
			audience.Weeks = append(audience.Weeks, pages.StructuredTrainingWeek{ID: row.planID.String(), Title: row.planTitle, Description: row.planDescription, Season: row.seasonName, DateRange: fmt.Sprintf("%s–%s", row.weekStart.In(location).Format("02/01/2006"), row.weekStart.AddDate(0, 0, 6).In(location).Format("02/01/2006"))})
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
				segment.Blocks = append(segment.Blocks, pages.StructuredTrainingBlock{ID: row.blockID.String(), Purpose: row.blockPurpose, Title: row.blockTitle, Instructions: row.instructions, Position: row.blockPosition, GymStructure: row.gymStructure, GymObjective: row.gymObjective, GymRounds: row.gymRounds, GymRoundRecovery: row.gymRoundRecovery})
			}
			block := &segment.Blocks[len(segment.Blocks)-1]
			if row.exerciseID != nil {
				block.Exercises = append(block.Exercises, pages.StructuredGymExercise{ID: row.exerciseID.String(), Name: row.exerciseName, Prescription: row.exercisePrescription, Resistance: row.exerciseResistance, Intent: row.exerciseIntent, Tempo: row.exerciseTempo, Notes: row.exerciseNotes, Position: row.exercisePosition})
			}
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

func managerStructuredRows(rows []dbgen.ListStructuredTrainingOverviewForManagerRow) []structuredTrainingRow {
	result := make([]structuredTrainingRow, 0, len(rows))
	for _, row := range rows {
		scope := row.ProgrammeName
		if row.TeamName != nil {
			scope += " · " + *row.TeamName
		}
		result = append(result, structuredRowFromValues(structuredTrainingRow{groupID: &row.GroupID, groupName: row.GroupName, scope: scope, memberCount: int(row.MemberCount), planID: row.PlanID, planTitle: stringValue(row.PlanTitle), planDescription: stringValue(row.PlanDescription), seasonName: stringValue(row.SeasonName), weekStart: row.WeekStart.Time, sessionID: row.SessionID, sessionTitle: stringValue(row.SessionTitle), sessionDescription: stringValue(row.SessionDescription), startsAt: row.StartsAt.Time, endsAt: row.EndsAt.Time, entryKind: enumString(row.EntryKind), segmentID: row.SegmentID, segmentPosition: intValue(row.SegmentPosition), modality: enumString(row.SegmentModality), segmentTitle: stringValue(row.SegmentTitle), segmentLocation: stringValue(row.SegmentLocation), duration: intValue(row.PlannedDurationMinutes), startOffset: intValue(row.PlannedStartOffsetMinutes), plannedStartSet: row.PlannedStartOffsetMinutes != nil, transition: intValue(row.TransitionDurationMinutes), equipmentNotes: stringValue(row.EquipmentNotes), blockID: row.BlockID, blockPosition: intValue(row.BlockPosition), blockPurpose: enumString(row.BlockPurpose), blockTitle: stringValue(row.BlockTitle), instructions: stringValue(row.BlockInstructions)}, row.GymStructure, row.GymObjective, row.GymRounds, row.RoundRecoverySeconds, row.ExerciseID, row.ExercisePosition, row.ExerciseName, row.ExerciseSets, row.ExerciseRepetitions, row.ExerciseDurationSeconds, row.ExerciseDistanceMetres, row.ExerciseRecoverySeconds, row.ResistanceKind, row.ResistanceValue, row.ResistanceText, row.ExecutionIntent, row.Tempo, row.ExerciseNotes))
	}
	return result
}

func subjectStructuredRows(rows []dbgen.ListStructuredTrainingOverviewForSubjectRow) []structuredTrainingRow {
	result := make([]structuredTrainingRow, 0, len(rows))
	for _, row := range rows {
		result = append(result, structuredRowFromValues(structuredTrainingRow{athleteName: row.AthleteName, groupID: &row.GroupID, groupName: row.GroupName, scope: "Plano atribuído", planID: &row.PlanID, planTitle: row.PlanTitle, planDescription: row.PlanDescription, seasonName: row.SeasonName, weekStart: row.WeekStart.Time, sessionID: row.SessionID, sessionTitle: stringValue(row.SessionTitle), sessionDescription: stringValue(row.SessionDescription), startsAt: row.StartsAt.Time, endsAt: row.EndsAt.Time, entryKind: enumString(row.EntryKind), segmentID: row.SegmentID, segmentPosition: intValue(row.SegmentPosition), modality: enumString(row.SegmentModality), segmentTitle: stringValue(row.SegmentTitle), segmentLocation: stringValue(row.SegmentLocation), duration: intValue(row.PlannedDurationMinutes), startOffset: intValue(row.PlannedStartOffsetMinutes), plannedStartSet: row.PlannedStartOffsetMinutes != nil, transition: intValue(row.TransitionDurationMinutes), equipmentNotes: stringValue(row.EquipmentNotes), blockID: row.BlockID, blockPosition: intValue(row.BlockPosition), blockPurpose: enumString(row.BlockPurpose), blockTitle: stringValue(row.BlockTitle), instructions: stringValue(row.BlockInstructions)}, row.GymStructure, row.GymObjective, row.GymRounds, row.RoundRecoverySeconds, row.ExerciseID, row.ExercisePosition, row.ExerciseName, row.ExerciseSets, row.ExerciseRepetitions, row.ExerciseDurationSeconds, row.ExerciseDistanceMetres, row.ExerciseRecoverySeconds, row.ResistanceKind, row.ResistanceValue, row.ResistanceText, row.ExecutionIntent, row.Tempo, row.ExerciseNotes))
	}
	return result
}

func structuredRowFromValues(row structuredTrainingRow, structure *dbgen.GymBlockStructure, objective *dbgen.TrainingObjective, rounds, roundRecovery *int32, exerciseID *uuid.UUID, exercisePosition *int32, name *string, sets, repetitions, duration, distance, recovery *int32, resistanceKind *dbgen.GymResistanceKind, resistanceValue *float64, resistanceText *string, intent *dbgen.GymExecutionIntent, tempo, notes *string) structuredTrainingRow {
	row.gymStructure, row.gymObjective = enumString(structure), enumString(objective)
	row.gymRounds, row.gymRoundRecovery = intValue(rounds), intValue(roundRecovery)
	row.exerciseID, row.exercisePosition, row.exerciseName = exerciseID, intValue(exercisePosition), stringValue(name)
	row.exercisePrescription = gymExercisePrescription(intValue(sets), intValue(repetitions), intValue(duration), intValue(distance), intValue(recovery))
	row.exerciseResistance = gymResistanceLabel(enumString(resistanceKind), resistanceValue, stringValue(resistanceText))
	row.exerciseIntent, row.exerciseTempo, row.exerciseNotes = enumString(intent), stringValue(tempo), stringValue(notes)
	return row
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
