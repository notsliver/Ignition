package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/functions"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/runtime"
)

// InvokeRequest is one test call of a function, made from inside its private network.
type InvokeRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type InvokeResult struct {
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	Truncated  bool              `json:"truncated"`
	DurationMS int               `json:"durationMs"`
	Error      string            `json:"error,omitempty"`
}

func (w *Worker) invokeFunction(ctx context.Context, job *jobs.Job) error {
	var in InvokeRequest
	json.Unmarshal(job.Payload, &in)
	out := w.invoke(ctx, *job.ResourceID, in)
	b, _ := json.Marshal(out)
	w.Queue.Progress(ctx, job.ID, string(b))
	return nil
}

func (w *Worker) invoke(ctx context.Context, resourceID string, in InvokeRequest) InvokeResult {
	res, err := w.Store.GetResource(ctx, resourceID)
	if err != nil {
		return InvokeResult{Error: "The function no longer exists."}
	}
	if res.ContainerID == "" || res.Status != "running" {
		return InvokeResult{Error: res.Name + " isn't running. Deploy it first."}
	}
	network := runtime.EnvNetwork(res.EnvironmentID)
	if err := w.joinNetwork(ctx, network); err != nil {
		return InvokeResult{Error: "Couldn't reach the function: " + err.Error()}
	}
	st, err := w.Runtime.Inspect(ctx, res.ContainerID)
	if err != nil || !st.Running {
		return InvokeResult{Error: "The function's container isn't running."}
	}
	ip := ""
	for _, a := range st.Networks {
		if a.Network == network {
			ip = a.IP
		}
	}
	port := functions.Port
	if res.Port != nil {
		port = *res.Port
	}
	p := "/" + strings.TrimLeft(in.Path, "/")
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, firstNonEmpty(strings.ToUpper(in.Method), "GET"), "http://"+net.JoinHostPort(ip, strconv.Itoa(port))+p, bytes.NewReader([]byte(in.Body)))
	if err != nil {
		return InvokeResult{Error: "That request isn't valid: " + err.Error()}
	}
	for k, v := range in.Headers {
		req.Header.Set(k, v)
	}
	if in.Body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	started := time.Now()
	resp, err := healthClient.Do(req)
	out := InvokeResult{DurationMS: int(time.Since(started).Milliseconds()), Headers: map[string]string{}}
	if err != nil {
		out.Error = "No answer: " + dialReason(err, 30*time.Second)
		return out
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10+1))
	out.Status, out.Truncated = resp.StatusCode, len(body) > 64<<10
	if out.Truncated {
		body = body[:64<<10]
	}
	out.Body = string(body)
	for k := range resp.Header {
		out.Headers[strings.ToLower(k)] = resp.Header.Get(k)
	}
	return out
}

// ExternalLoop checks every external database once a minute: can this machine reach it?
func (w *Worker) ExternalLoop(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		w.checkExternals(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (w *Worker) checkExternals(ctx context.Context) {
	rows, err := w.Store.DB.Query(ctx, `SELECT id, name, status FROM resources WHERE source_type = 'external'`)
	if err != nil {
		return
	}
	type ext struct{ id, name, status string }
	var list []ext
	for rows.Next() {
		var e ext
		if rows.Scan(&e.id, &e.name, &e.status) == nil {
			list = append(list, e)
		}
	}
	rows.Close()
	for _, e := range list {
		vars, err := w.envFor(ctx, e.id)
		if err != nil {
			continue
		}
		host, port := "", ""
		for _, v := range vars {
			k, val, _ := strings.Cut(v, "=")
			switch k {
			case "DB_HOST":
				host = val
			case "DB_PORT":
				port = val
			}
		}
		if host == "" || port == "" {
			continue
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 5*time.Second)
		status := "running"
		if err != nil {
			status = "failed"
		} else {
			conn.Close()
		}
		if status != e.status {
			w.Store.SetStatus(ctx, e.id, status)
			slog.Info("external database reachability changed", "resource", e.name, "status", status, "error", fmt.Sprint(err))
		}
	}
}
