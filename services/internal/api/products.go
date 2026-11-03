package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/notsliver/Ignition/services/internal/games"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Products (Instance administration › Products): what this instance offers, in plans. An
 * administrator assigns a plan to a team, which provisions a service: one resource created through
 * the same path the dashboard uses, as the team's owner, held to the plan's size. Prices are shown
 * to administrators; Ignition never charges anything.
 */

var productKinds = map[string]string{"application": "An application", "database": "A database", "worker": "A worker", "function": "A function", "game": "A game server", "files": "A runtime for the team's own files"}

// Editor-only keys in a template: what to ask when assigning (_ask), what teams may change later
// (_allow), and the editor's own choices (_ui). None of them reach the resource.
var templateMeta = []string{"_ask", "_allow", "_ui"}
var planPeriods = map[string]bool{"month": true, "quarter": true, "year": true, "once": true}

func requireInstanceAdmin(u *store.User) error {
	if roleRank[u.Role] < roleRank["admin"] {
		return httpx.Forbidden("Products are managed by instance administrators.")
	}
	return nil
}

type productInput struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Category    string          `json:"category"`
	Kind        string          `json:"kind"`
	Template    json.RawMessage `json:"template"`
	Icon        string          `json:"icon"`
	Placement   json.RawMessage `json:"placement"`
	Status      string          `json:"status"`
	Plans       []store.Plan    `json:"plans"`
}

// gameTemplate is what a game product deploys.
type gameTemplate struct {
	Game       string     `json:"game"`
	Spec       games.Spec `json:"spec"`
	AcceptEULA bool       `json:"acceptEula"`
}

func (in *productInput) validate() map[string]string {
	f := map[string]string{}
	in.Name, in.Description, in.Category = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description), strings.TrimSpace(in.Category)
	if in.Name == "" || len(in.Name) > 80 {
		f["name"] = "Name it, up to 80 characters."
	}
	if len(in.Description) > 1000 {
		f["description"] = "Up to 1,000 characters."
	}
	if len(in.Category) > 60 {
		f["category"] = "Up to 60 characters."
	}
	if in.Status == "" {
		in.Status = "draft"
	}
	if in.Status != "draft" && in.Status != "active" && in.Status != "retired" {
		f["status"] = "Draft, active or retired."
	}
	if !validIcon(in.Icon) {
		f["icon"] = "Use one of the icons, or a PNG, JPEG, WebP, GIF or SVG image up to 200 KB."
	}
	pl := parsePlacement(in.Placement)
	pl.validate(f)
	in.Placement, _ = json.Marshal(pl)
	if productKinds[in.Kind] == "" {
		f["kind"] = "Choose what it runs."
		return f
	}
	if len(in.Template) == 0 {
		in.Template = json.RawMessage(`{}`)
	}
	if in.Kind == "game" {
		var t gameTemplate
		if json.Unmarshal(in.Template, &t) != nil {
			f["template"] = "The game settings aren't valid."
		} else if _, ok := games.Get(t.Game); !ok {
			f["template"] = "Choose a game."
		}
	} else {
		var t resourceInput
		if json.Unmarshal(in.Template, &t) != nil {
			f["template"] = "The settings aren't valid."
		} else {
			switch {
			case in.Kind == "function" && t.Engine == "":
				f["template"] = "Choose a runtime."
			case (in.Kind == "application" || in.Kind == "worker") && t.Image == "" && t.RepoURL == "":
				f["template"] = "Give it an image or a Git repository."
			case in.Kind == "database" && t.Image == "":
				f["template"] = "Choose a database engine."
			case in.Kind == "files" && t.Image == "":
				f["template"] = "Choose a runtime."
			}
			// The kind decides the resource type, whatever the template says.
			var m map[string]any
			json.Unmarshal(in.Template, &m)
			m["type"] = in.Kind
			if in.Kind == "files" {
				m["type"], m["sourceType"] = "application", "image"
			}
			if in.Kind == "function" {
				m["sourceType"] = "image"
			}
			in.Template, _ = json.Marshal(m)
		}
	}
	if len(in.Plans) == 0 {
		f["plans"] = "Add at least one plan."
	}
	if len(in.Plans) > 20 {
		f["plans"] = "Up to 20 plans."
	}
	for i := range in.Plans {
		p := &in.Plans[i]
		p.Name, p.Price, p.Currency = strings.TrimSpace(p.Name), strings.TrimSpace(p.Price), strings.ToUpper(strings.TrimSpace(p.Currency))
		if p.Currency == "" {
			p.Currency = "USD"
		}
		if p.Period == "" {
			p.Period = "month"
		}
		key := fmt.Sprintf("plans.%d.", i)
		switch {
		case p.Name == "" || len(p.Name) > 60:
			f[key+"name"] = "Name each plan."
		case p.CPUs < 0.1 || p.CPUs > 64:
			f[key+"cpus"] = "CPU between 0.1 and 64."
		case p.MemoryMB < 64 || p.MemoryMB > 262144:
			f[key+"memoryMb"] = "Memory between 64 MB and 256 GB."
		case p.StorageGB < 0 || p.BackupGB < 0 || p.Ports < 0:
			f[key+"storageGb"] = "Storage, backups and ports can't be negative."
		case len(p.Price) > 20 || len(p.Currency) > 8:
			f[key+"price"] = "Keep the price short, like 9.99."
		case !planPeriods[p.Period]:
			f[key+"period"] = "Monthly, quarterly, yearly or one-time."
		}
	}
	return f
}

