package snapshot

import (
	"net"
	"strings"
)

func Match(host, name, kind string) bool {
	host = Normalize(host)
	name = Normalize(name)
	if host == "" || name == "" {
		return false
	}
	switch kind {
	case MatchFQDN:
		return host == name
	case MatchSuffix:
		return host == name || strings.HasSuffix(host, "."+name)
	default:
		return false
	}
}

func Normalize(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.Trim(host, "[]")
}
