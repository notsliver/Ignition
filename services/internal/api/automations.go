package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/automations"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/worker"
)

/*
 * Automations API. Resource automations can be managed by developers (they can already deploy and
 * restart that resource); server automations act on infrastructure and need an admin. The worker
 * does the work; this layer validates, plans previews and records decisions.
 */

const automationHookPrefix = "iga_"

type automationInput struct {
	Name           string                 `json:"name"`
	Description    string                 `json:"description"`
	Scope          string                 `json:"scope"`
	ResourceID     string                 `json:"resourceId"`
	Targets        automations.Targets    `json:"targets"`
	Trigger        automations.Trigger    `json:"trigger"`
	Conditions     automations.Conditions `json:"conditions"`
	Steps          []automations.Step     `json:"steps"`
	Mode           string                 `json:"mode"`
	Enabled        *bool                  `json:"enabled"`
	EscalateAfter  int                    `json:"escalateAfter"`
	MaxRunsPerHour int                    `json:"maxRunsPerHour"`
	NotifyURL      *string                `json:"notifyUrl"` // nil keeps the saved one; "" removes it
}

func (in *automationInput) spec(hasNotify bool) automations.Automation {
	notify := ""
	if in.NotifyURL != nil {
		notify = strings.TrimSpace(*in.NotifyURL)
	} else if hasNotify {
		notify = "https://saved.invalid" // a saved URL satisfies "notification steps need a webhook"
	}
	return automations.Automation{Name: in.Name, Description: in.Description, Scope: in.Scope, Mode: in.Mode, Targets: in.Targets, Trigger: in.Trigger,
		Conditions: in.Conditions, Steps: in.Steps, EscalateAfter: in.EscalateAfter, MaxRunsPerHour: in.MaxRunsPerHour, NotifyURL: notify}
}

func canManageAutomation(u *store.User, scope string) bool {
	if scope == automations.ScopeServers {
		return can(u, "automations.manage") && can(u, "servers.update")
	}
	return can(u, "automations.manage")
}

// canRunAutomation: running or deciding on server-scoped automations also needs server rights.
func canRunAutomation(u *store.User, scope string) bool {
	if scope == automations.ScopeServers {
		return can(u, "automations.run") && can(u, "servers.update")
	}
	return can(u, "automations.run")
}

func forbiddenFor(scope string) error {
	if scope == automations.ScopeServers {
		return httpx.Forbidden("Server automations act on infrastructure, so only admins can change or run them.")
	}
	return httpx.Forbidden("Viewers can't change or run automations.")
}

type automationView struct {
	store.Automation
	Workflow []string `json:"workflow"`
}

func viewOf(a store.Automation) automationView {
	spec := worker.ParseSpec(&a)
	return automationView{Automation: a, Workflow: automations.Workflow(&spec)}
}

func (s *Server) automationCatalog(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	actions := make([]automations.Action, len(automations.Actions))
	for i, a := range automations.Actions {
		if a.Params == nil {
			a.Params = []automations.Param{} // [] rather than null for the builder
		}
		actions[i] = a
	}
	httpx.JSON(w, 200, map[string]any{"actions": actions, "events": automations.Events, "metrics": automations.Metrics,
		"catalog": automations.Catalog, "templates": automations.Templates})
	return nil
}

// serverIndex lists every server with what a selector would need to match it.
func (s *Server) serverIndex(ctx context.Context) ([]store.Server, error) {
	list, _, err := s.Store.ListServers(ctx, store.ServerQuery{Limit: 1000, Sort: "name"})
	return list, err
}

