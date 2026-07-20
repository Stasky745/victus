-- +goose Up
-- Meal labels: shared/global (like meal_categories), not per-user — the
-- meal library itself is shared across the instance, so tags on it are too.
-- color is a fixed palette key (see mealslib.LabelColors), not a free-form
-- hex value, so every badge in the UI can map straight to a pre-built
-- vx-badge-{color} CSS class instead of interpolating arbitrary colors.
CREATE TABLE meal_labels (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL UNIQUE,
    color      text NOT NULL,
    sort_order smallint NOT NULL DEFAULT 0
);

CREATE TABLE meal_label_assignments (
    meal_id  uuid NOT NULL REFERENCES meals(id) ON DELETE CASCADE,
    label_id uuid NOT NULL REFERENCES meal_labels(id) ON DELETE CASCADE,
    PRIMARY KEY (meal_id, label_id)
);
CREATE INDEX meal_label_assignments_label_id_idx ON meal_label_assignments (label_id);

-- Favorites: also global rather than per-user, consistent with the meal
-- library itself having no per-user ownership boundary elsewhere.
ALTER TABLE meals ADD COLUMN is_favorite boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE meals DROP COLUMN is_favorite;
DROP TABLE meal_label_assignments;
DROP TABLE meal_labels;
