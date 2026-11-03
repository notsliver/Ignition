package api

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

func (s *Server) creationProject(r *http.Request, u *store.User, id string) (*store.Project, error) {
	if id != "" {
		if !uuidRE.MatchString(id) {
			return nil, httpx.NotFound("Project")
		}
		p, err := s.Store.GetProject(r.Context(), u.ID, id)
		return p, notFound(err, "Project")
	}
	var capacity bool
	if err := s.Store.SQL(r.Context()).QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM servers WHERE is_local AND NOT removing)`).Scan(&capacity); err != nil {
		return nil, err
	}
	if !capacity {
		return nil, httpx.Conflict("There's no server to deploy to yet. A platform administrator adds servers under Servers.")
	}
	envs, err := s.Store.TeamEnvironments(r.Context())
	if err != nil {
		return nil, err
	}
	defaultID, err := s.Store.DefaultEnvironment(r.Context())
	if err != nil {
		return nil, err
	}
	for i, e := range envs {
		if e.ID == defaultID {
			envs[0], envs[i] = envs[i], envs[0]
			break
		}
	}
	return &store.Project{TeamID: u.TeamID, Name: "No project", Environments: envs}, nil
}

// Global account/platform routes never inherit a team administrator's authority.
func teamRoute(pattern string) bool {
	_, path, _ := strings.Cut(pattern, " ")
	for _, prefix := range []string{"/api/platform/", "/api/auth/", "/api/account/", "/api/setup", "/api/settings/", "/api/users", "/api/invites", "/api/teams", "/api/team-invitations/"} {
		if strings.HasPrefix(path, prefix) {
			return false
		}
	}
	return path != "/api/system" && path != "/api/permissions"
}
func originalPattern(pattern string) string {
	return strings.Replace(pattern, "/api/teams/{teamId}/", "/api/", 1)
}
func projectPermission(key string) bool {
	area, _, _ := strings.Cut(key, ".")
	return slices.Contains([]string{"projects", "resources", "deployments", "variables", "logs", "terminal", "domains", "backups", "files", "storage", "databases", "tasks", "access", "games", "automations", "audit"}, area) && key != "projects.create"
}
func platformOperation(pattern string) bool {
	_, path, _ := strings.Cut(originalPattern(pattern), " ")
	if strings.HasPrefix(path, "/api/servers") {
		return !slices.Contains([]string{"GET /api/servers", "GET /api/servers/{id}", "GET /api/servers/{id}/resources", "GET /api/servers/{id}/deployments"}, originalPattern(pattern))
	}
	return strings.HasPrefix(path, "/api/diagnostic-runs/") || strings.HasPrefix(path, "/api/server-") || strings.HasPrefix(path, "/api/network/") || strings.HasPrefix(path, "/api/firewall-")
}

// infrastructureRoute is what "Infrastructure in Instance administration only" takes away from
// non-admins: servers and their operations, and instance networking. Sources belong to each team and
// stay theirs to manage (Settings › Sources), as do resource and project domains.
func infrastructureRoute(pattern string) bool {
	_, path, _ := strings.Cut(originalPattern(pattern), " ")
	for _, prefix := range []string{"/api/servers", "/api/server-tasks", "/api/server-jobs", "/api/diagnostic-runs", "/api/network/", "/api/firewall-rules"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func (s *Server) scopedRequest(r *http.Request, u *store.User, perm string) (*http.Request, *store.User, error) {
	team := r.PathValue("teamId")
	if team == "" {
		if teamRoute(r.Pattern) {
			return r, u, &httpx.Error{Status: 400, Code: "team_required", Message: "Use the team-scoped API: /api/teams/{teamId}/…"}
		}
		return r, u, nil
	}
	if !uuidRE.MatchString(team) {
		return r, u, httpx.NotFound("Team")
	}
	if u.ServiceResourceID != "" {
		if u.TeamID != team {
			return r, u, httpx.NotFound("Resource")
		}
		if err := s.Store.DB.QueryRow(r.Context(), `SELECT access_version FROM teams WHERE id=$1`, team).Scan(&u.TeamVersion); err != nil {
			return r, u, err
		}
		min := 1
		if p, ok := permissionByKey[perm]; ok {
			min = roleRank[p.Role] + 1
		}
		return r.WithContext(store.WithScope(r.Context(), store.Scope{TeamID: team, Permission: perm, MinRank: min, CredentialResourceID: u.ServiceResourceID})), u, nil
	}
	m, err := s.Store.Membership(r.Context(), team, u.ID)
	adminAccess := false
	// A platform administrator (signed in, not with a token) can work in any team as its owner.
	if errors.Is(err, store.ErrNotFound) && u.CredentialTokenID == "" && roleRank[u.Role] >= roleRank["admin"] {
		if m, err = s.Store.AdminTeam(r.Context(), team); errors.Is(err, store.ErrNotFound) {
			return r, u, httpx.NotFound("Team")
		}
		adminAccess = err == nil
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return r, u, &httpx.Error{Status: 403, Code: "team_access_lost", Message: "You no longer have access to this team."}
		}
		return r, u, err
	}
	if platformOperation(r.Pattern) && roleRank[u.Role] < roleRank["admin"] {
		return r, u, httpx.Forbidden("This operation requires a platform administrator.")
	}
	if roleRank[u.Role] < roleRank["admin"] && infrastructureRoute(r.Pattern) && s.instance(r.Context()).InfrastructureAdminOnly {
		return r, u, httpx.Forbidden("Infrastructure is managed in Instance administration on this instance. Ask a platform administrator.")
	}
	actor := *u
	actor.TeamVersion = m.AccessVersion
	actor.InstanceRole = u.Role
	actor.TeamID = team
	actor.TeamRole = m.Role
	actor.Role = m.Role
	actor.Permissions = m.Overrides
	min := 1
	if p, ok := permissionByKey[perm]; ok {
		min = roleRank[p.Role] + 1
	}
	scope := store.Scope{TeamID: team, UserID: u.ID, Permission: perm, MinRank: min, InstanceAdmin: adminAccess}
	ctx := store.WithScope(r.Context(), scope)
	if adminAccess {
		s.auditAdminAccess(ctx, &actor, m, r)
	}
	// RLS evaluates project assignments per row, including aggregate/list endpoints.
	if perm != "" && !allowed(&actor, perm) {
		if !projectPermission(perm) {
			return r, u, httpx.Forbidden(deniedMessage(&actor, perm))
		}
		var access bool
		if err := s.Store.SQL(ctx).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects)`).Scan(&access); err != nil {
			return r, u, err
		}
		if !access {
			return r, u, httpx.Forbidden(deniedMessage(&actor, perm))
		}
	}
	// For in-handler secondary checks, merge only the assignment of this actual target.
	if id := r.PathValue("id"); uuidRE.MatchString(id) {
		path := strings.TrimPrefix(strings.Split(originalPattern(r.Pattern), " ")[1], "/api/")
		kind, _, _ := strings.Cut(path, "/")
		joins := map[string]string{
			"projects":        "SELECT id FROM projects WHERE id=$1 AND team_id=$2",
			"resources":       "SELECT project_id FROM resources WHERE id=$1 AND team_id=$2",
			"deployments":     "SELECT r.project_id FROM deployments x JOIN resources r ON r.id=x.resource_id WHERE x.id=$1 AND r.team_id=$2",
			"tasks":           "SELECT r.project_id FROM scheduled_tasks x JOIN resources r ON r.id=x.resource_id WHERE x.id=$1 AND r.team_id=$2",
			"task-runs":       "SELECT r.project_id FROM task_runs x JOIN scheduled_tasks t ON t.id=x.task_id JOIN resources r ON r.id=t.resource_id WHERE x.id=$1 AND r.team_id=$2",
			"resource-tokens": "SELECT r.project_id FROM resource_tokens x JOIN resources r ON r.id=x.resource_id WHERE x.id=$1 AND r.team_id=$2",
			"data-imports":    "SELECT r.project_id FROM data_imports x JOIN resources r ON r.id=x.resource_id WHERE x.id=$1 AND r.team_id=$2",
			"automations":     "SELECT r.project_id FROM automations a LEFT JOIN resources r ON r.id=a.resource_id WHERE a.id=$1 AND a.team_id=$2",
			"automation-runs": "SELECT r.project_id FROM automation_runs x JOIN automations a ON a.id=x.automation_id LEFT JOIN resources r ON r.id=a.resource_id WHERE x.id=$1 AND a.team_id=$2",
			"environments":    "SELECT project_id FROM environments WHERE id=$1 AND team_id=$2",
			"domains":         "SELECT r.project_id FROM domains x JOIN resources r ON r.id=x.resource_id WHERE x.id=$1 AND r.team_id=$2",
		}
		if q := joins[kind]; q != "" {
			var project *string
			if err := s.Store.DB.QueryRow(ctx, q, id, team).Scan(&project); err != nil {
				return r, u, httpx.NotFound("Object")
			}
			if project != nil {
				assignments, err := s.Store.ProjectAssignments(ctx, team, *project)
				if err != nil {
					return r, u, err
				}
				for _, a := range assignments {
					if a.UserID == u.ID {
						if roleRank[a.Role] > roleRank[actor.Role] {
							actor.Role = a.Role
						}
						actor.Permissions.Grant = append(append([]string{}, actor.Permissions.Grant...), a.Permissions.Grant...)
						actor.Permissions.Deny = append(append([]string{}, actor.Permissions.Deny...), a.Permissions.Deny...)
					}
				}
			}
		}
	}
	return r.WithContext(ctx), &actor, nil
}

