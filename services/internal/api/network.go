package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/notsliver/Ignition/services/internal/dns"
	"github.com/notsliver/Ignition/services/internal/firewall"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/proxy"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Networking: the addresses this machine has, the host ports everything uses (with an
 * allocation range and reservations), internal networks with their subnets and IPv6, the domain
 * proxy (settings, certificates, routes, logs), DNS provider integrations, host firewall rules
 * and network diagnostics. Everything shown is read live from Docker, the proxy or the host;
 * nothing is assumed.
 */

// networkSettings are instance-wide: the public addresses DNS records point at (detected when
// empty) and the range host ports are allocated from.
type networkSettings struct {
	PublicIPv4     string `json:"publicIpv4"`
	PublicIPv6     string `json:"publicIpv6"`
	PortRangeStart int    `json:"portRangeStart"` // 0 = let Docker pick ports
	PortRangeEnd   int    `json:"portRangeEnd"`
}

func (s *Server) netSettings(ctx context.Context) networkSettings {
	var n networkSettings
	s.Store.GetSetting(ctx, "network", &n)
	return n
}

/* --------------------------------- Addresses --------------------------------- */

var publicIPCache struct {
	sync.Mutex
	v4, v6 string
	at     time.Time
}

// detectPublicIP asks a well-known echo service which address our traffic leaves from.
func detectPublicIP(ctx context.Context, endpoint string) string {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64))
	ip := net.ParseIP(strings.TrimSpace(string(b)))
	if ip == nil {
		return ""
	}
	return ip.String()
}

// publicAddresses are the saved addresses, or detected ones (cached for 10 minutes).
func (s *Server) publicAddresses(ctx context.Context) (v4, v6, source string) {
	set := s.netSettings(ctx)
	if set.PublicIPv4 != "" || set.PublicIPv6 != "" {
		return set.PublicIPv4, set.PublicIPv6, "settings"
	}
	publicIPCache.Lock()
	defer publicIPCache.Unlock()
	if time.Since(publicIPCache.at) > 10*time.Minute {
		publicIPCache.v4 = detectPublicIP(ctx, "https://api4.ipify.org")
		publicIPCache.v6 = detectPublicIP(ctx, "https://api6.ipify.org")
		publicIPCache.at = time.Now()
	}
	return publicIPCache.v4, publicIPCache.v6, "detected"
}

type hostAddress struct {
	Interface string `json:"interface"`
	Family    string `json:"family"` // inet | inet6
	Address   string `json:"address"`
	Prefix    int    `json:"prefix"`
	Scope     string `json:"scope"` // public, private, loopback, link-local, docker
}

func classify(iface string, ip net.IP) string {
	switch {
	case ip.IsLoopback():
		return "loopback"
	case strings.HasPrefix(iface, "docker") || strings.HasPrefix(iface, "br-") || strings.HasPrefix(iface, "veth"):
		return "docker"
	case ip.IsLinkLocalUnicast():
		return "link-local"
	case ip.IsPrivate() || (ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xc0 == 64):
		return "private"
	}
	return "public"
}

var ipAddrRE = regexp.MustCompile(`^\d+:\s+(\S+)\s+(inet6?)\s+([0-9a-fA-F:.]+)/(\d+)`)

// parseIPAddr reads `ip -o addr` output.
func parseIPAddr(out string) []hostAddress {
	list := []hostAddress{}
	for _, l := range strings.Split(out, "\n") {
		m := ipAddrRE.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		ip := net.ParseIP(m[3])
		if ip == nil {
			continue
		}
		prefix, _ := strconv.Atoi(m[4])
		iface := strings.TrimSuffix(m[1], ":")
		if at := strings.Index(iface, "@"); at > 0 {
			iface = iface[:at]
		}
		list = append(list, hostAddress{iface, m[2], ip.String(), prefix, classify(iface, ip)})
	}
	return list
}

func (s *Server) networkAddresses(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	v4, v6, source := s.publicAddresses(r.Context())
	out := map[string]any{"public": map[string]string{"ipv4": v4, "ipv6": v6, "source": source}, "interfaces": []hostAddress{}, "settings": s.netSettings(r.Context())}
	// Host interfaces come from the host probe (it runs on the host's network namespace).
	if srv, err := s.Store.LocalServer(r.Context()); err == nil {
		if dk, err := s.Servers.Docker(srv); err == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			if text, _, err := dk.ExecCapture(ctx, runtime.HostProbeName(dk.Instance(ctx)), []string{"ip", "-o", "addr", "show"}); err == nil {
				out["interfaces"] = parseIPAddr(text)
			} else {
				out["interfacesReason"] = "The host probe isn't running on this server, so its interfaces can't be read. Turn it on under Servers › Settings › Host probe."
			}
		}
	}
	httpx.JSON(w, 200, out)
	return nil
}

