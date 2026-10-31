package api

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Project-wide pages: deployments, monitoring and logs across every resource of a project
 * (optionally one environment), its environments, and its activity.
 */

func (s *Server) projectFor(r *http.Request, u *store.User) (*store.Project, error) {
	id, err := pathID(r, "Project")
	if err != nil {
		return nil, err
	}
	p, err := s.Store.GetProject(r.Context(), u.ID, id)
	return p, notFound(err, "Project")
}

// envFilter resolves ?env= (an environment name or ID) to an ID of this project, "" for all.
func envFilter(r *http.Request, p *store.Project) string {
	v := r.URL.Query().Get("env")
	for _, e := range p.Environments {
		if v == e.ID || v == e.Name {
			return e.ID
		}
	}
	return ""
}

func (s *Server) projectDeployments(w http.ResponseWriter, r *http.Request, u *store.User) error {
	p, err := s.projectFor(r, u)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	env := envFilter(r, p)
	list, total, err := s.Store.ListProjectDeployments(r.Context(), p.ID, env, q.Get("resource"),
		store.DeploymentQuery{Q: strings.TrimSpace(q.Get("q")), Status: q.Get("status"), Limit: limit, Offset: offset})
	if err != nil {
		return err
	}
	stats, err := s.Store.ProjectDeployStats(r.Context(), p.ID, env, 14)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"deployments": list, "total": total, "stats": stats})
	return nil
}

func (s *Server) projectMetrics(w http.ResponseWriter, r *http.Request, u *store.User) error {
	p, err := s.projectFor(r, u)
	if err != nil {
		return err
	}
	rng := r.URL.Query().Get("range")
	spec, ok := metricRanges[rng]
	if !ok {
		rng, spec = "1h", metricRanges["1h"]
	}
	buckets, resources, err := s.Store.ProjectUsage(r.Context(), p.ID, envFilter(r, p), spec[0], spec[1])
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"range": rng, "stepSeconds": int(spec[1].Seconds()), "buckets": buckets, "resources": resources,
		"sampleIntervalSeconds": 15, "retentionDays": 7})
	return nil
}

type projectLogLine struct {
	Time       time.Time `json:"time"`
	Stream     string    `json:"stream"`
	Message    string    `json:"message"`
	Resource   string    `json:"resource"`
	ResourceID string    `json:"resourceId"`
	Level      string    `json:"level"` // error, warn, info: guessed from the line, for filtering
}

var (
	errorWords = regexp.MustCompile(`(?i)\b(error|err|fatal|panic|exception|failed|traceback|critical)\b`)
	warnWords  = regexp.MustCompile(`(?i)\b(warn|warning|deprecated)\b`)
)

func levelOf(stream, msg string) string {
	switch {
	case errorWords.MatchString(msg):
		return "error"
	case warnWords.MatchString(msg):
		return "warn"
	case stream == "stderr":
		return "warn"
	}
	return "info"
}

// projectLogs merges the runtime output of the project's running resources, newest last.
// ?since= (unix seconds) returns only newer lines, for live tailing.
func (s *Server) projectLogs(w http.ResponseWriter, r *http.Request, u *store.User) error {
	p, err := s.projectFor(r, u)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	tail, _ := strconv.Atoi(q.Get("tail"))
	if tail <= 0 || tail > 2000 {
		tail = 300
	}
	// ?since= is the exact timestamp of the newest line the client has (RFC 3339, nanoseconds),
	// or unix seconds. Only strictly newer lines come back.
	var since time.Time
	if t, err := time.Parse(time.RFC3339Nano, q.Get("since")); err == nil {
		since = t
	} else if v, err := strconv.ParseFloat(q.Get("since"), 64); err == nil && v > 0 {
		since = time.Unix(0, int64(v*1e9))
	}
	only := map[string]bool{}
	for _, id := range strings.Split(q.Get("resources"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			only[id] = true
		}
	}
	env := envFilter(r, p)
	all, err := s.Store.ListResources(r.Context(), p.ID, env, "", "")
	if err != nil {
		return err
	}
	type source struct {
		ID, Name, Status, Environment string
		Running                       bool
	}
	sources := []source{}
	var targets []*store.Resource
	for i := range all {
		res := &all[i]
		if res.SourceType == "external" {
			continue
		}
		running := res.ContainerID != "" && res.Status == "running"
		sources = append(sources, source{res.ID, res.Name, res.Status, res.Environment, running})
		if running && (len(only) == 0 || only[res.ID]) {
			targets = append(targets, res)
		}
	}
	if len(targets) > 25 {
		targets = targets[:25]
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	lines := []projectLogLine{}
	failed := map[string]string{}
	for _, res := range targets {
		wg.Add(1)
		go func(res *store.Resource) {
			defer wg.Done()
			out, err := s.Runtime.Logs(ctx, res.ContainerID, tail, since)
			if err != nil {
				mu.Lock()
				failed[res.Name] = err.Error()
				mu.Unlock()
				return
			}
			redact := s.redactor(ctx, res.ID)
			batch := make([]projectLogLine, 0, len(out))
			for _, l := range out {
				// Docker's since filter has one-second precision; drop what the client already has.
				if !since.IsZero() && !l.Time.After(since) {
					continue
				}
				msg := l.Message
				if redact != nil {
					msg = redact(msg)
				}
				batch = append(batch, projectLogLine{l.Time, l.Stream, msg, res.Name, res.ID, levelOf(l.Stream, msg)})
			}
			mu.Lock()
			lines = append(lines, batch...)
			mu.Unlock()
		}(res)
	}
	wg.Wait()
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].Time.Before(lines[j].Time) })
	if since.IsZero() && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	httpx.JSON(w, 200, map[string]any{"lines": lines, "sources": sources, "failed": failed})
	return nil
}

