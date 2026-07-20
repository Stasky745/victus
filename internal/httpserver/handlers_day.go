package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/csrf"

	"github.com/Stasky745/victus/internal/auth"
	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/dberr"
	"github.com/Stasky745/victus/internal/httperr"
	"github.com/Stasky745/victus/internal/planning"
	"github.com/Stasky745/victus/web/templates/day"
)

const defaultDayItemQuantity = 1

// handleToday sends the browser to today's Day Builder — a stable,
// bookmarkable URL for "the current day" without needing a dedicated
// no-date route that duplicates handleDayBuilder's logic.
func (s *Server) handleToday(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/days/"+day.FormatDate(time.Now()), http.StatusSeeOther)
}

func (s *Server) handleDayBuilder(w http.ResponseWriter, r *http.Request) {
	date, err := parseDateParam(r)
	if err != nil {
		http.Error(w, "invalid date", http.StatusBadRequest)
		return
	}
	user := auth.UserFromContext(r.Context())

	d, err := s.planning.GetDay(r.Context(), user.ID, date)
	if err != nil {
		httperr.Internal(w, r, "failed to load day", err, "date", date)
		return
	}
	goalsList, err := s.goals.ListGoals(r.Context(), user.ID)
	if err != nil {
		httperr.Internal(w, r, "failed to load nutrient goals", err, "date", date)
		return
	}
	favorites, err := s.meals.ListFavorites(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to load favorite meals", err, "date", date)
		return
	}

	prev := day.FormatDate(date.AddDate(0, 0, -1))
	next := day.FormatDate(date.AddDate(0, 0, 1))
	if err := day.Page(csrf.Token(r), d, prev, next, goalsList, favorites).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render day builder", "error", err)
	}
}

func (s *Server) handleDayMealSearch(w http.ResponseWriter, r *http.Request) {
	dateStr := chi.URLParam(r, "date")
	if _, err := time.Parse(day.DateLayout, dateStr); err != nil {
		http.Error(w, "invalid date", http.StatusBadRequest)
		return
	}
	categoryID, err := uuid.Parse(r.URL.Query().Get("category_id"))
	if err != nil {
		http.Error(w, "invalid category id", http.StatusBadRequest)
		return
	}

	results, err := s.searchMealsForBuilder(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		httperr.Internal(w, r, "failed to search meals for day builder", err)
		return
	}

	// Render the canonical (uuid.Parse'd) form, not the raw query string —
	// uuid.Parse accepts non-canonical encodings (urn:uuid: prefix, braces,
	// undashed hex) that would otherwise survive into the hx-target/hx-vals
	// this fragment builds, producing an id htmx can never match.
	if err := day.SearchResults(dateStr, categoryID.String(), results).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render day meal search results", "error", err)
	}
}

// searchMealsForBuilder is the meal-search behavior shared by the Day and
// Week Builders' "add meal" search boxes: an empty query lists everything
// (browsing), a non-empty one searches by name.
func (s *Server) searchMealsForBuilder(ctx context.Context, q string) ([]sqlc.Meal, error) {
	if q == "" {
		return s.meals.List(ctx)
	}
	return s.meals.Search(ctx, q, defaultSearchLimit)
}

func (s *Server) handleDayAddItem(w http.ResponseWriter, r *http.Request) {
	date, err := parseDateParam(r)
	if err != nil {
		http.Error(w, "invalid date", http.StatusBadRequest)
		return
	}
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
	quantity := float64(defaultDayItemQuantity)
	if raw := r.FormValue("quantity"); raw != "" {
		v, ok := parseFiniteFloat(raw)
		if !ok || v <= 0 {
			http.Error(w, "quantity must be a positive number", http.StatusBadRequest)
			return
		}
		quantity = v
	}

	if err := s.planning.AddItem(r.Context(), user.ID, date, categoryID, mealID, quantity); err != nil {
		if dberr.IsForeignKeyViolation(err) {
			// A stale/forged meal_id or category_id — routine bad input, not
			// a server fault, so it shouldn't be logged or reported as one.
			http.Error(w, "that meal or category no longer exists", http.StatusBadRequest)
			return
		}
		httperr.Internal(w, r, "failed to add day plan item", err, "meal_id", mealID, "category_id", categoryID)
		return
	}

	// The Week Builder's "add meal" forms post here too, as plain (non-htmx)
	// submits — simpler than teaching every day column to understand day.templ's
	// htmx fragment shape. Redirecting to the week containing this date (rather
	// than trusting a client-supplied return URL) gets the right page back with
	// zero open-redirect surface, since day.MondayOf is a pure function of date.
	if r.Header.Get("HX-Request") != "true" {
		//nolint:gosec // not an open redirect: built entirely from day.FormatDate's
		// fixed YYYY-MM-DD output, never from user-controlled input.
		http.Redirect(w, r, "/weeks/"+day.FormatDate(day.MondayOf(date)), http.StatusSeeOther)
		return
	}

	s.renderCategoryAndSummary(w, r, user.ID, date, categoryID)
}

