package exportimport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/goals"
	"github.com/Stasky745/victus/internal/mealslib"
)

// ErrInvalidData wraps every error Import returns because of the uploaded
// file itself (wrong version, malformed date) rather than a database/server
// fault — callers can safely show these to the user (errors.Is(err,
// ErrInvalidData)), unlike an arbitrary internal error.
var ErrInvalidData = errors.New("invalid export data")

// ImportResult summarizes what an Import call actually did, for display to
// the user — counts plus a list of individual items that couldn't be
// resolved (e.g. a day-plan item referencing a meal that isn't in this
// import and doesn't already exist), which are skipped rather than failing
// the whole import.
type ImportResult struct {
	CategoriesCreated, CategoriesUnchanged       int
	LabelsCreated, LabelsUpdated                 int
	MealsCreated, MealsUpdated                   int
	GoalsSet                                     int
	DefaultDayItemsAdded, DefaultDayItemsSkipped int
	DayPlanItemsAdded, DayPlanItemsSkipped       int
	AppSettingsSet                               int
	Warnings                                     []string
}

// Import applies data to userID's instance, section by section, each
// "update in place": an existing category/label/meal (matched by natural
// key) is updated, not duplicated; a missing one is created. Sections not
// present in data (nil) are simply skipped — Import applies whatever was
// actually exported, it doesn't require every section to be present.
func (s *Store) Import(ctx context.Context, userID uuid.UUID, data Export) (ImportResult, error) {
	if data.Version != CurrentVersion {
		return ImportResult{}, fmt.Errorf("%w: unsupported export version %d (this Victus supports version %d)", ErrInvalidData, data.Version, CurrentVersion)
	}

	var res ImportResult
	_, nutrientIDByKey, err := s.nutrientMaps(ctx)
	if err != nil {
		return ImportResult{}, err
	}

	for _, c := range data.Sections.MealCategories {
		_, err := s.q.GetMealCategoryByName(ctx, c.Name)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := s.meals.CreateCategory(ctx, c.Name); err != nil {
				return ImportResult{}, fmt.Errorf("create category %q: %w", c.Name, err)
			}
			res.CategoriesCreated++
		case err != nil:
			return ImportResult{}, fmt.Errorf("look up category %q: %w", c.Name, err)
		default:
			// Already exists — deliberately not touching sort_order, so
			// importing never reshuffles a display order the user has
			// already arranged.
			res.CategoriesUnchanged++
		}
	}

	for _, l := range data.Sections.MealLabels {
		color := l.Color
		if !mealslib.IsValidLabelColor(color) {
			color = "gray"
		}
		existing, err := s.q.GetMealLabelByName(ctx, l.Name)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := s.meals.CreateLabel(ctx, l.Name, color); err != nil {
				return ImportResult{}, fmt.Errorf("create label %q: %w", l.Name, err)
			}
			res.LabelsCreated++
		case err != nil:
			return ImportResult{}, fmt.Errorf("look up label %q: %w", l.Name, err)
		default:
			if _, err := s.q.UpdateMealLabel(ctx, sqlc.UpdateMealLabelParams{ID: existing.ID, Name: l.Name, Color: color}); err != nil {
				return ImportResult{}, fmt.Errorf("update label %q: %w", l.Name, err)
			}
			res.LabelsUpdated++
		}
	}

	for _, m := range data.Sections.Meals {
		created, err := s.importMeal(ctx, userID, m, nutrientIDByKey)
		if err != nil {
			return ImportResult{}, fmt.Errorf("import meal %q: %w", m.Name, err)
		}
		if created {
			res.MealsCreated++
		} else {
			res.MealsUpdated++
		}
	}

	if len(data.Sections.Goals) > 0 {
		inputs := make([]goals.GoalInput, 0, len(data.Sections.Goals))
		for _, g := range data.Sections.Goals {
			nid, ok := nutrientIDByKey[g.NutrientKey]
			if !ok {
				res.Warnings = append(res.Warnings, fmt.Sprintf("goal skipped: unknown nutrient key %q", g.NutrientKey))
				continue
			}
			inputs = append(inputs, goals.GoalInput{NutrientID: nid, MinValue: g.MinValue, MaxValue: g.MaxValue, IdealMin: g.IdealMin, IdealMax: g.IdealMax})
		}
		if len(inputs) > 0 {
			if err := s.goalsSt.SaveGoals(ctx, userID, inputs); err != nil {
				return ImportResult{}, fmt.Errorf("save goals: %w", err)
			}
			res.GoalsSet = len(inputs)
		}
	}

	for _, it := range data.Sections.DefaultDay {
		catID, mealID, ok, err := s.resolveCategoryAndMeal(ctx, it.CategoryName, it.Meal)
		if err != nil {
			return ImportResult{}, err
		}
		if !ok {
			res.DefaultDayItemsSkipped++
			res.Warnings = append(res.Warnings, "default day item skipped: "+unresolvedReason(it))
			continue
		}
		if err := s.planning.AddDefaultItem(ctx, userID, catID, mealID, it.Quantity); err != nil {
			return ImportResult{}, fmt.Errorf("add default day item: %w", err)
		}
		res.DefaultDayItemsAdded++
	}

	for _, dp := range data.Sections.DayPlans {
		date, err := time.Parse(time.DateOnly, dp.Date)
		if err != nil {
			return ImportResult{}, fmt.Errorf("%w: parse day plan date %q: %w", ErrInvalidData, dp.Date, err)
		}
		for _, it := range dp.Items {
			catID, mealID, ok, err := s.resolveCategoryAndMeal(ctx, it.CategoryName, it.Meal)
			if err != nil {
				return ImportResult{}, err
			}
			if !ok {
				res.DayPlanItemsSkipped++
				res.Warnings = append(res.Warnings, fmt.Sprintf("day plan item on %s skipped: %s", dp.Date, unresolvedReason(it)))
				continue
			}
			if err := s.planning.AddItem(ctx, userID, date, catID, mealID, it.Quantity); err != nil {
				return ImportResult{}, fmt.Errorf("add day plan item (%s): %w", dp.Date, err)
			}
			res.DayPlanItemsAdded++
		}
	}

	for _, st := range data.Sections.AppSettings {
		if err := s.q.SetAppSetting(ctx, sqlc.SetAppSettingParams{Key: st.Key, Value: st.Value}); err != nil {
			return ImportResult{}, fmt.Errorf("set app setting %q: %w", st.Key, err)
		}
		res.AppSettingsSet++
	}

	return res, nil
}