// listAutomations returns one resource's automations, or every server automation that can act on
// a server, with the counts and items that need someone.
func (s *Server) listAutomations(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	ctx := r.Context()
	resourceID, serverID := r.URL.Query().Get("resourceId"), r.URL.Query().Get("serverId")
	var list []store.Automation
	var err error
	out := map[string]any{}
	switch {
	case resourceID != "":
		if !uuidRE.MatchString(resourceID) {
			return httpx.NotFound("Resource")
		}
		list, err = s.Store.ListAutomations(ctx, store.AutomationFilter{ResourceID: resourceID})
	case serverID != "":
		srv, err2 := s.Store.GetServer(ctx, serverID)
		if err2 != nil {
			return notFound(err2, "Server")
		}
		all, err2 := s.Store.ListAutomations(ctx, store.AutomationFilter{Scope: automations.ScopeServers})
		if err2 != nil {
			return err2
		}
		for _, a := range all {
			var t automations.Targets
			json.Unmarshal(a.Targets, &t)
			if t.Matches(srv.ID, srv.Environment, srv.Labels) {
				list = append(list, a)
			}
		}
		// Resource automations on this server's workloads show up as a count, linking there.
		var res int
		s.Store.DB.QueryRow(ctx, `SELECT count(*) FROM automations a JOIN resources r ON r.id = a.resource_id WHERE r.server_id = $1 AND a.enabled`, srv.ID).Scan(&res)
		out["resourceAutomations"] = res
		servers, _ := s.serverIndex(ctx)
		out["managedServers"] = len(servers)
	default:
		return httpx.BadRequest("Pass resourceId or serverId.")
	}
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(list))
	views := make([]automationView, 0, len(list))
	for _, a := range list {
		ids = append(ids, a.ID)
		views = append(views, viewOf(a))
	}
	counts, err := s.Store.CountAutomations(ctx, ids)
	if err != nil {
		return err
	}
	attention, err := s.Store.AttentionRuns(ctx, ids)
	if err != nil {
		return err
	}
	out["automations"], out["counts"], out["attention"] = views, counts, attention
	httpx.JSON(w, 200, out)
	return nil
}

func (s *Server) loadAutomation(r *http.Request) (*store.Automation, error) {
	id, err := pathID(r, "Automation")
	if err != nil {
		return nil, err
	}
	a, err := s.Store.GetAutomation(r.Context(), id)
	if err != nil {
		return nil, notFound(err, "Automation")
	}
	return a, nil
}

func (s *Server) getAutomation(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	a, err := s.loadAutomation(r)
	if err != nil {
		return err
	}
	runs, err := s.Store.AutomationRuns(r.Context(), a.ID, 25)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"automation": viewOf(*a), "runs": runs})
	return nil
}

// validateAutomation checks a draft and returns the stored row parts plus field errors.
func (s *Server) validateAutomation(ctx context.Context, in *automationInput, existing *store.Automation) (*store.Automation, map[string]string, error) {
	if existing != nil {
		in.Scope = existing.Scope
		if existing.ResourceID != nil {
			in.ResourceID = *existing.ResourceID
		}
	}
	f := map[string]string{}
	switch in.Scope {
	case automations.ScopeResource:
		if !uuidRE.MatchString(in.ResourceID) {
			f["resourceId"] = "Pick the resource."
		} else if _, err := s.Store.GetResource(ctx, in.ResourceID); err != nil {
			f["resourceId"] = "That resource doesn't exist."
		}
		in.Targets = automations.Targets{}
	case automations.ScopeServers:
	default:
		return nil, map[string]string{"scope": "Automations act on a resource or on servers."}, nil
	}
	spec := in.spec(existing != nil && existing.HasNotify)
	for k, v := range automations.Validate(&spec) {
		f[k] = v
	}
	if len(f) > 0 {
		return nil, f, nil
	}
	in.Trigger, in.Conditions, in.Targets, in.Steps = spec.Trigger, spec.Conditions, spec.Targets, spec.Steps
	row := &store.Automation{Name: spec.Name, Description: strings.TrimSpace(in.Description), Scope: in.Scope, Mode: spec.Mode,
		EscalateAfter: spec.EscalateAfter, MaxRunsPerHour: spec.MaxRunsPerHour, Enabled: in.Enabled == nil || *in.Enabled}
	if in.Scope == automations.ScopeResource {
		row.ResourceID = &in.ResourceID
	}
	row.Targets, _ = json.Marshal(spec.Targets)
	row.Trigger, _ = json.Marshal(spec.Trigger)
	row.Conditions, _ = json.Marshal(spec.Conditions)
	row.Steps, _ = json.Marshal(spec.Steps)
	if spec.Trigger.Type == "schedule" && row.Enabled {
		row.NextRunAt, _ = worker.NextRun(spec.Trigger.Schedule, spec.Trigger.Timezone, time.Now())
	}
	return row, nil, nil
}

