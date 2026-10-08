package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
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
	writeJSON(w, http.StatusOK, map[string]any{"overview": o, "nodes": nodeList(o.Nodes)})
}

func (s *Server) nodes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListNodes(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "Список узлов не прочитан.")
		return
	}
	writeJSON(w, http.StatusOK, nodeList(rows))
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
	writeJSON(w, http.StatusOK, map[string]any{"node": withFresh(node), "key": key, "image": st.AgentImage})
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
	writeJSON(w, http.StatusOK, rows)
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
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	ip := strings.TrimSpace(r.URL.Query().Get("ip"))
	dnsLogs, err := s.store.DNSLogs(r.Context(), q, ip)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	proxyLogs, err := s.store.ProxyLogs(r.Context(), q, ip)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dns": dnsLogs, "proxy": proxyLogs})
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
	rows, err := s.store.Audit(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "Журнал аудита не прочитан.")
		return
	}
	writeJSON(w, http.StatusOK, rows)
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

type nodeView struct {
	store.Node
	Fresh bool `json:"fresh"`
}

func withFresh(n store.Node) nodeView {
	return nodeView{Node: n, Fresh: n.Fresh(time.Now())}
}

func nodeList(rows []store.Node) []nodeView {
	out := make([]nodeView, 0, len(rows))
	for _, n := range rows {
		out = append(out, withFresh(n))
	}
	return out
}

func proxyOnly(rows []store.Node) []nodeView {
	out := []nodeView{}
	for _, n := range rows {
		if n.Role == snapshot.RoleProxy {
			out = append(out, withFresh(n))
		}
	}
	return out
}
