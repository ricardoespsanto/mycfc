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
	groupID, planID, sessionID, segmentID, blockID                                                          *uuid.UUID
	memberCount, segmentPosition, blockPosition                                                             int
	weekStart                                                                                               time.Time
	startsAt, endsAt                                                                                        time.Time
	entryKind, modality, segmentTitle, segmentLocation, blockPurpose, blockTitle, instructions              string
	duration                                                                                                int
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
	if !validTrainingSegmentModality(modality) || !validTrainingBlockPurpose(purpose) || utf8.RuneCountInString(title) > 120 || utf8.RuneCountInString(location) > 180 || utf8.RuneCountInString(blockTitle) > 120 || !validTrainingText(instructions, 2, 4000) || durationErr != nil {
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
	_, err = h.Store.CreateSegment(ctx, StructuredTrainingSegmentInput{Segment: dbgen.CreateTrainingSessionSegmentParams{SessionID: sessionID, Modality: modality, Title: title, Location: location, PlannedDurationMinutes: duration}, Block: dbgen.CreateTrainingSegmentBlockParams{Purpose: purpose, Title: blockTitle, Instructions: instructions}})
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

func (h StructuredTraining) MoveSegment(w http.ResponseWriter, r *http.Request) { h.move(w, r, true) }
func (h StructuredTraining) MoveBlock(w http.ResponseWriter, r *http.Request)   { h.move(w, r, false) }

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
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > maximum {
		return nil, errors.New("invalid positive integer")
	}
	result := int32(n)
	return &result, nil
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
			session.Segments = append(session.Segments, pages.StructuredTrainingSegment{ID: row.segmentID.String(), Modality: row.modality, Title: row.segmentTitle, Location: row.segmentLocation, Duration: duration, Position: row.segmentPosition})
		}
		segment := &session.Segments[len(session.Segments)-1]
		if row.blockID != nil {
			segment.Blocks = append(segment.Blocks, pages.StructuredTrainingBlock{ID: row.blockID.String(), Purpose: row.blockPurpose, Title: row.blockTitle, Instructions: row.instructions, Position: row.blockPosition})
		}
	}
	return audiences
}

func managerStructuredRows(rows []dbgen.ListStructuredTrainingOverviewForManagerRow) []structuredTrainingRow {
	result := make([]structuredTrainingRow, 0, len(rows))
	for _, row := range rows {
		scope := row.ProgrammeName
		if row.TeamName != nil {
			scope += " · " + *row.TeamName
		}
		result = append(result, structuredTrainingRow{groupID: &row.GroupID, groupName: row.GroupName, scope: scope, memberCount: int(row.MemberCount), planID: row.PlanID, planTitle: stringValue(row.PlanTitle), planDescription: stringValue(row.PlanDescription), seasonName: stringValue(row.SeasonName), weekStart: row.WeekStart.Time, sessionID: row.SessionID, sessionTitle: stringValue(row.SessionTitle), sessionDescription: stringValue(row.SessionDescription), startsAt: row.StartsAt.Time, endsAt: row.EndsAt.Time, entryKind: enumString(row.EntryKind), segmentID: row.SegmentID, segmentPosition: intValue(row.SegmentPosition), modality: enumString(row.SegmentModality), segmentTitle: stringValue(row.SegmentTitle), segmentLocation: stringValue(row.SegmentLocation), duration: intValue(row.PlannedDurationMinutes), blockID: row.BlockID, blockPosition: intValue(row.BlockPosition), blockPurpose: enumString(row.BlockPurpose), blockTitle: stringValue(row.BlockTitle), instructions: stringValue(row.BlockInstructions)})
	}
	return result
}

func subjectStructuredRows(rows []dbgen.ListStructuredTrainingOverviewForSubjectRow) []structuredTrainingRow {
	result := make([]structuredTrainingRow, 0, len(rows))
	for _, row := range rows {
		result = append(result, structuredTrainingRow{athleteName: row.AthleteName, groupID: &row.GroupID, groupName: row.GroupName, scope: "Plano atribuído", planID: &row.PlanID, planTitle: row.PlanTitle, planDescription: row.PlanDescription, seasonName: row.SeasonName, weekStart: row.WeekStart.Time, sessionID: row.SessionID, sessionTitle: stringValue(row.SessionTitle), sessionDescription: stringValue(row.SessionDescription), startsAt: row.StartsAt.Time, endsAt: row.EndsAt.Time, entryKind: enumString(row.EntryKind), segmentID: row.SegmentID, segmentPosition: intValue(row.SegmentPosition), modality: enumString(row.SegmentModality), segmentTitle: stringValue(row.SegmentTitle), segmentLocation: stringValue(row.SegmentLocation), duration: intValue(row.PlannedDurationMinutes), blockID: row.BlockID, blockPosition: intValue(row.BlockPosition), blockPurpose: enumString(row.BlockPurpose), blockTitle: stringValue(row.BlockTitle), instructions: stringValue(row.BlockInstructions)})
	}
	return result
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
