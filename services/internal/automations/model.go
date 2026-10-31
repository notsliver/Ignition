// Package automations defines Ignition's operations engine: what can trigger an automation, which
// built-in steps it can take, and how each step's outcome is verified. Execution lives in the worker;
// this package is the shared vocabulary and the rules (validation, policy) the API and worker agree on.
//
// Everything listed as available here really runs. Capabilities that need something Ignition doesn't
// have yet (a host agent for OS packages, a load balancer for scaling, provider APIs for power control)
// are listed in the catalog with what they need, never offered as steps.
package automations

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/cron"
)

const (
	ScopeResource = "resource"
	ScopeServers  = "servers"

	ModeObserve  = "observe"  // detect and recommend; nothing changes
	ModeAssisted = "assisted" // prepare a plan and wait for approval
	ModeAuto     = "auto"     // execute within the automation's limits

	// ApprovalTTL is how long a plan waits for a decision before it expires (state may have changed).
	ApprovalTTL = 24 * time.Hour
)

type Trigger struct {
	Type       string  `json:"type"` // schedule, event, metric, manual, webhook
	Schedule   string  `json:"schedule,omitempty"`
	Timezone   string  `json:"timezone,omitempty"`
	Event      string  `json:"event,omitempty"`
	Metric     string  `json:"metric,omitempty"` // cpu, memory, disk
	Above      float64 `json:"above,omitempty"`  // percent
	ForMinutes int     `json:"forMinutes,omitempty"`
}

type Window struct {
	Days     []int  `json:"days"`  // 0 = Sunday
	Start    string `json:"start"` // HH:MM
	End      string `json:"end"`   // HH:MM; may be before Start (crosses midnight)
	Timezone string `json:"timezone"`
}

type Conditions struct {
	CooldownMinutes   int     `json:"cooldownMinutes"`   // don't fire again for the same target sooner
	SkipInMaintenance bool    `json:"skipInMaintenance"` // servers in maintenance (or a resource's server) are left alone
	Window            *Window `json:"window,omitempty"`  // only act inside this maintenance window
}

type Targets struct {
	Mode          string   `json:"mode,omitempty"` // all, ids, environment, label
	IDs           []string `json:"ids,omitempty"`
	Environment   string   `json:"environment,omitempty"`
	Label         string   `json:"label,omitempty"`         // key=value
	StopOnFailure bool     `json:"stopOnFailure,omitempty"` // staged rollout: stop at the first target that fails
}

// Matches reports whether a server is selected.
func (t Targets) Matches(id, environment string, labels map[string]string) bool {
	switch t.Mode {
	case "all":
		return true
	case "ids":
		for _, x := range t.IDs {
			if x == id {
				return true
			}
		}
	case "environment":
		return strings.EqualFold(environment, strings.TrimSpace(t.Environment))
	case "label":
		k, v, _ := strings.Cut(t.Label, "=")
		return labels[k] == v
	}
	return false
}

type Step struct {
	Action    string         `json:"action"`
	Params    map[string]any `json:"params,omitempty"`
	OnFailure string         `json:"onFailure,omitempty"` // stop (default) or continue
}

/* --------------------------------- Catalog --------------------------------- */

// Action is a built-in step.
type Action struct {
	ID       string   `json:"id"`
	Scope    []string `json:"scope"`
	Label    string   `json:"label"`
	Does     string   `json:"does"`
	Verifies string   `json:"verifies"`
	Risk     string   `json:"risk"`     // safe, disruptive
	ReadOnly bool     `json:"readOnly"` // changes nothing; also runs in observe mode, to gather evidence
	Params   []Param  `json:"params"`
}

