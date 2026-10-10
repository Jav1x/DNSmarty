package dns

import (
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	mdns "github.com/miekg/dns"

	"dnsmarty/internal/snapshot"
)

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

func udpFrom(ip string) *fakeRW {
	return &fakeRW{addr: &net.UDPAddr{IP: net.ParseIP(ip), Port: 5353}}
}

// bigUpstream answers every A query with 60 records on both UDP and TCP, like a large CDN name.
func bigUpstream(t *testing.T) string {
	t.Helper()
	handler := mdns.HandlerFunc(func(w mdns.ResponseWriter, r *mdns.Msg) {
		m := new(mdns.Msg)
		m.SetReply(r)
		for i := 0; i < 60; i++ {
			m.Answer = append(m.Answer, &mdns.A{
				Hdr: mdns.RR_Header{Name: r.Question[0].Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 60},
				A:   net.IPv4(198, 51, 100, byte(i)),
			})
		}
		if _, ok := w.RemoteAddr().(*net.UDPAddr); ok {
			m.Truncate(udpSize(r))
		}
		_ = w.WriteMsg(m)
	})
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	udp := &mdns.Server{PacketConn: pc, Handler: handler}
	tcp := &mdns.Server{Listener: ln, Handler: handler}
	go func() { _ = udp.ActivateAndServe() }()
	go func() { _ = tcp.ActivateAndServe() }()
	t.Cleanup(func() { _ = udp.Shutdown(); _ = tcp.Shutdown() })
	return pc.LocalAddr().String()
}

