package httpserver

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/csrf"

	"github.com/Stasky745/victus/internal/auth"
	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/dberr"
	"github.com/Stasky745/victus/internal/httperr"
	"github.com/Stasky745/victus/internal/mealslib"
	"github.com/Stasky745/victus/internal/urlutil"
	"github.com/Stasky745/victus/web/templates/meals"
)

const defaultSearchLimit = 25

func (s *Server) handleMealsList(w http.ResponseWriter, r *http.Request) {
	labelID, _ := uuid.Parse(r.URL.Query().Get("label_id")) // zero uuid.Nil on empty/invalid — "no filter"
	list, err := s.searchMealsFiltered(r.Context(), "", labelID)
	if err != nil {
		httperr.Internal(w, r, "failed to list meals", err)
		return
	}
	labelsByMeal, err := s.meals.LabelsForMeals(r.Context(), list)
	if err != nil {
		httperr.Internal(w, r, "failed to load meal labels", err)
		return
	}
	favoriteCategoriesByMeal, err := s.meals.FavoriteCategoriesForMeals(r.Context(), list)
	if err != nil {
		httperr.Internal(w, r, "failed to load meal favorite categories", err)
		return
	}
	allLabels, err := s.meals.ListLabels(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to list labels", err)
		return
	}
	if err := meals.ListPage(csrf.Token(r), list, labelsByMeal, favoriteCategoriesByMeal, allLabels, labelID).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render meals list", "error", err)
	}
}

func (s *Server) handleMealsSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	labelID, _ := uuid.Parse(r.URL.Query().Get("label_id"))

	list, err := s.searchMealsFiltered(r.Context(), q, labelID)
	if err != nil {
		httperr.Internal(w, r, "failed to search meals", err, "query", q)
		return
	}
	labelsByMeal, err := s.meals.LabelsForMeals(r.Context(), list)
	if err != nil {
		httperr.Internal(w, r, "failed to load meal labels", err)
		return
	}
	favoriteCategoriesByMeal, err := s.meals.FavoriteCategoriesForMeals(r.Context(), list)
	if err != nil {
		httperr.Internal(w, r, "failed to load meal favorite categories", err)
		return
	}

	if err := meals.Results(list, labelsByMeal, favoriteCategoriesByMeal).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render meal search results", "error", err)
	}
}

// searchMealsFiltered is the Meal Library's list/search logic: an optional
// label filter (uuid.Nil means "no filter") combined with an optional text
// query, covering all four combinations with the right underlying query
// rather than filtering label matches out of a full-library scan in Go.
func (s *Server) searchMealsFiltered(ctx context.Context, q string, labelID uuid.UUID) ([]sqlc.Meal, error) {
	switch {
	case labelID != uuid.Nil && q != "":
		return s.meals.SearchByLabel(ctx, labelID, q, defaultSearchLimit)
	case labelID != uuid.Nil:
		return s.meals.ListByLabel(ctx, labelID)
	case q != "":
		return s.meals.Search(ctx, q, defaultSearchLimit)
	default:
		return s.meals.List(ctx)
	}
}

func (s *Server) handleMealNewForm(w http.ResponseWriter, r *http.Request) {
	blank, err := s.meals.NewMeal(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to build new meal form", err)
		return
	}
	allLabels, err := s.meals.ListLabels(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to list meal labels", err)
		return
	}
	allCategories, err := s.meals.ListCategories(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to list meal categories", err)
		return
	}
	if err := meals.FormPage(csrf.Token(r), blank, allLabels, allCategories, false, "").Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render new meal form", "error", err)
	}
}

func (s *Server) handleMealEditForm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid meal id", http.StatusBadRequest)
		return
	}
	meal, err := s.meals.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		httperr.Internal(w, r, "failed to load meal for editing", err, "meal_id", id)
		return
	}
	allLabels, err := s.meals.ListLabels(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to list meal labels", err)
		return
	}
	allCategories, err := s.meals.ListCategories(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to list meal categories", err)
		return
	}
	if err := meals.FormPage(csrf.Token(r), meal, allLabels, allCategories, true, "").Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render edit meal form", "error", err)
	}
}

