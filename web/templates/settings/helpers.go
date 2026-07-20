// Package settings renders Victus's Configuration tab: per-nutrient goal
// ranges (min/max) and the instance-wide "how to set healthy targets" info
// link — both retrofit the Day and Weekly Builder summaries with
// range-aware coloring once configured.
package settings

import "strconv"

// NutrientIDStr renders a nutrient id the same way the "min_"/"max_" form
// field names expect it — the single source of truth handlers_settings.go
// reuses when reading those fields back, so the two sides can't silently
// drift apart.
func NutrientIDStr(id int16) string {
	return strconv.Itoa(int(id))
}

// toStr renders a count for display on the import result page.
func toStr(n int) string {
	return strconv.Itoa(n)
}
