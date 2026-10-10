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

func TestInsertHitsLatency(t *testing.T) {
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
	measured := 12
	at := time.Now().UTC().Truncate(time.Second)
	if _, err := st.InsertHits(ctx, nodeID, []store.DNSHit{
		{At: at, ClientIP: "10.0.0.1", QName: "measured.example", QType: "A", Rcode: "NOERROR", Decision: "allowed", LatencyMS: &measured},
		{At: at.Add(time.Second), ClientIP: "10.0.0.1", QName: "legacy.example", QType: "A", Rcode: "NOERROR", Decision: "allowed"},
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := pool.Query(ctx, `SELECT latency_ms FROM dns_hit ORDER BY at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []*int
	for rows.Next() {
		var v *int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	if got[0] == nil || *got[0] != 12 {
		t.Fatalf("measured latency = %v, want 12", got[0])
	}
	if got[1] != nil {
		t.Fatalf("legacy latency = %d, want NULL", *got[1])
	}
}
