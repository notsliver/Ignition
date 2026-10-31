package api

import (
	"strings"
	"testing"

	"github.com/notsliver/Ignition/services/internal/runtime"
)

func TestParseIPAddr(t *testing.T) {
	out := `1: lo    inet 127.0.0.1/8 scope host lo\       valid_lft forever preferred_lft forever
2: eth0    inet 203.0.113.7/24 brd 203.0.113.255 scope global eth0\       valid_lft forever
2: eth0    inet6 2001:db8::7/64 scope global \       valid_lft forever
2: eth0    inet6 fe80::1/64 scope link \       valid_lft forever
7: veth12ab    inet6 fe80::9/64 scope link
3: docker0    inet 172.17.0.1/16 brd 172.17.255.255 scope global docker0
4: eth1@if5    inet 192.168.65.3/24 scope global eth1`
	got := parseIPAddr(out)
	want := map[string]string{"127.0.0.1": "loopback", "203.0.113.7": "public", "2001:db8::7": "public", "fe80::1": "link-local", "fe80::9": "docker", "172.17.0.1": "docker", "192.168.65.3": "private"}
	if len(got) != len(want) {
		t.Fatalf("%d addresses: %+v", len(got), got)
	}
	for _, a := range got {
		if want[a.Address] != a.Scope {
			t.Errorf("%s: scope %s, want %s", a.Address, a.Scope, want[a.Address])
		}
		if a.Address == "192.168.65.3" && a.Interface != "eth1" {
			t.Errorf("interface name keeps no @peer suffix: %q", a.Interface)
		}
		if a.Address == "2001:db8::7" && (a.Family != "inet6" || a.Prefix != 64) {
			t.Errorf("v6: %+v", a)
		}
	}
}

func TestCheckSubnet(t *testing.T) {
	existing := []runtime.NetworkDetail{{Name: "bridge", Subnets: []string{"172.17.0.0/16"}}, {Name: "v6net", Subnets: []string{"fd00:1::/64"}}}
	cases := map[string]struct {
		v6   bool
		want string
	}{
		"10.20.0.0/24":   {false, ""},
		"172.17.5.0/24":  {false, "Overlaps bridge"},
		"172.0.0.0/8":    {false, "Overlaps bridge"},
		"10.0.0.0/30":    {false, "/29"},
		"fd00:2::/64":    {true, ""},
		"fd00:1::/80":    {true, "Overlaps v6net"},
		"10.20.0.0/24 ":  {false, "IPv4 range"},
		"fd00:3::/64x":   {true, "IPv6 range"},
		"192.168.1.0/24": {true, "IPv6 range"},
	}
	for cidr, c := range cases {
		got := checkSubnet(cidr, c.v6, existing)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", cidr, got, c.want)
		}
	}
}

func TestRoutingInputs(t *testing.T) {
	for in, want := range map[string]string{"": "", "/": "", "api": "/api", "/api/": "/api", "/docs/v2": "/docs/v2"} {
		if got, msg := normalizePath(in); got != want || msg != "" {
			t.Errorf("%q: %q %q", in, got, msg)
		}
	}
	for _, bad := range []string{"/api/*", "/a?b", "/a b"} {
		if _, msg := normalizePath(bad); msg == "" {
			t.Errorf("%q should be refused", bad)
		}
	}
	if v, msg := validateRedirect("https://example.com{path}"); msg != "" || v == "" {
		t.Error("redirect with {path}", msg)
	}
	if _, msg := validateRedirect("example.com"); msg == "" {
		t.Error("redirects need a scheme")
	}
	if v, msg := validateAllowIPs([]string{" 10.0.0.1 ", "10.1.2.3/8", "2001:db8::/32"}); msg != "" || strings.Join(v, ",") != "10.0.0.1,10.0.0.0/8,2001:db8::/32" {
		t.Error("allow list normalizes", v, msg)
	}
	if _, msg := validateAllowIPs([]string{"everyone"}); msg == "" {
		t.Error("junk refused")
	}
	if shellJoin([]string{"nslookup", "a'b"}) != "'nslookup' 'ab' 2>&1" {
		t.Error(shellJoin([]string{"nslookup", "a'b"}))
	}
}
