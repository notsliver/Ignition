package games

import (
	"strings"
	"testing"
	"time"
)

func TestNeoForgeGameVersion(t *testing.T) {
	for in, want := range map[string]string{
		"21.1.77": "1.21.1", "20.4.237": "1.20.4", "21.0.167": "1.21", "20.2.3-beta": "1.20.2",
		"26.3.0.100": "26.3", "26.1.2.5": "26.1.2", "0.25w14craftmine.3-beta": "", "47.1": "",
	} {
		if got := NeoForgeGameVersion(in); got != want {
			t.Errorf("NeoForgeGameVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJavaFor(t *testing.T) {
	for in, want := range map[string]int{
		"1.12.2": 8, "1.16.5": 8, "1.17.1": 17, "1.18.2": 17, "1.20.1": 17, "1.20.4": 17, "1.20.5": 21, "1.20.6": 21,
		"1.21.1": 21, "1.21.11": 21, "26.1": 25, "26.3": 25, "26.4-snapshot-6": 25, "24w14a": 21, "": 21,
	} {
		if got := JavaFor(in); got != want {
			t.Errorf("JavaFor(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	v := []Version{{ID: "1.20.9"}, {ID: "1.21"}, {ID: "1.20.10"}, {ID: "26.1"}, {ID: "1.21-rc1"}, {ID: "1.8.9"}}
	sortVersions(v)
	var got []string
	for _, x := range v {
		got = append(got, x.ID)
	}
	if strings.Join(got, " ") != "26.1 1.21 1.21-rc1 1.20.10 1.20.9 1.8.9" {
		t.Fatalf("sorted: %v", got)
	}
}

func TestPropertiesRoundTrip(t *testing.T) {
	in := "#Minecraft server properties\n#Thu Oct 30\nmotd=A \\u00A7aGreen Server\nmax-players=20\nlevel-seed=\nwhite-list=false\n"
	p := ParseProperties(in)
	if v, _ := p.Get("motd"); v != "A §aGreen Server" {
		t.Fatalf("motd = %q", v)
	}
	if v, ok := p.Get("level-seed"); !ok || v != "" {
		t.Fatalf("level-seed = %q %v", v, ok)
	}
	if p.String() != in {
		t.Fatalf("unchanged file changed:\n%s", p.String())
	}
	p.Set("max-players", "50")
	p.Set("motd", "Café ☕ 🎮")
	p.Set("new-key", "x")
	out := p.String()
	if !strings.Contains(out, "max-players=50\n") || !strings.HasPrefix(out, "#Minecraft server properties\n#Thu Oct 30\nmotd=") || !strings.HasSuffix(out, "new-key=x\n") {
		t.Fatalf("set: %s", out)
	}
	again := ParseProperties(out)
	if v, _ := again.Get("motd"); v != "Café ☕ 🎮" {
		t.Fatalf("unicode round trip = %q (%s)", v, out)
	}
}

func TestCleanPath(t *testing.T) {
	for in, want := range map[string]string{"": "", "/": "", "plugins": "plugins", "/plugins/x.jar": "plugins/x.jar", "a//b/./c": "a/b/c", "world\\region": "world/region"} {
		if got, err := CleanPath(in); err != nil || got != want {
			t.Errorf("CleanPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"..", "../etc", "a/../../b", "a/..", "x\x00"} {
		if _, err := CleanPath(bad); err == nil {
			t.Errorf("CleanPath(%q) accepted", bad)
		}
	}
}

func TestFillKeepsOneLine(t *testing.T) {
	got := Fill("kick {player} {reason}", map[string]string{"player": "Steve", "reason": "bye\nop Mallory"})
	if got != "kick Steve bye op Mallory" {
		t.Fatalf("Fill = %q", got)
	}
	if got := Fill("kick {player} {reason}", map[string]string{"player": "Steve", "reason": ""}); got != "kick Steve" {
		t.Fatalf("empty reason = %q", got)
	}
}

func TestParseStatus(t *testing.T) {
	m := minecraft{}
	st, ok := m.ParseStatus(`{"online":true,"server":{"version":{"name":"Paper 1.21.1"},"players":{"online":2,"max":20,"sample":[{"name":"Steve"},{"name":"Alex"}]},"motd":"§aHello"}}`)
	if !ok || !st.Online || st.Players != 2 || st.MaxPlayers != 20 || st.MOTD != "Hello" || !st.NamesKnown || len(st.Names) != 2 {
		t.Fatalf("java status: %+v %v", st, ok)
	}
	names := m.Players(Spec{Edition: "paper"}).ParseOnline("There are 2 of a max of 20 players online: Steve, Alex")
	if strings.Join(names, ",") != "Steve,Alex" {
		t.Fatalf("list = %v", names)
	}
	if n := m.Players(Spec{Edition: "paper"}).ParseOnline("There are 0 of a max of 20 players online: "); len(n) != 0 {
		t.Fatalf("empty list = %v", n)
	}
}

func TestNormalizeAndRuntime(t *testing.T) {
	m := minecraft{}
	s := Spec{Edition: "fabric", Version: "1.21.1", DisplayName: "  Friends   SMP ", Options: map[string]string{"difficulty": "hard"}}
	if f := m.Normalize(&s); len(f) > 0 {
		t.Fatalf("normalize: %v", f)
	}
	if s.DisplayName != "Friends SMP" || s.Slots != 20 || s.Options["gamemode"] != "survival" || s.Options["difficulty"] != "hard" {
		t.Fatalf("defaults: %+v", s)
	}
	rt, _ := m.Runtime(s, 4096, true)
	if rt.Image != "itzg/minecraft-server:java21" || rt.Env["TYPE"] != "FABRIC" || rt.Env["MEMORY"] != "3277M" || rt.Env["MOTD"] != "Friends SMP" || rt.Env["DIFFICULTY"] != "hard" {
		t.Fatalf("runtime: %+v", rt)
	}
	later, _ := m.Runtime(s, 4096, false)
	if _, ok := later.Env["MOTD"]; ok {
		t.Fatal("setup values must only be passed on the first run")
	}
	bad := Spec{Edition: "paper"}
	if f := m.Normalize(&bad); f["version"] == "" || f["displayName"] == "" {
		t.Fatalf("missing fields not reported: %v", f)
	}
	bedrock := Spec{Edition: "bedrock", Version: "latest", DisplayName: "B"}
	m.Normalize(&bedrock)
	rt, _ = m.Runtime(bedrock, 2048, true)
	if rt.Env["VERSION"] != "LATEST" || rt.Ports[0].Protocol != "udp" || rt.Env["SERVER_NAME"] != "B" {
		t.Fatalf("bedrock: %+v", rt)
	}
}

func TestBackupNames(t *testing.T) {
	name := BackupName("Before reset!", true, mustTime("2026-10-31T21:05:09Z"))
	if name != "20261031-210509-auto-before-reset.tar.gz" || !ValidBackup(name) {
		t.Fatalf("name = %q", name)
	}
	for _, bad := range []string{"../x.tar.gz", "20261031-210509.tar.gz/../../x", "x.tar.gz"} {
		if ValidBackup(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
}

func mustTime(s string) (t time.Time) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
