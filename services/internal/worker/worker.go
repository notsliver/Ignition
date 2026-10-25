// Package worker executes queued jobs: deployments, lifecycle actions and removals.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ignition/ignition/internal/jobs"
	"github.com/ignition/ignition/internal/runtime"
	"github.com/ignition/ignition/internal/secrets"
	"github.com/ignition/ignition/internal/store"
)

const network = "ignition"

type Worker struct {
	ID          string
	Queue       *jobs.Queue
	Store       *store.Store
	Runtime     runtime.Runtime
	Secrets     *secrets.Box
	Concurrency int
	Lease       time.Duration
}

var errCancelled = errors.New("cancelled")

// Run claims and executes jobs until ctx is cancelled, then waits for in-flight jobs.
func (w *Worker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < w.Concurrency; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			owner := fmt.Sprintf("%s/%d", w.ID, slot)
			for ctx.Err() == nil {
				job, err := w.Queue.Claim(ctx, owner, w.Lease)
				if err != nil {
					slog.Error("claim failed", "error", err)
				}
				if job == nil {
					select {
					case <-ctx.Done():
					case <-time.After(time.Second):
					}
					continue
				}
				// Jobs get their own context so a shutdown doesn't abort a rollout halfway;
				// unfinished jobs are reclaimed after the lease expires.
				w.execute(context.WithoutCancel(ctx), owner, job)
			}
		}(i)
	}
	wg.Add(1)
	go func() { defer wg.Done(); w.syncLoop(ctx) }()
	wg.Add(1)
	go func() { defer wg.Done(); w.metricsLoop(ctx) }()
	wg.Wait()
}

func (w *Worker) execute(parent context.Context, owner string, job *jobs.Job) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
	defer cancel()
	log := slog.With("job", job.ID, "kind", job.Kind, "attempt", job.Attempts)
	log.Info("job started")

	// Heartbeat keeps the lease alive and turns a cancellation request into context cancellation.
	cancelled := make(chan struct{})
	go func() {
		t := time.NewTicker(w.Lease / 3)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				stop, err := w.Queue.Heartbeat(ctx, job.ID, owner, w.Lease)
				if err == nil && stop {
					close(cancelled)
					cancel()
					return
				}
			}
		}
	}()

	var err error
	switch job.Kind {
	case jobs.KindDeploy:
		err = w.deploy(ctx, job)
	case jobs.KindAction:
		err = w.action(ctx, job)
	case jobs.KindRemove:
		err = w.remove(ctx, job)
	default:
		err = fmt.Errorf("%w: unknown job kind %q", jobs.ErrPermanent, job.Kind)
	}

	bg := context.WithoutCancel(ctx)
	select {
	case <-cancelled:
		err = errCancelled
	default:
	}
	switch {
	case err == nil:
		w.Queue.Succeed(bg, job.ID)
		log.Info("job succeeded")
	case errors.Is(err, errCancelled):
		w.Queue.Log(bg, job.ID, "warn", "", "Cancelled by request.")
		w.Queue.Cancelled(bg, job.ID)
		if job.DeploymentID != nil {
			w.Store.SetDeploymentStatus(bg, *job.DeploymentID, "cancelled", "")
		}
		w.refreshStatus(bg, job.ResourceID)
		log.Info("job cancelled")
	default:
		final, _ := w.Queue.Fail(bg, job, err)
		level := "warn"
		if final {
			level = "error"
		}
		msg := err.Error()
		if !final {
			msg += " — retrying"
		}
		w.Queue.Log(bg, job.ID, level, "", msg)
		if final && job.DeploymentID != nil {
			w.Store.SetDeploymentStatus(bg, *job.DeploymentID, "failed", strings.TrimPrefix(err.Error(), jobs.ErrPermanent.Error()+": "))
		}
		if final {
			w.refreshStatus(bg, job.ResourceID)
		}
		log.Warn("job failed", "error", err, "final", final)
	}
}

func (w *Worker) logf(ctx context.Context, jobID, step string) runtime.Logf {
	return func(level, message string) { w.Queue.Log(ctx, jobID, level, step, message) }
}

