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
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
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
		t.Fatal("ключ не должен лежать в базе открытым текстом")
	}
	opened, err := st.NodeKey(ctx, node.ID)
	if err != nil || len(opened) != 32 {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO domain_proxy (domain_id, proxy_node_id, weight)
		SELECT id, $1, 1 FROM domain WHERE name = 'example.com'
	`, node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE domain SET balance = 'sticky24' WHERE name = 'example.com'`); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateClient(ctx, "test", "127.0.0.1/32", "local", "allow", true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetReachable(ctx, node.ID, 0); err != nil {
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
	eng.SetSnapshot(&snap)

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

	if err := st.EnsureAdmin(ctx, "admin", "panel-pass-long"); err != nil {
		t.Fatal(err)
	}
	ui := panel.New(st, nil)
	ts := httptest.NewServer(ui.Handler())
	t.Cleanup(ts.Close)
	loginBody := bytes.NewBufferString(`{"username":"admin","password":"panel-pass-long"}`)
	loginResp, err := http.Post(ts.URL+"/api/login", "application/json", loginBody)
	if err != nil {
		t.Fatal(err)
	}
	loginResp.Body.Close()
	var cookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == "dnsmarty" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("нет сессии")
	}
	closed := postConnect(t, ts.URL, cookie, node.ID)
	if closed.OK {
		t.Fatal("закрытый порт не должен подключаться")
	}
	if closed.Error == "" {
		t.Fatal("нет текста ошибки")
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
	go func() {
		_ = agent.Serve(agentCtx, ln, snapshot.RoleProxy, func([]byte) error { return nil }, &agent.Stats{}, nil)
	}()
	livePort := ln.Addr().(*net.TCPAddr).Port
	if _, err := pool.Exec(ctx, `UPDATE node SET agent_port = $2 WHERE id = $1`, node.ID, livePort); err != nil {
		t.Fatal(err)
	}
	ok := postConnect(t, ts.URL, cookie, node.ID)
	if !ok.OK {
		t.Fatalf("ожидали связь: %s", ok.Error)
	}
}

type connectResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func postConnect(t *testing.T, base string, cookie *http.Cookie, id string) connectResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/api/nodes/"+id+"/connect", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
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
