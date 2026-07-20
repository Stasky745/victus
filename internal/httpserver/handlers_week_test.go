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
	"github.com/Stasky745/victus/web/templates/day"
)

func TestWeek_Today_RedirectsToMonday(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	rec := c.get("/weeks")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	wantPrefix := "/weeks/" + day.MondayOf(time.Now()).Format("2006-01-02")
	if loc := rec.Header().Get("Location"); loc != wantPrefix {
		t.Errorf("Location = %q, want %q", loc, wantPrefix)
	}
}

func TestWeek_NonMondayURL_RedirectsToCanonicalMonday(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	monday := day.MondayOf(time.Now())
	wednesday := monday.AddDate(0, 0, 2)

	rec := c.get("/weeks/" + wednesday.Format("2006-01-02"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	want := "/weeks/" + monday.Format("2006-01-02")
	if loc := rec.Header().Get("Location"); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}
}

func TestWeek_PageShowsAllSevenDays(t *testing.T) {
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
	mealID, err := meals.Create(ctx, newMealAuthor(t, pool), mealslib.MealInput{Name: "Week Meal", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categories, err := q.ListMealCategories(ctx)
	if err != nil || len(categories) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	categoryID := categories[0].ID
	monday := day.MondayOf(time.Now())

	token := c.csrfToken("/days/" + monday.Format("2006-01-02"))
	addRec := c.postFormHX("/days/"+monday.Format("2006-01-02")+"/items", url.Values{
		"meal_id": {mealID.String()}, "category_id": {categoryID.String()},
	}, token)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add item status = %d", addRec.Code)
	}

	weekPage := c.get("/weeks/" + monday.Format("2006-01-02"))
	if weekPage.Code != http.StatusOK {
		t.Fatalf("week page status = %d", weekPage.Code)
	}
	body := weekPage.Body.String()
	if !strings.Contains(body, "Week Meal") {
		t.Errorf("expected the week page to show the added meal, got: %s", body)
	}
	if !strings.Contains(body, "Week average") {
		t.Errorf("expected a week-average summary section, got: %s", body)
	}
	// All 7 weekday abbreviations should appear as day-column headers.
	for _, wd := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
		if !strings.Contains(body, wd) {
			t.Errorf("expected weekday %q to appear on the week page", wd)
		}
	}
}

func TestWeek_EmptyDaysHaveNoCopyControl(t *testing.T) {
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
	mealID, err := meals.Create(ctx, newMealAuthor(t, pool), mealslib.MealInput{Name: "Only Monday Meal", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categories, err := q.ListMealCategories(ctx)
	if err != nil || len(categories) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	categoryID := categories[0].ID
	monday := day.MondayOf(time.Now())

	token := c.csrfToken("/days/" + monday.Format("2006-01-02"))
	addRec := c.postFormHX("/days/"+monday.Format("2006-01-02")+"/items", url.Values{
		"meal_id": {mealID.String()}, "category_id": {categoryID.String()},
	}, token)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add item status = %d", addRec.Code)
	}

	weekPage := c.get("/weeks/" + monday.Format("2006-01-02"))
	if weekPage.Code != http.StatusOK {
		t.Fatalf("week page status = %d", weekPage.Code)
	}
	body := weekPage.Body.String()
	// Only Monday has items, so exactly one of the 7 day columns should
	// render the "Copy to…" control.
	if got := strings.Count(body, "Copy to"); got != 1 {
		t.Errorf("expected exactly 1 \"Copy to…\" control (only Monday has items), got %d", got)
	}
}

func TestWeek_CopyDay_AddsItemsToSelectedTargets(t *testing.T) {
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
	mealID, err := meals.Create(ctx, newMealAuthor(t, pool), mealslib.MealInput{Name: "Copyable Meal", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categories, err := q.ListMealCategories(ctx)
	if err != nil || len(categories) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	categoryID := categories[0].ID
	monday := day.MondayOf(time.Now())
	tuesday := monday.AddDate(0, 0, 1)

	dayToken := c.csrfToken("/days/" + monday.Format("2006-01-02"))
	addRec := c.postFormHX("/days/"+monday.Format("2006-01-02")+"/items", url.Values{
		"meal_id": {mealID.String()}, "category_id": {categoryID.String()},
	}, dayToken)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add item status = %d", addRec.Code)
	}

	weekToken := c.csrfToken("/weeks/" + monday.Format("2006-01-02"))
	copyRec := c.postForm("/weeks/"+monday.Format("2006-01-02")+"/copy", url.Values{
		"source_date":  {monday.Format("2006-01-02")},
		"target_dates": {tuesday.Format("2006-01-02")},
	}, weekToken)
	if copyRec.Code != http.StatusSeeOther {
		t.Fatalf("copy status = %d, body: %s", copyRec.Code, copyRec.Body.String())
	}

	tuesdayPage := c.get("/days/" + tuesday.Format("2006-01-02"))
	if !strings.Contains(tuesdayPage.Body.String(), "Copyable Meal") {
		t.Errorf("expected the copied meal to appear on the target day, got: %s", tuesdayPage.Body.String())
	}
}

// TestWeek_AddMeal_PlainFormRedirectsBackToWeek guards the Week Builder's
// "add meal" flow specifically: unlike the Day Builder's htmx "Add" button,
// the week day columns submit a plain (non-htmx) form to the same POST
// /days/{date}/items endpoint — handleDayAddItem must redirect back to the
// week page for this caller instead of rendering a Day Builder htmx
// fragment, which the week page has nowhere to put.
func TestWeek_AddMeal_PlainFormRedirectsBackToWeek(t *testing.T) {
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
	mealID, err := meals.Create(ctx, newMealAuthor(t, pool), mealslib.MealInput{Name: "Plain Form Meal", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categories, err := q.ListMealCategories(ctx)
	if err != nil || len(categories) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	categoryID := categories[0].ID
	monday := day.MondayOf(time.Now())
	dateStr := monday.Format("2006-01-02")

	token := c.csrfToken("/weeks/" + dateStr)
	// Deliberately postForm, not postFormHX — this is the exact request
	// shape the week page's plain <form> Add button sends.
	addRec := c.postForm("/days/"+dateStr+"/items", url.Values{
		"meal_id": {mealID.String()}, "category_id": {categoryID.String()},
	}, token)
	if addRec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d, body: %s", addRec.Code, http.StatusSeeOther, addRec.Body.String())
	}
	if loc := addRec.Header().Get("Location"); loc != "/weeks/"+dateStr {
		t.Errorf("Location = %q, want %q", loc, "/weeks/"+dateStr)
	}

	weekPage := c.get("/weeks/" + dateStr)
	if !strings.Contains(weekPage.Body.String(), "Plain Form Meal") {
		t.Errorf("expected the week page to show the added meal after redirect, got: %s", weekPage.Body.String())
	}
}

// TestWeek_RemoveItem_ViaHTMXDeleteButton guards against a real regression:
// the row-based redesign initially rendered each day's items as a single
// joined text line with no way to remove one, which was a hard functional
// gap (not just a cosmetic issue) — you could add to a week day but never
// take anything back off there. The remove control posts straight to the
// same DELETE /days/{date}/items/{item_id} endpoint the Day Builder uses;
// the CSRF token rides along via layout.Base's global hx-headers, not a
// per-button field, so this only proves it end to end with a real htmx
// (X-CSRF-Token header) request.
func TestWeek_RemoveItem_ViaHTMXDeleteButton(t *testing.T) {
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
	mealID, err := meals.Create(ctx, newMealAuthor(t, pool), mealslib.MealInput{Name: "Removable Meal", ServingLabel: "per serving", ServingAmount: 1})
	if err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categories, err := q.ListMealCategories(ctx)
	if err != nil || len(categories) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	categoryID := categories[0].ID
	monday := day.MondayOf(time.Now())
	dateStr := monday.Format("2006-01-02")

	token := c.csrfToken("/weeks/" + dateStr)
	addRec := c.postForm("/days/"+dateStr+"/items", url.Values{
		"meal_id": {mealID.String()}, "category_id": {categoryID.String()},
	}, token)
	if addRec.Code != http.StatusSeeOther {
		t.Fatalf("add: status = %d", addRec.Code)
	}

	weekPage := c.get("/weeks/" + dateStr)
	body := weekPage.Body.String()
	if !strings.Contains(body, "Removable Meal") {
		t.Fatalf("expected the added meal to appear on the week page, got: %s", body)
	}

	const marker = "/items/"
	idx := strings.Index(body, marker)
	if idx == -1 {
		t.Fatalf("expected a remove control's /items/{id} reference in the week page, got: %s", body)
	}
	rest := body[idx+len(marker):]
	end := strings.IndexAny(rest, `"'`)
	if end == -1 {
		t.Fatalf("malformed item link in week page body: %s", body)
	}
	itemID := rest[:end]

	deleteRec := c.delete("/days/"+dateStr+"/items/"+itemID, token)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, body: %s", deleteRec.Code, deleteRec.Body.String())
	}

	afterDelete := c.get("/weeks/" + dateStr)
	if strings.Contains(afterDelete.Body.String(), "Removable Meal") {
		t.Error("expected the removed meal to no longer appear on the week page")
	}
}

func TestWeek_MealSearch_FindsSeededMeal(t *testing.T) {
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
	if _, err := meals.Create(ctx, newMealAuthor(t, pool), mealslib.MealInput{Name: "Week Search Waffles", ServingLabel: "per serving", ServingAmount: 1}); err != nil {
		t.Fatalf("create meal: %v", err)
	}
	categories, err := q.ListMealCategories(ctx)
	if err != nil || len(categories) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	categoryID := categories[0].ID
	monday := day.MondayOf(time.Now())
	dateStr := monday.Format("2006-01-02")

	search := c.get("/weeks/" + dateStr + "/days/" + dateStr + "/meal-search?category_id=" + categoryID.String() + "&q=Waffles")
	if search.Code != http.StatusOK {
		t.Fatalf("search status = %d", search.Code)
	}
	body := search.Body.String()
	if !strings.Contains(body, "Week Search Waffles") {
		t.Errorf("expected search results to include the seeded meal, got: %s", body)
	}
	// The result's Add control must be a plain form posting to the day-item
	// endpoint (not an htmx button) — the week page has no htmx-target
	// scaffolding to swap into.
	if !strings.Contains(body, `action="/days/`+dateStr+`/items"`) {
		t.Errorf("expected each result's Add form to post to /days/%s/items, got: %s", dateStr, body)
	}
}

func TestWeek_CopyDay_RequiresAtLeastOneTarget(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)
	monday := day.MondayOf(time.Now())

	token := c.csrfToken("/weeks/" + monday.Format("2006-01-02"))
	rec := c.postForm("/weeks/"+monday.Format("2006-01-02")+"/copy", url.Values{
		"source_date": {monday.Format("2006-01-02")},
	}, token)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
