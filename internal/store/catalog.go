package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"dnsmarty/internal/snapshot"
)

var domainName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type DomainLink struct {
	ProxyID string `json:"proxy_id"`
	Weight  int    `json:"weight"`
}

type DomainProxy struct {
	ProxyID   string `json:"proxy_id"`
	ProxyName string `json:"proxy_name"`
	Weight    int    `json:"weight"`
	On        bool   `json:"on"`
}

type Domain struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Match     string        `json:"match"`
	Enabled   bool          `json:"enabled"`
	Comment   string        `json:"comment"`
	Balance   string        `json:"balance"`
	GroupID   string        `json:"group_id"`
	GroupName string        `json:"group_name"`
	Weights   []DomainProxy `json:"weights"`
}

type DomainInput struct {
	Name    string       `json:"name"`
	Match   string       `json:"match"`
	Balance string       `json:"balance"`
	Comment string       `json:"comment"`
	Enabled bool         `json:"enabled"`
	GroupID string       `json:"group_id"`
	Links   []DomainLink `json:"links"`
}

type DomainGroup struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Comment string `json:"comment"`
	Count   int    `json:"count"`
}

type ClientCIDR struct {
	ID      string `json:"id"`
	CIDR    string `json:"cidr"`
	Label   string `json:"label"`
	Kind    string `json:"list_kind"`
	Enabled bool   `json:"enabled"`
}

type Upstream struct {
	ID      string `json:"id"`
	Addr    string `json:"addr"`
	Ordinal int    `json:"ordinal"`
}

type Settings struct {
	TTL             int    `json:"ttl"`
	PullIntervalSec int    `json:"pull_interval_sec"`
	RetentionDays   int    `json:"retention_days"`
	Bootstrap       string `json:"bootstrap_cidr"`
	SessionLimit    int    `json:"session_limit"`
	DialTimeoutMs   int    `json:"dial_timeout_ms"`
	IdleTimeoutMs   int    `json:"idle_timeout_ms"`
	AgentImage      string `json:"agent_image"`
	DNSRateQPS      int    `json:"dns_rate_qps"`
	AuditRetention  int    `json:"audit_retention_days"`
}

func (s *Store) ListDomains(ctx context.Context) ([]Domain, error) {
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
	rows, err := s.pool.Query(ctx, `
		SELECT d.id::text, d.name, d.match_kind, d.enabled, d.comment, d.balance,
		       COALESCE(d.group_id::text, ''), COALESCE(g.name, '')
		FROM domain d
		LEFT JOIN domain_group g ON g.id = d.group_id
		ORDER BY g.name NULLS FIRST, d.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var domains []Domain
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.ID, &d.Name, &d.Match, &d.Enabled, &d.Comment, &d.Balance, &d.GroupID, &d.GroupName); err != nil {
			return nil, err
		}
		domains = append(domains, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if domains == nil {
		domains = []Domain{}
	}
	linkRows, err := s.pool.Query(ctx, `SELECT domain_id::text, proxy_node_id::text, weight FROM domain_proxy`)
	if err != nil {
		return nil, err
	}
	defer linkRows.Close()
	type key struct{ d, p string }
	links := map[key]int{}
	for linkRows.Next() {
		var d, p string
		var w int
		if err := linkRows.Scan(&d, &p, &w); err != nil {
			return nil, err
		}
		links[key{d, p}] = w
	}
	if err := linkRows.Err(); err != nil {
		return nil, err
	}
	for i := range domains {
		domains[i].Weights = make([]DomainProxy, 0, len(proxies))
		for _, p := range proxies {
			w, on := links[key{domains[i].ID, p.ID}]
			if !on {
				w = 1
			}
			domains[i].Weights = append(domains[i].Weights, DomainProxy{
				ProxyID: p.ID, ProxyName: p.Name, Weight: w, On: on,
			})
		}
	}
	return domains, nil
}

func (s *Store) CreateDomain(ctx context.Context, actor string, in DomainInput) error {
	if err := normalizeDomain(&in); err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `
			INSERT INTO domain (name, match_kind, enabled, comment, balance, group_id)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id::text
		`, in.Name, in.Match, in.Enabled, in.Comment, in.Balance, nullUUID(in.GroupID)).Scan(&id)
		if err != nil {
			return mapErr(err)
		}
		if err := replaceLinks(ctx, tx, id, in.Links); err != nil {
			return err
		}
		raw, _ := json.Marshal(map[string]string{"id": id, "name": in.Name})
		return auditTx(ctx, tx, actor, "domain.create", raw)
	})
}

func (s *Store) UpdateDomain(ctx context.Context, actor, id string, in DomainInput) error {
	if err := normalizeDomain(&in); err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE domain SET name = $2, match_kind = $3, enabled = $4, comment = $5, balance = $6, group_id = $7
			WHERE id = $1
		`, id, in.Name, in.Match, in.Enabled, in.Comment, in.Balance, nullUUID(in.GroupID))
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := replaceLinks(ctx, tx, id, in.Links); err != nil {
			return err
		}
		raw, _ := json.Marshal(map[string]string{"id": id, "name": in.Name})
		return auditTx(ctx, tx, actor, "domain.update", raw)
	})
}

