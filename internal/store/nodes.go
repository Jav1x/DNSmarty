package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"dnsmarty/internal/snapshot"
)

type Node struct {
	ID            string          `json:"id"`
	Role          string          `json:"role"`
	Name          string          `json:"name"`
	PublicIPv4    string          `json:"public_ipv4"`
	PublicIPv6    string          `json:"public_ipv6"`
	Region        string          `json:"region"`
	Enabled       bool            `json:"enabled"`
	Ordinal       int             `json:"ordinal"`
	ConfigVersion int64           `json:"config_version"`
	LastSeen      *time.Time      `json:"last_seen_at"`
	AgentHost     string          `json:"agent_host"`
	AgentPort     int             `json:"agent_port"`
	LastError     string          `json:"last_error"`
	AgentVersion  string          `json:"agent_version"`
	LastHW        json.RawMessage `json:"last_hw"`
	// Fresh: enabled and heard from within the live window.
	Fresh      bool  `json:"fresh"`
	Queries24  int64 `json:"queries_24h"`
	Sessions24 int64 `json:"sessions_24h"`
	Bytes24    int64 `json:"bytes_24h"`
}

// liveWindow is how long a node counts as alive after the last successful push:
// three push intervals, at least 30 s. A couple of slow cycles must not drop proxies
// out of DNS answers.
const liveWindow = `make_interval(secs => (SELECT greatest(30, 3 * pull_interval_sec) FROM setting WHERE id = 1))`

const nodeFresh = `(enabled AND last_seen_at IS NOT NULL AND last_seen_at > now() - ` + liveWindow + `)`

// LiveWindowSec mirrors the SQL liveWindow expression: three push intervals, at least 30 s.
// The panel exposes it to the UI and to the "needs update" badge computation.
func LiveWindowSec(pullInterval int) int {
	w := 3 * pullInterval
	if w < 30 {
		w = 30
	}
	return w
}

type NodeInput struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	IPv4      string `json:"public_ipv4"`
	IPv6      string `json:"public_ipv6"`
	Region    string `json:"region"`
	AgentHost string `json:"agent_host"`
	AgentPort int    `json:"agent_port"`
	Enabled   bool   `json:"enabled"`
}

func (s *Store) ListNodes(ctx context.Context) ([]Node, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, role, name,
		       coalesce(host(public_ipv4), ''), coalesce(host(public_ipv6), ''),
		       region, enabled, ordinal, config_version, last_seen_at,
		       agent_host, agent_port, last_error, agent_version, coalesce(last_hw::text, 'null'), `+nodeFresh+`
		FROM node
		ORDER BY ordinal, role, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		var hw string
		if err := rows.Scan(&n.ID, &n.Role, &n.Name, &n.PublicIPv4, &n.PublicIPv6, &n.Region, &n.Enabled, &n.Ordinal, &n.ConfigVersion, &n.LastSeen, &n.AgentHost, &n.AgentPort, &n.LastError, &n.AgentVersion, &hw, &n.Fresh); err != nil {
			return nil, err
		}
		n.LastHW = json.RawMessage(hw)
		out = append(out, n)
	}
	if out == nil {
		out = []Node{}
	}
	return out, rows.Err()
}

