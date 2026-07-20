-- +goose Up
-- Favorites become per-category: starring a meal while browsing Breakfast
-- shouldn't make it a quick-add under Snack too. Replaces the single global
-- is_favorite boolean with a join table, same shape as meal_label_assignments.
CREATE TABLE meal_favorite_categories (
    meal_id     uuid NOT NULL REFERENCES meals(id) ON DELETE CASCADE,
    category_id uuid NOT NULL REFERENCES meal_categories(id) ON DELETE CASCADE,
    PRIMARY KEY (meal_id, category_id)
);
CREATE INDEX meal_favorite_categories_category_id_idx ON meal_favorite_categories (category_id);

ALTER TABLE meals DROP COLUMN is_favorite;

-- +goose Down
-- Lossy: which categories a meal was favorited for isn't recoverable, only
-- that it was favorited for at least one.
ALTER TABLE meals ADD COLUMN is_favorite boolean NOT NULL DEFAULT false;
UPDATE meals SET is_favorite = true
WHERE id IN (SELECT DISTINCT meal_id FROM meal_favorite_categories);
DROP TABLE meal_favorite_categories;
