package dns

import (
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mdns "github.com/miekg/dns"

	"dnsmarty/internal/snapshot"
)

// countedUpstream answers every A query with one record and counts how many it got.
func countedUpstream(t *testing.T, ttl uint32, rcode int) (string, *int64) {
	t.Helper()
	var n int64
	handler := mdns.HandlerFunc(func(w mdns.ResponseWriter, r *mdns.Msg) {
		atomic.AddInt64(&n, 1)
		m := new(mdns.Msg)
		m.SetReply(r)
		m.Rcode = rcode
		if rcode == mdns.RcodeSuccess {
			m.Answer = append(m.Answer, &mdns.A{
				Hdr: mdns.RR_Header{Name: r.Question[0].Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: ttl},
				A:   net.IPv4(203, 0, 113, 7),
			})
		}
		if rcode == mdns.RcodeNameError {
			m.Ns = append(m.Ns, &mdns.SOA{
				Hdr: mdns.RR_Header{Name: "test.", Rrtype: mdns.TypeSOA, Class: mdns.ClassINET, Ttl: ttl},
				Ns:  "ns.test.", Mbox: "hostmaster.test.", Serial: 1, Refresh: 3600, Retry: 900,
				Expire: 604800, Minttl: ttl,
			})
		}
		_ = w.WriteMsg(m)
	})
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &mdns.Server{PacketConn: pc, Handler: handler}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String(), &n
}

func engineWithUpstream(t *testing.T, upstreams ...string) *Engine {
	t.Helper()
	e := engineWith(t, snapshot.DNS{Upstreams: upstreams})
	e.SetForwardLimit(8)
	return e
}

func TestCacheHitsOneUpstreamQuery(t *testing.T) {
	addr, n := countedUpstream(t, 60, mdns.RcodeSuccess)
	e := engineWithUpstream(t, addr)
	q := new(mdns.Msg)
	q.SetQuestion("cdn.test.", mdns.TypeA)

	r1 := e.Resolve(netip.MustParseAddr("192.0.2.1"), q.Copy())
	if r1.Rcode != mdns.RcodeSuccess || len(r1.Answer) != 1 {
		t.Fatalf("first: %+v", r1)
	}
	// Same name and case: served from cache, no second upstream call.
	r2 := e.Resolve(netip.MustParseAddr("192.0.2.2"), q.Copy())
	if atomic.LoadInt64(n) != 1 {
		t.Fatalf("upstream called %d times", *n)
	}
	if r2.Answer[0].(*mdns.A).A.String() != "203.0.113.7" {
		t.Fatalf("cached answer differs")
	}
	// Different case still hits the same key.
	up := new(mdns.Msg)
	up.SetQuestion("CDN.TEST.", mdns.TypeA)
	r3 := e.Resolve(netip.MustParseAddr("192.0.2.3"), up)
	if atomic.LoadInt64(n) != 1 {
		t.Fatalf("case mismatch caused another call")
	}
	if r3.Question[0].Name != "CDN.TEST." {
		t.Fatalf("case not preserved: %q", r3.Question[0].Name)
	}
}

func TestCacheTTLDecays(t *testing.T) {
	addr, _ := countedUpstream(t, 60, mdns.RcodeSuccess)
	e := engineWithUpstream(t, addr)
	q := new(mdns.Msg)
	q.SetQuestion("dec.test.", mdns.TypeA)
	fresh := e.Resolve(netip.MustParseAddr("192.0.2.1"), q.Copy())
	if fresh.Answer[0].Header().Ttl != 60 {
		t.Fatalf("ttl=%d", fresh.Answer[0].Header().Ttl)
	}
	// Serve the cached entry with its TTL reduced by the age we inject via the store time.
	now := time.Now()
	ca := e.cache.get(cacheKey("dec.test.", mdns.TypeA), q, now.Add(40*time.Second))
	if ca == nil || ca.Answer[0].Header().Ttl != 20 {
		t.Fatalf("decay: %+v", ca)
	}
	// Past its TTL the entry is gone, not served with a floor.
	ca = e.cache.get(cacheKey("dec.test.", mdns.TypeA), q, now.Add(70*time.Second))
	if ca != nil {
		t.Fatalf("expired entry served: %+v", ca)
	}
}

