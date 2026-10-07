package netx

import "net"

func ParseCIDRs(items []string) ([]net.IPNet, error) {
	out := make([]net.IPNet, 0, len(items))
	for _, raw := range items {
		_, n, err := net.ParseCIDR(raw)
		if err != nil || n == nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, nil
}

func Contains(nets []net.IPNet, ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func IsBlocked(ip net.IP, own []net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, o := range own {
		if o != nil && o.Equal(ip) {
			return true
		}
		if v4 := o.To4(); v4 != nil && v4.Equal(ip) {
			return true
		}
	}
	if !ip.IsGlobalUnicast() {
		return true
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	return false
}