func (s *Server) putNetworkSettings(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in networkSettings
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	if in.PublicIPv4 = strings.TrimSpace(in.PublicIPv4); in.PublicIPv4 != "" {
		if ip := net.ParseIP(in.PublicIPv4); ip == nil || ip.To4() == nil {
			f["publicIpv4"] = "Enter an IPv4 address like 203.0.113.7."
		}
	}
	if in.PublicIPv6 = strings.TrimSpace(in.PublicIPv6); in.PublicIPv6 != "" {
		if ip := net.ParseIP(in.PublicIPv6); ip == nil || ip.To4() != nil {
			f["publicIpv6"] = "Enter an IPv6 address like 2001:db8::7."
		}
	}
	switch {
	case in.PortRangeStart == 0 && in.PortRangeEnd == 0:
	case in.PortRangeStart < 1024 || in.PortRangeEnd > 65535 || in.PortRangeStart >= in.PortRangeEnd:
		f["portRangeStart"] = "A range between 1024 and 65535, start below end."
	case in.PortRangeEnd-in.PortRangeStart > 20000:
		f["portRangeStart"] = "Up to 20,000 ports."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	if err := s.Store.SetSetting(r.Context(), "network", in); err != nil {
		return err
	}
	publicIPCache.Lock()
	publicIPCache.at = time.Time{}
	publicIPCache.Unlock()
	s.Store.Audit(r.Context(), u, "network.settings", "settings", "network", map[string]any{"publicIpv4": in.PublicIPv4, "publicIpv6": in.PublicIPv6,
		"portRange": fmt.Sprintf("%d-%d", in.PortRangeStart, in.PortRangeEnd)}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"settings": in})
	return nil
}

/* ----------------------------------- Ports ----------------------------------- */

type systemPort struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Use      string `json:"use"`
}

func (s *Server) systemPorts() []systemPort {
	out := []systemPort{{s.Proxy.HTTPPort, "tcp", "Domain proxy (HTTP)"}, {s.Proxy.HTTPSPort, "tcp", "Domain proxy (HTTPS)"}}
	if u, err := url.Parse(s.PublicURL); err == nil {
		if p, err := strconv.Atoi(u.Port()); err == nil {
			out = append(out, systemPort{p, "tcp", "Ignition dashboard"})
		}
	}
	return out
}

// nextFreePort is the first port in the range nobody uses, maps, reserves or needs.
func (s *Server) nextFreePort(ctx context.Context, protocol string, bindings []runtime.HostBinding) (int, error) {
	set := s.netSettings(ctx)
	if set.PortRangeStart == 0 {
		return 0, errors.New("no allocation range is set")
	}
	taken := map[int]bool{}
	for _, b := range bindings {
		if b.Protocol == protocol {
			taken[b.HostPort] = true
		}
	}
	for _, p := range s.systemPorts() {
		if p.Protocol == protocol {
			taken[p.Port] = true
		}
	}
	if assigned, err := s.Store.PortAssignments(ctx); err == nil {
		for _, a := range assigned {
			if a.HostPort != nil && a.Protocol == protocol {
				taken[*a.HostPort] = true
			}
		}
	}
	if res, err := s.Store.PortReservations(ctx); err == nil {
		for _, rv := range res {
			if rv.Protocol == protocol {
				taken[rv.Port] = true
			}
		}
	}
	for p := set.PortRangeStart; p <= set.PortRangeEnd; p++ {
		if !taken[p] {
			return p, nil
		}
	}
	return 0, fmt.Errorf("every port from %d to %d is taken", set.PortRangeStart, set.PortRangeEnd)
}

func (s *Server) networkPorts(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	assigned, err := s.Store.PortAssignments(ctx)
	if err != nil {
		return err
	}
	reservations, err := s.Store.PortReservations(ctx)
	if err != nil {
		return err
	}
	bindings, berr := s.Runtime.HostBindings(ctx)
	type live struct {
		HostIP    string `json:"hostIp"`
		HostPort  int    `json:"hostPort"`
		Protocol  string `json:"protocol"`
		Container string `json:"container"`
		Resource  string `json:"resourceId"`
	}
	lives := []live{}
	for _, b := range bindings {
		lives = append(lives, live{b.HostIP, b.HostPort, b.Protocol, b.Container, b.Resource})
	}
	sort.Slice(lives, func(i, j int) bool { return lives[i].HostPort < lives[j].HostPort })
	// Ports Docker picked (no fixed host port) are filled in from the live bindings.
	type assignmentView struct {
		store.PortAssignment
		LivePort *int `json:"livePort"`
	}
	views := []assignmentView{}
	for _, a := range assigned {
		v := assignmentView{PortAssignment: a}
		if a.HostPort == nil {
			for _, b := range bindings {
				if b.Resource == a.ResourceID && b.Protocol == a.Protocol {
					p := b.HostPort
					v.LivePort = &p
					break
				}
			}
		}
		views = append(views, v)
	}
	out := map[string]any{"assignments": views, "live": lives, "reservations": reservations, "system": s.systemPorts(), "settings": s.netSettings(ctx)}
	if berr != nil {
		out["liveReason"] = "Docker didn't list published ports: " + berr.Error()
	}
	if p, err := s.nextFreePort(ctx, "tcp", bindings); err == nil {
		out["nextFree"] = p
	} else {
		out["nextFreeReason"] = err.Error()
	}
	httpx.JSON(w, 200, out)
	return nil
}

