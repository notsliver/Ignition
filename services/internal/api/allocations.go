package api

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Server allocations: the IP:port pairs a server hands out, like a hosting provider's pool. Game
 * servers take a free one when they're created; a server without allocations uses the next free
 * port instead. Platform administrators only (the routes live under /api/servers).
 */

const maxAllocationsPerRequest = 1000

// parsePorts reads "25565, 25570-25580 27015" into sorted, unique ports.
func parsePorts(text string) ([]int, error) {
	seen := map[int]bool{}
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == ';' }) {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			return nil, fmt.Errorf("%q isn't a port or a range like 25565-25570", part)
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				return nil, fmt.Errorf("%q isn't a range like 25565-25570", part)
			}
		}
		if a < 1 || b > 65535 || b < a {
			return nil, fmt.Errorf("%q: ports go from 1 to 65535, low to high", part)
		}
		if b-a >= maxAllocationsPerRequest {
			return nil, fmt.Errorf("%q is more than %d ports at once", part, maxAllocationsPerRequest)
		}
		for p := a; p <= b; p++ {
			seen[p] = true
		}
		if len(seen) > maxAllocationsPerRequest {
			return nil, fmt.Errorf("up to %d ports at once", maxAllocationsPerRequest)
		}
	}
	if len(seen) == 0 {
		return nil, errors.New("enter at least one port")
	}
	out := make([]int, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Ints(out)
	return out, nil
}

func (s *Server) allocationServer(r *http.Request) (string, error) {
	id, err := pathID(r, "Server")
	if err != nil {
		return "", err
	}
	if _, err := s.Store.GetServer(r.Context(), id); err != nil {
		return "", notFound(err, "Server")
	}
	return id, nil
}

func (s *Server) listAllocations(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := s.allocationServer(r)
	if err != nil {
		return err
	}
	list, err := s.Store.Allocations(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"allocations": list})
	return nil
}

func (s *Server) addAllocations(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := s.allocationServer(r)
	if err != nil {
		return err
	}
	var in struct {
		IP    string `json:"ip"`
		Alias string `json:"alias"`
		Ports string `json:"ports"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	in.IP, in.Alias = strings.TrimSpace(in.IP), strings.TrimSpace(in.Alias)
	if net.ParseIP(in.IP) == nil {
		f["ip"] = "Enter an IP address, like 203.0.113.10 (or 0.0.0.0 for every address)."
	}
	if len(in.Alias) > 120 || strings.ContainsAny(in.Alias, " \t\r\n") {
		f["alias"] = "A hostname players can use, like play.example.com (up to 120 characters, no spaces)."
	}
	ports, perr := parsePorts(in.Ports)
	if perr != nil {
		f["ports"] = capitalize(perr.Error()) + "."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	added, err := s.Store.AddAllocations(r.Context(), id, in.IP, in.Alias, ports)
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "server.allocations_add", "server", id, map[string]any{"ip": in.IP, "ports": in.Ports, "added": added}, clientIP(r))
	httpx.JSON(w, 201, map[string]any{"added": added, "skipped": len(ports) - added})
	return nil
}

func (s *Server) deleteAllocations(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := s.allocationServer(r)
	if err != nil {
		return err
	}
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if len(in.IDs) == 0 || len(in.IDs) > 5000 {
		return httpx.BadRequest("Choose allocations to delete.")
	}
	for _, x := range in.IDs {
		if !uuidRE.MatchString(x) {
			return httpx.BadRequest("Unknown allocation.")
		}
	}
	deleted, inUse, err := s.Store.DeleteAllocations(r.Context(), id, in.IDs)
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "server.allocations_delete", "server", id, map[string]any{"deleted": deleted, "inUse": inUse}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"deleted": deleted, "inUse": inUse})
	return nil
}

func (s *Server) updateAllocation(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := s.allocationServer(r)
	if err != nil {
		return err
	}
	alloc := r.PathValue("allocation")
	if !uuidRE.MatchString(alloc) {
		return httpx.NotFound("Allocation")
	}
	var in struct {
		Alias string `json:"alias"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Alias = strings.TrimSpace(in.Alias)
	if len(in.Alias) > 120 || strings.ContainsAny(in.Alias, " \t\r\n") {
		return httpx.Invalid(map[string]string{"alias": "A hostname players can use, like play.example.com."})
	}
	if err := s.Store.SetAllocationAlias(r.Context(), id, alloc, in.Alias); err != nil {
		return notFound(err, "Allocation")
	}
	w.WriteHeader(204)
	return nil
}

// setServerVisibility decides whether teams can see (and deploy to) a server.
func (s *Server) setServerVisibility(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := s.allocationServer(r)
	if err != nil {
		return err
	}
	var in struct {
		Visible bool `json:"visible"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if err := s.Store.SetServerVisible(r.Context(), id, in.Visible); err != nil {
		return notFound(err, "Server")
	}
	s.Store.Audit(r.Context(), u, "server.visibility", "server", id, map[string]any{"visible": in.Visible}, clientIP(r))
	srv, err := s.Store.GetServer(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"server": srv})
	return nil
}