/* --------------------------------- Products -------------------------------- */

func (s *Server) listProducts(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := requireInstanceAdmin(u); err != nil {
		return err
	}
	list, err := s.Store.Products(r.Context())
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"products": list})
	return nil
}

func (s *Server) getProduct(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := requireInstanceAdmin(u); err != nil {
		return err
	}
	p, err := s.productFor(r)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"product": p})
	return nil
}

func (s *Server) productFor(r *http.Request) (*store.Product, error) {
	id := r.PathValue("id")
	if !uuidRE.MatchString(id) {
		return nil, httpx.NotFound("Product")
	}
	p, err := s.Store.Product(r.Context(), id)
	return p, notFound(err, "Product")
}

func (s *Server) saveProduct(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := requireInstanceAdmin(u); err != nil {
		return err
	}
	id := ""
	if r.Method == "PUT" {
		p, err := s.productFor(r)
		if err != nil {
			return err
		}
		id = p.ID
	}
	var in productInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if f := in.validate(); len(f) > 0 {
		return httpx.Invalid(f)
	}
	saved, err := s.Store.SaveProduct(r.Context(), id, store.Product{Name: in.Name, Description: in.Description, Category: in.Category, Kind: in.Kind, Template: in.Template, Icon: in.Icon, Placement: in.Placement, Status: in.Status, Plans: in.Plans}, u.ID)
	if store.IsForeignKey(err) {
		return httpx.Conflict("A plan you removed is still used by services. Move them to another plan first, or keep the plan.")
	}
	if err != nil {
		return notFound(err, "Product")
	}
	action := "product.update"
	if id == "" {
		action = "product.create"
	}
	s.Store.Audit(r.Context(), u, action, "product", saved, map[string]any{"name": in.Name, "kind": in.Kind, "status": in.Status, "plans": len(in.Plans)}, clientIP(r))
	p, err := s.Store.Product(r.Context(), saved)
	if err != nil {
		return err
	}
	code := 200
	if id == "" {
		code = 201
	}
	httpx.JSON(w, code, map[string]any{"product": p})
	return nil
}

func (s *Server) duplicateProduct(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := requireInstanceAdmin(u); err != nil {
		return err
	}
	p, err := s.productFor(r)
	if err != nil {
		return err
	}
	plans := make([]store.Plan, len(p.Plans))
	for i, pl := range p.Plans {
		pl.ID = ""
		plans[i] = pl
	}
	name := p.Name + " (copy)"
	if len(name) > 80 {
		name = name[:80]
	}
	id, err := s.Store.SaveProduct(r.Context(), "", store.Product{Name: name, Description: p.Description, Category: p.Category, Kind: p.Kind, Template: p.Template, Icon: p.Icon, Placement: p.Placement, Status: "draft", Plans: plans}, u.ID)
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "product.duplicate", "product", id, map[string]any{"from": p.Name}, clientIP(r))
	out, err := s.Store.Product(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.JSON(w, 201, map[string]any{"product": out})
	return nil
}

