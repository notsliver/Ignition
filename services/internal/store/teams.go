package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const LegacyTeamID = "00000000-0000-4000-8000-000000000001"

var ErrLastOwner = errors.New("a team must retain an owner")

type Team struct {
	IconURL       string        `json:"iconUrl"`
	AccessVersion int64         `json:"accessVersion"`
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Slug          string        `json:"slug"`
	Role          string        `json:"role"`
	Overrides     PermOverrides `json:"permissionOverrides"`
	Experiences   []string      `json:"experiences"`
	Permissions   []string      `json:"permissions"`
	// AdminAccess: not a member; a platform administrator working in it as owner (audited).
	AdminAccess bool `json:"adminAccess,omitempty"`
}

func (s *Store) Teams(ctx context.Context, userID string) ([]Team, error) {
	rows, err := s.TrustedSQL(ctx).Query(ctx, `SELECT t.id,t.name,t.slug,t.icon_file,m.role,m.permissions,t.access_version,ARRAY(SELECT experience FROM team_experiences x WHERE x.team_id=t.id ORDER BY experience) FROM teams t JOIN team_memberships m ON m.team_id=t.id WHERE m.user_id=$1 ORDER BY t.created_at,t.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Team{}
	for rows.Next() {
		var t Team
		var raw []byte
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.IconURL, &t.Role, &raw, &t.AccessVersion, &t.Experiences); err != nil {
			return nil, err
		}
		t.IconURL = ImageURL("teams", t.IconURL)
		json.Unmarshal(raw, &t.Overrides)
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) Membership(ctx context.Context, team, user string) (*Team, error) {
	list, err := s.Teams(ctx, user)
	if err != nil {
		return nil, err
	}
	for _, t := range list {
		if t.ID == team {
			return &t, nil
		}
	}
	if sc, ok := ScopeFrom(ctx); ok && sc.InstanceAdmin && sc.TeamID == team && sc.UserID == user {
		return s.AdminTeam(ctx, team)
	}
	return nil, ErrNotFound
}

// AdminTeam is a team as a platform administrator who isn't a member sees it: as its owner.
func (s *Store) AdminTeam(ctx context.Context, team string) (*Team, error) {
	t := Team{Role: "owner", AdminAccess: true, Overrides: PermOverrides{}}
	err := s.TrustedSQL(ctx).QueryRow(ctx, `SELECT id,name,slug,icon_file,access_version,ARRAY(SELECT experience FROM team_experiences x WHERE x.team_id=t.id ORDER BY experience) FROM teams t WHERE id=$1`, team).
		Scan(&t.ID, &t.Name, &t.Slug, &t.IconURL, &t.AccessVersion, &t.Experiences)
	if err != nil {
		return nil, noRows(err)
	}
	t.IconURL = ImageURL("teams", t.IconURL)
	return &t, nil
}
func (s *Store) CreateTeam(ctx context.Context, user, name string) (*Team, error) {
	tx, err := s.TrustedSQL(ctx).Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var id string
	if err = tx.QueryRow(ctx, `INSERT INTO teams(name,created_by) VALUES($1,$2) RETURNING id`, name, user).Scan(&id); err != nil {
		return nil, err
	}
	if err = s.initializeTeam(ctx, tx, id, user); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Membership(ctx, id, user)
}
func (s *Store) initializeTeam(ctx context.Context, tx pgx.Tx, id, user string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO team_memberships(team_id,user_id,role) VALUES($1,$2,'owner') ON CONFLICT DO NOTHING`, id, user); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO team_experiences(team_id,experience) VALUES($1,'infrastructure') ON CONFLICT DO NOTHING`, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO environments(team_id,name,is_team_default) VALUES($1,'Production',true) ON CONFLICT(team_id) WHERE is_team_default DO NOTHING`, id)
	return err
}

type TeamMember struct {
	AvatarURL   string        `json:"avatarUrl"`
	UserID      string        `json:"userId"`
	Name        string        `json:"name"`
	Email       string        `json:"email"`
	Role        string        `json:"role"`
	Permissions PermOverrides `json:"permissionOverrides"`
	// Only in the team member list.
	Status   string     `json:"status,omitempty"`
	MFA      bool       `json:"mfa,omitempty"`
	JoinedAt *time.Time `json:"joinedAt,omitempty"`
}

