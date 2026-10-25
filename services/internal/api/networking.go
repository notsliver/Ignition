package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Networking: the networks a resource joins, its DNS aliases, host port mappings, and diagnostics.
 * Networks are Ignition's, not raw Docker ones: each environment has a private network, and shared networks
 * connect resources across projects on purpose. Joining or leaving applies live; ports and aliases apply on
 * the next deployment. "live" is always what Docker reports now.
 */

const legacyNetwork = "ignition" // the old all-in-one network; the worker moves containers off it

var (
	aliasRE   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
	networkRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
)

type liveNetworking struct {
	Running  bool                  `json:"running"`
	Ports    []runtime.PortMapping `json:"ports"`
	Networks []runtime.Attachment  `json:"networks"`
}

// networkNames are the Docker networks a resource belongs on: its environment's, then its shared ones.
func networkNames(res *store.Resource, cfg store.Networking) []string {
	out := []string{runtime.EnvNetwork(res.EnvironmentID)}
	for _, n := range cfg.Networks {
		out = append(out, runtime.SharedNetwork(n))
	}
	return out
}

// networkLabel is how a Docker network is shown to people.
func networkLabel(res *store.Resource, docker string) string {
	switch {
	case docker == runtime.EnvNetwork(res.EnvironmentID):
		return "the private network"
	case strings.HasPrefix(docker, "ign-net-"):
		return strings.TrimPrefix(docker, "ign-net-")
	}
	return docker
}

