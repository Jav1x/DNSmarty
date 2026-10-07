-- +goose Up
ALTER TABLE node ADD COLUMN agent_host text NOT NULL DEFAULT '';
ALTER TABLE node ADD COLUMN agent_port integer NOT NULL DEFAULT 0;
ALTER TABLE node ADD COLUMN key_nonce bytea;
ALTER TABLE node ADD COLUMN key_ciphertext bytea;
ALTER TABLE node ADD COLUMN last_error text NOT NULL DEFAULT '';
ALTER TABLE node DROP COLUMN token_sha256;
ALTER TABLE setting ADD COLUMN agent_image text NOT NULL DEFAULT 'dnsmarty:local';

-- +goose Down
ALTER TABLE setting DROP COLUMN agent_image;
ALTER TABLE node ADD COLUMN token_sha256 bytea;
ALTER TABLE node DROP COLUMN last_error;
ALTER TABLE node DROP COLUMN key_ciphertext;
ALTER TABLE node DROP COLUMN key_nonce;
ALTER TABLE node DROP COLUMN agent_port;
ALTER TABLE node DROP COLUMN agent_host;
