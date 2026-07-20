// Package sqliteadapter makes internal/db/sqlite's generated Queries satisfy
// internal/db/sqlc's Querier interface, so business logic (internal/mealslib,
// internal/planning, internal/goals, internal/auth) can depend on one
// interface regardless of which backend Victus is configured to use.
//
// sqlc generates a distinct named Params/Row struct per engine even when the
// underlying SQL and column types are identical (e.g. sqlc.CreateMealParams
// vs sqlite.CreateMealParams) — Go interface satisfaction requires exact
// type identity, so sqlite.Queries can never structurally satisfy
// sqlc.Querier on its own. Every conversion below is a plain Go struct
// conversion (sqlc.X(sqliteX)), which the compiler only allows because both
// schemas/queries are deliberately authored to produce identical field
// name/type/order sequences — if a future migration/query change breaks
// that alignment, these conversions fail to compile rather than silently
// misbehaving at runtime.
package sqliteadapter

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/db/sqlite"
)

// Queries adapts *sqlite.Queries to sqlc.Querier.
type Queries struct {
	q *sqlite.Queries
}

// New returns a sqlc.Querier backed by db (a *sql.DB or *sql.Tx).
func New(db sqlite.DBTX) sqlc.Querier {
	return &Queries{q: sqlite.New(db)}
}

var _ sqlc.Querier = (*Queries)(nil)

// convertSlice converts each element of in via conv — the generic
// counterpart to the single-value struct conversions below, needed because
// Go doesn't allow converting a []From to a []To directly even when From
// and To are themselves convertible.
func convertSlice[From, To any](in []From, conv func(From) To) []To {
	out := make([]To, len(in))
	for i, v := range in {
		out[i] = conv(v)
	}
	return out
}

func (a *Queries) AddDayPlanItem(ctx context.Context, arg sqlc.AddDayPlanItemParams) (sqlc.DayPlanItem, error) {
	row, err := a.q.AddDayPlanItem(ctx, sqlite.AddDayPlanItemParams(arg))
	return sqlc.DayPlanItem(row), err
}

func (a *Queries) AddDefaultDayItem(ctx context.Context, arg sqlc.AddDefaultDayItemParams) (sqlc.DefaultDayItem, error) {
	row, err := a.q.AddDefaultDayItem(ctx, sqlite.AddDefaultDayItemParams(arg))
	return sqlc.DefaultDayItem(row), err
}

func (a *Queries) AddMealFavoriteCategory(ctx context.Context, arg sqlc.AddMealFavoriteCategoryParams) error {
	return a.q.AddMealFavoriteCategory(ctx, sqlite.AddMealFavoriteCategoryParams(arg))
}

func (a *Queries) AddMealLabelAssignment(ctx context.Context, arg sqlc.AddMealLabelAssignmentParams) error {
	return a.q.AddMealLabelAssignment(ctx, sqlite.AddMealLabelAssignmentParams(arg))
}

func (a *Queries) ClearMealFavoriteCategories(ctx context.Context, mealID uuid.UUID) error {
	return a.q.ClearMealFavoriteCategories(ctx, mealID)
}

func (a *Queries) ClearMealLabelAssignments(ctx context.Context, mealID uuid.UUID) error {
	return a.q.ClearMealLabelAssignments(ctx, mealID)
}

func (a *Queries) ClearMealNutrientValues(ctx context.Context, mealID uuid.UUID) error {
	return a.q.ClearMealNutrientValues(ctx, mealID)
}

func (a *Queries) CreateMeal(ctx context.Context, arg sqlc.CreateMealParams) (sqlc.Meal, error) {
	row, err := a.q.CreateMeal(ctx, sqlite.CreateMealParams(arg))
	return sqlc.Meal(row), err
}

func (a *Queries) CreateMealCategory(ctx context.Context, arg sqlc.CreateMealCategoryParams) (sqlc.MealCategory, error) {
	row, err := a.q.CreateMealCategory(ctx, sqlite.CreateMealCategoryParams(arg))
	return sqlc.MealCategory(row), err
}

