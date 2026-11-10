package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const structuredReusePath = structuredPlannerPath + "/reutilizar"

type structuredReuseStore interface {
	ReadReuse(context.Context, uuid.UUID, reuseSourceSelector, *reuseTargetSelector) (reuseRead, error)
	ConfirmReuse(context.Context, reuseReviewed) (reuseSaveResult, error)
	AuthorizeReuse(context.Context, uuid.UUID, reuseSourceSelector, *reuseTargetSelector) error
	ReuseSeasons(context.Context, uuid.UUID, uuid.UUID) ([]dbgen.ListReuseTargetSeasonsRow, error)
	ReuseRoutines(context.Context, dbgen.ListReuseRoutinesParams) ([]dbgen.ListReuseRoutinesRow, error)
	ReuseSessions(context.Context, uuid.UUID, uuid.UUID) ([]dbgen.ListReuseSourceSessionsRow, error)
	ReuseSegments(context.Context, FocusedWorkoutEditRequest) ([]dbgen.TrainingSessionSegment, error)
	ReusePlan(context.Context, uuid.UUID, uuid.UUID) (dbgen.GetReusePlanRow, error)
	ReuseArtifactSize(context.Context, []byte) (int32, error)
}
type StructuredReuse struct {
	Store            structuredReuseStore
	Groups           TrainingGroupStore
	System           System
	PageMeta         components.PageMeta
	HTTPWriteTimeout time.Duration
}

