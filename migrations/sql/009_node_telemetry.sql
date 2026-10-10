-- +goose Up
ALTER TABLE dns_hit ADD COLUMN IF NOT EXISTS latency_ms integer;
ALTER TABLE node ADD COLUMN IF NOT EXISTS last_hw jsonb;

-- +goose Down
ALTER TABLE node DROP COLUMN last_hw;
ALTER TABLE dns_hit DROP COLUMN latency_ms;
