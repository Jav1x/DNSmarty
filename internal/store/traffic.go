package store

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"dnsmarty/internal/snapshot"
)

var partName = regexp.MustCompile(`^(dns_hit|proxy_session)_([0-9]{8})$`)

type DNSHit struct {
	At       time.Time
	ClientIP string
	QName    string
	QType    string
	Rcode    string
	Decision string
}

type ProxyReport struct {
	At        time.Time
	ClientIP  string
	SNI       string
	BytesUp   int64
	BytesDown int64
	Status    string
	DialError string
}

type DNSLog struct {
	At       time.Time `json:"at"`
	ClientIP string    `json:"client_ip"`
	Name     string    `json:"name"`
	QType    string    `json:"qtype"`
	Rcode    string    `json:"rcode"`
	Decision string    `json:"decision"`
}

type ProxyLog struct {
	At        time.Time `json:"at"`
	ClientIP  string    `json:"client_ip"`
	SNI       string    `json:"sni"`
	BytesUp   int64     `json:"bytes_up"`
	BytesDown int64     `json:"bytes_down"`
	Status    string    `json:"status"`
	DialError string    `json:"dial_error"`
}

type AuditRow struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Detail string    `json:"detail"`
}

type BayCell struct {
	State string `json:"state"`
}

type BayRow struct {
	Name    string    `json:"name"`
	Balance string    `json:"balance"`
	Enabled bool      `json:"enabled"`
	Cells   []BayCell `json:"cells"`
}

type Overview struct {
	QPS        int64    `json:"qps"`
	Refused    int64    `json:"refused"`
	Sessions   int64    `json:"sessions"`
	Bytes      int64    `json:"bytes"`
	Stale      int64    `json:"stale"`
	Nodes      []Node   `json:"nodes"`
	BayProxies []Node   `json:"bay_proxies"`
	Rows       []BayRow `json:"rows"`
}

