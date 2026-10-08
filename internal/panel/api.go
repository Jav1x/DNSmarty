package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dnsmarty/internal/snapshot"
	"dnsmarty/internal/store"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// apiError is every error body. code is stable for the UI; error is a Russian message kept for
// older clients; field names the invalid input when code is "invalid".
type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
	Field string `json:"field,omitempty"`
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Error: msg, Code: code})
}

// fail maps a store error to a response. Unexpected errors are logged and not shown.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrInvalid):
		field := strings.TrimSpace(strings.TrimPrefix(err.Error(), store.ErrInvalid.Error()+":"))
		writeJSON(w, http.StatusBadRequest, apiError{Error: "Проверьте поле: " + field, Code: "invalid", Field: field})
	case errors.Is(err, store.ErrConflict):
		writeErr(w, http.StatusConflict, "conflict", "Такая запись уже есть.")
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "Запись не найдена.")
	default:
		s.log.Error("api", "id", requestID(r.Context()), "path", r.URL.Path, "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "Не удалось сохранить.")
	}
}

// readJSON accepts exactly one JSON object with known fields, up to 1 MiB.
// Requiring application/json also keeps plain HTML forms from other sites out.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeErr(w, http.StatusUnsupportedMediaType, "content_type", "Нужен Content-Type: application/json.")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil || dec.More() {
		writeErr(w, http.StatusBadRequest, "bad_json", "некорректный JSON")
		return false
	}
	return true
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeErr(w, http.StatusForbidden, "csrf", "Запрос отклонён: обновите страницу.")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	user := strings.TrimSpace(body.Username)
	ip := s.clientIP(r).String()
	if wait := s.limiter.blocked(ip, user); wait > 0 {
		w.Header().Set("Retry-After", retryAfter(wait))
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "Слишком много попыток. Попробуйте позже.")
		return
	}
	id, ok, err := s.store.Authenticate(r.Context(), user, body.Password)
	if err != nil {
		s.log.Error("login", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "Панель не смогла проверить пароль.")
		return
	}
	if !ok {
		s.limiter.fail(ip, user)
		s.audit(r, clip(user, 64), "login.fail", map[string]string{"ip": ip})
		writeErr(w, http.StatusUnauthorized, "bad_credentials", "Неверный логин или пароль.")
		return
	}
	s.limiter.success(ip)
	cookie, csrf, err := s.store.CreateSession(r.Context(), id, ip, r.UserAgent())
	if err != nil {
		s.log.Error("session", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "Сессия не создана.")
		return
	}
	s.audit(r, user, "login", map[string]string{"ip": ip})
	s.setCookie(w, cookie, int(store.SessionTTL.Seconds()))
	writeJSON(w, http.StatusOK, map[string]string{"user": user, "csrf": csrf})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := s.store.DeleteSession(r.Context(), sess.Cookie); err != nil {
		s.log.Warn("logout", "err", err)
	}
	s.audit(r, sess.Username, "logout", nil)
	s.setCookie(w, "", -1)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{
		"user": sess.Username, "csrf": sess.CSRF, "session_id": sess.ID, "version": s.opts.Version,
	})
}

