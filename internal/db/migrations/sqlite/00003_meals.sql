-- +goose Up
CREATE TABLE meal_categories (
    id         uuid PRIMARY KEY,
    name       text NOT NULL UNIQUE,
    sort_order smallint NOT NULL DEFAULT 0
);

-- SQLite has no built-in UUID generator; this assembles one in the same
-- canonical 8-4-4-4-12 hyphenated form uuid.UUID's driver.Valuer produces
-- (via google/uuid's Value()/String()) — a bare lower(hex(randomblob(16)))
-- (no hyphens) would store a byte-identical id in a textually different
-- format, silently breaking every foreign-key/equality comparison against
-- an application-generated uuid.UUID.
INSERT INTO meal_categories (id, name, sort_order) VALUES
    (lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(6))), 'Breakfast', 1),
    (lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(6))), 'Lunch', 2),
    (lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(6))), 'Dinner', 3),
    (lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(6))), 'Snacks', 4);

CREATE TABLE meals (
    id             uuid PRIMARY KEY,
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
    created_at     timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX meals_source_ref_idx ON meals (source, source_ref) WHERE source_ref IS NOT NULL;

-- Fuzzy/typo-tolerant name search: an external-content FTS5 table (indexing
-- meals.name without duplicating storage) using the trigram tokenizer, kept
-- in sync by triggers below — SQLite's nearest equivalent to Postgres's
-- pg_trgm + generated tsvector column. See internal/mealslib for how the
-- app queries this (an OR'd trigram MATCH expression, not a raw substring).
CREATE VIRTUAL TABLE meals_fts USING fts5(name, content='meals', content_rowid='rowid', tokenize='trigram');

-- +goose StatementBegin
CREATE TRIGGER meals_fts_ai AFTER INSERT ON meals BEGIN
    INSERT INTO meals_fts(rowid, name) VALUES (new.rowid, new.name);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER meals_fts_ad AFTER DELETE ON meals BEGIN
    INSERT INTO meals_fts(meals_fts, rowid, name) VALUES ('delete', old.rowid, old.name);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER meals_fts_au AFTER UPDATE ON meals BEGIN
    INSERT INTO meals_fts(meals_fts, rowid, name) VALUES ('delete', old.rowid, old.name);
    INSERT INTO meals_fts(rowid, name) VALUES (new.rowid, new.name);
END;
-- +goose StatementEnd

CREATE TABLE meal_nutrient_values (
    meal_id     uuid NOT NULL REFERENCES meals(id) ON DELETE CASCADE,
    nutrient_id smallint NOT NULL REFERENCES nutrients(id),
    amount      numeric NOT NULL,
    PRIMARY KEY (meal_id, nutrient_id)
);

-- +goose Down
DROP TABLE meal_nutrient_values;
DROP TRIGGER meals_fts_au;
DROP TRIGGER meals_fts_ad;
DROP TRIGGER meals_fts_ai;
DROP TABLE meals_fts;
DROP TABLE meals;
DROP TABLE meal_categories;
