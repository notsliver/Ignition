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
	"sync/atomic"
	"time"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/proxy"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/secrets"
	"github.com/notsliver/Ignition/services/internal/servers"
	"github.com/notsliver/Ignition/services/internal/store"
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
	Proxy         *proxy.Client
	Servers       *servers.Registry // Docker clients per server; Local is the socket client

	dashboardURL atomic.Pointer[string] // set in Settings › General; overrides PublicURL
	dashboardOne sync.Once

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
	h := func(pattern string, fn httpx.Handler) {
		mux.Handle(pattern, httpx.Handler(func(w http.ResponseWriter, r *http.Request) error { return domainError(fn(w, r)) }))
	}
	authed := func(pattern, minRole string, fn func(http.ResponseWriter, *http.Request, *store.User) error) {
		mux.Handle(pattern, s.requireUser(minRole, func(w http.ResponseWriter, r *http.Request, u *store.User) error { return domainError(fn(w, r, u)) }))
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
	h("GET /api/links/{token}", s.inspectLink)
	h("POST /api/links/{token}", s.acceptLink)
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
	authed("GET /api/resources/{id}/terminal", "developer", s.terminal)
	authed("GET /api/resources/{id}/logs/deployments", "viewer", s.logSources)
	authed("GET /api/resources/{id}/stats", "viewer", s.containerStats)
	authed("GET /api/resources/{id}/metrics", "viewer", s.resourceMetrics)
	authed("GET /api/deployments/{id}", "viewer", s.getDeployment)
	authed("GET /api/deployments/{id}/logs", "viewer", s.deploymentLogs)
	authed("POST /api/deployments/{id}/cancel", "developer", s.cancelDeployment)
	authed("GET /api/audit", "admin", s.listAudit)
	authed("GET /api/audit/export", "admin", s.exportAudit)
	authed("GET /api/settings/general", "admin", s.getGeneral)
	authed("PUT /api/settings/general", "admin", s.putGeneral)
	authed("GET /api/settings/updates", "admin", s.getUpdates)
	authed("PUT /api/settings/updates", "admin", s.putUpdates)
	authed("POST /api/settings/updates/check", "admin", s.checkUpdates)
	authed("POST /api/settings/updates/apply", "admin", s.applyUpdate)
	authed("POST /api/settings/updates/dismiss", "admin", s.dismissUpdate)
	authed("GET /api/users", "admin", s.listUsers)
	authed("PATCH /api/users/{id}", "admin", s.updateUser)
	authed("DELETE /api/users/{id}", "admin", s.deleteUser)
	authed("POST /api/users/{id}/sessions/revoke", "admin", s.revokeUserSessions)
	authed("POST /api/users/{id}/reset-link", "admin", s.createResetLink)
	authed("POST /api/invites", "admin", s.createInvite)
	authed("DELETE /api/invites/{id}", "admin", s.revokeInvite)
	authed("GET /api/resources/{id}/source", "viewer", s.resourceSource)
	authed("GET /api/resources/{id}/limits", "viewer", s.getLimits)
	authed("PUT /api/resources/{id}/limits", "developer", s.putLimits)
	authed("GET /api/resources/{id}/storage", "viewer", s.getStorage)
	authed("PUT /api/resources/{id}/storage", "developer", s.putStorage)
	authed("DELETE /api/resources/{id}/storage/{volume}", "admin", s.deleteVolume)
	authed("GET /api/resources/{id}/storage/{volume}/backup", "admin", s.backupVolume)
	authed("GET /api/resources/{id}/build", "viewer", s.getBuild)
	authed("PUT /api/resources/{id}/build", "developer", s.putBuild)
	authed("POST /api/resources/{id}/build/inspect", "developer", s.inspectBuild)
	authed("GET /api/resources/{id}/networking", "viewer", s.getNetworking)
	authed("PUT /api/resources/{id}/networking", "developer", s.putNetworking)
	authed("POST /api/resources/{id}/networking/port-check", "developer", s.checkHostPort)
	authed("POST /api/resources/{id}/diagnostics", "developer", s.runDiagnostic)
	authed("GET /api/resources/{id}/domains", "viewer", s.listDomains)
	authed("POST /api/resources/{id}/domains", "developer", s.createDomain)
	authed("PATCH /api/domains/{id}", "developer", s.updateDomain)
	authed("DELETE /api/domains/{id}", "developer", s.deleteDomain)
	authed("POST /api/domains/{id}/check", "viewer", s.checkDomainHandler)
	authed("GET /api/sources", "viewer", s.listSources)
	authed("POST /api/sources", "admin", s.createSource)
	authed("GET /api/sources/{id}", "viewer", s.getSource)
	authed("PATCH /api/sources/{id}", "admin", s.updateSource)
	authed("DELETE /api/sources/{id}", "admin", s.deleteSource)
	authed("POST /api/sources/{id}/test", "developer", s.testSourceHandler)
	authed("GET /api/sources/{id}/repositories", "developer", s.sourceRepositories)
	authed("GET /api/resources/{id}/health", "viewer", s.getHealth)
	authed("PUT /api/resources/{id}/health", "developer", s.putHealth)
	authed("POST /api/resources/{id}/health/test", "developer", s.testHealth)
	authed("GET /api/resources/{id}/releases", "viewer", s.listReleases)
	authed("GET /api/deployments/{id}/env-diff", "viewer", s.envDiff)
	authed("GET /api/resources/{id}/tasks", "viewer", s.listTasks)
	authed("POST /api/resources/{id}/tasks", "developer", s.createTask)
	authed("PATCH /api/tasks/{id}", "developer", s.updateTask)
	authed("DELETE /api/tasks/{id}", "developer", s.deleteTask)
	authed("POST /api/tasks/{id}/run", "developer", s.runTask)
	authed("GET /api/tasks/{id}/runs", "viewer", s.listTaskRuns)
	authed("GET /api/task-runs/{id}", "viewer", s.getTaskRun)
	authed("POST /api/task-runs/{id}/cancel", "developer", s.cancelTaskRun)
	authed("GET /api/cron/preview", "viewer", s.cronPreview)
	authed("GET /api/resources/{id}/access", "viewer", s.getAccess)
	authed("POST /api/resources/{id}/hook", "developer", s.createHook)
	authed("POST /api/resources/{id}/hook/reveal", "developer", s.revealHook)
	authed("DELETE /api/resources/{id}/hook", "developer", s.deleteHook)
	authed("POST /api/resources/{id}/tokens", "developer", s.createResourceToken)
	authed("DELETE /api/resource-tokens/{id}", "developer", s.deleteResourceToken)
	h("POST /api/hooks/{token}", s.deployHook)
	authed("GET /api/resources/{id}/advanced", "viewer", s.getAdvanced)
	authed("PUT /api/resources/{id}/advanced", "developer", s.putAdvanced)
	authed("GET /api/resources/{id}/export", "viewer", s.exportResource)
	authed("POST /api/resources/{id}/images/purge", "admin", s.purgeImages)
	authed("POST /api/resources/{id}/move", "admin", s.moveResource)

	// Infrastructure › Servers
	authed("GET /api/servers", "viewer", s.listServers)
	authed("POST /api/servers", "admin", s.createServer)
	authed("POST /api/servers/verify", "admin", s.verifyServer)
	authed("POST /api/servers/bulk", "admin", s.bulkServers)
	authed("GET /api/servers/bundle", "admin", s.serversBundle)
	authed("GET /api/servers/{id}", "viewer", s.getServer)
	authed("PATCH /api/servers/{id}", "admin", s.updateServer)
	authed("DELETE /api/servers/{id}", "admin", s.deleteServer)
	authed("GET /api/servers/{id}/removal", "admin", s.serverRemovalPreview)
	authed("POST /api/servers/{id}/test", "developer", s.testServer)
	authed("POST /api/servers/{id}/refresh", "developer", s.refreshServer)
	authed("PUT /api/servers/{id}/deployments", "admin", s.setServerDeployments)
	authed("GET /api/servers/{id}/maintenance", "viewer", s.getMaintenance)
	authed("POST /api/servers/{id}/maintenance", "admin", s.setServerMaintenance)
	authed("PUT /api/servers/{id}/maintenance/note", "admin", s.putMaintenanceNote)
	authed("GET /api/servers/{id}/events", "viewer", s.listServerEvents)
	authed("GET /api/servers/{id}/jobs", "viewer", s.listServerJobs)
	authed("GET /api/servers/{id}/metrics", "viewer", s.serverMetrics)
	authed("GET /api/servers/{id}/resources", "viewer", s.listServerResources)
	authed("GET /api/servers/{id}/deployments", "viewer", s.listServerDeployments)
	authed("GET /api/servers/{id}/logs", "viewer", s.serverLogs)
	authed("GET /api/servers/{id}/diagnostics", "viewer", s.getServerDiagnostics)
	authed("POST /api/servers/{id}/diagnostics", "developer", s.runServerDiagnostics)
	authed("GET /api/servers/{id}/diagnostics/bundle", "admin", s.serverBundle)
	authed("GET /api/diagnostic-runs/{id}", "viewer", s.getDiagnosticRun)
	authed("GET /api/servers/{id}/docker", "viewer", s.getServerDocker)
	authed("GET /api/servers/{id}/docker/containers", "viewer", s.listServerContainers)
	authed("GET /api/servers/{id}/docker/images", "viewer", s.listServerImages)
	authed("GET /api/servers/{id}/docker/networks", "viewer", s.listServerNetworks)
	authed("GET /api/servers/{id}/docker/volumes", "viewer", s.listServerVolumes)
	authed("GET /api/servers/{id}/docker/inspect", "admin", s.inspectServerObject)
	authed("POST /api/servers/{id}/docker/containers/{cid}/actions", "developer", s.serverContainerAction)
	authed("POST /api/servers/{id}/cleanup/preview", "admin", s.previewServerCleanup)
	authed("POST /api/servers/{id}/cleanup", "admin", s.runServerCleanup)
	authed("GET /api/servers/{id}/storage", "viewer", s.getServerStorage)
	authed("GET /api/servers/{id}/networking", "viewer", s.getServerNetworking)
	authed("GET /api/servers/{id}/tasks", "viewer", s.listServerTasks)
	authed("POST /api/servers/{id}/tasks", "admin", s.createServerTask)
	authed("PATCH /api/server-tasks/{id}", "admin", s.updateServerTask)
	authed("DELETE /api/server-tasks/{id}", "admin", s.deleteServerTask)
	authed("POST /api/server-tasks/{id}/run", "admin", s.runServerTask)
	authed("GET /api/server-tasks/{id}/runs", "viewer", s.listServerTaskRuns)
	authed("GET /api/server-jobs/{id}", "viewer", s.getServerJob)
	authed("POST /api/server-jobs/{id}/cancel", "admin", s.cancelServerJob)
	authed("GET /api/servers/{id}/access", "viewer", s.getServerAccess)
	authed("PUT /api/servers/{id}/credentials", "admin", s.rotateServerCredentials)
	authed("DELETE /api/servers/{id}/credentials", "admin", s.revokeServerCredentials)
	authed("PUT /api/servers/{id}/endpoint", "admin", s.changeServerEndpoint)

	mux.Handle("/api/", httpx.Handler(func(w http.ResponseWriter, r *http.Request) error { return httpx.NotFound("Endpoint") }))
	if s.WebDir != "" {
		mux.Handle("/", s.spa())
	}
	return httpx.WithRequestID(httpx.Recover(securityHeaders(s.checkOrigin(mux))))
}

