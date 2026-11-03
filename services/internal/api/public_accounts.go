package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

func (s *Server) customer(r *http.Request, id string) (*store.User, error) {
	u, e := s.Store.GetUser(r.Context(), id)
	if e != nil {
		return nil, httpx.NotFound("User")
	}
	if roleRank[u.Role] >= roleRank["admin"] {
		return nil, httpx.Forbidden("Customer endpoints cannot modify instance administrators.")
	}
	return u, nil
}
func (s *Server) customerView(r *http.Request, u *store.User) map[string]any {
	var method string
	var complete *time.Time
	s.Store.SQL(r.Context()).QueryRow(r.Context(), `SELECT method,completed_at FROM admin_onboarding WHERE user_id=$1`, u.ID).Scan(&method, &complete)
	state := "complete"
	if method != "" && complete == nil {
		state = "pending"
	}
	return map[string]any{"id": u.ID, "name": u.Name, "email": u.Email, "role": u.Role, "status": u.Status, "createdAt": u.CreatedAt, "externalId": s.external(r, "user", u.ID), "onboarding": map[string]any{"method": method, "state": state}}
}
func (s *Server) publicAccounts(w http.ResponseWriter, r *http.Request, u *store.User, t *adminToken, a string) error {
	ctx := r.Context()
	id := r.PathValue("id")
	q := r.URL.Query()
	switch a {
	case "users":
		list, err := s.Store.ListUsers(ctx)
		if err != nil {
			return err
		}
		out := []map[string]any{}
		for _, v := range list {
			if roleRank[v.Role] >= roleRank["admin"] {
				continue
			}
			row := s.customerView(r, &v.User)
			if q.Get("status") != "" && q.Get("status") != v.Status || q.Get("role") != "" && q.Get("role") != v.Role || q.Get("externalId") != "" && q.Get("externalId") != row["externalId"] {
				continue
			}
			if search := strings.ToLower(q.Get("q")); search != "" && !strings.Contains(strings.ToLower(v.Name+" "+v.Email), search) {
				continue
			}
			out = append(out, row)
		}
		return publicList(w, r, out)
	case "user-create":
		var in struct {
			Email      string `json:"email"`
			Name       string `json:"name"`
			Role       string `json:"role"`
			ExternalID string `json:"externalId"`
		}
		if err := httpx.Decode(r, &in); err != nil {
			return err
		}
		in.Email = strings.ToLower(strings.TrimSpace(in.Email))
		in.Name = strings.TrimSpace(in.Name)
		f := map[string]string{}
		if addr, e := mail.ParseAddress(in.Email); e != nil || addr.Address != in.Email || len(in.Email) > 254 {
			f["email"] = "Enter a valid email address."
		}
		if in.Name == "" || len(in.Name) > 80 {
			f["name"] = "Enter a name up to 80 characters."
		}
		p := s.policy()
		method := "password"
		if !p.LocalAuth || p.DisableRegistration || p.RequiredMethod == "sso" {
			method = "sso"
			if !p.OIDCAuth || !s.oidcConfig(ctx).Configured() {
				return httpx.Forbidden("Instance policy provides no supported account onboarding method.")
			}
		}
		if in.Role == "" && method == "sso" {
			in.Role = p.SSODefaultRole
		}
		if !slices.Contains([]string{"viewer", "developer"}, in.Role) {
			f["role"] = "Choose viewer or developer. Local accounts require an explicit role."
		}
		if len(f) > 0 {
			return httpx.Invalid(f)
		}
		account, team, err := s.Store.CreateCustomer(ctx, in.Email, in.Name, in.Role)
		if err != nil {
			return err
		}
		if err = s.setExternal(r, "user", account.ID, in.ExternalID); err != nil {
			return err
		}
		if _, err = s.Store.SQL(ctx).Exec(ctx, `INSERT INTO admin_onboarding(user_id,method,created_team_id) VALUES($1,$2,$3)`, account.ID, method, team); err != nil {
			return err
		}
		out := s.customerView(r, account)
		out["createdTeam"] = nil
		if team != nil {
			v, e := s.Store.AdminTeam(ctx, *team)
			if e != nil {
				return e
			}
			createdTeam := publicMap(v)
			var environmentID, environmentName string
			if e = s.Store.SQL(ctx).QueryRow(ctx, `SELECT id,name FROM environments WHERE team_id=$1 AND is_team_default`, *team).Scan(&environmentID, &environmentName); e != nil {
				return e
			}
			createdTeam["defaultEnvironment"] = map[string]string{"id": environmentID, "name": environmentName}
			out["createdTeam"] = createdTeam
		}
		publicCreated(w, out)
		return nil
	case "user", "user-update", "user-suspend", "user-resume", "user-sessions", "user-setup", "user-reset":
		target, err := s.customer(r, id)
		if err != nil {
			return err
		}
		switch a {
		case "user":
			row := s.customerView(r, target)
			teams, e := s.Store.Teams(ctx, id)
			if e != nil {
				return e
			}
			row["teams"] = teams
			publicOK(w, row)
			return nil
		case "user-update":
			var in struct {
				Name       *string `json:"name"`
				ExternalID *string `json:"externalId"`
			}
			if err = httpx.Decode(r, &in); err != nil {
				return err
			}
			if in.Name != nil {
				n := strings.TrimSpace(*in.Name)
				if n == "" || len(n) > 80 {
					return httpx.Invalid(map[string]string{"name": "Enter a name up to 80 characters."})
				}
				if _, err = s.Store.SQL(ctx).Exec(ctx, `UPDATE users SET name=$2 WHERE id=$1`, id, n); err != nil {
					return err
				}
			}
			if in.ExternalID != nil {
				if err = s.setExternal(r, "user", id, *in.ExternalID); err != nil {
					return err
				}
			}
		case "user-suspend", "user-resume":
			if err = publicEmpty(r); err != nil {
				return err
			}
			status := "active"
			if a == "user-suspend" {
				status = "suspended"
			}
			if err = s.Store.SetUserStatus(ctx, id, status); err != nil {
				return err
			}
			if status == "suspended" {
				if _, err = s.Store.RevokeSessions(ctx, id); err != nil {
					return err
				}
			}
		case "user-sessions":
			if err = publicEmpty(r); err != nil {
				return err
			}
			n, e := s.Store.RevokeSessions(ctx, id)
			if e != nil {
				return e
			}
			publicOK(w, map[string]any{"revoked": n})
			return nil
		case "user-setup", "user-reset":
			if err = publicEmpty(r); err != nil {
				return err
			}
			return s.publicSetupLink(w, r, u, target, a == "user-reset")
		}
		target, err = s.Store.GetUser(ctx, id)
		if err != nil {
			return err
		}
		publicOK(w, s.customerView(r, target))
		return nil
	case "invitations":
		list, err := s.Store.PendingInvites(ctx)
		if err != nil {
			return err
		}
		out := []store.UserToken{}
		for _, v := range list {
			if v.Role != nil && slices.Contains([]string{"viewer", "developer"}, *v.Role) {
				out = append(out, v)
			}
		}
		return publicList(w, r, out)
	case "invite":
		var in struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		}
		if err := httpx.Decode(r, &in); err != nil {
			return err
		}
		if !slices.Contains([]string{"viewer", "developer"}, in.Role) {
			return httpx.Invalid(map[string]string{"role": "Choose viewer or developer."})
		}
		out, err := s.publicExisting(r, u, s.createInvite, in)
		if err != nil {
			return err
		}
		publicCreated(w, out)
		return nil
	case "invite-delete":
		var role string
		if err := s.Store.SQL(ctx).QueryRow(ctx, `SELECT role FROM user_tokens WHERE id=$1 AND kind='invite'`, id).Scan(&role); err != nil {
			return httpx.NotFound("Invitation")
		}
		if !slices.Contains([]string{"viewer", "developer"}, role) {
			return httpx.Forbidden("Only customer invitations can be revoked.")
		}
		_, err := s.publicExisting(r, u, s.revokeInvite, nil)
		if err == nil {
			w.WriteHeader(204)
		}
		return err
	case "teams":
		rows, err := s.Store.SQL(ctx).Query(ctx, `SELECT id FROM teams ORDER BY id`)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var v string
			if err = rows.Scan(&v); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, v)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		out := []map[string]any{}
		for _, v := range ids {
			team, e := s.Store.AdminTeam(ctx, v)
			if e != nil {
				return e
			}
			row := publicMap(team)
			row["externalId"] = s.external(r, "team", v)
			if q.Get("externalId") != "" && q.Get("externalId") != row["externalId"] {
				continue
			}
			if search := strings.ToLower(q.Get("q")); search != "" && !strings.Contains(strings.ToLower(team.Name), search) {
				continue
			}
			out = append(out, row)
		}
		return publicList(w, r, out)
	case "team-create":
		var in struct {
			Name       string `json:"name"`
			OwnerID    string `json:"ownerId"`
			ExternalID string `json:"externalId"`
		}
		if err := httpx.Decode(r, &in); err != nil {
			return err
		}
		if !uuidRE.MatchString(in.OwnerID) {
			return httpx.Invalid(map[string]string{"ownerId": "Choose an existing customer."})
		}
		if _, err := s.customer(r, in.OwnerID); err != nil {
			return err
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" || len(in.Name) > 80 {
			return httpx.Invalid(map[string]string{"name": "Up to 80 characters."})
		}
		if err := s.Store.CheckOwnedTeams(ctx, in.OwnerID); err != nil {
			return err
		}
		team, err := s.Store.CreateTeam(ctx, in.OwnerID, in.Name)
		if err != nil {
			return err
		}
		if err = s.setExternal(r, "team", team.ID, in.ExternalID); err != nil {
			return err
		}
		row := publicMap(team)
		row["externalId"] = in.ExternalID
		publicCreated(w, row)
		return nil
	}
	// Team operations use a verified explicit boundary, never a caller-supplied resource team.
	team, err := s.Store.AdminTeam(ctx, id)
	if err != nil {
		return httpx.NotFound("Team")
	}
	switch a {
	case "team":
		row := publicMap(team)
		row["externalId"] = s.external(r, "team", id)
		publicOK(w, row)
		return nil
	case "team-update":
		var in struct {
			Name        *string   `json:"name"`
			Experiences *[]string `json:"experiences"`
			ExternalID  *string   `json:"externalId"`
		}
		if err = httpx.Decode(r, &in); err != nil {
			return err
		}
		name := team.Name
		ex := team.Experiences
		if in.Name != nil {
			name = strings.TrimSpace(*in.Name)
		}
		if in.Experiences != nil {
			ex = *in.Experiences
		}
		if name == "" || len(name) > 80 {
			return httpx.Invalid(map[string]string{"name": "Up to 80 characters."})
		}
		for _, v := range ex {
			if v != "infrastructure" && v != "hosting" {
				return httpx.Invalid(map[string]string{"experiences": "Unknown experience."})
			}
		}
		if !slices.Contains(ex, "infrastructure") {
			ex = append(ex, "infrastructure")
		}
		if err = s.Store.UpdateTeam(ctx, id, name, ex); err != nil {
			return err
		}
		if in.ExternalID != nil {
			if err = s.setExternal(r, "team", id, *in.ExternalID); err != nil {
				return err
			}
		}
		v, err := s.Store.AdminTeam(ctx, id)
		if err != nil {
			return err
		}
		publicOK(w, v)
		return nil
	case "members":
		v, err := s.Store.TeamMembers(ctx, id)
		if err != nil {
			return err
		}
		return publicList(w, r, v)
	case "member-create", "member-update", "member-delete":
		var in struct {
			UserID      string              `json:"userId"`
			Role        string              `json:"role"`
			Permissions store.PermOverrides `json:"permissionOverrides"`
		}
		if a != "member-delete" {
			if err = httpx.Decode(r, &in); err != nil {
				return err
			}
		}
		if a != "member-create" {
			in.UserID = r.PathValue("userId")
		}
		if !uuidRE.MatchString(in.UserID) {
			return httpx.NotFound("User")
		}
		if _, err = s.customer(r, in.UserID); err != nil {
			return err
		}
		if a != "member-delete" && (!slices.Contains([]string{"admin", "developer", "viewer", "member"}, in.Role) || !validTeamOverrides(in.Permissions)) {
			return httpx.Invalid(map[string]string{"role": "Choose a non-owner team role and valid permissions."})
		}
		for _, v := range in.Permissions.Grant {
			if !publicHas(t, v) {
				return httpx.Forbidden("A token cannot delegate permissions it does not hold.")
			}
		}
		old, e := s.Store.Membership(ctx, id, in.UserID)
		if e == nil && old.Role == "owner" {
			return httpx.Forbidden("Owner memberships require the dashboard.")
		}
		if a == "member-create" {
			if e == nil {
				return httpx.Conflict("This person already belongs to the team.")
			}
			if err = s.Store.CheckTeamLimit(ctx, id, "members"); err != nil {
				return err
			}
			_, err = s.Store.SQL(ctx).Exec(ctx, `INSERT INTO team_memberships(team_id,user_id,role,permissions) VALUES($1,$2,$3,$4)`, id, in.UserID, in.Role, encodePublic(in.Permissions))
		} else {
			if e != nil {
				return httpx.NotFound("Membership")
			}
			role := in.Role
			if a == "member-delete" {
				role = ""
			}
			err = s.Store.SetTeamMember(ctx, id, in.UserID, role, in.Permissions)
		}
		if err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	case "team-invitations":
		v, e := s.Store.TeamInvitations(ctx, id)
		if e != nil {
			return e
		}
		return publicList(w, r, v)
	case "team-invite":
		var in struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		}
		if err = httpx.Decode(r, &in); err != nil {
			return err
		}
		if !slices.Contains([]string{"admin", "developer", "viewer", "member"}, in.Role) {
			return httpx.Invalid(map[string]string{"role": "Choose a non-owner team role."})
		}
		out, e := s.callAsTeam(r, u, id, "", "", encodePublic(in), s.teamInvitations)
		if e != nil {
			return e
		}
		publicCreated(w, out)
		return nil
	case "team-invite-delete":
		inv := r.PathValue("invitationId")
		tag, e := s.Store.SQL(ctx).Exec(ctx, `DELETE FROM team_invitations WHERE id=$1 AND team_id=$2`, inv, id)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("Invitation")
		}
		w.WriteHeader(204)
		return nil
	case "team-usage":
		usage, e := s.Store.QuotaUsage(ctx, "team", id)
		if e != nil {
			return e
		}
		fillStorage(&usage, s.volumeSizes(ctx))
		publicOK(w, publicUsage(usage))
		return nil
	}
	return httpx.NotFound("Admin operation")
}
func publicMap(v any) map[string]any {
	out := map[string]any{}
	json.Unmarshal(encodePublic(v), &out)
	return out
}
func publicEmpty(r *http.Request) error { var v struct{}; return httpx.DecodeOptional(r, &v) }
func (s *Server) publicSetupLink(w http.ResponseWriter, r *http.Request, actor, target *store.User, reset bool) error {
	p := s.policy()
	password := p.LocalAuth && p.RequiredMethod != "sso" && (reset || !p.DisableRegistration)
	if reset && !password {
		return httpx.Forbidden("Password setup is disabled by instance authentication policy.")
	}
	var method string
	var done *time.Time
	err := s.Store.SQL(r.Context()).QueryRow(r.Context(), `SELECT method,completed_at FROM admin_onboarding WHERE user_id=$1`, target.ID).Scan(&method, &done)
	if !reset && (err != nil || done != nil) {
		return httpx.Conflict("This account is not awaiting API onboarding.")
	}
	if target.Status != "active" {
		return httpx.Conflict("Reactivate the account before issuing a setup link.")
	}
	if password {
		raw, hash, e := auth.NewToken()
		if e != nil {
			return e
		}
		_, e = s.Store.CreateUserToken(r.Context(), "reset", hash, target.Email, nil, &target.ID, actor.ID, time.Duration(p.ResetExpiryMinutes)*time.Minute)
		if e != nil {
			return e
		}
		publicCreated(w, map[string]any{"url": s.linkFor(r, raw), "method": "password", "expiresInSeconds": p.ResetExpiryMinutes * 60})
		return nil
	}
	if !p.OIDCAuth || !s.oidcConfig(r.Context()).Configured() {
		return httpx.Conflict("No usable SSO provider is configured.")
	}
	raw, hash, e := auth.NewToken()
	if e != nil {
		return e
	}
	_, e = s.Store.SQL(r.Context()).Exec(r.Context(), `INSERT INTO admin_setup_links(token_hash,user_id,expires_at) VALUES($1,$2,now()+$3::interval)`, hash, target.ID, fmt.Sprintf("%d minutes", p.ResetExpiryMinutes))
	if e != nil {
		return e
	}
	publicCreated(w, map[string]any{"url": strings.TrimRight(s.publicURL(), "/") + "/api/admin-onboarding/" + url.PathEscape(raw), "method": "sso", "expiresInSeconds": p.ResetExpiryMinutes * 60})
	return nil
}

