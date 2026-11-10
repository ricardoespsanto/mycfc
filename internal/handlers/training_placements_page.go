package handlers

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
)

type trainingPlacementPageContext struct {
	Group       TrainingGroupRecord
	ProgrammeID uuid.UUID
	Seasons     []dbgen.ListReuseTargetSeasonsRow
}
type trainingPlacementView struct {
	PersonID   uuid.UUID
	PersonName string
	Revision   int64
	Timeline   []dbgen.TrainingPlacementTimelineRow
	Groups     []dbgen.TrainingPlacementGroupChoicesRow
}
type trainingPlacementSaveResult struct {
	ChangeID, SourcePlacementID, DestinationPlacementID, ReturnPlacementID uuid.UUID
	Revision                                                               int64
	Replayed, CleanupFailed                                                bool
}
type TrainingPlacementStore interface {
	PlacementContext(context.Context, uuid.UUID, uuid.UUID) (trainingPlacementPageContext, error)
	PlacementView(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string) (trainingPlacementView, error)
	ReadPlacement(context.Context, uuid.UUID, trainingPlacementCommand, bool) (trainingPlacementRead, error)
	ConfirmPlacement(context.Context, trainingPlacementReviewed) (trainingPlacementSaveResult, error)
	ReadPlacementCancellation(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (trainingPlacementCancellationRead, error)
	ConfirmPlacementCancellation(context.Context, trainingPlacementCancelReviewed) (trainingPlacementSaveResult, error)
}
type TrainingPlacements struct {
	Store            TrainingPlacementStore
	System           System
	PageMeta         components.PageMeta
	Sessions         *scs.SessionManager
	HTTPWriteTimeout time.Duration
}

func placementURL(group, person uuid.UUID, season uuid.UUID, effective string) string {
	v := url.Values{"group_id": {group.String()}, "person_id": {person.String()}}
	if season != uuid.Nil {
		v.Set("season_id", season.String())
	}
	if validWorkspaceDate(effective) {
		v.Set("effective_on", effective)
	}
	return trainingPlacementsPath + "?" + v.Encode()
}
func placementForm(v url.Values) pages.TrainingPlacementForm {
	return pages.TrainingPlacementForm{GroupID: v.Get("group_id"), PersonID: v.Get("person_id"), CommandID: v.Get("command_id"), HeadRevision: v.Get("head_revision"), Kind: v.Get("kind"), SeasonID: v.Get("season_id"), SourcePlacementID: v.Get("source_placement_id"), TargetGroupID: v.Get("target_group_id"), EffectiveOn: v.Get("effective_on"), TemporaryUntil: v.Get("temporary_until"), ChangeID: v.Get("change_id"), CancellationID: v.Get("cancellation_id"), Errors: validation.FieldErrors{}}
}
func (h TrainingPlacements) page(r *http.Request, actor CurrentUser, c trainingPlacementPageContext, v url.Values) pages.TrainingPlacementsPage {
	meta := h.PageMeta
	meta.Title = "Colocação no grupo de treino | MyCFCoimbra"
	meta.CurrentPath = trainingGroupsPath
	meta.CurrentUserName = actor.Name
	meta.CurrentUserID = actor.ID.String()
	meta.EmailVerificationPending = !actor.IsDependent && !actor.EmailVerified
	meta.Navigation = dashboardNavigation(actor)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	p := pages.TrainingPlacementsPage{Meta: meta, CSRFField: meta.CSRFField, Mode: "editor", Title: "Preparar colocação", Form: placementForm(v), Person: pages.TrainingPlacementPerson{ID: v.Get("person_id")}, ContextGroup: pages.TrainingPlacementGroup{ID: c.Group.ID.String(), Name: c.Group.Name, ScopeName: c.Group.ScopeName}, EditorURL: trainingPlacementsPath, PreviewURL: trainingPlacementsPath + "/prever", ConfirmURL: trainingPlacementsPath + "/confirmar", CancelPreviewURL: trainingPlacementsPath + "/cancelar/prever", CancelConfirmURL: trainingPlacementsPath + "/cancelar/confirmar", ReturnURL: trainingGroupsPath + "/" + c.Group.ID.String()}
	for _, s := range c.Seasons {
		p.Seasons = append(p.Seasons, pages.TrainingPlacementSeason{ID: s.ID.String(), Name: s.Name, StartsOn: placementDateString(s.StartsOn), EndsOn: placementDateString(s.EndsOn), Selected: s.ID.String() == p.Form.SeasonID})
	}
	person, e := uuid.Parse(p.Form.PersonID)
	if e == nil {
		season, _ := uuid.Parse(p.Form.SeasonID)
		p.NewTaskURL = placementURL(c.Group.ID, person, season, p.Form.EffectiveOn)
	}
	return p
}
func (h TrainingPlacements) render(w http.ResponseWriter, r *http.Request, status int, p pages.TrainingPlacementsPage) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.TrainingPlacements(p).Render(r.Context(), w)
}
func placementTimelineRows(rows []dbgen.TrainingPlacementTimelineRow, group, person uuid.UUID) []pages.TrainingPlacementTimelineRow {
	out := []pages.TrainingPlacementTimelineRow{}
	for _, x := range rows {
		status := "Colocação registada"
		if x.SupersededByChangeID != nil {
			status = "Substituída"
		}
		if x.CancelledByChangeID != nil {
			status = "Cancelada"
		}
		v := url.Values{"group_id": {group.String()}, "person_id": {person.String()}, "change_id": {x.ChangeID.String()}}
		out = append(out, pages.TrainingPlacementTimelineRow{ID: x.ID.String(), GroupID: x.GroupID.String(), Group: x.GroupName, StartsOn: placementDateString(x.StartsOn), EndsOn: placementDateString(x.EndsOn), Status: status, Version: strconv.FormatInt(x.Version, 10), CancelURL: trainingPlacementsPath + "?" + v.Encode()})
	}
	return out
}
func placementFillView(p *pages.TrainingPlacementsPage, v trainingPlacementView) {
	p.Person.ID = v.PersonID.String()
	p.Person.Name = v.PersonName
	p.Timeline = placementTimelineRows(v.Timeline, mustPlacementUUID(p.ContextGroup.ID), v.PersonID)
	for _, s := range v.Timeline {
		if s.CancelledByChangeID != nil || s.SupersededByChangeID != nil {
			continue
		}
		p.Sources = append(p.Sources, pages.TrainingPlacementOption{ID: s.ID.String(), Name: s.GroupName + " · " + placementDateString(s.StartsOn), Selected: p.Form.SourcePlacementID == s.ID.String()})
	}
	for _, g := range v.Groups {
		p.Destinations = append(p.Destinations, pages.TrainingPlacementOption{ID: g.ID.String(), Name: g.Name, Selected: p.Form.TargetGroupID == g.ID.String()})
	}
}
func mustPlacementUUID(s string) uuid.UUID { id, _ := uuid.Parse(s); return id }
func placementReadView(r trainingPlacementRead) trainingPlacementView {
	v := trainingPlacementView{Revision: r.revision(), Timeline: r.Timeline, Groups: r.Groups}
	if r.Subject != nil {
		v.PersonID = r.Subject.SubjectID
		v.PersonName = r.Subject.SubjectName
	} else if r.Source != nil {
		v.PersonID = r.Source.SubjectID
		v.PersonName = r.Source.SubjectName
	}
	return v
}
func placementProjectedRow(s trainingPlacementSpan) pages.TrainingPlacementTimelineRow {
	status := "Proposta"
	if s.Superseded {
		status = "Substituída sem apagar o histórico"
	}
	if s.Cancelled {
		status = "Cancelada, com histórico preservado"
	}
	id := ""
	if s.ID != uuid.Nil {
		id = s.ID.String()
	}
	return pages.TrainingPlacementTimelineRow{ID: id, GroupID: s.GroupID.String(), Group: s.GroupName, StartsOn: s.StartsOn, EndsOn: s.EndsOn, Status: status, Version: strconv.FormatInt(s.Version, 10)}
}
func placementReviewPage(p *pages.TrainingPlacementsPage, read trainingPlacementRead, payload trainingPlacementReviewPayload, raw, token string) {
	p.Mode = "review"
	p.Title = "Rever alteração de grupo"
	review := &pages.TrainingPlacementReview{Kind: string(payload.Command.Kind), Headline: "Reveja as datas e os grupos antes de confirmar.", EffectiveOn: payload.Command.EffectiveOn, TemporaryUntil: payload.Command.TemporaryUntil, Notice: "As instruções futuras do grupo anterior deixam de estar disponíveis quando a mudança começa. As instruções do destino e do regresso precisam de publicação explícita.", Proposal: raw, ReviewToken: token, CanConfirm: true}
	if payload.Source != nil {
		review.Before = append(review.Before, placementProjectedRow(*payload.Source))
	}
	for _, s := range []*trainingPlacementSpan{payload.Projection.Source, payload.Projection.Destination, payload.Projection.Return} {
		if s != nil {
			review.After = append(review.After, placementProjectedRow(*s))
		}
	}
	if payload.Projection.Return != nil {
		review.ReturnOn = payload.Projection.Return.StartsOn
		review.ReturnGroup = payload.Projection.Return.GroupName
	}
	for _, x := range read.Instructions {
		status := "Histórico preservado"
		if x.WillWithdraw {
			status = "Acesso retirado quando a mudança se tornar efetiva"
		}
		if x.RequiresPolicy {
			status = "Requer decisão antes de continuar"
			review.CanConfirm = false
		}
		when := placementDateString(x.SessionOn)
		if x.StartsAt.Valid {
			when = x.StartsAt.Time.In(lisbonLocation()).Format("2006-01-02 15:04")
		}
		review.Instructions = append(review.Instructions, pages.TrainingPlacementInstructionRow{Title: x.Title, When: when, Status: status, RequiresPolicy: x.RequiresPolicy})
	}
	p.Review = review
}