func (s *Server) deleteProduct(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := requireInstanceAdmin(u); err != nil {
		return err
	}
	p, err := s.productFor(r)
	if err != nil {
		return err
	}
	if p.Services > 0 {
		return httpx.Conflict(fmt.Sprintf("%d services still use %s. Retire it instead, so nobody gets new ones, or terminate them first.", p.Services, p.Name))
	}
	if err := s.Store.DeleteProduct(r.Context(), p.ID); err != nil {
		return notFound(err, "Product")
	}
	s.Store.Audit(r.Context(), u, "product.delete", "product", p.ID, map[string]any{"name": p.Name}, clientIP(r))
	w.WriteHeader(204)
	return nil
}

/* --------------------------------- Services -------------------------------- */

// asTeam runs a team handler inside a team, as an instance administrator acting as its owner, so
// everything the dashboard checks (quotas, names, capacity) is checked here too. id fills the
// handler's {id} path value.
func (s *Server) asTeam(w http.ResponseWriter, r *http.Request, u *store.User, team, id, perm string, body []byte, fn func(http.ResponseWriter, *http.Request, *store.User) error) error {
	actor := *u
	actor.InstanceRole, actor.TeamID, actor.TeamRole, actor.Role, actor.Permissions = u.Role, team, "owner", "owner", store.PermOverrides{}
	ctx := store.WithScope(r.Context(), store.Scope{TeamID: team, UserID: u.ID, Permission: perm, MinRank: 1, InstanceAdmin: true})
	r2 := r.Clone(ctx)
	r2.SetPathValue("id", id)
	r2.SetPathValue("teamId", team)
	if body != nil {
		r2.Body = io.NopCloser(strings.NewReader(string(body)))
		r2.ContentLength = int64(len(body))
		r2.Header.Set("Content-Type", "application/json")
	}
	return fn(w, r2, &actor)
}

// callAsTeam runs asTeam and returns the handler's JSON, turning an error response into an error.
func (s *Server) callAsTeam(r *http.Request, u *store.User, team, id, perm string, body []byte, fn func(http.ResponseWriter, *http.Request, *store.User) error) (map[string]any, error) {
	rec := httptest.NewRecorder()
	if err := s.asTeam(rec, r, u, team, id, perm, body, fn); err != nil {
		return nil, err
	}
	out := map[string]any{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code >= 400 {
		if e, ok := out["error"].(map[string]any); ok {
			return nil, &httpx.Error{Status: rec.Code, Code: fmt.Sprint(e["code"]), Message: fmt.Sprint(e["message"])}
		}
		return nil, &httpx.Error{Status: rec.Code, Code: "failed", Message: "Provisioning failed."}
	}
	return out, nil
}

const secretChars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomSecret(n int) string {
	b := make([]byte, n)
	for i := range b {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(secretChars))))
		b[i] = secretChars[k.Int64()]
	}
	return string(b)
}

// fillTemplate replaces {{password}}, {{name}} and {{team}} in every string of a template. One
// password per service, so a database's settings agree with each other.
func fillTemplate(raw json.RawMessage, values map[string]string) json.RawMessage {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case string:
			for k, val := range values {
				t = strings.ReplaceAll(t, "{{"+k+"}}", val)
			}
			return t
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
		case map[string]any:
			for k := range t {
				t[k] = walk(t[k])
			}
		}
		return x
	}
	out, _ := json.Marshal(walk(v))
	return out
}

func (s *Server) listServices(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := requireInstanceAdmin(u); err != nil {
		return err
	}
	q := r.URL.Query()
	list, err := s.Store.Services(r.Context(), store.ServiceFilter{Product: q.Get("product"), Team: q.Get("team"), Status: q.Get("status"), Query: strings.TrimSpace(q.Get("q"))})
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"services": list})
	return nil
}

func (s *Server) serviceFor(r *http.Request) (*store.Service, error) {
	id := r.PathValue("id")
	if !uuidRE.MatchString(id) {
		return nil, httpx.NotFound("Service")
	}
	v, err := s.Store.Service(r.Context(), id)
	return v, notFound(err, "Service")
}

func (s *Server) getService(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := requireInstanceAdmin(u); err != nil {
		return err
	}
	v, err := s.serviceFor(r)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"service": v})
	return nil
}

