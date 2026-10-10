package store

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"dnsmarty/internal/snapshot"
)

var partName = regexp.MustCompile(`^(dns_hit|proxy_session)_([0-9]{8})$`)

type DNSHit struct {
	At        time.Time
	ClientIP  string
	QName     string
	QType     string
	Rcode     string
	Decision  string
	LatencyMS *int
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
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	ClientIP  string    `json:"client_ip"`
	Name      string    `json:"name"`
	QType     string    `json:"qtype"`
	Rcode     string    `json:"rcode"`
	Decision  string    `json:"decision"`
	LatencyMS *int      `json:"latency_ms"`
}

type ProxyLog struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	ClientIP  string    `json:"client_ip"`
	SNI       string    `json:"sni"`
	BytesUp   int64     `json:"bytes_up"`
	BytesDown int64     `json:"bytes_down"`
	Status    string    `json:"status"`
	DialError string    `json:"dial_error"`
}

type AuditRow struct {
	ID     int64     `json:"id"`
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
	LatencyAvg *float64 `json:"latency_avg_ms"`
	LatencyP95 *float64 `json:"latency_p95_ms"`
}

// Page is a keyset cursor: rows older than (Before, BeforeID), newest first.
type Page struct {
	Before   time.Time
	BeforeID int64
	Limit    int
}

// Cursor is the position to fetch the next page from.
type Cursor struct {
	At time.Time `json:"at"`
	ID int64     `json:"id"`
}

// SeriesPoint is one time bucket of the overview chart.
type SeriesPoint struct {
	T         time.Time `json:"t"`
	DNS       int64     `json:"dns"`
	Refused   int64     `json:"refused"`
	Sessions  int64     `json:"sessions"`
	Bytes     int64     `json:"bytes"`
	LatencyMS *float64  `json:"latency_ms"`
}

// insertChunk bounds one COPY. The agent buffers up to 2000 rows per kind between pushes.
const insertChunk = 1000

// InsertHits stores DNS log rows with COPY. A row with a malformed address is skipped rather
// than failing the batch: the agent has already handed the rows over and cannot resend them.
func (s *Store) InsertHits(ctx context.Context, nodeID string, hits []DNSHit) (skipped int, err error) {
	node, err := parseUUID(nodeID)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	rows := make([][]any, 0, len(hits))
	for _, h := range hits {
		ip, ok := ipOrNull(h.ClientIP)
		if !ok {
			skipped++
			continue
		}
		at := clampAt(h.At, now)
		rows = append(rows, []any{dayOf(at), at, node, ip,
			clip(h.QName, 255), clip(h.QType, 16), clip(h.Rcode, 32), clip(h.Decision, 16), h.LatencyMS})
	}
	cols := []string{"day", "at", "node_id", "client_ip", "qname", "qtype", "rcode", "decision", "latency_ms"}
	return skipped, s.copyChunks(ctx, "dns_hit", cols, rows)
}

func (s *Store) InsertSessions(ctx context.Context, nodeID string, reports []ProxyReport) (skipped int, err error) {
	node, err := parseUUID(nodeID)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	rows := make([][]any, 0, len(reports))
	for _, r := range reports {
		ip, ok := ipOrNull(r.ClientIP)
		if !ok {
			skipped++
			continue
		}
		at := clampAt(r.At, now)
		rows = append(rows, []any{dayOf(at), at, node, ip,
			clip(r.SNI, 255), r.BytesUp, r.BytesDown, clip(r.Status, 32), clip(r.DialError, 300)})
	}
	cols := []string{"day", "at", "node_id", "client_ip", "sni", "bytes_up", "bytes_down", "status", "dial_error"}
	return skipped, s.copyChunks(ctx, "proxy_session", cols, rows)
}

func (s *Store) copyChunks(ctx context.Context, table string, cols []string, rows [][]any) error {
	for len(rows) > 0 {
		n := min(len(rows), insertChunk)
		if _, err := s.pool.CopyFrom(ctx, pgx.Identifier{table}, cols, pgx.CopyFromRows(rows[:n])); err != nil {
			return err
		}
		rows = rows[n:]
	}
	return nil
}

// clampAt keeps a row inside the partitions that exist. An agent with a wrong clock
// would otherwise write into a day with no partition and lose the whole batch.
func clampAt(at, now time.Time) time.Time {
	if at.IsZero() || at.Before(now.Add(-time.Hour)) || at.After(now.Add(5*time.Minute)) {
		return now
	}
	return at.UTC()
}

