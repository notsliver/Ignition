package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/database"
	"github.com/notsliver/Ignition/services/internal/store"
)

func iconFixture(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 6, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 6; x++ {
			c := color.NRGBA{R: 255, A: 255}
			if x >= 2 && x < 4 {
				c = color.NRGBA{G: 255, A: 128}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestNormalizeIcon(t *testing.T) {
	raw := iconFixture(t)
	out, err := normalizeIcon(raw)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
		t.Fatal(img.Bounds())
	}
	// The centre square is green and transparent, whereas both discarded edges are red.
	for _, p := range []image.Point{{0, 0}, {128, 128}, {255, 255}} {
		c := color.NRGBAModel.Convert(img.At(p.X, p.Y)).(color.NRGBA)
		if c.R != 0 || c.G != 255 || c.A != 128 {
			t.Fatalf("crop/alpha at %v: %v", p, c)
		}
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, img, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeIcon(jpg.Bytes()); err != nil {
		t.Fatal(err)
	}
	webp, err := base64.StdEncoding.DecodeString("UklGRh4AAABXRUJQVlA4TBEAAAAvAkAAAAdQsqoUtP+BiOh/AAA=")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeIcon(webp); err != nil {
		t.Fatal(err)
	}
	var wide bytes.Buffer
	png.Encode(&wide, image.NewGray(image.Rect(0, 0, 8193, 1)))
	for name, b := range map[string][]byte{"empty": nil, "html": []byte("<svg onload='x'/>"), "too large": bytes.Repeat([]byte{0}, maxIconBytes+1), "dimensions": wide.Bytes(), "truncated": raw[:len(raw)/2]} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeIcon(b); err == nil {
				t.Fatal("accepted invalid image")
			}
		})
	}
}

