package games

import (
	"archive/tar"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/runtime"
)

// Data works on a game server's data volume (and its backups volume) through short-lived helper
// containers, so everything here works whether the server is running or stopped. Paths are always
// relative to the volume; CleanPath keeps them inside it.
type Data struct {
	D       *runtime.Docker
	Volume  string // the data volume
	Backups string // the backups volume
	// BackupDir is where this volume's backups go inside the backups volume ("" = its top, which game
	// servers use for their data volume; other volumes get a folder each).
	BackupDir string
}

func (d Data) bdir() string {
	if d.BackupDir == "" {
		return backupDir
	}
	return backupDir + "/" + d.BackupDir
}

const (
	dataDir   = "/data"
	backupDir = "/backups"
	scratch   = ".ignition" // temporary files inside the data volume
)

var (
	ErrOutside = errors.New("that path is outside the server's files")
	ErrIsDir   = errors.New("that's a folder, not a file")
)

// CleanPath turns user input into a path relative to the volume ("" is the volume itself).
func CleanPath(p string) (string, error) {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	if strings.ContainsRune(p, 0) || len(p) > 1024 {
		return "", ErrOutside
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", ErrOutside
		}
	}
	c := strings.TrimPrefix(path.Clean("/"+p), "/")
	if c == "." {
		c = ""
	}
	return c, nil
}

// ValidName is a single file or folder name.
func ValidName(n string) bool {
	return n != "" && n != "." && n != ".." && len(n) <= 255 && !strings.ContainsAny(n, "/\\\x00\r\n")
}

// q quotes a value for sh.
func q(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func abs(rel string) string {
	if rel == "" {
		return dataDir
	}
	return dataDir + "/" + rel
}

// owner hands what was written to whoever owns the volume (the game runs as that user).
const owner = `own() { chown -R "$(stat -c %u:%g /data)" "$@" 2>/dev/null || true; }; `

func (d Data) mounts() map[string]string {
	m := map[string]string{d.Volume: dataDir}
	if d.Backups != "" {
		m[d.Backups] = backupDir
	}
	return m
}

func (d Data) run(ctx context.Context, script string, timeout time.Duration) (string, error) {
	code, out, err := d.D.VolumeRun(ctx, d.mounts(), "set -e; "+owner+script, timeout)
	if err != nil {
		return "", err
	}
	if code != 0 {
		msg := strings.TrimSpace(out)
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		if msg == "" {
			msg = fmt.Sprintf("exit code %d", code)
		}
		return out, errors.New(msg)
	}
	return out, nil
}

type Entry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Dir      bool      `json:"dir"`
	Link     bool      `json:"link"`
	Size     int64     `json:"size"`
	Mode     string    `json:"mode"`
	Modified time.Time `json:"modified"`
}

// List shows a folder's entries, folders first. A missing folder is an empty list.
func (d Data) List(ctx context.Context, dir string) ([]Entry, error) {
	out, err := d.run(ctx, `cd `+q(abs(dir))+` 2>/dev/null || exit 0; for f in * .[!.]* ..?*; do [ -e "$f" ] || [ -L "$f" ] || continue; stat -c '%F|%s|%Y|%a|%n' -- "$f"; done`, time.Minute)
	if err != nil {
		return nil, err
	}
	list := []Entry{}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "|", 5)
		if len(parts) != 5 || parts[4] == scratch && dir == "" {
			continue
		}
		size, _ := strconv.ParseInt(parts[1], 10, 64)
		mod, _ := strconv.ParseInt(parts[2], 10, 64)
		name := parts[4]
		p := name
		if dir != "" {
			p = dir + "/" + name
		}
		list = append(list, Entry{Name: name, Path: p, Dir: parts[0] == "directory", Link: parts[0] == "symbolic link", Size: size, Mode: parts[3], Modified: time.Unix(mod, 0).UTC()})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Dir != list[j].Dir {
			return list[i].Dir
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	return list, nil
}

// Open streams one file. size is -1 when unknown. Close the reader when done.
func (d Data) Open(ctx context.Context, rel string) (io.ReadCloser, int64, error) {
	return d.open(ctx, abs(rel))
}

// OpenBackup streams a backup archive.
func (d Data) OpenBackup(ctx context.Context, name string) (io.ReadCloser, int64, error) {
	if !ValidBackup(name) {
		return nil, 0, ErrNotFound
	}
	return d.open(ctx, d.bdir()+"/"+name)
}

