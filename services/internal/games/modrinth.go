package games

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Modrinth is the content source for mods, plugins and modpacks: an open API that needs no key.
// https://docs.modrinth.com/api/

const userAgent = "notsliver/Ignition (self-hosted game servers)"

var httpClient = &http.Client{Timeout: 20 * time.Second}

func fetchJSON(ctx context.Context, u string, out any) error {
	return doJSON(ctx, "GET", u, nil, out)
}

func doJSON(ctx context.Context, method, u string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: couldn't reach %s", ErrUnavailable, hostOf(u))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: not found", ErrNotFound)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%w: %s answered %s", ErrUnavailable, hostOf(u), resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

var ErrNotFound = fmt.Errorf("not found")

func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil {
		return p.Host
	}
	return "the server"
}

const modrinthAPI = "https://api.modrinth.com/v2"

type Project struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	IconURL     string   `json:"iconUrl"`
	Author      string   `json:"author,omitempty"`
	Downloads   int      `json:"downloads"`
	Follows     int      `json:"follows"`
	Categories  []string `json:"categories"`
	ServerSide  string   `json:"serverSide"`
	ClientSide  string   `json:"clientSide"`
	ProjectType string   `json:"projectType"`
	Updated     string   `json:"updated,omitempty"`
	URL         string   `json:"url"`
}

type ContentVersion struct {
	ID            string       `json:"id"`
	ProjectID     string       `json:"projectId"`
	Name          string       `json:"name"`
	VersionNumber string       `json:"versionNumber"`
	VersionType   string       `json:"versionType"` // release, beta, alpha
	Loaders       []string     `json:"loaders"`
	GameVersions  []string     `json:"gameVersions"`
	Published     string       `json:"published"`
	Downloads     int          `json:"downloads"`
	Files         []File       `json:"files"`
	Dependencies  []Dependency `json:"dependencies"`
}

type File struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	SHA1     string `json:"sha1"`
	Primary  bool   `json:"primary"`
}

type Dependency struct {
	ProjectID string `json:"projectId"`
	VersionID string `json:"versionId"`
	Type      string `json:"type"` // required, optional, incompatible, embedded
}

// PrimaryFile is the file to install: the one marked primary, else the first .jar.
func (v ContentVersion) PrimaryFile() (File, bool) {
	for _, f := range v.Files {
		if f.Primary {
			return f, true
		}
	}
	for _, f := range v.Files {
		if strings.HasSuffix(f.Filename, ".jar") {
			return f, true
		}
	}
	return File{}, false
}

type rawProject struct {
	ID          string   `json:"id"`
	ProjectID   string   `json:"project_id"`
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	IconURL     string   `json:"icon_url"`
	Author      string   `json:"author"`
	Downloads   int      `json:"downloads"`
	Follows     int      `json:"follows"`
	Followers   int      `json:"followers"`
	Categories  []string `json:"categories"`
	Display     []string `json:"display_categories"`
	ServerSide  string   `json:"server_side"`
	ClientSide  string   `json:"client_side"`
	ProjectType string   `json:"project_type"`
	Updated     string   `json:"date_modified"`
	UpdatedP    string   `json:"updated"`
}

func (r rawProject) project() Project {
	id := r.ProjectID
	if id == "" {
		id = r.ID
	}
	cats := r.Display
	if len(cats) == 0 {
		cats = r.Categories
	}
	updated := r.Updated
	if updated == "" {
		updated = r.UpdatedP
	}
	follows := r.Follows
	if follows == 0 {
		follows = r.Followers
	}
	kind := r.ProjectType
	if kind == "" {
		kind = "mod"
	}
	return Project{ID: id, Slug: r.Slug, Title: r.Title, Description: r.Description, IconURL: r.IconURL, Author: r.Author, Downloads: r.Downloads, Follows: follows,
		Categories: cats, ServerSide: r.ServerSide, ClientSide: r.ClientSide, ProjectType: kind, Updated: updated,
		URL: "https://modrinth.com/" + kind + "/" + orDefault(r.Slug, id)}
}

