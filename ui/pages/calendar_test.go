package pages

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestBasicCalendarRendersMonthGridAndEntries(t *testing.T) {
	month := BasicCalendarMonth{
		Label: "agosto 2026",
		Days: []BasicCalendarDay{
			{Number: "1", Today: true, Entries: []BasicCalendarEntry{{Title: "Prova regional", URL: "/events/123", Kind: "Competição"}}},
		},
	}
	var output bytes.Buffer
	if err := BasicCalendar(month, "Calendário de eventos", "Vista mensal").Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	body := output.String()
	for _, want := range []string{`class="module calendar-month" role="region" aria-label="Calendário de eventos - agosto 2026" tabindex="0"`, "calendar-month__grid", `class="calendar-month__row" role="row"`, `role="columnheader"`, `role="gridcell"`, "agosto 2026", "Prova regional", `href="/events/123"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("calendar render does not contain %q: %q", want, body)
		}
	}
}
