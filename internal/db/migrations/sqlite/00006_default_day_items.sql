-- +goose Up
-- A user's "Default Day" template: meals auto-applied to any day they
-- haven't touched yet (see planning.Store's getDay materialization). No
-- uniqueness constraint on (user_id, category_id) — multiple default items
-- per category is intentional, same as a real day plan.
CREATE TABLE default_day_items (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category_id uuid NOT NULL REFERENCES meal_categories(id) ON DELETE CASCADE,
    meal_id     uuid NOT NULL REFERENCES meals(id) ON DELETE CASCADE,
    quantity    numeric NOT NULL DEFAULT 1,
    sort_order  smallint NOT NULL DEFAULT 0
);
CREATE INDEX default_day_items_user_id_idx ON default_day_items (user_id);

-- +goose Down
DROP TABLE default_day_items;
