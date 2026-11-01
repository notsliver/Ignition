package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/games"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

/* ---------------------------------- Players --------------------------------- */

type playerEntry struct {
	Name    string `json:"name"`
	UUID    string `json:"uuid,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Source  string `json:"source,omitempty"`
	Created string `json:"created,omitempty"`
	Expires string `json:"expires,omitempty"`
	Level   int    `json:"level,omitempty"`
}

type playerListOut struct {
	games.PlayerList
	Entries []playerEntry `json:"entries"`
	Enabled *bool         `json:"enabled"` // the list's on/off switch, when it has one
	Error   string        `json:"error,omitempty"`
}

func (s *Server) gamePlayers(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	_, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	ps, ok := srv.Adapter.(games.PlayerSupport)
	if !ok {
		return httpx.Unsupported("This game has no player management.")
	}
	cfg := ps.Players(srv.Spec)
	props := s.readProps(r.Context(), srv)
	lists := []playerListOut{}
	for _, l := range cfg.Lists {
		out := playerListOut{PlayerList: l, Entries: []playerEntry{}}
		if l.Toggle != nil && props != nil {
			v, _ := props.Get(l.Toggle.Property)
			on := v == "true"
			out.Enabled = &on
		}
		raw, err := srv.Data.Read(r.Context(), l.File, 4<<20)
		switch {
		case errors.Is(err, games.ErrNotFound):
		case err != nil:
			out.Error = err.Error()
		default:
			var items []map[string]any
			if json.Unmarshal(raw, &items) == nil {
				for _, it := range items {
					e := playerEntry{Name: str(it[l.Key]), UUID: str(it["uuid"]), Reason: str(it["reason"]), Source: str(it["source"]), Created: str(it["created"]), Expires: str(it["expires"])}
					if lv, ok := it["level"].(float64); ok {
						e.Level = int(lv)
					}
					if e.Name != "" {
						out.Entries = append(out.Entries, e)
					}
				}
			}
		}
		sort.Slice(out.Entries, func(i, j int) bool {
			return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
		})
		lists = append(lists, out)
	}
	online, known := []string{}, false
	count, maxPlayers, answering := 0, 0, false
	if srv.Running() {
		if st, ok := srv.Status(r.Context()); ok {
			online, known, count, maxPlayers, answering = st.Names, st.NamesKnown, st.Players, st.MaxPlayers, true
		}
	}
	// Slots come from the settings file when the server can't be asked.
	if !answering && props != nil {
		if v, ok := props.Get("max-players"); ok {
			maxPlayers, _ = strconv.Atoi(v)
		}
	}
	httpx.JSON(w, 200, map[string]any{"running": srv.Running(), "answering": answering, "online": online, "namesKnown": known, "count": count, "max": maxPlayers,
		"lists": lists, "actions": cfg.Actions, "nameRule": cfg.NameRule})
	return nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func (s *Server) gamePlayerAction(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	ps, ok := srv.Adapter.(games.PlayerSupport)
	if !ok {
		return httpx.Unsupported("This game has no player management.")
	}
	var in struct {
		Action string `json:"action"` // add, remove, enable, disable, or a player action's id
		List   string `json:"list"`
		Player string `json:"player"`
		Value  string `json:"value"`
		Input  string `json:"input"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if !srv.Running() {
		return httpx.Conflict("Start the server to change players. Changes go through its console, so they apply at once and the game keeps its own files right.")
	}
	cfg := ps.Players(srv.Spec)
	in.Player = strings.TrimSpace(in.Player)
	checkName := func(rule string) error {
		if !regexp.MustCompile(rule).MatchString(in.Player) {
			return httpx.Invalid(map[string]string{"player": "That isn't a valid player name."})
		}
		return nil
	}
	var line string
	switch in.Action {
	case "add", "remove", "enable", "disable":
		var list *games.PlayerList
		for i := range cfg.Lists {
			if cfg.Lists[i].ID == in.List {
				list = &cfg.Lists[i]
			}
		}
		if list == nil {
			return httpx.NotFound("List")
		}
		switch in.Action {
		case "enable", "disable":
			if list.Toggle == nil {
				return httpx.BadRequest("This list has no switch.")
			}
			line = map[bool]string{true: list.Toggle.On, false: list.Toggle.Off}[in.Action == "enable"]
		default:
			template := map[bool]string{true: list.Add, false: list.Remove}[in.Action == "add"]
			if template == "" {
				return httpx.BadRequest("That can't be done from here.")
			}
			if list.Key == "ip" {
				if net.ParseIP(in.Player) == nil {
					return httpx.Invalid(map[string]string{"player": "Enter an IP address."})
				}
			} else if err := checkName(cfg.NameRule); err != nil {
				return err
			}
			line = games.Fill(template, map[string]string{"player": in.Player, "reason": in.Input})
		}
	default:
		var act *games.PlayerAction
		for i := range cfg.Actions {
			if cfg.Actions[i].ID == in.Action {
				act = &cfg.Actions[i]
			}
		}
		if act == nil {
			return httpx.NotFound("Action")
		}
		if err := checkName(cfg.NameRule); err != nil {
			return err
		}
		if len(act.Options) > 0 {
			ok := false
			for _, o := range act.Options {
				ok = ok || o.Value == in.Value
			}
			if !ok {
				return httpx.Invalid(map[string]string{"value": "Choose one of the options."})
			}
		}
		if act.Input == "Message" && strings.TrimSpace(in.Input) == "" {
			return httpx.Invalid(map[string]string{"input": "Write a message."})
		}
		line = games.Fill(act.Template, map[string]string{"player": in.Player, "reason": in.Input, "message": in.Input, "value": in.Value})
	}
	if len(in.Input) > 300 {
		return httpx.Invalid(map[string]string{"input": "Up to 300 characters."})
	}
	out, _, err := srv.Command(r.Context(), line)
	if err != nil {
		return gameErr(err)
	}
	s.Store.Audit(r.Context(), u, "game.players", "resource", res.ID, map[string]any{"action": in.Action, "list": in.List, "player": in.Player, "command": line}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"output": out, "command": line})
	return nil
}

