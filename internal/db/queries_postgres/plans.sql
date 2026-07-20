-- name: GetDayPlan :one
SELECT * FROM day_plans WHERE user_id = $1 AND plan_date = $2;

-- name: GetOrCreateDayPlan :one
-- id is only used for a brand-new row — on conflict, the existing row keeps
-- its original id.
INSERT INTO day_plans (id, user_id, plan_date)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, plan_date) DO UPDATE SET plan_date = EXCLUDED.plan_date
RETURNING *;

-- name: ListDayPlanItems :many
-- Ordered by id as a final tiebreaker (not just category/item sort_order) so
-- that two items which happen to land on the same sort_order — e.g. from a
-- rare concurrent-insert race — still get a stable, repeatable order across
-- queries instead of appearing to reshuffle on every page load.
SELECT day_plan_items.*, meals.name AS meal_name, meal_categories.name AS category_name
FROM day_plan_items
JOIN meals ON meals.id = day_plan_items.meal_id
JOIN meal_categories ON meal_categories.id = day_plan_items.category_id
WHERE day_plan_id = $1
ORDER BY meal_categories.sort_order, day_plan_items.sort_order, day_plan_items.id;

-- name: AddDayPlanItem :one
-- sort_order is computed in the same statement as the insert rather than a
-- separate SELECT MAX + application-side increment, so normal sequential
-- adds (the common case) always get a distinct, increasing sort_order.
INSERT INTO day_plan_items (id, day_plan_id, category_id, meal_id, quantity, sort_order)
VALUES (
    $1, $2, $3, $4, $5,
    COALESCE(
        (SELECT MAX(sort_order) + 1 FROM day_plan_items WHERE day_plan_id = $2 AND category_id = $3),
        0
    )
)
RETURNING *;

-- name: UpdateDayPlanItemQuantity :exec
UPDATE day_plan_items SET quantity = $1 WHERE id = $2;

-- name: RemoveDayPlanItem :exec
DELETE FROM day_plan_items WHERE id = $1;

-- name: GetDayPlanItemOwner :one
-- Used to authorize item mutations: confirms which user's day plan an item
-- belongs to before a delete/quantity-update is allowed to touch it.
SELECT day_plans.user_id, day_plans.id AS day_plan_id
FROM day_plan_items
JOIN day_plans ON day_plans.id = day_plan_items.day_plan_id
WHERE day_plan_items.id = $1;

-- name: GetDayPlanTotals :many
SELECT nutrients.id AS nutrient_id, nutrients.key, nutrients.display_name, nutrients.unit,
       COALESCE(SUM(meal_nutrient_values.amount * day_plan_items.quantity), 0)::numeric AS total
FROM day_plan_items
JOIN meal_nutrient_values ON meal_nutrient_values.meal_id = day_plan_items.meal_id
JOIN nutrients ON nutrients.id = meal_nutrient_values.nutrient_id
WHERE day_plan_items.day_plan_id = $1
GROUP BY nutrients.id, nutrients.key, nutrients.display_name, nutrients.unit, nutrients.sort_order
ORDER BY nutrients.sort_order;

-- name: GetUserNutrientGoals :many
SELECT user_nutrient_goals.*, nutrients.key, nutrients.display_name, nutrients.unit
FROM user_nutrient_goals
JOIN nutrients ON nutrients.id = user_nutrient_goals.nutrient_id
WHERE user_id = $1
ORDER BY nutrients.sort_order;

-- name: SetUserNutrientGoal :exec
INSERT INTO user_nutrient_goals (user_id, nutrient_id, min_value, max_value, ideal_min, ideal_max)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id, nutrient_id) DO UPDATE SET
    min_value = EXCLUDED.min_value, max_value = EXCLUDED.max_value,
    ideal_min = EXCLUDED.ideal_min, ideal_max = EXCLUDED.ideal_max;

-- name: ListDayPlansInRange :many
SELECT * FROM day_plans
WHERE user_id = $1 AND plan_date BETWEEN $2 AND $3
ORDER BY plan_date;

-- name: ListDefaultDayItems :many
SELECT default_day_items.*, meals.name AS meal_name
FROM default_day_items
JOIN meals ON meals.id = default_day_items.meal_id
WHERE user_id = $1
ORDER BY category_id, sort_order, default_day_items.id;

-- name: AddDefaultDayItem :one
INSERT INTO default_day_items (id, user_id, category_id, meal_id, quantity, sort_order)
VALUES (
    $1, $2, $3, $4, $5,
    COALESCE(
        (SELECT MAX(sort_order) + 1 FROM default_day_items WHERE user_id = $2 AND category_id = $3),
        0
    )
)
RETURNING *;

-- name: RemoveDefaultDayItem :execrows
DELETE FROM default_day_items WHERE id = $1 AND user_id = $2;