func (d Data) open(ctx context.Context, src string) (io.ReadCloser, int64, error) {
	id, done, err := d.D.VolumeHelper(ctx, d.mounts())
	if err != nil {
		return nil, 0, err
	}
	body, err := d.D.GetArchive(ctx, id, src)
	if err != nil {
		done()
		if runtime.IsNotFound(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	tr := tar.NewReader(body)
	h, err := tr.Next()
	if err != nil || h.Typeflag != tar.TypeReg {
		body.Close()
		done()
		if err == nil {
			return nil, 0, ErrIsDir
		}
		return nil, 0, err
	}
	return &helperReader{Reader: tr, close: func() { body.Close(); done() }}, h.Size, nil
}

// Tar streams a file or folder as a tar archive (for downloads).
func (d Data) Tar(ctx context.Context, rel string) (io.ReadCloser, error) {
	id, done, err := d.D.VolumeHelper(ctx, d.mounts())
	if err != nil {
		return nil, err
	}
	body, err := d.D.GetArchive(ctx, id, abs(rel))
	if err != nil {
		done()
		if runtime.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &helperReader{Reader: body, close: func() { body.Close(); done() }}, nil
}

type helperReader struct {
	io.Reader
	close func()
}

func (h *helperReader) Close() error { h.close(); return nil }

// Read returns a whole file, refusing ones larger than max.
func (d Data) Read(ctx context.Context, rel string, max int64) ([]byte, error) {
	r, size, err := d.Open(ctx, rel)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if size > max {
		return nil, fmt.Errorf("the file is %s; files up to %s open here (download it instead)", HumanSize(size), HumanSize(max))
	}
	return io.ReadAll(io.LimitReader(r, max))
}

// Write puts one file at rel (its folder is created), replacing what's there.
func (d Data) Write(ctx context.Context, rel string, r io.Reader, size int64) error {
	dir, name := path.Split(rel)
	if !ValidName(name) {
		return errors.New("invalid file name")
	}
	dir = strings.TrimSuffix(dir, "/")
	if _, err := d.run(ctx, `mkdir -p `+q(abs(dir)), time.Minute); err != nil {
		return err
	}
	id, done, err := d.D.VolumeHelper(ctx, d.mounts())
	if err != nil {
		return err
	}
	defer done()
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o644, Size: size, ModTime: time.Now()})
		if err == nil {
			_, err = io.CopyN(tw, r, size)
		}
		if err == nil {
			err = tw.Close()
		}
		pw.CloseWithError(err)
	}()
	if err := d.D.PutArchive(ctx, id, abs(dir), pr); err != nil {
		return err
	}
	_, err = d.run(ctx, `own `+q(abs(rel)), time.Minute)
	return err
}

func (d Data) WriteBytes(ctx context.Context, rel string, b []byte) error {
	return d.Write(ctx, rel, strings.NewReader(string(b)), int64(len(b)))
}

func (d Data) Mkdir(ctx context.Context, rel string) error {
	if rel == "" {
		return nil
	}
	_, err := d.run(ctx, `mkdir -p `+q(abs(rel))+`; own `+q(abs(rel)), time.Minute)
	return err
}

func (d Data) Rename(ctx context.Context, from, to string) error {
	if from == "" || to == "" {
		return ErrOutside
	}
	_, err := d.run(ctx, `[ -e `+q(abs(from))+` ] || { echo "It's no longer there."; exit 1; }; [ ! -e `+q(abs(to))+` ] || { echo "Something with that name already exists."; exit 1; }; mkdir -p "$(dirname `+q(abs(to))+`)"; mv `+q(abs(from))+` `+q(abs(to)), time.Minute)
	return err
}

func (d Data) Delete(ctx context.Context, rels ...string) error {
	var args []string
	for _, r := range rels {
		if r == "" {
			return ErrOutside
		}
		args = append(args, q(abs(r)))
	}
	if len(args) == 0 {
		return nil
	}
	_, err := d.run(ctx, `rm -rf -- `+strings.Join(args, " "), 10*time.Minute)
	return err
}

func (d Data) Exists(ctx context.Context, rel string) (bool, error) {
	out, err := d.run(ctx, `[ -e `+q(abs(rel))+` ] && echo yes || echo no`, time.Minute)
	return strings.TrimSpace(out) == "yes", err
}