// provisionService assigns a plan to a team: it creates the resource and records the service.
func (s *Server) provisionService(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := requireInstanceAdmin(u); err != nil {
		return err
	}
	var in struct {
		ProductID string         `json:"productId"`
		PlanID    string         `json:"planId"`
		TeamID    string         `json:"teamId"`
		Name      string         `json:"name"`
		Note      string         `json:"note"`
		Answers   map[string]any `json:"answers"`  // values for the product's "ask when assigning" settings
		ServerID  string         `json:"serverId"` // for products placed by hand
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	f := map[string]string{}
	var product *store.Product
	var plan *store.Plan
	if !uuidRE.MatchString(in.ProductID) {
		f["productId"] = "Choose a product."
	} else if p, err := s.Store.Product(ctx, in.ProductID); err != nil {
		f["productId"] = "Choose a product."
	} else {
		product = p
		for i := range p.Plans {
			if p.Plans[i].ID == in.PlanID {
				plan = &p.Plans[i]
			}
		}
		if plan == nil {
			f["planId"] = "Choose a plan."
		}

	}
	var teamName string
	if !uuidRE.MatchString(in.TeamID) || s.Store.DB.QueryRow(ctx, `SELECT name FROM teams WHERE id = $1`, in.TeamID).Scan(&teamName) != nil {
		f["teamId"] = "Choose a team."
	}
	in.Name = strings.TrimSpace(in.Name)
	if !nameRE.MatchString(in.Name) {
		f["name"] = "Use lowercase letters, numbers and dashes (up to 50 characters)."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}

	// Placement: stock, team limits, eligible servers with room, and the strategy's pick.
	avail, err := s.availabilityFor(ctx, product, plan, in.TeamID)
	if err != nil {
		return err
	}
	if !avail.Available {
		return httpx.Conflict(avail.Reason)
	}
	pl := parsePlacement(product.Placement)
	chosen := avail.Chosen
	if pl.Strategy == "manual" {
		for i, c := range avail.Servers {
			if c.ID == in.ServerID && c.OK {
				chosen = &avail.Servers[i]
			}
		}
		if chosen == nil {
			return httpx.Invalid(map[string]string{"serverId": "Choose a server that can take it."})
		}
	}
	done, ok := startProvisioning(product.ID, pl.MaxConcurrent)
	if !ok {
		return httpx.TooMany(fmt.Sprintf("%s is already provisioning %d services at once. Try again in a moment.", product.Name, pl.MaxConcurrent))
	}
	defer done()
	if len(pl.IPs) > 0 {
		r = r.WithContext(context.WithValue(ctx, allocationIPsKey{}, pl.IPs))
	}

	var m map[string]any
	json.Unmarshal(product.Template, &m)
	if f := applyAnswers(m, in.Answers); len(f) > 0 {
		return httpx.Invalid(f)
	}
	for _, k := range templateMeta {
		delete(m, k)
	}
	raw, _ := json.Marshal(m)
	json.Unmarshal(fillTemplate(raw, map[string]string{"password": randomSecret(24), "name": in.Name, "team": teamName}), &m)
	m["name"], m["memoryMb"], m["cpus"] = in.Name, plan.MemoryMB, plan.CPUs
	body, _ := json.Marshal(m)
	handler, perm := s.createResource, "resources.create"
	if product.Kind == "game" {
		handler, perm = s.createGame, "resources.create"
	}
	out, err := s.callAsTeam(r, u, in.TeamID, "", perm, body, handler)
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) && he.Status == 422 {
			return httpx.Invalid(map[string]string{"name": he.Message})
		}
		return err
	}
	res, _ := out["resource"].(map[string]any)
	resourceID, _ := res["id"].(string)
	if resourceID == "" {
		return errors.New("provisioning returned no resource")
	}
	id, err := s.Store.CreateService(ctx, product.ID, plan.ID, in.TeamID, resourceID, in.Name, u.ID, chosen.ID)
	if err != nil {
		return err
	}
	if note := strings.TrimSpace(in.Note); note != "" {
		s.Store.UpdateService(ctx, id, nil, nil, &note, nil, nil)
	}
	s.Store.Audit(ctx, u, "service.provision", "service", id, map[string]any{"product": product.Name, "plan": plan.Name, "team": teamName, "resource": resourceID, "server": chosen.Name, "strategy": pl.Strategy}, clientIP(r))
	v, err := s.Store.Service(ctx, id)
	if err != nil {
		return err
	}
	httpx.JSON(w, 201, map[string]any{"service": v})
	return nil
}

