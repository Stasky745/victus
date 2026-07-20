package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Stasky745/victus/internal/httpserver"
	"github.com/Stasky745/victus/internal/importers/mealie"
	"github.com/Stasky745/victus/internal/importers/openfoodfacts"
)

// fakeMealieServer returns an httptest.Server that mimics just enough of
// Mealie's REST API for the import flow: search and single-recipe detail.
func fakeMealieServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/recipes":
			_, _ = w.Write([]byte(`{"items":[{"slug":"chicken-rice","name":"Chicken & Rice"}]}`))
		case "/api/recipes/chicken-rice":
			_, _ = w.Write([]byte(`{
				"slug": "chicken-rice",
				"name": "Chicken & Rice",
				"nutrition": {"calories": "650", "proteinContent": "45"}
			}`))
		case "/api/recipes/ghost-recipe":
			http.Error(w, "not found", http.StatusNotFound)
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeOFFServer returns an httptest.Server that mimics just enough of Open
// Food Facts's API for the import flow: barcode lookup and name search.
func fakeOFFServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/product/3017620422003.json":
			_, _ = w.Write([]byte(`{"product":{"code":"3017620422003","product_name":"Nutella","nutriments":{"energy-kcal_100g":539,"sodium_100g":0.0428}}}`))
		case "/api/v3/product/0000000000000.json":
			_, _ = w.Write([]byte(`{"code":"0000000000000","status":0,"product":{}}`))
		case "/cgi/search.pl":
			_, _ = w.Write([]byte(`{"products":[{"code":"3017620422003","product_name":"Nutella","nutriments":{"energy-kcal_100g":539}}]}`))
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestImport_PageHidesMealieSectionWhenNotConfigured(t *testing.T) {
	srv, pool := newTestServerAndPoolWithOptions(t, httpserver.WithOFFClient(openfoodfacts.New(openfoodfacts.WithBaseURL(fakeOFFServer(t).URL))))
	c := newAuthenticatedClient(t, pool, srv)

	rec := c.get("/meals/import")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "Search Mealie") {
		t.Error("expected the Mealie search section to be hidden when Mealie isn't configured")
	}
	if !strings.Contains(rec.Body.String(), "Search Open Food Facts") {
		t.Error("expected the Open Food Facts section to always be present")
	}
}

func TestImport_PageShowsMealieSectionWhenConfigured(t *testing.T) {
	mealieSrv := fakeMealieServer(t)
	srv, pool := newTestServerAndPoolWithOptions(t,
		httpserver.WithMealieClient(mealie.New(mealieSrv.URL, "test-token")),
		httpserver.WithOFFClient(openfoodfacts.New(openfoodfacts.WithBaseURL(fakeOFFServer(t).URL))),
	)
	c := newAuthenticatedClient(t, pool, srv)

	rec := c.get("/meals/import")
	if !strings.Contains(rec.Body.String(), "Search Mealie") {
		t.Error("expected the Mealie search section to be shown when configured")
	}
}

