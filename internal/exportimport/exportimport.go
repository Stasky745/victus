// Package exportimport lets a user download their meal-library/goals/day-plan
// data as a single JSON file, and load it back into any Victus instance
// (same one, or a different install entirely — including across the
// Postgres/SQLite backend split, which is the whole reason this doesn't just
// dump raw database rows: a meal's id means nothing on another instance, so
// every cross-reference here travels as a natural key — a name, a nutrient's
// fixed seed key, a (source, source_ref) pair — resolved back to a real id
// on import instead.
package exportimport

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/goals"
	"github.com/Stasky745/victus/internal/mealslib"
	"github.com/Stasky745/victus/internal/planning"
)

// CurrentVersion is bumped whenever Export's shape changes incompatibly.
// Import rejects any file whose Version isn't exactly this, rather than
// guessing at a best-effort parse of a shape it wasn't written for.
const CurrentVersion = 1

// exportListLimit bounds ListMeals — comfortably above what a self-hosted,
// single-instance meal library will ever hold.
const exportListLimit = 1_000_000

// dayPlanRangeStart/End bound ListDayPlansInRange for a "give me everything"
// export — wide enough to cover any real day plan without needing a
// dedicated "list all" query.
var (
	dayPlanRangeStart = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	dayPlanRangeEnd   = time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)
)

// Selection is which of the independently-exportable sections to include.
type Selection struct {
	MealCategories bool
	MealLabels     bool
	Meals          bool
	Goals          bool
	DefaultDay     bool
	DayPlans       bool
	AppSettings    bool
}

// Export is the full downloadable/uploadable file shape.
type Export struct {
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exported_at"`
	Sections   Sections  `json:"sections"`
}

// Sections holds whichever of the 7 categories were selected — an omitted
// (nil) field means that category wasn't part of this export, distinct from
// a selected-but-empty one ([]T{}).
type Sections struct {
	MealCategories []MealCategory `json:"meal_categories,omitempty"`
	MealLabels     []MealLabel    `json:"meal_labels,omitempty"`
	Meals          []Meal         `json:"meals,omitempty"`
	Goals          []Goal         `json:"goals,omitempty"`
	DefaultDay     []DayItem      `json:"default_day,omitempty"`
	DayPlans       []DayPlan      `json:"day_plans,omitempty"`
	AppSettings    []AppSetting   `json:"app_settings,omitempty"`
}

// MealCategory is a meal_categories row, keyed by its (unique) name.
type MealCategory struct {
	Name      string `json:"name"`
	SortOrder int16  `json:"sort_order"`
}

// MealLabel is a meal_labels row, keyed by its (unique) name.
type MealLabel struct {
	Name      string `json:"name"`
	Color     string `json:"color"`
	SortOrder int16  `json:"sort_order"`
}

// MealRef is enough to find a specific meal on any instance: sourced meals
// (Mealie/OFF/Tandoor imports) match by (Source, SourceRef); manually
// entered meals have no SourceRef, so they match by Name instead.
type MealRef struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	SourceRef string `json:"source_ref,omitempty"`
}

// Meal is a full meal-library entry. NutrientAmounts is keyed by the
// nutrient's fixed seed key (e.g. "calories"), not its id, since ids aren't
// portable — the key is (every install has the same seeded nutrient
// registry). Labels is a list of label names.
type Meal struct {
	MealRef
	RecipeURL       string             `json:"recipe_url,omitempty"`
	ServingLabel    string             `json:"serving_label"`
	ServingAmount   float64            `json:"serving_amount"`
	IsFavorite      bool               `json:"is_favorite"`
	NutrientAmounts map[string]float64 `json:"nutrient_amounts,omitempty"`
	Labels          []string           `json:"labels,omitempty"`
}

