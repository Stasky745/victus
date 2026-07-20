package httpserver_test

import (
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/dbtest"
)

func TestSettings_PageShowsSeededDefaults(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	rec := c.get("/settings")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Calories") {
		t.Errorf("expected the seeded nutrient registry to be listed, got: %s", body)
	}
	if !strings.Contains(body, "myplate.gov") {
		t.Errorf("expected migration 00005's seeded default info URL to pre-fill the form, got: %s", body)
	}
}

func TestSettings_SaveGoals_Roundtrips(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	nutrientID := caloriesNutrientID(t, pool)

	token := c.csrfToken("/settings")
	form := url.Values{}
	form.Set("min_"+nutrientID, "1300")
	form.Set("max_"+nutrientID, "1600")
	rec := c.postForm("/settings", form, token)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save: status = %d, body: %s", rec.Code, rec.Body.String())
	}

	reload := c.get("/settings")
	body := reload.Body.String()
	if !hasNamedFieldValue(body, "min_"+nutrientID, "1300") {
		t.Errorf("expected min_%s to have saved value 1300, got: %s", nutrientID, body)
	}
	if !hasNamedFieldValue(body, "max_"+nutrientID, "1600") {
		t.Errorf("expected max_%s to have saved value 1600, got: %s", nutrientID, body)
	}
}

func TestSettings_SaveGoals_IdealRangeRoundtrips(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	nutrientID := caloriesNutrientID(t, pool)

	token := c.csrfToken("/settings")
	form := url.Values{}
	form.Set("min_"+nutrientID, "1200")
	form.Set("ideal_min_"+nutrientID, "1400")
	form.Set("ideal_max_"+nutrientID, "1500")
	form.Set("max_"+nutrientID, "1600")
	rec := c.postForm("/settings", form, token)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save: status = %d, body: %s", rec.Code, rec.Body.String())
	}

	body := c.get("/settings").Body.String()
	if !hasNamedFieldValue(body, "ideal_min_"+nutrientID, "1400") {
		t.Errorf("expected ideal_min_%s to have saved value 1400, got: %s", nutrientID, body)
	}
	if !hasNamedFieldValue(body, "ideal_max_"+nutrientID, "1500") {
		t.Errorf("expected ideal_max_%s to have saved value 1500, got: %s", nutrientID, body)
	}

	dayPage := c.get(c.get("/").Header().Get("Location"))
	if !strings.Contains(dayPage.Body.String(), "text-red-600") {
		t.Errorf("expected under-goal (below min) coloring at 0 calories, got: %s", dayPage.Body.String())
	}
	if !strings.Contains(dayPage.Body.String(), "ideal 1400") {
		t.Errorf("expected the ideal range to appear in the day summary caption, got: %s", dayPage.Body.String())
	}
}

func TestSettings_RejectsIdealMinBelowMin(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	nutrientID := caloriesNutrientID(t, pool)

	token := c.csrfToken("/settings")
	form := url.Values{}
	form.Set("min_"+nutrientID, "1300")
	form.Set("ideal_min_"+nutrientID, "1000")
	rec := c.postForm("/settings", form, token)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ideal minimum") || !strings.Contains(rec.Body.String(), "below the minimum") {
		t.Errorf("expected a clear ideal-min-below-min validation message, got: %s", rec.Body.String())
	}
}

func TestSettings_RejectsIdealMaxAboveMax(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	nutrientID := caloriesNutrientID(t, pool)

	token := c.csrfToken("/settings")
	form := url.Values{}
	form.Set("max_"+nutrientID, "1600")
	form.Set("ideal_max_"+nutrientID, "2000")
	rec := c.postForm("/settings", form, token)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ideal maximum") || !strings.Contains(rec.Body.String(), "above the maximum") {
		t.Errorf("expected a clear ideal-max-above-max validation message, got: %s", rec.Body.String())
	}
}

