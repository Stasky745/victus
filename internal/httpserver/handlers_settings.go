package httpserver

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/csrf"

	"github.com/Stasky745/victus/internal/auth"
	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/goals"
	"github.com/Stasky745/victus/internal/httperr"
	"github.com/Stasky745/victus/internal/urlutil"
	"github.com/Stasky745/victus/web/templates/settings"
)

const dateDisplayLayout = "2006-01-02"

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	goalsList, err := s.goals.ListGoals(r.Context(), user.ID)
	if err != nil {
		httperr.Internal(w, r, "failed to load nutrient goals", err)
		return
	}
	infoURL, err := s.goals.InfoURL(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to load goal info url", err)
		return
	}
	usersSection, err := s.buildUsersSection(r.Context(), user, "")
	if err != nil {
		httperr.Internal(w, r, "failed to load users", err)
		return
	}
	s.renderSettings(w, r, rowsFromGoals(goalsList), infoURL, "", usersSection, http.StatusOK)
}

// handleSettingsCreateUser lets a password-mode admin add another account.
// Admin-gated inline (rather than via a dedicated middleware) since it's the
// only route that needs it — a 403 for any non-admin, same as an
// unauthorized action anywhere else in Victus.
func (s *Server) handleSettingsCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := auth.UserFromContext(ctx)
	if !user.IsAdmin {
		http.Error(w, "only admins can add users", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.rerenderSettingsWithUsersErr(w, r, user, "couldn't read the submitted form")
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	password := r.FormValue("password")

	if email == "" || password == "" {
		s.rerenderSettingsWithUsersErr(w, r, user, "email and password are required")
		return
	}
	if len(password) < 8 {
		s.rerenderSettingsWithUsersErr(w, r, user, "password must be at least 8 characters")
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		httperr.Internal(w, r, "failed to hash password", err)
		return
	}
	if _, err := s.queries.CreateUserWithPassword(ctx, sqlc.CreateUserWithPasswordParams{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: sql.NullString{String: hash, Valid: true},
		DisplayName:  sql.NullString{String: displayName, Valid: displayName != ""},
		IsAdmin:      false,
	}); err != nil {
		slog.ErrorContext(ctx, "failed to create user", "error", err)
		s.rerenderSettingsWithUsersErr(w, r, user, "couldn't create that user — the email may already be in use")
		return
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// buildUsersSection returns the admin-only "manage users" data, or nil when
// it doesn't apply (not password mode, or the viewer isn't an admin) — the
// template omits the whole section in that case.
func (s *Server) buildUsersSection(ctx context.Context, user *auth.User, createErr string) (*settings.UsersSection, error) {
	if !s.passwordAuth || !user.IsAdmin {
		return nil, nil
	}
	rows, err := s.queries.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	users := make([]settings.UserRow, len(rows))
	for i, u := range rows {
		users[i] = settings.UserRow{
			Email:       u.Email,
			DisplayName: u.DisplayName.String,
			IsAdmin:     u.IsAdmin,
			CreatedAt:   u.CreatedAt.Format(dateDisplayLayout),
		}
	}
	return &settings.UsersSection{Users: users, CreateErr: createErr}, nil
}

// rerenderSettingsWithUsersErr re-renders the full settings page (goals +
// info URL both re-fetched as-persisted, since this route doesn't touch
// them) with createErr surfaced in the Users section.
func (s *Server) rerenderSettingsWithUsersErr(w http.ResponseWriter, r *http.Request, user *auth.User, createErr string) {
	ctx := r.Context()
	goalsList, err := s.goals.ListGoals(ctx, user.ID)
	if err != nil {
		httperr.Internal(w, r, "failed to load nutrient goals", err)
		return
	}
	infoURL, err := s.goals.InfoURL(ctx)
	if err != nil {
		httperr.Internal(w, r, "failed to load goal info url", err)
		return
	}
	usersSection, err := s.buildUsersSection(ctx, user, createErr)
	if err != nil {
		httperr.Internal(w, r, "failed to load users", err)
		return
	}
	s.renderSettings(w, r, rowsFromGoals(goalsList), infoURL, "", usersSection, http.StatusUnprocessableEntity)
}

// handleSettingsUpdate saves the submitting user's own per-nutrient goal
// ranges. Deliberately a separate form/route from handleSettingsUpdateInfoURL
// — see goals.Store.SaveGoals's doc comment for why the two must never share
// one save.
func (s *Server) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	display, inputs, err := s.buildGoalsForm(r)
	if err != nil {
		s.rerenderSettingsErr(w, r, user, display, err.Error())
		return
	}

	if err := s.goals.SaveGoals(r.Context(), user.ID, inputs); err != nil {
		slog.ErrorContext(r.Context(), "failed to save nutrient goals", "error", err)
		s.rerenderSettingsErr(w, r, user, display, "Couldn't save your goals — please try again.")
		return
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// rerenderSettingsErr re-renders the settings page with rows exactly as
// submitted (so a validation failure never discards what the user typed)
// plus errMsg, re-fetching the info URL and users section fresh since this
// path doesn't touch either.
func (s *Server) rerenderSettingsErr(w http.ResponseWriter, r *http.Request, user *auth.User, rows []settings.Row, errMsg string) {
	infoURL, err := s.goals.InfoURL(r.Context())
	if err != nil {
		httperr.Internal(w, r, "failed to load goal info url", err)
		return
	}
	usersSection, err := s.buildUsersSection(r.Context(), user, "")
	if err != nil {
		httperr.Internal(w, r, "failed to load users", err)
		return
	}
	s.renderSettings(w, r, rows, infoURL, errMsg, usersSection, http.StatusUnprocessableEntity)
}

// handleSettingsUpdateInfoURL updates the instance-wide "how to set healthy
// targets" link. See goals.Store.SaveGoals's doc comment for why this is a
// separate route from handleSettingsUpdate.
func (s *Server) handleSettingsUpdateInfoURL(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	if err := r.ParseForm(); err != nil {
		s.rerenderSettingsWithGoals(w, r, user, "", "couldn't read the submitted form")
		return
	}
	infoURL := strings.TrimSpace(r.FormValue("info_url"))
	if infoURL == "" || !urlutil.IsAbsoluteHTTP(infoURL) {
		s.rerenderSettingsWithGoals(w, r, user, infoURL, "the healthy-targets link must be an absolute http(s) URL")
		return
	}

	if err := s.goals.SetInfoURL(r.Context(), infoURL); err != nil {
		slog.ErrorContext(r.Context(), "failed to save goal info url", "error", err)
		s.rerenderSettingsWithGoals(w, r, user, infoURL, "Couldn't save that link — please try again.")
		return
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// rerenderSettingsWithGoals re-fetches the current user's goal rows (this
// route doesn't touch them, so they're always the persisted, non-error
// values) and re-renders the full settings page with the submitted
// info-URL attempt and an error message.
func (s *Server) rerenderSettingsWithGoals(w http.ResponseWriter, r *http.Request, user *auth.User, infoURL, errMsg string) {
	goalsList, err := s.goals.ListGoals(r.Context(), user.ID)
	if err != nil {
		httperr.Internal(w, r, "failed to load nutrient goals", err)
		return
	}
	usersSection, err := s.buildUsersSection(r.Context(), user, "")
	if err != nil {
		httperr.Internal(w, r, "failed to load users", err)
		return
	}
	s.renderSettings(w, r, rowsFromGoals(goalsList), infoURL, errMsg, usersSection, http.StatusUnprocessableEntity)
}

// buildGoalsForm reads the goal-ranges form, validating as it goes. It
// always returns display rows reflecting exactly what the user submitted —
// including every field that DID parse successfully — so a validation
// failure on one nutrient's bound never discards what they typed for the
// rest. inputs/err are only meaningful when err is nil.
func (s *Server) buildGoalsForm(r *http.Request) ([]settings.Row, []goals.GoalInput, error) {
	if err := r.ParseForm(); err != nil {
		return nil, nil, errors.New("couldn't read the submitted form")
	}

	nutrients, err := s.goals.ListGoals(r.Context(), auth.UserFromContext(r.Context()).ID)
	if err != nil {
		return nil, nil, errors.New("couldn't load the nutrient list")
	}

	var problems []string
	rows := make([]settings.Row, 0, len(nutrients))
	inputs := make([]goals.GoalInput, 0, len(nutrients))
	for _, n := range nutrients {
		minRaw := strings.TrimSpace(r.FormValue("min_" + settings.NutrientIDStr(n.NutrientID)))
		maxRaw := strings.TrimSpace(r.FormValue("max_" + settings.NutrientIDStr(n.NutrientID)))
		idealMinRaw := strings.TrimSpace(r.FormValue("ideal_min_" + settings.NutrientIDStr(n.NutrientID)))
		idealMaxRaw := strings.TrimSpace(r.FormValue("ideal_max_" + settings.NutrientIDStr(n.NutrientID)))

		minVal, minOK := parseOptionalFiniteFloat(minRaw)
		maxVal, maxOK := parseOptionalFiniteFloat(maxRaw)
		idealMinVal, idealMinOK := parseOptionalFiniteFloat(idealMinRaw)
		idealMaxVal, idealMaxOK := parseOptionalFiniteFloat(idealMaxRaw)
		switch {
		case !minOK:
			problems = append(problems, n.DisplayName+" minimum must be a non-negative number")
		case !maxOK:
			problems = append(problems, n.DisplayName+" maximum must be a non-negative number")
		case !idealMinOK:
			problems = append(problems, n.DisplayName+" ideal minimum must be a non-negative number")
		case !idealMaxOK:
			problems = append(problems, n.DisplayName+" ideal maximum must be a non-negative number")
		case minVal != nil && maxVal != nil && *minVal > *maxVal:
			problems = append(problems, n.DisplayName+" minimum can't be greater than its maximum")
		case idealMinVal != nil && idealMaxVal != nil && *idealMinVal > *idealMaxVal:
			problems = append(problems, n.DisplayName+" ideal minimum can't be greater than its ideal maximum")
		case idealMinVal != nil && minVal != nil && *idealMinVal < *minVal:
			problems = append(problems, n.DisplayName+" ideal minimum can't be below the minimum")
		case idealMaxVal != nil && maxVal != nil && *idealMaxVal > *maxVal:
			problems = append(problems, n.DisplayName+" ideal maximum can't be above the maximum")
		}

		rows = append(rows, settings.Row{
			NutrientID:  n.NutrientID,
			DisplayName: n.DisplayName,
			Unit:        n.Unit,
			MinRaw:      minRaw,
			MaxRaw:      maxRaw,
			IdealMinRaw: idealMinRaw,
			IdealMaxRaw: idealMaxRaw,
		})
		inputs = append(inputs, goals.GoalInput{
			NutrientID: n.NutrientID,
			MinValue:   minVal, MaxValue: maxVal,
			IdealMin: idealMinVal, IdealMax: idealMaxVal,
		})
	}

	if len(problems) > 0 {
		return rows, nil, errors.New(strings.Join(problems, "; "))
	}
	return rows, inputs, nil
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, rows []settings.Row, infoURL, errMsg string, usersSection *settings.UsersSection, status int) {
	w.WriteHeader(status)
	if err := settings.Page(csrf.Token(r), rows, infoURL, errMsg, usersSection).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render settings page", "error", err)
	}
}

// parseOptionalFiniteFloat is parseFiniteFloat plus "blank means unset" and
// "must be non-negative": a goal bound is optional, unlike every other
// numeric form field Victus parses, so an empty string is valid input here
// (nil, ok) rather than a validation failure — but a negative bound isn't a
// meaningful nutrient goal, so it's rejected the same as a non-finite one.
func parseOptionalFiniteFloat(raw string) (*float64, bool) {
	if raw == "" {
		return nil, true
	}
	v, ok := parseFiniteFloat(raw)
	if !ok || v < 0 {
		return nil, false
	}
	return &v, true
}

// formatOptionalFloat renders a goal bound for an <input value="...">,
// blank when unset so the field starts empty rather than showing "0" —
// mirrors mealslib's formatAmount for the identical nil-means-unset case.
func formatOptionalFloat(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

func rowsFromGoals(goalsList []goals.Goal) []settings.Row {
	rows := make([]settings.Row, len(goalsList))
	for i, g := range goalsList {
		rows[i] = settings.Row{
			NutrientID:  g.NutrientID,
			DisplayName: g.DisplayName,
			Unit:        g.Unit,
			MinRaw:      formatOptionalFloat(g.MinValue),
			MaxRaw:      formatOptionalFloat(g.MaxValue),
			IdealMinRaw: formatOptionalFloat(g.IdealMin),
			IdealMaxRaw: formatOptionalFloat(g.IdealMax),
		}
	}
	return rows
}
