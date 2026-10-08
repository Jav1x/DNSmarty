-- +goose Up
-- A new epoch after a database reset lets agents accept version 1 again instead of rejecting it as old.
ALTER TABLE setting ADD COLUMN snapshot_epoch uuid NOT NULL DEFAULT gen_random_uuid();
-- Per-client DNS answers per second on each DNS node. 0 turns the limit off.
ALTER TABLE setting ADD COLUMN dns_rate_qps integer NOT NULL DEFAULT 50 CHECK (dns_rate_qps BETWEEN 0 AND 100000);
ALTER TABLE node ADD COLUMN agent_version text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE node DROP COLUMN agent_version;
ALTER TABLE setting DROP COLUMN dns_rate_qps;
ALTER TABLE setting DROP COLUMN snapshot_epoch;