func (s *Server) getNetworking(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	cfg, err := s.Store.GetNetworking(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	live := liveNetworking{Ports: []runtime.PortMapping{}, Networks: []runtime.Attachment{}}
	var dockerErr error
	if res.ContainerID != "" {
		st, err := s.Runtime.Inspect(ctx, res.ContainerID)
		dockerErr = err
		if err == nil && st.Exists {
			live.Running = st.Running
			if st.Ports != nil {
				live.Ports = st.Ports
			}
			if st.Networks != nil {
				live.Networks = st.Networks
			}
		}
	}
	liveBy := map[string]runtime.Attachment{}
	for _, a := range live.Networks {
		liveBy[a.Network] = a
	}

	var projectName string
	s.Store.DB.QueryRow(r.Context(), `SELECT name FROM projects WHERE id = $1`, res.ProjectID).Scan(&projectName)
	sharedInfo, err := s.Store.SharedNetworks(r.Context())
	if err != nil {
		return err
	}
	members := map[string][]string{}
	for _, n := range sharedInfo {
		members[n.Name] = n.Members
	}

	type netView struct {
		Kind     string   `json:"kind"` // private, shared or legacy
		Name     string   `json:"name"`
		Docker   string   `json:"docker"`
		Attached bool     `json:"attached"`
		IP       string   `json:"ip"`
		Aliases  []string `json:"aliases"`
		Members  []string `json:"members,omitempty"`
	}
	view := func(kind, name, docker string) netView {
		a, ok := liveBy[docker]
		v := netView{Kind: kind, Name: name, Docker: docker, Attached: ok, IP: a.IP, Aliases: a.Aliases}
		if v.Aliases == nil {
			v.Aliases = []string{}
		}
		return v
	}
	networks := []netView{view("private", projectName+" · "+res.Environment, runtime.EnvNetwork(res.EnvironmentID))}
	for _, n := range cfg.Networks {
		v := view("shared", n, runtime.SharedNetwork(n))
		v.Members = members[n]
		networks = append(networks, v)
	}
	if _, ok := liveBy[legacyNetwork]; ok {
		networks = append(networks, view("legacy", "Old shared network", legacyNetwork))
	}
	available := []store.SharedNetworkInfo{}
	for _, n := range sharedInfo {
		if !slices.Contains(cfg.Networks, n.Name) {
			available = append(available, n)
		}
	}

	peers, err := s.Store.Peers(r.Context(), id)
	if err != nil {
		return err
	}
	domains, _ := s.Store.ListDomains(r.Context(), id)
	tests, _ := s.Store.NetworkTests(r.Context(), id, 8)
	out := map[string]any{
		"config": cfg, "hostnames": append([]string{res.Name}, cfg.Aliases...), "networks": networks, "availableShared": available,
		"live": live, "peers": peers, "domains": domains, "tests": tests, "drift": drift(res, cfg, live),
		"proxy": map[string]any{"httpPort": s.Proxy.HTTPPort, "httpsPort": s.Proxy.HTTPSPort},
	}
	if dockerErr != nil {
		out["dockerError"] = dockerErr.Error()
	}
	httpx.JSON(w, 200, out)
	return nil
}

// drift explains how the running container differs from the saved configuration.
func drift(res *store.Resource, cfg store.Networking, live liveNetworking) []string {
	if res.ContainerID == "" || len(live.Networks) == 0 {
		return []string{}
	}
	var out []string
	liveNets := map[string]runtime.Attachment{}
	for _, a := range live.Networks {
		liveNets[a.Network] = a
	}
	wantNets := networkNames(res, cfg)
	for _, n := range wantNets {
		a, ok := liveNets[n]
		if !ok {
			out = append(out, "Not on "+networkLabel(res, n)+" yet")
			continue
		}
		for _, h := range append([]string{res.Name}, cfg.Aliases...) {
			if !slices.Contains(a.Aliases, h) {
				out = append(out, fmt.Sprintf("%s doesn't answer to %s on %s yet", res.Name, h, networkLabel(res, n)))
			}
		}
	}
	for name := range liveNets {
		if name != legacyNetwork && !slices.Contains(wantNets, name) {
			out = append(out, "Still on "+networkLabel(res, name))
		}
	}
	key := func(ip string, host, container int, proto string) string {
		return fmt.Sprintf("%s|%d|%d|%s", ip, host, container, proto)
	}
	want, have := map[string]bool{}, map[string]bool{}
	for _, p := range cfg.Ports {
		host := 0
		if p.HostPort != nil {
			host = *p.HostPort
		}
		want[key(p.BindAddress, host, p.ContainerPort, p.Protocol)] = true
	}
	for _, p := range live.Ports {
		if p.HostIP == "::" {
			continue // Docker mirrors IPv4 bindings on IPv6
		}
		have[key(p.HostIP, p.HostPort, p.ContainerPort, p.Protocol)] = true
	}
	for k := range want {
		parts := strings.Split(k, "|")
		if parts[1] == "0" { // random host port: match on everything else
			found := false
			for h := range have {
				hp := strings.Split(h, "|")
				if hp[0] == parts[0] && hp[2] == parts[2] && hp[3] == parts[3] {
					found = true
				}
			}
			if !found {
				out = append(out, fmt.Sprintf("Port %s/%s isn't published yet", parts[2], parts[3]))
			}
		} else if !have[k] {
			out = append(out, fmt.Sprintf("Host port %s → %s/%s isn't published yet", parts[1], parts[2], parts[3]))
		}
	}
	for h := range have {
		hp := strings.Split(h, "|")
		match := want[h]
		for k := range want {
			kp := strings.Split(k, "|")
			if kp[1] == "0" && kp[0] == hp[0] && kp[2] == hp[2] && kp[3] == hp[3] {
				match = true
			}
		}
		if !match {
			out = append(out, fmt.Sprintf("Host port %s → %s/%s is still published", hp[1], hp[2], hp[3]))
		}
	}
	slices.Sort(out)
	if out = slices.Compact(out); out == nil {
		return []string{}
	}
	return out
}

type portInput struct {
	HostPort      *int   `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
	BindAddress   string `json:"bindAddress"`
}

// hostPortConflict explains why hostPort can't be used by this resource, or returns "".
func (s *Server) hostPortConflict(ctx context.Context, res *store.Resource, hostPort int, protocol string, bindings []runtime.HostBinding) string {
	reserved := map[int]string{s.Proxy.HTTPPort: "Ignition's domain proxy (HTTP)", s.Proxy.HTTPSPort: "Ignition's domain proxy (HTTPS)"}
	if u, err := url.Parse(s.PublicURL); err == nil {
		if p, err := strconv.Atoi(u.Port()); err == nil {
			reserved[p] = "the Ignition dashboard"
		}
	}
	if what, ok := reserved[hostPort]; ok && protocol == "tcp" {
		return fmt.Sprintf("Port %d is used by %s.", hostPort, what)
	}
	if name, err := s.Store.TakenHostPort(ctx, res.ID, hostPort, protocol); err == nil && name != "" {
		return fmt.Sprintf("Port %d/%s is already mapped by %s.", hostPort, protocol, name)
	}
	for _, b := range bindings {
		if b.HostPort == hostPort && b.Protocol == protocol && b.Resource != res.ID {
			return fmt.Sprintf("Port %d/%s is in use by the container %s.", hostPort, protocol, b.Container)
		}
	}
	return ""
}

func (s *Server) putNetworking(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	var in struct {
		Ports    []portInput `json:"ports"`
		Networks []string    `json:"networks"`
		Aliases  []string    `json:"aliases"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	f := map[string]string{}
	cfg := store.Networking{Ports: []store.PortMapping{}, Networks: []string{}, Aliases: []string{}}

	// Aliases: valid DNS names, unique, and not already answered by another resource on a shared network.
	peers, _ := s.Store.Peers(r.Context(), id)
	if len(in.Aliases) > 10 {
		f["aliases"] = "Up to 10 aliases."
	}
	for _, a := range in.Aliases {
		a = strings.ToLower(strings.TrimSpace(a))
		switch {
		case a == "" || a == res.Name || slices.Contains(cfg.Aliases, a):
			continue
		case len(a) > 253 || !aliasRE.MatchString(a):
			f["aliases"] = fmt.Sprintf("%q isn't a valid hostname. Use lowercase letters, numbers, dashes and dots.", a)
		default:
			for _, p := range peers {
				if slices.Contains(p.Hostnames, a) {
					f["aliases"] = fmt.Sprintf("%s already answers to %q.", p.Name, a)
				}
			}
		}
		cfg.Aliases = append(cfg.Aliases, a)
	}

	// Shared networks: Ignition names; new ones are created on save.
	for _, name := range in.Networks {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || slices.Contains(cfg.Networks, name) {
			continue
		}
		if !networkRE.MatchString(name) {
			f["networks"] = fmt.Sprintf("%q isn't a valid network name. Use lowercase letters, numbers and dashes (up to 32).", name)
		}
		cfg.Networks = append(cfg.Networks, name)
	}
	if len(cfg.Networks) > 5 {
		f["networks"] = "Up to 5 shared networks."
	}

	// Ports: valid, not duplicated, and not taken by anything else on this machine.
	bindings, _ := s.Runtime.HostBindings(ctx)
	if len(in.Ports) > 20 {
		f["ports"] = "Up to 20 port mappings."
	}
	seen := map[string]bool{}
	for i, p := range in.Ports {
		key := func(field string) string { return fmt.Sprintf("ports.%d.%s", i, field) }
		if p.Protocol == "" {
			p.Protocol = "tcp"
		}
		if p.BindAddress == "" {
			p.BindAddress = "127.0.0.1"
		}
		if p.ContainerPort < 1 || p.ContainerPort > 65535 {
			f[key("containerPort")] = "Container ports are 1 to 65535."
		}
		if p.Protocol != "tcp" && p.Protocol != "udp" {
			f[key("protocol")] = "Use TCP or UDP."
		}
		if p.BindAddress != "127.0.0.1" && p.BindAddress != "0.0.0.0" {
			f[key("bindAddress")] = "Choose this machine only or all interfaces."
		}
		if p.HostPort != nil {
			hp := *p.HostPort
			switch {
			case hp < 1 || hp > 65535:
				f[key("hostPort")] = "Host ports are 1 to 65535."
			case seen[fmt.Sprintf("%d/%s", hp, p.Protocol)]:
				f[key("hostPort")] = fmt.Sprintf("Port %d/%s is mapped twice.", hp, p.Protocol)
			default:
				if msg := s.hostPortConflict(r.Context(), res, hp, p.Protocol, bindings); msg != "" {
					f[key("hostPort")] = msg
				}
			}
			seen[fmt.Sprintf("%d/%s", hp, p.Protocol)] = true
		}
		cfg.Ports = append(cfg.Ports, store.PortMapping{HostPort: p.HostPort, ContainerPort: p.ContainerPort, Protocol: p.Protocol, BindAddress: p.BindAddress})
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	before, _ := s.Store.GetNetworking(r.Context(), id)
	if err := s.Store.SetNetworking(r.Context(), id, u.ID, cfg); store.IsUnique(err) {
		return httpx.Invalid(map[string]string{"ports": "Another resource just took one of these host ports. Pick another."})
	} else if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "resource.networking", "resource", id, map[string]any{"ports": len(cfg.Ports), "networks": cfg.Networks, "aliases": cfg.Aliases}, clientIP(r))
	if saved, err := s.Store.GetNetworking(r.Context(), id); err == nil {
		cfg = saved // with the new mapping IDs
	}
	// Joining or leaving networks applies now, without a restart. Ports and aliases need the container recreated.
	applyErr := s.applyNetworks(r.Context(), res, cfg)
	redeploy := res.ContainerID != "" && (!slices.Equal(before.Aliases, cfg.Aliases) || !samePorts(before.Ports, cfg.Ports))
	out := map[string]any{"config": cfg, "requiresRedeploy": redeploy}
	if applyErr != nil {
		out["applyError"] = "Saved, but joining the networks failed: " + applyErr.Error() + ". It's retried automatically."
	}
	httpx.JSON(w, 200, out)
	return nil
}

func (s *Server) checkHostPort(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	var in struct {
		HostPort int    `json:"hostPort"`
		Protocol string `json:"protocol"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Protocol == "" {
		in.Protocol = "tcp"
	}
	if in.HostPort < 1 || in.HostPort > 65535 {
		return httpx.Invalid(map[string]string{"hostPort": "Host ports are 1 to 65535."})
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	bindings, _ := s.Runtime.HostBindings(ctx)
	msg := s.hostPortConflict(r.Context(), res, in.HostPort, in.Protocol, bindings)
	httpx.JSON(w, 200, map[string]any{"available": msg == "", "message": msg})
	return nil
}

/* ------------------------------- Diagnostics ------------------------------ */

var (
	probeMu      sync.Mutex
	probeRunning = map[string]bool{}        // one test at a time per resource
	probeRecent  = map[string][]time.Time{} // per user, for a simple rate limit
)

func claimProbe(resourceID, userID string) error {
	probeMu.Lock()
	defer probeMu.Unlock()
	if probeRunning[resourceID] {
		return httpx.Conflict("A test is already running for this resource.")
	}
	cut := time.Now().Add(-time.Minute)
	recent := slices.DeleteFunc(probeRecent[userID], func(t time.Time) bool { return t.Before(cut) })
	if len(recent) >= 20 {
		return httpx.TooMany("Too many tests in the last minute. Wait a moment.")
	}
	probeRecent[userID] = append(recent, time.Now())
	probeRunning[resourceID] = true
	return nil
}

func releaseProbe(resourceID string) {
	probeMu.Lock()
	delete(probeRunning, resourceID)
	probeMu.Unlock()
}

var customHostRE = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$`)

func (s *Server) runDiagnostic(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	var in struct {
		Kind     string `json:"kind"`     // dns or tcp
		Target   string `json:"target"`   // "self", a resource id on a shared network, or "custom"
		Hostname string `json:"hostname"` // custom target (admins only)
		Port     int    `json:"port"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Kind != "dns" && in.Kind != "tcp" {
		return httpx.Invalid(map[string]string{"kind": "Choose a DNS or TCP test."})
	}
	cfg, err := s.Store.GetNetworking(r.Context(), id)
	if err != nil {
		return err
	}
	joined := networkNames(res, cfg)

	// Only names Ignition configures are allowed, so this can't be used to scan arbitrary hosts.
	var hosts []string
	port := in.Port
	switch in.Target {
	case "self":
		hosts = append([]string{res.Name}, cfg.Aliases...)
		if port == 0 && res.Port != nil {
			port = *res.Port
		}
	case "custom":
		if u.Role != "admin" && u.Role != "owner" {
			return httpx.Forbidden("Only admins can test custom hosts.")
		}
		h := strings.TrimSpace(in.Hostname)
		ip := net.ParseIP(h)
		if !customHostRE.MatchString(h) || (ip != nil && (ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast())) {
			return httpx.Invalid(map[string]string{"hostname": "Enter a hostname or IP address."})
		}
		hosts = []string{h}
	default:
		peers, err := s.Store.Peers(r.Context(), id)
		if err != nil {
			return err
		}
		idx := slices.IndexFunc(peers, func(p store.Peer) bool { return p.ID == in.Target }) // peers are already limited to reachable ones
		if idx < 0 {
			return httpx.Invalid(map[string]string{"target": "Pick a resource on one of this resource's networks."})
		}
		hosts = peers[idx].Hostnames[:1]
		if port == 0 && peers[idx].Port != nil {
			port = *peers[idx].Port
		}
	}
	if in.Kind == "tcp" && (port < 1 || port > 65535) {
		return httpx.Invalid(map[string]string{"port": "Enter a port from 1 to 65535."})
	}
	if err := claimProbe(id, u.ID); err != nil {
		return err
	}
	defer releaseProbe(id)

	// Every host goes in as its own argument; nothing is interpolated into a shell.
	var cmd []string
	if in.Kind == "dns" {
		cmd = append([]string{"sh", "-c", `for h in "$@"; do echo "== $h"; nslookup "$h" 2>&1 || echo "!! $h"; done`, "probe"}, hosts...)
	} else {
		cmd = []string{"sh", "-c", `echo "== $1:$2"; if nc -z -w 3 "$1" "$2"; then echo "open"; else echo "closed"; exit 1; fi`, "probe", hosts[0], strconv.Itoa(port)}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	code, output, err := s.Runtime.RunProbe(ctx, runtime.Probe{Networks: joined, Cmd: cmd, Timeout: 20 * time.Second})
	t := store.NetworkTest{Kind: in.Kind, Target: strings.Join(hosts, ", ")}
	if in.Kind == "tcp" {
		t.Target = fmt.Sprintf("%s:%d", hosts[0], port)
	}
	switch {
	case err != nil:
		t.Summary, t.Output = "The test couldn't run: "+err.Error(), ""
	case in.Kind == "dns":
		t.OK, t.Summary = summarizeDNS(output, hosts)
		t.Output = output
	default:
		t.OK = code == 0
		t.Output = output
		if t.OK {
			t.Summary = fmt.Sprintf("%s accepts connections.", t.Target)
		} else {
			t.Summary = fmt.Sprintf("Nothing accepted a connection at %s within 3 seconds.", t.Target)
		}
	}
	t.ID, _ = s.Store.AddNetworkTest(r.Context(), id, u.ID, t)
	t.RanAt, t.RanBy = time.Now(), u.Name
	s.Store.Audit(r.Context(), u, "resource.diagnostic", "resource", id, map[string]any{"kind": in.Kind, "target": t.Target, "ok": t.OK}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"test": t, "ranFrom": "A temporary probe container on this resource's networks"})
	return nil
}

// summarizeDNS reads busybox nslookup output: each "== host" section either has an answer address or fails.
func summarizeDNS(out string, hosts []string) (bool, string) {
	resolved := map[string][]string{}
	var cur string
	inAnswer := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "== "):
			cur, inAnswer = strings.TrimPrefix(line, "== "), false
		case strings.HasPrefix(line, "Name:"):
			inAnswer = true
		case inAnswer && strings.HasPrefix(line, "Address"):
			if _, addr, ok := strings.Cut(line, ":"); ok {
				if a := strings.TrimSpace(addr); net.ParseIP(a) != nil {
					resolved[cur] = append(resolved[cur], a)
				}
			}
		}
	}
	var parts []string
	ok := true
	for _, h := range hosts {
		if addrs := resolved[h]; len(addrs) > 0 {
			parts = append(parts, fmt.Sprintf("%s → %s", h, strings.Join(addrs, ", ")))
		} else {
			ok = false
			parts = append(parts, h+" doesn't resolve")
		}
	}
	return ok, strings.Join(parts, " · ")
}

func samePorts(a, b []store.PortMapping) bool {
	key := func(p store.PortMapping) string {
		h := 0
		if p.HostPort != nil {
			h = *p.HostPort
		}
		return fmt.Sprintf("%d|%d|%s|%s", h, p.ContainerPort, p.Protocol, p.BindAddress)
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if key(a[i]) != key(b[i]) {
			return false
		}
	}
	return true
}

// applyNetworks connects or disconnects the running container to match its shared networks, live.
// The worker's reconciler does the same every 15 seconds, so a failure here heals on its own.
func (s *Server) applyNetworks(ctx context.Context, res *store.Resource, cfg store.Networking) error {
	if res.ContainerID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := s.Runtime.Inspect(ctx, res.ContainerID)
	if err != nil || !st.Exists {
		return err
	}
	aliases := append([]string{res.Name}, cfg.Aliases...)
	want := networkNames(res, cfg)
	attached := map[string]bool{}
	for _, a := range st.Networks {
		attached[a.Network] = true
	}
	for _, n := range cfg.Networks {
		if err := s.Runtime.EnsureManagedNetwork(ctx, runtime.SharedNetwork(n), map[string]string{"ignition.network": "shared", "ignition.shared": n}); err != nil {
			return err
		}
	}
	for _, n := range want {
		if !attached[n] {
			if err := s.Runtime.ConnectNetwork(ctx, n, res.ContainerID, aliases); err != nil {
				return err
			}
		}
	}
	for n := range attached {
		if strings.HasPrefix(n, "ign-net-") && !slices.Contains(want, n) {
			if err := s.Runtime.DisconnectNetwork(ctx, n, res.ContainerID); err != nil {
				return err
			}
		}
	}
	return nil
}
