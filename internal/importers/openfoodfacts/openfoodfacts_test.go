package openfoodfacts_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Stasky745/victus/internal/importers/openfoodfacts"
)

func TestProduct_DisplayName_FallsBackToBarcodeWhenNameBlank(t *testing.T) {
	named := openfoodfacts.Product{Barcode: "123", Name: "Nutella"}
	if got := named.DisplayName(); got != "Nutella" {
		t.Errorf("DisplayName() = %q, want Nutella", got)
	}

	unnamed := openfoodfacts.Product{Barcode: "123"}
	if got, want := unnamed.DisplayName(), "Unnamed product (123)"; got != want {
		t.Errorf("DisplayName() = %q, want %q", got, want)
	}
}

func TestClient_ProductURL_EscapesBarcode(t *testing.T) {
	c := openfoodfacts.New(openfoodfacts.WithBaseURL("https://world.openfoodfacts.org"))
	// A barcode containing a character meaningful in a URL must not be able
	// to alter the URL's structure — url.PathEscape should neutralize it.
	got := c.ProductURL("123 456")
	if got != "https://world.openfoodfacts.org/product/123%20456" {
		t.Errorf("ProductURL(%q) = %q, want the space percent-encoded", "123 456", got)
	}
}

func TestClient_GetByBarcode_ParsesNutrimentsAndConvertsUnits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/product/3017620422003.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Error("expected a User-Agent header to be set, per OFF's usage policy")
		}
		w.Header().Set("Content-Type", "application/json")
		// Real shape confirmed against the live API: nested under "product",
		// sodium/cholesterol/iron stored in GRAMS (a "_unit":"g" companion
		// field, not shown here, confirms this) despite Victus's nutrient
		// registry using milligrams for those three.
		_, _ = w.Write([]byte(`{
			"code": "3017620422003",
			"status": 1,
			"product": {
				"code": "3017620422003",
				"product_name": "Nutella",
				"nutriments": {
					"energy-kcal_100g": 539,
					"proteins_100g": 6.3,
					"carbohydrates_100g": 57.5,
					"fat_100g": 30.9,
					"saturated-fat_100g": 10.6,
					"sugars_100g": 56.3,
					"sodium_100g": 0.0428,
					"salt_100g": 0.11
				}
			}
		}`))
	}))
	defer srv.Close()

	c := openfoodfacts.New(openfoodfacts.WithBaseURL(srv.URL))
	product, err := c.GetByBarcode(t.Context(), "3017620422003")
	if err != nil {
		t.Fatalf("get by barcode: %v", err)
	}
	if product.Name != "Nutella" {
		t.Errorf("name = %q", product.Name)
	}
	if product.Barcode != "3017620422003" {
		t.Errorf("barcode = %q", product.Barcode)
	}

	amounts := product.NutrientAmounts
	if amounts["calories"] != 539 {
		t.Errorf("calories = %v, want 539", amounts["calories"])
	}
	if amounts["protein_g"] != 6.3 {
		t.Errorf("protein_g = %v, want 6.3", amounts["protein_g"])
	}
	// 0.0428g -> 42.8mg: the critical gram-to-milligram conversion.
	if got, want := amounts["sodium_mg"], 42.8; got < want-0.001 || got > want+0.001 {
		t.Errorf("sodium_mg = %v, want %v (0.0428g converted to mg)", got, want)
	}
	// salt_g stays in grams, unconverted — Victus tracks it separately from
	// sodium_mg specifically to match what's printed on packaging.
	if got, want := amounts["salt_g"], 0.11; got < want-0.001 || got > want+0.001 {
		t.Errorf("salt_g = %v, want %v (unconverted)", got, want)
	}
	// Not present in the response at all — must be omitted, not zero.
	if _, ok := amounts["fiber_g"]; ok {
		t.Errorf("expected fiber_g to be absent (not in response), got %v", amounts["fiber_g"])
	}
	if _, ok := amounts["cholesterol_mg"]; ok {
		t.Errorf("expected cholesterol_mg to be absent (not in response), got %v", amounts["cholesterol_mg"])
	}
}

