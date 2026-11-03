package api

import (
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
	"net/http"
)

func (s *Server) publicInfrastructure(w http.ResponseWriter, r *http.Request, u *store.User, t *adminToken, a string) error {
	ctx := r.Context()
	id := r.PathValue("id")
	if a == "servers" {
		list := []map[string]any{}
		for offset := 0; ; offset += 200 {
			rows, total, e := s.Store.ListServers(ctx, store.ServerQuery{Q: r.URL.Query().Get("q"), Limit: 200, Offset: offset, Sort: "name"})
			if e != nil {
				return e
			}
			for i := range rows {
				list = append(list, publicMap(viewServer(&rows[i])))
			}
			if offset+len(rows) >= total || len(rows) == 0 {
				break
			}
		}
		return publicList(w, r, list)
	}
	srv, err := s.Store.GetServer(ctx, id)
	if err != nil {
		return httpx.NotFound("Server")
	}
	switch a {
	case "server":
		publicOK(w, viewServer(srv))
		return nil
	case "server-capacity":
		caps, e := s.Store.FleetCapacity(ctx, 24*60*60*1000000000, true)
		if e != nil {
			return e
		}
		publicOK(w, caps[id])
		return nil
	case "allocations":
		v, e := s.Store.Allocations(ctx, id)
		if e != nil {
			return e
		}
		return publicList(w, r, v)
	case "allocation-create":
		out, e := s.publicExisting(r, u, s.addAllocations, nil)
		if e != nil {
			return e
		}
		publicCreated(w, out)
		return nil
	case "allocation-update":
		r = r.Clone(ctx)
		r.SetPathValue("allocation", r.PathValue("allocationId"))
		_, e := s.publicExisting(r, u, s.updateAllocation, nil)
		if e == nil {
			w.WriteHeader(204)
		}
		return e
	case "allocation-delete":
		if err = publicEmpty(r); err != nil {
			return err
		}
		allocation := r.PathValue("allocationId")
		var busy bool
		if err = s.Store.SQL(ctx).QueryRow(ctx, `SELECT resource_id IS NOT NULL FROM server_allocations WHERE server_id=$1 AND id=$2 FOR UPDATE`, id, allocation).Scan(&busy); err != nil {
			return httpx.NotFound("Allocation")
		}
		if busy {
			return httpx.Conflict("This allocation is in use.")
		}
		_, err = s.Store.SQL(ctx).Exec(ctx, `DELETE FROM server_allocations WHERE server_id=$1 AND id=$2`, id, allocation)
		if err == nil {
			w.WriteHeader(204)
		}
		return err
	case "maintenance":
		var in struct {
			Enabled *bool  `json:"enabled"`
			Note    string `json:"note"`
		}
		if err = httpx.Decode(r, &in); err != nil {
			return err
		}
		if in.Enabled == nil {
			return httpx.Invalid(map[string]string{"enabled": "Specify true or false."})
		}
		if len(in.Note) > 2000 {
			return httpx.Invalid(map[string]string{"note": "Up to 2000 characters."})
		}
		if srv.Removing {
			return httpx.Conflict("This server is being removed.")
		}
		_, err = s.Store.SetServerMaintenance(ctx, id, *in.Enabled, in.Note, u.ID)
		if err != nil {
			return err
		}
		v, e := s.Store.GetServer(ctx, id)
		if e != nil {
			return e
		}
		publicOK(w, viewServer(v))
		return nil
	}
	return httpx.NotFound("Infrastructure operation")
}
