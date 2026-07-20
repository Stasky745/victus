-- +goose Up
-- An optional narrower "sweet spot" inside min/max — e.g. sugar might have
-- no min, an ideal max of 30g, and a hard max of 50g. Both nullable and
-- independent of each other (an ideal ceiling with no ideal floor, like
-- sugar, is a normal, valid configuration), same as min_value/max_value.
ALTER TABLE user_nutrient_goals ADD COLUMN ideal_min numeric;
ALTER TABLE user_nutrient_goals ADD COLUMN ideal_max numeric;

-- +goose Down
ALTER TABLE user_nutrient_goals DROP COLUMN ideal_min;
ALTER TABLE user_nutrient_goals DROP COLUMN ideal_max;
