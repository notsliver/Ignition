package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/notsliver/Ignition/services/internal/games"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/worker"
)

/*
 * Backups of any resource's volumes, kept as .tar.gz archives in the resource's backups volume on
 * this machine. A game server's world is saved first; any other resource can be paused for the copy
 * (recommended for databases, whose files change while they run).
 */

// backupData is the volume named by ?volume= with its backup folder.
func (s *Server) backupData(r *http.Request) (*store.Resource, games.Data, error) {
	res, data, err := s.volumeData(r)
	if err != nil {
		return nil, data, err
	}
	vol := r.URL.Query().Get("volume")
	if vol == "" {
		vols, _ := s.Store.Volumes(r.Context(), res.ID)
		if len(vols) > 0 {
			vol = vols[0].Name
		}
	}
	if vol == "backups" {
		return nil, data, httpx.Conflict("A volume named backups can't be backed up here: that name is where backups are kept. Rename it under Storage.")
	}
	data.Backups = worker.VolumeName(res.ID, "backups")
	if vol != "data" {
		data.BackupDir = vol // game servers (and anything with a "data" volume) keep backups at the top
	}
	return res, data, nil
}

// stopResource stops a running resource and returns a function that starts it again.
func (s *Server) stopResource(ctx context.Context, res *store.Resource) (func(), error) {
	if res.Status != "running" || res.ContainerID == "" {
		return func() {}, nil
	}
	grace := 30 * time.Second
	if ds, err := s.Store.GetBuildSettings(ctx, res.ID); err == nil && ds.StopGrace > 0 {
		grace = time.Duration(ds.StopGrace) * time.Second
	}
	if err := s.Runtime.Stop(ctx, res.ContainerID, grace); err != nil {
		return nil, fmt.Errorf("couldn't stop %s: %v", res.Name, err)
	}
	s.Store.SetStatus(ctx, res.ID, "stopped")
	container := res.ContainerID
	return func() {
		ctx := context.WithoutCancel(ctx)
		if err := s.Runtime.Start(ctx, container); err == nil {
			s.Store.SetStatus(ctx, res.ID, "running")
			now := time.Now()
			s.Store.SetRunningSince(ctx, res.ID, &now)
		}
	}, nil
}

func (s *Server) listBackups(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	res, data, err := s.backupData(r)
	if err != nil {
		return err
	}
	list, err := data.ListBackups(r.Context())
	if err != nil {
		return gameErr(err)
	}
	var total int64
	for _, b := range list {
		total += b.Size
	}
	httpx.JSON(w, 200, map[string]any{"backups": list, "totalSize": total, "running": res.Status == "running", "type": res.Type})
	return nil
}

func (s *Server) createBackup(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, data, err := s.backupData(r)
	if err != nil {
		return err
	}
	var in struct {
		Label string `json:"label"`
		Stop  bool   `json:"stop"` // pause the resource while copying
	}
	if err := httpx.DecodeOptional(r, &in); err != nil {
		return err
	}
	if len(in.Label) > 60 {
		return httpx.Invalid(map[string]string{"label": "Up to 60 characters."})
	}
	if err := s.storageAllowed(r.Context(), res, u, true); err != nil {
		return domainError(err)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Hour)
	defer cancel()
	b, err := s.backupNow(ctx, res, data, in.Label, in.Stop, false)
	if err != nil {
		return gameErr(err)
	}
	s.Store.Audit(r.Context(), u, "resource.backup", "resource", res.ID, map[string]any{"backup": b.Name, "size": b.Size, "volume": r.URL.Query().Get("volume")}, clientIP(r))
	httpx.JSON(w, 201, b)
	return nil
}

// backupNow archives the volume: a game server saves its world first; stop pauses anything else.
func (s *Server) backupNow(ctx context.Context, res *store.Resource, data games.Data, label string, stop, auto bool) (games.Backup, error) {
	if res.Type == "game" && data.BackupDir == "" {
		if d, err := s.localDocker(); err == nil {
			if srv, err := worker.GameServer(ctx, s.Store, d, res); err == nil {
				srv.Data = data
				return srv.Backup(ctx, label, auto)
			}
		}
	}
	if stop {
		restart, err := s.stopResource(ctx, res)
		if err != nil {
			return games.Backup{}, err
		}
		defer restart()
	}
	return data.CreateBackup(ctx, games.BackupName(label, auto, time.Now()))
}

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, data, err := s.backupData(r)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	if !games.ValidBackup(name) {
		return httpx.NotFound("Backup")
	}
	var in struct {
		BackupFirst bool `json:"backupFirst"`
	}
	if err := httpx.DecodeOptional(r, &in); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Hour)
	defer cancel()
	out := map[string]any{"restored": name}
	if in.BackupFirst {
		b, err := s.backupNow(ctx, res, data, "before restore", false, false)
		if err != nil {
			return gameErr(err)
		}
		out["backup"] = b
	}
	// Nothing may write while the files are replaced.
	restart, err := s.stopResource(ctx, res)
	if err != nil {
		return gameErr(err)
	}
	defer restart()
	if err := data.RestoreBackup(ctx, name); err != nil {
		return gameErr(err)
	}
	s.Store.Audit(r.Context(), u, "resource.restore", "resource", res.ID, map[string]any{"backup": name, "backupFirst": in.BackupFirst, "volume": r.URL.Query().Get("volume")}, clientIP(r))
	httpx.JSON(w, 200, out)
	return nil
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, data, err := s.backupData(r)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	if !games.ValidBackup(name) {
		return httpx.NotFound("Backup")
	}
	if err := data.DeleteBackup(r.Context(), name); err != nil {
		return gameErr(err)
	}
	s.Store.Audit(r.Context(), u, "resource.backup_delete", "resource", res.ID, map[string]any{"backup": name}, clientIP(r))
	w.WriteHeader(204)
	return nil
}

func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, data, err := s.backupData(r)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	rc, size, err := data.OpenBackup(r.Context(), name)
	if err != nil {
		if errors.Is(err, games.ErrNotFound) {
			return httpx.NotFound("Backup")
		}
		return gameErr(err)
	}
	defer rc.Close()
	s.Store.Audit(r.Context(), u, "resource.backup_download", "resource", res.ID, map[string]any{"backup": name}, clientIP(r))
	attachment(w, res.Name+"-"+name, "application/gzip")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	io.Copy(w, rc)
	return nil
}
