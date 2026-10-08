package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"testing"
	"time"

	mdns "github.com/miekg/dns"

	"dnsmarty/internal/dns"
	"dnsmarty/internal/snapshot"
)

func TestWrongKeyRejected(t *testing.T) {
	key := mustKey(t)
	other := mustKey(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	port := startAgent(t, ctx, key, func(body []byte) error { return nil })
	pool := NewPool()
	if _, err := pool.Check(context.Background(), target("a", port, other)); err == nil {
		t.Fatal("чужой ключ прошёл")
	}
	h, err := pool.Check(context.Background(), target("b", port, key))
	if err != nil {
		t.Fatal(err)
	}
	if !h.OK || h.Role != snapshot.RoleDNS || h.Version == "" {
		t.Fatalf("health: %+v", h)
	}
}

// Without a panel certificate the agent must not answer at all: before this check anyone
// reaching the management port could push a snapshot or drain client logs.
func TestNoClientCertRejected(t *testing.T) {
	key := mustKey(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	applied := false
	port := startAgent(t, ctx, key, func(body []byte) error { applied = true; return nil })
	cli := &http.Client{
		Timeout:   3 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	for _, path := range []string{"/health", "/stats"} {
		resp, err := cli.Get(target("x", port, key).url(path))
		if err == nil {
			resp.Body.Close()
			t.Fatalf("%s без клиентского сертификата: %s", path, resp.Status)
		}
	}
	resp, err := cli.Post(target("x", port, key).url("/config"), "application/json", nil)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("/config без клиентского сертификата: %s", resp.Status)
	}
	if applied {
		t.Fatal("снимок применился без авторизации")
	}
}

// A client certificate derived from another node key is refused too.
func TestForeignPanelCertRejected(t *testing.T) {
	key := mustKey(t)
	other := mustKey(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	port := startAgent(t, ctx, key, func(body []byte) error { return nil })
	good, err := ClientTLS(key)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := ClientTLS(other)
	if err != nil {
		t.Fatal(err)
	}
	// Right agent pin, wrong panel certificate.
	good.Certificates = bad.Certificates
	cli := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: good}}
	resp, err := cli.Get(target("x", port, key).url("/health"))
	if err == nil {
		resp.Body.Close()
		t.Fatalf("чужой сертификат панели прошёл: %s", resp.Status)
	}
}

func TestTLS12Rejected(t *testing.T) {
	key := mustKey(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	port := startAgent(t, ctx, key, func(body []byte) error { return nil })
	cfg, err := ClientTLS(key)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MinVersion = tls.VersionTLS12
	cfg.MaxVersion = tls.VersionTLS12
	conn, err := tls.Dial("tcp", target("x", port, key).addr(), cfg)
	if err == nil {
		conn.Close()
		t.Fatal("TLS 1.2 принят")
	}
}

func TestPushAppliesSnapshot(t *testing.T) {
	key := mustKey(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng := dns.NewEngine(nil)
	port := startAgent(t, ctx, key, func(body []byte) error {
		return ApplyDNS(body, eng)
	})
	body := dnsConfig{
		DNS: snapshot.DNS{
			Version: 3,
			TTL:     30,
			Allow:   []string{"127.0.0.1/32"},
			Domains: []snapshot.Domain{{
				Name: "example.com", Match: snapshot.MatchSuffix, Balance: snapshot.BalanceSticky,
				Proxies: []snapshot.Proxy{{ID: "p", IPv4: "203.0.113.10", Weight: 1}},
			}},
		},
		NodeEnabled: true,
	}
	if err := NewPool().Push(context.Background(), target("n", port, key), body); err != nil {
		t.Fatal(err)
	}
	snap := eng.Snapshot()
	if snap == nil || len(snap.Domains) != 1 || snap.Domains[0].Proxies[0].IPv4 != "203.0.113.10" {
		t.Fatalf("снимок не применился: %+v", snap)
	}
}

func TestStaleVersionRejected(t *testing.T) {
	key := mustKey(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng := dns.NewEngine(nil)
	port := startAgent(t, ctx, key, func(body []byte) error { return ApplyDNS(body, eng) })
	pool := NewPool()
	tg := target("n", port, key)
	push := func(epoch string, version int64, enabled bool) error {
		return pool.Push(context.Background(), tg, dnsConfig{DNS: snapshot.DNS{Epoch: epoch, Version: version, TTL: 30}, NodeEnabled: enabled})
	}
	if err := push("e1", 5, true); err != nil {
		t.Fatal(err)
	}
	err := push("e1", 4, true)
	if have, ok := IsStale(err); !ok || have != 5 {
		t.Fatalf("старая версия принята или ошибка не та: %v", err)
	}
	if eng.Snapshot().Version != 5 {
		t.Fatalf("версия откатилась: %d", eng.Snapshot().Version)
	}
	// The same version is applied again, because node_enabled lives outside the version.
	if err := push("e1", 5, false); err != nil {
		t.Fatal(err)
	}
	q := testQuery()
	if resp := eng.Resolve(netip.MustParseAddr("127.0.0.1"), q); resp.Rcode == 0 {
		t.Fatal("выключенный узел ответил")
	}
	// A new epoch restarts numbering.
	if err := push("e2", 1, true); err != nil {
		t.Fatalf("новая эпоха отвергнута: %v", err)
	}
	if eng.Snapshot().Epoch != "e2" || eng.Snapshot().Version != 1 {
		t.Fatalf("снимок: %+v", eng.Snapshot())
	}
}

func TestPoolReusesConn(t *testing.T) {
	key := mustKey(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	port := startAgent(t, ctx, key, func(body []byte) error { return nil })
	pool := NewPool()
	tg := target("n", port, key)
	if _, err := pool.Check(context.Background(), tg); err != nil {
		t.Fatal(err)
	}
	reused := false
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}
	tctx := httptrace.WithClientTrace(context.Background(), trace)
	if _, err := pool.Check(tctx, tg); err != nil {
		t.Fatal(err)
	}
	if !reused {
		t.Fatal("соединение не переиспользовано")
	}
}

func TestStatsDrainedOnce(t *testing.T) {
	key := mustKey(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stats := &Stats{}
	stats.AddHit(dns.Hit{QName: "a.example."})
	port := startAgentStats(t, ctx, key, func(body []byte) error { return nil }, stats)
	pool := NewPool()
	tg := target("n", port, key)
	first, err := pool.FetchStats(context.Background(), tg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.FetchStats(context.Background(), tg)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Hits) != 1 || len(second.Hits) != 0 {
		t.Fatalf("first=%d second=%d", len(first.Hits), len(second.Hits))
	}
}

func TestClosedPort(t *testing.T) {
	key := mustKey(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if _, err := NewPool().Check(context.Background(), target("n", port, key)); err == nil {
		t.Fatal("закрытый порт не дал ошибку")
	}
}

func TestHealthBody(t *testing.T) {
	var h Health
	if err := json.Unmarshal([]byte(`{"ok":true,"role":"dns"}`), &h); err != nil {
		t.Fatal(err)
	}
	// An agent from before versioned health still decodes; the empty version marks it outdated.
	if h.Version != "" {
		t.Fatal(h.Version)
	}
}

func testQuery() *mdns.Msg {
	q := new(mdns.Msg)
	q.SetQuestion("www.example.com.", mdns.TypeA)
	return q
}

func target(id string, port int, key []byte) Target {
	return Target{ID: id, Host: "127.0.0.1", Port: port, Key: key}
}

func mustKey(t *testing.T) []byte {
	t.Helper()
	k, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func startAgent(t *testing.T, ctx context.Context, key []byte, apply ApplyFunc) int {
	return startAgentStats(t, ctx, key, apply, &Stats{})
}

func startAgentStats(t *testing.T, ctx context.Context, key []byte, apply ApplyFunc, stats *Stats) int {
	t.Helper()
	cfg, err := ServerTLS(key)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = Serve(ctx, ln, Config{Role: snapshot.RoleDNS, Apply: apply, Stats: stats}) }()
	return ln.Addr().(*net.TCPAddr).Port
}