func TestSettings_SaveInfoURL_Roundtrips(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/settings")
	rec := c.postForm("/settings/info-url", url.Values{"info_url": {"https://example.com/my-healthy-targets"}}, token)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save: status = %d, body: %s", rec.Code, rec.Body.String())
	}

	reload := c.get("/settings")
	if !hasNamedFieldValue(reload.Body.String(), "info_url", "https://example.com/my-healthy-targets") {
		t.Errorf("expected the saved info url to persist, got: %s", reload.Body.String())
	}
}

// TestSettings_SavingGoals_NeverTouchesInfoURL guards against a real bug
// found during review: the goal-ranges save and the info-url save used to
// be bundled into one form/one transaction, so every ordinary personal-goal
// save silently overwrote the shared info url with whatever value happened
// to be sitting in that submitter's already-rendered (possibly stale) form
// — clobbering a more recent change someone else made in the meantime. The
// two are now separate routes/forms; saving goals must never touch the url.
func TestSettings_SavingGoals_NeverTouchesInfoURL(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/settings")
	if rec := c.postForm("/settings/info-url", url.Values{"info_url": {"https://example.com/set-by-someone-else"}}, token); rec.Code != http.StatusSeeOther {
		t.Fatalf("save info url: status = %d, body: %s", rec.Code, rec.Body.String())
	}

	nutrientID := caloriesNutrientID(t, pool)
	goalsToken := c.csrfToken("/settings")
	goalsForm := url.Values{}
	goalsForm.Set("min_"+nutrientID, "1300")
	if rec := c.postForm("/settings", goalsForm, goalsToken); rec.Code != http.StatusSeeOther {
		t.Fatalf("save goals: status = %d, body: %s", rec.Code, rec.Body.String())
	}

	reload := c.get("/settings")
	if !hasNamedFieldValue(reload.Body.String(), "info_url", "https://example.com/set-by-someone-else") {
		t.Errorf("expected the info url set by the earlier save to survive an unrelated goals save, got: %s", reload.Body.String())
	}
}

func TestSettings_RejectsMinGreaterThanMax(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	nutrientID := caloriesNutrientID(t, pool)

	token := c.csrfToken("/settings")
	form := url.Values{}
	form.Set("min_"+nutrientID, "1600")
	form.Set("max_"+nutrientID, "1300")
	rec := c.postForm("/settings", form, token)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "greater than its maximum") {
		t.Errorf("expected a clear min>max validation message, got: %s", rec.Body.String())
	}
	// The invalid submission should still be visible in the re-rendered form.
	if !hasNamedFieldValue(rec.Body.String(), "min_"+nutrientID, "1600") {
		t.Errorf("expected the submitted (invalid) min to survive the re-render, got: %s", rec.Body.String())
	}
}

func TestSettings_RejectsNonFiniteBound(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	nutrientID := caloriesNutrientID(t, pool)

	token := c.csrfToken("/settings")
	form := url.Values{}
	form.Set("min_"+nutrientID, "NaN")
	rec := c.postForm("/settings", form, token)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d — NaN must not silently pass numeric validation", rec.Code, http.StatusUnprocessableEntity)
	}
}

// TestSettings_RejectsNegativeBound guards against a real bug found during
// review: buildGoalsForm's error message claimed a bound "must be a
// non-negative number," but parseOptionalFiniteFloat only checked
// finiteness, never sign — a negative goal bound silently saved.
func TestSettings_RejectsNegativeBound(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	nutrientID := caloriesNutrientID(t, pool)

	token := c.csrfToken("/settings")
	form := url.Values{}
	form.Set("min_"+nutrientID, "-500")
	rec := c.postForm("/settings", form, token)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d — a negative bound must not silently pass validation", rec.Code, http.StatusUnprocessableEntity)
	}
	if !strings.Contains(rec.Body.String(), "non-negative") {
		t.Errorf("expected a clear non-negative validation message, got: %s", rec.Body.String())
	}
}

