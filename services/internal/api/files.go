package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/notsliver/Ignition/services/internal/games"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/worker"
)

/*
 * Files: browse and change the files in any resource's volumes, the same way for apps, databases and
 * game servers. A short-lived helper container mounts the volume, so it works whether the resource
 * is running or stopped and never touches the resource's own container.
 */

// volumeData resolves ?volume= to one of the resource's volumes.
func (s *Server) volumeData(r *http.Request) (*store.Resource, games.Data, error) {
	id, err := pathID(r, "Resource")
	if err != nil {
		return nil, games.Data{}, err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return nil, games.Data{}, notFound(err, "Resource")
	}
	d, err := s.localDocker()
	if err != nil {
		return nil, games.Data{}, err
	}
	vols, err := s.Store.Volumes(r.Context(), id)
	if err != nil {
		return nil, games.Data{}, err
	}
	want := r.URL.Query().Get("volume")
	for _, v := range vols {
		if v.Name == want || (want == "" && len(vols) > 0) {
			return res, games.Data{D: d, Volume: worker.VolumeName(id, v.Name)}, nil
		}
	}
	return nil, games.Data{}, httpx.NotFound("Volume")
}

func (s *Server) fileVolumes(w http.ResponseWriter, r *http.Request, _ *store.User) error {
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
	if vols == nil {
		vols = []store.Volume{}
	}
	httpx.JSON(w, 200, map[string]any{"volumes": vols, "running": res.Status == "running"})
	return nil
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	_, data, err := s.volumeData(r)
	if err != nil {
		return err
	}
	dir, err := queryPath(r, "path")
	if err != nil {
		return err
	}
	list, err := data.List(r.Context(), dir)
	if err != nil {
		return gameErr(err)
	}
	httpx.JSON(w, 200, map[string]any{"path": dir, "entries": list})
	return nil
}

const maxEditable = 5 << 20

func (s *Server) fileContent(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	_, data, err := s.volumeData(r)
	if err != nil {
		return err
	}
	p, err := queryPath(r, "path")
	if err != nil || p == "" {
		return httpx.NotFound("File")
	}
	rc, size, err := data.Open(r.Context(), p)
	if err != nil {
		if errors.Is(err, games.ErrNotFound) {
			return httpx.NotFound("File")
		}
		return gameErr(err)
	}
	defer rc.Close()
	if size > maxEditable {
		httpx.JSON(w, 200, map[string]any{"path": p, "size": size, "binary": false, "tooLarge": true})
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(rc, maxEditable))
	if err != nil {
		return err
	}
	binary := !utf8.Valid(b) || strings.ContainsRune(string(b), 0)
	out := map[string]any{"path": p, "size": size, "binary": binary, "tooLarge": false}
	if !binary || r.URL.Query().Get("text") == "1" {
		out["content"] = strings.ToValidUTF8(string(b), "�")
	}
	httpx.JSON(w, 200, out)
	return nil
}

