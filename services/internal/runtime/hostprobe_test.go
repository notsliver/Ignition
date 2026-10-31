package runtime

import (
	"strings"
	"testing"
)

func TestHostProbeSections(t *testing.T) {
	got := SplitHostProbe("@@stat\ncpu 1 2\ncpu0 1 2\n@@net/dev\n  eth0: 1\n@@df\nFilesystem\n/ 1\n")
	if got["stat"] != "cpu 1 2\ncpu0 1 2\n" || got["net/dev"] != "  eth0: 1\n" || !strings.HasPrefix(got["df"], "Filesystem\n") {
		t.Fatalf("%q", got)
	}
	if strings.Contains(HostProbeScript(false), "cpuinfo") || !strings.Contains(HostProbeScript(true), "cpuinfo") {
		t.Fatal("cpuinfo should only be read when asked for")
	}
	if HostProbeName("") != "ignition-hostprobe" || HostProbeName("prod") != "ignition-hostprobe-prod" {
		t.Fatal("probe names")
	}
}
