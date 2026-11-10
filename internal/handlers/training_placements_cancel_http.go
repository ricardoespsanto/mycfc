package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func placementCancellationForm(v url.Values, read trainingPlacementCancellationRead) (trainingPlacementCancellation, url.Values, validation.FieldErrors) {
	raw := cloneFocusedValues(v)
	delete(raw, "gorilla.csrf.Token")
	errs := validation.FieldErrors{}
	allowed := map[string]bool{}
	for k := range (trainingPlacementCancellation{}).values() {
		allowed[k] = true
	}
	for k, values := range raw {
		if !allowed[k] || len(values) != 1 {
			errs.Add("form", "O formulário contém campos repetidos ou não reconhecidos.")
		}
	}
	if len(raw.Encode()) > trainingPlacementMaxFormBytes {
		errs.Add("form", "Os dados excedem o limite deste formulário.")
	}
	id := func(k string) uuid.UUID {
		x, e := placementSingleID(raw, k, false)
		if e != nil {
			errs.Add(k, "Recarregue a alteração antes de continuar.")
		}
		return x
	}
	c := trainingPlacementCancellation{ID: id("cancellation_id"), ChangeID: id("change_id"), ContextGroupID: id("group_id"), SubjectID: id("person_id"), ProgrammeID: read.Change.ProgrammeID, SeasonID: read.Change.SeasonID}
	rev, e := strconv.ParseInt(raw.Get("head_revision"), 10, 64)
	if e != nil || rev < 0 || strconv.FormatInt(rev, 10) != raw.Get("head_revision") {
		errs.Add("head_revision", "Recarregue o estado atual antes de continuar.")
	} else {
		c.ExpectedRevision = rev
	}
	if raw.Get("kind") != "CANCEL" || raw.Get("season_id") != c.SeasonID.String() || raw.Get("command_id") != "" || raw.Get("source_placement_id") != "" || raw.Get("target_group_id") != "" || raw.Get("effective_on") != "" || raw.Get("temporary_until") != "" || c.validate() != nil || read.Change.ID != c.ChangeID || read.Change.SubjectUserID == nil || *read.Change.SubjectUserID != c.SubjectID {
		errs.Add("form", "Os dados não correspondem ao cancelamento selecionado.")
	}
	return c, raw, errs
}
func (h TrainingPlacements) cancelEditor(ctx context.Context, w http.ResponseWriter, r *http.Request, u CurrentUser, c trainingPlacementPageContext, person, change uuid.UUID) {
	read, e := h.Store.ReadPlacementCancellation(ctx, u.ID, c.Group.ID, person, change)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	cmd := trainingPlacementCancellation{ID: uuid.New(), ChangeID: change, SubjectID: person, ProgrammeID: read.Change.ProgrammeID, SeasonID: read.Change.SeasonID, ContextGroupID: c.Group.ID, ExpectedRevision: read.Change.Revision}
	if cmd.ProgrammeID != c.ProgrammeID {
		h.System.NotFound(w, r)
		return
	}
	p := h.page(r, u, c, cmd.values())
	p.Title = "Cancelar alteração agendada"
	p.Person.Name = read.PersonName
	p.Timeline = placementTimelineRows(read.Timeline, c.Group.ID, person)
	if !read.CanCancel {
		p.CancelPreviewURL = ""
		p.Recovery = pages.TrainingPlacementRecovery{Active: true, Message: "Esta alteração já é efetiva ou foi alterada. Consulte o estado atual antes de continuar.", ReloadURL: p.NewTaskURL}
	}
	h.render(w, r, http.StatusOK, p)
}
func (h TrainingPlacements) CancelPreview(w http.ResponseWriter, r *http.Request) {
	u, ok := h.actor(w, r)
	if !ok {
		return
	}
	r, cancel, ok := h.writeRequest(w, r)
	if !ok {
		return
	}
	defer cancel()
	v := cloneFocusedValues(r.PostForm)
	group, person, e := placementContextIDs(v)
	if e != nil {
		h.System.NotFound(w, r)
		return
	}
	change, e := placementSingleID(v, "change_id", false)
	if e != nil {
		h.System.NotFound(w, r)
		return
	}
	c, e := h.Store.PlacementContext(r.Context(), u.ID, group)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	read, e := h.Store.ReadPlacementCancellation(r.Context(), u.ID, group, person, change)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	cmd, raw, errs := placementCancellationForm(v, read)
	p := h.page(r, u, c, raw)
	p.Person.Name = read.PersonName
	p.Timeline = placementTimelineRows(read.Timeline, group, person)
	p.Form.Errors = errs
	if !errs.Empty() {
		h.render(w, r, http.StatusUnprocessableEntity, p)
		return
	}
	// A deliberate review may refresh eligibility metadata/head, never identity.
	cmd.ExpectedRevision = read.Change.Revision
	p.Form = placementForm(cmd.values())
	payload, e := read.proposal(u.ID, cmd)
	if e != nil {
		h.recoverCancel(w, r, u, c, cmd, raw, "", "", e)
		return
	}
	encoded, token, e := focusedWorkoutReviews.signPlacementCancel(payload, time.Now())
	if e != nil {
		h.recoverCancel(w, r, u, c, cmd, raw, "", "", e)
		return
	}
	p.Mode = "review"
	p.Title = "Rever cancelamento"
	p.Review = placementCancellationReviewPage(payload, encoded, token)
	h.render(w, r, http.StatusOK, p)
}
func placementCancellationReviewPage(p trainingPlacementCancelPayload, raw, token string) *pages.TrainingPlacementReview {
	out := &pages.TrainingPlacementReview{Kind: "CANCEL", Headline: "Reveja a reposição da colocação anterior antes de cancelar.", EffectiveOn: p.EffectiveOn, Notice: "Só esta alteração e as suas colocações futuras são canceladas. O histórico é preservado; este cancelamento não publica instruções.", Proposal: raw, ReviewToken: token, CanConfirm: true}
	for _, x := range p.Before {
		out.Before = append(out.Before, placementProjectedRow(x))
	}
	for _, x := range p.After {
		out.After = append(out.After, placementProjectedRow(x))
	}
	return out
}
func verifyPlacementCancelTransport(raw, token string, actor uuid.UUID, now time.Time) (trainingPlacementCancelReviewed, error) {
	var p trainingPlacementCancelPayload
	if _, e := focusedWorkoutReviews.verifyNative(raw, token, trainingPlacementCancelPurpose, now); e != nil {
		return trainingPlacementCancelReviewed{}, errTrainingPlacementInvalid
	}
	if focusedStrictJSON([]byte(raw), &p) != nil {
		return trainingPlacementCancelReviewed{}, errTrainingPlacementInvalid
	}
	return focusedWorkoutReviews.verifyPlacementCancel(raw, token, actor, p.Command.ContextGroupID, p.Command.SubjectID, now)
}
func (h TrainingPlacements) CancelConfirm(w http.ResponseWriter, r *http.Request) {
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
	reviewed, e := verifyPlacementCancelTransport(raw, token, u.ID, time.Now())
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
	_, e = h.Store.ConfirmPlacementCancellation(r.Context(), reviewed)
	if e != nil {
		h.recoverCancel(w, r, u, c, cmd, reviewed.Payload.Values, raw, token, e)
		return
	}
	h.saved(w, r, cmd.ContextGroupID, cmd.SubjectID, cmd.SeasonID, reviewed.Payload.EffectiveOn)
}
func (h TrainingPlacements) cancelBack(w http.ResponseWriter, r *http.Request, u CurrentUser, raw, token string) {
	reviewed, e := verifyPlacementCancelTransport(raw, token, u.ID, time.Now())
	if e != nil {
		h.invalidProof(w, r, u, raw, token)
		return
	}
	cmd := reviewed.Payload.Command
	v := cloneFocusedValues(r.PostForm)
	delete(v, "gorilla.csrf.Token")
	delete(v, "proposal")
	delete(v, "review_token")
	if !focusedGraphJSONEqual(v, cmd.values()) {
		h.invalidProof(w, r, u, raw, token)
		return
	}
	c, e := h.Store.PlacementContext(r.Context(), u.ID, cmd.ContextGroupID)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	read, e := h.Store.ReadPlacementCancellation(r.Context(), u.ID, cmd.ContextGroupID, cmd.SubjectID, cmd.ChangeID)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	p := h.page(r, u, c, v)
	p.Person.Name = read.PersonName
	p.Review = &pages.TrainingPlacementReview{Proposal: raw, ReviewToken: token}
	p.Timeline = placementTimelineRows(read.Timeline, cmd.ContextGroupID, cmd.SubjectID)
	if !read.CanCancel {
		p.CancelPreviewURL = ""
		p.Recovery = pages.TrainingPlacementRecovery{Active: true, Message: "O estado mudou e este cancelamento precisa de revisão.", ReloadURL: p.NewTaskURL}
	}
	h.render(w, r, http.StatusOK, p)
}
func (h TrainingPlacements) recoverCancel(w http.ResponseWriter, r *http.Request, u CurrentUser, c trainingPlacementPageContext, cmd trainingPlacementCancellation, raw url.Values, proposal, token string, e error) {
	if errors.Is(e, db.ErrCommitOutcomeUnknown) {
		h.unknownOutcome(w, r, u, raw, proposal, token)
		return
	}

	if errors.Is(e, pgx.ErrNoRows) || errors.Is(e, errTrainingPlacementUnavailable) {
		h.fail(w, r, e)
		return
	}
	read, check := h.Store.ReadPlacementCancellation(r.Context(), u.ID, cmd.ContextGroupID, cmd.SubjectID, cmd.ChangeID)
	if check != nil {
		h.fail(w, r, check)
		return
	}
	current, check := h.Store.PlacementContext(r.Context(), u.ID, c.Group.ID)
	if check != nil {
		h.fail(w, r, check)
		return
	}
	p := h.page(r, u, current, raw)
	p.Person.Name = read.PersonName
	p.Timeline = placementTimelineRows(read.Timeline, cmd.ContextGroupID, cmd.SubjectID)
	p.Review = &pages.TrainingPlacementReview{Proposal: proposal, ReviewToken: token}
	p.Mode = "recovery"
	status := http.StatusConflict
	p.Recovery = pages.TrainingPlacementRecovery{Active: true, Message: "A alteração mudou. Os seus dados foram mantidos; reveja o estado atual antes de continuar.", ReloadURL: p.NewTaskURL}
	if !read.CanCancel {
		p.CancelPreviewURL = ""
	}
	if errors.Is(e, db.ErrCommitOutcomeUnknown) {
		status = http.StatusServiceUnavailable
		p.Recovery.UnknownCommit = true
		p.Recovery.Message = "Não foi possível confirmar se o cancelamento ficou guardado. Recarregue o estado atual; este pedido não será repetido automaticamente."
	} else if errors.Is(e, errTrainingPlacementInvalid) {
		status = http.StatusUnprocessableEntity
		p.Form.Errors.Add("form", "A revisão não é válida. Reabra a alteração antes de continuar.")
	} else if !errors.Is(e, errTrainingPlacementChanged) {
		var field *trainingPlacementFieldFailure
		if errors.As(e, &field) {
			status = http.StatusUnprocessableEntity
			p.Form.Errors.Add(field.Field, field.Message)
		} else if errors.Is(e, errTrainingPlacementLimit) {
			status = http.StatusUnprocessableEntity
			p.Form.Errors.Add("form", "O cancelamento excede o limite desta operação.")
		} else {
			h.System.InternalError(w, r)
			return
		}
	}
	h.render(w, r, status, p)
}
