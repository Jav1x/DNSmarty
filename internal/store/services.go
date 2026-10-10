package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"dnsmarty/internal/snapshot"
)

type ProxyWeight struct {
	ProxyID string `json:"proxy_id"`
	Weight  int    `json:"weight"`
}

type ServiceProxy struct {
	ProxyID   string `json:"proxy_id"`
	ProxyName string `json:"proxy_name"`
	Weight    int    `json:"weight"`
	On        bool   `json:"on"`
}

type Member struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Match      string `json:"match"`
	Enabled    bool   `json:"enabled"`
	Comment    string `json:"comment"`
	Queries24  int64  `json:"queries_24h"`
	Blocked24  int64  `json:"blocked_24h"`
	Sessions24 int64  `json:"sessions_24h"`
	Bytes24    int64  `json:"bytes_24h"`
}

type Service struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Strategy   string         `json:"strategy"`
	Enabled    bool           `json:"enabled"`
	Comment    string         `json:"comment"`
	Members    []Member       `json:"members"`
	Proxies    []ServiceProxy `json:"proxies"`
	Queries24  int64          `json:"queries_24h"`
	Blocked24  int64          `json:"blocked_24h"`
	Sessions24 int64          `json:"sessions_24h"`
	Bytes24    int64          `json:"bytes_24h"`
}

type ServiceInput struct {
	Name     string        `json:"name"`
	Strategy string        `json:"strategy"`
	Comment  string        `json:"comment"`
	Enabled  bool          `json:"enabled"`
	Proxies  []ProxyWeight `json:"proxies"`
}

type MemberInput struct {
	Name    string `json:"name"`
	Match   string `json:"match"`
	Comment string `json:"comment"`
	Enabled bool   `json:"enabled"`
}

const memberDNS24 = `
WITH q AS (
    SELECT rtrim(qname, '.') AS qname,
           count(*) AS n,
           count(*) FILTER (WHERE decision = 'acl') AS acl
    FROM dns_hit
    WHERE at > now() - interval '24 hours' GROUP BY 1
), sfx AS (
    SELECT q.qname, q.n, q.acl, array_to_string(p.parts[i:cardinality(p.parts)], '.') AS suffix
    FROM q
    CROSS JOIN LATERAL (SELECT string_to_array(q.qname, '.') AS parts) p
    CROSS JOIN LATERAL generate_series(1, cardinality(p.parts)) AS i
)
SELECT d.id::text, coalesce(sum(sfx.n), 0)::bigint, coalesce(sum(sfx.acl), 0)::bigint
FROM domain d
LEFT JOIN sfx ON sfx.suffix = d.name AND (d.match_kind = 'suffix' OR sfx.qname = d.name)
GROUP BY d.id`

const memberSessions24 = `
WITH q AS (
    SELECT sni AS nm, count(*) AS n, sum(bytes_up + bytes_down) AS b FROM proxy_session
    WHERE at > now() - interval '24 hours' GROUP BY sni
), sfx AS (
    SELECT q.nm, q.n, q.b, array_to_string(p.parts[i:cardinality(p.parts)], '.') AS suffix
    FROM q
    CROSS JOIN LATERAL (SELECT string_to_array(q.nm, '.') AS parts) p
    CROSS JOIN LATERAL generate_series(1, cardinality(p.parts)) AS i
)
SELECT d.id::text, coalesce(sum(sfx.n), 0)::bigint, coalesce(sum(sfx.b), 0)::bigint
FROM domain d
LEFT JOIN sfx ON sfx.suffix = d.name AND (d.match_kind = 'suffix' OR sfx.nm = d.name)
GROUP BY d.id`

