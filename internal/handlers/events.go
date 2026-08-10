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
	"github.com/alexedwards/scs/v2"
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
const eventsPageSize = 6
const eventResponsesPageSize = 6

type Events struct {
	Store    dbgen.Querier
	DB       db.Beginner
	System   System
	PageMeta components.PageMeta
	Location *time.Location
	Now      func() time.Time
	Sessions *scs.SessionManager
}

type eventForm struct {
	Title, Description, EventType, StartsAt, EndsAt, Deadline, Capacity string
	DocumentTitle, DocumentURL, DocumentSource, DocumentReviewedOn      string
	ExpectedUpdatedAt                                                   string
	ProgrammeIDs                                                        []uuid.UUID
	TeamIDs                                                             []uuid.UUID
	Editing, AudienceLocked, HasDocument                                bool
	Errors                                                              validation.FieldErrors
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
		event, err := queries.CreateEvent(ctx, dbgen.CreateEventParams{Title: form.Title, Description: form.Description, EventType: form.EventType, StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: endsAt, Valid: true}, ResponseDeadline: deadline, Capacity: capacity, CreatedByID: user.ID})
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
		if form.DocumentTitle != "" {
			reviewed, _ := time.Parse("2006-01-02", form.DocumentReviewedOn)
			if _, err := queries.CreateCompetitionDocument(ctx, dbgen.CreateCompetitionDocumentParams{Title: form.DocumentTitle, Url: form.DocumentURL, Source: form.DocumentSource, ReviewedOn: pgtype.Date{Time: reviewed, Valid: true}, EventID: &event.ID, AuthorID: user.ID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "events_flash", "Evento criado.")
	}
	httpx.Redirect(w, r, "/admin/eventos", http.StatusSeeOther)
}

func (h Events) Edit(w http.ResponseWriter, r *http.Request) {
	eventID, ok := h.eventID(w, r)
	if !ok {
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), eventQueryTimeout)
	defer cancel()
	event, err := h.Store.GetEventForEdit(ctx, eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageEvent(ctx, user, eventID, w, r) {
		return
	}
	form, err := h.eventFormFromRecord(ctx, event)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if event.Status != "ACTIVE" || !event.StartsAt.Time.After(h.now()) {
		form.Errors.Add("state", "Apenas eventos ativos que ainda não começaram podem ser alterados.")
		h.renderEdit(w, r, http.StatusConflict, eventID, form, "O evento já não pode ser alterado.", "", validation.FieldErrors{})
		return
	}
	h.renderEdit(w, r, http.StatusOK, eventID, form, "", "", validation.FieldErrors{})
}

