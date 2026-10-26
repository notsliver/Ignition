package runtime

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

/*
 * Server-level inventory: what a Docker Engine reports about itself and everything on it.
 * These calls only read, except the narrowly scoped removals at the bottom, which never force:
 * Docker itself refuses to remove a running container, an image a container uses, or a network
 * with endpoints, so a stale preview can't destroy something that became busy in the meantime.
 */

// NewDockerTLS talks to a remote Docker Engine over mutual TLS (dockerd --tlsverify).
// cfg must pin the engine's CA and carry the client certificate; there is no insecure fallback.
func NewDockerTLS(endpoint string, cfg *tls.Config, timeout time.Duration) *Docker {
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: timeout}).DialContext,
		TLSClientConfig:     cfg,
		TLSHandshakeTimeout: timeout,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Docker{client: &http.Client{Transport: transport}, base: "https://" + endpoint + "/" + apiVersion, root: "https://" + endpoint}
}

// MinAPIVersion is the oldest Engine API IGNITION supports.
const MinAPIVersion = "1.44"

type EngineInfo struct {
	Name            string    `json:"name"` // the engine's hostname
	Version         string    `json:"version"`
	APIVersion      string    `json:"apiVersion"`
	MinAPIVersion   string    `json:"minApiVersion"`
	OS              string    `json:"os"` // e.g. "Ubuntu 24.04 LTS"
	OSType          string    `json:"osType"`
	Arch            string    `json:"arch"`
	Kernel          string    `json:"kernel"`
	StorageDriver   string    `json:"storageDriver"`
	DockerRootDir   string    `json:"dockerRootDir"`
	CgroupDriver    string    `json:"cgroupDriver"`
	CgroupVersion   string    `json:"cgroupVersion"`
	NCPU            int       `json:"ncpu"`
	MemTotal        int64     `json:"memTotal"`
	Containers      int       `json:"containers"`
	Running         int       `json:"running"`
	Paused          int       `json:"paused"`
	Stopped         int       `json:"stopped"`
	Images          int       `json:"images"`
	SystemTime      time.Time `json:"systemTime"`
	SecurityOptions []string  `json:"securityOptions"`
	Warnings        []string  `json:"warnings"`
	Rootless        bool      `json:"rootless"`
}

// EngineInfo asks the engine about itself. /version is read unversioned so an engine too old for
// IGNITION still says which version it is, instead of failing with "client version too new".
func (d *Docker) EngineInfo(ctx context.Context) (EngineInfo, error) {
	var v struct {
		Version, ApiVersion, MinAPIVersion, Os, Arch, KernelVersion string
	}
	if err := d.rawJSON(ctx, "/version", &v); err != nil {
		return EngineInfo{}, err
	}
	out := EngineInfo{Version: v.Version, APIVersion: v.ApiVersion, MinAPIVersion: v.MinAPIVersion, OSType: v.Os, Arch: v.Arch, Kernel: v.KernelVersion}
	var info struct {
		Name, OperatingSystem, Driver, DockerRootDir, CgroupDriver, CgroupVersion, SystemTime string
		NCPU                                                                                  int
		MemTotal                                                                              int64
		Containers, ContainersRunning, ContainersPaused, ContainersStopped, Images            int
		SecurityOptions, Warnings                                                             []string
	}
	if err := d.rawJSON(ctx, "/info", &info); err != nil {
		return out, err
	}
	out.Name, out.OS, out.StorageDriver, out.DockerRootDir = info.Name, info.OperatingSystem, info.Driver, info.DockerRootDir
	out.CgroupDriver, out.CgroupVersion, out.NCPU, out.MemTotal = info.CgroupDriver, info.CgroupVersion, info.NCPU, info.MemTotal
	out.Containers, out.Running, out.Paused, out.Stopped, out.Images = info.Containers, info.ContainersRunning, info.ContainersPaused, info.ContainersStopped, info.Images
	out.SecurityOptions, out.Warnings = info.SecurityOptions, info.Warnings
	out.SystemTime, _ = time.Parse(time.RFC3339Nano, info.SystemTime)
	for _, o := range info.SecurityOptions {
		if strings.Contains(o, "rootless") {
			out.Rootless = true
		}
	}
	return out, nil
}

