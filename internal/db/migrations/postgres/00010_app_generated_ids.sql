-- +goose Up
-- Ids are now generated in application code (uuid.New()) rather than by the
-- database, so the same insert path works unchanged against SQLite (which
-- has no gen_random_uuid()-equivalent function of its own).
ALTER TABLE users ALTER COLUMN id DROP DEFAULT;
ALTER TABLE sessions ALTER COLUMN id DROP DEFAULT;
ALTER TABLE meal_categories ALTER COLUMN id DROP DEFAULT;
ALTER TABLE meals ALTER COLUMN id DROP DEFAULT;
ALTER TABLE day_plans ALTER COLUMN id DROP DEFAULT;
ALTER TABLE day_plan_items ALTER COLUMN id DROP DEFAULT;
ALTER TABLE default_day_items ALTER COLUMN id DROP DEFAULT;
ALTER TABLE meal_labels ALTER COLUMN id DROP DEFAULT;
-- pgcrypto's only use was gen_random_uuid(), now unused.
DROP EXTENSION IF EXISTS pgcrypto;

-- +goose Down
CREATE EXTENSION IF NOT EXISTS pgcrypto;
ALTER TABLE users ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE sessions ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE meal_categories ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE meals ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE day_plans ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE day_plan_items ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE default_day_items ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE meal_labels ALTER COLUMN id SET DEFAULT gen_random_uuid();
