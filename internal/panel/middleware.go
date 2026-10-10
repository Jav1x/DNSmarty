package panel

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"dnsmarty/internal/store"
)

// Options configures how the panel sits behind its reverse proxy.
type Options struct {
	// CookieSecure marks the session cookie Secure, names it __Host-dnsmarty and turns on HSTS.
	// Off only for plain-HTTP development.
	CookieSecure bool
	// OAuthHTTP and OAuthEndpoints override the provider calls. Production leaves them empty.
	OAuthHTTP      *http.Client
	OAuthEndpoints map[string]OAuthEndpoint
	// TrustedProxies may set X-Forwarded-For. Anyone else's header is ignored.
	TrustedProxies []netip.Prefix
	Version        string
}

// DefaultTrustedProxies covers loopback and private networks: the panel is reached
// through Caddy on the compose network or through 127.0.0.1.
func DefaultTrustedProxies() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

// ParsePrefixes reads a comma-separated list of CIDRs or addresses.
func ParsePrefixes(raw string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if p, err := netip.ParsePrefix(item); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: %w", item, err)
		}
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

func (s *Server) cookieName() string {
	if s.opts.CookieSecure {
		// __Host- forbids Domain and requires Secure and Path=/: a sibling subdomain cannot plant it.
		return "__Host-dnsmarty"
	}
	return "dnsmarty"
}

func (s *Server) trusted(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range s.opts.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// clientIP is the peer address, or the rightmost untrusted X-Forwarded-For hop when the peer
// is a trusted proxy. Hops left of it were written by the client and prove nothing.
func (s *Server) clientIP(r *http.Request) netip.Addr {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	ip := peer.Addr().Unmap()
	if !s.trusted(ip) {
		return ip
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		h, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		h = h.Unmap()
		if !s.trusted(h) {
			return h
		}
		ip = h
	}
	return ip
}

type ctxKey int

const (
	sessionKey ctxKey = iota
	requestIDKey
)

func sessionFrom(ctx context.Context) store.Session {
	sess, _ := ctx.Value(sessionKey).(store.Session)
	return sess
}

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// sameOrigin rejects requests a browser marks as coming from another site.
// Sec-Fetch-Site is sent by every current browser; Origin is checked as well for older ones.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return origin == ""
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// withAuth requires a live session. State-changing requests must also come from the panel's
// own origin and echo the session's CSRF token in X-CSRF-Token.
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(s.cookieName())
		if err != nil || c.Value == "" {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "Sign in required.")
			return
		}
		sess, err := s.store.LookupSession(r.Context(), c.Value)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "Sign in required.")
			return
		}
		if unsafeMethod(r.Method) {
			token := r.Header.Get("X-CSRF-Token")
			if !sameOrigin(r) || token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(sess.CSRF)) != 1 {
				writeErr(w, http.StatusForbidden, "csrf", "Request rejected: reload the page.")
				return
			}
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionKey, sess)))
	}
}

const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; " +
	"connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func (s *Server) secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		if s.opts.CookieSecure {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/"):
			h.Set("Cache-Control", "no-store")
		case strings.HasPrefix(r.URL.Path, "/assets/"):
			// File names carry a content hash.
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			h.Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// observe tags every request with an ID, logs it, and turns a panic into a 500.
func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := newRequestID()
		w.Header().Set("X-Request-ID", id)
		sw := &statusWriter{ResponseWriter: w}
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey, id))
		rctx := r.Context()
		defer func() {
			if p := recover(); p != nil {
				if err, ok := p.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(p)
				}
				s.log.Error("panic", "id", id, "path", r.URL.Path, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
				if sw.status == 0 {
					writeErr(sw, http.StatusInternalServerError, "internal", "Internal panel error.")
				}
			}
			if r.URL.Path == "/healthz" {
				return
			}
			level := slog.LevelInfo
			if sw.status >= 500 {
				level = slog.LevelError
			} else if !strings.HasPrefix(r.URL.Path, "/api/") {
				level = slog.LevelDebug
			}
			s.log.Log(rctx, level, "http",
				"id", id, "method", r.Method, "path", r.URL.Path, "status", sw.status,
				"bytes", sw.bytes, "ms", time.Since(start).Milliseconds(), "ip", s.clientIP(r).String())
		}()
		next.ServeHTTP(sw, r)
	})
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// loginLimiter counts failed logins per client address and per username in a sliding window.
// It lives in memory: one panel process, and a restart only forgets recent failures.
type loginLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	perIP   int
	perUser int
	fails   map[string][]time.Time
	now     func() time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{window: 15 * time.Minute, perIP: 5, perUser: 20, fails: map[string][]time.Time{}, now: time.Now}
}

// blocked returns how long the caller must wait, 0 if it may try.
func (l *loginLimiter) blocked(ip, user string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	wait := l.waitLocked("ip:"+ip, l.perIP, now)
	if w := l.waitLocked("user:"+strings.ToLower(user), l.perUser, now); w > wait {
		wait = w
	}
	return wait
}

func (l *loginLimiter) waitLocked(key string, limit int, now time.Time) time.Duration {
	times := l.prune(key, now)
	if len(times) < limit {
		return 0
	}
	return times[len(times)-limit].Add(l.window).Sub(now)
}

func (l *loginLimiter) prune(key string, now time.Time) []time.Time {
	times := l.fails[key]
	i := 0
	for i < len(times) && now.Sub(times[i]) >= l.window {
		i++
	}
	times = times[i:]
	if len(times) == 0 {
		delete(l.fails, key)
		return nil
	}
	l.fails[key] = times
	return times
}

func (l *loginLimiter) fail(ip, user string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for _, key := range []string{"ip:" + ip, "user:" + strings.ToLower(user)} {
		l.fails[key] = append(l.prune(key, now), now)
	}
	if len(l.fails) > 10000 {
		for key := range l.fails {
			l.prune(key, now)
		}
	}
}

// success clears the address: a correct password from it ends the lockout for that client only.
func (l *loginLimiter) success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, "ip:"+ip)
}

func retryAfter(d time.Duration) string {
	sec := int(d.Seconds() + 0.999)
	if sec < 1 {
		sec = 1
	}
	return strconv.Itoa(sec)
}