func (s *Server) handleMealCreate(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	display, input, err := s.buildMealForm(r)
	if err != nil {
		s.rerenderMealForm(w, r, display, false, err.Error())
		return
	}

	if _, err := s.meals.Create(r.Context(), user.ID, input); err != nil {
		slog.ErrorContext(r.Context(), "failed to create meal", "error", err)
		s.rerenderMealForm(w, r, display, false, "Couldn't save that meal — please try again.")
		return
	}

	http.Redirect(w, r, "/meals", http.StatusSeeOther)
}

func (s *Server) handleMealUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid meal id", http.StatusBadRequest)
		return
	}

	display, input, err := s.buildMealForm(r)
	display.ID = id // needed for the form's action URL regardless of outcome
	if err != nil {
		s.rerenderMealForm(w, r, display, true, err.Error())
		return
	}

	if err := s.meals.Update(r.Context(), id, input); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		slog.ErrorContext(r.Context(), "failed to update meal", "error", err, "meal_id", id)
		s.rerenderMealForm(w, r, display, true, "Couldn't save that meal — please try again.")
		return
	}

	http.Redirect(w, r, "/meals", http.StatusSeeOther)
}

func (s *Server) handleMealDelete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid meal id", http.StatusBadRequest)
		return
	}
	if err := s.meals.Delete(r.Context(), id); err != nil {
		if dberr.IsForeignKeyViolation(err) {
			slog.WarnContext(r.Context(), "meal delete blocked: still referenced elsewhere", "meal_id", id)
			http.Error(w, "This meal is still used in a day plan — remove it there first.", http.StatusConflict)
			return
		}
		httperr.Internal(w, r, "failed to delete meal", err, "meal_id", id)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) rerenderMealForm(w http.ResponseWriter, r *http.Request, meal mealslib.Meal, isEdit bool, errMsg string) {
	allLabels, err := s.meals.ListLabels(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to list meal labels", err)
		return
	}
	allCategories, err := s.meals.ListCategories(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to list meal categories", err)
		return
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	if err := meals.FormPage(csrf.Token(r), meal, allLabels, allCategories, isEdit, errMsg).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render meal form", "error", err)
	}
}

