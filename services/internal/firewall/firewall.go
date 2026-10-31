// Package firewall turns Ignition's port rules into iptables commands for a Docker host.
//
// Rules live in one chain, IGNITION-FW, jumped to from Docker's DOCKER-USER chain, so they only
// see traffic to published container ports and nothing else on the host (SSH, the host's own
// services) is ever touched. After Docker's DNAT a packet carries the container port, so rules
// match the port the client originally connected to (conntrack --ctorigdstport). A comment rule
// carries a hash of what was applied, so drift (a reboot, Docker recreating chains) is detected.
package firewall

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strings"
)

const Chain = "IGNITION-FW"

// Image runs the rules; iptables is installed into it when it runs.
const Image = "alpine:3.20"

type Rule struct {
	Port     int
	Protocol string
	Sources  []string // CIDRs allowed; empty means nobody (the port is closed)
}

// Hash identifies a rule set regardless of order.
func Hash(rules []Rule) string {
	lines := []string{}
	for _, r := range rules {
		src := append([]string{}, r.Sources...)
		sort.Strings(src)
		lines = append(lines, fmt.Sprintf("%d/%s:%s", r.Port, r.Protocol, strings.Join(src, ",")))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:8])
}

func split(sources []string) (v4, v6 []string) {
	for _, s := range sources {
		ip, _, err := net.ParseCIDR(s)
		if err != nil {
			ip = net.ParseIP(s)
		}
		if ip == nil {
			continue
		}
		if ip.To4() != nil {
			v4 = append(v4, s)
		} else {
			v6 = append(v6, s)
		}
	}
	return
}

// Script returns the shell script that makes the host match rules exactly. An empty rule set
// removes the chain and its jump.
func Script(rules []Rule) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	b.WriteString("command -v iptables >/dev/null || apk add --no-cache -q iptables ip6tables >/dev/null\n")
	for _, t := range []string{"iptables", "ip6tables"} {
		fmt.Fprintf(&b, "if %s -S DOCKER-USER >/dev/null 2>&1; then\n", t)
		if len(rules) == 0 {
			fmt.Fprintf(&b, "  while %[1]s -D DOCKER-USER -j %[2]s 2>/dev/null; do :; done\n  %[1]s -F %[2]s 2>/dev/null || true\n  %[1]s -X %[2]s 2>/dev/null || true\n", t, Chain)
			b.WriteString("fi\n")
			continue
		}
		fmt.Fprintf(&b, "  %[1]s -N %[2]s 2>/dev/null || true\n  %[1]s -F %[2]s\n", t, Chain)
		fmt.Fprintf(&b, "  %[1]s -C DOCKER-USER -j %[2]s 2>/dev/null || %[1]s -I DOCKER-USER 1 -j %[2]s\n", t, Chain)
		for _, r := range rules {
			v4, v6 := split(r.Sources)
			src := v4
			if t == "ip6tables" {
				src = v6
			}
			match := fmt.Sprintf("-p %s -m conntrack --ctorigdstport %d --ctdir ORIGINAL", r.Protocol, r.Port)
			for _, s := range src {
				fmt.Fprintf(&b, "  %s -A %s %s -s %s -j RETURN\n", t, Chain, match, s)
			}
			fmt.Fprintf(&b, "  %s -A %s %s -j DROP\n", t, Chain, match)
		}
		fmt.Fprintf(&b, "  %s -A %s -m comment --comment ignition:%s -j RETURN\n", t, Chain, Hash(rules))
		b.WriteString("fi\n")
	}
	b.WriteString("echo applied:" + Hash(rules) + "\n")
	return b.String()
}

// StatusScript prints the applied hash ("none" without the chain) and whether DOCKER-USER exists.
const StatusScript = `command -v iptables >/dev/null || apk add --no-cache -q iptables >/dev/null 2>&1
if ! iptables -S DOCKER-USER >/dev/null 2>&1; then echo docker-user:missing; exit 0; fi
h=$(iptables -S ` + Chain + ` 2>/dev/null | sed -n 's/.*ignition:\([0-9a-f]*\).*/\1/p' | head -1)
echo "hash:${h:-none}"`

// ParseStatus reads StatusScript's output.
func ParseStatus(out string) (hash string, dockerUser bool) {
	dockerUser = !strings.Contains(out, "docker-user:missing")
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "hash:"); ok {
			hash = v
		}
	}
	return
}