// Hashes returns sha1 of each file in dir whose name ends with one of the suffixes.
func (d Data) Hashes(ctx context.Context, dir string, suffixes ...string) (map[string]string, error) {
	var globs []string
	for _, s := range suffixes {
		globs = append(globs, "*"+s)
	}
	out, err := d.run(ctx, `cd `+q(abs(dir))+` 2>/dev/null || exit 0; for f in `+strings.Join(globs, " ")+`; do [ -f "$f" ] && sha1sum -- "$f"; done; true`, 5*time.Minute)
	if err != nil {
		return nil, err
	}
	hashes := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if sum, name, ok := strings.Cut(line, "  "); ok && len(sum) == 40 {
			hashes[name] = sum
		}
	}
	return hashes, nil
}

// Usage is the size of the data volume and its biggest folders.
func (d Data) Usage(ctx context.Context) (int64, error) {
	out, err := d.run(ctx, `du -sk /data | cut -f1`, 5*time.Minute)
	if err != nil {
		return 0, err
	}
	kb, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	return kb * 1024, nil
}

/* ---------------------------------- Backups --------------------------------- */

type Backup struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Created time.Time `json:"created"`
	Label   string    `json:"label"`
	Auto    bool      `json:"auto"`
}

var backupRE = regexp.MustCompile(`^(\d{8}-\d{6})(?:-(auto))?(?:-([a-z0-9-]{1,40}))?\.tar\.gz$`)

func BackupName(label string, auto bool, at time.Time) string {
	name := at.UTC().Format("20060102-150405")
	if auto {
		name += "-auto"
	}
	slug := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(label), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug != "" {
		name += "-" + slug
	}
	return name + ".tar.gz"
}

func (d Data) ListBackups(ctx context.Context) ([]Backup, error) {
	out, err := d.run(ctx, `cd `+q(d.bdir())+` 2>/dev/null || exit 0; for f in *.tar.gz; do [ -f "$f" ] && stat -c '%s|%Y|%n' -- "$f"; done; true`, time.Minute)
	if err != nil {
		return nil, err
	}
	list := []Backup{}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		m := backupRE.FindStringSubmatch(parts[2])
		if m == nil {
			continue
		}
		size, _ := strconv.ParseInt(parts[0], 10, 64)
		created, _ := time.Parse("20060102-150405", m[1])
		list = append(list, Backup{Name: parts[2], Size: size, Created: created, Auto: m[2] != "", Label: strings.ReplaceAll(m[3], "-", " ")})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name > list[j].Name })
	return list, nil
}

func ValidBackup(name string) bool { return backupRE.MatchString(name) }

// CreateBackup archives the whole data volume. The game should have saved first (SaveHooks).
func (d Data) CreateBackup(ctx context.Context, name string) (Backup, error) {
	if !ValidBackup(name) {
		return Backup{}, errors.New("invalid backup name")
	}
	tmp := d.bdir() + "/." + name + ".partial"
	if _, err := d.run(ctx, `mkdir -p `+q(d.bdir())+`; rm -f `+q(d.bdir())+`/.*.partial; tar czf `+q(tmp)+` -C /data --exclude ./`+scratch+` . && mv `+q(tmp)+` `+q(d.bdir()+"/"+name), 2*time.Hour); err != nil {
		d.run(context.WithoutCancel(ctx), `rm -f `+q(tmp), time.Minute)
		return Backup{}, fmt.Errorf("the backup failed: %v", err)
	}
	list, err := d.ListBackups(ctx)
	if err != nil {
		return Backup{}, err
	}
	for _, b := range list {
		if b.Name == name {
			return b, nil
		}
	}
	return Backup{}, errors.New("the backup wasn't written")
}

// RestoreBackup replaces everything in the data volume with the backup. Stop the server first.
func (d Data) RestoreBackup(ctx context.Context, name string) error {
	if !ValidBackup(name) {
		return errors.New("invalid backup name")
	}
	_, err := d.run(ctx, `[ -f `+q(d.bdir()+"/"+name)+` ] || { echo "That backup no longer exists."; exit 1; }
gzip -t `+q(d.bdir()+"/"+name)+` || { echo "The backup file is damaged; nothing was changed."; exit 1; }
find /data -mindepth 1 -maxdepth 1 -exec rm -rf {} +
tar xzf `+q(d.bdir()+"/"+name)+` -C /data
own /data`, 2*time.Hour)
	return err
}