// APICompatible reports whether the engine speaks an API version IGNITION can use.
func APICompatible(version string) bool { return versionAtLeast(version, MinAPIVersion) }

func versionAtLeast(have, want string) bool {
	var hMaj, hMin, wMaj, wMin int
	if _, err := fmt.Sscanf(have, "%d.%d", &hMaj, &hMin); err != nil {
		return false
	}
	fmt.Sscanf(want, "%d.%d", &wMaj, &wMin)
	return hMaj > wMaj || hMaj == wMaj && hMin >= wMin
}

func (d *Docker) rawJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", d.root+path, nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("docker unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var msg struct{ Message string }
		json.NewDecoder(resp.Body).Decode(&msg)
		return &apiError{Status: resp.StatusCode, Message: msg.Message}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type MountInfo struct {
	Type        string `json:"type"` // volume, bind, tmpfs
	Name        string `json:"name"` // volume name
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Driver      string `json:"driver"`
	ReadOnly    bool   `json:"readOnly"`
}

type ContainerSummary struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Image    string            `json:"image"`
	ImageID  string            `json:"imageId"`
	State    string            `json:"state"`  // created, running, paused, restarting, exited, dead
	Status   string            `json:"status"` // Docker's human text, e.g. "Up 3 hours (healthy)"
	Created  time.Time         `json:"created"`
	Labels   map[string]string `json:"labels"`
	Ports    []PortMapping     `json:"ports"`
	Mounts   []MountInfo       `json:"mounts"`
	Networks []Attachment      `json:"networks"`
	SizeRw   int64             `json:"sizeRw"`
}

// ListContainers returns every container, running or not.
func (d *Docker) ListContainers(ctx context.Context) ([]ContainerSummary, error) {
	var raw []struct {
		Id, Image, ImageID, State, Status string
		Names                             []string
		Created                           int64
		Labels                            map[string]string
		Ports                             []struct {
			IP          string
			PrivatePort int
			PublicPort  int
			Type        string
		}
		Mounts []struct {
			Type, Name, Source, Destination, Driver string
			RW                                      bool
		}
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string
				Aliases   []string
			}
		}
	}
	if err := d.doJSON(ctx, "GET", "/containers/json", url.Values{"all": {"1"}}, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]ContainerSummary, 0, len(raw))
	for _, c := range raw {
		s := ContainerSummary{ID: c.Id, Image: c.Image, ImageID: c.ImageID, State: c.State, Status: c.Status, Created: time.Unix(c.Created, 0).UTC(), Labels: c.Labels}
		if len(c.Names) > 0 {
			s.Name = strings.TrimPrefix(c.Names[0], "/")
		}
		for _, p := range c.Ports {
			s.Ports = append(s.Ports, PortMapping{HostIP: p.IP, HostPort: p.PublicPort, ContainerPort: p.PrivatePort, Protocol: protocolOr(p.Type)})
		}
		for _, m := range c.Mounts {
			s.Mounts = append(s.Mounts, MountInfo{Type: m.Type, Name: m.Name, Source: m.Source, Destination: m.Destination, Driver: m.Driver, ReadOnly: !m.RW})
		}
		for name, n := range c.NetworkSettings.Networks {
			s.Networks = append(s.Networks, Attachment{Network: name, IP: n.IPAddress, Aliases: n.Aliases})
		}
		sort.Slice(s.Networks, func(i, j int) bool { return s.Networks[i].Network < s.Networks[j].Network })
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

type ImageSummary struct {
	ID         string            `json:"id"`
	Tags       []string          `json:"tags"`
	Digests    []string          `json:"digests"`
	Size       int64             `json:"size"`
	SharedSize int64             `json:"sharedSize"` // -1 when unknown
	Created    time.Time         `json:"created"`
	Containers int               `json:"containers"` // -1 when unknown
	Labels     map[string]string `json:"labels"`
}

type VolumeSummary struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Mountpoint string            `json:"mountpoint"`
	Scope      string            `json:"scope"`
	Labels     map[string]string `json:"labels"`
	Size       int64             `json:"size"`     // -1 when Docker hasn't measured it
	RefCount   int64             `json:"refCount"` // -1 when unknown
	CreatedAt  string            `json:"createdAt"`
}

