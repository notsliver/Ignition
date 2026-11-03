package api

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/version"
)

type publicAdminRoute struct{ Pattern, Permission, Action string }

//go:embed public_openapi.json
var publicOpenAPI []byte

var publicAdminRoutes = []publicAdminRoute{
	{"GET /openapi.json", "", "openapi"},
	{"GET /instance", "settings.view", "instance"}, {"GET /defaults", "settings.view", "defaults"},
	{"GET /permissions", "", "permissions"}, {"GET /token", "", "token"},
	{"GET /capacity", "servers.view", "capacity"}, {"GET /usage", "usage.view", "usage"},
	{"GET /operations/{id}", "", "operation"},
	{"GET /users", "users.view", "users"}, {"POST /users", "users.create", "user-create"},
	{"GET /users/{id}", "users.view", "user"}, {"PATCH /users/{id}", "users.update", "user-update"},
	{"POST /users/{id}/suspend", "users.update", "user-suspend"}, {"POST /users/{id}/resume", "users.update", "user-resume"},
	{"POST /users/{id}/sessions/revoke", "users.update", "user-sessions"},
	{"POST /users/{id}/setup-link", "users.setup", "user-setup"}, {"POST /users/{id}/reset-link", "users.setup", "user-reset"},
	{"GET /invitations", "users.view", "invitations"}, {"POST /invitations", "users.invite", "invite"}, {"DELETE /invitations/{id}", "users.invite", "invite-delete"},
	{"GET /teams", "teams.view", "teams"}, {"POST /teams", "teams.create", "team-create"},
	{"GET /teams/{id}", "teams.view", "team"}, {"PATCH /teams/{id}", "teams.update", "team-update"},
	{"GET /teams/{id}/members", "teams.view", "members"}, {"POST /teams/{id}/members", "teams.members", "member-create"},
	{"PATCH /teams/{id}/members/{userId}", "teams.members", "member-update"}, {"DELETE /teams/{id}/members/{userId}", "teams.members", "member-delete"},
	{"GET /teams/{id}/invitations", "teams.view", "team-invitations"}, {"POST /teams/{id}/invitations", "teams.members", "team-invite"},
	{"DELETE /teams/{id}/invitations/{invitationId}", "teams.members", "team-invite-delete"}, {"GET /teams/{id}/usage", "usage.view", "team-usage"},
	{"GET /products", "products.view", "products"}, {"POST /products", "products.manage", "product-create"},
	{"GET /products/{id}", "products.view", "product"}, {"PATCH /products/{id}", "products.manage", "product-update"},
	{"DELETE /products/{id}", "products.delete", "product-delete"}, {"POST /products/{id}/duplicate", "products.manage", "product-duplicate"},
	{"GET /products/{id}/availability", "products.view", "availability"},
	{"GET /products/{id}/plans", "products.view", "plans"}, {"POST /products/{id}/plans", "products.manage", "plan-create"},
	{"GET /products/{id}/plans/{planId}", "products.view", "plan"}, {"PATCH /products/{id}/plans/{planId}", "products.manage", "plan-update"},
	{"DELETE /products/{id}/plans/{planId}", "products.delete", "plan-delete"},
	{"GET /resources", "resources.view", "resources"}, {"POST /resources", "resources.provision", "provision"},
	{"GET /resources/{id}", "resources.view", "resource"}, {"PATCH /resources/{id}", "resources.update", "resource-update"},
	{"GET /resources/{id}/usage", "usage.view", "resource-usage"}, {"GET /resources/{id}/limits", "resources.view", "resource-limits"},
	{"GET /resources/{id}/operations", "resources.view", "resource-operations"},
	{"POST /resources/{id}/start", "resources.control", "resource-start"}, {"POST /resources/{id}/stop", "resources.control", "resource-stop"},
	{"POST /resources/{id}/restart", "resources.control", "resource-restart"},
	{"POST /resources/{id}/suspend", "resources.suspend", "resource-suspend"}, {"POST /resources/{id}/resume", "resources.suspend", "resource-resume"},
	{"POST /resources/{id}/change-plan", "resources.change-plan", "resource-change-plan"},
	{"POST /resources/{id}/redeploy", "deployments.create", "resource-redeploy"}, {"DELETE /resources/{id}", "resources.delete", "resource-delete"},
	{"GET /resources/{id}/deployments", "deployments.view", "deployments"}, {"GET /deployments/{id}", "deployments.view", "deployment"},
	{"POST /deployments/{id}/cancel", "deployments.cancel", "deployment-cancel"}, {"GET /deployments/{id}/logs", "logs.view", "deployment-logs"},
	{"GET /servers", "servers.view", "servers"}, {"GET /servers/{id}", "servers.view", "server"},
	{"GET /servers/{id}/capacity", "servers.view", "server-capacity"}, {"GET /servers/{id}/allocations", "servers.view", "allocations"},
	{"POST /servers/{id}/allocations", "servers.update", "allocation-create"},
	{"PATCH /servers/{id}/allocations/{allocationId}", "servers.update", "allocation-update"},
	{"DELETE /servers/{id}/allocations/{allocationId}", "servers.update", "allocation-delete"},
	{"PUT /servers/{id}/maintenance", "servers.update", "maintenance"},
	{"GET /audit-events", "audit.view", "audit"}, {"GET /audit-events/{id}", "audit.view", "audit-event"},
	{"GET /webhooks", "webhooks.view", "webhooks"}, {"POST /webhooks", "webhooks.manage", "webhook-create"},
	{"GET /webhooks/{id}", "webhooks.view", "webhook"}, {"PATCH /webhooks/{id}", "webhooks.manage", "webhook-update"},
	{"DELETE /webhooks/{id}", "webhooks.manage", "webhook-delete"}, {"POST /webhooks/{id}/test", "webhooks.manage", "webhook-test"},
	{"GET /webhooks/{id}/deliveries", "webhooks.view", "deliveries"},
	{"POST /webhooks/{id}/deliveries/{deliveryId}/retry", "webhooks.manage", "delivery-retry"},
}

