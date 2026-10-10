package panel

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dnsmarty/internal/store"
)

const oauthCookie = "dnsmarty_oauth"

// OAuthEndpoint replaces the real authorize, token and userinfo URLs. Tests set it.
type OAuthEndpoint struct {
	Authorize string
	Token     string
	UserInfo  string
}

type oauthSpec struct {
	Authorize string
	Token     string
	UserInfo  string
	Scope     string
}

var oauthSpecs = map[string]oauthSpec{
	"google": {
		Authorize: "https://accounts.google.com/o/oauth2/v2/auth",
		Token:     "https://oauth2.googleapis.com/token",
		UserInfo:  "https://openidconnect.googleapis.com/v1/userinfo",
		Scope:     "openid email profile",
	},
	"github": {
		Authorize: "https://github.com/login/oauth/authorize",
		Token:     "https://github.com/login/oauth/access_token",
		UserInfo:  "https://api.github.com/user",
		Scope:     "read:user",
	},
	"yandex": {
		Authorize: "https://oauth.yandex.ru/authorize",
		Token:     "https://oauth.yandex.ru/token",
		UserInfo:  "https://login.yandex.ru/info?format=json",
		Scope:     "login:info",
	},
}

func (s *Server) oauthSpec(id string) (oauthSpec, bool) {
	sp, ok := oauthSpecs[id]
	if !ok {
		return oauthSpec{}, false
	}
	if over, ok := s.opts.OAuthEndpoints[id]; ok {
		if over.Authorize != "" {
			sp.Authorize = over.Authorize
		}
		if over.Token != "" {
			sp.Token = over.Token
		}
		if over.UserInfo != "" {
			sp.UserInfo = over.UserInfo
		}
	}
	return sp, true
}

