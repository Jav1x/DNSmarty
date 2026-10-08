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
	"sync/atomic"
	"time"

	"dnsmarty/internal/buildinfo"
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

// Config is what the management listener needs from a role.
type Config struct {
	Role string
	// Apply installs a snapshot. It returns *StaleError for an older snapshot of the same epoch.
	Apply ApplyFunc
	// Version reports the snapshot version in use, 0 before the first one.
	Version func() int64
	Stats   *Stats
	Log     *slog.Logger
}

func Listen(ctx context.Context, addr string, nodeKey []byte, cfg Config) error {
	tlsCfg, err := ServerTLS(nodeKey)
	if err != nil {
		return err
	}
	ln, err := tls.Listen("tcp", addr, tlsCfg)
	if err != nil {
		return err
	}
	return Serve(ctx, ln, cfg)
}

func Serve(ctx context.Context, ln net.Listener, cfg Config) error {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	stats := cfg.Stats
	if stats == nil {
		stats = &Stats{}
	}
	version := cfg.Version
	if version == nil {
		version = func() int64 { return 0 }
	}
	metrics.Stale.Set(1)
	// lastApply drives the stale gauge: 1 when no snapshot arrived for staleAfter.
	var lastApply atomic.Int64
	go watchStale(ctx, &lastApply)
	// Pushes from a check and from the loop may overlap; the order check needs them serialized.
	var applyMu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, Health{OK: true, Role: cfg.Role, Version: buildinfo.Version, Snapshot: version()})
	})
	mux.HandleFunc("POST /config", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<20))
		if err != nil {
			http.Error(w, "body", http.StatusRequestEntityTooLarge)
			return
		}
		applyMu.Lock()
		err = cfg.Apply(body)
		applyMu.Unlock()
		if have, ok := IsStale(err); ok {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "stale", "version": have})
			return
		}
		if err != nil {
			log.Warn("config", "err", err)
			http.Error(w, "config", http.StatusBadRequest)
			return
		}
		lastApply.Store(time.Now().UnixNano())
		metrics.Stale.Set(0)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		hits, sessions := stats.Drain()
		writeJSON(w, http.StatusOK, map[string]any{"hits": hits, "sessions": sessions})
	})
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
		_ = ln.Close()
	}()
	log.Info("agent", "addr", ln.Addr().String(), "role", cfg.Role)
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// staleAfter is three times the longest push interval the panel allows (15 s).
const staleAfter = 45 * time.Second

func watchStale(ctx context.Context, last *atomic.Int64) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if l := last.Load(); l == 0 || now.Sub(time.Unix(0, l)) > staleAfter {
				metrics.Stale.Set(1)
			}
		}
	}
}

// older reports a snapshot that must not replace the current one.
// An equal version is applied again: node_enabled changes without a version bump.
func older(curEpoch string, curVersion int64, epoch string, version int64) bool {
	return curEpoch == epoch && version < curVersion
}

func ApplyDNS(body []byte, eng *dns.Engine) error {
	var cfg dnsConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return err
	}
	if cur := eng.Snapshot(); cur != nil && older(cur.Epoch, cur.Version, cfg.Epoch, cfg.Version) {
		return &StaleError{Have: cur.Version}
	}
	if err := eng.SetSnapshot(&cfg.DNS); err != nil {
		return err
	}
	eng.SetEnabled(cfg.NodeEnabled)
	return nil
}

func ApplyProxy(body []byte, srv *proxy.Server) error {
	var cfg snapshot.ProxySnap
	if err := json.Unmarshal(body, &cfg); err != nil {
		return err
	}
	if cur := srv.Snapshot(); cur != nil && older(cur.Epoch, cur.Version, cfg.Epoch, cfg.Version) {
		return &StaleError{Have: cur.Version}
	}
	return srv.SetSnapshot(&cfg)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