func (s PostgresStructuredReuseStore) ReuseSeasons(ctx context.Context, actor, group uuid.UUID) ([]dbgen.ListReuseTargetSeasonsRow, error) {
	return dbgen.New(s.Pool).ListReuseTargetSeasons(ctx, dbgen.ListReuseTargetSeasonsParams{ActorID: actor, GroupID: group})
}
func (s PostgresStructuredReuseStore) ReuseRoutines(ctx context.Context, p dbgen.ListReuseRoutinesParams) ([]dbgen.ListReuseRoutinesRow, error) {
	return dbgen.New(s.Pool).ListReuseRoutines(ctx, p)
}
func (s PostgresStructuredReuseStore) ReuseSessions(ctx context.Context, actor, plan uuid.UUID) ([]dbgen.ListReuseSourceSessionsRow, error) {
	return dbgen.New(s.Pool).ListReuseSourceSessions(ctx, dbgen.ListReuseSourceSessionsParams{ActorID: actor, PlanID: plan})
}
func (s PostgresStructuredReuseStore) ReuseSegments(ctx context.Context, in FocusedWorkoutEditRequest) ([]dbgen.TrainingSessionSegment, error) {
	return dbgen.New(s.Pool).ListFocusedWorkoutSegments(ctx, dbgen.ListFocusedWorkoutSegmentsParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
}
func (s PostgresStructuredReuseStore) ReusePlan(ctx context.Context, actor, plan uuid.UUID) (dbgen.GetReusePlanRow, error) {
	return dbgen.New(s.Pool).GetReusePlan(ctx, dbgen.GetReusePlanParams{ActorID: actor, PlanID: plan})
}
func (s PostgresStructuredReuseStore) ReuseArtifactSize(ctx context.Context, raw []byte) (int32, error) {
	var size int32
	err := s.Pool.QueryRow(ctx, `SELECT pg_catalog.octet_length($1::jsonb::text)`, raw).Scan(&size)
	return size, err
}
func reuseID(values url.Values, name string) (uuid.UUID, error) {
	raw := values.Get(name)
	if raw == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil || id.String() != raw {
		return uuid.Nil, errFocusedWorkoutInvalid
	}
	return id, nil
}
func reuseSourceFromValues(values url.Values) (reuseSourceSelector, error) {
	s := reuseSourceSelector{Kind: values.Get("source_kind"), Day: values.Get("source_day")}
	var err error
	if s.PlanID, err = reuseID(values, "source_plan_id"); err != nil {
		return s, err
	}
	if s.SessionID, err = reuseID(values, "source_session_id"); err != nil {
		return s, err
	}
	if s.RoutineID, err = reuseID(values, "routine_id"); err != nil {
		return s, err
	}
	return s, s.validate()
}
func reuseTargetFromValues(values url.Values) (*reuseTargetSelector, validation.FieldErrors) {
	e := validation.FieldErrors{}
	s := reuseTargetSelector{WeekStart: values.Get("week_start"), Title: strings.TrimSpace(values.Get("week_title")), Description: strings.TrimSpace(values.Get("week_description"))}
	var err error
	for name, target := range map[string]*uuid.UUID{"target_group_id": &s.GroupID, "season_id": &s.SeasonID, "target_session_id": &s.SessionID, "target_segment_id": &s.SegmentID} {
		*target, err = reuseID(values, name)
		if err != nil {
			e.Add(name, "Escolha uma opção válida.")
		}
	}
	s.Load, err = optionalBoundedInt16(strings.TrimSpace(values.Get("planned_load_percentage")), 0, 100)
	if err != nil {
		e.Add("planned_load_percentage", "Indique uma percentagem entre 0 e 100 ou deixe em branco.")
	}
	if s.GroupID == uuid.Nil || s.SeasonID == uuid.Nil || s.WeekStart == "" {
		return nil, e
	}
	week, err := time.Parse("2006-01-02", s.WeekStart)
	if err != nil || !validWorkspaceDate(s.WeekStart) || week.Weekday() != time.Monday {
		e.Add("week_start", "Indique a segunda-feira da semana de destino.")
	}
	if len(e) > 0 {
		return nil, e
	}
	return &s, e
}
func reuseKeys(source reuseSourceMaterial) []string {
	keys := []string{}
	for _, item := range source.Items {
		if item.FragmentKind == "" {
			keys = append(keys, item.Key)
		}
	}
	return keys
}
func reuseScalarFormValid(v url.Values) bool {
	if len(v) > 1000 || len(v.Encode()) > focusedWorkoutMaxRequestBytes {
		return false
	}
	for name, values := range v {
		if name != "mapping_order" && len(values) != 1 {
			return false
		}
	}
	return true
}
func reuseSelectorValues(source reuseSourceSelector, target *reuseTargetSelector) url.Values {
	values := url.Values{"source_kind": {source.Kind}}
	if source.PlanID != uuid.Nil {
		values.Set("source_plan_id", source.PlanID.String())
	}
	if source.SessionID != uuid.Nil {
		values.Set("source_session_id", source.SessionID.String())
	}
	if source.RoutineID != uuid.Nil {
		values.Set("routine_id", source.RoutineID.String())
	}
	if source.Day != "" {
		values.Set("source_day", source.Day)
	}
	if target != nil {
		if target.GroupID != uuid.Nil {
			values.Set("target_group_id", target.GroupID.String())
		}
		if target.SeasonID != uuid.Nil {
			values.Set("season_id", target.SeasonID.String())
		}
		if target.WeekStart != "" {
			values.Set("week_start", target.WeekStart)
		}
		if target.SessionID != uuid.Nil {
			values.Set("target_session_id", target.SessionID.String())
		}
		if target.SegmentID != uuid.Nil {
			values.Set("target_segment_id", target.SegmentID.String())
		}
	}
	return values
}
func reuseTaskURL(source reuseSourceSelector, target *reuseTargetSelector) string {
	return structuredReusePath + "?" + reuseSelectorValues(source, target).Encode()
}
func reuseAdaptURL(group, week, session, day string) string {
	u, _ := url.Parse(structuredPlannerURL(group, week, session, day))
	q := u.Query()
	q.Set("task", "variation")
	u.RawQuery = q.Encode()
	u.Fragment = "training-variations"
	return u.String()
}
func (h StructuredReuse) Task(w http.ResponseWriter, r *http.Request)    { h.serve(w, r, "get") }
func (h StructuredReuse) Draft(w http.ResponseWriter, r *http.Request)   { h.serve(w, r, "draft") }
func (h StructuredReuse) Preview(w http.ResponseWriter, r *http.Request) { h.serve(w, r, "preview") }
func (h StructuredReuse) Confirm(w http.ResponseWriter, r *http.Request) { h.serve(w, r, "confirm") }
func (h StructuredReuse) render(w http.ResponseWriter, r *http.Request, status int, page pages.StructuredReuseTaskPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.StructuredReuseTask(page).Render(r.Context(), w)
}
func (h StructuredReuse) scopeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
	} else {
		h.System.InternalError(w, r)
	}
}
func (h StructuredReuse) failure(w http.ResponseWriter, r *http.Request, page pages.StructuredReuseTaskPage, err error, terminal bool) {
	status := 409
	page.Reviewing = false
	page.ReadOnly = false
	page.TargetPreview = nil
	switch {
	case errors.Is(err, db.ErrCommitOutcomeUnknown):
		page.Unknown = true
		page.Message = "Não foi possível confirmar o resultado. Consulte o destino antes de iniciar outra cópia; não volte a enviar esta confirmação."
	case errors.Is(err, errFocusedWorkoutLimit) || errors.Is(err, errFocusedArithmetic):
		status = 422
		if terminal {
			page.PreviewURL = ""
		}
		page.Message = "Esta operação excede os limites técnicos. Nada foi copiado parcialmente; reduza a origem ou o conteúdo e reveja o destino."
	case terminal:
		page.PreviewURL = ""
		page.Message = "Esta revisão já não pode ser utilizada com esta origem e este destino. Os valores introduzidos foram mantidos para copiar. Consulte o destino e inicie uma nova tarefa deliberadamente."
	default:
		page.Message = "A origem, o destino ou a configuração mudou. Os valores introduzidos foram mantidos. Reveja a operação antes de confirmar."
	}
	page.Errors.Add("form", page.Message)
	h.render(w, r, status, page)
}
func reuseFixedTargetEqual(a, b reuseTargetSelector) bool {
	return a.GroupID == b.GroupID && a.SeasonID == b.SeasonID && a.WeekStart == b.WeekStart && a.SessionID == b.SessionID && a.SegmentID == b.SegmentID
}
func reuseSourceContentEqual(a, b reuseSourceMaterial) bool {
	if a.Plan != nil && b.Plan != nil && a.Plan.ID == b.Plan.ID {
		copy := *b.Plan
		copy.UpdatedAt = a.Plan.UpdatedAt
		b.Plan = &copy
		b.Items = append([]reuseSourceItem(nil), b.Items...)
		for i := range b.Items {
			for _, old := range a.Items {
				if b.Items[i].Key == old.Key {
					b.Items[i].Graph.Context.PlanUpdatedAt = old.Graph.Context.PlanUpdatedAt
				}
			}
		}
	}
	return focusedGraphJSONEqual(a, b)
}
func reuseTargetContentEqual(a, b reuseTargetMaterial) bool {
	if (a.Existing == nil) != (b.Existing == nil) {
		return false
	}
	if a.Existing == nil {
		return true
	}
	return focusedGraphJSONEqual(a.Existing, b.Existing)
}
func (h StructuredReuse) serve(w http.ResponseWriter, r *http.Request, action string) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	user, ok := CurrentUserFromContext(r.Context())
	if !ok || h.Store == nil {
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
	values := r.URL.Query()
	var probe reuseReviewPayload
	priorRaw, priorToken := "", ""
	if action != "get" {
		if err := r.ParseForm(); err != nil {
			h.System.RequestRejected(w, r)
			return
		}
		values = r.PostForm
		priorRaw, priorToken = values.Get("prior_proposal"), values.Get("prior_review_token")
		if action == "confirm" {
			for name, entries := range values {
				if (name != "intent" && name != "proposal" && name != "review_token" && name != "gorilla.csrf.Token") || len(entries) != 1 {
					h.System.RequestRejected(w, r)
					return
				}
			}
			if values.Get("intent") != "confirm" {
				h.System.RequestRejected(w, r)
				return
			}
			priorRaw, priorToken = values.Get("proposal"), values.Get("review_token")
			if focusedStrictJSON([]byte(priorRaw), &probe) != nil {
				h.System.RequestRejected(w, r)
				return
			}
			values = probe.Values
		}
	}
	if !reuseScalarFormValid(values) {
		h.System.RequestRejected(w, r)
		return
	}
	if action == "get" && values.Get("source_kind") == "ROUTINE" && values.Get("routine_id") == "" {
		page, err := h.libraryPage(r, user, values)
		if err != nil {
			h.scopeError(w, r, err)
			return
		}
		h.render(w, r, 200, page)
		return
	}
	source, err := reuseSourceFromValues(values)
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	target, fields := reuseTargetFromValues(values)
	// A verified repeat reaches the transaction's scoped event/destination
	// probe before an enlarged same-plan source hits the initial 64-item cap.
	if action == "confirm" {
		old, reviewed, verifyErr := focusedWorkoutReviews.verifyReuse(priorRaw, priorToken, user.ID, source, time.Now())
		if verifyErr == nil {
			if err = h.Store.AuthorizeReuse(ctx, user.ID, source, target); err != nil {
				h.scopeError(w, r, err)
				return
			}
			h.confirmReviewed(w, r, user, old, reviewed, source, target)
			return
		}
	}
	read, err := h.Store.ReadReuse(ctx, user.ID, source, target)
	if err != nil {
		if errors.Is(err, errFocusedWorkoutLimit) || errors.Is(err, errFocusedArithmetic) || errors.Is(err, ErrTrainingSnapshotUnsupported) {
			page := h.basePage(r, user, values)
			page.ReloadURL = reuseTaskURL(source, target)
			if read.Source.Plan != nil {
				plan := read.Source.Plan
				page.Source = pages.StructuredReuseContext{Label: "Origem", Group: plan.GroupName, Season: plan.SeasonName, Week: plan.Title}
				page.ReturnURL = structuredPlannerURL(plan.TrainingGroupID.String(), plan.ID.String(), "", plan.WeekStart.Time.Format("2006-01-02"))
			}
			h.failure(w, r, page, err, true)
			return
		}
		h.scopeError(w, r, err)
		return
	}
	if action == "get" || (action == "draft" && r.PostForm.Get("command") == "refresh" && priorRaw == "" && priorToken == "") {
		values = cloneFocusedValues(values)
		if read.Source.Plan != nil {
			if values.Get("target_group_id") == "" {
				values.Set("target_group_id", read.Source.Plan.TrainingGroupID.String())
			}
			if values.Get("season_id") == "" {
				values.Set("season_id", read.Source.Plan.SeasonID.String())
			}
		}
		if _, exists := values["mapping_order"]; !exists {
			values["mapping_order"] = reuseKeys(read.Source)
		}
		for _, item := range read.Source.Items {
			if item.FragmentKind != "" {
				continue
			}
			day := ""
			if target != nil {
				offset := 0
				if source.Kind == "WEEK" && read.Source.Plan != nil {
					old, _ := time.Parse("2006-01-02", item.Graph.Schedule.Date)
					offset = int(old.Sub(read.Source.Plan.WeekStart.Time) / (24 * time.Hour))
				}
				day = read.Target.Configuration.WeekStart.Time.AddDate(0, 0, offset).Format("2006-01-02")
			}
			for name, v := range initialReuseMapping(item, day) {
				if name == "mapping_order" {
					continue
				}
				_, exists := values[name]
				if !exists || (action == "draft" && values.Get(name) == "" && name != "mapping_order") {
					values[name] = v
				}
			}
		}
	}
	var prior *reuseReviewPayload
	var reviewed reuseReviewed
	var proofErr, identityErr error
	display := read
	if priorRaw != "" || priorToken != "" {
		old, verified, verifyErr := focusedWorkoutReviews.verifyReuse(priorRaw, priorToken, user.ID, source, time.Now())
		proofErr = verifyErr
		if verifyErr == nil {
			prior = &old
			reviewed = verified
			if target == nil || !reuseFixedTargetEqual(old.Target.Selector, *target) || !reuseSourceContentEqual(old.Source, read.Source) || read.Target == nil || !reuseTargetContentEqual(old.Target, *read.Target) {
				identityErr = errReuseChanged
				display.Source = old.Source
			}
		}
	}
	parsed, parseErr := parseReuseValues(values, reuseKeys(display.Source))
	if parseErr == nil {
		values = parsed
	}
	page, pageErr := h.page(r, user, display, values)
	if pageErr != nil {
		h.scopeError(w, r, pageErr)
		return
	}
	page.Errors = fields
	page.ProposalJSON, page.ReviewToken = priorRaw, priorToken
	if proofErr != nil {
		h.failure(w, r, page, proofErr, true)
		return
	}
	if identityErr != nil {
		h.failure(w, r, page, identityErr, true)
		return
	}
	if parseErr != nil {
		page.Errors.Add("form", "Reveja os campos e a ordem da operação.")
		if action != "get" {
			h.render(w, r, 422, page)
			return
		}
	}
	if action == "get" {
		h.render(w, r, 200, page)
		return
	}
	if action == "confirm" {
		h.failure(w, r, page, errFocusedWorkoutInvalid, true)
		return
	}
	_ = reviewed

	command := r.PostForm.Get("command")
	if action == "draft" {
		if command != "refresh" {
			h.System.RequestRejected(w, r)
			return
		}
		h.render(w, r, 200, page)
		return
	}
	if command != "preview" {
		h.System.RequestRejected(w, r)
		return
	}
	if !read.State.PartialV2Enabled {
		h.failure(w, r, page, errFocusedWorkoutClosed, false)
		return
	}
	payload, errors := buildReuseProposal(user.ID, read, values, prior)
	if len(errors) > 0 {
		page.Errors = errors
		h.render(w, r, 422, page)
		return
	}
	for _, destination := range payload.Proposal.Destinations {
		graph := destination.Graph
		graph.References = payload.References
		artifact, err := focusedGraphArtifact(graph)
		if err != nil {
			h.failure(w, r, page, err, false)
			return
		}
		size, err := h.Store.ReuseArtifactSize(ctx, artifact)
		if err != nil {
			h.scopeError(w, r, err)
			return
		}
		if size > focusedWorkoutMaxArtifactBytes {
			h.failure(w, r, page, errFocusedWorkoutLimit, false)
			return
		}
	}
	raw, token, err := focusedWorkoutReviews.signReuse(payload, time.Now())
	if err != nil {
		h.failure(w, r, page, err, false)
		return
	}
	page.Reviewing = true
	page.ProposalJSON, page.ReviewToken = raw, token
	component, viewErr := reuseTargetPreview(payload)
	if viewErr != nil {
		h.failure(w, r, page, viewErr, false)
		return
	}
	page.TargetPreview = component
	h.render(w, r, 200, page)
}
func reuseJoin(parts []templ.Component) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		for _, part := range parts {
			if err := part.Render(ctx, w); err != nil {
				return err
			}
		}
		return nil
	})
}
func reuseSourcePreview(read reuseRead) (templ.Component, error) {
	parts := []templ.Component{}
	for _, item := range read.Source.Items {
		graph := item.Graph
		graph.References = read.References
		if item.FragmentKind != "" || item.DurationSeconds != nil {
			segments, err := projectFocusedFragment(graph)
			if err != nil {
				return nil, err
			}
			if item.FragmentKind != "" {
				parts = append(parts, pages.StructuredWorkoutFragment(segments))
			} else {
				// V1 SESSION routines have a real title/description/duration,
				// but no source calendar. Preserve all instructions without
				// filling When or a typed schedule from display text.
				view := pages.StructuredTrainingSession{ID: graph.SessionID.String(), Title: graph.Title, Description: graph.Description, EntryKind: string(graph.Kind), Segments: segments}
				parts = append(parts, pages.StructuredWorkoutInstructions(view))
			}
		} else {
			view, err := projectFocusedGraph(graph)
			if err != nil {
				return nil, err
			}
			parts = append(parts, pages.StructuredWorkoutInstructions(view))
		}
	}
	return reuseJoin(parts), nil
}
func reuseTargetPreview(p reuseReviewPayload) (templ.Component, error) {
	parts := []templ.Component{}
	for _, d := range p.Proposal.Destinations {
		graph := d.Graph
		graph.References = p.References
		view, err := projectFocusedGraph(graph)
		if err != nil {
			return nil, err
		}
		parts = append(parts, pages.StructuredWorkoutInstructions(view))
	}
	return reuseJoin(parts), nil
}

