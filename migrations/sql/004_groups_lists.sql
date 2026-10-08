-- +goose Up
CREATE TABLE domain_group (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    comment text NOT NULL DEFAULT ''
);

ALTER TABLE domain
    ADD COLUMN group_id uuid REFERENCES domain_group (id) ON DELETE SET NULL;

ALTER TABLE client_cidr
    ADD COLUMN list_kind text NOT NULL DEFAULT 'allow' CHECK (list_kind IN ('allow', 'deny'));

-- +goose Down
ALTER TABLE client_cidr DROP COLUMN list_kind;
ALTER TABLE domain DROP COLUMN group_id;
DROP TABLE domain_group;
