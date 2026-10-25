package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Docker talks to the Docker Engine API over its unix socket using structured JSON requests.
// No shell commands are assembled, so user input can't inject arguments.
type Docker struct {
	client *http.Client
	base   string

	instanceOnce sync.Once
	instance     string
}

// apiVersion is the oldest Engine API we rely on. Docker 29 requires >= 1.44.
const apiVersion = "v1.44"

func NewDocker(socket string) *Docker {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}
	return &Docker{client: &http.Client{Transport: transport}, base: "http://docker/" + apiVersion}
}

type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return fmt.Sprintf("docker: %s (%d)", e.Message, e.Status) }

func IsNotFound(err error) bool {
	var e *apiError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

func (d *Docker) do(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	u := d.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker unreachable: %w", err)
	}
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified {
		defer resp.Body.Close()
		var msg struct{ Message string }
		json.NewDecoder(resp.Body).Decode(&msg)
		return nil, &apiError{Status: resp.StatusCode, Message: msg.Message}
	}
	return resp, nil
}

// doWithHeaders is do() plus extra request headers (empty values are skipped).
func (d *Docker) doWithHeaders(ctx context.Context, method, path string, query url.Values, headers map[string]string) (*http.Response, error) {
	u := d.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker unreachable: %w", err)
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var msg struct{ Message string }
		json.NewDecoder(resp.Body).Decode(&msg)
		return nil, &apiError{Status: resp.StatusCode, Message: msg.Message}
	}
	return resp, nil
}

// RegistryLogin asks the Docker Engine to verify registry credentials (POST /auth). Nothing is stored by Docker.
func (d *Docker) RegistryLogin(ctx context.Context, username, password, server string) error {
	err := d.doJSON(ctx, "POST", "/auth", nil, map[string]string{"username": username, "password": password, "serveraddress": server}, nil)
	var e *apiError
	if errors.As(err, &e) && (e.Status == 401 || e.Status == 403 || e.Status == 500) {
		return fmt.Errorf("registry rejected the credentials: %s", strings.TrimSpace(e.Message))
	}
	return err
}

func (d *Docker) doJSON(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var body io.Reader
	ct := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, ct = bytes.NewReader(b), "application/json"
	}
	resp, err := d.do(ctx, method, path, query, body, ct)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

