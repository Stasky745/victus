-- +goose Up
CREATE TABLE user_nutrient_goals (
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    nutrient_id smallint NOT NULL REFERENCES nutrients(id),
    min_value   numeric,
    max_value   numeric,
    PRIMARY KEY (user_id, nutrient_id)
);

CREATE TABLE day_plans (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_date  date NOT NULL,
    UNIQUE (user_id, plan_date)
);

CREATE TABLE day_plan_items (
    id           uuid PRIMARY KEY,
    day_plan_id  uuid NOT NULL REFERENCES day_plans(id) ON DELETE CASCADE,
    category_id  uuid NOT NULL REFERENCES meal_categories(id),
    meal_id      uuid NOT NULL REFERENCES meals(id),
    quantity     numeric NOT NULL DEFAULT 1,
    sort_order   smallint NOT NULL DEFAULT 0
);
CREATE INDEX day_plan_items_day_plan_id_idx ON day_plan_items (day_plan_id);

CREATE TABLE app_settings (
    key   text PRIMARY KEY,
    value text NOT NULL
);

INSERT INTO app_settings (key, value) VALUES
    ('goal_info_url', 'https://www.myplate.gov/life-stages/healthy-eating-tips');

-- +goose Down
DROP TABLE app_settings;
DROP TABLE day_plan_items;
DROP TABLE day_plans;
DROP TABLE user_nutrient_goals;