type Param struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Kind    string `json:"kind"` // bool, int, text, command
	Default any    `json:"default"`
	Min     int    `json:"min,omitempty"`
	Max     int    `json:"max,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

var Actions = []Action{
	{ID: "diagnostics", ReadOnly: true, Scope: []string{ScopeServers}, Label: "Run diagnostics", Risk: "safe",
		Does:     "Runs the server's diagnostic checks: connectivity, Docker, disk, memory, clock, DNS and ports.",
		Verifies: "No check failed."},
	{ID: "disk_report", ReadOnly: true, Scope: []string{ScopeServers}, Label: "Analyse disk usage", Risk: "safe",
		Does:     "Measures the disk and what Docker uses on it, and projects when it fills from the last 24 hours of samples.",
		Verifies: "Usage is below the threshold.",
		Params:   []Param{{Key: "threshold", Label: "Healthy below", Kind: "int", Default: 85, Min: 10, Max: 99, Hint: "Percent of the disk"}}},
	{ID: "docker_cleanup", Scope: []string{ScopeServers}, Label: "Safe Docker cleanup", Risk: "safe",
		Does:     "Plans, then removes only unused objects: stopped containers Ignition created, untagged images, build cache. Never volumes, running containers or rollback images.",
		Verifies: "Each planned object is gone and disk usage dropped.",
		Params: []Param{{Key: "containers", Label: "Stopped containers", Kind: "bool", Default: true}, {Key: "dangling", Label: "Untagged images", Kind: "bool", Default: true},
			{Key: "buildCache", Label: "Build cache", Kind: "bool", Default: true}}},
	{ID: "refresh", ReadOnly: true, Scope: []string{ScopeServers}, Label: "Reconnect and reconcile", Risk: "safe",
		Does:     "Checks the connection and Docker, and re-reads every workload's real state.",
		Verifies: "The server is reachable and Docker is ready."},
	{ID: "restart", Scope: []string{ScopeResource}, Label: "Restart", Risk: "disruptive",
		Does:     "Restarts the container (stop grace from Build & Deploy).",
		Verifies: "The container is running again and, if a health check is on, passes it.",
		Params:   []Param{{Key: "verifySeconds", Label: "Wait for recovery", Kind: "int", Default: 60, Min: 10, Max: 600, Hint: "Seconds"}}},
	{ID: "redeploy", Scope: []string{ScopeResource}, Label: "Redeploy", Risk: "disruptive",
		Does:     "Deploys the latest source or image tag, with the usual health gate.",
		Verifies: "The deployment succeeded and is the one serving."},
	{ID: "rollback", Scope: []string{ScopeResource}, Label: "Roll back to the previous good release", Risk: "disruptive",
		Does:     "Runs the newest successful release before the current one, using its exact image.",
		Verifies: "The rollback deployment succeeded and is serving."},
	{ID: "run_command", Scope: []string{ScopeResource}, Label: "Run a command", Risk: "disruptive",
		Does:     "Runs a command inside the container with sh -c.",
		Verifies: "It exits with 0 within the timeout (and prints the expected text, if set).",
		Params: []Param{{Key: "command", Label: "Command", Kind: "command", Default: ""}, {Key: "expect", Label: "Output must contain", Kind: "text", Default: "", Hint: "Optional"},
			{Key: "timeoutSeconds", Label: "Timeout", Kind: "int", Default: 120, Min: 5, Max: 3600, Hint: "Seconds"}}},
	{ID: "collect_diagnostics", ReadOnly: true, Scope: []string{ScopeResource}, Label: "Collect diagnostics", Risk: "safe",
		Does:     "Saves the container's state, exit code, restarts, recent health results and last log lines to the run.",
		Verifies: "The evidence was captured."},
	{ID: "notify", ReadOnly: true, Scope: []string{ScopeResource, ScopeServers}, Label: "Send a notification", Risk: "safe",
		Does:     "POSTs a JSON summary to the automation's webhook (Slack, Discord, or anything that accepts JSON).",
		Verifies: "The webhook answered with 2xx.",
		Params:   []Param{{Key: "message", Label: "Message", Kind: "text", Default: ""}}},
}

func ActionByID(id string) *Action {
	for i := range Actions {
		if Actions[i].ID == id {
			return &Actions[i]
		}
	}
	return nil
}

// Event is something the engine watches for.
type Event struct {
	ID    string `json:"id"`
	Scope string `json:"scope"`
	Label string `json:"label"`
}

var Events = []Event{
	{"server.offline", ScopeServers, "The server goes offline"},
	{"server.degraded", ScopeServers, "The server is degraded (Docker not ready, or checks failing)"},
	{"resource.unhealthy", ScopeResource, "Its health check reports unhealthy"},
	{"resource.failed", ScopeResource, "The container stops unexpectedly"},
	{"deployment.failed", ScopeResource, "A deployment fails"},
	{"deployment.succeeded", ScopeResource, "A deployment succeeds"},
}

var Metrics = map[string][]string{ScopeServers: {"cpu", "memory", "disk"}, ScopeResource: {"cpu", "memory"}}

func eventFor(id, scope string) bool {
	for _, e := range Events {
		if e.ID == id && e.Scope == scope {
			return true
		}
	}
	return false
}

/* --------------------------------- Validation -------------------------------- */

var (
	hhmm    = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	labelRE = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,63}=[A-Za-z0-9._/ -]{0,63}$`)
)