// TestClient_GetByBarcode_ExplicitZeroIsNotOmitted guards the distinction
// between "OFF never recorded this nutrient" (omit) and "OFF recorded it as
// exactly zero" (keep) — e.g. a product genuinely has 0mg cholesterol.
func TestClient_GetByBarcode_ExplicitZeroIsNotOmitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"product": {
				"code": "0016000275287",
				"product_name": "Cheerios",
				"nutriments": {
					"cholesterol_100g": 0,
					"sodium_100g": 0.487179487179487
				}
			}
		}`))
	}))
	defer srv.Close()

	c := openfoodfacts.New(openfoodfacts.WithBaseURL(srv.URL))
	product, err := c.GetByBarcode(t.Context(), "0016000275287")
	if err != nil {
		t.Fatalf("get by barcode: %v", err)
	}
	amounts := product.NutrientAmounts
	if got, ok := amounts["cholesterol_mg"]; !ok || got != 0 {
		t.Errorf("expected an explicit 0 cholesterol to round-trip as present-and-zero, got ok=%v value=%v", ok, got)
	}
	if got, want := amounts["sodium_mg"], 487.179487179487; got < want-0.001 || got > want+0.001 {
		t.Errorf("sodium_mg = %v, want ~%v", got, want)
	}
}

func TestClient_GetByBarcode_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// OFF's real behavior for an unknown barcode: 200 OK with an empty product.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"0000000000000","status":0,"product":{}}`))
	}))
	defer srv.Close()

	c := openfoodfacts.New(openfoodfacts.WithBaseURL(srv.URL))
	if _, err := c.GetByBarcode(t.Context(), "0000000000000"); !errors.Is(err, openfoodfacts.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestClient_GetByBarcode_HTTPNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := openfoodfacts.New(openfoodfacts.WithBaseURL(srv.URL))
	if _, err := c.GetByBarcode(t.Context(), "x"); !errors.Is(err, openfoodfacts.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestClient_SearchByName_ParsesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cgi/search.pl" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("search_terms"); got != "nutella" {
			t.Errorf("search_terms = %q, want nutella", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"products": [
				{"code": "3017620422003", "product_name": "Nutella", "nutriments": {"energy-kcal_100g": 539}},
				{"code": "3017620425035", "product_name": "Nutella B-Ready", "nutriments": {"energy-kcal_100g": 495}}
			]
		}`))
	}))
	defer srv.Close()

	c := openfoodfacts.New(openfoodfacts.WithBaseURL(srv.URL))
	results, err := c.SearchByName(t.Context(), "nutella")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2: %v", len(results), results)
	}
	if results[0].Name != "Nutella" || results[0].NutrientAmounts["calories"] != 539 {
		t.Errorf("unexpected first result: %+v", results[0])
	}
}

// TestClient_SearchByName_SkipsEmptyEntries guards against a malformed or
// placeholder entry in OFF's response (no code at all) rendering as a
// blank, unusable "import" result.
func TestClient_SearchByName_SkipsEmptyEntries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"products": [{"code": "", "product_name": ""}, {"code": "123", "product_name": "Real Product"}]}`))
	}))
	defer srv.Close()

	c := openfoodfacts.New(openfoodfacts.WithBaseURL(srv.URL))
	results, err := c.SearchByName(t.Context(), "x")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].Barcode != "123" {
		t.Errorf("expected only the well-formed entry to survive, got %+v", results)
	}
}

// TestClient_SearchByName_SkipsMalformedEntry guards against a real bug
// found during review: decoding the whole "products" array in one
// json.Decode call meant a single structurally malformed entry (e.g. a
// numeric field OFF sometimes sends as "" instead of omitted — a
// well-documented OFF data-quality quirk) failed the ENTIRE search,
// discarding every other good result in the same response.
func TestClient_SearchByName_SkipsMalformedEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"products": [
				{"code": "111", "product_name": "Good Product", "nutriments": {"energy-kcal_100g": 100}},
				{"code": "222", "product_name": "Bad Product", "nutriments": {"energy-kcal_100g": ""}},
				{"code": "333", "product_name": "Another Good Product", "nutriments": {"energy-kcal_100g": 200}}
			]
		}`))
	}))
	defer srv.Close()

	c := openfoodfacts.New(openfoodfacts.WithBaseURL(srv.URL))
	results, err := c.SearchByName(t.Context(), "x")
	if err != nil {
		t.Fatalf("search: %v (the malformed entry should be skipped, not fail the whole search)", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (the two well-formed entries), got %+v", len(results), results)
	}
	for _, want := range []string{"111", "333"} {
		var found bool
		for _, r := range results {
			if r.Barcode == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected barcode %q to survive the malformed sibling entry, got %+v", want, results)
		}
	}
}

func TestClient_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := openfoodfacts.New(openfoodfacts.WithBaseURL(srv.URL))
	if _, err := c.SearchByName(t.Context(), "x"); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}
