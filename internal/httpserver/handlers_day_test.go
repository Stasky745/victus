package httpserver_test

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/dbtest"
	"github.com/Stasky745/victus/internal/mealslib"
)

// newMealAuthor creates a throwaway user to satisfy meals.created_by's
// foreign key — these tests don't care who authored the seeded meal, only
// that day-plan ownership (a different check) works correctly.
func newMealAuthor(t *testing.T, pool *sql.DB) uuid.UUID {
	t.Helper()
	q, err := db.NewQuerier(dbtest.Driver(), pool)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}
	suffix := uuid.NewString()
	user, err := q.CreateUser(t.Context(), sqlc.CreateUserParams{
		ID:          uuid.New(),
		OidcSubject: sql.NullString{String: "meal-author-" + suffix, Valid: true},
		Email:       "author-" + suffix + "@example.com",
	})
	if err != nil {
		t.Fatalf("create meal author: %v", err)
	}
	return user.ID
}

func TestDay_Today_RedirectsToDateURL(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	rec := c.get("/")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/days/"+time.Now().Format("2006-01-02")) {
		t.Errorf("Location = %q, want today's /days/{date}", loc)
	}
}

func TestDay_AddSearchUpdateRemoveFlow(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)
	q, err := db.NewQuerier(dbtest.Driver(), pool)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}
	ctx := t.Context()

	// Seed a meal directly through the business logic layer — the HTTP
	// meal-creation flow is already covered by handlers_meals_test.go.
	meals, err := mealslib.New(pool, dbtest.Driver())
	if err != nil {
		t.Fatalf("new mealslib store: %v", err)
	}
	nutrients, err := meals.ListNutrients(ctx)
	if err != nil {
		t.Fatalf("list nutrients: %v", err)
	}
	var caloriesID int16
	for _, n := range nutrients {
		if n.Key == "calories" {
			caloriesID = n.ID
		}
	}
	mealID, err := meals.Create(ctx, newMealAuthor(t, pool), mealslib.MealInput{
		Name: "Test Oatmeal", ServingLabel: "per serving", ServingAmount: 1,
		NutrientAmounts: map[int16]float64{caloriesID: 300},
	})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}

	categories, err := q.ListMealCategories(ctx)
	if err != nil || len(categories) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	categoryID := categories[0].ID

	dateStr := time.Now().Format("2006-01-02")

	// --- Day page loads and shows the category ---
	page := c.get("/days/" + dateStr)
	if page.Code != http.StatusOK {
		t.Fatalf("day page status = %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), categories[0].Name) {
		t.Errorf("expected the day page to list category %q", categories[0].Name)
	}

	// --- Meal search finds the seeded meal ---
	search := c.get("/days/" + dateStr + "/meal-search?category_id=" + categoryID.String() + "&q=Oatmeal")
	if search.Code != http.StatusOK {
		t.Fatalf("search status = %d", search.Code)
	}
	if !strings.Contains(search.Body.String(), "Test Oatmeal") {
		t.Errorf("expected search results to include the seeded meal, got: %s", search.Body.String())
	}

	// --- Add the item ---
	token := c.csrfToken("/days/" + dateStr)
	addRec := c.postFormHX("/days/"+dateStr+"/items", url.Values{
		"meal_id":     {mealID.String()},
		"category_id": {categoryID.String()},
		"quantity":    {"2"},
	}, token)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add item status = %d, body: %s", addRec.Code, addRec.Body.String())
	}
	if !strings.Contains(addRec.Body.String(), "Test Oatmeal") {
		t.Errorf("expected the item list fragment to include the new item, got: %s", addRec.Body.String())
	}
	// 300 kcal * 2 = 600, shown via the OOB summary in the same response.
	if !strings.Contains(addRec.Body.String(), "600.0") {
		t.Errorf("expected the OOB summary to show 600 total calories, got: %s", addRec.Body.String())
	}

	// Extract the new item's id from the response fragment's hx-delete attribute.
	itemID := extractItemID(t, addRec.Body.String())

	// --- Update quantity ---
	patchRec := c.do(http.MethodPatch, "/days/"+dateStr+"/items/"+itemID, strings.NewReader(url.Values{
		"quantity":           {"3"},
		"gorilla.csrf.Token": {token},
	}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if patchRec.Code != http.StatusOK {
		t.Fatalf("update quantity status = %d, body: %s", patchRec.Code, patchRec.Body.String())
	}
	if !strings.Contains(patchRec.Body.String(), "900.0") {
		t.Errorf("expected the OOB summary to show 900 total calories after quantity update, got: %s", patchRec.Body.String())
	}

	// --- Remove the item ---
	deleteRec := c.delete("/days/"+dateStr+"/items/"+itemID, token)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("remove item status = %d, body: %s", deleteRec.Code, deleteRec.Body.String())
	}
	if !strings.Contains(deleteRec.Body.String(), "0.0") {
		t.Errorf("expected the OOB summary to show 0 total calories after removal, got: %s", deleteRec.Body.String())
	}
}

