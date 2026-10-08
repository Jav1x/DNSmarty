-- +goose Up
ALTER TABLE session ADD COLUMN last_seen_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE session ADD COLUMN ip inet;
ALTER TABLE session ADD COLUMN user_agent text NOT NULL DEFAULT '';
ALTER TABLE session DROP COLUMN flash;
CREATE INDEX session_user_idx ON session (user_id);
ALTER TABLE admin_user ADD COLUMN password_changed_at timestamptz NOT NULL DEFAULT now();

-- +goose Down
ALTER TABLE admin_user DROP COLUMN password_changed_at;
DROP INDEX session_user_idx;
ALTER TABLE session ADD COLUMN flash text NOT NULL DEFAULT '';
ALTER TABLE session DROP COLUMN user_agent;
ALTER TABLE session DROP COLUMN ip;
ALTER TABLE session DROP COLUMN last_seen_at;
