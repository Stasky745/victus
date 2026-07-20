package httpserver_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMeals_CreateEditDeleteFlow(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	// --- Create ---
	token := c.csrfToken("/meals/new")
	rec := c.postForm("/meals", url.Values{
		"name":           {"Bolognese"},
		"recipe_url":     {"https://example.com/recipes/bolognese"},
		"serving_label":  {"per serving"},
		"serving_amount": {"1"},
		"nutrient_1":     {"510"}, // calories is nutrient id 1 (seeded)
	}, token)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// --- List shows it ---
	list := c.get("/meals")
	if !strings.Contains(list.Body.String(), "Bolognese") {
		t.Error("expected the new meal to appear in the list")
	}
	if !strings.Contains(list.Body.String(), "Recipe") {
		t.Error("expected a recipe link for a meal with recipe_url set")
	}

	// --- Search finds it ---
	search := c.get("/meals/search?q=bolognse") // deliberate typo
	if !strings.Contains(search.Body.String(), "Bolognese") {
		t.Errorf("expected fuzzy search to find Bolognese, got: %s", search.Body.String())
	}

	// Extract the meal's id from the edit link in the list page.
	id := extractMealID(t, list.Body.String(), "Bolognese")

	// --- Edit form pre-fills ---
	editForm := c.get("/meals/" + id + "/edit")
	if !strings.Contains(editForm.Body.String(), `value="Bolognese"`) {
		t.Error("expected edit form to be pre-filled with the meal's name")
	}

	// --- Update ---
	editToken := c.csrfToken("/meals/" + id + "/edit")
	updateRec := c.postForm("/meals/"+id, url.Values{
		"name":           {"Bolognese (updated)"},
		"serving_label":  {"per serving"},
		"serving_amount": {"1"},
	}, editToken)
	if updateRec.Code != http.StatusSeeOther {
		t.Fatalf("update: status = %d, body = %s", updateRec.Code, updateRec.Body.String())
	}

	afterUpdate := c.get("/meals")
	if !strings.Contains(afterUpdate.Body.String(), "Bolognese (updated)") {
		t.Error("expected the updated name to appear in the list")
	}

	// --- Delete ---
	deleteRec := c.delete("/meals/"+id, editToken)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, body = %s", deleteRec.Code, deleteRec.Body.String())
	}

	afterDelete := c.get("/meals")
	if strings.Contains(afterDelete.Body.String(), "Bolognese") {
		t.Error("expected the deleted meal to no longer appear in the list")
	}
}

func TestMeals_Create_RequiresName(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/meals/new")
	rec := c.postForm("/meals", url.Values{
		"serving_label":  {"per serving"},
		"serving_amount": {"1"},
	}, token)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if !strings.Contains(rec.Body.String(), "name is required") {
		t.Errorf("expected a validation error mentioning the missing name, got: %s", rec.Body.String())
	}
}

func TestMeals_Create_RejectsNonFiniteNumbers(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	cases := map[string]url.Values{
		"NaN serving amount": {
			"name": {"Test Meal"}, "serving_label": {"per serving"}, "serving_amount": {"NaN"},
		},
		"Inf serving amount": {
			"name": {"Test Meal"}, "serving_label": {"per serving"}, "serving_amount": {"Inf"},
		},
		"NaN nutrient value": {
			"name": {"Test Meal"}, "serving_label": {"per serving"}, "serving_amount": {"1"}, "nutrient_1": {"NaN"},
		},
	}
	for name, form := range cases {
		t.Run(name, func(t *testing.T) {
			token := c.csrfToken("/meals/new")
			rec := c.postForm("/meals", form, token)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want %d — NaN/Inf must not silently pass numeric validation", rec.Code, http.StatusUnprocessableEntity)
			}
		})
	}
}

