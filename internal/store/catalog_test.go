package store

import (
	"errors"
	"testing"
)

func TestNormalizeUpstream(t *testing.T) {
	cases := []struct {
		in, want string
		invalid  bool
	}{
		{in: "1.1.1.1", want: "1.1.1.1:53"},
		{in: "1.1.1.1:853", want: "1.1.1.1:853"},
		{in: "DNS.Example.COM.:853", want: "dns.example.com:853"},
		{in: "tls://dns.example.com", want: "dns.example.com:853"},
		{in: "https://dns.example.com/dns-query", want: "https://dns.example.com/dns-query"},
		{in: "https://", invalid: true},
		{in: "example.com", invalid: true},
		{in: "example.com:53", invalid: true},
	}
	for _, c := range cases {
		got, err := NormalizeUpstream(c.in)
		if c.invalid {
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("%q: err = %v, want ErrInvalid", c.in, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q: got %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}
