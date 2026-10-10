package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"dnsmarty/internal/migrate"
	"dnsmarty/internal/panel"
	"dnsmarty/internal/store"
)

func TestNodeDisableContract(t *testing.T) {
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
	if err := st.EnsureAdmin(ctx, "admin", "panel-pass-long"); err != nil {
		t.Fatal(err)
	}
	ui := panel.New(st, nil, panel.Options{Version: "test"})
	ts := httptest.NewServer(ui.Handler())
	t.Cleanup(ts.Close)
	sess := login(t, ts.URL, "admin", "panel-pass-long")

	code, raw := apiNodes(t, ts.URL, sess, http.MethodPost, "/api/nodes", `{
		"name": "edge",
		"role": "dns",
		"public_ipv4": "203.0.113.10",
		"region": "lab",
		"agent_host": "127.0.0.1",
		"agent_port": 224,
		"enabled": true
	}`)
	var created struct {
		Node  store.Node `json:"node"`
		Key   string     `json:"key"`
		Image string     `json:"image"`
	}
	if code != http.StatusOK {
		t.Fatalf("POST /api/nodes: %d %s", code, raw)
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("POST /api/nodes: %v %s", err, raw)
	}
	if created.Node.ID == "" || created.Key == "" || created.Image == "" {
		t.Fatalf("create node returned empty fields: %#v", created)
	}
	id := created.Node.ID

	if got := nodeByID(t, ts.URL, sess, id); got == nil || !got.Enabled {
		t.Fatalf("after create wanted enabled=true: %#v", got)
	}

	body := fmt.Sprintf(`{
		"name": "edge",
		"role": "dns",
		"public_ipv4": "203.0.113.10",
		"region": "lab",
		"agent_host": "127.0.0.1",
		"agent_port": 224,
		"enabled": %t
	}`, false)
	code, raw = apiNodes(t, ts.URL, sess, http.MethodPost, "/api/nodes/"+id, body)
	if code != http.StatusOK {
		t.Fatalf("POST /api/nodes/{id} {enabled:false}: %d %s", code, raw)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if n := auditCount(ctx, t, pool, "node.disable"); n != 1 {
		t.Fatalf("node.disable in audit: %d rows, want 1", n)
	}
	code, raw = apiNodes(t, ts.URL, sess, http.MethodPost, "/api/nodes/"+id, body)
	if code != http.StatusOK {
		t.Fatalf("second disable: %d %s", code, raw)
	}
	if n := auditCount(ctx, t, pool, "node.disable"); n != 1 {
		t.Fatalf("no-op disable added an audit row: %d", n)
	}

	off := nodeByID(t, ts.URL, sess, id)
	if off == nil {
		t.Fatal("node missing from list after disable")
	}
	if off.Enabled {
		t.Fatalf("GET /api/nodes returned enabled=true after disable: %#v", off)
	}
	if off.Fresh {
		t.Fatalf("disabled node must not be fresh: %#v", off)
	}

	code, raw = apiNodes(t, ts.URL, sess, http.MethodPut, "/api/nodes/"+id, body)
	if code != http.StatusNotFound {
		t.Fatalf("PUT /api/nodes/{id}: %d (want 404 — no PUT route) %s", code, raw)
	}

	code, raw = apiNodes(t, ts.URL, sess, http.MethodDelete, "/api/nodes/"+id, "")
	if code != http.StatusOK {
		t.Fatalf("DELETE /api/nodes/{id}: %d %s", code, raw)
	}
	if got := nodeByID(t, ts.URL, sess, id); got != nil {
		t.Fatalf("DELETE did not remove the node: %#v", got)
	}
}

func auditCount(ctx context.Context, t *testing.T, pool *pgxpool.Pool, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func apiNodes(t *testing.T, base string, sess session, method, path, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" && method != http.MethodDelete {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-CSRF-Token", sess.csrf)
	req.AddCookie(sess.cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, raw
}

func nodeByID(t *testing.T, base string, sess session, id string) *store.Node {
	t.Helper()
	code, raw := apiNodes(t, base, sess, http.MethodGet, "/api/nodes", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/nodes: %d %s", code, raw)
	}
	var rows []store.Node
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("GET /api/nodes: %v %s", err, raw)
	}
	for i := range rows {
		if rows[i].ID == id {
			return &rows[i]
		}
	}
	return nil
}
