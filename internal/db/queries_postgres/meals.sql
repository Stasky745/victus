-- name: SearchMeals :many
-- word_similarity (not whole-string similarity/%) so a typo in one word of a
-- multi-word name (e.g. "chikcen" against "Chicken & Rice") still matches —
-- whole-string trigram similarity dilutes a single-word typo's score too
-- much once other words are mixed in, and silently returns nothing at the
-- default 0.3 threshold. 0.3 against the best-matching word instead gives a
-- clean separation between typo'd/partial matches and unrelated queries.
SELECT *
FROM meals
WHERE word_similarity(sqlc.arg(query)::text, name) > 0.3
ORDER BY word_similarity(sqlc.arg(query)::text, name) DESC
LIMIT sqlc.arg(limit_count);

-- name: ListMeals :many
SELECT * FROM meals ORDER BY name LIMIT sqlc.arg(limit_count);

-- name: GetMeal :one
SELECT * FROM meals WHERE id = $1;

-- name: GetMealBySourceRef :one
SELECT * FROM meals WHERE source = $1 AND source_ref = $2;

-- name: GetManualMealByName :one
-- Export/import's natural-key lookup for manually-entered meals, which have
-- no source_ref to match on (GetMealBySourceRef doesn't fit: source_ref is
-- NULL for these, not an empty string).
SELECT * FROM meals WHERE source = 'manual' AND name = $1;

-- name: CreateMeal :one
INSERT INTO meals (id, name, source, source_ref, recipe_url, serving_label, serving_amount, created_by, is_favorite)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateMeal :one
UPDATE meals
SET name = $1, recipe_url = $2, serving_label = $3, serving_amount = $4, is_favorite = $5, updated_at = now()
WHERE id = $6
RETURNING *;

-- name: UpsertMealBySourceRef :one
-- id is only used for a brand-new row — on conflict, the existing row keeps
-- its original id (DO UPDATE below never touches the id column).
INSERT INTO meals (id, name, source, source_ref, recipe_url, serving_label, serving_amount, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (source, source_ref) WHERE source_ref IS NOT NULL
DO UPDATE SET name = EXCLUDED.name, recipe_url = EXCLUDED.recipe_url,
              serving_label = EXCLUDED.serving_label, serving_amount = EXCLUDED.serving_amount,
              updated_at = now()
RETURNING *;

-- name: DeleteMeal :exec
DELETE FROM meals WHERE id = $1;

-- name: SetMealNutrientValue :exec
INSERT INTO meal_nutrient_values (meal_id, nutrient_id, amount)
VALUES ($1, $2, $3)
ON CONFLICT (meal_id, nutrient_id) DO UPDATE SET amount = EXCLUDED.amount;

-- name: ListMealNutrientValues :many
SELECT meal_nutrient_values.*, nutrients.key, nutrients.display_name, nutrients.unit
FROM meal_nutrient_values
JOIN nutrients ON nutrients.id = meal_nutrient_values.nutrient_id
WHERE meal_id = $1
ORDER BY nutrients.sort_order;

-- name: ClearMealNutrientValues :exec
DELETE FROM meal_nutrient_values WHERE meal_id = $1;

-- name: ToggleMealFavorite :one
-- A single atomic flip rather than read-then-write from the caller, so a
-- double-click (or two tabs) can't race into an inconsistent end state.
UPDATE meals SET is_favorite = NOT is_favorite WHERE id = $1 RETURNING *;

-- name: ListFavoriteMeals :many
SELECT * FROM meals WHERE is_favorite ORDER BY name;

-- name: ListMealsByLabel :many
SELECT meals.*
FROM meals
JOIN meal_label_assignments ON meal_label_assignments.meal_id = meals.id
WHERE meal_label_assignments.label_id = $1
ORDER BY meals.name
LIMIT sqlc.arg(limit_count);

-- name: SearchMealsByLabel :many
-- See SearchMeals for why word_similarity rather than whole-string.
SELECT meals.*
FROM meals
JOIN meal_label_assignments ON meal_label_assignments.meal_id = meals.id
WHERE meal_label_assignments.label_id = $1
  AND word_similarity(sqlc.arg(query)::text, meals.name) > 0.3
ORDER BY word_similarity(sqlc.arg(query)::text, meals.name) DESC
LIMIT sqlc.arg(limit_count);

-- name: ListMealLabels :many
SELECT * FROM meal_labels ORDER BY sort_order, name;

-- name: GetMealLabelByName :one
-- Export/import's natural-key lookup — a label's id means nothing across
-- instances, but its name (already unique) does.
SELECT * FROM meal_labels WHERE name = $1;

-- name: CreateMealLabel :one
INSERT INTO meal_labels (id, name, color, sort_order)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateMealLabel :one
UPDATE meal_labels SET name = $1, color = $2 WHERE id = $3 RETURNING *;

-- name: DeleteMealLabel :exec
DELETE FROM meal_labels WHERE id = $1;

-- name: ListMealLabelIDsForMeal :many
-- The assigned label ids for one meal — used to pre-check the edit form's
-- label checkboxes.
SELECT label_id FROM meal_label_assignments WHERE meal_id = $1;

-- name: ListMealLabelsForMeals :many
-- Batched (not one query per meal) label lookup for a list of meals — the
-- Meal Library list page's badges, fetched in one round trip regardless of
-- how many meals are on the page.
SELECT meal_label_assignments.meal_id, meal_labels.id, meal_labels.name, meal_labels.color
FROM meal_label_assignments
JOIN meal_labels ON meal_labels.id = meal_label_assignments.label_id
WHERE meal_label_assignments.meal_id = ANY(sqlc.arg(meal_ids)::uuid[])
ORDER BY meal_labels.sort_order, meal_labels.name;

-- name: ClearMealLabelAssignments :exec
DELETE FROM meal_label_assignments WHERE meal_id = $1;

-- name: AddMealLabelAssignment :exec
INSERT INTO meal_label_assignments (meal_id, label_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;