// serviceAction suspends, unsuspends or changes the plan of a service.
func (s *Server) serviceAction(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := requireInstanceAdmin(u); err != nil {
		return err
	}
	v, err := s.serviceFor(r)
	if err != nil {
		return err
	}
	var in struct {
		Action string `json:"action"` // suspend, unsuspend, change-plan
		Reason string `json:"reason"`
		PlanID string `json:"planId"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if v.Status == "terminated" {
		return httpx.Conflict("This service was terminated.")
	}
	ctx := r.Context()
	resource := ""
	if v.ResourceID != nil {
		resource = *v.ResourceID
	}
	act := func(action string) error {
		if resource == "" {
			return nil
		}
		_, err := s.callAsTeam(r, u, v.TeamID, resource, "resources.control", []byte(`{"action":"`+action+`"}`), s.resourceAction)
		return err
	}
	detail := map[string]any{"service": v.Name, "team": v.TeamName}
	switch in.Action {
	case "suspend":
		if v.Status == "suspended" {
			return httpx.Conflict("It's already suspended.")
		}
		reason := strings.TrimSpace(in.Reason)
		if len(reason) > 300 {
			return httpx.Invalid(map[string]string{"reason": "Up to 300 characters."})
		}
		status := "suspended"
		if err := s.Store.UpdateService(ctx, v.ID, &status, &reason, nil, nil, nil); err != nil {
			return err
		}
		if v.ResourceStatus != nil && *v.ResourceStatus == "running" {
			if err := act("stop"); err != nil {
				detail["stopError"] = err.Error()
			}
		}
		detail["reason"] = reason
	case "unsuspend":
		if v.Status != "suspended" {
			return httpx.Conflict("It isn't suspended.")
		}
		status, empty := "active", ""
		if err := s.Store.UpdateService(ctx, v.ID, &status, &empty, nil, nil, nil); err != nil {
			return err
		}
		if err := act("start"); err != nil {
			detail["startError"] = err.Error()
		}
	case "change-plan":
		p, err := s.Store.Product(ctx, v.ProductID)
		if err != nil {
			return err
		}
		var plan *store.Plan
		for i := range p.Plans {
			if p.Plans[i].ID == in.PlanID {
				plan = &p.Plans[i]
			}
		}
		if plan == nil {
			return httpx.Invalid(map[string]string{"planId": "Choose one of " + p.Name + "'s plans."})
		}
		if err := s.Store.UpdateService(ctx, v.ID, nil, nil, nil, nil, &plan.ID); err != nil {
			return err
		}
		// The new size applies on the next deployment, which starts right away.
		if resource != "" {
			if _, err := s.Store.DB.Exec(ctx, `UPDATE resources SET memory_mb = $2, cpus = $3, updated_at = now() WHERE id = $1`, resource, plan.MemoryMB, plan.CPUs); err != nil {
				return err
			}
			if _, err := s.callAsTeam(r, u, v.TeamID, resource, "deployments.create", []byte(`{}`), s.createDeployment); err != nil {
				detail["redeployError"] = err.Error()
			}
		}
		detail["from"], detail["to"] = v.Plan.Name, plan.Name
	default:
		return httpx.Invalid(map[string]string{"action": "suspend, unsuspend or change-plan."})
	}
	s.Store.Audit(ctx, u, "service."+in.Action, "service", v.ID, detail, clientIP(r))
	out, err := s.Store.Service(ctx, v.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"service": out})
	return nil
}

func (s *Server) updateService(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := requireInstanceAdmin(u); err != nil {
		return err
	}
	v, err := s.serviceFor(r)
	if err != nil {
		return err
	}
	var in struct {
		Note *string `json:"note"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Note != nil {
		*in.Note = strings.TrimSpace(*in.Note)
		if len(*in.Note) > 2000 {
			return httpx.Invalid(map[string]string{"note": "Up to 2,000 characters."})
		}
	}
	if err := s.Store.UpdateService(r.Context(), v.ID, nil, nil, in.Note, nil, nil); err != nil {
		return err
	}
	out, err := s.Store.Service(r.Context(), v.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"service": out})
	return nil
}

