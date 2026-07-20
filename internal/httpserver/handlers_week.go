package httpserver

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/csrf"

	"github.com/Stasky745/victus/internal/auth"
	"github.com/Stasky745/victus/internal/httperr"
	"github.com/Stasky745/victus/internal/planning"
	"github.com/Stasky745/victus/web/templates/day"
	"github.com/Stasky745/victus/web/templates/week"
)

// handleWeekToday sends the browser to the Monday-anchored week containing
// today — the /weeks equivalent of handleToday.
func (s *Server) handleWeekToday(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/weeks/"+day.FormatDate(day.MondayOf(time.Now())), http.StatusSeeOther)
}

func (s *Server) handleWeekBuilder(w http.ResponseWriter, r *http.Request) {
	requested, err := time.Parse(day.DateLayout, chi.URLParam(r, "week_start"))
	if err != nil {
		http.Error(w, "invalid date", http.StatusBadRequest)
		return
	}
	// Always land on the canonical Monday for whatever week the requested
	// date falls in — forgiving of a non-Monday URL (e.g. hand-typed or an
	// old bookmark) rather than rejecting it outright.
	weekStart := day.MondayOf(requested)
	if !weekStart.Equal(requested) {
		//nolint:gosec // not an open redirect: day.FormatDate always emits a fixed YYYY-MM-DD
		// shape (no scheme/host/slashes possible), so this can only ever redirect same-origin.
		http.Redirect(w, r, "/weeks/"+day.FormatDate(weekStart), http.StatusSeeOther)
		return
	}

	user := auth.UserFromContext(r.Context())
	wk, err := s.planning.GetWeek(r.Context(), user.ID, weekStart)
	if err != nil {
		httperr.Internal(w, r, "failed to load week", err, "week_start", weekStart)
		return
	}
	goalsList, err := s.goals.ListGoals(r.Context(), user.ID)
	if err != nil {
		httperr.Internal(w, r, "failed to load nutrient goals", err, "week_start", weekStart)
		return
	}
	categories, err := s.meals.ListCategories(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to load meal categories", err, "week_start", weekStart)
		return
	}

	prev := day.FormatDate(weekStart.AddDate(0, 0, -planning.WeekLength))
	next := day.FormatDate(weekStart.AddDate(0, 0, planning.WeekLength))
	if err := week.Page(csrf.Token(r), wk, prev, next, goalsList, categories).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render week builder", "error", err)
	}
}

// handleWeekMealSearch is the Week Builder's equivalent of
// handleDayMealSearch — same underlying meal search, but rendering
// week.SearchResults (plain-form Add buttons, since a week day column has
// none of the Day Builder's htmx-target scaffolding) instead of
// day.SearchResults.
func (s *Server) handleWeekMealSearch(w http.ResponseWriter, r *http.Request) {
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
		httperr.Internal(w, r, "failed to search meals for week builder", err)
		return
	}

	// Render the canonical (uuid.Parse'd) form, matching handleDayMealSearch's
	// reasoning: a non-canonical id surviving into the hidden category_id
	// field would still round-trip correctly to AddItem, but keeping both
	// search handlers consistent is one less thing to reason about.
	if err := week.SearchResults(dateStr, csrf.Token(r), categoryID.String(), results).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render week meal search results", "error", err)
	}
}

func (s *Server) handleWeekCopyDay(w http.ResponseWriter, r *http.Request) {
	weekStart, err := time.Parse(day.DateLayout, chi.URLParam(r, "week_start"))
	if err != nil {
		http.Error(w, "invalid date", http.StatusBadRequest)
		return
	}
	user := auth.UserFromContext(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "couldn't read the submitted form", http.StatusBadRequest)
		return
	}
	sourceDate, err := time.Parse(day.DateLayout, r.FormValue("source_date"))
	if err != nil {
		http.Error(w, "invalid source date", http.StatusBadRequest)
		return
	}

	targetDates := make([]time.Time, 0, len(r.Form["target_dates"]))
	for _, raw := range r.Form["target_dates"] {
		t, err := time.Parse(day.DateLayout, raw)
		if err != nil {
			http.Error(w, "invalid target date", http.StatusBadRequest)
			return
		}
		targetDates = append(targetDates, t)
	}
	if len(targetDates) == 0 {
		http.Error(w, "select at least one day to copy to", http.StatusBadRequest)
		return
	}

	if err := s.planning.CopyDay(r.Context(), user.ID, sourceDate, targetDates); err != nil {
		httperr.Internal(w, r, "failed to copy day", err, "source_date", sourceDate)
		return
	}

	//nolint:gosec // not an open redirect: day.FormatDate always emits a fixed YYYY-MM-DD
	// shape (no scheme/host/slashes possible), so this can only ever redirect same-origin.
	http.Redirect(w, r, "/weeks/"+day.FormatDate(weekStart), http.StatusSeeOther)
}
