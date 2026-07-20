package httpserver

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/csrf"

	"github.com/Stasky745/victus/internal/auth"
	"github.com/Stasky745/victus/internal/exportimport"
	"github.com/Stasky745/victus/internal/httperr"
	"github.com/Stasky745/victus/web/templates/settings"
)

// maxImportFileSize bounds the uploaded export file — comfortably above any
// real Victus export, guarding against an accidental or malicious huge
// upload rather than a real size need.
const maxImportFileSize = 10 << 20 // 10MB

// handleExport builds the requested sections into a downloadable JSON file.
// A plain (non-htmx) form POST — the browser's own "Save As" handles the
// actual download-to-disk step, which works the same whether Victus itself
// is running in a container or not.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "couldn't read the submitted form", http.StatusUnprocessableEntity)
		return
	}
	user := auth.UserFromContext(r.Context())
	sel := exportimport.Selection{
		MealCategories: r.FormValue("meal_categories") == "true",
		MealLabels:     r.FormValue("meal_labels") == "true",
		Meals:          r.FormValue("meals") == "true",
		Goals:          r.FormValue("goals") == "true",
		DefaultDay:     r.FormValue("default_day") == "true",
		DayPlans:       r.FormValue("day_plans") == "true",
		AppSettings:    r.FormValue("app_settings") == "true",
	}
	if sel == (exportimport.Selection{}) {
		http.Error(w, "select at least one thing to export", http.StatusBadRequest)
		return
	}

	data, err := s.exportimport.Export(r.Context(), user.ID, sel)
	if err != nil {
		httperr.Internal(w, r, "failed to build export", err)
		return
	}
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		httperr.Internal(w, r, "failed to marshal export", err)
		return
	}

	filename := "victus-export-" + time.Now().Format("2006-01-02") + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, _ = w.Write(body)
}

// handleImport reads an uploaded export file and applies it, then renders a
// result summary — not a redirect, since the counts are worth actually
// showing and this codebase has no flash-message mechanism to thread them
// through one.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, maxImportFileSize)
	if err := r.ParseMultipartForm(maxImportFileSize); err != nil { //nolint:gosec // bounded by the MaxBytesReader wrap above, gosec can't see through it
		http.Error(w, "the uploaded file is too large, or the form couldn't be read", http.StatusRequestEntityTooLarge)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "no file was uploaded", http.StatusBadRequest)
		return
	}
	defer func() { _ = file.Close() }()

	var data exportimport.Export
	if err := json.NewDecoder(file).Decode(&data); err != nil {
		http.Error(w, "that file isn't valid Victus export JSON", http.StatusUnprocessableEntity)
		return
	}

	result, err := s.exportimport.Import(r.Context(), user.ID, data)
	if err != nil {
		if errors.Is(err, exportimport.ErrInvalidData) {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		httperr.Internal(w, r, "failed to import data", err)
		return
	}

	if err := settings.ImportResultPage(csrf.Token(r), toImportResultView(result)).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render import result", "error", err)
	}
}

// toImportResultView adapts exportimport.ImportResult to the template
// package's own view type, keeping web/templates independent of
// internal/exportimport's Go types.
func toImportResultView(r exportimport.ImportResult) settings.ImportResult {
	return settings.ImportResult{
		CategoriesCreated:      r.CategoriesCreated,
		CategoriesUnchanged:    r.CategoriesUnchanged,
		LabelsCreated:          r.LabelsCreated,
		LabelsUpdated:          r.LabelsUpdated,
		MealsCreated:           r.MealsCreated,
		MealsUpdated:           r.MealsUpdated,
		GoalsSet:               r.GoalsSet,
		DefaultDayItemsAdded:   r.DefaultDayItemsAdded,
		DefaultDayItemsSkipped: r.DefaultDayItemsSkipped,
		DayPlanItemsAdded:      r.DayPlanItemsAdded,
		DayPlanItemsSkipped:    r.DayPlanItemsSkipped,
		AppSettingsSet:         r.AppSettingsSet,
		Warnings:               r.Warnings,
	}
}
