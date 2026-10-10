package dns

import (
	"net"
	"net/netip"
	"testing"

	mdns "github.com/miekg/dns"

	"dnsmarty/internal/snapshot"
)

func TestDecideRefusedAndLocal(t *testing.T) {
	snap := &snapshot.DNS{
		TTL:   30,
		Allow: []string{"198.51.100.0/24"},
		Domains: []snapshot.Domain{{
			Name:    "example.com",
			Match:   snapshot.MatchSuffix,
			Balance: snapshot.BalanceSticky,
			Proxies: []snapshot.Proxy{{ID: "p1", IPv4: "203.0.113.10", Weight: 1}},
		}},
	}
	c, err := Compile(snap)
	if err != nil {
		t.Fatal(err)
	}
	foreign := c.Decide(netip.MustParseAddr("192.0.2.9"), "www.example.com.", mdns.TypeA, nil)
	if foreign.Action != ActionRefuse || foreign.Rcode != mdns.RcodeRefused {
		t.Fatalf("foreign: %+v", foreign)
	}
	home := c.Decide(netip.MustParseAddr("198.51.100.8"), "www.example.com.", mdns.TypeA, nil)
	if home.Action != ActionLocal || len(home.IPs) != 1 || !home.IPs[0].Equal(net.ParseIP("203.0.113.10")) {
		t.Fatalf("home: %+v", home)
	}
	again := c.Decide(netip.MustParseAddr("198.51.100.8"), "www.example.com.", mdns.TypeA, nil)
	if again.Action != ActionLocal || !again.IPs[0].Equal(home.IPs[0]) {
		t.Fatal("sticky changed")
	}
	dead, err := Compile(&snapshot.DNS{Domains: []snapshot.Domain{{Name: "example.com", Match: snapshot.MatchSuffix}}})
	if err != nil {
		t.Fatal(err)
	}
	if d := dead.Decide(netip.MustParseAddr("198.51.100.8"), "example.com.", mdns.TypeA, nil); d.Action != ActionLocal || len(d.IPs) != 0 {
		t.Fatalf("domain with no live proxies must not go to upstream: %+v", d)
	}
	if d := c.Decide(netip.MustParseAddr("198.51.100.8"), "other.test.", mdns.TypeA, nil); d.Action != ActionForward {
		t.Fatalf("miss: %+v", d)
	}
}

func TestDecideTwoA(t *testing.T) {
	snap := &snapshot.DNS{
		Domains: []snapshot.Domain{{
			Name: "example.com", Match: snapshot.MatchFQDN, Balance: snapshot.BalanceRoundRobin,
			Proxies: []snapshot.Proxy{{ID: "a", IPv4: "203.0.113.1", Weight: 1}, {ID: "b", IPv4: "203.0.113.2", Weight: 1}},
		}},
	}
	c, err := Compile(snap)
	if err != nil {
		t.Fatal(err)
	}
	if d := c.Decide(netip.MustParseAddr("10.1.2.3"), "example.com.", mdns.TypeA, &Picker{}); len(d.IPs) != 2 {
		t.Fatalf("wanted two A: %+v", d.IPs)
	}
	snap.Domains[0].Balance = snapshot.BalanceSticky
	if d := c.Decide(netip.MustParseAddr("10.1.2.3"), "example.com.", mdns.TypeA, nil); len(d.IPs) != 1 {
		t.Fatalf("sticky wanted one A: %+v", d.IPs)
	}
}

func TestFQDNBeforePrefix(t *testing.T) {
	snap := &snapshot.DNS{
		Domains: []snapshot.Domain{
			{Name: "a.example.com", Match: snapshot.MatchSuffix, Proxies: []snapshot.Proxy{{ID: "suffix", IPv4: "198.51.100.1"}}},
			{Name: "x.a.example.com", Match: snapshot.MatchFQDN, Proxies: []snapshot.Proxy{{ID: "fqdn", IPv4: "198.51.100.2"}}},
		},
	}
	c, err := Compile(snap)
	if err != nil {
		t.Fatal(err)
	}
	d := c.Decide(netip.MustParseAddr("127.0.0.1"), "x.a.example.com.", mdns.TypeA, nil)
	if len(d.IPs) != 1 || d.IPs[0].String() != "198.51.100.2" {
		t.Fatalf("FQDN did not win: %+v", d)
	}
}

