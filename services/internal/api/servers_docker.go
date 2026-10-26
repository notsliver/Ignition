package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/servers"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/worker"
)

/*
 * Server-level Docker inventory. Reads go through the API (never a socket exposed to the browser)
 * and every container is tagged with its owner: an IGNITION resource on this server, an IGNITION
 * helper, or unmanaged. Only resource containers can be started, stopped or restarted here, and
 * that goes through the resource's own job queue, exactly like the resource page.
 */

var dockerIDRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:@/-]{0,254}$`)

func (s *Server) serverDocker(r *http.Request) (*store.Server, *runtime.Docker, error) {
	srv, err := s.loadServer(r)
	if err != nil {
		return nil, nil, err
	}
	dk, err := s.Servers.Docker(srv)
	if err != nil {
		return srv, nil, httpx.Unavailable(err.Error())
	}
	return srv, dk, nil
}

func unavailable(srv *store.Server, err error) error {
	msg, _ := servers.Classify(err)
	return httpx.Unavailable("Docker on " + srv.Name + " didn't respond: " + msg)
}

type ownerRef struct {
	Kind     string             `json:"kind"` // resource, helper, ignition, unmanaged
	Resource *store.ResourceRef `json:"resource,omitempty"`
}

func ownerOf(labels map[string]string, idx map[string]store.ResourceRef, serverID string) ownerRef {
	if id := labels["ignition.resource"]; id != "" {
		if ref, ok := idx[id]; ok && ref.ServerID == serverID {
			return ownerRef{"resource", &ref}
		}
		return ownerRef{Kind: "ignition"} // a resource that's gone, or that lives on another server
	}
	if labels["ignition.probe"] != "" || labels["ignition.backup"] != "" || labels["ignition.updater"] != "" {
		return ownerRef{Kind: "helper"}
	}
	if labels["ignition.managed"] == "true" {
		return ownerRef{Kind: "ignition"}
	}
	return ownerRef{Kind: "unmanaged"}
}

// containerOwner returns the resource that owns a container on this server, or nil.
func (s *Server) containerOwner(ctx context.Context, dk *runtime.Docker, srv *store.Server, id string) (*store.ResourceRef, error) {
	if !dockerIDRE.MatchString(id) {
		return nil, httpx.NotFound("Container")
	}
	st, err := dk.Inspect(ctx, id)
	if err != nil {
		return nil, unavailable(srv, err)
	}
	if !st.Exists {
		return nil, httpx.NotFound("Container")
	}
	idx, err := s.Store.ResourceIndex(ctx)
	if err != nil {
		return nil, err
	}
	return ownerOf(st.Config.Labels, idx, srv.ID).Resource, nil
}

func (s *Server) getServerDocker(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, dk, err := s.serverDocker(r)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	info, err := dk.EngineInfo(ctx)
	if err != nil {
		return unavailable(srv, err)
	}
	du, duErr := dk.DiskUsage(ctx)
	nets, _ := dk.ListNetworks(ctx)
	out := map[string]any{"engine": info, "compatible": runtime.APICompatible(info.APIVersion), "minApiVersion": runtime.MinAPIVersion,
		"counts": map[string]int{"containers": info.Containers, "running": info.Running, "stopped": info.Stopped, "paused": info.Paused, "images": info.Images,
			"networks": len(nets), "volumes": len(du.Volumes)}}
	if duErr == nil {
		var vol, cache, cacheReclaim int64
		for _, v := range du.Volumes {
			vol += max(v.Size, 0)
		}
		for _, b := range du.BuildCache {
			if !b.Shared {
				cache += b.Size
				if !b.InUse {
					cacheReclaim += b.Size
				}
			}
		}
		out["disk"] = map[string]int64{"images": du.LayersSize, "volumes": vol, "buildCache": cache, "buildCacheReclaimable": cacheReclaim}
	}
	if srv.IsLocal {
		out["dataRoot"] = info.DockerRootDir
	}
	httpx.JSON(w, 200, out)
	return nil
}

type containerView struct {
	runtime.ContainerSummary
	Owner ownerRef `json:"owner"`
}

func (s *Server) listServerContainers(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, dk, err := s.serverDocker(r)
	if err != nil {
		return err
	}
	list, err := dk.ListContainers(r.Context())
	if err != nil {
		return unavailable(srv, err)
	}
	idx, err := s.Store.ResourceIndex(r.Context())
	if err != nil {
		return err
	}
	out := make([]containerView, 0, len(list))
	for _, c := range list {
		out = append(out, containerView{c, ownerOf(c.Labels, idx, srv.ID)})
	}
	httpx.JSON(w, 200, map[string]any{"containers": out, "observedAt": time.Now().UTC()})
	return nil
}

func (s *Server) listServerImages(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, dk, err := s.serverDocker(r)
	if err != nil {
		return err
	}
	du, err := dk.DiskUsage(r.Context())
	if err != nil {
		return unavailable(srv, err)
	}
	containers, _ := dk.ListContainers(r.Context())
	retained, _ := s.Store.RetainedImageIDs(r.Context(), srv.ID)
	usedBy := map[string][]string{}
	running := map[string]bool{}
	for _, c := range containers {
		usedBy[c.ImageID] = append(usedBy[c.ImageID], c.Name)
		if c.State == "running" {
			running[c.ImageID] = true
		}
	}
	type imageView struct {
		runtime.ImageSummary
		UsedBy   []string `json:"usedBy"`
		Running  bool     `json:"running"`
		Retained bool     `json:"retained"`
		Ignition bool     `json:"ignition"`
	}
	out := []imageView{}
	for _, img := range du.Images {
		v := imageView{ImageSummary: img, UsedBy: usedBy[img.ID], Running: running[img.ID], Retained: retained[img.ID]}
		if v.UsedBy == nil {
			v.UsedBy = []string{}
		}
		for _, t := range img.Tags {
			if strings.HasPrefix(t, "ignition/") {
				v.Ignition = true
			}
		}
		if img.Labels["ignition.resource"] != "" {
			v.Ignition = true
		}
		out = append(out, v)
	}
	httpx.JSON(w, 200, map[string]any{"images": out, "layersSize": du.LayersSize, "buildCache": du.BuildCache})
	return nil
}

func (s *Server) listServerNetworks(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, dk, err := s.serverDocker(r)
	if err != nil {
		return err
	}
	nets, err := dk.ListNetworks(r.Context())
	if err != nil {
		return unavailable(srv, err)
	}
	containers, _ := dk.ListContainers(r.Context())
	type member struct {
		Name string `json:"name"`
		IP   string `json:"ip"`
	}
	attached := map[string][]member{}
	for _, c := range containers {
		for _, n := range c.Networks {
			attached[n.Network] = append(attached[n.Network], member{c.Name, n.IP})
		}
	}
	type netView struct {
		runtime.NetworkDetail
		Containers []member `json:"containers"`
		System     bool     `json:"system"`
		Ignition   bool     `json:"ignition"`
	}
	out := []netView{}
	for _, n := range nets {
		v := netView{NetworkDetail: n, Containers: attached[n.Name], System: n.Name == "bridge" || n.Name == "host" || n.Name == "none",
			Ignition: n.Name == "ignition" || n.Labels["ignition.managed"] == "true"}
		if v.Containers == nil {
			v.Containers = []member{}
		}
		out = append(out, v)
	}
	httpx.JSON(w, 200, map[string]any{"networks": out})
	return nil
}

type volumeView struct {
	runtime.VolumeSummary
	Owner      ownerRef `json:"owner"`
	MountedBy  []string `json:"mountedBy"`
	MountPath  string   `json:"mountPath"`
	Persistent bool     `json:"persistent"`
}

func (s *Server) volumeInventory(ctx context.Context, srv *store.Server, dk *runtime.Docker) ([]volumeView, []runtime.ContainerSummary, error) {
	du, err := dk.DiskUsage(ctx)
	if err != nil {
		return nil, nil, err
	}
	containers, _ := dk.ListContainers(ctx)
	idx, err := s.Store.ResourceIndex(ctx)
	if err != nil {
		return nil, nil, err
	}
	mounted := map[string][]string{}
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Type == "volume" {
				mounted[m.Name] = append(mounted[m.Name], c.Name)
			}
		}
	}
	configured := map[string]string{}
	if rows, err := s.Store.ServerVolumes(ctx, srv.ID); err == nil {
		for _, v := range rows {
			configured[worker.VolumeName(v.ResourceID, v.Name)] = v.MountPath
		}
	}
	byPrefix := map[string]store.ResourceRef{}
	for id, ref := range idx {
		if ref.ServerID == srv.ID {
			byPrefix[worker.VolumePrefix(id)] = ref
		}
	}
	out := []volumeView{}
	for _, v := range du.Volumes {
		vv := volumeView{VolumeSummary: v, MountedBy: mounted[v.Name], Owner: ownerOf(v.Labels, idx, srv.ID), MountPath: configured[v.Name]}
		if vv.Owner.Kind == "unmanaged" || vv.Owner.Kind == "ignition" {
			for prefix, ref := range byPrefix {
				if strings.HasPrefix(v.Name, prefix) {
					r := ref
					vv.Owner = ownerRef{"resource", &r}
				}
			}
		}
		if vv.MountedBy == nil {
			vv.MountedBy = []string{}
		}
		// Anonymous volumes (64 hex chars) are usually throwaway; everything else is treated as data.
		vv.Persistent = !(len(v.Name) == 64 && strings.Trim(v.Name, "0123456789abcdef") == "")
		out = append(out, vv)
	}
	return out, containers, nil
}

func (s *Server) listServerVolumes(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, dk, err := s.serverDocker(r)
	if err != nil {
		return err
	}
	vols, _, err := s.volumeInventory(r.Context(), srv, dk)
	if err != nil {
		return unavailable(srv, err)
	}
	httpx.JSON(w, 200, map[string]any{"volumes": vols})
	return nil
}

func (s *Server) inspectServerObject(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, dk, err := s.serverDocker(r)
	if err != nil {
		return err
	}
	kind, id := r.URL.Query().Get("kind"), r.URL.Query().Get("id")
	if !dockerIDRE.MatchString(id) {
		return httpx.NotFound("Object")
	}
	doc, err := dk.InspectObject(r.Context(), kind, id)
	if runtime.IsNotFound(err) {
		return httpx.NotFound("Object")
	}
	if err != nil {
		return unavailable(srv, err)
	}
	s.Store.Audit(r.Context(), u, "server.docker_inspect", "server", srv.ID, map[string]any{"kind": kind, "id": id}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"kind": kind, "document": doc, "redacted": kind == "container"})
	return nil
}

// serverContainerAction starts, stops or restarts a container IGNITION owns, through the owning
// resource's job queue. Unmanaged containers are refused: IGNITION isn't authorised to run them.
func (s *Server) serverContainerAction(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, dk, err := s.serverDocker(r)
	if err != nil {
		return err
	}
	var in struct {
		Action string `json:"action"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	switch in.Action {
	case "start", "stop", "restart":
	default:
		return httpx.BadRequest("Action must be start, stop or restart. To recreate a container, redeploy its resource.")
	}
	owner, err := s.containerOwner(r.Context(), dk, srv, r.PathValue("cid"))
	if err != nil {
		return err
	}
	if owner == nil {
		return httpx.Forbidden("This container isn't an IGNITION resource on this server, so IGNITION won't start or stop it. Manage it on the host.")
	}
	res, err := s.Store.GetResource(r.Context(), owner.ID)
	if err != nil {
		return notFound(err, "Resource")
	}
	if res.ContainerID == "" || !strings.HasPrefix(res.ContainerID, r.PathValue("cid")) && !strings.HasPrefix(r.PathValue("cid"), res.ContainerID) {
		return httpx.Conflict("This is an old container of " + res.Name + ", not its current one. Act on the resource instead.")
	}
	jobID, err := s.enqueueForResource(r.Context(), jobs.KindAction, res.ID, u.ID, map[string]string{"action": in.Action})
	if err != nil {
		return err
	}
	if in.Action == "stop" {
		s.Store.SetDesiredState(r.Context(), res.ID, "stopped")
	} else {
		s.Store.SetDesiredState(r.Context(), res.ID, "running")
	}
	s.Store.Audit(r.Context(), u, "resource."+in.Action, "resource", res.ID, map[string]any{"name": res.Name, "via": "server " + srv.Name}, clientIP(r))
	httpx.JSON(w, 202, map[string]string{"jobId": jobID, "resourceId": res.ID})
	return nil
}

