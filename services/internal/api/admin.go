package api

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/version"
)

/* ------------------------------ General settings ---------------------------- */

type instanceSettings struct {
	Name string `json:"name"`
}

// Deployment defaults applied to new resources (each resource can change them afterwards).
type resourceDefaults struct {
	MemoryMB      int     `json:"memoryMb"`
	CPUs          float64 `json:"cpus"`
	HealthSeconds int     `json:"healthSeconds"`
	StopGrace     int     `json:"stopGraceSeconds"`
	RestartPolicy string  `json:"restartPolicy"`
	BuildMode     string  `json:"buildMode"`
}

var builtinDefaults = resourceDefaults{MemoryMB: 512, CPUs: 1, HealthSeconds: 5, StopGrace: 10, RestartPolicy: "always", BuildMode: "auto"}

func (s *Server) instance(ctx context.Context) instanceSettings {
	inst := instanceSettings{Name: "Ignition"}
	s.Store.GetSetting(ctx, "instance", &inst)
	if inst.Name == "" {
		inst.Name = "Ignition"
	}
	return inst
}

func (s *Server) defaults(ctx context.Context) resourceDefaults {
	d := builtinDefaults
	s.Store.GetSetting(ctx, "resource_defaults", &d)
	return d
}

func (s *Server) getGeneral(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	installed, _ := s.Store.InstalledAt(ctx)
	about := map[string]any{
		"version": version.Version, "commit": version.Commit, "builtAt": version.BuiltAt, "dirty": version.Dirty == "true",
		"installedAt": installed, "dashboardUrl": s.publicURL(), "envDashboardUrl": s.PublicURL, "postgres": s.Store.PostgresVersion(ctx),
		"proxyPorts": []int{s.Proxy.HTTPPort, s.Proxy.HTTPSPort},
	}
	if host, err := s.Runtime.Host(ctx); err == nil {
		about["cpus"], about["memoryBytes"] = host.CPUs, host.MemoryBytes
	}
	if v, err := s.Runtime.Version(ctx); err == nil {
		about["docker"], about["os"] = v.Docker, v.OS
	}
	httpx.JSON(w, 200, map[string]any{"instance": s.instance(ctx), "defaults": s.defaults(ctx), "about": about})
	return nil
}