// TestDay_AddItem_ClearsSearchResultsDropdown guards against a real bug
// found during review: the response to a successful add never touched the
// category's search-results dropdown, so the just-added meal's "Add" button
// stayed visible and clickable — a second click (double-click, slow network)
// would insert a duplicate item, since AddItem has no dedup guard.
func TestDay_AddItem_ClearsSearchResultsDropdown(t *testing.T) {
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
	mealID, err := meals.Create(ctx, newMealAuthor(t, pool), mealslib.MealInput{Name: "Clearable Meal", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categories, err := q.ListMealCategories(ctx)
	if err != nil || len(categories) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	categoryID := categories[0].ID
	dateStr := time.Now().Format("2006-01-02")

	token := c.csrfToken("/days/" + dateStr)
	addRec := c.postFormHX("/days/"+dateStr+"/items", url.Values{
		"meal_id": {mealID.String()}, "category_id": {categoryID.String()},
	}, token)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add item status = %d, body: %s", addRec.Code, addRec.Body.String())
	}

	wantID := "search-results-" + categoryID.String()
	body := addRec.Body.String()
	if !strings.Contains(body, `id="`+wantID+`"`) || !strings.Contains(body, `hx-swap-oob="true"`) {
		t.Errorf("expected an OOB fragment clearing #%s in the add response, got: %s", wantID, body)
	}
}

func TestDay_UpdateItem_RejectsOtherUsersItem(t *testing.T) {
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
	mealID, err := meals.Create(ctx, newMealAuthor(t, pool), mealslib.MealInput{Name: "Owner's Meal", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categories, err := q.ListMealCategories(ctx)
	if err != nil || len(categories) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	categoryID := categories[0].ID
	dateStr := time.Now().Format("2006-01-02")

	ownerToken := owner.csrfToken("/days/" + dateStr)
	addRec := owner.postFormHX("/days/"+dateStr+"/items", url.Values{
		"meal_id": {mealID.String()}, "category_id": {categoryID.String()},
	}, ownerToken)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add item status = %d", addRec.Code)
	}
	itemID := extractItemID(t, addRec.Body.String())

	attackerToken := attacker.csrfToken("/days/" + dateStr)
	deleteRec := attacker.delete("/days/"+dateStr+"/items/"+itemID, attackerToken)
	if deleteRec.Code != http.StatusNotFound {
		t.Errorf("expected a different user's delete attempt to be rejected with 404, got %d", deleteRec.Code)
	}
}

// TestDay_MutateNonexistentItem_Returns404 guards against a real bug found
// during review: a syntactically-valid item_id that simply doesn't exist
// (never existed, or already removed — e.g. a double-clicked Remove button)
// used to fall through to a generic 500 instead of a clean 404.
func TestDay_MutateNonexistentItem_Returns404(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)
	dateStr := time.Now().Format("2006-01-02")
	fakeItemID := uuid.NewString()

	token := c.csrfToken("/days/" + dateStr)

	patchRec := c.do(http.MethodPatch, "/days/"+dateStr+"/items/"+fakeItemID, strings.NewReader(url.Values{
		"quantity":           {"1"},
		"gorilla.csrf.Token": {token},
	}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if patchRec.Code != http.StatusNotFound {
		t.Errorf("PATCH on a nonexistent item: status = %d, want %d", patchRec.Code, http.StatusNotFound)
	}

	deleteRec := c.delete("/days/"+dateStr+"/items/"+fakeItemID, token)
	if deleteRec.Code != http.StatusNotFound {
		t.Errorf("DELETE on a nonexistent item: status = %d, want %d", deleteRec.Code, http.StatusNotFound)
	}
}

// TestDay_UpdateItem_RejectsZeroQuantity guards against a real bug found
// during review: quantity=0 used to be accepted, creating a dead row that
// contributes nothing to totals instead of the user just using Remove.
func TestDay_UpdateItem_RejectsZeroQuantity(t *testing.T) {
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
	mealID, err := meals.Create(ctx, newMealAuthor(t, pool), mealslib.MealInput{Name: "Toast", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categories, err := q.ListMealCategories(ctx)
	if err != nil || len(categories) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	categoryID := categories[0].ID
	dateStr := time.Now().Format("2006-01-02")

	token := c.csrfToken("/days/" + dateStr)
	addRec := c.postFormHX("/days/"+dateStr+"/items", url.Values{
		"meal_id": {mealID.String()}, "category_id": {categoryID.String()},
	}, token)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add item status = %d", addRec.Code)
	}
	itemID := extractItemID(t, addRec.Body.String())

	patchRec := c.do(http.MethodPatch, "/days/"+dateStr+"/items/"+itemID, strings.NewReader(url.Values{
		"quantity":           {"0"},
		"gorilla.csrf.Token": {token},
	}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if patchRec.Code != http.StatusBadRequest {
		t.Errorf("quantity=0: status = %d, want %d", patchRec.Code, http.StatusBadRequest)
	}
}

// TestDay_AddItem_RejectsNonexistentCategoryOrMeal guards against a real
// bug found during review: a well-formed but nonexistent category_id or
// meal_id relied entirely on a Postgres foreign-key violation, which
// surfaced as a 500 (and an ERROR-level log entry) instead of a routine
// 400 for bad client input.
func TestDay_AddItem_RejectsNonexistentCategoryOrMeal(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)
	q, err := db.NewQuerier(dbtest.Driver(), pool)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}
	ctx := t.Context()
	dateStr := time.Now().Format("2006-01-02")

	meals, err := mealslib.New(pool, dbtest.Driver())
	if err != nil {
		t.Fatalf("new mealslib store: %v", err)
	}
	mealID, err := meals.Create(ctx, newMealAuthor(t, pool), mealslib.MealInput{Name: "Real Meal", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categories, err := q.ListMealCategories(ctx)
	if err != nil || len(categories) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	realCategoryID := categories[0].ID

	token := c.csrfToken("/days/" + dateStr)

	badCategoryRec := c.postFormHX("/days/"+dateStr+"/items", url.Values{
		"meal_id": {mealID.String()}, "category_id": {uuid.NewString()},
	}, token)
	if badCategoryRec.Code != http.StatusBadRequest {
		t.Errorf("nonexistent category_id: status = %d, want %d, body: %s", badCategoryRec.Code, http.StatusBadRequest, badCategoryRec.Body.String())
	}

	badMealRec := c.postFormHX("/days/"+dateStr+"/items", url.Values{
		"meal_id": {uuid.NewString()}, "category_id": {realCategoryID.String()},
	}, token)
	if badMealRec.Code != http.StatusBadRequest {
		t.Errorf("nonexistent meal_id: status = %d, want %d, body: %s", badMealRec.Code, http.StatusBadRequest, badMealRec.Body.String())
	}
}

// TestDay_ReloadAfterAdd_ShowsItem guards against the full-page render path
// (day.Page) and the htmx-fragment render path (day.CategoryItems)
// diverging — everything else in this file only ever checks the response
// to the mutating request itself, never an independent subsequent GET.
func TestDay_ReloadAfterAdd_ShowsItem(t *testing.T) {
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
	mealID, err := meals.Create(ctx, newMealAuthor(t, pool), mealslib.MealInput{Name: "Persisted Pancakes", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categories, err := q.ListMealCategories(ctx)
	if err != nil || len(categories) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	categoryID := categories[0].ID
	dateStr := time.Now().Format("2006-01-02")

	token := c.csrfToken("/days/" + dateStr)
	addRec := c.postFormHX("/days/"+dateStr+"/items", url.Values{
		"meal_id": {mealID.String()}, "category_id": {categoryID.String()},
	}, token)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add item status = %d", addRec.Code)
	}

	reload := c.get("/days/" + dateStr)
	if reload.Code != http.StatusOK {
		t.Fatalf("reload status = %d", reload.Code)
	}
	if !strings.Contains(reload.Body.String(), "Persisted Pancakes") {
		t.Errorf("expected a fresh GET to show the previously-added item, got: %s", reload.Body.String())
	}
}

// extractItemID pulls a day-plan item's UUID out of its hx-delete attribute
// in a rendered category-items fragment.
func extractItemID(t *testing.T, body string) string {
	t.Helper()
	const marker = "/items/"
	idx := strings.Index(body, marker)
	if idx == -1 {
		t.Fatalf("no /items/ reference found in body: %s", body)
	}
	rest := body[idx+len(marker):]
	end := strings.IndexAny(rest, `"'`)
	if end == -1 {
		t.Fatalf("malformed item link in body: %s", body)
	}
	return rest[:end]
}
