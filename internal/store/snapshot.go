package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"dnsmarty/internal/snapshot"
)

func (s *Store) DNSSnapshot(ctx context.Context) (snapshot.DNS, error) {
	var out snapshot.DNS
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(714253)`); err != nil {
			return err
		}
		body, err := loadDNSBody(ctx, tx)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		version, err := publishDNS(ctx, tx, raw)
		if err != nil {
			return err
		}
		body.Version = version
		out = body
		return nil
	})
	if err != nil {
		return snapshot.DNS{}, err
	}
	return out, nil
}

func (s *Store) ProxySnapshot(ctx context.Context, nodeID string) (snapshot.ProxySnap, error) {
	var out snapshot.ProxySnap
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(714254)`); err != nil {
			return err
		}
		body, err := loadProxyBody(ctx, tx, nodeID)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		version, err := publishProxy(ctx, tx, nodeID, raw)
		if err != nil {
			return err
		}
		body.Version = version
		out = body
		return nil
	})
	return out, err
}

func loadDNSBody(ctx context.Context, tx pgx.Tx) (snapshot.DNS, error) {
	var body snapshot.DNS
	var ttl, pull, rate int
	if err := tx.QueryRow(ctx, `SELECT snapshot_epoch::text, ttl, pull_interval_sec, dns_rate_qps FROM setting WHERE id = 1`).Scan(&body.Epoch, &ttl, &pull, &rate); err != nil {
		return body, err
	}
	body.TTL = uint32(ttl)
	body.PullIntervalSec = pull
	body.RateQPS = rate
	var err error
	if body.Allow, body.Deny, body.Bootstrap, err = loadACL(ctx, tx); err != nil {
		return body, err
	}

	rows, err := tx.Query(ctx, `SELECT addr FROM upstream ORDER BY ordinal, addr`)
	if err != nil {
		return body, err
	}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			rows.Close()
			return body, err
		}
		body.Upstreams = append(body.Upstreams, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return body, err
	}
	rows.Close()

	type drow struct {
		id, name, match, balance string
	}
	var domains []drow
	rows, err = tx.Query(ctx, `SELECT id::text, name, match_kind, balance FROM domain WHERE enabled ORDER BY name`)
	if err != nil {
		return body, err
	}
	for rows.Next() {
		var d drow
		if err := rows.Scan(&d.id, &d.name, &d.match, &d.balance); err != nil {
			rows.Close()
			return body, err
		}
		domains = append(domains, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return body, err
	}
	rows.Close()

	rows, err = tx.Query(ctx, `
		SELECT dp.domain_id::text, n.id::text,
		       coalesce(host(n.public_ipv4), ''), coalesce(host(n.public_ipv6), ''), dp.weight
		FROM domain_proxy dp
		JOIN node n ON n.id = dp.proxy_node_id
		JOIN domain d ON d.id = dp.domain_id
		WHERE d.enabled
		  AND n.enabled
		  AND n.role = 'proxy'
		  AND n.last_seen_at IS NOT NULL
		  AND n.last_seen_at > now() - `+liveWindow+`
		  AND (n.public_ipv4 IS NOT NULL OR n.public_ipv6 IS NOT NULL)
		ORDER BY n.id::text
	`)
	if err != nil {
		return body, err
	}
	proxies := map[string][]snapshot.Proxy{}
	for rows.Next() {
		var domainID string
		var p snapshot.Proxy
		if err := rows.Scan(&domainID, &p.ID, &p.IPv4, &p.IPv6, &p.Weight); err != nil {
			rows.Close()
			return body, err
		}
		proxies[domainID] = append(proxies[domainID], p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return body, err
	}
	rows.Close()

	// Keep the domain when no proxy is live. Forwarding it would expose the real origin.
	for _, d := range domains {
		item := snapshot.Domain{Name: d.name, Match: d.match, Balance: d.balance, Proxies: proxies[d.id]}
		body.Domains = append(body.Domains, item)
	}
	snapshot.EmptySlicesDNS(&body)
	return body, nil
}

func loadProxyBody(ctx context.Context, tx pgx.Tx, nodeID string) (snapshot.ProxySnap, error) {
	var body snapshot.ProxySnap
	var enabled bool
	err := tx.QueryRow(ctx, `
		SELECT enabled, coalesce(host(public_ipv4), ''), coalesce(host(public_ipv6), '')
		FROM node WHERE id = $1 AND role = 'proxy'
	`, nodeID).Scan(&enabled, &body.PublicIPv4, &body.PublicIPv6)
	if err != nil {
		return body, mapErr(err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT snapshot_epoch::text, pull_interval_sec, session_limit, dial_timeout_ms, idle_timeout_ms
		FROM setting WHERE id = 1
	`).Scan(&body.Epoch, &body.PullIntervalSec, &body.SessionLimit, &body.DialTimeoutMs, &body.IdleTimeoutMs); err != nil {
		return body, err
	}
	rows, err := tx.Query(ctx, `SELECT addr FROM upstream ORDER BY ordinal, addr`)
	if err != nil {
		return body, err
	}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			rows.Close()
			return body, err
		}
		body.Upstreams = append(body.Upstreams, a)
	}
	rows.Close()
	if body.Allow, body.Deny, body.Bootstrap, err = loadACL(ctx, tx); err != nil {
		return body, err
	}
	if enabled {
		rows, err = tx.Query(ctx, `
			SELECT d.name, d.match_kind
			FROM domain d
			JOIN domain_proxy dp ON dp.domain_id = d.id
			WHERE dp.proxy_node_id = $1 AND d.enabled
			ORDER BY d.name
		`, nodeID)
		if err != nil {
			return body, err
		}
		for rows.Next() {
			var rule snapshot.NameRule
			if err := rows.Scan(&rule.Name, &rule.Match); err != nil {
				rows.Close()
				return body, err
			}
			body.Names = append(body.Names, rule)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return body, err
		}
		rows.Close()
	}
	snapshot.EmptySlicesProxy(&body)
	return body, nil
}

// loadACL returns the enabled client lists and the bootstrap CIDRs. DNS and proxy snapshots share them.
func loadACL(ctx context.Context, tx pgx.Tx) (allow, deny, bootstrap []string, err error) {
	var boot string
	if err := tx.QueryRow(ctx, `SELECT bootstrap_cidr FROM setting WHERE id = 1`).Scan(&boot); err != nil {
		return nil, nil, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT cidr::text, list_kind FROM client_cidr WHERE enabled ORDER BY cidr::text`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c, kind string
		if err := rows.Scan(&c, &kind); err != nil {
			return nil, nil, nil, err
		}
		if kind == "deny" {
			deny = append(deny, c)
		} else {
			allow = append(allow, c)
		}
	}
	return allow, deny, splitList(boot), rows.Err()
}

func publishDNS(ctx context.Context, tx pgx.Tx, raw []byte) (int64, error) {
	var version int64
	var same bool
	err := tx.QueryRow(ctx, `
		SELECT version, body = $1::jsonb
		FROM dns_snapshot
		ORDER BY version DESC
		LIMIT 1
	`, raw).Scan(&version, &same)
	if errors.Is(err, pgx.ErrNoRows) {
		version = 0
		same = false
	} else if err != nil {
		return 0, err
	}
	if same {
		return version, nil
	}
	var next int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO dns_snapshot (version, body) VALUES ($1, $2::jsonb) RETURNING version
	`, version+1, raw).Scan(&next); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM dns_snapshot WHERE version < $1`, next-50); err != nil {
		return 0, err
	}
	return next, nil
}

func publishProxy(ctx context.Context, tx pgx.Tx, nodeID string, raw []byte) (int64, error) {
	var version int64
	var same bool
	err := tx.QueryRow(ctx, `
		SELECT version, body = $2::jsonb
		FROM proxy_snapshot
		WHERE node_id = $1
		ORDER BY version DESC
		LIMIT 1
	`, nodeID, raw).Scan(&version, &same)
	if errors.Is(err, pgx.ErrNoRows) {
		version = 0
		same = false
	} else if err != nil {
		return 0, err
	}
	if same {
		return version, nil
	}
	var next int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO proxy_snapshot (node_id, version, body) VALUES ($1, $2, $3::jsonb) RETURNING version
	`, nodeID, version+1, raw).Scan(&next); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM proxy_snapshot WHERE node_id = $1 AND version < $2`, nodeID, next-50); err != nil {
		return 0, err
	}
	return next, nil
}

// LatestSnapshotVersion is the newest version published for a role, 0 if none.
// For proxies it is per node.
func (s *Store) LatestSnapshotVersion(ctx context.Context, role, nodeID string) (int64, error) {
	var v int64
	var err error
	if role == snapshot.RoleProxy {
		err = s.pool.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM proxy_snapshot WHERE node_id = $1`, nodeID).Scan(&v)
	} else {
		err = s.pool.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM dns_snapshot`).Scan(&v)
	}
	return v, err
}

// RotateSnapshotEpoch starts a new epoch. Agents then accept the next snapshot whatever its version.
// The panel calls it when an agent holds a version this database never published, i.e. after a restore.
func (s *Store) RotateSnapshotEpoch(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE setting SET snapshot_epoch = gen_random_uuid() WHERE id = 1`)
	return err
}
