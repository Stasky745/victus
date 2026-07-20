package httpserver_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/dbtest"
	"github.com/Stasky745/victus/internal/mealslib"
)

func TestDefaults_AddSearchRemoveFlow(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)
	q, err := db.NewQuerier(dbtest.Driver(), pool)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}
	ctx := t.Context()

	meals, err := mealslib.New(pool, dbtest.Driver())
	if err != nil {
		t.Fatalf("new mealslib store: %v", err)
	}
	mealID, err := meals.Create(ctx, newMealAuthor(t, pool), mealslib.MealInput{Name: "Default Granola", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categories, err := q.ListMealCategories(ctx)
	if err != nil || len(categories) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	categoryID := categories[0].ID

	// --- Page loads and lists the category ---
	page := c.get("/defaults")
	if page.Code != http.StatusOK {
		t.Fatalf("defaults page status = %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), categories[0].Name) {
		t.Errorf("expected the defaults page to list category %q", categories[0].Name)
	}

	// --- Search finds the seeded meal ---
	search := c.get("/defaults/meal-search?category_id=" + categoryID.String() + "&q=Granola")
	if search.Code != http.StatusOK {
		t.Fatalf("search status = %d", search.Code)
	}
	if !strings.Contains(search.Body.String(), "Default Granola") {
		t.Errorf("expected search results to include the seeded meal, got: %s", search.Body.String())
	}

	// --- Add it ---
	token := c.csrfToken("/defaults")
	addRec := c.postFormHX("/defaults/items", url.Values{
		"meal_id": {mealID.String()}, "category_id": {categoryID.String()},
	}, token)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add default status = %d, body: %s", addRec.Code, addRec.Body.String())
	}
	if !strings.Contains(addRec.Body.String(), "Default Granola") {
		t.Errorf("expected the item list fragment to include the new default, got: %s", addRec.Body.String())
	}
	itemID := extractItemID(t, addRec.Body.String())

	// --- Remove it ---
	deleteRec := c.delete("/defaults/items/"+itemID, token)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("remove default status = %d", deleteRec.Code)
	}

	reload := c.get("/defaults")
	if strings.Contains(reload.Body.String(), "Default Granola") {
		t.Error("expected the removed default to no longer appear on the defaults page")
	}
}

// TestDefaults_MaterializeOnDayView is the end-to-end version of the
// planning-layer materialization tests: a default set via the HTTP API
// must actually show up the first time an untouched day is viewed.
func TestDefaults_MaterializeOnDayView(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)
	q, err := db.NewQuerier(dbtest.Driver(), pool)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}
	ctx := t.Context()

	meals, err := mealslib.New(pool, dbtest.Driver())
	if err != nil {
		t.Fatalf("new mealslib store: %v", err)
	}
	mealID, err := meals.Create(ctx, newMealAuthor(t, pool), mealslib.MealInput{Name: "HTTP Default Meal", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categories, err := q.ListMealCategories(ctx)
	if err != nil || len(categories) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	categoryID := categories[0].ID

	token := c.csrfToken("/defaults")
	addRec := c.postFormHX("/defaults/items", url.Values{
		"meal_id": {mealID.String()}, "category_id": {categoryID.String()},
	}, token)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add default status = %d", addRec.Code)
	}

	futureDate := time.Now().AddDate(0, 0, 60).Format("2006-01-02")
	dayPage := c.get("/days/" + futureDate)
	if dayPage.Code != http.StatusOK {
		t.Fatalf("day page status = %d", dayPage.Code)
	}
	if !strings.Contains(dayPage.Body.String(), "HTTP Default Meal") {
		t.Errorf("expected an untouched day to show the configured default, got: %s", dayPage.Body.String())
	}
}

// TestDefaultsRemoveItem_RejectsOtherUsersItem mirrors
// TestDay_UpdateItem_RejectsOtherUsersItem for the Default Day equivalent.
func TestDefaultsRemoveItem_RejectsOtherUsersItem(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	owner := newAuthenticatedClient(t, pool, srv)
	attacker := newAuthenticatedClient(t, pool, srv)
	q, err := db.NewQuerier(dbtest.Driver(), pool)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}
	ctx := t.Context()

	meals, err := mealslib.New(pool, dbtest.Driver())
	if err != nil {
		t.Fatalf("new mealslib store: %v", err)
	}
	mealID, err := meals.Create(ctx, newMealAuthor(t, pool), mealslib.MealInput{Name: "Owner's Default", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categories, err := q.ListMealCategories(ctx)
	if err != nil || len(categories) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	categoryID := categories[0].ID

	ownerToken := owner.csrfToken("/defaults")
	addRec := owner.postFormHX("/defaults/items", url.Values{
		"meal_id": {mealID.String()}, "category_id": {categoryID.String()},
	}, ownerToken)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add default status = %d", addRec.Code)
	}
	itemID := extractItemID(t, addRec.Body.String())

	attackerToken := attacker.csrfToken("/defaults")
	deleteRec := attacker.delete("/defaults/items/"+itemID, attackerToken)
	if deleteRec.Code != http.StatusNotFound {
		t.Errorf("expected a different user's delete attempt to be rejected with 404, got %d", deleteRec.Code)
	}
}
