package integration

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"dnsmarty/internal/migrate"
)

func TestServicesMigration(t *testing.T) {
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
	if err := migrate.UpTo(dsn, 9); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	var n1, n2 string
	for i, name := range []string{"edge-1", "edge-2"} {
		id := &n1
		if i == 1 {
			id = &n2
		}
		if err := pool.QueryRow(ctx, `
			INSERT INTO node (role, name) VALUES ('proxy', $1) RETURNING id::text
		`, name).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	var gid string
	if err := pool.QueryRow(ctx, `INSERT INTO domain_group (name) VALUES ('G') RETURNING id::text`).Scan(&gid); err != nil {
		t.Fatal(err)
	}
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO domain (name, match_kind, balance, group_id) VALUES
		('a.example', 'suffix', 'round_robin', $1),
		('b.example', 'suffix', 'weighted', $1),
		('solo.example', 'fqdn', 'sticky24', NULL)`, gid)
	mustExec(`INSERT INTO domain_proxy (domain_id, proxy_node_id, weight)
		SELECT id, $1::uuid, 2 FROM domain WHERE name = 'a.example'
		UNION ALL SELECT id, $1::uuid, 1 FROM domain WHERE name = 'b.example'
		UNION ALL SELECT id, $2::uuid, 3 FROM domain WHERE name = 'b.example'
		UNION ALL SELECT id, $1::uuid, 1 FROM domain WHERE name = 'solo.example'`, n1, n2)

	if err := migrate.UpTo(dsn, 10); err != nil {
		t.Fatal(err)
	}

	names := queryStrings(t, pool, `SELECT name FROM service ORDER BY name`)
	want := []string{"G", "G (2)", "example.com", "solo.example"}
	sort.Strings(want)
	if !equalStrings(names, want) {
		t.Fatalf("services = %v, want %v", names, want)
	}
	strategy := func(name string) string {
		var s string
		if err := pool.QueryRow(ctx, `SELECT strategy FROM service WHERE name = $1`, name).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if got := strategy("solo.example"); got != "sticky24" {
		t.Fatalf("solo strategy = %s", got)
	}
	var proxies int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM service_proxy sp JOIN service s ON s.id = sp.service_id WHERE s.name = 'G (2)'
	`).Scan(&proxies); err != nil {
		t.Fatal(err)
	}
	if proxies != 2 {
		t.Fatalf("G (2) proxies = %d, want 2", proxies)
	}
	var orphans int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM domain WHERE service_id IS NULL`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Fatalf("domains without service = %d", orphans)
	}
	var legacy *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.domain_group')::text`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy != nil {
		t.Fatal("domain_group still exists after 010")
	}

	if err := migrate.DownTo(dsn, 9); err != nil {
		t.Fatal(err)
	}
	balances := map[string]string{}
	rows, err := pool.Query(ctx, `SELECT name, balance FROM domain`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n, b string
		if err := rows.Scan(&n, &b); err != nil {
			t.Fatal(err)
		}
		balances[n] = b
	}
	rows.Close()
	if balances["a.example"] != "round_robin" || balances["b.example"] != "weighted" || balances["solo.example"] != "sticky24" {
		t.Fatalf("balances after down = %v", balances)
	}
	var links int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM domain_proxy`).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 4 {
		t.Fatalf("domain_proxy rows after down = %d, want 4", links)
	}
}

func queryStrings(t *testing.T, pool *pgxpool.Pool, sql string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), sql)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