func (s *Server) putFileContent(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, data, err := s.volumeData(r)
	if err != nil {
		return err
	}
	p, err := queryPath(r, "path")
	if err != nil || p == "" {
		return httpx.NotFound("File")
	}
	var in struct {
		Content string `json:"content"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if len(in.Content) > maxEditable {
		return httpx.Invalid(map[string]string{"content": "Files up to 5 MB can be edited here."})
	}
	if err := data.WriteBytes(r.Context(), p, []byte(in.Content)); err != nil {
		return gameErr(err)
	}
	s.Store.Audit(r.Context(), u, "file.edit", "resource", res.ID, map[string]any{"path": p}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"path": p})
	return nil
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, data, err := s.volumeData(r)
	if err != nil {
		return err
	}
	dir, err := queryPath(r, "path")
	if err != nil {
		return err
	}
	name := r.URL.Query().Get("name")
	if !games.ValidName(name) {
		return httpx.Invalid(map[string]string{"name": "Invalid file name."})
	}
	body, size, cleanup, err := spoolUpload(r, 8<<30)
	if err != nil {
		return err
	}
	defer cleanup()
	target := path.Join(dir, name)
	if err := data.Write(r.Context(), target, body, size); err != nil {
		return gameErr(err)
	}
	s.Store.Audit(r.Context(), u, "file.upload", "resource", res.ID, map[string]any{"path": target, "size": size}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"path": target})
	return nil
}

// streamFile sends a file (inline for previews, sandboxed so nothing in it can run) or a folder as .tar.gz.
func streamFile(ctx context.Context, w http.ResponseWriter, data games.Data, p, fallbackName string, inline bool) error {
	if p != "" {
		rc, size, err := data.Open(ctx, p)
		switch {
		case err == nil:
			defer rc.Close()
			ct := mime.TypeByExtension(path.Ext(p))
			if ct == "" {
				ct = "application/octet-stream"
			}
			if inline {
				w.Header().Set("Content-Type", ct)
				w.Header().Set("Content-Disposition", "inline")
				w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; media-src 'self'")
				w.Header().Set("X-Content-Type-Options", "nosniff")
				w.Header().Set("Cache-Control", "no-store")
			} else {
				attachment(w, path.Base(p), ct)
			}
			w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
			io.Copy(w, rc)
			return nil
		case errors.Is(err, games.ErrNotFound):
			return httpx.NotFound("File")
		case !errors.Is(err, games.ErrIsDir):
			return gameErr(err)
		}
	}
	name := fallbackName
	if p != "" {
		name = path.Base(p)
	}
	attachment(w, name+".tar.gz", "application/gzip")
	return streamTarGz(ctx, w, data, []string{p})
}

func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, data, err := s.volumeData(r)
	if err != nil {
		return err
	}
	p, err := queryPath(r, "path")
	if err != nil {
		return err
	}
	inline := r.URL.Query().Get("inline") == "1"
	if !inline {
		s.Store.Audit(r.Context(), u, "file.download", "resource", res.ID, map[string]any{"path": p}, clientIP(r))
	}
	return streamFile(r.Context(), w, data, p, res.Name, inline)
}

func (s *Server) statFile(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	_, data, err := s.volumeData(r)
	if err != nil {
		return err
	}
	p, err := queryPath(r, "path")
	if err != nil {
		return err
	}
	info, err := data.Stat(r.Context(), p)
	if err != nil {
		return gameErr(err)
	}
	httpx.JSON(w, 200, info)
	return nil
}

func (s *Server) fileTrash(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	_, data, err := s.volumeData(r)
	if err != nil {
		return err
	}
	list, err := data.ListTrash(r.Context())
	if err != nil {
		return gameErr(err)
	}
	httpx.JSON(w, 200, map[string]any{"items": list})
	return nil
}

// fileOps runs one operation on one or more items.
func (s *Server) fileOps(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, data, err := s.volumeData(r)
	if err != nil {
		return err
	}
	var in struct {
		Op        string   `json:"op"`
		Path      string   `json:"path"`
		Paths     []string `json:"paths"`
		To        string   `json:"to"`
		Name      string   `json:"name"`
		Mode      string   `json:"mode"`
		Recursive bool     `json:"recursive"`
		IDs       []string `json:"ids"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	clean := func(p string) (string, error) {
		c, err := games.CleanPath(p)
		if err != nil || c == "" {
			return "", httpx.BadRequest("Choose a file or folder inside the volume.")
		}
		return c, nil
	}
	if in.Path != "" {
		in.Paths = append(in.Paths, in.Path)
	}
	if len(in.Paths) > 1000 {
		return httpx.BadRequest("Too many items at once.")
	}
	var paths []string
	for _, p := range in.Paths {
		c, err := clean(p)
		if err != nil {
			return err
		}
		paths = append(paths, c)
	}
	one := func() (string, error) {
		if len(paths) != 1 {
			return "", httpx.BadRequest("Choose one item.")
		}
		return paths[0], nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Hour)
	defer cancel()
	out := map[string]any{}
	switch in.Op {
	case "mkdir", "touch":
		p, err := one()
		if err != nil {
			return err
		}
		if !games.ValidName(path.Base(p)) {
			return httpx.Invalid(map[string]string{"name": "Invalid name."})
		}
		if in.Op == "mkdir" {
			err = data.Mkdir(ctx, p)
		} else {
			err = data.Touch(ctx, p)
		}
		if err != nil {
			return gameErr(err)
		}
		out["path"] = p
	case "rename":
		p, err := one()
		if err != nil {
			return err
		}
		to, err := clean(in.To)
		if err != nil {
			return err
		}
		if !games.ValidName(path.Base(to)) {
			return httpx.Invalid(map[string]string{"name": "Invalid name."})
		}
		if err := data.Rename(ctx, p, to); err != nil {
			return gameErr(err)
		}
		out["path"] = to
	case "copy", "move":
		to, err := games.CleanPath(in.To)
		if err != nil {
			return httpx.BadRequest("Choose a folder inside the volume.")
		}
		made, err := data.CopyInto(ctx, paths, to, in.Op == "move")
		if err != nil {
			return gameErr(err)
		}
		out["paths"] = made
	case "duplicate":
		made, err := data.Duplicate(ctx, paths)
		if err != nil {
			return gameErr(err)
		}
		out["paths"] = made
	case "compress":
		dir := path.Dir(paths[0])
		if dir == "." {
			dir = ""
		}
		made, err := data.Compress(ctx, paths, dir, in.Name)
		if err != nil {
			return gameErr(err)
		}
		out["path"] = made
	case "extract":
		p, err := one()
		if err != nil {
			return err
		}
		made, err := data.Extract(ctx, p)
		if err != nil {
			return gameErr(err)
		}
		out["path"] = made
	case "unpack":
		p, err := one()
		if err != nil {
			return err
		}
		if err := data.Unpack(ctx, p); err != nil {
			return gameErr(err)
		}
	case "chmod":
		if err := data.Chmod(ctx, paths, in.Mode, in.Recursive); err != nil {
			return gameErr(err)
		}
	case "trash":
		if err := data.Trash(ctx, paths); err != nil {
			return gameErr(err)
		}
	case "delete":
		if len(paths) == 0 {
			return httpx.BadRequest("Choose what to delete.")
		}
		if err := data.Delete(ctx, paths...); err != nil {
			return gameErr(err)
		}
	case "restore":
		if err := data.Restore(ctx, in.IDs); err != nil {
			return gameErr(err)
		}
	case "empty-trash":
		if err := data.EmptyTrash(ctx, in.IDs); err != nil {
			return gameErr(err)
		}
	default:
		return httpx.BadRequest("Unknown operation.")
	}
	s.Store.Audit(r.Context(), u, "file."+in.Op, "resource", res.ID, map[string]any{"paths": paths, "to": in.To, "mode": in.Mode, "volume": r.URL.Query().Get("volume")}, clientIP(r))
	httpx.JSON(w, 200, out)
	return nil
}

/* -------------------------------- Share links ------------------------------- */

type shareClaim struct {
	Resource string `json:"r"`
	Volume   string `json:"v"`
	Path     string `json:"p"`
	Expires  int64  `json:"e"`
}

const shareContext = "file-share"

// shareFile makes a link anyone can download the item with until it expires. The link is the item's
// location sealed with the instance key, so it can't be altered to point elsewhere.
func (s *Server) shareFile(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, _, err := s.volumeData(r)
	if err != nil {
		return err
	}
	var in struct {
		Path  string `json:"path"`
		Hours int    `json:"hours"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	p, err := games.CleanPath(in.Path)
	if err != nil || p == "" {
		return httpx.BadRequest("Choose a file or folder.")
	}
	if in.Hours < 1 || in.Hours > 24*7 {
		return httpx.Invalid(map[string]string{"hours": "Between 1 hour and 7 days."})
	}
	vol := r.URL.Query().Get("volume")
	if vol == "" {
		vols, _ := s.Store.Volumes(r.Context(), res.ID)
		if len(vols) > 0 {
			vol = vols[0].Name
		}
	}
	exp := time.Now().Add(time.Duration(in.Hours) * time.Hour)
	claim, _ := json.Marshal(shareClaim{Resource: res.ID, Volume: vol, Path: p, Expires: exp.Unix()})
	sealed, err := s.Secrets.Seal(string(claim), shareContext)
	if err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(sealed)
	s.Store.Audit(r.Context(), u, "file.share", "resource", res.ID, map[string]any{"path": p, "volume": vol, "expires": exp.UTC()}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"url": strings.TrimRight(s.PublicURL, "/") + "/api/shared-files/" + token, "path": "/api/shared-files/" + token, "expiresAt": exp.UTC()})
	return nil
}

// sharedFile serves a share link. No session: the sealed token is the permission.
func (s *Server) sharedFile(w http.ResponseWriter, r *http.Request) error {
	token := r.PathValue("token")
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(token) > 2000 {
		return httpx.NotFound("Link")
	}
	plain, err := s.Secrets.Open(raw, shareContext)
	if err != nil {
		return httpx.NotFound("Link")
	}
	var c shareClaim
	if json.Unmarshal([]byte(plain), &c) != nil || time.Now().Unix() > c.Expires {
		return httpx.NotFound("Link (it may have expired)")
	}
	res, err := s.Store.GetResource(r.Context(), c.Resource)
	if err != nil {
		return httpx.NotFound("Link")
	}
	d, err := s.localDocker()
	if err != nil {
		return err
	}
	return streamFile(r.Context(), w, games.Data{D: d, Volume: worker.VolumeName(res.ID, c.Volume)}, c.Path, res.Name, false)
}
