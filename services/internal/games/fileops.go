package games

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// More file operations for the Files page (any resource's volumes, not only games). Everything runs
// in the helper container with the volume at /data; paths are relative and checked by CleanPath.

// Trash lives inside the volume, so moving to it is instant and needs no extra space.
const trashDir = scratch + "/trash"

var modeRE = regexp.MustCompile(`^[0-7]{3,4}$`)

func quoteAll(rels []string) (string, error) {
	var out []string
	for _, r := range rels {
		if r == "" || r == scratch || strings.HasPrefix(r, scratch+"/") {
			return "", ErrOutside
		}
		out = append(out, q(abs(r)))
	}
	if len(out) == 0 {
		return "", errors.New("choose a file or folder")
	}
	return strings.Join(out, " "), nil
}

// CopyInto copies items into dir, keeping their names; a clash gets " copy" (" copy 2"...) added.
func (d Data) CopyInto(ctx context.Context, rels []string, dir string, move bool) ([]string, error) {
	items, err := quoteAll(rels)
	if err != nil {
		return nil, err
	}
	verb := "cp -a"
	if move {
		verb = "mv"
	}
	out, err := d.run(ctx, `dest=`+q(abs(dir))+`; [ -d "$dest" ] || { echo "The destination folder doesn't exist."; exit 1; }
for src in `+items+`; do
  [ -e "$src" ] || { echo "$(basename "$src") is no longer there."; exit 1; }
  case "$dest/" in "$src"/*) echo "A folder can't go inside itself."; exit 1;; esac
  base=$(basename "$src"); stem="$base"; ext=""
  case "$base" in ?*.*) stem="${base%.*}"; ext=".${base##*.}";; esac
  target="$dest/$base"; n=1
  if [ "$(dirname "$src")" = "$dest" ] && [ "`+verb+`" = "mv" ]; then echo "$target"; continue; fi
  while [ -e "$target" ]; do if [ $n = 1 ]; then target="$dest/$stem copy$ext"; else target="$dest/$stem copy $n$ext"; fi; n=$((n+1)); done
  `+verb+` "$src" "$target"; own "$target"; echo "$target"
done`, 30*time.Minute)
	if err != nil {
		return nil, err
	}
	var made []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, dataDir+"/") {
			made = append(made, strings.TrimPrefix(l, dataDir+"/"))
		}
	}
	return made, nil
}

// Duplicate copies each item next to itself.
func (d Data) Duplicate(ctx context.Context, rels []string) ([]string, error) {
	var made []string
	for _, r := range rels {
		dir := path.Dir(r)
		if dir == "." {
			dir = ""
		}
		m, err := d.CopyInto(ctx, []string{r}, dir, false)
		if err != nil {
			return made, err
		}
		made = append(made, m...)
	}
	return made, nil
}

// Touch creates an empty file (refusing to overwrite).
func (d Data) Touch(ctx context.Context, rel string) error {
	_, err := d.run(ctx, `f=`+q(abs(rel))+`; [ ! -e "$f" ] || { echo "Something with that name already exists."; exit 1; }; mkdir -p "$(dirname "$f")"; : > "$f"; own "$f"`, time.Minute)
	return err
}

// Compress archives items (relative to their folder) as name in dir: .zip or .tar.gz.
func (d Data) Compress(ctx context.Context, rels []string, dir, name string) (string, error) {
	if !ValidName(name) {
		return "", errors.New("invalid archive name")
	}
	var names []string
	for _, r := range rels {
		if r == "" || strings.HasPrefix(r, scratch) {
			return "", ErrOutside
		}
		parent := path.Dir(r)
		if parent == "." {
			parent = ""
		}
		if parent != dir {
			return "", errors.New("compress items from one folder at a time")
		}
		names = append(names, q(path.Base(r)))
	}
	target := abs(path.Join(dir, name))
	var cmd string
	switch {
	case strings.HasSuffix(name, ".zip"):
		// busybox has no zip writer; tar can't write zip either, so zip is built by the server
		return "", errors.New("choose .tar.gz")
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		cmd = `tar czf "$t" -C ` + q(abs(dir)) + ` ` + strings.Join(names, " ")
	default:
		return "", errors.New("archives end in .tar.gz")
	}
	_, err := d.run(ctx, `t=`+q(target)+`; [ ! -e "$t" ] || { echo "Something with that name already exists."; exit 1; }; `+cmd+`; own "$t"`, time.Hour)
	return strings.TrimPrefix(target, dataDir+"/"), err
}

