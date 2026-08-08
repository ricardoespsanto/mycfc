package handlers

import "github.com/cfcoimbra/mycfc/ui/components"

type DashboardVM struct {
	Meta      components.PageMeta
	Heading   string
	Intro     string
	EmptyText string
	Agenda    []DashboardAgendaItemVM
	Sections  []DashboardSectionVM
}

type DashboardSectionVM struct {
	Heading, Empty string
	Items          []DashboardItemVM
}
type DashboardItemVM struct{ Title, Detail, URL string }
type DashboardAgendaItemVM struct{ Title, Detail, URL, Kind string }
