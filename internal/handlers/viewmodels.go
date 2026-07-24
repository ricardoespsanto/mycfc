package handlers

import "github.com/cfcoimbra/mycfc/ui/components"

type CalendarVM struct {
	Label string
	URL   string
}

type DashboardVM struct {
	Meta      components.PageMeta
	Heading   string
	Intro     string
	EmptyText string
	Calendars []CalendarVM
	Sections  []DashboardSectionVM
}

type DashboardSectionVM struct {
	Heading, Empty string
	Items          []DashboardItemVM
}
type DashboardItemVM struct{ Title, Detail, URL string }
