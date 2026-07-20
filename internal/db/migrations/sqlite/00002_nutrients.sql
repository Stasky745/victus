-- +goose Up
CREATE TABLE nutrients (
    id           smallint PRIMARY KEY,
    key          text UNIQUE NOT NULL,
    display_name text NOT NULL,
    unit         text NOT NULL,
    sort_order   smallint NOT NULL DEFAULT 0
);

INSERT INTO nutrients (id, key, display_name, unit, sort_order) VALUES
    (1,  'calories',      'Calories',      'kcal', 1),
    (2,  'protein_g',     'Protein',       'g',    2),
    (3,  'carbs_g',       'Carbohydrates', 'g',    3),
    (4,  'fat_g',         'Fat',           'g',    4),
    (5,  'saturated_fat_g','Saturated Fat','g',    5),
    (6,  'fiber_g',       'Fiber',         'g',    6),
    (7,  'sugar_g',       'Sugar',         'g',    7),
    (8,  'sodium_mg',     'Sodium',        'mg',   8),
    (9,  'cholesterol_mg','Cholesterol',   'mg',   9),
    (10, 'iron_mg',       'Iron',          'mg',   10);

-- +goose Down
DROP TABLE nutrients;
