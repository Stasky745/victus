package exportimport

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/db/sqlc"
)

// Export builds the export file for userID, including only the sections
// sel selects. The meal library (categories/labels/meals) is shared/global
// — exported regardless of which user asks — while goals/Default
// Day/day-plan history are always the exporting user's own.
func (s *Store) Export(ctx context.Context, userID uuid.UUID, sel Selection) (Export, error) {
	out := Export{Version: CurrentVersion, ExportedAt: time.Now().UTC()}

	keyByID, _, err := s.nutrientMaps(ctx)
	if err != nil {
		return Export{}, err
	}

	// Category/label name maps are needed for DayItem/DayPlan resolution
	// even when those sections themselves aren't selected, so they're
	// always loaded — cheap, near-static reference tables.
	categories, err := s.q.ListMealCategories(ctx)
	if err != nil {
		return Export{}, fmt.Errorf("list categories: %w", err)
	}
	categoryNameByID := make(map[uuid.UUID]string, len(categories))
	for _, c := range categories {
		categoryNameByID[c.ID] = c.Name
	}
	if sel.MealCategories {
		for _, c := range categories {
			out.Sections.MealCategories = append(out.Sections.MealCategories, MealCategory{Name: c.Name, SortOrder: c.SortOrder})
		}
	}

	labels, err := s.q.ListMealLabels(ctx)
	if err != nil {
		return Export{}, fmt.Errorf("list labels: %w", err)
	}
	labelNameByID := make(map[uuid.UUID]string, len(labels))
	for _, l := range labels {
		labelNameByID[l.ID] = l.Name
	}
	if sel.MealLabels {
		for _, l := range labels {
			out.Sections.MealLabels = append(out.Sections.MealLabels, MealLabel{Name: l.Name, Color: l.Color, SortOrder: l.SortOrder})
		}
	}

	cache := newMealCache(s.q)

	if sel.Meals {
		meals, err := s.q.ListMeals(ctx, exportListLimit)
		if err != nil {
			return Export{}, fmt.Errorf("list meals: %w", err)
		}
		for _, m := range meals {
			cache.byID[m.ID] = m
			exported, err := s.exportMeal(ctx, m, keyByID, labelNameByID, categoryNameByID)
			if err != nil {
				return Export{}, err
			}
			out.Sections.Meals = append(out.Sections.Meals, exported)
		}
	}

	if sel.Goals {
		goalsList, err := s.goalsSt.ListGoals(ctx, userID)
		if err != nil {
			return Export{}, fmt.Errorf("list goals: %w", err)
		}
		for _, g := range goalsList {
			if g.MinValue == nil && g.MaxValue == nil && g.IdealMin == nil && g.IdealMax == nil {
				continue // nothing configured for this nutrient — not worth a row
			}
			out.Sections.Goals = append(out.Sections.Goals, Goal{
				NutrientKey: g.Key, MinValue: g.MinValue, MaxValue: g.MaxValue, IdealMin: g.IdealMin, IdealMax: g.IdealMax,
			})
		}
	}

	if sel.DefaultDay {
		items, err := s.q.ListDefaultDayItems(ctx, userID)
		if err != nil {
			return Export{}, fmt.Errorf("list default day items: %w", err)
		}
		for _, it := range items {
			meal, err := cache.get(ctx, it.MealID)
			if err != nil {
				return Export{}, fmt.Errorf("resolve default day item meal: %w", err)
			}
			out.Sections.DefaultDay = append(out.Sections.DefaultDay, DayItem{
				CategoryName: categoryNameByID[it.CategoryID],
				Meal:         mealRefOf(meal),
				Quantity:     it.Quantity,
			})
		}
	}

	if sel.DayPlans {
		plans, err := s.q.ListDayPlansInRange(ctx, sqlc.ListDayPlansInRangeParams{
			UserID: userID, PlanDate: dayPlanRangeStart, PlanDate_2: dayPlanRangeEnd,
		})
		if err != nil {
			return Export{}, fmt.Errorf("list day plans: %w", err)
		}
		for _, p := range plans {
			items, err := s.q.ListDayPlanItems(ctx, p.ID)
			if err != nil {
				return Export{}, fmt.Errorf("list day plan items: %w", err)
			}
			dp := DayPlan{Date: p.PlanDate.Format(time.DateOnly)}
			for _, it := range items {
				meal, err := cache.get(ctx, it.MealID)
				if err != nil {
					return Export{}, fmt.Errorf("resolve day plan item meal: %w", err)
				}
				dp.Items = append(dp.Items, DayItem{
					CategoryName: categoryNameByID[it.CategoryID],
					Meal:         mealRefOf(meal),
					Quantity:     it.Quantity,
				})
			}
			out.Sections.DayPlans = append(out.Sections.DayPlans, dp)
		}
	}

	if sel.AppSettings {
		settings, err := s.q.ListAppSettings(ctx)
		if err != nil {
			return Export{}, fmt.Errorf("list app settings: %w", err)
		}
		for _, st := range settings {
			out.Sections.AppSettings = append(out.Sections.AppSettings, AppSetting{Key: st.Key, Value: st.Value})
		}
	}

	return out, nil
}

func (s *Store) exportMeal(ctx context.Context, m sqlc.Meal, nutrientKeyByID map[int16]string, labelNameByID, categoryNameByID map[uuid.UUID]string) (Meal, error) {
	values, err := s.q.ListMealNutrientValues(ctx, m.ID)
	if err != nil {
		return Meal{}, fmt.Errorf("list nutrient values for meal %q: %w", m.Name, err)
	}
	amounts := make(map[string]float64, len(values))
	for _, v := range values {
		if key, ok := nutrientKeyByID[v.NutrientID]; ok {
			amounts[key] = v.Amount
		}
	}

	labelIDs, err := s.q.ListMealLabelIDsForMeal(ctx, m.ID)
	if err != nil {
		return Meal{}, fmt.Errorf("list labels for meal %q: %w", m.Name, err)
	}
	labelNames := make([]string, 0, len(labelIDs))
	for _, id := range labelIDs {
		if name, ok := labelNameByID[id]; ok {
			labelNames = append(labelNames, name)
		}
	}

	favoriteCategoryIDs, err := s.q.ListFavoriteCategoryIDsForMeal(ctx, m.ID)
	if err != nil {
		return Meal{}, fmt.Errorf("list favorite categories for meal %q: %w", m.Name, err)
	}
	favoriteCategoryNames := make([]string, 0, len(favoriteCategoryIDs))
	for _, id := range favoriteCategoryIDs {
		if name, ok := categoryNameByID[id]; ok {
			favoriteCategoryNames = append(favoriteCategoryNames, name)
		}
	}

	return Meal{
		MealRef:            mealRefOf(m),
		RecipeURL:          m.RecipeUrl.String,
		ServingLabel:       m.ServingLabel,
		ServingAmount:      m.ServingAmount,
		FavoriteCategories: favoriteCategoryNames,
		NutrientAmounts:    amounts,
		Labels:             labelNames,
	}, nil
}