func (h Events) Update(w http.ResponseWriter, r *http.Request) {
	eventID, ok := h.eventID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), eventQueryTimeout)
	defer cancel()
	current, err := h.Store.GetEventForEdit(ctx, eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageEvent(ctx, user, eventID, w, r) {
		return
	}
	form := h.validateEvent(r)
	form.Editing = true
	form.ExpectedUpdatedAt = strings.TrimSpace(r.PostForm.Get("expected_updated_at"))
	form.AudienceLocked = current.HasResponses
	form.HasDocument = current.HasDocument
	h.validateEventAudience(ctx, user, &form)
	programmeIDs, err := h.Store.ListEventProgrammeAudienceIDs(ctx, eventID)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	teamIDs, err := h.Store.ListEventTeamAudienceIDs(ctx, eventID)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	audienceChanged := !sameUUIDSet(programmeIDs, form.ProgrammeIDs) || !sameUUIDSet(teamIDs, form.TeamIDs)
	if current.HasResponses && audienceChanged {
		form.Errors.Add("audience", "Os destinatários não podem ser alterados depois da primeira resposta.")
	}
	if current.HasDocument && form.EventType != "COMPETITION" {
		form.Errors.Add("event_type", "Um evento com documento oficial tem de continuar a ser uma competição.")
	}
	if capacity, ok := eventCapacity(form.Capacity); ok && capacity != nil && int64(*capacity) < current.GoingCount {
		form.Errors.Add("capacity", "A lotação não pode ser inferior ao número de participantes confirmados.")
	}
	expected, err := time.Parse(time.RFC3339Nano, form.ExpectedUpdatedAt)
	if err != nil {
		form.Errors.Add("state", "O formulário deixou de ser válido. Atualize a página.")
	}
	if !form.Errors.Empty() {
		h.renderEdit(w, r, http.StatusUnprocessableEntity, eventID, form, "", "", validation.FieldErrors{})
		return
	}
	startsAt, _ := time.ParseInLocation("2006-01-02T15:04", form.StartsAt, h.location())
	endsAt, _ := time.ParseInLocation("2006-01-02T15:04", form.EndsAt, h.location())
	deadline := eventDeadline(form.Deadline, h.location())
	capacity, _ := eventCapacity(form.Capacity)
	err = db.WithinTx(ctx, h.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		if _, err := q.UpdateEvent(ctx, dbgen.UpdateEventParams{Title: form.Title, Description: form.Description, EventType: form.EventType, StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: endsAt, Valid: true}, ResponseDeadline: deadline, Capacity: capacity, ID: eventID, AsOf: pgtype.Timestamptz{Time: h.now(), Valid: true}, ExpectedUpdatedAt: pgtype.Timestamptz{Time: expected, Valid: true}, AudienceChanged: audienceChanged}); err != nil {
			return err
		}
		if !audienceChanged {
			return nil
		}
		if err := q.DeleteEventProgrammeAudiences(ctx, eventID); err != nil {
			return err
		}
		if err := q.DeleteEventTeamAudiences(ctx, eventID); err != nil {
			return err
		}
		for _, id := range form.ProgrammeIDs {
			if err := q.AddEventAudience(ctx, dbgen.AddEventAudienceParams{EventID: eventID, ProgrammeID: id}); err != nil {
				return err
			}
		}
		for _, id := range form.TeamIDs {
			if err := q.AddEventTeamAudience(ctx, dbgen.AddEventTeamAudienceParams{EventID: eventID, TeamID: id}); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		latest, getErr := h.Store.GetEventForEdit(ctx, eventID)
		if getErr != nil {
			h.System.InternalError(w, r)
			return
		}
		latestForm, getErr := h.eventFormFromRecord(ctx, latest)
		if getErr != nil {
			h.System.InternalError(w, r)
			return
		}
		h.renderEdit(w, r, http.StatusConflict, eventID, latestForm, "O evento foi alterado entretanto ou já não está ativo. Reveja os valores atuais.", "", validation.FieldErrors{})
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Evento atualizado.")
	httpx.Redirect(w, r, "/admin/eventos/"+eventID.String(), http.StatusSeeOther)
}

func (h Events) Cancel(w http.ResponseWriter, r *http.Request) {
	eventID, ok := h.eventID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido.", http.StatusBadRequest)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), eventQueryTimeout)
	defer cancel()
	current, err := h.Store.GetEventForEdit(ctx, eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !h.canManageEvent(ctx, user, eventID, w, r) {
		return
	}
	reason := strings.TrimSpace(r.PostForm.Get("cancellation_reason"))
	errs := validation.FieldErrors{}
	if utf8.RuneCountInString(reason) < 2 || utf8.RuneCountInString(reason) > 500 {
		errs.Add("cancellation_reason", "O motivo deve ter entre 2 e 500 caracteres.")
	}
	if r.PostForm.Get("confirm_cancellation") != "yes" {
		errs.Add("confirm_cancellation", "Confirme que pretende cancelar o evento.")
	}
	expected, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(r.PostForm.Get("expected_updated_at")))
	if parseErr != nil {
		errs.Add("state", "O formulário deixou de ser válido. Atualize a página.")
	}
	if !errs.Empty() {
		form, getErr := h.eventFormFromRecord(ctx, current)
		if getErr != nil {
			h.System.InternalError(w, r)
			return
		}
		h.renderEdit(w, r, http.StatusUnprocessableEntity, eventID, form, "", reason, errs)
		return
	}
	now := h.now()
	_, err = h.Store.CancelEvent(ctx, dbgen.CancelEventParams{CancelledAt: pgtype.Timestamptz{Time: now, Valid: true}, CancelledByID: &user.ID, CancellationReason: &reason, ID: eventID, ExpectedUpdatedAt: pgtype.Timestamptz{Time: expected, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		latest, getErr := h.Store.GetEventForEdit(ctx, eventID)
		if getErr != nil {
			h.System.InternalError(w, r)
			return
		}
		form, getErr := h.eventFormFromRecord(ctx, latest)
		if getErr != nil {
			h.System.InternalError(w, r)
			return
		}
		h.renderEdit(w, r, http.StatusConflict, eventID, form, "O evento foi alterado entretanto, já começou ou já foi cancelado.", reason, validation.FieldErrors{})
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Evento cancelado.")
	httpx.Redirect(w, r, "/admin/eventos/"+eventID.String(), http.StatusSeeOther)
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
	management := strings.HasPrefix(r.URL.Path, "/admin/")
	if management {
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
		responsePage := eventsPageNumber(r.URL.Query().Get("response_page"))
		responses, err := h.Store.ListEventResponsesForAdmin(ctx, dbgen.ListEventResponsesForAdminParams{
			EventID:   eventID,
			RowLimit:  eventResponsesPageSize + 1,
			RowOffset: int32((responsePage - 1) * eventResponsesPageSize),
		})
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		page = h.adminDetailPage(event, responses, responsePage)
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
	documents, err := h.Store.ListCompetitionDocumentsForEvent(ctx, &eventID)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	for _, document := range documents {
		page.Documents = append(page.Documents, pages.EventDocument{Title: document.Title, URL: document.Url, Source: document.Source, ReviewedOn: document.ReviewedOn.Time.Format("02/01/2006")})
	}
	basePath, pageLabel := "/events", "Evento"
	if management {
		basePath, pageLabel = "/admin/eventos", "Evento"
	}
	page.Meta = h.meta(r, user, basePath, pageLabel)
	page.Meta.CurrentPath = r.URL.Path
	page.Meta.PageLabel = page.Title
	page.Meta.Title = page.Title + " | MyCFC"
	page.Meta.Breadcrumbs = []components.NavigationItem{{Label: "Eventos", Path: basePath}}
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
	respondable, err := h.Store.GetRespondableEvent(ctx, dbgen.GetRespondableEventParams{SubjectUserID: subjectID, EventID: eventID, ActorUserID: user.ID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			h.System.Forbidden(w, r)
		} else {
			h.System.InternalError(w, r)
		}
		return
	}
	if respondable.Status != "ACTIVE" {
		http.Error(w, "O evento foi cancelado e já não aceita respostas.", http.StatusConflict)
		return
	}
	err = db.WithinTx(ctx, h.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		queries := dbgen.New(tx)
		event, err := queries.GetEventForResponse(ctx, eventID) // serializes capacity decisions for this event.
		if err != nil {
			return err
		}
		if _, err := queries.GetRespondableEvent(ctx, dbgen.GetRespondableEventParams{SubjectUserID: subjectID, EventID: eventID, ActorUserID: user.ID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errEventNotRespondable
			}
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
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "O evento já não está disponível para respostas.", http.StatusConflict)
		return
	}
	if errors.Is(err, errEventNotRespondable) {
		h.System.Forbidden(w, r)
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
		locked, err := queries.GetEventForResponse(ctx, eventID)
		if err != nil {
			return err
		}
		if !user.IsAdmin {
			allowed, err := queries.CanCoachManageEvent(ctx, dbgen.CanCoachManageEventParams{EventID: eventID, UserID: user.ID})
			if err != nil {
				return err
			}
			if !allowed {
				return errEventNotManageable
			}
		}
		if confirm {
			if locked.Capacity != nil {
				count, err := queries.CountGoingEventResponses(ctx, eventID)
				if err != nil {
					return err
				}
				if count >= int64(*locked.Capacity) {
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
		if h.now().Before(locked.StartsAt.Time) {
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
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "O evento já não está ativo.", http.StatusConflict)
		return
	}
	if errors.Is(err, errEventNotManageable) {
		h.System.Forbidden(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	httpx.Redirect(w, r, "/admin/eventos/"+eventID.String(), http.StatusSeeOther)
}

var (
	errResponseDeadline    = errors.New("event response deadline")
	errEventFull           = errors.New("event full")
	errInvalidEventState   = errors.New("invalid event state")
	errEventNotStarted     = errors.New("event has not started")
	errEventNotRespondable = errors.New("event not respondable by actor")
	errEventNotManageable  = errors.New("event not manageable by actor")
)

func (h Events) validateEvent(r *http.Request) eventForm {
	eventType := strings.TrimSpace(r.PostForm.Get("event_type"))
	if eventType == "" {
		eventType = "GENERAL"
	}
	form := eventForm{Title: strings.TrimSpace(r.PostForm.Get("title")), Description: strings.TrimSpace(r.PostForm.Get("description")), EventType: eventType, StartsAt: strings.TrimSpace(r.PostForm.Get("starts_at")), EndsAt: strings.TrimSpace(r.PostForm.Get("ends_at")), Deadline: strings.TrimSpace(r.PostForm.Get("response_deadline")), Capacity: strings.TrimSpace(r.PostForm.Get("capacity")), DocumentTitle: strings.TrimSpace(r.PostForm.Get("document_title")), DocumentURL: strings.TrimSpace(r.PostForm.Get("document_url")), DocumentSource: strings.TrimSpace(r.PostForm.Get("document_source")), DocumentReviewedOn: strings.TrimSpace(r.PostForm.Get("document_reviewed_on")), Errors: validation.FieldErrors{}}
	if form.EventType != "GENERAL" && form.EventType != "COMPETITION" {
		form.Errors.Add("event_type", "Selecione um tipo de evento válido.")
	}
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
	documentRequested := form.DocumentTitle != "" || form.DocumentURL != "" || form.DocumentSource != "" || form.DocumentReviewedOn != ""
	if documentRequested {
		if form.EventType != "COMPETITION" {
			form.Errors.Add("document", "Os documentos oficiais só podem ser publicados em eventos de competição.")
		}
		if !validTrainingText(form.DocumentTitle, 2, 180) {
			form.Errors.Add("document_title", "O título deve ter entre 2 e 180 caracteres.")
		}
		if !validDocumentURL(form.DocumentURL) {
			form.Errors.Add("document_url", "Introduza uma ligação HTTPS válida.")
		}
		if !validTrainingText(form.DocumentSource, 2, 180) {
			form.Errors.Add("document_source", "A fonte deve ter entre 2 e 180 caracteres.")
		}
		reviewed, err := time.Parse("2006-01-02", form.DocumentReviewedOn)
		if err != nil || reviewed.After(h.now().In(h.location())) {
			form.Errors.Add("document_reviewed_on", "Introduza uma data de revisão válida.")
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

func (h Events) eventID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return uuid.Nil, false
	}
	return id, true
}

func (h Events) canManageEvent(ctx context.Context, user CurrentUser, eventID uuid.UUID, w http.ResponseWriter, r *http.Request) bool {
	if user.IsAdmin {
		return true
	}
	allowed, err := h.Store.CanCoachManageEvent(ctx, dbgen.CanCoachManageEventParams{EventID: eventID, UserID: user.ID})
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

func (h Events) validateEventAudience(ctx context.Context, user CurrentUser, form *eventForm) {
	programmes, err := h.Store.ListProgrammes(ctx)
	if err != nil {
		form.Errors.Add("audience", "Não foi possível validar os destinatários.")
		return
	}
	knownProgrammes := make(map[uuid.UUID]bool, len(programmes))
	for _, programme := range programmes {
		knownProgrammes[programme.ID] = true
	}
	for _, id := range form.ProgrammeIDs {
		if !knownProgrammes[id] {
			form.Errors.Add("programme_id", "Selecione programas válidos.")
		}
	}
	teams, err := h.Store.ListTeamsForEventAuthoring(ctx)
	if err != nil {
		form.Errors.Add("audience", "Não foi possível validar os destinatários.")
		return
	}
	knownTeams := make(map[uuid.UUID]uuid.UUID, len(teams))
	for _, team := range teams {
		knownTeams[team.ID] = team.ProgrammeID
	}
	for _, id := range form.TeamIDs {
		if _, ok := knownTeams[id]; !ok {
			form.Errors.Add("team_id", "Selecione equipas válidas.")
		}
	}
	if !canAuthorEvent(user, form.ProgrammeIDs, form.TeamIDs, knownTeams) {
		form.Errors.Add("audience", "Selecione apenas destinatários que possa gerir.")
	}
}

func (h Events) eventFormFromRecord(ctx context.Context, event dbgen.GetEventForEditRow) (eventForm, error) {
	programmes, err := h.Store.ListEventProgrammeAudienceIDs(ctx, event.ID)
	if err != nil {
		return eventForm{}, err
	}
	teams, err := h.Store.ListEventTeamAudienceIDs(ctx, event.ID)
	if err != nil {
		return eventForm{}, err
	}
	form := eventForm{
		Title: event.Title, Description: event.Description, EventType: event.EventType,
		StartsAt: event.StartsAt.Time.In(h.location()).Format("2006-01-02T15:04"),
		EndsAt:   event.EndsAt.Time.In(h.location()).Format("2006-01-02T15:04"),
		Capacity: eventCapacityText(event.Capacity), ProgrammeIDs: programmes, TeamIDs: teams,
		ExpectedUpdatedAt: event.UpdatedAt.Time.Format(time.RFC3339Nano), Editing: true,
		AudienceLocked: event.HasResponses, HasDocument: event.HasDocument, Errors: validation.FieldErrors{},
	}
	if event.ResponseDeadline.Valid {
		form.Deadline = event.ResponseDeadline.Time.In(h.location()).Format("2006-01-02T15:04")
	}
	return form, nil
}

func (h Events) renderEdit(w http.ResponseWriter, r *http.Request, status int, eventID uuid.UUID, form eventForm, conflict, cancellationReason string, cancellationErrors validation.FieldErrors) {
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), eventQueryTimeout)
	defer cancel()
	pageForm := pages.EventForm{
		ID: eventID.String(), Title: form.Title, Description: form.Description, EventType: form.EventType, StartsAt: form.StartsAt,
		EndsAt: form.EndsAt, Deadline: form.Deadline, Capacity: form.Capacity, ExpectedUpdatedAt: form.ExpectedUpdatedAt,
		Editing: true, AudienceLocked: form.AudienceLocked, HasDocument: form.HasDocument, Errors: form.Errors,
		CSRFField: templ.Raw(string(csrf.TemplateField(r))),
	}
	selectedProgrammes := make(map[uuid.UUID]bool, len(form.ProgrammeIDs))
	for _, id := range form.ProgrammeIDs {
		selectedProgrammes[id] = true
	}
	programmes, err := h.Store.ListProgrammes(ctx)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	for _, programme := range programmes {
		if user.IsAdmin || user.CoachProgrammeIDs[programme.ID] {
			pageForm.Programmes = append(pageForm.Programmes, pages.EventProgramme{ID: programme.ID.String(), Name: programme.NamePt, Selected: selectedProgrammes[programme.ID]})
		}
	}
	selectedTeams := make(map[uuid.UUID]bool, len(form.TeamIDs))
	for _, id := range form.TeamIDs {
		selectedTeams[id] = true
	}
	teams, err := h.Store.ListTeamsForEventAuthoring(ctx)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	for _, team := range teams {
		if user.IsAdmin || user.CoachTeamIDs[team.ID] || user.CoachProgrammeIDs[team.ProgrammeID] {
			pageForm.Teams = append(pageForm.Teams, pages.EventTeam{ID: team.ID.String(), Name: team.Name, Selected: selectedTeams[team.ID]})
		}
	}
	page := pages.EventEditPage{
		Meta: h.meta(r, user, "/admin/eventos", "Editar evento"), EventID: eventID.String(), Form: pageForm,
		Conflict: conflict, CancellationReason: cancellationReason, CancellationErrors: cancellationErrors,
	}
	page.Meta.CurrentPath = r.URL.Path
	page.Meta.Breadcrumbs = []components.NavigationItem{{Label: "Eventos", Path: "/admin/eventos"}, {Label: form.Title, Path: "/admin/eventos/" + eventID.String()}}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.EventEdit(page).Render(r.Context(), w)
}

func eventDeadline(value string, location *time.Location) pgtype.Timestamptz {
	if value == "" {
		return pgtype.Timestamptz{}
	}
	parsed, _ := time.ParseInLocation("2006-01-02T15:04", value, location)
	return pgtype.Timestamptz{Time: parsed, Valid: true}
}

func eventCapacity(value string) (*int32, bool) {
	if value == "" {
		return nil, true
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed < 1 {
		return nil, false
	}
	result := int32(parsed)
	return &result, true
}

func eventCapacityText(value *int32) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(int64(*value), 10)
}

func sameUUIDSet(left, right []uuid.UUID) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[uuid.UUID]bool, len(left))
	for _, id := range left {
		values[id] = true
	}
	for _, id := range right {
		if !values[id] {
			return false
		}
	}
	return true
}

func (h Events) flash(r *http.Request, message string) {
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "events_flash", message)
	}
}

func (h Events) renderIndex(w http.ResponseWriter, r *http.Request, status int, form eventForm) {
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), eventQueryTimeout)
	defer cancel()
	management := strings.HasPrefix(r.URL.Path, "/admin/")
	page := pages.EventsPage{Management: management, CanManageEvents: user.IsAdmin || user.CanManageEvents, Form: pages.EventForm{Title: form.Title, Description: form.Description, EventType: form.EventType, StartsAt: form.StartsAt, EndsAt: form.EndsAt, Deadline: form.Deadline, Capacity: form.Capacity, DocumentTitle: form.DocumentTitle, DocumentURL: form.DocumentURL, DocumentSource: form.DocumentSource, DocumentReviewedOn: form.DocumentReviewedOn, Errors: form.Errors, CSRFField: templ.Raw(string(csrf.TemplateField(r)))}}
	if page.Form.EventType == "" {
		page.Form.EventType = "GENERAL"
	}
	if h.Sessions != nil {
		page.Success = h.Sessions.PopString(r.Context(), "events_flash")
	}
	if management {
		if user.IsAdmin {
			pageNumber := eventsPageNumber(r.URL.Query().Get("page"))
			items, err := h.Store.ListEventsForAdmin(ctx, dbgen.ListEventsForAdminParams{RowLimit: eventsPageSize + 1, RowOffset: int32((pageNumber - 1) * eventsPageSize)})
			if err != nil {
				h.System.InternalError(w, r)
				return
			}
			if pageNumber > 1 {
				page.PreviousURL = managedEventsPageURL(pageNumber - 1)
			}
			if len(items) > eventsPageSize {
				page.NextURL = managedEventsPageURL(pageNumber + 1)
				items = items[:eventsPageSize]
			}
			calendarEntries := make([]calendarEntry, 0, len(items))
			for _, item := range items {
				page.Events = append(page.Events, managedEventItem(item.ID, item.Title, item.EventType, item.StartsAt.Time, item.EndsAt.Time, item.Capacity, item.Status, item.CancellationReason, item.GoingCount, h))
				calendarEntries = append(calendarEntries, eventCalendarEntry(item.ID, item.Title, item.EventType, item.Status, item.StartsAt.Time, true))
			}
			page.Calendar = basicCalendarMonth(calendarEntries, h.now(), h.location())
		} else {
			items, err := h.Store.ListEventsForCoach(ctx, dbgen.ListEventsForCoachParams{UserID: user.ID, RowLimit: 100})
			if err != nil {
				h.System.InternalError(w, r)
				return
			}
			calendarEntries := make([]calendarEntry, 0, len(items))
			for _, item := range items {
				page.Events = append(page.Events, managedEventItem(item.ID, item.Title, item.EventType, item.StartsAt.Time, item.EndsAt.Time, item.Capacity, item.Status, item.CancellationReason, item.GoingCount, h))
				calendarEntries = append(calendarEntries, eventCalendarEntry(item.ID, item.Title, item.EventType, item.Status, item.StartsAt.Time, true))
			}
			page.Calendar = basicCalendarMonth(calendarEntries, h.now(), h.location())
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
		calendarEntries := make([]calendarEntry, 0, len(items))
		for _, item := range items {
			status := eventStatus(item.ResponseStatus)
			if item.Status == "CANCELLED" {
				status = "Cancelado"
			}
			page.Events = append(page.Events, pages.EventItem{ID: item.ID.String(), Title: item.Title, Type: eventTypeLabel(item.EventType), When: h.dateRange(item.StartsAt.Time, item.EndsAt.Time), Status: status, Capacity: h.capacity(item.Capacity), Cancelled: item.Status == "CANCELLED", CancellationReason: stringValue(item.CancellationReason)})
			calendarEntries = append(calendarEntries, eventCalendarEntry(item.ID, item.Title, item.EventType, item.Status, item.StartsAt.Time, false))
		}
		page.Calendar = basicCalendarMonth(calendarEntries, h.now(), h.location())
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
			page.Documents = append(page.Documents, pages.EventDocument{Title: document.Title, URL: document.Url, Source: document.Source, ReviewedOn: document.ReviewedOn.Time.Format("02/01/2006"), Context: context})
		}
	}
	currentPath, area := "/events", "Eventos"
	if management {
		currentPath, area = "/admin/eventos", "Eventos"
	}
	page.Meta = h.meta(r, user, currentPath, area)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.Events(page).Render(r.Context(), w)
}

func eventsPageNumber(value string) int {
	page, err := strconv.Atoi(value)
	if err != nil || page < 1 || page > 10000 {
		return 1
	}
	return page
}

func eventsPageURL(page int) string        { return "/events?page=" + strconv.Itoa(page) }
func managedEventsPageURL(page int) string { return "/admin/eventos?page=" + strconv.Itoa(page) }

func eventResponsesPageURL(eventID uuid.UUID, page int) string {
	return "/events/" + eventID.String() + "?response_page=" + strconv.Itoa(page)
}

func managedEventResponsesPageURL(eventID uuid.UUID, page int) string {
	return "/admin/eventos/" + eventID.String() + "?response_page=" + strconv.Itoa(page)
}

func (h Events) memberDetailPage(event dbgen.GetEventDetailForMemberRow, dependents []dbgen.ListDependentsByGuardianRow) pages.EventDetailPage {
	page := pages.EventDetailPage{ID: event.ID.String(), Title: event.Title, Type: eventTypeLabel(event.EventType), Description: event.Description, When: h.dateRange(event.StartsAt.Time, event.EndsAt.Time), Deadline: h.deadline(event.ResponseDeadline), Capacity: h.capacity(event.Capacity), Status: eventStatus(event.ResponseStatus), Cancelled: event.Status == "CANCELLED", CancellationReason: stringValue(event.CancellationReason)}
	if page.Cancelled {
		page.Status = "Cancelado"
	}
	if event.ResponseDeadline.Valid && h.now().After(event.ResponseDeadline.Time) {
		page.Status = "Fora do prazo"
	}
	for _, dependent := range dependents {
		page.Dependents = append(page.Dependents, pages.EventDependent{ID: dependent.ID.String(), Name: dependent.Name})
	}
	return page
}
func (h Events) adminDetailPage(event dbgen.GetEventDetailForAdminRow, responses []dbgen.ListEventResponsesForAdminRow, responsePage int) pages.EventDetailPage {
	page := pages.EventDetailPage{CanManageEvents: true, ID: event.ID.String(), Title: event.Title, Type: eventTypeLabel(event.EventType), Description: event.Description, When: h.dateRange(event.StartsAt.Time, event.EndsAt.Time), Deadline: h.deadline(event.ResponseDeadline), Capacity: h.capacity(event.Capacity), Cancelled: event.Status == "CANCELLED", CancellationReason: stringValue(event.CancellationReason), Editable: event.Status == "ACTIVE" && event.StartsAt.Time.After(h.now())}
	if event.CancelledAt.Valid {
		page.CancelledAt = event.CancelledAt.Time.In(h.location()).Format("02/01/2006 15:04")
	}
	page.CancelledBy = stringValue(event.CancelledByName)
	if responsePage > 1 {
		page.ResponsesPreviousURL = managedEventResponsesPageURL(event.ID, responsePage-1)
	}
	if len(responses) > eventResponsesPageSize {
		page.ResponsesNextURL = managedEventResponsesPageURL(event.ID, responsePage+1)
		responses = responses[:eventResponsesPageSize]
	}
	for _, response := range responses {
		checked := ""
		if response.CheckedInAt.Valid {
			checked = response.CheckedInAt.Time.In(h.location()).Format("02/01/2006 15:04")
		}
		page.Responses = append(page.Responses, pages.EventResponse{UserID: response.UserID.String(), Name: response.UserName, Status: eventStatus(response.Status), RespondedAt: response.RespondedAt.Time.In(h.location()).Format("02/01/2006 15:04"), CheckedInAt: checked})
	}
	return page
}

func managedEventItem(id uuid.UUID, title, eventType string, startsAt, endsAt time.Time, capacity *int32, status string, reason *string, going int64, h Events) pages.EventItem {
	label := "Confirmados: " + strconv.FormatInt(going, 10)
	if status == "CANCELLED" {
		label = "Cancelado"
	}
	return pages.EventItem{ID: id.String(), Title: title, Type: eventTypeLabel(eventType), When: h.dateRange(startsAt, endsAt), Status: label, Capacity: h.capacity(capacity), Cancelled: status == "CANCELLED", CancellationReason: stringValue(reason)}
}

func eventCalendarEntry(id uuid.UUID, title, eventType, status string, startsAt time.Time, management bool) calendarEntry {
	base := "/events/"
	if management {
		base = "/admin/eventos/"
	}
	kind := eventTypeLabel(eventType)
	if status == "CANCELLED" {
		title = "Cancelado: " + title
		kind = "Cancelado"
	}
	return calendarEntry{Title: title, URL: base + id.String(), Kind: kind, StartsAt: startsAt}
}
func (h Events) meta(r *http.Request, user CurrentUser, path, title string) components.PageMeta {
	meta := h.PageMeta
	meta.Title = title + " | MyCFC"
	meta.CurrentPath = path
	meta.CurrentUserName = user.Name
	meta.CurrentUserID = user.ID.String()
	meta.EmailVerificationPending = !user.IsDependent && !user.EmailVerified
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

func eventTypeLabel(eventType string) string {
	if eventType == "COMPETITION" {
		return "Competição"
	}
	return "Geral"
}
