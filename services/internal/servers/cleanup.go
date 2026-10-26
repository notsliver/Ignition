package servers

import (
	"context"
	"fmt"
	"strings"

	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Docker cleanup is planned before it runs. The plan lists exactly which objects would go and
 * why each is safe, and what's deliberately kept. Execution re-plans and only removes objects that
 * are still eligible *and* were in the plan the operator confirmed, and every removal is
 * non-forcing, so Docker itself refuses anything that became busy in between.
 *
 * Never in scope: volumes (persistent data), running containers, containers IGNITION didn't
 * create, images a container uses, images a successful deployment ran (rollback targets),
 * Docker's built-in networks, and IGNITION's own networks.
 */

type CleanupOptions struct {
	Containers   bool `json:"containers"`   // stopped IGNITION-created containers that no resource uses
	Dangling     bool `json:"dangling"`     // untagged images no container uses
	UnusedImages bool `json:"unusedImages"` // also tagged images no container uses (re-pulled on next use)
	Networks     bool `json:"networks"`     // custom networks with no containers
	BuildCache   bool `json:"buildCache"`
}

func (o CleanupOptions) Any() bool {
	return o.Containers || o.Dangling || o.UnusedImages || o.Networks || o.BuildCache
}

type CleanupItem struct {
	Kind   string `json:"kind"` // container, image, network, buildcache
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"` // bytes freed if known, else -1
	Reason string `json:"reason"`
}

type CleanupPlan struct {
	Items     []CleanupItem `json:"items"`
	Protected []CleanupItem `json:"protected"` // notable things kept, with why
	Bytes     int64         `json:"bytes"`     // known reclaimable bytes
	Volumes   int           `json:"volumes"`   // never touched; reported so nobody wonders
}

var systemNetworks = map[string]bool{"bridge": true, "host": true, "none": true, "ingress": true, "docker_gwbridge": true}

// PlanCleanup inspects the engine and returns what a cleanup with these options would remove.
func PlanCleanup(ctx context.Context, dk *runtime.Docker, st *store.Store, srv *store.Server, o CleanupOptions) (CleanupPlan, error) {
	plan := CleanupPlan{Items: []CleanupItem{}, Protected: []CleanupItem{}}
	containers, err := dk.ListContainers(ctx)
	if err != nil {
		return plan, err
	}
	du, err := dk.DiskUsage(ctx)
	if err != nil {
		return plan, err
	}
	plan.Volumes = len(du.Volumes)
	current := map[string]bool{}
	retained := map[string]bool{}
	known := map[string]bool{}
	if st != nil {
		idx, err := st.ResourceIndex(ctx)
		if err != nil {
			return plan, err
		}
		for id, r := range idx {
			known[id] = true
			if r.ContainerID != "" {
				current[r.ContainerID] = true
			}
		}
		if retained, err = st.RetainedImageIDs(ctx, srv.ID); err != nil {
			return plan, err
		}
	}
	sizes := map[string]int64{}
	for _, c := range du.Containers {
		sizes[c.ID] = c.SizeRw
	}
	usedImages := map[string]bool{}
	usedNetworks := map[string]bool{}
	for _, c := range containers {
		usedImages[c.ImageID] = true
		for _, n := range c.Networks {
			usedNetworks[n.Network] = true
		}
		if !o.Containers || c.State == "running" || c.State == "paused" || c.State == "restarting" {
			continue
		}
		// Only containers of this install's own resources: another IGNITION sharing the Docker host
		// (or a resource deleted from the database) may still need its stopped containers.
		res := c.Labels["ignition.resource"]
		switch {
		case c.Labels["ignition.managed"] != "true" || res == "":
			// Not ours, or a helper (the worker removes stale helpers itself).
		case !known[res]:
			plan.Protected = append(plan.Protected, CleanupItem{"container", c.ID, c.Name, -1, "belongs to a resource this IGNITION doesn't know (another install, or deleted)"})
		case current[c.ID]:
			plan.Protected = append(plan.Protected, CleanupItem{"container", c.ID, c.Name, -1, "current container of a resource"})
		default:
			plan.Items = append(plan.Items, CleanupItem{"container", c.ID, c.Name, sizes[c.ID], "stopped container from an earlier deployment"})
		}
	}
	for _, img := range du.Images {
		name := shortID(img.ID)
		if len(img.Tags) > 0 {
			name = img.Tags[0]
		}
		inUse := usedImages[img.ID] || img.Containers > 0
		dangling := len(img.Tags) == 0
		size := img.Size
		if img.SharedSize > 0 {
			size -= img.SharedSize
		}
		switch {
		case inUse:
			if !dangling && (o.Dangling || o.UnusedImages) {
				plan.Protected = append(plan.Protected, CleanupItem{"image", img.ID, name, img.Size, "used by a container"})
			}
		case retained[img.ID]:
			if o.Dangling || o.UnusedImages {
				plan.Protected = append(plan.Protected, CleanupItem{"image", img.ID, name, img.Size, "a successful deployment ran it (kept for rollback)"})
			}
		case dangling && (o.Dangling || o.UnusedImages):
			plan.Items = append(plan.Items, CleanupItem{"image", img.ID, name, size, "untagged and unused"})
		case !dangling && o.UnusedImages:
			plan.Items = append(plan.Items, CleanupItem{"image", img.ID, name, size, "no container uses it; it'll be pulled again if needed"})
		}
	}
	if o.Networks {
		nets, err := dk.ListNetworks(ctx)
		if err != nil {
			return plan, err
		}
		instance := dk.Instance(ctx)
		for _, n := range nets {
			switch {
			case systemNetworks[n.Name]:
			case n.Name == "ignition", n.Labels["ignition.instance"] != "" && n.Labels["ignition.instance"] == instance && usedNetworks[n.Name]:
				plan.Protected = append(plan.Protected, CleanupItem{"network", n.ID, n.Name, -1, "IGNITION network"})
			case n.Labels["com.docker.compose.project"] != "" && n.Labels["com.docker.compose.project"] == instance:
				plan.Protected = append(plan.Protected, CleanupItem{"network", n.ID, n.Name, -1, "IGNITION's own compose network"})
			case usedNetworks[n.Name]:
			default:
				plan.Items = append(plan.Items, CleanupItem{"network", n.ID, n.Name, -1, "no containers attached"})
			}
		}
	}
	if o.BuildCache {
		var reclaim int64
		n := 0
		for _, b := range du.BuildCache {
			if !b.InUse && !b.Shared {
				reclaim += b.Size
				n++
			}
		}
		if n > 0 {
			plan.Items = append(plan.Items, CleanupItem{"buildcache", "build-cache", fmt.Sprintf("%d unused build cache entries", n), reclaim, "not used by any running build"})
		}
	}
	for _, it := range plan.Items {
		if it.Size > 0 {
			plan.Bytes += it.Size
		}
	}
	return plan, nil
}

type CleanupOutcome struct {
	Removed   []CleanupItem `json:"removed"`
	Failed    []CleanupItem `json:"failed"`  // Reason holds the error
	Skipped   []CleanupItem `json:"skipped"` // no longer eligible
	Reclaimed int64         `json:"reclaimed"`
}

// ExecuteCleanup removes the confirmed items that are still eligible now.
func ExecuteCleanup(ctx context.Context, dk *runtime.Docker, st *store.Store, srv *store.Server, o CleanupOptions, confirmed []CleanupItem, progress func(string)) (CleanupOutcome, error) {
	out := CleanupOutcome{Removed: []CleanupItem{}, Failed: []CleanupItem{}, Skipped: []CleanupItem{}}
	plan, err := PlanCleanup(ctx, dk, st, srv, o)
	if err != nil {
		return out, err
	}
	eligible := map[string]CleanupItem{}
	for _, it := range plan.Items {
		eligible[it.Kind+":"+it.ID] = it
	}
	for i, want := range confirmed {
		if progress != nil {
			progress(fmt.Sprintf("%d of %d: %s %s", i+1, len(confirmed), want.Kind, want.Name))
		}
		it, ok := eligible[want.Kind+":"+want.ID]
		if !ok {
			want.Reason = "no longer eligible (in use, or already gone)"
			out.Skipped = append(out.Skipped, want)
			continue
		}
		var err error
		switch it.Kind {
		case "container":
			err = dk.RemoveStoppedContainer(ctx, it.ID)
		case "image":
			err = dk.RemoveUnusedImage(ctx, it.ID)
		case "network":
			err = dk.RemoveNetwork(ctx, it.ID)
		case "buildcache":
			var n int64
			n, err = dk.PruneBuildCache(ctx)
			it.Size = n
		}
		if err != nil {
			it.Reason = strings.TrimPrefix(err.Error(), "docker: ")
			if runtime.IsConflict(err) {
				it.Reason = "Docker refused: it's in use (" + it.Reason + ")"
			}
			out.Failed = append(out.Failed, it)
			continue
		}
		if it.Size > 0 {
			out.Reclaimed += it.Size
		}
		out.Removed = append(out.Removed, it)
	}
	return out, nil
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
