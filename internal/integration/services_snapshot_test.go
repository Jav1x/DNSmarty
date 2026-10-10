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
	"dnsmarty/internal/snapshot"
	"dnsmarty/internal/store"
)

func TestServicesSnapshot(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `UPDATE node SET last_seen_at = now() WHERE id = $1`, live.ID); err != nil {
		t.Fatal(err)
	}

	svc := func(name, strategy string, enabled bool, proxies []store.ProxyWeight, members ...store.MemberInput) {
		t.Helper()
		if _, err := st.CreateService(ctx, "admin", store.ServiceInput{
			Name: name, Strategy: strategy, Enabled: enabled, Proxies: proxies,
		}, members); err != nil {
			t.Fatal(err)
		}
	}
	on := func(name, match string) store.MemberInput {
		return store.MemberInput{Name: name, Match: match, Enabled: true}
	}
	off := func(name, match string) store.MemberInput {
		return store.MemberInput{Name: name, Match: match, Enabled: false}
	}
	svc("Alive", "weighted", true, []store.ProxyWeight{{ProxyID: live.ID, Weight: 3}},
		on("alive.test", "suffix"), off("member-off.test", "fqdn"))
	svc("Dead", "sticky24", true, []store.ProxyWeight{{ProxyID: dead.ID, Weight: 1}},
		on("dead.test", "fqdn"))
	svc("Off", "round_robin", false, []store.ProxyWeight{{ProxyID: live.ID, Weight: 1}},
		on("off.test", "suffix"))

	dns, err := st.DNSSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	domains := map[string]snapshot.Domain{}
	for _, d := range dns.Domains {
		domains[d.Name] = d
	}
	if _, ok := domains["off.test"]; ok {
		t.Fatal("member of disabled service is in DNS snapshot")
	}
	if _, ok := domains["member-off.test"]; ok {
		t.Fatal("disabled member is in DNS snapshot")
	}
	a, ok := domains["alive.test"]
	if !ok || a.Balance != snapshot.BalanceWeighted || a.Match != snapshot.MatchSuffix {
		t.Fatalf("alive.test = %+v", a)
	}
	if len(a.Proxies) != 1 || a.Proxies[0].ID != live.ID || a.Proxies[0].Weight != 3 {
		t.Fatalf("alive.test proxies = %+v", a.Proxies)
	}
	dd, ok := domains["dead.test"]
	if !ok || dd.Balance != snapshot.BalanceSticky || len(dd.Proxies) != 0 {
		t.Fatalf("dead.test = %+v (must stay with no proxies, not leak origin)", dd)
	}

	liveSnap, err := st.ProxySnapshot(ctx, live.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(liveSnap.Names) != 1 || liveSnap.Names[0].Name != "alive.test" {
		t.Fatalf("live node names = %+v", liveSnap.Names)
	}
	deadSnap, err := st.ProxySnapshot(ctx, dead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deadSnap.Names) != 1 || deadSnap.Names[0].Name != "dead.test" {
		t.Fatalf("dead node names = %+v", deadSnap.Names)
	}
}