func (s *Server) putGeneral(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in struct {
		Name         string           `json:"name"`
		DashboardURL string           `json:"dashboardUrl"` // "" = use IGNITION_PUBLIC_URL from .env
		Defaults     resourceDefaults `json:"defaults"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 40 {
		f["name"] = "Name it something (up to 40 characters)."
	}
	in.DashboardURL = strings.TrimRight(strings.TrimSpace(in.DashboardURL), "/")
	if in.DashboardURL != "" {
		u, err := url.Parse(in.DashboardURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
			f["dashboardUrl"] = "Enter the address people open, like https://ignition.example.com or http://192.168.1.20:8080."
		}
	}
	d := in.Defaults
	if d.MemoryMB < 64 || d.MemoryMB > 65536 {
		f["memoryMb"] = "Between 64 MB and 64 GB."
	}
	if d.CPUs < 0.1 || d.CPUs > 64 {
		f["cpus"] = "Between 0.1 and 64."
	}
	if d.HealthSeconds < 2 || d.HealthSeconds > 300 {
		f["healthSeconds"] = "Between 2 and 300 seconds."
	}
	if d.StopGrace < 0 || d.StopGrace > 300 {
		f["stopGraceSeconds"] = "Between 0 and 300 seconds."
	}
	if !slices.Contains([]string{"always", "on-failure", "never"}, d.RestartPolicy) {
		f["restartPolicy"] = "Choose a restart policy."
	}
	if d.BuildMode != "auto" && d.BuildMode != "dockerfile" {
		f["buildMode"] = "Choose a builder."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	inst := s.instance(r.Context())
	inst.Name = in.Name
	if err := s.Store.SetSetting(r.Context(), "instance", inst); err != nil {
		return err
	}
	if err := s.Store.SetSetting(r.Context(), "resource_defaults", d); err != nil {
		return err
	}
	if err := s.Store.SetSetting(r.Context(), "dashboard_url", in.DashboardURL); err != nil {
		return err
	}
	s.setPublicURL(in.DashboardURL)
	s.Store.Audit(r.Context(), u, "settings.general", "settings", "general", map[string]any{"name": in.Name, "dashboardUrl": in.DashboardURL}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"instance": inst, "defaults": d, "dashboardUrl": s.publicURL()})
	return nil
}

/* ----------------------------------- Users ---------------------------------- */

const (
	inviteTTL = 7 * 24 * time.Hour
)

var roleOrder = []string{"viewer", "developer", "admin", "owner"}

// canManage: admins manage developers and viewers (and other admins); only owners manage owners or grant ownership.
func canManage(actor *store.User, targetRole string) bool {
	if actor.Role == "owner" {
		return true
	}
	return actor.Role == "admin" && targetRole != "owner"
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	s.sweepMFADeadline(r)
	users, err := s.Store.ListUsers(r.Context())
	if err != nil {
		return err
	}
	invites, err := s.Store.PendingInvites(r.Context())
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"users": users, "invites": invites})
	return nil
}

func (s *Server) userFor(r *http.Request) (*store.User, error) {
	id, err := pathID(r, "User")
	if err != nil {
		return nil, err
	}
	u, err := s.Store.GetUser(r.Context(), id)
	if err != nil {
		return nil, notFound(err, "User")
	}
	return u, nil
}

// lastOwner reports whether changing target away from an active owner would leave none.
func (s *Server) lastOwner(ctx context.Context, target *store.User) bool {
	if target.Role != "owner" || target.Status != "active" {
		return false
	}
	n, _ := s.Store.CountActiveOwners(ctx)
	return n <= 1
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request, actor *store.User) error {
	target, err := s.userFor(r)
	if err != nil {
		return err
	}
	var in struct {
		Role   *string `json:"role"`
		Status *string `json:"status"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if target.ID == actor.ID {
		return httpx.Forbidden("You can't change your own role or suspend yourself. Ask another owner or admin.")
	}
	if !canManage(actor, target.Role) {
		return httpx.Forbidden("Only owners can manage owners.")
	}
	if in.Role != nil {
		if !slices.Contains(roleOrder, *in.Role) {
			return httpx.Invalid(map[string]string{"role": "Choose a role."})
		}
		if *in.Role == "owner" && actor.Role != "owner" {
			return httpx.Forbidden("Only owners can make someone an owner.")
		}
		if *in.Role != "owner" && s.lastOwner(r.Context(), target) {
			return httpx.Conflict("This is the only owner. Make someone else an owner first.")
		}
		if err := s.Store.SetUserRole(r.Context(), target.ID, *in.Role); err != nil {
			return err
		}
		s.Store.Audit(r.Context(), actor, "user.role", "user", target.ID, map[string]any{"email": target.Email, "from": target.Role, "to": *in.Role}, clientIP(r))
		if *in.Role != target.Role && s.policy().RevokeOnRoleChange {
			s.Store.RevokeSessions(r.Context(), target.ID)
		}
	}
	if in.Status != nil {
		if *in.Status != "active" && *in.Status != "suspended" {
			return httpx.Invalid(map[string]string{"status": "Active or suspended."})
		}
		if *in.Status == "suspended" && s.lastOwner(r.Context(), target) {
			return httpx.Conflict("This is the only owner. Make someone else an owner first.")
		}
		if err := s.Store.SetUserStatus(r.Context(), target.ID, *in.Status); err != nil {
			return err
		}
		action := map[string]string{"active": "user.reactivate", "suspended": "user.suspend"}[*in.Status]
		s.Store.Audit(r.Context(), actor, action, "user", target.ID, map[string]any{"email": target.Email}, clientIP(r))
	}
	updated, _ := s.Store.GetUser(r.Context(), target.ID)
	httpx.JSON(w, 200, map[string]any{"user": updated})
	return nil
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, actor *store.User) error {
	target, err := s.userFor(r)
	if err != nil {
		return err
	}
	if target.ID == actor.ID {
		return httpx.Forbidden("You can't remove your own account.")
	}
	if !canManage(actor, target.Role) {
		return httpx.Forbidden("Only owners can remove owners.")
	}
	if s.lastOwner(r.Context(), target) {
		return httpx.Conflict("This is the only owner. Make someone else an owner first.")
	}
	if err := s.Store.DeleteUser(r.Context(), target.ID); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), actor, "user.remove", "user", target.ID, map[string]any{"email": target.Email, "role": target.Role}, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}

