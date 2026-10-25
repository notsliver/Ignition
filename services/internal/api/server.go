// Package api wires HTTP routes to the store, job queue and runtime.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ignition/ignition/internal/auth"
	"github.com/ignition/ignition/internal/httpx"
	"github.com/ignition/ignition/internal/runtime"
	"github.com/ignition/ignition/internal/secrets"
	"github.com/ignition/ignition/internal/store"
)

const sessionCookie = "ignition_session"
const sessionTTL = 7 * 24 * time.Hour

type Server struct {
	Store         *store.Store
	Runtime       runtime.Runtime
	Secrets       *secrets.Box
	PublicURL     string
	SecureCookies bool
	WebDir        string
	PublishHost   string

	setupMu   sync.Mutex
	setupCode string
	limiter   *limiter
}

// PrepareSetup creates a one-time code for first-run setup when no users exist.
// It's printed to the API logs only, so only someone with server access can claim the instance.
func (s *Server) PrepareSetup(ctx context.Context) error {
	n, err := s.Store.CountUsers(ctx)
	if err != nil || n > 0 {
		return err
	}
	buf := make([]byte, 4)
	rand.Read(buf)
	s.setupCode = strings.ToUpper(hex.EncodeToString(buf))
	slog.Warn("no users yet: open IGNITION in your browser and enter this setup code", "setup_code", s.setupCode)
	return nil
}

func (s *Server) Handler() http.Handler {
	s.limiter = newLimiter(10, time.Minute)
	mux := http.NewServeMux()
	h := func(pattern string, fn httpx.Handler) { mux.Handle(pattern, fn) }
	authed := func(pattern, minRole string, fn func(http.ResponseWriter, *http.Request, *store.User) error) {
		mux.Handle(pattern, s.requireUser(minRole, fn))
	}

	h("GET /api/health", func(w http.ResponseWriter, r *http.Request) error {
		httpx.JSON(w, 200, map[string]string{"status": "ok"})
		return nil
	})
	h("GET /api/ready", s.ready)
	h("GET /api/setup", s.setupStatus)
	h("POST /api/setup", s.setup)
	h("POST /api/auth/login", s.login)
	h("POST /api/auth/logout", s.logout)
	authed("GET /api/auth/me", "viewer", s.me)
	authed("GET /api/system", "viewer", s.system)

	authed("GET /api/projects", "viewer", s.listProjects)
	authed("POST /api/projects", "admin", s.createProject)
	authed("GET /api/projects/{id}", "viewer", s.getProject)
	authed("PATCH /api/projects/{id}", "admin", s.updateProject)
	authed("DELETE /api/projects/{id}", "admin", s.deleteProject)
	authed("PUT /api/projects/{id}/favorite", "viewer", s.favorite(true))
	authed("DELETE /api/projects/{id}/favorite", "viewer", s.favorite(false))

	authed("GET /api/projects/{id}/resources", "viewer", s.listResources)
	authed("POST /api/projects/{id}/resources", "developer", s.createResource)
	authed("GET /api/resources", "viewer", s.listAllResources)
	authed("GET /api/resources/{id}", "viewer", s.getResource)
	authed("PATCH /api/resources/{id}", "developer", s.updateResource)
	authed("DELETE /api/resources/{id}", "admin", s.deleteResource)
	authed("POST /api/resources/{id}/actions", "developer", s.resourceAction)
	authed("GET /api/resources/{id}/env", "viewer", s.getEnv)
	authed("PUT /api/resources/{id}/env", "developer", s.putEnv)
	authed("POST /api/resources/{id}/env/{key}/reveal", "admin", s.revealEnv)
	authed("GET /api/resources/{id}/deployments", "viewer", s.listDeployments)
	authed("POST /api/resources/{id}/deployments", "developer", s.createDeployment)
	authed("GET /api/resources/{id}/logs", "viewer", s.containerLogs)
	authed("GET /api/resources/{id}/stats", "viewer", s.containerStats)
	authed("GET /api/resources/{id}/metrics", "viewer", s.resourceMetrics)
	authed("GET /api/deployments/{id}", "viewer", s.getDeployment)
	authed("GET /api/deployments/{id}/logs", "viewer", s.deploymentLogs)
	authed("POST /api/deployments/{id}/cancel", "developer", s.cancelDeployment)
	authed("GET /api/audit", "admin", s.listAudit)

	mux.Handle("/api/", httpx.Handler(func(w http.ResponseWriter, r *http.Request) error { return httpx.NotFound("Endpoint") }))
	if s.WebDir != "" {
		mux.Handle("/", s.spa())
	}
	return httpx.WithRequestID(httpx.Recover(securityHeaders(s.checkOrigin(mux))))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// checkOrigin is the CSRF defence: state-changing requests must come from our own origin.
// Combined with SameSite=Lax cookies this blocks cross-site form and fetch attacks.
func (s *Server) checkOrigin(next http.Handler) http.Handler {
	allowed, _ := url.Parse(s.PublicURL)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = r.Header.Get("Referer")
		}
		o, err := url.Parse(origin)
		ok := err == nil && origin != "" && (o.Host == r.Host || (allowed != nil && o.Host == allowed.Host))
		if !ok {
			httpx.WriteError(w, r, httpx.Forbidden("Request blocked: it didn't come from the IGNITION dashboard."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

var roleRank = map[string]int{"viewer": 0, "developer": 1, "admin": 2, "owner": 3}

func (s *Server) requireUser(minRole string, fn func(http.ResponseWriter, *http.Request, *store.User) error) http.Handler {
	return httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			return httpx.Unauthorized()
		}
		user, err := s.Store.UserBySession(r.Context(), auth.HashToken(c.Value))
		if errors.Is(err, store.ErrNotFound) {
			return httpx.Unauthorized()
		}
		if err != nil {
			return err
		}
		if roleRank[user.Role] < roleRank[minRole] {
			return httpx.Forbidden("Your role (" + user.Role + ") can't do this. Ask an admin.")
		}
		return fn(w, r, user)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.DB.Ping(ctx); err != nil {
		return httpx.Unavailable("Database unreachable.")
	}
	httpx.JSON(w, 200, map[string]string{"status": "ready"})
	return nil
}

// spa serves the built frontend and falls back to index.html for client-side routes.
func (s *Server) spa() http.Handler {
	files := http.FileServer(http.Dir(s.WebDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean("/" + r.URL.Path)
		if _, err := os.Stat(filepath.Join(s.WebDir, clean)); err != nil {
			http.ServeFile(w, r, filepath.Join(s.WebDir, "index.html"))
			return
		}
		files.ServeHTTP(w, r)
	})
}

/* ------------------------------ Rate limiting ------------------------------ */

// limiter is a fixed-window counter per key. It's in memory, which is fine for a single
// self-hosted API process; account lockout in the database covers distributed guessing.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	recent := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.max {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	return true
}