func (s *Server) sealNotify(in *automationInput, id string) ([]byte, error) {
	if in.NotifyURL == nil {
		return nil, nil
	}
	if strings.TrimSpace(*in.NotifyURL) == "" {
		return []byte{}, nil
	}
	return s.Secrets.Seal(strings.TrimSpace(*in.NotifyURL), "automation:"+id)
}

func (s *Server) createAutomation(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in automationInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if !canManageAutomation(u, in.Scope) {
		return forbiddenFor(in.Scope)
	}
	row, f, err := s.validateAutomation(r.Context(), &in, nil)
	if err != nil {
		return err
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	id, err := s.Store.SaveAutomation(r.Context(), row, nil, u.ID)
	if err != nil {
		return err
	}
	if in.NotifyURL != nil && *in.NotifyURL != "" { // sealed with the ID it's bound to
		sealed, err := s.sealNotify(&in, id)
		if err != nil {
			return err
		}
		row.ID = id
		if _, err := s.Store.SaveAutomation(r.Context(), row, sealed, u.ID); err != nil {
			return err
		}
	}
	s.Store.Audit(r.Context(), u, "automation.create", automationTargetType(row), automationTargetID(row, id), map[string]any{"name": row.Name, "mode": row.Mode, "automation": id}, clientIP(r))
	a, _ := s.Store.GetAutomation(r.Context(), id)
	httpx.JSON(w, 201, map[string]any{"automation": viewOf(*a)})
	return nil
}

func automationTargetType(a *store.Automation) string {
	if a.ResourceID != nil {
		return "resource"
	}
	return "automation"
}

func automationTargetID(a *store.Automation, id string) string {
	if a.ResourceID != nil {
		return *a.ResourceID
	}
	return id
}

func (s *Server) updateAutomation(w http.ResponseWriter, r *http.Request, u *store.User) error {
	existing, err := s.loadAutomation(r)
	if err != nil {
		return err
	}
	if !canManageAutomation(u, existing.Scope) {
		return forbiddenFor(existing.Scope)
	}
	var in automationInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	row, f, err := s.validateAutomation(r.Context(), &in, existing)
	if err != nil {
		return err
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	sealed, err := s.sealNotify(&in, existing.ID)
	if err != nil {
		return err
	}
	row.ID = existing.ID
	if _, err := s.Store.SaveAutomation(r.Context(), row, sealed, u.ID); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "automation.update", automationTargetType(existing), automationTargetID(existing, existing.ID), map[string]any{"name": row.Name, "mode": row.Mode, "enabled": row.Enabled, "automation": existing.ID}, clientIP(r))
	a, _ := s.Store.GetAutomation(r.Context(), existing.ID)
	httpx.JSON(w, 200, map[string]any{"automation": viewOf(*a)})
	return nil
}

func (s *Server) deleteAutomation(w http.ResponseWriter, r *http.Request, u *store.User) error {
	a, err := s.loadAutomation(r)
	if err != nil {
		return err
	}
	if !canManageAutomation(u, a.Scope) {
		return forbiddenFor(a.Scope)
	}
	if err := s.Store.DeleteAutomation(r.Context(), a.ID); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "automation.delete", automationTargetType(a), automationTargetID(a, a.ID), map[string]any{"name": a.Name}, clientIP(r))
	w.WriteHeader(204)
	return nil
}

