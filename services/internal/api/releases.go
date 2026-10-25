package api

import (
	"net/http"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Releases are successful deployments. Each one keeps the exact image it ran (by ID) and its variables
 * (sealed), so rolling back runs exactly that release again: no rebuild, no pull of a tag that may have moved.
 */

type release struct {
	store.Deployment
	Available bool   `json:"available"`       // its image is still on this machine
	Why       string `json:"why,omitempty"`   // why it can't be restored, when it can't
	Image     string `json:"image,omitempty"` // what a rollback would run
}

// rollbackImage is what a rollback to d runs, or "" with the reason it can't.
func rollbackImage(res *store.Resource, d *store.Deployment) (string, string) {
	switch {
	case d.ImageID != "":
		return d.ImageID, ""
	case res.SourceType == "git" && d.ImageRef != "":
		return d.ImageRef, ""
	case res.SourceType == "image":
		return "", "Deployed before Ignition kept exact image IDs, so the same image can't be guaranteed. Redeploy pulls the tag again."
	}
	return "", "This deployment didn't produce an image."
}

func (s *Server) listReleases(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	all, err := s.Store.ListDeployments(r.Context(), id, 200)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	releases, rollbacks := []release{}, []store.Deployment{}
	for _, d := range all {
		if d.Trigger == "rollback" {
			rollbacks = append(rollbacks, d)
		}
		if d.Status != "successful" || len(releases) >= 30 {
			continue
		}
		rel := release{Deployment: d}
		img, why := rollbackImage(res, &d)
		rel.Image, rel.Why = img, why
		if img != "" {
			ok, seen := present[img]
			if !seen {
				iid, err := s.Runtime.ImageID(r.Context(), img)
				ok = err == nil && iid != ""
				present[img] = ok
			}
			rel.Available = ok
			if !ok {
				rel.Why = "Its image is no longer on this machine (old images were removed). Redeploy that commit from the source instead."
			}
		}
		releases = append(releases, rel)
	}
	if len(rollbacks) > 30 {
		rollbacks = rollbacks[:30]
	}
	httpx.JSON(w, 200, map[string]any{"releases": releases, "rollbacks": rollbacks, "sourceType": res.SourceType})
	return nil
}

// envDiff compares a release's variables with the current ones. Only names are returned, never values.
func (s *Server) envDiff(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Deployment")
	if err != nil {
		return err
	}
	d, err := s.Store.GetDeployment(r.Context(), id)
	if err != nil {
		return notFound(err, "Deployment")
	}
	then, err := s.Store.DeploymentEnv(r.Context(), id)
	if err != nil {
		return err
	}
	now, err := s.Store.ListEnv(r.Context(), d.ResourceID)
	if err != nil {
		return err
	}
	out := map[string]any{"snapshot": d.EnvKept, "added": []string{}, "removed": []string{}, "changed": []string{}}
	if !d.EnvKept {
		httpx.JSON(w, 200, out)
		return nil
	}
	open := func(v store.EnvVar) string {
		plain, err := s.Secrets.Open(v.Sealed, d.ResourceID+":"+v.Key)
		if err != nil {
			return "\x00unreadable"
		}
		return plain
	}
	current := map[string]string{}
	for _, v := range now {
		current[v.Key] = open(v)
	}
	var added, removed, changed []string
	seen := map[string]bool{}
	for _, v := range then {
		seen[v.Key] = true
		cur, ok := current[v.Key]
		switch {
		case !ok:
			added = append(added, v.Key) // restoring brings it back
		case cur != open(v):
			changed = append(changed, v.Key)
		}
	}
	for _, v := range now {
		if !seen[v.Key] {
			removed = append(removed, v.Key) // restoring removes it
		}
	}
	for k, list := range map[string][]string{"added": added, "removed": removed, "changed": changed} {
		if list != nil {
			out[k] = list
		}
	}
	httpx.JSON(w, 200, out)
	return nil
}