// buildMealForm reads the shared meal create/edit form fields, validating as
// it goes. It always returns a fully-populated display Meal reflecting
// exactly what the user submitted — including every nutrient value that DID
// parse successfully — so a validation failure on one field never discards
// the rest of what they typed. input/err are only meaningful when err is
// nil; a MealInput is not usable while any field failed validation.
func (s *Server) buildMealForm(r *http.Request) (mealslib.Meal, mealslib.MealInput, error) {
	if err := r.ParseForm(); err != nil {
		return mealslib.Meal{}, mealslib.MealInput{}, errors.New("couldn't read the submitted form")
	}

	var problems []string

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		problems = append(problems, "name is required")
	}

	recipeURL := strings.TrimSpace(r.FormValue("recipe_url"))
	if recipeURL != "" && !urlutil.IsAbsoluteHTTP(recipeURL) {
		problems = append(problems, "recipe URL must be an absolute http(s) URL")
	}

	servingLabel := strings.TrimSpace(r.FormValue("serving_label"))
	if servingLabel == "" {
		servingLabel = "per serving"
	}

	servingAmount, ok := parseFiniteFloat(r.FormValue("serving_amount"))
	if !ok || servingAmount <= 0 {
		problems = append(problems, "serving amount must be a positive number")
	}

	nutrients, err := s.meals.ListNutrients(r.Context())
	if err != nil {
		return mealslib.Meal{}, mealslib.MealInput{}, errors.New("couldn't load the nutrient list")
	}

	amounts := make(map[int16]float64, len(nutrients))
	displayValues := make([]mealslib.NutrientValue, 0, len(nutrients))
	for _, n := range nutrients {
		nv := mealslib.NutrientValue{NutrientID: n.ID, Key: n.Key, DisplayName: n.DisplayName, Unit: n.Unit}

		raw := strings.TrimSpace(r.FormValue("nutrient_" + strconv.Itoa(int(n.ID))))
		if raw != "" {
			v, ok := parseFiniteFloat(raw)
			switch {
			case !ok || v < 0:
				problems = append(problems, n.DisplayName+" must be a non-negative number")
			default:
				amounts[n.ID] = v
				nv.Amount = &v
			}
		}
		displayValues = append(displayValues, nv)
	}

	// Invalid/stale ids are silently dropped rather than failing the whole
	// form — a label picked up mid-submit and deleted by someone else is a
	// routine race, not something worth discarding an otherwise-valid meal
	// edit over.
	labelIDs := parseUUIDs(r.Form["label_ids"])
	allLabels, err := s.meals.ListLabels(r.Context())
	if err != nil {
		return mealslib.Meal{}, mealslib.MealInput{}, errors.New("couldn't load the label list")
	}
	byLabelID := make(map[uuid.UUID]sqlc.MealLabel, len(allLabels))
	for _, l := range allLabels {
		byLabelID[l.ID] = l
	}
	selectedLabels := make([]mealslib.Label, 0, len(labelIDs))
	for _, id := range labelIDs {
		if l, ok := byLabelID[id]; ok {
			selectedLabels = append(selectedLabels, mealslib.Label{ID: id, Name: l.Name, Color: l.Color})
		}
	}

	// Same "drop stale/invalid ids rather than failing the whole form" rule
	// as label_ids above.
	favoriteCategoryIDs := parseUUIDs(r.Form["favorite_category_ids"])
	allCategories, err := s.meals.ListCategories(r.Context())
	if err != nil {
		return mealslib.Meal{}, mealslib.MealInput{}, errors.New("couldn't load the category list")
	}
	byCategoryID := make(map[uuid.UUID]sqlc.MealCategory, len(allCategories))
	for _, c := range allCategories {
		byCategoryID[c.ID] = c
	}
	selectedFavoriteCategories := make([]mealslib.FavoriteCategory, 0, len(favoriteCategoryIDs))
	for _, id := range favoriteCategoryIDs {
		if c, ok := byCategoryID[id]; ok {
			selectedFavoriteCategories = append(selectedFavoriteCategories, mealslib.FavoriteCategory{ID: id, Name: c.Name})
		}
	}

	display := mealslib.Meal{
		Name:               name,
		RecipeURL:          recipeURL,
		ServingLabel:       servingLabel,
		ServingAmount:      servingAmount,
		FavoriteCategories: selectedFavoriteCategories,
		NutrientValues:     displayValues,
		Labels:             selectedLabels,
	}

	if len(problems) > 0 {
		return display, mealslib.MealInput{LabelIDs: labelIDs, FavoriteCategoryIDs: favoriteCategoryIDs}, errors.New(strings.Join(problems, "; "))
	}

	return display, mealslib.MealInput{
		Name:                name,
		RecipeURL:           recipeURL,
		ServingLabel:        servingLabel,
		ServingAmount:       servingAmount,
		FavoriteCategoryIDs: favoriteCategoryIDs,
		NutrientAmounts:     amounts,
		LabelIDs:            labelIDs,
	}, nil
}

func parseUUIDs(raw []string) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		if id, err := uuid.Parse(s); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// parseFiniteFloat is strconv.ParseFloat plus a finiteness check — bare
// ParseFloat happily accepts "NaN" and "Inf" with no error, and both compare
// false against every bound check a caller might apply afterward, letting
// non-finite values silently slip through numeric validation entirely.
func parseFiniteFloat(raw string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func (s *Server) handleCategoriesList(w http.ResponseWriter, r *http.Request) {
	cats, err := s.meals.ListCategories(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to list meal categories", err)
		return
	}
	if err := meals.CategoriesPage(csrf.Token(r), cats, "").Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render categories page", "error", err)
	}
}