func (s *Server) passwordChange(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	err := s.store.ChangePassword(r.Context(), sess, body.Current, body.Next)
	switch {
	case errors.Is(err, store.ErrWrongPassword):
		writeErr(w, http.StatusBadRequest, "wrong_password", "Текущий пароль неверен.")
	case errors.Is(err, store.ErrWeakPassword):
		writeErr(w, http.StatusBadRequest, "weak_password",
			fmt.Sprintf("Новый пароль: от %d до %d байт и не равен текущему.", store.MinPassword, store.MaxPassword))
	case err != nil:
		s.fail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	rows, err := s.store.ListSessions(r.Context(), sess.UserID, sess.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) sessionsDelete(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := s.store.DeleteSessionByID(r.Context(), sess.Username, sess.UserID, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) sessionsRevokeOthers(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	n, err := s.store.DeleteOtherSessions(r.Context(), sess.Username, sess.UserID, sess.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revoked": n})
}

// audit records an event; a failed write is logged, not shown to the user.
func (s *Server) audit(r *http.Request, actor, action string, detail any) {
	if err := s.store.RecordAudit(r.Context(), actor, action, detail); err != nil {
		s.log.Warn("audit", "action", action, "err", err)
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	o, err := s.store.Overview(r.Context())
	if err != nil {
		s.log.Error("overview", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "Сводка не собралась.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"overview": o, "nodes": o.Nodes})
}

func (s *Server) nodes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListNodes(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "Список узлов не прочитан.")
		return
	}
	st, _ := s.store.Settings(r.Context())
	window := store.LiveWindowSec(st.PullIntervalSec)
	out := make([]nodeView, len(rows))
	for i, n := range rows {
		nu := n.AgentVersion != "" && n.AgentVersion != s.opts.Version
		out[i] = nodeView{Node: n, NeedsUpdate: nu, LiveWindowSec: window}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) nodesCreate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var in store.NodeInput
	if !readJSON(w, r, &in) {
		return
	}
	key, node, err := s.store.CreateNode(r.Context(), sess.Username, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	st, _ := s.store.Settings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"node": node, "key": key, "image": st.AgentImage})
}

func (s *Server) nodesUpdate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var in store.NodeInput
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.store.UpdateNode(r.Context(), sess.Username, r.PathValue("id"), in); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) nodesDelete(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id := r.PathValue("id")
	if err := s.store.DeleteNode(r.Context(), sess.Username, id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.agents.Forget(id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) nodesKey(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id := r.PathValue("id")
	key, err := s.store.RotateKey(r.Context(), sess.Username, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.agents.Forget(id)
	writeJSON(w, http.StatusOK, map[string]string{"key": key})
}

func (s *Server) nodesConnect(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), nodeTimeout)
	defer cancel()
	err := s.syncNode(ctx, id)
	s.audit(r, sess.Username, "node.check", map[string]any{"id": id, "ok": err == nil})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) domains(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListDomains(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "Список доменов не прочитан.")
		return
	}
	nodes, _ := s.store.ListNodes(r.Context())
	groups, err := s.store.ListGroups(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "Список доменов не прочитан.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"domains": rows, "proxies": proxyOnly(nodes), "groups": groups})
}

func (s *Server) domainsCreate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var in store.DomainInput
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.store.CreateDomain(r.Context(), sess.Username, in); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) domainsUpdate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var in store.DomainInput
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.store.UpdateDomain(r.Context(), sess.Username, r.PathValue("id"), in); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) domainsDelete(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := s.store.DeleteDomain(r.Context(), sess.Username, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) groupsCreate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		Name    string `json:"name"`
		Comment string `json:"comment"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	id, err := s.store.CreateGroup(r.Context(), sess.Username, body.Name, body.Comment)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (s *Server) groupsDelete(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := s.store.DeleteGroup(r.Context(), sess.Username, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) clients(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListClients(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "Список сетей не прочитан.")
		return
	}
	allowOn, denyOn, err := s.store.ListKinds(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "Список сетей не прочитан.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows":          rows,
		"allow_enabled": allowOn,
		"deny_enabled":  denyOn,
	})
}

// clientsKinds toggles whole lists on or off. The change reaches the nodes on the next push.
func (s *Server) clientsKinds(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		AllowEnabled bool `json:"allow_enabled"`
		DenyEnabled  bool `json:"deny_enabled"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.store.SetListKinds(r.Context(), sess.Username, body.AllowEnabled, body.DenyEnabled); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) clientsCreate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		CIDR    string `json:"cidr"`
		Label   string `json:"label"`
		Kind    string `json:"list_kind"`
		Enabled bool   `json:"enabled"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.store.CreateClient(r.Context(), sess.Username, body.CIDR, body.Label, body.Kind, body.Enabled); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) clientsUpdate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		CIDR    string `json:"cidr"`
		Label   string `json:"label"`
		Kind    string `json:"list_kind"`
		Enabled bool   `json:"enabled"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.store.UpdateClient(r.Context(), sess.Username, r.PathValue("id"), body.CIDR, body.Label, body.Kind, body.Enabled); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) clientsDelete(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := s.store.DeleteClient(r.Context(), sess.Username, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := strings.TrimSpace(q.Get("q"))
	ip := strings.TrimSpace(q.Get("ip"))
	kind := strings.TrimSpace(q.Get("kind"))
	switch kind {
	case "dns":
		rows, next, err := s.store.DNSLogs(r.Context(), name, ip, strings.TrimSpace(q.Get("decision")), parsePage(r, 100))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "next": next})
	case "proxy":
		rows, next, err := s.store.ProxyLogs(r.Context(), name, ip, strings.TrimSpace(q.Get("status")), parsePage(r, 100))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "next": next})
	default:
		// No kind: the first page of each table, so the current UI keeps working.
		dns, _, err := s.store.DNSLogs(r.Context(), name, ip, "", parsePage(r, 200))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		proxy, _, err := s.store.ProxyLogs(r.Context(), name, ip, "", parsePage(r, 200))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"dns": map[string]any{"rows": dns}, "proxy": map[string]any{"rows": proxy}})
	}
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Settings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "Настройки не прочитаны.")
		return
	}
	ups, err := s.store.ListUpstreams(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "Upstream не прочитан.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": st, "upstreams": ups})
}

