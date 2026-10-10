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

func TestLatencyAggregates(t *testing.T) {
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

	nodeID := seedNode(t, ctx, pool)
	at := time.Now().UTC().Add(-10 * time.Second)
	ms := func(v int) *int { return &v }
	if _, err := st.InsertHits(ctx, nodeID, []store.DNSHit{
		{At: at, ClientIP: "10.0.0.1", QName: "a.example", QType: "A", Rcode: "NOERROR", Decision: "allowed", LatencyMS: ms(10)},
		{At: at, ClientIP: "10.0.0.1", QName: "b.example", QType: "A", Rcode: "NOERROR", Decision: "allowed", LatencyMS: ms(20)},
		{At: at, ClientIP: "10.0.0.1", QName: "c.example", QType: "A", Rcode: "NOERROR", Decision: "allowed", LatencyMS: ms(100)},
		{At: at, ClientIP: "10.0.0.1", QName: "legacy.example", QType: "A", Rcode: "NOERROR", Decision: "allowed"},
	}); err != nil {
		t.Fatal(err)
	}

	o, err := st.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.LatencyAvg == nil || *o.LatencyAvg < 43 || *o.LatencyAvg > 44 {
		t.Fatalf("latency_avg_ms = %v, want about 43.3 (NULL ignored)", o.LatencyAvg)
	}
	if o.LatencyP95 == nil || *o.LatencyP95 < 90 || *o.LatencyP95 > 100 {
		t.Fatalf("latency_p95_ms = %v, want between 90 and 100", o.LatencyP95)
	}

	points, err := st.Series(ctx, time.Hour, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, p := range points {
		if p.LatencyMS != nil {
			seen = true
		}
	}
	if !seen {
		t.Fatal("series has no point with latency")
	}
}
