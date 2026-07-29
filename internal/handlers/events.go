package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const eventQueryTimeout = 5 * time.Second

type Events struct {
	Store    dbgen.Querier
	DB       db.Beginner
	System   System
	PageMeta components.PageMeta
	Location *time.Location
	Now      func() time.Time
}

type eventForm struct {
	Title, Description, StartsAt, EndsAt, Deadline, Capacity string
	ProgrammeIDs                                             []uuid.UUID
	TeamIDs                                                  []uuid.UUID
	Errors                                                   validation.FieldErrors
}

func (h Events) Index(w http.ResponseWriter, r *http.Request) {
	h.renderIndex(w, r, http.StatusOK, eventForm{Errors: validation.FieldErrors{}})
}

func (h Events) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderIndex(w, r, http.StatusBadRequest, eventForm{Errors: validation.FieldErrors{}})
		return
	}
	form := h.validateEvent(r)
	if !form.Errors.Empty() {
		h.renderIndex(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	startsAt, _ := time.ParseInLocation("2006-01-02T15:04", form.StartsAt, h.location())
	endsAt, _ := time.ParseInLocation("2006-01-02T15:04", form.EndsAt, h.location())
	var deadline pgtype.Timestamptz
	if form.Deadline != "" {
		value, _ := time.ParseInLocation("2006-01-02T15:04", form.Deadline, h.location())
		deadline = pgtype.Timestamptz{Time: value, Valid: true}
	}
	var capacity *int32
	if form.Capacity != "" {
		value, _ := strconv.ParseInt(form.Capacity, 10, 32)
		converted := int32(value)
		capacity = &converted
	}
	ctx, cancel := context.WithTimeout(r.Context(), eventQueryTimeout)
	defer cancel()
	programmes, err := h.Store.ListProgrammes(ctx)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	knownProgrammes := make(map[uuid.UUID]bool, len(programmes))
	for _, programme := range programmes {
		knownProgrammes[programme.ID] = true
	}
	for _, programmeID := range form.ProgrammeIDs {
		if !knownProgrammes[programmeID] {
			form.Errors.Add("programme_id", "Selecione programas válidos.")
		}
	}
	teams, err := h.Store.ListTeamsForEventAuthoring(ctx)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	knownTeams := make(map[uuid.UUID]uuid.UUID, len(teams))
	for _, team := range teams {
		knownTeams[team.ID] = team.ProgrammeID
	}
	for _, teamID := range form.TeamIDs {
		_, ok := knownTeams[teamID]
		if !ok {
			form.Errors.Add("team_id", "Selecione equipas válidas.")
		}
	}
	if !canAuthorEvent(user, form.ProgrammeIDs, form.TeamIDs, knownTeams) {
		form.Errors.Add("audience", "Selecione pelo menos um destinatário.")
	}
	if !form.Errors.Empty() {
		h.renderIndex(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	err = db.WithinTx(ctx, h.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		queries := dbgen.New(tx)
		event, err := queries.CreateEvent(ctx, dbgen.CreateEventParams{Title: form.Title, Description: form.Description, StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: endsAt, Valid: true}, ResponseDeadline: deadline, Capacity: capacity, CreatedByID: user.ID})
		if err != nil {
			return err
		}
		for _, programmeID := range form.ProgrammeIDs {
			if err := queries.AddEventAudience(ctx, dbgen.AddEventAudienceParams{EventID: event.ID, ProgrammeID: programmeID}); err != nil {
				return err
			}
		}
		for _, teamID := range form.TeamIDs {
			if err := queries.AddEventTeamAudience(ctx, dbgen.AddEventTeamAudienceParams{EventID: event.ID, TeamID: teamID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	httpx.Redirect(w, r, "/events", http.StatusSeeOther)
}

func (h Events) Detail(w http.ResponseWriter, r *http.Request) {
	eventID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), eventQueryTimeout)
	defer cancel()
	page := pages.EventDetailPage{}
	if user.IsAdmin || user.CanManageEvents {
		if !user.IsAdmin {
			allowed, err := h.Store.CanCoachManageEvent(ctx, dbgen.CanCoachManageEventParams{EventID: eventID, UserID: user.ID})
			if err != nil {
				h.System.InternalError(w, r)
				return
			}
			if !allowed {
				h.System.Forbidden(w, r)
				return
			}
		}
		event, err := h.Store.GetEventDetailForAdmin(ctx, eventID)
		if errors.Is(err, pgx.ErrNoRows) {
			h.System.NotFound(w, r)
			return
		}
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		responses, err := h.Store.ListEventResponsesForAdmin(ctx, eventID)
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page = h.adminDetailPage(event, responses)
	} else {
		event, err := h.Store.GetEventDetailForMember(ctx, dbgen.GetEventDetailForMemberParams{UserID: user.ID, EventID: eventID})
		if errors.Is(err, pgx.ErrNoRows) {
			h.System.NotFound(w, r)
			return
		}
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		dependents, err := h.Store.ListDependentsByGuardian(ctx, dbgen.ListDependentsByGuardianParams{GuardianID: &user.ID, RowLimit: 10})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page = h.memberDetailPage(event, dependents)
	}
	page.Meta = h.meta(r, user, "/events", "Evento")
	page.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pages.EventDetail(page).Render(r.Context(), w)
}

func (h Events) Respond(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	eventID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	subjectID := user.ID
	if raw := r.PostForm.Get("subject_user_id"); raw != "" {
		subjectID, err = uuid.Parse(raw)
		if err != nil {
			http.Error(w, "Pedido inválido.", http.StatusBadRequest)
			return
		}
	}
	wantsGoing := r.PostForm.Get("status") == "Going"
	if !wantsGoing && r.PostForm.Get("status") != "NotGoing" {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), eventQueryTimeout)
	defer cancel()
	if _, err := h.Store.GetRespondableEvent(ctx, dbgen.GetRespondableEventParams{SubjectUserID: subjectID, EventID: eventID, ActorUserID: user.ID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			h.System.Forbidden(w, r)
		} else {
			h.System.InternalError(w, r)
		}
		return
	}
	err = db.WithinTx(ctx, h.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		queries := dbgen.New(tx)
		event, err := queries.GetEventForResponse(ctx, eventID) // serializes capacity decisions for this event.
		if err != nil {
			return err
		}
		if event.ResponseDeadline.Valid && h.now().After(event.ResponseDeadline.Time) {
			return errResponseDeadline
		}
		status := dbgen.EventResponseStatusNotGoing
		if wantsGoing {
			status = dbgen.EventResponseStatusGoing
			existing, err := queries.GetEventResponse(ctx, dbgen.GetEventResponseParams{EventID: eventID, UserID: subjectID})
			alreadyGoing := err == nil && existing.Status == "Going"
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if event.Capacity != nil && !alreadyGoing {
				count, err := queries.CountGoingEventResponses(ctx, eventID)
				if err != nil {
					return err
				}
				if count >= int64(*event.Capacity) {
					status = dbgen.EventResponseStatusWaitlisted
				}
			}
		}
		return queries.SaveEventResponse(ctx, dbgen.SaveEventResponseParams{EventID: eventID, UserID: subjectID, Status: status, RespondedByID: user.ID})
	})
	if errors.Is(err, errResponseDeadline) {
		http.Error(w, "O prazo de resposta terminou.", http.StatusConflict)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	httpx.Redirect(w, r, "/events/"+eventID.String(), http.StatusSeeOther)
}

func (h Events) Confirm(w http.ResponseWriter, r *http.Request) { h.staffAction(w, r, true) }
func (h Events) CheckIn(w http.ResponseWriter, r *http.Request) { h.staffAction(w, r, false) }

func (h Events) staffAction(w http.ResponseWriter, r *http.Request, confirm bool) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	eventID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	subjectID, err := uuid.Parse(r.PostForm.Get("user_id"))
	if err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), eventQueryTimeout)
	defer cancel()
	if !user.IsAdmin {
		allowed, err := h.Store.CanCoachManageEvent(ctx, dbgen.CanCoachManageEventParams{EventID: eventID, UserID: user.ID})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		if !allowed {
			h.System.Forbidden(w, r)
			return
		}
	}
	err = db.WithinTx(ctx, h.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		queries := dbgen.New(tx)
		if confirm {
			event, err := queries.GetEventForResponse(ctx, eventID)
			if err != nil {
				return err
			}
			if event.Capacity != nil {
				count, err := queries.CountGoingEventResponses(ctx, eventID)
				if err != nil {
					return err
				}
				if count >= int64(*event.Capacity) {
					return errEventFull
				}
			}
			n, err := queries.ConfirmWaitlistedResponse(ctx, dbgen.ConfirmWaitlistedResponseParams{StaffUserID: user.ID, EventID: eventID, UserID: subjectID})
			if err != nil {
				return err
			}
			if n == 0 {
				return errInvalidEventState
			}
			return nil
		}
		event, err := queries.GetEventForResponse(ctx, eventID)
		if err != nil {
			return err
		}
		if h.now().Before(event.StartsAt.Time) {
			return errEventNotStarted
		}
		n, err := queries.CheckInEventResponse(ctx, dbgen.CheckInEventResponseParams{StaffUserID: &user.ID, EventID: eventID, UserID: subjectID})
		if err != nil {
			return err
		}
		if n == 0 {
			return errInvalidEventState
		}
		return nil
	})
	if errors.Is(err, errEventFull) {
		http.Error(w, "Não existe vaga disponível.", http.StatusConflict)
		return
	}
	if errors.Is(err, errInvalidEventState) {
		http.Error(w, "A operação não é válida para esta resposta.", http.StatusConflict)
		return
	}
	if errors.Is(err, errEventNotStarted) {
		http.Error(w, "A presença só pode ser registada após o início do evento.", http.StatusConflict)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	httpx.Redirect(w, r, "/events/"+eventID.String(), http.StatusSeeOther)
}

