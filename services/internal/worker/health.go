package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Health checks. The worker joins every environment network (like the domain proxy) and probes apps
 * directly: an HTTP request or a TCP connection to the container, or a command run inside it. Nothing has
 * to be installed in the app's image for HTTP and TCP checks.
 *
 * A probe that couldn't be made at all (the worker can't reach the network, Docker hiccups) is
 * inconclusive: it's reported, but never counts as a failure, so it can't restart a healthy app.
 */

// ProbeResult is one health check.
type ProbeResult struct {
	OK        bool   `json:"ok"`
	LatencyMS int    `json:"latencyMs"`
	Detail    string `json:"detail"`
}

// errInconclusive marks a probe that couldn't be made.
type errInconclusive struct{ msg string }

func (e errInconclusive) Error() string { return e.msg }

var healthClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, // a redirect is an answer
	Transport:     &http.Transport{DisableKeepAlives: true, Proxy: nil},
}

// StatusMatches reports whether code fits a spec like "200-399", "200,204" or "2xx".
func StatusMatches(spec string, code int) bool {
	for _, part := range strings.Split(spec, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		switch {
		case len(part) == 3 && strings.HasSuffix(part, "xx"):
			if strconv.Itoa(code/100) == part[:1] {
				return true
			}
		case strings.Contains(part, "-"):
			a, b, _ := strings.Cut(part, "-")
			lo, err1 := strconv.Atoi(a)
			hi, err2 := strconv.Atoi(b)
			if err1 == nil && err2 == nil && code >= lo && code <= hi {
				return true
			}
		default:
			if n, err := strconv.Atoi(part); err == nil && n == code {
				return true
			}
		}
	}
	return false
}

// ValidStatusSpec checks a spec before it's saved.
func ValidStatusSpec(spec string) bool {
	if strings.TrimSpace(spec) == "" {
		return false
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if len(part) == 3 && strings.HasSuffix(part, "xx") && part[0] >= '1' && part[0] <= '5' {
			continue
		}
		a, b, isRange := strings.Cut(part, "-")
		lo, err := strconv.Atoi(a)
		if err != nil || lo < 100 || lo > 599 {
			return false
		}
		if isRange {
			hi, err := strconv.Atoi(b)
			if err != nil || hi < lo || hi > 599 {
				return false
			}
		}
	}
	return true
}

// selfContainer is the worker's own container (its hostname), or "" when it doesn't run in Docker.
func (w *Worker) selfContainer(ctx context.Context) string {
	w.selfOnce.Do(func() {
		host, _ := os.Hostname()
		if st, err := w.Runtime.Inspect(ctx, host); err == nil && st.Exists {
			w.self = host
		}
	})
	return w.self
}

// joinNetwork attaches the worker to a network so it can reach the apps on it.
func (w *Worker) joinNetwork(ctx context.Context, network string) error {
	self := w.selfContainer(ctx)
	if self == "" {
		return errInconclusive{"the worker doesn't run in Docker, so it can't reach the app's network"}
	}
	w.joinedMu.Lock()
	defer w.joinedMu.Unlock()
	if w.joined == nil {
		w.joined = map[string]time.Time{}
	}
	if time.Since(w.joined[network]) < time.Minute {
		return nil
	}
	// Re-checked every minute: a network can be recreated, and connecting is a no-op when attached.
	if err := w.Runtime.ConnectNetwork(ctx, network, self, nil); err != nil {
		return errInconclusive{"the worker couldn't join the app's network: " + err.Error()}
	}
	w.joined[network] = time.Now()
	return nil
}

