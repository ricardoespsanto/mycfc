package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (h TrainingPlacements) actor(w http.ResponseWriter, r *http.Request) (CurrentUser, bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	u, ok := CurrentUserFromContext(r.Context())
	if !ok || h.Store == nil {
		h.System.NotFound(w, r)
		return u, false
	}
	return u, true
}
func placementSingleID(v url.Values, key string, optional bool) (uuid.UUID, error) {
	if len(v[key]) > 1 {
		return uuid.Nil, errTrainingPlacementInvalid
	}
	s := v.Get(key)
	if optional && s == "" {
		return uuid.Nil, nil
	}
	id, e := uuid.Parse(s)
	if e != nil || id == uuid.Nil || id.String() != s {
		return uuid.Nil, errTrainingPlacementInvalid
	}
	return id, nil
}
func placementContextIDs(v url.Values) (uuid.UUID, uuid.UUID, error) {
	g, e := placementSingleID(v, "group_id", false)
	if e != nil {
		return uuid.Nil, uuid.Nil, e
	}
	p, e := placementSingleID(v, "person_id", false)
	return g, p, e
}
func placementContextAllowsSeason(c trainingPlacementPageContext, id uuid.UUID) bool {
	for _, s := range c.Seasons {
		if s.ID == id {
			return true
		}
	}
	return false
}
func placementEditableValues(v url.Values) url.Values {
	out := cloneFocusedValues(v)
	for _, key := range []string{"gorilla.csrf.Token", "proposal", "review_token"} {
		delete(out, key)
	}
	if out.Get("change_id") == "" {
		delete(out, "change_id")
	}
	if out.Get("cancellation_id") == "" {
		delete(out, "cancellation_id")
	}
	return out
}
func (h TrainingPlacements) fail(w http.ResponseWriter, r *http.Request, e error) {
	if errors.Is(e, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
	} else if errors.Is(e, errTrainingPlacementUnavailable) {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "Esta tarefa está temporariamente indisponível.", http.StatusServiceUnavailable)
	} else {
		h.System.InternalError(w, r)
	}
}
func (h TrainingPlacements) Index(w http.ResponseWriter, r *http.Request) {
	u, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trainingQueryTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	v := r.URL.Query()
	group, person, e := placementContextIDs(v)
	if e != nil {
		h.System.NotFound(w, r)
		return
	}
	context, e := h.Store.PlacementContext(ctx, u.ID, group)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	raw := url.Values{"group_id": {group.String()}, "person_id": {person.String()}, "command_id": {uuid.NewString()}, "head_revision": {"0"}, "kind": {v.Get("kind")}, "season_id": {v.Get("season_id")}, "effective_on": {v.Get("effective_on")}, "source_placement_id": {v.Get("source_placement_id")}, "target_group_id": {v.Get("target_group_id")}, "temporary_until": {v.Get("temporary_until")}}
	p := h.page(r, u, context, raw)
	if change, e := placementSingleID(v, "change_id", true); e != nil {
		h.System.NotFound(w, r)
		return
	} else if change != uuid.Nil {
		h.cancelEditor(ctx, w, r, u, context, person, change)
		return
	}
	season, e := placementSingleID(v, "season_id", true)
	if e != nil {
		p.Form.Errors.Add("season_id", "Selecione uma época válida.")
		h.render(w, r, http.StatusUnprocessableEntity, p)
		return
	}
	if season == uuid.Nil || v.Get("effective_on") == "" {
		p.Mode = "select-date"
		p.Title = "Escolher época e data"
		h.render(w, r, http.StatusOK, p)
		return
	}
	if !placementContextAllowsSeason(context, season) {
		p.Form.Errors.Add("season_id", "A época selecionada não está disponível neste grupo.")
	}
	if !validWorkspaceDate(v.Get("effective_on")) {
		p.Form.Errors.Add("effective_on", "Indique uma data válida.")
	}
	if !p.Form.Errors.Empty() {
		h.render(w, r, http.StatusUnprocessableEntity, p)
		return
	}
	view, e := h.Store.PlacementView(ctx, u.ID, group, person, season, v.Get("effective_on"))
	if e != nil {
		h.fail(w, r, e)
		return
	}
	placementFillView(&p, view)
	p.Form.HeadRevision = strconv.FormatInt(view.Revision, 10)
	if h.Sessions != nil {
		p.Outcome = h.Sessions.PopString(ctx, "training_placements_flash")
	}
	h.render(w, r, http.StatusOK, p)
}
func (h TrainingPlacements) writeRequest(w http.ResponseWriter, r *http.Request) (*http.Request, context.CancelFunc, bool) {
	deadline, ok := httpx.PublicationDeadline(r.Context(), h.HTTPWriteTimeout)
	if !ok {
		h.System.RequestRejected(w, r)
		return r, func() {}, false
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	r = r.WithContext(ctx)
	r.Body = http.MaxBytesReader(w, r.Body, httpx.TrainingPlacementWriteMaxBytes)
	if e := r.ParseForm(); e != nil {
		cancel()
		h.System.RequestRejected(w, r)
		return r, func() {}, false
	}
	return r, cancel, true
}
func (h TrainingPlacements) Preview(w http.ResponseWriter, r *http.Request) {
	u, ok := h.actor(w, r)
	if !ok {
		return
	}
	r, cancel, ok := h.writeRequest(w, r)
	if !ok {
		return
	}
	defer cancel()
	v := placementEditableValues(r.PostForm)
	group, person, e := placementContextIDs(v)
	if e != nil {
		h.System.NotFound(w, r)
		return
	}
	c, e := h.Store.PlacementContext(r.Context(), u.ID, group)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	cmd, raw, errs := parseTrainingPlacementForm(v, trainingPlacementContext{ActorID: u.ID, SubjectID: person, ProgrammeID: c.ProgrammeID, GroupID: group})
	p := h.page(r, u, c, raw)
	p.Form.Errors = errs
	if !placementContextAllowsSeason(c, cmd.SeasonID) {
		p.Form.Errors.Add("season_id", "A época selecionada não está disponível neste grupo.")
	}
	if !p.Form.Errors.Empty() {
		h.fillEditableView(r, u, &p, group, person, cmd.SeasonID, cmd.EffectiveOn)
		h.render(w, r, http.StatusUnprocessableEntity, p)
		return
	}
	// This explicit review action may reload the head. Confirmation never does.
	read, e := h.Store.ReadPlacement(r.Context(), u.ID, cmd, true)
	if e != nil {
		h.recoverApply(w, r, u, c, cmd, raw, "", "", e)
		return
	}
	cmd.ExpectedRevision = read.revision()
	p.Form = placementForm(cmd.values())
	placementFillView(&p, placementReadView(read))
	proposal, e := read.proposal(u.ID, cmd)
	if e != nil {
		h.recoverApply(w, r, u, c, cmd, raw, "", "", e)
		return
	}
	encoded, token, e := focusedWorkoutReviews.signPlacement(proposal, time.Now())
	if e != nil {
		h.recoverApply(w, r, u, c, cmd, raw, "", "", e)
		return
	}
	placementReviewPage(&p, read, proposal, encoded, token)
	h.render(w, r, http.StatusOK, p)
}
func (h TrainingPlacements) fillEditableView(r *http.Request, u CurrentUser, p *pages.TrainingPlacementsPage, group, person, season uuid.UUID, date string) {
	if season == uuid.Nil || !validWorkspaceDate(date) {
		return
	}
	view, e := h.Store.PlacementView(r.Context(), u.ID, group, person, season, date)
	if e == nil {
		placementFillView(p, view)
	}
}
func placementProofTransport(v url.Values) (string, string, error) {
	for k, values := range v {
		if (k != "gorilla.csrf.Token" && k != "proposal" && k != "review_token") || len(values) != 1 {
			return "", "", errTrainingPlacementInvalid
		}
	}
	if v.Get("proposal") == "" || v.Get("review_token") == "" {
		return "", "", errTrainingPlacementInvalid
	}
	return v.Get("proposal"), v.Get("review_token"), nil
}
func verifyPlacementTransport(raw, token string, actor uuid.UUID, now time.Time) (trainingPlacementReviewed, error) {
	var p trainingPlacementReviewPayload
	if _, e := focusedWorkoutReviews.verifyNative(raw, token, trainingPlacementReviewPurpose, now); e != nil {
		return trainingPlacementReviewed{}, errTrainingPlacementInvalid
	}
	if focusedStrictJSON([]byte(raw), &p) != nil {
		return trainingPlacementReviewed{}, errTrainingPlacementInvalid
	}
	return focusedWorkoutReviews.verifyPlacement(raw, token, actor, p.Command.ContextGroupID, p.Command.SubjectID, now)
}
func (h TrainingPlacements) Confirm(w http.ResponseWriter, r *http.Request) {
	u, ok := h.actor(w, r)
	if !ok {
		return
	}
	r, cancel, ok := h.writeRequest(w, r)
	if !ok {
		return
	}
	defer cancel()
	raw, token, e := placementProofTransport(r.PostForm)
	if e != nil {
		h.invalidProof(w, r, u, raw, token)
		return
	}
	reviewed, e := verifyPlacementTransport(raw, token, u.ID, time.Now())
	if e != nil {
		h.invalidProof(w, r, u, raw, token)
		return
	}
	cmd := reviewed.Payload.Command
	c, e := h.Store.PlacementContext(r.Context(), u.ID, cmd.ContextGroupID)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	if c.ProgrammeID != cmd.ProgrammeID {
		h.System.NotFound(w, r)
		return
	}
	_, e = h.Store.ConfirmPlacement(r.Context(), reviewed)
	if e != nil {
		h.recoverApply(w, r, u, c, cmd, reviewed.Payload.Values, raw, token, e)
		return
	}
	h.saved(w, r, cmd.ContextGroupID, cmd.SubjectID, cmd.SeasonID, cmd.EffectiveOn)
}

// Editor POST is a native back operation. It rechecks scope and retains the
// original command/head/raw/proof; it never writes or silently signs a new review.
func (h TrainingPlacements) Editor(w http.ResponseWriter, r *http.Request) {
	u, ok := h.actor(w, r)
	if !ok {
		return
	}
	r, cancel, ok := h.writeRequest(w, r)
	if !ok {
		return
	}
	defer cancel()
	raw, token := r.PostForm.Get("proposal"), r.PostForm.Get("review_token")
	if r.PostForm.Get("change_id") != "" {
		h.cancelBack(w, r, u, raw, token)
		return
	}
	reviewed, e := verifyPlacementTransport(raw, token, u.ID, time.Now())
	if e != nil {
		h.invalidProof(w, r, u, raw, token)
		return
	}
	cmd := reviewed.Payload.Command
	v := placementEditableValues(r.PostForm)
	if v.Get("command_id") != cmd.ID.String() || v.Get("group_id") != cmd.ContextGroupID.String() || v.Get("person_id") != cmd.SubjectID.String() || v.Get("head_revision") != strconv.FormatInt(cmd.ExpectedRevision, 10) {
		h.invalidProof(w, r, u, raw, token)
		return
	}
	c, e := h.Store.PlacementContext(r.Context(), u.ID, cmd.ContextGroupID)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	read, e := h.Store.ReadPlacement(r.Context(), u.ID, cmd, true)
	if errors.Is(e, pgx.ErrNoRows) || errors.Is(e, errTrainingPlacementUnavailable) {
		h.fail(w, r, e)
		return
	}
	p := h.page(r, u, c, v)
	p.Review = &pages.TrainingPlacementReview{Proposal: raw, ReviewToken: token}
	if e == nil {
		placementFillView(&p, placementReadView(read))
		if read.revision() != cmd.ExpectedRevision {
			p.Mode = "recovery"
			p.Recovery = pages.TrainingPlacementRecovery{Active: true, Message: "O contexto mudou desde a revisão. Os seus dados foram mantidos; peça uma nova pré-visualização antes de confirmar.", ReloadURL: p.NewTaskURL}
		}
	} else {
		p.Mode = "recovery"
		p.Recovery = pages.TrainingPlacementRecovery{Active: true, Message: "O contexto mudou. Reveja os dados antes de pedir uma nova pré-visualização.", ReloadURL: p.NewTaskURL}
	}
	h.render(w, r, http.StatusOK, p)
}
func (h TrainingPlacements) invalidProof(w http.ResponseWriter, r *http.Request, u CurrentUser, raw, token string) {
	p := h.page(r, u, trainingPlacementPageContext{}, url.Values{})
	p.ReturnURL = trainingGroupsPath
	p.NewTaskURL = trainingGroupsPath
	p.PreviewURL = ""
	p.EditorURL = ""
	p.CancelPreviewURL = ""
	p.Mode = "recovery"
	p.Recovery = pages.TrainingPlacementRecovery{Active: true, Message: "Reabra a pessoa e reveja a alteração.", ReloadURL: trainingGroupsPath}
	p.Form.Errors = validation.FieldErrors{"form": "A revisão não é válida ou expirou. Reabra a pessoa e reveja a alteração."}
	p.Review = &pages.TrainingPlacementReview{Proposal: raw, ReviewToken: token}
	h.render(w, r, http.StatusUnprocessableEntity, p)
}
func (h TrainingPlacements) recoverApply(w http.ResponseWriter, r *http.Request, u CurrentUser, c trainingPlacementPageContext, cmd trainingPlacementCommand, raw url.Values, proposal, token string, e error) {
	if errors.Is(e, db.ErrCommitOutcomeUnknown) {
		h.unknownOutcome(w, r, u, raw, proposal, token)
		return
	}

	if errors.Is(e, pgx.ErrNoRows) || errors.Is(e, errTrainingPlacementUnavailable) {
		h.fail(w, r, e)
		return
	}
	// Re-authorize the original source and destination before retaining their task.
	read, check := h.Store.ReadPlacement(r.Context(), u.ID, cmd, true)
	if errors.Is(check, pgx.ErrNoRows) || errors.Is(check, errTrainingPlacementUnavailable) {
		h.fail(w, r, check)
		return
	}
	current, checkContext := h.Store.PlacementContext(r.Context(), u.ID, c.Group.ID)
	if checkContext != nil {
		h.fail(w, r, checkContext)
		return
	}
	p := h.page(r, u, current, raw)
	if check == nil {
		placementFillView(&p, placementReadView(read))
	}
	p.Review = &pages.TrainingPlacementReview{Proposal: proposal, ReviewToken: token}
	status := http.StatusUnprocessableEntity
	var field *trainingPlacementFieldFailure
	switch {
	case errors.Is(e, db.ErrCommitOutcomeUnknown):
		status = http.StatusServiceUnavailable
		p.Mode = "recovery"
		p.Recovery = pages.TrainingPlacementRecovery{Active: true, UnknownCommit: true, Message: "Não foi possível confirmar se a alteração ficou guardada. Recarregue o estado atual antes de continuar; este pedido não será repetido automaticamente.", ReloadURL: p.NewTaskURL}
	case errors.Is(e, errTrainingPlacementChanged):
		status = http.StatusConflict
		p.Mode = "recovery"
		p.Recovery = pages.TrainingPlacementRecovery{Active: true, Message: "A colocação mudou. Os seus dados foram mantidos; reveja o estado atual e peça uma nova pré-visualização.", ReloadURL: p.NewTaskURL}
	case errors.As(e, &field):
		p.Form.Errors.Add(field.Field, field.Message)
	case errors.Is(e, errTrainingPlacementLimit):
		p.Form.Errors.Add("form", "Esta alteração excede o limite da operação. Nada foi guardado.")
	case errors.Is(e, errTrainingPlacementInvalid):
		p.Form.Errors.Add("form", "Reveja os dados da alteração antes de continuar.")
	default:
		h.System.InternalError(w, r)
		return
	}
	h.render(w, r, status, p)
}
func (h TrainingPlacements) saved(w http.ResponseWriter, r *http.Request, group, person, season uuid.UUID, date string) {
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "training_placements_flash", "Pedido registado. Consulte o estado atual; as instruções dos grupos precisam de publicação explícita.")
	}
	httpx.Redirect(w, r, placementURL(group, person, season, date), http.StatusSeeOther)
}

