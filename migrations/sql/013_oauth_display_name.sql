-- +goose Up
ALTER TABLE user_identity
    ADD COLUMN display_name text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE user_identity
    DROP COLUMN display_name;