/* ---------------------------------- Content --------------------------------- */

type contentItem struct {
	Kind     string               `json:"kind"`
	File     string               `json:"file"`
	Enabled  bool                 `json:"enabled"`
	Size     int64                `json:"size"`
	Modified time.Time            `json:"modified"`
	SHA1     string               `json:"sha1"`
	Project  *games.Project       `json:"project"`
	Version  *contentVersionBrief `json:"version"`
	Update   *contentVersionBrief `json:"update"`
}

type contentVersionBrief struct {
	ID            string   `json:"id"`
	VersionNumber string   `json:"versionNumber"`
	Name          string   `json:"name"`
	VersionType   string   `json:"versionType"`
	GameVersions  []string `json:"gameVersions,omitempty"`
}

func brief(v games.ContentVersion) *contentVersionBrief {
	return &contentVersionBrief{ID: v.ID, VersionNumber: v.VersionNumber, Name: v.Name, VersionType: v.VersionType}
}

func contentKind(srv games.Server, id string) (games.ContentKind, error) {
	for _, k := range games.ContentKinds(srv.Adapter, srv.Spec) {
		if k.ID == id {
			return k, nil
		}
	}
	return games.ContentKind{}, httpx.NotFound("Content type")
}

func contentFileOK(name string) bool {
	return games.ValidName(name) && (strings.HasSuffix(name, ".jar") || strings.HasSuffix(name, ".jar.disabled"))
}

