// Package games turns a game server owner's choices into a container, and knows how to talk to that
// game: its console, players, content, settings file and worlds.
//
// Each game is one adapter in its own file that calls Register from init. The API and the dashboard
// are generic: they read the adapter's Manifest to build the setup wizard and the game's pages, and
// use the optional capabilities below when the adapter implements them. Adding a game means writing
// an adapter; nothing else needs to know about it.
package games

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// Spec is what the owner chose. Fields a game doesn't use stay empty.
type Spec struct {
	Edition       string            `json:"edition"`                 // e.g. paper, fabric, bedrock
	Version       string            `json:"version"`                 // game version; "" means the latest release
	LoaderVersion string            `json:"loaderVersion,omitempty"` // mod loader version; "" means the latest for Version
	DisplayName   string            `json:"displayName"`             // shown to players (MOTD, server name)
	Slots         int               `json:"slots"`
	Java          int               `json:"java,omitempty"` // 0 picks the right Java for the version
	CustomURL     string            `json:"customUrl,omitempty"`
	Modpack       *Modpack          `json:"modpack,omitempty"`
	Options       map[string]string `json:"options,omitempty"` // the edition's extra setup fields (difficulty, game mode, seed...)
}

type Modpack struct {
	Provider      string `json:"provider"` // modrinth
	ProjectID     string `json:"projectId"`
	VersionID     string `json:"versionId"`
	Title         string `json:"title"`
	IconURL       string `json:"iconUrl"`
	VersionNumber string `json:"versionNumber"`
	GameVersion   string `json:"gameVersion"`
	Loader        string `json:"loader"`
}

func ParseSpec(raw []byte) (Spec, error) {
	var s Spec
	if len(raw) == 0 {
		return s, nil
	}
	err := json.Unmarshal(raw, &s)
	return s, err
}

/* --------------------------------- Manifest --------------------------------- */

// Manifest describes a game to the dashboard: the wizard's choices and which pages apply.
type Manifest struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Editions    []Edition  `json:"editions"`
	Fields      []Field    `json:"fields"` // extra setup fields, shown after name and slots
	Slots       Range      `json:"slots"`
	Memory      MemoryHint `json:"memory"`
	EULA        *Agreement `json:"eula,omitempty"`
	Sections    []string   `json:"sections"` // game pages: Console, Players, Content, Worlds, Properties, Files, Backups, Schedules
}

type Edition struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Group       string        `json:"group"` // e.g. "Java Edition"
	Description string        `json:"description"`
	Recommended bool          `json:"recommended,omitempty"`
	Versions    string        `json:"versions"`         // list, free (typed), modpack (comes from the pack)
	Loader      string        `json:"loader,omitempty"` // label of the loader version choice, "" when there is none
	Snapshots   bool          `json:"snapshots,omitempty"`
	Content     []ContentKind `json:"content"`
	Modpacks    bool          `json:"modpacks,omitempty"`
	CustomJar   bool          `json:"customJar,omitempty"`
	Modded      bool          `json:"modded,omitempty"` // needs more memory
}

// ContentKind is something players add to the server: mods, plugins. Provider is where to find it.
type ContentKind struct {
	ID          string   `json:"id"`    // mod, plugin
	Label       string   `json:"label"` // Mods, Plugins
	Dir         string   `json:"dir"`   // folder under the data volume
	Provider    string   `json:"provider"`
	ProjectType string   `json:"projectType"`
	Loaders     []string `json:"loaders"`
}

type Field struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Kind    string   `json:"kind"` // select, text, bool
	Options []Option `json:"options,omitempty"`
	Default string   `json:"default"`
	Hint    string   `json:"hint,omitempty"`
	For     []string `json:"for,omitempty"` // editions it applies to; empty means all
}

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
}

type Range struct{ Min, Max, Default int }

func (r Range) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]int{"min": r.Min, "max": r.Max, "default": r.Default})
}

// MemoryHint suggests a memory limit: Base + PerSlot*slots, plus Modded for modded editions.
type MemoryHint struct {
	MinMB     int `json:"minMb"`
	BaseMB    int `json:"baseMb"`
	PerSlotMB int `json:"perSlotMb"`
	ModdedMB  int `json:"moddedMb"`
	MaxMB     int `json:"maxMb"`
}

