package api

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/games"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

func createTemp() (*os.File, error) { return os.CreateTemp("", "ignition-upload-*") }
func removeFile(name string)        { os.Remove(name) }

func attachment(w http.ResponseWriter, name, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(name)+`"; filename*=UTF-8''`+url.PathEscape(name))
	w.Header().Set("Cache-Control", "no-store")
}

/* -------------------------------- Properties -------------------------------- */

func (s *Server) readProps(ctx context.Context, srv games.Server) *games.Properties {
	ps, ok := srv.Adapter.(games.PropertySupport)
	if !ok {
		return nil
	}
	raw, err := srv.Data.Read(ctx, ps.Properties(srv.Spec).File, 1<<20)
	if err != nil {
		return nil
	}
	return games.ParseProperties(string(raw))
}

func (s *Server) gameProperties(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	_, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	ps, ok := srv.Adapter.(games.PropertySupport)
	if !ok {
		return httpx.Unsupported("This game has no settings file.")
	}
	cfg := ps.Properties(srv.Spec)
	raw, err := srv.Data.Read(r.Context(), cfg.File, 1<<20)
	exists := err == nil
	if err != nil && !errors.Is(err, games.ErrNotFound) {
		return gameErr(err)
	}
	p := games.ParseProperties(string(raw))
	httpx.JSON(w, 200, map[string]any{"config": cfg, "values": p.Values(), "keys": p.Keys(), "raw": string(raw), "exists": exists, "running": srv.Running()})
	return nil
}

