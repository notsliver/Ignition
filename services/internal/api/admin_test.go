package api

import (
	"testing"

	"github.com/notsliver/Ignition/services/internal/store"
)

func TestCanManage(t *testing.T) {
	owner, admin, dev := &store.User{Role: "owner"}, &store.User{Role: "admin"}, &store.User{Role: "developer"}
	if !canManage(owner, "owner") || !canManage(admin, "admin") || !canManage(admin, "viewer") {
		t.Error("owners manage everyone; admins manage non-owners")
	}
	if canManage(admin, "owner") || canManage(dev, "viewer") {
		t.Error("admins can't manage owners; developers can't manage anyone")
	}
}

func TestCSVSafe(t *testing.T) {
	for in, want := range map[string]string{"=HYPERLINK(1)": "'=HYPERLINK(1)", "+1": "'+1", "@x": "'@x", "-cmd": "'-cmd", "plain": "plain", "": ""} {
		if got := csvSafe(in); got != want {
			t.Errorf("%q: got %q", in, got)
		}
	}
}
