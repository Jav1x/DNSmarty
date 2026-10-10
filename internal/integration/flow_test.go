package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	mdns "github.com/miekg/dns"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"dnsmarty/internal/agent"
	"dnsmarty/internal/dns"
	"dnsmarty/internal/migrate"
	"dnsmarty/internal/panel"
	"dnsmarty/internal/proxy"
	"dnsmarty/internal/snapshot"
	"dnsmarty/internal/store"
)

func TestPostgresDecisions(t *testing.T) {
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("dnsmarty"),
		postgres.WithUsername("dnsmarty"),
		postgres.WithPassword("dnsmarty"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(dsn); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, dsn, "integration-session-secret", "integration-master-key")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)

	keyHex, node, err := st.CreateNode(ctx, "test", store.NodeInput{
		Name: "edge", Role: "proxy", IPv4: "203.0.113.10", Region: "lab",
		AgentHost: "127.0.0.1", AgentPort: 9444, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var ct []byte
	if err := pool.QueryRow(ctx, `SELECT key_ciphertext FROM node WHERE id = $1`, node.ID).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte(keyHex)) || len(ct) == 0 {
		t.Fatal("key must not be stored in plaintext")
	}
	opened, err := st.NodeKey(ctx, node.ID)
	if err != nil || len(opened) != 32 {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO service_proxy (service_id, proxy_node_id, weight)
		SELECT service_id, $1, 1 FROM domain WHERE name = 'example.com'
	`, node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE service s SET strategy = 'sticky24'
		FROM domain d WHERE d.service_id = s.id AND d.name = 'example.com'
	`); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateClient(ctx, "test", "127.0.0.1/32", "local", "allow", true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetReachable(ctx, node.ID, 0, "test"); err != nil {
		t.Fatal(err)
	}

	snap, err := st.DNSSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshotHasIP(snap, "203.0.113.10") {
		t.Fatalf("live proxy missing: %+v", snap)
	}

	eng := dns.NewEngine(nil)
	if err := eng.SetSnapshot(&snap); err != nil {
		t.Fatal(err)
	}

	q := new(mdns.Msg)
	q.SetQuestion("www.example.com.", mdns.TypeA)
	foreign := &fakeRW{addr: &net.UDPAddr{IP: net.ParseIP("192.0.2.9"), Port: 53}}
	eng.ServeDNS(foreign, q.Copy())
	if foreign.msg == nil || foreign.msg.Rcode != mdns.RcodeRefused {
		t.Fatalf("udp refused: %+v", foreign.msg)
	}

	raw, err := q.Pack()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/dns-query", bytes.NewReader(raw))
	req.RemoteAddr = "192.0.2.9:4444"
	req.Header.Set("Content-Type", "application/dns-message")
	rec := httptest.NewRecorder()
	eng.ServeDoH(rec, req)
	var dohRefused mdns.Msg
	if err := dohRefused.Unpack(rec.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	if dohRefused.Rcode != mdns.RcodeRefused {
		t.Fatalf("doh refused: %d", dohRefused.Rcode)
	}

	udpAns, err := queryUDP(eng, q.Copy())
	if err != nil {
		t.Fatal(err)
	}
	dohAns, err := queryDoH(eng, q.Copy())
	if err != nil {
		t.Fatal(err)
	}
	if udpAns != "203.0.113.10" || udpAns != dohAns {
		t.Fatalf("53=%s doh=%s", udpAns, dohAns)
	}

	if _, err := pool.Exec(ctx, `UPDATE node SET last_seen_at = now() - interval '2 minutes' WHERE id = $1`, node.ID); err != nil {
		t.Fatal(err)
	}
	dead, err := st.DNSSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rawSnap, _ := json.Marshal(dead)
	if bytes.Contains(rawSnap, []byte("203.0.113.10")) {
		t.Fatalf("dead proxy still in snapshot: %s", rawSnap)
	}
	found := false
	for _, d := range dead.Domains {
		if d.Name == "example.com" {
			found = true
			if len(d.Proxies) != 0 {
				t.Fatalf("proxies: %+v", d.Proxies)
			}
		}
	}
	if !found {
		t.Fatal("domain dropped")
	}

	// A longer push interval widens the live window: 3 × 15 s keeps a proxy seen 40 s ago.
	if _, err := pool.Exec(ctx, `UPDATE setting SET pull_interval_sec = 15`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE node SET last_seen_at = now() - interval '40 seconds' WHERE id = $1`, node.ID); err != nil {
		t.Fatal(err)
	}
	if wide, err := st.DNSSnapshot(ctx); err != nil || !snapshotHasIP(wide, "203.0.113.10") {
		t.Fatalf("liveness window did not widen: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE setting SET pull_interval_sec = 5`); err != nil {
		t.Fatal(err)
	}

	// A switched-off list disappears from the snapshot; bootstrap CIDRs survive either way.
	if _, err := pool.Exec(ctx, `UPDATE setting SET bootstrap_cidr = '203.0.113.5/32'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE setting SET allow_enabled = false, deny_enabled = false`); err != nil {
		t.Fatal(err)
	}
	if off, err := st.DNSSnapshot(ctx); err != nil {
		t.Fatal(err)
	} else if len(off.Allow) != 0 || len(off.Deny) != 0 || len(off.Bootstrap) != 1 {
		t.Fatalf("disabled lists still in snapshot: allow=%v deny=%v boot=%v", off.Allow, off.Deny, off.Bootstrap)
	}
	if _, err := pool.Exec(ctx, `UPDATE setting SET allow_enabled = true, deny_enabled = true`); err != nil {
		t.Fatal(err)
	}

	// A full agent buffer goes in whole: one bad address is skipped, a skewed clock is clamped.
	hits := make([]store.DNSHit, 0, 2002)
	for i := 0; i < 2000; i++ {
		hits = append(hits, store.DNSHit{At: time.Now(), ClientIP: "198.51.100.1", QName: "bulk.test.", QType: "A", Rcode: "NOERROR", Decision: "forward"})
	}
	hits = append(hits,
		store.DNSHit{At: time.Now(), ClientIP: "not-an-ip", QName: "bulk.test."},
		store.DNSHit{At: time.Now().AddDate(0, 0, -30), ClientIP: "198.51.100.2", QName: "skew.test."},
	)
	skipped, err := st.InsertHits(ctx, node.ID, hits)
	if err != nil || skipped != 1 {
		t.Fatalf("insert: skipped=%d err=%v", skipped, err)
	}
	var bulk, skew int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE qname = 'bulk.test.'), count(*) FILTER (WHERE qname = 'skew.test.' AND at > now() - interval '1 hour') FROM dns_hit`).Scan(&bulk, &skew); err != nil {
		t.Fatal(err)
	}
	if bulk != 2000 || skew != 1 {
		t.Fatalf("bulk=%d skew=%d", bulk, skew)
	}

	// Series: the bulk insert lands in the last minute bucket.
	points, err := st.Series(ctx, time.Hour, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) < 2 {
		t.Fatalf("series points: %d", len(points))
	}
	last := points[len(points)-1]
	if last.DNS < 2001 {
		t.Fatalf("last bucket dns=%d", last.DNS)
	}

	// Stats: the bulk insert (one client, decision forward) dominates the aggregates.
	// A second client with an ACL refusal checks the ACL filter and the distinct-client count.
	if _, err := pool.Exec(ctx, `INSERT INTO dns_hit (day, at, node_id, client_ip, qname, qtype, rcode, decision)
		VALUES (current_date, now(), $1, '192.0.2.7', 'bulk.test.', 'A', 'NOERROR', 'acl')`, node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO proxy_session (day, at, node_id, client_ip, sni, bytes_up, bytes_down, status)
		VALUES (current_date, now(), $1, '192.0.2.7', 'site.example', 100, 200, 'ok'),
		       (current_date, now(), $1, '192.0.2.7', 'site.example', 10, 20, 'acl')`, node.ID); err != nil {
		t.Fatal(err)
	}
	topDomains, err := st.TopDomains(ctx, int64(time.Hour.Seconds()), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(topDomains) == 0 || topDomains[0].Name != "bulk.test." || topDomains[0].Queries < 2001 {
		t.Fatalf("top domains: %+v", topDomains[:1])
	}
	if topDomains[0].ACL != 1 || topDomains[0].Clients != 2 {
		t.Fatalf("bulk.test. stat: %+v", topDomains[0])
	}
	topClients, err := st.TopClients(ctx, int64(time.Hour.Seconds()), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(topClients) < 2 || topClients[0].IP != "198.51.100.1" || topClients[0].ACL != 0 {
		t.Fatalf("top clients: %+v", topClients)
	}
	cd, err := st.ClientDomains(ctx, "192.0.2.7", int64(time.Hour.Seconds()))
	if err != nil || len(cd) != 1 || cd[0].Name != "bulk.test." || cd[0].ACL != 1 {
		t.Fatalf("client domains: %v %+v", err, cd)
	}
	topProxy, err := st.TopProxyDomains(ctx, int64(time.Hour.Seconds()), 10)
	if err != nil || len(topProxy) != 1 || topProxy[0].Name != "site.example" || topProxy[0].Sessions != 2 || topProxy[0].Bytes != 330 {
		t.Fatalf("top proxy: %v %+v", err, topProxy)
	}
	// A window of 0 means everything; with 1s nothing is older, so the counts match.
	all, err := st.TopDomains(ctx, 0, 10)
	if err != nil || len(all) != len(topDomains) {
		t.Fatalf("all window: %v %d vs %d", err, len(all), len(topDomains))
	}

	// Keyset pagination: the first page ends with a cursor, the second continues past it.
	page := store.Page{Limit: 5}
	rows, cur, err := st.DNSLogs(ctx, "bulk", "", "", page)
	if err != nil || len(rows) != 5 || cur == nil {
		t.Fatalf("dns page1: rows=%d cur=%v err=%v", len(rows), cur, err)
	}
	page2 := store.Page{Limit: 5, Before: cur.At, BeforeID: cur.ID}
	rows2, cur2, err := st.DNSLogs(ctx, "bulk", "", "", page2)
	if err != nil || len(rows2) == 0 || rows2[0].ID == rows[0].ID {
		t.Fatalf("dns page2: rows=%d err=%v", len(rows2), err)
	}
	if cur2 == nil {
		t.Fatal("page2 has no cursor")
	}

	if err := st.EnsureAdmin(ctx, "admin", "panel-pass-long"); err != nil {
		t.Fatal(err)
	}
	ui := panel.New(st, nil, panel.Options{Version: "test"})
	ts := httptest.NewServer(ui.Handler())
	t.Cleanup(ts.Close)
	sess := login(t, ts.URL, "admin", "panel-pass-long")

	// CSP header on the SPA.
	rootResp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	rootResp.Body.Close()
	if !strings.Contains(rootResp.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatalf("missing CSP: %q", rootResp.Header.Get("Content-Security-Policy"))
	}
	// State change without the CSRF header is refused.
	if code := apiCall(t, ts.URL, http.MethodPost, "/api/services", `{"name":"x"}`, "application/json", sess.cookie, ""); code != http.StatusForbidden {
		t.Fatalf("without CSRF: %d", code)
	}
	// The stats endpoint returns all three aggregates; a bad ip is rejected.
	req2, err := http.NewRequest(http.MethodGet, ts.URL+"/api/stats?window=1h", nil)
	if err != nil {
		t.Fatal(err)
	}
	req2.AddCookie(sess.cookie)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	var stats struct {
		Domains []store.DomainStat  `json:"domains"`
		Clients []store.ClientStat  `json:"clients"`
		Proxy   []store.ProxyDomain `json:"proxy"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK || len(stats.Domains) == 0 || len(stats.Clients) == 0 || len(stats.Proxy) == 0 {
		t.Fatalf("stats: %d %+v", resp2.StatusCode, stats)
	}
	badIP, err := http.Get(ts.URL + "/api/stats/client?ip=not-an-ip")
	if err != nil {
		t.Fatal(err)
	}
	badIP.Body.Close()
	if badIP.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad ip without session: %d", badIP.StatusCode)
	}
	if code := apiCall(t, ts.URL, http.MethodGet, "/api/stats/client?ip=not-an-ip", "", "", sess.cookie, ""); code != http.StatusBadRequest {
		t.Fatalf("bad ip with session: %d", code)
	}
	// Form-encoded body is refused.
	if code := apiCall(t, ts.URL, http.MethodPost, "/api/services", `name=x`, "application/x-www-form-urlencoded", sess.cookie, sess.csrf); code != http.StatusUnsupportedMediaType {
		t.Fatalf("form body: %d", code)
	}

	// Password change ends other sessions but keeps this one.
	other := login(t, ts.URL, "admin", "panel-pass-long")
	if code := apiCall(t, ts.URL, http.MethodPost, "/api/password", `{"current":"wrong","next":"another-pass-long"}`, "application/json", sess.cookie, sess.csrf); code != http.StatusBadRequest {
		t.Fatalf("wrong current password: %d", code)
	}
	if code := apiCall(t, ts.URL, http.MethodPost, "/api/password", `{"current":"panel-pass-long","next":"short"}`, "application/json", sess.cookie, sess.csrf); code != http.StatusBadRequest {
		t.Fatalf("short password: %d", code)
	}
	if code := apiCall(t, ts.URL, http.MethodPost, "/api/password", `{"current":"panel-pass-long","next":"another-pass-long"}`, "application/json", sess.cookie, sess.csrf); code != http.StatusOK {
		t.Fatalf("password change: %d", code)
	}
	if code := apiCall(t, ts.URL, http.MethodGet, "/api/me", "", "", other.cookie, ""); code != http.StatusUnauthorized {
		t.Fatalf("second session still alive: %d", code)
	}
	if code := apiCall(t, ts.URL, http.MethodGet, "/api/me", "", "", sess.cookie, ""); code != http.StatusOK {
		t.Fatalf("current session died: %d", code)
	}

	// Rate limit: sixth wrong password is blocked.
	for i := 0; i < 5; i++ {
		apiCall(t, ts.URL, http.MethodPost, "/api/login", `{"username":"admin","password":"nope"}`, "application/json", nil, "")
	}
	if code := apiCall(t, ts.URL, http.MethodPost, "/api/login", `{"username":"admin","password":"another-pass-long"}`, "application/json", nil, ""); code != http.StatusTooManyRequests {
		t.Fatalf("sixth attempt: %d", code)
	}

	closed := postConnect(t, ts.URL, sess, node.ID)
	if closed.OK {
		t.Fatal("closed port must not connect")
	}
	if closed.Error == "" {
		t.Fatal("missing error text")
	}
	tlsCfg, err := agent.ServerTLS(opened)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	agentCtx, cancelAgent := context.WithCancel(ctx)
	t.Cleanup(cancelAgent)
	proxySrv := proxy.New(nil, "127.0.0.1:0", "127.0.0.1:0")
	go func() {
		_ = agent.Serve(agentCtx, ln, agent.Config{Role: snapshot.RoleProxy, Apply: func(body []byte) error { return agent.ApplyProxy(body, proxySrv) }})
	}()
	livePort := ln.Addr().(*net.TCPAddr).Port
	if _, err := pool.Exec(ctx, `UPDATE node SET agent_port = $2 WHERE id = $1`, node.ID, livePort); err != nil {
		t.Fatal(err)
	}
	ok := postConnect(t, ts.URL, sess, node.ID)
	if !ok.OK {
		t.Fatalf("wanted a link: %s", ok.Error)
	}

	// The agent got v1. A settings change makes v2.
	if _, err := pool.Exec(ctx, `UPDATE setting SET session_limit = session_limit + 1`); err != nil {
		t.Fatal(err)
	}
	if ok := postConnect(t, ts.URL, sess, node.ID); !ok.OK {
		t.Fatalf("v2: %s", ok.Error)
	}
	before := proxySrv.Snapshot()
	if before == nil || before.Version != 2 {
		t.Fatalf("wanted v2 on the agent: %+v", before)
	}
	// The proxy gets the same client lists as DNS.
	if len(before.Allow) != 1 || before.Allow[0] != "127.0.0.1/32" {
		t.Fatalf("client lists did not reach the proxy: %+v", before.Allow)
	}
	// A restored database forgets v2 and would publish v1 again. The agent rejects it as old,
	// the panel notices it never published the agent's version and starts a new epoch.
	if _, err := pool.Exec(ctx, `DELETE FROM proxy_snapshot WHERE node_id = $1`, node.ID); err != nil {
		t.Fatal(err)
	}
	if ok := postConnect(t, ts.URL, sess, node.ID); !ok.OK {
		t.Fatalf("after rolling the DB back: %s", ok.Error)
	}
	after := proxySrv.Snapshot()
	if after.Epoch == before.Epoch {
		t.Fatalf("epoch did not change: %s", after.Epoch)
	}
}

type connectResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func postConnect(t *testing.T, base string, sess session, id string) connectResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/api/nodes/"+id+"/connect", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(sess.cookie)
	req.Header.Set("X-CSRF-Token", sess.csrf)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out connectResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

type session struct {
	cookie *http.Cookie
	csrf   string
}

func login(t *testing.T, base, user, pass string) session {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, user, pass)
	resp, err := http.Post(base+"/api/login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in: %d %v", resp.StatusCode, err)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "dnsmarty" {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Fatalf("cookie flags: %+v", c)
			}
			return session{cookie: c, csrf: out.CSRF}
		}
	}
	t.Fatal("no session")
	return session{}
}

func apiCall(t *testing.T, base, method, path, body, contentType string, cookie *http.Cookie, csrf string) int {
	t.Helper()
	req, err := http.NewRequest(method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

func snapshotHasIP(s snapshot.DNS, ip string) bool {
	for _, d := range s.Domains {
		for _, p := range d.Proxies {
			if p.IPv4 == ip {
				return true
			}
		}
	}
	return false
}

func queryUDP(eng *dns.Engine, q *mdns.Msg) (string, error) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	started := make(chan struct{})
	srv := &mdns.Server{
		PacketConn:        pc,
		Handler:           eng,
		NotifyStartedFunc: func() { close(started) },
	}
	go func() { _ = srv.ActivateAndServe() }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		_ = srv.Shutdown()
		return "", errors.New("dns server did not start")
	}
	defer func() { _ = srv.Shutdown() }()
	c := &mdns.Client{Net: "udp", Timeout: 3 * time.Second}
	r, _, err := c.Exchange(q, pc.LocalAddr().String())
	if err != nil {
		return "", err
	}
	return firstA(r), nil
}

func queryDoH(eng *dns.Engine, q *mdns.Msg) (string, error) {
	cert, err := testCert()
	if err != nil {
		return "", err
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		return "", err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/dns-query", eng.ServeDoH)
	go func() { _ = http.Serve(ln, mux) }()
	defer ln.Close()

	raw, err := q.Pack()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, "https://"+ln.Addr().String()+"/dns-query", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	cli := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var msg mdns.Msg
	if err := msg.Unpack(body); err != nil {
		return "", err
	}
	return firstA(&msg), nil
}

func firstA(msg *mdns.Msg) string {
	if msg == nil {
		return ""
	}
	for _, rr := range msg.Answer {
		if a, ok := rr.(*mdns.A); ok {
			return a.A.String()
		}
	}
	return ""
}

type fakeRW struct {
	addr net.Addr
	msg  *mdns.Msg
}

func (f *fakeRW) LocalAddr() net.Addr        { return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53} }
func (f *fakeRW) RemoteAddr() net.Addr       { return f.addr }
func (f *fakeRW) WriteMsg(m *mdns.Msg) error { f.msg = m; return nil }
func (f *fakeRW) Write([]byte) (int, error)  { return 0, nil }
func (f *fakeRW) Close() error               { return nil }
func (f *fakeRW) TsigStatus() error          { return nil }
func (f *fakeRW) TsigTimersOnly(bool)        {}
func (f *fakeRW) Hijack()                    {}

func testCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "dns"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
