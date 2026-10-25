package api

import "testing"

func TestNormalizeHostname(t *testing.T) {
	ok := map[string]string{
		"App.Example.com":              "app.example.com",
		"https://shop.example.com/x?y": "shop.example.com",
		"web.localhost.":               "web.localhost",
	}
	for in, want := range ok {
		if got, problem := normalizeHostname(in); got != want || problem != "" {
			t.Errorf("%q: got %q (%s)", in, got, problem)
		}
	}
	for _, bad := range []string{"*.example.com", "localhost", "127.0.0.1", "example.com:8080", "-bad.example.com", "nodot", "a..b.com", ""} {
		if got, problem := normalizeHostname(bad); problem == "" {
			t.Errorf("%q should be rejected, got %q", bad, got)
		}
	}
}
