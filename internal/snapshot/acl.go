package snapshot

import (
	"fmt"
	"net/netip"
	"strings"
)

// ACL is the compiled form of allow/deny/bootstrap lists.
type ACL struct {
	allow     []netip.Prefix
	deny      []netip.Prefix
	bootstrap []netip.Prefix
}

// CompileACL parses CIDR lists into prefixes. An empty allow list means everyone is allowed.
func CompileACL(allow, deny, bootstrap []string) (*ACL, error) {
	a := &ACL{}
	var err error
	if a.allow, err = parsePrefixes(allow); err != nil {
		return nil, fmt.Errorf("allow: %w", err)
	}
	if a.deny, err = parsePrefixes(deny); err != nil {
		return nil, fmt.Errorf("deny: %w", err)
	}
	if a.bootstrap, err = parsePrefixes(bootstrap); err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	return a, nil
}

func parsePrefixes(items []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(items))
	for _, raw := range items {
		p, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", raw, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// Allowed reports whether addr may query this node. Bootstrap overrides deny, deny overrides allow.
// An empty allow list permits everyone; this is the open resolver mode chosen by the user.
func (a *ACL) Allowed(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	for _, p := range a.bootstrap {
		if p.Contains(addr) {
			return true
		}
	}
	for _, p := range a.deny {
		if p.Contains(addr) {
			return false
		}
	}
	if len(a.allow) == 0 {
		return true
	}
	for _, p := range a.allow {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
