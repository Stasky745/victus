// Package mealslib is the business-logic layer for Victus's shared meal
// library: CRUD over meals + their per-nutrient values, fuzzy name search,
// and meal-category management. HTTP handlers should stay thin and call into
// this package rather than talking to sqlc directly, so the transactional
// multi-step writes (a meal plus its nutrient rows) live in one tested place.
package mealslib

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/db/sqlc"
)

const defaultListLimit = 200

// ErrCategoryNotFound is returned when a category id doesn't exist —
// distinct from a generic error so callers can map it to a 404 rather than
// a 500 or a misleading "please try again".
var ErrCategoryNotFound = errors.New("meal category not found")

// LabelColors is the fixed palette a meal label's color must be one of —
// deliberately not a free-form hex value, so every badge in the UI can map
// straight to a pre-built vx-badge-{color} CSS class (web/static/input.css)
// instead of interpolating arbitrary, unvalidated color strings into markup.
var LabelColors = []string{"red", "green", "blue", "purple", "amber", "gray"}

// IsValidLabelColor reports whether color is one of LabelColors.
func IsValidLabelColor(color string) bool {
	for _, c := range LabelColors {
		if c == color {
			return true
		}
	}
	return false
}

// Store is the meal-library repository.
type Store struct {
	db     *sql.DB
	driver string
	q      sqlc.Querier
}

// New returns a Store backed by sqlDB for driver ("postgres" or "sqlite").
func New(sqlDB *sql.DB, driver string) (*Store, error) {
	q, err := db.NewQuerier(driver, sqlDB)
	if err != nil {
		return nil, err
	}
	return &Store{db: sqlDB, driver: driver, q: q}, nil
}