var (
	errResponseDeadline  = errors.New("event response deadline")
	errEventFull         = errors.New("event full")
	errInvalidEventState = errors.New("invalid event state")
	errEventNotStarted   = errors.New("event has not started")
)

func (h Events) validateEvent(r *http.Request) eventForm {
	form := eventForm{Title: strings.TrimSpace(r.PostForm.Get("title")), Description: strings.TrimSpace(r.PostForm.Get("description")), StartsAt: strings.TrimSpace(r.PostForm.Get("starts_at")), EndsAt: strings.TrimSpace(r.PostForm.Get("ends_at")), Deadline: strings.TrimSpace(r.PostForm.Get("response_deadline")), Capacity: strings.TrimSpace(r.PostForm.Get("capacity")), Errors: validation.FieldErrors{}}
	if n := utf8.RuneCountInString(form.Title); n < 2 || n > 180 {
		form.Errors.Add("title", "O título deve ter entre 2 e 180 caracteres.")
	}
	if utf8.RuneCountInString(form.Description) > 4000 {
		form.Errors.Add("description", "A descrição não pode exceder 4000 caracteres.")
	}
	startsAt, startErr := time.ParseInLocation("2006-01-02T15:04", form.StartsAt, h.location())
	if startErr != nil {
		form.Errors.Add("starts_at", "Introduza uma data e hora de início válidas.")
	}
	endsAt, endErr := time.ParseInLocation("2006-01-02T15:04", form.EndsAt, h.location())
	if endErr != nil {
		form.Errors.Add("ends_at", "Introduza uma data e hora de fim válidas.")
	} else if startErr == nil && !endsAt.After(startsAt) {
		form.Errors.Add("ends_at", "O fim tem de ser posterior ao início.")
	}
	if form.Deadline != "" {
		deadline, err := time.ParseInLocation("2006-01-02T15:04", form.Deadline, h.location())
		if err != nil || (startErr == nil && deadline.After(startsAt)) {
			form.Errors.Add("response_deadline", "O limite de resposta tem de ser anterior ao início.")
		}
	}
	if form.Capacity != "" {
		value, err := strconv.ParseInt(form.Capacity, 10, 32)
		if err != nil || value < 1 {
			form.Errors.Add("capacity", "A lotação deve ser um número inteiro positivo.")
		}
	}
	seen := map[uuid.UUID]bool{}
	for _, raw := range r.PostForm["programme_id"] {
		id, err := uuid.Parse(raw)
		if err != nil || seen[id] {
			form.Errors.Add("programme_id", "Selecione programas válidos.")
			continue
		}
		seen[id] = true
		form.ProgrammeIDs = append(form.ProgrammeIDs, id)
	}
	seen = map[uuid.UUID]bool{}
	for _, raw := range r.PostForm["team_id"] {
		id, err := uuid.Parse(raw)
		if err != nil || seen[id] {
			form.Errors.Add("team_id", "Selecione equipas válidas.")
			continue
		}
		seen[id] = true
		form.TeamIDs = append(form.TeamIDs, id)
	}
	return form
}

