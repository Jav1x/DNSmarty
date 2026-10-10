package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"dnsmarty/internal/migrate"
	"dnsmarty/internal/store"
)

func TestOverviewBay(t *testing.T) {
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("dnsmarty"),
		postgres.WithUsername("dnsmarty"),
		postgres.WithPassword("dnsmarty"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(dsn); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, dsn, "integration-session-secret", "integration-master-key")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	_, live, err := st.CreateNode(ctx, "test", store.NodeInput{
		Name: "live", Role: "proxy", IPv4: "203.0.113.10", Region: "lab",
		AgentHost: "127.0.0.1", AgentPort: 9444, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, dead, err := st.CreateNode(ctx, "test", store.NodeInput{
		Name: "dead", Role: "proxy", IPv4: "203.0.113.11", Region: "lab",
		AgentHost: "127.0.0.1", AgentPort: 9445, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateNode(ctx, "test", store.NodeInput{
		Name: "spare", Role: "proxy", IPv4: "203.0.113.12", Region: "lab",
		AgentHost: "127.0.0.1", AgentPort: 9446, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE node SET last_seen_at = now() WHERE id = $1`, live.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := st.CreateService(ctx, "admin", store.ServiceInput{
		Name: "Alpha", Strategy: "weighted", Enabled: true,
		Proxies: []store.ProxyWeight{{ProxyID: live.ID, Weight: 1}, {ProxyID: dead.ID, Weight: 1}},
	}, []store.MemberInput{{Name: "a.test", Match: "suffix", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateService(ctx, "admin", store.ServiceInput{
		Name: "Beta", Strategy: "round_robin", Enabled: false,
		Proxies: []store.ProxyWeight{{ProxyID: live.ID, Weight: 1}},
	}, []store.MemberInput{{Name: "b.test", Match: "fqdn", Enabled: true}}); err != nil {
		t.Fatal(err)
	}

	o, err := st.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	idx := map[string]int{}
	for i, p := range o.BayProxies {
		idx[p.Name] = i
	}
	rows := map[string]store.BayRow{}
	for _, r := range o.Rows {
		rows[r.Name] = r
	}
	if len(o.Rows) != 3 {
		t.Fatalf("rows = %d, want 3 (seeded example.com, Alpha, Beta)", len(o.Rows))
	}
	alpha, ok := rows["Alpha"]
	if !ok || alpha.Balance != "weighted" || !alpha.Enabled {
		t.Fatalf("Alpha row = %+v", alpha)
	}
	if got := alpha.Cells[idx["live"]].State; got != "live" {
		t.Fatalf("Alpha live cell = %s", got)
	}
	if got := alpha.Cells[idx["dead"]].State; got != "dead" {
		t.Fatalf("Alpha dead cell = %s", got)
	}
	if got := alpha.Cells[idx["spare"]].State; got != "empty" {
		t.Fatalf("Alpha spare cell = %s", got)
	}
	beta, ok := rows["Beta"]
	if !ok || beta.Enabled {
		t.Fatalf("Beta row = %+v", beta)
	}
	if got := beta.Cells[idx["live"]].State; got != "live" {
		t.Fatalf("Beta live cell = %s", got)
	}
	if got := beta.Cells[idx["dead"]].State; got != "empty" {
		t.Fatalf("Beta dead cell = %s", got)
	}
}
