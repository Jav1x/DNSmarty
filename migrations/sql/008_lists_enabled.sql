-- +goose Up
-- A disabled list is skipped when snapshots are built: with allow_enabled=false the
-- whitelist is ignored (DNS and proxy answer everyone), deny_enabled=false ignores the blacklist.
ALTER TABLE setting ADD COLUMN allow_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE setting ADD COLUMN deny_enabled boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE setting DROP COLUMN deny_enabled;
ALTER TABLE setting DROP COLUMN allow_enabled;
