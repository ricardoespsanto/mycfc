package handlers

import (
	"net/url"
	"sort"
	"time"

	"github.com/cfcoimbra/mycfc/ui/pages"
)

func validWorkspaceDate(raw string) bool {
	d, e := time.Parse("2006-01-02", raw)
	return e == nil && d.Year() >= 1 && d.Year() <= 9999 && d.Format("2006-01-02") == raw
}
func structuredPlannerReturnDay(raw string) string {
	normalized := structuredPlannerReturn(raw)
	if normalized == "" {
		return ""
	}
	u, _ := url.Parse(normalized)
	return u.Query().Get("day")
}
func validStructuredWorkspaceSelection(audiences []pages.StructuredTrainingAudience, group, week string) bool {
	if group == "" {
		return week == ""
	}
	for _, a := range audiences {
		if a.GroupID != group {
			continue
		}
		if week == "" {
			return true
		}
		for _, w := range a.Weeks {
			if w.ID == week {
				return true
			}
		}
		return false
	}
	return false
}
func structuredWorkspaceAnchor(base, fragment string) string {
	u, _ := url.Parse(base)
	u.Fragment = fragment
	return u.String()
}
func structuredWorkspaceTask(base, task, fragment string) string {
	u, _ := url.Parse(base)
	q := u.Query()
	q.Set("task", task)
	u.RawQuery = q.Encode()
	u.Fragment = fragment
	return u.String()
}

var workspaceDays = []string{"domingo", "segunda-feira", "terça-feira", "quarta-feira", "quinta-feira", "sexta-feira", "sábado"}
var workspaceMonths = []string{"", "janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"}

func workspaceDateLabel(d time.Time) string {
	return workspaceDays[d.Weekday()] + ", " + d.Format("2") + " de " + workspaceMonths[d.Month()] + " de " + d.Format("2006")
}
func workspaceDateRange(start, end time.Time) string {
	if start.Year() == end.Year() && start.Month() == end.Month() {
		return start.Format("2") + "–" + end.Format("2") + " de " + workspaceMonths[start.Month()] + " de " + start.Format("2006")
	}
	return start.Format("02/01/2006") + "–" + end.Format("02/01/2006")
}