// Automation is what the API validates and the worker executes.
type Automation struct {
	Name, Description, Scope, Mode string
	Targets                        Targets
	Trigger                        Trigger
	Conditions                     Conditions
	Steps                          []Step
	EscalateAfter, MaxRunsPerHour  int
	NotifyURL                      string
}

// Validate normalises a and returns field errors written for people.
func Validate(a *Automation) map[string]string {
	f := map[string]string{}
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" || len(a.Name) > 80 {
		f["name"] = "Name it after what it does (up to 80 characters)."
	}
	if len(a.Description) > 500 {
		f["description"] = "Up to 500 characters."
	}
	if a.Mode != ModeObserve && a.Mode != ModeAssisted && a.Mode != ModeAuto {
		f["mode"] = "Choose observe, assisted or automatic."
	}
	if a.EscalateAfter == 0 {
		a.EscalateAfter = 3
	}
	if a.MaxRunsPerHour == 0 {
		a.MaxRunsPerHour = 6
	}
	if a.EscalateAfter < 1 || a.EscalateAfter > 20 {
		f["escalateAfter"] = "Between 1 and 20 failed runs."
	}
	if a.MaxRunsPerHour < 1 || a.MaxRunsPerHour > 60 {
		f["maxRunsPerHour"] = "Between 1 and 60."
	}
	if a.NotifyURL != "" {
		if u, err := url.Parse(a.NotifyURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			f["notifyUrl"] = "Enter the webhook's http(s) URL."
		}
	}

	t := &a.Trigger
	switch t.Type {
	case "schedule":
		if t.Timezone == "" {
			t.Timezone = "UTC"
		}
		if _, err := cron.Parse(t.Schedule); err != nil {
			f["trigger"] = "Schedule: " + err.Error() + "."
		} else if _, err := time.LoadLocation(t.Timezone); err != nil {
			f["trigger"] = "Unknown timezone."
		}
	case "event":
		if !eventFor(t.Event, a.Scope) {
			f["trigger"] = "Choose an event."
		}
	case "metric":
		ok := false
		for _, m := range Metrics[a.Scope] {
			ok = ok || m == t.Metric
		}
		switch {
		case !ok:
			f["trigger"] = "Choose a metric."
		case t.Above < 1 || t.Above > 100:
			f["trigger"] = "The threshold is a percentage from 1 to 100."
		case t.ForMinutes < 1 || t.ForMinutes > 1440:
			f["trigger"] = "It must hold for 1 to 1440 minutes, so a short spike doesn't fire it."
		}
	case "manual", "webhook":
	default:
		f["trigger"] = "Choose when it runs."
	}

	c := &a.Conditions
	if c.CooldownMinutes < 0 || c.CooldownMinutes > 10080 {
		f["conditions"] = "Cooldown is 0 to 10080 minutes."
	}
	if (t.Type == "event" || t.Type == "metric") && c.CooldownMinutes == 0 {
		c.CooldownMinutes = 30 // an event that keeps holding must not fire every few seconds
	}
	if w := c.Window; w != nil {
		if w.Timezone == "" {
			w.Timezone = "UTC"
		}
		_, tzErr := time.LoadLocation(w.Timezone)
		switch {
		case len(w.Days) == 0:
			f["conditions"] = "Pick at least one day for the maintenance window."
		case !hhmm.MatchString(w.Start) || !hhmm.MatchString(w.End) || w.Start == w.End:
			f["conditions"] = "The window needs a start and end time like 02:00 and 05:00."
		case tzErr != nil:
			f["conditions"] = "Unknown timezone for the window."
		}
		for _, d := range w.Days {
			if d < 0 || d > 6 {
				f["conditions"] = "Days run from 0 (Sunday) to 6."
			}
		}
	}

	if a.Scope == ScopeServers {
		tg := &a.Targets
		switch tg.Mode {
		case "all":
		case "ids":
			if len(tg.IDs) == 0 {
				f["targets"] = "Pick at least one server."
			}
		case "environment":
			if strings.TrimSpace(tg.Environment) == "" {
				f["targets"] = "Enter the environment, e.g. production."
			}
		case "label":
			if !labelRE.MatchString(tg.Label) {
				f["targets"] = "Use a label like role=web."
			}
		default:
			f["targets"] = "Choose which servers it acts on."
		}
	}

	if len(a.Steps) == 0 {
		f["steps"] = "Add at least one step."
	}
	if len(a.Steps) > 10 {
		f["steps"] = "Up to 10 steps."
	}
	for i := range a.Steps {
		if msg := validateStep(&a.Steps[i], a.Scope); msg != "" {
			f["steps"] = fmt.Sprintf("Step %d: %s", i+1, msg)
			break
		}
	}
	if a.NotifyURL == "" {
		for _, s := range a.Steps {
			if s.Action == "notify" {
				f["notifyUrl"] = "A notification step needs a webhook URL."
			}
		}
	}
	return f
}