type BuildCacheEntry struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	Description string     `json:"description"`
	Size        int64      `json:"size"`
	InUse       bool       `json:"inUse"`
	Shared      bool       `json:"shared"`
	LastUsedAt  *time.Time `json:"lastUsedAt"`
}

type DiskUsage struct {
	LayersSize int64             `json:"layersSize"`
	Images     []ImageSummary    `json:"images"`
	Volumes    []VolumeSummary   `json:"volumes"`
	BuildCache []BuildCacheEntry `json:"buildCache"`
	Containers []struct {
		ID     string `json:"id"`
		SizeRw int64  `json:"sizeRw"`
	} `json:"containers"`
}

// DiskUsage is `docker system df -v`: sizes Docker has measured, never estimates.
func (d *Docker) DiskUsage(ctx context.Context) (DiskUsage, error) {
	var raw struct {
		LayersSize int64
		Images     []struct {
			Id                           string
			RepoTags, RepoDigests        []string
			Size, SharedSize, Containers int64
			Created                      int64
			Labels                       map[string]string
		}
		Containers []struct {
			Id     string
			SizeRw int64
		}
		Volumes []struct {
			Name, Driver, Mountpoint, Scope, CreatedAt string
			Labels                                     map[string]string
			UsageData                                  *struct{ Size, RefCount int64 }
		}
		BuildCache []struct {
			ID, Type, Description string
			Size                  int64
			InUse, Shared         bool
			LastUsedAt            *time.Time
		}
	}
	if err := d.doJSON(ctx, "GET", "/system/df", nil, nil, &raw); err != nil {
		return DiskUsage{}, err
	}
	out := DiskUsage{LayersSize: raw.LayersSize}
	for _, i := range raw.Images {
		out.Images = append(out.Images, ImageSummary{ID: i.Id, Tags: noneFiltered(i.RepoTags), Digests: noneFiltered(i.RepoDigests), Size: i.Size, SharedSize: i.SharedSize,
			Created: time.Unix(i.Created, 0).UTC(), Containers: int(i.Containers), Labels: i.Labels})
	}
	for _, v := range raw.Volumes {
		vs := VolumeSummary{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, Scope: v.Scope, Labels: v.Labels, CreatedAt: v.CreatedAt, Size: -1, RefCount: -1}
		if v.UsageData != nil {
			vs.Size, vs.RefCount = v.UsageData.Size, v.UsageData.RefCount
		}
		out.Volumes = append(out.Volumes, vs)
	}
	for _, b := range raw.BuildCache {
		out.BuildCache = append(out.BuildCache, BuildCacheEntry{ID: b.ID, Type: b.Type, Description: b.Description, Size: b.Size, InUse: b.InUse, Shared: b.Shared, LastUsedAt: b.LastUsedAt})
	}
	for _, c := range raw.Containers {
		out.Containers = append(out.Containers, struct {
			ID     string `json:"id"`
			SizeRw int64  `json:"sizeRw"`
		}{c.Id, c.SizeRw})
	}
	sort.Slice(out.Images, func(i, j int) bool { return out.Images[i].Created.After(out.Images[j].Created) })
	sort.Slice(out.Volumes, func(i, j int) bool { return out.Volumes[i].Name < out.Volumes[j].Name })
	return out, nil
}

func noneFiltered(list []string) []string {
	out := []string{}
	for _, s := range list {
		if s != "<none>:<none>" && s != "<none>@<none>" {
			out = append(out, s)
		}
	}
	return out
}

