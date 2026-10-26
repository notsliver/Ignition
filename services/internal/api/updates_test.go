package api

import "testing"

func TestPickRelease(t *testing.T) {
	tags := []hubTag{
		{Name: "edge", Digest: "d-edge"}, {Name: "sha-abc1234", Digest: "d-edge"}, {Name: "sha-old0000", Digest: "d-old"},
		{Name: "latest", Digest: "d-110"}, {Name: "beta", Digest: "d-120b2"},
		{Name: "1.1.0", Digest: "d-110"}, {Name: "1.10.0-beta.1", Digest: "x"}, {Name: "1.2.0-beta.2", Digest: "d-120b2"},
		{Name: "1.2.0-beta.10", Digest: "d-120b10"}, {Name: "0.9.0", Digest: "d-090"}, {Name: "1.1", Digest: "d-110"},
	}
	if r := pickRelease(tags, "notsliver/ignition", "stable"); r == nil || r.Version != "1.1.0" || r.Image != "notsliver/ignition:1.1.0" {
		t.Fatalf("stable = %+v", r)
	}
	if r := pickRelease(tags, "notsliver/ignition", "beta"); r == nil || r.Version != "1.10.0-beta.1" {
		t.Fatalf("beta = %+v", r)
	}
	if r := pickRelease(tags, "notsliver/ignition", "edge"); r == nil || r.Commit != "abc1234" {
		t.Fatalf("edge = %+v", r)
	}
	if pickRelease([]hubTag{{Name: "0.1.0-beta"}}, "x/y", "stable") != nil {
		t.Fatal("a pre-release must not be offered on stable")
	}
}

func TestSemverOrder(t *testing.T) {
	order := []string{"0.1.0-beta", "0.1.0-beta.2", "0.1.0-beta.10", "0.1.0-rc.1", "0.1.0", "0.2.0", "1.0.0"}
	for i := 1; i < len(order); i++ {
		a, _ := parseSemver(order[i-1])
		b, _ := parseSemver(order[i])
		if !a.less(b) || b.less(a) {
			t.Errorf("%s should sort before %s", order[i-1], order[i])
		}
	}
	if _, ok := parseSemver("dev"); ok {
		t.Error("dev isn't a version")
	}
}