func (s *Server) oauthClient() *http.Client {
	if s.opts.OAuthHTTP != nil {
		return s.opts.OAuthHTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (s *Server) oauthProviders(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.OAuthProviders(r.Context(), true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if list == nil {
		list = []store.OAuthProvider{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": list})
}

func (s *Server) oauthSettings(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.OAuthProviders(r.Context(), false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": list})
}

func (s *Server) oauthSettingsSave(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		Provider string `json:"provider"`
		ClientID string `json:"client_id"`
		Secret   string `json:"secret"`
		Enabled  bool   `json:"enabled"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.store.SaveOAuth(r.Context(), sess.Username, body.Provider, body.ClientID, body.Secret, body.Enabled); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) oauthAccount(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	list, err := s.store.OAuthLinks(r.Context(), sess.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": list})
}

func (s *Server) oauthLogin(w http.ResponseWriter, r *http.Request) {
	s.oauthStart(w, r, "login", "", "")
}

func (s *Server) oauthLink(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	s.oauthStart(w, r, "link", sess.UserID, sess.Username)
}

func (s *Server) oauthStart(w http.ResponseWriter, r *http.Request, purpose, userID, username string) {
	id := r.PathValue("provider")
	sp, ok := s.oauthSpec(id)
	if !ok {
		s.oauthRedirect(w, r, purpose, "failed")
		return
	}
	clientID, _, err := s.store.OAuthClient(r.Context(), id)
	if err != nil {
		s.oauthRedirect(w, r, purpose, "failed")
		return
	}
	state, err := randomHex(16)
	if err != nil {
		s.oauthRedirect(w, r, purpose, "failed")
		return
	}
	redirectURI := s.externalBase(r) + "/api/auth/oauth/" + id + "/callback"
	sealed, err := s.store.SealOAuthPending(store.OAuthPending{
		State: state, Provider: id, Purpose: purpose, UserID: userID, Username: username, Redirect: redirectURI,
	})
	if err != nil {
		s.oauthRedirect(w, r, purpose, "failed")
		return
	}
	s.setOAuthCookie(w, sealed, 600)
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", sp.Scope)
	q.Set("state", state)
	http.Redirect(w, r, sp.Authorize+"?"+q.Encode(), http.StatusFound)
}

func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	s.clearOAuthCookie(w)
	purpose := "login"
	raw, err := r.Cookie(oauthCookie)
	if err != nil {
		s.oauthRedirect(w, r, purpose, "bad_state")
		return
	}
	pending, err := s.store.OpenOAuthPending(raw.Value)
	if err != nil {
		s.oauthRedirect(w, r, purpose, "bad_state")
		return
	}
	purpose = pending.Purpose
	got := r.URL.Query().Get("state")
	if pending.Provider != r.PathValue("provider") || len(got) != len(pending.State) || got != pending.State {
		s.oauthRedirect(w, r, purpose, "bad_state")
		return
	}
	redirectURI := s.externalBase(r) + "/api/auth/oauth/" + pending.Provider + "/callback"
	if redirectURI != pending.Redirect || r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		s.oauthRedirect(w, r, purpose, "denied")
		return
	}
	ident, err := s.fetchOAuthUser(r, pending.Provider, r.URL.Query().Get("code"), redirectURI)
	if err != nil {
		s.log.Warn("oauth", "provider", pending.Provider, "err", err)
		s.oauthRedirect(w, r, purpose, "failed")
		return
	}
	if purpose == "link" {
		// The session cookie is SameSite=Strict, so the provider's redirect does not send it.
		// The sealed state was issued only by the authenticated link route.
		if pending.UserID == "" {
			s.oauthRedirect(w, r, purpose, "bad_state")
			return
		}
		if err := s.store.LinkOAuth(r.Context(), pending.Username, pending.UserID, pending.Provider, ident.UID, ident.Name); err != nil {
			s.oauthRedirect(w, r, purpose, "failed")
			return
		}
		s.oauthRedirect(w, r, purpose, "")
		return
	}
	userID, username, err := s.store.UserByOAuth(r.Context(), pending.Provider, ident.UID)
	if errors.Is(err, store.ErrNotFound) {
		s.oauthRedirect(w, r, purpose, "unlinked")
		return
	}
	if err != nil {
		s.oauthRedirect(w, r, purpose, "failed")
		return
	}
	if err := s.store.SetOAuthDisplayName(r.Context(), pending.Provider, ident.UID, ident.Name); err != nil {
		s.log.Warn("oauth name", "provider", pending.Provider, "err", err)
	}
	ip := s.clientIP(r).String()
	cookie, _, err := s.store.CreateSession(r.Context(), userID, ip, r.UserAgent())
	if err != nil {
		s.oauthRedirect(w, r, purpose, "failed")
		return
	}
	s.audit(r, username, "oauth.login", map[string]string{"provider": pending.Provider, "ip": ip})
	s.setCookie(w, cookie, int(store.SessionTTL.Seconds()))
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) oauthUnlink(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := s.store.UnlinkOAuth(r.Context(), sess.Username, sess.UserID, r.PathValue("provider")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) fetchOAuthUser(r *http.Request, provider, code, redirectURI string) (oauthPerson, error) {
	sp, ok := s.oauthSpec(provider)
	if !ok {
		return oauthPerson{}, errors.New("provider")
	}
	clientID, secret, err := s.store.OAuthClient(r.Context(), provider)
	if err != nil {
		return oauthPerson{}, err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("client_secret", secret)
	form.Set("redirect_uri", redirectURI)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, sp.Token, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthPerson{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "DNSmarty")
	resp, err := s.oauthClient().Do(req)
	if err != nil {
		return oauthPerson{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return oauthPerson{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return oauthPerson{}, errors.New("token")
	}
	var tok struct {
		Access string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.Access == "" {
		return oauthPerson{}, errors.New("token")
	}
	ureq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, sp.UserInfo, nil)
	if err != nil {
		return oauthPerson{}, err
	}
	ureq.Header.Set("Accept", "application/json")
	ureq.Header.Set("User-Agent", "DNSmarty")
	if provider == "yandex" {
		ureq.Header.Set("Authorization", "OAuth "+tok.Access)
	} else {
		ureq.Header.Set("Authorization", "Bearer "+tok.Access)
	}
	uresp, err := s.oauthClient().Do(ureq)
	if err != nil {
		return oauthPerson{}, err
	}
	defer uresp.Body.Close()
	ubody, err := io.ReadAll(io.LimitReader(uresp.Body, 1<<20))
	if err != nil {
		return oauthPerson{}, err
	}
	if uresp.StatusCode != http.StatusOK {
		return oauthPerson{}, errors.New("user")
	}
	return oauthIdentity(provider, ubody)
}

type oauthPerson struct {
	UID  string
	Name string
}

func oauthIdentity(provider string, raw []byte) (oauthPerson, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return oauthPerson{}, err
	}
	uid, err := oauthUID(provider, m)
	if err != nil {
		return oauthPerson{}, err
	}
	return oauthPerson{UID: uid, Name: oauthName(provider, m)}, nil
}

func oauthUID(provider string, m map[string]any) (string, error) {
	if provider == "google" {
		sub, _ := m["sub"].(string)
		if sub == "" {
			return "", errors.New("sub")
		}
		return sub, nil
	}
	switch v := m["id"].(type) {
	case string:
		if v == "" {
			return "", errors.New("id")
		}
		return v, nil
	case float64:
		return strconv.FormatInt(int64(v), 10), nil
	default:
		return "", errors.New("id")
	}
}

func oauthName(provider string, m map[string]any) string {
	keys := []string{"name"}
	switch provider {
	case "github":
		keys = []string{"name", "login"}
	case "yandex":
		keys = []string{"real_name", "display_name", "login"}
	}
	for _, k := range keys {
		s, _ := m[k].(string)
		s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
		if s != "" {
			return s
		}
	}
	return ""
}

func (s *Server) externalBase(r *http.Request) string {
	scheme := "http"
	if s.opts.CookieSecure {
		scheme = "https"
	}
	if peer, err := netip.ParseAddrPort(r.RemoteAddr); err == nil && s.trusted(peer.Addr()) {
		switch strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))) {
		case "https", "http":
			scheme = strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")))
		}
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host
}

func (s *Server) setOAuthCookie(w http.ResponseWriter, value string, maxAge int) {
	// Lax, not Strict: the provider sends the browser back with a top-level GET.
	http.SetCookie(w, &http.Cookie{
		Name: oauthCookie, Value: value, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.opts.CookieSecure, MaxAge: maxAge,
	})
}

func (s *Server) clearOAuthCookie(w http.ResponseWriter) {
	s.setOAuthCookie(w, "", -1)
}

func (s *Server) oauthRedirect(w http.ResponseWriter, r *http.Request, purpose, code string) {
	path := "/login"
	if purpose == "link" {
		path = "/account"
	}
	if code != "" {
		path += "?oauth=" + url.QueryEscape(code)
	}
	http.Redirect(w, r, path, http.StatusFound)
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