// Probe runs one check against a container.
func (w *Worker) Probe(ctx context.Context, containerID, environmentID string, port *int, c store.HealthConfig) (ProbeResult, error) {
	timeout := time.Duration(c.TimeoutSeconds) * time.Second
	if c.Kind == "command" {
		return w.probeCommand(ctx, containerID, c.Command, timeout)
	}
	p := c.Port
	if p == nil {
		p = port
	}
	if p == nil {
		return ProbeResult{}, errInconclusive{"no port to check: set one here or under Build & Deploy"}
	}
	network := runtime.EnvNetwork(environmentID)
	if err := w.joinNetwork(ctx, network); err != nil {
		return ProbeResult{}, err
	}
	st, err := w.Runtime.Inspect(ctx, containerID)
	if err != nil {
		return ProbeResult{}, errInconclusive{"couldn't inspect the container: " + err.Error()}
	}
	if !st.Exists || !st.Running {
		return ProbeResult{Detail: "The container isn't running"}, nil
	}
	if st.Paused {
		return ProbeResult{}, errInconclusive{"the container is paused"}
	}
	ip := ""
	for _, a := range st.Networks {
		if a.Network == network {
			ip = a.IP
		}
	}
	if ip == "" {
		return ProbeResult{}, errInconclusive{"the container isn't on its environment network yet"}
	}
	addr := net.JoinHostPort(ip, strconv.Itoa(*p))
	start := time.Now()
	if c.Kind == "tcp" {
		conn, err := net.DialTimeout("tcp", addr, timeout)
		ms := int(time.Since(start).Milliseconds())
		if err != nil {
			return ProbeResult{LatencyMS: ms, Detail: fmt.Sprintf("TCP :%d %s", *p, dialReason(err, timeout))}, nil
		}
		conn.Close()
		return ProbeResult{OK: true, LatencyMS: ms, Detail: fmt.Sprintf("TCP :%d accepted the connection", *p)}, nil
	}

	path := c.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(rctx, "GET", "http://"+addr+path, nil)
	req.Header.Set("User-Agent", "Ignition-HealthCheck/1")
	resp, err := healthClient.Do(req)
	ms := int(time.Since(start).Milliseconds())
	if err != nil {
		return ProbeResult{LatencyMS: ms, Detail: fmt.Sprintf("GET %s on :%d %s", path, *p, dialReason(err, timeout))}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	line := fmt.Sprintf("GET %s → %s", path, resp.Status)
	if StatusMatches(c.ExpectStatus, resp.StatusCode) {
		return ProbeResult{OK: true, LatencyMS: ms, Detail: line}, nil
	}
	detail := line + " (expected " + c.ExpectStatus + ")"
	if snippet := bodySnippet(string(body)); snippet != "" {
		detail += ": " + snippet
	}
	return ProbeResult{LatencyMS: ms, Detail: detail}, nil
}

var tagRE = regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>|<[^>]+>`)

// bodySnippet is the start of a response body as plain text (error pages are often HTML).
func bodySnippet(body string) string {
	text := strings.Join(strings.Fields(tagRE.ReplaceAllString(body, " ")), " ")
	if r := []rune(text); len(r) > 140 {
		text = string(r[:139]) + "…"
	}
	return text
}

func dialReason(err error, timeout time.Duration) string {
	msg := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
		return fmt.Sprintf("didn't answer within %s", timeout)
	case strings.Contains(msg, "connection refused"):
		return "refused the connection: nothing is listening on that port"
	case strings.Contains(msg, "connection reset") || strings.Contains(msg, "EOF"):
		return "closed the connection without answering"
	case strings.Contains(msg, "no route") || strings.Contains(msg, "unreachable"):
		return "couldn't be reached"
	}
	return "failed: " + msg
}

func (w *Worker) probeCommand(ctx context.Context, containerID, command string, timeout time.Duration) (ProbeResult, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var out []string
	start := time.Now()
	code, err := w.Runtime.Exec(cctx, containerID, []string{"sh", "-c", command}, nil, func(_, line string) {
		if len(out) < 5 && strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	})
	ms := int(time.Since(start).Milliseconds())
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ProbeResult{LatencyMS: ms, Detail: fmt.Sprintf("%s didn't finish within %s", command, timeout)}, nil
	case err != nil && (strings.Contains(err.Error(), "executable file not found") || strings.Contains(err.Error(), "no such file")):
		return ProbeResult{LatencyMS: ms, Detail: "The container has no shell (sh) to run the command in. Use an HTTP or TCP check."}, nil
	case err != nil && strings.Contains(err.Error(), "is not running"):
		return ProbeResult{LatencyMS: ms, Detail: "The container isn't running"}, nil
	case err != nil:
		return ProbeResult{}, errInconclusive{"couldn't run the command: " + err.Error()}
	}
	if (code == 126 || code == 127) && noShell(strings.Join(out, " ")) {
		return ProbeResult{LatencyMS: ms, Detail: "The container has no shell (sh) to run the command in. Use an HTTP or TCP check."}, nil
	}
	detail := fmt.Sprintf("%s exited with %d", command, code)
	if len(out) > 0 {
		detail += ": " + strings.Join(out, " ")
	}
	return ProbeResult{OK: code == 0, LatencyMS: ms, Detail: detail}, nil
}

// noShell recognises Docker's error for an exec in an image without sh (e.g. FROM scratch).
func noShell(output string) bool {
	return strings.Contains(output, "OCI runtime exec failed") || strings.Contains(output, `exec: "sh": executable file not found`)
}

// awaitHealthy is the deploy gate: the new container must pass its health check before it gets traffic.
// Failures during the start period don't count; after it, `retries` failures in a row fail the deployment.
func (w *Worker) awaitHealthy(ctx context.Context, jobID string, res *store.Resource, containerID string, c store.HealthConfig) error {
	interval := time.Duration(c.IntervalSeconds) * time.Second
	if interval > 5*time.Second {
		interval = 5 * time.Second // during a deploy, don't make people wait a full monitoring interval
	}
	started := time.Now()
	grace := time.Duration(c.StartPeriod) * time.Second
	what := c.Kind
	if c.Kind == "http" {
		what = "GET " + c.Path
	}
	w.Queue.Log(ctx, jobID, "info", "health", fmt.Sprintf("Health check: %s, up to %ds start period, %d attempts", what, c.StartPeriod, c.Retries))
	failures := 0
	last := ""
	for {
		select {
		case <-ctx.Done():
			return errCancelled
		case <-time.After(time.Second):
		}
		r, err := w.Probe(ctx, containerID, res.EnvironmentID, res.Port, c)
		if err != nil {
			var inc errInconclusive
			if errors.As(err, &inc) && strings.Contains(inc.msg, "doesn't run in Docker") {
				w.Queue.Log(ctx, jobID, "warn", "health", "Skipping the health check: "+inc.msg)
				return nil
			}
			last = err.Error()
		} else {
			w.Store.RecordHealth(ctx, res.ID, store.HealthResult{OK: r.OK, LatencyMS: r.LatencyMS, Detail: r.Detail, Source: "deploy"})
			if r.OK {
				w.Queue.Log(ctx, jobID, "info", "health", fmt.Sprintf("Healthy: %s (%d ms)", r.Detail, r.LatencyMS))
				return nil
			}
			last = r.Detail
			if r.Detail == "The container isn't running" {
				return fmt.Errorf("%w: the container stopped during the health check. The previous release keeps running.", jobs.ErrPermanent)
			}
		}
		inGrace := time.Since(started) < grace
		if !inGrace {
			failures++
		}
		level := "info"
		if !inGrace {
			level = "warn"
		}
		suffix := " (start period)"
		if !inGrace {
			suffix = fmt.Sprintf(" (%d/%d)", failures, c.Retries)
		}
		w.Queue.Log(ctx, jobID, level, "health", last+suffix)
		if failures >= c.Retries {
			return fmt.Errorf("%w: health check failed: %s. The previous release keeps running.", jobs.ErrPermanent, last)
		}
		select {
		case <-ctx.Done():
			return errCancelled
		case <-time.After(interval - time.Second):
		}
	}
}

/* --------------------------------- Monitoring ------------------------------- */

// HealthLoop checks every running resource that has a health check, on its own interval.
func (w *Worker) HealthLoop(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var mu sync.Mutex
	busy := map[string]bool{}
	sem := make(chan struct{}, 8)
	var targets []store.HealthTarget
	var loaded time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if time.Since(loaded) > 3*time.Second {
			t, err := w.Store.HealthTargets(ctx)
			if err != nil {
				continue
			}
			targets, loaded = t, time.Now()
		}
		for i := range targets {
			t := &targets[i]
			due := t.State.LastCheckedAt == nil || time.Since(*t.State.LastCheckedAt) >= time.Duration(t.Config.IntervalSeconds)*time.Second
			mu.Lock()
			skip := busy[t.ResourceID] || !due
			if !skip {
				busy[t.ResourceID] = true
			}
			mu.Unlock()
			if skip {
				continue
			}
			now := time.Now()
			t.State.LastCheckedAt = &now // until the next reload, so it isn't picked again next second
			go func(t store.HealthTarget) {
				sem <- struct{}{}
				defer func() { <-sem; mu.Lock(); delete(busy, t.ResourceID); mu.Unlock() }()
				w.monitorOnce(ctx, t)
			}(*t)
		}
	}
}

func (w *Worker) monitorOnce(ctx context.Context, t store.HealthTarget) {
	c := t.Config
	if _, st, err := w.Store.GetHealth(ctx, t.ResourceID); err == nil {
		t.State = st // the list can be a few seconds old; failures must count exactly
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutSeconds+10)*time.Second)
	defer cancel()
	r, err := w.Probe(cctx, t.ContainerID, t.EnvironmentID, t.Port, c)
	if err != nil {
		slog.Debug("health check inconclusive", "resource", t.Name, "reason", err)
		w.Store.SetHealthState(ctx, t.ResourceID, t.State.State, t.State.Failures, false)
		return
	}
	w.Store.RecordHealth(ctx, t.ResourceID, store.HealthResult{OK: r.OK, LatencyMS: r.LatencyMS, Detail: r.Detail, Source: "monitor"})

	// The start period runs from the container's start or the last automatic restart, whichever is later.
	since := time.Time{}
	if t.RunningSince != nil {
		since = *t.RunningSince
	}
	if n := len(t.State.Restarts); n > 0 && t.State.Restarts[n-1].After(since) {
		since = t.State.Restarts[n-1]
	}
	inGrace := time.Since(since) < time.Duration(c.StartPeriod)*time.Second
	prev := t.State.State
	switch {
	case r.OK:
		w.Store.SetHealthState(ctx, t.ResourceID, "healthy", 0, prev != "healthy")
	case inGrace:
		w.Store.SetHealthState(ctx, t.ResourceID, "starting", 0, prev != "starting")
	default:
		failures := t.State.Failures + 1
		if failures < c.Retries {
			w.Store.SetHealthState(ctx, t.ResourceID, prev, failures, false)
			return
		}
		w.Store.SetHealthState(ctx, t.ResourceID, "unhealthy", failures, prev != "unhealthy")
		if !c.RestartUnhealthy || failures != c.Retries {
			return // restart once per streak of failures
		}
		recent := 0
		for _, at := range t.State.Restarts {
			if time.Since(at) < 15*time.Minute {
				recent++
			}
		}
		if recent >= 3 {
			w.Store.RecordHealth(ctx, t.ResourceID, store.HealthResult{Detail: "Not restarting again: 3 automatic restarts in 15 minutes didn't help. Check the logs.", Source: "restart"})
			return
		}
		slog.Info("restarting unhealthy resource", "resource", t.Name, "failures", failures)
		if err := w.Runtime.Restart(ctx, t.ContainerID, 10*time.Second); err != nil {
			w.Store.RecordHealth(ctx, t.ResourceID, store.HealthResult{Detail: "Automatic restart failed: " + err.Error(), Source: "restart"})
			return
		}
		w.Store.NoteHealthRestart(ctx, t.ResourceID)
		w.Store.RecordHealth(ctx, t.ResourceID, store.HealthResult{Detail: fmt.Sprintf("Restarted after %d failed checks in a row", failures), Source: "restart"})
	}
}

// healthTest runs a one-off check (from the Health Checks page) with the settings being edited.
func (w *Worker) healthTest(ctx context.Context, job *jobs.Job) error {
	var p struct {
		Config store.HealthConfig `json:"config"`
	}
	json.Unmarshal(job.Payload, &p)
	res, err := w.Store.GetResource(ctx, *job.ResourceID)
	if err != nil {
		return fmt.Errorf("%w: resource no longer exists", jobs.ErrPermanent)
	}
	var out struct {
		ProbeResult
		Inconclusive string `json:"inconclusive,omitempty"`
	}
	if res.ContainerID == "" || res.Status != "running" {
		out.Inconclusive = res.Name + " isn't running, so there's nothing to check."
	} else {
		r, err := w.Probe(ctx, res.ContainerID, res.EnvironmentID, res.Port, p.Config)
		if err != nil {
			out.Inconclusive = "Couldn't check: " + err.Error() + "."
		} else {
			out.ProbeResult = r
			w.Store.RecordHealth(ctx, res.ID, store.HealthResult{OK: r.OK, LatencyMS: r.LatencyMS, Detail: r.Detail, Source: "test"})
		}
	}
	b, _ := json.Marshal(out)
	w.Queue.Progress(ctx, job.ID, string(b))
	return nil
}
