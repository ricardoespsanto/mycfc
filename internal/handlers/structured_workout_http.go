package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/jackc/pgx/v5"
)

func focusedWaterCreateURL(plan, day string) string {
	return structuredPlannerPath + "/semanas/" + plan + "/treinos/criar?" + url.Values{"day": {day}}.Encode()
}
func (h StructuredWorkout) Create(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, "get")
}
func (h StructuredWorkout) Draft(w http.ResponseWriter, r *http.Request)   { h.serve(w, r, "draft") }
func (h StructuredWorkout) Preview(w http.ResponseWriter, r *http.Request) { h.serve(w, r, "preview") }
func (h StructuredWorkout) Confirm(w http.ResponseWriter, r *http.Request) { h.serve(w, r, "confirm") }
func (h StructuredWorkout) serve(w http.ResponseWriter, r *http.Request, action string) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	user, ok := CurrentUserFromContext(r.Context())
	plan, err := publicationRequestID(r.PathValue("id"))
	if !ok || err != nil {
		h.System.NotFound(w, r)
		return
	}
	ctx := r.Context()
	var cancel context.CancelFunc
	if action == "get" {
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
	} else {
		deadline, valid := httpx.PublicationDeadline(ctx, h.HTTPWriteTimeout)
		if !valid {
			h.System.RequestRejected(w, r)
			return
		}
		ctx, cancel = context.WithDeadline(ctx, deadline)
	}
	defer cancel()
	r = r.WithContext(ctx)
	var probe focusedWorkoutPayload
	day := r.URL.Query().Get("day")
	if action != "get" {
		if err = r.ParseForm(); err != nil {
			h.System.RequestRejected(w, r)
			return
		}
		if action == "confirm" {
			if focusedStrictJSON([]byte(r.PostForm.Get("proposal")), &probe) != nil {
				h.System.RequestRejected(w, r)
				return
			}
			day = probe.Proposal.Context.Day
		} else {
			day = r.PostForm.Get("day")
		}
	}
	if !validWorkspaceDate(day) {
		h.System.NotFound(w, r)
		return
	}
	request := FocusedWorkoutRequest{ActorID: user.ID, PlanID: plan, Day: day}
	source, err := h.Store.LoadFocusedWorkout(ctx, request)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			h.System.NotFound(w, r)
		} else {
			h.System.InternalError(w, r)
		}
		return
	}
	draft := newFocusedWorkoutDraft(day)
	page := h.page(r, user, source, draft)
	if action == "get" {
		if !source.AuthoringEnabled || source.SchemaContract != "schedule-p1" {
			page.ReadOnly = true
			page.Message = "A criação de treinos está temporariamente indisponível. Contacte um administrador."
		}
		h.render(w, r, 200, page)
		return
	}
	if action == "confirm" {
		for k, v := range r.PostForm {
			if (k != "intent" && k != "proposal" && k != "review_token" && k != "gorilla.csrf.Token") || len(v) != 1 {
				h.failure(w, r, 422, page, errFocusedWorkoutInvalid)
				return
			}
		}
		if r.PostForm.Get("intent") != "confirm" {
			h.failure(w, r, 422, page, errFocusedWorkoutInvalid)
			return
		}
		rawDraft, draftErr := parseFocusedWorkoutDraft(probe.Draft)
		if draftErr == nil {
			draft = rawDraft
			page = h.page(r, user, source, draft)
			page.Context = focusedPageContext(probe.Proposal.Context)
		}
		page.ProposalJSON, page.ReviewToken = r.PostForm.Get("proposal"), r.PostForm.Get("review_token")
		payload, reviewed, verifyErr := focusedWorkoutReviews.verify(r.PostForm.Get("proposal"), r.PostForm.Get("review_token"), user.ID, plan, time.Now())
		if verifyErr != nil {
			h.unusableReview(w, r, page)
			return
		}
		page = h.page(r, user, source, focusedDraftFromPayload(payload))
		page.Context = focusedPageContext(payload.Proposal.Context)
		page.ProposalJSON, page.ReviewToken = r.PostForm.Get("proposal"), r.PostForm.Get("review_token")
		result, saveErr := h.Store.SaveFocusedWorkout(ctx, reviewed)
		if saveErr != nil {
			if errors.Is(saveErr, pgx.ErrNoRows) {
				h.System.NotFound(w, r)
				return
			}
			if errors.Is(saveErr, db.ErrCommitOutcomeUnknown) {
				page.ReadOnly = true
				page.Message = "Não foi possível confirmar o resultado. Consulte a semana antes de iniciar outro treino."
				page.Preview = pages.StructuredWorkoutInstructions(projectFocusedWater(payload.Proposal))
				h.render(w, r, 409, page)
				return
			}
			if errors.Is(saveErr, errFocusedWorkoutInvalid) {
				h.unusableReview(w, r, page)
				return
			}
			h.failure(w, r, 409, page, saveErr)
			return
		}
		httpx.Redirect(w, r, structuredPlannerURL(source.GroupID.String(), plan.String(), result.SessionID.String(), day), http.StatusSeeOther)
		return
	}
	// A retained signed identity cannot be discarded or renewed by a draft
	// command. Check it before editable validation/version recovery so an
	// unusable proof stays an honest terminal recovery on every native path.
	priorRaw, priorToken := r.PostForm.Get("prior_proposal"), r.PostForm.Get("prior_review_token")
	var prior *focusedWaterProposal
	if priorRaw != "" || priorToken != "" {
		old, _, priorErr := focusedWorkoutReviews.verify(priorRaw, priorToken, user.ID, plan, time.Now())
		if priorErr != nil || !focusedSameCreateIdentity(old.Proposal.Context, source) {
			retained, parseErr := parseFocusedWorkoutDraft(r.PostForm)
			if parseErr != nil {
				retained = retainFocusedWorkoutDraft(r.PostForm, day)
			}
			page = h.page(r, user, source, retained)
			retainFocusedContext(&page, r.PostForm, source)
			page.ProposalJSON, page.ReviewToken = priorRaw, priorToken
			h.unusableReview(w, r, page)
			return
		}
		prior = &old.Proposal
	}
	draft, err = parseFocusedWorkoutDraft(r.PostForm)
	if err != nil {
		page = h.page(r, user, source, retainFocusedWorkoutDraft(r.PostForm, day))
		retainFocusedContext(&page, r.PostForm, source)
		page.ProposalJSON, page.ReviewToken = priorRaw, priorToken
		h.failure(w, r, 422, page, err)
		return
	}
	page = h.page(r, user, source, draft)
	retainFocusedContext(&page, r.PostForm, source)
	page.ProposalJSON, page.ReviewToken = r.PostForm.Get("prior_proposal"), r.PostForm.Get("prior_review_token")
	if action == "draft" && !focusedPostedContextValid(r.PostForm, source) {
		h.failure(w, r, 409, page, errFocusedWorkoutConflict)
		return
	}
	if action == "draft" {
		draft, err = applyFocusedWorkoutCommand(draft, r.PostForm.Get("command"))
		if err != nil {
			h.failure(w, r, 422, page, err)
			return
		}
		updated := h.page(r, user, source, draft)
		updated.Context = page.Context
		updated.ProposalJSON, updated.ReviewToken = page.ProposalJSON, page.ReviewToken
		h.render(w, r, 200, updated)
		return
	}
	if r.PostForm.Get("command") != "preview" {
		h.failure(w, r, 422, page, errFocusedWorkoutInvalid)
		return
	}
	if !source.AuthoringEnabled || source.SchemaContract != "schedule-p1" {
		h.failure(w, r, 409, page, errFocusedWorkoutClosed)
		return
	}
	proposal, fieldErrors := normalizeFocusedWater(source, draft)
	if len(fieldErrors) > 0 {
		page.Errors = fieldErrors
		h.render(w, r, 422, page)
		return
	}
	allocateFocusedWaterIDs(&proposal)
	if prior != nil {
		proposal.SessionID = prior.SessionID
		proposal.Activity.ID = prior.Activity.ID
		for i := range proposal.Phases {
			for _, old := range prior.Phases {
				if proposal.Phases[i].Key == old.Key {
					proposal.Phases[i].ID = old.ID
				}
			}
		}
	}
	artifact, err := focusedWaterArtifact(proposal)
	if err != nil {
		h.failure(w, r, 422, page, err)
		return
	}
	size, err := h.Store.FocusedWorkoutArtifactSize(ctx, request, artifact)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			h.System.NotFound(w, r)
			return
		}
		h.failure(w, r, 503, page, err)
		return
	}
	if size > focusedWorkoutMaxArtifactBytes {
		h.failure(w, r, 422, page, errFocusedWorkoutLimit)
		return
	}
	payload := focusedWorkoutPayload{Proposal: proposal, Draft: focusedDraftWithRemovalFields(draft)}
	raw, token, err := focusedWorkoutReviews.sign(payload, time.Now())
	if err != nil {
		h.failure(w, r, 422, page, err)
		return
	}
	page.Context = focusedPageContext(source)
	page.Reviewing = true
	page.ProposalJSON, page.ReviewToken = raw, token
	page.Preview = pages.StructuredWorkoutInstructions(projectFocusedWater(proposal))
	h.render(w, r, 200, page)
}
func focusedDraftFromPayload(p focusedWorkoutPayload) focusedWorkoutDraft {
	d, _ := parseFocusedWorkoutDraft(p.Draft)
	return d
}
func focusedPageContext(c FocusedWorkoutContext) pages.StructuredWorkoutContext {
	return pages.StructuredWorkoutContext{GroupID: c.GroupID.String(), PlanID: c.PlanID.String(), Day: c.Day, ExpectedPlanUpdatedAt: c.PlanUpdatedAt.Format(time.RFC3339Nano), SourceDigest: c.Digest(), DatabaseIncarnation: c.DatabaseIncarnation.String(), StateEpoch: strconv.FormatInt(c.StateEpoch, 10), EntryKind: "TRAINING"}
}
func (h StructuredWorkout) page(r *http.Request, user CurrentUser, c FocusedWorkoutContext, d focusedWorkoutDraft) pages.StructuredWorkoutTaskPage {
	meta := h.PageMeta
	meta.Title = "Criar treino de água | MyCFCoimbra"
	meta.PageLabel = "Criar treino de água"
	meta.CurrentPath = structuredPlannerPath
	meta.CurrentUserName = user.Name
	meta.CurrentUserID = user.ID.String()
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	base := structuredPlannerPath + "/semanas/" + c.PlanID.String() + "/treinos/criar"
	page := pages.StructuredWorkoutTaskPage{Meta: meta, CSRFField: meta.CSRFField, WaterCreateOnly: true, Context: focusedPageContext(c), Values: d.Values, Errors: focusedWorkoutErrors(), GroupName: c.GroupName, WeekTitle: c.PlanTitle, DraftURL: base + "/rascunho", PreviewURL: base + "/prever", ConfirmURL: base + "/confirmar", ReturnURL: structuredPlannerURL(c.GroupID.String(), c.PlanID.String(), "", c.Day), AdvancedURL: structuredWorkspaceAnchor(structuredPlannerURL(c.GroupID.String(), c.PlanID.String(), "", c.Day), "training-plan")}
	activity := pages.StructuredWorkoutActivity{Key: "a_1"}
	for _, key := range d.Keys {
		activity.Phases = append(activity.Phases, pages.StructuredWorkoutPhase{Key: key})
	}
	page.Activities = []pages.StructuredWorkoutActivity{activity}
	for _, removed := range d.Removed {
		page.Removed = append(page.Removed, pages.StructuredWorkoutRemoval{Kind: "phase", Key: removed.Key, Label: "Fase removida", Payload: removed.Payload})
	}
	page.StartOccurrences = focusedOccurrenceChoices(d.Values.Get("schedule.date"), d.Values.Get("schedule.start_time"))
	page.EndOccurrences = focusedOccurrenceChoices(d.Values.Get("schedule.end_date"), d.Values.Get("schedule.end_time"))
	return page
}
func (h StructuredWorkout) render(w http.ResponseWriter, r *http.Request, status int, page pages.StructuredWorkoutTaskPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.StructuredWorkoutTask(page).Render(r.Context(), w)
}