func (s *Store) DeleteDomain(ctx context.Context, actor, id string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		var name string
		err := tx.QueryRow(ctx, `SELECT name FROM domain WHERE id = $1`, id).Scan(&name)
		if err != nil {
			return mapErr(err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM domain WHERE id = $1`, id); err != nil {
			return err
		}
		raw, _ := json.Marshal(map[string]string{"id": id, "name": name})
		return auditTx(ctx, tx, actor, "domain.delete", raw)
	})
}

func replaceLinks(ctx context.Context, tx pgx.Tx, domainID string, links []DomainLink) error {
	if _, err := tx.Exec(ctx, `DELETE FROM domain_proxy WHERE domain_id = $1`, domainID); err != nil {
		return err
	}
	for _, l := range links {
		var role string
		err := tx.QueryRow(ctx, `SELECT role FROM node WHERE id = $1`, l.ProxyID).Scan(&role)
		if err != nil {
			return mapErr(err)
		}
		if role != snapshot.RoleProxy {
			return fmt.Errorf("%w: вес только у прокси", ErrInvalid)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO domain_proxy (domain_id, proxy_node_id, weight) VALUES ($1, $2, $3)
		`, domainID, l.ProxyID, l.Weight); err != nil {
			return err
		}
	}
	return nil
}