func (h StructuredReuse) confirmReviewed(w http.ResponseWriter, r *http.Request, user CurrentUser, p reuseReviewPayload, reviewed reuseReviewed, source reuseSourceSelector, target *reuseTargetSelector) {
	result, err := h.Store.ConfirmReuse(r.Context(), reviewed)
	if err == nil {
		httpx.Redirect(w, r, structuredPlannerURL(result.GroupID.String(), result.PlanID.String(), result.SessionID.String(), result.Day), http.StatusSeeOther)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if errors.Is(err, db.ErrCommitOutcomeUnknown) {
		page := h.basePage(r, user, p.Values)
		page.Source = reuseSourceContext(p.Source)
		page.ProposalJSON, page.ReviewToken = "", ""
		page.DestinationURL = structuredPlannerURL(p.Target.Selector.GroupID.String(), p.Proposal.TargetPlanID.String(), p.Proposal.Destinations[0].Graph.SessionID.String(), p.Proposal.Destinations[0].Graph.Schedule.Date)
		page.ReloadURL = reuseTaskURL(source, target)
		h.failure(w, r, page, err, false)
		return
	}
	if accessErr := h.Store.AuthorizeReuse(r.Context(), user.ID, source, target); accessErr != nil {
		h.scopeError(w, r, accessErr)
		return
	}
	fresh, readErr := h.Store.ReadReuse(r.Context(), user.ID, source, target)
	if readErr != nil {
		if errors.Is(readErr, errFocusedWorkoutLimit) {
			page := h.basePage(r, user, p.Values)
			page.ReloadURL = reuseTaskURL(source, target)
			page.ProposalJSON, page.ReviewToken = r.PostForm.Get("proposal"), r.PostForm.Get("review_token")
			h.failure(w, r, page, readErr, true)
			return
		}
		h.scopeError(w, r, readErr)
		return
	}
	display := fresh
	display.Source = p.Source
	page, pageErr := h.page(r, user, display, p.Values)
	if pageErr != nil {
		h.scopeError(w, r, pageErr)
		return
	}
	page.ProposalJSON, page.ReviewToken = r.PostForm.Get("proposal"), r.PostForm.Get("review_token")
	terminal := !reuseSourceContentEqual(p.Source, fresh.Source) || fresh.Target == nil || !reuseTargetContentEqual(p.Target, *fresh.Target) || (errors.Is(err, errFocusedEditReadOnly) || errors.Is(err, errReuseDestinationChanged))
	if fresh.Target != nil && fresh.Target.Existing != nil && !focusedEditEligible(fresh.Target.Existing.Session, fresh.DecisionAt) {
		terminal = true
	}
	h.failure(w, r, page, err, terminal)
}
func (s PostgresStructuredReuseStore) AuthorizeReuse(ctx context.Context, actor uuid.UUID, source reuseSourceSelector, target *reuseTargetSelector) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	if source.Kind == "ROUTINE" {
		if _, err = q.GetReuseRoutine(ctx, dbgen.GetReuseRoutineParams{ActorID: actor, RoutineID: source.RoutineID}); err != nil {
			return err
		}
	} else {
		if _, err = q.GetReusePlan(ctx, dbgen.GetReusePlanParams{ActorID: actor, PlanID: source.PlanID}); err != nil {
			return err
		}
		if source.Kind == "SESSION" {
			if _, err = q.GetFocusedWorkoutSession(ctx, dbgen.GetFocusedWorkoutSessionParams{ActorID: actor, PlanID: source.PlanID, SessionID: source.SessionID}); err != nil {
				return err
			}
		}
	}
	if target != nil {
		week, err := focusedDay(target.WeekStart)
		if err != nil {
			return err
		}
		row, err := q.GetReuseTargetContext(ctx, dbgen.GetReuseTargetContextParams{ActorID: actor, GroupID: target.GroupID, SeasonID: target.SeasonID, WeekStart: week})
		if err != nil {
			return err
		}
		if target.SessionID != uuid.Nil {
			if row.ExistingPlanID == nil {
				return pgx.ErrNoRows
			}
			if _, err = q.GetFocusedWorkoutSession(ctx, dbgen.GetFocusedWorkoutSessionParams{ActorID: actor, PlanID: *row.ExistingPlanID, SessionID: target.SessionID}); err != nil {
				return err
			}
			if target.SegmentID != uuid.Nil {
				segments, err := q.ListFocusedWorkoutSegments(ctx, dbgen.ListFocusedWorkoutSegmentsParams{ActorID: actor, PlanID: *row.ExistingPlanID, SessionID: target.SessionID})
				if err != nil {
					return err
				}
				found := false
				for _, segment := range segments {
					if segment.ID == target.SegmentID {
						found = true
					}
				}
				if !found {
					return pgx.ErrNoRows
				}
			}
		}
	}
	return tx.Commit(ctx)
}
