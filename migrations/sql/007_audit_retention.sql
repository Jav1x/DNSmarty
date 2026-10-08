-- +goose Up
-- Audit rows older than this are deleted by the hourly maintenance job.
ALTER TABLE setting ADD COLUMN audit_retention_days integer NOT NULL DEFAULT 180
    CHECK (audit_retention_days BETWEEN 7 AND 3650);

-- +goose Down
ALTER TABLE setting DROP COLUMN audit_retention_days;
