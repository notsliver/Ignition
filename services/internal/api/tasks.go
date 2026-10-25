package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/notsliver/Ignition/services/internal/cron"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/worker"
)

type taskInput struct {
	Name           *string `json:"name"`
	Schedule       *string `json:"schedule"`
	Timezone       *string `json:"timezone"`
	Command        *string `json:"command"`
	TimeoutSeconds *int    `json:"timeoutSeconds"`
	Enabled        *bool   `json:"enabled"`
}

// apply merges the input into t and validates the result.
func (in taskInput) apply(t *store.Task) map[string]string {
	if in.Name != nil {
		t.Name = strings.TrimSpace(*in.Name)
	}
	if in.Schedule != nil {
		t.Schedule = strings.Join(strings.Fields(*in.Schedule), " ")
	}
	if in.Timezone != nil {
		t.Timezone = strings.TrimSpace(*in.Timezone)
	}
	if in.Command != nil {
		t.Command = strings.TrimSpace(*in.Command)
	}
	if in.TimeoutSeconds != nil {
		t.TimeoutSeconds = *in.TimeoutSeconds
	}
	if in.Enabled != nil {
		t.Enabled = *in.Enabled
	}
	f := map[string]string{}
	if t.Name == "" || len(t.Name) > 80 {
		f["name"] = "Give the task a name (up to 80 characters)."
	}
	if t.Timezone == "" {
		t.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(t.Timezone); err != nil {
		f["timezone"] = "Unknown timezone."
	}
	if _, err := cron.Parse(t.Schedule); err != nil {
		f["schedule"] = capitalize(err.Error()) + "."
	}
	if t.Command == "" {
		f["command"] = "Enter the command to run."
	} else if len(t.Command) > 4000 {
		f["command"] = "That command is too long."
	}
	if t.TimeoutSeconds < 10 || t.TimeoutSeconds > 86400 {
		f["timeoutSeconds"] = "Between 10 seconds and 24 hours."
	}
	if len(f) == 0 {
		t.NextRunAt = nil
		if t.Enabled {
			t.NextRunAt, _ = worker.NextRun(t.Schedule, t.Timezone, time.Now())
		}
	}
	return f
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

type taskView struct {
	store.Task
	Description string `json:"description"`
}

func describe(t store.Task) taskView {
	return taskView{Task: t, Description: cron.Describe(t.Schedule)}
}

func uniqueTaskName(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	list, err := s.Store.ListTasks(r.Context(), id)
	if err != nil {
		return err
	}
	out := make([]taskView, 0, len(list))
	for _, t := range list {
		out = append(out, describe(t))
	}
	httpx.JSON(w, 200, map[string]any{"tasks": out, "status": res.Status})
	return nil
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	if _, err := s.Store.GetResource(r.Context(), id); err != nil {
		return notFound(err, "Resource")
	}
	var in taskInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	t := store.Task{ResourceID: id, TimeoutSeconds: 600, Enabled: true, Timezone: "UTC"}
	if f := in.apply(&t); len(f) > 0 {
		return httpx.Invalid(f)
	}
	taskID, err := s.Store.CreateTask(r.Context(), t, u.ID)
	if uniqueTaskName(err) {
		return httpx.Invalid(map[string]string{"name": "Another task of this resource has that name."})
	}
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "task.create", "resource", id, map[string]any{"task": t.Name, "schedule": t.Schedule}, clientIP(r))
	created, _ := s.Store.GetTask(r.Context(), taskID)
	httpx.JSON(w, 201, map[string]any{"task": describe(*created)})
	return nil
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Task")
	if err != nil {
		return err
	}
	t, err := s.Store.GetTask(r.Context(), id)
	if err != nil {
		return notFound(err, "Task")
	}
	var in taskInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if f := in.apply(t); len(f) > 0 {
		return httpx.Invalid(f)
	}
	err = s.Store.UpdateTask(r.Context(), *t)
	if uniqueTaskName(err) {
		return httpx.Invalid(map[string]string{"name": "Another task of this resource has that name."})
	}
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "task.update", "resource", t.ResourceID, map[string]any{"task": t.Name, "enabled": t.Enabled}, clientIP(r))
	updated, _ := s.Store.GetTask(r.Context(), id)
	if runs, err := s.Store.TaskRuns(r.Context(), id, 1, false); err == nil && len(runs) > 0 {
		updated.LastRun = &runs[0]
	}
	httpx.JSON(w, 200, map[string]any{"task": describe(*updated)})
	return nil
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Task")
	if err != nil {
		return err
	}
	t, err := s.Store.GetTask(r.Context(), id)
	if err != nil {
		return notFound(err, "Task")
	}
	// A run in progress is asked to stop; deleting the task then removes its history.
	s.Store.DB.Exec(r.Context(), `UPDATE task_runs SET cancel_requested = true WHERE task_id = $1 AND status = 'running'`, id)
	if err := s.Store.DeleteTask(r.Context(), id); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "task.delete", "resource", t.ResourceID, map[string]any{"task": t.Name}, clientIP(r))
	w.WriteHeader(204)
	return nil
}

