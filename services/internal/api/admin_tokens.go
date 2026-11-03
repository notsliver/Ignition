package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

type adminToken struct {
	CreatorActive bool       `json:"creatorActive"`
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Prefix        string     `json:"prefix"`
	CreatedBy     string     `json:"createdBy"`
	Permissions   []string   `json:"permissions"`
	Networks      []string   `json:"networks"`
	ExpiresAt     *time.Time `json:"expiresAt"`
	CreatedAt     time.Time  `json:"createdAt"`
	RevokedAt     *time.Time `json:"revokedAt"`
	LastUsedAt    *time.Time `json:"lastUsedAt"`
	LastIP        string     `json:"lastIp"`
}

const adminTokenColumns = `id,name,prefix,created_by,permissions,networks,expires_at,created_at,revoked_at,last_used_at,last_ip`

func scanAdminToken(row interface{ Scan(...any) error }) (adminToken, error) {
	var t adminToken
	err := row.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedBy, &t.Permissions, &t.Networks, &t.ExpiresAt, &t.CreatedAt, &t.RevokedAt, &t.LastUsedAt, &t.LastIP)
	return t, err
}

// Browser-only management. No public admin token can mint or rotate another credential.
func (s *Server) adminTokens(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if !platformAccountAdmin(u) {
		return httpx.Forbidden("Instance administrators manage admin tokens.")
	}
	if _, err := accountSession(r, u); err != nil {
		return err
	}
	if r.Method == "GET" {
		rows, err := s.Store.DB.Query(r.Context(), `SELECT `+adminTokenColumns+` FROM admin_tokens ORDER BY created_at DESC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		list := []adminToken{}
		for rows.Next() {
			t, err := scanAdminToken(rows)
			if err != nil {
				return err
			}
			creator, e := s.Store.GetUser(r.Context(), t.CreatedBy)
			t.CreatorActive = e == nil && creator.Status == "active" && platformAccountAdmin(creator)
			list = append(list, t)
		}
		httpx.JSON(w, 200, map[string]any{"tokens": list, "permissions": availablePublicPermissions(u), "routes": publicAdminRoutes, "baseUrl": strings.TrimRight(s.publicURL(), "/") + "/api/admin/v1"})
		return rows.Err()
	}
	if err := s.requireRecentAccount(r, u); err != nil {
		return err
	}
	id := r.PathValue("id")
	if id != "" && !uuidRE.MatchString(id) {
		return httpx.NotFound("Admin token")
	}
	if r.Method == "DELETE" {
		tag, err := s.Store.DB.Exec(r.Context(), `UPDATE admin_tokens SET revoked_at=coalesce(revoked_at,now()) WHERE id=$1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("Admin token")
		}
		s.Store.Audit(r.Context(), u, "admin_token.revoke", "admin_token", id, nil, clientIP(r))
		w.WriteHeader(204)
		return nil
	}
	var in struct {
		Name        string          `json:"name"`
		Permissions []string        `json:"permissions"`
		Networks    []string        `json:"networks"`
		ExpiresAt   json.RawMessage `json:"expiresAt"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var old *adminToken
	if id != "" {
		t, err := scanAdminToken(s.Store.DB.QueryRow(r.Context(), `SELECT `+adminTokenColumns+` FROM admin_tokens WHERE id=$1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now())`, id))
		if err != nil {
			return httpx.NotFound("Active admin token")
		}
		old = &t
		in.Name, in.Permissions, in.Networks = t.Name, t.Permissions, t.Networks
	}
	if in.Networks == nil {
		in.Networks = []string{}
	}
	in.Name = strings.TrimSpace(in.Name)
	expires := time.Now().Add(30 * 24 * time.Hour)
	expiresAt := &expires
	f := map[string]string{}
	if len(in.ExpiresAt) > 0 {
		if err := json.Unmarshal(in.ExpiresAt, &expiresAt); err != nil {
			f["expiresAt"] = "Choose a valid expiry or null for no expiry."
		}
	}
	if in.Name == "" || len(in.Name) > 80 {
		f["name"] = "Enter a name up to 80 characters."
	}
	if expiresAt != nil && (!expiresAt.After(time.Now()) || expiresAt.After(time.Now().Add(366*24*time.Hour))) {
		f["expiresAt"] = "Choose an expiry within the next year, or no expiry."
	}
	if len(in.Permissions) == 0 {
		f["permissions"] = "Select at least one permission."
	}
	for _, p := range in.Permissions {
		if !publicPermissionKnown(p) || !can(u, p) {
			f["permissions"] = "Choose public API permissions you currently hold."
		}
	}
	for _, v := range in.Networks {
		if net.ParseIP(v) == nil {
			if _, _, err := net.ParseCIDR(v); err != nil {
				f["networks"] = "Enter IP addresses or CIDR networks."
			}
		}
	}
	if len(in.Networks) > 50 {
		f["networks"] = "Use at most 50 networks."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	raw, _, err := auth.NewToken()
	if err != nil {
		return err
	}
	raw = "ign_admin_" + raw
	hash := auth.HashToken(raw)
	ctx := r.Context()
	tx, err := s.Store.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	creator := u.ID
	if old != nil {
		creator = old.CreatedBy
		var status, role string
		if err = tx.QueryRow(ctx, `SELECT status,role FROM users WHERE id=$1`, creator).Scan(&status, &role); err != nil || status != "active" || roleRank[role] < roleRank["admin"] {
			return httpx.Conflict("The token creator is no longer an active instance administrator.")
		}
	}
	var t adminToken
	q := `INSERT INTO admin_tokens(name,prefix,created_by,token_hash,permissions,networks,expires_at,rotated_from) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING ` + adminTokenColumns
	rotated := any(nil)
	if old != nil {
		rotated = old.ID
	}
	t, err = scanAdminToken(tx.QueryRow(ctx, q, in.Name, raw[:18], creator, hash, in.Permissions, in.Networks, expiresAt, rotated))
	if err != nil {
		return err
	}
	if old != nil {
		tag, e := tx.Exec(ctx, `UPDATE admin_tokens SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, old.ID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return httpx.Conflict("This token was already rotated or revoked.")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	event := "admin_token.create"
	if old != nil {
		event = "admin_token.rotate"
	}
	s.Store.Audit(ctx, u, event, "admin_token", t.ID, map[string]any{"permissions": in.Permissions}, clientIP(r))
	t.CreatorActive = true
	httpx.JSON(w, 201, map[string]any{"token": raw, "metadata": t})
	return nil
}
func publicPermissionKnown(p string) bool {
	for _, v := range publicPermissionCatalog() {
		if v.Key == p {
			return true
		}
	}
	return false
}
func publicPermissionCatalog() []permission {
	used := map[string]bool{}
	for _, r := range publicAdminRoutes {
		if r.Permission != "" {
			used[r.Permission] = true
		}
	}
	used["storage.delete"] = true
	out := []permission{}
	for _, p := range permissionCatalog {
		if used[p.Key] {
			out = append(out, p)
		}
	}
	return out
}
func (s *Server) authenticateAdminToken(r *http.Request) (*store.User, *adminToken, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ign_admin_") {
		return nil, nil, httpx.Unauthorized()
	}
	raw := strings.TrimPrefix(h, "Bearer ")
	t, err := scanAdminToken(s.Store.DB.QueryRow(r.Context(), `SELECT `+adminTokenColumns+` FROM admin_tokens WHERE token_hash=$1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now())`, auth.HashToken(raw)))
	if err != nil {
		return nil, nil, httpx.Unauthorized()
	}
	u, err := s.Store.GetUser(r.Context(), t.CreatedBy)
	if err != nil || u.Status != "active" || !platformAccountAdmin(u) {
		return nil, nil, httpx.Unauthorized()
	}
	ip := net.ParseIP(clientIP(r))
	ok := len(t.Networks) == 0
	for _, v := range t.Networks {
		if p := net.ParseIP(v); p != nil {
			ok = ok || p.Equal(ip)
		} else if _, n, e := net.ParseCIDR(v); e == nil {
			ok = ok || n.Contains(ip)
		}
	}
	if !ok {
		return nil, nil, httpx.Forbidden("This token cannot be used from this network.")
	}
	if !s.limiter.allowMax("admin-token:"+t.ID, 120) {
		return nil, nil, httpx.TooMany("Admin API rate limit exceeded.")
	}
	effective := []string{}
	for _, p := range t.Permissions {
		if can(u, p) {
			effective = append(effective, p)
		}
	}
	t.Permissions = effective
	t.CreatorActive = true
	u.AdminTokenID = t.ID
	return u, &t, nil
}
func hashString(v string) []byte             { h := sha256.Sum256([]byte(v)); return h[:] }
func publicHas(t *adminToken, p string) bool { return p == "" || slices.Contains(t.Permissions, p) }
func encodePublic(v any) []byte              { b, _ := json.Marshal(v); return b }
func publicRequestHash(r *http.Request, b []byte) []byte {
	return hashString(r.Method + " " + r.URL.RequestURI() + "\n" + string(b))
}
func publicKeyContext(token string, key []byte) string {
	return "admin-idempotency:" + token + ":" + hex.EncodeToString(key)
}

func availablePublicPermissions(u *store.User) []permission {
	out := []permission{}
	for _, p := range publicPermissionCatalog() {
		if can(u, p.Key) {
			out = append(out, p)
		}
	}
	return out
}
