package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/csrf"

	"github.com/Stasky745/victus/internal/auth"
	"github.com/Stasky745/victus/internal/dberr"
	"github.com/Stasky745/victus/internal/httperr"
	"github.com/Stasky745/victus/internal/planning"
	"github.com/Stasky745/victus/web/templates/defaults"
)

func (s *Server) handleDefaultsPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	sections, err := s.planning.GetDefaultDay(r.Context(), user.ID)
	if err != nil {
		httperr.Internal(w, r, "failed to load default day", err)
		return
	}
	favorites, err := s.meals.ListFavorites(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to load favorite meals", err)
		return
	}
	if err := defaults.Page(csrf.Token(r), sections, favorites).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render default day page", "error", err)
	}
}

func (s *Server) handleDefaultsMealSearch(w http.ResponseWriter, r *http.Request) {
	categoryID, err := uuid.Parse(r.URL.Query().Get("category_id"))
	if err != nil {
		http.Error(w, "invalid category id", http.StatusBadRequest)
		return
	}
	results, err := s.searchMealsForBuilder(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		httperr.Internal(w, r, "failed to search meals for default day", err)
		return
	}
	if err := defaults.SearchResults(categoryID.String(), results).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render default day search results", "error", err)
	}
}

func (s *Server) handleDefaultsAddItem(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "couldn't read the submitted form", http.StatusBadRequest)
		return
	}
	mealID, err := uuid.Parse(r.FormValue("meal_id"))
	if err != nil {
		http.Error(w, "invalid meal id", http.StatusBadRequest)
		return
	}
	categoryID, err := uuid.Parse(r.FormValue("category_id"))
	if err != nil {
		http.Error(w, "invalid category id", http.StatusBadRequest)
		return
	}

	if err := s.planning.AddDefaultItem(r.Context(), user.ID, categoryID, mealID, defaultDayItemQuantity); err != nil {
		if dberr.IsForeignKeyViolation(err) {
			http.Error(w, "that meal or category no longer exists", http.StatusBadRequest)
			return
		}
		httperr.Internal(w, r, "failed to add default day item", err, "meal_id", mealID, "category_id", categoryID)
		return
	}

	s.renderDefaultsCategory(w, r, user.ID, categoryID)
}

func (s *Server) handleDefaultsRemoveItem(w http.ResponseWriter, r *http.Request) {
	itemID, err := uuid.Parse(chi.URLParam(r, "item_id"))
	if err != nil {
		http.Error(w, "invalid item id", http.StatusBadRequest)
		return
	}
	user := auth.UserFromContext(r.Context())

	if err := s.planning.RemoveDefaultItem(r.Context(), user.ID, itemID); err != nil {
		if errors.Is(err, planning.ErrItemNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		httperr.Internal(w, r, "failed to remove default day item", err, "item_id", itemID)
		return
	}
	// hx-swap="delete" on the triggering button just removes its own <li>;
	// no response body needed.
	w.WriteHeader(http.StatusOK)
}

// renderDefaultsCategory re-fetches the Default Day and renders the
// affected category's item list plus a search-results clear — the Default
// Day equivalent of renderCategoryAndSummary, minus the summary (a
// template has no nutrient totals to show).
func (s *Server) renderDefaultsCategory(w http.ResponseWriter, r *http.Request, userID uuid.UUID, categoryID uuid.UUID) {
	sections, err := s.planning.GetDefaultDay(r.Context(), userID)
	if err != nil {
		httperr.Internal(w, r, "failed to reload default day", err)
		return
	}
	section, found := findCategorySection(sections, categoryID)
	if !found {
		slog.WarnContext(r.Context(), "category missing from default day right after adding to it", "category_id", categoryID)
		return
	}
	if err := defaults.CategoryItems(section).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render default day category items", "error", err)
		return
	}
	favorites, err := s.meals.ListFavorites(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to load favorite meals", err)
		return
	}
	if err := defaults.ResetSearchResults(categoryID.String(), favorites).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render default day search results reset", "error", err)
	}
}
