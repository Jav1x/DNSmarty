package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"dnsmarty/internal/migrate"
	"dnsmarty/internal/panel"
	"dnsmarty/internal/snapshot"
	"dnsmarty/internal/store"
)

func TestNodeOrdinalAndStatsTotals(t *testing.T) {
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

	mk := func(name, role, ip string, port int) store.Node {
		t.Helper()
		_, n, err := st.CreateNode(ctx, "test", store.NodeInput{
			Name: name, Role: role, IPv4: ip, Region: "lab",
			AgentHost: "127.0.0.1", AgentPort: port, Enabled: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	a := mk("alpha", "proxy", "203.0.113.10", 9444)
	b := mk("bravo", "proxy", "203.0.113.11", 9445)
	c := mk("charlie", "dns", "203.0.113.12", 9446)

	listed, err := st.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 || listed[0].ID != a.ID || listed[1].ID != b.ID || listed[2].ID != c.ID {
		t.Fatalf("create order = %+v", listed)
	}
	if listed[0].Ordinal != 1 || listed[1].Ordinal != 2 || listed[2].Ordinal != 3 {
		t.Fatalf("ordinals = %+v", listed)
	}

	if err := st.ReorderNodes(ctx, "admin", []string{c.ID, a.ID}); err == nil {
		t.Fatal("incomplete reorder must fail")
	}
	if err := st.ReorderNodes(ctx, "admin", []string{c.ID, b.ID, a.ID}); err != nil {
		t.Fatal(err)
	}
	listed, err = st.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if listed[0].ID != c.ID || listed[1].ID != b.ID || listed[2].ID != a.ID {
		t.Fatalf("after reorder = %+v", listed)
	}

	if _, err := pool.Exec(ctx, `UPDATE node SET last_seen_at = now() WHERE role = 'proxy'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateService(ctx, "admin", store.ServiceInput{
		Name: "Pair", Strategy: "round_robin", Enabled: true,
		Proxies: []store.ProxyWeight{{ProxyID: a.ID, Weight: 1}, {ProxyID: b.ID, Weight: 1}},
	}, []store.MemberInput{{Name: "pair.test", Match: "suffix", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	dns, err := st.DNSSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var pair *snapshot.Domain
	for i := range dns.Domains {
		if dns.Domains[i].Name == "pair.test" {
			pair = &dns.Domains[i]
			break
		}
	}
	if pair == nil || len(pair.Proxies) != 2 {
		t.Fatalf("pair.test = %+v", dns.Domains)
	}
	// List order is charlie, bravo, alpha — live proxies follow ordinal: bravo then alpha.
	if pair.Proxies[0].ID != b.ID || pair.Proxies[1].ID != a.ID {
		t.Fatalf("proxy rotation = %+v", pair.Proxies)
	}

	if err := st.MaintainPartitions(ctx); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if _, err := st.InsertHits(ctx, c.ID, []store.DNSHit{
		{At: at, ClientIP: "10.0.0.1", QName: "pair.test", QType: "A", Rcode: "NOERROR", Decision: "allowed"},
		{At: at, ClientIP: "10.0.0.2", QName: "pair.test", QType: "A", Rcode: "NOERROR", Decision: "acl"},
		{At: at, ClientIP: "10.0.0.1", QName: "other.test", QType: "A", Rcode: "NOERROR", Decision: "allowed"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertSessions(ctx, a.ID, []store.ProxyReport{
		{At: at, ClientIP: "10.0.0.1", SNI: "pair.test", BytesUp: 100, BytesDown: 400, Status: "ok"},
	}); err != nil {
		t.Fatal(err)
	}
	tot, err := st.WindowTotals(ctx, int64(time.Hour.Seconds()))
	if err != nil {
		t.Fatal(err)
	}
	if tot.Queries != 3 || tot.Blocked != 1 || tot.Clients != 2 || tot.Sessions != 1 || tot.Bytes != 500 {
		t.Fatalf("totals = %+v", tot)
	}

	if err := st.EnsureAdmin(ctx, "admin", "panel-pass-long"); err != nil {
		t.Fatal(err)
	}
	ui := panel.New(st, nil, panel.Options{Version: "test"})
	ts := httptest.NewServer(ui.Handler())
	t.Cleanup(ts.Close)
	sess := login(t, ts.URL, "admin", "panel-pass-long")

	body, _ := json.Marshal(map[string]any{"ids": []string{a.ID, c.ID, b.ID}})
	if code := apiCall(t, ts.URL, http.MethodPut, "/api/nodes/order", string(body), "application/json", sess.cookie, sess.csrf); code != http.StatusOK {
		t.Fatalf("PUT order = %d", code)
	}
	listed, err = st.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if listed[0].ID != a.ID || listed[1].ID != c.ID || listed[2].ID != b.ID {
		t.Fatalf("after PUT = %+v", listed)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/stats?window=1h", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(sess.cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var stats struct {
		Totals store.WindowTotals `json:"totals"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || stats.Totals.Queries != 3 || stats.Totals.Bytes != 500 {
		t.Fatalf("GET stats totals = %d %+v", resp.StatusCode, stats.Totals)
	}
}