func (s *Server) revokeUserSessions(w http.ResponseWriter, r *http.Request, actor *store.User) error {
	target, err := s.userFor(r)
	if err != nil {
		return err
	}
	if target.ID != actor.ID && !canManage(actor, target.Role) {
		return httpx.Forbidden("Only owners can manage owners.")
	}
	n, err := s.Store.RevokeSessions(r.Context(), target.ID)
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), actor, "user.sessions_revoked", "user", target.ID, map[string]any{"email": target.Email, "sessions": n}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"revoked": n})
	return nil
}

// linkFor builds the absolute link people open; tokens are shown once and only their hash is stored.
func (s *Server) linkFor(r *http.Request, token string) string {
	base := strings.TrimRight(s.publicURL(), "/")
	if base == "" {
		base = "http://" + r.Host
	}
	return base + "/join/" + token
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request, actor *store.User) error {
	switch p := s.policy(); {
	case p.Invitations == "disabled":
		return httpx.Forbidden("Invitations are turned off in Authentication & Security.")
	case p.Invitations == "admins" && !can(actor, "users.invite"):
		return httpx.Forbidden("Only people who manage users can invite others.")
	}
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Email = strings.TrimSpace(in.Email)
	if _, err := mail.ParseAddress(in.Email); err != nil || strings.Contains(in.Email, "<") {
		return httpx.Invalid(map[string]string{"email": "Enter a valid email address."})
	}
	if !slices.Contains(roleOrder, in.Role) {
		return httpx.Invalid(map[string]string{"role": "Choose a role."})
	}
	if in.Role == "owner" && actor.Role != "owner" {
		return httpx.Forbidden("Only owners can invite owners.")
	}
	// Members who don't manage users can only invite people with at most their own role.
	if !can(actor, "users.invite") && roleRank[in.Role] > roleRank[actor.Role] {
		return httpx.Forbidden("You can invite people with your role or below.")
	}
	if _, err := s.Store.UserByEmail(r.Context(), in.Email); err == nil {
		return httpx.Invalid(map[string]string{"email": "Someone with this email already has an account."})
	}
	token, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	t, err := s.Store.CreateUserToken(r.Context(), "invite", hash, in.Email, &in.Role, nil, actor.ID, inviteTTL)
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), actor, "user.invite", "user", "", map[string]any{"email": in.Email, "role": in.Role}, clientIP(r))
	httpx.JSON(w, 201, map[string]any{"invite": t, "url": s.linkFor(r, token)})
	return nil
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request, actor *store.User) error {
	id, err := pathID(r, "Invite")
	if err != nil {
		return err
	}
	email, err := s.Store.RevokeInvite(r.Context(), id)
	if err != nil {
		return notFound(err, "Invite")
	}
	s.Store.Audit(r.Context(), actor, "user.invite_revoked", "user", "", map[string]any{"email": email}, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}

func (s *Server) createResetLink(w http.ResponseWriter, r *http.Request, actor *store.User) error {
	target, err := s.userFor(r)
	if err != nil {
		return err
	}
	if target.ID != actor.ID && !canManage(actor, target.Role) {
		return httpx.Forbidden("Only owners can reset an owner's password.")
	}
	token, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	t, err := s.Store.CreateUserToken(r.Context(), "reset", hash, target.Email, nil, &target.ID, actor.ID, time.Duration(s.policy().ResetExpiryMinutes)*time.Minute)
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), actor, "user.reset_link", "user", target.ID, map[string]any{"email": target.Email}, clientIP(r))
	httpx.JSON(w, 201, map[string]any{"url": s.linkFor(r, token), "expiresAt": t.ExpiresAt})
	return nil
}

