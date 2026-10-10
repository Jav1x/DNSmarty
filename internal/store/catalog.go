package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

var domainName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

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

// ListKinds returns whether the allow and deny lists are applied at all. The UI
// greys the whole list block out when a list is off.
func (s *Store) ListKinds(ctx context.Context) (allowOn, denyOn bool, err error) {
	err = s.pool.QueryRow(ctx, `SELECT allow_enabled, deny_enabled FROM setting WHERE id = 1`).Scan(&allowOn, &denyOn)
	return allowOn, denyOn, err
}

// SetListKinds toggles whole lists and writes the change to the audit log.
// The next push cycle delivers the change to the nodes.
func (s *Store) SetListKinds(ctx context.Context, actor string, allowOn, denyOn bool) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE setting SET allow_enabled = $1, deny_enabled = $2 WHERE id = 1`, allowOn, denyOn); err != nil {
			return err
		}
		raw, _ := json.Marshal(map[string]bool{"allow_enabled": allowOn, "deny_enabled": denyOn})
		return auditTx(ctx, tx, actor, "lists.toggle", raw)
	})
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

var errUpstreamForm = fmt.Errorf("%w: upstream: IP, host:853 (DoT) или https://… (DoH)", ErrInvalid)

func NormalizeUpstream(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if strings.HasPrefix(addr, "https://") {
		u, err := url.Parse(addr)
		if err != nil || u.Host == "" {
			return "", errUpstreamForm
		}
		return addr, nil
	}
	rest, tlsScheme := strings.CutPrefix(addr, "tls://")
	host, port := rest, ""
	if h, p, err := net.SplitHostPort(rest); err == nil {
		host, port = h, p
	}
	ip := net.ParseIP(host)
	if port == "" {
		switch {
		case tlsScheme:
			port = "853"
		case ip != nil:
			port = "53"
		default:
			return "", errUpstreamForm
		}
	}
	pn, err := strconv.Atoi(port)
	if err != nil || pn < 1 || pn > 65535 {
		return "", fmt.Errorf("%w: порт upstream", ErrInvalid)
	}
	if tlsScheme || pn == 853 {
		if ip != nil {
			return net.JoinHostPort(ip.String(), port), nil
		}
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		if host == "" {
			return "", errUpstreamForm
		}
		return net.JoinHostPort(host, port), nil
	}
	if ip == nil {
		return "", errUpstreamForm
	}
	return net.JoinHostPort(ip.String(), port), nil
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