func (s *Server) reservePort(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in struct {
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
		Note     string `json:"note"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Note = strings.TrimSpace(in.Note)
	f := map[string]string{}
	if in.Port < 1 || in.Port > 65535 {
		f["port"] = "1 to 65535."
	}
	if in.Protocol != "tcp" && in.Protocol != "udp" {
		f["protocol"] = "TCP or UDP."
	}
	if len(in.Note) > 200 {
		f["note"] = "Up to 200 characters."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	id, err := s.Store.ReservePort(r.Context(), in.Port, in.Protocol, in.Note, u.ID)
	if store.IsUnique(err) {
		return httpx.Invalid(map[string]string{"port": fmt.Sprintf("%d/%s is already reserved.", in.Port, in.Protocol)})
	}
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "network.port_reserved", "port", id, map[string]any{"port": in.Port, "protocol": in.Protocol, "note": in.Note}, clientIP(r))
	httpx.JSON(w, 201, map[string]any{"id": id})
	return nil
}

func (s *Server) deleteReservation(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Reservation")
	if err != nil {
		return err
	}
	port, proto, err := s.Store.DeleteReservation(r.Context(), id)
	if err != nil {
		return notFound(err, "Reservation")
	}
	s.Store.Audit(r.Context(), u, "network.port_released", "port", id, map[string]any{"port": port, "protocol": proto}, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}

/* ------------------------------ Internal networks ------------------------------ */

var netNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

type liveNetwork struct {
	Exists    bool                      `json:"exists"`
	Subnets   []string                  `json:"subnets"`
	Gateways  []string                  `json:"gateways"`
	IPv6      bool                      `json:"ipv6"`
	Internal  bool                      `json:"internal"`
	Endpoints []runtime.NetworkEndpoint `json:"endpoints"`
}

func (s *Server) liveNetwork(ctx context.Context, dk *runtime.Docker, all []runtime.NetworkDetail, name string) liveNetwork {
	l := liveNetwork{Subnets: []string{}, Gateways: []string{}, Endpoints: []runtime.NetworkEndpoint{}}
	for _, n := range all {
		if n.Name == name {
			l.Exists, l.Subnets, l.Gateways, l.IPv6, l.Internal = true, n.Subnets, n.Gateways, n.IPv6, n.Internal
		}
	}
	if l.Exists {
		if eps, err := dk.NetworkEndpoints(ctx, name); err == nil {
			sort.Slice(eps, func(i, j int) bool { return eps[i].Container < eps[j].Container })
			l.Endpoints = eps
		}
	}
	return l
}

func (s *Server) listNetworks(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	shared, err := s.Store.ListSharedNetworks(ctx)
	if err != nil {
		return err
	}
	srv, err := s.Store.LocalServer(ctx)
	if err != nil {
		return err
	}
	dk, err := s.Servers.Docker(srv)
	if err != nil {
		return httpx.Unavailable(err.Error())
	}
	all, _ := dk.ListNetworks(ctx)
	type sharedView struct {
		store.SharedNetwork
		Docker string      `json:"docker"`
		Live   liveNetwork `json:"live"`
	}
	sv := []sharedView{}
	for _, n := range shared {
		sv = append(sv, sharedView{n, runtime.SharedNetwork(n.Name), s.liveNetwork(ctx, dk, all, runtime.SharedNetwork(n.Name))})
	}
	// Each environment has a private network; show them with their subnets too.
	type envView struct {
		EnvironmentID string      `json:"environmentId"`
		Name          string      `json:"name"`
		Docker        string      `json:"docker"`
		Live          liveNetwork `json:"live"`
	}
	ev := []envView{}
	rows, err := s.Store.DB.Query(ctx, `SELECT e.id, p.name || ' · ' || e.name FROM environments e JOIN projects p ON p.id = e.project_id
		WHERE EXISTS (SELECT 1 FROM resources r WHERE r.environment_id = e.id) ORDER BY p.name, e.name`)
	if err == nil {
		for rows.Next() {
			var id, name string
			if rows.Scan(&id, &name) == nil {
				ev = append(ev, envView{id, name, runtime.EnvNetwork(id), liveNetwork{}})
			}
		}
		rows.Close()
	}
	for i := range ev {
		ev[i].Live = s.liveNetwork(ctx, dk, all, ev[i].Docker)
	}
	// Subnets in use anywhere on the host, so new ones can avoid them.
	used := []map[string]string{}
	for _, n := range all {
		for _, sub := range n.Subnets {
			used = append(used, map[string]string{"network": n.Name, "subnet": sub})
		}
	}
	httpx.JSON(w, 200, map[string]any{"shared": sv, "environments": ev, "usedSubnets": used})
	return nil
}

// checkSubnet validates a CIDR of the right family and that it doesn't overlap any network.
func checkSubnet(cidr string, v6 bool, all []runtime.NetworkDetail) string {
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil || (ip.To4() == nil) != v6 {
		if v6 {
			return "Enter an IPv6 range like fd00:1::/64."
		}
		return "Enter an IPv4 range like 10.20.0.0/24."
	}
	ones, bits := n.Mask.Size()
	if (!v6 && (ones < 8 || ones > 29)) || (v6 && (ones < 48 || ones > 120)) || bits == 0 {
		if v6 {
			return "Use a prefix between /48 and /120."
		}
		return "Use a prefix between /8 and /29."
	}
	for _, other := range all {
		for _, sub := range other.Subnets {
			if _, on, err := net.ParseCIDR(sub); err == nil && (on.Contains(n.IP) || n.Contains(on.IP)) {
				return fmt.Sprintf("Overlaps %s (%s).", other.Name, sub)
			}
		}
	}
	return ""
}

func (s *Server) createNetwork(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		store.NetworkSpec
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	in.Description = strings.TrimSpace(in.Description)
	in.Subnet, in.Gateway, in.Subnet6 = strings.TrimSpace(in.Subnet), strings.TrimSpace(in.Gateway), strings.TrimSpace(in.Subnet6)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	srv, err := s.Store.LocalServer(ctx)
	if err != nil {
		return err
	}
	dk, err := s.Servers.Docker(srv)
	if err != nil {
		return httpx.Unavailable(err.Error())
	}
	all, _ := dk.ListNetworks(ctx)
	// A network left behind by an earlier install (or a deleted record) has the same Docker name:
	// adopt it when it matches what's asked for, otherwise say so instead of reporting an overlap.
	dockerName := runtime.SharedNetwork(in.Name)
	var leftover *runtime.NetworkDetail
	others := []runtime.NetworkDetail{}
	for i, n := range all {
		if n.Name == dockerName {
			leftover = &all[i]
		} else {
			others = append(others, n)
		}
	}
	all = others
	f := map[string]string{}
	if leftover != nil {
		want := []string{}
		for _, sub := range []string{in.Subnet, in.Subnet6} {
			if sub != "" {
				want = append(want, sub)
			}
		}
		for _, sub := range want {
			if !slices.Contains(leftover.Subnets, sub) {
				f["name"] = fmt.Sprintf("Docker already has a network %s (%s) left from earlier. Pick another name, or remove it with: docker network rm %s", dockerName, strings.Join(leftover.Subnets, ", "), dockerName)
			}
		}
	}
	if !netNameRE.MatchString(in.Name) {
		f["name"] = "Lowercase letters, digits and dashes, up to 32."
	} else if _, err := s.Store.GetSharedNetwork(ctx, in.Name); err == nil {
		f["name"] = "A network with this name already exists."
	}
	if len(in.Description) > 200 {
		f["description"] = "Up to 200 characters."
	}
	if in.Subnet != "" {
		if msg := checkSubnet(in.Subnet, false, all); msg != "" {
			f["subnet"] = msg
		}
	}
	if in.Gateway != "" {
		_, n, err := net.ParseCIDR(in.Subnet)
		if ip := net.ParseIP(in.Gateway); in.Subnet == "" || err != nil || ip == nil || !n.Contains(ip) {
			f["gateway"] = "The gateway must be an address inside the subnet."
		}
	}
	if in.Subnet6 != "" {
		if !in.IPv6 {
			f["subnet6"] = "Turn on IPv6 to set an IPv6 range."
		} else if msg := checkSubnet(in.Subnet6, true, all); msg != "" {
			f["subnet6"] = msg
		}
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	// Create it in Docker first, so a conflict surfaces here instead of at the next deploy.
	opts := runtime.NetworkOptions{Subnet: in.Subnet, Gateway: in.Gateway, IPv6: in.IPv6, Subnet6: in.Subnet6, Internal: in.Internal}
	if err := dk.EnsureManagedNetworkWith(ctx, runtime.SharedNetwork(in.Name), map[string]string{"ignition.network": "shared", "ignition.shared": in.Name}, opts); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "IPv6") || strings.Contains(msg, "ipv6") {
			msg += " (IPv6 has to be enabled in Docker's daemon settings first.)"
		}
		return httpx.Invalid(map[string]string{"subnet": "Docker refused the network: " + msg})
	}
	if _, err := s.Store.CreateSharedNetwork(ctx, in.Name, in.Description, in.NetworkSpec, u.ID); err != nil {
		return err
	}
	s.Store.Audit(ctx, u, "network.create", "network", in.Name, map[string]any{"subnet": in.Subnet, "ipv6": in.IPv6, "subnet6": in.Subnet6, "internal": in.Internal}, clientIP(r))
	httpx.JSON(w, 201, map[string]any{"name": in.Name})
	return nil
}

func (s *Server) updateNetwork(w http.ResponseWriter, r *http.Request, u *store.User) error {
	name := r.PathValue("name")
	var in struct {
		Description string `json:"description"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if len(in.Description) > 200 {
		return httpx.Invalid(map[string]string{"description": "Up to 200 characters."})
	}
	if _, err := s.Store.GetSharedNetwork(r.Context(), name); err != nil {
		return notFound(err, "Network")
	}
	if err := s.Store.UpdateSharedNetwork(r.Context(), name, strings.TrimSpace(in.Description)); err != nil {
		return err
	}
	httpx.JSON(w, 204, nil)
	return nil
}

func (s *Server) deleteNetwork(w http.ResponseWriter, r *http.Request, u *store.User) error {
	name := r.PathValue("name")
	n, err := s.Store.GetSharedNetwork(r.Context(), name)
	if err != nil {
		return notFound(err, "Network")
	}
	if err := s.Store.DeleteSharedNetwork(r.Context(), n.Name); errors.Is(err, store.ErrNotFound) {
		return httpx.Conflict("Resources still use this network. Remove it from them under Networking first.")
	} else if err != nil {
		return err
	}
	if srv, err := s.Store.LocalServer(r.Context()); err == nil {
		if dk, err := s.Servers.Docker(srv); err == nil {
			dk.RemoveNetwork(r.Context(), runtime.SharedNetwork(n.Name))
		}
	}
	s.Store.Audit(r.Context(), u, "network.delete", "network", n.Name, nil, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}

/* ----------------------------------- Proxy ----------------------------------- */

func (s *Server) proxyStatus(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	routes, err := s.Store.ProxyRoutes(ctx)
	if err != nil {
		return err
	}
	set := proxy.LoadSettings(ctx, s.Store)
	running := s.Proxy.Running(ctx)
	hash, current := "", false
	if running {
		hash, current = s.Proxy.RunningHash(ctx, routes, set)
	}
	type routeView struct {
		Hostname string   `json:"hostname"`
		Path     string   `json:"path"`
		Target   string   `json:"target"`
		Resource string   `json:"resource"`
		HTTPS    bool     `json:"https"`
		Redirect string   `json:"redirect"`
		Code     int      `json:"code"`
		Strip    bool     `json:"strip"`
		AllowIPs []string `json:"allowIps"`
		Live     bool     `json:"live"`
	}
	list := []routeView{}
	for _, rt := range routes {
		v := routeView{Hostname: rt.Hostname, Path: rt.PathPrefix, Resource: rt.Resource, HTTPS: rt.HTTPS, Redirect: rt.RedirectTo, Code: rt.RedirectCode, Strip: rt.StripPrefix,
			AllowIPs: rt.AllowIPs, Live: rt.ContainerID != "" || rt.RedirectTo != ""}
		if v.AllowIPs == nil {
			v.AllowIPs = []string{}
		}
		if rt.RedirectTo == "" {
			v.Target = fmt.Sprintf("%s:%d", rt.Resource, rt.Port)
		}
		list = append(list, v)
	}
	httpx.JSON(w, 200, map[string]any{"running": running, "configHash": hash, "current": current, "routes": list, "settings": set,
		"httpPort": s.Proxy.HTTPPort, "httpsPort": s.Proxy.HTTPSPort})
	return nil
}

func (s *Server) putProxySettings(w http.ResponseWriter, r *http.Request, u *store.User) error {
	in := proxy.DefaultSettings()
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.ACMEEmail = strings.TrimSpace(in.ACMEEmail)
	f := map[string]string{}
	if in.ACMEEmail != "" && (!strings.Contains(in.ACMEEmail, "@") || strings.ContainsAny(in.ACMEEmail, " <>")) {
		f["acmeEmail"] = "Enter an email address."
	}
	if in.ACMECA != "letsencrypt" && in.ACMECA != "letsencrypt-staging" {
		f["acmeCa"] = "Let's Encrypt or Let's Encrypt staging."
	}
	if in.MaxBodyMB < 0 || in.MaxBodyMB > 10240 {
		f["maxBodyMb"] = "0 (no limit) to 10,240 MB."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	if err := s.Store.SetSetting(r.Context(), "proxy", in); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "proxy.settings", "settings", "proxy", map[string]any{"compression": in.Compression, "hsts": in.HSTS, "maxBodyMb": in.MaxBodyMB,
		"acmeCa": in.ACMECA, "acmeEmail": in.ACMEEmail != ""}, clientIP(r))
	s.syncProxy(r.Context())
	httpx.JSON(w, 200, map[string]any{"settings": in})
	return nil
}

// reloadProxy re-sends the config and re-checks every domain.
func (s *Server) reloadProxy(w http.ResponseWriter, r *http.Request, u *store.User) error {
	routes, err := s.Store.ProxyRoutes(r.Context())
	if err != nil {
		return err
	}
	changed, err := s.Proxy.Sync(r.Context(), routes, proxy.LoadSettings(r.Context(), s.Store))
	if err != nil {
		return httpx.Unavailable(err.Error())
	}
	go s.recheckAllDomains(context.WithoutCancel(r.Context()))
	s.Store.Audit(r.Context(), u, "proxy.reload", "settings", "proxy", map[string]any{"changed": changed}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"changed": changed})
	return nil
}

func (s *Server) proxyLogs(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	project := s.Runtime.Instance(ctx)
	id, err := s.Runtime.FindContainer(ctx, map[string]string{"com.docker.compose.project": project, "com.docker.compose.service": "proxy"})
	if err != nil || id == "" {
		return httpx.Unavailable("The proxy container isn't running.")
	}
	lines, err := s.Runtime.Logs(ctx, id, 300, time.Time{})
	if err != nil {
		return httpx.Unavailable("Docker didn't return the proxy's logs: " + err.Error())
	}
	httpx.JSON(w, 200, map[string]any{"lines": lines})
	return nil
}

/* ------------------------------- Domains (all) -------------------------------- */

func (s *Server) allDomains(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	list, err := s.Store.AllDomains(r.Context())
	if err != nil {
		return err
	}
	if project, env := r.URL.Query().Get("project"), r.URL.Query().Get("env"); project != "" {
		kept := list[:0]
		for _, d := range list {
			if d.ProjectID == project && (env == "" || d.Environment == env) {
				kept = append(kept, d)
			}
		}
		list = kept
	}
	providers, _ := s.Store.DNSProviders(r.Context())
	pv := []map[string]any{}
	for _, p := range providers {
		pv = append(pv, map[string]any{"id": p.ID, "name": p.Name, "kind": p.Kind, "zones": p.Zones})
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	v4, v6, _ := s.publicAddresses(r.Context())
	httpx.JSON(w, 200, map[string]any{"domains": list, "dnsProviders": pv, "publicIpv4": v4, "publicIpv6": v6,
		"proxy": map[string]any{"running": s.Proxy.Running(ctx), "httpPort": s.Proxy.HTTPPort, "httpsPort": s.Proxy.HTTPSPort}})
	return nil
}

var recheckMu sync.Mutex

func (s *Server) recheckAllDomains(ctx context.Context) {
	if !recheckMu.TryLock() {
		return // already running
	}
	defer recheckMu.Unlock()
	list, err := s.Store.AllDomains(ctx)
	if err != nil {
		return
	}
	checked := map[string]bool{}
	for _, d := range list {
		if checked[d.Hostname] {
			continue // one check per hostname; the result is the same for every path on it
		}
		checked[d.Hostname] = true
		s.checkDomain(ctx, &d.Domain)
	}
}

func (s *Server) recheckDomains(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	s.syncProxy(r.Context())
	go s.recheckAllDomains(context.WithoutCancel(r.Context()))
	httpx.JSON(w, 202, map[string]any{"started": true})
	return nil
}

/* --------------------------------- DNS providers -------------------------------- */

func (s *Server) cloudflare(p *store.DNSProvider) (dns.Cloudflare, error) {
	tok, err := s.Secrets.Open(p.TokenEnc, "dns:"+p.ID)
	if err != nil {
		return dns.Cloudflare{}, errors.New("the stored token can't be read; reconnect the provider")
	}
	return dns.Cloudflare{Token: tok}, nil
}

func (s *Server) listDNSProviders(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	list, err := s.Store.DNSProviders(r.Context())
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"providers": list})
	return nil
}

