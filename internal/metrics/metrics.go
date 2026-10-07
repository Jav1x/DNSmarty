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
		Help: "1, если узел отвечает по последнему снимку: панель недоступна.",
	})
	ConfigVersion = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "dnsmarty_config_version",
		Help: "Версия последнего полученного снимка.",
	})
	DNSQueries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dnsmarty_dns_queries_total",
		Help: "DNS-запросы по решению.",
	}, []string{"decision"})
	ProxySessions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dnsmarty_proxy_sessions_total",
		Help: "Сессии прокси по статусу.",
	}, []string{"status"})
	ProxyBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dnsmarty_proxy_bytes_total",
		Help: "Байты splice, без тел в журнале.",
	}, []string{"dir"})
)

func init() {
	prometheus.MustRegister(Stale, ConfigVersion, DNSQueries, ProxySessions, ProxyBytes)
}

func Serve(ctx context.Context, addr string, log *slog.Logger) {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
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
