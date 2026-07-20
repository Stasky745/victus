// Package week renders the Weekly Builder: a Monday-anchored 7-day
// overview with per-day mini-summaries, a week-average nutrient summary,
// and a "copy this day to..." control per column.
package week

import (
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/planning"
)

func idString(id uuid.UUID) string {
	return id.String()
}

func formatWeekLabel(start time.Time) string {
	end := start.AddDate(0, 0, planning.WeekLength-1)
	if start.Month() == end.Month() {
		return start.Format("Jan 2") + "–" + end.Format("2, 2006")
	}
	return start.Format("Jan 2") + " – " + end.Format("Jan 2, 2006")
}

func formatWeekday(t time.Time) string {
	return t.Format("Mon")
}

func formatShortDate(t time.Time) string {
	return t.Format("Jan 2")
}

// formatTotal rounds to whole numbers, unlike day.formatTotal's 1-decimal
// precision — an intentional difference: this compact 7-column overview has
// no room for decimals, while the Day Builder's detailed per-nutrient rows do.
func formatTotal(f float64) string {
	return strconv.FormatFloat(f, 'f', 0, 64)
}

// formatQuantity preserves precision (1.5 stays "1.5") unlike formatTotal's
// rounded display — a serving count shouldn't silently round to a whole number.
func formatQuantity(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// keyTotal finds a nutrient's total by its registry key (e.g. "calories") —
// used both for the day-row's headline kcal/protein figures and to look up
// which NutrientID's Status decides the row's status-colored left border.
func keyTotal(totals []planning.NutrientTotal, key string) planning.NutrientTotal {
	for _, nt := range totals {
		if nt.Key == key {
			return nt
		}
	}
	return planning.NutrientTotal{}
}