func validateStep(s *Step, scope string) string {
	act := ActionByID(s.Action)
	if act == nil {
		return "unknown action."
	}
	allowed := false
	for _, sc := range act.Scope {
		allowed = allowed || sc == scope
	}
	if !allowed {
		return act.Label + " isn't available here."
	}
	if s.OnFailure == "" {
		s.OnFailure = "stop"
	}
	if s.OnFailure != "stop" && s.OnFailure != "continue" {
		return "on failure is stop or continue."
	}
	params := map[string]any{}
	for _, p := range act.Params {
		v, ok := s.Params[p.Key]
		if !ok || v == nil {
			v = p.Default
		}
		switch p.Kind {
		case "bool":
			b, ok := v.(bool)
			if !ok {
				return p.Label + " must be on or off."
			}
			params[p.Key] = b
		case "int":
			n, ok := toInt(v)
			if !ok || n < p.Min || n > p.Max {
				return fmt.Sprintf("%s must be %d to %d.", p.Label, p.Min, p.Max)
			}
			params[p.Key] = n
		default:
			str, ok := v.(string)
			if !ok {
				return p.Label + " must be text."
			}
			str = strings.TrimSpace(str)
			if p.Kind == "command" && str == "" {
				return "enter the command to run."
			}
			if len(str) > 2000 {
				return p.Label + " is too long."
			}
			params[p.Key] = str
		}
	}
	s.Params = params
	return ""
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), n == float64(int(n))
	}
	return 0, false
}

// Int and Bool read validated params.
func (s Step) Int(key string) int     { n, _ := toInt(s.Params[key]); return n }
func (s Step) Bool(key string) bool   { b, _ := s.Params[key].(bool); return b }
func (s Step) Text(key string) string { t, _ := s.Params[key].(string); return t }

// Disruptive reports whether any step can interrupt service.
func Disruptive(steps []Step) bool {
	for _, s := range steps {
		if a := ActionByID(s.Action); a != nil && a.Risk == "disruptive" {
			return true
		}
	}
	return false
}

// InWindow reports whether t falls inside the window (always true without one).
func InWindow(w *Window, t time.Time) bool {
	if w == nil {
		return true
	}
	loc, err := time.LoadLocation(w.Timezone)
	if err != nil {
		loc = time.UTC
	}
	lt := t.In(loc)
	now := lt.Format("15:04")
	day := int(lt.Weekday())
	if w.Start < w.End {
		return hasDay(w.Days, day) && now >= w.Start && now < w.End
	}
	// Crosses midnight: 22:00-04:00 belongs to the day it started.
	if now >= w.Start {
		return hasDay(w.Days, day)
	}
	return now < w.End && hasDay(w.Days, (day+6)%7)
}

func hasDay(days []int, d int) bool {
	for _, x := range days {
		if x == d {
			return true
		}
	}
	return false
}

// Workflow is the run's shape in words, for the builder's preview and the run page.
func Workflow(a *Automation) []string {
	trig := map[string]string{"schedule": "Schedule", "event": "Event", "metric": "Threshold", "manual": "Run by hand", "webhook": "Webhook"}[a.Trigger.Type]
	out := []string{trig, "Pick targets", "Check conditions"}
	switch a.Mode {
	case ModeObserve:
		return append(out, "Plan", "Recommend", "Record")
	case ModeAssisted:
		out = append(out, "Plan", "Approval")
	default:
		out = append(out, "Plan")
	}
	for _, s := range a.Steps {
		if act := ActionByID(s.Action); act != nil {
			out = append(out, act.Label)
		}
	}
	return append(out, "Verify", "Record")
}

/* ---------------------------- Capability catalog ---------------------------- */

// Capability is one entry of the catalog. Available ones map to a trigger, step or built-in
// behaviour; the others say what Ignition would need first.
type Capability struct {
	Label  string `json:"label"`
	Status string `json:"status"` // available, builtin, agent, loadbalancer, provider
	How    string `json:"how"`
}

