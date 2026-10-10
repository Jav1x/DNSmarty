package dns

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	mdns "github.com/miekg/dns"

	"dnsmarty/internal/metrics"
	"dnsmarty/internal/ratelimit"
	"dnsmarty/internal/snapshot"
)

const (
	// maxUDPSize follows the DNS flag day 2020 recommendation: no fragmentation on common paths.
	maxUDPSize = 1232
	// defaultRateQPS applies until the first snapshot sets the panel value.
	defaultRateQPS    = 50
	defaultForwardMax = 512
)

type Hit struct {
	At        time.Time
	ClientIP  string
	QName     string
	QType     string
	Rcode     string
	Decision  string
	LatencyMS *int
}

type Engine struct {
	snap    atomic.Pointer[Compiled]
	enabled atomic.Bool
	pick    Picker
	hits    chan Hit
	log     *slog.Logger
	limit   *ratelimit.Limiter
	// forwards bounds upstream queries in flight; a flood of unknown names cannot open unbounded sockets.
	forwards chan struct{}
	cache    *cache
	dotRoots *x509.CertPool
	doh      *http.Client
}

func NewEngine(log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	e := &Engine{
		hits:     make(chan Hit, 1024),
		log:      log,
		limit:    ratelimit.New(defaultRateQPS),
		forwards: make(chan struct{}, defaultForwardMax),
		cache:    newCache(),
		doh:      &http.Client{Timeout: 3 * time.Second},
	}
	e.enabled.Store(true)
	return e
}

// SetForwardLimit changes the number of concurrent upstream queries. Call it before serving.
func (e *Engine) SetForwardLimit(n int) {
	if n < 1 {
		n = 1
	}
	e.forwards = make(chan struct{}, n)
}

func (e *Engine) Snapshot() *snapshot.DNS {
	if c := e.snap.Load(); c != nil {
		return c.Snap
	}
	return nil
}

// SetSnapshot compiles and installs s. A snapshot that does not compile is rejected
// and the previous one stays, instead of failing every query.
func (e *Engine) SetSnapshot(s *snapshot.DNS) error {
	if s == nil {
		return errors.New("empty snapshot")
	}
	cp := *s
	c, err := Compile(&cp)
	if err != nil {
		return err
	}
	e.snap.Store(c)
	e.limit.SetQPS(cp.RateQPS)
	metrics.ConfigVersion.Set(float64(cp.Version))
	return nil
}

func (e *Engine) SetEnabled(v bool) { e.enabled.Store(v) }

func (e *Engine) Hits() <-chan Hit { return e.hits }

func (e *Engine) ServeDNS(w mdns.ResponseWriter, r *mdns.Msg) {
	client := addrOf(w.RemoteAddr())
	_, udp := w.RemoteAddr().(*net.UDPAddr)
	if udp {
		// Only UDP sources can be spoofed, so only UDP feeds the reflection limit.
		switch e.limit.Check(client) {
		case ratelimit.Drop:
			metrics.DNSQueries.WithLabelValues("limited").Inc()
			return
		case ratelimit.Slip:
			metrics.DNSQueries.WithLabelValues("limited").Inc()
			m := new(mdns.Msg)
			m.SetReply(r)
			m.Truncated = true
			_ = w.WriteMsg(m)
			return
		}
	}
	resp := e.Resolve(client, r)
	if udp {
		resp.Truncate(udpSize(r))
	}
	_ = w.WriteMsg(resp)
}

// udpSize is what the client can take over UDP: 512 without EDNS, its buffer capped at maxUDPSize with it.
func udpSize(r *mdns.Msg) int {
	opt := r.IsEdns0()
	if opt == nil {
		return mdns.MinMsgSize
	}
	size := int(opt.UDPSize())
	if size < mdns.MinMsgSize {
		size = mdns.MinMsgSize
	}
	if size > maxUDPSize {
		size = maxUDPSize
	}
	return size
}

