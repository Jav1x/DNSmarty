package panel

import (
	"embed"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"dnsmarty/internal/store"
	"dnsmarty/scripts"
)

//go:embed all:dist
var distFS embed.FS

type Server struct {
	store *store.Store
	log   *slog.Logger
}

func New(st *store.Store, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{store: st, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /install/node.sh", s.installScript)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.withAuth(s.logout))
	mux.HandleFunc("GET /api/me", s.withAuth(s.me))
	mux.HandleFunc("GET /api/overview", s.withAuth(s.overview))
	mux.HandleFunc("GET /api/nodes", s.withAuth(s.nodes))
	mux.HandleFunc("POST /api/nodes", s.withAuth(s.nodesCreate))
	mux.HandleFunc("POST /api/nodes/{id}/connect", s.withAuth(s.nodesConnect))
	mux.HandleFunc("POST /api/nodes/{id}/key", s.withAuth(s.nodesKey))
	mux.HandleFunc("DELETE /api/nodes/{id}", s.withAuth(s.nodesDelete))
	mux.HandleFunc("POST /api/nodes/{id}", s.withAuth(s.nodesUpdate))
	mux.HandleFunc("GET /api/domains", s.withAuth(s.domains))
	mux.HandleFunc("POST /api/domains", s.withAuth(s.domainsCreate))
	mux.HandleFunc("DELETE /api/domains/{id}", s.withAuth(s.domainsDelete))
	mux.HandleFunc("POST /api/domains/{id}", s.withAuth(s.domainsUpdate))
	mux.HandleFunc("POST /api/groups", s.withAuth(s.groupsCreate))
	mux.HandleFunc("DELETE /api/groups/{id}", s.withAuth(s.groupsDelete))
	mux.HandleFunc("GET /api/clients", s.withAuth(s.clients))
	mux.HandleFunc("POST /api/clients", s.withAuth(s.clientsCreate))
	mux.HandleFunc("DELETE /api/clients/{id}", s.withAuth(s.clientsDelete))
	mux.HandleFunc("POST /api/clients/{id}", s.withAuth(s.clientsUpdate))
	mux.HandleFunc("GET /api/logs", s.withAuth(s.logs))
	mux.HandleFunc("GET /api/settings", s.withAuth(s.settings))
	mux.HandleFunc("POST /api/settings", s.withAuth(s.settingsSave))
	mux.HandleFunc("POST /api/upstreams", s.withAuth(s.upstreamCreate))
	mux.HandleFunc("DELETE /api/upstreams/{id}", s.withAuth(s.upstreamDelete))
	mux.HandleFunc("POST /api/upstreams/{id}", s.withAuth(s.upstreamUpdate))
	mux.HandleFunc("GET /api/audit", s.withAuth(s.audit))
	mux.HandleFunc("/", s.spa)
	return mux
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
		http.NotFound(w, r)
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

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("dnsmarty")
		if err != nil || c.Value == "" {
			writeErr(w, http.StatusUnauthorized, "нужен вход")
			return
		}
		if _, err := s.store.LookupSession(r.Context(), c.Value); err != nil {
			writeErr(w, http.StatusUnauthorized, "нужен вход")
			return
		}
		next(w, r)
	}
}

func sessionUser(r *http.Request, st *store.Store) (store.Session, bool) {
	c, err := r.Cookie("dnsmarty")
	if err != nil {
		return store.Session{}, false
	}
	sess, err := st.LookupSession(r.Context(), c.Value)
	if err != nil {
		return store.Session{}, false
	}
	return sess, true
}

func human(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, store.ErrConflict) {
		return "Такая запись уже есть."
	}
	if errors.Is(err, store.ErrNotFound) {
		return "Запись не найдена."
	}
	if errors.Is(err, store.ErrInvalid) {
		return "Проверьте поле: " + strings.TrimPrefix(err.Error(), "invalid: ")
	}
	return "Не удалось сохранить."
}