func (d Data) DeleteBackup(ctx context.Context, name string) error {
	if !ValidBackup(name) {
		return errors.New("invalid backup name")
	}
	_, err := d.run(ctx, `rm -f `+q(d.bdir()+"/"+name), time.Minute)
	return err
}

// PruneBackups keeps the newest keep automatic backups; ones made by hand are never pruned.
func (d Data) PruneBackups(ctx context.Context, keep int) (int, error) {
	list, err := d.ListBackups(ctx)
	if err != nil {
		return 0, err
	}
	removed, kept := 0, 0
	for _, b := range list {
		if !b.Auto {
			continue
		}
		if kept < keep {
			kept++
			continue
		}
		if d.DeleteBackup(ctx, b.Name) == nil {
			removed++
		}
	}
	return removed, nil
}

/* ----------------------------------- Worlds --------------------------------- */

type World struct {
	Name       string    `json:"name"`
	Active     bool      `json:"active"`
	Size       int64     `json:"size"`
	Modified   time.Time `json:"modified"`
	Companions []string  `json:"companions"` // e.g. world_nether, world_the_end
}

func worldRoot(c WorldConfig) string { return abs(c.Root) }

// Worlds finds every folder under the world root that holds the world marker file.
func (d Data) Worlds(ctx context.Context, c WorldConfig, active string) ([]World, error) {
	out, err := d.run(ctx, `cd `+q(worldRoot(c))+` 2>/dev/null || exit 0; for f in */; do f="${f%/}"; [ -f "$f/`+c.Marker+`" ] || continue; echo "$(du -sk -- "$f" | cut -f1)|$(stat -c %Y -- "$f/`+c.Marker+`")|$f"; done; true`, 5*time.Minute)
	if err != nil {
		return nil, err
	}
	all := map[string]World{}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		kb, _ := strconv.ParseInt(parts[0], 10, 64)
		mod, _ := strconv.ParseInt(parts[1], 10, 64)
		all[parts[2]] = World{Name: parts[2], Size: kb * 1024, Modified: time.Unix(mod, 0).UTC(), Companions: []string{}}
		names = append(names, parts[2])
	}
	// Fold world_nether and world_the_end into world.
	list := []World{}
	for _, n := range names {
		companion := false
		for _, suffix := range c.Companions {
			if base := strings.TrimSuffix(n, suffix); base != n {
				if _, ok := all[base]; ok {
					companion = true
				}
			}
		}
		if companion {
			continue
		}
		w := all[n]
		for _, suffix := range c.Companions {
			if cw, ok := all[n+suffix]; ok {
				w.Size += cw.Size
				w.Companions = append(w.Companions, cw.Name)
			}
		}
		w.Active = n == active
		list = append(list, w)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Active != list[j].Active {
			return list[i].Active
		}
		return list[i].Name < list[j].Name
	})
	return list, nil
}

// WorldPaths are the folders a world is made of (the world and its companions).
func WorldPaths(c WorldConfig, name string) []string {
	base := name
	if c.Root != "" {
		base = c.Root + "/" + name
	}
	paths := []string{base}
	for _, s := range c.Companions {
		paths = append(paths, base+s)
	}
	return paths
}

// ImportWorld unpacks an uploaded .zip (already written to zipRel) as the world name. The world may
// sit at the top of the zip or in a folder inside it.
func (d Data) ImportWorld(ctx context.Context, c WorldConfig, zipRel, name string) error {
	dest := worldRoot(c) + "/" + name
	_, err := d.run(ctx, `t=/data/`+scratch+`/unzip; rm -rf "$t"; mkdir -p "$t"
trap 'rm -rf "$t" `+q(abs(zipRel))+`' EXIT
unzip -q -o `+q(abs(zipRel))+` -d "$t" || { echo "That isn't a zip file the server can read."; exit 1; }
m=$(find "$t" -maxdepth 4 -name `+q(c.Marker)+` -type f | head -n 1)
[ -n "$m" ] || { echo "No world in the zip: there's no `+c.Marker+` inside."; exit 1; }
[ ! -e `+q(dest)+` ] || { echo "A world with that name already exists."; exit 1; }
mkdir -p `+q(worldRoot(c))+`
mv "$(dirname "$m")" `+q(dest)+`
own `+q(dest), 30*time.Minute)
	return err
}

/* ---------------------------------- Helpers --------------------------------- */

func SHA1(b []byte) string {
	h := sha1.Sum(b)
	return hex.EncodeToString(h[:])
}

func HumanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}
