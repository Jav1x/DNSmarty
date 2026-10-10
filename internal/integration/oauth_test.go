package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestOAuthLinkAndLogin(t *testing.T) {
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

	var panelURL string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Errorf("form: %v", err)
			}
			if r.Form.Get("client_secret") != "sekret" || r.Form.Get("code") != "good" {
				http.Error(w, "no", http.StatusBadRequest)
				return
			}
			if r.Form.Get("redirect_uri") != panelURL+"/api/auth/oauth/google/callback" {
				http.Error(w, "redirect", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok"})
		case "/user":
			if r.Header.Get("Authorization") != "Bearer tok" {
				http.Error(w, "no", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"sub": "g-user", "name": "Ada Lovelace"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(mock.Close)

	ui := panel.New(st, nil, panel.Options{
		Version: "test",
		OAuthEndpoints: map[string]panel.OAuthEndpoint{
			"google": {Authorize: mock.URL + "/authorize", Token: mock.URL + "/token", UserInfo: mock.URL + "/user"},
		},
	})
	ts := httptest.NewServer(ui.Handler())
	t.Cleanup(ts.Close)
	panelURL = ts.URL
	sess := login(t, ts.URL, "admin", "panel-pass-long")

	empty := getPublic(t, ts.URL+"/api/auth/providers")
	if strings.Contains(string(empty), "google") {
		t.Fatalf("disabled provider must not appear on login: %s", empty)
	}
	postAuthJSON(t, ts.URL, "/api/settings/oauth", `{"provider":"google","client_id":"cid","enabled":true}`, sess, http.StatusBadRequest)
	postAuthJSON(t, ts.URL, "/api/settings/oauth", `{"provider":"google","client_id":"cid","secret":"sekret","enabled":true}`, sess, http.StatusOK)
	saved := getAuthJSON(t, ts.URL+"/api/settings/oauth", sess)
	if strings.Contains(string(saved), "sekret") || !strings.Contains(string(saved), `"client_id":"cid"`) {
		t.Fatalf("settings: %s", saved)
	}
	var ct []byte
	if err := pool.QueryRow(ctx, `SELECT secret_ciphertext FROM oauth_provider WHERE provider = 'google'`).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if len(ct) == 0 || strings.Contains(string(ct), "sekret") {
		t.Fatal("secret must not be stored in plaintext")
	}
	pub := getPublic(t, ts.URL+"/api/auth/providers")
	if !strings.Contains(string(pub), `"id":"google"`) || strings.Contains(string(pub), "sekret") || strings.Contains(string(pub), "cid") {
		t.Fatalf("public list: %s", pub)
	}

	noFollow := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp := noFollowGet(t, noFollow, ts.URL+"/api/auth/oauth/google/login", nil)
	loc := mustLoc(t, resp)
	if loc.Query().Get("client_id") != "cid" || loc.Query().Get("state") == "" || loc.Query().Get("scope") != "openid email profile" {
		t.Fatalf("login start: %s", loc)
	}
	stateCookie := cookieNamed(t, resp, "dnsmarty_oauth")
	if stateCookie.SameSite != http.SameSiteLaxMode || !stateCookie.HttpOnly {
		t.Fatalf("cookie state: %+v", stateCookie)
	}
	resp = noFollowGet(t, noFollow, ts.URL+"/api/auth/oauth/google/callback?code=good&state="+url.QueryEscape(loc.Query().Get("state")), stateCookie)
	back := mustLoc(t, resp)
	if back.Path != "/login" || back.Query().Get("oauth") != "unlinked" || hasSessionCookie(resp.Cookies()) {
		t.Fatalf("unlinked account must not sign in: %s", back)
	}

	resp = noFollowGet(t, noFollow, ts.URL+"/api/account/oauth/google/link", sess.cookie)
	loc = mustLoc(t, resp)
	stateCookie = cookieNamed(t, resp, "dnsmarty_oauth")
	resp = noFollowGet(t, noFollow, ts.URL+"/api/auth/oauth/google/callback?code=good&state="+url.QueryEscape(loc.Query().Get("state")), stateCookie)
	back = mustLoc(t, resp)
	if back.Path != "/account" || back.RawQuery != "" {
		t.Fatalf("link: %s", back)
	}
	links := getAuthJSON(t, ts.URL+"/api/account/oauth", sess)
	if !strings.Contains(string(links), `"id":"google"`) || !strings.Contains(string(links), `"linked":true`) || !strings.Contains(string(links), `"display_name":"Ada Lovelace"`) {
		t.Fatalf("links: %s", links)
	}

	resp = noFollowGet(t, noFollow, ts.URL+"/api/auth/oauth/google/login", nil)
	loc = mustLoc(t, resp)
	stateCookie = cookieNamed(t, resp, "dnsmarty_oauth")
	resp = noFollowGet(t, noFollow, ts.URL+"/api/auth/oauth/google/callback?code=good&state="+url.QueryEscape(loc.Query().Get("state")), stateCookie)
	back = mustLoc(t, resp)
	if back.Path != "/" || !hasSessionCookie(resp.Cookies()) {
		t.Fatalf("sign-in with linked account: %s cookies=%v", back, resp.Cookies())
	}
	me := getAuthJSON(t, ts.URL+"/api/me", session{cookie: sessionCookie(t, resp)})
	if !strings.Contains(string(me), `"user":"admin"`) {
		t.Fatalf("session: %s", me)
	}

	resp = noFollowGet(t, noFollow, ts.URL+"/api/auth/oauth/google/callback?code=good&state=nope", stateCookie)
	back = mustLoc(t, resp)
	if back.Query().Get("oauth") != "bad_state" {
		t.Fatalf("foreign state: %s", back)
	}

	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/account/oauth/google", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-CSRF-Token", sess.csrf)
	req.AddCookie(sess.cookie)
	del, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer del.Body.Close()
	raw, _ := io.ReadAll(del.Body)
	if del.StatusCode != http.StatusOK {
		t.Fatalf("unlink: %d %s", del.StatusCode, raw)
	}
	resp = noFollowGet(t, noFollow, ts.URL+"/api/auth/oauth/google/login", nil)
	loc = mustLoc(t, resp)
	stateCookie = cookieNamed(t, resp, "dnsmarty_oauth")
	resp = noFollowGet(t, noFollow, ts.URL+"/api/auth/oauth/google/callback?code=good&state="+url.QueryEscape(loc.Query().Get("state")), stateCookie)
	back = mustLoc(t, resp)
	if back.Query().Get("oauth") != "unlinked" {
		t.Fatalf("after unlink: %s", back)
	}
}

func getPublic(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
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

func noFollowGet(t *testing.T, client *http.Client, url string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	return resp
}

func mustLoc(t *testing.T, resp *http.Response) *url.URL {
	t.Helper()
	loc, err := resp.Location()
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("redirect %d: %v", resp.StatusCode, err)
	}
	return loc
}

func cookieNamed(t *testing.T, resp *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	t.Fatalf("missing cookie %s in %+v", name, resp.Cookies())
	return nil
}

func sessionCookie(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == "dnsmarty" && c.Value != "" {
			return c
		}
	}
	t.Fatal("no session")
	return nil
}
