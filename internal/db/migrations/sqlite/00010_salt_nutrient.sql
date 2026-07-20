-- +goose Up
-- Salt, tracked separately from Sodium: most packaged-food labels show salt
-- (g), not sodium (mg), and Open Food Facts carries both as distinct fields
-- (see internal/importers/openfoodfacts) — this avoids a lossy mg<->g,
-- sodium<->salt conversion on every manual entry.
INSERT INTO nutrients (id, key, display_name, unit, sort_order) VALUES
    (11, 'salt_g', 'Salt', 'g', 11);

-- +goose Down
DELETE FROM nutrients WHERE id = 11;
