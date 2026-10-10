-- +goose Up
CREATE TABLE oauth_provider (
    provider text PRIMARY KEY,
    client_id text NOT NULL DEFAULT '',
    secret_nonce bytea,
    secret_ciphertext bytea,
    enabled boolean NOT NULL DEFAULT false
);

INSERT INTO oauth_provider (provider) VALUES ('google'), ('github'), ('yandex');

CREATE TABLE user_identity (
    provider text NOT NULL,
    provider_uid text NOT NULL,
    user_id uuid NOT NULL REFERENCES admin_user (id) ON DELETE CASCADE,
    PRIMARY KEY (provider, provider_uid)
);

CREATE INDEX user_identity_user ON user_identity (user_id);

-- +goose Down
DROP TABLE user_identity;
DROP TABLE oauth_provider;
