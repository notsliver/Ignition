package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Access to one resource from outside the dashboard:
 *   - a deploy hook: a secret URL that deploys when POSTed to (CI, or a GitHub/GitLab push webhook);
 *   - resource tokens: Bearer tokens for scripts and CI, limited to this resource and to reading
 *     (status, deployments, logs) or also deploying.
 * People's access comes from their role in Users & Permissions; it applies to every resource.
 */

const (
	hookPrefix       = "igh_"
	tokenPrefix      = "ign_"
	tokenActorPrefix = "token:"
)

func newSecret(prefix string) (string, []byte) {
	raw := make([]byte, 24)
	rand.Read(raw)
	tok := prefix + base64.RawURLEncoding.EncodeToString(raw)
	return tok, auth.HashToken(tok)
}

func (s *Server) hookURL(token string) string {
	return strings.TrimRight(s.publicURL(), "/") + "/api/hooks/" + token
}

var roleCan = map[string]string{"owner": "manage", "admin": "manage", "developer": "deploy", "viewer": "view"}

func (s *Server) getAccess(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	users, err := s.Store.ListUsers(r.Context())
	if err != nil {
		return err
	}
	type person struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Email  string `json:"email"`
		Role   string `json:"role"`
		Can    string `json:"can"`
		Status string `json:"status"`
	}
	people := []person{}
	for _, u := range users {
		people = append(people, person{u.ID, u.Name, u.Email, u.Role, roleCan[u.Role], u.Status})
	}
	tokens, err := s.Store.ListTokens(r.Context(), id)
	if err != nil {
		return err
	}
	var hook *store.ResourceToken
	list := []store.ResourceToken{}
	for i := range tokens {
		if tokens[i].Kind == "hook" {
			hook = &tokens[i]
		} else {
			list = append(list, tokens[i])
		}
	}
	httpx.JSON(w, 200, map[string]any{"people": people, "hook": hook, "tokens": list, "branch": res.Branch, "sourceType": res.SourceType,
		"apiBase": strings.TrimRight(s.publicURL(), "/") + "/api"})
	return nil
}

// createHook makes (or replaces) the resource's deploy hook and returns its URL.
func (s *Server) createHook(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	if _, err := s.Store.GetResource(r.Context(), id); err != nil {
		return notFound(err, "Resource")
	}
	tok, hash := newSecret(hookPrefix)
	sealed, err := s.Secrets.Seal(tok, "hook:"+id)
	if err != nil {
		return err
	}
	if _, err := s.Store.CreateToken(r.Context(), store.ResourceToken{ResourceID: id, Kind: "hook", Name: "Deploy hook", Scope: "deploy", Hint: tok[len(tok)-4:], Sealed: sealed}, hash, u.ID); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "resource.hook_create", "resource", id, nil, clientIP(r))
	httpx.JSON(w, 201, map[string]string{"url": s.hookURL(tok)})
	return nil
}

func (s *Server) revealHook(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	tokens, err := s.Store.ListTokens(r.Context(), id)
	if err != nil {
		return err
	}
	for _, t := range tokens {
		if t.Kind == "hook" {
			tok, err := s.Secrets.Open(t.Sealed, "hook:"+id)
			if err != nil {
				return httpx.Conflict("The hook can't be decrypted (was IGNITION_SECRET_KEY changed?). Regenerate it.")
			}
			s.Store.Audit(r.Context(), u, "resource.hook_reveal", "resource", id, nil, clientIP(r))
			httpx.JSON(w, 200, map[string]string{"url": s.hookURL(tok)})
			return nil
		}
	}
	return httpx.NotFound("Deploy hook")
}

func (s *Server) deleteHook(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	if _, err := s.Store.DB.Exec(r.Context(), `DELETE FROM resource_tokens WHERE resource_id = $1 AND kind = 'hook'`, id); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "resource.hook_delete", "resource", id, nil, clientIP(r))
	w.WriteHeader(204)
	return nil
}

