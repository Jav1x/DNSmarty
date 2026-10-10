package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	Stale = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "dnsmarty_config_stale",
		Help: "1 if the node is serving the last snapshot because the panel is unreachable.",
	})
	ConfigVersion = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "dnsmarty_config_version",
		Help: "Version of the last received snapshot.",
	})
	DNSQueries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dnsmarty_dns_queries_total",
		Help: "DNS queries by decision.",
	}, []string{"decision"})
	ProxySessions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dnsmarty_proxy_sessions_total",
		Help: "Proxy sessions by status.",
	}, []string{"status"})
	ProxyBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dnsmarty_proxy_bytes_total",
		Help: "Splice bytes; bodies are not logged.",
	}, []string{"dir"})
)

func init() {
	prometheus.MustRegister(Stale, ConfigVersion, DNSQueries, ProxySessions, ProxyBytes)
}

func Serve(ctx context.Context, addr string, log *slog.Logger) {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	// Liveness for container healthchecks; distroless has no curl, the binary probes this.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	go func() {
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics", "err", err, "addr", addr)
		}
	}()
}