/* ------------------------------------ Cleanup ----------------------------------- */

func (s *Server) previewServerCleanup(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, dk, err := s.serverDocker(r)
	if err != nil {
		return err
	}
	var in servers.CleanupOptions
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if !in.Any() {
		return httpx.Invalid(map[string]string{"options": "Choose at least one kind of object to clean up."})
	}
	plan, err := servers.PlanCleanup(r.Context(), dk, s.Store, srv, in)
	if err != nil {
		return unavailable(srv, err)
	}
	httpx.JSON(w, 200, plan)
	return nil
}

func (s *Server) runServerCleanup(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	var in struct {
		Options servers.CleanupOptions `json:"options"`
		Items   []servers.CleanupItem  `json:"items"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if len(in.Items) == 0 {
		return httpx.Invalid(map[string]string{"items": "Nothing selected."})
	}
	if len(in.Items) > 2000 {
		return httpx.Invalid(map[string]string{"items": "Too many objects at once."})
	}
	for _, it := range in.Items {
		if (it.Kind != "container" && it.Kind != "image" && it.Kind != "network" && it.Kind != "buildcache") || !dockerIDRE.MatchString(it.ID) {
			return httpx.Invalid(map[string]string{"items": "Invalid object in the selection. Preview again."})
		}
	}
	jobID, err := s.enqueueServerOp(r.Context(), srv.ID, u.ID, servers.Payload{Op: "cleanup", Cleanup: in.Options, Items: in.Items, Actor: actorName(u), TimeoutSeconds: 900})
	if err != nil {
		return err
	}
	kinds := map[string]int{}
	for _, it := range in.Items {
		kinds[it.Kind]++
	}
	s.Store.Audit(r.Context(), u, "server.docker_cleanup", "server", srv.ID, map[string]any{"name": srv.Name, "objects": kinds}, clientIP(r))
	httpx.JSON(w, 202, map[string]string{"jobId": jobID})
	return nil
}

/* ------------------------------------ Storage ----------------------------------- */

func (s *Server) getServerStorage(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, dk, err := s.serverDocker(r)
	if err != nil {
		return err
	}
	vols, containers, err := s.volumeInventory(r.Context(), srv, dk)
	if err != nil {
		return unavailable(srv, err)
	}
	idx, _ := s.Store.ResourceIndex(r.Context())
	type bindView struct {
		Container   string   `json:"container"`
		Owner       ownerRef `json:"owner"`
		Source      string   `json:"source"`
		Destination string   `json:"destination"`
		ReadOnly    bool     `json:"readOnly"`
	}
	binds := []bindView{}
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Type == "bind" {
				binds = append(binds, bindView{c.Name, ownerOf(c.Labels, idx, srv.ID), m.Source, m.Destination, m.ReadOnly})
			}
		}
	}
	// Mount details per volume: destination and read-only, from the containers using it.
	type mountView struct {
		Container   string `json:"container"`
		Destination string `json:"destination"`
		ReadOnly    bool   `json:"readOnly"`
	}
	mounts := map[string][]mountView{}
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Type == "volume" {
				mounts[m.Name] = append(mounts[m.Name], mountView{c.Name, m.Destination, m.ReadOnly})
			}
		}
	}
	// Volumes IGNITION configured but Docker doesn't have (yet): created on the next deploy.
	present := map[string]bool{}
	for _, v := range vols {
		present[v.Name] = true
	}
	missing := []map[string]string{}
	if rows, err := s.Store.ServerVolumes(r.Context(), srv.ID); err == nil {
		for _, v := range rows {
			name := worker.VolumeName(v.ResourceID, v.Name)
			if !present[name] {
				ref := idx[v.ResourceID]
				missing = append(missing, map[string]string{"name": name, "resourceId": v.ResourceID, "resource": ref.Name, "projectId": ref.ProjectID, "mountPath": v.MountPath})
			}
		}
	}
	sort.Slice(binds, func(i, j int) bool { return binds[i].Container < binds[j].Container })
	httpx.JSON(w, 200, map[string]any{"volumes": vols, "mounts": mounts, "binds": binds, "missing": missing,
		"backups": "IGNITION doesn't schedule volume backups yet. Download a point-in-time archive from the resource's Storage page.",
		"hostPaths": srv.IsLocal})
	return nil
}

/* ---------------------------------- Networking ---------------------------------- */

func (s *Server) getServerNetworking(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, dk, err := s.serverDocker(r)
	if err != nil {
		return err
	}
	containers, err := dk.ListContainers(r.Context())
	if err != nil {
		return unavailable(srv, err)
	}
	nets, _ := dk.ListNetworks(r.Context())
	idx, _ := s.Store.ResourceIndex(r.Context())
	type portView struct {
		Container     string   `json:"container"`
		Owner         ownerRef `json:"owner"`
		HostIP        string   `json:"hostIp"`
		HostPort      int      `json:"hostPort"`
		ContainerPort int      `json:"containerPort"`
		Protocol      string   `json:"protocol"`
		Exposure      string   `json:"exposure"` // public (all interfaces), loopback, specific
	}
	ports := []portView{}
	for _, c := range containers {
		if c.State != "running" {
			continue
		}
		seen := map[string]bool{}
		for _, p := range c.Ports {
			if p.HostPort == 0 {
				continue
			}
			key := fmt.Sprintf("%s:%d/%s", p.HostIP, p.HostPort, p.Protocol)
			if seen[key] {
				continue
			}
			seen[key] = true
			exp := "specific"
			switch {
			case p.HostIP == "" || p.HostIP == "0.0.0.0" || p.HostIP == "::":
				exp = "public"
			case strings.HasPrefix(p.HostIP, "127.") || p.HostIP == "::1":
				exp = "loopback"
			}
			ports = append(ports, portView{c.Name, ownerOf(c.Labels, idx, srv.ID), p.HostIP, p.HostPort, p.ContainerPort, p.Protocol, exp})
		}
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].HostPort < ports[j].HostPort })
	attached := map[string]int{}
	for _, c := range containers {
		for _, n := range c.Networks {
			attached[n.Network]++
		}
	}
	type netSummary struct {
		Name       string   `json:"name"`
		Driver     string   `json:"driver"`
		Scope      string   `json:"scope"`
		Internal   bool     `json:"internal"`
		Subnets    []string `json:"subnets"`
		Containers int      `json:"containers"`
		Ignition   bool     `json:"ignition"`
	}
	netsOut := []netSummary{}
	for _, n := range nets {
		netsOut = append(netsOut, netSummary{n.Name, n.Driver, n.Scope, n.Internal, n.Subnets, attached[n.Name], n.Name == "ignition" || n.Labels["ignition.managed"] == "true"})
	}
	domains, _ := s.Store.ServerDomains(r.Context(), srv.ID)
	out := map[string]any{"ports": ports, "networks": netsOut, "domains": domains,
		"hostInterfaces": map[string]any{"available": false, "reason": "IGNITION reads the host through the Docker API (and, for this machine, /proc), which don't expose host interfaces, addresses or routes. Use ip addr and ip route on the host."},
		"firewall":       map[string]any{"managed": false, "reason": "IGNITION doesn't manage host firewalls. Ports published on all interfaces are reachable from any network that can reach the host unless your firewall blocks them. Note that Docker's published ports bypass ufw by default."},
	}
	if srv.IsLocal {
		out["proxy"] = map[string]any{"httpPort": store.ProxyHTTPPort, "httpsPort": store.ProxyHTTPSPort, "description": "Custom domains are routed by IGNITION's proxy to resources over Docker networks; TLS certificates are issued automatically for HTTPS domains."}
	} else {
		out["proxy"] = map[string]any{"description": "IGNITION's proxy runs on the local host only; it doesn't route to remote servers."}
	}
	httpx.JSON(w, 200, out)
	return nil
}
