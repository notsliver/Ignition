package runtime

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// The host probe is a long-lived busybox container on the host's network namespace. Ignition
// execs `cat /proc/...` in it to read host-wide CPU, memory, disk and network counters on any
// server it can reach through the Engine API, with no agent to install. It has no capabilities,
// a read-only root filesystem and 16 MB of memory, and only ever runs cat, df and sleep.
const hostProbeLabel = "ignition.hostprobe"

func HostProbeName(instance string) string {
	if instance == "" {
		return "ignition-hostprobe"
	}
	return "ignition-hostprobe-" + instance
}

// HostProbeScript prints each source after an @@name marker so one exec reads everything.
func HostProbeScript(withCPUInfo bool) string {
	files := "stat loadavg meminfo uptime diskstats net/dev"
	if withCPUInfo {
		files += " cpuinfo"
	}
	return `for f in ` + files + `; do echo "@@$f"; cat /proc/$f 2>/dev/null; done; echo "@@df"; df -Pk / 2>/dev/null`
}

// SplitHostProbe turns probe output back into sections keyed by name.
func SplitHostProbe(out string) map[string]string {
	sections := map[string]string{}
	name := ""
	var b strings.Builder
	flush := func() {
		if name != "" {
			sections[name] = b.String()
		}
		b.Reset()
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "@@") {
			flush()
			name = strings.TrimPrefix(line, "@@")
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	flush()
	return sections
}

// EnsureHostProbe returns the running probe's ID, creating or starting it when needed.
func (d *Docker) EnsureHostProbe(ctx context.Context, instance string) (string, error) {
	name := HostProbeName(instance)
	var c struct {
		Id    string
		State struct{ Running bool }
	}
	err := d.doJSON(ctx, "GET", "/containers/"+url.PathEscape(name)+"/json", nil, nil, &c)
	if err == nil {
		if !c.State.Running {
			if err := d.Start(ctx, c.Id); err != nil {
				return "", fmt.Errorf("start host probe: %w", err)
			}
		}
		return c.Id, nil
	}
	if !IsNotFound(err) {
		return "", err
	}
	if err := d.doJSON(ctx, "GET", "/images/"+url.PathEscape(ProbeImage)+"/json", nil, nil, nil); IsNotFound(err) {
		if err := d.PullImage(ctx, ProbeImage, "", func(string, string) {}); err != nil {
			return "", fmt.Errorf("pull %s: %w", ProbeImage, err)
		}
	}
	body := map[string]any{
		"Image":  ProbeImage,
		"Cmd":    []string{"sh", "-c", "trap 'exit 0' TERM INT; while :; do sleep 3600 & wait $!; done"},
		"Labels": map[string]string{"ignition.managed": "true", hostProbeLabel: "true", "ignition.instance": instance},
		"HostConfig": map[string]any{
			"NetworkMode": "host", "Memory": 16 << 20, "NanoCpus": int64(0.1 * 1e9), "PidsLimit": 32,
			"ReadonlyRootfs": true, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges"},
			"RestartPolicy": map[string]any{"Name": "unless-stopped"},
		},
	}
	var created struct{ Id string }
	if err := d.doJSON(ctx, "POST", "/containers/create", url.Values{"name": {name}}, body, &created); err != nil {
		if strings.Contains(err.Error(), "already in use") { // created concurrently
			return d.EnsureHostProbe(ctx, instance)
		}
		return "", fmt.Errorf("create host probe: %w", err)
	}
	if err := d.Start(ctx, created.Id); err != nil {
		return "", fmt.Errorf("start host probe: %w", err)
	}
	return created.Id, nil
}

// RemoveHostProbe deletes the probe; a missing probe isn't an error.
func (d *Docker) RemoveHostProbe(ctx context.Context, instance string) error {
	err := d.doJSON(ctx, "DELETE", "/containers/"+url.PathEscape(HostProbeName(instance)), url.Values{"force": {"true"}}, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// ExecCapture runs a command and returns its stdout (frames joined exactly, not split into lines).
func (d *Docker) ExecCapture(ctx context.Context, container string, cmd []string) (string, int, error) {
	var created struct{ Id string }
	if err := d.doJSON(ctx, "POST", "/containers/"+url.PathEscape(container)+"/exec", nil,
		map[string]any{"Cmd": cmd, "AttachStdout": true, "AttachStderr": true}, &created); err != nil {
		return "", 0, err
	}
	body, _ := json.Marshal(map[string]any{"Detach": false, "Tty": false})
	resp, err := d.do(ctx, "POST", "/exec/"+created.Id+"/start", nil, bytes.NewReader(body), "application/json")
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	header := make([]byte, 8)
	for out.Len() < 4<<20 {
		if _, err := io.ReadFull(resp.Body, header); err != nil {
			break
		}
		frame := make([]byte, binary.BigEndian.Uint32(header[4:]))
		if _, err := io.ReadFull(resp.Body, frame); err != nil {
			break
		}
		if header[0] == 1 {
			out.Write(frame)
		}
	}
	if ctx.Err() != nil {
		return "", 0, ctx.Err()
	}
	var inspect struct{ ExitCode int }
	if err := d.doJSON(context.WithoutCancel(ctx), "GET", "/exec/"+created.Id+"/json", nil, nil, &inspect); err != nil {
		return "", 0, err
	}
	return out.String(), inspect.ExitCode, nil
}
