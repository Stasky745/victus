package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/csrf"

	"github.com/Stasky745/victus/internal/auth"
	"github.com/Stasky745/victus/internal/httperr"
	"github.com/Stasky745/victus/internal/importers/mealie"
	"github.com/Stasky745/victus/internal/importers/openfoodfacts"
	"github.com/Stasky745/victus/internal/mealslib"
	"github.com/Stasky745/victus/web/templates/meals"
)

func (s *Server) handleImportPage(w http.ResponseWriter, r *http.Request) {
	if err := meals.ImportPage(csrf.Token(r), s.mealie != nil).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render import page", "error", err)
	}
}

// requireMealieConfigured writes a 404 and returns false if Mealie isn't
// configured for this deployment. Every Mealie-touching handler must call
// this first — a single shared guard, so a future third handler can't add
// itself without it the way two independent copies of the same check could.
func (s *Server) requireMealieConfigured(w http.ResponseWriter) bool {
	if s.mealie == nil {
		http.Error(w, "Mealie is not configured", http.StatusNotFound)
		return false
	}
	return true
}

func (s *Server) handleMealieSearch(w http.ResponseWriter, r *http.Request) {
	if !s.requireMealieConfigured(w) {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		s.renderMealieResults(w, r, nil)
		return
	}
	results, err := s.mealie.Search(r.Context(), q)
	if err != nil {
		httperr.Internal(w, r, "failed to search mealie", err, "query", q)
		return
	}
	s.renderMealieResults(w, r, results)
}

func (s *Server) handleMealieImport(w http.ResponseWriter, r *http.Request) {
	if !s.requireMealieConfigured(w) {
		return
	}
	slug := chi.URLParam(r, "slug")
	user := auth.UserFromContext(r.Context())

	recipe, err := s.mealie.GetRecipe(r.Context(), slug)
	if err != nil {
		if errors.Is(err, mealie.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		httperr.Internal(w, r, "failed to fetch mealie recipe", err, "slug", slug)
		return
	}

	amounts, err := s.nutrientAmountsFromKeys(r.Context(), recipe.Nutrition.NutrientAmounts())
	if err != nil {
		httperr.Internal(w, r, "failed to map mealie nutrient keys", err, "slug", slug)
		return
	}

	if _, err := s.meals.Import(r.Context(), user.ID, mealslib.ImportInput{
		Name:            recipe.Name,
		Source:          "mealie",
		SourceRef:       recipe.Slug,
		RecipeURL:       s.mealie.RecipeURL(recipe.Slug),
		ServingLabel:    "per serving",
		ServingAmount:   1,
		NutrientAmounts: amounts,
	}); err != nil {
		httperr.Internal(w, r, "failed to import mealie recipe", err, "slug", slug)
		return
	}

	http.Redirect(w, r, "/meals", http.StatusSeeOther)
}

func (s *Server) handleOFFSearch(w http.ResponseWriter, r *http.Request) {
	if barcode := strings.TrimSpace(r.URL.Query().Get("barcode")); barcode != "" {
		product, err := s.off.GetByBarcode(r.Context(), barcode)
		if err != nil {
			if errors.Is(err, openfoodfacts.ErrNotFound) {
				s.renderOFFResults(w, r, nil)
				return
			}
			httperr.Internal(w, r, "failed to look up barcode", err, "barcode", barcode)
			return
		}
		s.renderOFFResults(w, r, []openfoodfacts.Product{product})
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		s.renderOFFResults(w, r, nil)
		return
	}
	products, err := s.off.SearchByName(r.Context(), q)
	if err != nil {
		httperr.Internal(w, r, "failed to search open food facts", err, "query", q)
		return
	}
	s.renderOFFResults(w, r, products)
}

func (s *Server) handleOFFImport(w http.ResponseWriter, r *http.Request) {
	barcode := chi.URLParam(r, "barcode")
	user := auth.UserFromContext(r.Context())

	product, err := s.off.GetByBarcode(r.Context(), barcode)
	if err != nil {
		if errors.Is(err, openfoodfacts.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		httperr.Internal(w, r, "failed to fetch open food facts product", err, "barcode", barcode)
		return
	}

	amounts, err := s.nutrientAmountsFromKeys(r.Context(), product.NutrientAmounts)
	if err != nil {
		httperr.Internal(w, r, "failed to map open food facts nutrient keys", err, "barcode", barcode)
		return
	}

	if _, err := s.meals.Import(r.Context(), user.ID, mealslib.ImportInput{
		Name:            product.DisplayName(),
		Source:          "off",
		SourceRef:       barcode,
		RecipeURL:       s.off.ProductURL(barcode),
		ServingLabel:    "per 100g",
		ServingAmount:   100,
		NutrientAmounts: amounts,
	}); err != nil {
		httperr.Internal(w, r, "failed to import open food facts product", err, "barcode", barcode)
		return
	}

	http.Redirect(w, r, "/meals", http.StatusSeeOther)
}

func (s *Server) renderMealieResults(w http.ResponseWriter, r *http.Request, results []mealie.SearchResult) {
	if err := meals.MealieResults(results).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render mealie search results", "error", err)
	}
}

func (s *Server) renderOFFResults(w http.ResponseWriter, r *http.Request, products []openfoodfacts.Product) {
	if err := meals.OFFResults(products).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render open food facts search results", "error", err)
	}
}

// nutrientAmountsFromKeys bridges an importer's key-based nutrient amounts
// (internal/importers/mealie, internal/importers/openfoodfacts — both
// deliberately independent of Victus's DB schema) to the id-based amounts
// mealslib.ImportInput expects. A key with no matching entry in Victus's
// registry (there is none today, but importers and the registry evolve
// independently) is silently dropped rather than erroring the whole import.
func (s *Server) nutrientAmountsFromKeys(ctx context.Context, byKey map[string]float64) (map[int16]float64, error) {
	keyToID, err := s.meals.NutrientIDsByKey(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int16]float64, len(byKey))
	for key, v := range byKey {
		if id, ok := keyToID[key]; ok {
			out[id] = v
		}
	}
	return out, nil
}
