package layout

import (
	"strconv"
	"strings"

	"github.com/Stasky745/victus/internal/goals"
)

// GoalStatusClass maps a goal Status to the Tailwind text-color class used
// to render a nutrient total against its configured range — centralized so
// the Day and Weekly Builders can't drift on what each status looks like.
// Under/Over (outside the hard min/max) always render red regardless of
// direction — the point of the range is to land inside it, and under vs.
// over is already obvious from the number sitting next to its bounds.
// Acceptable (inside min/max, outside the optional ideal sub-range) gets
// its own amber so "fine but not ideal" reads differently from "ideal."
func GoalStatusClass(status goals.Status) string {
	switch status {
	case goals.StatusUnder, goals.StatusOver:
		return "text-red-600 dark:text-red-500"
	case goals.StatusAcceptable:
		return "text-amber-600 dark:text-amber-500"
	case goals.StatusIdeal:
		return "text-green-600 dark:text-green-500"
	default:
		return "text-slate-900 dark:text-slate-100"
	}
}

// GoalStatusClassFor combines goals.Lookup + Goal.Status + GoalStatusClass —
// the common case of rendering one nutrient total against its configured range.
func GoalStatusClassFor(goalsList []goals.Goal, nutrientID int16, total float64) string {
	g := goals.Lookup(goalsList, nutrientID)
	return GoalStatusClass(g.Status(total))
}

// GoalStatusBorderClass is GoalStatusClass's border-color counterpart —
// used for the Weekly Builder's per-day status indicator (a colored left
// border on each day's card, mirroring the original prototype).
func GoalStatusBorderClass(status goals.Status) string {
	switch status {
	case goals.StatusUnder, goals.StatusOver:
		return "border-red-500"
	case goals.StatusAcceptable:
		return "border-amber-500"
	case goals.StatusIdeal:
		return "border-green-500"
	default:
		return "border-slate-200 dark:border-vxborder"
	}
}

// GoalStatusBorderClassFor combines goals.Lookup + Goal.Status +
// GoalStatusBorderClass.
func GoalStatusBorderClassFor(goalsList []goals.Goal, nutrientID int16, total float64) string {
	g := goals.Lookup(goalsList, nutrientID)
	return GoalStatusBorderClass(g.Status(total))
}

// HasGoalRange reports whether nutrientID has any bound at all configured
// in goalsList (hard min/max or ideal min/max) — callers use this to decide
// whether to render GoalRangeLabel at all.
func HasGoalRange(goalsList []goals.Goal, nutrientID int16) bool {
	g := goals.Lookup(goalsList, nutrientID)
	return g.MinValue != nil || g.MaxValue != nil || g.IdealMin != nil || g.IdealMax != nil
}

// GoalRangeLabel renders a configured goal's bounds as e.g.
// "goal: 1200–1600 · ideal 1400–1500", "goal: ≤ 50 · ideal ≤ 30" — blank if
// HasGoalRange would report false, so callers should check that first.
func GoalRangeLabel(goalsList []goals.Goal, nutrientID int16) string {
	g := goals.Lookup(goalsList, nutrientID)
	var parts []string
	switch {
	case g.MinValue != nil && g.MaxValue != nil:
		parts = append(parts, "goal: "+formatBound(*g.MinValue)+"–"+formatBound(*g.MaxValue))
	case g.MinValue != nil:
		parts = append(parts, "goal: ≥ "+formatBound(*g.MinValue))
	case g.MaxValue != nil:
		parts = append(parts, "goal: ≤ "+formatBound(*g.MaxValue))
	}
	switch {
	case g.IdealMin != nil && g.IdealMax != nil:
		parts = append(parts, "ideal "+formatBound(*g.IdealMin)+"–"+formatBound(*g.IdealMax))
	case g.IdealMin != nil:
		parts = append(parts, "ideal ≥ "+formatBound(*g.IdealMin))
	case g.IdealMax != nil:
		parts = append(parts, "ideal ≤ "+formatBound(*g.IdealMax))
	}
	return strings.Join(parts, " · ")
}

func formatBound(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
