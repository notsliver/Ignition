package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/worker"
)

/*
 * Health checks are probed by the worker (it joins the app's private network). While one is enabled, a
 * deployment only takes over once its health check passes, and a running app that keeps failing is
 * restarted (with a limit, so a broken app isn't restarted forever).
 */

func validateHealth(c *store.HealthConfig, resourcePort *int) map[string]string {
	f := map[string]string{}
	c.Path = strings.TrimSpace(c.Path)
	c.Command = strings.TrimSpace(c.Command)
	c.ExpectStatus = strings.ReplaceAll(strings.TrimSpace(c.ExpectStatus), " ", "")
	switch c.Kind {
	case "http":
		if !strings.HasPrefix(c.Path, "/") || strings.ContainsAny(c.Path, " \t\r\n") || len(c.Path) > 500 {
			f["path"] = "Enter a path starting with /, like /health."
		}
		if c.ExpectStatus == "" {
			c.ExpectStatus = "200-399"
		}
		if !worker.ValidStatusSpec(c.ExpectStatus) {
			f["expectStatus"] = "Use status codes like 200, 200-299, 2xx or 200,204."
		}
	case "tcp":
	case "command":
		if c.Command == "" {
			f["command"] = "Enter the command to run, e.g. pg_isready."
		} else if len(c.Command) > 1000 {
			f["command"] = "That command is too long."
		}
	default:
		f["kind"] = "Choose HTTP, TCP or Command."
	}
	if c.Kind != "command" {
		if c.Port != nil && (*c.Port < 1 || *c.Port > 65535) {
			f["port"] = "Ports are numbers from 1 to 65535."
		} else if c.Port == nil && resourcePort == nil {
			f["port"] = "This resource has no port. Enter the one to check."
		}
	}
	if c.IntervalSeconds < 2 || c.IntervalSeconds > 3600 {
		f["intervalSeconds"] = "Between 2 seconds and 1 hour."
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 120 {
		f["timeoutSeconds"] = "Between 1 and 120 seconds."
	} else if c.TimeoutSeconds > c.IntervalSeconds && f["intervalSeconds"] == "" {
		f["timeoutSeconds"] = "Must be shorter than the interval."
	}
	if c.Retries < 1 || c.Retries > 20 {
		f["retries"] = "Between 1 and 20."
	}
	if c.StartPeriod < 0 || c.StartPeriod > 900 {
		f["startPeriodSeconds"] = "Between 0 and 900 seconds."
	}
	return f
}

func (s *Server) getHealth(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	cfg, state, err := s.Store.GetHealth(r.Context(), id)
	if err != nil {
		return err
	}
	results, err := s.Store.HealthResults(r.Context(), id, 60)
	if err != nil {
		return err
	}
	if !cfg.Enabled && cfg.Port == nil && res.Type == "database" {
		cfg.Kind = "tcp" // a sensible first suggestion; nothing is saved until the user saves
	}
	httpx.JSON(w, 200, map[string]any{"config": cfg, "state": state, "results": results, "resourcePort": res.Port, "status": res.Status})
	return nil
}

func (s *Server) putHealth(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	var c store.HealthConfig
	if err := httpx.Decode(r, &c); err != nil {
		return err
	}
	if f := validateHealth(&c, res.Port); len(f) > 0 {
		return httpx.Invalid(f)
	}
	if err := s.Store.PutHealth(r.Context(), id, c); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "resource.health", "resource", id, map[string]any{"enabled": c.Enabled, "kind": c.Kind}, clientIP(r))
	cfg, state, _ := s.Store.GetHealth(r.Context(), id)
	httpx.JSON(w, 200, map[string]any{"config": cfg, "state": state})
	return nil
}

// testHealth runs one check with the settings being edited (saved or not) and waits for the answer.
func (s *Server) testHealth(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	var c store.HealthConfig
	if err := httpx.Decode(r, &c); err != nil {
		return err
	}
	if c.IntervalSeconds == 0 {
		c.IntervalSeconds = 120 // the interval doesn't matter for one check
	}
	if f := validateHealth(&c, res.Port); len(f) > 0 {
		return httpx.Invalid(f)
	}
	if !s.limiter.allow("health-test:" + id) {
		return httpx.TooMany("That's a lot of checks in a minute. Try again shortly.")
	}
	jobID, err := jobs.Enqueue(r.Context(), s.Store.DB, jobs.NewJob{Kind: jobs.KindHealth, ResourceID: id, CreatedBy: u.ID, Payload: map[string]any{"config": c}, MaxAttempts: 1})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(c.TimeoutSeconds+20)*time.Second)
	defer cancel()
	for {
		var status, progress, lastErr string
		if err := s.Store.DB.QueryRow(ctx, `SELECT status, progress, last_error FROM jobs WHERE id = $1`, jobID).Scan(&status, &progress, &lastErr); err != nil {
			return err
		}
		switch status {
		case "succeeded":
			var out map[string]any
			json.Unmarshal([]byte(progress), &out)
			httpx.JSON(w, 200, map[string]any{"result": out})
			return nil
		case "failed", "cancelled":
			return httpx.Unavailable("The check couldn't run: " + lastErr)
		}
		select {
		case <-ctx.Done():
			return httpx.Unavailable("The worker didn't pick up the check. Is it running and not busy with a long build?")
		case <-time.After(300 * time.Millisecond):
		}
	}
}
