package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/jackc/pgx/v5"
)

func focusedWorkoutEditURL(plan, session string) string {
	return structuredPlannerPath + "/semanas/" + plan + "/treinos/" + session + "/editar"
}
func focusedWorkoutCancelURL(plan, session string) string {
	return structuredPlannerPath + "/semanas/" + plan + "/treinos/" + session + "/cancelar"
}
func (h StructuredWorkout) Edit(w http.ResponseWriter, r *http.Request) { h.serveEdit(w, r, "get") }
func (h StructuredWorkout) EditDraft(w http.ResponseWriter, r *http.Request) {
	h.serveEdit(w, r, "draft")
}
func (h StructuredWorkout) EditPreview(w http.ResponseWriter, r *http.Request) {
	h.serveEdit(w, r, "preview")
}
func (h StructuredWorkout) EditConfirm(w http.ResponseWriter, r *http.Request) {
	h.serveEdit(w, r, "confirm")
}
func (h StructuredWorkout) editStore() FocusedWorkoutEditStore {
	store, _ := h.Store.(FocusedWorkoutEditStore)
	return store
}
func focusedEditPageContext(source focusedEditSource) pages.StructuredWorkoutContext {
	c := focusedPageContext(source.Context)
	baseline, _ := focusedEditBaselineFor(source)
	c.SessionID = source.Raw.Session.ID.String()
	c.ExpectedSessionUpdatedAt = baseline.SessionVersion.Format(time.RFC3339Nano)
	c.SourceDigest = baseline.OwnRawDigest
	c.EntryKind = string(source.Raw.Session.EntryKind)
	return c
}
func (h StructuredWorkout) editPage(r *http.Request, user CurrentUser, source focusedEditSource, d focusedGraphDraft) pages.StructuredWorkoutTaskPage {
	page := h.graphPage(r, user, source.Context, d)
	page.Editing = true
	page.Context = focusedEditPageContext(source)
	base := focusedWorkoutEditURL(source.Context.PlanID.String(), source.Raw.Session.ID.String())
	page.Meta.Title = "Alterar treino | MyCFCoimbra"
	page.Meta.PageLabel = "Alterar treino"
	page.DraftURL, page.PreviewURL, page.ConfirmURL = base+"/rascunho", base+"/prever", base+"/confirmar"
	page.ReloadURL = base
	page.ReturnURL = structuredPlannerURL(source.Context.GroupID.String(), source.Context.PlanID.String(), source.Raw.Session.ID.String(), source.Context.Day)
	page.PinnedProfiles = map[string]pages.StructuredWorkoutChoice{}
	for i, removed := range d.Removed {
		page.Removed[i].ID = focusedGraphRemovedID(removed)
	}
	for ai := range page.Activities {
		a := &page.Activities[ai]
		ap := focusedActivityPrefix(a.Key)
		a.ID = d.Values.Get(ap + ".id")
		for bi := range a.Phases {
			b := &a.Phases[bi]
			bp := focusedGraphPhasePrefix(a.Key, b.Key)
			b.ID = d.Values.Get(bp + ".id")
			for ni := range b.WaterNodes {
				n := &b.WaterNodes[ni]
				n.ID = d.Values.Get(focusedNodePrefix(a.Key, b.Key, n.Key) + ".id")
			}
			for xi := range b.Exercises {
				x := &b.Exercises[xi]
				x.ID = d.Values.Get(focusedExercisePrefix(a.Key, b.Key, x.Key) + ".id")
			}
			for _, sa := range source.Graph.Activities {
				for _, sb := range sa.Phases {
					if b.ID != sb.ID.String() || sb.Water == nil || sb.Water.ProfileID == nil {
						continue
					}
					for _, profile := range source.Graph.References.Profiles {
						if profile.ID == *sb.Water.ProfileID && !profile.IsActive {
							field := bp + ".water.profile_id"
							page.PinnedProfiles[field] = pages.StructuredWorkoutChoice{Value: profile.ID.String(), Label: fmt.Sprintf("%s · %s · revisão %d · inativa (fixada neste bloco)", profile.Name, paddlingCraftLabel(string(profile.Craft)), profile.Revision)}
						}
					}
				}
			}
		}
	}
	return page
}
func focusedRetainEditContext(page *pages.StructuredWorkoutTaskPage, values url.Values) {
	for name, target := range map[string]*string{"day": &page.Context.Day, "expected_plan_updated_at": &page.Context.ExpectedPlanUpdatedAt, "expected_session_updated_at": &page.Context.ExpectedSessionUpdatedAt, "source_digest": &page.Context.SourceDigest, "database_incarnation": &page.Context.DatabaseIncarnation, "state_epoch": &page.Context.StateEpoch} {
		if len(values[name]) == 1 && len(values.Get(name)) <= 100 {
			*target = values.Get(name)
		}
	}
}
func focusedEditPostedBaseline(values url.Values, source focusedEditSource) error {
	b, err := focusedEditBaselineFor(source)
	if err != nil {
		return err
	}
	if values.Get("plan_id") != source.Context.PlanID.String() || values.Get("group_id") != source.Context.GroupID.String() || values.Get("session_id") != b.SessionID.String() || values.Get("day") != b.SourceDay || values.Get("source_digest") != b.OwnRawDigest || values.Get("expected_session_updated_at") != b.SessionVersion.Format(time.RFC3339Nano) {
		return errFocusedEditSourceChanged
	}
	return nil
}
func (h StructuredWorkout) editRecovery(w http.ResponseWriter, r *http.Request, page pages.StructuredWorkoutTaskPage, err error, status int) {
	focusedEditRecoveryPage(&page, err)
	h.render(w, r, status, page)
}
func (h StructuredWorkout) serveEdit(w http.ResponseWriter, r *http.Request, action string) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	user, ok := CurrentUserFromContext(r.Context())
	plan, pe := publicationRequestID(r.PathValue("plan"))
	session, se := publicationRequestID(r.PathValue("id"))
	store := h.editStore()
	if !ok || pe != nil || se != nil || store == nil {
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
	request := FocusedWorkoutEditRequest{user.ID, plan, session}
	source, err := store.LoadFocusedEdit(ctx, request)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			h.System.NotFound(w, r)
		} else {
			h.System.InternalError(w, r)
		}
		return
	}
	draft, draftErr := focusedEditLosslessDraft(source)
	if draftErr != nil {
		draft = newFocusedGraphDraft(source.Context.Day)
	}
	page := h.editPage(r, user, source, draft)
	if action == "get" {
		if draftErr != nil || !focusedEditEligible(source.Raw.Session, source.DecisionAt) || !source.Context.AuthoringEnabled {
			page.ReadOnly = true
			page.Preview = pages.StructuredWorkoutInstructions(focusedGraphView(source.Graph))
			page.Message = "Este treino está disponível para consulta. Não é possível alterar o seu conteúdo neste momento."
		}
		h.render(w, r, 200, page)
		return
	}
	if err = r.ParseForm(); err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	var old focusedEditReviewPayload
	raw, token := r.PostForm.Get("prior_proposal"), r.PostForm.Get("prior_review_token")
	if action == "confirm" {
		raw, token = r.PostForm.Get("proposal"), r.PostForm.Get("review_token")
	}
	// A bounded decoded proposal may help retain only the user's own field
	// topology on an unusable proof. It is never accepted as source authority.
	if raw != "" {
		_ = focusedStrictJSON([]byte(raw), &old)
	}
	ownInput := r.PostForm
	if action == "confirm" {
		ownInput = old.Draft
	}
	retained, parseErr := parseFocusedEditGraph(ownInput, source.Graph)
	if parseErr != nil && raw != "" {
		retained, parseErr = parseFocusedEditGraph(ownInput, old.Proposal)
	}
	if parseErr != nil {
		retained = retainFocusedGraphDraft(ownInput, source.Context.Day)
	}
	page = h.editPage(r, user, source, retained)
	focusedRetainEditContext(&page, r.PostForm)
	page.ProposalJSON, page.ReviewToken = raw, token
	var prior *focusedWorkoutGraph
	if raw != "" || token != "" {
		verified, _, verifyErr := focusedWorkoutReviews.verifyEdit(raw, token, user.ID, plan, session, time.Now())
		if verifyErr != nil {
			h.editRecovery(w, r, page, errFocusedWorkoutInvalid, 422)
			return
		}
		old = verified
		// Original metadata remains attached to a failed confirmation; no rebase.
		if action == "confirm" {
			page.Context = focusedEditPageContext(source)
			for name, value := range focusedEditMetadata(old) {
				switch name {
				case "expected_plan_updated_at":
					page.Context.ExpectedPlanUpdatedAt = value
				case "expected_session_updated_at":
					page.Context.ExpectedSessionUpdatedAt = value
				case "source_digest":
					page.Context.SourceDigest = value
				case "state_epoch":
					page.Context.StateEpoch = value
				case "database_incarnation":
					page.Context.DatabaseIncarnation = value
				case "day":
					page.Context.Day = value
				}
			}
		}
		if err = old.Original.matches(source); err != nil {
			h.editRecovery(w, r, page, err, 409)
			return
		}
		prior = &old.Proposal
	} else if action == "confirm" {
		h.editRecovery(w, r, page, errFocusedWorkoutInvalid, 422)
		return
	}
	if !focusedEditEligible(source.Raw.Session, source.DecisionAt) || draftErr != nil {
		h.editRecovery(w, r, page, errFocusedEditReadOnly, 409)
		return
	}
	if action == "confirm" {
		for key, values := range r.PostForm {
			if (key != "intent" && key != "proposal" && key != "review_token" && key != "gorilla.csrf.Token") || len(values) != 1 {
				h.editRecovery(w, r, page, errFocusedWorkoutInvalid, 422)
				return
			}
		}
		if r.PostForm.Get("intent") != "confirm" {
			h.editRecovery(w, r, page, errFocusedWorkoutInvalid, 422)
			return
		}
		_, reviewed, verifyErr := focusedWorkoutReviews.verifyEdit(raw, token, user.ID, plan, session, time.Now())
		if verifyErr != nil {
			h.editRecovery(w, r, page, verifyErr, 422)
			return
		}
		result, saveErr := store.SaveFocusedEdit(ctx, reviewed)
		if saveErr != nil {
			// Fresh scoped access after rollback distinguishes a stale mutation from
			// revocation; never fetch the current session through an unscoped method.
			if errors.Is(saveErr, db.ErrCommitOutcomeUnknown) {
				page.ReadOnly = true
				page.Preview = pages.StructuredWorkoutInstructions(focusedGraphView(old.Proposal))
				page.Message = "Não foi possível confirmar o resultado. Consulte o treino atual antes de tentar outra alteração."
				h.render(w, r, 409, page)
				return
			}
			// A rollback/serialization error does not prove that only the
			// plan changed. Re-read the complete scoped own source before
			// offering any deliberate re-preview; preserve the original proof.
			current, accessErr := store.LoadFocusedEdit(ctx, request)
			if errors.Is(accessErr, pgx.ErrNoRows) {
				h.System.NotFound(w, r)
				return
			} else if accessErr != nil {
				h.System.InternalError(w, r)
				return
			}
			if ownErr := old.Original.matches(current); ownErr != nil {
				h.editRecovery(w, r, page, ownErr, 409)
				return
			}
			if !focusedEditEligible(current.Raw.Session, current.DecisionAt) {
				h.editRecovery(w, r, page, errFocusedEditReadOnly, 409)
				return
			}
			if errors.Is(saveErr, errFocusedEditSourceChanged) || errors.Is(saveErr, errFocusedEditReadOnly) || errors.Is(saveErr, errFocusedWorkoutInvalid) {
				h.editRecovery(w, r, page, saveErr, 409)
				return
			}
			h.failure(w, r, 409, page, saveErr)
			return
		}
		httpx.Redirect(w, r, structuredPlannerURL(source.Context.GroupID.String(), plan.String(), result.SessionID.String(), old.Proposal.Schedule.Date), http.StatusSeeOther)
		return
	}
	if err = focusedEditPostedBaseline(r.PostForm, source); err != nil {
		h.editRecovery(w, r, page, err, 409)
		return
	}
	if parseErr != nil {
		h.failure(w, r, 422, page, parseErr)
		return
	}
	if action == "draft" {
		if r.PostForm.Get("expected_plan_updated_at") != source.Context.PlanUpdatedAt.Format(time.RFC3339Nano) || r.PostForm.Get("state_epoch") != strconv.FormatInt(source.Context.StateEpoch, 10) {
			h.failure(w, r, 409, page, errFocusedWorkoutConflict)
			return
		}
		updated, commandErr := applyFocusedEditCommand(retained, r.PostForm.Get("command"), source.Graph)
		if commandErr != nil {
			h.failure(w, r, 422, page, commandErr)
			return
		}
		updatedPage := h.editPage(r, user, source, updated)
		updatedPage.Context = page.Context
		updatedPage.ProposalJSON, updatedPage.ReviewToken = raw, token
		h.render(w, r, 200, updatedPage)
		return
	}
	if r.PostForm.Get("command") != "preview" {
		h.failure(w, r, 422, page, errFocusedWorkoutInvalid)
		return
	}
	proposal, fields := normalizeFocusedEdit(source, retained)
	if len(fields) > 0 {
		page.Errors = fields
		h.render(w, r, 422, page)
		return
	}
	fresh, err := store.ReadFocusedEditPreview(ctx, request, focusedGraphSelectedProfileIDs(proposal))
	if err != nil {
		h.failure(w, r, 409, page, err)
		return
	}
	baseline, _ := focusedEditBaselineFor(source)
	if err = baseline.matches(fresh); err != nil {
		h.editRecovery(w, r, page, err, 409)
		return
	}
	proposal.Context = fresh.Context
	proposal.References = fresh.Graph.References
	if err = allocateFocusedEditIDs(&proposal, fresh.Graph, prior); err != nil {
		h.failure(w, r, 422, page, err)
		return
	}
	baseline, _ = focusedEditBaselineFor(fresh)
	payload := focusedEditReviewPayload{Original: baseline, Proposal: proposal, Draft: focusedGraphValues(retained)}
	raw, token, err = focusedWorkoutReviews.signEdit(payload, fresh, time.Now())
	if err != nil {
		h.failure(w, r, 422, page, err)
		return
	}
	artifact, err := focusedGraphArtifact(proposal)
	if err != nil {
		h.failure(w, r, 422, page, err)
		return
	}
	size, err := h.Store.FocusedWorkoutArtifactSize(ctx, FocusedWorkoutRequest{user.ID, plan, proposal.Schedule.Date}, artifact)
	if err != nil {
		h.failure(w, r, 503, page, err)
		return
	}
	if size > focusedWorkoutMaxArtifactBytes {
		h.failure(w, r, 422, page, errFocusedWorkoutLimit)
		return
	}
	page = h.editPage(r, user, fresh, retained)
	page.Reviewing = true
	page.ProposalJSON, page.ReviewToken = raw, token
	page.Preview = pages.StructuredWorkoutInstructions(focusedGraphView(proposal))
	h.render(w, r, 200, page)
}
