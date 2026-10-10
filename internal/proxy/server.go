package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	mdns "github.com/miekg/dns"

	"dnsmarty/internal/lru"
	"dnsmarty/internal/metrics"
	"dnsmarty/internal/netx"
	"dnsmarty/internal/snapshot"
)

const defaultMaxConns = 4096

// resolveCacheSize bounds cached DNS answers for origins.
const resolveCacheSize = 4096

// A resolved name is kept for at most a minute: long enough to spare the upstream
// during a burst on one name, short enough that a moved origin is followed soon.
const resolveTTL = 60 * time.Second
const resolveNegTTL = 5 * time.Second

type Report struct {
	At        time.Time
	ClientIP  string
	SNI       string
	BytesUp   int64
	BytesDown int64
	Status    string
	DialError string
}

// resolved is one cached lookup result, positive or negative.
type resolved struct {
	ips     []net.IP
	expires time.Time
	neg     bool
}

// resolveCached returns the addresses for name from the cache when fresh, otherwise
// asks the upstreams and stores the result. A negative result is cached briefly so a
// non-resolving name does not trigger a lookup on every connection.
func (s *Server) resolveCached(name string, upstreams []string) ([]net.IP, error) {
	key := snapshot.Normalize(name)
	now := time.Now()
	s.resMu.Lock()
	if r, ok := s.res.Get(key); ok && now.Before(r.expires) {
		s.resMu.Unlock()
		if r.neg {
			return nil, errors.New("no address")
		}
		return r.ips, nil
	}
	s.resMu.Unlock()
	ips, err := resolveOrigin(name, upstreams)
	ttl := resolveTTL
	if err != nil {
		ttl = resolveNegTTL
	}
	s.resMu.Lock()
	s.res.Add(key, resolved{ips: ips, expires: now.Add(ttl), neg: err != nil})
	s.resMu.Unlock()
	return ips, err
}

// compiled is a snapshot with its client lists parsed once.
type compiled struct {
	snap *snapshot.ProxySnap
	acl  *snapshot.ACL
}

type Server struct {
	snap    atomic.Pointer[compiled]
	reports chan Report
	log     *slog.Logger
	http    string
	https   string

	maxConns atomic.Int64
	conns    atomic.Int64

	mu     sync.Mutex
	active map[string]int

	// aclSeen throttles reports of refused clients: a scan would otherwise fill the log.
	aclMu   sync.Mutex
	aclSeen map[string]time.Time

	resMu sync.Mutex
	res   *lru.Cache[string, resolved]

	transport *http.Transport
}

func New(log *slog.Logger, httpAddr, httpsAddr string) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		reports: make(chan Report, 1024),
		log:     log,
		http:    httpAddr,
		https:   httpsAddr,
		active:  map[string]int{},
		aclSeen: map[string]time.Time{},
		res:     lru.New[string, resolved](resolveCacheSize),
	}
	s.maxConns.Store(defaultMaxConns)
	s.transport = s.newTransport()
	return s
}

// SetMaxConns caps client connections across both ports.
func (s *Server) SetMaxConns(n int) {
	if n < 1 {
		n = 1
	}
	s.maxConns.Store(int64(n))
}

// SetSnapshot compiles and installs p. A snapshot that does not compile is rejected.
func (s *Server) SetSnapshot(p *snapshot.ProxySnap) error {
	if p == nil {
		return errors.New("empty snapshot")
	}
	cp := *p
	acl, err := snapshot.CompileACL(cp.Allow, cp.Deny, cp.Bootstrap)
	if err != nil {
		return err
	}
	s.snap.Store(&compiled{snap: &cp, acl: acl})
	metrics.ConfigVersion.Set(float64(cp.Version))
	return nil
}

func (s *Server) Snapshot() *snapshot.ProxySnap {
	if c := s.snap.Load(); c != nil {
		return c.snap
	}
	return nil
}

func (s *Server) Reports() <-chan Report { return s.reports }

func (s *Server) Listen(ctx context.Context) error {
	var lc net.ListenConfig
	lnHTTP, err := lc.Listen(ctx, "tcp", s.http)
	if err != nil {
		return err
	}
	lnTLS, err := lc.Listen(ctx, "tcp", s.https)
	if err != nil {
		_ = lnHTTP.Close()
		return err
	}
	return s.Serve(ctx, lnHTTP, lnTLS)
}

// Serve runs both ports on ready listeners until ctx ends.
func (s *Server) Serve(ctx context.Context, lnHTTP, lnTLS net.Listener) error {
	web := s.httpServer()
	errCh := make(chan error, 2)
	go func() { errCh <- s.acceptTLS(ctx, s.gate(lnTLS)) }()
	go func() {
		err := web.Serve(s.gate(lnHTTP))
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()
	var result error
	select {
	case <-ctx.Done():
	case result = <-errCh:
	}
	_ = lnTLS.Close()
	// WithoutCancel: the parent is already done, only the deadline matters.
	shut, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_ = web.Shutdown(shut)
	s.transport.CloseIdleConnections()
	return result
}

func (s *Server) acceptTLS(ctx context.Context, ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) && ctx.Err() == nil {
				// Not a shutdown: a real accept failure.
				return err
			}
			return nil
		}
		go s.handleTLS(ctx, conn.(*gatedConn))
	}
}