type rawVersion struct {
	ID            string   `json:"id"`
	ProjectID     string   `json:"project_id"`
	Name          string   `json:"name"`
	VersionNumber string   `json:"version_number"`
	VersionType   string   `json:"version_type"`
	Loaders       []string `json:"loaders"`
	GameVersions  []string `json:"game_versions"`
	Published     string   `json:"date_published"`
	Downloads     int      `json:"downloads"`
	Files         []struct {
		URL      string            `json:"url"`
		Filename string            `json:"filename"`
		Size     int64             `json:"size"`
		Primary  bool              `json:"primary"`
		Hashes   map[string]string `json:"hashes"`
	} `json:"files"`
	Dependencies []struct {
		ProjectID      string `json:"project_id"`
		VersionID      string `json:"version_id"`
		DependencyType string `json:"dependency_type"`
	} `json:"dependencies"`
}

func (r rawVersion) version() ContentVersion {
	v := ContentVersion{ID: r.ID, ProjectID: r.ProjectID, Name: r.Name, VersionNumber: r.VersionNumber, VersionType: r.VersionType, Loaders: r.Loaders,
		GameVersions: r.GameVersions, Published: r.Published, Downloads: r.Downloads, Files: []File{}, Dependencies: []Dependency{}}
	for _, f := range r.Files {
		v.Files = append(v.Files, File{URL: f.URL, Filename: f.Filename, Size: f.Size, SHA1: f.Hashes["sha1"], Primary: f.Primary})
	}
	for _, d := range r.Dependencies {
		v.Dependencies = append(v.Dependencies, Dependency{ProjectID: d.ProjectID, VersionID: d.VersionID, Type: d.DependencyType})
	}
	return v
}

type SearchQuery struct {
	Query       string
	ProjectType string   // mod, plugin, modpack
	Loaders     []string // any of
	GameVersion string
	Sort        string // relevance, downloads, follows, newest, updated
	Offset      int
	Limit       int
}

type SearchResult struct {
	Hits   []Project `json:"hits"`
	Total  int       `json:"total"`
	Offset int       `json:"offset"`
}

// Search finds projects that run on a server: client-only mods are left out.
func Search(ctx context.Context, q SearchQuery) (SearchResult, error) {
	facets := [][]string{{"project_type:" + q.ProjectType}, {"server_side:required", "server_side:optional"}}
	if len(q.Loaders) > 0 {
		var any []string
		for _, l := range q.Loaders {
			any = append(any, "categories:"+l)
		}
		facets = append(facets, any)
	}
	if q.GameVersion != "" {
		facets = append(facets, []string{"versions:" + q.GameVersion})
	}
	fb, _ := json.Marshal(facets)
	if q.Limit <= 0 || q.Limit > 50 {
		q.Limit = 20
	}
	switch q.Sort {
	case "relevance", "downloads", "follows", "newest", "updated":
	default:
		q.Sort = "relevance"
	}
	v := url.Values{"query": {q.Query}, "facets": {string(fb)}, "index": {q.Sort}, "offset": {strconv.Itoa(q.Offset)}, "limit": {strconv.Itoa(q.Limit)}}
	u := modrinthAPI + "/search?" + v.Encode()
	res, err := remote.get(ctx, u, 5*time.Minute, func(ctx context.Context) (any, error) {
		var doc struct {
			Hits      []rawProject `json:"hits"`
			TotalHits int          `json:"total_hits"`
			Offset    int          `json:"offset"`
		}
		if err := fetchJSON(ctx, u, &doc); err != nil {
			return nil, err
		}
		out := SearchResult{Hits: []Project{}, Total: doc.TotalHits, Offset: doc.Offset}
		for _, h := range doc.Hits {
			out.Hits = append(out.Hits, h.project())
		}
		return out, nil
	})
	if err != nil {
		return SearchResult{}, err
	}
	return res.(SearchResult), nil
}

func GetProject(ctx context.Context, idOrSlug string) (Project, error) {
	u := modrinthAPI + "/project/" + url.PathEscape(idOrSlug)
	res, err := remote.get(ctx, u, 10*time.Minute, func(ctx context.Context) (any, error) {
		var raw rawProject
		if err := fetchJSON(ctx, u, &raw); err != nil {
			return nil, err
		}
		return raw.project(), nil
	})
	if err != nil {
		return Project{}, err
	}
	return res.(Project), nil
}

