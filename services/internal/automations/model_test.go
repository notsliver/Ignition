package automations

import (
	"strings"
	"testing"
	"time"
)

func valid() Automation {
	return Automation{Name: "Heal", Scope: ScopeResource, Mode: ModeAuto, Trigger: Trigger{Type: "event", Event: "resource.unhealthy"},
		Steps: []Step{{Action: "restart"}}}
}

func TestValidateDefaultsAndParams(t *testing.T) {
	a := valid()
	if f := Validate(&a); len(f) > 0 {
		t.Fatalf("valid automation rejected: %v", f)
	}
	if a.Conditions.CooldownMinutes != 30 {
		t.Error("event triggers get a cooldown so a lasting condition doesn't fire every few seconds")
	}
	if a.Steps[0].Int("verifySeconds") != 60 || a.Steps[0].OnFailure != "stop" {
		t.Errorf("defaults not applied: %+v", a.Steps[0])
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Automation){
		"name":      func(a *Automation) { a.Name = " " },
		"mode":      func(a *Automation) { a.Mode = "yolo" },
		"trigger":   func(a *Automation) { a.Trigger = Trigger{Type: "metric", Metric: "disk", Above: 90, ForMinutes: 5} }, // disk is a server metric
		"steps":     func(a *Automation) { a.Steps = []Step{{Action: "docker_cleanup"}} },                                  // a server step on a resource
		"notifyUrl": func(a *Automation) { a.Steps = append(a.Steps, Step{Action: "notify"}) },
		"conditions": func(a *Automation) {
			a.Conditions.Window = &Window{Days: []int{1}, Start: "25:00", End: "03:00"}
		},
	}
	for field, change := range cases {
		a := valid()
		change(&a)
		if f := Validate(&a); f[field] == "" {
			t.Errorf("expected an error on %s, got %v", field, f)
		}
	}
	cmd := valid()
	cmd.Steps = []Step{{Action: "run_command", Params: map[string]any{"command": "  "}}}
	if f := Validate(&cmd); !strings.Contains(f["steps"], "command") {
		t.Errorf("an empty command must be refused: %v", f)
	}
	srv := Automation{Name: "Clean", Scope: ScopeServers, Mode: ModeAuto, Trigger: Trigger{Type: "manual"}, Steps: []Step{{Action: "docker_cleanup"}}, Targets: Targets{Mode: "label", Label: "role"}}
	if f := Validate(&srv); f["targets"] == "" {
		t.Error("a label selector needs key=value")
	}
}

func TestWindow(t *testing.T) {
	night := &Window{Days: []int{6}, Start: "22:00", End: "04:00", Timezone: "UTC"} // Saturday night
	at := func(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
	cases := map[string]bool{
		"2026-10-31T23:30:00Z": true,  // Saturday 23:30
		"2026-11-01T03:59:00Z": true,  // Sunday early morning belongs to Saturday's window
		"2026-11-01T04:00:00Z": false, // window over
		"2026-10-31T21:59:00Z": false,
		"2026-11-01T23:00:00Z": false, // Sunday night isn't a window day
	}
	for ts, want := range cases {
		if got := InWindow(night, at(ts)); got != want {
			t.Errorf("%s: %v, want %v", ts, got, want)
		}
	}
	if !InWindow(nil, time.Now()) {
		t.Error("no window means any time")
	}
}

func TestTargetsMatch(t *testing.T) {
	labels := map[string]string{"role": "web"}
	if !(Targets{Mode: "label", Label: "role=web"}).Matches("a", "prod", labels) || (Targets{Mode: "label", Label: "role=db"}).Matches("a", "prod", labels) {
		t.Error("label selector")
	}
	if !(Targets{Mode: "environment", Environment: "Prod"}).Matches("a", "prod", nil) {
		t.Error("environments match case-insensitively")
	}
	if (Targets{}).Matches("a", "", nil) {
		t.Error("no selector selects nothing")
	}
}

func TestWorkflow(t *testing.T) {
	a := valid()
	a.Mode = ModeAssisted
	Validate(&a)
	got := strings.Join(Workflow(&a), " → ")
	if got != "Event → Pick targets → Check conditions → Plan → Approval → Restart → Verify → Record" {
		t.Errorf("got %s", got)
	}
	a.Mode = ModeObserve
	if w := Workflow(&a); w[len(w)-2] != "Recommend" {
		t.Errorf("observe mode recommends: %v", w)
	}
}
