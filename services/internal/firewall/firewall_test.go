package firewall

import (
	"strings"
	"testing"
)

func TestScript(t *testing.T) {
	rules := []Rule{{Port: 5432, Protocol: "tcp", Sources: []string{"10.0.0.0/8", "2001:db8::/32"}}, {Port: 9000, Protocol: "udp"}}
	s := Script(rules)
	for _, want := range []string{
		"iptables -A IGNITION-FW -p tcp -m conntrack --ctorigdstport 5432 --ctdir ORIGINAL -s 10.0.0.0/8 -j RETURN",
		"ip6tables -A IGNITION-FW -p tcp -m conntrack --ctorigdstport 5432 --ctdir ORIGINAL -s 2001:db8::/32 -j RETURN",
		"iptables -A IGNITION-FW -p tcp -m conntrack --ctorigdstport 5432 --ctdir ORIGINAL -j DROP",
		"iptables -A IGNITION-FW -p udp -m conntrack --ctorigdstport 9000 --ctdir ORIGINAL -j DROP",
		"-I DOCKER-USER 1 -j IGNITION-FW", "ignition:" + Hash(rules),
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(s, "ip6tables -A IGNITION-FW -p tcp -m conntrack --ctorigdstport 5432 --ctdir ORIGINAL -s 10.0.0.0/8") {
		t.Error("IPv4 sources must not go to ip6tables")
	}
	// The allow rule must come before the drop for the same port.
	if strings.Index(s, "-s 10.0.0.0/8 -j RETURN") > strings.Index(s, "--ctorigdstport 5432 --ctdir ORIGINAL -j DROP") {
		t.Error("allow before drop")
	}
	if !strings.Contains(Script(nil), "-X IGNITION-FW") || strings.Contains(Script(nil), "-j DROP") {
		t.Error("no rules removes the chain")
	}
}

func TestHashIsOrderIndependent(t *testing.T) {
	a := []Rule{{80, "tcp", []string{"1.1.1.1/32", "2.2.2.2/32"}}, {53, "udp", nil}}
	b := []Rule{{53, "udp", nil}, {80, "tcp", []string{"2.2.2.2/32", "1.1.1.1/32"}}}
	if Hash(a) != Hash(b) || Hash(a) == Hash(a[:1]) {
		t.Fatal("hash")
	}
	if h, ok := ParseStatus("hash:abc123\n"); h != "abc123" || !ok {
		t.Fatal(h, ok)
	}
	if _, ok := ParseStatus("docker-user:missing"); ok {
		t.Fatal("missing DOCKER-USER")
	}
}