// withTx runs fn inside one transaction: begin, fn(a Querier bound to the
// tx), commit — with the tx always rolled back on any early return,
// including fn's own error. Shared by every multi-statement write
// (Create/Update/Import all touch a meal row plus its nutrient-value rows)
// so the same begin/rollback/commit skeleton can't drift between them.
func (s *Store) withTx(ctx context.Context, fn func(q sqlc.Querier) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	q, err := db.NewTxQuerier(s.driver, tx)
	if err != nil {
		return err
	}
	if err := fn(q); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// NutrientValue is one row of a meal's nutrient breakdown. Amount is nil
// when the meal has no recorded value for that nutrient (not the same as
// an explicit zero).
type NutrientValue struct {
	NutrientID  int16
	Key         string
	DisplayName string
	Unit        string
	Amount      *float64
}

// Meal is a fully hydrated meal-library entry, including every known
// nutrient (set or not) so templates can render a stable set of fields.
type Meal struct {
	ID             uuid.UUID
	Name           string
	Source         string
	RecipeURL      string
	ServingLabel   string
	ServingAmount  float64
	IsFavorite     bool
	NutrientValues []NutrientValue
	Labels         []Label
}

// MealInput is what a create/update form submits. NutrientAmounts should
// only contain nutrients the user actually filled in; Update clears and
// re-sets every nutrient value from this map, so omitting a nutrient here
// removes it from the meal. LabelIDs works the same way for labels: Update
// replaces the full set from this slice.
type MealInput struct {
	Name            string
	RecipeURL       string
	ServingLabel    string
	ServingAmount   float64
	IsFavorite      bool
	NutrientAmounts map[int16]float64
	LabelIDs        []uuid.UUID
}

// Label is one meal label/tag — shared across the instance (like a meal
// category), not per-user.
type Label struct {
	ID    uuid.UUID
	Name  string
	Color string
}

// ListNutrients returns the full seeded nutrient registry, in display order.
func (s *Store) ListNutrients(ctx context.Context) ([]sqlc.Nutrient, error) {
	return s.q.ListNutrients(ctx)
}

// NutrientIDsByKey returns a lookup from nutrient key (e.g. "calories") to
// its database id — the bridge between an importer's key-based nutrient
// amounts (internal/importers/mealie, internal/importers/openfoodfacts, both
// deliberately independent of Victus's DB schema) and ImportInput's
// id-based NutrientAmounts.
func (s *Store) NutrientIDsByKey(ctx context.Context) (map[string]int16, error) {
	nutrients, err := s.q.ListNutrients(ctx)
	if err != nil {
		return nil, fmt.Errorf("list nutrients: %w", err)
	}
	out := make(map[string]int16, len(nutrients))
	for _, n := range nutrients {
		out[n.Key] = n.ID
	}
	return out, nil
}

// ListCategories returns all meal categories, in display order.
func (s *Store) ListCategories(ctx context.Context) ([]sqlc.MealCategory, error) {
	return s.q.ListMealCategories(ctx)
}

// CreateCategory appends a new category at the end of the display order.
func (s *Store) CreateCategory(ctx context.Context, name string) (sqlc.MealCategory, error) {
	existing, err := s.q.ListMealCategories(ctx)
	if err != nil {
		return sqlc.MealCategory{}, fmt.Errorf("list categories: %w", err)
	}
	return s.q.CreateMealCategory(ctx, sqlc.CreateMealCategoryParams{
		ID:        uuid.New(),
		Name:      name,
		SortOrder: int16(len(existing) + 1), //nolint:gosec // category counts are tiny, never near int16 overflow
	})
}

// RenameCategory updates a category's name, keeping its sort order.
func (s *Store) RenameCategory(ctx context.Context, id uuid.UUID, name string) (sqlc.MealCategory, error) {
	current, err := s.getCategory(ctx, id)
	if err != nil {
		return sqlc.MealCategory{}, err
	}
	updated, err := s.q.UpdateMealCategory(ctx, sqlc.UpdateMealCategoryParams{
		ID:        id,
		Name:      name,
		SortOrder: current.SortOrder,
	})
	if err != nil {
		// The existence check above is a separate, non-transactional read —
		// if the category was deleted in the window between that check and
		// this UPDATE, surface the same ErrCategoryNotFound as a missing id
		// caught upfront, rather than leaking sql.ErrNoRows to the caller.
		if errors.Is(err, sql.ErrNoRows) {
			return sqlc.MealCategory{}, ErrCategoryNotFound
		}
		return sqlc.MealCategory{}, err
	}
	return updated, nil
}

// DeleteCategory removes a category. It fails if any day-plan item still
// references it (foreign key), which is the correct behavior — the caller
// should surface that as "category is in use", not swallow the error.
func (s *Store) DeleteCategory(ctx context.Context, id uuid.UUID) error {
	return s.q.DeleteMealCategory(ctx, id)
}

func (s *Store) getCategory(ctx context.Context, id uuid.UUID) (sqlc.MealCategory, error) {
	cats, err := s.q.ListMealCategories(ctx)
	if err != nil {
		return sqlc.MealCategory{}, err
	}
	for _, c := range cats {
		if c.ID == id {
			return c, nil
		}
	}
	return sqlc.MealCategory{}, ErrCategoryNotFound
}

// List returns up to defaultListLimit meals, alphabetically — the default
// (no search query yet) view of the meal library.
func (s *Store) List(ctx context.Context) ([]sqlc.Meal, error) {
	return s.q.ListMeals(ctx, defaultListLimit)
}

// Search returns meals whose name fuzzy-matches query, ranked by similarity.
func (s *Store) Search(ctx context.Context, query string, limit int32) ([]sqlc.Meal, error) {
	return s.q.SearchMeals(ctx, sqlc.SearchMealsParams{Query: s.searchMatchArg(query), LimitCount: limit})
}

// ListByLabel is List scoped to meals carrying labelID.
func (s *Store) ListByLabel(ctx context.Context, labelID uuid.UUID) ([]sqlc.Meal, error) {
	return s.q.ListMealsByLabel(ctx, sqlc.ListMealsByLabelParams{
		LabelID:    labelID,
		LimitCount: defaultListLimit,
	})
}

// SearchByLabel is Search scoped to meals carrying labelID.
func (s *Store) SearchByLabel(ctx context.Context, labelID uuid.UUID, query string, limit int32) ([]sqlc.Meal, error) {
	return s.q.SearchMealsByLabel(ctx, sqlc.SearchMealsByLabelParams{
		LabelID:    labelID,
		Query:      s.searchMatchArg(query),
		LimitCount: limit,
	})
}

// searchMatchArg builds the SearchMeals/SearchMealsByLabel "query" argument
// for whichever backend is active. Postgres's word_similarity() takes the
// raw search text directly. SQLite has no pg_trgm equivalent — search goes
// through an FTS5 virtual table with the trigram tokenizer instead, which
// (unhelped) ANDs together the query's own trigram tokens and so finds
// nothing for a typo'd query sharing only some of them; trigramMatchExpr
// builds an OR-joined MATCH expression instead, giving comparable
// typo-tolerant ranking via bm25().
func (s *Store) searchMatchArg(query string) string {
	if s.driver == "sqlite" {
		return trigramMatchExpr(query)
	}
	return query
}

// trigramMatchExpr splits query into lowercase 3-character trigrams and
// joins them with FTS5's OR operator, each quoted as a string literal so it
// matches literally rather than being parsed as FTS5 query syntax. The
// result is a value bound via a query parameter (never string-concatenated
// into SQL), so there's no injection risk from quote characters in query.
func trigramMatchExpr(query string) string {
	runes := []rune(strings.ToLower(query))
	if len(runes) < 3 {
		return quoteFTS5(string(runes))
	}
	terms := make([]string, 0, len(runes)-2)
	for i := 0; i+3 <= len(runes); i++ {
		terms = append(terms, quoteFTS5(string(runes[i:i+3])))
	}
	return strings.Join(terms, " OR ")
}

// quoteFTS5 wraps s as an FTS5 string literal, doubling any embedded
// double-quote per FTS5's escaping rule.
func quoteFTS5(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// ListFavorites returns every meal marked as a favorite, alphabetically —
// shown as one-click "quick add" buttons on the Day/Week/Default Day builder
// pages, in place of having to type a search for meals used constantly.
func (s *Store) ListFavorites(ctx context.Context) ([]sqlc.Meal, error) {
	return s.q.ListFavoriteMeals(ctx)
}

// ToggleFavorite flips a meal's favorite status and returns the updated row.
func (s *Store) ToggleFavorite(ctx context.Context, id uuid.UUID) (sqlc.Meal, error) {
	m, err := s.q.ToggleMealFavorite(ctx, id)
	if err != nil {
		return sqlc.Meal{}, fmt.Errorf("toggle meal favorite: %w", err)
	}
	return m, nil
}

// ListLabels returns every meal label, in display order.
func (s *Store) ListLabels(ctx context.Context) ([]sqlc.MealLabel, error) {
	return s.q.ListMealLabels(ctx)
}

// CreateLabel appends a new label at the end of the display order. color
// must be one of LabelColors — callers should validate with
// IsValidLabelColor before calling.
func (s *Store) CreateLabel(ctx context.Context, name, color string) (sqlc.MealLabel, error) {
	existing, err := s.q.ListMealLabels(ctx)
	if err != nil {
		return sqlc.MealLabel{}, fmt.Errorf("list labels: %w", err)
	}
	return s.q.CreateMealLabel(ctx, sqlc.CreateMealLabelParams{
		ID:        uuid.New(),
		Name:      name,
		Color:     color,
		SortOrder: int16(len(existing) + 1), //nolint:gosec // label counts are tiny, never near int16 overflow
	})
}

// DeleteLabel removes a label — its assignments cascade (ON DELETE CASCADE
// on meal_label_assignments), so no meal is left referencing a dangling id.
func (s *Store) DeleteLabel(ctx context.Context, id uuid.UUID) error {
	return s.q.DeleteMealLabel(ctx, id)
}

// labelsForMeals batch-fetches every label assigned to any of mealIDs,
// grouped by meal id — one query regardless of how many meals are being
// rendered, instead of one query per meal.
func (s *Store) labelsForMeals(ctx context.Context, mealIDs []uuid.UUID) (map[uuid.UUID][]Label, error) {
	if len(mealIDs) == 0 {
		return nil, nil
	}
	rows, err := s.q.ListMealLabelsForMeals(ctx, mealIDs)
	if err != nil {
		return nil, fmt.Errorf("list meal labels for meals: %w", err)
	}
	out := make(map[uuid.UUID][]Label, len(mealIDs))
	for _, row := range rows {
		out[row.MealID] = append(out[row.MealID], Label{ID: row.ID, Name: row.Name, Color: row.Color})
	}
	return out, nil
}

// LabelsForMeal returns the labels assigned to a single meal.
func (s *Store) LabelsForMeal(ctx context.Context, mealID uuid.UUID) ([]Label, error) {
	byMeal, err := s.labelsForMeals(ctx, []uuid.UUID{mealID})
	if err != nil {
		return nil, err
	}
	return byMeal[mealID], nil
}

// LabelsForMeals batch-fetches labels for every meal in mealsList — the
// Meal Library list page's badges, in one query regardless of list length.
func (s *Store) LabelsForMeals(ctx context.Context, mealsList []sqlc.Meal) (map[uuid.UUID][]Label, error) {
	ids := make([]uuid.UUID, len(mealsList))
	for i, m := range mealsList {
		ids[i] = m.ID
	}
	return s.labelsForMeals(ctx, ids)
}

// NewMeal returns a blank Meal pre-populated with every known nutrient
// (Amount nil), suitable for rendering an empty "create meal" form with the
// same shape as an edited one.
func (s *Store) NewMeal(ctx context.Context) (Meal, error) {
	nutrients, err := s.q.ListNutrients(ctx)
	if err != nil {
		return Meal{}, fmt.Errorf("list nutrients: %w", err)
	}
	nvs := make([]NutrientValue, 0, len(nutrients))
	for _, n := range nutrients {
		nvs = append(nvs, NutrientValue{NutrientID: n.ID, Key: n.Key, DisplayName: n.DisplayName, Unit: n.Unit})
	}
	return Meal{ServingLabel: "per serving", ServingAmount: 1, NutrientValues: nvs}, nil
}

// Get returns a single meal hydrated with every nutrient's value (or nil).
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Meal, error) {
	m, err := s.q.GetMeal(ctx, id)
	if err != nil {
		return Meal{}, fmt.Errorf("get meal: %w", err)
	}
	return s.hydrate(ctx, m)
}

func (s *Store) hydrate(ctx context.Context, m sqlc.Meal) (Meal, error) {
	nutrients, err := s.q.ListNutrients(ctx)
	if err != nil {
		return Meal{}, fmt.Errorf("list nutrients: %w", err)
	}
	values, err := s.q.ListMealNutrientValues(ctx, m.ID)
	if err != nil {
		return Meal{}, fmt.Errorf("list meal nutrient values: %w", err)
	}
	byNutrient := make(map[int16]float64, len(values))
	for _, v := range values {
		byNutrient[v.NutrientID] = v.Amount
	}

	nvs := make([]NutrientValue, 0, len(nutrients))
	for _, n := range nutrients {
		nv := NutrientValue{NutrientID: n.ID, Key: n.Key, DisplayName: n.DisplayName, Unit: n.Unit}
		if amt, ok := byNutrient[n.ID]; ok {
			nv.Amount = &amt
		}
		nvs = append(nvs, nv)
	}

	labels, err := s.LabelsForMeal(ctx, m.ID)
	if err != nil {
		return Meal{}, err
	}

	return Meal{
		ID:             m.ID,
		Name:           m.Name,
		Source:         m.Source,
		RecipeURL:      m.RecipeUrl.String,
		ServingLabel:   m.ServingLabel,
		ServingAmount:  m.ServingAmount,
		IsFavorite:     m.IsFavorite,
		NutrientValues: nvs,
		Labels:         labels,
	}, nil
}

// Create inserts a new manually-entered meal, its nutrient values, and its
// label assignments in one transaction — either everything lands, or none
// of it does.
func (s *Store) Create(ctx context.Context, createdBy uuid.UUID, in MealInput) (uuid.UUID, error) {
	mealID := uuid.New()
	err := s.withTx(ctx, func(q sqlc.Querier) error {
		meal, err := q.CreateMeal(ctx, sqlc.CreateMealParams{
			ID:            mealID,
			Name:          in.Name,
			Source:        "manual",
			RecipeUrl:     sql.NullString{String: in.RecipeURL, Valid: in.RecipeURL != ""},
			ServingLabel:  in.ServingLabel,
			ServingAmount: in.ServingAmount,
			CreatedBy:     uuid.NullUUID{UUID: createdBy, Valid: true},
			IsFavorite:    in.IsFavorite,
		})
		if err != nil {
			return fmt.Errorf("create meal: %w", err)
		}
		if err := setNutrientValues(ctx, q, meal.ID, in.NutrientAmounts); err != nil {
			return err
		}
		return setLabels(ctx, q, meal.ID, in.LabelIDs)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return mealID, nil
}

// Update replaces a meal's editable fields, its full set of nutrient
// values, and its full set of label assignments, in one transaction.
func (s *Store) Update(ctx context.Context, id uuid.UUID, in MealInput) error {
	return s.withTx(ctx, func(q sqlc.Querier) error {
		if _, err := q.UpdateMeal(ctx, sqlc.UpdateMealParams{
			ID:            id,
			Name:          in.Name,
			RecipeUrl:     sql.NullString{String: in.RecipeURL, Valid: in.RecipeURL != ""},
			ServingLabel:  in.ServingLabel,
			ServingAmount: in.ServingAmount,
			IsFavorite:    in.IsFavorite,
		}); err != nil {
			return fmt.Errorf("update meal: %w", err)
		}
		if err := q.ClearMealNutrientValues(ctx, id); err != nil {
			return fmt.Errorf("clear nutrient values: %w", err)
		}
		if err := setNutrientValues(ctx, q, id, in.NutrientAmounts); err != nil {
			return err
		}
		return setLabels(ctx, q, id, in.LabelIDs)
	})
}

// Delete removes a meal and its nutrient values (cascaded by the DB).
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	return s.q.DeleteMeal(ctx, id)
}

// ImportInput is what an importer (Mealie, Open Food Facts) submits to add
// or refresh a meal. SourceRef is the external identifier (a Mealie recipe
// slug, an Open Food Facts barcode) — it must be non-empty, since the
// upsert that makes re-importing idempotent is keyed by (Source, SourceRef)
// and the DB's partial unique index only covers non-NULL source_ref values.
type ImportInput struct {
	Name            string
	Source          string // "mealie" or "off" — must match meals.source's CHECK constraint
	SourceRef       string
	RecipeURL       string
	ServingLabel    string
	ServingAmount   float64
	NutrientAmounts map[int16]float64
}

// Import creates or updates (keyed by Source + SourceRef) a meal from an
// external importer, in one transaction. Unlike Create, this is meant to be
// called again for the same recipe/product later — a re-import always
// replaces the meal's nutrient values with in.NutrientAmounts (reflecting
// whatever the source currently reports), rather than merging with
// whatever a previous import or manual edit left behind.
func (s *Store) Import(ctx context.Context, createdBy uuid.UUID, in ImportInput) (uuid.UUID, error) {
	newID := uuid.New()
	var mealID uuid.UUID
	err := s.withTx(ctx, func(q sqlc.Querier) error {
		meal, err := q.UpsertMealBySourceRef(ctx, sqlc.UpsertMealBySourceRefParams{
			ID:            newID,
			Name:          in.Name,
			Source:        in.Source,
			SourceRef:     sql.NullString{String: in.SourceRef, Valid: in.SourceRef != ""},
			RecipeUrl:     sql.NullString{String: in.RecipeURL, Valid: in.RecipeURL != ""},
			ServingLabel:  in.ServingLabel,
			ServingAmount: in.ServingAmount,
			CreatedBy:     uuid.NullUUID{UUID: createdBy, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("upsert meal: %w", err)
		}
		mealID = meal.ID

		if err := q.ClearMealNutrientValues(ctx, meal.ID); err != nil {
			return fmt.Errorf("clear nutrient values: %w", err)
		}
		return setNutrientValues(ctx, q, meal.ID, in.NutrientAmounts)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return mealID, nil
}

// setLabels replaces mealID's full set of label assignments with labelIDs —
// always clear-then-add (safe as a no-op on a brand new meal with nothing
// to clear yet), so Create and Update can share the same replace semantics
// setNutrientValues already uses for nutrients.
func setLabels(ctx context.Context, q sqlc.Querier, mealID uuid.UUID, labelIDs []uuid.UUID) error {
	if err := q.ClearMealLabelAssignments(ctx, mealID); err != nil {
		return fmt.Errorf("clear label assignments: %w", err)
	}
	for _, labelID := range labelIDs {
		if err := q.AddMealLabelAssignment(ctx, sqlc.AddMealLabelAssignmentParams{
			MealID:  mealID,
			LabelID: labelID,
		}); err != nil {
			return fmt.Errorf("add label assignment: %w", err)
		}
	}
	return nil
}

func setNutrientValues(ctx context.Context, q sqlc.Querier, mealID uuid.UUID, amounts map[int16]float64) error {
	for nutrientID, amount := range amounts {
		if err := q.SetMealNutrientValue(ctx, sqlc.SetMealNutrientValueParams{
			MealID:     mealID,
			NutrientID: nutrientID,
			Amount:     amount,
		}); err != nil {
			return fmt.Errorf("set nutrient value: %w", err)
		}
	}
	return nil
}