func (s *Server) createResourceToken(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	if _, err := s.Store.GetResource(r.Context(), id); err != nil {
		return notFound(err, "Resource")
	}
	var in struct {
		Name          string `json:"name"`
		Scope         string `json:"scope"`
		ExpiresInDays int    `json:"expiresInDays"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	f := map[string]string{}
	if in.Name == "" || len(in.Name) > 60 {
		f["name"] = "Name the token after where it's used, e.g. GitHub Actions."
	}
	if in.Scope != "read" && in.Scope != "deploy" {
		f["scope"] = "Choose read only or deploy."
	}
	if in.ExpiresInDays < 0 || in.ExpiresInDays > 3650 {
		f["expiresInDays"] = "Up to 10 years, or never."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	var expires *time.Time
	if in.ExpiresInDays > 0 {
		t := time.Now().Add(time.Duration(in.ExpiresInDays) * 24 * time.Hour).UTC()
		expires = &t
	}
	tok, hash := newSecret(tokenPrefix)
	tokenID, err := s.Store.CreateToken(r.Context(), store.ResourceToken{ResourceID: id, Kind: "token", Name: in.Name, Scope: in.Scope, Hint: tok[len(tok)-4:], ExpiresAt: expires}, hash, u.ID)
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "resource.token_create", "resource", id, map[string]any{"name": in.Name, "scope": in.Scope}, clientIP(r))
	t, _ := s.Store.GetToken(r.Context(), tokenID)
	httpx.JSON(w, 201, map[string]any{"token": tok, "item": t})
	return nil
}

func (s *Server) deleteResourceToken(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Token")
	if err != nil {
		return err
	}
	t, err := s.Store.GetToken(r.Context(), id)
	if err != nil || t.Kind != "token" {
		return httpx.NotFound("Token")
	}
	if err := s.Store.DeleteToken(r.Context(), id); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "resource.token_revoke", "resource", t.ResourceID, map[string]any{"name": t.Name}, clientIP(r))
	w.WriteHeader(204)
	return nil
}

/* --------------------------------- The hook --------------------------------- */

// pushInfo is what GitHub and GitLab push webhooks (and our own JSON) tell us.
type pushInfo struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	HeadCommit *struct {
		ID      string `json:"id"`
		Message string `json:"message"`
		Author  struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"head_commit"`
	Commits []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	} `json:"commits"`
	Reason string `json:"reason"`
}

// deployHook is POST /api/hooks/{token}: no session, the secret URL is the credential.
func (s *Server) deployHook(w http.ResponseWriter, r *http.Request) error {
	tok := r.PathValue("token")
	if !strings.HasPrefix(tok, hookPrefix) || len(tok) > 100 {
		return httpx.NotFound("Hook")
	}
	if !s.limiter.allow("hook:" + tok[len(tok)-8:]) {
		return httpx.TooMany("Too many deploys from this hook. Wait a minute.")
	}
	t, err := s.Store.ResourceTokenByHash(r.Context(), auth.HashToken(tok))
	if errors.Is(err, store.ErrNotFound) || (err == nil && t.Kind != "hook") {
		return httpx.NotFound("Hook")
	}
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), t.ResourceID)
	if err != nil {
		return notFound(err, "Resource")
	}
	s.Store.TouchToken(r.Context(), t.ID, clientIP(r))

	var push pushInfo
	body, _ := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if len(strings.TrimSpace(string(body))) > 0 && strings.Contains(r.Header.Get("Content-Type"), "json") {
		json.Unmarshal(body, &push)
	}
	event := r.Header.Get("X-GitHub-Event") + r.Header.Get("X-Gitlab-Event")
	skip := func(why string) error {
		httpx.JSON(w, 202, map[string]any{"deployed": false, "skipped": why})
		return nil
	}
	switch {
	case event == "ping":
		httpx.JSON(w, 200, map[string]any{"ok": true, "message": hookPingMessage(res)})
		return nil
	case event != "" && event != "push" && event != "Push Hook":
		return skip("Ignored the " + event + " event; only pushes deploy.")
	case strings.HasPrefix(push.Ref, "refs/tags/"):
		return skip("Ignored a tag push.")
	case strings.Trim(push.After, "0") == "" && push.After != "":
		return skip("Ignored a deleted branch.")
	case strings.HasPrefix(push.Ref, "refs/heads/") && res.SourceType == "git" && strings.TrimPrefix(push.Ref, "refs/heads/") != res.Branch:
		return skip(fmt.Sprintf("Push to %s; %s deploys from %s.", strings.TrimPrefix(push.Ref, "refs/heads/"), res.Name, res.Branch))
	}

	// Several pushes in a row become one deployment while it's still waiting to start.
	var queuedID string
	var queuedNumber int
	s.Store.DB.QueryRow(r.Context(), `SELECT id, number FROM deployments WHERE resource_id = $1 AND status = 'queued' ORDER BY number DESC LIMIT 1`, res.ID).Scan(&queuedID, &queuedNumber)
	if queuedID != "" {
		httpx.JSON(w, 202, map[string]any{"deployed": true, "deploymentId": queuedID, "number": queuedNumber, "message": "A deployment was already waiting to start; it will pick up this change."})
		return nil
	}

	reason := "Deploy hook"
	switch {
	case push.Reason != "":
		reason = truncate(push.Reason, 200)
	case push.HeadCommit != nil:
		reason = fmt.Sprintf("Push: %s (%.7s)", firstLine(push.HeadCommit.Message), push.HeadCommit.ID)
	case len(push.Commits) > 0:
		c := push.Commits[len(push.Commits)-1]
		reason = fmt.Sprintf("Push: %s (%.7s)", firstLine(c.Message), c.ID)
	}
	tx, err := s.Store.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	creator := ""
	if t.CreatorID != nil {
		creator = *t.CreatorID
	}
	depID, err := s.Store.CreateDeployment(r.Context(), tx, res.ID, "hook", "", res.Branch, reason, creator)
	if err != nil {
		return err
	}
	if _, err := jobs.Enqueue(r.Context(), tx, jobs.NewJob{Kind: jobs.KindDeploy, ResourceID: res.ID, DeploymentID: depID, CreatedBy: creator}); err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), &store.User{Email: "deploy hook"}, "deployment.hook", "resource", res.ID, map[string]any{"deployment": depID, "reason": reason}, clientIP(r))
	d, _ := s.Store.GetDeployment(r.Context(), depID)
	httpx.JSON(w, 202, map[string]any{"deployed": true, "deploymentId": depID, "number": d.Number})
	return nil
}