func (a *Queries) CreateMealLabel(ctx context.Context, arg sqlc.CreateMealLabelParams) (sqlc.MealLabel, error) {
	row, err := a.q.CreateMealLabel(ctx, sqlite.CreateMealLabelParams(arg))
	return sqlc.MealLabel(row), err
}

func (a *Queries) CreateSession(ctx context.Context, arg sqlc.CreateSessionParams) (sqlc.Session, error) {
	row, err := a.q.CreateSession(ctx, sqlite.CreateSessionParams(arg))
	return sqlc.Session(row), err
}

func (a *Queries) CreateUser(ctx context.Context, arg sqlc.CreateUserParams) (sqlc.User, error) {
	row, err := a.q.CreateUser(ctx, sqlite.CreateUserParams(arg))
	return sqlc.User(row), err
}

func (a *Queries) CreateUserWithPassword(ctx context.Context, arg sqlc.CreateUserWithPasswordParams) (sqlc.User, error) {
	row, err := a.q.CreateUserWithPassword(ctx, sqlite.CreateUserWithPasswordParams(arg))
	return sqlc.User(row), err
}

func (a *Queries) DeleteExpiredSessions(ctx context.Context) error {
	return a.q.DeleteExpiredSessions(ctx)
}

func (a *Queries) DeleteMeal(ctx context.Context, id uuid.UUID) error {
	return a.q.DeleteMeal(ctx, id)
}

func (a *Queries) DeleteMealCategory(ctx context.Context, id uuid.UUID) error {
	return a.q.DeleteMealCategory(ctx, id)
}

func (a *Queries) DeleteMealLabel(ctx context.Context, id uuid.UUID) error {
	return a.q.DeleteMealLabel(ctx, id)
}

func (a *Queries) DeleteSession(ctx context.Context, id uuid.UUID) error {
	return a.q.DeleteSession(ctx, id)
}

func (a *Queries) GetAppSetting(ctx context.Context, key string) (string, error) {
	return a.q.GetAppSetting(ctx, key)
}

func (a *Queries) GetDayPlan(ctx context.Context, arg sqlc.GetDayPlanParams) (sqlc.DayPlan, error) {
	row, err := a.q.GetDayPlan(ctx, sqlite.GetDayPlanParams(arg))
	return sqlc.DayPlan(row), err
}

func (a *Queries) GetManualMealByName(ctx context.Context, name string) (sqlc.Meal, error) {
	row, err := a.q.GetManualMealByName(ctx, name)
	return sqlc.Meal(row), err
}

func (a *Queries) GetDayPlanItemOwner(ctx context.Context, id uuid.UUID) (sqlc.GetDayPlanItemOwnerRow, error) {
	row, err := a.q.GetDayPlanItemOwner(ctx, id)
	return sqlc.GetDayPlanItemOwnerRow(row), err
}

func (a *Queries) GetDayPlanTotals(ctx context.Context, dayPlanID uuid.UUID) ([]sqlc.GetDayPlanTotalsRow, error) {
	rows, err := a.q.GetDayPlanTotals(ctx, dayPlanID)
	return convertSlice(rows, func(r sqlite.GetDayPlanTotalsRow) sqlc.GetDayPlanTotalsRow { return sqlc.GetDayPlanTotalsRow(r) }), err
}

func (a *Queries) GetMeal(ctx context.Context, id uuid.UUID) (sqlc.Meal, error) {
	row, err := a.q.GetMeal(ctx, id)
	return sqlc.Meal(row), err
}

func (a *Queries) GetMealBySourceRef(ctx context.Context, arg sqlc.GetMealBySourceRefParams) (sqlc.Meal, error) {
	row, err := a.q.GetMealBySourceRef(ctx, sqlite.GetMealBySourceRefParams(arg))
	return sqlc.Meal(row), err
}