func (s *Store) CreateNode(ctx context.Context, actor string, in NodeInput) (keyHex string, node Node, err error) {
	if err := normalizeNode(&in); err != nil {
		return "", Node{}, err
	}
	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		return "", Node{}, err
	}
	nonce, ct, err := s.seal(rawKey)
	if err != nil {
		return "", Node{}, err
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		var ord int
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(ordinal), 0) + 1 FROM node`).Scan(&ord); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO node (role, name, public_ipv4, public_ipv6, region, enabled, ordinal, agent_host, agent_port, key_nonce, key_ciphertext)
			VALUES ($1, $2, $3::inet, $4::inet, $5, $6, $7, $8, $9, $10, $11)
			RETURNING id::text, role, name, coalesce(host(public_ipv4), ''), coalesce(host(public_ipv6), ''),
			          region, enabled, ordinal, config_version, last_seen_at, agent_host, agent_port, last_error, agent_version
		`, in.Role, in.Name, inetOrNil(in.IPv4), inetOrNil(in.IPv6), in.Region, in.Enabled, ord, in.AgentHost, in.AgentPort, nonce, ct).Scan(
			&node.ID, &node.Role, &node.Name, &node.PublicIPv4, &node.PublicIPv6, &node.Region, &node.Enabled, &node.Ordinal, &node.ConfigVersion, &node.LastSeen, &node.AgentHost, &node.AgentPort, &node.LastError, &node.AgentVersion,
		)
		if err != nil {
			return mapErr(err)
		}
		raw, _ := json.Marshal(map[string]any{"id": node.ID, "name": node.Name, "role": node.Role, "agent_host": node.AgentHost, "agent_port": node.AgentPort})
		return auditTx(ctx, tx, actor, "node.create", raw)
	})
	if err != nil {
		return "", Node{}, err
	}
	return hex.EncodeToString(rawKey), node, nil
}

func (s *Store) UpdateNode(ctx context.Context, actor, id string, in NodeInput) error {
	if err := normalizeNode(&in); err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		var was bool
		if err := tx.QueryRow(ctx, `SELECT enabled FROM node WHERE id = $1 FOR UPDATE`, id).Scan(&was); err != nil {
			return mapErr(err)
		}
		tag, err := tx.Exec(ctx, `
			UPDATE node
			SET role = $2, name = $3, public_ipv4 = $4::inet, public_ipv6 = $5::inet, region = $6, enabled = $7,
			    agent_host = $8, agent_port = $9
			WHERE id = $1
		`, id, in.Role, in.Name, inetOrNil(in.IPv4), inetOrNil(in.IPv6), in.Region, in.Enabled, in.AgentHost, in.AgentPort)
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		raw, _ := json.Marshal(map[string]any{"id": id, "name": in.Name, "enabled": in.Enabled})
		if err := auditTx(ctx, tx, actor, "node.update", raw); err != nil {
			return err
		}
		if in.Enabled == was {
			return nil
		}
		action := "node.disable"
		if in.Enabled {
			action = "node.enable"
		}
		return auditTx(ctx, tx, actor, action, raw)
	})
}

func (s *Store) RotateKey(ctx context.Context, actor, id string) (string, error) {
	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		return "", err
	}
	nonce, ct, err := s.seal(rawKey)
	if err != nil {
		return "", err
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE node SET key_nonce = $2, key_ciphertext = $3, last_error = '' WHERE id = $1`, id, nonce, ct)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		raw, _ := json.Marshal(map[string]string{"id": id})
		return auditTx(ctx, tx, actor, "node.key", raw)
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(rawKey), nil
}

func (s *Store) NodeKey(ctx context.Context, id string) ([]byte, error) {
	var nonce, ct []byte
	err := s.pool.QueryRow(ctx, `SELECT key_nonce, key_ciphertext FROM node WHERE id = $1`, id).Scan(&nonce, &ct)
	if err != nil {
		return nil, mapErr(err)
	}
	if len(nonce) == 0 || len(ct) == 0 {
		return nil, fmt.Errorf("%w: key", ErrNotFound)
	}
	return s.open(nonce, ct)
}

// SetNodeHW stores the hardware snapshot; an empty raw clears it to NULL.
func (s *Store) SetNodeHW(ctx context.Context, nodeID string, raw json.RawMessage) error {
	var v any
	if len(raw) > 0 {
		v = []byte(raw)
	}
	_, err := s.pool.Exec(ctx, `UPDATE node SET last_hw = $2 WHERE id = $1`, nodeID, v)
	return err
}

func (s *Store) SetReachable(ctx context.Context, id string, version int64, agentVersion string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE node SET last_seen_at = now(), config_version = $2, agent_version = $3, last_error = '' WHERE id = $1
	`, id, version, clip(agentVersion, 64))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetUnreachable(ctx context.Context, id, msg string) error {
	if len(msg) > 400 {
		msg = msg[:400]
	}
	tag, err := s.pool.Exec(ctx, `UPDATE node SET last_error = $2 WHERE id = $1`, id, msg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderNodes writes the rotation order: ids must be a permutation of every node.
func (s *Store) ReorderNodes(ctx context.Context, actor string, ids []string) error {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, err := parseUUID(id); err != nil {
			return fmt.Errorf("%w: order", ErrInvalid)
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("%w: order", ErrInvalid)
		}
		seen[id] = struct{}{}
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM node`).Scan(&n); err != nil {
			return err
		}
		if n != len(ids) {
			return fmt.Errorf("%w: order", ErrInvalid)
		}
		if n == 0 {
			return nil
		}
		tag, err := tx.Exec(ctx, `
			UPDATE node AS n
			SET ordinal = v.ord
			FROM unnest($1::text[]) WITH ORDINALITY AS v(id, ord)
			WHERE n.id::text = v.id
		`, ids)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != int64(n) {
			return fmt.Errorf("%w: order", ErrInvalid)
		}
		raw, _ := json.Marshal(map[string]any{"ids": ids})
		return auditTx(ctx, tx, actor, "node.reorder", raw)
	})
}

func (s *Store) DeleteNode(ctx context.Context, actor, id string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		var name string
		err := tx.QueryRow(ctx, `SELECT name FROM node WHERE id = $1`, id).Scan(&name)
		if err != nil {
			return mapErr(err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM node WHERE id = $1`, id); err != nil {
			return err
		}
		raw, _ := json.Marshal(map[string]string{"id": id, "name": name})
		return auditTx(ctx, tx, actor, "node.delete", raw)
	})
}

