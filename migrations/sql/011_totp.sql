-- +goose Up
ALTER TABLE admin_user
    ADD COLUMN totp_nonce bytea,
    ADD COLUMN totp_ciphertext bytea,
    ADD COLUMN totp_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN totp_last_step bigint;

CREATE TABLE recovery_code (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES admin_user (id) ON DELETE CASCADE,
    code_hash bytea NOT NULL UNIQUE,
    code_nonce bytea NOT NULL,
    code_ciphertext bytea NOT NULL,
    used_at timestamptz,
    ordinal smallint NOT NULL CHECK (ordinal BETWEEN 1 AND 8)
);

CREATE TABLE login_ticket (
    token_sha256 bytea PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES admin_user (id) ON DELETE CASCADE,
    attempts integer NOT NULL DEFAULT 0,
    expires_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE login_ticket;
DROP TABLE recovery_code;
ALTER TABLE admin_user
    DROP COLUMN totp_last_step,
    DROP COLUMN totp_enabled,
    DROP COLUMN totp_ciphertext,
    DROP COLUMN totp_nonce;
