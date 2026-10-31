package servers

import (
	"context"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/hostinfo"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Diagnostic checks. Each one tests something specific, from a known place, against a fixed
 * target: the server's own engine endpoint, its Docker socket, or a well-known registry. None of
 * them take a user-supplied destination, so the diagnostics page can't be turned into a network
 * scanner. Results say what ran, where, when, how long it took, what came back, and what to do.
 */

type CheckDef struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

var Checks = []CheckDef{
	{"connectivity", "Server connectivity", "Reach the host: the Docker socket for this machine, a TCP connection to the engine for remote servers."},
	{"identity", "Host identity", "Verify the engine's TLS certificate against the pinned CA, hostname and enrolled fingerprint."},
	{"agent", "Agent health and version", "Check the Ignition agent on the host."},
	{"docker", "Docker daemon", "Ping the Docker Engine and read its version."},
	{"platform", "Operating system and architecture", "Detect the OS, kernel and CPU architecture the engine reports."},
	{"compatibility", "Runtime compatibility", "Compare the engine's API version, OS type, architecture and storage driver with what Ignition supports."},
	{"disk", "Available disk capacity", "Measure free space where Docker stores images, containers and volumes."},
	{"memory", "Memory pressure", "Read available memory and swap use."},
	{"clock", "Clock synchronization", "Compare the engine's clock with Ignition's."},
	{"dns", "DNS resolution", "Resolve the names Ignition depends on for this server."},
	{"tcp", "TCP connectivity", "Open TCP connections to the endpoints Ignition depends on for this server."},
	{"ports", "Port conflicts and exposure", "Compare published host ports with what Ignition's resources and proxy expect."},
	{"consistency", "Resource status consistency", "Compare each workload's recorded status with what Docker reports."},
}

func CheckIDs() []string {
	ids := make([]string, len(Checks))
	for i, c := range Checks {
		ids[i] = c.ID
	}
	return ids
}

func ValidCheck(id string) bool {
	for _, c := range Checks {
		if c.ID == id {
			return true
		}
	}
	return false
}

// RegistryHost is the fixed external name local checks resolve and connect to: image pulls need it.
const RegistryHost = "registry-1.docker.io"

type Diagnoser struct {
	Registry *Registry
	Store    *store.Store
	Server   *store.Server
	Verbose  bool
	resolver *net.Resolver
}

func (d *Diagnoser) location() string {
	if d.Server.IsLocal {
		return "Ignition worker on this host"
	}
	return "Ignition worker → " + d.Server.Endpoint
}

// Run executes one check and always returns a result: failures are results, not errors.
func (d *Diagnoser) Run(ctx context.Context, id string) store.DiagnosticResult {
	if d.resolver == nil {
		d.resolver = net.DefaultResolver
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r := result{status: "passed"}
	switch id {
	case "connectivity":
		d.connectivity(ctx, &r)
	case "identity":
		d.identity(ctx, &r)
	case "agent":
		r.unsupported("Ignition has no agent: it talks to the Docker Engine API directly, over the local socket or mutual TLS. There's nothing to install or upgrade on the host.")
	case "docker":
		d.docker(ctx, &r)
	case "platform":
		d.platform(ctx, &r)
	case "compatibility":
		d.compatibility(ctx, &r)
	case "disk":
		d.disk(ctx, &r)
	case "memory":
		d.memory(ctx, &r)
	case "clock":
		d.clock(ctx, &r)
	case "dns":
		d.dns(ctx, &r)
	case "tcp":
		d.tcp(ctx, &r)
	case "ports":
		d.ports(ctx, &r)
	case "consistency":
		d.consistency(ctx, &r)
	default:
		r.fail("Unknown check.", "")
	}
	ms := int(time.Since(start).Milliseconds())
	limit := 2 << 10
	if d.Verbose {
		limit = 16 << 10
	}
	out := Redact(strings.TrimSpace(r.out.String()))
	if len(out) > limit {
		out = out[:limit] + "\n… (truncated; switch diagnostic output to verbose in Settings for more)"
	}
	return store.DiagnosticResult{Check: id, Status: r.status, Location: d.location(), Summary: r.summary, Output: out, Hint: r.hint, DurationMS: &ms, RanAt: start.UTC()}
}

type result struct {
	status, summary, hint string
	out                   strings.Builder
}

func (r *result) printf(f string, a ...any) { fmt.Fprintf(&r.out, f+"\n", a...) }
func (r *result) pass(s string)             { r.status, r.summary = "passed", s }
func (r *result) warn(s, hint string)       { r.status, r.summary, r.hint = "warning", s, hint }
func (r *result) fail(s, hint string)       { r.status, r.summary, r.hint = "failed", s, hint }
func (r *result) unsupported(s string)      { r.status, r.summary = "unsupported", s }
func (r *result) skip(s string)             { r.status, r.summary = "skipped", s }

func (d *Diagnoser) engine(ctx context.Context, r *result) (*runtime.Docker, *runtime.EngineInfo, bool) {
	dk, err := d.Registry.Docker(d.Server)
	if err != nil {
		r.skip("Skipped: " + err.Error())
		return nil, nil, false
	}
	info, err := dk.EngineInfo(ctx)
	if err != nil {
		msg, _ := Classify(err)
		r.skip("Skipped: the engine isn't reachable (" + msg + ")")
		return dk, nil, false
	}
	return dk, &info, true
}

func (d *Diagnoser) connectivity(ctx context.Context, r *result) {
	if d.Server.IsLocal {
		sock := d.Registry.Local.SocketPath()
		r.printf("socket: %s", sock)
		st, err := os.Stat(sock)
		if err != nil {
			r.fail("No Docker socket at "+sock+".", "Mount the Docker socket into the Ignition api and worker containers (see deploy/docker-compose.yml).")
			return
		}
		r.printf("mode: %s", st.Mode())
		t0 := time.Now()
		if err := d.Registry.Local.Ping(ctx); err != nil {
			r.fail("The socket exists but Docker didn't answer: "+cleanErr(err), "Check the daemon: systemctl status docker; and that the socket's group lets Ignition read it.")
			return
		}
		r.pass(fmt.Sprintf("Docker socket answered in %d ms.", time.Since(t0).Milliseconds()))
		return
	}
	host, port, _ := net.SplitHostPort(d.Server.Endpoint)
	r.printf("target: %s (port %s)", host, port)
	t0 := time.Now()
	conn, err := (&net.Dialer{Timeout: ParseSettings(d.Server.Settings).Timeout()}).DialContext(ctx, "tcp", d.Server.Endpoint)
	if err != nil {
		msg, kind := Classify(err)
		r.printf("error: %v", err)
		hint := "Check the server is up, the address and port are right, and no firewall blocks Ignition's address."
		if kind == "dns" {
			hint = "Fix the DNS record or use the server's IP address in Settings › Connection."
		}
		r.fail(msg, hint)
		return
	}
	r.printf("connected from %s to %s", conn.LocalAddr(), conn.RemoteAddr())
	conn.Close()
	r.pass(fmt.Sprintf("TCP connection to %s opened in %d ms.", d.Server.Endpoint, time.Since(t0).Milliseconds()))
}

func (d *Diagnoser) identity(ctx context.Context, r *result) {
	if d.Server.IsLocal {
		r.unsupported("Not applicable: this machine is reached through its local Docker socket, which only processes on this host can open.")
		return
	}
	if len(d.Server.ClientKeySealed) == 0 {
		r.fail("No credentials are stored (they were revoked).", "Rotate in a new client certificate and key from Access & Security.")
		return
	}
	plain, err := d.Registry.Secrets.Open(d.Server.ClientKeySealed, KeyContext(d.Server.ID))
	if err != nil {
		r.fail("The stored client key can't be decrypted.", "IGNITION_SECRET_KEY may have changed. Rotate credentials to store a new key.")
		return
	}
	creds := Credentials{CACert: d.Server.CACert, ClientCert: d.Server.ClientCert, ClientKey: plain}
	var seen string
	dk, err := RemoteDocker(d.Server.Endpoint, creds, d.Server.TLSFingerprint, &seen, ParseSettings(d.Server.Settings).Timeout())
	if err != nil {
		r.fail(err.Error(), "")
		return
	}
	r.printf("expected fingerprint: %s", d.Server.TLSFingerprint)
	if c := ClientCertSummary(d.Server.ClientCert); c != nil {
		r.printf("client certificate: %s, expires %s", c.Subject, c.NotAfter.Format(time.RFC3339))
		if time.Until(c.NotAfter) < 14*24*time.Hour {
			defer func() {
				if r.status == "passed" {
					r.warn("Identity verified, but the client certificate expires "+c.NotAfter.Format("2 Jan 2006")+".", "Rotate credentials in Access & Security before it expires.")
				}
			}()
		}
	}
	if err := dk.Ping(ctx); err != nil {
		r.printf("presented fingerprint: %s", seen)
		msg, kind := Classify(err)
		switch kind {
		case "identity":
			r.fail(msg, "Confirm on the host that its certificate was intentionally replaced (docker info, or the daemon's --tlscert). Then rotate credentials to accept the new identity.")
		case "auth":
			r.fail(msg, "Issue a new client certificate from the CA the daemon trusts and rotate credentials.")
		default:
			r.skip("Skipped: the engine isn't reachable (" + msg + ")")
		}
		return
	}
	r.printf("presented fingerprint: %s", seen)
	r.pass("The engine's certificate matches the CA, hostname and enrolled fingerprint, and it accepted Ignition's client certificate.")
}

func (d *Diagnoser) docker(ctx context.Context, r *result) {
	_, info, ok := d.engine(ctx, r)
	if !ok {
		if r.status == "skipped" {
			r.fail(strings.TrimPrefix(r.summary, "Skipped: "), "Check the daemon is running (systemctl status docker) and reachable.")
		}
		return
	}
	r.printf("version: %s (API %s, minimum %s)", info.Version, info.APIVersion, info.MinAPIVersion)
	r.printf("containers: %d (%d running, %d paused, %d stopped), images: %d", info.Containers, info.Running, info.Paused, info.Stopped, info.Images)
	r.printf("storage driver: %s, cgroup: %s %s", info.StorageDriver, info.CgroupDriver, info.CgroupVersion)
	for _, w := range info.Warnings {
		r.printf("warning: %s", w)
	}
	if len(info.Warnings) > 0 {
		r.warn(fmt.Sprintf("Docker %s is running but reports %d warning(s).", info.Version, len(info.Warnings)), "Read the warnings in the output; they usually concern kernel features or insecure settings.")
		return
	}
	r.pass(fmt.Sprintf("Docker %s is running.", info.Version))
}

func (d *Diagnoser) platform(ctx context.Context, r *result) {
	_, info, ok := d.engine(ctx, r)
	if !ok {
		return
	}
	r.printf("os: %s (%s)", info.OS, info.OSType)
	r.printf("kernel: %s", info.Kernel)
	r.printf("architecture: %s, CPUs: %d, memory: %s", info.Arch, info.NCPU, bytesText(info.MemTotal))
	r.printf("hostname: %s", info.Name)
	r.pass(fmt.Sprintf("%s on %s, kernel %s.", info.OS, info.Arch, info.Kernel))
}

func (d *Diagnoser) compatibility(ctx context.Context, r *result) {
	_, info, ok := d.engine(ctx, r)
	if !ok {
		return
	}
	var problems, notes []string
	if !runtime.APICompatible(info.APIVersion) {
		problems = append(problems, fmt.Sprintf("Engine API %s is older than %s", info.APIVersion, runtime.MinAPIVersion))
	}
	if info.OSType != "linux" {
		problems = append(problems, "the engine runs "+info.OSType+" containers; Ignition runs Linux images")
	}
	if info.Arch != "x86_64" && info.Arch != "aarch64" && info.Arch != "amd64" && info.Arch != "arm64" {
		notes = append(notes, "architecture "+info.Arch+" may not have images for many stacks")
	}
	if info.StorageDriver != "overlay2" && info.StorageDriver != "overlayfs" && info.StorageDriver != "" {
		notes = append(notes, "storage driver "+info.StorageDriver+" (overlay2 is recommended)")
	}
	if info.Rootless {
		notes = append(notes, "rootless Docker: ports below 1024 need extra host configuration")
	}
	r.printf("api: %s (needs ≥ %s), os type: %s, arch: %s, storage: %s, rootless: %v", info.APIVersion, runtime.MinAPIVersion, info.OSType, info.Arch, info.StorageDriver, info.Rootless)
	for _, n := range notes {
		r.printf("note: %s", n)
	}
	switch {
	case len(problems) > 0:
		r.fail("Incompatible: "+strings.Join(problems, "; ")+".", "Upgrade to Docker Engine 25 or newer on a Linux host.")
	case len(notes) > 0:
		r.warn("Compatible, with caveats: "+strings.Join(notes, "; ")+".", "")
	default:
		r.pass("Docker " + info.Version + " on " + info.Arch + " is fully supported.")
	}
}

func (d *Diagnoser) disk(ctx context.Context, r *result) {
	dk, info, ok := d.engine(ctx, r)
	if !ok {
		return
	}
	du, err := dk.DiskUsage(ctx)
	if err == nil {
		var vol, cache int64
		for _, v := range du.Volumes {
			vol += max(v.Size, 0)
		}
		for _, b := range du.BuildCache {
			if !b.Shared {
				cache += b.Size
			}
		}
		r.printf("docker data root: %s", info.DockerRootDir)
		r.printf("images: %s, volumes: %s, build cache: %s", bytesText(du.LayersSize), bytesText(vol), bytesText(cache))
	}
	if !d.Server.IsLocal || !hostinfo.Available() {
		r.unsupported("Filesystem capacity isn't measurable through the Docker API. Docker's own usage is in the output.")
		return
	}
	h := hostinfo.Read()
	if !h.FSOK {
		r.unsupported("The filesystem can't be measured from here.")
		return
	}
	free := h.FSTotal - h.FSUsed
	pct := float64(h.FSUsed) / float64(h.FSTotal) * 100
	r.printf("filesystem holding Docker's data (seen from the worker): %s used of %s (%.1f%%), %s free", bytesText(h.FSUsed), bytesText(h.FSTotal), pct, bytesText(free))
	switch {
	case pct >= 95:
		r.fail(fmt.Sprintf("Disk is %.0f%% full; only %s free.", pct, bytesText(free)), "Free space now: run a Docker cleanup from the Docker page, or remove old images from resources' Advanced Settings.")
	case pct >= 85:
		r.warn(fmt.Sprintf("Disk is %.0f%% full (%s free).", pct, bytesText(free)), "Plan a cleanup: unused images and build cache are usually the biggest wins.")
	default:
		r.pass(fmt.Sprintf("%s free (%.0f%% used).", bytesText(free), pct))
	}
}

func (d *Diagnoser) memory(ctx context.Context, r *result) {
	if !d.Server.IsLocal {
		_, info, ok := d.engine(ctx, r)
		if ok {
			r.printf("engine reports %s total memory", bytesText(info.MemTotal))
			r.unsupported("Memory pressure needs host access Ignition doesn't have on remote servers. The engine only reports total memory (" + bytesText(info.MemTotal) + ").")
		}
		return
	}
	if !hostinfo.Available() {
		r.unsupported("/proc isn't readable here, so memory can't be measured.")
		return
	}
	h := hostinfo.Read()
	if !h.MemOK {
		r.unsupported("/proc/meminfo couldn't be read.")
		return
	}
	availPct := float64(h.MemAvail) / float64(h.MemTotal) * 100
	r.printf("available: %s of %s (%.1f%%)", bytesText(h.MemAvail), bytesText(h.MemTotal), availPct)
	swapUsed := h.SwapTotal - h.SwapFree
	if h.SwapTotal > 0 {
		r.printf("swap: %s used of %s", bytesText(swapUsed), bytesText(h.SwapTotal))
	} else {
		r.printf("swap: none configured")
	}
	switch {
	case availPct < 5:
		r.fail(fmt.Sprintf("Only %.0f%% of memory is available (%s).", availPct, bytesText(h.MemAvail)), "Containers may be OOM-killed. Lower resource memory limits, stop unused workloads or add memory.")
	case availPct < 15 || (h.SwapTotal > 0 && float64(swapUsed)/float64(h.SwapTotal) > 0.5):
		r.warn(fmt.Sprintf("Memory is tight: %.0f%% available.", availPct), "Review the biggest consumers on the Monitoring page.")
	default:
		r.pass(fmt.Sprintf("%s available (%.0f%%).", bytesText(h.MemAvail), availPct))
	}
}

func (d *Diagnoser) clock(ctx context.Context, r *result) {
	dk, _, ok := d.engine(ctx, r)
	if !ok {
		return
	}
	t0 := time.Now()
	info, err := dk.EngineInfo(ctx)
	rtt := time.Since(t0)
	if err != nil || info.SystemTime.IsZero() {
		r.unsupported("The engine didn't report its clock.")
		return
	}
	skew := info.SystemTime.Sub(t0.Add(rtt / 2))
	r.printf("engine time: %s", info.SystemTime.UTC().Format(time.RFC3339Nano))
	r.printf("ignition time: %s (round trip %d ms)", t0.Add(rtt/2).UTC().Format(time.RFC3339Nano), rtt.Milliseconds())
	abs := skew
	if abs < 0 {
		abs = -abs
	}
	switch {
	case d.Server.IsLocal && abs < 2*time.Second:
		r.pass("Same host clock (difference within measurement error).")
	case abs > 30*time.Second:
		r.fail(fmt.Sprintf("Clocks differ by %s.", abs.Round(time.Second)), "TLS, logs and schedules depend on time. Enable NTP on the host: timedatectl set-ntp true.")
	case abs > 2*time.Second:
		r.warn(fmt.Sprintf("Clocks differ by %s.", abs.Round(100*time.Millisecond)), "Enable NTP on the host: timedatectl set-ntp true.")
	default:
		r.pass(fmt.Sprintf("In sync (difference %d ms, measured ±%d ms).", abs.Milliseconds(), rtt.Milliseconds()/2))
	}
}

// dnsTargets are fixed per server type: never user input.
func (d *Diagnoser) dnsTargets() []string {
	if d.Server.IsLocal {
		return []string{RegistryHost}
	}
	host, _, _ := net.SplitHostPort(d.Server.Endpoint)
	if net.ParseIP(host) != nil {
		return nil
	}
	return []string{host}
}

func (d *Diagnoser) dns(ctx context.Context, r *result) {
	targets := d.dnsTargets()
	if len(targets) == 0 {
		r.skip("Skipped: the server is configured by IP address, so nothing needs resolving.")
		return
	}
	var failed []string
	for _, t := range targets {
		t0 := time.Now()
		addrs, err := d.resolver.LookupHost(ctx, t)
		if err != nil {
			r.printf("%s: %v", t, err)
			failed = append(failed, t)
			continue
		}
		r.printf("%s → %s (%d ms)", t, strings.Join(addrs, ", "), time.Since(t0).Milliseconds())
	}
	if len(failed) > 0 {
		hint := "Check the DNS servers configured for Docker (/etc/docker/daemon.json \"dns\") and the host's /etc/resolv.conf."
		if !d.Server.IsLocal {
			hint = "Fix the server's DNS record, or switch the connection to its IP address in Settings."
		}
		r.fail("Couldn't resolve "+strings.Join(failed, ", ")+".", hint)
		return
	}
	what := "Image pulls can resolve the Docker Hub registry."
	if !d.Server.IsLocal {
		what = "The server's hostname resolves."
	}
	r.pass(what)
}

func (d *Diagnoser) tcp(ctx context.Context, r *result) {
	target := net.JoinHostPort(RegistryHost, "443")
	why := "image pulls from Docker Hub"
	if !d.Server.IsLocal {
		target, why = d.Server.Endpoint, "the Docker Engine API"
	}
	t0 := time.Now()
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", target)
	if err != nil {
		msg, _ := Classify(err)
		r.printf("%s: %v", target, err)
		r.fail("Can't open a TCP connection to "+target+" ("+why+"): "+msg, "Check outbound firewall rules and proxies on the host.")
		return
	}
	conn.Close()
	r.printf("%s: connected in %d ms", target, time.Since(t0).Milliseconds())
	r.pass(fmt.Sprintf("Connected to %s (%s).", target, why))
}

func (d *Diagnoser) ports(ctx context.Context, r *result) {
	dk, _, ok := d.engine(ctx, r)
	if !ok {
		return
	}
	containers, err := dk.ListContainers(ctx)
	if err != nil {
		r.fail("Couldn't list containers: "+cleanErr(err), "")
		return
	}
	type owner struct{ container, resource string }
	bound := map[string]owner{}
	var exposed []string
	for _, c := range containers {
		if c.State != "running" {
			continue
		}
		for _, p := range c.Ports {
			if p.HostPort == 0 {
				continue
			}
			key := fmt.Sprintf("%d/%s", p.HostPort, p.Protocol)
			bound[key] = owner{c.Name, c.Labels["ignition.resource"]}
			public := p.HostIP == "" || p.HostIP == "0.0.0.0" || p.HostIP == "::"
			if public && c.Labels["ignition.resource"] == "" && c.Labels["com.docker.compose.project"] == "" {
				exposed = append(exposed, fmt.Sprintf("%s publishes %s on all interfaces", c.Name, key))
			}
		}
	}
	keys := make([]string, 0, len(bound))
	for k := range bound {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		o := bound[k]
		r.printf("%-11s %s", k, o.container)
	}
	var conflicts []string
	if d.Server.IsLocal && d.Store != nil {
		claims, _ := d.Store.ServerPortClaims(ctx, d.Server.ID)
		for _, c := range claims {
			o, used := bound[fmt.Sprintf("%d/%s", c.HostPort, c.Protocol)]
			if used && o.resource != c.ResourceID {
				conflicts = append(conflicts, fmt.Sprintf("%s wants %d/%s but %s holds it", c.ResourceName, c.HostPort, c.Protocol, o.container))
			}
		}
		for _, p := range []int{store.ProxyHTTPPort, store.ProxyHTTPSPort} {
			if o, used := bound[fmt.Sprintf("%d/tcp", p)]; used && !strings.Contains(o.container, "proxy") {
				conflicts = append(conflicts, fmt.Sprintf("the domain proxy needs %d/tcp but %s holds it", p, o.container))
			}
		}
	}
	for _, c := range conflicts {
		r.printf("conflict: %s", c)
	}
	for _, e := range exposed {
		r.printf("exposure: %s", e)
	}
	switch {
	case len(conflicts) > 0:
		r.fail(fmt.Sprintf("%d port conflict(s): %s.", len(conflicts), strings.Join(conflicts, "; ")), "Stop the other container or change the resource's host port on its Networking page.")
	case len(exposed) > 0:
		r.warn(fmt.Sprintf("%d container(s) not managed by Ignition publish ports on every interface.", len(exposed)), "Ignition doesn't manage the host firewall. If these shouldn't be public, bind them to 127.0.0.1 or block them with your firewall.")
	default:
		r.pass(fmt.Sprintf("%d published port(s), no conflicts.", len(keys)))
	}
}

func (d *Diagnoser) consistency(ctx context.Context, r *result) {
	if d.Store == nil {
		r.skip("Skipped.")
		return
	}
	list, err := d.Store.ListServerResources(ctx, d.Server.ID)
	if err != nil {
		r.fail("Couldn't load resources: "+err.Error(), "")
		return
	}
	if len(list) == 0 {
		r.skip("Skipped: no Ignition workloads are assigned to this server.")
		return
	}
	dk, _, ok := d.engine(ctx, r)
	if !ok {
		return
	}
	var drift []string
	for _, res := range list {
		if res.Status == "deploying" || res.Status == "removing" {
			continue
		}
		observed := "missing"
		if res.ContainerID != "" {
			st, err := dk.Inspect(ctx, res.ContainerID)
			if err != nil {
				observed = "unreadable"
			} else if st.Exists {
				observed = st.Status
			}
		} else {
			observed = "not deployed"
		}
		recorded := res.Status
		match := recorded == observed || (recorded == "stopped" && (observed == "exited" || observed == "created")) ||
			(recorded == "failed" && (observed == "exited" || observed == "dead" || observed == "missing")) ||
			(recorded == "created" && observed == "not deployed")
		r.printf("%-24s recorded %-9s desired %-8s docker %s", res.Name, recorded, res.DesiredState, observed)
		if !match {
			drift = append(drift, fmt.Sprintf("%s (recorded %s, Docker says %s)", res.Name, recorded, observed))
		}
		if res.DesiredState == "running" && observed != "running" && observed != "not deployed" && recorded != "failed" {
			drift = append(drift, fmt.Sprintf("%s should be running but is %s", res.Name, observed))
		}
	}
	if len(drift) > 0 {
		r.warn(fmt.Sprintf("%d mismatch(es): %s.", len(drift), strings.Join(drift, "; ")), "Run a runtime reconciliation from Maintenance or Tasks to refresh recorded status, then start or redeploy affected resources.")
		return
	}
	r.pass(fmt.Sprintf("All %d workload(s) match what Docker reports.", len(list)))
}

func bytesText(b int64) string {
	switch {
	case b >= 1<<40:
		return fmt.Sprintf("%.1f TB", float64(b)/(1<<40))
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(b)/(1<<20))
	}
	return fmt.Sprintf("%d KB", b/1024)
}

// BytesText formats a byte count for messages.
func BytesText(b int64) string { return bytesText(b) }