func (s *Store) TeamMembers(ctx context.Context, team string) ([]TeamMember, error) {
	rows, err := s.TrustedSQL(ctx).Query(ctx, `SELECT u.id,u.name,u.email,m.role,m.permissions,u.status,u.totp_enabled_at IS NOT NULL,m.created_at,u.avatar_file FROM team_memberships m JOIN users u ON u.id=m.user_id WHERE m.team_id=$1 ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'developer' THEN 2 WHEN 'viewer' THEN 3 ELSE 4 END, lower(coalesce(nullif(u.name,''),u.email))`, team)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TeamMember{}
	for rows.Next() {
		var m TeamMember
		var raw []byte
		var joined time.Time
		if err := rows.Scan(&m.UserID, &m.Name, &m.Email, &m.Role, &raw, &m.Status, &m.MFA, &joined, &m.AvatarURL); err != nil {
			return nil, err
		}
		json.Unmarshal(raw, &m.Permissions)
		m.JoinedAt = &joined
		m.AvatarURL = ImageURL("avatars", m.AvatarURL)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Lock the team, not just one membership, to serialize concurrent owner removals.
func (s *Store) SetTeamMember(ctx context.Context, team, user, role string, perms PermOverrides) error {
	tx, err := s.TrustedSQL(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var locked string
	if err = tx.QueryRow(ctx, `SELECT id FROM teams WHERE id=$1 FOR UPDATE`, team).Scan(&locked); err != nil {
		return noRows(err)
	}
	var old string
	if err = tx.QueryRow(ctx, `SELECT role FROM team_memberships WHERE team_id=$1 AND user_id=$2`, team, user).Scan(&old); err != nil {
		return noRows(err)
	}
	if old == "owner" && role != "owner" {
		var n int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM team_memberships m JOIN users u ON u.id=m.user_id WHERE m.team_id=$1 AND m.role='owner' AND u.status='active' AND m.user_id<>$2`, team, user).Scan(&n); err != nil {
			return err
		}
		if n < 1 {
			return ErrLastOwner
		}
	}
	if role == "" {
		if _, err = tx.Exec(ctx, `DELETE FROM resource_tokens WHERE team_id=$1 AND created_by=$2`, team, user); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE automations SET hook_hash=NULL WHERE team_id=$1 AND created_by=$2`, team, user); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM team_memberships WHERE team_id=$1 AND user_id=$2`, team, user)
	} else {
		raw, _ := json.Marshal(perms)
		_, err = tx.Exec(ctx, `UPDATE team_memberships SET role=$3,permissions=$4 WHERE team_id=$1 AND user_id=$2`, team, user, role, raw)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) UpdateTeam(ctx context.Context, team, name string, experiences []string) error {
	tx, err := s.TrustedSQL(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE teams SET name=$2 WHERE id=$1`, team, name); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM team_experiences WHERE team_id=$1`, team); err != nil {
		return err
	}
	for _, e := range experiences {
		if _, err = tx.Exec(ctx, `INSERT INTO team_experiences VALUES($1,$2) ON CONFLICT DO NOTHING`, team, e); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) TeamEnvironments(ctx context.Context) ([]Environment, error) {
	rows, err := s.SQL(ctx).Query(ctx, `SELECT id,name FROM environments ORDER BY is_team_default DESC,project_id NULLS FIRST,position,name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Environment{}
	for rows.Next() {
		var e Environment
		if err = rows.Scan(&e.ID, &e.Name); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) DefaultEnvironment(ctx context.Context) (string, error) {
	var id string
	err := s.SQL(ctx).QueryRow(ctx, `SELECT id FROM environments WHERE is_team_default LIMIT 1`).Scan(&id)
	return id, noRows(err)
}
func (s *Store) ProjectAssignments(ctx context.Context, team, project string) ([]TeamMember, error) {
	rows, err := s.TrustedSQL(ctx).Query(ctx, `SELECT u.id,u.name,u.email,a.role,a.permissions,u.avatar_file FROM project_assignments a JOIN users u ON u.id=a.user_id WHERE a.team_id=$1 AND a.project_id=$2 ORDER BY lower(u.email)`, team, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TeamMember{}
	for rows.Next() {
		var m TeamMember
		var raw []byte
		if err = rows.Scan(&m.UserID, &m.Name, &m.Email, &m.Role, &raw, &m.AvatarURL); err != nil {
			return nil, err
		}
		json.Unmarshal(raw, &m.Permissions)
		m.AvatarURL = ImageURL("avatars", m.AvatarURL)
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) SetProjectAssignment(ctx context.Context, team, project, user, role string, perms PermOverrides) error {
	if role == "" {
		_, err := s.TrustedSQL(ctx).Exec(ctx, `DELETE FROM project_assignments WHERE team_id=$1 AND project_id=$2 AND user_id=$3`, team, project, user)
		return err
	}
	raw, _ := json.Marshal(perms)
	_, err := s.TrustedSQL(ctx).Exec(ctx, `INSERT INTO project_assignments(team_id,project_id,user_id,role,permissions) VALUES($1,$2,$3,$4,$5) ON CONFLICT(project_id,user_id) DO UPDATE SET role=excluded.role,permissions=excluded.permissions`, team, project, user, role, raw)
	return err
}
func (s *Store) InviteTeamMember(ctx context.Context, team, email, role, actor string, hash []byte) (string, error) {
	var id string
	err := s.TrustedSQL(ctx).QueryRow(ctx, `INSERT INTO team_invitations(team_id,email,role,created_by,token_hash,expires_at) VALUES($1,lower($2),$3,$4,$5,now()+interval '7 days') RETURNING id`, team, email, role, actor, hash).Scan(&id)
	return id, err
}
func (s *Store) AcceptTeamInvitation(ctx context.Context, user, email string, hash []byte) (string, error) {
	tx, err := s.TrustedSQL(ctx).Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var team, role string
	if err = tx.QueryRow(ctx, `UPDATE team_invitations SET accepted_at=now() WHERE token_hash=$1 AND email=lower($2) AND accepted_at IS NULL AND expires_at>now() RETURNING team_id,role`, hash, email).Scan(&team, &role); err != nil {
		return "", noRows(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO team_memberships(team_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, team, user, role); err != nil {
		return "", err
	}
	return team, tx.Commit(ctx)
}

type TeamInvitation struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (s *Store) TeamInvitations(ctx context.Context, team string) ([]TeamInvitation, error) {
	rows, err := s.TrustedSQL(ctx).Query(ctx, `SELECT id,email,role,expires_at FROM team_invitations WHERE team_id=$1 AND accepted_at IS NULL AND expires_at>now() ORDER BY created_at`, team)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TeamInvitation{}
	for rows.Next() {
		var i TeamInvitation
		if err = rows.Scan(&i.ID, &i.Email, &i.Role, &i.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) ServerAssigned(ctx context.Context, team, server string) bool {
	var ok bool
	s.TrustedSQL(ctx).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM team_server_assignments WHERE team_id=$1 AND server_id=$2)`, team, server).Scan(&ok)
	return ok
}

func (s *Store) JobOwnershipValid(ctx context.Context, team string, resource, deployment, server *string) bool {
	if team == "" {
		return false
	}
	if resource != nil {
		var actual, host string
		if err := s.TrustedSQL(ctx).QueryRow(ctx, `SELECT team_id,server_id FROM resources WHERE id=$1`, *resource).Scan(&actual, &host); err != nil || actual != team {
			return false
		}
		if !s.ServerAssigned(ctx, team, host) {
			return false
		}
	}
	if deployment != nil {
		var actual string
		var target string
		if err := s.TrustedSQL(ctx).QueryRow(ctx, `SELECT team_id,resource_id FROM deployments WHERE id=$1`, *deployment).Scan(&actual, &target); err != nil || actual != team || resource == nil || target != *resource {
			return false
		}
	}
	return server == nil || s.ServerAssigned(ctx, team, *server)
}

func (s *Store) RegisterTeamInvitation(ctx context.Context, token []byte, name, hash string) (*User, error) {
	tx, err := s.TrustedSQL(ctx).Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var team, email, role string
	if err = tx.QueryRow(ctx, `UPDATE team_invitations SET accepted_at=now() WHERE token_hash=$1 AND accepted_at IS NULL AND expires_at>now() RETURNING team_id,email,role`, token).Scan(&team, &email, &role); err != nil {
		return nil, noRows(err)
	}
	user, err := scanUser(tx.QueryRow(ctx, `INSERT INTO users(email,name,password_hash,role) VALUES($1,$2,$3,'viewer') RETURNING `+userCols, email, name, hash))
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO team_memberships(team_id,user_id,role) VALUES($1,$2,$3)`, team, user.ID, role); err != nil {
		return nil, err
	}
	if err = s.createPersonalTeam(ctx, tx, user); err != nil {
		return nil, err
	}
	return user, tx.Commit(ctx)
}

func lockUserTeamOwners(ctx context.Context, tx pgx.Tx, user string) error {
	rows, err := tx.Query(ctx, `SELECT id FROM teams WHERE id IN (SELECT team_id FROM team_memberships WHERE user_id=$1 AND role='owner') ORDER BY id FOR UPDATE`, user)
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	rows.Close()
	if rows.Err() != nil {
		return rows.Err()
	}
	var orphan bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM team_memberships m WHERE m.user_id=$1 AND m.role='owner' AND NOT EXISTS(SELECT 1 FROM team_memberships x JOIN users u ON u.id=x.user_id WHERE x.team_id=m.team_id AND x.user_id<>$1 AND x.role='owner' AND u.status='active'))`, user).Scan(&orphan)
	if err != nil {
		return err
	}
	if orphan {
		return ErrLastOwner
	}
	return nil
}