// TestMeals_Create_PreservesValidNutrientsOnPartialError guards against a
// real bug found during review: a single invalid field (e.g. one bad
// nutrient value) used to wipe every other nutrient value the user had
// already correctly entered, forcing a full retype.
func TestMeals_Create_PreservesValidNutrientsOnPartialError(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/meals/new")
	rec := c.postForm("/meals", url.Values{
		"name":           {"Chicken & Rice"},
		"serving_label":  {"per serving"},
		"serving_amount": {"1"},
		"nutrient_1":     {"650"}, // calories — valid
		"nutrient_2":     {"-5"},  // protein — invalid (negative), should trigger a 422
	}, token)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `value="650"`) {
		t.Errorf("expected the valid calories value (650) to survive the re-render, got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `value="Chicken &amp; Rice"`) {
		t.Errorf("expected the name to survive the re-render, got: %s", rec.Body.String())
	}
}

func TestMeals_Create_RejectsNonMutatingCSRFBypass(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	// A POST with no CSRF token at all, even from an authenticated session,
	// must be rejected.
	rec := c.do(http.MethodPost, "/meals", strings.NewReader(url.Values{
		"name":           {"Should Not Be Created"},
		"serving_label":  {"per serving"},
		"serving_amount": {"1"},
	}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (CSRF rejection)", rec.Code, http.StatusForbidden)
	}
}

// TestMeals_Update_NonexistentMealReturns404 guards against a real bug
// found during review: handleMealUpdate treated a not-found meal (e.g. one
// deleted by another admin between opening and submitting the edit form)
// identically to any other DB failure, returning a misleading 422 "please
// try again" that would never succeed no matter how many times it's retried.
func TestMeals_Update_NonexistentMealReturns404(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/meals/new")
	rec := c.postForm("/meals/"+uuid.New().String(), url.Values{
		"name":           {"Ghost Meal"},
		"serving_label":  {"per serving"},
		"serving_amount": {"1"},
	}, token)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d, body: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// TestCategories_Rename_NonexistentCategoryReturns404 guards against the
// same class of bug as TestMeals_Update_NonexistentMealReturns404, for
// handleCategoryRename.
func TestCategories_Rename_NonexistentCategoryReturns404(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/meals/categories")
	rec := c.postForm("/meals/categories/"+uuid.New().String(), url.Values{"name": {"Ghost Category"}}, token)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d, body: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// TestCategories_Create_RejectsDuplicateName guards against a real bug
// found during review: meal_categories.name has a UNIQUE constraint, but
// handleCategoryCreate never discriminated that from a generic server
// error — a duplicate name was logged at ERROR level and shown as
// "please try again," which would keep failing no matter how many times
// the user retried with the same name.
func TestCategories_Create_RejectsDuplicateName(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/meals/categories")
	rec := c.postForm("/meals/categories", url.Values{"name": {"Breakfast"}}, token) // seeded default category
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("expected a clear duplicate-name message, got: %s", rec.Body.String())
	}
}

// TestCategories_Rename_RejectsDuplicateName guards against the same class
// of bug as TestCategories_Create_RejectsDuplicateName, for
// handleCategoryRename.
func TestCategories_Rename_RejectsDuplicateName(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/meals/categories")
	createRec := c.postForm("/meals/categories", url.Values{"name": {"Second Breakfast"}}, token)
	if createRec.Code != http.StatusSeeOther {
		t.Fatalf("create category: status = %d, body = %s", createRec.Code, createRec.Body.String())
	}
	list := c.get("/meals/categories")
	id := extractCategoryID(t, list.Body.String(), "Second Breakfast")

	renameToken := c.csrfToken("/meals/categories")
	rec := c.postForm("/meals/categories/"+id, url.Values{"name": {"Breakfast"}}, renameToken) // seeded default category
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("expected a clear duplicate-name message, got: %s", rec.Body.String())
	}
}

func TestCategories_CreateRenameDelete(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/meals/categories")
	createRec := c.postForm("/meals/categories", url.Values{"name": {"Second Breakfast"}}, token)
	if createRec.Code != http.StatusSeeOther {
		t.Fatalf("create category: status = %d, body = %s", createRec.Code, createRec.Body.String())
	}

	list := c.get("/meals/categories")
	if !strings.Contains(list.Body.String(), "Second Breakfast") {
		t.Error("expected new category to appear in the list")
	}

	id := extractCategoryID(t, list.Body.String(), "Second Breakfast")

	renameToken := c.csrfToken("/meals/categories")
	renameRec := c.postForm("/meals/categories/"+id, url.Values{"name": {"Elevenses"}}, renameToken)
	if renameRec.Code != http.StatusSeeOther {
		t.Fatalf("rename category: status = %d, body = %s", renameRec.Code, renameRec.Body.String())
	}

	afterRename := c.get("/meals/categories")
	if !strings.Contains(afterRename.Body.String(), "Elevenses") {
		t.Error("expected renamed category to appear in the list")
	}

	deleteToken := c.csrfToken("/meals/categories")
	deleteRec := c.postForm("/meals/categories/"+id+"/delete", url.Values{}, deleteToken)
	if deleteRec.Code != http.StatusSeeOther {
		t.Fatalf("delete category: status = %d, body = %s", deleteRec.Code, deleteRec.Body.String())
	}

	afterDelete := c.get("/meals/categories")
	if strings.Contains(afterDelete.Body.String(), "Elevenses") {
		t.Error("expected deleted category to no longer appear in the list")
	}
}

// extractMealID pulls a meal's UUID out of its edit link
// (href="/meals/<uuid>/edit") on a rendered list page, by locating the <li>
// whose text contains name.
func extractMealID(t *testing.T, body, name string) string {
	t.Helper()
	idx := strings.Index(body, name)
	if idx == -1 {
		t.Fatalf("meal %q not found in body", name)
	}
	const marker = `/meals/`
	rest := body[idx:]
	start := strings.Index(rest, marker)
	if start == -1 {
		t.Fatalf("no /meals/ link found after %q", name)
	}
	rest = rest[start+len(marker):]
	end := strings.IndexByte(rest, '/')
	if end == -1 {
		t.Fatalf("malformed meal link after %q", name)
	}
	return rest[:end]
}

// extractCategoryID pulls a category's UUID out of its rename form action
// (action="/meals/categories/<uuid>") near the row containing name.
func extractCategoryID(t *testing.T, body, name string) string {
	t.Helper()
	idx := strings.Index(body, `value="`+name+`"`)
	if idx == -1 {
		t.Fatalf("category %q not found in body", name)
	}
	prefix := body[:idx]
	const marker = `/meals/categories/`
	start := strings.LastIndex(prefix, marker)
	if start == -1 {
		t.Fatalf("no /meals/categories/ link found before %q", name)
	}
	rest := prefix[start+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end == -1 {
		t.Fatalf("malformed category link before %q", name)
	}
	return rest[:end]
}

func TestMeals_ToggleFavorite(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/meals/new")
	createRec := c.postForm("/meals", url.Values{
		"name": {"Star Meal"}, "serving_label": {"per serving"}, "serving_amount": {"1"},
	}, token)
	if createRec.Code != http.StatusSeeOther {
		t.Fatalf("create: status = %d", createRec.Code)
	}
	id := extractMealID(t, c.get("/meals").Body.String(), "Star Meal")

	favToken := c.csrfToken("/meals")
	firstToggle := c.postFormHX("/meals/"+id+"/favorite", url.Values{}, favToken)
	if firstToggle.Code != http.StatusOK {
		t.Fatalf("first toggle: status = %d, body: %s", firstToggle.Code, firstToggle.Body.String())
	}
	if !strings.Contains(firstToggle.Body.String(), "★") {
		t.Errorf("expected the filled star after favoriting, got: %s", firstToggle.Body.String())
	}

	// Favorited meals show up as one-click quick-add on the Day Builder.
	dayPage := c.get("/")
	loc := dayPage.Header().Get("Location")
	dayBody := c.get(loc).Body.String()
	if !strings.Contains(dayBody, "Star Meal") {
		t.Errorf("expected the favorited meal to appear as a quick-add on the day page, got: %s", dayBody)
	}

	secondToggle := c.postFormHX("/meals/"+id+"/favorite", url.Values{}, favToken)
	if secondToggle.Code != http.StatusOK {
		t.Fatalf("second toggle: status = %d", secondToggle.Code)
	}
	if strings.Contains(secondToggle.Body.String(), "★") {
		t.Errorf("expected the outline star after un-favoriting, got: %s", secondToggle.Body.String())
	}
}

func TestMeals_LabelCreateAssignFilterDelete(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	// --- Create a label ---
	labelToken := c.csrfToken("/meals/labels")
	createLabelRec := c.postForm("/meals/labels", url.Values{
		"name": {"quick"}, "color": {"blue"},
	}, labelToken)
	if createLabelRec.Code != http.StatusSeeOther {
		t.Fatalf("create label: status = %d, body: %s", createLabelRec.Code, createLabelRec.Body.String())
	}
	labelsPage := c.get("/meals/labels")
	if !strings.Contains(labelsPage.Body.String(), "quick") {
		t.Fatalf("expected the new label to appear on the labels page, got: %s", labelsPage.Body.String())
	}
	labelID := extractLabelID(t, labelsPage.Body.String())

	// --- Assign it to a meal via the create form ---
	mealToken := c.csrfToken("/meals/new")
	createMealRec := c.postForm("/meals", url.Values{
		"name": {"Quick Wrap"}, "serving_label": {"per serving"}, "serving_amount": {"1"},
		"label_ids": {labelID},
	}, mealToken)
	if createMealRec.Code != http.StatusSeeOther {
		t.Fatalf("create meal with label: status = %d, body: %s", createMealRec.Code, createMealRec.Body.String())
	}

	// --- List shows the badge ---
	list := c.get("/meals")
	if !strings.Contains(list.Body.String(), "quick") {
		t.Errorf("expected the meal list to show the label badge, got: %s", list.Body.String())
	}

	// --- Filtering by label finds only the labeled meal ---
	createUnlabeledMeal(t, c)
	filtered := c.get("/meals?label_id=" + labelID)
	if !strings.Contains(filtered.Body.String(), "Quick Wrap") {
		t.Errorf("expected the label filter to include the labeled meal, got: %s", filtered.Body.String())
	}
	if strings.Contains(filtered.Body.String(), "Unlabeled Meal") {
		t.Errorf("expected the label filter to exclude an unlabeled meal, got: %s", filtered.Body.String())
	}

	// --- Deleting the label removes it from the meal, not the meal itself ---
	deleteToken := c.csrfToken("/meals/labels")
	deleteRec := c.postForm("/meals/labels/"+labelID+"/delete", nil, deleteToken)
	if deleteRec.Code != http.StatusSeeOther {
		t.Fatalf("delete label: status = %d", deleteRec.Code)
	}
	afterDelete := c.get("/meals")
	if !strings.Contains(afterDelete.Body.String(), "Quick Wrap") {
		t.Error("expected the meal to still exist after its label was deleted")
	}
}

func createUnlabeledMeal(t *testing.T, c *testClient) {
	t.Helper()
	token := c.csrfToken("/meals/new")
	rec := c.postForm("/meals", url.Values{
		"name": {"Unlabeled Meal"}, "serving_label": {"per serving"}, "serving_amount": {"1"},
	}, token)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create unlabeled meal: status = %d, body: %s", rec.Code, rec.Body.String())
	}
}

// extractLabelID pulls a label's UUID out of its delete-form action
// (action="/meals/labels/<uuid>/delete") on a rendered labels page.
func extractLabelID(t *testing.T, body string) string {
	t.Helper()
	const marker = `/meals/labels/`
	idx := strings.Index(body, marker)
	if idx == -1 {
		t.Fatalf("no /meals/labels/ link found in body: %s", body)
	}
	rest := body[idx+len(marker):]
	end := strings.Index(rest, "/delete")
	if end == -1 {
		t.Fatalf("malformed label delete link in body: %s", body)
	}
	return rest[:end]
}
