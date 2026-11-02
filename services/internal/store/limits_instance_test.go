package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/notsliver/Ignition/services/internal/database"
)

func TestPersonalTeamsAndLimits(t *testing.T) {
	url := os.Getenv("IGNITION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IGNITION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: pool}
	defer s.SetSetting(ctx, "limits", InstanceLimits{PersonalTeams: true})

	// A new account gets its own team, named after its email, and owns it.
	email := "personal-" + time.Now().Format("150405.000000") + "@example.test"
	u, err := s.CreateSSOUser(ctx, email, "Pat", "viewer", "sub-"+email)
	if err != nil {
		t.Fatal(err)
	}
	teams, err := s.Teams(ctx, u.ID)
	if err != nil || len(teams) != 1 || teams[0].Name != email || teams[0].Role != "owner" {
		t.Fatalf("personal team = %+v, %v", teams, err)
	}

	// One owned team allowed: the personal one already uses it.
	if err = s.SetSetting(ctx, "limits", InstanceLimits{TeamsPerUser: 1, PersonalTeams: true}); err != nil {
		t.Fatal(err)
	}
	var limit *LimitError
	if err = s.CheckOwnedTeams(ctx, u.ID); !errors.As(err, &limit) || limit.Max != 1 {
		t.Fatalf("teams-per-person limit not reached: %v", err)
	}
	if err = s.CheckTeamLimit(ctx, teams[0].ID, "members"); err != nil {
		t.Fatalf("members unlimited, got %v", err)
	}
	if err = s.SetSetting(ctx, "limits", InstanceLimits{MembersPerTeam: 1}); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckTeamLimit(ctx, teams[0].ID, "members"); !errors.As(err, &limit) {
		t.Fatalf("members limit (1, team has 1) not reached: %v", err)
	}

	// Turned off: new accounts start without a team.
	other := "noteam-" + time.Now().Format("150405.000000") + "@example.test"
	v, err := s.CreateSSOUser(ctx, other, "Noa", "viewer", "sub-"+other)
	if err != nil {
		t.Fatal(err)
	}
	if teams, _ := s.Teams(ctx, v.ID); len(teams) != 0 {
		t.Fatalf("personal team created while turned off: %+v", teams)
	}
}
