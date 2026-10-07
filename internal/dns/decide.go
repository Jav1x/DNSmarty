package dns

import (
	"hash/fnv"
	"net"
	"strings"
	"sync"

	mdns "github.com/miekg/dns"

	"dnsmarty/internal/netx"
	"dnsmarty/internal/snapshot"
)

type Action string

const (
	ActionRefuse  Action = "acl"
	ActionLocal   Action = "local"
	ActionForward Action = "forward"
	ActionFail    Action = "fail"
)

type Decision struct {
	Action Action
	Rcode  int
	IPs    []net.IP
	TTL    uint32
}

type Picker struct {
	mu sync.Mutex
	n  map[string]int
}

func (p *Picker) next(key string) int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.n == nil {
		p.n = map[string]int{}
	}
	v := p.n[key]
	p.n[key] = v + 1
	return v
}

func Decide(snap *snapshot.DNS, client net.IP, qname string, qtype uint16, pick *Picker) Decision {
	if snap == nil {
		return Decision{Action: ActionFail, Rcode: mdns.RcodeServerFailure}
	}
	allow, err := netx.ParseCIDRs(snap.Allow)
	if err != nil {
		return Decision{Action: ActionFail, Rcode: mdns.RcodeServerFailure}
	}
	boot, err := netx.ParseCIDRs(snap.Bootstrap)
	if err != nil {
		return Decision{Action: ActionFail, Rcode: mdns.RcodeServerFailure}
	}
	ttl := snap.TTL
	if !clientAllowed(client, allow, boot) {
		return Decision{Action: ActionRefuse, Rcode: mdns.RcodeRefused, TTL: ttl}
	}
	name := snapshot.Normalize(qname)
	dom := mostSpecific(snap.Domains, name)
	if dom == nil {
		return Decision{Action: ActionForward, TTL: ttl}
	}
	chosen := choose(dom, client, name, pick)
	if qtype != mdns.TypeA && qtype != mdns.TypeAAAA && qtype != mdns.TypeANY {
		return Decision{Action: ActionLocal, Rcode: mdns.RcodeSuccess, TTL: ttl}
	}
	return Decision{Action: ActionLocal, Rcode: mdns.RcodeSuccess, IPs: ipsFor(chosen, qtype), TTL: ttl}
}

func clientAllowed(ip net.IP, allow, boot []net.IPNet) bool {
	if len(allow) == 0 {
		return netx.Contains(boot, ip)
	}
	return netx.Contains(allow, ip) || netx.Contains(boot, ip)
}

func mostSpecific(domains []snapshot.Domain, host string) *snapshot.Domain {
	var best *snapshot.Domain
	for i := range domains {
		d := &domains[i]
		if !snapshot.Match(host, d.Name, d.Match) {
			continue
		}
		if best == nil || len(d.Name) > len(best.Name) || (len(d.Name) == len(best.Name) && d.Match == snapshot.MatchFQDN && best.Match != snapshot.MatchFQDN) {
			best = d
		}
	}
	return best
}

func choose(dom *snapshot.Domain, client net.IP, qname string, pick *Picker) []snapshot.Proxy {
	live := dom.Proxies
	if len(live) == 0 {
		return nil
	}
	var idx int
	switch dom.Balance {
	case snapshot.BalanceSticky:
		idx = stickyIndex(client, qname, len(live))
		return []snapshot.Proxy{live[idx]}
	case snapshot.BalanceWeighted:
		idx = weightedIndex(dom.Name, live, pick)
	case snapshot.BalanceRoundRobin:
		idx = 0
		if pick != nil {
			idx = pick.next(dom.Name) % len(live)
		}
	default:
		idx = 0
		if pick != nil {
			idx = pick.next(dom.Name) % len(live)
		}
	}
	out := []snapshot.Proxy{live[idx]}
	if len(live) > 1 {
		out = append(out, live[(idx+1)%len(live)])
	}
	return out
}

func stickyIndex(client net.IP, qname string, n int) int {
	h := fnv.New32a()
	if v4 := client.To4(); v4 != nil {
		_, _ = h.Write(v4[:3])
	} else if v6 := client.To16(); v6 != nil {
		_, _ = h.Write(v6[:6])
	}
	_, _ = h.Write([]byte(strings.ToLower(qname)))
	return int(h.Sum32() % uint32(n))
}

func weightedIndex(name string, live []snapshot.Proxy, pick *Picker) int {
	sum := 0
	weights := make([]int, len(live))
	for i, p := range live {
		w := p.Weight
		if w < 1 {
			w = 1
		}
		weights[i] = w
		sum += w
	}
	n := 0
	if pick != nil {
		n = pick.next(name+"#w") % sum
	}
	acc := 0
	for i, w := range weights {
		acc += w
		if n < acc {
			return i
		}
	}
	return 0
}

func ipsFor(proxies []snapshot.Proxy, qtype uint16) []net.IP {
	wantA := qtype == mdns.TypeA || qtype == mdns.TypeANY
	wantAAAA := qtype == mdns.TypeAAAA || qtype == mdns.TypeANY
	var out []net.IP
	for _, p := range proxies {
		if wantA && p.IPv4 != "" {
			if ip := net.ParseIP(p.IPv4); ip != nil && ip.To4() != nil {
				out = append(out, ip.To4())
			}
		}
		if wantAAAA && p.IPv6 != "" {
			if ip := net.ParseIP(p.IPv6); ip != nil && ip.To4() == nil {
				out = append(out, ip)
			}
		}
	}
	return out
}
