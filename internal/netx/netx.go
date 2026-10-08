package netx

import (
	"net"
	"net/netip"
)

// blocked are ranges the proxy must never dial: a client could otherwise point an allowed name
// at the node's own network. net.IP helpers miss several of them (0/8, CGNAT, 240/4, 6to4...).
var blocked = mustPrefixes(
	"0.0.0.0/8",      // "this network"
	"10.0.0.0/8",     // private
	"100.64.0.0/10",  // carrier-grade NAT
	"127.0.0.0/8",    // loopback
	"169.254.0.0/16", // link-local, cloud metadata
	"172.16.0.0/12",  // private
	"192.0.0.0/24",   // IETF protocol assignments
	"192.168.0.0/16", // private
	"198.18.0.0/15",  // benchmarking
	"224.0.0.0/4",    // multicast
	"240.0.0.0/4",    // reserved, broadcast
	"::/128",         // unspecified
	"::1/128",        // loopback
	"100::/64",       // discard
	"2001::/32",      // Teredo
	"2001:db8::/32",  // documentation
	"fc00::/7",       // unique local
	"fe80::/10",      // link-local
	"ff00::/8",       // multicast
)

// Prefixes that embed an IPv4 address; the embedded one is checked too.
var (
	nat64     = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour = netip.MustParsePrefix("2002::/16")
)

func mustPrefixes(items ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(items))
	for i, s := range items {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// IsBlocked reports whether the proxy must refuse to dial ip: a special-purpose range or
// one of the node's own public addresses.
func IsBlocked(ip net.IP, own []net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	for _, o := range own {
		if oa, ok := netip.AddrFromSlice(o); ok && oa.Unmap() == addr {
			return true
		}
	}
	return blockedAddr(addr)
}

func blockedAddr(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return true
	}
	for _, p := range blocked {
		if p.Contains(addr) {
			return true
		}
	}
	if addr.Is6() {
		b := addr.As16()
		switch {
		case nat64.Contains(addr):
			return blockedAddr(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		case sixToFour.Contains(addr):
			return blockedAddr(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
		}
	}
	return false
}
