package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	mdns "github.com/miekg/dns"

	"dnsmarty/internal/metrics"
	"dnsmarty/internal/netx"
	"dnsmarty/internal/snapshot"
)

type Report struct {
	At        time.Time
	ClientIP  string
	SNI       string
	BytesUp   int64
	BytesDown int64
	Status    string
	DialError string
}

type Server struct {
	snap    atomic.Pointer[snapshot.ProxySnap]
	reports chan Report
	log     *slog.Logger
	http    string
	https   string
	mu      sync.Mutex
	active  map[string]int
}

func New(log *slog.Logger, httpAddr, httpsAddr string) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		reports: make(chan Report, 1024),
		log:     log,
		http:    httpAddr,
		https:   httpsAddr,
		active:  map[string]int{},
	}
}

func (s *Server) SetSnapshot(p *snapshot.ProxySnap) {
	if p == nil {
		return
	}
	cp := *p
	s.snap.Store(&cp)
	metrics.ConfigVersion.Set(float64(p.Version))
}

func (s *Server) Snapshot() *snapshot.ProxySnap { return s.snap.Load() }

func (s *Server) Reports() <-chan Report { return s.reports }

func (s *Server) Listen(ctx context.Context) error {
	lnHTTP, err := net.Listen("tcp", s.http)
	if err != nil {
		return err
	}
	lnTLS, err := net.Listen("tcp", s.https)
	if err != nil {
		_ = lnHTTP.Close()
		return err
	}
	errCh := make(chan error, 2)
	go s.accept(ctx, lnHTTP, false, errCh)
	go s.accept(ctx, lnTLS, true, errCh)
	select {
	case <-ctx.Done():
		_ = lnHTTP.Close()
		_ = lnTLS.Close()
		return nil
	case err := <-errCh:
		_ = lnHTTP.Close()
		_ = lnTLS.Close()
		return err
	}
}

func (s *Server) accept(ctx context.Context, ln net.Listener, https bool, errCh chan error) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			errCh <- err
			return
		}
		go s.handle(conn, https)
	}
}

func (s *Server) handle(conn net.Conn, https bool) {
	defer conn.Close()
	clientIP := ""
	if ip := remoteIP(conn.RemoteAddr()); ip != nil {
		clientIP = ip.String()
	}
	snap := s.snap.Load()
	if snap == nil {
		s.emit(Report{At: time.Now().UTC(), ClientIP: clientIP, Status: "refused", DialError: "no snapshot"})
		return
	}
	limit := snap.SessionLimit
	if limit < 1 {
		limit = 1
	}
	if !s.acquire(clientIP, limit) {
		s.emit(Report{At: time.Now().UTC(), ClientIP: clientIP, Status: "limited"})
		return
	}
	defer s.release(clientIP)

	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var name string
	var prefix []byte
	var err error
	if https {
		name, prefix, err = ReadClientHello(conn)
	} else {
		name, prefix, err = ReadHTTPHost(conn)
	}
	_ = conn.SetDeadline(time.Time{})
	if err != nil || !nameAllowed(name, snap.Names) {
		sni := name
		msg := "not in snapshot"
		if err != nil {
			msg = err.Error()
			if sni == "" {
				sni = ""
			}
		}
		s.emit(Report{At: time.Now().UTC(), ClientIP: clientIP, SNI: clip(sni, 255), Status: "refused", DialError: clip(msg, 300)})
		return
	}

	port := "80"
	if https {
		port = "443"
	}
	ips, rerr := resolveOrigin(snapshot.Normalize(name), snap.Upstreams)
	own := ownIPs(snap)
	dialTimeout := time.Duration(snap.DialTimeoutMs) * time.Millisecond
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	idle := time.Duration(snap.IdleTimeoutMs) * time.Millisecond
	if idle <= 0 {
		idle = 120 * time.Second
	}
	var last error = rerr
	var up net.Conn
	for _, ip := range ips {
		if netx.IsBlocked(ip, own) {
			last = errors.New("blocked address")
			continue
		}
		d := net.Dialer{Timeout: dialTimeout}
		c, derr := d.Dial("tcp", net.JoinHostPort(ip.String(), port))
		if derr != nil {
			last = derr
			continue
		}
		up = c
		break
	}
	if up == nil {
		msg := "no address"
		if last != nil {
			msg = last.Error()
		}
		s.log.Warn("dial", "sni", name, "client", clientIP, "err", msg)
		s.emit(Report{At: time.Now().UTC(), ClientIP: clientIP, SNI: clip(name, 255), Status: "dial_error", DialError: clip(msg, 300)})
		return
	}
	defer up.Close()
	if _, err := up.Write(prefix); err != nil {
		s.emit(Report{At: time.Now().UTC(), ClientIP: clientIP, SNI: clip(name, 255), Status: "dial_error", DialError: clip(err.Error(), 300)})
		return
	}
	upN, downN := splice(conn, up, idle)
	metrics.ProxyBytes.WithLabelValues("up").Add(float64(upN))
	metrics.ProxyBytes.WithLabelValues("down").Add(float64(downN))
	s.emit(Report{At: time.Now().UTC(), ClientIP: clientIP, SNI: clip(name, 255), BytesUp: upN, BytesDown: downN, Status: "ok"})
}

func (s *Server) emit(r Report) {
	metrics.ProxySessions.WithLabelValues(r.Status).Inc()
	select {
	case s.reports <- r:
	default:
	}
}

func (s *Server) acquire(ip string, limit int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[ip] >= limit {
		return false
	}
	s.active[ip]++
	return true
}

func (s *Server) release(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[ip]--
	if s.active[ip] <= 0 {
		delete(s.active, ip)
	}
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
		if resp == nil {
			last = errors.New("empty")
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

type idleConn struct {
	net.Conn
	idle time.Duration
}

func (c idleConn) Read(p []byte) (int, error) {
	_ = c.Conn.SetReadDeadline(time.Now().Add(c.idle))
	return c.Conn.Read(p)
}

func (c idleConn) Write(p []byte) (int, error) {
	_ = c.Conn.SetWriteDeadline(time.Now().Add(c.idle))
	return c.Conn.Write(p)
}

func splice(client, origin net.Conn, idle time.Duration) (up, down int64) {
	var upN, downN atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(idleConn{Conn: origin, idle: idle}, idleConn{Conn: client, idle: idle})
		upN.Store(n)
		closeWrite(origin)
	}()
	n, _ := io.Copy(idleConn{Conn: client, idle: idle}, idleConn{Conn: origin, idle: idle})
	downN.Store(n)
	closeWrite(client)
	wg.Wait()
	return upN.Load(), downN.Load()
}

func closeWrite(c net.Conn) {
	type closer interface{ CloseWrite() error }
	if t, ok := c.(closer); ok {
		_ = t.CloseWrite()
	}
}

func remoteIP(a net.Addr) net.IP {
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return net.ParseIP(a.String())
	}
	return net.ParseIP(host)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