type publicHandler func(http.ResponseWriter, *http.Request, *store.User, *adminToken, string) error

func (s *Server) registerPublicAdmin(mux *http.ServeMux) {
	mux.Handle("/api/admin/v1/", httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
		if _, _, err := s.authenticateAdminToken(r); err != nil {
			return publicHTTPError(w, err)
		}
		return httpx.NotFound("Admin API endpoint")
	}))

	for _, route := range publicAdminRoutes {
		route := route
		method, path, _ := strings.Cut(route.Pattern, " ")
		pattern := method + " /api/admin/v1" + path
		mux.Handle(pattern, httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
			w.Header().Set("Cache-Control", "no-store")
			u, t, err := s.authenticateAdminToken(r)
			if err != nil {
				return publicHTTPError(w, err)
			}
			if !publicHas(t, route.Permission) {
				return httpx.Forbidden("This token requires " + route.Permission + ".")
			}
			for _, name := range []string{"id", "userId", "planId", "allocationId", "invitationId", "deliveryId"} {
				if v := r.PathValue(name); v != "" && !uuidRE.MatchString(v) && !(route.Action == "audit-event" && name == "id") {
					return httpx.NotFound("Object")
				}
			}
			if r.Method == "GET" || r.Method == "HEAD" {
				s.Store.DB.Exec(r.Context(), `UPDATE admin_tokens SET last_used_at=now(),last_ip=$2 WHERE id=$1`, t.ID, clientIP(r))
				return publicHTTPError(w, domainError(s.publicDispatch(w, r, u, t, route.Action)))
			}
			raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
			if err != nil {
				return httpx.BadRequest("Request body exceeds 1 MB.")
			}
			if len(bytes.TrimSpace(raw)) > 0 && !json.Valid(raw) {
				return httpx.BadRequest("Provide exactly one valid JSON value.")
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			ctx := r.Context()
			tx, err := s.Store.DB.Begin(ctx)
			if err != nil {
				return err
			}
			defer tx.Rollback(ctx)
			// Bound every mutation (including its queue records) to one commit. Also serialize
			// placement checks and onboarding limits across API replicas.
			if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(738219410)`); err != nil {
				return err
			}
			ctx = store.WithTransaction(ctx, tx)
			r = r.WithContext(ctx)
			// Recheck revocation after waiting for the mutation lock.
			var active bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_tokens a JOIN users u ON u.id=a.created_by WHERE a.id=$1 AND a.revoked_at IS NULL AND (a.expires_at IS NULL OR a.expires_at>now()) AND u.status='active' AND u.role IN ('owner','admin'))`, t.ID).Scan(&active); err != nil {
				return err
			}
			if !active {
				return httpx.Unauthorized()
			}
			fresh, e := s.Store.GetUser(ctx, t.CreatedBy)
			if e != nil || fresh.Status != "active" || !platformAccountAdmin(fresh) {
				return httpx.Unauthorized()
			}
			effective := []string{}
			for _, p := range t.Permissions {
				if can(fresh, p) {
					effective = append(effective, p)
				}
			}
			t.Permissions = effective
			if !publicHas(t, route.Permission) {
				return httpx.Forbidden("This token no longer has the required permission.")
			}
			fresh.AdminTokenID = t.ID
			u = fresh
			key := r.Header.Get("Idempotency-Key")
			if len(key) > 200 {
				return httpx.BadRequest("Idempotency-Key must be at most 200 characters.")
			}
			keyHash := hashString(r.Method + " " + r.URL.Path + "\n" + key)
			fingerprint := publicRequestHash(r, raw)
			if key != "" {
				var previous, sealed []byte
				var status int
				e := tx.QueryRow(ctx, `SELECT request_hash,response_enc,status FROM admin_idempotency WHERE token_id=$1 AND key_hash=$2 AND expires_at>now()`, t.ID, keyHash).Scan(&previous, &sealed, &status)
				if e == nil {
					if !bytes.Equal(previous, fingerprint) {
						return httpx.Conflict("Idempotency-Key was already used with another request.")
					}
					body, e := s.Secrets.Open(sealed, publicKeyContext(t.ID, keyHash))
					if e != nil {
						return e
					}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Idempotency-Replayed", "true")
					w.WriteHeader(status)
					_, e = w.Write([]byte(body))
					return e
				}
				if !errors.Is(e, store.ErrNotFound) && !strings.Contains(fmt.Sprint(e), "no rows") {
					return e
				}
			}
			rec := httptest.NewRecorder()
			if err = s.publicDispatch(rec, r, u, t, route.Action); err != nil {
				return publicHTTPError(w, domainError(err))
			}
			if rec.Code >= 400 {
				return httpx.Unavailable("The administrative operation was not accepted.")
			}
			if key != "" {
				sealed, e := s.Secrets.Seal(rec.Body.String(), publicKeyContext(t.ID, keyHash))
				if e != nil {
					return e
				}
				_, err = tx.Exec(ctx, `INSERT INTO admin_idempotency(token_id,key_hash,request_hash,response_enc,status) VALUES($1,$2,$3,$4,$5) ON CONFLICT(token_id,key_hash) DO UPDATE SET request_hash=excluded.request_hash,response_enc=excluded.response_enc,status=excluded.status,expires_at=excluded.expires_at`, t.ID, keyHash, fingerprint, sealed, rec.Code)
				if err != nil {
					return err
				}
			}
			s.Store.Audit(ctx, u, "admin_api."+route.Action, "admin_token", t.ID, map[string]any{"tokenId": t.ID, "requestId": httpx.RequestID(ctx), "targetId": r.PathValue("id")}, clientIP(r))
			if _, err = tx.Exec(ctx, `UPDATE admin_tokens SET last_used_at=now(),last_ip=$2 WHERE id=$1`, t.ID, clientIP(r)); err != nil {
				return err
			}
			if err = tx.Commit(ctx); err != nil {
				return err
			}
			for k, values := range rec.Header() {
				w.Header()[k] = values
			}
			w.WriteHeader(rec.Code)
			_, err = w.Write(rec.Body.Bytes())
			return err
		}))
	}
}
func publicOK(w http.ResponseWriter, v any)      { httpx.JSON(w, 200, map[string]any{"data": v}) }
func publicCreated(w http.ResponseWriter, v any) { httpx.JSON(w, 201, map[string]any{"data": v}) }
func publicList(w http.ResponseWriter, r *http.Request, v any) error {
	raw := encodePublic(v)
	items := []map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&items); err != nil {
		return err
	}
	sort.SliceStable(items, func(i, j int) bool { return fmt.Sprint(items[i]["id"]) < fmt.Sprint(items[j]["id"]) })
	q := r.URL.Query()
	limit := 50
	if v := q.Get("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 100 {
			return httpx.Invalid(map[string]string{"limit": "Between 1 and 100."})
		}
		limit = n
	}
	q.Del("cursor")
	filter := fmt.Sprintf("%x", hashString(r.URL.Path+"?"+q.Encode()))
	offset := 0
	if c := r.URL.Query().Get("cursor"); c != "" {
		b, e := base64.RawURLEncoding.DecodeString(c)
		if e != nil {
			return httpx.BadRequest("Invalid cursor.")
		}
		parts := strings.Split(string(b), ":")
		if len(parts) != 2 || parts[1] != filter {
			return httpx.BadRequest("Cursor belongs to another query.")
		}
		offset, e = strconv.Atoi(parts[0])
		if e != nil || offset < 0 || offset > len(items) {
			return httpx.BadRequest("Invalid cursor offset.")
		}
	}
	end := min(len(items), offset+limit)
	var next any
	if end < len(items) {
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end) + ":" + filter))
	}
	httpx.JSON(w, 200, map[string]any{"data": items[offset:end], "pagination": map[string]any{"nextCursor": next, "hasMore": end < len(items)}})
	return nil
}
func (s *Server) publicExisting(r *http.Request, u *store.User, fn func(http.ResponseWriter, *http.Request, *store.User) error, body any) (map[string]any, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	if body != nil {
		b := encodePublic(body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		r.ContentLength = int64(len(b))
	}
	rec := httptest.NewRecorder()
	if err := fn(rec, r, u); err != nil {
		return nil, err
	}
	if rec.Code == 204 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		return nil, httpx.Unavailable("The operation did not return JSON.")
	}
	return out, nil
}
func (s *Server) setExternal(r *http.Request, kind, id, external string) error {
	if len(external) > 200 {
		return httpx.Invalid(map[string]string{"externalId": "Up to 200 characters."})
	}
	if external == "" {
		_, err := s.Store.SQL(r.Context()).Exec(r.Context(), `DELETE FROM admin_external_ids WHERE kind=$1 AND object_id=$2`, kind, id)
		return err
	}
	_, err := s.Store.SQL(r.Context()).Exec(r.Context(), `INSERT INTO admin_external_ids(kind,object_id,external_id) VALUES($1,$2,$3) ON CONFLICT(kind,object_id) DO UPDATE SET external_id=excluded.external_id`, kind, id, external)
	return err
}
func (s *Server) external(r *http.Request, kind, id string) string {
	var v string
	s.Store.SQL(r.Context()).QueryRow(r.Context(), `SELECT external_id FROM admin_external_ids WHERE kind=$1 AND object_id=$2`, kind, id).Scan(&v)
	return v
}
func (s *Server) publicDispatch(w http.ResponseWriter, r *http.Request, u *store.User, t *adminToken, action string) error {
	switch action {
	case "openapi":
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write(publicOpenAPI)
		return err
	case "token":
		publicOK(w, t)
		return nil
	case "permissions":
		publicOK(w, publicPermissionCatalog())
		return nil
	case "instance":
		publicOK(w, map[string]any{"name": s.instance(r.Context()).Name, "version": version.Version, "apiVersion": "v1", "capabilities": map[string]bool{"adminTokens": true, "emailDelivery": false, "remoteProvisioning": false, "products": true, "lifecycleWebhooks": true}})
		return nil
	case "defaults":
		p := s.policy()
		publicOK(w, map[string]any{"accounts": map[string]any{"personalTeams": s.Store.Limits(r.Context()).PersonalTeams, "localAuth": p.LocalAuth, "disableRegistration": p.DisableRegistration, "requiredMethod": p.RequiredMethod, "invitations": p.Invitations, "ssoProvisioning": p.SSOProvisioning, "ssoDefaultRole": p.SSODefaultRole}, "limits": s.Store.Limits(r.Context()), "resources": s.defaults(r.Context()), "experiences": s.instance(r.Context())})
		return nil
	case "capacity":
		out, err := s.publicExisting(r, u, s.fleetCapacity, nil)
		if err != nil {
			return err
		}
		publicOK(w, out)
		return nil
	}
	if strings.HasPrefix(action, "product") || strings.HasPrefix(action, "plan") || action == "availability" {
		return s.publicProducts(w, r, u, t, action)
	}
	if strings.HasPrefix(action, "user") || strings.HasPrefix(action, "invit") || strings.HasPrefix(action, "team") || strings.HasPrefix(action, "member") {
		return s.publicAccounts(w, r, u, t, action)
	}
	if strings.HasPrefix(action, "webhook") || strings.HasPrefix(action, "deliver") {
		return s.publicWebhooks(w, r, u, t, action)
	}
	return s.publicResources(w, r, u, t, action)
}

func publicHTTPError(w http.ResponseWriter, err error) error {
	var e *httpx.Error
	if errors.As(err, &e) {
		if e.Status == 401 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="Ignition Instance Admin API"`)
		}
		if e.Status == 429 {
			w.Header().Set("Retry-After", "60")
		}
	}
	return err
}