func (s *Server) handleCategoryCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.rerenderCategories(w, r, "Category name is required")
		return
	}
	if _, err := s.meals.CreateCategory(r.Context(), name); err != nil {
		if dberr.IsUniqueViolation(err) {
			s.rerenderCategories(w, r, "A category named \""+name+"\" already exists.")
			return
		}
		slog.ErrorContext(r.Context(), "failed to create category", "error", err)
		s.rerenderCategories(w, r, "Couldn't create that category — please try again.")
		return
	}
	http.Redirect(w, r, "/meals/categories", http.StatusSeeOther)
}

func (s *Server) handleCategoryRename(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid category id", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.rerenderCategories(w, r, "Category name is required")
		return
	}
	if _, err := s.meals.RenameCategory(r.Context(), id, name); err != nil {
		if errors.Is(err, mealslib.ErrCategoryNotFound) {
			http.NotFound(w, r)
			return
		}
		if dberr.IsUniqueViolation(err) {
			s.rerenderCategories(w, r, "A category named \""+name+"\" already exists.")
			return
		}
		slog.ErrorContext(r.Context(), "failed to rename category", "error", err, "category_id", id)
		s.rerenderCategories(w, r, "Couldn't rename that category — please try again.")
		return
	}
	http.Redirect(w, r, "/meals/categories", http.StatusSeeOther)
}

func (s *Server) handleCategoryDelete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid category id", http.StatusBadRequest)
		return
	}
	if err := s.meals.DeleteCategory(r.Context(), id); err != nil {
		if dberr.IsForeignKeyViolation(err) {
			slog.WarnContext(r.Context(), "category delete blocked: still referenced elsewhere", "category_id", id)
			s.rerenderCategories(w, r, "Couldn't delete that category — it may still be used by a day plan.")
			return
		}
		httperr.Internal(w, r, "failed to delete category", err, "category_id", id)
		return
	}
	http.Redirect(w, r, "/meals/categories", http.StatusSeeOther)
}

func (s *Server) rerenderCategories(w http.ResponseWriter, r *http.Request, errMsg string) {
	cats, err := s.meals.ListCategories(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to list meal categories", err)
		return
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	if err := meals.CategoriesPage(csrf.Token(r), cats, errMsg).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render categories page", "error", err)
	}
}

func (s *Server) handleLabelsList(w http.ResponseWriter, r *http.Request) {
	labels, err := s.meals.ListLabels(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to list meal labels", err)
		return
	}
	if err := meals.LabelsPage(csrf.Token(r), labels, "").Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render labels page", "error", err)
	}
}

func (s *Server) handleLabelCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	color := r.FormValue("color")
	if name == "" {
		s.rerenderLabels(w, r, "Label name is required")
		return
	}
	if !mealslib.IsValidLabelColor(color) {
		s.rerenderLabels(w, r, "Choose a valid label color")
		return
	}
	if _, err := s.meals.CreateLabel(r.Context(), name, color); err != nil {
		if dberr.IsUniqueViolation(err) {
			s.rerenderLabels(w, r, "A label named \""+name+"\" already exists.")
			return
		}
		slog.ErrorContext(r.Context(), "failed to create label", "error", err)
		s.rerenderLabels(w, r, "Couldn't create that label — please try again.")
		return
	}
	http.Redirect(w, r, "/meals/labels", http.StatusSeeOther)
}

func (s *Server) handleLabelDelete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid label id", http.StatusBadRequest)
		return
	}
	if err := s.meals.DeleteLabel(r.Context(), id); err != nil {
		httperr.Internal(w, r, "failed to delete label", err, "label_id", id)
		return
	}
	http.Redirect(w, r, "/meals/labels", http.StatusSeeOther)
}

func (s *Server) rerenderLabels(w http.ResponseWriter, r *http.Request, errMsg string) {
	labels, err := s.meals.ListLabels(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to list meal labels", err)
		return
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	if err := meals.LabelsPage(csrf.Token(r), labels, errMsg).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render labels page", "error", err)
	}
}
