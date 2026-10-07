package agent

import (
	"context"
	"crypto/tls"
	"net"
	"testing"

	"dnsmarty/internal/dns"
	"dnsmarty/internal/snapshot"
)

func TestWrongKeyRejected(t *testing.T) {
	key := mustKey(t)
	other := mustKey(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	port := startAgent(t, ctx, key, func(body []byte) error { return nil })
	if err := Check(context.Background(), "127.0.0.1", port, other); err == nil {
		t.Fatal("чужой ключ прошёл")
	}
	if err := Check(context.Background(), "127.0.0.1", port, key); err != nil {
		t.Fatal(err)
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
	if err := Push(context.Background(), "127.0.0.1", port, key, body); err != nil {
		t.Fatal(err)
	}
	snap := eng.Snapshot()
	if snap == nil || len(snap.Domains) != 1 || snap.Domains[0].Proxies[0].IPv4 != "203.0.113.10" {
		t.Fatalf("снимок не применился: %+v", snap)
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
	err = Check(context.Background(), "127.0.0.1", port, key)
	if err == nil {
		t.Fatal("закрытый порт не дал ошибку")
	}
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
	t.Helper()
	cfg, err := ServerTLS(key)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = Serve(ctx, ln, snapshot.RoleDNS, apply, &Stats{}, nil) }()
	return ln.Addr().(*net.TCPAddr).Port
}
