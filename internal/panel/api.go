package panel

import (
	"context"
	"encoding/json"
	"io"
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

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "некорректный JSON")
		return false
	}
	return true
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	id, ok, err := s.store.Authenticate(r.Context(), strings.TrimSpace(body.Username), body.Password)
	if err != nil {
		s.log.Error("login", "err", err)
		writeErr(w, http.StatusInternalServerError, "Панель не смогла проверить пароль.")
		return
	}
	if !ok {
		writeErr(w, http.StatusUnauthorized, "Неверный логин или пароль.")
		return
	}
	cookie, _, err := s.store.CreateSession(r.Context(), id)
	if err != nil {
		s.log.Error("session", "err", err)
		writeErr(w, http.StatusInternalServerError, "Сессия не создана.")
		return
	}
	_ = s.store.RecordAudit(r.Context(), body.Username, "login", map[string]string{"user": body.Username})
	setCookie(w, r, cookie, 12*60*60)
	writeJSON(w, http.StatusOK, map[string]string{"user": body.Username})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("dnsmarty"); err == nil {
		_ = s.store.DeleteSession(r.Context(), c.Value)
	}
	setCookie(w, r, "", -1)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	sess, ok := sessionUser(r, s.store)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "нужен вход")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"user": sess.Username})
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	o, err := s.store.Overview(r.Context())
	if err != nil {
		s.log.Error("overview", "err", err)
		writeErr(w, http.StatusInternalServerError, "Сводка не собралась.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"overview": o, "nodes": nodeList(o.Nodes)})
}

func (s *Server) nodes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListNodes(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Список узлов не прочитан.")
		return
	}
	writeJSON(w, http.StatusOK, nodeList(rows))
}

func (s *Server) nodesCreate(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
	var in store.NodeInput
	if !readJSON(w, r, &in) {
		return
	}
	key, node, err := s.store.CreateNode(r.Context(), sess.Username, in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	st, _ := s.store.Settings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"node": withFresh(node), "key": key, "image": st.AgentImage})
}

func (s *Server) nodesUpdate(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
	var in store.NodeInput
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.store.UpdateNode(r.Context(), sess.Username, r.PathValue("id"), in); err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) nodesDelete(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
	id := r.PathValue("id")
	if err := s.store.DeleteNode(r.Context(), sess.Username, id); err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	s.agents.Forget(id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) nodesKey(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
	id := r.PathValue("id")
	key, err := s.store.RotateKey(r.Context(), sess.Username, id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	s.agents.Forget(id)
	writeJSON(w, http.StatusOK, map[string]string{"key": key})
}

func (s *Server) nodesConnect(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), nodeTimeout)
	defer cancel()
	err := s.syncNode(ctx, id)
	detail := map[string]any{"id": id, "ok": err == nil}
	if err := s.store.RecordAudit(r.Context(), sess.Username, "node.check", detail); err != nil {
		s.log.Warn("audit", "err", err)
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) domains(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListDomains(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Список доменов не прочитан.")
		return
	}
	nodes, _ := s.store.ListNodes(r.Context())
	groups, err := s.store.ListGroups(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Список доменов не прочитан.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"domains": rows, "proxies": proxyOnly(nodes), "groups": groups})
}

func (s *Server) domainsCreate(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
	var in store.DomainInput
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.store.CreateDomain(r.Context(), sess.Username, in); err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) domainsUpdate(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
	var in store.DomainInput
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.store.UpdateDomain(r.Context(), sess.Username, r.PathValue("id"), in); err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) domainsDelete(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
	if err := s.store.DeleteDomain(r.Context(), sess.Username, r.PathValue("id")); err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) groupsCreate(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
	var body struct {
		Name    string `json:"name"`
		Comment string `json:"comment"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	id, err := s.store.CreateGroup(r.Context(), sess.Username, body.Name, body.Comment)
	if err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (s *Server) groupsDelete(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
	if err := s.store.DeleteGroup(r.Context(), sess.Username, r.PathValue("id")); err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) clients(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListClients(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Список сетей не прочитан.")
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) clientsCreate(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
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
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) clientsUpdate(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
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
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) clientsDelete(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
	if err := s.store.DeleteClient(r.Context(), sess.Username, r.PathValue("id")); err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	ip := strings.TrimSpace(r.URL.Query().Get("ip"))
	dnsLogs, err := s.store.DNSLogs(r.Context(), q, ip)
	if err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	proxyLogs, err := s.store.ProxyLogs(r.Context(), q, ip)
	if err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dns": dnsLogs, "proxy": proxyLogs})
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Settings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Настройки не прочитаны.")
		return
	}
	ups, err := s.store.ListUpstreams(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Upstream не прочитан.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": st, "upstreams": ups})
}

func (s *Server) settingsSave(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
	var st store.Settings
	if !readJSON(w, r, &st) {
		return
	}
	if err := s.store.SaveSettings(r.Context(), sess.Username, st); err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) upstreamCreate(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
	var body struct {
		Addr string `json:"addr"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.store.CreateUpstream(r.Context(), sess.Username, body.Addr); err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) upstreamUpdate(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
	var body struct {
		Addr    string `json:"addr"`
		Ordinal int    `json:"ordinal"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.store.UpdateUpstream(r.Context(), sess.Username, r.PathValue("id"), body.Addr, body.Ordinal); err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) upstreamDelete(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionUser(r, s.store)
	if err := s.store.DeleteUpstream(r.Context(), sess.Username, r.PathValue("id")); err != nil {
		writeErr(w, http.StatusBadRequest, human(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.Audit(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Журнал аудита не прочитан.")
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func setCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     "dnsmarty",
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
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