func TestLongestSuffix(t *testing.T) {
	snap := &snapshot.DNS{
		Domains: []snapshot.Domain{
			{Name: "example.com", Match: snapshot.MatchSuffix, Proxies: []snapshot.Proxy{{ID: "short", IPv4: "198.51.100.1"}}},
			{Name: "a.example.com", Match: snapshot.MatchSuffix, Proxies: []snapshot.Proxy{{ID: "long", IPv4: "198.51.100.2"}}},
		},
	}
	c, err := Compile(snap)
	if err != nil {
		t.Fatal(err)
	}
	d := c.Decide(netip.MustParseAddr("127.0.0.1"), "x.a.example.com.", mdns.TypeA, nil)
	if len(d.IPs) != 1 || d.IPs[0].String() != "198.51.100.2" {
		t.Fatalf("longer suffix did not win: %+v", d)
	}
}

func TestOtherTypes(t *testing.T) {
	snap := &snapshot.DNS{
		Domains: []snapshot.Domain{{
			Name:    "example.com",
			Match:   snapshot.MatchSuffix,
			Proxies: []snapshot.Proxy{{ID: "p", IPv4: "203.0.113.10"}},
		}},
	}
	c, err := Compile(snap)
	if err != nil {
		t.Fatal(err)
	}
	d := c.Decide(netip.MustParseAddr("127.0.0.1"), "example.com.", mdns.TypeMX, nil)
	if d.Action != ActionLocal || d.Rcode != mdns.RcodeSuccess || len(d.IPs) != 0 {
		t.Fatalf("MX: %+v", d)
	}
}

func TestACLOpenAndDenyLists(t *testing.T) {
	open := &snapshot.DNS{Domains: []snapshot.Domain{}}
	c, err := Compile(open)
	if err != nil {
		t.Fatal(err)
	}
	if c.Decide(netip.MustParseAddr("8.8.8.8"), "example.com.", mdns.TypeA, nil).Action != ActionForward {
		t.Fatal("open resolver refused")
	}
	denied := &snapshot.DNS{Deny: []string{"203.0.113.0/24"}, Domains: []snapshot.Domain{}}
	c, err = Compile(denied)
	if err != nil {
		t.Fatal(err)
	}
	if c.Decide(netip.MustParseAddr("203.0.113.5"), "example.com.", mdns.TypeA, nil).Action != ActionRefuse {
		t.Fatal("blacklist did not match")
	}
	if c.Decide(netip.MustParseAddr("198.51.100.9"), "example.com.", mdns.TypeA, nil).Action != ActionForward {
		t.Fatal("not in blacklist")
	}
	listed := &snapshot.DNS{Allow: []string{"198.51.100.0/24"}, Deny: []string{"198.51.100.9/32"}, Domains: []snapshot.Domain{}}
	c, err = Compile(listed)
	if err != nil {
		t.Fatal(err)
	}
	if c.Decide(netip.MustParseAddr("192.0.2.1"), "example.com.", mdns.TypeA, nil).Action != ActionRefuse {
		t.Fatal("outside whitelist")
	}
	if c.Decide(netip.MustParseAddr("198.51.100.9"), "example.com.", mdns.TypeA, nil).Action != ActionRefuse {
		t.Fatal("blacklist did not override whitelist")
	}
	if c.Decide(netip.MustParseAddr("198.51.100.8"), "example.com.", mdns.TypeA, nil).Action != ActionForward {
		t.Fatal("whitelist refused")
	}
	boot := &snapshot.DNS{Bootstrap: []string{"203.0.113.5/32"}, Deny: []string{"203.0.113.0/24"}, Domains: []snapshot.Domain{}}
	c, err = Compile(boot)
	if err != nil {
		t.Fatal(err)
	}
	if c.Decide(netip.MustParseAddr("203.0.113.5"), "example.com.", mdns.TypeA, nil).Action != ActionForward {
		t.Fatal("bootstrap lost to deny")
	}
}

func TestCompileBadCIDR(t *testing.T) {
	if _, err := Compile(&snapshot.DNS{Allow: []string{"not-a-cidr"}}); err == nil {
		t.Fatal("malformed CIDR compiled")
	}
}