// previewAutomation validates a draft and shows its workflow and what it would act on now.
func (s *Server) previewAutomation(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	var in automationInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	spec := in.spec(true)
	f := automations.Validate(&spec)
	var targets []string
	if in.Scope == automations.ScopeServers {
		list, err := s.serverIndex(r.Context())
		if err != nil {
			return err
		}
		for _, srv := range list {
			if !srv.Removing && spec.Targets.Matches(srv.ID, srv.Environment, srv.Labels) {
				targets = append(targets, srv.Name)
			}
		}
	}
	var next []time.Time
	if spec.Trigger.Type == "schedule" && f["trigger"] == "" {
		t := time.Now()
		for i := 0; i < 3; i++ {
			n, _ := worker.NextRun(spec.Trigger.Schedule, spec.Trigger.Timezone, t)
			if n == nil {
				break
			}
			next, t = append(next, *n), *n
		}
	}
	httpx.JSON(w, 200, map[string]any{"workflow": automations.Workflow(&spec), "errors": f, "targets": targets, "nextRuns": next,
		"disruptive": automations.Disruptive(spec.Steps), "inWindowNow": automations.InWindow(spec.Conditions.Window, time.Now())})
	return nil
}

func (s *Server) runAutomation(w http.ResponseWriter, r *http.Request, u *store.User) error {
	a, err := s.loadAutomation(r)
	if err != nil {
		return err
	}
	if !canRunAutomation(u, a.Scope) {
		return forbiddenFor(a.Scope)
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeOptional(r, &in); err != nil {
		return err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "Run by " + u.Email
	}
	id, status, err := s.Store.CreateAutomationRun(r.Context(), a.ID, "manual", truncate(reason, 200), u.Email, nil)
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "automation.run", automationTargetType(a), automationTargetID(a, a.ID), map[string]any{"name": a.Name, "run": id}, clientIP(r))
	run, _ := s.Store.GetAutomationRun(r.Context(), id)
	code := 202
	if status == "skipped" {
		code = 200
	}
	httpx.JSON(w, code, map[string]any{"run": run})
	return nil
}

func (s *Server) getAutomationRun(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Run")
	if err != nil {
		return err
	}
	run, err := s.Store.GetAutomationRun(r.Context(), id)
	if err != nil {
		return notFound(err, "Run")
	}
	httpx.JSON(w, 200, map[string]any{"run": run})
	return nil
}

// decideAutomationRun approves, rejects, acknowledges or cancels a run.
func (s *Server) decideAutomationRun(decision string) func(http.ResponseWriter, *http.Request, *store.User) error {
	return func(w http.ResponseWriter, r *http.Request, u *store.User) error {
		id, err := pathID(r, "Run")
		if err != nil {
			return err
		}
		run, err := s.Store.GetAutomationRun(r.Context(), id)
		if err != nil {
			return notFound(err, "Run")
		}
		a, err := s.Store.GetAutomation(r.Context(), run.AutomationID)
		if err != nil {
			return notFound(err, "Automation")
		}
		if !canRunAutomation(u, a.Scope) {
			return forbiddenFor(a.Scope)
		}
		var in struct {
			Note string `json:"note"`
		}
		if err := httpx.DecodeOptional(r, &in); err != nil {
			return err
		}
		ok := true
		switch decision {
		case "approve":
			ok, err = s.Store.DecideRun(r.Context(), id, "approved", u.Email, truncate(strings.TrimSpace(in.Note), 300))
		case "reject":
			ok, err = s.Store.DecideRun(r.Context(), id, "rejected", u.Email, truncate(strings.TrimSpace(in.Note), 300))
		case "acknowledge":
			err = s.Store.AcknowledgeRun(r.Context(), id, u.Email)
		case "cancel":
			ok, err = s.Store.CancelAutomationRun(r.Context(), id)
		}
		if err != nil {
			return err
		}
		if !ok {
			return httpx.Conflict("This run isn't waiting for that any more (it may have expired or already been decided).")
		}
		s.Store.Audit(r.Context(), u, "automation."+decision, automationTargetType(a), automationTargetID(a, a.ID), map[string]any{"name": a.Name, "run": id}, clientIP(r))
		run, _ = s.Store.GetAutomationRun(r.Context(), id)
		httpx.JSON(w, 200, map[string]any{"run": run})
		return nil
	}
}

