-- name: ListNutrients :many
SELECT * FROM nutrients ORDER BY sort_order;

-- name: ListMealCategories :many
SELECT * FROM meal_categories ORDER BY sort_order;

-- name: CreateMealCategory :one
INSERT INTO meal_categories (id, name, sort_order)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateMealCategory :one
UPDATE meal_categories SET name = $1, sort_order = $2 WHERE id = $3
RETURNING *;

-- name: DeleteMealCategory :exec
DELETE FROM meal_categories WHERE id = $1;

-- name: GetMealCategoryByName :one
-- Export/import's natural-key lookup — a category's id means nothing across
-- instances, but its name (already unique) does.
SELECT * FROM meal_categories WHERE name = $1;

-- name: GetAppSetting :one
SELECT value FROM app_settings WHERE key = $1;

-- name: ListAppSettings :many
SELECT * FROM app_settings ORDER BY key;

-- name: SetAppSetting :exec
INSERT INTO app_settings (key, value)
VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