// This uses only the explicitly configured disposable test database.
func TestIconUploadPersistenceAndAccess(t *testing.T) {
	dbURL := os.Getenv("IGNITION_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("IGNITION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	st := &store.Store{DB: pool}
	dir := t.TempDir()
	s := &Server{Store: st, PublicURL: "http://example.test", UploadsDir: dir}
	users := make([]*store.User, 4)
	tokens := make([]string, 4)
	for i := range users {
		users[i], err = st.CreateUser(ctx, strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")+"@icons.test", "Icons", "hash", "viewer")
		if err != nil {
			t.Fatal(err)
		}
		token, hash, err := auth.NewToken()
		if err != nil {
			t.Fatal(err)
		}
		tokens[i] = token
		if err := st.CreateSession(ctx, users[i].ID, hash, "test", "", time.Hour, "password"); err != nil {
			t.Fatal(err)
		}
	}
	team, err := st.CreateTeam(ctx, users[0].ID, "Icon test")
	if err != nil {
		t.Fatal(err)
	}
	// A platform admin can read images; being an owner of another team cannot.
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, users[3].ID); err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateTeam(ctx, users[2].ID, "Unrelated icon team")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO team_memberships(team_id,user_id,role) VALUES($1,$2,'viewer')`, team.ID, users[1].ID); err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	call := func(who int, method, path string, body []byte, want int, extra ...map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://example.test"+path, bytes.NewReader(body))
		if who >= 0 {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tokens[who]})
		}
		r.Header.Set("Origin", "http://example.test")
		r.Header.Set("Content-Type", "application/octet-stream")
		for _, headers := range extra {
			for key, value := range headers {
				r.Header.Set(key, value)
			}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s user %d: %d want %d: %s", method, path, who, w.Code, want, w.Body.String())
		}
		return w
	}
	urlFrom := func(w *httptest.ResponseRecorder) string {
		var out struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.URL
	}
	raw := iconFixture(t)
	call(-1, "POST", "/api/account/avatar", raw, 401)
	avatar := urlFrom(call(0, "POST", "/api/account/avatar", raw, 200))
	file := filepath.Join(dir, "avatars", filepath.Base(avatar))
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
	me := call(0, "GET", "/api/auth/me", nil, 200)
	if !strings.Contains(me.Body.String(), avatar) {
		t.Fatal("session is missing avatar URL")
	}
	call(1, "GET", avatar, nil, 200)
	call(2, "GET", avatar, nil, 404)
	call(-1, "GET", avatar, nil, 401)
	call(3, "GET", avatar, nil, 200)
	call(1, "HEAD", avatar, nil, 200)
	call(2, "HEAD", avatar, nil, 404)
	// Conditional/range requests must still authorize before serving any bytes or 304.
	for _, headers := range []map[string]string{
		{"If-Modified-Since": "Wed, 31 Dec 2099 23:59:59 GMT"},
		{"Range": "bytes=0-15"},
	} {
		call(2, "GET", avatar, nil, 404, headers)
		call(-1, "GET", avatar, nil, 401, headers)
	}
	// Changing a URL's image kind cannot turn it into an authorized resource.
	call(1, "GET", strings.Replace(avatar, "/avatars/", "/teams/", 1), nil, 404)
	call(0, "POST", "/api/account/avatar", raw, 403, map[string]string{"Origin": "https://attacker.test"})
	call(0, "DELETE", "/api/account/avatar", nil, 403, map[string]string{"Origin": ""})
	// A fresh server object reads from the same persistent directory and database.
	restarted := &Server{Store: st, PublicURL: "http://example.test", UploadsDir: dir}
	handler = restarted.Handler()
	got := call(0, "GET", avatar, nil, 200)
	if got.Header().Get("Content-Type") != "image/png" {
		t.Fatal("wrong image content type")
	}
	if got.Header().Get("Cache-Control") != "private, no-store" || got.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Fatal("private images must not be stored or embedded across origins")
	}
	img, err := png.Decode(bytes.NewReader(got.Body.Bytes()))
	if err != nil || img.Bounds().Dx() != 256 {
		t.Fatal("image not normalized", err)
	}
	// Bad replacements keep the existing image, including when the body exceeds the limit.
	call(0, "POST", "/api/account/avatar", []byte("<svg/>"), 422)
	call(0, "POST", "/api/account/avatar", bytes.Repeat([]byte{0}, maxIconBytes+1), 422)
	call(0, "GET", avatar, nil, 200)
	next := urlFrom(call(0, "POST", "/api/account/avatar", raw, 200))
	if next == avatar {
		t.Fatal("replacement reused URL")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("old avatar file wasn't removed")
	}
	call(0, "GET", avatar, nil, 404)
	if urlFrom(call(0, "DELETE", "/api/account/avatar", nil, 200)) != "" {
		t.Fatal("avatar not cleared")
	}
	if _, err := os.Stat(filepath.Join(dir, "avatars", filepath.Base(next))); !os.IsNotExist(err) {
		t.Fatal("removed avatar file remains")
	}
	iconPath := "/api/teams/" + team.ID + "/icon"
	call(1, "POST", iconPath, raw, 403)
	call(2, "POST", iconPath, raw, 403)
	icon := urlFrom(call(0, "POST", iconPath, raw, 200))
	call(1, "GET", icon, nil, 200)
	call(2, "GET", icon, nil, 404)
	call(3, "GET", icon, nil, 200)
	call(-1, "GET", icon, nil, 401)
	call(0, "POST", "/api/teams/"+other.ID+"/icon", raw, 403)
	call(0, "DELETE", "/api/teams/"+other.ID+"/icon", nil, 403)
	call(0, "POST", iconPath, raw, 403, map[string]string{"Origin": "https://attacker.test"})
	// Team admins can manage their own icon; demotion immediately removes that ability.
	if _, err := pool.Exec(ctx, `UPDATE team_memberships SET role='admin' WHERE team_id=$1 AND user_id=$2`, team.ID, users[1].ID); err != nil {
		t.Fatal(err)
	}
	icon = urlFrom(call(1, "POST", iconPath, raw, 200))
	if _, err := pool.Exec(ctx, `UPDATE team_memberships SET role='viewer' WHERE team_id=$1 AND user_id=$2`, team.ID, users[1].ID); err != nil {
		t.Fatal(err)
	}
	teams := call(0, "GET", "/api/teams", nil, 200)
	if !strings.Contains(teams.Body.String(), icon) {
		t.Fatal("team list missing icon")
	}
	call(1, "DELETE", iconPath, nil, 403)
	call(1, "POST", iconPath, raw, 403)
	// Keep an avatar alive while revoking membership so both kinds are checked.
	avatar = urlFrom(call(0, "POST", "/api/account/avatar", raw, 200))
	call(1, "GET", avatar, nil, 200)
	// Revocation also applies to previously issued image URLs.
	if _, err := pool.Exec(ctx, `DELETE FROM team_memberships WHERE team_id=$1 AND user_id=$2`, team.ID, users[1].ID); err != nil {
		t.Fatal(err)
	}
	call(1, "GET", icon, nil, 404)
	call(1, "HEAD", icon, nil, 404)
	call(1, "GET", avatar, nil, 404)
	call(1, "GET", avatar, nil, 404, map[string]string{"Range": "bytes=0-15"})
	call(0, "GET", avatar, nil, 200)
	// Extra IDs cannot make the account endpoint overwrite somebody else's picture.
	otherAvatar := urlFrom(call(2, "POST", "/api/account/avatar", raw, 200))
	call(0, "POST", "/api/account/avatar?userId="+users[2].ID, raw, 200)
	call(2, "GET", otherAvatar, nil, 200)
	call(0, "GET", otherAvatar, nil, 404)
	// Suspended accounts and expired sessions lose access to their own images too.
	if _, err := pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, users[0].ID); err != nil {
		t.Fatal(err)
	}
	call(0, "GET", icon, nil, 401)
	if _, err := pool.Exec(ctx, `UPDATE users SET status='active' WHERE id=$1`, users[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sessions SET expires_at=now()-interval '1 minute' WHERE user_id=$1`, users[2].ID); err != nil {
		t.Fatal(err)
	}
	call(2, "GET", otherAvatar, nil, 401)
	call(2, "DELETE", "/api/account/avatar", nil, 401)
	if err := st.DeleteSession(ctx, auth.HashToken(tokens[1])); err != nil {
		t.Fatal(err)
	}
	call(1, "GET", icon, nil, 401)
	call(1, "HEAD", avatar, nil, 401)

	call(0, "DELETE", iconPath, nil, 200)
	if _, err := os.Stat(filepath.Join(dir, "teams", filepath.Base(icon))); !os.IsNotExist(err) {
		t.Fatal("removed team file remains")
	}
	call(0, "GET", "/api/account/images/avatars/not-a-generated-name.png", nil, 404)
	for _, path := range []string{
		"/uploads/avatars/" + filepath.Base(otherAvatar),
		"/var/lib/ignition/uploads/avatars/" + filepath.Base(otherAvatar),
		"/api/account/images/avatars/",
		"/api/account/images/invalid/" + filepath.Base(avatar),
		"/api/account/images/avatars/%2e%2e%2f" + filepath.Base(avatar),
	} {
		call(0, "GET", path, nil, 404)
	}
}

