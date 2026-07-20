package planning_test

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
	"github.com/Stasky745/victus/internal/planning"
	"github.com/Stasky745/victus/web/templates/day"
)

// newTestStores returns a planning.Store, a mealslib.Store, and the raw
// Querier backing both (for test fixtures like newTestUser that need to
// reach the DB directly), against whichever backend TEST_DB_DRIVER selects.
func newTestStores(t *testing.T) (*planning.Store, *mealslib.Store, sqlc.Querier) {
	t.Helper()
	sqlDB := dbtest.NewDB(t)
	q, err := db.NewQuerier(dbtest.Driver(), sqlDB)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}
	planningStore, err := planning.New(sqlDB, dbtest.Driver())
	if err != nil {
		t.Fatalf("new planning store: %v", err)
	}
	mealsStore, err := mealslib.New(sqlDB, dbtest.Driver())
	if err != nil {
		t.Fatalf("new mealslib store: %v", err)
	}
	return planningStore, mealsStore, q
}

func newTestUser(t *testing.T, q sqlc.Querier, label string) uuid.UUID {
	t.Helper()
	user, err := q.CreateUser(t.Context(), sqlc.CreateUserParams{
		ID:          uuid.New(),
		OidcSubject: sql.NullString{String: "test-subject-" + t.Name() + "-" + label, Valid: true},
		Email:       label + "@example.com",
	})
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return user.ID
}

func firstCategoryID(t *testing.T, store *planning.Store, userID uuid.UUID, date time.Time) uuid.UUID {
	t.Helper()
	day, err := store.GetDay(t.Context(), userID, date)
	if err != nil {
		t.Fatalf("get day: %v", err)
	}
	if len(day.Categories) == 0 {
		t.Fatal("expected at least one seeded meal category")
	}
	return day.Categories[0].Category.ID
}

func TestStore_GetDay_EmptyByDefault(t *testing.T) {
	store, _, q := newTestStores(t)
	userID := newTestUser(t, q, "a")

	day, err := store.GetDay(t.Context(), userID, time.Now())
	if err != nil {
		t.Fatalf("get day: %v", err)
	}
	if day.PlanID != uuid.Nil {
		t.Errorf("expected no day plan to exist yet, got PlanID = %v", day.PlanID)
	}
	if len(day.Categories) == 0 {
		t.Error("expected the seeded meal categories to still be listed even with no plan")
	}
	for _, cat := range day.Categories {
		if len(cat.Items) != 0 {
			t.Errorf("expected no items in category %q, got %d", cat.Category.Name, len(cat.Items))
		}
	}
	for _, nt := range day.Totals {
		if nt.Total != 0 {
			t.Errorf("expected zero total for %q, got %v", nt.DisplayName, nt.Total)
		}
	}
}

func TestStore_AddUpdateRemoveItem(t *testing.T) {
	store, meals, q := newTestStores(t)
	userID := newTestUser(t, q, "a")
	date := time.Now()

	calID := nutrientIDByKey(t, meals, "calories")
	mealID, err := meals.Create(t.Context(), userID, mealslib.MealInput{
		Name: "Oatmeal", ServingLabel: "per serving", ServingAmount: 1,
		NutrientAmounts: map[int16]float64{calID: 300},
	})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categoryID := firstCategoryID(t, store, userID, date)

	if err := store.AddItem(t.Context(), userID, date, categoryID, mealID, 2); err != nil {
		t.Fatalf("add item: %v", err)
	}

	day, err := store.GetDay(t.Context(), userID, date)
	if err != nil {
		t.Fatalf("get day: %v", err)
	}
	if day.PlanID == uuid.Nil {
		t.Fatal("expected a day plan to now exist")
	}
	var item planning.Item
	var found bool
	for _, cat := range day.Categories {
		if cat.Category.ID == categoryID && len(cat.Items) == 1 {
			item = cat.Items[0]
			found = true
		}
	}
	if !found {
		t.Fatalf("expected exactly one item in the target category, got: %+v", day.Categories)
	}
	if item.Quantity != 2 {
		t.Errorf("quantity = %v, want 2", item.Quantity)
	}
	if item.MealName != "Oatmeal" {
		t.Errorf("meal name = %q, want Oatmeal", item.MealName)
	}
	// 300 kcal/serving * 2 servings = 600
	assertNutrientTotal(t, day.Totals, calID, 600)

	if err := store.UpdateItemQuantity(t.Context(), userID, item.ID, 3); err != nil {
		t.Fatalf("update quantity: %v", err)
	}
	day, err = store.GetDay(t.Context(), userID, date)
	if err != nil {
		t.Fatalf("get day after update: %v", err)
	}
	assertNutrientTotal(t, day.Totals, calID, 900)

	if err := store.RemoveItem(t.Context(), userID, item.ID); err != nil {
		t.Fatalf("remove item: %v", err)
	}
	day, err = store.GetDay(t.Context(), userID, date)
	if err != nil {
		t.Fatalf("get day after remove: %v", err)
	}
	assertNutrientTotal(t, day.Totals, calID, 0)
}