func normalizeNode(in *NodeInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Region = strings.TrimSpace(in.Region)
	if len(in.Name) < 1 || len(in.Name) > 64 {
		return fmt.Errorf("%w: node name", ErrInvalid)
	}
	switch in.Role {
	case snapshot.RoleDNS, snapshot.RoleProxy:
	default:
		return fmt.Errorf("%w: role", ErrInvalid)
	}
	if err := optionalIP(&in.IPv4); err != nil {
		return fmt.Errorf("%w: IPv4", ErrInvalid)
	}
	if err := optionalIP(&in.IPv6); err != nil {
		return fmt.Errorf("%w: IPv6", ErrInvalid)
	}
	if len(in.Region) > 64 {
		return fmt.Errorf("%w: region", ErrInvalid)
	}
	in.AgentHost = strings.TrimSpace(in.AgentHost)
	if in.AgentHost == "" || len(in.AgentHost) > 253 || strings.ContainsAny(in.AgentHost, " /\\") {
		return fmt.Errorf("%w: agent address", ErrInvalid)
	}
	if in.AgentPort < 1 || in.AgentPort > 65535 {
		return fmt.Errorf("%w: agent port", ErrInvalid)
	}
	return nil
}

func (s *Store) GetNode(ctx context.Context, id string) (Node, error) {
	var n Node
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, role, name,
		       coalesce(host(public_ipv4), ''), coalesce(host(public_ipv6), ''),
		       region, enabled, ordinal, config_version, last_seen_at,
		       agent_host, agent_port, last_error, agent_version, `+nodeFresh+`
		FROM node WHERE id = $1
	`, id).Scan(&n.ID, &n.Role, &n.Name, &n.PublicIPv4, &n.PublicIPv6, &n.Region, &n.Enabled, &n.Ordinal, &n.ConfigVersion, &n.LastSeen, &n.AgentHost, &n.AgentPort, &n.LastError, &n.AgentVersion, &n.Fresh)
	if err != nil {
		return Node{}, mapErr(err)
	}
	return n, nil
}

func optionalIP(s *string) error {
	*s = strings.TrimSpace(*s)
	if *s == "" {
		return nil
	}
	ip := net.ParseIP(*s)
	if ip == nil {
		return fmt.Errorf("ip")
	}
	*s = ip.String()
	return nil
}
