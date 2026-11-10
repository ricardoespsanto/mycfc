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

func TestCalendarDayOnlyEntryUsesCanonicalDateWithoutAnInstant(t *testing.T) {
	now := time.Date(2026, 11, 10, 23, 0, 0, 0, time.UTC)
	month := basicCalendarMonth([]calendarEntry{{Title: "Descanso", Date: "2026-12-06", Kind: "Hora por definir"}}, now, lisbonLocation())
	if month.Label != "dezembro 2026" {
		t.Fatalf("month %s", month.Label)
	}
	found := false
	for _, d := range month.Days {
		for _, entry := range d.Entries {
			if entry.Title == "Descanso" {
				found = true
				if d.Number != "6" || d.Muted {
					t.Fatalf("wrong day %#v", d)
				}
			}
		}
	}
	if !found {
		t.Fatal("untimed session omitted")
	}
	if _, ok := calendarEntryDate(calendarEntry{}, lisbonLocation()); ok {
		t.Fatal("zero instant became date")
	}
}
