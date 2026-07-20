package exportimport_test

import (
	"database/sql"
	"testing"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/dbtest"
	"github.com/Stasky745/victus/internal/exportimport"
)

func newTestStore(t *testing.T) (*exportimport.Store, sqlc.Querier) {
	t.Helper()
	sqlDB := dbtest.NewDB(t)
	q, err := db.NewQuerier(dbtest.Driver(), sqlDB)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}
	store, err := exportimport.New(sqlDB, dbtest.Driver())
	if err != nil {
		t.Fatalf("new exportimport store: %v", err)
	}
	return store, q
}

func newTestUser(t *testing.T, q sqlc.Querier) uuid.UUID {
	t.Helper()
	suffix := t.Name() + "-" + uuid.NewString()
	user, err := q.CreateUser(t.Context(), sqlc.CreateUserParams{
		ID:          uuid.New(),
		OidcSubject: sql.NullString{String: "test-subject-" + suffix, Valid: true},
		Email:       "test-" + suffix + "@example.com",
	})
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return user.ID
}

func allSections() exportimport.Selection {
	return exportimport.Selection{
		MealCategories: true, MealLabels: true, Meals: true, Goals: true,
		DefaultDay: true, DayPlans: true, AppSettings: true,
	}
}

func TestExportImport_RoundTrip(t *testing.T) {
	store, q := newTestStore(t)
	ctx := t.Context()
	userID := newTestUser(t, q)

	// Seed some data directly through the querier.
	cat, err := q.CreateMealCategory(ctx, sqlc.CreateMealCategoryParams{ID: uuid.New(), Name: "Second Breakfast", SortOrder: 99})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	label, err := q.CreateMealLabel(ctx, sqlc.CreateMealLabelParams{ID: uuid.New(), Name: "quick", Color: "blue", SortOrder: 1})
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	nutrients, err := q.ListNutrients(ctx)
	if err != nil || len(nutrients) == 0 {
		t.Fatalf("list nutrients: %v", err)
	}
	calID := nutrients[0].ID

	meal, err := q.CreateMeal(ctx, sqlc.CreateMealParams{
		ID: uuid.New(), Name: "Toast", Source: "manual", ServingLabel: "per serving", ServingAmount: 1, IsFavorite: true,
	})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	if err := q.SetMealNutrientValue(ctx, sqlc.SetMealNutrientValueParams{MealID: meal.ID, NutrientID: calID, Amount: 250}); err != nil {
		t.Fatalf("set nutrient value: %v", err)
	}
	if err := q.AddMealLabelAssignment(ctx, sqlc.AddMealLabelAssignmentParams{MealID: meal.ID, LabelID: label.ID}); err != nil {
		t.Fatalf("add label assignment: %v", err)
	}

	minVal := 1300.0
	if err := q.SetUserNutrientGoal(ctx, sqlc.SetUserNutrientGoalParams{UserID: userID, NutrientID: calID, MinValue: &minVal}); err != nil {
		t.Fatalf("set goal: %v", err)
	}

	if _, err := q.AddDefaultDayItem(ctx, sqlc.AddDefaultDayItemParams{
		ID: uuid.New(), UserID: userID, CategoryID: cat.ID, MealID: meal.ID, Quantity: 2,
	}); err != nil {
		t.Fatalf("add default day item: %v", err)
	}

	if err := q.SetAppSetting(ctx, sqlc.SetAppSettingParams{Key: "goal_info_url", Value: "https://example.com/custom"}); err != nil {
		t.Fatalf("set app setting: %v", err)
	}

	// --- Export ---
	exported, err := store.Export(ctx, userID, allSections())
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(exported.Sections.MealCategories) == 0 {
		t.Error("expected at least one exported category")
	}
	if len(exported.Sections.Meals) != 1 {
		t.Fatalf("expected exactly 1 exported meal, got %d", len(exported.Sections.Meals))
	}
	em := exported.Sections.Meals[0]
	if em.Name != "Toast" || !em.IsFavorite || em.NutrientAmounts["calories"] != 250 {
		t.Errorf("unexpected exported meal: %+v", em)
	}
	if len(em.Labels) != 1 || em.Labels[0] != "quick" {
		t.Errorf("expected exported meal to carry label %q, got %v", "quick", em.Labels)
	}

	// --- Import into a *different* user on the same instance (simulating a
	// fresh install that already has the seeded nutrients/categories) ---
	otherUser := newTestUser(t, q)
	result, err := store.Import(ctx, otherUser, exported)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("expected no warnings on a clean import, got: %v", result.Warnings)
	}
	if result.MealsCreated != 0 || result.MealsUpdated != 1 {
		t.Errorf("expected the existing meal to be updated in place (created=0, updated=1), got created=%d updated=%d", result.MealsCreated, result.MealsUpdated)
	}
	if result.CategoriesUnchanged == 0 {
		t.Error("expected the pre-existing category to be recognized as unchanged")
	}
	if result.GoalsSet != 1 {
		t.Errorf("expected 1 goal set for the importing user, got %d", result.GoalsSet)
	}
	if result.DefaultDayItemsAdded != 1 {
		t.Errorf("expected 1 default day item added, got %d", result.DefaultDayItemsAdded)
	}

	// The imported goal must belong to otherUser, not leak back to userID's
	// original value or apply to the wrong account.
	otherGoals, err := q.GetUserNutrientGoals(ctx, otherUser)
	if err != nil {
		t.Fatalf("get other user's goals: %v", err)
	}
	if len(otherGoals) != 1 || otherGoals[0].MinValue == nil || *otherGoals[0].MinValue != 1300 {
		t.Errorf("expected other user to have the imported goal, got %+v", otherGoals)
	}
}