func GetProjects(ctx context.Context, ids []string) (map[string]Project, error) {
	out := map[string]Project{}
	if len(ids) == 0 {
		return out, nil
	}
	b, _ := json.Marshal(ids)
	var raw []rawProject
	if err := fetchJSON(ctx, modrinthAPI+"/projects?ids="+url.QueryEscape(string(b)), &raw); err != nil {
		return nil, err
	}
	for _, r := range raw {
		p := r.project()
		out[p.ID] = p
	}
	return out, nil
}

// ProjectVersions lists a project's versions that fit the loaders and game version, newest first.
func ProjectVersions(ctx context.Context, project string, loaders []string, gameVersion string) ([]ContentVersion, error) {
	v := url.Values{}
	if len(loaders) > 0 {
		b, _ := json.Marshal(loaders)
		v.Set("loaders", string(b))
	}
	if gameVersion != "" {
		b, _ := json.Marshal([]string{gameVersion})
		v.Set("game_versions", string(b))
	}
	u := modrinthAPI + "/project/" + url.PathEscape(project) + "/version?" + v.Encode()
	res, err := remote.get(ctx, u, 5*time.Minute, func(ctx context.Context) (any, error) {
		var raw []rawVersion
		if err := fetchJSON(ctx, u, &raw); err != nil {
			return nil, err
		}
		out := []ContentVersion{}
		for _, r := range raw {
			out = append(out, r.version())
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return res.([]ContentVersion), nil
}

func GetVersion(ctx context.Context, id string) (ContentVersion, error) {
	var raw rawVersion
	if err := fetchJSON(ctx, modrinthAPI+"/version/"+url.PathEscape(id), &raw); err != nil {
		return ContentVersion{}, err
	}
	return raw.version(), nil
}

// BestVersion picks what to install: the newest release, else the newest beta, else anything.
func BestVersion(list []ContentVersion) (ContentVersion, bool) {
	for _, want := range []string{"release", "beta", ""} {
		for _, v := range list {
			if want == "" || v.VersionType == want {
				return v, true
			}
		}
	}
	return ContentVersion{}, false
}

// VersionsByHash identifies files: sha1 -> the Modrinth version they are. Unknown files are left out.
func VersionsByHash(ctx context.Context, hashes []string) (map[string]ContentVersion, error) {
	out := map[string]ContentVersion{}
	if len(hashes) == 0 {
		return out, nil
	}
	var raw map[string]rawVersion
	if err := doJSON(ctx, "POST", modrinthAPI+"/version_files", map[string]any{"hashes": hashes, "algorithm": "sha1"}, &raw); err != nil {
		return nil, err
	}
	for h, r := range raw {
		out[h] = r.version()
	}
	return out, nil
}

// Updates finds a newer version for each file that fits the loaders and game version.
func Updates(ctx context.Context, hashes, loaders []string, gameVersion string) (map[string]ContentVersion, error) {
	out := map[string]ContentVersion{}
	if len(hashes) == 0 {
		return out, nil
	}
	body := map[string]any{"hashes": hashes, "algorithm": "sha1", "loaders": loaders}
	if gameVersion != "" {
		body["game_versions"] = []string{gameVersion}
	}
	var raw map[string]rawVersion
	if err := doJSON(ctx, "POST", modrinthAPI+"/version_files/update", body, &raw); err != nil {
		return nil, err
	}
	for h, r := range raw {
		out[h] = r.version()
	}
	return out, nil
}

// Download fetches a content file. Only Modrinth's CDN is allowed, and the size and hash must match.
func Download(ctx context.Context, f File) ([]byte, error) {
	u, err := url.Parse(f.URL)
	if err != nil || u.Scheme != "https" || u.Host != "cdn.modrinth.com" {
		return nil, fmt.Errorf("refusing to download %s: not from Modrinth's CDN", f.URL)
	}
	if f.Size > 256<<20 {
		return nil, fmt.Errorf("%s is too large (%d MB)", f.Filename, f.Size>>20)
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", f.URL, nil)
	req.Header.Set("User-Agent", userAgent)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: couldn't download %s", ErrUnavailable, f.Filename)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: downloading %s: %s", ErrUnavailable, f.Filename, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20+1))
	if err != nil {
		return nil, err
	}
	if f.SHA1 != "" && SHA1(data) != f.SHA1 {
		return nil, fmt.Errorf("%s didn't match its checksum; nothing was installed", f.Filename)
	}
	return data, nil
}
