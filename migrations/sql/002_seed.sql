-- +goose Up
INSERT INTO setting (id) VALUES (1);

INSERT INTO domain (name, match_kind, enabled, comment, balance)
VALUES ('example.com', 'suffix', true, 'example, replace with your name', 'round_robin');

-- +goose Down
DELETE FROM domain WHERE name = 'example.com';
DELETE FROM setting WHERE id = 1;
