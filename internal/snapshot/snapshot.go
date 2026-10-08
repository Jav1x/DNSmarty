package snapshot

const (
	RoleDNS   = "dns"
	RoleProxy = "proxy"

	MatchSuffix = "suffix"
	MatchFQDN   = "fqdn"

	BalanceRoundRobin = "round_robin"
	BalanceWeighted   = "weighted"
	BalanceSticky     = "sticky24"

	LiveWindowSec = 30
)

type DNS struct {
	// Epoch changes when the panel database starts over, so versions restart without being rejected as old.
	Epoch           string   `json:"epoch"`
	Version         int64    `json:"version"`
	TTL             uint32   `json:"ttl"`
	PullIntervalSec int      `json:"pull_interval_sec"`
	Bootstrap       []string `json:"bootstrap"`
	Allow           []string `json:"allow"`
	Deny            []string `json:"deny"`
	Upstreams       []string `json:"upstreams"`
	Domains         []Domain `json:"domains"`
}

type Domain struct {
	Name    string  `json:"name"`
	Match   string  `json:"match"`
	Balance string  `json:"balance"`
	Proxies []Proxy `json:"proxies"`
}

type Proxy struct {
	ID     string `json:"id"`
	IPv4   string `json:"ipv4,omitempty"`
	IPv6   string `json:"ipv6,omitempty"`
	Weight int    `json:"weight"`
}

type ProxySnap struct {
	Epoch           string     `json:"epoch"`
	Version         int64      `json:"version"`
	PullIntervalSec int        `json:"pull_interval_sec"`
	Names           []NameRule `json:"names"`
	PublicIPv4      string     `json:"public_ipv4,omitempty"`
	PublicIPv6      string     `json:"public_ipv6,omitempty"`
	SessionLimit    int        `json:"session_limit"`
	DialTimeoutMs   int        `json:"dial_timeout_ms"`
	IdleTimeoutMs   int        `json:"idle_timeout_ms"`
	Upstreams       []string   `json:"upstreams"`
}

type NameRule struct {
	Name  string `json:"name"`
	Match string `json:"match"`
}

func EmptySlicesDNS(s *DNS) {
	if s.Bootstrap == nil {
		s.Bootstrap = []string{}
	}
	if s.Allow == nil {
		s.Allow = []string{}
	}
	if s.Deny == nil {
		s.Deny = []string{}
	}
	if s.Upstreams == nil {
		s.Upstreams = []string{}
	}
	if s.Domains == nil {
		s.Domains = []Domain{}
	}
	for i := range s.Domains {
		if s.Domains[i].Proxies == nil {
			s.Domains[i].Proxies = []Proxy{}
		}
	}
}

func EmptySlicesProxy(s *ProxySnap) {
	if s.Names == nil {
		s.Names = []NameRule{}
	}
	if s.Upstreams == nil {
		s.Upstreams = []string{}
	}
}
