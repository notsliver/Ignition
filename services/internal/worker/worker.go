// Package worker executes queued jobs: deployments, lifecycle actions and removals.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/proxy"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/secrets"
	"github.com/notsliver/Ignition/services/internal/sources"
	"github.com/notsliver/Ignition/services/internal/store"
)

// LegacyNetwork is the single network every resource used to share. Containers are moved off it.
const LegacyNetwork = "ignition"

type Worker struct {
	ID          string
	Queue       *jobs.Queue
	Store       *store.Store
	Runtime     runtime.Runtime
	Secrets     *secrets.Box
	Concurrency int
	Lease       time.Duration
	Proxy       *proxy.Client

	selfOnce sync.Once
	self     string // the worker's own container, for joining app networks
	joinedMu sync.Mutex
	joined   map[string]time.Time
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
	wg.Add(1)
	go func() { defer wg.Done(); w.HealthLoop(ctx) }()
	wg.Add(1)
	go func() { defer wg.Done(); w.TaskLoop(ctx) }()
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
	case jobs.KindHealth:
		err = w.healthTest(ctx, job)
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
	case imageRef != "":
		// Redeploy or rollback of an earlier release: reuse its exact image without rebuilding or pulling.
		if id, err := w.Runtime.ImageID(ctx, imageRef); err != nil {
			return err
		} else if id == "" {
			return fmt.Errorf("%w: that release's image is no longer on this machine (old images may have been purged). Deploy from the source instead.", jobs.ErrPermanent)
		}
		w.Queue.Log(ctx, job.ID, "info", "source", "Reusing the image of an earlier release: "+shortImage(imageRef))
	case res.SourceType == "image":
		imageRef = res.Image
		w.Store.SetDeploymentStatus(ctx, dep.ID, "deploying", "")
		w.Queue.Log(ctx, job.ID, "info", "pull", "Pulling "+imageRef)
		auth, err := w.registryAuth(ctx, res)
		if err != nil {
			return err
		}
		if err := w.Runtime.PullImage(ctx, imageRef, auth, w.logf(ctx, job.ID, "pull")); err != nil {
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
		creds, err := w.sourceCredentials(ctx, res)
		if err != nil {
			return err
		}
		if creds != nil {
			w.Queue.Log(ctx, job.ID, "info", "source", "Using saved credentials ("+creds.AuthType+")")
		}
		commit, err := cloneRepo(ctx, res.RepoURL, res.Branch, dir, creds)
		if err != nil {
			return err
		}
		w.Queue.Log(ctx, job.ID, "info", "source", "Checked out "+commit)
		bs, err := w.Store.GetBuildSettings(ctx, res.ID)
		if err != nil {
			return err
		}
		opts, info, contextDir, err := w.prepareBuild(ctx, job.ID, dir, res, bs)
		if err != nil {
			return err
		}
		buildContext, err := tarDir(contextDir)
		if err != nil {
			return err
		}
		imageRef = fmt.Sprintf("ignition/%s:%s", res.ID[:8], commit)
		opts.Tag, opts.NoCache = imageRef, w.Store.DeploymentNoCache(ctx, dep.ID)
		// Labelled, so an older build of the same commit (left untagged) can still be found and removed.
		opts.Labels = map[string]string{"ignition.resource": res.ID, "ignition.deployment": dep.ID}
		if opts.NoCache {
			w.Queue.Log(ctx, job.ID, "info", "build", "Building without cache and pulling fresh base images")
		}
		w.Queue.Log(ctx, job.ID, "info", "build", fmt.Sprintf("Building %s (%s)", imageRef, info))
		w.Store.SetBuildInfo(ctx, dep.ID, info)
		buildCtx, cancelBuild := context.WithTimeout(ctx, time.Duration(bs.TimeoutMinutes)*time.Minute)
		err = w.Runtime.BuildImage(buildCtx, buildContext, opts, w.logf(ctx, job.ID, "build"))
		timedOut := buildCtx.Err() == context.DeadlineExceeded
		cancelBuild()
		if timedOut {
			return fmt.Errorf("%w: the build took longer than %d minutes. Raise the build timeout in Build & Deploy.", jobs.ErrPermanent, bs.TimeoutMinutes)
		}
		if err != nil {
			return fmt.Errorf("%w: build failed: %v", jobs.ErrPermanent, err)
		}
		w.Store.SetDeploymentArtifact(ctx, dep.ID, imageRef, commit)
	}

	if id, err := w.Runtime.ImageID(ctx, imageRef); err == nil && id != "" {
		w.Store.SetDeploymentImageID(ctx, dep.ID, id) // the exact image, so a rollback gets the same bits
	}
	w.Store.SetDeploymentStatus(ctx, dep.ID, "deploying", "")
	ds, err := w.Store.GetBuildSettings(ctx, res.ID)
	if err != nil {
		return err
	}
	grace := time.Duration(ds.StopGrace) * time.Second
	lim, err := w.Store.GetLimits(ctx, res.ID)
	if err != nil {
		return err
	}
	vols, err := w.Store.Volumes(ctx, res.ID)
	if err != nil {
		return err
	}
	env, err := w.envFor(ctx, res.ID)
	if err != nil {
		return err
	}
	w.Store.SnapshotEnv(ctx, dep.ID, res.ID) // kept so a rollback can restore exactly these variables
	adv, err := w.Store.GetAdvanced(ctx, res.ID)
	if err != nil {
		return err
	}
	entrypoint, err := SplitArgs(adv.Entrypoint)
	if err != nil {
		return fmt.Errorf("%w: entrypoint %v", jobs.ErrPermanent, err)
	}
	labels := map[string]string{}
	for k, v := range adv.Labels {
		if !strings.HasPrefix(k, "ignition.") && !strings.HasPrefix(k, "com.docker.") {
			labels[k] = v
		}
	}
	// Which labels are ours, so Advanced Settings can tell a removed label from one the image brings.
	customKeys := make([]string, 0, len(labels))
	for k := range labels {
		customKeys = append(customKeys, k)
	}
	sort.Strings(customKeys)
	labels["ignition.labels"] = strings.Join(customKeys, ",")
	for k, v := range map[string]string{"ignition.managed": "true", "ignition.resource": res.ID, "ignition.deployment": dep.ID, "ignition.project": res.ProjectID} {
		labels[k] = v
	}
	// Each environment has its own private network; shared networks connect resources across projects on purpose.
	network := runtime.EnvNetwork(res.EnvironmentID)
	if err := w.Runtime.EnsureManagedNetwork(ctx, network, envLabels(res.EnvironmentID)); err != nil {
		return err
	}
	name := fmt.Sprintf("ign-%s-%s-%d", res.Name, res.ID[:8], dep.Number)
	w.Runtime.Remove(ctx, name, false) // idempotent retry: drop a half-created container from a previous attempt
	netcfg, err := w.Store.GetNetworking(ctx, res.ID)
	if err != nil {
		return err
	}
	ports, fixed := portSpecs(netcfg.Ports)
	var shared []string
	for _, n := range netcfg.Networks {
		if err := w.Runtime.EnsureManagedNetwork(ctx, runtime.SharedNetwork(n), sharedLabels(n)); err != nil {
			return err
		}
		shared = append(shared, runtime.SharedNetwork(n))
	}
	var cmd []string
	if res.Command != "" {
		cmd = []string{"sh", "-c", res.Command} // runs inside the user's own container, never on the host
	}
	w.Queue.Log(ctx, job.ID, "info", "rollout", "Starting container "+name)
	id, err := w.Runtime.CreateContainer(ctx, runtime.ContainerSpec{
		Name: name, Image: imageRef, Env: env, Cmd: cmd, Ports: ports, MemoryMB: res.MemoryMB, CPUs: res.CPUs, Network: network, Networks: shared,
		Volumes: volumesFor(res, vols), VolumeLabels: map[string]string{"ignition.resource": res.ID},
		Limits:     RuntimeLimits(lim),
		Aliases:    append([]string{res.Name}, netcfg.Aliases...),
		Labels:     labels,
		Entrypoint: entrypoint, WorkingDir: adv.Workdir, User: adv.User, ReadOnlyRoot: adv.ReadOnlyRoot, Init: adv.Init, Privileged: adv.Privileged,
	})
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	// A fixed host port can only be bound by one container, so the old one has to let go first.
	// That means a few seconds of downtime; if the new release fails, the old one is started again.
	stoppedOld := false
	if len(fixed) > 0 && res.ContainerID != "" {
		w.Queue.Log(ctx, job.ID, "info", "rollout", fmt.Sprintf("Stopping the previous container first: host port %s is fixed (brief downtime)", strings.Join(fixed, ", ")))
		w.Runtime.Stop(ctx, res.ContainerID, grace)
		stoppedOld = true
	}
	restoreOld := func() {
		if stoppedOld {
			w.Queue.Log(ctx, job.ID, "info", "rollout", "Starting the previous container again")
			w.Runtime.Start(context.WithoutCancel(ctx), res.ContainerID)
		}
	}
	if err := w.Runtime.Start(ctx, id); err != nil {
		w.Runtime.Remove(ctx, id, false)
		restoreOld()
		if strings.Contains(err.Error(), "address already in use") || strings.Contains(err.Error(), "port is already allocated") {
			return fmt.Errorf("%w: a host port is already in use by another program. Pick a different one under Networking. (%v)", jobs.ErrPermanent, err)
		}
		return fmt.Errorf("%w: start container: %v", jobs.ErrPermanent, err)
	}

	// Health verification: the container must stay running for the configured time.
	w.Queue.Log(ctx, job.ID, "info", "health", fmt.Sprintf("Waiting %ds for the container to stay up", ds.HealthSeconds))
	var st runtime.State
	for i := 0; i < ds.HealthSeconds; i++ {
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
		w.archiveLogs(ctx, id) // the crash output stays readable under Logs for this deployment
		w.Runtime.Remove(ctx, id, false)
		restoreOld()
		reason := fmt.Sprintf("container exited with code %d", st.ExitCode)
		if st.OOMKilled {
			reason = fmt.Sprintf("container ran out of memory (limit %d MB)", res.MemoryMB)
		}
		return fmt.Errorf("%w: health check failed: %s. The previous release keeps running.", jobs.ErrPermanent, reason)
	}
	w.Queue.Log(ctx, job.ID, "info", "health", "Container is running")

	// Release command (e.g. migrations): runs once in the new container before it takes over.
	if ds.ReleaseCommand != "" {
		w.Queue.Log(ctx, job.ID, "info", "release", "Running release command: "+ds.ReleaseCommand)
		rctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		code, err := w.Runtime.Exec(rctx, id, []string{"sh", "-c", ds.ReleaseCommand}, nil, w.logf(ctx, job.ID, "release"))
		cancel()
		if err != nil || code != 0 {
			w.archiveLogs(ctx, id)
			w.Runtime.Remove(ctx, id, false)
			restoreOld()
			if err != nil {
				return fmt.Errorf("%w: release command couldn't run: %v. The previous release keeps running.", jobs.ErrPermanent, err)
			}
			return fmt.Errorf("%w: release command exited with code %d. The previous release keeps running.", jobs.ErrPermanent, code)
		}
		w.Queue.Log(ctx, job.ID, "info", "release", "Release command finished")
	}

	// Health check gate: the new release only takes over once it answers.
	hc, _, err := w.Store.GetHealth(ctx, res.ID)
	if err != nil {
		return err
	}
	if hc.Enabled {
		if err := w.awaitHealthy(ctx, job.ID, res, id, hc); err != nil {
			if !errors.Is(err, errCancelled) {
				if lines, lerr := w.Runtime.Logs(ctx, id, 15, time.Time{}); lerr == nil && len(lines) > 0 {
					w.Queue.Log(ctx, job.ID, "info", "health", "Last output of the new release:")
					for _, l := range lines {
						w.Queue.Log(ctx, job.ID, "info", "health", "  "+l.Message)
					}
				}
			}
			w.archiveLogs(ctx, id)
			w.Runtime.Remove(context.WithoutCancel(ctx), id, false)
			restoreOld()
			return err
		}
	}

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
	if hc.Enabled {
		w.Store.SetHealthState(ctx, res.ID, "healthy", 0, true)
	}
	if old != "" && old != id {
		w.Queue.Log(ctx, job.ID, "info", "rollout", "Removing previous container")
		w.Runtime.Stop(ctx, old, grace)
		w.archiveLogs(ctx, old)
		w.Runtime.Remove(ctx, old, false)
	}
	w.removeStrays(ctx, job.ID, res.ID, id)
	w.Queue.Log(ctx, job.ID, "info", "complete", "Deployment complete")
	return nil
}

