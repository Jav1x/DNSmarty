package buildinfo

import "strings"

// Version is set at build time: -ldflags "-X dnsmarty/internal/buildinfo.Version=0.1.0".
var Version = "dev"

// Normalize trims space and a leading "v" so "v0.1.0" and "0.1.0" compare equal.
func Normalize(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// AgentOutdated is true when the agent should be updated to match the panel.
// Local/dev panel builds do not flag agents: there is no release version to match.
func AgentOutdated(panel, agent string) bool {
	p := Normalize(panel)
	a := Normalize(agent)
	if a == "" || p == "" || p == "dev" {
		return false
	}
	return a != p
}