func canAuthorEvent(user CurrentUser, programmeIDs, teamIDs []uuid.UUID, teamProgrammes map[uuid.UUID]uuid.UUID) bool {
	if user.IsAdmin {
		return true
	}
	if len(programmeIDs) == 0 && len(teamIDs) == 0 {
		return false
	}
	for _, programmeID := range programmeIDs {
		if !user.CoachProgrammeIDs[programmeID] {
			return false
		}
	}
	for _, teamID := range teamIDs {
		if !user.CoachTeamIDs[teamID] && !user.CoachProgrammeIDs[teamProgrammes[teamID]] {
			return false
		}
	}
	return true
}

func (h Events) renderIndex(w http.ResponseWriter, r *http.Request, status int, form eventForm) {
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), eventQueryTimeout)
	defer cancel()
	page := pages.EventsPage{CanManageEvents: user.IsAdmin || user.CanManageEvents, Form: pages.EventForm{Title: form.Title, Description: form.Description, StartsAt: form.StartsAt, EndsAt: form.EndsAt, Deadline: form.Deadline, Capacity: form.Capacity, Errors: form.Errors, CSRFField: templ.Raw(string(csrf.TemplateField(r)))}}
	if user.IsAdmin || user.CanManageEvents {
		if user.IsAdmin {
			items, err := h.Store.ListEventsForAdmin(ctx, 100)
			if err != nil {
				h.System.InternalError(w, r)
				return
			}
			for _, item := range items {
				page.Events = append(page.Events, pages.EventItem{ID: item.ID.String(), Title: item.Title, When: h.dateRange(item.StartsAt.Time, item.EndsAt.Time), Status: "Confirmados: " + strconv.FormatInt(item.GoingCount, 10), Capacity: h.capacity(item.Capacity)})
			}
		} else {
			items, err := h.Store.ListEventsForCoach(ctx, dbgen.ListEventsForCoachParams{UserID: user.ID, RowLimit: 100})
			if err != nil {
				h.System.InternalError(w, r)
				return
			}
			for _, item := range items {
				page.Events = append(page.Events, pages.EventItem{ID: item.ID.String(), Title: item.Title, When: h.dateRange(item.StartsAt.Time, item.EndsAt.Time), Status: "Confirmados: " + strconv.FormatInt(item.GoingCount, 10), Capacity: h.capacity(item.Capacity)})
			}
		}
		programmes, err := h.Store.ListProgrammes(ctx)
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		selected := map[uuid.UUID]bool{}
		for _, id := range form.ProgrammeIDs {
			selected[id] = true
		}
		for _, programme := range programmes {
			if user.IsAdmin || user.CoachProgrammeIDs[programme.ID] {
				page.Form.Programmes = append(page.Form.Programmes, pages.EventProgramme{ID: programme.ID.String(), Name: programme.NamePt, Selected: selected[programme.ID]})
			}
		}
		teams, err := h.Store.ListTeamsForEventAuthoring(ctx)
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		selectedTeams := map[uuid.UUID]bool{}
		for _, id := range form.TeamIDs {
			selectedTeams[id] = true
		}
		for _, team := range teams {
			if user.IsAdmin || user.CoachTeamIDs[team.ID] || user.CoachProgrammeIDs[team.ProgrammeID] {
				page.Form.Teams = append(page.Form.Teams, pages.EventTeam{ID: team.ID.String(), Name: team.Name, Selected: selectedTeams[team.ID]})
			}
		}
	} else {
		items, err := h.Store.ListEventsForMember(ctx, dbgen.ListEventsForMemberParams{UserID: user.ID, RowLimit: 100})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		for _, item := range items {
			page.Events = append(page.Events, pages.EventItem{ID: item.ID.String(), Title: item.Title, When: h.dateRange(item.StartsAt.Time, item.EndsAt.Time), Status: eventStatus(item.ResponseStatus), Capacity: h.capacity(item.Capacity)})
		}
	}
	page.Meta = h.meta(r, user, "/events", "Eventos")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.Events(page).Render(r.Context(), w)
}

