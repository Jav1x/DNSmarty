package proxy

import (
	"context"
	"net"
	"testing"
	"time"

	"dnsmarty/internal/snapshot"
)

func startQUIC(t *testing.T, snap snapshot.ProxySnap, origin net.Addr) (*Server, net.PacketConn) {
	t.Helper()
	s := New(nil, "", "")
	if err := s.SetSnapshot(&snap); err != nil {
		t.Fatal(err)
	}
	s.udpDial = func(_ context.Context, _ *snapshot.ProxySnap, _ string) (net.Conn, error) {
		return net.Dial("udp", origin.String())
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.serveQUIC(ctx, pc); close(done) }()
	t.Cleanup(func() {
		cancel()
		_ = pc.Close()
		<-done
	})
	return s, pc
}

func startUDPOrigin(t *testing.T) (net.PacketConn, chan []byte) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan []byte, 8)
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			p := append([]byte(nil), buf[:n]...)
			got <- p
			_, _ = pc.WriteTo([]byte("from-origin"), addr)
		}
	}()
	t.Cleanup(func() { _ = pc.Close() })
	return pc, got
}

func TestQUICRelayForwardsInitialAndReply(t *testing.T) {
	origin, got := startUDPOrigin(t)
	s, pc := startQUIC(t, snapshot.ProxySnap{
		SessionLimit:  10,
		IdleTimeoutMs: 200,
		Names:         []snapshot.NameRule{{Name: "example.com", Match: snapshot.MatchSuffix}},
	}, origin.LocalAddr())

	client, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	pkt := mustHex(rfc9001ClientInitial)
	if _, err := client.WriteTo(pkt, pc.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-got:
		if len(p) != len(pkt) {
			t.Fatalf("origin got %d, want %d", len(p), len(pkt))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("origin got nothing")
	}
	buf := make([]byte, 2048)
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := client.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "from-origin" {
		t.Fatalf("reply %q", buf[:n])
	}
	if _, err := client.WriteTo([]byte("more"), pc.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-got:
		if string(p) != "more" {
			t.Fatalf("follow-up %q", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("origin missed follow-up datagram")
	}
	select {
	case r := <-s.Reports():
		if r.Status != "ok" || r.SNI != "example.com" {
			t.Fatalf("report: %+v", r)
		}
		if r.BytesUp < int64(len(pkt)) || r.BytesDown < int64(len("from-origin")) {
			t.Fatalf("bytes: %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no report")
	}
}

func TestQUICRelayUnknownName(t *testing.T) {
	origin, got := startUDPOrigin(t)
	s, pc := startQUIC(t, snapshot.ProxySnap{
		SessionLimit: 10,
		Names:        []snapshot.NameRule{{Name: "other.test", Match: snapshot.MatchFQDN}},
	}, origin.LocalAddr())

	client, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.WriteTo(mustHex(rfc9001ClientInitial), pc.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-s.Reports():
		if r.Status != "refused" {
			t.Fatalf("report: %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no report")
	}
	select {
	case <-got:
		t.Fatal("origin must not see a refused name")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestQUICRelayACL(t *testing.T) {
	origin, got := startUDPOrigin(t)
	s, pc := startQUIC(t, snapshot.ProxySnap{
		SessionLimit: 10,
		Deny:         []string{"127.0.0.0/8"},
		Names:        []snapshot.NameRule{{Name: "example.com", Match: snapshot.MatchSuffix}},
	}, origin.LocalAddr())

	client, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.WriteTo(mustHex(rfc9001ClientInitial), pc.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-s.Reports():
		if r.Status != "acl" {
			t.Fatalf("report: %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no report")
	}
	select {
	case <-got:
		t.Fatal("origin must not see an ACL drop")
	case <-time.After(200 * time.Millisecond):
	}
}
