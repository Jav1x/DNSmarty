package snapshot

import (
	"net/netip"
	"testing"
)

func TestACLOpen(t *testing.T) {
	acl, err := CompileACL(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !acl.Allowed(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("empty allow refused")
	}
}

func TestACLBootstrapOverridesDeny(t *testing.T) {
	acl, err := CompileACL(nil, []string{"10.0.0.0/8"}, []string{"10.1.2.3/32"})
	if err != nil {
		t.Fatal(err)
	}
	if !acl.Allowed(netip.MustParseAddr("10.1.2.3")) {
		t.Fatal("bootstrap did not override deny")
	}
	if acl.Allowed(netip.MustParseAddr("10.1.2.4")) {
		t.Fatal("deny did not match")
	}
}

func TestACLDenyOverridesAllow(t *testing.T) {
	acl, err := CompileACL([]string{"0.0.0.0/0"}, []string{"192.0.2.0/24"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if acl.Allowed(netip.MustParseAddr("192.0.2.1")) {
		t.Fatal("deny did not override allow")
	}
	if !acl.Allowed(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("allow did not pass")
	}
}

func TestACLWhitelist(t *testing.T) {
	acl, err := CompileACL([]string{"203.0.113.0/24"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !acl.Allowed(netip.MustParseAddr("203.0.113.50")) {
		t.Fatal("inside whitelist refused")
	}
	if acl.Allowed(netip.MustParseAddr("198.51.100.1")) {
		t.Fatal("outside whitelist allowed")
	}
}

func TestACLInvalid(t *testing.T) {
	if _, err := CompileACL([]string{"not-a-cidr"}, nil, nil); err == nil {
		t.Fatal("malformed CIDR accepted")
	}
}