// Public: what a /join/<token> link is for. Rate limited like sign-in.
func (s *Server) inspectLink(w http.ResponseWriter, r *http.Request) error {
	if !s.limiter.allow("link:" + clientIP(r)) {
		return httpx.TooMany("Too many attempts. Wait a minute.")
	}
	t, err := s.Store.TokenByHash(r.Context(), auth.HashToken(r.PathValue("token")))
	if err != nil {
		return httpx.NotFound("This link has expired or was already used. Ask for a new one")
	}
	p := s.policy()
	out := map[string]any{"kind": t.Kind, "email": t.Email, "expiresAt": t.ExpiresAt, "instance": s.instance(r.Context()).Name,
		"passwordLength": p.PasswordLength, "passwordAllowed": t.Kind == "reset" || (p.LocalAuth && !p.DisableRegistration && p.Invitations != "disabled"),
		"sso": p.OIDCAuth && s.oidcConfig(r.Context()).Configured(), "ssoLabel": s.oidcConfig(r.Context()).label()}
	if t.Role != nil {
		out["role"] = *t.Role
	}
	httpx.JSON(w, 200, out)
	return nil
}

// Public: finish an invite (create the account) or a reset (set a new password), then sign in.
func (s *Server) acceptLink(w http.ResponseWriter, r *http.Request) error {
	if !s.limiter.allow("link:" + clientIP(r)) {
		return httpx.TooMany("Too many attempts. Wait a minute.")
	}
	var in struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	t, err := s.Store.TokenByHash(r.Context(), auth.HashToken(r.PathValue("token")))
	if err != nil {
		return httpx.NotFound("This link has expired or was already used. Ask for a new one")
	}
	p := s.policy()
	if t.Kind == "invite" && p.Invitations == "disabled" {
		return httpx.Forbidden("Invitations are turned off on this Ignition.")
	}
	if t.Kind == "invite" && (p.DisableRegistration || !p.LocalAuth) {
		return httpx.Forbidden("New accounts can't be created with a password here. Sign in with single sign-on using " + t.Email + ".")
	}
	if msg := s.checkPassword(r.Context(), in.Password); msg != "" {
		return httpx.Invalid(map[string]string{"password": msg})
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return err
	}
	var user *store.User
	switch t.Kind {
	case "invite":
		name := strings.TrimSpace(in.Name)
		if name == "" || len(name) > 80 {
			return httpx.Invalid(map[string]string{"name": "Enter your name."})
		}
		user, err = s.Store.AcceptInvite(r.Context(), t.ID, t.Email, name, hash, *t.Role)
		if store.IsUnique(err) {
			return httpx.Conflict("An account with this email already exists. Sign in instead.")
		}
		if err != nil {
			return httpx.NotFound("This link has expired or was already used. Ask for a new one")
		}
		s.Store.Audit(r.Context(), user, "user.joined", "user", user.ID, map[string]any{"email": user.Email, "role": user.Role}, clientIP(r))
	case "reset":
		if err := s.Store.ResetPassword(r.Context(), t.ID, *t.UserID, hash); err != nil {
			return httpx.NotFound("This link has expired or was already used. Ask for a new one")
		}
		if user, err = s.Store.GetUser(r.Context(), *t.UserID); err != nil {
			return err
		}
		s.Store.Audit(r.Context(), user, "user.password_reset", "user", user.ID, nil, clientIP(r))
	}
	if user.Status != "active" {
		return httpx.Forbidden("This account is suspended.")
	}
	if t.Kind == "reset" {
		return s.secondFactor(w, r, user, "password")
	}
	return s.secondFactor(w, r, user, "invite")
}

/* ----------------------------------- Audit ---------------------------------- */