func (d *Docker) Ping(ctx context.Context) error {
	resp, err := d.do(ctx, "GET", "/_ping", nil, nil, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// readProgress drains Docker's JSON-lines progress stream and surfaces errors.
func readProgress(r io.Reader, log Logf) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	last := ""
	for scanner.Scan() {
		var msg struct {
			Stream string `json:"stream"`
			Status string `json:"status"`
			ID     string `json:"id"`
			Error  string `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}
		if msg.Error != "" {
			return errors.New(strings.TrimSpace(msg.Error))
		}
		line := strings.TrimRight(msg.Stream, "\n")
		if line == "" && msg.Status != "" {
			line = strings.TrimSpace(msg.ID + " " + msg.Status)
		}
		// Pull progress repeats "Downloading" per layer; only log meaningful changes.
		if line != "" && line != last && !strings.HasSuffix(line, "Downloading") && !strings.HasSuffix(line, "Extracting") {
			log("info", line)
			last = line
		}
	}
	return scanner.Err()
}

func splitRef(ref string) (string, string) {
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

func (d *Docker) PullImage(ctx context.Context, ref, auth string, log Logf) error {
	name, tag := splitRef(ref)
	resp, err := d.doWithHeaders(ctx, "POST", "/images/create", url.Values{"fromImage": {name}, "tag": {tag}}, map[string]string{"X-Registry-Auth": auth})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return readProgress(resp.Body, log)
}

func (d *Docker) BuildImage(ctx context.Context, contextTar io.Reader, dockerfile, tag string, log Logf) error {
	q := url.Values{"t": {tag}, "dockerfile": {dockerfile}, "rm": {"1"}, "forcerm": {"1"}}
	resp, err := d.do(ctx, "POST", "/build", q, contextTar, "application/x-tar")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return readProgress(resp.Body, log)
}

func (d *Docker) EnsureNetwork(ctx context.Context, name string) error {
	err := d.doJSON(ctx, "GET", "/networks/"+url.PathEscape(name), nil, nil, nil)
	if err == nil {
		return nil
	}
	if !IsNotFound(err) {
		return err
	}
	return d.doJSON(ctx, "POST", "/networks/create", nil, map[string]any{"Name": name, "Driver": "bridge", "Labels": map[string]string{"ignition.managed": "true"}}, nil)
}

func (d *Docker) CreateContainer(ctx context.Context, s ContainerSpec) (string, error) {
	hostConfig := map[string]any{
		"Memory":        int64(s.MemoryMB) * 1024 * 1024,
		"NanoCpus":      int64(s.CPUs * 1e9),
		"RestartPolicy": map[string]any{"Name": "unless-stopped"},
		"NetworkMode":   s.Network,
	}
	if len(s.Volumes) > 0 {
		var mounts []map[string]any
		for name, target := range s.Volumes {
			mounts = append(mounts, map[string]any{"Type": "volume", "Source": name, "Target": target,
				"VolumeOptions": map[string]any{"Labels": map[string]string{"ignition.managed": "true"}}})
		}
		hostConfig["Mounts"] = mounts
	}
	body := map[string]any{"Image": s.Image, "Env": s.Env, "Labels": s.Labels, "HostConfig": hostConfig}
	if len(s.Cmd) > 0 {
		body["Cmd"] = s.Cmd
	}
	if s.Network != "" {
		// API 1.44+ accepts every network at create time, each with the same aliases.
		endpoints := map[string]any{s.Network: map[string]any{"Aliases": s.Aliases}}
		for _, n := range s.Networks {
			endpoints[n] = map[string]any{"Aliases": s.Aliases}
		}
		body["NetworkingConfig"] = map[string]any{"EndpointsConfig": endpoints}
	}
	if len(s.Ports) > 0 {
		exposed, bindings := map[string]any{}, map[string][]map[string]string{}
		for _, m := range s.Ports {
			key := fmt.Sprintf("%d/%s", m.ContainerPort, protocolOr(m.Protocol))
			exposed[key] = struct{}{}
			host := ""
			if m.HostPort > 0 {
				host = strconv.Itoa(m.HostPort)
			} // empty: Docker picks a free port
			bindings[key] = append(bindings[key], map[string]string{"HostIp": m.HostIP, "HostPort": host})
		}
		body["ExposedPorts"] = exposed
		hostConfig["PortBindings"] = bindings
	}
	var out struct{ Id string }
	err := d.doJSON(ctx, "POST", "/containers/create", url.Values{"name": {s.Name}}, body, &out)
	return out.Id, err
}

func (d *Docker) Start(ctx context.Context, id string) error {
	return d.doJSON(ctx, "POST", "/containers/"+url.PathEscape(id)+"/start", nil, nil, nil)
}

func (d *Docker) Stop(ctx context.Context, id string, timeout time.Duration) error {
	return d.doJSON(ctx, "POST", "/containers/"+url.PathEscape(id)+"/stop", url.Values{"t": {strconv.Itoa(int(timeout.Seconds()))}}, nil, nil)
}

func (d *Docker) Restart(ctx context.Context, id string, timeout time.Duration) error {
	return d.doJSON(ctx, "POST", "/containers/"+url.PathEscape(id)+"/restart", url.Values{"t": {strconv.Itoa(int(timeout.Seconds()))}}, nil, nil)
}

func (d *Docker) Remove(ctx context.Context, id string, removeVolumes bool) error {
	q := url.Values{"force": {"1"}}
	if removeVolumes {
		q.Set("v", "1")
	}
	err := d.doJSON(ctx, "DELETE", "/containers/"+url.PathEscape(id), q, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// RemoveVolume deletes a named volume and its data. Missing volumes are ignored.
func (d *Docker) RemoveVolume(ctx context.Context, name string) error {
	err := d.doJSON(ctx, "DELETE", "/volumes/"+url.PathEscape(name), nil, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

func (d *Docker) Inspect(ctx context.Context, id string) (State, error) {
	var out struct {
		State struct {
			Status     string
			Running    bool
			Restarting bool
			OOMKilled  bool
			ExitCode   int
			StartedAt  time.Time
		}
		Config          struct{ Image string }
		NetworkSettings struct {
			Ports    map[string][]struct{ HostIp, HostPort string }
			Networks map[string]struct {
				IPAddress string
				Aliases   []string
				DNSNames  []string
			}
		}
	}
	err := d.doJSON(ctx, "GET", "/containers/"+url.PathEscape(id)+"/json", nil, nil, &out)
	if IsNotFound(err) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	st := State{Exists: true, Running: out.State.Running, Status: out.State.Status, ExitCode: out.State.ExitCode, StartedAt: out.State.StartedAt,
		Image: out.Config.Image, Restarting: out.State.Restarting, OOMKilled: out.State.OOMKilled}
	for key, bindings := range out.NetworkSettings.Ports {
		port, proto, _ := strings.Cut(key, "/")
		cport, _ := strconv.Atoi(port)
		for _, b := range bindings {
			if p, err := strconv.Atoi(b.HostPort); err == nil {
				st.Ports = append(st.Ports, PortMapping{HostIP: b.HostIp, HostPort: p, ContainerPort: cport, Protocol: proto})
				if st.HostPort == 0 {
					st.HostPort = p
				}
			}
		}
	}
	sort.Slice(st.Ports, func(i, j int) bool { return st.Ports[i].ContainerPort < st.Ports[j].ContainerPort })
	for name, n := range out.NetworkSettings.Networks {
		aliases := n.DNSNames
		if len(aliases) == 0 {
			aliases = n.Aliases
		}
		st.Networks = append(st.Networks, Attachment{Network: name, IP: n.IPAddress, Aliases: aliases})
	}
	sort.Slice(st.Networks, func(i, j int) bool { return st.Networks[i].Network < st.Networks[j].Network })
	return st, nil
}

// Logs returns the last `tail` lines, demultiplexing Docker's stdout/stderr framing.
func (d *Docker) Logs(ctx context.Context, id string, tail int, since time.Time) ([]LogLine, error) {
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}, "tail": {strconv.Itoa(tail)}}
	if !since.IsZero() {
		q.Set("since", strconv.FormatInt(since.Unix(), 10))
	}
	resp, err := d.do(ctx, "GET", "/containers/"+url.PathEscape(id)+"/logs", q, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var lines []LogLine
	header := make([]byte, 8)
	for {
		if _, err := io.ReadFull(resp.Body, header); err != nil {
			break
		}
		size := binary.BigEndian.Uint32(header[4:])
		frame := make([]byte, size)
		if _, err := io.ReadFull(resp.Body, frame); err != nil {
			break
		}
		stream := "stdout"
		if header[0] == 2 {
			stream = "stderr"
		}
		for _, raw := range strings.Split(strings.TrimRight(string(frame), "\n"), "\n") {
			ts, msg, _ := strings.Cut(raw, " ")
			t, _ := time.Parse(time.RFC3339Nano, ts)
			lines = append(lines, LogLine{Time: t, Stream: stream, Message: msg})
		}
	}
	return lines, nil
}

func (d *Docker) Stats(ctx context.Context, id string) (Stats, error) {
	var s struct {
		Read     time.Time
		CPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemUsage uint64 `json:"system_cpu_usage"`
			OnlineCPUs  uint64 `json:"online_cpus"`
		} `json:"cpu_stats"`
		PreCPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemUsage uint64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
		MemoryStats struct {
			Usage uint64
			Limit uint64
			Stats map[string]uint64
		} `json:"memory_stats"`
		Networks map[string]struct {
			RxBytes uint64 `json:"rx_bytes"`
			TxBytes uint64 `json:"tx_bytes"`
		}
	}
	// one-shot=false with stream=false waits for two samples, so precpu_stats is populated.
	if err := d.doJSON(ctx, "GET", "/containers/"+url.PathEscape(id)+"/stats", url.Values{"stream": {"0"}}, nil, &s); err != nil {
		return Stats{}, err
	}
	// Like `docker stats`, exclude reclaimable page cache from memory usage.
	used := s.MemoryStats.Usage
	if cache := s.MemoryStats.Stats["inactive_file"]; cache < used {
		used -= cache
	}
	out := Stats{MemoryBytes: used, MemoryLimit: s.MemoryStats.Limit, CollectedAt: s.Read}
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	if sysDelta > 0 && cpuDelta > 0 {
		out.CPUPercent = cpuDelta / sysDelta * float64(max(s.CPUStats.OnlineCPUs, 1)) * 100
	}
	for _, n := range s.Networks {
		out.NetworkRxB += n.RxBytes
		out.NetworkTxB += n.TxBytes
	}
	return out, nil
}

func protocolOr(p string) string {
	if p == "" {
		return "tcp"
	}
	return p
}

func (d *Docker) Networks(ctx context.Context) ([]Network, error) {
	var raw []struct {
		Name, Driver, Scope string
		Internal            bool
		Labels              map[string]string
	}
	if err := d.doJSON(ctx, "GET", "/networks", nil, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Network, 0, len(raw))
	for _, n := range raw {
		out = append(out, Network{Name: n.Name, Driver: n.Driver, Scope: n.Scope, Internal: n.Internal, Labels: n.Labels})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// HostBindings lists every host port published by any running container, for conflict checks.
func (d *Docker) HostBindings(ctx context.Context) ([]HostBinding, error) {
	var raw []struct {
		Names  []string
		Labels map[string]string
		Ports  []struct {
			IP         string
			PublicPort int
			Type       string
		}
	}
	if err := d.doJSON(ctx, "GET", "/containers/json", nil, nil, &raw); err != nil {
		return nil, err
	}
	var out []HostBinding
	for _, c := range raw {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		for _, p := range c.Ports {
			if p.PublicPort > 0 {
				out = append(out, HostBinding{HostIP: p.IP, HostPort: p.PublicPort, Protocol: p.Type, Container: name, Resource: c.Labels["ignition.resource"]})
			}
		}
	}
	return out, nil
}

// ProbeImage is small, has nslookup and nc, and is pulled on first use.
const ProbeImage = "busybox:1.36"

func (d *Docker) RunProbe(ctx context.Context, p Probe) (int, string, error) {
	if p.Timeout == 0 {
		p.Timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout+30*time.Second) // room for a first-time pull
	defer cancel()
	if err := d.doJSON(ctx, "GET", "/images/"+url.PathEscape(ProbeImage)+"/json", nil, nil, nil); IsNotFound(err) {
		if err := d.PullImage(ctx, ProbeImage, "", func(string, string) {}); err != nil {
			return 0, "", fmt.Errorf("pull %s: %w", ProbeImage, err)
		}
	}
	if len(p.Networks) == 0 {
		return 0, "", errors.New("no network to probe from")
	}
	endpoints := map[string]any{}
	for _, n := range p.Networks { // every network, including NetworkMode: Docker treats this as the full set
		endpoints[n] = map[string]any{}
	}
	body := map[string]any{
		"Image": ProbeImage, "Cmd": p.Cmd, "Labels": map[string]string{"ignition.managed": "true", "ignition.probe": "true"},
		"HostConfig":       map[string]any{"NetworkMode": p.Networks[0], "Memory": 32 << 20, "NanoCpus": int64(0.25 * 1e9), "AutoRemove": false},
		"NetworkingConfig": map[string]any{"EndpointsConfig": endpoints},
	}
	var created struct{ Id string }
	if err := d.doJSON(ctx, "POST", "/containers/create", nil, body, &created); err != nil {
		return 0, "", err
	}
	defer d.Remove(context.WithoutCancel(ctx), created.Id, false)
	if err := d.Start(ctx, created.Id); err != nil {
		return 0, "", err
	}
	waitCtx, stop := context.WithTimeout(ctx, p.Timeout)
	defer stop()
	var waited struct{ StatusCode int }
	if err := d.doJSON(waitCtx, "POST", "/containers/"+created.Id+"/wait", nil, nil, &waited); err != nil {
		return 0, "", errors.New("the test timed out")
	}
	lines, _ := d.Logs(ctx, created.Id, 200, time.Time{})
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Message)
		b.WriteByte('\n')
	}
	return waited.StatusCode, strings.TrimSpace(b.String()), nil
}

// EnsureNetworkExists fails when the network is gone; unlike EnsureNetwork it never creates one.
func (d *Docker) EnsureNetworkExists(ctx context.Context, name string) error {
	err := d.doJSON(ctx, "GET", "/networks/"+url.PathEscape(name), nil, nil, nil)
	if IsNotFound(err) {
		return errors.New("it no longer exists in Docker")
	}
	return err
}

// Ignition's own networks. Names are derived from IDs so renames never orphan a network.
func EnvNetwork(environmentID string) string {
	id := strings.ReplaceAll(environmentID, "-", "")
	if len(id) > 12 {
		id = id[:12]
	}
	return "ign-env-" + id
}

func SharedNetwork(name string) string { return "ign-net-" + name }

func (d *Docker) EnsureManagedNetwork(ctx context.Context, name string, labels map[string]string) error {
	err := d.doJSON(ctx, "GET", "/networks/"+url.PathEscape(name), nil, nil, nil)
	if err == nil || !IsNotFound(err) {
		return err
	}
	all := map[string]string{"ignition.managed": "true", "ignition.instance": d.Instance(ctx)}
	for k, v := range labels {
		all[k] = v
	}
	err = d.doJSON(ctx, "POST", "/networks/create", nil, map[string]any{"Name": name, "Driver": "bridge", "Labels": all}, nil)
	if err != nil && strings.Contains(err.Error(), "already exists") {
		return nil // created concurrently by the api or worker
	}
	return err
}

func (d *Docker) RemoveNetwork(ctx context.Context, name string) error {
	err := d.doJSON(ctx, "DELETE", "/networks/"+url.PathEscape(name), nil, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

func (d *Docker) ConnectNetwork(ctx context.Context, network, container string, aliases []string) error {
	err := d.doJSON(ctx, "POST", "/networks/"+url.PathEscape(network)+"/connect", nil,
		map[string]any{"Container": container, "EndpointConfig": map[string]any{"Aliases": aliases}}, nil)
	if err != nil && (strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "already attached")) {
		return nil
	}
	return err
}

func (d *Docker) DisconnectNetwork(ctx context.Context, network, container string) error {
	err := d.doJSON(ctx, "POST", "/networks/"+url.PathEscape(network)+"/disconnect", nil, map[string]any{"Container": container}, nil)
	if IsNotFound(err) || (err != nil && strings.Contains(err.Error(), "is not connected")) {
		return nil
	}
	return err
}

func (d *Docker) ContainerLabels(ctx context.Context, id string) (map[string]string, error) {
	var out struct {
		Config struct{ Labels map[string]string }
	}
	if err := d.doJSON(ctx, "GET", "/containers/"+url.PathEscape(id)+"/json", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Config.Labels, nil
}

func (d *Docker) FindContainer(ctx context.Context, labels map[string]string) (string, error) {
	var filter []string
	for k, v := range labels {
		filter = append(filter, k+"="+v)
	}
	f, _ := json.Marshal(map[string][]string{"label": filter})
	var out []struct{ Id string }
	if err := d.doJSON(ctx, "GET", "/containers/json", url.Values{"filters": {string(f)}}, nil, &out); err != nil || len(out) == 0 {
		return "", err
	}
	return out[0].Id, nil
}

// Instance names this Ignition install (its compose project), so installs sharing one Docker
// never clean up each other's networks. Empty when not running under compose.
func (d *Docker) Instance(ctx context.Context) string {
	d.instanceOnce.Do(func() {
		self, _ := os.Hostname()
		if labels, err := d.ContainerLabels(ctx, self); err == nil {
			d.instance = labels["com.docker.compose.project"]
		}
	})
	return d.instance
}
