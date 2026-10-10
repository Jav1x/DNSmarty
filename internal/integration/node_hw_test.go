package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"dnsmarty/internal/migrate"
	"dnsmarty/internal/panel"
	"dnsmarty/internal/store"
)

func TestNodeHW(t *testing.T) {
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

	createNode := func(name, ip, port string) string {
		t.Helper()
		body := `{"name":"` + name + `","role":"dns","public_ipv4":"` + ip + `","region":"lab","agent_host":"127.0.0.1","agent_port":` + port + `,"enabled":true}`
		code, raw := apiNodes(t, ts.URL, sess, http.MethodPost, "/api/nodes", body)
		if code/100 != 2 {
			t.Fatalf("create %s: %d %s", name, code, raw)
		}
		var created struct {
			Node store.Node `json:"node"`
		}
		if err := json.Unmarshal(raw, &created); err != nil {
			t.Fatal(err)
		}
		return created.Node.ID
	}
	edgeID := createNode("edge", "203.0.113.10", "224")
	labID := createNode("lab", "203.0.113.11", "225")

	if err := st.SetNodeHW(ctx, edgeID, json.RawMessage(`{"cpu_model":"x"}`)); err != nil {
		t.Fatal(err)
	}

	edge := nodeByID(t, ts.URL, sess, edgeID)
	if edge == nil {
		t.Fatal("edge node missing from /api/nodes")
	}
	var hw struct {
		CPUModel string `json:"cpu_model"`
	}
	if err := json.Unmarshal(edge.LastHW, &hw); err != nil || hw.CPUModel != "x" {
		t.Fatalf("edge last_hw = %s, want cpu_model x", edge.LastHW)
	}

	lab := nodeByID(t, ts.URL, sess, labID)
	if lab == nil {
		t.Fatal("lab node missing from /api/nodes")
	}
	if s := string(lab.LastHW); s != "null" {
		t.Fatalf("lab last_hw = %s, want null", s)
	}
}