type NetworkDetail struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Scope      string            `json:"scope"`
	Internal   bool              `json:"internal"`
	Attachable bool              `json:"attachable"`
	IPv6       bool              `json:"ipv6"`
	Labels     map[string]string `json:"labels"`
	Subnets    []string          `json:"subnets"`
	Gateways   []string          `json:"gateways"`
	Created    time.Time         `json:"created"`
}

func (d *Docker) ListNetworks(ctx context.Context) ([]NetworkDetail, error) {
	var raw []struct {
		Id, Name, Driver, Scope string
		Internal, Attachable    bool
		EnableIPv6              bool
		Labels                  map[string]string
		Created                 time.Time
		IPAM                    struct {
			Config []struct{ Subnet, Gateway string }
		}
	}
	if err := d.doJSON(ctx, "GET", "/networks", nil, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]NetworkDetail, 0, len(raw))
	for _, n := range raw {
		nd := NetworkDetail{ID: n.Id, Name: n.Name, Driver: n.Driver, Scope: n.Scope, Internal: n.Internal, Attachable: n.Attachable, IPv6: n.EnableIPv6, Labels: n.Labels, Created: n.Created,
			Subnets: []string{}, Gateways: []string{}}
		for _, c := range n.IPAM.Config {
			if c.Subnet != "" {
				nd.Subnets = append(nd.Subnets, c.Subnet)
			}
			if c.Gateway != "" {
				nd.Gateways = append(nd.Gateways, c.Gateway)
			}
		}
		out = append(out, nd)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// InspectObject returns Docker's raw inspect document for a container, image, network or volume.
// Container environment values are masked: they routinely hold passwords and tokens.
func (d *Docker) InspectObject(ctx context.Context, kind, id string) (map[string]any, error) {
	paths := map[string]string{"container": "/containers/%s/json", "image": "/images/%s/json", "network": "/networks/%s", "volume": "/volumes/%s"}
	p, ok := paths[kind]
	if !ok {
		return nil, errors.New("unknown object kind")
	}
	var out map[string]any
	if err := d.doJSON(ctx, "GET", fmt.Sprintf(p, url.PathEscape(id)), nil, nil, &out); err != nil {
		return nil, err
	}
	if cfg, ok := out["Config"].(map[string]any); ok {
		if env, ok := cfg["Env"].([]any); ok {
			for i, e := range env {
				if s, ok := e.(string); ok {
					k, _, _ := strings.Cut(s, "=")
					env[i] = k + "=••••••"
				}
			}
		}
	}
	return out, nil
}

// RemoveStoppedContainer deletes a container only if it isn't running. Its volumes are kept.
func (d *Docker) RemoveStoppedContainer(ctx context.Context, id string) error {
	err := d.doJSON(ctx, "DELETE", "/containers/"+url.PathEscape(id), nil, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// RemoveUnusedImage deletes an image only if no container (running or stopped) uses it.
func (d *Docker) RemoveUnusedImage(ctx context.Context, id string) error {
	err := d.doJSON(ctx, "DELETE", "/images/"+url.PathEscape(id), url.Values{"noprune": {"0"}}, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// PruneBuildCache removes build cache that no build is using and returns the bytes reclaimed.
func (d *Docker) PruneBuildCache(ctx context.Context) (int64, error) {
	var out struct{ SpaceReclaimed int64 }
	err := d.doJSON(ctx, "POST", "/build/prune", nil, nil, &out)
	return out.SpaceReclaimed, err
}

// IsConflict reports Docker refusing an operation because the object is in use.
func IsConflict(err error) bool {
	var e *apiError
	return errors.As(err, &e) && e.Status == http.StatusConflict
}

// IsLocal reports whether this client uses the local socket.
func (d *Docker) IsLocal() bool { return d.socket != "" }

// SocketPath is the local socket this client uses, or "".
func (d *Docker) SocketPath() string { return d.socket }