// installedContent lists the jars in each content folder and identifies them on Modrinth by hash.
func (s *Server) installedContent(ctx context.Context, srv games.Server, kinds []games.ContentKind, withUpdates bool) ([]contentItem, string, error) {
	items := []contentItem{}
	var hashes []string
	for _, k := range kinds {
		entries, err := srv.Data.List(ctx, k.Dir)
		if err != nil {
			return nil, "", err
		}
		sums, err := srv.Data.Hashes(ctx, k.Dir, ".jar", ".jar.disabled")
		if err != nil {
			return nil, "", err
		}
		for _, e := range entries {
			if e.Dir || !contentFileOK(e.Name) {
				continue
			}
			items = append(items, contentItem{Kind: k.ID, File: e.Name, Enabled: !strings.HasSuffix(e.Name, ".disabled"), Size: e.Size, Modified: e.Modified, SHA1: sums[e.Name]})
			if sums[e.Name] != "" {
				hashes = append(hashes, sums[e.Name])
			}
		}
	}
	if len(hashes) == 0 {
		return items, "", nil
	}
	known, err := games.VersionsByHash(ctx, hashes)
	if err != nil {
		return items, "Couldn't reach Modrinth to identify these files: " + err.Error(), nil
	}
	ids := map[string]bool{}
	for _, v := range known {
		ids[v.ProjectID] = true
	}
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	projects, _ := games.GetProjects(ctx, list)
	updates := map[string]games.ContentVersion{}
	if withUpdates {
		gv := games.GameVersion(srv.Spec)
		for _, k := range kinds {
			var kh []string
			for _, it := range items {
				if it.Kind == k.ID && it.SHA1 != "" {
					kh = append(kh, it.SHA1)
				}
			}
			if u, err := games.Updates(ctx, kh, k.Loaders, gv); err == nil {
				for h, v := range u {
					updates[h] = v
				}
			}
		}
	}
	for i := range items {
		v, ok := known[items[i].SHA1]
		if !ok {
			continue
		}
		items[i].Version = brief(v)
		if p, ok := projects[v.ProjectID]; ok {
			items[i].Project = &p
		}
		if u, ok := updates[items[i].SHA1]; ok && u.ID != v.ID && games.CompareVersions(u.Published, v.Published) >= 0 {
			items[i].Update = brief(u)
		}
	}
	return items, "", nil
}

func (s *Server) gameContent(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	_, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	kinds := games.ContentKinds(srv.Adapter, srv.Spec)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	items, sourceErr, err := s.installedContent(ctx, srv, kinds, true)
	if err != nil {
		return gameErr(err)
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(contentTitle(items[i])) < strings.ToLower(contentTitle(items[j]))
	})
	httpx.JSON(w, 200, map[string]any{"kinds": kinds, "items": items, "gameVersion": games.GameVersion(srv.Spec), "sourceError": sourceErr, "running": srv.Running()})
	return nil
}

func contentTitle(it contentItem) string {
	if it.Project != nil {
		return it.Project.Title
	}
	return it.File
}