// Unusable proof recovery deliberately keeps the editor and proof bytes. It
// never allocates another create identity or presents an impossible re-preview.
func (h StructuredWorkout) unusableReview(w http.ResponseWriter, r *http.Request, page pages.StructuredWorkoutTaskPage) {
	page.Reviewing = false
	page.Preview = nil
	page.PreviewURL = ""
	page.Message = "Esta revisão já não pode ser utilizada. Os valores introduzidos foram mantidos para copiar. Consulte primeiro a semana para verificar se o treino foi guardado; só depois inicie um novo treino."
	page.Errors.Add("form", page.Message)
	h.render(w, r, http.StatusUnprocessableEntity, page)
}

func (h StructuredWorkout) failure(w http.ResponseWriter, r *http.Request, status int, page pages.StructuredWorkoutTaskPage, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	switch {
	case errors.Is(err, errFocusedWorkoutConflict) || errors.Is(err, errStructuredTrainingPublicationConflict):
		status = 409
		page.Message = "A semana mudou. Os valores introduzidos foram mantidos; reveja o contexto antes de guardar."
	case errors.Is(err, errFocusedWorkoutClosed) || errors.Is(err, ErrTrainingScheduleWriteUnsupported):
		status = 409
		page.Message = "A criação de treinos está fechada ou a preparação mudou. Consulte a semana antes de rever o treino."
	case errors.Is(err, errFocusedWorkoutLimit):
		status = 422
		page.Message = "Este treino excede os limites técnicos do pedido. Reduza o conteúdo antes de rever."
	case errors.Is(err, errFocusedWorkoutInvalid):
		status = 422
		page.Message = "Esta revisão é inválida ou expirou. Os valores seguros foram mantidos; volte a pré-visualizar."
	default:
		status = 503
		page.Message = "Não foi possível concluir o pedido. Os valores introduzidos foram mantidos."
	}
	page.Reviewing = false
	page.Preview = nil
	page.Errors.Add("form", page.Message)
	h.render(w, r, status, page)
}
func retainFocusedContext(page *pages.StructuredWorkoutTaskPage, input url.Values, c FocusedWorkoutContext) {
	for key, target := range map[string]*string{"expected_plan_updated_at": &page.Context.ExpectedPlanUpdatedAt, "source_digest": &page.Context.SourceDigest, "database_incarnation": &page.Context.DatabaseIncarnation, "state_epoch": &page.Context.StateEpoch} {
		if len(input[key]) == 1 && len(input.Get(key)) <= 100 {
			*target = input.Get(key)
		}
	}
}
func focusedPostedContextValid(input url.Values, c FocusedWorkoutContext) bool {
	if input.Get("group_id") != c.GroupID.String() || input.Get("plan_id") != c.PlanID.String() || input.Get("session_id") != "" || input.Get("expected_session_updated_at") != "" || input.Get("day") != c.Day {
		return false
	}
	return input.Get("expected_plan_updated_at") == c.PlanUpdatedAt.Format(time.RFC3339Nano) && input.Get("source_digest") == c.Digest() && input.Get("database_incarnation") == c.DatabaseIncarnation.String() && input.Get("state_epoch") == strconv.FormatInt(c.StateEpoch, 10)
}
func retainFocusedWorkoutDraft(input url.Values, day string) focusedWorkoutDraft {
	d := newFocusedWorkoutDraft(day)
	for k := range d.Values {
		delete(d.Values, k)
	}
	d.Keys = nil
	d.Removed = nil
	keys := input["activities[a_1].phase_order"]
	seen := map[string]bool{}
	for _, key := range keys {
		if len(d.Keys) >= focusedWorkoutMaxPhases {
			break
		}
		if focusedWorkoutKey.MatchString(key) && !seen[key] {
			d.Keys = append(d.Keys, key)
			seen[key] = true
		}
	}
	d.Values.Set("activity_order", "a_1")
	d.Values["activities[a_1].phase_order"] = append([]string(nil), d.Keys...)
	for k, v := range input {
		keep := focusedWorkoutOrdinary[k] || strings.HasPrefix(k, "activities[a_1].") && !strings.Contains(k, ".phases[")
		for _, key := range d.Keys {
			for _, f := range []string{"purpose", "title", "instructions", "detail_mode"} {
				if k == focusedPhasePrefix(key)+"."+f {
					keep = true
				}
			}
		}
		if keep && len(v) > 0 && len(v[0]) < focusedWorkoutMaxRequestBytes && k != "activities[a_1].id" {
			d.Values[k] = []string{v[0]}
		}
	}
	return d
}

func focusedSameCreateIdentity(a, b FocusedWorkoutContext) bool {
	return a.ActorID == b.ActorID && a.PlanID == b.PlanID && a.GroupID == b.GroupID && a.SeasonID == b.SeasonID && a.Day == b.Day && a.WeekStart.Equal(b.WeekStart) && a.DatabaseIncarnation == b.DatabaseIncarnation && a.SchemaContract == b.SchemaContract
}
