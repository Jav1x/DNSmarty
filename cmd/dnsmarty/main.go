package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"dnsmarty/internal/agent"
	"dnsmarty/internal/buildinfo"
	"dnsmarty/internal/config"
	"dnsmarty/internal/dns"
	"dnsmarty/internal/metrics"
	"dnsmarty/internal/migrate"
	"dnsmarty/internal/panel"
	"dnsmarty/internal/proxy"
	"dnsmarty/internal/snapshot"
	"dnsmarty/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel()})))
	root := &cobra.Command{Use: "dnsmarty", SilenceUsage: true}
	root.AddCommand(migrateCmd(), panelCmd(), dnsCmd(), proxyCmd())
	if err := root.Execute(); err != nil {
		slog.Error("exit", "err", err)
		os.Exit(1)
	}
}

func migrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Применить SQL-миграции",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dsn, err := config.Must("DATABASE_URL")
			if err != nil {
				return err
			}
			return migrate.Up(dsn)
		},
	}
}

func panelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "panel",
		Short: "API и UI, единственный писатель в Postgres",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dsn, err := config.Must("DATABASE_URL")
			if err != nil {
				return err
			}
			secret, err := config.Must("SESSION_SECRET")
			if err != nil {
				return err
			}
			master, err := config.Must("PANEL_MASTER_KEY")
			if err != nil {
				return err
			}
			user, err := config.FromEnv("PANEL_ADMIN_USER")
			if err != nil {
				return err
			}
			if user == "" {
				user = "admin"
			}
			pass, err := config.FromEnv("PANEL_ADMIN_PASSWORD")
			if err != nil {
				return err
			}
			listen, _ := config.FromEnv("PANEL_LISTEN")
			if listen == "" {
				listen = ":8080"
			}
			metricsAddr, _ := config.FromEnv("METRICS_ADDR")
			if metricsAddr == "" {
				metricsAddr = ":9100"
			}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			st, err := store.Open(ctx, dsn, secret, master)
			if err != nil {
				return err
			}
			defer st.Close()
			if err := st.EnsureAdmin(ctx, user, pass); err != nil {
				return err
			}
			if err := st.MaintainPartitions(ctx); err != nil {
				return err
			}
			go partitionLoop(ctx, st)
			opts, err := panelOptions()
			if err != nil {
				return err
			}
			ui := panel.New(st, slog.Default(), opts)
			go ui.PushLoop(ctx)
			metrics.Serve(ctx, metricsAddr, slog.Default())
			srv := &http.Server{
				Addr:              listen,
				Handler:           ui.Handler(),
				ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout:       15 * time.Second,
				// Node checks run up to 15 s inside a request.
				WriteTimeout:   30 * time.Second,
				IdleTimeout:    120 * time.Second,
				MaxHeaderBytes: 64 << 10,
				ErrorLog:       slog.NewLogLogger(slog.Default().Handler(), slog.LevelDebug),
			}
			go func() {
				<-ctx.Done()
				shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(shut)
			}()
			slog.Info("panel", "addr", listen, "metrics", metricsAddr, "version", buildinfo.Version, "cookie_secure", opts.CookieSecure)
			err = srv.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		},
	}
}

func dnsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dns",
		Short: "Резолвер: 53, DoT и DoH через один Decide",
		RunE: func(cmd *cobra.Command, _ []string) error {
			nodeKey, err := nodeKey()
			if err != nil {
				return err
			}
			cert, err := config.Must("TLS_CERT_FILE")
			if err != nil {
				return err
			}
			keyFile, err := config.Must("TLS_KEY_FILE")
			if err != nil {
				return err
			}
			cfg := dns.ListenConfig{
				DNSAddr:  envDefault("DNS_ADDR", ":53"),
				DoTAddr:  envDefault("DOT_ADDR", ":853"),
				DoHAddr:  envDefault("DOH_ADDR", ":8443"),
				CertFile: cert,
				KeyFile:  keyFile,
			}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			// Metrics stay on loopback by default: DNS nodes run in the host network.
			metrics.Serve(ctx, envDefault("METRICS_ADDR", "127.0.0.1:9101"), slog.Default())
			eng := dns.NewEngine(slog.Default())
			eng.SetForwardLimit(envInt("DNS_FORWARD_MAX", 512))
			stats := &agent.Stats{}
			go agent.Collect(ctx, stats, eng.Hits(), nil)
			errCh := make(chan error, 2)
			go func() {
				errCh <- agent.Listen(ctx, envDefault("AGENT_ADDR", ":9443"), nodeKey, agent.Config{
					Role:  snapshot.RoleDNS,
					Apply: func(body []byte) error { return agent.ApplyDNS(body, eng) },
					Version: func() int64 {
						if s := eng.Snapshot(); s != nil {
							return s.Version
						}
						return 0
					},
					Stats: stats,
				})
			}()
			go func() { errCh <- dns.Listen(ctx, eng, cfg) }()
			slog.Info("dns", "dns", cfg.DNSAddr, "dot", cfg.DoTAddr, "doh", cfg.DoHAddr, "agent", envDefault("AGENT_ADDR", ":9443"))
			select {
			case <-ctx.Done():
				return nil
			case err := <-errCh:
				return err
			}
		},
	}
}

func proxyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "proxy",
		Short: "SNI splice без терминации TLS",
		RunE: func(cmd *cobra.Command, _ []string) error {
			nodeKey, err := nodeKey()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			metrics.Serve(ctx, envDefault("METRICS_ADDR", "127.0.0.1:9102"), slog.Default())
			srv := proxy.New(slog.Default(), envDefault("PROXY_HTTP_ADDR", ":80"), envDefault("PROXY_HTTPS_ADDR", ":443"))
			srv.SetMaxConns(envInt("PROXY_MAX_CONNS", 4096))
			stats := &agent.Stats{}
			go agent.Collect(ctx, stats, nil, srv.Reports())
			errCh := make(chan error, 2)
			go func() {
				errCh <- agent.Listen(ctx, envDefault("AGENT_ADDR", ":9444"), nodeKey, agent.Config{
					Role:  snapshot.RoleProxy,
					Apply: func(body []byte) error { return agent.ApplyProxy(body, srv) },
					Version: func() int64 {
						if s := srv.Snapshot(); s != nil {
							return s.Version
						}
						return 0
					},
					Stats: stats,
				})
			}()
			go func() { errCh <- srv.Listen(ctx) }()
			slog.Info("proxy", "http", envDefault("PROXY_HTTP_ADDR", ":80"), "https", envDefault("PROXY_HTTPS_ADDR", ":443"), "agent", envDefault("AGENT_ADDR", ":9444"))
			select {
			case <-ctx.Done():
				return nil
			case err := <-errCh:
				return err
			}
		},
	}
}

// panelOptions reads PANEL_COOKIE_SECURE (default true) and PANEL_TRUSTED_PROXIES
// (comma-separated CIDRs, default loopback and private networks).
func panelOptions() (panel.Options, error) {
	opts := panel.Options{CookieSecure: true, Version: buildinfo.Version}
	if v := envDefault("PANEL_COOKIE_SECURE", ""); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return opts, fmt.Errorf("PANEL_COOKIE_SECURE: %w", err)
		}
		opts.CookieSecure = b
	}
	if v := envDefault("PANEL_TRUSTED_PROXIES", ""); v != "" {
		p, err := panel.ParsePrefixes(v)
		if err != nil {
			return opts, err
		}
		opts.TrustedProxies = p
	}
	return opts, nil
}

func nodeKey() ([]byte, error) {
	raw, err := config.Must("NODE_KEY")
	if err != nil {
		return nil, err
	}
	return agent.ParseKey(raw)
}

func envDefault(key, def string) string {
	v, err := config.FromEnv(key)
	if err != nil || v == "" {
		return def
	}
	return v
}

// logLevel reads LOG_LEVEL: debug, info (default), warn, error.
func logLevel() slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
		return slog.LevelInfo
	}
	return l
}

func envInt(key string, def int) int {
	n, err := strconv.Atoi(envDefault(key, ""))
	if err != nil || n < 1 {
		return def
	}
	return n
}

func partitionLoop(ctx context.Context, st *store.Store) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := st.MaintainPartitions(ctx); err != nil {
				slog.Error("partitions", "err", err)
			}
		}
	}
}
