package snapshot

import "testing"

func TestMatch(t *testing.T) {
	if !Match("www.example.com.", "example.com", MatchSuffix) {
		t.Fatal("suffix")
	}
	if !Match("example.com", "example.com", MatchSuffix) {
		t.Fatal("exact suffix")
	}
	if Match("notexample.com", "example.com", MatchSuffix) {
		t.Fatal("boundary")
	}
	if !Match("Example.COM.", "example.com", MatchFQDN) {
		t.Fatal("fqdn")
	}
	if Match("www.example.com", "example.com", MatchFQDN) {
		t.Fatal("fqdn child")
	}
	if !Match("www.example.com:443", "example.com", MatchSuffix) {
		t.Fatal("host port")
	}
}