// importMeal applies m, reporting whether a new meal was created (as
// opposed to an existing one being updated in place).
func (s *Store) importMeal(ctx context.Context, userID uuid.UUID, m Meal, nutrientIDByKey map[string]int16) (created bool, err error) {
	amounts := make(map[int16]float64, len(m.NutrientAmounts))
	for key, amt := range m.NutrientAmounts {
		if id, ok := nutrientIDByKey[key]; ok {
			amounts[id] = amt
		}
	}
	labelIDs := make([]uuid.UUID, 0, len(m.Labels))
	for _, name := range m.Labels {
		label, err := s.q.GetMealLabelByName(ctx, name)
		if errors.Is(err, sql.ErrNoRows) {
			continue // label not present in this instance/import — drop the assignment, not a hard failure
		}
		if err != nil {
			return false, fmt.Errorf("look up label %q: %w", name, err)
		}
		labelIDs = append(labelIDs, label.ID)
	}

	favoriteCategoryIDs := make([]uuid.UUID, 0, len(m.FavoriteCategories))
	for _, name := range m.FavoriteCategories {
		cat, err := s.q.GetMealCategoryByName(ctx, name)
		if errors.Is(err, sql.ErrNoRows) {
			continue // category not present in this instance/import — drop the favorite, not a hard failure
		}
		if err != nil {
			return false, fmt.Errorf("look up category %q: %w", name, err)
		}
		favoriteCategoryIDs = append(favoriteCategoryIDs, cat.ID)
	}

	input := mealslib.MealInput{
		Name: m.Name, RecipeURL: m.RecipeURL, ServingLabel: m.ServingLabel, ServingAmount: m.ServingAmount,
		FavoriteCategoryIDs: favoriteCategoryIDs, NutrientAmounts: amounts, LabelIDs: labelIDs,
	}

	if m.Source == "manual" {
		existing, err := s.q.GetManualMealByName(ctx, m.Name)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := s.meals.Create(ctx, userID, input); err != nil {
				return false, err
			}
			return true, nil
		case err != nil:
			return false, err
		default:
			return false, s.meals.Update(ctx, existing.ID, input)
		}
	}

	// Sourced meal (mealie/off/tandoor): mealslib.Store.Import upserts the
	// core fields by (source, source_ref) in one call, but doesn't take
	// is_favorite/labels (it's built for the Mealie/OFF importers, which
	// have no such concept) — Update fills those in right after. Update
	// never touches source/source_ref, so it can't undo the upsert.
	_, existErr := s.q.GetMealBySourceRef(ctx, sqlc.GetMealBySourceRefParams{
		Source: m.Source, SourceRef: sql.NullString{String: m.SourceRef, Valid: m.SourceRef != ""},
	})
	created = errors.Is(existErr, sql.ErrNoRows)

	id, err := s.meals.Import(ctx, userID, mealslib.ImportInput{
		Name: m.Name, Source: m.Source, SourceRef: m.SourceRef,
		RecipeURL: m.RecipeURL, ServingLabel: m.ServingLabel, ServingAmount: m.ServingAmount,
		NutrientAmounts: amounts,
	})
	if err != nil {
		return false, err
	}
	if err := s.meals.Update(ctx, id, input); err != nil {
		return false, err
	}
	return created, nil
}

// resolveCategoryAndMeal looks up categoryName and ref by natural key,
// reporting ok=false (not an error) if either doesn't exist on this
// instance — the caller skips that one item rather than failing the import.
func (s *Store) resolveCategoryAndMeal(ctx context.Context, categoryName string, ref MealRef) (categoryID, mealID uuid.UUID, ok bool, err error) {
	cat, err := s.q.GetMealCategoryByName(ctx, categoryName)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, false, fmt.Errorf("look up category %q: %w", categoryName, err)
	}

	mealID, found, err := s.findMealID(ctx, ref)
	if err != nil {
		return uuid.Nil, uuid.Nil, false, err
	}
	if !found {
		return uuid.Nil, uuid.Nil, false, nil
	}
	return cat.ID, mealID, true, nil
}

func (s *Store) findMealID(ctx context.Context, ref MealRef) (uuid.UUID, bool, error) {
	var (
		m   sqlc.Meal
		err error
	)
	if ref.SourceRef != "" {
		m, err = s.q.GetMealBySourceRef(ctx, sqlc.GetMealBySourceRefParams{
			Source: ref.Source, SourceRef: sql.NullString{String: ref.SourceRef, Valid: true},
		})
	} else {
		m, err = s.q.GetManualMealByName(ctx, ref.Name)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return m.ID, true, nil
}

func unresolvedReason(it DayItem) string {
	return fmt.Sprintf("category %q or meal %q not found", it.CategoryName, it.Meal.Name)
}