type Agreement struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// VersionList is what the version picker shows, newest first.
type VersionList struct {
	Versions []Version           `json:"versions"`
	Latest   string              `json:"latest"`
	Loaders  map[string][]Option `json:"loaders,omitempty"` // game version -> loader versions, newest first
	Note     string              `json:"note,omitempty"`
}

type Version struct {
	ID     string `json:"id"`
	Stable bool   `json:"stable"`
}

/* --------------------------------- Adapters --------------------------------- */

// Runtime is the container an adapter asks for.
type Runtime struct {
	Image     string
	Env       map[string]string
	Ports     []Port
	DataPath  string // the game's data volume mount
	Marker    string // a file under DataPath that exists once the game has set itself up
	StopGrace int    // seconds to save and shut down
}

type Port struct {
	Name      string `json:"name"`
	Container int    `json:"container"`
	Protocol  string `json:"protocol"`
	Preferred int    `json:"preferred"` // the host port players expect; the next free one is used when taken
}

type Adapter interface {
	Manifest() Manifest
	Versions(ctx context.Context, edition string) (VersionList, error)
	// Normalize fills in defaults and validates; field keys match the wizard's.
	Normalize(s *Spec) map[string]string
	// Runtime builds the container. firstRun is true until the game has written its own settings, so
	// setup values (name, slots, difficulty) are only passed once and later edits aren't overwritten.
	Runtime(s Spec, memoryMB int, firstRun bool) (Runtime, error)
}

// Optional capabilities. The API checks for each one.

// Console sends one line to the server. replies is false when the answer only shows in the log.
type Console interface {
	ConsoleCommand(s Spec, line string) (cmd []string, replies bool)
	QuickCommands(s Spec) []QuickCommand
	Announce(s Spec, message string) string // a console line that broadcasts message to players
}

type QuickCommand struct {
	Label   string `json:"label"`
	Command string `json:"command"`
	Hint    string `json:"hint,omitempty"`
}

// Pinger asks the running server for its status from inside the container.
type Pinger interface {
	StatusCommand(s Spec) []string
	ParseStatus(out string) (Status, bool)
}

type Status struct {
	Online     bool     `json:"online"`
	Players    int      `json:"players"`
	MaxPlayers int      `json:"maxPlayers"`
	Version    string   `json:"version,omitempty"`
	MOTD       string   `json:"motd,omitempty"`
	Names      []string `json:"names"`
	NamesKnown bool     `json:"namesKnown"`
}

type PlayerSupport interface {
	Players(s Spec) PlayerConfig
}

type PlayerConfig struct {
	OnlineCommand string                    `json:"-"` // console line listing who's online
	ParseOnline   func(out string) []string `json:"-"`
	Lists         []PlayerList              `json:"lists"`
	Actions       []PlayerAction            `json:"actions"`
	NameRule      string                    `json:"nameRule"` // a regexp the dashboard checks names against
}

// PlayerList is a list kept in a file: whitelist, operators, bans.
type PlayerList struct {
	ID          string  `json:"id"`
	Label       string  `json:"label"`
	Description string  `json:"description"`
	File        string  `json:"-"`
	Add         string  `json:"add,omitempty"`    // console template, {player} and {reason}
	Remove      string  `json:"remove,omitempty"` // console template
	Toggle      *Toggle `json:"toggle,omitempty"`
	Key         string  `json:"-"` // field holding the name ("name", or "ip" for IP bans)
}

type Toggle struct {
	Property string `json:"property"`
	On       string `json:"on"`
	Off      string `json:"off"`
}

type PlayerAction struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Template string   `json:"-"`
	Danger   bool     `json:"danger,omitempty"`
	Input    string   `json:"input,omitempty"`   // label of a free-text value ({reason}/{message}), "" when none
	Options  []Option `json:"options,omitempty"` // fixed choices for {value}
}

type PropertySupport interface {
	Properties(s Spec) PropertyConfig
}

