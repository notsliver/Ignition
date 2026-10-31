package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	_ "time/tzdata" // time zones are validated even where the image has no zoneinfo

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Interface preferences belong to the person, not the browser: they're stored on the account and
 * applied by the dashboard before its first paint. Every key is checked against a fixed list of
 * allowed values, so stored preferences are always something the dashboard understands.
 */

var prefEnums = map[string][]string{
	"theme":        {"system", "light", "dark"},
	"preset":       {"ignition", "graphite", "midnight", "ocean", "forest", "sand", "sunset", "rose", "violet"},
	"contrast":     {"normal", "high"},
	"size":         {"90", "100", "110", "125"},
	"motion":       {"system", "reduce", "full"},
	"timeStyle":    {"relative", "absolute"},
	"clock":        {"auto", "12h", "24h"},
	"dateFormat":   {"auto", "iso", "dmy", "mdy"},
	"startPage":    {"dashboard", "projects", "servers", "monitor", "domains", "last"},
	"sidebar":      {"expanded", "collapsed"},
	"refresh":      {"live", "normal", "relaxed"},
	"shortcuts":    {"on", "off"},
	"tableDensity": {"compact", "comfortable"},
}

var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// validatePreferences returns the cleaned map or per-key errors. Unknown keys are refused so a
// typo doesn't silently do nothing.
func validatePreferences(in map[string]any) (map[string]string, map[string]string) {
	out, f := map[string]string{}, map[string]string{}
	for k, raw := range in {
		v, ok := raw.(string)
		if !ok {
			f[k] = "Must be text."
			continue
		}
		v = strings.TrimSpace(v)
		switch {
		case k == "accent":
			if v != "" && !hexColorRE.MatchString(v) {
				f[k] = "A colour like #3c55cf."
				continue
			}
		case k == "timezone":
			if v != "" && v != "browser" {
				if _, err := time.LoadLocation(v); err != nil || len(v) > 64 {
					f[k] = "Unknown time zone."
					continue
				}
			}
		default:
			allowed, known := prefEnums[k]
			if !known {
				f[k] = "Unknown preference."
				continue
			}
			if !slices.Contains(allowed, v) {
				f[k] = fmt.Sprintf("One of %s.", strings.Join(allowed, ", "))
				continue
			}
		}
		out[k] = v
	}
	return out, f
}

func (s *Server) getPreferences(w http.ResponseWriter, r *http.Request, u *store.User) error {
	p, err := s.Store.UserPreferences(r.Context(), u.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"preferences": p})
	return nil
}

// putPreferences replaces the stored preferences (the dashboard always sends all of them).
func (s *Server) putPreferences(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in map[string]any
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if len(in) > 40 {
		return httpx.Invalid(map[string]string{"preferences": "Too many preferences."})
	}
	clean, f := validatePreferences(in)
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	b, _ := json.Marshal(clean)
	if err := s.Store.SetUserPreferences(r.Context(), u.ID, b); err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"preferences": clean})
	return nil
}