func (s *Server) runTask(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Task")
	if err != nil {
		return err
	}
	t, err := s.Store.GetTask(r.Context(), id)
	if err != nil {
		return notFound(err, "Task")
	}
	res, err := s.Store.GetResource(r.Context(), t.ResourceID)
	if err != nil {
		return notFound(err, "Resource")
	}
	if res.Status != "running" {
		return httpx.Conflict(res.Name + " isn't running, so the task has nowhere to run. Start it first.")
	}
	runID, ok, err := s.Store.QueueTaskRun(r.Context(), id, u.ID)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.Conflict("This task is already running.")
	}
	s.Store.Audit(r.Context(), u, "task.run", "resource", t.ResourceID, map[string]any{"task": t.Name}, clientIP(r))
	run, _ := s.Store.GetTaskRun(r.Context(), runID)
	httpx.JSON(w, 202, map[string]any{"run": run})
	return nil
}

func (s *Server) listTaskRuns(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Task")
	if err != nil {
		return err
	}
	if _, err := s.Store.GetTask(r.Context(), id); err != nil {
		return notFound(err, "Task")
	}
	runs, err := s.Store.TaskRuns(r.Context(), id, 20, false)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"runs": runs})
	return nil
}

func (s *Server) getTaskRun(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Run")
	if err != nil {
		return err
	}
	run, err := s.Store.GetTaskRun(r.Context(), id)
	if err != nil {
		return notFound(err, "Run")
	}
	httpx.JSON(w, 200, map[string]any{"run": run})
	return nil
}

func (s *Server) cancelTaskRun(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Run")
	if err != nil {
		return err
	}
	status, err := s.Store.CancelTaskRun(r.Context(), id)
	if err != nil {
		return err
	}
	if status == "" {
		return httpx.Conflict("This run already finished.")
	}
	httpx.JSON(w, 202, map[string]string{"status": status})
	return nil
}

// cronPreview checks a schedule as it's typed and shows when it would run.
func (s *Server) cronPreview(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	expr, tz := r.URL.Query().Get("schedule"), r.URL.Query().Get("timezone")
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		httpx.JSON(w, 200, map[string]any{"valid": false, "error": "Unknown timezone."})
		return nil
	}
	sched, err := cron.Parse(expr)
	if err != nil {
		httpx.JSON(w, 200, map[string]any{"valid": false, "error": capitalize(err.Error()) + "."})
		return nil
	}
	next := []time.Time{}
	t := time.Now()
	for i := 0; i < 3; i++ {
		if t = sched.Next(t, loc); t.IsZero() {
			break
		}
		next = append(next, t)
	}
	httpx.JSON(w, 200, map[string]any{"valid": true, "description": cron.Describe(expr), "next": next})
	return nil
}
