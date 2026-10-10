package buildinfo

import "testing"

func TestNormalize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"0.1.0", "0.1.0"},
		{"v0.1.0", "0.1.0"},
		{"  v0.1.0  ", "0.1.0"},
		{"dev", "dev"},
	}
	for _, tc := range cases {
		if got := Normalize(tc.in); got != tc.want {
			t.Fatalf("Normalize(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestAgentOutdated(t *testing.T) {
	t.Parallel()
	cases := []struct {
		panel, agent string
		want         bool
	}{
		{"dev", "0.1.0", false},
		{"", "0.1.0", false},
		{"0.1.0", "", false},
		{"0.1.0", "0.1.0", false},
		{"v0.1.0", "0.1.0", false},
		{"0.1.0", "v0.1.0", false},
		{"0.2.0", "0.1.0", true},
		{"0.1.0", "0.2.0", true},
		{"abc1234", "0.1.0", true},
	}
	for _, tc := range cases {
		if got := AgentOutdated(tc.panel, tc.agent); got != tc.want {
			t.Fatalf("AgentOutdated(%q, %q)=%v want %v", tc.panel, tc.agent, got, tc.want)
		}
	}
}