func (s *Server) createDNSProvider(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in struct{ Name, Kind, Token string }
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Name, in.Token = strings.TrimSpace(in.Name), strings.TrimSpace(in.Token)
	f := map[string]string{}
	if in.Kind != "cloudflare" {
		f["kind"] = "Only Cloudflare is supported."
	}
	if in.Name == "" || len(in.Name) > 60 {
		f["name"] = "Name it (up to 60 characters)."
	}
	if in.Token == "" {
		f["token"] = "Paste an API token with Zone:Read and DNS:Edit."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	cf := dns.Cloudflare{Token: in.Token}
	if err := cf.Verify(r.Context()); err != nil {
		return httpx.Invalid(map[string]string{"token": err.Error()})
	}
	zones, err := cf.Zones(r.Context())
	if err != nil {
		return httpx.Invalid(map[string]string{"token": err.Error()})
	}
	if len(zones) == 0 {
		return httpx.Invalid(map[string]string{"token": "The token works, but it can't see any zones. Give it Zone:Read on your zones."})
	}
	names := []string{}
	for _, z := range zones {
		names = append(names, z.Name)
	}
	id, err := s.Store.CreateDNSProvider(r.Context(), in.Name, in.Kind, names, func(id string) ([]byte, error) { return s.Secrets.Seal(in.Token, "dns:"+id) }, u.ID)
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "dns.provider_connected", "dns", id, map[string]any{"name": in.Name, "kind": in.Kind, "zones": len(names)}, clientIP(r))
	httpx.JSON(w, 201, map[string]any{"id": id, "zones": names})
	return nil
}