// Goal is one nutrient's configured range for the exporting user, keyed by
// nutrient key rather than id.
type Goal struct {
	NutrientKey string   `json:"nutrient_key"`
	MinValue    *float64 `json:"min_value,omitempty"`
	MaxValue    *float64 `json:"max_value,omitempty"`
	IdealMin    *float64 `json:"ideal_min,omitempty"`
	IdealMax    *float64 `json:"ideal_max,omitempty"`
}

// DayItem is one meal placed into a category slot — used both for Default
// Day items and (nested inside DayPlan) real day-plan items.
type DayItem struct {
	CategoryName string  `json:"category_name"`
	Meal         MealRef `json:"meal"`
	Quantity     float64 `json:"quantity"`
}

// DayPlan is one date's worth of day-plan items for the exporting user.
type DayPlan struct {
	Date  string    `json:"date"` // YYYY-MM-DD
	Items []DayItem `json:"items"`
}

// AppSetting is one app_settings row (e.g. the healthy-targets info URL).
// Instance-wide, not tied to the exporting user.
type AppSetting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Store is the export/import repository. It follows the same shape as its
// peers (internal/mealslib, internal/planning, internal/goals) — a thin
// layer over sqlc.Querier — and composes those peers directly wherever an
// existing method already does exactly what's needed (mealslib.Create/
// Update/Import for meals, goals.SaveGoals for goals, planning.AddItem/
// AddDefaultItem for day plans), falling back to sqlc.Querier directly only
// for the natural-key lookups those peers don't expose.
type Store struct {
	q        sqlc.Querier
	meals    *mealslib.Store
	goalsSt  *goals.Store
	planning *planning.Store
}

// New returns a Store backed by sqlDB for driver ("postgres" or "sqlite").
func New(sqlDB *sql.DB, driver string) (*Store, error) {
	q, err := db.NewQuerier(driver, sqlDB)
	if err != nil {
		return nil, err
	}
	mealsSt, err := mealslib.New(sqlDB, driver)
	if err != nil {
		return nil, err
	}
	goalsSt, err := goals.New(sqlDB, driver)
	if err != nil {
		return nil, err
	}
	planningSt, err := planning.New(sqlDB, driver)
	if err != nil {
		return nil, err
	}
	return &Store{q: q, meals: mealsSt, goalsSt: goalsSt, planning: planningSt}, nil
}

// nutrientMaps loads the fixed seed nutrient registry once, both directions
// (id<->key), used throughout Export/Import to translate nutrient ids
// (not portable across instances) to/from their stable keys.
func (s *Store) nutrientMaps(ctx context.Context) (keyByID map[int16]string, idByKey map[string]int16, err error) {
	nutrients, err := s.q.ListNutrients(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list nutrients: %w", err)
	}
	keyByID = make(map[int16]string, len(nutrients))
	idByKey = make(map[string]int16, len(nutrients))
	for _, n := range nutrients {
		keyByID[n.ID] = n.Key
		idByKey[n.Key] = n.ID
	}
	return keyByID, idByKey, nil
}

// mealCache resolves a meal id to its full row, memoizing lookups — day
// plans/Default Day items only carry a meal id, and building each
// referenced meal's natural key (MealRef) needs a full row fetch, but the
// same meal is often referenced by many items.
type mealCache struct {
	q    sqlc.Querier
	byID map[uuid.UUID]sqlc.Meal
}

func newMealCache(q sqlc.Querier) *mealCache {
	return &mealCache{q: q, byID: make(map[uuid.UUID]sqlc.Meal)}
}

func (c *mealCache) get(ctx context.Context, id uuid.UUID) (sqlc.Meal, error) {
	if m, ok := c.byID[id]; ok {
		return m, nil
	}
	m, err := c.q.GetMeal(ctx, id)
	if err != nil {
		return sqlc.Meal{}, err
	}
	c.byID[id] = m
	return m, nil
}

func mealRefOf(m sqlc.Meal) MealRef {
	return MealRef{Name: m.Name, Source: m.Source, SourceRef: m.SourceRef.String}
}
