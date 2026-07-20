-- +goose Up
CREATE TABLE meal_categories (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL UNIQUE,
    sort_order smallint NOT NULL DEFAULT 0
);

INSERT INTO meal_categories (name, sort_order) VALUES
    ('Breakfast', 1),
    ('Lunch', 2),
    ('Dinner', 3),
    ('Snacks', 4);

CREATE TABLE meals (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name           text NOT NULL,
    source         text NOT NULL CHECK (source IN ('manual', 'off', 'mealie', 'tandoor')),
    source_ref     text,
    -- Victus deliberately doesn't store recipes/instructions — this is the
    -- way back to them: a Mealie/Tandoor recipe page (derived from
    -- base_url + source_ref at import time) or any URL a user pastes in
    -- manually (a blog post, a video, etc).
    recipe_url     text,
    serving_label  text NOT NULL DEFAULT 'per serving',
    serving_amount numeric NOT NULL DEFAULT 1,
    created_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    search_tsv     tsvector GENERATED ALWAYS AS (to_tsvector('simple', name)) STORED
);
CREATE INDEX meals_name_trgm_idx ON meals USING gin (name gin_trgm_ops);
CREATE INDEX meals_search_tsv_idx ON meals USING gin (search_tsv);
CREATE UNIQUE INDEX meals_source_ref_idx ON meals (source, source_ref) WHERE source_ref IS NOT NULL;

CREATE TABLE meal_nutrient_values (
    meal_id     uuid NOT NULL REFERENCES meals(id) ON DELETE CASCADE,
    nutrient_id smallint NOT NULL REFERENCES nutrients(id),
    amount      numeric NOT NULL,
    PRIMARY KEY (meal_id, nutrient_id)
);

-- +goose Down
DROP TABLE meal_nutrient_values;
DROP TABLE meals;
DROP TABLE meal_categories;
