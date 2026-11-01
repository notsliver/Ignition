package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Volume helpers work on a resource's volumes without touching its container, so they behave the
// same whether the app is running or stopped. Each one is a short-lived busybox container labelled
// as a probe, so a copy left behind by a crash is cleaned up with the other helpers.

func (d *Docker) ensureProbeImage(ctx context.Context) error {
	if err := d.doJSON(ctx, "GET", "/images/"+url.PathEscape(ProbeImage)+"/json", nil, nil, nil); IsNotFound(err) {
		return d.PullImage(ctx, ProbeImage, "", func(string, string) {})
	}
	return nil
}

func (d *Docker) createVolumeHelper(ctx context.Context, mounts map[string]string, cmd []string) (string, error) {
	if err := d.ensureProbeImage(ctx); err != nil {
		return "", err
	}
	var list []map[string]any
	for name, target := range mounts {
		list = append(list, map[string]any{"Type": "volume", "Source": name, "Target": target})
	}
	body := map[string]any{"Image": ProbeImage, "Labels": map[string]string{"ignition.managed": "true", "ignition.probe": "true", "ignition.volumehelper": "true"},
		"HostConfig": map[string]any{"Mounts": list, "Memory": 256 << 20}}
	if len(cmd) > 0 {
		body["Cmd"] = cmd
	}
	var created struct{ Id string }
	if err := d.doJSON(ctx, "POST", "/containers/create", nil, body, &created); err != nil {
		return "", err
	}
	return created.Id, nil
}

// VolumeRun runs a shell script with the volumes mounted (name -> path) and returns its exit code and
// combined output. Volumes that don't exist yet are created empty.
func (d *Docker) VolumeRun(ctx context.Context, mounts map[string]string, script string, timeout time.Duration) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout+30*time.Second)
	defer cancel()
	id, err := d.createVolumeHelper(ctx, mounts, []string{"sh", "-c", script})
	if err != nil {
		return 0, "", err
	}
	defer d.Remove(context.WithoutCancel(ctx), id, false)
	if err := d.Start(ctx, id); err != nil {
		return 0, "", err
	}
	waitCtx, stop := context.WithTimeout(ctx, timeout)
	defer stop()
	var waited struct{ StatusCode int }
	if err := d.doJSON(waitCtx, "POST", "/containers/"+id+"/wait", nil, nil, &waited); err != nil {
		return 0, "", errors.New("the file operation timed out")
	}
	lines, err := d.Logs(ctx, id, 200000, time.Time{})
	if err != nil {
		return 0, "", err
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Message)
		b.WriteByte('\n')
	}
	return waited.StatusCode, strings.TrimRight(b.String(), "\n"), nil
}

// VolumeHelper creates a stopped helper with the volumes mounted, for the archive API (reading and
// writing files works on a stopped container). Call done when finished.
func (d *Docker) VolumeHelper(ctx context.Context, mounts map[string]string) (id string, done func(), err error) {
	id, err = d.createVolumeHelper(ctx, mounts, []string{"true"})
	if err != nil {
		return "", nil, err
	}
	return id, func() { d.Remove(context.WithoutCancel(ctx), id, false) }, nil
}

// GetArchive streams a tar of path from a container (running or stopped).
func (d *Docker) GetArchive(ctx context.Context, container, path string) (io.ReadCloser, error) {
	resp, err := d.do(ctx, "GET", "/containers/"+url.PathEscape(container)+"/archive", url.Values{"path": {path}}, nil, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, &apiError{Status: 404, Message: "no such file"}
		}
		return nil, fmt.Errorf("read %s: %s", path, resp.Status)
	}
	return resp.Body, nil
}

// PutArchive extracts a tar stream into dir inside a container (running or stopped).
func (d *Docker) PutArchive(ctx context.Context, container, dir string, tarball io.Reader) error {
	resp, err := d.do(ctx, "PUT", "/containers/"+url.PathEscape(container)+"/archive", url.Values{"path": {dir}}, tarball, "application/x-tar")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("write into %s: %s %s", dir, resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}