// gate wraps a listener so every accepted connection has passed the global cap,
// the client lists and the per-client session limit.
func (s *Server) gate(ln net.Listener) net.Listener { return &gateListener{Listener: ln, s: s} }

type gateListener struct {
	net.Listener
	s *Server
}

func (l *gateListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if g := l.s.admit(conn); g != nil {
			return g, nil
		}
	}
}

// gatedConn releases its slots on Close, once.
type gatedConn struct {
	net.Conn
	client netip.Addr
	key    string
	snap   *compiled
	once   sync.Once
	s      *Server
}

func (c *gatedConn) Close() error {
	c.once.Do(func() {
		c.s.release(c.key)
		c.s.conns.Add(-1)
	})
	return c.Conn.Close()
}

// admit returns nil and closes conn when it must not be served.
func (s *Server) admit(conn net.Conn) *gatedConn {
	client := addrOf(conn.RemoteAddr())
	if s.conns.Add(1) > s.maxConns.Load() {
		s.conns.Add(-1)
		_ = conn.Close()
		metrics.ProxySessions.WithLabelValues("overload").Inc()
		return nil
	}
	c := s.snap.Load()
	if c == nil {
		s.conns.Add(-1)
		_ = conn.Close()
		s.emit(Report{At: time.Now().UTC(), ClientIP: client.String(), Status: "refused", DialError: "no snapshot"})
		return nil
	}
	if !c.acl.Allowed(client) {
		s.conns.Add(-1)
		_ = conn.Close()
		s.refuseACL(client)
		return nil
	}
	key := clientKey(client)
	limit := c.snap.SessionLimit
	if limit < 1 {
		limit = 1
	}
	if !s.acquire(key, limit) {
		s.conns.Add(-1)
		_ = conn.Close()
		s.emit(Report{At: time.Now().UTC(), ClientIP: client.String(), Status: "limited"})
		return nil
	}
	return &gatedConn{Conn: conn, client: client, key: key, snap: c, s: s}
}

func (s *Server) refuseACL(client netip.Addr) {
	metrics.ProxySessions.WithLabelValues("acl").Inc()
	ip := client.String()
	now := time.Now()
	s.aclMu.Lock()
	last, seen := s.aclSeen[ip]
	if !seen || now.Sub(last) > time.Minute {
		s.aclSeen[ip] = now
		if len(s.aclSeen) > 10000 {
			for k, t := range s.aclSeen {
				if now.Sub(t) > time.Minute {
					delete(s.aclSeen, k)
				}
			}
		}
		seen = false
	}
	s.aclMu.Unlock()
	if !seen {
		s.report(Report{At: now.UTC(), ClientIP: ip, Status: "acl"})
	}
}

func (s *Server) handleTLS(ctx context.Context, conn *gatedConn) {
	defer conn.Close()
	clientIP := conn.client.String()
	snap := conn.snap.snap
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	name, prefix, err := ReadClientHello(conn)
	_ = conn.SetDeadline(time.Time{})
	if err != nil || !nameAllowed(name, snap.Names) {
		msg := "not in snapshot"
		if err != nil {
			msg = err.Error()
		}
		s.emit(Report{At: time.Now().UTC(), ClientIP: clientIP, SNI: clip(name, 255), Status: "refused", DialError: clip(msg, 300)})
		return
	}
	up, err := s.dialOrigin(ctx, snap, snapshot.Normalize(name), "443")
	if err != nil {
		s.log.Warn("dial", "sni", name, "client", clientIP, "err", err)
		s.emit(Report{At: time.Now().UTC(), ClientIP: clientIP, SNI: clip(name, 255), Status: "dial_error", DialError: clip(err.Error(), 300)})
		return
	}
	defer up.Close()
	if _, err := up.Write(prefix); err != nil {
		s.emit(Report{At: time.Now().UTC(), ClientIP: clientIP, SNI: clip(name, 255), Status: "dial_error", DialError: clip(err.Error(), 300)})
		return
	}
	upN, downN := splice(conn.Conn, up, idleTimeout(snap))
	metrics.ProxyBytes.WithLabelValues("up").Add(float64(upN))
	metrics.ProxyBytes.WithLabelValues("down").Add(float64(downN))
	s.emit(Report{At: time.Now().UTC(), ClientIP: clientIP, SNI: clip(name, 255), BytesUp: upN, BytesDown: downN, Status: "ok"})
}

// dialOrigin resolves name through the snapshot upstreams and dials the first address
// that is not a special-purpose range or the node itself.
func (s *Server) dialOrigin(ctx context.Context, snap *snapshot.ProxySnap, name, port string) (net.Conn, error) {
	ips, err := s.resolveCached(name, snap.Upstreams)
	if err != nil {
		return nil, err
	}
	own := ownIPs(snap)
	dialTimeout := time.Duration(snap.DialTimeoutMs) * time.Millisecond
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	last := errors.New("no address")
	for _, ip := range ips {
		if netx.IsBlocked(ip, own) {
			last = errors.New("blocked address")
			continue
		}
		d := net.Dialer{Timeout: dialTimeout}
		c, derr := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if derr != nil {
			last = derr
			continue
		}
		return c, nil
	}
	return nil, last
}