func (s *Server) refreshDNSProvider(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Provider")
	if err != nil {
		return err
	}
	p, err := s.Store.GetDNSProvider(r.Context(), id)
	if err != nil {
		return notFound(err, "Provider")
	}
	cf, err := s.cloudflare(p)
	if err != nil {
		return httpx.Unavailable(err.Error())
	}
	zones, err := cf.Zones(r.Context())
	if err != nil {
		return httpx.Unavailable(err.Error())
	}
	names := []string{}
	for _, z := range zones {
		names = append(names, z.Name)
	}
	s.Store.SetDNSProviderZones(r.Context(), id, names)
	httpx.JSON(w, 200, map[string]any{"zones": names})
	return nil
}

func (s *Server) deleteDNSProvider(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Provider")
	if err != nil {
		return err
	}
	if err := s.Store.DeleteDNSProvider(r.Context(), id); err != nil {
		return notFound(err, "Provider")
	}
	s.Store.Audit(r.Context(), u, "dns.provider_removed", "dns", id, nil, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}

// createDNSRecords points the domain's hostname at this server (A and AAAA) through the provider.
func (s *Server) createDNSRecords(ctx context.Context, d *store.Domain, providerID string) error {
	if proxy.IsLocal(d.Hostname) {
		return errors.New(d.Hostname + " is a local name; it needs no DNS record")
	}
	p, err := s.Store.GetDNSProvider(ctx, providerID)
	if err != nil {
		return errors.New("that DNS provider doesn't exist any more")
	}
	cf, err := s.cloudflare(p)
	if err != nil {
		return err
	}
	zones, err := cf.Zones(ctx)
	if err != nil {
		return err
	}
	zone, ok := dns.ZoneFor(zones, d.Hostname)
	if !ok {
		return fmt.Errorf("%s isn't in any zone %s can edit", d.Hostname, p.Name)
	}
	v4, v6, _ := s.publicAddresses(ctx)
	if v4 == "" && v6 == "" {
		return errors.New("this server's public address is unknown; set it under Networking › Addresses")
	}
	var recs []store.DNSRecordRef
	for _, rr := range []struct{ typ, ip string }{{"A", v4}, {"AAAA", v6}} {
		if rr.ip == "" {
			continue
		}
		rec, err := cf.Upsert(ctx, zone.ID, rr.typ, d.Hostname, rr.ip)
		if err != nil {
			return err
		}
		recs = append(recs, store.DNSRecordRef{Type: rr.typ, ID: rec.ID, ZoneID: zone.ID, Content: rr.ip})
	}
	// Every route on the hostname shares the records.
	s.Store.DB.Exec(ctx, `UPDATE domains SET dns_provider_id = NULL, dns_records = '[]' WHERE hostname = $1 AND id <> $2`, d.Hostname, d.ID)
	return s.Store.SetDomainDNS(ctx, d.ID, &p.ID, recs)
}

func (s *Server) deleteDNSRecords(ctx context.Context, d *store.Domain) error {
	p, err := s.Store.GetDNSProvider(ctx, *d.DNSProviderID)
	if err != nil {
		return nil // provider gone: nothing we can remove
	}
	cf, err := s.cloudflare(p)
	if err != nil {
		return err
	}
	for _, rec := range d.DNSRecords {
		if err := cf.Delete(ctx, rec.ZoneID, rec.ID); err != nil {
			return err
		}
	}
	return s.Store.SetDomainDNS(ctx, d.ID, nil, nil)
}

func (s *Server) domainDNS(w http.ResponseWriter, r *http.Request, u *store.User) error {
	d, err := s.domainFor(r)
	if err != nil {
		return err
	}
	var in struct {
		ProviderID string `json:"providerId"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if err := s.createDNSRecords(r.Context(), d, in.ProviderID); err != nil {
		return httpx.Invalid(map[string]string{"providerId": err.Error()})
	}
	s.Store.Audit(r.Context(), u, "domain.dns_records", "resource", d.ResourceID, map[string]any{"hostname": d.Hostname}, clientIP(r))
	d, _ = s.Store.GetDomain(r.Context(), d.ID)
	httpx.JSON(w, 200, map[string]any{"domain": s.checkDomain(r.Context(), d)})
	return nil
}

/* ---------------------------------- Firewall ---------------------------------- */

func firewallRulesFor(list []store.FirewallRule) []firewall.Rule {
	out := []firewall.Rule{}
	for _, f := range list {
		if f.Enabled {
			out = append(out, firewall.Rule{Port: f.Port, Protocol: f.Protocol, Sources: f.Sources})
		}
	}
	return out
}

func (s *Server) getFirewall(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, dk, err := s.serverDocker(r)
	if err != nil {
		return err
	}
	rules, err := s.Store.FirewallRules(r.Context(), srv.ID)
	if err != nil {
		return err
	}
	desired := firewall.Hash(firewallRulesFor(rules))
	status := map[string]any{"desired": desired}
	if r.URL.Query().Get("status") == "1" && can(u, "firewall.manage") { // reading the host's rules runs a helper container
		code, out, err := dk.RunHostTool(r.Context(), firewall.Image, firewall.StatusScript, 60*time.Second)
		switch {
		case err != nil:
			status["error"] = err.Error()
		case code != 0:
			status["error"] = "Reading the host's rules failed: " + lastLine(out)
		default:
			applied, dockerUser := firewall.ParseStatus(out)
			status["applied"], status["dockerUser"] = applied, dockerUser
			status["inSync"] = applied == desired || (applied == "none" && len(firewallRulesFor(rules)) == 0)
		}
	}
	httpx.JSON(w, 200, map[string]any{"rules": rules, "status": status, "dockerDesktop": strings.Contains(strings.ToLower(serverOS(srv)), "docker desktop"),
		"protected": s.systemPorts(), "you": clientIP(r)})
	return nil
}

func serverOS(srv *store.Server) string {
	v := viewServer(srv)
	if v.Info.Engine != nil {
		return v.Info.Engine.OS
	}
	return ""
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func (s *Server) putFirewallRule(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	var in struct {
		Port     int      `json:"port"`
		Protocol string   `json:"protocol"`
		Sources  []string `json:"sources"`
		Note     string   `json:"note"`
		Enabled  bool     `json:"enabled"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	if in.Port < 1 || in.Port > 65535 {
		f["port"] = "1 to 65535."
	}
	if in.Protocol != "tcp" && in.Protocol != "udp" {
		f["protocol"] = "TCP or UDP."
	}
	sources, msg := validateAllowIPs(in.Sources)
	if msg != "" {
		f["sources"] = msg
	}
	in.Note = strings.TrimSpace(in.Note)
	if len(in.Note) > 200 {
		f["note"] = "Up to 200 characters."
	}
	// Never let a rule cut the person saving it off the dashboard or the proxy.
	if srv.IsLocal && in.Enabled && len(f) == 0 {
		for _, p := range s.systemPorts() {
			if p.Port != in.Port || p.Protocol != in.Protocol {
				continue
			}
			ip := net.ParseIP(clientIP(r))
			allowed := false
			for _, src := range sources {
				if _, n, err := net.ParseCIDR(src); err == nil && ip != nil && n.Contains(ip) {
					allowed = true
				} else if src == clientIP(r) {
					allowed = true
				}
			}
			if !allowed {
				f["sources"] = fmt.Sprintf("Port %d is the %s. Include your address (%s), or you'll lock yourself out.", p.Port, strings.ToLower(p.Use), clientIP(r))
			}
		}
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	id, err := s.Store.SaveFirewallRule(r.Context(), store.FirewallRule{ServerID: srv.ID, Port: in.Port, Protocol: in.Protocol, Sources: sources, Note: in.Note, Enabled: in.Enabled}, u.ID)
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "firewall.rule_saved", "server", srv.ID, map[string]any{"port": in.Port, "protocol": in.Protocol, "sources": sources, "enabled": in.Enabled}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"id": id})
	return nil
}

func (s *Server) deleteFirewallRule(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Rule")
	if err != nil {
		return err
	}
	rule, err := s.Store.GetFirewallRule(r.Context(), id)
	if err != nil {
		return notFound(err, "Rule")
	}
	if err := s.Store.DeleteFirewallRule(r.Context(), id); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "firewall.rule_deleted", "server", rule.ServerID, map[string]any{"port": rule.Port, "protocol": rule.Protocol}, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}

// applyFirewall makes the host match the saved rules now.
func (s *Server) applyFirewall(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, dk, err := s.serverDocker(r)
	if err != nil {
		return err
	}
	rules, err := s.Store.FirewallRules(r.Context(), srv.ID)
	if err != nil {
		return err
	}
	fr := firewallRulesFor(rules)
	code, out, err := dk.RunHostTool(r.Context(), firewall.Image, firewall.Script(fr), 90*time.Second)
	if err != nil {
		return httpx.Unavailable("Couldn't run the firewall helper: " + err.Error())
	}
	if code != 0 {
		return httpx.Unavailable("The host refused the rules: " + lastLine(out))
	}
	s.Store.Audit(r.Context(), u, "firewall.applied", "server", srv.ID, map[string]any{"rules": len(fr), "hash": firewall.Hash(fr)}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"applied": firewall.Hash(fr), "rules": len(fr), "output": out})
	return nil
}