type CapabilityGroup struct {
	Label string       `json:"label"`
	Items []Capability `json:"items"`
}

const (
	needAgent = "Needs the Ignition server agent (package, kernel and host access), which isn't available yet. Servers are reached through the Docker API only."
)

var Catalog = []CapabilityGroup{
	{"Deployments & releases", []Capability{
		{"Git auto-deploy", "builtin", "Deploy hook under a resource's Access & Secrets; GitHub and GitLab pushes filtered by branch."},
		{"Image updates", "available", "Schedule a Redeploy step on an image resource; it pulls the tag again."},
		{"Health-gated promotion", "builtin", "Health Checks: a release only takes over once its check passes."},
		{"Automatic rollback", "available", "Trigger on unhealthy or a failed deployment, step: Roll back to the previous good release."},
		{"Rolling deployments", "loadbalancer", "Needs several replicas behind a load balancer; resources run one container."},
		{"Canary releases", "loadbalancer", "Needs traffic splitting between two releases."},
	}},
	{"Reliability & recovery", []Capability{
		{"Service recovery", "available", "Trigger: unhealthy or stopped unexpectedly. Steps: Restart, verified by health check."},
		{"Container restart policies", "builtin", "Resource Limits › When it crashes."},
		{"Incident diagnostics", "available", "Step: Collect diagnostics saves state and logs when something breaks."},
		{"Recovery verification", "builtin", "Every step verifies its outcome; exit code 0 alone isn't treated as proof."},
		{"Escalation", "builtin", "After repeated failures an automation pauses itself and asks for a person."},
	}},
	{"Capacity & performance", []Capability{
		{"CPU and memory thresholds", "available", "Threshold trigger on servers or resources, sustained for N minutes."},
		{"Disk forecasting", "available", "Step: Analyse disk usage projects when the disk fills."},
		{"Capacity recommendations", "builtin", "Resource Limits recommends limits from the 7-day peak."},
		{"Horizontal scaling", "loadbalancer", "Needs replicas and a load balancer."},
		{"Workload placement", "provider", "The deploy engine only targets this machine; remote servers are monitored."},
	}},
	{"Storage & data protection", []Capability{
		{"Safe Docker cleanup", "available", "Step: Safe Docker cleanup, never volumes or rollback images."},
		{"Storage growth alerts", "available", "Threshold trigger on disk, plus a notification step."},
		{"Scheduled volume backups", "agent", "Volume backups can be downloaded by hand under Storage; scheduled, verified backups need backup storage."},
		{"Restore drills", "agent", "Needs scheduled backups first."},
	}},
	{"Networking & certificates", []Capability{
		{"TLS renewal", "builtin", "The domain proxy renews Let's Encrypt certificates on its own."},
		{"DNS verification", "builtin", "Domains are re-checked every minute until they work, then hourly."},
		{"Port exposure audits", "available", "Server diagnostics report published ports and conflicts."},
		{"Firewall drift detection", "agent", needAgent},
	}},
	{"Operating system", []Capability{
		{"Security patches", "agent", needAgent},
		{"Package and kernel updates", "agent", needAgent},
		{"Reboot coordination", "provider", "Needs the agent or a cloud provider's API to reboot and wait for the host."},
		{"OS lifecycle tracking", "available", "Server diagnostics read the OS and kernel the Docker Engine reports."},
	}},
	{"Security & compliance", []Capability{
		{"CVE scanning", "agent", "Needs package inventory from the agent or an image scanner."},
		{"SSH configuration checks", "agent", needAgent},
		{"Privileged container audit", "builtin", "Advanced Settings: privileged mode is admin-only and audited."},
	}},
	{"Fleet operations", []Capability{
		{"Policies across servers", "available", "Target all servers, an environment or a label."},
		{"Staged changes", "available", "Targets run one at a time; stop at the first failure."},
		{"Configuration drift", "available", "Server diagnostics check workload consistency; resources show drift under Advanced Settings."},
		{"Maintenance windows", "available", "Condition: only act inside a window."},
	}},
	{"Workflows", []Capability{
		{"Scheduled jobs", "available", "Schedule trigger, any timezone."},
		{"Approval workflows", "available", "Assisted mode: the plan waits for approval, and expires after 24 hours."},
		{"Webhook-triggered jobs", "available", "Webhook trigger with its own secret URL."},
		{"Notifications", "available", "Notification step, plus alerts on failures and escalations."},
		{"Audit reports", "builtin", "Every run keeps its plan, steps, output and verification; changes go to the Audit Log."},
	}},
}