func (s *Server) settingsSave(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var st store.Settings
	if !readJSON(w, r, &st) {
		return
	}
	if err := s.store.SaveSettings(r.Context(), sess.Username, st); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) upstreamCreate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		Addr string `json:"addr"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.store.CreateUpstream(r.Context(), sess.Username, body.Addr); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) upstreamUpdate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		Addr    string `json:"addr"`
		Ordinal int    `json:"ordinal"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.store.UpdateUpstream(r.Context(), sess.Username, r.PathValue("id"), body.Addr, body.Ordinal); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) upstreamDelete(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := s.store.DeleteUpstream(r.Context(), sess.Username, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) auditLog(w http.ResponseWriter, r *http.Request) {
	rows, next, err := s.store.Audit(r.Context(), parsePage(r, 200))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "next": next})
}

// parsePage reads the keyset cursor from the query string. def is the default page size.
func parsePage(r *http.Request, def int) store.Page {
	q := r.URL.Query()
	limit := def
	if raw := q.Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	p := store.Page{Limit: limit}
	if raw := q.Get("before"); raw != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			p.Before = t
		}
	}
	if id, err := strconv.ParseInt(q.Get("before_id"), 10, 64); err == nil {
		p.BeforeID = id
	}
	return p
}

// seriesStep picks the chart resolution: one minute for short windows, coarser for a day.
func seriesStep(window time.Duration) time.Duration {
	switch {
	case window <= time.Hour:
		return time.Minute
	case window <= 6*time.Hour:
		return 5 * time.Minute
	default:
		return 15 * time.Minute
	}
}

func (s *Server) overviewSeries(w http.ResponseWriter, r *http.Request) {
	windows := map[string]time.Duration{
		"1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour,
	}
	window, ok := windows[strings.TrimSpace(r.URL.Query().Get("window"))]
	if !ok {
		window = time.Hour
	}
	step := seriesStep(window)
	points, err := s.store.Series(r.Context(), window, step)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"step_sec": int(step.Seconds()),
		"points":   points,
	})
}

// setCookie writes the session cookie. Secure comes from configuration, never from a request header
// a client could forge.
func (s *Server) setCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.opts.CookieSecure,
		MaxAge:   maxAge,
	})
}

func proxyOnly(rows []store.Node) []store.Node {
	out := []store.Node{}
	for _, n := range rows {
		if n.Role == snapshot.RoleProxy {
			out = append(out, n)
		}
	}
	return out
}

// nodeView adds version and liveness hints the UI cannot compute from the raw row.
type nodeView struct {
	store.Node
	NeedsUpdate   bool `json:"needs_update"`
	LiveWindowSec int  `json:"live_window_sec"`
}
