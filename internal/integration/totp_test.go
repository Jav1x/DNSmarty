package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"dnsmarty/internal/migrate"
	"dnsmarty/internal/panel"
	"dnsmarty/internal/store"
)

func TestTOTPLogin(t *testing.T) {
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
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	ui := panel.New(st, nil, panel.Options{Version: "test"})
	ts := httptest.NewServer(ui.Handler())
	t.Cleanup(ts.Close)
	sess := login(t, ts.URL, "admin", "panel-pass-long")

	setup := postAuthJSON(t, ts.URL, "/api/account/2fa/setup", `{}`, sess, http.StatusOK)
	var begun struct {
		Secret string `json:"secret"`
		URL    string `json:"url"`
		QR     string `json:"qr_png"`
	}
	if err := json.Unmarshal(setup, &begun); err != nil || begun.Secret == "" || !strings.HasPrefix(begun.URL, "otpauth://totp/") || begun.QR == "" {
		t.Fatalf("setup: %s", setup)
	}
	var ct []byte
	if err := pool.QueryRow(ctx, `SELECT totp_ciphertext FROM admin_user WHERE username = 'admin'`).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte(begun.Secret)) || len(ct) == 0 {
		t.Fatal("secret must not be stored in plaintext")
	}

	now := time.Now()
	wrong := postAuthJSON(t, ts.URL, "/api/account/2fa/enable", `{"code":"000000"}`, sess, http.StatusBadRequest)
	if !bytes.Contains(wrong, []byte(`"code"`)) {
		t.Fatalf("wrong code: %s", wrong)
	}

	code, err := totp.GenerateCode(begun.Secret, now)
	if err != nil {
		t.Fatal(err)
	}
	enabled := postAuthJSON(t, ts.URL, "/api/account/2fa/enable", `{"code":"`+code+`"}`, sess, http.StatusOK)
	var en struct {
		Codes []string `json:"codes"`
	}
	if err := json.Unmarshal(enabled, &en); err != nil || len(en.Codes) != 8 {
		t.Fatalf("codes: %s", enabled)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'totp.enable'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("totp.enable in audit: %d %v", n, err)
	}

	listed := getAuthJSON(t, ts.URL+"/api/account/2fa/codes", sess)
	var list struct {
		Enabled bool `json:"enabled"`
		Codes   []struct {
			ID   string `json:"id"`
			Code string `json:"code"`
			Used bool   `json:"used"`
		} `json:"codes"`
	}
	if err := json.Unmarshal(listed, &list); err != nil || !list.Enabled || len(list.Codes) != 8 || list.Codes[0].ID == "" || list.Codes[0].Code != "" {
		t.Fatalf("code list without plaintext: %s", listed)
	}
	if bytes.Contains(listed, []byte(en.Codes[0])) {
		t.Fatalf("list returned a plaintext code: %s", listed)
	}
	postAuthJSON(t, ts.URL, "/api/account/2fa/codes/"+list.Codes[0].ID, `{}`, sess, http.StatusOK)
	after := getAuthJSON(t, ts.URL+"/api/account/2fa/codes", sess)
	var spent struct {
		Codes []struct {
			Code string `json:"code"`
			Used bool   `json:"used"`
		} `json:"codes"`
	}
	if err := json.Unmarshal(after, &spent); err != nil || !spent.Codes[0].Used || spent.Codes[0].Code != "" {
		t.Fatalf("after click the code must be spent: %s", after)
	}

	status, raw, cookies := postJSON(t, ts.URL+"/api/login", `{"username":"admin","password":"panel-pass-long"}`)
	if status != http.StatusOK || bytes.Contains(raw, []byte(`"csrf"`)) {
		t.Fatalf("password with 2FA on must not issue a session: %d %s", status, raw)
	}
	for _, c := range cookies {
		if c.Name == "dnsmarty" {
			t.Fatal("session cookie arrived before the code")
		}
	}
	var challenge struct {
		Required bool   `json:"totp_required"`
		Ticket   string `json:"ticket"`
	}
	if err := json.Unmarshal(raw, &challenge); err != nil || !challenge.Required || challenge.Ticket == "" {
		t.Fatalf("ticket: %s", raw)
	}
	status, _, _ = postJSON(t, ts.URL+"/api/login/totp", `{"ticket":"`+challenge.Ticket+`","code":"`+code+`"}`)
	if status != http.StatusUnauthorized {
		t.Fatalf("setup code reused: %d, want 401", status)
	}
	next, err := totp.GenerateCode(begun.Secret, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	status, raw, cookies = postJSON(t, ts.URL+"/api/login/totp", `{"ticket":"`+challenge.Ticket+`","code":"`+next+`"}`)
	if status != http.StatusOK {
		t.Fatalf("next code: %d %s", status, raw)
	}
	if !hasSessionCookie(cookies) {
		t.Fatal("no session after a valid code")
	}

	backup := en.Codes[1]
	status, raw, _ = postJSON(t, ts.URL+"/api/login", `{"username":"admin","password":"panel-pass-long"}`)
	if err := json.Unmarshal(raw, &challenge); err != nil || status != http.StatusOK {
		t.Fatalf("second sign-in: %d %s", status, raw)
	}
	status, raw, cookies = postJSON(t, ts.URL+"/api/login/totp", `{"ticket":"`+challenge.Ticket+`","code":"`+backup+`"}`)
	if status != http.StatusOK || !hasSessionCookie(cookies) {
		t.Fatalf("backup code: %d %s", status, raw)
	}
	status, raw, _ = postJSON(t, ts.URL+"/api/login", `{"username":"admin","password":"panel-pass-long"}`)
	if err := json.Unmarshal(raw, &challenge); err != nil {
		t.Fatal(err)
	}
	status, _, _ = postJSON(t, ts.URL+"/api/login/totp", `{"ticket":"`+challenge.Ticket+`","code":"`+backup+`"}`)
	if status != http.StatusUnauthorized {
		t.Fatalf("backup code reused: %d, want 401", status)
	}

	status, raw, _ = postJSON(t, ts.URL+"/api/login", `{"username":"admin","password":"panel-pass-long"}`)
	if err := json.Unmarshal(raw, &challenge); err != nil || status != http.StatusOK {
		t.Fatalf("ticket for misses: %d %s", status, raw)
	}
	burn := now.Add(60 * time.Second)
	for i := 0; i < 5; i++ {
		if _, _, err := st.RedeemLoginTicket(ctx, challenge.Ticket, "000000", burn); err == nil {
			t.Fatal("empty code must not pass")
		}
	}
	later, err := totp.GenerateCode(begun.Secret, burn)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.RedeemLoginTicket(ctx, challenge.Ticket, later, burn); err == nil {
		t.Fatal("ticket must die after five misses")
	}

	postAuthJSON(t, ts.URL, "/api/account/2fa/disable", `{"password":"wrong-password-here"}`, sess, http.StatusBadRequest)
	postAuthJSON(t, ts.URL, "/api/account/2fa/disable", `{"password":"panel-pass-long"}`, sess, http.StatusOK)
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'totp.disable'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("totp.disable in audit: %d %v", n, err)
	}
	sess = login(t, ts.URL, "admin", "panel-pass-long")
	if sess.csrf == "" {
		t.Fatal("after 2FA off, password sign-in must create a session")
	}
}

func postAuthJSON(t *testing.T, base, path, body string, sess session, want int) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", sess.csrf)
	req.AddCookie(sess.cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s: %d %s, want %d", http.MethodPost, path, resp.StatusCode, raw, want)
	}
	return raw
}

func getAuthJSON(t *testing.T, url string, sess session) []byte {
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
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d %s", url, resp.StatusCode, raw)
	}
	return raw
}

func postJSON(t *testing.T, url, body string) (int, []byte, []*http.Cookie) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, resp.Cookies()
}

func hasSessionCookie(cookies []*http.Cookie) bool {
	for _, c := range cookies {
		if c.Name == "dnsmarty" && c.Value != "" {
			return true
		}
	}
	return false
}
