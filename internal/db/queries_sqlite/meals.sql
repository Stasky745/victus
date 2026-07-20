-- name: SearchMeals :many
-- query is a pre-built trigram-OR FTS5 match expression (see internal/mealslib),
-- not the raw user string - MATCH syntax is passed as a bound parameter, never
-- string-concatenated into the query text.
SELECT meals.*
FROM meals_fts
JOIN meals ON meals.rowid = meals_fts.rowid
WHERE meals_fts.name MATCH sqlc.arg(query)
ORDER BY bm25(meals_fts)
LIMIT sqlc.arg(limit_count);

-- name: ListMeals :many
SELECT * FROM meals ORDER BY name LIMIT sqlc.arg(limit_count);

-- name: GetMeal :one
SELECT * FROM meals WHERE id = sqlc.arg(id);

-- name: GetMealBySourceRef :one
SELECT * FROM meals WHERE source = sqlc.arg(source) AND source_ref = sqlc.arg(source_ref);

-- name: GetManualMealByName :one
-- Export/import's natural-key lookup for manually-entered meals, which have
-- no source_ref to match on (GetMealBySourceRef doesn't fit: source_ref is
-- NULL for these, not an empty string).
SELECT * FROM meals WHERE source = 'manual' AND name = sqlc.arg(name);

-- name: CreateMeal :one
INSERT INTO meals (id, name, source, source_ref, recipe_url, serving_label, serving_amount, created_by, is_favorite)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(source), sqlc.arg(source_ref), sqlc.arg(recipe_url),
        sqlc.arg(serving_label), sqlc.arg(serving_amount), sqlc.arg(created_by), sqlc.arg(is_favorite))
RETURNING *;

-- name: UpdateMeal :one
UPDATE meals
SET name = sqlc.arg(name), recipe_url = sqlc.arg(recipe_url), serving_label = sqlc.arg(serving_label),
    serving_amount = sqlc.arg(serving_amount), is_favorite = sqlc.arg(is_favorite), updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: UpsertMealBySourceRef :one
-- id is only used for a brand-new row - on conflict, the existing row keeps
-- its original id (DO UPDATE below never touches the id column).
INSERT INTO meals (id, name, source, source_ref, recipe_url, serving_label, serving_amount, created_by)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(source), sqlc.arg(source_ref), sqlc.arg(recipe_url),
        sqlc.arg(serving_label), sqlc.arg(serving_amount), sqlc.arg(created_by))
ON CONFLICT (source, source_ref) WHERE source_ref IS NOT NULL
DO UPDATE SET name = EXCLUDED.name, recipe_url = EXCLUDED.recipe_url,
              serving_label = EXCLUDED.serving_label, serving_amount = EXCLUDED.serving_amount,
              updated_at = CURRENT_TIMESTAMP
RETURNING *;

-- name: DeleteMeal :exec
DELETE FROM meals WHERE id = sqlc.arg(id);

-- name: SetMealNutrientValue :exec
INSERT INTO meal_nutrient_values (meal_id, nutrient_id, amount)
VALUES (sqlc.arg(meal_id), sqlc.arg(nutrient_id), sqlc.arg(amount))
ON CONFLICT (meal_id, nutrient_id) DO UPDATE SET amount = EXCLUDED.amount;

-- name: ListMealNutrientValues :many
SELECT meal_nutrient_values.*, nutrients.key, nutrients.display_name, nutrients.unit
FROM meal_nutrient_values
JOIN nutrients ON nutrients.id = meal_nutrient_values.nutrient_id
WHERE meal_id = sqlc.arg(meal_id)
ORDER BY nutrients.sort_order;

-- name: ClearMealNutrientValues :exec
DELETE FROM meal_nutrient_values WHERE meal_id = sqlc.arg(meal_id);

-- name: ToggleMealFavorite :one
-- A single atomic flip rather than read-then-write from the caller, so a
-- double-click (or two tabs) can't race into an inconsistent end state.
UPDATE meals SET is_favorite = NOT is_favorite WHERE id = sqlc.arg(id) RETURNING *;

-- name: ListFavoriteMeals :many
SELECT * FROM meals WHERE is_favorite ORDER BY name;

-- name: ListMealsByLabel :many
SELECT meals.*
FROM meals
JOIN meal_label_assignments ON meal_label_assignments.meal_id = meals.id
WHERE meal_label_assignments.label_id = sqlc.arg(label_id)
ORDER BY meals.name
LIMIT sqlc.arg(limit_count);

-- name: SearchMealsByLabel :many
-- See SearchMeals for why query is a pre-built trigram-OR match expression.
SELECT meals.*
FROM meals_fts
JOIN meals ON meals.rowid = meals_fts.rowid
JOIN meal_label_assignments ON meal_label_assignments.meal_id = meals.id
WHERE meal_label_assignments.label_id = sqlc.arg(label_id)
  AND meals_fts.name MATCH sqlc.arg(query)
ORDER BY bm25(meals_fts)
LIMIT sqlc.arg(limit_count);

-- name: ListMealLabels :many
SELECT * FROM meal_labels ORDER BY sort_order, name;

-- name: GetMealLabelByName :one
-- Export/import's natural-key lookup - a label's id means nothing across
-- instances, but its name (already unique) does.
SELECT * FROM meal_labels WHERE name = sqlc.arg(name);

-- name: CreateMealLabel :one
INSERT INTO meal_labels (id, name, color, sort_order)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(color), sqlc.arg(sort_order))
RETURNING *;

-- name: UpdateMealLabel :one
UPDATE meal_labels SET name = sqlc.arg(name), color = sqlc.arg(color) WHERE id = sqlc.arg(id) RETURNING *;

-- name: DeleteMealLabel :exec
DELETE FROM meal_labels WHERE id = sqlc.arg(id);

-- name: ListMealLabelIDsForMeal :many
-- The assigned label ids for one meal - used to pre-check the edit form's
-- label checkboxes.
SELECT label_id FROM meal_label_assignments WHERE meal_id = sqlc.arg(meal_id);

-- name: ListMealLabelsForMeals :many
-- Batched (not one query per meal) label lookup for a list of meals - the
-- Meal Library list page's badges, fetched in one round trip regardless of
-- how many meals are on the page.
SELECT meal_label_assignments.meal_id, meal_labels.id, meal_labels.name, meal_labels.color
FROM meal_label_assignments
JOIN meal_labels ON meal_labels.id = meal_label_assignments.label_id
WHERE meal_label_assignments.meal_id IN (sqlc.slice('meal_ids'))
ORDER BY meal_labels.sort_order, meal_labels.name;

-- name: ClearMealLabelAssignments :exec
DELETE FROM meal_label_assignments WHERE meal_id = sqlc.arg(meal_id);

-- name: AddMealLabelAssignment :exec
INSERT INTO meal_label_assignments (meal_id, label_id)
VALUES (sqlc.arg(meal_id), sqlc.arg(label_id))
ON CONFLICT DO NOTHING;