// domainError turns store-level rule violations into the matching API error.
func domainError(err error) error {
	var blocked *store.DeploymentsBlocked
	if errors.As(err, &blocked) {
		return httpx.Conflict(blocked.Error())
	}
	return err
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed, _ := url.Parse(s.publicURL())
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		// Deploy hooks carry their own secret, and token requests have no cookie a forged request could ride on.
		_, cookieErr := r.Cookie(sessionCookie)
		if strings.HasPrefix(r.URL.Path, "/api/hooks/") || (strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && cookieErr != nil) {
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
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			user, err := s.tokenUser(r, strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")))
			if err != nil {
				return err
			}
			if roleRank[user.Role] < roleRank[minRole] {
				return httpx.Forbidden("This token can't do this.")
			}
			return fn(w, r, user)
		}
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

// publicURL is the address people open the dashboard at: the one saved in Settings › General, else IGNITION_PUBLIC_URL.
// Requests to the address actually in use are always accepted, so changing it can't lock anyone out.
func (s *Server) publicURL() string {
	s.dashboardOne.Do(func() {
		var saved string
		s.Store.GetSetting(context.Background(), "dashboard_url", &saved)
		if saved != "" {
			s.dashboardURL.Store(&saved)
		}
	})
	if v := s.dashboardURL.Load(); v != nil && *v != "" {
		return *v
	}
	return s.PublicURL
}

func (s *Server) setPublicURL(v string) {
	s.dashboardOne.Do(func() {})
	s.dashboardURL.Store(&v)
}