// sourceCredentials decrypts the resource's linked source, if it has one.
func (w *Worker) sourceCredentials(ctx context.Context, res *store.Resource) (*sources.Credentials, error) {
	if res.SourceID == nil {
		return nil, nil
	}
	src, err := w.Store.GetSource(ctx, *res.SourceID)
	if err != nil {
		return nil, fmt.Errorf("%w: the linked source no longer exists", jobs.ErrPermanent)
	}
	c := sources.Credentials{Provider: src.Provider, URL: src.URL, AuthType: src.AuthType, Username: src.Username}
	if src.SecretEnc != nil {
		if c.Secret, err = w.Secrets.Open(src.SecretEnc, "source:"+src.ID); err != nil {
			return nil, fmt.Errorf("%w: source %s can't be decrypted; was IGNITION_SECRET_KEY changed?", jobs.ErrPermanent, src.Name)
		}
	}
	return &c, nil
}

// registryAuth returns the X-Registry-Auth header for image pulls through a linked registry source.
func (w *Worker) registryAuth(ctx context.Context, res *store.Resource) (string, error) {
	c, err := w.sourceCredentials(ctx, res)
	if err != nil || c == nil || c.AuthType == "none" || sources.IsGitProvider(c.Provider) {
		return "", err
	}
	return sources.RegistryAuthHeader(*c), nil
}

