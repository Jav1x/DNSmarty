-- +goose Up
CREATE TABLE service (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    strategy text NOT NULL DEFAULT 'weighted'
        CHECK (strategy IN ('round_robin', 'weighted', 'sticky24')),
    enabled boolean NOT NULL DEFAULT true,
    comment text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE service_proxy (
    service_id uuid NOT NULL REFERENCES service (id) ON DELETE CASCADE,
    proxy_node_id uuid NOT NULL REFERENCES node (id) ON DELETE CASCADE,
    weight integer NOT NULL CHECK (weight > 0),
    PRIMARY KEY (service_id, proxy_node_id)
);

CREATE TABLE service_template (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE domain ADD COLUMN service_id uuid REFERENCES service (id) ON DELETE CASCADE;

CREATE TEMP TABLE dsig AS
SELECT d.id AS domain_id, d.group_id, d.name, d.balance,
       coalesce((SELECT string_agg(dp.proxy_node_id::text || ':' || dp.weight, ',' ORDER BY dp.proxy_node_id::text)
                 FROM domain_proxy dp WHERE dp.domain_id = d.id), '') AS links
FROM domain d;

-- +goose StatementBegin
CREATE FUNCTION migrate_svc_name(base text) RETURNS text LANGUAGE plpgsql AS $$
DECLARE
    cand text := base;
    i int := 1;
BEGIN
    WHILE EXISTS (SELECT 1 FROM service WHERE name = cand) LOOP
        i := i + 1;
        cand := base || ' (' || i || ')';
    END LOOP;
    RETURN cand;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE
    r record;
    sid uuid;
    rep uuid;
BEGIN
    FOR r IN
        SELECT p.group_id, p.balance, p.links, g.name AS gname
        FROM (SELECT group_id, balance, links, min(name) AS first_name
              FROM dsig WHERE group_id IS NOT NULL
              GROUP BY group_id, balance, links) p
        JOIN domain_group g ON g.id = p.group_id
        ORDER BY g.name, p.first_name
    LOOP
        INSERT INTO service (name, strategy)
        VALUES (migrate_svc_name(r.gname), r.balance)
        RETURNING id INTO sid;
        SELECT domain_id INTO rep FROM dsig
        WHERE group_id = r.group_id AND balance = r.balance AND links = r.links
        ORDER BY name LIMIT 1;
        INSERT INTO service_proxy (service_id, proxy_node_id, weight)
        SELECT sid, proxy_node_id, weight FROM domain_proxy WHERE domain_id = rep;
        UPDATE domain d SET service_id = sid
        FROM dsig s
        WHERE s.domain_id = d.id AND s.group_id = r.group_id AND s.balance = r.balance AND s.links = r.links;
    END LOOP;

    FOR r IN SELECT domain_id, name, balance FROM dsig WHERE group_id IS NULL ORDER BY name LOOP
        INSERT INTO service (name, strategy)
        VALUES (migrate_svc_name(r.name), r.balance)
        RETURNING id INTO sid;
        INSERT INTO service_proxy (service_id, proxy_node_id, weight)
        SELECT sid, proxy_node_id, weight FROM domain_proxy WHERE domain_id = r.domain_id;
        UPDATE domain SET service_id = sid WHERE id = r.domain_id;
    END LOOP;
END
$$;
-- +goose StatementEnd

DROP FUNCTION migrate_svc_name(text);
DROP TABLE dsig;

ALTER TABLE domain ALTER COLUMN service_id SET NOT NULL;
ALTER TABLE domain DROP COLUMN group_id;
ALTER TABLE domain DROP COLUMN balance;
DROP TABLE domain_proxy;
DROP TABLE domain_group;

-- +goose Down
CREATE TABLE domain_group (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    comment text NOT NULL DEFAULT ''
);

CREATE TABLE domain_proxy (
    domain_id uuid NOT NULL REFERENCES domain (id) ON DELETE CASCADE,
    proxy_node_id uuid NOT NULL REFERENCES node (id) ON DELETE CASCADE,
    weight integer NOT NULL CHECK (weight > 0),
    PRIMARY KEY (domain_id, proxy_node_id)
);

ALTER TABLE domain ADD COLUMN group_id uuid REFERENCES domain_group (id) ON DELETE SET NULL;
ALTER TABLE domain ADD COLUMN balance text;
UPDATE domain d SET balance = s.strategy FROM service s WHERE s.id = d.service_id;
ALTER TABLE domain ALTER COLUMN balance SET NOT NULL;
ALTER TABLE domain ADD CONSTRAINT domain_balance_check
    CHECK (balance IN ('round_robin', 'weighted', 'sticky24'));

INSERT INTO domain_proxy (domain_id, proxy_node_id, weight)
SELECT d.id, sp.proxy_node_id, sp.weight
FROM domain d JOIN service_proxy sp ON sp.service_id = d.service_id;

INSERT INTO domain_group (name)
SELECT s.name FROM service s
WHERE (SELECT count(*) FROM domain d WHERE d.service_id = s.id) > 1;
UPDATE domain d SET group_id = g.id
FROM service s JOIN domain_group g ON g.name = s.name
WHERE d.service_id = s.id AND (SELECT count(*) FROM domain x WHERE x.service_id = s.id) > 1;

ALTER TABLE domain DROP COLUMN service_id;
DROP TABLE service_template;
DROP TABLE service_proxy;
DROP TABLE service;
