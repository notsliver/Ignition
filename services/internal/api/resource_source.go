package api

import (
	"context"
	"net/http"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/sources"
	"github.com/notsliver/Ignition/services/internal/store"
)

// resourceSource reports what the resource is built from: the linked credentials, the remote's branches
// and latest commit (read live with git ls-remote), and what the current deployment is running.
// A remote that can't be read is reported in "error" rather than failing the request.
func (s *Server) resourceSource(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	out := map[string]any{"source": nil, "branches": []sources.Branch{}, "latestCommit": "", "deployed": nil, "error": ""}
	var deployed struct {
		Commit   string    `json:"commit"`
		ImageRef string    `json:"imageRef"`
		Branch   string    `json:"branch"`
		At       time.Time `json:"at"`
	}
	if s.Store.DB.QueryRow(r.Context(), `
		SELECT d.commit_sha, d.image_ref, d.branch, COALESCE(d.finished_at, d.created_at) FROM resources r
		JOIN deployments d ON d.id = r.current_deployment_id WHERE r.id = $1`, id).Scan(&deployed.Commit, &deployed.ImageRef, &deployed.Branch, &deployed.At) == nil {
		out["deployed"] = deployed
	}
	creds := sources.Credentials{AuthType: "none"}
	if res.SourceID != nil {
		src, err := s.Store.GetSource(r.Context(), *res.SourceID)
		if err == nil {
			out["source"] = map[string]any{"id": src.ID, "name": src.Name, "provider": src.Provider, "authType": src.AuthType, "status": src.Status, "statusMessage": src.StatusMessage}
			if creds, err = s.credentials(src); err != nil {
				out["error"] = err.Error()
			}
		}
	}
	if res.SourceType == "git" && out["error"] == "" {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		branches, err := sources.Branches(ctx, creds, res.RepoURL)
		if err != nil {
			out["error"] = err.Error()
		} else {
			// Reading the repository is a real check of the credentials, so the source shows it too.
			if src, ok := out["source"].(map[string]any); ok && src["status"] != "connected" {
				s.Store.DB.Exec(r.Context(), `UPDATE sources SET status = 'connected', status_message = $2, last_checked_at = now() WHERE id = $1`, *res.SourceID, "Connected (read "+res.RepoURL+")")
				src["status"], src["statusMessage"] = "connected", ""
			}
			out["branches"] = branches
			for _, b := range branches {
				if b.Name == res.Branch {
					out["latestCommit"] = b.Commit
				}
			}
			if out["latestCommit"] == "" {
				out["error"] = "Branch " + res.Branch + " doesn't exist in the repository."
			}
		}
	}
	httpx.JSON(w, 200, out)
	return nil
}
