package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"dnsmarty/internal/migrate"
	"dnsmarty/internal/store"
)

func TestTemplates(t *testing.T) {
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

	_, proxy, err := st.CreateNode(ctx, "test", store.NodeInput{
		Name: "edge", Role: "proxy", IPv4: "203.0.113.10", Region: "lab",
		AgentHost: "127.0.0.1", AgentPort: 9444, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, dnsNode, err := st.CreateNode(ctx, "test", store.NodeInput{
		Name: "resolver", Role: "dns", IPv4: "203.0.113.20", Region: "lab",
		AgentHost: "127.0.0.1", AgentPort: 9445, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	payload := store.TemplatePayload{
		Domains:  []store.MemberInput{{Name: "xbox.com", Match: "suffix", Enabled: true}},
		Strategy: "round_robin",
		Proxies:  []store.ProxyWeight{{ProxyID: proxy.ID, Weight: 2}},
	}
	id, err := st.CreateTemplate(ctx, "admin", "Xbox Live", payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTemplate(ctx, "admin", "Xbox Live", payload); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate template name: %v", err)
	}
	bad := payload
	bad.Domains = []store.MemberInput{{Name: "xbox.com", Match: "regex", Enabled: true}}
	if _, err := st.CreateTemplate(ctx, "admin", "Bad match", bad); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("bad match: %v", err)
	}
	bad = payload
	bad.Proxies = []store.ProxyWeight{{ProxyID: dnsNode.ID, Weight: 1}}
	if _, err := st.CreateTemplate(ctx, "admin", "Dns proxy", bad); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("dns node as proxy: %v", err)
	}

	list, err := st.ListTemplates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != id || list[0].Name != "Xbox Live" || list[0].CreatedAt.IsZero() {
		t.Fatalf("templates = %+v", list)
	}
	var got store.TemplatePayload
	if err := json.Unmarshal(list[0].Payload, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Domains) != 1 || got.Domains[0].Name != "xbox.com" || got.Strategy != "round_robin" || len(got.Proxies) != 1 || got.Proxies[0].Weight != 2 {
		t.Fatalf("payload = %+v", got)
	}

	if err := st.DeleteTemplate(ctx, "admin", id); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTemplate(ctx, "admin", id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	list, err = st.ListTemplates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("templates after delete = %#v", list)
	}
}
