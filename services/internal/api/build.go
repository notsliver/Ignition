package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/sources"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/worker"
)

var (
	buildArgRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	targetRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
)

func (s *Server) getBuild(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	bs, err := s.Store.GetBuildSettings(r.Context(), id)
	if err != nil {
		return err
	}
	last, _ := s.Store.LastBuild(r.Context(), id)
	httpx.JSON(w, 200, map[string]any{"settings": bs, "builds": res.SourceType == "git", "lastBuild": last})
	return nil
}

func (s *Server) putBuild(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	var in store.BuildSettings
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	in.Dockerfile, in.Context, in.Target = strings.TrimSpace(in.Dockerfile), strings.Trim(strings.TrimSpace(in.Context), "/"), strings.TrimSpace(in.Target)
	in.Command, in.ReleaseCommand = strings.TrimSpace(in.Command), strings.TrimSpace(in.ReleaseCommand)
	if in.Mode != "auto" && in.Mode != "dockerfile" {
		f["mode"] = "Choose Automatic or Dockerfile."
	}
	if in.Dockerfile == "" {
		in.Dockerfile = "Dockerfile"
	}
	if _, err := worker.SafeDockerfile(in.Dockerfile); err != nil {
		f["dockerfile"] = "The Dockerfile path must stay inside the build context."
	}
	if in.Context == "." {
		in.Context = ""
	}
	if in.Context != "" {
		if _, err := worker.SafeDockerfile(in.Context); err != nil {
			f["context"] = "The build context must be a folder inside the repository."
		}
	}
	if in.Target != "" && !targetRE.MatchString(in.Target) {
		f["target"] = "Use the stage name from the Dockerfile (letters, numbers, dots, dashes)."
	}
	args := []string{}
	for _, a := range in.Args {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if !buildArgRE.MatchString(a) {
			f["args"] = fmt.Sprintf("%q must look like NAME=value.", a)
		}
		args = append(args, a)
	}
	in.Args = args
	if len(args) > 50 {
		f["args"] = "Up to 50 build arguments."
	}
	if in.TimeoutMinutes < 1 || in.TimeoutMinutes > 120 {
		f["timeoutMinutes"] = "Between 1 and 120 minutes."
	}
	if in.Port != nil && (*in.Port < 1 || *in.Port > 65535) {
		f["port"] = "Ports are numbers from 1 to 65535."
	}
	if in.Port == nil && res.Type == "application" {
		f["port"] = "Applications need the port they listen on."
	}
	if in.HealthSeconds < 2 || in.HealthSeconds > 300 {
		f["healthSeconds"] = "Between 2 and 300 seconds."
	}
	if in.StopGrace < 0 || in.StopGrace > 300 {
		f["stopGraceSeconds"] = "Between 0 and 300 seconds."
	}
	if len(in.Command) > 2000 || len(in.ReleaseCommand) > 2000 {
		f["command"] = "Commands are limited to 2000 characters."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	before, _ := s.Store.GetBuildSettings(r.Context(), id)
	if err := s.Store.SetBuildSettings(r.Context(), id, in); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "resource.build_settings", "resource", id, nil, clientIP(r))
	rebuild := res.SourceType == "git" && (before.Mode != in.Mode || before.Dockerfile != in.Dockerfile || before.Context != in.Context ||
		before.Target != in.Target || strings.Join(before.Args, "\n") != strings.Join(in.Args, "\n"))
	httpx.JSON(w, 200, map[string]any{"settings": in, "requiresRebuild": rebuild})
	return nil
}

// inspectBuild clones the repository (shallow) and reports what the next build will use, without building.
func (s *Server) inspectBuild(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	if res.SourceType != "git" {
		return httpx.Invalid(map[string]string{"source": "This resource runs an image; there's nothing to build."})
	}
	var in store.BuildSettings // unsaved settings from the form
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "ignition-inspect-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	creds, err := s.resourceCredentials(ctx, res)
	if err != nil {
		return err
	}
	commit, err := worker.CloneRepo(ctx, res.RepoURL, res.Branch, dir, creds)
	if err != nil {
		return httpx.Invalid(map[string]string{"source": strings.TrimPrefix(err.Error(), "permanent failure: ")})
	}
	contextDir := dir
	if c := strings.Trim(in.Context, "/. "); c != "" {
		sub, err := worker.SafeDockerfile(c)
		if err != nil {
			return httpx.Invalid(map[string]string{"context": "The build context must be a folder inside the repository."})
		}
		contextDir = filepath.Join(dir, filepath.FromSlash(sub))
		if info, err := os.Stat(contextDir); err != nil || !info.IsDir() {
			return httpx.Invalid(map[string]string{"context": sub + " isn't a folder in the repository."})
		}
	}
	name := strings.TrimSpace(in.Dockerfile)
	if name == "" {
		name = "Dockerfile"
	}
	out := map[string]any{"commit": commit, "branch": res.Branch}
	if _, err := os.Stat(filepath.Join(contextDir, filepath.FromSlash(name))); err == nil {
		out["uses"], out["dockerfile"] = "dockerfile", name
	} else if in.Mode == "dockerfile" {
		out["uses"], out["error"] = "none", name+" isn't in the build context. Add it, or switch the builder to Automatic."
	} else {
		port := 0
		if in.Port != nil {
			port = *in.Port
		}
		plan, err := worker.DetectStack(contextDir, port, strings.TrimSpace(in.Command) != "")
		if err != nil {
			out["uses"], out["error"] = "none", err.Error()
		} else {
			out["uses"], out["plan"] = "detected", plan
		}
	}
	httpx.JSON(w, 200, out)
	return nil
}

// resourceCredentials decrypts the resource's linked source, if any.
func (s *Server) resourceCredentials(ctx context.Context, res *store.Resource) (*sources.Credentials, error) {
	if res.SourceID == nil {
		return nil, nil
	}
	src, err := s.Store.GetSource(ctx, *res.SourceID)
	if err != nil {
		return nil, err
	}
	c, err := s.credentials(src)
	if err != nil {
		return nil, err
	}
	return &c, nil
}