// createAutomationHook makes (or replaces) the secret URL that triggers the automation.
func (s *Server) createAutomationHook(w http.ResponseWriter, r *http.Request, u *store.User) error {
	a, err := s.loadAutomation(r)
	if err != nil {
		return err
	}
	if !canManageAutomation(u, a.Scope) {
		return forbiddenFor(a.Scope)
	}
	tok, hash := newSecret(automationHookPrefix)
	if err := s.Store.SetAutomationHook(r.Context(), a.ID, hash); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "automation.hook", automationTargetType(a), automationTargetID(a, a.ID), map[string]any{"name": a.Name}, clientIP(r))
	httpx.JSON(w, 201, map[string]string{"url": strings.TrimRight(s.publicURL(), "/") + "/api/automation-hooks/" + tok})
	return nil
}

// automationHook is POST /api/automation-hooks/{token}: the URL is the credential.
func (s *Server) automationHook(w http.ResponseWriter, r *http.Request) error {
	tok := r.PathValue("token")
	if !strings.HasPrefix(tok, automationHookPrefix) || len(tok) > 100 {
		return httpx.NotFound("Hook")
	}
	if !s.limiter.allow("ahook:" + tok[len(tok)-8:]) {
		return httpx.TooMany("Too many calls. Wait a minute.")
	}
	a, err := s.Store.AutomationByHook(r.Context(), auth.HashToken(tok))
	if errors.Is(err, store.ErrNotFound) {
		return httpx.NotFound("Hook")
	}
	if err != nil {
		return err
	}
	if !a.Enabled {
		return httpx.Conflict("This automation is paused.")
	}
	var body struct {
		Reason string `json:"reason"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body)
	reason := "Webhook call"
	if strings.TrimSpace(body.Reason) != "" {
		reason = "Webhook: " + truncate(strings.TrimSpace(body.Reason), 180)
	}
	id, status, err := s.Store.CreateAutomationRun(r.Context(), a.ID, "webhook", reason, "webhook "+clientIP(r), nil)
	if err != nil {
		return err
	}
	httpx.JSON(w, 202, map[string]any{"runId": id, "status": status})
	return nil
}

// testAutomationNotify sends a test message to the saved webhook.
func (s *Server) testAutomationNotify(w http.ResponseWriter, r *http.Request, u *store.User) error {
	a, err := s.loadAutomation(r)
	if err != nil {
		return err
	}
	if !canManageAutomation(u, a.Scope) {
		return forbiddenFor(a.Scope)
	}
	if !a.HasNotify {
		return httpx.Conflict("Save a webhook URL first.")
	}
	url, err := s.Secrets.Open(a.NotifySealed, "automation:"+a.ID)
	if err != nil {
		return httpx.Conflict("The webhook URL can't be decrypted. Enter it again.")
	}
	text := "Test from Ignition: notifications for " + a.Name + " arrive here."
	b, _ := json.Marshal(map[string]any{"text": text, "content": text, "kind": "test", "automation": a.Name})
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return httpx.Unavailable("The webhook didn't answer: " + err.Error())
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return httpx.Unavailable("The webhook answered " + resp.Status + ".")
	}
	httpx.JSON(w, 200, map[string]string{"status": resp.Status})
	return nil
}