// Extract unpacks a .zip, .tar, .tar.gz or .tgz into a new folder next to it.
func (d Data) Extract(ctx context.Context, rel string) (string, error) {
	base := path.Base(rel)
	var stem, cmd string
	switch lower := strings.ToLower(base); {
	case strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".jar"):
		stem, cmd = base[:len(base)-4], `unzip -q -o "$a" -d "$t"`
	case strings.HasSuffix(lower, ".tar.gz"):
		stem, cmd = base[:len(base)-7], `tar xzf "$a" -C "$t"`
	case strings.HasSuffix(lower, ".tgz"):
		stem, cmd = base[:len(base)-4], `tar xzf "$a" -C "$t"`
	case strings.HasSuffix(lower, ".tar"):
		stem, cmd = base[:len(base)-4], `tar xf "$a" -C "$t"`
	default:
		return "", errors.New("only .zip, .tar, .tar.gz and .tgz archives can be extracted")
	}
	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	out, err := d.run(ctx, `a=`+q(abs(rel))+`; [ -f "$a" ] || { echo "The archive is no longer there."; exit 1; }
t=`+q(abs(path.Join(dir, stem)))+`; n=2; while [ -e "$t" ]; do t=`+q(abs(path.Join(dir, stem)))+`" $n"; n=$((n+1)); done
mkdir -p "$t"; `+cmd+` || { rm -rf "$t"; echo "That archive can't be read."; exit 1; }
own "$t"; echo "$t"`, time.Hour)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimPrefix(lines[len(lines)-1], dataDir+"/"), nil
}

// Chmod sets permissions (octal), optionally for everything inside folders too.
func (d Data) Chmod(ctx context.Context, rels []string, mode string, recursive bool) error {
	if !modeRE.MatchString(mode) {
		return errors.New("permissions are three or four octal digits, like 644 or 755")
	}
	items, err := quoteAll(rels)
	if err != nil {
		return err
	}
	flag := ""
	if recursive {
		flag = "-R "
	}
	_, err = d.run(ctx, `chmod `+flag+mode+` `+items, 10*time.Minute)
	return err
}

type FileInfo struct {
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Dir      bool      `json:"dir"`
	Link     string    `json:"link,omitempty"` // a symlink's target
	Size     int64     `json:"size"`           // a folder's total
	Files    int       `json:"files"`          // inside a folder
	Folders  int       `json:"folders"`
	Mode     string    `json:"mode"`  // 755
	Perms    string    `json:"perms"` // drwxr-xr-x
	Owner    string    `json:"owner"` // uid:gid
	Modified time.Time `json:"modified"`
	Accessed time.Time `json:"accessed"`
	SHA256   string    `json:"sha256,omitempty"` // files up to 256 MB
}

// Stat describes one item, measuring folders.
func (d Data) Stat(ctx context.Context, rel string) (FileInfo, error) {
	out, err := d.run(ctx, `f=`+q(abs(rel))+`; [ -e "$f" ] || [ -L "$f" ] || { echo "It's no longer there."; exit 1; }
stat -c '%F|%s|%a|%A|%u:%g|%Y|%X' -- "$f"
if [ -L "$f" ]; then echo "link|$(readlink -- "$f")"; fi
if [ -d "$f" ]; then echo "du|$(du -sk -- "$f" | cut -f1)|$(find "$f" -type f | wc -l)|$(find "$f" -mindepth 1 -type d | wc -l)"
elif [ "$(stat -c %s -- "$f")" -le 268435456 ]; then echo "sum|$(sha256sum -- "$f" | cut -d' ' -f1)"; fi`, 10*time.Minute)
	if err != nil {
		return FileInfo{}, err
	}
	info := FileInfo{Path: rel, Name: path.Base(rel)}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "|")
		switch {
		case len(f) == 7 && !strings.HasPrefix(line, "du|"):
			info.Dir = f[0] == "directory"
			info.Size, _ = strconv.ParseInt(f[1], 10, 64)
			info.Mode, info.Perms, info.Owner = f[2], f[3], f[4]
			m, _ := strconv.ParseInt(f[5], 10, 64)
			a, _ := strconv.ParseInt(f[6], 10, 64)
			info.Modified, info.Accessed = time.Unix(m, 0).UTC(), time.Unix(a, 0).UTC()
		case f[0] == "link" && len(f) == 2:
			info.Link = f[1]
		case f[0] == "du" && len(f) == 4:
			kb, _ := strconv.ParseInt(strings.TrimSpace(f[1]), 10, 64)
			info.Size = kb * 1024
			info.Files, _ = strconv.Atoi(strings.TrimSpace(f[2]))
			info.Folders, _ = strconv.Atoi(strings.TrimSpace(f[3]))
		case f[0] == "sum" && len(f) == 2:
			info.SHA256 = f[1]
		}
	}
	return info, nil
}

/* ----------------------------------- Trash ---------------------------------- */

type TrashItem struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	From    string    `json:"from"` // where it was
	Dir     bool      `json:"dir"`
	Size    int64     `json:"size"`
	Trashed time.Time `json:"trashed"`
}

var trashIDRE = regexp.MustCompile(`^\d{10,}-[0-9a-z]{4,12}$`)

