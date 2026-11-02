package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/games"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/worker"
)

/*
 * Game servers. The games package knows each game; these handlers are generic and use whatever the
 * adapter supports. Everything that touches files works through helper containers, so it works
 * whether the server is running or not; anything that talks to the game needs it running.
 */

func (s *Server) localDocker() (*runtime.Docker, error) {
	d, ok := s.Runtime.(*runtime.Docker)
	if !ok {
		return nil, httpx.Unsupported("Game servers need the local Docker engine.")
	}
	return d, nil
}

// gameServer loads the game server named by the path's {id}.
func (s *Server) gameServer(r *http.Request) (*store.Resource, games.Server, error) {
	id, err := pathID(r, "Game server")
	if err != nil {
		return nil, games.Server{}, err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return nil, games.Server{}, notFound(err, "Game server")
	}
	if res.Type != "game" {
		return nil, games.Server{}, httpx.NotFound("Game server")
	}
	d, err := s.localDocker()
	if err != nil {
		return nil, games.Server{}, err
	}
	srv, err := worker.GameServer(r.Context(), s.Store, d, res)
	if err != nil {
		return nil, games.Server{}, httpx.Conflict(err.Error())
	}
	return res, srv, nil
}

// gameErr turns the games package's errors into answers people can act on.
func gameErr(err error) error {
	var he *httpx.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &he):
		return err
	case errors.Is(err, games.ErrNotRunning):
		return httpx.Conflict("The server isn't running. Start it first.")
	case errors.Is(err, games.ErrUnavailable):
		return httpx.Unavailable(capitalize(err.Error()) + ". Try again in a moment.")
	case errors.Is(err, games.ErrNotFound):
		return httpx.NotFound("File")
	case errors.Is(err, games.ErrOutside):
		return httpx.BadRequest(capitalize(err.Error()) + ".")
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return err
	}
	return httpx.Conflict(capitalize(strings.TrimSuffix(err.Error(), ".")) + ".")
}

func gameAdapter(r *http.Request) (games.Adapter, error) {
	a, ok := games.Get(r.PathValue("game"))
	if !ok {
		return nil, httpx.NotFound("Game")
	}
	return a, nil
}

/* --------------------------------- Catalog ---------------------------------- */

func (s *Server) gamesCatalog(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	list := []games.Manifest{}
	for _, a := range games.All() {
		list = append(list, a.Manifest())
	}
	httpx.JSON(w, 200, map[string]any{"games": list})
	return nil
}

