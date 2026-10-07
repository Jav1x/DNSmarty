package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"dnsmarty/internal/dns"
	"dnsmarty/internal/metrics"
	"dnsmarty/internal/proxy"
	"dnsmarty/internal/snapshot"
)

type ApplyFunc func(body []byte) error

type Stats struct {
	mu       sync.Mutex
	hits     []dns.Hit
	sessions []proxy.Report
}

func (s *Stats) AddHit(h dns.Hit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits = append(s.hits, h)
	if len(s.hits) > 2000 {
		s.hits = s.hits[len(s.hits)-2000:]
	}
}

func (s *Stats) AddSession(r proxy.Report) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = append(s.sessions, r)
	if len(s.sessions) > 2000 {
		s.sessions = s.sessions[len(s.sessions)-2000:]
	}
}

func (s *Stats) Drain() (hits []dns.Hit, sessions []proxy.Report) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hits, sessions = s.hits, s.sessions
	s.hits, s.sessions = nil, nil
	if hits == nil {
		hits = []dns.Hit{}
	}
	if sessions == nil {
		sessions = []proxy.Report{}
	}
	return hits, sessions
}

func Collect(ctx context.Context, stats *Stats, hits <-chan dns.Hit, sessions <-chan proxy.Report) {
	for {
		select {
		case <-ctx.Done():
			return
		case h, ok := <-hits:
			if ok {
				stats.AddHit(h)
			}
		case r, ok := <-sessions:
			if ok {
				stats.AddSession(r)
			}
		}
	}
}

type dnsConfig struct {
	snapshot.DNS
	NodeEnabled bool `json:"node_enabled"`
}

func Listen(ctx context.Context, addr string, nodeKey []byte, role string, apply ApplyFunc, stats *Stats, log *slog.Logger) error {
	tlsCfg, err := ServerTLS(nodeKey)
	if err != nil {
		return err
	}
	ln, err := tls.Listen("tcp", addr, tlsCfg)
	if err != nil {
		return err
	}
	return Serve(ctx, ln, role, apply, stats, log)
}

func Serve(ctx context.Context, ln net.Listener, role string, apply ApplyFunc, stats *Stats, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	metrics.Stale.Set(1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "role": role})
	})
	mux.HandleFunc("POST /config", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			http.Error(w, "body", http.StatusBadRequest)
			return
		}
		if err := apply(body); err != nil {
			log.Warn("config", "err", err)
			http.Error(w, "config", http.StatusBadRequest)
			return
		}
		metrics.Stale.Set(0)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		hits, sessions := stats.Drain()
		writeJSON(w, http.StatusOK, map[string]any{"hits": hits, "sessions": sessions})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
		_ = ln.Close()
	}()
	log.Info("agent", "addr", ln.Addr().String(), "role", role)
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func ApplyDNS(body []byte, eng *dns.Engine) error {
	var cfg dnsConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return err
	}
	eng.SetSnapshot(&cfg.DNS)
	eng.SetEnabled(cfg.NodeEnabled)
	return nil
}

func ApplyProxy(body []byte, srv *proxy.Server) error {
	var cfg snapshot.ProxySnap
	if err := json.Unmarshal(body, &cfg); err != nil {
		return err
	}
	srv.SetSnapshot(&cfg)
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
