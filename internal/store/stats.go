package store

import (
	"context"
)

// DomainStat is one row of the top-domains table: queries in the window,
// how many of them ACL refused, and how many distinct clients asked.
type DomainStat struct {
	Name    string `json:"name"`
	Queries int64  `json:"queries"`
	ACL     int64  `json:"acl"`
	Clients int64  `json:"clients"`
}

// ClientStat is one client: its share of queries and ACL refusals.
type ClientStat struct {
	IP      string `json:"ip"`
	Queries int64  `json:"queries"`
	ACL     int64  `json:"acl"`
}

// ClientDomain is one domain a single client asked for.
type ClientDomain struct {
	Name    string `json:"name"`
	Queries int64  `json:"queries"`
	ACL     int64  `json:"acl"`
}

// ProxyDomain is one SNI domain: sessions and bytes through the proxy.
type ProxyDomain struct {
	Name     string `json:"name"`
	Sessions int64  `json:"sessions"`
	Bytes    int64  `json:"bytes"`
}

// The ranking queries take the window in seconds and the limit. With a window the
// time clause is $1 and the limit $2; without one ($secs = 0, everything within
// retention) the time clause is absent and the limit is $1: pgx rejects an unused
// parameter (42P18), so each case needs its own SQL text.
const (
	withWindow         = "at > now() - make_interval(secs => $1)"
	limitWithWindow    = "LIMIT $2"
	limitWithoutWindow = "LIMIT $1"
)

func topDomainsSQL(withTime bool) string {
	cond, lim := "TRUE", limitWithoutWindow
	if withTime {
		cond, lim = withWindow, limitWithWindow
	}
	return `
		SELECT qname, count(*), count(*) FILTER (WHERE decision = 'acl'), count(DISTINCT client_ip)
		FROM dns_hit
		WHERE ` + cond + `
		GROUP BY qname
		ORDER BY 2 DESC, 1
		` + lim
}

func topClientsSQL(withTime bool) string {
	cond, lim := "TRUE", limitWithoutWindow
	if withTime {
		cond, lim = withWindow, limitWithWindow
	}
	return `
		SELECT coalesce(host(client_ip), ''), count(*), count(*) FILTER (WHERE decision = 'acl')
		FROM dns_hit
		WHERE client_ip IS NOT NULL AND ` + cond + `
		GROUP BY client_ip
		ORDER BY 2 DESC, 1
		` + lim
}

func clientDomainsSQL(withTime bool) string {
	// Without a window the ip is $1; with one the time clause is $1 and the ip $2.
	ip, lim, cond := "$1", "LIMIT 500", "TRUE"
	if withTime {
		ip, cond = "$2", withWindow
	}
	return `
		SELECT qname, count(*), count(*) FILTER (WHERE decision = 'acl')
		FROM dns_hit
		WHERE client_ip = ` + ip + ` AND ` + cond + `
		GROUP BY qname
		ORDER BY 2 DESC, 1
		` + lim
}

func topProxySQL(withTime bool) string {
	cond, lim := "TRUE", limitWithoutWindow
	if withTime {
		cond, lim = withWindow, limitWithWindow
	}
	return `
		SELECT sni, count(*), coalesce(sum(bytes_up + bytes_down), 0)
		FROM proxy_session
		WHERE ` + cond + `
		GROUP BY sni
		ORDER BY 2 DESC, 1
		` + lim
}

// statsArgs places secs and limit per the SQL variant: with a window the arguments
// are (secs, limit), without one just (limit).
func statsArgs(withTime bool, secs, limit int64) []any {
	if withTime {
		return []any{secs, limit}
	}
	return []any{limit}
}

// TopDomains ranks DNS hits by query count over the window. window <= 0 means everything.
func (s *Store) TopDomains(ctx context.Context, window int64, limit int) ([]DomainStat, error) {
	withTime := window > 0
	rows, err := s.pool.Query(ctx, topDomainsSQL(withTime), statsArgs(withTime, window, int64(limit))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DomainStat{}
	for rows.Next() {
		var d DomainStat
		if err := rows.Scan(&d.Name, &d.Queries, &d.ACL, &d.Clients); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// TopClients ranks client IPs by query count over the window. window <= 0 means everything.
func (s *Store) TopClients(ctx context.Context, window int64, limit int) ([]ClientStat, error) {
	withTime := window > 0
	rows, err := s.pool.Query(ctx, topClientsSQL(withTime), statsArgs(withTime, window, int64(limit))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientStat{}
	for rows.Next() {
		var c ClientStat
		if err := rows.Scan(&c.IP, &c.Queries, &c.ACL); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ClientDomains lists the domains one client asked for over the window, most asked first.
func (s *Store) ClientDomains(ctx context.Context, ip string, window int64) ([]ClientDomain, error) {
	withTime := window > 0
	// The SQL takes (secs, ip) when a window is set, just (ip) otherwise.
	args := []any{ip}
	if withTime {
		args = []any{window, ip}
	}
	rows, err := s.pool.Query(ctx, clientDomainsSQL(withTime), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientDomain{}
	for rows.Next() {
		var d ClientDomain
		if err := rows.Scan(&d.Name, &d.Queries, &d.ACL); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// TopProxyDomains ranks proxy SNIs by session count and total bytes over the window.
// window <= 0 means everything.
func (s *Store) TopProxyDomains(ctx context.Context, window int64, limit int) ([]ProxyDomain, error) {
	withTime := window > 0
	rows, err := s.pool.Query(ctx, topProxySQL(withTime), statsArgs(withTime, window, int64(limit))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProxyDomain{}
	for rows.Next() {
		var p ProxyDomain
		if err := rows.Scan(&p.Name, &p.Sessions, &p.Bytes); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
