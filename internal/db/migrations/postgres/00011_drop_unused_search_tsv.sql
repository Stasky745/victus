-- +goose Up
-- Dead weight: no query ever reads search_tsv (meal search uses
-- word_similarity() against name directly, added after this column). Also
-- keeps the Meal row shape identical to the SQLite engine's, which has no
-- generated-column equivalent (SQLite's meals_fts virtual table doesn't
-- appear in the meals row itself).
DROP INDEX meals_search_tsv_idx;
ALTER TABLE meals DROP COLUMN search_tsv;

-- +goose Down
ALTER TABLE meals ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple', name)) STORED;
CREATE INDEX meals_search_tsv_idx ON meals USING gin (search_tsv);
