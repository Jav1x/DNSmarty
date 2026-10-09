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

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"dnsmarty/internal/migrate"
	"dnsmarty/internal/panel"
	"dnsmarty/internal/store"
)

/*
Закрепление контракта «Ноды» со стороны UI (задача 7, редизайн): выключатель
«Нода отключена» в модалке отправляет `{enabled:false}` на эндпоинт обновления
ноды вместе с остальными полями NodeInput, кнопка 🗑 — на DELETE. Бэкенд не
меняется: тест проверяет, что существующая панель принимает disabled-обновление,
что GET /api/nodes после него отдаёт enabled=false и что нода убирается DELETE'ом.
Маршрут обновления — POST /api/nodes/{id}; PUT в замороженной серверной таблице
маршрутов отсутствует (бриф говорил PUT — обогнано тестом, верб не добавлялся).
*/

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

	// Нода создаётся обычным путём панели: POST /api/nodes → {node, key, image}.
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
		t.Fatalf("создание ноды вернуло пустые поля: %#v", created)
	}
	id := created.Node.ID

	// Санити: сразу после создания нода включена.
	if got := nodeByID(t, ts.URL, sess, id); got == nil || !got.Enabled {
		t.Fatalf("после создания ждём enabled=true: %#v", got)
	}

	// Отключение: полное тело NodeInput с enabled:false. Верб — POST,
	// как в серверной таблице маршрутов (server.go: POST /api/nodes/{id}).
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

	// Список после отключения: enabled=false (и, следовательно, fresh=false).
	off := nodeByID(t, ts.URL, sess, id)
	if off == nil {
		t.Fatal("нода пропала из списка после отключения")
	}
	if off.Enabled {
		t.Fatalf("GET /api/nodes вернул enabled=true после отключения: %#v", off)
	}
	if off.Fresh {
		t.Fatalf("выключенная нода не может быть fresh: %#v", off)
	}

	// PUT не маршрутизируется: путь проваливается в SPA-обёртку, которая отвечает
	// «Нет такого метода API» (404/not_found). Бриф говорил PUT — тест это обогнал.
	code, raw = apiNodes(t, ts.URL, sess, http.MethodPut, "/api/nodes/"+id, body)
	if code != http.StatusNotFound {
		t.Fatalf("PUT /api/nodes/{id}: %d (ждём 404 — маршрута PUT нет) %s", code, raw)
	}

	// Кнопка 🗑: DELETE /api/nodes/{id} → ок, нода уходит из списка.
	code, raw = apiNodes(t, ts.URL, sess, http.MethodDelete, "/api/nodes/"+id, "")
	if code != http.StatusOK {
		t.Fatalf("DELETE /api/nodes/{id}: %d %s", code, raw)
	}
	if got := nodeByID(t, ts.URL, sess, id); got != nil {
		t.Fatalf("DELETE не удалил ноду: %#v", got)
	}
}

// apiNodes делает авторизированный запрос к /api/nodes и возвращает статус и тело.
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

// nodeByID читает GET /api/nodes и находит ноду по id (nil — нет в списке).
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
