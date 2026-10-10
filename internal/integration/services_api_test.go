package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"dnsmarty/internal/migrate"
	"dnsmarty/internal/panel"
	"dnsmarty/internal/store"
)

func TestServicesAPI(t *testing.T) {
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
	_, node, err := st.CreateNode(ctx, "test", store.NodeInput{
		Name: "edge", Role: "proxy", IPv4: "203.0.113.10", Region: "lab",
		AgentHost: "127.0.0.1", AgentPort: 9444, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	ui := panel.New(st, nil, panel.Options{Version: "test"})
	ts := httptest.NewServer(ui.Handler())
	t.Cleanup(ts.Close)
	sess := login(t, ts.URL, "admin", "panel-pass-long")

	body := `{"name":"Gaming","strategy":"round_robin","enabled":true,` +
		`"proxies":[{"proxy_id":"` + node.ID + `","weight":2}],` +
		`"members":[{"name":"xbox.test","match":"suffix","enabled":true}]}`

	if code := apiCall(t, ts.URL, http.MethodPost, "/api/services", body, "application/json", sess.cookie, ""); code != http.StatusForbidden {
		t.Fatalf("POST without CSRF = %d, want 403", code)
	}
	if code := apiCall(t, ts.URL, http.MethodPost, "/api/services", body, "application/json", sess.cookie, sess.csrf); code != http.StatusOK {
		t.Fatalf("POST service = %d, want 200", code)
	}

	resp := getJSON(t, ts.URL+"/api/services", sess)
	var list struct {
		Services  []store.Service  `json:"services"`
		Proxies   []store.Node     `json:"proxies"`
		Templates []store.Template `json:"templates"`
	}
	if err := json.Unmarshal(resp, &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sv := range list.Services {
		if sv.Name == "Gaming" && len(sv.Members) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("Gaming service missing: %s", resp)
	}
	if len(list.Proxies) != 1 || list.Proxies[0].ID != node.ID {
		t.Fatalf("proxies = %+v", list.Proxies)
	}
	if list.Templates == nil {
		t.Fatalf("templates must be [] not null: %s", resp)
	}

	if code := apiCall(t, ts.URL, http.MethodGet, "/api/domains", "", "", sess.cookie, ""); code != http.StatusNotFound {
		t.Fatalf("GET /api/domains = %d, want 404", code)
	}
	if code := apiCall(t, ts.URL, http.MethodPost, "/api/templates", `{"name":"Steam","payload":{"domains":[{"name":"steam.test","match":"suffix","enabled":true}],"strategy":"weighted","proxies":[]}}`, "application/json", sess.cookie, sess.csrf); code != http.StatusOK {
		t.Fatalf("POST template = %d, want 200", code)
	}
}

func getJSON(t *testing.T, url string, sess session) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
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
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", url, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw
}