// envFor decrypts the resource's variables for injection into the container.
func (w *Worker) envFor(ctx context.Context, resourceID string) ([]string, error) {
	vars, err := w.Store.ListEnv(ctx, resourceID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(vars))
	for _, v := range vars {
		plain, err := w.Secrets.Open(v.Sealed, resourceID+":"+v.Key)
		if err != nil {
			return nil, fmt.Errorf("%w: variable %s can't be decrypted; was IGNITION_SECRET_KEY changed?", jobs.ErrPermanent, v.Key)
		}
		out = append(out, v.Key+"="+plain)
	}
	return out, nil
}

func (w *Worker) deploy(ctx context.Context, job *jobs.Job) error {
	if job.DeploymentID == nil || job.ResourceID == nil {
		return fmt.Errorf("%w: deploy job without deployment", jobs.ErrPermanent)
	}
	res, err := w.Store.GetResource(ctx, *job.ResourceID)
	if err != nil {
		return fmt.Errorf("%w: resource no longer exists", jobs.ErrPermanent)
	}
	dep, err := w.Store.GetDeployment(ctx, *job.DeploymentID)
	if err != nil {
		return err
	}
	if dep.Status == "cancelled" {
		return errCancelled
	}
	w.Store.SetStatus(ctx, res.ID, "deploying")

	imageRef := dep.ImageRef
	switch {
	case imageRef != "" && res.SourceType == "git":
		// Redeploy or rollback of an image built earlier: reuse it without rebuilding.
		w.Queue.Log(ctx, job.ID, "info", "source", "Reusing built image "+imageRef)
	case res.SourceType == "image":
		imageRef = res.Image
		w.Store.SetDeploymentStatus(ctx, dep.ID, "deploying", "")
		w.Queue.Log(ctx, job.ID, "info", "pull", "Pulling "+imageRef)
		if err := w.Runtime.PullImage(ctx, imageRef, w.logf(ctx, job.ID, "pull")); err != nil {
			if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "denied") || strings.Contains(err.Error(), "invalid reference") {
				return fmt.Errorf("%w: pull %s: %v", jobs.ErrPermanent, imageRef, err)
			}
			return fmt.Errorf("pull %s: %w", imageRef, err)
		}
		w.Store.SetDeploymentArtifact(ctx, dep.ID, imageRef, "")
	default:
		w.Store.SetDeploymentStatus(ctx, dep.ID, "building", "")
		dir, err := os.MkdirTemp("", "ignition-src-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		w.Queue.Log(ctx, job.ID, "info", "source", fmt.Sprintf("Cloning %s (%s)", res.RepoURL, res.Branch))
		commit, err := cloneRepo(ctx, res.RepoURL, res.Branch, dir)
		if err != nil {
			return err
		}
		w.Queue.Log(ctx, job.ID, "info", "source", "Checked out "+commit)
		dockerfile, err := SafeDockerfile(res.Dockerfile)
		if err != nil {
			return err
		}
		if _, err := os.Stat(dir + "/" + dockerfile); err != nil {
			return fmt.Errorf("%w: %s not found in the repository. Only Dockerfile builds are supported right now", jobs.ErrPermanent, dockerfile)
		}
		buildContext, err := tarDir(dir)
		if err != nil {
			return err
		}
		imageRef = fmt.Sprintf("ignition/%s:%s", res.ID[:8], commit)
		w.Queue.Log(ctx, job.ID, "info", "build", "Building "+imageRef)
		if err := w.Runtime.BuildImage(ctx, buildContext, dockerfile, imageRef, w.logf(ctx, job.ID, "build")); err != nil {
			return fmt.Errorf("%w: build failed: %v", jobs.ErrPermanent, err)
		}
		w.Store.SetDeploymentArtifact(ctx, dep.ID, imageRef, commit)
	}

	w.Store.SetDeploymentStatus(ctx, dep.ID, "deploying", "")
	env, err := w.envFor(ctx, res.ID)
	if err != nil {
		return err
	}
	if err := w.Runtime.EnsureNetwork(ctx, network); err != nil {
		return err
	}
	name := fmt.Sprintf("ign-%s-%s-%d", res.Name, res.ID[:8], dep.Number)
	w.Runtime.Remove(ctx, name, false) // idempotent retry: drop a half-created container from a previous attempt
	port := 0
	if res.Port != nil {
		port = *res.Port
	}
	var cmd []string
	if res.Command != "" {
		cmd = []string{"sh", "-c", res.Command} // runs inside the user's own container, never on the host
	}
	w.Queue.Log(ctx, job.ID, "info", "rollout", "Starting container "+name)
	id, err := w.Runtime.CreateContainer(ctx, runtime.ContainerSpec{
		Name: name, Image: imageRef, Env: env, Cmd: cmd, Port: port, MemoryMB: res.MemoryMB, CPUs: res.CPUs, Network: network,
		Volumes: volumesFor(res),
		Aliases: []string{res.Name},
		Labels:  map[string]string{"ignition.managed": "true", "ignition.resource": res.ID, "ignition.deployment": dep.ID, "ignition.project": res.ProjectID},
	})
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	if err := w.Runtime.Start(ctx, id); err != nil {
		w.Runtime.Remove(ctx, id, false)
		return fmt.Errorf("%w: start container: %v", jobs.ErrPermanent, err)
	}

	// Health verification: the container must stay running for a few seconds.
	w.Queue.Log(ctx, job.ID, "info", "health", "Waiting for the container to stay up")
	var st runtime.State
	for i := 0; i < 5; i++ {
		select {
		case <-ctx.Done():
			w.Runtime.Remove(context.WithoutCancel(ctx), id, false)
			return errCancelled
		case <-time.After(time.Second):
		}
		if st, err = w.Runtime.Inspect(ctx, id); err != nil {
			return err
		}
		if !st.Running || st.Restarting {
			break
		}
	}
	if !st.Running || st.Restarting {
		if lines, err := w.Runtime.Logs(ctx, id, 20, time.Time{}); err == nil {
			for _, l := range lines {
				w.Queue.Log(ctx, job.ID, "error", "health", l.Message)
			}
		}
		w.Runtime.Remove(ctx, id, false)
		reason := fmt.Sprintf("container exited with code %d", st.ExitCode)
		if st.OOMKilled {
			reason = fmt.Sprintf("container ran out of memory (limit %d MB)", res.MemoryMB)
		}
		return fmt.Errorf("%w: health check failed: %s. The previous release keeps running.", jobs.ErrPermanent, reason)
	}
	w.Queue.Log(ctx, job.ID, "info", "health", "Container is running")

	// Switch over, then retire the previous container.
	old := res.ContainerID
	var hostPort *int
	if st.HostPort > 0 {
		hostPort = &st.HostPort
		w.Queue.Log(ctx, job.ID, "info", "rollout", fmt.Sprintf("Published on host port %d", st.HostPort))
	}
	if err := w.Store.SetRuntimeState(ctx, res.ID, "running", id, hostPort); err != nil {
		return err
	}
	w.Store.SetDesiredState(ctx, res.ID, "running")
	if !st.StartedAt.IsZero() {
		w.Store.SetRunningSince(ctx, res.ID, &st.StartedAt)
	}
	w.Store.MarkCurrent(ctx, res.ID, dep.ID)
	w.Store.SetDeploymentStatus(ctx, dep.ID, "successful", "")
	if old != "" && old != id {
		w.Queue.Log(ctx, job.ID, "info", "rollout", "Removing previous container")
		w.Runtime.Stop(ctx, old, 10*time.Second)
		w.Runtime.Remove(ctx, old, false)
	}
	w.Queue.Log(ctx, job.ID, "info", "complete", "Deployment complete")
	return nil
}

