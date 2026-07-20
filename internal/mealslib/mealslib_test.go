package mealslib_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/dbtest"
	"github.com/Stasky745/victus/internal/mealslib"
)

// newTestStore returns a mealslib.Store and the raw Querier backing it (for
// test fixtures like newTestUser that need to reach the DB directly),
// against whichever backend TEST_DB_DRIVER selects.
func newTestStore(t *testing.T) (*mealslib.Store, sqlc.Querier) {
	t.Helper()
	sqlDB := dbtest.NewDB(t)
	q, err := db.NewQuerier(dbtest.Driver(), sqlDB)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}
	store, err := mealslib.New(sqlDB, dbtest.Driver())
	if err != nil {
		t.Fatalf("new mealslib store: %v", err)
	}
	return store, q
}

// newTestUser satisfies meals.created_by's foreign key — mealslib.Store
// intentionally doesn't create users itself, so tests must.
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

func nutrientIDByKey(t *testing.T, store *mealslib.Store, key string) int16 {
	t.Helper()
	nutrients, err := store.ListNutrients(t.Context())
	if err != nil {
		t.Fatalf("list nutrients: %v", err)
	}
	for _, n := range nutrients {
		if n.Key == key {
			return n.ID
		}
	}
	t.Fatalf("nutrient %q not found in seeded registry", key)
	return 0
}

