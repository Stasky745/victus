-- name: ListNutrients :many
SELECT * FROM nutrients ORDER BY sort_order;

-- name: ListMealCategories :many
SELECT * FROM meal_categories ORDER BY sort_order;

-- name: CreateMealCategory :one
INSERT INTO meal_categories (id, name, sort_order)
VALUES (?, ?, ?)
RETURNING *;

-- name: UpdateMealCategory :one
UPDATE meal_categories SET name = ?, sort_order = ? WHERE id = ?
RETURNING *;

-- name: DeleteMealCategory :exec
DELETE FROM meal_categories WHERE id = ?;

-- name: GetMealCategoryByName :one
-- Export/import's natural-key lookup - a category's id means nothing across
-- instances, but its name (already unique) does.
SELECT * FROM meal_categories WHERE name = ?;

-- name: GetAppSetting :one
SELECT value FROM app_settings WHERE key = ?;

-- name: ListAppSettings :many
SELECT * FROM app_settings ORDER BY key;

-- name: SetAppSetting :exec
INSERT INTO app_settings (key, value)
VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
