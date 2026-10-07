package dns

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	mdns "github.com/miekg/dns"

	"dnsmarty/internal/metrics"
	"dnsmarty/internal/snapshot"
)

type Hit struct {
	At       time.Time
	ClientIP string
	QName    string
	QType    string
	Rcode    string
	Decision string
}

type Engine struct {
	snap    atomic.Pointer[snapshot.DNS]
	enabled atomic.Bool
	pick    Picker
	hits    chan Hit
	log     *slog.Logger
}

func NewEngine(log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	e := &Engine{hits: make(chan Hit, 1024), log: log}
	e.enabled.Store(true)
	return e
}

func (e *Engine) Snapshot() *snapshot.DNS { return e.snap.Load() }

func (e *Engine) SetSnapshot(s *snapshot.DNS) {
	if s == nil {
		return
	}
	cp := *s
	e.snap.Store(&cp)
	metrics.ConfigVersion.Set(float64(s.Version))
}

func (e *Engine) SetEnabled(v bool) { e.enabled.Store(v) }

func (e *Engine) Hits() <-chan Hit { return e.hits }

func (e *Engine) ServeDNS(w mdns.ResponseWriter, r *mdns.Msg) {
	resp := e.Resolve(remoteIP(w.RemoteAddr()), r)
	_ = w.WriteMsg(resp)
}

func (e *Engine) ServeDoH(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/dns-query" {
		http.NotFound(w, r)
		return
	}
	raw, err := readDoH(r)
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
	resp := e.Resolve(net.ParseIP(host), req)
	packed, err := resp.Pack()
	if err != nil {
		http.Error(w, "pack", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/dns-message")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(packed)
}

func readDoH(r *http.Request) ([]byte, error) {
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query().Get("dns")
		raw, err := base64.RawURLEncoding.DecodeString(q)
		if err != nil {
			raw, err = base64.URLEncoding.DecodeString(q)
		}
		return raw, err
	case http.MethodPost:
		if r.Header.Get("Content-Type") != "application/dns-message" {
			return nil, errors.New("content type")
		}
		return io.ReadAll(io.LimitReader(r.Body, 65535))
	default:
		return nil, errors.New("method")
	}
}

func (e *Engine) Resolve(client net.IP, req *mdns.Msg) *mdns.Msg {
	if req == nil || len(req.Question) != 1 {
		m := new(mdns.Msg)
		if req != nil {
			m.SetReply(req)
		}
		m.Rcode = mdns.RcodeFormatError
		return m
	}
	q := req.Question[0]
	if !e.enabled.Load() {
		resp := reply(req, mdns.RcodeRefused, nil, 0)
		e.note(client, q, resp, "disabled")
		return resp
	}
	snap := e.snap.Load()
	d := Decide(snap, client, q.Name, q.Qtype, &e.pick)
	var resp *mdns.Msg
	switch d.Action {
	case ActionRefuse:
		resp = reply(req, mdns.RcodeRefused, nil, 0)
	case ActionLocal:
		resp = reply(req, mdns.RcodeSuccess, d.IPs, d.TTL)
	case ActionForward:
		if snap == nil || len(snap.Upstreams) == 0 {
			resp = reply(req, mdns.RcodeServerFailure, nil, 0)
			d.Action = ActionFail
		} else {
			fwd, err := forward(req, snap.Upstreams)
			if err != nil {
				e.log.Warn("forward", "err", err, "qname", q.Name)
				resp = reply(req, mdns.RcodeServerFailure, nil, 0)
				d.Action = ActionFail
			} else {
				resp = fwd
			}
		}
	default:
		resp = reply(req, mdns.RcodeServerFailure, nil, 0)
		d.Action = ActionFail
	}
	e.note(client, q, resp, string(d.Action))
	return resp
}

func (e *Engine) note(client net.IP, q mdns.Question, resp *mdns.Msg, decision string) {
	rcode := "UNKNOWN"
	if resp != nil {
		if name, ok := mdns.RcodeToString[resp.Rcode]; ok {
			rcode = name
		}
	}
	metrics.DNSQueries.WithLabelValues(decision).Inc()
	ip := ""
	if client != nil {
		ip = client.String()
	}
	qtype := "TYPE"
	if name, ok := mdns.TypeToString[q.Qtype]; ok {
		qtype = name
	}
	h := Hit{
		At:       time.Now().UTC(),
		ClientIP: ip,
		QName:    q.Name,
		QType:    qtype,
		Rcode:    rcode,
		Decision: decision,
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
	if rcode != mdns.RcodeSuccess || len(req.Question) == 0 {
		return m
	}
	q := req.Question[0]
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil && (q.Qtype == mdns.TypeA || q.Qtype == mdns.TypeANY) {
			m.Answer = append(m.Answer, &mdns.A{
				Hdr: mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: ttl},
				A:   ip4,
			})
		} else if ip.To4() == nil && (q.Qtype == mdns.TypeAAAA || q.Qtype == mdns.TypeANY) {
			m.Answer = append(m.Answer, &mdns.AAAA{
				Hdr:  mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeAAAA, Class: mdns.ClassINET, Ttl: ttl},
				AAAA: ip,
			})
		}
	}
	return m
}

func forward(req *mdns.Msg, upstreams []string) (*mdns.Msg, error) {
	c := &mdns.Client{Net: "tcp", Timeout: 5 * time.Second}
	q := req.Copy()
	q.Id = mdns.Id()
	var last error
	for _, u := range upstreams {
		addr := u
		if _, _, err := net.SplitHostPort(addr); err != nil {
			addr = net.JoinHostPort(addr, "53")
		}
		r, _, err := c.Exchange(q, addr)
		if err != nil {
			last = err
			continue
		}
		if r == nil {
			last = errors.New("empty upstream")
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

func remoteIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.UDPAddr:
		return v.IP
	case *net.TCPAddr:
		return v.IP
	default:
		if a == nil {
			return nil
		}
		host, _, err := net.SplitHostPort(a.String())
		if err != nil {
			return net.ParseIP(a.String())
		}
		return net.ParseIP(host)
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
	tcp := &mdns.Server{Addr: cfg.DNSAddr, Net: "tcp", Handler: e}
	dot := &mdns.Server{Addr: cfg.DoTAddr, Net: "tcp-tls", Handler: e, TLSConfig: dotTLS}
	mux := http.NewServeMux()
	mux.HandleFunc("/dns-query", e.ServeDoH)
	ln, err := tls.Listen("tcp", cfg.DoHAddr, dohTLS)
	if err != nil {
		return err
	}
	doh := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 4)
	go func() { errCh <- udp.ListenAndServe() }()
	go func() { errCh <- tcp.ListenAndServe() }()
	go func() { errCh <- dot.ListenAndServe() }()
	go func() { errCh <- doh.Serve(ln) }()
	select {
	case <-ctx.Done():
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = udp.Shutdown()
		_ = tcp.Shutdown()
		_ = dot.Shutdown()
		_ = doh.Shutdown(shut)
		return nil
	case err := <-errCh:
		_ = udp.Shutdown()
		_ = tcp.Shutdown()
		_ = dot.Shutdown()
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = doh.Shutdown(shut)
		return err
	}
}
