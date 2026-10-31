package api

import "testing"

func TestValidatePreferences(t *testing.T) {
	out, f := validatePreferences(map[string]any{"theme": "dark", "preset": "ocean", "accent": "#12AB9f", "timezone": "Europe/Brussels", "size": "110"})
	if len(f) > 0 || out["preset"] != "ocean" || out["timezone"] != "Europe/Brussels" {
		t.Fatal(out, f)
	}
	_, f = validatePreferences(map[string]any{"theme": "neon", "accent": "blue", "timezone": "Mars/Base", "nope": "x", "size": 110})
	for _, k := range []string{"theme", "accent", "timezone", "nope", "size"} {
		if f[k] == "" {
			t.Errorf("%s should be refused", k)
		}
	}
	if out, f := validatePreferences(map[string]any{"accent": "", "timezone": "browser"}); len(f) > 0 || len(out) != 2 {
		t.Error("empty accent and browser time zone are allowed", f)
	}
}
