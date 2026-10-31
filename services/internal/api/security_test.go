package api

import (
	"testing"
	"time"

	"github.com/notsliver/Ignition/services/internal/store"
)

func TestMFADeadline(t *testing.T) {
	day := func(d time.Duration) string { return time.Now().UTC().Add(d).Format("2006-01-02") }
	cases := []struct {
		name            string
		p               securityPolicy
		grace, required bool
	}{
		{"off", securityPolicy{}, false, false},
		{"enforced now", securityPolicy{EnforceMFA: true}, false, true},
		{"deadline ahead", securityPolicy{EnforceMFA: true, MFADeadline: day(72 * time.Hour)}, true, false},
		{"deadline is today: still grace until the day ends", securityPolicy{EnforceMFA: true, MFADeadline: day(0)}, true, false},
		{"deadline passed", securityPolicy{EnforceMFA: true, MFADeadline: day(-48 * time.Hour)}, false, true},
		{"unparseable deadline counts as none", securityPolicy{EnforceMFA: true, MFADeadline: "soon"}, false, true},
	}
	for _, c := range cases {
		if c.p.mfaGrace() != c.grace || c.p.mfaRequired() != c.required {
			t.Errorf("%s: grace %v required %v", c.name, c.p.mfaGrace(), c.p.mfaRequired())
		}
	}
	if !mfaExempt(&store.User{SSO: true}) || mfaExempt(&store.User{SSO: true, HasPassword: true}) {
		t.Error("only SSO accounts without a password are exempt")
	}
}