/* -------------------------------- Diagnostics --------------------------------- */

var diagTargetRE = regexp.MustCompile(`^[A-Za-z0-9.:_-]{1,253}$`)

// diagnose runs one network test from a server: on its host network, or inside one of Ignition's
// Docker networks (what a container there would see).
func (s *Server) diagnose(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in struct {
		ServerID string `json:"serverId"`
		Network  string `json:"network"` // "host" or a Docker network name
		Kind     string `json:"kind"`    // dns | tcp | http | ping | traceroute
		Target   string `json:"target"`
		Port     int    `json:"port"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if !s.limiter.allow("diag:" + u.ID) {
		return httpx.TooMany("Wait a moment between tests.")
	}
	in.Target = strings.TrimSpace(in.Target)
	f := map[string]string{}
	target := in.Target
	if in.Kind == "http" {
		u, err := url.Parse(in.Target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.ContainsAny(in.Target, " '\"`$\\") {
			f["target"] = "Enter a URL like https://example.com/health."
		}
	} else if !diagTargetRE.MatchString(target) || strings.HasPrefix(target, "-") {
		f["target"] = "Enter a hostname or IP address."
	}
	if in.Kind == "tcp" && (in.Port < 1 || in.Port > 65535) {
		f["port"] = "1 to 65535."
	}
	var cmd []string
	switch in.Kind {
	case "dns":
		cmd = []string{"nslookup", target}
	case "tcp":
		cmd = []string{"nc", "-z", "-v", "-w", "5", target, strconv.Itoa(in.Port)}
	case "http":
		cmd = []string{"wget", "-S", "-O", "/dev/null", "-T", "10", target}
	case "ping":
		cmd = []string{"ping", "-c", "4", "-W", "2", target}
	case "traceroute":
		cmd = []string{"traceroute", "-n", "-w", "2", "-m", "20", target}
	default:
		f["kind"] = "DNS, TCP, HTTP, ping or traceroute."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	srv, err := s.Store.GetServer(r.Context(), in.ServerID)
	if err != nil {
		if srv, err = s.Store.LocalServer(r.Context()); err != nil {
			return notFound(err, "Server")
		}
	}
	dk, err := s.Servers.Docker(srv)
	if err != nil {
		return httpx.Unavailable(err.Error())
	}
	start := time.Now()
	var code int
	var out string
	if in.Network == "" || in.Network == "host" {
		code, out, err = dk.RunHostTool(r.Context(), runtime.ProbeImage, shellJoin(cmd), 60*time.Second)
	} else {
		if !strings.HasPrefix(in.Network, "ign-") && in.Network != "ignition" {
			return httpx.Invalid(map[string]string{"network": "Pick one of Ignition's networks."})
		}
		code, out, err = dk.RunProbe(r.Context(), runtime.Probe{Networks: []string{in.Network}, Cmd: cmd, Timeout: 60 * time.Second})
	}
	if err != nil {
		return httpx.Unavailable("The test couldn't run: " + err.Error())
	}
	where := in.Network
	if where == "" {
		where = "host"
	}
	s.Store.Audit(r.Context(), u, "network.diagnose", "server", srv.ID, map[string]any{"kind": in.Kind, "target": in.Target, "network": where, "ok": code == 0}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"ok": code == 0, "exitCode": code, "output": out, "ms": time.Since(start).Milliseconds(), "server": srv.Name, "network": where,
		"command": strings.Join(cmd, " ")})
	return nil
}

// shellJoin quotes arguments for sh -c; they're already restricted to safe characters.
func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(a, "'", "") + "'"
	}
	return strings.Join(q, " ") + " 2>&1"
}
