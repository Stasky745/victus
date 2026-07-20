// Package day renders the Day Builder: a date's meal-category slots, the
// live nutrient summary, and the meal-search-and-add flow, all driven by
// htmx out-of-band swaps rather than client-side state.
package day

import (
	"strconv"
	"time"

	"github.com/google/uuid"
)

// DateLayout is the YYYY-MM-DD format used for /days/{date} URLs — exported
// so internal/httpserver's routing/redirect logic formats and parses dates
// the same way these templates display them, instead of a second copy of
// the layout string drifting out of sync.
const DateLayout = "2006-01-02"

// FormatDate renders t the same way a /days/{date} URL expects it.
func FormatDate(t time.Time) string {
	return t.Format(DateLayout)
}

// MondayOf returns the Monday of the week containing t — the single shared
// implementation for canonicalizing /weeks/{week_start} URLs, used by both
// the httpserver handlers and their tests so they can't drift apart.
func MondayOf(t time.Time) time.Time {
	offset := (int(t.Weekday()) + 6) % 7 // Monday=0 .. Sunday=6
	return t.AddDate(0, 0, -offset)
}

func idString(id uuid.UUID) string {
	return id.String()
}

func formatQuantity(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func formatTotal(f float64) string {
	return strconv.FormatFloat(f, 'f', 1, 64)
}
