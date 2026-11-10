package handlers

import (
	"time"

	"github.com/cfcoimbra/mycfc/ui/pages"
)

type calendarEntry struct {
	Title    string
	URL      string
	Kind     string
	StartsAt time.Time
	Date     string
}

func basicCalendarMonth(entries []calendarEntry, now time.Time, location *time.Location) pages.BasicCalendarMonth {
	if location == nil {
		location = time.UTC
	}
	anchor := now.In(location)
	var future *time.Time
	for _, entry := range entries {
		local, valid := calendarEntryDate(entry, location)
		if !valid {
			continue
		}
		if (entry.Date != "" && local.Format("2006-01-02") == anchor.Format("2006-01-02") || !local.Before(anchor)) && (future == nil || local.Before(*future)) {
			copy := local
			future = &copy
		}
	}
	if future != nil {
		anchor = *future
	}
	monthStart := time.Date(anchor.Year(), anchor.Month(), 1, 0, 0, 0, 0, location)
	gridStart := monthStart.AddDate(0, 0, -((int(monthStart.Weekday()) + 6) % 7))
	today := time.Date(now.In(location).Year(), now.In(location).Month(), now.In(location).Day(), 0, 0, 0, 0, location)
	byDate := map[string][]pages.BasicCalendarEntry{}
	for _, entry := range entries {
		local, valid := calendarEntryDate(entry, location)
		if !valid {
			continue
		}
		key := local.Format("2006-01-02")
		byDate[key] = append(byDate[key], pages.BasicCalendarEntry{Title: entry.Title, URL: entry.URL, Kind: entry.Kind})
	}
	days := make([]pages.BasicCalendarDay, 42)
	for i := range days {
		day := gridStart.AddDate(0, 0, i)
		dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, location)
		days[i] = pages.BasicCalendarDay{
			Number:  day.Format("2"),
			Muted:   day.Month() != anchor.Month(),
			Today:   dayStart.Equal(today),
			Entries: byDate[day.Format("2006-01-02")],
		}
	}
	return pages.BasicCalendarMonth{Label: calendarMonthLabel(anchor), Days: days}
}

func calendarMonthLabel(t time.Time) string {
	months := [...]string{"janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"}
	return months[int(t.Month())-1] + " " + t.Format("2006")
}

// Calendar cells use recorded civil dates; a day-only session has no instant.
func calendarEntryDate(entry calendarEntry, location *time.Location) (time.Time, bool) {
	if entry.Date != "" {
		d, e := time.ParseInLocation("2006-01-02", entry.Date, location)
		return d, e == nil && d.Format("2006-01-02") == entry.Date
	}
	if entry.StartsAt.IsZero() {
		return time.Time{}, false
	}
	return entry.StartsAt.In(location), true
}