func (e *Engine) ServeDoH(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/dns-query" {
		http.NotFound(w, r)
		return
	}
	raw, err := readDoH(w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	req := new(mdns.Msg)
	if err := req.Unpack(raw); err != nil {
		http.Error(w, "bad dns", http.StatusBadRequest)
		return
	}
	host, _, splitErr := net.SplitHostPort(r.RemoteAddr)
	if splitErr != nil {
		host = r.RemoteAddr
	}
	client, _ := netip.ParseAddr(host)
	resp := e.Resolve(client, req)
	packed, err := resp.Pack()
	if err != nil {
		http.Error(w, "pack", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/dns-message")
	if ttl, ok := minTTL(resp); ok {
		w.Header().Set("Cache-Control", "max-age="+strconv.FormatUint(uint64(ttl), 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(packed)
}

func minTTL(m *mdns.Msg) (uint32, bool) {
	if m.Rcode != mdns.RcodeSuccess || len(m.Answer) == 0 {
		return 0, false
	}
	ttl := m.Answer[0].Header().Ttl
	for _, rr := range m.Answer[1:] {
		if t := rr.Header().Ttl; t < ttl {
			ttl = t
		}
	}
	return ttl, true
}

func readDoH(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query().Get("dns")
		if len(q) > 4096 {
			return nil, errors.New("too long")
		}
		raw, err := base64.RawURLEncoding.DecodeString(q)
		if err != nil {
			raw, err = base64.URLEncoding.DecodeString(q)
		}
		return raw, err
	case http.MethodPost:
		if r.Header.Get("Content-Type") != "application/dns-message" {
			return nil, errors.New("content type")
		}
		return io.ReadAll(http.MaxBytesReader(w, r.Body, 65535))
	default:
		return nil, errors.New("method")
	}
}

func (e *Engine) Resolve(client netip.Addr, req *mdns.Msg) *mdns.Msg {
	start := time.Now()
	if req == nil || len(req.Question) != 1 {
		m := new(mdns.Msg)
		if req != nil {
			m.SetReply(req)
		}
		m.Rcode = mdns.RcodeFormatError
		return m
	}
	q := req.Question[0]
	if req.Opcode != mdns.OpcodeQuery {
		resp := reply(req, mdns.RcodeNotImplemented, nil, 0)
		e.note(client, q, resp, "notimp", time.Since(start))
		return resp
	}
	if q.Qclass != mdns.ClassINET {
		resp := reply(req, mdns.RcodeRefused, nil, 0)
		e.note(client, q, resp, "class", time.Since(start))
		return resp
	}
	if !e.enabled.Load() {
		resp := reply(req, mdns.RcodeRefused, nil, 0)
		e.note(client, q, resp, "disabled", time.Since(start))
		return resp
	}
	c := e.snap.Load()
	d := c.Decide(client, q.Name, q.Qtype, &e.pick)
	if q.Qtype == mdns.TypeANY && d.Action != ActionRefuse && d.Action != ActionFail {
		// RFC 8482: ANY is the classic amplification query; answer with one small record.
		resp := hinfoReply(req, d.TTL)
		e.note(client, q, resp, "any", time.Since(start))
		return resp
	}
	var resp *mdns.Msg
	switch d.Action {
	case ActionRefuse:
		resp = reply(req, mdns.RcodeRefused, nil, 0)
	case ActionLocal:
		resp = reply(req, mdns.RcodeSuccess, d.IPs, d.TTL)
	case ActionForward:
		if len(c.Snap.Upstreams) == 0 {
			resp = reply(req, mdns.RcodeServerFailure, nil, 0)
			d.Action = ActionFail
		} else {
			key := cacheKey(q.Name, q.Qtype)
			now := time.Now()
			if hit := e.cache.get(key, req, now); hit != nil {
				resp = hit
				d.Action = ActionCached
			} else if fwd, err := e.cache.flightDo(key, func() (*mdns.Msg, error) {
				msg, err := e.forward(req, c.Snap.Upstreams)
				if err != nil {
					return nil, err
				}
				e.cache.put(key, msg, now)
				return msg, nil
			}); err != nil {
				e.log.Warn("forward", "err", err, "qname", q.Name)
				resp = reply(req, mdns.RcodeServerFailure, nil, 0)
				d.Action = ActionFail
			} else {
				fwd.Id = req.Id
				resp = fwd
			}
		}
	default:
		resp = reply(req, mdns.RcodeServerFailure, nil, 0)
		d.Action = ActionFail
	}
	e.note(client, q, resp, string(d.Action), time.Since(start))
	return resp
}

func (e *Engine) note(client netip.Addr, q mdns.Question, resp *mdns.Msg, decision string, took time.Duration) {
	rcode := "UNKNOWN"
	if resp != nil {
		if name, ok := mdns.RcodeToString[resp.Rcode]; ok {
			rcode = name
		}
	}
	metrics.DNSQueries.WithLabelValues(decision).Inc()
	ip := ""
	if client.IsValid() {
		ip = client.Unmap().String()
	}
	qtype := "TYPE"
	if name, ok := mdns.TypeToString[q.Qtype]; ok {
		qtype = name
	}
	ms := int(took.Milliseconds())
	h := Hit{
		At:        time.Now().UTC(),
		ClientIP:  ip,
		QName:     q.Name,
		QType:     qtype,
		Rcode:     rcode,
		Decision:  decision,
		LatencyMS: &ms,
	}
	select {
	case e.hits <- h:
	default:
	}
}

func reply(req *mdns.Msg, rcode int, ips []net.IP, ttl uint32) *mdns.Msg {
	m := new(mdns.Msg)
	m.SetReply(req)
	m.Authoritative = true
	m.RecursionAvailable = true
	m.Rcode = rcode
	if req.IsEdns0() != nil {
		m.SetEdns0(maxUDPSize, false)
	}
	if rcode != mdns.RcodeSuccess || len(req.Question) == 0 {
		return m
	}
	q := req.Question[0]
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil && q.Qtype == mdns.TypeA {
			m.Answer = append(m.Answer, &mdns.A{
				Hdr: mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: ttl},
				A:   ip4,
			})
		} else if ip.To4() == nil && q.Qtype == mdns.TypeAAAA {
			m.Answer = append(m.Answer, &mdns.AAAA{
				Hdr:  mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeAAAA, Class: mdns.ClassINET, Ttl: ttl},
				AAAA: ip,
			})
		}
	}
	return m
}

func hinfoReply(req *mdns.Msg, ttl uint32) *mdns.Msg {
	m := reply(req, mdns.RcodeSuccess, nil, 0)
	q := req.Question[0]
	m.Answer = []mdns.RR{&mdns.HINFO{
		Hdr: mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeHINFO, Class: mdns.ClassINET, Ttl: ttl},
		Cpu: "RFC8482",
	}}
	return m
}

