package api

import (
	"context"
	"net/http"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

// onboardingState is first-run setup: what the instance is for, and whether the owner still has to
// finish securing it. Instances set up before onboarding existed have no state and aren't pending.
type onboardingState struct {
	UseCase     string     `json:"useCase"` // infrastructure | hosting
	Pending     bool       `json:"pending"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

func (s *Server) onboarding(ctx context.Context) onboardingState {
	var st onboardingState
	s.Store.GetSetting(ctx, "onboarding", &st)
	if st.UseCase == "" {
		st.UseCase = "infrastructure"
	}
	return st
}

// completeOnboarding finishes setup. The initial account must be protected: the owner needs
// two-factor authentication (and so recovery codes) before the instance is ready.
func (s *Server) completeOnboarding(w http.ResponseWriter, r *http.Request, u *store.User) error {
	st := s.onboarding(r.Context())
	if !st.Pending {
		httpx.JSON(w, 200, st)
		return nil
	}
	if u.Role != "owner" {
		return httpx.Forbidden("The owner finishes setting up Ignition.")
	}
	if u.MFAEnabledAt == nil {
		return httpx.Conflict("Turn on two-factor authentication first: the first account has full control of this instance.")
	}
	now := time.Now().UTC()
	st.Pending, st.CompletedAt = false, &now
	if err := s.Store.SetSetting(r.Context(), "onboarding", st); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "instance.setup_complete", "user", u.ID, map[string]any{"useCase": st.UseCase}, clientIP(r))
	httpx.JSON(w, 200, st)
	return nil
}