// terminateService deletes the service's resource and its data; the service stays as history.
func (s *Server) terminateService(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := requireInstanceAdmin(u); err != nil {
		return err
	}
	if r.URL.Query().Get("confirm") != "true" {
		return httpx.BadRequest("Terminating deletes the service's resource and data. Add ?confirm=true to go ahead.")
	}
	v, err := s.serviceFor(r)
	if err != nil {
		return err
	}
	if v.ResourceID != nil {
		if _, err := s.callAsTeam(r, u, v.TeamID, *v.ResourceID, "resources.delete", nil, s.deleteResource); err != nil {
			var he *httpx.Error
			if !errors.As(err, &he) || he.Status != 404 {
				return err
			}
		}
	}
	status := "terminated"
	if err := s.Store.UpdateService(r.Context(), v.ID, &status, nil, nil, nil, nil); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "service.terminate", "service", v.ID, map[string]any{"service": v.Name, "team": v.TeamName, "product": v.ProductName}, clientIP(r))
	w.WriteHeader(204)
	return nil
}

/* ------------------------------ Plan enforcement ----------------------------- */

// planLimit refuses settings beyond a service's plan. Instance administrators may go past it.
func (s *Server) planLimit(ctx context.Context, u *store.User, resourceID string, cpus float64, memoryMB, ports int) error {
	if roleRank[u.InstanceRole] >= roleRank["admin"] {
		return nil
	}
	v, err := s.Store.ServiceForResource(ctx, resourceID)
	if err != nil {
		return nil // not a service
	}
	p := v.Plan
	switch {
	case cpus > p.CPUs+1e-9:
		return &store.QuotaError{Message: fmt.Sprintf("Your %s plan includes %g CPU cores. Ask your provider for a bigger plan.", p.Name, p.CPUs)}
	case memoryMB > p.MemoryMB:
		return &store.QuotaError{Message: fmt.Sprintf("Your %s plan includes %s of memory. Ask your provider for a bigger plan.", p.Name, mb(p.MemoryMB))}
	case p.Ports > 0 && ports > p.Ports:
		return &store.QuotaError{Message: fmt.Sprintf("Your %s plan includes %d published %s.", p.Name, p.Ports, plural(p.Ports, "port", "ports"))}
	}
	return nil
}

// planStorage refuses writes once a service's storage or backups are full.
func (s *Server) planStorage(ctx context.Context, u *store.User, resourceID string, backup bool) error {
	if roleRank[u.InstanceRole] >= roleRank["admin"] {
		return nil
	}
	v, err := s.Store.ServiceForResource(ctx, resourceID)
	if err != nil || (backup && v.Plan.BackupGB == 0) || (!backup && v.Plan.StorageGB == 0) {
		return nil
	}
	use := store.QuotaUsage{ResourceIDs: []string{resourceID}}
	fillStorage(&use, s.volumeSizes(ctx))
	if backup && use.BackupBytes >= int64(v.Plan.BackupGB)<<30 {
		return &store.QuotaError{Message: fmt.Sprintf("Your %s plan includes %d GB of backups, and they're full. Delete old backups first.", v.Plan.Name, v.Plan.BackupGB)}
	}
	if !backup && use.StorageBytes >= int64(v.Plan.StorageGB)<<30 {
		return &store.QuotaError{Message: fmt.Sprintf("Your %s plan includes %d GB of storage, and it's full. Delete files first, or ask your provider for a bigger plan.", v.Plan.Name, v.Plan.StorageGB)}
	}
	return nil
}

// suspended refuses starting or deploying a suspended service, except for instance administrators.
func (s *Server) suspended(ctx context.Context, u *store.User, resourceID string) error {
	if roleRank[u.InstanceRole] >= roleRank["admin"] {
		return nil
	}
	v, err := s.Store.ServiceForResource(ctx, resourceID)
	if err != nil || v.Status != "suspended" {
		return nil
	}
	msg := "This service is suspended by your provider."
	if v.SuspendedReason != "" {
		msg += " Reason: " + v.SuspendedReason
	}
	return &httpx.Error{Status: 403, Code: "service_suspended", Message: msg}
}

// storageAllowed applies every storage limit before a write: quotas, then the service's plan.
func (s *Server) storageAllowed(ctx context.Context, res *store.Resource, u *store.User, backup bool) error {
	if err := s.checkResourceStorage(ctx, res, u.ID, backup); err != nil {
		return err
	}
	return s.planStorage(ctx, u, res.ID, backup)
}

/* --------------------------- Ask when assigning ---------------------------- */

// templateAsk is one setting the product leaves to whoever assigns it.
type templateAsk struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Kind    string `json:"kind"` // text, number, select, source
	Path    string `json:"path"` // where the answer goes: "image", "spec.version", "env.API_KEY", "source"
	Default any    `json:"default"`
	Options []struct {
		Value any    `json:"value"`
		Label string `json:"label"`
	} `json:"options"`
	Required bool     `json:"required"`
	Min      *float64 `json:"min"`
	Max      *float64 `json:"max"`
}