// TestStore_AddItem_PreservesInsertOrderWithinCategory guards against a real
// bug found during review: AddDayPlanItem never set sort_order, so every row
// landed at 0 and ListDayPlanItems (which orders by it) had no tiebreaker,
// letting items within a category appear in arbitrary/unstable order.
func TestStore_AddItem_PreservesInsertOrderWithinCategory(t *testing.T) {
	store, meals, q := newTestStores(t)
	userID := newTestUser(t, q, "a")
	date := time.Now()
	categoryID := firstCategoryID(t, store, userID, date)

	names := []string{"First Meal", "Second Meal", "Third Meal"}
	for _, name := range names {
		mealID, err := meals.Create(t.Context(), userID, mealslib.MealInput{Name: name, ServingLabel: "per serving", ServingAmount: 1})
		if err != nil {
			t.Fatalf("create meal %q: %v", name, err)
		}
		if err := store.AddItem(t.Context(), userID, date, categoryID, mealID, 1); err != nil {
			t.Fatalf("add item %q: %v", name, err)
		}
	}

	for attempt := range 3 {
		day, err := store.GetDay(t.Context(), userID, date)
		if err != nil {
			t.Fatalf("get day (attempt %d): %v", attempt, err)
		}
		var got []string
		for _, cat := range day.Categories {
			if cat.Category.ID == categoryID {
				for _, item := range cat.Items {
					got = append(got, item.MealName)
				}
			}
		}
		if len(got) != len(names) {
			t.Fatalf("attempt %d: got %d items, want %d: %v", attempt, len(got), len(names), got)
		}
		for i, name := range names {
			if got[i] != name {
				t.Errorf("attempt %d: item[%d] = %q, want %q (order = %v)", attempt, i, got[i], name, got)
			}
		}
	}
}

