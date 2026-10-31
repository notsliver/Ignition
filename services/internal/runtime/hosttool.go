package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// RunHostTool runs a short script in a throwaway container on the host's network namespace with
// NET_ADMIN, for host networking changes (firewall rules). It's labelled as a probe, so a copy
// left behind by a crash is cleaned up like other helpers.
func (d *Docker) RunHostTool(ctx context.Context, image, script string, timeout time.Duration) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout+60*time.Second) // room for a first-time pull
	defer cancel()
	if err := d.doJSON(ctx, "GET", "/images/"+url.PathEscape(image)+"/json", nil, nil, nil); IsNotFound(err) {
		if err := d.PullImage(ctx, image, "", func(string, string) {}); err != nil {
			return 0, "", fmt.Errorf("pull %s: %w", image, err)
		}
	}
	body := map[string]any{
		"Image": image, "Cmd": []string{"sh", "-c", script},
		"Labels":     map[string]string{"ignition.managed": "true", "ignition.probe": "true", "ignition.hosttool": "true"},
		"HostConfig": map[string]any{"NetworkMode": "host", "CapAdd": []string{"NET_ADMIN", "NET_RAW"}, "Memory": 128 << 20},
	}
	var created struct{ Id string }
	if err := d.doJSON(ctx, "POST", "/containers/create", nil, body, &created); err != nil {
		return 0, "", err
	}
	defer d.Remove(context.WithoutCancel(ctx), created.Id, false)
	if err := d.Start(ctx, created.Id); err != nil {
		return 0, "", err
	}
	waitCtx, stop := context.WithTimeout(ctx, timeout)
	defer stop()
	var waited struct{ StatusCode int }
	if err := d.doJSON(waitCtx, "POST", "/containers/"+created.Id+"/wait", nil, nil, &waited); err != nil {
		return 0, "", errors.New("the host tool timed out")
	}
	lines, _ := d.Logs(ctx, created.Id, 500, time.Time{})
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Message)
		b.WriteByte('\n')
	}
	return waited.StatusCode, strings.TrimSpace(b.String()), nil
}