func engineWith(t *testing.T, snap snapshot.DNS) *Engine {
	t.Helper()
	e := NewEngine(nil)
	if err := e.SetSnapshot(&snap); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestUDPTruncation(t *testing.T) {
	up := bigUpstream(t)
	e := engineWith(t, snapshot.DNS{Upstreams: []string{up}})
	q := new(mdns.Msg)
	q.SetQuestion("cdn.test.", mdns.TypeA)

	plain := udpFrom("192.0.2.1")
	e.ServeDNS(plain, q.Copy())
	packed, err := plain.msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	if !plain.msg.Truncated || len(packed) > mdns.MinMsgSize {
		t.Fatalf("без EDNS: tc=%v size=%d", plain.msg.Truncated, len(packed))
	}

	edns := q.Copy()
	edns.SetEdns0(4096, false)
	big := udpFrom("192.0.2.2")
	e.ServeDNS(big, edns)
	packed, err = big.msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	if len(packed) > maxUDPSize {
		t.Fatalf("EDNS 4096 не урезан до %d: %d", maxUDPSize, len(packed))
	}

	// Over TCP the client gets the full answer.
	tcp := &fakeRW{addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.3"), Port: 5353}}
	e.ServeDNS(tcp, q.Copy())
	if tcp.msg.Truncated || len(tcp.msg.Answer) != 60 {
		t.Fatalf("TCP: tc=%v answers=%d", tcp.msg.Truncated, len(tcp.msg.Answer))
	}
}

func TestANYGetsHINFO(t *testing.T) {
	e := engineWith(t, snapshot.DNS{Upstreams: []string{bigUpstream(t)}})
	q := new(mdns.Msg)
	q.SetQuestion("cdn.test.", mdns.TypeANY)
	w := udpFrom("192.0.2.1")
	e.ServeDNS(w, q)
	if len(w.msg.Answer) != 1 {
		t.Fatalf("answers: %d", len(w.msg.Answer))
	}
	if h, ok := w.msg.Answer[0].(*mdns.HINFO); !ok || h.Cpu != "RFC8482" {
		t.Fatalf("ждали HINFO: %v", w.msg.Answer[0])
	}
}

func TestLatencyRecorded(t *testing.T) {
	e := engineWith(t, snapshot.DNS{Upstreams: []string{bigUpstream(t)}})
	q := new(mdns.Msg)
	q.SetQuestion("cdn.test.", mdns.TypeA)
	e.Resolve(netip.MustParseAddr("192.0.2.1"), q)
	select {
	case h := <-e.Hits():
		if h.LatencyMS == nil || *h.LatencyMS < 0 {
			t.Fatalf("latency = %v, want measured value >= 0", h.LatencyMS)
		}
	case <-time.After(time.Second):
		t.Fatal("no hit recorded")
	}
}

func TestANYRefusedOutsideACL(t *testing.T) {
	e := engineWith(t, snapshot.DNS{Deny: []string{"192.0.2.0/24"}})
	q := new(mdns.Msg)
	q.SetQuestion("cdn.test.", mdns.TypeANY)
	w := udpFrom("192.0.2.1")
	e.ServeDNS(w, q)
	if w.msg.Rcode != mdns.RcodeRefused || len(w.msg.Answer) != 0 {
		t.Fatalf("ANY из blacklist: %+v", w.msg)
	}
}

func TestOpcodeAndClass(t *testing.T) {
	e := engineWith(t, snapshot.DNS{})
	q := new(mdns.Msg)
	q.SetQuestion("a.test.", mdns.TypeA)
	q.Opcode = mdns.OpcodeUpdate
	if r := e.Resolve(netip.MustParseAddr("192.0.2.1"), q); r.Rcode != mdns.RcodeNotImplemented {
		t.Fatalf("UPDATE: %d", r.Rcode)
	}
	q = new(mdns.Msg)
	q.SetQuestion("version.bind.", mdns.TypeTXT)
	q.Question[0].Qclass = mdns.ClassCHAOS
	if r := e.Resolve(netip.MustParseAddr("192.0.2.1"), q); r.Rcode != mdns.RcodeRefused {
		t.Fatalf("CHAOS: %d", r.Rcode)
	}
}

func TestForwardBusy(t *testing.T) {
	e := engineWith(t, snapshot.DNS{Upstreams: []string{bigUpstream(t)}})
	e.SetForwardLimit(1)
	e.forwards <- struct{}{}
	q := new(mdns.Msg)
	q.SetQuestion("cdn.test.", mdns.TypeA)
	if r := e.Resolve(netip.MustParseAddr("192.0.2.1"), q); r.Rcode != mdns.RcodeServerFailure {
		t.Fatalf("занятый семафор: %d", r.Rcode)
	}
	<-e.forwards
	if r := e.Resolve(netip.MustParseAddr("192.0.2.1"), q); r.Rcode != mdns.RcodeSuccess {
		t.Fatalf("после освобождения: %d", r.Rcode)
	}
}

func TestRateLimitUDPOnly(t *testing.T) {
	e := engineWith(t, snapshot.DNS{RateQPS: 1, Domains: []snapshot.Domain{{
		Name: "example.com", Match: snapshot.MatchSuffix,
		Proxies: []snapshot.Proxy{{ID: "p", IPv4: "203.0.113.10", Weight: 1}},
	}}})
	q := new(mdns.Msg)
	q.SetQuestion("example.com.", mdns.TypeA)
	var answered, slipped, dropped int
	for i := 0; i < 12; i++ {
		w := udpFrom("192.0.2.1")
		e.ServeDNS(w, q.Copy())
		switch {
		case w.msg == nil:
			dropped++
		case w.msg.Truncated && len(w.msg.Answer) == 0:
			slipped++
		default:
			answered++
		}
	}
	// Burst is 2 × 1 qps.
	if answered != 2 || slipped != 5 || dropped != 5 {
		t.Fatalf("answered=%d slipped=%d dropped=%d", answered, slipped, dropped)
	}
	tcp := &fakeRW{addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 5353}}
	e.ServeDNS(tcp, q.Copy())
	if tcp.msg == nil || len(tcp.msg.Answer) != 1 {
		t.Fatal("TCP попал под лимит UDP")
	}
}

func TestBadSnapshotKeepsOld(t *testing.T) {
	e := engineWith(t, snapshot.DNS{Version: 1})
	if err := e.SetSnapshot(&snapshot.DNS{Version: 2, Allow: []string{"nope"}}); err == nil {
		t.Fatal("битый снимок принят")
	}
	if e.Snapshot().Version != 1 {
		t.Fatal("старый снимок потерян")
	}
}

func TestNoSnapshotFails(t *testing.T) {
	e := NewEngine(nil)
	q := new(mdns.Msg)
	q.SetQuestion("a.test.", mdns.TypeA)
	done := make(chan *mdns.Msg, 1)
	go func() { done <- e.Resolve(netip.MustParseAddr("192.0.2.1"), q) }()
	select {
	case r := <-done:
		if r.Rcode != mdns.RcodeServerFailure {
			t.Fatalf("без снимка: %d", r.Rcode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("завис без снимка")
	}
}