type adminOnboardingKey struct{}

func (s *Server) adminOnboardingStart(w http.ResponseWriter, r *http.Request) error {
	var id string
	hash := auth.HashToken(r.PathValue("token"))
	err := s.Store.DB.QueryRow(r.Context(), `SELECT l.user_id FROM admin_setup_links l JOIN admin_onboarding o ON o.user_id=l.user_id JOIN users u ON u.id=l.user_id WHERE l.token_hash=$1 AND l.used_at IS NULL AND l.expires_at>now() AND o.completed_at IS NULL AND u.status='active'`, hash).Scan(&id)
	if err != nil {
		return httpx.NotFound("Setup link")
	}
	r = r.WithContext(context.WithValue(r.Context(), adminOnboardingKey{}, hash))
	rec := httptest.NewRecorder()
	if err = s.beginSSO(rec, r, id, "onboarding", "/", nil); err != nil {
		return err
	}
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	var out map[string]string
	if json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		return httpx.Unavailable("Couldn't start SSO.")
	}
	http.Redirect(w, r, out["url"], 302)
	return nil
}

// Explicit possession of an onboarding link plus verified provider identity authorizes linking.
func (s *Server) completeAdminSSO(r *http.Request, pend ssoPending, o oidcSettings, c *auth.Claims) (*store.User, error) {
	ctx := r.Context()
	tx, e := s.Store.DB.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	var id string
	if e = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, pend.userID).Scan(&id); e != nil {
		return nil, e
	}
	e = tx.QueryRow(ctx, `UPDATE admin_setup_links SET used_at=now() WHERE token_hash=$1 AND user_id=$2 AND used_at IS NULL AND expires_at>now() RETURNING user_id`, pend.onboardingHash, pend.userID).Scan(&id)
	if e != nil {
		return nil, errors.New("this setup link expired or was used")
	}
	target, e := s.Store.GetUser(store.WithTransaction(ctx, tx), id)
	if e != nil || target.Status != "active" || target.HasPassword || target.SSO || roleRank[target.Role] >= roleRank["admin"] {
		return nil, errors.New("this account cannot be linked through setup")
	}
	if c.EmailVerified == nil || !*c.EmailVerified || !strings.EqualFold(c.Email, target.Email) {
		return nil, errors.New("sign in with the verified email this setup link belongs to")
	}
	if len(o.AllowedDomains) > 0 {
		_, domain, _ := strings.Cut(strings.ToLower(c.Email), "@")
		if !slices.Contains(o.AllowedDomains, domain) {
			return nil, errors.New("this email domain is not allowed")
		}
	}
	tag, e := tx.Exec(ctx, `UPDATE admin_onboarding SET completed_at=now(),method='sso' WHERE user_id=$1 AND completed_at IS NULL`, id)
	if e != nil || tag.RowsAffected() != 1 {
		return nil, errors.New("account setup is already complete")
	}
	if _, e = tx.Exec(ctx, `UPDATE users SET oidc_issuer=$2,oidc_subject=$3 WHERE id=$1 AND oidc_subject IS NULL`, id, o.Issuer, c.Subject); e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `UPDATE admin_setup_links SET used_at=coalesce(used_at,now()) WHERE user_id=$1`, id); e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `UPDATE user_tokens SET used_at=coalesce(used_at,now()) WHERE user_id=$1 AND kind='reset'`, id); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return s.Store.GetUser(ctx, id)
}
