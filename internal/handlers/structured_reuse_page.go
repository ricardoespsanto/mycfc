package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
)

func reusePageNumber(v url.Values, name string) int {
	n, err := strconv.Atoi(v.Get(name))
	if err != nil || n < 0 || n > 99 {
		return 0
	}
	return n
}
func reuseClockChoices(day, clock string) []pages.StructuredWorkoutChoice {
	times := reuseClockOccurrences(day, clock)
	if len(times) < 2 {
		return nil
	}
	out := []pages.StructuredWorkoutChoice{}
	for _, t := range times {
		out = append(out, pages.StructuredWorkoutChoice{Value: t.Format(time.RFC3339Nano), Label: t.In(lisbonLocation()).Format("02/01/2006 15:04:05.999999999 MST -07:00")})
	}
	return out
}
func (h StructuredReuse) basePage(r *http.Request, user CurrentUser, values url.Values) pages.StructuredReuseTaskPage {
	meta := h.PageMeta
	meta.Title = "Reutilizar treino | MyCFCoimbra"
	meta.PageLabel = "Reutilizar treino"
	meta.CurrentPath = structuredPlannerPath
	meta.CurrentUserName = user.Name
	meta.CurrentUserID = user.ID.String()
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return pages.StructuredReuseTaskPage{Meta: meta, CSRFField: meta.CSRFField, Values: cloneFocusedValues(values), Errors: validation.FieldErrors{}, Source: pages.StructuredReuseContext{Label: "Origem"}, Target: pages.StructuredReuseContext{Label: "Destino"}, DraftURL: structuredReusePath + "/rascunho", PreviewURL: structuredReusePath + "/prever", ConfirmURL: structuredReusePath + "/confirmar", ReturnURL: trainingGroupsPath, ReloadURL: structuredReusePath}
}
func reusePartialTarget(values url.Values) *reuseTargetSelector {
	target := &reuseTargetSelector{}
	target.GroupID, _ = reuseID(values, "target_group_id")
	target.SeasonID, _ = reuseID(values, "season_id")
	target.SessionID, _ = reuseID(values, "target_session_id")
	target.SegmentID, _ = reuseID(values, "target_segment_id")
	if validWorkspaceDate(values.Get("week_start")) {
		target.WeekStart = values.Get("week_start")
	}
	return target
}
func reuseSourceContext(source reuseSourceMaterial) pages.StructuredReuseContext {
	out := pages.StructuredReuseContext{Label: "Origem"}
	if p := source.Plan; p != nil {
		out.Group = p.GroupName
		out.Season = p.SeasonName
		out.Week = p.Title
		out.Day = source.Selector.Day
		out.Item = fmt.Sprintf("%d sessões ativas", len(source.Items))
		if len(source.Items) == 1 {
			out.Item = source.Items[0].Graph.Title
			out.Day = source.Items[0].Graph.Schedule.Date
		}
	}
	if routine := source.Routine; routine != nil {
		out.Item = routine.Name
		out.Week = "Rotina configurada"
	}
	return out
}
func (h StructuredReuse) page(r *http.Request, user CurrentUser, read reuseRead, values url.Values) (pages.StructuredReuseTaskPage, error) {
	page := h.basePage(r, user, values)
	page.Source = reuseSourceContext(read.Source)
	partial := reusePartialTarget(values)
	page.ReloadURL = reuseTaskURL(read.Source.Selector, partial)
	if read.Source.Plan != nil {
		source := read.Source.Plan
		day := source.WeekStart.Time.Format("2006-01-02")
		if read.Source.Selector.Kind == "DAY" {
			day = read.Source.Selector.Day
		} else if read.Source.Selector.Kind == "SESSION" && len(read.Source.Items) > 0 {
			day = read.Source.Items[0].Graph.Schedule.Date
		}
		page.ReturnURL = structuredPlannerURL(source.TrainingGroupID.String(), source.ID.String(), read.Source.Selector.SessionID.String(), day)
		if read.Source.Selector.SessionID == uuid.Nil {
			page.ReturnURL = structuredPlannerURL(source.TrainingGroupID.String(), source.ID.String(), "", day)
		}
	}
	if target := read.Target; target != nil {
		page.Target.Group = target.Configuration.GroupName
		page.Target.Season = target.Configuration.SeasonName
		page.Target.Week = target.Selector.WeekStart
		page.Target.Item = "Nova semana em rascunho"
		if target.Plan != nil {
			page.Target.Week = target.Plan.Title
		}
		if target.Existing != nil {
			page.Target.Item = target.Existing.Session.Title
			page.Target.Day = read.TargetGraph.Schedule.Date
		}
	}
	if len(read.Source.Items) == 0 {
		page.NoWork = true
		page.Message = "A origem não contém sessões ativas para copiar. Nada foi alterado."
	} else {
		component, err := reuseSourcePreview(read)
		if err != nil {
			return page, err
		}
		page.SourcePreview = component
	}
	for _, item := range read.Source.Items {
		if item.FragmentKind != "" {
			continue
		}
		mapping := pages.StructuredReuseMapping{Key: item.Key, SourceLabel: item.Graph.Title, Precision: string(item.Graph.Schedule.Precision)}
		if item.Graph.Schedule.Band != nil {
			mapping.Band = string(*item.Graph.Schedule.Band)
		}
		if item.DurationSeconds != nil {
			mapping.Duration = fmt.Sprintf("%d s", *item.DurationSeconds)
			mapping.Precision = "INTERVAL"
		}
		mapping.NeedsStart = mapping.Precision == "INTERVAL" || mapping.Precision == "START_ONLY"
		mapping.NeedsEnd = mapping.Precision == "INTERVAL" && item.DurationSeconds == nil
		prefix := reuseMappingPrefix(item.Key)
		mapping.StartOccurrences = reuseClockChoices(values.Get(prefix+".date"), values.Get(prefix+".start_time"))
		mapping.EndOccurrences = reuseClockChoices(values.Get(prefix+".end_date"), values.Get(prefix+".end_time"))
		page.Mappings = append(page.Mappings, mapping)
	}
	if !read.State.PartialV2Enabled {
		page.ReadOnly = true
		page.Message = "A criação de cópias está temporariamente indisponível. Contacte um administrador."
	}
	groupPage := reusePageNumber(values, "group_page")
	if h.Groups != nil {
		groups, err := h.Groups.ListGroups(r.Context(), user.ID, int32(groupPage*20))
		if err != nil {
			return page, err
		}
		seen := map[uuid.UUID]bool{}
		for _, group := range groups {
			page.Groups = append(page.Groups, pages.StructuredWorkoutChoice{Value: group.ID.String(), Label: group.Name})
			seen[group.ID] = true
		}
		if partial.GroupID != uuid.Nil && !seen[partial.GroupID] {
			selected, err := h.Groups.GetGroup(r.Context(), user.ID, partial.GroupID)
			if err != nil {
				return page, err
			}
			page.Groups = append(page.Groups, pages.StructuredWorkoutChoice{Value: selected.ID.String(), Label: selected.Name})
		}
		base := reuseSelectorValues(read.Source.Selector, partial)
		if groupPage > 0 {
			base.Set("group_page", strconv.Itoa(groupPage-1))
			page.GroupPreviousURL = structuredReusePath + "?" + base.Encode()
		}
		if len(groups) == 20 {
			base.Set("group_page", strconv.Itoa(groupPage+1))
			page.GroupNextURL = structuredReusePath + "?" + base.Encode()
		}
	}
	if partial.GroupID != uuid.Nil {
		seasons, err := h.Store.ReuseSeasons(r.Context(), user.ID, partial.GroupID)
		if err != nil {
			return page, err
		}
		if len(seasons) > 100 {
			page.PreviewURL = ""
			page.Message = "Há demasiadas épocas para esta lista. Contacte um administrador para rever a configuração."
		} else {
			for _, season := range seasons {
				page.Seasons = append(page.Seasons, pages.StructuredWorkoutChoice{Value: season.ID.String(), Label: season.Name + " · " + season.StartsOn.Time.Format("2006-01-02") + "–" + season.EndsOn.Time.Format("2006-01-02")})
			}
		}
	}
	if len(read.Source.Items) > 0 && read.Source.Items[0].FragmentKind != "" {
		page.Sessions = []pages.StructuredWorkoutChoice{}
		if read.Source.Items[0].FragmentKind == "BLOCK" {
			page.Segments = []pages.StructuredWorkoutChoice{}
		}
		if read.Target != nil && read.Target.Plan != nil {
			sessions, err := h.Store.ReuseSessions(r.Context(), user.ID, read.Target.Plan.ID)
			if err != nil {
				return page, err
			}
			if len(sessions) > 64 {
				page.PreviewURL = ""
				page.Message = "A semana excede o limite técnico desta lista. Nada foi alterado."
			} else {
				for _, session := range sessions {
					row := reuseSessionRow(session)
					if ((row.EntryKind == "TRAINING" || row.EntryKind == "COMPETITION") && focusedEditEligible(row, read.DecisionAt)) || session.ID == partial.SessionID {
						page.Sessions = append(page.Sessions, pages.StructuredWorkoutChoice{Value: session.ID.String(), Label: session.Title + " · " + session.SessionOn.Time.Format("2006-01-02")})
					}
				}
			}
			if partial.SessionID != uuid.Nil && read.Source.Items[0].FragmentKind == "BLOCK" {
				segments, err := h.Store.ReuseSegments(r.Context(), FocusedWorkoutEditRequest{user.ID, read.Target.Plan.ID, partial.SessionID})
				if err != nil {
					return page, err
				}
				if len(segments) > 8 {
					page.PreviewURL = ""
					page.Message = "A sessão excede o limite técnico desta lista. Nada foi alterado."
				} else {
					for _, segment := range segments {
						page.Segments = append(page.Segments, pages.StructuredWorkoutChoice{Value: segment.ID.String(), Label: structuredModalityName(string(segment.Modality)) + " · " + segment.Title})
					}
				}
			}
		}
	}
	page.RoutineURL = reuseTaskURL(reuseSourceSelector{Kind: "ROUTINE"}, partial)
	return page, nil
}
func (h StructuredReuse) libraryPage(r *http.Request, user CurrentUser, values url.Values) (pages.StructuredReuseTaskPage, error) {
	page := h.basePage(r, user, values)
	page.ShowRoutineLibrary = true
	page.Source.Item = "Biblioteca de rotinas"
	partial := reusePartialTarget(values)
	page.ReloadURL = reuseTaskURL(reuseSourceSelector{Kind: "ROUTINE"}, partial)
	page.RoutineURL = page.ReloadURL
	if values.Get("source_plan_id") != "" || values.Get("source_session_id") != "" || values.Get("source_day") != "" {
		return page, errFocusedWorkoutInvalid
	}
	for name, max := range map[string]int{"routine_query": 100, "routine_modality": 20, "routine_objective": 40, "routine_tag": 80} {
		if !validTrainingText(values.Get(name), 0, max) {
			page.Errors.Add(name, "Reveja este filtro.")
		}
	}
	if len(page.Errors) > 0 {
		return page, nil
	}
	if partial.GroupID != uuid.Nil && h.Groups != nil {
		group, err := h.Groups.GetGroup(r.Context(), user.ID, partial.GroupID)
		if err != nil {
			return page, err
		}
		page.Target.Group = group.Name
		page.ReturnURL = trainingGroupsPath + "/" + group.ID.String()
	}
	number := reusePageNumber(values, "routine_page")
	rows, err := h.Store.ReuseRoutines(r.Context(), dbgen.ListReuseRoutinesParams{ActorID: user.ID, Query: values.Get("routine_query"), Modality: values.Get("routine_modality"), Objective: values.Get("routine_objective"), Tag: values.Get("routine_tag"), RowOffset: int32(number * 20)})
	if err != nil {
		return page, err
	}
	more := len(rows) > 20
	if more {
		rows = rows[:20]
	}
	for _, row := range rows {
		page.Routines = append(page.Routines, pages.StructuredReuseRoutine{ID: row.ID.String(), Name: row.Name, Kind: string(row.Kind), Description: row.Description, URL: reuseTaskURL(reuseSourceSelector{Kind: "ROUTINE", RoutineID: row.ID}, partial)})
	}
	q := reuseSelectorValues(reuseSourceSelector{Kind: "ROUTINE"}, partial)
	for _, name := range []string{"routine_query", "routine_modality", "routine_objective", "routine_tag"} {
		if values.Get(name) != "" {
			q.Set(name, values.Get(name))
		}
	}
	if number > 0 {
		q.Set("routine_page", strconv.Itoa(number-1))
		page.PreviousURL = structuredReusePath + "?" + q.Encode()
	}
	if more {
		q.Set("routine_page", strconv.Itoa(number+1))
		page.NextURL = structuredReusePath + "?" + q.Encode()
	}
	return page, nil
}