// The complete week is chosen before the detail-engine session filter. All
// calendar values come from raw dates/timestamps rather than localized labels.
func buildStructuredWeekWorkspace(audiences []pages.StructuredTrainingAudience, publications []pages.StructuredPublicationState, unavailable bool, currentGroup *TrainingGroupRecord, user CurrentUser, groupID, weekID, sessionID, requestedDay string, now time.Time) pages.StructuredWeekWorkspace {
	out := pages.StructuredWeekWorkspace{GroupsURL: trainingGroupsPath, CoachName: user.Name}
	var audience *pages.StructuredTrainingAudience
	for i := range audiences {
		if audiences[i].GroupID == groupID {
			audience = &audiences[i]
			break
		}
	}
	if audience == nil {
		return out
	}
	out.Enabled = true
	out.GroupID = groupID
	out.GroupName = audience.GroupName
	out.Scope = audience.Scope
	out.MemberCount = audience.MemberCount
	if currentGroup != nil && currentGroup.ID.String() == groupID {
		out.GroupName = currentGroup.Name
		out.Scope = currentGroup.ScopeName
		out.MemberCount = int(currentGroup.MemberCount)
	}
	out.GroupURL = trainingGroupsPath + "/" + groupID
	emptyURL := structuredPlannerURL(groupID, "", "")
	out.CreateWeekURL = structuredWorkspaceTask(emptyURL, "week", "criar-semana-treino")
	out.RefreshURL = emptyURL
	out.AdvancedURL = structuredWorkspaceAnchor(emptyURL, "training-cycles")
	var week *pages.StructuredTrainingWeek
	for i := range audience.Weeks {
		w := &audience.Weeks[i]
		out.Weeks = append(out.Weeks, pages.StructuredWorkspaceWeekChoice{ID: w.ID, Title: w.Title, StartsOn: w.StartsOn, EndsOn: w.EndsOn, URL: structuredPlannerURL(groupID, w.ID, ""), Selected: w.ID == weekID})
		if w.ID == weekID {
			week = w
		}
	}
	sort.SliceStable(out.Weeks, func(i, j int) bool { return out.Weeks[i].StartsOn < out.Weeks[j].StartsOn })
	if week == nil {
		return out
	}
	start, e := time.Parse("2006-01-02", week.StartsOn)
	if e != nil || start.Weekday() != time.Monday {
		return out
	}
	end := start.AddDate(0, 0, 6)
	out.HasWeek = true
	out.WeekID = week.ID
	out.WeekTitle = week.Title
	out.Season = week.Season
	out.StartsOn = week.StartsOn
	out.EndsOn = end.Format("2006-01-02")
	out.DateRange = workspaceDateRange(start, end)
	out.Summary = week.Summary
	day := requestedDay
	if !validWorkspaceDate(day) || day < out.StartsOn || day > out.EndsOn {
		day = now.Format("2006-01-02")
		if day < out.StartsOn || day > out.EndsOn {
			day = out.StartsOn
		}
	}
	for _, session := range week.Sessions {
		if session.ID == sessionID && validWorkspaceDate(session.StartsOn) && session.StartsOn >= out.StartsOn && session.StartsOn <= out.EndsOn {
			day = session.StartsOn
			break
		}
	}
	out.SelectedDay = day
	base := structuredPlannerURL(groupID, weekID, sessionID, day)
	out.RefreshURL = base
	out.GroupURL = groupContextDayURL(out.GroupURL, weekID, day)
	out.AddSessionURL = structuredWorkspaceTask(base, "session", "criar-sessao-estruturada")
	out.WaterCreateURL = focusedWaterCreateURL(weekID, day)
	out.CreateWeekURL = structuredWorkspaceTask(base, "week", "criar-semana-treino")
	out.PublicationURL = structuredWorkspaceAnchor(base, "training-publication")
	out.AdvancedURL = structuredWorkspaceAnchor(base, "training-cycles")
	for i, c := range out.Weeks {
		if c.ID == weekID {
			if i > 0 {
				out.PreviousURL = out.Weeks[i-1].URL
			}
			if i+1 < len(out.Weeks) {
				out.NextURL = out.Weeks[i+1].URL
			}
			break
		}
	}
	for i := 0; i < 7; i++ {
		d := start.AddDate(0, 0, i)
		date := d.Format("2006-01-02")
		dayURL := structuredPlannerURL(groupID, weekID, "", date)
		record := pages.StructuredWorkspaceDay{Date: date, Label: workspaceDateLabel(d), ShortLabel: d.Format("02/01"), URL: dayURL, AddSessionURL: structuredWorkspaceTask(dayURL, "session", "criar-sessao-estruturada"), Selected: date == day, WaterCreateURL: focusedWaterCreateURL(weekID, date)}
		for _, session := range week.Sessions {
			if session.StartsOn != date {
				continue
			}
			appointment := pages.StructuredWorkspaceAppointment{Label: session.Schedule.Label, Precision: session.Schedule.Precision, Band: session.Schedule.Band, TimeUnknown: session.Schedule.Known && session.Schedule.StartsAt == "", EndUnknown: session.Schedule.Known && session.Schedule.EndsAt == "", ID: session.ID, Title: session.Title, StartsOn: session.StartsOn, EndsOn: session.EndsOn, StartsAt: session.StartsAt, EndsAt: session.EndsAt, EntryKind: session.EntryKind, Status: session.Status, URL: structuredPlannerURL(groupID, weekID, session.ID, date), Modalities: session.Modalities, Selected: session.ID == sessionID}
			switch {
			case session.Schedule.Known && session.Schedule.Precision == "DAY":
				record.Unscheduled = append(record.Unscheduled, appointment)
			case session.Schedule.Known && session.Schedule.Precision == "BAND" && session.Schedule.Band == "MORNING":
				record.Morning = append(record.Morning, appointment)
			case session.Schedule.Known && session.Schedule.Precision == "BAND" && session.Schedule.Band == "AFTERNOON":
				record.Afternoon = append(record.Afternoon, appointment)
			case session.StartsAt != "" && session.StartsAt < "12:00":
				record.Morning = append(record.Morning, appointment)
			case session.StartsAt != "":
				record.Afternoon = append(record.Afternoon, appointment)
			default:
				record.Unscheduled = append(record.Unscheduled, appointment)
			}
		}
		order := func(a []pages.StructuredWorkspaceAppointment) {
			sort.SliceStable(a, func(i, j int) bool {
				if a[i].StartsAt == a[j].StartsAt {
					return a[i].ID < a[j].ID
				}
				return a[i].StartsAt < a[j].StartsAt
			})
		}
		order(record.Morning)
		order(record.Afternoon)
		out.Days = append(out.Days, record)
	}
	out.Publication = pages.StructuredWorkspacePublication{State: "unavailable", Label: "Não foi possível confirmar o estado de publicação", Unavailable: true}
	if !unavailable {
		for _, p := range publications {
			if p.PlanID != weekID {
				continue
			}
			if p.Unavailable {
				break
			}
			// PrescriptionCount is the engine's prospective draft recipient/session
			// count, not a stored/delivered prescription count.
			out.Publication = pages.StructuredWorkspacePublication{State: "never", Label: "Rascunho · nunca publicado", Revision: p.Revision, PublishedAt: p.PublishedAt, PublishedBy: p.PublishedBy, SourceUpdatedAt: p.SourceUpdatedAt, AthleteCount: p.AthleteCount, PrescriptionCount: p.PrescriptionCount, ConflictCount: p.ConflictCount}
			if p.Revision > 0 {
				out.Publication.State = "changes"
				out.Publication.Label = "Alterações por publicar"
				if p.Current {
					out.Publication.State = "current"
					out.Publication.Label = "Publicado"
				}
			}
			break
		}
	}
	return out
}

// The manager overview has already scoped each row. Links are advisory only;
// task GETs and transactions independently recheck scope and the actual DB clock.
func applyFocusedWorkoutEditLinks(workspace *pages.StructuredWeekWorkspace, eligible map[string]bool) {
	for i := range workspace.Days {
		day := &workspace.Days[i]
		for _, items := range [][]pages.StructuredWorkspaceAppointment{day.Morning, day.Afternoon, day.Unscheduled} {
			for j := range items {
				if eligible[items[j].ID] {
					items[j].EditURL = focusedWorkoutEditURL(workspace.WeekID, items[j].ID)
					items[j].CancelURL = focusedWorkoutCancelURL(workspace.WeekID, items[j].ID)
				}
			}
		}
	}
}
