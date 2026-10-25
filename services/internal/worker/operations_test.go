package worker

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSplitArgs(t *testing.T) {
	cases := map[string][]string{
		``:                             nil,
		`node server.js`:               {"node", "server.js"},
		`sh -c "echo hi && sleep 1"`:   {"sh", "-c", "echo hi && sleep 1"},
		`a 'b c' d\ e`:                 {"a", "b c", "d e"},
		`"" x`:                         {"", "x"},
		`["/bin/app", "--port", "80"]`: {"/bin/app", "--port", "80"},
		`  spaced   out  `:             {"spaced", "out"},
		`it's"quoted"`:                 nil, // unclosed
	}
	for in, want := range cases {
		got, err := SplitArgs(in)
		if want == nil && in != "" {
			if err == nil {
				t.Errorf("%q: expected an error, got %q", in, got)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %q (%v), want %q", in, got, err, want)
		}
	}
}

func TestStatusSpecs(t *testing.T) {
	match := []struct {
		spec string
		code int
		want bool
	}{
		{"200-399", 200, true}, {"200-399", 302, true}, {"200-399", 404, false},
		{"200", 200, true}, {"200", 201, false}, {"200,204", 204, true},
		{"2xx", 299, true}, {"2xx", 301, false}, {"2xx,401", 401, true},
	}
	for _, c := range match {
		if got := StatusMatches(c.spec, c.code); got != c.want {
			t.Errorf("%s vs %d: %v", c.spec, c.code, got)
		}
	}
	for spec, valid := range map[string]bool{"200-399": true, "2xx": true, "200,204": true, "": false, "99": false, "300-200": false, "6xx": false, "abc": false} {
		if ValidStatusSpec(spec) != valid {
			t.Errorf("ValidStatusSpec(%q) should be %v", spec, valid)
		}
	}
}

func TestNextRun(t *testing.T) {
	after := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	next, err := NextRun("0 9 * * *", "America/New_York", after) // 06:00 in New York: 09:00 is today
	if err != nil || next == nil || next.Format(time.RFC3339) != "2026-10-25T13:00:00Z" {
		t.Errorf("got %v, %v", next, err)
	}
	if _, err := NextRun("0 9 * * *", "Mars/Olympus", after); err == nil || !strings.Contains(err.Error(), "timezone") {
		t.Errorf("unknown timezone should fail, got %v", err)
	}
}

func TestTaskOutputKeepsTheEnd(t *testing.T) {
	o := &taskOutput{}
	o.add(pidMarker + "42")
	for i := 0; i < 20000; i++ {
		o.add("line of output that keeps going")
	}
	o.add("the last line")
	text, _ := o.text()
	if o.process() != "42" || strings.Contains(text, pidMarker) {
		t.Error("the PID line must be taken out of the output")
	}
	if len(text) > taskOutputMax+100 || !strings.HasSuffix(text, "the last line\n") || !strings.HasPrefix(text, "… earlier output was trimmed") {
		t.Errorf("output should keep the last %d bytes, got %d", taskOutputMax, len(text))
	}
}

func TestBodySnippet(t *testing.T) {
	html := `<html><head><title>404 Not Found</title><style>body{x:y}</style></head><body><center><h1>404 Not Found</h1></center><hr><center>nginx</center></body></html>`
	if got := bodySnippet(html); got != "404 Not Found 404 Not Found nginx" {
		t.Errorf("got %q", got)
	}
	if got := bodySnippet(strings.Repeat("a", 500)); len(got) > 145 || !strings.HasSuffix(got, "…") {
		t.Errorf("long bodies are cut, got %d bytes", len(got))
	}
}