// applyAnswers puts the assigner's answers (or the defaults) into a template. Only the settings the
// product asks for can be set this way.
func applyAnswers(m map[string]any, answers map[string]any) map[string]string {
	f := map[string]string{}
	raw, _ := json.Marshal(m["_ask"])
	var asks []templateAsk
	json.Unmarshal(raw, &asks)
	for _, a := range asks {
		v, given := answers[a.Key]
		if !given || v == nil || v == "" {
			v = a.Default
		}
		field := "answers." + a.Key
		switch a.Kind {
		case "number":
			n, ok := v.(float64)
			if !ok {
				f[field] = a.Label + " needs a number."
				continue
			}
			if (a.Min != nil && n < *a.Min) || (a.Max != nil && n > *a.Max) {
				f[field] = fmt.Sprintf("%s must be between %v and %v.", a.Label, deref(a.Min), deref(a.Max))
				continue
			}
		case "select":
			ok := false
			for _, o := range a.Options {
				if fmt.Sprint(o.Value) == fmt.Sprint(v) {
					ok, v = true, o.Value
				}
			}
			if !ok {
				f[field] = "Choose a " + strings.ToLower(a.Label) + "."
				continue
			}
		case "source":
			src, _ := v.(map[string]any)
			typ, _ := src["sourceType"].(string)
			image, _ := src["image"].(string)
			repo, _ := src["repoUrl"].(string)
			switch {
			case typ == "image" && strings.TrimSpace(image) != "":
				m["sourceType"], m["image"] = "image", strings.TrimSpace(image)
				delete(m, "repoUrl")
			case typ == "git" && strings.HasPrefix(strings.TrimSpace(repo), "https://"):
				branch, _ := src["branch"].(string)
				if strings.TrimSpace(branch) == "" {
					branch = "main"
				}
				m["sourceType"], m["repoUrl"], m["branch"] = "git", strings.TrimSuffix(strings.TrimSpace(repo), ".git"), strings.TrimSpace(branch)
				delete(m, "image")
			default:
				f[field] = "Give an image, or a Git repository starting with https://."
			}
			continue
		default: // text
			s, _ := v.(string)
			if a.Required && strings.TrimSpace(s) == "" {
				f[field] = a.Label + " is needed."
				continue
			}
			v = s
		}
		setPath(m, a.Path, v)
	}
	return f
}

func deref(p *float64) any {
	if p == nil {
		return "any"
	}
	return *p
}

// setPath sets a value inside a template: "spec.version" nests, "env.NAME" sets that variable.
func setPath(m map[string]any, path string, v any) {
	if name, ok := strings.CutPrefix(path, "env."); ok {
		env, _ := m["env"].([]any)
		for _, e := range env {
			if e, ok := e.(map[string]any); ok && e["key"] == name {
				e["value"] = fmt.Sprint(v)
				return
			}
		}
		m["env"] = append(env, map[string]any{"key": name, "value": fmt.Sprint(v), "secret": false})
		return
	}
	parts := strings.Split(path, ".")
	cur := m
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = v
}

/* ------------------------- What teams may change ---------------------------- */

var allowLabels = map[string]string{"source": "where it runs from (image or repository)", "command": "start command", "env": "environment variables", "version": "game type and version"}

// productAllows refuses a change a service's product keeps for administrators. Products without
// the setting (made before it existed) allow everything.
func (s *Server) productAllows(ctx context.Context, u *store.User, resourceID, what string) error {
	if roleRank[u.InstanceRole] >= roleRank["admin"] {
		return nil
	}
	v, err := s.Store.ServiceForResource(ctx, resourceID)
	if err != nil {
		return nil
	}
	p, err := s.Store.Product(ctx, v.ProductID)
	if err != nil {
		return nil
	}
	var t struct {
		Allow *[]string `json:"_allow"`
	}
	if json.Unmarshal(p.Template, &t) != nil || t.Allow == nil {
		return nil
	}
	for _, a := range *t.Allow {
		if a == what {
			return nil
		}
	}
	return &httpx.Error{Status: 403, Code: "managed_by_provider", Message: "Your provider manages the " + allowLabels[what] + " of " + v.Name + "."}
}
