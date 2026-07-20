package mealie_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Stasky745/victus/internal/importers/mealie"
)

func TestRecipeNutrition_NutrientAmounts_ParsesValidFields(t *testing.T) {
	n := mealie.RecipeNutrition{
		Calories:            "540",
		ProteinContent:      "12.5",
		FatContent:          "20",
		SaturatedFatContent: "8",
		CarbohydrateContent: "60",
		FiberContent:        "4",
		SugarContent:        "30",
		SodiumContent:       "400",
		CholesterolContent:  "10",
	}
	got := n.NutrientAmounts()

	want := map[string]float64{
		"calories": 540, "protein_g": 12.5, "fat_g": 20, "saturated_fat_g": 8,
		"carbs_g": 60, "fiber_g": 4, "sugar_g": 30, "sodium_mg": 400, "cholesterol_mg": 10,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

// TestRecipeNutrition_NutrientAmounts_OmitsBlankAndUnparseableFields guards
// the core mapping contract: a blank or garbage field must be OMITTED from
// the result (no value recorded), never coerced to a misleading zero — a
// recipe with no fiber data shouldn't claim "0g fiber."
func TestRecipeNutrition_NutrientAmounts_OmitsBlankAndUnparseableFields(t *testing.T) {
	n := mealie.RecipeNutrition{
		Calories:       "500",
		ProteinContent: "",        // blank: not recorded
		FatContent:     "unknown", // Mealie sometimes has non-numeric junk
		FiberContent:   "  ",      // whitespace-only
	}
	got := n.NutrientAmounts()

	if _, ok := got["calories"]; !ok {
		t.Error("expected calories to be present")
	}
	for _, key := range []string{"protein_g", "fat_g", "fiber_g"} {
		if _, ok := got[key]; ok {
			t.Errorf("expected %s to be omitted (blank/unparseable), got %v", key, got[key])
		}
	}
}

// TestRecipeNutrition_NutrientAmounts_OmitsNaNAndInfinity guards against a
// real bug found during review: strconv.ParseFloat explicitly accepts the
// literal strings "NaN"/"Inf"/"+Inf"/"-Inf" as valid input, so unlike a
// genuinely garbled string these used to sail through into the amounts map
// instead of being omitted — only failing much later at the DB-write
// boundary, aborting the whole import instead of just this one nutrient.
func TestRecipeNutrition_NutrientAmounts_OmitsNaNAndInfinity(t *testing.T) {
	n := mealie.RecipeNutrition{
		Calories:            "500",
		ProteinContent:      "NaN",
		FatContent:          "Inf",
		SaturatedFatContent: "+Inf",
		CarbohydrateContent: "-Inf",
	}
	got := n.NutrientAmounts()

	if _, ok := got["calories"]; !ok {
		t.Error("expected calories to be present")
	}
	for _, key := range []string{"protein_g", "fat_g", "saturated_fat_g", "carbs_g"} {
		if _, ok := got[key]; ok {
			t.Errorf("expected %s to be omitted (NaN/Infinity), got %v", key, got[key])
		}
	}
}

func TestRecipeNutrition_NutrientAmounts_EmptyNutritionYieldsEmptyMap(t *testing.T) {
	got := mealie.RecipeNutrition{}.NutrientAmounts()
	if len(got) != 0 {
		t.Errorf("expected no entries for entirely-blank nutrition, got %v", got)
	}
}

func TestClient_Search_ParsesItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/recipes" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("search"); got != "chicken" {
			t.Errorf("search query = %q, want chicken", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization header = %q, want Bearer test-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"slug":"chicken-rice","name":"Chicken & Rice"},{"slug":"chicken-soup","name":"Chicken Soup"}],"page":1,"total":2}`))
	}))
	defer srv.Close()

	c := mealie.New(srv.URL, "test-token")
	results, err := c.Search(t.Context(), "chicken")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2: %v", len(results), results)
	}
	if results[0].Slug != "chicken-rice" || results[0].Name != "Chicken & Rice" {
		t.Errorf("unexpected first result: %+v", results[0])
	}
}

func TestClient_Search_TrailingSlashInBaseURLDoesNotDoubleUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/recipes" {
			t.Errorf("unexpected path (base URL trailing slash not trimmed?): %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()

	c := mealie.New(srv.URL+"/", "test-token")
	if _, err := c.Search(t.Context(), "x"); err != nil {
		t.Fatalf("search: %v", err)
	}
}

func TestClient_GetRecipe_ParsesNutrition(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/recipes/chicken-rice" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"slug": "chicken-rice",
			"name": "Chicken & Rice",
			"nutrition": {
				"calories": "650",
				"proteinContent": "45",
				"fatContent": "",
				"carbohydrateContent": "70"
			}
		}`))
	}))
	defer srv.Close()

	c := mealie.New(srv.URL, "test-token")
	recipe, err := c.GetRecipe(t.Context(), "chicken-rice")
	if err != nil {
		t.Fatalf("get recipe: %v", err)
	}
	if recipe.Name != "Chicken & Rice" {
		t.Errorf("name = %q", recipe.Name)
	}
	amounts := recipe.Nutrition.NutrientAmounts()
	if amounts["calories"] != 650 || amounts["protein_g"] != 45 || amounts["carbs_g"] != 70 {
		t.Errorf("unexpected amounts: %v", amounts)
	}
	if _, ok := amounts["fat_g"]; ok {
		t.Errorf("expected blank fatContent to be omitted, got %v", amounts["fat_g"])
	}
}

func TestClient_GetRecipe_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := mealie.New(srv.URL, "test-token")
	if _, err := c.GetRecipe(t.Context(), "ghost-recipe"); !errors.Is(err, mealie.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestClient_GetRecipe_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := mealie.New(srv.URL, "test-token")
	if _, err := c.GetRecipe(t.Context(), "x"); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestClient_GetRecipe_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not valid json`))
	}))
	defer srv.Close()

	c := mealie.New(srv.URL, "test-token")
	if _, err := c.GetRecipe(t.Context(), "x"); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

func TestClient_RecipeURL(t *testing.T) {
	c := mealie.New("https://mealie.example.com/", "token")
	if got, want := c.RecipeURL("chicken-rice"), "https://mealie.example.com/recipe/chicken-rice"; got != want {
		t.Errorf("RecipeURL = %q, want %q", got, want)
	}
}