var errBusy = errors.New("too many upstream requests")

func (e *Engine) forward(req *mdns.Msg, upstreams []string) (*mdns.Msg, error) {
	select {
	case e.forwards <- struct{}{}:
		defer func() { <-e.forwards }()
	default:
		metrics.DNSQueries.WithLabelValues("busy").Inc()
		return nil, errBusy
	}
	q := req.Copy()
	q.Id = mdns.Id()
	var last error
	for _, u := range upstreams {
		r, err := e.exchange(q, u)
		if err != nil {
			last = err
			continue
		}
		if r == nil || !sameQuestion(q, r) {
			last = errors.New("upstream answered a different question")
			continue
		}
		r.Id = req.Id
		return r, nil
	}
	if last == nil {
		last = errors.New("no upstream")
	}
	return nil, last
}

func UpstreamProto(addr string) string {
	if strings.HasPrefix(addr, "https://") {
		return "doh"
	}
	if strings.HasPrefix(addr, "tls://") {
		return "dot"
	}
	if _, port, err := net.SplitHostPort(addr); err == nil && port == "853" {
		return "dot"
	}
	return "udp"
}

func (e *Engine) exchange(q *mdns.Msg, upstream string) (*mdns.Msg, error) {
	switch UpstreamProto(upstream) {
	case "doh":
		return e.exchangeDoH(q, upstream)
	case "dot":
		return e.exchangeDoT(q, upstream)
	}
	addr := upstream
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "53")
	}
	udp := &mdns.Client{Net: "udp", Timeout: 2 * time.Second, UDPSize: maxUDPSize}
	r, _, err := udp.Exchange(q, addr)
	if err == nil && r != nil && r.Truncated {
		tcp := &mdns.Client{Net: "tcp", Timeout: 3 * time.Second}
		r, _, err = tcp.Exchange(q, addr)
	}
	return r, err
}