func (s *Store) ListServices(ctx context.Context) ([]Service, error) {
	nodes, err := s.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	var proxies []Node
	for _, n := range nodes {
		if n.Role == snapshot.RoleProxy {
			proxies = append(proxies, n)
		}
	}

	services := []Service{}
	rows, err := s.pool.Query(ctx, `SELECT id::text, name, strategy, enabled, comment FROM service ORDER BY name`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sv Service
		if err := rows.Scan(&sv.ID, &sv.Name, &sv.Strategy, &sv.Enabled, &sv.Comment); err != nil {
			rows.Close()
			return nil, err
		}
		sv.Members = []Member{}
		services = append(services, sv)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	index := map[string]int{}
	for i := range services {
		index[services[i].ID] = i
	}

	rows, err = s.pool.Query(ctx, `SELECT id::text, service_id::text, name, match_kind, enabled, comment FROM domain ORDER BY name`)
	if err != nil {
		return nil, err
	}
	memberIdx := map[string][2]int{}
	for rows.Next() {
		var m Member
		var sid string
		if err := rows.Scan(&m.ID, &sid, &m.Name, &m.Match, &m.Enabled, &m.Comment); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := index[sid]; ok {
			services[i].Members = append(services[i].Members, m)
			memberIdx[m.ID] = [2]int{i, len(services[i].Members) - 1}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := s.fillMemberTraffic(ctx, memberDNS24, func(id string, a, b int64) {
		if at, ok := memberIdx[id]; ok {
			services[at[0]].Members[at[1]].Queries24 = a
			services[at[0]].Members[at[1]].Blocked24 = b
		}
	}); err != nil {
		return nil, err
	}
	if err := s.fillMemberTraffic(ctx, memberSessions24, func(id string, a, b int64) {
		if at, ok := memberIdx[id]; ok {
			services[at[0]].Members[at[1]].Sessions24 = a
			services[at[0]].Members[at[1]].Bytes24 = b
		}
	}); err != nil {
		return nil, err
	}

	weights := map[string]map[string]int{}
	rows, err = s.pool.Query(ctx, `SELECT service_id::text, proxy_node_id::text, weight FROM service_proxy`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sid, pid string
		var w int
		if err := rows.Scan(&sid, &pid, &w); err != nil {
			rows.Close()
			return nil, err
		}
		if weights[sid] == nil {
			weights[sid] = map[string]int{}
		}
		weights[sid][pid] = w
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range services {
		sv := &services[i]
		sv.Proxies = make([]ServiceProxy, 0, len(proxies))
		for _, p := range proxies {
			w, on := weights[sv.ID][p.ID]
			if !on {
				w = 1
			}
			sv.Proxies = append(sv.Proxies, ServiceProxy{ProxyID: p.ID, ProxyName: p.Name, Weight: w, On: on})
		}
		for _, m := range sv.Members {
			sv.Queries24 += m.Queries24
			sv.Blocked24 += m.Blocked24
			sv.Sessions24 += m.Sessions24
			sv.Bytes24 += m.Bytes24
		}
	}
	return services, nil
}

func (s *Store) fillMemberTraffic(ctx context.Context, sql string, set func(id string, a, b int64)) error {
	rows, err := s.pool.Query(ctx, sql)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var a, b int64
		if err := rows.Scan(&id, &a, &b); err != nil {
			return err
		}
		set(id, a, b)
	}
	return rows.Err()
}

func (s *Store) CreateService(ctx context.Context, actor string, in ServiceInput, members []MemberInput) (string, error) {
	if err := normalizeService(&in); err != nil {
		return "", err
	}
	if len(members) == 0 {
		return "", fmt.Errorf("%w: service has no domains", ErrInvalid)
	}
	for i := range members {
		if err := normalizeMember(&members[i]); err != nil {
			return "", err
		}
	}
	var id string
	err := s.tx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO service (name, strategy, enabled, comment) VALUES ($1, $2, $3, $4) RETURNING id::text
		`, in.Name, in.Strategy, in.Enabled, in.Comment).Scan(&id)
		if err != nil {
			return mapErr(err)
		}
		if err := replaceServiceProxies(ctx, tx, id, in.Proxies); err != nil {
			return err
		}
		for _, m := range members {
			if err := insertMember(ctx, tx, actor, id, m); err != nil {
				return err
			}
		}
		raw, _ := json.Marshal(map[string]string{"id": id, "name": in.Name})
		return auditTx(ctx, tx, actor, "service.create", raw)
	})
	return id, err
}

func (s *Store) UpdateService(ctx context.Context, actor, id string, in ServiceInput) error {
	if err := normalizeService(&in); err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE service SET name = $2, strategy = $3, enabled = $4, comment = $5 WHERE id = $1
		`, id, in.Name, in.Strategy, in.Enabled, in.Comment)
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := replaceServiceProxies(ctx, tx, id, in.Proxies); err != nil {
			return err
		}
		raw, _ := json.Marshal(map[string]string{"id": id, "name": in.Name})
		return auditTx(ctx, tx, actor, "service.update", raw)
	})
}

func (s *Store) DeleteService(ctx context.Context, actor, id string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM service WHERE id = $1`, id).Scan(&name); err != nil {
			return mapErr(err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM service WHERE id = $1`, id); err != nil {
			return err
		}
		raw, _ := json.Marshal(map[string]string{"id": id, "name": name})
		return auditTx(ctx, tx, actor, "service.delete", raw)
	})
}

func (s *Store) AddMembers(ctx context.Context, actor, serviceID string, members []MemberInput) error {
	for i := range members {
		if err := normalizeMember(&members[i]); err != nil {
			return err
		}
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM service WHERE id = $1)`, serviceID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		for _, m := range members {
			if err := insertMember(ctx, tx, actor, serviceID, m); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) UpdateMember(ctx context.Context, actor, id string, in MemberInput) error {
	if err := normalizeMember(&in); err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE domain SET name = $2, match_kind = $3, enabled = $4, comment = $5 WHERE id = $1
		`, id, in.Name, in.Match, in.Enabled, in.Comment)
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		raw, _ := json.Marshal(map[string]string{"id": id, "name": in.Name})
		return auditTx(ctx, tx, actor, "domain.update", raw)
	})
}

func (s *Store) DeleteMember(ctx context.Context, actor, id string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM domain WHERE id = $1`, id).Scan(&name); err != nil {
			return mapErr(err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM domain WHERE id = $1`, id); err != nil {
			return err
		}
		raw, _ := json.Marshal(map[string]string{"id": id, "name": name})
		return auditTx(ctx, tx, actor, "domain.delete", raw)
	})
}