// Trash moves items into the volume's trash, remembering where each came from.
func (d Data) Trash(ctx context.Context, rels []string) error {
	items, err := quoteAll(rels)
	if err != nil {
		return err
	}
	_, err = d.run(ctx, `mkdir -p /data/`+trashDir+`; i=0
for src in `+items+`; do
  [ -e "$src" ] || [ -L "$src" ] || continue
  i=$((i+1)); id="$(date +%s)-$(head -c 6 /dev/urandom | od -An -tx1 | tr -d ' \n' | head -c 8)$i"
  mkdir -p "/data/`+trashDir+`/$id"; printf '%s' "${src#/data/}" > "/data/`+trashDir+`/$id/.from"; mv "$src" "/data/`+trashDir+`/$id/"
done`, 10*time.Minute)
	return err
}

func (d Data) ListTrash(ctx context.Context) ([]TrashItem, error) {
	out, err := d.run(ctx, `cd /data/`+trashDir+` 2>/dev/null || exit 0
for id in */; do id="${id%/}"; [ -f "$id/.from" ] || continue
  for f in "$id"/* "$id"/.[!.]*; do [ -e "$f" ] || continue; [ "$(basename "$f")" = .from ] && continue
    echo "$id|$(cat "$id/.from")|$(stat -c %F -- "$f")|$(du -sk -- "$f" | cut -f1)"; done; done`, 5*time.Minute)
	if err != nil {
		return nil, err
	}
	list := []TrashItem{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "|", 4)
		if len(f) != 4 || !trashIDRE.MatchString(f[0]) {
			continue
		}
		ts, _ := strconv.ParseInt(strings.SplitN(f[0], "-", 2)[0], 10, 64)
		kb, _ := strconv.ParseInt(strings.TrimSpace(f[3]), 10, 64)
		list = append(list, TrashItem{ID: f[0], From: f[1], Name: path.Base(f[1]), Dir: f[2] == "directory", Size: kb * 1024, Trashed: time.Unix(ts, 0).UTC()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Trashed.After(list[j].Trashed) })
	return list, nil
}

// Restore puts trashed items back where they were (with " copy" added if something took the name).
func (d Data) Restore(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if !trashIDRE.MatchString(id) {
			return errors.New("unknown trash item")
		}
		_, err := d.run(ctx, `t=/data/`+trashDir+`/`+id+`; [ -f "$t/.from" ] || { echo "That's no longer in the trash."; exit 1; }
from="/data/$(cat "$t/.from")"; dest=$(dirname "$from"); mkdir -p "$dest"
for f in "$t"/* "$t"/.[!.]*; do [ -e "$f" ] || continue; [ "$(basename "$f")" = .from ] && continue
  target="$from"; n=1; while [ -e "$target" ]; do target="$from copy"; [ $n -gt 1 ] && target="$from copy $n"; n=$((n+1)); done
  mv "$f" "$target"; own "$target"; done
rm -rf "$t"`, 10*time.Minute)
		if err != nil {
			return err
		}
	}
	return nil
}

// EmptyTrash deletes trashed items for good (all of them when ids is empty).
func (d Data) EmptyTrash(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		_, err := d.run(ctx, `rm -rf /data/`+trashDir, 30*time.Minute)
		return err
	}
	var paths []string
	for _, id := range ids {
		if !trashIDRE.MatchString(id) {
			return fmt.Errorf("unknown trash item")
		}
		paths = append(paths, "/data/"+trashDir+"/"+id)
	}
	_, err := d.run(ctx, `rm -rf `+strings.Join(paths, " "), 30*time.Minute)
	return err
}

// Unpack extracts an archive into the folder it's in and removes it. When everything in the archive
// sits inside one folder (site.zip › site/index.html), that folder's contents come up a level.
func (d Data) Unpack(ctx context.Context, rel string) error {
	lower := strings.ToLower(rel)
	cmd := ""
	switch {
	case strings.HasSuffix(lower, ".zip"):
		cmd = `unzip -q -o "$a" -d "$t"`
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		cmd = `tar xzf "$a" -C "$t"`
	case strings.HasSuffix(lower, ".tar"):
		cmd = `tar xf "$a" -C "$t"`
	default:
		return errors.New("upload a .zip, .tar or .tar.gz")
	}
	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	_, err := d.run(ctx, `a=`+q(abs(rel))+`; dest=`+q(abs(dir))+`; t=/data/`+scratch+`/unpack; rm -rf "$t"; mkdir -p "$t"
`+cmd+` || { rm -rf "$t" "$a"; echo "That archive can't be read."; exit 1; }
set -- "$t"/* "$t"/.[!.]*; n=0; one=""; for f in "$@"; do [ -e "$f" ] || continue; n=$((n+1)); one="$f"; done
src="$t"; if [ $n = 1 ] && [ -d "$one" ]; then src="$one"; fi
for f in "$src"/* "$src"/.[!.]*; do [ -e "$f" ] || continue; rm -rf "$dest/$(basename "$f")"; mv "$f" "$dest/"; done
rm -rf "$t" "$a"; own "$dest"`, time.Hour)
	return err
}