func TestSettings_RejectsInvalidInfoURL(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/settings")
	rec := c.postForm("/settings/info-url", url.Values{"info_url": {"not-a-url"}}, token)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if !strings.Contains(rec.Body.String(), "absolute http(s) URL") {
		t.Errorf("expected a clear URL validation message, got: %s", rec.Body.String())
	}
}

// TestSettings_ClearGoal_RemovesRangeColoring guards the whole point of this
// milestone end to end: setting a goal colors the Day Builder's summary, and
// clearing it (submitting blank min/max) removes that coloring again.
func TestSettings_ClearGoal_RemovesRangeColoring(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	nutrientID := caloriesNutrientID(t, pool)

	token := c.csrfToken("/settings")
	form := url.Values{}
	form.Set("min_"+nutrientID, "1300")
	form.Set("max_"+nutrientID, "1600")
	if rec := c.postForm("/settings", form, token); rec.Code != http.StatusSeeOther {
		t.Fatalf("save: status = %d, body: %s", rec.Code, rec.Body.String())
	}

	day := c.get("/")
	loc := day.Header().Get("Location")
	dayPage := c.get(loc)
	// No items added yet, so the 0-calorie total is below the 1300 minimum —
	// expect the "under" coloring class.
	if !strings.Contains(dayPage.Body.String(), "text-red-600") {
		t.Errorf("expected the day summary to show under-goal coloring, got: %s", dayPage.Body.String())
	}
	if !strings.Contains(dayPage.Body.String(), "goal: 1300") {
		t.Errorf("expected the day summary to show the configured goal range, got: %s", dayPage.Body.String())
	}

	clearToken := c.csrfToken("/settings")
	clearForm := url.Values{}
	clearForm.Set("min_"+nutrientID, "")
	clearForm.Set("max_"+nutrientID, "")
	if rec := c.postForm("/settings", clearForm, clearToken); rec.Code != http.StatusSeeOther {
		t.Fatalf("clear: status = %d, body: %s", rec.Code, rec.Body.String())
	}

	afterClear := c.get(loc)
	if strings.Contains(afterClear.Body.String(), "text-red-600") {
		t.Errorf("expected coloring to be gone after clearing the goal, got: %s", afterClear.Body.String())
	}
	if strings.Contains(afterClear.Body.String(), "goal: 1300") {
		t.Errorf("expected the goal range caption to be gone after clearing, got: %s", afterClear.Body.String())
	}
}

// caloriesNutrientID looks up the seeded "Calories" nutrient's id directly
// from the database, rather than scraping the rendered settings page for
// it — a text-scan approach ("find the word Calories, then the next min_
// input after it") would silently resolve to the wrong nutrient's field if
// a future migration ever reordered the seeded sort_order, or if any page
// text before the real row happened to contain the word "Calories".
func caloriesNutrientID(t *testing.T, pool *sql.DB) string {
	t.Helper()
	q, err := db.NewQuerier(dbtest.Driver(), pool)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}
	nutrients, err := q.ListNutrients(t.Context())
	if err != nil {
		t.Fatalf("list nutrients: %v", err)
	}
	for _, n := range nutrients {
		if n.Key == "calories" {
			return strconv.Itoa(int(n.ID))
		}
	}
	t.Fatal("calories nutrient not found in seeded registry")
	return ""
}

// hasNamedFieldValue reports whether the rendered HTML's <input name="name"
// ...> tag also carries value="value", scoped to that single tag (up to its
// closing '>') so an intervening attribute like "required" (present on the
// info_url field but not the goal-range fields) doesn't break the match, and
// — unlike a bare substring check for `value="X"` — a different field on the
// page coincidentally sharing the same value can't satisfy it.
func hasNamedFieldValue(body, name, value string) bool {
	idx := strings.Index(body, `name="`+name+`"`)
	if idx == -1 {
		return false
	}
	rest := body[idx:]
	end := strings.IndexByte(rest, '>')
	if end == -1 {
		return false
	}
	return strings.Contains(rest[:end], `value="`+value+`"`)
}