func (s *Server) projectActivity(w http.ResponseWriter, r *http.Request, u *store.User) error {
	p, err := s.projectFor(r, u)
	if err != nil {
		return err
	}
	list, err := s.Store.ProjectActivity(r.Context(), p.ID, 100)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"events": list})
	return nil
}

/* ------------------------------- Environments -------------------------------- */

var envNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$`)

func (s *Server) listEnvironments(w http.ResponseWriter, r *http.Request, u *store.User) error {
	p, err := s.projectFor(r, u)
	if err != nil {
		return err
	}
	list, err := s.Store.ProjectEnvironments(r.Context(), p.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"environments": list})
	return nil
}

func (s *Server) createEnvironment(w http.ResponseWriter, r *http.Request, u *store.User) error {
	p, err := s.projectFor(r, u)
	if err != nil {
		return err
	}
	var in struct{ Name string }
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	if !envNameRE.MatchString(in.Name) {
		return httpx.Invalid(map[string]string{"name": "Up to 40 letters, digits, spaces, dots, dashes or underscores."})
	}
	if s.Store.EnvironmentNameTaken(r.Context(), p.ID, in.Name, "") {
		return httpx.Invalid(map[string]string{"name": p.Name + " already has an environment called " + in.Name + "."})
	}
	if len(p.Environments) >= 10 {
		return httpx.Invalid(map[string]string{"name": "Up to 10 environments per project."})
	}
	id, err := s.Store.CreateEnvironment(r.Context(), p.ID, in.Name)
	if store.IsUnique(err) {
		return httpx.Invalid(map[string]string{"name": p.Name + " already has an environment called " + in.Name + "."})
	}
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "environment.create", "project", p.ID, map[string]any{"name": in.Name}, clientIP(r))
	httpx.JSON(w, 201, map[string]any{"id": id})
	return nil
}

func (s *Server) renameEnvironment(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Environment")
	if err != nil {
		return err
	}
	projectID, old, _, err := s.Store.EnvironmentProject(r.Context(), id)
	if err != nil {
		return notFound(err, "Environment")
	}
	var in struct{ Name string }
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	if !envNameRE.MatchString(in.Name) {
		return httpx.Invalid(map[string]string{"name": "Up to 40 letters, digits, spaces, dots, dashes or underscores."})
	}
	if s.Store.EnvironmentNameTaken(r.Context(), projectID, in.Name, id) {
		return httpx.Invalid(map[string]string{"name": "Another environment already has this name."})
	}
	if err := s.Store.RenameEnvironment(r.Context(), id, in.Name); store.IsUnique(err) {
		return httpx.Invalid(map[string]string{"name": "Another environment already has this name."})
	} else if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "environment.rename", "project", projectID, map[string]any{"from": old, "to": in.Name}, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}

func (s *Server) deleteEnvironment(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Environment")
	if err != nil {
		return err
	}
	projectID, name, resources, err := s.Store.EnvironmentProject(r.Context(), id)
	if err != nil {
		return notFound(err, "Environment")
	}
	if resources > 0 {
		return httpx.Conflict(name + " still has " + strconv.Itoa(resources) + " resource(s). Move or delete them first.")
	}
	if err := s.Store.DeleteEnvironment(r.Context(), id); err != nil {
		return httpx.Conflict("A project keeps at least one environment.")
	}
	s.Store.Audit(r.Context(), u, "environment.delete", "project", projectID, map[string]any{"name": name}, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}

func (s *Server) orderEnvironments(w http.ResponseWriter, r *http.Request, u *store.User) error {
	p, err := s.projectFor(r, u)
	if err != nil {
		return err
	}
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	known := map[string]bool{}
	for _, e := range p.Environments {
		known[e.ID] = true
	}
	if len(in.IDs) != len(known) {
		return httpx.Invalid(map[string]string{"ids": "List every environment once."})
	}
	for _, id := range in.IDs {
		if !known[id] {
			return httpx.Invalid(map[string]string{"ids": "Unknown environment."})
		}
		delete(known, id)
	}
	if err := s.Store.ReorderEnvironments(r.Context(), p.ID, in.IDs); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "environment.reorder", "project", p.ID, nil, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}
