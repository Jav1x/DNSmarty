package panel

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestClientIP(t *testing.T) {
	s := &Server{opts: Options{TrustedProxies: DefaultTrustedProxies()}}
	cases := []struct {
		remote, xff, want string
	}{
		// Direct client: its header is ignored.
		{"203.0.113.5:1234", "198.51.100.1", "203.0.113.5"},
		// Through Caddy on the compose network.
		{"172.18.0.3:4444", "198.51.100.7", "198.51.100.7"},
		// A forged left-hand hop does not win over the address Caddy appended.
		{"172.18.0.3:4444", "1.2.3.4, 198.51.100.7", "198.51.100.7"},
		// Chain of trusted proxies.
		{"127.0.0.1:1", "198.51.100.9, 10.0.0.2", "198.51.100.9"},
		// Garbage stops the walk at the last trusted hop.
		{"127.0.0.1:1", "nonsense", "127.0.0.1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := s.clientIP(r).String(); got != c.want {
			t.Errorf("remote=%s xff=%q: %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		site, origin string
		ok           bool
	}{
		{"", "", true},
		{"same-origin", "https://panel.test", true},
		{"none", "", true},
		{"cross-site", "", false},
		{"same-site", "https://other.panel.test", false},
		{"", "https://evil.test", false},
		{"", "null", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "https://panel.test/api/x", nil)
		if c.site != "" {
			r.Header.Set("Sec-Fetch-Site", c.site)
		}
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := sameOrigin(r); got != c.ok {
			t.Errorf("site=%q origin=%q: %v", c.site, c.origin, got)
		}
	}
}

func TestReadJSON(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
	}
	try := func(ct, body string) int {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		w := httptest.NewRecorder()
		if readJSON(w, r, &dst) {
			return http.StatusOK
		}
		return w.Code
	}
	if c := try("text/plain", `{"name":"a"}`); c != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain: %d", c)
	}
	if c := try("application/json", `{"name":"a","admin":true}`); c != http.StatusBadRequest {
		t.Errorf("лишнее поле: %d", c)
	}
	if c := try("application/json", `{"name":"a"}{"name":"b"}`); c != http.StatusBadRequest {
		t.Errorf("два объекта: %d", c)
	}
	if c := try("application/json; charset=utf-8", `{"name":"a"}`); c != http.StatusOK {
		t.Errorf("корректный: %d", c)
	}
}

func TestLoginLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := newLoginLimiter()
	l.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if l.blocked("192.0.2.1", "admin") > 0 {
			t.Fatalf("заблокирован после %d", i)
		}
		l.fail("192.0.2.1", "admin")
	}
	if l.blocked("192.0.2.1", "admin") == 0 {
		t.Fatal("шестая попытка разрешена")
	}
	// Another address may still try the same user until the per-user limit.
	if l.blocked("192.0.2.2", "admin") > 0 {
		t.Fatal("чужой IP заблокирован лимитом по адресу")
	}
	now = now.Add(15*time.Minute + time.Second)
	if l.blocked("192.0.2.1", "admin") > 0 {
		t.Fatal("окно не истекло")
	}
	// Per-user limit across many addresses.
	for i := 0; i < 20; i++ {
		l.fail(netip.AddrFrom4([4]byte{198, 51, 100, byte(i)}).String(), "Admin")
	}
	if l.blocked("203.0.113.9", "admin") == 0 {
		t.Fatal("лимит по логину не сработал")
	}
}

func TestSecureHeaders(t *testing.T) {
	s := &Server{opts: Options{CookieSecure: true}}
	h := s.secureHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for path, cache := range map[string]string{"/api/me": "no-store", "/assets/a.js": "immutable", "/": "no-cache"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if !strings.Contains(w.Header().Get("Cache-Control"), cache) {
			t.Errorf("%s: Cache-Control %q", path, w.Header().Get("Cache-Control"))
		}
		for _, name := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Strict-Transport-Security"} {
			if w.Header().Get(name) == "" {
				t.Errorf("%s: нет %s", path, name)
			}
		}
	}
}

func TestObserveRecoversPanic(t *testing.T) {
	s := New(nil, nil, Options{})
	h := s.observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/x", nil))
	if w.Code != http.StatusInternalServerError || w.Header().Get("X-Request-ID") == "" {
		t.Fatalf("code=%d id=%q", w.Code, w.Header().Get("X-Request-ID"))
	}
}