func TestImageHandlersRejectResourceCredentials(t *testing.T) {
	s := &Server{UploadsDir: t.TempDir()} // No database: rejection must happen before any lookup.
	for _, actor := range []*store.User{
		{ID: "issuer", CredentialTokenID: "resource-token", TeamRole: "owner"},
		{ServiceResourceID: "legacy-resource", TeamRole: "owner"},
	} {
		r := httptest.NewRequest("GET", "/api/account/images/avatars/"+strings.Repeat("a", 32)+".png", nil)
		r.SetPathValue("kind", "avatars")
		r.SetPathValue("filename", strings.Repeat("a", 32)+".png")
		for _, fn := range []func(http.ResponseWriter, *http.Request, *store.User) error{s.getImage, s.accountAvatar, s.teamIcon} {
			err := fn(httptest.NewRecorder(), r, actor)
			if err == nil || !strings.Contains(err.Error(), "Sign in") && !strings.Contains(err.Error(), "signed-in") {
				t.Fatalf("resource credential was not rejected: %v", err)
			}
		}
	}
	for _, route := range []string{"GET /api/account/images/{kind}/{filename}", "POST /api/account/avatar", "DELETE /api/account/avatar", "POST /api/teams/{teamId}/icon", "DELETE /api/teams/{teamId}/icon"} {
		if _, ok := tokenRoutes[route]; ok {
			t.Fatalf("image route unexpectedly permits resource tokens: %s", route)
		}
	}
}