func TestCacheSingleflightCoalesces(t *testing.T) {
	// The upstream sleeps so several concurrent queries arrive during one flight.
	var n int64
	handler := mdns.HandlerFunc(func(w mdns.ResponseWriter, r *mdns.Msg) {
		atomic.AddInt64(&n, 1)
		time.Sleep(120 * time.Millisecond)
		m := new(mdns.Msg)
		m.SetReply(r)
		m.Answer = append(m.Answer, &mdns.A{
			Hdr: mdns.RR_Header{Name: r.Question[0].Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 60}, A: net.IPv4(203, 0, 113, 9),
		})
		_ = w.WriteMsg(m)
	})
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &mdns.Server{PacketConn: pc, Handler: handler}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	e := engineWithUpstream(t, pc.LocalAddr().String())
	q := new(mdns.Msg)
	q.SetQuestion("slow.test.", mdns.TypeA)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := e.Resolve(netip.MustParseAddr("192.0.2.1"), q.Copy()); r.Rcode != mdns.RcodeSuccess {
				t.Errorf("rcode %d", r.Rcode)
			}
		}()
	}
	wg.Wait()
	if atomic.LoadInt64(&n) != 1 {
		t.Fatalf("singleflight made %d upstream calls", n)
	}
}

func TestCacheNegativeNXDOMAIN(t *testing.T) {
	addr, n := countedUpstream(t, 10, mdns.RcodeNameError)
	e := engineWithUpstream(t, addr)
	q := new(mdns.Msg)
	q.SetQuestion("none.test.", mdns.TypeA)
	r := e.Resolve(netip.MustParseAddr("192.0.2.1"), q.Copy())
	if r.Rcode != mdns.RcodeNameError {
		t.Fatalf("rcode %d", r.Rcode)
	}
	// The NXDOMAIN is cached, so a repeat does not hit the upstream.
	e.Resolve(netip.MustParseAddr("192.0.2.2"), q.Copy())
	if atomic.LoadInt64(n) != 1 {
		t.Fatalf("NXDOMAIN not cached: %d calls", *n)
	}
}

func TestCacheNotStoredZeroTTL(t *testing.T) {
	addr, n := countedUpstream(t, 0, mdns.RcodeSuccess)
	e := engineWithUpstream(t, addr)
	q := new(mdns.Msg)
	q.SetQuestion("zero.test.", mdns.TypeA)
	e.Resolve(netip.MustParseAddr("192.0.2.1"), q.Copy())
	e.Resolve(netip.MustParseAddr("192.0.2.2"), q.Copy())
	if atomic.LoadInt64(n) != 2 {
		t.Fatalf("zero-TTL cached: %d", *n)
	}
}

func TestCacheTTLClamps(t *testing.T) {
	m := new(mdns.Msg)
	m.Rcode = mdns.RcodeSuccess
	m.Answer = []mdns.RR{&mdns.A{Hdr: mdns.RR_Header{Name: "x.", Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 99999}}}
	if ttl, ok := cacheTTL(m); !ok || ttl != time.Hour {
		t.Fatalf("positive clamp: %v %v", ttl, ok)
	}
	soa := &mdns.SOA{Hdr: mdns.RR_Header{Name: "x.", Rrtype: mdns.TypeSOA, Class: mdns.ClassINET, Ttl: 99999}, Minttl: 99999}
	m2 := new(mdns.Msg)
	m2.Rcode = mdns.RcodeNameError
	m2.Ns = []mdns.RR{soa}
	if ttl, ok := cacheTTL(m2); !ok || ttl != negMaxTTL {
		t.Fatalf("negative clamp: %v %v", ttl, ok)
	}
	servfail := new(mdns.Msg)
	servfail.Rcode = mdns.RcodeServerFailure
	if _, ok := cacheTTL(servfail); ok {
		t.Fatal("SERVFAIL cached")
	}
}
