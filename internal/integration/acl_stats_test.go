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

/*
Тест агрегата «заблокировано 24ч»: dns_hit с decision='acl' за сутки

	раскладывается по включённым правилам client_cidr (netip-попадание IP в CIDR).
	Проверки: счётчики по /32 и /24; выключенное правило молчит; старые (>24ч)
	и не-acl записи не считаются; при нуле правил — пустой срез и "rules":[]
	в JSON эндпоинта (не null и не ошибка); контракт лимита — 201 включённое
	правило даёт ровно 200 записей (aclRulesLimit).
*/
func TestACLRulesStats(t *testing.T) {
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

	node := seedNode(t, ctx, pool)

	// Ноль правил: пустой срез (не nil), а эндпоинт отдаёт "rules":[].
	empty, err := st.ACLRulesStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("без правил ждём пустой срез: %v", empty)
	}
	emptyJSON := aclStatsJSON(t, st)
	// json.Encoder добавляет \n — сравниваем после TrimSpace.
	if strings.TrimSpace(emptyJSON) != `{"rules":[]}` {
		t.Fatalf("пустой JSON не []: %s", emptyJSON)
	}

	// Правила: /32 (deny), /24 (allow) — оба включены; /8 выключено.
	if err := st.CreateClient(ctx, "test", "198.51.100.7/32", "scanner", "deny", true); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateClient(ctx, "test", "203.0.113.0/24", "lab", "allow", true); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateClient(ctx, "test", "10.0.0.0/8", "off", "allow", false); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, c := range mustListClients(t, ctx, st) {
		ids[c.CIDR] = c.ID
	}
	if len(ids) != 3 {
		t.Fatalf("правила не создались: %v", ids)
	}

	// Хиты: 198.51.100.7 → правило /32 (2), 203.0.113.42 → /24 (3),
	// 192.0.2.5 → ни в одно (1), 10.5.5.5 → только в выключенное /8 (1).
	// Шум: forward-запись того же IP и acl-запись старше 24 часов не считаются.
	now := time.Now().UTC()
	for _, h := range []store.DNSHit{
		{At: now, ClientIP: "198.51.100.7", QName: "a.test.", Decision: "acl"},
		{At: now.Add(-time.Minute), ClientIP: "198.51.100.7", QName: "b.test.", Decision: "acl"},
		{At: now, ClientIP: "203.0.113.42", QName: "c.test.", Decision: "acl"},
		{At: now.Add(-2 * time.Minute), ClientIP: "203.0.113.42", QName: "d.test.", Decision: "acl"},
		{At: now.Add(-3 * time.Minute), ClientIP: "203.0.113.42", QName: "e.test.", Decision: "acl"},
		{At: now, ClientIP: "192.0.2.5", QName: "f.test.", Decision: "acl"},
		{At: now, ClientIP: "10.5.5.5", QName: "g.test.", Decision: "acl"},
		{At: now, ClientIP: "198.51.100.7", QName: "h.test.", Decision: "forward"},
		{At: now.Add(-26 * time.Hour), ClientIP: "198.51.100.7", QName: "old.test.", Decision: "acl"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO dns_hit (day, at, node_id, client_ip, qname, qtype, rcode, decision)
			VALUES ($1, $2, $3, $4, $5, 'A', 'NOERROR', $6)
		`, h.At.UTC().Format("2006-01-02"), h.At, node, h.ClientIP, h.QName, h.Decision); err != nil {
			t.Fatal(err)
		}
	}

	out, err := st.ACLRulesStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("с правилами ждём срез, не nil")
	}
	got := map[string]int64{}
	for _, r := range out {
		got[r.ID] = r.Hits
	}
	if len(out) != 2 {
		t.Fatalf("ждём 2 включённых правила: %+v", out)
	}
	if got[ids["198.51.100.7/32"]] != 2 {
		t.Fatalf("/32: %d (хотим 2)", got[ids["198.51.100.7/32"]])
	}
	if got[ids["203.0.113.0/24"]] != 3 {
		t.Fatalf("/24: %d (хотим 3)", got[ids["203.0.113.0/24"]])
	}
	if _, other := got[ids["10.0.0.0/8"]]; other {
		t.Fatal("выключенное правило попало в статистику")
	}

	// Эндпоинт: {rules:[{id,hits}]} тем же счётчикам.
	rulesJSON := aclStatsJSON(t, st)
	var body struct {
		Rules []store.RuleHit `json:"rules"`
	}
	if err := json.Unmarshal([]byte(rulesJSON), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Rules) != 2 {
		t.Fatalf("эндпоинт: %s", rulesJSON)
	}
	for _, r := range body.Rules {
		if want := got[r.ID]; r.Hits != want {
			t.Fatalf("эндпоинт %s: %d (хотим %d)", r.ID, r.Hits, want)
		}
	}

	// Лимит контракта: aclRulesLimit = 200 включённых правил. Досыпаем 199
	// разрешённых deny-правил (192.168.N.0/24 — мимо всех хитовых IP), всего
	// 201 включённое. ORDER BY list_kind, cidr ставит allow и младшие CIDR
	// раньше, поэтому ровно 200 записей, а вытесненным оказывается /32 deny —
	// последний по сортировке.
	for n := 0; n < 199; n++ {
		cidr := fmt.Sprintf("192.168.%d.0/24", n)
		if err := st.CreateClient(ctx, "test", cidr, "filler", "deny", true); err != nil {
			t.Fatalf("%s: %v", cidr, err)
		}
	}
	capped, err := st.ACLRulesStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 200 {
		t.Fatalf("лимит: %d записей (хотим ровно 200 из 201 включённого)", len(capped))
	}
	cappedHits := map[string]int64{}
	for _, r := range capped {
		cappedHits[r.ID] = r.Hits
	}
	if cappedHits[ids["203.0.113.0/24"]] != 3 {
		t.Fatalf("лимит вытеснил /24: %d (хотим 3)", cappedHits[ids["203.0.113.0/24"]])
	}
	if _, inside := cappedHits[ids["198.51.100.7/32"]]; inside {
		t.Fatal("/32 deny должен быть вытеснен лимитом (последний по ORDER BY list_kind, cidr)")
	}

	// Эндпоинт под лимитом: те же 200 записей.
	cappedJSON := aclStatsJSON(t, st)
	var cappedBody struct {
		Rules []store.RuleHit `json:"rules"`
	}
	if err := json.Unmarshal([]byte(cappedJSON), &cappedBody); err != nil {
		t.Fatal(err)
	}
	if len(cappedBody.Rules) != 200 {
		t.Fatalf("эндпоинт под лимитом: %d записей (хотим 200)", len(cappedBody.Rules))
	}
}

func seedNode(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO node (name, role, public_ipv4, region, agent_host, agent_port, enabled)
		VALUES ('edge', 'proxy', '203.0.113.10', 'lab', '127.0.0.1', 9444, true)
	`); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM node WHERE name = 'edge'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustListClients(t *testing.T, ctx context.Context, st *store.Store) []store.ClientCIDR {
	t.Helper()
	rows, err := st.ListClients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// aclStatsJSON calls the authenticated endpoint and returns the raw body.
func aclStatsJSON(t *testing.T, st *store.Store) string {
	t.Helper()
	if err := st.EnsureAdmin(context.Background(), "admin", "panel-pass-long"); err != nil {
		t.Fatal(err)
	}
	ui := panel.New(st, nil, panel.Options{Version: "test"})
	ts := httptest.NewServer(ui.Handler())
	t.Cleanup(ts.Close)
	sess := login(t, ts.URL, "admin", "panel-pass-long")
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/stats/acl", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(sess.cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/stats/acl: %d %s", resp.StatusCode, body)
	}
	return string(body)
}
