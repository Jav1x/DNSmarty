package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"dnsmarty/internal/agent"
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
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
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
			ui := panel.New(st, slog.Default())
			go ui.PushLoop(ctx)
			metrics.Serve(ctx, metricsAddr, slog.Default())
			srv := &http.Server{Addr: listen, Handler: ui.Handler(), ReadHeaderTimeout: 5 * time.Second}
			go func() {
				<-ctx.Done()
				shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(shut)
			}()
			slog.Info("panel", "addr", listen, "metrics", metricsAddr)
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
			metrics.Serve(ctx, envDefault("METRICS_ADDR", ":9101"), slog.Default())
			eng := dns.NewEngine(slog.Default())
			stats := &agent.Stats{}
			go agent.Collect(ctx, stats, eng.Hits(), nil)
			errCh := make(chan error, 2)
			go func() {
				errCh <- agent.Listen(ctx, envDefault("AGENT_ADDR", ":9443"), nodeKey, snapshot.RoleDNS, func(body []byte) error {
					return agent.ApplyDNS(body, eng)
				}, stats, slog.Default())
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
			metrics.Serve(ctx, envDefault("METRICS_ADDR", ":9102"), slog.Default())
			srv := proxy.New(slog.Default(), envDefault("PROXY_HTTP_ADDR", ":80"), envDefault("PROXY_HTTPS_ADDR", ":443"))
			stats := &agent.Stats{}
			go agent.Collect(ctx, stats, nil, srv.Reports())
			errCh := make(chan error, 2)
			go func() {
				errCh <- agent.Listen(ctx, envDefault("AGENT_ADDR", ":9444"), nodeKey, snapshot.RoleProxy, func(body []byte) error {
					return agent.ApplyProxy(body, srv)
				}, stats, slog.Default())
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