type PropertyConfig struct {
	File   string            `json:"file"`
	Groups []PropertyGroup   `json:"groups"`
	Locked map[string]string `json:"locked"` // key -> why Ignition manages it
}

type PropertyGroup struct {
	Label string     `json:"label"`
	Props []Property `json:"props"`
}

type Property struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Kind    string   `json:"kind"` // bool, int, text, select
	Options []Option `json:"options,omitempty"`
	Min     *int     `json:"min,omitempty"`
	Max     *int     `json:"max,omitempty"`
	Hint    string   `json:"hint,omitempty"`
}

type WorldSupport interface {
	Worlds(s Spec) WorldConfig
}

type WorldConfig struct {
	Root          string   `json:"root"`          // folder under the data volume holding worlds ("" = the volume itself)
	Marker        string   `json:"marker"`        // a file every world folder has
	LevelProperty string   `json:"levelProperty"` // property naming the active world
	DefaultLevel  string   `json:"defaultLevel"`
	SeedProperty  string   `json:"seedProperty"`
	Companions    []string `json:"companions"` // suffixes of folders that belong to a world (world_nether)
}

// SaveSupport makes backups consistent: console lines to run before and after copying the files.
type SaveSupport interface {
	SaveHooks(s Spec) (before, after []string)
}

/* --------------------------------- Registry --------------------------------- */

var (
	mu       sync.RWMutex
	adapters = map[string]Adapter{}
)

func Register(a Adapter) {
	mu.Lock()
	defer mu.Unlock()
	adapters[a.Manifest().ID] = a
}

func Get(id string) (Adapter, bool) {
	mu.RLock()
	defer mu.RUnlock()
	a, ok := adapters[id]
	return a, ok
}

func All() []Adapter {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Adapter, 0, len(adapters))
	for _, a := range adapters {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest().Name < out[j].Manifest().Name })
	return out
}

func EditionOf(a Adapter, id string) (Edition, bool) {
	for _, e := range a.Manifest().Editions {
		if e.ID == id {
			return e, true
		}
	}
	return Edition{}, false
}

// ContentKinds is what can be added to this server: the edition's kinds, with the loader a modpack decided.
func ContentKinds(a Adapter, s Spec) []ContentKind {
	e, _ := EditionOf(a, s.Edition)
	kinds := append([]ContentKind{}, e.Content...) // [] rather than null for editions without add-ons
	if s.Modpack != nil && s.Modpack.Loader != "" {
		for i := range kinds {
			if kinds[i].ID == "mod" {
				kinds[i].Loaders = []string{s.Modpack.Loader}
			}
		}
	}
	return kinds
}

// GameVersion is the game version content must support ("" when unknown).
func GameVersion(s Spec) string {
	if s.Modpack != nil && s.Modpack.GameVersion != "" {
		return s.Modpack.GameVersion
	}
	return s.Version
}

/* ----------------------------------- Cache ---------------------------------- */

// cache keeps remote lookups (version lists, content searches) for a while, and serves the last good
// answer when the source is down.
type cache struct {
	mu   sync.Mutex
	data map[string]cached
}

type cached struct {
	v  any
	at time.Time
}

var remote = &cache{data: map[string]cached{}}

func (c *cache) get(ctx context.Context, key string, ttl time.Duration, load func(context.Context) (any, error)) (any, error) {
	c.mu.Lock()
	hit, ok := c.data[key]
	c.mu.Unlock()
	if ok && time.Since(hit.at) < ttl {
		return hit.v, nil
	}
	v, err := load(ctx)
	if err != nil {
		if ok {
			return hit.v, nil // stale beats nothing
		}
		return nil, err
	}
	c.mu.Lock()
	if len(c.data) > 2000 {
		c.data = map[string]cached{}
	}
	c.data[key] = cached{v, time.Now()}
	c.mu.Unlock()
	return v, nil
}

var ErrUnavailable = errors.New("unavailable")

// Fill replaces {player}, {reason}, {message} and {value} in a console template. Values are kept to
// one line so they can't start a second command.
func Fill(template string, values map[string]string) string {
	out := template
	for k, v := range values {
		v = strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(v)), " ")
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(out), " "))
}