func auditFilter(r *http.Request) store.AuditFilter {
	q := r.URL.Query()
	f := store.AuditFilter{Query: strings.TrimSpace(q.Get("q")), Actor: q.Get("actor"), Action: q.Get("action"), Target: q.Get("target")}
	if t, err := time.Parse(time.RFC3339, q.Get("since")); err == nil {
		f.Since = &t
	}
	if t, err := time.Parse(time.RFC3339, q.Get("until")); err == nil {
		f.Until = &t
	}
	f.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	return f
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	f := auditFilter(r)
	f.Limit = 100
	events, err := s.Store.SearchAudit(r.Context(), f)
	if err != nil {
		return err
	}
	var ids []string
	for _, e := range events {
		if e.TargetID != "" {
			ids = append(ids, e.TargetID)
		}
	}
	targets, err := s.Store.TargetNames(r.Context(), ids)
	if err != nil {
		return err
	}
	out := map[string]any{"events": events, "targets": targets}
	if len(events) == f.Limit {
		out["nextBefore"] = events[len(events)-1].ID
	}
	if f.Before == 0 {
		actors, actions, _ := s.Store.AuditFacets(r.Context())
		out["actors"], out["actions"] = actors, actions
	}
	httpx.JSON(w, 200, out)
	return nil
}

// exportAudit streams matching events as CSV (up to 50,000).
func (s *Server) exportAudit(w http.ResponseWriter, r *http.Request, u *store.User) error {
	f := auditFilter(r)
	f.Limit = 50000
	events, err := s.Store.SearchAudit(r.Context(), f)
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "audit.export", "audit", "", map[string]any{"events": len(events)}, clientIP(r))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="ignition-audit-%s.csv"`, time.Now().UTC().Format("20060102-150405")))
	cw := csv.NewWriter(w)
	cw.Write([]string{"id", "time", "actor", "action", "target_type", "target_id", "ip", "detail"})
	for _, e := range events {
		cw.Write([]string{strconv.FormatInt(e.ID, 10), e.CreatedAt.UTC().Format(time.RFC3339), csvSafe(e.ActorEmail), csvSafe(e.Action), csvSafe(e.TargetType), csvSafe(e.TargetID), csvSafe(e.IP), csvSafe(string(e.Detail))})
	}
	cw.Flush()
	return cw.Error()
}

// csvSafe stops spreadsheet apps from treating a cell as a formula.
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@", rune(v[0])) {
		return "'" + v
	}
	return v
}

/* -------------------------------- Permissions -------------------------------- */

// putUserPermissions sets grants and denials on top of a user's role. You can't change your own,
// only owners change owners', and you can only grant what you have yourself.
func (s *Server) putUserPermissions(w http.ResponseWriter, r *http.Request, actor *store.User) error {
	target, err := s.userFor(r)
	if err != nil {
		return err
	}
	var in store.PermOverrides
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if target.ID == actor.ID {
		return httpx.Forbidden("You can't change your own permissions. Ask another admin.")
	}
	if !canManage(actor, target.Role) {
		return httpx.Forbidden("Only owners can manage owners.")
	}
	seen := map[string]bool{}
	for _, list := range [][]string{in.Grant, in.Deny} {
		for _, k := range list {
			p, ok := permissionByKey[k]
			switch {
			case !ok || !p.Grantable:
				return httpx.Invalid(map[string]string{"permissions": "Unknown permission: " + k})
			case seen[k]:
				return httpx.Invalid(map[string]string{"permissions": p.Label + " is both granted and denied."})
			}
			seen[k] = true
		}
	}
	for _, k := range in.Grant {
		if !can(actor, k) {
			return httpx.Forbidden("You can't grant " + strings.ToLower(permissionByKey[k].Label) + ": you don't have it yourself.")
		}
	}
	if in.Grant == nil {
		in.Grant = []string{}
	}
	if in.Deny == nil {
		in.Deny = []string{}
	}
	if err := s.Store.SetUserPermissions(r.Context(), target.ID, in); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), actor, "user.permissions", "user", target.ID, map[string]any{"email": target.Email, "grant": in.Grant, "deny": in.Deny,
		"before": target.Permissions}, clientIP(r))
	if s.policy().RevokeOnRoleChange {
		s.Store.RevokeSessions(r.Context(), target.ID)
	}
	updated, _ := s.Store.GetUser(r.Context(), target.ID)
	httpx.JSON(w, 200, map[string]any{"user": updated, "permissions": effectivePermissions(updated)})
	return nil
}

func (s *Server) listPermissions(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	httpx.JSON(w, 200, map[string]any{"permissions": permissionCatalog, "roles": roleOrder})
	return nil
}
