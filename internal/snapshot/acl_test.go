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
		t.Fatal("пустой allow отказал")
	}
}

func TestACLBootstrapOverridesDeny(t *testing.T) {
	acl, err := CompileACL(nil, []string{"10.0.0.0/8"}, []string{"10.1.2.3/32"})
	if err != nil {
		t.Fatal(err)
	}
	if !acl.Allowed(netip.MustParseAddr("10.1.2.3")) {
		t.Fatal("bootstrap не перекрыл deny")
	}
	if acl.Allowed(netip.MustParseAddr("10.1.2.4")) {
		t.Fatal("deny не сработал")
	}
}

func TestACLDenyOverridesAllow(t *testing.T) {
	acl, err := CompileACL([]string{"0.0.0.0/0"}, []string{"192.0.2.0/24"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if acl.Allowed(netip.MustParseAddr("192.0.2.1")) {
		t.Fatal("deny не перекрыл allow")
	}
	if !acl.Allowed(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("allow не пропустил")
	}
}

func TestACLWhitelist(t *testing.T) {
	acl, err := CompileACL([]string{"203.0.113.0/24"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !acl.Allowed(netip.MustParseAddr("203.0.113.50")) {
		t.Fatal("в whitelist отказано")
	}
	if acl.Allowed(netip.MustParseAddr("198.51.100.1")) {
		t.Fatal("вне whitelist пропущено")
	}
}

func TestACLInvalid(t *testing.T) {
	if _, err := CompileACL([]string{"not-a-cidr"}, nil, nil); err == nil {
		t.Fatal("битный CIDR принят")
	}
}
