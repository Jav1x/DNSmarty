package netx

import (
	"net"
	"testing"
)

func TestIsBlocked(t *testing.T) {
	own := []net.IP{net.ParseIP("203.0.113.10")}
	blocked := []string{
		"127.0.0.1",
		"10.1.2.3",
		"192.168.1.1",
		"172.16.0.1",
		"172.31.255.1",
		"169.254.1.1",
		"203.0.113.10",
		"::1",
		"fe80::1",
		"fc00::1",
		"0.0.0.0",
	}
	for _, s := range blocked {
		if !IsBlocked(net.ParseIP(s), own) {
			t.Fatalf("want blocked %s", s)
		}
	}
	open := []string{"203.0.113.11", "8.8.8.8", "172.15.5.5"}
	for _, s := range open {
		if IsBlocked(net.ParseIP(s), own) {
			t.Fatalf("want open %s", s)
		}
	}
}