func nullUUID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func (s *Store) ListGroups(ctx context.Context) ([]DomainGroup, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id::text, g.name, g.comment, COUNT(d.id)
		FROM domain_group g
		LEFT JOIN domain d ON d.group_id = g.id
		GROUP BY g.id
		ORDER BY g.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DomainGroup{}
	for rows.Next() {
		var g DomainGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Comment, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) CreateGroup(ctx context.Context, actor, name, comment string) (string, error) {
	name = strings.TrimSpace(name)
	comment = strings.TrimSpace(comment)
	if len(name) < 1 || len(name) > 64 {
		return "", fmt.Errorf("%w: группа", ErrInvalid)
	}
	if len(comment) > 200 {
		return "", fmt.Errorf("%w: комментарий", ErrInvalid)
	}
	var id string
	err := s.tx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO domain_group (name, comment) VALUES ($1, $2) RETURNING id::text
		`, name, comment).Scan(&id)
		if err != nil {
			return mapErr(err)
		}
		raw, _ := json.Marshal(map[string]string{"id": id, "name": name})
		return auditTx(ctx, tx, actor, "group.create", raw)
	})
	return id, err
}

func (s *Store) DeleteGroup(ctx context.Context, actor, id string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM domain_group WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		raw, _ := json.Marshal(map[string]string{"id": id})
		return auditTx(ctx, tx, actor, "group.delete", raw)
	})
}

func (s *Store) ListClients(ctx context.Context) ([]ClientCIDR, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, cidr::text, label, list_kind, enabled FROM client_cidr ORDER BY list_kind, cidr`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientCIDR{}
	for rows.Next() {
		var c ClientCIDR
		if err := rows.Scan(&c.ID, &c.CIDR, &c.Label, &c.Kind, &c.Enabled); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateClient(ctx context.Context, actor string, cidr, label, kind string, enabled bool) error {
	norm, err := normalizeCIDR(cidr)
	if err != nil {
		return err
	}
	kind, err = normalizeListKind(kind)
	if err != nil {
		return err
	}
	label = strings.TrimSpace(label)
	if len(label) > 80 {
		return fmt.Errorf("%w: подпись", ErrInvalid)
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `
			INSERT INTO client_cidr (cidr, label, list_kind, enabled) VALUES ($1::inet, $2, $3, $4) RETURNING id::text
		`, norm, label, kind, enabled).Scan(&id)
		if err != nil {
			return mapErr(err)
		}
		raw, _ := json.Marshal(map[string]string{"id": id, "cidr": norm, "list_kind": kind})
		return auditTx(ctx, tx, actor, "client.create", raw)
	})
}

func (s *Store) UpdateClient(ctx context.Context, actor, id, cidr, label, kind string, enabled bool) error {
	norm, err := normalizeCIDR(cidr)
	if err != nil {
		return err
	}
	kind, err = normalizeListKind(kind)
	if err != nil {
		return err
	}
	label = strings.TrimSpace(label)
	if len(label) > 80 {
		return fmt.Errorf("%w: подпись", ErrInvalid)
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE client_cidr SET cidr = $2::inet, label = $3, list_kind = $4, enabled = $5 WHERE id = $1`, id, norm, label, kind, enabled)
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		raw, _ := json.Marshal(map[string]string{"id": id, "cidr": norm, "list_kind": kind})
		return auditTx(ctx, tx, actor, "client.update", raw)
	})
}

func (s *Store) DeleteClient(ctx context.Context, actor, id string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM client_cidr WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		raw, _ := json.Marshal(map[string]string{"id": id})
		return auditTx(ctx, tx, actor, "client.delete", raw)
	})
}

func (s *Store) ListUpstreams(ctx context.Context) ([]Upstream, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, addr, ordinal FROM upstream ORDER BY ordinal, addr`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Upstream{}
	for rows.Next() {
		var u Upstream
		if err := rows.Scan(&u.ID, &u.Addr, &u.Ordinal); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CreateUpstream(ctx context.Context, actor, addr string) error {
	norm, err := NormalizeUpstream(addr)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		var ordinal int
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(ordinal), 0) + 1 FROM upstream`).Scan(&ordinal); err != nil {
			return err
		}
		var id string
		err := tx.QueryRow(ctx, `INSERT INTO upstream (addr, ordinal) VALUES ($1, $2) RETURNING id::text`, norm, ordinal).Scan(&id)
		if err != nil {
			return mapErr(err)
		}
		raw, _ := json.Marshal(map[string]string{"id": id, "addr": norm})
		return auditTx(ctx, tx, actor, "upstream.create", raw)
	})
}

func (s *Store) UpdateUpstream(ctx context.Context, actor, id, addr string, ordinal int) error {
	norm, err := NormalizeUpstream(addr)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE upstream SET addr = $2, ordinal = $3 WHERE id = $1`, id, norm, ordinal)
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		raw, _ := json.Marshal(map[string]any{"id": id, "addr": norm, "ordinal": ordinal})
		return auditTx(ctx, tx, actor, "upstream.update", raw)
	})
}

func (s *Store) DeleteUpstream(ctx context.Context, actor, id string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM upstream WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		raw, _ := json.Marshal(map[string]string{"id": id})
		return auditTx(ctx, tx, actor, "upstream.delete", raw)
	})
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	var st Settings
	err := s.pool.QueryRow(ctx, `
		SELECT ttl, pull_interval_sec, retention_days, bootstrap_cidr, session_limit, dial_timeout_ms, idle_timeout_ms, agent_image, dns_rate_qps, audit_retention_days
		FROM setting WHERE id = 1
	`).Scan(&st.TTL, &st.PullIntervalSec, &st.RetentionDays, &st.Bootstrap, &st.SessionLimit, &st.DialTimeoutMs, &st.IdleTimeoutMs, &st.AgentImage, &st.DNSRateQPS, &st.AuditRetention)
	return st, err
}