func (s *Store) InsertHits(ctx context.Context, nodeID string, hits []DNSHit) error {
	if len(hits) == 0 {
		return nil
	}
	if len(hits) > 500 {
		return fmt.Errorf("%w: batch", ErrInvalid)
	}
	if err := s.MaintainPartitions(ctx); err != nil {
		return err
	}
	batch := &pgx.Batch{}
	for _, h := range hits {
		if h.At.IsZero() {
			h.At = time.Now().UTC()
		}
		at := h.At.UTC()
		day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
		var ip any
		if h.ClientIP != "" {
			if net.ParseIP(h.ClientIP) == nil {
				return fmt.Errorf("%w: client ip", ErrInvalid)
			}
			ip = h.ClientIP
		}
		batch.Queue(`
			INSERT INTO dns_hit (day, at, node_id, client_ip, qname, qtype, rcode, decision)
			VALUES ($1, $2, $3, $4::inet, $5, $6, $7, $8)
		`, day, at, nodeID, ip, clip(h.QName, 255), clip(h.QType, 16), clip(h.Rcode, 32), clip(h.Decision, 16))
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range hits {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) InsertSessions(ctx context.Context, nodeID string, rowsIn []ProxyReport) error {
	if len(rowsIn) == 0 {
		return nil
	}
	if len(rowsIn) > 500 {
		return fmt.Errorf("%w: batch", ErrInvalid)
	}
	if err := s.MaintainPartitions(ctx); err != nil {
		return err
	}
	batch := &pgx.Batch{}
	for _, h := range rowsIn {
		if h.At.IsZero() {
			h.At = time.Now().UTC()
		}
		at := h.At.UTC()
		day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
		var ip any
		if h.ClientIP != "" {
			if net.ParseIP(h.ClientIP) == nil {
				return fmt.Errorf("%w: client ip", ErrInvalid)
			}
			ip = h.ClientIP
		}
		batch.Queue(`
			INSERT INTO proxy_session (day, at, node_id, client_ip, sni, bytes_up, bytes_down, status, dial_error)
			VALUES ($1, $2, $3, $4::inet, $5, $6, $7, $8, $9)
		`, day, at, nodeID, ip, clip(h.SNI, 255), h.BytesUp, h.BytesDown, clip(h.Status, 32), clip(h.DialError, 300))
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range rowsIn {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) MaintainPartitions(ctx context.Context) error {
	var retention int
	if err := s.pool.QueryRow(ctx, `SELECT retention_days FROM setting WHERE id = 1`).Scan(&retention); err != nil {
		return err
	}
	if retention < 1 {
		retention = 7
	}
	y, m, d := time.Now().UTC().Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	for i := -1; i <= 2; i++ {
		day := today.AddDate(0, 0, i)
		if err := s.ensurePart(ctx, "dns_hit", day); err != nil {
			return err
		}
		if err := s.ensurePart(ctx, "proxy_session", day); err != nil {
			return err
		}
	}
	cutoff := today.AddDate(0, 0, -(retention - 1))
	if err := s.dropOlder(ctx, "dns_hit", cutoff); err != nil {
		return err
	}
	if err := s.dropOlder(ctx, "proxy_session", cutoff); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM session WHERE expires_at < now()`)
	return err
}

func (s *Store) ensurePart(ctx context.Context, parent string, day time.Time) error {
	if parent != "dns_hit" && parent != "proxy_session" {
		return fmt.Errorf("partition parent")
	}
	name := parent + "_" + day.UTC().Format("20060102")
	from := day.UTC().Format("2006-01-02")
	to := day.UTC().AddDate(0, 0, 1).Format("2006-01-02")
	q := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')`,
		name, parent, from, to,
	)
	_, err := s.pool.Exec(ctx, q)
	return err
}

func (s *Store) dropOlder(ctx context.Context, parent string, cutoff time.Time) error {
	rows, err := s.pool.Query(ctx, `
		SELECT c.relname
		FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname = $1
	`, parent)
	if err != nil {
		return err
	}
	defer rows.Close()
	var drop []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		m := partName.FindStringSubmatch(name)
		if m == nil || m[1] != parent {
			continue
		}
		pt, err := time.Parse("20060102", m[2])
		if err != nil {
			continue
		}
		if pt.Before(cutoff) {
			drop = append(drop, name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range drop {
		if _, err := s.pool.Exec(ctx, `DROP TABLE IF EXISTS `+name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Overview(ctx context.Context) (Overview, error) {
	var o Overview
	err := s.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM dns_hit WHERE at > now() - interval '60 seconds'),
		  (SELECT count(*) FROM dns_hit WHERE at > now() - interval '60 seconds' AND decision = 'acl'),
		  (SELECT count(*) FROM proxy_session WHERE at > now() - interval '60 seconds'),
		  (SELECT coalesce(sum(bytes_up + bytes_down), 0) FROM proxy_session WHERE at > now() - interval '24 hours'),
		  (SELECT count(*) FROM node WHERE enabled AND (last_seen_at IS NULL OR last_seen_at < now() - interval '30 seconds'))
	`).Scan(&o.QPS, &o.Refused, &o.Sessions, &o.Bytes, &o.Stale)
	if err != nil {
		return Overview{}, err
	}
	o.Nodes, err = s.ListNodes(ctx)
	if err != nil {
		return Overview{}, err
	}
	for _, n := range o.Nodes {
		if n.Role == snapshot.RoleProxy {
			o.BayProxies = append(o.BayProxies, n)
		}
	}
	if o.BayProxies == nil {
		o.BayProxies = []Node{}
	}
	domains, err := s.ListDomains(ctx)
	if err != nil {
		return Overview{}, err
	}
	now := time.Now()
	for _, d := range domains {
		row := BayRow{Name: d.Name, Balance: d.Balance, Enabled: d.Enabled, Cells: []BayCell{}}
		for _, p := range o.BayProxies {
			state := "empty"
			for _, w := range d.Weights {
				if w.ProxyID == p.ID && w.On {
					state = "dead"
					if p.Fresh(now) && (p.PublicIPv4 != "" || p.PublicIPv6 != "") {
						state = "live"
					}
					break
				}
			}
			row.Cells = append(row.Cells, BayCell{State: state})
		}
		o.Rows = append(o.Rows, row)
	}
	if o.Rows == nil {
		o.Rows = []BayRow{}
	}
	return o, nil
}

func (s *Store) DNSLogs(ctx context.Context, name, ip string) ([]DNSLog, error) {
	where, args, err := logFilter(name, ip, "qname")
	if err != nil {
		return nil, err
	}
	q := `SELECT at, coalesce(host(client_ip), ''), qname, qtype, rcode, decision FROM dns_hit WHERE ` + where + ` ORDER BY at DESC LIMIT 200`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DNSLog{}
	for rows.Next() {
		var row DNSLog
		if err := rows.Scan(&row.At, &row.ClientIP, &row.Name, &row.QType, &row.Rcode, &row.Decision); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ProxyLogs(ctx context.Context, name, ip string) ([]ProxyLog, error) {
	where, args, err := logFilter(name, ip, "sni")
	if err != nil {
		return nil, err
	}
	q := `SELECT at, coalesce(host(client_ip), ''), sni, bytes_up, bytes_down, status, dial_error FROM proxy_session WHERE ` + where + ` ORDER BY at DESC LIMIT 200`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProxyLog{}
	for rows.Next() {
		var row ProxyLog
		if err := rows.Scan(&row.At, &row.ClientIP, &row.SNI, &row.BytesUp, &row.BytesDown, &row.Status, &row.DialError); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) Audit(ctx context.Context) ([]AuditRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT at, actor, action, detail::text FROM audit_log ORDER BY at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditRow{}
	for rows.Next() {
		var row AuditRow
		if err := rows.Scan(&row.At, &row.Actor, &row.Action, &row.Detail); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func logFilter(name, ip, nameCol string) (string, []any, error) {
	if nameCol != "qname" && nameCol != "sni" {
		return "", nil, fmt.Errorf("column")
	}
	clauses := []string{"TRUE"}
	var args []any
	if name != "" {
		args = append(args, likeContains(name))
		clauses = append(clauses, fmt.Sprintf("%s ILIKE $%d ESCAPE '\\'", nameCol, len(args)))
	}
	ip = strings.TrimSpace(ip)
	if ip != "" {
		if _, n, err := net.ParseCIDR(ip); err == nil {
			args = append(args, n.String())
			clauses = append(clauses, fmt.Sprintf("client_ip <<= $%d::inet", len(args)))
		} else if parsed := net.ParseIP(ip); parsed != nil {
			args = append(args, parsed.String())
			clauses = append(clauses, fmt.Sprintf("client_ip = $%d::inet", len(args)))
		} else {
			return "", nil, fmt.Errorf("%w: IP", ErrInvalid)
		}
	}
	return strings.Join(clauses, " AND "), args, nil
}

func likeContains(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return "%" + s + "%"
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
