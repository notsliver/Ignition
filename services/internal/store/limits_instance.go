package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// InstanceLimits caps how much each team (and person) can create. 0 means unlimited. Set in
// Instance administration › Limits; checked by the API before anything is created.
type InstanceLimits struct {
	TeamsPerUser     int `json:"teamsPerUser"` // teams one person can own
	ProjectsPerTeam  int `json:"projectsPerTeam"`
	ResourcesPerTeam int `json:"resourcesPerTeam"` // applications, databases, workers, functions, game servers
	MembersPerTeam   int `json:"membersPerTeam"`
	// PersonalTeams gives every new account its own team, named after its email.
	PersonalTeams bool `json:"personalTeams"`
}

func (s *Store) Limits(ctx context.Context) InstanceLimits {
	l := InstanceLimits{PersonalTeams: true}
	s.GetSetting(ctx, "limits", &l)
	return l
}

// LimitError says which limit was reached, in words the API can return as is.
type LimitError struct {
	What string
	Max  int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("This has reached the limit of %d %s. A platform administrator can raise it under Instance administration › Limits.", e.Max, e.What)
}

// CheckTeamLimit returns a *LimitError when the team already has as many projects, resources or
// members as allowed. Counts use the privileged pool: a member may not see everything the team has.
func (s *Store) CheckTeamLimit(ctx context.Context, team, kind string) error {
	l := s.Limits(ctx)
	var max int
	var query, what string
	switch kind {
	case "projects":
		max, what, query = l.ProjectsPerTeam, "projects in a team", `SELECT count(*) FROM projects WHERE team_id = $1`
	case "resources":
		max, what, query = l.ResourcesPerTeam, "services in a team", `SELECT count(*) FROM resources WHERE team_id = $1`
	case "members":
		// Pending invitations count: accepting them would go over.
		max, what, query = l.MembersPerTeam, "members in a team", `SELECT (SELECT count(*) FROM team_memberships WHERE team_id = $1) + (SELECT count(*) FROM team_invitations WHERE team_id = $1 AND accepted_at IS NULL AND expires_at > now())`
	default:
		return fmt.Errorf("unknown limit %q", kind)
	}
	if max <= 0 || team == "" {
		return nil
	}
	var n int
	if err := s.DB.QueryRow(ctx, query, team).Scan(&n); err != nil {
		return err
	}
	if n >= max {
		return &LimitError{What: what, Max: max}
	}
	return nil
}

// CheckOwnedTeams applies the teams-per-person limit before someone creates a team.
func (s *Store) CheckOwnedTeams(ctx context.Context, user string) error {
	max := s.Limits(ctx).TeamsPerUser
	if max <= 0 {
		return nil
	}
	var n int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM team_memberships WHERE user_id = $1 AND role = 'owner'`, user).Scan(&n); err != nil {
		return err
	}
	if n >= max {
		return &LimitError{What: "teams you can own", Max: max}
	}
	return nil
}

// createPersonalTeam gives a new account its own team named after its email, when the instance
// does that (Limits › Default team). It isn't counted against the teams-per-person limit.
func (s *Store) createPersonalTeam(ctx context.Context, tx pgx.Tx, u *User) error {
	if !s.Limits(ctx).PersonalTeams {
		return nil
	}
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO teams(name,created_by) VALUES($1,$2) RETURNING id`, u.Email, u.ID).Scan(&id); err != nil {
		return err
	}
	return s.initializeTeam(ctx, tx, id, u.ID)
}

// CreateCustomer initializes the account and optional personal team in the caller's transaction.
func (s *Store) CreateCustomer(ctx context.Context, email, name, role string) (*User, *string, error) {
	tx, err := s.SQL(ctx).Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)
	u, err := scanUser(tx.QueryRow(ctx, `INSERT INTO users(email,name,password_hash,role) VALUES($1,$2,'',$3) RETURNING `+userCols, email, name, role))
	if err != nil {
		return nil, nil, err
	}
	if err = s.createPersonalTeam(ctx, tx, u); err != nil {
		return nil, nil, err
	}
	var team *string
	err = tx.QueryRow(ctx, `SELECT id FROM teams WHERE created_by=$1 ORDER BY created_at,id LIMIT 1`, u.ID).Scan(&team)
	if err != nil && err != pgx.ErrNoRows {
		return nil, nil, err
	}
	return u, team, tx.Commit(ctx)
}