// An ambiguous COMMIT is handled before any follow-up database error. Only the
// caller's own raw request/proof is retained if current data cannot be read;
// no cached private person/group labels or "nothing changed" error is emitted.
func (h TrainingPlacements) unknownOutcome(w http.ResponseWriter, r *http.Request, u CurrentUser, raw url.Values, proposal, token string) {
	p := h.page(r, u, trainingPlacementPageContext{}, raw)
	p.ContextGroup = pages.TrainingPlacementGroup{}
	p.Person = pages.TrainingPlacementPerson{ID: raw.Get("person_id")}
	p.ReturnURL = trainingGroupsPath
	p.EditorURL = ""
	p.PreviewURL = ""
	p.ConfirmURL = ""
	p.CancelPreviewURL = ""
	p.CancelConfirmURL = ""
	p.Mode = "recovery"
	group, gErr := placementSingleID(raw, "group_id", false)
	person, pErr := placementSingleID(raw, "person_id", false)
	season, _ := placementSingleID(raw, "season_id", true)
	reload := trainingGroupsPath
	if gErr == nil && pErr == nil {
		reload = placementURL(group, person, season, raw.Get("effective_on"))
	}
	p.Recovery = pages.TrainingPlacementRecovery{Active: true, UnknownCommit: true, Message: "Não foi possível confirmar se o pedido ficou guardado. Recarregue o estado atual antes de continuar; o pedido não será repetido automaticamente.", ReloadURL: reload}
	p.Review = &pages.TrainingPlacementReview{Proposal: proposal, ReviewToken: token}
	h.render(w, r, http.StatusServiceUnavailable, p)
}