func (s *Server) gameVersions(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	a, err := gameAdapter(r)
	if err != nil {
		return err
	}
	edition := r.URL.Query().Get("edition")
	if _, ok := games.EditionOf(a, edition); !ok {
		return httpx.NotFound("Edition")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	list, err := a.Versions(ctx, edition)
	if err != nil {
		return httpx.Unavailable("Couldn't load the version list (" + err.Error() + "). Try again, or type a version.")
	}
	if list.Versions == nil {
		list.Versions = []games.Version{}
	}
	httpx.JSON(w, 200, list)
	return nil
}

// gameModpacks searches modpacks that run on a server.
func (s *Server) gameModpacks(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	if _, err := gameAdapter(r); err != nil {
		return err
	}
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	res, err := games.Search(r.Context(), games.SearchQuery{Query: q.Get("q"), ProjectType: "modpack", Sort: q.Get("sort"), Offset: max(offset, 0), Limit: 20})
	if err != nil {
		return gameErr(err)
	}
	httpx.JSON(w, 200, res)
	return nil
}

func (s *Server) gameModpackVersions(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	if _, err := gameAdapter(r); err != nil {
		return err
	}
	list, err := games.ProjectVersions(r.Context(), r.PathValue("project"), nil, "")
	if err != nil {
		return gameErr(err)
	}
	httpx.JSON(w, 200, map[string]any{"versions": list})
	return nil
}

// resolveModpack fills in a modpack choice from Modrinth (never from what the browser claims).
func resolveModpack(ctx context.Context, m *games.Modpack) error {
	if m == nil || m.ProjectID == "" {
		return nil
	}
	p, err := games.GetProject(ctx, m.ProjectID)
	if err != nil {
		if errors.Is(err, games.ErrNotFound) {
			return httpx.Invalid(map[string]string{"modpack": "That modpack doesn't exist on Modrinth."})
		}
		return gameErr(err)
	}
	if p.ProjectType != "modpack" {
		return httpx.Invalid(map[string]string{"modpack": p.Title + " isn't a modpack."})
	}
	var v games.ContentVersion
	if m.VersionID != "" {
		if v, err = games.GetVersion(ctx, m.VersionID); err != nil || v.ProjectID != p.ID {
			return httpx.Invalid(map[string]string{"modpack": "That modpack version doesn't exist."})
		}
	} else {
		list, err := games.ProjectVersions(ctx, p.ID, nil, "")
		if err != nil {
			return gameErr(err)
		}
		var ok bool
		if v, ok = games.BestVersion(list); !ok {
			return httpx.Invalid(map[string]string{"modpack": p.Title + " has no versions to install."})
		}
	}
	game := ""
	for _, gv := range v.GameVersions {
		if game == "" || games.CompareVersions(gv, game) > 0 {
			game = gv
		}
	}
	loader := ""
	for _, l := range v.Loaders {
		if l == "fabric" || l == "forge" || l == "neoforge" || l == "quilt" {
			loader = l
			break
		}
	}
	*m = games.Modpack{Provider: "modrinth", ProjectID: p.ID, VersionID: v.ID, Title: p.Title, IconURL: p.IconURL, VersionNumber: v.VersionNumber, GameVersion: game, Loader: loader}
	return nil
}

/* ---------------------------------- Create ---------------------------------- */

type gameInput struct {
	ProjectID     string     `json:"projectId"`
	EnvironmentID string     `json:"environmentId"`
	Name          string     `json:"name"`
	Game          string     `json:"game"`
	Spec          games.Spec `json:"spec"`
	MemoryMB      int        `json:"memoryMb"`
	CPUs          float64    `json:"cpus"`
	AcceptEULA    bool       `json:"acceptEula"`
}

// SuggestedMemory follows the adapter's hint, rounded up to whole 512 MB steps.
func suggestedMemory(m games.Manifest, e games.Edition, slots int) int {
	h := m.Memory
	mb := h.BaseMB + h.PerSlotMB*slots
	if e.Modded {
		mb += h.ModdedMB
	}
	mb = int(math.Ceil(float64(mb)/512)) * 512
	return min(max(mb, h.MinMB), h.MaxMB)
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugName(s string) string {
	n := strings.Trim(slugRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(n) > 50 {
		n = strings.Trim(n[:50], "-")
	}
	return n
}

func (s *Server) createGame(w http.ResponseWriter, r *http.Request, u *store.User) error {
	projectID := r.PathValue("id")
	var in gameInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if projectID == "" {
		projectID = in.ProjectID
	}
	project, err := s.creationProject(r, u, projectID)
	if err != nil {
		return err
	}

	if _, err := s.localDocker(); err != nil {
		return err
	}
	adapter, ok := games.Get(in.Game)
	if !ok {
		return httpx.Invalid(map[string]string{"game": "Choose a game."})
	}
	man := adapter.Manifest()
	f := map[string]string{}
	if man.EULA != nil && !in.AcceptEULA {
		f["eula"] = "Accept the " + man.EULA.Label + " to run a server."
	}
	edition, ok := games.EditionOf(adapter, in.Spec.Edition)
	if ok && edition.Modpacks {
		if err := resolveModpack(r.Context(), in.Spec.Modpack); err != nil {
			return err
		}
	}
	// No version chosen: pin today's latest, so the world isn't upgraded behind the owner's back.
	if ok && edition.Versions == "list" && strings.TrimSpace(in.Spec.Version) == "" {
		if list, err := adapter.Versions(r.Context(), edition.ID); err == nil {
			in.Spec.Version = list.Latest
		}
	}
	for k, v := range adapter.Normalize(&in.Spec) {
		f[k] = v
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		in.Name = slugName(in.Spec.DisplayName)
	}
	if !nameRE.MatchString(in.Name) {
		f["name"] = "Use lowercase letters, numbers and dashes (up to 50 characters)."
	}
	if in.MemoryMB == 0 {
		in.MemoryMB = suggestedMemory(man, edition, in.Spec.Slots)
	}
	if in.MemoryMB < man.Memory.MinMB || in.MemoryMB > man.Memory.MaxMB {
		f["memoryMb"] = fmt.Sprintf("Between %d MB and %d GB.", man.Memory.MinMB, man.Memory.MaxMB/1024)
	}
	if in.CPUs == 0 {
		in.CPUs = 2
	}
	if in.CPUs < 0.25 || in.CPUs > 64 {
		f["cpus"] = "Between 0.25 and 64 CPUs."
	}
	envID := in.EnvironmentID
	if envID == "" && len(project.Environments) > 0 {
		envID = project.Environments[0].ID
	}
	valid := false
	for _, e := range project.Environments {
		valid = valid || e.ID == envID
	}
	if !valid {
		f["environmentId"] = "Unknown environment."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	rt, err := adapter.Runtime(in.Spec, in.MemoryMB, true)
	if err != nil {
		return httpx.Invalid(map[string]string{"version": err.Error()})
	}
	ports, err := s.pickGamePorts(r.Context(), rt.Ports)
	if err != nil {
		return err
	}

	tx, err := s.Store.SQL(r.Context()).Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	id, depID, err := s.provision(r.Context(), tx, u, projectID, envID, resourceInput{
		Name: in.Name, Type: "game", SourceType: "image", Image: rt.Image, DataPath: rt.DataPath, MemoryMB: in.MemoryMB, CPUs: in.CPUs,
	}, provisionExtras{Engine: in.Game, Trigger: "create"})
	if store.IsUnique(err) {
		return httpx.Invalid(map[string]string{"name": "A resource with this name already exists in this environment."})
	}
	if err != nil {
		return err
	}
	if err := s.Store.CreateGameServer(r.Context(), tx, id, in.Game, in.Spec, u.ID); err != nil {
		return err
	}
	// A server with allocations hands out its own IP:port pairs; the game's main port takes one.
	if srv, err := s.Store.LocalServer(r.Context()); err == nil && s.Store.ServerHasAllocations(r.Context(), srv.ID) && len(ports) > 0 {
		alloc, err := s.Store.ClaimAllocation(r.Context(), tx, srv.ID, id)
		if errors.Is(err, store.ErrNotFound) {
			return httpx.Conflict("Every allocation on this server is in use. A platform administrator can add more under Servers › Allocations.")
		}
		if err != nil {
			return err
		}
		ports[0] = alloc.Port
	}
	if err := s.checkResourceCap(r.Context(), "game", 0, 0, len(rt.Ports)); err != nil {
		return domainError(err)
	}
	if err := s.checkQuotas(r.Context(), store.QuotaOwner{Team: u.TeamID, User: u.ID, Location: s.Store.LocalLocation(r.Context())}, u.ID, quotaDemand{Ports: len(rt.Ports)}); err != nil {
		return domainError(err)
	}
	for i, p := range rt.Ports {
		if _, err := tx.Exec(r.Context(), `INSERT INTO resource_ports (resource_id, host_port, container_port, protocol, bind_address) VALUES ($1, $2, $3, $4, '0.0.0.0')`,
			id, ports[i], p.Container, p.Protocol); err != nil {
			if store.IsUnique(err) {
				return httpx.Conflict(fmt.Sprintf("Port %d was just taken by another resource. Try again.", ports[i]))
			}
			return err
		}
	}
	// Saving a big world takes a while; a first start downloads and installs the server.
	if _, err := tx.Exec(r.Context(), `UPDATE resources SET stop_grace_seconds = $2, health_seconds = 15 WHERE id = $1`, id, min(max(rt.StopGrace, 10), 300)); err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "game.create", "resource", id, map[string]any{"name": in.Name, "project": project.Name, "game": in.Game, "edition": in.Spec.Edition, "version": games.GameVersion(in.Spec)}, clientIP(r))
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.JSON(w, 201, map[string]any{"resource": res, "deploymentId": depID})
	return nil
}

// pickGamePorts finds a free host port for each game port: the one players expect, or the next free one.
func (s *Server) pickGamePorts(ctx context.Context, want []games.Port) ([]int, error) {
	inUse := map[string]bool{}
	for _, proto := range []string{"tcp", "udp"} {
		taken, err := s.Store.TakenHostPorts(ctx, proto)
		if err != nil {
			return nil, err
		}
		for p := range taken {
			inUse[fmt.Sprintf("%d/%s", p, proto)] = true
		}
	}
	if bindings, err := s.Runtime.HostBindings(ctx); err == nil {
		for _, b := range bindings {
			inUse[fmt.Sprintf("%d/%s", b.HostPort, orStr(b.Protocol, "tcp"))] = true
		}
	}
	out := make([]int, len(want))
	for i, p := range want {
		port := 0
		for c := p.Preferred; c < p.Preferred+200 && c <= 65535; c++ {
			if !inUse[fmt.Sprintf("%d/%s", c, p.Protocol)] {
				port = c
				break
			}
		}
		if port == 0 {
			return nil, httpx.Conflict(fmt.Sprintf("No free %s port near %d. Free one under Networking and try again.", strings.ToUpper(p.Protocol), p.Preferred))
		}
		inUse[fmt.Sprintf("%d/%s", port, p.Protocol)] = true
		out[i] = port
	}
	return out, nil
}

func orStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

/* ---------------------------------- Server ---------------------------------- */

type gameAddress struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Display  string `json:"display"` // what players type
	Default  bool   `json:"default"` // the port players' games assume
	Public   bool   `json:"public"`  // the host is the configured public address
}

func (s *Server) gameAddress(r *http.Request, res *store.Resource, srv games.Server) *gameAddress {
	rt, err := srv.Adapter.Runtime(srv.Spec, res.MemoryMB, false)
	if err != nil || len(rt.Ports) == 0 {
		return nil
	}
	gp := rt.Ports[0]
	netcfg, err := s.Store.GetNetworking(r.Context(), res.ID)
	if err != nil {
		return nil
	}
	port := 0
	for _, p := range netcfg.Ports {
		if p.ContainerPort == gp.Container && p.Protocol == gp.Protocol && p.HostPort != nil {
			port = *p.HostPort
		}
	}
	if port == 0 {
		return nil
	}
	host, public := "", false
	if v4, v6, _ := s.publicAddresses(r.Context()); v4 != "" {
		host, public = v4, true
	} else if v6 != "" {
		host, public = "["+v6+"]", true
	}
	if host == "" {
		host = r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
	}
	// An allocation's alias (or its own IP) is the address players should use.
	if alloc, err := s.Store.ResourceAllocation(r.Context(), res.ID); err == nil {
		if alloc.Alias != "" {
			host, public = alloc.Alias, true
		} else if alloc.IP != "0.0.0.0" && alloc.IP != "::" {
			host, public = alloc.IP, true
		}
	}
	a := &gameAddress{Host: host, Port: port, Protocol: gp.Protocol, Default: port == gp.Preferred, Public: public}
	a.Display = host
	if !a.Default {
		a.Display = fmt.Sprintf("%s:%d", host, port)
	}
	return a
}

func (s *Server) getGame(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	res, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	gs, err := s.Store.GetGameServer(r.Context(), res.ID)
	if err != nil {
		return err
	}
	man := srv.Adapter.Manifest()
	edition, _ := games.EditionOf(srv.Adapter, srv.Spec.Edition)
	out := map[string]any{
		"game":        map[string]any{"id": man.ID, "name": man.Name, "sections": man.Sections, "slots": man.Slots, "memory": man.Memory, "fields": man.Fields},
		"edition":     edition,
		"editions":    man.Editions,
		"spec":        srv.Spec,
		"initialized": gs.Initialized,
		"running":     srv.Running(),
		"status":      nil,
		"address":     s.gameAddress(r, res, srv),
		"content":     games.ContentKinds(srv.Adapter, srv.Spec),
		"gameVersion": games.GameVersion(srv.Spec),
		"image":       res.Image,
		"quick":       []games.QuickCommand{},
		"console":     false,
		"replies":     false,
	}
	if c, ok := srv.Adapter.(games.Console); ok {
		out["console"] = true
		out["quick"] = c.QuickCommands(srv.Spec)
		_, out["replies"] = c.ConsoleCommand(srv.Spec, "list")
	}
	if srv.Running() {
		if st, ok := srv.Status(r.Context()); ok {
			out["status"] = st
		}
	}
	httpx.JSON(w, 200, out)
	return nil
}

// updateGame changes what runs: type, version, loader, Java, modpack or JAR. The world stays.
func (s *Server) updateGame(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	var in struct {
		Edition       *string        `json:"edition"`
		Version       *string        `json:"version"`
		LoaderVersion *string        `json:"loaderVersion"`
		Java          *int           `json:"java"`
		CustomURL     *string        `json:"customUrl"`
		Modpack       *games.Modpack `json:"modpack"`
		DisplayName   *string        `json:"displayName"`
		Deploy        bool           `json:"deploy"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	spec, before := srv.Spec, srv.Spec
	oldEdition, _ := games.EditionOf(srv.Adapter, spec.Edition)
	if in.Edition != nil {
		next, ok := games.EditionOf(srv.Adapter, *in.Edition)
		if !ok {
			return httpx.Invalid(map[string]string{"edition": "Choose a server type."})
		}
		if next.Group != oldEdition.Group {
			return httpx.Invalid(map[string]string{"edition": fmt.Sprintf("%s worlds can't run on %s. Create a new server instead.", oldEdition.Group, next.Group)})
		}
		if next.ID != spec.Edition {
			spec.LoaderVersion = "" // a Fabric loader version means nothing to Forge
		}
		spec.Edition = next.ID
	}
	if in.Version != nil {
		spec.Version = *in.Version
	}
	if in.LoaderVersion != nil {
		spec.LoaderVersion = *in.LoaderVersion
	}
	if in.Java != nil {
		spec.Java = *in.Java
	}
	if in.CustomURL != nil {
		spec.CustomURL = *in.CustomURL
	}
	if in.DisplayName != nil {
		spec.DisplayName = *in.DisplayName
	}
	if in.Modpack != nil {
		mp := *in.Modpack
		if err := resolveModpack(r.Context(), &mp); err != nil {
			return err
		}
		spec.Modpack = &mp
	}
	if f := srv.Adapter.Normalize(&spec); len(f) > 0 {
		return httpx.Invalid(f)
	}
	rt, err := srv.Adapter.Runtime(spec, res.MemoryMB, false)
	if err != nil {
		return httpx.Invalid(map[string]string{"version": err.Error()})
	}
	if err := s.Store.UpdateGameServer(r.Context(), res.ID, spec, rt.Image); err != nil {
		return err
	}
	edition, _ := games.EditionOf(srv.Adapter, spec.Edition)
	summary := edition.Name + " " + games.GameVersion(spec)
	s.Store.Audit(r.Context(), u, "game.update", "resource", res.ID, map[string]any{
		"from": oldEdition.Name + " " + games.GameVersion(before), "to": summary}, clientIP(r))
	out := map[string]any{"spec": spec, "image": rt.Image}
	if in.Deploy {
		depID, err := s.deployNow(r.Context(), res.ID, u, "Switched to "+strings.TrimSpace(summary))
		if err != nil {
			return err
		}
		out["deploymentId"] = depID
	}
	httpx.JSON(w, 200, out)
	return nil
}

// deployNow queues a deployment of the resource's current settings.
func (s *Server) deployNow(ctx context.Context, resourceID string, u *store.User, reason string) (string, error) {
	tx, err := s.Store.SQL(ctx).Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	depID, err := s.Store.CreateDeployment(ctx, tx, resourceID, "manual", "", "", reason, u.ID)
	if err != nil {
		return "", err
	}
	if _, err := jobs.Enqueue(ctx, tx, jobs.NewJob{Kind: jobs.KindDeploy, ResourceID: resourceID, DeploymentID: depID, CreatedBy: u.ID}); err != nil {
		return "", err
	}
	return depID, tx.Commit(ctx)
}

/* ---------------------------------- Console --------------------------------- */

func (s *Server) gameConsole(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	var in struct {
		Command string `json:"command"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	out, replies, err := srv.Command(r.Context(), in.Command)
	if err != nil {
		return gameErr(err)
	}
	s.Store.Audit(r.Context(), u, "game.console", "resource", res.ID, map[string]any{"command": strings.TrimSpace(in.Command)}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"output": out, "replies": replies, "at": time.Now().UTC()})
	return nil
}

// stopForMaintenance stops a running server (it saves on the way down) and returns a function that
// starts it again. Status follows, so the dashboard doesn't show a stale state.
func (s *Server) stopForMaintenance(ctx context.Context, res *store.Resource, srv *games.Server) (func(), error) {
	if !srv.Running() {
		return func() {}, nil
	}
	grace := 60 * time.Second
	if ds, err := s.Store.GetBuildSettings(ctx, res.ID); err == nil && ds.StopGrace > 0 {
		grace = time.Duration(ds.StopGrace) * time.Second
	}
	if err := s.Runtime.Stop(ctx, res.ContainerID, grace); err != nil {
		return nil, fmt.Errorf("couldn't stop the server: %v", err)
	}
	s.Store.SetStatus(ctx, res.ID, "stopped")
	container := srv.Container
	srv.Container = ""
	return func() {
		ctx := context.WithoutCancel(ctx)
		if err := s.Runtime.Start(ctx, container); err == nil {
			s.Store.SetStatus(ctx, res.ID, "running")
			now := time.Now()
			s.Store.SetRunningSince(ctx, res.ID, &now)
		}
	}, nil
}
