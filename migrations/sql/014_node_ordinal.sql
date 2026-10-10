-- +goose Up
ALTER TABLE node
    ADD COLUMN ordinal integer NOT NULL DEFAULT 0;

UPDATE node n
SET ordinal = sub.n
FROM (
    SELECT id, row_number() OVER (ORDER BY role, name) AS n
    FROM node
) sub
WHERE n.id = sub.id;

-- +goose Down
ALTER TABLE node
    DROP COLUMN ordinal;