func (h Events) memberDetailPage(event dbgen.GetEventDetailForMemberRow, dependents []dbgen.ListDependentsByGuardianRow) pages.EventDetailPage {
	page := pages.EventDetailPage{ID: event.ID.String(), Title: event.Title, Description: event.Description, When: h.dateRange(event.StartsAt.Time, event.EndsAt.Time), Deadline: h.deadline(event.ResponseDeadline), Capacity: h.capacity(event.Capacity), Status: eventStatus(event.ResponseStatus)}
	if event.ResponseDeadline.Valid && h.now().After(event.ResponseDeadline.Time) {
		page.Status = "Fora do prazo"
	}
	for _, dependent := range dependents {
		page.Dependents = append(page.Dependents, pages.EventDependent{ID: dependent.ID.String(), Name: dependent.Name})
	}
	return page
}
func (h Events) adminDetailPage(event dbgen.Event, responses []dbgen.ListEventResponsesForAdminRow) pages.EventDetailPage {
	page := pages.EventDetailPage{CanManageEvents: true, ID: event.ID.String(), Title: event.Title, Description: event.Description, When: h.dateRange(event.StartsAt.Time, event.EndsAt.Time), Deadline: h.deadline(event.ResponseDeadline), Capacity: h.capacity(event.Capacity)}
	for _, response := range responses {
		checked := ""
		if response.CheckedInAt.Valid {
			checked = response.CheckedInAt.Time.In(h.location()).Format("02/01/2006 15:04")
		}
		page.Responses = append(page.Responses, pages.EventResponse{UserID: response.UserID.String(), Name: response.UserName, Status: eventStatus(response.Status), RespondedAt: response.RespondedAt.Time.In(h.location()).Format("02/01/2006 15:04"), CheckedInAt: checked})
	}
	return page
}
func (h Events) meta(r *http.Request, user CurrentUser, path, title string) components.PageMeta {
	meta := h.PageMeta
	meta.Title = title + " | MyCFC"
	meta.CurrentPath = path
	meta.CurrentUserName = user.Name
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return meta
}
func (h Events) location() *time.Location {
	if h.Location != nil {
		return h.Location
	}
	return time.UTC
}
func (h Events) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}
func (h Events) dateRange(start, end time.Time) string {
	return start.In(h.location()).Format("02/01/2006 15:04") + " - " + end.In(h.location()).Format("15:04")
}
func (h Events) deadline(value pgtype.Timestamptz) string {
	if !value.Valid {
		return "Sem limite de resposta"
	}
	return "Responda até " + value.Time.In(h.location()).Format("02/01/2006 15:04")
}
func (h Events) capacity(value *int32) string {
	if value == nil {
		return ""
	}
	return " · Lotação: " + strconv.FormatInt(int64(*value), 10)
}
func eventStatus(value interface{}) string {
	switch status := value.(type) {
	case string:
		return eventStatusText(status)
	case dbgen.EventResponseStatus:
		return eventStatusText(string(status))
	default:
		return "Pendente"
	}
}
func eventStatusText(status string) string {
	switch status {
	case "Going":
		return "Vou"
	case "NotGoing":
		return "Não vou"
	case "Waitlisted":
		return "Em lista de espera"
	default:
		return "Pendente"
	}
}
