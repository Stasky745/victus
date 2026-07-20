-- +goose Up
ALTER TABLE users ALTER COLUMN oidc_subject DROP NOT NULL;
ALTER TABLE users ADD COLUMN password_hash text;
ALTER TABLE users ADD COLUMN is_admin boolean NOT NULL DEFAULT false;
ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);
-- Every user must be able to authenticate somehow: either via an OIDC
-- identity or a local password (or, in principle, both).
ALTER TABLE users ADD CONSTRAINT users_auth_method_chk
    CHECK (oidc_subject IS NOT NULL OR password_hash IS NOT NULL);

-- +goose Down
ALTER TABLE users DROP CONSTRAINT users_auth_method_chk;
ALTER TABLE users DROP CONSTRAINT users_email_key;
ALTER TABLE users DROP COLUMN is_admin;
ALTER TABLE users DROP COLUMN password_hash;
ALTER TABLE users ALTER COLUMN oidc_subject SET NOT NULL;