func insertMember(ctx context.Context, tx pgx.Tx, actor, serviceID string, m MemberInput) error {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO domain (name, match_kind, enabled, comment, service_id)
		VALUES ($1, $2, $3, $4, $5) RETURNING id::text
	`, m.Name, m.Match, m.Enabled, m.Comment, serviceID).Scan(&id)
	if err != nil {
		return mapErr(err)
	}
	raw, _ := json.Marshal(map[string]string{"id": id, "name": m.Name})
	return auditTx(ctx, tx, actor, "domain.create", raw)
}

func replaceServiceProxies(ctx context.Context, tx pgx.Tx, serviceID string, proxies []ProxyWeight) error {
	if _, err := tx.Exec(ctx, `DELETE FROM service_proxy WHERE service_id = $1`, serviceID); err != nil {
		return err
	}
	for _, p := range proxies {
		if err := requireProxy(ctx, tx, p.ProxyID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO service_proxy (service_id, proxy_node_id, weight) VALUES ($1, $2, $3)
		`, serviceID, p.ProxyID, p.Weight); err != nil {
			return err
		}
	}
	return nil
}

func normalizeService(in *ServiceInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) < 1 || len(in.Name) > 64 {
		return fmt.Errorf("%w: service name", ErrInvalid)
	}
	switch in.Strategy {
	case snapshot.BalanceRoundRobin, snapshot.BalanceWeighted, snapshot.BalanceSticky:
	default:
		return fmt.Errorf("%w: strategy", ErrInvalid)
	}
	in.Comment = strings.TrimSpace(in.Comment)
	if len(in.Comment) > 200 {
		return fmt.Errorf("%w: comment", ErrInvalid)
	}
	for _, p := range in.Proxies {
		if !uuidRe.MatchString(p.ProxyID) {
			return fmt.Errorf("%w: proxy", ErrInvalid)
		}
		if p.Weight < 1 || p.Weight > 1000 {
			return fmt.Errorf("%w: weight", ErrInvalid)
		}
	}
	return nil
}

func normalizeMember(m *MemberInput) error {
	m.Name = strings.ToLower(strings.TrimSpace(m.Name))
	m.Name = strings.TrimSuffix(m.Name, ".")
	if len(m.Name) == 0 || len(m.Name) > 253 || !domainName.MatchString(m.Name) {
		return fmt.Errorf("%w: domain name", ErrInvalid)
	}
	switch m.Match {
	case snapshot.MatchSuffix, snapshot.MatchFQDN:
	default:
		return fmt.Errorf("%w: match type", ErrInvalid)
	}
	m.Comment = strings.TrimSpace(m.Comment)
	if len(m.Comment) > 200 {
		return fmt.Errorf("%w: comment", ErrInvalid)
	}
	return nil
}

func requireProxy(ctx context.Context, tx pgx.Tx, nodeID string) error {
	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM node WHERE id = $1`, nodeID).Scan(&role); err != nil {
		return mapErr(err)
	}
	if role != snapshot.RoleProxy {
		return fmt.Errorf("%w: weight is only for a proxy", ErrInvalid)
	}
	return nil
}

type Template struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type TemplatePayload struct {
	Domains  []MemberInput `json:"domains"`
	Strategy string        `json:"strategy"`
	Proxies  []ProxyWeight `json:"proxies"`
}

func (s *Store) ListTemplates(ctx context.Context) ([]Template, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, name, payload, created_at FROM service_template ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		var t Template
		var raw []byte
		if err := rows.Scan(&t.ID, &t.Name, &raw, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.Payload = raw
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) CreateTemplate(ctx context.Context, actor, name string, p TemplatePayload) (string, error) {
	if err := normalizeService(&ServiceInput{Name: name, Strategy: p.Strategy, Proxies: p.Proxies}); err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	if len(p.Domains) == 0 {
		return "", fmt.Errorf("%w: template has no domains", ErrInvalid)
	}
	for i := range p.Domains {
		if err := normalizeMember(&p.Domains[i]); err != nil {
			return "", err
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	var id string
	err = s.tx(ctx, func(tx pgx.Tx) error {
		for _, px := range p.Proxies {
			if err := requireProxy(ctx, tx, px.ProxyID); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO service_template (name, payload) VALUES ($1, $2) RETURNING id::text`, name, raw).Scan(&id); err != nil {
			return mapErr(err)
		}
		detail, _ := json.Marshal(map[string]string{"id": id, "name": name})
		return auditTx(ctx, tx, actor, "template.create", detail)
	})
	return id, err
}

func (s *Store) DeleteTemplate(ctx context.Context, actor, id string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		var name string
		if err := tx.QueryRow(ctx, `DELETE FROM service_template WHERE id = $1 RETURNING name`, id).Scan(&name); err != nil {
			return mapErr(err)
		}
		detail, _ := json.Marshal(map[string]string{"id": id, "name": name})
		return auditTx(ctx, tx, actor, "template.delete", detail)
	})
}