func hookPingMessage(res *store.Resource) string {
	if res.SourceType == "git" {
		return fmt.Sprintf("Hook works. Pushes to %s deploy %s; other branches are ignored.", res.Branch, res.Name)
	}
	return fmt.Sprintf("Hook works. Each call pulls %s and deploys %s.", res.Image, res.Name)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return truncate(line, 120)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

/* ------------------------------ Token requests ------------------------------ */

// tokenRoutes are the only endpoints a resource token can call, with the scope each needs.
var tokenRoutes = map[string]string{
	"GET /api/resources/{id}":              "read",
	"GET /api/resources/{id}/deployments":  "read",
	"GET /api/resources/{id}/logs":         "read",
	"GET /api/deployments/{id}":            "read",
	"GET /api/deployments/{id}/logs":       "read",
	"POST /api/resources/{id}/deployments": "deploy",
	"POST /api/deployments/{id}/cancel":    "deploy",
}

// tokenUser authenticates a Bearer resource token for this request, or explains why not.
func (s *Server) tokenUser(r *http.Request, raw string) (*store.User, error) {
	t, err := s.Store.ResourceTokenByHash(r.Context(), auth.HashToken(raw))
	if errors.Is(err, store.ErrNotFound) || (err == nil && t.Kind != "token") {
		return nil, httpx.Unauthorized()
	}
	if err != nil {
		return nil, err
	}
	if t.ExpiresAt != nil && time.Now().After(*t.ExpiresAt) {
		return nil, &httpx.Error{Status: 401, Code: "token_expired", Message: "This token expired. Create a new one under Access & Secrets."}
	}
	need, ok := tokenRoutes[r.Pattern]
	if !ok {
		return nil, httpx.Forbidden("Resource tokens can read status, deployments and logs, and deploy. This endpoint needs a signed-in user.")
	}
	if need == "deploy" && t.Scope != "deploy" {
		return nil, httpx.Forbidden("This token is read only.")
	}
	resourceID := r.PathValue("id")
	if strings.HasPrefix(r.Pattern, "GET /api/deployments/") || strings.HasPrefix(r.Pattern, "POST /api/deployments/") {
		d, err := s.Store.GetDeployment(r.Context(), resourceID)
		if err != nil {
			return nil, httpx.NotFound("Deployment")
		}
		resourceID = d.ResourceID
	}
	if resourceID != t.ResourceID {
		return nil, httpx.Forbidden("This token only works for its own resource.")
	}
	s.Store.TouchToken(r.Context(), t.ID, clientIP(r))
	role := map[string]string{"read": "viewer", "deploy": "developer"}[t.Scope]
	id := ""
	if t.CreatorID != nil {
		id = *t.CreatorID // deployments and the audit log show who created the token
	}
	return &store.User{ID: id, Email: tokenActorPrefix + t.Name, Name: t.Name, Role: role, Status: "active"}, nil
}