func (a *Queries) GetMealCategoryByName(ctx context.Context, name string) (sqlc.MealCategory, error) {
	row, err := a.q.GetMealCategoryByName(ctx, name)
	return sqlc.MealCategory(row), err
}

func (a *Queries) IsMealFavoriteForCategory(ctx context.Context, arg sqlc.IsMealFavoriteForCategoryParams) (bool, error) {
	return a.q.IsMealFavoriteForCategory(ctx, sqlite.IsMealFavoriteForCategoryParams(arg))
}

func (a *Queries) GetMealLabelByName(ctx context.Context, name string) (sqlc.MealLabel, error) {
	row, err := a.q.GetMealLabelByName(ctx, name)
	return sqlc.MealLabel(row), err
}

func (a *Queries) GetOrCreateDayPlan(ctx context.Context, arg sqlc.GetOrCreateDayPlanParams) (sqlc.DayPlan, error) {
	row, err := a.q.GetOrCreateDayPlan(ctx, sqlite.GetOrCreateDayPlanParams(arg))
	return sqlc.DayPlan(row), err
}

func (a *Queries) GetSession(ctx context.Context, id uuid.UUID) (sqlc.GetSessionRow, error) {
	row, err := a.q.GetSession(ctx, id)
	return sqlc.GetSessionRow(row), err
}

func (a *Queries) GetUserByEmail(ctx context.Context, email string) (sqlc.User, error) {
	row, err := a.q.GetUserByEmail(ctx, email)
	return sqlc.User(row), err
}

func (a *Queries) GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error) {
	row, err := a.q.GetUserByID(ctx, id)
	return sqlc.User(row), err
}

func (a *Queries) GetUserByOIDCSubject(ctx context.Context, oidcSubject sql.NullString) (sqlc.User, error) {
	row, err := a.q.GetUserByOIDCSubject(ctx, oidcSubject)
	return sqlc.User(row), err
}

func (a *Queries) GetUserNutrientGoals(ctx context.Context, userID uuid.UUID) ([]sqlc.GetUserNutrientGoalsRow, error) {
	rows, err := a.q.GetUserNutrientGoals(ctx, userID)
	return convertSlice(rows, func(r sqlite.GetUserNutrientGoalsRow) sqlc.GetUserNutrientGoalsRow {
		return sqlc.GetUserNutrientGoalsRow(r)
	}), err
}

func (a *Queries) ListAppSettings(ctx context.Context) ([]sqlc.AppSetting, error) {
	rows, err := a.q.ListAppSettings(ctx)
	return convertSlice(rows, func(r sqlite.AppSetting) sqlc.AppSetting { return sqlc.AppSetting(r) }), err
}

func (a *Queries) ListDayPlanItems(ctx context.Context, dayPlanID uuid.UUID) ([]sqlc.ListDayPlanItemsRow, error) {
	rows, err := a.q.ListDayPlanItems(ctx, dayPlanID)
	return convertSlice(rows, func(r sqlite.ListDayPlanItemsRow) sqlc.ListDayPlanItemsRow { return sqlc.ListDayPlanItemsRow(r) }), err
}

func (a *Queries) ListDayPlansInRange(ctx context.Context, arg sqlc.ListDayPlansInRangeParams) ([]sqlc.DayPlan, error) {
	rows, err := a.q.ListDayPlansInRange(ctx, sqlite.ListDayPlansInRangeParams(arg))
	return convertSlice(rows, func(r sqlite.DayPlan) sqlc.DayPlan { return sqlc.DayPlan(r) }), err
}

func (a *Queries) ListDefaultDayItems(ctx context.Context, userID uuid.UUID) ([]sqlc.ListDefaultDayItemsRow, error) {
	rows, err := a.q.ListDefaultDayItems(ctx, userID)
	return convertSlice(rows, func(r sqlite.ListDefaultDayItemsRow) sqlc.ListDefaultDayItemsRow {
		return sqlc.ListDefaultDayItemsRow(r)
	}), err
}