func TestStore_CreateGetUpdateDelete(t *testing.T) {
	store, q := newTestStore(t)
	ctx := t.Context()
	userID := newTestUser(t, q)

	calories := nutrientIDByKey(t, store, "calories")
	protein := nutrientIDByKey(t, store, "protein_g")

	id, err := store.Create(ctx, userID, mealslib.MealInput{
		Name:          "Chicken & Rice",
		RecipeURL:     "https://example.com/recipes/chicken-rice",
		ServingLabel:  "per serving",
		ServingAmount: 1,
		NutrientAmounts: map[int16]float64{
			calories: 650,
			protein:  45,
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	meal, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if meal.Name != "Chicken & Rice" {
		t.Errorf("name = %q, want %q", meal.Name, "Chicken & Rice")
	}
	if meal.RecipeURL != "https://example.com/recipes/chicken-rice" {
		t.Errorf("recipe url = %q", meal.RecipeURL)
	}
	if meal.Source != "manual" {
		t.Errorf("source = %q, want manual", meal.Source)
	}

	var gotCalories, gotProtein *float64
	var gotIron *float64
	for _, nv := range meal.NutrientValues {
		switch nv.NutrientID {
		case calories:
			gotCalories = nv.Amount
		case protein:
			gotProtein = nv.Amount
		}
		if nv.Key == "iron_mg" {
			gotIron = nv.Amount
		}
	}
	if gotCalories == nil || *gotCalories != 650 {
		t.Errorf("calories = %v, want 650", gotCalories)
	}
	if gotProtein == nil || *gotProtein != 45 {
		t.Errorf("protein = %v, want 45", gotProtein)
	}
	if gotIron != nil {
		t.Errorf("iron = %v, want nil (never set)", *gotIron)
	}

	// Update: change name, drop protein, add nothing else.
	if err := store.Update(ctx, id, mealslib.MealInput{
		Name:          "Chicken & Rice (updated)",
		ServingLabel:  "per serving",
		ServingAmount: 1,
		NutrientAmounts: map[int16]float64{
			calories: 700,
		},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	updated, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if updated.Name != "Chicken & Rice (updated)" {
		t.Errorf("name after update = %q", updated.Name)
	}
	if updated.RecipeURL != "" {
		t.Errorf("recipe url after update = %q, want cleared", updated.RecipeURL)
	}
	for _, nv := range updated.NutrientValues {
		if nv.NutrientID == protein && nv.Amount != nil {
			t.Errorf("protein should have been cleared by update, got %v", *nv.Amount)
		}
		if nv.NutrientID == calories && (nv.Amount == nil || *nv.Amount != 700) {
			t.Errorf("calories after update = %v, want 700", nv.Amount)
		}
	}

	if err := store.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Get(ctx, id); err == nil {
		t.Fatal("expected an error getting a deleted meal")
	}
}

func TestStore_Search(t *testing.T) {
	store, q := newTestStore(t)
	ctx := t.Context()
	userID := newTestUser(t, q)

	if _, err := store.Create(ctx, userID, mealslib.MealInput{Name: "Bolognese", ServingLabel: "per serving", ServingAmount: 1}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Create(ctx, userID, mealslib.MealInput{Name: "Poke Bowl", ServingLabel: "per serving", ServingAmount: 1}); err != nil {
		t.Fatalf("create: %v", err)
	}

	results, err := store.Search(ctx, "bolognse", 10) // deliberate typo — fuzzy search should still match
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 || results[0].Name != "Bolognese" {
		t.Errorf("expected fuzzy search to find Bolognese, got %+v", results)
	}
}

// TestStore_Search_TypoInMultiWordName guards against a real bug found
// during manual testing: whole-string similarity dilutes a single-word
// typo's score too much once other words are mixed into a multi-word name,
// and silently returns nothing at the default threshold. Search must use
// word-level similarity instead (Postgres: word_similarity; SQLite: the
// trigram-OR FTS5 match, which shares the same word-level tolerance).
func TestStore_Search_TypoInMultiWordName(t *testing.T) {
	store, q := newTestStore(t)
	ctx := t.Context()
	userID := newTestUser(t, q)

	if _, err := store.Create(ctx, userID, mealslib.MealInput{Name: "Chicken & Rice", ServingLabel: "per serving", ServingAmount: 1}); err != nil {
		t.Fatalf("create: %v", err)
	}

	results, err := store.Search(ctx, "chikcen", 10) // typo in one word of a three-word name
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 || results[0].Name != "Chicken & Rice" {
		t.Errorf("expected a single-word typo to still match a multi-word name, got %+v", results)
	}
}

func TestStore_CategoryCRUD(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := t.Context()

	before, err := store.ListCategories(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	cat, err := store.CreateCategory(ctx, "Second Breakfast")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	if cat.Name != "Second Breakfast" {
		t.Errorf("name = %q", cat.Name)
	}
	if int(cat.SortOrder) != len(before)+1 {
		t.Errorf("sort_order = %d, want %d", cat.SortOrder, len(before)+1)
	}

	catID := cat.ID
	renamed, err := store.RenameCategory(ctx, catID, "Elevenses")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if renamed.Name != "Elevenses" {
		t.Errorf("name after rename = %q", renamed.Name)
	}
	if renamed.SortOrder != cat.SortOrder {
		t.Errorf("sort_order changed on rename: got %d, want %d", renamed.SortOrder, cat.SortOrder)
	}

	if err := store.DeleteCategory(ctx, catID); err != nil {
		t.Fatalf("delete category: %v", err)
	}
	after, err := store.ListCategories(ctx)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("category count after delete = %d, want %d", len(after), len(before))
	}
}

// TestStore_RenameCategory_NonexistentReturnsErrCategoryNotFound guards
// against a real bug found during review: RenameCategory's UpdateMealCategory
// call (a separate, non-transactional statement from its existence check)
// used to leak a raw sql.ErrNoRows instead of the documented
// ErrCategoryNotFound when the row didn't exist at UPDATE time — the exact
// error a concurrent delete between the check and the update would produce.
func TestStore_RenameCategory_NonexistentReturnsErrCategoryNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := t.Context()

	if _, err := store.RenameCategory(ctx, uuid.New(), "Ghost Category"); !errors.Is(err, mealslib.ErrCategoryNotFound) {
		t.Errorf("expected ErrCategoryNotFound, got %v", err)
	}
}

// TestStore_DeleteCategory_BlockedByFKWhenInUse guards the foreign key that
// already exists today (day_plan_items.category_id REFERENCES
// meal_categories(id)) even though no UI creates day plans until M3 — the
// constraint is live now, so a regression here (e.g. an accidental ON
// DELETE CASCADE, or an error swallowed somewhere in the delete path)
// should be caught before M3 ships on top of it.
func TestStore_DeleteCategory_BlockedByFKWhenInUse(t *testing.T) {
	store, q := newTestStore(t)
	ctx := t.Context()
	userID := newTestUser(t, q)

	mealID, err := store.Create(ctx, userID, mealslib.MealInput{Name: "Oatmeal", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	cat, err := store.CreateCategory(ctx, "Breakfast Slot")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	dayPlan, err := q.GetOrCreateDayPlan(ctx, sqlc.GetOrCreateDayPlanParams{
		ID:       uuid.New(),
		UserID:   userID,
		PlanDate: time.Now(),
	})
	if err != nil {
		t.Fatalf("create day plan: %v", err)
	}
	if _, err := q.AddDayPlanItem(ctx, sqlc.AddDayPlanItemParams{
		ID:         uuid.New(),
		DayPlanID:  dayPlan.ID,
		CategoryID: cat.ID,
		MealID:     mealID,
		Quantity:   1,
	}); err != nil {
		t.Fatalf("add day plan item: %v", err)
	}

	if err := store.DeleteCategory(ctx, cat.ID); err == nil {
		t.Fatal("expected deleting a category still referenced by a day plan item to fail")
	}
}

func TestStore_Import_CreatesNewMeal(t *testing.T) {
	store, q := newTestStore(t)
	ctx := t.Context()
	userID := newTestUser(t, q)

	calID := nutrientIDByKey(t, store, "calories")
	id, err := store.Import(ctx, userID, mealslib.ImportInput{
		Name:          "Nutella",
		Source:        "off",
		SourceRef:     "3017620422003",
		RecipeURL:     "",
		ServingLabel:  "per 100g",
		ServingAmount: 100,
		NutrientAmounts: map[int16]float64{
			calID: 539,
		},
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	meal, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if meal.Name != "Nutella" {
		t.Errorf("name = %q", meal.Name)
	}
	if meal.Source != "off" {
		t.Errorf("source = %q, want off", meal.Source)
	}
	for _, nv := range meal.NutrientValues {
		if nv.NutrientID == calID {
			if nv.Amount == nil || *nv.Amount != 539 {
				t.Errorf("calories = %v, want 539", nv.Amount)
			}
		}
	}
}

// TestStore_Import_ReImportUpdatesInPlace guards the whole point of keying
// the upsert by (source, source_ref): importing the same external item
// again (the user re-scans a barcode, or re-imports a Mealie recipe after
// editing it there) must update the existing meal, not create a duplicate
// library entry every time.
func TestStore_Import_ReImportUpdatesInPlace(t *testing.T) {
	store, q := newTestStore(t)
	ctx := t.Context()
	userID := newTestUser(t, q)

	calID := nutrientIDByKey(t, store, "calories")
	firstID, err := store.Import(ctx, userID, mealslib.ImportInput{
		Name: "Cheerios", Source: "off", SourceRef: "0016000275287",
		ServingLabel: "per 100g", ServingAmount: 100,
		NutrientAmounts: map[int16]float64{calID: 380},
	})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}

	secondID, err := store.Import(ctx, userID, mealslib.ImportInput{
		Name: "Cheerios (updated)", Source: "off", SourceRef: "0016000275287",
		ServingLabel: "per 100g", ServingAmount: 100,
		NutrientAmounts: map[int16]float64{calID: 400}, // OFF data changed since last import
	})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}

	if firstID != secondID {
		t.Fatalf("expected re-import to update the same meal id, got %s then %s", firstID, secondID)
	}

	all, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var count int
	for _, m := range all {
		if m.ID == firstID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one library entry for the re-imported item, got %d", count)
	}

	meal, err := store.Get(ctx, firstID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if meal.Name != "Cheerios (updated)" {
		t.Errorf("name = %q, want the re-imported name to have replaced the original", meal.Name)
	}
	for _, nv := range meal.NutrientValues {
		if nv.NutrientID == calID && (nv.Amount == nil || *nv.Amount != 400) {
			t.Errorf("calories = %v, want 400 (the re-imported value, not the original 380)", nv.Amount)
		}
	}
}

// TestStore_Import_ReImportClearsDroppedNutrients guards against a stale
// nutrient value surviving a re-import that no longer reports it — e.g. a
// Mealie recipe that used to have fiber data but was edited to remove it.
func TestStore_Import_ReImportClearsDroppedNutrients(t *testing.T) {
	store, q := newTestStore(t)
	ctx := t.Context()
	userID := newTestUser(t, q)

	calID := nutrientIDByKey(t, store, "calories")
	fiberID := nutrientIDByKey(t, store, "fiber_g")

	id, err := store.Import(ctx, userID, mealslib.ImportInput{
		Name: "Recipe", Source: "mealie", SourceRef: "some-slug",
		ServingLabel: "per serving", ServingAmount: 1,
		NutrientAmounts: map[int16]float64{calID: 500, fiberID: 5},
	})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}

	if _, err := store.Import(ctx, userID, mealslib.ImportInput{
		Name: "Recipe", Source: "mealie", SourceRef: "some-slug",
		ServingLabel: "per serving", ServingAmount: 1,
		NutrientAmounts: map[int16]float64{calID: 500}, // fiber no longer reported
	}); err != nil {
		t.Fatalf("second import: %v", err)
	}

	meal, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	for _, nv := range meal.NutrientValues {
		if nv.NutrientID == fiberID && nv.Amount != nil {
			t.Errorf("expected fiber to be cleared after a re-import that no longer reports it, got %v", *nv.Amount)
		}
	}
}

func TestStore_NutrientIDsByKey(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := t.Context()

	byKey, err := store.NutrientIDsByKey(ctx)
	if err != nil {
		t.Fatalf("nutrient ids by key: %v", err)
	}
	calID := nutrientIDByKey(t, store, "calories")
	if byKey["calories"] != calID {
		t.Errorf("byKey[calories] = %d, want %d", byKey["calories"], calID)
	}
	if _, ok := byKey["not_a_real_key"]; ok {
		t.Error("expected an unknown key to be absent from the map")
	}
}

func TestStore_ToggleFavorite_RoundTrips(t *testing.T) {
	store, q := newTestStore(t)
	ctx := t.Context()
	userID := newTestUser(t, q)

	id, err := store.Create(ctx, userID, mealslib.MealInput{Name: "Toast", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	favorites, err := store.ListFavorites(ctx)
	if err != nil {
		t.Fatalf("list favorites: %v", err)
	}
	if containsMealID(favorites, id) {
		t.Fatal("a freshly created meal should not start as a favorite")
	}

	m, err := store.ToggleFavorite(ctx, id)
	if err != nil {
		t.Fatalf("toggle favorite: %v", err)
	}
	if !m.IsFavorite {
		t.Error("expected the meal to be favorited after the first toggle")
	}
	favorites, err = store.ListFavorites(ctx)
	if err != nil {
		t.Fatalf("list favorites: %v", err)
	}
	if !containsMealID(favorites, id) {
		t.Error("expected the toggled meal to appear in ListFavorites")
	}

	m, err = store.ToggleFavorite(ctx, id)
	if err != nil {
		t.Fatalf("toggle favorite again: %v", err)
	}
	if m.IsFavorite {
		t.Error("expected a second toggle to un-favorite the meal")
	}
}

func TestStore_Create_SetsFavoriteAndLabels(t *testing.T) {
	store, q := newTestStore(t)
	ctx := t.Context()
	userID := newTestUser(t, q)

	label, err := store.CreateLabel(ctx, "quick", "blue")
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	labelID := label.ID

	id, err := store.Create(ctx, userID, mealslib.MealInput{
		Name: "Instant Oats", ServingLabel: "per serving", ServingAmount: 1,
		IsFavorite: true, LabelIDs: []uuid.UUID{labelID},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	meal, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !meal.IsFavorite {
		t.Error("expected IsFavorite=true to be persisted on create")
	}
	if len(meal.Labels) != 1 || meal.Labels[0].ID != labelID {
		t.Errorf("expected the meal to carry the assigned label, got %+v", meal.Labels)
	}
}

// TestStore_Update_ReplacesLabels guards Update's replace (not merge)
// semantics for labels — the same contract SaveGoals/setNutrientValues
// already guarantee, now extended to label assignments.
func TestStore_Update_ReplacesLabels(t *testing.T) {
	store, q := newTestStore(t)
	ctx := t.Context()
	userID := newTestUser(t, q)

	labelA, err := store.CreateLabel(ctx, "A-"+uuid.NewString(), "red")
	if err != nil {
		t.Fatalf("create label a: %v", err)
	}
	labelB, err := store.CreateLabel(ctx, "B-"+uuid.NewString(), "green")
	if err != nil {
		t.Fatalf("create label b: %v", err)
	}
	idA, idB := labelA.ID, labelB.ID

	id, err := store.Create(ctx, userID, mealslib.MealInput{
		Name: "Salad", ServingLabel: "per serving", ServingAmount: 1, LabelIDs: []uuid.UUID{idA},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := store.Update(ctx, id, mealslib.MealInput{
		Name: "Salad", ServingLabel: "per serving", ServingAmount: 1, LabelIDs: []uuid.UUID{idB},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	meal, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(meal.Labels) != 1 || meal.Labels[0].ID != idB {
		t.Errorf("expected only label B after update, got %+v", meal.Labels)
	}
}

func TestStore_DeleteLabel_RemovesAssignment(t *testing.T) {
	store, q := newTestStore(t)
	ctx := t.Context()
	userID := newTestUser(t, q)

	label, err := store.CreateLabel(ctx, "temp-"+uuid.NewString(), "purple")
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	labelID := label.ID

	id, err := store.Create(ctx, userID, mealslib.MealInput{
		Name: "Curry", ServingLabel: "per serving", ServingAmount: 1, LabelIDs: []uuid.UUID{labelID},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := store.DeleteLabel(ctx, labelID); err != nil {
		t.Fatalf("delete label: %v", err)
	}

	meal, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(meal.Labels) != 0 {
		t.Errorf("expected the deleted label's assignment to be gone too, got %+v", meal.Labels)
	}
}

func TestStore_ListByLabel_And_SearchByLabel(t *testing.T) {
	store, q := newTestStore(t)
	ctx := t.Context()
	userID := newTestUser(t, q)

	label, err := store.CreateLabel(ctx, "tagged-"+uuid.NewString(), "amber")
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	labelID := label.ID

	taggedID, err := store.Create(ctx, userID, mealslib.MealInput{
		Name: "Tagged Tacos", ServingLabel: "per serving", ServingAmount: 1, LabelIDs: []uuid.UUID{labelID},
	})
	if err != nil {
		t.Fatalf("create tagged meal: %v", err)
	}
	if _, err := store.Create(ctx, userID, mealslib.MealInput{Name: "Untagged Tacos", ServingLabel: "per serving", ServingAmount: 1}); err != nil {
		t.Fatalf("create untagged meal: %v", err)
	}

	list, err := store.ListByLabel(ctx, labelID)
	if err != nil {
		t.Fatalf("list by label: %v", err)
	}
	if len(list) != 1 || list[0].ID != taggedID {
		t.Errorf("expected ListByLabel to return only the tagged meal, got %d results", len(list))
	}

	results, err := store.SearchByLabel(ctx, labelID, "Tacos", 10)
	if err != nil {
		t.Fatalf("search by label: %v", err)
	}
	if len(results) != 1 || results[0].ID != taggedID {
		t.Errorf("expected SearchByLabel to return only the tagged meal, got %d results", len(results))
	}
}

func TestStore_IsValidLabelColor(t *testing.T) {
	for _, c := range mealslib.LabelColors {
		if !mealslib.IsValidLabelColor(c) {
			t.Errorf("expected %q (from LabelColors itself) to be valid", c)
		}
	}
	if mealslib.IsValidLabelColor("chartreuse") {
		t.Error("expected an out-of-palette color to be invalid")
	}
}

func containsMealID(list []sqlc.Meal, id uuid.UUID) bool {
	for _, l := range list {
		if l.ID != id {
			continue
		}
		return true
	}
	return false
}
