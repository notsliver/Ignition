package games

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Minecraft runs on the itzg/minecraft-server images (Java) and itzg/minecraft-bedrock-server. They
// install every server type and loader themselves; this adapter only decides what to ask for.

func init() { Register(minecraft{}) }

type minecraft struct{}

var (
	javaContent = func(kinds ...ContentKind) []ContentKind { return kinds }
	plugins     = ContentKind{ID: "plugin", Label: "Plugins", Dir: "plugins", Provider: "modrinth", ProjectType: "plugin", Loaders: []string{"paper", "spigot", "bukkit"}}
	modsFor     = func(loader string) ContentKind {
		return ContentKind{ID: "mod", Label: "Mods", Dir: "mods", Provider: "modrinth", ProjectType: "mod", Loaders: []string{loader}}
	}
	mcVersionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,39}$`)
)

func (minecraft) Manifest() Manifest {
	return Manifest{
		ID: "minecraft", Name: "Minecraft",
		Description: "Java and Bedrock servers: Paper, Fabric, Forge, NeoForge, Mohist, vanilla, modpacks and custom JARs.",
		Editions: []Edition{
			{ID: "paper", Name: "Paper", Group: "Java Edition", Recommended: true, Versions: "list", Content: javaContent(plugins),
				Description: "Fast and stable, and runs Bukkit and Spigot plugins. The best choice for most servers."},
			{ID: "fabric", Name: "Fabric", Group: "Java Edition", Versions: "list", Loader: "Fabric loader", Modded: true, Content: javaContent(modsFor("fabric")),
				Description: "A lightweight mod loader that updates quickly. Home of most performance and utility mods."},
			{ID: "neoforge", Name: "NeoForge", Group: "Java Edition", Versions: "list", Loader: "NeoForge version", Modded: true, Content: javaContent(modsFor("neoforge")),
				Description: "Forge's modern successor. Most new big mods for 1.20.2 and later target it."},
			{ID: "forge", Name: "Forge", Group: "Java Edition", Versions: "list", Loader: "Forge version", Modded: true, Content: javaContent(modsFor("forge")),
				Description: "The classic mod loader. Large modpacks for 1.20.1 and older usually need it."},
			{ID: "mohist", Name: "Mohist", Group: "Java Edition", Versions: "list", Modded: true, Content: javaContent(modsFor("forge"), plugins),
				Description: "Forge mods and Bukkit plugins on the same server. Handy, but less stable than either alone."},
			{ID: "vanilla", Name: "Vanilla", Group: "Java Edition", Versions: "list", Snapshots: true, Content: []ContentKind{},
				Description: "The official server from Mojang, exactly as released. No mods or plugins."},
			{ID: "modpack", Name: "Modpack", Group: "Java Edition", Versions: "modpack", Modpacks: true, Modded: true, Content: javaContent(modsFor("fabric")),
				Description: "Pick a Modrinth modpack. The right loader, version and mods come with it."},
			{ID: "custom", Name: "Custom JAR", Group: "Java Edition", Versions: "free", CustomJar: true, Content: []ContentKind{},
				Description: "Run your own server .jar from a download link: Purpur, Folia, a fork, anything."},
			{ID: "bedrock", Name: "Bedrock", Group: "Bedrock Edition", Versions: "list", Content: []ContentKind{},
				Description: "For players on phones, consoles and Windows. Add-ons are uploaded as files; mods and plugins don't exist for Bedrock."},
		},
		Fields: []Field{
			{Key: "gamemode", Label: "Game mode", Kind: "select", Default: "survival", Options: []Option{
				{Value: "survival", Label: "Survival"}, {Value: "creative", Label: "Creative"}, {Value: "adventure", Label: "Adventure"}}},
			{Key: "difficulty", Label: "Difficulty", Kind: "select", Default: "easy", Options: []Option{
				{Value: "peaceful", Label: "Peaceful"}, {Value: "easy", Label: "Easy"}, {Value: "normal", Label: "Normal"}, {Value: "hard", Label: "Hard"}}},
			{Key: "levelType", Label: "World type", Kind: "select", Default: "normal", For: []string{"paper", "fabric", "neoforge", "forge", "mohist", "vanilla", "custom"}, Options: []Option{
				{Value: "normal", Label: "Normal"}, {Value: "flat", Label: "Superflat"}, {Value: "large_biomes", Label: "Large biomes"}, {Value: "amplified", Label: "Amplified", Hint: "Needs a lot more CPU"}}},
			{Key: "seed", Label: "World seed", Kind: "text", Default: "", Hint: "Leave empty for a random world."},
			{Key: "whitelist", Label: "Only let in players I add", Kind: "bool", Default: "false", Hint: "A whitelist (allow list on Bedrock). You can switch it any time under Players."},
		},
		Slots:    Range{Min: 1, Max: 500, Default: 20},
		Memory:   MemoryHint{MinMB: 1024, BaseMB: 1536, PerSlotMB: 32, ModdedMB: 2048, MaxMB: 65536},
		EULA:     &Agreement{Label: "Minecraft End User License Agreement", URL: "https://aka.ms/MinecraftEULA"},
		Sections: []string{"Console", "Players", "Content", "Worlds", "Properties", "Files", "Backups", "Schedules"},
	}
}

/* --------------------------------- Versions --------------------------------- */

func (m minecraft) Versions(ctx context.Context, edition string) (VersionList, error) {
	v, err := remote.get(ctx, "mc-versions:"+edition, time.Hour, func(ctx context.Context) (any, error) { return m.fetchVersions(ctx, edition) })
	if err != nil {
		return VersionList{}, err
	}
	return v.(VersionList), nil
}

func (minecraft) fetchVersions(ctx context.Context, edition string) (VersionList, error) {
	switch edition {
	case "vanilla":
		var doc struct {
			Latest   struct{ Release string }
			Versions []struct{ ID, Type string }
		}
		if err := fetchJSON(ctx, "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json", &doc); err != nil {
			return VersionList{}, err
		}
		out := VersionList{Latest: doc.Latest.Release}
		for _, v := range doc.Versions {
			if v.Type == "release" || v.Type == "snapshot" {
				out.Versions = append(out.Versions, Version{ID: v.ID, Stable: v.Type == "release"})
			}
		}
		return out, nil
	case "paper":
		var doc struct {
			Versions []struct {
				Version struct{ ID string } `json:"version"`
				Builds  []int               `json:"builds"`
			} `json:"versions"`
		}
		if err := fetchJSON(ctx, "https://fill.papermc.io/v3/projects/paper/versions", &doc); err != nil {
			return VersionList{}, err
		}
		var out VersionList
		for _, v := range doc.Versions {
			if len(v.Builds) == 0 {
				continue
			}
			id := v.Version.ID
			out.Versions = append(out.Versions, Version{ID: id, Stable: !strings.ContainsAny(id, "-")})
		}
		sortVersions(out.Versions)
		out.Latest = latestStable(out.Versions)
		return out, nil
	case "fabric":
		var gamesList []struct {
			Version string
			Stable  bool
		}
		var loaders []struct {
			Version string
			Stable  bool
		}
		if err := fetchJSON(ctx, "https://meta.fabricmc.net/v2/versions/game", &gamesList); err != nil {
			return VersionList{}, err
		}
		if err := fetchJSON(ctx, "https://meta.fabricmc.net/v2/versions/loader", &loaders); err != nil {
			return VersionList{}, err
		}
		out := VersionList{Loaders: map[string][]Option{}}
		for _, g := range gamesList {
			out.Versions = append(out.Versions, Version{ID: g.Version, Stable: g.Stable})
		}
		for i, l := range loaders {
			if i >= 40 {
				break
			}
			label := l.Version
			if !l.Stable {
				label += " (beta)"
			}
			out.Loaders["*"] = append(out.Loaders["*"], Option{Value: l.Version, Label: label})
		}
		out.Latest = latestStable(out.Versions)
		return out, nil
	case "forge":
		var doc struct{ Promos map[string]string }
		if err := fetchJSON(ctx, "https://files.minecraftforge.net/net/minecraftforge/forge/promotions_slim.json", &doc); err != nil {
			return VersionList{}, err
		}
		out := VersionList{Loaders: map[string][]Option{}}
		seen := map[string]bool{}
		for key := range doc.Promos {
			mc, _, ok := strings.Cut(key, "-")
			if ok && !seen[mc] {
				seen[mc] = true
				out.Versions = append(out.Versions, Version{ID: mc, Stable: true})
			}
		}
		sortVersions(out.Versions)
		for _, v := range out.Versions {
			rec, latest := doc.Promos[v.ID+"-recommended"], doc.Promos[v.ID+"-latest"]
			if rec != "" {
				out.Loaders[v.ID] = append(out.Loaders[v.ID], Option{Value: rec, Label: rec + " (recommended)"})
			}
			if latest != "" && latest != rec {
				out.Loaders[v.ID] = append(out.Loaders[v.ID], Option{Value: latest, Label: latest + " (latest)"})
			}
		}
		out.Latest = latestStable(out.Versions)
		return out, nil
	case "neoforge":
		var doc struct{ Versions []string }
		if err := fetchJSON(ctx, "https://maven.neoforged.net/api/maven/versions/releases/net/neoforged/neoforge", &doc); err != nil {
			return VersionList{}, err
		}
		out := VersionList{Loaders: map[string][]Option{}}
		stable := map[string]bool{}
		for i := len(doc.Versions) - 1; i >= 0; i-- { // newest last in the feed
			nv := doc.Versions[i]
			mc := NeoForgeGameVersion(nv)
			if mc == "" {
				continue
			}
			beta := strings.Contains(nv, "beta")
			if _, ok := out.Loaders[mc]; !ok {
				out.Versions = append(out.Versions, Version{ID: mc})
			}
			if len(out.Loaders[mc]) < 25 {
				label := nv
				if beta {
					label += " (beta)"
				}
				out.Loaders[mc] = append(out.Loaders[mc], Option{Value: nv, Label: label})
			}
			stable[mc] = stable[mc] || !beta
		}
		for i := range out.Versions {
			out.Versions[i].Stable = stable[out.Versions[i].ID]
		}
		sortVersions(out.Versions)
		out.Latest = latestStable(out.Versions)
		return out, nil
	case "mohist":
		var list []struct{ Name string }
		if err := fetchJSON(ctx, "https://api.mohistmc.com/project/mohist/versions", &list); err != nil {
			return VersionList{}, err
		}
		var out VersionList
		for _, v := range list {
			out.Versions = append(out.Versions, Version{ID: v.Name, Stable: true})
		}
		sortVersions(out.Versions)
		out.Latest = latestStable(out.Versions)
		return out, nil
	case "bedrock":
		return VersionList{Latest: "LATEST", Versions: []Version{{ID: "LATEST", Stable: true}, {ID: "PREVIEW"}},
			Note: "Bedrock servers follow Mojang's releases. Pick Latest, Preview, or type an exact version such as 1.21.50.07."}, nil
	}
	return VersionList{Versions: []Version{}}, nil
}

// NeoForgeGameVersion maps a NeoForge version to the Minecraft version it's for: 21.1.77 is for
// 1.21.1, 20.4.237 for 1.20.4, 21.0.x for 1.21, and from 2026 on 26.1.2.5 is for 26.1.2.
func NeoForgeGameVersion(v string) string {
	core, _, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) < 3 {
		return ""
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 20 {
		return "" // 0.25w14craftmine and other experiments
	}
	if major >= 25 { // year-based Minecraft versions
		if len(parts) < 4 {
			return ""
		}
		if parts[2] == "0" {
			return parts[0] + "." + parts[1]
		}
		return parts[0] + "." + parts[1] + "." + parts[2]
	}
	if parts[1] == "0" {
		return "1." + parts[0]
	}
	return "1." + parts[0] + "." + parts[1]
}

// JavaFor is the Java release a Minecraft version runs on.
func JavaFor(version string) int {
	nums := versionNumbers(version)
	switch {
	case len(nums) == 0:
		return 21
	case nums[0] >= 25 && !strings.Contains(version, "w"): // 26.1 and later
		return 25
	case nums[0] != 1 || len(nums) < 2: // weekly snapshots like 24w14a
		return 21
	case nums[1] <= 16:
		return 8
	case nums[1] <= 19:
		return 17
	case nums[1] == 20 && (len(nums) < 3 || nums[2] < 5):
		return 17
	default:
		return 21
	}
}

func versionNumbers(v string) []int {
	var out []int
	for _, f := range regexp.MustCompile(`\d+`).FindAllString(strings.SplitN(v, "-", 2)[0], -1) {
		n, _ := strconv.Atoi(f)
		out = append(out, n)
	}
	return out
}

// CompareVersions orders dotted versions numerically (1.20.10 after 1.20.9).
func CompareVersions(a, b string) int {
	x, y := versionNumbers(a), versionNumbers(b)
	for i := 0; i < len(x) || i < len(y); i++ {
		var p, q int
		if i < len(x) {
			p = x[i]
		}
		if i < len(y) {
			q = y[i]
		}
		if p != q {
			if p < q {
				return -1
			}
			return 1
		}
	}
	// 1.21 before 1.21-rc1? A release sorts after its pre-releases.
	switch pa, pb := strings.Contains(a, "-"), strings.Contains(b, "-"); {
	case pa && !pb:
		return -1
	case !pa && pb:
		return 1
	}
	return strings.Compare(a, b)
}

func sortVersions(v []Version) {
	sort.SliceStable(v, func(i, j int) bool { return CompareVersions(v[i].ID, v[j].ID) > 0 })
}

func latestStable(v []Version) string {
	for _, x := range v {
		if x.Stable {
			return x.ID
		}
	}
	if len(v) > 0 {
		return v[0].ID
	}
	return ""
}

/* -------------------------------- Normalize --------------------------------- */

var (
	difficulties = []string{"peaceful", "easy", "normal", "hard"}
	gamemodes    = []string{"survival", "creative", "adventure"}
	levelTypes   = []string{"normal", "flat", "large_biomes", "amplified"}
	javaReleases = []int{8, 11, 17, 21, 25}
)

func (m minecraft) Normalize(s *Spec) map[string]string {
	f := map[string]string{}
	e, ok := EditionOf(m, s.Edition)
	if !ok {
		f["edition"] = "Choose a server type."
		return f
	}
	man := m.Manifest()
	s.DisplayName = strings.Join(strings.Fields(s.DisplayName), " ")
	if s.DisplayName == "" {
		f["displayName"] = "Name your server. Players see it in their server list."
	} else if len([]rune(s.DisplayName)) > 59 {
		f["displayName"] = "Up to 59 characters."
	}
	if s.Slots == 0 {
		s.Slots = man.Slots.Default
	}
	if s.Slots < man.Slots.Min || s.Slots > man.Slots.Max {
		f["slots"] = fmt.Sprintf("Between %d and %d players.", man.Slots.Min, man.Slots.Max)
	}
	s.Version, s.LoaderVersion = strings.TrimSpace(s.Version), strings.TrimSpace(s.LoaderVersion)
	if s.Version != "" && !mcVersionRE.MatchString(s.Version) {
		f["version"] = "That doesn't look like a Minecraft version."
	}
	if s.LoaderVersion != "" && (e.Loader == "" || !mcVersionRE.MatchString(s.LoaderVersion)) {
		f["loaderVersion"] = "That doesn't look like a loader version."
	}
	switch {
	case e.CustomJar:
		u, err := url.Parse(strings.TrimSpace(s.CustomURL))
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			f["customUrl"] = "Paste a direct download link to the server .jar (http or https)."
		}
		s.CustomURL = strings.TrimSpace(s.CustomURL)
		if s.Java == 0 {
			s.Java = JavaFor(s.Version)
		}
	case e.Modpacks:
		if s.Modpack == nil || s.Modpack.ProjectID == "" {
			f["modpack"] = "Choose a modpack."
		}
		s.Version = ""
	default:
		s.CustomURL, s.Modpack = "", nil
		if e.Versions == "list" && s.Version == "" {
			f["version"] = "Choose a version."
		}
	}
	if s.Java != 0 && !contains(javaReleases, s.Java) {
		f["java"] = "Choose Java 8, 11, 17, 21 or 25."
	}
	opts := map[string]string{}
	for _, fd := range man.Fields {
		if len(fd.For) > 0 && !containsStr(fd.For, s.Edition) {
			continue
		}
		v := strings.TrimSpace(s.Options[fd.Key])
		if v == "" {
			v = fd.Default
		}
		switch fd.Key {
		case "difficulty":
			if !containsStr(difficulties, v) {
				f["difficulty"] = "Choose a difficulty."
			}
		case "gamemode":
			if !containsStr(gamemodes, v) {
				f["gamemode"] = "Choose a game mode."
			}
		case "levelType":
			if !containsStr(levelTypes, v) {
				f["levelType"] = "Choose a world type."
			}
		case "seed":
			if len(v) > 64 || strings.ContainsAny(v, "\r\n") {
				f["seed"] = "Up to 64 characters."
			}
		case "whitelist":
			if v != "true" && v != "false" {
				v = "false"
			}
		}
		if v != "" {
			opts[fd.Key] = v
		}
	}
	s.Options = opts
	return f
}

/* --------------------------------- Runtime ---------------------------------- */

const (
	javaPort    = 25565
	bedrockPort = 19132
)

// HeapMB leaves room outside the Java heap for the JVM itself, threads and native memory.
func HeapMB(limitMB int) int {
	reserve := limitMB / 5
	if reserve < 512 {
		reserve = 512
	}
	heap := limitMB - reserve
	if heap < 512 {
		heap = 512
	}
	return heap
}

func (m minecraft) Runtime(s Spec, memoryMB int, firstRun bool) (Runtime, error) {
	if s.Edition == "bedrock" {
		version := s.Version
		if version == "" || strings.EqualFold(version, "latest") || strings.EqualFold(version, "preview") {
			version = orDefault(strings.ToUpper(version), "LATEST")
		}
		env := map[string]string{"EULA": "TRUE", "VERSION": version, "SERVER_PORT": strconv.Itoa(bedrockPort)}
		if firstRun {
			env["SERVER_NAME"] = s.DisplayName
			env["MAX_PLAYERS"] = strconv.Itoa(s.Slots)
			env["GAMEMODE"] = orDefault(s.Options["gamemode"], "survival")
			env["DIFFICULTY"] = orDefault(s.Options["difficulty"], "easy")
			if seed := s.Options["seed"]; seed != "" {
				env["LEVEL_SEED"] = seed
			}
			if s.Options["whitelist"] == "true" {
				env["ALLOW_LIST"] = "true"
			}
		}
		return Runtime{Image: "itzg/minecraft-bedrock-server:latest", Env: env, DataPath: "/data", Marker: "server.properties", StopGrace: 30,
			Ports: []Port{{Name: "Game", Container: bedrockPort, Protocol: "udp", Preferred: bedrockPort}}}, nil
	}

	java := s.Java
	gameVersion := GameVersion(s)
	if java == 0 {
		java = JavaFor(gameVersion)
	}
	env := map[string]string{
		"EULA": "TRUE", "MEMORY": fmt.Sprintf("%dM", HeapMB(memoryMB)), "USE_AIKAR_FLAGS": "true",
		"ENABLE_RCON": "true", "SERVER_PORT": strconv.Itoa(javaPort), "CREATE_CONSOLE_IN_PIPE": "true",
	}
	switch s.Edition {
	case "modpack":
		env["TYPE"] = "MODRINTH"
		env["MODRINTH_MODPACK"] = s.Modpack.ProjectID
		if s.Modpack.VersionID != "" {
			env["MODRINTH_VERSION"] = s.Modpack.VersionID
		}
	case "custom":
		env["TYPE"] = "CUSTOM"
		env["CUSTOM_SERVER"] = s.CustomURL
		if s.Version != "" {
			env["VERSION"] = s.Version
		}
	default:
		env["TYPE"] = strings.ToUpper(s.Edition)
		env["VERSION"] = s.Version
		if s.LoaderVersion != "" {
			key := map[string]string{"fabric": "FABRIC_LOADER_VERSION", "forge": "FORGE_VERSION", "neoforge": "NEOFORGE_VERSION"}[s.Edition]
			if key != "" {
				env[key] = s.LoaderVersion
			}
		}
	}
	if firstRun {
		env["MOTD"] = s.DisplayName
		env["MAX_PLAYERS"] = strconv.Itoa(s.Slots)
		env["MODE"] = orDefault(s.Options["gamemode"], "survival")
		env["DIFFICULTY"] = orDefault(s.Options["difficulty"], "easy")
		if seed := s.Options["seed"]; seed != "" {
			env["SEED"] = seed
		}
		if lt := s.Options["levelType"]; lt != "" && lt != "normal" {
			env["LEVEL_TYPE"] = levelType(lt, gameVersion)
		}
		if s.Options["whitelist"] == "true" {
			env["ENABLE_WHITELIST"] = "true"
		}
	}
	return Runtime{Image: fmt.Sprintf("itzg/minecraft-server:java%d", java), Env: env, DataPath: "/data", Marker: "server.properties", StopGrace: 60,
		Ports: []Port{{Name: "Game", Container: javaPort, Protocol: "tcp", Preferred: javaPort}}}, nil
}

// levelType names a world type the way the version expects: minecraft:flat since 1.19, FLAT before.
func levelType(t, version string) string {
	if n := versionNumbers(version); len(n) >= 2 && n[0] == 1 && n[1] < 19 {
		return map[string]string{"flat": "FLAT", "large_biomes": "LARGEBIOMES", "amplified": "AMPLIFIED"}[t]
	}
	return "minecraft:" + t
}

/* ------------------------------ Capabilities -------------------------------- */

func (minecraft) ConsoleCommand(s Spec, line string) ([]string, bool) {
	line = strings.TrimPrefix(strings.TrimSpace(line), "/")
	if s.Edition == "bedrock" {
		return []string{"send-command", line}, false
	}
	return []string{"rcon-cli", line}, true
}

func (minecraft) QuickCommands(s Spec) []QuickCommand {
	q := []QuickCommand{
		{Label: "Save the world", Command: "save-all", Hint: "Writes everything to disk now"},
		{Label: "Make it day", Command: "time set day"},
		{Label: "Make it night", Command: "time set night"},
		{Label: "Clear weather", Command: "weather clear"},
		{Label: "Who's online", Command: "list"},
	}
	if s.Edition == "bedrock" {
		return q[1:]
	}
	if s.Edition == "paper" || s.Edition == "mohist" {
		q = append(q, QuickCommand{Label: "Ticks per second", Command: "tps", Hint: "20 is perfect; lower means lag"})
	}
	return q
}

func (minecraft) Announce(_ Spec, message string) string { return "say " + message }

func (minecraft) StatusCommand(s Spec) []string {
	if s.Edition == "bedrock" {
		return []string{"mc-monitor", "status-bedrock", "-host", "127.0.0.1", "-port", strconv.Itoa(bedrockPort), "-json", "-timeout", "4s"}
	}
	return []string{"mc-monitor", "status", "-host", "127.0.0.1", "-port", strconv.Itoa(javaPort), "-json", "-timeout", "4s"}
}

var colorCodes = regexp.MustCompile(`§.`)

// ParseStatus reads mc-monitor's JSON. Java servers report a sample of online names; Bedrock only counts.
func (minecraft) ParseStatus(out string) (Status, bool) {
	var doc struct {
		Online bool `json:"online"`
		Server struct {
			Version struct{ Name string } `json:"version"`
			Players struct {
				Online int                     `json:"online"`
				Max    int                     `json:"max"`
				Sample []struct{ Name string } `json:"sample"`
			} `json:"players"`
			MOTD        string          `json:"motd"`
			Description json.RawMessage `json:"description"`
		} `json:"server"`
		Bedrock struct {
			Name       string `json:"name"`
			Version    string `json:"version"`
			Players    int    `json:"players"`
			MaxPlayers int    `json:"maxPlayers"`
		} `json:"bedrock"`
	}
	start := strings.Index(out, "{")
	if start < 0 || json.Unmarshal([]byte(out[start:]), &doc) != nil {
		return Status{}, false
	}
	st := Status{Online: doc.Online, Names: []string{}}
	if doc.Bedrock.MaxPlayers > 0 || doc.Bedrock.Version != "" {
		st.Players, st.MaxPlayers, st.Version, st.MOTD = doc.Bedrock.Players, doc.Bedrock.MaxPlayers, doc.Bedrock.Version, doc.Bedrock.Name
		return st, true
	}
	st.Players, st.MaxPlayers, st.Version = doc.Server.Players.Online, doc.Server.Players.Max, doc.Server.Version.Name
	st.MOTD = colorCodes.ReplaceAllString(doc.Server.MOTD, "")
	for _, p := range doc.Server.Players.Sample {
		st.Names = append(st.Names, p.Name)
	}
	st.NamesKnown = len(st.Names) == st.Players
	return st, true
}

func (minecraft) Players(s Spec) PlayerConfig {
	if s.Edition == "bedrock" {
		return PlayerConfig{
			NameRule: `^[A-Za-z0-9 _-]{1,32}$`,
			Lists: []PlayerList{{ID: "allowlist", Label: "Allow list", Description: "Only these players can join while the allow list is on (Properties › allow-list).",
				File: "allowlist.json", Key: "name", Add: `allowlist add "{player}"`, Remove: `allowlist remove "{player}"`}},
			Actions: []PlayerAction{
				{ID: "kick", Label: "Kick", Template: `kick "{player}" {reason}`, Input: "Reason (optional)"},
				{ID: "op", Label: "Make operator", Template: `op "{player}"`},
				{ID: "deop", Label: "Remove operator", Template: `deop "{player}"`},
				{ID: "gamemode", Label: "Set game mode", Template: `gamemode {value} "{player}"`, Options: gamemodeOptions},
			},
		}
	}
	return PlayerConfig{
		OnlineCommand: "list",
		ParseOnline: func(out string) []string {
			_, after, ok := strings.Cut(colorCodes.ReplaceAllString(out, ""), ":")
			names := []string{}
			if !ok {
				return names
			}
			for _, n := range strings.Split(after, ",") {
				if n = strings.TrimSpace(n); n != "" {
					names = append(names, n)
				}
			}
			return names
		},
		NameRule: `^[A-Za-z0-9_]{2,16}$`,
		Lists: []PlayerList{
			{ID: "whitelist", Label: "Whitelist", Description: "Only these players can join while the whitelist is on.", File: "whitelist.json", Key: "name",
				Add: "whitelist add {player}", Remove: "whitelist remove {player}", Toggle: &Toggle{Property: "white-list", On: "whitelist on", Off: "whitelist off"}},
			{ID: "ops", Label: "Operators", Description: "Operators can run every command in game.", File: "ops.json", Key: "name", Add: "op {player}", Remove: "deop {player}"},
			{ID: "bans", Label: "Banned players", Description: "These players can't join.", File: "banned-players.json", Key: "name", Add: "ban {player} {reason}", Remove: "pardon {player}"},
			{ID: "ipbans", Label: "Banned IPs", Description: "Connections from these addresses are refused. Ban an IP from the console with ban-ip.", File: "banned-ips.json", Key: "ip", Remove: "pardon-ip {player}"},
		},
		Actions: []PlayerAction{
			{ID: "tell", Label: "Message", Template: "tell {player} {message}", Input: "Message"},
			{ID: "kick", Label: "Kick", Template: "kick {player} {reason}", Input: "Reason (optional)"},
			{ID: "gamemode", Label: "Set game mode", Template: "gamemode {value} {player}", Options: gamemodeOptions},
			{ID: "op", Label: "Make operator", Template: "op {player}"},
			{ID: "deop", Label: "Remove operator", Template: "deop {player}"},
			{ID: "ban", Label: "Ban", Template: "ban {player} {reason}", Input: "Reason (optional)", Danger: true},
		},
	}
}

var gamemodeOptions = []Option{{Value: "survival", Label: "Survival"}, {Value: "creative", Label: "Creative"}, {Value: "adventure", Label: "Adventure"}, {Value: "spectator", Label: "Spectator"}}

func ip(n int) *int { return &n }

func (minecraft) Properties(s Spec) PropertyConfig {
	sel := func(key, label string, opts []Option, hint string) Property {
		return Property{Key: key, Label: label, Kind: "select", Options: opts, Hint: hint}
	}
	b := func(key, label, hint string) Property {
		return Property{Key: key, Label: label, Kind: "bool", Hint: hint}
	}
	n := func(key, label string, min, max int, hint string) Property {
		return Property{Key: key, Label: label, Kind: "int", Min: ip(min), Max: ip(max), Hint: hint}
	}
	t := func(key, label, hint string) Property {
		return Property{Key: key, Label: label, Kind: "text", Hint: hint}
	}
	diff := []Option{{Value: "peaceful", Label: "Peaceful"}, {Value: "easy", Label: "Easy"}, {Value: "normal", Label: "Normal"}, {Value: "hard", Label: "Hard"}}
	if s.Edition == "bedrock" {
		return PropertyConfig{File: "server.properties", Locked: map[string]string{
			"server-port": "Players connect on the published port. Change it under Networking.", "server-portv6": "Managed by Ignition.",
		}, Groups: []PropertyGroup{
			{Label: "Server", Props: []Property{t("server-name", "Server name", "Shown in the server list."), n("max-players", "Player slots", 1, 500, ""),
				b("online-mode", "Require Xbox sign-in", "Keep on for public servers."), b("allow-list", "Allow list", "Only players on the allow list can join.")}},
			{Label: "Gameplay", Props: []Property{sel("gamemode", "Game mode", gamemodeOptions[:3], ""), sel("difficulty", "Difficulty", diff, ""),
				b("allow-cheats", "Allow cheats", "Lets operators use cheat commands."), n("player-idle-timeout", "Kick idle players after", 0, 1440, "Minutes; 0 never kicks."),
				sel("default-player-permission-level", "New players are", []Option{{Value: "visitor", Label: "Visitors"}, {Value: "member", Label: "Members"}, {Value: "operator", Label: "Operators"}}, "")}},
			{Label: "World", Props: []Property{t("level-name", "World", "The folder under worlds/ that's loaded."), t("level-seed", "Seed", "Used when a new world is created."),
				n("view-distance", "View distance", 5, 96, "Chunks"), n("tick-distance", "Simulation distance", 4, 12, "Chunks"),
				b("texturepack-required", "Require the world's resource packs", "")}},
		}}
	}
	return PropertyConfig{File: "server.properties", Locked: map[string]string{
		"server-port": "Players connect on the published port. Change it under Networking.", "query.port": "Managed by Ignition.",
		"enable-rcon": "The console uses RCON.", "rcon.port": "The console uses RCON.", "rcon.password": "Set by the server image at every start.",
		"server-ip": "Leave it empty inside a container.",
	}, Groups: []PropertyGroup{
		{Label: "Server", Props: []Property{t("motd", "Message of the day", "Shown under the name in the server list."), n("max-players", "Player slots", 1, 1000, ""),
			b("online-mode", "Require Minecraft accounts", "Keep on for public servers: it stops anyone joining as anyone."), b("white-list", "Whitelist", "Only whitelisted players can join."),
			b("enforce-whitelist", "Kick players removed from the whitelist", ""), b("enable-status", "Show in server lists", ""), b("hide-online-players", "Hide who's online", "")}},
		{Label: "Gameplay", Props: []Property{sel("gamemode", "Game mode", gamemodeOptions, ""), b("force-gamemode", "Force the game mode on join", ""),
			sel("difficulty", "Difficulty", diff, ""), b("hardcore", "Hardcore", "One life: players become spectators when they die."), b("pvp", "Player versus player", ""),
			b("allow-flight", "Allow flight", "Turn on for mods or plugins that let players fly, or they get kicked."), b("allow-nether", "Allow the Nether", ""),
			n("spawn-protection", "Spawn protection", 0, 1000, "Blocks around spawn only operators can change."), n("player-idle-timeout", "Kick idle players after", 0, 1440, "Minutes; 0 never kicks."),
			b("enable-command-block", "Command blocks", ""), sel("op-permission-level", "Operator level", []Option{{Value: "1", Label: "1: bypass spawn protection"}, {Value: "2", Label: "2: cheat commands"}, {Value: "3", Label: "3: moderate players"}, {Value: "4", Label: "4: everything"}}, "")}},
		{Label: "World", Props: []Property{t("level-name", "World", "The folder that's loaded."), t("level-seed", "Seed", "Used when a new world is created."),
			b("generate-structures", "Generate structures", "Villages, temples and the like, in new chunks."),
			n("view-distance", "View distance", 3, 32, "Chunks sent to players. Lower it first when the server lags."), n("simulation-distance", "Simulation distance", 3, 32, "Chunks where things move and grow."),
			n("max-world-size", "World border radius", 1, 29999984, "Blocks")}},
		{Label: "Network", Props: []Property{n("network-compression-threshold", "Compression threshold", -1, 65535, "Bytes; -1 turns compression off."),
			n("rate-limit", "Packet rate limit", 0, 100000, "Packets a second before a player is kicked; 0 is off."), b("prevent-proxy-connections", "Block VPN and proxy connections", ""),
			b("enable-query", "GameSpy query", "Some server lists use it.")}},
	}}
}

func (minecraft) Worlds(s Spec) WorldConfig {
	if s.Edition == "bedrock" {
		return WorldConfig{Root: "worlds", Marker: "level.dat", LevelProperty: "level-name", DefaultLevel: "Bedrock level", SeedProperty: "level-seed"}
	}
	return WorldConfig{Root: "", Marker: "level.dat", LevelProperty: "level-name", DefaultLevel: "world", SeedProperty: "level-seed", Companions: []string{"_nether", "_the_end"}}
}

func (minecraft) SaveHooks(s Spec) (before, after []string) {
	if s.Edition == "bedrock" {
		return []string{"save hold"}, []string{"save resume"}
	}
	return []string{"save-off", "save-all flush"}, []string{"save-on"}
}

/* ---------------------------------- Helpers --------------------------------- */

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func contains(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