// volumeName is stable per resource, so data survives redeploys and rollbacks.
func volumeName(res *store.Resource) string { return "ign-" + res.ID[:8] + "-data" }

func volumesFor(res *store.Resource) map[string]string {
	if res.DataPath == "" {
		return nil
	}
	return map[string]string{volumeName(res): res.DataPath}
}

type actionPayload struct {
	Action        string `json:"action"`
	RemoveVolumes bool   `json:"removeVolumes"`
}

func (w *Worker) action(ctx context.Context, job *jobs.Job) error {
	var p actionPayload
	json.Unmarshal(job.Payload, &p)
	res, err := w.Store.GetResource(ctx, *job.ResourceID)
	if err != nil {
		return fmt.Errorf("%w: resource no longer exists", jobs.ErrPermanent)
	}
	if res.ContainerID == "" {
		return fmt.Errorf("%w: %s has never been deployed. Deploy it first.", jobs.ErrPermanent, res.Name)
	}
	w.Queue.Log(ctx, job.ID, "info", p.Action, fmt.Sprintf("%s %s", strings.Title(p.Action), res.Name))
	switch p.Action {
	case "start":
		err = w.Runtime.Start(ctx, res.ContainerID)
	case "stop":
		err = w.Runtime.Stop(ctx, res.ContainerID, 15*time.Second)
	case "restart":
		err = w.Runtime.Restart(ctx, res.ContainerID, 15*time.Second)
	default:
		return fmt.Errorf("%w: unknown action %q", jobs.ErrPermanent, p.Action)
	}
	if runtime.IsNotFound(err) {
		w.Store.SetRuntimeState(ctx, res.ID, "failed", "", nil)
		return fmt.Errorf("%w: the container was removed outside Ignition. Redeploy to recreate it.", jobs.ErrPermanent)
	}
	if err != nil {
		return err
	}
	w.refreshStatus(ctx, job.ResourceID)
	return nil
}