func (s *Server) gameContentSearch(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	_, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	kind, err := contentKind(srv, q.Get("kind"))
	if err != nil {
		return err
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	res, err := games.Search(r.Context(), games.SearchQuery{Query: q.Get("q"), ProjectType: kind.ProjectType, Loaders: kind.Loaders,
		GameVersion: games.GameVersion(srv.Spec), Sort: q.Get("sort"), Offset: max(offset, 0), Limit: 20})
	if err != nil {
		return gameErr(err)
	}
	httpx.JSON(w, 200, res)
	return nil
}

func (s *Server) gameContentVersions(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	_, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	kind, err := contentKind(srv, q.Get("kind"))
	if err != nil {
		return err
	}
	project := q.Get("project")
	if project == "" || len(project) > 64 {
		return httpx.NotFound("Project")
	}
	list, err := games.ProjectVersions(r.Context(), project, kind.Loaders, games.GameVersion(srv.Spec))
	if err != nil {
		return gameErr(err)
	}
	httpx.JSON(w, 200, map[string]any{"versions": list})
	return nil
}

// planInstall picks the version to install and every required dependency that isn't there yet.
func planInstall(ctx context.Context, kind games.ContentKind, gameVersion, projectID, versionID string, installed map[string]bool) ([]games.ContentVersion, error) {
	var plan []games.ContentVersion
	seen := map[string]bool{}
	var visit func(project, version string, root bool) error
	visit = func(project, version string, root bool) error {
		if len(plan) > 40 {
			return errors.New("too many dependencies to install at once")
		}
		var v games.ContentVersion
		if version != "" {
			got, err := games.GetVersion(ctx, version)
			if err != nil {
				return err
			}
			v = got
			project = v.ProjectID
		}
		if seen[project] || (!root && installed[project]) {
			return nil
		}
		seen[project] = true
		if version == "" {
			list, err := games.ProjectVersions(ctx, project, kind.Loaders, gameVersion)
			if err != nil {
				return err
			}
			best, ok := games.BestVersion(list)
			if !ok {
				name := project
				if p, err := games.GetProject(ctx, project); err == nil {
					name = p.Title
				}
				return fmt.Errorf("no version of %s runs on %s %s", name, strings.Join(kind.Loaders, "/"), gameVersion)
			}
			v = best
		} else {
			// An exact version was asked for: it must still be for this server's loader.
			loaderOK := false
			for _, l := range v.Loaders {
				for _, k := range kind.Loaders {
					loaderOK = loaderOK || l == k
				}
			}
			if !loaderOK {
				return fmt.Errorf("that version is for %s, not %s", strings.Join(v.Loaders, "/"), strings.Join(kind.Loaders, "/"))
			}
		}
		plan = append(plan, v)
		for _, d := range v.Dependencies {
			if d.Type != "required" || (d.ProjectID == "" && d.VersionID == "") {
				continue
			}
			dv := d.VersionID
			if d.ProjectID != "" {
				dv = "" // pick the dependency's best fitting version rather than an exact old pin
			}
			if err := visit(d.ProjectID, dv, false); err != nil {
				return fmt.Errorf("a required dependency: %w", err)
			}
		}
		return nil
	}
	if err := visit(projectID, versionID, true); err != nil {
		return nil, err
	}
	return plan, nil
}

func (s *Server) gameContentInstall(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	var in struct {
		Kind      string `json:"kind"`
		ProjectID string `json:"projectId"`
		VersionID string `json:"versionId"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	kind, err := contentKind(srv, in.Kind)
	if err != nil {
		return err
	}
	if in.ProjectID == "" && in.VersionID == "" {
		return httpx.Invalid(map[string]string{"projectId": "Choose something to install."})
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	// What's there already, by project, so dependencies aren't installed twice and an update replaces the old file.
	items, _, err := s.installedContent(ctx, srv, []games.ContentKind{kind}, false)
	if err != nil {
		return gameErr(err)
	}
	installed, filesOf := map[string]bool{}, map[string][]string{}
	for _, it := range items {
		if it.Project != nil {
			installed[it.Project.ID] = true
			filesOf[it.Project.ID] = append(filesOf[it.Project.ID], it.File)
		}
	}
	plan, err := planInstall(ctx, kind, games.GameVersion(srv.Spec), in.ProjectID, in.VersionID, installed)
	if err != nil {
		return httpx.Conflict(capitalize(err.Error()) + ".")
	}
	type done struct {
		Title   string `json:"title"`
		File    string `json:"file"`
		Version string `json:"version"`
		Reason  string `json:"reason"`
	}
	var out []done
	for i, v := range plan {
		f, ok := v.PrimaryFile()
		if !ok || !contentFileOK(f.Filename) {
			return httpx.Conflict(fmt.Sprintf("%s %s has no server file to install.", v.Name, v.VersionNumber))
		}
		data, err := games.Download(ctx, f)
		if err != nil {
			return gameErr(err)
		}
		// Replace older files of the same project (an update or a reinstall).
		var old []string
		for _, name := range filesOf[v.ProjectID] {
			if name != f.Filename {
				old = append(old, kind.Dir+"/"+name)
			}
		}
		if err := srv.Data.WriteBytes(ctx, kind.Dir+"/"+f.Filename, data); err != nil {
			return gameErr(err)
		}
		if len(old) > 0 {
			srv.Data.Delete(ctx, old...)
		}
		title := v.Name
		if p, err := games.GetProject(ctx, v.ProjectID); err == nil {
			title = p.Title
		}
		reason := "requested"
		if i > 0 {
			reason = "required"
		}
		out = append(out, done{Title: title, File: f.Filename, Version: v.VersionNumber, Reason: reason})
	}
	s.Store.Audit(r.Context(), u, "game.content_install", "resource", res.ID, map[string]any{"kind": kind.ID, "installed": out}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"installed": out, "restartNeeded": srv.Running()})
	return nil
}

func (s *Server) gameContentUpload(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	kind, err := contentKind(srv, r.URL.Query().Get("kind"))
	if err != nil {
		return err
	}
	name := r.URL.Query().Get("name")
	if !games.ValidName(name) || !strings.HasSuffix(strings.ToLower(name), ".jar") {
		return httpx.Invalid(map[string]string{"file": "Upload a .jar file."})
	}
	body, size, cleanup, err := spoolUpload(r, 256<<20)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := srv.Data.Write(r.Context(), kind.Dir+"/"+name, body, size); err != nil {
		return gameErr(err)
	}
	s.Store.Audit(r.Context(), u, "game.content_upload", "resource", res.ID, map[string]any{"kind": kind.ID, "file": name, "size": size}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"file": name, "restartNeeded": srv.Running()})
	return nil
}

func (s *Server) gameContentChange(w http.ResponseWriter, r *http.Request, u *store.User) error {
	res, srv, err := s.gameServer(r)
	if err != nil {
		return err
	}
	var in struct {
		Kind    string `json:"kind"`
		File    string `json:"file"`
		Enabled *bool  `json:"enabled"`
		Remove  bool   `json:"remove"`
		Update  bool   `json:"update"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	kind, err := contentKind(srv, in.Kind)
	if err != nil {
		return err
	}
	if !contentFileOK(in.File) {
		return httpx.NotFound("File")
	}
	path := kind.Dir + "/" + in.File
	base := strings.TrimSuffix(in.File, ".disabled")
	action := ""
	switch {
	case in.Remove:
		action = "remove"
		err = srv.Data.Delete(r.Context(), path)
	case in.Enabled != nil:
		target := base
		if !*in.Enabled {
			target = base + ".disabled"
		}
		if target == in.File {
			httpx.JSON(w, 200, map[string]any{"file": in.File, "restartNeeded": false})
			return nil
		}
		action = map[bool]string{true: "enable", false: "disable"}[*in.Enabled]
		err = srv.Data.Rename(r.Context(), path, kind.Dir+"/"+target)
		in.File = target
	case in.Update:
		action = "update"
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		sums, herr := srv.Data.Hashes(ctx, kind.Dir, ".jar", ".jar.disabled")
		if herr != nil || sums[in.File] == "" {
			return httpx.NotFound("File")
		}
		ups, uerr := games.Updates(ctx, []string{sums[in.File]}, kind.Loaders, games.GameVersion(srv.Spec))
		if uerr != nil {
			return gameErr(uerr)
		}
		next, ok := ups[sums[in.File]]
		if !ok {
			return httpx.Conflict("There's no newer version for this server's version and loader.")
		}
		f, ok := next.PrimaryFile()
		if !ok || !contentFileOK(f.Filename) {
			return httpx.Conflict("The new version has no server file.")
		}
		data, derr := games.Download(ctx, f)
		if derr != nil {
			return gameErr(derr)
		}
		target := f.Filename
		if strings.HasSuffix(in.File, ".disabled") {
			target += ".disabled" // keep it off if it was off
		}
		if err = srv.Data.WriteBytes(ctx, kind.Dir+"/"+target, data); err == nil && target != in.File {
			err = srv.Data.Delete(ctx, path)
		}
		in.File = target
	default:
		return httpx.BadRequest("Say what to change.")
	}
	if err != nil {
		return gameErr(err)
	}
	s.Store.Audit(r.Context(), u, "game.content_"+action, "resource", res.ID, map[string]any{"kind": kind.ID, "file": in.File}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"file": in.File, "restartNeeded": srv.Running()})
	return nil
}

// spoolUpload reads a raw request body up to limit. Bodies without a length go through a temporary file.
func spoolUpload(r *http.Request, limit int64) (io.Reader, int64, func(), error) {
	if r.ContentLength > limit {
		return nil, 0, nil, httpx.Invalid(map[string]string{"file": fmt.Sprintf("Files up to %s can be uploaded.", games.HumanSize(limit))})
	}
	if r.ContentLength > 0 {
		return io.LimitReader(r.Body, r.ContentLength), r.ContentLength, func() {}, nil
	}
	f, err := createTemp()
	if err != nil {
		return nil, 0, nil, err
	}
	cleanup := func() { f.Close(); removeFile(f.Name()) }
	n, err := io.Copy(f, io.LimitReader(r.Body, limit+1))
	if err != nil {
		cleanup()
		return nil, 0, nil, err
	}
	if n > limit {
		cleanup()
		return nil, 0, nil, httpx.Invalid(map[string]string{"file": fmt.Sprintf("Files up to %s can be uploaded.", games.HumanSize(limit))})
	}
	if n == 0 {
		cleanup()
		return nil, 0, nil, httpx.Invalid(map[string]string{"file": "The file is empty."})
	}
	f.Seek(0, io.SeekStart)
	return f, n, cleanup, nil
}
