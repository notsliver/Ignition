package api

import (
	"reflect"
	"testing"
)

func TestParsePorts(t *testing.T) {
	got, err := parsePorts("25565, 25570-25572 27015;25565")
	if err != nil || !reflect.DeepEqual(got, []int{25565, 25570, 25571, 25572, 27015}) {
		t.Fatalf("parsePorts = %v, %v", got, err)
	}
	for _, bad := range []string{"", "abc", "0", "65536", "30-20", "1-2000", "25565-"} {
		if _, err := parsePorts(bad); err == nil {
			t.Errorf("parsePorts(%q) accepted", bad)
		}
	}
}