func (s *Store) SaveSettings(ctx context.Context, actor string, st Settings) error {
	norm, err := normalizeSettings(&st)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE setting SET ttl = $1, pull_interval_sec = $2, retention_days = $3, bootstrap_cidr = $4,
			       session_limit = $5, dial_timeout_ms = $6, idle_timeout_ms = $7, agent_image = $8, dns_rate_qps = $9, audit_retention_days = $10
			WHERE id = 1
		`, st.TTL, st.PullIntervalSec, st.RetentionDays, norm, st.SessionLimit, st.DialTimeoutMs, st.IdleTimeoutMs, st.AgentImage, st.DNSRateQPS, st.AuditRetention)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		raw, _ := json.Marshal(st)
		return auditTx(ctx, tx, actor, "settings.update", raw)
	})
}

func normalizeDomain(in *DomainInput) error {
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	in.Name = strings.TrimSuffix(in.Name, ".")
	if len(in.Name) == 0 || len(in.Name) > 253 || !domainName.MatchString(in.Name) {
		return fmt.Errorf("%w: имя домена", ErrInvalid)
	}
	switch in.Match {
	case snapshot.MatchSuffix, snapshot.MatchFQDN:
	default:
		return fmt.Errorf("%w: тип совпадения", ErrInvalid)
	}
	switch in.Balance {
	case snapshot.BalanceRoundRobin, snapshot.BalanceWeighted, snapshot.BalanceSticky:
	default:
		return fmt.Errorf("%w: стратегия", ErrInvalid)
	}
	in.Comment = strings.TrimSpace(in.Comment)
	if len(in.Comment) > 200 {
		return fmt.Errorf("%w: комментарий", ErrInvalid)
	}
	in.GroupID = strings.TrimSpace(in.GroupID)
	if in.GroupID != "" && !uuidRe.MatchString(in.GroupID) {
		return fmt.Errorf("%w: группа", ErrInvalid)
	}
	for i := range in.Links {
		if in.Links[i].Weight < 1 || in.Links[i].Weight > 1000 {
			return fmt.Errorf("%w: вес", ErrInvalid)
		}
	}
	return nil
}

func normalizeListKind(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "allow"
	}
	if kind != "allow" && kind != "deny" {
		return "", fmt.Errorf("%w: список", ErrInvalid)
	}
	return kind, nil
}

func normalizeCIDR(raw string) (string, error) {
	_, n, err := net.ParseCIDR(strings.TrimSpace(raw))
	if err != nil || n == nil {
		return "", fmt.Errorf("%w: CIDR", ErrInvalid)
	}
	return n.String(), nil
}

func NormalizeUpstream(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	host := addr
	port := "53"
	if h, p, err := net.SplitHostPort(addr); err == nil {
		host, port = h, p
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", fmt.Errorf("%w: upstream должен быть IP", ErrInvalid)
	}
	pn, err := strconv.Atoi(port)
	if err != nil || pn < 1 || pn > 65535 {
		return "", fmt.Errorf("%w: порт upstream", ErrInvalid)
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(pn)), nil
}

func normalizeSettings(st *Settings) (string, error) {
	if st.TTL < 1 || st.TTL > 300 {
		return "", fmt.Errorf("%w: TTL 1–300", ErrInvalid)
	}
	if st.PullIntervalSec < 2 || st.PullIntervalSec > 15 {
		return "", fmt.Errorf("%w: интервал pull 2–15 с", ErrInvalid)
	}
	if st.RetentionDays < 1 || st.RetentionDays > 30 {
		return "", fmt.Errorf("%w: retention 1–30", ErrInvalid)
	}
	if st.SessionLimit < 1 || st.SessionLimit > 10000 {
		return "", fmt.Errorf("%w: лимит сессий", ErrInvalid)
	}
	if st.DialTimeoutMs < 100 || st.DialTimeoutMs > 60000 {
		return "", fmt.Errorf("%w: таймаут dial", ErrInvalid)
	}
	if st.IdleTimeoutMs < 1000 || st.IdleTimeoutMs > 600000 {
		return "", fmt.Errorf("%w: таймаут idle", ErrInvalid)
	}
	if st.AuditRetention < 7 || st.AuditRetention > 3650 {
		return "", fmt.Errorf("%w: хранение аудита", ErrInvalid)
	}
	if st.DNSRateQPS < 0 || st.DNSRateQPS > 100000 {
		return "", fmt.Errorf("%w: лимит DNS", ErrInvalid)
	}
	st.AgentImage = strings.TrimSpace(st.AgentImage)
	if st.AgentImage == "" {
		st.AgentImage = "dnsmarty:local"
	}
	if len(st.AgentImage) > 200 || strings.ContainsAny(st.AgentImage, " \t\n") {
		return "", fmt.Errorf("%w: образ агента", ErrInvalid)
	}
	parts := splitList(st.Bootstrap)
	norm := make([]string, 0, len(parts))
	for _, p := range parts {
		n, err := normalizeCIDR(p)
		if err != nil {
			return "", fmt.Errorf("%w: bootstrap %s", ErrInvalid, p)
		}
		norm = append(norm, n)
	}
	return strings.Join(norm, ", "), nil
}

func splitList(s string) []string {
	s = strings.ReplaceAll(s, ",", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	fields := strings.Fields(s)
	if fields == nil {
		return []string{}
	}
	return fields
}
