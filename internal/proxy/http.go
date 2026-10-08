package proxy

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"sync/atomic"
	"time"

	"dnsmarty/internal/metrics"
	"dnsmarty/internal/snapshot"
)

// Port 80 is a reverse proxy, not a TCP splice: every request on a keep-alive connection
// carries its own Host, and each one is checked against the snapshot. A splice would check
// only the first request and let the rest reach any site behind the same origin IP.

type connKey struct{}

var discardLog = log.New(io.Discard, "", 0)

func (s *Server) httpServer() *http.Server {
	return &http.Server{
		Handler:           http.HandlerFunc(s.serveHTTP),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          discardLog,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connKey{}, c)
		},
	}
}

func (s *Server) newTransport() *http.Transport {
	return &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			c := s.snap.Load()
			if c == nil || !nameAllowed(host, c.snap.Names) {
				return nil, errors.New("not in snapshot")
			}
			return s.dialOrigin(ctx, c.snap, host, "80")
		},
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: time.Second,
		// Pass Accept-Encoding through untouched instead of decompressing on the client's behalf.
		DisableCompression: true,
	}
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	var client netip.Addr
	if gc, ok := r.Context().Value(connKey{}).(*gatedConn); ok {
		client = gc.client
	}
	ip := client.String()
	c := s.snap.Load()
	// The connection passed the lists when it was accepted; a newer snapshot may have changed them.
	if c == nil || !c.acl.Allowed(client) {
		s.refuseACL(client)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	host := snapshot.Normalize(r.Host)
	if !nameAllowed(host, c.snap.Names) {
		s.emit(Report{At: time.Now().UTC(), ClientIP: ip, SNI: clip(host, 255), Status: "refused", DialError: "not in snapshot"})
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	body := &countReader{r: r.Body}
	if r.Body != nil && r.Body != http.NoBody {
		r.Body = body
	}
	cw := &countWriter{ResponseWriter: w}
	failed := false
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Rewrite already drops X-Forwarded-*; the client address is not sent to the origin.
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = host
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Del("Forwarded")
		},
		Transport: s.transport,
		ErrorLog:  discardLog,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			failed = true
			s.log.Warn("dial", "host", host, "client", ip, "err", err)
			s.emit(Report{At: time.Now().UTC(), ClientIP: ip, SNI: clip(host, 255), Status: "dial_error", DialError: clip(err.Error(), 300)})
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(cw, r)
	if failed {
		return
	}
	up, down := body.n.Load(), cw.n
	metrics.ProxyBytes.WithLabelValues("up").Add(float64(up))
	metrics.ProxyBytes.WithLabelValues("down").Add(float64(down))
	s.emit(Report{At: time.Now().UTC(), ClientIP: ip, SNI: clip(host, 255), BytesUp: up, BytesDown: down, Status: "ok"})
}

type countReader struct {
	r io.ReadCloser
	n atomic.Int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

func (c *countReader) Close() error { return c.r.Close() }

type countWriter struct {
	http.ResponseWriter
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	c.n += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach Flush and Hijack (streaming, WebSocket upgrades).
func (c *countWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