// VolumePrefix starts the Docker name of every volume of a resource. Names are stable per resource and
// volume name, so data survives redeploys and rollbacks ("data" keeps the original single volume).
func VolumePrefix(resourceID string) string { return "ign-" + resourceID[:8] + "-" }

func VolumeName(resourceID, name string) string { return VolumePrefix(resourceID) + name }

func volumesFor(res *store.Resource, vols []store.Volume) map[string]string {
	if len(vols) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, v := range vols {
		out[VolumeName(res.ID, v.Name)] = v.MountPath
	}
	return out
}

// RuntimeLimits converts saved limits into what Docker applies.
func RuntimeLimits(l store.Limits) runtime.Limits {
	r := runtime.Limits{MemoryMB: l.MemoryMB, CPUs: l.CPUs, NoSwap: !l.AllowSwap, Restart: l.RestartPolicy, MaxRestarts: l.MaxRestarts}
	if l.MemoryReservationMB != nil {
		r.MemoryReservationMB = *l.MemoryReservationMB
	}
	if l.PidsLimit != nil {
		r.PidsLimit = *l.PidsLimit
	}
	return r
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
	if p.Action == "refresh" {
		w.refreshStatus(ctx, job.ResourceID)
		return nil
	}
	switch p.Action {
	case "start":
		if res.Status == "paused" {
			err = w.Runtime.Unpause(ctx, res.ContainerID)
		} else {
			err = w.Runtime.Start(ctx, res.ContainerID)
		}
	case "pause":
		err = w.Runtime.Pause(ctx, res.ContainerID)
	case "resume":
		err = w.Runtime.Unpause(ctx, res.ContainerID)
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
	if p.RemoveVolumes {
		if err := w.removeVolumes(ctx, job.ID, res.ID); err != nil {
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
	case st.Paused:
		status = "paused"
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
			SELECT r.id FROM resources r WHERE r.container_id <> '' AND r.status IN ('running', 'stopped', 'failed', 'paused')
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

// portSpecs turns saved mappings into Docker port bindings and lists the fixed host ports among them.
func portSpecs(list []store.PortMapping) ([]runtime.PortMapping, []string) {
	var out []runtime.PortMapping
	var fixed []string
	for _, p := range list {
		m := runtime.PortMapping{HostIP: p.BindAddress, ContainerPort: p.ContainerPort, Protocol: p.Protocol}
		if p.HostPort != nil {
			m.HostPort = *p.HostPort
			fixed = append(fixed, fmt.Sprintf("%d/%s", *p.HostPort, p.Protocol))
		}
		out = append(out, m)
	}
	return out, fixed
}

// prepareBuild decides how to build: the repository's Dockerfile, or (in automatic mode, when there is none)
// a Dockerfile generated for the detected stack. It returns the options, a description and the context folder.
func (w *Worker) prepareBuild(ctx context.Context, jobID, repoDir string, res *store.Resource, bs store.BuildSettings) (runtime.BuildOptions, string, string, error) {
	var opts runtime.BuildOptions
	contextDir := repoDir
	if strings.Trim(bs.Context, "/. ") != "" {
		sub, err := SafeDockerfile(strings.Trim(bs.Context, "/"))
		if err != nil {
			return opts, "", "", fmt.Errorf("%w: build context must be a folder inside the repository", jobs.ErrPermanent)
		}
		contextDir = filepath.Join(repoDir, filepath.FromSlash(sub))
		if info, err := os.Stat(contextDir); err != nil || !info.IsDir() {
			return opts, "", "", fmt.Errorf("%w: build context %s isn't a folder in the repository", jobs.ErrPermanent, sub)
		}
	}
	name := bs.Dockerfile
	if strings.TrimSpace(name) == "" {
		name = "Dockerfile"
	}
	dockerfile, err := SafeDockerfile(name)
	if err != nil {
		return opts, "", "", err
	}
	info := dockerfile
	if _, err := os.Stat(filepath.Join(contextDir, filepath.FromSlash(dockerfile))); err != nil {
		if bs.Mode == "dockerfile" {
			return opts, "", "", fmt.Errorf("%w: %s not found in the build context. Add it, or switch the builder to Automatic", jobs.ErrPermanent, dockerfile)
		}
		port := 0
		if res.Port != nil {
			port = *res.Port
		}
		plan, err := DetectStack(contextDir, port, strings.TrimSpace(res.Command) != "")
		if err != nil {
			return opts, "", "", fmt.Errorf("%w: %v", jobs.ErrPermanent, err)
		}
		if err := os.WriteFile(filepath.Join(contextDir, GeneratedDockerfile), []byte(plan.Dockerfile), 0o644); err != nil {
			return opts, "", "", err
		}
		w.Queue.Log(ctx, jobID, "info", "build", fmt.Sprintf("No %s found. Detected %s: %s", dockerfile, plan.Stack, plan.Summary))
		for _, line := range strings.Split(strings.TrimSpace(plan.Dockerfile), "\n") {
			w.Queue.Log(ctx, jobID, "info", "build", "  "+line)
		}
		dockerfile, info = GeneratedDockerfile, "Detected "+plan.Stack
	}
	opts.Dockerfile, opts.Target = dockerfile, bs.Target
	if len(bs.Args) > 0 {
		opts.Args = map[string]string{}
		for _, a := range bs.Args {
			if k, v, ok := strings.Cut(a, "="); ok {
				opts.Args[k] = v
			}
		}
	}
	if bs.Target != "" {
		info += " · target " + bs.Target
	}
	return opts, info, contextDir, nil
}

// removeStrays deletes containers of this resource other than keep, e.g. left by an interrupted deploy.
func (w *Worker) removeStrays(ctx context.Context, jobID, resourceID, keep string) {
	ids, err := w.Runtime.ResourceContainers(ctx, resourceID)
	if err != nil {
		return
	}
	for _, c := range ids {
		if c != keep {
			w.Queue.Log(ctx, jobID, "info", "rollout", "Removing a leftover container from an earlier deployment")
			w.archiveLogs(ctx, c)
			w.Runtime.Remove(ctx, c, false)
		}
	}
}

// removeVolumes deletes every volume of the resource, attached or detached.
func (w *Worker) removeVolumes(ctx context.Context, jobID, resourceID string) error {
	vols, err := w.Runtime.Volumes(ctx, VolumePrefix(resourceID))
	if err != nil {
		return err
	}
	for _, v := range vols {
		w.Queue.Log(ctx, jobID, "warn", "remove", "Deleting volume "+v.Name+" and its data")
		if err := w.Runtime.RemoveVolume(ctx, v.Name); err != nil {
			return err
		}
	}
	return nil
}

// ArchivedLogLines and ArchivedDeployments bound what's kept of retired containers' output.
const (
	ArchivedLogLines    = 10000
	ArchivedDeployments = 10
)

// archiveLogs saves a container's stdout/stderr under its deployment before the container is removed.
func (w *Worker) archiveLogs(ctx context.Context, containerID string) {
	labels, err := w.Runtime.ContainerLabels(ctx, containerID)
	if err != nil || labels["ignition.deployment"] == "" {
		return
	}
	lines, err := w.Runtime.Logs(ctx, containerID, ArchivedLogLines, time.Time{})
	if err != nil || len(lines) == 0 {
		return
	}
	out := make([]store.RuntimeLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, store.RuntimeLine{Time: l.Time, Stream: l.Stream, Message: l.Message})
	}
	if err := w.Store.ArchiveRuntimeLogs(context.WithoutCancel(ctx), labels["ignition.deployment"], out, ArchivedDeployments); err != nil {
		slog.Warn("archive runtime logs", "container", containerID, "error", err)
	}
}

// shortImage shortens sha256 image IDs for logs.
func shortImage(ref string) string {
	if strings.HasPrefix(ref, "sha256:") && len(ref) > 19 {
		return ref[:19]
	}
	return ref
}

// SplitArgs splits a command line like a shell would for quoting ("a b" and 'a b' stay one argument),
// without running a shell. A JSON array (["a", "b"]) is taken as is.
func SplitArgs(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if strings.HasPrefix(s, "[") {
		var out []string
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, errors.New("isn't a valid JSON array of strings")
		}
		return out, nil
	}
	var out []string
	var cur strings.Builder
	inWord, escaped := false, false
	var quote rune
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped, inWord = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, errors.New("has an unclosed quote")
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out, nil
}
