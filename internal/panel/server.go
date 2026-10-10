package panel

import (
	"embed"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"dnsmarty/internal/agent"
	"dnsmarty/internal/store"
	"dnsmarty/scripts"
)

//go:embed all:dist
var distFS embed.FS

type Server struct {
	store   *store.Store
	log     *slog.Logger
	agents  *agent.Pool
	opts    Options
	limiter *loginLimiter
}

func New(st *store.Store, log *slog.Logger, opts Options) *Server {
	if log == nil {
		log = slog.Default()
	}
	if opts.TrustedProxies == nil {
		opts.TrustedProxies = DefaultTrustedProxies()
	}
	return &Server{store: st, log: log, agents: agent.NewPool(), opts: opts, limiter: newLoginLimiter()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /install/node.sh", s.installScript)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/login/totp", s.loginTOTP)
	mux.HandleFunc("GET /api/auth/providers", s.oauthProviders)
	mux.HandleFunc("GET /api/auth/oauth/{provider}/login", s.oauthLogin)
	mux.HandleFunc("GET /api/auth/oauth/{provider}/callback", s.oauthCallback)
	mux.HandleFunc("GET /api/settings/oauth", s.withAuth(s.oauthSettings))
	mux.HandleFunc("POST /api/settings/oauth", s.withAuth(s.oauthSettingsSave))
	mux.HandleFunc("GET /api/account/oauth", s.withAuth(s.oauthAccount))
	mux.HandleFunc("GET /api/account/oauth/{provider}/link", s.withAuth(s.oauthLink))
	mux.HandleFunc("DELETE /api/account/oauth/{provider}", s.withAuth(s.oauthUnlink))
	mux.HandleFunc("POST /api/logout", s.withAuth(s.logout))
	mux.HandleFunc("GET /api/me", s.withAuth(s.me))
	mux.HandleFunc("POST /api/password", s.withAuth(s.passwordChange))
	mux.HandleFunc("POST /api/account/2fa/setup", s.withAuth(s.totpSetup))
	mux.HandleFunc("POST /api/account/2fa/enable", s.withAuth(s.totpEnable))
	mux.HandleFunc("POST /api/account/2fa/disable", s.withAuth(s.totpDisable))
	mux.HandleFunc("GET /api/account/2fa/codes", s.withAuth(s.totpCodes))
	mux.HandleFunc("POST /api/account/2fa/codes/{id}", s.withAuth(s.totpCodeUse))
	mux.HandleFunc("GET /api/sessions", s.withAuth(s.sessions))
	mux.HandleFunc("POST /api/sessions/revoke-others", s.withAuth(s.sessionsRevokeOthers))
	mux.HandleFunc("DELETE /api/sessions/{id}", s.withAuth(s.sessionsDelete))
	mux.HandleFunc("GET /api/overview", s.withAuth(s.overview))
	mux.HandleFunc("GET /api/overview/series", s.withAuth(s.overviewSeries))
	mux.HandleFunc("GET /api/nodes", s.withAuth(s.nodes))
	mux.HandleFunc("PUT /api/nodes/order", s.withAuth(s.nodesReorder))
	mux.HandleFunc("POST /api/nodes", s.withAuth(s.nodesCreate))
	mux.HandleFunc("POST /api/nodes/{id}/connect", s.withAuth(s.nodesConnect))
	mux.HandleFunc("POST /api/nodes/{id}/key", s.withAuth(s.nodesKey))
	mux.HandleFunc("DELETE /api/nodes/{id}", s.withAuth(s.nodesDelete))
	mux.HandleFunc("POST /api/nodes/{id}", s.withAuth(s.nodesUpdate))
	mux.HandleFunc("GET /api/services", s.withAuth(s.services))
	mux.HandleFunc("POST /api/services", s.withAuth(s.servicesCreate))
	mux.HandleFunc("POST /api/services/{id}", s.withAuth(s.servicesUpdate))
	mux.HandleFunc("DELETE /api/services/{id}", s.withAuth(s.servicesDelete))
	mux.HandleFunc("POST /api/services/{id}/members", s.withAuth(s.membersAdd))
	mux.HandleFunc("POST /api/members/{id}", s.withAuth(s.membersUpdate))
	mux.HandleFunc("DELETE /api/members/{id}", s.withAuth(s.membersDelete))
	mux.HandleFunc("GET /api/templates", s.withAuth(s.templates))
	mux.HandleFunc("POST /api/templates", s.withAuth(s.templatesCreate))
	mux.HandleFunc("DELETE /api/templates/{id}", s.withAuth(s.templatesDelete))
	mux.HandleFunc("GET /api/clients", s.withAuth(s.clients))
	mux.HandleFunc("POST /api/clients", s.withAuth(s.clientsCreate))
	mux.HandleFunc("POST /api/clients/kinds", s.withAuth(s.clientsKinds))
	mux.HandleFunc("DELETE /api/clients/{id}", s.withAuth(s.clientsDelete))
	mux.HandleFunc("POST /api/clients/{id}", s.withAuth(s.clientsUpdate))
	mux.HandleFunc("GET /api/logs", s.withAuth(s.logs))
	mux.HandleFunc("GET /api/stats", s.withAuth(s.stats))
	mux.HandleFunc("GET /api/stats/acl", s.withAuth(s.statsACL))
	mux.HandleFunc("GET /api/stats/client", s.withAuth(s.statsClient))
	mux.HandleFunc("GET /api/settings", s.withAuth(s.settings))
	mux.HandleFunc("POST /api/settings", s.withAuth(s.settingsSave))
	mux.HandleFunc("POST /api/upstreams", s.withAuth(s.upstreamCreate))
	mux.HandleFunc("DELETE /api/upstreams/{id}", s.withAuth(s.upstreamDelete))
	mux.HandleFunc("POST /api/upstreams/{id}", s.withAuth(s.upstreamUpdate))
	mux.HandleFunc("GET /api/audit", s.withAuth(s.auditLog))
	mux.HandleFunc("/", s.spa)
	return s.observe(s.secureHeaders(mux))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		http.Error(w, "db", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) installScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=install-node.sh")
	_, _ = w.Write(scripts.NodeSH)
}

func (s *Server) spa(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeErr(w, http.StatusNotFound, "not_found", "No such API method.")
		return
	}
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		http.Error(w, "ui", http.StatusInternalServerError)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	f, err := sub.Open(path)
	if err != nil {
		f, err = sub.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		path = "index.html"
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		b, err := io.ReadAll(f)
		if err != nil {
			http.Error(w, "ui", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", contentType(path))
		_, _ = w.Write(b)
		return
	}
	w.Header().Set("Content-Type", contentType(path))
	http.ServeContent(w, r, path, st.ModTime(), rs)
}

func contentType(path string) string {
	switch {
	case strings.HasSuffix(path, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(path, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(path, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(path, ".svg"):
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}
