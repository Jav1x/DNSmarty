package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"dnsmarty/internal/migrate"
	"dnsmarty/internal/store"
)

func TestServicesStore(t *testing.T) {
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

	_, node, err := st.CreateNode(ctx, "test", store.NodeInput{
		Name: "edge", Role: "proxy", IPv4: "203.0.113.10", Region: "lab",
		AgentHost: "127.0.0.1", AgentPort: 9444, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	in := store.ServiceInput{
		Name: "Example", Strategy: "round_robin", Enabled: true,
		Proxies: []store.ProxyWeight{{ProxyID: node.ID, Weight: 3}},
	}
	members := []store.MemberInput{
		{Name: "Demo.test.", Match: "suffix", Enabled: true},
		{Name: "ex.test", Match: "fqdn", Enabled: true},
	}

	if _, err := st.CreateService(ctx, "admin", in, nil); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("service without members: %v", err)
	}
	bad := in
	bad.Strategy = "bogus"
	if _, err := st.CreateService(ctx, "admin", bad, members); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("bad strategy: %v", err)
	}
	bad = in
	bad.Proxies = []store.ProxyWeight{{ProxyID: node.ID, Weight: 0}}
	if _, err := st.CreateService(ctx, "admin", bad, members); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("zero weight: %v", err)
	}

	svcID, err := st.CreateService(ctx, "admin", in, members)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateService(ctx, "admin", in, members); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate service name: %v", err)
	}
	if err := st.AddMembers(ctx, "admin", "00000000-0000-0000-0000-000000000000", members); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("add to missing service: %v", err)
	}

	now := time.Now().UTC()
	for _, q := range []string{"www.demo.test.", "www.demo.test.", "demo.test.", "deep.sub.demo.test.", "ex.test.", "other.test.", "other.test.", "other.test.", "other.test.", "other.test."} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO dns_hit (day, at, node_id, client_ip, qname, qtype, rcode, decision)
			VALUES ($1, $2, $3, '192.0.2.7', $4, 'A', 'NOERROR', 'forward')
		`, now.Format("2006-01-02"), now, node.ID, q); err != nil {
			t.Fatal(err)
		}
	}
	for _, sni := range []string{"api.demo.test", "ex.test"} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO proxy_session (day, at, node_id, client_ip, sni, bytes_up, bytes_down, status)
			VALUES ($1, $2, $3, '192.0.2.7', $4, 100, 200, 'ok')
		`, now.Format("2006-01-02"), now, node.ID, sni); err != nil {
			t.Fatal(err)
		}
	}

	sv, ok := findService(t, ctx, st, "Example")
	if !ok {
		t.Fatal("service Example not listed")
	}
	if sv.ID != svcID || sv.Strategy != "round_robin" {
		t.Fatalf("service = %+v", sv)
	}
	if len(sv.Proxies) != 1 || !sv.Proxies[0].On || sv.Proxies[0].Weight != 3 {
		t.Fatalf("proxies = %+v", sv.Proxies)
	}
	byName := map[string]store.Member{}
	for _, m := range sv.Members {
		byName[m.Name] = m
	}
	if m := byName["demo.test"]; m.Match != "suffix" || m.Queries24 != 4 || m.Sessions24 != 1 || m.Bytes24 != 300 {
		t.Fatalf("demo.test = %+v", m)
	}
	if m := byName["ex.test"]; m.Match != "fqdn" || m.Queries24 != 1 || m.Sessions24 != 1 || m.Bytes24 != 300 {
		t.Fatalf("ex.test = %+v", m)
	}
	if sv.Queries24 != 5 || sv.Sessions24 != 2 || sv.Bytes24 != 600 {
		t.Fatalf("service totals = %d/%d/%d", sv.Queries24, sv.Sessions24, sv.Bytes24)
	}

	if err := st.DeleteService(ctx, "admin", svcID); err != nil {
		t.Fatal(err)
	}
	if _, ok := findService(t, ctx, st, "Example"); ok {
		t.Fatal("service Example still listed after delete")
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM domain WHERE name IN ('demo.test', 'ex.test')`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("members after service delete = %d", left)
	}
}

func findService(t *testing.T, ctx context.Context, st *store.Store, name string) (store.Service, bool) {
	t.Helper()
	list, err := st.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, sv := range list {
		if sv.Name == name {
			return sv, true
		}
	}
	return store.Service{}, false
}
