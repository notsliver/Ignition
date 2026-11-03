package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

func publicRequest(r *http.Request, method string, body any) *http.Request {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	r.Method = method
	if body != nil {
		b := encodePublic(body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		r.ContentLength = int64(len(b))
	}
	return r
}
func publicPatch(r *http.Request, allowed ...string) (map[string]json.RawMessage, error) {
	var v map[string]json.RawMessage
	if err := httpx.Decode(r, &v); err != nil {
		return nil, err
	}
	for k := range v {
		ok := false
		for _, a := range allowed {
			ok = ok || k == a
		}
		if !ok {
			return nil, httpx.Invalid(map[string]string{k: "Unknown or read-only field."})
		}
	}
	return v, nil
}
func redactProduct(p *store.Product) map[string]any {
	v := publicMap(p)
	delete(v, "services")
	v["assignedResources"] = p.Services
	v["template"] = redactTemplate(v["template"])
	plans := []map[string]any{}
	for _, pl := range p.Plans {
		plans = append(plans, publicPlan(pl))
	}
	v["plans"] = plans
	return v
}
func redactTemplate(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			key := strings.ToLower(k)
			if key == "env" {
				if list, ok := val.([]any); ok {
					clean := []any{}
					for _, item := range list {
						m, ok := item.(map[string]any)
						if !ok {
							continue
						}
						copy := map[string]any{}
						for name, value := range m {
							if name != "value" {
								copy[name] = value
							}
						}
						copy["value"] = nil
						copy["unchanged"] = true
						clean = append(clean, copy)
					}
					out[k] = clean
					continue
				}
			}
			if strings.Contains(key, "password") || strings.Contains(key, "secret") || key == "token" || key == "apikey" {
				out[k] = nil
			} else {
				out[k] = redactTemplate(val)
			}
		}
		return out
	case []any:
		out := []any{}
		for _, v := range x {
			out = append(out, redactTemplate(v))
		}
		return out
	}
	return v
}
func planCapacityEqual(a, b store.Plan) bool {
	return a.CPUs == b.CPUs && a.MemoryMB == b.MemoryMB && a.StorageGB == b.StorageGB && a.BackupGB == b.BackupGB && a.Ports == b.Ports
}
func (s *Server) publicProducts(w http.ResponseWriter, r *http.Request, u *store.User, t *adminToken, a string) error {
	ctx := r.Context()
	id := r.PathValue("id")
	if a == "products" {
		list, err := s.Store.Products(ctx)
		if err != nil {
			return err
		}
		out := []map[string]any{}
		q := r.URL.Query()
		for i := range list {
			p := &list[i]
			if q.Get("kind") != "" && q.Get("kind") != p.Kind || q.Get("status") != "" && q.Get("status") != p.Status || q.Get("category") != "" && q.Get("category") != p.Category {
				continue
			}
			if v := strings.ToLower(q.Get("q")); v != "" && !strings.Contains(strings.ToLower(p.Name+" "+p.Description), v) {
				continue
			}
			out = append(out, redactProduct(p))
		}
		return publicList(w, r, out)
	}
	if a == "product-create" {
		out, err := s.publicExisting(publicRequest(r, "POST", nil), u, s.saveProduct, nil)
		if err != nil {
			return err
		}
		p := publicMap(out["product"])
		var product store.Product
		json.Unmarshal(encodePublic(p), &product)
		publicCreated(w, redactProduct(&product))
		return nil
	}
	p, err := s.Store.Product(ctx, id)
	if err != nil {
		return httpx.NotFound("Product")
	}
	switch a {
	case "product":
		publicOK(w, redactProduct(p))
		return nil
	case "product-delete":
		_, err = s.publicExisting(r, u, s.deleteProduct, nil)
		if err == nil {
			w.WriteHeader(204)
		}
		return err
	case "product-duplicate":
		if err = publicEmpty(r); err != nil {
			return err
		}
		out, e := s.publicExisting(r, u, s.duplicateProduct, nil)
		if e != nil {
			return e
		}
		var product store.Product
		json.Unmarshal(encodePublic(out["product"]), &product)
		publicCreated(w, redactProduct(&product))
		return nil
	case "availability":
		q := r.URL.Query()
		q.Set("plan", q.Get("planId"))
		q.Set("team", q.Get("teamId"))
		r = r.Clone(ctx)
		r.URL.RawQuery = q.Encode()
		if q.Get("plan") == "" {
			return httpx.Invalid(map[string]string{"planId": "Choose a plan."})
		}
		found := false
		for _, pl := range p.Plans {
			found = found || pl.ID == q.Get("plan")
		}
		if !found {
			return httpx.NotFound("Plan")
		}
		out, e := s.publicExisting(r, u, s.productAvailability, nil)
		if e != nil {
			return e
		}
		publicOK(w, out)
		return nil
	case "plans":
		plans := []map[string]any{}
		for _, pl := range p.Plans {
			plans = append(plans, publicPlan(pl))
		}
		return publicList(w, r, plans)
	case "plan":
		for _, pl := range p.Plans {
			if pl.ID == r.PathValue("planId") {
				publicOK(w, publicPlan(pl))
				return nil
			}
		}
		return httpx.NotFound("Plan")
	}
	in := productInput{Name: p.Name, Description: p.Description, Category: p.Category, Kind: p.Kind, Template: p.Template, Icon: p.Icon, Placement: p.Placement, Status: p.Status, Plans: append([]store.Plan{}, p.Plans...)}
	switch a {
	case "product-update":
		patch, e := publicPatch(r, "name", "description", "category", "kind", "template", "icon", "placement", "status", "plans")
		if e != nil {
			return e
		}
		merged := map[string]json.RawMessage{}
		json.Unmarshal(encodePublic(in), &merged)
		for k, v := range patch {
			merged[k] = v
		}
		if e = json.Unmarshal(encodePublic(merged), &in); e != nil {
			return httpx.BadRequest("Invalid product fields.")
		}
		if p.Services > 0 && in.Kind != p.Kind {
			return httpx.Conflict("An assigned product cannot change kind.")
		}
	case "plan-create":
		var pl store.Plan
		if err = httpx.Decode(r, &pl); err != nil {
			return err
		}
		if pl.ID != "" || pl.Services != 0 {
			return httpx.Invalid(map[string]string{"id": "Plan IDs and counts are read-only."})
		}
		in.Plans = append(in.Plans, pl)
	case "plan-update", "plan-delete":
		index := -1
		for i, v := range in.Plans {
			if v.ID == r.PathValue("planId") {
				index = i
			}
		}
		if index < 0 {
			return httpx.NotFound("Plan")
		}
		if a == "plan-delete" {
			if in.Plans[index].Services > 0 {
				return httpx.Conflict("This plan is in use.")
			}
			in.Plans = append(in.Plans[:index], in.Plans[index+1:]...)
		} else {
			patch, e := publicPatch(r, "name", "cpus", "memoryMb", "storageGb", "backupGb", "ports", "price", "currency", "period")
			if e != nil {
				return e
			}
			merged := map[string]json.RawMessage{}
			json.Unmarshal(encodePublic(in.Plans[index]), &merged)
			for k, v := range patch {
				merged[k] = v
			}
			if json.Unmarshal(encodePublic(merged), &in.Plans[index]) != nil {
				return httpx.BadRequest("Invalid plan fields.")
			}
		}
	default:
		return httpx.NotFound("Product operation")
	}
	for _, next := range in.Plans {
		if next.ID != "" {
			found := false
			for _, old := range p.Plans {
				found = found || old.ID == next.ID
			}
			if !found {
				return httpx.Invalid(map[string]string{"plans": "Plan IDs must belong to this product; omit the ID for new plans."})
			}
		}
	}
	for _, old := range p.Plans {
		found := false
		for _, next := range in.Plans {
			found = found || next.ID == old.ID
		}
		if !found && !publicHas(t, "products.delete") {
			return httpx.Forbidden("Removing plans requires products.delete.")
		}
	}
	if err = preserveTemplateValues(p.Template, &in.Template); err != nil {
		return err
	}
	for _, old := range p.Plans {
		for _, next := range in.Plans {
			if next.ID == old.ID && old.Services > 0 && !planCapacityEqual(old, next) {
				return httpx.Conflict("Create a new plan and migrate assigned resources before changing capacity.")
			}
		}
	}
	if len(in.Plans) < len(p.Plans) && !publicHas(t, "products.delete") {
		return httpx.Forbidden("Removing plans requires products.delete.")
	}
	out, err := s.publicExisting(publicRequest(r, "PUT", in), u, s.saveProduct, nil)
	if err != nil {
		return err
	}
	var saved store.Product
	json.Unmarshal(encodePublic(out["product"]), &saved)
	if a == "plan-delete" {
		w.WriteHeader(204)
		return nil
	}
	if a == "plan-create" {
		for _, pl := range saved.Plans {
			found := false
			for _, old := range p.Plans {
				found = found || old.ID == pl.ID
			}
			if !found {
				publicCreated(w, publicPlan(pl))
				return nil
			}
		}
	}
	if a == "plan-update" {
		for _, pl := range saved.Plans {
			if pl.ID == r.PathValue("planId") {
				publicOK(w, publicPlan(pl))
				return nil
			}
		}
	}
	publicOK(w, redactProduct(&saved))
	return nil
}

// A round trip through a redacted read must not erase write-only template variables.
func preserveTemplateValues(previous json.RawMessage, next *json.RawMessage) error {
	var old, in map[string]any
	if json.Unmarshal(previous, &old) != nil || json.Unmarshal(*next, &in) != nil {
		return nil
	}
	oldVars := map[string]map[string]any{}
	if env, ok := old["env"].([]any); ok {
		for _, v := range env {
			if m, ok := v.(map[string]any); ok {
				key, _ := m["key"].(string)
				oldVars[key] = m
			}
		}
	}
	if env, ok := in["env"].([]any); ok {
		for _, v := range env {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			key, _ := m["key"].(string)
			if unchanged, _ := m["unchanged"].(bool); unchanged {
				prior, found := oldVars[key]
				if !found {
					return httpx.Invalid(map[string]string{"template": "An unchanged variable must already exist."})
				}
				m["value"] = prior["value"]
				delete(m, "unchanged")
			}
		}
	}
	*next = encodePublic(in)
	return nil
}

func publicPlan(p store.Plan) map[string]any {
	v := publicMap(p)
	delete(v, "services")
	v["assignedResources"] = p.Services
	return v
}