func (a *Queries) ListFavoriteCategoriesForMeals(ctx context.Context, mealIds []uuid.UUID) ([]sqlc.ListFavoriteCategoriesForMealsRow, error) {
	rows, err := a.q.ListFavoriteCategoriesForMeals(ctx, mealIds)
	return convertSlice(rows, func(r sqlite.ListFavoriteCategoriesForMealsRow) sqlc.ListFavoriteCategoriesForMealsRow {
		return sqlc.ListFavoriteCategoriesForMealsRow(r)
	}), err
}

func (a *Queries) ListFavoriteCategoryIDsForMeal(ctx context.Context, mealID uuid.UUID) ([]uuid.UUID, error) {
	return a.q.ListFavoriteCategoryIDsForMeal(ctx, mealID)
}

func (a *Queries) ListFavoriteMealsForCategory(ctx context.Context, categoryID uuid.UUID) ([]sqlc.Meal, error) {
	rows, err := a.q.ListFavoriteMealsForCategory(ctx, categoryID)
	return convertSlice(rows, func(r sqlite.Meal) sqlc.Meal { return sqlc.Meal(r) }), err
}

func (a *Queries) ListMealCategories(ctx context.Context) ([]sqlc.MealCategory, error) {
	rows, err := a.q.ListMealCategories(ctx)
	return convertSlice(rows, func(r sqlite.MealCategory) sqlc.MealCategory { return sqlc.MealCategory(r) }), err
}

func (a *Queries) ListMealLabelIDsForMeal(ctx context.Context, mealID uuid.UUID) ([]uuid.UUID, error) {
	return a.q.ListMealLabelIDsForMeal(ctx, mealID)
}

func (a *Queries) ListMealLabels(ctx context.Context) ([]sqlc.MealLabel, error) {
	rows, err := a.q.ListMealLabels(ctx)
	return convertSlice(rows, func(r sqlite.MealLabel) sqlc.MealLabel { return sqlc.MealLabel(r) }), err
}

func (a *Queries) ListMealLabelsForMeals(ctx context.Context, mealIds []uuid.UUID) ([]sqlc.ListMealLabelsForMealsRow, error) {
	rows, err := a.q.ListMealLabelsForMeals(ctx, mealIds)
	return convertSlice(rows, func(r sqlite.ListMealLabelsForMealsRow) sqlc.ListMealLabelsForMealsRow {
		return sqlc.ListMealLabelsForMealsRow(r)
	}), err
}

func (a *Queries) ListMealNutrientValues(ctx context.Context, mealID uuid.UUID) ([]sqlc.ListMealNutrientValuesRow, error) {
	rows, err := a.q.ListMealNutrientValues(ctx, mealID)
	return convertSlice(rows, func(r sqlite.ListMealNutrientValuesRow) sqlc.ListMealNutrientValuesRow {
		return sqlc.ListMealNutrientValuesRow(r)
	}), err
}

func (a *Queries) ListMeals(ctx context.Context, limitCount int32) ([]sqlc.Meal, error) {
	rows, err := a.q.ListMeals(ctx, limitCount)
	return convertSlice(rows, func(r sqlite.Meal) sqlc.Meal { return sqlc.Meal(r) }), err
}

func (a *Queries) ListMealsByLabel(ctx context.Context, arg sqlc.ListMealsByLabelParams) ([]sqlc.Meal, error) {
	rows, err := a.q.ListMealsByLabel(ctx, sqlite.ListMealsByLabelParams(arg))
	return convertSlice(rows, func(r sqlite.Meal) sqlc.Meal { return sqlc.Meal(r) }), err
}

func (a *Queries) ListNutrients(ctx context.Context) ([]sqlc.Nutrient, error) {
	rows, err := a.q.ListNutrients(ctx)
	return convertSlice(rows, func(r sqlite.Nutrient) sqlc.Nutrient { return sqlc.Nutrient(r) }), err
}

