package handlers

import (
	"testing"
	"time"
)

func TestBasicCalendarMonthUsesNearestFutureEntryAndLocalDates(t *testing.T) {
	location := time.FixedZone("WEST", 3600)
	now := time.Date(2026, time.August, 30, 23, 30, 0, 0, time.UTC)
	calendar := basicCalendarMonth([]calendarEntry{
		{Title: "Passado", URL: "/past", Kind: "Treino", StartsAt: now.Add(-time.Hour)},
		{Title: "Futuro", URL: "/future", Kind: "Evento", StartsAt: now.Add(48 * time.Hour)},
	}, now, location)

	if calendar.Label != "setembro 2026" || len(calendar.Days) != 42 {
		t.Fatalf("calendar = %#v", calendar)
	}
	var found bool
	for _, day := range calendar.Days {
		for _, entry := range day.Entries {
			if entry.Title == "Futuro" && entry.URL == "/future" && entry.Kind == "Evento" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("future entry was not rendered in its local calendar day")
	}
}
