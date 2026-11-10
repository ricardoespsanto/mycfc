package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type recipientPublicationStore interface {
	AuthorizePublication(context.Context, uuid.UUID, uuid.UUID) (publicationPlanHeader, error)
	ReadPublicationSource(context.Context, uuid.UUID, uuid.UUID) (publicationSourceEnvelope, error)
	ConfirmPublication(context.Context, publicationConfirmInput) (publicationSaveResult, error)
}

func (h StructuredTraining) publishReviewedPlan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	actor, ok := CurrentUserFromContext(r.Context())
	plan, err := publicationRequestID(r.PathValue("id"))
	if !ok || err != nil {
		h.System.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.System.RequestRejected(w, r)
		return
	}
	deadline, ok := httpx.PublicationDeadline(r.Context(), h.HTTPWriteTimeout)
	if !ok {
		http.Error(w, "indisponível", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	r = r.WithContext(ctx)
	store := h.Publications
	if store == nil {
		store, _ = h.Store.(recipientPublicationStore)
	}
	if store == nil {
		h.System.InternalError(w, r)
		return
	}
	// Denial precedes any subject-specific validation or retained task data.
	header, err := store.AuthorizePublication(ctx, actor.ID, plan)
	if err != nil {
		if publicationScopeDenied(err) {
			h.System.NotFound(w, r)
		} else {
			h.System.InternalError(w, r)
		}
		return
	}
	page := h.publicationReviewPage(r, actor, header)
	intent := r.PostForm.Get("publication_intent")
	if intent == "" {
		intent = "preview"
	}
	if len(r.PostForm["publication_intent"]) > 1 {
		intent = "invalid"
	}
	switch intent {
	case "preview":
		summary := strings.TrimSpace(r.PostForm.Get("change_summary"))
		validationMeta := trainingPublicationReviewMetadata{ActorID: actor.ID, PlanID: plan, SourceUpdatedAt: header.SourceUpdatedAt.Time, ChangeSummary: summary}
		if len(r.PostForm["change_summary"]) != 1 {
			h.publicationFailure(w, r, 422, page, errTrainingPublicationReviewInvalid)
			return
		}
		if _, _, err := trainingPublicationMetadataBinding(validationMeta); err != nil {
			h.publicationFailure(w, r, 422, page, err)
			return
		}
		source, err := store.ReadPublicationSource(ctx, actor.ID, plan)
		if err != nil {
			h.publicationFailure(w, r, 422, page, err)
			return
		}
		returned, err := publicationPreviewReturn(r.PostForm, source.Header)
		if err != nil {
			h.publicationFailure(w, r, 422, page, err)
			return
		}
		preview, err := makePublicationPreview(source, actor.ID, summary, returned, h.location(), time.Now())
		if err != nil {
			h.publicationFailure(w, r, 422, page, err)
			return
		}
		metadata := preview.Metadata
		page.Mode = "preview"
		page.PlanTitle = preview.Materialization.PlanTitle
		page.GroupName = preview.Materialization.GroupName
		page.WeekLabel = preview.Materialization.WeekLabel
		page.Summary = metadata.ChangeSummary
		page.SourceUpdatedAt = metadata.SourceUpdatedAt.Format(time.RFC3339Nano)
		page.ReviewToken = preview.Token
		page.LatestPublicationID = ""
		if metadata.LatestPublicationID != uuid.Nil {
			page.LatestPublicationID = metadata.LatestPublicationID.String()
		}
		page.ExistingRevision = int(metadata.LatestRevision)
		page.ReturnGroup, page.ReturnWeek, page.ReturnSession, page.ReturnDay = publicationContextFields(metadata.Return)
		page.ReturnURL, page.ReviewURL = publicationReturnURL(metadata.Return), publicationReviewURL(metadata.Return)
		page.PrescriptionCount = len(preview.Materialization.Prescriptions)
		page.AddedCount = preview.Materialization.Added
		page.ChangedCount = preview.Materialization.Changed
		page.RemovedCount = preview.Materialization.Removed
		page.UnchangedCount = preview.Materialization.Unchanged
		page.NoChanges = preview.Materialization.NoChanges
		page.CanConfirm = preview.CanConfirm
		for _, recipient := range preview.Recipients {
			page.Recipients = append(page.Recipients, pages.TrainingPublicationRecipient{Name: recipient.Name, SessionCount: recipient.SessionCount})
		}
		h.renderPublicationReview(w, r, 200, page)
	case "confirm":
		if len(r.PostForm["confirm"]) != 1 || r.PostForm.Get("confirm") != "yes" {
			h.publicationFailure(w, r, 422, page, errTrainingPublicationReviewInvalid)
			return
		}
		metadata, err := publicationRequestMetadata(r.PostForm, actor.ID, plan)
		if err != nil {
			h.publicationFailure(w, r, 422, page, err)
			return
		}
		claims, err := trainingPublicationReviews.verify(r.PostForm.Get("review_token"), metadata, time.Now())
		if err != nil {
			h.publicationFailure(w, r, 422, page, err)
			return
		}
		result, err := store.ConfirmPublication(ctx, publicationConfirmInput{ActorID: actor.ID, PlanID: plan, Review: claims})
		if err != nil {
			h.publicationFailure(w, r, 409, page, err)
			return
		}
		if result.CleanupFailed {
			slog.WarnContext(ctx, "recipient publication completed with connection cleanup failure", "request_id", httpx.RequestID(ctx))
		}
		if result.NoOp {
			h.flash(r, "Sem alterações materiais. A revisão "+strconv.FormatInt(int64(result.Revision), 10)+" foi mantida.")
		} else {
			h.flash(r, "Revisão "+strconv.FormatInt(int64(result.Revision), 10)+" publicada para "+strconv.Itoa(result.PrescriptionCount)+" prescrições privadas.")
		}
		httpx.Redirect(w, r, publicationReturnURL(claims.metadata.Return), http.StatusSeeOther)
	default:
		h.publicationFailure(w, r, 422, page, errTrainingPublicationReviewInvalid)
	}
}

func publicationScopeDenied(err error) bool {
	return errors.Is(err, pgx.ErrNoRows) || errors.Is(err, errTrainingScheduleUnavailable)
}
func publicationContextFields(c trainingPublicationReturnContext) (group, week, session, day string) {
	if c.GroupID != uuid.Nil {
		group = c.GroupID.String()
	}
	if c.WeekID != uuid.Nil {
		week = c.WeekID.String()
	}
	if c.SessionID != uuid.Nil {
		session = c.SessionID.String()
	}
	return group, week, session, c.Day
}
func (h StructuredTraining) publicationReviewPage(r *http.Request, actor CurrentUser, header publicationPlanHeader) pages.TrainingPublicationReviewPage {
	meta := h.PageMeta
	meta.Title = "Rever publicação | MyCFCoimbra"
	meta.PageLabel = "Rever publicação"
	meta.CurrentPath = structuredPlannerPath
	meta.CurrentUserName = actor.Name
	meta.CurrentUserID = actor.ID.String()
	meta.Navigation = dashboardNavigation(actor)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	returned := trainingPublicationReturnContext{GroupID: header.GroupID, WeekID: header.PlanID}
	page := pages.TrainingPublicationReviewPage{Meta: meta, CSRFField: meta.CSRFField, Mode: "error", Errors: validation.FieldErrors{}, ActionURL: structuredPlannerPath + "/semanas/" + header.PlanID.String() + "/publicar", ReturnURL: publicationReturnURL(returned), ReviewURL: publicationReviewURL(returned), SetupURL: trainingGroupsPath + "/" + header.GroupID.String(), Summary: r.PostForm.Get("change_summary"), SourceUpdatedAt: r.PostForm.Get("source_updated_at"), ReviewToken: r.PostForm.Get("review_token"), LatestPublicationID: r.PostForm.Get("reviewed_latest_id"), ReturnGroup: r.PostForm.Get("return_group"), ReturnWeek: r.PostForm.Get("return_week"), ReturnSession: r.PostForm.Get("return_session"), ReturnDay: r.PostForm.Get("return_day")}
	if revision, err := strconv.ParseInt(r.PostForm.Get("reviewed_latest_revision"), 10, 32); err == nil && revision >= 0 {
		page.ExistingRevision = int(revision)
	}
	// The group/week and civil day can be checked against the freshly scoped
	// header without another audience query. A valid original signed review also
	// preserves its session reference; following it still reauthorizes on GET.
	if c, err := publicationPreviewReturn(r.PostForm, header); err == nil && c.GroupID == header.GroupID && c.WeekID == header.PlanID {
		validDay := c.Day == ""
		if c.Day != "" {
			d, e := time.Parse("2006-01-02", c.Day)
			validDay = e == nil && header.WeekStart.Valid && !d.Before(header.WeekStart.Time) && d.Before(header.WeekStart.Time.AddDate(0, 0, 7))
		}
		if validDay {
			page.ReturnGroup, page.ReturnWeek, page.ReturnSession, page.ReturnDay = publicationContextFields(c)
			linked := c
			linked.SessionID = uuid.Nil
			if metadata, e := publicationRequestMetadata(r.PostForm, actor.ID, header.PlanID); e == nil {
				if _, e := trainingPublicationReviews.verify(r.PostForm.Get("review_token"), metadata, time.Now()); e == nil {
					linked = c
				}
			}
			page.ReturnURL = publicationReturnURL(linked)
			page.ReviewURL = publicationReviewURL(linked)
		}
	}

	return page
}
func (h StructuredTraining) publicationFailure(w http.ResponseWriter, r *http.Request, status int, page pages.TrainingPublicationReviewPage, err error) {
	if publicationScopeDenied(err) {
		h.System.NotFound(w, r)
		return
	}
	page.CanConfirm = false
	page.Recipients = nil
	page.NoChanges = false
	page.PrescriptionCount = 0
	page.AddedCount = 0
	page.ChangedCount = 0
	page.RemovedCount = 0
	page.UnchangedCount = 0
	switch {
	case errors.Is(err, db.ErrCommitOutcomeUnknown):
		page.Mode = "unknown"
		page.Error = "Não foi possível confirmar o resultado da publicação. Consulte a revisão atual antes de iniciar uma nova revisão."
		status = 409
	case errors.Is(err, errTrainingPublicationReviewInvalid):
		page.Error = "Esta confirmação é inválida ou expirou. Reveja explicitamente os destinatários antes de confirmar."
		status = 422
	case errors.Is(err, errTrainingPublicationReviewChanged) || errors.Is(err, errStructuredTrainingPublicationConflict):
		page.Error = "A semana, os destinatários ou a revisão atual mudaram. Os valores introduzidos foram mantidos; reveja novamente antes de confirmar."
		status = 409
	case errors.Is(err, errStructuredTrainingPublicationVariationConflict):
		page.Error = "Existem variações em conflito. Regresse ao planeamento e reveja-as antes de publicar."
		status = 422
	case errors.Is(err, errPublicationFirstEmpty):
		page.Error = "Ainda não existem sessões com destinatários elegíveis para uma primeira publicação. Reveja a semana e os atletas do grupo."
		status = 422
	case errors.Is(err, errTrainingPublicationReviewLimit):
		page.Error = "Não foi possível preparar esta publicação dentro dos limites do pedido. Reveja a semana antes de tentar novamente."
		status = 422
	case errors.Is(err, ErrTrainingScheduleWriteUnsupported):
		page.Error = trainingScheduleUnsupportedMessage
		status = 409
	default:
		page.Error = "Não foi possível concluir esta revisão. Consulte o estado atual antes de tentar novamente."
		status = 503
	}
	page.Errors.Add("form", page.Error)
	h.renderPublicationReview(w, r, status, page)
}
func (h StructuredTraining) renderPublicationReview(w http.ResponseWriter, r *http.Request, status int, page pages.TrainingPublicationReviewPage) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.TrainingPublicationReview(page).Render(r.Context(), w)
}
