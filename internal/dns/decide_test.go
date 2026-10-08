package dns

import (
	"net"
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
	foreign := Decide(snap, net.ParseIP("192.0.2.9"), "www.example.com.", mdns.TypeA, nil)
	if foreign.Action != ActionRefuse || foreign.Rcode != mdns.RcodeRefused {
		t.Fatalf("foreign: %+v", foreign)
	}
	home := Decide(snap, net.ParseIP("198.51.100.8"), "www.example.com.", mdns.TypeA, nil)
	if home.Action != ActionLocal || len(home.IPs) != 1 || !home.IPs[0].Equal(net.ParseIP("203.0.113.10")) {
		t.Fatalf("home: %+v", home)
	}
	again := Decide(snap, net.ParseIP("198.51.100.8"), "www.example.com.", mdns.TypeA, nil)
	if len(again.IPs) != 1 || !again.IPs[0].Equal(home.IPs[0]) {
		t.Fatal("sticky drifted")
	}
	empty := *snap
	empty.Domains = []snapshot.Domain{{
		Name: "example.com", Match: snapshot.MatchSuffix, Balance: snapshot.BalanceRoundRobin,
	}}
	dead := Decide(&empty, net.ParseIP("198.51.100.8"), "example.com.", mdns.TypeA, nil)
	if dead.Action != ActionLocal || len(dead.IPs) != 0 {
		t.Fatalf("dead domain must not forward: %+v", dead)
	}
	miss := Decide(snap, net.ParseIP("198.51.100.8"), "other.test.", mdns.TypeA, nil)
	if miss.Action != ActionForward {
		t.Fatalf("miss: %+v", miss)
	}
}

func TestDecideTwoA(t *testing.T) {
	snap := &snapshot.DNS{
		TTL:   30,
		Allow: []string{"10.0.0.0/8"},
		Domains: []snapshot.Domain{{
			Name:    "example.com",
			Match:   snapshot.MatchFQDN,
			Balance: snapshot.BalanceRoundRobin,
			Proxies: []snapshot.Proxy{
				{ID: "a", IPv4: "203.0.113.1", Weight: 1},
				{ID: "b", IPv4: "203.0.113.2", Weight: 1},
			},
		}},
	}
	pick := &Picker{}
	d := Decide(snap, net.ParseIP("10.1.2.3"), "example.com.", mdns.TypeA, pick)
	if len(d.IPs) != 2 {
		t.Fatalf("want two A, got %+v", d.IPs)
	}
	sticky := *snap
	sticky.Domains[0].Balance = snapshot.BalanceSticky
	one := Decide(&sticky, net.ParseIP("10.1.2.3"), "example.com.", mdns.TypeA, nil)
	if len(one.IPs) != 1 {
		t.Fatalf("sticky want one A, got %d", len(one.IPs))
	}
}

func TestAccessLists(t *testing.T) {
	open := &snapshot.DNS{Domains: []snapshot.Domain{}}
	if Decide(open, net.ParseIP("198.51.100.1"), "example.com.", mdns.TypeA, nil).Action != ActionForward {
		t.Fatal("empty lists allow everyone")
	}
	denied := &snapshot.DNS{Deny: []string{"198.51.100.0/24"}, Domains: []snapshot.Domain{}}
	if Decide(denied, net.ParseIP("198.51.100.9"), "example.com.", mdns.TypeA, nil).Action != ActionRefuse {
		t.Fatal("blacklist")
	}
	if Decide(denied, net.ParseIP("203.0.113.9"), "example.com.", mdns.TypeA, nil).Action != ActionForward {
		t.Fatal("not on blacklist")
	}
	listed := &snapshot.DNS{Allow: []string{"198.51.100.0/24"}, Deny: []string{"198.51.100.9/32"}, Domains: []snapshot.Domain{}}
	if Decide(listed, net.ParseIP("192.0.2.1"), "example.com.", mdns.TypeA, nil).Action != ActionRefuse {
		t.Fatal("outside whitelist")
	}
	if Decide(listed, net.ParseIP("198.51.100.9"), "example.com.", mdns.TypeA, nil).Action != ActionRefuse {
		t.Fatal("blacklist wins inside whitelist")
	}
	if Decide(listed, net.ParseIP("198.51.100.8"), "example.com.", mdns.TypeA, nil).Action != ActionForward {
		t.Fatal("whitelist")
	}
	boot := &snapshot.DNS{Bootstrap: []string{"203.0.113.5/32"}, Deny: []string{"203.0.113.0/24"}, Domains: []snapshot.Domain{}}
	if Decide(boot, net.ParseIP("203.0.113.5"), "example.com.", mdns.TypeA, nil).Action != ActionForward {
		t.Fatal("bootstrap stays allowed")
	}
}