func idleTimeout(snap *snapshot.ProxySnap) time.Duration {
	idle := time.Duration(snap.IdleTimeoutMs) * time.Millisecond
	if idle <= 0 {
		idle = 120 * time.Second
	}
	return idle
}

func (s *Server) emit(r Report) {
	metrics.ProxySessions.WithLabelValues(r.Status).Inc()
	s.report(r)
}

func (s *Server) report(r Report) {
	select {
	case s.reports <- r:
	default:
	}
}

func (s *Server) acquire(key string, limit int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[key] >= limit {
		return false
	}
	s.active[key]++
	return true
}

func (s *Server) release(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[key]--
	if s.active[key] <= 0 {
		delete(s.active, key)
	}
}

// clientKey groups IPv6 clients by /64: one host usually owns the whole /64.
func clientKey(a netip.Addr) string {
	if a.Is6() {
		p, err := a.Prefix(64)
		if err == nil {
			return p.String()
		}
	}
	return a.String()
}

func nameAllowed(host string, rules []snapshot.NameRule) bool {
	for _, rule := range rules {
		if snapshot.Match(host, rule.Name, rule.Match) {
			return true
		}
	}
	return false
}

func ownIPs(snap *snapshot.ProxySnap) []net.IP {
	var out []net.IP
	if ip := net.ParseIP(snap.PublicIPv4); ip != nil {
		out = append(out, ip)
	}
	if ip := net.ParseIP(snap.PublicIPv6); ip != nil {
		out = append(out, ip)
	}
	return out
}

func resolveOrigin(name string, upstreams []string) ([]net.IP, error) {
	if name == "" {
		return nil, errors.New("empty name")
	}
	var out []net.IP
	for _, qtype := range []uint16{mdns.TypeA, mdns.TypeAAAA} {
		ips, cname, err := lookup(name, qtype, upstreams)
		if err != nil {
			continue
		}
		out = append(out, ips...)
		if len(ips) == 0 && cname != "" {
			ips2, _, err2 := lookup(cname, qtype, upstreams)
			if err2 == nil {
				out = append(out, ips2...)
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no address")
	}
	return out, nil
}

func lookup(name string, qtype uint16, upstreams []string) ([]net.IP, string, error) {
	msg := new(mdns.Msg)
	msg.SetQuestion(mdns.Fqdn(name), qtype)
	c := &mdns.Client{Net: "tcp", Timeout: 5 * time.Second}
	var last error
	for _, u := range upstreams {
		addr := u
		if _, _, err := net.SplitHostPort(addr); err != nil {
			addr = net.JoinHostPort(addr, "53")
		}
		resp, _, err := c.Exchange(msg, addr)
		if err != nil {
			last = err
			continue
		}
		if resp == nil || resp.Rcode != mdns.RcodeSuccess && resp.Rcode != mdns.RcodeNameError {
			// SERVFAIL or REFUSED from one upstream: ask the next one.
			last = errors.New("upstream failed")
			continue
		}
		var ips []net.IP
		var cname string
		for _, rr := range resp.Answer {
			switch v := rr.(type) {
			case *mdns.A:
				ips = append(ips, v.A)
			case *mdns.AAAA:
				ips = append(ips, v.AAAA)
			case *mdns.CNAME:
				cname = v.Target
			}
		}
		return ips, cname, nil
	}
	if last == nil {
		last = errors.New("no upstream")
	}
	return nil, "", last
}

// splice copies both ways until either side closes or nothing moves in either direction for idle.
// A single activity clock keeps a long one-way download alive while the client stays silent.
func splice(client, origin net.Conn, idle time.Duration) (up, down int64) {
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	done := make(chan struct{})
	go func() {
		tick := idle / 4
		if tick < 50*time.Millisecond {
			tick = 50 * time.Millisecond
		}
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-t.C:
				if now.UnixNano()-last.Load() > int64(idle) {
					_ = client.Close()
					_ = origin.Close()
					return
				}
			}
		}
	}()
	var upN atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(origin, activity{client, &last})
		upN.Store(n)
		closeWrite(origin)
	}()
	downN, _ := io.Copy(client, activity{origin, &last})
	closeWrite(client)
	wg.Wait()
	close(done)
	return upN.Load(), downN
}

// activity stamps the shared clock on every read.
type activity struct {
	r    io.Reader
	last *atomic.Int64
}

func (a activity) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if n > 0 {
		a.last.Store(time.Now().UnixNano())
	}
	return n, err
}

func closeWrite(c net.Conn) {
	type closer interface{ CloseWrite() error }
	if t, ok := c.(closer); ok {
		_ = t.CloseWrite()
	}
}

func addrOf(a net.Addr) netip.Addr {
	if t, ok := a.(*net.TCPAddr); ok {
		ip, _ := netip.AddrFromSlice(t.IP)
		return ip.Unmap()
	}
	if a == nil {
		return netip.Addr{}
	}
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