func (s *Server) listTeams(w http.ResponseWriter, r *http.Request, u *store.User) error {
	list, err := s.Store.Teams(r.Context(), u.ID)
	if err != nil {
		return err
	}
	for i := range list {
		actor := &store.User{Role: list[i].Role, Permissions: list[i].Overrides}
		list[i].Permissions = effectivePermissions(actor)
	}
	httpx.JSON(w, 200, map[string]any{"teams": list})
	return nil
}

// createTeam is for platform administrators: a team is a new ownership boundary, and it gets no
// deployment capacity until an administrator assigns a server to it.
func (s *Server) createTeam(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if roleRank[u.Role] < roleRank["admin"] {
		return httpx.Forbidden("Only platform administrators can create teams. Ask one to create a team or invite you to one.")
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		return httpx.Invalid(map[string]string{"name": "Enter a team name (up to 80 characters)."})
	}
	if err := s.Store.CheckOwnedTeams(r.Context(), u.ID); err != nil {
		return err
	}
	t, err := s.Store.CreateTeam(r.Context(), u.ID, in.Name)
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "team.create", "team", t.ID, map[string]any{"name": in.Name}, clientIP(r))
	httpx.JSON(w, 201, map[string]any{"team": t})
	return nil
}
func (s *Server) teamInfo(w http.ResponseWriter, r *http.Request, u *store.User) error {
	t, err := s.Store.Membership(r.Context(), u.TeamID, u.ID)
	if err != nil {
		return notFound(err, "Team")
	}
	t.Permissions = effectivePermissions(u)
	httpx.JSON(w, 200, map[string]any{"team": t})
	return nil
}
func teamAdmin(u *store.User) error {
	if u.TeamRole != "owner" && u.TeamRole != "admin" {
		return httpx.Forbidden("A team administrator is required.")
	}
	return nil
}
func (s *Server) updateTeam(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := teamAdmin(u); err != nil {
		return err
	}
	var in struct {
		Name        string   `json:"name"`
		Experiences []string `json:"experiences"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		return httpx.Invalid(map[string]string{"name": "Enter a team name (up to 80 characters)."})
	}
	for _, e := range in.Experiences {
		if e != "infrastructure" && e != "hosting" {
			return httpx.Invalid(map[string]string{"experiences": "Unknown experience."})
		}
	}
	if !slices.Contains(in.Experiences, "infrastructure") {
		in.Experiences = append(in.Experiences, "infrastructure")
	}
	if err := s.Store.UpdateTeam(r.Context(), u.TeamID, in.Name, in.Experiences); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "team.update", "team", u.TeamID, nil, clientIP(r))
	return s.teamInfo(w, r, u)
}

// teamMembers is readable by everyone on the team; changing membership needs a team administrator.
func (s *Server) teamMembers(w http.ResponseWriter, r *http.Request, u *store.User) error {
	list, err := s.Store.TeamMembers(r.Context(), u.TeamID)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"members": list})
	return nil
}

type assignmentInput struct {
	Role        string              `json:"role"`
	Permissions store.PermOverrides `json:"permissionOverrides"`
}

func validTeamOverrides(p store.PermOverrides) bool {
	for _, key := range append(append([]string{}, p.Grant...), p.Deny...) {
		v, ok := permissionByKey[key]
		if !ok || !v.Grantable || strings.HasPrefix(key, "users.") || strings.HasPrefix(key, "settings.") || key == "security.manage" {
			return false
		}
	}
	return true
}
func (s *Server) putTeamMember(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := teamAdmin(u); err != nil {
		return err
	}
	id, err := pathID(r, "Member")
	if err != nil {
		return err
	}
	var in assignmentInput
	if r.Method != "DELETE" {
		if err := httpx.Decode(r, &in); err != nil {
			return err
		}
		if !slices.Contains([]string{"owner", "admin", "developer", "viewer", "member"}, in.Role) || !validTeamOverrides(in.Permissions) {
			return httpx.Invalid(map[string]string{"role": "Choose a valid team role and permissions."})
		}
	}
	for _, key := range in.Permissions.Grant {
		if !can(u, key) {
			return httpx.Forbidden("You cannot delegate a permission you do not hold.")
		}
	}
	old, err := s.Store.Membership(r.Context(), u.TeamID, id)
	if err != nil {
		return notFound(err, "Member")
	}
	if (old.Role == "owner" || in.Role == "owner") && u.TeamRole != "owner" {
		return httpx.Forbidden("Only a team owner can manage ownership.")
	}
	err = s.Store.SetTeamMember(r.Context(), u.TeamID, id, in.Role, in.Permissions)
	if errors.Is(err, store.ErrLastOwner) {
		return httpx.Conflict(err.Error())
	}
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "team.member_update", "user", id, map[string]any{"role": in.Role}, clientIP(r))
	w.WriteHeader(204)
	return nil
}
func (s *Server) teamEnvironments(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if r.Method == "POST" {
		if err := teamAdmin(u); err != nil {
			return err
		}
		var in struct {
			Name string `json:"name"`
		}
		if err := httpx.Decode(r, &in); err != nil {
			return err
		}
		in.Name = strings.TrimSpace(in.Name)
		if !envNameRE.MatchString(in.Name) {
			return httpx.Invalid(map[string]string{"name": "Enter an environment name (up to 40 characters)."})
		}
		id, err := s.Store.CreateEnvironment(r.Context(), "", in.Name)
		if err != nil {
			return err
		}
		s.Store.Audit(r.Context(), u, "environment.create", "environment", id, nil, clientIP(r))
		httpx.JSON(w, 201, map[string]any{"environment": store.Environment{ID: id, Name: in.Name}})
		return nil
	}
	list, err := s.Store.TeamEnvironments(r.Context())
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"environments": list})
	return nil
}
func (s *Server) assignments(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Project")
	if err != nil {
		return err
	}
	if _, err = s.Store.GetProject(r.Context(), u.ID, id); err != nil {
		return notFound(err, "Project")
	}
	if roleRank[u.Role] < roleRank["admin"] {
		return httpx.Forbidden("A project administrator is required.")
	}
	if r.Method == "GET" {
		list, err := s.Store.ProjectAssignments(r.Context(), u.TeamID, id)
		if err != nil {
			return err
		}
		members, err := s.Store.TeamMembers(r.Context(), u.TeamID)
		if err != nil {
			return err
		}
		httpx.JSON(w, 200, map[string]any{"assignments": list, "members": members})
		return nil
	}
	var in struct {
		UserID string `json:"userId"`
		assignmentInput
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if !uuidRE.MatchString(in.UserID) || (!slices.Contains([]string{"", "admin", "developer", "viewer"}, in.Role)) || !validTeamOverrides(in.Permissions) {
		return httpx.Invalid(map[string]string{"role": "Choose a team member and project role."})
	}
	for _, key := range append(append([]string{}, in.Permissions.Grant...), in.Permissions.Deny...) {
		if !projectPermission(key) {
			return httpx.Invalid(map[string]string{"permissionOverrides": "Project assignments can only contain project-scoped permissions."})
		}
	}
	for _, key := range in.Permissions.Grant {
		if !can(u, key) {
			return httpx.Forbidden("You cannot delegate a permission you do not hold.")
		}
	}
	if _, err = s.Store.Membership(r.Context(), u.TeamID, in.UserID); err != nil {
		return notFound(err, "Member")
	}
	if err = s.Store.SetProjectAssignment(r.Context(), u.TeamID, id, in.UserID, in.Role, in.Permissions); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "project.assignment", "project", id, map[string]any{"userId": in.UserID, "role": in.Role}, clientIP(r))
	w.WriteHeader(204)
	return nil
}
func (s *Server) teamInvitations(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := teamAdmin(u); err != nil {
		return err
	}
	if r.Method == "GET" {
		list, err := s.Store.TeamInvitations(r.Context(), u.TeamID)
		if err != nil {
			return err
		}
		httpx.JSON(w, 200, map[string]any{"invitations": list})
		return nil
	}
	if r.Method == "DELETE" {
		id, err := pathID(r, "Invitation")
		if err != nil {
			return err
		}
		_, err = s.Store.DB.Exec(r.Context(), `DELETE FROM team_invitations WHERE team_id=$1 AND id=$2`, u.TeamID, id)
		if err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	}
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	address, err := mail.ParseAddress(strings.TrimSpace(in.Email))
	if err != nil || !slices.Contains([]string{"admin", "developer", "viewer", "member"}, in.Role) {
		return httpx.Invalid(map[string]string{"email": "Enter an email and valid team role."})
	}
	token, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	if err := s.Store.CheckTeamLimit(r.Context(), u.TeamID, "members"); err != nil {
		return err
	}
	id, err := s.Store.InviteTeamMember(r.Context(), u.TeamID, address.Address, in.Role, u.ID, hash)
	if err != nil {
		return err
	}
	httpx.JSON(w, 201, map[string]any{"id": id, "token": token, "path": "/join-team/" + token})
	return nil
}
func (s *Server) acceptTeamInvitation(w http.ResponseWriter, r *http.Request, u *store.User) error {
	team, err := s.Store.AcceptTeamInvitation(r.Context(), u.ID, u.Email, auth.HashToken(r.PathValue("token")))
	if err != nil {
		return notFound(err, "Invitation")
	}
	httpx.JSON(w, 200, map[string]any{"teamId": team})
	return nil
}

// Physical capacity is assigned by a platform operator, independently of team roles.
func (s *Server) teamCapacity(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if roleRank[u.Role] < roleRank["admin"] {
		return httpx.Forbidden("A platform administrator is required.")
	}
	if r.Method == "GET" {
		// ?team= marks the servers already assigned to that team.
		team := r.URL.Query().Get("team")
		if team != "" && !uuidRE.MatchString(team) {
			return httpx.NotFound("Team")
		}
		rows, err := s.Store.DB.Query(r.Context(), `SELECT s.id,s.name,EXISTS(SELECT 1 FROM team_server_assignments a WHERE a.server_id=s.id AND a.team_id::text=$1) FROM servers s WHERE NOT s.removing ORDER BY s.name`, team)
		if err != nil {
			return err
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, name string
			var assigned bool
			if err = rows.Scan(&id, &name, &assigned); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "name": name, "assigned": assigned})
		}
		if err = rows.Err(); err != nil {
			return err
		}
		httpx.JSON(w, 200, map[string]any{"servers": out})
		return nil
	}
	var in struct {
		TeamID   string `json:"teamId"`
		ServerID string `json:"serverId"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if !uuidRE.MatchString(in.TeamID) || !uuidRE.MatchString(in.ServerID) {
		return httpx.Invalid(map[string]string{"serverId": "Choose a team and server."})
	}
	if _, err := s.Store.DB.Exec(r.Context(), `INSERT INTO team_server_assignments(team_id,server_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, in.TeamID, in.ServerID); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "team.capacity_assign", "team", in.TeamID, map[string]any{"serverId": in.ServerID}, clientIP(r))
	w.WriteHeader(204)
	return nil
}

func (s *Server) groupResource(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := teamAdmin(u); err != nil {
		return err
	}
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	if _, err = s.Store.GetResource(r.Context(), id); err != nil {
		return notFound(err, "Resource")
	}
	var in struct {
		ProjectID *string `json:"projectId"`
	}
	if err = httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.ProjectID != nil && *in.ProjectID != "" {
		if err := s.projectsOff(r.Context()); err != nil {
			return err
		}
		if !uuidRE.MatchString(*in.ProjectID) {
			return httpx.NotFound("Project")
		}
		if _, err = s.Store.GetProject(r.Context(), u.ID, *in.ProjectID); err != nil {
			return notFound(err, "Project")
		}
	} else {
		in.ProjectID = nil
	}
	if _, err = s.Store.SQL(r.Context()).Exec(r.Context(), `UPDATE resources SET project_id=$2,updated_at=now() WHERE id=$1`, id, in.ProjectID); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "resource.group", "resource", id, map[string]any{"projectId": in.ProjectID}, clientIP(r))
	w.WriteHeader(204)
	return nil
}

func (s *Server) watchTeam(ctx context.Context, u *store.User) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	if u.TeamID != "" {
		go func() {
			tick := time.NewTicker(5 * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					if u.CredentialTokenID != "" {
						token, err := s.Store.GetToken(ctx, u.CredentialTokenID)
						if err != nil || (token.ExpiresAt != nil && time.Now().After(*token.ExpiresAt)) {
							cancel()
							return
						}
					}
					if u.ServiceResourceID != "" {
						continue
					}

					m, err := s.Store.Membership(ctx, u.TeamID, u.ID)
					if err != nil || m.AccessVersion != u.TeamVersion {
						cancel()
						return
					}
					current, err := s.Store.GetUser(ctx, u.ID)
					if err != nil || current.Status != "active" {
						cancel()
						return
					}
				}
			}
		}()
	}
	return ctx, cancel
}

func (s *Server) inspectTeamInvitation(w http.ResponseWriter, r *http.Request) error {
	var team, email, role, name string
	err := s.Store.DB.QueryRow(r.Context(), `SELECT i.team_id,i.email,i.role,t.name FROM team_invitations i JOIN teams t ON t.id=i.team_id WHERE i.token_hash=$1 AND i.accepted_at IS NULL AND i.expires_at>now()`, auth.HashToken(r.PathValue("token"))).Scan(&team, &email, &role, &name)
	if err != nil {
		return httpx.NotFound("Invitation")
	}
	httpx.JSON(w, 200, map[string]any{"teamId": team, "teamName": name, "email": email, "role": role})
	return nil
}
func (s *Server) registerTeamInvitation(w http.ResponseWriter, r *http.Request) error {
	if !s.limiter.allow("team-register:" + clientIP(r)) {
		return httpx.TooMany("Too many attempts. Try again later.")
	}
	p := s.policy()
	if p.DisableRegistration || !p.LocalAuth || p.Invitations == "disabled" {
		return httpx.Forbidden("Account registration is disabled. Sign in with your existing account or single sign-on to accept the invitation.")
	}
	var in struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		return httpx.Invalid(map[string]string{"name": "Enter your name (up to 80 characters)."})
	}
	if msg := s.checkPassword(r.Context(), in.Password); msg != "" {
		return httpx.Invalid(map[string]string{"password": msg})
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return err
	}
	user, err := s.Store.RegisterTeamInvitation(r.Context(), auth.HashToken(r.PathValue("token")), in.Name, hash)
	if store.IsUnique(err) {
		return httpx.Conflict("An account with this email already exists. Sign in to accept the invitation.")
	}
	if err != nil {
		return notFound(err, "Invitation")
	}
	return s.secondFactor(w, r, user, "invite")
}

// platformTeams lists every team on the instance with its size, for Instance administration › Teams.
// Platform administrators only; it shows counts, not anyone's workloads.
func (s *Server) platformTeams(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if roleRank[u.Role] < roleRank["admin"] {
		return httpx.Forbidden("A platform administrator is required.")
	}
	rows, err := s.Store.DB.Query(r.Context(), `SELECT t.id,t.name,t.slug,t.created_at,t.icon_file,
		(SELECT count(*) FROM team_memberships m WHERE m.team_id=t.id),
		(SELECT count(*) FROM team_memberships m WHERE m.team_id=t.id AND m.role='owner'),
		(SELECT count(*) FROM projects p WHERE p.team_id=t.id),
		(SELECT count(*) FROM resources x WHERE x.team_id=t.id),
		(SELECT count(*) FROM team_server_assignments a WHERE a.team_id=t.id),
		(SELECT m.role FROM team_memberships m WHERE m.team_id=t.id AND m.user_id=$1),
		ARRAY(SELECT experience FROM team_experiences x WHERE x.team_id=t.id ORDER BY experience)
		FROM teams t ORDER BY t.created_at,t.id`, u.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		Slug        string    `json:"slug"`
		CreatedAt   time.Time `json:"createdAt"`
		Members     int       `json:"members"`
		Owners      int       `json:"owners"`
		Projects    int       `json:"projects"`
		Resources   int       `json:"resources"`
		Servers     int       `json:"servers"`
		IconURL     string    `json:"iconUrl"`
		YourRole    *string   `json:"yourRole"`
		Experiences []string  `json:"experiences"`
	}
	out := []row{}
	for rows.Next() {
		var t row
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.CreatedAt, &t.IconURL, &t.Members, &t.Owners, &t.Projects, &t.Resources, &t.Servers, &t.YourRole, &t.Experiences); err != nil {
			return err
		}
		t.IconURL = store.ImageURL("teams", t.IconURL)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"teams": out})
	return nil
}

var adminAccessSeen sync.Map // user:team -> time.Time of the last audit entry

// auditAdminAccess records that a platform administrator is working in a team they aren't a member
// of: once an hour per team, plus every audited change they make there (marked as such).
func (s *Server) auditAdminAccess(ctx context.Context, u *store.User, t *store.Team, r *http.Request) {
	key := u.ID + ":" + t.ID
	if last, ok := adminAccessSeen.Load(key); ok && time.Since(last.(time.Time)) < time.Hour {
		return
	}
	adminAccessSeen.Store(key, time.Now())
	s.Store.Audit(ctx, u, "team.admin_access", "team", t.ID, map[string]any{"team": t.Name, "as": "owner"}, clientIP(r))
}
