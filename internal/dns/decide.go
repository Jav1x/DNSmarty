package dns

import (
	"hash/fnv"
	"net"
	"net/netip"
	"strings"
	"sync"

	mdns "github.com/miekg/dns"

	"dnsmarty/internal/snapshot"
)

type Action string

const (
	ActionRefuse  Action = "acl"
	ActionLocal   Action = "local"
	ActionForward Action = "forward"
	ActionFail    Action = "fail"
	ActionCached  Action = "cached"
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

// Compiled is a snapshot prepared for the query path: CIDRs parsed once, names indexed.
type Compiled struct {
	Snap   *snapshot.DNS
	acl    *snapshot.ACL
	fqdn   map[string]*snapshot.Domain
	suffix map[string]*snapshot.Domain
}

func Compile(s *snapshot.DNS) (*Compiled, error) {
	acl, err := snapshot.CompileACL(s.Allow, s.Deny, s.Bootstrap)
	if err != nil {
		return nil, err
	}
	c := &Compiled{
		Snap:   s,
		acl:    acl,
		fqdn:   map[string]*snapshot.Domain{},
		suffix: map[string]*snapshot.Domain{},
	}
	for i := range s.Domains {
		d := &s.Domains[i]
		name := snapshot.Normalize(d.Name)
		switch d.Match {
		case snapshot.MatchFQDN:
			c.fqdn[name] = d
		case snapshot.MatchSuffix:
			c.suffix[name] = d
		}
	}
	return c, nil
}

// Allowed applies the client lists of the snapshot.
func (c *Compiled) Allowed(client netip.Addr) bool {
	return c.acl.Allowed(client.Unmap())
}

// Decide picks the answer for one question. A nil Compiled means no snapshot yet.
func (c *Compiled) Decide(client netip.Addr, qname string, qtype uint16, pick *Picker) Decision {
	if c == nil {
		return Decision{Action: ActionFail, Rcode: mdns.RcodeServerFailure}
	}
	ttl := c.Snap.TTL
	if !c.Allowed(client) {
		return Decision{Action: ActionRefuse, Rcode: mdns.RcodeRefused, TTL: ttl}
	}
	name := snapshot.Normalize(qname)
	dom := c.mostSpecific(name)
	if dom == nil {
		return Decision{Action: ActionForward, TTL: ttl}
	}
	if qtype != mdns.TypeA && qtype != mdns.TypeAAAA {
		// The name is ours: other types get NODATA instead of leaking the origin's records.
		return Decision{Action: ActionLocal, Rcode: mdns.RcodeSuccess, TTL: ttl}
	}
	chosen := choose(dom, client, name, pick)
	return Decision{Action: ActionLocal, Rcode: mdns.RcodeSuccess, IPs: ipsFor(chosen, qtype), TTL: ttl}
}

// mostSpecific walks from the full name towards the root. The first hit is the longest rule;
// at the full name an exact rule beats a suffix rule.
func (c *Compiled) mostSpecific(host string) *snapshot.Domain {
	if host == "" {
		return nil
	}
	if d := c.fqdn[host]; d != nil {
		return d
	}
	for h := host; ; {
		if d := c.suffix[h]; d != nil {
			return d
		}
		i := strings.IndexByte(h, '.')
		if i < 0 {
			return nil
		}
		h = h[i+1:]
	}
}

func choose(dom *snapshot.Domain, client netip.Addr, qname string, pick *Picker) []snapshot.Proxy {
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
	default:
		idx = pick.next(dom.Name) % len(live)
	}
	out := []snapshot.Proxy{live[idx]}
	if len(live) > 1 {
		out = append(out, live[(idx+1)%len(live)])
	}
	return out
}

func stickyIndex(client netip.Addr, qname string, n int) int {
	h := fnv.New32a()
	client = client.Unmap()
	if client.Is4() {
		b := client.As4()
		_, _ = h.Write(b[:3])
	} else if client.Is6() {
		b := client.As16()
		_, _ = h.Write(b[:6])
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
	n := pick.next(name+"#w") % sum
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
	var out []net.IP
	for _, p := range proxies {
		if qtype == mdns.TypeA && p.IPv4 != "" {
			if ip := net.ParseIP(p.IPv4); ip != nil && ip.To4() != nil {
				out = append(out, ip.To4())
			}
		}
		if qtype == mdns.TypeAAAA && p.IPv6 != "" {
			if ip := net.ParseIP(p.IPv6); ip != nil && ip.To4() == nil {
				out = append(out, ip)
			}
		}
	}
	return out
}
