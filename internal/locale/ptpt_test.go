package locale

import (
	"testing"
	"time"
)

func TestDateLong(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Lisbon")
	value := time.Date(2026, time.January, 2, 12, 0, 0, 0, time.UTC)
	if got := DateLong(value, location); got != "2 de janeiro de 2026" {
		t.Fatalf("DateLong() = %q", got)
	}
}
