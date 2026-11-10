package handlers

import (
	"context"

	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
)

func (h StructuredReuse) workspaceLinks(ctx context.Context, user CurrentUser, page *pages.StructuredWeekWorkspace) {
	if !page.Enabled || !page.HasWeek || h.Store == nil {
		return
	}
	planID, err := uuid.Parse(page.WeekID)
	if err != nil {
		return
	}
	plan, err := h.Store.ReusePlan(ctx, user.ID, planID)
	if err != nil || plan.TrainingGroupID == nil || plan.SeasonID == nil || plan.TrainingGroupID.String() != page.GroupID {
		return
	}
	target := reuseTargetSelector{GroupID: *plan.TrainingGroupID, SeasonID: *plan.SeasonID, WeekStart: plan.WeekStart.Time.Format("2006-01-02")}
	page.CopyURL = reuseTaskURL(reuseSourceSelector{Kind: "WEEK", PlanID: planID}, nil)
	page.RoutineURL = reuseTaskURL(reuseSourceSelector{Kind: "ROUTINE"}, &target)
	page.AdaptURL = reuseAdaptURL(page.GroupID, page.WeekID, "", page.SelectedDay)
	for i := range page.Days {
		day := &page.Days[i]
		day.CopyURL = reuseTaskURL(reuseSourceSelector{Kind: "DAY", PlanID: planID, Day: day.Date}, nil)
		day.RoutineURL = page.RoutineURL
		for _, appointments := range [][]pages.StructuredWorkspaceAppointment{day.Morning, day.Afternoon, day.Unscheduled} {
			for j := range appointments {
				a := &appointments[j]
				id, err := uuid.Parse(a.ID)
				if err != nil {
					continue
				}
				a.CopyURL = reuseTaskURL(reuseSourceSelector{Kind: "SESSION", PlanID: planID, SessionID: id}, nil)
				selected := target
				selected.SessionID = id
				a.RoutineURL = reuseTaskURL(reuseSourceSelector{Kind: "ROUTINE"}, &selected)
				a.AdaptURL = reuseAdaptURL(page.GroupID, page.WeekID, a.ID, day.Date)
			}
		}
	}
}
