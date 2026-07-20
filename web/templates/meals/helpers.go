// Package meals renders the meal-library UI: the searchable list, the
// create/edit form, and meal-category management.
package meals

import (
	"strconv"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/mealslib"
)

func idString(id uuid.UUID) string {
	return id.String()
}

// labelBadgeClass maps a mealslib.LabelColors value to its matching
// vx-badge-{color} CSS class (web/static/input.css) — falls back to gray
// for any value that somehow isn't in the fixed palette, rather than
// emitting an unstyled/broken class name.
func labelBadgeClass(color string) string {
	switch color {
	case "red", "green", "blue", "purple", "amber":
		return "vx-badge-" + color
	default:
		return "vx-badge-gray"
	}
}

// hasLabel reports whether id is present in labels — used to pre-check the
// meal form's label checkboxes.
func hasLabel(labels []mealslib.Label, id uuid.UUID) bool {
	for _, l := range labels {
		if l.ID == id {
			return true
		}
	}
	return false
}

// labelIDValue renders the active label filter for the hidden form field
// the search box includes — blank (not "00000000-...") when there's no
// active filter, so the query param is simply absent rather than an
// explicit-but-meaningless nil uuid.
func labelIDValue(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// formatAmount renders a nutrient/serving amount for an <input value="...">,
// blank when unset so the field starts empty rather than showing "0".
func formatAmount(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}