// TestExportImport_ReimportIsIdempotent guards the "update in place" contract:
// importing the same export twice must not duplicate categories/labels/meals
// or double up default-day items.
func TestExportImport_ReimportIsIdempotent(t *testing.T) {
	store, q := newTestStore(t)
	ctx := t.Context()
	userID := newTestUser(t, q)

	if _, err := q.CreateMeal(ctx, sqlc.CreateMealParams{
		ID: uuid.New(), Name: "Oatmeal", Source: "manual", ServingLabel: "per serving", ServingAmount: 1,
	}); err != nil {
		t.Fatalf("create meal: %v", err)
	}

	exported, err := store.Export(ctx, userID, exportimport.Selection{Meals: true})
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := store.Import(ctx, userID, exported); err != nil {
			t.Fatalf("import #%d: %v", i, err)
		}
	}

	all, err := q.ListMeals(ctx, 100)
	if err != nil {
		t.Fatalf("list meals: %v", err)
	}
	var count int
	for _, m := range all {
		if m.Name == "Oatmeal" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one Oatmeal meal after two imports, got %d", count)
	}
}

// TestExportImport_SkipsUnresolvableReferences guards against a hard
// failure when a default-day item references a meal that isn't part of
// this import and doesn't already exist on the target instance.
func TestExportImport_SkipsUnresolvableReferences(t *testing.T) {
	store, q := newTestStore(t)
	ctx := t.Context()
	userID := newTestUser(t, q)

	cats, err := q.ListMealCategories(ctx)
	if err != nil || len(cats) == 0 {
		t.Fatalf("list categories: %v", err)
	}

	data := exportimport.Export{
		Version: exportimport.CurrentVersion,
		Sections: exportimport.Sections{
			DefaultDay: []exportimport.DayItem{
				{CategoryName: cats[0].Name, Meal: exportimport.MealRef{Name: "Ghost Meal", Source: "manual"}, Quantity: 1},
			},
		},
	}

	result, err := store.Import(ctx, userID, data)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.DefaultDayItemsSkipped != 1 {
		t.Errorf("expected 1 skipped default day item, got %d", result.DefaultDayItemsSkipped)
	}
	if len(result.Warnings) != 1 {
		t.Errorf("expected 1 warning, got %v", result.Warnings)
	}
}

// TestExportImport_RejectsUnknownVersion guards against silently
// misinterpreting a file written by an incompatible future version.
func TestExportImport_RejectsUnknownVersion(t *testing.T) {
	store, q := newTestStore(t)
	userID := newTestUser(t, q)

	_, err := store.Import(t.Context(), userID, exportimport.Export{Version: exportimport.CurrentVersion + 1})
	if err == nil {
		t.Fatal("expected an error importing an unsupported version")
	}
}