func (s *Server) handleDayUpdateItemQuantity(w http.ResponseWriter, r *http.Request) {
	date, err := parseDateParam(r)
	if err != nil {
		http.Error(w, "invalid date", http.StatusBadRequest)
		return
	}
	itemID, err := uuid.Parse(chi.URLParam(r, "item_id"))
	if err != nil {
		http.Error(w, "invalid item id", http.StatusBadRequest)
		return
	}
	user := auth.UserFromContext(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "couldn't read the submitted form", http.StatusBadRequest)
		return
	}
	// Zero isn't a meaningful quantity — Remove already covers "get rid of
	// this item", so treating 0 as valid here would just create a dead row
	// that contributes nothing and looks like a bug. Matches handleDayAddItem's
	// quantity <= 0 rule.
	quantity, ok := parseFiniteFloat(r.FormValue("quantity"))
	if !ok || quantity <= 0 {
		http.Error(w, "quantity must be a positive number", http.StatusBadRequest)
		return
	}

	if err := s.planning.UpdateItemQuantity(r.Context(), user.ID, itemID, quantity); err != nil {
		s.handlePlanningError(w, r, err, "failed to update item quantity", itemID)
		return
	}

	s.renderSummary(w, r, user.ID, date)
}

func (s *Server) handleDayRemoveItem(w http.ResponseWriter, r *http.Request) {
	date, err := parseDateParam(r)
	if err != nil {
		http.Error(w, "invalid date", http.StatusBadRequest)
		return
	}
	itemID, err := uuid.Parse(chi.URLParam(r, "item_id"))
	if err != nil {
		http.Error(w, "invalid item id", http.StatusBadRequest)
		return
	}
	user := auth.UserFromContext(r.Context())

	if err := s.planning.RemoveItem(r.Context(), user.ID, itemID); err != nil {
		s.handlePlanningError(w, r, err, "failed to remove item", itemID)
		return
	}

	s.renderSummary(w, r, user.ID, date)
}

// renderCategoryAndSummary re-fetches the day and renders the affected
// category's item list (the primary response, swapped into
// #category-{id}-items) plus the day summary as an out-of-band swap — used
// after adding an item, since both the item list and the totals change.
func (s *Server) renderCategoryAndSummary(w http.ResponseWriter, r *http.Request, userID uuid.UUID, date time.Time, categoryID uuid.UUID) {
	d, err := s.planning.GetDay(r.Context(), userID, date)
	if err != nil {
		httperr.Internal(w, r, "failed to reload day", err)
		return
	}
	goalsList, err := s.goals.ListGoals(r.Context(), userID)
	if err != nil {
		httperr.Internal(w, r, "failed to load nutrient goals", err)
		return
	}
	section, found := findCategorySection(d.Categories, categoryID)
	if !found {
		// AddItem's FK guarantees the category existed at commit time, so
		// this only fires on a genuine TOCTOU race (category deleted in the
		// instant between commit and this re-fetch) — vanishingly rare, but
		// rendering nothing here would mean htmx's hx-swap="outerHTML" on
		// the Add button replaces #category-{id}-items with an empty
		// response, deleting it from the DOM and permanently desyncing the
		// UI. Render an empty-but-valid fragment for the same id instead.
		slog.WarnContext(r.Context(), "category missing from day view right after adding to it", "category_id", categoryID)
		section = planning.CategorySection{Category: sqlc.MealCategory{ID: categoryID}}
	}
	if err := day.CategoryItems(day.FormatDate(date), section).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render category items", "error", err)
		return
	}
	if err := day.Summary(d, goalsList).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render day summary", "error", err)
		return
	}
	// Resets the search-results dropdown the "Add" click came from back to
	// its favorites quick-add state — left alone, the just-added meal's Add
	// button stays visible and clickable, and a second click would insert a
	// duplicate item.
	favorites, err := s.meals.ListFavorites(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to load favorite meals", err)
		return
	}
	if err := day.ResetSearchResults(day.FormatDate(date), categoryID.String(), favorites).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render search results reset", "error", err)
	}
}

func findCategorySection(sections []planning.CategorySection, categoryID uuid.UUID) (planning.CategorySection, bool) {
	for _, section := range sections {
		if section.Category.ID == categoryID {
			return section, true
		}
	}
	return planning.CategorySection{}, false
}

// renderSummary re-fetches the day and renders only the OOB summary — used
// after a quantity change or removal, where the triggering element handles
// its own DOM update (hx-swap="none" or "delete") and only the totals need
// refreshing elsewhere on the page.
func (s *Server) renderSummary(w http.ResponseWriter, r *http.Request, userID uuid.UUID, date time.Time) {
	d, err := s.planning.GetDay(r.Context(), userID, date)
	if err != nil {
		httperr.Internal(w, r, "failed to reload day", err)
		return
	}
	goalsList, err := s.goals.ListGoals(r.Context(), userID)
	if err != nil {
		httperr.Internal(w, r, "failed to load nutrient goals", err)
		return
	}
	if err := day.Summary(d, goalsList).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render day summary", "error", err)
	}
}

func (s *Server) handlePlanningError(w http.ResponseWriter, r *http.Request, err error, msg string, itemID uuid.UUID) {
	if errors.Is(err, planning.ErrNotOwner) || errors.Is(err, planning.ErrItemNotFound) {
		slog.WarnContext(r.Context(), "rejected day plan item mutation", "item_id", itemID, "reason", err)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	httperr.Internal(w, r, msg, err, "item_id", itemID)
}

func parseDateParam(r *http.Request) (time.Time, error) {
	return time.Parse(day.DateLayout, chi.URLParam(r, "date"))
}
