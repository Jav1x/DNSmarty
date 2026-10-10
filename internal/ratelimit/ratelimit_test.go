package ratelimit

import (
	"net/netip"
	"testing"
	"time"
)

func TestOff(t *testing.T) {
	l := New(0)
	for i := 0; i < 1000; i++ {
		if l.Check(netip.MustParseAddr("192.0.2.1")) != Pass {
			t.Fatal("disabled limiter refused")
		}
	}
}

func TestBurstThenRefill(t *testing.T) {
	l := New(10)
	addr := netip.MustParseAddr("192.0.2.1")
	now := time.Unix(1000, 0)
	for i := 0; i < 20; i++ {
		if v := l.check(addr, now); v != Pass {
			t.Fatalf("refused inside burst at %d", i)
		}
	}
	if l.check(addr, now) == Pass {
		t.Fatal("over burst allowed")
	}
	// 10 qps: 100 ms gives one token.
	if l.check(addr, now.Add(100*time.Millisecond)) != Pass {
		t.Fatal("token did not refill")
	}
	if l.check(addr, now.Add(100*time.Millisecond)) == Pass {
		t.Fatal("extra token")
	}
}

func TestSlipAlternates(t *testing.T) {
	l := New(1)
	addr := netip.MustParseAddr("192.0.2.1")
	now := time.Unix(1000, 0)
	for i := 0; i < 2; i++ {
		l.check(addr, now)
	}
	var slip, drop int
	for i := 0; i < 10; i++ {
		switch l.check(addr, now) {
		case Slip:
			slip++
		case Drop:
			drop++
		default:
			t.Fatal("allowed with no tokens")
		}
	}
	if slip != 5 || drop != 5 {
		t.Fatalf("slip=%d drop=%d", slip, drop)
	}
}

func TestSameNetworkSharesBucket(t *testing.T) {
	l := New(1)
	now := time.Unix(1000, 0)
	l.check(netip.MustParseAddr("192.0.2.1"), now)
	l.check(netip.MustParseAddr("192.0.2.200"), now)
	if l.check(netip.MustParseAddr("192.0.2.77"), now) == Pass {
		t.Fatal("one /24 is one bucket")
	}
	if l.check(netip.MustParseAddr("192.0.3.1"), now) != Pass {
		t.Fatal("neighbor /24 shared the bucket")
	}
	l.check(netip.MustParseAddr("2001:db8:0:1::1"), now)
	l.check(netip.MustParseAddr("2001:db8:0:2::1"), now)
	if l.check(netip.MustParseAddr("2001:db8:0:3::1"), now) == Pass {
		t.Fatal("one /56 is one bucket")
	}
}

func TestMappedIPv4(t *testing.T) {
	l := New(1)
	now := time.Unix(1000, 0)
	l.check(netip.MustParseAddr("192.0.2.1"), now)
	l.check(netip.MustParseAddr("::ffff:192.0.2.1"), now)
	if l.check(netip.MustParseAddr("192.0.2.1"), now) == Pass {
		t.Fatal("IPv4-mapped did not match IPv4")
	}
}