func TestStore_UpdateItemQuantity_RejectsWrongOwner(t *testing.T) {
	store, meals, q := newTestStores(t)
	owner := newTestUser(t, q, "owner")
	attacker := newTestUser(t, q, "attacker")
	date := time.Now()

	mealID, err := meals.Create(t.Context(), owner, mealslib.MealInput{Name: "Toast", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categoryID := firstCategoryID(t, store, owner, date)
	if err := store.AddItem(t.Context(), owner, date, categoryID, mealID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}
	day, err := store.GetDay(t.Context(), owner, date)
	if err != nil {
		t.Fatalf("get day: %v", err)
	}
	itemID := day.Categories[0].Items[0].ID

	if err := store.UpdateItemQuantity(t.Context(), attacker, itemID, 99); !errors.Is(err, planning.ErrNotOwner) {
		t.Errorf("expected ErrNotOwner, got %v", err)
	}
	if err := store.RemoveItem(t.Context(), attacker, itemID); !errors.Is(err, planning.ErrNotOwner) {
		t.Errorf("expected ErrNotOwner, got %v", err)
	}

	// Confirm the attacker's rejected calls didn't actually change anything.
	day, err = store.GetDay(t.Context(), owner, date)
	if err != nil {
		t.Fatalf("get day: %v", err)
	}
	if len(day.Categories[0].Items) != 1 || day.Categories[0].Items[0].Quantity != 1 {
		t.Errorf("expected the item to be untouched, got: %+v", day.Categories[0].Items)
	}
}

// TestStore_AddItem_NonexistentCategoryLeavesNoOrphanedPlan guards against a
// real bug found during review: AddItem used to run GetOrCreateDayPlan and
// AddDayPlanItem as two separate, non-transactional statements, so a
// failure on the second (e.g. a bad category_id) left the first's insert
// committed — an empty day_plans row a future week view would count as
// "planned" even though it has zero items.
func TestStore_AddItem_NonexistentCategoryLeavesNoOrphanedPlan(t *testing.T) {
	store, meals, q := newTestStores(t)
	userID := newTestUser(t, q, "a")
	date := time.Now()

	mealID, err := meals.Create(t.Context(), userID, mealslib.MealInput{Name: "Cereal", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}

	if err := store.AddItem(t.Context(), userID, date, uuid.New(), mealID, 1); err == nil {
		t.Fatal("expected adding an item with a nonexistent category to fail")
	}

	_, err = q.GetDayPlan(t.Context(), sqlc.GetDayPlanParams{
		UserID:   userID,
		PlanDate: date,
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected no day plan to have been left behind, got err = %v", err)
	}
}

func TestStore_GetWeek_AveragesAcrossDays(t *testing.T) {
	store, meals, q := newTestStores(t)
	userID := newTestUser(t, q, "a")
	weekStart := day.MondayOf(time.Now())

	calID := nutrientIDByKey(t, meals, "calories")
	mealID, err := meals.Create(t.Context(), userID, mealslib.MealInput{
		Name: "Snack", ServingLabel: "per serving", ServingAmount: 1,
		NutrientAmounts: map[int16]float64{calID: 700},
	})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categoryID := firstCategoryID(t, store, userID, weekStart)

	// Add the 700-kcal item to exactly one of the 7 days.
	if err := store.AddItem(t.Context(), userID, weekStart, categoryID, mealID, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}

	week, err := store.GetWeek(t.Context(), userID, weekStart)
	if err != nil {
		t.Fatalf("get week: %v", err)
	}
	if len(week.Days) != planning.WeekLength {
		t.Fatalf("len(week.Days) = %d, want %d", len(week.Days), planning.WeekLength)
	}
	if !week.Days[0].Date.Equal(weekStart) {
		t.Errorf("week.Days[0].Date = %v, want %v", week.Days[0].Date, weekStart)
	}
	if !week.Days[6].Date.Equal(weekStart.AddDate(0, 0, 6)) {
		t.Errorf("week.Days[6].Date = %v, want %v", week.Days[6].Date, weekStart.AddDate(0, 0, 6))
	}

	// 700 kcal on one day, 0 on the other six -> average 100/day.
	found := false
	for _, nt := range week.Average {
		if nt.NutrientID == calID {
			found = true
			if nt.Total != 100 {
				t.Errorf("average calories = %v, want 100", nt.Total)
			}
		}
	}
	if !found {
		t.Fatal("calories nutrient not found in week average")
	}
}

func TestStore_CopyDay_AddsItemsToEachTarget(t *testing.T) {
	store, meals, q := newTestStores(t)
	userID := newTestUser(t, q, "a")
	source := day.MondayOf(time.Now())
	targetA := source.AddDate(0, 0, 1)
	targetB := source.AddDate(0, 0, 2)

	calID := nutrientIDByKey(t, meals, "calories")
	mealID, err := meals.Create(t.Context(), userID, mealslib.MealInput{
		Name: "Copied Meal", ServingLabel: "per serving", ServingAmount: 1,
		NutrientAmounts: map[int16]float64{calID: 400},
	})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categoryID := firstCategoryID(t, store, userID, source)
	if err := store.AddItem(t.Context(), userID, source, categoryID, mealID, 2); err != nil {
		t.Fatalf("add item to source: %v", err)
	}

	// targetA already has an unrelated item — copying must be additive, not
	// replace what's already there.
	otherMealID, err := meals.Create(t.Context(), userID, mealslib.MealInput{Name: "Pre-existing", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create other meal: %v", err)
	}
	if err := store.AddItem(t.Context(), userID, targetA, categoryID, otherMealID, 1); err != nil {
		t.Fatalf("add pre-existing item to target: %v", err)
	}

	if err := store.CopyDay(t.Context(), userID, source, []time.Time{targetA, targetB}); err != nil {
		t.Fatalf("copy day: %v", err)
	}

	dayA, err := store.GetDay(t.Context(), userID, targetA)
	if err != nil {
		t.Fatalf("get target A: %v", err)
	}
	var namesA []string
	for _, cat := range dayA.Categories {
		for _, item := range cat.Items {
			namesA = append(namesA, item.MealName)
		}
	}
	if len(namesA) != 2 {
		t.Fatalf("expected target A to have 2 items (pre-existing + copied), got %v", namesA)
	}

	dayB, err := store.GetDay(t.Context(), userID, targetB)
	if err != nil {
		t.Fatalf("get target B: %v", err)
	}
	var foundCopyInB bool
	var copiedQty float64
	for _, cat := range dayB.Categories {
		for _, item := range cat.Items {
			if item.MealName == "Copied Meal" {
				foundCopyInB = true
				copiedQty = item.Quantity
			}
		}
	}
	if !foundCopyInB {
		t.Fatalf("expected target B to have the copied item, got: %+v", dayB.Categories)
	}
	if copiedQty != 2 {
		t.Errorf("copied item quantity = %v, want 2 (preserved from source)", copiedQty)
	}
}

func TestStore_CopyDay_EmptySourceIsNoop(t *testing.T) {
	store, _, q := newTestStores(t)
	userID := newTestUser(t, q, "a")
	source := day.MondayOf(time.Now())
	target := source.AddDate(0, 0, 1)

	if err := store.CopyDay(t.Context(), userID, source, []time.Time{target}); err != nil {
		t.Fatalf("copy empty day: %v", err)
	}

	day, err := store.GetDay(t.Context(), userID, target)
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	if day.PlanID != uuid.Nil {
		t.Errorf("expected copying an empty day to leave the target untouched, but a plan now exists")
	}
}

// TestStore_CopyDay_DuplicateTargetDatesOnlyAddsOnce guards against a real
// bug found during review: CopyDay looped over targetDates without
// deduplicating, so a resubmitted form (or forged request) repeating the
// same target_date would insert every source item once per repetition —
// day_plan_items has no unique constraint to catch this at the DB layer.
func TestStore_CopyDay_DuplicateTargetDatesOnlyAddsOnce(t *testing.T) {
	store, meals, q := newTestStores(t)
	userID := newTestUser(t, q, "a")
	source := day.MondayOf(time.Now())
	target := source.AddDate(0, 0, 1)

	mealID, err := meals.Create(t.Context(), userID, mealslib.MealInput{Name: "Duplicate Target Meal", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categoryID := firstCategoryID(t, store, userID, source)
	if err := store.AddItem(t.Context(), userID, source, categoryID, mealID, 1); err != nil {
		t.Fatalf("add item to source: %v", err)
	}

	if err := store.CopyDay(t.Context(), userID, source, []time.Time{target, target, target}); err != nil {
		t.Fatalf("copy day: %v", err)
	}

	targetDay, err := store.GetDay(t.Context(), userID, target)
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	var count int
	for _, cat := range targetDay.Categories {
		for _, item := range cat.Items {
			if item.MealName == "Duplicate Target Meal" {
				count++
			}
		}
	}
	if count != 1 {
		t.Errorf("expected the item to be copied exactly once despite the repeated target date, got %d copies", count)
	}
}

// TestStore_CopyDay_SelfCopyIsExcluded guards against a real bug found
// during review: a target_date equal to source_date (impossible via the UI's
// checkboxes, but not validated server-side) would re-add every item on the
// source day onto itself, doubling it.
func TestStore_CopyDay_SelfCopyIsExcluded(t *testing.T) {
	store, meals, q := newTestStores(t)
	userID := newTestUser(t, q, "a")
	source := day.MondayOf(time.Now())

	mealID, err := meals.Create(t.Context(), userID, mealslib.MealInput{Name: "Self Copy Meal", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categoryID := firstCategoryID(t, store, userID, source)
	if err := store.AddItem(t.Context(), userID, source, categoryID, mealID, 1); err != nil {
		t.Fatalf("add item to source: %v", err)
	}

	if err := store.CopyDay(t.Context(), userID, source, []time.Time{source}); err != nil {
		t.Fatalf("copy day: %v", err)
	}

	sourceDay, err := store.GetDay(t.Context(), userID, source)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	var count int
	for _, cat := range sourceDay.Categories {
		for _, item := range cat.Items {
			if item.MealName == "Self Copy Meal" {
				count++
			}
		}
	}
	if count != 1 {
		t.Errorf("expected a self-targeting copy to be a no-op, got %d copies of the item", count)
	}
}

// TestStore_MutateNonexistentItem_ReturnsErrItemNotFound guards against a
// real bug found during review: a nonexistent item_id used to produce a
// generic wrapped error indistinguishable from ErrNotOwner or a genuine DB
// fault, which the HTTP layer then reported as a 500 instead of a 404.
func TestStore_MutateNonexistentItem_ReturnsErrItemNotFound(t *testing.T) {
	store, _, _ := newTestStores(t)
	fakeItemID := uuid.New()

	if err := store.UpdateItemQuantity(t.Context(), uuid.New(), fakeItemID, 1); !errors.Is(err, planning.ErrItemNotFound) {
		t.Errorf("UpdateItemQuantity: expected ErrItemNotFound, got %v", err)
	}
	if err := store.RemoveItem(t.Context(), uuid.New(), fakeItemID); !errors.Is(err, planning.ErrItemNotFound) {
		t.Errorf("RemoveItem: expected ErrItemNotFound, got %v", err)
	}
}

// TestStore_GetDay_MaterializesDefaultsOnFirstView is the core Default Day
// guarantee: a day the user has never touched picks up their configured
// defaults automatically, as real, editable items (not a virtual overlay).
func TestStore_GetDay_MaterializesDefaultsOnFirstView(t *testing.T) {
	store, meals, q := newTestStores(t)
	userID := newTestUser(t, q, "a")
	date := time.Now().AddDate(0, 0, 30) // an untouched future date

	mealID, err := meals.Create(t.Context(), userID, mealslib.MealInput{Name: "Default Oatmeal", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categoryID := firstCategoryID(t, store, userID, date)

	if err := store.AddDefaultItem(t.Context(), userID, categoryID, mealID, 2); err != nil {
		t.Fatalf("add default item: %v", err)
	}

	d, err := store.GetDay(t.Context(), userID, date)
	if err != nil {
		t.Fatalf("get day: %v", err)
	}
	if d.PlanID == uuid.Nil {
		t.Error("expected materializing defaults to create a real day plan (non-nil PlanID)")
	}
	var found bool
	for _, section := range d.Categories {
		for _, item := range section.Items {
			if item.MealName == "Default Oatmeal" {
				found = true
				if item.Quantity != 2 {
					t.Errorf("materialized item quantity = %v, want 2", item.Quantity)
				}
			}
		}
	}
	if !found {
		t.Error("expected the default item to be materialized into the day")
	}
}

// TestStore_GetDay_DefaultsNotReappliedAfterCleared guards the exact
// footgun a naive "always overlay defaults" implementation would hit:
// once a day is materialized, removing every item from it must NOT bring
// the defaults back on the next view — the day_plans row now existing is
// what distinguishes "never touched" from "touched, currently empty."
func TestStore_GetDay_DefaultsNotReappliedAfterCleared(t *testing.T) {
	store, meals, q := newTestStores(t)
	userID := newTestUser(t, q, "a")
	date := time.Now().AddDate(0, 0, 31)

	mealID, err := meals.Create(t.Context(), userID, mealslib.MealInput{Name: "Clearable Default", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categoryID := firstCategoryID(t, store, userID, date)
	if err := store.AddDefaultItem(t.Context(), userID, categoryID, mealID, 1); err != nil {
		t.Fatalf("add default item: %v", err)
	}

	// First view materializes the default.
	first, err := store.GetDay(t.Context(), userID, date)
	if err != nil {
		t.Fatalf("get day (first view): %v", err)
	}
	var itemID uuid.UUID
	for _, section := range first.Categories {
		for _, item := range section.Items {
			itemID = item.ID
		}
	}
	if itemID == uuid.Nil {
		t.Fatal("expected the default to have materialized on first view")
	}

	// Remove it, like a user who doesn't want the default today.
	if err := store.RemoveItem(t.Context(), userID, itemID); err != nil {
		t.Fatalf("remove item: %v", err)
	}

	// A second view must NOT bring the default back.
	second, err := store.GetDay(t.Context(), userID, date)
	if err != nil {
		t.Fatalf("get day (second view): %v", err)
	}
	for _, section := range second.Categories {
		if len(section.Items) != 0 {
			t.Errorf("expected the day to stay empty after clearing, got items in %q", section.Category.Name)
		}
	}
}

// TestStore_GetDay_NoDefaults_StaysPureRead confirms a user who hasn't
// configured any defaults sees the exact same behavior as before this
// feature existed — no day plan row is created just from viewing.
func TestStore_GetDay_NoDefaults_StaysPureRead(t *testing.T) {
	store, _, q := newTestStores(t)
	userID := newTestUser(t, q, "a")

	d, err := store.GetDay(t.Context(), userID, time.Now().AddDate(0, 0, 32))
	if err != nil {
		t.Fatalf("get day: %v", err)
	}
	if d.PlanID != uuid.Nil {
		t.Error("expected no day plan to be created when no defaults are configured")
	}
}

func TestStore_DefaultItem_AddListRemove(t *testing.T) {
	store, meals, q := newTestStores(t)
	userID := newTestUser(t, q, "a")

	mealID, err := meals.Create(t.Context(), userID, mealslib.MealInput{Name: "Snack Bar", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categoryID := firstCategoryID(t, store, userID, time.Now())

	if err := store.AddDefaultItem(t.Context(), userID, categoryID, mealID, 1); err != nil {
		t.Fatalf("add default item: %v", err)
	}

	sections, err := store.GetDefaultDay(t.Context(), userID)
	if err != nil {
		t.Fatalf("get default day: %v", err)
	}
	var itemID uuid.UUID
	for _, section := range sections {
		for _, item := range section.Items {
			if item.MealName == "Snack Bar" {
				itemID = item.ID
			}
		}
	}
	if itemID == uuid.Nil {
		t.Fatal("expected the added default item to appear in GetDefaultDay")
	}

	if err := store.RemoveDefaultItem(t.Context(), userID, itemID); err != nil {
		t.Fatalf("remove default item: %v", err)
	}

	sections, err = store.GetDefaultDay(t.Context(), userID)
	if err != nil {
		t.Fatalf("get default day after removal: %v", err)
	}
	for _, section := range sections {
		for _, item := range section.Items {
			if item.MealName == "Snack Bar" {
				t.Error("expected the default item to be gone after RemoveDefaultItem")
			}
		}
	}
}

// TestStore_RemoveDefaultItem_WrongUser_ReturnsErrItemNotFound guards the
// ownership boundary: one user must never be able to remove another's
// default item, even by guessing/reusing a valid item id.
func TestStore_RemoveDefaultItem_WrongUser_ReturnsErrItemNotFound(t *testing.T) {
	store, meals, q := newTestStores(t)
	owner := newTestUser(t, q, "owner")
	attacker := newTestUser(t, q, "attacker")

	mealID, err := meals.Create(t.Context(), owner, mealslib.MealInput{Name: "Owner's Default", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categoryID := firstCategoryID(t, store, owner, time.Now())
	if err := store.AddDefaultItem(t.Context(), owner, categoryID, mealID, 1); err != nil {
		t.Fatalf("add default item: %v", err)
	}

	sections, err := store.GetDefaultDay(t.Context(), owner)
	if err != nil {
		t.Fatalf("get default day: %v", err)
	}
	var itemID uuid.UUID
	for _, section := range sections {
		for _, item := range section.Items {
			itemID = item.ID
		}
	}
	if itemID == uuid.Nil {
		t.Fatal("expected to find the added default item")
	}

	if err := store.RemoveDefaultItem(t.Context(), attacker, itemID); !errors.Is(err, planning.ErrItemNotFound) {
		t.Errorf("expected ErrItemNotFound for a different user's default item, got %v", err)
	}
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

func assertNutrientTotal(t *testing.T, totals []planning.NutrientTotal, nutrientID int16, want float64) {
	t.Helper()
	for _, nt := range totals {
		if nt.NutrientID == nutrientID {
			if nt.Total != want {
				t.Errorf("%s total = %v, want %v", nt.DisplayName, nt.Total, want)
			}
			return
		}
	}
	t.Fatalf("nutrient id %d not found in totals", nutrientID)
}
