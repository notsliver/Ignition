package api

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/worker"
)

/* ------------------------------ Resource Limits ----------------------------- */

func (s *Server) getLimits(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	lim, err := s.Store.GetLimits(r.Context(), id)
	if err != nil {
		return err
	}
	usage, err := s.Store.UsageSummary(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out := map[string]any{"limits": lim, "usage": usage}
	if host, err := s.Runtime.Host(ctx); err == nil {
		out["host"] = map[string]any{"cpus": host.CPUs, "memoryBytes": host.MemoryBytes}
	}
	if res.ContainerID != "" {
		if st, err := s.Runtime.Inspect(ctx, res.ContainerID); err == nil && st.Exists {
			live := map[string]any{"running": st.Running, "restartCount": st.RestartCount, "oomKilled": st.OOMKilled, "exitCode": st.ExitCode}
			if !st.FinishedAt.IsZero() && st.FinishedAt.Year() > 1 {
				live["finishedAt"] = st.FinishedAt
			}
			if st.Running {
				if stats, err := s.Runtime.Stats(ctx, res.ContainerID); err == nil {
					live["cpuPercent"], live["memoryBytes"] = stats.CPUPercent, stats.MemoryBytes
				}
			}
			out["live"] = live
		}
	}
	httpx.JSON(w, 200, out)
	return nil
}

func (s *Server) putLimits(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	var in store.Limits
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	f := map[string]string{}
	host, _ := s.Runtime.Host(ctx)
	if in.MemoryMB < 64 || in.MemoryMB > 65536 {
		f["memoryMb"] = "Memory must be between 64 MB and 64 GB."
	} else if host.MemoryBytes > 0 && int64(in.MemoryMB)<<20 > host.MemoryBytes {
		f["memoryMb"] = fmt.Sprintf("Docker on this machine has %d MB in total.", host.MemoryBytes>>20)
	}
	if in.MemoryReservationMB != nil && (*in.MemoryReservationMB < 16 || *in.MemoryReservationMB > in.MemoryMB) {
		f["memoryReservationMb"] = "The reservation must be at least 16 MB and no more than the memory limit."
	}
	if in.CPUs < 0.1 || in.CPUs > 64 {
		f["cpus"] = "CPU must be between 0.1 and 64."
	} else if host.CPUs > 0 && in.CPUs > float64(host.CPUs) {
		f["cpus"] = fmt.Sprintf("Docker on this machine has %d CPUs.", host.CPUs)
	}
	if in.PidsLimit != nil && (*in.PidsLimit < 16 || *in.PidsLimit > 100000) {
		f["pidsLimit"] = "Between 16 and 100000, or empty for no limit."
	}
	if !slices.Contains([]string{"always", "on-failure", "never"}, in.RestartPolicy) {
		f["restartPolicy"] = "Choose when to restart."
	}
	if in.MaxRestarts < 1 || in.MaxRestarts > 100 {
		in.MaxRestarts = 5
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	if err := s.Store.SetLimits(r.Context(), id, in); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "resource.limits", "resource", id, map[string]any{"memoryMb": in.MemoryMB, "cpus": in.CPUs}, clientIP(r))
	out := map[string]any{"limits": in, "applied": false}
	// Docker changes these on the running container: no restart, no redeploy.
	if res.ContainerID != "" {
		if err := s.Runtime.UpdateLimits(ctx, res.ContainerID, worker.RuntimeLimits(in)); err != nil {
			msg := err.Error()
			if strings.Contains(msg, "Memory limit should be smaller than already set memoryswap") || strings.Contains(msg, "cannot be less than") {
				msg = "Docker refused the change while the container runs (" + msg + "). It applies on the next deployment."
			}
			out["applyError"] = msg
		} else {
			out["applied"] = true
		}
	}
	httpx.JSON(w, 200, out)
	return nil
}

/* ---------------------------------- Storage --------------------------------- */

var volumeNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// Paths that would break the container or the host's view of it if a volume covered them.
var reservedMounts = []string{"/", "/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc", "/sbin", "/sys", "/usr", "/run"}

func (s *Server) getStorage(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	vols, err := s.Store.Volumes(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	docker, dockerErr := s.Runtime.Volumes(ctx, worker.VolumePrefix(id))
	mounted := map[string]string{} // docker volume -> where it's mounted now
	if res.ContainerID != "" {
		if st, err := s.Runtime.Inspect(ctx, res.ContainerID); err == nil {
			for _, m := range st.Mounts {
				mounted[m.Volume] = m.Destination
			}
		}
	}
	type volView struct {
		Name      string `json:"name"`
		MountPath string `json:"mountPath"`
		Docker    string `json:"docker"`
		SizeBytes *int64 `json:"sizeBytes"` // nil: not created yet
		Mounted   string `json:"mounted"`   // where the running container has it ("" = not mounted yet)
		InUse     bool   `json:"inUse"`     // some container (e.g. the previous release, shutting down) still uses it
	}
	sizes, inUse := map[string]int64{}, map[string]bool{}
	for _, v := range docker {
		sizes[v.Name], inUse[v.Name] = v.SizeBytes, v.InUse
	}
	views := []volView{}
	configured := map[string]bool{}
	for _, v := range vols {
		dn := worker.VolumeName(id, v.Name)
		configured[dn] = true
		vv := volView{Name: v.Name, MountPath: v.MountPath, Docker: dn, Mounted: mounted[dn]}
		if size, ok := sizes[dn]; ok {
			vv.SizeBytes = &size
		}
		views = append(views, vv)
	}
	detached := []volView{}
	for _, v := range docker {
		if !configured[v.Name] {
			size := v.SizeBytes
			detached = append(detached, volView{Name: strings.TrimPrefix(v.Name, worker.VolumePrefix(id)), Docker: v.Name, SizeBytes: &size, Mounted: mounted[v.Name], InUse: inUse[v.Name]})
		}
	}
	out := map[string]any{"volumes": views, "detached": detached, "running": res.ContainerID != "" && res.Status == "running"}
	if dockerErr != nil {
		out["dockerError"] = dockerErr.Error()
	}
	httpx.JSON(w, 200, out)
	return nil
}

func (s *Server) putStorage(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	var in struct {
		Volumes []store.Volume `json:"volumes"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	var out []store.Volume
	names, paths := map[string]bool{}, map[string]bool{}
	if len(in.Volumes) > 10 {
		f["volumes"] = "Up to 10 volumes."
	}
	for i, v := range in.Volumes {
		key := func(field string) string { return fmt.Sprintf("volumes.%d.%s", i, field) }
		v.Name = strings.ToLower(strings.TrimSpace(v.Name))
		raw := strings.TrimSpace(v.MountPath)
		v.MountPath = path.Clean(raw)
		switch {
		case !volumeNameRE.MatchString(v.Name):
			f[key("name")] = "Use lowercase letters, numbers and dashes (up to 32)."
		case names[v.Name]:
			f[key("name")] = "Each volume needs its own name."
		}
		switch {
		case !strings.HasPrefix(raw, "/"):
			f[key("mountPath")] = "Mount paths are absolute, like /app/data."
		case slices.Contains(reservedMounts, v.MountPath):
			f[key("mountPath")] = v.MountPath + " is a system folder; mounting a volume there would break the container."
		case paths[v.MountPath]:
			f[key("mountPath")] = "Another volume is already mounted there."
		default:
			for p := range paths {
				if strings.HasPrefix(v.MountPath+"/", p+"/") || strings.HasPrefix(p+"/", v.MountPath+"/") {
					f[key("mountPath")] = fmt.Sprintf("Overlaps with the volume at %s. Pick a separate folder.", p)
				}
			}
		}
		names[v.Name], paths[v.MountPath] = true, true
		out = append(out, v)
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	if err := s.Store.SetVolumes(r.Context(), id, out); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "resource.storage", "resource", id, map[string]any{"volumes": len(out)}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"requiresRedeploy": res.ContainerID != ""})
	return nil
}

// volumeFor resolves a Docker volume name and checks it belongs to the resource.
func (s *Server) volumeFor(r *http.Request) (string, *store.Resource, error) {
	id, err := pathID(r, "Resource")
	if err != nil {
		return "", nil, err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return "", nil, notFound(err, "Resource")
	}
	name := r.PathValue("volume")
	if !strings.HasPrefix(name, worker.VolumePrefix(id)) || !volumeNameRE.MatchString(strings.TrimPrefix(name, worker.VolumePrefix(id))) {
		return "", nil, httpx.NotFound("Volume")
	}
	return name, res, nil
}

func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request, u *store.User) error {
	name, res, err := s.volumeFor(r)
	if err != nil {
		return err
	}
	vols, _ := s.Store.Volumes(r.Context(), res.ID)
	for _, v := range vols {
		if worker.VolumeName(res.ID, v.Name) == name {
			return httpx.Conflict("This volume is still configured. Remove it from the list and redeploy first.")
		}
	}
	if err := s.Runtime.RemoveVolume(r.Context(), name); err != nil {
		if strings.Contains(err.Error(), "in use") {
			return httpx.Conflict("The running container still uses this volume. Redeploy so it's detached, then delete it.")
		}
		return err
	}
	s.Store.Audit(r.Context(), u, "resource.volume_delete", "resource", res.ID, map[string]any{"volume": name}, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}

// backupVolume streams the volume's files as a .tar.gz download. Audited, since it exports data.
func (s *Server) backupVolume(w http.ResponseWriter, r *http.Request, u *store.User) error {
	name, res, err := s.volumeFor(r)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	tarball, err := s.Runtime.ExportVolume(ctx, name)
	if err != nil {
		if strings.Contains(err.Error(), "No such volume") || strings.Contains(err.Error(), "not found") {
			return httpx.NotFound("Volume (it's created on the first deployment)")
		}
		return err
	}
	defer tarball.Close()
	s.Store.Audit(r.Context(), u, "resource.volume_backup", "resource", res.ID, map[string]any{"volume": name}, clientIP(r))
	file := fmt.Sprintf("%s-%s-%s.tar.gz", res.Name, strings.TrimPrefix(name, worker.VolumePrefix(res.ID)), time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`"`)
	w.Header().Set("Cache-Control", "no-store")
	gz := gzip.NewWriter(w)
	if _, err := io.Copy(gz, tarball); err != nil {
		return nil // headers are sent; the client sees a truncated download
	}
	gz.Close()
	return nil
}