func (s *Server) putGameProperties(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	ps, ok := srv.Adapter.(games.PropertySupport)
	if !ok {
		return httpx.Unsupported("This game has no settings file.")
	}
	var in struct {
		Values map[string]string `json:"values"`
		Raw    *string           `json:"raw"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	cfg := ps.Properties(srv.Spec)
	current, err := srv.Data.Read(r.Context(), cfg.File, 1<<20)
	if errors.Is(err, games.ErrNotFound) {
		return httpx.Conflict("The settings file doesn't exist yet: the server writes it on its first start.")
	}
	if err != nil {
		return gameErr(err)
	}
	cur := games.ParseProperties(string(current))
	schema := map[string]games.Property{}
	for _, g := range cfg.Groups {
		for _, p := range g.Props {
			schema[p.Key] = p
		}
	}
	f := map[string]string{}
	var next *games.Properties
	var changed []string
	if in.Raw != nil {
		if len(*in.Raw) > 1<<20 {
			return httpx.Invalid(map[string]string{"raw": "The file is too large."})
		}
		next = games.ParseProperties(*in.Raw)
		for key, why := range cfg.Locked {
			a, _ := cur.Get(key)
			b, _ := next.Get(key)
			if a != b {
				f["raw"] = fmt.Sprintf("%s can't be changed here: %s", key, why)
			}
		}
		for _, k := range next.Keys() {
			if a, _ := cur.Get(k); a != mustGet(next, k) {
				changed = append(changed, k)
			}
		}
	} else {
		next = games.ParseProperties(string(current))
		keys := make([]string, 0, len(in.Values))
		for k := range in.Values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := in.Values[k]
			if why, locked := cfg.Locked[k]; locked {
				f[k] = why
				continue
			}
			if k == "" || len(k) > 100 || strings.ContainsAny(k, "\r\n=:") || strings.ContainsAny(v, "\r\n") || len(v) > 2000 {
				f[k] = "Invalid value."
				continue
			}
			if p, ok := schema[k]; ok {
				if msg := checkProperty(p, v); msg != "" {
					f[k] = msg
					continue
				}
			}
			if old, _ := cur.Get(k); old != v {
				next.Set(k, v)
				changed = append(changed, k)
			}
		}
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	if len(changed) > 0 {
		if err := srv.Data.WriteBytes(r.Context(), cfg.File, []byte(next.String())); err != nil {
			return gameErr(err)
		}
		s.Store.Audit(r.Context(), u, "game.settings", "resource", res.ID, map[string]any{"changed": changed}, clientIP(r))
	}
	httpx.JSON(w, 200, map[string]any{"changed": changed, "restartNeeded": srv.Running() && len(changed) > 0})
	return nil
}

func mustGet(p *games.Properties, k string) string {
	v, _ := p.Get(k)
	return v
}

func checkProperty(p games.Property, v string) string {
	switch p.Kind {
	case "bool":
		if v != "true" && v != "false" {
			return "On or off."
		}
	case "int":
		n, err := strconv.Atoi(v)
		if err != nil {
			return "Enter a whole number."
		}
		if (p.Min != nil && n < *p.Min) || (p.Max != nil && n > *p.Max) {
			return fmt.Sprintf("Between %d and %d.", *p.Min, *p.Max)
		}
	case "select":
		for _, o := range p.Options {
			if o.Value == v {
				return ""
			}
		}
		return "Choose one of the options."
	}
	return ""
}

/* ---------------------------------- Worlds ---------------------------------- */

func (s *Server) worldConfig(r *http.Request, srv games.Server) (games.WorldConfig, string, string, error) {
	ws, ok := srv.Adapter.(games.WorldSupport)
	if !ok {
		return games.WorldConfig{}, "", "", httpx.Unsupported("This game has no worlds to manage.")
	}
	c := ws.Worlds(srv.Spec)
	active, seed := c.DefaultLevel, ""
	if p := s.readProps(r.Context(), srv); p != nil {
		if v, ok := p.Get(c.LevelProperty); ok && v != "" {
			active = v
		}
		seed, _ = p.Get(c.SeedProperty)
	}
	return c, active, seed, nil
}

func (s *Server) gameWorlds(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	_, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	c, active, seed, err := s.worldConfig(r, srv)
	if err != nil {
		return err
	}
	list, err := srv.Data.Worlds(r.Context(), c, active)
	if err != nil {
		return gameErr(err)
	}
	httpx.JSON(w, 200, map[string]any{"config": c, "worlds": list, "active": active, "seed": seed, "running": srv.Running()})
	return nil
}

func validWorldName(n string) bool {
	return games.ValidName(n) && len(n) <= 64 && !strings.HasPrefix(n, ".") && n != "plugins" && n != "mods" && n != "config" && n != "logs" && n != "libraries"
}

func (s *Server) gameWorldAction(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	c, active, _, err := s.worldConfig(r, srv)
	if err != nil {
		return err
	}
	var in struct {
		World  string  `json:"world"`
		Backup bool    `json:"backup"`
		Seed   *string `json:"seed"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	action := r.PathValue("action")
	if !validWorldName(in.World) {
		return httpx.Invalid(map[string]string{"world": "Choose a world."})
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Hour)
	defer cancel()
	result := map[string]any{}
	switch action {
	case "reset", "activate", "delete":
	default:
		return httpx.NotFound("Action")
	}
	if action == "delete" && in.World == active {
		return httpx.Conflict("That's the world the server loads. Switch to another world first, or reset it instead.")
	}
	if in.Seed != nil && (len(*in.Seed) > 64 || strings.ContainsAny(*in.Seed, "\r\n")) {
		return httpx.Invalid(map[string]string{"seed": "Up to 64 characters."})
	}
	if in.Backup {
		b, err := srv.Backup(ctx, "before "+action+" "+in.World, false)
		if err != nil {
			return gameErr(err)
		}
		result["backup"] = b
	}
	restart, err := s.stopForMaintenance(ctx, res, &srv)
	if err != nil {
		return gameErr(err)
	}
	defer restart()
	switch action {
	case "reset", "delete":
		if err := srv.Data.Delete(ctx, games.WorldPaths(c, in.World)...); err != nil {
			return gameErr(err)
		}
	}
	if action == "activate" || (action == "reset" && in.Seed != nil) {
		ps, ok := srv.Adapter.(games.PropertySupport)
		if ok {
			file := ps.Properties(srv.Spec).File
			if raw, err := srv.Data.Read(ctx, file, 1<<20); err == nil {
				p := games.ParseProperties(string(raw))
				if action == "activate" {
					p.Set(c.LevelProperty, in.World)
				}
				if in.Seed != nil {
					p.Set(c.SeedProperty, strings.TrimSpace(*in.Seed))
				}
				if err := srv.Data.WriteBytes(ctx, file, []byte(p.String())); err != nil {
					return gameErr(err)
				}
			}
		}
	}
	s.Store.Audit(r.Context(), u, "game.world_"+action, "resource", res.ID, map[string]any{"world": in.World, "backup": in.Backup}, clientIP(r))
	result["ok"] = true
	httpx.JSON(w, 200, result)
	return nil
}

func (s *Server) gameWorldUpload(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	c, _, _, err := s.worldConfig(r, srv)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if !validWorldName(name) {
		return httpx.Invalid(map[string]string{"name": "Name the world: letters, numbers, spaces, dashes."})
	}
	body, size, cleanup, err := spoolUpload(r, 8<<30)
	if err != nil {
		return err
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(r.Context(), time.Hour)
	defer cancel()
	tmp := ".ignition/upload-" + strconv.FormatInt(time.Now().UnixNano(), 36) + ".zip"
	if err := srv.Data.Write(ctx, tmp, body, size); err != nil {
		return gameErr(err)
	}
	if err := srv.Data.ImportWorld(ctx, c, tmp, name); err != nil {
		return gameErr(err)
	}
	s.Store.Audit(r.Context(), u, "game.world_upload", "resource", res.ID, map[string]any{"world": name, "size": size}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"world": name})
	return nil
}

func (s *Server) gameWorldDownload(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	c, _, _, err := s.worldConfig(r, srv)
	if err != nil {
		return err
	}
	name := r.URL.Query().Get("world")
	if !validWorldName(name) {
		return httpx.NotFound("World")
	}
	paths := games.WorldPaths(c, name)
	if ok, err := srv.Data.Exists(r.Context(), paths[0]); err != nil || !ok {
		return httpx.NotFound("World")
	}
	s.Store.Audit(r.Context(), u, "game.world_download", "resource", res.ID, map[string]any{"world": name}, clientIP(r))
	attachment(w, fmt.Sprintf("%s-%s-%s.tar.gz", res.Name, name, time.Now().UTC().Format("20060102-150405")), "application/gzip")
	return streamTarGz(r.Context(), w, srv.Data, paths)
}

// streamTarGz writes several folders of the volume into one .tar.gz.
func streamTarGz(ctx context.Context, w io.Writer, data games.Data, paths []string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, p := range paths {
		if ok, _ := data.Exists(ctx, p); !ok {
			continue
		}
		src, err := data.Tar(ctx, p)
		if err != nil {
			return nil // headers are sent; the client sees a truncated download
		}
		tr := tar.NewReader(src)
		for {
			h, err := tr.Next()
			if err != nil {
				break
			}
			if tw.WriteHeader(h) != nil {
				break
			}
			io.Copy(tw, tr)
		}
		src.Close()
	}
	tw.Close()
	gz.Close()
	return nil
}

func queryPath(r *http.Request, key string) (string, error) {
	p, err := games.CleanPath(r.URL.Query().Get(key))
	if err != nil {
		return "", httpx.BadRequest("That path is outside the server's files.")
	}
	return p, nil
}