func (w *Worker) remove(ctx context.Context, job *jobs.Job) error {
	var p actionPayload
	json.Unmarshal(job.Payload, &p)
	res, err := w.Store.GetResource(ctx, *job.ResourceID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // already gone: removal is idempotent
	}
	if err != nil {
		return err
	}
	if res.ContainerID != "" {
		w.Queue.Log(ctx, job.ID, "info", "remove", "Removing container")
		if err := w.Runtime.Remove(ctx, res.ContainerID, p.RemoveVolumes); err != nil {
			return err
		}
	}
	if p.RemoveVolumes && res.DataPath != "" {
		w.Queue.Log(ctx, job.ID, "warn", "remove", "Deleting volume "+volumeName(res)+" and its data")
		if err := w.Runtime.RemoveVolume(ctx, volumeName(res)); err != nil {
			return err
		}
	}
	return w.Store.DeleteResourceRow(ctx, res.ID)
}

// refreshStatus derives the resource's status from Docker so the UI reflects reality.
func (w *Worker) refreshStatus(ctx context.Context, resourceID *string) {
	if resourceID == nil {
		return
	}
	res, err := w.Store.GetResource(ctx, *resourceID)
	if err != nil || res.ContainerID == "" {
		if err == nil && res.Status == "deploying" {
			w.Store.SetStatus(ctx, res.ID, "failed")
		}
		return
	}
	st, err := w.Runtime.Inspect(ctx, res.ContainerID)
	if err != nil {
		return
	}
	status := "stopped"
	switch {
	case !st.Exists:
		status = "failed"
	case st.Running && !st.Restarting:
		status = "running"
	case st.Restarting || (st.ExitCode != 0 && res.DesiredState == "running"):
		status = "failed"
	}
	var hostPort *int
	if st.HostPort > 0 {
		hostPort = &st.HostPort
	}
	containerID := res.ContainerID
	if !st.Exists {
		containerID = ""
	}
	w.Store.SetRuntimeState(ctx, res.ID, status, containerID, hostPort)
	var since *time.Time
	if status == "running" && !st.StartedAt.IsZero() {
		since = &st.StartedAt
	}
	w.Store.SetRunningSince(ctx, res.ID, since)
}

// syncLoop periodically reconciles stored status with Docker (containers can crash or be stopped externally).
func (w *Worker) syncLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		rows, err := w.Store.DB.Query(ctx, `
			SELECT r.id FROM resources r WHERE r.container_id <> '' AND r.status IN ('running', 'stopped', 'failed')
			  AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.resource_id = r.id AND j.status IN ('pending', 'running'))`)
		if err != nil {
			continue
		}
		var ids []string
		for rows.Next() {
			var id string
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			w.refreshStatus(ctx, &id)
		}
	}
}