func (a *Queries) ListUsers(ctx context.Context) ([]sqlc.User, error) {
	rows, err := a.q.ListUsers(ctx)
	return convertSlice(rows, func(r sqlite.User) sqlc.User { return sqlc.User(r) }), err
}

func (a *Queries) RemoveDayPlanItem(ctx context.Context, id uuid.UUID) error {
	return a.q.RemoveDayPlanItem(ctx, id)
}

func (a *Queries) RemoveDefaultDayItem(ctx context.Context, arg sqlc.RemoveDefaultDayItemParams) (int64, error) {
	return a.q.RemoveDefaultDayItem(ctx, sqlite.RemoveDefaultDayItemParams(arg))
}

func (a *Queries) SearchMeals(ctx context.Context, arg sqlc.SearchMealsParams) ([]sqlc.Meal, error) {
	rows, err := a.q.SearchMeals(ctx, sqlite.SearchMealsParams(arg))
	return convertSlice(rows, func(r sqlite.Meal) sqlc.Meal { return sqlc.Meal(r) }), err
}

func (a *Queries) SearchMealsByLabel(ctx context.Context, arg sqlc.SearchMealsByLabelParams) ([]sqlc.Meal, error) {
	rows, err := a.q.SearchMealsByLabel(ctx, sqlite.SearchMealsByLabelParams(arg))
	return convertSlice(rows, func(r sqlite.Meal) sqlc.Meal { return sqlc.Meal(r) }), err
}

func (a *Queries) SetAppSetting(ctx context.Context, arg sqlc.SetAppSettingParams) error {
	return a.q.SetAppSetting(ctx, sqlite.SetAppSettingParams(arg))
}

func (a *Queries) SetMealNutrientValue(ctx context.Context, arg sqlc.SetMealNutrientValueParams) error {
	return a.q.SetMealNutrientValue(ctx, sqlite.SetMealNutrientValueParams(arg))
}

func (a *Queries) SetUserNutrientGoal(ctx context.Context, arg sqlc.SetUserNutrientGoalParams) error {
	return a.q.SetUserNutrientGoal(ctx, sqlite.SetUserNutrientGoalParams(arg))
}

func (a *Queries) RemoveMealFavoriteCategory(ctx context.Context, arg sqlc.RemoveMealFavoriteCategoryParams) error {
	return a.q.RemoveMealFavoriteCategory(ctx, sqlite.RemoveMealFavoriteCategoryParams(arg))
}

func (a *Queries) UpdateDayPlanItemQuantity(ctx context.Context, arg sqlc.UpdateDayPlanItemQuantityParams) error {
	return a.q.UpdateDayPlanItemQuantity(ctx, sqlite.UpdateDayPlanItemQuantityParams(arg))
}

func (a *Queries) UpdateMeal(ctx context.Context, arg sqlc.UpdateMealParams) (sqlc.Meal, error) {
	row, err := a.q.UpdateMeal(ctx, sqlite.UpdateMealParams(arg))
	return sqlc.Meal(row), err
}

func (a *Queries) UpdateMealCategory(ctx context.Context, arg sqlc.UpdateMealCategoryParams) (sqlc.MealCategory, error) {
	row, err := a.q.UpdateMealCategory(ctx, sqlite.UpdateMealCategoryParams(arg))
	return sqlc.MealCategory(row), err
}

func (a *Queries) UpdateMealLabel(ctx context.Context, arg sqlc.UpdateMealLabelParams) (sqlc.MealLabel, error) {
	row, err := a.q.UpdateMealLabel(ctx, sqlite.UpdateMealLabelParams(arg))
	return sqlc.MealLabel(row), err
}

func (a *Queries) UpsertMealBySourceRef(ctx context.Context, arg sqlc.UpsertMealBySourceRefParams) (sqlc.Meal, error) {
	row, err := a.q.UpsertMealBySourceRef(ctx, sqlite.UpsertMealBySourceRefParams(arg))
	return sqlc.Meal(row), err
}