func (e *Engine) exchangeDoT(q *mdns.Msg, upstream string) (*mdns.Msg, error) {
	addr := strings.TrimPrefix(upstream, "tls://")
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	c := &mdns.Client{
		Net:       "tcp-tls",
		Timeout:   3 * time.Second,
		TLSConfig: &tls.Config{ServerName: host, RootCAs: e.dotRoots, MinVersion: tls.VersionTLS12},
	}
	r, _, err := c.Exchange(q, addr)
	return r, err
}

func (e *Engine) exchangeDoH(q *mdns.Msg, endpoint string) (*mdns.Msg, error) {
	wire, err := q.Pack()
	if err != nil {
		return nil, err
	}
	// Client Timeout bounds the call; the UDP/DoT path has no request context to thread.
	resp, err := e.doh.Post(endpoint, "application/dns-message", bytes.NewReader(wire)) //nolint:noctx
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("doh: HTTP " + strconv.Itoa(resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 65535))
	if err != nil {
		return nil, err
	}
	m := new(mdns.Msg)
	if err := m.Unpack(body); err != nil {
		return nil, err
	}
	return m, nil
}

func sameQuestion(q, r *mdns.Msg) bool {
	if len(r.Question) != 1 {
		return false
	}
	a, b := q.Question[0], r.Question[0]
	return a.Qtype == b.Qtype && a.Qclass == b.Qclass && strings.EqualFold(a.Name, b.Name)
}

func addrOf(a net.Addr) netip.Addr {
	switch v := a.(type) {
	case *net.UDPAddr:
		ip, _ := netip.AddrFromSlice(v.IP)
		return ip.Unmap()
	case *net.TCPAddr:
		ip, _ := netip.AddrFromSlice(v.IP)
		return ip.Unmap()
	case nil:
		return netip.Addr{}
	default:
		ap, err := netip.ParseAddrPort(a.String())
		if err != nil {
			ip, _ := netip.ParseAddr(a.String())
			return ip.Unmap()
		}
		return ap.Addr().Unmap()
	}
}

type ListenConfig struct {
	DNSAddr  string
	DoTAddr  string
	DoHAddr  string
	CertFile string
	KeyFile  string
}

func Listen(ctx context.Context, e *Engine, cfg ListenConfig) error {
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return err
	}
	dotTLS := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"dot"},
	}
	dohTLS := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2", "http/1.1"},
	}
	udp := &mdns.Server{Addr: cfg.DNSAddr, Net: "udp", Handler: e}
	tcp := &mdns.Server{Addr: cfg.DNSAddr, Net: "tcp", Handler: e, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: idle}
	dot := &mdns.Server{Addr: cfg.DoTAddr, Net: "tcp-tls", Handler: e, TLSConfig: dotTLS, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: idle}
	mux := http.NewServeMux()
	mux.HandleFunc("/dns-query", e.ServeDoH)
	ln, err := tls.Listen("tcp", cfg.DoHAddr, dohTLS)
	if err != nil {
		return err
	}
	doh := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          slog.NewLogLogger(e.log.Handler(), slog.LevelDebug),
	}
	errCh := make(chan error, 4)
	go func() { errCh <- udp.ListenAndServe() }()
	go func() { errCh <- tcp.ListenAndServe() }()
	go func() { errCh <- dot.ListenAndServe() }()
	go func() { errCh <- doh.Serve(ln) }()
	var result error
	select {
	case <-ctx.Done():
	case result = <-errCh:
	}
	shut, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_ = udp.Shutdown()
	_ = tcp.Shutdown()
	_ = dot.Shutdown()
	_ = doh.Shutdown(shut)
	return result
}

// idle closes TCP and DoT connections that send nothing for 10 s (RFC 7766 suggests seconds, not minutes).
func idle() time.Duration { return 10 * time.Second }