func TestImport_MealieSearchReturns404WhenNotConfigured(t *testing.T) {
	srv, pool := newTestServerAndPoolWithOptions(t, httpserver.WithOFFClient(openfoodfacts.New(openfoodfacts.WithBaseURL(fakeOFFServer(t).URL))))
	c := newAuthenticatedClient(t, pool, srv)

	rec := c.get("/meals/import/mealie/search?q=chicken")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestImport_MealieSearchAndImport(t *testing.T) {
	mealieSrv := fakeMealieServer(t)
	srv, pool := newTestServerAndPoolWithOptions(t,
		httpserver.WithMealieClient(mealie.New(mealieSrv.URL, "test-token")),
		httpserver.WithOFFClient(openfoodfacts.New(openfoodfacts.WithBaseURL(fakeOFFServer(t).URL))),
	)
	c := newAuthenticatedClient(t, pool, srv)

	search := c.get("/meals/import/mealie/search?q=chicken")
	if search.Code != http.StatusOK {
		t.Fatalf("search status = %d, body: %s", search.Code, search.Body.String())
	}
	if !strings.Contains(search.Body.String(), "Chicken &amp; Rice") {
		t.Errorf("expected the fake Mealie recipe in results, got: %s", search.Body.String())
	}

	token := c.csrfToken("/meals/import")
	importRec := c.postForm("/meals/import/mealie/chicken-rice", url.Values{}, token)
	if importRec.Code != http.StatusSeeOther {
		t.Fatalf("import status = %d, body: %s", importRec.Code, importRec.Body.String())
	}

	list := c.get("/meals")
	if !strings.Contains(list.Body.String(), "Chicken &amp; Rice") {
		t.Errorf("expected the imported recipe to appear in the meal library, got: %s", list.Body.String())
	}
}

func TestImport_MealieImport_NotFoundRecipe(t *testing.T) {
	mealieSrv := fakeMealieServer(t)
	srv, pool := newTestServerAndPoolWithOptions(t,
		httpserver.WithMealieClient(mealie.New(mealieSrv.URL, "test-token")),
		httpserver.WithOFFClient(openfoodfacts.New(openfoodfacts.WithBaseURL(fakeOFFServer(t).URL))),
	)
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/meals/import")
	rec := c.postForm("/meals/import/mealie/ghost-recipe", url.Values{}, token)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestImport_OFFSearchAndImport(t *testing.T) {
	srv, pool := newTestServerAndPoolWithOptions(t, httpserver.WithOFFClient(openfoodfacts.New(openfoodfacts.WithBaseURL(fakeOFFServer(t).URL))))
	c := newAuthenticatedClient(t, pool, srv)

	search := c.get("/meals/import/off/search?q=nutella")
	if search.Code != http.StatusOK {
		t.Fatalf("search status = %d, body: %s", search.Code, search.Body.String())
	}
	if !strings.Contains(search.Body.String(), "Nutella") {
		t.Errorf("expected the fake product in results, got: %s", search.Body.String())
	}

	token := c.csrfToken("/meals/import")
	importRec := c.postForm("/meals/import/off/3017620422003", url.Values{}, token)
	if importRec.Code != http.StatusSeeOther {
		t.Fatalf("import status = %d, body: %s", importRec.Code, importRec.Body.String())
	}

	list := c.get("/meals")
	if !strings.Contains(list.Body.String(), "Nutella") {
		t.Errorf("expected the imported product to appear in the meal library, got: %s", list.Body.String())
	}
}

func TestImport_OFFBarcodeLookup(t *testing.T) {
	srv, pool := newTestServerAndPoolWithOptions(t, httpserver.WithOFFClient(openfoodfacts.New(openfoodfacts.WithBaseURL(fakeOFFServer(t).URL))))
	c := newAuthenticatedClient(t, pool, srv)

	rec := c.get("/meals/import/off/search?barcode=3017620422003")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Nutella") {
		t.Errorf("expected the barcode lookup to find the fake product, got: %s", rec.Body.String())
	}
}

func TestImport_OFFBarcodeLookup_NotFound(t *testing.T) {
	srv, pool := newTestServerAndPoolWithOptions(t, httpserver.WithOFFClient(openfoodfacts.New(openfoodfacts.WithBaseURL(fakeOFFServer(t).URL))))
	c := newAuthenticatedClient(t, pool, srv)

	rec := c.get("/meals/import/off/search?barcode=0000000000000")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "No matching products") {
		t.Errorf("expected a clear no-results message for an unknown barcode, got: %s", rec.Body.String())
	}
}

// TestImport_ReImport_UpdatesExistingMeal guards the idempotent-import
// contract at the HTTP layer: importing the same barcode twice must update
// the same library entry, not create a duplicate every time.
func TestImport_ReImport_UpdatesExistingMeal(t *testing.T) {
	srv, pool := newTestServerAndPoolWithOptions(t, httpserver.WithOFFClient(openfoodfacts.New(openfoodfacts.WithBaseURL(fakeOFFServer(t).URL))))
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/meals/import")
	if rec := c.postForm("/meals/import/off/3017620422003", url.Values{}, token); rec.Code != http.StatusSeeOther {
		t.Fatalf("first import: status = %d, body: %s", rec.Code, rec.Body.String())
	}
	token2 := c.csrfToken("/meals/import")
	if rec := c.postForm("/meals/import/off/3017620422003", url.Values{}, token2); rec.Code != http.StatusSeeOther {
		t.Fatalf("second import: status = %d, body: %s", rec.Code, rec.Body.String())
	}

	list := c.get("/meals")
	if got := strings.Count(list.Body.String(), "Nutella"); got != 1 {
		t.Errorf("expected exactly one library entry after re-importing the same barcode, got %d occurrences", got)
	}
}