/* --------------------------------- Templates --------------------------------- */

type Template struct {
	ID          string     `json:"id"`
	Scope       string     `json:"scope"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Mode        string     `json:"mode"`
	Trigger     Trigger    `json:"trigger"`
	Conditions  Conditions `json:"conditions"`
	Steps       []Step     `json:"steps"`
}

var Templates = []Template{
	{ID: "heal", Scope: ScopeResource, Name: "Recover when unhealthy", Mode: ModeAuto,
		Description: "Collect evidence, restart, and verify it passes its health check. Pauses itself if restarts don't help.",
		Trigger:     Trigger{Type: "event", Event: "resource.unhealthy"}, Conditions: Conditions{CooldownMinutes: 15},
		Steps: []Step{{Action: "collect_diagnostics", OnFailure: "continue"}, {Action: "restart"}}},
	{ID: "crash", Scope: ScopeResource, Name: "Investigate crashes", Mode: ModeObserve,
		Description: "When the container stops unexpectedly, save its exit code, restarts and last logs, and recommend a fix.",
		Trigger:     Trigger{Type: "event", Event: "resource.failed"}, Conditions: Conditions{CooldownMinutes: 10},
		Steps: []Step{{Action: "collect_diagnostics"}}},
	{ID: "rollback", Scope: ScopeResource, Name: "Roll back an unhealthy release", Mode: ModeAssisted,
		Description: "When the app turns unhealthy, prepare a rollback to the previous good release for approval.",
		Trigger:     Trigger{Type: "event", Event: "resource.unhealthy"}, Conditions: Conditions{CooldownMinutes: 60},
		Steps: []Step{{Action: "collect_diagnostics", OnFailure: "continue"}, {Action: "rollback"}}},
	{ID: "memory", Scope: ScopeResource, Name: "Memory pressure", Mode: ModeObserve,
		Description: "Flag memory above 90% of the limit for 10 minutes, with evidence, before it gets killed.",
		Trigger:     Trigger{Type: "metric", Metric: "memory", Above: 90, ForMinutes: 10}, Conditions: Conditions{CooldownMinutes: 120},
		Steps: []Step{{Action: "collect_diagnostics"}}},
	{ID: "nightly", Scope: ScopeResource, Name: "Nightly maintenance command", Mode: ModeAuto,
		Description: "Run a command every night and check it succeeds, e.g. clearing expired sessions.",
		Trigger:     Trigger{Type: "schedule", Schedule: "0 3 * * *"},
		Steps:       []Step{{Action: "run_command", Params: map[string]any{"command": "", "timeoutSeconds": 300}}}},
	{ID: "disk", Scope: ScopeServers, Name: "Disk pressure cleanup", Mode: ModeAssisted,
		Description: "When a disk stays above 85%, analyse what's using it and prepare a safe cleanup for approval.",
		Trigger:     Trigger{Type: "metric", Metric: "disk", Above: 85, ForMinutes: 15}, Conditions: Conditions{CooldownMinutes: 360},
		Steps: []Step{{Action: "disk_report", OnFailure: "continue"}, {Action: "docker_cleanup"}, {Action: "disk_report"}}},
	{ID: "weekly-cleanup", Scope: ScopeServers, Name: "Weekly Docker cleanup", Mode: ModeAuto,
		Description: "Every Sunday night, remove unused containers, untagged images and build cache inside a maintenance window.",
		Trigger:     Trigger{Type: "schedule", Schedule: "0 3 * * 0"},
		Steps:       []Step{{Action: "docker_cleanup"}}},
	{ID: "offline", Scope: ScopeServers, Name: "Server went offline", Mode: ModeAuto,
		Description: "When a server stops answering, try to reconnect, run diagnostics and tell someone if it's still down.",
		Trigger:     Trigger{Type: "event", Event: "server.offline"}, Conditions: Conditions{CooldownMinutes: 30},
		Steps: []Step{{Action: "refresh", OnFailure: "continue"}, {Action: "diagnostics", OnFailure: "continue"}}},
	{ID: "daily-diagnostics", Scope: ScopeServers, Name: "Daily health report", Mode: ModeObserve,
		Description: "Run diagnostics every morning and surface anything that failed.",
		Trigger:     Trigger{Type: "schedule", Schedule: "0 7 * * *"},
		Steps:       []Step{{Action: "diagnostics"}}},
}