func dayOf(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ipOrNull returns nil for an empty address and false for one that does not parse.
func ipOrNull(s string) (any, bool) {
	if s == "" {
		return nil, true
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return nil, false
	}
	return a.Unmap(), true
}

func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return u, fmt.Errorf("%w: node id", ErrInvalid)
	}
	return u, nil
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
	if _, err := s.pool.Exec(ctx, `DELETE FROM session WHERE expires_at < now() OR last_seen_at < now() - make_interval(secs => $1)`, SessionIdle.Seconds()); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `
		DELETE FROM audit_log
		WHERE at < now() - make_interval(days => (SELECT audit_retention_days FROM setting WHERE id = 1))
	`)
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
		  (SELECT count(*) FROM node WHERE enabled AND NOT `+nodeFresh+`),
		  (SELECT avg(latency_ms)::float8 FROM dns_hit WHERE at > now() - interval '60 seconds'),
		  (SELECT percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms)::float8 FROM dns_hit WHERE at > now() - interval '60 seconds')
	`).Scan(&o.QPS, &o.Refused, &o.Sessions, &o.Bytes, &o.Stale, &o.LatencyAvg, &o.LatencyP95)
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
	for _, d := range domains {
		row := BayRow{Name: d.Name, Balance: d.Balance, Enabled: d.Enabled, Cells: []BayCell{}}
		for _, p := range o.BayProxies {
			state := "empty"
			for _, w := range d.Weights {
				if w.ProxyID == p.ID && w.On {
					state = "dead"
					if p.Fresh && (p.PublicIPv4 != "" || p.PublicIPv6 != "") {
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

func (s *Store) DNSLogs(ctx context.Context, name, ip, decision string, p Page) ([]DNSLog, *Cursor, error) {
	clauses, args, err := dnsFilter(name, ip, decision)
	if err != nil {
		return nil, nil, err
	}
	clauses, args = pageClause(p, clauses, args)
	rows, err := s.pool.Query(ctx, `SELECT id, at, coalesce(host(client_ip), ''), qname, qtype, rcode, decision, latency_ms FROM dns_hit WHERE `+strings.Join(clauses, " AND ")+` ORDER BY at DESC, id DESC LIMIT `+strconv.Itoa(p.Limit+1), args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out, cur, err := scanDNS(rows, p.Limit)
	if err != nil {
		return nil, nil, err
	}
	return out, cur, nil
}

func (s *Store) ProxyLogs(ctx context.Context, name, ip, status string, p Page) ([]ProxyLog, *Cursor, error) {
	clauses, args, err := proxyFilter(name, ip, status)
	if err != nil {
		return nil, nil, err
	}
	clauses, args = pageClause(p, clauses, args)
	rows, err := s.pool.Query(ctx, `SELECT id, at, coalesce(host(client_ip), ''), sni, bytes_up, bytes_down, status, dial_error FROM proxy_session WHERE `+strings.Join(clauses, " AND ")+` ORDER BY at DESC, id DESC LIMIT `+strconv.Itoa(p.Limit+1), args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out, cur, err := scanProxy(rows, p.Limit)
	if err != nil {
		return nil, nil, err
	}
	return out, cur, nil
}

func (s *Store) Audit(ctx context.Context, p Page) ([]AuditRow, *Cursor, error) {
	clauses, args := pageClause(p, []string{"TRUE"}, nil)
	rows, err := s.pool.Query(ctx, `SELECT id, at, actor, action, detail::text FROM audit_log WHERE `+strings.Join(clauses, " AND ")+` ORDER BY at DESC, id DESC LIMIT `+strconv.Itoa(p.Limit+1), args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out, cur, err := scanAudit(rows, p.Limit)
	if err != nil {
		return nil, nil, err
	}
	return out, cur, nil
}

// Series returns one point per step across the window, newest last, with zero buckets filled in.
// It groups DNS hits and proxy sessions by time, aligned to the Unix epoch so buckets are stable.
func (s *Store) Series(ctx context.Context, window, step time.Duration) ([]SeriesPoint, error) {
	rows, err := s.pool.Query(ctx, `
		WITH d AS (
			SELECT date_bin(make_interval(secs => $2), at, timestamptz '2000-01-01') AS t,
			       count(*) AS n, count(*) FILTER (WHERE decision = 'acl') AS ref,
			       avg(latency_ms)::float8 AS lat
			FROM dns_hit
			WHERE at > now() - make_interval(secs => $1)
			GROUP BY 1
		), p AS (
			SELECT date_bin(make_interval(secs => $2), at, timestamptz '2000-01-01') AS t,
			       count(*) AS n, coalesce(sum(bytes_up + bytes_down), 0) AS b
			FROM proxy_session
			WHERE at > now() - make_interval(secs => $1)
			GROUP BY 1
		)
		SELECT coalesce(d.t, p.t), coalesce(d.n, 0), coalesce(d.ref, 0), coalesce(p.n, 0), coalesce(p.b, 0), d.lat
		FROM d FULL OUTER JOIN p ON d.t = p.t
		ORDER BY 1
	`, window.Seconds(), step.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	buckets := map[time.Time]*SeriesPoint{}
	for rows.Next() {
		var t time.Time
		var pt SeriesPoint
		if err := rows.Scan(&t, &pt.DNS, &pt.Refused, &pt.Sessions, &pt.Bytes, &pt.LatencyMS); err != nil {
			return nil, err
		}
		pt.T = t
		buckets[t] = &pt
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []SeriesPoint{}
	now := time.Now()
	end := now.Truncate(step)
	start := end.Add(-window)
	for t := start; !t.After(end); t = t.Add(step) {
		if pt, ok := buckets[t]; ok {
			out = append(out, *pt)
		} else {
			out = append(out, SeriesPoint{T: t})
		}
	}
	return out, nil
}

// pageClause appends the keyset condition for a page cursor.
func pageClause(p Page, clauses []string, args []any) ([]string, []any) {
	if p.Limit < 1 {
		p.Limit = 100
	}
	if !p.Before.IsZero() {
		args = append(args, p.Before, p.BeforeID)
		clauses = append(clauses, fmt.Sprintf("(at, id) < ($%d::timestamptz, $%d)", len(args)-1, len(args)))
	}
	return clauses, args
}

// scanRows reads up to limit+1 rows and, when the extra row exists, builds the next cursor
// from it. The caller keeps limit rows and uses the cursor to continue.
func scanDNS(rows pgx.Rows, limit int) ([]DNSLog, *Cursor, error) {
	out := make([]DNSLog, 0, limit)
	for rows.Next() {
		var r DNSLog
		if err := rows.Scan(&r.ID, &r.At, &r.ClientIP, &r.Name, &r.QType, &r.Rcode, &r.Decision, &r.LatencyMS); err != nil {
			return nil, nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var cur *Cursor
	if len(out) > limit {
		last := out[limit]
		cur = &Cursor{At: last.At, ID: last.ID}
		out = out[:limit]
	}
	return out, cur, nil
}

func scanProxy(rows pgx.Rows, limit int) ([]ProxyLog, *Cursor, error) {
	out := make([]ProxyLog, 0, limit)
	for rows.Next() {
		var r ProxyLog
		if err := rows.Scan(&r.ID, &r.At, &r.ClientIP, &r.SNI, &r.BytesUp, &r.BytesDown, &r.Status, &r.DialError); err != nil {
			return nil, nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var cur *Cursor
	if len(out) > limit {
		last := out[limit]
		cur = &Cursor{At: last.At, ID: last.ID}
		out = out[:limit]
	}
	return out, cur, nil
}

func scanAudit(rows pgx.Rows, limit int) ([]AuditRow, *Cursor, error) {
	out := make([]AuditRow, 0, limit)
	for rows.Next() {
		var r AuditRow
		if err := rows.Scan(&r.ID, &r.At, &r.Actor, &r.Action, &r.Detail); err != nil {
			return nil, nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var cur *Cursor
	if len(out) > limit {
		last := out[limit]
		cur = &Cursor{At: last.At, ID: last.ID}
		out = out[:limit]
	}
	return out, cur, nil
}

func dnsFilter(name, ip, decision string) ([]string, []any, error) {
	clauses := []string{"TRUE"}
	var args []any
	if name != "" {
		args = append(args, likeContains(name))
		clauses = append(clauses, fmt.Sprintf("qname ILIKE $%d ESCAPE '\\'", len(args)))
	}
	if decision != "" {
		args = append(args, decision)
		clauses = append(clauses, fmt.Sprintf("decision = $%d", len(args)))
	}
	if extra, ea, err := ipClause(ip, len(args)); err != nil {
		return nil, nil, err
	} else {
		clauses = append(clauses, extra...)
		args = append(args, ea...)
	}
	return clauses, args, nil
}

func proxyFilter(name, ip, status string) ([]string, []any, error) {
	clauses := []string{"TRUE"}
	var args []any
	if name != "" {
		args = append(args, likeContains(name))
		clauses = append(clauses, fmt.Sprintf("sni ILIKE $%d ESCAPE '\\'", len(args)))
	}
	if status != "" {
		args = append(args, status)
		clauses = append(clauses, fmt.Sprintf("status = $%d", len(args)))
	}
	if extra, ea, err := ipClause(ip, len(args)); err != nil {
		return nil, nil, err
	} else {
		clauses = append(clauses, extra...)
		args = append(args, ea...)
	}
	return clauses, args, nil
}

// ipClause builds the client_ip test with the next placeholder number base.
func ipClause(ip string, base int) ([]string, []any, error) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return nil, nil, nil
	}
	if _, n, err := net.ParseCIDR(ip); err == nil {
		return []string{fmt.Sprintf("client_ip <<= $%d::inet", base+1)}, []any{n.String()}, nil
	}
	if parsed := net.ParseIP(ip); parsed != nil {
		return []string{fmt.Sprintf("client_ip = $%d::inet", base+1)}, []any{parsed.String()}, nil
	}
	return nil, nil, fmt.Errorf("%w: IP", ErrInvalid)
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
